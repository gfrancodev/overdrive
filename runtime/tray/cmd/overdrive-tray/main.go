package main

import (
	"fmt"
	"os"
	"path/filepath"

	"fyne.io/systray"

	"github.com/gfrancodev/overdrive/runtime/tray/internal/single"
)

func main() {
	if len(os.Args) > 1 && os.Args[1] == "register" {
		os.Exit(runRegister())
	}
	release, ok, err := single.Acquire(lockPath())
	if err != nil {
		logErr(err)
		os.Exit(1)
	}
	if !ok {
		return
	}
	defer release()
	systray.Run(onReady, func() {})
}

func lockPath() string {
	if dir := os.Getenv("XDG_RUNTIME_DIR"); dir != "" {
		return filepath.Join(dir, "overdrive-tray.lock")
	}
	home := os.Getenv("OVERDRIVE_HOME")
	if home == "" {
		if user, err := os.UserHomeDir(); err == nil {
			home = filepath.Join(user, ".overdrive")
		}
	}
	if home == "" {
		return filepath.Join(os.TempDir(), "overdrive-tray.lock")
	}
	if err := os.MkdirAll(home, 0o700); err != nil {
		return filepath.Join(os.TempDir(), "overdrive-tray.lock")
	}
	return filepath.Join(home, "tray.lock")
}

func logErr(err error) {
	if err == nil {
		return
	}
	msg := err.Error()
	if len(msg) > 180 {
		msg = msg[:180]
	}
	fmt.Fprintf(os.Stderr, "overdrive-tray: %s\n", msg)
}
