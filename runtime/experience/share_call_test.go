package main

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestAcceptCallSignalWireInboundInvite(t *testing.T) {
	home := meshHome(t)
	t.Cleanup(resetCallStateForTests)
	self, priv := testDevice(t)
	peer, _ := testDevice(t)
	writeIdentityFile(t, home, self)
	circle := saveTestCircle(t, home, testCircle(self, peer))

	peerHome := meshHome(t)
	writeIdentityFile(t, peerHome, peer)
	saveTestCircle(t, peerHome, circle)

	profile, err := loadOrCreateLocalUser(home)
	if err != nil {
		t.Fatal(err)
	}
	env := chatEnvelope{
		Version: chatEnvelopeVersion, MessageID: newMessageID(), MeshID: circle.ID,
		ConversationID: "call_wire1", SenderUserID: profile.UserID, SenderDeviceID: self.DeviceID,
		Recipient: peer.DeviceID, Type: "call:invite", Timestamp: nowRFC3339(),
		Payload: json.RawMessage(`{}`),
	}
	env, err = signChatEnvelope(priv, env)
	if err != nil {
		t.Fatal(err)
	}
	wire, err := sealChatEnvelope(circle.GroupKey, env)
	if err != nil {
		t.Fatal(err)
	}
	if err := acceptCallSignalWire(peerHome, wire); err != nil {
		t.Fatal(err)
	}
	snap := peerDashboardSnapshot(peerHome)
	if snap.IncomingCall == nil || snap.IncomingCall.CallID != "call_wire1" {
		t.Fatalf("incoming %+v", snap.IncomingCall)
	}
}

func TestCallHTTPEndpoints(t *testing.T) {
	home := meshHome(t)
	t.Cleanup(resetCallStateForTests)
	self, _ := testDevice(t)
	peer, _ := testDevice(t)
	writeIdentityFile(t, home, self)
	circle := saveTestCircle(t, home, testCircle(self, peer))

	body, _ := json.Marshal(callSignalRequest{
		MeshID: circle.ID, TargetDeviceID: peer.DeviceID, Kind: "invite",
	})
	rec := meshHTTP(t, home, http.MethodPost, "/api/call/signal", "127.0.0.1:9", body)
	if rec.Code != http.StatusOK {
		t.Fatalf("signal %d %s", rec.Code, rec.Body.String())
	}
	rec = meshHTTP(t, home, http.MethodGet, "/api/call/events?mesh_id="+circle.ID, "127.0.0.1:9", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("events %d", rec.Code)
	}

	snap := peerDashboardSnapshot(home)
	if snap.IncomingCall != nil {
		t.Fatalf("outbound invite should not surface incoming_call")
	}

	summary, _ := json.Marshal(callSummaryRequest{
		Subject: "Call checkpoint", Content: "Agreed on WebRTC signaling path.",
		ProjectRoot: home,
	})
	rec = meshHTTP(t, home, http.MethodPost, "/api/call/summary", "127.0.0.1:9", summary)
	if rec.Code != http.StatusOK {
		t.Fatalf("summary %d %s", rec.Code, rec.Body.String())
	}
}

func TestIncomingCallSnapshot(t *testing.T) {
	home := meshHome(t)
	t.Cleanup(resetCallStateForTests)
	self, _ := testDevice(t)
	caller, _ := testDevice(t)
	writeIdentityFile(t, home, self)
	circle := saveTestCircle(t, home, testCircle(caller, self))
	updateCallSession(circle.ID, "call_ring1", "invite", caller.DeviceID, false, "", json.RawMessage(`{"mode":"video","conversation_id":"direct:a:b"}`))
	snap := peerDashboardSnapshot(home)
	if snap.IncomingCall == nil || snap.IncomingCall.CallID != "call_ring1" {
		t.Fatalf("incoming %+v", snap.IncomingCall)
	}
	if snap.IncomingCall.Mode != "video" || snap.IncomingCall.ConversationID != "direct:a:b" {
		t.Fatalf("metadata %+v", snap.IncomingCall)
	}
}

