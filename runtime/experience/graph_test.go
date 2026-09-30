package main

import (
	"encoding/json"
	"errors"
	"net"
	"strings"
	"testing"
	"time"
)

func TestGraphHopFindsSamePathMemory(t *testing.T) {
	home := t.TempDir()
	setupTestEnv(t, home)
	e, err := newEngine(home)
	if err != nil {
		t.Fatal(err)
	}
	defer e.Close()
	project := Project{Root: home, Repository: "github.com/acme/r", Organization: "github.com/acme"}
	first, err := e.recordMemory(project, "lesson", "repository", "WAL lock in db.go", "Enable WAL mode for writers", 0.9, 50, "verified_execution", "", "see db.go")
	if err != nil {
		t.Fatal(err)
	}
	second, err := e.recordMemory(project, "lesson", "repository", "HTTP client timeout", "Raise the deadline", 0.9, 50, "verified_execution", "", "also db.go")
	if err != nil {
		t.Fatal(err)
	}
	resp, err := e.recall(project, "WAL lock", 12, "all")
	if err != nil {
		t.Fatal(err)
	}
	foundHit := false
	for _, m := range resp.Memories {
		if m.ID == first.ID {
			foundHit = true
		}
		if m.ID == second.ID {
			t.Fatalf("second memory ranked without query match: %+v", m)
		}
	}
	if !foundHit {
		t.Fatalf("expected first memory in recall: %+v", idsOf(resp.Memories))
	}
	foundNeighbor := false
	for _, m := range resp.GraphNeighbors {
		if m.ID == second.ID {
			foundNeighbor = true
		}
	}
	if !foundNeighbor {
		t.Fatalf("expected graph neighbor %s, got %v page=%v", second.ID, idsOf(resp.GraphNeighbors), resp.PageIndex)
	}
	raw, _ := json.Marshal(resp.PageIndex)
	if strings.Contains(string(raw), "Enable WAL mode for writers") || strings.Contains(string(raw), "Raise the deadline") {
		t.Fatalf("page_index leaked lesson body: %s", raw)
	}
	sawFirst, sawSecond := false, false
	for _, n := range resp.PageIndex {
		if n.MemoryID == first.ID {
			sawFirst = true
		}
		if n.MemoryID == second.ID {
			sawSecond = true
		}
	}
	if !sawFirst || !sawSecond {
		t.Fatalf("page_index missing memories first=%v second=%v nodes=%s", sawFirst, sawSecond, raw)
	}
}

func TestCatalogGraphOmitsSecrets(t *testing.T) {
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
	circle.AllowedFolders = []string{"/srv/workstation"}
	if err := saveCircle(home, circle); err != nil {
		t.Fatal(err)
	}
	e, err := newEngine(home)
	if err != nil {
		t.Fatal(err)
	}
	defer e.Close()
	project := Project{Root: "/srv/workstation", Repository: "github.com/acme/r", Organization: "github.com/acme"}
	if _, err := e.recordMemory(project, "lesson", "repository", "Use WAL", "secret body should not leak", 0.9, 50, "verified_execution", "", "db.go"); err != nil {
		t.Fatal(err)
	}
	resp, err := e.buildCatalogResponse(circle, id, priv)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(resp)
	if err != nil {
		t.Fatal(err)
	}
	body := string(raw)
	if strings.Contains(body, "group_key") || strings.Contains(body, circle.GroupKey) || strings.Contains(body, "secret body should not leak") {
		t.Fatalf("catalog leaked secrets: %s", body)
	}
	if len(resp.Tree) == 0 {
		t.Fatal("expected page-index tree in catalog")
	}
}

