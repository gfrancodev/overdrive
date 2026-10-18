package main

import (
	"testing"
)

func pairingParty(t *testing.T) (DeviceIdentity, pairingPeer, proofDevice) {
	t.Helper()
	d := newProofDevice(t)
	rec, err := selfKexRecord(d.id, d.priv)
	if err != nil {
		t.Fatal(err)
	}
	return d.id, pairingPeer{DeviceID: d.id.DeviceID, PublicKey: d.id.PublicKey, KexPublicKey: rec.PublicKey, Hostname: "host-" + d.id.DeviceID[4:8]}, d
}

func TestPairingCodeMatchesOnlyWhenBothSidesSawTheSameKeys(t *testing.T) {
	_, joiner, _ := pairingParty(t)
	_, member, _ := pairingParty(t)
	_, impostor, _ := pairingParty(t)
	nonceJ, nonceM := []byte("joiner-nonce-0123456789abcdef012"), []byte("member-nonce-0123456789abcdef012")
	commit := pairingCommit("ps_1", joiner, nonceJ)
	honestJ := pairingCode("ps_1", joiner, member, commit, nonceM, nonceJ)
	honestM := pairingCode("ps_1", joiner, member, commit, nonceM, nonceJ)
	if honestJ != honestM || len(honestJ) != 6 {
		t.Fatalf("both sides of an honest pairing must show the same 6-digit code: %q %q", honestJ, honestM)
	}
	if swapped := pairingCode("ps_1", joiner, impostor, commit, nonceM, nonceJ); swapped == honestJ {
		t.Fatal("a swapped member key must change the code")
	}
	if !pairingRevealMatches("ps_1", joiner, commit, nonceJ) {
		t.Fatal("the joiner's own nonce must open its commitment")
	}
	if pairingRevealMatches("ps_1", joiner, commit, nonceM) {
		t.Fatal("a different nonce must not open the commitment")
	}
}

func TestPairingWelcomeOpensOnlyForTheJoiner(t *testing.T) {
	memberID, member, memberDev := pairingParty(t)
	_, joiner, joinerDev := pairingParty(t)
	_, _, other := pairingParty(t)
	circle := testCircle(memberID)
	circle.CreatorDeviceID = memberID.DeviceID
	welcome, _, err := buildPairingWelcome("ps_2", circle, memberID, memberDev.priv, joiner)
	if err != nil {
		t.Fatal(err)
	}
	got, err := openPairingWelcome("ps_2", welcome, member, joinerDev.id, joinerDev.priv)
	if err != nil {
		t.Fatal(err)
	}
	if got.Circle.GroupKey != circle.GroupKey || got.Circle.ID != circle.ID {
		t.Fatal("the joiner must receive the circle key it was admitted with")
	}
	if adm, ok := got.Circle.Admissions[joiner.DeviceID]; !ok || adm.InvitedBy != memberID.DeviceID {
		t.Fatal("the welcome must carry the joiner's admission signed by the member")
	}
	if _, err := openPairingWelcome("ps_2", welcome, member, other.id, other.priv); err == nil {
		t.Fatal("another device must not open a welcome sealed for the joiner")
	}
	if _, err := openPairingWelcome("ps_other", welcome, member, joinerDev.id, joinerDev.priv); err == nil {
		t.Fatal("a welcome must not open under another pairing session")
	}
	_, forger, _ := pairingParty(t)
	if _, err := openPairingWelcome("ps_2", welcome, forger, joinerDev.id, joinerDev.priv); err == nil {
		t.Fatal("a welcome must be signed by the member the joiner compared codes with")
	}
}
