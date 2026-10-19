//go:build windows

package main

import (
	"syscall"
	"unsafe"
)

// ortPathPtr encodes a path as onnxruntime's ORTCHAR_T, which is wchar_t on Windows: a UTF-8 path reaches it as a
// file name nothing has.
func ortPathPtr(path string) (unsafe.Pointer, error) {
	p, err := syscall.UTF16PtrFromString(path)
	return unsafe.Pointer(p), err
}
