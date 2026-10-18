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
	got, lines, err := readMCPMessage(bufio.NewReader(&enc))
	if err != nil || lines || !bytes.Equal(got, body) {
		t.Fatalf("frame roundtrip: %v %v %q", err, lines, got)
	}
}

// The MCP stdio transport sends one JSON-RPC message per line; that is what Claude Desktop, Cursor, VS Code and
// Zed speak. The server answers each on its own line, without headers.
func TestServeMCPAnswersClientsThatSendOneMessagePerLine(t *testing.T) {
	in := strings.Join([]string{
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{}}}`,
		`{"jsonrpc":"2.0","method":"notifications/initialized"}`,
		``,
		`{"jsonrpc":"2.0","id":2,"method":"tools/list"}`,
	}, "\n")
	var out bytes.Buffer
	if err := serveMCP(strings.NewReader(in), &out, t.TempDir()); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out.String(), "Content-Length") {
		t.Fatalf("a line client got headers: %q", out.String())
	}
	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	if len(lines) != 2 {
		t.Fatalf("want two answers, got %q", out.String())
	}
	for i, line := range lines {
		var resp struct {
			ID     int            `json:"id"`
			Result map[string]any `json:"result"`
		}
		if err := json.Unmarshal([]byte(line), &resp); err != nil || resp.ID != i+1 || resp.Result == nil {
			t.Fatalf("answer %d: %q %v", i+1, line, err)
		}
	}
	if !strings.Contains(lines[1], `"recall"`) {
		t.Fatalf("tools/list: %s", lines[1])
	}
}

// Every tool must carry an inputSchema of type object: clients that validate the protocol drop a tools/list
// answer that lacks one.
func TestMCPToolsDescribeTheirArguments(t *testing.T) {
	for _, tool := range mcpTools() {
		schema, ok := tool["inputSchema"].(map[string]any)
		if !ok || schema["type"] != "object" {
			t.Fatalf("%v has no object inputSchema", tool["name"])
		}
		props, _ := schema["properties"].(map[string]any)
		for _, req := range asStrings(schema["required"]) {
			if _, ok := props[req]; !ok {
				t.Fatalf("%v requires %q but does not describe it", tool["name"], req)
			}
		}
	}
}

func asStrings(v any) []string {
	out, _ := v.([]string)
	return out
}
