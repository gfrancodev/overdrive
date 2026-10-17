package main

import (
	"crypto/ed25519"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"
)

const terminalHTTPBodyMax = 512 * 1024

func requireTerminalMethod(w http.ResponseWriter, r *http.Request, method string) bool {
	if r.Method == method {
		return true
	}
	http.Error(w, methodNotAllowedMsg, http.StatusMethodNotAllowed)
	return false
}

func registerTerminalAPIRoutes(mux *http.ServeMux, home string) {
	mux.HandleFunc("/api/v1/terminal/", func(w http.ResponseWriter, r *http.Request) {
		if rejectRemoteCoreAdmin(w, r) {
			return
		}
		serveTerminalAPI(w, r, home)
	})
}

func serveTerminalAPI(w http.ResponseWriter, r *http.Request, home string) {
	path := strings.TrimPrefix(r.URL.Path, "/api/v1/terminal/")
	path = strings.Trim(path, "/")
	parts := strings.Split(path, "/")
	if len(parts) == 0 || parts[0] == "" {
		http.NotFound(w, r)
		return
	}
	mgr, err := getTerminalManager(home)
	if err != nil {
		writeTerminalError(w, err)
		return
	}
	id, priv, err := loadOrCreateIdentity(home)
	if err != nil {
		writeTerminalError(w, err)
		return
	}
	switch parts[0] {
	case "sessions":
		serveTerminalSessions(w, r, home, mgr, id, priv, parts[1:])
	case "remote":
		serveTerminalRemote(w, r, home, id, priv, parts[1:])
	default:
		http.NotFound(w, r)
	}
}

func serveTerminalSessionsCollection(w http.ResponseWriter, r *http.Request, home string, mgr *TerminalManager, id DeviceIdentity) {
	switch r.Method {
	case http.MethodPost:
		serveTerminalCreate(w, r, home, mgr, id)
	case http.MethodGet:
		list, err := mgr.ListSessions(100)
		if err != nil {
			writeTerminalError(w, err)
			return
		}
		if list == nil {
			list = []TerminalSession{}
		}
		writeTerminalJSON(w, http.StatusOK, map[string]any{"sessions": list})
	default:
		http.Error(w, methodNotAllowedMsg, http.StatusMethodNotAllowed)
	}
}

func serveTerminalSessionResource(w http.ResponseWriter, r *http.Request, mgr *TerminalManager, sessionID, deviceID string) {
	switch r.Method {
	case http.MethodGet:
		sess, err := mgr.GetSession(sessionID)
		if err != nil {
			writeTerminalError(w, err)
			return
		}
		writeTerminalJSON(w, http.StatusOK, terminalSessionPayload(mgr, sess, deviceID))
	case http.MethodDelete:
		if err := mgr.PurgeSession(sessionID, deviceID); err != nil {
			writeTerminalError(w, err)
			return
		}
		writeTerminalJSON(w, http.StatusOK, map[string]any{"ok": true})
	default:
		http.Error(w, methodNotAllowedMsg, http.StatusMethodNotAllowed)
	}
}

type terminalSessionSubrouteCtx struct {
	home      string
	mgr       *TerminalManager
	id        DeviceIdentity
	priv      ed25519.PrivateKey
	sessionID string
	parts     []string
}

func serveTerminalSessionSubroute(w http.ResponseWriter, r *http.Request, ctx terminalSessionSubrouteCtx) {
	if serveTerminalSessionLifecycleSubroute(w, r, ctx) {
		return
	}
	if serveTerminalSessionMetaSubroute(w, r, ctx) {
		return
	}
	http.NotFound(w, r)
}

