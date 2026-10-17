package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestKeysExchangeCarriesTheNewKeyToAMember(t *testing.T) {
	setupTestEnv(t, t.TempDir())
	creator := startRosterCore(t, "creator")
	member := startRosterCore(t, "member")
	circle, err := createCircle(creator.home, "team", creator.id, creator.priv)
	if err != nil {
		t.Fatal(err)
	}
	member.join(t, creator, circle.ID)

	if err := member.pullKeys(t, creator, circle.ID); err != nil {
		t.Fatal(err)
	}
	if _, ok := creator.circle(t, circle.ID).KexKeys[member.id.DeviceID]; !ok {
		t.Fatal("a keys request must hand the requester's key-agreement key to the peer")
	}
	rotated, cutOff, err := rekeyCircle(creator.circle(t, circle.ID), creator.id, creator.priv)
	if err != nil || len(cutOff) != 0 {
		t.Fatalf("rekey: %v, cut off %v", err, cutOff)
	}
	if err := saveCircle(creator.home, rotated); err != nil {
		t.Fatal(err)
	}
	if err := member.pullKeys(t, creator, circle.ID); err != nil {
		t.Fatal(err)
	}
	if got := member.circle(t, circle.ID); got.GroupKey != rotated.GroupKey || got.KeyEpoch != rotated.KeyEpoch {
		t.Fatal("a member that pulls keys must move to the creator's new key")
	}
}

func TestKeysExchangeWorksForAMemberThatMissedTheRotation(t *testing.T) {
	setupTestEnv(t, t.TempDir())
	creator := startRosterCore(t, "creator")
	member := startRosterCore(t, "member")
	circle, err := createCircle(creator.home, "team", creator.id, creator.priv)
	if err != nil {
		t.Fatal(err)
	}
	member.join(t, creator, circle.ID)
	if err := member.pullKeys(t, creator, circle.ID); err != nil {
		t.Fatal(err)
	}
	current := creator.circle(t, circle.ID)
	for epoch := 0; epoch < 2; epoch++ {
		if current, _, err = rekeyCircle(current, creator.id, creator.priv); err != nil {
			t.Fatal(err)
		}
	}
	current.PreviousKey = nil
	if err := saveCircle(creator.home, current); err != nil {
		t.Fatal(err)
	}
	if err := member.pullKeys(t, creator, circle.ID); err != nil {
		t.Fatalf("a member two epochs behind shares no key with the creator, yet must still reach it: %v", err)
	}
	if got := member.circle(t, circle.ID); got.GroupKey != current.GroupKey {
		t.Fatal("a member two epochs behind must jump to the current key")
	}
}

func TestAdoptingANewKeyDropsInvitesAndResealsTheOutbox(t *testing.T) {
	home := meshHome(t)
	self, selfPriv := testDevice(t)
	peer, _ := testDevice(t)
	writeIdentityFile(t, home, self)
	circle := saveTestCircle(t, home, testCircle(self, peer))
	if _, err := createInvite(home, circle.ID, self); err != nil {
		t.Fatal(err)
	}
	queued, _, err := postLocalChat(home, chatPostRequest{MeshID: circle.ID, Recipient: peer.DeviceID, Type: "text", Text: "queued"})
	if err != nil {
		t.Fatal(err)
	}
	rotated, _, err := rekeyCircle(circle, self, selfPriv)
	if err != nil {
		t.Fatal(err)
	}
	if err := saveCircle(home, rotated); err != nil {
		t.Fatal(err)
	}
	afterKeyRotation(home, rotated)

	entries, _ := os.ReadDir(invitesDir(home))
	for _, e := range entries {
		raw, _ := os.ReadFile(filepath.Join(invitesDir(home), e.Name()))
		var invite CircleInvite
		if json.Unmarshal(raw, &invite) == nil && invite.CircleID == circle.ID {
			t.Fatal("an invite carrying the old key must not outlive the rotation")
		}
	}
	items, err := loadOutbox(home, circle.ID)
	if err != nil || len(items) != 1 {
		t.Fatalf("outbox: %v %v", items, err)
	}
	env, err := openChatWire(rotated.GroupKey, items[0].Wire)
	if err != nil || env.MessageID != queued.MessageID {
		t.Fatalf("a queued message must leave sealed with the new key: %v", err)
	}
}
