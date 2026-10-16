package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
)

func TestHandleMCPRequestInitializeAndList(t *testing.T) {
	init := handleMCPRequest(".", mcpRequest{JSONRPC: "2.0", Method: "initialize", ID: 1})
	if init.Error != nil {
		t.Fatalf("initialize: %+v", init)
	}
	list := handleMCPRequest(".", mcpRequest{JSONRPC: "2.0", Method: "tools/list", ID: 2})
	if list.Error != nil {
		t.Fatalf("tools/list: %+v", list)
	}
	unknown := handleMCPRequest(".", mcpRequest{JSONRPC: "2.0", Method: "nope", ID: 3})
	if unknown.Error == nil {
		t.Fatal("expected unknown method error")
	}
}

func TestServeMCPToolCallRecord(t *testing.T) {
	home := t.TempDir()
	setupTestEnv(t, home)
	repo := makeTestRepo(t, filepath.Join(t.TempDir(), "repo"), "https://github.com/acme/mcp-stdio.git")
	call := map[string]any{
		"jsonrpc": "2.0",
		"id":      9,
		"method":  "tools/call",
		"params": map[string]any{
			"name": "record",
			"arguments": map[string]any{
				"kind":    "lesson",
				"subject": "MCP",
				"content": "vamos usar MCP stdio para recall",
			},
		},
	}
	raw, err := json.Marshal(call)
	if err != nil {
		t.Fatal(err)
	}
	var in bytes.Buffer
	w := bufio.NewWriter(&in)
	if err := writeMCPFrame(w, raw); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := serveMCP(&in, &out, repo); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "MCP") && !strings.Contains(out.String(), "content") {
		t.Fatalf("unexpected mcp response: %s", out.String())
	}
}

func TestReadWriteMCPFrame(t *testing.T) {
	body := []byte(`{"jsonrpc":"2.0","id":1,"method":"ping"}`)
	var enc bytes.Buffer
	if err := writeMCPFrame(bufio.NewWriter(&enc), body); err != nil {
		t.Fatal(err)
	}
	got, err := readMCPFrame(bufio.NewReader(&enc))
	if err != nil || !bytes.Equal(got, body) {
		t.Fatalf("frame roundtrip: %v %q", err, got)
	}
}
