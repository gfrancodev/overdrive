//go:build !windows

package single

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestSecondProcessDoesNotAcquire(t *testing.T) {
	if os.Getenv("OVERDRIVE_TRAY_LOCK_CHILD") == "1" {
		_, ok, err := Acquire(os.Getenv("OVERDRIVE_TRAY_LOCK_PATH"))
		if err != nil || ok {
			os.Exit(2)
		}
		os.Exit(0)
	}
	path := filepath.Join(t.TempDir(), "tray.lock")
	release, ok, err := Acquire(path)
	if err != nil || !ok {
		t.Fatal(err, ok)
	}
	defer release()
	cmd := exec.Command(os.Args[0], "-test.run=TestSecondProcessDoesNotAcquire", "-test.v")
	cmd.Env = append(os.Environ(), "OVERDRIVE_TRAY_LOCK_CHILD=1", "OVERDRIVE_TRAY_LOCK_PATH="+path)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("child acquired the lock: %v\n%s", err, out)
	}
}
