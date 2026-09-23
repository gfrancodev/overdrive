package main

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

type errReader struct{ err error }

func (e errReader) Read([]byte) (int, error) { return 0, e.err }

func TestEngineBootstrapErrors(t *testing.T) {
	if _, err := newEngine(""); err == nil {
		t.Fatal("empty home")
	}
	home := t.TempDir()
	setupTestEnv(t, home)
	e, err := openEngine()
	if err != nil {
		t.Fatal(err)
	}
	e.Close()
	e2, err := openEngineWithoutMigrate(home)
	if err != nil {
		t.Fatal(err)
	}
	e2.Close()
	if err := withEngine(func(e *Engine) error {
		if e == nil {
			t.Fatal("nil engine")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func TestParseHelpers(t *testing.T) {
	if parseFloat("", 0.5) != 0.5 || parseFloat("bad", 0.5) != 0.5 || parseFloat("0.25", 0) != 0.25 {
		t.Fatal("parseFloat")
	}
	if parseInt("", 7) != 7 || parseInt("bad", 7) != 7 || parseInt("42", 0) != 42 {
		t.Fatal("parseInt")
	}
	if sanitizeRunID("../evil/run") != "-evil-run" || sanitizeRunID("") != "default" {
		t.Fatal("sanitizeRunID")
	}
}

func TestRunGCStaleMemory(t *testing.T) {
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
	m, err := e.recordMemory(project, "fact", "repository", "", "Stale low-confidence fact.", 0.2, 10, "repository_observation", "", "")
	if err != nil {
		t.Fatal(err)
	}
	_, _ = e.db.Exec(`UPDATE memories SET status = 'stale', confidence = 0.2 WHERE id = ?`, m.ID)
	old := time.Now().UTC().AddDate(0, 0, -200).Format(time.RFC3339)
	_, _ = e.db.Exec(`UPDATE memories SET updated_at = ? WHERE id = ?`, old, m.ID)
	if err := e.runGC(""); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := e.db.QueryRow(`SELECT COUNT(*) FROM memories WHERE id = ?`, m.ID).Scan(&count); err != nil || count != 0 {
		t.Fatalf("stale deleted count=%d err=%v", count, err)
	}
}

func TestRecordMemoryMergePath(t *testing.T) {
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
	first, err := e.recordMemory(project, "lesson", "repository", "subj", "Merge path lesson.", 0.5, 40, "verified_execution", "", "evidence one")
	if err != nil {
		t.Fatal(err)
	}
	second, err := e.recordMemory(project, "lesson", "repository", "subj", "Merge path lesson.", 0.9, 80, "adr", "ref-1", "evidence two")
	if err != nil || second.ID != first.ID || second.SuccessCount < 1 {
		t.Fatalf("merge id=%s count=%d err=%v", second.ID, second.SuccessCount, err)
	}
}

func TestPeerEligibleAndRecallEnabled(t *testing.T) {
	home := t.TempDir()
	setupTestEnv(t, home)
	repo := makeTestRepo(t, filepath.Join(t.TempDir(), "repo"), "https://github.com/acme/r.git")
	id, priv, _ := loadOrCreateIdentity(home)
	circle, _ := createCircle(home, "team", id, priv)
	circle, _ = addAllowedFolder(home, circle.ID, repo, id)
	e, _ := newEngine(home)
	defer e.Close()
	project, _ := identifyProject(repo)
	if !e.peerRecallEnabled(project) {
		t.Fatal("peer recall enabled")
	}
	pm := Memory{
		ID: "mem_peer_ok", Kind: "lesson", Scope: "repository", ScopeID: project.Repository,
		Status: "active", Origin: "peer", HotIndex: true, CircleID: circle.ID, PeerDeviceID: id.DeviceID,
		Content: "peer lesson", VectorSpace: "stub", Layer: 2, CreatedAt: nowRFC3339(), UpdatedAt: nowRFC3339(),
	}
	if !e.peerEligible(pm, project) {
		t.Fatal("peer eligible")
	}
}

func TestReindexPeerMemories(t *testing.T) {
	home := t.TempDir()
	setupTestEnv(t, home)
	e, err := newEngine(home)
	if err != nil {
		t.Fatal(err)
	}
	defer e.Close()
	pm := Memory{
		ID: "mem_peer_reindex", Kind: "lesson", Scope: "repository", ScopeID: "github.com/acme/r",
		Status: "active", Origin: "peer", HotIndex: true, Content: "peer reindex body",
		VectorSpace: "stub", Layer: 2, CreatedAt: nowRFC3339(), UpdatedAt: nowRFC3339(),
	}
	if err := e.upsertPeerMemory(pm, nil); err != nil {
		t.Fatal(err)
	}
	if err := e.reindexActiveMemories(); err != nil {
		t.Fatal(err)
	}
}

func TestImportPeerBatchSkipsLowerEvidence(t *testing.T) {
	home := t.TempDir()
	setupTestEnv(t, home)
	e, _ := newEngine(home)
	defer e.Close()
	circle := Circle{
		ID: "circle_x", Members: []CircleMember{{DeviceID: "dev_a", PublicKey: "pk", Revoked: false}},
	}
	existing := Memory{
		ID: "mem_peer_skip", Kind: "lesson", Scope: "repository", ScopeID: "github.com/acme/r",
		Status: "active", Origin: "peer", HotIndex: true, EvidenceScore: 0.95, VectorSpace: "stub",
		Content: "existing", Layer: 2, CreatedAt: nowRFC3339(), UpdatedAt: nowRFC3339(),
	}
	if err := e.upsertPeerMemory(existing, embedText(existing.Content)); err != nil {
		t.Fatal(err)
	}
	resp := SyncResponse{
		CircleID: "circle_x", DeviceID: "dev_a",
		Packets: []SharedPacket{{
			ID: "mem_peer_skip", Kind: "lesson", ScopeID: "github.com/acme/r",
			PacketContent: "new packet", LessonContent: "new", HotIndex: true,
			VectorSpace: "stub", Fingerprint: "mem_peer_skip", EvidenceScore: 0.2, UpdatedAt: nowRFC3339(),
		}},
	}
	n, err := e.importPeerBatch(circle, resp)
	if err != nil || n != 0 {
		t.Fatalf("skip lower evidence imported=%d err=%v", n, err)
	}
}

func TestUpsertPeerMemoryWithVector(t *testing.T) {
	home := t.TempDir()
	setupTestEnv(t, home)
	e, _ := newEngine(home)
	defer e.Close()
	vec := embedText("vector path")
	m := Memory{
		ID: "mem_peer_vec", Kind: "lesson", Scope: "repository", ScopeID: "github.com/acme/r",
		Status: "active", Origin: "peer", HotIndex: true, Content: "vector path",
		VectorSpace: "stub", Layer: 2, CreatedAt: nowRFC3339(), UpdatedAt: nowRFC3339(),
	}
	if err := e.upsertPeerMemory(m, vec); err != nil {
		t.Fatal(err)
	}
}

func TestStartSessionConsolidatesPrevious(t *testing.T) {
	home := t.TempDir()
	setupTestEnv(t, home)
	repo := makeTestRepo(t, filepath.Join(t.TempDir(), "repo"), "https://github.com/acme/a.git")
	id, priv, _ := loadOrCreateIdentity(home)
	circle, _ := createCircle(home, "solo", id, priv)
	circle, _ = addAllowedFolder(home, circle.ID, repo, id)
	e, _ := newEngine(home)
	defer e.Close()
	project, _ := identifyProject(repo)
	s1, err := e.startSession(project)
	if err != nil || s1 == "" {
		t.Fatal(err)
	}
	_, _ = e.recordMemory(project, "lesson", "repository", "", "Session two consolidation.", 0.9, 50, "verified_execution", "", "")
	s2, err := e.startSession(project)
	if err != nil || s2 == s1 {
		t.Fatalf("sessions %q %q err=%v", s1, s2, err)
	}
}

func TestEndSessionWithAllowedCircle(t *testing.T) {
	home := t.TempDir()
	setupTestEnv(t, home)
	repo := makeTestRepo(t, filepath.Join(t.TempDir(), "repo"), "https://github.com/acme/a.git")
	id, priv, _ := loadOrCreateIdentity(home)
	circle, _ := createCircle(home, "solo", id, priv)
	circle, _ = addAllowedFolder(home, circle.ID, repo, id)
	e, _ := newEngine(home)
	defer e.Close()
	project, _ := identifyProject(repo)
	_, _ = e.startSession(project)
	_, _ = e.recordMemory(project, "lesson", "repository", "", "End session export.", 0.9, 50, "verified_execution", "", "")
	if err := e.endSession(project); err != nil {
		t.Fatal(err)
	}
}

func TestBuildSyncResponseSinceFilter(t *testing.T) {
	home := t.TempDir()
	setupTestEnv(t, home)
	repo := makeTestRepo(t, filepath.Join(t.TempDir(), "repo"), "https://github.com/acme/payments-api.git")
	id, priv, _ := loadOrCreateIdentity(home)
	circle, _ := createCircle(home, "team", id, priv)
	circle, _ = addAllowedFolder(home, circle.ID, repo, id)
	e, _ := newEngine(home)
	defer e.Close()
	project, _ := identifyProject(repo)
	_, _ = e.recordMemory(project, "lesson", "repository", "sig", "Sync since filter lesson.", 0.9, 50, "verified_execution", "", "evidence")
	resp, err := e.buildSyncResponse(home, circle, id, priv, SyncRequest{
		DeviceID: id.DeviceID, CircleID: circle.ID, Repository: project.Repository, Since: "2099-01-01T00:00:00Z",
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(resp.Packets) != 0 {
		t.Fatalf("since filter should skip packets: %d", len(resp.Packets))
	}
}

func TestListExportableMemoriesResolvesFolder(t *testing.T) {
	home := t.TempDir()
	setupTestEnv(t, home)
	repo := makeTestRepo(t, filepath.Join(t.TempDir(), "repo"), "https://github.com/acme/r.git")
	id, priv, _ := loadOrCreateIdentity(home)
	circle, _ := createCircle(home, "team", id, priv)
	circle, _ = addAllowedFolder(home, circle.ID, repo, id)
	e, _ := newEngine(home)
	defer e.Close()
	project := Project{Repository: "github.com/acme/r"}
	_, _ = e.recordMemory(Project{Root: repo, Repository: project.Repository}, "lesson", "repository", "", "Exportable lesson.", 0.9, 50, "verified_execution", "", "")
	out, err := e.listExportableMemories(project, circle)
	if err != nil || len(out) == 0 {
		t.Fatalf("exportable len=%d err=%v", len(out), err)
	}
}

func TestShareCircleErrorPaths(t *testing.T) {
	home := t.TempDir()
	setupTestEnv(t, home)
	_, stderr, code := runCLICapture(t, []string{"share"})
	if code != 2 || !strings.Contains(stderr, "usage") {
		t.Fatalf("share usage: %d %q", code, stderr)
	}
	_, stderr, code = runCLICapture(t, []string{"share", "circle"})
	if code != 2 || !strings.Contains(stderr, "usage") {
		t.Fatalf("circle usage: %d %q", code, stderr)
	}
	_, stderr, code = runCLICapture(t, []string{"share", "circle", "create"})
	if code != 2 || !strings.Contains(stderr, "--name") {
		t.Fatalf("create: %d %q", code, stderr)
	}
	_, stderr, code = runCLICapture(t, []string{"share", "circle", "invite"})
	if code != 2 || !strings.Contains(stderr, "--circle") {
		t.Fatalf("invite: %d %q", code, stderr)
	}
	_, stderr, code = runCLICapture(t, []string{"share", "circle", "accept"})
	if code != 2 || !strings.Contains(stderr, "--code") {
		t.Fatalf("accept: %d %q", code, stderr)
	}
	_, stderr, code = runCLICapture(t, []string{"share", "circle", "revoke"})
	if code != 2 || !strings.Contains(stderr, "--circle") {
		t.Fatalf("revoke: %d %q", code, stderr)
	}
	_, stderr, code = runCLICapture(t, []string{"share", "circle", "folder-add"})
	if code != 2 || !strings.Contains(stderr, "--circle") {
		t.Fatalf("folder-add: %d %q", code, stderr)
	}
	_, stderr, code = runCLICapture(t, []string{"share", "circle", "folder-list"})
	if code != 2 || !strings.Contains(stderr, "--circle") {
		t.Fatalf("folder-list: %d %q", code, stderr)
	}
}

func TestAcceptInviteErrors(t *testing.T) {
	home := t.TempDir()
	setupTestEnv(t, home)
	id, priv, _ := loadOrCreateIdentity(home)
	circle, _ := createCircle(home, "team", id, priv)
	invite, _ := createInvite(home, circle.ID, id)
	if _, err := acceptInvite(home, invite.Code, "wrong", "", id, priv); err == nil {
		t.Fatal("fingerprint mismatch")
	}
	expired := invite
	expired.ExpiresAt = "2020-01-01T00:00:00Z"
	raw, _ := json.Marshal(expired)
	_ = os.WriteFile(filepath.Join(invitesDir(home), invite.Code+".json"), raw, 0o600)
	if _, err := acceptInvite(home, invite.Code, invite.Fingerprint, "", id, priv); err == nil {
		t.Fatal("expired invite")
	}
}

func TestCreateInviteAndRevokeErrors(t *testing.T) {
	home := t.TempDir()
	setupTestEnv(t, home)
	id, priv, _ := loadOrCreateIdentity(home)
	pub, privB, _ := ed25519.GenerateKey(rand.Reader)
	idB := DeviceIdentity{DeviceID: "dev_" + sha256Hex(pub)[:16], PublicKey: base64.StdEncoding.EncodeToString(pub)}
	circle, _ := createCircle(home, "team", id, priv)
	if _, err := createInvite(home, circle.ID, idB); err == nil {
		t.Fatal("non-member invite")
	}
	if _, err := revokeMember(home, circle.ID, "missing", idB, privB); err == nil {
		t.Fatal("non-creator revoke")
	}
}

func TestJoinRequestRejections(t *testing.T) {
	home := t.TempDir()
	setupTestEnv(t, home)
	idA, privA, _ := loadOrCreateIdentity(home)
	pubB, privB, _ := ed25519.GenerateKey(rand.Reader)
	idB := DeviceIdentity{DeviceID: "dev_" + sha256Hex(pubB)[:16], PublicKey: base64.StdEncoding.EncodeToString(pubB)}
	circle, _ := createCircle(home, "team", idA, privA)
	invite, _ := createInvite(home, circle.ID, idA)
	sl := &shareListener{home: home}
	req := JoinRequest{CircleID: circle.ID, DeviceID: idB.DeviceID, PublicKey: idB.PublicKey, Code: invite.Code, Timestamp: nowRFC3339()}
	req, _ = signJoinRequest(privB, req)
	payload, _ := json.Marshal(req)
	payload[0] = 'x' // corrupt json branch
	sl.tryHandleJoin(wireEnvelope{Kind: "join"}, payload)
	expiredInvite := invite
	expiredInvite.ExpiresAt = "2020-01-01T00:00:00Z"
	raw, _ := json.Marshal(expiredInvite)
	_ = os.WriteFile(filepath.Join(invitesDir(home), invite.Code+".json"), raw, 0o600)
	sl.tryHandleJoin(wireEnvelope{Kind: "join"}, []byte(`{"circle_id":"`+circle.ID+`","device_id":"x","public_key":"`+idB.PublicKey+`","code":"`+invite.Code+`","timestamp":"`+nowRFC3339()+`"}`))
}

func TestNotifyPeerJoin(t *testing.T) {
	home := t.TempDir()
	setupTestEnv(t, home)
	idA, privA, _ := loadOrCreateIdentity(home)
	circle, _ := createCircle(home, "team", idA, privA)
	invite, _ := createInvite(home, circle.ID, idA)
	pubB, privB, _ := ed25519.GenerateKey(rand.Reader)
	idB := DeviceIdentity{DeviceID: "dev_" + sha256Hex(pubB)[:16], PublicKey: base64.StdEncoding.EncodeToString(pubB)}
	if err := notifyPeerJoin(invite, "127.0.0.1:1", idB, privB); err == nil {
		t.Fatal("unreachable endpoint should error")
	}
	sl, endpoint := startShareListenerForTest(t, home)
	defer sl.Close()
	if err := notifyPeerJoin(invite, endpoint, idB, privB); err != nil {
		t.Fatal(err)
	}
}

func TestSyncCirclePeersSessionStart(t *testing.T) {
	repo := makeTestRepo(t, filepath.Join(t.TempDir(), "repo"), "https://github.com/acme/payments-api.git")
	homeA, homeB, _, stop := setupCirclePair(t, repo)
	defer stop()
	setupTestEnv(t, homeA)
	t.Setenv("OVERDRIVE_HOME", homeA)
	_, _, code := runCLICapture(t, []string{
		"record", "--cwd", repo, "--kind", "lesson",
		"--content", "Sync on session-start marker lesson.",
		"--confidence", "0.95", "--source", "verified_execution",
	})
	if code != 0 {
		t.Fatal(code)
	}
	setupTestEnv(t, homeB)
	t.Setenv("OVERDRIVE_HOME", homeB)
	port := freeTCPPort(t)
	t.Setenv("OVERDRIVE_SHARE_LISTEN", fmt.Sprintf("127.0.0.1:%d", port))
	_, _, code = runCLICapture(t, []string{"session-start", "--cwd", repo})
	if code != 0 {
		t.Fatal(code)
	}
}

func TestCLIRecallLayerAndLedgerFields(t *testing.T) {
	home := t.TempDir()
	setupTestEnv(t, home)
	repo := makeTestRepo(t, filepath.Join(t.TempDir(), "repo"), "https://github.com/acme/a.git")
	_, _, code := runCLICapture(t, []string{
		"record", "--cwd", repo, "--kind", "episode",
		"--content", "Episode layer recall marker.",
		"--confidence", "0.8", "--source", "agent_observation",
	})
	if code != 0 {
		t.Fatal(code)
	}
	stdout, _, code := runCLICapture(t, []string{"recall", "--cwd", repo, "--query", "episode layer", "--layer", "episodes"})
	if code != 0 {
		t.Fatal(code)
	}
	data := mustJSON(t, stdout)
	memories, _ := data["memories"].([]any)
	if len(memories) == 0 {
		t.Fatal("layer filter")
	}
	_, _, code = runCLICapture(t, []string{
		"ledger-add", "--cwd", repo,
		"--run", "run-full",
		"--decision", "Use repository boundary",
		"--evidence", "tests",
		"--reason", "consistency",
		"--risk", "regression",
		"--reversibility", "easy",
	})
	if code != 0 {
		t.Fatal(code)
	}
	body, err := os.ReadFile(filepath.Join(home, "runs", "run-full", "ledger.md"))
	if err != nil || !strings.Contains(string(body), "Risk if wrong") {
		t.Fatal("ledger fields")
	}
}

func TestCLISessionStartOutputAndRunCLIErrors(t *testing.T) {
	home := t.TempDir()
	setupTestEnv(t, home)
	repo := makeTestRepo(t, filepath.Join(t.TempDir(), "repo"), "https://github.com/acme/a.git")
	_, stderr, code := runCLICapture(t, []string{})
	if code != 2 || !strings.Contains(stderr, "usage") {
		t.Fatalf("no args: %d %q", code, stderr)
	}
	_, stderr, code = runCLICapture(t, []string{"nope"})
	if code != 2 || !strings.Contains(stderr, "unknown command") {
		t.Fatalf("unknown: %d %q", code, stderr)
	}
	stdout, _, code := runCLICapture(t, []string{"session-start", "--cwd", repo})
	if code != 0 {
		t.Fatal(code)
	}
	data := mustJSON(t, stdout)
	if data["session_id"] == nil || data["share"] == nil {
		t.Fatal("session-start output")
	}
	_, stderr, code = runCLICapture(t, []string{"record", "--cwd", repo, "--kind", "lesson", "--content", ""})
	if code != 2 || !strings.Contains(stderr, "--content is required") {
		t.Fatalf("record content: %d %q", code, stderr)
	}
}

func TestDownloadFailureBranches(t *testing.T) {
	path := filepath.Join(t.TempDir(), "out.bin")
	if err := atomicWriteFile(path, errReader{err: errors.New("copy fail")}, 0o644); err == nil {
		t.Fatal("copy error")
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()
	oldGet := downloadHTTPGet
	downloadHTTPGet = func(client *http.Client, url string) (*http.Response, error) {
		return client.Get(srv.URL)
	}
	defer func() { downloadHTTPGet = oldGet }()
	if err := downloadURL(srv.URL, path); err == nil {
		t.Fatal("http status")
	}
	archive := filepath.Join(t.TempDir(), "empty.tgz")
	var buf bytes.Buffer
	gzw := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gzw)
	_ = tw.Close()
	_ = gzw.Close()
	if err := os.WriteFile(archive, buf.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := extractORTArchive(archive, t.TempDir()); err == nil {
		t.Fatal("missing lib in archive")
	}
}

func TestCircleForAnyAndDeleteMemory(t *testing.T) {
	home := t.TempDir()
	setupTestEnv(t, home)
	repo := makeTestRepo(t, filepath.Join(t.TempDir(), "repo"), "https://github.com/acme/a.git")
	id, priv, _ := loadOrCreateIdentity(home)
	circle, _ := createCircle(home, "solo", id, priv)
	circle, _ = addAllowedFolder(home, circle.ID, repo, id)
	if _, ok := circleForAny(home); !ok {
		t.Fatal("circleForAny")
	}
	e, _ := newEngine(home)
	defer e.Close()
	project, _ := identifyProject(repo)
	m, _ := e.recordMemory(project, "fact", "repository", "", "Delete me.", 0.5, 10, "agent_observation", "", "")
	if err := e.deleteMemory(m.ID); err != nil {
		t.Fatal(err)
	}
}

func TestVectorScoreFloorMinilm(t *testing.T) {
	home := t.TempDir()
	lib := filepath.Join(home, "libonnxruntime.so")
	_ = os.WriteFile(lib, []byte("fake-lib"), 0o644)
	dir := modelDir(home)
	_ = os.MkdirAll(dir, 0o700)
	for name, content := range map[string]string{
		"model.onnx": "model", "vocab.txt": "[CLS]\n[SEP]\nhello\n", "tokenizer.json": "{}",
	} {
		_ = os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644)
	}
	resetORTLibForTest()
	resetRuntimeGlobals()
	t.Setenv("OVERDRIVE_HOME", home)
	t.Setenv("OVERDRIVE_ORT_LIB", lib)
	t.Setenv("OVERDRIVE_SKIP_EMBED_DOWNLOAD", "1")
	t.Setenv("OVERDRIVE_EMBEDDER", "minilm")
	old := ortSessionOpener
	ortSessionOpener = func(libPath, modelPath string) (modelRunner, error) {
		return fakeModelRunner{}, nil
	}
	defer func() { ortSessionOpener = old }()
	initEmbedder(home)
	if vectorScoreFloor() != miniLMScoreFloor {
		t.Fatalf("floor %v", vectorScoreFloor())
	}
}

func TestTokenizerAndScopeHelpers(t *testing.T) {
	dir := t.TempDir()
	vocab := filepath.Join(dir, "vocab.txt")
	content := "[CLS]\n[SEP]\nhello\nworld\n##lo\nunk\n\n"
	if err := os.WriteFile(vocab, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	tok, err := loadWordPieceTokenizer(vocab)
	if err != nil {
		t.Fatal(err)
	}
	tokens := tok.basicTokenize("hello, world! 42")
	if len(tokens) == 0 {
		t.Fatal("basicTokenize")
	}
	ids, mask, types := tok.Encode("hello world", 8)
	if len(ids) != 8 || len(mask) != 8 || len(types) != 8 {
		t.Fatal("encode padding")
	}
	if parseIntEnv("MISSING_ENV", 9) != 9 {
		t.Fatal("parseIntEnv missing")
	}
	t.Setenv("OVERDRIVE_TEST_INT", "bad")
	if parseIntEnv("OVERDRIVE_TEST_INT", 9) != 9 {
		t.Fatal("parseIntEnv bad")
	}
	t.Setenv("OVERDRIVE_TEST_INT", "12")
	if parseIntEnv("OVERDRIVE_TEST_INT", 9) != 12 {
		t.Fatal("parseIntEnv value")
	}
	p := Project{Repository: "github.com/acme/r", Organization: "github.com/acme", Module: "pkg"}
	if scopeWeight(Memory{Scope: "organization", ScopeID: p.Organization}, p) <= 0 {
		t.Fatal("org scope weight")
	}
	if scopeWeight(Memory{Scope: "module", ScopeID: scopeIDFor("module", p)}, p) <= 0 {
		t.Fatal("module scope weight")
	}
}

func TestSharedPacketRoundtrip(t *testing.T) {
	m := Memory{
		ID: "mem_pkt", Kind: "lesson", ScopeID: "github.com/acme/r", Subject: "sig",
		Content: "Body", PacketContent: "", ProblemSignature: "sig", EvidenceScore: 0.8,
		UpdatedAt: nowRFC3339(), VectorSpace: "stub", Layer: 2, HotIndex: true,
	}
	pkt := memoryToSharedPacket(m, "/tmp/repo", true)
	if len(pkt.Vector) == 0 {
		t.Fatal("packet vector")
	}
	out := sharedPacketToMemory(pkt, "circle_x", "dev_peer")
	if out.Origin != "peer" || out.CircleID != "circle_x" {
		t.Fatal("shared packet to memory")
	}
}

func TestInstallBundledTurboVecLib(t *testing.T) {
	home := t.TempDir()
	src := filepath.Join(t.TempDir(), turbovecLibName())
	if err := os.WriteFile(src, []byte("fake-turbovec-lib"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("OVERDRIVE_TURBOVEC_LIB", src)
	if err := os.MkdirAll(libDir(home), 0o700); err != nil {
		t.Fatal(err)
	}
	installBundledTurboVecLib(home)
	dst := filepath.Join(libDir(home), turbovecLibName())
	body, err := os.ReadFile(dst)
	if err != nil || string(body) != "fake-turbovec-lib" {
		t.Fatal("bundled lib not installed")
	}
	installBundledTurboVecLib(home) // cached path should no-op
}

func TestFTSFallbackQuery(t *testing.T) {
	home := t.TempDir()
	setupTestEnv(t, home)
	repo := makeTestRepo(t, filepath.Join(t.TempDir(), "repo"), "https://github.com/acme/a.git")
	e, _ := newEngine(home)
	defer e.Close()
	project, _ := identifyProject(repo)
	_, _ = e.recordMemory(project, "lesson", "repository", "", "Fallback OR token marker alpha.", 0.9, 50, "verified_execution", "", "")
	ids, ranks, err := e.ftsCandidates(`:"fallback"`, project, 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(ids) == 0 {
		t.Fatal("fts fallback expected hits")
	}
	_ = ranks
}

func TestOrganizationAndModuleRecord(t *testing.T) {
	home := t.TempDir()
	setupTestEnv(t, home)
	repo := makeTestRepo(t, filepath.Join(t.TempDir(), "repo"), "https://github.com/acme/org-repo.git")
	e, _ := newEngine(home)
	defer e.Close()
	project, _ := identifyProject(repo)
	_, err := e.recordMemory(project, "rule", "organization", "", "Organization-wide rule marker.", 0.95, 50, "adr", "", "")
	if err != nil {
		t.Fatal(err)
	}
	_, err = e.recordMemory(project, "procedure", "module", "", "Module scoped procedure marker.", 0.9, 50, "verified_execution", "", "")
	if err != nil {
		t.Fatal(err)
	}
	resp, err := e.recall(project, "Organization-wide rule marker", 5, "knowledge")
	if err != nil || len(resp.Memories) == 0 {
		t.Fatalf("org recall memories=%d err=%v", len(resp.Memories), err)
	}
}

func TestTurboVecInvalidSearchAndNormalize(t *testing.T) {
	tv := &TurboVecIndex{available: true, handle: 1, dim: 4}
	ids, _ := tv.Search([]float32{1, 2}, 5, nil)
	if ids != nil {
		t.Fatal("wrong dim")
	}
	ids, _ = tv.Search([]float32{1, 2, 3, 4}, 0, nil)
	if ids != nil {
		t.Fatal("k<=0")
	}
	if normalizeVectorScore(-1) != 0 || normalizeVectorScore(5) == 0 {
		t.Fatal("normalizeVectorScore")
	}
	if turbovecLibName() == "" || ortLibName() == "" {
		t.Fatal("lib names")
	}
}

func TestAddAllowedFolderDuplicate(t *testing.T) {
	home := t.TempDir()
	setupTestEnv(t, home)
	repo := makeTestRepo(t, filepath.Join(t.TempDir(), "repo"), "https://github.com/acme/a.git")
	id, priv, _ := loadOrCreateIdentity(home)
	circle, _ := createCircle(home, "team", id, priv)
	circle, _ = addAllowedFolder(home, circle.ID, repo, id)
	circle2, _ := addAllowedFolder(home, circle.ID, repo, id)
	if len(circle2.AllowedFolders) != len(circle.AllowedFolders) {
		t.Fatal("duplicate folder add")
	}
}

func TestEnsureShareListenerHook(t *testing.T) {
	home := t.TempDir()
	setupTestEnv(t, home)
	port := freeTCPPort(t)
	t.Setenv("OVERDRIVE_SHARE_LISTEN", fmt.Sprintf("127.0.0.1:%d", port))
	shareListenerOnce = sync.Once{}
	shareListenerInst = nil
	ensureShareListener(home)
	if shareListenerInst == nil {
		t.Fatal("listener not started")
	}
	shareListenerInst.Close()
}

func TestExtractORTZipMissingDLL(t *testing.T) {
	archive := filepath.Join(t.TempDir(), "empty.zip")
	buf := new(bytes.Buffer)
	zw := zip.NewWriter(buf)
	w, _ := zw.Create("readme.txt")
	_, _ = w.Write([]byte("no dll"))
	_ = zw.Close()
	_ = os.WriteFile(archive, buf.Bytes(), 0o644)
	if err := extractORTZip(archive, t.TempDir()); err == nil {
		t.Fatal("missing dll")
	}
}

func TestShareStatusModes(t *testing.T) {
	home := t.TempDir()
	setupTestEnv(t, home)
	repo := makeTestRepo(t, filepath.Join(t.TempDir(), "repo"), "https://github.com/acme/a.git")
	id, priv, _ := loadOrCreateIdentity(home)
	circle, _ := createCircle(home, "team", id, priv)
	circle, _ = addAllowedFolder(home, circle.ID, repo, id)
	e, _ := newEngine(home)
	defer e.Close()
	pub, _, _ := ed25519.GenerateKey(rand.Reader)
	idB := DeviceIdentity{DeviceID: "dev_" + sha256Hex(pub)[:16], PublicKey: base64.StdEncoding.EncodeToString(pub)}
	circle.Members = []CircleMember{{DeviceID: idB.DeviceID, PublicKey: idB.PublicKey, Revoked: false}}
	_ = saveCircle(home, circle)
	status, err := e.shareStatus(identifyProjectMust(t, repo))
	if err != nil || status.Mode != "not-member" {
		t.Fatalf("not-member mode=%s", status.Mode)
	}
	circle.Members = append(circle.Members, CircleMember{DeviceID: id.DeviceID, PublicKey: id.PublicKey, Revoked: false})
	_ = saveCircle(home, circle)
	e.recordShareSyncReport(circle.ID, identifyProjectMust(t, repo).Repository, shareSyncReport{Imported: 2, PeersContacted: 1, PeersOK: 1})
	status, _ = e.shareStatus(identifyProjectMust(t, repo))
	if status.Mode != "synced" || status.LastImported != 2 {
		t.Fatalf("synced mode=%s imported=%d", status.Mode, status.LastImported)
	}
}

func identifyProjectMust(t *testing.T, path string) Project {
	p, err := identifyProject(path)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func TestIdentifyProjectSubmodule(t *testing.T) {
	repo := makeTestRepo(t, filepath.Join(t.TempDir(), "repo"), "https://github.com/acme/submod.git")
	sub := filepath.Join(repo, "pkg", "service")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	p, err := identifyProject(sub)
	if err != nil || p.Module != "pkg/service" {
		t.Fatalf("module=%q err=%v", p.Module, err)
	}
}

func TestPeerEligibleRevokedMember(t *testing.T) {
	home := t.TempDir()
	setupTestEnv(t, home)
	repo := makeTestRepo(t, filepath.Join(t.TempDir(), "repo"), "https://github.com/acme/r.git")
	id, priv, _ := loadOrCreateIdentity(home)
	pub, _, _ := ed25519.GenerateKey(rand.Reader)
	idB := DeviceIdentity{DeviceID: "dev_" + sha256Hex(pub)[:16], PublicKey: base64.StdEncoding.EncodeToString(pub)}
	circle, _ := createCircle(home, "team", id, priv)
	circle.Members = append(circle.Members, CircleMember{DeviceID: idB.DeviceID, PublicKey: idB.PublicKey, Revoked: true})
	circle, _ = addAllowedFolder(home, circle.ID, repo, id)
	e, _ := newEngine(home)
	defer e.Close()
	project, _ := identifyProject(repo)
	pm := Memory{
		ID: "mem_rev_peer", Kind: "lesson", Scope: "repository", ScopeID: project.Repository,
		Status: "active", Origin: "peer", HotIndex: true, CircleID: circle.ID, PeerDeviceID: idB.DeviceID,
	}
	if e.peerEligible(pm, project) {
		t.Fatal("revoked peer should be ineligible")
	}
}

func TestAddAllowedFolderNotMember(t *testing.T) {
	home := t.TempDir()
	setupTestEnv(t, home)
	repo := makeTestRepo(t, filepath.Join(t.TempDir(), "repo"), "https://github.com/acme/a.git")
	id, priv, _ := loadOrCreateIdentity(home)
	pub, privB, _ := ed25519.GenerateKey(rand.Reader)
	idB := DeviceIdentity{DeviceID: "dev_" + sha256Hex(pub)[:16], PublicKey: base64.StdEncoding.EncodeToString(pub)}
	circle, _ := createCircle(home, "team", id, priv)
	if _, err := addAllowedFolder(home, circle.ID, repo, idB); err == nil {
		t.Fatal("non-member folder add")
	}
	_ = privB
}

func TestFakeORTSessionErrors(t *testing.T) {
	if _, err := openORTSessionFFI("", "/tmp/model.onnx"); err == nil {
		t.Fatal("empty lib path")
	}
	sess, err := openORTSessionFFI(filepath.Join(t.TempDir(), "lib.so"), "/tmp/model.onnx")
	if err != nil {
		t.Skip("fake ort not enabled")
	}
	if _, err := sess.Run(nil, nil, nil); err == nil {
		t.Fatal("invalid tensors")
	}
	if closer, ok := sess.(interface{ Close() }); ok {
		closer.Close()
	}
}

func TestScoringRecallPaths(t *testing.T) {
	m := Memory{Subject: "alpha", Content: "beta gamma", Confidence: 0.8, Priority: 60, Source: "verified_execution", UpdatedAt: nowRFC3339(), EvidenceScore: 0.7}
	if queryRelevance(m, "alpha beta") <= 0 {
		t.Fatal("queryRelevance")
	}
	if recallScore(m, "alpha", 0.9, 0) <= 0 || recallScore(m, "", 0.9, 0) <= 0 {
		t.Fatal("recallScore no vector")
	}
	if recallScore(m, "alpha", 0.9, 0.8) <= recallScore(m, "alpha", 0.9, 0) {
		t.Fatal("recallScore vector boost")
	}
	pm := Memory{PacketContent: "peer packet", Content: "peer body", Source: "peer_share", EvidenceScore: 0.6}
	if peerRecallScore(pm, "peer packet", 0.7) <= 0 {
		t.Fatal("peerRecallScore")
	}
	p := Project{Repository: "github.com/acme/r", Organization: "github.com/acme", Module: "pkg/sub"}
	modMem := Memory{Scope: "module", ScopeID: "github.com/acme/r#pkg"}
	if scopeWeight(modMem, p) <= 0 {
		t.Fatal("module prefix scope weight")
	}
}

func TestNewEngineInvalidHomePath(t *testing.T) {
	dir := t.TempDir()
	blocker := filepath.Join(dir, "blocker")
	if err := os.WriteFile(blocker, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	home := filepath.Join(blocker, "nested")
	if _, err := newEngine(home); err == nil {
		t.Fatal("expected mkdir failure")
	}
}

func TestValidateContradictionWithReplaces(t *testing.T) {
	home := t.TempDir()
	setupTestEnv(t, home)
	repo := makeTestRepo(t, filepath.Join(t.TempDir(), "repo"), "https://github.com/acme/a.git")
	stdout, _, code := runCLICapture(t, []string{
		"record", "--cwd", repo, "--kind", "fact",
		"--content", "Contradiction target fact marker.",
		"--confidence", "0.8", "--source", "verified_execution",
	})
	if code != 0 {
		t.Fatal(code)
	}
	id := mustJSON(t, stdout)["id"].(string)
	_, _, code = runCLICapture(t, []string{
		"validate", "--id", id, "--result", "contradiction",
		"--winner-note", "superseded", "--replaces-id", "mem_successor",
	})
	if code != 0 {
		t.Fatal(code)
	}
}

func TestCLILedgerListRequiresRun(t *testing.T) {
	home := t.TempDir()
	setupTestEnv(t, home)
	repo := makeTestRepo(t, filepath.Join(t.TempDir(), "repo"), "https://github.com/acme/a.git")
	_, stderr, code := runCLICapture(t, []string{"ledger-list", "--cwd", repo})
	if code != 2 || !strings.Contains(stderr, "--run is required") {
		t.Fatalf("ledger-list: %d %q", code, stderr)
	}
}

func TestFullP2PFlowWithTurboVec(t *testing.T) {
	lib := filepath.Join("..", "turbovec-ffi", "target", "release", "liboverdrive_turbovec_ffi.so")
	if _, err := os.Stat(lib); err != nil {
		t.Skip("turbovec not built")
	}
	repo := makeTestRepo(t, filepath.Join(t.TempDir(), "repo"), "https://github.com/acme/payments-api.git")
	homeA, homeB, _, stop := setupCirclePair(t, repo)
	defer stop()
	setupTestEnv(t, homeA)
	t.Setenv("OVERDRIVE_HOME", homeA)
	abs, _ := filepath.Abs(lib)
	t.Setenv("OVERDRIVE_TURBOVEC_LIB", abs)
	_, _, code := runCLICapture(t, []string{
		"record", "--cwd", repo, "--kind", "lesson",
		"--subject", "ECONNREFUSED proposal.service.ts",
		"--content", "Use ProposalRepository for proposal persistence.",
		"--confidence", "0.95", "--source", "verified_execution",
		"--evidence", "proposal.service.ts integration tests failed with ECONNREFUSED",
	})
	if code != 0 {
		t.Fatal(code)
	}
	setupTestEnv(t, homeB)
	t.Setenv("OVERDRIVE_HOME", homeB)
	t.Setenv("OVERDRIVE_TURBOVEC_LIB", abs)
	_, _, code = runCLICapture(t, []string{"session-start", "--cwd", repo})
	if code != 0 {
		t.Fatal(code)
	}
	stdout, _, code := runCLICapture(t, []string{
		"recall", "--cwd", repo,
		"--query", "ECONNREFUSED proposal.service.ts ProposalRepository",
		"--limit", "3",
	})
	if code != 0 {
		t.Fatal(code)
	}
	data := mustJSON(t, stdout)
	peer, _ := data["peer_memories"].([]any)
	if len(peer) == 0 {
		t.Fatal("expected peer memories with turbovec")
	}
	_, _, code = runCLICapture(t, []string{"session-end", "--cwd", repo, "--quiet"})
	if code != 0 {
		t.Fatal(code)
	}
}

func TestShareCircleIOHelpers(t *testing.T) {
	home := t.TempDir()
	if err := os.MkdirAll(circlesDir(home), 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := loadCircle(home, "missing"); err == nil {
		t.Fatal("missing circle")
	}
	if err := os.WriteFile(circlePath(home, "bad"), []byte("{"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := loadCircle(home, "bad"); err == nil {
		t.Fatal("bad circle json")
	}
	_ = os.WriteFile(circlePath(home, "skip"), []byte("{}"), 0o600)
	circles, err := listCircles(home)
	if err != nil || len(circles) != 1 {
		t.Fatalf("list circles len=%d err=%v", len(circles), err)
	}
	if _, ok := circleForProject(home, t.TempDir()); ok {
		t.Fatal("unexpected circle for project")
	}
}

func TestEnsureORTLibMissingEnvPath(t *testing.T) {
	home := t.TempDir()
	resetORTLibForTest()
	t.Setenv("OVERDRIVE_ORT_LIB", filepath.Join(home, "missing.so"))
	if _, err := ensureORTLib(home); err == nil {
		t.Fatal("missing ort env lib")
	}
}

func TestHandleConnBadCircleAndCipher(t *testing.T) {
	home := t.TempDir()
	setupTestEnv(t, home)
	sl, endpoint := startShareListenerForTest(t, home)
	defer sl.Close()
	conn, err := net.Dial("tcp", endpoint)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	_, _ = conn.Write([]byte(`{"circle_id":"missing","ciphertext":"bad"}` + "\n"))
	time.Sleep(50 * time.Millisecond)
}

func TestWorkingMemoryOnRecall(t *testing.T) {
	home := t.TempDir()
	setupTestEnv(t, home)
	repo := makeTestRepo(t, filepath.Join(t.TempDir(), "repo"), "https://github.com/acme/a.git")
	e, _ := newEngine(home)
	defer e.Close()
	project, _ := identifyProject(repo)
	_, _ = e.startSession(project)
	_, _ = e.recordMemory(project, "lesson", "repository", "", "Working memory seed lesson.", 0.9, 50, "verified_execution", "", "")
	resp, err := e.recall(project, "working memory seed", 5, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(resp.WorkingMemory) == 0 {
		t.Fatal("working memory seeded")
	}
}
