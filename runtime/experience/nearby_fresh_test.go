package main

import (
	"testing"
	"time"
)

func TestADeviceThatStopsAnnouncingLeavesTheNearbyList(t *testing.T) {
	home := meshHome(t)
	clock := withMeshClock(t, time.Date(2026, 10, 18, 12, 0, 0, 0, time.UTC))
	if err := noteDiscoveredPeer(home, discoveredPeer{DeviceID: "dev_0000000000000001", DisplayName: "Ada"}); err != nil {
		t.Fatal(err)
	}
	*clock = clock.Add(nearbyFreshFor)
	if found, _ := listDiscoveredPeers(home); len(found) != 1 {
		t.Fatalf("a device heard %s ago is missing: %+v", nearbyFreshFor, found)
	}
	*clock = clock.Add(time.Second)
	if found, _ := listDiscoveredPeers(home); len(found) != 0 {
		t.Fatalf("a device that stopped announcing is still nearby: %+v", found)
	}
}

func TestRecordingAnAnnouncementDropsTheOnesThatWentAway(t *testing.T) {
	home := meshHome(t)
	clock := withMeshClock(t, time.Date(2026, 10, 18, 12, 0, 0, 0, time.UTC))
	for _, id := range []string{"dev_0000000000000001", "dev_0000000000000002", "dev_0000000000000003"} {
		if err := noteDiscoveredPeer(home, discoveredPeer{DeviceID: id, DisplayName: "Test"}); err != nil {
			t.Fatal(err)
		}
	}
	*clock = clock.Add(time.Hour)
	// An announcement already stamped long ago is still the one just heard, so it stays.
	if err := noteDiscoveredPeer(home, discoveredPeer{DeviceID: "dev_0000000000000004", LastSeen: "2026-10-18T12:00:00Z"}); err != nil {
		t.Fatal(err)
	}
	file, err := loadDiscovered(home)
	if err != nil {
		t.Fatal(err)
	}
	if len(file.Peers) != 1 || file.Peers[0].DeviceID != "dev_0000000000000004" {
		t.Fatalf("the file kept devices that went away: %+v", file.Peers)
	}
}

func TestAnnouncementWithoutAReadableTimeIsNotNearby(t *testing.T) {
	if discoveryFresh(discoveredPeer{DeviceID: "dev_0000000000000001", LastSeen: "yesterday"}, time.Now()) {
		t.Fatal("an unreadable time counted as fresh")
	}
}

func TestDiscoveryOffKeepsTheCoreOffTheLocalNetwork(t *testing.T) {
	home := meshHome(t)
	meshRT.mu.Lock()
	meshRT.initLocked()
	meshRT.armed = true
	meshRT.home = home
	meshRT.epoch = 1
	meshRT.mu.Unlock()
	for _, value := range []string{"off", " OFF "} {
		t.Setenv("OVERDRIVE_DISCOVERY", value)
		if err := startDiscovery(home, 1); err != nil {
			t.Fatal(err)
		}
		if discoveryAdvertising() {
			t.Fatalf("OVERDRIVE_DISCOVERY=%q still announced on the network", value)
		}
	}
	t.Setenv("OVERDRIVE_DISCOVERY", "on")
	if discoveryOff() {
		t.Fatal("only off turns discovery off")
	}
}

func TestDiscoveryWaitsForAnOpenMeshAndTheCurrentEpoch(t *testing.T) {
	home := meshHome(t)
	t.Setenv("OVERDRIVE_DISCOVERY", "")
	meshRT.mu.Lock()
	meshRT.initLocked()
	meshRT.armed = true
	meshRT.home = home
	meshRT.epoch = 2
	meshRT.mu.Unlock()
	if err := startDiscovery(home, 1); err != nil {
		t.Fatal(err)
	}
	if discoveryAdvertising() {
		t.Fatal("a start from an earlier epoch announced on the network")
	}
	meshMu.Lock()
	meshPausedFlag = true
	meshMu.Unlock()
	t.Cleanup(resetCoreLifecycle)
	if err := startDiscovery(home, 2); err != nil {
		t.Fatal(err)
	}
	if discoveryAdvertising() {
		t.Fatal("a paused mesh announced on the network")
	}
}
