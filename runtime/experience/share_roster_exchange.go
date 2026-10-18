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
	id, priv, err := loadOrCreateIdentity(home)
	if err != nil {
		return err
	}
	if _, err := updateCircle(home, circleID, func(circle Circle) (Circle, error) {
		if resp.DeviceID == circle.CreatorDeviceID && resp.CreatorDeviceID != "" {
			circle.CreatorDeviceID = resp.CreatorDeviceID
		}
		return vouchMembers(ingestRosterProofs(circle, resp.Members, resp.Admissions, resp.Revocations), id, priv), nil
	}); err != nil {
		return err
	}
	return mergePeerRoster(home, circleID, resp.Members)
}

var rosterRefresh = struct {
	sync.Mutex
	last map[string]time.Time
}{last: map[string]time.Time{}}

// refreshPeerRoster runs the membership exchanges with a peer this core just synced with, at most once a minute.
func refreshPeerRoster(home, endpoint string, circle Circle, id DeviceIdentity, priv ed25519.PrivateKey) {
	key := circle.ID + "|" + endpoint
	rosterRefresh.Lock()
	if time.Since(rosterRefresh.last[key]) < rosterRefreshEvery {
		rosterRefresh.Unlock()
		return
	}
	rosterRefresh.last[key] = time.Now()
	rosterRefresh.Unlock()
	exchangeMembership(home, endpoint, circle, id, priv)
}

// exchangeMembership pulls the peer's signed roster, then trades key-agreement records and key updates.
func exchangeMembership(home, endpoint string, circle Circle, id DeviceIdentity, priv ed25519.PrivateKey) {
	if resp, err := fetchPeerRoster(endpoint, circle, id, priv); err == nil {
		_ = applyPeerRoster(home, circle.ID, resp)
	}
	if current, err := loadCircle(home, circle.ID); err == nil {
		circle = current
	}
	if resp, err := fetchPeerKeys(endpoint, circle, id, priv); err == nil {
		_ = applyPeerKeys(home, circle.ID, resp)
	}
}

// membershipLoop keeps rosters and circle keys converging while the mesh is open, even when no memory sync
// runs, so a revocation or a new key reaches every member within a minute or two.
func membershipLoop(home string, epoch uint64) {
	ticker := time.NewTicker(rosterRefreshEvery)
	defer ticker.Stop()
	for meshEpochOpen(epoch) {
		if meshAllowsSync() {
			exchangeMembershipWithPeers(home)
		}
		<-ticker.C
	}
}

func exchangeMembershipWithPeers(home string) {
	id, priv, err := loadOrCreateIdentity(home)
	if err != nil {
		return
	}
	circles, err := listCircles(home)
	if err != nil {
		return
	}
	selfListen := advertisedListenAddr()
	for _, circle := range circles {
		if _, ok := circle.activeMember(id.DeviceID); !ok {
			continue
		}
		for _, endpoint := range nonEmptyEndpoints(circle.PeerEndpoints) {
			if endpoint != selfListen {
				exchangeMembership(home, endpoint, circle, id, priv)
			}
		}
	}
}
