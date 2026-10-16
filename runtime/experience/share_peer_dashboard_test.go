package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func memberEndpoint(circle Circle, deviceID string) string {
	member, ok := circle.activeMember(deviceID)
	if !ok {
		return ""
	}
	return member.Endpoint
}

func TestChooseEndpointPrefersReachableAddress(t *testing.T) {
	if got := chooseEndpoint("0.0.0.0:7741", "192.168.1.155:43122"); got != "192.168.1.155:7741" {
		t.Fatalf("wildcard: %s", got)
	}
	if got := chooseEndpoint("127.0.0.1:7741", "192.168.1.155:43122"); got != "192.168.1.155:7741" {
		t.Fatalf("loopback advertised: %s", got)
	}
	if got := chooseEndpoint("192.168.1.20:7741", "10.0.0.4:9"); got != "192.168.1.20:7741" {
		t.Fatalf("explicit: %s", got)
	}
}

func TestJoinRecordsEndpointsBothSides(t *testing.T) {
	homeA := t.TempDir()
	sl, endpointA := startShareListenerForTest(t, homeA)
	defer sl.Close()

	idA, privA, err := loadOrCreateIdentity(homeA)
	if err != nil {
		t.Fatal(err)
	}
	circle, err := createCircle(homeA, "team", idA, privA)
	if err != nil {
		t.Fatal(err)
	}
	invite, err := createInvite(homeA, circle.ID, idA)
	if err != nil {
		t.Fatal(err)
	}

	homeB := t.TempDir()
	if err := os.MkdirAll(invitesDir(homeB), 0o700); err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(invite)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(invitesDir(homeB), invite.Code+".json"), raw, 0o600); err != nil {
		t.Fatal(err)
	}

	endpointB := fmt.Sprintf("127.0.0.1:%d", freeTCPPort(t))
	t.Setenv("OVERDRIVE_SHARE_LISTEN", endpointB)
	idB, privB, err := loadOrCreateIdentity(homeB)
	if err != nil {
		t.Fatal(err)
	}
	gotB, err := acceptInvite(homeB, invite.Code, invite.Fingerprint, endpointA, idB, privB)
	if err != nil {
		t.Fatal(err)
	}
	assertJoinedEndpoints(t, homeA, circle.ID, gotB, idA.DeviceID, idB.DeviceID, endpointA, endpointB)
}

func assertJoinedEndpoints(t *testing.T, homeA, circleID string, gotB Circle, idA, idB, endpointA, endpointB string) {
	t.Helper()
	if memberEndpoint(gotB, idA) != endpointA {
		t.Fatalf("creator endpoint on B = %q", memberEndpoint(gotB, idA))
	}
	if memberEndpoint(gotB, idB) != endpointB {
		t.Fatalf("self endpoint on B = %q", memberEndpoint(gotB, idB))
	}
	if !containsString(gotB.PeerEndpoints, endpointA) {
		t.Fatalf("B peer endpoints: %v", gotB.PeerEndpoints)
	}
	gotA, err := loadCircle(homeA, circleID)
	if err != nil {
		t.Fatal(err)
	}
	if memberEndpoint(gotA, idB) != endpointB {
		t.Fatalf("joiner endpoint on A = %q members=%+v", memberEndpoint(gotA, idB), gotA.Members)
	}
	if !containsString(gotA.PeerEndpoints, endpointB) {
		t.Fatalf("A peer endpoints: %v", gotA.PeerEndpoints)
	}
	if gotA.Members[0].Hostname == "" && memberHostname(gotA, idB) == "" {
		t.Fatal("expected hostname on joiner")
	}
}

func memberHostname(circle Circle, deviceID string) string {
	member, ok := circle.activeMember(deviceID)
	if !ok {
		return ""
	}
	return member.Hostname
}

func TestPeerDashboardShowsMembersWithoutSecrets(t *testing.T) {
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
	circle.Members[0].Endpoint = "192.168.1.20:7741"
	circle.Members[0].Hostname = "alpha"
	circle.Members[0].RemoteAddr = "192.168.1.20:50000"
	circle.AllowedFolders = []string{"/srv/workstation"}
	if err := saveCircle(home, circle); err != nil {
		t.Fatal(err)
	}

	assertPeerDashboardBody(t, home, circle.GroupKey)
	assertDashboardShell(t, home)
}

