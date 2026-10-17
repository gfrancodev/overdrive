package main

import (
	"database/sql"
	"errors"
	"strings"
	"time"
)

// Circle membership says who is trusted on the network; a share row says which device may reach one
// session from another core. Only the session owner writes shares, and the host checks them on every peer op.

const terminalSharePermissionObserve = "observe"

var errTerminalShareCircle = errors.New("terminal session belongs to another circle")

func newTerminalShareID() string {
	return "tsh_" + randomToken(16)
}

// GrantShare keeps one row per (session, device); granting again returns the existing row.
func (s *TerminalStore) GrantShare(sessionID, deviceID, permission string) (TerminalShare, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var share TerminalShare
	err := s.db.QueryRow(`SELECT id, session_id, target_id, permission, created_at, expires_at FROM terminal_shares
 WHERE session_id=? AND target_id=? LIMIT 1`, sessionID, deviceID).
		Scan(&share.ID, &share.SessionID, &share.TargetID, &share.Permission, &share.CreatedAt, &share.ExpiresAt)
	if err == nil {
		_, err = s.db.Exec(`UPDATE terminal_shares SET permission=?, expires_at='' WHERE id=?`, permission, share.ID)
		share.Permission, share.ExpiresAt = permission, ""
		return share, err
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return TerminalShare{}, err
	}
	share = TerminalShare{
		ID: newTerminalShareID(), SessionID: sessionID, TargetID: deviceID,
		Permission: permission, CreatedAt: terminalNow(),
	}
	_, err = s.db.Exec(`INSERT INTO terminal_shares (id, session_id, target_id, permission, created_at, expires_at)
 VALUES (?,?,?,?,?,'')`, share.ID, share.SessionID, share.TargetID, share.Permission, share.CreatedAt)
	if err != nil {
		return TerminalShare{}, err
	}
	_ = s.audit(sessionID, deviceID, "share_grant", permission)
	return share, nil
}

// HasShare reports whether deviceID holds an unexpired share on sessionID.
func (s *TerminalStore) HasShare(sessionID, deviceID string) (bool, error) {
	var expires string
	err := s.db.QueryRow(`SELECT expires_at FROM terminal_shares WHERE session_id=? AND target_id=? LIMIT 1`,
		sessionID, deviceID).Scan(&expires)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if expires == "" {
		return true, nil
	}
	t, err := time.Parse(time.RFC3339, expires)
	return err == nil && time.Now().Before(t), nil
}

func (s *TerminalStore) RevokeShare(sessionID, deviceID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	res, err := s.db.Exec(`DELETE FROM terminal_shares WHERE session_id=? AND target_id=?`, sessionID, deviceID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n > 0 {
		_ = s.audit(sessionID, deviceID, "share_revoke", "")
	}
	return nil
}

func (s *TerminalStore) ListSessionsSharedWithDevice(deviceID string) ([]string, error) {
	rows, err := s.db.Query(`SELECT DISTINCT session_id FROM terminal_shares WHERE target_id=?`, deviceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

// RevokeSharesForDevice drops every share held by deviceID and returns the affected sessions.
func (s *TerminalStore) RevokeSharesForDevice(deviceID string) ([]string, error) {
	sessions, err := s.ListSessionsSharedWithDevice(deviceID)
	if err != nil {
		return nil, err
	}
	for _, sid := range sessions {
		if err := s.RevokeShare(sid, deviceID); err != nil {
			return sessions, err
		}
	}
	return sessions, nil
}

// revokeTerminalSessionAccess drops one device from one session: its share, any control lease it holds,
// and its live streams.
func revokeTerminalSessionAccess(store *TerminalStore, sessionID, deviceID, reason string) {
	_ = store.RevokeShare(sessionID, deviceID)
	if lease, epoch, err := store.GetActiveLease(sessionID); err == nil && lease.HolderDeviceID == deviceID {
		_, _ = store.grantLeaseCAS(sessionID, store.sessionOwner(sessionID), epoch, time.Now().Add(time.Hour).UTC().Format(time.RFC3339))
	}
	_ = store.audit(sessionID, deviceID, reason, "share, lease and streams cleared")
	closeTerminalStreamsForDevice(sessionID, deviceID)
}

// shareTerminalWithConversation grants every member of a chat conversation access to a session this device
// owns, before the card that announces it is sent. Cards for sessions hosted elsewhere or owned by another
// device grant nothing: only the owner delegates access, and the host enforces it.
func shareTerminalWithConversation(home string, circle Circle, selfID, sessionID, conversationID string) error {
	if sessionID == "" {
		return nil
	}
	store, err := newTerminalStore(home)
	if err != nil {
		return err
	}
	sess, err := store.GetSession(sessionID)
	if err != nil {
		if strings.Contains(err.Error(), string(TerminalErrSessionNotFound)) {
			return nil
		}
		return err
	}
	if sess.OwnerDeviceID != selfID {
		return nil
	}
	if sess.CircleID != circle.ID {
		return errTerminalShareCircle
	}
	audience, err := conversationAudience(home, circle.ID, conversationID)
	if err != nil {
		return err
	}
	for _, deviceID := range audience {
		if _, ok := circle.activeMember(deviceID); !ok || deviceID == selfID {
			continue
		}
		if _, err := store.GrantShare(sessionID, deviceID, terminalSharePermissionObserve); err != nil {
			return err
		}
	}
	return nil
}

// conversationAudience lists the devices that read a conversation: both ends of a direct thread, the roster
// of a custom group, or every active member for the circle channel and project threads.
func conversationAudience(home, meshID, conversationID string) ([]string, error) {
	switch {
	case strings.HasPrefix(conversationID, "direct:"):
		return strings.Split(strings.TrimPrefix(conversationID, "direct:"), ":"), nil
	case isGroupConversationID(conversationID) && conversationID != circleGroupConversationID():
		rec, err := loadChatGroupByConversation(home, meshID, conversationID)
		if err != nil {
			return nil, err
		}
		return rec.MemberDeviceIDs, nil
	default:
		return activeCircleDeviceIDs(home, meshID)
	}
}
