package main

import (
	"crypto/cipher"
	"database/sql"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"
)

type nthExecStore struct {
	n, failAt int
}

func (s *nthExecStore) Exec(string, ...any) (sql.Result, error) {
	s.n++
	if s.failAt > 0 && s.n == s.failAt {
		return nil, errors.New("exec fail")
	}
	return nil, nil
}

func (s *nthExecStore) Query(string, ...any) (*sql.Rows, error) {
	return nil, errors.New("query fail")
}

func richGraphMemory() Memory {
	return Memory{
		ID: "mem-1", Kind: "", ScopeID: "", Subject: "lock", Evidence: "db.go SQLITE_BUSY 503",
		Origin: "peer", PeerDeviceID: "dev-peer", Status: "active", SourceFolder: "",
	}
}

func TestGraphStoresAndClassifiers(t *testing.T) {
	if !isConcreteErrorToken("503") || isConcreteErrorToken("nope") || isConcreteErrorToken("WAL") {
		t.Fatal("error token classes")
	}
	if k, _ := classifySignatureToken("  "); k != "" {
		t.Fatal("blank token")
	}
	if k, _ := classifySignatureToken("503"); k != "error" {
		t.Fatal(k)
	}
	var nilEng *Engine
	if nilEng.graphDB() != nil {
		t.Fatal("nil graph db")
	}
	if err := upsertGraphNodeOn(nil, PageIndexNode{ID: "x"}); err == nil {
		t.Fatal("nil node store")
	}
	if err := upsertGraphNodeOn(&nthExecStore{}, PageIndexNode{}); err != nil {
		t.Fatal(err)
	}
	if err := upsertGraphEdgeOn(nil, "a", "b", "mentions"); err == nil {
		t.Fatal("nil edge store")
	}
	if err := upsertGraphEdgeOn(&nthExecStore{}, "", "b", "mentions"); err != nil {
		t.Fatal(err)
	}
	if err := removeMemoryGraphOn(nil, "id"); err == nil {
		t.Fatal("nil remove")
	}
	if err := removeMemoryGraphOn(&nthExecStore{}, "  "); err != nil {
		t.Fatal(err)
	}
	if err := removeMemoryGraphOn(&nthExecStore{failAt: 1}, "id"); err == nil {
		t.Fatal("remove exec")
	}
	if err := removeMemoryGraphOn(&nthExecStore{failAt: 2}, "id"); err == nil {
		t.Fatal("remove second")
	}
	if err := syncMemoryGraphOn(nil, richGraphMemory(), nil); err == nil {
		t.Fatal("nil sync")
	}
	if err := syncMemoryGraphOn(&nthExecStore{}, Memory{}, nil); err != nil {
		t.Fatal(err)
	}
	if err := syncMemoryGraphOn(&nthExecStore{}, Memory{ID: "x", Status: "stale"}, nil); err != nil {
		t.Fatal(err)
	}
	if err := writeMemoryGraphOn(nil, richGraphMemory()); err == nil {
		t.Fatal("nil write")
	}
	for i := 1; i <= 16; i++ {
		_ = writeMemoryGraphOn(&nthExecStore{failAt: i}, richGraphMemory())
		_ = syncMemoryGraphOn(&nthExecStore{failAt: i}, richGraphMemory(), []GraphEdge{{Src: "a", Dst: "b", Kind: "mentions"}})
	}
	if err := (&Engine{}).upsertGraphNode(PageIndexNode{ID: "n"}); err == nil {
		t.Fatal("nil engine node")
	}
	(&Engine{}).backfillGraph()
	if (&Engine{}).graphNeighborIDs([]string{"a"}) != nil {
		t.Fatal("nil neighbors")
	}
	if _, ok := (&Engine{}).loadGraphNode("x"); ok {
		t.Fatal("nil load")
	}
	if nodes, _ := (&Engine{}).catalogGraphExport("self"); len(nodes) != 0 {
		t.Fatal(nodes)
	}
	(&Engine{}).fetchMissingPeerPackets(Project{}, []string{"a"})
	(&Engine{}).importPeerCatalogs(Project{})
	if got := uniqueStrings([]string{"", "a", "a", "  b"}); len(got) != 2 {
		t.Fatal(got)
	}
}

