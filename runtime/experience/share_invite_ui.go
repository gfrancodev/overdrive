package main

import (
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"path/filepath"
	"strings"
	"time"
)

func inviteTTLSeconds() int {
	return int(inviteTTL() / time.Second)
}

func registerCircleInviteRoutes(mux *http.ServeMux, home string) {
	mux.HandleFunc("/api/circle/invite", func(w http.ResponseWriter, r *http.Request) {
		if rejectRemoteCoreAdmin(w, r) {
			return
		}
		switch r.Method {
		case http.MethodGet:
			writeInviteJSON(w, http.StatusOK, map[string]int{"ttl_seconds": inviteTTLSeconds()})
		case http.MethodPost:
			serveCircleInviteIssue(w, r, home)
		default:
			http.Error(w, methodNotAllowedMsg, http.StatusMethodNotAllowed)
		}
	})
	mux.HandleFunc("/api/circle/join", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, methodNotAllowedMsg, http.StatusMethodNotAllowed)
			return
		}
		if rejectRemoteCoreAdmin(w, r) {
			return
		}
		serveCircleJoin(w, r, home)
	})
}

func serveCircleInviteIssue(w http.ResponseWriter, r *http.Request, home string) {
	var body struct {
		CircleID string `json:"circle_id"`
	}
	if r.Body != nil {
		r.Body = http.MaxBytesReader(w, r.Body, 4*1024)
		_ = json.NewDecoder(r.Body).Decode(&body)
	}
	invite, err := issueCircleInvite(home, strings.TrimSpace(body.CircleID))
	if err != nil {
		writeInviteError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeInviteJSON(w, http.StatusOK, map[string]any{
		"ttl_seconds": inviteTTLSeconds(),
		"expires_at":  invite.ExpiresAt,
		"invite":      invite,
	})
}

func issueCircleInvite(home, circleID string) (CircleInvite, error) {
	id, _, err := loadOrCreateIdentity(home)
	if err != nil {
		return CircleInvite{}, err
	}
	circle, err := circleForInvite(home, id, circleID)
	if err != nil {
		return CircleInvite{}, err
	}
	return createInvite(home, circle.ID, id)
}

// circleForInvite resolves the circle an invite is for. The caller names it; only a device in exactly one
// circle may leave it out. An invite never creates a circle on its own.
func circleForInvite(home string, id DeviceIdentity, circleID string) (Circle, error) {
	circles, err := memberCircles(home, id.DeviceID)
	if err != nil {
		return Circle{}, err
	}
	if circleID != "" {
		for _, circle := range circles {
			if circle.ID == circleID {
				return circle, nil
			}
		}
		return Circle{}, errors.New("this device is not in that circle")
	}
	switch len(circles) {
	case 0:
		return Circle{}, errors.New("create a circle before inviting")
	case 1:
		return circles[0], nil
	default:
		return Circle{}, errors.New("choose the circle to invite into")
	}
}

func serveCircleJoin(w http.ResponseWriter, r *http.Request, home string) {
	r.Body = http.MaxBytesReader(w, r.Body, 64*1024)
	var invite CircleInvite
	if err := json.NewDecoder(r.Body).Decode(&invite); err != nil {
		writeInviteError(w, http.StatusBadRequest, "bad request")
		return
	}
	circle, err := registerDeviceWithInvite(home, invite)
	if err != nil {
		writeInviteError(w, inviteErrorStatus(err), err.Error())
		return
	}
	writeInviteJSON(w, http.StatusOK, map[string]any{
		"ok":        true,
		"circle_id": circle.ID,
		"name":      circle.Name,
	})
}

func registerDeviceWithInvite(home string, invite CircleInvite) (Circle, error) {
	if !inviteCodeSafe(invite.Code) || !inviteCircleIDSafe(invite.CircleID) {
		return Circle{}, errors.New("invite not found")
	}
	if invite.Fingerprint != hashHex([]byte(invite.CircleID + invite.Code))[:12] {
		return Circle{}, errors.New("invite fingerprint mismatch")
	}
	id, priv, err := loadOrCreateIdentity(home)
	if err != nil {
		return Circle{}, err
	}
	if err := storeInviteFile(home, invite); err != nil {
		return Circle{}, err
	}
	if joinInviteValid(invite, time.Now()) {
		if existing, err := loadCircle(home, invite.CircleID); err == nil {
			if _, ok := existing.activeMember(id.DeviceID); ok {
				return Circle{}, errors.New("already a member")
			}
		}
	}
	return acceptInvite(home, invite.Code, invite.Fingerprint, joinEndpoint(invite.Endpoint), id, priv)
}

func storeInviteFile(home string, invite CircleInvite) error {
	if err := hookMkdirAll(invitesDir(home), 0o700); err != nil {
		return err
	}
	data, err := hookJSONMarshalIndent(invite, "", "  ")
	if err != nil {
		return err
	}
	return hookWriteFile(filepath.Join(invitesDir(home), invite.Code+".json"), data, 0o600)
}

func inviteCodeSafe(code string) bool {
	if code == "" || len(code) > 32 {
		return false
	}
	for _, r := range code {
		if r < '0' || (r > '9' && r < 'a') || r > 'z' {
			return false
		}
	}
	return true
}

func inviteCircleIDSafe(id string) bool {
	if id == "" || len(id) > 64 || strings.Contains(id, "..") {
		return false
	}
	for _, r := range id {
		if r != '_' && (r < '0' || (r > '9' && r < 'a') || r > 'z') {
			return false
		}
	}
	return true
}

func joinEndpoint(raw string) string {
	raw = strings.TrimSpace(raw)
	host, port, err := net.SplitHostPort(raw)
	if err != nil || host == "" || port == "" {
		return ""
	}
	return raw
}

func inviteErrorStatus(err error) int {
	switch err.Error() {
	case "invite not found":
		return http.StatusNotFound
	case "already a member":
		return http.StatusConflict
	default:
		return http.StatusBadRequest
	}
}

func writeInviteJSON(w http.ResponseWriter, status int, v any) {
	raw, err := json.Marshal(v)
	if err != nil {
		http.Error(w, "request failed", http.StatusInternalServerError)
		return
	}
	w.Header().Set(headerContentType, "application/json")
	w.Header().Set(headerCacheControl, cacheNoStore)
	w.WriteHeader(status)
	_, _ = w.Write(append(raw, '\n'))
}

func writeInviteError(w http.ResponseWriter, status int, message string) {
	writeInviteJSON(w, status, map[string]string{"error": message})
}
