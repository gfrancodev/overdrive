package main

import (
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
)

const (
	chatGroupNameMax              = 120
	chatGroupMembersMax           = 32
	chatConversationGroupPrefix   = "group:"
)

func circleGroupConversationID() string {
	return chatConversationGroupPrefix + "circle"
}

type chatGroupRecord struct {
	GroupID          string   `json:"group_id"`
	MeshID           string   `json:"mesh_id"`
	ConversationID   string   `json:"conversation_id"`
	Name             string   `json:"name"`
	Kind             string   `json:"kind"`
	MemberDeviceIDs  []string `json:"member_device_ids"`
	CreatedAt        string   `json:"created_at"`
	CreatedByDevice  string   `json:"created_by_device_id"`
	CreatedByUserID  string   `json:"created_by_user_id"`
}

type chatGroupCreateRequest struct {
	MeshID          string   `json:"mesh_id"`
	Name            string   `json:"name"`
	MemberDeviceIDs []string `json:"member_device_ids"`
}

func isGroupConversationID(conversationID string) bool {
	return strings.HasPrefix(conversationID, chatConversationGroupPrefix)
}

func initChatGroupsSchema(db *sql.DB) error {
	_, err := db.Exec(`
CREATE TABLE IF NOT EXISTS chat_groups (
  group_id TEXT PRIMARY KEY,
  mesh_id TEXT NOT NULL,
  conversation_id TEXT NOT NULL,
  name TEXT NOT NULL,
  kind TEXT NOT NULL DEFAULT 'custom',
  member_devices_json TEXT NOT NULL DEFAULT '[]',
  created_at TEXT NOT NULL,
  created_by_device_id TEXT NOT NULL,
  created_by_user_id TEXT NOT NULL,
  UNIQUE (mesh_id, conversation_id)
);
CREATE INDEX IF NOT EXISTS chat_groups_mesh ON chat_groups (mesh_id);
`)
	return err
}

func ensureCircleGroupChannel(home, meshID, creatorDeviceID, creatorUserID string) error {
	db, err := openChatDB(home)
	if err != nil {
		return err
	}
	members, err := activeCircleDeviceIDs(home, meshID)
	if err != nil {
		return err
	}
	refs, err := json.Marshal(members)
	if err != nil {
		return err
	}
	chatMu.Lock()
	defer chatMu.Unlock()
	_, err = db.Exec(`INSERT INTO chat_groups (
		group_id, mesh_id, conversation_id, name, kind, member_devices_json,
		created_at, created_by_device_id, created_by_user_id
	) VALUES (?, ?, ?, ?, 'circle', ?, ?, ?, ?)
	ON CONFLICT (mesh_id, conversation_id) DO UPDATE SET member_devices_json = excluded.member_devices_json`,
		"grp_circle_"+meshID, meshID, circleGroupConversationID(), "Circle",
		string(refs), nowRFC3339(), creatorDeviceID, creatorUserID,
	)
	return err
}

func activeCircleDeviceIDs(home, meshID string) ([]string, error) {
	circle, err := loadCircle(home, meshID)
	if err != nil {
		return nil, err
	}
	out := []string{}
	for _, m := range circle.Members {
		if m.Revoked {
			continue
		}
		if m.DeviceID != "" {
			out = append(out, m.DeviceID)
		}
	}
	return out, nil
}

func newGroupID() string {
	buf := make([]byte, 8)
	if _, err := hookRandRead(buf); err != nil {
		return "grp_local"
	}
	return "grp_" + hex.EncodeToString(buf)
}

func dedupeValidDeviceIDs(ids []string) ([]string, error) {
	seen := map[string]bool{}
	out := []string{}
	for _, id := range ids {
		id = strings.TrimSpace(id)
		if id == "" || seen[id] {
			continue
		}
		if err := validTopicSegment(id); err != nil {
			return nil, err
		}
		seen[id] = true
		out = append(out, id)
	}
	return out, nil
}

