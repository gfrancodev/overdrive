package main

import (
	"fmt"
	"strings"
)

func normalizeLifetime(v string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "", "run":
		return "run", nil
	case "standing":
		return "standing", nil
	default:
		return "", fmt.Errorf("invalid lifetime %q", v)
	}
}

func shouldPromoteLifetime(lifetime string) bool {
	return strings.TrimSpace(lifetime) == "standing"
}

func (e *Engine) promoteStandingDecisions(project Project) error {
	if e == nil || e.db == nil {
		return nil
	}
	rows, err := e.db.Query(`SELECT decision, evidence, reason FROM ledger_entries
		WHERE repository = ? AND lifetime = 'standing' ORDER BY id ASC`, project.Repository)
	if err != nil {
		return err
	}
	defer rows.Close()
	type row struct {
		decision, evidence, reason string
	}
	pending := []row{}
	for rows.Next() {
		var item row
		if err := rows.Scan(&item.decision, &item.evidence, &item.reason); err != nil {
			return err
		}
		pending = append(pending, item)
	}
	if err := rows.Err(); err != nil {
		return err
	}
	for _, item := range pending {
		if err := e.promoteStandingLedgerRow(project, item.decision, item.evidence, item.reason); err != nil {
			return err
		}
	}
	return nil
}

func (e *Engine) promoteStandingLedgerRow(project Project, decision, evidence, reason string) error {
	if !shouldPromoteLifetime("standing") {
		return nil
	}
	subject := decisionSubject(decision)
	if reason != "" {
		if evidence != "" {
			evidence += " "
		}
		evidence += reason
	}
	_, err := e.recordMemory(memoryRecord{
		Project: project, Kind: "decision", Scope: "repository", Subject: subject, Content: decision,
		Confidence: 0.9, Priority: 70, Source: "verified_execution", SourceRef: "", Evidence: evidence,
	})
	return err
}

func decisionSubject(decision string) string {
	decision = strings.TrimSpace(decision)
	if decision == "" {
		return "decision"
	}
	if len(decision) > 80 {
		return decision[:80]
	}
	return decision
}
