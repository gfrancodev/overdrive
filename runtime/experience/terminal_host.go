package main

import (
	"bufio"
	"encoding/json"
	"errors"
	"flag"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"
)

type sessionHostRuntime struct {
	home      string
	sessionID string
	store     *TerminalStore
	sess      TerminalSession
	pty       *ptyHandle
	screen    *ansiScreen
	ipcToken  string
	cols      int
	rows      int

	mu          sync.Mutex
	streamMu    sync.Mutex
	streamPeers []io.Writer // Etapa 3: P2P/WebSocket sinks; stub in etapa 1-2

	stopCh chan struct{}
}

func cmdSessionHost(args []string) error {
	fs := flag.NewFlagSet("session-host", flag.ContinueOnError)
	sessionID := fs.String("session-id", "", "terminal session id")
	homeFlag := fs.String("home", "", "overdrive home")
	cols := fs.Int("cols", 80, "terminal columns")
	rows := fs.Int("rows", 24, "terminal rows")
	compatible := fs.Bool("compatible-attach", false, "optional tmux attach mode")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if strings.TrimSpace(*sessionID) == "" {
		return errors.New("--session-id is required")
	}
	home, err := sessionHostHome(strings.TrimSpace(*homeFlag))
	if err != nil {
		return err
	}
	store, err := newTerminalStore(home)
	if err != nil {
		return err
	}
	sess, err := store.GetSession(*sessionID)
	if err != nil {
		return err
	}
	token, err := sessionHostIPCToken(sess)
	if err != nil {
		return err
	}
	rt := newSessionHostRuntime(home, store, sess, token, *cols, *rows)
	if err := rt.bootShell(*compatible); err != nil {
		return err
	}
	if err := rt.serveIPC(); err != nil && !errors.Is(err, io.EOF) {
		_ = store.UpdateSessionState(sess.ID, TerminalStateFailed, err.Error(), nil)
		return err
	}
	return nil
}

func sessionHostHome(homeFlag string) (string, error) {
	if homeFlag != "" {
		return homeFlag, nil
	}
	return ensureHome()
}

func sessionHostIPCToken(sess TerminalSession) (string, error) {
	token := strings.TrimSpace(os.Getenv("TERMINAL_IPC_TOKEN"))
	if token == "" {
		token = sess.IPCToken
	}
	if token == "" || token != sess.IPCToken {
		return "", errors.New("invalid session host token")
	}
	return token, nil
}

func newSessionHostRuntime(home string, store *TerminalStore, sess TerminalSession, token string, cols, rows int) *sessionHostRuntime {
	return &sessionHostRuntime{
		home: home, sessionID: sess.ID, store: store, sess: sess, ipcToken: token,
		cols: cols, rows: rows, screen: newANSIScreen(cols, rows), stopCh: make(chan struct{}),
	}
}

func (rt *sessionHostRuntime) bootShell(compatible bool) error {
	if compatible && rt.sess.Mode == TerminalModeCompatibleAttach {
		if err := rt.tryCompatibleAttach(); err != nil {
			_ = rt.store.audit(rt.sessionID, rt.sess.HostDeviceID, "compatible_attach_fallback", err.Error())
		}
	}
	if rt.pty != nil {
		return nil
	}
	if err := rt.startManagedShell(); err != nil {
		_ = rt.store.UpdateSessionState(rt.sessionID, TerminalStateFailed, err.Error(), nil)
		return err
	}
	return nil
}

func (rt *sessionHostRuntime) startManagedShell() error {
	cmd, shellArgs := rt.sess.Command, []string(nil)
	if strings.TrimSpace(cmd) == "" {
		cmd, shellArgs = defaultShellCommand()
	} else if len(shellArgs) == 0 && isInteractiveShell(cmd) {
		shellArgs = []string{"-i"}
	}
	env := filterTerminalEnv(os.Environ())
	pty, err := startPTY(cmd, shellArgs, rt.sess.WorkingDirectory, env, uint16(rt.cols), uint16(rt.rows))
	if err != nil {
		return err
	}
	rt.pty = pty
	_ = rt.store.UpdateSessionState(rt.sessionID, TerminalStateRunning, "", nil)
	go rt.pumpPTYOutput()
	go rt.waitProcess()
	return nil
}

