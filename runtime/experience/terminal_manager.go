package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

var (
	terminalEnvExactBlock = map[string]bool{
		"OVERDRIVE_IPC_TOKEN": true,
		"TERMINAL_IPC_TOKEN":  true,
	}
	terminalEnvPrefixBlock = []string{
		"OVERDRIVE_", "OD_", "CIRCLE_", "MQTT_", "AWS_SECRET", "GITHUB_TOKEN", "GH_TOKEN",
	}
	terminalEnvSuffixBlock = []string{"_API_KEY", "_SECRET", "_SECRET_KEY", "_TOKEN", "_PASSWORD"}
)

// TerminalManager owns session catalog and supervised host processes.
type TerminalManager struct {
	home   string
	store  *TerminalStore
	device DeviceIdentity
	mu     sync.Mutex
	hosts  map[string]*exec.Cmd
}

func newTerminalManager(home string) (*TerminalManager, error) {
	store, err := newTerminalStore(home)
	if err != nil {
		return nil, err
	}
	id, _, err := loadOrCreateIdentity(home)
	if err != nil {
		return nil, err
	}
	return &TerminalManager{home: home, store: store, device: id, hosts: map[string]*exec.Cmd{}}, nil
}

type terminalCreateOpts struct {
	Name             string
	WorkingDirectory string
	ProjectRoot      string
	Command          string
	Args             []string
	Mode             TerminalSessionMode
	HarnessID        string
	ProjectID        string
	CircleID         string
	Cols             int
	Rows             int
	Env              []string
}

func (m *TerminalManager) CreateSession(opts terminalCreateOpts) (TerminalSession, error) {
	sess, opts, err := m.buildCreateSessionRecord(opts)
	if err != nil {
		return TerminalSession{}, err
	}
	if err := m.persistNewSession(sess); err != nil {
		return TerminalSession{}, err
	}
	return m.startSessionHostAndWait(sess, opts)
}

func (m *TerminalManager) buildCreateSessionRecord(opts terminalCreateOpts) (TerminalSession, terminalCreateOpts, error) {
	if strings.TrimSpace(opts.Name) == "" {
		opts.Name = "Terminal"
	}
	if opts.Mode == "" {
		opts.Mode = TerminalModeManaged
	}
	if opts.Mode == TerminalModeContextOnly {
		return TerminalSession{}, opts, errors.New("context_only sessions cannot spawn a host in etapa 1")
	}
	wd, err := m.resolveSessionWorkingDirectory(opts)
	if err != nil {
		return TerminalSession{}, opts, fmt.Errorf("%s: %v", TerminalErrWorkingDirectory, err)
	}
	if opts.Cols <= 0 {
		opts.Cols = 80
	}
	if opts.Rows <= 0 {
		opts.Rows = 24
	}
	cmd := opts.Command
	if cmd == "" {
		cmd, _ = defaultShellCommand()
	}
	now := terminalNow()
	sess := TerminalSession{
		ID: newTerminalSessionID(), Name: opts.Name, State: TerminalStateCreated, Mode: opts.Mode,
		OwnerDeviceID: m.device.DeviceID, HostDeviceID: m.device.DeviceID, WorkingDirectory: wd, Command: cmd,
		HarnessID: opts.HarnessID, ProjectID: opts.ProjectID, CircleID: opts.CircleID,
		Cols: opts.Cols, Rows: opts.Rows, IPCToken: randomToken(32),
		SocketPath: terminalSocketPath(m.home, ""), CreatedAt: now, UpdatedAt: now,
	}
	sess.SocketPath = terminalSocketPath(m.home, sess.ID)
	return sess, opts, nil
}

