package main

import (
	"database/sql"
	"errors"
	"sort"
	"strings"
)

type graphStore interface {
	Exec(query string, args ...any) (sql.Result, error)
	Query(query string, args ...any) (*sql.Rows, error)
}

const maxGraphNeighbors = 8

var errGraphStoreUnavailable = errors.New("graph store unavailable")

func graphMemoryNodeID(id string) string {
	return "memory:" + strings.TrimSpace(id)
}

func graphNodeID(kind, key string) string {
	return kind + ":" + strings.TrimSpace(key)
}

func memoryGraphSummary(m Memory) string {
	parts := []string{}
	if k := strings.TrimSpace(m.Kind); k != "" {
		parts = append(parts, k)
	}
	folder := strings.TrimSpace(m.SourceFolder)
	if folder != "" && folder != "(no folder)" {
		parts = append(parts, folderBaseName(folder))
	}
	if s := strings.TrimSpace(m.Subject); s != "" {
		parts = append(parts, s)
	}
	seen := map[string]bool{}
	tokens := []string{}
	blob := strings.Join([]string{m.Subject, m.Evidence}, " ")
	for tok := range collectSignatureTokens(blob) {
		kind, key := classifySignatureToken(tok)
		if kind == "" || seen[key] {
			continue
		}
		seen[key] = true
		tokens = append(tokens, key)
	}
	sort.Strings(tokens)
	if len(tokens) > 0 {
		parts = append(parts, strings.Join(tokens, ", "))
	}
	return strings.Join(parts, " · ")
}

func isConcreteErrorToken(tok string) bool {
	if httpCodePattern.MatchString(tok) {
		return true
	}
	if !errCodePattern.MatchString(tok) {
		return false
	}
	for _, r := range tok {
		if r == '_' || (r >= '0' && r <= '9') {
			return true
		}
	}
	return false
}

func classifySignatureToken(tok string) (kind, key string) {
	tok = strings.TrimSpace(tok)
	if tok == "" {
		return "", ""
	}
	if pathPattern.MatchString(tok) {
		return "path", tok
	}
	if symbolPattern.MatchString(tok) {
		return "symbol", tok
	}
	if isConcreteErrorToken(tok) {
		return "error", tok
	}
	return "", ""
}

func graphSchemaStmts() []string {
	return []string{
		`CREATE TABLE IF NOT EXISTS graph_nodes (
			id TEXT PRIMARY KEY,
			kind TEXT NOT NULL,
			title TEXT NOT NULL DEFAULT '',
			summary TEXT NOT NULL DEFAULT '',
			parent_id TEXT NOT NULL DEFAULT '',
			memory_id TEXT NOT NULL DEFAULT '',
			updated_at TEXT NOT NULL DEFAULT ''
		)`,
		`CREATE TABLE IF NOT EXISTS graph_edges (
			src TEXT NOT NULL,
			dst TEXT NOT NULL,
			kind TEXT NOT NULL,
			PRIMARY KEY (src, dst, kind)
		)`,
		`CREATE INDEX IF NOT EXISTS graph_edges_dst ON graph_edges(dst)`,
		`CREATE INDEX IF NOT EXISTS graph_nodes_parent ON graph_nodes(parent_id)`,
		`CREATE INDEX IF NOT EXISTS graph_nodes_memory ON graph_nodes(memory_id)`,
	}
}

func (e *Engine) graphDB() graphStore {
	if e == nil || e.db == nil {
		return nil
	}
	return e.db
}

func (e *Engine) upsertGraphNode(n PageIndexNode) error {
	return upsertGraphNodeOn(e.graphDB(), n)
}

func (e *Engine) upsertGraphEdge(src, dst, kind string) error {
	return upsertGraphEdgeOn(e.graphDB(), src, dst, kind)
}

func upsertGraphNodeOn(store graphStore, n PageIndexNode) error {
	if store == nil {
		return errGraphStoreUnavailable
	}
	if strings.TrimSpace(n.ID) == "" {
		return nil
	}
	_, err := store.Exec(`INSERT INTO graph_nodes(id, kind, title, summary, parent_id, memory_id, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			kind=excluded.kind, title=excluded.title, summary=excluded.summary,
			parent_id=excluded.parent_id, memory_id=excluded.memory_id, updated_at=excluded.updated_at`,
		n.ID, n.Kind, n.Title, n.Summary, n.ParentID, n.MemoryID, n.UpdatedAt)
	return err
}

func upsertGraphEdgeOn(store graphStore, src, dst, kind string) error {
	if store == nil {
		return errGraphStoreUnavailable
	}
	if src == "" || dst == "" || kind == "" {
		return nil
	}
	_, err := store.Exec(`INSERT OR IGNORE INTO graph_edges(src, dst, kind) VALUES (?, ?, ?)`, src, dst, kind)
	return err
}

