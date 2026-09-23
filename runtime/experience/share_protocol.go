package main

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/ed25519"
	"encoding/base64"
	"errors"
	"strings"
)

type SharedPacket struct {
	ID               string    `json:"id"`
	Kind             string    `json:"kind"`
	ScopeID          string    `json:"scope_id"`
	Subject          string    `json:"subject"`
	PacketContent    string    `json:"packet_content"`
	LessonContent    string    `json:"lesson_content,omitempty"`
	ProblemSignature string    `json:"problem_signature"`
	SourceFolder     string    `json:"source_folder"`
	EvidenceScore    float64   `json:"evidence_score"`
	Fingerprint      string    `json:"fingerprint"`
	Vector           []float32 `json:"vector,omitempty"`
	VectorSpace      string    `json:"vector_space"`
	UpdatedAt        string    `json:"updated_at"`
	HotIndex         bool      `json:"hot_index"`
}

type SyncRequest struct {
	DeviceID   string `json:"device_id"`
	CircleID   string `json:"circle_id"`
	Repository string `json:"repository"`
	Since      string `json:"since"`
	Timestamp  string `json:"timestamp"`
	Signature  string `json:"signature"`
}

type SyncResponse struct {
	DeviceID   string         `json:"device_id"`
	CircleID   string         `json:"circle_id"`
	Repository string         `json:"repository"`
	Cursor     string         `json:"cursor"`
	Packets    []SharedPacket `json:"packets"`
	Signature  string         `json:"signature"`
}

type wireEnvelope struct {
	CircleID   string `json:"circle_id"`
	Ciphertext string `json:"ciphertext"`
	Kind       string `json:"kind,omitempty"`
}

func encryptBatch(groupKeyB64 string, plaintext []byte) (string, error) {
	key, err := base64.StdEncoding.DecodeString(groupKeyB64)
	if err != nil {
		return "", err
	}
	if len(key) != 32 {
		return "", errors.New("invalid group key length")
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return "", err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := hookRandRead(nonce); err != nil {
		return "", err
	}
	sealed := gcm.Seal(nonce, nonce, plaintext, nil)
	return base64.StdEncoding.EncodeToString(sealed), nil
}

func decryptBatch(groupKeyB64 string, ciphertextB64 string) ([]byte, error) {
	key, err := base64.StdEncoding.DecodeString(groupKeyB64)
	if err != nil {
		return nil, err
	}
	sealed, err := base64.StdEncoding.DecodeString(ciphertextB64)
	if err != nil {
		return nil, err
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	if len(sealed) < gcm.NonceSize() {
		return nil, errors.New("ciphertext too short")
	}
	nonce := sealed[:gcm.NonceSize()]
	return gcm.Open(nil, nonce, sealed[gcm.NonceSize():], nil)
}

func signSyncRequest(priv ed25519.PrivateKey, req SyncRequest) (SyncRequest, error) {
	req.Signature = ""
	payload, err := hookJSONMarshal(req)
	if err != nil {
		return req, err
	}
	req.Signature = signPayload(priv, payload)
	return req, nil
}

func verifySyncRequest(pub ed25519.PublicKey, req SyncRequest) bool {
	sig := req.Signature
	req.Signature = ""
	payload, err := hookJSONMarshal(req)
	if err != nil {
		return false
	}
	return verifySignature(pub, payload, sig)
}

func signSyncResponse(priv ed25519.PrivateKey, resp SyncResponse) (SyncResponse, error) {
	resp.Signature = ""
	payload, err := hookJSONMarshal(resp)
	if err != nil {
		return resp, err
	}
	resp.Signature = signPayload(priv, payload)
	return resp, nil
}

func verifySyncResponse(pub ed25519.PublicKey, resp SyncResponse) bool {
	sig := resp.Signature
	resp.Signature = ""
	payload, err := hookJSONMarshal(resp)
	if err != nil {
		return false
	}
	return verifySignature(pub, payload, sig)
}

func memoryToSharedPacket(m Memory, folder string, hot bool) SharedPacket {
	packet := m.PacketContent
	if packet == "" {
		packet = buildPacketContent(m)
	}
	vecSpace := m.VectorSpace
	if vecSpace == "" {
		vecSpace = embedderName()
	}
	text := packet
	if text == "" {
		text = strings.Join([]string{m.Subject, m.Content, m.Evidence}, " ")
	}
	return SharedPacket{
		ID:               m.ID,
		Kind:             m.Kind,
		ScopeID:          m.ScopeID,
		Subject:          m.Subject,
		PacketContent:    packet,
		LessonContent:    m.Content,
		ProblemSignature: m.ProblemSignature,
		SourceFolder:     folder,
		EvidenceScore:    m.EvidenceScore,
		Fingerprint:      m.ID,
		Vector:           embedText(text),
		VectorSpace:      vecSpace,
		UpdatedAt:        m.UpdatedAt,
		HotIndex:         hot,
	}
}

func sharedPacketToMemory(p SharedPacket, circleID, peerDeviceID string) Memory {
	return Memory{
		ID:               p.Fingerprint,
		Kind:             p.Kind,
		Scope:            "repository",
		ScopeID:          p.ScopeID,
		Subject:          p.Subject,
		Content:          p.LessonContent,
		PacketContent:    p.PacketContent,
		ProblemSignature: p.ProblemSignature,
		SourceFolder:     p.SourceFolder,
		Confidence:       clamp(p.EvidenceScore, 0.3, 0.92),
		Priority:         45,
		Status:           "active",
		Source:           "peer_share",
		Evidence:         "",
		EvidenceScore:    p.EvidenceScore,
		CreatedAt:        p.UpdatedAt,
		UpdatedAt:        p.UpdatedAt,
		Origin:           "peer",
		PeerDeviceID:     peerDeviceID,
		CircleID:         circleID,
		Layer:            2,
		HotIndex:         p.HotIndex,
		VectorSpace:      p.VectorSpace,
	}
}
