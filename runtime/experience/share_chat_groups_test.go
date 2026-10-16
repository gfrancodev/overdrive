package main

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

func TestGroupChatTopicAndPublishPlan(t *testing.T) {
	env := chatEnvelope{MeshID: "circle_mesh01", ConversationID: circleGroupConversationID()}
	topic, qos, err := chatPublishPlan(env)
	if err != nil {
		t.Fatal(err)
	}
	want := meshPrefix("circle_mesh01") + "chat/group/" + circleGroupConversationID()
	if topic != want || qos != 1 {
		t.Fatalf("topic=%q qos=%d", topic, qos)
	}
}

func TestChatGroupsHTTPCreateAndList(t *testing.T) {
	home := meshHome(t)
	self, _ := testDevice(t)
	peer, _ := testDevice(t)
	writeIdentityFile(t, home, self)
	circle := saveTestCircle(t, home, testCircle(self, peer))

	list := meshHTTP(t, home, http.MethodGet, "/api/chat/groups?mesh_id="+circle.ID, "127.0.0.1:9", nil)
	if list.Code != http.StatusOK {
		t.Fatalf("list %d %s", list.Code, list.Body.String())
	}
	var listed struct {
		Groups []chatGroupRecord `json:"groups"`
	}
	if err := json.Unmarshal(list.Body.Bytes(), &listed); err != nil {
		t.Fatal(err)
	}
	if len(listed.Groups) == 0 || listed.Groups[0].ConversationID != circleGroupConversationID() {
		t.Fatalf("circle channel missing %+v", listed.Groups)
	}

	body, _ := json.Marshal(map[string]any{
		"mesh_id":           circle.ID,
		"name":              "Squad",
		"member_device_ids": []string{peer.DeviceID},
	})
	created := meshHTTP(t, home, http.MethodPost, "/api/chat/groups", "127.0.0.1:9", body)
	if created.Code != http.StatusOK {
		t.Fatalf("create %d %s", created.Code, created.Body.String())
	}
	var out struct {
		Group chatGroupRecord `json:"group"`
	}
	if err := json.Unmarshal(created.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if out.Group.Name != "Squad" || len(out.Group.MemberDeviceIDs) < 2 {
		t.Fatalf("group %+v", out.Group)
	}
	if !strings.HasPrefix(out.Group.ConversationID, "group:grp_") {
		t.Fatalf("conv id %q", out.Group.ConversationID)
	}

	postBody, _ := json.Marshal(chatPostRequest{
		MeshID: circle.ID, ConversationID: out.Group.ConversationID, Type: "text", Text: "hello group",
	})
	posted := meshHTTP(t, home, http.MethodPost, "/api/chat/messages", "127.0.0.1:9", postBody)
	if posted.Code != http.StatusOK {
		t.Fatalf("post %d %s", posted.Code, posted.Body.String())
	}

	convs := meshHTTP(t, home, http.MethodGet, "/api/chat/conversations?mesh_id="+circle.ID, "127.0.0.1:9", nil)
	if convs.Code != http.StatusOK || !strings.Contains(convs.Body.String(), out.Group.ConversationID) {
		t.Fatalf("conversations %s", convs.Body.String())
	}
}

func TestCallSignalMultiTargetAndConversationID(t *testing.T) {
	home := meshHome(t)
	t.Cleanup(resetCallStateForTests)
	self, _ := testDevice(t)
	peerA, _ := testDevice(t)
	peerB, _ := testDevice(t)
	writeIdentityFile(t, home, self)
	circle := saveTestCircle(t, home, testCircle(self, peerA, peerB))

	payload, _ := json.Marshal(map[string]any{"mode": "voice"})
	body, _ := json.Marshal(callSignalRequest{
		MeshID:          circle.ID,
		TargetDeviceIDs: []string{peerA.DeviceID, peerB.DeviceID},
		Kind:            "invite",
		ConversationID:  circleGroupConversationID(),
		Payload:         payload,
	})
	rec := meshHTTP(t, home, http.MethodPost, "/api/call/signal", "127.0.0.1:9", body)
	if rec.Code != http.StatusOK {
		t.Fatalf("signal %d %s", rec.Code, rec.Body.String())
	}
	events, _, _ := listCallEventsSince(circle.ID, 0)
	if len(events) < 2 {
		t.Fatalf("expected 2 invite events, got %d", len(events))
	}
	var invitePayload map[string]any
	if err := json.Unmarshal(events[0].Payload, &invitePayload); err != nil {
		t.Fatal(err)
	}
	if invitePayload["conversation_id"] != circleGroupConversationID() {
		t.Fatalf("payload %+v", invitePayload)
	}
}

func TestTerminalSessionMessageType(t *testing.T) {
	home := meshHome(t)
	self, _ := testDevice(t)
	writeIdentityFile(t, home, self)
	circle := saveTestCircle(t, home, testCircle(self))

	body, _ := json.Marshal(chatPostRequest{
		MeshID: circle.ID, ConversationID: circleGroupConversationID(), Type: "terminal_session",
		Title: "Build", Summary: "running tests",
		SessionID: "ts_share", Host: "dev_host", Project: "/work/overdrive", State: "RUNNING",
	})
	rec := meshHTTP(t, home, http.MethodPost, "/api/chat/messages", "127.0.0.1:9", body)
	if rec.Code != http.StatusOK {
		t.Fatalf("post %d %s", rec.Code, rec.Body.String())
	}
	var posted struct {
		Message chatEnvelope `json:"message"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &posted); err != nil {
		t.Fatal(err)
	}
	var payload chatPayload
	if err := json.Unmarshal(posted.Message.Payload, &payload); err != nil {
		t.Fatal(err)
	}
	if payload.SessionID != "ts_share" || payload.Host != "dev_host" || payload.Project != "/work/overdrive" || payload.State != "RUNNING" {
		t.Fatalf("terminal payload %+v", payload)
	}
}

func TestCircleChannelMembersFollowCircleJoins(t *testing.T) {
	home := meshHome(t)
	self, _ := testDevice(t)
	joiner, _ := testDevice(t)
	writeIdentityFile(t, home, self)
	circle := saveTestCircle(t, home, testCircle(self))

	groups, err := listChatGroups(home, circle.ID)
	if err != nil || len(groups) != 1 || len(groups[0].MemberDeviceIDs) != 1 {
		t.Fatalf("solo circle channel %+v %v", groups, err)
	}

	saveTestCircle(t, home, testCircle(self, joiner))
	convs := meshHTTP(t, home, http.MethodGet, "/api/chat/conversations?mesh_id="+circle.ID, "127.0.0.1:9", nil)
	if convs.Code != http.StatusOK {
		t.Fatalf("conversations %d %s", convs.Code, convs.Body.String())
	}
	var out struct {
		Conversations []conversationView `json:"conversations"`
	}
	if err := json.Unmarshal(convs.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	var channel *conversationView
	for i := range out.Conversations {
		if out.Conversations[i].ConversationID == circleGroupConversationID() {
			channel = &out.Conversations[i]
		}
	}
	if channel == nil || len(channel.MemberDeviceIDs) != 2 || !strings.Contains(strings.Join(channel.MemberDeviceIDs, ","), joiner.DeviceID) {
		t.Fatalf("circle channel did not pick up the joiner: %+v", channel)
	}

	revoked := testCircle(self, joiner)
	revoked.Members[1].Revoked = true
	saveTestCircle(t, home, revoked)
	groups, err = listChatGroups(home, circle.ID)
	if err != nil || len(groups[0].MemberDeviceIDs) != 1 {
		t.Fatalf("revoked member still in circle channel %+v %v", groups, err)
	}
}

func TestChatGroupErrorsUseClientStatusCodes(t *testing.T) {
	home := meshHome(t)
	self, _ := testDevice(t)
	peer, _ := testDevice(t)
	writeIdentityFile(t, home, self)
	circle := saveTestCircle(t, home, testCircle(self, peer))

	bad, _ := json.Marshal(map[string]any{"mesh_id": circle.ID, "name": "x", "member_device_ids": []string{"dev_not_in_circle"}})
	if rec := meshHTTP(t, home, http.MethodPost, "/api/chat/groups", "127.0.0.1:9", bad); rec.Code != http.StatusBadRequest {
		t.Fatalf("non-member group create %d %s", rec.Code, rec.Body.String())
	}
	post, _ := json.Marshal(chatPostRequest{MeshID: circle.ID, ConversationID: "group:grp_missing", Type: "text", Text: "hi"})
	if rec := meshHTTP(t, home, http.MethodPost, "/api/chat/messages", "127.0.0.1:9", post); rec.Code != http.StatusNotFound {
		t.Fatalf("unknown group post %d %s", rec.Code, rec.Body.String())
	}
}

func sealedGroupMessage(t *testing.T, senderHome string, circle Circle, conversationID, text string) []byte {
	t.Helper()
	env, _, err := postLocalChat(senderHome, chatPostRequest{MeshID: circle.ID, ConversationID: conversationID, Type: "text", Text: text})
	if err != nil {
		t.Fatal(err)
	}
	wire, err := sealChatEnvelope(circle.GroupKey, env)
	if err != nil {
		t.Fatal(err)
	}
	return wire
}

func TestCustomGroupReachesMembersWithNameAndReplies(t *testing.T) {
	creatorHome := meshHome(t)
	creator, _ := testDevice(t)
	member, _ := testDevice(t)
	outsider, _ := testDevice(t)
	writeIdentityFile(t, creatorHome, creator)
	circle := saveTestCircle(t, creatorHome, testCircle(creator, member, outsider))
	group, err := createChatGroup(creatorHome, chatGroupCreateRequest{MeshID: circle.ID, Name: "Squad", MemberDeviceIDs: []string{member.DeviceID}})
	if err != nil {
		t.Fatal(err)
	}
	wire := sealedGroupMessage(t, creatorHome, circle, group.ConversationID, "hello squad")

	memberHome := meshHome(t)
	writeIdentityFile(t, memberHome, member)
	saveTestCircle(t, memberHome, circle)
	inserted, err := acceptChatWire(memberHome, wire)
	if err != nil || !inserted {
		t.Fatalf("member accept %v %v", inserted, err)
	}
	rec, err := loadChatGroupByConversation(memberHome, circle.ID, group.ConversationID)
	if err != nil || rec.Name != "Squad" || len(rec.MemberDeviceIDs) != 2 || rec.Kind != "custom" {
		t.Fatalf("member group record %+v %v", rec, err)
	}
	if _, _, err := postLocalChat(memberHome, chatPostRequest{MeshID: circle.ID, ConversationID: group.ConversationID, Type: "text", Text: "reply"}); err != nil {
		t.Fatalf("member reply: %v", err)
	}

	outsiderHome := meshHome(t)
	writeIdentityFile(t, outsiderHome, outsider)
	saveTestCircle(t, outsiderHome, circle)
	inserted, err = acceptChatWire(outsiderHome, wire)
	if err != nil || inserted {
		t.Fatalf("outsider stored a private group message %v %v", inserted, err)
	}
	convs, err := listChatConversations(outsiderHome, circle.ID)
	if err != nil || len(convs) != 0 {
		t.Fatalf("outsider conversations %+v %v", convs, err)
	}
}

func TestDirectMessageIgnoredByThirdParty(t *testing.T) {
	senderHome := meshHome(t)
	sender, _ := testDevice(t)
	recipient, _ := testDevice(t)
	bystander, _ := testDevice(t)
	writeIdentityFile(t, senderHome, sender)
	circle := saveTestCircle(t, senderHome, testCircle(sender, recipient, bystander))
	env, _, err := postLocalChat(senderHome, chatPostRequest{MeshID: circle.ID, Recipient: recipient.DeviceID, Type: "text", Text: "private"})
	if err != nil {
		t.Fatal(err)
	}
	wire, err := sealChatEnvelope(circle.GroupKey, env)
	if err != nil {
		t.Fatal(err)
	}

	bystanderHome := meshHome(t)
	writeIdentityFile(t, bystanderHome, bystander)
	saveTestCircle(t, bystanderHome, circle)
	inserted, err := acceptChatWire(bystanderHome, wire)
	if err != nil || inserted {
		t.Fatalf("bystander stored a direct message %v %v", inserted, err)
	}

	recipientHome := meshHome(t)
	writeIdentityFile(t, recipientHome, recipient)
	saveTestCircle(t, recipientHome, circle)
	inserted, err = acceptChatWire(recipientHome, wire)
	if err != nil || !inserted {
		t.Fatalf("recipient accept %v %v", inserted, err)
	}
}

func TestChatHistoryKeepsArrivalOrderWithinSameSecond(t *testing.T) {
	home := meshHome(t)
	self, _ := testDevice(t)
	writeIdentityFile(t, home, self)
	circle := saveTestCircle(t, home, testCircle(self))
	conv := circleGroupConversationID()
	ts := nowRFC3339()
	for _, id := range []string{"msg_zzzz_first", "msg_aaaa_second"} {
		env := chatEnvelope{Version: chatEnvelopeVersion, MessageID: id, MeshID: circle.ID, ConversationID: conv,
			SenderDeviceID: self.DeviceID, Type: "text", Timestamp: ts, Payload: json.RawMessage(`{"text":"x"}`)}
		if _, err := insertChatMessage(home, env, 0); err != nil {
			t.Fatal(err)
		}
	}
	msgs, err := listChatMessages(home, circle.ID, conv, false)
	if err != nil || len(msgs) != 2 || msgs[0].MessageID != "msg_zzzz_first" {
		t.Fatalf("order %+v %v", msgs, err)
	}
}

func TestConversationPreviewFallsBackToFileName(t *testing.T) {
	if got := payloadPreview(json.RawMessage(`{"fileName":"voice-1.webm","data":"AAAA"}`)); got != "voice-1.webm" {
		t.Fatalf("preview %q", got)
	}
}
