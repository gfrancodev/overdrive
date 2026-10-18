package main

import (
	"crypto/ed25519"
	"crypto/hpke"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
)

// Pairing adds a nearby device to a circle without copying invites around. The joiner commits to a nonce, the
// member answers with its own, the joiner reveals; both screens then show a 6-digit code derived from the whole
// exchange. A device in the middle has to commit before it sees either nonce, so it gets one guess in a million
// at making both codes match. Once both people confirm the code, the member signs the joiner's admission and
// seals the circle key to the joiner's key-agreement key.

const (
	pairingCommitPurpose  = "overdrive-pair-commit-v1"
	pairingCodePurpose    = "overdrive-pair-code-v1"
	pairingWelcomePurpose = "overdrive-pair-welcome-v1"
)

var errPairingWelcome = errors.New("pairing welcome is not valid for this device")

// pairingPeer is what each side of a pairing presents about itself.
type pairingPeer struct {
	DeviceID     string `json:"device_id"`
	PublicKey    string `json:"public_key"`
	KexPublicKey string `json:"kex_public_key"`
	Hostname     string `json:"hostname,omitempty"`
	ListenAddr   string `json:"listen_addr,omitempty"`
}

func selfPairingPeer(id DeviceIdentity, priv ed25519.PrivateKey) (pairingPeer, error) {
	rec, err := selfKexRecord(id, priv)
	if err != nil {
		return pairingPeer{}, err
	}
	return pairingPeer{
		DeviceID: id.DeviceID, PublicKey: id.PublicKey, KexPublicKey: rec.PublicKey,
		Hostname: computerName(), ListenAddr: advertisedListenAddr(),
	}, nil
}

func pairingDigest(parts ...string) []byte {
	h := sha256.New()
	for _, p := range parts {
		var n [4]byte
		binary.BigEndian.PutUint32(n[:], uint32(len(p)))
		h.Write(n[:])
		h.Write([]byte(p))
	}
	return h.Sum(nil)
}

func pairingCommit(sessionID string, joiner pairingPeer, nonce []byte) string {
	return base64.StdEncoding.EncodeToString(pairingDigest(pairingCommitPurpose, sessionID,
		joiner.DeviceID, joiner.PublicKey, joiner.KexPublicKey, string(nonce)))
}

func pairingRevealMatches(sessionID string, joiner pairingPeer, commit string, nonce []byte) bool {
	return pairingCommit(sessionID, joiner, nonce) == commit
}

// pairingCode is the 6-digit number both people compare. It covers both identities, both key-agreement keys,
// the commitment and both nonces.
func pairingCode(sessionID string, joiner, member pairingPeer, commit string, nonceM, nonceJ []byte) string {
	sum := pairingDigest(pairingCodePurpose, sessionID,
		joiner.DeviceID, joiner.PublicKey, joiner.KexPublicKey,
		member.DeviceID, member.PublicKey, member.KexPublicKey,
		commit, string(nonceM), string(nonceJ))
	return fmt.Sprintf("%06d", binary.BigEndian.Uint32(sum[:4])%1000000)
}

type pairingWelcome struct {
	Sealed    string `json:"sealed"`
	Signature string `json:"signature"`
}

type pairingWelcomeBody struct {
	Circle Circle `json:"circle"`
}

func pairingWelcomeInfo(sessionID, joinerID string) []byte {
	return pairingDigest(pairingWelcomePurpose, sessionID, joinerID)
}

func pairingWelcomeSigned(sessionID, joinerID, sealed string) []byte {
	return pairingDigest(pairingWelcomePurpose, "signature", sessionID, joinerID, sealed)
}

