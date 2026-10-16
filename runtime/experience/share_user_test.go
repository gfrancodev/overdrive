package main

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestLocalUserCreateIsStableAndSeparateFromDevice(t *testing.T) {
	home := t.TempDir()
	resetHooksForTest()
	t.Cleanup(resetHooksForTest)

	hookRandRead = func(p []byte) (int, error) {
		for i := range p {
			p[i] = 0xAB
		}
		return len(p), nil
	}
	user, err := loadOrCreateLocalUser(home)
	if err != nil {
		t.Fatal(err)
	}
	if user.UserID != "usr_abababababababab" {
		t.Fatalf("user id %q", user.UserID)
	}
	if user.DisplayName != defaultLocalDisplayName() {
		t.Fatalf("display name %q", user.DisplayName)
	}
	if _, err := time.Parse(time.RFC3339, user.CreatedAt); err != nil {
		t.Fatal(err)
	}
	if strings.HasPrefix(user.UserID, "dev_") || user.UserID == "harness" || user.UserID == "cursor" {
		t.Fatalf("mixed id %q", user.UserID)
	}

	info, err := os.Stat(localUserPath(home))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("file mode %o", info.Mode().Perm())
	}
	dirInfo, err := os.Stat(shareDir(home))
	if err != nil {
		t.Fatal(err)
	}
	if dirInfo.Mode().Perm() != 0o700 {
		t.Fatalf("dir mode %o", dirInfo.Mode().Perm())
	}
	assertLocalUserJSONKeys(t, home)

	hookRandRead = func([]byte) (int, error) {
		return 0, errors.New("rand must not run when the profile exists")
	}
	again, err := loadOrCreateLocalUser(home)
	if err != nil {
		t.Fatal(err)
	}
	if again != user {
		t.Fatalf("unstable profile %+v vs %+v", again, user)
	}

	resetHooksForTest()
	identity, _, err := loadOrCreateIdentity(home)
	if err != nil {
		t.Fatal(err)
	}
	if identity.DeviceID == user.UserID || strings.HasPrefix(identity.DeviceID, "usr_") {
		t.Fatalf("device id %q user id %q", identity.DeviceID, user.UserID)
	}
	raw, err := os.ReadFile(localUserPath(home))
	if err != nil {
		t.Fatal(err)
	}
	text := string(raw)
	for _, forbidden := range []string{"device_id", "harness_id", "private_key", "public_key", identity.DeviceID} {
		if strings.Contains(text, forbidden) {
			t.Fatalf("user file contains %q", forbidden)
		}
	}
	shown := showLocalUser(user)
	if shown != user.DisplayName+" ("+user.UserID+")" {
		t.Fatalf("show %q", shown)
	}
	if strings.Contains(shown, identity.DeviceID) || strings.Contains(shown, "harness") {
		t.Fatalf("show mixes identities %q", shown)
	}
}

func TestLocalUserHomesAreNotLinked(t *testing.T) {
	homeA := t.TempDir()
	homeB := t.TempDir()
	userA, err := loadOrCreateLocalUser(homeA)
	if err != nil {
		t.Fatal(err)
	}
	userB, err := loadOrCreateLocalUser(homeB)
	if err != nil {
		t.Fatal(err)
	}
	if userA.UserID == userB.UserID {
		t.Fatal("two homes share a user id")
	}
	rawB, err := os.ReadFile(localUserPath(homeB))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(rawB), userA.UserID) {
		t.Fatal("home B stored home A user id")
	}
}

func TestLocalUserDisplayNameUpdateKeepsID(t *testing.T) {
	home := t.TempDir()
	original, err := loadOrCreateLocalUser(home)
	if err != nil {
		t.Fatal(err)
	}
	updated, err := setLocalUserDisplayName(home, "  José  ")
	if err != nil {
		t.Fatal(err)
	}
	if updated.UserID != original.UserID || updated.CreatedAt != original.CreatedAt {
		t.Fatalf("identity changed %+v", updated)
	}
	if updated.DisplayName != "José" {
		t.Fatalf("name %q", updated.DisplayName)
	}
	if showLocalUser(updated) != "José ("+original.UserID+")" {
		t.Fatalf("show %q", showLocalUser(updated))
	}
	loaded, err := loadOrCreateLocalUser(home)
	if err != nil {
		t.Fatal(err)
	}
	if loaded != updated {
		t.Fatalf("not persisted %+v", loaded)
	}
	info, err := os.Stat(localUserPath(home))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("file mode %o", info.Mode().Perm())
	}

	for _, name := range []string{"", "   ", original.UserID, "dev_0123456789abcdef", "usr_0123456789abcdef", strings.Repeat("a", 65), "line\nbreak"} {
		if _, err := setLocalUserDisplayName(home, name); err == nil {
			t.Fatalf("accepted %q", name)
		}
	}
	kept, err := loadOrCreateLocalUser(home)
	if err != nil {
		t.Fatal(err)
	}
	if kept != updated {
		t.Fatalf("rejected rename changed profile %+v", kept)
	}
}

