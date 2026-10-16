package main

import (
	"encoding/json"
	"testing"
)

func TestParsePeerSyncLineRoundTrip(t *testing.T) {
	cases := []struct {
		name     string
		state    string
		imported int
	}{
		{"studio-box", "timeout", 0},
		{"dev_peer_alpha", "ok", 12},
		{"", "ok", 0},
	}
	for _, c := range cases {
		line := formatPeerSyncLine(c.name, c.state, c.imported)
		gotName, gotState, gotImported, ok := parsePeerSyncLine(line)
		if !ok {
			t.Fatalf("parse failed for %q", line)
		}
		wantName := c.name
		if wantName == "" {
			wantName = "peer"
		}
		if gotName != wantName || gotState != c.state || gotImported != c.imported {
			t.Fatalf("line %q -> name=%q state=%q imported=%d", line, gotName, gotState, gotImported)
		}
	}
}

func TestParsePeerSyncLineRejectsGarbage(t *testing.T) {
	for _, line := range []string{
		"",
		"no dash prefix",
		"- : missing name",
		"- peer: unknown status",
		"- peer: online, sync ok, -1 pacotes",
		"- peer: online, sync ok, abc pacotes",
	} {
		if _, _, _, ok := parsePeerSyncLine(line); ok {
			t.Fatalf("should not parse %q", line)
		}
	}
}

func TestRejectStatusGroupKey(t *testing.T) {
	status := ShareStatus{Mode: "ready", CircleID: "circle_a"}
	if err := rejectStatusGroupKey(status, "super-secret-key"); err != nil {
		t.Fatal(err)
	}
	leaky := ShareStatus{Mode: "ready", CircleID: "super-secret-key"}
	if err := rejectStatusGroupKey(leaky, "super-secret-key"); err == nil {
		t.Fatal("expected group key leak error")
	}
}

func TestPeerLinesPersistInEngineMeta(t *testing.T) {
	home := t.TempDir()
	setupTestEnv(t, home)
	e, err := newEngine(home)
	if err != nil {
		t.Fatal(err)
	}
	defer e.Close()

	circleID := "circle_test01"
	repo := "/tmp/repo"
	lines := []string{
		formatPeerSyncLine("peer-a", "ok", 2),
		formatPeerSyncLine("peer-b", "timeout", 0),
	}
	e.savePeerLines(circleID, repo, lines)
	got := e.loadPeerLines(circleID, repo)
	if len(got) != len(lines) {
		t.Fatalf("lines %+v", got)
	}
	for i := range lines {
		if got[i] != lines[i] {
			t.Fatalf("line %d: got %q want %q", i, got[i], lines[i])
		}
	}

	var nilEngine *Engine
	nilEngine.savePeerLines(circleID, repo, lines)
	if nilEngine.loadPeerLines(circleID, repo) != nil {
		t.Fatal("nil engine should not load lines")
	}

	e.setMeta(peerLinesKey(circleID, repo), "{not-json")
	if e.loadPeerLines(circleID, repo) != nil {
		t.Fatal("bad json should yield nil lines")
	}

	e.savePeerLines(circleID, repo, nil)
	raw := e.getMeta(peerLinesKey(circleID, repo))
	var empty []string
	if err := json.Unmarshal([]byte(raw), &empty); err != nil || len(empty) != 0 {
		t.Fatalf("nil save should persist empty array, raw=%q", raw)
	}
}
