package main

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

var (
	terminalMu  sync.Mutex
	terminalDBs = map[string]*sql.DB{}
)

func migrateTerminalParticipants(db *sql.DB) error {
	rows, err := db.Query(`SELECT id, session_id, device_id, role, created_at FROM terminal_participants`)
	if err != nil {
		return err
	}
	type row struct {
		id, session, device, role, created string
	}
	var all []row
	for rows.Next() {
		var r row
		if err := rows.Scan(&r.id, &r.session, &r.device, &r.role, &r.created); err != nil {
			_ = rows.Close()
			return err
		}
		all = append(all, r)
	}
	if err := rows.Close(); err != nil {
		return err
	}
	if err := rows.Err(); err != nil {
		return err
	}
	keep := map[string]row{}
	drop := make([]string, 0)
	for _, r := range all {
		key := r.session + "\x00" + r.device
		prev, ok := keep[key]
		if !ok {
			keep[key] = r
			continue
		}
		preferNew := r.role == "owner" && prev.role != "owner"
		if preferNew {
			drop = append(drop, prev.id)
			keep[key] = r
			continue
		}
		drop = append(drop, r.id)
	}
	for _, id := range drop {
		if _, err := db.Exec(`DELETE FROM terminal_participants WHERE id=?`, id); err != nil {
			return err
		}
	}
	_, err = db.Exec(`CREATE UNIQUE INDEX IF NOT EXISTS terminal_participants_session_device ON terminal_participants(session_id, device_id)`)
	return err
}

func openTerminalDB(home string) (*sql.DB, error) {
	terminalMu.Lock()
	defer terminalMu.Unlock()
	if db, ok := terminalDBs[home]; ok {
		return db, nil
	}
	if err := hookMkdirAll(shareDir(home), 0o700); err != nil {
		return nil, err
	}
	db, err := hookDBOpen("sqlite", terminalDBPath(home)+"?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)&_pragma=foreign_keys(1)")
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	if err := initTerminalSchema(db); err != nil {
		_ = db.Close()
		return nil, err
	}
	_ = hookChmod(terminalDBPath(home), 0o600)
	terminalDBs[home] = db
	return db, nil
}

func initTerminalSchema(db *sql.DB) error {
	_, err := db.Exec(`
CREATE TABLE IF NOT EXISTS terminal_sessions (
  id TEXT PRIMARY KEY,
  name TEXT NOT NULL,
  state TEXT NOT NULL,
  mode TEXT NOT NULL,
  owner_device_id TEXT NOT NULL,
  host_device_id TEXT NOT NULL,
  working_directory TEXT NOT NULL,
  command TEXT NOT NULL,
  harness_id TEXT NOT NULL DEFAULT '',
  project_id TEXT NOT NULL DEFAULT '',
  circle_id TEXT NOT NULL DEFAULT '',
  cols INTEGER NOT NULL DEFAULT 80,
  rows INTEGER NOT NULL DEFAULT 24,
  private_mode INTEGER NOT NULL DEFAULT 0,
  recording_paused INTEGER NOT NULL DEFAULT 0,
  last_sequence INTEGER NOT NULL DEFAULT 0,
  host_pid INTEGER NOT NULL DEFAULT 0,
  socket_path TEXT NOT NULL DEFAULT '',
  ipc_token TEXT NOT NULL DEFAULT '',
  exit_code INTEGER,
  error_message TEXT NOT NULL DEFAULT '',
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS terminal_participants (
  id TEXT PRIMARY KEY,
  session_id TEXT NOT NULL REFERENCES terminal_sessions(id) ON DELETE CASCADE,
  device_id TEXT NOT NULL,
  user_id TEXT NOT NULL DEFAULT '',
  role TEXT NOT NULL DEFAULT 'observer',
  capabilities_json TEXT NOT NULL DEFAULT '[]',
  connected INTEGER NOT NULL DEFAULT 0,
  last_seen_at TEXT NOT NULL,
  created_at TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS terminal_participants_session ON terminal_participants(session_id);
CREATE TABLE IF NOT EXISTS terminal_control_leases (
  id TEXT PRIMARY KEY,
  session_id TEXT NOT NULL REFERENCES terminal_sessions(id) ON DELETE CASCADE,
  holder_device_id TEXT NOT NULL,
  epoch INTEGER NOT NULL,
  granted_at TEXT NOT NULL,
  expires_at TEXT NOT NULL DEFAULT '',
  revoked INTEGER NOT NULL DEFAULT 0
);
CREATE INDEX IF NOT EXISTS terminal_leases_session ON terminal_control_leases(session_id);
CREATE TABLE IF NOT EXISTS terminal_events (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  session_id TEXT NOT NULL REFERENCES terminal_sessions(id) ON DELETE CASCADE,
  sequence INTEGER NOT NULL,
  kind TEXT NOT NULL,
  timestamp TEXT NOT NULL,
  actor_id TEXT NOT NULL DEFAULT '',
  payload BLOB NOT NULL DEFAULT X'',
  UNIQUE(session_id, sequence)
);
CREATE INDEX IF NOT EXISTS terminal_events_session_seq ON terminal_events(session_id, sequence);
CREATE TABLE IF NOT EXISTS terminal_recording_segments (
  id TEXT PRIMARY KEY,
  session_id TEXT NOT NULL REFERENCES terminal_sessions(id) ON DELETE CASCADE,
  start_seq INTEGER NOT NULL,
  end_seq INTEGER NOT NULL DEFAULT 0,
  started_at TEXT NOT NULL,
  ended_at TEXT NOT NULL DEFAULT '',
  bytes INTEGER NOT NULL DEFAULT 0,
  paused INTEGER NOT NULL DEFAULT 0
);
CREATE TABLE IF NOT EXISTS terminal_snapshots (
  id TEXT PRIMARY KEY,
  session_id TEXT NOT NULL REFERENCES terminal_sessions(id) ON DELETE CASCADE,
  sequence INTEGER NOT NULL,
  cols INTEGER NOT NULL,
  rows INTEGER NOT NULL,
  created_at TEXT NOT NULL,
  payload BLOB NOT NULL
);
CREATE INDEX IF NOT EXISTS terminal_snapshots_session ON terminal_snapshots(session_id, sequence);
CREATE TABLE IF NOT EXISTS terminal_shares (
  id TEXT PRIMARY KEY,
  session_id TEXT NOT NULL REFERENCES terminal_sessions(id) ON DELETE CASCADE,
  target_id TEXT NOT NULL,
  permission TEXT NOT NULL,
  created_at TEXT NOT NULL,
  expires_at TEXT NOT NULL DEFAULT ''
);
CREATE TABLE IF NOT EXISTS terminal_context_links (
  id TEXT PRIMARY KEY,
  session_id TEXT NOT NULL REFERENCES terminal_sessions(id) ON DELETE CASCADE,
  link_kind TEXT NOT NULL,
  ref_id TEXT NOT NULL,
  start_seq INTEGER NOT NULL,
  end_seq INTEGER NOT NULL,
  created_at TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS terminal_audit_events (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  session_id TEXT NOT NULL,
  actor_id TEXT NOT NULL DEFAULT '',
  action TEXT NOT NULL,
  detail TEXT NOT NULL DEFAULT '',
  created_at TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS terminal_audit_session ON terminal_audit_events(session_id);
`)
	if err != nil {
		return err
	}
	return migrateTerminalParticipants(db)
}