func (rt *sessionHostRuntime) tryCompatibleAttach() error {
	if _, err := exec.LookPath("tmux"); err != nil {
		return err
	}
	pty, err := startPTY("tmux", []string{"attach"}, rt.sess.WorkingDirectory, filterTerminalEnv(os.Environ()), uint16(rt.cols), uint16(rt.rows))
	if err != nil {
		return err
	}
	rt.pty = pty
	_ = rt.store.UpdateSessionState(rt.sessionID, TerminalStateRunning, "", nil)
	go rt.pumpPTYOutput()
	go rt.waitProcess()
	return nil
}

func (rt *sessionHostRuntime) pumpPTYOutput() {
	buf := make([]byte, 32*1024)
	for {
		select {
		case <-rt.stopCh:
			return
		default:
		}
		n, err := rt.pty.Read(buf)
		if n > 0 {
			chunk := append([]byte(nil), buf[:n]...)
			_, _ = rt.store.RecordOutput(rt.sessionID, rt.sess.HostDeviceID, chunk, rt.screen)
			rt.broadcastOutput(chunk)
		}
		if err != nil {
			if err != io.EOF {
				_ = rt.store.audit(rt.sessionID, rt.sess.HostDeviceID, "pty_read_error", err.Error())
			}
			return
		}
	}
}

func (rt *sessionHostRuntime) broadcastOutput(chunk []byte) {
	rt.streamMu.Lock()
	peers := append([]io.Writer(nil), rt.streamPeers...)
	rt.streamMu.Unlock()
	for _, w := range peers {
		if fw, ok := w.(*terminalFramedWriter); ok {
			_, _ = fw.Write(chunk)
		} else {
			_, _ = w.Write(chunk)
		}
	}
}

type terminalFramedWriter struct {
	w   io.Writer
	seq int64
}

func (f *terminalFramedWriter) Write(p []byte) (int, error) {
	f.seq++
	frame, err := encodeTerminalFrame(terminalFrame{Type: terminalFrameOutput, Sequence: f.seq, Payload: p})
	if err != nil {
		return 0, err
	}
	if _, err := f.w.Write(frame); err != nil {
		return 0, err
	}
	return len(p), nil
}

func (rt *sessionHostRuntime) waitProcess() {
	code, err := rt.pty.Wait()
	exit := code
	var exitPtr *int
	if err == nil {
		exitPtr = &exit
	}
	state := TerminalStateExited
	if err != nil {
		state = TerminalStateFailed
	}
	_ = rt.store.UpdateSessionState(rt.sessionID, state, "", exitPtr)
	close(rt.stopCh)
}

func (rt *sessionHostRuntime) serveIPC() error {
	socketPath := rt.sess.SocketPath
	if socketPath == "" {
		socketPath = terminalSocketPath(rt.home, rt.sessionID)
	}
	_ = hookRemove(socketPath)
	ln, err := hookNetListen("unix", socketPath)
	if err != nil {
		return err
	}
	defer func() {
		_ = ln.Close()
		_ = hookRemove(socketPath)
	}()
	_ = hookChmod(socketPath, 0o600)
	done := make(chan struct{})
	go func() {
		<-rt.stopCh
		close(done)
		_ = ln.Close()
	}()
	for {
		conn, err := ln.Accept()
		if err != nil {
			select {
			case <-done:
				return nil
			default:
				return err
			}
		}
		go rt.handleIPCConn(conn)
	}
}

func (rt *sessionHostRuntime) handleIPCConn(conn net.Conn) {
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(30 * time.Minute))
	sc := bufio.NewScanner(conn)
	sc.Buffer(make([]byte, 256*1024), 1024*1024)
	if !sc.Scan() {
		return
	}
	var first terminalIPCRequest
	if err := json.Unmarshal(sc.Bytes(), &first); err != nil {
		return
	}
	if first.Op == "attach_stream" {
		rt.serveStreamAttach(conn, first)
		return
	}
	enc := json.NewEncoder(conn)
	resp := rt.dispatchIPC(first)
	if err := enc.Encode(resp); err != nil {
		return
	}
	for sc.Scan() {
		var req terminalIPCRequest
		if err := json.Unmarshal(sc.Bytes(), &req); err != nil {
			_ = enc.Encode(terminalIPCResponse{OK: false, Error: "invalid json"})
			continue
		}
		resp := rt.dispatchIPC(req)
		if err := enc.Encode(resp); err != nil {
			return
		}
	}
}

