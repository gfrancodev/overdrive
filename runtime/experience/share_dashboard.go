package main

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
)

//go:embed share_dashboard.html
var dashboardHTML []byte

//go:embed assets/logo.png
var dashboardLogo []byte

type peerDashboardCircle struct {
	ID            string      `json:"id"`
	Name          string      `json:"name"`
	PeerEndpoints []string    `json:"peer_endpoints"`
	Members       []SharePeer `json:"members"`
	Folders       []string    `json:"folders"`
}

type peerDashboard struct {
	DeviceID string                `json:"device_id,omitempty"`
	Hostname string                `json:"hostname,omitempty"`
	Listen   string                `json:"listen,omitempty"`
	UI       string                `json:"ui,omitempty"`
	Circles  []peerDashboardCircle `json:"circles"`
}

func shareUIAddr() (string, bool) {
	if v := strings.TrimSpace(os.Getenv("OVERDRIVE_SHARE_UI")); v != "" {
		if v == "off" || v == "0" {
			return "", false
		}
		return v, true
	}
	raw := strings.TrimSpace(os.Getenv("OVERDRIVE_SHARE_LISTEN"))
	if raw == "" {
		return "", false
	}
	host, port, err := net.SplitHostPort(normalizeListen(raw))
	if err != nil {
		return "", false
	}
	if host != "0.0.0.0" && host != "::" && host != "" {
		return "", false
	}
	p, err := strconv.Atoi(port)
	if err != nil || p <= 0 {
		return "", false
	}
	return net.JoinHostPort(host, strconv.Itoa(p+1)), true
}

func peerDashboardSnapshot(home string) peerDashboard {
	out := peerDashboard{
		Listen:   advertisedListenAddr(),
		Hostname: localHostname(),
		Circles:  []peerDashboardCircle{},
	}
	if ui, ok := shareUIAddr(); ok {
		out.UI = ui
	}
	if id, _, err := loadOrCreateIdentity(home); err == nil {
		out.DeviceID = id.DeviceID
	}
	circles, err := listCircles(home)
	if err != nil {
		return out
	}
	for _, circle := range circles {
		out.Circles = append(out.Circles, peerDashboardCircle{
			ID:            circle.ID,
			Name:          circle.Name,
			PeerEndpoints: circle.PeerEndpoints,
			Members:       sharePeersFromCircle(circle, out.DeviceID),
			Folders:       append([]string{}, circle.AllowedFolders...),
		})
	}
	return out
}

func serveDashboardLogo(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	w.Header().Set("Content-Type", "image/png")
	w.Header().Set("Cache-Control", "public, max-age=86400")
	_, _ = w.Write(dashboardLogo)
}

func shareDashboardHandler(home string) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/logo.png", serveDashboardLogo)
	mux.HandleFunc("/favicon.ico", serveDashboardLogo)
	mux.HandleFunc("/apple-touch-icon.png", serveDashboardLogo)
	mux.HandleFunc("/api/peers", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		_ = json.NewEncoder(w).Encode(peerDashboardSnapshot(home))
	})
	mux.HandleFunc("/api/contexts", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		_ = json.NewEncoder(w).Encode(peerDashboardContexts(home, strings.TrimSpace(r.URL.Query().Get("device"))))
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		_, _ = w.Write(dashboardHTML)
	})
	return mux
}

var shareDashboardOnce sync.Once

func startShareDashboard(home string) {
	shareDashboardOnce.Do(func() {
		addr, ok := shareUIAddr()
		if !ok {
			return
		}
		srv := &http.Server{
			Addr:              addr,
			Handler:           shareDashboardHandler(home),
			ReadHeaderTimeout: 5 * time.Second,
		}
		go func() { _ = srv.ListenAndServe() }()
		fmt.Fprintf(os.Stderr, "share ui: http://%s\n", addr)
	})
}
