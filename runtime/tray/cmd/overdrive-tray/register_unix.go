//go:build !windows

package main

import "github.com/gfrancodev/overdrive/runtime/tray/internal/autostart"

func applyPlatform(plan autostart.Plan) error {
	return autostart.Apply(plan, nil)
}