func (rt *sessionHostRuntime) replayStreamAttachHistory(conn net.Conn, fw *terminalFramedWriter, events []SessionEvent) {
	for _, ev := range events {
		switch ev.Kind {
		case TerminalEventOutput:
			_, _ = fw.Write(ev.Raw)
		case TerminalEventSnapshot:
			frame, err := encodeTerminalFrame(terminalFrame{Type: terminalFrameSnapshot, Sequence: ev.Sequence, Payload: ev.Raw})
			if err == nil {
				_, _ = conn.Write(frame)
			}
		}
	}
}

func (rt *sessionHostRuntime) unregisterStreamPeer(fw io.Writer) {
	rt.streamMu.Lock()
	out := rt.streamPeers[:0]
	for _, w := range rt.streamPeers {
		if w != fw {
			out = append(out, w)
		}
	}
	rt.streamPeers = out
	rt.streamMu.Unlock()
}

func (rt *sessionHostRuntime) serveStreamAttach(conn net.Conn, req terminalIPCRequest) {
	if req.Token != rt.ipcToken {
		return
	}
	// Long-lived output stream: the request/response IPC deadline would silently cut output after 30 minutes.
	_ = conn.SetDeadline(time.Time{})
	fw := &terminalFramedWriter{w: conn}
	events, _ := rt.store.ReplayRecentOutput(rt.sessionID, 2000)
	rt.replayStreamAttachHistory(conn, fw, events)
	rt.streamMu.Lock()
	rt.streamPeers = append(rt.streamPeers, fw)
	rt.streamMu.Unlock()
	defer rt.unregisterStreamPeer(fw)
	buf := make([]byte, 4096)
	for {
		select {
		case <-rt.stopCh:
			return
		default:
		}
		if _, err := conn.Read(buf); err != nil {
			return
		}
	}
}

func (rt *sessionHostRuntime) ipcSnapshot() terminalIPCResponse {
	seq, err := rt.store.NextSequence(rt.sessionID)
	if err != nil {
		return terminalIPCResponse{OK: false, Error: err.Error()}
	}
	snap, err := rt.store.SaveSnapshot(rt.sessionID, seq, rt.screen)
	if err != nil {
		return terminalIPCResponse{OK: false, Error: err.Error()}
	}
	b, _ := json.Marshal(snap)
	return terminalIPCResponse{OK: true, Data: b}
}

func (rt *sessionHostRuntime) ipcInput(req terminalIPCRequest) terminalIPCResponse {
	if err := rt.validateIPCLlease(req); err != nil {
		return terminalIPCResponse{OK: false, Error: err.Error(), Code: string(TerminalErrInvalidLease)}
	}
	sess, err := rt.store.GetSession(rt.sessionID)
	if err != nil {
		return terminalIPCResponse{OK: false, Error: err.Error()}
	}
	if sess.PrivateMode {
		return terminalIPCResponse{OK: false, Error: "private mode active", Code: string(TerminalErrPrivateMode)}
	}
	var payload terminalInputPayload
	if err := json.Unmarshal(req.Data, &payload); err != nil {
		return terminalIPCResponse{OK: false, Error: "invalid input payload"}
	}
	data := []byte(payload.Data)
	if len(data) == 0 {
		return terminalIPCResponse{OK: false, Error: "empty input"}
	}
	if _, err := rt.pty.Write(data); err != nil {
		return terminalIPCResponse{OK: false, Error: err.Error()}
	}
	_, _ = rt.store.RecordInput(rt.sessionID, sess.HostDeviceID, payload.InputID, data)
	return terminalIPCResponse{OK: true}
}

func (rt *sessionHostRuntime) ipcResize(req terminalIPCRequest) terminalIPCResponse {
	if err := rt.validateIPCLlease(req); err != nil {
		return terminalIPCResponse{OK: false, Error: err.Error(), Code: string(TerminalErrInvalidLease)}
	}
	var payload terminalResizePayload
	if err := json.Unmarshal(req.Data, &payload); err != nil {
		return terminalIPCResponse{OK: false, Error: "invalid resize payload"}
	}
	if payload.Cols <= 0 || payload.Rows <= 0 {
		return terminalIPCResponse{OK: false, Error: "invalid size"}
	}
	rt.cols, rt.rows = payload.Cols, payload.Rows
	rt.screen = newANSIScreen(payload.Cols, payload.Rows)
	if err := rt.pty.Resize(uint16(payload.Cols), uint16(payload.Rows)); err != nil {
		return terminalIPCResponse{OK: false, Error: err.Error()}
	}
	return terminalIPCResponse{OK: true}
}

