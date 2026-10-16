package main

import (
	"context"
	"crypto/ed25519"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"golang.org/x/crypto/hkdf"
)

const (
	terminalFrameMagic = "ODT1"
	terminalFrameHeaderLen = 4 + 1 + 1 + 8 + 4

	terminalFrameOutput    byte = 1
	terminalFrameInput     byte = 2
	terminalFrameResize    byte = 3
	terminalFrameSnapshot  byte = 4
	terminalFrameACK       byte = 5
	terminalFrameGAP       byte = 6
	terminalFrameHeartbeat byte = 7
	terminalFrameClose     byte = 8
	terminalFrameError     byte = 9

	terminalStreamQueueMax = 256
	terminalStreamTokenTTL = 30 * time.Minute
)

type terminalFrame struct {
	Type     byte
	Flags    byte
	Sequence int64
	Payload  []byte
}

func encodeTerminalFrame(f terminalFrame) ([]byte, error) {
	if len(f.Payload) > 16*1024*1024 {
		return nil, errors.New("frame too large")
	}
	out := make([]byte, terminalFrameHeaderLen+len(f.Payload))
	copy(out, terminalFrameMagic)
	out[4] = f.Type
	out[5] = f.Flags
	binary.BigEndian.PutUint64(out[6:14], uint64(f.Sequence))
	binary.BigEndian.PutUint32(out[14:18], uint32(len(f.Payload)))
	copy(out[18:], f.Payload)
	return out, nil
}

func decodeTerminalFrame(b []byte) (terminalFrame, error) {
	if len(b) < terminalFrameHeaderLen {
		return terminalFrame{}, errors.New("short frame")
	}
	if string(b[:4]) != terminalFrameMagic {
		return terminalFrame{}, errors.New("bad magic")
	}
	ln := int(binary.BigEndian.Uint32(b[14:18]))
	if ln < 0 || terminalFrameHeaderLen+ln != len(b) {
		return terminalFrame{}, errors.New("bad length")
	}
	return terminalFrame{
		Type:     b[4],
		Flags:    b[5],
		Sequence: int64(binary.BigEndian.Uint64(b[6:14])),
		Payload:  append([]byte(nil), b[18:]...),
	}, nil
}

func splitTerminalFrames(buf []byte) (frames []terminalFrame, rest []byte, err error) {
	off := 0
	for {
		if len(buf)-off < terminalFrameHeaderLen {
			return frames, buf[off:], nil
		}
		if string(buf[off:off+4]) != terminalFrameMagic {
			return frames, buf[off:], errors.New("bad magic")
		}
		ln := int(binary.BigEndian.Uint32(buf[off+14 : off+18]))
		if ln < 0 || ln > 16*1024*1024 {
			return frames, buf[off:], errors.New("bad length")
		}
		need := terminalFrameHeaderLen + ln
		if len(buf)-off < need {
			return frames, buf[off:], nil
		}
		f, derr := decodeTerminalFrame(buf[off : off+need])
		if derr != nil {
			return frames, buf[off:], derr
		}
		frames = append(frames, f)
		off += need
	}
}

func deriveTerminalSessionKey(circleID, sessionID string, groupKey []byte) []byte {
	salt := []byte("overdrive-terminal-stream-v1")
	info := []byte(circleID + "\x00" + sessionID)
	out := make([]byte, 32)
	r := hkdf.New(sha256.New, groupKey, salt, info)
	_, _ = io.ReadFull(r, out)
	return out
}

func frameMAC(key, frame []byte) []byte {
	m := hmac.New(sha256.New, key)
	_, _ = m.Write(frame)
	return m.Sum(nil)
}

type terminalStreamGrant struct {
	SessionID string
	DeviceID  string
	Token     string
	Caps      []TerminalCapability
	Expires   time.Time
}

var (
	streamGrantMu sync.Mutex
	streamGrants  = map[string]terminalStreamGrant{} // token -> grant
)

