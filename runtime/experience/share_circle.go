package main

import (
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type CircleMember struct {
	DeviceID  string `json:"device_id"`
	PublicKey string `json:"public_key"`
	AddedAt   string `json:"added_at"`
	Revoked   bool   `json:"revoked"`
}

type Circle struct {
	ID                  string         `json:"id"`
	Name                string         `json:"name"`
	GroupKey            string         `json:"group_key"`
	CreatorDeviceID     string         `json:"creator_device_id"`
	Members             []CircleMember `json:"members"`
	AllowedFolders      []string       `json:"allowed_folders"`
	PeerEndpoints       []string       `json:"peer_endpoints"`
	MemberListSignature string         `json:"member_list_signature"`
	CreatedAt           string         `json:"created_at"`
	UpdatedAt           string         `json:"updated_at"`
}

type CircleInvite struct {
	CircleID    string `json:"circle_id"`
	CircleName  string `json:"circle_name"`
	Code        string `json:"code"`
	Fingerprint string `json:"fingerprint"`
	GroupKey    string `json:"group_key"`
	ExpiresAt   string `json:"expires_at"`
	CreatorPK   string `json:"creator_public_key"`
	CreatorID   string `json:"creator_device_id"`
}

func circlesDir(home string) string {
	return filepath.Join(shareDir(home), "circles")
}

func invitesDir(home string) string {
	return filepath.Join(shareDir(home), "invites")
}

func circlePath(home, circleID string) string {
	return filepath.Join(circlesDir(home), circleID+".json")
}

func loadCircle(home, circleID string) (Circle, error) {
	data, err := hookReadFile(circlePath(home, circleID))
	if err != nil {
		return Circle{}, err
	}
	var c Circle
	if err := json.Unmarshal(data, &c); err != nil {
		return Circle{}, err
	}
	return c, nil
}

func saveCircle(home string, c Circle) error {
	if err := hookMkdirAll(circlesDir(home), 0o700); err != nil {
		return err
	}
	c.UpdatedAt = nowRFC3339()
	data, err := hookJSONMarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	return hookWriteFile(circlePath(home, c.ID), data, 0o600)
}

func listCircles(home string) ([]Circle, error) {
	dir := circlesDir(home)
	if err := hookMkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	entries, err := hookReadDir(dir)
	if err != nil {
		return nil, err
	}
	out := []Circle{}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		data, err := hookReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			continue
		}
		var c Circle
		if json.Unmarshal(data, &c) == nil {
			out = append(out, c)
		}
	}
	return out, nil
}

func createCircle(home string, name string, id DeviceIdentity, priv ed25519.PrivateKey) (Circle, error) {
	key := make([]byte, 32)
	if _, err := hookRandRead(key); err != nil {
		return Circle{}, err
	}
	now := nowRFC3339()
	c := Circle{
		ID:              "circle_" + randomToken(8),
		Name:            strings.TrimSpace(name),
		GroupKey:        base64.StdEncoding.EncodeToString(key),
		CreatorDeviceID: id.DeviceID,
		Members: []CircleMember{{
			DeviceID:  id.DeviceID,
			PublicKey: id.PublicKey,
			AddedAt:   now,
			Revoked:   false,
		}},
		AllowedFolders: []string{},
		PeerEndpoints:  []string{},
		CreatedAt:      now,
		UpdatedAt:      now,
	}
	if err := c.signMemberList(priv); err != nil {
		return Circle{}, err
	}
	if err := saveCircle(home, c); err != nil {
		return Circle{}, err
	}
	return c, nil
}

func (c *Circle) signMemberList(priv ed25519.PrivateKey) error {
	payload, err := hookJSONMarshal(c.Members)
	if err != nil {
		return err
	}
	c.MemberListSignature = signPayload(priv, payload)
	return nil
}

func (c Circle) activeMember(deviceID string) (CircleMember, bool) {
	for _, m := range c.Members {
		if m.DeviceID == deviceID && !m.Revoked {
			return m, true
		}
	}
	return CircleMember{}, false
}

func (c Circle) isActiveMemberPubKey(pubB64 string) bool {
	for _, m := range c.Members {
		if m.PublicKey == pubB64 && !m.Revoked {
			return true
		}
	}
	return false
}

func createInvite(home string, circleID string, id DeviceIdentity) (CircleInvite, error) {
	c, err := loadCircle(home, circleID)
	if err != nil {
		return CircleInvite{}, err
	}
	if _, ok := c.activeMember(id.DeviceID); !ok {
		return CircleInvite{}, errors.New("not a circle member")
	}
	code := randomToken(6)
	fp := hashHex([]byte(c.ID + code))[:12]
	invite := CircleInvite{
		CircleID:    c.ID,
		CircleName:  c.Name,
		Code:        code,
		Fingerprint: fp,
		GroupKey:    c.GroupKey,
		ExpiresAt:   time.Now().UTC().Add(inviteTTL()).Format(time.RFC3339),
		CreatorPK:   id.PublicKey,
		CreatorID:   id.DeviceID,
	}
	if err := hookMkdirAll(invitesDir(home), 0o700); err != nil {
		return CircleInvite{}, err
	}
	data, err := hookJSONMarshalIndent(invite, "", "  ")
	if err != nil {
		return CircleInvite{}, err
	}
	if err := hookWriteFile(filepath.Join(invitesDir(home), code+".json"), data, 0o600); err != nil {
		return CircleInvite{}, err
	}
	return invite, nil
}

