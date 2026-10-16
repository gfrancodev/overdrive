package main

import (
	"crypto/ed25519"
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
			serveCircleInviteIssue(w, home)
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

func serveCircleInviteIssue(w http.ResponseWriter, home string) {
	invite, err := issueCircleInvite(home)
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

func issueCircleInvite(home string) (CircleInvite, error) {
	id, priv, err := loadOrCreateIdentity(home)
	if err != nil {
		return CircleInvite{}, err
	}
	circle, err := circleForInvite(home, id, priv)
	if err != nil {
		return CircleInvite{}, err
	}
	return createInvite(home, circle.ID, id)
}

func circleForInvite(home string, id DeviceIdentity, priv ed25519.PrivateKey) (Circle, error) {
	circles, err := listCircles(home)
	if err != nil {
		return Circle{}, err
	}
	for _, circle := range circles {
		if _, ok := circle.activeMember(id.DeviceID); ok {
			return circle, nil
		}
	}
	name := strings.TrimSpace(computerName())
	if name == "" {
		name = "Circle"
	}
	return createCircle(home, name, id, priv)
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