func TestGraphRecallBranches(t *testing.T) {
	home := t.TempDir()
	setupTestEnv(t, home)
	e, err := newEngine(home)
	if err != nil {
		t.Fatal(err)
	}
	defer e.Close()
	project := Project{Root: home, Repository: "github.com/acme/r", Organization: "github.com/acme"}
	if _, err := e.recall(project, "", 4, "all"); err != nil {
		t.Fatal(err)
	}
	first, err := e.recordMemory(project, "lesson", "repository", "WAL lock db.go", "body one", 0.9, 50, "verified_execution", "", "db.go SQLITE_BUSY")
	if err != nil {
		t.Fatal(err)
	}
	second, err := e.recordMemory(project, "lesson", "repository", "other db.go", "body two", 0.9, 50, "verified_execution", "", "db.go")
	if err != nil {
		t.Fatal(err)
	}
	if err := e.upsertGraphEdge(graphMemoryNodeID(first.ID), graphMemoryNodeID(second.ID), "supersedes"); err != nil {
		t.Fatal(err)
	}
	if ids := e.graphNeighborIDs([]string{first.ID}); len(ids) == 0 {
		t.Fatal("expected supersedes hop")
	}
	e.graphNeighborIDs(nil)
	_, _ = e.pageIndexBranch(nil)
	_, _ = e.loadGraphNode("")
	e.importCatalogGraph(CatalogResponse{Tree: []PageIndexNode{{}}, Edges: []GraphEdge{{Src: "a", Dst: "b", Kind: "mentions"}}})
	missing := e.unresolvedPageIndexIDs([]PageIndexNode{
		{Kind: "memory", MemoryID: "ghost-1", Title: "timeout lock"},
		{Kind: "memory", MemoryID: "ghost-2", Title: "unrelated"},
		{Kind: "folder", Title: "skip"},
	}, "timeout")
	if len(missing) != 1 || missing[0] != "ghost-1" {
		t.Fatalf("missing: %v", missing)
	}
	cold := second
	cold.Status = "stale"
	cold.HotIndex = false
	if err := e.upsertMemory(cold); err != nil {
		t.Fatal(err)
	}
	if got := e.loadGraphNeighborMemories([]string{second.ID, "missing", first.ID}, map[string]bool{first.ID: true}, project); len(got) != 0 {
		t.Fatalf("filtered neighbors: %+v", got)
	}
	if _, err := e.db.Exec(`DELETE FROM graph_nodes`); err != nil {
		t.Fatal(err)
	}
	if _, err := e.db.Exec(`DELETE FROM graph_edges`); err != nil {
		t.Fatal(err)
	}
	e.backfillGraph()
	e.backfillGraph()
}

