package main

import (
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"testing"
)

func TestQueryRelevanceNegativeHashedClamp(t *testing.T) {
	resetHooksForTest()
	defer resetHooksForTest()
	hookHashedCosine = func(string, string) float64 { return -0.5 }
	m := Memory{Content: "alpha beta", Subject: "gamma"}
	if queryRelevance(m, "alpha") <= 0 {
		t.Fatal("should clamp negative hashed component")
	}
}

func TestReindexActiveMemoriesFailuresAndColdSkip(t *testing.T) {
	home := t.TempDir()
	setupTestEnv(t, home)
	repo := makeTestRepo(t, filepath.Join(t.TempDir(), "repo"), "https://github.com/acme/a.git")
	e, err := newEngine(home)
	if err != nil {
		t.Fatal(err)
	}
	defer e.Close()
	project, _ := identifyProject(repo)
	m, _ := e.recordMemory(project, "lesson", "repository", "", "hot lesson", 0.8, 50, "agent_observation", "", "")
	cold, _ := e.recordMemory(project, "lesson", "repository", "", "cold lesson", 0.8, 50, "agent_observation", "", "")
	_, _ = e.db.Exec(`UPDATE memories SET hot_index = 0 WHERE id = ?`, cold.ID)
	if err := e.reindexActiveMemories(); err != nil {
		t.Fatal(err)
	}
	_ = e.db.Close()
	if err := e.reindexActiveMemories(); err == nil {
		t.Fatal("closed db")
	}
	_ = m
}

func TestListExportableMemoriesBranches(t *testing.T) {
	home := t.TempDir()
	setupTestEnv(t, home)
	repo := makeTestRepo(t, filepath.Join(t.TempDir(), "repo"), "https://github.com/acme/a.git")
	id, priv, _ := loadOrCreateIdentity(home)
	circle, _ := createCircle(home, "team", id, priv)
	circle, _ = addAllowedFolder(home, circle.ID, repo, id)
	e, err := newEngine(home)
	if err != nil {
		t.Fatal(err)
	}
	defer e.Close()
	project := identifyProjectMust(t, repo)
	_, _ = e.recordMemory(project, "preference", "repository", "", "not exported", 0.9, 50, "user_feedback", "", "")
	exported, _ := e.recordMemory(project, "lesson", "repository", "", "exported lesson", 0.9, 50, "verified_execution", "", "")
	exported.HotIndex = true
	exported.Layer = 2
	exported.SourceFolder = repo
	if err := e.upsertMemory(exported); err != nil {
		t.Fatal(err)
	}
	memories, err := e.listExportableMemories(Project{Repository: project.Repository}, circle)
	if err != nil || len(memories) != 1 {
		t.Fatalf("exportable=%d err=%v", len(memories), err)
	}
	_ = e.db.Close()
	if _, err := e.listExportableMemories(Project{Repository: project.Repository, Root: repo}, circle); err == nil {
		t.Fatal("query fail")
	}

	e2, err := newEngine(home)
	if err != nil {
		t.Fatal(err)
	}
	defer e2.Close()
	resetHooksForTest()
	defer resetHooksForTest()
	hookScanMemory = func(scanner) (Memory, error) {
		return Memory{}, errors.New("scan fail")
	}
	if _, err := e2.listExportableMemories(Project{Repository: project.Repository, Root: repo}, circle); err == nil {
		t.Fatal("scan fail")
	}
}

func TestBuildSyncResponseListFailure(t *testing.T) {
	home := t.TempDir()
	setupTestEnv(t, home)
	repo := makeTestRepo(t, filepath.Join(t.TempDir(), "repo"), "https://github.com/acme/a.git")
	id, priv, _ := loadOrCreateIdentity(home)
	circle, _ := createCircle(home, "team", id, priv)
	circle, _ = addAllowedFolder(home, circle.ID, repo, id)
	e := engineWithClosedDB(t, home)
	project := identifyProjectMust(t, repo)
	_, err := e.buildSyncResponse(home, circle, id, priv, SyncRequest{Repository: project.Repository})
	if err == nil {
		t.Fatal("build sync fail")
	}
}

