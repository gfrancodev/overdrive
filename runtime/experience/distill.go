package main

import (
	"crypto/ed25519"
	"regexp"
	"strings"
)

var (
	errCodePattern  = regexp.MustCompile(`\b[A-Z][A-Z0-9_]{2,}\b`)
	pathPattern     = regexp.MustCompile(`(?:\.{0,2}/)?[\w./-]+\.(?:go|ts|tsx|js|jsx|py|rb|java|kt|rs|sql|yaml|yml|json|md)\b`)
	symbolPattern   = regexp.MustCompile(`\b[A-Z][a-zA-Z0-9]+(?:Repository|Factory|Service|Controller|Handler|Provider|Client)\b`)
	httpCodePattern = regexp.MustCompile(`\b(?:4\d{2}|5\d{2})\b`)
)

func buildPacketContent(m Memory) string {
	sig := m.ProblemSignature
	if sig == "" {
		sig = extractProblemSignature(strings.Join([]string{m.Subject, m.Content, m.Evidence}, " "))
	}
	parts := []string{}
	if m.Subject != "" {
		parts = append(parts, "symptom: "+m.Subject)
	}
	if m.Content != "" {
		parts = append(parts, "fix: "+m.Content)
	}
	if m.Evidence != "" {
		parts = append(parts, "evidence: "+m.Evidence)
	}
	if sig != "" {
		parts = append(parts, "signature: "+sig)
	}
	return strings.Join(parts, " | ")
}

func extractProblemSignature(text string) string {
	return joinSignatureTokens(collectSignatureTokens(text), 6)
}

func collectSignatureTokens(text string) map[string]bool {
	tokens := map[string]bool{}
	for _, re := range signaturePatterns() {
		for _, m := range re.FindAllString(text, 8) {
			m = strings.TrimSpace(m)
			if len(m) < 3 {
				continue
			}
			tokens[m] = true
		}
	}
	return tokens
}

func signaturePatterns() []*regexp.Regexp {
	return []*regexp.Regexp{errCodePattern, pathPattern, symbolPattern, httpCodePattern}
}

func joinSignatureTokens(tokens map[string]bool, limit int) string {
	out := []string{}
	for t := range tokens {
		out = append(out, t)
		if len(out) >= limit {
			break
		}
	}
	return strings.Join(out, ",")
}

func concreteSignatureOverlap(querySig, memorySig string) bool {
	if strings.TrimSpace(querySig) == "" || strings.TrimSpace(memorySig) == "" {
		return false
	}
	q := tokenSet(strings.Split(querySig, ","))
	m := tokenSet(strings.Split(memorySig, ","))
	for k := range q {
		if m[k] {
			return true
		}
	}
	return false
}

func tokenSet(parts []string) map[string]bool {
	out := map[string]bool{}
	for _, p := range parts {
		p = strings.TrimSpace(strings.ToLower(p))
		if p != "" {
			out[p] = true
		}
	}
	return out
}

var shareableKinds = map[string]bool{
	"lesson": true, "anti_pattern": true, "episode": true, "procedure": true, "fact": true,
}

func shareableKind(kind string) bool {
	return shareableKinds[kind]
}

func applyDistillation(m *Memory, project Project) {
	if m.ProblemSignature == "" {
		m.ProblemSignature = extractProblemSignature(strings.Join([]string{m.Subject, m.Content, m.Evidence}, " "))
	}
	if m.PacketContent == "" {
		m.PacketContent = buildPacketContent(*m)
	}
	if m.Layer == 0 {
		m.Layer = 2
	}
	if m.VectorSpace == "" {
		m.VectorSpace = embedderName()
	}
	if m.SourceFolder == "" {
		m.SourceFolder = project.Root
	}
	m.HotIndex = true
}

func (e *Engine) consolidateSession(project Project, circle Circle) error {
	rows, err := e.db.Query(`SELECT id FROM memories WHERE origin = 'local' AND status = 'active' AND hot_index = 1`)
	if err != nil {
		return err
	}
	defer rows.Close()
	ids := []string{}
	for rows.Next() {
		if hookConsolidateScanErr != nil {
			return hookConsolidateScanErr
		}
		var id string
		if err := rows.Scan(&id); err != nil {
			return err
		}
		ids = append(ids, id)
	}
	for _, id := range ids {
		m, err := e.getMemory(id)
		if err != nil {
			continue
		}
		if m.FailureCount > m.SuccessCount && m.Confidence < 0.4 {
			m.HotIndex = false
			if e.tv != nil && e.tv.Available() {
				e.tv.Remove(id)
			}
			_, _ = e.db.Exec(`UPDATE memories SET hot_index = 0 WHERE id = ?`, id)
		}
	}
	if circle.ID != "" && folderAllowed(circle, project.Root) {
		_ = e.exportPeerIndex(circle, project)
	}
	return nil
}

func (e *Engine) exportPeerIndex(circle Circle, project Project) error {
	memories, err := e.listExportableMemories(project, circle)
	if err != nil {
		return err
	}
	for _, m := range memories {
		if !m.HotIndex || m.Layer != 2 {
			continue
		}
		text := m.PacketContent
		if text == "" {
			text = buildPacketContent(m)
		}
		vec := embedText(text)
		if e.tv != nil && e.tv.Available() && m.Origin == "local" {
			e.tv.Add(m.ID, vec)
		}
	}
	if e.tv != nil && e.tv.Available() {
		e.tv.Sync()
	}
	return nil
}

func (e *Engine) listExportableMemories(project Project, circle Circle) ([]Memory, error) {
	project, ok := resolveProjectForExport(project, circle)
	if !ok {
		return nil, nil
	}
	rows, err := e.db.Query(memorySelect+` WHERE status = 'active' AND origin = 'local' AND scope = 'repository' AND scope_id = ?`, project.Repository)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Memory{}
	for rows.Next() {
		m, err := e.scanMemory(rows)
		if err != nil {
			return nil, err
		}
		if !shouldExportMemory(m) {
			continue
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

func (e *Engine) buildSyncResponse(home string, circle Circle, id DeviceIdentity, priv ed25519.PrivateKey, req SyncRequest) (SyncResponse, error) {
	project := Project{Repository: req.Repository}
	memories, err := e.listExportableMemories(project, circle)
	if err != nil {
		return SyncResponse{}, err
	}
	packets := buildSyncPackets(memories, req.Since, circle, project)
	resp := SyncResponse{
		DeviceID:   id.DeviceID,
		CircleID:   circle.ID,
		Repository: req.Repository,
		Cursor:     nowRFC3339(),
		Packets:    packets,
	}
	return signSyncResponse(priv, resp)
}
