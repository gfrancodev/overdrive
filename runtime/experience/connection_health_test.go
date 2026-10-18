package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	pahomqtt "github.com/eclipse/paho.golang/paho"
)

// healthCircle is a circle seen from dev_self with the given peers, as endpoint by device id.
func healthCircle(folders []string, peers map[string]string) Circle {
	c := Circle{ID: "c1", Name: "Squad", CreatorDeviceID: "dev_self", AllowedFolders: folders,
		Members: []CircleMember{{DeviceID: "dev_self", Hostname: "studio"}}}
	for _, id := range []string{"dev_b", "dev_c"} {
		if ep, ok := peers[id]; ok {
			c.Members = append(c.Members, CircleMember{DeviceID: id, Hostname: map[string]string{"dev_b": "laptop", "dev_c": "old-mac"}[id], Endpoint: ep})
		}
	}
	return c
}

func lanNetwork() networkView {
	return networkView{LANAccess: true, Listen: "0.0.0.0:7741", Address: "192.168.1.2:7741", LANIP: "192.168.1.2", Listening: true}
}

func localNetwork() networkView {
	return networkView{LANAccess: false, Listen: "127.0.0.1:7741", Address: "127.0.0.1:7741", LANIP: "192.168.1.2", Listening: true}
}

func healthyInputs() healthInputs {
	return healthInputs{
		SelfID:  "dev_self",
		Circles: []Circle{healthCircle([]string{"/work/api"}, map[string]string{"dev_b": "192.168.1.9:7741"})},
		Network: lanNetwork(),
		Probes:  map[string]peerProbe{"192.168.1.9:7741": {Reach: reachOK, RTT: 3 * time.Millisecond}},
		Chat:    map[string]chatLinkState{"c1": {Connected: true, HostID: "dev_self"}},
	}
}

func capability(t *testing.T, h connectionHealth, id string) capabilityHealth {
	t.Helper()
	for _, c := range h.Capabilities {
		if c.ID == id {
			return c
		}
	}
	t.Fatalf("no %s capability in %+v", id, h.Capabilities)
	return capabilityHealth{}
}

func TestHealthWhenEverythingWorks(t *testing.T) {
	h := deriveConnectionHealth(healthyInputs())
	if h.State != healthConnected {
		t.Fatalf("overall %s: %+v", h.State, h.Capabilities)
	}
	want := map[string]string{
		"chat":     "This device hosts the circle's chat.",
		"calls":    "Calls work between devices on this network. Calls across networks need a TURN server.",
		"memory":   "Experience syncs with 1 device.",
		"terminal": "Terminals can be shared with 1 device.",
	}
	for id, detail := range want {
		c := capability(t, h, id)
		if c.State != healthConnected || c.Detail != detail || c.Fix != "" || c.Action != "" {
			t.Errorf("%s: %+v", id, c)
		}
	}
	if len(h.Peers) != 1 || h.Peers[0].Name != "laptop" || h.Peers[0].Reach != reachOK || h.Peers[0].RTTMillis != 3 {
		t.Fatalf("peers: %+v", h.Peers)
	}

	in := healthyInputs()
	in.TURN = true
	in.Chat["c1"] = chatLinkState{Connected: true, HostID: "dev_b"}
	h = deriveConnectionHealth(in)
	if c := capability(t, h, "chat"); c.Detail != "Chat runs through laptop." {
		t.Fatalf("chat through a peer: %+v", c)
	}
	if c := capability(t, h, "calls"); c.Detail != "Calls are ready, on this network and across networks." {
		t.Fatalf("calls with TURN: %+v", c)
	}
}

func TestHealthWhenTheMeshIsPaused(t *testing.T) {
	in := healthyInputs()
	in.Paused = true
	h := deriveConnectionHealth(in)
	if h.State != healthOffline || h.Mesh != meshPaused {
		t.Fatalf("overall %s mesh %s", h.State, h.Mesh)
	}
	for _, c := range h.Capabilities {
		if c.State != healthOffline || c.Cause != "mesh_paused" || c.Action != "resume_mesh" || c.Fix != "Resume the mesh." {
			t.Errorf("%s: %+v", c.ID, c)
		}
	}
	if c := capability(t, h, "terminal"); !strings.Contains(c.Detail, "Terminals on this device still work") {
		t.Fatalf("terminal: %+v", c)
	}
}

