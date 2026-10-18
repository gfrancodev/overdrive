package main

import (
	"crypto/ed25519"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

// Local dashboard API to manage a circle: the folders this device shares with it and, on the creator, removing
// a device. A removal always rotates the circle key, and the preview says who loses access before it happens.

const maxFolderSuggestions = 8

var (
	errFolderNotShared   = errors.New("that folder is not shared with this circle")
	errNotCircleCreator  = errors.New("only the circle creator can remove devices")
	errCreatorNotRemoved = errors.New("the circle creator cannot be removed")
	errDeviceNotInCircle = errors.New("that device is not in this circle")
)

type circleFolderView struct {
	Path   string `json:"path"`
	Name   string `json:"name"`
	Exists bool   `json:"exists"`
}

// revokeView is both the preview of a removal and its result: the devices that lose access, the devices the
// first one invited, and the members the key rotation leaves without the new key.
type revokeView struct {
	Targets   []string    `json:"targets"`
	Invitees  []string    `json:"invitees"`
	CutOff    []string    `json:"cut_off"`
	NextEpoch int         `json:"next_epoch"`
	Revoked   []string    `json:"revoked,omitempty"`
	Circle    *circleView `json:"circle,omitempty"`
}

func registerCircleManageRoutes(mux *http.ServeMux, home string) {
	mux.HandleFunc("/api/circles/{id}/folders", func(w http.ResponseWriter, r *http.Request) {
		if rejectRemoteCoreAdmin(w, r) {
			return
		}
		switch r.Method {
		case http.MethodGet:
			serveCircleFolders(w, home, r.PathValue("id"))
		case http.MethodPost:
			serveCircleFolderAdd(w, r, home, r.PathValue("id"))
		case http.MethodDelete:
			serveCircleFolderRemove(w, r, home, r.PathValue("id"))
		default:
			http.Error(w, methodNotAllowedMsg, http.StatusMethodNotAllowed)
		}
	})
	mux.HandleFunc("/api/circles/{id}/members/{device}/revoke", func(w http.ResponseWriter, r *http.Request) {
		if rejectRemoteCoreAdmin(w, r) {
			return
		}
		switch r.Method {
		case http.MethodGet:
			serveRevokePreview(w, r, home, r.PathValue("id"), r.PathValue("device"))
		case http.MethodPost:
			serveRevoke(w, r, home, r.PathValue("id"), r.PathValue("device"))
		default:
			http.Error(w, methodNotAllowedMsg, http.StatusMethodNotAllowed)
		}
	})
}

// selfCircle loads a circle this device is an active member of, with this device's identity.
func selfCircle(home, circleID string) (Circle, DeviceIdentity, ed25519.PrivateKey, error) {
	if !inviteCircleIDSafe(circleID) {
		return Circle{}, DeviceIdentity{}, nil, errCircleNotFound
	}
	id, priv, err := loadOrCreateIdentity(home)
	if err != nil {
		return Circle{}, DeviceIdentity{}, nil, err
	}
	circle, err := loadCircle(home, circleID)
	if err != nil {
		return Circle{}, DeviceIdentity{}, nil, errCircleNotFound
	}
	if _, ok := circle.activeMember(id.DeviceID); !ok {
		return Circle{}, DeviceIdentity{}, nil, errCircleNotFound
	}
	return circle, id, priv, nil
}

func writeCircleManageError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, errCircleNotFound):
		writeInviteError(w, http.StatusNotFound, "circle not found")
	case errors.Is(err, errFolderNotShared), errors.Is(err, errDeviceNotInCircle):
		writeInviteError(w, http.StatusNotFound, err.Error())
	case errors.Is(err, errNotCircleCreator):
		writeInviteError(w, http.StatusForbidden, err.Error())
	case errors.Is(err, errCreatorNotRemoved):
		writeInviteError(w, http.StatusBadRequest, err.Error())
	default:
		writeInviteError(w, http.StatusInternalServerError, "could not update the circle")
	}
}

func folderViews(paths []string) []circleFolderView {
	out := []circleFolderView{}
	for _, path := range paths {
		st, err := os.Stat(path)
		out = append(out, circleFolderView{Path: path, Name: filepath.Base(path), Exists: err == nil && st.IsDir()})
	}
	return out
}

