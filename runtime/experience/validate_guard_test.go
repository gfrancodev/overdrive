package main

import (
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	pahomqtt "github.com/eclipse/paho.golang/paho"
)

// Tests for 2.3 behavior the mutation gate found unchecked.

func TestHealthNameListsShortenPastThree(t *testing.T) {
	cases := map[string][]string{
		"":                   nil,
		"a":                  {"a"},
		"a and b":            {"a", "b"},
		"a, b and c":         {"a", "b", "c"},
		"a, b, c and 1 more": {"a", "b", "c", "d"},
		"a, b, c and 2 more": {"a", "b", "c", "d", "e"},
	}
	for want, names := range cases {
		if got := healthNameList(names); got != want {
			t.Errorf("%v: %q, want %q", names, got, want)
		}
	}
}

func TestOwnAddressIsLoopbackOrThisDevicesLANAddressOnItsPort(t *testing.T) {
	network := networkView{Listen: "0.0.0.0:7741", LANIP: "192.168.1.2"}
	for endpoint, want := range map[string]bool{
		"127.0.0.1:7741":   true,
		"192.168.1.2:7741": true,
		"192.168.1.3:7741": false,
		"127.0.0.1:7841":   false,
		"not an address":   false,
	} {
		if got := isOwnAddress(endpoint, network); got != want {
			t.Errorf("%s: %v", endpoint, got)
		}
	}
	if isOwnAddress("192.168.1.2:7741", networkView{Listen: "0.0.0.0:7741"}) {
		t.Fatal("without a LAN address only loopback leads back here")
	}
}

func TestAPeerInTwoCirclesIsListedOnce(t *testing.T) {
	in := healthyInputs()
	second := healthCircle([]string{"/work/web"}, map[string]string{"dev_b": "192.168.1.9:7741"})
	second.ID = "c2"
	in.Circles = append(in.Circles, second)
	if peers := deriveConnectionHealth(in).Peers; len(peers) != 1 {
		t.Fatalf("peers: %+v", peers)
	}
}

func TestAProbeWithoutAnAnswerIsNotChecked(t *testing.T) {
	in := healthyInputs()
	in.Probes = map[string]peerProbe{"192.168.1.9:7741": {}}
	if p := deriveConnectionHealth(in).Peers[0]; p.Reach != reachUnknown {
		t.Fatalf("reach %q", p.Reach)
	}
}

func TestATimeoutNamesThePeersPort(t *testing.T) {
	if got := peerFix(peerHealth{Name: "laptop", Reach: reachTimeout, Endpoint: "192.168.1.9:9100"}); !strings.HasSuffix(got, "blocks port 9100.") {
		t.Fatalf("%q", got)
	}
	if got := peerFix(peerHealth{Name: "laptop", Reach: reachTimeout, Endpoint: "laptop.local"}); !strings.HasSuffix(got, "blocks port 7741.") {
		t.Fatalf("an endpoint without a port falls back to the default: %q", got)
	}
}

func TestChatHealthIsTheWorstCircle(t *testing.T) {
	in := healthyInputs()
	second := healthCircle([]string{"/work/web"}, map[string]string{"dev_b": "192.168.1.9:7741"})
	second.ID = "c2"
	in.Circles = append(in.Circles, second)
	in.Chat = map[string]chatLinkState{"c1": {LastError: "refused"}, "c2": {Connected: true, HostID: "dev_self"}}
	if c := capability(t, deriveConnectionHealth(in), "chat"); c.State != healthOffline {
		t.Fatalf("a working circle must not hide one that is offline: %+v", c)
	}
	in.Chat = map[string]chatLinkState{"c1": {Recovering: true}, "c2": {}}
	if c := capability(t, deriveConnectionHealth(in), "chat"); c.Detail != "Reconnecting to the circle's chat…" {
		t.Fatalf("between equally bad circles the first one speaks: %+v", c)
	}
}

func TestCirclesWithoutOtherMembersDoNotCountForChat(t *testing.T) {
	in := healthyInputs()
	alone := healthCircle([]string{"/work/web"}, map[string]string{"dev_b": "192.168.1.9:7741"})
	alone.ID = "c2"
	alone.Members[1].Revoked = true
	alone.Members = append(alone.Members, CircleMember{})
	in.Circles = append(in.Circles, alone)
	if c := capability(t, deriveConnectionHealth(in), "chat"); c.State != healthConnected {
		t.Fatalf("a circle with no one else has no chat to wait for: %+v", c)
	}
}