func serveTerminalSessionLifecycleSubroute(w http.ResponseWriter, r *http.Request, ctx terminalSessionSubrouteCtx) bool {
	switch ctx.parts[1] {
	case "stream":
		if !requireTerminalMethod(w, r, http.MethodGet) {
			return true
		}
		serveTerminalWebSocket(w, r, ctx.home, ctx.sessionID)
	case "attach":
		if !requireTerminalMethod(w, r, http.MethodPost) {
			return true
		}
		serveTerminalAttach(w, r, ctx.mgr, ctx.sessionID, ctx.id.DeviceID)
	case "detach":
		if !requireTerminalMethod(w, r, http.MethodPost) {
			return true
		}
		_ = ctx.mgr.DetachSession(ctx.sessionID, ctx.id.DeviceID)
		writeTerminalJSON(w, http.StatusOK, map[string]any{"ok": true})
	case "terminate":
		if !requireTerminalMethod(w, r, http.MethodPost) {
			return true
		}
		if err := ctx.mgr.TerminateSession(ctx.sessionID, ctx.id.DeviceID); err != nil {
			writeTerminalError(w, err)
			return true
		}
		writeTerminalJSON(w, http.StatusOK, map[string]any{"ok": true})
	case "resize":
		if !requireTerminalMethod(w, r, http.MethodPost) {
			return true
		}
		serveTerminalResize(w, r, ctx.mgr, ctx.sessionID, ctx.id.DeviceID)
	default:
		return false
	}
	return true
}

func serveTerminalSessionMetaSubroute(w http.ResponseWriter, r *http.Request, ctx terminalSessionSubrouteCtx) bool {
	switch ctx.parts[1] {
	case "participants":
		participantID := ""
		if len(ctx.parts) > 2 {
			participantID = ctx.parts[2]
		}
		serveTerminalParticipants(w, r, ctx.mgr, ctx.sessionID, participantID)
	case "invite":
		if r.Method != http.MethodPost {
			http.Error(w, methodNotAllowedMsg, http.StatusMethodNotAllowed)
			return true
		}
		serveTerminalInvite(w, r, ctx.home, ctx.mgr, ctx.sessionID, ctx.id, ctx.priv)
	case "events":
		if r.Method != http.MethodGet {
			http.Error(w, methodNotAllowedMsg, http.StatusMethodNotAllowed)
			return true
		}
		serveTerminalEvents(w, r, ctx.mgr, ctx.sessionID)
	case "snapshots":
		if r.Method != http.MethodGet {
			http.Error(w, methodNotAllowedMsg, http.StatusMethodNotAllowed)
			return true
		}
		snaps, err := ctx.mgr.store.ListSnapshots(ctx.sessionID, 50)
		if err != nil {
			writeTerminalError(w, err)
			return true
		}
		writeTerminalJSON(w, http.StatusOK, map[string]any{"snapshots": snaps})
	case "recording":
		serveTerminalRecording(w, r, ctx.mgr, ctx.sessionID, ctx.id.DeviceID, ctx.parts[2:])
	case "context":
		serveTerminalContext(w, r, ctx.home, ctx.mgr, ctx.sessionID, ctx.id, ctx.parts[2:])
	case "control":
		serveTerminalControl(w, r, ctx.home, ctx.mgr, ctx.sessionID, ctx.id, ctx.parts[2:])
	default:
		return false
	}
	return true
}

func serveTerminalParticipants(w http.ResponseWriter, r *http.Request, mgr *TerminalManager, sessionID, participantID string) {
	if participantID != "" && r.Method == http.MethodDelete {
		if err := mgr.RemoveParticipantAccess(sessionID, participantID); err != nil {
			writeTerminalError(w, err)
			return
		}
		writeTerminalJSON(w, http.StatusOK, map[string]any{"ok": true})
		return
	}
	if r.Method != http.MethodGet {
		http.Error(w, methodNotAllowedMsg, http.StatusMethodNotAllowed)
		return
	}
	participants, err := mgr.store.ListParticipants(sessionID)
	if err != nil {
		writeTerminalError(w, err)
		return
	}
	writeTerminalJSON(w, http.StatusOK, map[string]any{"participants": participants})
}

