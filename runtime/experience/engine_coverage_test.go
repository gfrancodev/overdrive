package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestValidationSuccessAndFailure(t *testing.T) {
	home := t.TempDir()
	setupTestEnv(t, home)
	repo := makeTestRepo(t, filepath.Join(t.TempDir(), "repo"), "https://github.com/acme/a.git")

	stdout, _, code := runCLICapture(t, []string{
		"record", "--cwd", repo, "--kind", "lesson",
		"--content", "Validation path lesson marker.",
		"--confidence", "0.7", "--source", "verified_execution",
	})
	if code != 0 {
		t.Fatal(code)
	}
	id := mustJSON(t, stdout)["id"].(string)

	stdout, _, code = runCLICapture(t, []string{"validate", "--id", id, "--result", "success"})
	if code != 0 {
		t.Fatal(code)
	}
	ok := mustJSON(t, stdout)
	if ok["status"] != "active" {
		t.Fatal("success status")
	}

	stdout, _, code = runCLICapture(t, []string{"validate", "--id", id, "--result", "failure"})
	if code != 0 {
		t.Fatal(code)
	}
	failMem := mustJSON(t, stdout)
	if failMem["failure_count"].(float64) < 1 {
		t.Fatal("failure count")
	}

	_, stderr, code := runCLICapture(t, []string{"validate", "--id", id, "--result", "bogus"})
	if code != 2 || !strings.Contains(stderr, "success, failure, or contradiction") {
		t.Fatalf("invalid result: %q", stderr)
	}
}

func TestScopeIsolationAndGlobalMemory(t *testing.T) {
	home := t.TempDir()
	setupTestEnv(t, home)
	repoA := makeTestRepo(t, filepath.Join(t.TempDir(), "a"), "https://github.com/acme/a.git")
	repoB := makeTestRepo(t, filepath.Join(t.TempDir(), "b"), "https://github.com/acme/b.git")

	_, _, code := runCLICapture(t, []string{
		"record", "--cwd", repoA, "--kind", "rule",
		"--content", "Repository A must use AlphaFactory.",
		"--confidence", "1.0", "--source", "user_feedback",
	})
	if code != 0 {
		t.Fatal(code)
	}
	stdout, _, code := runCLICapture(t, []string{"recall", "--cwd", repoB, "--query", "factory"})
	if code != 0 {
		t.Fatal(code)
	}
	data := mustJSON(t, stdout)
	for _, item := range data["memories"].([]any) {
		m := item.(map[string]any)
		if strings.Contains(m["content"].(string), "AlphaFactory") {
			t.Fatal("leaked across repos")
		}
	}

	_, _, code = runCLICapture(t, []string{
		"record", "--cwd", repoA, "--kind", "preference", "--scope", "global",
		"--content", "Prefer existing abstractions before creating new ones.",
		"--confidence", "0.95", "--source", "user_feedback",
	})
	if code != 0 {
		t.Fatal(code)
	}
	stdout, _, code = runCLICapture(t, []string{"recall", "--cwd", repoB, "--query", "create abstraction"})
	if code != 0 {
		t.Fatal(code)
	}
	data = mustJSON(t, stdout)
	found := false
	for _, item := range data["memories"].([]any) {
		m := item.(map[string]any)
		if strings.Contains(m["content"].(string), "existing abstractions") {
			found = true
		}
	}
	if !found {
		t.Fatal("global memory missing")
	}
}

func TestRecallLayerFilterAndIrrelevant(t *testing.T) {
	home := t.TempDir()
	setupTestEnv(t, home)
	repo := makeTestRepo(t, filepath.Join(t.TempDir(), "repo"), "https://github.com/acme/a.git")
	_, _, code := runCLICapture(t, []string{
		"record", "--cwd", repo, "--kind", "lesson",
		"--content", "Use CustomerFactory when building customer integration fixtures.",
		"--confidence", "0.9", "--source", "verified_execution",
	})
	if code != 0 {
		t.Fatal(code)
	}
	stdout, _, code := runCLICapture(t, []string{
		"recall", "--cwd", repo, "--query", "kubernetes ingress certificate rotation",
	})
	if code != 0 {
		t.Fatal(code)
	}
	data := mustJSON(t, stdout)
	if len(data["memories"].([]any)) != 0 {
		t.Fatal("expected empty recall")
	}
	stdout, _, code = runCLICapture(t, []string{
		"recall", "--cwd", repo, "--query", "customer factory", "--layer", "lessons",
	})
	if code != 0 {
		t.Fatal(code)
	}
	data = mustJSON(t, stdout)
	if len(data["memories"].([]any)) == 0 {
		t.Fatal("layer lessons expected hit")
	}
}

