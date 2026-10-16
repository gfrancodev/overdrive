//go:build windows

package main

import (
	"github.com/gfrancodev/overdrive/runtime/tray/internal/autostart"
	"github.com/gfrancodev/overdrive/runtime/tray/internal/boot"
	"github.com/gfrancodev/overdrive/runtime/tray/internal/single"

	"golang.org/x/sys/windows/registry"
)

func applyPlatform(plan autostart.Plan) error {
	key, _, err := registry.CreateKey(registry.CURRENT_USER, autostart.WindowsRunKey, registry.SET_VALUE)
	if err != nil {
		return err
	}
	defer key.Close()
	if err := key.SetStringValue(autostart.WindowsRunName, autostart.WindowsRunValue(plan.TrayExe)); err != nil {
		return err
	}
	if trayRunning() {
		return nil
	}
	return boot.Spawn([]string{plan.TrayExe}, nil)
}

func trayRunning() bool {
	release, ok, err := single.Acquire(lockPath())
	if err != nil || !ok {
		return err == nil
	}
	release()
	return false
}
