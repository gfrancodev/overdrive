package main

import (
	"bufio"
	"crypto/ed25519"
	"encoding/json"
	"errors"
	"net"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

var errUnknownCatalogPeer = errors.New("unknown catalog peer")

type contextItem struct {
	ID        string `json:"id"`
	Subject   string `json:"subject"`
	Summary   string `json:"summary,omitempty"`
	UpdatedAt string `json:"updated_at"`
}

type contextKind struct {
	Kind  string        `json:"kind"`
	Items []contextItem `json:"items"`
}

type contextFolder struct {
	Path  string        `json:"path"`
	Name  string        `json:"name"`
	Kinds []contextKind `json:"kinds"`
}

type contextComputer struct {
	DeviceID string          `json:"device_id"`
	Name     string          `json:"name"`
	Self     bool            `json:"self"`
	Folders  []string        `json:"folders"`
	Tree     []contextFolder `json:"tree"`
}

type contextNetwork struct {
	Computers []contextComputer `json:"computers"`
}

func (e *Engine) listCatalogItems(selfID string) ([]CatalogItem, error) {
	if e == nil || e.db == nil {
		return nil, nil
	}
	rows, err := e.db.Query(`SELECT id, kind, subject, problem_signature, source_folder, scope_id, updated_at, origin, peer_device_id FROM memories WHERE status = 'active'`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []CatalogItem{}
	for rows.Next() {
		var item CatalogItem
		var origin, peerID, signature string
		if hookRowsScanErr != nil {
			err = hookRowsScanErr
		} else {
			err = rows.Scan(&item.ID, &item.Kind, &item.Subject, &signature, &item.SourceFolder, &item.Repository, &item.UpdatedAt, &origin, &peerID)
		}
		if err != nil {
			return nil, err
		}
		if !shareableKind(item.Kind) {
			continue
		}
		item.Summary = memoryGraphSummary(Memory{
			Kind: item.Kind, SourceFolder: item.SourceFolder, Subject: item.Subject, ProblemSignature: signature,
		})
		item.DeviceID = selfID
		if origin == "peer" && peerID != "" {
			item.DeviceID = peerID
		}
		out = append(out, item)
	}
	return out, rows.Err()
}

func (e *Engine) buildCatalogResponse(circle Circle, id DeviceIdentity, priv ed25519.PrivateKey) (CatalogResponse, error) {
	items, err := e.listCatalogItems(id.DeviceID)
	if err != nil {
		return CatalogResponse{}, err
	}
	local := []CatalogItem{}
	for _, item := range items {
		if item.DeviceID == id.DeviceID {
			local = append(local, item)
		}
	}
	tree, edges := e.catalogGraphExport(id.DeviceID)
	resp := CatalogResponse{
		DeviceID: id.DeviceID,
		CircleID: circle.ID,
		Hostname: computerName(),
		Folders:  append([]string{}, circle.AllowedFolders...),
		Items:    local,
		Tree:     tree,
		Edges:    edges,
		Members:  circle.Members,
	}
	return signCatalogResponse(priv, resp)
}

func (sl *shareListener) handleCatalog(conn net.Conn, circle Circle, priv ed25519.PrivateKey, plain []byte) {
	var req SyncRequest
	if err := json.Unmarshal(plain, &req); err != nil {
		return
	}
	member, ok := circle.activeMember(req.DeviceID)
	if !ok {
		return
	}
	pub, err := decodePublicKey(member.PublicKey)
	if err != nil || !verifySyncRequest(pub, req) {
		return
	}
	var e *Engine
	if hookShareNewEngine != nil {
		e, err = hookShareNewEngine(sl.home)
	} else {
		e, err = newEngine(sl.home)
	}
	if err != nil {
		return
	}
	defer e.Close()
	resp, err := e.buildCatalogResponse(circle, sl.id, priv)
	if err != nil {
		return
	}
	payload, err := hookJSONMarshal(resp)
	if err != nil {
		return
	}
	cipher, err := encryptBatch(circle.GroupKey, payload)
	if err != nil {
		return
	}
	out, _ := hookJSONMarshal(wireEnvelope{CircleID: circle.ID, Ciphertext: cipher, Kind: "catalog"})
	_, _ = conn.Write(append(out, '\n'))
}

func fetchPeerCatalog(home string, endpoint string, circle Circle, id DeviceIdentity, priv ed25519.PrivateKey) (CatalogResponse, error) {
	conn, err := hookNetDial("tcp", endpoint, peerIDDialTimeout)
	if err != nil {
		return CatalogResponse{}, err
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(peerIDDeadline))
	req := SyncRequest{
		DeviceID:   id.DeviceID,
		CircleID:   circle.ID,
		Timestamp:  nowRFC3339(),
		ListenAddr: advertisedListenAddr(),
		Hostname:   localHostname(),
	}
	req, err = signSyncRequest(priv, req)
	if err != nil {
		return CatalogResponse{}, err
	}
	payload, err := hookJSONMarshal(req)
	if err != nil {
		return CatalogResponse{}, err
	}
	cipher, err := encryptBatch(circle.GroupKey, payload)
	if err != nil {
		return CatalogResponse{}, err
	}
	out, _ := hookJSONMarshal(wireEnvelope{CircleID: circle.ID, Ciphertext: cipher, Kind: "catalog"})
	if _, err := conn.Write(append(out, '\n')); err != nil {
		return CatalogResponse{}, err
	}
	reader := bufio.NewReader(conn)
	line, err := reader.ReadString('\n')
	if err != nil {
		return CatalogResponse{}, err
	}
	var env wireEnvelope
	if err := json.Unmarshal([]byte(strings.TrimSpace(line)), &env); err != nil {
		return CatalogResponse{}, err
	}
	plain, err := decryptBatch(circle.GroupKey, env.Ciphertext)
	if err != nil {
		return CatalogResponse{}, err
	}
	var resp CatalogResponse
	if err := json.Unmarshal(plain, &resp); err != nil {
		return CatalogResponse{}, err
	}
	member, ok := circle.activeMember(resp.DeviceID)
	if !ok {
		return CatalogResponse{}, errUnknownCatalogPeer
	}
	pub, err := decodePublicKey(member.PublicKey)
	if err != nil || !verifyCatalogResponse(pub, resp) {
		return CatalogResponse{}, errUnknownCatalogPeer
	}
	return resp, nil
}

func peerDashboardContexts(home, deviceFilter string) contextNetwork {
	out := contextNetwork{Computers: []contextComputer{}}
	id, priv, err := loadOrCreateIdentity(home)
	if err != nil {
		return out
	}
	circles, err := listCircles(home)
	if err != nil || len(circles) == 0 {
		return out
	}
	circle := circles[0]
	e, err := newEngine(home)
	if err != nil {
		return out
	}
	defer e.Close()
	items, err := e.listCatalogItems(id.DeviceID)
	if err != nil {
		items = nil
	}
	foldersByDevice := map[string][]string{}
	treesByDevice := map[string][]PageIndexNode{}
	if tree, _ := e.catalogGraphExport(id.DeviceID); len(tree) > 0 {
		treesByDevice[id.DeviceID] = tree
	}
	for _, member := range circle.Members {
		foldersByDevice[member.DeviceID] = append([]string{}, circle.AllowedFolders...)
	}
	selfListen := advertisedListenAddr()
	for _, endpoint := range nonEmptyEndpoints(circle.PeerEndpoints) {
		if endpoint == selfListen {
			continue
		}
		resp, err := fetchPeerCatalog(home, endpoint, circle, id, priv)
		if err != nil {
			continue
		}
		e.importCatalogGraph(resp)
		if len(resp.Folders) > 0 {
			foldersByDevice[resp.DeviceID] = append([]string{}, resp.Folders...)
		}
		if resp.DeviceID != "" && len(resp.Tree) > 0 {
			treesByDevice[resp.DeviceID] = resp.Tree
		}
		for _, item := range resp.Items {
			if item.DeviceID == "" {
				item.DeviceID = resp.DeviceID
			}
			items = append(items, item)
		}
		if len(resp.Members) > 0 {
			_ = mergePeerRoster(home, circle.ID, resp.Members)
		}
	}
	seen := map[string]bool{}
	unique := []CatalogItem{}
	for _, item := range items {
		if item.ID == "" || seen[item.ID] {
			continue
		}
		seen[item.ID] = true
		unique = append(unique, item)
	}
	out.Computers = buildContextComputers(circle, id.DeviceID, unique, foldersByDevice, treesByDevice)
	if deviceFilter != "" {
		filtered := []contextComputer{}
		for _, computer := range out.Computers {
			if computer.DeviceID == deviceFilter {
				filtered = append(filtered, computer)
			}
		}
		out.Computers = filtered
	}
	return out
}

func buildContextComputers(circle Circle, selfID string, items []CatalogItem, foldersByDevice map[string][]string, treesByDevice map[string][]PageIndexNode) []contextComputer {
	byDevice := map[string][]CatalogItem{}
	for _, item := range items {
		byDevice[item.DeviceID] = append(byDevice[item.DeviceID], item)
	}
	out := []contextComputer{}
	for _, member := range circle.Members {
		if member.Revoked {
			continue
		}
		name := peerDisplayName(member, selfID)
		folders := foldersByDevice[member.DeviceID]
		tree := []contextFolder{}
		if nodes := treesByDevice[member.DeviceID]; len(nodes) > 0 {
			tree = projectPageIndexTree(nodes, folders)
		} else {
			tree = buildFolderTree(byDevice[member.DeviceID], folders)
		}
		out = append(out, contextComputer{
			DeviceID: member.DeviceID,
			Name:     name,
			Self:     member.DeviceID == selfID,
			Folders:  folders,
			Tree:     tree,
		})
	}
	return out
}

func buildFolderTree(items []CatalogItem, folders []string) []contextFolder {
	grouped := map[string][]CatalogItem{}
	for _, item := range items {
		path := strings.TrimSpace(item.SourceFolder)
		if path == "" {
			path = "(no folder)"
		}
		grouped[path] = append(grouped[path], item)
	}
	paths := []string{}
	seen := map[string]bool{}
	for _, path := range folders {
		if path == "" || seen[path] {
			continue
		}
		seen[path] = true
		paths = append(paths, path)
	}
	for path := range grouped {
		if !seen[path] {
			paths = append(paths, path)
			seen[path] = true
		}
	}
	sort.Strings(paths)
	out := []contextFolder{}
	for _, path := range paths {
		out = append(out, contextFolder{
			Path:  path,
			Name:  folderBaseName(path),
			Kinds: buildKindGroups(grouped[path]),
		})
	}
	return out
}

func buildKindGroups(items []CatalogItem) []contextKind {
	byKind := map[string][]contextItem{}
	for _, item := range items {
		subject := strings.TrimSpace(item.Subject)
		if subject == "" {
			subject = item.ID
		}
		byKind[item.Kind] = append(byKind[item.Kind], contextItem{
			ID: item.ID, Subject: subject, Summary: strings.TrimSpace(item.Summary), UpdatedAt: item.UpdatedAt,
		})
	}
	return contextKindsFromMap(byKind)
}

func projectPageIndexTree(nodes []PageIndexNode, folders []string) []contextFolder {
	folderPath := map[string]string{}
	folderName := map[string]string{}
	kindTitle := map[string]string{}
	kindFolder := map[string]string{}
	for _, n := range nodes {
		switch n.Kind {
		case "folder":
			path := strings.TrimSpace(n.Summary)
			if path == "" {
				path = strings.TrimSpace(n.Title)
			}
			if path == "" {
				continue
			}
			folderPath[n.ID] = path
			name := strings.TrimSpace(n.Title)
			if name == "" {
				name = folderBaseName(path)
			}
			folderName[n.ID] = name
		case "kind":
			kindTitle[n.ID] = strings.TrimSpace(n.Title)
			kindFolder[n.ID] = n.ParentID
		}
	}
	grouped := map[string]map[string][]contextItem{}
	pathName := map[string]string{}
	for id, path := range folderPath {
		pathName[path] = folderName[id]
		if grouped[path] == nil {
			grouped[path] = map[string][]contextItem{}
		}
	}
	for _, n := range nodes {
		if n.Kind != "memory" {
			continue
		}
		kindID := n.ParentID
		folderID := kindFolder[kindID]
		path := folderPath[folderID]
		if path == "" {
			continue
		}
		kn := kindTitle[kindID]
		if kn == "" {
			kn = "note"
		}
		subject := strings.TrimSpace(n.Title)
		if subject == "" {
			subject = n.MemoryID
		}
		grouped[path][kn] = append(grouped[path][kn], contextItem{
			ID: n.MemoryID, Subject: subject, Summary: strings.TrimSpace(n.Summary), UpdatedAt: n.UpdatedAt,
		})
	}
	paths := []string{}
	seen := map[string]bool{}
	for _, path := range folders {
		if path == "" || seen[path] {
			continue
		}
		seen[path] = true
		paths = append(paths, path)
		if grouped[path] == nil {
			grouped[path] = map[string][]contextItem{}
		}
		if pathName[path] == "" {
			pathName[path] = folderBaseName(path)
		}
	}
	for path := range grouped {
		if !seen[path] {
			paths = append(paths, path)
			seen[path] = true
		}
	}
	sort.Strings(paths)
	out := []contextFolder{}
	for _, path := range paths {
		name := pathName[path]
		if name == "" {
			name = path
		}
		out = append(out, contextFolder{
			Path:  path,
			Name:  name,
			Kinds: contextKindsFromMap(grouped[path]),
		})
	}
	return out
}

func contextKindsFromMap(byKind map[string][]contextItem) []contextKind {
	kinds := []string{}
	for kind := range byKind {
		kinds = append(kinds, kind)
	}
	sort.Strings(kinds)
	out := []contextKind{}
	for _, kind := range kinds {
		group := byKind[kind]
		sort.Slice(group, func(i, j int) bool { return group[i].Subject < group[j].Subject })
		out = append(out, contextKind{Kind: kind, Items: group})
	}
	return out
}

func folderBaseName(path string) string {
	if path == "(no folder)" {
		return path
	}
	name := filepath.Base(filepath.Clean(path))
	if name == "." || name == "/" || name == "" {
		return path
	}
	return name
}
