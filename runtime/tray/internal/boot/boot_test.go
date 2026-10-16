package boot

import (
	"context"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestEnsureSkipsRegisteredCore(t *testing.T) {
	var spawned int
	s := &Starter{
		Runtime:    "/usr/bin/true",
		ListenAddr: "127.0.0.1:7741",
		UIAddr:     "127.0.0.1:7742",
		Cwd:        t.TempDir(),
		PortOpen:   func(string) bool { return true },
		Probe:      func(context.Context) bool { t.Fatal("probed an open port"); return false },
		Spawn: func([]string, []string) error {
			spawned++
			return nil
		},
	}
	started, err := s.Ensure(context.Background())
	if err != nil || started || spawned != 0 {
		t.Fatalf("started=%v err=%v spawned=%d", started, err, spawned)
	}

	s.PortOpen = func(string) bool { return false }
	s.Probe = func(context.Context) bool { return true }
	started, err = s.Ensure(context.Background())
	if err != nil || started || spawned != 0 {
		t.Fatalf("probe started=%v err=%v spawned=%d", started, err, spawned)
	}
}

func TestEnsureSpawnsOnce(t *testing.T) {
	var gotArgv []string
	var gotEnv []string
	s := &Starter{
		Runtime:    "/opt/overdrive-runtime",
		ListenAddr: "127.0.0.1:7741",
		UIAddr:     "127.0.0.1:7742",
		Cwd:        "/home/user",
		PortOpen:   func(string) bool { return false },
		Probe:      func(context.Context) bool { return false },
		Spawn: func(argv []string, env []string) error {
			gotArgv = append([]string(nil), argv...)
			gotEnv = append([]string(nil), env...)
			return nil
		},
	}
	started, err := s.Ensure(context.Background())
	if err != nil || !started {
		t.Fatal(err, started)
	}
	want := []string{"/opt/overdrive-runtime", "share", "listen", "--cwd", "/home/user"}
	if strings.Join(gotArgv, " ") != strings.Join(want, " ") {
		t.Fatalf("argv %v", gotArgv)
	}
	if strings.Join(gotEnv, "\n") != "OVERDRIVE_SHARE_LISTEN=127.0.0.1:7741\nOVERDRIVE_SHARE_UI=127.0.0.1:7742" {
		t.Fatalf("env %v", gotEnv)
	}
	started, err = s.Ensure(context.Background())
	if err != nil || started {
		t.Fatalf("second start %v %v", started, err)
	}
}

func TestUIDisabledDoesNotSpawn(t *testing.T) {
	s := &Starter{UIOff: true, Spawn: func([]string, []string) error { t.Fatal("spawned"); return nil }}
	if _, err := s.Ensure(context.Background()); err != ErrUIDisabled {
		t.Fatal(err)
	}
	t.Setenv("OVERDRIVE_SHARE_UI", "off")
	s.UIOff = false
	if _, err := s.Ensure(context.Background()); err != ErrUIDisabled {
		t.Fatal(err)
	}
}

func TestPortsFromBase(t *testing.T) {
	listen, ui, err := PortsFromBase("http://127.0.0.1:7742")
	if err != nil || listen != "127.0.0.1:7741" || ui != "127.0.0.1:7742" {
		t.Fatalf("%s %s %v", listen, ui, err)
	}
}

func TestDialOpen(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	if !DialOpen(ln.Addr().String()) {
		t.Fatal("expected open")
	}
	if DialOpen("127.0.0.1:1") {
		t.Fatal("expected closed")
	}
}

func TestResolveRuntimeOrder(t *testing.T) {
	dir := t.TempDir()
	bin := filepath.Join(dir, "overdrive-runtime")
	if err := os.WriteFile(bin, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("OVERDRIVE_BIN_DIR", dir)
	t.Setenv("OVERDRIVE_HOME", filepath.Join(dir, "missing-home"))
	got, err := ResolveRuntime()
	if err != nil {
		t.Fatal(err)
	}
	if got != bin && filepath.Base(got) != "overdrive-runtime" {
		t.Fatal(got)
	}
	// Prefer the directory we just created when the executable's sibling is absent.
	if !strings.Contains(got, dir) && got != bin {
		t.Fatal(got)
	}
}

func TestSpawnTrue(t *testing.T) {
	if _, err := os.Stat("/bin/true"); err != nil {
		t.Skip(err)
	}
	if err := Spawn([]string{"/bin/true"}, nil); err != nil {
		t.Fatal(err)
	}
	time.Sleep(20 * time.Millisecond)
}
