package main

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func newShareTestStore(t *testing.T, sessionID string) *TerminalStore {
	t.Helper()
	home := t.TempDir()
	closeTerminalDBs()
	store, err := newTerminalStore(home)
	if err != nil {
		t.Fatal(err)
	}
	now := terminalNow()
	if err := store.InsertSession(TerminalSession{
		ID: sessionID, Name: "S", State: TerminalStateRunning, Mode: TerminalModeManaged,
		OwnerDeviceID: "dev_owner", HostDeviceID: "dev_owner", WorkingDirectory: home, Command: "/bin/sh",
		CircleID: "circle_a", IPCToken: "tok", CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	return store
}

func TestTerminalShareGrantIsIdempotentAndRevocable(t *testing.T) {
	store := newShareTestStore(t, "ts_share")
	if ok, err := store.HasShare("ts_share", "dev_viewer"); err != nil || ok {
		t.Fatalf("no grant yet: ok=%v err=%v", ok, err)
	}
	first, err := store.GrantShare("ts_share", "dev_viewer", terminalSharePermissionObserve)
	if err != nil {
		t.Fatal(err)
	}
	again, err := store.GrantShare("ts_share", "dev_viewer", terminalSharePermissionObserve)
	if err != nil {
		t.Fatal(err)
	}
	if first.ID == "" || again.ID != first.ID {
		t.Fatalf("grant must be one row per session and device: %q %q", first.ID, again.ID)
	}
	if ok, err := store.HasShare("ts_share", "dev_viewer"); err != nil || !ok {
		t.Fatalf("granted device: ok=%v err=%v", ok, err)
	}
	if ok, _ := store.HasShare("ts_share", "dev_other"); ok {
		t.Fatal("grant must not extend to another device")
	}
	if err := store.RevokeShare("ts_share", "dev_viewer"); err != nil {
		t.Fatal(err)
	}
	if ok, _ := store.HasShare("ts_share", "dev_viewer"); ok {
		t.Fatal("revoked grant must not pass")
	}
}

func TestTerminalShareExpiredGrantDoesNotPass(t *testing.T) {
	store := newShareTestStore(t, "ts_exp")
	if _, err := store.GrantShare("ts_exp", "dev_viewer", terminalSharePermissionObserve); err != nil {
		t.Fatal(err)
	}
	past := time.Now().Add(-time.Minute).UTC().Format(time.RFC3339)
	if _, err := store.db.Exec(`UPDATE terminal_shares SET expires_at=? WHERE session_id=?`, past, "ts_exp"); err != nil {
		t.Fatal(err)
	}
	if ok, _ := store.HasShare("ts_exp", "dev_viewer"); ok {
		t.Fatal("expired grant must not pass")
	}
}

func TestTerminalShareRevokeAllForDevice(t *testing.T) {
	store := newShareTestStore(t, "ts_one")
	now := terminalNow()
	if err := store.InsertSession(TerminalSession{
		ID: "ts_two", Name: "S2", State: TerminalStateRunning, Mode: TerminalModeManaged,
		OwnerDeviceID: "dev_owner", HostDeviceID: "dev_owner", WorkingDirectory: store.home, Command: "/bin/sh",
		CircleID: "circle_a", IPCToken: "tok", CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	for _, sid := range []string{"ts_one", "ts_two"} {
		if _, err := store.GrantShare(sid, "dev_gone", terminalSharePermissionObserve); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := store.GrantShare("ts_one", "dev_stay", terminalSharePermissionObserve); err != nil {
		t.Fatal(err)
	}
	sessions, err := store.RevokeSharesForDevice("dev_gone")
	if err != nil {
		t.Fatal(err)
	}
	if len(sessions) != 2 {
		t.Fatalf("want both sessions reported, got %v", sessions)
	}
	for _, sid := range []string{"ts_one", "ts_two"} {
		if ok, _ := store.HasShare(sid, "dev_gone"); ok {
			t.Fatalf("grant on %s must be gone", sid)
		}
	}
	if ok, _ := store.HasShare("ts_one", "dev_stay"); !ok {
		t.Fatal("other devices keep their grants")
	}
}

type terminalChatShareFixture struct {
	home      string
	owner     DeviceIdentity
	friend    DeviceIdentity
	squad     DeviceIdentity
	bystander DeviceIdentity
	circle    Circle
	store     *TerminalStore
	sessionID string
}

func newTerminalChatShareFixture(t *testing.T) terminalChatShareFixture {
	t.Helper()
	home := meshHome(t)
	closeTerminalDBs()
	owner, _ := testDevice(t)
	friend, _ := testDevice(t)
	squad, _ := testDevice(t)
	bystander, _ := testDevice(t)
	writeIdentityFile(t, home, owner)
	circle := saveTestCircle(t, home, testCircle(owner, friend, squad, bystander))
	store := mustTerminalStore(t, home)
	now := terminalNow()
	sess := TerminalSession{
		ID: newTerminalSessionID(), Name: "live", State: TerminalStateRunning, Mode: TerminalModeManaged,
		OwnerDeviceID: owner.DeviceID, HostDeviceID: owner.DeviceID, CircleID: circle.ID,
		WorkingDirectory: home, Command: "/bin/sh", IPCToken: "tok", CreatedAt: now, UpdatedAt: now,
	}
	if err := store.InsertSession(sess); err != nil {
		t.Fatal(err)
	}
	return terminalChatShareFixture{home: home, owner: owner, friend: friend, squad: squad, bystander: bystander, circle: circle, store: store, sessionID: sess.ID}
}

func (f terminalChatShareFixture) card(sessionID string) chatPostRequest {
	return chatPostRequest{MeshID: f.circle.ID, Type: "terminal_session", Title: "live", SessionID: sessionID, Host: f.owner.DeviceID}
}

func (f terminalChatShareFixture) shared(t *testing.T, dev DeviceIdentity) bool {
	t.Helper()
	ok, err := f.store.HasShare(f.sessionID, dev.DeviceID)
	if err != nil {
		t.Fatal(err)
	}
	return ok
}

func TestTerminalChatShareGrantsOnlyTheConversationAudience(t *testing.T) {
	f := newTerminalChatShareFixture(t)
	dm := f.card(f.sessionID)
	dm.Recipient = f.friend.DeviceID
	if _, _, err := postLocalChat(f.home, dm); err != nil {
		t.Fatal(err)
	}
	if !f.shared(t, f.friend) || f.shared(t, f.squad) || f.shared(t, f.bystander) {
		t.Fatal("a direct share must reach only the recipient")
	}
	group, err := createChatGroup(f.home, chatGroupCreateRequest{MeshID: f.circle.ID, Name: "Squad", MemberDeviceIDs: []string{f.squad.DeviceID}})
	if err != nil {
		t.Fatal(err)
	}
	inGroup := f.card(f.sessionID)
	inGroup.ConversationID = group.ConversationID
	if _, _, err := postLocalChat(f.home, inGroup); err != nil {
		t.Fatal(err)
	}
	if !f.shared(t, f.squad) || f.shared(t, f.bystander) {
		t.Fatal("a group share must reach only the group members")
	}
	if ok, _ := f.store.HasShare(f.sessionID, f.owner.DeviceID); ok {
		t.Fatal("the owner never needs a share row")
	}
	everyone := f.card(f.sessionID)
	everyone.ConversationID = circleGroupConversationID()
	if _, _, err := postLocalChat(f.home, everyone); err != nil {
		t.Fatal(err)
	}
	if !f.shared(t, f.bystander) {
		t.Fatal("the circle channel share must reach every active member")
	}
}

func TestTerminalChatShareDirectConversationIDWithoutRecipient(t *testing.T) {
	f := newTerminalChatShareFixture(t)
	dm := f.card(f.sessionID)
	dm.ConversationID = directConversationID(f.owner.DeviceID, f.friend.DeviceID)
	if _, _, err := postLocalChat(f.home, dm); err != nil {
		t.Fatal(err)
	}
	if !f.shared(t, f.friend) || f.shared(t, f.bystander) {
		t.Fatal("the other side of the direct conversation must be the only grantee")
	}
}

func TestTerminalChatShareGrantsNothingForSessionsThisDeviceDoesNotOwn(t *testing.T) {
	f := newTerminalChatShareFixture(t)
	remote := f.card("ts_hosted_elsewhere")
	remote.Recipient = f.friend.DeviceID
	if _, _, err := postLocalChat(f.home, remote); err != nil {
		t.Fatalf("relaying a card for a remote session stays allowed: %v", err)
	}
	now := terminalNow()
	if err := f.store.InsertSession(TerminalSession{
		ID: "ts_not_mine", Name: "x", State: TerminalStateRunning, Mode: TerminalModeManaged,
		OwnerDeviceID: f.squad.DeviceID, HostDeviceID: f.owner.DeviceID, CircleID: f.circle.ID,
		WorkingDirectory: f.home, Command: "/bin/sh", IPCToken: "tok", CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	foreign := f.card("ts_not_mine")
	foreign.Recipient = f.friend.DeviceID
	if _, _, err := postLocalChat(f.home, foreign); err != nil {
		t.Fatal(err)
	}
	if ok, _ := f.store.HasShare("ts_not_mine", f.friend.DeviceID); ok {
		t.Fatal("only the owner may share a session")
	}
}

func TestTerminalChatShareRejectsSessionOutsideTheCircle(t *testing.T) {
	f := newTerminalChatShareFixture(t)
	now := terminalNow()
	if err := f.store.InsertSession(TerminalSession{
		ID: "ts_local_only", Name: "x", State: TerminalStateRunning, Mode: TerminalModeManaged,
		OwnerDeviceID: f.owner.DeviceID, HostDeviceID: f.owner.DeviceID,
		WorkingDirectory: f.home, Command: "/bin/sh", IPCToken: "tok", CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	card := f.card("ts_local_only")
	card.Recipient = f.friend.DeviceID
	if _, _, err := postLocalChat(f.home, card); err == nil {
		t.Fatal("a session outside this circle cannot be shared into it")
	}
	if msgs, _ := listChatConversations(f.home, f.circle.ID); len(msgs) != 0 {
		t.Fatalf("a refused share must not post the card: %+v", msgs)
	}
}

// abortInserts makes SQLite refuse inserts into table when the condition holds, standing in for a disk
// or lock failure at that step of a post.
func abortInserts(t *testing.T, db *sql.DB, table, when string) {
	t.Helper()
	if _, err := db.Exec(`CREATE TRIGGER abort_` + table + ` BEFORE INSERT ON ` + table +
		` WHEN ` + when + ` BEGIN SELECT RAISE(ABORT, 'injected failure'); END`); err != nil {
		t.Fatal(err)
	}
}

func countChatRows(t *testing.T, home, table string) int {
	t.Helper()
	db, err := openChatDB(home)
	if err != nil {
		t.Fatal(err)
	}
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM ` + table).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestTerminalChatShareWaitsForTheCardToBeSaved(t *testing.T) {
	f := newTerminalChatShareFixture(t)
	db, err := openChatDB(f.home)
	if err != nil {
		t.Fatal(err)
	}
	abortInserts(t, db, "messages", "1")
	dm := f.card(f.sessionID)
	dm.Recipient = f.friend.DeviceID
	if _, _, err := postLocalChat(f.home, dm); err == nil {
		t.Fatal("the post must fail when its card cannot be saved")
	}
	if f.shared(t, f.friend) {
		t.Fatal("a card that was never saved must not leave a share behind")
	}
}

func TestTerminalChatShareFailureDropsTheCardAndEveryGrant(t *testing.T) {
	f := newTerminalChatShareFixture(t)
	abortInserts(t, f.store.db, "terminal_shares", "NEW.target_id = '"+f.bystander.DeviceID+"'")
	everyone := f.card(f.sessionID)
	everyone.ConversationID = circleGroupConversationID()
	if _, _, err := postLocalChat(f.home, everyone); err == nil {
		t.Fatal("the post must fail when its share cannot be granted")
	}
	if f.shared(t, f.friend) || f.shared(t, f.squad) {
		t.Fatal("a failed grant must not leave part of the audience shared")
	}
	if n := countChatRows(t, f.home, "messages"); n != 0 {
		t.Fatalf("the card must be dropped when its share fails, %d messages kept", n)
	}
	if n := countChatRows(t, f.home, "outbox"); n != 0 {
		t.Fatalf("the card must not stay queued for the mesh, %d outbox rows kept", n)
	}
}

func TestTerminalInviteGrantsActiveMemberOnly(t *testing.T) {
	f := newTerminalChatShareFixture(t)
	invite := func(target string) *httptest.ResponseRecorder {
		body, _ := json.Marshal(map[string]string{"target_device_id": target, "permission": "observe"})
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/api/v1/terminal/sessions/"+f.sessionID+"/invite", bytes.NewReader(body))
		serveTerminalAPI(rec, req, f.home)
		return rec
	}
	if rec := invite(f.friend.DeviceID); rec.Code == http.StatusOK {
		t.Fatal("an invite that cannot be delivered must fail")
	}
	if f.shared(t, f.friend) {
		t.Fatal("an undelivered invite must not leave a grant")
	}
	if err := pauseMesh(f.home); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		meshMu.Lock()
		meshPausedFlag = false
		meshMu.Unlock()
	})
	if rec := invite(f.friend.DeviceID); rec.Code != http.StatusOK {
		t.Fatalf("invite member status=%d body=%s", rec.Code, rec.Body.String())
	}
	if !f.shared(t, f.friend) {
		t.Fatal("an invite must grant the invited device")
	}
	if rec := invite("dev_not_in_circle"); rec.Code == http.StatusOK {
		t.Fatal("an invite to a non-member must fail")
	}
	if ok, _ := f.store.HasShare(f.sessionID, "dev_not_in_circle"); ok {
		t.Fatal("a failed invite must not leave a grant")
	}
}

func TestRevokeTerminalAccessForDeviceStaysInsideTheCircle(t *testing.T) {
	store := newShareTestStore(t, "ts_a1")
	now := terminalNow()
	for id, circleID := range map[string]string{"ts_a2": "circle_a", "ts_b1": "circle_b"} {
		if err := store.InsertSession(TerminalSession{
			ID: id, Name: id, State: TerminalStateRunning, Mode: TerminalModeManaged,
			OwnerDeviceID: "dev_owner", HostDeviceID: "dev_owner", WorkingDirectory: store.home, Command: "/bin/sh",
			CircleID: circleID, IPCToken: "tok", CreatedAt: now, UpdatedAt: now,
		}); err != nil {
			t.Fatal(err)
		}
	}
	for _, sid := range []string{"ts_a1", "ts_a2", "ts_b1"} {
		if _, err := store.GrantShare(sid, "dev_gone", terminalSharePermissionObserve); err != nil {
			t.Fatal(err)
		}
	}
	exp := time.Now().Add(time.Hour).UTC().Format(time.RFC3339)
	if _, err := store.grantLeaseCAS("ts_a1", "dev_gone", 0, exp); err != nil {
		t.Fatal(err)
	}
	if _, err := store.grantLeaseCAS("ts_a2", "dev_other", 0, exp); err != nil {
		t.Fatal(err)
	}
	if err := revokeTerminalAccessForDevice(store.home, "circle_a", "dev_gone"); err != nil {
		t.Fatal(err)
	}
	for _, sid := range []string{"ts_a1", "ts_a2"} {
		if ok, _ := store.HasShare(sid, "dev_gone"); ok {
			t.Fatalf("share on %s must be dropped", sid)
		}
	}
	if ok, _ := store.HasShare("ts_b1", "dev_gone"); !ok {
		t.Fatal("a share in another circle must survive")
	}
	if lease, _, _ := store.GetActiveLease("ts_a1"); lease.HolderDeviceID != "dev_owner" {
		t.Fatalf("control held by the revoked device must return to the owner: %+v", lease)
	}
	if lease, _, _ := store.GetActiveLease("ts_a2"); lease.HolderDeviceID != "dev_other" {
		t.Fatalf("control held by someone else must stay: %+v", lease)
	}
}
