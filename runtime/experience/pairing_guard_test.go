package main

import (
	"bufio"
	"crypto/ed25519"
	"crypto/hpke"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net"
	"strings"
	"testing"
	"time"
)

// The checks that keep a third device out of a pairing. Mutation testing found each of them untested.

func signedPairMessage(t *testing.T, d proofDevice, self pairingPeer, m pairMessage) pairMessage {
	t.Helper()
	m.Joiner = self
	signed, err := signPairMessage(d.priv, m)
	if err != nil {
		t.Fatal(err)
	}
	return signed
}

func TestPairMessagesMustBeSignedByTheJoiner(t *testing.T) {
	_, self, dev := pairingParty(t)
	now := time.Now()
	m := signedPairMessage(t, dev, self, pairMessage{Op: "hello", SessionID: "ps_1", Commit: "c", Timestamp: now.UTC().Format(time.RFC3339)})
	if !verifyPairMessage(m, now) {
		t.Fatal("a message signed by the joiner must verify")
	}
	changed := m
	changed.Op = "cancel"
	if verifyPairMessage(changed, now) {
		t.Fatal("a message changed after signing must not verify")
	}
	forged := m
	forged.Signature = base64.StdEncoding.EncodeToString(make([]byte, ed25519.SignatureSize))
	if verifyPairMessage(forged, now) {
		t.Fatal("a forged signature must not verify")
	}
	_, other, _ := pairingParty(t)
	swapped := m
	swapped.Joiner.PublicKey = other.PublicKey
	if verifyPairMessage(swapped, now) {
		t.Fatal("a key that is not the device id's must not verify")
	}
}

func TestPairMessagesOnlyHoldWithinTheClockSkew(t *testing.T) {
	_, self, dev := pairingParty(t)
	now := time.Now().UTC().Truncate(time.Second)
	at := func(ts time.Time) bool {
		return verifyPairMessage(signedPairMessage(t, dev, self, pairMessage{Op: "poll", SessionID: "ps_1", Timestamp: ts.Format(time.RFC3339)}), now)
	}
	if !at(now.Add(-rosterMaxSkew)) || !at(now.Add(rosterMaxSkew)) {
		t.Fatal("a message right at the skew limit still holds")
	}
	if at(now.Add(-rosterMaxSkew-time.Second)) || at(now.Add(rosterMaxSkew+time.Second)) {
		t.Fatal("a message past the skew limit must not hold")
	}
	if verifyPairMessage(signedPairMessage(t, dev, self, pairMessage{Op: "poll", SessionID: "ps_1", Timestamp: "yesterday"}), now) {
		t.Fatal("a message without a readable time must not hold")
	}
}

func TestPairRepliesMustBeSignedByTheMember(t *testing.T) {
	_, member, dev := pairingParty(t)
	r := signPairReply(dev.priv, pairReply{OK: true, State: "connecting", SessionID: "ps_1", Member: member, Nonce: "bm9uY2U="})
	if !verifyPairReply(r) {
		t.Fatal("a reply signed by the member must verify")
	}
	changed := r
	changed.Nonce = "b3RoZXI="
	if verifyPairReply(changed) {
		t.Fatal("a reply changed after signing must not verify")
	}
	forged := r
	forged.Signature = base64.StdEncoding.EncodeToString(make([]byte, ed25519.SignatureSize))
	if verifyPairReply(forged) {
		t.Fatal("a forged reply must not verify")
	}
}

func clearPairings(t *testing.T, home string) {
	t.Helper()
	t.Cleanup(func() {
		pairings.Lock()
		defer pairings.Unlock()
		for key, s := range pairings.byKey {
			if s.home == home {
				delete(pairings.byKey, key)
			}
		}
	})
}