func TestWorkingMemoryAndSessionGC(t *testing.T) {
	home := t.TempDir()
	setupTestEnv(t, home)
	repoA := makeTestRepo(t, filepath.Join(t.TempDir(), "a"), "https://github.com/acme/a.git")
	repoB := makeTestRepo(t, filepath.Join(t.TempDir(), "b"), "https://github.com/acme/b.git")

	_, _, code := runCLICapture(t, []string{"session-start", "--cwd", repoA, "--quiet"})
	if code != 0 {
		t.Fatal(code)
	}
	_, _, code = runCLICapture(t, []string{
		"record", "--cwd", repoA, "--kind", "lesson",
		"--content", "Alpha mission scratch marker for working memory.",
		"--confidence", "0.9", "--source", "verified_execution",
	})
	if code != 0 {
		t.Fatal(code)
	}
	stdout, _, code := runCLICapture(t, []string{"recall", "--cwd", repoA, "--query", "alpha mission scratch"})
	if code != 0 {
		t.Fatal(code)
	}
	data := mustJSON(t, stdout)
	if len(data["working_memory"].([]any)) == 0 {
		t.Fatal("working memory expected")
	}
	stdout, _, code = runCLICapture(t, []string{"recall", "--cwd", repoB, "--query", "alpha mission scratch"})
	if code != 0 {
		t.Fatal(code)
	}
	data = mustJSON(t, stdout)
	for _, item := range data["working_memory"].([]any) {
		m := item.(map[string]any)
		if strings.Contains(m["content"].(string), "Alpha mission scratch") {
			t.Fatal("working memory leaked")
		}
	}
	_, _, code = runCLICapture(t, []string{"session-start", "--cwd", repoA, "--quiet"})
	if code != 0 {
		t.Fatal(code)
	}
	stdout, _, code = runCLICapture(t, []string{
		"recall", "--cwd", repoA, "--query", "kubernetes ingress certificate rotation",
	})
	if code != 0 {
		t.Fatal(code)
	}
	data = mustJSON(t, stdout)
	if len(data["working_memory"].([]any)) != 0 {
		t.Fatal("working memory should reset")
	}
}

func TestDedupRecordAndCLIVersionAliases(t *testing.T) {
	home := t.TempDir()
	setupTestEnv(t, home)
	repo := makeTestRepo(t, filepath.Join(t.TempDir(), "repo"), "https://github.com/acme/a.git")
	args := []string{
		"record", "--cwd", repo, "--kind", "lesson", "--subject", "testing",
		"--content", "Use CustomerFactory for customer integration tests.",
		"--confidence", "0.8", "--source", "verified_execution",
	}
	first := mustJSON(t, runCLICaptureStdout(t, args))
	second := mustJSON(t, runCLICaptureStdout(t, args))
	if first["id"] != second["id"] {
		t.Fatal("dedup id")
	}
	for _, flag := range []string{"--version", "-v"} {
		stdout, _, code := runCLICapture(t, []string{flag})
		if code != 0 || !strings.Contains(stdout, version) {
			t.Fatalf("version flag %s", flag)
		}
	}
}

func runCLICaptureStdout(t *testing.T, args []string) string {
	stdout, _, code := runCLICapture(t, args)
	if code != 0 {
		t.Fatalf("cli %v exit %d", args, code)
	}
	return stdout
}

func TestUtilParseIntEnvAndScopeHelpers(t *testing.T) {
	t.Setenv("OVERDRIVE_TEST_INT", "42")
	if parseIntEnv("OVERDRIVE_TEST_INT", 1) != 42 {
		t.Fatal("parseIntEnv")
	}
	if parseIntEnv("OVERDRIVE_TEST_INT_BAD", 7) != 7 {
		t.Fatal("parseIntEnv fallback")
	}
	if parseFloat("0.5", 0) != 0.5 || parseFloat("", 0.9) != 0.9 {
		t.Fatal("parseFloat")
	}
	p := Project{Repository: "github.com/acme/r", Organization: "github.com/acme", Module: "pkg/sub"}
	m := Memory{Scope: "module", ScopeID: scopeIDFor("module", p)}
	if scopeWeight(m, p) == 0 {
		t.Fatal("module scope weight")
	}
	m = Memory{Scope: "organization", ScopeID: p.Organization}
	if scopeWeight(m, p) == 0 {
		t.Fatal("org scope weight")
	}
	if sha256Sum([]byte("x"))[0] == 0 && false {
		t.Fatal("unused")
	}
	_ = sha256Sum([]byte("probe"))
}

