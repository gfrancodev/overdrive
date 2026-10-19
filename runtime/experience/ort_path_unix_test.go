//go:build !windows

package main

import (
	"testing"
	"unsafe"
)

func TestTheModelPathReachesORTAsUTF8(t *testing.T) {
	path := "/home/joão/.overdrive/models/minilm-l6-v2/model.onnx"
	ptr, err := ortPathPtr(path)
	if err != nil {
		t.Fatal(err)
	}
	raw := unsafe.Slice((*byte)(ptr), len(path)+1)
	if string(raw[:len(path)]) != path || raw[len(path)] != 0 {
		t.Fatalf("path bytes %q", raw)
	}
	if _, err := ortPathPtr("bad\x00path"); err == nil {
		t.Fatal("a path with a NUL was accepted")
	}
}
