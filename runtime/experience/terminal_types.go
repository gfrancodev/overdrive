package main

import (
	"encoding/json"
	"time"
)

const (
	terminalProtocolVersion = "1.0"
	terminalDBFile            = "terminal.db"
)

// TerminalSessionState is the lifecycle state of a supervised terminal session.
type TerminalSessionState string

const (
	TerminalStateCreated    TerminalSessionState = "CREATED"
	TerminalStateStarting   TerminalSessionState = "STARTING"
	TerminalStateRunning    TerminalSessionState = "RUNNING"
	TerminalStateDetached   TerminalSessionState = "DETACHED"
	TerminalStateSuspended  TerminalSessionState = "SUSPENDED"
	TerminalStateStopping   TerminalSessionState = "STOPPING"
	TerminalStateExited     TerminalSessionState = "EXITED"
	TerminalStateFailed     TerminalSessionState = "FAILED"
	TerminalStateTerminated TerminalSessionState = "TERMINATED"
	TerminalStateLost       TerminalSessionState = "LOST"
)

// TerminalSessionMode selects how the host attaches to a harness.
type TerminalSessionMode string

const (
	TerminalModeManaged          TerminalSessionMode = "managed"
	TerminalModeCompatibleAttach TerminalSessionMode = "compatible_attach"
	TerminalModeContextOnly      TerminalSessionMode = "context_only"
)

// TerminalCapability advertises what a client or device can do with live sessions.
type TerminalCapability string

const (
	TerminalCapObserve TerminalCapability = "observe"
	TerminalCapInput   TerminalCapability = "input"
	TerminalCapControl TerminalCapability = "control"
	TerminalCapHost    TerminalCapability = "host"
	TerminalCapReplay  TerminalCapability = "replay"
)

// TerminalErrorCode is returned to clients for structured errors.
type TerminalErrorCode string

const (
	TerminalErrAccessDenied        TerminalErrorCode = "TERMINAL_ACCESS_DENIED"
	TerminalErrInvalidLease        TerminalErrorCode = "TERMINAL_INVALID_CONTROL_LEASE"
	TerminalErrSessionNotFound     TerminalErrorCode = "TERMINAL_SESSION_NOT_FOUND"
	TerminalErrInvalidState        TerminalErrorCode = "TERMINAL_INVALID_STATE"
	TerminalErrWorkingDirectory    TerminalErrorCode = "TERMINAL_INVALID_WORKING_DIRECTORY"
	TerminalErrPrivateMode         TerminalErrorCode = "TERMINAL_PRIVATE_MODE_ACTIVE"
	TerminalErrHostUnavailable     TerminalErrorCode = "TERMINAL_HOST_UNAVAILABLE"
	TerminalErrReplayInputRejected TerminalErrorCode = "TERMINAL_REPLAY_INPUT_REJECTED"
)

// TerminalEventKind classifies persisted session events.
type TerminalEventKind string

const (
	TerminalEventOutput       TerminalEventKind = "OUTPUT"
	TerminalEventInput        TerminalEventKind = "INPUT"
	TerminalEventResize       TerminalEventKind = "RESIZE"
	TerminalEventSnapshot     TerminalEventKind = "SNAPSHOT"
	TerminalEventState        TerminalEventKind = "STATE"
	TerminalEventPrivate      TerminalEventKind = "PRIVATE_MODE"
	TerminalEventRecording    TerminalEventKind = "RECORDING"
	TerminalEventGap          TerminalEventKind = "GAP"
	TerminalEventAudit        TerminalEventKind = "AUDIT"
	TerminalEventParticipant  TerminalEventKind = "PARTICIPANT"
	TerminalEventControlLease TerminalEventKind = "CONTROL_LEASE"
)

// TerminalSession is the persisted metadata for a live terminal session.
type TerminalSession struct {
	ID               string               `json:"id"`
	Name             string               `json:"name"`
	State            TerminalSessionState `json:"state"`
	Mode             TerminalSessionMode  `json:"mode"`
	OwnerDeviceID    string               `json:"owner_device_id"`
	HostDeviceID     string               `json:"host_device_id"`
	WorkingDirectory string               `json:"working_directory"`
	Command          string               `json:"command"`
	HarnessID        string               `json:"harness_id,omitempty"`
	ProjectID        string               `json:"project_id,omitempty"`
	CircleID         string               `json:"circle_id,omitempty"`
	Cols             int                  `json:"cols"`
	Rows             int                  `json:"rows"`
	PrivateMode      bool                 `json:"private_mode"`
	RecordingPaused  bool                 `json:"recording_paused"`
	LastSequence     int64                `json:"last_sequence"`
	HostPID          int                  `json:"host_pid,omitempty"`
	SocketPath       string               `json:"socket_path,omitempty"`
	IPCToken         string               `json:"-"`
	CreatedAt        string               `json:"created_at"`
	UpdatedAt        string               `json:"updated_at"`
	ExitCode         *int                 `json:"exit_code,omitempty"`
	ErrorMessage     string               `json:"error_message,omitempty"`
}

