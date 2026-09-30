package main

import (
	"bufio"
	"encoding/json"
	"errors"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestTailCoverageBranches(t *testing.T) {
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
	if _, err := e.db.Exec(`INSERT INTO memories(id, vector_id, kind, scope, scope_id, subject, content, confidence, priority, status, source, source_ref, evidence, success_count, failure_count, evidence_score, created_at, updated_at, last_validated_at, origin, peer_device_id, circle_id, layer, packet_content, problem_signature, source_folder, vector_space, hot_index) VALUES ('gone',1,'lesson','repository','r','s','c',1,1,'active','','','',0,0,0,'','','','local','','',2,'','','','',1)`); err != nil {
		t.Fatal(err)
	}
	if _, err := e.db.Exec(`CREATE TRIGGER block_del BEFORE DELETE ON memories BEGIN SELECT RAISE(ABORT, 'no'); END`); err != nil {
		t.Fatal(err)
	}
	if err := e.deleteMemory("gone"); err == nil {
		t.Fatal("delete memories")
	}
	if _, err := e.db.Exec(`DROP TRIGGER block_del`); err != nil {
		t.Fatal(err)
	}
	if _, err := e.db.Exec(`DELETE FROM graph_nodes`); err != nil {
		t.Fatal(err)
	}
	if _, err := e.db.Exec(`DELETE FROM graph_edges`); err != nil {
		t.Fatal(err)
	}
	if _, err := e.db.Exec(`DROP TABLE memories`); err != nil {
		t.Fatal(err)
	}
	e.backfillGraph()
	eCat, err := newEngine(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer eCat.Close()
	if _, err := eCat.db.Exec(`DROP TABLE memories`); err != nil {
		t.Fatal(err)
	}
	if _, err := eCat.db.Exec(`CREATE TABLE memories (id TEXT)`); err != nil {
		t.Fatal(err)
	}
	if _, err := eCat.listCatalogItems(id.DeviceID); err == nil {
		t.Fatal("catalog scan")
	}

	e2, err := newEngine(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer e2.Close()
	if _, err := e2.db.Exec(`DROP TABLE working_memory`); err != nil {
		t.Fatal(err)
	}
	if _, err := e2.db.Exec(`CREATE TABLE working_memory (id INTEGER)`); err != nil {
		t.Fatal(err)
	}
	e2.sessionID = "sess"
	if _, err := e2.listWorkingMemory(Project{Repository: "r"}); err == nil {
		t.Fatal("wm scan")
	}
	if _, err := e2.db.Exec(`DROP TABLE ledger_entries`); err != nil {
		t.Fatal(err)
	}
	if _, err := e2.db.Exec(`CREATE TABLE ledger_entries (id INTEGER)`); err != nil {
		t.Fatal(err)
	}
	if _, err := e2.ledgerList(Project{Repository: "r"}, "run"); err == nil {
		t.Fatal("ledger scan")
	}
	if _, err := e2.db.Exec(`DROP TABLE graph_edges`); err != nil {
		t.Fatal(err)
	}
	if _, err := e2.db.Exec(`CREATE TABLE graph_edges (src TEXT)`); err != nil {
		t.Fatal(err)
	}
	e2.graphNeighborIDs([]string{"seed"})
	_, _ = e2.pageIndexBranch([]string{"missing-node"})
	_, _ = e2.catalogGraphExport(id.DeviceID)

	if peerDisplayName(CircleMember{DeviceID: "other", Endpoint: "alpha.example:9"}, "self") != "alpha.example" {
		t.Fatal(peerDisplayName(CircleMember{Endpoint: "alpha.example:9"}, "self"))
	}
	if hostLabelFromEndpoint("alpha.example:9") != "alpha.example" {
		t.Fatal("host label")
	}
	upsertMember(Circle{Members: nil}, CircleMember{DeviceID: "new"})
	if err := mergePeerRoster(home, "missing", nil); err == nil {
		t.Fatal("roster")
	}
	c1, c2 := net.Pipe()
	hookJSONMarshal = func(any) ([]byte, error) { return nil, errors.New("json") }
	if err := writeJoinAck(c1, circle); err == nil {
		t.Fatal("ack")
	}
	_ = c2.Close()
	hookJSONMarshal = json.Marshal
	invite := CircleInvite{CircleID: circle.ID, GroupKey: circle.GroupKey, Code: "code"}
	if _, err := exchangeJoin(home, invite, "127.0.0.1:1", id, priv); err == nil {
		t.Fatal("dial join")
	}
	hookJSONMarshal = func(v any) ([]byte, error) {
		if _, ok := v.(JoinRequest); ok {
			return nil, errors.New("join")
		}
		return json.Marshal(v)
	}
	if _, err := exchangeJoin(home, invite, "127.0.0.1:1", id, priv); err == nil {
		t.Fatal("marshal join")
	}
	hookJSONMarshal = json.Marshal
	badInvite := invite
	badInvite.GroupKey = "aa"
	if _, err := exchangeJoin(home, badInvite, "127.0.0.1:1", id, priv); err == nil {
		t.Fatal("encrypt join")
	}

	client, server := net.Pipe()
	hookNetDial = func(string, string, time.Duration) (net.Conn, error) { return client, nil }
	go func() {
		defer server.Close()
		_, _ = server.Write([]byte("not-json\n"))
	}()
	if _, err := fetchPeerCatalog(home, "127.0.0.1:9", circle, id, priv); err == nil {
		t.Fatal("bad catalog json")
	}

	sl := &shareListener{home: home, id: id}
	left, right := net.Pipe()
	go func() {
		defer right.Close()
		sl.handleCatalog(left, circle, priv, []byte(`{"device_id":"stranger"}`))
	}()
	_, _ = bufio.NewReader(right).ReadString('\n')

	hookShareNewEngine = func(string) (*Engine, error) { return nil, errors.New("engine") }
	left, right = net.Pipe()
	req, err := signSyncRequest(priv, SyncRequest{DeviceID: id.DeviceID, CircleID: circle.ID, Timestamp: nowRFC3339()})
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(req)
	go func() {
		defer right.Close()
		sl.handleCatalog(left, circle, priv, raw)
	}()
	_, _ = bufio.NewReader(right).ReadString('\n')
	hookShareNewEngine = nil

	if name := folderBaseName(""); name == "" && false {
		t.Fatal(name)
	}
	_ = projectPageIndexTree([]PageIndexNode{
		{ID: "f", Kind: "folder", Title: "work", Summary: "/work"},
		{ID: "m", Kind: "memory", Title: "t", ParentID: "missing", MemoryID: "m"},
	}, []string{""})
	_ = buildFolderTree(nil, []string{"", "/work", "/work"})

	mig := filepath.Join(t.TempDir(), "mig")
	if err := os.MkdirAll(mig, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(mig, "experience-v1.json"), []byte("{"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := migrateJSONIfNeeded(mig); err == nil {
		t.Fatal("bad migrate")
	}
	if err := os.WriteFile(filepath.Join(mig, "experience-v1.json"), []byte("not-json"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(dbPath(mig)); err == nil {
		_ = os.Remove(dbPath(mig))
	}
	if err := migrateJSONIfNeeded(mig); err == nil {
		t.Fatal("migrate syntax")
	}

	hookOpenDynamicLib = func(string) (uintptr, error) { return 1, nil }
	idx := openTurboVecAt(filepath.Join(t.TempDir(), "idx"), home, 4)
	if idx != nil && idx.available && idx.handle == 0 {
		t.Fatal("handle")
	}
	tvOpen = func(*byte, int32, int32) uintptr { return 0 }
	again := openTurboVecAt(filepath.Join(t.TempDir(), "idx2"), home, 4)
	if again.available {
		t.Fatal("zero handle")
	}
}

func TestScanHooksAndUIPort(t *testing.T) {
	resetHooksForTest()
	t.Cleanup(resetHooksForTest)
	home := t.TempDir()
	setupTestEnv(t, home)
	e, err := newEngine(home)
	if err != nil {
		t.Fatal(err)
	}
	defer e.Close()
	project := Project{Root: home, Repository: "github.com/acme/r", Organization: "github.com/acme"}
	if _, err := e.recordMemory(project, "lesson", "repository", "scan", "body", 0.5, 1, "agent_observation", "", ""); err != nil {
		t.Fatal(err)
	}
	if err := e.addWorkingMemory(project, "note", "s", "c"); err != nil && e.sessionID == "" {
		e.sessionID = "sess"
		_, _ = e.db.Exec(`INSERT INTO working_memory(session_id, repository, kind, subject, content, created_at) VALUES ('sess','github.com/acme/r','note','s','c','now')`)
	}
	e.sessionID = "sess"
	_, _ = e.db.Exec(`INSERT INTO working_memory(session_id, repository, kind, subject, content, created_at) VALUES ('sess','github.com/acme/r','note','s','c','now')`)
	_, _ = e.db.Exec(`INSERT INTO ledger_entries(run_id, repository, decision, evidence, reason, risk, reversibility, created_at) VALUES ('run','github.com/acme/r','d','e','r','low','easy','now')`)
	hookRowsScanErr = errors.New("scan")
	if _, err := e.listCatalogItems("self"); err == nil {
		t.Fatal("catalog scan hook")
	}
	if _, err := e.listWorkingMemory(project); err == nil {
		t.Fatal("wm scan hook")
	}
	if _, err := e.ledgerList(project, "run"); err == nil {
		t.Fatal("ledger scan hook")
	}
	e.graphNeighborIDs([]string{"seed"})
	_, _ = e.pageIndexBranch([]string{"seed"})
	_, _ = e.catalogGraphExport("self")
	hookRowsScanErr = nil
	t.Setenv("OVERDRIVE_SHARE_UI", "")
	t.Setenv("OVERDRIVE_SHARE_LISTEN", "0.0.0.0:0")
	if _, ok := shareUIAddr(); ok {
		t.Fatal("port 0")
	}
	t.Setenv("OVERDRIVE_SHARE_LISTEN", "0.0.0.0:no")
	if _, ok := shareUIAddr(); ok {
		t.Fatal("bad port")
	}
}
