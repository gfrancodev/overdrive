// Package autostart writes the per-user tray registration. It never starts the Core.
package autostart

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

const (
	LinuxUnit      = "overdrive-tray.service"
	DarwinLabel    = "com.overdrive.tray"
	WindowsRunKey  = `Software\Microsoft\Windows\CurrentVersion\Run`
	WindowsRunName = "OverdriveTray"
)

// Command is an enable step. Optional failures leave the unit file in place.
type Command struct {
	Name     string
	Args     []string
	Optional bool
}

// File is a user-level unit or agent definition.
type File struct {
	Path string
	Data []byte
	Mode os.FileMode
}

// Plan is the registration for one OS. TrayExe is the tray binary, never the Core.
type Plan struct {
	Files    []File
	Commands []Command
	TrayExe  string
}

// Build returns a per-user plan. System locations such as /Library or /etc are not used.
func Build(goos, home, xdg, exe string, uid int) (Plan, error) {
	if exe == "" || home == "" {
		return Plan{}, fmt.Errorf("tray registration needs a home and a binary")
	}
	switch goos {
	case "linux":
		dir := xdg
		if dir == "" {
			dir = filepath.Join(home, ".config")
		}
		path := filepath.Join(dir, "systemd", "user", LinuxUnit)
		return Plan{
			TrayExe: exe,
			Files: []File{{
				Path: path,
				Mode: 0o644,
				Data: []byte(systemdUnit(exe)),
			}},
			Commands: []Command{
				{Name: "systemctl", Args: []string{"--user", "daemon-reload"}, Optional: true},
				{Name: "systemctl", Args: []string{"--user", "enable", LinuxUnit}, Optional: true},
				{Name: "systemctl", Args: []string{"--user", "restart", LinuxUnit}, Optional: true},
			},
		}, nil
	case "darwin":
		path := filepath.Join(home, "Library", "LaunchAgents", DarwinLabel+".plist")
		target := fmt.Sprintf("gui/%d/%s", uid, DarwinLabel)
		return Plan{
			TrayExe: exe,
			Files: []File{{
				Path: path,
				Mode: 0o644,
				Data: []byte(launchAgent(exe)),
			}},
			Commands: []Command{
				{Name: "launchctl", Args: []string{"bootout", target}, Optional: true},
				{Name: "launchctl", Args: []string{"bootstrap", fmt.Sprintf("gui/%d", uid), path}},
			},
		}, nil
	case "windows":
		return Plan{TrayExe: exe}, nil
	default:
		return Plan{TrayExe: exe}, nil
	}
}

func systemdUnit(exe string) string {
	return "[Unit]\nDescription=Overdrive tray\nAfter=graphical-session.target\n\n[Service]\nType=simple\nExecStart=" + quoteExec(exe) + "\nRestart=on-failure\nRestartSec=10\nKillMode=process\n\n[Install]\nWantedBy=graphical-session.target\n"
}

func quoteExec(exe string) string {
	if strings.ContainsAny(exe, " \t") {
		return `"` + strings.ReplaceAll(exe, `"`, ``) + `"`
	}
	return exe
}

func launchAgent(exe string) string {
	return `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>Label</key>
  <string>` + DarwinLabel + `</string>
  <key>ProgramArguments</key>
  <array>
    <string>` + xmlEscape(exe) + `</string>
  </array>
  <key>RunAtLoad</key>
  <true/>
  <key>KeepAlive</key>
  <false/>
  <key>LimitLoadToSessionType</key>
  <string>Aqua</string>
</dict>
</plist>
`
}

func xmlEscape(s string) string {
	s = strings.ReplaceAll(s, "&", "&amp;")
	s = strings.ReplaceAll(s, "<", "&lt;")
	s = strings.ReplaceAll(s, ">", "&gt;")
	return s
}

// WindowsRunValue is the per-user Run key value.
func WindowsRunValue(exe string) string {
	if strings.ContainsAny(exe, " \t") {
		return `"` + exe + `"`
	}
	return exe
}

// Apply writes files and runs enable commands. It does not launch the Core.
func Apply(plan Plan, run func(name string, args ...string) error) error {
	for _, file := range plan.Files {
		if err := os.MkdirAll(filepath.Dir(file.Path), 0o755); err != nil {
			return err
		}
		mode := file.Mode
		if mode == 0 {
			mode = 0o644
		}
		if err := os.WriteFile(file.Path, file.Data, mode); err != nil {
			return err
		}
	}
	if run == nil {
		run = Exec
	}
	var first error
	for _, cmd := range plan.Commands {
		if err := run(cmd.Name, cmd.Args...); err != nil && !cmd.Optional && first == nil {
			first = err
		}
	}
	return first
}

// Exec runs an enable command without capturing output that might contain local paths.
func Exec(name string, args ...string) error {
	cmd := exec.Command(name, args...)
	cmd.Stdin = nil
	cmd.Stdout = nil
	cmd.Stderr = os.Stderr
	return cmd.Run()
}
