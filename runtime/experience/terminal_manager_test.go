package main

import (
	"os/exec"
	"strings"
	"testing"
	"time"
)

func TestFilterTerminalEnv(t *testing.T) {
	in := []string{
		"PATH=/usr/bin",
		"OVERDRIVE_HOME=/secret",
		"GITHUB_TOKEN=ghp_abc",
		"MY_VAR=ok",
		"MQTT_PASSWORD=x",
	}
	out := filterTerminalEnv(in)
	joined := strings.Join(out, "|")
	if strings.Contains(joined, "OVERDRIVE") || strings.Contains(joined, "GITHUB") || strings.Contains(joined, "MQTT") {
		t.Fatalf("blocked env leaked: %v", out)
	}
	if !strings.Contains(joined, "PATH=") || !strings.Contains(joined, "MY_VAR=ok") {
		t.Fatalf("expected vars missing: %v", out)
	}
}

func TestCanonicalTerminalWorkingDirectory(t *testing.T) {
	dir := t.TempDir()
	got, err := canonicalTerminalWorkingDirectory(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got == "" {
		t.Fatal("empty wd")
	}
	_, err = canonicalTerminalWorkingDirectory(dir + "/missing-subdir")
	if err == nil {
		t.Fatal("expected error for missing dir")
	}
}

func TestWatchSessionHostReapsExitedHost(t *testing.T) {
	home := t.TempDir()
	setupTestEnv(t, home)
	closeTerminalDBs()
	sid := insertHTTPTestSession(t, home)
	mgr, err := newTerminalManager(home)
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("true")
	if err := cmd.Start(); err != nil {
		t.Skip("true unavailable")
	}
	mgr.mu.Lock()
	mgr.hosts[sid] = cmd
	mgr.mu.Unlock()
	done := make(chan struct{})
	go func() { mgr.watchSessionHost(sid, cmd); close(done) }()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("watchSessionHost must return once the host exits")
	}
	if cmd.ProcessState == nil {
		t.Fatal("host process must be waited (no zombie)")
	}
	mgr.mu.Lock()
	_, tracked := mgr.hosts[sid]
	mgr.mu.Unlock()
	if tracked {
		t.Fatal("exited host must be untracked")
	}
	sess, err := mgr.GetSession(sid)
	if err != nil {
		t.Fatal(err)
	}
	if sess.State != TerminalStateFailed {
		t.Fatalf("host gone while RUNNING must mark FAILED, got %s", sess.State)
	}
}

func TestTerminalManagerCreateSession(t *testing.T) {
	home := t.TempDir()
	closeTerminalDBs()
	// minimal identity for manager
	if err := hookMkdirAll(shareDir(home), 0o700); err != nil {
		t.Fatal(err)
	}
	mgr, err := newTerminalManager(home)
	if err != nil {
		t.Fatal(err)
	}
	sess, err := mgr.CreateSession(terminalCreateOpts{
		Name:             "unit",
		WorkingDirectory: home,
		Cols:             80,
		Rows:             24,
	})
	if err != nil {
		t.Fatal(err)
	}
	if sess.ID == "" || sess.SocketPath == "" {
		t.Fatalf("session: %+v", sess)
	}
	if sess.State != TerminalStateStarting {
		t.Fatalf("state=%s", sess.State)
	}
}
