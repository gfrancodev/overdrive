package main

import (
	"encoding/base64"
	"net/http"
	"strings"
	"testing"
	"time"
)

func fastPairingPolls(t *testing.T) {
	t.Helper()
	prev := pairingPollEvery
	pairingPollEvery = 20 * time.Millisecond
	t.Cleanup(func() { pairingPollEvery = prev })
}

func waitPairingState(t *testing.T, home, id string, want pairingState) pairingView {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	var last pairingView
	for time.Now().Before(deadline) {
		for _, v := range listPairings(home) {
			if v.ID == id {
				last = v
				if v.State == string(want) {
					return v
				}
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("pairing %s never reached %s; last %+v", id, want, last)
	return last
}

func memberSession(t *testing.T, home, id string) pairingView {
	t.Helper()
	for _, v := range listPairings(home) {
		if v.ID == id {
			return v
		}
	}
	t.Fatalf("the member has no session %s", id)
	return pairingView{}
}

func TestPairingAddsANearbyDeviceOnlyAfterBothPeopleConfirm(t *testing.T) {
	fastPairingPolls(t)
	setupTestEnv(t, t.TempDir())
	member := startRosterCore(t, "member")
	joiner := startRosterCore(t, "joiner")
	circle, err := createCircle(member.home, "team", member.id, member.priv)
	if err != nil {
		t.Fatal(err)
	}

	started, err := startPairing(joiner.home, member.endpoint, member.id.DeviceID)
	if err != nil {
		t.Fatal(err)
	}
	seen := memberSession(t, member.home, started.ID)
	if seen.Role != "member" || seen.State != string(pairingComparing) || seen.Code != started.Code || len(seen.Code) != 6 {
		t.Fatalf("both screens must show the same code: joiner %+v member %+v", started, seen)
	}
	if _, err := confirmPairing(member.home, started.ID, circle.ID); err != nil {
		t.Fatal(err)
	}
	time.Sleep(150 * time.Millisecond)
	if _, err := loadCircle(joiner.home, circle.ID); err == nil {
		t.Fatal("the joiner must not join before its own person confirms the code")
	}
	if _, err := confirmPairing(joiner.home, started.ID, ""); err != nil {
		t.Fatal(err)
	}
	waitPairingState(t, joiner.home, started.ID, pairingJoined)

	got := joiner.circle(t, circle.ID)
	if got.GroupKey != circle.GroupKey || got.CreatorDeviceID != member.id.DeviceID {
		t.Fatal("the joiner must hold the circle key and know the creator")
	}
	if _, ok := got.activeMember(joiner.id.DeviceID); !ok || len(got.AllowedFolders) != 0 {
		t.Fatalf("the joiner is a member and shares no folder until it chooses one: %+v", got.AllowedFolders)
	}
	if adm := member.circle(t, circle.ID).Admissions[joiner.id.DeviceID]; adm.InvitedBy != member.id.DeviceID {
		t.Fatal("the member must keep the admission it signed")
	}
}

func TestPairingWithADeviceOtherThanThePickedOneFails(t *testing.T) {
	setupTestEnv(t, t.TempDir())
	member := startRosterCore(t, "member")
	joiner := startRosterCore(t, "joiner")
	if _, err := createCircle(member.home, "team", member.id, member.priv); err != nil {
		t.Fatal(err)
	}
	if _, err := startPairing(joiner.home, member.endpoint, "dev_0011223344556677"); err == nil {
		t.Fatal("an address answered by another device than the one picked must not pair")
	}
}

func TestARejectedPairingAddsNothing(t *testing.T) {
	fastPairingPolls(t)
	setupTestEnv(t, t.TempDir())
	member := startRosterCore(t, "member")
	joiner := startRosterCore(t, "joiner")
	circle, err := createCircle(member.home, "team", member.id, member.priv)
	if err != nil {
		t.Fatal(err)
	}
	started, err := startPairing(joiner.home, member.endpoint, member.id.DeviceID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := cancelPairing(member.home, started.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := confirmPairing(joiner.home, started.ID, ""); err != nil {
		t.Fatal(err)
	}
	waitPairingState(t, joiner.home, started.ID, pairingRejected)
	if _, err := loadCircle(joiner.home, circle.ID); err == nil {
		t.Fatal("a rejected pairing must not add the joiner to the circle")
	}
	if _, ok := member.circle(t, circle.ID).memberRecord(joiner.id.DeviceID); ok {
		t.Fatal("a rejected pairing must not admit anyone")
	}
}

func TestMemberRefusesARevealThatBreaksTheCommitment(t *testing.T) {
	home := t.TempDir()
	setupTestEnv(t, home)
	memberID, memberPriv, _ := loadOrCreateIdentity(home)
	self, err := selfPairingPeer(memberID, memberPriv)
	if err != nil {
		t.Fatal(err)
	}
	joinerDev := newProofDevice(t)
	joiner, err := selfPairingPeer(joinerDev.id, joinerDev.priv)
	if err != nil {
		t.Fatal(err)
	}
	hello, _ := signPairMessage(joinerDev.priv, pairMessage{
		Op: "hello", SessionID: "ps_commit", Joiner: joiner, Timestamp: nowRFC3339(),
		Commit: pairingCommit("ps_commit", joiner, []byte("the-real-nonce")),
	})
	if r := memberHandlePair(home, self, hello); !r.OK {
		t.Fatalf("hello refused: %s", r.Error)
	}
	reveal, _ := signPairMessage(joinerDev.priv, pairMessage{
		Op: "reveal", SessionID: "ps_commit", Joiner: joiner, Timestamp: nowRFC3339(),
		Nonce: base64.StdEncoding.EncodeToString([]byte("a-chosen-nonce")),
	})
	if r := memberHandlePair(home, self, reveal); r.OK {
		t.Fatal("a reveal that does not open the commitment must be refused")
	}
	if v := memberSession(t, home, "ps_commit"); v.State != string(pairingFailed) || v.Code != "" {
		t.Fatalf("the session must fail without a code: %+v", v)
	}
	stale, _ := signPairMessage(joinerDev.priv, pairMessage{
		Op: "hello", SessionID: "ps_stale", Joiner: joiner, Timestamp: time.Now().Add(-time.Hour).UTC().Format(time.RFC3339),
		Commit: "x",
	})
	if verifyPairMessage(stale, time.Now()) {
		t.Fatal("an old pairing message must not be accepted")
	}
}

func TestPairingRoutesAreLocalOnly(t *testing.T) {
	home := t.TempDir()
	setupTestEnv(t, home)
	if rec := inviteHTTP(t, home, http.MethodGet, "/api/pairing/sessions", "127.0.0.1:9", nil); rec.Code != http.StatusOK {
		t.Fatalf("list %d", rec.Code)
	}
	if rec := inviteHTTP(t, home, http.MethodGet, "/api/pairing/sessions", "192.168.1.20:9", nil); rec.Code != http.StatusForbidden {
		t.Fatalf("a remote caller must not see pairings, got %d", rec.Code)
	}
	if rec := inviteHTTP(t, home, http.MethodPost, "/api/pairing/sessions", "127.0.0.1:9", []byte(`{"device_id":"dev_0011223344556677"}`)); rec.Code != http.StatusNotFound {
		t.Fatalf("pairing with a device that is not nearby must say so, got %d", rec.Code)
	}
}

func TestPairingSaysWhyTheOtherDeviceCannotBeReached(t *testing.T) {
	home := t.TempDir()
	setupTestEnv(t, home)
	port := freeTCPPort(t)
	if err := noteDiscoveredPeer(home, discoveredPeer{DeviceID: "dev_0011223344556677", FriendlyName: "studio", Host: "127.0.0.1", Port: port, LocalOnly: true}); err != nil {
		t.Fatal(err)
	}
	rec := inviteHTTP(t, home, http.MethodPost, "/api/pairing/sessions", "127.0.0.1:9", []byte(`{"device_id":"dev_0011223344556677"}`))
	want := "Could not reach studio. studio accepts connections only from itself. On studio, open Connection and allow devices on this network."
	if rec.Code != http.StatusBadGateway || !strings.Contains(rec.Body.String(), want) {
		t.Fatalf("%d %s", rec.Code, rec.Body.String())
	}
	if err := noteDiscoveredPeer(home, discoveredPeer{DeviceID: "dev_0011223344556677", FriendlyName: "studio", Host: "127.0.0.1", Port: port}); err != nil {
		t.Fatal(err)
	}
	rec = inviteHTTP(t, home, http.MethodPost, "/api/pairing/sessions", "127.0.0.1:9", []byte(`{"device_id":"dev_0011223344556677"}`))
	if want = "Could not reach studio. Overdrive on studio isn't accepting connections."; rec.Code != http.StatusBadGateway || !strings.Contains(rec.Body.String(), want) {
		t.Fatalf("%d %s", rec.Code, rec.Body.String())
	}
}