func assertPeerDashboardBody(t *testing.T, home, groupKey string) {
	t.Helper()
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/peers", nil)
	req.RemoteAddr = "127.0.0.1:9"
	req.Host = "127.0.0.1:7742"
	shareDashboardHandler(home).ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatal(rr.Code)
	}
	body := rr.Body.String()
	if strings.Contains(body, "group_key") || strings.Contains(body, groupKey) {
		t.Fatalf("secret leaked: %s", body)
	}
	if !strings.Contains(body, `"name":"alpha"`) || !strings.Contains(body, "192.168.1.20:7741") || !strings.Contains(body, "/srv/workstation") {
		t.Fatalf("missing peer fields: %s", body)
	}
	if !strings.Contains(body, `"platform":"`+runtime.GOOS+"/"+runtime.GOARCH+`"`) {
		t.Fatalf("missing local platform: %s", body)
	}
}

func assertDashboardShell(t *testing.T, home string) {
	t.Helper()
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	shareDashboardHandler(home).ServeHTTP(rr, req)
	html := rr.Body.String()
	if rr.Code != http.StatusOK || !strings.Contains(html, "Overdrive") || !strings.Contains(html, "/logo.png") {
		t.Fatalf("html: %d %s", rr.Code, html)
	}
	if !strings.Contains(html, `aria-label="Close"`) || !strings.Contains(html, "Contexts") || !strings.Contains(html, "popover") || !strings.Contains(html, "three@0.170.0") {
		t.Fatalf("html chrome: %s", html)
	}
	if !strings.Contains(html, `class="machine-picker"`) || !strings.Contains(html, `id="chips"`) || !strings.Contains(html, `id="iconFolder"`) || !strings.Contains(html, `id="iconTray"`) {
		t.Fatalf("html sheet: %s", html)
	}
	if !strings.Contains(html, ".drawer .machine-picker .peer-pick-menu") || !strings.Contains(html, "position: absolute") {
		t.Fatal("drawer machine picker menu should use anchored absolute positioning")
	}
	if !strings.Contains(html, `id="popBody"`) || !strings.Contains(html, `id="popStatus"`) || !strings.Contains(html, `id="popContexts"`) {
		t.Fatalf("html popover: %s", html)
	}
	if !strings.Contains(html, `id="menubar"`) || !strings.Contains(html, `id="iconMacBook"`) || !strings.Contains(html, `id="legendApp"`) || !strings.Contains(html, `id="stack"`) {
		t.Fatalf("html menubar/stack: %s", html)
	}
	if !strings.Contains(html, `id="findApp"`) || !strings.Contains(html, `id="findSearch"`) || !strings.Contains(html, `id="findResults"`) {
		t.Fatalf("html subject search: %s", html)
	}
	if !strings.Contains(html, `class="brand"`) || !strings.Contains(html, `href="https://github.com/gfrancodev/overdrive/releases"`) || !strings.Contains(html, `target="_blank"`) || !strings.Contains(html, `rel="noopener noreferrer"`) {
		t.Fatalf("brand should link to releases: %s", html)
	}
	if !strings.Contains(html, `id="circleApp"`) || !strings.Contains(html, `id="barPulse"`) || !strings.Contains(html, `id="barListen"`) || !strings.Contains(html, `id="barDateTimeBtn"`) {
		t.Fatalf("dock mark or status cluster missing: %s", html)
	}
	if !strings.Contains(html, `id="dock"`) || !strings.Contains(html, `id="iconComputer"`) || !strings.Contains(html, "deviceMesh") {
		t.Fatalf("html dock: %s", html)
	}
	if !strings.Contains(html, `id="stage"`) || !strings.Contains(html, `id="drawer"`) || !strings.Contains(html, `id="find"`) {
		t.Fatalf("globe shell missing: %s", html)
	}
	for _, id := range []string{
		`id="msgApp"`,
		`id="msgCount"`,
		`id="messages"`,
		`id="conversationList"`,
		`id="threadPane"`,
		`id="nearby"`,
		`id="discoveredPeers"`,
		`id="settings"`,
		`id="meshPause"`,
		`id="meshResume"`,
		`id="barPaused"`,
	} {
		if !strings.Contains(html, id) {
			t.Fatalf("mesh chrome missing %s", id)
		}
	}
	if !strings.Contains(html, "row-forget") || !strings.Contains(html, "function forgetNearbyPeer") {
		t.Fatal("nearby forget control missing from embedded dashboard")
	}
	if strings.Contains(html, `id="sidebar"`) || strings.Contains(html, `id="navOverview"`) || strings.Contains(html, `class="sidebar"`) {
		t.Fatal("sidebar should not be the primary shell")
	}
	if !strings.Contains(html, `class="chat-widget"`) || !strings.Contains(html, `id="msgExpand"`) || !strings.Contains(html, `id="msgClose"`) || !strings.Contains(html, `id="msgResize"`) || strings.Contains(html, `stack-panel" id="messages"`) {
		t.Fatal("chat widget missing")
	}
	if !strings.Contains(html, `id="messages"`) || !strings.Contains(html, "messagesWidgetOpen") {
		t.Fatal("messages widget should persist open state in session storage")
	}
	if !strings.Contains(html, `id="messages" hidden`) {
		t.Fatal("messages widget should be hidden by default")
	}
	if !strings.Contains(html, "function restoreWidgetSize()") || !strings.Contains(html, "function bindWidgetResize()") {
		t.Fatal("chat widget chrome helpers missing from embedded dashboard script")
	}
	if !strings.Contains(html, `id="emojiCatalog"`) || !strings.Contains(html, `"tabs":["recent"`) || !strings.Contains(html, "function ensureEmojiPicker()") {
		t.Fatal("emoji picker catalog or bootstrap missing from embedded dashboard")
	}
	if !strings.Contains(html, "function ensureWorld()") {
		t.Fatal("globe renderer bootstrap missing from embedded dashboard script")
	}
	if !strings.Contains(html, `id="callPanel"`) || !strings.Contains(html, "function createMeshCall") {
		t.Fatal("call panel or mesh call bootstrap missing from embedded dashboard")
	}
	if !strings.Contains(html, "globalThis.overdriveShowLiveTerminal = showLiveTerminal") {
		t.Fatal("composer terminal button hook missing from embedded dashboard")
	}
	for _, route := range []string{
		"/api/chat/conversations?mesh_id=",
		"/api/chat/messages",
		"/api/discovery/peers",
		"/api/discovery/forget",
		"/api/mesh/pause",
		"/api/mesh/resume",
		"/api/peers",
		"/api/contexts",
		"/api/circle/invite",
		"/api/circle/join",
		"/api/call/signal",
		"/api/call/events",
	} {
		if !strings.Contains(html, route) {
			t.Fatalf("shell route missing %s", route)
		}
	}
	for _, id := range []string{
		`id="copyInvite"`,
		`id="joinJSON"`,
		`id="joinDevice"`,
		`id="inviteRule"`,
		`id="nearbyConnection"`,
		"An expired connection is refused.",
		"function copyConnection",
	} {
		if !strings.Contains(html, id) {
			t.Fatalf("device connection chrome missing %s", id)
		}
	}
	rr = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/logo.png", nil)
	shareDashboardHandler(home).ServeHTTP(rr, req)
	if rr.Code != http.StatusOK || !strings.HasPrefix(rr.Header().Get(headerContentType), "image/") || rr.Body.Len() < 100 {
		t.Fatalf("logo: %d %s %d", rr.Code, rr.Header().Get(headerContentType), rr.Body.Len())
	}
	rr = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/favicon.ico", nil)
	shareDashboardHandler(home).ServeHTTP(rr, req)
	if rr.Code != http.StatusOK || rr.Body.Len() < 100 {
		t.Fatalf("favicon: %d %d", rr.Code, rr.Body.Len())
	}
}