func issueTerminalStreamToken(home, sessionID, deviceID string, caps []TerminalCapability, ttl time.Duration) (string, error) {
	if ttl <= 0 {
		ttl = terminalStreamTokenTTL
	}
	store, err := newTerminalStore(home)
	if err != nil {
		return "", err
	}
	if _, err := store.GetSession(sessionID); err != nil {
		return "", err
	}
	tok := randomToken(32)
	grant := terminalStreamGrant{
		SessionID: sessionID,
		DeviceID:  deviceID,
		Token:     tok,
		Caps:      append([]TerminalCapability(nil), caps...),
		Expires:   time.Now().Add(ttl),
	}
	streamGrantMu.Lock()
	streamGrants[tok] = grant
	streamGrantMu.Unlock()
	return tok, nil
}

func verifyTerminalStreamToken(home, sessionID, token string, need TerminalCapability) error {
	streamGrantMu.Lock()
	grant, ok := streamGrants[token]
	streamGrantMu.Unlock()
	if !ok {
		return errors.New("invalid stream token")
	}
	if time.Now().After(grant.Expires) {
		return errors.New("stream token expired")
	}
	if grant.SessionID != sessionID {
		return errors.New("session mismatch")
	}
	if _, err := newTerminalStore(home); err != nil {
		return err
	}
	has := false
	for _, c := range grant.Caps {
		if c == need || c == TerminalCapHost {
			has = true
			break
		}
	}
	if !has {
		return fmt.Errorf("%s", TerminalErrAccessDenied)
	}
	return nil
}

func resetStreamGrantsForTests() {
	streamGrantMu.Lock()
	streamGrants = map[string]terminalStreamGrant{}
	streamGrantMu.Unlock()
}

var terminalWSUpgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool {
		origin := strings.TrimSpace(r.Header.Get("Origin"))
		if origin == "" {
			return true
		}
		return loopbackHTTPOrigin(origin)
	},
}

type terminalStreamHub struct {
	home      string
	sessionID string
	deviceID  string
	caps      []TerminalCapability
	outCh     chan terminalFrame
	done      chan struct{}
}

func newTerminalStreamHub(home, sessionID, deviceID string, caps []TerminalCapability) *terminalStreamHub {
	return &terminalStreamHub{
		home: home, sessionID: sessionID, deviceID: deviceID, caps: caps,
		outCh: make(chan terminalFrame, terminalStreamQueueMax),
		done:  make(chan struct{}),
	}
}

func (h *terminalStreamHub) close() {
	select {
	case <-h.done:
	default:
		close(h.done)
	}
}

func (h *terminalStreamHub) push(f terminalFrame) bool {
	select {
	case h.outCh <- f:
		return true
	default:
		// backpressure: drop slow viewer frames (never block PTY)
		return false
	}
}

func terminalStreamGrantForToken(token, sessionID string) (terminalStreamGrant, bool) {
	streamGrantMu.Lock()
	grant, ok := streamGrants[token]
	streamGrantMu.Unlock()
	if !ok || grant.SessionID != sessionID || time.Now().After(grant.Expires) {
		return terminalStreamGrant{}, false
	}
	return grant, true
}

func serveTerminalWebSocket(w http.ResponseWriter, r *http.Request, home, sessionID string) {
	if rejectRemoteCoreAdmin(w, r) {
		return
	}
	token := strings.TrimSpace(r.URL.Query().Get("token"))
	if token == "" {
		http.Error(w, string(TerminalErrAccessDenied), http.StatusUnauthorized)
		return
	}
	grant, ok := terminalStreamGrantForToken(token, sessionID)
	if !ok {
		http.Error(w, string(TerminalErrAccessDenied), http.StatusUnauthorized)
		return
	}
	mgr, err := getTerminalManager(home)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if _, err := mgr.GetSession(sessionID); err != nil {
		http.Error(w, string(TerminalErrSessionNotFound), http.StatusNotFound)
		return
	}
	conn, err := terminalWSUpgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	defer conn.Close()
	ws := &terminalWSWriter{conn: conn}
	hub := registerTerminalStream(sessionID, grant.DeviceID, grant.Caps)
	defer unregisterTerminalStreamHub(sessionID, grant.DeviceID, hub)
	ctrl := newTerminalControl(mgr.store, grant.DeviceID)
	go terminalBridgeHostOutput(home, sessionID, hub)
	if snap, err := fetchSessionSnapshot(home, sessionID); err == nil {
		_ = ws.write(mustEncodeFrame(terminalFrameSnapshot, snap.Sequence, snap.Payload))
	}
	errCh := make(chan error, 1)
	go func() {
		for {
			_, data, err := conn.ReadMessage()
			if err != nil {
				errCh <- err
				return
			}
			f, err := decodeTerminalFrame(data)
			if err != nil {
				errCh <- err
				return
			}
			handleViewerFrame(home, sessionID, grant.DeviceID, grant.Caps, ctrl, f, ws.write)
		}
	}()
	pumpTerminalHub(hub, ws.write, errCh)
}

