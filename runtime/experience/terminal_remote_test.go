package main

import (
	"crypto/ed25519"
	"encoding/json"
	"net"
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
	if _, err := store.GrantShare(sess.ID, viewer.DeviceID, terminalSharePermissionObserve); err != nil {
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

// addUngrantedMember joins a second device to the host's circle without sharing the session with it.
func (f terminalRemoteFixture) addUngrantedMember(t *testing.T) (terminalPeerRequest, ed25519.PrivateKey) {
	t.Helper()
	member, key, err := loadOrCreateIdentity(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	circle, err := loadCircle(f.home, f.circle.ID)
	if err != nil {
		t.Fatal(err)
	}
	circle.Members = append(circle.Members, CircleMember{DeviceID: member.DeviceID, PublicKey: member.PublicKey, AddedAt: terminalNow()})
	if err := saveCircle(f.home, circle); err != nil {
		t.Fatal(err)
	}
	req := f.request("meta")
	req.DeviceID = member.DeviceID
	return req, key
}

func TestAuthorizeTerminalPeerRequiresSessionShare(t *testing.T) {
	f := newTerminalRemoteFixture(t)
	mgr, err := getTerminalManager(f.home)
	if err != nil {
		t.Fatal(err)
	}
	req, key := f.addUngrantedMember(t)
	circle, err := loadCircle(f.home, f.circle.ID)
	if err != nil {
		t.Fatal(err)
	}
	signed, err := signTerminalPeerRequest(key, req)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := authorizeTerminalPeer(mgr, circle, signed); err == nil || !strings.Contains(err.Error(), string(TerminalErrAccessDenied)) {
		t.Fatalf("circle member without a share must be denied, got %v", err)
	}
	if _, err := mgr.store.GrantShare(f.session.ID, req.DeviceID, terminalSharePermissionObserve); err != nil {
		t.Fatal(err)
	}
	if signed, err = signTerminalPeerRequest(key, req); err != nil {
		t.Fatal(err)
	}
	if _, err := authorizeTerminalPeer(mgr, circle, signed); err != nil {
		t.Fatalf("shared member must be allowed: %v", err)
	}
}

func TestTerminalPeerOpsDenyUngrantedMemberOverShareListener(t *testing.T) {
	f := newTerminalRemoteFixture(t)
	req, key := f.addUngrantedMember(t)
	circle, err := loadCircle(f.home, f.circle.ID)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := callTerminalPeer(f.endpoint, circle, key, req)
	if err != nil {
		t.Fatal(err)
	}
	if resp.OK || resp.Session != nil || resp.Error != string(TerminalErrAccessDenied) {
		t.Fatalf("meta must be denied without leaking the session: %+v", resp)
	}
	control := req
	control.Op, control.ControlOp = "control", "request"
	if resp, err = callTerminalPeer(f.endpoint, circle, key, control); err != nil || resp.OK {
		t.Fatalf("control request must be denied: %+v %v", resp, err)
	}
	pending, err := mustTerminalStore(t, f.home).PendingControlRequesters(f.session.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range pending {
		if id == req.DeviceID {
			t.Fatal("denied control request must not be queued")
		}
	}
	if _, err := openTerminalPeerStream(f.endpoint, circle, key, req); err == nil {
		t.Fatal("stream must be denied without a share")
	}
}

func mustTerminalStore(t *testing.T, home string) *TerminalStore {
	t.Helper()
	store, err := newTerminalStore(home)
	if err != nil {
		t.Fatal(err)
	}
	return store
}

func TestTerminalParticipantRemovalRevokesShare(t *testing.T) {
	f := newTerminalRemoteFixture(t)
	mgr, err := getTerminalManager(f.home)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := mgr.AttachSession(f.session.ID, f.viewer.DeviceID, []TerminalCapability{TerminalCapObserve}); err != nil {
		t.Fatal(err)
	}
	parts, err := mgr.store.ListParticipants(f.session.ID)
	if err != nil || len(parts) != 1 {
		t.Fatalf("participants %+v %v", parts, err)
	}
	rec := httptest.NewRecorder()
	serveTerminalAPI(rec, httptest.NewRequest(http.MethodDelete, "/api/v1/terminal/sessions/"+f.session.ID+"/participants/"+parts[0].ID, nil), f.home)
	if rec.Code != http.StatusOK {
		t.Fatalf("remove participant status=%d body=%s", rec.Code, rec.Body.String())
	}
	if ok, _ := mgr.store.HasShare(f.session.ID, f.viewer.DeviceID); ok {
		t.Fatal("removing a participant must revoke its share")
	}
	if resp, err := callTerminalPeer(f.endpoint, f.circle, f.viewKey, f.request("meta")); err != nil || resp.OK {
		t.Fatalf("a removed participant must not reconnect: %+v %v", resp, err)
	}
}

func TestTerminalOwnerParticipantRemovalKeepsOwnerControl(t *testing.T) {
	f := newTerminalRemoteFixture(t)
	mgr, err := getTerminalManager(f.home)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := mgr.AttachSession(f.session.ID, f.host.DeviceID, []TerminalCapability{TerminalCapObserve}); err != nil {
		t.Fatal(err)
	}
	exp := time.Now().Add(time.Hour).UTC().Format(time.RFC3339)
	if _, err := mgr.store.grantLeaseCAS(f.session.ID, f.host.DeviceID, 0, exp); err != nil {
		t.Fatal(err)
	}
	_, epoch, err := mgr.store.GetActiveLease(f.session.ID)
	if err != nil {
		t.Fatal(err)
	}
	parts, err := mgr.store.ListParticipants(f.session.ID)
	if err != nil || len(parts) != 1 {
		t.Fatalf("participants %+v %v", parts, err)
	}
	if err := mgr.RemoveParticipantAccess(f.session.ID, parts[0].ID); err != nil {
		t.Fatal(err)
	}
	if lease, now, _ := mgr.store.GetActiveLease(f.session.ID); lease.HolderDeviceID != f.host.DeviceID || now != epoch {
		t.Fatalf("owner lease must be untouched: %+v epoch %d -> %d", lease, epoch, now)
	}
}

func TestWatchTerminalPeerAccessClosesRevokedViewer(t *testing.T) {
	prev := terminalPeerRecheckEvery
	terminalPeerRecheckEvery = 20 * time.Millisecond
	t.Cleanup(func() { terminalPeerRecheckEvery = prev })
	f := newTerminalRemoteFixture(t)
	mgr, err := getTerminalManager(f.home)
	if err != nil {
		t.Fatal(err)
	}
	hostSide, viewerSide := net.Pipe()
	defer viewerSide.Close()
	viewer := newTerminalPeerLink(viewerSide, nil, f.circle.GroupKey)
	done := make(chan struct{})
	defer close(done)
	go watchTerminalPeerAccess(f.home, mgr, f.request("stream"), newTerminalPeerLink(hostSide, nil, f.circle.GroupKey), done)

	_ = viewerSide.SetReadDeadline(time.Now().Add(150 * time.Millisecond))
	if _, err := viewer.Recv(); err == nil || !isTimeout(err) {
		t.Fatalf("an admitted viewer must stay connected, got %v", err)
	}
	if err := mgr.store.RevokeShare(f.session.ID, f.viewer.DeviceID); err != nil {
		t.Fatal(err)
	}
	_ = viewerSide.SetReadDeadline(time.Now().Add(3 * time.Second))
	raw, err := viewer.Recv()
	if err != nil {
		t.Fatalf("revoked viewer must be told why: %v", err)
	}
	frame, err := decodeTerminalFrame(raw)
	if err != nil || frame.Type != terminalFrameError || string(frame.Payload) != string(TerminalErrAccessDenied) {
		t.Fatalf("frame %+v %v", frame, err)
	}
	if _, err := viewer.Recv(); err == nil || isTimeout(err) {
		t.Fatalf("revoked viewer link must be closed, got %v", err)
	}
}

func isTimeout(err error) bool {
	ne, ok := err.(net.Error)
	return ok && ne.Timeout()
}
