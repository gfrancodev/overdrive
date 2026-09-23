package main

import (
	"bufio"
	"crypto/ed25519"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unsafe"
)

func hookFail(t *testing.T) {
	t.Helper()
	resetHooksForTest()
}

func withJSONMarshalFail(t *testing.T, fn func()) {
	t.Helper()
	hookJSONMarshal = func(any) ([]byte, error) {
		return nil, errors.New("json marshal fail")
	}
	hookJSONMarshalIndent = func(any, string, string) ([]byte, error) {
		return nil, errors.New("json marshal indent fail")
	}
	defer resetHooksForTest()
	fn()
}

func validGroupKeyB64(t *testing.T) string {
	t.Helper()
	key := make([]byte, 32)
	for i := range key {
		key[i] = byte(i + 1)
	}
	return base64.StdEncoding.EncodeToString(key)
}

func TestAtomicWriteFileHookErrors(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "nested", "out.txt")
	hookFail(t)
	defer resetHooksForTest()

	hookMkdirAll = func(string, os.FileMode) error {
		return errors.New("mkdir fail")
	}
	if err := atomicWriteFile(path, strings.NewReader("x"), 0o644); err == nil {
		t.Fatal("mkdir fail")
	}
	resetHooksForTest()

	hookCreateTemp = func(string, string) (writeCloser, error) {
		return nil, errors.New("create temp fail")
	}
	if err := atomicWriteFile(path, strings.NewReader("x"), 0o644); err == nil {
		t.Fatal("create temp fail")
	}
	resetHooksForTest()

	hookIOCopy = func(io.Writer, io.Reader) (int64, error) {
		return 0, errors.New("copy fail")
	}
	if err := atomicWriteFile(path, strings.NewReader("x"), 0o644); err == nil {
		t.Fatal("copy fail")
	}
	resetHooksForTest()

	hookChmod = func(string, os.FileMode) error {
		return errors.New("chmod fail")
	}
	if err := atomicWriteFile(path, strings.NewReader("x"), 0o644); err == nil {
		t.Fatal("chmod fail")
	}
	resetHooksForTest()

	hookRename = func(string, string) error {
		return errors.New("rename fail")
	}
	if err := atomicWriteFile(path, strings.NewReader("x"), 0o644); err == nil {
		t.Fatal("rename fail")
	}
}

func TestEnsureHomeHookErrors(t *testing.T) {
	hookFail(t)
	defer resetHooksForTest()

	t.Setenv("OVERDRIVE_HOME", "")
	hookUserHomeDir = func() (string, error) {
		return "", errors.New("user home fail")
	}
	if _, err := ensureHome(); err == nil {
		t.Fatal("user home fail")
	}
	resetHooksForTest()

	t.Setenv("OVERDRIVE_HOME", "/tmp/overdrive-test-home")
	hookFilepathAbs = func(string) (string, error) {
		return "", errors.New("abs fail")
	}
	if _, err := ensureHome(); err == nil {
		t.Fatal("abs fail")
	}
	resetHooksForTest()

	t.Setenv("OVERDRIVE_HOME", t.TempDir())
	hookMkdirAll = func(path string, _ os.FileMode) error {
		if strings.Contains(path, "lib") {
			return errors.New("lib mkdir fail")
		}
		return os.MkdirAll(path, 0o700)
	}
	if _, err := ensureHome(); err == nil {
		t.Fatal("mkdir fail")
	}
}

func TestRandReadHookErrors(t *testing.T) {
	hookFail(t)
	defer resetHooksForTest()

	hookRandRead = func([]byte) (int, error) {
		return 0, errors.New("rand fail")
	}
	if _, err := encryptBatch(validGroupKeyB64(t), []byte("plain")); err == nil {
		t.Fatal("encryptBatch rand fail")
	}
	if tok := randomToken(8); tok == "" {
		t.Fatal("randomToken empty")
	}
	home := t.TempDir()
	t.Setenv("OVERDRIVE_HOME", home)
	t.Setenv("OVERDRIVE_EMBEDDER", "stub")
	t.Setenv("OVERDRIVE_SKIP_EMBED_DOWNLOAD", "1")
	hookRandRead = func([]byte) (int, error) {
		return 0, errors.New("rand fail")
	}
	id, priv, _ := loadOrCreateIdentity(home)
	if _, err := createCircle(home, "team", id, priv); err == nil {
		t.Fatal("createCircle rand fail")
	}
}

func TestJSONMarshalHookErrors(t *testing.T) {
	pub, priv, _ := ed25519.GenerateKey(nil)
	req := SyncRequest{DeviceID: "dev_a", CircleID: "circle_x", Repository: "github.com/acme/r", Timestamp: nowRFC3339()}
	resp := SyncResponse{DeviceID: "dev_b", CircleID: "circle_x", Repository: "github.com/acme/r", Cursor: nowRFC3339()}
	join := JoinRequest{CircleID: "circle_x", DeviceID: "dev_b", PublicKey: base64.StdEncoding.EncodeToString(pub), Code: "abc", Timestamp: nowRFC3339()}

	withJSONMarshalFail(t, func() {
		if _, err := signSyncRequest(priv, req); err == nil {
			t.Fatal("signSyncRequest")
		}
		if verifySyncRequest(pub, req) {
			t.Fatal("verifySyncRequest should fail closed")
		}
		if _, err := signSyncResponse(priv, resp); err == nil {
			t.Fatal("signSyncResponse")
		}
		if verifySyncResponse(pub, resp) {
			t.Fatal("verifySyncResponse")
		}
		if _, err := signJoinRequest(priv, join); err == nil {
			t.Fatal("signJoinRequest")
		}
		if verifyJoinRequest(pub, join) {
			t.Fatal("verifyJoinRequest")
		}
	})

	home := t.TempDir()
	setupTestEnv(t, home)
	id, priv, _ := loadOrCreateIdentity(home)
	circle, err := createCircle(home, "team", id, priv)
	if err != nil {
		t.Fatal(err)
	}

	withJSONMarshalFail(t, func() {
		circle.MemberListSignature = ""
		if err := circle.signMemberList(priv); err == nil {
			t.Fatal("signMemberList")
		}
		if err := saveCircle(home, circle); err == nil {
			t.Fatal("saveCircle")
		}
		if _, err := createInvite(home, circle.ID, id); err == nil {
			t.Fatal("createInvite")
		}
	})

	e, err := newEngine(home)
	if err != nil {
		t.Fatal(err)
	}
	defer e.Close()
	withJSONMarshalFail(t, func() {
		e.recordShareSyncReport(circle.ID, "github.com/acme/r", shareSyncReport{Imported: 1})
		if e.getMeta("share_last_sync:"+circle.ID+":github.com/acme/r") != "" {
			t.Fatal("recordShareSyncReport should no-op on marshal error")
		}
	})

	withJSONMarshalFail(t, func() {
		_, err := fetchPeerSync(home, "127.0.0.1:1", circle, id, priv, "github.com/acme/r", "")
		if err == nil {
			t.Fatal("fetchPeerSync marshal req")
		}
	})
}