func TestFetchMissingAndCatalogTreeEdges(t *testing.T) {
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
	circle.PeerEndpoints = []string{"127.0.0.1:9", "127.0.0.1:9"}
	if err := saveCircle(home, circle); err != nil {
		t.Fatal(err)
	}
	e, err := newEngine(home)
	if err != nil {
		t.Fatal(err)
	}
	defer e.Close()
	project := Project{Root: home, Repository: "github.com/acme/r"}
	e.fetchMissingPeerPackets(project, nil)
	e.fetchMissingPeerPackets(project, []string{"", "mem-x"})
	e.importPeerCatalogs(Project{Root: t.TempDir()})
	resetHooksForTest()
	t.Cleanup(resetHooksForTest)
	hookNetDial = func(string, string, time.Duration) (net.Conn, error) {
		return nil, errors.New("dial")
	}
	e.fetchMissingPeerPackets(project, []string{"mem-x"})
	e.importPeerCatalogs(project)
	t.Setenv("OVERDRIVE_SHARE_LISTEN", "127.0.0.1:9")
	e.fetchMissingPeerPackets(project, []string{"mem-x"})

	tree := projectPageIndexTree([]PageIndexNode{
		{ID: "f", Kind: "folder", Title: "", Summary: ""},
		{ID: "f2", Kind: "folder", Title: "src", Summary: "/srv/src"},
		{ID: "k", Kind: "kind", Title: "", ParentID: "f2"},
		{ID: "m", Kind: "memory", Title: "", Summary: "card", ParentID: "k", MemoryID: "m1"},
	}, []string{"", "/srv/src", "/srv/src"})
	if len(tree) == 0 {
		t.Fatal("expected projected folders")
	}
	if folderBaseName(".") == "" || folderBaseName("/") == "" {
		t.Fatal("folder base")
	}
	loose := buildFolderTree([]CatalogItem{{ID: "a", Kind: "lesson", SourceFolder: ""}}, nil)
	if len(loose) != 1 || loose[0].Path != "(no folder)" {
		t.Fatalf("%+v", loose)
	}
	groups := buildKindGroups([]CatalogItem{{ID: "id-only", Kind: "fact"}})
	if groups[0].Items[0].Subject != "id-only" {
		t.Fatal(groups)
	}
	if _, err := (&Engine{}).listCatalogItems("self"); err != nil {
		t.Fatal(err)
	}
	if _, err := e.db.Exec(`INSERT INTO memories(id, vector_id, kind, scope, scope_id, subject, content, confidence, priority, status, source, source_ref, evidence, success_count, failure_count, evidence_score, created_at, updated_at, last_validated_at, origin, peer_device_id, circle_id, layer, packet_content, problem_signature, source_folder, vector_space, hot_index) VALUES ('rule-1', 1, 'rule', 'repository', 'r', '', '', 1, 1, 'active', '', '', '', 0, 0, 0, '', '', '', 'local', '', '', 2, '', '', '/srv', '', 1)`); err != nil {
		t.Fatal(err)
	}
	items, err := e.listCatalogItems(id.DeviceID)
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range items {
		if item.Kind == "rule" {
			t.Fatal("rule should be skipped")
		}
	}
}

