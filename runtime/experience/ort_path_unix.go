//go:build !windows

package main

import (
	"syscall"
	"unsafe"
)

// ortPathPtr encodes a path as onnxruntime's ORTCHAR_T, which is char outside Windows.
func ortPathPtr(path string) (unsafe.Pointer, error) {
	p, err := syscall.BytePtrFromString(path)
	return unsafe.Pointer(p), err
}
