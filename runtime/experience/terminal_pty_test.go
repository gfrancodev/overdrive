package main

import (
	"bytes"
	"os"
	"os/exec"
	"testing"
	"time"
)

func TestPTYStartAndEcho(t *testing.T) {
	if os.Getenv("CI") != "" && !isTerminalTestEnabled() {
		t.Skip("PTY integration skipped on CI without OVERDRIVE_PTY_TEST=1")
	}
	home := t.TempDir()
	cmd, args := defaultShellCommand()
	pty, err := startPTY(cmd, append(args, "-c", "echo pty_ok"), home, []string{"TERM=xterm", "HOME=" + home}, 80, 24)
	if err != nil {
		t.Skip("PTY unavailable:", err)
	}
	defer pty.Close()
	buf := make([]byte, 4096)
	deadline := time.Now().Add(5 * time.Second)
	var out []byte
	for time.Now().Before(deadline) {
		n, _ := pty.Read(buf)
		if n > 0 {
			out = append(out, buf[:n]...)
			if containsSub(string(out), "pty_ok") {
				return
			}
		}
		time.Sleep(50 * time.Millisecond)
	}
	_ = pty.Close()
	t.Fatalf("expected pty_ok in output, got %q", string(out))
}

func isTerminalTestEnabled() bool {
	return os.Getenv("OVERDRIVE_PTY_TEST") == "1"
}

func TestPTYClearEmitsEraseDisplay(t *testing.T) {
	if _, err := exec.LookPath("clear"); err != nil {
		t.Skip("clear not installed")
	}
	home := t.TempDir()
	env := ensurePTYEnv([]string{
		"TERM=dumb",
		"HOME=" + home,
		"PATH=" + os.Getenv("PATH"),
	})
	pty, err := startPTY("/bin/sh", []string{"-c", "printf before; clear; printf AFTER"}, home, env, 80, 24)
	if err != nil {
		t.Skip("PTY unavailable:", err)
	}
	defer pty.Close()
	buf := make([]byte, 8192)
	deadline := time.Now().Add(5 * time.Second)
	var out []byte
	for time.Now().Before(deadline) {
		n, _ := pty.Read(buf)
		if n > 0 {
			out = append(out, buf[:n]...)
			if bytes.Contains(out, []byte("AFTER")) {
				break
			}
		}
		time.Sleep(30 * time.Millisecond)
	}
	if !bytes.Contains(out, []byte{0x1b}) {
		t.Fatalf("clear with forced TERM must emit CSI, got %q", out)
	}
	if !bytes.Contains(out, []byte("AFTER")) {
		t.Fatalf("expected AFTER after clear, got %q", out)
	}
}
