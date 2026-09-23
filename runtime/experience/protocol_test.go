package main

import (
	"crypto/ed25519"
	"encoding/base64"
	"os"
	"testing"
)

func TestEncryptDecryptAndSignatures(t *testing.T) {
	key := make([]byte, 32)
	for i := range key {
		key[i] = byte(i)
	}
	groupKey := base64.StdEncoding.EncodeToString(key)
	plain := []byte(`{"hello":"world"}`)
	cipher, err := encryptBatch(groupKey, plain)
	if err != nil {
		t.Fatal(err)
	}
	out, err := decryptBatch(groupKey, cipher)
	if err != nil || string(out) != string(plain) {
		t.Fatal("roundtrip")
	}
	if _, err := decryptBatch(groupKey, "short"); err == nil {
		t.Fatal("short ciphertext")
	}
	if _, err := encryptBatch("bad", plain); err == nil {
		t.Fatal("bad group key")
	}

	pub, priv, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	req := SyncRequest{DeviceID: "dev_a", CircleID: "circle_x", Repository: "github.com/acme/r", Timestamp: "2026-01-01T00:00:00Z"}
	signed, err := signSyncRequest(priv, req)
	if err != nil || signed.Signature == "" {
		t.Fatal("sign request")
	}
	if !verifySyncRequest(pub, signed) {
		t.Fatal("verify request")
	}
	resp := SyncResponse{DeviceID: "dev_b", CircleID: "circle_x", Repository: "github.com/acme/r", Cursor: "2026-01-02T00:00:00Z"}
	signedResp, err := signSyncResponse(priv, resp)
	if err != nil || !verifySyncResponse(pub, signedResp) {
		t.Fatal("sign response")
	}
}

func TestIdentityLoadErrors(t *testing.T) {
	home := t.TempDir()
	if err := os.MkdirAll(shareDir(home), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(identityPath(home), []byte("{"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := loadOrCreateIdentity(home); err == nil {
		t.Fatal("parse error expected")
	}
}

func TestDecodePublicKeyInvalid(t *testing.T) {
	if _, err := decodePublicKey("!!!"); err == nil {
		t.Fatal("invalid b64")
	}
	if _, err := decodePublicKey(base64.StdEncoding.EncodeToString([]byte{1, 2})); err == nil {
		t.Fatal("invalid size")
	}
}
