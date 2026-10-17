package main

import "crypto/ed25519"

// Membership proofs. Another core adds a device to its roster only when a member it already trusts signed
// the device's admission, and marks one revoked only when the circle creator signed the revocation. The
// proofs live beside Circle.Members so the sync and catalog responses, which older cores verify over their
// exact JSON, keep their shape.

const (
	admissionPurpose  = "overdrive-circle-admission-v1"
	revocationPurpose = "overdrive-circle-revocation-v1"
)

type MemberAdmission struct {
	InvitedBy string `json:"invited_by"`
	AddedAt   string `json:"added_at"`
	Signature string `json:"signature"`
}

type MemberRevocation struct {
	RevokedAt string `json:"revoked_at"`
	Signature string `json:"signature"`
}

type admissionPayload struct {
	Purpose   string `json:"purpose"`
	CircleID  string `json:"circle_id"`
	DeviceID  string `json:"device_id"`
	PublicKey string `json:"public_key"`
	InvitedBy string `json:"invited_by"`
	AddedAt   string `json:"added_at"`
}

type revocationPayload struct {
	Purpose   string `json:"purpose"`
	CircleID  string `json:"circle_id"`
	DeviceID  string `json:"device_id"`
	RevokedAt string `json:"revoked_at"`
}

func signAdmission(priv ed25519.PrivateKey, circleID string, member CircleMember, inviter string) (MemberAdmission, error) {
	adm := MemberAdmission{InvitedBy: inviter, AddedAt: nowRFC3339()}
	payload, err := hookJSONMarshal(admissionPayload{
		Purpose: admissionPurpose, CircleID: circleID, DeviceID: member.DeviceID,
		PublicKey: member.PublicKey, InvitedBy: inviter, AddedAt: adm.AddedAt,
	})
	if err != nil {
		return MemberAdmission{}, err
	}
	adm.Signature = signPayload(priv, payload)
	return adm, nil
}

func verifyAdmission(circleID string, member CircleMember, adm MemberAdmission, inviterKey string) bool {
	pub, err := decodePublicKey(inviterKey)
	if err != nil {
		return false
	}
	payload, err := hookJSONMarshal(admissionPayload{
		Purpose: admissionPurpose, CircleID: circleID, DeviceID: member.DeviceID,
		PublicKey: member.PublicKey, InvitedBy: adm.InvitedBy, AddedAt: adm.AddedAt,
	})
	return err == nil && verifySignature(pub, payload, adm.Signature)
}

func signRevocation(priv ed25519.PrivateKey, circleID, deviceID string) (MemberRevocation, error) {
	rev := MemberRevocation{RevokedAt: nowRFC3339()}
	payload, err := hookJSONMarshal(revocationPayload{
		Purpose: revocationPurpose, CircleID: circleID, DeviceID: deviceID, RevokedAt: rev.RevokedAt,
	})
	if err != nil {
		return MemberRevocation{}, err
	}
	rev.Signature = signPayload(priv, payload)
	return rev, nil
}

func verifyRevocation(circleID, deviceID string, rev MemberRevocation, creatorKey string) bool {
	pub, err := decodePublicKey(creatorKey)
	if err != nil {
		return false
	}
	payload, err := hookJSONMarshal(revocationPayload{
		Purpose: revocationPurpose, CircleID: circleID, DeviceID: deviceID, RevokedAt: rev.RevokedAt,
	})
	return err == nil && verifySignature(pub, payload, rev.Signature)
}

func (c Circle) memberRecord(deviceID string) (CircleMember, bool) {
	for _, m := range c.Members {
		if m.DeviceID == deviceID {
			return m, true
		}
	}
	return CircleMember{}, false
}

// trustsAdmission accepts an admission signed by an active member of this roster, or the creator's own.
func (c Circle) trustsAdmission(member CircleMember, adm MemberAdmission) bool {
	if adm.InvitedBy == member.DeviceID {
		return member.DeviceID == c.CreatorDeviceID && verifyAdmission(c.ID, member, adm, member.PublicKey)
	}
	inviter, ok := c.activeMember(adm.InvitedBy)
	return ok && verifyAdmission(c.ID, member, adm, inviter.PublicKey)
}

