package main

import (
	"crypto/ed25519"
	"testing"
)

type proofDevice struct {
	id   DeviceIdentity
	priv ed25519.PrivateKey
}

func newProofDevice(t *testing.T) proofDevice {
	t.Helper()
	id, priv := testDevice(t)
	return proofDevice{id: id, priv: priv}
}

func (d proofDevice) member() CircleMember {
	return CircleMember{DeviceID: d.id.DeviceID, PublicKey: d.id.PublicKey}
}

func (d proofDevice) admit(t *testing.T, circleID string, joiner proofDevice) MemberAdmission {
	t.Helper()
	adm, err := signAdmission(d.priv, circleID, joiner.member(), d.id.DeviceID)
	if err != nil {
		t.Fatal(err)
	}
	return adm
}

func (d proofDevice) revoke(t *testing.T, circleID string, target proofDevice) MemberRevocation {
	t.Helper()
	rev, err := signRevocation(d.priv, circleID, target.id.DeviceID)
	if err != nil {
		t.Fatal(err)
	}
	return rev
}

func activeIn(c Circle, d proofDevice) bool {
	_, ok := c.activeMember(d.id.DeviceID)
	return ok
}

func knownIn(c Circle, d proofDevice) bool {
	for _, m := range c.Members {
		if m.DeviceID == d.id.DeviceID {
			return true
		}
	}
	return false
}

func TestAdmissionVerifiesOnlyForItsSignerCircleAndKey(t *testing.T) {
	creator, joiner, other := newProofDevice(t), newProofDevice(t), newProofDevice(t)
	adm := creator.admit(t, "circle_a", joiner)
	if !verifyAdmission("circle_a", joiner.member(), adm, creator.id.PublicKey) {
		t.Fatal("a genuine admission must verify")
	}
	if verifyAdmission("circle_a", joiner.member(), adm, other.id.PublicKey) {
		t.Fatal("an admission must not verify under another member's key")
	}
	if verifyAdmission("circle_b", joiner.member(), adm, creator.id.PublicKey) {
		t.Fatal("an admission must not carry over to another circle")
	}
	swapped := joiner.member()
	swapped.PublicKey = other.id.PublicKey
	if verifyAdmission("circle_a", swapped, adm, creator.id.PublicKey) {
		t.Fatal("an admission must not carry over to another key")
	}
}

func TestIngestAdmitsAChainFromATrustedMember(t *testing.T) {
	creator, self, a, b := newProofDevice(t), newProofDevice(t), newProofDevice(t), newProofDevice(t)
	circle := testCircle(creator.id, self.id)
	got := ingestRosterProofs(circle, self.id.DeviceID,
		[]CircleMember{b.member(), a.member()},
		map[string]MemberAdmission{
			a.id.DeviceID: creator.admit(t, circle.ID, a),
			b.id.DeviceID: a.admit(t, circle.ID, b),
		}, nil)
	if !activeIn(got, a) || !activeIn(got, b) {
		t.Fatal("a device invited by a newly admitted member must join in the same batch")
	}
	if got.Admissions[b.id.DeviceID].InvitedBy != a.id.DeviceID {
		t.Fatal("the roster must remember who invited each device")
	}
}

func TestIngestDropsDevicesWithoutATrustedAdmission(t *testing.T) {
	creator, self, outsider, puppet, selfSigned, bare := newProofDevice(t), newProofDevice(t), newProofDevice(t), newProofDevice(t), newProofDevice(t), newProofDevice(t)
	circle := testCircle(creator.id, self.id)
	got := ingestRosterProofs(circle, self.id.DeviceID,
		[]CircleMember{outsider.member(), puppet.member(), selfSigned.member(), bare.member()},
		map[string]MemberAdmission{
			puppet.id.DeviceID:     outsider.admit(t, circle.ID, puppet),
			selfSigned.id.DeviceID: selfSigned.admit(t, circle.ID, selfSigned),
		}, nil)
	for name, d := range map[string]proofDevice{"outsider": outsider, "puppet": puppet, "self-signed": selfSigned, "bare": bare} {
		if knownIn(got, d) {
			t.Fatalf("%s has no admission from a trusted member and must stay out", name)
		}
	}
}

