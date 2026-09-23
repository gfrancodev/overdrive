package main

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"time"
)

type DeviceIdentity struct {
	DeviceID   string `json:"device_id"`
	PublicKey  string `json:"public_key"`
	CreatedAt  string `json:"created_at"`
	PrivateKey string `json:"private_key"`
}

func shareDir(home string) string {
	return filepath.Join(home, "share")
}

func identityPath(home string) string {
	return filepath.Join(shareDir(home), "identity.json")
}

func loadOrCreateIdentity(home string) (DeviceIdentity, ed25519.PrivateKey, error) {
	if err := hookMkdirAll(shareDir(home), 0o700); err != nil {
		return DeviceIdentity{}, nil, err
	}
	path := identityPath(home)
	if data, err := hookReadFile(path); err == nil {
		var id DeviceIdentity
		if err := json.Unmarshal(data, &id); err != nil {
			return DeviceIdentity{}, nil, fmt.Errorf("parse identity: %w", err)
		}
		privBytes, err := base64.StdEncoding.DecodeString(id.PrivateKey)
		if err != nil {
			return DeviceIdentity{}, nil, err
		}
		if len(privBytes) != ed25519.PrivateKeySize {
			return DeviceIdentity{}, nil, errors.New("invalid private key size")
		}
		priv := ed25519.PrivateKey(privBytes)
		return id, priv, nil
	}
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return DeviceIdentity{}, nil, err
	}
	sum := sha256Hex(pub)
	id := DeviceIdentity{
		DeviceID:   "dev_" + sum[:16],
		PublicKey:  base64.StdEncoding.EncodeToString(pub),
		PrivateKey: base64.StdEncoding.EncodeToString(priv),
		CreatedAt:  nowRFC3339(),
	}
	data, err := hookJSONMarshalIndent(id, "", "  ")
	if err != nil {
		return DeviceIdentity{}, nil, err
	}
	if err := hookWriteFile(path, data, 0o600); err != nil {
		return DeviceIdentity{}, nil, err
	}
	return id, priv, nil
}

func decodePublicKey(b64 string) (ed25519.PublicKey, error) {
	raw, err := base64.StdEncoding.DecodeString(b64)
	if err != nil {
		return nil, err
	}
	if len(raw) != ed25519.PublicKeySize {
		return nil, errors.New("invalid public key size")
	}
	return ed25519.PublicKey(raw), nil
}

func signPayload(priv ed25519.PrivateKey, payload []byte) string {
	sig := ed25519.Sign(priv, payload)
	return base64.StdEncoding.EncodeToString(sig)
}

func verifySignature(pub ed25519.PublicKey, payload []byte, sigB64 string) bool {
	sig, err := base64.StdEncoding.DecodeString(sigB64)
	if err != nil {
		return false
	}
	return ed25519.Verify(pub, payload, sig)
}

func sha256Hex(b []byte) string {
	sum := sha256.Sum256(b)
	return fmt.Sprintf("%x", sum)
}

func inviteTTL() time.Duration {
	return 15 * time.Minute
}
