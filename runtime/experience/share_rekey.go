package main

import (
	"crypto/ed25519"
	"crypto/hkdf"
	"crypto/hpke"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"time"
)

// Circle key rotation. Each device has a key-agreement key (HPKE, ML-KEM-768 + X25519) derived from its
// Ed25519 identity, so nothing new is stored and every process derives the same key. The creator hands a new
// circle key to each remaining member sealed to that member's key-agreement key.

const (
	kexDerivationInfo = "overdrive-device-kex-v1"
	kexRecordPurpose  = "overdrive-device-kex-record-v1"
)

// DeviceKexRecord publishes a device's key-agreement public key, signed by the device's Ed25519 key.
type DeviceKexRecord struct {
	DeviceID  string `json:"device_id"`
	PublicKey string `json:"kex_public_key"`
	Signature string `json:"signature"`
}

type kexRecordPayload struct {
	Purpose   string `json:"purpose"`
	DeviceID  string `json:"device_id"`
	PublicKey string `json:"kex_public_key"`
}

func deviceKexKey(priv ed25519.PrivateKey) (hpke.PrivateKey, error) {
	ikm, err := hkdf.Key(sha256.New, priv.Seed(), nil, kexDerivationInfo, 64)
	if err != nil {
		return nil, err
	}
	return hpke.MLKEM768X25519().DeriveKeyPair(ikm)
}

func selfKexRecord(id DeviceIdentity, priv ed25519.PrivateKey) (DeviceKexRecord, error) {
	kex, err := deviceKexKey(priv)
	if err != nil {
		return DeviceKexRecord{}, err
	}
	rec := DeviceKexRecord{DeviceID: id.DeviceID, PublicKey: base64.StdEncoding.EncodeToString(kex.PublicKey().Bytes())}
	payload, err := hookJSONMarshal(kexRecordPayload{Purpose: kexRecordPurpose, DeviceID: rec.DeviceID, PublicKey: rec.PublicKey})
	if err != nil {
		return DeviceKexRecord{}, err
	}
	rec.Signature = signPayload(priv, payload)
	return rec, nil
}

func verifyKexRecord(rec DeviceKexRecord, deviceKey string) bool {
	pub, err := decodePublicKey(deviceKey)
	if err != nil {
		return false
	}
	payload, err := hookJSONMarshal(kexRecordPayload{Purpose: kexRecordPurpose, DeviceID: rec.DeviceID, PublicKey: rec.PublicKey})
	return err == nil && verifySignature(pub, payload, rec.Signature)
}

const (
	keyUpdatePurpose = "overdrive-circle-key-update-v1"
	keyEntryInfo     = "overdrive-circle-key-v1"
	previousKeyGrace = 24 * time.Hour
)

var errNoKexKey = errors.New("member has no key-agreement key")

// CirclePreviousKey keeps the key a rotation replaced, so messages sealed just before it still open.
type CirclePreviousKey struct {
	Epoch     int    `json:"epoch"`
	Key       string `json:"key"`
	ExpiresAt string `json:"expires_at"`
}

// CircleKeyUpdate hands a new circle key to each member, sealed to that member's key-agreement key and
// signed by the creator. HPKE info binds circle, epoch and device, so an entry opens only where it was meant to.
type CircleKeyUpdate struct {
	Purpose   string            `json:"purpose"`
	CircleID  string            `json:"circle_id"`
	Epoch     int               `json:"epoch"`
	CreatedAt string            `json:"created_at"`
	Entries   map[string]string `json:"entries"`
	Signature string            `json:"signature"`
}

func signKeyUpdate(priv ed25519.PrivateKey, u CircleKeyUpdate) (CircleKeyUpdate, error) {
	u.Purpose, u.Signature = keyUpdatePurpose, ""
	payload, err := hookJSONMarshal(u)
	if err != nil {
		return CircleKeyUpdate{}, err
	}
	u.Signature = signPayload(priv, payload)
	return u, nil
}

func verifyKeyUpdate(u CircleKeyUpdate, creatorKey string) bool {
	pub, err := decodePublicKey(creatorKey)
	if err != nil || u.Purpose != keyUpdatePurpose {
		return false
	}
	sig := u.Signature
	u.Signature = ""
	payload, err := hookJSONMarshal(u)
	return err == nil && verifySignature(pub, payload, sig)
}

func keyEntryInfoFor(circleID string, epoch int, deviceID string) []byte {
	return []byte(fmt.Sprintf("%s\x00%s\x00%d\x00%s", keyEntryInfo, circleID, epoch, deviceID))
}

// ingestKexRecords keeps the signed key-agreement keys of devices on this roster. A derived key never changes,
// so the first verified record for a device stays.
func (c Circle) ingestKexRecords(records []DeviceKexRecord) Circle {
	for _, rec := range records {
		m, ok := c.memberRecord(rec.DeviceID)
		if !ok || !verifyKexRecord(rec, m.PublicKey) {
			continue
		}
		if _, has := c.KexKeys[rec.DeviceID]; has {
			continue
		}
		if c.KexKeys == nil {
			c.KexKeys = map[string]DeviceKexRecord{}
		}
		c.KexKeys[rec.DeviceID] = rec
	}
	return c
}

