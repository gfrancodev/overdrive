package main

import (
	"bytes"
	_ "embed" // registers //go:embed for dashboard static assets
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"
)

//go:embed dashboard/shell.html
var dashboardShell []byte

//go:embed dashboard/dashboard.css
var dashboardCSS []byte

//go:embed dashboard/chat.css
var dashboardChatCSS []byte

//go:embed dashboard/chat.html
var dashboardChatHTML []byte

//go:embed dashboard/dashboard-shared.mjs
var dashboardSharedJS []byte

//go:embed dashboard/chat.mjs
var dashboardChatJS []byte

//go:embed dashboard/call.mjs
var dashboardCallJS []byte

//go:embed dashboard/terminal.mjs
var dashboardTerminalJS []byte

//go:embed dashboard/emojis.json
var dashboardEmojisJSON []byte

//go:embed dashboard/onboarding.mjs
var dashboardOnboardingJS []byte

//go:embed dashboard/machines.mjs
var dashboardMachinesJS []byte

//go:embed dashboard/app.mjs
var dashboardAppJS []byte

//go:embed dashboard/vendor/xterm.min.js
var dashboardXtermJS []byte

//go:embed dashboard/vendor/xterm.css
var dashboardXtermCSS []byte

//go:embed dashboard/vendor/addon-fit.js
var dashboardXtermFitJS []byte

//go:embed dashboard/vendor/highlight.min.js
var dashboardHighlightJS []byte

//go:embed dashboard/vendor/mermaid.min.js
var dashboardMermaidJS []byte

func assembleDashboardHTML() []byte {
	page := append([]byte(nil), dashboardShell...)
	page = bytes.Replace(page, []byte("/*{{xterm.css}}*/"), dashboardXtermCSS, 1)
	page = bytes.Replace(page, []byte("/*{{dashboard.css}}*/"), dashboardCSS, 1)
	page = bytes.Replace(page, []byte("/*{{chat.css}}*/"), dashboardChatCSS, 1)
	page = bytes.Replace(page, []byte("<!--{{chat.html}}-->"), dashboardChatHTML, 1)
	page = bytes.Replace(page, []byte("<!--{{emojis.json}}-->"), dashboardEmojisJSON, 1)
	script := append(append([]byte{}, dashboardSharedJS...), '\n')
	script = append(script, dashboardChatJS...)
	script = append(script, '\n')
	script = append(script, dashboardCallJS...)
	script = append(script, '\n')
	script = append(script, dashboardTerminalJS...)
	script = append(script, '\n')
	script = append(script, dashboardMachinesJS...)
	script = append(script, '\n')
	script = append(script, dashboardOnboardingJS...)
	script = append(script, '\n')
	script = append(script, dashboardAppJS...)
	page = bytes.Replace(page, []byte("/*{{xterm.js}}*/"), dashboardXtermJS, 1)
	page = bytes.Replace(page, []byte("/*{{xterm-fit.js}}*/"), dashboardXtermFitJS, 1)
	page = bytes.Replace(page, []byte("/*{{highlight.js}}*/"), dashboardHighlightJS, 1)
	page = bytes.Replace(page, []byte("/*{{mermaid.js}}*/"), dashboardMermaidJS, 1)
	page = bytes.Replace(page, []byte("/*{{app.mjs}}*/"), script, 1)
	if bytes.Contains(page, []byte("/*{{")) || bytes.Contains(page, []byte("<!--{{")) {
		panic("dashboard source was not assembled")
	}
	return page
}

var dashboardHTML = assembleDashboardHTML()

//go:embed assets/logo.png
var dashboardLogo []byte

const (
	headerContentType      = "Content-Type"
	headerCacheControl     = "Cache-Control"
	cacheNoStore           = "no-store"
	methodNotAllowedMsg    = "method not allowed"
	badRequestMsg          = "bad request"
	mimeApplicationJSON    = "application/json"
)

type peerDashboardCircle struct {
	ID            string      `json:"id"`
	Name          string      `json:"name"`
	PeerEndpoints []string    `json:"peer_endpoints"`
	Members       []SharePeer `json:"members"`
	Folders       []string    `json:"folders"`
}

type peerDashboard struct {
	DeviceID            string                `json:"device_id,omitempty"`
	Hostname            string                `json:"hostname,omitempty"`
	Platform            string                `json:"platform,omitempty"`
	Listen              string                `json:"listen,omitempty"`
	UI                  string                `json:"ui,omitempty"`
	Circles             []peerDashboardCircle `json:"circles"`
	Events              []string              `json:"events,omitempty"`
	Links               []dashboardLink       `json:"links,omitempty"`
	Decisions           []string              `json:"decisions,omitempty"`
	Handoffs            []string              `json:"handoffs,omitempty"`
	IncomingCall        *incomingCallView     `json:"incoming_call,omitempty"`
	TerminalActiveCount int                   `json:"terminal_active_count,omitempty"`
	TerminalInviteID    string                `json:"terminal_invite_id,omitempty"`
	TerminalControlID   string                `json:"terminal_control_id,omitempty"`
	ProjectRoot         string                `json:"project_root,omitempty"`
}

