//go:build windows

package main

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
)

type ptyHandle struct {
	inW    *os.File
	outR   *os.File
	cmd    *exec.Cmd
	closed bool
}

func startPTY(command string, args []string, dir string, env []string, cols, rows uint16) (*ptyHandle, error) {
	if cols == 0 {
		cols = 80
	}
	if rows == 0 {
		rows = 24
	}
	inR, inW, err := os.Pipe()
	if err != nil {
		return nil, err
	}
	outR, outW, err := os.Pipe()
	if err != nil {
		_ = inR.Close()
		_ = inW.Close()
		return nil, err
	}

	cmd := exec.Command(command, args...)
	cmd.Dir = dir
	cmd.Env = ensurePTYEnv(env)
	cmd.Stdin = inR
	cmd.Stdout = outW
	cmd.Stderr = outW
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	if err := cmd.Start(); err != nil {
		_ = inR.Close()
		_ = inW.Close()
		_ = outR.Close()
		_ = outW.Close()
		return nil, err
	}
	_ = inR.Close()
	_ = outW.Close()
	return &ptyHandle{inW: inW, outR: outR, cmd: cmd}, nil
}

func (p *ptyHandle) Read(b []byte) (int, error) {
	if p == nil || p.outR == nil {
		return 0, io.EOF
	}
	return p.outR.Read(b)
}

func (p *ptyHandle) Write(b []byte) (int, error) {
	if p == nil || p.inW == nil {
		return 0, io.EOF
	}
	return p.inW.Write(b)
}

func (p *ptyHandle) Resize(cols, rows uint16) error {
	return nil
}

func (p *ptyHandle) Signal(sig syscall.Signal) error {
	if p == nil || p.cmd == nil || p.cmd.Process == nil {
		return fmt.Errorf("no process")
	}
	return p.cmd.Process.Signal(sig)
}

func (p *ptyHandle) Wait() (int, error) {
	if p == nil || p.cmd == nil {
		return -1, fmt.Errorf("no process")
	}
	err := p.cmd.Wait()
	code := 0
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			code = exitErr.ExitCode()
		} else {
			return -1, err
		}
	}
	return code, nil
}

func (p *ptyHandle) Close() error {
	if p == nil || p.closed {
		return nil
	}
	p.closed = true
	if p.inW != nil {
		_ = p.inW.Close()
	}
	if p.outR != nil {
		_ = p.outR.Close()
	}
	return nil
}

func defaultShellCommand() (string, []string) {
	comspec := os.Getenv("ComSpec")
	if comspec == "" {
		comspec = `C:\Windows\System32\cmd.exe`
	}
	return comspec, []string{"/Q", "/K"}
}

func isInteractiveShell(command string) bool {
	base := strings.ToLower(filepath.Base(command))
	switch base {
	case "cmd.exe", "powershell.exe", "pwsh.exe", "bash.exe":
		return true
	default:
		return strings.Contains(base, "powershell") || strings.Contains(base, "pwsh")
	}
}
