package main

import (
	"net/http"
	"runtime"
	"time"
)

// The diagnostics report is what a person copies and sends to whoever helps them. It says how this device
// connects and what it sees, and leaves out what must not travel: keys, invite secrets, tokens, and folder
// paths or names, which reveal projects. Folders are counted, not listed.

type diagnosticsCircle struct {
	ID              string        `json:"id"`
	Name            string        `json:"name"`
	IsCreator       bool          `json:"is_creator"`
	KeyEpoch        int           `json:"key_epoch"`
	PreviousKeyOpen bool          `json:"previous_key_open"`
	Members         int           `json:"members"`
	ActiveMembers   int           `json:"active_members"`
	SharedFolders   int           `json:"shared_folders"`
	Chat            chatLinkState `json:"chat"`
}

type diagnosticsNearby struct {
	DeviceID  string `json:"device_id"`
	Name      string `json:"name"`
	OS        string `json:"os,omitempty"`
	Host      string `json:"host,omitempty"`
	Port      int    `json:"port,omitempty"`
	LocalOnly bool   `json:"local_only,omitempty"`
	LastSeen  string `json:"last_seen,omitempty"`
}

type diagnosticsReport struct {
	GeneratedAt    string              `json:"generated_at"`
	Version        string              `json:"version"`
	Platform       string              `json:"platform"`
	DeviceID       string              `json:"device_id"`
	Hostname       string              `json:"hostname"`
	Mesh           string              `json:"mesh"`
	Network        networkView         `json:"network"`
	Discovery      bool                `json:"discovery"`
	ChatPort       int                 `json:"chat_port,omitempty"`
	TURN           bool                `json:"turn"`
	LocalTerminals int                 `json:"local_terminals"`
	Circles        []diagnosticsCircle `json:"circles"`
	Nearby         []diagnosticsNearby `json:"nearby"`
	Health         connectionHealth    `json:"health"`
}

func localActiveTerminals(home string) int {
	mgr, err := getTerminalManager(home)
	if err != nil {
		return 0
	}
	active, _ := activeTerminalCount(mgr)
	return active
}

func buildDiagnostics(home string) (diagnosticsReport, error) {
	in, err := gatherHealthInputs(home)
	if err != nil {
		return diagnosticsReport{}, err
	}
	health := deriveConnectionHealth(in)
	report := diagnosticsReport{
		GeneratedAt: nowRFC3339(), Version: version, Platform: runtime.GOOS + "/" + runtime.GOARCH,
		DeviceID: in.SelfID, Hostname: computerName(), Mesh: health.Mesh, Network: in.Network,
		Discovery: discoveryAdvertising(), ChatPort: meshMQTTPort(), TURN: in.TURN,
		LocalTerminals: localActiveTerminals(home), Circles: []diagnosticsCircle{}, Nearby: []diagnosticsNearby{},
		Health: health,
	}
	now := time.Now()
	for _, c := range in.Circles {
		dc := diagnosticsCircle{
			ID: c.ID, Name: c.Name, IsCreator: c.CreatorDeviceID == in.SelfID, KeyEpoch: c.KeyEpoch,
			PreviousKeyOpen: len(c.openKeys(now)) > 1, Members: len(c.Members), SharedFolders: len(c.AllowedFolders),
			Chat: in.Chat[c.ID],
		}
		for _, m := range c.Members {
			if !m.Revoked {
				dc.ActiveMembers++
			}
		}
		report.Circles = append(report.Circles, dc)
	}
	for _, p := range in.Nearby {
		report.Nearby = append(report.Nearby, diagnosticsNearby{
			DeviceID: p.DeviceID, Name: p.FriendlyName, OS: p.OS, Host: p.Host, Port: p.Port,
			LocalOnly: p.LocalOnly, LastSeen: p.LastSeen,
		})
	}
	return report, nil
}

func registerDiagnosticsRoutes(mux *http.ServeMux, home string) {
	mux.HandleFunc("/api/diagnostics", func(w http.ResponseWriter, r *http.Request) {
		if rejectRemoteCoreAdmin(w, r) {
			return
		}
		if r.Method != http.MethodGet {
			http.Error(w, methodNotAllowedMsg, http.StatusMethodNotAllowed)
			return
		}
		report, err := buildDiagnostics(home)
		if err != nil {
			writeInviteError(w, http.StatusInternalServerError, "could not build the diagnostics")
			return
		}
		writeInviteJSON(w, http.StatusOK, report)
	})
}
