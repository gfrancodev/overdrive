package main

import (
	"database/sql"
	"fmt"
	"strings"
)

func insertLedgerEntry(db *sql.DB, project Project, runID, decision, evidence, reason, risk, reversibility string) (LedgerEntry, error) {
	res, err := db.Exec(`INSERT INTO ledger_entries(run_id, repository, decision, evidence, reason, risk, reversibility, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		runID, project.Repository, redact(decision), redact(evidence), redact(reason), redact(risk), redact(reversibility), nowRFC3339())
	if err != nil {
		return LedgerEntry{}, err
	}
	id, _ := res.LastInsertId()
	return LedgerEntry{
		ID: id, RunID: runID, Decision: decision, Evidence: evidence, Reason: reason,
		Risk: risk, Reversibility: reversibility, CreatedAt: nowRFC3339(),
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
