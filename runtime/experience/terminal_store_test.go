package main

import (
	"strings"
	"testing"
)

func TestSourceDeviceForMemoryAndContextLink(t *testing.T) {
	if got := sourceDeviceForMemory(Memory{Origin: "peer", PeerDeviceID: "peer-1"}, "local"); got != "peer-1" {
		t.Fatal(got)
	}
	if got := sourceDeviceForMemory(Memory{Origin: "local", PeerDeviceID: "peer-1"}, "local"); got != "local" {
		t.Fatal(got)
	}
	home := t.TempDir()
	closeTerminalDBs()
	store, err := newTerminalStore(home)
	if err != nil {
		t.Fatal(err)
	}
	now := terminalNow()
	if err := store.InsertSession(TerminalSession{
		ID: "ts_link", Name: "Link", State: TerminalStateCreated, Mode: TerminalModeManaged,
		OwnerDeviceID: "dev1", HostDeviceID: "dev1", WorkingDirectory: home, Command: "/bin/sh",
		IPCToken: "tok", CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.InsertContextLink(TerminalContextLink{SessionID: "ts_link", LinkKind: "context_bundle", RefID: "b1"}); err != nil {
		t.Fatal(err)
	}
}

func TestCirclePruneDryRun(t *testing.T) {
	home := t.TempDir()
	setupTestEnv(t, home)
	stdout, stderr, code := runCLICapture(t, []string{"share", "circle", "prune", "--dry-run"})
	if code != 0 {
		t.Fatalf("prune: %d %s %s", code, stdout, stderr)
	}
	if !strings.Contains(stdout, "{") {
		t.Fatalf("expected json, got %s", stdout)
	}
}

func TestTerminalGetSessionMissingReturnsNotFound(t *testing.T) {
	home := t.TempDir()
	closeTerminalDBs()
	store, err := newTerminalStore(home)
	if err != nil {
		t.Fatal(err)
	}
	_, err = store.GetSession("ts_does_not_exist")
	if err == nil || !strings.Contains(err.Error(), string(TerminalErrSessionNotFound)) {
		t.Fatalf("expected session not found, got %v", err)
	}
}

func TestTerminalSchemaAndSequence(t *testing.T) {
	home := t.TempDir()
	closeTerminalDBs()
	store, err := newTerminalStore(home)
	if err != nil {
		t.Fatal(err)
	}
	now := terminalNow()
	sess := TerminalSession{
		ID:               "ts_test1",
		Name:             "Test",
		State:            TerminalStateCreated,
		Mode:             TerminalModeManaged,
		OwnerDeviceID:    "dev1",
		HostDeviceID:     "dev1",
		WorkingDirectory: home,
		Command:          "/bin/sh",
		IPCToken:         "tok",
		CreatedAt:        now,
		UpdatedAt:        now,
	}
	if err := store.InsertSession(sess); err != nil {
		t.Fatal(err)
	}
	seq1, err := store.NextSequence(sess.ID)
	if err != nil || seq1 != 1 {
		t.Fatalf("seq1=%d err=%v", seq1, err)
	}
	screen := newANSIScreen(80, 24)
	if _, err := store.RecordOutput(sess.ID, "dev1", []byte("hello"), screen); err != nil {
		t.Fatal(err)
	}
	got, err := store.GetSession(sess.ID)
	if err != nil || got.LastSequence != 2 {
		t.Fatalf("last_sequence=%d err=%v", got.LastSequence, err)
	}
	events, err := store.ReplayEvents(sess.ID, 1, 0, 10)
	if err != nil || len(events) != 1 || events[0].Kind != TerminalEventOutput {
		t.Fatalf("replay=%+v err=%v", events, err)
	}
}

func TestTerminalPrivateModeSkipsOutputPersistence(t *testing.T) {
	home := t.TempDir()
	closeTerminalDBs()
	store, _ := newTerminalStore(home)
	now := terminalNow()
	id := "ts_private"
	_ = store.InsertSession(TerminalSession{
		ID: id, Name: "P", State: TerminalStateRunning, Mode: TerminalModeManaged,
		OwnerDeviceID: "d", HostDeviceID: "d", WorkingDirectory: home, Command: "/bin/sh",
		CreatedAt: now, UpdatedAt: now, IPCToken: "x",
	})
	if err := store.SetPrivateMode(id, true, "d"); err != nil {
		t.Fatal(err)
	}
	screen := newANSIScreen(80, 24)
	_, _ = store.RecordOutput(id, "d", []byte("secret"), screen)
	events, _ := store.ReplayEvents(id, 1, 0, 50)
	for _, ev := range events {
		if ev.Kind == TerminalEventOutput {
			t.Fatal("output persisted during private mode")
		}
	}
	if !stringsContains(screen.RenderPlain(), "secret") {
		t.Fatal("local emulator should still see output")
	}
}

func TestANSIScreenClearHomeAndEraseDisplay(t *testing.T) {
	s := newANSIScreen(20, 6)
	s.Write([]byte("hello\nworld\nkeep"))
	s.Write([]byte("\x1b[H\x1b[2J"))
	if strings.TrimSpace(s.RenderPlain()) != "" {
		t.Fatalf("clear should empty the screen, got %q", s.RenderPlain())
	}
	s.Write([]byte("ok"))
	if !stringsContains(s.RenderPlain(), "ok") {
		t.Fatalf("expected prompt after clear: %q", s.RenderPlain())
	}
}

func TestANSIScreenSGRDoesNotLeakIntoPlainText(t *testing.T) {
	s := newANSIScreen(40, 3)
	s.Write([]byte("\x1b[01;32mgreen\x1b[0m plain"))
	out := s.RenderPlain()
	if stringsContains(out, "\x1b") || stringsContains(out, "[01") {
		t.Fatalf("SGR leaked into plain text: %q", out)
	}
	if !stringsContains(out, "green") || !stringsContains(out, "plain") {
		t.Fatalf("missing text: %q", out)
	}
}

func TestANSIScreenSnapshotRoundTrip(t *testing.T) {
	s := newANSIScreen(10, 3)
	s.Write([]byte("ab\x1b[2;3Hcd"))
	snap := s.Snapshot()
	s2 := newANSIScreen(10, 3)
	s2.Restore(snap)
	if s2.RenderPlain() != s.RenderPlain() {
		t.Fatalf("roundtrip mismatch:\n%q\n%q", s2.RenderPlain(), s.RenderPlain())
	}
}

func TestReplayApplyReadOnly(t *testing.T) {
	screen := newANSIScreen(40, 5)
	events := []SessionEvent{
		{Kind: TerminalEventOutput, Raw: []byte("line1\n")},
		{Kind: TerminalEventInput, Raw: []byte("must-not-apply")},
		{Kind: TerminalEventOutput, Raw: []byte("line2\n")},
	}
	ReplayApply(screen, events)
	out := screen.RenderPlain()
	if !stringsContains(out, "line1") || !stringsContains(out, "line2") {
		t.Fatalf("replay output missing: %q", out)
	}
}

func TestGrantLeaseSequenceDoesNotCollideWithOutput(t *testing.T) {
	home := t.TempDir()
	closeTerminalDBs()
	store, err := newTerminalStore(home)
	if err != nil {
		t.Fatal(err)
	}
	now := terminalNow()
	id := "ts_lease_seq"
	if err := store.InsertSession(TerminalSession{
		ID: id, Name: "L", State: TerminalStateRunning, Mode: TerminalModeManaged,
		OwnerDeviceID: "dev1", HostDeviceID: "dev1", WorkingDirectory: home, Command: "/bin/sh",
		CreatedAt: now, UpdatedAt: now, IPCToken: "x",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.grantLeaseCAS(id, "dev1", 0, now); err != nil {
		t.Fatal(err)
	}
	screen := newANSIScreen(80, 24)
	if _, err := store.RecordOutput(id, "dev1", []byte("prompt"), screen); err != nil {
		t.Fatalf("output after lease: %v", err)
	}
}

func TestUpsertParticipantByDeviceKeepsOwnerAndDedupes(t *testing.T) {
	home := t.TempDir()
	closeTerminalDBs()
	store, err := newTerminalStore(home)
	if err != nil {
		t.Fatal(err)
	}
	now := terminalNow()
	id := "ts_part_dup"
	if err := store.InsertSession(TerminalSession{
		ID: id, Name: "P", State: TerminalStateRunning, Mode: TerminalModeManaged,
		OwnerDeviceID: "dev1", HostDeviceID: "dev1", WorkingDirectory: home, Command: "/bin/sh",
		CreatedAt: now, UpdatedAt: now, IPCToken: "x",
	}); err != nil {
		t.Fatal(err)
	}
	owner := TerminalParticipant{
		ID: "tp_a", SessionID: id, DeviceID: "dev1", Role: "owner",
		Connected: true, LastSeenAt: now, CreatedAt: now,
	}
	if err := store.insertParticipant(owner); err != nil {
		t.Fatal(err)
	}
	if err := store.UpsertParticipantByDevice(TerminalParticipant{
		SessionID: id, DeviceID: "dev1", Role: "observer", Connected: true,
	}); err != nil {
		t.Fatal(err)
	}
	parts, err := store.ListParticipants(id)
	if err != nil {
		t.Fatal(err)
	}
	if len(parts) != 1 {
		t.Fatalf("want 1 participant, got %d", len(parts))
	}
	if parts[0].Role != "owner" {
		t.Fatalf("role=%s", parts[0].Role)
	}
}

func TestListSessionsReapsMissingSocket(t *testing.T) {
	home := t.TempDir()
	closeTerminalDBs()
	mgr, err := newTerminalManager(home)
	if err != nil {
		t.Fatal(err)
	}
	now := terminalNow()
	id := "ts_dead_sock"
	if err := mgr.store.InsertSession(TerminalSession{
		ID: id, Name: "Dead", State: TerminalStateRunning, Mode: TerminalModeManaged,
		OwnerDeviceID: mgr.device.DeviceID, HostDeviceID: mgr.device.DeviceID,
		WorkingDirectory: home, Command: "/bin/sh",
		SocketPath: terminalSocketPath(home, id),
		CreatedAt:  now, UpdatedAt: now, IPCToken: "x",
	}); err != nil {
		t.Fatal(err)
	}
	list, err := mgr.ListSessions(10)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 || list[0].State != TerminalStateFailed {
		t.Fatalf("expected FAILED reap, got %+v", list)
	}
}

func TestValidateSessionHostLayout(t *testing.T) {
	home := t.TempDir()
	if err := validateSessionHostLayout(home, "ts_ok"); err != nil {
		t.Fatal(err)
	}
	if err := validateSessionHostLayout(home, "../escape"); err == nil {
		t.Fatal("expected rejection for traversal id")
	}
}

func stringsContains(s, sub string) bool {
	return len(sub) == 0 || (len(s) >= len(sub) && (s == sub || len(s) > 0 && containsSub(s, sub)))
}

func containsSub(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