func TestNormalizeCallSignalKindsAndLimits(t *testing.T) {
	t.Cleanup(resetCallStateForTests)
	circleID := "circle_mesh01"
	good, err := normalizeCallSignal(callSignalRequest{
		MeshID: circleID, TargetDeviceID: "dev_peer0000000001", Kind: "invite",
		Payload: json.RawMessage(`{"mode":"voice"}`),
	})
	if err != nil || good.CallID == "" {
		t.Fatalf("invite %+v err=%v", good, err)
	}
	_, err = normalizeCallSignal(callSignalRequest{MeshID: circleID, Kind: "offer"})
	if err == nil {
		t.Fatal("expected missing target error")
	}
	big := make([]byte, callHTTPBodyMax+1)
	_, err = normalizeCallSignal(callSignalRequest{
		MeshID: circleID, TargetDeviceID: "dev_peer0000000001", Kind: "ice", CallID: "call_x",
		Payload: json.RawMessage(big),
	})
	if err == nil {
		t.Fatal("expected payload limit error")
	}
	_, err = normalizeCallSignal(callSignalRequest{
		MeshID: circleID, TargetDeviceID: "dev_peer0000000001", Kind: "bogus", CallID: "call_x",
	})
	if err == nil {
		t.Fatal("expected unsupported kind")
	}
}

func TestCallSignalTargetsDedup(t *testing.T) {
	targets := callSignalTargets(callSignalRequest{
		TargetDeviceID:  "dev_a",
		TargetDeviceIDs: []string{"dev_a", "dev_b", "", "dev_b"},
	})
	if len(targets) != 2 || targets[0] != "dev_a" || targets[1] != "dev_b" {
		t.Fatalf("targets %v", targets)
	}
}

func TestParseInviteSessionMeta(t *testing.T) {
	mode, conv := parseInviteSessionMeta("invite", "req_conv", json.RawMessage(`{"mode":"video","conversation_id":"payload_conv"}`))
	if mode != "video" || conv != "payload_conv" {
		t.Fatalf("got %s %s", mode, conv)
	}
	mode, conv = parseInviteSessionMeta("invite", "", json.RawMessage(`{"mode":"bogus"}`))
	if mode != "voice" || conv != "" {
		t.Fatalf("default mode got %s %s", mode, conv)
	}
}

func TestUpdateCallSessionLifecycle(t *testing.T) {
	t.Cleanup(resetCallStateForTests)
	mesh := "circle_mesh01"
	updateCallSession(mesh, "call_lc1", "invite", "dev_caller", false, "", json.RawMessage(`{"mode":"video","conversation_id":"c1"}`))
	updateCallSession(mesh, "call_lc1", "accept", "dev_caller", false, "", nil)
	updateCallSession(mesh, "call_lc1", "hangup", "dev_caller", false, "", nil)
	_, sessions, _ := listCallEventsSince(mesh, 0)
	var ended bool
	callMu.Lock()
	sess := callByID["call_lc1"]
	callMu.Unlock()
	if sess == nil || sess.State != "ended" || sess.Mode != "video" || sess.ConversationID != "c1" {
		t.Fatalf("sess %+v", sess)
	}
	for _, s := range sessions {
		if s.CallID == "call_lc1" {
			ended = true
		}
	}
	if ended {
		t.Fatal("ended session should not be listed as active")
	}
}

func TestCallQoS(t *testing.T) {
	if callQoS("ice") != 0 || callQoS("media-state") != 0 || callQoS("invite") != 1 {
		t.Fatal("qos mismatch")
	}
}

func TestRecordCallSummaryValidation(t *testing.T) {
	home := meshHome(t)
	if err := recordCallSummary(home, callSummaryRequest{Subject: "", Content: "x"}); err == nil {
		t.Fatal("expected subject required")
	}
	big := strings.Repeat("x", chatSummaryMax*4+1)
	if err := recordCallSummary(home, callSummaryRequest{Subject: "s", Content: big, ProjectRoot: home}); err == nil {
		t.Fatal("expected content limit")
	}
}

