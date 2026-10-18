package main

import (
	"bufio"
	"crypto/ed25519"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Pairing sessions and their transport. Both cores keep their side of a session in memory; nothing about an
// unfinished pairing is written to disk. The joiner talks to the member's share listener with kind "pair" in
// the clear, since it has no circle key yet: every joiner message is signed and fresh, every member reply is
// signed, and nothing joins a circle until both people confirm the same code.

const (
	pairKind           = "pair"
	pairingSessionTTL  = 2 * time.Minute
	maxPairingSessions = 8
)

var pairingPollEvery = time.Second

type pairingState string

const (
	pairingConnecting pairingState = "connecting"
	pairingComparing  pairingState = "comparing"
	pairingWaiting    pairingState = "waiting"
	pairingJoined     pairingState = "joined"
	pairingRejected   pairingState = "rejected"
	pairingExpired    pairingState = "expired"
	pairingFailed     pairingState = "failed"
)

type pairingSession struct {
	home      string
	ID        string
	Role      string
	State     pairingState
	Code      string
	Peer      pairingPeer
	Endpoint  string
	CircleID  string
	Error     string
	ExpiresAt time.Time

	commit    string
	nonceJ    []byte
	nonceM    []byte
	confirmed bool
	welcome   *pairingWelcome
}

// pairingView is what the local dashboard sees of a session: no nonces, no keys.
type pairingView struct {
	ID        string `json:"id"`
	Role      string `json:"role"`
	State     string `json:"state"`
	Code      string `json:"code,omitempty"`
	DeviceID  string `json:"device_id"`
	Hostname  string `json:"hostname,omitempty"`
	CircleID  string `json:"circle_id,omitempty"`
	Confirmed bool   `json:"confirmed"`
	Error     string `json:"error,omitempty"`
	ExpiresAt string `json:"expires_at"`
}

var pairings = struct {
	sync.Mutex
	byKey map[string]*pairingSession
}{byKey: map[string]*pairingSession{}}

func pairingKey(home, id string) string { return home + "|" + id }

func (s *pairingSession) view() pairingView {
	return pairingView{
		ID: s.ID, Role: s.Role, State: string(s.State), Code: s.Code, DeviceID: s.Peer.DeviceID,
		Hostname: s.Peer.Hostname, CircleID: s.CircleID, Confirmed: s.confirmed, Error: s.Error,
		ExpiresAt: s.ExpiresAt.UTC().Format(time.RFC3339),
	}
}

func (s *pairingSession) open() bool {
	switch s.State {
	case pairingJoined, pairingRejected, pairingExpired, pairingFailed:
		return false
	}
	return true
}

// expireLocked closes sessions past their deadline. Callers hold pairings.Mutex.
func expirePairingsLocked(now time.Time) {
	for key, s := range pairings.byKey {
		if s.open() && now.After(s.ExpiresAt) {
			s.State = pairingExpired
		}
		if !s.open() && now.After(s.ExpiresAt.Add(10*time.Minute)) {
			delete(pairings.byKey, key)
		}
	}
}

func listPairings(home string) []pairingView {
	pairings.Lock()
	defer pairings.Unlock()
	expirePairingsLocked(time.Now())
	out := []pairingView{}
	for _, s := range pairings.byKey {
		if s.home == home {
			out = append(out, s.view())
		}
	}
	return out
}

func getPairing(home, id string) (*pairingSession, bool) {
	pairings.Lock()
	defer pairings.Unlock()
	expirePairingsLocked(time.Now())
	s, ok := pairings.byKey[pairingKey(home, id)]
	return s, ok
}

func openPairingsLocked(home string) int {
	n := 0
	for _, s := range pairings.byKey {
		if s.home == home && s.open() {
			n++
		}
	}
	return n
}

// ---- wire ----

type pairMessage struct {
	Op        string      `json:"op"`
	SessionID string      `json:"session_id"`
	Joiner    pairingPeer `json:"joiner"`
	Commit    string      `json:"commit,omitempty"`
	Nonce     string      `json:"nonce,omitempty"`
	Code      string      `json:"code,omitempty"`
	Proof     string      `json:"proof,omitempty"`
	Timestamp string      `json:"timestamp"`
	Signature string      `json:"signature"`
}

type pairReply struct {
	OK        bool            `json:"ok"`
	Error     string          `json:"error,omitempty"`
	State     string          `json:"state,omitempty"`
	SessionID string          `json:"session_id"`
	Member    pairingPeer     `json:"member"`
	Nonce     string          `json:"nonce,omitempty"`
	Welcome   *pairingWelcome `json:"welcome,omitempty"`
	Signature string          `json:"signature"`
}

func signPairMessage(priv ed25519.PrivateKey, m pairMessage) (pairMessage, error) {
	m.Signature = ""
	payload, err := json.Marshal(m)
	if err != nil {
		return m, err
	}
	m.Signature = signPayload(priv, payload)
	return m, nil
}

func verifyPairMessage(m pairMessage, now time.Time) bool {
	if !memberKeyBound(m.Joiner.DeviceID, m.Joiner.PublicKey) {
		return false
	}
	ts, err := time.Parse(time.RFC3339, m.Timestamp)
	if err != nil || now.Sub(ts) > rosterMaxSkew || ts.Sub(now) > rosterMaxSkew {
		return false
	}
	pub, err := decodePublicKey(m.Joiner.PublicKey)
	if err != nil {
		return false
	}
	sig := m.Signature
	m.Signature = ""
	payload, err := json.Marshal(m)
	return err == nil && verifySignature(pub, payload, sig)
}

func signPairReply(priv ed25519.PrivateKey, r pairReply) pairReply {
	r.Signature = ""
	payload, err := json.Marshal(r)
	if err != nil {
		return r
	}
	r.Signature = signPayload(priv, payload)
	return r
}

func verifyPairReply(r pairReply) bool {
	if !memberKeyBound(r.Member.DeviceID, r.Member.PublicKey) {
		return false
	}
	pub, err := decodePublicKey(r.Member.PublicKey)
	if err != nil {
		return false
	}
	sig := r.Signature
	r.Signature = ""
	payload, err := json.Marshal(r)
	return err == nil && verifySignature(pub, payload, sig)
}

// handlePair serves the member side. The joiner's messages arrive on the share listener before any circle
// is known, so this runs ahead of the circle lookup.
func (sl *shareListener) handlePair(conn net.Conn, priv ed25519.PrivateKey, plain string) {
	var m pairMessage
	if err := json.Unmarshal([]byte(plain), &m); err != nil || !verifyPairMessage(m, time.Now()) {
		return
	}
	// As a legacy join does, record where the joiner actually connected from when it advertises a wildcard
	// or loopback address.
	if conn != nil && conn.RemoteAddr() != nil {
		m.Joiner.ListenAddr = chooseEndpoint(m.Joiner.ListenAddr, conn.RemoteAddr().String())
	}
	self, err := selfPairingPeer(sl.id, priv)
	if err != nil {
		return
	}
	var reply pairReply
	if m.Op == "invite" {
		reply = memberHandleInvite(sl.home, sl.id, priv, m)
	} else {
		reply = memberHandlePair(sl.home, self, m)
	}
	reply.SessionID, reply.Member = m.SessionID, self
	out, err := json.Marshal(signPairReply(priv, reply))
	if err != nil {
		return
	}
	env, _ := json.Marshal(wireEnvelope{Kind: pairKind, Plain: string(out)})
	_, _ = conn.Write(append(env, '\n'))
}

func memberHandlePair(home string, self pairingPeer, m pairMessage) pairReply {
	pairings.Lock()
	defer pairings.Unlock()
	expirePairingsLocked(time.Now())
	key := pairingKey(home, m.SessionID)
	s, exists := pairings.byKey[key]
	if exists && (s.Role != "member" || s.Peer.DeviceID != m.Joiner.DeviceID || s.Peer.PublicKey != m.Joiner.PublicKey) {
		return pairReply{Error: "unknown pairing"}
	}
	switch m.Op {
	case "hello":
		if exists || m.SessionID == "" || m.Commit == "" || len(m.SessionID) > 64 {
			return pairReply{Error: "bad pairing"}
		}
		if openPairingsLocked(home) >= maxPairingSessions {
			return pairReply{Error: "too many pairings"}
		}
		nonce := []byte(randomToken(32))
		pairings.byKey[key] = &pairingSession{
			home: home, ID: m.SessionID, Role: "member", State: pairingConnecting, Peer: m.Joiner,
			commit: m.Commit, nonceM: nonce, ExpiresAt: time.Now().Add(pairingSessionTTL),
		}
		return pairReply{OK: true, State: string(pairingConnecting), Nonce: base64.StdEncoding.EncodeToString(nonce)}
	case "reveal":
		if !exists || s.State != pairingConnecting {
			return pairReply{Error: "unknown pairing"}
		}
		nonce, err := base64.StdEncoding.DecodeString(m.Nonce)
		if err != nil || !pairingRevealMatches(s.ID, s.Peer, s.commit, nonce) {
			s.State, s.Error = pairingFailed, "the other device did not keep its commitment"
			return pairReply{Error: "commitment mismatch"}
		}
		s.nonceJ = nonce
		s.Code = pairingCode(s.ID, s.Peer, self, s.commit, s.nonceM, s.nonceJ)
		s.State = pairingComparing
		return pairReply{OK: true, State: string(s.State)}
	case "poll":
		if !exists {
			return pairReply{Error: "unknown pairing"}
		}
		reply := pairReply{OK: true, State: string(s.State)}
		if s.welcome != nil {
			reply.Welcome = s.welcome
			s.State = pairingJoined
		}
		return reply
	case "cancel":
		if exists && s.open() {
			s.State = pairingRejected
		}
		return pairReply{OK: true, State: string(pairingRejected)}
	}
	return pairReply{Error: "bad pairing"}
}

func sendPairMessage(endpoint string, m pairMessage) (pairReply, error) {
	conn, err := hookNetDial("tcp", endpoint, peerIDDialTimeout*5)
	if err != nil {
		return pairReply{}, err
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
	payload, err := json.Marshal(m)
	if err != nil {
		return pairReply{}, err
	}
	out, _ := json.Marshal(wireEnvelope{Kind: pairKind, Plain: string(payload)})
	if _, err := conn.Write(append(out, '\n')); err != nil {
		return pairReply{}, err
	}
	line, err := bufio.NewReader(conn).ReadString('\n')
	if err != nil {
		return pairReply{}, err
	}
	var env wireEnvelope
	if err := json.Unmarshal([]byte(strings.TrimSpace(line)), &env); err != nil {
		return pairReply{}, err
	}
	var reply pairReply
	if err := json.Unmarshal([]byte(env.Plain), &reply); err != nil {
		return pairReply{}, err
	}
	if !verifyPairReply(reply) || reply.SessionID != m.SessionID {
		return pairReply{}, errors.New("pairing reply is not signed by the device that answered")
	}
	if reply.Error != "" {
		return reply, errors.New(reply.Error)
	}
	return reply, nil
}

// ---- joiner side ----

func joinerMessage(id DeviceIdentity, priv ed25519.PrivateKey, self pairingPeer, sessionID, op string) (pairMessage, error) {
	return signPairMessage(priv, pairMessage{Op: op, SessionID: sessionID, Joiner: self, Timestamp: nowRFC3339()})
}

// startPairing runs the joiner's half of the exchange with the device the person picked, up to the code.
// expectedDeviceID is the device chosen in the UI: whoever answers must prove that identity.
func startPairing(home, endpoint, expectedDeviceID string) (pairingView, error) {
	if _, _, err := net.SplitHostPort(endpoint); err != nil {
		return pairingView{}, errors.New("that device has no reachable address")
	}
	id, priv, err := loadOrCreateIdentity(home)
	if err != nil {
		return pairingView{}, err
	}
	self, err := selfPairingPeer(id, priv)
	if err != nil {
		return pairingView{}, err
	}
	s := &pairingSession{
		home: home, ID: "ps_" + randomToken(16), Role: "joiner", State: pairingConnecting, Endpoint: endpoint,
		Peer: pairingPeer{DeviceID: expectedDeviceID}, nonceJ: []byte(randomToken(32)),
		ExpiresAt: time.Now().Add(pairingSessionTTL),
	}
	s.commit = pairingCommit(s.ID, self, s.nonceJ)
	hello, err := joinerMessage(id, priv, self, s.ID, "hello")
	if err != nil {
		return pairingView{}, err
	}
	hello.Commit = s.commit
	if hello, err = signPairMessage(priv, hello); err != nil {
		return pairingView{}, err
	}
	reply, err := sendPairMessage(endpoint, hello)
	if err != nil {
		return pairingView{}, err
	}
	if expectedDeviceID != "" && reply.Member.DeviceID != expectedDeviceID {
		return pairingView{}, errors.New("the device that answered is not the one you picked")
	}
	nonceM, err := base64.StdEncoding.DecodeString(reply.Nonce)
	if err != nil || len(nonceM) == 0 {
		return pairingView{}, errors.New("pairing reply is incomplete")
	}
	s.Peer, s.nonceM = reply.Member, nonceM
	reveal, err := joinerMessage(id, priv, self, s.ID, "reveal")
	if err != nil {
		return pairingView{}, err
	}
	reveal.Nonce = base64.StdEncoding.EncodeToString(s.nonceJ)
	if reveal, err = signPairMessage(priv, reveal); err != nil {
		return pairingView{}, err
	}
	if _, err := sendPairMessage(endpoint, reveal); err != nil {
		return pairingView{}, err
	}
	s.Code = pairingCode(s.ID, self, s.Peer, s.commit, s.nonceM, s.nonceJ)
	s.State = pairingComparing
	pairings.Lock()
	expirePairingsLocked(time.Now())
	pairings.byKey[pairingKey(home, s.ID)] = s
	view := s.view()
	pairings.Unlock()
	go pollPairing(home, s.ID, id, priv, self)
	return view, nil
}

// pollPairing waits for the member's decision. A welcome is kept until this side's person confirms the code;
// only then does the device join.
func pollPairing(home, sessionID string, id DeviceIdentity, priv ed25519.PrivateKey, self pairingPeer) {
	ticker := time.NewTicker(pairingPollEvery)
	defer ticker.Stop()
	for range ticker.C {
		s, ok := getPairing(home, sessionID)
		if !ok {
			return
		}
		pairings.Lock()
		open, endpoint, haveWelcome := s.open(), s.Endpoint, s.welcome != nil
		pairings.Unlock()
		if !open {
			return
		}
		if haveWelcome {
			finishJoinerPairing(home, s, id, priv)
			continue
		}
		poll, err := joinerMessage(id, priv, self, sessionID, "poll")
		if err != nil {
			continue
		}
		reply, err := sendPairMessage(endpoint, poll)
		pairings.Lock()
		switch {
		case err != nil && reply.Error == "":
		case reply.State == string(pairingRejected) || reply.Error != "":
			s.State = pairingRejected
		case reply.Welcome != nil:
			s.welcome = reply.Welcome
		}
		pairings.Unlock()
		finishJoinerPairing(home, s, id, priv)
	}
}

func finishJoinerPairing(home string, s *pairingSession, id DeviceIdentity, priv ed25519.PrivateKey) {
	pairings.Lock()
	if !s.open() || !s.confirmed || s.welcome == nil {
		pairings.Unlock()
		return
	}
	welcome, member, endpoint := *s.welcome, s.Peer, s.Endpoint
	pairings.Unlock()
	circleID, err := applyPairingWelcome(home, s.ID, welcome, member, endpoint, id, priv)
	pairings.Lock()
	if err != nil {
		s.State, s.Error = pairingFailed, err.Error()
	} else {
		s.State, s.CircleID = pairingJoined, circleID
	}
	pairings.Unlock()
}

func applyPairingWelcome(home, sessionID string, w pairingWelcome, member pairingPeer, endpoint string, id DeviceIdentity, priv ed25519.PrivateKey) (string, error) {
	body, err := openPairingWelcome(sessionID, w, member, id, priv)
	if err != nil {
		return "", err
	}
	circle := body.Circle
	if existing, err := loadCircle(home, circle.ID); err == nil {
		if _, ok := existing.activeMember(id.DeviceID); ok {
			return "", errors.New("this device is already in that circle")
		}
	}
	circle.AllowedFolders, circle.PeerEndpoints = []string{}, []string{}
	circle = stampMemberEndpoint(circle, member.DeviceID, endpoint, "")
	// This device's own record says where it listens, not where the member saw it connect from.
	selfEndpoint := advertisedListenAddr()
	if selfEndpoint == strings.TrimSpace(endpoint) {
		selfEndpoint = ""
	}
	for i := range circle.Members {
		if circle.Members[i].DeviceID == id.DeviceID {
			circle.Members[i].Endpoint, circle.Members[i].RemoteAddr = selfEndpoint, ""
		}
	}
	circle = stampSelfMember(circle, id.DeviceID, selfEndpoint)
	circle = refreshPeerEndpoints(circle, id.DeviceID)
	if err := persistJoinedCircle(home, circle); err != nil {
		return "", err
	}
	return circle.ID, nil
}

// ---- local actions ----

// confirmPairing records this side's confirmation. On the member side it names the circle and admits the
// joiner; on the joiner side it lets a received welcome be applied.
func confirmPairing(home, sessionID, circleID string) (pairingView, error) {
	s, ok := getPairing(home, sessionID)
	if !ok {
		return pairingView{}, errors.New("pairing not found")
	}
	pairings.Lock()
	if s.State != pairingComparing {
		v := s.view()
		pairings.Unlock()
		return v, errors.New("this pairing is no longer waiting for a confirmation")
	}
	role, peer := s.Role, s.Peer
	pairings.Unlock()
	if role == "joiner" {
		pairings.Lock()
		s.confirmed, s.State = true, pairingWaiting
		pairings.Unlock()
		id, priv, err := loadOrCreateIdentity(home)
		if err == nil {
			finishJoinerPairing(home, s, id, priv)
		}
		pairings.Lock()
		defer pairings.Unlock()
		return s.view(), nil
	}
	id, priv, err := loadOrCreateIdentity(home)
	if err != nil {
		return pairingView{}, err
	}
	circle, err := circleForInvite(home, id, circleID)
	if err != nil {
		return pairingView{}, err
	}
	welcome, err := admitWithWelcome(home, circle.ID, sessionID, id, priv, peer)
	if err != nil {
		return pairingView{}, err
	}
	pairings.Lock()
	defer pairings.Unlock()
	s.confirmed, s.welcome, s.CircleID, s.State = true, &welcome, circle.ID, pairingWaiting
	return s.view(), nil
}

func cancelPairing(home, sessionID string) (pairingView, error) {
	s, ok := getPairing(home, sessionID)
	if !ok {
		return pairingView{}, errors.New("pairing not found")
	}
	pairings.Lock()
	if s.open() {
		s.State = pairingRejected
	}
	role, endpoint, view := s.Role, s.Endpoint, s.view()
	pairings.Unlock()
	if role == "joiner" {
		if id, priv, err := loadOrCreateIdentity(home); err == nil {
			if self, err := selfPairingPeer(id, priv); err == nil {
				if m, err := joinerMessage(id, priv, self, sessionID, "cancel"); err == nil {
					_, _ = sendPairMessage(endpoint, m)
				}
			}
		}
	}
	return view, nil
}

// discoveredEndpoint resolves the listener address of a device found by mDNS.
func discoveredEndpoint(home, deviceID string) (string, error) {
	peers, err := listDiscoveredPeers(home)
	if err != nil {
		return "", err
	}
	for _, p := range peers {
		if p.DeviceID == deviceID && p.Host != "" && p.Port > 0 {
			return net.JoinHostPort(p.Host, strconv.Itoa(p.Port)), nil
		}
	}
	return "", errors.New("that device is not on the nearby list any more")
}

func registerPairingRoutes(mux *http.ServeMux, home string) {
	mux.HandleFunc("/api/pairing/sessions", func(w http.ResponseWriter, r *http.Request) {
		if rejectRemoteCoreAdmin(w, r) {
			return
		}
		switch r.Method {
		case http.MethodGet:
			writeInviteJSON(w, http.StatusOK, map[string]any{"sessions": listPairings(home)})
		case http.MethodPost:
			var body struct {
				DeviceID string `json:"device_id"`
				Endpoint string `json:"endpoint"`
			}
			r.Body = http.MaxBytesReader(w, r.Body, 4*1024)
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				writeInviteError(w, http.StatusBadRequest, "bad request")
				return
			}
			endpoint := strings.TrimSpace(body.Endpoint)
			if endpoint == "" {
				found, err := discoveredEndpoint(home, strings.TrimSpace(body.DeviceID))
				if err != nil {
					writeInviteError(w, http.StatusNotFound, err.Error())
					return
				}
				endpoint = found
			}
			view, err := startPairing(home, endpoint, strings.TrimSpace(body.DeviceID))
			if err != nil {
				writeInviteError(w, http.StatusBadGateway, err.Error())
				return
			}
			writeInviteJSON(w, http.StatusCreated, map[string]any{"session": view})
		default:
			http.Error(w, methodNotAllowedMsg, http.StatusMethodNotAllowed)
		}
	})
	mux.HandleFunc("/api/pairing/sessions/{id}/{action}", func(w http.ResponseWriter, r *http.Request) {
		if rejectRemoteCoreAdmin(w, r) {
			return
		}
		if r.Method != http.MethodPost {
			http.Error(w, methodNotAllowedMsg, http.StatusMethodNotAllowed)
			return
		}
		var view pairingView
		var err error
		switch r.PathValue("action") {
		case "confirm":
			var body struct {
				CircleID string `json:"circle_id"`
			}
			r.Body = http.MaxBytesReader(w, r.Body, 4*1024)
			_ = json.NewDecoder(r.Body).Decode(&body)
			view, err = confirmPairing(home, r.PathValue("id"), strings.TrimSpace(body.CircleID))
		case "cancel":
			view, err = cancelPairing(home, r.PathValue("id"))
		default:
			http.NotFound(w, r)
			return
		}
		if err != nil {
			writeInviteError(w, http.StatusConflict, err.Error())
			return
		}
		writeInviteJSON(w, http.StatusOK, map[string]any{"session": view})
	})
}

// ---- invite tokens ----

const inviteProofPurpose = "overdrive-invite-proof-v1"

var inviteUse sync.Mutex

func inviteProof(secret, sessionID, code string, joiner pairingPeer, timestamp string) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(pairingDigest(inviteProofPurpose, sessionID, code, joiner.DeviceID, joiner.PublicKey, joiner.KexPublicKey, timestamp))
	return base64.StdEncoding.EncodeToString(mac.Sum(nil))
}

