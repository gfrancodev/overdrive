package main

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestEmbedderDimChangeRebuildsIndex(t *testing.T) {
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
	_, err = e.recordMemory(project, "lesson", "repository", "", "Index rebuild marker lesson.", 0.9, 50, "verified_execution", "", "")
	if err != nil {
		t.Fatal(err)
	}
	e.setMeta("embedder_dim", "999")
	if err := e.ensureVectorIndex(); err != nil {
		t.Fatal(err)
	}
	if e.getMeta("embedder_dim") != "256" {
		t.Fatalf("dim meta %s", e.getMeta("embedder_dim"))
	}
}

func TestImportPeerBatchEdgeCases(t *testing.T) {
	home := t.TempDir()
	setupTestEnv(t, home)
	e, err := newEngine(home)
	if err != nil {
		t.Fatal(err)
	}
	defer e.Close()
	circle := Circle{
		ID: "circle_a", Members: []CircleMember{{DeviceID: "dev_a", PublicKey: "pk", Revoked: false}},
	}
	if _, err := e.importPeerBatch(circle, SyncResponse{CircleID: "other"}); err == nil {
		t.Fatal("circle mismatch")
	}
	if _, err := e.importPeerBatch(circle, SyncResponse{CircleID: "circle_a", DeviceID: "dev_revoked"}); err == nil {
		t.Fatal("revoked peer")
	}
	resp := SyncResponse{
		CircleID: "circle_a", DeviceID: "dev_a",
		Packets: []SharedPacket{{
			ID: "mem_peer_edge", Kind: "lesson", ScopeID: "github.com/acme/r",
			PacketContent: "peer packet", LessonContent: "body", HotIndex: true,
			VectorSpace: "minilm", Fingerprint: "mem_peer_edge", EvidenceScore: 0.7,
			UpdatedAt: nowRFC3339(),
		}},
	}
	n, err := e.importPeerBatch(circle, resp)
	if err != nil || n != 0 {
		t.Fatalf("vector space filter imported=%d err=%v", n, err)
	}
	resp.Packets[0].VectorSpace = "stub"
	n, err = e.importPeerBatch(circle, resp)
	if err != nil || n != 1 {
		t.Fatalf("imported=%d err=%v", n, err)
	}
	resp.Packets[0].EvidenceScore = 0.1
	n, err = e.importPeerBatch(circle, resp)
	if err != nil || n != 0 {
		t.Fatalf("lower evidence should skip imported=%d", n)
	}
}

func TestMarkPeerRevokedAndPeerEligible(t *testing.T) {
	home := t.TempDir()
	setupTestEnv(t, home)
	e, err := newEngine(home)
	if err != nil {
		t.Fatal(err)
	}
	defer e.Close()
	m := Memory{
		ID: "mem_peer_elig", Kind: "lesson", Scope: "repository", ScopeID: "github.com/acme/r",
		Status: "active", Origin: "peer", HotIndex: true, CircleID: "missing", PeerDeviceID: "dev_x",
	}
	if e.peerEligible(m, Project{Repository: "github.com/acme/r", Root: t.TempDir()}) {
		t.Fatal("missing circle should fail eligibility")
	}
	home2 := t.TempDir()
	setupTestEnv(t, home2)
	id, priv, _ := loadOrCreateIdentity(home2)
	circle, _ := createCircle(home2, "c", id, priv)
	repo := makeTestRepo(t, filepath.Join(t.TempDir(), "r"), "https://github.com/acme/r.git")
	circle, _ = addAllowedFolder(home2, circle.ID, repo, id)
	e2, _ := newEngine(home2)
	defer e2.Close()
	pm := Memory{
		ID: "mem_peer_rev", Kind: "lesson", Scope: "repository", ScopeID: "github.com/acme/r",
		Status: "active", Origin: "peer", HotIndex: true, CircleID: circle.ID, PeerDeviceID: id.DeviceID,
		Content: "peer to revoke", VectorSpace: "stub", Layer: 2,
		CreatedAt: nowRFC3339(), UpdatedAt: nowRFC3339(),
	}
	if err := e2.upsertPeerMemory(pm, embedText(pm.Content)); err != nil {
		t.Fatal(err)
	}
	if err := e2.markPeerRevoked(circle.ID, id.DeviceID); err != nil {
		t.Fatal(err)
	}
}

