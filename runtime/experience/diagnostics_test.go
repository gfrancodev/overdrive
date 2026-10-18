package main

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestDiagnosticsReportWhatHelpsAndNoSecret(t *testing.T) {
	home := t.TempDir()
	setupTestEnv(t, home)
	t.Cleanup(resetPeerProbes)
	id, priv, err := loadOrCreateIdentity(home)
	if err != nil {
		t.Fatal(err)
	}
	circle, err := createCircle(home, "Squad", id, priv)
	if err != nil {
		t.Fatal(err)
	}
	project := filepath.Join(home, "projects", "secret-client-api")
	if err := os.MkdirAll(project, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := addAllowedFolder(home, circle.ID, project, id); err != nil {
		t.Fatal(err)
	}
	invite, err := issueCircleInvite(home, circle.ID)
	if err != nil {
		t.Fatal(err)
	}
	previous := "cHJldmlvdXMta2V5LW5ldmVyLXNob3du"
	circle, err = updateCircle(home, circle.ID, func(c Circle) (Circle, error) {
		c.KeyEpoch = 1
		c.PreviousKey = &CirclePreviousKey{Epoch: 0, Key: previous, ExpiresAt: time.Now().Add(time.Hour).UTC().Format(time.RFC3339)}
		c.Members = append(c.Members, CircleMember{DeviceID: "dev_peer", Hostname: "laptop", Endpoint: "127.0.0.1:1"})
		return c, nil
	})
	if err != nil {
		t.Fatal(err)
	}

	mux := http.NewServeMux()
	registerDiagnosticsRoutes(mux, home)
	req := httptest.NewRequest(http.MethodGet, "/api/diagnostics", nil)
	req.RemoteAddr = "127.0.0.1:50000"
	req.Host = "127.0.0.1:7742"
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("%d %s", rr.Code, rr.Body.String())
	}
	body := rr.Body.String()

	secrets := map[string]string{
		"circle key":    circle.GroupKey,
		"previous key":  previous,
		"invite secret": invite.Secret,
		"private key":   base64.StdEncoding.EncodeToString(priv),
		"private seed":  base64.StdEncoding.EncodeToString(priv.Seed()),
		"folder path":   project,
		"folder name":   "secret-client-api",
		"home":          home,
	}
	for what, secret := range secrets {
		if secret != "" && strings.Contains(body, secret) {
			t.Errorf("diagnostics carry the %s", what)
		}
	}

	var report diagnosticsReport
	if err := json.Unmarshal(rr.Body.Bytes(), &report); err != nil {
		t.Fatal(err)
	}
	if report.Version != version || report.Platform != runtime.GOOS+"/"+runtime.GOARCH || report.DeviceID != id.DeviceID {
		t.Fatalf("who: %+v", report)
	}
	if len(report.Circles) != 1 {
		t.Fatalf("circles: %+v", report.Circles)
	}
	c := report.Circles[0]
	if c.ID != circle.ID || c.KeyEpoch != 1 || !c.PreviousKeyOpen || c.SharedFolders != 1 || c.ActiveMembers != 2 || !c.IsCreator {
		t.Fatalf("circle: %+v", c)
	}
	if report.Health.State == "" || len(report.Health.Capabilities) != 4 {
		t.Fatalf("health: %+v", report.Health)
	}
	if len(report.Health.Peers) != 1 || report.Health.Peers[0].Reach != reachRefused {
		t.Fatalf("the peer on a closed port should read refused: %+v", report.Health.Peers)
	}
	if report.Mesh != meshRunning || report.Network.Listen == "" {
		t.Fatalf("mesh/network: %+v", report)
	}

	req = httptest.NewRequest(http.MethodGet, "/api/diagnostics", nil)
	req.RemoteAddr = "192.168.1.20:50000"
	rr = httptest.NewRecorder()
	mux.ServeHTTP(rr, req)
	if rr.Code != http.StatusForbidden {
		t.Fatalf("remote: %d", rr.Code)
	}
}
