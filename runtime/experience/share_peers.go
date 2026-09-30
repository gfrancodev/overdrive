package main

import (
	"net"
	"os"
	"strconv"
	"strings"
)

func defaultIfaceAddrs(iface net.Interface) ([]net.Addr, error) { return iface.Addrs() }

var (
	hookNetInterfaces = net.Interfaces
	hookIfaceAddrs    = defaultIfaceAddrs
	hookHostname      = os.Hostname
)

func advertisedListenAddr() string {
	raw := strings.TrimSpace(os.Getenv("OVERDRIVE_SHARE_LISTEN"))
	if raw == "" {
		return ""
	}
	return publicEndpoint(normalizeListen(raw))
}

func normalizeListen(raw string) string {
	raw = strings.TrimSpace(raw)
	if strings.HasPrefix(raw, ":") {
		return "0.0.0.0" + raw
	}
	return raw
}

func publicEndpoint(listenAddr string) string {
	host, port, err := net.SplitHostPort(listenAddr)
	if err != nil {
		return listenAddr
	}
	if host == "" || host == "0.0.0.0" || host == "::" || host == "[::]" {
		if ip := firstLANIPv4(); ip != "" {
			return net.JoinHostPort(ip, port)
		}
		return net.JoinHostPort("0.0.0.0", port)
	}
	return net.JoinHostPort(host, port)
}

func (sl *shareListener) selfEndpoint() string {
	if sl != nil && sl.ln != nil {
		return publicEndpoint(sl.ln.Addr().String())
	}
	return advertisedListenAddr()
}

func firstLANIPv4() string {
	ifaces, err := hookNetInterfaces()
	if err != nil {
		return ""
	}
	for _, iface := range ifaces {
		if iface.Flags&net.FlagUp == 0 || iface.Flags&net.FlagLoopback != 0 {
			continue
		}
		name := strings.ToLower(iface.Name)
		if strings.HasPrefix(name, "docker") || strings.HasPrefix(name, "br-") || strings.HasPrefix(name, "veth") {
			continue
		}
		addrs, err := hookIfaceAddrs(iface)
		if err != nil {
			continue
		}
		for _, addr := range addrs {
			ipNet, ok := addr.(*net.IPNet)
			if !ok || ipNet.IP == nil || ipNet.IP.IsLoopback() {
				continue
			}
			ip4 := ipNet.IP.To4()
			if ip4 == nil || ip4.IsLinkLocalUnicast() {
				continue
			}
			return ip4.String()
		}
	}
	return ""
}