func TestChatThatCannotHostItselfNamesTheCreator(t *testing.T) {
	in := healthyInputs()
	in.Circles[0].CreatorDeviceID = "dev_b"
	in.Chat = map[string]chatLinkState{"c1": {HostID: "dev_self", LastError: "listen: address in use"}}
	if c := capability(t, deriveConnectionHealth(in), "chat"); c.Detail != "Chat can't reach laptop, the device that hosts it." {
		t.Fatalf("%+v", c)
	}
}

func TestAProbeIsRedoneOnceItIsTenSecondsOld(t *testing.T) {
	resetPeerProbes()
	t.Cleanup(resetPeerProbes)
	var dials atomic.Int32
	hookNetDial = func(network, address string, timeout time.Duration) (net.Conn, error) {
		dials.Add(1)
		return nil, errors.New("connect: connection refused")
	}
	t.Cleanup(func() { hookNetDial = defaultNetDial })
	now := time.Now()
	probePeerEndpoints([]string{"10.0.0.1:7741"}, now)
	probePeerEndpoints([]string{"10.0.0.1:7741"}, now.Add(peerProbeTTL))
	if dials.Load() != 2 {
		t.Fatalf("a probe exactly ten seconds old is redone: %d dials", dials.Load())
	}
}

func TestAGoneMarkEndsExactlyAtItsTTL(t *testing.T) {
	meshHome(t)
	clock := withMeshClock(t, time.Now())
	markBrokerGone("c1", "dev_creator")
	*clock = clock.Add(brokerGoneTTL)
	if brokerGoneSet("c1")["dev_creator"] {
		t.Fatal("a mark is over once its time is up")
	}
}

func TestClearMeshClientDropsOnlyTheCurrentClient(t *testing.T) {
	meshHome(t)
	current, stale := &pahomqtt.Client{}, &pahomqtt.Client{}
	meshRT.mu.Lock()
	meshRT.initLocked()
	meshRT.links["c1"] = &circleLink{client: current}
	meshRT.mu.Unlock()
	if clearMeshClient("c1", stale) {
		t.Fatal("a replaced client must not drop its successor")
	}
	if !chatLinkStates()["c1"].Connected {
		t.Fatal("the current client is still linked")
	}
	if !clearMeshClient("c1", current) || chatLinkStates()["c1"].Connected {
		t.Fatal("the current client is dropped when it fails")
	}
	if clearMeshClient("c9", current) {
		t.Fatal("no link, nothing to drop")
	}
}

func TestTheChatHostCountsTheOtherDevicesOnIt(t *testing.T) {
	home := meshHome(t)
	creator, creatorPriv := testDevice(t)
	self, selfPriv := testDevice(t)
	writeIdentityFile(t, home, self)
	circle := saveTestCircle(t, home, testCircle(creator, self))
	if (*circleBroker)(nil).guests(self.DeviceID) != 0 || (&circleBroker{}).guests(self.DeviceID) != 0 {
		t.Fatal("no broker, no guests")
	}
	broker, err := listenMemberBroker(home, circle, self, selfPriv)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(broker.close)
	own, err := dialMeshClient(home, circle, self, selfPriv, self.DeviceID, broker.addr, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { closePaho(own) })
	meshRT.mu.Lock()
	meshRT.initLocked()
	meshRT.links[circle.ID] = &circleLink{broker: broker, client: own, hostID: self.DeviceID, addr: broker.addr}
	meshRT.mu.Unlock()
	if st := chatLinkStates()[circle.ID]; !st.Hosted || st.Guests != 0 {
		t.Fatalf("alone: %+v", st)
	}
	guest, err := dialMeshClient(home, circle, creator, creatorPriv, self.DeviceID, broker.addr, nil)
	if err != nil {
		t.Fatal(err)
	}
	waitUntil(t, 3*time.Second, func() bool { return chatLinkStates()[circle.ID].Guests == 1 })
	closePaho(guest)
	waitUntil(t, 3*time.Second, func() bool { return chatLinkStates()[circle.ID].Guests == 0 })

	meshRT.mu.Lock()
	meshRT.links[circle.ID].addr = "127.0.0.1:1"
	meshRT.mu.Unlock()
	if st := chatLinkStates()[circle.ID]; st.Hosted {
		t.Fatalf("a link to another broker is not hosted here even if this device runs one: %+v", st)
	}
}

