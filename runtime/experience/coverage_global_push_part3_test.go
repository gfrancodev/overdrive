package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestEnsureHomeOverdriveHomeFail(t *testing.T) {
	resetHooksForTest()
	defer resetHooksForTest()
	t.Setenv("OVERDRIVE_HOME", "")
	hookUserHomeDir = func() (string, error) { return "", errors.New("no user home") }
	if _, err := ensureHome(); err == nil {
		t.Fatal("overdrive home fail")
	}
	t.Setenv("OVERDRIVE_HOME", t.TempDir())
	hookFilepathAbs = func(string) (string, error) { return "", errors.New("abs fail") }
	if _, err := ensureHome(); err == nil {
		t.Fatal("abs fail")
	}
}

func TestNewEngineValidationAndMkdir(t *testing.T) {
	if _, err := newEngine(""); err == nil {
		t.Fatal("empty home")
	}
	home := t.TempDir()
	setupTestEnv(t, home)
	resetHooksForTest()
	defer resetHooksForTest()
	hookMkdirAll = func(path string, _ os.FileMode) error {
		if strings.Contains(path, "lib") {
			return errors.New("lib mkdir")
		}
		return os.MkdirAll(path, 0o700)
	}
	if _, err := newEngine(home); err == nil {
		t.Fatal("lib mkdir in newEngine")
	}
}

func TestCollectSignatureTokenBranches(t *testing.T) {
	short := collectSignatureTokens("ab cd ef")
	if len(short) != 0 {
		t.Fatalf("short tokens filtered: %v", short)
	}
	rich := collectSignatureTokens("ERR_TIMEOUT ./pkg/foo.go UserRepo HTTP 503")
	if len(rich) < 3 {
		t.Fatalf("expected rich tokens, got %v", rich)
	}
}

