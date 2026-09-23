package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCLIStatusAndProject(t *testing.T) {
	home := t.TempDir()
	setupTestEnv(t, home)
	stdout, _, code := runCLICapture(t, []string{"status"})
	if code != 0 {
		t.Fatalf("status exit %d", code)
	}
	data := mustJSON(t, stdout)
	if data["version"] != version {
		t.Fatalf("version %v", data["version"])
	}
	if data["backend"] != backendName {
		t.Fatalf("backend %v", data["backend"])
	}

	repo := makeTestRepo(t, filepath.Join(t.TempDir(), "repo"), "https://github.com/acme/payments-api.git")
	stdout, _, code = runCLICapture(t, []string{"project", "--cwd", repo})
	if code != 0 {
		t.Fatal(code)
	}
	project := mustJSON(t, stdout)
	if project["repository"] != "github.com/acme/payments-api" {
		t.Fatalf("repository %v", project["repository"])
	}
}

func TestCLIRecordRecallValidateLedger(t *testing.T) {
	home := t.TempDir()
	setupTestEnv(t, home)
	repo := makeTestRepo(t, filepath.Join(t.TempDir(), "repo"), "https://github.com/acme/payments-api.git")

	stdout, _, code := runCLICapture(t, []string{
		"record", "--cwd", repo,
		"--kind", "anti_pattern",
		"--subject", "persistence",
		"--content", "Do not access Prisma directly; use ProposalRepository.",
		"--confidence", "0.96",
		"--source", "verified_execution",
		"--evidence", "integration tests passed",
	})
	if code != 0 {
		t.Fatal(code)
	}
	saved := mustJSON(t, stdout)
	id, _ := saved["id"].(string)
	if id == "" {
		t.Fatal("missing id")
	}

	stdout, _, code = runCLICapture(t, []string{"recall", "--cwd", repo, "--query", "proposal prisma", "--limit", "5"})
	if code != 0 {
		t.Fatal(code)
	}
	recalled := mustJSON(t, stdout)
	memories, _ := recalled["memories"].([]any)
	if len(memories) == 0 {
		t.Fatal("expected memories")
	}

	stdout, _, code = runCLICapture(t, []string{"validate", "--id", id, "--result", "contradiction"})
	if code != 0 {
		t.Fatal(code)
	}
	updated := mustJSON(t, stdout)
	if updated["status"] != "deprecated" {
		t.Fatalf("status %v", updated["status"])
	}

	stdout, _, code = runCLICapture(t, []string{
		"ledger-add", "--cwd", repo,
		"--run", "run-alpha",
		"--decision", "Keep repository boundary",
		"--evidence", "ADR-1",
	})
	if code != 0 {
		t.Fatal(code)
	}
	stdout, _, code = runCLICapture(t, []string{"ledger-list", "--cwd", repo, "--run", "run-alpha"})
	if code != 0 {
		t.Fatal(code)
	}
	listed := mustJSON(t, stdout)
	entries, _ := listed["entries"].([]any)
	if len(entries) != 1 {
		t.Fatalf("entries %d", len(entries))
	}
	ledgerPath := filepath.Join(home, "runs", "run-alpha", "ledger.md")
	body, err := os.ReadFile(ledgerPath)
	if err != nil || !strings.Contains(string(body), "Keep repository boundary") {
		t.Fatal("ledger markdown missing")
	}
}

func TestCLILegacyJSONMigration(t *testing.T) {
	home := t.TempDir()
	setupTestEnv(t, home)
	legacy := map[string]any{
		"version": 1,
		"memories": []map[string]any{{
			"id": "mem-legacy-1", "kind": "fact", "scope": "global", "scope_id": "",
			"content": "Legacy migration marker fact.", "confidence": 0.9, "priority": 5,
			"status": "active", "source": "user_feedback", "created_at": "2026-01-01T00:00:00Z",
			"updated_at": "2026-01-01T00:00:00Z",
		}},
	}
	raw, _ := json.Marshal(legacy)
	if err := os.WriteFile(filepath.Join(home, "experience-v1.json"), raw, 0o644); err != nil {
		t.Fatal(err)
	}
	repo := makeTestRepo(t, filepath.Join(t.TempDir(), "repo"), "https://github.com/acme/a.git")
	stdout, _, code := runCLICapture(t, []string{"recall", "--cwd", repo, "--query", "legacy migration marker"})
	if code != 0 {
		t.Fatal(code)
	}
	data := mustJSON(t, stdout)
	memories, _ := data["memories"].([]any)
	found := false
	for _, item := range memories {
		m, _ := item.(map[string]any)
		if strings.Contains(m["content"].(string), "Legacy migration marker") {
			found = true
		}
	}
	if !found {
		t.Fatal("legacy memory not migrated")
	}
}

func TestCLISessionStartEnd(t *testing.T) {
	home := t.TempDir()
	setupTestEnv(t, home)
	repo := makeTestRepo(t, filepath.Join(t.TempDir(), "repo"), "https://github.com/acme/a.git")
	_, _, code := runCLICapture(t, []string{"session-start", "--cwd", repo, "--quiet"})
	if code != 0 {
		t.Fatal(code)
	}
	stdout, _, code := runCLICapture(t, []string{"session-end", "--cwd", repo})
	if code != 0 {
		t.Fatal(code)
	}
	data := mustJSON(t, stdout)
	if _, ok := data["share"]; !ok {
		t.Fatal("missing share in session-end")
	}
}

func TestCLIMinilmFailOpenToHashed(t *testing.T) {
	home := t.TempDir()
	resetRuntimeGlobals()
	t.Setenv("OVERDRIVE_HOME", home)
	t.Setenv("OVERDRIVE_EMBEDDER", "minilm")
	t.Setenv("OVERDRIVE_MODEL_DIR", filepath.Join(t.TempDir(), "missing-models"))
	t.Setenv("OVERDRIVE_ORT_LIB", filepath.Join(t.TempDir(), "missing-ort.so"))
	t.Setenv("OVERDRIVE_SKIP_EMBED_DOWNLOAD", "1")
	repo := makeTestRepo(t, filepath.Join(t.TempDir(), "repo"), "https://github.com/acme/a.git")
	_, _, code := runCLICapture(t, []string{
		"record", "--cwd", repo, "--kind", "lesson",
		"--content", "Fail-open embedder should still record.",
		"--confidence", "0.8", "--source", "verified_execution",
	})
	if code != 0 {
		t.Fatal(code)
	}
	stdout, _, code := runCLICapture(t, []string{"status"})
	if code != 0 {
		t.Fatal(code)
	}
	status := mustJSON(t, stdout)
	if status["embedder"] != "hashed" {
		t.Fatalf("embedder %v", status["embedder"])
	}
}
