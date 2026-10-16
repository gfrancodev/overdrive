package main

import (
	"crypto/ed25519"
	"encoding/json"
	"testing"
	"time"
)

func TestTerminalMQTTEnvelopeSignVerify(t *testing.T) {
	_, priv, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	pub := priv.Public().(ed25519.PublicKey)
	env := terminalEnvelope{
		ProtocolVersion: terminalProtocolVersion,
		MessageID:       newMessageID(),
		CircleID:        "circle_test",
		Kind:            "status",
		SessionID:       "ts_abc",
		SenderDeviceID:  "dev_sender",
		Timestamp:       terminalNow(),
		ExpiresAt:       time.Now().Add(5 * time.Minute).UTC().Format(time.RFC3339),
		Payload:         json.RawMessage(`{"state":"RUNNING"}`),
	}
	signed, err := signTerminalEnvelope(priv, env)
	if err != nil {
		t.Fatal(err)
	}
	if !verifyTerminalEnvelope(pub, signed) {
		t.Fatal("signature invalid")
	}
}

func TestTerminalMQTTDedupTTL(t *testing.T) {
	resetTerminalDedupForTests()
	id := "msg_dedup_1"
	if !terminalDedupMark(id, time.Minute) {
		t.Fatal("first mark should succeed")
	}
	if terminalDedupMark(id, time.Minute) {
		t.Fatal("duplicate should be rejected")
	}
}

func TestTerminalMQTTTopics(t *testing.T) {
	circle := "mesh_01"
	if terminalInviteTopic(circle, "dev_x") != "overdrive/v1/mesh_01/terminal/invite/dev_x" {
		t.Fatal("invite topic")
	}
	if terminalControlTopic(circle, "ts_1") != "overdrive/v1/mesh_01/terminal/control/ts_1" {
		t.Fatal("control topic")
	}
}
