package main

import (
	"fmt"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	pahomqtt "github.com/eclipse/paho.golang/paho"
)

func withMeshClock(t *testing.T, now time.Time) *time.Time {
	t.Helper()
	clock := now
	meshNow = func() time.Time { return clock }
	t.Cleanup(func() { meshNow = time.Now })
	return &clock
}

// immediateFailover elects another broker on the first failure, for tests about what happens after a failover.
func immediateFailover(t *testing.T) {
	t.Helper()
	prev := brokerFailoverAfter
	brokerFailoverAfter = 0
	t.Cleanup(func() { brokerFailoverAfter = prev })
}

func TestAnUnreachableBrokerIsOnlyAvoidedForAWhile(t *testing.T) {
	meshHome(t)
	clock := withMeshClock(t, time.Now())
	markBrokerGone("c1", "dev_creator")
	if !brokerGoneSet("c1")["dev_creator"] {
		t.Fatal("a broker that just failed should be avoided")
	}
	*clock = clock.Add(brokerGoneTTL + time.Second)
	if brokerGoneSet("c1")["dev_creator"] {
		t.Fatal("the creator stays avoided for good")
	}
}

// A member that joined through someone other than the creator could not reach the creator's broker yet, hosted
// the chat itself, and stayed alone there: the others never heard it. Once the creator answers, it moves back.
func TestChatMovesBackToTheCreatorOnceItAnswers(t *testing.T) {
	home := meshHome(t)
	immediateFailover(t)
	creator, creatorPriv := testDevice(t)
	self, selfPriv := testDevice(t)
	writeIdentityFile(t, home, self)
	circle := saveTestCircle(t, home, testCircle(creator, self))
	clock := withMeshClock(t, time.Now())

	setMeshEndpoint(creator.DeviceID, fmt.Sprintf("127.0.0.1:%d", freeTCPPort(t)))
	host, err := recoverCircleMesh(home, circle, self, selfPriv)
	if err != nil || host != self.DeviceID {
		t.Fatalf("with the creator down this device hosts: %s %v", host, err)
	}

	remote, err := listenMemberBroker(home, circle, creator, creatorPriv)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(remote.close)
	setMeshEndpoint(creator.DeviceID, remote.addr)

	chatFailBack(home)
	if st := chatLinkStates()[circle.ID]; st.HostID != self.DeviceID {
		t.Fatalf("moved before the creator stopped being avoided: %+v", st)
	}

	*clock = clock.Add(brokerGoneTTL + time.Second)
	chatFailBack(home)
	if st := chatLinkStates()[circle.ID]; !st.Connected || st.HostID != creator.DeviceID {
		t.Fatalf("still apart from the creator: %+v", st)
	}
}

func TestChatStaysWhenTheCreatorIsStillDown(t *testing.T) {
	home := meshHome(t)
	immediateFailover(t)
	creator, _ := testDevice(t)
	self, selfPriv := testDevice(t)
	writeIdentityFile(t, home, self)
	circle := saveTestCircle(t, home, testCircle(creator, self))
	clock := withMeshClock(t, time.Now())
	setMeshEndpoint(creator.DeviceID, fmt.Sprintf("127.0.0.1:%d", freeTCPPort(t)))
	if _, err := recoverCircleMesh(home, circle, self, selfPriv); err != nil {
		t.Fatal(err)
	}
	*clock = clock.Add(brokerGoneTTL + time.Second)
	chatFailBack(home)
	st := chatLinkStates()[circle.ID]
	if !st.Connected || st.HostID != self.DeviceID {
		t.Fatalf("a failed move must keep the working link: %+v", st)
	}
	if !brokerGoneSet(circle.ID)[creator.DeviceID] {
		t.Fatal("a failed move should avoid the creator again for a while")
	}
}