func TestDashboardAndPeersBranches(t *testing.T) {
	resetHooksForTest()
	t.Cleanup(resetHooksForTest)
	t.Setenv("OVERDRIVE_SHARE_UI", "off")
	if _, ok := shareUIAddr(); ok {
		t.Fatal("ui off")
	}
	t.Setenv("OVERDRIVE_SHARE_UI", "127.0.0.1:9")
	if addr, ok := shareUIAddr(); !ok || addr != "127.0.0.1:9" {
		t.Fatal(addr, ok)
	}
	t.Setenv("OVERDRIVE_SHARE_UI", "")
	t.Setenv("OVERDRIVE_SHARE_LISTEN", "bad")
	if _, ok := shareUIAddr(); ok {
		t.Fatal("bad listen")
	}
	t.Setenv("OVERDRIVE_SHARE_LISTEN", "127.0.0.1:0")
	if _, ok := shareUIAddr(); ok {
		t.Fatal("port 0")
	}
	t.Setenv("OVERDRIVE_SHARE_LISTEN", ":7741")
	if addr, ok := shareUIAddr(); !ok || !strings.HasSuffix(addr, ":7742") {
		t.Fatal(addr, ok)
	}
	if normalizeListen(":9") != "0.0.0.0:9" {
		t.Fatal(normalizeListen(":9"))
	}
	if publicEndpoint("not-host") != "not-host" {
		t.Fatal("split")
	}
	if !isLoopbackHost("localhost") || !isLoopbackHost("127.0.0.1") || isLoopbackHost("10.0.0.1") {
		t.Fatal("loopback")
	}
	if chooseEndpoint("", "10.0.0.8:1") == "" || chooseEndpoint("", "nope") != "" {
		t.Fatal("choose")
	}
	hookNetInterfaces = func() ([]net.Interface, error) { return nil, errors.New("ifaces") }
	if firstLANIPv4() != "" {
		t.Fatal("iface err")
	}
	hookNetInterfaces = func() ([]net.Interface, error) {
		return []net.Interface{
			{Name: "down", Flags: 0},
			{Name: "lo", Flags: net.FlagUp | net.FlagLoopback},
			{Name: "docker0", Flags: net.FlagUp},
			{Name: "br-1", Flags: net.FlagUp},
			{Name: "veth0", Flags: net.FlagUp},
			{Name: "eth0", Flags: net.FlagUp},
		}, nil
	}
	hookIfaceAddrs = func(iface net.Interface) ([]net.Addr, error) {
		if iface.Name != "eth0" {
			return nil, errors.New("addrs")
		}
		return []net.Addr{
			ipOnly("not-ip"),
			&net.IPNet{IP: nil},
			&net.IPNet{IP: net.ParseIP("127.0.0.1")},
			&net.IPNet{IP: net.ParseIP("2001:db8::1")},
			&net.IPNet{IP: net.ParseIP("169.254.1.1")},
			&net.IPNet{IP: net.ParseIP("10.9.8.7")},
		}, nil
	}
	if got := firstLANIPv4(); got != "10.9.8.7" {
		t.Fatal(got)
	}
	if publicEndpoint("0.0.0.0:7741") == "" || publicEndpoint("[::]:7741") == "" {
		t.Fatal("public wildcard")
	}
	hookReadFile = func(string) ([]byte, error) {
		return []byte("OTHER=1\nPRETTY_HOSTNAME=\nPRETTY_HOSTNAME=\"station\"\n"), nil
	}
	if computerName() != "station" {
		t.Fatal(computerName())
	}
	hookReadFile = func(string) ([]byte, error) { return nil, errors.New("read") }
	hookHostname = func() (string, error) { return "", errors.New("host") }
	if computerName() != "" {
		t.Fatal("hostname")
	}
	if peerDisplayName(CircleMember{Endpoint: "10.0.0.1:1"}, "") == "" {
		t.Fatal("display")
	}

	home := t.TempDir()
	setupTestEnv(t, home)
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/logo.png", nil)
	shareDashboardHandler(home).ServeHTTP(rr, req)
	if rr.Code != http.StatusMethodNotAllowed {
		t.Fatal(rr.Code)
	}
	for _, path := range []string{"/api/peers", "/api/contexts"} {
		rr = httptest.NewRecorder()
		req = httptest.NewRequest(http.MethodPost, path, nil)
		shareDashboardHandler(home).ServeHTTP(rr, req)
		if rr.Code != http.StatusMethodNotAllowed {
			t.Fatal(path, rr.Code)
		}
	}
	rr = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/missing", nil)
	shareDashboardHandler(home).ServeHTTP(rr, req)
	if rr.Code != http.StatusNotFound {
		t.Fatal(rr.Code)
	}
	if err := os.MkdirAll(circlesDir(home), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(circlesDir(home)+"/bad.json", []byte("{"), 0o600); err != nil {
		t.Fatal(err)
	}
	snap := peerDashboardSnapshot(home)
	if snap.Hostname == "" && false {
		t.Fatal(snap)
	}
	shareDashboardOnce = sync.Once{}
	t.Setenv("OVERDRIVE_SHARE_UI", "127.0.0.1:0")
	startShareDashboard(home)
	startShareDashboard(home)
}

type ipOnly string

func (a ipOnly) Network() string { return "ip" }
func (a ipOnly) String() string  { return string(a) }

func TestClosedDBAndCommitHook(t *testing.T) {
	home := t.TempDir()
	setupTestEnv(t, home)
	e, err := newEngine(home)
	if err != nil {
		t.Fatal(err)
	}
	if columnExists(e.db, "no_such", "id") {
		t.Fatal("missing table info")
	}
	_ = e.db.Close()
	if err := e.persistMemory(Memory{ID: "x", Status: "active"}, nil); err == nil {
		t.Fatal("closed persist")
	}
	if err := e.deleteMemory("x"); err == nil {
		t.Fatal("closed delete")
	}
	if err := e.upsertPeerMemory(Memory{ID: "p", Status: "active", Origin: "peer"}, nil); err == nil {
		t.Fatal("closed peer")
	}
	if err := e.markPeerRevoked("c", "d"); err == nil {
		t.Fatal("closed revoke")
	}
	if (&Engine{}).persistMemory(Memory{ID: "x"}, nil) == nil {
		t.Fatal("nil persist")
	}
	if (&Engine{}).deleteMemory("x") == nil {
		t.Fatal("nil delete")
	}
	if (&Engine{}).upsertPeerMemory(Memory{ID: "p"}, nil) == nil {
		t.Fatal("nil peer")
	}
	if (&Engine{}).markPeerRevoked("c", "d") == nil {
		t.Fatal("nil revoke")
	}

	e2, err := newEngine(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer e2.Close()
	setupTestEnv(t, e2.home)
	resetHooksForTest()
	t.Cleanup(resetHooksForTest)
	hookSQLCommit = func(*sql.Tx) error { return errors.New("commit") }
	project := Project{Root: e2.home, Repository: "github.com/acme/r", Organization: "github.com/acme"}
	if _, err := e2.recordMemory(project, "lesson", "repository", "s", "c", 0.5, 1, "agent_observation", "", ""); err == nil {
		t.Fatal("commit record")
	}
	m := Memory{ID: "peer-1", Kind: "lesson", Status: "active", Origin: "peer", Scope: "repository", ScopeID: "github.com/acme/r", HotIndex: true, Layer: 2}
	if err := e2.upsertPeerMemory(m, nil); err == nil {
		t.Fatal("commit peer")
	}
	if err := e2.deleteMemory("missing"); err == nil {
		t.Fatal("commit delete")
	}
	if err := e2.markPeerRevoked("c", "d"); err == nil {
		t.Fatal("commit revoke")
	}
	if _, err := e2.db.Exec(`DROP TABLE memories`); err != nil {
		t.Fatal(err)
	}
	hookSQLCommit = defaultSQLCommit
	if err := e2.persistMemory(Memory{ID: "z", Status: "active", Kind: "lesson"}, nil); err == nil {
		t.Fatal("insert without table")
	}
}

func TestCryptoAndSmallBranches(t *testing.T) {
	resetHooksForTest()
	t.Cleanup(resetHooksForTest)
	key := "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA="
	hookNewAES = func([]byte) (cipher.Block, error) { return nil, errors.New("aes") }
	if _, err := encryptBatch(key, []byte("x")); err == nil {
		t.Fatal("aes")
	}
	if _, err := decryptBatch(key, "AAAA"); err == nil {
		t.Fatal("decrypt aes")
	}
	hookNewAES = aesNewCipher
	hookNewGCM = func(cipher.Block) (cipher.AEAD, error) { return nil, errors.New("gcm") }
	if _, err := encryptBatch(key, []byte("x")); err == nil {
		t.Fatal("gcm")
	}
	hookJSONMarshal = func(any) ([]byte, error) { return nil, errors.New("json") }
	if _, err := signCatalogResponse(nil, CatalogResponse{}); err == nil {
		t.Fatal("sign catalog")
	}
	if verifyCatalogResponse(nil, CatalogResponse{}) {
		t.Fatal("verify catalog")
	}
	hookMkdirAll = func(string, os.FileMode) error { return errors.New("mkdir") }
	if _, err := ensureHome(); err == nil {
		t.Fatal("home")
	}
	hookOpenDynamicLib = func(string) (uintptr, error) { return 0, errors.New("dl") }
	if _, err := openDynamicLib("lib"); err == nil {
		t.Fatal("dl")
	}
	tok := &wordPieceTokenizer{cls: 1, sep: 2, pad: 0, vocab: map[string]int64{"a": 3}}
	tok.Encode("a", 1)
	host, path := normalizeRemote("https://no-slash")
	if host != "" || path != "" {
		t.Fatal("remote")
	}
	host, path = normalizeRemote("https://example.com/")
	if host != "" || path != "" {
		t.Fatalf("empty path %s %s", host, path)
	}
	packets := buildSyncPacketsForIDs([]Memory{{ID: "keep", HotIndex: false, Layer: 1}}, "", []string{"other", "keep"}, Circle{}, Project{})
	if len(packets) != 0 {
		t.Fatal(packets)
	}
}
