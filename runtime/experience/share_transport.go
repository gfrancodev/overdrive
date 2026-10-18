package main

import (
	"bufio"
	"crypto/ed25519"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	defaultSharePort    = 7741
	peerSyncDialTimeout = 5 * time.Second
	peerSyncDeadline    = 30 * time.Second
	peerIDDialTimeout   = 400 * time.Millisecond
	peerIDDeadline      = 800 * time.Millisecond
)

type shareListener struct {
	mu sync.Mutex
	ln net.Listener
	// bind is the address the listener was asked for, with the port it got: a wildcard bind reports "[::]".
	bind string
	home string
	id   DeviceIdentity
	stop chan struct{}
	once sync.Once
}

func shareListenAddr() string {
	if v := strings.TrimSpace(os.Getenv("OVERDRIVE_SHARE_LISTEN")); v != "" {
		return v
	}
	return fmt.Sprintf("127.0.0.1:%d", defaultSharePort)
}

var (
	shareListenerOnce sync.Once
	shareListenerMu   sync.Mutex
	shareListenerInst *shareListener
)

func currentShareListener() *shareListener {
	shareListenerMu.Lock()
	defer shareListenerMu.Unlock()
	return shareListenerInst
}

func startShareListener(home string, id DeviceIdentity, priv ed25519.PrivateKey) (*shareListener, error) {
	loadMeshState(home)
	addr := effectiveShareListen(home)
	ln, err := hookNetListen("tcp", addr)
	if err != nil {
		return currentShareListener(), err
	}
	bind := boundAddr(addr, ln)
	if addr != shareListenAddr() {
		setListenOverride(bind)
	}
	sl := &shareListener{ln: ln, bind: bind, home: home, id: id, stop: make(chan struct{})}
	shareListenerMu.Lock()
	shareListenerInst = sl
	shareListenerMu.Unlock()
	go sl.serve(priv)
	return sl, nil
}

// boundAddr is addr with the port the listener actually got.
func boundAddr(addr string, ln net.Listener) string {
	host, _, err := net.SplitHostPort(normalizeListen(addr))
	tcp, ok := ln.Addr().(*net.TCPAddr)
	if err != nil || !ok {
		return ln.Addr().String()
	}
	return net.JoinHostPort(host, strconv.Itoa(tcp.Port))
}

func (sl *shareListener) bindAddr() string {
	if sl == nil {
		return ""
	}
	sl.mu.Lock()
	defer sl.mu.Unlock()
	if sl.bind == "" && sl.ln != nil {
		return sl.ln.Addr().String()
	}
	return sl.bind
}

func (sl *shareListener) listener() net.Listener {
	if sl == nil {
		return nil
	}
	sl.mu.Lock()
	defer sl.mu.Unlock()
	return sl.ln
}

// rebind moves the listener to addr. The old socket closes first, because a wildcard bind cannot share a
// port with a loopback one; when the new bind fails the old address is taken back.
func (sl *shareListener) rebind(addr string) error {
	sl.mu.Lock()
	defer sl.mu.Unlock()
	old := sl.ln
	if old == nil {
		return errors.New("listener is not running")
	}
	prev := sl.bind
	if prev == "" {
		prev = old.Addr().String()
	}
	if prev == addr {
		return nil
	}
	_ = old.Close()
	ln, err := hookNetListen("tcp", addr)
	if err != nil {
		if back, backErr := hookNetListen("tcp", prev); backErr == nil {
			sl.ln = back
		}
		return err
	}
	sl.ln, sl.bind = ln, boundAddr(addr, ln)
	return nil
}

func ensureShareListener(home string) {
	shareListenerOnce.Do(func() {
		if strings.TrimSpace(os.Getenv("OVERDRIVE_SHARE_LISTEN")) == "" {
			return
		}
		id, priv, err := loadOrCreateIdentity(home)
		if err != nil {
			return
		}
		_, _ = startShareListener(home, id, priv)
		startShareDashboard(home)
	})
}

