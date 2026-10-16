//go:build windows

package main

import "os/exec"

func playIncomingCallSound() {
	_ = exec.Command("powershell", "-NoProfile", "-Command", "(New-Object Media.SoundPlayer 'C:\\Windows\\Media\\Ring01.wav').PlaySync()").Run()
}
