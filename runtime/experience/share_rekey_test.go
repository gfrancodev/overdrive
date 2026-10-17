package main

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"testing"
	"time"
)

func TestDeviceKexKeyIsDerivedFromTheIdentity(t *testing.T) {
	d := newProofDevice(t)
	first, err := deviceKexKey(d.priv)
	if err != nil {
		t.Fatal(err)
	}
	again, err := deviceKexKey(d.priv)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(first.PublicKey().Bytes(), again.PublicKey().Bytes()) {
		t.Fatal("the key-agreement key must be the same every time it is derived")
	}
	other, err := deviceKexKey(newProofDevice(t).priv)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Equal(first.PublicKey().Bytes(), other.PublicKey().Bytes()) {
		t.Fatal("two devices must not share a key-agreement key")
	}
}

func TestKexRecordVerifiesOnlyUnderItsDeviceKey(t *testing.T) {
	d, other := newProofDevice(t), newProofDevice(t)
	rec, err := selfKexRecord(d.id, d.priv)
	if err != nil {
		t.Fatal(err)
	}
	if !verifyKexRecord(rec, d.id.PublicKey) {
		t.Fatal("a device's own kex record must verify")
	}
	if verifyKexRecord(rec, other.id.PublicKey) {
		t.Fatal("a kex record must not verify under another device's key")
	}
	forged, err := selfKexRecord(other.id, other.priv)
	if err != nil {
		t.Fatal(err)
	}
	forged.DeviceID = d.id.DeviceID
	if verifyKexRecord(forged, d.id.PublicKey) {
		t.Fatal("a device must not publish a kex key in another device's name")
	}
}

func kexRecordFor(t *testing.T, d proofDevice) DeviceKexRecord {
	t.Helper()
	rec, err := selfKexRecord(d.id, d.priv)
	if err != nil {
		t.Fatal(err)
	}
	return rec
}

type rekeyFixture struct {
	creator, member, gone, stale proofDevice
	circle                       Circle
}

func newRekeyFixture(t *testing.T) rekeyFixture {
	t.Helper()
	f := rekeyFixture{creator: newProofDevice(t), member: newProofDevice(t), gone: newProofDevice(t), stale: newProofDevice(t)}
	circle := testCircle(f.creator.id, f.member.id, f.gone.id, f.stale.id)
	circle.Members[2].Revoked = true
	f.circle = circle.ingestKexRecords([]DeviceKexRecord{
		kexRecordFor(t, f.creator), kexRecordFor(t, f.member), kexRecordFor(t, f.gone),
	})
	return f
}

func TestRekeyHandsTheNewKeyOnlyToRemainingMembers(t *testing.T) {
	f := newRekeyFixture(t)
	rotated, cutOff, err := rekeyCircle(f.circle, f.creator.id, f.creator.priv)
	if err != nil {
		t.Fatal(err)
	}
	if rotated.GroupKey == f.circle.GroupKey || rotated.KeyEpoch != f.circle.KeyEpoch+1 {
		t.Fatal("a rekey must produce a new key in the next epoch")
	}
	if rotated.PreviousKey == nil || rotated.PreviousKey.Key != f.circle.GroupKey {
		t.Fatal("the old key stays for the grace window so in-flight messages still open")
	}
	if len(cutOff) != 1 || cutOff[0] != f.stale.id.DeviceID {
		t.Fatalf("members without a key-agreement key must be named, got %v", cutOff)
	}
	entries := rotated.KeyUpdate.Entries
	if _, ok := entries[f.member.id.DeviceID]; !ok {
		t.Fatal("an active member must get the new key")
	}
	if _, ok := entries[f.gone.id.DeviceID]; ok {
		t.Fatal("a revoked member must not get the new key")
	}
	adopted, ok := f.circle.adoptKeyUpdate(*rotated.KeyUpdate, f.member.id, f.member.priv)
	if !ok || adopted.GroupKey != rotated.GroupKey || adopted.KeyEpoch != rotated.KeyEpoch {
		t.Fatal("a member must open its entry and move to the new key")
	}
	if _, ok := f.circle.adoptKeyUpdate(*rotated.KeyUpdate, f.gone.id, f.gone.priv); ok {
		t.Fatal("a revoked member has no entry to open")
	}
	if _, _, err := rekeyCircle(f.circle, f.member.id, f.member.priv); err == nil {
		t.Fatal("only the creator rotates the circle key")
	}
}

func TestKeyUpdateMustBeTheCreatorsAndNewer(t *testing.T) {
	f := newRekeyFixture(t)
	rotated, _, err := rekeyCircle(f.circle, f.creator.id, f.creator.priv)
	if err != nil {
		t.Fatal(err)
	}
	update := *rotated.KeyUpdate
	forged := update
	forged.Signature = ""
	forged, err = signKeyUpdate(f.member.priv, forged)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := f.circle.adoptKeyUpdate(forged, f.member.id, f.member.priv); ok {
		t.Fatal("a key update not signed by the creator must be ignored")
	}
	adopted, ok := f.circle.adoptKeyUpdate(update, f.member.id, f.member.priv)
	if !ok {
		t.Fatal("precondition: the genuine update applies")
	}
	if _, ok := adopted.adoptKeyUpdate(update, f.member.id, f.member.priv); ok {
		t.Fatal("an update for the current epoch must not apply twice")
	}
	moved := update
	moved.Entries = map[string]string{f.stale.id.DeviceID: update.Entries[f.member.id.DeviceID]}
	if _, ok := f.circle.adoptKeyUpdate(moved, f.stale.id, f.stale.priv); ok {
		t.Fatal("an entry sealed for one device must not open for another")
	}
}

