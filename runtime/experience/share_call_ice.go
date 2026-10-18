package main

import (
	"encoding/json"
	"os"
	"strings"
)

const (
	defaultSTUNURL = "stun:stun.l.google.com:19302"
	callPipNote    = "Screen mini player works best in Chrome or Edge (Picture-in-Picture). Safari and Firefox use the draggable overlay."
	callNatNote    = "Devices on different networks need a relay (TURN) server to call each other. Set OVERDRIVE_TURN_URLS on each device."
)

type rtcIceServer struct {
	URLs       any    `json:"urls"`
	Username   string `json:"username,omitempty"`
	Credential string `json:"credential,omitempty"`
}

func splitEnvList(raw string) []string {
	var out []string
	for _, part := range strings.Split(raw, ",") {
		part = strings.TrimSpace(part)
		if part != "" {
			out = append(out, part)
		}
	}
	return out
}

func turnConfigured() bool {
	return len(splitEnvList(os.Getenv("OVERDRIVE_TURN_URLS"))) > 0
}

func meshIceServers() []rtcIceServer {
	servers := []rtcIceServer{{URLs: defaultSTUNURL}}
	urls := splitEnvList(os.Getenv("OVERDRIVE_TURN_URLS"))
	if len(urls) == 0 {
		return servers
	}
	turn := rtcIceServer{URLs: urls[0]}
	if len(urls) > 1 {
		turn.URLs = urls
	}
	if user := strings.TrimSpace(os.Getenv("OVERDRIVE_TURN_USERNAME")); user != "" {
		turn.Username = user
		turn.Credential = os.Getenv("OVERDRIVE_TURN_CREDENTIAL")
	}
	return append(servers, turn)
}

func callRuntimeScript() string {
	payload, err := json.Marshal(map[string]any{
		"iceServers":     meshIceServers(),
		"turnConfigured": turnConfigured(),
		"pipNote":        callPipNote,
		"natNote":        callNatNote,
	})
	if err != nil {
		return ""
	}
	return "<script>window.OVERDRIVE_CALL=" + string(payload) + "</script>"
}
