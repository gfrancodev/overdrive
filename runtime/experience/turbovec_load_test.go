//go:build linux

package main

import (
	"os"
	"testing"
)

func TestALibraryWithoutTheTurboVecSymbolsLeavesTurboVecOff(t *testing.T) {
	libc := ""
	for _, p := range []string{"/lib/x86_64-linux-gnu/libc.so.6", "/usr/lib/x86_64-linux-gnu/libc.so.6", "/lib/aarch64-linux-gnu/libc.so.6", "/usr/lib/aarch64-linux-gnu/libc.so.6"} {
		if _, err := os.Stat(p); err == nil {
			libc = p
			break
		}
	}
	if libc == "" {
		t.Skip("no libc to stand in for a broken TurboVec build")
	}
	resetRuntimeGlobals()
	t.Cleanup(resetRuntimeGlobals)
	t.Setenv("OVERDRIVE_TURBOVEC_LIB", libc)
	// A library that loads but lacks od_tv_* (the Windows DLLs built since 0.3.0) must not crash the
	// runtime: the engine runs without TurboVec, as it does when the library is missing.
	if loadTurboVecLib(t.TempDir()) {
		t.Fatal("a library without od_tv_open must not count as TurboVec")
	}
	if tvOpen != nil || tvSync != nil {
		t.Fatal("no TurboVec function may stay bound to a half-loaded library")
	}
}