func TestHostingTheChatAloneIsNotConnected(t *testing.T) {
	in := healthyInputs()
	in.Chat = map[string]chatLinkState{"c1": {Connected: true, HostID: "dev_self", Hosted: true, Guests: 0}}
	h := deriveConnectionHealth(in)
	c := capability(t, h, "chat")
	if c.State != healthDegraded || c.Cause != "chat_alone" || c.Detail != "This device hosts the circle's chat, but no other device is on it." {
		t.Fatalf("alone: %+v", c)
	}
	// The chat is up, so calls are not "out of reach": they just have no one to ring yet.
	if calls := capability(t, h, "calls"); calls.Detail != "Calls start over the circle's chat, and no other device is on it yet." {
		t.Fatalf("calls while the chat is alone: %+v", calls)
	}
	in.Chat = map[string]chatLinkState{"c1": {Connected: true, HostID: "dev_self", Hosted: true, Guests: 1}}
	if c = capability(t, deriveConnectionHealth(in), "chat"); c.State != healthConnected || c.Detail != "This device hosts the circle's chat for 1 other device." {
		t.Fatalf("with a guest: %+v", c)
	}
}

// A message sent while this device hosts the circle's chat alone reached no one and was gone. It now waits in
// the outbox, reported as queued, and goes out once another device subscribes.
func TestChatWaitsForAnotherDeviceInsteadOfPublishingIntoAnEmptyChat(t *testing.T) {
	home := meshHome(t)
	self, selfPriv := testDevice(t)
	other, otherPriv := testDevice(t)
	writeIdentityFile(t, home, self)
	if _, err := loadOrCreateLocalUser(home); err != nil {
		t.Fatal(err)
	}
	circle := saveTestCircle(t, home, testCircle(self, other))
	if host, err := recoverCircleMesh(home, circle, self, selfPriv); err != nil || host != self.DeviceID {
		t.Fatalf("%s %v", host, err)
	}
	env, queued, err := postLocalChat(home, chatPostRequest{MeshID: circle.ID, ConversationID: circleGroupConversationID(), Type: "text", Text: "anyone?"})
	if err != nil || !queued {
		t.Fatalf("a message nobody can receive must be reported queued: %v %v", queued, err)
	}
	var got atomic.Bool
	setMeshDeliveryHook(func(p *pahomqtt.Publish) {
		if p != nil && strings.Contains(string(p.Payload), env.MessageID) {
			got.Store(true)
		}
	})
	addr, _ := meshEndpoint(self.DeviceID)
	guest, err := dialMeshClient(home, circle, other, otherPriv, self.DeviceID, addr, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { closePaho(guest) })
	waitUntil(t, 4*time.Second, got.Load)
	waitUntil(t, 4*time.Second, func() bool {
		left, err := loadOutbox(home, circle.ID)
		return err == nil && len(left) == 0
	})
	if _, queued, err := postLocalChat(home, chatPostRequest{MeshID: circle.ID, ConversationID: circleGroupConversationID(), Type: "text", Text: "now"}); err != nil || queued {
		t.Fatalf("with another device on the chat a message goes out at once: %v %v", queued, err)
	}
}

// After the device that hosts the chat restarts, its broker listens on another port. A member that only knew
// the old one asked nobody and hosted a chat of its own; it now asks the device for the new port.
func TestARestartedChatHostIsFoundAtItsNewPort(t *testing.T) {
	homeHost := meshHome(t)
	sl, endpoint := startShareListenerForTest(t, homeHost)
	defer sl.Close()
	host, hostPriv, err := loadOrCreateIdentity(homeHost)
	if err != nil {
		t.Fatal(err)
	}
	peer, peerPriv := testDevice(t)
	circle := testCircle(host, peer)
	for i := range circle.Members {
		if circle.Members[i].DeviceID == host.DeviceID {
			circle.Members[i].Endpoint = endpoint
		}
	}
	saveTestCircle(t, homeHost, circle)
	meshRT.mu.Lock()
	meshRT.initLocked()
	meshRT.armed = true
	meshRT.home = homeHost
	meshRT.mu.Unlock()
	brokerAddr, err := ensureLocalBroker(homeHost, circle, host, hostPriv)
	if err != nil {
		t.Fatal(err)
	}
	homePeer := t.TempDir()
	writeIdentityFile(t, homePeer, peer)
	saveTestCircle(t, homePeer, circle)
	setMeshEndpoint(host.DeviceID, fmt.Sprintf("127.0.0.1:%d", freeTCPPort(t)))

	if linked, err := recoverCircleMeshOnce(homePeer, circle, peer, peerPriv, host.DeviceID); err != nil || linked != host.DeviceID {
		t.Fatalf("the member must find the host at its new port: %q %v", linked, err)
	}
	if got, _ := meshEndpoint(host.DeviceID); got != brokerAddr {
		t.Fatalf("the new port must replace the old one: %q, want %q", got, brokerAddr)
	}
	if st := chatLinkStates()[circle.ID]; !st.Connected || st.HostID != host.DeviceID {
		t.Fatalf("%+v", st)
	}
}

