package main

import (
	"encoding/json"
	"fmt"
	"strings"
)

func runMCPTool(e *Engine, project Project, home string, name string, args map[string]any) (string, error) {
	switch name {
	case "recall":
		return mcpToolRecall(e, project, args)
	case "record":
		return mcpToolRecord(e, project, args)
	case "handoff":
		return mcpToolHandoff(e, project, args)
	case "terminal_context_create":
		return mcpToolTerminalContextCreate(e, project, home, args)
	case "terminal_context_validate_workspace":
		return mcpToolTerminalValidateWorkspace(project, args)
	case "terminal_workspace_ensure":
		return mcpToolTerminalWorkspaceEnsure(home, args)
	default:
		return "", fmt.Errorf("unknown tool %q", name)
	}
}

func mcpToolRecall(e *Engine, project Project, args map[string]any) (string, error) {
	query, _ := args["query"].(string)
	resp, err := e.recall(project, query, 6, "")
	if err != nil {
		return "", err
	}
	raw, err := json.Marshal(resp)
	return string(raw), err
}

func mcpToolRecord(e *Engine, project Project, args map[string]any) (string, error) {
	kind, _ := args["kind"].(string)
	if kind == "" {
		kind = "decision"
	}
	content, _ := args["content"].(string)
	subject, _ := args["subject"].(string)
	if strings.TrimSpace(content) == "" {
		return "", fmt.Errorf("content is required")
	}
	if !validKind(kind) {
		return "", fmt.Errorf("invalid kind %q", kind)
	}
	m, err := e.recordMemory(memoryRecord{
		Project: project, Kind: kind, Scope: "repository", Subject: subject, Content: content,
		Confidence: 0.8, Priority: 50, Source: "agent_observation", SourceRef: "", Evidence: "",
	})
	if err != nil {
		return "", err
	}
	raw, err := json.Marshal(m)
	return string(raw), err
}

func mcpToolHandoff(e *Engine, project Project, args map[string]any) (string, error) {
	if claim, _ := args["claim"].(bool); claim {
		_, _ = e.claimOpenHandoff(project, localDeviceID(e.home))
	}
	return e.renderPin(project), nil
}

func mcpToolTerminalContextCreate(e *Engine, project Project, home string, args map[string]any) (string, error) {
	sessionID, _ := args["session_id"].(string)
	if strings.TrimSpace(sessionID) == "" {
		return "", fmt.Errorf("session_id is required")
	}
	mgr, err := getTerminalManager(home)
	if err != nil {
		return "", err
	}
	sess, err := mgr.GetSession(sessionID)
	if err != nil {
		return "", err
	}
	objective, _ := args["objective"].(string)
	summary, _ := args["conversation_summary"].(string)
	recallQ, _ := args["recall_query"].(string)
	startSeq, _ := args["start_seq"].(float64)
	endSeq, _ := args["end_seq"].(float64)
	budget, _ := args["token_budget"].(float64)
	bundle, err := buildContextBundle(home, mgr.store, sess, localDeviceID(home), contextBuildRequest{
		Objective: objective, ConversationSummary: summary, RecallQuery: recallQ,
		StartSeq: int64(startSeq), EndSeq: int64(endSeq), TokenBudget: int(budget),
	})
	if err != nil {
		return "", err
	}
	raw, err := json.Marshal(bundle)
	return string(raw), err
}

func mcpToolTerminalValidateWorkspace(project Project, args map[string]any) (string, error) {
	root := project.Root
	if v, ok := args["root"].(string); ok && strings.TrimSpace(v) != "" {
		root = v
	}
	var expected *GitWorkspaceSnapshot
	if raw, ok := args["expected_workspace"]; ok && raw != nil {
		b, _ := json.Marshal(raw)
		var snap GitWorkspaceSnapshot
		if err := json.Unmarshal(b, &snap); err != nil {
			return "", err
		}
		expected = &snap
	}
	result := validateWorkspaceState(root, expected)
	out, err := json.Marshal(result)
	return string(out), err
}

func mcpToolTerminalWorkspaceEnsure(home string, args map[string]any) (string, error) {
	circleID, _ := args["circle_id"].(string)
	workspaceID, _ := args["workspace_id"].(string)
	if strings.TrimSpace(circleID) == "" || strings.TrimSpace(workspaceID) == "" {
		return "", fmt.Errorf("circle_id and workspace_id are required")
	}
	root, err := EnsureSharedWorkspace(home, circleID, workspaceID, localDeviceID(home))
	if err != nil {
		return "", err
	}
	manifest, err := readSharedWorkspaceManifest(root)
	if err != nil {
		return "", err
	}
	out, err := json.Marshal(map[string]any{"root": root, "manifest": manifest})
	return string(out), err
}