// handleViewerFrame applies one frame sent by a viewer: writes need the input/control capability and a valid lease.
func handleViewerFrame(home, sessionID, deviceID string, caps []TerminalCapability, ctrl *TerminalControl, f terminalFrame, reply func([]byte) error) {
	switch f.Type {
	case terminalFrameInput, terminalFrameResize:
		if !capAllowed(caps, TerminalCapInput) && !capAllowed(caps, TerminalCapControl) {
			return
		}
		var meta terminalForwardMeta
		_ = json.Unmarshal(f.Payload, &meta)
		if err := ctrl.ValidateWrite(sessionID, deviceID, meta.LeaseID, meta.Epoch); err != nil {
			_ = reply(mustEncodeFrame(terminalFrameError, 0, []byte(err.Error())))
			return
		}
		if err := forwardTerminalFrameToHost(home, sessionID, f, deviceID, meta); err != nil {
			_ = reply(mustEncodeFrame(terminalFrameError, 0, []byte(err.Error())))
		}
	case terminalFrameHeartbeat:
		_ = reply(mustEncodeFrame(terminalFrameHeartbeat, 0, nil))
	}
}

// pumpTerminalHub sends hub output to one viewer until the hub closes or the viewer's reader fails.
func pumpTerminalHub(hub *terminalStreamHub, send func([]byte) error, errCh <-chan error) {
	ticker := time.NewTicker(15 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case f := <-hub.outCh:
			raw, err := encodeTerminalFrame(f)
			if err != nil {
				continue
			}
			if err := send(raw); err != nil {
				return
			}
		case <-ticker.C:
			_ = send(mustEncodeFrame(terminalFrameHeartbeat, 0, nil))
		case <-hub.done:
			for {
				select {
				case f := <-hub.outCh:
					raw, err := encodeTerminalFrame(f)
					if err != nil {
						continue
					}
					if err := send(raw); err != nil {
						return
					}
				default:
					return
				}
			}
		case <-errCh:
			return
		}
	}
}

// terminalWSWriter serializes writes: gorilla/websocket allows a single concurrent writer.
type terminalWSWriter struct {
	mu   sync.Mutex
	conn *websocket.Conn
}

func (w *terminalWSWriter) write(raw []byte) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.conn.WriteMessage(websocket.BinaryMessage, raw)
}

func (w *terminalWSWriter) writeFrame(f terminalFrame) error {
	raw, err := encodeTerminalFrame(f)
	if err != nil {
		return nil
	}
	return w.write(raw)
}

func capAllowed(caps []TerminalCapability, need TerminalCapability) bool {
	for _, c := range caps {
		if c == need || c == TerminalCapHost {
			return true
		}
	}
	return false
}

func mustEncodeFrame(typ byte, seq int64, payload []byte) []byte {
	b, err := encodeTerminalFrame(terminalFrame{Type: typ, Sequence: seq, Payload: payload})
	if err != nil {
		return nil
	}
	return b
}

var (
	terminalHubMu sync.Mutex
	// session -> device -> one hub per open connection; a device may watch from several windows at once.
	terminalHubs = map[string]map[string]map[*terminalStreamHub]struct{}{}
)

func registerTerminalStream(sessionID, deviceID string, caps []TerminalCapability) *terminalStreamHub {
	h := newTerminalStreamHub("", sessionID, deviceID, caps)
	terminalHubMu.Lock()
	defer terminalHubMu.Unlock()
	if terminalHubs[sessionID] == nil {
		terminalHubs[sessionID] = map[string]map[*terminalStreamHub]struct{}{}
	}
	if terminalHubs[sessionID][deviceID] == nil {
		terminalHubs[sessionID][deviceID] = map[*terminalStreamHub]struct{}{}
	}
	terminalHubs[sessionID][deviceID][h] = struct{}{}
	return h
}

