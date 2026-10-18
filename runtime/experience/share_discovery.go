package main

import (
	"context"
	"encoding/json"
	"net"
	"os"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/hashicorp/mdns"
)

const (
	overdriveDNSSDService = "_overdrive._tcp"
	overdriveDNSSDDomain  = "local."
)

// discoveredPeer is a LAN announcement. Recording one does not join a circle.
type discoveredPeer struct {
	DeviceID     string `json:"device_id"`
	FriendlyName string `json:"friendly_name"`
	OS           string `json:"os"`
	DisplayName  string `json:"display_name"`
	Host         string `json:"host,omitempty"`
	Port         int    `json:"port,omitempty"`
	MQTTPort     int    `json:"mqtt_port,omitempty"`
	// LocalOnly is set when the device announced that it accepts connections only from itself. Cores before
	// 2.3 do not say, and are not assumed to be.
	LocalOnly bool   `json:"local_only,omitempty"`
	LastSeen  string `json:"last_seen"`
}

type discoveryAnnouncement struct {
	FriendlyName string
	OS           string
	DisplayName  string
	DeviceID     string
	Port         int
	MQTTPort     int
	// Net is "lan" when devices on the network can connect and "local" when only this computer can.
	Net string
}

type discoveredFile struct {
	Peers []discoveredPeer `json:"peers"`
}

type discoveryIgnoreFile struct {
	DeviceIDs []string `json:"device_ids"`
}

var discoveryMu sync.Mutex

func discoveredPath(home string) string {
	return shareDir(home) + "/discovered.json"
}

func discoveryIgnorePath(home string) string {
	return shareDir(home) + "/discovery-ignore.json"
}

func announcementTXT(ann discoveryAnnouncement) []string {
	txt := []string{
		"fn=" + ann.FriendlyName,
		"os=" + ann.OS,
		"dn=" + ann.DisplayName,
		"id=" + ann.DeviceID,
	}
	if ann.MQTTPort > 0 {
		txt = append(txt, "mqtt="+strconv.Itoa(ann.MQTTPort))
	}
	if ann.Net != "" {
		txt = append(txt, "net="+ann.Net)
	}
	return txt
}

func mdnsInstanceName(ann discoveryAnnouncement) string {
	base := ann.FriendlyName
	if base == "" {
		base = "overdrive"
	}
	var b strings.Builder
	for _, r := range base {
		switch {
		case r == '.' || r == ' ' || r < 0x20:
			b.WriteByte('-')
		default:
			b.WriteRune(r)
		}
	}
	name := strings.Trim(b.String(), "-")
	if name == "" {
		name = "overdrive"
	}
	if len(name) > 40 {
		name = name[:40]
	}
	suffix := ann.DeviceID
	if len(suffix) > 8 {
		suffix = suffix[len(suffix)-8:]
	}
	return name + "-" + suffix
}

func announcementIPs() []net.IP {
	if ip := firstLANIPv4(); ip != "" {
		if parsed := net.ParseIP(ip); parsed != nil {
			return []net.IP{parsed}
		}
	}
	return []net.IP{net.ParseIP("127.0.0.1")}
}

func shareAnnouncePort() int {
	raw := strings.TrimSpace(os.Getenv("OVERDRIVE_SHARE_LISTEN"))
	if raw == "" {
		return defaultSharePort
	}
	_, port, err := net.SplitHostPort(normalizeListen(raw))
	if err != nil {
		return defaultSharePort
	}
	n, err := strconv.Atoi(port)
	if err != nil || n <= 0 {
		return defaultSharePort
	}
	return n
}

func localDiscoveryAnnouncement(home string) (discoveryAnnouncement, error) {
	id, _, err := loadOrCreateIdentity(home)
	if err != nil {
		return discoveryAnnouncement{}, err
	}
	profile, err := loadOrCreateLocalUser(home)
	if err != nil {
		return discoveryAnnouncement{}, err
	}
	return discoveryAnnouncement{
		FriendlyName: computerName(),
		OS:           runtime.GOOS,
		DisplayName:  profile.DisplayName,
		DeviceID:     id.DeviceID,
		Port:         shareAnnouncePort(),
		MQTTPort:     meshMQTTPort(),
		Net:          announcedNet(),
	}, nil
}

func announcedNet() string {
	if announcesNetwork() {
		return "lan"
	}
	return "local"
}

// newOverdriveMDNS builds the DNS-SD service this device advertises.
// The TXT record carries the friendly name, OS, and user display name.
func newOverdriveMDNS(ann discoveryAnnouncement) (*mdns.MDNSService, error) {
	port := ann.Port
	if port <= 0 {
		port = defaultSharePort
	}
	return mdns.NewMDNSService(
		mdnsInstanceName(ann),
		overdriveDNSSDService,
		overdriveDNSSDDomain,
		"overdrive.local.",
		port,
		announcementIPs(),
		announcementTXT(ann),
	)
}

