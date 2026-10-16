package main

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func writeStaleCircle(t *testing.T, home string, c Circle) {
	t.Helper()
	stale := time.Now().UTC().Add(-90 * 24 * time.Hour).Format(time.RFC3339)
	c.CreatedAt = stale
	c.UpdatedAt = stale
	for i := range c.Members {
		c.Members[i].AddedAt = stale
		c.Members[i].LastSeen = stale
	}
	data, err := hookJSONMarshalIndent(c, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := hookMkdirAll(circlesDir(home), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := hookWriteFile(circlePath(home, c.ID), data, 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestCircleIdleRetentionDays(t *testing.T) {
	t.Setenv(circleIdleDaysEnv, "30")
	if circleIdleRetentionDays() != 30 {
		t.Fatal("expected 30")
	}
	t.Setenv(circleIdleDaysEnv, "-1")
	if circleIdleRetentionDays() != 0 {
		t.Fatal("negative should disable")
	}
	t.Setenv(circleIdleDaysEnv, "")
	if circleIdleRetentionDays() != 0 {
		t.Fatal("default off")
	}
}

func TestPruneIdleCirclesDisabled(t *testing.T) {
	home := t.TempDir()
	id, _ := testDevice(t)
	writeStaleCircle(t, home, testCircle(id))
	t.Setenv(circleIdleDaysEnv, "0")
	report, err := pruneIdleCircles(home, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Pruned) != 0 {
		t.Fatalf("unexpected prune: %v", report.Pruned)
	}
	if _, err := loadCircle(home, "circle_mesh01"); err != nil {
		t.Fatal("circle should remain")
	}
}

func TestPruneIdleCirclesRemovesStaleSoloCircle(t *testing.T) {
	home := t.TempDir()
	id, _ := testDevice(t)
	c := testCircle(id)
	writeStaleCircle(t, home, c)
	extraDir := filepath.Join(circlesDir(home), c.ID, "workspaces", "ws_demo")
	if err := os.MkdirAll(extraDir, 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv(circleIdleDaysEnv, "30")
	report, err := pruneIdleCircles(home, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Pruned) != 1 || report.Pruned[0] != c.ID {
		t.Fatalf("pruned: %+v", report)
	}
	if _, err := os.Stat(circlePath(home, c.ID)); !os.IsNotExist(err) {
		t.Fatal("metadata should be gone")
	}
	if _, err := os.Stat(filepath.Join(circlesDir(home), c.ID)); !os.IsNotExist(err) {
		t.Fatal("circle dir should be gone")
	}
}

func TestPruneIdleCirclesSkipsAllowedFolders(t *testing.T) {
	home := t.TempDir()
	id, _ := testDevice(t)
	c := testCircle(id)
	c.AllowedFolders = []string{"/tmp/project"}
	writeStaleCircle(t, home, c)
	t.Setenv(circleIdleDaysEnv, "30")
	report, err := pruneIdleCircles(home, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Pruned) != 0 {
		t.Fatal("should not prune")
	}
	if len(report.Skipped) != 1 || report.Skipped[0].Reason != "circle has allowed folders" {
		t.Fatalf("skipped: %+v", report.Skipped)
	}
}

func TestPruneIdleCirclesSkipsRecentActivity(t *testing.T) {
	home := t.TempDir()
	id, _ := testDevice(t)
	if err := saveCircle(home, testCircle(id)); err != nil {
		t.Fatal(err)
	}
	t.Setenv(circleIdleDaysEnv, "30")
	report, err := pruneIdleCircles(home, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Pruned) != 0 {
		t.Fatal("recent circle should stay")
	}
	found := false
	for _, s := range report.Skipped {
		if s.Reason == "still within idle window" {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected idle window skip: %+v", report.Skipped)
	}
}

func TestPruneIdleCirclesDryRun(t *testing.T) {
	home := t.TempDir()
	id, _ := testDevice(t)
	c := testCircle(id)
	writeStaleCircle(t, home, c)
	t.Setenv(circleIdleDaysEnv, "30")
	report, err := pruneIdleCircles(home, true)
	if err != nil {
		t.Fatal(err)
	}
	if !report.DryRun || len(report.Pruned) != 1 {
		t.Fatalf("dry-run report: %+v", report)
	}
	if _, err := loadCircle(home, c.ID); err != nil {
		t.Fatal("dry-run must not delete")
	}
}

func TestCircleLastActivityAt(t *testing.T) {
	old := time.Now().UTC().Add(-48 * time.Hour).Format(time.RFC3339)
	recent := time.Now().UTC().Format(time.RFC3339)
	c := Circle{CreatedAt: old, UpdatedAt: old, Members: []CircleMember{{LastSeen: recent}}}
	last := circleLastActivityAt(c)
	recentT, _ := time.Parse(time.RFC3339, recent)
	if !last.Equal(recentT) {
		t.Fatalf("last=%v want=%v", last, recentT)
	}
}
