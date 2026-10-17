package main

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// TerminalControl enforces turn-based control leases with monotonic epoch CAS.
type TerminalControl struct {
	store       *TerminalStore
	actorDevice string
}

func newTerminalControl(store *TerminalStore, actorDeviceID string) *TerminalControl {
	return &TerminalControl{store: store, actorDevice: strings.TrimSpace(actorDeviceID)}
}

func (c *TerminalControl) CurrentLease(sessionID string) (ControlLease, int64, error) {
	return c.store.GetActiveLease(sessionID)
}

func (c *TerminalControl) RequestControl(sessionID, requesterDeviceID string) error {
	sess, err := c.store.GetSession(sessionID)
	if err != nil {
		return err
	}
	if sess.PrivateMode {
		return fmt.Errorf("%s", TerminalErrPrivateMode)
	}
	_ = c.store.audit(sessionID, requesterDeviceID, "control_request", "")
	if sess.CircleID != "" {
		payload, _ := json.Marshal(map[string]string{
			"session_id": sessionID, "requester_device_id": requesterDeviceID,
		})
		_ = publishTerminalMQTT(c.store.home, sess.CircleID, terminalControlTopic(sess.CircleID, sessionID), "control-request", payload)
	}
	return nil
}

func (c *TerminalControl) GrantControl(sessionID, actorDeviceID, holderDeviceID string, expectedEpoch int64, expiresAt string) (ControlLease, error) {
	sess, err := c.store.GetSession(sessionID)
	if err != nil {
		return ControlLease{}, err
	}
	if actorDeviceID != sess.OwnerDeviceID && actorDeviceID != sess.HostDeviceID {
		return ControlLease{}, fmt.Errorf("%s", TerminalErrAccessDenied)
	}
	if sess.PrivateMode {
		return ControlLease{}, fmt.Errorf("%s", TerminalErrPrivateMode)
	}
	lease, err := c.store.grantLeaseCAS(sessionID, holderDeviceID, expectedEpoch, expiresAt)
	if err != nil {
		return ControlLease{}, err
	}
	payload, _ := json.Marshal(map[string]any{
		"session_id": sessionID, "lease_id": lease.ID, "holder_device_id": holderDeviceID, "epoch": lease.Epoch,
	})
	_ = c.store.recordControlEvent(sessionID, lease.Epoch, actorDeviceID, "grant", payload)
	if sess.CircleID != "" {
		_ = publishTerminalMQTT(c.store.home, sess.CircleID, terminalControlTopic(sess.CircleID, sessionID), "control-grant", payload)
	}
	return lease, nil
}

func (c *TerminalControl) RejectControl(sessionID, actorDeviceID, requesterDeviceID, reason string) error {
	sess, err := c.store.GetSession(sessionID)
	if err != nil {
		return err
	}
	if actorDeviceID != sess.OwnerDeviceID && actorDeviceID != sess.HostDeviceID {
		return fmt.Errorf("%s", TerminalErrAccessDenied)
	}
	_ = c.store.audit(sessionID, actorDeviceID, "control_reject", requesterDeviceID+": "+reason)
	return nil
}

func (c *TerminalControl) ReleaseControl(sessionID, holderDeviceID, leaseID string, epoch int64) (ControlLease, error) {
	lease, err := c.store.releaseLeaseCAS(sessionID, holderDeviceID, leaseID, epoch)
	if err != nil {
		return ControlLease{}, err
	}
	payload, _ := json.Marshal(map[string]any{"session_id": sessionID, "epoch": lease.Epoch, "released": true})
	_ = c.store.recordControlEvent(sessionID, lease.Epoch, holderDeviceID, "release", payload)
	sess, _ := c.store.GetSession(sessionID)
	if sess.CircleID != "" {
		_ = publishTerminalMQTT(c.store.home, sess.CircleID, terminalControlTopic(sess.CircleID, sessionID), "control-release", payload)
	}
	return lease, nil
}

func (c *TerminalControl) RevokeControl(sessionID, actorDeviceID, holderDeviceID, reason string) (ControlLease, error) {
	sess, err := c.store.GetSession(sessionID)
	if err != nil {
		return ControlLease{}, err
	}
	if actorDeviceID != sess.OwnerDeviceID && actorDeviceID != sess.HostDeviceID {
		return ControlLease{}, fmt.Errorf("%s", TerminalErrAccessDenied)
	}
	cur, epoch, err := c.store.GetActiveLease(sessionID)
	if err != nil {
		return ControlLease{}, err
	}
	if holderDeviceID != "" && cur.HolderDeviceID != "" && cur.HolderDeviceID != holderDeviceID {
		return ControlLease{}, fmt.Errorf("%s", TerminalErrInvalidLease)
	}
	lease, err := c.store.grantLeaseCAS(sessionID, sess.OwnerDeviceID, epoch, time.Now().Add(24*time.Hour).UTC().Format(time.RFC3339))
	if err != nil {
		return ControlLease{}, err
	}
	_ = c.store.audit(sessionID, actorDeviceID, "control_revoke", reason)
	payload, _ := json.Marshal(map[string]any{"session_id": sessionID, "epoch": lease.Epoch, "revoked": holderDeviceID})
	if sess.CircleID != "" {
		_ = publishTerminalMQTT(c.store.home, sess.CircleID, terminalControlTopic(sess.CircleID, sessionID), "control-revoke", payload)
	}
	return lease, nil
}

func (c *TerminalControl) ValidateWrite(sessionID, deviceID, leaseID string, epoch int64) error {
	sess, err := c.store.GetSession(sessionID)
	if err != nil {
		return err
	}
	if sess.PrivateMode {
		return fmt.Errorf("%s", TerminalErrPrivateMode)
	}
	lease, curEpoch, err := c.store.GetActiveLease(sessionID)
	if err != nil {
		return err
	}
	if lease.Revoked || lease.ID == "" {
		return fmt.Errorf("%s", TerminalErrInvalidLease)
	}
	if leaseID != "" && lease.ID != leaseID {
		return fmt.Errorf("%s", TerminalErrInvalidLease)
	}
	if epoch != curEpoch {
		return fmt.Errorf("%s", TerminalErrInvalidLease)
	}
	if lease.HolderDeviceID != deviceID && deviceID != sess.OwnerDeviceID {
		return fmt.Errorf("%s", TerminalErrAccessDenied)
	}
	if lease.ExpiresAt != "" {
		if t, err := time.Parse(time.RFC3339, lease.ExpiresAt); err == nil && time.Now().After(t) {
			return fmt.Errorf("%s", TerminalErrInvalidLease)
		}
	}
	return nil
}

// revokeTerminalAccessForDevice removes a revoked circle member from every session of that circle it joined or
// was shared; an empty circleID covers every circle.
func revokeTerminalAccessForDevice(home, circleID, deviceID string) error {
	store, err := newTerminalStore(home)
	if err != nil {
		return err
	}
	joined, err := store.ListSessionsByParticipantDevice(deviceID)
	if err != nil {
		return err
	}
	shared, err := store.ListSessionsSharedWithDevice(deviceID)
	if err != nil {
		return err
	}
	seen := map[string]bool{}
	for _, sid := range append(joined, shared...) {
		if seen[sid] {
			continue
		}
		seen[sid] = true
		if sess, err := store.GetSession(sid); err != nil || (circleID != "" && sess.CircleID != circleID) {
			continue
		}
		revokeTerminalSessionAccess(store, sid, deviceID, "circle_revoke")
	}
	return nil
}

func closeTerminalStreamsForDevice(sessionID, deviceID string) {
	unregisterTerminalStream(sessionID, deviceID)
}
