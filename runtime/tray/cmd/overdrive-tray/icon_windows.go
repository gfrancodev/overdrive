//go:build windows

package main

import (
	"github.com/gfrancodev/overdrive/runtime/tray/internal/icon"
	"github.com/gfrancodev/overdrive/runtime/tray/internal/whitelabel"
)

func trayIcon() []byte {
	if b, ok := whitelabel.TrayICO(whitelabel.Home()); ok {
		return b
	}
	return icon.ICO()
}
