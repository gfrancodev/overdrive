//go:build windows

package main

import "golang.org/x/sys/windows"

func openDynamicLib(path string) (uintptr, error) {
	if hookOpenDynamicLib != nil {
		return hookOpenDynamicLib(path)
	}
	handle, err := windows.LoadLibrary(path)
	return uintptr(handle), err
}
