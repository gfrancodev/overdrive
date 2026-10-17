package main

import (
	"testing"
	"time"
)

func TestControlLeaseEpochCAS(t *testing.T) {
	home := t.TempDir()
	closeTerminalDBs()
	store, err := newTerminalStore(home)
	if err != nil {
		t.Fatal(err)
	}
	sess := TerminalSession{
		ID: newTerminalSessionID(), Name: "t", State: TerminalStateRunning, Mode: TerminalModeManaged,
		OwnerDeviceID: "dev_owner", HostDeviceID: "dev_owner", WorkingDirectory: home, Command: "sh",
		CreatedAt: terminalNow(), UpdatedAt: terminalNow(),
	}
	if err := store.InsertSession(sess); err != nil {
		t.Fatal(err)
	}
	ctrl := newTerminalControl(store, "dev_owner")
	exp := time.Now().Add(time.Hour).UTC().Format(time.RFC3339)
	lease, err := ctrl.GrantControl(sess.ID, "dev_owner", "dev_owner", 0, exp)
	if err != nil {
		t.Fatal(err)
	}
	if lease.Epoch != 1 {
		t.Fatalf("epoch=%d", lease.Epoch)
	}
	_, err = ctrl.GrantControl(sess.ID, "dev_owner", "dev_peer", 0, exp)
	if err == nil {
		t.Fatal("expected CAS failure on stale epoch")
	}
	lease2, err := ctrl.GrantControl(sess.ID, "dev_owner", "dev_peer", lease.Epoch, exp)
	if err != nil {
		t.Fatal(err)
	}
	if lease2.Epoch != 2 {
		t.Fatalf("epoch=%d", lease2.Epoch)
	}
	if err := ctrl.ValidateWrite(sess.ID, "dev_peer", lease2.ID, lease2.Epoch); err != nil {
		t.Fatal(err)
	}
	if err := ctrl.ValidateWrite(sess.ID, "dev_peer", lease2.ID, 1); err == nil {
		t.Fatal("expected stale epoch rejection")
	}
}

func TestControlReleaseBumpsEpoch(t *testing.T) {
	home := t.TempDir()
	closeTerminalDBs()
	store, err := newTerminalStore(home)
	if err != nil {
		t.Fatal(err)
	}
	sess := TerminalSession{
		ID: newTerminalSessionID(), Name: "t", State: TerminalStateRunning, Mode: TerminalModeManaged,
		OwnerDeviceID: "dev_a", HostDeviceID: "dev_a", WorkingDirectory: home, Command: "sh",
		CreatedAt: terminalNow(), UpdatedAt: terminalNow(),
	}
	if err := store.InsertSession(sess); err != nil {
		t.Fatal(err)
	}
	ctrl := newTerminalControl(store, "dev_a")
	exp := time.Now().Add(time.Hour).UTC().Format(time.RFC3339)
	lease, err := ctrl.GrantControl(sess.ID, "dev_a", "dev_a", 0, exp)
	if err != nil {
		t.Fatal(err)
	}
	after, err := ctrl.ReleaseControl(sess.ID, "dev_a", lease.ID, lease.Epoch)
	if err != nil {
		t.Fatal(err)
	}
	if after.Epoch <= lease.Epoch {
		t.Fatalf("epoch not bumped: before=%d after=%d", lease.Epoch, after.Epoch)
	}
}

func TestControlRejectAndRevoke(t *testing.T) {
	home := t.TempDir()
	closeTerminalDBs()
	store, err := newTerminalStore(home)
	if err != nil {
		t.Fatal(err)
	}
	sess := TerminalSession{
		ID: newTerminalSessionID(), Name: "t", State: TerminalStateRunning, Mode: TerminalModeManaged,
		OwnerDeviceID: "dev_owner", HostDeviceID: "dev_owner", WorkingDirectory: home, Command: "sh",
		CreatedAt: terminalNow(), UpdatedAt: terminalNow(),
	}
	if err := store.InsertSession(sess); err != nil {
		t.Fatal(err)
	}
	ctrl := newTerminalControl(store, "dev_owner")
	exp := time.Now().Add(time.Hour).UTC().Format(time.RFC3339)
	lease, err := ctrl.GrantControl(sess.ID, "dev_owner", "dev_peer", 0, exp)
	if err != nil {
		t.Fatal(err)
	}
	if _, epoch, err := ctrl.CurrentLease(sess.ID); err != nil || epoch != lease.Epoch {
		t.Fatalf("current lease epoch=%d err=%v", epoch, err)
	}
	if err := ctrl.RejectControl(sess.ID, "dev_intruder", "dev_peer", "no"); err == nil {
		t.Fatal("non-owner cannot reject")
	}
	if err := ctrl.RejectControl(sess.ID, "dev_owner", "dev_peer", "busy"); err != nil {
		t.Fatal(err)
	}
	revoked, err := ctrl.RevokeControl(sess.ID, "dev_owner", "dev_peer", "policy")
	if err != nil {
		t.Fatal(err)
	}
	if revoked.HolderDeviceID != "dev_owner" {
		t.Fatalf("revoke holder %+v", revoked)
	}
	if err := revokeTerminalAccessForDevice(home, "", "dev_peer"); err != nil {
		t.Fatal(err)
	}
	closeTerminalStreamsForDevice(sess.ID, "dev_peer")
}

func TestPendingControlRequesters(t *testing.T) {
	home := t.TempDir()
	closeTerminalDBs()
	store, err := newTerminalStore(home)
	if err != nil {
		t.Fatal(err)
	}
	sess := TerminalSession{
		ID: newTerminalSessionID(), Name: "t", State: TerminalStateRunning, Mode: TerminalModeManaged,
		OwnerDeviceID: "dev_owner", HostDeviceID: "dev_owner", WorkingDirectory: home, Command: "sh",
		CreatedAt: terminalNow(), UpdatedAt: terminalNow(),
	}
	if err := store.InsertSession(sess); err != nil {
		t.Fatal(err)
	}
	ctrl := newTerminalControl(store, "dev_peer")
	if err := ctrl.RequestControl(sess.ID, "dev_peer"); err != nil {
		t.Fatal(err)
	}
	pending, err := store.PendingControlRequesters(sess.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(pending) != 1 || pending[0] != "dev_peer" {
		t.Fatalf("pending=%v", pending)
	}
	exp := time.Now().Add(time.Hour).UTC().Format(time.RFC3339)
	ownerCtrl := newTerminalControl(store, "dev_owner")
	if _, err := ownerCtrl.GrantControl(sess.ID, "dev_owner", "dev_peer", 0, exp); err != nil {
		t.Fatal(err)
	}
	pending, err = store.PendingControlRequesters(sess.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(pending) != 0 {
		t.Fatalf("expected no pending after grant: %v", pending)
	}
}
