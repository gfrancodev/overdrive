package main

import (
	"crypto/ed25519"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"
)

const (
	callHTTPBodyMax    = 256 * 1024
	callIceTTLSeconds  = 60
	callRingTTL        = 90 * time.Second
	callKindICE        = "ice"
	callKindMediaState = "media-state"
)

var (
	callMu          sync.Mutex
	callEvents      []callEventRecord
	callEventSeq    int64
	callByID        = map[string]*callSessionState{}
	errCallHostOnly = errors.New("only the host can moderate")
)

type callSessionState struct {
	CallID         string   `json:"call_id"`
	MeshID         string   `json:"mesh_id"`
	State          string   `json:"state"`
	PeerDeviceID   string   `json:"peer_device_id"`
	Direction      string   `json:"direction"`
	Mode           string   `json:"mode,omitempty"`
	ConversationID string   `json:"conversation_id,omitempty"`
	HostDeviceID   string   `json:"host_device_id,omitempty"`
	Participants   []string `json:"participants,omitempty"`
	UpdatedAt      string   `json:"updated_at"`
}

type callEventRecord struct {
	Seq          int64           `json:"seq"`
	At           string          `json:"at"`
	MeshID       string          `json:"mesh_id"`
	CallID       string          `json:"call_id"`
	Kind         string          `json:"kind"`
	FromDeviceID string          `json:"from_device_id"`
	Payload      json.RawMessage `json:"payload,omitempty"`
}

type incomingCallView struct {
	CallID         string `json:"call_id"`
	PeerDeviceID   string `json:"peer_device_id"`
	MeshID         string `json:"mesh_id"`
	Mode           string `json:"mode,omitempty"`
	ConversationID string `json:"conversation_id,omitempty"`
}

type callSignalRequest struct {
	MeshID          string          `json:"mesh_id"`
	TargetDeviceID  string          `json:"target_device_id"`
	TargetDeviceIDs []string        `json:"target_device_ids"`
	ConversationID  string          `json:"conversation_id"`
	CallID          string          `json:"call_id"`
	Kind            string          `json:"kind"`
	Payload         json.RawMessage `json:"payload,omitempty"`
}

type callSummaryRequest struct {
	MeshID      string `json:"mesh_id"`
	CallID      string `json:"call_id"`
	Subject     string `json:"subject"`
	Content     string `json:"content"`
	ProjectRoot string `json:"project_root,omitempty"`
	Evidence    string `json:"evidence,omitempty"`
}

func callTopic(meshID, deviceID string) string {
	return meshPrefix(meshID) + "call/" + deviceID
}

func callControlKinds() map[string]bool {
	return map[string]bool{
		"invite": true, "accept": true, "reject": true, "busy": true,
		"hangup": true, "context": true, "offer": true, "answer": true,
		"mod": true, "host": true,
	}
}

func callQoS(kind string) byte {
	switch kind {
	case callKindICE, callKindMediaState:
		return 0
	default:
		return 1
	}
}

func callSignalTargets(req callSignalRequest) []string {
	seen := map[string]bool{}
	out := []string{}
	add := func(id string) {
		id = strings.TrimSpace(id)
		if id == "" || seen[id] {
			return
		}
		seen[id] = true
		out = append(out, id)
	}
	add(req.TargetDeviceID)
	for _, id := range req.TargetDeviceIDs {
		add(id)
	}
	return out
}

func validateCallSignalTargets(req callSignalRequest) error {
	targets := callSignalTargets(req)
	if len(targets) == 0 {
		return errors.New("target_device_id is required")
	}
	for _, id := range targets {
		if err := validTopicSegment(id); err != nil {
			return errors.New("target_device_id is required")
		}
	}
	return nil
}

func normalizeCallSignal(req callSignalRequest) (callSignalRequest, error) {
	req.MeshID = strings.TrimSpace(req.MeshID)
	req.TargetDeviceID = strings.TrimSpace(req.TargetDeviceID)
	req.ConversationID = strings.TrimSpace(req.ConversationID)
	req.CallID = strings.TrimSpace(req.CallID)
	req.Kind = strings.TrimSpace(req.Kind)
	if err := validTopicSegment(req.MeshID); err != nil {
		return req, err
	}
	if err := validateCallSignalTargets(req); err != nil {
		return req, err
	}
	if req.ConversationID != "" {
		if err := validTopicSegment(req.ConversationID); err != nil {
			return req, err
		}
	}
	if req.Kind == "" {
		return req, errors.New("kind is required")
	}
	if !callControlKinds()[req.Kind] && req.Kind != callKindICE && req.Kind != callKindMediaState {
		return req, errors.New("unsupported call kind")
	}
	if req.CallID == "" && req.Kind == "invite" {
		req.CallID = "call_" + newMessageID()
	}
	if req.CallID == "" {
		return req, errors.New("call_id is required")
	}
	if err := validTopicSegment(req.CallID); err != nil {
		return req, err
	}
	if len(req.Payload) > callHTTPBodyMax {
		return req, errors.New("payload is too large")
	}
	return req, nil
}