func TestORTReleaseStatusAndAPIFn(t *testing.T) {
	oldAPI := ortAPIFuncAt
	oldSyscall := ortSyscallN
	oldRelease := ortReleaseStatusFn
	defer func() {
		ortAPIFuncAt = oldAPI
		ortSyscallN = oldSyscall
		ortReleaseStatusFn = oldRelease
	}()

	if apiFn(0, 3) != 0 {
		t.Fatal("zero api")
	}

	var releaseCalled bool
	ortAPIFuncAt = func(api uintptr, idx int) uintptr {
		if api == 42 && idx == ortIdxReleaseStatus {
			return 99
		}
		return 0
	}
	if apiFn(42, ortIdxReleaseStatus) != 99 {
		t.Fatal("non-zero apiFn")
	}

	ortSyscallN = func(trap uintptr, args ...uintptr) (uintptr, uintptr, uintptr) {
		if trap == 99 && len(args) == 1 && args[0] == 7 {
			releaseCalled = true
		}
		return 0, 0, 0
	}
	ortReleaseStatusFn = releaseORTStatus
	releaseORTStatus(42, 7)
	if !releaseCalled {
		t.Fatal("releaseORTStatus syscall")
	}

	releaseCalled = false
	releaseORTStatus(0, 7)
	if releaseCalled {
		t.Fatal("zero api should skip release")
	}
}

func TestCryptoEdgeCases(t *testing.T) {
	pub, _ := decodePublicKey(base64.StdEncoding.EncodeToString(make([]byte, ed25519.PublicKeySize)))
	if verifySignature(pub, []byte("payload"), "!!!not-b64!!!") {
		t.Fatal("bad base64 sig")
	}
	if verifySignature(pub, []byte("payload"), base64.StdEncoding.EncodeToString(make([]byte, ed25519.SignatureSize))) {
		t.Fatal("invalid signature bytes")
	}

	groupKey := validGroupKeyB64(t)
	if _, err := encryptBatch(base64.StdEncoding.EncodeToString([]byte{1, 2, 3}) /* 3 bytes */, []byte("x")); err == nil {
		t.Fatal("invalid group key length")
	}
	if _, err := decryptBatch("!!!", "cipher"); err == nil {
		t.Fatal("bad group key b64")
	}
	if _, err := decryptBatch(groupKey, "!!!"); err == nil {
		t.Fatal("bad ciphertext b64")
	}
	if _, err := decryptBatch(groupKey, base64.StdEncoding.EncodeToString([]byte{1})); err == nil {
		t.Fatal("ciphertext too short")
	}
	cipher, err := encryptBatch(groupKey, []byte("secret"))
	if err != nil {
		t.Fatal(err)
	}
	wrongKey := base64.StdEncoding.EncodeToString(bytesRepeat(0xff, 32))
	if _, err := decryptBatch(wrongKey, cipher); err == nil {
		t.Fatal("gcm open should fail with wrong key")
	}
}

func bytesRepeat(b byte, n int) []byte {
	out := make([]byte, n)
	for i := range out {
		out[i] = b
	}
	return out
}

func TestUtilHelpersCoverage(t *testing.T) {
	if maxInt(2, 5) != 5 || maxInt(9, 3) != 9 {
		t.Fatal("maxInt")
	}
	if clampInt(-3, 0, 10) != 0 || clampInt(99, 0, 10) != 10 || clampInt(5, 0, 10) != 5 {
		t.Fatal("clampInt")
	}

	homeDir := t.TempDir()
	t.Setenv("OVERDRIVE_HOME", homeDir)
	got, err := overdriveHome()
	if err != nil || got != homeDir {
		t.Fatalf("overdriveHome set: %q err=%v", got, err)
	}

	hookFail(t)
	defer resetHooksForTest()
	t.Setenv("OVERDRIVE_HOME", "")
	hookUserHomeDir = func() (string, error) {
		return homeDir, nil
	}
	got, err = overdriveHome()
	if err != nil || got != filepath.Join(homeDir, ".overdrive") {
		t.Fatalf("overdriveHome unset: %q err=%v", got, err)
	}

	normalizeMemoryScope(nil)
	m := &Memory{Scope: "repository", ScopeID: ""}
	normalizeMemoryScope(m)
	if m.ScopeID != "" {
		t.Fatal("non-global empty scope_id unchanged")
	}
	m = &Memory{Scope: "global", ScopeID: ""}
	normalizeMemoryScope(m)
	if m.ScopeID != "*" {
		t.Fatal("global scope normalized")
	}
}

