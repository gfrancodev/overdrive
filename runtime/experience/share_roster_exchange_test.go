package main

import (
	"encoding/json"
	"net"
	"path/filepath"
	"testing"
	"time"
)

func TestRosterExchangeCarriesProofsBetweenCores(t *testing.T) {
	creator, member, invited := newProofDevice(t), newProofDevice(t), newProofDevice(t)
	homeA := filepath.Join(t.TempDir(), "a")
	homeB := filepath.Join(t.TempDir(), "b")
	circle := testCircle(creator.id, member.id)

	setupTestEnv(t, homeA)
	writeIdentityFile(t, homeA, creator.id)
	onA := circle.admit(invited.member(), creator.admit(t, circle.ID, invited))
	saveTestCircle(t, homeA, onA)
	sl, endpoint := startShareListenerForTest(t, homeA)
	defer sl.Close()

	setupTestEnv(t, homeB)
	writeIdentityFile(t, homeB, member.id)
	saveTestCircle(t, homeB, circle)

	resp, err := fetchPeerRoster(endpoint, circle, member.id, member.priv)
	if err != nil {
		t.Fatal(err)
	}
	if err := applyPeerRoster(homeB, circle.ID, resp); err != nil {
		t.Fatal(err)
	}
	got, err := loadCircle(homeB, circle.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !activeIn(got, invited) {
		t.Fatal("a member admitted on one core must reach another through the roster exchange")
	}
}

func TestRosterRequestNeedsAFreshSignatureFromAnActiveMember(t *testing.T) {
	creator, member, outsider := newProofDevice(t), newProofDevice(t), newProofDevice(t)
	circle := testCircle(creator.id, member.id)
	now := time.Now()
	sign := func(d proofDevice, at time.Time) SyncRequest {
		t.Helper()
		req, err := signSyncRequest(d.priv, SyncRequest{DeviceID: d.id.DeviceID, CircleID: circle.ID, Timestamp: at.UTC().Format(time.RFC3339)})
		if err != nil {
			t.Fatal(err)
		}
		return req
	}
	if !rosterRequestAdmitted(circle, sign(member, now), now) {
		t.Fatal("a fresh request from an active member must be served")
	}
	if rosterRequestAdmitted(circle, sign(member, now.Add(-10*time.Minute)), now) {
		t.Fatal("a replayed request must not be served")
	}
	if rosterRequestAdmitted(circle, sign(outsider, now), now) {
		t.Fatal("a device outside the circle must not get the roster")
	}
	forged := sign(member, now)
	forged.DeviceID = creator.id.DeviceID
	if rosterRequestAdmitted(circle, forged, now) {
		t.Fatal("a request must be signed by the device it names")
	}
}

func TestRosterFromTheRecordedCreatorCanNameTheRealCreator(t *testing.T) {
	home, creator, inviter, self, circle := joinerFixture(t)
	circle.CreatorDeviceID = inviter.id.DeviceID
	saveTestCircle(t, home, circle)
	respond := func(from proofDevice, claim string) RosterResponse {
		t.Helper()
		resp, err := signRosterResponse(from.priv, RosterResponse{
			DeviceID: from.id.DeviceID, CircleID: circle.ID, CreatorDeviceID: claim,
		})
		if err != nil {
			t.Fatal(err)
		}
		return resp
	}
	if err := applyPeerRoster(home, circle.ID, respond(self, self.id.DeviceID)); err != nil {
		t.Fatal(err)
	}
	if got, _ := loadCircle(home, circle.ID); got.CreatorDeviceID != inviter.id.DeviceID {
		t.Fatal("only the device recorded as creator may name another creator")
	}
	if err := applyPeerRoster(home, circle.ID, respond(inviter, creator.id.DeviceID)); err != nil {
		t.Fatal(err)
	}
	if got, _ := loadCircle(home, circle.ID); got.CreatorDeviceID != creator.id.DeviceID {
		t.Fatal("a core that joined through another member must learn the real creator from it")
	}
}

func TestOlderCoresStaySilentOnARosterRequest(t *testing.T) {
	creator, member := newProofDevice(t), newProofDevice(t)
	home := meshHome(t)
	writeIdentityFile(t, home, creator.id)
	circle := saveTestCircle(t, home, testCircle(creator.id, member.id))
	req, err := signSyncRequest(member.priv, SyncRequest{DeviceID: member.id.DeviceID, CircleID: circle.ID, Timestamp: nowRFC3339()})
	if err != nil {
		t.Fatal(err)
	}
	payload, _ := json.Marshal(rosterRequest{Roster: req})
	server, client := net.Pipe()
	defer client.Close()
	sl := &shareListener{home: home, id: creator.id}
	go func() {
		defer server.Close()
		sl.handleSyncWire(server, circle, creator.priv, payload)
	}()
	_ = client.SetReadDeadline(time.Now().Add(2 * time.Second))
	if n, _ := client.Read(make([]byte, 1)); n != 0 {
		t.Fatal("the sync handler older cores run must not answer a roster request")
	}
}

func TestSyncRequestWithABadSignatureCannotMoveAMemberEndpoint(t *testing.T) {
	creator, member, attacker := newProofDevice(t), newProofDevice(t), newProofDevice(t)
	home := meshHome(t)
	writeIdentityFile(t, home, creator.id)
	circle := testCircle(creator.id, member.id)
	circle.Members[1].Endpoint = "10.0.0.2:7741"
	circle = saveTestCircle(t, home, circle)
	req, err := signSyncRequest(attacker.priv, SyncRequest{
		DeviceID: member.id.DeviceID, CircleID: circle.ID, Timestamp: nowRFC3339(), ListenAddr: "10.6.6.6:7741",
	})
	if err != nil {
		t.Fatal(err)
	}
	payload, _ := json.Marshal(req)
	server, client := net.Pipe()
	defer client.Close()
	sl := &shareListener{home: home, id: creator.id}
	done := make(chan struct{})
	go func() {
		defer close(done)
		defer server.Close()
		sl.handleSyncWire(server, circle, creator.priv, payload)
	}()
	<-done
	got, err := loadCircle(home, circle.ID)
	if err != nil {
		t.Fatal(err)
	}
	if m, _ := got.memberRecord(member.id.DeviceID); m.Endpoint != "10.0.0.2:7741" {
		t.Fatalf("an unsigned request moved the member endpoint to %s", m.Endpoint)
	}
}