// rekeyCircle replaces the circle key and returns the members left without it: those whose key-agreement key
// this core has not seen, usually because their core predates key rotation.
func rekeyCircle(circle Circle, self DeviceIdentity, priv ed25519.PrivateKey) (Circle, []string, error) {
	if circle.CreatorDeviceID != self.DeviceID {
		return circle, nil, errors.New("only the circle creator can rotate its key")
	}
	key := make([]byte, 32)
	if _, err := hookRandRead(key); err != nil {
		return circle, nil, err
	}
	epoch := circle.KeyEpoch + 1
	update := CircleKeyUpdate{CircleID: circle.ID, Epoch: epoch, CreatedAt: nowRFC3339(), Entries: map[string]string{}}
	var cutOff []string
	for _, m := range circle.Members {
		if m.Revoked || m.DeviceID == self.DeviceID {
			continue
		}
		sealed, err := sealKeyEntry(circle, epoch, m.DeviceID, key)
		if err != nil {
			cutOff = append(cutOff, m.DeviceID)
			continue
		}
		update.Entries[m.DeviceID] = sealed
	}
	update, err := signKeyUpdate(priv, update)
	if err != nil {
		return circle, nil, err
	}
	circle = circle.moveToKey(epoch, base64.StdEncoding.EncodeToString(key))
	circle.KeyUpdate = &update
	return circle, cutOff, nil
}

func sealKeyEntry(circle Circle, epoch int, deviceID string, key []byte) (string, error) {
	rec, ok := circle.KexKeys[deviceID]
	if !ok {
		return "", errNoKexKey
	}
	raw, err := base64.StdEncoding.DecodeString(rec.PublicKey)
	if err != nil {
		return "", err
	}
	pub, err := hpke.MLKEM768X25519().NewPublicKey(raw)
	if err != nil {
		return "", err
	}
	sealed, err := hpke.Seal(pub, hpke.HKDFSHA256(), hpke.AES256GCM(), keyEntryInfoFor(circle.ID, epoch, deviceID), key)
	if err != nil {
		return "", err
	}
	return base64.StdEncoding.EncodeToString(sealed), nil
}

func (c Circle) moveToKey(epoch int, key string) Circle {
	c.PreviousKey = &CirclePreviousKey{
		Epoch: c.KeyEpoch, Key: c.GroupKey,
		ExpiresAt: time.Now().Add(previousKeyGrace).UTC().Format(time.RFC3339),
	}
	c.GroupKey, c.KeyEpoch = key, epoch
	return c
}

// adoptKeyUpdate moves this device to a newer key the creator signed, if the update holds an entry for it.
func (c Circle) adoptKeyUpdate(u CircleKeyUpdate, self DeviceIdentity, priv ed25519.PrivateKey) (Circle, bool) {
	if u.CircleID != c.ID || u.Epoch <= c.KeyEpoch {
		return c, false
	}
	creator, ok := c.memberRecord(c.CreatorDeviceID)
	if !ok || !verifyKeyUpdate(u, creator.PublicKey) {
		return c, false
	}
	entry, ok := u.Entries[self.DeviceID]
	if !ok {
		return c, false
	}
	sealed, err := base64.StdEncoding.DecodeString(entry)
	if err != nil {
		return c, false
	}
	kex, err := deviceKexKey(priv)
	if err != nil {
		return c, false
	}
	key, err := hpke.Open(kex, hpke.HKDFSHA256(), hpke.AES256GCM(), keyEntryInfoFor(c.ID, u.Epoch, self.DeviceID), sealed)
	if err != nil || len(key) != 32 {
		return c, false
	}
	c = c.moveToKey(u.Epoch, base64.StdEncoding.EncodeToString(key))
	c.KeyUpdate = &u
	return c, true
}

// openKeys lists the keys a delivery may be sealed with: the current one, and the previous one until the
// grace window after a rotation ends. Senders always seal with the current key.
func (c Circle) openKeys(now time.Time) []string {
	keys := []string{c.GroupKey}
	if p := c.PreviousKey; p != nil && p.Key != "" {
		if exp, err := time.Parse(time.RFC3339, p.ExpiresAt); err == nil && now.Before(exp) {
			keys = append(keys, p.Key)
		}
	}
	return keys
}

func decryptCircle(c Circle, ciphertext string) ([]byte, error) {
	var lastErr error
	for _, key := range c.openKeys(time.Now()) {
		plain, err := decryptBatch(key, ciphertext)
		if err == nil {
			return plain, nil
		}
		lastErr = err
	}
	return nil, lastErr
}
