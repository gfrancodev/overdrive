package main

import (
	"bytes"
	"encoding/json"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

type exitSignal struct{}

func resetRuntimeGlobals() {
	resetHooksForTest()
	resetORTLibForTest()
	embedOnce = sync.Once{}
	globalEmbedder = nil
	globalEmbedderName = ""
	tvOnce = sync.Once{}
	tvLoaded = false
	tvOpen = nil
	tvClose = nil
	tvAdd = nil
	tvRemove = nil
	tvSearch = nil
	tvSync = nil
	shareListenerOnce = sync.Once{}
	shareListenerInst = nil
}

func runCLICapture(t *testing.T, args []string) (stdout, stderr string, code int) {
	t.Helper()
	stdoutR, stdoutW, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	stderrR, stderrW, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	oldOut, oldErr := os.Stdout, os.Stderr
	os.Stdout, os.Stderr = stdoutW, stderrW

	code = 0
	oldExit := exitProcess
	exitProcess = func(c int) {
		code = c
		panic(exitSignal{})
	}
	defer func() {
		exitProcess = oldExit
		os.Stdout = oldOut
		os.Stderr = oldErr
	}()

	func() {
		defer func() { recover() }()
		if c := runCLI(args); c != 0 {
			code = c
		}
	}()

	_ = stdoutW.Close()
	_ = stderrW.Close()
	stdoutBytes, _ := io.ReadAll(stdoutR)
	stderrBytes, _ := io.ReadAll(stderrR)
	return string(stdoutBytes), string(stderrBytes), code
}

func mustJSON(t *testing.T, stdout string) map[string]any {
	t.Helper()
	var out map[string]any
	if err := json.Unmarshal(bytes.TrimSpace([]byte(stdout)), &out); err != nil {
		t.Fatalf("json decode: %v\nstdout=%q", err, stdout)
	}
	return out
}

func setupTestEnv(t *testing.T, home string) {
	t.Helper()
	resetRuntimeGlobals()
	t.Setenv("OVERDRIVE_HOME", home)
	t.Setenv("OVERDRIVE_EMBEDDER", "stub")
	t.Setenv("OVERDRIVE_SKIP_EMBED_DOWNLOAD", "1")
	lib := filepath.Join("..", "turbovec-ffi", "target", "release", "liboverdrive_turbovec_ffi.so")
	if st, err := os.Stat(lib); err == nil && !st.IsDir() {
		abs, _ := filepath.Abs(lib)
		t.Setenv("OVERDRIVE_TURBOVEC_LIB", abs)
	}
}

func makeTestRepo(t *testing.T, dir string, remote string) string {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	gitRun(t, dir, "init", "-q")
	gitRun(t, dir, "remote", "add", "origin", remote)
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("# test\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	abs, err := filepath.Abs(dir)
	if err != nil {
		t.Fatal(err)
	}
	return abs
}

func gitRun(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
}

func freeTCPPort(t *testing.T) int {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	addr, ok := ln.Addr().(*net.TCPAddr)
	if !ok {
		t.Fatal("tcp addr")
	}
	return addr.Port
}
