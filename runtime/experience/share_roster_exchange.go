package main

import (
	"bufio"
	"crypto/ed25519"
	"encoding/json"
	"errors"
	"net"
	"strings"
	"sync"
	"time"
)

// The roster exchange carries membership proofs between cores. It is its own request kind so the sync and
// catalog responses keep the exact shape older cores verify. The request wraps a signed SyncRequest under
// "roster": an older core routes the unknown kind to its sync handler, finds no device id and stays silent.

const (
	rosterKind         = "roster"
	rosterMaxSkew      = 2 * time.Minute
	rosterRefreshEvery = time.Minute
)

var errUnknownRosterPeer = errors.New("roster from unknown or unverified peer")

type rosterRequest struct {
	Roster SyncRequest `json:"roster"`
}

type RosterResponse struct {
	DeviceID        string                      `json:"device_id"`
	CircleID        string                      `json:"circle_id"`
	CreatorDeviceID string                      `json:"creator_device_id"`
	Members         []CircleMember              `json:"members"`
	Admissions      map[string]MemberAdmission  `json:"admissions,omitempty"`
	Revocations     map[string]MemberRevocation `json:"revocations,omitempty"`
	Signature       string                      `json:"signature"`
}

func signRosterResponse(priv ed25519.PrivateKey, resp RosterResponse) (RosterResponse, error) {
	resp.Signature = ""
	payload, err := hookJSONMarshal(resp)
	if err != nil {
		return resp, err
	}
	resp.Signature = signPayload(priv, payload)
	return resp, nil
}

func verifyRosterResponse(pub ed25519.PublicKey, resp RosterResponse) bool {
	sig := resp.Signature
	resp.Signature = ""
	payload, err := hookJSONMarshal(resp)
	return err == nil && verifySignature(pub, payload, sig)
}

// rosterRequestAdmitted serves the roster only to an active member, signed and fresh: the response is sealed
// with the circle key, which a revoked device may still hold.
func rosterRequestAdmitted(circle Circle, req SyncRequest, now time.Time) bool {
	member, ok := circle.activeMember(req.DeviceID)
	if !ok || req.CircleID != circle.ID {
		return false
	}
	ts, err := time.Parse(time.RFC3339, req.Timestamp)
	if err != nil {
		return false
	}
	if skew := now.Sub(ts); skew > rosterMaxSkew || skew < -rosterMaxSkew {
		return false
	}
	pub, err := decodePublicKey(member.PublicKey)
	return err == nil && verifySyncRequest(pub, req)
}

func (sl *shareListener) handleRoster(conn net.Conn, circle Circle, priv ed25519.PrivateKey, plain []byte) {
	var wrapped rosterRequest
	if err := json.Unmarshal(plain, &wrapped); err != nil || !rosterRequestAdmitted(circle, wrapped.Roster, time.Now()) {
		return
	}
	resp, err := signRosterResponse(priv, RosterResponse{
		DeviceID: sl.id.DeviceID, CircleID: circle.ID, CreatorDeviceID: circle.CreatorDeviceID,
		Members: circle.Members, Admissions: circle.Admissions, Revocations: circle.Revocations,
	})
	if err != nil {
		return
	}
	payload, err := hookJSONMarshal(resp)
	if err != nil {
		return
	}
	cipher, err := encryptBatch(circle.GroupKey, payload)
	if err != nil {
		return
	}
	out, _ := hookJSONMarshal(wireEnvelope{CircleID: circle.ID, Ciphertext: cipher, Kind: rosterKind})
	_, _ = conn.Write(append(out, '\n'))
}

func fetchPeerRoster(endpoint string, circle Circle, id DeviceIdentity, priv ed25519.PrivateKey) (RosterResponse, error) {
	req, err := signSyncRequest(priv, SyncRequest{DeviceID: id.DeviceID, CircleID: circle.ID, Timestamp: nowRFC3339()})
	if err != nil {
		return RosterResponse{}, err
	}
	payload, err := hookJSONMarshal(rosterRequest{Roster: req})
	if err != nil {
		return RosterResponse{}, err
	}
	cipher, err := encryptBatch(circle.GroupKey, payload)
	if err != nil {
		return RosterResponse{}, err
	}
	conn, err := hookNetDial("tcp", endpoint, peerIDDialTimeout)
	if err != nil {
		return RosterResponse{}, err
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(peerIDDeadline))
	out, _ := hookJSONMarshal(wireEnvelope{CircleID: circle.ID, Ciphertext: cipher, Kind: rosterKind})
	if _, err := conn.Write(append(out, '\n')); err != nil {
		return RosterResponse{}, err
	}
	line, err := bufio.NewReader(conn).ReadString('\n')
	if err != nil {
		return RosterResponse{}, err
	}
	var env wireEnvelope
	if err := json.Unmarshal([]byte(strings.TrimSpace(line)), &env); err != nil {
		return RosterResponse{}, err
	}
	plain, err := decryptCircle(circle, env.Ciphertext)
	if err != nil {
		return RosterResponse{}, err
	}
	var resp RosterResponse
	if err := json.Unmarshal(plain, &resp); err != nil {
		return RosterResponse{}, err
	}
	member, ok := circle.activeMember(resp.DeviceID)
	if !ok || resp.CircleID != circle.ID {
		return RosterResponse{}, errUnknownRosterPeer
	}
	pub, err := decodePublicKey(member.PublicKey)
	if err != nil || !verifyRosterResponse(pub, resp) {
		return RosterResponse{}, errUnknownRosterPeer
	}
	return resp, nil
}

// applyPeerRoster takes a verified roster response. The device this core records as creator may name another
// creator: cores that joined through another member before invites carried the creator recorded that member.
func applyPeerRoster(home, circleID string, resp RosterResponse) error {
	circle, err := loadCircle(home, circleID)
	if err != nil {
		return err
	}
	if resp.DeviceID == circle.CreatorDeviceID && resp.CreatorDeviceID != "" {
		circle.CreatorDeviceID = resp.CreatorDeviceID
	}
	circle = ingestRosterProofs(circle, resp.Members, resp.Admissions, resp.Revocations)
	if err := persistJoinedCircle(home, circle); err != nil {
		return err
	}
	return mergePeerRoster(home, circleID, resp.Members)
}

var rosterRefresh = struct {
	sync.Mutex
	last map[string]time.Time
}{last: map[string]time.Time{}}

// refreshPeerRoster runs the roster exchange with a peer this core just synced with, at most once a minute.
func refreshPeerRoster(home, endpoint string, circle Circle, id DeviceIdentity, priv ed25519.PrivateKey) {
	key := circle.ID + "|" + endpoint
	rosterRefresh.Lock()
	if time.Since(rosterRefresh.last[key]) < rosterRefreshEvery {
		rosterRefresh.Unlock()
		return
	}
	rosterRefresh.last[key] = time.Now()
	rosterRefresh.Unlock()
	if resp, err := fetchPeerRoster(endpoint, circle, id, priv); err == nil {
		_ = applyPeerRoster(home, circle.ID, resp)
	}
}