func TestChatFailBackLeavesLinksThatAreDownOrRecovering(t *testing.T) {
	home := meshHome(t)
	creator, creatorPriv := testDevice(t)
	self, _ := testDevice(t)
	writeIdentityFile(t, home, self)
	circle := saveTestCircle(t, home, testCircle(creator, self))
	remote, err := listenMemberBroker(home, circle, creator, creatorPriv)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(remote.close)
	setMeshEndpoint(creator.DeviceID, remote.addr)
	stale := &pahomqtt.Client{}
	t.Cleanup(func() {
		// The stand-in client cannot be disconnected: drop it before the mesh shuts down.
		meshRT.mu.Lock()
		meshRT.links = map[string]*circleLink{}
		meshRT.recovering = map[string]bool{}
		meshRT.mu.Unlock()
	})
	for _, link := range []struct {
		name       string
		client     *pahomqtt.Client
		recovering bool
	}{{"down", nil, false}, {"recovering", stale, true}} {
		meshRT.mu.Lock()
		meshRT.initLocked()
		meshRT.links[circle.ID] = &circleLink{client: link.client, hostID: self.DeviceID}
		meshRT.recovering[circle.ID] = link.recovering
		meshRT.mu.Unlock()
		chatFailBack(home)
		if st := chatLinkStates()[circle.ID]; st.HostID != self.DeviceID {
			t.Fatalf("%s: recovery owns this link, fail-back moved it: %+v", link.name, st)
		}
	}
}

func TestAFolderThatBecameAFileIsNotListedAsThere(t *testing.T) {
	home := t.TempDir()
	setupTestEnv(t, home)
	id, priv, _ := loadOrCreateIdentity(home)
	circle, _ := createCircle(home, "team", id, priv)
	dir := filepath.Join(t.TempDir(), "proj")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := addAllowedFolder(home, circle.ID, dir, id); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(dir); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dir, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	out := folderAPI(t, home, http.MethodGet, "/api/circles/"+circle.ID+"/folders", nil, http.StatusOK)
	if len(out.Folders) != 1 || out.Folders[0].Exists {
		t.Fatalf("%+v", out.Folders)
	}
}

func TestFolderSuggestionsStopAtEight(t *testing.T) {
	home := t.TempDir()
	setupTestEnv(t, home)
	id, priv, _ := loadOrCreateIdentity(home)
	circle, _ := createCircle(home, "team", id, priv)
	e, err := newEngine(home)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < maxFolderSuggestions+2; i++ {
		dir := filepath.Join(t.TempDir(), fmt.Sprintf("p%d", i))
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		m := Memory{ID: fmt.Sprintf("m%d", i), Kind: "lesson", Status: "active", Scope: "repository", ScopeID: "github.com/acme/r",
			Subject: "s", Content: "c", CreatedAt: nowRFC3339(), UpdatedAt: nowRFC3339(), SourceFolder: dir, Origin: "local"}
		if err := e.upsertMemory(m); err != nil {
			t.Fatal(err)
		}
	}
	e.Close()
	if got := folderSuggestions(home, circle); len(got) != maxFolderSuggestions {
		t.Fatalf("%d suggestions", len(got))
	}
}

func TestRemovalOnlyTakesDeviceIDs(t *testing.T) {
	f := newRevokeFixture(t)
	for _, method := range []string{http.MethodGet, http.MethodPost} {
		var body any
		if method == http.MethodPost {
			body = map[string]bool{"with_invitees": false}
		}
		for _, target := range []string{"circle_team", "not-a-device"} {
			revokeCall(t, f.home, method, "/api/circles/"+f.circle.ID+"/members/"+target+"/revoke", body, http.StatusNotFound)
		}
	}
	done := revokeCall(t, f.home, http.MethodPost, "/api/circles/"+f.circle.ID+"/members/"+f.d.id.DeviceID+"/revoke",
		map[string]bool{"with_invitees": false}, http.StatusOK)
	if done.CutOff == nil {
		t.Fatal("cut_off is an empty list, not null, when nobody is left behind")
	}
}

func TestStopSharingMatchesTheFolderHoweverItIsWritten(t *testing.T) {
	home := t.TempDir()
	setupTestEnv(t, home)
	id, priv, _ := loadOrCreateIdentity(home)
	circle, _ := createCircle(home, "team", id, priv)
	dir := filepath.Join(t.TempDir(), "proj")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(t.TempDir(), "link")
	if err := os.Symlink(dir, link); err != nil {
		t.Fatal(err)
	}
	for _, spelling := range []string{dir + "/", link} {
		if _, err := addAllowedFolder(home, circle.ID, dir, id); err != nil {
			t.Fatal(err)
		}
		got, err := removeAllowedFolder(home, circle.ID, spelling, id)
		if err != nil || len(got.AllowedFolders) != 0 {
			t.Fatalf("%s: %v %v", spelling, got.AllowedFolders, err)
		}
	}
	if _, err := addAllowedFolder(home, circle.ID, dir, id); err != nil {
		t.Fatal(err)
	}
	if _, err := removeAllowedFolder(home, circle.ID, filepath.Join(t.TempDir(), "other"), id); !errors.Is(err, errFolderNotShared) {
		t.Fatalf("another folder: %v", err)
	}
	if got, _ := loadCircle(home, circle.ID); len(got.AllowedFolders) != 1 {
		t.Fatalf("a failed removal keeps the folders: %v", got.AllowedFolders)
	}
}

