package main

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestDecisionSubjectTruncation(t *testing.T) {
	if decisionSubject("") != "decision" {
		t.Fatal("empty decision")
	}
	short := decisionSubject("Use WAL for chat outbox")
	if short != "Use WAL for chat outbox" {
		t.Fatal(short)
	}
	long := strings.Repeat("x", 120)
	if len(decisionSubject(long)) != 80 {
		t.Fatalf("truncated len %d", len(decisionSubject(long)))
	}
}

func TestPromoteStandingDecisions(t *testing.T) {
	home := t.TempDir()
	repo := makeTestRepo(t, filepath.Join(t.TempDir(), "repo"), "https://github.com/acme/standing.git")
	setupTestEnv(t, home)
	project := rememberLocalLesson(t, home, repo, "baseline")
	e, err := newEngine(home)
	if err != nil {
		t.Fatal(err)
	}
	defer e.Close()
	_, err = e.db.Exec(`INSERT INTO ledger_entries(run_id, repository, decision, evidence, reason, risk, reversibility, lifetime, created_at)
		VALUES ('run_standing', ?, ?, ?, '', 'low', 'easy', 'standing', ?)`,
		project.Repository, "Prefer idempotent handlers", "verified in CI", nowRFC3339())
	if err != nil {
		t.Fatal(err)
	}
	if err := e.promoteStandingDecisions(project); err != nil {
		t.Fatal(err)
	}
	var n int
	if err := e.db.QueryRow(`SELECT COUNT(*) FROM memories WHERE kind = 'decision' AND content = ?`,
		"Prefer idempotent handlers").Scan(&n); err != nil || n != 1 {
		t.Fatalf("promoted memory count=%d err=%v", n, err)
	}
}
