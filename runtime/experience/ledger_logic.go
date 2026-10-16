package main

import (
	"database/sql"
	"fmt"
	"strings"
)

type ledgerWrite struct {
	Project       Project
	RunID         string
	Decision      string
	Evidence      string
	Reason        string
	Risk          string
	Reversibility string
	Lifetime      string
}

func insertLedgerEntry(db *sql.DB, in ledgerWrite) (LedgerEntry, error) {
	in.Lifetime = "run"
	return insertLedgerEntryLifetime(db, in)
}

func insertLedgerEntryLifetime(db *sql.DB, in ledgerWrite) (LedgerEntry, error) {
	life, err := normalizeLifetime(in.Lifetime)
	if err != nil {
		return LedgerEntry{}, err
	}
	res, err := db.Exec(`INSERT INTO ledger_entries(run_id, repository, decision, evidence, reason, risk, reversibility, created_at, lifetime)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		in.RunID, in.Project.Repository, redact(in.Decision), redact(in.Evidence), redact(in.Reason), redact(in.Risk), redact(in.Reversibility), nowRFC3339(), life)
	if err != nil {
		return LedgerEntry{}, err
	}
	id, _ := res.LastInsertId()
	return LedgerEntry{
		ID: id, RunID: in.RunID, Decision: in.Decision, Evidence: in.Evidence, Reason: in.Reason,
		Risk: in.Risk, Reversibility: in.Reversibility, CreatedAt: nowRFC3339(),
	}, nil
}

func renderLedgerMarkdown(entries []LedgerEntry) string {
	var b strings.Builder
	b.WriteString("# Decision Ledger\n\n")
	for _, entry := range entries {
		b.WriteString(fmt.Sprintf("## R-%d\n\n", entry.ID))
		b.WriteString(entry.Decision + "\n\n")
		if entry.Evidence != "" {
			b.WriteString("Evidence: " + entry.Evidence + "\n\n")
		}
		if entry.Reason != "" {
			b.WriteString("Reason: " + entry.Reason + "\n\n")
		}
		if entry.Risk != "" {
			b.WriteString("Risk if wrong: " + entry.Risk + "\n\n")
		}
		if entry.Reversibility != "" {
			b.WriteString("Reversibility: " + entry.Reversibility + "\n\n")
		}
	}
	return b.String()
}
