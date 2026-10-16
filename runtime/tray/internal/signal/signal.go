// Package signal turns Core observations into notification kinds.
// Kinds that the Core does not currently emit stay representable and are not fired.
package signal

// Kind is a notification the tray can show. Text is generic on purpose:
// lock-screen and banner copy never include a message or handoff body.
type Kind string

const (
	Pairing        Kind = "pairing"
	Message        Kind = "message"
	Handoff        Kind = "handoff"
	AgentApproval  Kind = "agent_approval"
	MemoryConflict Kind = "memory_conflict"
	MeshFailure    Kind = "mesh_failure"
	IncomingCall   Kind = "incoming_call"
	SessionInvite  Kind = "session_invite"
	ControlRequest Kind = "control_request"
)

// AllKinds is the set the tray can represent. Diff only returns kinds it can
// actually see on the Core.
func AllKinds() []Kind {
	return []Kind{Pairing, Message, Handoff, AgentApproval, MemoryConflict, MeshFailure, IncomingCall, SessionInvite, ControlRequest}
}

// PublicText is the only string that may be shown in a notification.
func PublicText(kind Kind) (title, body string) {
	title = "Overdrive"
	switch kind {
	case Pairing:
		body = "A device is waiting to pair"
	case Message:
		body = "New message"
	case Handoff:
		body = "Handoff is ready"
	case AgentApproval:
		body = "An agent action needs approval"
	case MemoryConflict:
		body = "Memory conflict needs review"
	case MeshFailure:
		body = "Mesh needs attention"
	case IncomingCall:
		body = "Incoming call"
	case SessionInvite:
		body = "Live session invite"
	case ControlRequest:
		body = "Terminal control request"
	default:
		body = "Overdrive needs attention"
	}
	return title, body
}

// Link is a peer arc. Name is an identity for change detection, not banner text.
type Link struct {
	Name  string
	State string
}

// Snapshot is the previous Core reading. Handoff strings are compared, never displayed.
type Snapshot struct {
	Links         []Link
	Handoffs      []string
	RingingCallID      string
	SessionInviteID    string
	ControlRequestID   string
}

// Notice is a fired signal. It carries no body text.
type Notice struct {
	Kind Kind
}

// Diff emits mesh failures and new handoffs only after a baseline snapshot.
// The first reading seeds history and stays quiet. Pairing, messages, agent
// approval, and memory conflicts are not inferred from peer sync lines.
func Diff(prev Snapshot, next Snapshot, seeded bool) []Notice {
	if !seeded {
		return nil
	}
	var out []Notice
	prevFail := map[string]bool{}
	for _, link := range prev.Links {
		if failure(link.State) {
			prevFail[link.Name] = true
		}
	}
	for _, link := range next.Links {
		if failure(link.State) && !prevFail[link.Name] {
			out = append(out, Notice{Kind: MeshFailure})
			break
		}
	}
	prevHands := map[string]bool{}
	for _, handoff := range prev.Handoffs {
		prevHands[handoff] = true
	}
	for _, handoff := range next.Handoffs {
		if handoff != "" && !prevHands[handoff] {
			out = append(out, Notice{Kind: Handoff})
			break
		}
	}
	if next.RingingCallID != "" && next.RingingCallID != prev.RingingCallID {
		out = append(out, Notice{Kind: IncomingCall})
	}
	if next.SessionInviteID != "" && next.SessionInviteID != prev.SessionInviteID {
		out = append(out, Notice{Kind: SessionInvite})
	}
	if next.ControlRequestID != "" && next.ControlRequestID != prev.ControlRequestID {
		out = append(out, Notice{Kind: ControlRequest})
	}
	return out
}

func failure(state string) bool {
	switch state {
	case "timeout", "error", "failed":
		return true
	default:
		return false
	}
}
