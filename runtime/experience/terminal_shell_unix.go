//go:build linux || darwin

package main

import (
	"os"
	"strings"
)

func defaultShellCommand() (string, []string) {
	shell := os.Getenv("SHELL")
	if shell == "" {
		shell = "/bin/sh"
	}
	return shell, []string{"-i"}
}

func isInteractiveShell(command string) bool {
	base := strings.ToLower(command)
	if i := strings.LastIndex(base, "/"); i >= 0 {
		base = base[i+1:]
	}
	switch base {
	case "bash", "zsh", "sh", "fish", "dash":
		return true
	default:
		return strings.Contains(base, "bash") || strings.Contains(base, "zsh")
	}
}
