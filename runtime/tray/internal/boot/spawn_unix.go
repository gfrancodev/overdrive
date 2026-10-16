//go:build !windows

package boot

import (
	"os/exec"
	"syscall"
)

// Spawn detaches the Core so the tray exiting does not stop it.
func Spawn(argv []string, extra []string) error {
	if len(argv) == 0 {
		return errMissingRuntime()
	}
	cmd := exec.Command(argv[0], argv[1:]...)
	cmd.Env = mergeEnv(extra)
	cmd.Stdin = nil
	cmd.Stdout = nil
	cmd.Stderr = nil
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	return cmd.Start()
}
