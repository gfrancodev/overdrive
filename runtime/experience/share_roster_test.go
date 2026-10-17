package main

import (
	"encoding/json"
	"testing"
)

// A device id is derived from its public key, so roster gossip and joins can never change which key
// speaks for a device: that key is what every signed peer request is checked against.

type rosterFixture struct {
	home    string
	creator DeviceIdentity
	rogue   DeviceIdentity
	victim  DeviceIdentity
	circle  Circle
}

func newRosterFixture(t *testing.T) rosterFixture {
	t.Helper()
	home := meshHome(t)
	creator, _ := testDevice(t)
	self, _ := testDevice(t)
	rogue, _ := testDevice(t)
	victim, _ := testDevice(t)
	writeIdentityFile(t, home, self)
	circle := saveTestCircle(t, home, testCircle(creator, self, rogue, victim))
	return rosterFixture{home: home, creator: creator, rogue: rogue, victim: victim, circle: circle}
}

func (f rosterFixture) reload(t *testing.T) Circle {
	t.Helper()
	c, err := loadCircle(f.home, f.circle.ID)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestRosterGossipCannotRewriteAMemberKey(t *testing.T) {
	f := newRosterFixture(t)
	if err := mergePeerRoster(f.home, f.circle.ID, []CircleMember{
		{DeviceID: f.victim.DeviceID, PublicKey: f.rogue.PublicKey, Hostname: "spoofed"},
	}); err != nil {
		t.Fatal(err)
	}
	m, ok := f.reload(t).activeMember(f.victim.DeviceID)
	if !ok || m.PublicKey != f.victim.PublicKey {
		t.Fatalf("a peer must not swap another member's key, got %q", m.PublicKey)
	}
	if m.Hostname == "spoofed" {
		t.Fatal("a record whose key does not match its device id must be dropped whole")
	}
}

func TestRosterGossipOnlyRefreshesHintsOfKnownMembers(t *testing.T) {
	f := newRosterFixture(t)
	unsigned, _ := testDevice(t)
	if err := mergePeerRoster(f.home, f.circle.ID, []CircleMember{
		{DeviceID: unsigned.DeviceID, PublicKey: unsigned.PublicKey},
		{DeviceID: f.creator.DeviceID, PublicKey: f.creator.PublicKey, Revoked: true},
		{DeviceID: f.victim.DeviceID, PublicKey: f.victim.PublicKey, Hostname: "victim-laptop"},
	}); err != nil {
		t.Fatal(err)
	}
	c := f.reload(t)
	if _, ok := c.memberRecord(unsigned.DeviceID); ok {
		t.Fatal("gossip without an admission must not add a member")
	}
	if _, ok := c.activeMember(f.creator.DeviceID); !ok {
		t.Fatal("an unsigned revoked flag must not revoke anyone")
	}
	if m, _ := c.memberRecord(f.victim.DeviceID); m.Hostname != "victim-laptop" {
		t.Fatal("hints about a known member still refresh")
	}
}

func TestJoinRejectsADeviceIDThatIsNotItsKey(t *testing.T) {
	home := t.TempDir()
	setupTestEnv(t, home)
	id, priv, _ := loadOrCreateIdentity(home)
	circle, _ := createCircle(home, "team", id, priv)
	victim, _ := testDevice(t)
	circle = upsertMember(circle, CircleMember{DeviceID: victim.DeviceID, PublicKey: victim.PublicKey})
	if err := saveCircle(home, circle); err != nil {
		t.Fatal(err)
	}
	invite, _ := createInvite(home, circle.ID, id)
	attacker, attackerPriv := testDevice(t)
	req, err := signJoinRequest(attackerPriv, JoinRequest{
		CircleID: circle.ID, DeviceID: victim.DeviceID, PublicKey: attacker.PublicKey,
		Code: invite.Code, Timestamp: nowRFC3339(),
	})
	if err != nil {
		t.Fatal(err)
	}
	payload, _ := json.Marshal(req)
	sl := &shareListener{home: home, id: id}
	sl.tryHandleJoin(wireEnvelope{Kind: "join"}, payload)
	updated, err := loadCircle(home, circle.ID)
	if err != nil {
		t.Fatal(err)
	}
	if m, _ := updated.activeMember(victim.DeviceID); m.PublicKey != victim.PublicKey {
		t.Fatal("a join must not take over an existing device id with another key")
	}
}
