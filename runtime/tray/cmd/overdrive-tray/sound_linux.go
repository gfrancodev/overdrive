//go:build linux

package main

import "os/exec"

func playIncomingCallSound() {
	_ = exec.Command("canberra-gtk-play", "-i", "phone-incoming-call").Run()
}
