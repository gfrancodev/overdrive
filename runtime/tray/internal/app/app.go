// Package app is the tray control loop: observe the Core, open its UI, and start it only when it is down.
package app

import (
	"context"
	"errors"

	"github.com/gfrancodev/overdrive/runtime/tray/internal/boot"
	"github.com/gfrancodev/overdrive/runtime/tray/internal/coreclient"
	"github.com/gfrancodev/overdrive/runtime/tray/internal/menu"
	"github.com/gfrancodev/overdrive/runtime/tray/internal/signal"
)

// Opener opens a loopback UI URL in the default browser.
type Opener interface {
	Open(rawURL string) error
}

// App talks to one Core client. It keeps running when the Core is stopped.
type App struct {
	Client  *coreclient.Client
	Opener  Opener
	Starter *boot.Starter

	prev   signal.Snapshot
	seeded bool
	url    string
}

// Launch starts the Core when the tray opens (at login) and nothing is serving yet, so the dashboard is ready
// without a click. A Core that is already up, or a disabled UI, is left alone.
func (a *App) Launch(ctx context.Context) error {
	if a == nil || a.Starter == nil {
		return nil
	}
	if _, err := a.Starter.Ensure(ctx); err != nil && !errors.Is(err, boot.ErrUIDisabled) {
		return err
	}
	return nil
}

// Refresh reads the admin API and returns menu state plus new notices.
// The first successful peer reading is a baseline and does not notify.
func (a *App) Refresh(ctx context.Context) (menu.View, []signal.Notice, error) {
	if a == nil || a.Client == nil {
		return menu.Build(menu.Input{}), nil, errors.New("missing core client")
	}
	ui, err := a.Client.UI(ctx)
	if err != nil {
		a.seeded = false
		a.url = ""
		var se *coreclient.StatusError
		if errors.As(err, &se) {
			return menu.Build(menu.Input{Connected: true, HTTPError: true}), nil, nil
		}
		return menu.Build(menu.Input{}), nil, nil
	}
	a.url = ui.URL
	in := menu.Input{Connected: true, Mesh: ui.Mesh, CanOpen: false}
	if _, err := coreclient.ValidateUIURL(ui.URL); err == nil {
		in.CanOpen = true
	}
	peers, peerErr := a.Client.Peers(ctx)
	var snap signal.Snapshot
	if peerErr == nil {
		in.PeersKnown = peers.CountKnown
		in.PeerCount = peers.Count
		for _, link := range peers.Links {
			in.Links = append(in.Links, menu.Link{State: link.State})
			snap.Links = append(snap.Links, signal.Link{Name: link.Name, State: link.State})
		}
		snap.Handoffs = append([]string(nil), peers.Handoffs...)
		snap.RingingCallID = peers.RingingCallID
		snap.SessionInviteID = peers.TerminalInviteID
		snap.ControlRequestID = peers.TerminalControlID
		if n, known := a.Client.Unread(ctx, peers.MeshIDs); known {
			in.UnreadKnown = true
			in.Unread = n
		}
	}
	var notes []signal.Notice
	if peerErr == nil {
		notes = signal.Diff(a.prev, snap, a.seeded)
		a.prev = snap
		a.seeded = true
	}
	return menu.Build(in), notes, nil
}

// Activate runs one menu action. Notifications are not a second bus; open uses the Core UI URL.
func (a *App) Activate(ctx context.Context, action string) error {
	switch action {
	case menu.ActionOpen, menu.ActionMessages, menu.ActionPreferences:
		return a.OpenUI(ctx)
	case menu.ActionPause:
		_, err := a.Client.Pause(ctx)
		return err
	case menu.ActionResume:
		_, err := a.Client.Resume(ctx)
		return err
	case menu.ActionStop:
		_, err := a.Client.Stop(ctx)
		return err
	case menu.ActionStart:
		if a.Starter == nil {
			return errors.New("core start is unavailable")
		}
		_, err := a.Starter.Ensure(ctx)
		return err
	default:
		return nil
	}
}

// OpenUI loads GET /api/core/ui and opens that URL.
func (a *App) OpenUI(ctx context.Context) error {
	if a.Opener == nil {
		return errors.New("browser is unavailable")
	}
	ui, err := a.Client.UI(ctx)
	if err != nil {
		return err
	}
	return a.Opener.Open(ui.URL)
}

// UIURL is the last loopback UI URL, used as the notification click target.
func (a *App) UIURL() string {
	return a.url
}