func TestGraphTokensSkipGenericAcronyms(t *testing.T) {
	home := t.TempDir()
	setupTestEnv(t, home)
	e, err := newEngine(home)
	if err != nil {
		t.Fatal(err)
	}
	defer e.Close()
	project := Project{Root: home, Repository: "github.com/acme/r", Organization: "github.com/acme"}
	m, err := e.recordMemory(project, "lesson", "repository", "WAL HTTP FTS", "secret lesson body", 0.9, 50, "verified_execution", "", "db.go SQLITE_BUSY E500")
	if err != nil {
		t.Fatal(err)
	}
	rows, err := e.db.Query(`SELECT kind, title FROM graph_nodes`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	errorNodes := map[string]bool{}
	pathNodes := map[string]bool{}
	for rows.Next() {
		var kind, title string
		if err := rows.Scan(&kind, &title); err != nil {
			t.Fatal(err)
		}
		switch kind {
		case "error":
			errorNodes[title] = true
		case "path":
			pathNodes[title] = true
		}
	}
	if errorNodes["WAL"] || errorNodes["HTTP"] || errorNodes["FTS"] {
		t.Fatalf("generic acronyms became error nodes: %v", errorNodes)
	}
	if !errorNodes["SQLITE_BUSY"] || !errorNodes["E500"] {
		t.Fatalf("expected concrete error nodes, got %v", errorNodes)
	}
	if !pathNodes["db.go"] {
		t.Fatalf("expected db.go path node, got %v", pathNodes)
	}
	card := memoryGraphSummary(m)
	if strings.Contains(card, "secret lesson body") {
		t.Fatalf("index card leaked lesson body: %s", card)
	}
	if !strings.Contains(card, "lesson") || !strings.Contains(card, "SQLITE_BUSY") || !strings.Contains(card, "db.go") {
		t.Fatalf("index card missing concrete fields: %s", card)
	}
}

func TestRecordFailsWhenGraphStoreClosed(t *testing.T) {
	home := t.TempDir()
	setupTestEnv(t, home)
	e, err := newEngine(home)
	if err != nil {
		t.Fatal(err)
	}
	defer e.Close()
	if _, err := e.db.Exec(`DROP TABLE graph_nodes`); err != nil {
		t.Fatal(err)
	}
	project := Project{Root: home, Repository: "github.com/acme/r", Organization: "github.com/acme"}
	if _, err := e.recordMemory(project, "lesson", "repository", "Use WAL", "body", 0.9, 50, "verified_execution", "", "db.go"); err == nil {
		t.Fatal("expected record to fail when graph write fails")
	}
	var n int
	if err := e.db.QueryRow(`SELECT COUNT(*) FROM memories`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatalf("memory persisted after graph failure: %d", n)
	}
}

func TestMentionsIgnoreLessonBody(t *testing.T) {
	home := t.TempDir()
	setupTestEnv(t, home)
	e, err := newEngine(home)
	if err != nil {
		t.Fatal(err)
	}
	defer e.Close()
	project := Project{Root: home, Repository: "github.com/acme/r", Organization: "github.com/acme"}
	if _, err := e.recordMemory(project, "lesson", "repository", "plain timeout", "see secret.go SQLITE_BUSY", 0.9, 50, "verified_execution", "", "no path here"); err != nil {
		t.Fatal(err)
	}
	rows, err := e.db.Query(`SELECT kind, title FROM graph_nodes WHERE kind IN ('path', 'error')`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var kind, title string
		if err := rows.Scan(&kind, &title); err != nil {
			t.Fatal(err)
		}
		if title == "secret.go" || title == "SQLITE_BUSY" {
			t.Fatalf("body-only token became graph node: %s %s", kind, title)
		}
	}
}

func TestSameFolderDoesNotBecomeRecallNeighbor(t *testing.T) {
	home := t.TempDir()
	setupTestEnv(t, home)
	e, err := newEngine(home)
	if err != nil {
		t.Fatal(err)
	}
	defer e.Close()
	project := Project{Root: home, Repository: "github.com/acme/r", Organization: "github.com/acme"}
	first, err := e.recordMemory(project, "lesson", "repository", "WAL lock in db.go", "Enable WAL", 0.9, 50, "verified_execution", "", "see db.go")
	if err != nil {
		t.Fatal(err)
	}
	second, err := e.recordMemory(project, "lesson", "repository", "unrelated timeout", "Raise the deadline", 0.9, 50, "verified_execution", "", "http.go")
	if err != nil {
		t.Fatal(err)
	}
	resp, err := e.recall(project, "WAL lock", 12, "all")
	if err != nil {
		t.Fatal(err)
	}
	foundHit := false
	for _, m := range resp.Memories {
		if m.ID == first.ID {
			foundHit = true
		}
	}
	if !foundHit {
		t.Fatalf("expected first memory in recall: %+v", idsOf(resp.Memories))
	}
	for _, m := range resp.GraphNeighbors {
		if m.ID == second.ID {
			t.Fatalf("same-folder memory became graph neighbor: %s", second.ID)
		}
	}
}

func TestFetchPeerCatalogUsesShortDial(t *testing.T) {
	resetHooksForTest()
	t.Cleanup(resetHooksForTest)
	var got time.Duration
	hookNetDial = func(network, address string, timeout time.Duration) (net.Conn, error) {
		got = timeout
		return nil, errors.New("dial refused")
	}
	if _, err := fetchPeerCatalog(t.TempDir(), "127.0.0.1:1", Circle{ID: "c"}, DeviceIdentity{DeviceID: "d"}, nil); err == nil {
		t.Fatal("expected dial error")
	}
	if got != peerIDDialTimeout {
		t.Fatalf("catalog dial timeout = %s want %s", got, peerIDDialTimeout)
	}
}

func TestFetchPeerSyncIDsUsesShortDial(t *testing.T) {
	resetHooksForTest()
	t.Cleanup(resetHooksForTest)
	var got time.Duration
	hookNetDial = func(network, address string, timeout time.Duration) (net.Conn, error) {
		got = timeout
		return nil, errors.New("dial refused")
	}
	if _, err := fetchPeerSyncIDs(t.TempDir(), "127.0.0.1:1", Circle{ID: "c"}, DeviceIdentity{DeviceID: "d"}, nil, "repo", []string{"mem-1"}); err == nil {
		t.Fatal("expected dial error")
	}
	if got != peerIDDialTimeout {
		t.Fatalf("id fetch dial timeout = %s want %s", got, peerIDDialTimeout)
	}
}

func TestProjectPageIndexTreeUsesFolderNodes(t *testing.T) {
	nodes := []PageIndexNode{
		{ID: "repo:r", Kind: "repository", Title: "github.com/acme/r"},
		{ID: "folder:r|/srv/workstation", Kind: "folder", Title: "workstation", Summary: "/srv/workstation", ParentID: "repo:r"},
		{ID: "kind:r|/srv/workstation|lesson", Kind: "kind", Title: "lesson", ParentID: "folder:r|/srv/workstation"},
		{ID: "memory:local-lesson", Kind: "memory", Title: "Use WAL", Summary: "lesson · workstation · Use WAL · db.go", ParentID: "kind:r|/srv/workstation|lesson", MemoryID: "local-lesson"},
	}
	folders := projectPageIndexTree(nodes, nil)
	if len(folders) != 1 || folders[0].Path != "/srv/workstation" || folders[0].Name != "workstation" {
		t.Fatalf("folder should come from PageIndex folder node: %+v", folders)
	}
	if len(folders[0].Kinds) != 1 || folders[0].Kinds[0].Kind != "lesson" {
		t.Fatalf("kind should come from PageIndex: %+v", folders[0].Kinds)
	}
	item := folders[0].Kinds[0].Items[0]
	if item.Subject != "Use WAL" || item.Summary == "" || strings.Contains(item.Summary, "secret") {
		t.Fatalf("unexpected memory card: %+v", item)
	}
}

func idsOf(memories []Memory) []string {
	out := make([]string, 0, len(memories))
	for _, m := range memories {
		out = append(out, m.ID)
	}
	return out
}
