//go:build linux

package main

import (
	"errors"
	"os/exec"
	"sync"
	"sync/atomic"

	"github.com/godbus/dbus/v5"

	"github.com/gfrancodev/overdrive/runtime/tray/internal/browser"
	"github.com/gfrancodev/overdrive/runtime/tray/internal/coreclient"
	"github.com/gfrancodev/overdrive/runtime/tray/internal/quiet"
)

var (
	noteOnce sync.Once
	noteErr  error
	noteConn *dbus.Conn
	noteURL  atomic.Value
)

func notificationsPaused() bool {
	out, err := exec.Command("gsettings", "get", "org.gnome.desktop.notifications", "show-banners").Output()
	return quiet.Paused(quiet.GnomeBannersDisabled(string(out), err), dbusInhibited())
}

func dbusInhibited() bool {
	conn, err := dbus.ConnectSessionBus()
	if err != nil {
		return false
	}
	defer conn.Close()
	value, err := conn.Object("org.freedesktop.Notifications", "/org/freedesktop/Notifications").GetProperty("org.freedesktop.Notifications.Inhibited")
	if err != nil {
		return false
	}
	flag, ok := value.Value().(bool)
	return ok && flag
}

func sendNotice(title, body, openURL string) error {
	if _, err := coreclient.ValidateUIURL(openURL); err != nil {
		return nil
	}
	if err := ensureNotes(); err != nil {
		return errors.New("notification was not sent")
	}
	noteURL.Store(openURL)
	call := noteConn.Object("org.freedesktop.Notifications", "/org/freedesktop/Notifications").Call(
		"org.freedesktop.Notifications.Notify", 0,
		"Overdrive", uint32(0), "", title, body,
		[]string{"default", "Open"},
		map[string]dbus.Variant{"urgency": dbus.MakeVariant(byte(1))},
		int32(5000),
	)
	if call.Err != nil {
		return errors.New("notification was not sent")
	}
	return nil
}

func ensureNotes() error {
	noteOnce.Do(func() {
		conn, err := dbus.ConnectSessionBus()
		if err != nil {
			noteErr = err
			return
		}
		if err := conn.AddMatchSignal(
			dbus.WithMatchInterface("org.freedesktop.Notifications"),
			dbus.WithMatchMember("ActionInvoked"),
		); err != nil {
			_ = conn.Close()
			noteErr = err
			return
		}
		ch := make(chan *dbus.Signal, 4)
		conn.Signal(ch)
		noteConn = conn
		go func() {
			for sig := range ch {
				if sig == nil || len(sig.Body) < 2 {
					continue
				}
				action, _ := sig.Body[1].(string)
				if action != "default" && action != "Open" {
					continue
				}
				raw, _ := noteURL.Load().(string)
				if raw == "" {
					continue
				}
				_ = browser.Open(raw)
			}
		}()
	})
	return noteErr
}
