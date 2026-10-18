package main

import (
	"net/http"
	"path/filepath"
	"testing"
)

func withEndpoint(t *testing.T, c Circle, dev proofDevice, endpoint string) Circle {
	t.Helper()
	for i := range c.Members {
		if c.Members[i].DeviceID == dev.id.DeviceID {
			c.Members[i].Endpoint = endpoint
			return c
		}
	}
	t.Fatalf("%s is not a member", dev.id.DeviceID)
	return c
}

// A revocation marked the member but kept its address among the endpoints, so sync, catalog and roster exchanges
// went on dialing the removed device, and its last sync line (a timeout) kept the tray on "error" for good.
func TestARemovedDeviceIsNoLongerDialed(t *testing.T) {
	f := newRevokeFixture(t)
	circle := withEndpoint(t, f.circle, f.b, "192.0.2.11:7741")
	circle = withEndpoint(t, circle, f.d, "192.0.2.13:7741")
	// A peer added by address alone, with no member behind it, is kept.
	circle.PeerEndpoints = []string{"192.0.2.11:7741", "192.0.2.13:7741", "192.0.2.99:7741"}
	if err := saveCircle(f.home, circle); err != nil {
		t.Fatal(err)
	}
	revokeCall(t, f.home, http.MethodPost, f.memberPath(f.b), map[string]bool{}, http.StatusOK)
	loaded, err := loadCircle(f.home, circle.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !sameStrings(loaded.PeerEndpoints, []string{"192.0.2.13:7741", "192.0.2.99:7741"}) {
		t.Fatalf("endpoints after the removal: %v", loaded.PeerEndpoints)
	}
	refreshed := refreshPeerEndpoints(loaded, f.circle.CreatorDeviceID)
	if !sameStrings(refreshed.PeerEndpoints, []string{"192.0.2.13:7741", "192.0.2.99:7741"}) {
		t.Fatalf("a refresh brought the removed device back: %v", refreshed.PeerEndpoints)
	}
}

func TestAnAddressAnActiveMemberStillUsesStays(t *testing.T) {
	c := Circle{
		Members: []CircleMember{
			{DeviceID: "dev_old", Endpoint: "192.0.2.20:7741", Revoked: true},
			{DeviceID: "dev_new", Endpoint: "192.0.2.20:7741"},
		},
		PeerEndpoints: []string{"192.0.2.20:7741"},
	}
	if got := c.withoutRevokedEndpoints().PeerEndpoints; !sameStrings(got, []string{"192.0.2.20:7741"}) {
		t.Fatalf("the computer paired again lost its address: %v", got)
	}
}

func TestSyncLinesOnlyForDevicesTheCircleStillSyncsWith(t *testing.T) {
	lines := []string{
		"- 192.0.2.11:7741: " + peerLineTimeout,
		"- 192.0.2.13:7741: " + peerLineOKPrefix + "3" + peerLineOKSuffix,
		"a line that is not a sync result",
	}
	got := currentPeerLines(lines, []string{" 192.0.2.13:7741 "})
	if !sameStrings(got, lines[1:]) {
		t.Fatalf("lines kept: %v", got)
	}
	if links := dashboardLinks(got); len(links) != 1 || links[0].Name != "192.0.2.13:7741" || links[0].State != "ok" {
		t.Fatalf("links: %+v", links)
	}
}

// The dashboard and the sync loops read circles through listCircles, which decoded the files without the cleanup
// loadCircle does. A circle revoked before that cleanup existed keeps the removed device's address on disk; on a
// real Core it kept its sync line, and the tray stayed on "error".
func TestTheDashboardDropsARemovedDeviceSyncLine(t *testing.T) {
	f := newRevokeFixture(t)
	repo := makeTestRepo(t, filepath.Join(t.TempDir(), "proj"), "https://github.com/acme/removed-device.git")
	circle := withEndpoint(t, f.circle, f.b, "192.0.2.11:7741")
	circle = withEndpoint(t, circle, f.d, "192.0.2.13:7741")
	for i := range circle.Members {
		if circle.Members[i].DeviceID == f.b.id.DeviceID {
			circle.Members[i].Revoked = true
		}
	}
	circle.PeerEndpoints = []string{"192.0.2.11:7741", "192.0.2.13:7741"}
	circle.AllowedFolders = []string{repo}
	if err := saveCircle(f.home, circle); err != nil {
		t.Fatal(err)
	}
	project, err := identifyProject(repo)
	if err != nil {
		t.Fatal(err)
	}
	e, err := newEngine(f.home)
	if err != nil {
		t.Fatal(err)
	}
	e.savePeerLines(circle.ID, project.Repository, []string{
		formatPeerSyncLine("192.0.2.11:7741", "timeout", 0),
		formatPeerSyncLine("192.0.2.13:7741", "ok", 2),
	})
	e.Close()

	listed, err := listCircles(f.home)
	if err != nil || len(listed) != 1 {
		t.Fatalf("circles %d, %v", len(listed), err)
	}
	if !sameStrings(listed[0].PeerEndpoints, []string{"192.0.2.13:7741"}) {
		t.Fatalf("listed circles still dial the removed device: %v", listed[0].PeerEndpoints)
	}
	links := peerDashboardSnapshot(f.home).Links
	if len(links) != 1 || links[0].Name != "192.0.2.13:7741" || links[0].State != "ok" {
		t.Fatalf("dashboard links: %+v", links)
	}
}
