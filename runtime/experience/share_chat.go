package main

import (
	"crypto/ed25519"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"
)

const (
	chatEnvelopeVersion           = 1
	chatTextMax                   = 8192
	chatSummaryMax                = 4096
	chatFileNameMax               = 200
	chatMimeMax                   = 128
	chatFileMaxBytes              = 4 * 1024 * 1024
	chatHTTPBodyMax               = 8 * 1024 * 1024
	meshTopicRoot                 = "overdrive/v1"
	chatConversationProjectPrefix = "project:"
	shareChatRequestFailedMsg     = "request failed"
)

// chatEnvelope is the circle chat record. Payload is plaintext in the local
// store and ciphertext on MQTT, so the broker cannot read the text.
type chatEnvelope struct {
	Version         int             `json:"version"`
	MessageID       string          `json:"messageId"`
	MeshID          string          `json:"meshId"`
	ProjectID       string          `json:"projectId,omitempty"`
	ConversationID  string          `json:"conversationId"`
	SenderUserID    string          `json:"senderUserId"`
	SenderDeviceID  string          `json:"senderDeviceId"`
	SenderHarnessID string          `json:"senderHarnessId,omitempty"`
	Recipient       string          `json:"recipient,omitempty"`
	Type            string          `json:"type"`
	Timestamp       string          `json:"timestamp"`
	CorrelationID   string          `json:"correlationId,omitempty"`
	CausationID     string          `json:"causationId,omitempty"`
	Payload         json.RawMessage `json:"payload"`
	References      []string        `json:"references,omitempty"`
	ExpiresAt       string          `json:"expiresAt,omitempty"`
	Signature       string          `json:"signature"`
}

type wireChatEnvelope struct {
	Version         int      `json:"version"`
	MessageID       string   `json:"messageId"`
	MeshID          string   `json:"meshId"`
	ProjectID       string   `json:"projectId,omitempty"`
	ConversationID  string   `json:"conversationId"`
	SenderUserID    string   `json:"senderUserId"`
	SenderDeviceID  string   `json:"senderDeviceId"`
	SenderHarnessID string   `json:"senderHarnessId,omitempty"`
	Recipient       string   `json:"recipient,omitempty"`
	Type            string   `json:"type"`
	Timestamp       string   `json:"timestamp"`
	CorrelationID   string   `json:"correlationId,omitempty"`
	CausationID     string   `json:"causationId,omitempty"`
	Payload         string   `json:"payload"`
	References      []string `json:"references,omitempty"`
	ExpiresAt       string   `json:"expiresAt,omitempty"`
	Signature       string   `json:"signature"`
}

type chatPayload struct {
	Text       string `json:"text,omitempty"`
	Title      string `json:"title,omitempty"`
	Summary    string `json:"summary,omitempty"`
	FileName   string `json:"fileName,omitempty"`
	MimeType   string `json:"mimeType,omitempty"`
	Data       string `json:"data,omitempty"`
	SessionID  string `json:"sessionId,omitempty"`
	Host       string `json:"host,omitempty"`
	Owner      string `json:"owner,omitempty"`
	Project    string `json:"project,omitempty"`
	Harness    string `json:"harness,omitempty"`
	State      string `json:"state,omitempty"`
	Viewers    int    `json:"viewers,omitempty"`
	Controller string `json:"controller,omitempty"`
	StartSeq   int64  `json:"startSeq,omitempty"`
	EndSeq     int64  `json:"endSeq,omitempty"`

	GroupName    string   `json:"groupName,omitempty"`
	GroupMembers []string `json:"groupMembers,omitempty"`
}

type chatPostRequest struct {
	MeshID         string   `json:"mesh_id"`
	ProjectID      string   `json:"project_id,omitempty"`
	ConversationID string   `json:"conversation_id,omitempty"`
	Recipient      string   `json:"recipient,omitempty"`
	Type           string   `json:"type"`
	Text           string   `json:"text,omitempty"`
	Title          string   `json:"title,omitempty"`
	Summary        string   `json:"summary,omitempty"`
	FileName       string   `json:"file_name,omitempty"`
	MimeType       string   `json:"mime_type,omitempty"`
	Data           string   `json:"data,omitempty"`
	References     []string `json:"references,omitempty"`
	CorrelationID  string   `json:"correlation_id,omitempty"`
	CausationID    string   `json:"causation_id,omitempty"`
	HarnessID      string   `json:"harness_id,omitempty"`
	ExpiresAt      string   `json:"expires_at,omitempty"`
	SessionID      string   `json:"session_id,omitempty"`
	Host           string   `json:"host,omitempty"`
	Project        string   `json:"project,omitempty"`
	State          string   `json:"state,omitempty"`
}

