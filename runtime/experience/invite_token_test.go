package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func handOverInvite(t *testing.T, invite CircleInvite, home string) {
	t.Helper()
	if err := storeInviteFile(home, invite); err != nil {
		t.Fatal(err)
	}
}

func TestInvitesCarryAOneTimeTokenInsteadOfTheCircleKey(t *testing.T) {
	home := t.TempDir()
	setupTestEnv(t, home)
	id, priv, _ := loadOrCreateIdentity(home)
	circle, _ := createCircle(home, "team", id, priv)
	invite, err := createInvite(home, circle.ID, id)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(invite)
	if invite.GroupKey != "" || strings.Contains(string(raw), circle.GroupKey) {
		t.Fatal("an invite must not carry the circle key")
	}
	if invite.Version < 2 || len(invite.Secret) < 20 {
		t.Fatalf("an invite needs a long one-time secret: %+v", invite)
	}
}

func TestJoiningWithAnInviteTokenTakesTheKeyFromTheInviter(t *testing.T) {
	setupTestEnv(t, t.TempDir())
	inviter := startRosterCore(t, "inviter")
	joiner := startRosterCore(t, "joiner")
	other := startRosterCore(t, "other")
	circle, err := createCircle(inviter.home, "team", inviter.id, inviter.priv)
	if err != nil {
		t.Fatal(err)
	}
	invite, err := createInvite(inviter.home, circle.ID, inviter.id)
	if err != nil {
		t.Fatal(err)
	}

	handOverInvite(t, invite, joiner.home)
	got, err := acceptInvite(joiner.home, invite.Code, invite.Fingerprint, inviter.endpoint, joiner.id, joiner.priv)
	if err != nil {
		t.Fatal(err)
	}
	if got.GroupKey != circle.GroupKey || got.CreatorDeviceID != inviter.id.DeviceID {
		t.Fatal("the joiner must receive the circle key sealed to it by the inviter")
	}
	if adm := inviter.circle(t, circle.ID).Admissions[joiner.id.DeviceID]; adm.InvitedBy != inviter.id.DeviceID {
		t.Fatal("the inviter must sign the joiner's admission")
	}
	if _, err := os.Stat(filepath.Join(invitesDir(inviter.home), invite.Code+".json")); err == nil {
		t.Fatal("an invite works once: the inviter must drop it after use")
	}

	handOverInvite(t, invite, other.home)
	if _, err := acceptInvite(other.home, invite.Code, invite.Fingerprint, inviter.endpoint, other.id, other.priv); err == nil {
		t.Fatal("a used invite must not let a second device in")
	}
}

func TestAnInviteWithAWrongSecretLetsNobodyIn(t *testing.T) {
	setupTestEnv(t, t.TempDir())
	inviter := startRosterCore(t, "inviter")
	joiner := startRosterCore(t, "joiner")
	circle, err := createCircle(inviter.home, "team", inviter.id, inviter.priv)
	if err != nil {
		t.Fatal(err)
	}
	invite, err := createInvite(inviter.home, circle.ID, inviter.id)
	if err != nil {
		t.Fatal(err)
	}
	forged := invite
	forged.Secret = strings.Repeat("A", len(invite.Secret))
	handOverInvite(t, forged, joiner.home)
	if _, err := acceptInvite(joiner.home, invite.Code, invite.Fingerprint, inviter.endpoint, joiner.id, joiner.priv); err == nil {
		t.Fatal("an invite with the wrong secret must be refused")
	}
	if _, ok := inviter.circle(t, circle.ID).memberRecord(joiner.id.DeviceID); ok {
		t.Fatal("a refused invite must admit nobody")
	}
	if _, err := os.Stat(filepath.Join(invitesDir(inviter.home), invite.Code+".json")); err != nil {
		t.Fatal("a failed attempt must not burn the real invite")
	}
}
