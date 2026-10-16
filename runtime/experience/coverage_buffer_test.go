package main

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDecisionMemoryMatchesQueryAndTranscriptRoots(t *testing.T) {
	m := Memory{Kind: "decision", Subject: "MeshOffer", Content: "lower device_id offers first", Status: "active"}
	if !decisionMemoryMatchesQuery(m, "meshoffer") {
		t.Fatal("expected subject match")
	}
	if decisionMemoryMatchesQuery(Memory{Kind: "lesson"}, "mesh") {
		t.Fatal("lessons should not match")
	}
	if !shouldOpenTranscript(nil, "mesh") {
		t.Fatal("expected transcript open")
	}
	if shouldOpenTranscript([]Memory{{Kind: "decision", Subject: "mesh", Content: "mesh", Status: "active"}}, "mesh") {
		t.Fatal("covered decision should skip transcript")
	}
	t.Setenv("OVERDRIVE_TRANSCRIPT_ROOT", t.TempDir())
	if len(transcriptScanRoots()) != 1 {
		t.Fatal("expected transcript root")
	}
	t.Setenv("OVERDRIVE_TRANSCRIPT_SCAN", "1")
	t.Setenv("OVERDRIVE_TRANSCRIPT_ROOT", "")
	home := os.Getenv("HOME")
	if home == "" {
		t.Skip("HOME unset")
	}
	roots := transcriptScanRoots()
	if len(roots) == 0 {
		t.Fatal("expected default transcript roots")
	}
}

func TestIndexHarnessTailsWalksJSONL(t *testing.T) {
	home := t.TempDir()
	setupTestEnv(t, home)
	repo := makeTestRepo(t, filepath.Join(t.TempDir(), "overdrive"), "https://github.com/acme/overdrive.git")
	root := t.TempDir()
	path := filepath.Join(root, "agent-overdrive.jsonl")
	if err := os.WriteFile(path, []byte("vamos usar mesh TURN quando necessário\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("OVERDRIVE_TRANSCRIPT_ROOT", root)
	e, err := newEngine(home)
	if err != nil {
		t.Fatal(err)
	}
	defer e.Close()
	project, err := identifyProject(repo)
	if err != nil {
		t.Fatal(err)
	}
	moved, err := e.indexHarnessTails(project)
	if err != nil {
		t.Fatal(err)
	}
	if !moved {
		t.Fatal("expected harness tail indexing")
	}
	hints, err := e.matchingSpanHints(project, "mesh TURN")
	if err != nil || len(hints) == 0 {
		t.Fatalf("hints: %v %v", hints, err)
	}
	if !strings.Contains(hints[0], "mesh") {
		t.Fatalf("hint text: %q", hints[0])
	}
}

func TestPeerSyncLineFormatParse(t *testing.T) {
	line := formatPeerSyncLine("dev_a", "ok", 7)
	name, state, imported, ok := parsePeerSyncLine(line)
	if !ok || name != "dev_a" || state != "ok" || imported != 7 {
		t.Fatalf("parse ok line: %q -> %s %s %d %v", line, name, state, imported, ok)
	}
	timeoutLine := formatPeerSyncLine("dev_b", "stale", 0)
	_, state2, _, ok2 := parsePeerSyncLine(timeoutLine)
	if !ok2 || state2 != "timeout" {
		t.Fatalf("timeout line: %q", timeoutLine)
	}
	if err := rejectGroupKeyLeak(`{"safe":true}`, "secret"); err != nil {
		t.Fatal(err)
	}
	if err := rejectGroupKeyLeak(`{"key":"secret"}`, "secret"); err == nil {
		t.Fatal("expected leak error")
	}
}

func TestServeTerminalRecordingListAndPause(t *testing.T) {
	home := t.TempDir()
	setupTestEnv(t, home)
	closeTerminalDBs()
	sid := insertHTTPTestSession(t, home)
	list := terminalHTTP(t, home, http.MethodGet, "/api/v1/terminal/sessions/"+sid+"/recording", "127.0.0.1:7742", nil, nil)
	if list.Code != http.StatusOK {
		t.Fatalf("recording list %d", list.Code)
	}
	pause := terminalHTTP(t, home, http.MethodPost, "/api/v1/terminal/sessions/"+sid+"/recording/pause", "127.0.0.1:7742", nil, nil)
	if pause.Code != http.StatusOK {
		t.Fatalf("recording pause %d", pause.Code)
	}
	resume := terminalHTTP(t, home, http.MethodPost, "/api/v1/terminal/sessions/"+sid+"/recording/resume", "127.0.0.1:7742", nil, nil)
	if resume.Code != http.StatusOK {
		t.Fatalf("recording resume %d", resume.Code)
	}
}
