package main

import (
	"bufio"
	"crypto/cipher"
	"crypto/ed25519"
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
	"unsafe"
)

func TestRemainingStoreBranches(t *testing.T) {
	home := t.TempDir()
	setupTestEnv(t, home)
	e, err := newEngine(home)
	if err != nil {
		t.Fatal(err)
	}
	defer e.Close()
	_ = e.db.Close()
	if columnExists(e.db, "memories", "id") {
		t.Fatal("closed pragma")
	}

	e2, err := newEngine(home)
	if err != nil {
		t.Fatal(err)
	}
	defer e2.Close()
	project := Project{Root: home, Repository: "github.com/acme/r", Organization: "github.com/acme"}
	m, err := e2.recordMemory(project, "lesson", "repository", "subject", "content", 0.8, 10, "agent_observation", "", "")
	if err != nil {
		t.Fatal(err)
	}
	if e2.peerEligible(Memory{Status: "active", Origin: "peer", HotIndex: true, ScopeID: project.Repository}, project) {
		t.Fatal("missing circle")
	}
	resetHooksForTest()
	t.Cleanup(resetHooksForTest)
	hookSQLCommit = func(*sql.Tx) error { return errors.New("commit") }
	if _, err := e2.validateMemory(m.ID, "success", "", ""); err == nil {
		t.Fatal("validate commit")
	}
	hookSQLCommit = defaultSQLCommit
	if _, err := e2.db.Exec(`DROP TABLE graph_nodes`); err != nil {
		t.Fatal(err)
	}
	if err := e2.deleteMemory(m.ID); err == nil {
		t.Fatal("graph delete")
	}
	if _, err := e2.db.Exec(`DROP TABLE memories`); err != nil {
		t.Fatal(err)
	}
	if _, err := e2.listCatalogItems("self"); err == nil {
		t.Fatal("catalog query")
	}
	if _, err := e2.buildCatalogResponse(Circle{}, DeviceIdentity{}, nil); err == nil {
		t.Fatal("catalog build")
	}
	if _, edges := e2.catalogGraphExport("self"); edges != nil && len(edges) != 0 {
		t.Fatal(edges)
	}
	e2.backfillGraph()
	if _, err := e2.db.Exec(`DROP TABLE ledger_entries`); err != nil {
		t.Fatal(err)
	}
	if _, err := e2.ledgerList(project, "run"); err == nil {
		t.Fatal("ledger")
	}
	if _, err := e2.db.Exec(`DROP TABLE working_memory`); err != nil {
		t.Fatal(err)
	}
	e2.sessionID = "s"
	if _, err := e2.listWorkingMemory(project); err == nil {
		t.Fatal("working")
	}
	if _, err := e2.startSession(project); err == nil {
		t.Fatal("session")
	}
}

