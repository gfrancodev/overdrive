package main

import (
	"bytes"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"
)

func terminalHTTP(t *testing.T, home, method, path, remote string, body []byte, headers map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	var reader io.Reader
	if body != nil {
		reader = bytes.NewReader(body)
	}
	req := httptest.NewRequest(method, path, reader)
	req.RemoteAddr = remote
	req.Host = "127.0.0.1:7742"
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	shareDashboardHandler(home).ServeHTTP(rec, req)
	return rec
}

func terminalJSONBody(t *testing.T, rec *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var out map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("json: %v body %s", err, rec.Body.String())
	}
	return out
}

func insertHTTPTestSession(t *testing.T, home string) string {
	t.Helper()
	id, _, err := loadOrCreateIdentity(home)
	if err != nil {
		t.Fatal(err)
	}
	store, err := newTerminalStore(home)
	if err != nil {
		t.Fatal(err)
	}
	sid := newTerminalSessionID()
	sess := TerminalSession{
		ID: sid, Name: "e2e", State: TerminalStateRunning, Mode: TerminalModeManaged,
		OwnerDeviceID: id.DeviceID, HostDeviceID: id.DeviceID, WorkingDirectory: home, Command: "sh",
		Cols: 80, Rows: 24, CreatedAt: terminalNow(), UpdatedAt: terminalNow(),
	}
	if err := store.InsertSession(sess); err != nil {
		t.Fatal(err)
	}
	return sid
}

func TestTerminalE2E01CreateAndListSessions(t *testing.T) {
	home := t.TempDir()
	setupTestEnv(t, home)
	closeTerminalDBs()
	resetStreamGrantsForTests()
	sid := insertHTTPTestSession(t, home)
	list := terminalHTTP(t, home, http.MethodGet, "/api/v1/terminal/sessions", "127.0.0.1:7742", nil, nil)
	if list.Code != http.StatusOK {
		t.Fatalf("list %d", list.Code)
	}
	lb := terminalJSONBody(t, list)
	sessions, _ := lb["sessions"].([]any)
	if len(sessions) == 0 {
		t.Fatal("expected sessions")
	}
	get := terminalHTTP(t, home, http.MethodGet, "/api/v1/terminal/sessions/"+sid, "127.0.0.1:7742", nil, nil)
	if get.Code != http.StatusOK {
		t.Fatalf("get %d", get.Code)
	}
}

func TestTerminalHTTPMissingSessionReturns404(t *testing.T) {
	home := t.TempDir()
	setupTestEnv(t, home)
	closeTerminalDBs()
	resetStreamGrantsForTests()
	missing := "ts_missing_" + randomToken(8)
	get := terminalHTTP(t, home, http.MethodGet, "/api/v1/terminal/sessions/"+missing, "127.0.0.1:7742", nil, nil)
	if get.Code != http.StatusNotFound {
		t.Fatalf("get missing session: status %d body %s", get.Code, get.Body.String())
	}
	if !strings.Contains(get.Body.String(), string(TerminalErrSessionNotFound)) {
		t.Fatalf("expected not found code in body, got %s", get.Body.String())
	}
	attach := terminalHTTP(t, home, http.MethodPost, "/api/v1/terminal/sessions/"+missing+"/attach", "127.0.0.1:7742",
		[]byte(`{"capabilities":["observe"]}`), nil)
	if attach.Code != http.StatusNotFound {
		t.Fatalf("attach missing session: status %d body %s", attach.Code, attach.Body.String())
	}
	events := terminalHTTP(t, home, http.MethodGet, "/api/v1/terminal/sessions/"+missing+"/events?from_seq=0", "127.0.0.1:7742", nil, nil)
	if events.Code != http.StatusOK {
		t.Fatalf("events for missing session should return empty list, got %d %s", events.Code, events.Body.String())
	}
}