type conversationView struct {
	ConversationID  string   `json:"conversation_id"`
	MeshID          string   `json:"mesh_id"`
	ProjectID       string   `json:"project_id,omitempty"`
	Kind            string   `json:"kind"`
	Name            string   `json:"name,omitempty"`
	MemberDeviceIDs []string `json:"member_device_ids,omitempty"`
	Unread          int      `json:"unread"`
	LastTimestamp   string   `json:"last_timestamp"`
	Preview         string   `json:"preview,omitempty"`
}

type outboxItem struct {
	MessageID string
	MeshID    string
	Topic     string
	QoS       byte
	Wire      []byte
	CreatedAt string
}

var (
	chatMu  sync.Mutex
	chatDBs = map[string]*sql.DB{}
)

func chatDBPath(home string) string {
	return shareDir(home) + "/chat.db"
}

func meshPrefix(meshID string) string {
	return meshTopicRoot + "/" + meshID + "/"
}

func presenceTopic(meshID, deviceID string) string {
	return meshPrefix(meshID) + "presence/" + deviceID
}

func directChatTopic(meshID, conversationID string) string {
	return meshPrefix(meshID) + "chat/direct/" + conversationID
}

func groupChatTopic(meshID, conversationID string) string {
	return meshPrefix(meshID) + "chat/group/" + conversationID
}

func projectChatTopic(meshID, projectID string) string {
	return meshPrefix(meshID) + "chat/project/" + projectID
}

func validTopicSegment(s string) error {
	if s == "" {
		return errors.New("empty topic segment")
	}
	if len(s) > 128 {
		return errors.New("topic segment too long")
	}
	if strings.ContainsAny(s, "/+#\x00\r\n") || strings.TrimSpace(s) != s {
		return errors.New("invalid topic segment")
	}
	return nil
}

func directConversationID(a, b string) string {
	if a > b {
		a, b = b, a
	}
	return "direct:" + a + ":" + b
}

func chatPublishPlan(env chatEnvelope) (string, byte, error) {
	if err := validTopicSegment(env.MeshID); err != nil {
		return "", 0, err
	}
	if env.ProjectID != "" {
		if err := validTopicSegment(env.ProjectID); err != nil {
			return "", 0, err
		}
		return projectChatTopic(env.MeshID, env.ProjectID), 1, nil
	}
	if err := validTopicSegment(env.ConversationID); err != nil {
		return "", 0, err
	}
	if isGroupConversationID(env.ConversationID) {
		return groupChatTopic(env.MeshID, env.ConversationID), 1, nil
	}
	return directChatTopic(env.MeshID, env.ConversationID), 1, nil
}

func openChatDB(home string) (*sql.DB, error) {
	chatMu.Lock()
	defer chatMu.Unlock()
	if db, ok := chatDBs[home]; ok {
		return db, nil
	}
	if err := hookMkdirAll(shareDir(home), 0o700); err != nil {
		return nil, err
	}
	db, err := hookDBOpen("sqlite", chatDBPath(home)+"?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)")
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	if err := initChatSchema(db); err != nil {
		_ = db.Close()
		return nil, err
	}
	_ = hookChmod(chatDBPath(home), 0o600)
	chatDBs[home] = db
	return db, nil
}

func initChatSchema(db *sql.DB) error {
	_, err := db.Exec(`
CREATE TABLE IF NOT EXISTS messages (
  message_id TEXT PRIMARY KEY,
  version INTEGER NOT NULL,
  mesh_id TEXT NOT NULL,
  project_id TEXT NOT NULL DEFAULT '',
  conversation_id TEXT NOT NULL,
  sender_user_id TEXT NOT NULL,
  sender_device_id TEXT NOT NULL,
  sender_harness_id TEXT NOT NULL DEFAULT '',
  recipient TEXT NOT NULL DEFAULT '',
  type TEXT NOT NULL,
  timestamp TEXT NOT NULL,
  correlation_id TEXT NOT NULL DEFAULT '',
  causation_id TEXT NOT NULL DEFAULT '',
  payload TEXT NOT NULL,
  references_json TEXT NOT NULL DEFAULT '[]',
  expires_at TEXT NOT NULL DEFAULT '',
  signature TEXT NOT NULL DEFAULT '',
  unread INTEGER NOT NULL DEFAULT 0
);
CREATE TABLE IF NOT EXISTS outbox (
  message_id TEXT PRIMARY KEY,
  mesh_id TEXT NOT NULL,
  topic TEXT NOT NULL,
  qos INTEGER NOT NULL,
  wire TEXT NOT NULL,
  created_at TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS messages_conv ON messages (mesh_id, conversation_id, timestamp);
`)
	if err != nil {
		return err
	}
	return initChatGroupsSchema(db)
}

func closeChatDBs() {
	chatMu.Lock()
	defer chatMu.Unlock()
	for home, db := range chatDBs {
		_ = db.Close()
		delete(chatDBs, home)
	}
}

func newMessageID() string {
	buf := make([]byte, 16)
	if _, err := hookRandRead(buf); err != nil {
		return "msg_local"
	}
	return "msg_" + hex.EncodeToString(buf)
}

