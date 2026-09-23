package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func startShareListenerForTest(t *testing.T, home string) (*shareListener, string) {
	t.Helper()
	setupTestEnv(t, home)
	t.Setenv("OVERDRIVE_HOME", home)
	port := freeTCPPort(t)
	endpoint := fmt.Sprintf("127.0.0.1:%d", port)
	t.Setenv("OVERDRIVE_SHARE_LISTEN", endpoint)
	id, priv, err := loadOrCreateIdentity(home)
	if err != nil {
		t.Fatal(err)
	}
	sl, err := startShareListener(home, id, priv)
	if err != nil {
		t.Fatal(err)
	}
	return sl, endpoint
}

func setupCirclePair(t *testing.T, repo string) (homeA, homeB string, circleID string, stop func()) {
	t.Helper()
	homeA = filepath.Join(t.TempDir(), "home-a")
	homeB = filepath.Join(t.TempDir(), "home-b")

	setupTestEnv(t, homeA)
	t.Setenv("OVERDRIVE_HOME", homeA)
	stdout, _, code := runCLICapture(t, []string{"share", "circle", "create", "--name", "team"})
	if code != 0 {
		t.Fatalf("create circle: %d", code)
	}
	circle := mustJSON(t, stdout)
	circleID = circle["id"].(string)

	stdout, _, code = runCLICapture(t, []string{"share", "circle", "invite", "--circle", circleID})
	if code != 0 {
		t.Fatal(code)
	}
	invite := mustJSON(t, stdout)
	inviteDir := filepath.Join(homeB, "share", "invites")
	if err := os.MkdirAll(inviteDir, 0o700); err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(invite)
	if err := os.WriteFile(filepath.Join(inviteDir, invite["code"].(string)+".json"), raw, 0o600); err != nil {
		t.Fatal(err)
	}

	_, _, code = runCLICapture(t, []string{"share", "circle", "folder-add", "--circle", circleID, "--folder", repo})
	if code != 0 {
		t.Fatal(code)
	}

	sl, endpoint := startShareListenerForTest(t, homeA)

	setupTestEnv(t, homeB)
	t.Setenv("OVERDRIVE_HOME", homeB)
	_, _, code = runCLICapture(t, []string{
		"share", "circle", "accept",
		"--code", invite["code"].(string),
		"--fingerprint", invite["fingerprint"].(string),
		"--peer", endpoint,
	})
	if code != 0 {
		t.Fatal(code)
	}
	_, _, code = runCLICapture(t, []string{"share", "circle", "folder-add", "--circle", circleID, "--folder", repo})
	if code != 0 {
		t.Fatal(code)
	}

	stop = func() { sl.Close() }
	return homeA, homeB, circleID, stop
}

func TestShareIdentityCircleAndStatus(t *testing.T) {
	home := t.TempDir()
	setupTestEnv(t, home)
	repo := makeTestRepo(t, filepath.Join(t.TempDir(), "repo"), "https://github.com/acme/a.git")

	stdout, _, code := runCLICapture(t, []string{"share", "identity"})
	if code != 0 {
		t.Fatal(code)
	}
	identity := mustJSON(t, stdout)
	if !strings.HasPrefix(identity["device_id"].(string), "dev_") {
		t.Fatal("device id")
	}

	stdout, _, code = runCLICapture(t, []string{"share", "circle", "create", "--name", "alpha"})
	if code != 0 {
		t.Fatal(code)
	}
	circle := mustJSON(t, stdout)
	stdout, _, code = runCLICapture(t, []string{"share", "circle", "list"})
	if code != 0 {
		t.Fatal(code)
	}
	listed := mustJSON(t, stdout)
	circles, _ := listed["circles"].([]any)
	if len(circles) == 0 {
		t.Fatal("no circles")
	}

	stdout, _, code = runCLICapture(t, []string{"share", "status", "--cwd", repo})
	if code != 0 {
		t.Fatal(code)
	}
	status := mustJSON(t, stdout)
	if status["mode"] != "off" {
		t.Fatalf("mode %v", status["mode"])
	}
	stdout, _, code = runCLICapture(t, []string{"share", "status", "--cwd", repo, "--format", "text"})
	if code != 0 || !strings.HasPrefix(strings.TrimSpace(stdout), "share: off") {
		t.Fatalf("text status %q", stdout)
	}

	_, _, code = runCLICapture(t, []string{"share", "circle", "folder-add", "--circle", circle["id"].(string), "--folder", repo})
	if code != 0 {
		t.Fatal(code)
	}
	stdout, _, code = runCLICapture(t, []string{"share", "circle", "folder-list", "--circle", circle["id"].(string)})
	if code != 0 {
		t.Fatal(code)
	}
	folders := mustJSON(t, stdout)
	allowed, _ := folders["allowed_folders"].([]any)
	if len(allowed) == 0 {
		t.Fatal("allowed folders empty")
	}
}