func acceptInvite(home string, code string, fingerprint string, endpoint string, id DeviceIdentity, priv ed25519.PrivateKey) (Circle, error) {
	path := filepath.Join(invitesDir(home), code+".json")
	data, err := hookReadFile(path)
	if err != nil {
		return Circle{}, fmt.Errorf("invite not found")
	}
	var invite CircleInvite
	if err := json.Unmarshal(data, &invite); err != nil {
		return Circle{}, err
	}
	if invite.Fingerprint != fingerprint {
		return Circle{}, errors.New("invite fingerprint mismatch")
	}
	exp, err := time.Parse(time.RFC3339, invite.ExpiresAt)
	if err != nil || time.Now().UTC().After(exp) {
		return Circle{}, errors.New("invite expired")
	}

	c, err := loadCircle(home, invite.CircleID)
	if err != nil {
		c = Circle{
			ID:              invite.CircleID,
			Name:            invite.CircleName,
			GroupKey:        invite.GroupKey,
			CreatorDeviceID: invite.CreatorID,
			Members:           []CircleMember{},
			AllowedFolders:    []string{},
			PeerEndpoints:     []string{},
			CreatedAt:         nowRFC3339(),
		}
		// copy creator as member if new local circle file
		c.Members = append(c.Members, CircleMember{
			DeviceID: invite.CreatorID, PublicKey: invite.CreatorPK, AddedAt: nowRFC3339(), Revoked: false,
		})
	}
	if _, ok := c.activeMember(id.DeviceID); !ok {
		c.Members = append(c.Members, CircleMember{
			DeviceID: id.DeviceID, PublicKey: id.PublicKey, AddedAt: nowRFC3339(), Revoked: false,
		})
	}
	if endpoint != "" && !containsString(c.PeerEndpoints, endpoint) {
		c.PeerEndpoints = append(c.PeerEndpoints, endpoint)
	}
	if err := c.signMemberList(priv); err != nil {
		return Circle{}, err
	}
	if err := saveCircle(home, c); err != nil {
		return Circle{}, err
	}
	_ = notifyPeerJoin(invite, endpoint, id, priv)
	_ = os.Remove(path)
	return c, nil
}

func revokeMember(home, circleID, deviceID string, actor DeviceIdentity, priv ed25519.PrivateKey) (Circle, error) {
	c, err := loadCircle(home, circleID)
	if err != nil {
		return Circle{}, err
	}
	if c.CreatorDeviceID != actor.DeviceID {
		return Circle{}, errors.New("only circle creator can revoke members")
	}
	found := false
	for i, m := range c.Members {
		if m.DeviceID == deviceID {
			c.Members[i].Revoked = true
			found = true
		}
	}
	if !found {
		return Circle{}, errors.New("member not found")
	}
	if err := c.signMemberList(priv); err != nil {
		return Circle{}, err
	}
	if err := saveCircle(home, c); err != nil {
		return Circle{}, err
	}
	return c, nil
}

func addAllowedFolder(home, circleID, folder string, actor DeviceIdentity) (Circle, error) {
	c, err := loadCircle(home, circleID)
	if err != nil {
		return Circle{}, err
	}
	if _, ok := c.activeMember(actor.DeviceID); !ok {
		return Circle{}, errors.New("not a circle member")
	}
	abs, err := hookFilepathAbs(folder)
	if err != nil {
		return Circle{}, err
	}
	abs, err = filepath.EvalSymlinks(abs)
	if err != nil {
		abs, _ = hookFilepathAbs(folder)
	}
	if !containsString(c.AllowedFolders, abs) {
		c.AllowedFolders = append(c.AllowedFolders, abs)
	}
	if err := saveCircle(home, c); err != nil {
		return Circle{}, err
	}
	return c, nil
}

func folderAllowed(c Circle, projectRoot string) bool {
	if len(c.AllowedFolders) == 0 {
		return false
	}
	root, err := hookFilepathAbs(projectRoot)
	if err != nil {
		return false
	}
	root, err = filepath.EvalSymlinks(root)
	if err != nil {
		root, _ = hookFilepathAbs(projectRoot)
	}
	root = filepath.Clean(root)
	for _, allowed := range c.AllowedFolders {
		a, err := hookFilepathAbs(allowed)
		if err != nil {
			continue
		}
		a, err = filepath.EvalSymlinks(a)
		if err != nil {
			a = filepath.Clean(allowed)
		}
		a = filepath.Clean(a)
		if root == a || strings.HasPrefix(root, a+string(os.PathSeparator)) {
			return true
		}
	}
	return false
}

func circleForProject(home string, projectRoot string) (Circle, bool) {
	circles, err := listCircles(home)
	if err != nil {
		return Circle{}, false
	}
	for _, c := range circles {
		if folderAllowed(c, projectRoot) {
			return c, true
		}
	}
	return Circle{}, false
}

func containsString(ss []string, v string) bool {
	for _, s := range ss {
		if s == v {
			return true
		}
	}
	return false
}

func randomToken(n int) string {
	const alphabet = "abcdefghijklmnopqrstuvwxyz0123456789"
	b := make([]byte, n)
	if _, err := hookRandRead(b); err != nil {
		return fmt.Sprintf("%d", time.Now().UnixNano())
	}
	for i := range b {
		b[i] = alphabet[int(b[i])%len(alphabet)]
	}
	return string(b)
}

func hashHex(b []byte) string {
	sum := sha256.Sum256(b)
	return fmt.Sprintf("%x", sum)
}