func trimChatPostRequest(req chatPostRequest) chatPostRequest {
	req.MeshID = strings.TrimSpace(req.MeshID)
	req.ProjectID = strings.TrimSpace(req.ProjectID)
	req.ConversationID = strings.TrimSpace(req.ConversationID)
	req.Recipient = strings.TrimSpace(req.Recipient)
	req.Type = strings.TrimSpace(req.Type)
	req.Text = strings.TrimSpace(req.Text)
	req.Title = strings.TrimSpace(req.Title)
	req.Summary = strings.TrimSpace(req.Summary)
	req.FileName = strings.TrimSpace(req.FileName)
	req.MimeType = strings.TrimSpace(req.MimeType)
	req.Data = strings.TrimSpace(req.Data)
	req.CorrelationID = strings.TrimSpace(req.CorrelationID)
	req.CausationID = strings.TrimSpace(req.CausationID)
	req.HarnessID = strings.TrimSpace(req.HarnessID)
	req.ExpiresAt = strings.TrimSpace(req.ExpiresAt)
	return req
}

func validateChatPostText(req *chatPostRequest) error {
	if req.Text == "" {
		return errors.New("text is empty")
	}
	if len(req.Text) > chatTextMax {
		return errors.New("text is too long")
	}
	return nil
}

func validateChatPostCard(req *chatPostRequest) error {
	if req.Title == "" && req.Summary == "" {
		return errors.New("card is empty")
	}
	if len(req.Title) > 200 {
		return errors.New("title is too long")
	}
	if len(req.Summary) > chatSummaryMax {
		return errors.New("summary is too long")
	}
	return nil
}

func validateChatPostFile(req *chatPostRequest) error {
	if req.FileName == "" {
		return errors.New("file name is empty")
	}
	if len(req.FileName) > chatFileNameMax {
		return errors.New("file name is too long")
	}
	if req.Data == "" {
		return errors.New("file data is empty")
	}
	raw, err := base64.StdEncoding.DecodeString(req.Data)
	if err != nil {
		return errors.New("invalid file data")
	}
	if len(raw) > chatFileMaxBytes {
		return errors.New("file is too large")
	}
	if len(req.MimeType) > chatMimeMax {
		return errors.New("mime type is too long")
	}
	if len(req.Text) > chatTextMax {
		return errors.New("text is too long")
	}
	return nil
}

func chatPostValidators() map[string]func(*chatPostRequest) error {
	return map[string]func(*chatPostRequest) error{
		"text":             validateChatPostText,
		"context":          validateChatPostCard,
		"checkpoint":       validateChatPostCard,
		"decision":         validateChatPostCard,
		"handoff":          validateChatPostCard,
		"terminal_session": validateChatPostCard,
		"file":             validateChatPostFile,
	}
}

func validateChatPostTopicFields(req chatPostRequest) error {
	for _, part := range []string{req.ProjectID, req.ConversationID, req.Recipient, req.HarnessID, req.CorrelationID, req.CausationID} {
		if part == "" {
			continue
		}
		if err := validTopicSegment(part); err != nil {
			return err
		}
	}
	if req.ProjectID == "" && req.ConversationID == "" && req.Recipient == "" {
		return errors.New("recipient is required")
	}
	return nil
}

func validateChatPostReferences(refs []string) error {
	if len(refs) > 16 {
		return errors.New("too many references")
	}
	for _, ref := range refs {
		if len(ref) == 0 || len(ref) > 128 || strings.ContainsAny(ref, "\r\n") {
			return errors.New("invalid reference")
		}
	}
	return nil
}

func normalizeChatPost(req chatPostRequest) (chatPostRequest, error) {
	req = trimChatPostRequest(req)
	if err := validTopicSegment(req.MeshID); err != nil {
		return req, err
	}
	validate, ok := chatPostValidators()[req.Type]
	if !ok {
		return req, errors.New("unsupported message type")
	}
	if err := validate(&req); err != nil {
		return req, err
	}
	if err := validateChatPostTopicFields(req); err != nil {
		return req, err
	}
	if req.ConversationID != "" && isGroupConversationID(req.ConversationID) {
		req.Recipient = ""
	}
	if req.ExpiresAt != "" {
		if _, err := time.Parse(time.RFC3339, req.ExpiresAt); err != nil {
			return req, errors.New("invalid expires_at")
		}
	}
	if err := validateChatPostReferences(req.References); err != nil {
		return req, err
	}
	return req, nil
}

func minimalPayload(req chatPostRequest) chatPayload {
	switch req.Type {
	case "text":
		return chatPayload{Text: req.Text}
	case "file":
		return chatPayload{FileName: req.FileName, MimeType: req.MimeType, Data: req.Data, Text: req.Text}
	case "terminal_session":
		return chatPayload{
			Title: req.Title, Summary: req.Summary,
			SessionID: req.SessionID, Host: req.Host, Project: req.Project, State: req.State,
		}
	default:
		return chatPayload{Title: req.Title, Summary: req.Summary}
	}
}