func pickNewestInboundRingingCall(self string, cutoff time.Time) *callSessionState {
	var newest *callSessionState
	var newestAt time.Time
	for _, sess := range callByID {
		if sess.State != "ringing" || sess.Direction != "inbound" || sess.PeerDeviceID == "" {
			continue
		}
		if self != "" && sess.PeerDeviceID == self {
			continue
		}
		at, err := time.Parse(time.RFC3339, sess.UpdatedAt)
		if err != nil || at.Before(cutoff) {
			continue
		}
		if newest == nil || at.After(newestAt) || (at.Equal(newestAt) && sess.CallID > newest.CallID) {
			newest, newestAt = sess, at
		}
	}
	return newest
}

func attachIncomingCall(home string, dash *peerDashboard) {
	if dash == nil {
		return
	}
	self := dash.DeviceID
	if self == "" {
		self = localDeviceID(home)
	}
	callMu.Lock()
	defer callMu.Unlock()
	newest := pickNewestInboundRingingCall(self, time.Now().Add(-callRingTTL))
	if newest == nil {
		return
	}
	dash.IncomingCall = &incomingCallView{
		CallID:         newest.CallID,
		PeerDeviceID:   newest.PeerDeviceID,
		MeshID:         newest.MeshID,
		Mode:           newest.Mode,
		ConversationID: newest.ConversationID,
	}
}

func appendCallEvent(ev callEventRecord) {
	callMu.Lock()
	callEventSeq++
	ev.Seq = callEventSeq
	if ev.At == "" {
		ev.At = nowRFC3339()
	}
	callEvents = append(callEvents, ev)
	if len(callEvents) > 512 {
		callEvents = callEvents[len(callEvents)-512:]
	}
	callMu.Unlock()
}

func parseInviteSessionMeta(kind string, reqConversationID string, payload json.RawMessage) (mode, conversationID string) {
	if kind != "invite" {
		return "", strings.TrimSpace(reqConversationID)
	}
	body := map[string]any{}
	if len(payload) > 0 {
		_ = json.Unmarshal(payload, &body)
	}
	conversationID = strings.TrimSpace(reqConversationID)
	if v, ok := body["conversation_id"].(string); ok && strings.TrimSpace(v) != "" {
		conversationID = strings.TrimSpace(v)
	}
	if v, ok := body["mode"].(string); ok {
		mode = strings.TrimSpace(v)
	}
	if mode != "video" {
		mode = "voice"
	}
	return mode, conversationID
}

func hostFromPayload(payload json.RawMessage) string {
	body := map[string]any{}
	if len(payload) == 0 || json.Unmarshal(payload, &body) != nil {
		return ""
	}
	host, _ := body["host_device_id"].(string)
	return strings.TrimSpace(host)
}

func participantsFromPayload(payload json.RawMessage) []string {
	body := map[string]any{}
	if len(payload) == 0 || json.Unmarshal(payload, &body) != nil {
		return nil
	}
	raw, ok := body["participants"].([]any)
	if !ok {
		return nil
	}
	seen := map[string]bool{}
	out := []string{}
	for _, item := range raw {
		id, ok := item.(string)
		if !ok {
			continue
		}
		id = strings.TrimSpace(id)
		if id == "" || seen[id] {
			continue
		}
		seen[id] = true
		out = append(out, id)
	}
	return out
}

func removeParticipant(ids []string, id string) []string {
	if len(ids) == 0 {
		return ids
	}
	out := make([]string, 0, len(ids))
	for _, item := range ids {
		if item != id {
			out = append(out, item)
		}
	}
	return out
}

func lowestDeviceID(ids []string) string {
	best := ""
	for _, id := range ids {
		id = strings.TrimSpace(id)
		if id == "" {
			continue
		}
		if best == "" || id < best {
			best = id
		}
	}
	return best
}

func callHostID(callID string) string {
	callMu.Lock()
	defer callMu.Unlock()
	sess := callByID[callID]
	if sess == nil {
		return ""
	}
	return sess.HostDeviceID
}

