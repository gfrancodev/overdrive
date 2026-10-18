package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"
)

func TestListenFollowsTheNetworkChoice(t *testing.T) {
	lan, local := networkSetting{LANAccess: true}, networkSetting{LANAccess: false}
	cases := []struct {
		raw     string
		setting networkSetting
		chosen  bool
		want    string
	}{
		{"127.0.0.1:7741", networkSetting{}, false, "127.0.0.1:7741"},
		{"0.0.0.0:7741", networkSetting{}, false, "0.0.0.0:7741"},
		{"127.0.0.1:7741", lan, true, "0.0.0.0:7741"},
		{"localhost:7741", lan, true, "0.0.0.0:7741"},
		{"[::1]:7741", lan, true, "0.0.0.0:7741"},
		{"0.0.0.0:7741", lan, true, "0.0.0.0:7741"},
		{"192.168.1.5:7741", lan, true, "192.168.1.5:7741"},
		{"0.0.0.0:7741", local, true, "127.0.0.1:7741"},
		{":7741", local, true, "127.0.0.1:7741"},
		{"192.168.1.5:7741", local, true, "127.0.0.1:7741"},
		{"127.0.0.1:7741", local, true, "127.0.0.1:7741"},
		{"not-an-address", lan, true, "not-an-address"},
	}
	for _, c := range cases {
		if got := listenForSetting(c.raw, c.setting, c.chosen); got != c.want {
			t.Errorf("listenForSetting(%q, lan=%v, chosen=%v) = %q, want %q", c.raw, c.setting.LANAccess, c.chosen, got, c.want)
		}
	}
}

func networkCall(t *testing.T, home, method, body string) (int, networkView, string) {
	t.Helper()
	mux := http.NewServeMux()
	registerNetworkRoutes(mux, home)
	req := httptest.NewRequest(method, "/api/network", strings.NewReader(body))
	req.RemoteAddr = "127.0.0.1:50000"
	req.Host = "127.0.0.1:7742"
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)
	var view networkView
	_ = json.Unmarshal(rr.Body.Bytes(), &view)
	return rr.Code, view, rr.Body.String()
}

func startTestShareListener(t *testing.T, home string) *shareListener {
	t.Helper()
	t.Setenv("OVERDRIVE_SHARE_LISTEN", fmt.Sprintf("127.0.0.1:%d", freeTCPPort(t)))
	id, priv, err := loadOrCreateIdentity(home)
	if err != nil {
		t.Fatal(err)
	}
	sl, err := startShareListener(home, id, priv)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(sl.Close)
	return sl
}

func listenerPort(t *testing.T, sl *shareListener) int {
	t.Helper()
	return sl.listener().Addr().(*net.TCPAddr).Port
}

func dialsWithin(addr string) bool {
	conn, err := net.DialTimeout("tcp", addr, 300*time.Millisecond)
	if err != nil {
		return false
	}
	_ = conn.Close()
	return true
}

func TestAllowingTheNetworkRebindsTheRunningCore(t *testing.T) {
	home := t.TempDir()
	setupTestEnv(t, home)
	sl := startTestShareListener(t, home)
	port := listenerPort(t, sl)

	code, view, body := networkCall(t, home, http.MethodGet, "")
	if code != http.StatusOK || view.LANAccess || !view.Listening || view.Listen != fmt.Sprintf("127.0.0.1:%d", port) {
		t.Fatalf("before: %d %s", code, body)
	}

	code, view, body = networkCall(t, home, http.MethodPost, `{"lan_access":true}`)
	if code != http.StatusOK || !view.LANAccess || view.Listen != fmt.Sprintf("0.0.0.0:%d", port) {
		t.Fatalf("allow: %d %s", code, body)
	}
	if got := listenerPort(t, sl); got != port {
		t.Fatalf("the port moved from %d to %d", port, got)
	}
	if !dialsWithin(fmt.Sprintf("127.0.0.1:%d", port)) {
		t.Fatal("the Core stopped answering on loopback")
	}
	if ip := firstLANIPv4(); ip != "" {
		if want := net.JoinHostPort(ip, fmt.Sprint(port)); advertisedListenAddr() != want || view.Address != want {
			t.Fatalf("peers should dial %s, advertised %q, view %q", want, advertisedListenAddr(), view.Address)
		}
		if !dialsWithin(net.JoinHostPort(ip, fmt.Sprint(port))) {
			t.Fatal("not reachable from the network")
		}
	}
	if s, ok := loadNetworkSetting(home); !ok || !s.LANAccess {
		t.Fatalf("choice not stored: %+v %v", s, ok)
	}

	code, view, body = networkCall(t, home, http.MethodPost, `{"lan_access":false}`)
	if code != http.StatusOK || view.LANAccess || view.Listen != fmt.Sprintf("127.0.0.1:%d", port) {
		t.Fatalf("back to local: %d %s", code, body)
	}
	if advertisedListenAddr() != fmt.Sprintf("127.0.0.1:%d", port) {
		t.Fatalf("advertised after closing: %q", advertisedListenAddr())
	}
	if !dialsWithin(fmt.Sprintf("127.0.0.1:%d", port)) {
		t.Fatal("loopback lost after closing the network")
	}
	if ip := firstLANIPv4(); ip != "" && dialsWithin(net.JoinHostPort(ip, fmt.Sprint(port))) {
		t.Fatal("still reachable from the network")
	}
}

