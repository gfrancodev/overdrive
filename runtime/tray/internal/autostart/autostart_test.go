package autostart

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLinuxPlanIsPerUserAndDoesNotStartCore(t *testing.T) {
	home := t.TempDir()
	exe := filepath.Join(home, "bin", "overdrive-tray")
	plan, err := Build("linux", home, "", exe, 1000)
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Files) != 1 {
		t.Fatal(len(plan.Files))
	}
	if !strings.HasSuffix(plan.Files[0].Path, filepath.Join(".config", "systemd", "user", LinuxUnit)) {
		t.Fatal(plan.Files[0].Path)
	}
	if strings.Contains(plan.Files[0].Path, "/etc/") || strings.Contains(string(plan.Files[0].Data), "share listen") {
		t.Fatalf("unit %s", plan.Files[0].Data)
	}
	text := string(plan.Files[0].Data)
	if !strings.Contains(text, "ExecStart="+exe) || strings.Contains(text, "overdrive-runtime") {
		t.Fatal(text)
	}
	var log []string
	if err := Apply(plan, func(name string, args ...string) error {
		log = append(log, name+" "+strings.Join(args, " "))
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(log, "\n")
	if strings.Contains(joined, "share") || strings.Contains(joined, "overdrive-runtime") {
		t.Fatal(joined)
	}
	if !strings.Contains(joined, "enable "+LinuxUnit) || !strings.Contains(joined, "restart "+LinuxUnit) {
		t.Fatal(joined)
	}
	raw, err := os.ReadFile(plan.Files[0].Path)
	if err != nil || !strings.Contains(string(raw), exe) {
		t.Fatal(err, string(raw))
	}
}

func TestDarwinPlanIsUserLaunchAgent(t *testing.T) {
	home := t.TempDir()
	exe := "/Users/me/.overdrive/bin/overdrive-tray"
	plan, err := Build("darwin", home, "", exe, 501)
	if err != nil {
		t.Fatal(err)
	}
	path := plan.Files[0].Path
	if !strings.HasSuffix(path, filepath.Join("Library", "LaunchAgents", DarwinLabel+".plist")) {
		t.Fatal(path)
	}
	if strings.Contains(path, "/Library/LaunchDaemons") || strings.HasPrefix(path, "/Library") {
		t.Fatal(path)
	}
	text := string(plan.Files[0].Data)
	if !strings.Contains(text, exe) || !strings.Contains(text, "Aqua") || strings.Contains(text, "share listen") {
		t.Fatal(text)
	}
	var cmds []string
	for _, cmd := range plan.Commands {
		cmds = append(cmds, cmd.Name+" "+strings.Join(cmd.Args, " "))
	}
	joined := strings.Join(cmds, "\n")
	if !strings.Contains(joined, "bootstrap gui/501 ") || strings.Contains(joined, "share") {
		t.Fatal(joined)
	}
}

func TestWindowsRunValue(t *testing.T) {
	if WindowsRunValue(`C:\Overdrive\overdrive-tray.exe`) != `C:\Overdrive\overdrive-tray.exe` {
		t.Fatal(WindowsRunValue(`C:\Overdrive\overdrive-tray.exe`))
	}
	quoted := WindowsRunValue(`C:\Program Files\overdrive-tray.exe`)
	if quoted != `"C:\Program Files\overdrive-tray.exe"` {
		t.Fatal(quoted)
	}
	plan, err := Build("windows", `C:\Users\me`, "", `C:\Users\me\.overdrive\bin\overdrive-tray.exe`, 0)
	if err != nil || len(plan.Commands) != 0 || strings.Contains(plan.TrayExe, "share") {
		t.Fatal(plan, err)
	}
}

func TestLinuxUnitStopsOnlyTheTray(t *testing.T) {
	home := t.TempDir()
	plan, err := Build("linux", home, "", filepath.Join(home, "bin", "overdrive-tray"), 1000)
	if err != nil {
		t.Fatal(err)
	}
	// The tray starts the Core and terminal hosts, which land in the unit's cgroup. Restarting the tray on
	// install must not take them down with it.
	if !strings.Contains(string(plan.Files[0].Data), "\nKillMode=process\n") {
		t.Fatalf("unit must stop only the tray process:\n%s", plan.Files[0].Data)
	}
}
