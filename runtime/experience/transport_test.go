package main

import (
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestShareTransportSyncRoundtrip(t *testing.T) {
	homeA := t.TempDir()
	homeB := t.TempDir()
	repo := makeTestRepo(t, filepath.Join(t.TempDir(), "repo"), "https://github.com/acme/payments-api.git")

	setupTestEnv(t, homeA)
	t.Setenv("OVERDRIVE_HOME", homeA)
	idA, privA, err := loadOrCreateIdentity(homeA)
	if err != nil {
		t.Fatal(err)
	}
	circle, err := createCircle(homeA, "team", idA, privA)
	if err != nil {
		t.Fatal(err)
	}
	circle, err = addAllowedFolder(homeA, circle.ID, repo, idA)
	if err != nil {
		t.Fatal(err)
	}

	sl, endpoint := startShareListenerForTest(t, homeA)
	defer sl.Close()

	setupTestEnv(t, homeB)
	t.Setenv("OVERDRIVE_HOME", homeB)
	idB, privB, err := loadOrCreateIdentity(homeB)
	if err != nil {
		t.Fatal(err)
	}
	invite, err := createInvite(homeA, circle.ID, idA)
	if err != nil {
		t.Fatal(err)
	}
	inviteDir := invitesDir(homeB)
	if err := os.MkdirAll(inviteDir, 0o700); err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(invite)
	if err := os.WriteFile(filepath.Join(inviteDir, invite.Code+".json"), raw, 0o600); err != nil {
		t.Fatal(err)
	}
	circleB, err := acceptInvite(homeB, invite.Code, invite.Fingerprint, endpoint, idB, privB)
	if err != nil {
		t.Fatal(err)
	}
	circleB, err = addAllowedFolder(homeB, circleB.ID, repo, idB)
	if err != nil {
		t.Fatal(err)
	}

	eA, err := newEngine(homeA)
	if err != nil {
		t.Fatal(err)
	}
	defer eA.Close()
	project, err := identifyProject(repo)
	if err != nil {
		t.Fatal(err)
	}
	_, err = eA.recordMemory(project, "lesson", "repository", "ECONNREFUSED proposal.service.ts",
		"Use ProposalRepository instead of Prisma.", 0.95, 50, "verified_execution", "",
		"proposal.service.ts integration tests failed with ECONNREFUSED")
	if err != nil {
		t.Fatal(err)
	}

	resp, err := fetchPeerSync(homeB, endpoint, circleB, idB, privB, project.Repository, "")
	if err != nil {
		t.Fatal(err)
	}
	eB, err := newEngine(homeB)
	if err != nil {
		t.Fatal(err)
	}
	defer eB.Close()
	imported, err := eB.importPeerBatch(circleB, resp)
	if err != nil || imported == 0 {
		t.Fatalf("imported=%d err=%v", imported, err)
	}
	recalled, err := eB.recall(project, "ECONNREFUSED proposal.service.ts ProposalRepository", 5, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(recalled.PeerMemories) == 0 {
		t.Fatal("expected peer memories after roundtrip")
	}
}

func TestShareListenerRejectsBadWire(t *testing.T) {
	home := t.TempDir()
	setupTestEnv(t, home)
	sl, endpoint := startShareListenerForTest(t, home)
	defer sl.Close()
	conn, err := net.Dial("tcp", endpoint)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	_, _ = conn.Write([]byte("not-json\n"))
	time.Sleep(50 * time.Millisecond)
}
