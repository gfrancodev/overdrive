//go:build windows

package main

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

type ptyHandle struct {
	conIn  *windows.Handle
	conOut *windows.Handle
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
	inRead, inWrite, err := os.Pipe()
	if err != nil {
		return nil, err
	}
	outRead, outWrite, err := os.Pipe()
	if err != nil {
		_ = inRead.Close()
		_ = inWrite.Close()
		return nil, err
	}

	coord := windows.Coord{X: int16(cols), Y: int16(rows)}
	var hIn, hOut windows.Handle
	if err := windows.CreatePseudoConsole(coord, windows.Handle(inRead.Fd()), windows.Handle(outWrite.Fd()), 0, &hIn, &hOut); err != nil {
		_ = inRead.Close()
		_ = inWrite.Close()
		_ = outRead.Close()
		_ = outWrite.Close()
		return nil, fmt.Errorf("CreatePseudoConsole: %w", err)
	}
	_ = inRead.Close()
	_ = outWrite.Close()

	exe, err := windows.UTF16PtrFromString(command)
	if err != nil {
		closePseudo(hIn, hOut)
		return nil, err
	}
	argv, err := windows.UTF16PtrFromString(command)
	if err != nil {
		closePseudo(hIn, hOut)
		return nil, err
	}
	attr := &windows.ProcThreadAttributeListContainer{}
	cmd := exec.Command(command, args...)
	cmd.Dir = dir
	cmd.Env = ensurePTYEnv(env)
	cmd.SysProcAttr = &syscall.SysProcAttr{
		CreationFlags: windows.CREATE_UNICODE_ENVIRONMENT | windows.EXTENDED_STARTUPINFO_PRESENT,
	}
	if len(args) > 0 {
		_ = argv
	}
	_ = exe
	_ = attr

	// Attach ConPTY to child via STARTUPINFOEX — simplified path uses extra files.
	cmd.ExtraFiles = []*os.File{os.NewFile(uintptr(hIn), "conin"), os.NewFile(uintptr(hOut), "conout")}
	cmd.Stdin = os.NewFile(uintptr(hIn), "stdin")
	cmd.Stdout = os.NewFile(uintptr(outRead.Fd()), "stdout")
	cmd.Stderr = os.NewFile(uintptr(outRead.Fd()), "stderr")
	if err := cmd.Start(); err != nil {
		closePseudo(hIn, hOut)
		_ = outRead.Close()
		return nil, err
	}
	return &ptyHandle{conIn: &hIn, conOut: &hOut, cmd: cmd}, nil
}

func closePseudo(hIn, hOut windows.Handle) {
	_ = windows.ClosePseudoConsole(hIn)
	_ = windows.CloseHandle(hOut)
}

func (p *ptyHandle) Read(b []byte) (int, error) {
	if p == nil || p.cmd == nil || p.cmd.Stdout == nil {
		return 0, io.EOF
	}
	if f, ok := p.cmd.Stdout.(*os.File); ok {
		return f.Read(b)
	}
	return 0, io.EOF
}

func (p *ptyHandle) Write(b []byte) (int, error) {
	if p == nil || p.cmd == nil || p.cmd.Stdin == nil {
		return 0, io.EOF
	}
	if f, ok := p.cmd.Stdin.(*os.File); ok {
		return f.Write(b)
	}
	return 0, io.EOF
}

func (p *ptyHandle) Resize(cols, rows uint16) error {
	if p == nil || p.conIn == nil {
		return fmt.Errorf("pty closed")
	}
	coord := windows.Coord{X: int16(cols), Y: int16(rows)}
	return windows.ResizePseudoConsole(*p.conIn, coord)
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
	if p.conIn != nil {
		_ = windows.ClosePseudoConsole(*p.conIn)
	}
	if p.conOut != nil {
		_ = windows.CloseHandle(*p.conOut)
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

// silence unused on older windows builds
var _ = unsafe.Pointer(nil)