func allowHostClaim(callID, sender string) bool {
	callMu.Lock()
	defer callMu.Unlock()
	sess := callByID[callID]
	if sess == nil || sender == "" {
		return false
	}
	if sess.HostDeviceID == "" || sess.HostDeviceID == sender {
		return true
	}
	for _, id := range sess.Participants {
		if id == sess.HostDeviceID {
			return false
		}
	}
	return true
}

func authorizeCallModerator(req callSignalRequest, sender string) error {
	switch req.Kind {
	case "mod":
		if strings.TrimSpace(callHostID(req.CallID)) != sender {
			return errCallHostOnly
		}
	case "host":
		next := hostFromPayload(req.Payload)
		if next == "" || next != sender || !allowHostClaim(req.CallID, sender) {
			return errCallHostOnly
		}
	}
	return nil
}

func applyCallSessionInvite(sess *callSessionState, remoteDevice string, outbound bool, kind, reqConversationID string, payload json.RawMessage) {
	sess.State = "ringing"
	sess.PeerDeviceID = remoteDevice
	mode, conv := parseInviteSessionMeta(kind, reqConversationID, payload)
	if mode != "" {
		sess.Mode = mode
	}
	if conv != "" {
		sess.ConversationID = conv
	}
	if host := hostFromPayload(payload); host != "" {
		sess.HostDeviceID = host
	}
	if roster := participantsFromPayload(payload); len(roster) > 0 {
		sess.Participants = roster
	}
	if outbound {
		sess.Direction = "outbound"
	} else {
		sess.Direction = "inbound"
	}
}

func applyCallSessionAccept(sess *callSessionState, remoteDevice string) {
	sess.State = "active"
	if remoteDevice != "" && !containsDevice(sess.Participants, remoteDevice) {
		sess.Participants = append(sess.Participants, remoteDevice)
	}
}

func applyCallSessionReject(sess *callSessionState, remoteDevice string, outbound bool) {
	if !outbound {
		sess.Participants = removeParticipant(sess.Participants, remoteDevice)
		if sess.HostDeviceID == remoteDevice {
			if next := lowestDeviceID(sess.Participants); next != "" {
				sess.HostDeviceID = next
			}
		}
	}
	if outbound || len(sess.Participants) <= 1 {
		sess.State = "ended"
	}
}

func applyCallSessionHost(sess *callSessionState, payload json.RawMessage) {
	if next := hostFromPayload(payload); next != "" {
		sess.HostDeviceID = next
	}
	if sess.State == "" || sess.State == "ringing" {
		sess.State = "active"
	}
}

func ensureCallSessionActive(sess *callSessionState) {
	if sess.State == "" {
		sess.State = "active"
	}
}

func updateCallSession(meshID, callID, kind, remoteDevice string, outbound bool, reqConversationID string, payload json.RawMessage) {
	callMu.Lock()
	defer callMu.Unlock()
	sess := callByID[callID]
	if sess == nil {
		sess = &callSessionState{CallID: callID, MeshID: meshID}
		callByID[callID] = sess
	}
	sess.MeshID = meshID
	sess.UpdatedAt = nowRFC3339()
	switch kind {
	case "invite":
		applyCallSessionInvite(sess, remoteDevice, outbound, kind, reqConversationID, payload)
	case "accept":
		applyCallSessionAccept(sess, remoteDevice)
	case "reject", "busy", "hangup":
		applyCallSessionReject(sess, remoteDevice, outbound)
	case "host":
		applyCallSessionHost(sess, payload)
	case "mod", "offer", "answer", callKindICE, callKindMediaState, "context":
		ensureCallSessionActive(sess)
	}
	if sess.PeerDeviceID == "" {
		sess.PeerDeviceID = remoteDevice
	}
}

func listCallEventsSince(meshID string, since int64) ([]callEventRecord, []callSessionState, int64) {
	callMu.Lock()
	defer callMu.Unlock()
	out := []callEventRecord{}
	for _, ev := range callEvents {
		if meshID != "" && ev.MeshID != meshID {
			continue
		}
		if ev.Seq > since {
			out = append(out, ev)
		}
	}
	sessions := []callSessionState{}
	for _, sess := range callByID {
		if meshID != "" && sess.MeshID != meshID {
			continue
		}
		if sess.State != "ended" {
			sessions = append(sessions, *sess)
		}
	}
	return out, sessions, callEventSeq
}

func enrichCallInvitePayload(req callSignalRequest, payload json.RawMessage) json.RawMessage {
	if req.Kind != "invite" && req.ConversationID == "" {
		return payload
	}
	body := map[string]any{}
	if len(payload) > 0 {
		_ = json.Unmarshal(payload, &body)
	}
	if req.ConversationID != "" {
		body["conversation_id"] = req.ConversationID
	}
	if len(body) == 0 {
		return payload
	}
	out, err := json.Marshal(body)
	if err != nil {
		return payload
	}
	return out
}

