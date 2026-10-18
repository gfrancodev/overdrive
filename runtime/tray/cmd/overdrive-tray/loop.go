package main

import (
	"context"
	"sync"
	"time"

	"fyne.io/systray"

	"github.com/gfrancodev/overdrive/runtime/tray/internal/app"
	"github.com/gfrancodev/overdrive/runtime/tray/internal/boot"
	"github.com/gfrancodev/overdrive/runtime/tray/internal/browser"
	"github.com/gfrancodev/overdrive/runtime/tray/internal/coreclient"
	"github.com/gfrancodev/overdrive/runtime/tray/internal/menu"
	"github.com/gfrancodev/overdrive/runtime/tray/internal/signal"
)

func onReady() {
	systray.SetIcon(trayIcon())
	systray.SetTitle("Status: disconnected")
	systray.SetTooltip("Status: disconnected")
	desk := newDesk()
	go func() {
		time.Sleep(300 * time.Millisecond)
		systray.SetIcon(trayIcon())
	}()
	go poll(desk)
}

type desk struct {
	clicks chan string
	mu     sync.Mutex
	action map[*systray.MenuItem]string

	status   *systray.MenuItem
	mesh     *systray.MenuItem
	open     *systray.MenuItem
	messages *systray.MenuItem
	toggle   *systray.MenuItem
	prefs    *systray.MenuItem
	restart  *systray.MenuItem
	power    *systray.MenuItem
}

func newDesk() *desk {
	d := &desk{
		clicks: make(chan string, 8),
		action: map[*systray.MenuItem]string{},
	}
	d.status = d.item("Status: disconnected")
	d.mesh = d.item("Mesh: unknown")
	systray.AddSeparator()
	d.open = d.item("Open Overdrive")
	d.messages = d.item("Messages")
	systray.AddSeparator()
	d.toggle = d.item("Pause Mesh")
	d.prefs = d.item("Preferences")
	systray.AddSeparator()
	d.restart = d.item("Restart Overdrive")
	d.power = d.item("Start Overdrive")
	return d
}

func (d *desk) item(title string) *systray.MenuItem {
	it := systray.AddMenuItem(title, title)
	it.Disable()
	go func() {
		for range it.ClickedCh {
			d.mu.Lock()
			action := d.action[it]
			d.mu.Unlock()
			if action == "" {
				continue
			}
			select {
			case d.clicks <- action:
			default:
			}
		}
	}()
	return it
}

func (d *desk) Apply(view menu.View) {
	systray.SetTitle(view.StatusText)
	systray.SetTooltip(view.StatusText)
	d.set(d.status, view.StatusText, "")
	d.set(d.mesh, view.MeshText, "")
	d.set(d.open, view.Open.Title, view.Open.Action)
	d.set(d.messages, view.Messages.Title, view.Messages.Action)
	d.set(d.toggle, view.MeshToggle.Title, view.MeshToggle.Action)
	d.set(d.prefs, view.Prefs.Title, view.Prefs.Action)
	d.set(d.restart, view.Restart.Title, view.Restart.Action)
	d.set(d.power, view.Power.Title, view.Power.Action)
}

func (d *desk) set(it *systray.MenuItem, title, action string) {
	it.SetTitle(title)
	it.SetTooltip(title)
	if action == "" {
		it.Disable()
	} else {
		it.Enable()
	}
	d.mu.Lock()
	d.action[it] = action
	d.mu.Unlock()
}

type browserOpener struct{}

func (browserOpener) Open(raw string) error { return browser.Open(raw) }

func poll(d *desk) {
	client, err := coreclient.New("")
	if err != nil {
		logErr(err)
		return
	}
	listen, ui, err := boot.PortsFromBase(client.Base())
	if err != nil {
		logErr(err)
		return
	}
	application := &app.App{
		Client: client,
		Opener: browserOpener{},
		Starter: &boot.Starter{
			ListenAddr: listen,
			UIAddr:     ui,
			PortOpen:   boot.DialOpen,
			Probe: func(ctx context.Context) bool {
				_, probeErr := client.UI(ctx)
				return probeErr == nil
			},
		},
	}
	ctx := context.Background()
	if err := application.Launch(ctx); err != nil {
		logErr(err)
	}
	refresh := func() {
		view, notes, err := application.Refresh(ctx)
		if err != nil {
			logErr(err)
			return
		}
		d.Apply(view)
		if notificationsPaused() {
			return
		}
		for _, note := range notes {
			title, body := signal.PublicText(note.Kind)
			openURL := application.UIURL()
			switch note.Kind {
			case signal.IncomingCall:
				openURL = coreclient.CallDeepLink(openURL)
				playIncomingCallSound()
			case signal.SessionInvite, signal.ControlRequest:
				openURL = coreclient.SessionsDeepLink(openURL)
			}
			if err := sendNotice(title, body, openURL); err != nil {
				logErr(err)
			}
		}
	}
	refresh()
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case action := <-d.clicks:
			if err := application.Activate(ctx, action); err != nil {
				logErr(err)
			}
			refresh()
		case <-ticker.C:
			refresh()
		}
	}
}
