//go:build darwin

package main

import "os/exec"

func playIncomingCallSound() {
	_ = exec.Command("afplay", "/System/Library/Sounds/Ping.aiff").Run()
}