func signChatEnvelope(priv ed25519.PrivateKey, env chatEnvelope) (chatEnvelope, error) {
	env.Signature = ""
	payload, err := hookJSONMarshal(env)
	if err != nil {
		return env, err
	}
	env.Signature = signPayload(priv, payload)
	return env, nil
}

func verifyChatEnvelope(pub ed25519.PublicKey, env chatEnvelope) bool {
	sig := env.Signature
	env.Signature = ""
	payload, err := hookJSONMarshal(env)
	if err != nil {
		return false
	}
	return verifySignature(pub, payload, sig)
}

func sealChatEnvelope(groupKey string, env chatEnvelope) ([]byte, error) {
	cipher, err := encryptBatch(groupKey, []byte(env.Payload))
	if err != nil {
		return nil, err
	}
	wire := wireChatEnvelope{
		Version: env.Version, MessageID: env.MessageID, MeshID: env.MeshID,
		ProjectID: env.ProjectID, ConversationID: env.ConversationID,
		SenderUserID: env.SenderUserID, SenderDeviceID: env.SenderDeviceID,
		SenderHarnessID: env.SenderHarnessID, Recipient: env.Recipient,
		Type: env.Type, Timestamp: env.Timestamp, CorrelationID: env.CorrelationID,
		CausationID: env.CausationID, Payload: cipher, References: env.References,
		ExpiresAt: env.ExpiresAt, Signature: env.Signature,
	}
	return hookJSONMarshal(wire)
}

func openChatWire(groupKey string, body []byte) (chatEnvelope, error) {
	var wire wireChatEnvelope
	if err := json.Unmarshal(body, &wire); err != nil {
		return chatEnvelope{}, err
	}
	plain, err := decryptBatch(groupKey, wire.Payload)
	if err != nil {
		return chatEnvelope{}, err
	}
	return chatEnvelope{
		Version: wire.Version, MessageID: wire.MessageID, MeshID: wire.MeshID,
		ProjectID: wire.ProjectID, ConversationID: wire.ConversationID,
		SenderUserID: wire.SenderUserID, SenderDeviceID: wire.SenderDeviceID,
		SenderHarnessID: wire.SenderHarnessID, Recipient: wire.Recipient,
		Type: wire.Type, Timestamp: wire.Timestamp, CorrelationID: wire.CorrelationID,
		CausationID: wire.CausationID, Payload: json.RawMessage(plain),
		References: wire.References, ExpiresAt: wire.ExpiresAt, Signature: wire.Signature,
	}, nil
}

func prepareLocalChatPost(home string, req chatPostRequest) (chatPostRequest, DeviceIdentity, ed25519.PrivateKey, LocalUser, Circle, error) {
	req, err := normalizeChatPost(req)
	if err != nil {
		return req, DeviceIdentity{}, nil, LocalUser{}, Circle{}, err
	}
	id, priv, err := loadOrCreateIdentity(home)
	if err != nil {
		return req, DeviceIdentity{}, nil, LocalUser{}, Circle{}, err
	}
	profile, err := loadOrCreateLocalUser(home)
	if err != nil {
		return req, DeviceIdentity{}, nil, LocalUser{}, Circle{}, err
	}
	circle, err := loadCircle(home, req.MeshID)
	if err != nil {
		return req, DeviceIdentity{}, nil, LocalUser{}, Circle{}, err
	}
	if _, ok := circle.activeMember(id.DeviceID); !ok {
		return req, DeviceIdentity{}, nil, LocalUser{}, Circle{}, errors.New("not a circle member")
	}
	if req.ConversationID != "" && isGroupConversationID(req.ConversationID) {
		if err := assertCanPostToGroup(home, circle.ID, req.ConversationID, id.DeviceID); err != nil {
			return req, DeviceIdentity{}, nil, LocalUser{}, Circle{}, err
		}
	}
	return req, id, priv, profile, circle, nil
}

func resolvePostConversationID(req chatPostRequest, senderDeviceID string) string {
	switch {
	case req.ConversationID != "":
		return req.ConversationID
	case req.ProjectID != "":
		return chatConversationProjectPrefix + req.ProjectID
	default:
		return directConversationID(senderDeviceID, req.Recipient)
	}
}

// deliverLocalChatPost saves the post, runs afterSave, then publishes. When afterSave fails the saved post
// is dropped, so an error means nothing was posted.
func deliverLocalChatPost(home string, circle Circle, env chatEnvelope, topic string, qos byte, wire []byte, afterSave func() error) (chatEnvelope, bool, error) {
	if err := saveOutgoingChat(home, env, topic, qos, wire); err != nil {
		return chatEnvelope{}, false, err
	}
	if err := afterSave(); err != nil {
		_ = discardOutgoingChat(home, env.MessageID)
		return chatEnvelope{}, false, err
	}
	if !meshAllowsChat() {
		return env, true, nil
	}
	if err := meshTrySend(circle.ID, topic, qos, wire); err != nil {
		return env, true, nil
	}
	_ = deleteOutbox(home, env.MessageID)
	return env, false, nil
}