func TestConsolidateSessionDemotesWeakMemory(t *testing.T) {
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
	m, err := e.recordMemory(project, "lesson", "repository", "", "Weak lesson to demote.", 0.3, 50, "agent_observation", "", "")
	if err != nil {
		t.Fatal(err)
	}
	_, _ = e.db.Exec(`UPDATE memories SET failure_count = 3, success_count = 0, confidence = 0.3 WHERE id = ?`, m.ID)
	circle := Circle{ID: "c1", AllowedFolders: []string{repo}}
	if err := e.consolidateSession(project, circle); err != nil {
		t.Fatal(err)
	}
	var hot int
	if err := e.db.QueryRow(`SELECT hot_index FROM memories WHERE id = ?`, m.ID).Scan(&hot); err != nil {
		t.Fatal(err)
	}
	if hot != 0 {
		t.Fatal("expected cold index after consolidate")
	}
}

func TestTurbovecRecallReranksWhenAvailable(t *testing.T) {
	lib := filepath.Join("..", "turbovec-ffi", "target", "release", "liboverdrive_turbovec_ffi.so")
	if _, err := os.Stat(lib); err != nil {
		t.Skip("turbovec not built")
	}
	home := t.TempDir()
	setupTestEnv(t, home)
	abs, _ := filepath.Abs(lib)
	t.Setenv("OVERDRIVE_TURBOVEC_LIB", abs)
	repo := makeTestRepo(t, filepath.Join(t.TempDir(), "repo"), "https://github.com/acme/a.git")
	_, _, code := runCLICapture(t, []string{
		"record", "--cwd", repo, "--kind", "lesson",
		"--content", "Use ProposalRepository for proposal persistence changes.",
		"--confidence", "0.95", "--source", "verified_execution",
	})
	if code != 0 {
		t.Fatal(code)
	}
	_, _, code = runCLICapture(t, []string{
		"record", "--cwd", repo, "--kind", "lesson",
		"--content", "Use CustomerFactory for customer integration fixtures.",
		"--confidence", "0.95", "--source", "verified_execution",
	})
	if code != 0 {
		t.Fatal(code)
	}
	stdout, _, code := runCLICapture(t, []string{
		"recall", "--cwd", repo, "--query", "proposal persistence repository",
	})
	if code != 0 {
		t.Fatal(code)
	}
	data := mustJSON(t, stdout)
	memories, _ := data["memories"].([]any)
	if len(memories) == 0 {
		t.Fatal("expected recall hit")
	}
	top := memories[0].(map[string]any)["content"].(string)
	if strings.Contains(top, "CustomerFactory") {
		t.Fatal("turbovec should prefer proposal lesson")
	}
}

func TestShareListenAddrAndSmallHelpers(t *testing.T) {
	t.Setenv("OVERDRIVE_SHARE_LISTEN", "10.0.0.1:9999")
	if shareListenAddr() != "10.0.0.1:9999" {
		t.Fatal("listen addr override")
	}
	t.Setenv("OVERDRIVE_SHARE_LISTEN", "")
	if !strings.Contains(shareListenAddr(), ":") {
		t.Fatal("default listen addr")
	}
	if !containsString([]string{"a", "b"}, "a") || containsString([]string{"a"}, "z") {
		t.Fatal("containsString")
	}
	if maxInt(3, 9) != 9 {
		t.Fatal("maxInt")
	}
}

func TestEnsureFileModelURLOverride(t *testing.T) {
	dir := t.TempDir()
	dest := filepath.Join(dir, "models", "model.onnx")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("onnx-model"))
	}))
	defer srv.Close()
	t.Setenv("OVERDRIVE_SKIP_EMBED_DOWNLOAD", "0")
	t.Setenv("OVERDRIVE_MODEL_URL", srv.URL)
	oldGet := downloadHTTPGet
	downloadHTTPGet = func(client *http.Client, url string) (*http.Response, error) {
		return client.Get(srv.URL)
	}
	defer func() { downloadHTTPGet = oldGet }()
	if err := ensureFile("http://ignored.example/model", dest); err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(dest)
	if err != nil || string(body) != "onnx-model" {
		t.Fatal("model override download")
	}
}