func shareUIAddr() (string, bool) {
	if v := strings.TrimSpace(os.Getenv("OVERDRIVE_SHARE_UI")); v != "" {
		if v == "off" || v == "0" {
			return "", false
		}
		return v, true
	}
	raw := strings.TrimSpace(os.Getenv("OVERDRIVE_SHARE_LISTEN"))
	if raw == "" {
		return "", false
	}
	_, port, err := net.SplitHostPort(normalizeListen(raw))
	if err != nil {
		return "", false
	}
	p, err := strconv.Atoi(port)
	if err != nil || p <= 0 || p >= 65535 {
		return "", false
	}
	return net.JoinHostPort("127.0.0.1", strconv.Itoa(p+1)), true
}

func peerDashboardSnapshot(home string) peerDashboard {
	out := peerDashboard{
		Listen:   advertisedListenAddr(),
		Hostname: localHostname(),
		Platform: runtime.GOOS + "/" + runtime.GOARCH,
		Circles:  []peerDashboardCircle{},
	}
	if ui, ok := shareUIAddr(); ok {
		out.UI = ui
	}
	if id, _, err := loadOrCreateIdentity(home); err == nil {
		out.DeviceID = id.DeviceID
	}
	if root, err := readShareListenProjectRoot(home); err == nil {
		out.ProjectRoot = root
	}
	circles, err := listCircles(home)
	if err != nil {
		return out
	}
	for _, circle := range circles {
		out.Circles = append(out.Circles, peerDashboardCircle{
			ID:            circle.ID,
			Name:          circle.Name,
			PeerEndpoints: circle.PeerEndpoints,
			Members:       sharePeersFromCircle(circle, out.DeviceID),
			Folders:       append([]string{}, circle.AllowedFolders...),
		})
	}
	attachDashboardActivity(home, &out)
	attachIncomingCall(home, &out)
	attachTerminalTraySignals(home, &out)
	return out
}

func serveDashboardPeers(home string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, methodNotAllowedMsg, http.StatusMethodNotAllowed)
			return
		}
		if rejectRemoteCoreAdmin(w, r) {
			return
		}
		w.Header().Set(headerContentType, mimeApplicationJSON)
		w.Header().Set(headerCacheControl, cacheNoStore)
		_ = json.NewEncoder(w).Encode(peerDashboardSnapshot(home))
	}
}

func serveDashboardContexts(home string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, methodNotAllowedMsg, http.StatusMethodNotAllowed)
			return
		}
		w.Header().Set(headerContentType, mimeApplicationJSON)
		w.Header().Set(headerCacheControl, cacheNoStore)
		_ = json.NewEncoder(w).Encode(peerDashboardContexts(home, strings.TrimSpace(r.URL.Query().Get("device"))))
	}
}

func runDashboardHookSession(home, event string, project Project) error {
	e, err := newEngine(home)
	if err != nil {
		return err
	}
	defer e.Close()
	if event == "session-end" {
		return e.endSession(project)
	}
	_, err = e.startSession(project)
	return err
}

func serveDashboardHook(home string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, methodNotAllowedMsg, http.StatusMethodNotAllowed)
			return
		}
		if rejectRemoteCoreAdmin(w, r) {
			return
		}
		var body struct {
			Event string `json:"event"`
			Cwd   string `json:"cwd"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, badRequestMsg, http.StatusBadRequest)
			return
		}
		cwd := body.Cwd
		if cwd == "" {
			cwd = "."
		}
		project, err := identifyProject(cwd)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if err := runDashboardHookSession(home, body.Event, project); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set(headerContentType, mimeApplicationJSON)
		_ = json.NewEncoder(w).Encode(map[string]string{"ok": "1"})
	}
}

func serveDashboardIndex(home string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set(headerContentType, "text/html; charset=utf-8")
		w.Header().Set(headerCacheControl, cacheNoStore)
		_, _ = w.Write(dashboardHTMLWithUI(home))
	}
}

func shareDashboardHandler(home string) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/assets/", serveShareAssets(home))
	mux.HandleFunc("/logo.png", serveDashboardBrandImage(home, func(c shareUIConfig) string { return c.Logo }))
	mux.HandleFunc("/favicon.ico", serveDashboardBrandImage(home, func(c shareUIConfig) string {
		if c.Favicon != "" {
			return c.Favicon
		}
		return c.Logo
	}))
	mux.HandleFunc("/apple-touch-icon.png", serveDashboardBrandImage(home, func(c shareUIConfig) string {
		if c.AppleTouchIcon != "" {
			return c.AppleTouchIcon
		}
		return c.Logo
	}))
	mux.HandleFunc("/api/peers", serveDashboardPeers(home))
	mux.HandleFunc("/api/contexts", serveDashboardContexts(home))
	registerCoreLifecycleRoutes(mux, home)
	registerMeshAPIRoutes(mux, home)
	registerTerminalAPIRoutes(mux, home)
	registerCircleInviteRoutes(mux, home)
	registerCircleAPIRoutes(mux, home)
	registerPairingRoutes(mux, home)
	mux.HandleFunc("/hook", serveDashboardHook(home))
	mux.HandleFunc("/", serveDashboardIndex(home))
	return mux
}

var shareDashboardOnce sync.Once

func startShareDashboard(home string) {
	shareDashboardOnce.Do(func() {
		warmShareWhitelabelAssets(home)
		addr, ok := shareUIAddr()
		if !ok {
			return
		}
		srv := &http.Server{
			Addr:              addr,
			Handler:           shareDashboardHandler(home),
			ReadHeaderTimeout: 5 * time.Second,
		}
		meshMu.Lock()
		coreDashboardServer = srv
		meshMu.Unlock()
		go func() { _ = srv.ListenAndServe() }()
		fmt.Fprintf(os.Stderr, "share ui: http://%s\n", addr)
	})
}