func postLocalChat(home string, req chatPostRequest) (chatEnvelope, bool, error) {
	req, id, priv, profile, circle, err := prepareLocalChatPost(home, req)
	if err != nil {
		return chatEnvelope{}, false, err
	}
	conv := resolvePostConversationID(req, id.DeviceID)
	var grantees []string
	if req.Type == "terminal_session" {
		if grantees, err = terminalChatShareGrantees(home, circle, id.DeviceID, req.SessionID, conv); err != nil {
			return chatEnvelope{}, false, err
		}
	}
	body := minimalPayload(req)
	body.GroupName, body.GroupMembers = groupPayloadMeta(home, circle.ID, conv)
	payload, err := json.Marshal(body)
	if err != nil {
		return chatEnvelope{}, false, err
	}
	env := chatEnvelope{
		Version: chatEnvelopeVersion, MessageID: newMessageID(), MeshID: circle.ID,
		ProjectID: req.ProjectID, ConversationID: conv, SenderUserID: profile.UserID,
		SenderDeviceID: id.DeviceID, SenderHarnessID: req.HarnessID, Recipient: req.Recipient,
		Type: req.Type, Timestamp: nowRFC3339(), CorrelationID: req.CorrelationID,
		CausationID: req.CausationID, Payload: payload, References: req.References,
		ExpiresAt: req.ExpiresAt,
	}
	if env.Recipient == "" && env.ProjectID != "" {
		env.Recipient = chatConversationProjectPrefix + env.ProjectID
	}
	env, err = signChatEnvelope(priv, env)
	if err != nil {
		return chatEnvelope{}, false, err
	}
	topic, qos, err := chatPublishPlan(env)
	if err != nil {
		return chatEnvelope{}, false, err
	}
	wire, err := sealChatEnvelope(circle.GroupKey, env)
	if err != nil {
		return chatEnvelope{}, false, err
	}
	// The card is saved before anyone gains access, so a failed post never leaves a share without its card.
	return deliverLocalChatPost(home, circle, env, topic, qos, wire, func() error {
		return grantTerminalChatShare(home, req.SessionID, grantees)
	})
}

func saveOutgoingChat(home string, env chatEnvelope, topic string, qos byte, wire []byte) error {
	db, err := openChatDB(home)
	if err != nil {
		return err
	}
	refs, err := json.Marshal(referenceList(env.References))
	if err != nil {
		return err
	}
	chatMu.Lock()
	defer chatMu.Unlock()
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.Exec(`INSERT INTO messages (
		message_id, version, mesh_id, project_id, conversation_id, sender_user_id, sender_device_id,
		sender_harness_id, recipient, type, timestamp, correlation_id, causation_id, payload,
		references_json, expires_at, signature, unread
	) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 0)`,
		env.MessageID, env.Version, env.MeshID, env.ProjectID, env.ConversationID, env.SenderUserID,
		env.SenderDeviceID, env.SenderHarnessID, env.Recipient, env.Type, env.Timestamp,
		env.CorrelationID, env.CausationID, string(env.Payload), string(refs), env.ExpiresAt, env.Signature,
	); err != nil {
		return err
	}
	if _, err := tx.Exec(`INSERT INTO outbox (message_id, mesh_id, topic, qos, wire, created_at)
		VALUES (?, ?, ?, ?, ?, ?)`,
		env.MessageID, env.MeshID, topic, int(qos), string(wire), env.Timestamp,
	); err != nil {
		return err
	}
	return tx.Commit()
}

func referenceList(refs []string) []string {
	if refs == nil {
		return []string{}
	}
	return refs
}

// acceptChatWire stores a delivered MQTT payload. A repeated messageId does not
// create a second row and does not change unread state.
func acceptChatWire(home string, body []byte) (bool, error) {
	var peek struct {
		MeshID string `json:"meshId"`
	}
	if err := json.Unmarshal(body, &peek); err != nil {
		return false, err
	}
	circle, err := loadCircle(home, peek.MeshID)
	if err != nil {
		return false, err
	}
	env, err := openCircleChatWire(circle, body)
	if err != nil {
		return false, err
	}
	if env.MeshID != circle.ID {
		return false, errors.New("mesh id mismatch")
	}
	member, ok := circle.activeMember(env.SenderDeviceID)
	if !ok {
		return false, errors.New("sender is not a circle member")
	}
	pub, err := decodePublicKey(member.PublicKey)
	if err != nil {
		return false, err
	}
	if !verifyChatEnvelope(pub, env) {
		return false, errors.New("bad chat signature")
	}
	selfID := existingDeviceID(home)
	if admit, err := admitInboundChat(home, circle, env, selfID); err != nil || !admit {
		return false, err
	}
	unread := 1
	if selfID == env.SenderDeviceID {
		unread = 0
	}
	return insertChatMessage(home, env, unread)
}