func TestHealthBeforeACircleAndBeforeAPeer(t *testing.T) {
	h := deriveConnectionHealth(healthInputs{SelfID: "dev_self", Network: localNetwork()})
	for _, c := range h.Capabilities {
		if c.State != healthOffline || c.Cause != "no_circle" || c.Action != "open_setup" {
			t.Errorf("no circle %s: %+v", c.ID, c)
		}
	}
	h = deriveConnectionHealth(healthInputs{SelfID: "dev_self", Network: localNetwork(),
		Circles: []Circle{healthCircle([]string{"/work/api"}, nil)}})
	for _, c := range h.Capabilities {
		if c.State != healthOffline || c.Cause != "no_peers" || c.Detail != "No other device is in the circle yet." {
			t.Errorf("alone %s: %+v", c.ID, c)
		}
	}
	// A revoked member is not a peer.
	alone := healthCircle([]string{"/work/api"}, map[string]string{"dev_b": "192.168.1.9:7741"})
	alone.Members[1].Revoked = true
	if h = deriveConnectionHealth(healthInputs{SelfID: "dev_self", Network: lanNetwork(), Circles: []Circle{alone}}); capability(t, h, "memory").Cause != "no_peers" || len(h.Peers) != 0 {
		t.Fatalf("revoked member counted: %+v", h)
	}
}

func TestHealthNamesTheDeviceThatDoesNotAnswer(t *testing.T) {
	in := healthyInputs()
	in.Circles = []Circle{healthCircle([]string{"/work/api"}, map[string]string{"dev_b": "192.168.1.9:7741", "dev_c": "192.168.1.10:7741"})}
	in.Probes["192.168.1.10:7741"] = peerProbe{Reach: reachTimeout}
	h := deriveConnectionHealth(in)
	if h.State != healthDegraded {
		t.Fatalf("overall %s", h.State)
	}
	mem := capability(t, h, "memory")
	if mem.State != healthDegraded || mem.Cause != "peers_unreachable" || mem.Detail != "old-mac doesn't answer; experience syncs with the others." {
		t.Fatalf("memory: %+v", mem)
	}
	if mem.Fix != "Check that old-mac is on and on this network, and that no firewall blocks port 7741." {
		t.Fatalf("fix: %q", mem.Fix)
	}
	if term := capability(t, h, "terminal"); term.State != healthDegraded || term.Detail != "old-mac doesn't answer; terminals from the others still open." {
		t.Fatalf("terminal: %+v", term)
	}

	in.Probes["192.168.1.9:7741"] = peerProbe{Reach: reachRefused}
	h = deriveConnectionHealth(in)
	mem = capability(t, h, "memory")
	if mem.State != healthOffline || mem.Detail != "No device in the circle answers, so experience stays on this device for now." {
		t.Fatalf("none answer: %+v", mem)
	}
	if mem.Fix != "Overdrive on laptop isn't accepting connections. Check that it's running and allows devices on this network." {
		t.Fatalf("refused fix: %q", mem.Fix)
	}
	if term := capability(t, h, "terminal"); term.Detail != "No device in the circle answers. Terminals on this device still work." {
		t.Fatalf("terminal: %+v", term)
	}
}

func TestHealthExplainsEachWayAPeerCanBeOutOfReach(t *testing.T) {
	cases := []struct {
		probe     peerProbe
		endpoint  string
		localOnly bool
		fix       string
	}{
		{peerProbe{Reach: reachRefused}, "192.168.1.9:7741", true, "laptop accepts connections only from itself. On laptop, open Connection and allow devices on this network."},
		{peerProbe{Reach: reachUnreachable}, "192.168.1.9:7741", false, "This device has no route to laptop. Check the Wi-Fi or cable, or the VPN."},
		{peerProbe{}, "", false, "laptop hasn't shared its address yet. It does the next time it connects to the circle."},
		{peerProbe{}, "127.0.0.1:7741", false, "laptop registered an address that works only on its own computer. On laptop, allow devices on this network."},
	}
	for _, c := range cases {
		in := healthyInputs()
		in.Circles = []Circle{healthCircle([]string{"/work/api"}, map[string]string{"dev_b": c.endpoint})}
		in.Probes = map[string]peerProbe{}
		if c.endpoint != "" && c.probe.Reach != "" {
			in.Probes[c.endpoint] = c.probe
		}
		if c.localOnly {
			in.Nearby = map[string]discoveredPeer{"dev_b": {DeviceID: "dev_b", LocalOnly: true}}
		}
		h := deriveConnectionHealth(in)
		if got := capability(t, h, "memory").Fix; got != c.fix {
			t.Errorf("%+v: fix %q", c, got)
		}
	}
}

