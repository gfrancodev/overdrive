package main

import (
	"crypto/ed25519"
	"database/sql"
	"encoding/base64"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
	"unsafe"
)

func TestValidateORTTokenTensors(t *testing.T) {
	if err := validateORTTokenTensors([]int64{1}, []int64{1}, []int64{1}); err != nil {
		t.Fatal(err)
	}
	if err := validateORTTokenTensors(nil, nil, nil); err == nil {
		t.Fatal("empty tensors")
	}
	if err := validateORTTokenTensors([]int64{1, 2}, []int64{1}, []int64{1, 2}); err == nil {
		t.Fatal("length mismatch")
	}
}

func TestORTTensorHelpers(t *testing.T) {
	shape := ortInt64TensorShape(12)
	if len(shape) != 2 || shape[0] != 1 || shape[1] != 12 {
		t.Fatalf("shape %v", shape)
	}
	if ortHiddenOutputFloatCount(4) != 4*miniLMDims {
		t.Fatal("hidden count")
	}
	out := copyFloat32FromUnsafe(0, 3)
	if len(out) != 3 {
		t.Fatal("zero ptr copy")
	}
	data := []float32{1.5, 2.5, 3.5}
	ptr := uintptr(unsafe.Pointer(&data[0]))
	copied := copyFloat32FromUnsafe(ptr, len(data))
	if copied[0] != 1.5 || copied[2] != 3.5 {
		t.Fatalf("copy %v", copied)
	}
	if len(copyFloat32FromUnsafe(ptr, 0)) != 0 {
		t.Fatal("zero length copy")
	}
}

func TestMatchesORTZipEntry(t *testing.T) {
	if !matchesORTZipEntry("dir/onnxruntime.dll") {
		t.Fatal("dll match")
	}
	if matchesORTZipEntry("dir/libonnxruntime.so") {
		t.Fatal("so mismatch")
	}
}

func TestResolveORTAPIErrors(t *testing.T) {
	if _, err := resolveORTAPI(nil, 18); err == nil {
		t.Fatal("nil getApiBase")
	}
	if _, err := resolveORTAPI(func() uintptr { return 0 }, 18); err == nil {
		t.Fatal("nil base")
	}
	if readORTGetAPIAddr(0) != 0 {
		t.Fatal("zero base addr")
	}
	var emptyBase uintptr
	if _, err := resolveORTAPI(func() uintptr { return uintptr(unsafe.Pointer(&emptyBase)) }, 18); err == nil {
		t.Fatal("getApi missing")
	}
	old := ortSyscallN
	ortSyscallN = func(trap uintptr, args ...uintptr) (uintptr, uintptr, uintptr) {
		return 0, 0, 0
	}
	defer func() { ortSyscallN = old }()
	getApi := uintptr(0x1234)
	baseMem := getApi
	if _, err := resolveORTAPI(func() uintptr { return uintptr(unsafe.Pointer(&baseMem)) }, 18); err == nil {
		t.Fatal("unsupported api version")
	}
}

func TestBindORTSessionTensorNames(t *testing.T) {
	s := &ortSession{}
	if err := bindORTSessionTensorNames(s); err != nil {
		t.Fatal(err)
	}
	if s.inputNames[0] == nil || s.outputName == nil {
		t.Fatal("tensor names")
	}
	old := hookBytePtrFromString
	hookBytePtrFromString = func(string) (*byte, error) {
		return nil, errors.New("cstring fail")
	}
	defer func() { hookBytePtrFromString = old }()
	if err := bindORTSessionTensorNames(&ortSession{}); err == nil {
		t.Fatal("bind fail")
	}
}

