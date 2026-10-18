package main

import (
	"errors"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

// Connection health says, per capability, whether this device reaches the circle and, when it does not, why
// and what fixes it. It is derived from state the Core already keeps (mesh switch, network choice, chat links)
// plus a TCP connect to each member's listener; nothing new travels between devices.

const (
	healthConnected    = "connected"
	healthDegraded     = "degraded"
	healthReconnecting = "reconnecting"
	healthOffline      = "offline"
	healthBlocked      = "blocked"
)

// healthSeverity orders states for the overall one: what needs the person's action comes first.
var healthSeverity = map[string]int{
	healthConnected: 0, healthDegraded: 1, healthReconnecting: 2, healthOffline: 3, healthBlocked: 4,
}

const (
	reachOK          = "ok"
	reachRefused     = "refused"
	reachTimeout     = "timeout"
	reachUnreachable = "unreachable"
	reachNoAddress   = "no_address"
	reachSelfAddress = "self_address"
	reachUnknown     = "unknown"
)

const (
	peerProbeTTL     = 10 * time.Second
	peerProbeTimeout = 700 * time.Millisecond
)

type capabilityHealth struct {
	ID     string `json:"id"`
	State  string `json:"state"`
	Cause  string `json:"cause,omitempty"`
	Detail string `json:"detail"`
	Fix    string `json:"fix,omitempty"`
	// Action is what the dashboard offers next to the fix: resume_mesh, allow_network, share_folder, open_setup.
	Action string `json:"action,omitempty"`
}

type peerHealth struct {
	DeviceID  string `json:"device_id"`
	Name      string `json:"name"`
	CircleID  string `json:"circle_id"`
	Endpoint  string `json:"endpoint,omitempty"`
	Reach     string `json:"reach"`
	RTTMillis int64  `json:"rtt_ms,omitempty"`
	LocalOnly bool   `json:"local_only,omitempty"`
	LastSeen  string `json:"last_seen,omitempty"`
}

type connectionHealth struct {
	State        string             `json:"state"`
	CheckedAt    string             `json:"checked_at"`
	Mesh         string             `json:"mesh"`
	Network      networkView        `json:"network"`
	Capabilities []capabilityHealth `json:"capabilities"`
	Peers        []peerHealth       `json:"peers"`
}

type peerProbe struct {
	Reach string
	RTT   time.Duration
	At    time.Time
}

// healthInputs is everything health is derived from, gathered once per request.
type healthInputs struct {
	SelfID  string
	Paused  bool
	Circles []Circle
	Network networkView
	Probes  map[string]peerProbe
	Chat    map[string]chatLinkState
	TURN    bool
	Nearby  map[string]discoveredPeer
}

func memberLabel(m CircleMember) string {
	if name := strings.TrimSpace(m.Hostname); name != "" {
		return name
	}
	return m.DeviceID
}

func healthNameList(names []string) string {
	if len(names) > 3 {
		names = append(append([]string{}, names[:3]...), strconv.Itoa(len(names)-3)+" more")
	}
	if len(names) <= 1 {
		return strings.Join(names, "")
	}
	return strings.Join(names[:len(names)-1], ", ") + " and " + names[len(names)-1]
}

func countOtherDevices(n int) string {
	if n == 1 {
		return "1 other device"
	}
	return strconv.Itoa(n) + " other devices"
}

func countDevices(n int) string {
	if n == 1 {
		return "1 device"
	}
	return strconv.Itoa(n) + " devices"
}

func endpointHost(endpoint string) string {
	host, _, err := net.SplitHostPort(endpoint)
	if err != nil {
		return ""
	}
	return host
}

// isOwnAddress is an endpoint that leads back to this device: a peer that registered a loopback address on
// another computer gets this device's own listener.
func isOwnAddress(endpoint string, network networkView) bool {
	host, port, err := net.SplitHostPort(endpoint)
	if err != nil {
		return false
	}
	_, ownPort, err := net.SplitHostPort(normalizeListen(network.Listen))
	if err != nil || port != ownPort {
		return false
	}
	return isLoopbackHost(host) || (network.LANIP != "" && host == network.LANIP)
}

func (in healthInputs) peers() []peerHealth {
	seen := map[string]bool{in.SelfID: true}
	out := []peerHealth{}
	for _, c := range in.Circles {
		for _, m := range c.Members {
			if m.Revoked || m.DeviceID == "" || seen[m.DeviceID] {
				continue
			}
			seen[m.DeviceID] = true
			p := peerHealth{DeviceID: m.DeviceID, Name: memberLabel(m), CircleID: c.ID, Endpoint: m.Endpoint,
				Reach: reachUnknown, LastSeen: m.LastSeen, LocalOnly: in.Nearby[m.DeviceID].LocalOnly}
			switch {
			case strings.TrimSpace(m.Endpoint) == "":
				p.Reach = reachNoAddress
			case isOwnAddress(m.Endpoint, in.Network):
				p.Reach = reachSelfAddress
			default:
				if probe, ok := in.Probes[m.Endpoint]; ok && probe.Reach != "" {
					p.Reach = probe.Reach
					p.RTTMillis = probe.RTT.Milliseconds()
				}
			}
			out = append(out, p)
		}
	}
	return out
}

// peerFix is what to do about one peer that does not answer.
func peerFix(p peerHealth) string {
	switch p.Reach {
	case reachRefused:
		if p.LocalOnly {
			return p.Name + " accepts connections only from itself. On " + p.Name + ", open Connection and allow devices on this network."
		}
		return "Overdrive on " + p.Name + " isn't accepting connections. Check that it's running and allows devices on this network."
	case reachTimeout:
		port := "7741"
		if _, pt, err := net.SplitHostPort(p.Endpoint); err == nil {
			port = pt
		}
		return "Check that " + p.Name + " is on and on this network, and that no firewall blocks port " + port + "."
	case reachUnreachable:
		return "This device has no route to " + p.Name + ". Check the Wi-Fi or cable, or the VPN."
	case reachNoAddress:
		return p.Name + " hasn't shared its address yet. It does the next time it connects to the circle."
	case reachSelfAddress:
		return p.Name + " registered an address that works only on its own computer. On " + p.Name + ", allow devices on this network."
	default:
		return ""
	}
}

func uniform(state, cause, fix, action string, details map[string]string) []capabilityHealth {
	out := []capabilityHealth{}
	for _, id := range []string{"chat", "calls", "memory", "terminal"} {
		out = append(out, capabilityHealth{ID: id, State: state, Cause: cause, Detail: details[id], Fix: fix, Action: action})
	}
	return out
}

func deriveConnectionHealth(in healthInputs) connectionHealth {
	out := connectionHealth{CheckedAt: nowRFC3339(), Mesh: meshRunning, Network: in.Network, Peers: in.peers()}
	if in.Paused {
		out.Mesh = meshPaused
	}
	out.Capabilities = deriveCapabilities(in, out.Peers)
	out.State = healthConnected
	for _, c := range out.Capabilities {
		if healthSeverity[c.State] > healthSeverity[out.State] {
			out.State = c.State
		}
	}
	return out
}

func deriveCapabilities(in healthInputs, peers []peerHealth) []capabilityHealth {
	switch {
	case in.Paused:
		return uniform(healthOffline, "mesh_paused", "Resume the mesh.", "resume_mesh", map[string]string{
			"chat":     "The mesh is paused on this device, so chat is off.",
			"calls":    "The mesh is paused on this device, so calls are off.",
			"memory":   "The mesh is paused, so experience stays on this device.",
			"terminal": "The mesh is paused. Terminals on this device still work; shared ones don't.",
		})
	case len(in.Circles) == 0:
		return uniform(healthOffline, "no_circle", "Create a circle or pair with a device.", "open_setup", map[string]string{
			"chat": "This device is not in a circle yet.", "calls": "This device is not in a circle yet.",
			"memory": "This device is not in a circle yet.", "terminal": "This device is not in a circle yet.",
		})
	case len(peers) == 0:
		alone := "No other device is in the circle yet."
		return uniform(healthOffline, "no_peers", "Pair another device.", "open_setup", map[string]string{
			"chat": alone, "calls": alone, "memory": alone, "terminal": alone,
		})
	}
	chat := chatHealth(in)
	return []capabilityHealth{chat, callsHealth(in, chat), memoryHealth(in, peers), terminalHealth(in, peers)}
}

// remotePeers is whether some member lives on another computer: then this device must accept network
// connections. Cores on one computer reach each other over loopback.
func remotePeers(peers []peerHealth) bool {
	for _, p := range peers {
		if host := endpointHost(p.Endpoint); host != "" && !isLoopbackHost(host) {
			return true
		}
	}
	return false
}

func localOnlyHealth(id, detail string) capabilityHealth {
	return capabilityHealth{ID: id, State: healthBlocked, Cause: "local_only", Detail: detail,
		Fix: "Allow devices on this network to connect.", Action: "allow_network"}
}

// reachHealth grades a capability carried by the members' listeners: all answer, some, or none.
func reachHealth(id string, peers []peerHealth, allOK func(n int) string, someOK func(names string, one bool) string, noneOK string) capabilityHealth {
	var ok int
	var silent []string
	fix := ""
	for _, p := range peers {
		if p.Reach == reachOK {
			ok++
			continue
		}
		silent = append(silent, p.Name)
		if fix == "" {
			fix = peerFix(p)
		}
	}
	switch {
	case len(silent) == 0:
		return capabilityHealth{ID: id, State: healthConnected, Detail: allOK(ok)}
	case ok > 0:
		return capabilityHealth{ID: id, State: healthDegraded, Cause: "peers_unreachable", Detail: someOK(healthNameList(silent), len(silent) == 1), Fix: fix}
	default:
		return capabilityHealth{ID: id, State: healthOffline, Cause: "peers_unreachable", Detail: noneOK, Fix: fix}
	}
}

func answerVerb(one bool) string {
	if one {
		return " doesn't answer"
	}
	return " don't answer"
}

func memoryHealth(in healthInputs, peers []peerHealth) capabilityHealth {
	shared := false
	for _, c := range in.Circles {
		shared = shared || len(c.AllowedFolders) > 0
	}
	if !shared {
		return capabilityHealth{ID: "memory", State: healthBlocked, Cause: "no_folder",
			Detail: "No folder is shared, so no experience leaves or reaches this device.",
			Fix:    "Share a project folder with the circle.", Action: "share_folder"}
	}
	if !in.Network.LANAccess && remotePeers(peers) {
		return localOnlyHealth("memory", "Other devices can't send experience to this one: it accepts sync connections only from this computer.")
	}
	return reachHealth("memory", peers,
		func(n int) string { return "Experience syncs with " + countDevices(n) + "." },
		func(names string, one bool) string {
			return names + answerVerb(one) + "; experience syncs with the others."
		},
		"No device in the circle answers, so experience stays on this device for now.")
}

func terminalHealth(in healthInputs, peers []peerHealth) capabilityHealth {
	if !in.Network.LANAccess && remotePeers(peers) {
		return localOnlyHealth("terminal", "Other devices can't open terminals shared from this one: it accepts terminal connections only from this computer.")
	}
	return reachHealth("terminal", peers,
		func(n int) string { return "Terminals can be shared with " + countDevices(n) + "." },
		func(names string, one bool) string {
			return names + answerVerb(one) + "; terminals from the others still open."
		},
		"No device in the circle answers. Terminals on this device still work.")
}

func circleMemberLabel(c Circle, deviceID string) string {
	for _, m := range c.Members {
		if m.DeviceID == deviceID {
			return memberLabel(m)
		}
	}
	return deviceID
}

const chatWaitFix = "Messages you send wait and go out once it's back."

// chatHealth is the worst chat link among the circles that have other members.
func chatHealth(in healthInputs) capabilityHealth {
	worst := capabilityHealth{ID: "chat", State: healthConnected}
	first := true
	for _, c := range in.Circles {
		if !circleHasPeers(c, in.SelfID) {
			continue
		}
		cur := circleChatHealth(c, in.Chat[c.ID], in.SelfID)
		if first || healthSeverity[cur.State] > healthSeverity[worst.State] {
			worst, first = cur, false
		}
	}
	return worst
}

func circleHasPeers(c Circle, selfID string) bool {
	for _, m := range c.Members {
		if !m.Revoked && m.DeviceID != "" && m.DeviceID != selfID {
			return true
		}
	}
	return false
}

func circleChatHealth(c Circle, st chatLinkState, selfID string) capabilityHealth {
	switch {
	case st.Connected && st.HostID == selfID && st.Hosted && st.Guests == 0:
		return capabilityHealth{ID: "chat", State: healthDegraded, Cause: "chat_alone",
			Detail: "This device hosts the circle's chat, but no other device is on it.",
			Fix:    "The others join it when they connect. Check the devices below that don't answer."}
	case st.Connected && st.HostID == selfID && st.Hosted:
		return capabilityHealth{ID: "chat", State: healthConnected, Detail: "This device hosts the circle's chat for " + countOtherDevices(st.Guests) + "."}
	case st.Connected && st.HostID == selfID:
		return capabilityHealth{ID: "chat", State: healthConnected, Detail: "This device hosts the circle's chat."}
	case st.Connected:
		return capabilityHealth{ID: "chat", State: healthConnected, Detail: "Chat runs through " + circleMemberLabel(c, st.HostID) + "."}
	case st.Recovering:
		return capabilityHealth{ID: "chat", State: healthReconnecting, Cause: "reconnecting", Detail: "Reconnecting to the circle's chat…", Fix: chatWaitFix}
	case st.LastError != "":
		host := st.HostID
		if host == "" || host == selfID {
			host = c.CreatorDeviceID
		}
		name := circleMemberLabel(c, host)
		return capabilityHealth{ID: "chat", State: healthOffline, Cause: "chat_unreachable",
			Detail: "Chat can't reach " + name + ", the device that hosts it.",
			Fix:    chatWaitFix + " Check that " + name + " is on and on this network."}
	default:
		return capabilityHealth{ID: "chat", State: healthReconnecting, Cause: "connecting", Detail: "Connecting to the circle's chat…", Fix: chatWaitFix}
	}
}

// callsHealth follows chat: calls are set up over it. Media goes straight between devices, which needs a TURN
// server once they are on different networks.
func callsHealth(in healthInputs, chat capabilityHealth) capabilityHealth {
	out := capabilityHealth{ID: "calls", State: chat.State, Cause: chat.Cause, Fix: chat.Fix}
	switch chat.State {
	case healthConnected:
		out.Detail = "Calls work between devices on this network. Calls across networks need a TURN server."
		if in.TURN {
			out.Detail = "Calls are ready, on this network and across networks."
		}
	case healthReconnecting:
		out.Detail = "Calls wait for the circle's chat to reconnect."
	default:
		if chat.Cause == "chat_alone" {
			out.Detail = "Calls start over the circle's chat, and no other device is on it yet."
			break
		}
		out.Detail = "Calls start over the circle's chat, which is out of reach."
	}
	return out
}

var peerProbes = struct {
	sync.Mutex
	m map[string]peerProbe
}{m: map[string]peerProbe{}}

func resetPeerProbes() {
	peerProbes.Lock()
	peerProbes.m = map[string]peerProbe{}
	peerProbes.Unlock()
}

// classifyDialError turns a failed TCP connect into what it says about the peer.
func classifyDialError(err error) string {
	var ne net.Error
	switch {
	case errors.Is(err, syscall.ECONNREFUSED) || strings.Contains(strings.ToLower(err.Error()), "refused"):
		return reachRefused
	case errors.As(err, &ne) && ne.Timeout():
		return reachTimeout
	default:
		return reachUnreachable
	}
}

// probePeerEndpoints connects to each endpoint, in parallel, unless a recent answer is cached. The connection
// closes at once: the listener reads nothing and drops it.
func probePeerEndpoints(endpoints []string, now time.Time) map[string]peerProbe {
	out := map[string]peerProbe{}
	var stale []string
	peerProbes.Lock()
	for _, ep := range endpoints {
		if p, ok := peerProbes.m[ep]; ok && now.Sub(p.At) < peerProbeTTL {
			out[ep] = p
			continue
		}
		stale = append(stale, ep)
	}
	peerProbes.Unlock()
	var wg sync.WaitGroup
	var mu sync.Mutex
	for _, ep := range stale {
		wg.Add(1)
		go func(ep string) {
			defer wg.Done()
			start := time.Now()
			p := peerProbe{Reach: reachOK, At: now}
			conn, err := hookNetDial("tcp", ep, peerProbeTimeout)
			if err != nil {
				p.Reach = classifyDialError(err)
			} else {
				p.RTT = time.Since(start)
				_ = conn.Close()
			}
			mu.Lock()
			out[ep] = p
			mu.Unlock()
		}(ep)
	}
	wg.Wait()
	peerProbes.Lock()
	for _, ep := range stale {
		peerProbes.m[ep] = out[ep]
	}
	peerProbes.Unlock()
	return out
}

func nearbyByID(home string) map[string]discoveredPeer {
	out := map[string]discoveredPeer{}
	peers, err := listDiscoveredPeers(home)
	if err != nil {
		return out
	}
	for _, p := range peers {
		out[p.DeviceID] = p
	}
	return out
}

func gatherHealthInputs(home string) (healthInputs, error) {
	id, _, err := loadOrCreateIdentity(home)
	if err != nil {
		return healthInputs{}, err
	}
	circles, err := memberCircles(home, id.DeviceID)
	if err != nil {
		return healthInputs{}, err
	}
	in := healthInputs{
		SelfID: id.DeviceID, Paused: !meshTransportsOpen(), Circles: circles, Network: currentNetworkView(home),
		Chat: chatLinkStates(), TURN: turnConfigured(), Nearby: nearbyByID(home),
	}
	if !in.Paused {
		var endpoints []string
		for _, p := range in.peers() {
			if p.Reach == reachUnknown {
				endpoints = append(endpoints, p.Endpoint)
			}
		}
		in.Probes = probePeerEndpoints(endpoints, time.Now())
	}
	return in, nil
}

func registerConnectionHealthRoutes(mux *http.ServeMux, home string) {
	mux.HandleFunc("/api/connections/health", func(w http.ResponseWriter, r *http.Request) {
		if rejectRemoteCoreAdmin(w, r) {
			return
		}
		if r.Method != http.MethodGet {
			http.Error(w, methodNotAllowedMsg, http.StatusMethodNotAllowed)
			return
		}
		in, err := gatherHealthInputs(home)
		if err != nil {
			writeInviteError(w, http.StatusInternalServerError, "could not read the connection state")
			return
		}
		writeInviteJSON(w, http.StatusOK, deriveConnectionHealth(in))
	})
}