// ingestRosterProofs admits the devices an incoming roster carries admissions for and applies the creator's
// revocations. Member records only supply keys and hints; on their own they change nothing.
func ingestRosterProofs(circle Circle, members []CircleMember, admissions map[string]MemberAdmission, revocations map[string]MemberRevocation) Circle {
	pending := map[string]CircleMember{}
	for _, m := range members {
		if m.DeviceID != "" && memberKeyBound(m.DeviceID, m.PublicKey) {
			pending[m.DeviceID] = m
		}
	}
	for progress := true; progress; {
		progress = false
		for deviceID, m := range pending {
			adm, ok := admissions[deviceID]
			if !ok {
				delete(pending, deviceID)
				continue
			}
			if !circle.trustsAdmission(m, adm) {
				continue
			}
			circle = circle.admit(m, adm)
			delete(pending, deviceID)
			progress = true
		}
	}
	for deviceID, rev := range revocations {
		circle = circle.applyRevocation(deviceID, rev)
	}
	return circle
}

func (c Circle) admit(m CircleMember, adm MemberAdmission) Circle {
	if c.Admissions == nil {
		c.Admissions = map[string]MemberAdmission{}
	}
	if _, has := c.Admissions[m.DeviceID]; !has {
		c.Admissions[m.DeviceID] = adm
	}
	if _, known := c.memberRecord(m.DeviceID); known {
		return c
	}
	_, revoked := c.Revocations[m.DeviceID]
	c.Members = append(c.Members, CircleMember{
		DeviceID: m.DeviceID, PublicKey: m.PublicKey, AddedAt: adm.AddedAt, Revoked: revoked,
		Endpoint: m.Endpoint, RemoteAddr: m.RemoteAddr, Hostname: m.Hostname, LastSeen: m.LastSeen,
	})
	return c
}

// applyRevocation keeps a revocation the creator signed, even for a device not on this roster yet, so it
// arrives revoked. The creator itself cannot be revoked: nobody could revoke anyone afterwards.
func (c Circle) applyRevocation(deviceID string, rev MemberRevocation) Circle {
	creator, ok := c.memberRecord(c.CreatorDeviceID)
	if !ok || deviceID == c.CreatorDeviceID || !verifyRevocation(c.ID, deviceID, rev, creator.PublicKey) {
		return c
	}
	if c.Revocations == nil {
		c.Revocations = map[string]MemberRevocation{}
	}
	if _, has := c.Revocations[deviceID]; !has {
		c.Revocations[deviceID] = rev
	}
	for i := range c.Members {
		if c.Members[i].DeviceID == deviceID {
			c.Members[i].Revoked = true
		}
	}
	return c
}

// vouchMembers signs what this core can vouch for and the roster still lacks: the creator's own admission,
// an admission for every active member nobody vouched for yet (rosters from before signed admissions, and
// devices this core just let in), and, on the creator, a revocation for every member already revoked.
func vouchMembers(circle Circle, self DeviceIdentity, priv ed25519.PrivateKey) Circle {
	if _, ok := circle.activeMember(self.DeviceID); !ok {
		return circle
	}
	isCreator := self.DeviceID == circle.CreatorDeviceID
	for _, m := range circle.Members {
		if m.Revoked {
			if isCreator {
				circle = circle.signRevocationFor(m.DeviceID, priv)
			}
			continue
		}
		if _, has := circle.Admissions[m.DeviceID]; has || !memberKeyBound(m.DeviceID, m.PublicKey) {
			continue
		}
		if m.DeviceID == self.DeviceID && !isCreator {
			continue
		}
		if adm, err := signAdmission(priv, circle.ID, m, self.DeviceID); err == nil {
			circle = circle.admit(m, adm)
		}
	}
	return circle
}

func (c Circle) signRevocationFor(deviceID string, priv ed25519.PrivateKey) Circle {
	if _, has := c.Revocations[deviceID]; has || deviceID == c.CreatorDeviceID {
		return c
	}
	rev, err := signRevocation(priv, c.ID, deviceID)
	if err != nil {
		return c
	}
	if c.Revocations == nil {
		c.Revocations = map[string]MemberRevocation{}
	}
	c.Revocations[deviceID] = rev
	return c
}