func TestMemberRefusesHellosThatCannotStartAPairing(t *testing.T) {
	home := t.TempDir()
	clearPairings(t, home)
	_, member, _ := pairingParty(t)
	_, joiner, _ := pairingParty(t)
	hello := func(sessionID, commit string) pairReply {
		return memberHandlePair(home, member, pairMessage{Op: "hello", SessionID: sessionID, Commit: commit, Joiner: joiner})
	}
	for _, c := range []struct{ id, commit string }{{"", "c"}, {"ps_1", ""}, {"ps_" + strings.Repeat("x", 62), "c"}} {
		if r := hello(c.id, c.commit); r.OK || r.Error != "bad pairing" {
			t.Fatalf("hello %q/%q: %+v", c.id, c.commit, r)
		}
	}
	if r := hello("ps_"+strings.Repeat("x", 61), "c"); !r.OK || r.Nonce == "" {
		t.Fatalf("a 64-character session id is fine: %+v", r)
	}
	if r := hello("ps_"+strings.Repeat("x", 61), "c"); r.OK {
		t.Fatal("a session id already in use must not start a second pairing")
	}
	if r := memberHandlePair(home, member, pairMessage{Op: "poll", SessionID: "ps_" + strings.Repeat("x", 61), Joiner: joiner}); !r.OK || r.State != string(pairingConnecting) {
		t.Fatalf("poll: %+v", r)
	}
	if r := memberHandlePair(home, member, pairMessage{Op: "cancel", SessionID: "ps_" + strings.Repeat("x", 61), Joiner: joiner}); !r.OK || r.State != string(pairingRejected) {
		t.Fatalf("cancel: %+v", r)
	}
	if r := memberHandlePair(home, member, pairMessage{Op: "dance", SessionID: "ps_2", Joiner: joiner}); r.OK || r.Error != "bad pairing" {
		t.Fatalf("unknown op: %+v", r)
	}
}

func TestAnotherDeviceCannotTakeOverAPairing(t *testing.T) {
	home := t.TempDir()
	clearPairings(t, home)
	_, member, _ := pairingParty(t)
	_, joiner, _ := pairingParty(t)
	_, intruder, _ := pairingParty(t)
	if r := memberHandlePair(home, member, pairMessage{Op: "hello", SessionID: "ps_1", Commit: "c", Joiner: joiner}); !r.OK {
		t.Fatal(r.Error)
	}
	for _, op := range []string{"reveal", "poll", "cancel"} {
		if r := memberHandlePair(home, member, pairMessage{Op: op, SessionID: "ps_1", Nonce: "bm9uY2U=", Joiner: intruder}); r.OK || r.Error != "unknown pairing" {
			t.Fatalf("%s from another device: %+v", op, r)
		}
	}
	keyless := joiner
	keyless.PublicKey = intruder.PublicKey
	if r := memberHandlePair(home, member, pairMessage{Op: "poll", SessionID: "ps_1", Joiner: keyless}); r.OK {
		t.Fatal("the same device id with another key must not reach the session")
	}
	if v := memberSession(t, home, "ps_1"); v.State != string(pairingConnecting) {
		t.Fatalf("the session must be untouched: %+v", v)
	}
	if r := memberHandlePair(home, member, pairMessage{Op: "reveal", SessionID: "ps_unknown", Nonce: "bm9uY2U=", Joiner: joiner}); r.OK {
		t.Fatal("a reveal for no session must fail")
	}
}

func TestARevealOnlyCountsWhileTheSessionWaitsForIt(t *testing.T) {
	home := t.TempDir()
	clearPairings(t, home)
	_, member, _ := pairingParty(t)
	_, joiner, _ := pairingParty(t)
	nonce := []byte("joiner-nonce-0123456789abcdef012")
	commit := pairingCommit("ps_1", joiner, nonce)
	if r := memberHandlePair(home, member, pairMessage{Op: "hello", SessionID: "ps_1", Commit: commit, Joiner: joiner}); !r.OK {
		t.Fatal(r.Error)
	}
	reveal := pairMessage{Op: "reveal", SessionID: "ps_1", Nonce: base64.StdEncoding.EncodeToString(nonce), Joiner: joiner}
	if r := memberHandlePair(home, member, reveal); !r.OK || r.State != string(pairingComparing) {
		t.Fatalf("first reveal: %+v", r)
	}
	if r := memberHandlePair(home, member, reveal); r.OK {
		t.Fatal("a second reveal must not reset a session that is already comparing codes")
	}
}

