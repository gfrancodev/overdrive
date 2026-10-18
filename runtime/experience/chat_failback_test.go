package main

import (
	"fmt"
	"testing"
	"time"
)

func withMeshClock(t *testing.T, now time.Time) *time.Time {
	t.Helper()
	clock := now
	meshNow = func() time.Time { return clock }
	t.Cleanup(func() { meshNow = time.Now })
	return &clock
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
	in.Chat = map[string]chatLinkState{"c1": {Connected: true, HostID: "dev_self", Hosted: true, Guests: 1}}
	if c = capability(t, deriveConnectionHealth(in), "chat"); c.State != healthConnected || c.Detail != "This device hosts the circle's chat for 1 other device." {
		t.Fatalf("with a guest: %+v", c)
	}
}
