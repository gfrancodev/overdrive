package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const circleIdleDaysEnv = "OVERDRIVE_CIRCLE_IDLE_DAYS"

func circleIdleRetentionDays() int {
	days := parseIntEnv(circleIdleDaysEnv, 0)
	if days < 0 {
		return 0
	}
	return days
}

func circleLastActivityAt(c Circle) time.Time {
	var latest time.Time
	bump := func(raw string) {
		raw = strings.TrimSpace(raw)
		if raw == "" {
			return
		}
		t, err := time.Parse(time.RFC3339, raw)
		if err != nil {
			return
		}
		if t.After(latest) {
			latest = t
		}
	}
	bump(c.CreatedAt)
	bump(c.UpdatedAt)
	for _, m := range c.Members {
		bump(m.AddedAt)
		bump(m.LastSeen)
	}
	return latest
}

func circleActiveMemberCount(c Circle) int {
	n := 0
	for _, m := range c.Members {
		if !m.Revoked {
			n++
		}
	}
	return n
}

func circlePruneSkipReason(home string, c Circle, now time.Time, idleDays int) string {
	if idleDays <= 0 {
		return "retention disabled"
	}
	if len(c.AllowedFolders) > 0 {
		return "circle has allowed folders"
	}
	if circleActiveMemberCount(c) > 1 {
		return "multiple active members"
	}
	if circleHasRunningTerminal(home, c.ID) {
		return "running terminal session"
	}
	last := circleLastActivityAt(c)
	if last.IsZero() {
		return "no activity timestamp"
	}
	cutoff := now.Add(-time.Duration(idleDays) * 24 * time.Hour)
	if !last.Before(cutoff) {
		return "still within idle window"
	}
	return ""
}

func circleHasRunningTerminal(home, circleID string) bool {
	db, err := openTerminalDB(home)
	if err != nil {
		return false
	}
	var n int
	err = db.QueryRow(`SELECT COUNT(1) FROM terminal_sessions
		WHERE circle_id = ? AND state IN (?, ?, ?, ?)`,
		circleID, TerminalStateRunning, TerminalStateStarting, TerminalStateDetached, TerminalStateCreated).Scan(&n)
	return err == nil && n > 0
}

type circlePruneSkip struct {
	CircleID string `json:"circle_id"`
	Name     string `json:"name,omitempty"`
	Reason   string `json:"reason"`
}

type circlePruneReport struct {
	IdleDays int               `json:"idle_days"`
	DryRun   bool              `json:"dry_run"`
	Pruned   []string          `json:"pruned"`
	Skipped  []circlePruneSkip `json:"skipped"`
}

func pruneIdleCircles(home string, dryRun bool) (circlePruneReport, error) {
	idleDays := circleIdleRetentionDays()
	report := circlePruneReport{IdleDays: idleDays, DryRun: dryRun}
	if idleDays <= 0 {
		return report, nil
	}
	circles, err := listCircles(home)
	if err != nil {
		return report, err
	}
	now := time.Now().UTC()
	for _, c := range circles {
		if reason := circlePruneSkipReason(home, c, now, idleDays); reason != "" {
			report.Skipped = append(report.Skipped, circlePruneSkip{CircleID: c.ID, Name: c.Name, Reason: reason})
			continue
		}
		if dryRun {
			report.Pruned = append(report.Pruned, c.ID)
			continue
		}
		if err := deleteCircleLocal(home, c.ID); err != nil {
			report.Skipped = append(report.Skipped, circlePruneSkip{CircleID: c.ID, Name: c.Name, Reason: err.Error()})
			continue
		}
		report.Pruned = append(report.Pruned, c.ID)
	}
	return report, nil
}

func deleteCircleLocal(home, circleID string) error {
	if strings.TrimSpace(circleID) == "" {
		return errors.New("circle id required")
	}
	if err := hookRemove(circlePath(home, circleID)); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("remove circle metadata: %w", err)
	}
	dir := filepath.Join(circlesDir(home), circleID)
	if err := os.RemoveAll(dir); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("remove circle data: %w", err)
	}
	return nil
}

func maybePruneIdleCirclesOnListen(home string) {
	if circleIdleRetentionDays() <= 0 {
		return
	}
	_, _ = pruneIdleCircles(home, false)
}