func TestMemberCapsOpenPairings(t *testing.T) {
	home := t.TempDir()
	clearPairings(t, home)
	_, member, _ := pairingParty(t)
	for i := 0; i < maxPairingSessions; i++ {
		_, joiner, _ := pairingParty(t)
		if r := memberHandlePair(home, member, pairMessage{Op: "hello", SessionID: "ps_" + joiner.DeviceID, Commit: "c", Joiner: joiner}); !r.OK {
			t.Fatalf("pairing %d: %+v", i, r)
		}
	}
	_, late, _ := pairingParty(t)
	if r := memberHandlePair(home, member, pairMessage{Op: "hello", SessionID: "ps_late", Commit: "c", Joiner: late}); r.OK || r.Error != "too many pairings" {
		t.Fatalf("pairing %d: %+v", maxPairingSessions+1, r)
	}
	// Another home's sessions do not count against this one.
	other := t.TempDir()
	clearPairings(t, other)
	if r := memberHandlePair(other, member, pairMessage{Op: "hello", SessionID: "ps_late", Commit: "c", Joiner: late}); !r.OK {
		t.Fatalf("another core: %+v", r)
	}
}

func TestFinishedPairingsAreForgottenTenMinutesAfterTheirDeadline(t *testing.T) {
	home := t.TempDir()
	clearPairings(t, home)
	now := time.Now()
	add := func(id string, state pairingState, expires time.Time) {
		pairings.byKey[pairingKey(home, id)] = &pairingSession{home: home, ID: id, State: state, ExpiresAt: expires}
	}
	pairings.Lock()
	add("open-late", pairingComparing, now.Add(-time.Minute))
	add("open-abandoned", pairingComparing, now.Add(-time.Hour))
	add("done-recent", pairingJoined, now.Add(-9*time.Minute))
	add("done-old", pairingJoined, now.Add(-11*time.Minute))
	add("open-fresh", pairingComparing, now.Add(time.Minute))
	expirePairingsLocked(now)
	_, lateKept := pairings.byKey[pairingKey(home, "open-late")]
	late := pairings.byKey[pairingKey(home, "open-late")]
	_, recentKept := pairings.byKey[pairingKey(home, "done-recent")]
	_, oldKept := pairings.byKey[pairingKey(home, "done-old")]
	_, abandonedKept := pairings.byKey[pairingKey(home, "open-abandoned")]
	fresh := pairings.byKey[pairingKey(home, "open-fresh")]
	pairings.Unlock()
	if !lateKept || late.State != pairingExpired {
		t.Fatal("an open pairing past its deadline expires and stays listed for a while")
	}
	if abandonedKept {
		t.Fatal("a pairing abandoned long past its deadline is forgotten")
	}
	if !recentKept || oldKept {
		t.Fatalf("finished pairings go ten minutes after their deadline: recent %v old %v", recentKept, oldKept)
	}
	if fresh.State != pairingComparing {
		t.Fatal("a pairing inside its deadline stays open")
	}
}

// fakeMember answers each pairing message with what reply returns, signed by signer.
func fakeMember(t *testing.T, signer ed25519.PrivateKey, reply func(m pairMessage) pairReply) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				defer c.Close()
				line, err := bufio.NewReader(c).ReadString('\n')
				if err != nil {
					return
				}
				var env wireEnvelope
				var m pairMessage
				if json.Unmarshal([]byte(line), &env) != nil || json.Unmarshal([]byte(env.Plain), &m) != nil {
					return
				}
				out, _ := json.Marshal(signPairReply(signer, reply(m)))
				raw, _ := json.Marshal(wireEnvelope{Kind: pairKind, Plain: string(out)})
				_, _ = c.Write(append(raw, '\n'))
			}(conn)
		}
	}()
	return ln.Addr().String()
}

