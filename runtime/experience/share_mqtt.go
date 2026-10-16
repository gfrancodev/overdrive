package main

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"log/slog"
	"math/big"
	"net"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	pahopackets "github.com/eclipse/paho.golang/packets"
	pahomqtt "github.com/eclipse/paho.golang/paho"
	"github.com/hashicorp/mdns"
	mqtt "github.com/mochi-mqtt/server/v2"
	"github.com/mochi-mqtt/server/v2/listeners"
	mqpackets "github.com/mochi-mqtt/server/v2/packets"
)

const presenceExpirySeconds uint32 = 45

var (
	meshLog           = log.New(io.Discard, "", 0)
	meshSlog          = slog.New(slog.NewTextHandler(io.Discard, nil))
	errMeshClientDown = errors.New("mesh client is down")
	errMeshSuspended  = errors.New("mesh chat is suspended")
	errBrokerUnknown  = errors.New("broker endpoint unknown")

	meshDeliveryMu   sync.Mutex
	hookMeshDelivery func(*pahomqtt.Publish)
)

// electBroker picks the circle creator, or the next active member in
// lexicographic device-id order when the creator is gone. Revoked members
// and members marked gone are skipped. There is no global message order.
func electBroker(circle Circle, gone map[string]bool) (string, bool) {
	if gone == nil {
		gone = map[string]bool{}
	}
	seen := map[string]bool{}
	active := make([]string, 0, len(circle.Members))
	for _, member := range circle.Members {
		if member.Revoked || member.DeviceID == "" || seen[member.DeviceID] {
			continue
		}
		seen[member.DeviceID] = true
		active = append(active, member.DeviceID)
	}
	if len(active) == 0 {
		return "", false
	}
	sort.Strings(active)
	creator := circle.CreatorDeviceID
	if seen[creator] && !gone[creator] {
		return creator, true
	}
	start := 0
	for i, id := range active {
		if id == creator {
			start = i + 1
			break
		}
	}
	for n := 0; n < len(active); n++ {
		id := active[(start+n)%len(active)]
		if id == creator || gone[id] {
			continue
		}
		return id, true
	}
	return "", false
}

type circleBroker struct {
	server *mqtt.Server
	addr   string
	port   int
	once   sync.Once
}

func (b *circleBroker) close() {
	if b == nil || b.server == nil {
		return
	}
	b.once.Do(func() { _ = b.server.Close() })
}

type circleLink struct {
	broker *circleBroker
	client *pahomqtt.Client
	hostID string
	addr   string
}

type meshRuntime struct {
	mu              sync.Mutex
	armed           bool
	closing         bool
	epoch           uint64
	home            string
	mqttPort        int
	discovery       *mdns.Server
	discoveryCancel context.CancelFunc
	endpoints       map[string]string
	gone            map[string]map[string]bool
	links           map[string]*circleLink
	recovering      map[string]bool
}

var meshRT meshRuntime

func (m *meshRuntime) initLocked() {
	if m.endpoints == nil {
		m.endpoints = map[string]string{}
	}
	if m.gone == nil {
		m.gone = map[string]map[string]bool{}
	}
	if m.links == nil {
		m.links = map[string]*circleLink{}
	}
	if m.recovering == nil {
		m.recovering = map[string]bool{}
	}
}

func meshServicesArmed() bool {
	meshRT.mu.Lock()
	defer meshRT.mu.Unlock()
	return meshRT.armed
}

func meshEpoch() uint64 {
	meshRT.mu.Lock()
	defer meshRT.mu.Unlock()
	return meshRT.epoch
}

func meshEpochOpen(epoch uint64) bool {
	meshRT.mu.Lock()
	defer meshRT.mu.Unlock()
	return meshRT.epoch == epoch && meshRT.armed && !meshRT.closing
}

func meshClosing() bool {
	meshRT.mu.Lock()
	defer meshRT.mu.Unlock()
	return meshRT.closing
}

func chatTransportOpen() bool {
	return meshAllowsChat() && !meshClosing()
}

func meshMQTTPort() int {
	meshRT.mu.Lock()
	defer meshRT.mu.Unlock()
	return meshRT.mqttPort
}

func setMeshEndpoint(deviceID, addr string) {
	meshRT.mu.Lock()
	defer meshRT.mu.Unlock()
	meshRT.initLocked()
	meshRT.endpoints[deviceID] = addr
}