func unregisterTerminalStream(sessionID, deviceID string) {
	terminalHubMu.Lock()
	defer terminalHubMu.Unlock()
	m := terminalHubs[sessionID]
	if m == nil {
		return
	}
	for h := range m[deviceID] {
		h.close()
	}
	delete(m, deviceID)
	if len(m) == 0 {
		delete(terminalHubs, sessionID)
	}
}

func unregisterTerminalStreamHub(sessionID, deviceID string, h *terminalStreamHub) {
	h.close()
	terminalHubMu.Lock()
	defer terminalHubMu.Unlock()
	m := terminalHubs[sessionID]
	if m == nil {
		return
	}
	delete(m[deviceID], h)
	if len(m[deviceID]) == 0 {
		delete(m, deviceID)
	}
	if len(m) == 0 {
		delete(terminalHubs, sessionID)
	}
}

func broadcastTerminalOutput(sessionID string, seq int64, chunk []byte) {
	terminalHubMu.Lock()
	var hubs []*terminalStreamHub
	for _, set := range terminalHubs[sessionID] {
		for h := range set {
			hubs = append(hubs, h)
		}
	}
	terminalHubMu.Unlock()
	f := terminalFrame{Type: terminalFrameOutput, Sequence: seq, Payload: chunk}
	for _, h := range hubs {
		h.push(f)
	}
}