func TestGraphCapsAndPeerImport(t *testing.T) {
	home := t.TempDir()
	setupTestEnv(t, home)
	id, priv, err := loadOrCreateIdentity(home)
	if err != nil {
		t.Fatal(err)
	}
	circle, err := createCircle(home, "team", id, priv)
	if err != nil {
		t.Fatal(err)
	}
	circle, err = addAllowedFolder(home, circle.ID, home, id)
	if err != nil {
		t.Fatal(err)
	}
	e, err := newEngine(home)
	if err != nil {
		t.Fatal(err)
	}
	defer e.Close()
	project := Project{Root: home, Repository: "github.com/acme/r", Organization: "github.com/acme"}
	var ids []string
	for i := 0; i < 10; i++ {
		m, err := e.recordMemory(project, "lesson", "repository", "db.go token "+string(rune('a'+i)), "body", 0.9, 10, "verified_execution", "", "db.go")
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, m.ID)
	}
	for _, other := range ids[1:] {
		if err := e.upsertGraphEdge(graphMemoryNodeID(ids[0]), graphMemoryNodeID(other), "mentions"); err != nil {
			t.Fatal(err)
		}
	}
	if got := e.graphNeighborIDs([]string{ids[0]}); len(got) > maxGraphNeighbors {
		t.Fatal(len(got))
	}
	nodes := []PageIndexNode{}
	for i := 0; i < maxGraphNeighbors+2; i++ {
		nodes = append(nodes, PageIndexNode{Kind: "memory", MemoryID: "g" + string(rune('a'+i)), Title: "alpha"})
	}
	if got := e.unresolvedPageIndexIDs(nodes, "nope alpha"); len(got) > maxGraphNeighbors {
		t.Fatal(len(got))
	}
	peer := Memory{
		ID: "peer-hot", Kind: "lesson", Status: "active", Origin: "peer", HotIndex: true, Layer: 2,
		Scope: "repository", ScopeID: project.Repository, PeerDeviceID: "missing", CircleID: circle.ID,
		Subject: "peer", Content: "body",
	}
	if err := e.upsertMemory(peer); err != nil {
		t.Fatal(err)
	}
	if got := e.loadGraphNeighborMemories([]string{peer.ID}, map[string]bool{}, project); len(got) != 0 {
		t.Fatal(got)
	}
	local := ids[1]
	mem, _ := e.getMemory(local)
	mem.Scope = "module"
	mem.ScopeID = "other-module"
	if err := e.upsertMemory(mem); err != nil {
		t.Fatal(err)
	}
	_ = e.loadGraphNeighborMemories([]string{local}, map[string]bool{}, Project{Repository: "elsewhere"})

	if _, err := e.db.Exec(`DROP TABLE graph_edges`); err != nil {
		t.Fatal(err)
	}
	e.graphNeighborIDs([]string{ids[0]})
	_, _ = e.pageIndexBranch([]string{ids[0]})
	if _, err := e.db.Exec(`INSERT INTO memories(id, vector_id, kind, scope, scope_id, subject, content, confidence, priority, status, source, source_ref, evidence, success_count, failure_count, evidence_score, created_at, updated_at, last_validated_at, origin, peer_device_id, circle_id, layer, packet_content, problem_signature, source_folder, vector_space, hot_index) VALUES ('rule-x',1,'rule','repository',?, '', '', 1,1,'active','','','',0,0,0,'','','','local','','',2,'','','', '',1)`, project.Repository); err != nil {
		t.Fatal(err)
	}
	_, _ = e.catalogGraphExport(id.DeviceID)

	resetHooksForTest()
	t.Cleanup(resetHooksForTest)
	hookMkdirAll = func(string, os.FileMode) error { return errors.New("mkdir") }
	e.fetchMissingPeerPackets(project, []string{"ghost"})
	e.importPeerCatalogs(project)
	if _, _, err := loadOrCreateIdentity(home); err == nil {
		t.Fatal("identity mkdir")
	}
	hookMkdirAll = os.MkdirAll
	hookReadFile = func(string) ([]byte, error) {
		return []byte(`{"private_key":"@@@"}`), nil
	}
	if _, _, err := loadOrCreateIdentity(home); err == nil {
		t.Fatal("bad key")
	}
	hookReadFile = os.ReadFile
	hookRandRead = func([]byte) (int, error) { return 0, errors.New("rand") }
	if _, _, err := loadOrCreateIdentity(t.TempDir()); err == nil {
		t.Fatal("rand identity")
	}
}

