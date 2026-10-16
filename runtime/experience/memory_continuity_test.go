package main

import (
	"bytes"
	"crypto/ed25519"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLifetimePromotesOnlyStanding(t *testing.T) {
	if shouldPromoteLifetime("run") || shouldPromoteLifetime("  run ") {
		t.Fatal("run must stay in the ledger")
	}
	if !shouldPromoteLifetime(" standing ") {
		t.Fatal("standing must promote")
	}
	if _, err := normalizeLifetime("forever"); err == nil {
		t.Fatal("invalid lifetime")
	}
	life, err := normalizeLifetime(" Standing ")
	if err != nil || life != "standing" {
		t.Fatalf("normalize: %v %q", err, life)
	}

	home := t.TempDir()
	setupTestEnv(t, home)
	repo := makeTestRepo(t, filepath.Join(t.TempDir(), "repo"), "https://github.com/acme/promo.git")
	e, err := newEngine(home)
	if err != nil {
		t.Fatal(err)
	}
	defer e.Close()
	project, err := identifyProject(repo)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.ledgerAddLifetime(ledgerWrite{Project: project, RunID: "run-1", Decision: "mock this test", Lifetime: "run"}); err != nil {
		t.Fatal(err)
	}
	if _, err := e.ledgerAddLifetime(ledgerWrite{
		Project: project, RunID: "run-1", Decision: "Use ProposalRepository", Evidence: "explicit",
		Reason: "boundary", Lifetime: "standing",
	}); err != nil {
		t.Fatal(err)
	}
	if err := e.promoteStandingDecisions(project); err != nil {
		t.Fatal(err)
	}
	decisions := e.listDecisionMemories(project)
	if len(decisions) != 1 || !strings.Contains(decisions[0].Content, "ProposalRepository") {
		t.Fatalf("decisions: %+v", decisions)
	}
	if err := e.promoteStandingDecisions(project); err != nil {
		t.Fatal(err)
	}
	if got := e.listDecisionMemories(project); len(got) != 1 {
		t.Fatalf("promoted twice: %d", len(got))
	}
}

func TestPinSkipsEmbedder(t *testing.T) {
	home := t.TempDir()
	setupTestEnv(t, home)
	repo := makeTestRepo(t, filepath.Join(t.TempDir(), "repo"), "https://github.com/acme/pin.git")
	_, _, code := runCLICapture(t, []string{
		"record", "--cwd", repo, "--kind", "decision", "--subject", "ProposalRepository",
		"--content", "Persistência entra no repositório existente.",
	})
	if code != 0 {
		t.Fatal(code)
	}
	e, err := newEngine(home)
	if err != nil {
		t.Fatal(err)
	}
	defer e.Close()
	project, err := identifyProject(repo)
	if err != nil {
		t.Fatal(err)
	}
	before := embedTextCalls
	pin := e.renderPin(project)
	if embedTextCalls != before {
		t.Fatalf("pin called embedder: before=%d after=%d", before, embedTextCalls)
	}
	if !strings.Contains(pin, "Decisões vigentes") || !strings.Contains(pin, "ProposalRepository") {
		t.Fatalf("pin: %s", pin)
	}
}

func TestHandoffClaimOnce(t *testing.T) {
	home := t.TempDir()
	setupTestEnv(t, home)
	repo := makeTestRepo(t, filepath.Join(t.TempDir(), "repo"), "https://github.com/acme/hof.git")
	e, err := newEngine(home)
	if err != nil {
		t.Fatal(err)
	}
	defer e.Close()
	project, err := identifyProject(repo)
	if err != nil {
		t.Fatal(err)
	}
	if err := e.saveHandoff(project, "id da proposta ainda não conferido", "dev_ana"); err != nil {
		t.Fatal(err)
	}
	claimed, err := e.claimOpenHandoff(project, "dev_ana")
	if err != nil || claimed {
		t.Fatalf("owner claim: claimed=%v err=%v", claimed, err)
	}
	claimed, err = e.claimOpenHandoff(project, "dev_gabriel")
	if err != nil || !claimed {
		t.Fatalf("peer claim: claimed=%v err=%v", claimed, err)
	}
	claimed, err = e.claimOpenHandoff(project, "dev_other")
	if err != nil || claimed {
		t.Fatalf("second claim: claimed=%v err=%v", claimed, err)
	}
	list, err := e.listHandoffs(project)
	if err != nil || len(list) != 1 || list[0].State != "assumido" || list[0].ClaimedBy != "dev_gabriel" {
		t.Fatalf("handoff: %+v err=%v", list, err)
	}
}

func TestSpanBoundsAndMiss(t *testing.T) {
	if err := spanBoundsOK(10, 0, 10); err != nil {
		t.Fatal(err)
	}
	if err := spanBoundsOK(10, -1, 2); err == nil {
		t.Fatal("negative start")
	}
	if err := spanBoundsOK(10, 4, 3); err == nil {
		t.Fatal("end before start")
	}
	if err := spanBoundsOK(10, 0, 11); err == nil {
		t.Fatal("end past size")
	}

	dir := t.TempDir()
	path := filepath.Join(dir, "overdrive-session.jsonl")
	body := []byte("{\"role\":\"user\",\"content\":\"vamos usar ProposalRepository\"}\n{\"role\":\"user\",\"content\":\"hello\"}\n")
	if err := os.WriteFile(path, body, 0o600); err != nil {
		t.Fatal(err)
	}
	home := t.TempDir()
	setupTestEnv(t, home)
	repo := makeTestRepo(t, filepath.Join(t.TempDir(), "overdrive"), "https://github.com/acme/overdrive.git")
	e, err := newEngine(home)
	if err != nil {
		t.Fatal(err)
	}
	defer e.Close()
	project, err := identifyProject(repo)
	if err != nil {
		t.Fatal(err)
	}
	spanReadCount = 0
	moved, err := e.indexTranscriptFile(project, "cursor", path)
	if err != nil || !moved {
		t.Fatalf("index: moved=%v err=%v", moved, err)
	}
	moved, err = e.indexTranscriptFile(project, "cursor", path)
	if err != nil || moved {
		t.Fatalf("second index should be idle: moved=%v err=%v", moved, err)
	}
	if spanReadCount != 0 {
		t.Fatalf("index must not use span reads: %d", spanReadCount)
	}
	hints, err := e.matchingSpanHints(project, "ProposalRepository")
	if err != nil || len(hints) == 0 || spanReadCount != 1 {
		t.Fatalf("miss read hints=%v reads=%d err=%v", hints, spanReadCount, err)
	}
	if _, err := e.recordMemory(memoryRecord{Project: project, Kind: "decision", Scope: "repository", Subject: "ProposalRepository", Content: "usar o repositório existente", Confidence: 0.9, Priority: 70, Source: "verified_execution", SourceRef: "", Evidence: ""}); err != nil {
		t.Fatal(err)
	}
	before := spanReadCount
	resp, err := e.recall(project, "ProposalRepository", 4, "")
	if err != nil {
		t.Fatal(err)
	}
	if spanReadCount != before {
		t.Fatalf("decision hit opened the transcript: reads %d -> %d", before, spanReadCount)
	}
	if len(resp.Unconfirmed) != 0 {
		t.Fatalf("unconfirmed with decision: %+v", resp.Unconfirmed)
	}
	if !decisionShaped("vamos usar o repo") || decisionShaped("hello") {
		t.Fatal("decision shape")
	}
}

func TestUnconfirmedHintDoesNotBecomeDecision(t *testing.T) {
	home := t.TempDir()
	setupTestEnv(t, home)
	repo := makeTestRepo(t, filepath.Join(t.TempDir(), "overdrive"), "https://github.com/acme/overdrive.git")
	root := t.TempDir()
	path := filepath.Join(root, "overdrive-notes.jsonl")
	if err := os.WriteFile(path, []byte("decidimos o id do CRM\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("OVERDRIVE_TRANSCRIPT_ROOT", root)
	e, err := newEngine(home)
	if err != nil {
		t.Fatal(err)
	}
	defer e.Close()
	project, err := identifyProject(repo)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.startSession(project); err != nil {
		t.Fatal(err)
	}
	if got := e.listDecisionMemories(project); len(got) != 0 {
		t.Fatalf("hint became decision: %+v", got)
	}
	if !e.hasOpenHandoff(project) {
		t.Fatal("expected unconfirmed handoff")
	}
	list, _ := e.listHandoffs(project)
	if len(list) == 0 || !strings.Contains(list[0].Summary, "não confirmada") {
		t.Fatalf("handoff: %+v", list)
	}
}

func TestSecurityPredicates(t *testing.T) {
	redacted := redact("api_key=supersecretvalue")
	if strings.Contains(redacted, "supersecretvalue") || !strings.Contains(redacted, "<redacted>") {
		t.Fatalf("redact: %s", redacted)
	}
	if redact("") != "" {
		t.Fatal("empty redact")
	}
	key := "-----BEGIN OPENSSH PRIVATE KEY-----\nabc\n-----END OPENSSH PRIVATE KEY-----"
	if !strings.Contains(redact(key), "redacted-private-key") {
		t.Fatal("private key")
	}

	pub, priv, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	req := SyncRequest{DeviceID: "dev_a", CircleID: "c1", Repository: "acme/r", Timestamp: "2026-01-01T00:00:00Z"}
	signed, err := signSyncRequest(priv, req)
	if err != nil || !verifySyncRequest(pub, signed) {
		t.Fatal("valid signature rejected")
	}
	signed.DeviceID = "dev_b"
	if verifySyncRequest(pub, signed) {
		t.Fatal("tampered request accepted")
	}
	signed, _ = signSyncRequest(priv, req)
	signed.Signature = "bm90LWEgc2ln"
	if verifySyncRequest(pub, signed) {
		t.Fatal("bad signature accepted")
	}

	allowed := t.TempDir()
	outside := t.TempDir()
	circle := Circle{AllowedFolders: []string{allowed}}
	if exportFolderAllowed(circle, outside) {
		t.Fatal("outside folder exported")
	}
	if !exportFolderAllowed(circle, allowed) {
		t.Fatal("allowed folder blocked")
	}
	if exportFolderAllowed(Circle{}, allowed) || exportFolderAllowed(circle, "  ") {
		t.Fatal("empty allowlist or folder")
	}
	if shouldExportMemory(Memory{Kind: "preference", Scope: "repository"}) {
		t.Fatal("preference exported")
	}
	if shouldExportMemory(Memory{Kind: "decision", Scope: "global"}) {
		t.Fatal("global decision exported")
	}
	if !shouldExportMemory(Memory{Kind: "decision", Scope: "repository"}) {
		t.Fatal("repository decision not exported")
	}
	if !shouldExportMemory(Memory{Kind: "handoff", Scope: "repository"}) {
		t.Fatal("handoff not exported")
	}

	mem := sharedPacketToMemory(SharedPacket{Fingerprint: "mem_peer", Kind: "decision", LessonContent: "body", PacketContent: "pkt", HotIndex: true}, "circle", "dev_peer")
	if mem.Origin != "peer" {
		t.Fatalf("origin %s", mem.Origin)
	}
	pkt := memoryToSharedPacket(Memory{ID: "mem_local", Kind: "decision", ScopeID: "acme/r", Content: "body", HotIndex: true, Layer: 2}, allowed, true)
	if len(pkt.Vector) != 0 {
		t.Fatal("vector shipped")
	}
	pkt.VectorSpace = "other-embedder"
	if !peerPacketImportable(pkt, "stub") {
		t.Fatal("empty vector must import on another embedder")
	}
	pkt.Vector = []float32{0.1}
	if peerPacketImportable(pkt, "stub") {
		t.Fatal("foreign vector accepted")
	}

	if err := rejectGroupKeyLeak(`{"mode":"ready"}`, "group-secret"); err != nil {
		t.Fatal(err)
	}
	if err := rejectGroupKeyLeak(`{"group_key":"group-secret"}`, "group-secret"); err == nil {
		t.Fatal("group key leak accepted")
	}
	if err := rejectGroupKeyLeak(`{"mode":"ready"}`, "  "); err != nil {
		t.Fatal("blank key")
	}

	if !strings.Contains(formatPeerSyncLine("ci-box", "timeout", 0), "sem resposta (timeout)") {
		t.Fatal("timeout line")
	}
	if !strings.Contains(formatPeerSyncLine("notebook-ana", "ok", 1), "online, sync ok, 1 pacotes") {
		t.Fatal("ok line")
	}
	if !strings.HasPrefix(formatPeerSyncLine("  ", "timeout", 0), "- peer:") {
		t.Fatal("blank peer name")
	}
	if hookRemoteAllowed("10.0.0.8:9") || !hookRemoteAllowed("127.0.0.1:9") || !hookRemoteAllowed("[::1]:9") {
		t.Fatal("hook remote")
	}
}

func TestSessionEndPromotesAndContextFile(t *testing.T) {
	home := t.TempDir()
	setupTestEnv(t, home)
	repo := makeTestRepo(t, filepath.Join(t.TempDir(), "repo"), "https://github.com/acme/end.git")
	if _, _, code := runCLICapture(t, []string{"session-start", "--cwd", repo, "--quiet"}); code != 0 {
		t.Fatal(code)
	}
	if _, _, code := runCLICapture(t, []string{
		"ledger-add", "--cwd", repo, "--run", "r1", "--lifetime", "standing",
		"--decision", "Use the existing repository boundary",
	}); code != 0 {
		t.Fatal("ledger")
	}
	if _, _, code := runCLICapture(t, []string{
		"ledger-add", "--cwd", repo, "--run", "r1", "--lifetime", "run",
		"--decision", "defer the pull request",
	}); code != 0 {
		t.Fatal("run ledger")
	}
	if _, _, code := runCLICapture(t, []string{"session-end", "--cwd", repo, "--quiet"}); code != 0 {
		t.Fatal("end")
	}
	stdout, _, code := runCLICapture(t, []string{"session-start", "--cwd", repo, "--format", "text"})
	if code != 0 || !strings.Contains(stdout, "Use the existing repository boundary") {
		t.Fatalf("pin missing decision: code=%d %s", code, stdout)
	}
	if strings.Contains(stdout, "defer the pull request") && strings.Contains(stdout, "Decisões vigentes\n- defer") {
		t.Fatalf("run ruling promoted: %s", stdout)
	}
	out := filepath.Join(t.TempDir(), "crush.txt")
	if _, _, code := runCLICapture(t, []string{"context-file", "--cwd", repo, "--out", out}); code != 0 {
		t.Fatal(code)
	}
	raw, err := os.ReadFile(out)
	if err != nil || !bytes.Contains(raw, []byte("Decisões vigentes")) {
		t.Fatalf("context file: %v %s", err, raw)
	}
}

func TestMCPRecallAndHandoffTool(t *testing.T) {
	home := t.TempDir()
	setupTestEnv(t, home)
	repo := makeTestRepo(t, filepath.Join(t.TempDir(), "repo"), "https://github.com/acme/mcp.git")
	init := handleMCPRequest(repo, mcpRequest{JSONRPC: "2.0", ID: 1, Method: "initialize"})
	if init.Result == nil || init.Error != nil {
		t.Fatalf("initialize: %+v", init)
	}
	list := handleMCPRequest(repo, mcpRequest{JSONRPC: "2.0", ID: 2, Method: "tools/list"})
	raw, _ := json.Marshal(list.Result)
	if !bytes.Contains(raw, []byte("recall")) || !bytes.Contains(raw, []byte("handoff")) {
		t.Fatalf("tools: %s", raw)
	}
	params, _ := json.Marshal(map[string]any{"name": "record", "arguments": map[string]any{"kind": "decision", "subject": "CRM", "content": "id da proposta é o do CRM"}})
	recorded := handleMCPRequest(repo, mcpRequest{JSONRPC: "2.0", ID: 3, Method: "tools/call", Params: params})
	if recorded.Error != nil {
		t.Fatalf("record: %+v", recorded.Error)
	}
	missing := handleMCPRequest(repo, mcpRequest{JSONRPC: "2.0", ID: 4, Method: "nope"})
	if missing.Error == nil {
		t.Fatal("unknown method")
	}
}

func TestDashboardHookLoopback(t *testing.T) {
	home := t.TempDir()
	setupTestEnv(t, home)
	repo := makeTestRepo(t, filepath.Join(t.TempDir(), "repo"), "https://github.com/acme/hook.git")
	handler := shareDashboardHandler(home)
	req := httptest.NewRequest(http.MethodPost, "/hook", strings.NewReader(`{"event":"session-end","cwd":"`+repo+`"}`))
	req.RemoteAddr = "10.1.1.1:9"
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("remote code %d", rec.Code)
	}
	req = httptest.NewRequest(http.MethodPost, "/hook", strings.NewReader(`{"event":"session-start","cwd":"`+repo+`"}`))
	req.RemoteAddr = "127.0.0.1:9"
	req.Host = "127.0.0.1:7742"
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("loopback code %d body %s", rec.Code, rec.Body.String())
	}
}

func TestQueryFromHookPayload(t *testing.T) {
	if queryFromHookPayload(`{"prompt":"seguir a proposta"}`) != "seguir a proposta" {
		t.Fatal("prompt")
	}
	if queryFromHookPayload("texto cru") != "texto cru" {
		t.Fatal("raw")
	}
	if queryFromHookPayload("   ") != "" {
		t.Fatal("blank")
	}
}