func removeMemoryGraphOn(store graphStore, id string) error {
	if store == nil {
		return errGraphStoreUnavailable
	}
	if strings.TrimSpace(id) == "" {
		return nil
	}
	node := graphMemoryNodeID(id)
	if _, err := store.Exec(`DELETE FROM graph_edges WHERE src = ? OR dst = ?`, node, node); err != nil {
		return err
	}
	_, err := store.Exec(`DELETE FROM graph_nodes WHERE id = ? OR memory_id = ?`, node, id)
	return err
}

func syncMemoryGraphOn(store graphStore, m Memory, extra []GraphEdge) error {
	if store == nil {
		return errGraphStoreUnavailable
	}
	if strings.TrimSpace(m.ID) == "" {
		return nil
	}
	if m.Status != "active" {
		return removeMemoryGraphOn(store, m.ID)
	}
	if err := writeMemoryGraphOn(store, m); err != nil {
		return err
	}
	for _, edge := range extra {
		if err := upsertGraphEdgeOn(store, edge.Src, edge.Dst, edge.Kind); err != nil {
			return err
		}
	}
	return nil
}

func writeMemoryGraphOn(store graphStore, m Memory) error {
	if store == nil {
		return errGraphStoreUnavailable
	}
	memID := graphMemoryNodeID(m.ID)
	if _, err := store.Exec(`DELETE FROM graph_edges WHERE src = ? OR (dst = ? AND kind IN ('same_folder', 'mentions', 'from_device'))`, memID, memID); err != nil {
		return err
	}

	repoKey := strings.TrimSpace(m.ScopeID)
	if repoKey == "" {
		repoKey = "unknown"
	}
	folderKey := strings.TrimSpace(m.SourceFolder)
	if folderKey == "" {
		folderKey = "(no folder)"
	}
	kindKey := strings.TrimSpace(m.Kind)
	if kindKey == "" {
		kindKey = "note"
	}
	repoID := graphNodeID("repository", repoKey)
	folderID := graphNodeID("folder", repoKey+"|"+folderKey)
	kindID := graphNodeID("kind", repoKey+"|"+folderKey+"|"+kindKey)
	updated := m.UpdatedAt
	if updated == "" {
		updated = nowRFC3339()
	}
	if err := upsertGraphNodeOn(store, PageIndexNode{ID: repoID, Kind: "repository", Title: repoKey, UpdatedAt: updated}); err != nil {
		return err
	}
	if err := upsertGraphNodeOn(store, PageIndexNode{
		ID: folderID, Kind: "folder", Title: folderBaseName(folderKey), Summary: folderKey,
		ParentID: repoID, UpdatedAt: updated,
	}); err != nil {
		return err
	}
	if err := upsertGraphNodeOn(store, PageIndexNode{
		ID: kindID, Kind: "kind", Title: kindKey, ParentID: folderID, UpdatedAt: updated,
	}); err != nil {
		return err
	}
	if err := upsertGraphNodeOn(store, PageIndexNode{
		ID: memID, Kind: "memory", Title: strings.TrimSpace(m.Subject), Summary: memoryGraphSummary(m),
		ParentID: kindID, MemoryID: m.ID, UpdatedAt: updated,
	}); err != nil {
		return err
	}

	blob := strings.Join([]string{m.Subject, m.Evidence}, " ")
	for tok := range collectSignatureTokens(blob) {
		kind, key := classifySignatureToken(tok)
		if kind == "" {
			continue
		}
		dst := graphNodeID(kind, key)
		if err := upsertGraphNodeOn(store, PageIndexNode{ID: dst, Kind: kind, Title: key, UpdatedAt: updated}); err != nil {
			return err
		}
		if err := upsertGraphEdgeOn(store, memID, dst, "mentions"); err != nil {
			return err
		}
	}

	if m.Origin == "peer" && m.PeerDeviceID != "" {
		devID := graphNodeID("device", m.PeerDeviceID)
		if err := upsertGraphNodeOn(store, PageIndexNode{ID: devID, Kind: "device", Title: m.PeerDeviceID, UpdatedAt: updated}); err != nil {
			return err
		}
		if err := upsertGraphEdgeOn(store, memID, devID, "from_device"); err != nil {
			return err
		}
	}
	return nil
}

func (e *Engine) backfillGraph() {
	if e == nil || e.db == nil {
		return
	}
	var n int
	if e.db.QueryRow(`SELECT COUNT(*) FROM graph_nodes`).Scan(&n) != nil || n > 0 {
		return
	}
	memories, err := e.listActiveMemories()
	if err != nil {
		return
	}
	for _, m := range memories {
		_ = writeMemoryGraphOn(e.db, m)
	}
}

