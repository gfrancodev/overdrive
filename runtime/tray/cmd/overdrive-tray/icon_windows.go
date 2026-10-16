//go:build windows

package main

import "github.com/gfrancodev/overdrive/runtime/tray/internal/icon"

func trayIcon() []byte { return icon.ICO() }
