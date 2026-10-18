package main

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	pahomqtt "github.com/eclipse/paho.golang/paho"
	"github.com/hashicorp/mdns"
)

func meshHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	setupTestEnv(t, home)
	t.Cleanup(func() {
		stopMeshServices()
		setMeshDeliveryHook(nil)
	})
	return home
}

func testDevice(t *testing.T) (DeviceIdentity, ed25519.PrivateKey) {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return DeviceIdentity{
		DeviceID:   "dev_" + sha256Hex(pub)[:16],
		PublicKey:  base64.StdEncoding.EncodeToString(pub),
		PrivateKey: base64.StdEncoding.EncodeToString(priv),
		CreatedAt:  nowRFC3339(),
	}, priv
}

func testGroupKey() string {
	return base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{9}, 32))
}

func writeIdentityFile(t *testing.T, home string, id DeviceIdentity) {
	t.Helper()
	if err := os.MkdirAll(shareDir(home), 0o700); err != nil {
		t.Fatal(err)
	}
	data, err := json.MarshalIndent(id, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(identityPath(home), append(data, '\n'), 0o600); err != nil {
		t.Fatal(err)
	}
}

func testCircle(creator DeviceIdentity, others ...DeviceIdentity) Circle {
	now := nowRFC3339()
	circle := Circle{
		ID:              "circle_mesh01",
		Name:            "mesh",
		GroupKey:        testGroupKey(),
		CreatorDeviceID: creator.DeviceID,
		CreatedAt:       now,
		UpdatedAt:       now,
	}
	add := func(id DeviceIdentity) {
		circle.Members = append(circle.Members, CircleMember{
			DeviceID: id.DeviceID, PublicKey: id.PublicKey, AddedAt: now,
		})
	}
	add(creator)
	for _, other := range others {
		add(other)
	}
	return circle
}

func saveTestCircle(t *testing.T, home string, circle Circle) Circle {
	t.Helper()
	if err := saveCircle(home, circle); err != nil {
		t.Fatal(err)
	}
	return circle
}

func assertNoKeyMaterial(t *testing.T, body string, secrets ...string) {
	t.Helper()
	for _, secret := range secrets {
		if secret != "" && strings.Contains(body, secret) {
			t.Fatal("response leaked key material")
		}
	}
}

func meshHTTP(t *testing.T, home, method, target, remote string, body []byte) *httptest.ResponseRecorder {
	t.Helper()
	var reader io.Reader
	if body != nil {
		reader = bytes.NewReader(body)
	}
	req := httptest.NewRequest(method, target, reader)
	req.RemoteAddr = remote
	req.Host = "127.0.0.1:7742"
	rec := httptest.NewRecorder()
	shareDashboardHandler(home).ServeHTTP(rec, req)
	return rec
}

func waitUntil(t *testing.T, limit time.Duration, ok func() bool) {
	t.Helper()
	deadline := time.Now().Add(limit)
	for time.Now().Before(deadline) {
		if ok() {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("timed out")
}

func armDiscovery(t *testing.T, home string) {
	t.Helper()
	t.Setenv("OVERDRIVE_DISCOVERY", "")
	meshRT.mu.Lock()
	meshRT.initLocked()
	meshRT.armed = true
	meshRT.home = home
	meshRT.epoch = 1
	meshRT.mu.Unlock()
	if err := startDiscovery(home, 1); err != nil {
		t.Fatal(err)
	}
	if !discoveryAdvertising() {
		t.Fatal("discovery is not advertising")
	}
	// A device on the real network can be heard and recorded in this home right up to the end of the test.
	t.Cleanup(func() {
		stopMeshServices()
		discoveryBrowsers.Wait()
	})
}

func TestMeshElectBrokerCreatorThenNextDevice(t *testing.T) {
	circle := Circle{
		CreatorDeviceID: "dev_m",
		Members: []CircleMember{
			{DeviceID: "dev_z"},
			{DeviceID: "dev_m"},
			{DeviceID: "dev_a"},
			{DeviceID: "dev_gone", Revoked: true},
		},
	}
	host, ok := electBroker(circle, nil)
	if !ok || host != "dev_m" {
		t.Fatalf("creator host %q ok %v", host, ok)
	}
	host, ok = electBroker(circle, map[string]bool{"dev_m": true})
	if !ok || host != "dev_z" {
		t.Fatalf("next host %q ok %v", host, ok)
	}
	host, ok = electBroker(circle, map[string]bool{"dev_m": true, "dev_z": true})
	if !ok || host != "dev_a" {
		t.Fatalf("wrapped host %q ok %v", host, ok)
	}
	if _, ok := electBroker(circle, map[string]bool{"dev_m": true, "dev_z": true, "dev_a": true}); ok {
		t.Fatal("elected a broker when every member is gone")
	}

	revokedCreator := circle
	revokedCreator.CreatorDeviceID = "dev_gone"
	host, ok = electBroker(revokedCreator, nil)
	if !ok || host != "dev_a" {
		t.Fatalf("revoked creator fallback %q ok %v", host, ok)
	}
}

func TestMeshDiscoveredPeerIsNotAMember(t *testing.T) {
	home := meshHome(t)
	self, _ := testDevice(t)
	writeIdentityFile(t, home, self)
	circle := saveTestCircle(t, home, testCircle(self))
	before, err := os.ReadFile(circlePath(home, circle.ID))
	if err != nil {
		t.Fatal(err)
	}
	peer := discoveredPeer{
		DeviceID: "dev_0011223344556677", FriendlyName: "laptop", OS: "linux", DisplayName: "Ada",
		Host: "192.0.2.10", Port: 7741,
	}
	if err := noteDiscoveredPeer(home, peer); err != nil {
		t.Fatal(err)
	}
	after, err := os.ReadFile(circlePath(home, circle.ID))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Fatal("listing a discovered peer rewrote the circle")
	}
	found, err := listDiscoveredPeers(home)
	if err != nil {
		t.Fatal(err)
	}
	if len(found) != 1 || found[0].DeviceID != peer.DeviceID || found[0].DisplayName != "Ada" || found[0].OS != "linux" || found[0].FriendlyName != "laptop" {
		t.Fatalf("discovered %+v", found)
	}
	loaded, err := loadCircle(home, circle.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := loaded.activeMember(peer.DeviceID); ok {
		t.Fatal("discovery added a circle member")
	}
	if _, err := os.Stat(filepath.Join(invitesDir(home), "dev_0011223344556677.json")); !os.IsNotExist(err) {
		t.Fatal("discovery created a join invite")
	}
}

func TestMeshIgnoreAnnouncementCreatesNoMember(t *testing.T) {
	home := meshHome(t)
	self, _ := testDevice(t)
	writeIdentityFile(t, home, self)
	circle := saveTestCircle(t, home, testCircle(self))
	const ignored = "dev_0011223344556678"
	if err := noteDiscoveredPeer(home, discoveredPeer{DeviceID: ignored, FriendlyName: "other", OS: "linux", DisplayName: "Bea"}); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(circlePath(home, circle.ID))
	if err != nil {
		t.Fatal(err)
	}
	if err := ignoreDiscoveredPeer(home, ignored); err != nil {
		t.Fatal(err)
	}
	if err := noteDiscoveredPeer(home, discoveredPeer{DeviceID: ignored, FriendlyName: "other", OS: "linux", DisplayName: "Bea"}); err != nil {
		t.Fatal(err)
	}
	after, err := os.ReadFile(circlePath(home, circle.ID))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Fatal("ignoring an announcement changed the circle")
	}
	loaded, err := loadCircle(home, circle.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := loaded.activeMember(ignored); ok {
		t.Fatal("ignore created a member")
	}
	found, err := listDiscoveredPeers(home)
	if err != nil {
		t.Fatal(err)
	}
	for _, peer := range found {
		if peer.DeviceID == ignored {
			t.Fatal("ignored announcement stayed listed")
		}
	}
}

func TestMeshListDiscoveredPeersDedupesByDeviceID(t *testing.T) {
	home := meshHome(t)
	withMeshClock(t, time.Date(2026, 1, 1, 0, 1, 0, 0, time.UTC))
	const id = "dev_0011223344556679"
	discoveryMu.Lock()
	file := discoveredFile{
		Peers: []discoveredPeer{
			{DeviceID: id, DisplayName: "Ada", LastSeen: "2026-01-01T00:00:00Z"},
			{DeviceID: id, DisplayName: "Ada", LastSeen: "2026-01-01T00:00:30Z"},
		},
	}
	if err := saveDiscovered(home, file); err != nil {
		discoveryMu.Unlock()
		t.Fatal(err)
	}
	discoveryMu.Unlock()
	found, err := listDiscoveredPeers(home)
	if err != nil {
		t.Fatal(err)
	}
	if len(found) != 1 || found[0].LastSeen != "2026-01-01T00:00:30Z" {
		t.Fatalf("deduped %+v", found)
	}
}

func TestMeshDiscoveryPeersLoopback(t *testing.T) {
	home := meshHome(t)
	self, _ := testDevice(t)
	writeIdentityFile(t, home, self)
	if err := noteDiscoveredPeer(home, discoveredPeer{DeviceID: "dev_nearby01", DisplayName: "Ada"}); err != nil {
		t.Fatal(err)
	}
	if err := noteDiscoveredPeer(home, discoveredPeer{DeviceID: self.DeviceID, DisplayName: "self"}); err != nil {
		t.Fatal(err)
	}
	rec := meshHTTP(t, home, http.MethodGet, "/api/discovery/peers", "127.0.0.1:9", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("peers %d %s", rec.Code, rec.Body.String())
	}
	var body struct {
		Peers []discoveredPeer `json:"peers"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if len(body.Peers) != 1 || body.Peers[0].DeviceID != "dev_nearby01" {
		t.Fatalf("peers %+v", body.Peers)
	}
	rec = meshHTTP(t, home, http.MethodPost, "/api/discovery/peers", "127.0.0.1:9", nil)
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("peers POST %d", rec.Code)
	}
}

func TestMeshDiscoveryForgetAPI(t *testing.T) {
	home := meshHome(t)
	self, _ := testDevice(t)
	writeIdentityFile(t, home, self)
	saveTestCircle(t, home, testCircle(self))
	const forgotten = "dev_0011223344556680"
	if err := noteDiscoveredPeer(home, discoveredPeer{DeviceID: forgotten, FriendlyName: "laptop", OS: "linux", DisplayName: "Bea"}); err != nil {
		t.Fatal(err)
	}
	remote := meshHTTP(t, home, http.MethodPost, "/api/discovery/forget", "10.9.9.9:9", []byte(`{"device_id":"`+forgotten+`"}`))
	if remote.Code != http.StatusForbidden {
		t.Fatalf("remote forget %d", remote.Code)
	}
	ok := meshHTTP(t, home, http.MethodPost, "/api/discovery/forget", "127.0.0.1:9", []byte(`{"device_id":"`+forgotten+`"}`))
	if ok.Code != http.StatusOK {
		t.Fatalf("forget %d %s", ok.Code, ok.Body.String())
	}
	found, err := listDiscoveredPeers(home)
	if err != nil {
		t.Fatal(err)
	}
	for _, peer := range found {
		if peer.DeviceID == forgotten {
			t.Fatal("forgotten peer still listed")
		}
	}
	if err := noteDiscoveredPeer(home, discoveredPeer{DeviceID: forgotten, FriendlyName: "laptop", OS: "linux", DisplayName: "Bea"}); err != nil {
		t.Fatal(err)
	}
	found, err = listDiscoveredPeers(home)
	if err != nil {
		t.Fatal(err)
	}
	for _, peer := range found {
		if peer.DeviceID == forgotten {
			t.Fatal("ignored peer re-listed after mDNS note")
		}
	}
}

func TestMeshMDNSAnnouncementDoesNotJoin(t *testing.T) {
	home := meshHome(t)
	id, _, err := loadOrCreateIdentity(home)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := setLocalUserDisplayName(home, "Ada Lovelace"); err != nil {
		t.Fatal(err)
	}
	ann, err := localDiscoveryAnnouncement(home)
	if err != nil {
		t.Fatal(err)
	}
	if ann.DeviceID != id.DeviceID || ann.DisplayName != "Ada Lovelace" || ann.OS != runtime.GOOS || ann.FriendlyName != computerName() {
		t.Fatalf("announcement %+v", ann)
	}
	svc, err := newOverdriveMDNS(ann)
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(svc.TXT, "\n")
	for _, want := range []string{"fn=" + ann.FriendlyName, "os=" + runtime.GOOS, "dn=Ada Lovelace", "id=" + id.DeviceID} {
		if !strings.Contains(joined, want) {
			t.Fatalf("txt missing %s", want)
		}
	}
	armDiscovery(t, home)
	entries := make(chan *mdns.ServiceEntry, 8)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	go func() {
		_ = mdns.QueryContext(ctx, &mdns.QueryParam{
			Service: overdriveDNSSDService, Domain: "local", Timeout: 1500 * time.Millisecond,
			Entries: entries, DisableIPv6: true, WantUnicastResponse: true, Logger: meshLog,
		})
	}()
	found := false
	timer := time.NewTimer(2 * time.Second)
	defer timer.Stop()
	for !found {
		select {
		case entry := <-entries:
			peer, ok := discoveredPeerFromEntry(entry)
			if ok && peer.DeviceID == id.DeviceID && peer.DisplayName == "Ada Lovelace" && peer.OS == runtime.GOOS {
				found = true
			}
		case <-timer.C:
			t.Fatal("mDNS query did not see this device")
		}
	}
	if entries, err := os.ReadDir(circlesDir(home)); err == nil && len(entries) > 0 {
		t.Fatal("mDNS announcement created a circle")
	}
}

func TestMeshPauseStopsDiscovery(t *testing.T) {
	home := meshHome(t)
	if _, _, err := loadOrCreateIdentity(home); err != nil {
		t.Fatal(err)
	}
	if _, err := loadOrCreateLocalUser(home); err != nil {
		t.Fatal(err)
	}
	armDiscovery(t, home)
	const seen = "dev_aabbccddeeff0011"
	if err := noteDiscoveredPeer(home, discoveredPeer{DeviceID: seen, FriendlyName: "laptop", OS: "linux", DisplayName: "Ada"}); err != nil {
		t.Fatal(err)
	}
	if err := pauseMesh(home); err != nil {
		t.Fatal(err)
	}
	if discoveryAdvertising() || meshAllowsDiscovery() || meshAllowsChat() || meshAllowsSync() {
		t.Fatal("pause left mesh transports open")
	}
	found, err := listDiscoveredPeers(home)
	if err != nil {
		t.Fatal(err)
	}
	var kept bool
	for _, peer := range found {
		if peer.DeviceID == seen {
			kept = true
		}
	}
	if !kept {
		t.Fatal("pause dropped the discovery cache")
	}
	if err := resumeMesh(home); err != nil {
		t.Fatal(err)
	}
	waitUntil(t, 3*time.Second, discoveryAdvertising)
	if !meshAllowsDiscovery() {
		t.Fatal("resume left discovery off")
	}
}

func TestMeshChatEnvelopeRoundTripAndSeparateDB(t *testing.T) {
	home := meshHome(t)
	self, priv := testDevice(t)
	writeIdentityFile(t, home, self)
	if _, err := setLocalUserDisplayName(home, "Ada Lovelace"); err != nil {
		t.Fatal(err)
	}
	circle := saveTestCircle(t, home, testCircle(self))
	expires := time.Now().UTC().Add(time.Hour).Format(time.RFC3339)
	env, queued, err := postLocalChat(home, chatPostRequest{
		MeshID: circle.ID, Recipient: "dev_0011223344556677", Type: "text", Text: "hello circle",
		HarnessID: "cursor", CorrelationID: "corr-1", CausationID: "cause-1",
		References: []string{"msg_ref"}, ExpiresAt: expires,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !queued {
		t.Fatal("message published without a broker")
	}
	if env.Version != 1 || env.SenderDeviceID != self.DeviceID || env.SenderUserID == "" || env.Signature == "" {
		t.Fatalf("envelope %+v", env)
	}
	if strings.HasPrefix(env.SenderUserID, "dev_") {
		t.Fatalf("sender user id %s", env.SenderUserID)
	}
	pub, err := decodePublicKey(self.PublicKey)
	if err != nil {
		t.Fatal(err)
	}
	if !verifyChatEnvelope(pub, env) {
		t.Fatal("stored envelope does not verify")
	}
	_ = priv
	messages, err := listChatMessages(home, circle.ID, env.ConversationID, false)
	if err != nil || len(messages) != 1 || string(messages[0].Payload) == "" {
		t.Fatalf("history %+v %v", messages, err)
	}
	if !verifyChatEnvelope(pub, messages[0]) {
		t.Fatal("reloaded history does not verify")
	}
	if messages[0].CorrelationID != "corr-1" || messages[0].CausationID != "cause-1" || messages[0].SenderHarnessID != "cursor" || messages[0].ExpiresAt != expires {
		t.Fatalf("fields %+v", messages[0])
	}
	if len(messages[0].References) != 1 || messages[0].References[0] != "msg_ref" {
		t.Fatalf("references %+v", messages[0].References)
	}
	var payload chatPayload
	if err := json.Unmarshal(messages[0].Payload, &payload); err != nil || payload.Text != "hello circle" {
		t.Fatalf("payload %s", messages[0].Payload)
	}
	if _, err := os.Stat(chatDBPath(home)); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(dbPath(home)); !os.IsNotExist(err) {
		t.Fatal("chat store wrote experience-v2.db")
	}
	raw, err := os.ReadFile(chatDBPath(home))
	if err != nil {
		t.Fatal(err)
	}
	assertNoKeyMaterial(t, string(raw), circle.GroupKey, self.PrivateKey)
	items, err := loadOutbox(home, circle.ID)
	if err != nil || len(items) != 1 || items[0].QoS != 1 || items[0].Topic != directChatTopic(circle.ID, env.ConversationID) {
		t.Fatalf("outbox %+v %v", items, err)
	}
	if bytes.Contains(items[0].Wire, []byte("hello circle")) || strings.Contains(string(items[0].Wire), circle.GroupKey) {
		t.Fatal("queued wire exposed chat text or the group key")
	}

	_, _, err = postLocalChat(home, chatPostRequest{MeshID: circle.ID, ProjectID: "proj_alpha", Type: "text", Text: "in the channel"})
	if err != nil {
		t.Fatal(err)
	}
	items, err = loadOutbox(home, circle.ID)
	if err != nil {
		t.Fatal(err)
	}
	var project outboxItem
	for _, item := range items {
		if item.Topic == projectChatTopic(circle.ID, "proj_alpha") {
			project = item
		}
	}
	if project.QoS != 1 || project.Topic == "" {
		t.Fatalf("project topic %+v", project)
	}
	if _, _, err := postLocalChat(home, chatPostRequest{MeshID: circle.ID, ProjectID: "SECRET/REPO", Type: "text", Text: "nope"}); err == nil {
		t.Fatal("accepted a repository path as a project id")
	}
}

func TestMeshAPIRoutesLoopbackHistoryAndCards(t *testing.T) {
	home := meshHome(t)
	self, _ := testDevice(t)
	writeIdentityFile(t, home, self)
	if _, err := setLocalUserDisplayName(home, "Ada Lovelace"); err != nil {
		t.Fatal(err)
	}
	circle := saveTestCircle(t, home, testCircle(self))
	body := []byte(`{"mesh_id":"` + circle.ID + `","recipient":"dev_0011223344556677","type":"decision","title":"Ship","summary":"Do it","repository":"SECRET_REPO_PATH","files":["main.go"]}`)
	remote := meshHTTP(t, home, http.MethodPost, "/api/chat/messages", "10.9.9.9:9", body)
	if remote.Code != http.StatusForbidden {
		t.Fatalf("remote post %d", remote.Code)
	}
	for _, path := range []string{"/api/discovery/peers", "/api/chat/conversations?mesh_id=" + circle.ID, "/api/chat/messages?mesh_id=" + circle.ID + "&conversation_id=direct:a:b"} {
		got := meshHTTP(t, home, http.MethodGet, path, "10.9.9.9:9", nil)
		if got.Code != http.StatusForbidden {
			t.Fatalf("%s remote %d", path, got.Code)
		}
	}
	if _, err := os.Stat(chatDBPath(home)); !os.IsNotExist(err) {
		t.Fatal("remote post created chat history")
	}
	ok := meshHTTP(t, home, http.MethodPost, "/api/chat/messages", "127.0.0.1:9", body)
	if ok.Code != http.StatusOK {
		t.Fatalf("post %d %s", ok.Code, ok.Body.String())
	}
	assertNoKeyMaterial(t, ok.Body.String(), circle.GroupKey, self.PrivateKey, "SECRET_REPO_PATH", "main.go")
	var posted struct {
		Queued  bool         `json:"queued"`
		Message chatEnvelope `json:"message"`
	}
	if err := json.Unmarshal(ok.Body.Bytes(), &posted); err != nil {
		t.Fatal(err)
	}
	if !posted.Queued || posted.Message.Type != "decision" || posted.Message.MeshID != circle.ID {
		t.Fatalf("posted %+v", posted)
	}
	var card chatPayload
	if err := json.Unmarshal(posted.Message.Payload, &card); err != nil {
		t.Fatal(err)
	}
	if card.Title != "Ship" || card.Summary != "Do it" || card.Text != "" {
		t.Fatalf("card %+v", card)
	}
	history := meshHTTP(t, home, http.MethodGet, "/api/chat/messages?mesh_id="+circle.ID+"&conversation_id="+posted.Message.ConversationID, "127.0.0.1:9", nil)
	if history.Code != http.StatusOK || !strings.Contains(history.Body.String(), "Ship") {
		t.Fatalf("history %d %s", history.Code, history.Body.String())
	}
	assertNoKeyMaterial(t, history.Body.String(), circle.GroupKey, self.PrivateKey, "SECRET_REPO_PATH")
	convs := meshHTTP(t, home, http.MethodGet, "/api/chat/conversations?mesh_id="+circle.ID, "[::1]:9", nil)
	if convs.Code != http.StatusOK || !strings.Contains(convs.Body.String(), posted.Message.ConversationID) {
		t.Fatalf("conversations %d %s", convs.Code, convs.Body.String())
	}
	dbRaw, err := os.ReadFile(chatDBPath(home))
	if err != nil {
		t.Fatal(err)
	}
	assertNoKeyMaterial(t, string(dbRaw), "SECRET_REPO_PATH", "main.go", circle.GroupKey, self.PrivateKey)
	if err := pauseMesh(home); err != nil {
		t.Fatal(err)
	}
	again := meshHTTP(t, home, http.MethodGet, "/api/chat/messages?mesh_id="+circle.ID+"&conversation_id="+posted.Message.ConversationID, "127.0.0.1:9", nil)
	if again.Code != http.StatusOK || !strings.Contains(again.Body.String(), "Do it") {
		t.Fatalf("paused history %d %s", again.Code, again.Body.String())
	}
	if _, err := os.Stat(chatDBPath(home)); err != nil {
		t.Fatal(err)
	}
	closeChatDBs()
	reloaded, err := listChatMessages(home, circle.ID, posted.Message.ConversationID, false)
	if err != nil || len(reloaded) != 1 || reloaded[0].MessageID != posted.Message.MessageID {
		t.Fatalf("restart history %+v %v", reloaded, err)
	}
}

func TestMeshChatFileMessage(t *testing.T) {
	home := meshHome(t)
	self, _ := testDevice(t)
	writeIdentityFile(t, home, self)
	circle := saveTestCircle(t, home, testCircle(self))
	raw := []byte("hello attachment")
	data := base64.StdEncoding.EncodeToString(raw)
	body := []byte(`{"mesh_id":"` + circle.ID + `","recipient":"dev_0011223344556677","type":"file","file_name":"note.txt","mime_type":"text/plain","data":"` + data + `","text":"see file"}`)
	ok := meshHTTP(t, home, http.MethodPost, "/api/chat/messages", "127.0.0.1:9", body)
	if ok.Code != http.StatusOK {
		t.Fatalf("post file %d %s", ok.Code, ok.Body.String())
	}
	var posted struct {
		Message chatEnvelope `json:"message"`
	}
	if err := json.Unmarshal(ok.Body.Bytes(), &posted); err != nil {
		t.Fatal(err)
	}
	if posted.Message.Type != "file" {
		t.Fatalf("type %+v", posted.Message)
	}
	var file chatPayload
	if err := json.Unmarshal(posted.Message.Payload, &file); err != nil {
		t.Fatal(err)
	}
	if file.FileName != "note.txt" || file.Text != "see file" || file.Data != data {
		t.Fatalf("payload %+v", file)
	}
	decoded, err := base64.StdEncoding.DecodeString(file.Data)
	if err != nil || !bytes.Equal(decoded, raw) {
		t.Fatalf("decode %+v", err)
	}
	history := meshHTTP(t, home, http.MethodGet, "/api/chat/messages?mesh_id="+circle.ID+"&conversation_id="+posted.Message.ConversationID, "127.0.0.1:9", nil)
	if history.Code != http.StatusOK || !strings.Contains(history.Body.String(), "note.txt") {
		t.Fatalf("history %d %s", history.Code, history.Body.String())
	}
	_, _, err = postLocalChat(home, chatPostRequest{
		MeshID: circle.ID, Recipient: "dev_0011223344556677", Type: "file",
		FileName: "big.bin", Data: base64.StdEncoding.EncodeToString(make([]byte, chatFileMaxBytes+1)),
	})
	if err == nil || !strings.Contains(err.Error(), "too large") {
		t.Fatalf("oversize file err=%v", err)
	}
}

func TestMeshChatAudioFileMessage(t *testing.T) {
	home := meshHome(t)
	self, _ := testDevice(t)
	writeIdentityFile(t, home, self)
	circle := saveTestCircle(t, home, testCircle(self))
	raw := []byte{0x1a, 0x45, 0xdf, 0xa3}
	data := base64.StdEncoding.EncodeToString(raw)
	body := []byte(`{"mesh_id":"` + circle.ID + `","recipient":"dev_0011223344556677","type":"file","file_name":"clip.webm","mime_type":"audio/webm","data":"` + data + `"}`)
	ok := meshHTTP(t, home, http.MethodPost, "/api/chat/messages", "127.0.0.1:9", body)
	if ok.Code != http.StatusOK {
		t.Fatalf("post audio file %d %s", ok.Code, ok.Body.String())
	}
	var posted struct {
		Message chatEnvelope `json:"message"`
	}
	if err := json.Unmarshal(ok.Body.Bytes(), &posted); err != nil {
		t.Fatal(err)
	}
	var file chatPayload
	if err := json.Unmarshal(posted.Message.Payload, &file); err != nil {
		t.Fatal(err)
	}
	if file.MimeType != "audio/webm" || file.FileName != "clip.webm" {
		t.Fatalf("payload %+v", file)
	}
}

func TestMeshDuplicateDeliveryKeepsOneRow(t *testing.T) {
	home := meshHome(t)
	self, _ := testDevice(t)
	peer, peerPriv := testDevice(t)
	writeIdentityFile(t, home, self)
	circle := saveTestCircle(t, home, testCircle(self, peer))
	payload, err := json.Marshal(chatPayload{Text: "once only"})
	if err != nil {
		t.Fatal(err)
	}
	env := chatEnvelope{
		Version: chatEnvelopeVersion, MessageID: "msg_duplicate_fixed_0001", MeshID: circle.ID,
		ConversationID: directConversationID(self.DeviceID, peer.DeviceID),
		SenderUserID:   "usr_0123456789abcdef", SenderDeviceID: peer.DeviceID,
		Recipient: self.DeviceID, Type: "text", Timestamp: nowRFC3339(), Payload: payload,
	}
	env, err = signChatEnvelope(peerPriv, env)
	if err != nil {
		t.Fatal(err)
	}
	wire, err := sealChatEnvelope(circle.GroupKey, env)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(wire, []byte("once only")) || strings.Contains(string(wire), circle.GroupKey) {
		t.Fatal("wire exposed chat text or the group key")
	}
	inserted, err := acceptChatWire(home, wire)
	if err != nil || !inserted {
		t.Fatalf("insert %v %v", inserted, err)
	}
	inserted, err = acceptChatWire(home, wire)
	if err != nil || inserted {
		t.Fatalf("duplicate insert %v %v", inserted, err)
	}
	if err := markConversationRead(home, circle.ID, env.ConversationID); err != nil {
		t.Fatal(err)
	}
	inserted, err = acceptChatWire(home, wire)
	if err != nil || inserted {
		t.Fatalf("redelivery after read %v %v", inserted, err)
	}
	db, err := openChatDB(home)
	if err != nil {
		t.Fatal(err)
	}
	var count, unread int
	if err := db.QueryRow(`SELECT COUNT(*), COALESCE(SUM(unread), 0) FROM messages WHERE message_id = ?`, env.MessageID).Scan(&count, &unread); err != nil {
		t.Fatal(err)
	}
	if count != 1 || unread != 0 {
		t.Fatalf("count %d unread %d", count, unread)
	}
}

func TestMeshBrokerDropResendsAndDedups(t *testing.T) {
	home := meshHome(t)
	immediateFailover(t)
	creator, creatorPriv := testDevice(t)
	self, selfPriv := testDevice(t)
	writeIdentityFile(t, home, self)
	if _, err := setLocalUserDisplayName(home, "Ada Lovelace"); err != nil {
		t.Fatal(err)
	}
	circle := saveTestCircle(t, home, testCircle(creator, self))
	remote, err := listenMemberBroker(home, circle, creator, creatorPriv)
	if err != nil {
		t.Fatal(err)
	}
	setMeshEndpoint(creator.DeviceID, remote.addr)
	remote.close()

	const secretText = "circle-secret-sentence-do-not-leak"
	first, queued, err := postLocalChat(home, chatPostRequest{MeshID: circle.ID, Recipient: creator.DeviceID, Type: "text", Text: secretText})
	if err != nil || !queued {
		t.Fatalf("queue %v %v", queued, err)
	}
	second, _, err := postLocalChat(home, chatPostRequest{MeshID: circle.ID, Recipient: creator.DeviceID, Type: "checkpoint", Title: "Later", Summary: "Still here"})
	if err != nil {
		t.Fatal(err)
	}
	items, err := loadOutbox(home, circle.ID)
	if err != nil || len(items) != 2 {
		t.Fatalf("outbox %d %v", len(items), err)
	}

	var mu sync.Mutex
	got := map[string][]byte{}
	setMeshDeliveryHook(func(packet *pahomqtt.Publish) {
		if packet == nil || !strings.Contains(packet.Topic, "/chat/") {
			return
		}
		if packet.QoS != 1 {
			t.Errorf("chat qos %d", packet.QoS)
		}
		var peek struct {
			MessageID string `json:"messageId"`
		}
		if json.Unmarshal(packet.Payload, &peek) != nil || peek.MessageID == "" {
			return
		}
		mu.Lock()
		got[peek.MessageID] = append([]byte(nil), packet.Payload...)
		mu.Unlock()
	})

	host, err := recoverCircleMesh(home, circle, self, selfPriv)
	if err != nil {
		t.Fatal(err)
	}
	if host != self.DeviceID {
		t.Fatalf("elected %s", host)
	}
	// This device hosts the chat with no one else on it: publishing would reach no one, so the chat waits.
	if left, err := loadOutbox(home, circle.ID); err != nil || len(left) != 2 {
		t.Fatalf("chat must stay queued while no other device is on it: %d %v", len(left), err)
	}
	addr, ok := meshEndpoint(self.DeviceID)
	if !ok {
		t.Fatal("missing broker address")
	}
	guest, err := dialMeshClient(home, circle, creator, creatorPriv, self.DeviceID, addr, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { closePaho(guest) })
	waitUntil(t, 4*time.Second, func() bool {
		mu.Lock()
		defer mu.Unlock()
		_, ok1 := got[first.MessageID]
		_, ok2 := got[second.MessageID]
		return ok1 && ok2
	})
	mu.Lock()
	defer mu.Unlock()
	if _, ok := got["sequence"]; ok {
		t.Fatal("chat gained a global sequence")
	}
	for id, body := range got {
		if bytes.Contains(body, []byte(secretText)) || bytes.Contains(body, []byte("Still here")) || strings.Contains(string(body), circle.GroupKey) || strings.Contains(string(body), self.PrivateKey) {
			t.Fatalf("broker payload for %s exposed chat text or key material", id)
		}
		if strings.Contains(string(body), `"sequence"`) {
			t.Fatal("envelope invented a global order")
		}
		inserted, err := acceptChatWire(home, body)
		if err != nil {
			t.Fatal(err)
		}
		if inserted {
			t.Fatal("duplicate mqtt delivery created a second chat row")
		}
	}
	var count int
	db, err := openChatDB(home)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`SELECT COUNT(*) FROM messages WHERE message_id IN (?, ?)`, first.MessageID, second.MessageID).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 2 {
		t.Fatalf("rows %d", count)
	}
	left, err := loadOutbox(home, circle.ID)
	if err != nil || len(left) != 0 {
		t.Fatalf("outbox after resend %+v %v", left, err)
	}
}

func TestMeshPauseClosesBrokerAndKeepsHistory(t *testing.T) {
	home := meshHome(t)
	self, priv := testDevice(t)
	writeIdentityFile(t, home, self)
	if _, err := loadOrCreateLocalUser(home); err != nil {
		t.Fatal(err)
	}
	circle := saveTestCircle(t, home, testCircle(self))
	env, _, err := postLocalChat(home, chatPostRequest{MeshID: circle.ID, Recipient: "dev_00112233445566aa", Type: "text", Text: "keep me"})
	if err != nil {
		t.Fatal(err)
	}
	host, err := recoverCircleMesh(home, circle, self, priv)
	if err != nil {
		t.Fatal(err)
	}
	if host != self.DeviceID {
		t.Fatalf("host %s", host)
	}
	addr, ok := meshEndpoint(self.DeviceID)
	if !ok {
		t.Fatal("missing broker address")
	}
	if err := pauseMesh(home); err != nil {
		t.Fatal(err)
	}
	conn, err := net.DialTimeout("tcp", addr, 300*time.Millisecond)
	if err == nil {
		_ = conn.Close()
		t.Fatal("paused mesh left the broker listening")
	}
	messages, err := listChatMessages(home, circle.ID, env.ConversationID, false)
	if err != nil || len(messages) != 1 || messages[0].MessageID != env.MessageID {
		t.Fatalf("history after pause %+v %v", messages, err)
	}
	if _, err := os.Stat(chatDBPath(home)); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(dbPath(home)); !os.IsNotExist(err) {
		t.Fatal("pause removed or created the experience database")
	}
}

func TestMeshPresenceIsRetainedQoS0WithExpiry(t *testing.T) {
	home := meshHome(t)
	self, selfPriv := testDevice(t)
	observer, observerPriv := testDevice(t)
	writeIdentityFile(t, home, self)
	if _, err := setLocalUserDisplayName(home, "Ada Lovelace"); err != nil {
		t.Fatal(err)
	}
	circle := saveTestCircle(t, home, testCircle(self, observer))
	broker, err := listenMemberBroker(home, circle, self, selfPriv)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(broker.close)
	client, err := dialMeshClient(home, circle, self, selfPriv, self.DeviceID, broker.addr, func(*pahomqtt.Publish) {})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { closePaho(client) })
	meshRT.mu.Lock()
	meshRT.initLocked()
	if meshRT.links[circle.ID] == nil {
		meshRT.links[circle.ID] = &circleLink{}
	}
	meshRT.links[circle.ID].client = client
	meshRT.mu.Unlock()
	if err := publishPresence(home, circle, self); err != nil {
		t.Fatal(err)
	}
	meshRT.mu.Lock()
	if link := meshRT.links[circle.ID]; link != nil {
		link.client = nil
	}
	meshRT.mu.Unlock()

	late := make(chan *pahomqtt.Publish, 1)
	watcher, err := dialMeshClient(home, circle, observer, observerPriv, self.DeviceID, broker.addr, func(packet *pahomqtt.Publish) {
		if packet != nil && strings.Contains(packet.Topic, "/presence/") {
			select {
			case late <- packet:
			default:
			}
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { closePaho(watcher) })
	select {
	case packet := <-late:
		if packet.QoS != 0 || !packet.Retain {
			t.Fatalf("retained presence qos %d retain %v", packet.QoS, packet.Retain)
		}
		if packet.Properties == nil || packet.Properties.MessageExpiry == nil || *packet.Properties.MessageExpiry == 0 || *packet.Properties.MessageExpiry > presenceExpirySeconds {
			t.Fatalf("presence expiry %+v", packet.Properties)
		}
		if bytes.Contains(packet.Payload, []byte(circle.GroupKey)) || bytes.Contains(packet.Payload, []byte(self.PrivateKey)) {
			t.Fatal("presence exposed key material")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("new subscriber did not receive retained presence")
	}
}

func TestMeshPresenceExpires(t *testing.T) {
	home := meshHome(t)
	self, selfPriv := testDevice(t)
	observer, observerPriv := testDevice(t)
	writeIdentityFile(t, home, self)
	if _, err := loadOrCreateLocalUser(home); err != nil {
		t.Fatal(err)
	}
	circle := saveTestCircle(t, home, testCircle(self, observer))
	broker, err := listenMemberBroker(home, circle, self, selfPriv)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(broker.close)
	client, err := dialMeshClient(home, circle, self, selfPriv, self.DeviceID, broker.addr, func(*pahomqtt.Publish) {})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { closePaho(client) })
	expiry := uint32(1)
	body := []byte(`{"deviceId":"` + self.DeviceID + `","online":true}`)
	if err := publishMQTT(client, presenceTopic(circle.ID, self.DeviceID), 0, body, true, &expiry); err != nil {
		t.Fatal(err)
	}
	time.Sleep(3 * time.Second)
	late := make(chan struct{}, 1)
	watcher, err := dialMeshClient(home, circle, observer, observerPriv, self.DeviceID, broker.addr, func(packet *pahomqtt.Publish) {
		if packet != nil && packet.Topic == presenceTopic(circle.ID, self.DeviceID) && packet.Retain {
			select {
			case late <- struct{}{}:
			default:
			}
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { closePaho(watcher) })
	select {
	case <-late:
		t.Fatal("expired presence was still retained")
	case <-time.After(1500 * time.Millisecond):
	}
}

func TestMeshEpochOpenTracksArmedEpoch(t *testing.T) {
	meshHome(t)
	meshRT.mu.Lock()
	meshRT.initLocked()
	meshRT.armed = true
	meshRT.epoch = 7
	meshRT.closing = false
	meshRT.mu.Unlock()
	if meshEpoch() != 7 {
		t.Fatalf("epoch %d", meshEpoch())
	}
	if !meshEpochOpen(7) || meshEpochOpen(6) {
		t.Fatal("epoch open mismatch")
	}
	meshRT.mu.Lock()
	meshRT.closing = true
	meshRT.mu.Unlock()
	if meshEpochOpen(7) {
		t.Fatal("closing epoch still open")
	}
}

func TestBrokerAddrFromPeerCatalog(t *testing.T) {
	homeHost := meshHome(t)
	sl, endpoint := startShareListenerForTest(t, homeHost)
	defer sl.Close()
	host, hostPriv, err := loadOrCreateIdentity(homeHost)
	if err != nil {
		t.Fatal(err)
	}
	peer, peerPriv := testDevice(t)
	circle := testCircle(host, peer)
	for i := range circle.Members {
		if circle.Members[i].DeviceID == host.DeviceID {
			circle.Members[i].Endpoint = endpoint
		}
	}
	saveTestCircle(t, homeHost, circle)
	meshRT.mu.Lock()
	meshRT.initLocked()
	meshRT.armed = true
	meshRT.home = homeHost
	meshRT.mu.Unlock()
	brokerAddr, err := ensureLocalBroker(homeHost, circle, host, hostPriv)
	if err != nil {
		t.Fatal(err)
	}
	_, brokerPort, err := net.SplitHostPort(brokerAddr)
	if err != nil {
		t.Fatal(err)
	}
	meshRT.mu.Lock()
	broker := meshRT.links[circle.ID].broker
	meshRT.mu.Unlock()
	if broker == nil {
		t.Fatal("missing broker")
	}
	t.Cleanup(broker.close)
	homePeer := t.TempDir()
	writeIdentityFile(t, homePeer, peer)
	saveTestCircle(t, homePeer, circle)

	resp, err := fetchPeerCatalog(homePeer, endpoint, circle, peer, peerPriv)
	if err != nil {
		t.Fatalf("fetch catalog: %v", err)
	}
	if resp.MeshMQTTPort <= 0 {
		t.Fatalf("catalog mqtt port %d", resp.MeshMQTTPort)
	}

	addr, err := brokerAddrFromPeerCatalog(homePeer, circle, peer, peerPriv, host.DeviceID)
	if err != nil {
		t.Fatal(err)
	}
	hostIP, port, err := net.SplitHostPort(addr)
	if err != nil || hostIP != "127.0.0.1" || port != brokerPort || port != strconv.Itoa(broker.port) {
		t.Fatalf("addr %q broker port %d", addr, broker.port)
	}
	got, ok := meshEndpoint(host.DeviceID)
	if !ok || got != addr {
		t.Fatalf("cached endpoint %q ok %v", got, ok)
	}

	if _, err := brokerAddrFromPeerCatalog(homePeer, circle, peer, peerPriv, "dev_missing"); err != errBrokerUnknown {
		t.Fatalf("missing member err=%v", err)
	}
	empty := circle
	empty.Members = circle.Members
	for i := range empty.Members {
		if empty.Members[i].DeviceID == host.DeviceID {
			empty.Members[i].Endpoint = ""
		}
	}
	if _, err := brokerAddrFromPeerCatalog(homePeer, empty, peer, peerPriv, host.DeviceID); err != errBrokerUnknown {
		t.Fatalf("empty endpoint err=%v", err)
	}
}

func TestMeshBrokerRejectsOutsider(t *testing.T) {
	home := meshHome(t)
	self, selfPriv := testDevice(t)
	writeIdentityFile(t, home, self)
	circle := saveTestCircle(t, home, testCircle(self))
	broker, err := listenMemberBroker(home, circle, self, selfPriv)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(broker.close)
	member, err := dialMeshClient(home, circle, self, selfPriv, self.DeviceID, broker.addr, func(*pahomqtt.Publish) {})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { closePaho(member) })
	outsider, outsiderPriv := testDevice(t)
	if _, err := dialMeshClient(home, circle, outsider, outsiderPriv, self.DeviceID, broker.addr, func(*pahomqtt.Publish) {}); err == nil {
		t.Fatal("outsider connected to the circle broker")
	}
}