func TestAChatAnnouncedAtANewAddressIsTriedAgainAtOnce(t *testing.T) {
	home := meshHome(t)
	self, _ := testDevice(t)
	writeIdentityFile(t, home, self)
	meshRT.mu.Lock()
	meshRT.initLocked()
	meshRT.armed = true
	meshRT.mu.Unlock()
	markBrokerGone("c1", "dev_0011223344556677")
	setMeshEndpoint("dev_0011223344556677", "192.168.1.9:41000")
	if err := noteDiscoveredPeer(home, discoveredPeer{DeviceID: "dev_0011223344556677", Host: "192.168.1.9", Port: 7741, MQTTPort: 41000}); err != nil {
		t.Fatal(err)
	}
	if !brokerGoneSet("c1")["dev_0011223344556677"] {
		t.Fatal("the same address announced again changes nothing")
	}
	if err := noteDiscoveredPeer(home, discoveredPeer{DeviceID: "dev_0011223344556677", Host: "192.168.1.9", Port: 7741, MQTTPort: 42000}); err != nil {
		t.Fatal(err)
	}
	if brokerGoneSet("c1")["dev_0011223344556677"] {
		t.Fatal("a chat announced at a new address is back: it must be tried again now")
	}
}

// A broker that just failed may be restarting. Electing another at once split the circle's chat for as long
// as it took the others to come back; the core now stays without a link, chat queued, until the broker has
// kept failing for a while.
func TestABrokerThatJustFailedIsNotPassedOverYet(t *testing.T) {
	home := meshHome(t)
	creator, _ := testDevice(t)
	self, selfPriv := testDevice(t)
	writeIdentityFile(t, home, self)
	if _, err := loadOrCreateLocalUser(home); err != nil {
		t.Fatal(err)
	}
	circle := saveTestCircle(t, home, testCircle(creator, self))
	clock := withMeshClock(t, time.Now())
	setMeshEndpoint(creator.DeviceID, fmt.Sprintf("127.0.0.1:%d", freeTCPPort(t)))
	if _, err := recoverCircleMesh(home, circle, self, selfPriv); err == nil {
		t.Fatal("the creator is down: no link yet")
	}
	if st := chatLinkStates()[circle.ID]; st.Connected {
		t.Fatalf("one failure must not make this device host the chat: %+v", st)
	}
	if _, queued, err := postLocalChat(home, chatPostRequest{MeshID: circle.ID, ConversationID: circleGroupConversationID(), Type: "text", Text: "wait"}); err != nil || !queued {
		t.Fatalf("chat waits for the link: %v %v", queued, err)
	}
	*clock = clock.Add(brokerFailoverAfter / 2)
	if _, err := recoverCircleMesh(home, circle, self, selfPriv); err == nil || chatLinkStates()[circle.ID].Connected {
		t.Fatal("still inside the grace period")
	}
	*clock = clock.Add(brokerFailoverAfter)
	if host, err := recoverCircleMesh(home, circle, self, selfPriv); err != nil || host != self.DeviceID {
		t.Fatalf("a broker that keeps failing is passed over: %s %v", host, err)
	}
}

func TestTheChatLoopLinksCirclesThatHaveNoLink(t *testing.T) {
	home := meshHome(t)
	self, _ := testDevice(t)
	other, _ := testDevice(t)
	writeIdentityFile(t, home, self)
	circle := saveTestCircle(t, home, testCircle(self, other))
	if chatLinkStates()[circle.ID].Connected {
		t.Fatal("setup: no link yet")
	}
	chatFailBack(home)
	if st := chatLinkStates()[circle.ID]; !st.Connected || st.HostID != self.DeviceID {
		t.Fatalf("the creator must start hosting without waiting for an event: %+v", st)
	}
}

