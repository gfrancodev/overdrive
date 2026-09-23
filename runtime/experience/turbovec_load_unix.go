//go:build !windows

package main

import "github.com/ebitengine/purego"

func openDynamicLib(path string) (uintptr, error) {
	if hookOpenDynamicLib != nil {
		return hookOpenDynamicLib(path)
	}
	return purego.Dlopen(path, purego.RTLD_NOW|purego.RTLD_GLOBAL)
}