func publishCallSignalOnce(home string, req callSignalRequest, targetDeviceID string, id DeviceIdentity, priv ed25519.PrivateKey, profile LocalUser, circle Circle) (bool, error) {
	if _, ok := circle.activeMember(targetDeviceID); !ok {
		return false, errors.New("target is not a circle member")
	}
	expires := ""
	if req.Kind == callKindICE || req.Kind == callKindMediaState {
		expires = time.Now().Add(time.Duration(callIceTTLSeconds) * time.Second).UTC().Format(time.RFC3339)
	}
	payload := enrichCallInvitePayload(req, req.Payload)
	if payload == nil {
		payload = json.RawMessage(`{}`)
	}
	env := chatEnvelope{
		Version: chatEnvelopeVersion, MessageID: newMessageID(), MeshID: circle.ID,
		ConversationID: req.CallID, SenderUserID: profile.UserID, SenderDeviceID: id.DeviceID,
		Recipient: targetDeviceID, Type: "call:" + req.Kind, Timestamp: nowRFC3339(),
		Payload: payload, ExpiresAt: expires,
	}
	env, err := signChatEnvelope(priv, env)
	if err != nil {
		return false, err
	}
	topic := callTopic(circle.ID, targetDeviceID)
	wire, err := sealChatEnvelope(circle.GroupKey, env)
	if err != nil {
		return false, err
	}
	updateCallSession(req.MeshID, req.CallID, req.Kind, targetDeviceID, true, req.ConversationID, payload)
	appendCallEvent(callEventRecord{
		MeshID: req.MeshID, CallID: req.CallID, Kind: req.Kind,
		FromDeviceID: id.DeviceID, Payload: payload,
	})
	if !meshAllowsChat() {
		return true, nil
	}
	if err := meshTrySend(circle.ID, topic, callQoS(req.Kind), wire); err != nil {
		return true, nil
	}
	return false, nil
}

func publishCallSignal(home string, req callSignalRequest) (callSignalRequest, bool, error) {
	req, err := normalizeCallSignal(req)
	if err != nil {
		return req, false, err
	}
	id, priv, err := loadOrCreateIdentity(home)
	if err != nil {
		return req, false, err
	}
	profile, err := loadOrCreateLocalUser(home)
	if err != nil {
		return req, false, err
	}
	circle, err := loadCircle(home, req.MeshID)
	if err != nil {
		return req, false, err
	}
	if _, ok := circle.activeMember(id.DeviceID); !ok {
		return req, false, errors.New("not a circle member")
	}
	if err := authorizeCallModerator(req, id.DeviceID); err != nil {
		return req, false, err
	}
	if req.Payload == nil {
		req.Payload = json.RawMessage(`{}`)
	}
	targets := callSignalTargets(req)
	queued := false
	for _, target := range targets {
		q, err := publishCallSignalOnce(home, req, target, id, priv, profile, circle)
		if err != nil {
			return req, queued, err
		}
		if q {
			queued = true
		}
	}
	return req, queued, nil
}

func acceptCallSignalWire(home string, body []byte) error {
	var peek struct {
		MeshID string `json:"meshId"`
	}
	if err := json.Unmarshal(body, &peek); err != nil {
		return err
	}
	circle, err := loadCircle(home, peek.MeshID)
	if err != nil {
		return err
	}
	env, err := openCircleChatWire(circle, body)
	if err != nil {
		return err
	}
	if !strings.HasPrefix(env.Type, "call:") {
		return nil
	}
	kind := strings.TrimPrefix(env.Type, "call:")
	member, ok := circle.activeMember(env.SenderDeviceID)
	if !ok {
		return errors.New("sender is not a circle member")
	}
	pub, err := decodePublicKey(member.PublicKey)
	if err != nil {
		return err
	}
	if !verifyChatEnvelope(pub, env) {
		return errors.New("bad call signature")
	}
	if env.ExpiresAt != "" {
		if t, err := time.Parse(time.RFC3339, env.ExpiresAt); err == nil && time.Now().After(t) {
			return nil
		}
	}
	updateCallSession(env.MeshID, env.ConversationID, kind, env.SenderDeviceID, false, "", env.Payload)
	appendCallEvent(callEventRecord{
		MeshID: env.MeshID, CallID: env.ConversationID, Kind: kind,
		FromDeviceID: env.SenderDeviceID, Payload: env.Payload,
	})
	return nil
}