func TestJoinerRefusesRepliesItCannotTrust(t *testing.T) {
	home := t.TempDir()
	setupTestEnv(t, home)
	_, member, memberDev := pairingParty(t)
	_, _, impostorDev := pairingParty(t)
	nonce := base64.StdEncoding.EncodeToString([]byte("member-nonce"))

	forged := fakeMember(t, impostorDev.priv, func(m pairMessage) pairReply {
		return pairReply{OK: true, State: "connecting", SessionID: m.SessionID, Member: member, Nonce: nonce}
	})
	if _, err := startPairing(home, forged, member.DeviceID); err == nil || !strings.Contains(err.Error(), "not signed") {
		t.Fatalf("a reply signed by another key must be refused: %v", err)
	}
	elsewhere := fakeMember(t, memberDev.priv, func(m pairMessage) pairReply {
		return pairReply{OK: true, State: "connecting", SessionID: "ps_someone_else", Member: member, Nonce: nonce}
	})
	if _, err := startPairing(home, elsewhere, member.DeviceID); err == nil || !strings.Contains(err.Error(), "not signed") {
		t.Fatalf("a reply for another session must be refused: %v", err)
	}
	noNonce := fakeMember(t, memberDev.priv, func(m pairMessage) pairReply {
		return pairReply{OK: true, State: "connecting", SessionID: m.SessionID, Member: member}
	})
	if _, err := startPairing(home, noNonce, member.DeviceID); err == nil || !strings.Contains(err.Error(), "incomplete") {
		t.Fatalf("a reply without the member's nonce must be refused: %v", err)
	}
	refusing := fakeMember(t, memberDev.priv, func(m pairMessage) pairReply {
		return pairReply{Error: "too many pairings", SessionID: m.SessionID, Member: member}
	})
	if _, err := startPairing(home, refusing, member.DeviceID); err == nil || err.Error() != "too many pairings" {
		t.Fatalf("the member's refusal must reach the person: %v", err)
	}
	if len(listPairings(home)) != 0 {
		t.Fatal("a refused pairing must leave no session")
	}
}

func TestOnlyConnectionFailuresAreExplained(t *testing.T) {
	home := t.TempDir()
	setupTestEnv(t, home)
	plain := errors.New("too many pairings")
	if got := explainPairingDial(home, "dev_x", "127.0.0.1:1", plain); got != plain {
		t.Fatalf("an answer from the other device is passed on as is: %v", got)
	}
	read := &net.OpError{Op: "read", Err: errors.New("connection reset by peer")}
	if got := explainPairingDial(home, "dev_x", "127.0.0.1:1", read); got != error(read) {
		t.Fatalf("a failure after connecting is passed on as is: %v", got)
	}
}

func TestPairingUsesOnlyNearbyEntriesWithAnAddress(t *testing.T) {
	home := t.TempDir()
	setupTestEnv(t, home)
	for _, p := range []discoveredPeer{
		{DeviceID: "dev_0000000000000001", Host: "", Port: 7741},
		{DeviceID: "dev_0000000000000002", Host: "192.168.1.9", Port: 0},
		{DeviceID: "dev_0000000000000003", Host: "192.168.1.10", Port: 7741},
	} {
		if err := noteDiscoveredPeer(home, p); err != nil {
			t.Fatal(err)
		}
	}
	for _, id := range []string{"dev_0000000000000001", "dev_0000000000000002", "dev_0000000000000009"} {
		if ep, err := discoveredEndpoint(home, id); err == nil {
			t.Fatalf("%s has no usable address, got %s", id, ep)
		}
	}
	if ep, err := discoveredEndpoint(home, "dev_0000000000000003"); err != nil || ep != "192.168.1.10:7741" {
		t.Fatalf("%s %v", ep, err)
	}
}

