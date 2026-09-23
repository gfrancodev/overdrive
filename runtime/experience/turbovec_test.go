package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestTurboVecUnavailableWithoutLib(t *testing.T) {
	resetRuntimeGlobals()
	home := t.TempDir()
	t.Setenv("OVERDRIVE_TURBOVEC_LIB", filepath.Join(home, "missing.so"))
	tv := openTurboVec(home, hashedDims)
	if tv.Available() {
		t.Skip("turbovec lib unexpectedly available")
	}
	tv.Add("mem_test", embedText("hello"))
	tv.Remove("mem_test")
	tv.Sync()
	tv.Close()
	if normalizeVectorScore(0) != 0 {
		t.Fatal("zero score")
	}
	if normalizeVectorScore(5) <= 0 {
		t.Fatal("positive score")
	}
	if turbovecLibName() == "" {
		t.Fatal("lib name")
	}
	if resolveTurboVecLib(home) == "" {
		// expected without lib
	}
}

func TestTurboVecWithBuiltLib(t *testing.T) {
	lib := filepath.Join("..", "turbovec-ffi", "target", "release", "liboverdrive_turbovec_ffi.so")
	if _, err := os.Stat(lib); err != nil {
		t.Skip("turbovec ffi not built")
	}
	resetRuntimeGlobals()
	home := t.TempDir()
	abs, _ := filepath.Abs(lib)
	t.Setenv("OVERDRIVE_TURBOVEC_LIB", abs)
	tv := openTurboVecAt(filepath.Join(home, "test.tvim"), home, hashedDims)
	if !tv.Available() {
		t.Fatal("expected turbovec available")
	}
	id := "mem_turbovec_test_1234567890"
	vec := embedText("proposal repository persistence")
	tv.Add(id, vec)
	ids, scores := tv.Search(vec, 3, nil)
	if len(ids) == 0 {
		t.Fatal("search returned nothing")
	}
	if len(scores) != len(ids) {
		t.Fatal("score length mismatch")
	}
	tv.Remove(id)
	tv.Sync()
	tv.Close()
	installBundledTurboVecLib(home)
}