func peerFromTXT(txt []string, host string, port int) (discoveredPeer, bool) {
	fields := map[string]string{}
	for _, item := range txt {
		k, v, ok := strings.Cut(item, "=")
		if !ok || k == "" {
			continue
		}
		fields[k] = v
	}
	id := strings.TrimSpace(fields["id"])
	if id == "" || strings.ContainsAny(id, "/+#") {
		return discoveredPeer{}, false
	}
	peer := discoveredPeer{
		DeviceID:     id,
		FriendlyName: fields["fn"],
		OS:           fields["os"],
		DisplayName:  fields["dn"],
		Host:         host,
		Port:         port,
		LocalOnly:    fields["net"] == "local",
		LastSeen:     nowRFC3339(),
	}
	if raw := fields["mqtt"]; raw != "" {
		if n, err := strconv.Atoi(raw); err == nil && n > 0 && n < 65536 {
			peer.MQTTPort = n
		}
	}
	return peer, true
}

func discoveredPeerFromEntry(entry *mdns.ServiceEntry) (discoveredPeer, bool) {
	if entry == nil {
		return discoveredPeer{}, false
	}
	host := ""
	if entry.AddrV4 != nil {
		host = entry.AddrV4.String()
	} else if entry.Addr != nil {
		host = entry.Addr.String()
	}
	return peerFromTXT(entry.InfoFields, host, entry.Port)
}

func noteDiscoveredPeer(home string, peer discoveredPeer) error {
	peer.DeviceID = strings.TrimSpace(peer.DeviceID)
	if peer.DeviceID == "" {
		return nil
	}
	if existingDeviceID(home) == peer.DeviceID {
		return nil
	}
	if discoveryIgnored(home, peer.DeviceID) {
		return nil
	}
	if peer.LastSeen == "" {
		peer.LastSeen = nowRFC3339()
	}
	discoveryMu.Lock()
	file, err := loadDiscovered(home)
	if err != nil {
		discoveryMu.Unlock()
		return err
	}
	replaced := false
	for i := range file.Peers {
		if file.Peers[i].DeviceID == peer.DeviceID {
			file.Peers[i] = peer
			replaced = true
			break
		}
	}
	if !replaced {
		file.Peers = append(file.Peers, peer)
	}
	err = saveDiscovered(home, file)
	discoveryMu.Unlock()
	if err != nil {
		return err
	}
	if peer.MQTTPort > 0 && peer.Host != "" && meshServicesArmed() && meshAllowsChat() {
		setMeshEndpoint(peer.DeviceID, net.JoinHostPort(peer.Host, strconv.Itoa(peer.MQTTPort)))
		epoch := meshEpoch()
		go func() { _ = startCircleLinks(home, epoch) }()
	}
	return nil
}

// ignoreDiscoveredPeer dismisses an announcement. It never creates a circle member.
func ignoreDiscoveredPeer(home, deviceID string) error {
	deviceID = strings.TrimSpace(deviceID)
	if deviceID == "" {
		return nil
	}
	discoveryMu.Lock()
	defer discoveryMu.Unlock()
	ignored, err := loadIgnored(home)
	if err != nil {
		return err
	}
	if !containsString(ignored.DeviceIDs, deviceID) {
		ignored.DeviceIDs = append(ignored.DeviceIDs, deviceID)
		if err := saveIgnored(home, ignored); err != nil {
			return err
		}
	}
	file, err := loadDiscovered(home)
	if err != nil {
		return err
	}
	next := make([]discoveredPeer, 0, len(file.Peers))
	for _, peer := range file.Peers {
		if peer.DeviceID != deviceID {
			next = append(next, peer)
		}
	}
	file.Peers = next
	return saveDiscovered(home, file)
}

func listDiscoveredPeers(home string) ([]discoveredPeer, error) {
	discoveryMu.Lock()
	defer discoveryMu.Unlock()
	file, err := loadDiscovered(home)
	if err != nil {
		return nil, err
	}
	if file.Peers == nil {
		return []discoveredPeer{}, nil
	}
	byID := make(map[string]discoveredPeer, len(file.Peers))
	for _, peer := range file.Peers {
		id := strings.TrimSpace(peer.DeviceID)
		if id == "" {
			continue
		}
		prev, ok := byID[id]
		if !ok || peer.LastSeen > prev.LastSeen {
			byID[id] = peer
		}
	}
	out := make([]discoveredPeer, 0, len(byID))
	for _, peer := range byID {
		out = append(out, peer)
	}
	return out, nil
}