func recordCallSummary(home string, req callSummaryRequest) error {
	req.Subject = strings.TrimSpace(req.Subject)
	req.Content = strings.TrimSpace(req.Content)
	if req.Subject == "" || req.Content == "" {
		return errors.New("subject and content are required")
	}
	if len(req.Content) > chatSummaryMax*4 {
		return errors.New("content is too long")
	}
	root := strings.TrimSpace(req.ProjectRoot)
	if root == "" {
		root = "."
	}
	project, err := identifyProject(root)
	if err != nil {
		return err
	}
	e, err := newEngine(home)
	if err != nil {
		return err
	}
	defer e.Close()
	evidence := strings.TrimSpace(req.Evidence)
	if evidence == "" {
		evidence = "authorized call summary"
	}
	_, err = e.recordMemory(memoryRecord{
		Project: project, Kind: "lesson", Scope: "repository", Subject: req.Subject,
		Content: req.Content, Confidence: 0.85, Priority: 55,
		Source: "verified_execution", Evidence: evidence,
	})
	return err
}

func handleCallSignalHTTP(w http.ResponseWriter, r *http.Request, home string) {
	if r.Method != http.MethodPost {
		http.Error(w, methodNotAllowedMsg, http.StatusMethodNotAllowed)
		return
	}
	if rejectRemoteCoreAdmin(w, r) {
		return
	}
	raw, err := io.ReadAll(io.LimitReader(r.Body, callHTTPBodyMax))
	if err != nil {
		http.Error(w, badRequestMsg, http.StatusBadRequest)
		return
	}
	var req callSignalRequest
	if err := json.Unmarshal(raw, &req); err != nil {
		http.Error(w, badRequestMsg, http.StatusBadRequest)
		return
	}
	out, queued, err := publishCallSignal(home, req)
	if err != nil {
		writeChatError(w, err)
		return
	}
	writeMeshJSON(w, http.StatusOK, map[string]any{
		"queued": queued, "call_id": out.CallID, "kind": out.Kind,
	}, "")
}

func handleCallEventsHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, methodNotAllowedMsg, http.StatusMethodNotAllowed)
		return
	}
	if rejectRemoteCoreAdmin(w, r) {
		return
	}
	meshID := strings.TrimSpace(r.URL.Query().Get("mesh_id"))
	if meshID != "" {
		if err := validTopicSegment(meshID); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
	}
	since := 0
	if raw := strings.TrimSpace(r.URL.Query().Get("since")); raw != "" {
		if n, err := parseInt64(raw); err == nil {
			since = int(n)
		}
	}
	events, sessions, seq := listCallEventsSince(meshID, int64(since))
	writeMeshJSON(w, http.StatusOK, map[string]any{
		"events": events, "sessions": sessions, "seq": seq,
	}, "")
}

func handleCallSummaryHTTP(w http.ResponseWriter, r *http.Request, home string) {
	if r.Method != http.MethodPost {
		http.Error(w, methodNotAllowedMsg, http.StatusMethodNotAllowed)
		return
	}
	if rejectRemoteCoreAdmin(w, r) {
		return
	}
	raw, err := io.ReadAll(io.LimitReader(r.Body, callHTTPBodyMax))
	if err != nil {
		http.Error(w, badRequestMsg, http.StatusBadRequest)
		return
	}
	var req callSummaryRequest
	if err := json.Unmarshal(raw, &req); err != nil {
		http.Error(w, badRequestMsg, http.StatusBadRequest)
		return
	}
	if err := recordCallSummary(home, req); err != nil {
		writeChatError(w, err)
		return
	}
	writeMeshJSON(w, http.StatusOK, map[string]any{"saved": true}, "")
}

func registerCallAPIRoutes(mux *http.ServeMux, home string) {
	mux.HandleFunc("/api/call/signal", func(w http.ResponseWriter, r *http.Request) {
		handleCallSignalHTTP(w, r, home)
	})
	mux.HandleFunc("/api/call/events", handleCallEventsHTTP)
	mux.HandleFunc("/api/call/summary", func(w http.ResponseWriter, r *http.Request) {
		handleCallSummaryHTTP(w, r, home)
	})
}

func containsDevice(ids []string, id string) bool {
	for _, item := range ids {
		if item == id {
			return true
		}
	}
	return false
}

func parseInt64(s string) (int64, error) {
	var n int64
	for _, c := range s {
		if c < '0' || c > '9' {
			return 0, errors.New("bad int")
		}
		n = n*10 + int64(c-'0')
	}
	return n, nil
}

// resetCallStateForTests clears in-memory call routing (tests only).
func resetCallStateForTests() {
	callMu.Lock()
	callEvents = nil
	callEventSeq = 0
	callByID = map[string]*callSessionState{}
	callMu.Unlock()
}
