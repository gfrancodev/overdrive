package main

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"
)

// Local dashboard API for onboarding and circle management. Every route is loopback-only and none of them
// returns key material: circle keys, previous keys and invite secrets stay in the circle and invite files.

const maxCircleNameRunes = 64

var errCircleNotFound = errors.New("circle not found")

type setupStep struct {
	ID   string `json:"id"`
	Done bool   `json:"done"`
}

// setupStatus is derived from the Core's real state on every request; there is no stored "onboarding done".
type setupStatus struct {
	DeviceID        string      `json:"device_id"`
	Hostname        string      `json:"hostname"`
	DiscoveryActive bool        `json:"discovery_active"`
	Steps           []setupStep `json:"steps"`
	Next            string      `json:"next"`
}

type circleMemberView struct {
	DeviceID  string `json:"device_id"`
	Hostname  string `json:"hostname,omitempty"`
	InvitedBy string `json:"invited_by,omitempty"`
	Revoked   bool   `json:"revoked"`
	Self      bool   `json:"self"`
	LastSeen  string `json:"last_seen,omitempty"`
	// ReceivesKeys is whether a key rotation reaches this member: this core holds its key-agreement key.
	ReceivesKeys bool `json:"receives_keys"`
}

type circleView struct {
	ID              string `json:"id"`
	Name            string `json:"name"`
	CreatorDeviceID string `json:"creator_device_id"`
	IsCreator       bool   `json:"is_creator"`
	KeyEpoch        int    `json:"key_epoch"`
	KeyRotatedAt    string `json:"key_rotated_at,omitempty"`
	// PreviousKeyUntil is when deliveries sealed with the key before the last rotation stop being accepted.
	PreviousKeyUntil string             `json:"previous_key_until,omitempty"`
	Folders          []string           `json:"folders"`
	Members          []circleMemberView `json:"members"`
}

func registerCircleAPIRoutes(mux *http.ServeMux, home string) {
	mux.HandleFunc("/api/setup/status", func(w http.ResponseWriter, r *http.Request) {
		if rejectRemoteCoreAdmin(w, r) {
			return
		}
		if r.Method != http.MethodGet {
			http.Error(w, methodNotAllowedMsg, http.StatusMethodNotAllowed)
			return
		}
		st, err := deriveSetupStatus(home)
		if err != nil {
			writeInviteError(w, http.StatusInternalServerError, "could not read setup state")
			return
		}
		writeInviteJSON(w, http.StatusOK, st)
	})
	mux.HandleFunc("/api/circles", func(w http.ResponseWriter, r *http.Request) {
		if rejectRemoteCoreAdmin(w, r) {
			return
		}
		switch r.Method {
		case http.MethodGet:
			serveCircleList(w, home)
		case http.MethodPost:
			serveCircleCreate(w, r, home)
		default:
			http.Error(w, methodNotAllowedMsg, http.StatusMethodNotAllowed)
		}
	})
	mux.HandleFunc("/api/circles/{id}", func(w http.ResponseWriter, r *http.Request) {
		if rejectRemoteCoreAdmin(w, r) {
			return
		}
		if r.Method != http.MethodPatch {
			http.Error(w, methodNotAllowedMsg, http.StatusMethodNotAllowed)
			return
		}
		serveCircleRename(w, r, home, r.PathValue("id"))
	})
	registerCircleManageRoutes(mux, home)
}

func deriveSetupStatus(home string) (setupStatus, error) {
	id, _, err := loadOrCreateIdentity(home)
	if err != nil {
		return setupStatus{}, err
	}
	circles, err := memberCircles(home, id.DeviceID)
	if err != nil {
		return setupStatus{}, err
	}
	hasPeer, hasFolder := false, false
	for _, c := range circles {
		for _, m := range c.Members {
			if !m.Revoked && m.DeviceID != id.DeviceID {
				hasPeer = true
			}
		}
		if len(c.AllowedFolders) > 0 {
			hasFolder = true
		}
	}
	st := setupStatus{
		DeviceID: id.DeviceID, Hostname: computerName(), DiscoveryActive: meshAllowsDiscovery(),
		Steps: []setupStep{
			{ID: "circle", Done: len(circles) > 0},
			{ID: "peer", Done: hasPeer},
			{ID: "folder", Done: hasFolder},
		},
		Next: "ready",
	}
	for _, step := range st.Steps {
		if !step.Done {
			st.Next = step.ID
			break
		}
	}
	return st, nil
}

