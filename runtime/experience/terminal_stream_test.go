package main

import (
	"bytes"
	"io"
	"net"
	"testing"
	"time"
)

func TestDecodeConcatenatedTerminalFrames(t *testing.T) {
	a, err := encodeTerminalFrame(terminalFrame{Type: terminalFrameOutput, Sequence: 1, Payload: []byte("one")})
	if err != nil {
		t.Fatal(err)
	}
	b, err := encodeTerminalFrame(terminalFrame{Type: terminalFrameSnapshot, Sequence: 2, Payload: []byte(`{"cols":80}`)})
	if err != nil {
		t.Fatal(err)
	}
	joined := append(append([]byte{}, a...), b...)
	got, rest, err := splitTerminalFrames(joined)
	if err != nil {
		t.Fatal(err)
	}
	if len(rest) != 0 || len(got) != 2 {
		t.Fatalf("got=%d rest=%d", len(got), len(rest))
	}
	if got[0].Type != terminalFrameOutput || string(got[0].Payload) != "one" {
		t.Fatalf("first %+v", got[0])
	}
	if got[1].Type != terminalFrameSnapshot {
		t.Fatalf("second type %d", got[1].Type)
	}
}

func TestTerminalFrameRoundTrip(t *testing.T) {
	payload := []byte("hello terminal")
	orig := terminalFrame{Type: terminalFrameOutput, Sequence: 42, Payload: payload}
	b, err := encodeTerminalFrame(orig)
	if err != nil {
		t.Fatal(err)
	}
	got, err := decodeTerminalFrame(b)
	if err != nil {
		t.Fatal(err)
	}
	if got.Type != orig.Type || got.Sequence != orig.Sequence || !bytes.Equal(got.Payload, payload) {
		t.Fatalf("frame mismatch: %+v", got)
	}
}

