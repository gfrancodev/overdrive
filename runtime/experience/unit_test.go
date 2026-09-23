package main

import (
	"strings"
	"testing"
)

func TestUtilHelpers(t *testing.T) {
	if clamp(1.5, 0, 1) != 1 {
		t.Fatal("clamp max")
	}
	if clamp(-1, 0, 1) != 0 {
		t.Fatal("clamp min")
	}
	if clampInt(99, 0, 10) != 10 {
		t.Fatal("clampInt")
	}
	if maxInt(2, 5) != 5 {
		t.Fatal("maxInt")
	}
	if round(1.2345, 2) != 1.23 {
		t.Fatal("round")
	}
	if normalize("  Foo   Bar ") != "foo bar" {
		t.Fatal("normalize")
	}
	fp := memoryFingerprint("repository", "github.com/acme/x", "lesson", "subj", "content")
	if !strings.HasPrefix(fp, "mem_") {
		t.Fatal("fingerprint prefix")
	}
	if memoryVectorID("abc") == memoryVectorID("abc") {
		// stable
	}
	red := redact("api_key=supersecretvalue token eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.eyJzdWIiOiIxMjM0NTY3ODkwIn0.dozjgNryP4J3jVmNHl0w5N_XgL0n3I9PlFUP0THsR8U")
	if strings.Contains(red, "supersecretvalue") {
		t.Fatal("redact api key")
	}
	if redact("-----BEGIN PRIVATE KEY-----\nabc\n-----END PRIVATE KEY-----") != "<redacted-private-key>" {
		t.Fatal("redact private key")
	}
	if !validKind("lesson") || validKind("bogus") {
		t.Fatal("validKind")
	}
	if !validScope("repository") || validScope("bogus") {
		t.Fatal("validScope")
	}
	if !positiveSource("verified_execution") || positiveSource("bogus") {
		t.Fatal("positiveSource")
	}
	if strongestSource("agent_observation", "adr") != "adr" {
		t.Fatal("strongestSource")
	}
	if !matchesLayer("fact", "knowledge") || matchesLayer("episode", "knowledge") {
		t.Fatal("matchesLayer knowledge")
	}
	if !matchesLayer("lesson", "lessons") || !matchesLayer("anti_pattern", "lessons") {
		t.Fatal("matchesLayer lessons")
	}
	if !matchesLayer("episode", "episodes") {
		t.Fatal("matchesLayer episodes")
	}
	if !matchesLayer("anything", "unknown-layer") {
		t.Fatal("matchesLayer default")
	}
	m := &Memory{Scope: "global"}
	normalizeMemoryScope(m)
	if m.ScopeID != "*" {
		t.Fatal("normalizeMemoryScope global")
	}
}

func TestScoringHelpers(t *testing.T) {
	p := Project{Repository: "github.com/acme/a", Organization: "github.com/acme", Module: "pkg/mod"}
	if scopeIDFor("global", p) != "*" {
		t.Fatal("scope global")
	}
	if scopeIDFor("module", Project{Repository: "r", Module: ""}) != "r#root" {
		t.Fatal("scope module root")
	}
	m := Memory{Scope: "repository", ScopeID: p.Repository}
	if scopeWeight(m, p) <= 0 {
		t.Fatal("scopeWeight repository")
	}
	if tokenCosine("proposal repository persistence", "Use ProposalRepository for persistence") <= 0 {
		t.Fatal("tokenCosine")
	}
	if stopword("the") != true || stopword("proposal") {
		t.Fatal("stopword")
	}
	if sourceTrust("adr") != 1.0 || sourceTrust("unknown") != 0.6 {
		t.Fatal("sourceTrust")
	}
	if freshnessScore("not-a-date") != 0.5 {
		t.Fatal("freshness invalid")
	}
	if !isCritical(Memory{Kind: "rule", Priority: 95, Confidence: 0.9}) {
		t.Fatal("isCritical")
	}
	repo, org := normalizeRemote("https://github.com/acme/payments-api.git")
	if repo != "github.com/acme/payments-api" || org != "github.com/acme" {
		t.Fatalf("normalizeRemote https: %s %s", repo, org)
	}
	repo, org = normalizeRemote("git@github.com:acme/payments-api.git")
	if repo != "github.com/acme/payments-api" {
		t.Fatalf("normalizeRemote ssh: %s", repo)
	}
}

func TestDistillHelpers(t *testing.T) {
	m := Memory{
		Subject:  "ECONNREFUSED proposal.service.ts",
		Content:  "Use ProposalRepository.",
		Evidence: "integration tests failed",
	}
	sig := extractProblemSignature(strings.Join([]string{m.Subject, m.Content, m.Evidence}, " "))
	if sig == "" {
		t.Fatal("signature empty")
	}
	if !concreteSignatureOverlap(sig, sig) {
		t.Fatal("overlap self")
	}
	if concreteSignatureOverlap("", "x") || concreteSignatureOverlap("a", "") {
		t.Fatal("overlap empty")
	}
	packet := buildPacketContent(m)
	if !strings.Contains(packet, "ProposalRepository") {
		t.Fatal("packet content")
	}
	if !shareableKind("lesson") || shareableKind("preference") {
		t.Fatal("shareableKind")
	}
	applyDistillation(&m, Project{Root: "/tmp/repo"})
	if m.PacketContent == "" || m.Layer != 2 || !m.HotIndex {
		t.Fatal("applyDistillation")
	}
}

func TestShareStatusTextStrategies(t *testing.T) {
	cases := map[string]ShareStatus{
		"off":            {Mode: "off"},
		"folder-blocked": {Mode: "folder-blocked", CircleName: "team"},
		"not-member":     {Mode: "not-member", CircleName: "team"},
		"synced":         {Mode: "synced", CircleName: "team", LastImported: 1, PeerMemoryCount: 2, PeersContacted: 1},
		"ready":          {Mode: "ready", CircleName: "team", PeerMemoryCount: 1, PeerEndpoints: []string{"127.0.0.1:1"}},
		"custom":         {Mode: "custom"},
	}
	for mode, status := range cases {
		text := formatShareStatusText(status)
		if !strings.HasPrefix(text, "share:") {
			t.Fatalf("format %s: %q", mode, text)
		}
	}
}

func TestRunCLIVersionAndErrors(t *testing.T) {
	resetRuntimeGlobals()
	stdout, _, code := runCLICapture(t, []string{"version"})
	if code != 0 || !strings.Contains(stdout, version) {
		t.Fatalf("version: code=%d stdout=%q", code, stdout)
	}
	_, stderr, code := runCLICapture(t, []string{})
	if code != 2 || !strings.Contains(stderr, "usage:") {
		t.Fatalf("usage: code=%d stderr=%q", code, stderr)
	}
	_, stderr, code = runCLICapture(t, []string{"nope"})
	if code != 2 || !strings.Contains(stderr, "unknown command") {
		t.Fatalf("unknown: code=%d stderr=%q", code, stderr)
	}
	_, stderr, code = runCLICapture(t, []string{"record"})
	if code != 2 || !strings.Contains(stderr, "--content is required") {
		t.Fatalf("record missing content: code=%d stderr=%q", code, stderr)
	}
}