func isLoopbackHost(host string) bool {
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func chooseEndpoint(advertised, remote string) string {
	advertised = strings.TrimSpace(advertised)
	if advertised == "" {
		remHost, _, remErr := net.SplitHostPort(remote)
		if remErr == nil && remHost != "" && !isLoopbackHost(remHost) {
			return net.JoinHostPort(remHost, strconv.Itoa(defaultSharePort))
		}
		return ""
	}
	advHost, advPort, advErr := net.SplitHostPort(advertised)
	remHost, _, remErr := net.SplitHostPort(remote)
	if advErr == nil && advHost != "" && advHost != "0.0.0.0" && advHost != "::" {
		if isLoopbackHost(advHost) && remErr == nil && remHost != "" && !isLoopbackHost(remHost) {
			return net.JoinHostPort(remHost, advPort)
		}
		return net.JoinHostPort(advHost, advPort)
	}
	if remErr == nil && remHost != "" {
		port := strconv.Itoa(defaultSharePort)
		if advErr == nil && advPort != "" {
			port = advPort
		}
		return net.JoinHostPort(remHost, port)
	}
	return ""
}

func rememberJoin(circle Circle, req JoinRequest, remote net.Addr) Circle {
	remoteStr := ""
	if remote != nil {
		remoteStr = remote.String()
	}
	return upsertMember(circle, CircleMember{
		DeviceID:   req.DeviceID,
		PublicKey:  req.PublicKey,
		AddedAt:    nowRFC3339(),
		Endpoint:   chooseEndpoint(req.ListenAddr, remoteStr),
		RemoteAddr: remoteStr,
		Hostname:   req.Hostname,
		LastSeen:   nowRFC3339(),
	})
}

func rememberSyncPeer(circle Circle, req SyncRequest, remote net.Addr) Circle {
	if _, ok := circle.activeMember(req.DeviceID); !ok {
		return circle
	}
	remoteStr := ""
	if remote != nil {
		remoteStr = remote.String()
	}
	return upsertMember(circle, CircleMember{
		DeviceID:   req.DeviceID,
		Endpoint:   chooseEndpoint(req.ListenAddr, remoteStr),
		RemoteAddr: remoteStr,
		Hostname:   req.Hostname,
		LastSeen:   nowRFC3339(),
	})
}

func upsertMember(circle Circle, member CircleMember) Circle {
	if member.DeviceID == "" {
		return circle
	}
	for i, existing := range circle.Members {
		if existing.DeviceID != member.DeviceID {
			continue
		}
		circle.Members[i] = mergeMember(existing, member)
		return circle
	}
	if member.AddedAt == "" {
		member.AddedAt = nowRFC3339()
	}
	circle.Members = append(circle.Members, member)
	return circle
}

func mergeMember(local, incoming CircleMember) CircleMember {
	if incoming.PublicKey != "" {
		local.PublicKey = incoming.PublicKey
	}
	if incoming.Endpoint != "" {
		local.Endpoint = incoming.Endpoint
	}
	if incoming.RemoteAddr != "" {
		local.RemoteAddr = incoming.RemoteAddr
	}
	if incoming.Hostname != "" {
		local.Hostname = incoming.Hostname
	}
	if incoming.LastSeen != "" {
		local.LastSeen = incoming.LastSeen
	}
	if incoming.Revoked {
		local.Revoked = true
	}
	if local.AddedAt == "" {
		local.AddedAt = incoming.AddedAt
	}
	return local
}

func stampSelfMember(circle Circle, deviceID, endpoint string) Circle {
	if deviceID == "" {
		return circle
	}
	for i, member := range circle.Members {
		if member.DeviceID != deviceID {
			continue
		}
		if endpoint != "" {
			circle.Members[i].Endpoint = endpoint
		}
		if circle.Members[i].Hostname == "" {
			circle.Members[i].Hostname = computerName()
		}
		circle.Members[i].LastSeen = nowRFC3339()
		return circle
	}
	return circle
}

func stampMemberEndpoint(circle Circle, deviceID, endpoint, remote string) Circle {
	return upsertMember(circle, CircleMember{
		DeviceID:   deviceID,
		Endpoint:   endpoint,
		RemoteAddr: remote,
		LastSeen:   nowRFC3339(),
	})
}

func stampMemberHostname(circle Circle, deviceID, hostname string) Circle {
	if hostname == "" {
		return circle
	}
	return upsertMember(circle, CircleMember{DeviceID: deviceID, Hostname: hostname})
}

func refreshPeerEndpoints(circle Circle, selfID string) Circle {
	seen := map[string]bool{}
	out := []string{}
	selfEndpoint := ""
	for _, member := range circle.Members {
		if member.DeviceID == selfID {
			selfEndpoint = member.Endpoint
		}
	}
	add := func(endpoint string) {
		endpoint = strings.TrimSpace(endpoint)
		if endpoint == "" || endpoint == selfEndpoint || seen[endpoint] {
			return
		}
		seen[endpoint] = true
		out = append(out, endpoint)
	}
	for _, member := range circle.Members {
		if member.Revoked || member.DeviceID == selfID {
			continue
		}
		add(member.Endpoint)
	}
	for _, endpoint := range circle.PeerEndpoints {
		add(endpoint)
	}
	circle.PeerEndpoints = out
	return circle
}

func mergePeerRoster(home, circleID string, incoming []CircleMember) error {
	circle, err := loadCircle(home, circleID)
	if err != nil {
		return err
	}
	id, _, err := loadOrCreateIdentity(home)
	if err != nil {
		return err
	}
	for _, member := range incoming {
		if member.DeviceID == "" || member.DeviceID == id.DeviceID {
			continue
		}
		circle = upsertMember(circle, member)
	}
	circle = refreshPeerEndpoints(circle, id.DeviceID)
	return persistJoinedCircle(home, circle)
}

func writeJoinAck(conn net.Conn, circle Circle) error {
	payload, err := hookJSONMarshal(joinAck{CircleID: circle.ID, Members: circle.Members})
	if err != nil {
		return err
	}
	cipher, err := encryptBatch(circle.GroupKey, payload)
	if err != nil {
		return err
	}
	out, err := hookJSONMarshal(wireEnvelope{CircleID: circle.ID, Ciphertext: cipher, Kind: "join-ack"})
	if err != nil {
		return err
	}
	_, err = conn.Write(append(out, '\n'))
	return err
}

func localHostname() string {
	return computerName()
}

func computerName() string {
	if raw, err := hookReadFile("/etc/machine-info"); err == nil {
		for _, line := range strings.Split(string(raw), "\n") {
			line = strings.TrimSpace(line)
			if !strings.HasPrefix(line, "PRETTY_HOSTNAME=") {
				continue
			}
			name := strings.Trim(strings.TrimPrefix(line, "PRETTY_HOSTNAME="), `"'`)
			if name != "" {
				return name
			}
		}
	}
	host, err := hookHostname()
	if err != nil {
		return ""
	}
	return host
}

func peerDisplayName(member CircleMember, selfID string) string {
	if strings.TrimSpace(member.Hostname) != "" {
		return member.Hostname
	}
	if selfID != "" && member.DeviceID == selfID {
		if name := computerName(); name != "" {
			return name
		}
	}
	if name := hostLabelFromEndpoint(member.Endpoint); name != "" {
		return name
	}
	return "computer"
}

func hostLabelFromEndpoint(endpoint string) string {
	host, _, err := net.SplitHostPort(strings.TrimSpace(endpoint))
	if err != nil {
		host = strings.TrimSpace(endpoint)
	}
	if host == "" || isLoopbackHost(host) || net.ParseIP(host) != nil {
		return ""
	}
	return host
}