func TestShareRevokeMember(t *testing.T) {
	home := t.TempDir()
	setupTestEnv(t, home)
	_ = makeTestRepo(t, filepath.Join(t.TempDir(), "repo"), "https://github.com/acme/a.git")
	homeB := filepath.Join(t.TempDir(), "home-b")

	stdout, _, code := runCLICapture(t, []string{"share", "circle", "create", "--name", "alpha"})
	if code != 0 {
		t.Fatal(code)
	}
	circle := mustJSON(t, stdout)
	stdout, _, code = runCLICapture(t, []string{"share", "circle", "invite", "--circle", circle["id"].(string)})
	if code != 0 {
		t.Fatal(code)
	}
	invite := mustJSON(t, stdout)
	inviteDir := filepath.Join(homeB, "share", "invites")
	if err := os.MkdirAll(inviteDir, 0o700); err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(invite)
	if err := os.WriteFile(filepath.Join(inviteDir, invite["code"].(string)+".json"), raw, 0o600); err != nil {
		t.Fatal(err)
	}

	sl, endpoint := startShareListenerForTest(t, home)

	setupTestEnv(t, homeB)
	t.Setenv("OVERDRIVE_HOME", homeB)
	_, _, code = runCLICapture(t, []string{
		"share", "circle", "accept",
		"--code", invite["code"].(string),
		"--fingerprint", invite["fingerprint"].(string),
		"--peer", endpoint,
	})
	if code != 0 {
		t.Fatal(code)
	}
	stdout, _, code = runCLICapture(t, []string{"share", "identity"})
	if code != 0 {
		t.Fatal(code)
	}
	deviceB := mustJSON(t, stdout)["device_id"].(string)
	sl.Close()

	setupTestEnv(t, home)
	t.Setenv("OVERDRIVE_HOME", home)
	stdout, _, code = runCLICapture(t, []string{
		"share", "circle", "revoke",
		"--circle", circle["id"].(string),
		"--device", deviceB,
	})
	if code != 0 {
		t.Fatal(code)
	}
	revoked := mustJSON(t, stdout)
	members, _ := revoked["members"].([]any)
	found := false
	for _, m := range members {
		row, _ := m.(map[string]any)
		if row["device_id"] == deviceB && row["revoked"] == true {
			found = true
		}
	}
	if !found {
		t.Fatal("member not revoked")
	}
}

func TestPeerLessonRecallWithSignature(t *testing.T) {
	repo := makeTestRepo(t, filepath.Join(t.TempDir(), "repo"), "https://github.com/acme/payments-api.git")
	homeA, homeB, _, stop := setupCirclePair(t, repo)
	defer stop()

	setupTestEnv(t, homeA)
	t.Setenv("OVERDRIVE_HOME", homeA)
	_, _, code := runCLICapture(t, []string{
		"record", "--cwd", repo,
		"--kind", "lesson",
		"--subject", "ProposalRepository ECONNREFUSED",
		"--content", "Use ProposalRepository instead of Prisma in proposal transitions.",
		"--confidence", "0.95",
		"--source", "verified_execution",
		"--evidence", "src/proposals/proposal.service.ts integration tests failed with ECONNREFUSED",
	})
	if code != 0 {
		t.Fatal(code)
	}

	setupTestEnv(t, homeB)
	t.Setenv("OVERDRIVE_HOME", homeB)
	t.Setenv("OVERDRIVE_SHARE_LISTEN", "")
	_, _, code = runCLICapture(t, []string{"session-start", "--cwd", repo, "--quiet"})
	if code != 0 {
		t.Fatal(code)
	}
	time.Sleep(200 * time.Millisecond)

	stdout, _, code := runCLICapture(t, []string{
		"recall", "--cwd", repo,
		"--query", "ECONNREFUSED proposal.service.ts ProposalRepository",
	})
	if code != 0 {
		t.Fatal(code)
	}
	recalled := mustJSON(t, stdout)
	peer, _ := recalled["peer_memories"].([]any)
	if len(peer) == 0 {
		t.Fatal("expected peer memories")
	}
}

func TestShareListenCommandReturnsWithHook(t *testing.T) {
	home := t.TempDir()
	setupTestEnv(t, home)
	repo := makeTestRepo(t, filepath.Join(t.TempDir(), "repo"), "https://github.com/acme/a.git")
	port := freeTCPPort(t)
	t.Setenv("OVERDRIVE_SHARE_LISTEN", fmt.Sprintf("127.0.0.1:%d", port))
	oldWait := shareListenWait
	shareListenWait = func(sl *shareListener) {}
	defer func() { shareListenWait = oldWait }()
	_, _, code := runCLICapture(t, []string{"share", "listen", "--cwd", repo})
	if code != 0 {
		t.Fatal(code)
	}
}

func TestShareUnknownCommands(t *testing.T) {
	home := t.TempDir()
	setupTestEnv(t, home)
	_, stderr, code := runCLICapture(t, []string{"share", "nope"})
	if code != 2 || !strings.Contains(stderr, "unknown share command") {
		t.Fatalf("share unknown: %d %q", code, stderr)
	}
	_, stderr, code = runCLICapture(t, []string{"share", "circle", "nope"})
	if code != 2 || !strings.Contains(stderr, "unknown circle command") {
		t.Fatalf("circle unknown: %d %q", code, stderr)
	}
	_, stderr, code = runCLICapture(t, []string{"share", "listen"})
	if code != 2 || !strings.Contains(stderr, "OVERDRIVE_SHARE_LISTEN") {
		t.Fatalf("listen missing env: %d %q", code, stderr)
	}
}
