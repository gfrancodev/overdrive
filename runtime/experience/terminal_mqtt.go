package main

import (
	"crypto/ed25519"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"time"
)

const terminalMQTTPayloadTTL = 10 * time.Minute

type terminalEnvelope struct {
	ProtocolVersion string          `json:"protocolVersion"`
	MessageID       string          `json:"messageId"`
	CircleID        string          `json:"circleId"`
	Kind            string          `json:"kind"`
	SessionID       string          `json:"sessionId,omitempty"`
	SenderDeviceID  string          `json:"senderDeviceId"`
	TargetDeviceID  string          `json:"targetDeviceId,omitempty"`
	Timestamp       string          `json:"timestamp"`
	ExpiresAt       string          `json:"expiresAt,omitempty"`
	Payload         json.RawMessage `json:"payload"`
	Signature       string          `json:"signature"`
}

type wireTerminalEnvelope struct {
	ProtocolVersion string `json:"protocolVersion"`
	MessageID       string `json:"messageId"`
	CircleID        string `json:"circleId"`
	Kind            string `json:"kind"`
	SessionID       string `json:"sessionId,omitempty"`
	SenderDeviceID  string `json:"senderDeviceId"`
	TargetDeviceID  string `json:"targetDeviceId,omitempty"`
	Timestamp       string `json:"timestamp"`
	ExpiresAt       string `json:"expiresAt,omitempty"`
	Payload         string `json:"payload"`
	Signature       string `json:"signature"`
}

var (
	terminalDedupMu sync.Mutex
	terminalDedup   = map[string]time.Time{}
)

func terminalPrefix(circleID string) string {
	return meshPrefix(circleID) + "terminal/"
}

func terminalInviteTopic(circleID, deviceID string) string {
	return terminalPrefix(circleID) + "invite/" + deviceID
}

func terminalRequestTopic(circleID, hostDeviceID string) string {
	return terminalPrefix(circleID) + "request/" + hostDeviceID
}

func terminalPermissionTopic(circleID, deviceID string) string {
	return terminalPrefix(circleID) + "permission/" + deviceID
}

func terminalStatusTopic(circleID, sessionID string) string {
	return terminalPrefix(circleID) + "status/" + sessionID
}

func terminalControlTopic(circleID, sessionID string) string {
	return terminalPrefix(circleID) + "control/" + sessionID
}

func terminalSignalTopic(circleID, deviceID string) string {
	return terminalPrefix(circleID) + "signal/" + deviceID
}

func signTerminalEnvelope(priv ed25519.PrivateKey, env terminalEnvelope) (terminalEnvelope, error) {
	env.Signature = ""
	payload, err := hookJSONMarshal(env)
	if err != nil {
		return env, err
	}
	env.Signature = signPayload(priv, payload)
	return env, nil
}

func verifyTerminalEnvelope(pub ed25519.PublicKey, env terminalEnvelope) bool {
	sig := env.Signature
	env.Signature = ""
	payload, err := hookJSONMarshal(env)
	if err != nil {
		return false
	}
	return verifySignature(pub, payload, sig)
}

func sealTerminalEnvelope(groupKey string, env terminalEnvelope) ([]byte, error) {
	cipher, err := encryptBatch(groupKey, env.Payload)
	if err != nil {
		return nil, err
	}
	wire := wireTerminalEnvelope{
		ProtocolVersion: env.ProtocolVersion, MessageID: env.MessageID, CircleID: env.CircleID,
		Kind: env.Kind, SessionID: env.SessionID, SenderDeviceID: env.SenderDeviceID,
		TargetDeviceID: env.TargetDeviceID, Timestamp: env.Timestamp, ExpiresAt: env.ExpiresAt,
		Payload: cipher, Signature: env.Signature,
	}
	return hookJSONMarshal(wire)
}