func TestTerminalStreamTokenAuth(t *testing.T) {
	home := t.TempDir()
	closeTerminalDBs()
	mgr, err := newTerminalManager(home)
	if err != nil {
		t.Fatal(err)
	}
	sess, err := mgr.CreateSession(terminalCreateOpts{Name: "s", WorkingDirectory: home})
	if err != nil {
		t.Fatal(err)
	}
	tok, err := issueTerminalStreamToken(home, sess.ID, mgr.device.DeviceID, []TerminalCapability{TerminalCapObserve}, 5*time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if err := verifyTerminalStreamToken(home, sess.ID, tok, TerminalCapObserve); err != nil {
		t.Fatal(err)
	}
	if err := verifyTerminalStreamToken(home, sess.ID, tok, TerminalCapInput); err == nil {
		t.Fatal("expected input cap denied")
	}
}

func resetTerminalHubsForTest() {
	terminalHubMu.Lock()
	terminalHubs = map[string]map[string]map[*terminalStreamHub]struct{}{}
	terminalHubMu.Unlock()
}

func hubClosed(h *terminalStreamHub) bool {
	select {
	case <-h.done:
		return true
	default:
		return false
	}
}

// Two browser windows of the same device must not evict each other, or they reconnect forever.
func TestRegisterTerminalStreamKeepsSameDeviceHubs(t *testing.T) {
	resetTerminalHubsForTest()
	h1 := registerTerminalStream("sess1", "devA", nil)
	h2 := registerTerminalStream("sess1", "devA", nil)
	if hubClosed(h1) || hubClosed(h2) {
		t.Fatal("concurrent viewers of the same device must both stay open")
	}
	broadcastTerminalOutput("sess1", 1, []byte("hi"))
	for _, h := range []*terminalStreamHub{h1, h2} {
		select {
		case f := <-h.outCh:
			if string(f.Payload) != "hi" {
				t.Fatalf("payload = %q", f.Payload)
			}
		case <-time.After(time.Second):
			t.Fatal("every viewer must receive broadcast output")
		}
	}
	unregisterTerminalStreamHub("sess1", "devA", h1)
	unregisterTerminalStreamHub("sess1", "devA", h2)
}

func TestUnregisterTerminalHubKeepsSiblingHub(t *testing.T) {
	resetTerminalHubsForTest()
	h1 := registerTerminalStream("sess1", "devA", nil)
	h2 := registerTerminalStream("sess1", "devA", nil)
	unregisterTerminalStreamHub("sess1", "devA", h1)
	if !hubClosed(h1) {
		t.Fatal("own unregister must close hub")
	}
	if hubClosed(h2) {
		t.Fatal("unregistering one viewer must not close its sibling")
	}
	terminalHubMu.Lock()
	_, stillThere := terminalHubs["sess1"]["devA"][h2]
	_, gone := terminalHubs["sess1"]["devA"][h1]
	terminalHubMu.Unlock()
	if !stillThere || gone {
		t.Fatal("registry must drop only the unregistered hub")
	}
	unregisterTerminalStreamHub("sess1", "devA", h2)
	terminalHubMu.Lock()
	_, sessLeft := terminalHubs["sess1"]
	terminalHubMu.Unlock()
	if sessLeft {
		t.Fatal("empty session entry must be removed")
	}
}

func TestUnregisterTerminalStreamClosesEveryDeviceHub(t *testing.T) {
	resetTerminalHubsForTest()
	h1 := registerTerminalStream("sess1", "devA", nil)
	h2 := registerTerminalStream("sess1", "devA", nil)
	other := registerTerminalStream("sess1", "devB", nil)
	unregisterTerminalStream("sess1", "devA")
	if !hubClosed(h1) || !hubClosed(h2) {
		t.Fatal("revoking a device must close all of its viewers")
	}
	if hubClosed(other) {
		t.Fatal("other devices keep streaming")
	}
	unregisterTerminalStreamHub("sess1", "devB", other)
}

func bridgeTestSession(t *testing.T) (string, string, net.Conn) {
	t.Helper()
	home := t.TempDir()
	setupTestEnv(t, home)
	closeTerminalDBs()
	resetTerminalManagersForTests()
	sid := insertHTTPTestSession(t, home)
	client, server := net.Pipe()
	orig := hookNetDial
	hookNetDial = func(string, string, time.Duration) (net.Conn, error) { return client, nil }
	t.Cleanup(func() { hookNetDial = orig; _ = server.Close() })
	return home, sid, server
}

func TestTerminalBridgeClosesHubWhenHostStreamEnds(t *testing.T) {
	home, sid, server := bridgeTestSession(t)
	hub := newTerminalStreamHub("", sid, "dev", nil)
	go terminalBridgeHostOutput(home, sid, hub)
	go func() {
		buf := make([]byte, 4096)
		_, _ = server.Read(buf)
		raw, _ := encodeTerminalFrame(terminalFrame{Type: terminalFrameOutput, Sequence: 1, Payload: []byte("hi")})
		_, _ = server.Write(raw)
		_ = server.Close()
	}()
	select {
	case f := <-hub.outCh:
		if f.Type != terminalFrameOutput || string(f.Payload) != "hi" {
			t.Fatalf("frame %+v", f)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("expected output frame")
	}
	select {
	case <-hub.done:
	case <-time.After(2 * time.Second):
		t.Fatal("hub must close when the host stream ends (shell exit)")
	}
}

func TestTerminalBridgeReleasesHostConnWhenHubCloses(t *testing.T) {
	home, sid, server := bridgeTestSession(t)
	hub := newTerminalStreamHub("", sid, "dev", nil)
	go terminalBridgeHostOutput(home, sid, hub)
	buf := make([]byte, 4096)
	if _, err := server.Read(buf); err != nil {
		t.Fatal(err)
	}
	hub.close()
	_ = server.SetReadDeadline(time.Now().Add(2 * time.Second))
	if _, err := server.Read(buf); err != io.EOF {
		t.Fatalf("expected bridge to close host conn, got %v", err)
	}
}

type deadlineRecorder struct {
	net.Conn
	zero chan struct{}
}

func (d *deadlineRecorder) SetDeadline(t time.Time) error {
	if t.IsZero() {
		select {
		case d.zero <- struct{}{}:
		default:
		}
	}
	return d.Conn.SetDeadline(t)
}

func TestSessionHostStreamAttachClearsIPCDeadline(t *testing.T) {
	home := t.TempDir()
	setupTestEnv(t, home)
	closeTerminalDBs()
	sid := insertHTTPTestSession(t, home)
	store, err := newTerminalStore(home)
	if err != nil {
		t.Fatal(err)
	}
	rt := &sessionHostRuntime{home: home, sessionID: sid, store: store, ipcToken: "tok", stopCh: make(chan struct{})}
	client, server := net.Pipe()
	rec := &deadlineRecorder{Conn: server, zero: make(chan struct{}, 1)}
	go rt.handleIPCConn(rec)
	if _, err := client.Write([]byte(`{"op":"attach_stream","token":"tok"}` + "\n")); err != nil {
		t.Fatal(err)
	}
	select {
	case <-rec.zero:
	case <-time.After(2 * time.Second):
		t.Fatal("attach_stream must clear the 30-minute IPC deadline")
	}
	close(rt.stopCh)
	_ = client.Close()
}

func TestReplayRecentOutputReturnsNewestTailAscending(t *testing.T) {
	home := t.TempDir()
	setupTestEnv(t, home)
	closeTerminalDBs()
	sid := insertHTTPTestSession(t, home)
	store, err := newTerminalStore(home)
	if err != nil {
		t.Fatal(err)
	}
	for i := int64(1); i <= 12; i++ {
		kind := TerminalEventOutput
		if i%3 == 0 {
			kind = TerminalEventInput
		}
		if _, err := store.AppendEvent(SessionEvent{SessionID: sid, Sequence: i, Kind: kind, Raw: []byte{byte('a' + i)}}); err != nil {
			t.Fatal(err)
		}
	}
	got, err := store.ReplayRecentOutput(sid, 4)
	if err != nil {
		t.Fatal(err)
	}
	var seqs []int64
	for _, ev := range got {
		seqs = append(seqs, ev.Sequence)
	}
	want := []int64{7, 8, 10, 11}
	if len(seqs) != len(want) {
		t.Fatalf("seqs %v", seqs)
	}
	for i := range want {
		if seqs[i] != want[i] {
			t.Fatalf("seqs %v want %v", seqs, want)
		}
	}
}

func TestDeriveTerminalSessionKey(t *testing.T) {
	k1 := deriveTerminalSessionKey("circle_a", "ts_1", []byte("group-key-material"))
	k2 := deriveTerminalSessionKey("circle_a", "ts_2", []byte("group-key-material"))
	if len(k1) != 32 || bytes.Equal(k1, k2) {
		t.Fatalf("keys: len=%d equal=%v", len(k1), bytes.Equal(k1, k2))
	}
}