func meshEndpoint(deviceID string) (string, bool) {
	meshRT.mu.Lock()
	defer meshRT.mu.Unlock()
	meshRT.initLocked()
	addr, ok := meshRT.endpoints[deviceID]
	return addr, ok && addr != ""
}

func markBrokerGone(circleID, deviceID string) {
	meshRT.mu.Lock()
	defer meshRT.mu.Unlock()
	meshRT.initLocked()
	if meshRT.gone[circleID] == nil {
		meshRT.gone[circleID] = map[string]bool{}
	}
	meshRT.gone[circleID][deviceID] = true
}

func brokerGoneSet(circleID string) map[string]bool {
	meshRT.mu.Lock()
	defer meshRT.mu.Unlock()
	meshRT.initLocked()
	out := map[string]bool{}
	for id, gone := range meshRT.gone[circleID] {
		if gone {
			out[id] = true
		}
	}
	return out
}

func meshClient(meshID string) *pahomqtt.Client {
	meshRT.mu.Lock()
	defer meshRT.mu.Unlock()
	link := meshRT.links[meshID]
	if link == nil {
		return nil
	}
	return link.client
}

func clearMeshClient(meshID string) {
	meshRT.mu.Lock()
	defer meshRT.mu.Unlock()
	if link := meshRT.links[meshID]; link != nil {
		link.client = nil
	}
}

func alreadyLinked(circleID, host string) bool {
	meshRT.mu.Lock()
	defer meshRT.mu.Unlock()
	link := meshRT.links[circleID]
	return link != nil && link.client != nil && link.hostID == host
}

func setMeshDeliveryHook(fn func(*pahomqtt.Publish)) {
	meshDeliveryMu.Lock()
	hookMeshDelivery = fn
	meshDeliveryMu.Unlock()
}

func emitMeshDelivery(packet *pahomqtt.Publish) {
	meshDeliveryMu.Lock()
	hook := hookMeshDelivery
	meshDeliveryMu.Unlock()
	if hook != nil && packet != nil {
		hook(packet)
	}
}

func meshTrySend(meshID, topic string, qos byte, body []byte) error {
	if !chatTransportOpen() {
		return errMeshClientDown
	}
	client := meshClient(meshID)
	if client == nil {
		return errMeshClientDown
	}
	return publishMQTT(client, topic, qos, body, false, nil)
}

func publishMQTT(client *pahomqtt.Client, topic string, qos byte, body []byte, retain bool, expiry *uint32) error {
	if client == nil {
		return errMeshClientDown
	}
	pub := &pahomqtt.Publish{Topic: topic, QoS: qos, Payload: append([]byte(nil), body...), Retain: retain}
	if expiry != nil {
		pub.Properties = &pahomqtt.PublishProperties{MessageExpiry: expiry}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()
	_, err := client.Publish(ctx, pub)
	return err
}

type circleTopicHook struct {
	mqtt.HookBase
	prefix string
}

func (h *circleTopicHook) ID() string { return "overdrive-circle-acl" }

func (h *circleTopicHook) Provides(b byte) bool {
	return b == mqtt.OnConnectAuthenticate || b == mqtt.OnACLCheck
}

func (h *circleTopicHook) OnConnectAuthenticate(*mqtt.Client, mqpackets.Packet) bool {
	return true
}

func (h *circleTopicHook) OnACLCheck(_ *mqtt.Client, topic string, _ bool) bool {
	return strings.HasPrefix(topic, h.prefix)
}

func deviceCertificate(id DeviceIdentity, priv ed25519.PrivateKey) (tls.Certificate, error) {
	pub, ok := priv.Public().(ed25519.PublicKey)
	if !ok {
		return tls.Certificate{}, errors.New("device key is not ed25519")
	}
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return tls.Certificate{}, err
	}
	now := time.Now()
	tmpl := &x509.Certificate{
		SerialNumber:          serial,
		Subject:               pkix.Name{CommonName: id.DeviceID},
		NotBefore:             now.Add(-time.Hour),
		NotAfter:              now.Add(365 * 24 * time.Hour),
		KeyUsage:              x509.KeyUsageDigitalSignature,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth, x509.ExtKeyUsageServerAuth},
		DNSNames:              []string{id.DeviceID},
		BasicConstraintsValid: true,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, pub, priv)
	if err != nil {
		return tls.Certificate{}, err
	}
	return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: priv}, nil
}