func (m *TerminalManager) persistNewSession(sess TerminalSession) error {
	if err := m.store.InsertSession(sess); err != nil {
		return err
	}
	now := sess.CreatedAt
	owner := TerminalParticipant{
		ID: newTerminalParticipantID(), SessionID: sess.ID, DeviceID: m.device.DeviceID, Role: "owner",
		Capabilities: []TerminalCapability{TerminalCapHost, TerminalCapControl, TerminalCapInput, TerminalCapObserve, TerminalCapReplay},
		Connected: true, LastSeenAt: now, CreatedAt: now,
	}
	_ = m.store.insertParticipant(owner)
	exp := time.Now().Add(24 * time.Hour).UTC().Format(time.RFC3339)
	_, err := m.store.grantLeaseCAS(sess.ID, m.device.DeviceID, 0, exp)
	return err
}

func (m *TerminalManager) startSessionHostAndWait(sess TerminalSession, opts terminalCreateOpts) (TerminalSession, error) {
	if err := m.spawnSessionHost(sess, opts); err != nil {
		_ = m.store.UpdateSessionState(sess.ID, TerminalStateFailed, err.Error(), nil)
		return TerminalSession{}, err
	}
	sess.State = TerminalStateStarting
	_ = m.store.UpdateSessionState(sess.ID, TerminalStateStarting, "", nil)
	if err := m.waitForSessionRunning(sess.ID); err != nil {
		return TerminalSession{}, err
	}
	return m.store.GetSession(sess.ID)
}

func (m *TerminalManager) waitForSessionRunning(sessionID string) error {
	deadline := time.Now().Add(1500 * time.Millisecond)
	for time.Now().Before(deadline) {
		cur, err := m.store.GetSession(sessionID)
		if err != nil {
			return nil
		}
		if cur.State == TerminalStateFailed {
			msg := cur.ErrorMessage
			if msg == "" {
				msg = "session host failed"
			}
			return fmt.Errorf("%s", msg)
		}
		if cur.State == TerminalStateRunning && sessionHostSocketExists(m.home, sessionID) {
			return nil
		}
		time.Sleep(15 * time.Millisecond)
	}
	return nil
}

func (m *TerminalManager) spawnSessionHost(sess TerminalSession, opts terminalCreateOpts) error {
	if err := hookMkdirAll(terminalSessionsDir(m.home), 0o700); err != nil {
		return err
	}
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	env := filterTerminalEnv(append(os.Environ(), opts.Env...))
	env = append(env,
		"OVERDRIVE_HOME="+m.home,
		"TERMINAL_IPC_TOKEN="+sess.IPCToken,
	)
	args := []string{
		"session-host",
		"--session-id", sess.ID,
		"--home", m.home,
		"--cols", fmt.Sprintf("%d", sess.Cols),
		"--rows", fmt.Sprintf("%d", sess.Rows),
	}
	if opts.Mode == TerminalModeCompatibleAttach {
		args = append(args, "--compatible-attach")
	}
	cmd := exec.Command(exe, args...)
	cmd.Dir = sess.WorkingDirectory
	cmd.Env = env
	configureSessionHostCmd(cmd)
	if err := cmd.Start(); err != nil {
		return err
	}
	m.mu.Lock()
	m.hosts[sess.ID] = cmd
	m.mu.Unlock()
	_ = m.store.UpdateSessionHost(sess.ID, cmd.Process.Pid, sess.SocketPath)
	go m.watchSessionHost(sess.ID, cmd)
	return nil
}

// watchSessionHost reaps the host process so exited sessions do not linger as zombies.
func (m *TerminalManager) watchSessionHost(sessionID string, cmd *exec.Cmd) {
	_ = cmd.Wait()
	m.mu.Lock()
	if m.hosts[sessionID] == cmd {
		delete(m.hosts, sessionID)
	}
	m.mu.Unlock()
	cur, err := m.store.GetSession(sessionID)
	if err != nil {
		return
	}
	switch cur.State {
	case TerminalStateCreated, TerminalStateStarting, TerminalStateRunning:
		_ = m.store.UpdateSessionState(sessionID, TerminalStateFailed, "session host exited", nil)
	}
}

