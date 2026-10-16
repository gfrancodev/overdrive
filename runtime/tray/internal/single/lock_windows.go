//go:build windows

package single

import (
	"os"

	"golang.org/x/sys/windows"
)

// Acquire takes a per-session mutex so a second tray exits without starting a Core.
func Acquire(path string) (release func(), acquired bool, err error) {
	name, err := windows.UTF16PtrFromString("Local\\OverdriveTray")
	if err != nil {
		return nil, false, err
	}
	handle, err := windows.CreateMutex(nil, false, name)
	if err == windows.ERROR_ALREADY_EXISTS {
		if handle != 0 {
			_ = windows.CloseHandle(handle)
		}
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	_ = os.WriteFile(path, []byte("1"), 0o600)
	return func() { _ = windows.CloseHandle(handle) }, true, nil
}
