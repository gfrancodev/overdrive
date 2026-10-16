package main

import (
	"encoding/base64"
	"strings"
	"testing"
)

func TestValidateChatPostTextAndCard(t *testing.T) {
	if err := validateChatPostText(&chatPostRequest{}); err == nil {
		t.Fatal("empty text")
	}
	long := strings.Repeat("x", chatTextMax+1)
	if err := validateChatPostText(&chatPostRequest{Text: long}); err == nil {
		t.Fatal("long text")
	}
	if err := validateChatPostCard(&chatPostRequest{}); err == nil {
		t.Fatal("empty card")
	}
	if err := validateChatPostCard(&chatPostRequest{Title: strings.Repeat("t", 201)}); err == nil {
		t.Fatal("long title")
	}
	if err := validateChatPostCard(&chatPostRequest{Summary: strings.Repeat("s", chatSummaryMax+1)}); err == nil {
		t.Fatal("long summary")
	}
	if err := validateChatPostCard(&chatPostRequest{Title: "ok"}); err != nil {
		t.Fatal(err)
	}
}

func TestValidateChatPostFile(t *testing.T) {
	if err := validateChatPostFile(&chatPostRequest{}); err == nil {
		t.Fatal("missing file name")
	}
	raw := base64.StdEncoding.EncodeToString([]byte("hi"))
	if err := validateChatPostFile(&chatPostRequest{FileName: "a.txt", Data: raw}); err != nil {
		t.Fatal(err)
	}
	if err := validateChatPostFile(&chatPostRequest{FileName: "a.txt", Data: "!!!"}); err == nil {
		t.Fatal("invalid base64")
	}
	big := base64.StdEncoding.EncodeToString(make([]byte, chatFileMaxBytes+1))
	if err := validateChatPostFile(&chatPostRequest{FileName: "big.bin", Data: big}); err == nil {
		t.Fatal("file too large")
	}
}

func TestValidateChatPostTopicAndReferences(t *testing.T) {
	if err := validateChatPostTopicFields(chatPostRequest{}); err == nil {
		t.Fatal("recipient required")
	}
	if err := validateChatPostTopicFields(chatPostRequest{Recipient: "bad/topic"}); err == nil {
		t.Fatal("invalid recipient")
	}
	if err := validateChatPostReferences([]string{"ok"}); err != nil {
		t.Fatal(err)
	}
	refs := make([]string, 17)
	for i := range refs {
		refs[i] = "r"
	}
	if err := validateChatPostReferences(refs); err == nil {
		t.Fatal("too many refs")
	}
	if err := validateChatPostReferences([]string{"has\nnewline"}); err == nil {
		t.Fatal("bad ref")
	}
}

func TestPeerFromTXT(t *testing.T) {
	peer, ok := peerFromTXT([]string{"id=dev_a", "dn=Ada", "mqtt=1883"}, "10.0.0.2", 7741)
	if !ok || peer.DeviceID != "dev_a" || peer.MQTTPort != 1883 || peer.Host != "10.0.0.2" {
		t.Fatalf("peer %+v ok=%v", peer, ok)
	}
	if _, ok := peerFromTXT([]string{"id=bad/id"}, "10.0.0.2", 0); ok {
		t.Fatal("invalid id")
	}
}
