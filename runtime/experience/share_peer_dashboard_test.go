package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
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
	if memberEndpoint(gotB, idA.DeviceID) != endpointA {
		t.Fatalf("creator endpoint on B = %q", memberEndpoint(gotB, idA.DeviceID))
	}
	if memberEndpoint(gotB, idB.DeviceID) != endpointB {
		t.Fatalf("self endpoint on B = %q", memberEndpoint(gotB, idB.DeviceID))
	}
	if !containsString(gotB.PeerEndpoints, endpointA) {
		t.Fatalf("B peer endpoints: %v", gotB.PeerEndpoints)
	}

	gotA, err := loadCircle(homeA, circle.ID)
	if err != nil {
		t.Fatal(err)
	}
	if memberEndpoint(gotA, idB.DeviceID) != endpointB {
		t.Fatalf("joiner endpoint on A = %q members=%+v", memberEndpoint(gotA, idB.DeviceID), gotA.Members)
	}
	if !containsString(gotA.PeerEndpoints, endpointB) {
		t.Fatalf("A peer endpoints: %v", gotA.PeerEndpoints)
	}
	if gotA.Members[0].Hostname == "" && memberHostname(gotA, idB.DeviceID) == "" {
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

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/peers", nil)
	shareDashboardHandler(home).ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatal(rr.Code)
	}
	body := rr.Body.String()
	if strings.Contains(body, "group_key") || strings.Contains(body, circle.GroupKey) {
		t.Fatalf("secret leaked: %s", body)
	}
	if !strings.Contains(body, `"name":"alpha"`) || !strings.Contains(body, "192.168.1.20:7741") || !strings.Contains(body, "/srv/workstation") {
		t.Fatalf("missing peer fields: %s", body)
	}

	rr = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/", nil)
	shareDashboardHandler(home).ServeHTTP(rr, req)
	html := rr.Body.String()
	if rr.Code != http.StatusOK || !strings.Contains(html, "Overdrive") || !strings.Contains(html, "/logo.png") || !strings.Contains(html, "Hide") || !strings.Contains(html, "Contexts") || !strings.Contains(html, "popover") || !strings.Contains(html, "three@0.170.0") {
		t.Fatalf("html: %d %s", rr.Code, html)
	}

	rr = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/logo.png", nil)
	shareDashboardHandler(home).ServeHTTP(rr, req)
	if rr.Code != http.StatusOK || !strings.HasPrefix(rr.Header().Get("Content-Type"), "image/") || rr.Body.Len() < 100 {
		t.Fatalf("logo: %d %s %d", rr.Code, rr.Header().Get("Content-Type"), rr.Body.Len())
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
	e.Close()

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/contexts", nil)
	shareDashboardHandler(home).ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatal(rr.Code)
	}
	body := rr.Body.String()
	if strings.Contains(body, "group_key") || strings.Contains(body, circle.GroupKey) || strings.Contains(body, "secret body should not leak") {
		t.Fatalf("secret leaked: %s", body)
	}
	if !strings.Contains(body, "Use WAL") || !strings.Contains(body, "Peer timeout") {
		t.Fatalf("missing subjects: %s", body)
	}
	var network contextNetwork
	if err := json.Unmarshal(rr.Body.Bytes(), &network); err != nil {
		t.Fatal(err)
	}
	foundFolder := false
	for _, computer := range network.Computers {
		if !computer.Self {
			continue
		}
		for _, folder := range computer.Tree {
			if folder.Path == "/srv/workstation" {
				foundFolder = true
			}
		}
	}
	if !foundFolder {
		t.Fatalf("expected PageIndex folder path on this station: %s", body)
	}

	rr = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/api/contexts?device=dev_other", nil)
	shareDashboardHandler(home).ServeHTTP(rr, req)
	filtered := rr.Body.String()
	if !strings.Contains(filtered, "Peer timeout") {
		t.Fatalf("expected peer subject: %s", filtered)
	}
	if strings.Contains(filtered, "Use WAL") {
		t.Fatalf("local subject leaked into peer filter: %s", filtered)
	}
}