func TestShareStatusBlockedAndSyncPackets(t *testing.T) {
	resetHooksForTest()
	t.Cleanup(resetHooksForTest)
	home := t.TempDir()
	setupTestEnv(t, home)
	id, priv, err := loadOrCreateIdentity(home)
	if err != nil {
		t.Fatal(err)
	}
	circle, err := createCircle(home, "team", id, priv)
	if err != nil {
		t.Fatal(err)
	}
	e, err := newEngine(home)
	if err != nil {
		t.Fatal(err)
	}
	defer e.Close()
	circle, err = addAllowedFolder(home, circle.ID, home, id)
	if err != nil {
		t.Fatal(err)
	}
	calls := 0
	hookFilepathAbs = func(p string) (string, error) {
		calls++
		if calls > 3 {
			return "", errors.New("abs")
		}
		return filepath.Abs(p)
	}
	status, err := e.shareStatus(Project{Root: home, Repository: "github.com/acme/r"})
	if err != nil || status.Mode != "folder-blocked" {
		t.Fatalf("%+v %v", status, err)
	}
	packets := buildSyncPacketsForIDs([]Memory{{
		ID: "nope", HotIndex: true, Layer: 2, Kind: "lesson",
	}}, "", []string{"keep"}, Circle{}, Project{Root: home})
	if len(packets) != 0 {
		t.Fatal(packets)
	}
	upsertMember(Circle{}, CircleMember{})
	merged := mergeMember(CircleMember{}, CircleMember{Revoked: true})
	if !merged.Revoked {
		t.Fatal("revoked")
	}
	stamped := stampMemberHostname(Circle{Members: []CircleMember{{DeviceID: "d"}}}, "d", "")
	if stamped.Members[0].Hostname != "" {
		t.Fatal("empty host")
	}
	slot := uintptr(77)
	old := ortSyscallN
	ortSyscallN = func(uintptr, ...uintptr) (uintptr, uintptr, uintptr) { return 9, 0, 0 }
	defer func() { ortSyscallN = old }()
	api, err := resolveORTAPI(func() uintptr { return uintptr(unsafe.Pointer(&slot)) }, 1)
	if err != nil || api == 0 {
		t.Fatal(api, err)
	}
	tokPath := filepath.Join(t.TempDir(), "vocab.txt")
	huge := strings.Repeat("a", 70*1024)
	if err := os.WriteFile(tokPath, []byte(huge+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := loadWordPieceTokenizer(tokPath); err == nil {
		t.Fatal("scanner")
	}
	basic := (&wordPieceTokenizer{}).basicTokenize("hello!")
	if len(basic) == 0 {
		t.Fatal("tokenize")
	}
}

func TestCatalogDialFailures(t *testing.T) {
	resetHooksForTest()
	t.Cleanup(resetHooksForTest)
	home := t.TempDir()
	setupTestEnv(t, home)
	id, priv, err := loadOrCreateIdentity(home)
	if err != nil {
		t.Fatal(err)
	}
	circle, err := createCircle(home, "team", id, priv)
	if err != nil {
		t.Fatal(err)
	}
	circle.Members = append(circle.Members, CircleMember{DeviceID: "other", PublicKey: id.PublicKey})
	client, server := net.Pipe()
	hookNetDial = func(string, string, time.Duration) (net.Conn, error) { return client, nil }
	go func() {
		defer server.Close()
		_, _ = bufio.NewReader(server).ReadString('\n')
	}()
	if _, err := fetchPeerCatalog(home, "127.0.0.1:1", circle, id, priv); err == nil {
		t.Fatal("short catalog")
	}
	_ = client.Close()

	hookJSONMarshal = func(v any) ([]byte, error) {
		if _, ok := v.(SyncRequest); ok {
			return nil, errors.New("marshal")
		}
		return json.Marshal(v)
	}
	if _, err := fetchPeerSyncIDs(home, "127.0.0.1:1", circle, id, priv, "repo", []string{"a"}); err == nil {
		t.Fatal("marshal ids")
	}
	hookJSONMarshal = json.Marshal
	bad := circle
	bad.GroupKey = "aa"
	if _, err := fetchPeerSyncIDs(home, "127.0.0.1:1", bad, id, priv, "repo", nil); err == nil {
		t.Fatal("encrypt")
	}
	hookNetDial = func(string, string, time.Duration) (net.Conn, error) {
		c, s := net.Pipe()
		_ = s.Close()
		return c, nil
	}
	if _, err := fetchPeerSyncIDs(home, "127.0.0.1:1", circle, id, priv, "repo", nil); err == nil {
		t.Fatal("write")
	}

	sl := &shareListener{home: home, id: id}
	c1, c2 := net.Pipe()
	go func() {
		defer c2.Close()
		sl.handleCatalog(c1, circle, priv, []byte("{"))
	}()
	_, _ = bufio.NewReader(c2).ReadString('\n')
	c3, c4 := net.Pipe()
	go sl.handleConn(c3, priv)
	_ = c4.Close()

	out := peerDashboardContexts(t.TempDir(), "")
	if len(out.Computers) != 0 {
		t.Fatal(out)
	}
	revoked := buildContextComputers(Circle{Members: []CircleMember{{DeviceID: "d", Revoked: true}}}, "d", nil, nil, nil)
	if len(revoked) != 0 {
		t.Fatal(revoked)
	}
	tree := projectPageIndexTree([]PageIndexNode{
		{ID: "folder:x", Kind: "folder", Title: "", Summary: "/only"},
		{ID: "mem", Kind: "memory", ParentID: "missing-kind", Title: "t", MemoryID: "m"},
	}, []string{"/extra"})
	if len(tree) == 0 {
		t.Fatal(tree)
	}

	tvOnce = sync.Once{}
	prevLoaded := tvLoaded
	hookOpenDynamicLib = func(string) (uintptr, error) { return 0, errors.New("dl") }
	_ = loadTurboVecLib(home)
	tvOnce = sync.Once{}
	hookOpenDynamicLib = nil
	tvLoaded = prevLoaded
	idx := &TurboVecIndex{available: true, handle: 1, dim: 3}
	oldSearch := tvSearch
	tvSearch = func(uintptr, *float32, int32, int32, *uint64, int32, *uint64, *float32, *int32) int32 { return 1 }
	_, _ = idx.Search([]float32{0, 0, 0}, 1, nil)
	tvSearch = oldSearch

	pub, genPriv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	_ = pub
	hookJSONMarshal = func(any) ([]byte, error) { return nil, errors.New("json") }
	if _, err := signSyncResponse(genPriv, SyncResponse{}); err == nil {
		t.Fatal("sign sync")
	}
	if verifySyncResponse(nil, SyncResponse{}) {
		t.Fatal("verify sync")
	}
}

func TestCatalogRoundTripAndDBEdges(t *testing.T) {
	resetHooksForTest()
	t.Cleanup(resetHooksForTest)
	home := t.TempDir()
	sl, endpoint := startShareListenerForTest(t, home)
	defer sl.Close()
	id, priv, err := loadOrCreateIdentity(home)
	if err != nil {
		t.Fatal(err)
	}
	circle, err := createCircle(home, "team", id, priv)
	if err != nil {
		t.Fatal(err)
	}
	repo := t.TempDir()
	circle, err = addAllowedFolder(home, circle.ID, repo, id)
	if err != nil {
		t.Fatal(err)
	}
	e, err := newEngine(home)
	if err != nil {
		t.Fatal(err)
	}
	project := Project{Root: repo, Repository: "github.com/acme/r", Organization: "github.com/acme"}
	if _, err := e.recordMemory(project, "lesson", "repository", "catalog lesson", "secret body", 0.9, 10, "verified_execution", "", "db.go"); err != nil {
		t.Fatal(err)
	}
	e.Close()
	hookJSONMarshal = func(v any) ([]byte, error) {
		if _, ok := v.(SyncRequest); ok {
			return nil, errors.New("req")
		}
		return json.Marshal(v)
	}
	if _, err := fetchPeerCatalog(home, endpoint, circle, id, priv); err == nil {
		t.Fatal("marshal catalog")
	}
	hookJSONMarshal = json.Marshal
	resp, err := fetchPeerCatalog(home, endpoint, circle, id, priv)
	if err != nil {
		t.Fatal(err)
	}
	if len(resp.Tree) == 0 {
		t.Fatal("empty tree")
	}
	circle.PeerEndpoints = []string{endpoint}
	if err := saveCircle(home, circle); err != nil {
		t.Fatal(err)
	}
	t.Setenv("OVERDRIVE_SHARE_LISTEN", "127.0.0.1:1")
	network := peerDashboardContexts(home, "")
	if len(network.Computers) == 0 {
		t.Fatal("no computers")
	}
	unknown := circle
	unknown.Members = []CircleMember{{DeviceID: "nope", PublicKey: id.PublicKey}}
	if _, err := fetchPeerCatalog(home, endpoint, unknown, id, priv); err == nil {
		t.Fatal("unknown catalog peer")
	}

	e2, err := newEngine(home)
	if err != nil {
		t.Fatal(err)
	}
	defer e2.Close()
	hookFTSScanErr = errors.New("fts")
	if _, _, err := e2.ftsCandidates("lesson", project, 3); err == nil {
		t.Fatal("fts scan")
	}
	hookFTSScanErr = nil
	hookSQLCommit = func(*sql.Tx) error { return errors.New("commit") }
	if _, err := e2.recordMemory(project, "lesson", "repository", "catalog lesson", "secret body", 0.9, 10, "verified_execution", "", "db.go"); err == nil {
		t.Fatal("second record")
	}
	hookSQLCommit = defaultSQLCommit
	if _, err := e2.db.Exec(`DROP TABLE sessions`); err != nil {
		t.Fatal(err)
	}
	if _, err := e2.startSession(project); err == nil {
		t.Fatal("sessions")
	}
	hookIOCopy = func(io.Writer, io.Reader) (int64, error) { return 0, errors.New("copy") }
	if err := atomicWriteFile(filepath.Join(t.TempDir(), "a.bin"), strings.NewReader("x"), 0o600); err == nil {
		t.Fatal("copy")
	}
	if err := extractORTArchive(filepath.Join(t.TempDir(), "missing.tar.gz"), t.TempDir()); err == nil {
		t.Fatal("archive")
	}
	key := "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA="
	hookNewGCM = func(cipher.Block) (cipher.AEAD, error) { return nil, errors.New("gcm") }
	if _, err := decryptBatch(key, base64.StdEncoding.EncodeToString([]byte("0123456789abcdef0123456789abcdef"))); err == nil {
		t.Fatal("gcm decrypt")
	}
	hookNetInterfaces = func() ([]net.Interface, error) { return nil, nil }
	if !strings.HasPrefix(publicEndpoint("0.0.0.0:7741"), "0.0.0.0:") {
		t.Fatal(publicEndpoint("0.0.0.0:7741"))
	}
	if chooseEndpoint("0.0.0.0:1", "not-a-remote") != "" {
		t.Fatal("choose empty")
	}
	c1, c2 := net.Pipe()
	go func() {
		defer c1.Close()
		sl.handleCatalog(c2, circle, priv, []byte("{"))
	}()
	_, _ = bufio.NewReader(c1).ReadString('\n')
}