func fetchSessionSnapshot(home, sessionID string) (TerminalSnapshot, error) {
	mgr, err := getTerminalManager(home)
	if err != nil {
		return TerminalSnapshot{}, err
	}
	sock, tok, err := mgr.sessionIPC(sessionID)
	if err != nil {
		return TerminalSnapshot{}, err
	}
	var resp terminalIPCResponse
	for attempt := 0; attempt < 20; attempt++ {
		resp, err = DialSessionHost(sock, tok, terminalIPCRequest{Op: "snapshot"})
		if err == nil && resp.OK {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if err != nil {
		return TerminalSnapshot{}, err
	}
	if !resp.OK {
		return TerminalSnapshot{}, errors.New(resp.Error)
	}
	var snap TerminalSnapshot
	if err := json.Unmarshal(resp.Data, &snap); err != nil {
		return TerminalSnapshot{}, err
	}
	return snap, nil
}

type terminalForwardMeta struct {
	LeaseID string `json:"lease_id"`
	Epoch   int64  `json:"epoch"`
	InputID string `json:"input_id"`
}

func dialTerminalSessionHost(home, sessionID string) (string, string, error) {
	mgr, err := getTerminalManager(home)
	if err != nil {
		return "", "", err
	}
	return mgr.sessionIPC(sessionID)
}

func postTerminalHostIPC(sock, tok string, req terminalIPCRequest) error {
	resp, err := DialSessionHost(sock, tok, req)
	if err != nil {
		return err
	}
	if !resp.OK {
		return errors.New(resp.Error)
	}
	return nil
}

func forwardTerminalInputFrame(sock, tok, actor string, f terminalFrame, meta terminalForwardMeta) error {
	data := string(f.Payload)
	var inline struct {
		Data    string `json:"data"`
		InputID string `json:"input_id"`
	}
	if json.Unmarshal(f.Payload, &inline) == nil && inline.Data != "" {
		data = inline.Data
		if meta.InputID == "" {
			meta.InputID = inline.InputID
		}
	}
	body, _ := json.Marshal(map[string]string{"data": data, "input_id": meta.InputID})
	req := terminalIPCRequest{Op: "input", Data: body, ActorDeviceID: actor, LeaseID: meta.LeaseID, Epoch: meta.Epoch}
	return postTerminalHostIPC(sock, tok, req)
}

func forwardTerminalResizeFrame(sock, tok, actor string, f terminalFrame, meta terminalForwardMeta) error {
	var size terminalResizePayload
	if err := json.Unmarshal(f.Payload, &size); err != nil {
		return err
	}
	body, _ := json.Marshal(size)
	req := terminalIPCRequest{Op: "resize", Data: body, ActorDeviceID: actor, LeaseID: meta.LeaseID, Epoch: meta.Epoch}
	return postTerminalHostIPC(sock, tok, req)
}

func forwardTerminalFrameToHost(home, sessionID string, f terminalFrame, actor string, meta terminalForwardMeta) error {
	sock, tok, err := dialTerminalSessionHost(home, sessionID)
	if err != nil {
		return err
	}
	switch f.Type {
	case terminalFrameInput:
		return forwardTerminalInputFrame(sock, tok, actor, f, meta)
	case terminalFrameResize:
		return forwardTerminalResizeFrame(sock, tok, actor, f, meta)
	}
	return nil
}

func dialTerminalHostSocket(sock string) (net.Conn, error) {
	var conn net.Conn
	var err error
	for attempt := 0; attempt < 40; attempt++ {
		conn, err = hookNetDial("unix", sock, 2*time.Second)
		if err == nil {
			return conn, nil
		}
		time.Sleep(50 * time.Millisecond)
	}
	return nil, err
}

func streamTerminalHostFrames(conn net.Conn, hub *terminalStreamHub) {
	buf := make([]byte, 64*1024)
	var pending []byte
	for {
		n, err := conn.Read(buf)
		if n > 0 {
			pending = append(pending, buf[:n]...)
			frames, rest, ferr := splitTerminalFrames(pending)
			pending = rest
			for _, f := range frames {
				hub.push(f)
			}
			if ferr != nil && len(frames) == 0 {
				pending = pending[:0]
			}
		}
		if err != nil {
			return
		}
	}
}

func terminalBridgeHostOutput(home, sessionID string, hub *terminalStreamHub) {
	defer hub.close()
	mgr, err := getTerminalManager(home)
	if err != nil {
		return
	}
	sock, tok, err := mgr.sessionIPC(sessionID)
	if err != nil {
		return
	}
	conn, err := dialTerminalHostSocket(sock)
	if conn == nil {
		msg := "session host unavailable"
		if err != nil {
			msg = err.Error()
		}
		hub.push(terminalFrame{Type: terminalFrameError, Payload: []byte(msg)})
		return
	}
	defer conn.Close()
	go func() {
		<-hub.done
		_ = conn.Close()
	}()
	req := terminalIPCRequest{Op: "attach_stream", Token: tok}
	if _, err := conn.Write(append(marshalTerminalJSON(req), '\n')); err != nil {
		return
	}
	streamTerminalHostFrames(conn, hub)
}

// --- P2P mTLS stream between cores ---

func dialTerminalP2PStream(home string, circle Circle, self DeviceIdentity, priv ed25519.PrivateKey, peerDeviceID, addr, sessionID string) (net.Conn, []byte, error) {
	cert, err := deviceCertificate(self, priv)
	if err != nil {
		return nil, nil, err
	}
	cfg := &tls.Config{
		MinVersion:         tls.VersionTLS13,
		Certificates:       []tls.Certificate{cert},
		ServerName:         peerDeviceID,
		InsecureSkipVerify: true,
		VerifyPeerCertificate: func(raw [][]byte, _ [][]*x509.Certificate) error {
			return verifyDeviceCert(raw, func(pub ed25519.PublicKey) error {
				member, ok := circle.activeMember(peerDeviceID)
				if !ok {
					return errors.New("peer not in circle")
				}
				if member.PublicKey != base64.StdEncoding.EncodeToString(pub) {
					return errors.New("peer cert mismatch")
				}
				return nil
			})
		},
	}
	dctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	conn, err := (&tls.Dialer{NetDialer: &net.Dialer{Timeout: 2 * time.Second}, Config: cfg}).DialContext(dctx, "tcp", addr)
	if err != nil {
		return nil, nil, err
	}
	key := deriveTerminalSessionKey(circle.ID, sessionID, []byte(circle.GroupKey))
	hello, _ := json.Marshal(map[string]string{"session_id": sessionID, "device_id": self.DeviceID})
	if _, err := conn.Write(append(hello, '\n')); err != nil {
		_ = conn.Close()
		return nil, nil, err
	}
	return conn, key, nil
}

func acceptTerminalP2PStream(home string, circle Circle, conn *tls.Conn, sessionID string) ([]byte, error) {
	key := deriveTerminalSessionKey(circle.ID, sessionID, []byte(circle.GroupKey))
	return key, nil
}
