package main

import (
	"bufio"
	"crypto/ed25519"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"strings"
	"sync"
	"time"
)

const defaultSharePort = 7741

type shareListener struct {
	ln   net.Listener
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
	shareListenerInst *shareListener
)

func startShareListener(home string, id DeviceIdentity, priv ed25519.PrivateKey) (*shareListener, error) {
	addr := shareListenAddr()
	ln, err := hookNetListen("tcp", addr)
	if err != nil {
		return shareListenerInst, err
	}
	sl := &shareListener{ln: ln, home: home, id: id, stop: make(chan struct{})}
	shareListenerInst = sl
	go sl.serve(priv)
	return sl, nil
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
	})
}

func (sl *shareListener) serve(priv ed25519.PrivateKey) {
	for {
		conn, err := sl.ln.Accept()
		if err != nil {
			select {
			case <-sl.stop:
				return
			default:
			}
			continue
		}
		go sl.handleConn(conn, priv)
	}
}

func (sl *shareListener) handleConn(conn net.Conn, priv ed25519.PrivateKey) {
	defer conn.Close()
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
	circle, err := loadCircle(sl.home, env.CircleID)
	if err != nil {
		return
	}
	plain, err := decryptBatch(circle.GroupKey, env.Ciphertext)
	if err != nil {
		return
	}
	if sl.tryHandleJoin(env, plain) {
		return
	}
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
		_ = sl.ln.Close()
	})
}

func circleForAny(home string) (Circle, bool) {
	circles, err := listCircles(home)
	if err != nil || len(circles) == 0 {
		return Circle{}, false
	}
	return circles[0], true
}

func fetchPeerSync(home string, endpoint string, circle Circle, id DeviceIdentity, priv ed25519.PrivateKey, repository string, since string) (SyncResponse, error) {
	if hookFetchPeerSync != nil {
		return hookFetchPeerSync(home, endpoint, circle, id, priv, repository, since)
	}
	conn, err := hookNetDial("tcp", endpoint, 5*time.Second)
	if err != nil {
		return SyncResponse{}, err
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(30 * time.Second))

	req := SyncRequest{
		DeviceID:   id.DeviceID,
		CircleID:   circle.ID,
		Repository: repository,
		Since:      since,
		Timestamp:  nowRFC3339(),
	}
	req, err = signSyncRequest(priv, req)
	if err != nil {
		return SyncResponse{}, err
	}
	payload, err := hookJSONMarshal(req)
	if err != nil {
		return SyncResponse{}, err
	}
	cipher, err := encryptBatch(circle.GroupKey, payload)
	if err != nil {
		return SyncResponse{}, err
	}
	out, _ := hookJSONMarshal(wireEnvelope{CircleID: circle.ID, Ciphertext: cipher})
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
	plain, err := decryptBatch(circle.GroupKey, env.Ciphertext)
	if err != nil {
		return SyncResponse{}, err
	}
	var resp SyncResponse
	if err := json.Unmarshal(plain, &resp); err != nil {
		return SyncResponse{}, err
	}
	member, ok := circle.activeMember(resp.DeviceID)
	if !ok {
		return SyncResponse{}, fmt.Errorf("unknown peer device")
	}
	pub, err := decodePublicKey(member.PublicKey)
	if err != nil || !verifySyncResponse(pub, resp) {
		return SyncResponse{}, fmt.Errorf("invalid peer signature")
	}
	return resp, nil
}