func insertChatMessage(home string, env chatEnvelope, unread int) (bool, error) {
	db, err := openChatDB(home)
	if err != nil {
		return false, err
	}
	refs, err := json.Marshal(referenceList(env.References))
	if err != nil {
		return false, err
	}
	chatMu.Lock()
	defer chatMu.Unlock()
	var existing int
	if err := db.QueryRow(`SELECT COUNT(*) FROM messages WHERE message_id = ?`, env.MessageID).Scan(&existing); err != nil {
		return false, err
	}
	if existing > 0 {
		return false, nil
	}
	if _, err := db.Exec(`INSERT INTO messages (
		message_id, version, mesh_id, project_id, conversation_id, sender_user_id, sender_device_id,
		sender_harness_id, recipient, type, timestamp, correlation_id, causation_id, payload,
		references_json, expires_at, signature, unread
	) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		env.MessageID, env.Version, env.MeshID, env.ProjectID, env.ConversationID, env.SenderUserID,
		env.SenderDeviceID, env.SenderHarnessID, env.Recipient, env.Type, env.Timestamp,
		env.CorrelationID, env.CausationID, string(env.Payload), string(refs), env.ExpiresAt, env.Signature, unread,
	); err != nil {
		return false, err
	}
	return true, nil
}

func loadOutbox(home, meshID string) ([]outboxItem, error) {
	db, err := openChatDB(home)
	if err != nil {
		return nil, err
	}
	chatMu.Lock()
	defer chatMu.Unlock()
	rows, err := db.Query(`SELECT message_id, mesh_id, topic, qos, wire, created_at FROM outbox
		WHERE mesh_id = ? ORDER BY created_at, message_id`, meshID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []outboxItem
	for rows.Next() {
		var item outboxItem
		var qos int
		var wire string
		if err := rows.Scan(&item.MessageID, &item.MeshID, &item.Topic, &qos, &wire, &item.CreatedAt); err != nil {
			return nil, err
		}
		item.QoS = byte(qos)
		item.Wire = []byte(wire)
		out = append(out, item)
	}
	return out, rows.Err()
}

func deleteOutbox(home, messageID string) error {
	db, err := openChatDB(home)
	if err != nil {
		return err
	}
	chatMu.Lock()
	defer chatMu.Unlock()
	_, err = db.Exec(`DELETE FROM outbox WHERE message_id = ?`, messageID)
	return err
}

// discardOutgoingChat removes a saved post that must not go out.
func discardOutgoingChat(home, messageID string) error {
	db, err := openChatDB(home)
	if err != nil {
		return err
	}
	chatMu.Lock()
	defer chatMu.Unlock()
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.Exec(`DELETE FROM outbox WHERE message_id = ?`, messageID); err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM messages WHERE message_id = ?`, messageID); err != nil {
		return err
	}
	return tx.Commit()
}

func flushOutbox(home, meshID string, send func(topic string, qos byte, body []byte) error) error {
	if !meshAllowsChat() {
		return nil
	}
	items, err := loadOutbox(home, meshID)
	if err != nil {
		return err
	}
	for _, item := range items {
		if !meshAllowsChat() {
			return nil
		}
		if err := send(item.Topic, item.QoS, item.Wire); err != nil {
			return err
		}
		if err := deleteOutbox(home, item.MessageID); err != nil {
			return err
		}
	}
	return nil
}

