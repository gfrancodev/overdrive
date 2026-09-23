package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPureHelperBranches(t *testing.T) {
	if scopeIDFor("bogus", Project{Repository: "r", Organization: "o"}) != "" {
		t.Fatal("scope default")
	}
	tokens := collectSignatureTokens("ab 12 x")
	if len(tokens) != 0 {
		t.Fatalf("short tokens filtered: %v", tokens)
	}
	if collectSignatureTokens("ERR_TIMEOUT src/main.go HTTP 503") == nil {
		t.Fatal("real tokens")
	}

	resetHooksForTest()
	defer resetHooksForTest()
	hookRandRead = func([]byte) (int, error) { return 0, errors.New("rand fail") }
	if _, err := encryptBatch(base64.StdEncoding.EncodeToString(make([]byte, 32)), []byte("x")); err == nil {
		t.Fatal("rand fail")
	}
	if _, err := encryptBatch("not-b64", []byte("x")); err == nil {
		t.Fatal("bad key b64")
	}
	if _, err := encryptBatch(base64.StdEncoding.EncodeToString(make([]byte, 16)), []byte("x")); err == nil {
		t.Fatal("bad key len")
	}
}

func TestEnsureHomeAndOrtReleaseURL(t *testing.T) {
	resetHooksForTest()
	defer resetHooksForTest()
	t.Setenv("OVERDRIVE_HOME", t.TempDir())
	hookMkdirAll = func(path string, _ os.FileMode) error {
		if strings.Contains(path, "lib") {
			return errors.New("lib mkdir fail")
		}
		return os.MkdirAll(path, 0o700)
	}
	if _, err := ensureHome(); err == nil {
		t.Fatal("ensure home lib fail")
	}

	t.Setenv("OVERDRIVE_SKIP_EMBED_DOWNLOAD", "1")
	if _, err := ortReleaseURL(); err == nil {
		t.Fatal("ort download disabled")
	}
	t.Setenv("OVERDRIVE_SKIP_EMBED_DOWNLOAD", "0")
	t.Setenv("OVERDRIVE_ORT_URL", "")
	oldURL := defaultORTURL
	defaultORTURL = func() string { return "" }
	defer func() { defaultORTURL = oldURL }()
	if _, err := ortReleaseURL(); err == nil {
		t.Fatal("no ort url")
	}
}