// buildPairingWelcome admits the joiner on the member's side and seals what it needs to join: the circle with
// its current key and epoch, the signed roster, and its own admission. Folders stay out: each device authorizes
// its own. It returns the admitted circle so the member stores the same admission it sends.
func buildPairingWelcome(sessionID string, circle Circle, member DeviceIdentity, priv ed25519.PrivateKey, joiner pairingPeer) (pairingWelcome, Circle, error) {
	if !memberKeyBound(joiner.DeviceID, joiner.PublicKey) {
		return pairingWelcome{}, circle, errors.New("joiner key does not match its device id")
	}
	joined := CircleMember{DeviceID: joiner.DeviceID, PublicKey: joiner.PublicKey, Hostname: joiner.Hostname, Endpoint: joiner.ListenAddr}
	adm, err := signAdmission(priv, circle.ID, joined, member.DeviceID)
	if err != nil {
		return pairingWelcome{}, circle, err
	}
	admitted := refreshPeerEndpoints(circle.admit(joined, adm), member.DeviceID)
	gift := admitted
	gift.AllowedFolders, gift.PeerEndpoints, gift.PreviousKey, gift.KeyUpdate = nil, nil, nil, nil
	payload, err := json.Marshal(pairingWelcomeBody{Circle: gift})
	if err != nil {
		return pairingWelcome{}, circle, err
	}
	raw, err := base64.StdEncoding.DecodeString(joiner.KexPublicKey)
	if err != nil {
		return pairingWelcome{}, circle, err
	}
	pub, err := hpke.MLKEM768X25519().NewPublicKey(raw)
	if err != nil {
		return pairingWelcome{}, circle, err
	}
	sealed, err := hpke.Seal(pub, hpke.HKDFSHA256(), hpke.AES256GCM(), pairingWelcomeInfo(sessionID, joiner.DeviceID), payload)
	if err != nil {
		return pairingWelcome{}, circle, err
	}
	w := pairingWelcome{Sealed: base64.StdEncoding.EncodeToString(sealed)}
	w.Signature = base64.StdEncoding.EncodeToString(ed25519.Sign(priv, pairingWelcomeSigned(sessionID, joiner.DeviceID, w.Sealed)))
	return w, admitted, nil
}

// openPairingWelcome checks the welcome came from the member whose code the person confirmed, opens it with this
// device's key-agreement key, and checks the member admitted this device into a circle it belongs to.
func openPairingWelcome(sessionID string, w pairingWelcome, member pairingPeer, self DeviceIdentity, priv ed25519.PrivateKey) (pairingWelcomeBody, error) {
	if !memberKeyBound(member.DeviceID, member.PublicKey) {
		return pairingWelcomeBody{}, errPairingWelcome
	}
	pub, err := decodePublicKey(member.PublicKey)
	if err != nil {
		return pairingWelcomeBody{}, errPairingWelcome
	}
	sig, err := base64.StdEncoding.DecodeString(w.Signature)
	if err != nil || !ed25519.Verify(pub, pairingWelcomeSigned(sessionID, self.DeviceID, w.Sealed), sig) {
		return pairingWelcomeBody{}, errPairingWelcome
	}
	sealed, err := base64.StdEncoding.DecodeString(w.Sealed)
	if err != nil {
		return pairingWelcomeBody{}, errPairingWelcome
	}
	kex, err := deviceKexKey(priv)
	if err != nil {
		return pairingWelcomeBody{}, err
	}
	plain, err := hpke.Open(kex, hpke.HKDFSHA256(), hpke.AES256GCM(), pairingWelcomeInfo(sessionID, self.DeviceID), sealed)
	if err != nil {
		return pairingWelcomeBody{}, errPairingWelcome
	}
	var body pairingWelcomeBody
	if err := json.Unmarshal(plain, &body); err != nil {
		return pairingWelcomeBody{}, errPairingWelcome
	}
	c := body.Circle
	key, err := base64.StdEncoding.DecodeString(c.GroupKey)
	if err != nil || len(key) != 32 || !inviteCircleIDSafe(c.ID) {
		return pairingWelcomeBody{}, errPairingWelcome
	}
	if _, ok := c.activeMember(member.DeviceID); !ok {
		return pairingWelcomeBody{}, errPairingWelcome
	}
	adm, ok := c.Admissions[self.DeviceID]
	if !ok || adm.InvitedBy != member.DeviceID ||
		!verifyAdmission(c.ID, CircleMember{DeviceID: self.DeviceID, PublicKey: self.PublicKey}, adm, member.PublicKey) {
		return pairingWelcomeBody{}, errPairingWelcome
	}
	return body, nil
}