func verifyDeviceCert(raw [][]byte, allow func(ed25519.PublicKey) error) error {
	if len(raw) == 0 || len(raw[0]) == 0 {
		return errors.New("missing peer certificate")
	}
	cert, err := x509.ParseCertificate(raw[0])
	if err != nil {
		return err
	}
	now := time.Now()
	if now.Before(cert.NotBefore) || now.After(cert.NotAfter) {
		return errors.New("peer certificate is outside its validity")
	}
	pub, ok := cert.PublicKey.(ed25519.PublicKey)
	if !ok || cert.SignatureAlgorithm != x509.PureEd25519 {
		return errors.New("peer certificate key is not ed25519")
	}
	if !ed25519.Verify(pub, cert.RawTBSCertificate, cert.Signature) {
		return errors.New("peer certificate is not signed by its device key")
	}
	return allow(pub)
}

func circleForTLS(home string, fallback Circle) Circle {
	if strings.TrimSpace(home) == "" {
		return fallback
	}
	if current, err := loadCircle(home, fallback.ID); err == nil {
		return current
	}
	return fallback
}

func allowCircleMember(circle Circle, pub ed25519.PublicKey) error {
	if circle.isActiveMemberPubKey(base64.StdEncoding.EncodeToString(pub)) {
		return nil
	}
	return errors.New("peer is not a circle member")
}

func allowBrokerKey(circle Circle, brokerID string, pub ed25519.PublicKey) error {
	member, ok := circle.activeMember(brokerID)
	if !ok {
		return errors.New("broker is not a circle member")
	}
	if member.PublicKey != base64.StdEncoding.EncodeToString(pub) {
		return errors.New("broker certificate does not match the elected device")
	}
	return nil
}

func listenMemberBroker(home string, circle Circle, id DeviceIdentity, priv ed25519.PrivateKey) (*circleBroker, error) {
	cert, err := deviceCertificate(id, priv)
	if err != nil {
		return nil, err
	}
	serverTLS := &tls.Config{
		MinVersion:   tls.VersionTLS13,
		Certificates: []tls.Certificate{cert},
		ClientAuth:   tls.RequireAnyClientCert,
		VerifyPeerCertificate: func(raw [][]byte, _ [][]*x509.Certificate) error {
			return verifyDeviceCert(raw, func(pub ed25519.PublicKey) error {
				return allowCircleMember(circleForTLS(home, circle), pub)
			})
		},
	}
	caps := mqtt.NewDefaultServerCapabilities()
	caps.MinimumProtocolVersion = 5
	caps.Compatibilities.NoInheritedPropertiesOnAck = true
	caps.Compatibilities.PassiveClientDisconnect = true
	server := mqtt.New(&mqtt.Options{Logger: meshSlog, Capabilities: caps})
	if err := server.AddHook(&circleTopicHook{prefix: meshPrefix(circle.ID)}, nil); err != nil {
		return nil, err
	}
	ln := listeners.NewTCP(listeners.Config{
		ID:        "overdrive-" + circle.ID,
		Address:   "0.0.0.0:0",
		TLSConfig: serverTLS,
	})
	if err := server.AddListener(ln); err != nil {
		return nil, err
	}
	if err := server.Serve(); err != nil {
		_ = server.Close()
		return nil, err
	}
	_, portText, err := net.SplitHostPort(ln.Address())
	if err != nil {
		_ = server.Close()
		return nil, err
	}
	port, err := strconv.Atoi(portText)
	if err != nil {
		_ = server.Close()
		return nil, err
	}
	return &circleBroker{
		server: server,
		addr:   net.JoinHostPort("127.0.0.1", portText),
		port:   port,
	}, nil
}

func meshClientPublishHandler(home string, onPub func(*pahomqtt.Publish)) func(pahomqtt.PublishReceived) (bool, error) {
	return func(pr pahomqtt.PublishReceived) (bool, error) {
		if pr.Packet == nil {
			return true, nil
		}
		emitMeshDelivery(pr.Packet)
		if onPub != nil {
			onPub(pr.Packet)
			return true, nil
		}
		if meshAllowsChat() {
			meshAcceptDefaultPublish(home, pr.Packet)
		}
		return true, nil
	}
}

func meshAcceptDefaultPublish(home string, pkt *pahomqtt.Publish) {
	if strings.Contains(pkt.Topic, "/chat/") {
		_, _ = acceptChatWire(home, pkt.Payload)
	}
	if strings.Contains(pkt.Topic, "/call/") {
		_ = acceptCallSignalWire(home, pkt.Payload)
	}
	if strings.Contains(pkt.Topic, "/terminal/") {
		_ = acceptTerminalWire(home, pkt.Payload)
	}
}