func TestEnsureORTLibMkdirAndDownloadFailures(t *testing.T) {
	home := t.TempDir()
	resetORTLibForTest()
	t.Setenv("OVERDRIVE_SKIP_EMBED_DOWNLOAD", "0")
	t.Setenv("OVERDRIVE_ORT_URL", "http://example.invalid/ort.tgz")
	t.Setenv("OVERDRIVE_ORT_LIB", "")

	resetHooksForTest()
	defer resetHooksForTest()
	hookMkdirTemp = func(string, string) (string, error) {
		return "", errors.New("mkdir temp fail")
	}
	if _, err := ensureORTLib(home); err == nil {
		t.Fatal("mkdir temp fail")
	}
	hookMkdirTemp = os.MkdirTemp

	oldGet := downloadHTTPGet
	downloadHTTPGet = func(*http.Client, string) (*http.Response, error) {
		return nil, errors.New("download fail")
	}
	defer func() { downloadHTTPGet = oldGet }()
	if _, err := ensureORTLib(home); err == nil {
		t.Fatal("download fail")
	}
}

func TestBuildMiniLMEmbedderFailureBranches(t *testing.T) {
	resetHooksForTest()
	defer resetHooksForTest()
	home := t.TempDir()
	setupTestEnv(t, home)
	resetORTLibForTest()
	lib := filepath.Join(home, "libonnxruntime.so")
	if err := os.WriteFile(lib, []byte("ort"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("OVERDRIVE_ORT_LIB", lib)
	if err := os.MkdirAll(modelDir(home), 0o700); err != nil {
		t.Fatal(err)
	}

	resetHooksForTest()
	defer resetHooksForTest()
	hookEnsureFile = func(string, string) error { return errors.New("pack fail") }
	if _, err := buildMiniLMEmbedder(home); err == nil {
		t.Fatal("pack fail")
	}
	resetHooksForTest()

	if err := os.WriteFile(miniLMModelPath(home), []byte("model"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := buildMiniLMEmbedder(home); err == nil {
		t.Fatal("missing vocab fail")
	}

	if err := os.WriteFile(miniLMVocabPath(home), []byte("\n[CLS]\n[SEP]\n[UNK]\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	hookTryORTSession = func(string, string) (modelRunner, error) {
		return nil, errors.New("session fail")
	}
	if _, err := buildMiniLMEmbedder(home); err == nil {
		t.Fatal("session fail")
	}
}

func TestEngineHelperErrorBranches(t *testing.T) {
	home := t.TempDir()
	setupTestEnv(t, home)
	repo := makeTestRepo(t, filepath.Join(t.TempDir(), "repo"), "https://github.com/acme/a.git")
	e, err := newEngine(home)
	if err != nil {
		t.Fatal(err)
	}
	defer e.Close()
	project := identifyProjectMust(t, repo)

	if ids, ranks, err := e.ftsCandidates("", project, 5); err != nil || ids != nil || ranks != nil {
		t.Fatalf("empty fts %v %v %v", ids, ranks, err)
	}
	_ = e.db.Close()
	if _, _, err := e.ftsCandidates("factory", project, 5); err != nil {
		t.Fatal("fts should swallow db errors")
	}

	e2, err := newEngine(home)
	if err != nil {
		t.Fatal(err)
	}
	defer e2.Close()
	if wm, err := e2.listWorkingMemory(project); err != nil || wm != nil {
		t.Fatalf("no session wm=%v err=%v", wm, err)
	}
	_, _ = e2.db.Exec(`INSERT OR REPLACE INTO meta(key, value) VALUES ('current_session_id', 'sess_test')`)
	e2.sessionID = "sess_test"
	_ = e2.db.Close()
	if _, err := e2.listWorkingMemory(project); err == nil {
		t.Fatal("wm db fail")
	}

	e3, err := newEngine(home)
	if err != nil {
		t.Fatal(err)
	}
	defer e3.Close()
	m, _ := e3.recordMemory(project, "lesson", "repository", "", "validate me", 0.8, 50, "agent_observation", "", "")
	_ = e3.db.Close()
	if _, err := e3.validateMemory(m.ID, "success", "", ""); err == nil {
		t.Fatal("validate upsert fail")
	}

	resetHooksForTest()
	defer resetHooksForTest()
	hookReadDir = func(string) ([]os.DirEntry, error) { return nil, errors.New("readdir fail") }
	if _, ok := circleForProject(home, repo); ok {
		t.Fatal("circle for project fail")
	}
}