func TestIngestRefusesAdmissionsFromRevokedMembers(t *testing.T) {
	creator, self, gone, puppet := newProofDevice(t), newProofDevice(t), newProofDevice(t), newProofDevice(t)
	circle := testCircle(creator.id, self.id, gone.id)
	circle.Members[2].Revoked = true
	got := ingestRosterProofs(circle, self.id.DeviceID, []CircleMember{puppet.member()},
		map[string]MemberAdmission{puppet.id.DeviceID: gone.admit(t, circle.ID, puppet)}, nil)
	if knownIn(got, puppet) {
		t.Fatal("a revoked member can no longer bring devices in")
	}
}

func TestIngestRevocationNeedsTheCreator(t *testing.T) {
	creator, self, rogue, victim := newProofDevice(t), newProofDevice(t), newProofDevice(t), newProofDevice(t)
	circle := testCircle(creator.id, self.id, rogue.id, victim.id)
	got := ingestRosterProofs(circle, self.id.DeviceID, nil, nil, map[string]MemberRevocation{
		victim.id.DeviceID:  rogue.revoke(t, circle.ID, victim),
		creator.id.DeviceID: rogue.revoke(t, circle.ID, creator),
	})
	if !activeIn(got, victim) || !activeIn(got, creator) {
		t.Fatal("only the creator can revoke a member")
	}
	got = ingestRosterProofs(got, self.id.DeviceID, nil, nil, map[string]MemberRevocation{
		rogue.id.DeviceID:   creator.revoke(t, circle.ID, rogue),
		creator.id.DeviceID: creator.revoke(t, circle.ID, creator),
	})
	if activeIn(got, rogue) {
		t.Fatal("a revocation signed by the creator must apply")
	}
	if !activeIn(got, creator) {
		t.Fatal("the creator cannot be revoked, or the circle could never revoke again")
	}
	if _, ok := got.Revocations[rogue.id.DeviceID]; !ok {
		t.Fatal("an applied revocation must be kept so it can be relayed")
	}
}

func TestIngestRevocationThatArrivesBeforeTheMember(t *testing.T) {
	creator, self, late := newProofDevice(t), newProofDevice(t), newProofDevice(t)
	circle := testCircle(creator.id, self.id)
	got := ingestRosterProofs(circle, self.id.DeviceID, nil, nil, map[string]MemberRevocation{
		late.id.DeviceID: creator.revoke(t, circle.ID, late),
	})
	got = ingestRosterProofs(got, self.id.DeviceID, []CircleMember{late.member()},
		map[string]MemberAdmission{late.id.DeviceID: creator.admit(t, circle.ID, late)}, nil)
	if activeIn(got, late) {
		t.Fatal("a device revoked before its admission arrives must join already revoked")
	}
}

func TestIngestTrustsTheCreatorSelfAdmission(t *testing.T) {
	creator, inviter, self, peer := newProofDevice(t), newProofDevice(t), newProofDevice(t), newProofDevice(t)
	circle := testCircle(inviter.id, self.id)
	circle.CreatorDeviceID = creator.id.DeviceID
	got := ingestRosterProofs(circle, self.id.DeviceID,
		[]CircleMember{peer.member(), creator.member()},
		map[string]MemberAdmission{
			creator.id.DeviceID: creator.admit(t, circle.ID, creator),
			peer.id.DeviceID:    creator.admit(t, circle.ID, peer),
		}, nil)
	if !activeIn(got, creator) || !activeIn(got, peer) {
		t.Fatal("a joiner must reach the creator and the members it admitted")
	}
}
