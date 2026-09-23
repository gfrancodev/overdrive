package main

import (
	"encoding/json"
	"path/filepath"
	"time"
)

func parseJoinRequest(plain []byte) (JoinRequest, error) {
	var req JoinRequest
	if err := json.Unmarshal(plain, &req); err != nil {
		return JoinRequest{}, err
	}
	return req, nil
}

func loadJoinInvite(home, code string) (CircleInvite, error) {
	path := filepath.Join(invitesDir(home), code+".json")
	data, err := hookReadFile(path)
	if err != nil {
		return CircleInvite{}, err
	}
	var invite CircleInvite
	if err := json.Unmarshal(data, &invite); err != nil {
		return CircleInvite{}, err
	}
	return invite, nil
}

func joinInviteValid(invite CircleInvite, now time.Time) bool {
	exp, err := time.Parse(time.RFC3339, invite.ExpiresAt)
	if err != nil {
		return false
	}
	return !now.UTC().After(exp)
}

func circleWithJoinMember(circle Circle, req JoinRequest) Circle {
	if _, ok := circle.activeMember(req.DeviceID); ok {
		return circle
	}
	circle.Members = append(circle.Members, CircleMember{
		DeviceID: req.DeviceID, PublicKey: req.PublicKey, AddedAt: nowRFC3339(), Revoked: false,
	})
	return circle
}

func persistJoinedCircle(home string, circle Circle) error {
	_, priv, err := loadOrCreateIdentity(home)
	if err != nil {
		return err
	}
	if err := circle.signMemberList(priv); err != nil {
		return err
	}
	return saveCircle(home, circle)
}