func TestCircleOpensWithThePreviousKeyOnlyDuringTheGraceWindow(t *testing.T) {
	f := newRekeyFixture(t)
	sealedOld, err := encryptBatch(f.circle.GroupKey, []byte("in flight"))
	if err != nil {
		t.Fatal(err)
	}
	rotated, _, err := rekeyCircle(f.circle, f.creator.id, f.creator.priv)
	if err != nil {
		t.Fatal(err)
	}
	if plain, err := decryptCircle(rotated, sealedOld); err != nil || string(plain) != "in flight" {
		t.Fatal("a message sealed just before the rotation must still open")
	}
	sealedNew, _ := encryptBatch(rotated.GroupKey, []byte("after"))
	if plain, err := decryptCircle(rotated, sealedNew); err != nil || string(plain) != "after" {
		t.Fatal("the current key must open")
	}
	rotated.PreviousKey.ExpiresAt = time.Now().Add(-time.Minute).UTC().Format(time.RFC3339)
	if _, err := decryptCircle(rotated, sealedOld); err == nil {
		t.Fatal("the previous key must stop opening once the grace window ends")
	}
}

func TestIngestKexRecordsKeepsOnlySignedRecordsOfMembers(t *testing.T) {
	f := newRekeyFixture(t)
	outsider := newProofDevice(t)
	spoofed := kexRecordFor(t, outsider)
	spoofed.DeviceID = f.stale.id.DeviceID
	got := f.circle.ingestKexRecords([]DeviceKexRecord{kexRecordFor(t, outsider), spoofed})
	if _, ok := got.KexKeys[outsider.id.DeviceID]; ok {
		t.Fatal("a device outside the roster must not register a key-agreement key")
	}
	if _, ok := got.KexKeys[f.stale.id.DeviceID]; ok {
		t.Fatal("a key-agreement key must be signed by the device it names")
	}
	got = got.ingestKexRecords([]DeviceKexRecord{kexRecordFor(t, f.stale)})
	if _, ok := got.KexKeys[f.stale.id.DeviceID]; !ok {
		t.Fatal("a member's own signed record must be kept")
	}
}

func TestChatSealedJustBeforeARotationStillDelivers(t *testing.T) {
	home := meshHome(t)
	self, _ := testDevice(t)
	peer, peerPriv := testDevice(t)
	writeIdentityFile(t, home, self)
	circle := saveTestCircle(t, home, testCircle(self, peer))
	seal := func(id, key string) []byte {
		t.Helper()
		payload, _ := json.Marshal(chatPayload{Text: "hello"})
		env, err := signChatEnvelope(peerPriv, chatEnvelope{
			Version: chatEnvelopeVersion, MessageID: id, MeshID: circle.ID,
			ConversationID: directConversationID(self.DeviceID, peer.DeviceID),
			SenderUserID:   "usr_0123456789abcdef", SenderDeviceID: peer.DeviceID,
			Recipient: self.DeviceID, Type: "text", Timestamp: nowRFC3339(), Payload: payload,
		})
		if err != nil {
			t.Fatal(err)
		}
		wire, err := sealChatEnvelope(key, env)
		if err != nil {
			t.Fatal(err)
		}
		return wire
	}
	oldKey := circle.GroupKey
	inFlight := seal("msg_rotation_in_flight_01", oldKey)
	rotated := circle.moveToKey(1, base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{7}, 32)))
	saveTestCircle(t, home, rotated)
	if ok, err := acceptChatWire(home, inFlight); err != nil || !ok {
		t.Fatalf("a message sealed before the rotation must still deliver: %v %v", ok, err)
	}
	if ok, err := acceptChatWire(home, seal("msg_rotation_after_00001", rotated.GroupKey)); err != nil || !ok {
		t.Fatalf("a message sealed with the new key must deliver: %v %v", ok, err)
	}
	rotated.PreviousKey.ExpiresAt = time.Now().Add(-time.Minute).UTC().Format(time.RFC3339)
	saveTestCircle(t, home, rotated)
	if ok, _ := acceptChatWire(home, seal("msg_rotation_too_late_01", oldKey)); ok {
		t.Fatal("after the grace window the old key must no longer deliver")
	}
}

func TestCreatorHandsTheCurrentKeyToAMemberThatUpgradesLater(t *testing.T) {
	f := newRekeyFixture(t)
	rotated, cutOff, err := rekeyCircle(f.circle, f.creator.id, f.creator.priv)
	if err != nil || len(cutOff) != 1 {
		t.Fatalf("precondition: the stale member is cut off, got %v %v", cutOff, err)
	}
	rotated = rotated.ingestKexRecords([]DeviceKexRecord{kexRecordFor(t, f.stale)})
	extended := rotated.extendKeyUpdate(f.creator.id, f.creator.priv)
	if extended.KeyUpdate.Epoch != rotated.KeyEpoch {
		t.Fatal("extending must not start a new epoch")
	}
	adopted, ok := f.circle.adoptKeyUpdate(*extended.KeyUpdate, f.stale.id, f.stale.priv)
	if !ok || adopted.GroupKey != rotated.GroupKey {
		t.Fatal("a member that publishes its key-agreement key later must receive the current key")
	}
	if _, ok := f.circle.adoptKeyUpdate(*extended.KeyUpdate, f.member.id, f.member.priv); !ok {
		t.Fatal("extending must keep the entries already handed out")
	}
}
