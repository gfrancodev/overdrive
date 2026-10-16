package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestShareUILoopbackAndTheme(t *testing.T) {
	t.Setenv("OVERDRIVE_SHARE_UI", "")
	t.Setenv("OVERDRIVE_SHARE_LISTEN", "127.0.0.1:7741")
	addr, ok := shareUIAddr()
	if !ok || addr != "127.0.0.1:7742" {
		t.Fatalf("loopback ui %q %v", addr, ok)
	}

	home := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, "share"), 0o700); err != nil {
		t.Fatal(err)
	}
	raw := []byte(`{"preset":"enterprise","title":"Ops <circle>","logo":"https://evil.example/x","tokens":{"cyan":"#00b4ff","ink":"red"}}`)
	if err := os.WriteFile(filepath.Join(home, "share", "ui.json"), raw, 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := loadShareUI(home)
	if cfg.Preset != "enterprise" || cfg.Title != "Ops circle" || cfg.Logo != "/logo.png" {
		t.Fatalf("config %+v", cfg)
	}
	if cfg.Tokens["cyan"] != "#00b4ff" || cfg.Tokens["ink"] != "" {
		t.Fatalf("tokens %+v", cfg.Tokens)
	}
	page := string(dashboardHTMLWithUI(home))
	if !strings.Contains(page, `"preset":"enterprise"`) || !strings.Contains(page, "entRows") {
		t.Fatal("enterprise page")
	}
}

func TestDashboardToastClosesBeforeDock(t *testing.T) {
	page := string(dashboardHTML)
	toast := strings.Index(page, `<output class="toast" id="toast"`)
	close := strings.Index(page, "</output>")
	dock := strings.Index(page, `id="dock"`)
	if toast < 0 || close < 0 || dock < 0 || toast >= close || close >= dock {
		t.Fatalf("toast must close before the dock: toast=%d close=%d dock=%d", toast, close, dock)
	}
	if strings.Contains(page, "_unused") {
		t.Fatal("dashboard script references _unused")
	}
}

func TestDashboardEmbedsXterm(t *testing.T) {
	page := string(dashboardHTML)
	if strings.Contains(page, "/*{{xterm") {
		t.Fatal("unresolved xterm placeholder")
	}
	if !strings.Contains(page, ".xterm") {
		t.Fatal("missing xterm css")
	}
	if !strings.Contains(page, "FitAddon") {
		t.Fatal("missing FitAddon")
	}
	if !strings.Contains(page, "hljs") || !strings.Contains(page, "mermaid") {
		t.Fatal("missing highlight or mermaid")
	}
}

func TestDecisionLineAvoidsDuplicateSubject(t *testing.T) {
	if got := decisionLine(Memory{Subject: "Keep SQLite", Content: " keep sqlite "}); got != "Keep SQLite" {
		t.Fatalf("duplicate subject kept: %q", got)
	}
	if got := decisionLine(Memory{Subject: "Boundary", Content: "Use the repository"}); got != "Boundary: Use the repository" {
		t.Fatalf("full line: %q", got)
	}
	if got := decisionLine(Memory{Content: "only content"}); got != "only content" {
		t.Fatalf("content only: %q", got)
	}
	if got := decisionLine(Memory{Subject: "only subject"}); got != "only subject" {
		t.Fatalf("subject only: %q", got)
	}
}

func TestClipUILineAndList(t *testing.T) {
	long := strings.Repeat("word ", 30)
	if clipUILine("  spaced\n\ttext  ") != "spaced text" {
		t.Fatalf("collapse %q", clipUILine("  spaced\n\ttext  "))
	}
	if len(clipUILine(long)) != 96 {
		t.Fatalf("clip len %d", len(clipUILine(long)))
	}
	if clipUIList(nil, 3) != nil {
		t.Fatal("nil list")
	}
	lines := []string{"a", "b", "c", "d"}
	if len(clipUIList(lines, 2)) != 2 {
		t.Fatal("limit")
	}
}

func TestDashboardLinksFromPeerLines(t *testing.T) {
	lines := []string{
		formatPeerSyncLine("192.168.1.20:7741", "timeout", 0),
		formatPeerSyncLine("dev_peer_studio", "ok", 3),
		"not a peer line",
		"",
	}
	links := dashboardLinks(lines)
	if len(links) != 2 {
		t.Fatalf("links: %+v", links)
	}
	if links[0].Name != "192.168.1.20:7741" || links[0].State != "timeout" || links[0].Packets != 0 {
		t.Fatalf("timeout link: %+v", links[0])
	}
	if links[1].Name != "dev_peer_studio" || links[1].State != "ok" || links[1].Packets != 3 {
		t.Fatalf("ok link: %+v", links[1])
	}
	if _, _, _, ok := parsePeerSyncLine("- peer: online, sync ok, x pacotes"); ok {
		t.Fatal("malformed packet count should not parse")
	}
}

func TestContextDetailSurfacesShareableDistillation(t *testing.T) {
	got := catalogDisplaySummary("lesson", "/srv/workstation", "Resume handoff", "The next agent continues from the open handoff", "ERR_TIMEOUT")
	if !strings.Contains(got, "The next agent continues from the open handoff") || !strings.Contains(got, "ERR_TIMEOUT") {
		t.Fatalf("distillation: %q", got)
	}
	if strings.Contains(got, "Resume handoff") || strings.Contains(got, "secret body") {
		t.Fatalf("subject or raw content leaked into summary: %q", got)
	}
	if got := catalogDisplaySummary("lesson", "/srv/workstation", "Use WAL", "", ""); got != "" {
		t.Fatalf("subject-only card should stay empty: %q", got)
	}
	if got := catalogDisplaySummary("lesson", "/srv/workstation", "Use WAL", "db.go", ""); got != "db.go" && !strings.Contains(got, "db.go") {
		t.Fatalf("graph remainder kept: %q", got)
	}
}

func TestContextDetailDropsWhatTheTreeAlreadyShows(t *testing.T) {
	summary := "decision · overdrive · Keep SQLite as the source of truth · OVERDRIVE_SHARE_UI, sqlite"
	if got := contextDetail(summary, "decision", "overdrive", "Keep SQLite as the source of truth"); got != "OVERDRIVE_SHARE_UI, sqlite" {
		t.Fatalf("tokens kept: %q", got)
	}
	if got := contextDetail("decision · overdrive · Keep SQLite", "Decision", "Overdrive", "keep sqlite"); got != "" {
		t.Fatalf("fully redundant summary should vanish: %q", got)
	}
	if got := contextDetail("free text summary", "lesson", "api", "Subject"); got != "free text summary" {
		t.Fatalf("free text untouched: %q", got)
	}
	if got := contextDetail("lesson · Subject · retry on 503", "lesson", "", "Subject"); got != "retry on 503" {
		t.Fatalf("missing folder still strips: %q", got)
	}
}