func normalizeGroupCreate(req chatGroupCreateRequest) (chatGroupCreateRequest, error) {
	req.MeshID = strings.TrimSpace(req.MeshID)
	req.Name = strings.TrimSpace(req.Name)
	if err := validTopicSegment(req.MeshID); err != nil {
		return req, err
	}
	if req.Name == "" {
		return req, errors.New("name is required")
	}
	if len([]rune(req.Name)) > chatGroupNameMax {
		return req, errors.New("name is too long")
	}
	if len(req.MemberDeviceIDs) > chatGroupMembersMax {
		return req, errors.New("too many members")
	}
	out, err := dedupeValidDeviceIDs(req.MemberDeviceIDs)
	if err != nil {
		return req, err
	}
	req.MemberDeviceIDs = out
	return req, nil
}

func createChatGroup(home string, req chatGroupCreateRequest) (chatGroupRecord, error) {
	req, err := normalizeGroupCreate(req)
	if err != nil {
		return chatGroupRecord{}, err
	}
	id, _, err := loadOrCreateIdentity(home)
	if err != nil {
		return chatGroupRecord{}, err
	}
	profile, err := loadOrCreateLocalUser(home)
	if err != nil {
		return chatGroupRecord{}, err
	}
	circle, err := loadCircle(home, req.MeshID)
	if err != nil {
		return chatGroupRecord{}, err
	}
	if _, ok := circle.activeMember(id.DeviceID); !ok {
		return chatGroupRecord{}, errors.New("not a circle member")
	}
	members := append([]string{id.DeviceID}, req.MemberDeviceIDs...)
	seen := map[string]bool{id.DeviceID: true}
	unique := []string{id.DeviceID}
	for _, dev := range req.MemberDeviceIDs {
		if seen[dev] {
			continue
		}
		if _, ok := circle.activeMember(dev); !ok {
			return chatGroupRecord{}, errMemberNotInCircle
		}
		seen[dev] = true
		unique = append(unique, dev)
	}
	if len(unique) < 2 {
		return chatGroupRecord{}, errors.New("at least one other member is required")
	}
	members = unique
	refs, err := json.Marshal(members)
	if err != nil {
		return chatGroupRecord{}, err
	}
	groupID := newGroupID()
	convID := chatConversationGroupPrefix + groupID
	rec := chatGroupRecord{
		GroupID: groupID, MeshID: circle.ID, ConversationID: convID, Name: req.Name,
		Kind: "custom", MemberDeviceIDs: members, CreatedAt: nowRFC3339(),
		CreatedByDevice: id.DeviceID, CreatedByUserID: profile.UserID,
	}
	db, err := openChatDB(home)
	if err != nil {
		return chatGroupRecord{}, err
	}
	chatMu.Lock()
	defer chatMu.Unlock()
	if _, err := db.Exec(`INSERT INTO chat_groups (
		group_id, mesh_id, conversation_id, name, kind, member_devices_json,
		created_at, created_by_device_id, created_by_user_id
	) VALUES (?, ?, ?, ?, 'custom', ?, ?, ?, ?)`,
		rec.GroupID, rec.MeshID, rec.ConversationID, rec.Name, string(refs),
		rec.CreatedAt, rec.CreatedByDevice, rec.CreatedByUserID,
	); err != nil {
		return chatGroupRecord{}, err
	}
	return rec, nil
}