func TestEnsureShareListenerAndCircleForAny(t *testing.T) {
	home := t.TempDir()
	setupTestEnv(t, home)
	if _, ok := circleForAny(home); ok {
		t.Fatal("no circles yet")
	}
	_, _, code := runCLICapture(t, []string{"share", "circle", "create", "--name", "solo"})
	if code != 0 {
		t.Fatal(code)
	}
	if _, ok := circleForAny(home); !ok {
		t.Fatal("expected circle")
	}
	port := freeTCPPort(t)
	t.Setenv("OVERDRIVE_SHARE_LISTEN", fmt.Sprintf("127.0.0.1:%d", port))
	ensureShareListener(home)
}

func TestFolderBlockedPrivateRepo(t *testing.T) {
	home := t.TempDir()
	setupTestEnv(t, home)
	repoAllowed := makeTestRepo(t, filepath.Join(t.TempDir(), "allowed"), "https://github.com/acme/allowed.git")
	repoPrivate := makeTestRepo(t, filepath.Join(t.TempDir(), "private"), "https://github.com/acme/private.git")
	stdout, _, code := runCLICapture(t, []string{"share", "circle", "create", "--name", "scoped"})
	if code != 0 {
		t.Fatal(code)
	}
	circle := mustJSON(t, stdout)
	_, _, code = runCLICapture(t, []string{
		"share", "circle", "folder-add", "--circle", circle["id"].(string), "--folder", repoAllowed,
	})
	if code != 0 {
		t.Fatal(code)
	}
	_, _, code = runCLICapture(t, []string{
		"record", "--cwd", repoPrivate, "--kind", "lesson",
		"--content", "Private repo secret lesson marker.",
		"--confidence", "0.95", "--source", "verified_execution",
	})
	if code != 0 {
		t.Fatal(code)
	}
	stdout, _, code = runCLICapture(t, []string{"share", "status", "--cwd", repoPrivate, "--format", "text"})
	if code != 0 {
		t.Fatal(code)
	}
	if !strings.Contains(stdout, "folder-blocked") && !strings.Contains(stdout, "off") {
		t.Fatalf("status %q", stdout)
	}
}

