package main

import (
	"path/filepath"
	"testing"
)

func TestRecallEmptyQueryAndLimitClamp(t *testing.T) {
	home := t.TempDir()
	setupTestEnv(t, home)
	repo := makeTestRepo(t, filepath.Join(t.TempDir(), "repo"), "https://github.com/acme/a.git")
	e, err := newEngine(home)
	if err != nil {
		t.Fatal(err)
	}
	defer e.Close()
	project, err := identifyProject(repo)
	if err != nil {
		t.Fatal(err)
	}
	_, err = e.recordMemory(project, "lesson", "repository", "", "Empty query recall marker.", 0.9, 50, "verified_execution", "", "")
	if err != nil {
		t.Fatal(err)
	}
	resp, err := e.recall(project, "", 0, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(resp.Memories) != 1 {
		t.Fatalf("empty query memories=%d", len(resp.Memories))
	}
}

func TestAddWorkingMemoryWithoutSession(t *testing.T) {
	home := t.TempDir()
	setupTestEnv(t, home)
	e, err := newEngine(home)
	if err != nil {
		t.Fatal(err)
	}
	defer e.Close()
	project := Project{Repository: "github.com/acme/r", Root: t.TempDir()}
	if err := e.addWorkingMemory(project, "episode", "s", "c"); err != nil {
		t.Fatal(err)
	}
	wm, err := e.listWorkingMemory(project)
	if err != nil || len(wm) != 0 {
		t.Fatalf("wm len=%d err=%v", len(wm), err)
	}
}

func TestOpenEngineUsesHomeEnv(t *testing.T) {
	home := t.TempDir()
	setupTestEnv(t, home)
	e, err := openEngine()
	if err != nil {
		t.Fatal(err)
	}
	if e.home != home {
		t.Fatalf("home %q", e.home)
	}
	e.Close()
}
