package main

import (
	"bufio"
	"encoding/json"
	"net"
	"testing"
)

func serveOnce(t *testing.T, write func(net.Conn)) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		_, _ = bufio.NewReader(conn).ReadString('\n')
		if write != nil {
			write(conn)
		}
	}()
	return ln.Addr().String()
}

func TestCatalogErrorMatrix(t *testing.T) {
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
	var ids []string
	for i := 0; i < 10; i++ {
		m, err := e.recordMemory(project, "lesson", "repository", "db.go n", "body", 0.9, 1, "verified_execution", "", "db.go")
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, m.ID)
	}
	for _, other := range ids[2:] {
		_ = e.upsertGraphEdge(graphMemoryNodeID(ids[0]), graphMemoryNodeID(other), "mentions")
	}
	_ = e.graphNeighborIDs([]string{ids[0], ids[1]})
	e.Close()

	addr := serveOnce(t, func(c net.Conn) { _, _ = c.Write([]byte("not-json\n")) })
	if _, err := fetchPeerCatalog(home, addr, circle, id, priv); err == nil {
		t.Fatal("json")
	}
	addr = serveOnce(t, func(c net.Conn) {
		out, _ := json.Marshal(wireEnvelope{CircleID: circle.ID, Ciphertext: "%%%"})
		_, _ = c.Write(append(out, '\n'))
	})
	if _, err := fetchPeerCatalog(home, addr, circle, id, priv); err == nil {
		t.Fatal("decrypt")
	}
	plain, _ := json.Marshal(map[string]any{"nope": true})
	cipher, err := encryptBatch(circle.GroupKey, plain)
	if err != nil {
		t.Fatal(err)
	}
	addr = serveOnce(t, func(c net.Conn) {
		out, _ := json.Marshal(wireEnvelope{CircleID: circle.ID, Ciphertext: cipher})
		_, _ = c.Write(append(out, '\n'))
	})
	if _, err := fetchPeerCatalog(home, addr, circle, id, priv); err == nil {
		t.Fatal("catalog body")
	}
	resp, err := signCatalogResponse(priv, CatalogResponse{
		DeviceID: id.DeviceID,
		CircleID: circle.ID,
		Items:    []CatalogItem{{ID: "loose", Kind: "lesson", Subject: "s"}},
		Folders:  []string{repo},
		Tree:     []PageIndexNode{{ID: "folder:x", Kind: "folder", Title: "repo", Summary: repo}},
	})
	if err != nil {
		t.Fatal(err)
	}
	body, _ := json.Marshal(resp)
	wire, err := encryptBatch(circle.GroupKey, body)
	if err != nil {
		t.Fatal(err)
	}
	addr = serveOnce(t, func(c net.Conn) {
		out, _ := json.Marshal(wireEnvelope{CircleID: circle.ID, Ciphertext: wire})
		_, _ = c.Write(append(out, '\n'))
	})
	if _, err := fetchPeerCatalog(home, addr, circle, id, priv); err != nil {
		t.Fatal(err)
	}
	circle.PeerEndpoints = []string{addr, addr}
	if err := saveCircle(home, circle); err != nil {
		t.Fatal(err)
	}
	t.Setenv("OVERDRIVE_SHARE_LISTEN", addr)
	_ = peerDashboardContexts(home, "")
	t.Setenv("OVERDRIVE_SHARE_LISTEN", "127.0.0.1:1")
	_ = peerDashboardContexts(home, "")
}