func TestShareCircleEdgeCases(t *testing.T) {
	home := t.TempDir()
	setupTestEnv(t, home)
	id, priv, _ := loadOrCreateIdentity(home)
	pubB, privB, _ := ed25519.GenerateKey(nil)
	idB := DeviceIdentity{DeviceID: "dev_" + sha256Hex(pubB)[:16], PublicKey: base64.StdEncoding.EncodeToString(pubB)}

	circle, _ := createCircle(home, "team", id, priv)
	circle.Members = append(circle.Members, CircleMember{
		DeviceID: idB.DeviceID, PublicKey: idB.PublicKey, Revoked: true,
	})
	if _, ok := circle.activeMember(idB.DeviceID); ok {
		t.Fatal("revoked activeMember")
	}
	if circle.isActiveMemberPubKey(idB.PublicKey) {
		t.Fatal("revoked pubkey")
	}
	if circle.isActiveMemberPubKey(id.PublicKey) {
		// creator active
	} else {
		t.Fatal("creator should be active")
	}

	root := t.TempDir()
	parent := filepath.Join(root, "allowed")
	child := filepath.Join(parent, "nested")
	if err := os.MkdirAll(child, 0o755); err != nil {
		t.Fatal(err)
	}
	c := Circle{AllowedFolders: []string{parent}}
	if !folderAllowed(c, child) {
		t.Fatal("prefix folder allowed")
	}
	if folderAllowed(Circle{}, child) {
		t.Fatal("empty allowed folders")
	}
	if folderAllowed(c, filepath.Join(root, "other")) {
		t.Fatal("outside prefix blocked")
	}

	linkRoot := filepath.Join(t.TempDir(), "linkcase")
	real := filepath.Join(linkRoot, "real")
	if err := os.MkdirAll(real, 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(linkRoot, "link")
	if err := os.Symlink(real, link); err != nil {
		t.Skip("symlink not supported")
	}
	cLink := Circle{AllowedFolders: []string{real}}
	if !folderAllowed(cLink, link) {
		t.Fatal("symlink project root should match allowed real path")
	}

	missing := filepath.Join(t.TempDir(), "does-not-exist-yet")
	if _, err := addAllowedFolder(home, circle.ID, missing, id); err != nil {
		t.Fatal(err)
	}

	if _, err := revokeMember(home, "missing", idB.DeviceID, id, priv); err == nil {
		t.Fatal("missing circle revoke")
	}
	if _, err := revokeMember(home, circle.ID, "missing-device", idB, privB); err == nil {
		t.Fatal("non-creator revoke")
	}
	if _, err := revokeMember(home, circle.ID, "missing-device", id, priv); err == nil {
		t.Fatal("member not found revoke")
	}

	withJSONMarshalFail(t, func() {
		if _, err := revokeMember(home, circle.ID, idB.DeviceID, id, priv); err == nil {
			t.Fatal("revoke signMemberList fail")
		}
	})

	homeJoin := t.TempDir()
	setupTestEnv(t, homeJoin)
	idJ, privJ, _ := loadOrCreateIdentity(homeJoin)
	invite := CircleInvite{
		CircleID: "circle_new", CircleName: "remote", Code: "join01", Fingerprint: "fp1234567890",
		GroupKey: validGroupKeyB64(t), ExpiresAt: time.Now().UTC().Add(time.Hour).Format(time.RFC3339),
		CreatorPK: id.PublicKey, CreatorID: id.DeviceID,
	}
	if err := os.MkdirAll(invitesDir(homeJoin), 0o700); err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(invite)
	if err := os.WriteFile(filepath.Join(invitesDir(homeJoin), invite.Code+".json"), raw, 0o600); err != nil {
		t.Fatal(err)
	}
	accepted, err := acceptInvite(homeJoin, invite.Code, invite.Fingerprint, "", idJ, privJ)
	if err != nil {
		t.Fatal(err)
	}
	if accepted.ID != invite.CircleID {
		t.Fatal("new circle file path")
	}
	if _, err := loadCircle(homeJoin, invite.CircleID); err != nil {
		t.Fatal("circle saved locally")
	}
}

func TestPeerEligibleAllBranches(t *testing.T) {
	home := t.TempDir()
	setupTestEnv(t, home)
	repo := makeTestRepo(t, filepath.Join(t.TempDir(), "repo"), "https://github.com/acme/r.git")
	id, priv, _ := loadOrCreateIdentity(home)
	circle, _ := createCircle(home, "team", id, priv)
	circle, _ = addAllowedFolder(home, circle.ID, repo, id)
	e, _ := newEngine(home)
	defer e.Close()
	project, _ := identifyProject(repo)

	base := Memory{
		ID: "mem_peer_base", Kind: "lesson", Scope: "repository", ScopeID: project.Repository,
		Status: "active", Origin: "peer", HotIndex: true, CircleID: circle.ID, PeerDeviceID: id.DeviceID,
		Content: "peer lesson", VectorSpace: "stub", Layer: 2,
	}

	cases := []struct {
		name string
		m    Memory
		ok   bool
	}{
		{"valid", base, true},
		{"wrong status", func() Memory { m := base; m.Status = "stale"; return m }(), false},
		{"not peer", func() Memory { m := base; m.Origin = "local"; return m }(), false},
		{"not hot", func() Memory { m := base; m.HotIndex = false; return m }(), false},
		{"wrong scope", func() Memory { m := base; m.ScopeID = "other"; return m }(), false},
		{"missing circle", func() Memory { m := base; m.CircleID = "missing"; return m }(), false},
	}
	for _, tc := range cases {
		if got := e.peerEligible(tc.m, project); got != tc.ok {
			t.Fatalf("%s: got %v want %v", tc.name, got, tc.ok)
		}
	}

	noFolder := Circle{ID: circle.ID, Members: circle.Members, AllowedFolders: []string{"/tmp/not-repo"}}
	_ = saveCircle(home, noFolder)
	if e.peerEligible(base, project) {
		t.Fatal("folder not allowed")
	}
	circle, _ = addAllowedFolder(home, circle.ID, repo, id)
	pub, _, _ := ed25519.GenerateKey(nil)
	revoked := DeviceIdentity{DeviceID: "dev_" + sha256Hex(pub)[:16], PublicKey: base64.StdEncoding.EncodeToString(pub)}
	revMem := base
	revMem.PeerDeviceID = revoked.DeviceID
	circle.Members = append(circle.Members, CircleMember{DeviceID: revoked.DeviceID, PublicKey: revoked.PublicKey, Revoked: true})
	_ = saveCircle(home, circle)
	if e.peerEligible(revMem, project) {
		t.Fatal("revoked member")
	}

	if e.peerRecallEnabled(project) {
		// allowed folder restored
	} else {
		t.Fatal("peerRecallEnabled")
	}
	if e.peerRecallEnabled(Project{Root: t.TempDir(), Repository: "github.com/acme/other"}) {
		t.Fatal("peerRecallEnabled without circle")
	}
}

func TestDeleteMemoryWithTurboVec(t *testing.T) {
	lib := filepath.Join("..", "turbovec-ffi", "target", "release", "liboverdrive_turbovec_ffi.so")
	if _, err := os.Stat(lib); err != nil {
		t.Skip("turbovec not built")
	}
	home := t.TempDir()
	setupTestEnv(t, home)
	abs, _ := filepath.Abs(lib)
	t.Setenv("OVERDRIVE_TURBOVEC_LIB", abs)
	repo := makeTestRepo(t, filepath.Join(t.TempDir(), "repo"), "https://github.com/acme/a.git")
	e, err := newEngine(home)
	if err != nil {
		t.Fatal(err)
	}
	defer e.Close()
	project, _ := identifyProject(repo)
	local, _ := e.recordMemory(project, "lesson", "repository", "", "Local delete turbovec.", 0.9, 50, "verified_execution", "", "")
	peer := Memory{
		ID: "mem_peer_del", Kind: "lesson", Scope: "repository", ScopeID: project.Repository,
		Status: "active", Origin: "peer", HotIndex: true, CircleID: "c1", PeerDeviceID: "dev_x",
		Content: "Peer delete turbovec.", VectorSpace: "stub", Layer: 2,
		CreatedAt: nowRFC3339(), UpdatedAt: nowRFC3339(),
	}
	if err := e.upsertPeerMemory(peer, embedText(peer.Content)); err != nil {
		t.Fatal(err)
	}
	if err := e.deleteMemory(local.ID); err != nil {
		t.Fatal(err)
	}
	if err := e.deleteMemory(peer.ID); err != nil {
		t.Fatal(err)
	}
}

func TestRunGCWorkingMemoryCleanup(t *testing.T) {
	home := t.TempDir()
	setupTestEnv(t, home)
	repo := makeTestRepo(t, filepath.Join(t.TempDir(), "repo"), "https://github.com/acme/a.git")
	e, _ := newEngine(home)
	defer e.Close()
	project, _ := identifyProject(repo)
	s1, _ := e.startSession(project)
	_ = e.addWorkingMemory(project, "episode", "subj", "working body")
	if _, err := e.startSession(project); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := e.db.QueryRow(`SELECT COUNT(*) FROM working_memory WHERE session_id = ?`, s1).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("working_memory not cleaned count=%d", count)
	}
}

