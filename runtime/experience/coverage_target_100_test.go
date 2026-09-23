package main

import (
	"crypto/ed25519"
	"encoding/json"
	"errors"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func engineWithClosedDB(t *testing.T, home string) *Engine {
	e, err := newEngine(home)
	if err != nil {
		t.Fatal(err)
	}
	_ = e.db.Close()
	return e
}

func TestOpenEngineWithoutMigrateVectorIndexFailure(t *testing.T) {
	home := t.TempDir()
	setupTestEnv(t, home)
	resetHooksForTest()
	defer resetHooksForTest()
	hookEnsureVectorIndex = func(*Engine) error { return errors.New("vector index fail") }
	if _, err := openEngineWithoutMigrate(home); err == nil {
		t.Fatal("expected ensureVectorIndex error")
	}
}

func TestRunGCFailureBranches(t *testing.T) {
	home := t.TempDir()
	setupTestEnv(t, home)
	repo := makeTestRepo(t, filepath.Join(t.TempDir(), "repo"), "https://github.com/acme/a.git")
	e, err := newEngine(home)
	if err != nil {
		t.Fatal(err)
	}
	defer e.Close()
	project, _ := identifyProject(repo)

	oldDeprecated := "2000-01-01T00:00:00Z"
	mDep, _ := e.recordMemory(project, "episode", "repository", "", "deprecated body", 0.5, 50, "agent_observation", "", "")
	_, _ = e.db.Exec(`UPDATE memories SET status = 'deprecated', updated_at = ? WHERE id = ?`, oldDeprecated, mDep.ID)
	mStale, _ := e.recordMemory(project, "episode", "repository", "", "stale body", 0.1, 50, "agent_observation", "", "")
	_, _ = e.db.Exec(`UPDATE memories SET status = 'stale', updated_at = ? WHERE id = ?`, oldDeprecated, mStale.ID)

	resetHooksForTest()
	defer resetHooksForTest()
	hookDeleteMemory = func(*Engine, string) error { return errors.New("delete fail") }
	if err := e.runGC(""); err == nil {
		t.Fatal("delete fail deprecated")
	}

	hookDeleteMemory = nil
	hookRunGCScanErr = errors.New("scan fail")
	if err := e.runGC(""); err == nil {
		t.Fatal("scan fail")
	}
	hookRunGCScanErr = nil

	_ = e.db.Close()
	if err := e.runGC("sess_prev"); err == nil {
		t.Fatal("query fail")
	}
}

func TestRunGCStaleDeleteFailure(t *testing.T) {
	home := t.TempDir()
	setupTestEnv(t, home)
	repo := makeTestRepo(t, filepath.Join(t.TempDir(), "repo"), "https://github.com/acme/a.git")
	e, err := newEngine(home)
	if err != nil {
		t.Fatal(err)
	}
	defer e.Close()
	project, _ := identifyProject(repo)
	mStale, _ := e.recordMemory(project, "episode", "repository", "", "stale only", 0.1, 50, "agent_observation", "", "")
	_, _ = e.db.Exec(`UPDATE memories SET status = 'stale', updated_at = ? WHERE id = ?`, "2000-01-01T00:00:00Z", mStale.ID)

	resetHooksForTest()
	defer resetHooksForTest()
	hookDeleteMemory = func(*Engine, string) error { return errors.New("delete stale fail") }
	if err := e.runGC(""); err == nil {
		t.Fatal("stale delete fail")
	}
}

func TestLedgerAddInsertAndListFailures(t *testing.T) {
	home := t.TempDir()
	setupTestEnv(t, home)
	repo := makeTestRepo(t, filepath.Join(t.TempDir(), "repo"), "https://github.com/acme/a.git")
	e, err := newEngine(home)
	if err != nil {
		t.Fatal(err)
	}
	defer e.Close()
	project, _ := identifyProject(repo)

	_ = e.db.Close()
	if _, err := e.ledgerAdd(project, "run-z", "decision", "", "", "", ""); err == nil {
		t.Fatal("insert fail")
	}

	e2, err := newEngine(home)
	if err != nil {
		t.Fatal(err)
	}
	defer e2.Close()
	resetHooksForTest()
	defer resetHooksForTest()
	hookLedgerList = func(*Engine, Project, string) ([]LedgerEntry, error) {
		return nil, errors.New("ledger list fail")
	}
	entry, err := e2.ledgerAdd(project, "run-list", "decision", "", "", "", "")
	if err == nil || entry.Decision != "decision" {
		t.Fatalf("list fail entry=%+v err=%v", entry, err)
	}
}

func TestSyncCirclePeersFailureBranches(t *testing.T) {
	home := t.TempDir()
	setupTestEnv(t, home)
	repo := makeTestRepo(t, filepath.Join(t.TempDir(), "repo"), "https://github.com/acme/a.git")
	project := identifyProjectMust(t, repo)
	e, err := newEngine(home)
	if err != nil {
		t.Fatal(err)
	}
	defer e.Close()

	if err := e.syncCirclePeers(project); err != nil {
		t.Fatal(err)
	}

	id, priv, _ := loadOrCreateIdentity(home)
	circle, _ := createCircle(home, "team", id, priv)
	circle, _ = addAllowedFolder(home, circle.ID, repo, id)
	circle.PeerEndpoints = []string{"127.0.0.1:1"}
	_ = saveCircle(home, circle)

	_ = os.WriteFile(identityPath(home), []byte("{bad"), 0o600)
	if err := e.syncCirclePeers(project); err != nil {
		t.Fatal(err)
	}

	_ = os.Remove(identityPath(home))
	id, priv, _ = loadOrCreateIdentity(home)
	circle.Members = []CircleMember{{DeviceID: "other-device", PublicKey: id.PublicKey, Revoked: false}}
	_ = saveCircle(home, circle)
	if err := e.syncCirclePeers(project); err != nil {
		t.Fatal(err)
	}

	circle.Members = []CircleMember{{DeviceID: id.DeviceID, PublicKey: id.PublicKey}}
	_ = saveCircle(home, circle)
	if err := e.syncCirclePeers(project); err != nil {
		t.Fatal(err)
	}

	resetHooksForTest()
	defer resetHooksForTest()
	hookFetchPeerSync = func(string, string, Circle, DeviceIdentity, ed25519.PrivateKey, string, string) (SyncResponse, error) {
		return SyncResponse{}, errors.New("fetch fail")
	}
	if err := e.syncCirclePeers(project); err != nil {
		t.Fatal(err)
	}

	hookFetchPeerSync = func(string, string, Circle, DeviceIdentity, ed25519.PrivateKey, string, string) (SyncResponse, error) {
		return SyncResponse{DeviceID: id.DeviceID, CircleID: "wrong-circle", Repository: project.Repository}, nil
	}
	if err := e.syncCirclePeers(project); err != nil {
		t.Fatal(err)
	}
}

func TestImportPeerBatchSkipsEmptyScope(t *testing.T) {
	home := t.TempDir()
	setupTestEnv(t, home)
	e, err := newEngine(home)
	if err != nil {
		t.Fatal(err)
	}
	defer e.Close()
	id, priv, _ := loadOrCreateIdentity(home)
	circle, _ := createCircle(home, "team", id, priv)
	resp := SyncResponse{
		DeviceID: id.DeviceID,
		CircleID: circle.ID,
		Packets: []SharedPacket{{
			ID: "pkt-empty-scope", Kind: "lesson", HotIndex: true, VectorSpace: "stub",
			Fingerprint: "fp-empty", EvidenceScore: 0.5, UpdatedAt: nowRFC3339(),
		}},
	}
	n, err := e.importPeerBatch(circle, resp)
	if err != nil || n != 0 {
		t.Fatalf("import=%d err=%v", n, err)
	}
}

func TestCLIInnerErrorBranches(t *testing.T) {
	home := t.TempDir()
	setupTestEnv(t, home)
	repo := makeTestRepo(t, filepath.Join(t.TempDir(), "repo"), "https://github.com/acme/a.git")
	t.Setenv("OVERDRIVE_HOME", home)

	resetHooksForTest()
	defer resetHooksForTest()
	hookFilepathAbs = func(path string) (string, error) {
		if path == "bad-cwd" {
			return "", errors.New("abs fail")
		}
		return filepath.Abs(path)
	}
	for _, err := range []error{
		cmdProject([]string{"--cwd", "bad-cwd"}),
		cmdSessionStart([]string{"--cwd", "bad-cwd"}),
		cmdSessionEnd([]string{"--cwd", "bad-cwd"}),
		cmdRecord([]string{"--cwd", "bad-cwd", "--content", "x"}),
		cmdRecall([]string{"--cwd", "bad-cwd"}),
		cmdLedgerAdd([]string{"--cwd", "bad-cwd", "--run", "r", "--decision", "d"}),
		cmdLedgerList([]string{"--cwd", "bad-cwd", "--run", "r"}),
	} {
		if err == nil {
			t.Fatal("expected identifyProject error")
		}
	}
	hookFilepathAbs = filepath.Abs

	hookOpenEngine = func() (*Engine, error) { return engineWithClosedDB(t, home), nil }
	for _, tc := range []struct {
		name string
		fn   func() error
	}{
		{"status", func() error { return cmdStatus(nil) }},
		{"session-start", func() error { return cmdSessionStart([]string{"--cwd", repo}) }},
		{"ledger-add", func() error { return cmdLedgerAdd([]string{"--cwd", repo, "--run", "r1", "--decision", "go"}) }},
		{"ledger-list", func() error { return cmdLedgerList([]string{"--cwd", repo, "--run", "r1"}) }},
	} {
		if err := tc.fn(); err == nil {
			t.Fatalf("%s expected engine error", tc.name)
		}
	}

	e, err := newEngine(home)
	if err != nil {
		t.Fatal(err)
	}
	defer e.Close()
	hookOpenEngine = func() (*Engine, error) { return e, nil }
	hookRecordMemory = func(*Engine, Project, string, string, string, string, float64, int, string, string, string) (Memory, error) {
		return Memory{}, errors.New("record fail")
	}
	if err := cmdRecord([]string{"--cwd", repo, "--content", "body"}); err == nil {
		t.Fatal("record fail")
	}
	hookRecordMemory = nil
	hookEngineRecall = func(*Engine, Project, string, int, string) (RecallResponse, error) {
		return RecallResponse{}, errors.New("recall fail")
	}
	if err := cmdRecall([]string{"--cwd", repo}); err == nil {
		t.Fatal("recall fail")
	}
}

func TestShareCLIErrorBranches(t *testing.T) {
	home := t.TempDir()
	setupTestEnv(t, home)
	repo := makeTestRepo(t, filepath.Join(t.TempDir(), "repo"), "https://github.com/acme/a.git")
	t.Setenv("OVERDRIVE_HOME", home)

	resetHooksForTest()
	defer resetHooksForTest()
	t.Setenv("OVERDRIVE_SHARE_LISTEN", "127.0.0.1:0")
	if err := os.MkdirAll(shareDir(home), 0o700); err != nil {
		t.Fatal(err)
	}
	_ = os.WriteFile(identityPath(home), []byte("{bad"), 0o600)
	if err := cmdShareListen([]string{"--cwd", repo}); err == nil {
		t.Fatal("identity parse fail")
	}

	_ = os.Remove(identityPath(home))
	hookNetListen = func(string, string) (net.Listener, error) {
		return nil, errors.New("listen fail")
	}
	if err := cmdShareListen([]string{"--cwd", repo}); err == nil {
		t.Fatal("listen fail")
	}
	hookNetListen = net.Listen

	hookShareListenWait = func(*shareListener) {}
	if err := cmdShareListen([]string{"--cwd", repo}); err != nil {
		t.Fatal(err)
	}

	started := make(chan struct{})
	go func() {
		close(started)
		hookShareListenWait = nil
		shareListenWait(&shareListener{})
	}()
	<-started
	time.Sleep(time.Millisecond)

	id, priv, _ := loadOrCreateIdentity(home)
	circle, _ := createCircle(home, "team", id, priv)
	_, _ = addAllowedFolder(home, circle.ID, repo, id)
	e, err := newEngine(home)
	if err != nil {
		t.Fatal(err)
	}
	defer e.Close()
	hookOpenEngine = func() (*Engine, error) { return e, nil }
	hookPeerMemoryCount = func(*Engine, string, string) (int, error) {
		return 0, errors.New("peer count fail")
	}
	if err := cmdShareStatus([]string{"--cwd", repo, "--format", "text"}); err == nil {
		t.Fatal("share status fail")
	}
}

func TestSessionEndConsolidateFailure(t *testing.T) {
	home := t.TempDir()
	setupTestEnv(t, home)
	repo := makeTestRepo(t, filepath.Join(t.TempDir(), "repo"), "https://github.com/acme/a.git")
	t.Setenv("OVERDRIVE_HOME", home)
	id, priv, _ := loadOrCreateIdentity(home)
	circle, _ := createCircle(home, "team", id, priv)
	circle, _ = addAllowedFolder(home, circle.ID, repo, id)

	resetHooksForTest()
	defer resetHooksForTest()
	hookOpenEngine = func() (*Engine, error) { return engineWithClosedDB(t, home), nil }
	if err := cmdSessionEnd([]string{"--cwd", repo}); err == nil {
		t.Fatal("session-end consolidate fail")
	}
}

func TestCmdShareStatusIdentifyProjectFailure(t *testing.T) {
	resetHooksForTest()
	defer resetHooksForTest()
	hookFilepathAbs = func(path string) (string, error) {
		if path == "bad-cwd" {
			return "", errors.New("abs fail")
		}
		return filepath.Abs(path)
	}
	if err := cmdShareStatus([]string{"--cwd", "bad-cwd"}); err == nil {
		t.Fatal("share status identify fail")
	}
}

func TestCmdShareIdentityCorrupt(t *testing.T) {
	home := t.TempDir()
	setupTestEnv(t, home)
	t.Setenv("OVERDRIVE_HOME", home)
	if err := os.MkdirAll(shareDir(home), 0o700); err != nil {
		t.Fatal(err)
	}
	_ = os.WriteFile(identityPath(home), []byte("{bad"), 0o600)
	if err := cmdShareIdentity(nil); err == nil {
		t.Fatal("share identity corrupt")
	}
}

func TestCircleFolderListMissingCircle(t *testing.T) {
	home := t.TempDir()
	setupTestEnv(t, home)
	t.Setenv("OVERDRIVE_HOME", home)
	if err := cmdCircleFolderList([]string{"--circle", "missing"}); err == nil {
		t.Fatal("missing circle")
	}
}

func TestCircleCommandOperationFailures(t *testing.T) {
	home := t.TempDir()
	setupTestEnv(t, home)
	t.Setenv("OVERDRIVE_HOME", home)
	if err := cmdCircleInvite([]string{"--circle", "missing"}); err == nil {
		t.Fatal("invite missing circle")
	}
	if err := cmdCircleAccept([]string{"--code", "x", "--fingerprint", "y"}); err == nil {
		t.Fatal("accept invalid invite")
	}
	if err := cmdCircleRevoke([]string{"--circle", "missing", "--device", "d"}); err == nil {
		t.Fatal("revoke missing circle")
	}
	repo := makeTestRepo(t, filepath.Join(t.TempDir(), "repo"), "https://github.com/acme/a.git")
	if err := cmdCircleFolderAdd([]string{"--circle", "missing", "--folder", repo}); err == nil {
		t.Fatal("folder-add missing circle")
	}
}

func TestCircleCreateInviteIdentityFailures(t *testing.T) {
	home := t.TempDir()
	setupTestEnv(t, home)
	t.Setenv("OVERDRIVE_HOME", home)
	if err := os.MkdirAll(shareDir(home), 0o700); err != nil {
		t.Fatal(err)
	}
	_ = os.WriteFile(identityPath(home), []byte("{bad"), 0o600)

	resetHooksForTest()
	defer resetHooksForTest()
	if _, _, err := loadOrCreateIdentity(home); err == nil {
		t.Fatal("identity should be corrupt")
	}
	if err := cmdCircleCreate([]string{"--name", "team"}); err == nil {
		t.Fatal("create identity fail")
	}
	if err := cmdCircleInvite([]string{"--circle", "c1"}); err == nil {
		t.Fatal("invite identity fail")
	}
	if err := cmdCircleAccept([]string{"--code", "c", "--fingerprint", "f"}); err == nil {
		t.Fatal("accept identity fail")
	}
	if err := cmdCircleRevoke([]string{"--circle", "c", "--device", "d"}); err == nil {
		t.Fatal("revoke identity fail")
	}
	if err := cmdCircleFolderAdd([]string{"--circle", "c", "--folder", "/tmp"}); err == nil {
		t.Fatal("folder-add identity fail")
	}

	_ = os.Remove(identityPath(home))
	hookJSONMarshal = func(v any) ([]byte, error) {
		if _, ok := v.([]CircleMember); ok {
			return nil, errors.New("marshal members")
		}
		return json.Marshal(v)
	}
	if err := cmdCircleCreate([]string{"--name", "team"}); err == nil {
		t.Fatal("create circle fail")
	}
}

func TestCircleListReadDirFailure(t *testing.T) {
	home := t.TempDir()
	setupTestEnv(t, home)
	t.Setenv("OVERDRIVE_HOME", home)
	resetHooksForTest()
	defer resetHooksForTest()
	hookReadDir = func(string) ([]os.DirEntry, error) {
		return nil, errors.New("readdir fail")
	}
	if err := cmdCircleList(nil); err == nil {
		t.Fatal("circle list fail")
	}
}