// joinWithInviteToken redeems a version 2 invite: the joiner proves it holds the invite secret without sending
// it, and the inviter answers with the same sealed welcome a pairing ends with.
func joinWithInviteToken(home string, invite CircleInvite, endpoint string, id DeviceIdentity, priv ed25519.PrivateKey) (Circle, error) {
	if strings.TrimSpace(endpoint) == "" {
		endpoint = joinEndpoint(invite.Endpoint)
	}
	if _, _, err := net.SplitHostPort(endpoint); err != nil {
		return Circle{}, errors.New("the invite has no address to reach the inviting device")
	}
	self, err := selfPairingPeer(id, priv)
	if err != nil {
		return Circle{}, err
	}
	m := pairMessage{Op: "invite", SessionID: "pi_" + randomToken(16), Joiner: self, Code: invite.Code, Timestamp: nowRFC3339()}
	m.Proof = inviteProof(invite.Secret, m.SessionID, m.Code, self, m.Timestamp)
	if m, err = signPairMessage(priv, m); err != nil {
		return Circle{}, err
	}
	reply, err := sendPairMessage(endpoint, m)
	if err != nil {
		return Circle{}, err
	}
	if reply.Member.DeviceID != invite.CreatorID || reply.Member.PublicKey != invite.CreatorPK || reply.Welcome == nil {
		return Circle{}, errors.New("the device that answered did not issue this invite")
	}
	circleID, err := applyPairingWelcome(home, m.SessionID, *reply.Welcome, reply.Member, endpoint, id, priv)
	if err != nil {
		return Circle{}, err
	}
	_ = hookRemove(filepath.Join(invitesDir(home), invite.Code+".json"))
	return loadCircle(home, circleID)
}

