package main

import (
	"path/filepath"
	"strings"
)

// matchesORTZipEntry reports whether a zip member is an ONNX Runtime Windows DLL payload.
func matchesORTZipEntry(name string) bool {
	base := filepath.Base(name)
	return strings.Contains(base, "onnxruntime") && strings.HasSuffix(base, ".dll")
}
