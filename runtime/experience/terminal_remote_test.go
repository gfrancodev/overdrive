package main

import (
	"crypto/ed25519"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

type terminalRemoteFixture struct {
	home       string
	viewerHome string
	host       DeviceIdentity
	endpoint   string
	circle     Circle
	viewer     DeviceIdentity
	viewKey    ed25519.PrivateKey
	session    TerminalSession
	listener   *shareListener
}

func newTerminalRemoteFixture(t *testing.T) terminalRemoteFixture {
	t.Helper()
	home := t.TempDir()
	closeTerminalDBs()
	sl, endpoint := startShareListenerForTest(t, home)
	t.Cleanup(sl.Close)
	hostID, hostKey, err := loadOrCreateIdentity(home)
	if err != nil {
		t.Fatal(err)
	}
	circle, err := createCircle(home, "team", hostID, hostKey)
	if err != nil {
		t.Fatal(err)
	}
	viewerHome := t.TempDir()
	viewer, viewKey, err := loadOrCreateIdentity(viewerHome)
	if err != nil {
		t.Fatal(err)
	}
	circle.Members = append(circle.Members, CircleMember{DeviceID: viewer.DeviceID, PublicKey: viewer.PublicKey, AddedAt: terminalNow()})
	if err := saveCircle(home, circle); err != nil {
		t.Fatal(err)
	}
	store, err := newTerminalStore(home)
	if err != nil {
		t.Fatal(err)
	}
	sess := TerminalSession{
		ID: newTerminalSessionID(), Name: "shared", State: TerminalStateRunning, Mode: TerminalModeManaged,
		OwnerDeviceID: hostID.DeviceID, HostDeviceID: hostID.DeviceID, CircleID: circle.ID,
		WorkingDirectory: home, Command: "sh", CreatedAt: terminalNow(), UpdatedAt: terminalNow(),
	}
	if err := store.InsertSession(sess); err != nil {
		t.Fatal(err)
	}
	viewerCircle := circle
	viewerCircle.Members = append([]CircleMember(nil), circle.Members...)
	for i := range viewerCircle.Members {
		if viewerCircle.Members[i].DeviceID == hostID.DeviceID {
			viewerCircle.Members[i].Endpoint = endpoint
		}
	}
	if err := saveCircle(viewerHome, viewerCircle); err != nil {
		t.Fatal(err)
	}
	return terminalRemoteFixture{home: home, viewerHome: viewerHome, host: hostID, endpoint: endpoint, circle: circle, viewer: viewer, viewKey: viewKey, session: sess, listener: sl}
}

func TestTerminalRemoteRouteProxiesSessionFromHost(t *testing.T) {
	f := newTerminalRemoteFixture(t)
	base := "/api/v1/terminal/remote/" + f.host.DeviceID + "/sessions/" + f.session.ID
	rec := httptest.NewRecorder()
	serveTerminalAPI(rec, httptest.NewRequest(http.MethodGet, base, nil), f.viewerHome)
	if rec.Code != http.StatusOK {
		t.Fatalf("meta status=%d body=%s", rec.Code, rec.Body.String())
	}
	var meta struct {
		Session TerminalSession `json:"session"`
		Self    string          `json:"self_device_id"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &meta); err != nil {
		t.Fatal(err)
	}
	if meta.Session.ID != f.session.ID || meta.Self != f.viewer.DeviceID {
		t.Fatalf("meta=%+v", meta)
	}
	rec = httptest.NewRecorder()
	serveTerminalAPI(rec, httptest.NewRequest(http.MethodPost, base+"/attach", nil), f.viewerHome)
	var attach struct {
		Token string `json:"stream_token"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &attach)
	if rec.Code != http.StatusOK || attach.Token == "" {
		t.Fatalf("attach status=%d body=%s", rec.Code, rec.Body.String())
	}
	if err := verifyTerminalStreamToken(f.viewerHome, f.session.ID, attach.Token, TerminalCapObserve); err == nil {
		t.Fatal("a remote stream token must not open a local session stream")
	}
	rec = httptest.NewRecorder()
	serveTerminalAPI(rec, httptest.NewRequest(http.MethodGet, "/api/v1/terminal/remote/dev_unknown/sessions/"+f.session.ID, nil), f.viewerHome)
	if rec.Code != http.StatusBadGateway {
		t.Fatalf("unknown host status=%d", rec.Code)
	}
}

func (f terminalRemoteFixture) request(op string) terminalPeerRequest {
	return terminalPeerRequest{DeviceID: f.viewer.DeviceID, CircleID: f.circle.ID, SessionID: f.session.ID, Op: op}
}

func TestTerminalPeerRequestSignatureAndFreshness(t *testing.T) {
	pub, priv, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	req, err := signTerminalPeerRequest(priv, terminalPeerRequest{DeviceID: "dev_v", SessionID: "s1", Op: "meta"})
	if err != nil {
		t.Fatal(err)
	}
	if !verifyTerminalPeerRequest(pub, req, time.Now()) {
		t.Fatal("fresh signed request must verify")
	}
	tampered := req
	tampered.SessionID = "s2"
	if verifyTerminalPeerRequest(pub, tampered, time.Now()) {
		t.Fatal("tampered request must not verify")
	}
	if verifyTerminalPeerRequest(pub, req, time.Now().Add(10*time.Minute)) {
		t.Fatal("stale request must not verify")
	}
}

func TestAuthorizeTerminalPeerChecksCircleMembershipAndSession(t *testing.T) {
	f := newTerminalRemoteFixture(t)
	mgr, err := getTerminalManager(f.home)
	if err != nil {
		t.Fatal(err)
	}
	signed := func(req terminalPeerRequest, key ed25519.PrivateKey) terminalPeerRequest {
		out, err := signTerminalPeerRequest(key, req)
		if err != nil {
			t.Fatal(err)
		}
		return out
	}
	if _, err := authorizeTerminalPeer(mgr, f.circle, signed(f.request("meta"), f.viewKey)); err != nil {
		t.Fatalf("member must be allowed: %v", err)
	}
	stranger, strangerKey, err := loadOrCreateIdentity(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	foreign := f.request("meta")
	foreign.DeviceID = stranger.DeviceID
	if _, err := authorizeTerminalPeer(mgr, f.circle, signed(foreign, strangerKey)); err == nil {
		t.Fatal("non-member must be denied")
	}
	other := f.circle
	other.ID = "circle_other"
	if _, err := authorizeTerminalPeer(mgr, other, signed(f.request("meta"), f.viewKey)); err == nil {
		t.Fatal("session from another circle must be denied")
	}
	if err := mgr.store.SetPrivateMode(f.session.ID, true, f.session.OwnerDeviceID); err != nil {
		t.Fatal(err)
	}
	if _, err := authorizeTerminalPeer(mgr, f.circle, signed(f.request("meta"), f.viewKey)); err == nil {
		t.Fatal("private session must be denied")
	}
}

func TestTerminalPeerMetaAndControlOverShareListener(t *testing.T) {
	f := newTerminalRemoteFixture(t)
	resp, err := callTerminalPeer(f.endpoint, f.circle, f.viewKey, f.request("meta"))
	if err != nil {
		t.Fatal(err)
	}
	if !resp.OK || resp.Session == nil || resp.Session.ID != f.session.ID {
		t.Fatalf("meta: %+v", resp)
	}
	req := f.request("control")
	req.ControlOp = "request"
	if resp, err = callTerminalPeer(f.endpoint, f.circle, f.viewKey, req); err != nil || !resp.OK {
		t.Fatalf("control request: %+v %v", resp, err)
	}
	req.ControlOp = "release"
	req.LeaseID = "lease_missing"
	if resp, err = callTerminalPeer(f.endpoint, f.circle, f.viewKey, req); err != nil || resp.OK {
		t.Fatalf("release without a lease must fail: %+v %v", resp, err)
	}
}

func TestTerminalPeerStreamRejectsInputWithoutLease(t *testing.T) {
	f := newTerminalRemoteFixture(t)
	link, err := openTerminalPeerStream(f.endpoint, f.circle, f.viewKey, f.request("stream"))
	if err != nil {
		t.Fatal(err)
	}
	defer link.Close()
	input := mustEncodeFrame(terminalFrameInput, 0, []byte(`{"lease_id":"","epoch":0,"data":"ls\n"}`))
	if err := link.Send(input); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		raw, err := link.Recv()
		if err != nil {
			t.Fatal(err)
		}
		frame, err := decodeTerminalFrame(raw)
		if err != nil {
			t.Fatal(err)
		}
		if frame.Type == terminalFrameError {
			if !strings.Contains(strings.ToLower(string(frame.Payload)), "lease") {
				t.Fatalf("unexpected error: %s", frame.Payload)
			}
			return
		}
	}
	t.Fatal("input without lease must be rejected with an error frame")
}

func TestTerminalPeerStreamDeniesNonMember(t *testing.T) {
	f := newTerminalRemoteFixture(t)
	_, strangerKey, err := loadOrCreateIdentity(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := openTerminalPeerStream(f.endpoint, f.circle, strangerKey, f.request("stream")); err == nil {
		t.Fatal("stream with a foreign key must be denied")
	}
}
