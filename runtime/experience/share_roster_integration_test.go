package main

import (
	"crypto/ed25519"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

type rosterCore struct {
	home     string
	id       DeviceIdentity
	priv     ed25519.PrivateKey
	endpoint string
}

func startRosterCore(t *testing.T, name string) rosterCore {
	t.Helper()
	home := filepath.Join(t.TempDir(), name)
	id, priv, err := loadOrCreateIdentity(home)
	if err != nil {
		t.Fatal(err)
	}
	endpoint := fmt.Sprintf("127.0.0.1:%d", freeTCPPort(t))
	t.Setenv("OVERDRIVE_SHARE_LISTEN", endpoint)
	sl, err := startShareListener(home, id, priv)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(sl.Close)
	// Runs before the home is removed: a roster fetch started by a key adoption may still be writing to it.
	t.Cleanup(rosterPulls.Wait)
	return rosterCore{home: home, id: id, priv: priv, endpoint: endpoint}
}

func (c rosterCore) circle(t *testing.T, circleID string) Circle {
	t.Helper()
	circle, err := loadCircle(c.home, circleID)
	if err != nil {
		t.Fatal(err)
	}
	return circle
}

// join makes c accept an invite that inviter created, over inviter's listener.
func (c rosterCore) join(t *testing.T, inviter rosterCore, circleID string) {
	t.Helper()
	invite, err := createInvite(inviter.home, circleID, inviter.id)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(invite)
	if err := os.MkdirAll(invitesDir(c.home), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(invitesDir(c.home), invite.Code+".json"), raw, 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("OVERDRIVE_SHARE_LISTEN", c.endpoint)
	if _, err := acceptInvite(c.home, invite.Code, invite.Fingerprint, inviter.endpoint, c.id, c.priv); err != nil {
		t.Fatal(err)
	}
}

// pullRoster runs the roster exchange from c to peer.
func (c rosterCore) pullRoster(t *testing.T, peer rosterCore, circleID string) error {
	t.Helper()
	resp, err := fetchPeerRoster(peer.endpoint, c.circle(t, circleID), c.id, c.priv)
	if err != nil {
		return err
	}
	return applyPeerRoster(c.home, circleID, resp)
}

func (c rosterCore) sees(t *testing.T, circleID string, other rosterCore) (known, active bool) {
	t.Helper()
	circle := c.circle(t, circleID)
	_, known = circle.memberRecord(other.id.DeviceID)
	_, active = circle.activeMember(other.id.DeviceID)
	return known, active
}

func TestSignedRosterAcrossThreeCores(t *testing.T) {
	setupTestEnv(t, t.TempDir())
	creator := startRosterCore(t, "creator")
	relay := startRosterCore(t, "relay")
	late := startRosterCore(t, "late")
	circle, err := createCircle(creator.home, "team", creator.id, creator.priv)
	if err != nil {
		t.Fatal(err)
	}

	relay.join(t, creator, circle.ID)
	late.join(t, relay, circle.ID)

	if got := late.circle(t, circle.ID).CreatorDeviceID; got != creator.id.DeviceID {
		t.Fatalf("a device invited by a non-creator must record the real creator, got %s", got)
	}
	if _, active := late.sees(t, circle.ID, creator); !active {
		t.Fatal("the join ack must carry the creator, vouched by its own admission")
	}
	if known, _ := creator.sees(t, circle.ID, late); known {
		t.Fatal("precondition: the creator has not heard of the late joiner yet")
	}

	if err := creator.pullRoster(t, relay, circle.ID); err != nil {
		t.Fatal(err)
	}
	if _, active := creator.sees(t, circle.ID, late); !active {
		t.Fatal("the creator must learn a member another member admitted")
	}
	if adm := creator.circle(t, circle.ID).Admissions[late.id.DeviceID]; adm.InvitedBy != relay.id.DeviceID {
		t.Fatalf("the creator must see who invited the late joiner, got %q", adm.InvitedBy)
	}

	if _, err := revokeMember(creator.home, circle.ID, late.id.DeviceID, creator.id, creator.priv); err != nil {
		t.Fatal(err)
	}
	if err := relay.pullRoster(t, creator, circle.ID); err != nil {
		t.Fatal(err)
	}
	if _, active := relay.sees(t, circle.ID, late); active {
		t.Fatal("a revocation must reach a member through the roster exchange")
	}
	if err := late.pullRoster(t, creator, circle.ID); err == nil {
		t.Fatal("a revoked device must not get the roster any more")
	}
}

// pullKeys runs the keys exchange from c to peer.
func (c rosterCore) pullKeys(t *testing.T, peer rosterCore, circleID string) error {
	t.Helper()
	resp, err := fetchPeerKeys(peer.endpoint, c.circle(t, circleID), c.id, c.priv)
	if err != nil {
		return err
	}
	return applyPeerKeys(c.home, circleID, resp)
}