func dialMeshClient(home string, circle Circle, self DeviceIdentity, priv ed25519.PrivateKey, brokerID, addr string, onPub func(*pahomqtt.Publish)) (*pahomqtt.Client, error) {
	cert, err := deviceCertificate(self, priv)
	if err != nil {
		return nil, err
	}
	cfg := &tls.Config{
		MinVersion:   tls.VersionTLS13,
		Certificates: []tls.Certificate{cert},
		ServerName:   brokerID,
		// Hostname and public CA checks are skipped. VerifyPeerCertificate still
		// requires a certificate signed by the elected device's ed25519 key.
		InsecureSkipVerify: true,
		VerifyPeerCertificate: func(raw [][]byte, _ [][]*x509.Certificate) error {
			return verifyDeviceCert(raw, func(pub ed25519.PublicKey) error {
				return allowBrokerKey(circleForTLS(home, circle), brokerID, pub)
			})
		},
	}
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()
	conn, err := (&tls.Dialer{NetDialer: &net.Dialer{Timeout: 2 * time.Second}, Config: cfg}).DialContext(ctx, "tcp", addr)
	if err != nil {
		return nil, err
	}
	client := pahomqtt.NewClient(pahomqtt.ClientConfig{
		ClientID: self.DeviceID,
		Conn:     pahopackets.NewThreadSafeConn(conn),
		OnPublishReceived: []func(pahomqtt.PublishReceived) (bool, error){
			meshClientPublishHandler(home, onPub),
		},
		OnClientError: func(error) {
			clearMeshClient(circle.ID)
			scheduleRecover(home, circle.ID)
		},
		PacketTimeout: 4 * time.Second,
	})
	connectCtx, connectCancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer connectCancel()
	if _, err := client.Connect(connectCtx, &pahomqtt.Connect{
		ClientID:   self.DeviceID,
		KeepAlive:  30,
		CleanStart: true,
	}); err != nil {
		_ = conn.Close()
		return nil, err
	}
	subCtx, subCancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer subCancel()
	if _, err := client.Subscribe(subCtx, &pahomqtt.Subscribe{
		Subscriptions: []pahomqtt.SubscribeOptions{
			{Topic: meshPrefix(circle.ID) + "presence/+", QoS: 0},
			{Topic: meshPrefix(circle.ID) + "chat/direct/+", QoS: 1},
			{Topic: meshPrefix(circle.ID) + "chat/group/+", QoS: 1},
			{Topic: meshPrefix(circle.ID) + "chat/project/+", QoS: 1},
			{Topic: meshPrefix(circle.ID) + "call/+", QoS: 1},
			{Topic: meshPrefix(circle.ID) + "terminal/invite/+", QoS: 1},
			{Topic: meshPrefix(circle.ID) + "terminal/request/+", QoS: 1},
			{Topic: meshPrefix(circle.ID) + "terminal/permission/+", QoS: 1},
			{Topic: meshPrefix(circle.ID) + "terminal/status/+", QoS: 1},
			{Topic: meshPrefix(circle.ID) + "terminal/control/+", QoS: 1},
			{Topic: meshPrefix(circle.ID) + "terminal/signal/+", QoS: 1},
		},
	}); err != nil {
		_ = client.Disconnect(&pahomqtt.Disconnect{})
		return nil, err
	}
	return client, nil
}

func connectMeshClient(home string, circle Circle, self DeviceIdentity, priv ed25519.PrivateKey, brokerID, addr string) error {
	client, err := dialMeshClient(home, circle, self, priv, brokerID, addr, nil)
	if err != nil {
		return err
	}
	meshRT.mu.Lock()
	meshRT.initLocked()
	link := meshRT.links[circle.ID]
	if link == nil {
		link = &circleLink{}
		meshRT.links[circle.ID] = link
	}
	old := link.client
	link.client = client
	link.hostID = brokerID
	link.addr = addr
	meshRT.mu.Unlock()
	if old != nil && old != client {
		closePaho(old)
	}
	return publishPresence(home, circle, self)
}

func publishPresence(home string, circle Circle, self DeviceIdentity) error {
	client := meshClient(circle.ID)
	if client == nil {
		return errMeshClientDown
	}
	profile, err := loadOrCreateLocalUser(home)
	if err != nil {
		return err
	}
	body, err := json.Marshal(map[string]any{
		"deviceId":        self.DeviceID,
		"userId":          profile.UserID,
		"displayName":     profile.DisplayName,
		"online":          true,
		"liveSessions":    true,
		"protocolVersion": terminalProtocolVersion,
		"transports":      []string{"mtls-tcp"},
	})
	if err != nil {
		return err
	}
	expiry := presenceExpirySeconds
	return publishMQTT(client, presenceTopic(circle.ID, self.DeviceID), 0, body, true, &expiry)
}