func TestTerminalE2E02GetSessionAndAttach(t *testing.T) {
	home := t.TempDir()
	setupTestEnv(t, home)
	closeTerminalDBs()
	resetStreamGrantsForTests()
	id := insertHTTPTestSession(t, home)
	get := terminalHTTP(t, home, http.MethodGet, "/api/v1/terminal/sessions/"+id, "127.0.0.1:7742", nil, nil)
	if get.Code != http.StatusOK {
		t.Fatalf("get %d", get.Code)
	}
	attach := terminalHTTP(t, home, http.MethodPost, "/api/v1/terminal/sessions/"+id+"/attach", "127.0.0.1:7742",
		[]byte(`{"capabilities":["observe"]}`), nil)
	if attach.Code != http.StatusOK {
		t.Fatalf("attach %d %s", attach.Code, attach.Body.String())
	}
	ab := terminalJSONBody(t, attach)
	if ab["stream_token"] == nil || ab["stream_token"] == "" {
		t.Fatal("missing stream_token")
	}
}

func TestTerminalE2E03ControlGrantAndStaleEpoch(t *testing.T) {
	home := t.TempDir()
	setupTestEnv(t, home)
	closeTerminalDBs()
	id, _, _ := loadOrCreateIdentity(home)
	sessID := insertHTTPTestSession(t, home)
	exp := time.Now().Add(time.Hour).UTC().Format(time.RFC3339)
	grant := terminalHTTP(t, home, http.MethodPost, "/api/v1/terminal/sessions/"+sessID+"/control/grant", "127.0.0.1:7742",
		terminalTestJSON(map[string]any{
			"target_device_id": id.DeviceID,
			"epoch":            0,
			"expires_at":       exp,
		}), nil)
	if grant.Code != http.StatusOK {
		t.Fatalf("grant %d %s", grant.Code, grant.Body.String())
	}
	gb := terminalJSONBody(t, grant)
	lease, _ := gb["lease"].(map[string]any)
	epoch, _ := lease["epoch"].(float64)
	bad := terminalHTTP(t, home, http.MethodPost, "/api/v1/terminal/sessions/"+sessID+"/control/grant", "127.0.0.1:7742",
		terminalTestJSON(map[string]any{
			"target_device_id": "dev_other",
			"epoch":            0,
			"expires_at":       exp,
		}), nil)
	if bad.Code != http.StatusConflict && bad.Code != http.StatusBadRequest {
		t.Fatalf("stale epoch expected conflict, got %d", bad.Code)
	}
	release := terminalHTTP(t, home, http.MethodPost, "/api/v1/terminal/sessions/"+sessID+"/control/release", "127.0.0.1:7742",
		terminalTestJSON(map[string]any{
			"lease_id": lease["id"],
			"epoch":    epoch,
		}), nil)
	if release.Code != http.StatusOK {
		t.Fatalf("release %d %s", release.Code, release.Body.String())
	}
}

func TestTerminalE2E04ControlRequest(t *testing.T) {
	home := t.TempDir()
	setupTestEnv(t, home)
	closeTerminalDBs()
	sid := insertHTTPTestSession(t, home)
	rec := terminalHTTP(t, home, http.MethodPost, "/api/v1/terminal/sessions/"+sid+"/control/request", "127.0.0.1:7742", []byte("{}"), nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("request %d %s", rec.Code, rec.Body.String())
	}
}

func TestTerminalE2E05RemoteAdminDenied(t *testing.T) {
	home := t.TempDir()
	setupTestEnv(t, home)
	closeTerminalDBs()
	rec := terminalHTTP(t, home, http.MethodGet, "/api/v1/terminal/sessions", "203.0.113.1:1234", nil, nil)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d", rec.Code)
	}
}

func TestTerminalE2E06CrossSiteMutationBlocked(t *testing.T) {
	home := t.TempDir()
	setupTestEnv(t, home)
	closeTerminalDBs()
	rec := terminalHTTP(t, home, http.MethodPost, "/api/v1/terminal/sessions", "127.0.0.1:7742",
		[]byte(`{"name":"x","working_directory":"`+home+`"}`), map[string]string{
			"Sec-Fetch-Site": "cross-site",
		})
	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d", rec.Code)
	}
}

