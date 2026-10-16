package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestIsDefaultTerminalWorkingDirectory(t *testing.T) {
	for _, d := range []string{"", ".", "./"} {
		if !isDefaultTerminalWorkingDirectory(d) {
			t.Fatalf("expected default for %q", d)
		}
	}
	if isDefaultTerminalWorkingDirectory("/tmp/explicit") {
		t.Fatal("expected explicit path")
	}
}

func TestEnsureTerminalWorkSessionDirectoryNotRepoRoot(t *testing.T) {
	home := t.TempDir()
	repo := filepath.Join(home, "checkout", "overdrive")
	if err := os.MkdirAll(repo, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := writeShareListenProjectRoot(home, repo); err != nil {
		t.Fatal(err)
	}
	id, _, err := loadOrCreateIdentity(home)
	if err != nil {
		t.Fatal(err)
	}
	circle := Circle{
		ID: "circle_test", Name: "team", AllowedFolders: []string{repo},
		CreatedAt: nowRFC3339(), UpdatedAt: nowRFC3339(),
	}
	if err := saveCircle(home, circle); err != nil {
		t.Fatal(err)
	}

	got, err := ensureTerminalWorkSessionDirectory(home, id.DeviceID, circle.ID, repo)
	if err != nil {
		t.Fatal(err)
	}
	repoAbs, _ := hookFilepathAbs(repo)
	if got == repoAbs {
		t.Fatalf("expected work session dir, got repo root %s", got)
	}
	if !strings.Contains(got, string(filepath.Separator)+"sessions"+string(filepath.Separator)) {
		t.Fatalf("expected sessions segment in %s", got)
	}
	if strings.Contains(got, string(filepath.Separator)+"workspaces"+string(filepath.Separator)) {
		t.Fatalf("expected short circle session link path, got %s", got)
	}
	wantLink := filepath.Join(shareDir(home), "circles", circle.ID, "sessions")
	if !strings.HasPrefix(got, wantLink) {
		t.Fatalf("expected prefix %s, got %s", wantLink, got)
	}
}

func TestSharedWorkspaceShortLabel(t *testing.T) {
	if got := sharedWorkspaceShortLabel("ws_github_com_gfrancodev_overdrive"); got != "overdrive" {
		t.Fatalf("got %q", got)
	}
}

func TestAssertCircleScopedPathRejectsEscape(t *testing.T) {
	home := t.TempDir()
	outside := filepath.Join(home, "..", "escape")
	if err := assertCircleScopedPath(home, outside); err == nil {
		t.Fatal("expected path outside circles dir to be rejected")
	}
}

func TestCircleTerminalSessionLinkPathRejectsTraversal(t *testing.T) {
	home := t.TempDir()
	if _, err := circleTerminalSessionLinkPath(home, "circle_ok", "ws_a", "../evil", false); err == nil {
		t.Fatal("expected traversal rejection in session link path")
	}
}

func TestCircleTerminalSessionLinkPathMultiWorkspace(t *testing.T) {
	home := t.TempDir()
	circleID := "circle_multi"
	wsB := sharedWorkspaceIDForRepository("github.com/other/demo")
	got, err := circleTerminalSessionLinkPath(home, circleID, wsB, "sess_test", true)
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(circlesDir(home), circleID, "sessions", "demo", "sess_test")
	if got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}

func TestEnsureCircleTerminalSessionLinkCreatesSymlink(t *testing.T) {
	home := t.TempDir()
	circleID := "circle_link"
	target := filepath.Join(home, "real", "sessions", "sess_a")
	if err := os.MkdirAll(target, 0o700); err != nil {
		t.Fatal(err)
	}
	wsID := sharedWorkspaceIDForRepository("github.com/acme/overdrive")
	got, err := ensureCircleTerminalSessionLink(home, circleID, wsID, "sess_a", target)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(got, string(filepath.Separator)+"workspaces"+string(filepath.Separator)) {
		t.Fatalf("expected display path without workspaces: %s", got)
	}
	link, err := circleTerminalSessionLinkPath(home, circleID, wsID, "sess_a", false)
	if err != nil {
		t.Fatal(err)
	}
	if st, err := os.Lstat(link); err != nil || st.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("expected symlink at %s: %v", link, err)
	}
}

func TestEnsureTerminalWorkSessionDirectoryExplicitPathPreserved(t *testing.T) {
	home := t.TempDir()
	custom := filepath.Join(home, "custom-wd")
	if err := os.MkdirAll(custom, 0o700); err != nil {
		t.Fatal(err)
	}
	mgr, err := newTerminalManager(home)
	if err != nil {
		t.Fatal(err)
	}
	got, err := mgr.resolveSessionWorkingDirectory(terminalCreateOpts{WorkingDirectory: custom})
	if err != nil {
		t.Fatal(err)
	}
	want, _ := hookFilepathAbs(custom)
	if got != want {
		t.Fatalf("got %s want %s", got, want)
	}
}
