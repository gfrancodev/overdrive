package main

import (
	"bufio"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
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
		body, err := readMCPFrame(reader)
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
		if err := writeMCPFrame(writer, raw); err != nil {
			return err
		}
	}
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

func mcpTools() []map[string]any {
	return []map[string]any{
		{"name": "recall", "description": "Recall standing decisions and unconfirmed transcript hints"},
		{"name": "record", "description": "Record a compact memory packet"},
		{"name": "handoff", "description": "Show or claim the open handoff"},
		{"name": "terminal_context_create", "description": "Build a ContextBundle for a live terminal session (TurboVec recall + untrusted session excerpt)"},
		{"name": "terminal_context_validate_workspace", "description": "Validate git branch/commit/dirty state before harness handoff"},
		{"name": "terminal_workspace_ensure", "description": "Ensure shared circle workspace layout under ~/.overdrive/circles/{circleId}/workspaces/{workspaceId}"},
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

func readMCPFrame(r *bufio.Reader) ([]byte, error) {
	length := 0
	for {
		line, err := r.ReadString('\n')
		if err != nil {
			return nil, err
		}
		line = strings.TrimSpace(line)
		if line == "" {
			break
		}
		if strings.HasPrefix(strings.ToLower(line), "content-length:") {
			_, _ = fmt.Sscanf(line, "Content-Length: %d", &length)
			if length == 0 {
				_, _ = fmt.Sscanf(line, "content-length: %d", &length)
			}
		}
	}
	if length <= 0 {
		return nil, io.EOF
	}
	buf := make([]byte, length)
	_, err := io.ReadFull(r, buf)
	return buf, err
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