func listChatMessages(home, meshID, conversationID string, markRead bool) ([]chatEnvelope, error) {
	if markRead {
		if err := markConversationRead(home, meshID, conversationID); err != nil {
			return nil, err
		}
	}
	db, err := openChatDB(home)
	if err != nil {
		return nil, err
	}
	chatMu.Lock()
	defer chatMu.Unlock()
	rows, err := db.Query(`SELECT version, message_id, mesh_id, project_id, conversation_id, sender_user_id,
		sender_device_id, sender_harness_id, recipient, type, timestamp, correlation_id, causation_id,
		payload, references_json, expires_at, signature
		FROM messages WHERE mesh_id = ? AND conversation_id = ? ORDER BY timestamp, rowid`, meshID, conversationID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	now := time.Now().UTC()
	out := []chatEnvelope{}
	for rows.Next() {
		env, err := scanChatEnvelope(rows)
		if err != nil {
			return nil, err
		}
		if !messageVisible(env, now) {
			continue
		}
		out = append(out, env)
	}
	return out, rows.Err()
}

type chatScanner interface {
	Scan(dest ...any) error
}

func scanChatEnvelope(rows chatScanner) (chatEnvelope, error) {
	var env chatEnvelope
	var payload, refs string
	if err := rows.Scan(&env.Version, &env.MessageID, &env.MeshID, &env.ProjectID, &env.ConversationID,
		&env.SenderUserID, &env.SenderDeviceID, &env.SenderHarnessID, &env.Recipient, &env.Type,
		&env.Timestamp, &env.CorrelationID, &env.CausationID, &payload, &refs, &env.ExpiresAt, &env.Signature); err != nil {
		return chatEnvelope{}, err
	}
	env.Payload = json.RawMessage(payload)
	if refs != "" && refs != "null" {
		_ = json.Unmarshal([]byte(refs), &env.References)
	}
	return env, nil
}

func messageVisible(env chatEnvelope, now time.Time) bool {
	if env.ExpiresAt == "" {
		return true
	}
	t, err := time.Parse(time.RFC3339, env.ExpiresAt)
	if err != nil {
		return true
	}
	return !now.After(t)
}

func markConversationRead(home, meshID, conversationID string) error {
	db, err := openChatDB(home)
	if err != nil {
		return err
	}
	chatMu.Lock()
	defer chatMu.Unlock()
	_, err = db.Exec(`UPDATE messages SET unread = 0 WHERE mesh_id = ? AND conversation_id = ?`, meshID, conversationID)
	return err
}

func listChatConversations(home, meshID string) ([]conversationView, error) {
	db, err := openChatDB(home)
	if err != nil {
		return nil, err
	}
	chatMu.Lock()
	defer chatMu.Unlock()
	rows, err := db.Query(`SELECT conversation_id, project_id, type, timestamp, payload, unread, expires_at
		FROM messages WHERE mesh_id = ? ORDER BY timestamp, rowid`, meshID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	now := time.Now().UTC()
	byID := map[string]*conversationView{}
	order := []string{}
	for rows.Next() {
		var convID, projectID, typ, ts, payload, expires string
		var unread int
		if err := rows.Scan(&convID, &projectID, &typ, &ts, &payload, &unread, &expires); err != nil {
			return nil, err
		}
		env := chatEnvelope{ConversationID: convID, ProjectID: projectID, Type: typ, Timestamp: ts, ExpiresAt: expires, Payload: json.RawMessage(payload)}
		if !messageVisible(env, now) {
			continue
		}
		view, ok := byID[convID]
		if !ok {
			view = &conversationView{
				ConversationID: convID,
				MeshID:         meshID,
				ProjectID:      projectID,
				Kind:           conversationKind(projectID, convID),
			}
			byID[convID] = view
			order = append(order, convID)
		}
		view.Unread += unread
		view.LastTimestamp = ts
		view.Preview = payloadPreview(json.RawMessage(payload))
		if projectID != "" {
			view.ProjectID = projectID
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	out := make([]conversationView, 0, len(order))
	for _, id := range order {
		out = append(out, *byID[id])
	}
	return out, nil
}

func conversationKind(projectID, conversationID string) string {
	if projectID != "" || strings.HasPrefix(conversationID, chatConversationProjectPrefix) {
		return "project"
	}
	if conversationID == circleGroupConversationID() {
		return "circle"
	}
	if isGroupConversationID(conversationID) {
		return "group"
	}
	return "direct"
}

func payloadPreview(raw json.RawMessage) string {
	var payload chatPayload
	if json.Unmarshal(raw, &payload) != nil {
		return ""
	}
	text := payload.Text
	if text == "" {
		text = payload.Title
	}
	if text == "" {
		text = payload.Summary
	}
	if text == "" {
		text = payload.FileName
	}
	runes := []rune(text)
	if len(runes) > 80 {
		return string(runes[:80])
	}
	return text
}

func serveDiscoveryPeers(home string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, methodNotAllowedMsg, http.StatusMethodNotAllowed)
			return
		}
		if rejectRemoteCoreAdmin(w, r) {
			return
		}
		peers, err := listDiscoveredPeers(home)
		if err != nil {
			writeChatError(w, err)
			return
		}
		writeMeshJSON(w, http.StatusOK, map[string]any{"peers": peers}, "")
	}
}

func serveDiscoveryForget(home string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, methodNotAllowedMsg, http.StatusMethodNotAllowed)
			return
		}
		if rejectRemoteCoreAdmin(w, r) {
			return
		}
		var payload struct {
			DeviceID string `json:"device_id"`
		}
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&payload); err != nil {
			http.Error(w, badRequestMsg, http.StatusBadRequest)
			return
		}
		if err := ignoreDiscoveredPeer(home, payload.DeviceID); err != nil {
			writeChatError(w, err)
			return
		}
		writeMeshJSON(w, http.StatusOK, map[string]any{"ok": true}, "")
	}
}