func TestTheNetworkChoiceSurvivesACoreRestart(t *testing.T) {
	home := t.TempDir()
	setupTestEnv(t, home)
	if err := saveNetworkSetting(home, true); err != nil {
		t.Fatal(err)
	}
	sl := startTestShareListener(t, home)
	if host, _, _ := net.SplitHostPort(sl.bindAddr()); host != "0.0.0.0" {
		t.Fatalf("started on %s", sl.bindAddr())
	}
	if !announcesNetwork() {
		t.Fatal("discovery should tell peers this device accepts network connections")
	}
}

func TestNetworkChoiceWithoutARunningCoreAppliesAtNextStart(t *testing.T) {
	home := t.TempDir()
	setupTestEnv(t, home)
	t.Setenv("OVERDRIVE_SHARE_LISTEN", "127.0.0.1:7741")
	code, view, body := networkCall(t, home, http.MethodPost, `{"lan_access":true}`)
	if code != http.StatusOK || !view.LANAccess || view.Listening || view.Listen != "0.0.0.0:7741" {
		t.Fatalf("%d %s", code, body)
	}
}

func TestAFailedRebindKeepsTheCoreOnItsAddress(t *testing.T) {
	home := t.TempDir()
	setupTestEnv(t, home)
	sl := startTestShareListener(t, home)
	port := listenerPort(t, sl)
	real := hookNetListen
	hookNetListen = func(network, addr string) (net.Listener, error) {
		if strings.HasPrefix(addr, "0.0.0.0:") {
			return nil, fmt.Errorf("bind: permission denied")
		}
		return real(network, addr)
	}
	code, _, body := networkCall(t, home, http.MethodPost, `{"lan_access":true}`)
	if code != http.StatusInternalServerError || !strings.Contains(body, "could not accept network connections") {
		t.Fatalf("%d %s", code, body)
	}
	if !dialsWithin(fmt.Sprintf("127.0.0.1:%d", port)) {
		t.Fatal("the Core lost its address after a failed rebind")
	}
	if _, ok := loadNetworkSetting(home); ok {
		t.Fatal("a failed rebind must leave no choice behind when there was none")
	}

	if err := saveNetworkSetting(home, false); err != nil {
		t.Fatal(err)
	}
	if code, _, body := networkCall(t, home, http.MethodPost, `{"lan_access":true}`); code != http.StatusInternalServerError {
		t.Fatalf("%d %s", code, body)
	}
	if s, ok := loadNetworkSetting(home); !ok || s.LANAccess {
		t.Fatalf("a failed rebind must keep the earlier choice: %+v %v", s, ok)
	}
}

func TestNetworkAPIIsLocalOnly(t *testing.T) {
	home := t.TempDir()
	setupTestEnv(t, home)
	mux := http.NewServeMux()
	registerNetworkRoutes(mux, home)
	req := httptest.NewRequest(http.MethodPost, "/api/network", bytes.NewBufferString(`{"lan_access":true}`))
	req.RemoteAddr = "192.168.1.20:50000"
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)
	if rr.Code != http.StatusForbidden {
		t.Fatalf("remote: %d", rr.Code)
	}
	if code, _, _ := networkCall(t, home, http.MethodPost, `{`); code != http.StatusBadRequest {
		t.Fatalf("bad body: %d", code)
	}
	if code, _, _ := networkCall(t, home, http.MethodPost, `{}`); code != http.StatusBadRequest {
		t.Fatalf("a body that does not say: %d", code)
	}
	if code, _, _ := networkCall(t, home, http.MethodDelete, ``); code != http.StatusMethodNotAllowed {
		t.Fatalf("delete: %d", code)
	}
	if _, ok := loadNetworkSetting(home); ok {
		t.Fatal("rejected calls stored a choice")
	}
}

func TestAnUnreadableNetworkFileIsNoChoice(t *testing.T) {
	home := t.TempDir()
	setupTestEnv(t, home)
	if err := os.MkdirAll(shareDir(home), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(networkSettingPath(home), []byte("{lan"), 0o600); err != nil {
		t.Fatal(err)
	}
	if s, ok := loadNetworkSetting(home); ok || s.LANAccess {
		t.Fatalf("a corrupt file must read as no choice: %+v %v", s, ok)
	}
}

func TestDiscoveryTellsWhetherADeviceAcceptsNetworkConnections(t *testing.T) {
	txt := announcementTXT(discoveryAnnouncement{DeviceID: "dev_a", FriendlyName: "studio", Net: "local"})
	peer, ok := peerFromTXT(txt, "192.168.1.9", 7741)
	if !ok || !peer.LocalOnly {
		t.Fatalf("local-only flag lost: %+v %v", peer, ok)
	}
	txt = announcementTXT(discoveryAnnouncement{DeviceID: "dev_a", FriendlyName: "studio", Net: "lan"})
	if peer, _ = peerFromTXT(txt, "192.168.1.9", 7741); peer.LocalOnly {
		t.Fatal("lan device marked local-only")
	}
	// Cores before 2.3 do not say; they are not assumed to be local-only.
	if peer, _ = peerFromTXT([]string{"id=dev_old", "fn=old"}, "192.168.1.9", 7741); peer.LocalOnly {
		t.Fatal("older core marked local-only")
	}
}