func TestLocalUserRejectsMixedOrCorruptFile(t *testing.T) {
	home := t.TempDir()
	if err := os.MkdirAll(shareDir(home), 0o700); err != nil {
		t.Fatal(err)
	}
	created := nowRFC3339()
	cases := []string{
		"{bad json",
		`{"user_id":"dev_abababababababab","display_name":"Ada","created_at":"` + created + `"}`,
		`{"user_id":"cursor","display_name":"Ada","created_at":"` + created + `"}`,
		`{"user_id":"usr_abababababababab","display_name":"Ada","created_at":"` + created + `","device_id":"dev_0123456789abcdef"}`,
		`{"user_id":"usr_abababababababab","display_name":"Ada","created_at":"` + created + `","harness_id":"cursor"}`,
		`{"user_id":"usr_abababababababab","display_name":"","created_at":"` + created + `"}`,
		`{"user_id":"usr_abababababababab","display_name":"Ada","created_at":"yesterday"}`,
	}
	for _, body := range cases {
		if err := os.WriteFile(localUserPath(home), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := loadOrCreateLocalUser(home); err == nil {
			t.Fatalf("accepted %s", body)
		}
		raw, err := os.ReadFile(localUserPath(home))
		if err != nil {
			t.Fatal(err)
		}
		if string(raw) != body {
			t.Fatal("corrupt profile was rewritten")
		}
	}
}

func TestLocalUserCreateErrorPaths(t *testing.T) {
	home := t.TempDir()
	resetHooksForTest()
	t.Cleanup(resetHooksForTest)

	hookMkdirAll = func(string, os.FileMode) error {
		return errors.New("share mkdir fail")
	}
	if _, err := loadOrCreateLocalUser(home); err == nil {
		t.Fatal("mkdir fail")
	}

	resetHooksForTest()
	hookRandRead = func([]byte) (int, error) {
		return 0, errors.New("rand fail")
	}
	if _, err := loadOrCreateLocalUser(home); err == nil {
		t.Fatal("rand fail")
	}
	if _, err := os.Stat(localUserPath(home)); !os.IsNotExist(err) {
		t.Fatal("wrote profile after rand failure")
	}

	resetHooksForTest()
	hookRandRead = func(p []byte) (int, error) {
		if len(p) == 0 {
			return 0, nil
		}
		p[0] = 1
		return 1, nil
	}
	if _, err := loadOrCreateLocalUser(home); err == nil {
		t.Fatal("short rand")
	}

	resetHooksForTest()
	hookJSONMarshalIndent = func(any, string, string) ([]byte, error) {
		return nil, errors.New("marshal indent fail")
	}
	if _, err := loadOrCreateLocalUser(home); err == nil {
		t.Fatal("marshal fail")
	}

	resetHooksForTest()
	hookWriteFile = func(string, []byte, os.FileMode) error {
		return errors.New("write user fail")
	}
	if _, err := loadOrCreateLocalUser(home); err == nil {
		t.Fatal("write fail")
	}
}

func TestShowLocalUserIsPure(t *testing.T) {
	home := t.TempDir()
	user := LocalUser{UserID: "usr_0123456789abcdef", DisplayName: "Ada", CreatedAt: "2026-01-01T00:00:00Z"}
	if showLocalUser(user) != "Ada (usr_0123456789abcdef)" {
		t.Fatalf("show %q", showLocalUser(user))
	}
	if _, err := os.Stat(filepath.Join(home, "share")); !os.IsNotExist(err) {
		t.Fatal("show touched the filesystem")
	}
}

func assertLocalUserJSONKeys(t *testing.T, home string) {
	t.Helper()
	raw, err := os.ReadFile(localUserPath(home))
	if err != nil {
		t.Fatal(err)
	}
	var payload map[string]any
	if err := json.Unmarshal(raw, &payload); err != nil {
		t.Fatal(err)
	}
	allowed := map[string]bool{"user_id": true, "display_name": true, "created_at": true}
	if len(payload) != len(allowed) {
		t.Fatalf("keys %v", payload)
	}
	for key := range payload {
		if !allowed[key] {
			t.Fatalf("unexpected key %s", key)
		}
	}
}
