package main

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os/user"
	"path/filepath"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"
)

// LocalUser is the human profile stored beside this device's identity.
// The user id is not a device id (dev_…) and not a harness id.
type LocalUser struct {
	UserID      string `json:"user_id"`
	DisplayName string `json:"display_name"`
	CreatedAt   string `json:"created_at"`
}

var (
	localUserIDPattern = regexp.MustCompile(`^usr_[0-9a-f]{16}$`)
	foreignIDPattern   = regexp.MustCompile(`^(usr|dev)_[0-9a-f]{16}$`)
)

func localUserPath(home string) string {
	return filepath.Join(shareDir(home), "user.json")
}

func loadOrCreateLocalUser(home string) (LocalUser, error) {
	if err := hookMkdirAll(shareDir(home), 0o700); err != nil {
		return LocalUser{}, err
	}
	if data, err := hookReadFile(localUserPath(home)); err == nil {
		return parseLocalUser(data)
	}
	id, err := newLocalUserID()
	if err != nil {
		return LocalUser{}, err
	}
	profile := LocalUser{
		UserID:      id,
		DisplayName: defaultLocalDisplayName(),
		CreatedAt:   nowRFC3339(),
	}
	if err := validateLocalUser(profile); err != nil {
		return LocalUser{}, err
	}
	if err := writeLocalUser(home, profile); err != nil {
		return LocalUser{}, err
	}
	return profile, nil
}

func setLocalUserDisplayName(home, name string) (LocalUser, error) {
	profile, err := loadOrCreateLocalUser(home)
	if err != nil {
		return LocalUser{}, err
	}
	cleaned, err := validDisplayName(name)
	if err != nil {
		return LocalUser{}, err
	}
	if cleaned == profile.DisplayName {
		return profile, nil
	}
	profile.DisplayName = cleaned
	if err := writeLocalUser(home, profile); err != nil {
		return LocalUser{}, err
	}
	return profile, nil
}

// showLocalUser is a pure label for this device's user profile.
func showLocalUser(user LocalUser) string {
	return user.DisplayName + " (" + user.UserID + ")"
}

func defaultLocalDisplayName() string {
	current, err := user.Current()
	if err == nil {
		for _, candidate := range []string{gecosName(current.Name), current.Username} {
			if name, err := validDisplayName(candidate); err == nil {
				return name
			}
		}
	}
	return "local"
}

func gecosName(raw string) string {
	raw = strings.TrimSpace(raw)
	if i := strings.IndexByte(raw, ','); i >= 0 {
		raw = raw[:i]
	}
	return strings.TrimSpace(raw)
}

func newLocalUserID() (string, error) {
	buf := make([]byte, 8)
	n, err := hookRandRead(buf)
	if err != nil {
		return "", err
	}
	if n != len(buf) {
		return "", errors.New("short random read")
	}
	return "usr_" + hex.EncodeToString(buf), nil
}

func writeLocalUser(home string, profile LocalUser) error {
	if err := hookMkdirAll(shareDir(home), 0o700); err != nil {
		return err
	}
	data, err := hookJSONMarshalIndent(profile, "", "  ")
	if err != nil {
		return err
	}
	return hookWriteFile(localUserPath(home), data, 0o600)
}

func parseLocalUser(data []byte) (LocalUser, error) {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	var profile LocalUser
	if err := dec.Decode(&profile); err != nil {
		return LocalUser{}, fmt.Errorf("parse user: %w", err)
	}
	if err := validateLocalUser(profile); err != nil {
		return LocalUser{}, err
	}
	profile.DisplayName = strings.TrimSpace(profile.DisplayName)
	return profile, nil
}

func validateLocalUser(profile LocalUser) error {
	if !localUserIDPattern.MatchString(profile.UserID) {
		return errors.New("invalid user id")
	}
	if _, err := validDisplayName(profile.DisplayName); err != nil {
		return err
	}
	if _, err := time.Parse(time.RFC3339, profile.CreatedAt); err != nil {
		return errors.New("invalid created_at")
	}
	return nil
}

func validDisplayName(name string) (string, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return "", errors.New("display name is empty")
	}
	if utf8.RuneCountInString(name) > 64 {
		return "", errors.New("display name is too long")
	}
	for _, r := range name {
		if r < 0x20 || r == 0x7f {
			return "", errors.New("display name has control characters")
		}
	}
	if foreignIDPattern.MatchString(name) {
		return "", errors.New("display name must not be a device or user id")
	}
	return name, nil
}