func (sl *shareListener) serve(priv ed25519.PrivateKey) {
	for {
		ln := sl.listener()
		conn, err := ln.Accept()
		if err != nil {
			select {
			case <-sl.stop:
				return
			default:
			}
			if errors.Is(err, net.ErrClosed) && sl.listener() == ln {
				// Closed without a replacement: a rebind that could not take any address back.
				time.Sleep(200 * time.Millisecond)
			}
			continue
		}
		go sl.handleConn(conn, priv)
	}
}

func (sl *shareListener) handleConn(conn net.Conn, priv ed25519.PrivateKey) {
	defer conn.Close()
	if !meshAllowsSync() {
		return
	}
	_ = conn.SetDeadline(time.Now().Add(30 * time.Second))
	reader := bufio.NewReader(conn)
	line, err := reader.ReadString('\n')
	if err != nil {
		return
	}
	line = strings.TrimSpace(line)
	var env wireEnvelope
	if err := json.Unmarshal([]byte(line), &env); err != nil {
		return
	}
	if env.Kind == pairKind {
		sl.handlePair(conn, priv, env.Plain)
		return
	}
	circle, err := loadCircle(sl.home, env.CircleID)
	if err != nil {
		return
	}
	if env.Kind == keysKind {
		sl.handleKeys(conn, circle, priv, env.Plain)
		return
	}
	plain, err := decryptCircle(circle, env.Ciphertext)
	if err != nil {
		return
	}
	if sl.applyJoin(conn, env, plain) {
		return
	}
	if env.Kind == "catalog" {
		sl.handleCatalog(conn, circle, priv, plain)
		return
	}
	if env.Kind == terminalPeerKind {
		sl.handleTerminalPeer(conn, reader, circle, plain)
		return
	}
	if env.Kind == rosterKind {
		sl.handleRoster(conn, circle, priv, plain)
		return
	}
	sl.handleSyncWire(conn, circle, priv, plain)
}

func (sl *shareListener) handleSyncWire(conn net.Conn, circle Circle, priv ed25519.PrivateKey, plain []byte) {
	var req SyncRequest
	if err := json.Unmarshal(plain, &req); err != nil {
		return
	}
	member, ok := circle.activeMember(req.DeviceID)
	if !ok {
		return
	}
	pub, err := decodePublicKey(member.PublicKey)
	if err != nil || !verifySyncRequest(pub, req) {
		return
	}
	if updated, err := updateCircle(sl.home, circle.ID, func(c Circle) (Circle, error) {
		c = rememberSyncPeer(c, req, conn.RemoteAddr())
		c = stampSelfMember(c, sl.id.DeviceID, sl.selfEndpoint())
		return vouchMembers(refreshPeerEndpoints(c, sl.id.DeviceID), sl.id, priv), nil
	}); err == nil {
		circle = updated
	}
	var e *Engine
	if hookShareNewEngine != nil {
		e, err = hookShareNewEngine(sl.home)
	} else {
		e, err = newEngine(sl.home)
	}
	if err != nil {
		return
	}
	defer e.Close()
	_ = e.importPushedPackets(circle, req)
	resp, err := e.buildSyncResponse(sl.home, circle, sl.id, priv, req)
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
	out, _ := hookJSONMarshal(wireEnvelope{CircleID: circle.ID, Ciphertext: cipher})
	_, _ = conn.Write(append(out, '\n'))
}

func (sl *shareListener) Close() {
	sl.once.Do(func() {
		close(sl.stop)
		if ln := sl.listener(); ln != nil {
			_ = ln.Close()
		}
	})
}

func circleForAny(home string) (Circle, bool) {
	circles, err := listCircles(home)
	if err != nil || len(circles) == 0 {
		return Circle{}, false
	}
	return circles[0], true
}

