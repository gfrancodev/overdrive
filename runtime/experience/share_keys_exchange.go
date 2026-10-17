package main

import (
	"bufio"
	"crypto/ed25519"
	"encoding/json"
	"errors"
	"net"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// The keys exchange moves key-agreement records and the creator's latest key update between cores. A device
// that missed a rotation shares no circle key with anyone any more, so this exchange travels in the clear: it
// holds only public keys, entries sealed to one device each, and signatures. Older cores try to decrypt the
// empty ciphertext, fail, and stay silent.

const keysKind = "keys"

var errUnknownKeysPeer = errors.New("keys from unknown or unverified peer")

type keysRequest struct {
	Keys   SyncRequest      `json:"keys"`
	Kex    DeviceKexRecord  `json:"kex"`
	Update *CircleKeyUpdate `json:"update,omitempty"`
}

type KeysResponse struct {
	DeviceID  string            `json:"device_id"`
	CircleID  string            `json:"circle_id"`
	Kex       []DeviceKexRecord `json:"kex"`
	Update    *CircleKeyUpdate  `json:"update,omitempty"`
	Signature string            `json:"signature"`
}

func signKeysResponse(priv ed25519.PrivateKey, resp KeysResponse) (KeysResponse, error) {
	resp.Signature = ""
	payload, err := hookJSONMarshal(resp)
	if err != nil {
		return resp, err
	}
	resp.Signature = signPayload(priv, payload)
	return resp, nil
}

func verifyKeysResponse(pub ed25519.PublicKey, resp KeysResponse) bool {
	sig := resp.Signature
	resp.Signature = ""
	payload, err := hookJSONMarshal(resp)
	return err == nil && verifySignature(pub, payload, sig)
}

func (sl *shareListener) handleKeys(conn net.Conn, circle Circle, priv ed25519.PrivateKey, plain string) {
	var req keysRequest
	if err := json.Unmarshal([]byte(plain), &req); err != nil || !rosterRequestAdmitted(circle, req.Keys, time.Now()) {
		return
	}
	if err := applyKeysMaterial(sl.home, circle.ID, []DeviceKexRecord{req.Kex}, req.Update); err != nil {
		return
	}
	current, err := loadCircle(sl.home, circle.ID)
	if err != nil {
		return
	}
	self, err := selfKexRecord(sl.id, priv)
	if err != nil {
		return
	}
	records := []DeviceKexRecord{self}
	for _, rec := range current.KexKeys {
		if rec.DeviceID != sl.id.DeviceID {
			records = append(records, rec)
		}
	}
	resp, err := signKeysResponse(priv, KeysResponse{
		DeviceID: sl.id.DeviceID, CircleID: current.ID, Kex: records, Update: current.KeyUpdate,
	})
	if err != nil {
		return
	}
	payload, err := hookJSONMarshal(resp)
	if err != nil {
		return
	}
	out, _ := hookJSONMarshal(wireEnvelope{CircleID: current.ID, Kind: keysKind, Plain: string(payload)})
	_, _ = conn.Write(append(out, '\n'))
}

func fetchPeerKeys(endpoint string, circle Circle, id DeviceIdentity, priv ed25519.PrivateKey) (KeysResponse, error) {
	req, err := signSyncRequest(priv, SyncRequest{DeviceID: id.DeviceID, CircleID: circle.ID, Timestamp: nowRFC3339()})
	if err != nil {
		return KeysResponse{}, err
	}
	self, err := selfKexRecord(id, priv)
	if err != nil {
		return KeysResponse{}, err
	}
	payload, err := hookJSONMarshal(keysRequest{Keys: req, Kex: self, Update: circle.KeyUpdate})
	if err != nil {
		return KeysResponse{}, err
	}
	conn, err := hookNetDial("tcp", endpoint, peerIDDialTimeout)
	if err != nil {
		return KeysResponse{}, err
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(peerIDDeadline))
	out, _ := hookJSONMarshal(wireEnvelope{CircleID: circle.ID, Kind: keysKind, Plain: string(payload)})
	if _, err := conn.Write(append(out, '\n')); err != nil {
		return KeysResponse{}, err
	}
	line, err := bufio.NewReader(conn).ReadString('\n')
	if err != nil {
		return KeysResponse{}, err
	}
	var env wireEnvelope
	if err := json.Unmarshal([]byte(strings.TrimSpace(line)), &env); err != nil {
		return KeysResponse{}, err
	}
	var resp KeysResponse
	if err := json.Unmarshal([]byte(env.Plain), &resp); err != nil {
		return KeysResponse{}, err
	}
	member, ok := circle.activeMember(resp.DeviceID)
	if !ok || resp.CircleID != circle.ID {
		return KeysResponse{}, errUnknownKeysPeer
	}
	pub, err := decodePublicKey(member.PublicKey)
	if err != nil || !verifyKeysResponse(pub, resp) {
		return KeysResponse{}, errUnknownKeysPeer
	}
	return resp, nil
}

func applyPeerKeys(home, circleID string, resp KeysResponse) error {
	return applyKeysMaterial(home, circleID, resp.Kex, resp.Update)
}

// applyKeysMaterial stores the key-agreement records of known members and moves this device to a newer key
// the creator handed it.
func applyKeysMaterial(home, circleID string, records []DeviceKexRecord, update *CircleKeyUpdate) error {
	circle, err := loadCircle(home, circleID)
	if err != nil {
		return err
	}
	id, priv, err := loadOrCreateIdentity(home)
	if err != nil {
		return err
	}
	circle = circle.ingestKexRecords(records)
	adopted := false
	if update != nil {
		circle, adopted = circle.adoptKeyUpdate(*update, id, priv)
	}
	if err := persistJoinedCircle(home, circle); err != nil {
		return err
	}
	if adopted {
		afterKeyRotation(home, circle)
	}
	return nil
}

// afterKeyRotation runs once this core holds a new circle key: invites it issued carry the old key and are
// dropped, and chat still queued for the mesh is sealed again with the new key.
func afterKeyRotation(home string, circle Circle) {
	dropCircleInvites(home, circle.ID)
	if circle.PreviousKey != nil {
		_ = resealOutbox(home, circle.ID, circle.PreviousKey.Key, circle.GroupKey)
	}
}

func dropCircleInvites(home, circleID string) {
	entries, err := hookReadDir(invitesDir(home))
	if err != nil {
		return
	}
	for _, e := range entries {
		path := filepath.Join(invitesDir(home), e.Name())
		raw, err := hookReadFile(path)
		if err != nil {
			continue
		}
		var invite CircleInvite
		if json.Unmarshal(raw, &invite) == nil && invite.CircleID == circleID {
			_ = os.Remove(path)
		}
	}
}

func resealOutbox(home, circleID, oldKey, newKey string) error {
	items, err := loadOutbox(home, circleID)
	if err != nil {
		return err
	}
	db, err := openChatDB(home)
	if err != nil {
		return err
	}
	for _, item := range items {
		env, err := openChatWire(oldKey, item.Wire)
		if err != nil {
			continue
		}
		wire, err := sealChatEnvelope(newKey, env)
		if err != nil {
			continue
		}
		chatMu.Lock()
		_, err = db.Exec(`UPDATE outbox SET wire = ? WHERE message_id = ?`, string(wire), item.MessageID)
		chatMu.Unlock()
		if err != nil {
			return err
		}
	}
	return nil
}