func ensureLocalBroker(home string, circle Circle, self DeviceIdentity, priv ed25519.PrivateKey) (string, error) {
	meshRT.mu.Lock()
	meshRT.initLocked()
	if link := meshRT.links[circle.ID]; link != nil && link.broker != nil && link.hostID == self.DeviceID && link.addr != "" {
		addr := link.addr
		meshRT.mu.Unlock()
		return addr, nil
	}
	meshRT.mu.Unlock()
	if !chatTransportOpen() {
		return "", errMeshSuspended
	}
	broker, err := listenMemberBroker(home, circle, self, priv)
	if err != nil {
		return "", err
	}
	meshRT.mu.Lock()
	meshRT.initLocked()
	if meshRT.closing || !meshAllowsChat() {
		meshRT.mu.Unlock()
		broker.close()
		return "", errMeshSuspended
	}
	link := meshRT.links[circle.ID]
	if link == nil {
		link = &circleLink{}
		meshRT.links[circle.ID] = link
	}
	old := link.broker
	link.broker = broker
	link.addr = broker.addr
	if link.hostID == "" {
		link.hostID = self.DeviceID
	}
	meshRT.mqttPort = broker.port
	meshRT.mu.Unlock()
	if old != nil && old != broker {
		old.close()
	}
	setMeshEndpoint(self.DeviceID, broker.addr)
	return broker.addr, nil
}

func flushCircleOutbox(home, circleID string) error {
	return flushOutbox(home, circleID, func(topic string, qos byte, body []byte) error {
		return meshTrySend(circleID, topic, qos, body)
	})
}

func meshBrokerAddr(home string, circle Circle, self DeviceIdentity, priv ed25519.PrivateKey, host string) (string, error) {
	if host == self.DeviceID {
		return ensureLocalBroker(home, circle, self, priv)
	}
	addr, found := meshEndpoint(host)
	if !found {
		addr, err := brokerAddrFromPeerCatalog(home, circle, self, priv, host)
		if err != nil {
			return "", errBrokerUnknown
		}
		return addr, nil
	}
	return addr, nil
}

func brokerAddrFromPeerCatalog(home string, circle Circle, self DeviceIdentity, priv ed25519.PrivateKey, host string) (string, error) {
	member, ok := circle.activeMember(host)
	if !ok {
		return "", errBrokerUnknown
	}
	endpoint := strings.TrimSpace(member.Endpoint)
	if endpoint == "" {
		return "", errBrokerUnknown
	}
	resp, err := fetchPeerCatalog(home, endpoint, circle, self, priv)
	if err != nil || resp.MeshMQTTPort <= 0 {
		return "", errBrokerUnknown
	}
	hostIP, _, err := net.SplitHostPort(endpoint)
	if err != nil {
		return "", errBrokerUnknown
	}
	addr := net.JoinHostPort(hostIP, strconv.Itoa(resp.MeshMQTTPort))
	setMeshEndpoint(host, addr)
	return addr, nil
}

func recoverCircleMeshOnce(home string, circle Circle, self DeviceIdentity, priv ed25519.PrivateKey, host string) (string, error) {
	if alreadyLinked(circle.ID, host) {
		return host, flushCircleOutbox(home, circle.ID)
	}
	addr, err := meshBrokerAddr(home, circle, self, priv, host)
	if err != nil {
		return host, err
	}
	if err := connectMeshClient(home, circle, self, priv, host, addr); err != nil {
		return "", err
	}
	if err := flushCircleOutbox(home, circle.ID); err != nil {
		return host, err
	}
	return host, nil
}

func recoverCircleMesh(home string, circle Circle, self DeviceIdentity, priv ed25519.PrivateKey) (string, error) {
	if !meshAllowsChat() {
		return "", nil
	}
	limit := len(circle.Members) + 1
	if limit < 1 {
		limit = 1
	}
	for attempt := 0; attempt < limit; attempt++ {
		if !meshAllowsChat() {
			return "", nil
		}
		host, ok := electBroker(circle, brokerGoneSet(circle.ID))
		if !ok {
			return "", errors.New("no circle broker")
		}
		linkedHost, err := recoverCircleMeshOnce(home, circle, self, priv, host)
		if err == nil {
			return linkedHost, nil
		}
		if host == self.DeviceID {
			return "", err
		}
		markBrokerGone(circle.ID, host)
	}
	return "", errors.New("no reachable broker")
}