type peerSyncCall struct {
	Home        string
	Endpoint    string
	Circle      Circle
	Identity    DeviceIdentity
	Priv        ed25519.PrivateKey
	Repository  string
	Since       string
	IDs         []string
	Packets     []SharedPacket
	DialTimeout time.Duration
	Deadline    time.Duration
}

func fetchPeerSync(home string, endpoint string, circle Circle, id DeviceIdentity, priv ed25519.PrivateKey, repository string, since string) (SyncResponse, error) {
	if hookFetchPeerSync != nil {
		return hookFetchPeerSync(home, endpoint, circle, id, priv, repository, since)
	}
	return doFetchPeerSyncTimed(peerSyncCall{
		Home: home, Endpoint: endpoint, Circle: circle, Identity: id, Priv: priv,
		Repository: repository, Since: since, DialTimeout: peerSyncDialTimeout, Deadline: peerSyncDeadline,
	})
}

func fetchPeerSyncIDs(home string, endpoint string, circle Circle, id DeviceIdentity, priv ed25519.PrivateKey, repository string, ids []string) (SyncResponse, error) {
	return doFetchPeerSyncTimed(peerSyncCall{
		Home: home, Endpoint: endpoint, Circle: circle, Identity: id, Priv: priv,
		Repository: repository, IDs: ids, DialTimeout: peerIDDialTimeout, Deadline: peerIDDeadline,
	})
}

func fetchPeerSyncPackets(call peerSyncCall) (SyncResponse, error) {
	call.DialTimeout = peerSyncDialTimeout
	call.Deadline = peerSyncDeadline
	return doFetchPeerSyncTimed(call)
}

func doFetchPeerSyncTimed(call peerSyncCall) (SyncResponse, error) {
	conn, err := hookNetDial("tcp", call.Endpoint, call.DialTimeout)
	if err != nil {
		return SyncResponse{}, err
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(call.Deadline))

	req := SyncRequest{
		DeviceID:   call.Identity.DeviceID,
		CircleID:   call.Circle.ID,
		Repository: call.Repository,
		Since:      call.Since,
		IDs:        call.IDs,
		Packets:    call.Packets,
		Timestamp:  nowRFC3339(),
		ListenAddr: advertisedListenAddr(),
		Hostname:   localHostname(),
	}
	req, err = signSyncRequest(call.Priv, req)
	if err != nil {
		return SyncResponse{}, err
	}
	payload, err := hookJSONMarshal(req)
	if err != nil {
		return SyncResponse{}, err
	}
	cipher, err := encryptBatch(call.Circle.GroupKey, payload)
	if err != nil {
		return SyncResponse{}, err
	}
	out, _ := hookJSONMarshal(wireEnvelope{CircleID: call.Circle.ID, Ciphertext: cipher})
	if _, err := conn.Write(append(out, '\n')); err != nil {
		return SyncResponse{}, err
	}
	reader := bufio.NewReader(conn)
	line, err := reader.ReadString('\n')
	if err != nil {
		return SyncResponse{}, err
	}
	var env wireEnvelope
	if err := json.Unmarshal([]byte(strings.TrimSpace(line)), &env); err != nil {
		return SyncResponse{}, err
	}
	plain, err := decryptCircle(call.Circle, env.Ciphertext)
	if err != nil {
		return SyncResponse{}, err
	}
	var resp SyncResponse
	if err := json.Unmarshal(plain, &resp); err != nil {
		return SyncResponse{}, err
	}
	member, ok := call.Circle.activeMember(resp.DeviceID)
	if !ok {
		return SyncResponse{}, fmt.Errorf("unknown peer device")
	}
	pub, err := decodePublicKey(member.PublicKey)
	if err != nil || !verifySyncResponse(pub, resp) {
		return SyncResponse{}, fmt.Errorf("invalid peer signature")
	}
	if len(resp.Members) > 0 {
		_ = mergePeerRoster(call.Home, call.Circle.ID, resp.Members)
	}
	refreshPeerRoster(call.Home, call.Endpoint, call.Circle, call.Identity, call.Priv)
	return resp, nil
}
