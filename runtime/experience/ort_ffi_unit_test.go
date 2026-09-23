//go:build !overdrive_fake_ort

package main

import (
	"errors"
	"testing"
)

func TestOpenORTSessionFFILibOpenFail(t *testing.T) {
	resetHooksForTest()
	defer resetHooksForTest()
	hookOpenDynamicLib = func(string) (uintptr, error) {
		return 0, errors.New("dlopen fail")
	}
	if _, err := openORTSessionFFI("/tmp/fake.so", "/tmp/model.onnx"); err == nil {
		t.Fatal("expected dlopen error")
	}
}
