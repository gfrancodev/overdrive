package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

type shareUIConfig struct {
	Preset         string            `json:"preset"`
	Title          string            `json:"title"`
	Logo           string            `json:"logo"`
	Favicon        string            `json:"favicon,omitempty"`
	AppleTouchIcon string            `json:"appleTouchIcon,omitempty"`
	ThemeColor     string            `json:"themeColor,omitempty"`
	Tokens         map[string]string `json:"tokens,omitempty"`
}

const shareDefaultLogoPath = "/logo.png"

const shareWhitelabelFile = "whitelabel.json"
const shareWhitelabelLegacyFile = "ui.json"

var shareUIColor = regexp.MustCompile(`^#[0-9a-fA-F]{3,8}$`)

func defaultShareWhitelabelConfig() shareUIConfig {
	return shareUIConfig{
		Preset:     "orbit",
		Title:      "Overdrive",
		Logo:       shareDefaultLogoPath,
		ThemeColor: "#00b4ff",
		Tokens: map[string]string{
			"ink":  "#04070d",
			"sea":  "#07131c",
			"foam": "#e8f6ff",
			"cyan": "#00b4ff",
			"live": "#5ee7ff",
			"mute": "#7a93a6",
		},
	}
}

func readShareWhitelabelRaw(home string) ([]byte, error) {
	primary := filepath.Join(home, "share", shareWhitelabelFile)
	raw, err := os.ReadFile(primary)
	if err == nil {
		return raw, nil
	}
	if !os.IsNotExist(err) {
		return nil, err
	}
	legacy := filepath.Join(home, "share", shareWhitelabelLegacyFile)
	return os.ReadFile(legacy)
}

var shareUITokenKeys = map[string]bool{
	"ink": true, "sea": true, "foam": true, "cyan": true, "live": true, "mute": true,
}

func loadShareUI(home string) shareUIConfig {
	cfg := defaultShareWhitelabelConfig()
	raw, err := readShareWhitelabelRaw(home)
	if err != nil {
		return cfg
	}
	var incoming shareUIConfig
	if json.Unmarshal(raw, &incoming) != nil {
		return cfg
	}
	cfg = sanitizeShareUI(incoming)
	return materializeShareUIAssets(home, cfg)
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

func shareHexRGB(hex string) (int, int, int, bool) {
	h := strings.TrimPrefix(strings.TrimSpace(hex), "#")
	if len(h) == 3 {
		h = string([]byte{h[0], h[0], h[1], h[1], h[2], h[2]})
	}
	if len(h) != 6 {
		return 0, 0, 0, false
	}
	var rgb [3]int
	for i := 0; i < 3; i++ {
		n, err := fmt.Sscanf(h[i*2:i*2+2], "%02x", &rgb[i])
		if err != nil || n != 1 {
			return 0, 0, 0, false
		}
	}
	return rgb[0], rgb[1], rgb[2], true
}

func sanitizeShareUI(in shareUIConfig) shareUIConfig {
	out := defaultShareWhitelabelConfig()
	if in.Preset == "enterprise" || in.Preset == "orbit" {
		out.Preset = in.Preset
	}
	if title := sanitizeShareUITitle(in.Title); title != "" {
		out.Title = title
	}
	if path := shareUIImageRef(in.Logo); path != "" {
		out.Logo = path
	}
	if path := shareUIImageRef(in.Favicon); path != "" {
		out.Favicon = path
	}
	if path := shareUIImageRef(in.AppleTouchIcon); path != "" {
		out.AppleTouchIcon = path
	}
	if color := strings.TrimSpace(in.ThemeColor); shareUIColor.MatchString(color) {
		out.ThemeColor = color
	}
	out.Tokens = sanitizeShareUITokens(in.Tokens)
	return out
}

func shareThemeStyleBlock(cfg shareUIConfig) []byte {
	if len(cfg.Tokens) == 0 && cfg.ThemeColor == "" {
		return nil
	}
	var b strings.Builder
	b.WriteString("<style id=\"share-ui-theme\">:root{")
	for key, value := range cfg.Tokens {
		if !shareUITokenKeys[key] || !shareUIColor.MatchString(value) {
			continue
		}
		b.WriteString("--")
		b.WriteString(key)
		b.WriteString(":")
		b.WriteString(value)
		b.WriteString(";")
	}
	accent := strings.TrimSpace(cfg.Tokens["cyan"])
	if accent == "" {
		accent = strings.TrimSpace(cfg.ThemeColor)
	}
	if shareUIColor.MatchString(accent) {
		b.WriteString("--accent:")
		b.WriteString(accent)
		b.WriteString(";")
		if r, g, bl, ok := shareHexRGB(accent); ok {
			b.WriteString("--line:")
			b.WriteString(fmt.Sprintf("rgba(%d,%d,%d,0.22)", r, g, bl))
			b.WriteString(";--focus-border:")
			b.WriteString(fmt.Sprintf("rgba(%d,%d,%d,0.9)", r, g, bl))
			b.WriteString(";--focus-shadow:")
			b.WriteString(fmt.Sprintf("rgba(%d,%d,%d,0.3)", r, g, bl))
			b.WriteString(";--accent-soft:")
			b.WriteString(fmt.Sprintf("rgba(%d,%d,%d,0.2)", r, g, bl))
			b.WriteString(";--accent-mid:")
			b.WriteString(fmt.Sprintf("rgba(%d,%d,%d,0.38)", r, g, bl))
			b.WriteString(";--accent-ring:")
			b.WriteString(fmt.Sprintf("rgba(%d,%d,%d,0.55)", r, g, bl))
			b.WriteString(";--accent-glow-18:")
			b.WriteString(fmt.Sprintf("rgba(%d,%d,%d,0.18)", r, g, bl))
			b.WriteString(";--accent-glow-60:")
			b.WriteString(fmt.Sprintf("rgba(%d,%d,%d,0.6)", r, g, bl))
			b.WriteString(";--term-insp-accent:")
			b.WriteString(accent)
			b.WriteString(";")
		}
	}
	live := strings.TrimSpace(cfg.Tokens["live"])
	if r, g, bl, ok := shareHexRGB(live); ok {
		b.WriteString("--live-glow:")
		b.WriteString(fmt.Sprintf("rgba(%d,%d,%d,0.7)", r, g, bl))
		b.WriteString(";")
	}
	b.WriteString("}html{")
	if cfg.ThemeColor != "" {
		b.WriteString("color-scheme:dark;")
	}
	b.WriteString("}</style>")
	return []byte(b.String())
}

func dashboardHTMLWithUI(home string) []byte {
	cfg := loadShareUI(home)
	payload, err := json.Marshal(cfg)
	if err != nil {
		return dashboardHTML
	}
	var insert bytes.Buffer
	insert.WriteString("<head>")
	if block := shareThemeStyleBlock(cfg); len(block) > 0 {
		insert.Write(block)
	}
	insert.WriteString("<script>window.OVERDRIVE_WHITELABEL=")
	insert.Write(payload)
	insert.WriteString(";window.OVERDRIVE_UI=window.OVERDRIVE_WHITELABEL</script>")
	insert.WriteString(callRuntimeScript())
	return bytes.Replace(dashboardHTML, []byte("<head>"), insert.Bytes(), 1)
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