func scheduleRecover(home, circleID string) {
	meshRT.mu.Lock()
	meshRT.initLocked()
	if meshRT.closing || !meshRT.armed || !meshAllowsChat() || meshRT.recovering[circleID] {
		meshRT.mu.Unlock()
		return
	}
	meshRT.recovering[circleID] = true
	meshRT.mu.Unlock()
	go func() {
		defer func() {
			meshRT.mu.Lock()
			delete(meshRT.recovering, circleID)
			meshRT.mu.Unlock()
		}()
		id, priv, err := loadOrCreateIdentity(home)
		if err != nil {
			return
		}
		circle, err := loadCircle(home, circleID)
		if err != nil {
			return
		}
		_, _ = recoverCircleMesh(home, circle, id, priv)
	}()
}

func registerDiscovery(epoch uint64, srv *mdns.Server, cancel context.CancelFunc) bool {
	meshRT.mu.Lock()
	defer meshRT.mu.Unlock()
	meshRT.initLocked()
	if meshRT.epoch != epoch || meshRT.closing || !meshRT.armed || meshRT.discovery != nil {
		return false
	}
	meshRT.discovery = srv
	meshRT.discoveryCancel = cancel
	return true
}

func startMeshServices(home string) {
	meshRT.mu.Lock()
	meshRT.initLocked()
	meshRT.armed = true
	meshRT.home = home
	meshRT.epoch++
	epoch := meshRT.epoch
	meshRT.mu.Unlock()
	if !meshTransportsOpen() {
		return
	}
	go func() {
		if err := startCircleLinks(home, epoch); err != nil && !errors.Is(err, errBrokerUnknown) && meshEpochOpen(epoch) {
			fmt.Fprintf(os.Stderr, "mesh: %v\n", err)
		}
		if err := startDiscovery(home, epoch); err != nil && meshEpochOpen(epoch) {
			fmt.Fprintf(os.Stderr, "discovery: %v\n", err)
		}
	}()
}

func startCircleLinks(home string, epoch uint64) error {
	if !meshEpochOpen(epoch) || !meshAllowsChat() {
		return nil
	}
	circles, err := listCircles(home)
	if err != nil {
		return err
	}
	id, priv, err := loadOrCreateIdentity(home)
	if err != nil {
		return err
	}
	var first error
	for _, circle := range circles {
		if !meshEpochOpen(epoch) || !meshAllowsChat() {
			return first
		}
		if _, ok := circle.activeMember(id.DeviceID); !ok {
			continue
		}
		if _, err := recoverCircleMesh(home, circle, id, priv); err != nil && first == nil {
			first = err
		}
	}
	return first
}

func closePaho(client *pahomqtt.Client) {
	if client == nil {
		return
	}
	_ = client.Disconnect(&pahomqtt.Disconnect{})
}

func closeLink(link *circleLink) {
	if link == nil {
		return
	}
	closePaho(link.client)
	link.client = nil
	if link.broker != nil {
		link.broker.close()
		link.broker = nil
	}
}

func suspendMeshTransports() {
	meshRT.mu.Lock()
	meshRT.initLocked()
	meshRT.epoch++
	meshRT.closing = true
	disc := meshRT.discovery
	cancel := meshRT.discoveryCancel
	meshRT.discovery = nil
	meshRT.discoveryCancel = nil
	links := meshRT.links
	meshRT.links = map[string]*circleLink{}
	meshRT.mqttPort = 0
	meshRT.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	if disc != nil {
		_ = disc.Shutdown()
	}
	for _, link := range links {
		closeLink(link)
	}
	meshRT.mu.Lock()
	meshRT.closing = false
	meshRT.mu.Unlock()
}

func stopMeshServices() {
	meshRT.mu.Lock()
	meshRT.initLocked()
	meshRT.armed = false
	meshRT.mu.Unlock()
	suspendMeshTransports()
	meshRT.mu.Lock()
	meshRT.endpoints = map[string]string{}
	meshRT.gone = map[string]map[string]bool{}
	meshRT.home = ""
	meshRT.mqttPort = 0
	meshRT.mu.Unlock()
	closeChatDBs()
	setMeshDeliveryHook(nil)
}