func (e *Engine) graphNeighborIDs(seedIDs []string) []string {
	if e == nil || e.db == nil || len(seedIDs) == 0 {
		return nil
	}
	seen := map[string]bool{}
	for _, id := range seedIDs {
		seen[id] = true
	}
	out := []string{}
	add := func(id string) {
		id = strings.TrimSpace(strings.TrimPrefix(id, "memory:"))
		if id == "" || seen[id] || len(out) >= maxGraphNeighbors {
			return
		}
		seen[id] = true
		out = append(out, id)
	}
	for _, id := range seedIDs {
		if len(out) >= maxGraphNeighbors {
			break
		}
		src := graphMemoryNodeID(id)
		rows, err := e.db.Query(`SELECT dst, kind FROM graph_edges WHERE src = ?`, src)
		if err != nil {
			continue
		}
		type hop struct {
			dst, kind string
		}
		hops := []hop{}
		for rows.Next() {
			var dst, kind string
			if hookRowsScanErr != nil {
				err = hookRowsScanErr
			} else {
				err = rows.Scan(&dst, &kind)
			}
			if err != nil {
				continue
			}
			hops = append(hops, hop{dst: dst, kind: kind})
		}
		rows.Close()
		for _, h := range hops {
			switch h.kind {
			case "supersedes":
				if strings.HasPrefix(h.dst, "memory:") {
					add(h.dst)
				}
			case "mentions":
				if strings.HasPrefix(h.dst, "memory:") {
					add(h.dst)
					continue
				}
				others, err := e.db.Query(`SELECT src FROM graph_edges WHERE dst = ? AND kind = 'mentions' AND src != ?`, h.dst, src)
				if err != nil {
					continue
				}
				for others.Next() {
					var other string
					if others.Scan(&other) == nil {
						add(other)
					}
				}
				others.Close()
			}
		}
	}
	return out
}

func (e *Engine) loadGraphNode(id string) (PageIndexNode, bool) {
	var n PageIndexNode
	if e == nil || e.db == nil || id == "" {
		return n, false
	}
	err := e.db.QueryRow(`SELECT id, kind, title, summary, parent_id, memory_id, updated_at FROM graph_nodes WHERE id = ?`, id).
		Scan(&n.ID, &n.Kind, &n.Title, &n.Summary, &n.ParentID, &n.MemoryID, &n.UpdatedAt)
	return n, err == nil
}

func (e *Engine) pageIndexBranch(memoryIDs []string) ([]PageIndexNode, []GraphEdge) {
	nodes := []PageIndexNode{}
	seen := map[string]bool{}
	addNode := func(n PageIndexNode) {
		if n.ID == "" || seen[n.ID] {
			return
		}
		seen[n.ID] = true
		nodes = append(nodes, n)
	}
	walkParents := func(id string) {
		for i := 0; i < 6 && id != ""; i++ {
			n, ok := e.loadGraphNode(id)
			if !ok {
				return
			}
			addNode(n)
			id = n.ParentID
		}
	}
	for _, id := range uniqueStrings(memoryIDs) {
		mem := graphMemoryNodeID(id)
		walkParents(mem)
		if n, ok := e.loadGraphNode(mem); ok && n.ParentID != "" {
			rows, err := e.db.Query(`SELECT id, kind, title, summary, parent_id, memory_id, updated_at FROM graph_nodes WHERE parent_id = ? LIMIT 12`, n.ParentID)
			if err == nil {
				for rows.Next() {
					var sib PageIndexNode
					if rows.Scan(&sib.ID, &sib.Kind, &sib.Title, &sib.Summary, &sib.ParentID, &sib.MemoryID, &sib.UpdatedAt) == nil {
						addNode(sib)
					}
				}
				rows.Close()
			}
		}
		edgeRows, err := e.db.Query(`SELECT dst FROM graph_edges WHERE src = ? AND kind = 'mentions'`, mem)
		if err == nil {
			for edgeRows.Next() {
				var dst string
				if edgeRows.Scan(&dst) == nil {
					if n, ok := e.loadGraphNode(dst); ok {
						addNode(n)
					}
				}
			}
			edgeRows.Close()
		}
	}
	sort.SliceStable(nodes, func(i, j int) bool {
		if nodes[i].Kind != nodes[j].Kind {
			return nodes[i].Kind < nodes[j].Kind
		}
		return nodes[i].Title < nodes[j].Title
	})
	idSet := map[string]bool{}
	for _, n := range nodes {
		idSet[n.ID] = true
	}
	edges := []GraphEdge{}
	for _, n := range nodes {
		rows, err := e.db.Query(`SELECT src, dst, kind FROM graph_edges WHERE src = ?`, n.ID)
		if err != nil {
			continue
		}
		for rows.Next() {
			var edge GraphEdge
			if hookRowsScanErr != nil {
				err = hookRowsScanErr
			} else {
				err = rows.Scan(&edge.Src, &edge.Dst, &edge.Kind)
			}
			if err != nil {
				continue
			}
			if idSet[edge.Dst] {
				edges = append(edges, edge)
			}
		}
		rows.Close()
	}
	return nodes, edges
}