// adoptedHostPollEvery is how often the Core checks that a host it adopted (not its child) is still alive.
var adoptedHostPollEvery = 5 * time.Second

// recoverSessionHosts runs when a Core starts. A session host outlives the Core that spawned it (own process
// group), so a host whose pid is alive and whose socket answers the session token is adopted back and the
// terminal stays open. Any other host left by a previous Core is terminated and its session marked failed.
func (m *TerminalManager) recoverSessionHosts() {
	rows, err := m.store.db.Query(`SELECT id, host_pid FROM terminal_sessions WHERE state IN ('RUNNING','STARTING')`)
	if err != nil {
		return
	}
	defer rows.Close()
	type row struct {
		id  string
		pid int
	}
	var stale []row
	for rows.Next() {
		var r row
		if err := rows.Scan(&r.id, &r.pid); err != nil {
			return
		}
		stale = append(stale, r)
	}
	for _, r := range stale {
		m.mu.Lock()
		_, tracked := m.hosts[r.id]
		m.mu.Unlock()
		if tracked || r.pid <= 1 {
			continue
		}
		if processAlive(r.pid) && m.hostAnswers(r.id) {
			go m.watchAdoptedHost(r.id, r.pid)
			continue
		}
		if processAlive(r.pid) {
			terminateProcess(r.pid)
		}
		removeStaleSessionSocket(m.home, r.id)
		_ = m.store.UpdateSessionState(r.id, TerminalStateFailed, "session host restarted", nil)
	}
}

// hostAnswers proves the process behind a session is its host, not a recycled pid: it holds the IPC token.
func (m *TerminalManager) hostAnswers(sessionID string) bool {
	sock, tok, err := m.sessionIPC(sessionID)
	if err != nil || sock == "" || tok == "" {
		return false
	}
	resp, err := DialSessionHost(sock, tok, terminalIPCRequest{Op: "ping"})
	return err == nil && resp.OK
}

// watchAdoptedHost stands in for cmd.Wait on a host this Core did not spawn: once the process is gone, a
// session still marked live is failed, as watchSessionHost does for children.
func (m *TerminalManager) watchAdoptedHost(sessionID string, pid int) {
	ticker := time.NewTicker(adoptedHostPollEvery)
	defer ticker.Stop()
	for range ticker.C {
		cur, err := m.store.GetSession(sessionID)
		if err != nil {
			return
		}
		switch cur.State {
		case TerminalStateCreated, TerminalStateStarting, TerminalStateRunning:
		default:
			return
		}
		if !processAlive(pid) {
			_ = m.store.UpdateSessionState(sessionID, TerminalStateFailed, "session host exited", nil)
			removeStaleSessionSocket(m.home, sessionID)
			return
		}
	}
}

func (m *TerminalManager) GetSession(id string) (TerminalSession, error) {
	return m.store.GetSession(id)
}

