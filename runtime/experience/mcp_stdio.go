package main

import (
	"bufio"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"sort"
	"strconv"
	"strings"
)

type mcpRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      any             `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type mcpResponse struct {
	JSONRPC string `json:"jsonrpc"`
	ID      any    `json:"id,omitempty"`
	Result  any    `json:"result,omitempty"`
	Error   any    `json:"error,omitempty"`
}

func cmdMCP(args []string) error {
	fs := flag.NewFlagSet("mcp", flag.ContinueOnError)
	cwd := fs.String("cwd", ".", "working directory")
	if err := fs.Parse(args); err != nil {
		return err
	}
	return serveMCP(os.Stdin, os.Stdout, *cwd)
}

func serveMCP(in io.Reader, out io.Writer, cwd string) error {
	reader := bufio.NewReader(in)
	writer := bufio.NewWriter(out)
	for {
		body, lines, err := readMCPMessage(reader)
		if err != nil {
			if err == io.EOF {
				return nil
			}
			return err
		}
		var req mcpRequest
		if err := json.Unmarshal(body, &req); err != nil {
			continue
		}
		if strings.HasPrefix(req.Method, "notifications/") {
			continue
		}
		resp := handleMCPRequest(cwd, req)
		raw, err := json.Marshal(resp)
		if err != nil {
			return err
		}
		if lines {
			err = writeMCPLine(writer, raw)
		} else {
			err = writeMCPFrame(writer, raw)
		}
		if err != nil {
			return err
		}
	}
}

// readMCPMessage reads the next message in whichever framing the client uses. The MCP stdio transport sends one
// JSON message per line; clients written against earlier versions of this server sent Content-Length headers,
// as LSP does. lines reports which one, so the answer goes back the same way.
func readMCPMessage(r *bufio.Reader) (body []byte, lines bool, err error) {
	for {
		line, readErr := r.ReadString('\n')
		trimmed := strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(trimmed, "{"):
			return []byte(trimmed), true, nil
		case strings.HasPrefix(strings.ToLower(trimmed), "content-length:"):
			body, err := finishMCPFrame(r, mcpContentLength(trimmed))
			return body, false, err
		case readErr != nil:
			return nil, false, readErr
		}
	}
}

func mcpContentLength(header string) int {
	_, value, _ := strings.Cut(header, ":")
	n, _ := strconv.Atoi(strings.TrimSpace(value))
	return n
}

// finishMCPFrame reads the rest of a header block and the body it announced.
func finishMCPFrame(r *bufio.Reader, length int) ([]byte, error) {
	for {
		line, err := r.ReadString('\n')
		if err != nil {
			return nil, err
		}
		if strings.TrimSpace(line) == "" {
			break
		}
	}
	if length <= 0 {
		return nil, io.EOF
	}
	buf := make([]byte, length)
	_, err := io.ReadFull(r, buf)
	return buf, err
}

func writeMCPLine(w *bufio.Writer, body []byte) error {
	if _, err := w.Write(append(body, '\n')); err != nil {
		return err
	}
	return w.Flush()
}

func handleMCPRequest(cwd string, req mcpRequest) mcpResponse {
	resp := mcpResponse{JSONRPC: "2.0", ID: req.ID}
	switch req.Method {
	case "initialize":
		resp.Result = map[string]any{
			"protocolVersion": "2024-11-05",
			"capabilities":    map[string]any{"tools": map[string]any{}},
			"serverInfo":      map[string]any{"name": "overdrive-runtime", "version": version},
		}
	case "tools/list":
		resp.Result = map[string]any{"tools": mcpTools()}
	case "tools/call":
		result, err := callMCPTool(cwd, req.Params)
		if err != nil {
			resp.Error = map[string]any{"code": -32000, "message": err.Error()}
			break
		}
		resp.Result = result
	case "":
		resp.Error = map[string]any{"code": -32600, "message": "missing method"}
	default:
		resp.Error = map[string]any{"code": -32601, "message": "method not found"}
	}
	return resp
}

// mcpSchema is a tool's inputSchema: an object with the given properties, each a JSON Schema.
func mcpSchema(props map[string]any, required ...string) map[string]any {
	schema := map[string]any{"type": "object", "properties": props}
	if len(required) > 0 {
		schema["required"] = required
	}
	return schema
}

func memoryKinds() []string {
	kinds := make([]string, 0, len(validKinds))
	for k := range validKinds {
		kinds = append(kinds, k)
	}
	sort.Strings(kinds)
	return kinds
}

func mcpString(description string) map[string]any {
	return map[string]any{"type": "string", "description": description}
}

func mcpTools() []map[string]any {
	return []map[string]any{
		{"name": "recall", "description": "Recall standing decisions and unconfirmed transcript hints",
			"inputSchema": mcpSchema(map[string]any{"query": mcpString("What the current task is about")})},
		{"name": "record", "description": "Record a compact memory packet",
			"inputSchema": mcpSchema(map[string]any{
				"content": mcpString("The memory itself"),
				"subject": mcpString("A short title"),
				"kind":    map[string]any{"type": "string", "enum": memoryKinds(), "description": "decision when left out"},
			}, "content")},
		{"name": "handoff", "description": "Show or claim the open handoff",
			"inputSchema": mcpSchema(map[string]any{"claim": map[string]any{"type": "boolean", "description": "Claim the open handoff for this device"}})},
		{"name": "terminal_context_create", "description": "Build a ContextBundle for a live terminal session (TurboVec recall + untrusted session excerpt)",
			"inputSchema": mcpSchema(map[string]any{
				"session_id":           mcpString("The terminal session"),
				"objective":            mcpString("What the next agent should do"),
				"conversation_summary": mcpString("The conversation so far"),
				"recall_query":         mcpString("What to recall for the bundle"),
				"start_seq":            map[string]any{"type": "integer", "description": "First output sequence to include"},
				"end_seq":              map[string]any{"type": "integer", "description": "Last output sequence to include"},
				"token_budget":         map[string]any{"type": "integer", "description": "Size limit of the bundle"},
			}, "session_id")},
		{"name": "terminal_context_validate_workspace", "description": "Validate git branch/commit/dirty state before harness handoff",
			"inputSchema": mcpSchema(map[string]any{
				"root":               mcpString("Repository root; the project root when left out"),
				"expected_workspace": map[string]any{"type": "object", "description": "The workspace snapshot the handoff expects"},
			})},
		{"name": "terminal_workspace_ensure", "description": "Ensure shared circle workspace layout under ~/.overdrive/circles/{circleId}/workspaces/{workspaceId}",
			"inputSchema": mcpSchema(map[string]any{
				"circle_id":    mcpString("The circle"),
				"workspace_id": mcpString("The shared workspace"),
			}, "circle_id", "workspace_id")},
	}
}

func callMCPTool(cwd string, params json.RawMessage) (any, error) {
	var call struct {
		Name      string         `json:"name"`
		Arguments map[string]any `json:"arguments"`
	}
	if len(params) > 0 {
		if err := json.Unmarshal(params, &call); err != nil {
			return nil, err
		}
	}
	if call.Arguments == nil {
		call.Arguments = map[string]any{}
	}
	project, err := identifyProject(cwd)
	if err != nil {
		return nil, err
	}
	home, _ := overdriveHome()
	var text string
	err = withEngine(func(e *Engine) error {
		out, err := runMCPTool(e, project, home, call.Name, call.Arguments)
		text = out
		return err
	})
	if err != nil {
		return nil, err
	}
	return map[string]any{
		"content": []map[string]any{{"type": "text", "text": text}},
	}, nil
}

func writeMCPFrame(w *bufio.Writer, body []byte) error {
	if _, err := fmt.Fprintf(w, "Content-Length: %d\r\n\r\n", len(body)); err != nil {
		return err
	}
	if _, err := w.Write(body); err != nil {
		return err
	}
	return w.Flush()
}