// sealedWelcome builds a welcome around any circle, so a test can hand the joiner one a member should not send.
func sealedWelcome(t *testing.T, sessionID string, circle Circle, signer ed25519.PrivateKey, joiner pairingPeer) pairingWelcome {
	t.Helper()
	payload, err := json.Marshal(pairingWelcomeBody{Circle: circle})
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := base64.StdEncoding.DecodeString(joiner.KexPublicKey)
	pub, err := hpke.MLKEM768X25519().NewPublicKey(raw)
	if err != nil {
		t.Fatal(err)
	}
	sealed, err := hpke.Seal(pub, hpke.HKDFSHA256(), hpke.AES256GCM(), pairingWelcomeInfo(sessionID, joiner.DeviceID), payload)
	if err != nil {
		t.Fatal(err)
	}
	w := pairingWelcome{Sealed: base64.StdEncoding.EncodeToString(sealed)}
	w.Signature = base64.StdEncoding.EncodeToString(ed25519.Sign(signer, pairingWelcomeSigned(sessionID, joiner.DeviceID, w.Sealed)))
	return w
}

func TestAWelcomeOpensOnlyWithTheMembersSignatureAndAdmission(t *testing.T) {
	memberID, member, memberDev := pairingParty(t)
	_, joiner, joinerDev := pairingParty(t)
	circle := testCircle(memberID)
	welcome, admitted, err := buildPairingWelcome("ps_1", circle, memberID, memberDev.priv, joiner)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := openPairingWelcome("ps_1", welcome, member, joinerDev.id, joinerDev.priv); err != nil {
		t.Fatal(err)
	}

	resigned := welcome
	resigned.Signature = base64.StdEncoding.EncodeToString(ed25519.Sign(memberDev.priv, []byte("something else")))
	if _, err := openPairingWelcome("ps_1", resigned, member, joinerDev.id, joinerDev.priv); err == nil {
		t.Fatal("a welcome whose signature does not cover it must not open")
	}

	broken := admitted
	broken.Admissions = map[string]MemberAdmission{}
	for k, v := range admitted.Admissions {
		broken.Admissions[k] = v
	}
	adm := broken.Admissions[joiner.DeviceID]
	adm.Signature = base64.StdEncoding.EncodeToString(make([]byte, ed25519.SignatureSize))
	broken.Admissions[joiner.DeviceID] = adm
	if _, err := openPairingWelcome("ps_1", sealedWelcome(t, "ps_1", broken, memberDev.priv, joiner), member, joinerDev.id, joinerDev.priv); err == nil {
		t.Fatal("a welcome whose admission the member did not sign must not open")
	}
}

func TestInviteRedemptionChecksWhoAnswersAndWhoIssued(t *testing.T) {
	home := t.TempDir()
	setupTestEnv(t, home)
	id, priv, err := loadOrCreateIdentity(home)
	if err != nil {
		t.Fatal(err)
	}
	creatorID, creator, creatorDev := pairingParty(t)
	_, other, otherDev := pairingParty(t)
	circle := testCircle(creatorID)
	self, err := selfPairingPeer(id, priv)
	if err != nil {
		t.Fatal(err)
	}
	welcome, _, err := buildPairingWelcome("pi_x", circle, creatorID, creatorDev.priv, self)
	if err != nil {
		t.Fatal(err)
	}
	invite := CircleInvite{Version: 2, Code: "code1", CircleID: circle.ID, CreatorID: creator.DeviceID, CreatorPK: creator.PublicKey}

	wrongDevice := fakeMember(t, otherDev.priv, func(m pairMessage) pairReply {
		return pairReply{OK: true, State: string(pairingJoined), SessionID: m.SessionID, Member: other, Welcome: &welcome}
	})
	if _, err := joinWithInviteToken(home, invite, wrongDevice, id, priv); err == nil || !strings.Contains(err.Error(), "did not issue") {
		t.Fatalf("an answer from a device that did not issue the invite must be refused: %v", err)
	}
	noWelcome := fakeMember(t, creatorDev.priv, func(m pairMessage) pairReply {
		return pairReply{OK: true, State: string(pairingJoined), SessionID: m.SessionID, Member: creator}
	})
	if _, err := joinWithInviteToken(home, invite, noWelcome, id, priv); err == nil || !strings.Contains(err.Error(), "did not issue") {
		t.Fatalf("an answer without a welcome must be refused: %v", err)
	}
	keySwap := creator
	keySwap.PublicKey = other.PublicKey
	swapped := fakeMember(t, creatorDev.priv, func(m pairMessage) pairReply {
		return pairReply{OK: true, State: string(pairingJoined), SessionID: m.SessionID, Member: keySwap, Welcome: &welcome}
	})
	if _, err := joinWithInviteToken(home, invite, swapped, id, priv); err == nil {
		t.Fatal("an answer under the creator's id with another key must be refused")
	}
}