func (m *TerminalManager) ListSessions(limit int) ([]TerminalSession, error) {
	if limit <= 0 {
		limit = 50
	}
	rows, err := m.store.db.Query(`SELECT id FROM terminal_sessions ORDER BY updated_at DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	out := make([]TerminalSession, 0, len(ids))
	for _, id := range ids {
		s, err := m.store.GetSession(id)
		if err != nil {
			continue
		}
		if s.State == TerminalStateRunning && !sessionHostSocketExists(m.home, s.ID) {
			_ = m.store.UpdateSessionState(s.ID, TerminalStateFailed, "session host unavailable", nil)
			s.State = TerminalStateFailed
			s.ErrorMessage = "session host unavailable"
		}
		out = append(out, s)
	}
	return out, nil
}

func canonicalTerminalWorkingDirectory(dir string) (string, error) {
	abs, err := validateTerminalWorkingDirectory(dir)
	if err != nil {
		return "", err
	}
	resolved, err := filepath.EvalSymlinks(abs)
	if err != nil {
		resolved = abs
	}
	if strings.Contains(resolved, "..") {
		return "", errors.New(errMsgPathTraversal)
	}
	return resolved, nil
}

// logicalTerminalWorkingDirectory keeps symlinked circle session paths for shorter shell prompts (PS1 \\w).
func logicalTerminalWorkingDirectory(dir string) (string, error) {
	abs, err := validateTerminalWorkingDirectory(dir)
	if err != nil {
		return "", err
	}
	if strings.Contains(abs, "..") {
		return "", errors.New(errMsgPathTraversal)
	}
	return abs, nil
}

func validateTerminalWorkingDirectory(dir string) (string, error) {
	if strings.TrimSpace(dir) == "" {
		dir = "."
	}
	abs, err := hookFilepathAbs(dir)
	if err != nil {
		return "", err
	}
	abs = filepath.Clean(abs)
	if st, err := os.Stat(abs); err != nil {
		return "", err
	} else if !st.IsDir() {
		return "", errors.New("not a directory")
	}
	return abs, nil
}

func terminalEnvKeyBlocked(key, upper string) bool {
	if terminalEnvExactBlock[upper] {
		return true
	}
	for _, p := range terminalEnvPrefixBlock {
		if strings.HasPrefix(upper, p) {
			return true
		}
	}
	for _, s := range terminalEnvSuffixBlock {
		if strings.HasSuffix(upper, s) {
			return true
		}
	}
	return strings.Contains(strings.ToLower(key), "private_key")
}

func filterTerminalEnv(env []string) []string {
	out := make([]string, 0, len(env))
	for _, kv := range env {
		eq := strings.IndexByte(kv, '=')
		if eq <= 0 {
			continue
		}
		key := kv[:eq]
		if terminalEnvKeyBlocked(key, strings.ToUpper(key)) {
			continue
		}
		out = append(out, kv)
	}
	return out
}

func (s *TerminalStore) insertParticipant(p TerminalParticipant) error {
	caps, _ := jsonMarshalCaps(p.Capabilities)
	_, err := s.db.Exec(`INSERT INTO terminal_participants (id, session_id, device_id, user_id, role, capabilities_json, connected, last_seen_at, created_at)
 VALUES (?,?,?,?,?,?,?,?,?)`,
		p.ID, p.SessionID, p.DeviceID, p.UserID, p.Role, caps, boolInt(p.Connected), p.LastSeenAt, p.CreatedAt)
	return err
}

func jsonMarshalCaps(caps []TerminalCapability) (string, error) {
	if len(caps) == 0 {
		return "[]", nil
	}
	b, err := json.Marshal(caps)
	return string(b), err
}

var (
	terminalMgrMu  sync.Mutex
	terminalMgrMap = map[string]*TerminalManager{}
)

func resetTerminalManagersForTests() {
	terminalMgrMu.Lock()
	terminalMgrMap = map[string]*TerminalManager{}
	terminalMgrMu.Unlock()
}

func getTerminalManager(home string) (*TerminalManager, error) {
	terminalMgrMu.Lock()
	defer terminalMgrMu.Unlock()
	if m, ok := terminalMgrMap[home]; ok {
		return m, nil
	}
	m, err := newTerminalManager(home)
	if err != nil {
		return nil, err
	}
	m.recoverSessionHosts()
	terminalMgrMap[home] = m
	return m, nil
}

func (m *TerminalManager) sessionIPC(sessionID string) (socketPath, token string, err error) {
	sess, err := m.GetSession(sessionID)
	if err != nil {
		return "", "", err
	}
	return sess.SocketPath, sess.IPCToken, nil
}

func (m *TerminalManager) AttachSession(sessionID, deviceID string, caps []TerminalCapability) (string, error) {
	sess, err := m.GetSession(sessionID)
	if err != nil {
		return "", err
	}
	now := terminalNow()
	role := "observer"
	if deviceID == sess.OwnerDeviceID {
		role = "owner"
	}
	p := TerminalParticipant{
		ID: newTerminalParticipantID(), SessionID: sessionID, DeviceID: deviceID,
		Role: role, Capabilities: caps, Connected: true, LastSeenAt: now, CreatedAt: now,
	}
	if err := m.store.UpsertParticipantByDevice(p); err != nil {
		return "", err
	}
	if len(caps) == 0 {
		caps = []TerminalCapability{TerminalCapObserve}
	}
	return issueTerminalStreamToken(m.home, sessionID, deviceID, caps, terminalStreamTokenTTL)
}

func (m *TerminalManager) DetachSession(sessionID, deviceID string) error {
	parts, err := m.store.ListParticipants(sessionID)
	if err != nil {
		return err
	}
	for _, p := range parts {
		if p.DeviceID == deviceID {
			_ = m.store.RemoveParticipant(sessionID, p.ID)
			unregisterTerminalStream(sessionID, deviceID)
		}
	}
	return nil
}

// RemoveParticipantAccess removes a participant and revokes its device's share, so it cannot reconnect.
// The owner and host devices keep their access.
func (m *TerminalManager) RemoveParticipantAccess(sessionID, participantID string) error {
	sess, err := m.GetSession(sessionID)
	if err != nil {
		return err
	}
	parts, err := m.store.ListParticipants(sessionID)
	if err != nil {
		return err
	}
	if err := m.store.RemoveParticipant(sessionID, participantID); err != nil {
		return err
	}
	for _, p := range parts {
		if p.ID == participantID && p.DeviceID != sess.OwnerDeviceID && p.DeviceID != sess.HostDeviceID {
			revokeTerminalSessionAccess(m.store, sessionID, p.DeviceID, "participant_remove")
		}
	}
	return nil
}

func (m *TerminalManager) TerminateSession(sessionID, actorDeviceID string) error {
	s, err := m.GetSession(sessionID)
	if err != nil {
		return err
	}
	if actorDeviceID != s.OwnerDeviceID && actorDeviceID != s.HostDeviceID {
		return fmt.Errorf("%s", TerminalErrAccessDenied)
	}
	sock, tok, _ := m.sessionIPC(sessionID)
	if sock != "" {
		_, _ = DialSessionHost(sock, tok, terminalIPCRequest{Op: "terminate"})
	}
	_ = m.store.UpdateSessionState(sessionID, TerminalStateTerminated, "", nil)
	m.mu.Lock()
	if cmd := m.hosts[sessionID]; cmd != nil && cmd.Process != nil {
		_ = cmd.Process.Kill()
		delete(m.hosts, sessionID)
	}
	m.mu.Unlock()
	removeStaleSessionSocket(m.home, sessionID)
	return nil
}

func (m *TerminalManager) PurgeSession(sessionID, actorDeviceID string) error {
	if err := m.TerminateSession(sessionID, actorDeviceID); err != nil {
		return err
	}
	return m.store.DeleteSession(sessionID)
}

func (m *TerminalManager) ResizeSession(sessionID, actorDeviceID, leaseID string, epoch int64, cols, rows int) error {
	ctrl := newTerminalControl(m.store, actorDeviceID)
	if err := ctrl.ValidateWrite(sessionID, actorDeviceID, leaseID, epoch); err != nil {
		return err
	}
	sock, tok, err := m.sessionIPC(sessionID)
	if err != nil {
		return err
	}
	body, _ := json.Marshal(terminalResizePayload{Cols: cols, Rows: rows})
	resp, err := DialSessionHost(sock, tok, terminalIPCRequest{
		Op: "resize", Data: body, ActorDeviceID: actorDeviceID, LeaseID: leaseID, Epoch: epoch,
	})
	if err != nil {
		return err
	}
	if !resp.OK {
		return errors.New(resp.Error)
	}
	return nil
}
