package main

import (
	"bufio"
	"crypto/ed25519"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestOpenEngineWithoutMigrateClosedDB(t *testing.T) {
	resetHooksForTest()
	defer resetHooksForTest()
	hookDBOpen = func(driver, dsn string) (*sql.DB, error) {
		db, err := sql.Open(driver, dsn)
		if err != nil {
			return nil, err
		}
		_ = db.Close()
		return db, nil
	}
	if _, err := openEngineWithoutMigrate(t.TempDir()); err == nil {
		t.Fatal("initSchema should fail on closed db")
	}
}

func TestNewEngineMigrateJSONFailure(t *testing.T) {
	home := t.TempDir()
	setupTestEnv(t, home)
	if err := os.WriteFile(filepath.Join(home, "experience-v1.json"), []byte("{bad"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := newEngine(home); err == nil {
		t.Fatal("migrate json fail")
	}
}

func TestNewEngineLibDirMkdirFailure(t *testing.T) {
	resetHooksForTest()
	defer resetHooksForTest()
	home := t.TempDir()
	hookMkdirAll = func(path string, mode os.FileMode) error {
		if strings.HasSuffix(path, "lib") {
			return errors.New("lib mkdir fail")
		}
		return os.MkdirAll(path, mode)
	}
	if _, err := newEngine(home); err == nil {
		t.Fatal("lib mkdir fail")
	}
}

func TestOpenEngineHomeFailure(t *testing.T) {
	resetHooksForTest()
	defer resetHooksForTest()
	t.Setenv("OVERDRIVE_HOME", "")
	hookUserHomeDir = func() (string, error) {
		return "", errors.New("no home")
	}
	if _, err := openEngine(); err == nil {
		t.Fatal("openEngine home fail")
	}
	_, _, code := runCLICapture(t, []string{"status"})
	if code == 0 {
		t.Fatal("status should fail without home")
	}
}

func TestLedgerAddWriteFailures(t *testing.T) {
	home := t.TempDir()
	setupTestEnv(t, home)
	repo := makeTestRepo(t, filepath.Join(t.TempDir(), "repo"), "https://github.com/acme/a.git")
	e, err := newEngine(home)
	if err != nil {
		t.Fatal(err)
	}
	defer e.Close()
	project, _ := identifyProject(repo)

	resetHooksForTest()
	defer resetHooksForTest()
	hookMkdirAll = func(path string, mode os.FileMode) error {
		if strings.Contains(path, "runs") {
			return errors.New("runs mkdir fail")
		}
		return os.MkdirAll(path, mode)
	}
	if _, err := e.ledgerAdd(project, "run-x", "decision", "", "", "", ""); err == nil {
		t.Fatal("ledger mkdir fail")
	}
	resetHooksForTest()

	hookWriteFile = func(string, []byte, os.FileMode) error {
		return errors.New("write ledger fail")
	}
	entry, err := e.ledgerAdd(project, "run-y", "decision", "ev", "", "", "")
	if err == nil {
		t.Fatal("ledger write fail")
	}
	if entry.Decision != "decision" {
		t.Fatalf("entry=%q", entry.Decision)
	}
}

func TestEnsureMiniLMPackHookFailures(t *testing.T) {
	home := t.TempDir()
	t.Setenv("OVERDRIVE_SKIP_EMBED_DOWNLOAD", "0")
	resetHooksForTest()
	defer resetHooksForTest()

	calls := 0
	hookEnsureFile = func(url, dest string) error {
		calls++
		if strings.Contains(dest, "model.onnx") {
			return errors.New("model fail")
		}
		return ensureFileImpl(url, dest)
	}
	if err := ensureMiniLMPack(home); err == nil || calls == 0 {
		t.Fatal("model ensure fail")
	}

	resetHooksForTest()
	hookEnsureFile = func(url, dest string) error {
		if strings.Contains(dest, "vocab.txt") {
			return errors.New("vocab fail")
		}
		return ensureFileImpl(url, dest)
	}
	if err := ensureMiniLMPack(home); err == nil {
		t.Fatal("vocab ensure fail")
	}
}

func TestTryMiniLMFailureBranches(t *testing.T) {
	home := t.TempDir()
	resetORTLibForTest()
	t.Setenv("OVERDRIVE_ORT_LIB", filepath.Join(home, "missing.so"))
	if emb, ok := tryMiniLM(home); ok || emb != nil {
		t.Fatal("missing ort lib")
	}

	lib := filepath.Join(home, "libonnxruntime.so")
	if err := os.WriteFile(lib, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	resetORTLibForTest()
	t.Setenv("OVERDRIVE_ORT_LIB", lib)
	t.Setenv("OVERDRIVE_SKIP_EMBED_DOWNLOAD", "1")
	if emb, ok := tryMiniLM(home); ok || emb != nil {
		t.Fatal("missing model pack")
	}
}

func TestTokenizerCoveragePush(t *testing.T) {
	if sqrt64(0) != 0 || sqrt64(-1) != 0 {
		t.Fatal("sqrt64 non-positive")
	}
	dir := t.TempDir()
	emptyVocab := filepath.Join(dir, "empty.txt")
	if err := os.WriteFile(emptyVocab, []byte("\n\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := loadWordPieceTokenizer(emptyVocab); err == nil {
		t.Fatal("empty vocab")
	}
	vocab := filepath.Join(dir, "vocab.txt")
	if err := os.WriteFile(vocab, []byte("[CLS]\n[SEP]\nhel\n##lo\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	tok, err := loadWordPieceTokenizer(vocab)
	if err != nil {
		t.Fatal(err)
	}
	if tok.basicTokenize("") != nil {
		t.Fatal("empty basicTokenize")
	}
	ids, mask, types := tok.Encode("hello world this is a long sentence for truncation", 6)
	if len(ids) != 6 || ids[5] != tok.sep || mask[5] != 1 {
		t.Fatalf("truncate ids=%v mask=%v", ids, mask)
	}
	if len(types) != 6 {
		t.Fatal("token types len")
	}
}

func TestReleaseORTStatusNoAPI(t *testing.T) {
	called := false
	old := ortReleaseStatusFn
	ortReleaseStatusFn = func(api, status uintptr) { called = true }
	defer func() { ortReleaseStatusFn = old }()
	releaseORTStatus(0, 1)
	if called {
		t.Fatal("zero api should not release")
	}
}

func TestCLIValidationAndShareErrors(t *testing.T) {
	home := t.TempDir()
	setupTestEnv(t, home)
	repo := makeTestRepo(t, filepath.Join(t.TempDir(), "repo"), "https://github.com/acme/a.git")

	failCases := [][]string{
		{"record", "--cwd", repo},
		{"validate"},
		{"ledger-add", "--cwd", repo},
		{"ledger-list", "--cwd", repo},
		{"share"},
		{"share", "nope"},
		{"share", "circle"},
		{"share", "circle", "create"},
		{"share", "circle", "invite"},
		{"share", "circle", "accept"},
		{"share", "circle", "revoke"},
		{"share", "circle", "folder-add"},
		{"share", "circle", "folder-list"},
		{"share", "listen", "--cwd", repo},
	}
	for _, args := range failCases {
		_, _, code := runCLICapture(t, args)
		if code == 0 {
			t.Fatalf("expected failure for %v", args)
		}
	}
}

func TestCmdShareStatusTextFormat(t *testing.T) {
	home := t.TempDir()
	setupTestEnv(t, home)
	repo := makeTestRepo(t, filepath.Join(t.TempDir(), "repo"), "https://github.com/acme/a.git")
	stdout, _, code := runCLICapture(t, []string{"share", "status", "--cwd", repo, "--format", "text"})
	if code != 0 {
		t.Fatal(code)
	}
	if !strings.Contains(stdout, "share") {
		t.Fatalf("text status %q", stdout)
	}
}

func TestBuildSyncResponseSinceAndFolderFilter(t *testing.T) {
	home := t.TempDir()
	setupTestEnv(t, home)
	repo := makeTestRepo(t, filepath.Join(t.TempDir(), "repo"), "https://github.com/acme/a.git")
	other := makeTestRepo(t, filepath.Join(t.TempDir(), "other"), "https://github.com/acme/b.git")
	id, priv, _ := loadOrCreateIdentity(home)
	circle, _ := createCircle(home, "team", id, priv)
	circle, _ = addAllowedFolder(home, circle.ID, repo, id)
	e, _ := newEngine(home)
	defer e.Close()
	project, _ := identifyProject(repo)
	m, _ := e.recordMemory(project, "lesson", "repository", "", "Exportable lesson body.", 0.9, 50, "verified_execution", "", "")
	m.SourceFolder = other
	m.HotIndex = true
	m.Layer = 2
	_ = e.upsertMemory(m)

	req := SyncRequest{DeviceID: id.DeviceID, CircleID: circle.ID, Repository: project.Repository, Since: nowRFC3339(), Timestamp: nowRFC3339()}
	req, _ = signSyncRequest(priv, req)
	resp, err := e.buildSyncResponse(home, circle, id, priv, req)
	if err != nil {
		t.Fatal(err)
	}
	if len(resp.Packets) != 0 {
		t.Fatalf("since filter should skip packets: %d", len(resp.Packets))
	}

	req.Since = ""
	resp, err = e.buildSyncResponse(home, circle, id, priv, req)
	if err != nil {
		t.Fatal(err)
	}
	if len(resp.Packets) != 0 {
		t.Fatalf("folder filter should skip other root: %d", len(resp.Packets))
	}
}

func TestFetchPeerSyncInvalidSignature(t *testing.T) {
	home := t.TempDir()
	setupTestEnv(t, home)
	id, priv, _ := loadOrCreateIdentity(home)
	circle, _ := createCircle(home, "team", id, priv)
	circle.Members = append(circle.Members, CircleMember{DeviceID: id.DeviceID, PublicKey: id.PublicKey})
	bad := SyncResponse{
		DeviceID: id.DeviceID, CircleID: circle.ID, Repository: "github.com/acme/r",
		Cursor: nowRFC3339(), Signature: "not-a-valid-signature",
	}
	endpoint, stop := startMockPeerSyncServerRaw(t, circle, bad)
	defer stop()
	if _, err := fetchPeerSync(home, endpoint, circle, id, priv, "github.com/acme/r", ""); err == nil || !strings.Contains(err.Error(), "invalid peer signature") {
		t.Fatalf("invalid signature err=%v", err)
	}
}

func startMockPeerSyncServerRaw(t *testing.T, circle Circle, resp SyncResponse) (endpoint string, stop func()) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		reader := bufio.NewReader(conn)
		if _, err := reader.ReadString('\n'); err != nil {
			return
		}
		payload, err := json.Marshal(resp)
		if err != nil {
			return
		}
		cipher, err := encryptBatch(circle.GroupKey, payload)
		if err != nil {
			return
		}
		out, err := json.Marshal(wireEnvelope{CircleID: circle.ID, Ciphertext: cipher})
		if err != nil {
			return
		}
		_, _ = conn.Write(append(out, '\n'))
	}()
	return ln.Addr().String(), func() {
		_ = ln.Close()
		<-done
	}
}

func TestEnsureShareListenerNoEnv(t *testing.T) {
	home := t.TempDir()
	setupTestEnv(t, home)
	shareListenerOnce = sync.Once{}
	shareListenerInst = nil
	t.Setenv("OVERDRIVE_SHARE_LISTEN", "")
	ensureShareListener(home)
	if shareListenerInst != nil {
		t.Fatal("listener without env")
	}
}

func TestCmdShareListenWaits(t *testing.T) {
	home := t.TempDir()
	setupTestEnv(t, home)
	port := freeTCPPort(t)
	t.Setenv("OVERDRIVE_SHARE_LISTEN", fmt.Sprintf("127.0.0.1:%d", port))
	oldWait := shareListenWait
	shareListenWait = func(sl *shareListener) {
		sl.Close()
	}
	defer func() { shareListenWait = oldWait }()
	if err := cmdShareListen([]string{"--cwd", home}); err != nil {
		t.Fatal(err)
	}
}

func TestWithEnginePropagatesError(t *testing.T) {
	resetHooksForTest()
	defer resetHooksForTest()
	t.Setenv("OVERDRIVE_HOME", t.TempDir())
	t.Setenv("OVERDRIVE_EMBEDDER", "stub")
	t.Setenv("OVERDRIVE_SKIP_EMBED_DOWNLOAD", "1")
	err := withEngine(func(e *Engine) error {
		return errors.New("inner fail")
	})
	if err == nil || err.Error() != "inner fail" {
		t.Fatalf("withEngine err=%v", err)
	}
}

func TestMemoryToSharedPacketAndValidateEdge(t *testing.T) {
	m := Memory{ID: "m1", Kind: "lesson", ScopeID: "github.com/acme/r", PacketContent: "pkt", Content: "body", HotIndex: true}
	pkt := memoryToSharedPacket(m, "/allowed", true)
	if pkt.ID != "m1" || !pkt.HotIndex {
		t.Fatalf("packet %+v", pkt)
	}
	home := t.TempDir()
	setupTestEnv(t, home)
	e, _ := newEngine(home)
	defer e.Close()
	if _, err := e.validateMemory("missing-id", "success", "", ""); err == nil {
		t.Fatal("missing memory")
	}
}

func TestRunCLINoArgsAndUnknown(t *testing.T) {
	_, _, code := runCLICapture(t, nil)
	if code != 2 {
		t.Fatal(code)
	}
	_, _, code = runCLICapture(t, []string{"nope"})
	if code != 2 {
		t.Fatal(code)
	}
}

func TestFetchPeerSyncJSONMarshalFail(t *testing.T) {
	home := t.TempDir()
	setupTestEnv(t, home)
	id, priv, _ := loadOrCreateIdentity(home)
	circle, _ := createCircle(home, "team", id, priv)
	hookJSONMarshal = func(v any) ([]byte, error) {
		return nil, errors.New("marshal fail")
	}
	defer resetHooksForTest()
	if _, err := fetchPeerSync(home, "127.0.0.1:1", circle, id, priv, "github.com/acme/r", ""); err == nil {
		t.Fatal("marshal fail")
	}
}

func TestHandleConnInvalidJSON(t *testing.T) {
	home := t.TempDir()
	setupTestEnv(t, home)
	port := freeTCPPort(t)
	t.Setenv("OVERDRIVE_SHARE_LISTEN", fmt.Sprintf("127.0.0.1:%d", port))
	id, priv, _ := loadOrCreateIdentity(home)
	sl, err := startShareListener(home, id, priv)
	if err != nil {
		t.Fatal(err)
	}
	defer sl.Close()
	conn, err := net.Dial("tcp", fmt.Sprintf("127.0.0.1:%d", port))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	_, _ = conn.Write([]byte("not-json\n"))
	time.Sleep(100 * time.Millisecond)
}

func TestLoadOrCreateIdentityErrorPaths(t *testing.T) {
	home := t.TempDir()
	resetHooksForTest()
	defer resetHooksForTest()
	hookMkdirAll = func(string, os.FileMode) error {
		return errors.New("share mkdir fail")
	}
	if _, _, err := loadOrCreateIdentity(home); err == nil {
		t.Fatal("mkdir fail")
	}
	resetHooksForTest()

	if err := hookMkdirAll(shareDir(home), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := hookWriteFile(identityPath(home), []byte("{bad json"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := loadOrCreateIdentity(home); err == nil {
		t.Fatal("parse fail")
	}

	badKey := DeviceIdentity{DeviceID: "dev_x", PublicKey: "cGk=", PrivateKey: "cGk=", CreatedAt: nowRFC3339()}
	data, _ := json.Marshal(badKey)
	if err := hookWriteFile(identityPath(home), data, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := loadOrCreateIdentity(home); err == nil {
		t.Fatal("invalid key size")
	}
	_ = os.Remove(identityPath(home))

	hookJSONMarshalIndent = func(any, string, string) ([]byte, error) {
		return nil, errors.New("marshal indent fail")
	}
	if _, _, err := loadOrCreateIdentity(home); err == nil {
		t.Fatal("marshal indent fail")
	}
	resetHooksForTest()

	hookWriteFile = func(string, []byte, os.FileMode) error {
		return errors.New("write identity fail")
	}
	if _, _, err := loadOrCreateIdentity(home); err == nil {
		t.Fatal("write fail")
	}
}

func TestCreateCircleRandFailure(t *testing.T) {
	home := t.TempDir()
	setupTestEnv(t, home)
	id, priv, _ := loadOrCreateIdentity(home)
	resetHooksForTest()
	defer resetHooksForTest()
	hookRandRead = func([]byte) (int, error) {
		return 0, errors.New("rand fail")
	}
	if _, err := createCircle(home, "team", id, priv); err == nil {
		t.Fatal("rand fail")
	}
}

func TestCurrentEmbedderHomeFailure(t *testing.T) {
	resetRuntimeGlobals()
	resetHooksForTest()
	defer resetHooksForTest()
	t.Setenv("OVERDRIVE_HOME", "")
	hookUserHomeDir = func() (string, error) {
		return "", errors.New("no home")
	}
	emb, name := currentEmbedder()
	if name != "hashed" || emb == nil {
		t.Fatalf("embedder=%s", name)
	}
}

func TestOpenTurboVecAtInvalidPath(t *testing.T) {
	home := t.TempDir()
	setupTestEnv(t, home)
	idx := openTurboVecAt("bad\x00path", home, hashedDims)
	if idx == nil || idx.Available() {
		t.Fatal("invalid path should be unavailable")
	}
}

func TestSyncCirclePeersWithMockEndpoint(t *testing.T) {
	home := t.TempDir()
	setupTestEnv(t, home)
	repo := makeTestRepo(t, filepath.Join(t.TempDir(), "repo"), "https://github.com/acme/a.git")
	id, priv, _ := loadOrCreateIdentity(home)
	circle, _ := createCircle(home, "team", id, priv)
	circle, _ = addAllowedFolder(home, circle.ID, repo, id)
	resp := SyncResponse{
		DeviceID: id.DeviceID, CircleID: circle.ID, Repository: identifyProjectMust(t, repo).Repository,
		Cursor: nowRFC3339(),
		Packets: []SharedPacket{{
			ID: "peer_pkt_1", Kind: "lesson", ScopeID: identifyProjectMust(t, repo).Repository,
			PacketContent: "shared lesson", LessonContent: "body", HotIndex: true, VectorSpace: "stub",
			Fingerprint: "peer_pkt_1", EvidenceScore: 0.8, UpdatedAt: nowRFC3339(),
		}},
	}
	endpoint, stop := startMockPeerSyncServer(t, circle, priv, resp)
	defer stop()
	circle.PeerEndpoints = []string{endpoint, ""}
	_ = saveCircle(home, circle)
	port := freeTCPPort(t)
	t.Setenv("OVERDRIVE_SHARE_LISTEN", fmt.Sprintf("127.0.0.1:%d", port))
	e, _ := newEngine(home)
	defer e.Close()
	project := identifyProjectMust(t, repo)
	if err := e.syncCirclePeers(project); err != nil {
		t.Fatal(err)
	}
}

func TestFetchPeerSyncDialFailure(t *testing.T) {
	home := t.TempDir()
	setupTestEnv(t, home)
	id, priv, _ := loadOrCreateIdentity(home)
	circle, _ := createCircle(home, "team", id, priv)
	circle.Members = append(circle.Members, CircleMember{DeviceID: id.DeviceID, PublicKey: id.PublicKey})
	if _, err := fetchPeerSync(home, "127.0.0.1:1", circle, id, priv, "github.com/acme/r", ""); err == nil {
		t.Fatal("dial fail")
	}
}

func TestCLICommandFlagParseErrors(t *testing.T) {
	home := t.TempDir()
	setupTestEnv(t, home)
	repo := makeTestRepo(t, filepath.Join(t.TempDir(), "repo"), "https://github.com/acme/a.git")
	cmds := []func([]string) error{
		func(a []string) error { return cmdStatus(a) },
		func(a []string) error { return cmdProject(a) },
		func(a []string) error { return cmdSessionStart(a) },
		func(a []string) error { return cmdSessionEnd(a) },
		func(a []string) error { return cmdRecord(a) },
		func(a []string) error { return cmdRecall(a) },
		func(a []string) error { return cmdValidate(a) },
		func(a []string) error { return cmdLedgerAdd(a) },
		func(a []string) error { return cmdLedgerList(a) },
		func(a []string) error { return cmdShareIdentity(a) },
		func(a []string) error { return cmdCircleList(a) },
		func(a []string) error { return cmdCircleCreate(a) },
		func(a []string) error { return cmdCircleInvite(a) },
		func(a []string) error { return cmdCircleAccept(a) },
		func(a []string) error { return cmdCircleRevoke(a) },
		func(a []string) error { return cmdCircleFolderAdd(a) },
		func(a []string) error { return cmdCircleFolderList(a) },
		func(a []string) error { return cmdShareStatus(a) },
		func(a []string) error { return cmdShareListen(a) },
	}
	for _, cmd := range cmds {
		if err := cmd([]string{"-not-a-real-flag"}); err == nil {
			t.Fatalf("expected flag error for %T", cmd)
		}
	}
	_, _, code := runCLICapture(t, []string{"status"})
	if code != 0 {
		t.Fatal(code)
	}
	_, _, code = runCLICapture(t, []string{"project", "--cwd", repo})
	if code != 0 {
		t.Fatal(code)
	}
	_, _, code = runCLICapture(t, []string{"share", "identity"})
	if code != 0 {
		t.Fatal(code)
	}
	_, _, code = runCLICapture(t, []string{"share", "circle", "list"})
	if code != 0 {
		t.Fatal(code)
	}
}

func TestTryHandleJoinMalformedPlaintext(t *testing.T) {
	home := t.TempDir()
	setupTestEnv(t, home)
	sl := &shareListener{home: home}
	if !sl.tryHandleJoin(wireEnvelope{Kind: "join"}, []byte("not-json")) {
		t.Fatal("malformed join should be handled")
	}
	if sl.tryHandleJoin(wireEnvelope{Kind: "sync"}, []byte("{}")) {
		t.Fatal("non-join kind")
	}
}

func TestCmdStatusOpenEngineFailure(t *testing.T) {
	resetHooksForTest()
	defer resetHooksForTest()
	t.Setenv("OVERDRIVE_HOME", t.TempDir())
	t.Setenv("OVERDRIVE_EMBEDDER", "stub")
	t.Setenv("OVERDRIVE_SKIP_EMBED_DOWNLOAD", "1")
	hookDBOpen = func(driver, dsn string) (*sql.DB, error) {
		db, err := sql.Open(driver, dsn)
		if err != nil {
			return nil, err
		}
		_ = db.Close()
		return db, nil
	}
	if err := cmdStatus(nil); err == nil {
		t.Fatal("status with broken db")
	}
}

func TestHandleConnValidSyncRoundTrip(t *testing.T) {
	home := t.TempDir()
	setupTestEnv(t, home)
	repo := makeTestRepo(t, filepath.Join(t.TempDir(), "repo"), "https://github.com/acme/a.git")
	id, priv, _ := loadOrCreateIdentity(home)
	circle, _ := createCircle(home, "team", id, priv)
	circle, _ = addAllowedFolder(home, circle.ID, repo, id)
	port := freeTCPPort(t)
	t.Setenv("OVERDRIVE_SHARE_LISTEN", fmt.Sprintf("127.0.0.1:%d", port))
	sl, err := startShareListener(home, id, priv)
	if err != nil {
		t.Fatal(err)
	}
	defer sl.Close()

	e, _ := newEngine(home)
	defer e.Close()
	project := identifyProjectMust(t, repo)
	m, _ := e.recordMemory(project, "lesson", "repository", "", "Listener export lesson body.", 0.9, 50, "verified_execution", "", "")
	m.HotIndex = true
	m.Layer = 2
	m.SourceFolder = repo
	if err := e.upsertMemory(m); err != nil {
		t.Fatal(err)
	}

	resp, err := fetchPeerSync(home, fmt.Sprintf("127.0.0.1:%d", port), circle, id, priv, project.Repository, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(resp.Packets) == 0 {
		t.Fatal("expected export packets from handleConn")
	}
}

func TestCreateCircleSignMemberListFailure(t *testing.T) {
	home := t.TempDir()
	setupTestEnv(t, home)
	id, priv, _ := loadOrCreateIdentity(home)
	resetHooksForTest()
	defer resetHooksForTest()
	hookJSONMarshal = func(v any) ([]byte, error) {
		if _, ok := v.([]CircleMember); ok {
			return nil, errors.New("members marshal fail")
		}
		return json.Marshal(v)
	}
	if _, err := createCircle(home, "team", id, priv); err == nil {
		t.Fatal("sign member list fail")
	}
}

func TestTryHandleJoinExpiredInvite(t *testing.T) {
	home := t.TempDir()
	setupTestEnv(t, home)
	id, priv, _ := loadOrCreateIdentity(home)
	circle, _ := createCircle(home, "team", id, priv)
	invite, _ := createInvite(home, circle.ID, id)
	invite.ExpiresAt = "2000-01-01T00:00:00Z"
	if err := hookMkdirAll(invitesDir(home), 0o700); err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(invite)
	if err := hookWriteFile(filepath.Join(invitesDir(home), invite.Code+".json"), raw, 0o600); err != nil {
		t.Fatal(err)
	}
	pub, privB, _ := ed25519.GenerateKey(nil)
	idB := DeviceIdentity{DeviceID: "dev_" + sha256Hex(pub)[:16], PublicKey: base64.StdEncoding.EncodeToString(pub)}
	req := JoinRequest{
		CircleID: circle.ID, DeviceID: idB.DeviceID, PublicKey: idB.PublicKey,
		Code: invite.Code, Timestamp: nowRFC3339(),
	}
	req, _ = signJoinRequest(privB, req)
	payload, _ := json.Marshal(req)
	sl := &shareListener{home: home}
	if !sl.tryHandleJoin(wireEnvelope{Kind: "join"}, payload) {
		t.Fatal("expired invite handled")
	}
}

func TestSaveCircleWriteFailure(t *testing.T) {
	home := t.TempDir()
	setupTestEnv(t, home)
	id, priv, _ := loadOrCreateIdentity(home)
	circle, _ := createCircle(home, "team", id, priv)
	resetHooksForTest()
	defer resetHooksForTest()
	hookWriteFile = func(string, []byte, os.FileMode) error {
		return errors.New("circle write fail")
	}
	if err := saveCircle(home, circle); err == nil {
		t.Fatal("save circle fail")
	}
}
