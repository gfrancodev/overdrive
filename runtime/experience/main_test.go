package main

import (
	"os"
	"sync"
	"testing"
)

// TestMain lets this test binary stand in for the runtime when a test starts a terminal: the terminal manager
// runs os.Executable() as the session host, and in a test that is this binary. Without it the binary ignored
// "session-host" and ran the whole suite again in the background, and since a Core keeps live hosts nothing
// stopped those copies. The hosts the tests started are stopped once the suite ends.
func TestMain(m *testing.M) {
	if len(os.Args) > 1 && os.Args[1] == "session-host" {
		os.Exit(runCLI(os.Args[1:]))
	}
	var mu sync.Mutex
	var started []int
	hookSessionHostStarted = func(pid int) {
		mu.Lock()
		started = append(started, pid)
		mu.Unlock()
	}
	code := m.Run()
	mu.Lock()
	for _, pid := range started {
		if processAlive(pid) {
			terminateProcess(pid)
		}
	}
	mu.Unlock()
	os.Exit(code)
}