func serveChatConversations(home string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, methodNotAllowedMsg, http.StatusMethodNotAllowed)
			return
		}
		if rejectRemoteCoreAdmin(w, r) {
			return
		}
		meshID := strings.TrimSpace(r.URL.Query().Get("mesh_id"))
		if err := validTopicSegment(meshID); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		conversations, err := listChatConversations(home, meshID)
		if err != nil {
			writeChatError(w, err)
			return
		}
		conversations, err = mergeGroupConversations(home, meshID, conversations)
		if err != nil {
			writeChatError(w, err)
			return
		}
		secret := circleGroupKey(home, meshID)
		writeMeshJSON(w, http.StatusOK, map[string]any{"conversations": conversations}, secret)
	}
}

func serveChatMessages(home string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if rejectRemoteCoreAdmin(w, r) {
			return
		}
		switch r.Method {
		case http.MethodGet:
			serveChatHistory(w, r, home)
		case http.MethodPost:
			serveChatPost(w, r, home)
		default:
			http.Error(w, methodNotAllowedMsg, http.StatusMethodNotAllowed)
		}
	}
}

func registerMeshAPIRoutes(mux *http.ServeMux, home string) {
	mux.HandleFunc("/api/discovery/peers", serveDiscoveryPeers(home))
	mux.HandleFunc("/api/discovery/forget", serveDiscoveryForget(home))
	mux.HandleFunc("/api/chat/conversations", serveChatConversations(home))
	registerChatGroupAPIRoutes(mux, home)
	mux.HandleFunc("/api/chat/messages", serveChatMessages(home))
	registerCallAPIRoutes(mux, home)
}

func serveChatHistory(w http.ResponseWriter, r *http.Request, home string) {
	meshID := strings.TrimSpace(r.URL.Query().Get("mesh_id"))
	conversationID := strings.TrimSpace(r.URL.Query().Get("conversation_id"))
	if err := validTopicSegment(meshID); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if err := validTopicSegment(conversationID); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	mark := r.URL.Query().Get("mark_read") == "1" || r.URL.Query().Get("mark_read") == "true"
	messages, err := listChatMessages(home, meshID, conversationID, mark)
	if err != nil {
		writeChatError(w, err)
		return
	}
	unread := 0
	if !mark {
		convs, err := listChatConversations(home, meshID)
		if err != nil {
			writeChatError(w, err)
			return
		}
		for _, conv := range convs {
			if conv.ConversationID == conversationID {
				unread = conv.Unread
			}
		}
	}
	writeMeshJSON(w, http.StatusOK, map[string]any{"messages": messages, "unread": unread}, circleGroupKey(home, meshID))
}

func serveChatPost(w http.ResponseWriter, r *http.Request, home string) {
	r.Body = http.MaxBytesReader(w, r.Body, chatHTTPBodyMax)
	var req chatPostRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, badRequestMsg, http.StatusBadRequest)
		return
	}
	env, queued, err := postLocalChat(home, req)
	if err != nil {
		writeChatError(w, err)
		return
	}
	writeMeshJSON(w, http.StatusOK, map[string]any{"queued": queued, "message": env}, circleGroupKey(home, env.MeshID))
}

func circleGroupKey(home, meshID string) string {
	circle, err := loadCircle(home, meshID)
	if err != nil {
		return ""
	}
	return circle.GroupKey
}

func writeMeshJSON(w http.ResponseWriter, status int, v any, secret string) {
	raw, err := json.Marshal(v)
	if err != nil {
		http.Error(w, shareChatRequestFailedMsg, http.StatusInternalServerError)
		return
	}
	if err := rejectGroupKeyLeak(string(raw), secret); err != nil {
		http.Error(w, shareChatRequestFailedMsg, http.StatusInternalServerError)
		return
	}
	w.Header().Set(headerContentType, "application/json")
	w.Header().Set(headerCacheControl, cacheNoStore)
	w.WriteHeader(status)
	_, _ = w.Write(append(raw, '\n'))
}

func writeChatError(w http.ResponseWriter, err error) {
	if err == nil {
		return
	}
	msg := err.Error()
	switch {
	case os.IsNotExist(err) || strings.Contains(msg, "no such file"):
		http.Error(w, "circle not found", http.StatusNotFound)
	case strings.Contains(msg, "not a circle member") || errors.Is(err, errNotGroupMember) || errors.Is(err, errCallHostOnly):
		http.Error(w, msg, http.StatusForbidden)
	case errors.Is(err, errUnknownGroupConversation):
		http.Error(w, msg, http.StatusNotFound)
	case errors.Is(err, errMemberNotInCircle):
		http.Error(w, msg, http.StatusBadRequest)
	case strings.Contains(msg, "empty") || strings.Contains(msg, "invalid") || strings.Contains(msg, "unsupported") || strings.Contains(msg, "required") || strings.Contains(msg, "too "):
		http.Error(w, msg, http.StatusBadRequest)
	default:
		http.Error(w, shareChatRequestFailedMsg, http.StatusInternalServerError)
	}
}
