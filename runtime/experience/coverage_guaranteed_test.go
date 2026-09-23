package main

import (
	"archive/zip"
	"crypto/ed25519"
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func ortZipWithDLL(t *testing.T, dir string, content []byte) string {
	t.Helper()
	zipPath := filepath.Join(dir, "ort-win.zip")
	zf, err := os.Create(zipPath)
	if err != nil {
		t.Fatal(err)
	}
	zw := zip.NewWriter(zf)
	w, err := zw.Create("onnxruntime-win-x64/onnxruntime.dll")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.Write(content); err != nil {
		t.Fatal(err)
	}
	_ = zw.Close()
	_ = zf.Close()
	return zipPath
}

func TestORTZipLogicAndExtract(t *testing.T) {
	if !matchesORTZipEntry("pkg/onnxruntime.dll") {
		t.Fatal("dll entry")
	}
	if matchesORTZipEntry("pkg/libonnxruntime.so") {
		t.Fatal("so should not match zip extractor")
	}
	dir := t.TempDir()
	zipPath := ortZipWithDLL(t, dir, []byte("fake-dll"))
	out := filepath.Join(dir, "lib")
	if err := os.MkdirAll(out, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := extractORTZip(zipPath, out); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(out, ortLibName())); err != nil {
		t.Fatal("extracted ort lib missing")
	}
	if err := extractORTArchive(zipPath, out); err != nil {
		t.Fatal("zip via archive router")
	}
	resetHooksForTest()
	defer resetHooksForTest()
	hookCreateTemp = func(string, string) (writeCloser, error) { return nil, errors.New("temp fail") }
	if err := extractORTZip(ortZipWithDLL(t, t.TempDir(), []byte("x")), t.TempDir()); err == nil {
		t.Fatal("atomic write fail")
	}
}

func TestHandleConnRemainingBranches(t *testing.T) {
	home := t.TempDir()
	setupTestEnv(t, home)
	repo := makeTestRepo(t, filepath.Join(t.TempDir(), "repo"), "https://github.com/acme/a.git")
	id, priv, _ := loadOrCreateIdentity(home)
	circle, _ := createCircle(home, "team", id, priv)
	circle, _ = addAllowedFolder(home, circle.ID, repo, id)
	project := identifyProjectMust(t, repo)
	sl := &shareListener{home: home, id: id}
	reqOK, _ := signSyncRequest(priv, SyncRequest{
		DeviceID: id.DeviceID, CircleID: circle.ID, Repository: project.Repository, Timestamp: nowRFC3339(),
	})

	resetHooksForTest()
	defer resetHooksForTest()
	hookShareNewEngine = func(string) (*Engine, error) { return nil, errors.New("engine fail") }
	c1, s1 := net.Pipe()
	go sl.handleConn(s1, priv)
	_, _ = c1.Write(wireLine(circle, mustJSONBytes(t, reqOK), ""))
	_ = c1.Close()
	time.Sleep(25 * time.Millisecond)

	shareEng, err := newEngine(home)
	if err != nil {
		t.Fatal(err)
	}
	shareEng.tv = nil
	shareEng.tvPeer = nil
	hookShareNewEngine = func(string) (*Engine, error) { return shareEng, nil }

	hookScanMemory = func(scanner) (Memory, error) { return Memory{}, errors.New("export scan fail") }
	c2, s2 := net.Pipe()
	go sl.handleConn(s2, priv)
	_, _ = c2.Write(wireLine(circle, mustJSONBytes(t, reqOK), ""))
	_ = c2.Close()
	time.Sleep(25 * time.Millisecond)

	hookScanMemory = nil
	hookRandRead = func([]byte) (int, error) { return 0, errors.New("rand fail") }
	c3, s3 := net.Pipe()
	go sl.handleConn(s3, priv)
	_, _ = c3.Write(wireLine(circle, mustJSONBytes(t, reqOK), ""))
	_ = c3.Close()
	time.Sleep(25 * time.Millisecond)
	shareEng.Close()
}

func TestFetchPeerSyncRemainingBranches(t *testing.T) {
	home := t.TempDir()
	setupTestEnv(t, home)
	id, priv, _ := loadOrCreateIdentity(home)
	circle, _ := createCircle(home, "team", id, priv)
	circle.Members = append(circle.Members, CircleMember{DeviceID: id.DeviceID, PublicKey: id.PublicKey})

	resetHooksForTest()
	defer resetHooksForTest()
	hookRandRead = func([]byte) (int, error) { return 0, errors.New("rand fail") }
	if _, err := fetchPeerSync(home, "127.0.0.1:1", circle, id, priv, "github.com/acme/r", ""); err == nil {
		t.Fatal("encrypt request fail")
	}
	hookRandRead = rand.Read

	hookJSONMarshal = func(v any) ([]byte, error) {
		if _, ok := v.(SyncRequest); ok {
			return nil, errors.New("marshal req")
		}
		return json.Marshal(v)
	}
	if _, err := fetchPeerSync(home, "127.0.0.1:1", circle, id, priv, "github.com/acme/r", ""); err == nil {
		t.Fatal("marshal req fail")
	}
	hookJSONMarshal = json.Marshal

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	go func() {
		c, _ := ln.Accept()
		if c == nil {
			return
		}
		_, _ = c.Read(make([]byte, 4096))
		cipher, _ := encryptBatch(circle.GroupKey, []byte("not-json"))
		out, _ := json.Marshal(wireEnvelope{CircleID: circle.ID, Ciphertext: cipher})
		_, _ = c.Write(append(out, '\n'))
		_ = c.Close()
	}()
	if _, err := fetchPeerSync(home, ln.Addr().String(), circle, id, priv, "github.com/acme/r", ""); err == nil {
		t.Fatal("plain resp unmarshal fail")
	}
	_ = ln.Close()

	ln2, _ := net.Listen("tcp", "127.0.0.1:0")
	go func() {
		c, _ := ln2.Accept()
		if c == nil {
			return
		}
		_, _ = c.Read(make([]byte, 4096))
		resp, _ := signSyncResponse(priv, SyncResponse{
			DeviceID: "unknown_dev", CircleID: circle.ID, Repository: "github.com/acme/r", Cursor: nowRFC3339(),
		})
		payload, _ := json.Marshal(resp)
		cipher, _ := encryptBatch(circle.GroupKey, payload)
		out, _ := json.Marshal(wireEnvelope{CircleID: circle.ID, Ciphertext: cipher})
		_, _ = c.Write(append(out, '\n'))
		_ = c.Close()
	}()
	if _, err := fetchPeerSync(home, ln2.Addr().String(), circle, id, priv, "github.com/acme/r", ""); err == nil || !strings.Contains(err.Error(), "unknown peer") {
		t.Fatalf("unknown peer: %v", err)
	}
	_ = ln2.Close()

	ln3, _ := net.Listen("tcp", "127.0.0.1:0")
	pubB, _, _ := ed25519.GenerateKey(nil)
	idB := DeviceIdentity{DeviceID: "dev_" + sha256Hex(pubB)[:16], PublicKey: base64.StdEncoding.EncodeToString(pubB)}
	circle.Members = append(circle.Members, CircleMember{DeviceID: idB.DeviceID, PublicKey: idB.PublicKey})
	go func() {
		c, _ := ln3.Accept()
		if c == nil {
			return
		}
		_, _ = c.Read(make([]byte, 4096))
		resp := SyncResponse{DeviceID: idB.DeviceID, CircleID: circle.ID, Repository: "github.com/acme/r", Cursor: nowRFC3339(), Signature: "bad"}
		payload, _ := json.Marshal(resp)
		cipher, _ := encryptBatch(circle.GroupKey, payload)
		out, _ := json.Marshal(wireEnvelope{CircleID: circle.ID, Ciphertext: cipher})
		_, _ = c.Write(append(out, '\n'))
		_ = c.Close()
	}()
	if _, err := fetchPeerSync(home, ln3.Addr().String(), circle, id, priv, "github.com/acme/r", ""); err == nil || !strings.Contains(err.Error(), "invalid peer signature") {
		t.Fatalf("bad signature: %v", err)
	}
	_ = ln3.Close()
}

func TestNotifyPeerJoinWriteFail(t *testing.T) {
	home := t.TempDir()
	setupTestEnv(t, home)
	id, priv, _ := loadOrCreateIdentity(home)
	invite := CircleInvite{CircleID: "c1", Code: "join", GroupKey: validGroupKeyB64(t)}
	resetHooksForTest()
	defer resetHooksForTest()
	hookNetDial = func(string, string, time.Duration) (net.Conn, error) {
		c1, c2 := net.Pipe()
		_ = c2.Close()
		return &errWriteConn{Conn: c1}, nil
	}
	if err := notifyPeerJoin(invite, "127.0.0.1:1", id, priv); err == nil {
		t.Fatal("write fail")
	}
}

func TestNewEngineFailureBranches(t *testing.T) {
	home := t.TempDir()
	setupTestEnv(t, home)
	resetHooksForTest()
	defer resetHooksForTest()

	hookMkdirAll = func(path string, _ os.FileMode) error {
		if path == home {
			return errors.New("home mkdir")
		}
		return os.MkdirAll(path, 0o700)
	}
	if _, err := newEngine(home); err == nil {
		t.Fatal("home mkdir fail")
	}
	hookMkdirAll = os.MkdirAll

	jsonPath := filepath.Join(home, "experience-v1.json")
	_ = os.WriteFile(jsonPath, []byte("{"), 0o600)
	if _, err := newEngine(home); err == nil {
		t.Fatal("migrate json fail")
	}
	_ = os.Remove(jsonPath)

	oldOpen := hookDBOpen
	hookDBOpen = func(driver, dsn string) (*sql.DB, error) { return nil, errors.New("db open fail") }
	if _, err := newEngine(home); err == nil {
		t.Fatal("db open fail")
	}
	hookDBOpen = oldOpen

	e, err := newEngine(home)
	if err != nil {
		t.Fatal(err)
	}
	_ = e.db.Close()
	hookEnsureVectorIndex = func(*Engine) error { return errors.New("vector index fail") }
	if _, err := newEngine(home); err == nil {
		t.Fatal("vector index fail")
	}
}

func TestStartSessionRunGCFailWithPrevious(t *testing.T) {
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
	m, err := e.recordMemory(project, "lesson", "repository", "", "gc candidate", 0.1, 1, "agent_observation", "", "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.db.Exec(`UPDATE memories SET status = 'stale' WHERE id = ?`, m.ID); err != nil {
		t.Fatal(err)
	}
	resetHooksForTest()
	defer resetHooksForTest()
	hookRunGCScanErr = errors.New("gc scan fail")
	if _, err := e.startSession(project); err == nil {
		t.Fatal("runGC fail with previous session")
	}
}

func TestRecordMemoryFailureBranches(t *testing.T) {
	home := t.TempDir()
	setupTestEnv(t, home)
	repo := makeTestRepo(t, filepath.Join(t.TempDir(), "repo"), "https://github.com/acme/a.git")
	e, err := newEngine(home)
	if err != nil {
		t.Fatal(err)
	}
	defer e.Close()
	project, _ := identifyProject(repo)

	if _, err := e.recordMemory(project, "lesson", "bogus_scope", "", "x", 0.5, 50, "agent_observation", "", ""); err == nil {
		t.Fatal("invalid scope")
	}

	m1, err := e.recordMemory(project, "lesson", "repository", "subj", "content A", 0.5, 50, "user_feedback", "", "evidence")
	if err != nil {
		t.Fatal(err)
	}
	m2, err := e.recordMemory(project, "lesson", "repository", "subj", "content A", 0.6, 60, "adr", "ref", "")
	if err != nil || m2.ID != m1.ID {
		t.Fatalf("dedup update: %v %s %s", err, m1.ID, m2.ID)
	}
	if m2.Confidence < 0.95 {
		t.Fatal("adr confidence floor")
	}

	closed := engineWithClosedDB(t, home)
	if _, err := closed.recordMemory(project, "lesson", "repository", "", "closed db", 0.5, 50, "agent_observation", "", ""); err == nil {
		t.Fatal("upsert on closed db")
	}
}

func TestRecallFailureAndSessionSeed(t *testing.T) {
	home := t.TempDir()
	setupTestEnv(t, home)
	repo := makeTestRepo(t, filepath.Join(t.TempDir(), "repo"), "https://github.com/acme/a.git")
	e, err := newEngine(home)
	if err != nil {
		t.Fatal(err)
	}
	project, _ := identifyProject(repo)
	_, _ = e.startSession(project)
	_, _ = e.recordMemory(project, "rule", "repository", "", "always validate input", 0.9, 90, "project_instruction", "", "")

	resetHooksForTest()
	defer resetHooksForTest()
	hookScanMemory = func(scanner) (Memory, error) { return Memory{}, errors.New("recall list fail") }
	if _, err := e.recall(project, "validate", 5, ""); err == nil {
		t.Fatal("listActiveMemories fail")
	}
	hookScanMemory = nil

	resp, err := e.recall(project, "validate input", 5, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(resp.WorkingMemory) == 0 {
		t.Fatal("expected working memory seed")
	}
	if resp.Memories == nil || resp.CriticalRules == nil {
		t.Fatal("nil slices normalized")
	}
	e.Close()
}

func TestSyncReconcilerHelpers(t *testing.T) {
	circle := Circle{
		ID: "c1",
		Members: []CircleMember{{DeviceID: "d1", PublicKey: "pk", Revoked: false}},
		AllowedFolders: []string{"/allowed"},
	}
	if err := validatePeerBatchCircle(circle, SyncResponse{CircleID: "c1", DeviceID: "d1"}); err != nil {
		t.Fatal(err)
	}
	if err := validatePeerBatchCircle(circle, SyncResponse{CircleID: "other"}); err == nil {
		t.Fatal("circle mismatch")
	}
	report := shareSyncReport{}
	applySharePeerSyncResult(&report, sharePeerSyncResult{Contacted: true, OK: true, Imported: 1, Cursor: ""}, func(string) {
		t.Fatal("empty cursor should not call setter")
	})
	if report.Imported != 1 {
		t.Fatal("import count")
	}
}
