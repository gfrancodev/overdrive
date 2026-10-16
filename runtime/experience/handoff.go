package main

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
)

type Handoff struct {
	ID          string `json:"id"`
	Repository  string `json:"repository"`
	Summary     string `json:"summary"`
	State       string `json:"state"`
	OwnerDevice string `json:"owner_device"`
	ClaimedBy   string `json:"claimed_by,omitempty"`
	UpdatedAt   string `json:"updated_at"`
}

func handoffID(repository, summary string) string {
	sum := sha256.Sum256([]byte(repository + "\x00" + normalize(summary)))
	return "hof_" + hex.EncodeToString(sum[:8])
}

func (e *Engine) listHandoffs(project Project) ([]Handoff, error) {
	if e == nil || e.db == nil {
		return nil, nil
	}
	rows, err := e.db.Query(`SELECT id, repository, summary, state, owner_device, claimed_by, updated_at
		FROM handoffs WHERE repository = ? ORDER BY updated_at DESC LIMIT 8`, project.Repository)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Handoff{}
	for rows.Next() {
		var h Handoff
		if err := rows.Scan(&h.ID, &h.Repository, &h.Summary, &h.State, &h.OwnerDevice, &h.ClaimedBy, &h.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, h)
	}
	return out, rows.Err()
}

func (e *Engine) hasOpenHandoff(project Project) bool {
	if e == nil || e.db == nil {
		return false
	}
	var n int
	err := e.db.QueryRow(`SELECT COUNT(*) FROM handoffs WHERE repository = ? AND state = 'aberto'`, project.Repository).Scan(&n)
	return err == nil && n > 0
}

func (e *Engine) saveHandoff(project Project, summary, owner string) error {
	summary = strings.TrimSpace(summary)
	if summary == "" || e == nil || e.db == nil {
		return nil
	}
	now := nowRFC3339()
	id := handoffID(project.Repository, summary)
	_, err := e.db.Exec(`INSERT INTO handoffs(id, repository, summary, state, owner_device, claimed_by, updated_at)
		VALUES (?, ?, ?, 'aberto', ?, '', ?)
		ON CONFLICT(id) DO UPDATE SET summary=excluded.summary, updated_at=excluded.updated_at`,
		id, project.Repository, redact(summary), owner, now)
	return err
}

func (e *Engine) claimOpenHandoff(project Project, claimer string) (bool, error) {
	claimer = strings.TrimSpace(claimer)
	if claimer == "" || e == nil || e.db == nil {
		return false, nil
	}
	res, err := e.db.Exec(`UPDATE handoffs SET state = 'assumido', claimed_by = ?, updated_at = ?
		WHERE repository = ? AND state = 'aberto' AND claimed_by = '' AND owner_device != '' AND owner_device != ?`,
		claimer, nowRFC3339(), project.Repository, claimer)
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n > 0, nil
}

func (e *Engine) captureSessionHandoff(project Project) error {
	if e == nil || e.db == nil {
		return nil
	}
	started := ""
	_ = e.db.QueryRow(`SELECT started_at FROM sessions WHERE repository = ? ORDER BY started_at DESC LIMIT 1`, project.Repository).Scan(&started)
	query := `SELECT decision FROM ledger_entries WHERE repository = ?`
	args := []any{project.Repository}
	if started != "" {
		query += ` AND created_at >= ?`
		args = append(args, started)
	}
	query += ` ORDER BY id ASC LIMIT 6`
	rows, err := e.db.Query(query, args...)
	if err != nil {
		return err
	}
	defer rows.Close()
	parts := []string{}
	for rows.Next() {
		var decision string
		if err := rows.Scan(&decision); err != nil {
			return err
		}
		decision = strings.TrimSpace(decision)
		if decision != "" {
			parts = append(parts, decision)
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}
	if len(parts) == 0 {
		return nil
	}
	owner := ""
	if id, _, err := loadOrCreateIdentity(e.home); err == nil {
		owner = id.DeviceID
	}
	return e.saveHandoff(project, strings.Join(parts, "; "), owner)
}

func (e *Engine) ensureHandoffFromHints(project Project) error {
	if e.hasOpenHandoff(project) {
		return nil
	}
	hints, err := e.unconfirmedTitles(project)
	if err != nil || len(hints) == 0 {
		return err
	}
	owner := ""
	if id, _, err := loadOrCreateIdentity(e.home); err == nil {
		owner = id.DeviceID
	}
	return e.saveHandoff(project, "pista não confirmada: "+strings.Join(hints, "; "), owner)
}