// memberHandleInvite redeems one of this device's invites. The proof binds the secret to the joiner's identity
// and the request time, so a captured request cannot be replayed for another device; a used invite is dropped.
func memberHandleInvite(home string, id DeviceIdentity, priv ed25519.PrivateKey, m pairMessage) pairReply {
	if !inviteCodeSafe(m.Code) {
		return pairReply{Error: "invite not found"}
	}
	inviteUse.Lock()
	defer inviteUse.Unlock()
	invite, err := loadJoinInvite(home, m.Code)
	if err != nil || invite.Version < 2 || invite.CreatorID != id.DeviceID {
		return pairReply{Error: "invite not found"}
	}
	if !joinInviteValid(invite, time.Now()) {
		return pairReply{Error: "invite expired"}
	}
	want := inviteProof(invite.Secret, m.SessionID, m.Code, m.Joiner, m.Timestamp)
	if !hmac.Equal([]byte(want), []byte(m.Proof)) {
		return pairReply{Error: "invite not found"}
	}
	welcome, err := admitWithWelcome(home, invite.CircleID, m.SessionID, id, priv, m.Joiner)
	if err != nil {
		return pairReply{Error: "could not admit this device"}
	}
	_ = hookRemove(filepath.Join(invitesDir(home), invite.Code+".json"))
	return pairReply{OK: true, State: string(pairingJoined), Welcome: &welcome}
}

// admitWithWelcome admits the joiner into the current state of the circle on disk and returns the sealed
// welcome built from that same state.
func admitWithWelcome(home, circleID, sessionID string, id DeviceIdentity, priv ed25519.PrivateKey, joiner pairingPeer) (pairingWelcome, error) {
	var welcome pairingWelcome
	_, err := updateCircle(home, circleID, func(circle Circle) (Circle, error) {
		if _, ok := circle.activeMember(id.DeviceID); !ok {
			return circle, errors.New("this device is not in that circle")
		}
		w, admitted, err := buildPairingWelcome(sessionID, circle, id, priv, joiner)
		welcome = w
		return vouchMembers(admitted, id, priv), err
	})
	return welcome, err
}
