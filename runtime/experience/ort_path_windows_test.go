//go:build windows

package main

import (
	"testing"
	"unicode/utf16"
	"unsafe"
)

func TestTheModelPathReachesORTAsUTF16(t *testing.T) {
	path := `C:\Users\joão\.overdrive\models\minilm-l6-v2\model.onnx`
	ptr, err := ortPathPtr(path)
	if err != nil {
		t.Fatal(err)
	}
	want := utf16.Encode([]rune(path))
	raw := unsafe.Slice((*uint16)(ptr), len(want)+1)
	if string(utf16.Decode(raw[:len(want)])) != path || raw[len(want)] != 0 {
		t.Fatalf("path units %v", raw)
	}
}
