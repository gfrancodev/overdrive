package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestInviteTTLStaysFifteenMinutes(t *testing.T) {
	if inviteTTL() != 15*time.Minute {
		t.Fatal("invite ttl changed")
	}
}

func TestCircleInvitePolicyReportsExistingDeadline(t *testing.T) {
	home := t.TempDir()
	setupTestEnv(t, home)

	rec := inviteHTTP(t, home, http.MethodGet, "/api/circle/invite", "127.0.0.1:9", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d", rec.Code)
	}
	var body struct {
		TTLSeconds int    `json:"ttl_seconds"`
		ExpiresAt  string `json:"expires_at"`
		Invite     any    `json:"invite"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal("policy json")
	}
	if body.TTLSeconds != int(inviteTTL()/time.Second) {
		t.Fatalf("ttl seconds %d", body.TTLSeconds)
	}
	if body.ExpiresAt != "" || body.Invite != nil {
		t.Fatal("policy created an invite")
	}
	if strings.Contains(rec.Body.String(), "group_key") {
		t.Fatal("policy leaked invite material")
	}
	entries, err := hookReadDir(invitesDir(home))
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatal("policy wrote an invite")
	}
}

func TestCircleInviteIssueCopiesConnectionJSON(t *testing.T) {
	home := t.TempDir()
	setupTestEnv(t, home)
	t.Setenv("OVERDRIVE_SHARE_LISTEN", "127.0.0.1:7741")
	id, priv, _ := loadOrCreateIdentity(home)
	if _, err := createCircle(home, "team", id, priv); err != nil {
		t.Fatal(err)
	}

	rec := inviteHTTP(t, home, http.MethodPost, "/api/circle/invite", "127.0.0.1:9", []byte("{}"))
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d", rec.Code)
	}
	issued := decodeIssuedInvite(t, rec.Body.Bytes())
	if issued.TTLSeconds != int(inviteTTL()/time.Second) || issued.ExpiresAt == "" || issued.ExpiresAt != issued.Invite.ExpiresAt {
		t.Fatal("missing deadline")
	}
	exp, err := time.Parse(time.RFC3339, issued.ExpiresAt)
	if err != nil {
		t.Fatal("expires_at")
	}
	window := time.Now().UTC().Add(inviteTTL())
	if exp.Before(window.Add(-time.Minute)) || exp.After(window.Add(time.Minute)) {
		t.Fatal("expiry is not the existing invite deadline")
	}
	if issued.Invite.Code == "" || issued.Invite.CircleID == "" || issued.Invite.Fingerprint == "" || issued.Invite.Secret == "" {
		t.Fatal("connection json incomplete")
	}
	if issued.Invite.GroupKey != "" || strings.Contains(rec.Body.String(), "group_key") {
		t.Fatal("the connection json must not carry the circle key")
	}
	if issued.Invite.Fingerprint != hashHex([]byte(issued.Invite.CircleID + issued.Invite.Code))[:12] {
		t.Fatal("fingerprint")
	}
	var raw struct {
		Invite map[string]any `json:"invite"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &raw); err != nil {
		t.Fatal("issued invite json")
	}
	if raw.Invite["endpoint"] != "127.0.0.1:7741" {
		t.Fatal("endpoint")
	}
	if _, err := loadJoinInvite(home, issued.Invite.Code); err != nil {
		t.Fatal("invite was not stored")
	}
	circles, err := listCircles(home)
	if err != nil || len(circles) != 1 || circles[0].ID != issued.Invite.CircleID {
		t.Fatal("expected one circle")
	}
}

func TestCircleInviteIssueReusesExistingCircle(t *testing.T) {
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
	rec := inviteHTTP(t, home, http.MethodPost, "/api/circle/invite", "127.0.0.1:9", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d", rec.Code)
	}
	issued := decodeIssuedInvite(t, rec.Body.Bytes())
	if issued.Invite.CircleID != circle.ID {
		t.Fatal("created a second circle")
	}
	circles, err := listCircles(home)
	if err != nil || len(circles) != 1 {
		t.Fatal("circle count")
	}
}