// TerminalParticipant tracks viewers/controllers for a session.
type TerminalParticipant struct {
	ID             string               `json:"id"`
	SessionID      string               `json:"session_id"`
	DeviceID       string               `json:"device_id"`
	UserID         string               `json:"user_id,omitempty"`
	Role           string               `json:"role"`
	Capabilities   []TerminalCapability `json:"capabilities"`
	Connected      bool                 `json:"connected"`
	LastSeenAt     string               `json:"last_seen_at"`
	CreatedAt      string               `json:"created_at"`
}

// ControlLease grants turn-based write access to a session (epoch CAS in later etapa).
type ControlLease struct {
	ID             string `json:"id"`
	SessionID      string `json:"session_id"`
	HolderDeviceID string `json:"holder_device_id"`
	Epoch          int64  `json:"epoch"`
	GrantedAt      string `json:"granted_at"`
	ExpiresAt      string `json:"expires_at"`
	Revoked        bool   `json:"revoked"`
}

// SessionEvent is an ordered, replayable record (output chunks, state, etc.).
type SessionEvent struct {
	ID        int64                `json:"id"`
	SessionID string               `json:"session_id"`
	Sequence  int64                `json:"sequence"`
	Kind      TerminalEventKind    `json:"kind"`
	Timestamp string               `json:"timestamp"`
	ActorID   string               `json:"actor_id,omitempty"`
	Payload   json.RawMessage      `json:"payload,omitempty"`
	Raw       []byte               `json:"-"`
}

// TerminalRecordingSegment indexes a span of output for retention/replay.
type TerminalRecordingSegment struct {
	ID        string `json:"id"`
	SessionID string `json:"session_id"`
	StartSeq  int64  `json:"start_seq"`
	EndSeq    int64  `json:"end_seq"`
	StartedAt string `json:"started_at"`
	EndedAt   string `json:"ended_at"`
	Bytes     int64  `json:"bytes"`
	Paused    bool   `json:"paused"`
}

// TerminalSnapshot captures emulated screen state for late joiners / GAP recovery.
type TerminalSnapshot struct {
	ID        string `json:"id"`
	SessionID string `json:"session_id"`
	Sequence  int64  `json:"sequence"`
	Cols      int    `json:"cols"`
	Rows      int    `json:"rows"`
	CreatedAt string `json:"created_at"`
	// Payload is JSON-encoded ansiScreenSnapshot or raw ANSI restore sequence.
	Payload []byte `json:"payload"`
}

// TerminalShare links a session to a circle/device share invitation (Etapa 3+).
type TerminalShare struct {
	ID         string `json:"id"`
	SessionID  string `json:"session_id"`
	TargetID   string `json:"target_id"`
	Permission string `json:"permission"`
	CreatedAt  string `json:"created_at"`
	ExpiresAt  string `json:"expires_at,omitempty"`
}

// TerminalContextLink binds chat/context references to session sequences.
type TerminalContextLink struct {
	ID        string `json:"id"`
	SessionID string `json:"session_id"`
	LinkKind  string `json:"link_kind"`
	RefID     string `json:"ref_id"`
	StartSeq  int64  `json:"start_seq"`
	EndSeq    int64  `json:"end_seq"`
	CreatedAt string `json:"created_at"`
}

// TerminalAuditEvent records security-relevant actions.
type TerminalAuditEvent struct {
	ID        int64  `json:"id"`
	SessionID string `json:"session_id"`
	ActorID   string `json:"actor_id"`
	Action    string `json:"action"`
	Detail    string `json:"detail"`
	CreatedAt string `json:"created_at"`
}

// terminalIPCRequest is the JSON envelope for session-host loopback control.
type terminalIPCRequest struct {
	Op            string          `json:"op"`
	Token         string          `json:"token"`
	Data          json.RawMessage `json:"data,omitempty"`
	ActorDeviceID string          `json:"actor_device_id,omitempty"`
	LeaseID       string          `json:"lease_id,omitempty"`
	Epoch         int64           `json:"epoch,omitempty"`
}

type terminalIPCResponse struct {
	OK    bool            `json:"ok"`
	Error string          `json:"error,omitempty"`
	Code  string          `json:"code,omitempty"`
	Data  json.RawMessage `json:"data,omitempty"`
}

type terminalInputPayload struct {
	Data    string `json:"data"`
	InputID string `json:"input_id,omitempty"`
}

type terminalResizePayload struct {
	Cols int `json:"cols"`
	Rows int `json:"rows"`
}

func terminalNow() string {
	return time.Now().UTC().Format(time.RFC3339)
}

func newTerminalSessionID() string {
	return "ts_" + randomToken(24)
}

func newTerminalParticipantID() string {
	return "tp_" + randomToken(16)
}

func newTerminalLeaseID() string {
	return "tl_" + randomToken(16)
}

func newTerminalSnapshotID() string {
	return "snap_" + randomToken(16)
}

func newTerminalSegmentID() string {
	return "seg_" + randomToken(16)
}

func terminalSessionsDir(home string) string {
	return shareDir(home) + "/sessions"
}

func terminalSocketPath(home, sessionID string) string {
	return terminalSessionsDir(home) + "/" + sessionID + ".sock"
}

func terminalDBPath(home string) string {
	return shareDir(home) + "/" + terminalDBFile
}