func listChatGroups(home, meshID string) ([]chatGroupRecord, error) {
	id, _, err := loadOrCreateIdentity(home)
	if err != nil {
		return nil, err
	}
	profile, err := loadOrCreateLocalUser(home)
	if err != nil {
		return nil, err
	}
	if err := ensureCircleGroupChannel(home, meshID, id.DeviceID, profile.UserID); err != nil {
		return nil, err
	}
	db, err := openChatDB(home)
	if err != nil {
		return nil, err
	}
	chatMu.Lock()
	defer chatMu.Unlock()
	rows, err := db.Query(`SELECT group_id, mesh_id, conversation_id, name, kind, member_devices_json,
		created_at, created_by_device_id, created_by_user_id
		FROM chat_groups WHERE mesh_id = ? ORDER BY kind DESC, name, conversation_id`, meshID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []chatGroupRecord{}
	for rows.Next() {
		rec, err := scanChatGroup(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, rec)
	}
	return out, rows.Err()
}

func scanChatGroup(rows *sql.Rows) (chatGroupRecord, error) {
	var rec chatGroupRecord
	var membersJSON string
	if err := rows.Scan(&rec.GroupID, &rec.MeshID, &rec.ConversationID, &rec.Name, &rec.Kind,
		&membersJSON, &rec.CreatedAt, &rec.CreatedByDevice, &rec.CreatedByUserID); err != nil {
		return chatGroupRecord{}, err
	}
	if membersJSON != "" {
		_ = json.Unmarshal([]byte(membersJSON), &rec.MemberDeviceIDs)
	}
	return rec, nil
}

func loadChatGroupByConversation(home, meshID, conversationID string) (chatGroupRecord, error) {
	db, err := openChatDB(home)
	if err != nil {
		return chatGroupRecord{}, err
	}
	chatMu.Lock()
	defer chatMu.Unlock()
	row := db.QueryRow(`SELECT group_id, mesh_id, conversation_id, name, kind, member_devices_json,
		created_at, created_by_device_id, created_by_user_id
		FROM chat_groups WHERE mesh_id = ? AND conversation_id = ?`, meshID, conversationID)
	var rec chatGroupRecord
	var membersJSON string
	if err := row.Scan(&rec.GroupID, &rec.MeshID, &rec.ConversationID, &rec.Name, &rec.Kind,
		&membersJSON, &rec.CreatedAt, &rec.CreatedByDevice, &rec.CreatedByUserID); err != nil {
		return chatGroupRecord{}, err
	}
	if membersJSON != "" {
		_ = json.Unmarshal([]byte(membersJSON), &rec.MemberDeviceIDs)
	}
	return rec, nil
}

func memberOfChatGroup(rec chatGroupRecord, deviceID string) bool {
	for _, id := range rec.MemberDeviceIDs {
		if id == deviceID {
			return true
		}
	}
	return false
}

func assertCanPostToGroup(home, meshID, conversationID, deviceID string) error {
	if conversationID == circleGroupConversationID() {
		circle, err := loadCircle(home, meshID)
		if err != nil {
			return err
		}
		if _, ok := circle.activeMember(deviceID); !ok {
			return errors.New("not a circle member")
		}
		return nil
	}
	rec, err := loadChatGroupByConversation(home, meshID, conversationID)
	if err != nil {
		return errUnknownGroupConversation
	}
	if !memberOfChatGroup(rec, deviceID) {
		return errNotGroupMember
	}
	return nil
}

var (
	errUnknownGroupConversation = errors.New("unknown group conversation")
	errNotGroupMember           = errors.New("not a group member")
	errMemberNotInCircle        = errors.New("member is not in circle")
)

// groupPayloadMeta carries the custom group roster inside the encrypted payload
// so members that did not create the group can name it and reply.
func groupPayloadMeta(home, meshID, conversationID string) (string, []string) {
	if !isGroupConversationID(conversationID) || conversationID == circleGroupConversationID() {
		return "", nil
	}
	rec, err := loadChatGroupByConversation(home, meshID, conversationID)
	if err != nil {
		return "", nil
	}
	return rec.Name, rec.MemberDeviceIDs
}

// admitInboundChat reports whether this device should keep a delivered
// message. Every circle member receives every chat topic, so direct and custom
// group threads are filtered to their participants here.
func admitDirectInboundChat(conv, selfID, senderID string) (bool, error) {
	if !strings.HasPrefix(conv, "direct:") {
		return false, nil
	}
	parts := strings.Split(conv, ":")
	if len(parts) != 3 {
		return false, nil
	}
	return (parts[1] == selfID || parts[2] == selfID) &&
		(parts[1] == senderID || parts[2] == senderID), nil
}

func admitBootstrapGroupChat(home string, circle Circle, env chatEnvelope, selfID, conv string) (bool, error) {
	var payload chatPayload
	if json.Unmarshal(env.Payload, &payload) != nil {
		return false, nil
	}
	members := []string{}
	seen := map[string]bool{}
	for _, id := range payload.GroupMembers {
		if seen[id] {
			continue
		}
		if _, ok := circle.activeMember(id); !ok {
			continue
		}
		seen[id] = true
		members = append(members, id)
	}
	if !seen[selfID] || !seen[env.SenderDeviceID] || len(members) > chatGroupMembersMax {
		return false, nil
	}
	name := strings.TrimSpace(payload.GroupName)
	if name == "" || len([]rune(name)) > chatGroupNameMax {
		name = "Group"
	}
	refs, err := json.Marshal(members)
	if err != nil {
		return false, err
	}
	db, err := openChatDB(home)
	if err != nil {
		return false, err
	}
	chatMu.Lock()
	defer chatMu.Unlock()
	_, err = db.Exec(`INSERT OR IGNORE INTO chat_groups (
		group_id, mesh_id, conversation_id, name, kind, member_devices_json,
		created_at, created_by_device_id, created_by_user_id
	) VALUES (?, ?, ?, ?, 'custom', ?, ?, ?, ?)`,
		strings.TrimPrefix(conv, chatConversationGroupPrefix), circle.ID, conv, name, string(refs),
		env.Timestamp, env.SenderDeviceID, env.SenderUserID,
	)
	return err == nil, err
}

func admitInboundChat(home string, circle Circle, env chatEnvelope, selfID string) (bool, error) {
	conv := env.ConversationID
	if strings.HasPrefix(conv, "direct:") {
		return admitDirectInboundChat(conv, selfID, env.SenderDeviceID)
	}
	if !isGroupConversationID(conv) || conv == circleGroupConversationID() {
		return true, nil
	}
	if rec, err := loadChatGroupByConversation(home, circle.ID, conv); err == nil {
		return memberOfChatGroup(rec, selfID) && memberOfChatGroup(rec, env.SenderDeviceID), nil
	} else if !errors.Is(err, sql.ErrNoRows) {
		return false, err
	}
	return admitBootstrapGroupChat(home, circle, env, selfID, conv)
}

func mergeGroupConversations(home, meshID string, convs []conversationView) ([]conversationView, error) {
	groups, err := listChatGroups(home, meshID)
	if err != nil {
		return convs, err
	}
	byID := map[string]int{}
	for i, c := range convs {
		byID[c.ConversationID] = i
	}
	for _, g := range groups {
		kind := "group"
		if g.Kind == "circle" {
			kind = "circle"
		}
		if idx, ok := byID[g.ConversationID]; ok {
			convs[idx].Kind = kind
			convs[idx].Name = g.Name
			convs[idx].MemberDeviceIDs = g.MemberDeviceIDs
			continue
		}
		convs = append(convs, conversationView{
			ConversationID:  g.ConversationID,
			MeshID:          meshID,
			Kind:            kind,
			Name:            g.Name,
			MemberDeviceIDs: g.MemberDeviceIDs,
			LastTimestamp:   g.CreatedAt,
		})
	}
	return convs, nil
}

func serveChatGroupsList(home string, w http.ResponseWriter, r *http.Request) {
	meshID := strings.TrimSpace(r.URL.Query().Get("mesh_id"))
	if err := validTopicSegment(meshID); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	groups, err := listChatGroups(home, meshID)
	if err != nil {
		writeChatError(w, err)
		return
	}
	writeMeshJSON(w, http.StatusOK, map[string]any{"groups": groups}, circleGroupKey(home, meshID))
}

func serveChatGroupsCreate(home string, w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, chatHTTPBodyMax)
	raw, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, badRequestMsg, http.StatusBadRequest)
		return
	}
	var req chatGroupCreateRequest
	if err := json.Unmarshal(raw, &req); err != nil {
		http.Error(w, badRequestMsg, http.StatusBadRequest)
		return
	}
	group, err := createChatGroup(home, req)
	if err != nil {
		writeChatError(w, err)
		return
	}
	writeMeshJSON(w, http.StatusOK, map[string]any{"group": group}, circleGroupKey(home, group.MeshID))
}

func serveChatGroups(home string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if rejectRemoteCoreAdmin(w, r) {
			return
		}
		switch r.Method {
		case http.MethodGet:
			serveChatGroupsList(home, w, r)
		case http.MethodPost:
			serveChatGroupsCreate(home, w, r)
		default:
			http.Error(w, methodNotAllowedMsg, http.StatusMethodNotAllowed)
		}
	}
}

func registerChatGroupAPIRoutes(mux *http.ServeMux, home string) {
	mux.HandleFunc("/api/chat/groups", serveChatGroups(home))
}