func (rt *sessionHostRuntime) ipcPrivate(req terminalIPCRequest) terminalIPCResponse {
	var body struct {
		Enabled bool `json:"enabled"`
	}
	_ = json.Unmarshal(req.Data, &body)
	if err := rt.store.SetPrivateMode(rt.sessionID, body.Enabled, rt.sess.OwnerDeviceID); err != nil {
		return terminalIPCResponse{OK: false, Error: err.Error()}
	}
	return terminalIPCResponse{OK: true}
}

func (rt *sessionHostRuntime) ipcReplay(req terminalIPCRequest) terminalIPCResponse {
	var body struct {
		FromSeq int64 `json:"from_seq"`
		ToSeq   int64 `json:"to_seq"`
		Limit   int   `json:"limit"`
	}
	_ = json.Unmarshal(req.Data, &body)
	events, err := rt.store.ReplayEvents(rt.sessionID, body.FromSeq, body.ToSeq, body.Limit)
	if err != nil {
		return terminalIPCResponse{OK: false, Error: err.Error()}
	}
	b, _ := json.Marshal(events)
	return terminalIPCResponse{OK: true, Data: b}
}

func (rt *sessionHostRuntime) ipcTerminate() terminalIPCResponse {
	_ = rt.store.UpdateSessionState(rt.sessionID, TerminalStateStopping, "", nil)
	_ = rt.pty.Signal(syscall.SIGTERM)
	time.AfterFunc(3*time.Second, func() { _ = rt.pty.Signal(syscall.SIGKILL) })
	return terminalIPCResponse{OK: true}
}

func (rt *sessionHostRuntime) dispatchIPC(req terminalIPCRequest) terminalIPCResponse {
	if req.Token != rt.ipcToken {
		return terminalIPCResponse{OK: false, Error: "access denied", Code: string(TerminalErrAccessDenied)}
	}
	switch req.Op {
	case "ping":
		return terminalIPCResponse{OK: true}
	case "snapshot":
		return rt.ipcSnapshot()
	case "input":
		return rt.ipcInput(req)
	case "resize":
		return rt.ipcResize(req)
	case "private":
		return rt.ipcPrivate(req)
	case "replay":
		return rt.ipcReplay(req)
	case "terminate":
		return rt.ipcTerminate()
	default:
		return terminalIPCResponse{OK: false, Error: "unknown op: " + req.Op}
	}
}

func (rt *sessionHostRuntime) validateIPCLlease(req terminalIPCRequest) error {
	if req.ActorDeviceID == "" {
		return nil
	}
	ctrl := newTerminalControl(rt.store, req.ActorDeviceID)
	return ctrl.ValidateWrite(rt.sessionID, req.ActorDeviceID, req.LeaseID, req.Epoch)
}

// DialSessionHost connects to a supervised session IPC socket (loopback clients).
func DialSessionHost(socketPath, token string, req terminalIPCRequest) (terminalIPCResponse, error) {
	req.Token = token
	conn, err := hookNetDial("unix", socketPath, 5*time.Second)
	if err != nil {
		return terminalIPCResponse{}, err
	}
	defer conn.Close()
	if _, err := conn.Write(append(marshalTerminalJSON(req), '\n')); err != nil {
		return terminalIPCResponse{}, err
	}
	var resp terminalIPCResponse
	dec := json.NewDecoder(conn)
	if err := dec.Decode(&resp); err != nil {
		return terminalIPCResponse{}, err
	}
	return resp, nil
}

func marshalTerminalJSON(v any) []byte {
	b, err := json.Marshal(v)
	if err != nil {
		return []byte("{}")
	}
	return b
}

func sessionHostSocketExists(home, sessionID string) bool {
	path := terminalSocketPath(home, sessionID)
	st, err := os.Stat(path)
	return err == nil && st.Mode()&os.ModeSocket != 0
}

func removeStaleSessionSocket(home, sessionID string) {
	path := terminalSocketPath(home, sessionID)
	if st, err := os.Stat(path); err == nil && st.Mode()&os.ModeSocket != 0 {
		_ = hookRemove(path)
	}
}

// validateSessionHostLayout ensures session paths stay under share/sessions.
func validateSessionHostLayout(home, sessionID string) error {
	base, err := hookFilepathAbs(terminalSessionsDir(home))
	if err != nil {
		return err
	}
	sock := terminalSocketPath(home, sessionID)
	abs, err := hookFilepathAbs(sock)
	if err != nil {
		return err
	}
	if !strings.HasPrefix(abs, base+string(filepath.Separator)) && abs != base {
		return errors.New("session socket outside sessions dir")
	}
	return nil
}