// TerminalStore persists terminal sessions, events, and replay data.
type TerminalStore struct {
	home string
	db   *sql.DB
	mu   sync.Mutex
}

func newTerminalStore(home string) (*TerminalStore, error) {
	db, err := openTerminalDB(home)
	if err != nil {
		return nil, err
	}
	return &TerminalStore{home: home, db: db}, nil
}

func (s *TerminalStore) Close() {
	// pooled in terminalDBs; closed via closeTerminalDBs in tests
}

func closeTerminalDBs() {
	terminalMu.Lock()
	defer terminalMu.Unlock()
	for home, db := range terminalDBs {
		_ = db.Close()
		delete(terminalDBs, home)
	}
	resetTerminalManagersForTests()
}

func (s *TerminalStore) InsertSession(sess TerminalSession) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, err := s.db.Exec(`INSERT INTO terminal_sessions (
 id, name, state, mode, owner_device_id, host_device_id, working_directory, command,
 harness_id, project_id, circle_id, cols, rows, private_mode, recording_paused, last_sequence,
 host_pid, socket_path, ipc_token, exit_code, error_message, created_at, updated_at
) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		sess.ID, sess.Name, string(sess.State), string(sess.Mode), sess.OwnerDeviceID, sess.HostDeviceID,
		sess.WorkingDirectory, sess.Command, sess.HarnessID, sess.ProjectID, sess.CircleID,
		sess.Cols, sess.Rows, boolInt(sess.PrivateMode), boolInt(sess.RecordingPaused), sess.LastSequence,
		sess.HostPID, sess.SocketPath, sess.IPCToken, sess.ExitCode, sess.ErrorMessage, sess.CreatedAt, sess.UpdatedAt,
	)
	return err
}

func (s *TerminalStore) UpdateSessionState(sessionID string, state TerminalSessionState, errMsg string, exitCode *int) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, err := s.db.Exec(`UPDATE terminal_sessions SET state=?, error_message=?, exit_code=?, updated_at=? WHERE id=?`,
		string(state), errMsg, exitCode, terminalNow(), sessionID)
	return err
}

func (s *TerminalStore) UpdateSessionHost(sessionID string, pid int, socketPath string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, err := s.db.Exec(`UPDATE terminal_sessions SET host_pid=?, socket_path=?, updated_at=? WHERE id=?`,
		pid, socketPath, terminalNow(), sessionID)
	return err
}

func (s *TerminalStore) GetSession(sessionID string) (TerminalSession, error) {
	row := s.db.QueryRow(`SELECT id, name, state, mode, owner_device_id, host_device_id, working_directory, command,
 harness_id, project_id, circle_id, cols, rows, private_mode, recording_paused, last_sequence,
 host_pid, socket_path, ipc_token, exit_code, error_message, created_at, updated_at
 FROM terminal_sessions WHERE id=?`, sessionID)
	return scanTerminalSession(row)
}

func (s *TerminalStore) DeleteSession(sessionID string) error {
	_, err := s.db.Exec(`DELETE FROM terminal_sessions WHERE id=?`, sessionID)
	return err
}

func terminalSessionLookupErr(err error) error {
	if errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("%s", TerminalErrSessionNotFound)
	}
	return err
}

func scanTerminalSession(row *sql.Row) (TerminalSession, error) {
	var sess TerminalSession
	var state, mode string
	var priv, rec int
	var exitCode sql.NullInt64
	err := row.Scan(
		&sess.ID, &sess.Name, &state, &mode, &sess.OwnerDeviceID, &sess.HostDeviceID,
		&sess.WorkingDirectory, &sess.Command, &sess.HarnessID, &sess.ProjectID, &sess.CircleID,
		&sess.Cols, &sess.Rows, &priv, &rec, &sess.LastSequence, &sess.HostPID, &sess.SocketPath,
		&sess.IPCToken, &exitCode, &sess.ErrorMessage, &sess.CreatedAt, &sess.UpdatedAt,
	)
	if err != nil {
		return TerminalSession{}, terminalSessionLookupErr(err)
	}
	sess.State = TerminalSessionState(state)
	sess.Mode = TerminalSessionMode(mode)
	sess.PrivateMode = priv != 0
	sess.RecordingPaused = rec != 0
	if exitCode.Valid {
		c := int(exitCode.Int64)
		sess.ExitCode = &c
	}
	return sess, nil
}

func boolInt(v bool) int {
	if v {
		return 1
	}
	return 0
}

// NextSequence allocates the next monotonic sequence for a session (host-only).
func (s *TerminalStore) NextSequence(sessionID string) (int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, err := s.db.Begin()
	if err != nil {
		return 0, err
	}
	defer func() { _ = tx.Rollback() }()
	var seq int64
	if err := tx.QueryRow(`SELECT last_sequence FROM terminal_sessions WHERE id=?`, sessionID).Scan(&seq); err != nil {
		return 0, terminalSessionLookupErr(err)
	}
	seq++
	if _, err := tx.Exec(`UPDATE terminal_sessions SET last_sequence=?, updated_at=? WHERE id=?`, seq, terminalNow(), sessionID); err != nil {
		return 0, err
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return seq, nil
}

func (s *TerminalStore) AppendEvent(ev SessionEvent) (int64, error) {
	if ev.Timestamp == "" {
		ev.Timestamp = terminalNow()
	}
	res, err := s.db.Exec(`INSERT INTO terminal_events (session_id, sequence, kind, timestamp, actor_id, payload)
 VALUES (?,?,?,?,?,?)`, ev.SessionID, ev.Sequence, string(ev.Kind), ev.Timestamp, ev.ActorID, ev.Raw)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

func (s *TerminalStore) RecordOutput(sessionID, actorID string, data []byte, screen *ansiScreen) (SessionEvent, error) {
	seq, err := s.NextSequence(sessionID)
	if err != nil {
		return SessionEvent{}, err
	}
	sess, err := s.GetSession(sessionID)
	if err != nil {
		return SessionEvent{}, err
	}
	if sess.PrivateMode || sess.RecordingPaused {
		// Still feed local emulator for snapshots; skip durable output event.
		if screen != nil {
			screen.Write(data)
		}
		return SessionEvent{SessionID: sessionID, Sequence: seq, Kind: TerminalEventOutput}, nil
	}
	if screen != nil {
		screen.Write(data)
	}
	ev := SessionEvent{
		SessionID: sessionID,
		Sequence:  seq,
		Kind:      TerminalEventOutput,
		Timestamp: terminalNow(),
		ActorID:   actorID,
		Raw:       append([]byte(nil), data...),
	}
	if _, err := s.AppendEvent(ev); err != nil {
		return SessionEvent{}, err
	}
	s.touchRecordingSegment(sessionID, seq, int64(len(data)))
	return ev, nil
}

func (s *TerminalStore) touchRecordingSegment(sessionID string, seq, nbytes int64) {
	var segID string
	err := s.db.QueryRow(`SELECT id FROM terminal_recording_segments WHERE session_id=? AND ended_at='' ORDER BY started_at DESC LIMIT 1`, sessionID).Scan(&segID)
	now := terminalNow()
	if err == sql.ErrNoRows {
		segID = newTerminalSegmentID()
		_, _ = s.db.Exec(`INSERT INTO terminal_recording_segments (id, session_id, start_seq, started_at, bytes) VALUES (?,?,?,?,?)`,
			segID, sessionID, seq, now, nbytes)
		return
	}
	if err != nil {
		return
	}
	_, _ = s.db.Exec(`UPDATE terminal_recording_segments SET end_seq=?, bytes=bytes+? WHERE id=?`, seq, nbytes, segID)
}

func (s *TerminalStore) RecordInput(sessionID, actorID, inputID string, data []byte) (SessionEvent, error) {
	seq, err := s.NextSequence(sessionID)
	if err != nil {
		return SessionEvent{}, err
	}
	payload, _ := json.Marshal(terminalInputPayload{Data: string(data), InputID: inputID})
	ev := SessionEvent{
		SessionID: sessionID,
		Sequence:  seq,
		Kind:      TerminalEventInput,
		Timestamp: terminalNow(),
		ActorID:   actorID,
		Raw:       payload,
	}
	if _, err := s.AppendEvent(ev); err != nil {
		return SessionEvent{}, err
	}
	return ev, nil
}

func (s *TerminalStore) SaveSnapshot(sessionID string, seq int64, screen *ansiScreen) (TerminalSnapshot, error) {
	if screen == nil {
		return TerminalSnapshot{}, errors.New("screen required")
	}
	snap := screen.Snapshot()
	payload, err := json.Marshal(snap)
	if err != nil {
		return TerminalSnapshot{}, err
	}
	id := newTerminalSnapshotID()
	now := terminalNow()
	_, err = s.db.Exec(`INSERT INTO terminal_snapshots (id, session_id, sequence, cols, rows, created_at, payload) VALUES (?,?,?,?,?,?,?)`,
		id, sessionID, seq, snap.Cols, snap.Rows, now, payload)
	if err != nil {
		return TerminalSnapshot{}, err
	}
	ev := SessionEvent{
		SessionID: sessionID,
		Sequence:  seq,
		Kind:      TerminalEventSnapshot,
		Timestamp: now,
		Raw:       payload,
	}
	_, _ = s.AppendEvent(ev)
	return TerminalSnapshot{ID: id, SessionID: sessionID, Sequence: seq, Cols: snap.Cols, Rows: snap.Rows, CreatedAt: now, Payload: payload}, nil
}

func (s *TerminalStore) SetPrivateMode(sessionID string, enabled bool, actorID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	recPaused := 0
	if enabled {
		recPaused = 1
	}
	_, err := s.db.Exec(`UPDATE terminal_sessions SET private_mode=?, recording_paused=?, updated_at=? WHERE id=?`,
		boolInt(enabled), recPaused, terminalNow(), sessionID)
	if err != nil {
		return err
	}
	seq, err := s.nextSequenceLocked(sessionID)
	if err != nil {
		return err
	}
	payload, _ := json.Marshal(map[string]any{"enabled": enabled})
	_, err = s.db.Exec(`INSERT INTO terminal_events (session_id, sequence, kind, timestamp, actor_id, payload) VALUES (?,?,?,?,?,?)`,
		sessionID, seq, string(TerminalEventPrivate), terminalNow(), actorID, payload)
	if enabled {
		_, _ = s.db.Exec(`UPDATE terminal_control_leases SET revoked=1 WHERE session_id=? AND revoked=0`, sessionID)
		_, _ = s.db.Exec(`INSERT INTO terminal_audit_events (session_id, actor_id, action, detail, created_at) VALUES (?,?,?,?,?)`,
			sessionID, actorID, "private_mode_enter", "", terminalNow())
	} else {
		_, _ = s.db.Exec(`INSERT INTO terminal_audit_events (session_id, actor_id, action, detail, created_at) VALUES (?,?,?,?,?)`,
			sessionID, actorID, "private_mode_exit", "", terminalNow())
	}
	return err
}

func (s *TerminalStore) nextSequenceLocked(sessionID string) (int64, error) {
	var seq int64
	if err := s.db.QueryRow(`SELECT last_sequence FROM terminal_sessions WHERE id=?`, sessionID).Scan(&seq); err != nil {
		return 0, terminalSessionLookupErr(err)
	}
	seq++
	_, err := s.db.Exec(`UPDATE terminal_sessions SET last_sequence=?, updated_at=? WHERE id=?`, seq, terminalNow(), sessionID)
	return seq, err
}

func (s *TerminalStore) PauseRecording(sessionID, actorID string) error {
	_, err := s.db.Exec(`UPDATE terminal_sessions SET recording_paused=1, updated_at=? WHERE id=?`, terminalNow(), sessionID)
	if err != nil {
		return err
	}
	_, _ = s.db.Exec(`UPDATE terminal_recording_segments SET paused=1, ended_at=? WHERE session_id=? AND ended_at=''`, terminalNow(), sessionID)
	return s.audit(sessionID, actorID, "recording_pause", "")
}

func (s *TerminalStore) ResumeRecording(sessionID, actorID string) error {
	_, err := s.db.Exec(`UPDATE terminal_sessions SET recording_paused=0, updated_at=? WHERE id=?`, terminalNow(), sessionID)
	if err != nil {
		return err
	}
	sess, _ := s.GetSession(sessionID)
	if !sess.PrivateMode {
		seq := sess.LastSequence + 1
		segID := newTerminalSegmentID()
		_, _ = s.db.Exec(`INSERT INTO terminal_recording_segments (id, session_id, start_seq, started_at) VALUES (?,?,?,?)`,
			segID, sessionID, seq, terminalNow())
	}
	return s.audit(sessionID, actorID, "recording_resume", "")
}

func (s *TerminalStore) audit(sessionID, actorID, action, detail string) error {
	_, err := s.db.Exec(`INSERT INTO terminal_audit_events (session_id, actor_id, action, detail, created_at) VALUES (?,?,?,?,?)`,
		sessionID, actorID, action, detail, terminalNow())
	return err
}

// PendingControlRequesters lists viewers who asked for control and have not been granted or rejected yet.
func (s *TerminalStore) PendingControlRequesters(sessionID string) ([]string, error) {
	sess, err := s.GetSession(sessionID)
	if err != nil {
		return nil, err
	}
	lease, _, _ := s.GetActiveLease(sessionID)
	rows, err := s.db.Query(`SELECT actor_id, action, detail FROM terminal_audit_events
 WHERE session_id=? AND action IN ('control_request','control_reject') ORDER BY id`, sessionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	pending := map[string]struct{}{}
	for rows.Next() {
		var actor, action, detail string
		if err := rows.Scan(&actor, &action, &detail); err != nil {
			return nil, err
		}
		switch action {
		case "control_request":
			if actor != sess.OwnerDeviceID && actor != sess.HostDeviceID {
				pending[actor] = struct{}{}
			}
		case "control_reject":
			target := strings.TrimSpace(strings.Split(detail, ":")[0])
			delete(pending, target)
		}
	}
	out := make([]string, 0, len(pending))
	for id := range pending {
		if lease.HolderDeviceID != "" && id == lease.HolderDeviceID {
			continue
		}
		out = append(out, id)
	}
	sort.Strings(out)
	return out, nil
}

// ReplayEvents returns read-only events for UI replay (never executes INPUT).
func (s *TerminalStore) ReplayEvents(sessionID string, fromSeq, toSeq int64, limit int) ([]SessionEvent, error) {
	if limit <= 0 {
		limit = 500
	}
	if limit > 5000 {
		limit = 5000
	}
	q := `SELECT id, session_id, sequence, kind, timestamp, actor_id, payload FROM terminal_events
 WHERE session_id=? AND sequence>=?`
	args := []any{sessionID, fromSeq}
	if toSeq > 0 {
		q += ` AND sequence<=?`
		args = append(args, toSeq)
	}
	q += ` ORDER BY sequence ASC LIMIT ?`
	args = append(args, limit)
	rows, err := s.db.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []SessionEvent
	for rows.Next() {
		var ev SessionEvent
		var kind string
		if err := rows.Scan(&ev.ID, &ev.SessionID, &ev.Sequence, &kind, &ev.Timestamp, &ev.ActorID, &ev.Raw); err != nil {
			return nil, err
		}
		ev.Kind = TerminalEventKind(kind)
		out = append(out, ev)
	}
	return out, rows.Err()
}

// ReplayRecentOutput returns the newest OUTPUT/SNAPSHOT events (oldest first) for attach replay.
func (s *TerminalStore) ReplayRecentOutput(sessionID string, limit int) ([]SessionEvent, error) {
	if limit <= 0 {
		limit = 500
	}
	if limit > 5000 {
		limit = 5000
	}
	rows, err := s.db.Query(`SELECT id, session_id, sequence, kind, timestamp, actor_id, payload FROM (
 SELECT id, session_id, sequence, kind, timestamp, actor_id, payload FROM terminal_events
 WHERE session_id=? AND kind IN (?, ?) ORDER BY sequence DESC LIMIT ?
) ORDER BY sequence ASC`, sessionID, string(TerminalEventOutput), string(TerminalEventSnapshot), limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []SessionEvent
	for rows.Next() {
		var ev SessionEvent
		var kind string
		if err := rows.Scan(&ev.ID, &ev.SessionID, &ev.Sequence, &kind, &ev.Timestamp, &ev.ActorID, &ev.Raw); err != nil {
			return nil, err
		}
		ev.Kind = TerminalEventKind(kind)
		out = append(out, ev)
	}
	return out, rows.Err()
}

func (s *TerminalStore) ListParticipants(sessionID string) ([]TerminalParticipant, error) {
	rows, err := s.db.Query(`SELECT id, session_id, device_id, user_id, role, capabilities_json, connected, last_seen_at, created_at
 FROM terminal_participants WHERE session_id=? ORDER BY created_at`, sessionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []TerminalParticipant
	for rows.Next() {
		var p TerminalParticipant
		var capsJSON string
		var connected int
		if err := rows.Scan(&p.ID, &p.SessionID, &p.DeviceID, &p.UserID, &p.Role, &capsJSON, &connected, &p.LastSeenAt, &p.CreatedAt); err != nil {
			return nil, err
		}
		p.Connected = connected != 0
		_ = json.Unmarshal([]byte(capsJSON), &p.Capabilities)
		out = append(out, p)
	}
	return out, rows.Err()
}

func (s *TerminalStore) RemoveParticipant(sessionID, participantID string) error {
	_, err := s.db.Exec(`DELETE FROM terminal_participants WHERE session_id=? AND id=?`, sessionID, participantID)
	return err
}

func (s *TerminalStore) UpsertParticipant(p TerminalParticipant) error {
	caps, _ := jsonMarshalCaps(p.Capabilities)
	_, err := s.db.Exec(`INSERT INTO terminal_participants (id, session_id, device_id, user_id, role, capabilities_json, connected, last_seen_at, created_at)
 VALUES (?,?,?,?,?,?,?,?,?)
 ON CONFLICT(id) DO UPDATE SET role=excluded.role, capabilities_json=excluded.capabilities_json, connected=excluded.connected, last_seen_at=excluded.last_seen_at`,
		p.ID, p.SessionID, p.DeviceID, p.UserID, p.Role, caps, boolInt(p.Connected), p.LastSeenAt, p.CreatedAt)
	return err
}

func (s *TerminalStore) ListSnapshots(sessionID string, limit int) ([]TerminalSnapshot, error) {
	if limit <= 0 {
		limit = 20
	}
	rows, err := s.db.Query(`SELECT id, session_id, sequence, cols, rows, created_at, payload FROM terminal_snapshots
 WHERE session_id=? ORDER BY sequence DESC LIMIT ?`, sessionID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []TerminalSnapshot
	for rows.Next() {
		var snap TerminalSnapshot
		if err := rows.Scan(&snap.ID, &snap.SessionID, &snap.Sequence, &snap.Cols, &snap.Rows, &snap.CreatedAt, &snap.Payload); err != nil {
			return nil, err
		}
		out = append(out, snap)
	}
	return out, rows.Err()
}

func (s *TerminalStore) ListContextLinks(sessionID string) ([]TerminalContextLink, error) {
	rows, err := s.db.Query(`SELECT id, session_id, link_kind, ref_id, start_seq, end_seq, created_at FROM terminal_context_links WHERE session_id=?`, sessionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []TerminalContextLink
	for rows.Next() {
		var l TerminalContextLink
		if err := rows.Scan(&l.ID, &l.SessionID, &l.LinkKind, &l.RefID, &l.StartSeq, &l.EndSeq, &l.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, l)
	}
	return out, rows.Err()
}

func (s *TerminalStore) GetActiveLease(sessionID string) (ControlLease, int64, error) {
	row := s.db.QueryRow(`SELECT id, session_id, holder_device_id, epoch, granted_at, expires_at, revoked
 FROM terminal_control_leases WHERE session_id=? AND revoked=0 ORDER BY epoch DESC LIMIT 1`, sessionID)
	var lease ControlLease
	var revoked int
	err := row.Scan(&lease.ID, &lease.SessionID, &lease.HolderDeviceID, &lease.Epoch, &lease.GrantedAt, &lease.ExpiresAt, &revoked)
	if err == sql.ErrNoRows {
		return ControlLease{}, 0, nil
	}
	if err != nil {
		return ControlLease{}, 0, err
	}
	lease.Revoked = revoked != 0
	return lease, lease.Epoch, nil
}

func (s *TerminalStore) grantLeaseCAS(sessionID, holderDeviceID string, expectedEpoch int64, expiresAt string) (ControlLease, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var curEpoch int64
	row := s.db.QueryRow(`SELECT epoch FROM terminal_control_leases WHERE session_id=? AND revoked=0 ORDER BY epoch DESC LIMIT 1`, sessionID)
	if err := row.Scan(&curEpoch); err != nil && err != sql.ErrNoRows {
		return ControlLease{}, err
	}
	if curEpoch != expectedEpoch {
		return ControlLease{}, fmt.Errorf("%s", TerminalErrInvalidLease)
	}
	_, _ = s.db.Exec(`UPDATE terminal_control_leases SET revoked=1 WHERE session_id=? AND revoked=0`, sessionID)
	nextEpoch := curEpoch + 1
	now := terminalNow()
	lease := ControlLease{
		ID: newTerminalLeaseID(), SessionID: sessionID, HolderDeviceID: holderDeviceID,
		Epoch: nextEpoch, GrantedAt: now, ExpiresAt: expiresAt,
	}
	_, err := s.db.Exec(`INSERT INTO terminal_control_leases (id, session_id, holder_device_id, epoch, granted_at, expires_at, revoked)
 VALUES (?,?,?,?,?,?,0)`, lease.ID, lease.SessionID, lease.HolderDeviceID, lease.Epoch, lease.GrantedAt, lease.ExpiresAt)
	if err != nil {
		return ControlLease{}, err
	}
	seq, err := s.nextSequenceLocked(sessionID)
	if err != nil {
		return ControlLease{}, err
	}
	payload, _ := json.Marshal(map[string]any{"lease_id": lease.ID, "holder_device_id": holderDeviceID, "epoch": nextEpoch})
	_, err = s.db.Exec(`INSERT INTO terminal_events (session_id, sequence, kind, timestamp, actor_id, payload) VALUES (?,?,?,?,?,?)`,
		sessionID, seq, string(TerminalEventControlLease), now, holderDeviceID, payload)
	if err != nil {
		return ControlLease{}, err
	}
	return lease, nil
}

// UpsertParticipantByDevice keeps a single row per (session_id, device_id); owner role is never downgraded.
func (s *TerminalStore) UpsertParticipantByDevice(p TerminalParticipant) error {
	caps, _ := jsonMarshalCaps(p.Capabilities)
	now := terminalNow()
	if p.LastSeenAt == "" {
		p.LastSeenAt = now
	}
	if p.CreatedAt == "" {
		p.CreatedAt = now
	}
	var existingID, existingRole string
	err := s.db.QueryRow(`SELECT id, role FROM terminal_participants WHERE session_id=? AND device_id=? LIMIT 1`,
		p.SessionID, p.DeviceID).Scan(&existingID, &existingRole)
	if err == sql.ErrNoRows {
		if p.ID == "" {
			p.ID = newTerminalParticipantID()
		}
		return s.insertParticipant(p)
	}
	if err != nil {
		return err
	}
	role := p.Role
	if existingRole == "owner" {
		role = "owner"
	}
	_, err = s.db.Exec(`UPDATE terminal_participants SET role=?, capabilities_json=?, connected=?, last_seen_at=? WHERE id=?`,
		role, caps, boolInt(p.Connected), p.LastSeenAt, existingID)
	return err
}

func (s *TerminalStore) releaseLeaseCAS(sessionID, holderDeviceID, leaseID string, epoch int64) (ControlLease, error) {
	lease, curEpoch, err := s.GetActiveLease(sessionID)
	if err != nil {
		return ControlLease{}, err
	}
	if lease.ID != leaseID || curEpoch != epoch || lease.HolderDeviceID != holderDeviceID {
		return ControlLease{}, fmt.Errorf("%s", TerminalErrInvalidLease)
	}
	sess, err := s.GetSession(sessionID)
	if err != nil {
		return ControlLease{}, err
	}
	exp := time.Now().Add(24 * time.Hour).UTC().Format(time.RFC3339)
	return s.grantLeaseCAS(sessionID, sess.OwnerDeviceID, epoch, exp)
}

func (s *TerminalStore) recordControlEvent(sessionID string, epoch int64, actorID, action string, payload []byte) error {
	detail := string(payload)
	return s.audit(sessionID, actorID, action, detail)
}

func (s *TerminalStore) ListSessionsByParticipantDevice(deviceID string) ([]string, error) {
	rows, err := s.db.Query(`SELECT DISTINCT session_id FROM terminal_participants WHERE device_id=?`, deviceID)
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

func (s *TerminalStore) sessionOwner(sessionID string) string {
	sess, err := s.GetSession(sessionID)
	if err != nil {
		return ""
	}
	return sess.OwnerDeviceID
}

// ReplayApply feeds OUTPUT/SNAPSHOT events into a screen emulator (read-only replay).
func ReplayApply(screen *ansiScreen, events []SessionEvent) {
	for _, ev := range events {
		switch ev.Kind {
		case TerminalEventOutput:
			screen.Write(ev.Raw)
		case TerminalEventSnapshot:
			var snap ansiScreenSnapshot
			if json.Unmarshal(ev.Raw, &snap) == nil {
				screen.Restore(snap)
			}
		}
	}
}

// --- ANSI / VT screen emulator (subset for snapshots) ---

type ansiCell struct {
	Rune rune
	FG   int
	BG   int
	Bold bool
}

type ansiScreen struct {
	cols   int
	rows   int
	curX   int
	curY   int
	cells  [][]ansiCell
	alt    [][]ansiCell
	useAlt bool
}

type ansiScreenSnapshot struct {
	Cols   int        `json:"cols"`
	Rows   int        `json:"rows"`
	CurX   int        `json:"cur_x"`
	CurY   int        `json:"cur_y"`
	UseAlt bool       `json:"use_alt"`
	Cells  [][]string `json:"cells"`
}

func newANSIScreen(cols, rows int) *ansiScreen {
	if cols <= 0 {
		cols = 80
	}
	if rows <= 0 {
		rows = 24
	}
	s := &ansiScreen{cols: cols, rows: rows}
	s.cells = s.allocGrid()
	return s
}

func (s *ansiScreen) allocGrid() [][]ansiCell {
	g := make([][]ansiCell, s.rows)
	for y := 0; y < s.rows; y++ {
		g[y] = make([]ansiCell, s.cols)
	}
	return g
}

func (s *ansiScreen) activeGrid() [][]ansiCell {
	if s.useAlt && s.alt != nil {
		return s.alt
	}
	return s.cells
}

func (s *ansiScreen) tryWriteCSI(p []byte, i int) (int, bool) {
	if p[i] != 0x1b || i+1 >= len(p) || p[i+1] != '[' {
		return i, false
	}
	j := i + 2
	for j < len(p) && p[j] >= 0x30 && p[j] <= 0x3f {
		j++
	}
	for j < len(p) && p[j] >= 0x20 && p[j] <= 0x2f {
		j++
	}
	if j >= len(p) {
		return i, false
	}
	cmd := p[j]
	params := string(p[i+2 : j])
	s.applyCSI(cmd, params)
	return j + 1, true
}

func (s *ansiScreen) writeControlByte(b byte) bool {
	switch b {
	case '\r':
		s.curX = 0
		return true
	case '\n':
		s.curY++
		if s.curY >= s.rows {
			s.scrollUp()
		}
		return true
	case '\t':
		s.curX = (s.curX/8 + 1) * 8
		if s.curX >= s.cols {
			s.curX = s.cols - 1
		}
		return true
	default:
		return b < 0x20
	}
}

func (s *ansiScreen) writeGlyph(b byte) {
	grid := s.activeGrid()
	if s.curY >= 0 && s.curY < s.rows && s.curX >= 0 && s.curX < s.cols {
		grid[s.curY][s.curX].Rune = rune(b)
	}
	s.curX++
	if s.curX >= s.cols {
		s.curX = 0
		s.curY++
		if s.curY >= s.rows {
			s.scrollUp()
		}
	}
}

func (s *ansiScreen) Write(p []byte) {
	if len(p) == 0 {
		return
	}
	for i := 0; i < len(p); {
		if next, ok := s.tryWriteCSI(p, i); ok {
			i = next
			continue
		}
		if s.writeControlByte(p[i]) {
			i++
			continue
		}
		s.writeGlyph(p[i])
		i++
	}
}

func (s *ansiScreen) scrollUp() {
	grid := s.activeGrid()
	copy(grid[0:], grid[1:])
	last := s.rows - 1
	for x := 0; x < s.cols; x++ {
		grid[last][x] = ansiCell{}
	}
	s.curY = s.rows - 1
}

func csiParamInt(parts []string, idx, def int) int {
	if idx >= len(parts) || parts[idx] == "" {
		return def
	}
	n, err := strconv.Atoi(parts[idx])
	if err != nil {
		return def
	}
	return n
}

func (s *ansiScreen) applyCSICursor(parts []string) {
	row := csiParamInt(parts, 0, 1) - 1
	col := csiParamInt(parts, 1, 1) - 1
	if row < 0 {
		row = 0
	}
	if col < 0 {
		col = 0
	}
	s.curY = row
	s.curX = col
}

func (s *ansiScreen) applyCSIErase(parts []string) {
	if csiParamInt(parts, 0, 0) != 2 {
		return
	}
	grid := s.activeGrid()
	for y := 0; y < s.rows; y++ {
		for x := 0; x < s.cols; x++ {
			grid[y][x] = ansiCell{}
		}
	}
	s.curX, s.curY = 0, 0
}

func (s *ansiScreen) applyCSIAltScreen(cmd byte, params string) {
	if !strings.Contains(params, "?1049") {
		return
	}
	if cmd == 'h' {
		s.useAlt = true
		s.alt = s.allocGrid()
		return
	}
	s.useAlt = false
	s.alt = nil
}

func (s *ansiScreen) applyCSI(cmd byte, params string) {
	parts := strings.Split(params, ";")
	switch cmd {
	case 'H', 'f':
		s.applyCSICursor(parts)
	case 'J':
		s.applyCSIErase(parts)
	case 'K', '?', 'm':
		// simplified emulator: line clear, private mode, SGR ignored
	case 'h', 'l':
		s.applyCSIAltScreen(cmd, params)
	}
}

func (s *ansiScreen) Snapshot() ansiScreenSnapshot {
	grid := s.activeGrid()
	rows := make([][]string, s.rows)
	for y := 0; y < s.rows; y++ {
		line := make([]string, s.cols)
		var b strings.Builder
		for x := 0; x < s.cols; x++ {
			r := grid[y][x].Rune
			if r == 0 {
				r = ' '
			}
			b.WriteRune(r)
		}
		line[0] = strings.TrimRight(b.String(), " ")
		rows[y] = line
	}
	return ansiScreenSnapshot{
		Cols: s.cols, Rows: s.rows, CurX: s.curX, CurY: s.curY, UseAlt: s.useAlt, Cells: rows,
	}
}

func (s *ansiScreen) Restore(snap ansiScreenSnapshot) {
	s.cols = snap.Cols
	s.rows = snap.Rows
	s.curX = snap.CurX
	s.curY = snap.CurY
	s.useAlt = snap.UseAlt
	s.cells = s.allocGrid()
	for y := 0; y < len(snap.Cells) && y < s.rows; y++ {
		if len(snap.Cells[y]) == 0 {
			continue
		}
		line := snap.Cells[y][0]
		for x, ch := range line {
			if x >= s.cols {
				break
			}
			s.cells[y][x].Rune = ch
		}
	}
}

func (s *ansiScreen) RenderPlain() string {
	snap := s.Snapshot()
	var buf bytes.Buffer
	for _, row := range snap.Cells {
		if len(row) > 0 {
			buf.WriteString(row[0])
		}
		buf.WriteByte('\n')
	}
	return buf.String()
}