func TestMigrateJSONIfNeededErrors(t *testing.T) {
	home := t.TempDir()
	setupTestEnv(t, home)
	jsonFile := filepath.Join(home, "experience-v1.json")
	if err := os.Mkdir(jsonFile, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := migrateJSONIfNeeded(home); err == nil {
		t.Fatal("read error on directory json path")
	}
	_ = os.Remove(jsonFile)
	if err := os.WriteFile(jsonFile, []byte("{bad json"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := migrateJSONIfNeeded(home); err == nil {
		t.Fatal("invalid json migrate")
	}
}

func TestOpenEngineWithoutMigrateAndLedgerMarkdown(t *testing.T) {
	home := t.TempDir()
	setupTestEnv(t, home)
	e, err := openEngineWithoutMigrate(home)
	if err != nil {
		t.Fatal(err)
	}
	e.Close()

	repo := makeTestRepo(t, filepath.Join(t.TempDir(), "repo"), "https://github.com/acme/a.git")
	e2, _ := newEngine(home)
	defer e2.Close()
	project, _ := identifyProject(repo)
	entry, err := e2.ledgerAdd(project, "run-md", "Decision only", "", "", "", "")
	if err != nil || entry.Decision == "" {
		t.Fatalf("ledger decision only err=%v", err)
	}
	_, err = e2.ledgerAdd(project, "run-md", "Full entry", "evidence", "reason", "risk", "easy")
	if err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(filepath.Join(home, "runs", "run-md", "ledger.md"))
	if err != nil {
		t.Fatal(err)
	}
	text := string(body)
	for _, want := range []string{"Decision only", "Evidence:", "Reason:", "Risk if wrong:", "Reversibility:"} {
		if !strings.Contains(text, want) {
			t.Fatalf("ledger.md missing %q in %q", want, text)
		}
	}
}

func TestValidateMemoryAllHandlers(t *testing.T) {
	home := t.TempDir()
	setupTestEnv(t, home)
	repo := makeTestRepo(t, filepath.Join(t.TempDir(), "repo"), "https://github.com/acme/a.git")
	e, _ := newEngine(home)
	defer e.Close()
	project, _ := identifyProject(repo)
	m, _ := e.recordMemory(project, "lesson", "repository", "", "Validate handlers lesson.", 0.8, 50, "verified_execution", "", "")

	out, err := e.validateMemory(m.ID, "success", "", "")
	if err != nil || out.SuccessCount < 1 {
		t.Fatalf("success err=%v", err)
	}
	out, err = e.validateMemory(m.ID, "failure", "", "")
	if err != nil || out.FailureCount < 1 {
		t.Fatalf("failure err=%v", err)
	}
	out, err = e.validateMemory(m.ID, "contradiction", "winner", "mem_next")
	if err != nil || out.Status != "deprecated" {
		t.Fatalf("contradiction err=%v status=%s", err, out.Status)
	}
	if _, err := e.validateMemory(m.ID, "bogus", "", ""); err == nil {
		t.Fatal("invalid result")
	}
}

func TestRecallVectorScoresWithTurboVec(t *testing.T) {
	lib := filepath.Join("..", "turbovec-ffi", "target", "release", "liboverdrive_turbovec_ffi.so")
	if _, err := os.Stat(lib); err != nil {
		t.Skip("turbovec not built")
	}
	home := t.TempDir()
	setupTestEnv(t, home)
	abs, _ := filepath.Abs(lib)
	t.Setenv("OVERDRIVE_TURBOVEC_LIB", abs)
	repo := makeTestRepo(t, filepath.Join(t.TempDir(), "repo"), "https://github.com/acme/a.git")
	e, _ := newEngine(home)
	defer e.Close()
	project, _ := identifyProject(repo)
	_, _ = e.recordMemory(project, "lesson", "repository", "", "Alpha vector marker for turbovec recall.", 0.95, 50, "verified_execution", "", "")
	_, _ = e.recordMemory(project, "lesson", "repository", "", "Unrelated zebra taxonomy content.", 0.95, 50, "verified_execution", "", "")
	resp, err := e.recall(project, "alpha vector marker", 5, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(resp.Memories) == 0 {
		t.Fatal("expected vector-ranked recall")
	}
	if !strings.Contains(resp.Memories[0].Content, "Alpha vector") {
		t.Fatalf("top hit %q", resp.Memories[0].Content)
	}
}

func TestTokenizerEdgeCases(t *testing.T) {
	if meanPoolL2([]float32{1, 2, 3, 4}, []int64{0, 0}, 2, 2) != nil {
		t.Fatal("meanPoolL2 count==0")
	}
	if _, err := loadWordPieceTokenizer(filepath.Join(t.TempDir(), "missing-vocab.txt")); err == nil {
		t.Fatal("missing vocab file")
	}
	dir := t.TempDir()
	vocab := filepath.Join(dir, "vocab.txt")
	content := "[CLS]\n[SEP]\nhello\nunk\n"
	if err := os.WriteFile(vocab, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	tok, err := loadWordPieceTokenizer(vocab)
	if err != nil {
		t.Fatal(err)
	}
	ids := tok.wordPiece("zzzzunknown")
	if len(ids) != 1 || ids[0] != tok.unk {
		t.Fatal("unk token")
	}
	parts := tok.basicTokenize("hello, world! 42")
	if len(parts) < 3 {
		t.Fatalf("basicTokenize punctuation: %v", parts)
	}
}

type errModelRunner struct{}

func (errModelRunner) Run([]int64, []int64, []int64) ([]float32, error) {
	return nil, errors.New("runner error")
}

type shortHiddenRunner struct{}

func (shortHiddenRunner) Run(inputIDs, _, _ []int64) ([]float32, error) {
	return make([]float32, 4), nil
}

func TestMiniLMEmbedderErrorPaths(t *testing.T) {
	dir := t.TempDir()
	vocab := filepath.Join(dir, "vocab.txt")
	if err := os.WriteFile(vocab, []byte("[CLS]\n[SEP]\nhello\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	tok, err := loadWordPieceTokenizer(vocab)
	if err != nil {
		t.Fatal(err)
	}
	emb := &miniLMEmbedder{tok: tok, ort: errModelRunner{}}
	vec := emb.Embed("hello")
	if len(vec) != hashedDims {
		t.Fatal("runner error fail-open")
	}
	if meanPoolL2(make([]float32, 128*miniLMDims), make([]int64, 128), 128, miniLMDims) != nil {
		t.Fatal("zero mask meanPoolL2")
	}
	emb.ort = shortHiddenRunner{}
	vec = emb.Embed("hello")
	if len(vec) != hashedDims {
		t.Fatal("nil pooled fail-open")
	}
}

func startMockPeerSyncServer(t *testing.T, circle Circle, priv ed25519.PrivateKey, resp SyncResponse) (endpoint string, stop func()) {
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
		line, err := reader.ReadString('\n')
		if err != nil {
			return
		}
		var env wireEnvelope
		if json.Unmarshal([]byte(strings.TrimSpace(line)), &env) != nil {
			return
		}
		if _, err := decryptBatch(circle.GroupKey, env.Ciphertext); err != nil {
			return
		}
		signed, err := signSyncResponse(priv, resp)
		if err != nil {
			return
		}
		payload, err := json.Marshal(signed)
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

func TestFetchPeerSyncErrors(t *testing.T) {
	home := t.TempDir()
	setupTestEnv(t, home)
	idA, privA, _ := loadOrCreateIdentity(home)
	pubB, privB, _ := ed25519.GenerateKey(nil)
	idBDev := DeviceIdentity{
		DeviceID:  "dev_" + sha256Hex(pubB)[:16],
		PublicKey: base64.StdEncoding.EncodeToString(pubB),
	}
	circle, _ := createCircle(home, "team", idA, privA)
	circle.Members = append(circle.Members, CircleMember{
		DeviceID: idBDev.DeviceID, PublicKey: idBDev.PublicKey, Revoked: false,
	})
	_ = saveCircle(home, circle)

	resp := SyncResponse{
		DeviceID: idA.DeviceID, CircleID: circle.ID, Repository: "github.com/acme/payments-api", Cursor: nowRFC3339(),
	}
	endpoint, stop := startMockPeerSyncServer(t, circle, privA, resp)
	defer stop()

	clientCircle := circle
	clientCircle.Members = []CircleMember{{DeviceID: idBDev.DeviceID, PublicKey: idBDev.PublicKey, Revoked: false}}
	if _, err := fetchPeerSync(home, endpoint, clientCircle, idBDev, privB, "github.com/acme/payments-api", ""); err == nil || !strings.Contains(err.Error(), "unknown peer device") {
		t.Fatalf("unknown peer device err=%v", err)
	}

	endpoint2, stop2 := startMockPeerSyncServer(t, circle, privA, resp)
	defer stop2()
	pubX, _, _ := ed25519.GenerateKey(nil)
	tampered := circle
	tampered.Members = []CircleMember{{DeviceID: idA.DeviceID, PublicKey: base64.StdEncoding.EncodeToString(pubX), Revoked: false}}
	if _, err := fetchPeerSync(home, endpoint2, tampered, idBDev, privB, "github.com/acme/payments-api", ""); err == nil || !strings.Contains(err.Error(), "invalid peer signature") {
		t.Fatalf("invalid signature err=%v", err)
	}
}

func TestHandleConnValidSyncRoundtrip(t *testing.T) {
	homeA := t.TempDir()
	homeB := t.TempDir()
	repo := makeTestRepo(t, filepath.Join(t.TempDir(), "repo"), "https://github.com/acme/sync.git")
	setupTestEnv(t, homeA)
	idA, privA, _ := loadOrCreateIdentity(homeA)
	circle, _ := createCircle(homeA, "team", idA, privA)
	circle, _ = addAllowedFolder(homeA, circle.ID, repo, idA)
	sl, endpoint := startShareListenerForTest(t, homeA)
	defer sl.Close()

	setupTestEnv(t, homeB)
	idB, privB, _ := loadOrCreateIdentity(homeB)
	invite, _ := createInvite(homeA, circle.ID, idA)
	_ = os.MkdirAll(invitesDir(homeB), 0o700)
	raw, _ := json.Marshal(invite)
	_ = os.WriteFile(filepath.Join(invitesDir(homeB), invite.Code+".json"), raw, 0o600)
	circleB, _ := acceptInvite(homeB, invite.Code, invite.Fingerprint, endpoint, idB, privB)
	circleB, _ = addAllowedFolder(homeB, circleB.ID, repo, idB)

	eA, _ := newEngine(homeA)
	defer eA.Close()
	project, _ := identifyProject(repo)
	_, _ = eA.recordMemory(project, "lesson", "repository", "", "HandleConn sync lesson.", 0.95, 50, "verified_execution", "", "")

	resp, err := fetchPeerSync(homeB, endpoint, circleB, idB, privB, project.Repository, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(resp.Packets) == 0 {
		t.Fatal("expected sync packets")
	}
}

func TestHandleConnJSONMarshalFailure(t *testing.T) {
	home := t.TempDir()
	setupTestEnv(t, home)
	id, priv, _ := loadOrCreateIdentity(home)
	circle, _ := createCircle(home, "solo", id, priv)
	repo := makeTestRepo(t, filepath.Join(t.TempDir(), "repo"), "https://github.com/acme/a.git")
	circle, _ = addAllowedFolder(home, circle.ID, repo, id)
	e, _ := newEngine(home)
	defer e.Close()
	project, _ := identifyProject(repo)
	_, _ = e.recordMemory(project, "lesson", "repository", "", "Marshal fail path.", 0.9, 50, "verified_execution", "", "")

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	sl := &shareListener{ln: ln, home: home, id: id, stop: make(chan struct{})}
	go sl.serve(priv)
	endpoint := ln.Addr().String()

	req := SyncRequest{
		DeviceID: id.DeviceID, CircleID: circle.ID, Repository: project.Repository, Timestamp: nowRFC3339(),
	}
	req, err = signSyncRequest(priv, req)
	if err != nil {
		t.Fatal(err)
	}
	payload, err := json.Marshal(req)
	if err != nil {
		t.Fatal(err)
	}
	cipher, err := encryptBatch(circle.GroupKey, payload)
	if err != nil {
		t.Fatal(err)
	}
	line, err := json.Marshal(wireEnvelope{CircleID: circle.ID, Ciphertext: cipher})
	if err != nil {
		t.Fatal(err)
	}

	hookJSONMarshal = func(any) ([]byte, error) {
		return nil, errors.New("resp marshal fail")
	}
	defer resetHooksForTest()

	conn, err := net.Dial("tcp", endpoint)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	_, _ = conn.Write(append(line, '\n'))
	reader := bufio.NewReader(conn)
	if _, err := reader.ReadString('\n'); err == nil {
		t.Fatal("expected no response on marshal failure")
	}
}

func TestCLIMissingRequiredFlags(t *testing.T) {
	home := t.TempDir()
	setupTestEnv(t, home)
	repo := makeTestRepo(t, filepath.Join(t.TempDir(), "repo"), "https://github.com/acme/a.git")

	type check struct {
		args   []string
		substr string
	}
	checks := []check{
		{[]string{"share"}, "usage"},
		{[]string{"share", "circle", "revoke"}, "--circle and --device"},
		{[]string{"share", "circle", "folder-add"}, "--circle and --folder"},
		{[]string{"share", "circle", "accept"}, "--code and --fingerprint"},
		{[]string{"share", "circle", "create"}, "--name"},
		{[]string{"share", "circle", "invite"}, "--circle"},
		{[]string{"share", "circle", "folder-list"}, "--circle"},
		{[]string{"validate"}, "--id and --result"},
		{[]string{"ledger-add", "--cwd", repo}, "--run and --decision"},
		{[]string{"ledger-list", "--cwd", repo}, "--run is required"},
		{[]string{"record", "--cwd", repo, "--kind", "bogus", "--content", "x"}, "invalid --kind"},
		{[]string{"record", "--cwd", repo, "--scope", "bogus", "--content", "x"}, "invalid --scope"},
	}
	for _, c := range checks {
		_, stderr, code := runCLICapture(t, c.args)
		if code != 2 || !strings.Contains(stderr, c.substr) {
			t.Fatalf("args=%v code=%d stderr=%q want %q", c.args, code, stderr, c.substr)
		}
	}
}

func TestNotifyPeerJoinMarshalErrors(t *testing.T) {
	home := t.TempDir()
	setupTestEnv(t, home)
	id, priv, _ := loadOrCreateIdentity(home)
	circle, _ := createCircle(home, "team", id, priv)
	invite, _ := createInvite(home, circle.ID, id)
	withJSONMarshalFail(t, func() {
		if err := notifyPeerJoin(invite, "127.0.0.1:1", id, priv); err == nil {
			t.Fatal("notifyPeerJoin marshal fail")
		}
	})
}

func TestSaveCircleMkdirFailure(t *testing.T) {
	home := t.TempDir()
	setupTestEnv(t, home)
	id, priv, _ := loadOrCreateIdentity(home)
	circle, _ := createCircle(home, "team", id, priv)
	hookMkdirAll = func(string, os.FileMode) error {
		return errors.New("mkdir fail")
	}
	defer resetHooksForTest()
	if err := saveCircle(home, circle); err == nil {
		t.Fatal("saveCircle mkdir fail")
	}
}

func TestCreateInviteMkdirFailure(t *testing.T) {
	home := t.TempDir()
	setupTestEnv(t, home)
	id, priv, _ := loadOrCreateIdentity(home)
	circle, _ := createCircle(home, "team", id, priv)
	hookMkdirAll = func(path string, _ os.FileMode) error {
		if strings.Contains(path, "invites") {
			return errors.New("invite mkdir fail")
		}
		return os.MkdirAll(path, 0o700)
	}
	defer resetHooksForTest()
	if _, err := createInvite(home, circle.ID, id); err == nil {
		t.Fatal("createInvite mkdir fail")
	}
}

func TestFetchPeerSyncRequestMarshalFailure(t *testing.T) {
	home := t.TempDir()
	setupTestEnv(t, home)
	id, priv, _ := loadOrCreateIdentity(home)
	circle, _ := createCircle(home, "solo", id, priv)
	withJSONMarshalFail(t, func() {
		_, err := fetchPeerSync(home, "127.0.0.1:9", circle, id, priv, "github.com/acme/r", "")
		if err == nil {
			t.Fatal("fetchPeerSync request marshal")
		}
	})
}

func TestIdentityJSONMarshalIndentFailure(t *testing.T) {
	home := t.TempDir()
	setupTestEnv(t, home)
	_ = os.RemoveAll(shareDir(home))
	withJSONMarshalFail(t, func() {
		if _, _, err := loadOrCreateIdentity(home); err == nil {
			t.Fatal("identity marshal indent fail")
		}
	})
}

func TestOrtStatusErrorReleasesViaHook(t *testing.T) {
	called := false
	oldRelease := ortReleaseStatusFn
	ortReleaseStatusFn = func(api, status uintptr) {
		called = api == 5 && status == 9
	}
	defer func() { ortReleaseStatusFn = oldRelease }()
	if ortStatusError(5, 9) == nil || !called {
		t.Fatal("ortStatusError release hook")
	}
}

func TestDecryptBatchInvalidKeySize(t *testing.T) {
	shortKey := base64.StdEncoding.EncodeToString([]byte{1, 2, 3, 4})
	cipher, err := encryptBatch(validGroupKeyB64(t), []byte("x"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := decryptBatch(shortKey, cipher); err == nil {
		t.Fatal("aes new cipher should fail on short key")
	}
}

func TestPeerEligibleWrongScopeAndHot(t *testing.T) {
	home := t.TempDir()
	setupTestEnv(t, home)
	e, _ := newEngine(home)
	defer e.Close()
	project := Project{Repository: "github.com/acme/r", Root: t.TempDir()}
	m := Memory{Status: "active", Origin: "peer", HotIndex: false, ScopeID: project.Repository, CircleID: "c"}
	if e.peerEligible(m, project) {
		t.Fatal("not hot")
	}
}

func TestRunGCDirectCall(t *testing.T) {
	home := t.TempDir()
	setupTestEnv(t, home)
	e, _ := newEngine(home)
	defer e.Close()
	if err := e.runGC("sess_prev"); err != nil {
		t.Fatal(err)
	}
}

func TestShareTransportSignPaths(t *testing.T) {
	pub, priv, _ := ed25519.GenerateKey(nil)
	req := SyncRequest{DeviceID: "dev_a", CircleID: "c", Repository: "r", Timestamp: nowRFC3339()}
	withJSONMarshalFail(t, func() {
		if verifySyncRequest(pub, req) {
			t.Fatal("verify sync request marshal fail")
		}
	})
	resp := SyncResponse{DeviceID: "dev_b", CircleID: "c", Repository: "r", Cursor: nowRFC3339()}
	withJSONMarshalFail(t, func() {
		if verifySyncResponse(pub, resp) {
			t.Fatal("verify sync response marshal fail")
		}
	})
	join := JoinRequest{CircleID: "c", DeviceID: "dev_b", PublicKey: base64.StdEncoding.EncodeToString(pub), Timestamp: nowRFC3339()}
	withJSONMarshalFail(t, func() {
		if verifyJoinRequest(pub, join) {
			t.Fatal("verify join marshal fail")
		}
	})
	_ = priv
}

func TestHandleConnInvalidMemberSync(t *testing.T) {
	home := t.TempDir()
	setupTestEnv(t, home)
	id, priv, _ := loadOrCreateIdentity(home)
	circle, _ := createCircle(home, "solo", id, priv)
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	sl := &shareListener{ln: ln, home: home, id: id, stop: make(chan struct{})}
	go sl.serve(priv)

	pubX, _, _ := ed25519.GenerateKey(nil)
	req := SyncRequest{
		DeviceID: "dev_" + sha256Hex(pubX)[:16], CircleID: circle.ID, Repository: "github.com/acme/r", Timestamp: nowRFC3339(),
	}
	req.Signature = base64.StdEncoding.EncodeToString(make([]byte, ed25519.SignatureSize))
	payload, _ := json.Marshal(req)
	cipher, _ := encryptBatch(circle.GroupKey, payload)
	line, _ := json.Marshal(wireEnvelope{CircleID: circle.ID, Ciphertext: cipher})
	conn, err := net.Dial("tcp", ln.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	_, _ = conn.Write(append(line, '\n'))
	time.Sleep(30 * time.Millisecond)
}

func TestOverdriveHomeAbsFailure(t *testing.T) {
	hookFail(t)
	defer resetHooksForTest()
	t.Setenv("OVERDRIVE_HOME", "relative/path")
	hookFilepathAbs = func(string) (string, error) {
		return "", errors.New("abs fail")
	}
	if _, err := overdriveHome(); err == nil {
		t.Fatal("overdriveHome abs fail")
	}
}

func TestRandomTokenFallback(t *testing.T) {
	hookRandRead = func([]byte) (int, error) {
		return 0, errors.New("rand fail")
	}
	defer resetHooksForTest()
	tok := randomToken(6)
	if tok == "" {
		t.Fatal("fallback token")
	}
	if _, err := fmt.Sscanf(tok, "%d", new(int)); err != nil {
		t.Fatalf("expected numeric fallback got %q", tok)
	}
}

type closeFailWriter struct {
	*os.File
	fail bool
}

func (w *closeFailWriter) Close() error {
	if w.fail {
		return errors.New("close fail")
	}
	return w.File.Close()
}

func TestAtomicWriteFileCloseFailure(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "out.bin")
	f, err := os.CreateTemp(dir, "t-*")
	if err != nil {
		t.Fatal(err)
	}
	hookCreateTemp = func(string, string) (writeCloser, error) {
		return &closeFailWriter{File: f, fail: true}, nil
	}
	defer resetHooksForTest()
	if err := atomicWriteFile(path, strings.NewReader("data"), 0o644); err == nil {
		t.Fatal("close fail expected")
	}
}

func TestOrtAPIFuncUnsafeReadsIndex(t *testing.T) {
	slots := make([]uintptr, ortIdxReleaseStatus+1)
	slots[ortIdxReleaseStatus] = 99
	api := uintptr(unsafe.Pointer(&slots[0]))
	if got := ortAPIFuncUnsafe(api, ortIdxReleaseStatus); got != 99 {
		t.Fatalf("api slot %d", got)
	}
	if ortAPIFuncUnsafe(0, 1) != 0 {
		t.Fatal("zero api")
	}
}

func TestNewEngineDBOpenFailure(t *testing.T) {
	hookDBOpen = func(string, string) (*sql.DB, error) {
		return nil, errors.New("db open fail")
	}
	defer resetHooksForTest()
	if _, err := newEngine(t.TempDir()); err == nil {
		t.Fatal("db open fail")
	}
	if _, err := openEngineWithoutMigrate(t.TempDir()); err == nil {
		t.Fatal("open without migrate fail")
	}
}

func TestListCirclesReadDirFailure(t *testing.T) {
	home := t.TempDir()
	if err := hookMkdirAll(circlesDir(home), 0o700); err != nil {
		t.Fatal(err)
	}
	hookReadDir = func(string) ([]os.DirEntry, error) {
		return nil, errors.New("readdir fail")
	}
	defer resetHooksForTest()
	if _, err := listCircles(home); err == nil {
		t.Fatal("readdir fail")
	}
}

func TestWordPieceSubTokenAndUnk(t *testing.T) {
	dir := t.TempDir()
	vocab := filepath.Join(dir, "vocab.txt")
	content := "[CLS]\n[SEP]\nhel\n##lo\nunk\n"
	if err := os.WriteFile(vocab, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	tok, err := loadWordPieceTokenizer(vocab)
	if err != nil {
		t.Fatal(err)
	}
	if ids := tok.wordPiece("hello"); len(ids) != 2 {
		t.Fatalf("subtoken split ids=%v", ids)
	}
	if ids := tok.wordPiece("xyzzyunknown"); len(ids) != 1 || ids[0] != tok.unk {
		t.Fatalf("unk path ids=%v unk=%d", ids, tok.unk)
	}
}

func TestCLIQuietSessionFlags(t *testing.T) {
	home := t.TempDir()
	setupTestEnv(t, home)
	repo := makeTestRepo(t, filepath.Join(t.TempDir(), "repo"), "https://github.com/acme/a.git")
	if _, _, code := runCLICapture(t, []string{"session-start", "--cwd", repo, "--quiet"}); code != 0 {
		t.Fatal(code)
	}
	if _, _, code := runCLICapture(t, []string{"session-end", "--cwd", repo, "--quiet"}); code != 0 {
		t.Fatal(code)
	}
	if _, _, code := runCLICapture(t, []string{"version"}); code != 0 {
		t.Fatal(code)
	}
	if _, _, code := runCLICapture(t, []string{"-v"}); code != 0 {
		t.Fatal(code)
	}
}
