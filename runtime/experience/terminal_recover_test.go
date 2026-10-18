//go:build !windows

package main

import (
	"bufio"
	"encoding/json"
	"net"
	"os/exec"
	"testing"
	"time"
)

// fakeSessionHost answers the session-host IPC ping for one token, like a host left by a previous Core.
func fakeSessionHost(t *testing.T, socketPath, token string) {
	t.Helper()
	ln, err := net.Listen("unix", socketPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			var req terminalIPCRequest
			if json.NewDecoder(bufio.NewReader(conn)).Decode(&req) == nil {
				ok := req.Op == "ping" && req.Token == token
				_, _ = conn.Write(append(marshalTerminalJSON(terminalIPCResponse{OK: ok}), '\n'))
			}
			_ = conn.Close()
		}
	}()
}

func startedProcess(t *testing.T) *exec.Cmd {
	t.Helper()
	cmd := exec.Command("sleep", "30")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cmd.Process.Kill(); _, _ = cmd.Process.Wait() })
	return cmd
}

func insertHostedSession(t *testing.T, store *TerminalStore, home, id, token string, pid int) {
	t.Helper()
	now := terminalNow()
	if err := store.InsertSession(TerminalSession{
		ID: id, Name: "t", State: TerminalStateRunning, Mode: TerminalModeManaged,
		OwnerDeviceID: "dev_owner", HostDeviceID: "dev_owner", WorkingDirectory: home, Command: "/bin/sh",
		IPCToken: token, CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.UpdateSessionHost(id, pid, terminalSocketPath(home, id)); err != nil {
		t.Fatal(err)
	}
}

func sessionState(t *testing.T, store *TerminalStore, id string) TerminalSession {
	t.Helper()
	s, err := store.GetSession(id)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestCoreStartAdoptsLiveSessionHosts(t *testing.T) {
	prev := adoptedHostPollEvery
	adoptedHostPollEvery = 20 * time.Millisecond
	t.Cleanup(func() { adoptedHostPollEvery = prev })
	home := t.TempDir()
	closeTerminalDBs()
	store := mustTerminalStore(t, home)
	if err := hookMkdirAll(terminalSessionsDir(home), 0o700); err != nil {
		t.Fatal(err)
	}

	live := startedProcess(t)
	insertHostedSession(t, store, home, "ts_live", "tok-live", live.Process.Pid)
	fakeSessionHost(t, terminalSocketPath(home, "ts_live"), "tok-live")

	gone := exec.Command("true")
	if err := gone.Run(); err != nil {
		t.Fatal(err)
	}
	insertHostedSession(t, store, home, "ts_gone", "tok-gone", gone.Process.Pid)

	impostor := startedProcess(t)
	insertHostedSession(t, store, home, "ts_impostor", "tok-impostor", impostor.Process.Pid)

	resetTerminalManagersForTests()
	if _, err := getTerminalManager(home); err != nil {
		t.Fatal(err)
	}
	if s := sessionState(t, store, "ts_live"); s.State != TerminalStateRunning {
		t.Fatalf("a host that still answers must be adopted, got %s (%s)", s.State, s.ErrorMessage)
	}
	if s := sessionState(t, store, "ts_gone"); s.State != TerminalStateFailed {
		t.Fatalf("a session whose host is gone must fail, got %s", s.State)
	}
	if s := sessionState(t, store, "ts_impostor"); s.State != TerminalStateFailed {
		t.Fatalf("a live pid that does not answer the session socket must not be trusted, got %s", s.State)
	}
	waited := make(chan struct{})
	go func() { _, _ = impostor.Process.Wait(); close(waited) }()
	select {
	case <-waited:
	case <-time.After(3 * time.Second):
		t.Fatal("a host that does not answer must be terminated")
	}

	_ = live.Process.Kill()
	_, _ = live.Process.Wait()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if sessionState(t, store, "ts_live").State == TerminalStateFailed {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("an adopted host that exits must leave its session failed, not running forever")
}

func TestOnlyAHostThatAnswersItsTokenIsAdopted(t *testing.T) {
	home := shortTempHome(t)
	closeTerminalDBs()
	store := mustTerminalStore(t, home)
	if err := hookMkdirAll(terminalSessionsDir(home), 0o700); err != nil {
		t.Fatal(err)
	}
	mgr := &TerminalManager{home: home, store: store}
	insertHostedSession(t, store, home, "ts_ok", "tok", 1)
	fakeSessionHost(t, terminalSocketPath(home, "ts_ok"), "tok")
	if !mgr.hostAnswers("ts_ok") {
		t.Fatal("a host that answers its token is adopted")
	}
	insertHostedSession(t, store, home, "ts_wrong", "expected", 1)
	fakeSessionHost(t, terminalSocketPath(home, "ts_wrong"), "other")
	if mgr.hostAnswers("ts_wrong") {
		t.Fatal("a host that refuses the token is not adopted")
	}
	insertHostedSession(t, store, home, "ts_notoken", "", 1)
	fakeSessionHost(t, terminalSocketPath(home, "ts_notoken"), "")
	if mgr.hostAnswers("ts_notoken") {
		t.Fatal("a session without a token is not adopted")
	}
	if mgr.hostAnswers("ts_missing") {
		t.Fatal("an unknown session is not adopted")
	}
}