func TestMigrateJSONBranches(t *testing.T) {
	home := t.TempDir()
	setupTestEnv(t, home)
	if err := migrateJSONIfNeeded(home); err != nil {
		t.Fatal(err)
	}
	bad := filepath.Join(home, "experience-v1.json")
	if err := os.WriteFile(bad, []byte("{"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := migrateJSONIfNeeded(home); err == nil {
		t.Fatal("bad json migrate")
	}
	store := JSONStore{Memories: []Memory{{ID: "m1", Kind: "lesson", Scope: "repository", ScopeID: "github.com/a/r", Content: "x", Confidence: 0.5}}}
	data, _ := json.Marshal(store)
	if err := os.WriteFile(bad, data, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := migrateJSONIfNeeded(home); err != nil {
		t.Fatal(err)
	}
}

func TestListActiveMemoriesScanFail(t *testing.T) {
	home := t.TempDir()
	setupTestEnv(t, home)
	repo := makeTestRepo(t, filepath.Join(t.TempDir(), "repo"), "https://github.com/acme/a.git")
	e, err := newEngine(home)
	if err != nil {
		t.Fatal(err)
	}
	defer e.Close()
	project, _ := identifyProject(repo)
	_, _ = e.recordMemory(project, "lesson", "repository", "", "scan fail", 0.5, 50, "agent_observation", "", "")
	resetHooksForTest()
	defer resetHooksForTest()
	hookScanMemory = func(scanner) (Memory, error) { return Memory{}, errors.New("scan fail") }
	if _, err := e.listActiveMemories(); err == nil {
		t.Fatal("list scan fail")
	}
}

func TestStartSessionPreviousConsolidate(t *testing.T) {
	home := t.TempDir()
	setupTestEnv(t, home)
	repo := makeTestRepo(t, filepath.Join(t.TempDir(), "repo"), "https://github.com/acme/a.git")
	e, err := newEngine(home)
	if err != nil {
		t.Fatal(err)
	}
	defer e.Close()
	project, _ := identifyProject(repo)
	s1, err := e.startSession(project)
	if err != nil || s1 == "" {
		t.Fatal(err)
	}
	m, _ := e.recordMemory(project, "lesson", "repository", "", "cold lesson", 0.2, 50, "agent_observation", "", "")
	for i := 0; i < 3; i++ {
		_, _ = e.validateMemory(m.ID, "failure", "", "")
	}
	s2, err := e.startSession(project)
	if err != nil || s2 == "" || s2 == s1 {
		t.Fatalf("second session: %v %s %s", err, s1, s2)
	}
}

func TestStartSessionMetaInsertFail(t *testing.T) {
	home := t.TempDir()
	setupTestEnv(t, home)
	repo := makeTestRepo(t, filepath.Join(t.TempDir(), "repo"), "https://github.com/acme/a.git")
	e, err := newEngine(home)
	if err != nil {
		t.Fatal(err)
	}
	project, _ := identifyProject(repo)
	_, _ = e.db.Exec(`DROP TABLE meta`)
	if _, err := e.startSession(project); err == nil {
		t.Fatal("meta insert fail")
	}
	e.Close()
}

func TestConsolidateSessionScanFail(t *testing.T) {
	home := t.TempDir()
	setupTestEnv(t, home)
	repo := makeTestRepo(t, filepath.Join(t.TempDir(), "repo"), "https://github.com/acme/a.git")
	e, err := newEngine(home)
	if err != nil {
		t.Fatal(err)
	}
	defer e.Close()
	project, _ := identifyProject(repo)
	_, _ = e.recordMemory(project, "lesson", "repository", "", "consolidate row", 0.5, 50, "agent_observation", "", "")
	resetHooksForTest()
	defer resetHooksForTest()
	hookConsolidateScanErr = errors.New("consolidate scan")
	if err := e.consolidateSession(project, Circle{}); err == nil {
		t.Fatal("consolidate scan fail")
	}
}

func TestDecryptBatchShortCiphertext(t *testing.T) {
	key := base64.StdEncoding.EncodeToString(make([]byte, 32))
	if _, err := decryptBatch(key, base64.StdEncoding.EncodeToString([]byte("x"))); err == nil {
		t.Fatal("short ciphertext")
	}
	if _, err := decryptBatch("not-b64", "x"); err == nil {
		t.Fatal("bad key b64")
	}
}

func TestNotifyPeerJoinMarshalAndEncryptFail(t *testing.T) {
	home := t.TempDir()
	setupTestEnv(t, home)
	id, priv, _ := loadOrCreateIdentity(home)
	invite := CircleInvite{CircleID: "c1", GroupKey: validGroupKeyB64(t), Code: "join1"}
	if err := notifyPeerJoin(invite, "", id, priv); err != nil {
		t.Fatal(err)
	}
	resetHooksForTest()
	defer resetHooksForTest()
	hookJSONMarshal = func(v any) ([]byte, error) {
		if _, ok := v.(JoinRequest); ok {
			return nil, errors.New("marshal join")
		}
		return json.Marshal(v)
	}
	if err := notifyPeerJoin(invite, "127.0.0.1:1", id, priv); err == nil {
		t.Fatal("marshal join")
	}
	hookJSONMarshal = json.Marshal
	hookRandRead = func([]byte) (int, error) { return 0, errors.New("rand") }
	if err := notifyPeerJoin(invite, "127.0.0.1:1", id, priv); err == nil {
		t.Fatal("encrypt fail")
	}
}

func TestExtractORTArchiveWithLib(t *testing.T) {
	dir := t.TempDir()
	var buf bytes.Buffer
	gzw := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gzw)
	hdr := &tar.Header{Name: "lib/libonnxruntime.so", Mode: 0o755, Size: 8, Typeflag: tar.TypeReg}
	_ = tw.WriteHeader(hdr)
	_, _ = tw.Write([]byte("fake-lib"))
	_ = tw.Close()
	_ = gzw.Close()
	tgz := filepath.Join(dir, "ort.tgz")
	if err := os.WriteFile(tgz, buf.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(dir, "out")
	if err := os.MkdirAll(out, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := extractORTArchive(tgz, out); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(out, ortLibName())); err != nil {
		t.Fatal("extracted lib missing")
	}
}

func TestHandleConnInvalidEnvelope(t *testing.T) {
	home := t.TempDir()
	setupTestEnv(t, home)
	id, priv, _ := loadOrCreateIdentity(home)
	sl := &shareListener{home: home, id: id}
	s1, s2 := net.Pipe()
	go sl.handleConn(s1, priv)
	_, _ = s2.Write([]byte("not-json\n"))
	_ = s2.Close()
}

func TestHandleConnSyncRoundTrip(t *testing.T) {
	home := t.TempDir()
	setupTestEnv(t, home)
	repo := makeTestRepo(t, filepath.Join(t.TempDir(), "repo"), "https://github.com/acme/r.git")
	project, _ := identifyProject(repo)
	id, priv, _ := loadOrCreateIdentity(home)
	circle, _ := createCircle(home, "team", id, priv)
	_, _ = addAllowedFolder(home, circle.ID, project.Root, id)
	e, _ := newEngine(home)
	defer e.Close()
	_, _ = e.recordMemory(project, "lesson", "repository", "", "shared lesson", 0.6, 50, "agent_observation", "", "")

	sl := &shareListener{home: home, id: id}
	server, client := net.Pipe()
	go sl.handleConn(server, priv)

	req := SyncRequest{
		DeviceID:   id.DeviceID,
		Repository: project.Repository,
		Since:      "",
		Timestamp:  nowRFC3339(),
	}
	req, _ = signSyncRequest(priv, req)
	payload, _ := json.Marshal(req)
	cipher, _ := encryptBatch(circle.GroupKey, payload)
	env, _ := json.Marshal(wireEnvelope{CircleID: circle.ID, Ciphertext: cipher})
	_, _ = client.Write(append(env, '\n'))
	line, err := io.ReadAll(client)
	if err != nil {
		t.Fatal(err)
	}
	if len(strings.TrimSpace(string(line))) == 0 {
		t.Fatal("empty sync response")
	}
	_ = client.Close()
}

func TestPeerEligibleLoadCircleFail(t *testing.T) {
	home := t.TempDir()
	setupTestEnv(t, home)
	repo := makeTestRepo(t, filepath.Join(t.TempDir(), "repo"), "https://github.com/acme/a.git")
	e, _ := newEngine(home)
	defer e.Close()
	project, _ := identifyProject(repo)
	m := Memory{
		ID: "p1", Status: "active", Origin: "peer", HotIndex: true,
		ScopeID: project.Repository, CircleID: "missing", PeerDeviceID: "dev",
	}
	if e.peerEligible(m, project) {
		t.Fatal("missing circle")
	}
}

func TestListWorkingMemoryScanFail(t *testing.T) {
	home := t.TempDir()
	setupTestEnv(t, home)
	repo := makeTestRepo(t, filepath.Join(t.TempDir(), "repo"), "https://github.com/acme/a.git")
	e, _ := newEngine(home)
	defer e.Close()
	project, _ := identifyProject(repo)
	_, _ = e.startSession(project)
	_ = e.addWorkingMemory(project, "note", "s", "c")
	_, _ = e.db.Exec(`ALTER TABLE working_memory ADD COLUMN corrupt_extra TEXT`)
	resetHooksForTest()
	defer resetHooksForTest()
	// Force scan mismatch by altering expected columns indirectly via broken query path:
	_, _ = e.db.Exec(`DROP TABLE working_memory`)
	if _, err := e.listWorkingMemory(project); err == nil {
		t.Fatal("working memory query fail")
	}
}

func TestAtomicWriteFileBranches(t *testing.T) {
	dir := t.TempDir()
	resetHooksForTest()
	defer resetHooksForTest()
	if err := atomicWriteFile(filepath.Join(dir, "ok.txt"), strings.NewReader("data"), 0o600); err != nil {
		t.Fatal(err)
	}
	hookCreateTemp = func(string, string) (writeCloser, error) { return nil, errors.New("temp fail") }
	if err := atomicWriteFile(filepath.Join(dir, "x.txt"), strings.NewReader("x"), 0o600); err == nil {
		t.Fatal("temp fail")
	}
}

func TestBuildMiniLMEmbedderFakeORT(t *testing.T) {
	home := t.TempDir()
	setupTestEnv(t, home)
	resetORTLibForTest()
	fakeLib := filepath.Join(libDir(home), ortLibName())
	if err := os.MkdirAll(filepath.Dir(fakeLib), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(fakeLib, []byte("fake-ort"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("OVERDRIVE_ORT_LIB", fakeLib)
	md := modelDir(home)
	if err := os.MkdirAll(md, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(miniLMVocabPath(home), []byte("hello\n[CLS]\n[SEP]\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(miniLMModelPath(home), []byte("onnx"), 0o600); err != nil {
		t.Fatal(err)
	}
	resetHooksForTest()
	defer resetHooksForTest()
	hookTryORTSession = func(string, string) (modelRunner, error) {
		return fakeModelRunner{}, nil
	}
	emb, err := buildMiniLMEmbedder(home)
	if err != nil {
		t.Fatal(err)
	}
	if emb == nil {
		t.Fatal("nil embedder")
	}
}
