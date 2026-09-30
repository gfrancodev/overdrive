package main

import (
	"bufio"
	"crypto/ed25519"
	"encoding/json"
	"net"
	"strings"
	"time"
)

type JoinRequest struct {
	CircleID   string `json:"circle_id"`
	DeviceID   string `json:"device_id"`
	PublicKey  string `json:"public_key"`
	Code       string `json:"code"`
	Timestamp  string `json:"timestamp"`
	ListenAddr string `json:"listen_addr,omitempty"`
	Hostname   string `json:"hostname,omitempty"`
	Signature  string `json:"signature"`
}

type joinAck struct {
	CircleID string         `json:"circle_id"`
	Members  []CircleMember `json:"members"`
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
	_, err := exchangeJoin("", invite, endpoint, id, priv)
	return err
}

func exchangeJoin(home string, invite CircleInvite, endpoint string, id DeviceIdentity, priv ed25519.PrivateKey) (joinAck, error) {
	if strings.TrimSpace(endpoint) == "" {
		return joinAck{}, nil
	}
	host := computerName()
	listen := advertisedListenAddr()
	if listen == strings.TrimSpace(endpoint) {
		listen = ""
	}
	req := JoinRequest{
		CircleID:   invite.CircleID,
		DeviceID:   id.DeviceID,
		PublicKey:  id.PublicKey,
		Code:       invite.Code,
		Timestamp:  nowRFC3339(),
		ListenAddr: listen,
		Hostname:   host,
	}
	req, err := signJoinRequest(priv, req)
	if err != nil {
		return joinAck{}, err
	}
	payload, err := hookJSONMarshal(req)
	if err != nil {
		return joinAck{}, err
	}
	cipher, err := encryptBatch(invite.GroupKey, payload)
	if err != nil {
		return joinAck{}, err
	}
	conn, err := hookNetDial("tcp", endpoint, 5*time.Second)
	if err != nil {
		return joinAck{}, err
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(10 * time.Second))
	out, _ := hookJSONMarshal(wireEnvelope{CircleID: invite.CircleID, Ciphertext: cipher, Kind: "join"})
	if _, err := conn.Write(append(out, '\n')); err != nil {
		return joinAck{}, err
	}
	reader := bufio.NewReader(conn)
	line, err := reader.ReadString('\n')
	if err != nil {
		return joinAck{}, err
	}
	var env wireEnvelope
	if err := json.Unmarshal([]byte(strings.TrimSpace(line)), &env); err != nil {
		return joinAck{}, err
	}
	plain, err := decryptBatch(invite.GroupKey, env.Ciphertext)
	if err != nil {
		return joinAck{}, err
	}
	var ack joinAck
	if err := json.Unmarshal(plain, &ack); err != nil {
		return joinAck{}, err
	}
	if home != "" && len(ack.Members) > 0 {
		_ = mergePeerRoster(home, invite.CircleID, ack.Members)
	}
	return ack, nil
}

func (sl *shareListener) tryHandleJoin(env wireEnvelope, plain []byte) bool {
	return sl.applyJoin(nil, env, plain)
}

func (sl *shareListener) applyJoin(conn net.Conn, env wireEnvelope, plain []byte) bool {
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
	var remote net.Addr
	if conn != nil {
		remote = conn.RemoteAddr()
	}
	circle = rememberJoin(circle, req, remote)
	circle = stampSelfMember(circle, sl.id.DeviceID, sl.selfEndpoint())
	circle = refreshPeerEndpoints(circle, sl.id.DeviceID)
	if err := persistJoinedCircle(sl.home, circle); err != nil {
		return true
	}
	if conn != nil {
		_ = writeJoinAck(conn, circle)
	}
	return true
}