func serveTerminalSessions(w http.ResponseWriter, r *http.Request, home string, mgr *TerminalManager, id DeviceIdentity, priv ed25519.PrivateKey, parts []string) {
	if len(parts) == 0 {
		serveTerminalSessionsCollection(w, r, home, mgr, id)
		return
	}
	sessionID := parts[0]
	if len(parts) == 1 {
		serveTerminalSessionResource(w, r, mgr, sessionID, id.DeviceID)
		return
	}
	ctx := terminalSessionSubrouteCtx{home: home, mgr: mgr, id: id, priv: priv, sessionID: sessionID, parts: parts}
	serveTerminalSessionSubroute(w, r, ctx)
}

func serveTerminalCreate(w http.ResponseWriter, r *http.Request, home string, mgr *TerminalManager, id DeviceIdentity) {
	raw, err := io.ReadAll(io.LimitReader(r.Body, terminalHTTPBodyMax))
	if err != nil {
		http.Error(w, badRequestMsg, http.StatusBadRequest)
		return
	}
	var req struct {
		Name             string `json:"name"`
		WorkingDirectory string `json:"working_directory"`
		ProjectRoot      string `json:"project_root"`
		Command          string `json:"command"`
		CircleID         string `json:"circle_id"`
		Cols             int    `json:"cols"`
		Rows             int    `json:"rows"`
	}
	_ = json.Unmarshal(raw, &req)
	sess, err := mgr.CreateSession(terminalCreateOpts{
		Name: req.Name, WorkingDirectory: req.WorkingDirectory, ProjectRoot: req.ProjectRoot,
		Command: req.Command, CircleID: req.CircleID, Cols: req.Cols, Rows: req.Rows,
	})
	if err != nil {
		writeTerminalError(w, err)
		return
	}
	if req.CircleID != "" {
		payload, _ := json.Marshal(map[string]string{"session_id": sess.ID, "state": string(sess.State)})
		_ = publishTerminalMQTT(home, req.CircleID, terminalStatusTopic(req.CircleID, sess.ID), "status", payload)
	}
	writeTerminalJSON(w, http.StatusCreated, terminalSessionPayload(mgr, sess, id.DeviceID))
}

