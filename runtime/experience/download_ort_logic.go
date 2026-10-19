package main

import (
	"path/filepath"
	"strings"
)

// matchesORTZipEntry reports whether a zip member is the ONNX Runtime Windows DLL, not the providers DLL beside it.
func matchesORTZipEntry(name string) bool {
	return strings.EqualFold(filepath.Base(name), "onnxruntime.dll")
}

// matchesORTTarEntry reports whether a tar member is the ONNX Runtime library. The archives ship it versioned
// (libonnxruntime.so.1.18.1, libonnxruntime.1.18.1.dylib) behind an unversioned symlink, and the macOS ones carry a
// debug-symbols copy of the same name inside a .dSYM bundle.
func matchesORTTarEntry(name string) bool {
	if strings.Contains(name, ".dSYM/") {
		return false
	}
	base := filepath.Base(name)
	return strings.HasPrefix(base, "libonnxruntime.so") || strings.HasPrefix(base, "libonnxruntime.") && strings.HasSuffix(base, ".dylib")
}
