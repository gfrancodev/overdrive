//go:build !windows

package main

import (
	"os/exec"
	"syscall"
)

func configureSessionHostCmd(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

func processAlive(pid int) bool {
	return syscall.Kill(pid, 0) == nil
}

func terminateProcess(pid int) {
	_ = syscall.Kill(pid, syscall.SIGTERM)
}
