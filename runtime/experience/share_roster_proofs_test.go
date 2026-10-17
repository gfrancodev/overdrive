package main

import (
	"crypto/ed25519"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
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
	got := ingestRosterProofs(circle,
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
	got := ingestRosterProofs(circle,
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
	got := ingestRosterProofs(circle, []CircleMember{puppet.member()},
		map[string]MemberAdmission{puppet.id.DeviceID: gone.admit(t, circle.ID, puppet)}, nil)
	if knownIn(got, puppet) {
		t.Fatal("a revoked member can no longer bring devices in")
	}
}

func TestIngestRevocationNeedsTheCreator(t *testing.T) {
	creator, self, rogue, victim := newProofDevice(t), newProofDevice(t), newProofDevice(t), newProofDevice(t)
	circle := testCircle(creator.id, self.id, rogue.id, victim.id)
	got := ingestRosterProofs(circle, nil, nil, map[string]MemberRevocation{
		victim.id.DeviceID:  rogue.revoke(t, circle.ID, victim),
		creator.id.DeviceID: rogue.revoke(t, circle.ID, creator),
	})
	if !activeIn(got, victim) || !activeIn(got, creator) {
		t.Fatal("only the creator can revoke a member")
	}
	got = ingestRosterProofs(got, nil, nil, map[string]MemberRevocation{
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
	got := ingestRosterProofs(circle, nil, nil, map[string]MemberRevocation{
		late.id.DeviceID: creator.revoke(t, circle.ID, late),
	})
	got = ingestRosterProofs(got, []CircleMember{late.member()},
		map[string]MemberAdmission{late.id.DeviceID: creator.admit(t, circle.ID, late)}, nil)
	if activeIn(got, late) {
		t.Fatal("a device revoked before its admission arrives must join already revoked")
	}
}

func TestIngestTrustsTheCreatorSelfAdmission(t *testing.T) {
	creator, inviter, self, peer := newProofDevice(t), newProofDevice(t), newProofDevice(t), newProofDevice(t)
	circle := testCircle(inviter.id, self.id)
	circle.CreatorDeviceID = creator.id.DeviceID
	got := ingestRosterProofs(circle,
		[]CircleMember{peer.member(), creator.member()},
		map[string]MemberAdmission{
			creator.id.DeviceID: creator.admit(t, circle.ID, creator),
			peer.id.DeviceID:    creator.admit(t, circle.ID, peer),
		}, nil)
	if !activeIn(got, creator) || !activeIn(got, peer) {
		t.Fatal("a joiner must reach the creator and the members it admitted")
	}
}

func TestCreateCircleSelfAdmitsTheCreator(t *testing.T) {
	home := t.TempDir()
	setupTestEnv(t, home)
	id, priv, _ := loadOrCreateIdentity(home)
	circle, err := createCircle(home, "team", id, priv)
	if err != nil {
		t.Fatal(err)
	}
	creator, _ := circle.memberRecord(id.DeviceID)
	adm, ok := circle.Admissions[id.DeviceID]
	if !ok || !circle.trustsAdmission(creator, adm) {
		t.Fatal("a new circle must carry the creator's own admission")
	}
}

func TestPersistVouchesForMembersFromBeforeSignedAdmissions(t *testing.T) {
	home := meshHome(t)
	creator, self, legacy, gone := newProofDevice(t), newProofDevice(t), newProofDevice(t), newProofDevice(t)
	writeIdentityFile(t, home, self.id)
	circle := testCircle(creator.id, self.id, legacy.id, gone.id)
	circle.Members[3].Revoked = true
	if err := persistJoinedCircle(home, circle); err != nil {
		t.Fatal(err)
	}
	got, err := loadCircle(home, circle.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, d := range []proofDevice{creator, legacy} {
		adm, ok := got.Admissions[d.id.DeviceID]
		if !ok || adm.InvitedBy != self.id.DeviceID || !got.trustsAdmission(d.member(), adm) {
			t.Fatalf("%s needs an admission vouched by this core", d.id.DeviceID)
		}
	}
	if _, ok := got.Admissions[self.id.DeviceID]; ok {
		t.Fatal("a member other than the creator cannot admit itself")
	}
	if _, ok := got.Admissions[gone.id.DeviceID]; ok {
		t.Fatal("a revoked member gets no admission")
	}
	if _, ok := got.Revocations[gone.id.DeviceID]; ok {
		t.Fatal("only the creator signs revocations")
	}
}

func TestPersistOnTheCreatorSignsExistingRevocations(t *testing.T) {
	home := meshHome(t)
	creator, gone := newProofDevice(t), newProofDevice(t)
	writeIdentityFile(t, home, creator.id)
	circle := testCircle(creator.id, gone.id)
	circle.Members[1].Revoked = true
	if err := persistJoinedCircle(home, circle); err != nil {
		t.Fatal(err)
	}
	got, err := loadCircle(home, circle.ID)
	if err != nil {
		t.Fatal(err)
	}
	rev, ok := got.Revocations[gone.id.DeviceID]
	if !ok || !verifyRevocation(got.ID, gone.id.DeviceID, rev, creator.id.PublicKey) {
		t.Fatal("the creator must sign a revocation for every member it already revoked")
	}
	if _, ok := got.Admissions[creator.id.DeviceID]; !ok {
		t.Fatal("the creator must admit itself")
	}
}

func TestRevokeMemberSignsTheRevocation(t *testing.T) {
	home := t.TempDir()
	setupTestEnv(t, home)
	id, priv, _ := loadOrCreateIdentity(home)
	circle, _ := createCircle(home, "team", id, priv)
	member := newProofDevice(t)
	circle = upsertMember(circle, member.member())
	if err := saveCircle(home, circle); err != nil {
		t.Fatal(err)
	}
	got, err := revokeMember(home, circle.ID, member.id.DeviceID, id, priv)
	if err != nil {
		t.Fatal(err)
	}
	rev, ok := got.Revocations[member.id.DeviceID]
	if !ok || !verifyRevocation(got.ID, member.id.DeviceID, rev, id.PublicKey) {
		t.Fatal("revoke must leave a creator-signed revocation to relay")
	}
	if _, err := revokeMember(home, circle.ID, id.DeviceID, id, priv); err == nil {
		t.Fatal("the creator cannot revoke itself")
	}
}

func TestJoinAdmitsTheJoinerUnderTheInviter(t *testing.T) {
	home := t.TempDir()
	setupTestEnv(t, home)
	id, priv, _ := loadOrCreateIdentity(home)
	circle, _ := createCircle(home, "team", id, priv)
	invite, _ := createInvite(home, circle.ID, id)
	joiner := newProofDevice(t)
	req, err := signJoinRequest(joiner.priv, JoinRequest{
		CircleID: circle.ID, DeviceID: joiner.id.DeviceID, PublicKey: joiner.id.PublicKey,
		Code: invite.Code, Timestamp: nowRFC3339(),
	})
	if err != nil {
		t.Fatal(err)
	}
	payload, _ := json.Marshal(req)
	sl := &shareListener{home: home, id: id}
	sl.tryHandleJoin(wireEnvelope{Kind: "join"}, payload)
	got, err := loadCircle(home, circle.ID)
	if err != nil {
		t.Fatal(err)
	}
	adm, ok := got.Admissions[joiner.id.DeviceID]
	if !ok || adm.InvitedBy != id.DeviceID || !got.trustsAdmission(joiner.member(), adm) {
		t.Fatal("the inviter must sign the joiner's admission")
	}
}

func joinerFixture(t *testing.T) (home string, creator, inviter, self proofDevice, circle Circle) {
	t.Helper()
	home = meshHome(t)
	creator, inviter, self = newProofDevice(t), newProofDevice(t), newProofDevice(t)
	writeIdentityFile(t, home, self.id)
	circle = testCircle(inviter.id, self.id)
	circle.CreatorDeviceID = creator.id.DeviceID
	return home, creator, inviter, self, saveTestCircle(t, home, circle)
}

func TestJoinAckWithProofsAdmitsOnlyVouchedMembers(t *testing.T) {
	home, creator, inviter, self, circle := joinerFixture(t)
	peer, puppet := newProofDevice(t), newProofDevice(t)
	ack := joinAck{
		CircleID: circle.ID,
		Members:  []CircleMember{creator.member(), inviter.member(), peer.member(), puppet.member(), self.member()},
		Admissions: map[string]MemberAdmission{
			creator.id.DeviceID: creator.admit(t, circle.ID, creator),
			inviter.id.DeviceID: creator.admit(t, circle.ID, inviter),
			peer.id.DeviceID:    creator.admit(t, circle.ID, peer),
			self.id.DeviceID:    inviter.admit(t, circle.ID, self),
		},
	}
	if err := applyJoinAck(home, circle.ID, ack); err != nil {
		t.Fatal(err)
	}
	got, err := loadCircle(home, circle.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !activeIn(got, creator) || !activeIn(got, peer) {
		t.Fatal("the joiner must learn every member the creator chain vouches for")
	}
	if knownIn(got, puppet) {
		t.Fatal("a member without an admission must not ride in on a join ack")
	}
	if _, ok := got.Admissions[self.id.DeviceID]; !ok {
		t.Fatal("the joiner keeps its own admission to relay")
	}
}

func TestJoinAckFromAnOlderCoreStillTrustsTheInviter(t *testing.T) {
	home, creator, inviter, self, circle := joinerFixture(t)
	peer := newProofDevice(t)
	gone := newProofDevice(t)
	goneMember := gone.member()
	goneMember.Revoked = true
	ack := joinAck{CircleID: circle.ID, Members: []CircleMember{
		creator.member(), inviter.member(), peer.member(), goneMember, self.member(),
		{DeviceID: "dev_00112233445566ff", PublicKey: peer.id.PublicKey},
	}}
	if err := applyJoinAck(home, circle.ID, ack); err != nil {
		t.Fatal(err)
	}
	got, err := loadCircle(home, circle.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !activeIn(got, creator) || !activeIn(got, peer) {
		t.Fatal("an ack without proofs comes from an older inviter the joiner already trusts")
	}
	if activeIn(got, gone) {
		t.Fatal("the older inviter's revocations still apply on join")
	}
	if _, ok := got.memberRecord("dev_00112233445566ff"); ok {
		t.Fatal("a key that does not derive its device id is still refused")
	}
}

func TestAcceptInviteRecordsTheCircleCreator(t *testing.T) {
	home := meshHome(t)
	self, inviter, creator := newProofDevice(t), newProofDevice(t), newProofDevice(t)
	writeIdentityFile(t, home, self.id)
	write := func(invite CircleInvite) {
		t.Helper()
		raw, _ := json.Marshal(invite)
		if err := os.MkdirAll(invitesDir(home), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(invitesDir(home), invite.Code+".json"), raw, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	invite := CircleInvite{
		CircleID: "circle_join01", CircleName: "team", Code: "abc123", Fingerprint: "fp",
		GroupKey: testGroupKey(), ExpiresAt: time.Now().Add(time.Hour).UTC().Format(time.RFC3339),
		CreatorPK: inviter.id.PublicKey, CreatorID: inviter.id.DeviceID, CircleCreatorID: creator.id.DeviceID,
	}
	write(invite)
	got, err := acceptInvite(home, invite.Code, invite.Fingerprint, "", self.id, self.priv)
	if err != nil {
		t.Fatal(err)
	}
	if got.CreatorDeviceID != creator.id.DeviceID {
		t.Fatalf("the joiner must record the circle creator, not the inviter: %s", got.CreatorDeviceID)
	}
	forged := invite
	forged.CircleID, forged.Code = "circle_join02", "def456"
	forged.CreatorPK = creator.id.PublicKey
	write(forged)
	if _, err := acceptInvite(home, forged.Code, forged.Fingerprint, "", self.id, self.priv); err == nil {
		t.Fatal("an invite whose inviter key does not derive the inviter id must be refused")
	}
}