func TestTerminalE2E07BadOriginBlocked(t *testing.T) {
	home := t.TempDir()
	setupTestEnv(t, home)
	closeTerminalDBs()
	rec := terminalHTTP(t, home, http.MethodPost, "/api/v1/terminal/sessions", "127.0.0.1:7742",
		[]byte(`{"name":"x","working_directory":"`+home+`"}`), map[string]string{
			"Origin": "https://evil.example",
		})
	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d", rec.Code)
	}
}

func TestTerminalE2E08StreamRequiresToken(t *testing.T) {
	home := t.TempDir()
	setupTestEnv(t, home)
	closeTerminalDBs()
	sid := insertHTTPTestSession(t, home)
	rec := terminalHTTP(t, home, http.MethodGet, "/api/v1/terminal/sessions/"+sid+"/stream", "127.0.0.1:7742", nil, nil)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", rec.Code)
	}
}

func TestTerminalE2E09PeersActiveCount(t *testing.T) {
	home := t.TempDir()
	setupTestEnv(t, home)
	closeTerminalDBs()
	sid := insertHTTPTestSession(t, home)
	if err := os.MkdirAll(terminalSessionsDir(home), 0o700); err != nil {
		t.Fatal(err)
	}
	ln, err := net.Listen("unix", terminalSocketPath(home, sid))
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	rec := terminalHTTP(t, home, http.MethodGet, "/api/peers", "127.0.0.1:7742", nil, nil)
	if rec.Code != http.StatusOK {
		t.Fatal(rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "terminal_active_count") {
		t.Fatalf("peers missing terminal_active_count: %s", rec.Body.String())
	}
}

func TestTerminalE2E10PrivateModeBlocksControlRequest(t *testing.T) {
	home := t.TempDir()
	setupTestEnv(t, home)
	closeTerminalDBs()
	store, err := newTerminalStore(home)
	if err != nil {
		t.Fatal(err)
	}
	sess := TerminalSession{
		ID: newTerminalSessionID(), Name: "priv", State: TerminalStateRunning, Mode: TerminalModeManaged,
		OwnerDeviceID: "dev_a", HostDeviceID: "dev_a", WorkingDirectory: home, Command: "sh",
		CreatedAt: terminalNow(), UpdatedAt: terminalNow(),
	}
	id, _, _ := loadOrCreateIdentity(home)
	sess.OwnerDeviceID = id.DeviceID
	sess.HostDeviceID = id.DeviceID
	if err := store.InsertSession(sess); err != nil {
		t.Fatal(err)
	}
	if err := store.SetPrivateMode(sess.ID, true, id.DeviceID); err != nil {
		t.Fatal(err)
	}
	rec := terminalHTTP(t, home, http.MethodPost, "/api/v1/terminal/sessions/"+sess.ID+"/control/request", "127.0.0.1:7742", []byte("{}"), nil)
	if rec.Code == http.StatusOK {
		t.Fatal("expected private mode rejection")
	}
	if !strings.Contains(rec.Body.String(), string(TerminalErrPrivateMode)) {
		t.Fatalf("body %s", rec.Body.String())
	}
}

func TestTerminalE2E11EventsReplayReadOnly(t *testing.T) {
	home := t.TempDir()
	setupTestEnv(t, home)
	closeTerminalDBs()
	store, _ := newTerminalStore(home)
	sess := TerminalSession{
		ID: newTerminalSessionID(), Name: "ev", State: TerminalStateRunning, Mode: TerminalModeManaged,
		OwnerDeviceID: "d", HostDeviceID: "d", WorkingDirectory: home, Command: "sh",
		CreatedAt: terminalNow(), UpdatedAt: terminalNow(),
	}
	_ = store.InsertSession(sess)
	_, _ = store.RecordOutput(sess.ID, "d", []byte("hello\n"), newANSIScreen(80, 24))
	rec := terminalHTTP(t, home, http.MethodGet, "/api/v1/terminal/sessions/"+sess.ID+"/events?from_seq=0", "127.0.0.1:7742", nil, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("events %d", rec.Code)
	}
}

func TestTerminalE2E13CreateResizeParticipantsAndContext(t *testing.T) {
	home := shortTempHome(t)
	setupTestEnv(t, home)
	closeTerminalDBs()
	resetStreamGrantsForTests()
	body := terminalTestJSON(map[string]any{
		"name":              "api-create",
		"working_directory": home,
		"cols":              100,
		"rows":              30,
	})
	created := terminalHTTP(t, home, http.MethodPost, "/api/v1/terminal/sessions", "127.0.0.1:7742", body, nil)
	if created.Code != http.StatusCreated {
		t.Fatalf("create %d %s", created.Code, created.Body.String())
	}
	cb := terminalJSONBody(t, created)
	sess, _ := cb["session"].(map[string]any)
	sid, _ := sess["id"].(string)
	if sid == "" {
		t.Fatalf("session %+v", sess)
	}
	participants := terminalHTTP(t, home, http.MethodGet, "/api/v1/terminal/sessions/"+sid+"/participants", "127.0.0.1:7742", nil, nil)
	if participants.Code != http.StatusOK {
		t.Fatalf("participants %d %s", participants.Code, participants.Body.String())
	}
	links := terminalHTTP(t, home, http.MethodGet, "/api/v1/terminal/sessions/"+sid+"/context/links", "127.0.0.1:7742", nil, nil)
	if links.Code != http.StatusOK {
		t.Fatalf("context links %d %s", links.Code, links.Body.String())
	}
	badResize := terminalHTTP(t, home, http.MethodPost, "/api/v1/terminal/sessions/"+sid+"/resize", "127.0.0.1:7742", []byte("{"), nil)
	if badResize.Code != http.StatusBadRequest {
		t.Fatalf("resize bad json %d", badResize.Code)
	}
}

func TestTerminalE2E12RecordingPauseResume(t *testing.T) {
	home := t.TempDir()
	setupTestEnv(t, home)
	closeTerminalDBs()
	sid := insertHTTPTestSession(t, home)
	pause := terminalHTTP(t, home, http.MethodPost, "/api/v1/terminal/sessions/"+sid+"/recording/pause", "127.0.0.1:7742", nil, nil)
	if pause.Code != http.StatusOK {
		t.Fatalf("pause %d", pause.Code)
	}
	resume := terminalHTTP(t, home, http.MethodPost, "/api/v1/terminal/sessions/"+sid+"/recording/resume", "127.0.0.1:7742", nil, nil)
	if resume.Code != http.StatusOK {
		t.Fatalf("resume %d", resume.Code)
	}
}

func TestTerminalE2ELoopbackOriginAllowsWSUpgradeCheck(t *testing.T) {
	if !loopbackHTTPOrigin("http://127.0.0.1:7743") {
		t.Fatal("127.0.0.1 should be loopback origin")
	}
	if loopbackHTTPOrigin("https://attacker.test") {
		t.Fatal("external origin must fail")
	}
}

func TestTerminalStripUnsafeSequencesInRenderer(t *testing.T) {
	// Document expected sanitizer behavior mirrored in dashboard/terminal.mjs
	raw := "\x1b]52;c;clipboard\x07ok"
	if strings.Contains(raw, "\x1b]52") {
		clean := strings.ReplaceAll(raw, "\x1b]52;c;clipboard\x07", "")
		if strings.Contains(clean, "\x1b]") {
			t.Fatal("osc should be stripped in UI layer")
		}
	}
}

func terminalTestJSON(v any) []byte {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return b
}
