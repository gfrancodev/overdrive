package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

type shareUIConfig struct {
	Preset string            `json:"preset"`
	Title  string            `json:"title"`
	Logo   string            `json:"logo"`
	Tokens map[string]string `json:"tokens,omitempty"`
}

const shareDefaultLogoPath = "/logo.png"

var shareUIColor = regexp.MustCompile(`^#[0-9a-fA-F]{3,8}$`)

var shareUITokenKeys = map[string]bool{
	"ink": true, "sea": true, "foam": true, "cyan": true, "live": true, "mute": true,
}

func loadShareUI(home string) shareUIConfig {
	cfg := shareUIConfig{Preset: "orbit", Title: "Overdrive", Logo: shareDefaultLogoPath}
	raw, err := os.ReadFile(filepath.Join(home, "share", "ui.json"))
	if err != nil {
		return cfg
	}
	var incoming shareUIConfig
	if json.Unmarshal(raw, &incoming) != nil {
		return cfg
	}
	return sanitizeShareUI(incoming)
}

func sanitizeShareUITitle(raw string) string {
	title := strings.TrimSpace(raw)
	title = strings.Map(func(r rune) rune {
		if r < 32 || r == '<' || r == '>' {
			return -1
		}
		return r
	}, title)
	if title == "" {
		return ""
	}
	if len(title) > 40 {
		return title[:40]
	}
	return title
}

func sanitizeShareUITokens(in map[string]string) map[string]string {
	if len(in) == 0 {
		return nil
	}
	out := map[string]string{}
	for key, value := range in {
		if !shareUITokenKeys[key] || !shareUIColor.MatchString(strings.TrimSpace(value)) {
			continue
		}
		out[key] = strings.TrimSpace(value)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func sanitizeShareUI(in shareUIConfig) shareUIConfig {
	out := shareUIConfig{Preset: "orbit", Title: "Overdrive", Logo: shareDefaultLogoPath}
	if in.Preset == "enterprise" || in.Preset == "orbit" {
		out.Preset = in.Preset
	}
	if title := sanitizeShareUITitle(in.Title); title != "" {
		out.Title = title
	}
	logo := strings.TrimSpace(in.Logo)
	if logo == shareDefaultLogoPath || strings.HasPrefix(logo, "/assets/") {
		out.Logo = logo
	}
	out.Tokens = sanitizeShareUITokens(in.Tokens)
	return out
}

func dashboardHTMLWithUI(home string) []byte {
	payload, err := json.Marshal(loadShareUI(home))
	if err != nil {
		return dashboardHTML
	}
	insert := []byte("<head><script>window.OVERDRIVE_UI=" + string(payload) + "</script>" + callRuntimeScript())
	return bytes.Replace(dashboardHTML, []byte("<head>"), insert, 1)
}

func collectDashboardActivity(e *Engine, dash *peerDashboard) (events, decisions, handoffs []string) {
	seen := map[string]bool{}
	for _, circle := range dash.Circles {
		ev, dec, ho := collectCircleDashboardActivity(e, circle, seen)
		events = append(events, ev...)
		decisions = append(decisions, dec...)
		handoffs = append(handoffs, ho...)
	}
	return events, decisions, handoffs
}

func collectCircleDashboardActivity(e *Engine, circle peerDashboardCircle, seen map[string]bool) (events, decisions, handoffs []string) {
	for _, folder := range circle.Folders {
		project, err := identifyProject(folder)
		if err != nil || seen[project.Repository] {
			continue
		}
		seen[project.Repository] = true
		events = append(events, e.loadPeerLines(circle.ID, project.Repository)...)
		for _, memory := range e.listDecisionMemories(project) {
			if line := decisionLine(memory); line != "" {
				decisions = append(decisions, clipUILine(line))
			}
		}
		list, err := e.listHandoffs(project)
		if err != nil {
			continue
		}
		for _, handoff := range list {
			handoffs = append(handoffs, clipUILine(handoff.State+": "+handoff.Summary))
		}
	}
	return events, decisions, handoffs
}

func attachDashboardActivity(home string, dash *peerDashboard) {
	if dash == nil {
		return
	}
	e, err := newEngine(home)
	if err != nil {
		return
	}
	defer e.Close()
	events, decisions, handoffs := collectDashboardActivity(e, dash)
	dash.Links = dashboardLinks(events)
	dash.Events = clipUIList(events, 4)
	dash.Decisions = clipUIList(decisions, 4)
	dash.Handoffs = clipUIList(handoffs, 4)
}

// dashboardLink is the machine-readable twin of a peer sync line: the UI colours the
// arc to Name ("ok" or "timeout") and captions the packets with the imported count.
type dashboardLink struct {
	Name    string `json:"name"`
	State   string `json:"state"`
	Packets int    `json:"packets"`
}

func dashboardLinks(lines []string) []dashboardLink {
	out := []dashboardLink{}
	for _, line := range lines {
		name, state, packets, ok := parsePeerSyncLine(line)
		if !ok {
			continue
		}
		out = append(out, dashboardLink{Name: name, State: state, Packets: packets})
	}
	return out
}

func clipUILine(line string) string {
	line = strings.Join(strings.Fields(line), " ")
	if len(line) > 96 {
		return line[:96]
	}
	return line
}

func clipUIList(lines []string, limit int) []string {
	if len(lines) == 0 {
		return nil
	}
	if len(lines) > limit {
		lines = lines[:limit]
	}
	return lines
}
