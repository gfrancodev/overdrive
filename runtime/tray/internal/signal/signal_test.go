package signal

import (
	"strings"
	"testing"
)

func TestKindsHaveGenericText(t *testing.T) {
	secret := "secret handoff summary and message body"
	for _, kind := range AllKinds() {
		title, body := PublicText(kind)
		if title != "Overdrive" || body == "" {
			t.Fatalf("%s %q %q", kind, title, body)
		}
		if strings.Contains(title, secret) || strings.Contains(body, secret) || strings.Contains(body, "summary") {
			t.Fatalf("text included detail: %s %q", kind, body)
		}
	}
}

func TestFirstSnapshotDoesNotNotify(t *testing.T) {
	next := Snapshot{
		Links:    []Link{{Name: "10.0.0.8:7741", State: "timeout"}},
		Handoffs: []string{"aberto: secret handoff summary"},
	}
	if notes := Diff(Snapshot{}, next, false); len(notes) != 0 {
		t.Fatalf("seed notified %#v", notes)
	}
}

func TestTransitionsFireWithoutBodies(t *testing.T) {
	prev := Snapshot{Links: []Link{{Name: "peer", State: "ok"}}}
	next := Snapshot{
		Links:    []Link{{Name: "peer", State: "timeout"}},
		Handoffs: []string{"aberto: secret handoff summary"},
	}
	notes := Diff(prev, next, true)
	if len(notes) != 2 || notes[0].Kind != MeshFailure || notes[1].Kind != Handoff {
		t.Fatalf("%#v", notes)
	}
	for _, note := range notes {
		_, body := PublicText(note.Kind)
		if strings.Contains(body, "secret") || strings.Contains(body, "10.0.0.8") || strings.Contains(body, "peer") {
			t.Fatalf("banner %q", body)
		}
	}
	again := Diff(next, next, true)
	if len(again) != 0 {
		t.Fatalf("repeat %#v", again)
	}
}

func TestTerminalTrayTransitions(t *testing.T) {
	prev := Snapshot{}
	next := Snapshot{SessionInviteID: "ts_1:share", ControlRequestID: "ts_1:42"}
	notes := Diff(prev, next, true)
	kinds := map[Kind]bool{}
	for _, n := range notes {
		kinds[n.Kind] = true
	}
	if !kinds[SessionInvite] || !kinds[ControlRequest] {
		t.Fatalf("%#v", notes)
	}
}

func TestIncomingCallTransition(t *testing.T) {
	prev := Snapshot{}
	next := Snapshot{RingingCallID: "call_abc"}
	notes := Diff(prev, next, true)
	if len(notes) != 1 || notes[0].Kind != IncomingCall {
		t.Fatalf("%#v", notes)
	}
	again := Diff(next, next, true)
	if len(again) != 0 {
		t.Fatalf("repeat %#v", again)
	}
}

func TestSyncLinesAreNotPairingOrMessages(t *testing.T) {
	prev := Snapshot{}
	next := Snapshot{Links: []Link{{Name: "peer", State: "ok"}}}
	for _, note := range Diff(prev, next, true) {
		switch note.Kind {
		case Pairing, Message, AgentApproval, MemoryConflict:
			t.Fatalf("invented %s", note.Kind)
		}
	}
}
