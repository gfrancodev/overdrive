package main

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
)

type ShareStatus struct {
	Mode              string   `json:"mode"`
	CircleID          string   `json:"circle_id,omitempty"`
	CircleName        string   `json:"circle_name,omitempty"`
	FolderAllowed     bool     `json:"folder_allowed"`
	PeerEndpoints     []string `json:"peer_endpoints,omitempty"`
	PeerMemoryCount   int      `json:"peer_memory_count"`
	LastSyncCursor    string   `json:"last_sync_cursor,omitempty"`
	LastImported      int      `json:"last_imported_packets"`
	PeersContacted    int      `json:"last_peers_contacted"`
	ListenerConfigured bool    `json:"listener_configured"`
	DeviceID          string   `json:"device_id,omitempty"`
}

type shareSyncReport struct {
	Imported       int `json:"imported"`
	PeersContacted int `json:"peers_contacted"`
	PeersOK        int `json:"peers_ok"`
}

func (e *Engine) shareStatus(project Project) (ShareStatus, error) {
	out := ShareStatus{Mode: "off", FolderAllowed: false}
	id, _, err := loadOrCreateIdentity(e.home)
	if err == nil {
		out.DeviceID = id.DeviceID
	}
	out.ListenerConfigured = strings.TrimSpace(os.Getenv("OVERDRIVE_SHARE_LISTEN")) != ""

	circle, ok := circleForProject(e.home, project.Root)
	if !ok {
		return out, nil
	}
	out.CircleID = circle.ID
	out.CircleName = circle.Name
	out.PeerEndpoints = circle.PeerEndpoints
	out.FolderAllowed = folderAllowed(circle, project.Root)
	if !out.FolderAllowed {
		out.Mode = "folder-blocked"
		return out, nil
	}
	if _, ok := circle.activeMember(id.DeviceID); !ok {
		out.Mode = "not-member"
		return out, nil
	}
	out.Mode = "ready"
	out.LastSyncCursor = e.getMeta("share_cursor:" + circle.ID + ":" + project.Repository)
	if raw := e.getMeta("share_last_sync:" + circle.ID + ":" + project.Repository); raw != "" {
		var report shareSyncReport
		if json.Unmarshal([]byte(raw), &report) == nil {
			out.LastImported = report.Imported
			out.PeersContacted = report.PeersContacted
			if report.Imported > 0 || report.PeersOK > 0 {
				out.Mode = "synced"
			}
		}
	}
	count, err := e.peerMemoryCount(project.Repository, circle.ID)
	if err != nil {
		return out, err
	}
	out.PeerMemoryCount = count
	return out, nil
}

func (e *Engine) peerMemoryCount(repository, circleID string) (int, error) {
	if hookPeerMemoryCount != nil {
		return hookPeerMemoryCount(e, repository, circleID)
	}
	var n int
	err := e.db.QueryRow(`SELECT COUNT(*) FROM memories WHERE origin = 'peer' AND status = 'active' AND scope_id = ? AND circle_id = ?`,
		repository, circleID).Scan(&n)
	return n, err
}

func (e *Engine) recordShareSyncReport(circleID, repository string, report shareSyncReport) {
	key := "share_last_sync:" + circleID + ":" + repository
	raw, err := hookJSONMarshal(report)
	if err != nil {
		return
	}
	e.setMeta(key, string(raw))
}

var shareStatusTextStrategies = map[string]func(ShareStatus) string{
	"off": func(ShareStatus) string {
		return "share: off (no circle for this folder)"
	},
	"folder-blocked": func(s ShareStatus) string {
		return fmt.Sprintf("share: folder-blocked (circle %s; add an allowed folder to enable P2P)", s.CircleName)
	},
	"not-member": func(s ShareStatus) string {
		return fmt.Sprintf("share: not-member (circle %s)", s.CircleName)
	},
	"synced": func(s ShareStatus) string {
		return fmt.Sprintf("share: synced circle=%s imported=%d peer_memories=%d peers=%d", s.CircleName, s.LastImported, s.PeerMemoryCount, s.PeersContacted)
	},
	"ready": func(s ShareStatus) string {
		return fmt.Sprintf("share: ready circle=%s peer_memories=%d endpoints=%d", s.CircleName, s.PeerMemoryCount, len(s.PeerEndpoints))
	},
}

func formatShareStatusText(s ShareStatus) string {
	if format, ok := shareStatusTextStrategies[s.Mode]; ok {
		return format(s)
	}
	return "share: " + s.Mode
}
