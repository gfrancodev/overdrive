//go:build linux

package main

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"syscall"

	"golang.org/x/sys/unix"
)

type ptyHandle struct {
	ptm    *os.File
	cmd    *exec.Cmd
	closed bool
}

func openUnixPTY() (*os.File, string, error) {
	ptm, err := os.OpenFile("/dev/ptmx", os.O_RDWR, 0)
	if err != nil {
		return nil, "", err
	}
	if err := unix.IoctlSetPointerInt(int(ptm.Fd()), unix.TIOCSPTLCK, 0); err != nil {
		_ = ptm.Close()
		return nil, "", err
	}
	n, err := unix.IoctlGetInt(int(ptm.Fd()), unix.TIOCGPTN)
	if err != nil {
		_ = ptm.Close()
		return nil, "", err
	}
	return ptm, fmt.Sprintf("/dev/pts/%d", n), nil
}

func startPTY(command string, args []string, dir string, env []string, cols, rows uint16) (*ptyHandle, error) {
	if cols == 0 {
		cols = 80
	}
	if rows == 0 {
		rows = 24
	}
	ptm, slavePath, err := openUnixPTY()
	if err != nil {
		return nil, fmt.Errorf("ptmx: %w", err)
	}
	if err := resizeUnixPTY(ptm, cols, rows); err != nil {
		_ = ptm.Close()
		return nil, err
	}
	slave, err := os.OpenFile(slavePath, os.O_RDWR, 0)
	if err != nil {
		_ = ptm.Close()
		return nil, err
	}

	cmd := exec.Command(command, args...)
	cmd.Dir = dir
	cmd.Env = ensurePTYEnv(env)
	cmd.Stdin = slave
	cmd.Stdout = slave
	cmd.Stderr = slave
	cmd.SysProcAttr = &syscall.SysProcAttr{
		Setsid:  true,
		Setctty: true,
		Ctty:    0,
	}
	if err := cmd.Start(); err != nil {
		_ = slave.Close()
		_ = ptm.Close()
		return nil, err
	}
	_ = slave.Close()
	return &ptyHandle{ptm: ptm, cmd: cmd}, nil
}

func resizeUnixPTY(ptm *os.File, cols, rows uint16) error {
	ws := &unix.Winsize{Col: cols, Row: rows}
	return unix.IoctlSetWinsize(int(ptm.Fd()), unix.TIOCSWINSZ, ws)
}

func (p *ptyHandle) Read(b []byte) (int, error) {
	if p == nil || p.ptm == nil {
		return 0, io.EOF
	}
	return p.ptm.Read(b)
}

func (p *ptyHandle) Write(b []byte) (int, error) {
	if p == nil || p.ptm == nil {
		return 0, io.EOF
	}
	return p.ptm.Write(b)
}

func (p *ptyHandle) Resize(cols, rows uint16) error {
	if p == nil || p.ptm == nil {
		return fmt.Errorf("pty closed")
	}
	return resizeUnixPTY(p.ptm, cols, rows)
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
	if p.ptm != nil {
		_ = p.ptm.Close()
	}
	return nil
}