func TestHealthIsBlockedWhileOnlyThisComputerCanConnect(t *testing.T) {
	in := healthyInputs()
	in.Network = localNetwork()
	h := deriveConnectionHealth(in)
	if h.State != healthBlocked {
		t.Fatalf("overall %s", h.State)
	}
	for _, id := range []string{"memory", "terminal"} {
		c := capability(t, h, id)
		if c.State != healthBlocked || c.Cause != "local_only" || c.Action != "allow_network" || c.Fix != "Allow devices on this network to connect." {
			t.Errorf("%s: %+v", id, c)
		}
	}
	if c := capability(t, h, "chat"); c.State != healthConnected {
		t.Fatalf("chat does not depend on the listener: %+v", c)
	}
	// Several cores on one computer talk over loopback; nothing is blocked.
	in.Circles = []Circle{healthCircle([]string{"/work/api"}, map[string]string{"dev_b": "127.0.0.1:7841"})}
	in.Probes = map[string]peerProbe{"127.0.0.1:7841": {Reach: reachOK}}
	if h = deriveConnectionHealth(in); h.State != healthConnected {
		t.Fatalf("same computer: %s %+v", h.State, h.Capabilities)
	}
}

func TestHealthNeedsASharedFolderForExperience(t *testing.T) {
	in := healthyInputs()
	in.Circles = []Circle{healthCircle(nil, map[string]string{"dev_b": "192.168.1.9:7741"})}
	h := deriveConnectionHealth(in)
	mem := capability(t, h, "memory")
	if mem.State != healthBlocked || mem.Cause != "no_folder" || mem.Action != "share_folder" {
		t.Fatalf("memory: %+v", mem)
	}
	if term := capability(t, h, "terminal"); term.State != healthConnected {
		t.Fatalf("terminals need no folder: %+v", term)
	}
}

func TestHealthFollowsTheChatLink(t *testing.T) {
	in := healthyInputs()
	in.Chat = map[string]chatLinkState{"c1": {Recovering: true}}
	h := deriveConnectionHealth(in)
	if c := capability(t, h, "chat"); c.State != healthReconnecting || c.Fix != "Messages you send wait and go out once it's back." {
		t.Fatalf("recovering: %+v", c)
	}
	if c := capability(t, h, "calls"); c.State != healthReconnecting || c.Detail != "Calls wait for the circle's chat to reconnect." {
		t.Fatalf("calls while recovering: %+v", c)
	}

	in.Circles[0].CreatorDeviceID = "dev_b"
	in.Chat = map[string]chatLinkState{"c1": {LastError: "dial tcp 192.168.1.9:41000: connect: connection refused"}}
	h = deriveConnectionHealth(in)
	c := capability(t, h, "chat")
	if c.State != healthOffline || c.Cause != "chat_unreachable" || c.Detail != "Chat can't reach laptop, the device that hosts it." {
		t.Fatalf("unreachable: %+v", c)
	}
	if !strings.HasPrefix(c.Fix, "Messages you send wait and go out once it's back. Check that laptop is on") {
		t.Fatalf("fix: %q", c.Fix)
	}

	in.Chat = map[string]chatLinkState{}
	if c = capability(t, deriveConnectionHealth(in), "chat"); c.State != healthReconnecting || c.Cause != "connecting" {
		t.Fatalf("not linked yet: %+v", c)
	}
}

func TestOverallHealthIsTheWorstCapability(t *testing.T) {
	in := healthyInputs()
	in.Network = localNetwork()
	in.Chat = map[string]chatLinkState{"c1": {LastError: "boom"}}
	if h := deriveConnectionHealth(in); h.State != healthBlocked {
		t.Fatalf("blocked beats offline: %s", h.State)
	}
}

