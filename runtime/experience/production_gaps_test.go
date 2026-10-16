package main

import (
	"crypto/ed25519"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCLIHandoffClaimAndBeforePrompt(t *testing.T) {
	home := t.TempDir()
	setupTestEnv(t, home)
	repo := makeTestRepo(t, filepath.Join(t.TempDir(), "repo"), "https://github.com/acme/handoff-cli.git")
	e, err := newEngine(home)
	if err != nil {
		t.Fatal(err)
	}
	defer e.Close()
	project, err := identifyProject(repo)
	if err != nil {
		t.Fatal(err)
	}
	id, _, err := loadOrCreateIdentity(home)
	if err != nil {
		t.Fatal(err)
	}
	if err := e.saveHandoff(project, "finish mesh signaling tests", id.DeviceID); err != nil {
		t.Fatal(err)
	}
	other := "dev_other_peer"
	if _, err := e.claimOpenHandoff(project, other); err != nil {
		t.Fatal(err)
	}
	claimed, err := e.claimOpenHandoff(project, id.DeviceID)
	if err != nil || claimed {
		t.Fatalf("handoff already claimed by peer: %v %v", claimed, err)
	}
	out, _, code := runCLICapture(t, []string{"handoff-claim", "--cwd", repo})
	if code != 0 {
		t.Fatalf("handoff-claim failed: %d %s", code, out)
	}
	var body map[string]any
	if err := json.Unmarshal([]byte(out), &body); err != nil {
		t.Fatal(err)
	}
	if body["claimed"] != false {
		t.Fatalf("expected no second claim: %+v", body)
	}
	if err := e.saveHandoff(project, "hook handoff summary", id.DeviceID); err != nil {
		t.Fatal(err)
	}
	payload := filepath.Join(t.TempDir(), "hook.json")
	if err := os.WriteFile(payload, []byte(`{"prompt":"mesh webrtc"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	hookOut, _, hookCode := runCLICapture(t, []string{"before-prompt", "--cwd", repo, "--payload-file", payload})
	if hookCode != 0 {
		t.Fatalf("before-prompt failed: %d", hookCode)
	}
	if !strings.Contains(hookOut, "handoff") && hookOut == "" {
		t.Fatalf("expected pin output, got %q", hookOut)
	}
}

func TestCLIIndexTailWritesSpan(t *testing.T) {
	home := t.TempDir()
	setupTestEnv(t, home)
	repo := makeTestRepo(t, filepath.Join(t.TempDir(), "repo"), "https://github.com/acme/index-tail.git")
	jsonl := filepath.Join(t.TempDir(), "events.jsonl")
	line := `{"role":"user","content":"vamos usar ProposalRepository no mesh"}`
	if err := os.WriteFile(jsonl, []byte(line+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	out, _, code := runCLICapture(t, []string{"index-tail", "--cwd", repo, "--file", jsonl, "--harness", "cursor"})
	if code != 0 {
		t.Fatalf("index-tail failed: %d %s", code, out)
	}
	body := mustJSON(t, out)
	if body["moved"] != true {
		t.Fatalf("expected moved transcript: %+v", body)
	}
}

func TestTerminalContextCreateHTTP(t *testing.T) {
	home := t.TempDir()
	setupTestEnv(t, home)
	closeTerminalDBs()
	repo := makeTestRepo(t, filepath.Join(t.TempDir(), "repo"), "https://github.com/acme/term-ctx.git")
	gitRun(t, repo, "add", "README.md")
	gitRun(t, repo, "commit", "-qm", "init")
	sid := insertHTTPTestSession(t, home)
	body := terminalTestJSON(map[string]any{
		"objective": "smoke context bundle",
		"start_seq": 0,
		"end_seq":   1,
	})
	rec := terminalHTTP(t, home, http.MethodPost, "/api/v1/terminal/sessions/"+sid+"/context/create", "127.0.0.1:7742", body, nil)
	if rec.Code != http.StatusCreated {
		t.Fatalf("context create %d %s", rec.Code, rec.Body.String())
	}
	payload := terminalJSONBody(t, rec)
	if payload["bundle"] == nil || payload["link"] == nil {
		t.Fatalf("expected bundle and link: %+v", payload)
	}
}

func TestTerminalContextShareRoundTrip(t *testing.T) {
	home := t.TempDir()
	setupTestEnv(t, home)
	closeTerminalDBs()
	id, priv, err := loadOrCreateIdentity(home)
	if err != nil {
		t.Fatal(err)
	}
	circle, err := createCircle(home, "ctx-roundtrip", id, priv)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := EnsureSharedWorkspace(home, circle.ID, "ws_main", id.DeviceID); err != nil {
		t.Fatal(err)
	}
	repo := makeTestRepo(t, filepath.Join(t.TempDir(), "repo"), "https://github.com/acme/ctx-round.git")
	gitRun(t, repo, "add", "README.md")
	gitRun(t, repo, "commit", "-qm", "init")
	createBody := terminalTestJSON(map[string]any{
		"name":              "ctx",
		"working_directory": repo,
		"circle_id":         circle.ID,
		"cols":              80,
		"rows":              24,
	})
	created := terminalHTTP(t, home, http.MethodPost, "/api/v1/terminal/sessions", "127.0.0.1:7742", createBody, nil)
	if created.Code != http.StatusCreated {
		t.Fatalf("create session %d %s", created.Code, created.Body.String())
	}
	sessPayload := terminalJSONBody(t, created)
	sess, _ := sessPayload["session"].(map[string]any)
	sid, _ := sess["id"].(string)
	bundleBody := terminalTestJSON(map[string]any{
		"objective": "share roundtrip",
		"start_seq": 0,
		"end_seq":   1,
	})
	bundleRec := terminalHTTP(t, home, http.MethodPost, "/api/v1/terminal/sessions/"+sid+"/context/create", "127.0.0.1:7742", bundleBody, nil)
	if bundleRec.Code != http.StatusCreated {
		t.Fatalf("context create %d %s", bundleRec.Code, bundleRec.Body.String())
	}
	createdBundle := terminalJSONBody(t, bundleRec)
	shareBody := terminalTestJSON(map[string]any{
		"workspace_id": "ws_main",
		"circle_id":    circle.ID,
		"bundle":       createdBundle["bundle"],
	})
	shareRec := terminalHTTP(t, home, http.MethodPost, "/api/v1/terminal/sessions/"+sid+"/context/share", "127.0.0.1:7742", shareBody, nil)
	if shareRec.Code != http.StatusOK {
		t.Fatalf("context share %d %s", shareRec.Code, shareRec.Body.String())
	}
}

func TestTerminalInviteRequiresCircle(t *testing.T) {
	home := t.TempDir()
	setupTestEnv(t, home)
	closeTerminalDBs()
	sid := insertHTTPTestSession(t, home)
	body := []byte(`{"target_device_id":"dev_peer","permission":"view"}`)
	rec := terminalHTTP(t, home, http.MethodPost, "/api/v1/terminal/sessions/"+sid+"/invite", "127.0.0.1:7742", body, nil)
	if rec.Code == http.StatusOK {
		t.Fatalf("expected invite failure without circle, got %s", rec.Body.String())
	}
}

func TestCmdContextFileWritesPin(t *testing.T) {
	home := t.TempDir()
	setupTestEnv(t, home)
	repo := makeTestRepo(t, filepath.Join(t.TempDir(), "repo"), "https://github.com/acme/context-file.git")
	outPath := filepath.Join(t.TempDir(), "pin.md")
	_, _, code := runCLICapture(t, []string{"context-file", "--cwd", repo, "--out", outPath})
	if code != 0 {
		t.Fatalf("context-file failed: %d", code)
	}
	raw, err := os.ReadFile(outPath)
	if err != nil || len(raw) == 0 {
		t.Fatalf("missing pin file: %v", err)
	}
}

func TestQueryFromHookPayloadExtractsPrompt(t *testing.T) {
	if got := queryFromHookPayload(`{"prompt":"mesh webrtc"}`); got != "mesh webrtc" {
		t.Fatalf("prompt: %q", got)
	}
	if got := queryFromHookPayload("plain text"); got != "plain text" {
		t.Fatalf("plain: %q", got)
	}
}

func TestCmdIndexTailMissingFileFlag(t *testing.T) {
	home := t.TempDir()
	setupTestEnv(t, home)
	repo := makeTestRepo(t, filepath.Join(t.TempDir(), "repo"), "https://github.com/acme/index-tail-flag.git")
	_, _, code := runCLICapture(t, []string{"index-tail", "--cwd", repo})
	if code == 0 {
		t.Fatal("expected missing --file error")
	}
}

func TestEnsureHandoffFromHintsOpensHandoff(t *testing.T) {
	home := t.TempDir()
	setupTestEnv(t, home)
	repo := makeTestRepo(t, filepath.Join(t.TempDir(), "repo"), "https://github.com/acme/handoff-hints.git")
	e, err := newEngine(home)
	if err != nil {
		t.Fatal(err)
	}
	defer e.Close()
	project, err := identifyProject(repo)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "session.jsonl")
	line := `{"role":"user","content":"vamos usar mesh signaling tests"}`
	if err := os.WriteFile(path, []byte(line+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := e.indexTranscriptFile(project, "cursor", path); err != nil {
		t.Fatal(err)
	}
	if err := e.ensureHandoffFromHints(project); err != nil {
		t.Fatal(err)
	}
	if !e.hasOpenHandoff(project) {
		t.Fatal("expected open handoff from transcript hints")
	}
}

func TestWriteSharedWorkspaceArtifactRoundTrip(t *testing.T) {
	home := t.TempDir()
	setupTestEnv(t, home)
	var priv ed25519.PrivateKey
	id, priv, err := loadOrCreateIdentity(home)
	if err != nil {
		t.Fatal(err)
	}
	circle, err := createCircle(home, "artifact", id, priv)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := EnsureSharedWorkspace(home, circle.ID, "ws_main", id.DeviceID); err != nil {
		t.Fatal(err)
	}
	entry, err := WriteSharedWorkspaceArtifact(home, circle.ID, "ws_main", id.DeviceID, "notes.json", []byte(`{"ok":true}`))
	if err != nil {
		t.Fatal(err)
	}
	if entry.Path == "" {
		t.Fatal("expected artifact path")
	}
}