func writeCircleFolders(w http.ResponseWriter, status int, home string, circle Circle, selfID string) {
	writeInviteJSON(w, status, map[string]any{
		"folders":     folderViews(circle.AllowedFolders),
		"suggestions": folderViews(folderSuggestions(home, circle)),
		"circle":      viewCircle(circle, selfID),
	})
}

func serveCircleFolders(w http.ResponseWriter, home, circleID string) {
	circle, id, _, err := selfCircle(home, circleID)
	if err != nil {
		writeCircleManageError(w, err)
		return
	}
	writeCircleFolders(w, http.StatusOK, home, circle, id.DeviceID)
}

func serveCircleFolderAdd(w http.ResponseWriter, r *http.Request, home, circleID string) {
	r.Body = http.MaxBytesReader(w, r.Body, 8*1024)
	var body struct {
		Path string `json:"path"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeInviteError(w, http.StatusBadRequest, badRequestMsg)
		return
	}
	if _, _, _, err := selfCircle(home, circleID); err != nil {
		writeCircleManageError(w, err)
		return
	}
	path, err := resolveProjectFolder(body.Path)
	if err != nil {
		writeInviteError(w, http.StatusBadRequest, err.Error())
		return
	}
	id, _, err := loadOrCreateIdentity(home)
	if err != nil {
		writeCircleManageError(w, err)
		return
	}
	circle, err := addAllowedFolder(home, circleID, path, id)
	if err != nil {
		writeCircleManageError(w, err)
		return
	}
	writeCircleFolders(w, http.StatusCreated, home, circle, id.DeviceID)
}

func serveCircleFolderRemove(w http.ResponseWriter, r *http.Request, home, circleID string) {
	path := strings.TrimSpace(r.URL.Query().Get("path"))
	if path == "" {
		writeInviteError(w, http.StatusBadRequest, "choose the folder to stop sharing")
		return
	}
	_, id, _, err := selfCircle(home, circleID)
	if err != nil {
		writeCircleManageError(w, err)
		return
	}
	circle, err := removeAllowedFolder(home, circleID, path, id)
	if err != nil {
		writeCircleManageError(w, err)
		return
	}
	writeCircleFolders(w, http.StatusOK, home, circle, id.DeviceID)
}

// resolveProjectFolder turns what someone typed into the folder to share: an existing directory given by its
// full path or from ~, and never the whole disk or home folder, which would share every project at once.
func resolveProjectFolder(raw string) (string, error) {
	path := strings.TrimSpace(raw)
	if path == "" {
		return "", errors.New("choose a folder to share")
	}
	userHome, _ := os.UserHomeDir()
	if path == "~" || strings.HasPrefix(path, "~/") || strings.HasPrefix(path, `~\`) {
		if userHome == "" {
			return "", errors.New("use the folder's full path")
		}
		path = filepath.Join(userHome, path[1:])
	}
	if !filepath.IsAbs(path) {
		return "", errors.New("use the folder's full path, such as /home/me/project")
	}
	resolved, err := filepath.EvalSymlinks(filepath.Clean(path))
	if err != nil {
		return "", errors.New("that folder does not exist on this device")
	}
	if st, err := os.Stat(resolved); err != nil || !st.IsDir() {
		return "", errors.New("that path is not a folder")
	}
	if resolved == filepath.Dir(resolved) {
		return "", errors.New("share a project folder, not the whole disk")
	}
	if userHome != "" {
		if h, err := filepath.EvalSymlinks(userHome); err == nil && h == resolved {
			return "", errors.New("share a project folder, not your whole home folder")
		}
	}
	return resolved, nil
}

// folderSuggestions lists projects this device recorded experience in, newest first, that still exist and
// are not shared with the circle yet.
func folderSuggestions(home string, circle Circle) []string {
	e, err := newEngine(home)
	if err != nil {
		return nil
	}
	defer e.Close()
	rows, err := e.db.Query(`SELECT source_folder FROM memories WHERE origin = 'local' AND source_folder != ''
		GROUP BY source_folder ORDER BY max(updated_at) DESC LIMIT 64`)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var out []string
	for rows.Next() && len(out) < maxFolderSuggestions {
		var folder string
		if rows.Scan(&folder) != nil {
			continue
		}
		resolved, err := resolveProjectFolder(folder)
		if err != nil || folderAllowed(circle, resolved) || containsString(out, resolved) {
			continue
		}
		out = append(out, resolved)
	}
	return out
}

// planRevoke checks that this device may remove target and says what removing it does.
func planRevoke(circle Circle, selfID, target string, withInvitees bool) (revokeView, error) {
	if circle.CreatorDeviceID != selfID {
		return revokeView{}, errNotCircleCreator
	}
	if target == circle.CreatorDeviceID {
		return revokeView{}, errCreatorNotRemoved
	}
	if _, ok := circle.activeMember(target); !ok {
		return revokeView{}, errDeviceNotInCircle
	}
	plan := revokeView{Targets: []string{target}, Invitees: []string{}, CutOff: []string{}, NextEpoch: circle.KeyEpoch + 1}
	for _, deviceID := range circleInvitees(circle, target) {
		if _, ok := circle.activeMember(deviceID); ok {
			plan.Invitees = append(plan.Invitees, deviceID)
		}
	}
	if withInvitees {
		plan.Targets = append(plan.Targets, plan.Invitees...)
	}
	for _, m := range circle.Members {
		if m.Revoked || m.DeviceID == selfID || containsString(plan.Targets, m.DeviceID) {
			continue
		}
		if _, ok := circle.KexKeys[m.DeviceID]; !ok {
			plan.CutOff = append(plan.CutOff, m.DeviceID)
		}
	}
	return plan, nil
}

func revokeTargetSafe(deviceID string) bool {
	return inviteCircleIDSafe(deviceID) && strings.HasPrefix(deviceID, "dev_")
}

func serveRevokePreview(w http.ResponseWriter, r *http.Request, home, circleID, deviceID string) {
	circle, id, _, err := selfCircle(home, circleID)
	if err == nil && !revokeTargetSafe(deviceID) {
		err = errDeviceNotInCircle
	}
	if err != nil {
		writeCircleManageError(w, err)
		return
	}
	plan, err := planRevoke(circle, id.DeviceID, deviceID, r.URL.Query().Get("with_invitees") == "1")
	if err != nil {
		writeCircleManageError(w, err)
		return
	}
	writeInviteJSON(w, http.StatusOK, plan)
}

func serveRevoke(w http.ResponseWriter, r *http.Request, home, circleID, deviceID string) {
	r.Body = http.MaxBytesReader(w, r.Body, 4*1024)
	var body struct {
		WithInvitees bool `json:"with_invitees"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeInviteError(w, http.StatusBadRequest, badRequestMsg)
		return
	}
	circle, id, priv, err := selfCircle(home, circleID)
	if err == nil && !revokeTargetSafe(deviceID) {
		err = errDeviceNotInCircle
	}
	if err != nil {
		writeCircleManageError(w, err)
		return
	}
	plan, err := planRevoke(circle, id.DeviceID, deviceID, body.WithInvitees)
	if err != nil {
		writeCircleManageError(w, err)
		return
	}
	rotated, cutOff, err := revokeAndRotate(home, circleID, plan.Targets, id, priv)
	if err != nil {
		writeCircleManageError(w, err)
		return
	}
	forgetRevokedDevices(home, circleID, plan.Targets)
	view := viewCircle(rotated, id.DeviceID)
	if cutOff == nil {
		cutOff = []string{}
	}
	writeInviteJSON(w, http.StatusOK, revokeView{
		Targets: plan.Targets, Invitees: plan.Invitees, CutOff: cutOff, NextEpoch: rotated.KeyEpoch,
		Revoked: plan.Targets, Circle: &view,
	})
}

// forgetRevokedDevices retires what the revoked devices shared with this one and closes their terminal access.
func forgetRevokedDevices(home, circleID string, targets []string) {
	e, err := newEngine(home)
	if err == nil {
		defer e.Close()
	}
	for _, target := range targets {
		if e != nil {
			_ = e.markPeerRevoked(circleID, target)
		}
		_ = revokeTerminalAccessForDevice(home, circleID, target)
	}
	if circle, err := loadCircle(home, circleID); err == nil {
		dropRemovedChatClients(circle)
	}
}