func TestExtractORTArchiveErrorBranches(t *testing.T) {
	dir := t.TempDir()
	if err := extractORTArchive(filepath.Join(dir, "missing.tgz"), dir); err == nil {
		t.Fatal("missing tgz")
	}
	if err := extractORTArchive(filepath.Join(dir, "bad.tgz"), dir); err == nil {
		t.Fatal("bad gzip")
	}
	if err := os.WriteFile(filepath.Join(dir, "bad.tgz"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := extractORTArchive(filepath.Join(dir, "bad.tgz"), dir); err == nil {
		t.Fatal("invalid gzip content")
	}

	var buf bytes.Buffer
	gzw := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gzw)
	hdr := &tar.Header{Name: "readme.txt", Mode: 0o644, Size: 1, Typeflag: tar.TypeReg}
	_ = tw.WriteHeader(hdr)
	_, _ = tw.Write([]byte("x"))
	_ = tw.Close()
	_ = gzw.Close()
	emptyTgz := filepath.Join(dir, "empty.tgz")
	if err := os.WriteFile(emptyTgz, buf.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := extractORTArchive(emptyTgz, dir); err == nil {
		t.Fatal("no ort in tgz")
	}
	if err := extractORTArchive(filepath.Join(dir, "nope.xyz"), dir); err == nil {
		t.Fatal("unsupported archive")
	}

	zipPath := filepath.Join(dir, "bad.zip")
	zf, err := os.Create(zipPath)
	if err != nil {
		t.Fatal(err)
	}
	_ = zf.Close()
	if err := extractORTZip(zipPath, dir); err == nil {
		t.Fatal("empty zip")
	}
}

func TestValidateMemoryHandlerAndFailureStale(t *testing.T) {
	home := t.TempDir()
	setupTestEnv(t, home)
	repo := makeTestRepo(t, filepath.Join(t.TempDir(), "repo"), "https://github.com/acme/a.git")
	e, err := newEngine(home)
	if err != nil {
		t.Fatal(err)
	}
	defer e.Close()
	project, _ := identifyProject(repo)
	m, _ := e.recordMemory(project, "lesson", "repository", "", "fail path", 0.36, 50, "agent_observation", "", "")

	resetHooksForTest()
	defer resetHooksForTest()
	hookValidateMemoryHandler = func(*Engine, *Memory, string, string, string, string) error {
		return errors.New("handler fail")
	}
	if _, err := e.validateMemory(m.ID, "success", "", ""); err == nil {
		t.Fatal("handler fail")
	}
	hookValidateMemoryHandler = nil

	peer, _ := e.recordMemory(project, "lesson", "repository", "", "peer fail", 0.36, 50, "agent_observation", "", "")
	peer.Origin = "peer"
	peer.PeerDeviceID = "dev_peer"
	peer.CircleID = "circle_x"
	peer.HotIndex = true
	if err := e.upsertMemory(peer); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 6; i++ {
		if _, err := e.validateMemory(peer.ID, "failure", "", ""); err != nil {
			t.Fatal(err)
		}
	}
	got, _ := e.getMemory(peer.ID)
	if got.Status != "stale" {
		t.Fatalf("status=%s", got.Status)
	}
}

func TestExportPeerIndexAndConsolidate(t *testing.T) {
	home := t.TempDir()
	setupTestEnv(t, home)
	repo := makeTestRepo(t, filepath.Join(t.TempDir(), "repo"), "https://github.com/acme/a.git")
	id, priv, _ := loadOrCreateIdentity(home)
	circle, _ := createCircle(home, "team", id, priv)
	circle, _ = addAllowedFolder(home, circle.ID, repo, id)
	e, err := newEngine(home)
	if err != nil {
		t.Fatal(err)
	}
	defer e.Close()
	project := identifyProjectMust(t, repo)
	m, _ := e.recordMemory(project, "lesson", "repository", "", "export body", 0.9, 50, "verified_execution", "", "")
	m.HotIndex = true
	m.Layer = 2
	m.SourceFolder = repo
	m.PacketContent = ""
	if err := e.upsertMemory(m); err != nil {
		t.Fatal(err)
	}
	if err := e.exportPeerIndex(circle, project); err != nil {
		t.Fatal(err)
	}
	_ = e.db.Close()
	if err := e.exportPeerIndex(circle, project); err == nil {
		t.Fatal("export list fail")
	}

	e2, err := newEngine(home)
	if err != nil {
		t.Fatal(err)
	}
	defer e2.Close()
	if err := e2.consolidateSession(project, circle); err != nil {
		t.Fatal(err)
	}
	resetHooksForTest()
	defer resetHooksForTest()
	hookConsolidateScanErr = errors.New("scan fail")
	if err := e2.consolidateSession(project, circle); err == nil {
		t.Fatal("consolidate scan fail")
	}
}

func TestUpsertMemoryPeerAndDBError(t *testing.T) {
	home := t.TempDir()
	setupTestEnv(t, home)
	repo := makeTestRepo(t, filepath.Join(t.TempDir(), "repo"), "https://github.com/acme/a.git")
	e, err := newEngine(home)
	if err != nil {
		t.Fatal(err)
	}
	defer e.Close()
	project, _ := identifyProject(repo)
	m, _ := e.recordMemory(project, "lesson", "repository", "", "peer vec", 0.8, 50, "agent_observation", "", "")
	m.Origin = "peer"
	m.HotIndex = true
	m.Status = "active"
	if err := e.upsertMemory(m); err != nil {
		t.Fatal(err)
	}
	_ = e.db.Close()
	m2 := m
	m2.ID = "mem_closed_db"
	if err := e.upsertMemory(m2); err == nil {
		t.Fatal("upsert db fail")
	}
}

func TestColumnExistsAndMigrateSchema(t *testing.T) {
	if columnExists(nil, "memories", "id") {
		t.Fatal("nil db")
	}
	home := t.TempDir()
	setupTestEnv(t, home)
	e, err := newEngine(home)
	if err != nil {
		t.Fatal(err)
	}
	defer e.Close()
	if !columnExists(e.db, "memories", "id") {
		t.Fatal("id column")
	}
	resetHooksForTest()
	defer resetHooksForTest()
	hookColumnInfoScanErr = errors.New("scan fail")
	if columnExists(e.db, "memories", "id") {
		t.Fatal("scan fail")
	}
	if _, err := e.db.Exec(`ALTER TABLE memories DROP COLUMN hot_index`); err != nil {
		t.Skip("sqlite drop column unsupported: " + err.Error())
	}
	_, _ = e.db.Exec(`UPDATE meta SET value = '0' WHERE key = 'schema'`)
	hookMigrateAddColumn = func(*sql.DB, string, string) error { return errors.New("alter fail") }
	if err := e.migrateSchema(); err == nil {
		t.Fatal("migrate alter fail")
	}
}

func TestShareCircleErrorBranches(t *testing.T) {
	home := t.TempDir()
	setupTestEnv(t, home)
	id, priv, _ := loadOrCreateIdentity(home)

	resetHooksForTest()
	defer resetHooksForTest()
	hookMkdirAll = func(path string, _ os.FileMode) error {
		if strings.Contains(path, "circles") {
			return errors.New("mkdir circles")
		}
		return os.MkdirAll(path, 0o700)
	}
	if _, err := listCircles(home); err == nil {
		t.Fatal("list circles mkdir")
	}
	hookMkdirAll = os.MkdirAll

	hookWriteFile = func(string, []byte, os.FileMode) error { return errors.New("save fail") }
	if _, err := createCircle(home, "team", id, priv); err == nil {
		t.Fatal("create save fail")
	}
	resetHooksForTest()

	circle, _ := createCircle(home, "team", id, priv)
	hookJSONMarshal = func(v any) ([]byte, error) {
		if _, ok := v.([]CircleMember); ok {
			return nil, errors.New("sign fail")
		}
		return json.Marshal(v)
	}
	if _, err := revokeMember(home, circle.ID, id.DeviceID, id, priv); err == nil {
		t.Fatal("revoke sign fail")
	}
}

func TestAcceptInviteAndJoinErrors(t *testing.T) {
	home := t.TempDir()
	setupTestEnv(t, home)
	if err := os.MkdirAll(invitesDir(home), 0o700); err != nil {
		t.Fatal(err)
	}
	_ = os.WriteFile(filepath.Join(invitesDir(home), "bad.json"), []byte("{"), 0o600)
	id, priv, _ := loadOrCreateIdentity(home)
	if _, err := acceptInvite(home, "bad", "fp", "", id, priv); err == nil {
		t.Fatal("bad invite json")
	}

	sl := &shareListener{home: home}
	if !sl.tryHandleJoin(wireEnvelope{Kind: "join"}, []byte(`{"circle_id":"missing","code":"c","public_key":"pk","device_id":"d","timestamp":"t","signature":"s"}`)) {
		t.Fatal("missing circle handled")
	}
}

func TestFolderAllowedAbsErrors(t *testing.T) {
	resetHooksForTest()
	defer resetHooksForTest()
	hookFilepathAbs = func(string) (string, error) { return "", errors.New("abs fail") }
	c := Circle{AllowedFolders: []string{"/tmp/repo"}}
	if folderAllowed(c, "/tmp/repo") {
		t.Fatal("abs fail root")
	}
}

func TestFTSCandidatesScanAndEmptyTokens(t *testing.T) {
	home := t.TempDir()
	setupTestEnv(t, home)
	repo := makeTestRepo(t, filepath.Join(t.TempDir(), "repo"), "https://github.com/acme/a.git")
	e, err := newEngine(home)
	if err != nil {
		t.Fatal(err)
	}
	defer e.Close()
	project, _ := identifyProject(repo)
	if _, _, err := e.ftsCandidates("!!!", project, 5); err != nil {
		t.Fatal(err)
	}
	if _, err := e.recordMemory(project, "lesson", "repository", "", "factory pattern wiring", 0.5, 50, "agent_observation", "", ""); err != nil {
		t.Fatal(err)
	}
	resetHooksForTest()
	defer resetHooksForTest()
	hookFTSScanErr = errors.New("fts scan fail")
	if _, _, err := e.ftsCandidates("factory", project, 5); err == nil {
		t.Fatal("fts scan fail")
	}
}

func TestFetchPeerSyncWireErrors(t *testing.T) {
	home := t.TempDir()
	setupTestEnv(t, home)
	id, priv, _ := loadOrCreateIdentity(home)
	circle, _ := createCircle(home, "team", id, priv)
	circle.Members = append(circle.Members, CircleMember{DeviceID: id.DeviceID, PublicKey: id.PublicKey})

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go func() {
		conn, _ := ln.Accept()
		if conn != nil {
			_, _ = conn.Write([]byte("not-json\n"))
			_ = conn.Close()
		}
	}()
	if _, err := fetchPeerSync(home, ln.Addr().String(), circle, id, priv, "github.com/acme/r", ""); err == nil {
		t.Fatal("bad response json")
	}

	resetHooksForTest()
	defer resetHooksForTest()
	hookJSONMarshal = func(v any) ([]byte, error) {
		if _, ok := v.(SyncRequest); ok {
			return nil, errors.New("marshal req")
		}
		return json.Marshal(v)
	}
	if _, err := fetchPeerSync(home, "127.0.0.1:1", circle, id, priv, "github.com/acme/r", ""); err == nil {
		t.Fatal("marshal req")
	}
}

func TestStartSessionInsertFailure(t *testing.T) {
	home := t.TempDir()
	setupTestEnv(t, home)
	repo := makeTestRepo(t, filepath.Join(t.TempDir(), "repo"), "https://github.com/acme/a.git")
	e, err := newEngine(home)
	if err != nil {
		t.Fatal(err)
	}
	project, _ := identifyProject(repo)
	_, _ = e.db.Exec(`DROP TABLE sessions`)
	_ = e.db.Close()
	if _, err := e.startSession(project); err == nil {
		t.Fatal("session insert fail")
	}
}