func TestProbeSaysHowAPeerAnswers(t *testing.T) {
	resetPeerProbes()
	t.Cleanup(resetPeerProbes)
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			_ = conn.Close()
		}
	}()
	closed := fmt.Sprintf("127.0.0.1:%d", freeTCPPort(t))
	got := probePeerEndpoints([]string{ln.Addr().String(), closed}, time.Now())
	if got[ln.Addr().String()].Reach != reachOK || got[closed].Reach != reachRefused {
		t.Fatalf("probes: %+v", got)
	}

	var dials atomic.Int32
	hookNetDial = func(network, address string, timeout time.Duration) (net.Conn, error) {
		dials.Add(1)
		switch address {
		case "10.0.0.1:7741":
			return nil, &net.OpError{Op: "dial", Err: timeoutErr{}}
		default:
			return nil, errors.New("connect: no route to host")
		}
	}
	t.Cleanup(func() { hookNetDial = defaultNetDial })
	now := time.Now()
	got = probePeerEndpoints([]string{"10.0.0.1:7741", "10.0.0.2:7741"}, now)
	if got["10.0.0.1:7741"].Reach != reachTimeout || got["10.0.0.2:7741"].Reach != reachUnreachable {
		t.Fatalf("failures: %+v", got)
	}
	probePeerEndpoints([]string{"10.0.0.1:7741"}, now.Add(peerProbeTTL/2))
	if dials.Load() != 2 {
		t.Fatalf("a fresh probe was redone: %d dials", dials.Load())
	}
	probePeerEndpoints([]string{"10.0.0.1:7741"}, now.Add(2*peerProbeTTL))
	if dials.Load() != 3 {
		t.Fatalf("a stale probe was not redone: %d dials", dials.Load())
	}
}

type timeoutErr struct{}

func (timeoutErr) Error() string   { return "i/o timeout" }
func (timeoutErr) Timeout() bool   { return true }
func (timeoutErr) Temporary() bool { return true }

func TestRefusalsAreRecognisedOnEveryOS(t *testing.T) {
	windows := errors.New("dial tcp 192.168.1.9:7741: connectex: No connection could be made because the target machine actively refused it.")
	if classifyDialError(windows) != reachRefused {
		t.Fatal("windows refusal")
	}
}

func TestChatLinkStateRemembersTheLastFailure(t *testing.T) {
	resetCoreLifecycle()
	t.Cleanup(resetCoreLifecycle)
	noteChatLinkResult("c1", errors.New("no reachable broker"))
	if st := chatLinkStates()["c1"]; st.LastError != "no reachable broker" || st.Connected {
		t.Fatalf("failure: %+v", st)
	}
	meshRT.mu.Lock()
	meshRT.links["c1"] = &circleLink{client: &pahomqtt.Client{}, hostID: "dev_b"}
	meshRT.recovering["c2"] = true
	meshRT.mu.Unlock()
	states := chatLinkStates()
	if st := states["c1"]; !st.Connected || st.HostID != "dev_b" || st.LastError != "" {
		t.Fatalf("a live link hides an old failure: %+v", st)
	}
	if !states["c2"].Recovering {
		t.Fatalf("recovering: %+v", states)
	}
	noteChatLinkResult("c1", nil)
	meshRT.mu.Lock()
	meshRT.links = map[string]*circleLink{}
	meshRT.mu.Unlock()
	if st := chatLinkStates()["c1"]; st.LastError != "" {
		t.Fatalf("success clears the failure: %+v", st)
	}
}

func TestConnectionHealthAPI(t *testing.T) {
	home := t.TempDir()
	setupTestEnv(t, home)
	t.Cleanup(resetPeerProbes)
	id, priv, err := loadOrCreateIdentity(home)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := createCircle(home, "Squad", id, priv); err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	registerConnectionHealthRoutes(mux, home)

	req := httptest.NewRequest(http.MethodGet, "/api/connections/health", nil)
	req.RemoteAddr = "127.0.0.1:50000"
	req.Host = "127.0.0.1:7742"
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK || rr.Header().Get("Cache-Control") != cacheNoStore {
		t.Fatalf("%d %s", rr.Code, rr.Body.String())
	}
	var h connectionHealth
	if err := json.Unmarshal(rr.Body.Bytes(), &h); err != nil {
		t.Fatal(err)
	}
	if h.State != healthOffline || len(h.Capabilities) != 4 || capability(t, h, "chat").Cause != "no_peers" || h.CheckedAt == "" {
		t.Fatalf("%s: %+v", id.DeviceID, h)
	}

	req = httptest.NewRequest(http.MethodGet, "/api/connections/health", nil)
	req.RemoteAddr = "192.168.1.20:50000"
	rr = httptest.NewRecorder()
	mux.ServeHTTP(rr, req)
	if rr.Code != http.StatusForbidden {
		t.Fatalf("remote: %d", rr.Code)
	}
	req = httptest.NewRequest(http.MethodPost, "/api/connections/health", nil)
	req.RemoteAddr = "127.0.0.1:50000"
	req.Host = "127.0.0.1:7742"
	rr = httptest.NewRecorder()
	mux.ServeHTTP(rr, req)
	if rr.Code != http.StatusMethodNotAllowed {
		t.Fatalf("post: %d", rr.Code)
	}
}