func TestContextTreeFiltersByDevice(t *testing.T) {
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
	circle.Members = append(circle.Members, CircleMember{
		DeviceID: "dev_other", Hostname: "beta", AddedAt: nowRFC3339(),
	})
	if err := saveCircle(home, circle); err != nil {
		t.Fatal(err)
	}
	e, err := newEngine(home)
	if err != nil {
		t.Fatal(err)
	}
	if err := e.upsertMemory(Memory{
		ID: "local-lesson", Kind: "lesson", Status: "active", Scope: "repository",
		ScopeID: "github.com/acme/r", Subject: "Use WAL", Content: "secret body should not leak",
		SourceFolder: "/srv/workstation", Origin: "local", CreatedAt: nowRFC3339(), UpdatedAt: nowRFC3339(),
	}); err != nil {
		t.Fatal(err)
	}
	if err := e.upsertMemory(Memory{
		ID: "peer-lesson", Kind: "lesson", Status: "active", Scope: "repository",
		ScopeID: "github.com/acme/r", Subject: "Peer timeout", Content: "peer secret",
		SourceFolder: "/srv/workstation", Origin: "peer", PeerDeviceID: "dev_other",
		CreatedAt: nowRFC3339(), UpdatedAt: nowRFC3339(),
	}); err != nil {
		t.Fatal(err)
	}
	if err := e.upsertMemory(Memory{
		ID: "distilled-lesson", Kind: "lesson", Status: "active", Scope: "repository",
		ScopeID: "github.com/acme/r", Subject: "Resume handoff",
		Content:          "raw lesson body stays private",
		Evidence:         "The next agent continues from the open handoff",
		ProblemSignature: "ERR_TIMEOUT",
		SourceFolder:     "/srv/workstation", Origin: "local",
		CreatedAt: nowRFC3339(), UpdatedAt: nowRFC3339(),
	}); err != nil {
		t.Fatal(err)
	}
	e.Close()

	assertContextTree(t, home, circle.GroupKey)
}

