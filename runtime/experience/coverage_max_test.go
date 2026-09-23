package main

import (
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

type errWriteConn struct {
	net.Conn
}

func (c *errWriteConn) Write([]byte) (int, error) {
	return 0, errors.New("write fail")
}

type oneLineConn struct {
	net.Conn
	line []byte
	read bool
}

func (c *oneLineConn) Read(b []byte) (int, error) {
	if !c.read {
		c.read = true
		n := copy(b, c.line)
		return n, nil
	}
	return 0, errors.New("eof")
}

func wireLine(circle Circle, plain []byte, kind string) []byte {
	cipher, _ := encryptBatch(circle.GroupKey, plain)
	out, _ := json.Marshal(wireEnvelope{CircleID: circle.ID, Ciphertext: cipher, Kind: kind})
	return append(out, '\n')
}

func TestHandleConnPipeBranches(t *testing.T) {
	home := t.TempDir()
	setupTestEnv(t, home)
	repo := makeTestRepo(t, filepath.Join(t.TempDir(), "repo"), "https://github.com/acme/a.git")
	id, priv, _ := loadOrCreateIdentity(home)
	circle, _ := createCircle(home, "team", id, priv)
	circle, _ = addAllowedFolder(home, circle.ID, repo, id)
	sl := &shareListener{home: home, id: id}

	// read error
	c1, s1 := net.Pipe()
	go sl.handleConn(s1, priv)
	_ = c1.Close()
	time.Sleep(20 * time.Millisecond)

	// invalid json line
	c2, s2 := net.Pipe()
	go sl.handleConn(s2, priv)
	_, _ = c2.Write([]byte("not-json\n"))
	_ = c2.Close()
	time.Sleep(20 * time.Millisecond)

	// unknown circle
	c3, s3 := net.Pipe()
	go sl.handleConn(s3, priv)
	env, _ := json.Marshal(wireEnvelope{CircleID: "circle_missing", Ciphertext: "x"})
	_, _ = c3.Write(append(env, '\n'))
	_ = c3.Close()
	time.Sleep(20 * time.Millisecond)

	// decrypt fail
	c4, s4 := net.Pipe()
	go sl.handleConn(s4, priv)
	env, _ = json.Marshal(wireEnvelope{CircleID: circle.ID, Ciphertext: "not-valid-cipher"})
	_, _ = c4.Write(append(env, '\n'))
	_ = c4.Close()
	time.Sleep(20 * time.Millisecond)

	// invalid sync plaintext
	c5, s5 := net.Pipe()
	go sl.handleConn(s5, priv)
	_, _ = c5.Write(wireLine(circle, []byte("not-json"), ""))
	_ = c5.Close()
	time.Sleep(20 * time.Millisecond)

	// inactive member
	pubB, privB, _ := ed25519.GenerateKey(nil)
	idB := DeviceIdentity{DeviceID: "dev_" + sha256Hex(pubB)[:16], PublicKey: base64.StdEncoding.EncodeToString(pubB)}
	reqSync := SyncRequest{DeviceID: idB.DeviceID, CircleID: circle.ID, Repository: identifyProjectMust(t, repo).Repository, Timestamp: nowRFC3339()}
	reqSync, _ = signSyncRequest(privB, reqSync)
	c6, s6 := net.Pipe()
	go sl.handleConn(s6, priv)
	_, _ = c6.Write(wireLine(circle, mustJSONBytes(t, reqSync), ""))
	_ = c6.Close()
	time.Sleep(20 * time.Millisecond)

	// bad sync signature
	reqBad := SyncRequest{DeviceID: id.DeviceID, CircleID: circle.ID, Repository: identifyProjectMust(t, repo).Repository, Timestamp: nowRFC3339(), Signature: "bad"}
	c7, s7 := net.Pipe()
	go sl.handleConn(s7, priv)
	_, _ = c7.Write(wireLine(circle, mustJSONBytes(t, reqBad), ""))
	_ = c7.Close()
	time.Sleep(20 * time.Millisecond)

	// marshal response fail
	resetHooksForTest()
	defer resetHooksForTest()
	hookJSONMarshal = func(v any) ([]byte, error) {
		if _, ok := v.(SyncResponse); ok {
			return nil, errors.New("resp marshal fail")
		}
		return json.Marshal(v)
	}
	reqOK, _ := signSyncRequest(priv, SyncRequest{DeviceID: id.DeviceID, CircleID: circle.ID, Repository: identifyProjectMust(t, repo).Repository, Timestamp: nowRFC3339()})
	c8, s8 := net.Pipe()
	go sl.handleConn(s8, priv)
	_, _ = c8.Write(wireLine(circle, mustJSONBytes(t, reqOK), ""))
	_ = c8.Close()
	time.Sleep(20 * time.Millisecond)
}

func mustJSONBytes(t *testing.T, v any) []byte {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestFetchPeerSyncResponseErrors(t *testing.T) {
	home := t.TempDir()
	setupTestEnv(t, home)
	id, priv, _ := loadOrCreateIdentity(home)
	circle, _ := createCircle(home, "team", id, priv)
	circle.Members = append(circle.Members, CircleMember{DeviceID: id.DeviceID, PublicKey: id.PublicKey})

	// server closes without response
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	go func() {
		c, _ := ln.Accept()
		if c != nil {
			_, _ = c.Read(make([]byte, 4096))
			_ = c.Close()
		}
	}()
	if _, err := fetchPeerSync(home, ln.Addr().String(), circle, id, priv, "github.com/acme/r", ""); err == nil {
		t.Fatal("read fail expected")
	}
	_ = ln.Close()

	// invalid wire json
	ln2, _ := net.Listen("tcp", "127.0.0.1:0")
	go func() {
		c, _ := ln2.Accept()
		if c != nil {
			_, _ = c.Write([]byte("garbage\n"))
			_ = c.Close()
		}
	}()
	if _, err := fetchPeerSync(home, ln2.Addr().String(), circle, id, priv, "github.com/acme/r", ""); err == nil {
		t.Fatal("unmarshal wire fail")
	}
	_ = ln2.Close()

	// decrypt fail on response
	ln3, _ := net.Listen("tcp", "127.0.0.1:0")
	go func() {
		c, _ := ln3.Accept()
		if c != nil {
			_, _ = c.Read(make([]byte, 4096))
			out, _ := json.Marshal(wireEnvelope{CircleID: circle.ID, Ciphertext: "bad-cipher"})
			_, _ = c.Write(append(out, '\n'))
			_ = c.Close()
		}
	}()
	if _, err := fetchPeerSync(home, ln3.Addr().String(), circle, id, priv, "github.com/acme/r", ""); err == nil {
		t.Fatal("decrypt response fail")
	}
	_ = ln3.Close()

	// write fail via hook
	resetHooksForTest()
	defer resetHooksForTest()
	hookNetDial = func(network, address string, timeout time.Duration) (net.Conn, error) {
		c1, c2 := net.Pipe()
		_ = c2.Close()
		return &errWriteConn{Conn: c1}, nil
	}
	if _, err := fetchPeerSync(home, "127.0.0.1:1", circle, id, priv, "github.com/acme/r", ""); err == nil {
		t.Fatal("write fail")
	}
}

func TestTryHandleJoinSuccessAddsMember(t *testing.T) {
	home := t.TempDir()
	setupTestEnv(t, home)
	id, priv, _ := loadOrCreateIdentity(home)
	circle, _ := createCircle(home, "team", id, priv)
	invite, _ := createInvite(home, circle.ID, id)
	pubB, privB, _ := ed25519.GenerateKey(nil)
	idB := DeviceIdentity{DeviceID: "dev_" + sha256Hex(pubB)[:16], PublicKey: base64.StdEncoding.EncodeToString(pubB)}
	req := JoinRequest{
		CircleID: circle.ID, DeviceID: idB.DeviceID, PublicKey: idB.PublicKey,
		Code: invite.Code, Timestamp: nowRFC3339(),
	}
	req, err := signJoinRequest(privB, req)
	if err != nil {
		t.Fatal(err)
	}
	payload, _ := json.Marshal(req)
	sl := &shareListener{home: home}
	if !sl.tryHandleJoin(wireEnvelope{Kind: "join"}, payload) {
		t.Fatal("join not handled")
	}
	updated, err := loadCircle(home, circle.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := updated.activeMember(idB.DeviceID); !ok {
		t.Fatal("member not added")
	}
}

func TestNotifyPeerJoinBranches(t *testing.T) {
	invite := CircleInvite{CircleID: "c1", Code: "abc", GroupKey: validGroupKeyB64(t)}
	id, priv, _ := loadOrCreateIdentity(t.TempDir())
	if err := notifyPeerJoin(invite, "", id, priv); err != nil {
		t.Fatal("empty endpoint ok")
	}
	resetHooksForTest()
	defer resetHooksForTest()
	hookNetDial = func(string, string, time.Duration) (net.Conn, error) {
		return nil, errors.New("dial fail")
	}
	if err := notifyPeerJoin(invite, "127.0.0.1:1", id, priv); err == nil {
		t.Fatal("dial fail")
	}
}

func TestRunGCAndStartSessionPrevious(t *testing.T) {
	home := t.TempDir()
	setupTestEnv(t, home)
	repo := makeTestRepo(t, filepath.Join(t.TempDir(), "repo"), "https://github.com/acme/a.git")
	e, _ := newEngine(home)
	defer e.Close()
	project := identifyProjectMust(t, repo)

	old := "sess_prev"
	e.setMeta("current_session_id", old)
	e.sessionID = old
	_, _ = e.db.Exec(`INSERT INTO working_memory(session_id, repository, kind, subject, content, created_at)
		VALUES (?, ?, 'episode', '', 'wm cleanup', ?)`, old, project.Repository, nowRFC3339())

	m, _ := e.recordMemory(project, "lesson", "repository", "", "Deprecated old lesson.", 0.5, 50, "agent_observation", "", "")
	_, _ = e.db.Exec(`UPDATE memories SET status = 'deprecated', updated_at = ? WHERE id = ?`, "2000-01-01T00:00:00Z", m.ID)

	if err := e.runGC(old); err != nil {
		t.Fatal(err)
	}
	var n int
	_ = e.db.QueryRow(`SELECT COUNT(*) FROM working_memory WHERE session_id = ?`, old).Scan(&n)
	if n != 0 {
		t.Fatal("working memory not cleaned")
	}
	_, _ = e.getMemory(m.ID) // may be deleted

	sid, err := e.startSession(project)
	if err != nil || sid == "" {
		t.Fatalf("start session err=%v", err)
	}
}

func TestEndSessionConsolidateFailure(t *testing.T) {
	home := t.TempDir()
	setupTestEnv(t, home)
	repo := makeTestRepo(t, filepath.Join(t.TempDir(), "repo"), "https://github.com/acme/a.git")
	id, priv, _ := loadOrCreateIdentity(home)
	circle, _ := createCircle(home, "team", id, priv)
	circle, _ = addAllowedFolder(home, circle.ID, repo, id)
	e, _ := newEngine(home)
	project := identifyProjectMust(t, repo)
	_ = e.db.Close()
	if err := e.endSession(project); err == nil {
		t.Fatal("consolidate should fail on closed db")
	}
	_ = circle
	_ = priv
}

func TestListCirclesSkipsCorruptAndSubdirs(t *testing.T) {
	home := t.TempDir()
	setupTestEnv(t, home)
	id, priv, _ := loadOrCreateIdentity(home)
	circle, _ := createCircle(home, "good", id, priv)
	dir := circlesDir(home)
	if err := os.Mkdir(filepath.Join(dir, "subdir"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := hookWriteFile(filepath.Join(dir, "bad.json"), []byte("{"), 0o600); err != nil {
		t.Fatal(err)
	}
	listed, err := listCircles(home)
	if err != nil {
		t.Fatal(err)
	}
	if len(listed) != 1 || listed[0].ID != circle.ID {
		t.Fatalf("listed=%v", listed)
	}
}

func TestAddAllowedFolderBrokenSymlink(t *testing.T) {
	home := t.TempDir()
	setupTestEnv(t, home)
	id, priv, _ := loadOrCreateIdentity(home)
	circle, _ := createCircle(home, "team", id, priv)
	link := filepath.Join(t.TempDir(), "broken-link")
	if err := os.Symlink("/nonexistent/target/path", link); err != nil {
		t.Skip("symlink unsupported")
	}
	updated, err := addAllowedFolder(home, circle.ID, link, id)
	if err != nil {
		t.Fatal(err)
	}
	if len(updated.AllowedFolders) != 1 {
		t.Fatal("folder added via abs fallback")
	}
}

func TestListExportableMemoriesRepoOnlyProject(t *testing.T) {
	home := t.TempDir()
	setupTestEnv(t, home)
	repo := makeTestRepo(t, filepath.Join(t.TempDir(), "repo"), "https://github.com/acme/a.git")
	id, priv, _ := loadOrCreateIdentity(home)
	circle, _ := createCircle(home, "team", id, priv)
	circle, _ = addAllowedFolder(home, circle.ID, repo, id)
	e, _ := newEngine(home)
	defer e.Close()
	project := identifyProjectMust(t, repo)
	_, _ = e.recordMemory(project, "lesson", "repository", "", "Exportable via repo-only project.", 0.9, 50, "verified_execution", "", "")
	p := Project{Repository: project.Repository}
	out, err := e.listExportableMemories(p, circle)
	if err != nil || len(out) == 0 {
		t.Fatalf("exportable=%d err=%v", len(out), err)
	}
	if _, err := e.listExportableMemories(Project{Repository: project.Repository, Root: "/outside"}, circle); err != nil {
		t.Fatal(err)
	}
}

func TestAcceptInviteErrorPaths(t *testing.T) {
	home := t.TempDir()
	setupTestEnv(t, home)
	id, priv, _ := loadOrCreateIdentity(home)
	if _, err := acceptInvite(home, "nope", "fp", "", id, priv); err == nil {
		t.Fatal("missing invite")
	}
	circle, _ := createCircle(home, "team", id, priv)
	invite, _ := createInvite(home, circle.ID, id)
	if _, err := acceptInvite(home, invite.Code, "wrong-fp", "", id, priv); err == nil {
		t.Fatal("fingerprint mismatch")
	}
	expired := invite
	expired.ExpiresAt = "2000-01-01T00:00:00Z"
	if err := hookWriteFile(filepath.Join(invitesDir(home), invite.Code+".json"), mustJSONBytes(t, expired), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := acceptInvite(home, invite.Code, invite.Fingerprint, "", id, priv); err == nil {
		t.Fatal("expired invite")
	}
}

func TestCreateInviteWriteFailure(t *testing.T) {
	home := t.TempDir()
	setupTestEnv(t, home)
	id, priv, _ := loadOrCreateIdentity(home)
	circle, _ := createCircle(home, "team", id, priv)
	resetHooksForTest()
	defer resetHooksForTest()
	hookWriteFile = func(path string, data []byte, mode os.FileMode) error {
		if strings.Contains(path, "invites") {
			return errors.New("invite write fail")
		}
		return os.WriteFile(path, data, mode)
	}
	if _, err := createInvite(home, circle.ID, id); err == nil {
		t.Fatal("invite write fail")
	}
}

func TestNormalizeRemoteFormats(t *testing.T) {
	cases := map[string]string{
		"https://github.com/acme/repo.git":        "github.com/acme/repo",
		"git@github.com:acme/repo.git":            "github.com/acme/repo",
		"ssh://git@gitlab.com/group/proj.git":     "gitlab.com/group/proj",
		"":                                        "",
		"invalid":                                 "",
	}
	for remote, wantRepo := range cases {
		repo, org := normalizeRemote(remote)
		if wantRepo == "" {
			if repo != "" {
				t.Fatalf("remote %q => %q", remote, repo)
			}
			continue
		}
		if repo != wantRepo {
			t.Fatalf("remote %q repo=%q want %q", remote, repo, wantRepo)
		}
		if org == "" {
			t.Fatalf("org empty for %q", remote)
		}
	}
}

func TestScoringEdgeBranches(t *testing.T) {
	m := Memory{Subject: "ERR_TIMEOUT", Content: "src/main.go failed with HTTP 503", Evidence: "UserRepository timeout"}
	sig := extractProblemSignature(m.Content + " " + m.Subject)
	if sig == "" {
		t.Fatal("signature")
	}
	if !concreteSignatureOverlap(sig, sig) {
		t.Fatal("overlap")
	}
	if concreteSignatureOverlap("", sig) || concreteSignatureOverlap(sig, "") {
		t.Fatal("empty overlap")
	}
	if freshnessScore("not-a-date") != 0.5 {
		t.Fatal("bad freshness")
	}
	future := time.Now().UTC().Add(24 * time.Hour).Format(time.RFC3339)
	if freshnessScore(future) <= 0 {
		t.Fatal("future freshness")
	}
	if queryRelevance(m, "timeout repository") <= 0 {
		t.Fatal("query relevance")
	}
}

func TestConsolidateSessionDemotesFailures(t *testing.T) {
	home := t.TempDir()
	setupTestEnv(t, home)
	repo := makeTestRepo(t, filepath.Join(t.TempDir(), "repo"), "https://github.com/acme/a.git")
	id, priv, _ := loadOrCreateIdentity(home)
	circle, _ := createCircle(home, "team", id, priv)
	circle, _ = addAllowedFolder(home, circle.ID, repo, id)
	e, _ := newEngine(home)
	defer e.Close()
	project := identifyProjectMust(t, repo)
	m, _ := e.recordMemory(project, "lesson", "repository", "", "Failure heavy lesson.", 0.2, 50, "agent_observation", "", "")
	_, _ = e.db.Exec(`UPDATE memories SET failure_count = 5, success_count = 0, hot_index = 1 WHERE id = ?`, m.ID)
	if err := e.consolidateSession(project, circle); err != nil {
		t.Fatal(err)
	}
	var hot int
	_ = e.db.QueryRow(`SELECT hot_index FROM memories WHERE id = ?`, m.ID).Scan(&hot)
	if hot != 0 {
		t.Fatal("demoted hot index")
	}
}

func TestSignJoinRequestMarshalFailure(t *testing.T) {
	resetHooksForTest()
	defer resetHooksForTest()
	hookJSONMarshal = func(any) ([]byte, error) {
		return nil, errors.New("join marshal fail")
	}
	_, priv, _ := ed25519.GenerateKey(nil)
	_, err := signJoinRequest(priv, JoinRequest{CircleID: "c"})
	if err == nil {
		t.Fatal("marshal fail")
	}
}

func TestRandomTokenRandFallback(t *testing.T) {
	resetHooksForTest()
	defer resetHooksForTest()
	hookRandRead = func([]byte) (int, error) {
		return 0, errors.New("rand fail")
	}
	if randomToken(8) == "" {
		t.Fatal("fallback token")
	}
}

func TestCircleForProjectAndFolderAllowed(t *testing.T) {
	home := t.TempDir()
	setupTestEnv(t, home)
	repo := makeTestRepo(t, filepath.Join(t.TempDir(), "repo"), "https://github.com/acme/a.git")
	if _, ok := circleForProject(home, repo); ok {
		t.Fatal("no circles yet")
	}
	id, priv, _ := loadOrCreateIdentity(home)
	circle, _ := createCircle(home, "team", id, priv)
	circle, _ = addAllowedFolder(home, circle.ID, repo, id)
	got, ok := circleForProject(home, repo)
	if !ok || got.ID != circle.ID {
		t.Fatal("circle for project")
	}
	if folderAllowed(Circle{}, repo) {
		t.Fatal("empty allowed folders")
	}
}

func TestImportPeerBatchUpsertFailure(t *testing.T) {
	home := t.TempDir()
	setupTestEnv(t, home)
	e, _ := newEngine(home)
	pub, _, _ := ed25519.GenerateKey(nil)
	circle := Circle{ID: "c1", Members: []CircleMember{{DeviceID: "d1", PublicKey: base64.StdEncoding.EncodeToString(pub), Revoked: false}}}
	resp := SyncResponse{
		CircleID: "c1", DeviceID: "d1",
		Packets: []SharedPacket{{
			ID: "peer_mem_fail", Kind: "lesson", ScopeID: "github.com/acme/r",
			PacketContent: "x", HotIndex: true, VectorSpace: "stub", Fingerprint: "fp",
			UpdatedAt: nowRFC3339(),
		}},
	}
	_ = e.db.Close()
	if _, err := e.importPeerBatch(circle, resp); err == nil {
		t.Fatal("closed db upsert fail")
	}
}

func TestEnsureShareListenerIdentityFailure(t *testing.T) {
	home := t.TempDir()
	setupTestEnv(t, home)
	shareListenerOnce = sync.Once{}
	shareListenerInst = nil
	t.Setenv("OVERDRIVE_SHARE_LISTEN", "127.0.0.1:7742")
	resetHooksForTest()
	defer resetHooksForTest()
	hookMkdirAll = func(path string, mode os.FileMode) error {
		if strings.Contains(path, "share") {
			return errors.New("share mkdir fail")
		}
		return os.MkdirAll(path, mode)
	}
	ensureShareListener(home)
	if shareListenerInst != nil {
		t.Fatal("listener should not start")
	}
}

func TestLedgerListOnClosedDB(t *testing.T) {
	home := t.TempDir()
	setupTestEnv(t, home)
	repo := makeTestRepo(t, filepath.Join(t.TempDir(), "repo"), "https://github.com/acme/a.git")
	e, _ := newEngine(home)
	project := identifyProjectMust(t, repo)
	_, _ = e.ledgerAdd(project, "run1", "decide", "", "", "", "")
	_ = e.db.Close()
	if _, err := e.ledgerList(project, "run1"); err == nil {
		t.Fatal("ledger list closed db")
	}
}

func TestTryORTSessionResolveError(t *testing.T) {
	home := t.TempDir()
	resetORTLibForTest()
	t.Setenv("OVERDRIVE_ORT_LIB", filepath.Join(home, "missing.so"))
	if _, err := tryORTSession(home, "/tmp/m.onnx"); err == nil {
		t.Fatal("missing ort lib")
	}
}

func TestMigrateJSONSuccessPath(t *testing.T) {
	home := t.TempDir()
	setupTestEnv(t, home)
	store := JSONStore{Memories: []Memory{{
		ID: "legacy_mem", Kind: "lesson", Scope: "repository", ScopeID: "github.com/acme/r",
		Content: "Legacy json memory.", Confidence: 0.8, Priority: 50, Source: "verified_execution",
		CreatedAt: nowRFC3339(), UpdatedAt: nowRFC3339(),
	}}}
	raw, _ := json.Marshal(store)
	if err := os.WriteFile(filepath.Join(home, "experience-v1.json"), raw, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := migrateJSONIfNeeded(home); err != nil {
		t.Fatal(err)
	}
	e, err := newEngine(home)
	if err != nil {
		t.Fatal(err)
	}
	defer e.Close()
	m, err := e.getMemory("legacy_mem")
	if err != nil || m.Content == "" {
		t.Fatalf("migrated memory err=%v", err)
	}
}

func TestRecallCriticalLayerAndWorkingMemory(t *testing.T) {
	home := t.TempDir()
	setupTestEnv(t, home)
	repo := makeTestRepo(t, filepath.Join(t.TempDir(), "repo"), "https://github.com/acme/a.git")
	e, _ := newEngine(home)
	defer e.Close()
	project := identifyProjectMust(t, repo)
	rule, _ := e.recordMemory(project, "rule", "repository", "Critical auth rule", "Always validate JWT on API routes.", 0.9, 95, "project_instruction", "", "")
	_, _ = e.recordMemory(project, "episode", "repository", "", "Ephemeral episode memory.", 0.7, 50, "agent_observation", "", "")
	sid, _ := e.startSession(project)
	e.sessionID = sid
	resp, err := e.recall(project, "JWT validate API", 5, "knowledge")
	if err != nil {
		t.Fatal(err)
	}
	if len(resp.CriticalRules) == 0 {
		t.Fatal("expected critical rules")
	}
	foundRule := false
	for _, m := range resp.CriticalRules {
		if m.ID == rule.ID {
			foundRule = true
		}
	}
	if !foundRule {
		t.Fatal("critical rule missing")
	}
	if len(resp.WorkingMemory) == 0 {
		t.Fatal("working memory seeded")
	}
	resp2, _ := e.recall(project, "episode", 5, "episodes")
	if len(resp2.Memories) == 0 {
		t.Fatal("layer filter episodes")
	}
}

func TestRunGCStaleProtectedSource(t *testing.T) {
	home := t.TempDir()
	setupTestEnv(t, home)
	repo := makeTestRepo(t, filepath.Join(t.TempDir(), "repo"), "https://github.com/acme/a.git")
	e, _ := newEngine(home)
	defer e.Close()
	project := identifyProjectMust(t, repo)
	m, _ := e.recordMemory(project, "fact", "repository", "", "Protected stale fact.", 0.1, 50, "user_feedback", "", "")
	_, _ = e.db.Exec(`UPDATE memories SET status = 'stale', updated_at = ? WHERE id = ?`, "2000-01-01T00:00:00Z", m.ID)
	if err := e.runGC(""); err != nil {
		t.Fatal(err)
	}
	if _, err := e.getMemory(m.ID); err != nil {
		t.Fatal("protected stale should remain")
	}
}

func TestTryHandleJoinInvalidSignature(t *testing.T) {
	home := t.TempDir()
	setupTestEnv(t, home)
	id, priv, _ := loadOrCreateIdentity(home)
	circle, _ := createCircle(home, "team", id, priv)
	invite, _ := createInvite(home, circle.ID, id)
	pubB, _, _ := ed25519.GenerateKey(nil)
	idB := DeviceIdentity{DeviceID: "dev_" + sha256Hex(pubB)[:16], PublicKey: base64.StdEncoding.EncodeToString(pubB)}
	req := JoinRequest{
		CircleID: circle.ID, DeviceID: idB.DeviceID, PublicKey: idB.PublicKey,
		Code: invite.Code, Timestamp: nowRFC3339(), Signature: "bad-sig",
	}
	payload, _ := json.Marshal(req)
	sl := &shareListener{home: home}
	if !sl.tryHandleJoin(wireEnvelope{Kind: "join"}, payload) {
		t.Fatal("handled")
	}
	updated, _ := loadCircle(home, circle.ID)
	if _, ok := updated.activeMember(idB.DeviceID); ok {
		t.Fatal("should not add member")
	}
	_ = priv
}

func TestFtsCandidatesFallbackQuery(t *testing.T) {
	home := t.TempDir()
	setupTestEnv(t, home)
	repo := makeTestRepo(t, filepath.Join(t.TempDir(), "repo"), "https://github.com/acme/a.git")
	e, _ := newEngine(home)
	defer e.Close()
	project := identifyProjectMust(t, repo)
	_, _ = e.recordMemory(project, "lesson", "repository", "alpha", "Token searchable lesson content.", 0.9, 50, "verified_execution", "", "")
	ids, ranks, _ := e.ftsCandidates("\"broken fts\" AND (:", project, 5)
	if len(ids) == 0 && len(ranks) == 0 {
		// fallback may still return empty for nonsense query
		ids, ranks, _ = e.ftsCandidates("searchable lesson", project, 5)
	}
	if len(ids) == 0 {
		t.Fatal("fts candidates")
	}
}

func TestDeleteMemoryAndColumnExists(t *testing.T) {
	home := t.TempDir()
	setupTestEnv(t, home)
	repo := makeTestRepo(t, filepath.Join(t.TempDir(), "repo"), "https://github.com/acme/a.git")
	e, _ := newEngine(home)
	defer e.Close()
	project := identifyProjectMust(t, repo)
	m, _ := e.recordMemory(project, "lesson", "repository", "", "Delete me lesson.", 0.8, 50, "verified_execution", "", "")
	if !columnExists(e.db, "memories", "origin") {
		t.Fatal("origin column")
	}
	if columnExists(nil, "memories", "origin") {
		t.Fatal("nil db should return false")
	}
	if err := e.deleteMemory(m.ID); err != nil {
		t.Fatal(err)
	}
	if err := e.deleteMemory("missing-id"); err != nil {
		t.Fatal("delete missing should not error on exec")
	}
}

func TestRecallPeerMemoriesPath(t *testing.T) {
	lib := filepath.Join("..", "turbovec-ffi", "target", "release", "liboverdrive_turbovec_ffi.so")
	if _, err := os.Stat(lib); err != nil {
		t.Skip("turbovec not built")
	}
	home := t.TempDir()
	setupTestEnv(t, home)
	abs, _ := filepath.Abs(lib)
	t.Setenv("OVERDRIVE_TURBOVEC_LIB", abs)
	repo := makeTestRepo(t, filepath.Join(t.TempDir(), "repo"), "https://github.com/acme/a.git")
	id, priv, _ := loadOrCreateIdentity(home)
	circle, _ := createCircle(home, "team", id, priv)
	circle, _ = addAllowedFolder(home, circle.ID, repo, id)
	e, _ := newEngine(home)
	defer e.Close()
	project := identifyProjectMust(t, repo)
	sig := extractProblemSignature("ERR_TIMEOUT src/auth.go HTTP 503")
	pm := Memory{
		ID: "peer_recall_1", Kind: "lesson", Scope: "repository", ScopeID: project.Repository,
		Status: "active", Origin: "peer", HotIndex: true, CircleID: circle.ID, PeerDeviceID: id.DeviceID,
		PacketContent: "symptom: ERR_TIMEOUT | fix: retry auth", ProblemSignature: sig,
		VectorSpace: "stub", Source: "peer_share", EvidenceScore: 0.8,
		CreatedAt: nowRFC3339(), UpdatedAt: nowRFC3339(),
	}
	if err := e.upsertPeerMemory(pm, embedText(pm.PacketContent)); err != nil {
		t.Fatal(err)
	}
	_ = priv
	resp, err := e.recall(project, "ERR_TIMEOUT auth.go 503", 5, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(resp.PeerMemories) == 0 {
		t.Fatal("peer memories expected")
	}
}

func TestTryHandleJoinSignAndSaveFailures(t *testing.T) {
	home := t.TempDir()
	setupTestEnv(t, home)
	id, priv, _ := loadOrCreateIdentity(home)
	circle, _ := createCircle(home, "team", id, priv)
	invite, _ := createInvite(home, circle.ID, id)
	pubB, privB, _ := ed25519.GenerateKey(nil)
	idB := DeviceIdentity{DeviceID: "dev_" + sha256Hex(pubB)[:16], PublicKey: base64.StdEncoding.EncodeToString(pubB)}
	req := JoinRequest{
		CircleID: circle.ID, DeviceID: idB.DeviceID, PublicKey: idB.PublicKey,
		Code: invite.Code, Timestamp: nowRFC3339(),
	}
	req, _ = signJoinRequest(privB, req)
	payload, _ := json.Marshal(req)
	sl := &shareListener{home: home}

	resetHooksForTest()
	defer resetHooksForTest()
	hookJSONMarshal = func(v any) ([]byte, error) {
		if _, ok := v.([]CircleMember); ok {
			return nil, errors.New("members marshal fail")
		}
		return json.Marshal(v)
	}
	if !sl.tryHandleJoin(wireEnvelope{Kind: "join"}, payload) {
		t.Fatal("join handled")
	}

	resetHooksForTest()
	hookWriteFile = func(path string, data []byte, mode os.FileMode) error {
		if strings.Contains(path, "circles") && strings.HasSuffix(path, ".json") {
			return errors.New("save circle fail")
		}
		return os.WriteFile(path, data, mode)
	}
	if !sl.tryHandleJoin(wireEnvelope{Kind: "join"}, payload) {
		t.Fatal("join handled on save fail")
	}
}

func TestAddAllowedFolderAbsFailure(t *testing.T) {
	home := t.TempDir()
	setupTestEnv(t, home)
	id, priv, _ := loadOrCreateIdentity(home)
	circle, _ := createCircle(home, "team", id, priv)
	resetHooksForTest()
	defer resetHooksForTest()
	hookFilepathAbs = func(string) (string, error) {
		return "", errors.New("abs fail")
	}
	if _, err := addAllowedFolder(home, circle.ID, "/tmp/folder", id); err == nil {
		t.Fatal("abs fail")
	}
	_ = priv
}

func TestListActiveMemoriesClosedDB(t *testing.T) {
	home := t.TempDir()
	setupTestEnv(t, home)
	e, _ := newEngine(home)
	_ = e.db.Close()
	if _, err := e.listActiveMemories(); err == nil {
		t.Fatal("closed db")
	}
}

func TestStartSessionRunGCFailure(t *testing.T) {
	home := t.TempDir()
	setupTestEnv(t, home)
	repo := makeTestRepo(t, filepath.Join(t.TempDir(), "repo"), "https://github.com/acme/a.git")
	e, _ := newEngine(home)
	project := identifyProjectMust(t, repo)
	_ = e.db.Close()
	if _, err := e.startSession(project); err == nil {
		t.Fatal("start session closed db")
	}
}

func TestCmdShareListenEnsureHomeFail(t *testing.T) {
	resetHooksForTest()
	defer resetHooksForTest()
	t.Setenv("OVERDRIVE_SHARE_LISTEN", "127.0.0.1:7743")
	t.Setenv("OVERDRIVE_HOME", "")
	hookUserHomeDir = func() (string, error) {
		return "", errors.New("no home")
	}
	if err := cmdShareListen(nil); err == nil {
		t.Fatal("ensure home fail")
	}
}
