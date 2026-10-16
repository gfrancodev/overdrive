package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"strconv"
	"strings"
)

func rejectStatusGroupKey(status ShareStatus, groupKey string) error {
	raw, err := json.Marshal(status)
	if err != nil {
		return err
	}
	return rejectGroupKeyLeak(string(raw), groupKey)
}

func rejectGroupKeyLeak(encoded, groupKey string) error {
	key := strings.TrimSpace(groupKey)
	if key == "" {
		return nil
	}
	if strings.Contains(encoded, key) {
		return errors.New("group key leaked")
	}
	return nil
}

const (
	peerLineTimeout  = "sem resposta (timeout)"
	peerLineOKPrefix = "online, sync ok, "
	peerLineOKSuffix = " pacotes"
)

func formatPeerSyncLine(name, state string, imported int) string {
	if strings.TrimSpace(name) == "" {
		name = "peer"
	}
	if state != "ok" {
		return "- " + name + ": " + peerLineTimeout
	}
	return fmt.Sprintf("- %s: %s%d%s", name, peerLineOKPrefix, imported, peerLineOKSuffix)
}

// parsePeerSyncLine inverts formatPeerSyncLine so the dashboard can colour links
// without scraping the human text. name is the endpoint or device id the line was written for.
func parsePeerSyncLine(line string) (name, state string, imported int, ok bool) {
	rest := strings.TrimPrefix(strings.TrimSpace(line), "- ")
	name, detail, found := strings.Cut(rest, ": ")
	if !found || strings.TrimSpace(name) == "" {
		return "", "", 0, false
	}
	switch {
	case detail == peerLineTimeout:
		return name, "timeout", 0, true
	case strings.HasPrefix(detail, peerLineOKPrefix) && strings.HasSuffix(detail, peerLineOKSuffix):
		count := strings.TrimSuffix(strings.TrimPrefix(detail, peerLineOKPrefix), peerLineOKSuffix)
		n, err := strconv.Atoi(count)
		if err != nil || n < 0 {
			return "", "", 0, false
		}
		return name, "ok", n, true
	default:
		return "", "", 0, false
	}
}

func peerLinesKey(circleID, repository string) string {
	return "share_peer_lines:" + circleID + ":" + repository
}

func (e *Engine) savePeerLines(circleID, repository string, lines []string) {
	if e == nil {
		return
	}
	if lines == nil {
		lines = []string{}
	}
	raw, err := json.Marshal(lines)
	if err != nil {
		return
	}
	e.setMeta(peerLinesKey(circleID, repository), string(raw))
}

func (e *Engine) loadPeerLines(circleID, repository string) []string {
	if e == nil {
		return nil
	}
	raw := e.getMeta(peerLinesKey(circleID, repository))
	if raw == "" {
		return nil
	}
	var lines []string
	if err := json.Unmarshal([]byte(raw), &lines); err != nil {
		return nil
	}
	return lines
}

func (e *Engine) listDecisionMemories(project Project) []Memory {
	if e == nil || e.db == nil {
		return nil
	}
	rows, err := e.db.Query(`SELECT subject, content, source_ref, updated_at FROM memories
		WHERE status = 'active' AND kind = 'decision' AND scope_id = ? ORDER BY updated_at DESC LIMIT 12`, project.Repository)
	if err != nil {
		return nil
	}
	defer rows.Close()
	out := []Memory{}
	for rows.Next() {
		var m Memory
		if err := rows.Scan(&m.Subject, &m.Content, &m.SourceRef, &m.UpdatedAt); err != nil {
			return out
		}
		m.Kind = "decision"
		m.Status = "active"
		out = append(out, m)
	}
	return out
}

// decisionLine renders a decision memory as one line without repeating the subject
// when the promoted ledger entry stored the same text in subject and content.
func decisionLine(m Memory) string {
	subject := strings.TrimSpace(m.Subject)
	content := strings.TrimSpace(m.Content)
	switch {
	case content == "" || strings.EqualFold(content, subject):
		return subject
	case subject == "":
		return content
	default:
		return subject + ": " + content
	}
}

func (e *Engine) renderPin(project Project) string {
	var b strings.Builder
	b.WriteString("Decisões vigentes\n")
	for _, d := range e.listDecisionMemories(project) {
		b.WriteString("- " + decisionLine(d) + "\n")
	}
	b.WriteString("\nHandoff\n")
	handoffs, _ := e.listHandoffs(project)
	for _, h := range handoffs {
		owner := h.OwnerDevice
		if owner == "" {
			owner = "local"
		}
		b.WriteString("- " + owner + ", " + h.State + ": " + h.Summary + "\n")
	}
	b.WriteString("\nCírculo\n")
	status, err := e.shareStatus(project)
	if err != nil {
		b.WriteString("share: off\n")
		return b.String()
	}
	if len(status.PeerLines) == 0 {
		b.WriteString(formatShareStatusText(status) + "\n")
		return b.String()
	}
	for _, line := range status.PeerLines {
		b.WriteString(line + "\n")
	}
	return b.String()
}

func (e *Engine) maybePush(project Project, m Memory) {
	if e == nil || !meshAllowsSync() || !shareListenEnabled() || !shouldExportMemory(m) {
		return
	}
	_ = e.syncCirclePeers(project)
}

func hookRemoteAllowed(remote string) bool {
	host, _, err := net.SplitHostPort(remote)
	if err != nil {
		host = remote
	}
	return host == "127.0.0.1" || host == "::1"
}

func localDeviceID(home string) string {
	id, _, err := loadOrCreateIdentity(home)
	if err != nil {
		return ""
	}
	return id.DeviceID
}