func (e *Engine) catalogGraphExport(selfID string) ([]PageIndexNode, []GraphEdge) {
	if e == nil || e.db == nil {
		return []PageIndexNode{}, []GraphEdge{}
	}
	ids := []string{}
	rows, err := e.db.Query(`SELECT id FROM memories WHERE status = 'active' AND origin = 'local'`)
	if err != nil {
		return []PageIndexNode{}, []GraphEdge{}
	}
	defer rows.Close()
	for rows.Next() {
		var id string
		if hookRowsScanErr != nil {
			err = hookRowsScanErr
		} else {
			err = rows.Scan(&id)
		}
		if err != nil {
			continue
		}
		m, err := e.getMemory(id)
		if err != nil || !shareableKind(m.Kind) {
			continue
		}
		ids = append(ids, id)
	}
	_ = selfID
	nodes, edges := e.pageIndexBranch(ids)
	return nodes, edges
}

func (e *Engine) importCatalogGraph(resp CatalogResponse) {
	for _, n := range resp.Tree {
		if n.ID == "" {
			continue
		}
		n.Summary = strings.TrimSpace(n.Summary)
		_ = e.upsertGraphNode(n)
	}
	for _, edge := range resp.Edges {
		_ = e.upsertGraphEdge(edge.Src, edge.Dst, edge.Kind)
	}
}

func (e *Engine) unresolvedPageIndexIDs(nodes []PageIndexNode, query string) []string {
	q := strings.ToLower(strings.TrimSpace(query))
	out := []string{}
	seen := map[string]bool{}
	for _, n := range nodes {
		if n.Kind != "memory" || n.MemoryID == "" || seen[n.MemoryID] {
			continue
		}
		if _, err := e.getMemory(n.MemoryID); err == nil {
			continue
		}
		blob := strings.ToLower(n.Title + " " + n.Summary)
		if q != "" && !strings.Contains(blob, q) {
			matched := false
			for _, tok := range tokenize(q) {
				if strings.Contains(blob, strings.ToLower(tok)) {
					matched = true
					break
				}
			}
			if !matched {
				continue
			}
		}
		seen[n.MemoryID] = true
		out = append(out, n.MemoryID)
		if len(out) >= maxGraphNeighbors {
			break
		}
	}
	return out
}

func uniqueStrings(in []string) []string {
	seen := map[string]bool{}
	out := []string{}
	for _, s := range in {
		s = strings.TrimSpace(s)
		if s == "" || seen[s] {
			continue
		}
		seen[s] = true
		out = append(out, s)
	}
	return out
}

func (e *Engine) loadGraphNeighborMemories(ids []string, skip map[string]bool, project Project) []Memory {
	out := []Memory{}
	for _, id := range ids {
		if skip[id] {
			continue
		}
		m, err := e.getMemory(id)
		if err != nil || m.Status != "active" || !m.HotIndex {
			continue
		}
		if m.Origin == "peer" {
			if !e.peerEligible(m, project) {
				continue
			}
		} else if scopeWeight(m, project) == 0 {
			continue
		}
		skip[id] = true
		out = append(out, m)
		if len(out) >= maxGraphNeighbors {
			break
		}
	}
	return out
}

func (e *Engine) fetchMissingPeerPackets(project Project, ids []string) {
	ids = uniqueStrings(ids)
	if e == nil || len(ids) == 0 {
		return
	}
	circle, ok := circleForProject(e.home, project.Root)
	if !ok || !folderAllowed(circle, project.Root) {
		return
	}
	id, priv, err := loadOrCreateIdentity(e.home)
	if err != nil {
		return
	}
	selfListen := advertisedListenAddr()
	for _, endpoint := range nonEmptyEndpoints(circle.PeerEndpoints) {
		if endpoint == selfListen {
			continue
		}
		resp, err := fetchPeerSyncIDs(e.home, endpoint, circle, id, priv, project.Repository, ids)
		if err != nil {
			continue
		}
		_, _ = e.importPeerBatch(circle, resp)
	}
}

func (e *Engine) importPeerCatalogs(project Project) {
	circle, ok := circleForProject(e.home, project.Root)
	if !ok {
		return
	}
	id, priv, err := loadOrCreateIdentity(e.home)
	if err != nil {
		return
	}
	selfListen := advertisedListenAddr()
	for _, endpoint := range nonEmptyEndpoints(circle.PeerEndpoints) {
		if endpoint == selfListen {
			continue
		}
		resp, err := fetchPeerCatalog(e.home, endpoint, circle, id, priv)
		if err != nil {
			continue
		}
		e.importCatalogGraph(resp)
	}
}