func openTerminalWire(groupKey string, body []byte) (terminalEnvelope, error) {
	var wire wireTerminalEnvelope
	if err := json.Unmarshal(body, &wire); err != nil {
		return terminalEnvelope{}, err
	}
	plain, err := decryptBatch(groupKey, wire.Payload)
	if err != nil {
		return terminalEnvelope{}, err
	}
	return terminalEnvelope{
		ProtocolVersion: wire.ProtocolVersion, MessageID: wire.MessageID, CircleID: wire.CircleID,
		Kind: wire.Kind, SessionID: wire.SessionID, SenderDeviceID: wire.SenderDeviceID,
		TargetDeviceID: wire.TargetDeviceID, Timestamp: wire.Timestamp, ExpiresAt: wire.ExpiresAt,
		Payload: json.RawMessage(plain), Signature: wire.Signature,
	}, nil
}

func terminalDedupMark(messageID string, ttl time.Duration) bool {
	if messageID == "" {
		return false
	}
	now := time.Now()
	terminalDedupMu.Lock()
	defer terminalDedupMu.Unlock()
	for id, exp := range terminalDedup {
		if now.After(exp) {
			delete(terminalDedup, id)
		}
	}
	if exp, ok := terminalDedup[messageID]; ok && now.Before(exp) {
		return false
	}
	terminalDedup[messageID] = now.Add(ttl)
	return true
}

func resetTerminalDedupForTests() {
	terminalDedupMu.Lock()
	terminalDedup = map[string]time.Time{}
	terminalDedupMu.Unlock()
}

func publishTerminalMQTT(home, circleID, topic, kind string, payload json.RawMessage) error {
	if !meshAllowsChat() {
		return nil
	}
	id, priv, err := loadOrCreateIdentity(home)
	if err != nil {
		return err
	}
	circle, err := loadCircle(home, circleID)
	if err != nil {
		return err
	}
	if _, ok := circle.activeMember(id.DeviceID); !ok {
		return errors.New("not a circle member")
	}
	if payload == nil {
		payload = json.RawMessage(`{}`)
	}
	env := terminalEnvelope{
		ProtocolVersion: terminalProtocolVersion,
		MessageID:       newMessageID(),
		CircleID:        circle.ID,
		Kind:            kind,
		SenderDeviceID:  id.DeviceID,
		Timestamp:       terminalNow(),
		ExpiresAt:       time.Now().Add(terminalMQTTPayloadTTL).UTC().Format(time.RFC3339),
		Payload:         payload,
	}
	if err := json.Unmarshal(payload, &struct {
		SessionID *string `json:"session_id"`
	}{}); err == nil {
		var peek struct {
			SessionID string `json:"session_id"`
		}
		_ = json.Unmarshal(payload, &peek)
		env.SessionID = peek.SessionID
	}
	signed, err := signTerminalEnvelope(priv, env)
	if err != nil {
		return err
	}
	wire, err := sealTerminalEnvelope(circle.GroupKey, signed)
	if err != nil {
		return err
	}
	return meshTrySend(circle.ID, topic, 1, wire)
}

func acceptTerminalWire(home string, body []byte) error {
	var peek struct {
		CircleID string `json:"circleId"`
	}
	if err := json.Unmarshal(body, &peek); err != nil {
		return err
	}
	circle, err := loadCircle(home, peek.CircleID)
	if err != nil {
		return err
	}
	env, err := openCircleTerminalWire(circle, body)
	if err != nil {
		return err
	}
	if env.ProtocolVersion != terminalProtocolVersion {
		return errors.New("unsupported terminal protocol")
	}
	if !terminalDedupMark(env.MessageID, terminalMQTTPayloadTTL) {
		return nil
	}
	if env.ExpiresAt != "" {
		if t, err := time.Parse(time.RFC3339, env.ExpiresAt); err == nil && time.Now().After(t) {
			return nil
		}
	}
	member, ok := circle.activeMember(env.SenderDeviceID)
	if !ok {
		return errors.New("sender is not a circle member")
	}
	pub, err := decodePublicKey(member.PublicKey)
	if err != nil {
		return err
	}
	if !verifyTerminalEnvelope(pub, env) {
		return errors.New("bad terminal signature")
	}
	return dispatchTerminalMQTT(home, circle, env)
}

func dispatchTerminalMQTT(home string, circle Circle, env terminalEnvelope) error {
	_ = strings.TrimSpace(env.Kind)
	// Etapa 5+ UI/tray may consume; core validates wire only in etapa 3.
	return nil
}
