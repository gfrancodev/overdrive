// Package browser opens the Core UI in the default browser.
package browser

import (
	"errors"
	"os/exec"
	"runtime"

	"github.com/gfrancodev/overdrive/runtime/tray/internal/coreclient"
)

// CommandFor is the OS open command. The URL is one argument and is not passed through a shell.
func CommandFor(goos, rawURL string) (string, []string, error) {
	safe, err := coreclient.ValidateUIURL(rawURL)
	if err != nil {
		return "", nil, err
	}
	switch goos {
	case "darwin":
		return "open", []string{safe}, nil
	case "windows":
		return "rundll32", []string{"url.dll,FileProtocolHandler", safe}, nil
	default:
		return "xdg-open", []string{safe}, nil
	}
}

// Open starts the default browser and does not wait for it to exit.
func Open(rawURL string) error {
	name, args, err := CommandFor(runtime.GOOS, rawURL)
	if err != nil {
		return err
	}
	cmd := exec.Command(name, args...)
	cmd.Stdin = nil
	cmd.Stdout = nil
	cmd.Stderr = nil
	if err := cmd.Start(); err != nil {
		return err
	}
	go func() { _ = cmd.Wait() }()
	return nil
}

// ErrNoBrowser is unused by Open; callers may wrap command errors.
var ErrNoBrowser = errors.New("browser did not start")