func serveTerminalAttach(w http.ResponseWriter, r *http.Request, mgr *TerminalManager, sessionID, deviceID string) {
	var body struct {
		Capabilities []TerminalCapability `json:"capabilities"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body)
	tok, err := mgr.AttachSession(sessionID, deviceID, body.Capabilities)
	if err != nil {
		writeTerminalError(w, err)
		return
	}
	lease, _, _ := mgr.store.GetActiveLease(sessionID)
	writeTerminalJSON(w, http.StatusOK, map[string]any{
		"stream_token":   tok,
		"lease":          lease,
		"self_device_id": deviceID,
	})
}

func serveTerminalResize(w http.ResponseWriter, r *http.Request, mgr *TerminalManager, sessionID, deviceID string) {
	var body struct {
		Cols    int    `json:"cols"`
		Rows    int    `json:"rows"`
		LeaseID string `json:"lease_id"`
		Epoch   int64  `json:"epoch"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, badRequestMsg, http.StatusBadRequest)
		return
	}
	if err := mgr.ResizeSession(sessionID, deviceID, body.LeaseID, body.Epoch, body.Cols, body.Rows); err != nil {
		writeTerminalError(w, err)
		return
	}
	writeTerminalJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func serveTerminalInvite(w http.ResponseWriter, r *http.Request, home string, mgr *TerminalManager, sessionID string, id DeviceIdentity, priv ed25519.PrivateKey) {
	var body struct {
		TargetDeviceID string               `json:"target_device_id"`
		Permission     string               `json:"permission"`
		Capabilities   []TerminalCapability `json:"capabilities"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, badRequestMsg, http.StatusBadRequest)
		return
	}
	sess, err := mgr.GetSession(sessionID)
	if err != nil {
		writeTerminalError(w, err)
		return
	}
	if id.DeviceID != sess.OwnerDeviceID {
		writeTerminalError(w, errors.New(string(TerminalErrAccessDenied)))
		return
	}
	payload, _ := json.Marshal(map[string]any{
		"session_id": sessionID, "host_device_id": sess.HostDeviceID, "permission": body.Permission,
	})
	circleID := sess.CircleID
	if circleID == "" {
		writeTerminalError(w, errors.New("session has no circle"))
		return
	}
	circle, err := loadCircle(home, circleID)
	if err != nil {
		writeTerminalError(w, err)
		return
	}
	if _, ok := circle.activeMember(body.TargetDeviceID); !ok || body.TargetDeviceID == id.DeviceID {
		writeTerminalError(w, errors.New("invite target is not another active circle member"))
		return
	}
	alreadyShared, err := mgr.store.HasShare(sessionID, body.TargetDeviceID)
	if err != nil {
		writeTerminalError(w, err)
		return
	}
	if _, err := mgr.store.GrantShare(sessionID, body.TargetDeviceID, terminalSharePermissionObserve); err != nil {
		writeTerminalError(w, err)
		return
	}
	if err := publishTerminalMQTT(home, circleID, terminalInviteTopic(circleID, body.TargetDeviceID), "invite", payload); err != nil {
		if !alreadyShared {
			_ = mgr.store.RevokeShare(sessionID, body.TargetDeviceID)
		}
		writeTerminalError(w, err)
		return
	}
	writeTerminalJSON(w, http.StatusOK, map[string]any{"sent": true})
}

func serveTerminalEvents(w http.ResponseWriter, r *http.Request, mgr *TerminalManager, sessionID string) {
	from := int64(0)
	if v := strings.TrimSpace(r.URL.Query().Get("from_seq")); v != "" {
		from, _ = parseInt64(v)
	}
	events, err := mgr.store.ReplayEvents(sessionID, from, 0, 500)
	if err != nil {
		writeTerminalError(w, err)
		return
	}
	writeTerminalJSON(w, http.StatusOK, map[string]any{"events": events})
}

func serveTerminalRecording(w http.ResponseWriter, r *http.Request, mgr *TerminalManager, sessionID, actor string, parts []string) {
	if len(parts) == 0 {
		if r.Method != http.MethodGet {
			http.Error(w, methodNotAllowedMsg, http.StatusMethodNotAllowed)
			return
		}
		events, _ := mgr.store.ReplayEvents(sessionID, 0, 0, 1000)
		writeTerminalJSON(w, http.StatusOK, map[string]any{"events": events})
		return
	}
	if r.Method != http.MethodPost {
		http.Error(w, methodNotAllowedMsg, http.StatusMethodNotAllowed)
		return
	}
	switch parts[0] {
	case "pause":
		if err := mgr.store.PauseRecording(sessionID, actor); err != nil {
			writeTerminalError(w, err)
			return
		}
	case "resume":
		if err := mgr.store.ResumeRecording(sessionID, actor); err != nil {
			writeTerminalError(w, err)
			return
		}
	default:
		http.NotFound(w, r)
		return
	}
	writeTerminalJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func serveTerminalContext(w http.ResponseWriter, r *http.Request, home string, mgr *TerminalManager, sessionID string, id DeviceIdentity, parts []string) {
	if len(parts) == 0 || parts[0] == "links" {
		if r.Method != http.MethodGet {
			http.Error(w, methodNotAllowedMsg, http.StatusMethodNotAllowed)
			return
		}
		links, err := mgr.store.ListContextLinks(sessionID)
		if err != nil {
			writeTerminalError(w, err)
			return
		}
		writeTerminalJSON(w, http.StatusOK, map[string]any{"links": links})
		return
	}
	if r.Method != http.MethodPost {
		http.Error(w, methodNotAllowedMsg, http.StatusMethodNotAllowed)
		return
	}
	sess, err := mgr.GetSession(sessionID)
	if err != nil {
		writeTerminalError(w, err)
		return
	}
	switch parts[0] {
	case "create":
		serveTerminalContextCreate(w, r, home, mgr, sess, id.DeviceID)
	case "share":
		serveTerminalContextShare(w, r, home, mgr, sess, id.DeviceID)
	default:
		http.NotFound(w, r)
	}
}

func serveTerminalContextCreate(w http.ResponseWriter, r *http.Request, home string, mgr *TerminalManager, sess TerminalSession, actorDeviceID string) {
	raw, err := io.ReadAll(io.LimitReader(r.Body, terminalHTTPBodyMax))
	if err != nil {
		http.Error(w, badRequestMsg, http.StatusBadRequest)
		return
	}
	var body struct {
		Objective           string                `json:"objective"`
		ConversationSummary string                `json:"conversation_summary"`
		RecallQuery         string                `json:"recall_query"`
		StartSeq            int64                 `json:"start_seq"`
		EndSeq              int64                 `json:"end_seq"`
		TokenBudget         int                   `json:"token_budget"`
		Decisions           []string              `json:"decisions"`
		ExpectedWorkspace   *GitWorkspaceSnapshot `json:"expected_workspace"`
	}
	if err := json.Unmarshal(raw, &body); err != nil {
		http.Error(w, badRequestMsg, http.StatusBadRequest)
		return
	}
	bundle, err := buildContextBundle(home, mgr.store, sess, actorDeviceID, contextBuildRequest{
		Objective: body.Objective, ConversationSummary: body.ConversationSummary,
		RecallQuery: body.RecallQuery, StartSeq: body.StartSeq, EndSeq: body.EndSeq,
		TokenBudget: body.TokenBudget, ExpectedWorkspace: body.ExpectedWorkspace, Decisions: body.Decisions,
	})
	if err != nil {
		writeTerminalError(w, err)
		return
	}
	link := TerminalContextLink{
		ID: "tcl_" + randomToken(16), SessionID: sess.ID, LinkKind: "context_bundle", RefID: bundle.ID,
		StartSeq: body.StartSeq, EndSeq: body.EndSeq, CreatedAt: terminalNow(),
	}
	if err := mgr.store.InsertContextLink(link); err != nil {
		writeTerminalError(w, err)
		return
	}
	_ = mgr.store.audit(sess.ID, actorDeviceID, "context_create", bundle.ID)
	writeTerminalJSON(w, http.StatusCreated, map[string]any{"bundle": bundle, "link": link})
}

func serveTerminalContextShare(w http.ResponseWriter, r *http.Request, home string, mgr *TerminalManager, sess TerminalSession, actorDeviceID string) {
	raw, err := io.ReadAll(io.LimitReader(r.Body, terminalHTTPBodyMax))
	if err != nil {
		http.Error(w, badRequestMsg, http.StatusBadRequest)
		return
	}
	var body struct {
		Bundle           ContextBundle `json:"bundle"`
		WorkspaceID      string        `json:"workspace_id"`
		CircleID         string        `json:"circle_id"`
		ArtifactName     string        `json:"artifact_name"`
	}
	if err := json.Unmarshal(raw, &body); err != nil {
		http.Error(w, badRequestMsg, http.StatusBadRequest)
		return
	}
	circleID := strings.TrimSpace(body.CircleID)
	if circleID == "" {
		circleID = strings.TrimSpace(sess.CircleID)
	}
	if circleID == "" || strings.TrimSpace(body.WorkspaceID) == "" {
		writeTerminalError(w, errors.New("circle_id and workspace_id required"))
		return
	}
	if body.Bundle.ID == "" {
		writeTerminalError(w, errors.New("bundle required"))
		return
	}
	name := strings.TrimSpace(body.ArtifactName)
	if name == "" {
		name = body.Bundle.ID + ".json"
	}
	payload, _ := json.MarshalIndent(body.Bundle, "", "  ")
	entry, err := WriteSharedWorkspaceArtifact(home, circleID, body.WorkspaceID, actorDeviceID, name, payload)
	if err != nil {
		writeTerminalError(w, err)
		return
	}
	link := TerminalContextLink{
		ID: "tcl_" + randomToken(16), SessionID: sess.ID, LinkKind: "shared_workspace", RefID: entry.Path,
		CreatedAt: terminalNow(),
	}
	if err := mgr.store.InsertContextLink(link); err != nil {
		writeTerminalError(w, err)
		return
	}
	_ = mgr.store.audit(sess.ID, actorDeviceID, "context_share", entry.Path)
	writeTerminalJSON(w, http.StatusOK, map[string]any{"artifact": entry, "link": link})
}

func serveTerminalControl(w http.ResponseWriter, r *http.Request, home string, mgr *TerminalManager, sessionID string, id DeviceIdentity, parts []string) {
	if len(parts) == 0 || r.Method != http.MethodPost {
		http.Error(w, methodNotAllowedMsg, http.StatusMethodNotAllowed)
		return
	}
	ctrl := newTerminalControl(mgr.store, id.DeviceID)
	var body struct {
		TargetDeviceID string `json:"target_device_id"`
		LeaseID        string `json:"lease_id"`
		Epoch          int64  `json:"epoch"`
		Reason         string `json:"reason"`
		ExpiresAt      string `json:"expires_at"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body)
	if body.ExpiresAt == "" {
		body.ExpiresAt = time.Now().Add(time.Hour).UTC().Format(time.RFC3339)
	}
	switch parts[0] {
	case "request":
		if err := ctrl.RequestControl(sessionID, id.DeviceID); err != nil {
			writeTerminalError(w, err)
			return
		}
	case "grant":
		lease, err := ctrl.GrantControl(sessionID, id.DeviceID, body.TargetDeviceID, body.Epoch, body.ExpiresAt)
		if err != nil {
			writeTerminalError(w, err)
			return
		}
		writeTerminalJSON(w, http.StatusOK, map[string]any{"lease": lease})
		return
	case "reject":
		if err := ctrl.RejectControl(sessionID, id.DeviceID, body.TargetDeviceID, body.Reason); err != nil {
			writeTerminalError(w, err)
			return
		}
	case "release":
		lease, err := ctrl.ReleaseControl(sessionID, id.DeviceID, body.LeaseID, body.Epoch)
		if err != nil {
			writeTerminalError(w, err)
			return
		}
		writeTerminalJSON(w, http.StatusOK, map[string]any{"lease": lease})
		return
	case "revoke":
		lease, err := ctrl.RevokeControl(sessionID, id.DeviceID, body.TargetDeviceID, body.Reason)
		if err != nil {
			writeTerminalError(w, err)
			return
		}
		writeTerminalJSON(w, http.StatusOK, map[string]any{"lease": lease})
		return
	default:
		http.NotFound(w, r)
		return
	}
	writeTerminalJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func terminalSessionPayload(mgr *TerminalManager, sess TerminalSession, selfDeviceID string) map[string]any {
	lease, _, _ := mgr.store.GetActiveLease(sess.ID)
	out := map[string]any{
		"session":        sess,
		"lease":          lease,
		"self_device_id": selfDeviceID,
	}
	if selfDeviceID == sess.OwnerDeviceID || selfDeviceID == sess.HostDeviceID {
		if pending, err := mgr.store.PendingControlRequesters(sess.ID); err == nil {
			out["pending_control_requesters"] = pending
		}
	}
	return out
}

func writeTerminalJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set(headerContentType, "application/json")
	w.Header().Set(headerCacheControl, cacheNoStore)
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeTerminalError(w http.ResponseWriter, err error) {
	if err == nil {
		return
	}
	msg := err.Error()
	code := http.StatusBadRequest
	switch {
	case errors.Is(err, sql.ErrNoRows):
		code = http.StatusNotFound
		msg = string(TerminalErrSessionNotFound)
	case strings.Contains(msg, string(TerminalErrAccessDenied)):
		code = http.StatusForbidden
	case strings.Contains(msg, string(TerminalErrSessionNotFound)):
		code = http.StatusNotFound
	case strings.Contains(msg, string(TerminalErrInvalidLease)):
		code = http.StatusConflict
	}
	http.Error(w, msg, code)
}
