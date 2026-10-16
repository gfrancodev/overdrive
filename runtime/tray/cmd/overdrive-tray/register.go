package main

import (
	"os"
	"os/user"
	"path/filepath"
	"runtime"
	"strconv"

	"github.com/gfrancodev/overdrive/runtime/tray/internal/autostart"
)

func runRegister() int {
	exe, err := os.Executable()
	if err != nil {
		logErr(err)
		return 1
	}
	if resolved, err := filepath.EvalSymlinks(exe); err == nil {
		exe = resolved
	}
	home, err := os.UserHomeDir()
	if err != nil {
		logErr(err)
		return 1
	}
	uid := os.Getuid()
	if runtime.GOOS == "windows" {
		uid = 0
	}
	if runtime.GOOS != "windows" {
		if current, err := user.Current(); err == nil {
			if n, err := strconv.Atoi(current.Uid); err == nil {
				uid = n
			}
		}
	}
	plan, err := autostart.Build(runtime.GOOS, home, os.Getenv("XDG_CONFIG_HOME"), exe, uid)
	if err != nil {
		logErr(err)
		return 1
	}
	if err := applyPlatform(plan); err != nil {
		logErr(err)
		return 1
	}
	return 0
}