func TestFolderRemoveNeedsTheFolder(t *testing.T) {
	assertCLIRejects(t, []string{"share", "circle", "folder-remove", "--circle", "circle_x"}, "--folder")
}

func TestAFailedRebindKeepsServing(t *testing.T) {
	home := t.TempDir()
	setupTestEnv(t, home)
	sl := startTestShareListener(t, home)
	real := hookNetListen
	hookNetListen = func(network, addr string) (net.Listener, error) {
		if strings.HasPrefix(addr, "0.0.0.0:") {
			return nil, errors.New("bind: permission denied")
		}
		return real(network, addr)
	}
	t.Cleanup(func() { hookNetListen = real })
	if _, err := applyNetworkAccess(home, true); err == nil {
		t.Fatal("the rebind should fail")
	}
	_, joiner, dev := pairingParty(t)
	clearPairings(t, home)
	m := signedPairMessage(t, dev, joiner, pairMessage{Op: "hello", SessionID: "ps_after", Commit: "c", Timestamp: nowRFC3339()})
	if _, err := sendPairMessage(sl.bindAddr(), m); err != nil {
		t.Fatalf("the Core must still answer on its old address: %v", err)
	}
}

func TestABindAddressFallsBackToTheSocket(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	sl := &shareListener{ln: ln}
	t.Cleanup(func() { _ = sl.listener().Close() })
	if sl.bindAddr() != ln.Addr().String() {
		t.Fatalf("%q", sl.bindAddr())
	}
	if err := sl.rebind(ln.Addr().String()); err != nil || sl.listener() != ln {
		t.Fatal("rebinding to the address it already has is a no-op")
	}
}

func TestEveryStartedSessionHostIsReported(t *testing.T) {
	var seen atomic.Int32
	prev := hookSessionHostStarted
	hookSessionHostStarted = func(pid int) {
		seen.Add(1)
		if prev != nil {
			prev(pid)
		}
	}
	t.Cleanup(func() { hookSessionHostStarted = prev })
	home := shortTempHome(t)
	closeTerminalDBs()
	if err := hookMkdirAll(shareDir(home), 0o700); err != nil {
		t.Fatal(err)
	}
	mgr, err := newTerminalManager(home)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := mgr.CreateSession(terminalCreateOpts{Name: "t", WorkingDirectory: home, Cols: 80, Rows: 24}); err != nil {
		t.Fatal(err)
	}
	if seen.Load() != 1 {
		t.Fatalf("%d hosts reported", seen.Load())
	}
}

func TestDiagnosticsSayWhenThePreviousKeyIsStillAccepted(t *testing.T) {
	home := t.TempDir()
	setupTestEnv(t, home)
	t.Cleanup(resetPeerProbes)
	id, priv, _ := loadOrCreateIdentity(home)
	if _, err := createCircle(home, "team", id, priv); err != nil {
		t.Fatal(err)
	}
	report, err := buildDiagnostics(home)
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Circles) != 1 || report.Circles[0].PreviousKeyOpen {
		t.Fatalf("a circle on its first key has no previous key open: %+v", report.Circles)
	}
}

func TestAJoinerThatCancelsTellsTheMember(t *testing.T) {
	fastPairingPolls(t)
	setupTestEnv(t, t.TempDir())
	member := startRosterCore(t, "member")
	joiner := startRosterCore(t, "joiner")
	circle, err := createCircle(member.home, "team", member.id, member.priv)
	if err != nil {
		t.Fatal(err)
	}
	started, err := startPairing(joiner.home, member.endpoint, member.id.DeviceID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := cancelPairing(joiner.home, started.ID); err != nil {
		t.Fatal(err)
	}
	if v := memberSession(t, member.home, started.ID); v.State != string(pairingRejected) {
		t.Fatalf("the member must learn the joiner cancelled: %+v", v)
	}
	if _, err := confirmPairing(member.home, started.ID, circle.ID); err == nil {
		t.Fatal("a cancelled pairing cannot be confirmed")
	}

	again, err := startPairing(joiner.home, member.endpoint, member.id.DeviceID)
	if err != nil {
		t.Fatal(err)
	}
	if v, err := confirmPairing(member.home, again.ID, circle.ID); err != nil || !v.Confirmed {
		t.Fatalf("the member's own confirmation shows: %+v %v", v, err)
	}
}
