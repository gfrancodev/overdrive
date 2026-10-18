//go:build linux

package boot

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// zombieChildren lists this process's children that exited and were never waited for.
func zombieChildren(t *testing.T) []string {
	t.Helper()
	stats, _ := filepath.Glob("/proc/[0-9]*/stat")
	self := strconv.Itoa(os.Getpid())
	var out []string
	for _, path := range stats {
		raw, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		// pid (comm) state ppid ...: comm may hold spaces, so read after its closing parenthesis.
		fields := strings.Fields(string(raw[strings.LastIndexByte(string(raw), ')')+1:]))
		if len(fields) > 1 && fields[0] == "Z" && fields[1] == self {
			out = append(out, path)
		}
	}
	return out
}

// The tray is the parent of every Core it starts. A Core stopped from the menu stayed a zombie, one per stop,
// until the tray exited.
func TestASpawnedCoreThatExitsIsReaped(t *testing.T) {
	if _, err := os.Stat("/bin/true"); err != nil {
		t.Skip(err)
	}
	if err := Spawn([]string{"/bin/true"}, nil); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(3 * time.Second)
	for {
		left := zombieChildren(t)
		if len(left) == 0 {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("exited child never reaped: %v", left)
		}
		time.Sleep(20 * time.Millisecond)
	}
}
