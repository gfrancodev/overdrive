package main

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestRunMCPToolRecordAndRecall(t *testing.T) {
	home := t.TempDir()
	setupTestEnv(t, home)
	repo := makeTestRepo(t, filepath.Join(t.TempDir(), "repo"), "https://github.com/acme/mcp-tools.git")
	e, err := newEngine(home)
	if err != nil {
		t.Fatal(err)
	}
	defer e.Close()
	project, err := identifyProject(repo)
	if err != nil {
		t.Fatal(err)
	}
	recorded, err := runMCPTool(e, project, home, "record", map[string]any{
		"kind":    "lesson",
		"subject": "MeshICE",
		"content": "vamos usar TURN quando NAT bloquear STUN",
	})
	if err != nil || !strings.Contains(recorded, "MeshICE") {
		t.Fatalf("record: %v %q", err, recorded)
	}
	recalled, err := runMCPTool(e, project, home, "recall", map[string]any{"query": "MeshICE TURN"})
	if err != nil || !strings.Contains(recalled, "MeshICE") {
		t.Fatalf("recall: %v %q", err, recalled)
	}
	handoff, err := runMCPTool(e, project, home, "handoff", map[string]any{"claim": true})
	if err != nil || handoff == "" {
		t.Fatalf("handoff: %v %q", err, handoff)
	}
	if _, err := runMCPTool(e, project, home, "unknown", nil); err == nil {
		t.Fatal("expected unknown tool error")
	}
}