func TestCircleJoinRegistersDevice(t *testing.T) {
	homeA := t.TempDir()
	sl, _ := startShareListenerForTest(t, homeA)
	defer sl.Close()
	issued := issueInviteForTest(t, homeA)

	homeB := t.TempDir()
	setupTestEnv(t, homeB)
	raw, err := json.Marshal(issued.Invite)
	if err != nil {
		t.Fatal(err)
	}
	rec := inviteHTTP(t, homeB, http.MethodPost, "/api/circle/join", "127.0.0.1:9", raw)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d", rec.Code)
	}
	var body struct {
		OK       bool   `json:"ok"`
		CircleID string `json:"circle_id"`
		Name     string `json:"name"`
		Error    string `json:"error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal("join json")
	}
	if !body.OK || body.CircleID != issued.Invite.CircleID || body.Name == "" || body.Error != "" {
		t.Fatal("join result")
	}
	circleA, err := loadCircle(homeA, issued.Invite.CircleID)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(rec.Body.String(), circleA.GroupKey) || strings.Contains(rec.Body.String(), "group_key") {
		t.Fatal("join response leaked invite material")
	}
	idB, _, err := loadOrCreateIdentity(homeB)
	if err != nil {
		t.Fatal(err)
	}
	got, err := loadCircle(homeB, issued.Invite.CircleID)
	if err != nil {
		t.Fatal("circle missing")
	}
	if _, ok := got.activeMember(idB.DeviceID); !ok {
		t.Fatal("device was not registered")
	}
}

func TestCircleJoinRejectsExpiredInvite(t *testing.T) {
	homeA := t.TempDir()
	setupTestEnv(t, homeA)
	issued := issueInviteForTest(t, homeA)
	issued.Invite.ExpiresAt = "2000-01-01T00:00:00Z"

	homeB := t.TempDir()
	setupTestEnv(t, homeB)
	rec := postInvite(t, homeB, issued.Invite)
	assertInviteError(t, rec, http.StatusBadRequest, "invite expired")
	if _, err := loadCircle(homeB, issued.Invite.CircleID); err == nil {
		t.Fatal("expired invite created a circle")
	}
}

func TestCircleJoinRejectsBadCode(t *testing.T) {
	homeA := t.TempDir()
	setupTestEnv(t, homeA)
	issued := issueInviteForTest(t, homeA)
	issued.Invite.Fingerprint = "ffffffffffff"

	homeB := t.TempDir()
	setupTestEnv(t, homeB)
	rec := postInvite(t, homeB, issued.Invite)
	assertInviteError(t, rec, http.StatusBadRequest, "invite fingerprint mismatch")
	if _, err := loadCircle(homeB, issued.Invite.CircleID); err == nil {
		t.Fatal("bad code created a circle")
	}

	unsafe := issued.Invite
	unsafe.Code = "../secret"
	rec = postInvite(t, homeB, unsafe)
	assertInviteError(t, rec, http.StatusNotFound, "invite not found")
	if _, err := hookReadFile(filepath.Join(homeB, "secret.json")); err == nil {
		t.Fatal("unsafe code wrote outside invites")
	}
}

func TestCircleJoinRejectsExistingMember(t *testing.T) {
	home := t.TempDir()
	setupTestEnv(t, home)
	issued := issueInviteForTest(t, home)
	rec := postInvite(t, home, issued.Invite)
	assertInviteError(t, rec, http.StatusConflict, "already a member")
	got, err := loadCircle(home, issued.Invite.CircleID)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Members) != 1 {
		t.Fatal("membership changed")
	}
}

func TestCircleInviteRoutesRequireLoopback(t *testing.T) {
	home := t.TempDir()
	setupTestEnv(t, home)
	for _, path := range []string{"/api/circle/invite", "/api/circle/join"} {
		rec := inviteHTTP(t, home, http.MethodPost, path, "10.1.2.3:9", []byte(`{}`))
		if rec.Code != http.StatusForbidden {
			t.Fatalf("%s status %d", path, rec.Code)
		}
	}
	rec := inviteHTTP(t, home, http.MethodGet, "/api/circle/invite", "10.1.2.3:9", nil)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("get status %d", rec.Code)
	}
	if _, err := listCircles(home); err != nil {
		t.Fatal(err)
	}
	circles, _ := listCircles(home)
	if len(circles) != 0 {
		t.Fatal("remote request created a circle")
	}
}

func TestCircleJoinRejectsMalformedJSON(t *testing.T) {
	home := t.TempDir()
	setupTestEnv(t, home)
	rec := inviteHTTP(t, home, http.MethodPost, "/api/circle/join", "127.0.0.1:9", []byte("{"))
	assertInviteError(t, rec, http.StatusBadRequest, "bad request")
}

func inviteHTTP(t *testing.T, home, method, path, remote string, body []byte) *httptest.ResponseRecorder {
	t.Helper()
	var reader *bytes.Reader
	if body != nil {
		reader = bytes.NewReader(body)
	}
	var req *http.Request
	if reader != nil {
		req = httptest.NewRequest(method, path, reader)
		req.Header.Set("Content-Type", "application/json")
	} else {
		req = httptest.NewRequest(method, path, nil)
	}
	req.RemoteAddr = remote
	req.Host = "127.0.0.1:7742"
	rec := httptest.NewRecorder()
	shareDashboardHandler(home).ServeHTTP(rec, req)
	return rec
}

func issueInviteForTest(t *testing.T, home string) issuedInviteBody {
	t.Helper()
	if circles, _ := listCircles(home); len(circles) == 0 {
		id, priv, err := loadOrCreateIdentity(home)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := createCircle(home, "team", id, priv); err != nil {
			t.Fatal(err)
		}
	}
	rec := inviteHTTP(t, home, http.MethodPost, "/api/circle/invite", "127.0.0.1:9", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("issue status %d", rec.Code)
	}
	return decodeIssuedInvite(t, rec.Body.Bytes())
}

type issuedInviteBody struct {
	TTLSeconds int          `json:"ttl_seconds"`
	ExpiresAt  string       `json:"expires_at"`
	Invite     CircleInvite `json:"invite"`
}

func decodeIssuedInvite(t *testing.T, raw []byte) issuedInviteBody {
	t.Helper()
	var body issuedInviteBody
	if err := json.Unmarshal(raw, &body); err != nil {
		t.Fatal("issued invite json")
	}
	return body
}

func postInvite(t *testing.T, home string, invite CircleInvite) *httptest.ResponseRecorder {
	t.Helper()
	raw, err := json.Marshal(invite)
	if err != nil {
		t.Fatal(err)
	}
	return inviteHTTP(t, home, http.MethodPost, "/api/circle/join", "127.0.0.1:9", raw)
}

func assertInviteError(t *testing.T, rec *httptest.ResponseRecorder, status int, message string) {
	t.Helper()
	if rec.Code != status {
		t.Fatalf("status %d", rec.Code)
	}
	var body struct {
		Error string `json:"error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal("error json")
	}
	if body.Error != message {
		t.Fatalf("error %q", body.Error)
	}
}