func discoveryIgnored(home, deviceID string) bool {
	ignored, err := loadIgnored(home)
	if err != nil {
		return false
	}
	return containsString(ignored.DeviceIDs, deviceID)
}

func loadDiscovered(home string) (discoveredFile, error) {
	var file discoveredFile
	data, err := hookReadFile(discoveredPath(home))
	if err != nil {
		if os.IsNotExist(err) {
			return discoveredFile{Peers: []discoveredPeer{}}, nil
		}
		return file, err
	}
	if err := json.Unmarshal(data, &file); err != nil {
		return discoveredFile{}, err
	}
	if file.Peers == nil {
		file.Peers = []discoveredPeer{}
	}
	return file, nil
}

func saveDiscovered(home string, file discoveredFile) error {
	if file.Peers == nil {
		file.Peers = []discoveredPeer{}
	}
	if err := hookMkdirAll(shareDir(home), 0o700); err != nil {
		return err
	}
	data, err := hookJSONMarshalIndent(file, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	return hookWriteFile(discoveredPath(home), data, 0o600)
}

func loadIgnored(home string) (discoveryIgnoreFile, error) {
	var file discoveryIgnoreFile
	data, err := hookReadFile(discoveryIgnorePath(home))
	if err != nil {
		if os.IsNotExist(err) {
			return discoveryIgnoreFile{DeviceIDs: []string{}}, nil
		}
		return file, err
	}
	if err := json.Unmarshal(data, &file); err != nil {
		return discoveryIgnoreFile{}, err
	}
	if file.DeviceIDs == nil {
		file.DeviceIDs = []string{}
	}
	return file, nil
}

func saveIgnored(home string, file discoveryIgnoreFile) error {
	if file.DeviceIDs == nil {
		file.DeviceIDs = []string{}
	}
	if err := hookMkdirAll(shareDir(home), 0o700); err != nil {
		return err
	}
	data, err := hookJSONMarshalIndent(file, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	return hookWriteFile(discoveryIgnorePath(home), data, 0o600)
}

func existingDeviceID(home string) string {
	data, err := hookReadFile(identityPath(home))
	if err != nil {
		return ""
	}
	var id DeviceIdentity
	if json.Unmarshal(data, &id) != nil {
		return ""
	}
	return id.DeviceID
}

func startDiscovery(home string, epoch uint64) error {
	if !meshAllowsDiscovery() || !meshEpochOpen(epoch) {
		return nil
	}
	ann, err := localDiscoveryAnnouncement(home)
	if err != nil {
		return err
	}
	svc, err := newOverdriveMDNS(ann)
	if err != nil {
		return err
	}
	srv, err := mdns.NewServer(&mdns.Config{Zone: svc, Logger: meshLog})
	if err != nil {
		return err
	}
	ctx, cancel := context.WithCancel(context.Background())
	if !registerDiscovery(epoch, srv, cancel) {
		cancel()
		_ = srv.Shutdown()
		return nil
	}
	go browseLoop(ctx, home)
	return nil
}

func browseLoop(ctx context.Context, home string) {
	browseOnce(ctx, home)
	ticker := time.NewTicker(3 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			browseOnce(ctx, home)
		}
	}
}

func browseOnce(ctx context.Context, home string) {
	if ctx.Err() != nil || !meshAllowsDiscovery() {
		return
	}
	entries := make(chan *mdns.ServiceEntry, 32)
	queryCtx, cancel := context.WithTimeout(ctx, 1200*time.Millisecond)
	defer cancel()
	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = mdns.QueryContext(queryCtx, &mdns.QueryParam{
			Service:             overdriveDNSSDService,
			Domain:              "local",
			Timeout:             time.Second,
			Entries:             entries,
			DisableIPv6:         true,
			WantUnicastResponse: true,
			Logger:              meshLog,
		})
	}()
	timer := time.NewTimer(1300 * time.Millisecond)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case entry := <-entries:
			peer, ok := discoveredPeerFromEntry(entry)
			if !ok {
				continue
			}
			_ = noteDiscoveredPeer(home, peer)
		case <-done:
			return
		case <-timer.C:
			return
		}
	}
}

// restartDiscovery announces again after something peers read from the announcement changed. Discovery that
// was not running stays off.
func restartDiscovery(home string) {
	meshRT.mu.Lock()
	srv, cancel, epoch := meshRT.discovery, meshRT.discoveryCancel, meshRT.epoch
	meshRT.discovery, meshRT.discoveryCancel = nil, nil
	meshRT.mu.Unlock()
	if srv == nil {
		return
	}
	if cancel != nil {
		cancel()
	}
	_ = srv.Shutdown()
	_ = startDiscovery(home, epoch)
}

func discoveryAdvertising() bool {
	meshRT.mu.Lock()
	defer meshRT.mu.Unlock()
	return meshRT.discovery != nil
}
