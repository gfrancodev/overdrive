package main

import (
	"crypto/ed25519"
	"strings"
	"time"
)

type JoinRequest struct {
	CircleID  string `json:"circle_id"`
	DeviceID  string `json:"device_id"`
	PublicKey string `json:"public_key"`
	Code      string `json:"code"`
	Timestamp string `json:"timestamp"`
	Signature string `json:"signature"`
}

func signJoinRequest(priv ed25519.PrivateKey, req JoinRequest) (JoinRequest, error) {
	req.Signature = ""
	payload, err := hookJSONMarshal(req)
	if err != nil {
		return req, err
	}
	req.Signature = signPayload(priv, payload)
	return req, nil
}

func verifyJoinRequest(pub ed25519.PublicKey, req JoinRequest) bool {
	sig := req.Signature
	req.Signature = ""
	payload, err := hookJSONMarshal(req)
	if err != nil {
		return false
	}
	return verifySignature(pub, payload, sig)
}

func notifyPeerJoin(invite CircleInvite, endpoint string, id DeviceIdentity, priv ed25519.PrivateKey) error {
	if strings.TrimSpace(endpoint) == "" {
		return nil
	}
	req := JoinRequest{
		CircleID:  invite.CircleID,
		DeviceID:  id.DeviceID,
		PublicKey: id.PublicKey,
		Code:      invite.Code,
		Timestamp: nowRFC3339(),
	}
	req, err := signJoinRequest(priv, req)
	if err != nil {
		return err
	}
	payload, err := hookJSONMarshal(req)
	if err != nil {
		return err
	}
	cipher, err := encryptBatch(invite.GroupKey, payload)
	if err != nil {
		return err
	}
	conn, err := hookNetDial("tcp", endpoint, 5*time.Second)
	if err != nil {
		return err
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(10 * time.Second))
	out, _ := hookJSONMarshal(wireEnvelope{CircleID: invite.CircleID, Ciphertext: cipher, Kind: "join"})
	_, err = conn.Write(append(out, '\n'))
	return err
}

func (sl *shareListener) tryHandleJoin(env wireEnvelope, plain []byte) bool {
	if env.Kind != "join" {
		return false
	}
	req, err := parseJoinRequest(plain)
	if err != nil {
		return true
	}
	circle, err := loadCircle(sl.home, req.CircleID)
	if err != nil {
		return true
	}
	invite, err := loadJoinInvite(sl.home, req.Code)
	if err != nil {
		return true
	}
	if !joinInviteValid(invite, time.Now()) {
		return true
	}
	pub, err := decodePublicKey(req.PublicKey)
	if err != nil || !verifyJoinRequest(pub, req) {
		return true
	}
	circle = circleWithJoinMember(circle, req)
	_ = persistJoinedCircle(sl.home, circle)
	return true
}