func TestRemovedDevicesAreDroppedFromTheChatThisDeviceHosts(t *testing.T) {
	home := meshHome(t)
	self, selfPriv := testDevice(t)
	other, otherPriv := testDevice(t)
	writeIdentityFile(t, home, self)
	circle := saveTestCircle(t, home, testCircle(self, other))
	if _, err := recoverCircleMesh(home, circle, self, selfPriv); err != nil {
		t.Fatal(err)
	}
	addr, _ := meshEndpoint(self.DeviceID)
	guest, err := dialMeshClient(home, circle, other, otherPriv, self.DeviceID, addr, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { closePaho(guest) })
	waitUntil(t, 3*time.Second, func() bool { return chatLinkStates()[circle.ID].Guests == 1 })
	dropRemovedChatClients(circle)
	// A disconnect takes effect a moment later: give it the time before checking nobody was dropped.
	time.Sleep(300 * time.Millisecond)
	if chatLinkStates()[circle.ID].Guests != 1 {
		t.Fatal("an active member stays on")
	}
	circle.Members[1].Revoked = true
	saveTestCircle(t, home, circle)
	chatFailBack(home)
	waitUntil(t, 3*time.Second, func() bool { return chatLinkStates()[circle.ID].Guests == 0 })
}

// Only a device whose own broker carries its link holds chat back when no one else is on it. A member linked
// to another device's broker sends at once, even if a broker it hosted earlier is still up and empty.
func TestOnlyTheDeviceThatHostsTheChatHoldsMessagesBack(t *testing.T) {
	home := meshHome(t)
	self, selfPriv := testDevice(t)
	other, _ := testDevice(t)
	writeIdentityFile(t, home, self)
	circle := saveTestCircle(t, home, testCircle(self, other))
	if hostingAlone(circle.ID) {
		t.Fatal("no link: nothing to hold back")
	}
	meshRT.mu.Lock()
	meshRT.initLocked()
	meshRT.links[circle.ID] = &circleLink{hostID: other.DeviceID, addr: "192.168.1.9:41000"}
	meshRT.mu.Unlock()
	if hostingAlone(circle.ID) {
		t.Fatal("a link without a broker of this device's is not hosted here")
	}
	broker, err := listenMemberBroker(home, circle, self, selfPriv)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(broker.close)
	meshRT.mu.Lock()
	meshRT.links[circle.ID].broker = broker
	meshRT.mu.Unlock()
	if hostingAlone(circle.ID) {
		t.Fatal("linked to another device's broker, this device's empty one does not matter")
	}
	meshRT.mu.Lock()
	meshRT.links[circle.ID].addr, meshRT.links[circle.ID].hostID = broker.addr, self.DeviceID
	meshRT.mu.Unlock()
	if !hostingAlone(circle.ID) {
		t.Fatal("linked to its own empty broker, this device hosts the chat alone")
	}
}

func TestRemovingADeviceFromTheDashboardDropsItFromTheChat(t *testing.T) {
	home := meshHome(t)
	self, selfPriv := testDevice(t)
	other, otherPriv := testDevice(t)
	writeIdentityFile(t, home, self)
	circle := saveTestCircle(t, home, testCircle(self, other))
	if _, err := recoverCircleMesh(home, circle, self, selfPriv); err != nil {
		t.Fatal(err)
	}
	addr, _ := meshEndpoint(self.DeviceID)
	guest, err := dialMeshClient(home, circle, other, otherPriv, self.DeviceID, addr, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { closePaho(guest) })
	waitUntil(t, 3*time.Second, func() bool { return chatLinkStates()[circle.ID].Guests == 1 })
	circle.Members[1].Revoked = true
	saveTestCircle(t, home, circle)
	forgetRevokedDevices(home, circle.ID, []string{other.DeviceID})
	waitUntil(t, 3*time.Second, func() bool { return chatLinkStates()[circle.ID].Guests == 0 })
}