func TestCallHTTPEndpointsMultiTargetInvite(t *testing.T) {
	home := meshHome(t)
	t.Cleanup(resetCallStateForTests)
	self, _ := testDevice(t)
	peerA, _ := testDevice(t)
	peerB, _ := testDevice(t)
	writeIdentityFile(t, home, self)
	circle := saveTestCircle(t, home, testCircle(self, peerA, peerB))
	body, _ := json.Marshal(callSignalRequest{
		MeshID: circle.ID, Kind: "invite",
		TargetDeviceIDs: []string{peerA.DeviceID, peerB.DeviceID},
		ConversationID:  "conv_group1",
		Payload:         json.RawMessage(`{"mode":"video"}`),
	})
	rec := meshHTTP(t, home, http.MethodPost, "/api/call/signal", "127.0.0.1:9", body)
	if rec.Code != http.StatusOK {
		t.Fatalf("signal %d %s", rec.Code, rec.Body.String())
	}
	var resp map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if resp["call_id"] == "" {
		t.Fatal("missing call_id")
	}
}

func TestNonHostModRejectedAndRosterPersists(t *testing.T) {
	home := meshHome(t)
	t.Cleanup(resetCallStateForTests)
	self, _ := testDevice(t)
	peerA, _ := testDevice(t)
	peerB, _ := testDevice(t)
	writeIdentityFile(t, home, self)
	circle := saveTestCircle(t, home, testCircle(self, peerA, peerB))

	payload, err := json.Marshal(map[string]any{
		"mode":           "voice",
		"host_device_id": peerA.DeviceID,
		"participants":   []string{peerA.DeviceID, self.DeviceID, peerB.DeviceID},
	})
	if err != nil {
		t.Fatal(err)
	}
	invite, err := json.Marshal(callSignalRequest{
		MeshID: circle.ID, CallID: "call_roster", Kind: "invite",
		TargetDeviceID: peerB.DeviceID, Payload: payload,
	})
	if err != nil {
		t.Fatal(err)
	}
	rec := meshHTTP(t, home, http.MethodPost, "/api/call/signal", "127.0.0.1:9", invite)
	if rec.Code != http.StatusOK {
		t.Fatalf("invite %d %s", rec.Code, rec.Body.String())
	}

	modBody, err := json.Marshal(map[string]any{
		"action": "mute", "target_device_id": peerB.DeviceID,
	})
	if err != nil {
		t.Fatal(err)
	}
	mod, err := json.Marshal(callSignalRequest{
		MeshID: circle.ID, CallID: "call_roster", Kind: "mod",
		TargetDeviceID: peerB.DeviceID, Payload: modBody,
	})
	if err != nil {
		t.Fatal(err)
	}
	rec = meshHTTP(t, home, http.MethodPost, "/api/call/signal", "127.0.0.1:9", mod)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("non-host mod %d %s", rec.Code, rec.Body.String())
	}

	hostPayload, err := json.Marshal(map[string]any{
		"mode":           "video",
		"host_device_id": self.DeviceID,
		"participants":   []string{self.DeviceID, peerA.DeviceID},
	})
	if err != nil {
		t.Fatal(err)
	}
	hostInvite, err := json.Marshal(callSignalRequest{
		MeshID: circle.ID, CallID: "call_host", Kind: "invite",
		TargetDeviceID: peerA.DeviceID, Payload: hostPayload,
	})
	if err != nil {
		t.Fatal(err)
	}
	rec = meshHTTP(t, home, http.MethodPost, "/api/call/signal", "127.0.0.1:9", hostInvite)
	if rec.Code != http.StatusOK {
		t.Fatalf("host invite %d %s", rec.Code, rec.Body.String())
	}
	hostMod, err := json.Marshal(callSignalRequest{
		MeshID: circle.ID, CallID: "call_host", Kind: "mod",
		TargetDeviceID: peerA.DeviceID, Payload: modBody,
	})
	if err != nil {
		t.Fatal(err)
	}
	rec = meshHTTP(t, home, http.MethodPost, "/api/call/signal", "127.0.0.1:9", hostMod)
	if rec.Code != http.StatusOK {
		t.Fatalf("host mod %d %s", rec.Code, rec.Body.String())
	}

	rec = meshHTTP(t, home, http.MethodGet, "/api/call/events?mesh_id="+circle.ID, "127.0.0.1:9", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("events %d %s", rec.Code, rec.Body.String())
	}
	var snap struct {
		Sessions []callSessionState `json:"sessions"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &snap); err != nil {
		t.Fatal(err)
	}
	var roster, hosted *callSessionState
	for i := range snap.Sessions {
		switch snap.Sessions[i].CallID {
		case "call_roster":
			roster = &snap.Sessions[i]
		case "call_host":
			hosted = &snap.Sessions[i]
		}
	}
	if roster == nil || roster.HostDeviceID != peerA.DeviceID {
		t.Fatalf("roster session %+v", roster)
	}
	if !sameDeviceSet(roster.Participants, []string{peerA.DeviceID, self.DeviceID, peerB.DeviceID}) {
		t.Fatalf("participants %+v", roster.Participants)
	}
	if hosted == nil || hosted.HostDeviceID != self.DeviceID {
		t.Fatalf("host session %+v", hosted)
	}
}

func TestHostLeavesPromotesLowestDevice(t *testing.T) {
	t.Cleanup(resetCallStateForTests)
	mesh := "circle_mesh01"
	payload, err := json.Marshal(map[string]any{
		"mode":           "voice",
		"host_device_id": "dev_c",
		"participants":   []string{"dev_b", "dev_c", "dev_a"},
	})
	if err != nil {
		t.Fatal(err)
	}
	updateCallSession(mesh, "call_promote", "invite", "dev_c", false, "", payload)
	updateCallSession(mesh, "call_promote", "hangup", "dev_c", false, "", nil)
	callMu.Lock()
	sess := callByID["call_promote"]
	callMu.Unlock()
	if sess == nil || sess.HostDeviceID != "dev_a" || sess.State == "ended" {
		t.Fatalf("promoted session %+v", sess)
	}
	if !sameDeviceSet(sess.Participants, []string{"dev_a", "dev_b"}) {
		t.Fatalf("remaining %+v", sess.Participants)
	}
}

func sameDeviceSet(got, want []string) bool {
	if len(got) != len(want) {
		return false
	}
	seen := map[string]int{}
	for _, id := range got {
		seen[id]++
	}
	for _, id := range want {
		seen[id]--
		if seen[id] < 0 {
			return false
		}
	}
	return true
}

func TestAllowHostClaim(t *testing.T) {
	t.Cleanup(resetCallStateForTests)
	if allowHostClaim("missing", "dev_a") {
		t.Fatal("missing session")
	}
	callMu.Lock()
	callByID["call_claim"] = &callSessionState{
		CallID: "call_claim", HostDeviceID: "dev_host", Participants: []string{"dev_host", "dev_b"},
	}
	callMu.Unlock()
	if !allowHostClaim("call_claim", "dev_host") {
		t.Fatal("current host may signal")
	}
	if allowHostClaim("call_claim", "dev_b") {
		t.Fatal("cannot claim while host still listed")
	}
	callMu.Lock()
	callByID["call_claim"].Participants = []string{"dev_b", "dev_a"}
	callMu.Unlock()
	if !allowHostClaim("call_claim", "dev_a") {
		t.Fatal("host left roster; lowest peer may claim")
	}
}

func TestApplyCallSessionHostActivatesRinging(t *testing.T) {
	t.Cleanup(resetCallStateForTests)
	payload, err := json.Marshal(map[string]any{"host_device_id": "dev_new"})
	if err != nil {
		t.Fatal(err)
	}
	updateCallSession("mesh1", "call_host_apply", "invite", "dev_peer", false, "", nil)
	updateCallSession("mesh1", "call_host_apply", "host", "dev_new", false, "", payload)
	callMu.Lock()
	sess := callByID["call_host_apply"]
	callMu.Unlock()
	if sess == nil || sess.HostDeviceID != "dev_new" || sess.State != "active" {
		t.Fatalf("session %+v", sess)
	}
}

func TestAuthorizeCallModeratorHostClaim(t *testing.T) {
	t.Cleanup(resetCallStateForTests)
	callMu.Lock()
	callByID["call_auth"] = &callSessionState{
		CallID: "call_auth", HostDeviceID: "dev_host", Participants: []string{"dev_a", "dev_claim"},
	}
	callMu.Unlock()
	hostPayload, err := json.Marshal(map[string]any{"host_device_id": "dev_claim"})
	if err != nil {
		t.Fatal(err)
	}
	req := callSignalRequest{CallID: "call_auth", Kind: "host", Payload: hostPayload}
	if err := authorizeCallModerator(req, "dev_claim"); err != nil {
		t.Fatalf("claim allowed: %v", err)
	}
	if err := authorizeCallModerator(req, "dev_intruder"); err == nil {
		t.Fatal("foreign device cannot claim host")
	}
}

func TestCallHTTPEndpointsMethodGuards(t *testing.T) {
	home := meshHome(t)
	t.Cleanup(resetCallStateForTests)
	rec := meshHTTP(t, home, http.MethodGet, "/api/call/signal", "127.0.0.1:9", nil)
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("signal GET %d", rec.Code)
	}
	rec = meshHTTP(t, home, http.MethodPost, "/api/call/events?mesh_id=circle_mesh01", "127.0.0.1:9", nil)
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("events POST %d", rec.Code)
	}
	rec = meshHTTP(t, home, http.MethodGet, "/api/call/summary", "127.0.0.1:9", nil)
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("summary GET %d", rec.Code)
	}
	rec = meshHTTP(t, home, http.MethodGet, "/api/call/events?mesh_id=bad%2Fid", "127.0.0.1:9", nil)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("bad mesh_id %d %s", rec.Code, rec.Body.String())
	}
}

func TestEnrichCallInvitePayload(t *testing.T) {
	out := enrichCallInvitePayload(callSignalRequest{
		Kind: "invite", ConversationID: "conv_1",
	}, json.RawMessage(`{"mode":"voice"}`))
	var body map[string]any
	if err := json.Unmarshal(out, &body); err != nil {
		t.Fatal(err)
	}
	if body["conversation_id"] != "conv_1" || body["mode"] != "voice" {
		t.Fatalf("body %+v", body)
	}
	unchanged := enrichCallInvitePayload(callSignalRequest{Kind: "ice"}, nil)
	if len(unchanged) != 0 {
		t.Fatalf("expected empty payload, got %s", string(unchanged))
	}
}

func TestCallSummaryHTTPRejectsBadJSON(t *testing.T) {
	home := meshHome(t)
	rec := meshHTTP(t, home, http.MethodPost, "/api/call/summary", "127.0.0.1:9", []byte("not-json"))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("summary bad json %d", rec.Code)
	}
}

func TestContainsDevice(t *testing.T) {
	if !containsDevice([]string{"a", "b"}, "b") || containsDevice(nil, "a") {
		t.Fatal("containsDevice mismatch")
	}
}

func TestAttachIncomingCallSkipsStaleRinging(t *testing.T) {
	home := meshHome(t)
	t.Cleanup(resetCallStateForTests)
	self, _ := testDevice(t)
	writeIdentityFile(t, home, self)
	callMu.Lock()
	callByID["call_stale"] = &callSessionState{CallID: "call_stale", MeshID: "circle_mesh01", State: "ringing",
		Direction: "inbound", PeerDeviceID: "dev_old", UpdatedAt: time.Now().Add(-10 * time.Minute).UTC().Format(time.RFC3339)}
	callMu.Unlock()
	dash := &peerDashboard{DeviceID: self.DeviceID}
	attachIncomingCall(home, dash)
	if dash.IncomingCall != nil {
		t.Fatalf("stale ringing session surfaced as incoming call %+v", dash.IncomingCall)
	}
	callMu.Lock()
	callByID["call_old"] = &callSessionState{CallID: "call_old", MeshID: "circle_mesh01", State: "ringing",
		Direction: "inbound", PeerDeviceID: "dev_a", UpdatedAt: time.Now().Add(-20 * time.Second).UTC().Format(time.RFC3339)}
	callByID["call_new"] = &callSessionState{CallID: "call_new", MeshID: "circle_mesh01", State: "ringing",
		Direction: "inbound", PeerDeviceID: "dev_b", UpdatedAt: nowRFC3339()}
	callMu.Unlock()
	for i := 0; i < 8; i++ {
		dash = &peerDashboard{DeviceID: self.DeviceID}
		attachIncomingCall(home, dash)
		if dash.IncomingCall == nil || dash.IncomingCall.CallID != "call_new" {
			t.Fatalf("incoming call should be the newest ringing session, got %+v", dash.IncomingCall)
		}
	}
}