func assertContextTree(t *testing.T, home, groupKey string) {
	t.Helper()
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/contexts", nil)
	shareDashboardHandler(home).ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatal(rr.Code)
	}
	body := rr.Body.String()
	if strings.Contains(body, "group_key") || strings.Contains(body, groupKey) || strings.Contains(body, "secret body should not leak") || strings.Contains(body, "peer secret") || strings.Contains(body, "raw lesson body stays private") {
		t.Fatalf("secret leaked: %s", body)
	}
	if !strings.Contains(body, "Use WAL") || !strings.Contains(body, "Peer timeout") {
		t.Fatalf("missing subjects: %s", body)
	}
	assertSelfFolder(t, rr.Body.Bytes(), body)
	assertDistilledSummary(t, rr.Body.Bytes())
	assertDeviceContextFilter(t, home)
}

func assertDistilledSummary(t *testing.T, raw []byte) {
	t.Helper()
	var network contextNetwork
	if err := json.Unmarshal(raw, &network); err != nil {
		t.Fatal(err)
	}
	for _, computer := range network.Computers {
		for _, folder := range computer.Tree {
			for _, kind := range folder.Kinds {
				for _, item := range kind.Items {
					if item.Subject != "Resume handoff" {
						continue
					}
					if strings.Contains(item.Summary, "The next agent continues from the open handoff") && strings.Contains(item.Summary, "ERR_TIMEOUT") {
						return
					}
					t.Fatalf("shareable summary missing distillation: %+v", item)
				}
			}
		}
	}
	t.Fatal("distilled memory missing from context tree")
}

func assertSelfFolder(t *testing.T, raw []byte, body string) {
	t.Helper()
	var network contextNetwork
	if err := json.Unmarshal(raw, &network); err != nil {
		t.Fatal(err)
	}
	foundFolder := false
	for _, computer := range network.Computers {
		if computer.Self && contextTreeHasPath(computer.Tree, "/srv/workstation") {
			foundFolder = true
		}
	}
	if !foundFolder {
		t.Fatalf("expected PageIndex folder path on this station: %s", body)
	}
}

func contextTreeHasPath(folders []contextFolder, path string) bool {
	for _, folder := range folders {
		if folder.Path == path {
			return true
		}
	}
	return false
}

func assertDeviceContextFilter(t *testing.T, home string) {
	t.Helper()
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/contexts?device=dev_other", nil)
	shareDashboardHandler(home).ServeHTTP(rr, req)
	filtered := rr.Body.String()
	if !strings.Contains(filtered, "Peer timeout") {
		t.Fatalf("expected peer subject: %s", filtered)
	}
	if strings.Contains(filtered, "Use WAL") {
		t.Fatalf("local subject leaked into peer filter: %s", filtered)
	}
}
