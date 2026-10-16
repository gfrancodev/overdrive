// Package menu builds the tray menu from Core observations.
package menu

import "fmt"

type Status string

const (
	Running      Status = "running"
	Disconnected Status = "disconnected"
	Syncing      Status = "syncing"
	Error        Status = "error"
)

func (s Status) Text() string {
	switch s {
	case Running, Disconnected, Syncing, Error:
		return "Status: " + string(s)
	default:
		return "Status: disconnected"
	}
}

const (
	ActionOpen        = "open"
	ActionMessages    = "messages"
	ActionPause       = "pause"
	ActionResume      = "resume"
	ActionPreferences = "preferences"
	ActionStop        = "stop"
	ActionStart       = "start"
)

// Link is a peer arc state. "sync" on the admin status is not a sync-in-progress flag.
type Link struct {
	State string
}

type Item struct {
	Title   string
	Tooltip string
	Enabled bool
	Action  string
}

type View struct {
	Status     Status
	StatusText string
	MeshText   string
	Open       Item
	Messages   Item
	MeshToggle Item
	Prefs      Item
	Power      Item
}

type Input struct {
	Connected   bool
	HTTPError   bool
	Mesh        string
	Links       []Link
	PeersKnown  bool
	PeerCount   int
	UnreadKnown bool
	Unread      int
	CanOpen     bool
}

func Build(in Input) View {
	status := DeriveStatus(in.Connected, in.HTTPError, in.Links)
	view := View{
		Status:     status,
		StatusText: status.Text(),
		MeshText:   meshText(in.Connected && !in.HTTPError && in.PeersKnown, in.PeerCount),
	}
	view.Open = item("Open Overdrive", in.CanOpen && in.Connected && !in.HTTPError, ActionOpen)
	if in.UnreadKnown && in.Connected && !in.HTTPError {
		view.Messages = item(fmt.Sprintf("Messages: %d", in.Unread), true, ActionMessages)
	} else {
		view.Messages = item("Messages", false, "")
	}
	switch {
	case !in.Connected || in.HTTPError:
		view.MeshToggle = item("Pause Mesh", false, "")
		view.Power = item("Stop Overdrive", false, "")
		if !in.Connected {
			view.Power = item("Start Overdrive", true, ActionStart)
		} else {
			view.Power = item("Stop Overdrive", true, ActionStop)
		}
	case in.Mesh == "paused":
		view.MeshToggle = item("Resume Mesh", true, ActionResume)
		view.Power = item("Stop Overdrive", true, ActionStop)
	default:
		view.MeshToggle = item("Pause Mesh", true, ActionPause)
		view.Power = item("Stop Overdrive", true, ActionStop)
	}
	view.Prefs = item("Preferences", in.CanOpen && in.Connected && !in.HTTPError, ActionPreferences)
	return view
}

func item(title string, enabled bool, action string) Item {
	if !enabled {
		action = ""
	}
	return Item{Title: title, Tooltip: title, Enabled: enabled, Action: action}
}

func meshText(known bool, count int) string {
	if !known {
		return "Mesh: unknown"
	}
	if count == 1 {
		return "Mesh: 1 peer"
	}
	return fmt.Sprintf("Mesh: %d peers", count)
}

// DeriveStatus maps Core observations onto the four menu states.
// A link state of "syncing" is the only in-progress signal. Admin "sync: true"
// only means the mesh transports are open.
func DeriveStatus(connected, httpError bool, links []Link) Status {
	if !connected {
		return Disconnected
	}
	if httpError {
		return Error
	}
	syncing := false
	for _, link := range links {
		if FailureState(link.State) {
			return Error
		}
		if link.State == "syncing" {
			syncing = true
		}
	}
	if syncing {
		return Syncing
	}
	return Running
}

func FailureState(state string) bool {
	switch state {
	case "timeout", "error", "failed":
		return true
	default:
		return false
	}
}
