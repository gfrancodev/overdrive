package main

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

func setupStatusFor(t *testing.T, home string) setupStatus {
	t.Helper()
	rec := inviteHTTP(t, home, http.MethodGet, "/api/setup/status", "127.0.0.1:9", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("setup status %d: %s", rec.Code, rec.Body.String())
	}
	var st setupStatus
	if err := json.Unmarshal(rec.Body.Bytes(), &st); err != nil {
		t.Fatal(err)
	}
	return st
}

func TestSetupStatusFollowsTheCoreState(t *testing.T) {
	home := t.TempDir()
	setupTestEnv(t, home)
	if st := setupStatusFor(t, home); st.Next != "circle" || st.DeviceID == "" {
		t.Fatalf("a fresh device must start at creating or joining a circle: %+v", st)
	}
	id, priv, _ := loadOrCreateIdentity(home)
	circle, err := createCircle(home, "team", id, priv)
	if err != nil {
		t.Fatal(err)
	}
	if st := setupStatusFor(t, home); st.Next != "peer" {
		t.Fatalf("a circle without another device must ask to pair: %+v", st)
	}
	peer := newProofDevice(t)
	circle = circle.admit(peer.member(), proofDevice{id: id, priv: priv}.admit(t, circle.ID, peer))
	if err := saveCircle(home, circle); err != nil {
		t.Fatal(err)
	}
	if st := setupStatusFor(t, home); st.Next != "folder" {
		t.Fatalf("paired but nothing shared must ask for a folder: %+v", st)
	}
	if _, err := addAllowedFolder(home, circle.ID, home, id); err != nil {
		t.Fatal(err)
	}
	st := setupStatusFor(t, home)
	if st.Next != "ready" {
		t.Fatalf("everything done must be ready: %+v", st)
	}
	for _, step := range st.Steps {
		if !step.Done {
			t.Fatalf("step %s not done in a ready setup", step.ID)
		}
	}
}

func TestCirclesAPIListsWithoutKeyMaterial(t *testing.T) {
	home := t.TempDir()
	setupTestEnv(t, home)
	id, priv, _ := loadOrCreateIdentity(home)
	team, _ := createCircle(home, "team", id, priv)
	squad, _ := createCircle(home, "squad", id, priv)
	peer := newProofDevice(t)
	team = team.admit(peer.member(), proofDevice{id: id, priv: priv}.admit(t, team.ID, peer))
	if err := saveCircle(home, team); err != nil {
		t.Fatal(err)
	}
	rec := inviteHTTP(t, home, http.MethodGet, "/api/circles", "127.0.0.1:9", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("list %d", rec.Code)
	}
	body := rec.Body.String()
	for _, secret := range []string{team.GroupKey, squad.GroupKey, "group_key", "previous_key"} {
		if strings.Contains(body, secret) {
			t.Fatalf("the circle list must not carry key material (%q)", secret)
		}
	}
	var out struct {
		Circles []circleView `json:"circles"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil || len(out.Circles) != 2 {
		t.Fatalf("circles %+v %v", out, err)
	}
	for _, c := range out.Circles {
		if c.ID != team.ID {
			continue
		}
		if !c.IsCreator || len(c.Members) != 2 {
			t.Fatalf("team view %+v", c)
		}
		for _, m := range c.Members {
			if m.DeviceID == peer.id.DeviceID && m.InvitedBy != id.DeviceID {
				t.Fatalf("each member shows who invited it: %+v", m)
			}
		}
	}
	if rec := inviteHTTP(t, home, http.MethodGet, "/api/circles", "192.168.1.9:9", nil); rec.Code != http.StatusForbidden {
		t.Fatalf("a remote caller must not list circles, got %d", rec.Code)
	}
}

func TestCirclesAPICreatesAndRenames(t *testing.T) {
	home := t.TempDir()
	setupTestEnv(t, home)
	rec := inviteHTTP(t, home, http.MethodPost, "/api/circles", "127.0.0.1:9", []byte(`{"name":"Squad"}`))
	if rec.Code != http.StatusCreated {
		t.Fatalf("create %d: %s", rec.Code, rec.Body.String())
	}
	var created struct {
		Circle circleView `json:"circle"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil || created.Circle.ID == "" || created.Circle.Name != "Squad" {
		t.Fatalf("created %+v %v", created, err)
	}
	if strings.Contains(rec.Body.String(), "group_key") {
		t.Fatal("creating a circle must not return its key")
	}
	if rec := inviteHTTP(t, home, http.MethodPost, "/api/circles", "127.0.0.1:9", []byte(`{"name":"  "}`)); rec.Code != http.StatusBadRequest {
		t.Fatalf("a blank name must be refused, got %d", rec.Code)
	}
	path := "/api/circles/" + created.Circle.ID
	if rec := inviteHTTP(t, home, http.MethodPatch, path, "127.0.0.1:9", []byte(`{"name":"Core team"}`)); rec.Code != http.StatusOK {
		t.Fatalf("rename %d: %s", rec.Code, rec.Body.String())
	}
	if got, _ := loadCircle(home, created.Circle.ID); got.Name != "Core team" {
		t.Fatalf("rename did not stick: %q", got.Name)
	}
	if rec := inviteHTTP(t, home, http.MethodPatch, "/api/circles/circle_missing01", "127.0.0.1:9", []byte(`{"name":"x"}`)); rec.Code != http.StatusNotFound {
		t.Fatalf("an unknown circle must be 404, got %d", rec.Code)
	}
}

func TestInviteNamesItsCircle(t *testing.T) {
	home := t.TempDir()
	setupTestEnv(t, home)
	if rec := inviteHTTP(t, home, http.MethodPost, "/api/circle/invite", "127.0.0.1:9", []byte(`{}`)); rec.Code != http.StatusBadRequest {
		t.Fatalf("with no circle the invite must ask to create one, got %d", rec.Code)
	}
	if circles, _ := listCircles(home); len(circles) != 0 {
		t.Fatal("issuing an invite must not create a circle behind the user's back")
	}
	id, priv, _ := loadOrCreateIdentity(home)
	first, _ := createCircle(home, "first", id, priv)
	second, _ := createCircle(home, "second", id, priv)
	if rec := inviteHTTP(t, home, http.MethodPost, "/api/circle/invite", "127.0.0.1:9", []byte(`{}`)); rec.Code != http.StatusBadRequest {
		t.Fatalf("with several circles the invite must name one, got %d", rec.Code)
	}
	rec := inviteHTTP(t, home, http.MethodPost, "/api/circle/invite", "127.0.0.1:9", []byte(`{"circle_id":"`+second.ID+`"}`))
	if rec.Code != http.StatusOK {
		t.Fatalf("invite %d: %s", rec.Code, rec.Body.String())
	}
	if issued := decodeIssuedInvite(t, rec.Body.Bytes()); issued.Invite.CircleID != second.ID {
		t.Fatalf("the invite went to %s, not the chosen %s", issued.Invite.CircleID, second.ID)
	}
	_ = first
}
