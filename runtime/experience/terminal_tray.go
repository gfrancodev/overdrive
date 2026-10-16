package main

import (
	"database/sql"
	"strconv"
	"strings"
)

func attachTerminalTraySignals(home string, dash *peerDashboard) {
	if dash == nil {
		return
	}
	mgr, err := getTerminalManager(home)
	if err != nil {
		return
	}
	sessions, err := mgr.ListSessions(200)
	if err != nil {
		return
	}
	store := mgr.store
	active := 0
	for _, s := range sessions {
		switch s.State {
		case TerminalStateRunning, TerminalStateStarting, TerminalStateDetached, TerminalStateCreated:
			active++
		}
	}
	dash.TerminalActiveCount = active
	id, _, err := loadOrCreateIdentity(home)
	if err != nil {
		return
	}
	dash.TerminalControlID = latestTerminalControlRequestID(store, id.DeviceID)
	dash.TerminalInviteID = latestTerminalInviteSignalID(store, id.DeviceID)
}

func latestTerminalControlRequestID(store *TerminalStore, ownerDeviceID string) string {
	if store == nil || ownerDeviceID == "" {
		return ""
	}
	var auditID int64
	var sessionID string
	err := store.db.QueryRow(`SELECT id, session_id FROM terminal_audit_events
 WHERE action = 'control_request' ORDER BY id DESC LIMIT 1`).Scan(&auditID, &sessionID)
	if err != nil {
		return ""
	}
	sess, err := store.GetSession(sessionID)
	if err != nil {
		return ""
	}
	if sess.OwnerDeviceID != ownerDeviceID && sess.HostDeviceID != ownerDeviceID {
		return ""
	}
	return sessionID + ":" + strconv.FormatInt(auditID, 10)
}

func latestTerminalInviteSignalID(store *TerminalStore, deviceID string) string {
	if store == nil || deviceID == "" {
		return ""
	}
	var shareID, sessionID, target string
	err := store.db.QueryRow(`SELECT id, session_id, target_id FROM terminal_shares
 WHERE target_id = ? ORDER BY created_at DESC LIMIT 1`, deviceID).Scan(&shareID, &sessionID, &target)
	if err != nil {
		if err != sql.ErrNoRows {
			return ""
		}
		return ""
	}
	return sessionID + ":" + strings.TrimSpace(shareID)
}
