package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestResolveSharedWorkspacePathRejectsTraversal(t *testing.T) {
	root := t.TempDir()
	if _, err := resolveSharedWorkspacePath(root, "../etc/passwd"); err == nil {
		t.Fatal("expected traversal rejection")
	}
	if _, err := resolveSharedWorkspacePath(root, "artifacts/../../outside"); err == nil {
		t.Fatal("expected nested traversal rejection")
	}
}

func TestResolveSharedWorkspacePathRejectsSymlinkEscape(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	secret := filepath.Join(outside, "secret.txt")
	if err := os.WriteFile(secret, []byte("secret"), 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "escape.link")
	if err := os.Symlink(secret, link); err != nil {
		t.Skip("symlink not permitted")
	}
	if _, err := resolveSharedWorkspacePath(root, "escape.link"); err == nil {
		t.Fatal("expected symlink escape rejection")
	}
}

func TestEnsureSharedWorkspaceLayout(t *testing.T) {
	home := t.TempDir()
	root, err := EnsureSharedWorkspace(home, "circle_test", "ws_main", "dev_a")
	if err != nil {
		t.Fatal(err)
	}
	for _, rel := range []string{"context.md", "manifest.json", "artifacts", "sessions"} {
		path, err := resolveSharedWorkspacePath(root, rel)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("missing %s: %v", rel, err)
		}
	}
}

func TestPlanWorkspaceHashSync(t *testing.T) {
	local := SharedWorkspaceManifest{Files: map[string]SharedWorkspaceFile{
		"context.md": {Path: "context.md", SHA256: "aaa"},
	}}
	remote := SharedWorkspaceManifest{Files: map[string]SharedWorkspaceFile{
		"context.md": {Path: "context.md", SHA256: "bbb"},
		"tasks.md":   {Path: "tasks.md", SHA256: "ccc"},
	}}
	plan := PlanWorkspaceHashSync(local, remote)
	if len(plan.Pull) < 2 {
		t.Fatalf("expected pull entries, got %#v", plan)
	}
}