// memberCircles lists the circles this device is an active member of.
func memberCircles(home, deviceID string) ([]Circle, error) {
	all, err := listCircles(home)
	if err != nil {
		return nil, err
	}
	var out []Circle
	for _, c := range all {
		if _, ok := c.activeMember(deviceID); ok {
			out = append(out, c)
		}
	}
	return out, nil
}

func viewCircle(c Circle, selfID string) circleView {
	v := circleView{
		ID: c.ID, Name: c.Name, CreatorDeviceID: c.CreatorDeviceID, IsCreator: c.CreatorDeviceID == selfID,
		KeyEpoch: c.KeyEpoch, Folders: append([]string{}, c.AllowedFolders...), Members: []circleMemberView{},
	}
	if c.KeyUpdate != nil && c.KeyUpdate.Epoch == c.KeyEpoch {
		v.KeyRotatedAt = c.KeyUpdate.CreatedAt
	}
	if len(c.openKeys(time.Now())) > 1 {
		v.PreviousKeyUntil = c.PreviousKey.ExpiresAt
	}
	for _, m := range c.Members {
		_, hasKex := c.KexKeys[m.DeviceID]
		v.Members = append(v.Members, circleMemberView{
			DeviceID: m.DeviceID, Hostname: m.Hostname, InvitedBy: c.Admissions[m.DeviceID].InvitedBy,
			Revoked: m.Revoked, Self: m.DeviceID == selfID, LastSeen: m.LastSeen,
			ReceivesKeys: hasKex || m.DeviceID == selfID,
		})
	}
	return v
}

func serveCircleList(w http.ResponseWriter, home string) {
	id, _, err := loadOrCreateIdentity(home)
	if err != nil {
		writeInviteError(w, http.StatusInternalServerError, "could not read identity")
		return
	}
	circles, err := memberCircles(home, id.DeviceID)
	if err != nil {
		writeInviteError(w, http.StatusInternalServerError, "could not read circles")
		return
	}
	views := []circleView{}
	for _, c := range circles {
		views = append(views, viewCircle(c, id.DeviceID))
	}
	writeInviteJSON(w, http.StatusOK, map[string]any{"device_id": id.DeviceID, "circles": views})
}

func readCircleName(w http.ResponseWriter, r *http.Request) (string, error) {
	r.Body = http.MaxBytesReader(w, r.Body, 4*1024)
	var body struct {
		Name string `json:"name"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		return "", errors.New("bad request")
	}
	name := strings.TrimSpace(body.Name)
	if name == "" || utf8.RuneCountInString(name) > maxCircleNameRunes {
		return "", errors.New("a circle name needs 1 to 64 characters")
	}
	return name, nil
}

func serveCircleCreate(w http.ResponseWriter, r *http.Request, home string) {
	name, err := readCircleName(w, r)
	if err != nil {
		writeInviteError(w, http.StatusBadRequest, err.Error())
		return
	}
	id, priv, err := loadOrCreateIdentity(home)
	if err != nil {
		writeInviteError(w, http.StatusInternalServerError, "could not read identity")
		return
	}
	circle, err := createCircle(home, name, id, priv)
	if err != nil {
		writeInviteError(w, http.StatusInternalServerError, "could not create the circle")
		return
	}
	writeInviteJSON(w, http.StatusCreated, map[string]any{"circle": viewCircle(circle, id.DeviceID)})
}

func serveCircleRename(w http.ResponseWriter, r *http.Request, home, circleID string) {
	if !inviteCircleIDSafe(circleID) {
		writeInviteError(w, http.StatusNotFound, "circle not found")
		return
	}
	name, err := readCircleName(w, r)
	if err != nil {
		writeInviteError(w, http.StatusBadRequest, err.Error())
		return
	}
	id, _, err := loadOrCreateIdentity(home)
	if err != nil {
		writeInviteError(w, http.StatusInternalServerError, "could not read identity")
		return
	}
	circle, err := updateCircle(home, circleID, func(c Circle) (Circle, error) {
		if _, ok := c.activeMember(id.DeviceID); !ok {
			return Circle{}, errCircleNotFound
		}
		c.Name = name
		return c, nil
	})
	if err != nil {
		writeInviteError(w, http.StatusNotFound, "circle not found")
		return
	}
	writeInviteJSON(w, http.StatusOK, map[string]any{"circle": viewCircle(circle, id.DeviceID)})
}