func TestJoinLogicHelpers(t *testing.T) {
	home := t.TempDir()
	setupTestEnv(t, home)
	id, priv, _ := loadOrCreateIdentity(home)
	circle, _ := createCircle(home, "team", id, priv)
	invite, _ := createInvite(home, circle.ID, id)

	if _, err := parseJoinRequest([]byte("{")); err == nil {
		t.Fatal("bad json")
	}
	if _, err := loadJoinInvite(home, "missing"); err == nil {
		t.Fatal("missing invite")
	}
	if !joinInviteValid(invite, time.Now()) {
		t.Fatal("valid invite")
	}
	if joinInviteValid(CircleInvite{ExpiresAt: "2000-01-01T00:00:00Z"}, time.Now()) {
		t.Fatal("expired invite")
	}
	if joinInviteValid(CircleInvite{ExpiresAt: "not-rfc3339"}, time.Now()) {
		t.Fatal("invalid expires format")
	}
	if err := hookWriteFile(filepath.Join(invitesDir(home), "bad.json"), []byte("{"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := loadJoinInvite(home, "bad"); err == nil {
		t.Fatal("bad invite json")
	}

	pubB, privB, _ := ed25519.GenerateKey(nil)
	idB := DeviceIdentity{DeviceID: "dev_x", PublicKey: base64.StdEncoding.EncodeToString(pubB)}
	req := JoinRequest{CircleID: circle.ID, DeviceID: idB.DeviceID, PublicKey: idB.PublicKey, Code: invite.Code, Timestamp: nowRFC3339()}
	req, _ = signJoinRequest(privB, req)
	updated := circleWithJoinMember(circle, req)
	if _, ok := updated.activeMember(idB.DeviceID); !ok {
		t.Fatal("member added")
	}
	dup := circleWithJoinMember(updated, req)
	if len(dup.Members) != len(updated.Members) {
		t.Fatal("duplicate member")
	}
}

func TestLedgerLogicHelpers(t *testing.T) {
	home := t.TempDir()
	setupTestEnv(t, home)
	repo := makeTestRepo(t, filepath.Join(t.TempDir(), "repo"), "https://github.com/acme/a.git")
	e, _ := newEngine(home)
	defer e.Close()
	project, _ := identifyProject(repo)
	entry, err := insertLedgerEntry(e.db, project, "run-a", "decide", "ev", "reason", "risk", "easy")
	if err != nil || entry.Decision != "decide" {
		t.Fatalf("insert err=%v", err)
	}
	md := renderLedgerMarkdown([]LedgerEntry{entry})
	for _, want := range []string{"# Decision Ledger", "Evidence:", "Risk if wrong:"} {
		if !strings.Contains(md, want) {
			t.Fatalf("missing %q in %q", want, md)
		}
	}
}

func TestORTLibLogicHelpers(t *testing.T) {
	home := t.TempDir()
	t.Setenv("OVERDRIVE_ORT_LIB", "")
	if path, err := resolveORTLibFromEnv(); err != nil || path != "" {
		t.Fatalf("empty env path=%q err=%v", path, err)
	}
	t.Setenv("OVERDRIVE_ORT_LIB", filepath.Join(home, "missing.so"))
	if _, err := resolveORTLibFromEnv(); err == nil {
		t.Fatal("missing lib")
	}
	lib := filepath.Join(home, "libonnxruntime.so")
	if err := os.WriteFile(lib, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("OVERDRIVE_ORT_LIB", lib)
	if path, err := resolveORTLibFromEnv(); err != nil || path != lib {
		t.Fatalf("lib=%q err=%v", path, err)
	}
	if _, ok := bundledORTLibPath(home); ok {
		t.Fatal("bundled missing")
	}
	if err := os.MkdirAll(libDir(home), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(libDir(home), ortLibName()), []byte("lib"), 0o644); err != nil {
		t.Fatal(err)
	}
	path, ok := bundledORTLibPath(home)
	if !ok || path == "" {
		t.Fatal("bundled present")
	}
	t.Setenv("OVERDRIVE_SKIP_EMBED_DOWNLOAD", "1")
	if _, err := ortReleaseURL(); err == nil {
		t.Fatal("download disabled")
	}
	t.Setenv("OVERDRIVE_SKIP_EMBED_DOWNLOAD", "0")
	t.Setenv("OVERDRIVE_ORT_URL", "https://example.com/ort.tgz")
	if url, err := ortReleaseURL(); err != nil || url == "" {
		t.Fatalf("custom url %q err=%v", url, err)
	}
	if ortArchiveFilename("https://x/ort.zip") != "ort-archive.zip" {
		t.Fatal("zip name")
	}
	if ortArchiveFilename("https://x/ort.tgz") != "ort-archive.tgz" {
		t.Fatal("tgz name")
	}
}

func TestGCLogicHelpers(t *testing.T) {
	now := time.Date(2025, 6, 1, 0, 0, 0, 0, time.UTC)
	if !gcProtectedSource("user_feedback") {
		t.Fatal("protected")
	}
	if gcProtectedSource("agent_observation") {
		t.Fatal("not protected")
	}
	if !shouldDeleteDeprecatedMemory("2000-01-01T00:00:00Z", gcDeprecatedCutoff(now)) {
		t.Fatal("deprecated delete")
	}
	if shouldDeleteStaleMemory("user_feedback", "2000-01-01T00:00:00Z", gcStaleCutoff(now), 0.1) {
		t.Fatal("protected stale")
	}
	if !shouldDeleteStaleMemory("agent_observation", "2000-01-01T00:00:00Z", gcStaleCutoff(now), 0.1) {
		t.Fatal("stale delete")
	}
}

func TestSignatureTokenHelpers(t *testing.T) {
	tokens := collectSignatureTokens("ERR_TIMEOUT src/main.go UserRepository HTTP 503")
	if len(tokens) == 0 {
		t.Fatal("tokens")
	}
	sig := joinSignatureTokens(tokens, 2)
	if sig == "" {
		t.Fatal("joined empty")
	}
	if extractProblemSignature("ERR_TIMEOUT src/main.go") == "" {
		t.Fatal("extract")
	}
}

func TestBuildMiniLMEmbedderErrors(t *testing.T) {
	home := t.TempDir()
	resetORTLibForTest()
	t.Setenv("OVERDRIVE_ORT_LIB", filepath.Join(home, "missing.so"))
	if _, err := buildMiniLMEmbedder(home); err == nil {
		t.Fatal("missing ort")
	}
}

func TestInsertLedgerEntryDBError(t *testing.T) {
	db, _ := sql.Open("sqlite", ":memory:")
	_ = db.Close()
	if _, err := insertLedgerEntry(db, Project{Repository: "r"}, "run", "d", "", "", "", ""); err == nil {
		t.Fatal("closed db")
	}
}

func TestPersistJoinedCircleFailure(t *testing.T) {
	home := t.TempDir()
	setupTestEnv(t, home)
	id, priv, _ := loadOrCreateIdentity(home)
	circle, _ := createCircle(home, "team", id, priv)
	resetHooksForTest()
	defer resetHooksForTest()
	hookMkdirAll = func(string, os.FileMode) error {
		return errors.New("mkdir fail")
	}
	if err := persistJoinedCircle(home, circle); err == nil {
		t.Fatal("persist fail")
	}
}

func TestReleaseORTValues(t *testing.T) {
	called := 0
	old := ortSyscallN
	ortSyscallN = func(trap uintptr, args ...uintptr) (uintptr, uintptr, uintptr) {
		called++
		return 0, 0, 0
	}
	defer func() { ortSyscallN = old }()
	releaseORTValues([]uintptr{1, 0, 2}, 99)
	if called != 2 {
		t.Fatalf("released %d", called)
	}
	releaseORTValues([]uintptr{1}, 0)
}

func TestRecallLogicHelpers(t *testing.T) {
	project := Project{Repository: "acme/repo", Root: "/tmp/repo"}
	memories := []Memory{
		{ID: "local1", Status: "active", Kind: "lesson", HotIndex: true, Scope: "repository", ScopeID: "acme/repo"},
		{ID: "peer1", Status: "active", Kind: "lesson", HotIndex: true, Origin: "peer", Scope: "repository", ScopeID: "acme/repo"},
		{ID: "inactive", Status: "deprecated", Kind: "lesson", HotIndex: true, Scope: "repository", ScopeID: "acme/repo"},
	}
	pools := partitionRecallMemories(memories, project, "", func(m Memory) bool { return m.ID == "peer1" })
	if len(pools.local) != 1 || len(pools.peer) != 1 {
		t.Fatalf("pools local=%d peer=%d", len(pools.local), len(pools.peer))
	}

	allow := recallLocalAllowIDs([]string{"local1", "missing"}, pools.local)
	if len(allow) != 1 {
		t.Fatalf("fts allow %v", allow)
	}
	allowAll := recallLocalAllowIDs(nil, pools.local)
	if len(allowAll) != 1 {
		t.Fatalf("fallback allow %v", allowAll)
	}

	scores := mapVectorScoresToIDs([]uint64{memoryVectorID("local1")}, []float32{0.5}, pools.local)
	if scores["local1"] <= 0 {
		t.Fatalf("mapped score %v", scores)
	}

	floor := 0.2
	ftsRanks := map[string]float64{"local1": 5}
	regular, critical := buildLocalRecallResults(pools.local, "query", ftsRanks, scores, project, floor)
	if len(regular) == 0 {
		t.Fatal("expected regular results")
	}

	critMem := Memory{ID: "crit", Status: "active", Kind: "rule", HotIndex: true, Scope: "repository", ScopeID: "acme/repo", Priority: 95, Confidence: 0.9, Source: "user_feedback"}
	critPools := partitionRecallMemories([]Memory{critMem}, project, "", nil)
	_, critical = buildLocalRecallResults(critPools.local, "", nil, nil, project, floor)
	if len(critical) != 1 || critical[0].Score < 10 {
		t.Fatalf("critical %v", critical)
	}

	if shouldIncludeLocalRecall(regular[0], "totally unrelated xyz", map[string]float64{}, 0.01, floor) {
		t.Fatal("low relevance should exclude")
	}
	if !shouldIncludeLocalRecall(regular[0], "totally unrelated xyz", ftsRanks, 0.01, floor) {
		t.Fatal("fts rank should rescue low relevance")
	}
	if !shouldIncludeLocalRecall(regular[0], "query", ftsRanks, 0, floor) {
		t.Fatal("fts boost should include")
	}
	if !shouldIncludeLocalRecall(regular[0], "query", ftsRanks, 0.01, floor) {
		t.Fatal("fts rank should rescue low vector score")
	}
	if shouldIncludeLocalRecall(regular[0], "query", map[string]float64{}, 0.05, floor) {
		t.Fatal("low vector without fts should exclude")
	}
	if shouldIncludeLocalRecall(regular[0], "", map[string]float64{}, 0.05, floor) {
		t.Fatal("empty query low vector without fts should exclude")
	}
	if ftsBoostedVectorScore("local1", 0, ftsRanks) != 0.5 {
		t.Fatal("fts boost value")
	}

	peer := Memory{ID: "peer1", ProblemSignature: "ERR_TIMEOUT", VectorSpace: "stub", PacketContent: "packet body"}
	peerPool := map[string]Memory{"peer1": peer}
	if !shouldIncludePeerRecall(peer, "ERR_TIMEOUT", "q", 0.5, floor, true, "stub") {
		t.Fatal("peer should include")
	}
	if shouldIncludePeerRecall(peer, "ERR_TIMEOUT", "q", 0.01, floor, true, "stub") {
		t.Fatal("peer below floor")
	}
	if shouldIncludePeerRecall(peer, "OTHER", "q", 0.9, floor, false, "stub") {
		t.Fatal("signature mismatch")
	}
	if shouldIncludePeerRecall(peer, "ERR_TIMEOUT", "q", 0.9, floor, false, "other-embedder") {
		t.Fatal("embedder mismatch")
	}
	peerResults := buildPeerRecallResults(peerPool, "q", "ERR_TIMEOUT", map[string]float64{"peer1": 0.8}, floor, true, "stub")
	if len(peerResults) != 1 || peerResults[0].Content != "packet body" {
		t.Fatalf("peer results %v", peerResults)
	}
	peerSkip := map[string]Memory{
		"bad":  {ID: "bad", ProblemSignature: "OTHER", VectorSpace: "stub"},
		"good": {ID: "good", ProblemSignature: "ERR_TIMEOUT", VectorSpace: "stub", PacketContent: "ok"},
	}
	filtered := buildPeerRecallResults(peerSkip, "q", "ERR_TIMEOUT", map[string]float64{"good": 0.9, "bad": 0.9}, floor, true, "stub")
	if len(filtered) != 1 || filtered[0].ID != "good" {
		t.Fatalf("filtered peer %v", filtered)
	}
	many := map[string]Memory{}
	peerScores := map[string]float64{}
	for i := 0; i < 5; i++ {
		id := "peer" + strconv.Itoa(i)
		many[id] = Memory{ID: id, ProblemSignature: "ERR_TIMEOUT", VectorSpace: "stub"}
		peerScores[id] = float64(i+1) / 10
	}
	capped := buildPeerRecallResults(many, "q", "ERR_TIMEOUT", peerScores, floor, true, "stub")
	if len(capped) != maxPeerRecallItems {
		t.Fatalf("cap %d", len(capped))
	}

	bulk := []Memory{{ID: "a", Score: 1}, {ID: "b", Score: 2}, {ID: "c", Score: 3}}
	critBulk := []Memory{{ID: "c1", Score: 9}, {ID: "c2", Score: 8}, {ID: "c3", Score: 7}, {ID: "c4", Score: 6}, {ID: "c5", Score: 5}, {ID: "c6", Score: 4}, {ID: "c7", Score: 3}, {ID: "c8", Score: 2}, {ID: "c9", Score: 1}}
	regular, critical = capRecallResults(bulk, critBulk, 2)
	if len(regular) != 2 || regular[0].ID != "c" {
		t.Fatalf("regular cap/sort %v", regular)
	}
	if len(critical) != 8 || critical[0].ID != "c1" {
		t.Fatalf("critical cap/sort %v", critical)
	}
	if normalizeRecallLimit(0) != 1 {
		t.Fatal("normalize limit")
	}
	if recallSeedCandidates(nil, nil, 0) != nil {
		t.Fatal("zero max seeds")
	}
	seeds := recallSeedCandidates(critical, regular, 2)
	if len(seeds) != 2 {
		t.Fatalf("seed cap %v", seeds)
	}
	if recallQueryTrimmed("  hi ") != "hi" {
		t.Fatal("trim")
	}
	allowPeer := peerRecallVectorAllow(peerPool)
	if len(allowPeer) != 1 {
		t.Fatal("peer allow")
	}
}

func TestSyncLogicHelpers(t *testing.T) {
	key := shareCursorMetaKey("c1", "acme/repo")
	if key != "share_cursor:c1:acme/repo" {
		t.Fatal(key)
	}

	circle := Circle{
		ID:             "c1",
		AllowedFolders: []string{"/tmp/repo"},
		Members:        []CircleMember{{DeviceID: "dev1", PublicKey: "pk1"}},
	}
	if !syncCirclePreconditions(circle, "/tmp/repo", "dev1") {
		t.Fatal("preconditions ok")
	}
	if syncCirclePreconditions(circle, "/tmp/repo", "unknown") {
		t.Fatal("unknown member")
	}
	if syncCirclePreconditions(circle, "/other", "dev1") {
		t.Fatal("folder not allowed")
	}

	project := Project{Repository: "acme/repo", Root: "/tmp/repo"}
	resolved, ok := resolveProjectForExport(project, circle)
	if !ok || resolved.Root != "/tmp/repo" {
		t.Fatalf("resolve with root %v %v", resolved, ok)
	}
	_, ok = resolveProjectForExport(Project{Repository: "acme/repo"}, Circle{AllowedFolders: []string{"/nope"}})
	if ok {
		t.Fatal("no matching folder")
	}
	_, ok = resolveProjectForExport(Project{Repository: "acme/repo", Root: "/denied"}, circle)
	if ok {
		t.Fatal("root not allowed")
	}

	m := Memory{Kind: "lesson", Scope: "repository", HotIndex: true, Layer: 2, UpdatedAt: "2025-01-02T00:00:00Z", SourceFolder: "/tmp/repo"}
	if !shouldExportMemory(m) {
		t.Fatal("exportable")
	}
	if shouldExportMemory(Memory{Kind: "preference"}) {
		t.Fatal("preference blocked")
	}
	if shouldExportMemory(Memory{Kind: "episode", Scope: "global"}) {
		t.Fatal("global blocked")
	}
	if shouldExportMemory(Memory{Kind: "working_memory"}) {
		t.Fatal("non-shareable kind")
	}
	if memorySyncFolder(Memory{SourceFolder: "/x"}, "/root") != "/x" {
		t.Fatal("source folder")
	}
	if memorySyncFolder(Memory{}, "/root") != "/root" {
		t.Fatal("project root folder")
	}
	if !memoryEligibleForSync(m, "2025-01-01T00:00:00Z", "/tmp/repo", circle) {
		t.Fatal("eligible")
	}
	if memoryEligibleForSync(m, "2025-01-03T00:00:00Z", "/tmp/repo", circle) {
		t.Fatal("since filter")
	}
	if memoryEligibleForSync(Memory{HotIndex: false, Layer: 2}, "", "/tmp/repo", circle) {
		t.Fatal("cold memory")
	}
	if memoryEligibleForSync(Memory{HotIndex: true, Layer: 1}, "", "/tmp/repo", circle) {
		t.Fatal("wrong layer")
	}
	if memoryEligibleForSync(Memory{HotIndex: true, Layer: 2, SourceFolder: ""}, "", "/denied", circle) {
		t.Fatal("folder denied")
	}

	packets := buildSyncPackets([]Memory{m}, "", circle, project)
	if len(packets) != 1 {
		t.Fatalf("packets %d", len(packets))
	}
	if !peerPacketImportable(SharedPacket{HotIndex: true, VectorSpace: "stub"}, "stub") {
		t.Fatal("importable")
	}
	if peerPacketImportable(SharedPacket{HotIndex: true, VectorSpace: "other"}, "stub") {
		t.Fatal("vector space mismatch")
	}
	if peerPacketImportable(SharedPacket{HotIndex: false}, "stub") {
		t.Fatal("cold packet")
	}
	if !peerMemoryShouldUpdate(Memory{}, errors.New("missing"), 1) {
		t.Fatal("missing updates")
	}
	if peerMemoryShouldUpdate(Memory{EvidenceScore: 2}, nil, 1) {
		t.Fatal("lower evidence skips")
	}
	if !peerMemoryShouldUpdate(Memory{EvidenceScore: 1}, nil, 2) {
		t.Fatal("higher evidence updates")
	}

	resp := SyncResponse{CircleID: "c1", DeviceID: "dev1"}
	if err := validatePeerBatchCircle(circle, resp); err != nil {
		t.Fatal(err)
	}
	if err := validatePeerBatchCircle(circle, SyncResponse{CircleID: "other"}); err == nil {
		t.Fatal("circle mismatch")
	}
	if memberPubKeyFor(circle, "dev1") != "pk1" {
		t.Fatal("member pubkey")
	}

	report := shareSyncReport{}
	applySharePeerSyncResult(&report, sharePeerSyncResult{Contacted: true, OK: true, Imported: 2, Cursor: "cur"}, func(c string) {
		if c != "cur" {
			t.Fatal("cursor")
		}
	})
	if report.PeersContacted != 1 || report.PeersOK != 1 || report.Imported != 2 {
		t.Fatalf("report %+v", report)
	}
	applySharePeerSyncResult(&report, sharePeerSyncResult{Contacted: true}, nil)
	if report.PeersOK != 1 {
		t.Fatal("failed peer should not increment ok")
	}
	before := report.PeersContacted
	applySharePeerSyncResult(&report, sharePeerSyncResult{}, nil)
	if report.PeersContacted != before {
		t.Fatal("not contacted should noop")
	}

	eps := nonEmptyEndpoints([]string{"", " http://peer "})
	if len(eps) != 1 || eps[0] != " http://peer " {
		t.Fatalf("endpoints %v", eps)
	}

	t.Setenv("OVERDRIVE_SHARE_LISTEN", "1")
	if !shareListenEnabled() {
		t.Fatal("listen enabled")
	}
	t.Setenv("OVERDRIVE_SHARE_LISTEN", "  ")
	if shareListenEnabled() {
		t.Fatal("listen disabled")
	}
}