func TestMigrateJSONInvalid(t *testing.T) {
	home := t.TempDir()
	if err := os.WriteFile(filepath.Join(home, "experience-v1.json"), []byte("{"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := migrateJSONIfNeeded(home); err == nil {
		t.Fatal("invalid json should fail")
	}
}

func TestJoinRequestHandler(t *testing.T) {
	home := t.TempDir()
	setupTestEnv(t, home)
	idA, privA, _ := loadOrCreateIdentity(home)
	pubB, privB, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	idB := DeviceIdentity{
		DeviceID:  "dev_" + sha256Hex(pubB)[:16],
		PublicKey: base64.StdEncoding.EncodeToString(pubB),
	}
	circle, _ := createCircle(home, "team", idA, privA)
	invite, _ := createInvite(home, circle.ID, idA)
	req := JoinRequest{
		CircleID: circle.ID, DeviceID: idB.DeviceID, PublicKey: idB.PublicKey,
		Code: invite.Code, Timestamp: nowRFC3339(),
	}
	req, _ = signJoinRequest(privB, req)
	payload, _ := json.Marshal(req)
	cipher, _ := encryptBatch(circle.GroupKey, payload)
	sl := &shareListener{home: home}
	if !sl.tryHandleJoin(wireEnvelope{CircleID: circle.ID, Ciphertext: cipher, Kind: "join"}, payload) {
		t.Fatal("join handler should accept")
	}
	updated, err := loadCircle(home, circle.ID)
	if err != nil || len(updated.Members) < 2 {
		t.Fatalf("members=%d err=%v", len(updated.Members), err)
	}
}

func TestCLIValidationPaths(t *testing.T) {
	home := t.TempDir()
	setupTestEnv(t, home)
	repo := makeTestRepo(t, filepath.Join(t.TempDir(), "repo"), "https://github.com/acme/a.git")
	_, stderr, code := runCLICapture(t, []string{
		"record", "--cwd", repo, "--kind", "bogus", "--content", "x",
	})
	if code != 2 || !strings.Contains(stderr, "invalid --kind") {
		t.Fatalf("kind: %d %q", code, stderr)
	}
	_, stderr, code = runCLICapture(t, []string{
		"record", "--cwd", repo, "--kind", "lesson", "--scope", "bogus", "--content", "x",
	})
	if code != 2 || !strings.Contains(stderr, "invalid --scope") {
		t.Fatalf("scope: %d %q", code, stderr)
	}
	_, stderr, code = runCLICapture(t, []string{"ledger-add", "--cwd", repo})
	if code != 2 || !strings.Contains(stderr, "--run and --decision") {
		t.Fatalf("ledger: %d %q", code, stderr)
	}
}

func TestHashedEmbedderAndVectorScoreFloor(t *testing.T) {
	h := hashedEmbedder{}
	if len(h.Embed("hashed path")) != hashedDims {
		t.Fatal("hashed embed")
	}
	resetRuntimeGlobals()
	home := t.TempDir()
	t.Setenv("OVERDRIVE_HOME", home)
	t.Setenv("OVERDRIVE_EMBEDDER", "hashed")
	if vectorScoreFloor() != hashedScoreFloor {
		t.Fatal("hashed floor")
	}
}

func TestListExportableMemoriesBlockedFolder(t *testing.T) {
	home := t.TempDir()
	setupTestEnv(t, home)
	e, _ := newEngine(home)
	defer e.Close()
	circle := Circle{ID: "c1", AllowedFolders: []string{"/tmp/allowed-only"}}
	project := Project{Root: "/tmp/private", Repository: "github.com/acme/r"}
	out, err := e.listExportableMemories(project, circle)
	if err != nil || len(out) != 0 {
		t.Fatalf("blocked export len=%d err=%v", len(out), err)
	}
}
