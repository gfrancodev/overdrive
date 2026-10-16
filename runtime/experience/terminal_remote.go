package main

import (
	"bufio"
	"crypto/ed25519"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

// A circle member opens a terminal hosted on another core: the viewer's core proxies the dashboard's
// session calls to the host's share listener. Every connection starts with one signed request; stream
// frames then flow as lines sealed with the circle key. The host keeps enforcing its own control lease.

const (
	terminalPeerKind        = "terminal"
	terminalPeerMaxSkew     = 2 * time.Minute
	terminalPeerDialWait    = 3 * time.Second
	terminalPeerReplyWait   = 10 * time.Second
	terminalPeerMaxLineLen  = 4 << 20
	terminalRemoteKeyPrefix = "remote:"
)

type terminalPeerRequest struct {
	DeviceID  string `json:"device_id"`
	CircleID  string `json:"circle_id"`
	SessionID string `json:"session_id"`
	Op        string `json:"op"`
	ControlOp string `json:"control_op,omitempty"`
	LeaseID   string `json:"lease_id,omitempty"`
	Epoch     int64  `json:"epoch,omitempty"`
	Timestamp string `json:"timestamp"`
	Signature string `json:"signature"`
}

type terminalPeerResponse struct {
	OK           bool                  `json:"ok"`
	Error        string                `json:"error,omitempty"`
	Session      *TerminalSession      `json:"session,omitempty"`
	Lease        *ControlLease         `json:"lease,omitempty"`
	Participants             []TerminalParticipant `json:"participants,omitempty"`
	PendingControlRequesters []string              `json:"pending_control_requesters,omitempty"`
}

func signTerminalPeerRequest(priv ed25519.PrivateKey, req terminalPeerRequest) (terminalPeerRequest, error) {
	if req.Timestamp == "" {
		req.Timestamp = time.Now().UTC().Format(time.RFC3339)
	}
	req.Signature = ""
	payload, err := json.Marshal(req)
	if err != nil {
		return req, err
	}
	req.Signature = signPayload(priv, payload)
	return req, nil
}

func verifyTerminalPeerRequest(pub ed25519.PublicKey, req terminalPeerRequest, now time.Time) bool {
	ts, err := time.Parse(time.RFC3339, req.Timestamp)
	if err != nil {
		return false
	}
	if skew := now.Sub(ts); skew > terminalPeerMaxSkew || skew < -terminalPeerMaxSkew {
		return false
	}
	sig := req.Signature
	req.Signature = ""
	payload, err := json.Marshal(req)
	if err != nil {
		return false
	}
	return verifySignature(pub, payload, sig)
}

// authorizeTerminalPeer admits active circle members to sessions shared with that circle, never private ones.
func authorizeTerminalPeer(mgr *TerminalManager, circle Circle, req terminalPeerRequest) (TerminalSession, error) {
	denied := fmt.Errorf("%s", TerminalErrAccessDenied)
	member, ok := circle.activeMember(req.DeviceID)
	if !ok || req.CircleID != circle.ID {
		return TerminalSession{}, denied
	}
	pub, err := decodePublicKey(member.PublicKey)
	if err != nil || !verifyTerminalPeerRequest(pub, req, time.Now()) {
		return TerminalSession{}, denied
	}
	sess, err := mgr.GetSession(req.SessionID)
	if err != nil {
		return TerminalSession{}, fmt.Errorf("%s", TerminalErrSessionNotFound)
	}
	if sess.CircleID != circle.ID {
		return TerminalSession{}, denied
	}
	if sess.PrivateMode {
		return TerminalSession{}, fmt.Errorf("%s", TerminalErrPrivateMode)
	}
	return sess, nil
}

// terminalPeerLink carries sealed lines over one peer connection; writes are serialized.
type terminalPeerLink struct {
	conn   net.Conn
	reader *bufio.Reader
	key    string
	mu     sync.Mutex
}

func newTerminalPeerLink(conn net.Conn, reader *bufio.Reader, key string) *terminalPeerLink {
	if reader == nil {
		reader = bufio.NewReader(conn)
	}
	return &terminalPeerLink{conn: conn, reader: reader, key: key}
}

func (l *terminalPeerLink) Send(plain []byte) error {
	sealed, err := encryptBatch(l.key, plain)
	if err != nil {
		return err
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	_, err = l.conn.Write([]byte(sealed + "\n"))
	return err
}

func (l *terminalPeerLink) Recv() ([]byte, error) {
	line, err := l.reader.ReadString('\n')
	if err != nil {
		return nil, err
	}
	if len(line) > terminalPeerMaxLineLen {
		return nil, errors.New("terminal peer frame too large")
	}
	return decryptBatch(l.key, strings.TrimSpace(line))
}

func (l *terminalPeerLink) sendJSON(v any) error {
	raw, err := json.Marshal(v)
	if err != nil {
		return err
	}
	return l.Send(raw)
}

func (l *terminalPeerLink) recvResponse() (terminalPeerResponse, error) {
	var resp terminalPeerResponse
	raw, err := l.Recv()
	if err != nil {
		return resp, err
	}
	err = json.Unmarshal(raw, &resp)
	return resp, err
}

func (l *terminalPeerLink) Close() error {
	return l.conn.Close()
}

// handleTerminalPeer serves one viewer request that arrived on the share listener.
func (sl *shareListener) handleTerminalPeer(conn net.Conn, reader *bufio.Reader, circle Circle, plain []byte) {
	link := newTerminalPeerLink(conn, reader, circle.GroupKey)
	var req terminalPeerRequest
	if err := json.Unmarshal(plain, &req); err != nil {
		return
	}
	mgr, err := getTerminalManager(sl.home)
	if err != nil {
		_ = link.sendJSON(terminalPeerResponse{Error: string(TerminalErrHostUnavailable)})
		return
	}
	sess, err := authorizeTerminalPeer(mgr, circle, req)
	if err != nil {
		_ = link.sendJSON(terminalPeerResponse{Error: err.Error()})
		return
	}
	switch req.Op {
	case "meta":
		_ = link.sendJSON(terminalPeerMeta(mgr, sess))
	case "control":
		_ = link.sendJSON(terminalPeerControl(mgr, req))
	case "stream":
		serveTerminalPeerStream(sl.home, mgr, link, req)
	default:
		_ = link.sendJSON(terminalPeerResponse{Error: "unknown terminal op"})
	}
}

func terminalPeerMeta(mgr *TerminalManager, sess TerminalSession) terminalPeerResponse {
	resp := terminalPeerResponse{OK: true, Session: &sess}
	if lease, _, err := mgr.store.GetActiveLease(sess.ID); err == nil {
		resp.Lease = &lease
	}
	if parts, err := mgr.store.ListParticipants(sess.ID); err == nil {
		resp.Participants = parts
	}
	if pending, err := mgr.store.PendingControlRequesters(sess.ID); err == nil {
		resp.PendingControlRequesters = pending
	}
	return resp
}

func terminalPeerControl(mgr *TerminalManager, req terminalPeerRequest) terminalPeerResponse {
	ctrl := newTerminalControl(mgr.store, req.DeviceID)
	var err error
	switch req.ControlOp {
	case "request":
		err = ctrl.RequestControl(req.SessionID, req.DeviceID)
	case "release":
		_, err = ctrl.ReleaseControl(req.SessionID, req.DeviceID, req.LeaseID, req.Epoch)
	default:
		err = errors.New("unknown control op")
	}
	if err != nil {
		return terminalPeerResponse{Error: err.Error()}
	}
	sess, err := mgr.GetSession(req.SessionID)
	if err != nil {
		return terminalPeerResponse{Error: err.Error()}
	}
	return terminalPeerMeta(mgr, sess)
}

// serveTerminalPeerStream relays host output to a remote viewer and applies its writes under the lease rules.
func serveTerminalPeerStream(home string, mgr *TerminalManager, link *terminalPeerLink, req terminalPeerRequest) {
	_ = link.conn.SetDeadline(time.Time{})
	caps := []TerminalCapability{TerminalCapObserve, TerminalCapInput}
	if _, err := mgr.AttachSession(req.SessionID, req.DeviceID, caps); err != nil {
		_ = link.sendJSON(terminalPeerResponse{Error: err.Error()})
		return
	}
	if err := link.sendJSON(terminalPeerResponse{OK: true}); err != nil {
		return
	}
	hub := registerTerminalStream(req.SessionID, req.DeviceID, caps)
	defer unregisterTerminalStreamHub(req.SessionID, req.DeviceID, hub)
	go terminalBridgeHostOutput(home, req.SessionID, hub)
	ctrl := newTerminalControl(mgr.store, req.DeviceID)
	errCh := make(chan error, 1)
	go func() {
		for {
			raw, err := link.Recv()
			if err != nil {
				errCh <- err
				return
			}
			f, err := decodeTerminalFrame(raw)
			if err != nil {
				continue
			}
			handleViewerFrame(home, req.SessionID, req.DeviceID, caps, ctrl, f, link.Send)
		}
	}()
	pumpTerminalHub(hub, link.Send, errCh)
}

// dialTerminalPeer opens a link to endpoint and sends the signed request; the caller reads the reply.
func dialTerminalPeer(endpoint string, circle Circle, priv ed25519.PrivateKey, req terminalPeerRequest) (*terminalPeerLink, error) {
	req.CircleID = circle.ID
	signed, err := signTerminalPeerRequest(priv, req)
	if err != nil {
		return nil, err
	}
	payload, err := json.Marshal(signed)
	if err != nil {
		return nil, err
	}
	sealed, err := encryptBatch(circle.GroupKey, payload)
	if err != nil {
		return nil, err
	}
	envRaw, err := json.Marshal(wireEnvelope{CircleID: circle.ID, Ciphertext: sealed, Kind: terminalPeerKind})
	if err != nil {
		return nil, err
	}
	conn, err := hookNetDial("tcp", endpoint, terminalPeerDialWait)
	if err != nil {
		return nil, err
	}
	_ = conn.SetDeadline(time.Now().Add(terminalPeerReplyWait))
	if _, err := conn.Write(append(envRaw, '\n')); err != nil {
		conn.Close()
		return nil, err
	}
	return newTerminalPeerLink(conn, nil, circle.GroupKey), nil
}

func callTerminalPeer(endpoint string, circle Circle, priv ed25519.PrivateKey, req terminalPeerRequest) (terminalPeerResponse, error) {
	link, err := dialTerminalPeer(endpoint, circle, priv, req)
	if err != nil {
		return terminalPeerResponse{}, err
	}
	defer link.Close()
	return link.recvResponse()
}

// openTerminalPeerStream returns a live link once the host accepted the stream.
func openTerminalPeerStream(endpoint string, circle Circle, priv ed25519.PrivateKey, req terminalPeerRequest) (*terminalPeerLink, error) {
	req.Op = "stream"
	link, err := dialTerminalPeer(endpoint, circle, priv, req)
	if err != nil {
		return nil, err
	}
	resp, err := link.recvResponse()
	if err == nil && !resp.OK {
		err = errors.New(resp.Error)
	}
	if err != nil {
		link.Close()
		return nil, err
	}
	_ = link.conn.SetDeadline(time.Time{})
	return link, nil
}

// terminalHostRoute finds the circle and endpoint through which this core reaches hostDeviceID.
func terminalHostRoute(home, hostDeviceID string) (Circle, string, error) {
	circles, err := listCircles(home)
	if err != nil {
		return Circle{}, "", err
	}
	for _, c := range circles {
		member, ok := c.activeMember(hostDeviceID)
		if ok && member.Endpoint != "" {
			return c, member.Endpoint, nil
		}
	}
	return Circle{}, "", fmt.Errorf("%s", TerminalErrHostUnavailable)
}

func terminalRemoteStreamKey(hostDeviceID, sessionID string) string {
	return terminalRemoteKeyPrefix + hostDeviceID + ":" + sessionID
}

type terminalRemoteProxy struct {
	w          http.ResponseWriter
	r          *http.Request
	host       string
	sessionID  string
	endpoint   string
	circle     Circle
	priv       ed25519.PrivateKey
	id         DeviceIdentity
	peerCall   func(terminalPeerRequest) (terminalPeerResponse, bool)
}

func (p *terminalRemoteProxy) handleMetaGET() {
	if resp, ok := p.peerCall(terminalPeerRequest{Op: "meta"}); ok {
		writeTerminalJSON(p.w, http.StatusOK, terminalRemotePayload(resp, p.id.DeviceID))
	}
}

func (p *terminalRemoteProxy) handleParticipantsGET() {
	if resp, ok := p.peerCall(terminalPeerRequest{Op: "meta"}); ok {
		writeTerminalJSON(p.w, http.StatusOK, map[string]any{"participants": nonNilParticipants(resp.Participants)})
	}
}

func (p *terminalRemoteProxy) handleAttachPOST() {
	resp, ok := p.peerCall(terminalPeerRequest{Op: "meta"})
	if !ok {
		return
	}
	token := issueRemoteStreamToken(terminalRemoteStreamKey(p.host, p.sessionID), p.id.DeviceID)
	payload := terminalRemotePayload(resp, p.id.DeviceID)
	payload["stream_token"] = token
	writeTerminalJSON(p.w, http.StatusOK, payload)
}

func (p *terminalRemoteProxy) handleControlPOST(action string) {
	var body struct {
		LeaseID string `json:"lease_id"`
		Epoch   int64  `json:"epoch"`
	}
	_ = json.NewDecoder(p.r.Body).Decode(&body)
	req := terminalPeerRequest{
		Op: "control", ControlOp: strings.TrimPrefix(action, "control/"),
		LeaseID: body.LeaseID, Epoch: body.Epoch,
	}
	if resp, ok := p.peerCall(req); ok {
		writeTerminalJSON(p.w, http.StatusOK, terminalRemotePayload(resp, p.id.DeviceID))
	}
}

func (p *terminalRemoteProxy) dispatch(action string) bool {
	switch {
	case action == "" && p.r.Method == http.MethodGet:
		p.handleMetaGET()
	case action == "participants" && p.r.Method == http.MethodGet:
		p.handleParticipantsGET()
	case action == "attach" && p.r.Method == http.MethodPost:
		p.handleAttachPOST()
	case action == "detach" && p.r.Method == http.MethodPost:
		writeTerminalJSON(p.w, http.StatusOK, map[string]any{"ok": true})
	case action == "stream" && p.r.Method == http.MethodGet:
		serveTerminalRemoteStream(p.w, p.r, terminalRemoteStreamParams{
			endpoint: p.endpoint, circle: p.circle, priv: p.priv, id: p.id, host: p.host, sessionID: p.sessionID,
		})
	case strings.HasPrefix(action, "control/") && p.r.Method == http.MethodPost:
		p.handleControlPOST(action)
	default:
		return false
	}
	return true
}

// serveTerminalRemote proxies /remote/{host}/sessions/{id}[/action] from the local dashboard to the host core.
func serveTerminalRemote(w http.ResponseWriter, r *http.Request, home string, id DeviceIdentity, priv ed25519.PrivateKey, parts []string) {
	if len(parts) < 3 || parts[1] != "sessions" || parts[0] == "" || parts[2] == "" {
		http.NotFound(w, r)
		return
	}
	host, sessionID := parts[0], parts[2]
	action := strings.Join(parts[3:], "/")
	circle, endpoint, err := terminalHostRoute(home, host)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	proxy := terminalRemoteProxy{
		w: w, r: r, host: host, sessionID: sessionID, endpoint: endpoint, circle: circle, priv: priv, id: id,
	}
	proxy.peerCall = func(req terminalPeerRequest) (terminalPeerResponse, bool) {
		req.DeviceID, req.SessionID = id.DeviceID, sessionID
		resp, err := callTerminalPeer(endpoint, circle, priv, req)
		if err != nil {
			http.Error(w, string(TerminalErrHostUnavailable), http.StatusBadGateway)
			return resp, false
		}
		if !resp.OK {
			writeTerminalError(w, errors.New(resp.Error))
			return resp, false
		}
		return resp, true
	}
	if !proxy.dispatch(action) {
		http.NotFound(w, r)
	}
}

func terminalRemotePayload(resp terminalPeerResponse, selfDeviceID string) map[string]any {
	lease := ControlLease{}
	if resp.Lease != nil {
		lease = *resp.Lease
	}
	pending := resp.PendingControlRequesters
	if pending == nil {
		pending = []string{}
	}
	return map[string]any{
		"session": resp.Session, "lease": lease, "self_device_id": selfDeviceID, "remote": true,
		"pending_control_requesters": pending,
	}
}

func nonNilParticipants(parts []TerminalParticipant) []TerminalParticipant {
	if parts == nil {
		return []TerminalParticipant{}
	}
	return parts
}

func issueRemoteStreamToken(key, deviceID string) string {
	tok := randomToken(32)
	streamGrantMu.Lock()
	streamGrants[tok] = terminalStreamGrant{
		SessionID: key,
		DeviceID:  deviceID,
		Token:     tok,
		Caps:      []TerminalCapability{TerminalCapObserve, TerminalCapInput},
		Expires:   time.Now().Add(terminalStreamTokenTTL),
	}
	streamGrantMu.Unlock()
	return tok
}

func remoteStreamTokenValid(key, token string) bool {
	streamGrantMu.Lock()
	grant, ok := streamGrants[token]
	streamGrantMu.Unlock()
	return ok && grant.SessionID == key && time.Now().Before(grant.Expires)
}

type terminalRemoteStreamParams struct {
	endpoint  string
	circle    Circle
	priv      ed25519.PrivateKey
	id        DeviceIdentity
	host      string
	sessionID string
}

// serveTerminalRemoteStream bridges the dashboard WebSocket to the host's peer stream, frame for frame.
func serveTerminalRemoteStream(w http.ResponseWriter, r *http.Request, p terminalRemoteStreamParams) {
	if !remoteStreamTokenValid(terminalRemoteStreamKey(p.host, p.sessionID), r.URL.Query().Get("token")) {
		http.Error(w, "invalid stream token", http.StatusUnauthorized)
		return
	}
	link, err := openTerminalPeerStream(p.endpoint, p.circle, p.priv, terminalPeerRequest{DeviceID: p.id.DeviceID, SessionID: p.sessionID})
	if err != nil {
		writeTerminalError(w, err)
		return
	}
	defer link.Close()
	conn, err := terminalWSUpgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	defer conn.Close()
	ws := &terminalWSWriter{conn: conn}
	go pumpTerminalClientToPeer(conn, link)
	pumpTerminalPeerToClient(link, ws)
}

func pumpTerminalClientToPeer(conn *websocket.Conn, link *terminalPeerLink) {
	defer link.Close()
	for {
		_, data, err := conn.ReadMessage()
		if err != nil {
			return
		}
		if link.Send(data) != nil {
			return
		}
	}
}

func pumpTerminalPeerToClient(link *terminalPeerLink, ws *terminalWSWriter) {
	for {
		raw, err := link.Recv()
		if err != nil {
			return
		}
		if ws.write(raw) != nil {
			return
		}
	}
}