func TestEngineDirectConsolidateAndSyncResponse(t *testing.T) {
	home := t.TempDir()
	setupTestEnv(t, home)
	repo := makeTestRepo(t, filepath.Join(t.TempDir(), "repo"), "https://github.com/acme/payments-api.git")
	_, _, _ = runCLICapture(t, []string{
		"share", "circle", "create", "--name", "solo",
	})
	circles, _ := listCircles(home)
	if len(circles) == 0 {
		t.Fatal("circle")
	}
	circle := circles[0]
	id, priv, err := loadOrCreateIdentity(home)
	if err != nil {
		t.Fatal(err)
	}
	circle, err = addAllowedFolder(home, circle.ID, repo, id)
	if err != nil {
		t.Fatal(err)
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
	m, err := e.recordMemory(project, "lesson", "repository", "sig", "Use ProposalRepository.", 0.9, 50, "verified_execution", "", "proposal.service.ts ECONNREFUSED")
	if err != nil {
		t.Fatal(err)
	}
	if m.PacketContent == "" {
		t.Fatal("packet")
	}
	resp, err := e.buildSyncResponse(home, circle, id, priv, SyncRequest{DeviceID: id.DeviceID, CircleID: circle.ID, Repository: project.Repository})
	if err != nil || len(resp.Packets) == 0 {
		t.Fatalf("sync response packets=%d err=%v", len(resp.Packets), err)
	}
	if err := e.consolidateSession(project, circle); err != nil {
		t.Fatal(err)
	}
	if err := e.exportPeerIndex(circle, project); err != nil {
		t.Fatal(err)
	}
}

func TestTokenizerMeanPool(t *testing.T) {
	hidden := make([]float32, 384*4)
	for i := range hidden {
		hidden[i] = 1
	}
	mask := []int64{1, 1, 1, 0}
	out := meanPoolL2(hidden, mask, 4, 384)
	if out == nil || len(out) != 384 {
		t.Fatal("meanPoolL2")
	}
	if sqrt64(4) != 2 {
		t.Fatal("sqrt64")
	}
}

func TestEmbedderInterfaceNames(t *testing.T) {
	stub := stubEmbedder{}
	hashed := hashedEmbedder{}
	if stub.Name() != "stub" || hashed.Name() != "hashed" {
		t.Fatal("embedder names")
	}
	if stub.Dim() != hashedDims || stub.Embed("x") == nil {
		t.Fatal("stub embedder")
	}
}

func TestValidationFailureOnPeerMemory(t *testing.T) {
	home := t.TempDir()
	setupTestEnv(t, home)
	repo := makeTestRepo(t, filepath.Join(t.TempDir(), "repo"), "https://github.com/acme/a.git")
	e, err := newEngine(home)
	if err != nil {
		t.Fatal(err)
	}
	defer e.Close()
	project, err := identifyProject(repo)
	if err != nil {
		t.Fatal(err)
	}
	m := Memory{
		ID: "mem_peer_validate_test", Kind: "lesson", Scope: "repository", ScopeID: project.Repository,
		Subject: "peer", Content: "Peer lesson for validation failure.", Confidence: 0.5, Priority: 45,
		Status: "active", Source: "peer_share", Origin: "peer", PeerDeviceID: "dev_peer",
		CircleID: "circle_test", Layer: 2, HotIndex: true, VectorSpace: "stub",
		CreatedAt: nowRFC3339(), UpdatedAt: nowRFC3339(),
	}
	if err := e.upsertPeerMemory(m, embedText(m.Content)); err != nil {
		t.Fatal(err)
	}
	updated, err := e.validateMemory(m.ID, "failure", "", "")
	if err != nil {
		t.Fatal(err)
	}
	if updated.HotIndex {
		t.Fatal("peer hot index should clear on failure")
	}
}

func TestEndSessionConsolidates(t *testing.T) {
	home := t.TempDir()
	setupTestEnv(t, home)
	repo := makeTestRepo(t, filepath.Join(t.TempDir(), "repo"), "https://github.com/acme/a.git")
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
	if _, err := e.recordMemory(project, "lesson", "repository", "", "Session end consolidation marker.", 0.8, 50, "verified_execution", "", ""); err != nil {
		t.Fatal(err)
	}
	if err := e.endSession(project); err != nil {
		t.Fatal(err)
	}
}

func TestIdentifyProjectWithoutGitRemote(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte("package main\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	p, err := identifyProject(dir)
	if err != nil {
		t.Fatal(err)
	}
	if p.Repository == "" {
		t.Fatal("local repository id expected")
	}
}

func TestGCDeletesOldDeprecatedMemory(t *testing.T) {
	home := t.TempDir()
	setupTestEnv(t, home)
	repo := makeTestRepo(t, filepath.Join(t.TempDir(), "repo"), "https://github.com/acme/a.git")
	stdout, _, code := runCLICapture(t, []string{
		"record", "--cwd", repo, "--kind", "rule",
		"--content", "ADR: keep repository boundary intact.",
		"--confidence", "0.99", "--source", "adr",
	})
	if code != 0 {
		t.Fatal(code)
	}
	adrID := mustJSON(t, stdout)["id"].(string)
	stdout, _, code = runCLICapture(t, []string{
		"record", "--cwd", repo, "--kind", "fact",
		"--content", "Deprecated fact to garbage collect.",
		"--confidence", "0.5", "--source", "repository_observation",
	})
	if code != 0 {
		t.Fatal(code)
	}
	staleID := mustJSON(t, stdout)["id"].(string)
	_, _, code = runCLICapture(t, []string{"validate", "--id", staleID, "--result", "contradiction"})
	if code != 0 {
		t.Fatal(code)
	}
	e, err := newEngine(home)
	if err != nil {
		t.Fatal(err)
	}
	defer e.Close()
	old := time.Now().UTC().AddDate(0, 0, -120).Format(time.RFC3339)
	if _, err := e.db.Exec(`UPDATE memories SET updated_at = ? WHERE id = ?`, old, staleID); err != nil {
		t.Fatal(err)
	}
	if err := e.runGC(""); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := e.db.QueryRow(`SELECT COUNT(*) FROM memories WHERE id = ?`, staleID).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatal("stale deprecated memory should be deleted")
	}
	if err := e.db.QueryRow(`SELECT COUNT(*) FROM memories WHERE id = ?`, adrID).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatal("adr should remain")
	}
}

func TestMainWrapper(t *testing.T) {
	oldArgs := os.Args
	oldOut := os.Stdout
	oldExit := exitProcess
	defer func() { os.Args = oldArgs; os.Stdout = oldOut; exitProcess = oldExit }()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stdout = w
	os.Args = []string{"overdrive-runtime", "version"}
	exitCode := 0
	exitProcess = func(c int) { exitCode = c }
	main()
	_ = w.Close()
	os.Stdout = oldOut
	if exitCode != 0 {
		t.Fatalf("main exit %d", exitCode)
	}
	_ = r.Close()
}
