//go:build windows

package boot

import "os/exec"

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
	cmd.SysProcAttr = detachAttr()
	if err := cmd.Start(); err != nil {
		return err
	}
	// The tray is the Core's parent: without a wait, each Core it started and that later stopped stayed behind
	// as a zombie until the tray exited.
	go func() { _ = cmd.Wait() }()
	return nil
}
