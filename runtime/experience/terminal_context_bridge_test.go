package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestBuildContextBundleIncludesUntrustedFence(t *testing.T) {
	home := t.TempDir()
	closeTerminalDBs()
	store, err := newTerminalStore(home)
	if err != nil {
		t.Fatal(err)
	}
	repo := makeTestRepo(t, filepath.Join(home, "repo"), "https://github.com/acme/context-bridge.git")
	gitRun(t, repo, "add", "README.md")
	gitRun(t, repo, "commit", "-qm", "init")

	sess := TerminalSession{
		ID: newTerminalSessionID(), Name: "ctx", State: TerminalStateRunning, Mode: TerminalModeManaged,
		OwnerDeviceID: "dev_local", HostDeviceID: "dev_local", WorkingDirectory: repo, Command: "sh",
		CreatedAt: terminalNow(), UpdatedAt: terminalNow(),
	}
	if err := store.InsertSession(sess); err != nil {
		t.Fatal(err)
	}
	if _, err := store.AppendEvent(SessionEvent{
		SessionID: sess.ID, Kind: TerminalEventOutput, Sequence: 1, Raw: []byte("echo hello\n"),
	}); err != nil {
		t.Fatal(err)
	}

	bundle, err := buildContextBundle(home, store, sess, "dev_local", contextBuildRequest{
		Objective: "ship context bridge", StartSeq: 1, EndSeq: 5, TokenBudget: 4096,
	})
	if err != nil {
		t.Fatal(err)
	}
	if bundle.SessionExcerpt == nil || !strings.Contains(bundle.SessionExcerpt.Fenced, contextUntrustedFenceStart) {
		t.Fatal("expected fenced untrusted session excerpt")
	}
	if !strings.Contains(bundle.RenderedMarkdown, "echo hello") {
		t.Fatal("expected terminal output in rendered markdown")
	}
	if !bundle.Workspace.IsGitRepo {
		t.Fatal("expected git repo snapshot")
	}
	if len(bundle.Provenance) == 0 {
		t.Fatal("expected provenance markers")
	}
}

func TestValidateWorkspaceStateDetectsDirtyTree(t *testing.T) {
	repo := makeTestRepo(t, filepath.Join(t.TempDir(), "repo"), "https://github.com/acme/handoff.git")
	gitRun(t, repo, "add", "README.md")
	gitRun(t, repo, "commit", "-qm", "init")
	expected := gitWorkspaceSnapshot(repo)
	if err := os.WriteFile(filepath.Join(repo, "dirty.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	result := validateWorkspaceState(repo, &expected)
	if result.OK {
		t.Fatal("expected validation failure on dirty tree")
	}
	if len(result.Warnings) == 0 {
		t.Fatal("expected warnings")
	}
}