func TestMemberRedeemsOnlyItsOwnTokenInvites(t *testing.T) {
	home := t.TempDir()
	setupTestEnv(t, home)
	id, priv, err := loadOrCreateIdentity(home)
	if err != nil {
		t.Fatal(err)
	}
	circle, err := createCircle(home, "team", id, priv)
	if err != nil {
		t.Fatal(err)
	}
	invite, err := createInvite(home, circle.ID, id)
	if err != nil {
		t.Fatal(err)
	}
	_, joiner, _ := pairingParty(t)
	ask := func(code string) pairReply {
		ts := nowRFC3339()
		m := pairMessage{Op: "invite", SessionID: "pi_1", Code: code, Joiner: joiner, Timestamp: ts}
		m.Proof = inviteProof(invite.Secret, m.SessionID, code, joiner, ts)
		return memberHandleInvite(home, id, priv, m)
	}
	rewrite := func(mutate func(*CircleInvite)) {
		stored, err := loadJoinInvite(home, invite.Code)
		if err != nil {
			t.Fatal(err)
		}
		mutate(&stored)
		raw, _ := json.Marshal(stored)
		if err := hookWriteFile(invitesDir(home)+"/"+invite.Code+".json", raw, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	rewrite(func(c *CircleInvite) { c.Version = 1 })
	if r := ask(invite.Code); r.OK {
		t.Fatal("an invite that carries the circle key is not redeemed as a token")
	}
	rewrite(func(c *CircleInvite) { c.Version = 2; c.CreatorID = "dev_0011223344556677" })
	if r := ask(invite.Code); r.OK {
		t.Fatal("an invite another device issued is not this device's to redeem")
	}
	rewrite(func(c *CircleInvite) { c.CreatorID = id.DeviceID })
	if r := ask(invite.Code); !r.OK || r.Welcome == nil {
		t.Fatalf("its own invite: %+v", r)
	}
}

func TestAWelcomeWithABrokenCircleDoesNotOpen(t *testing.T) {
	memberID, member, memberDev := pairingParty(t)
	_, joiner, joinerDev := pairingParty(t)
	_, admitted, err := buildPairingWelcome("ps_1", testCircle(memberID), memberID, memberDev.priv, joiner)
	if err != nil {
		t.Fatal(err)
	}
	for name, breakIt := range map[string]func(c *Circle){
		"short key":      func(c *Circle) { c.GroupKey = base64.StdEncoding.EncodeToString(make([]byte, 16)) },
		"unreadable key": func(c *Circle) { c.GroupKey = "not base64!" },
		"unsafe id":      func(c *Circle) { c.ID = "../circle" },
	} {
		broken := admitted
		breakIt(&broken)
		if _, err := openPairingWelcome("ps_1", sealedWelcome(t, "ps_1", broken, memberDev.priv, joiner), member, joinerDev.id, joinerDev.priv); err == nil {
			t.Fatalf("%s: a welcome with a broken circle must not open", name)
		}
	}
}
