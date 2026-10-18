// Package boot starts the Core only when nothing is already registered on its ports.
package boot

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	DefaultListen = "127.0.0.1:7741"
	DefaultUI     = "127.0.0.1:7742"
)

var ErrUIDisabled = errors.New("share ui is off")

// ErrStillRunning is a Core that kept its ports after it was asked to stop.
var ErrStillRunning = errors.New("the Core did not stop")

// awaitDownEvery is how often AwaitDown checks whether the Core let go of its ports.
const awaitDownEvery = 100 * time.Millisecond

// Starter launches `overdrive-runtime share listen` when the loopback Core is down.
type Starter struct {
	Runtime    string
	ListenAddr string
	UIAddr     string
	Cwd        string
	UIOff      bool
	Probe      func(ctx context.Context) bool
	PortOpen   func(addr string) bool
	Spawn      func(argv []string, env []string) error

	mu        sync.Mutex
	lastSpawn time.Time
}

// Ensure returns true only when it spawned a Core. An occupied listen port or a
// live admin API counts as already registered.
func (s *Starter) Ensure(ctx context.Context) (bool, error) {
	if s == nil {
		return false, errors.New("missing starter")
	}
	if s.disabled() {
		return false, ErrUIDisabled
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.registered(ctx) {
		return false, nil
	}
	if !s.lastSpawn.IsZero() && time.Since(s.lastSpawn) < 5*time.Second {
		return false, nil
	}
	runtime := s.Runtime
	if runtime == "" {
		found, err := ResolveRuntime()
		if err != nil {
			return false, err
		}
		runtime = found
	}
	argv, env := ListenCommand(runtime, s.cwd(), s.listen(), s.ui())
	spawn := s.Spawn
	if spawn == nil {
		spawn = Spawn
	}
	if err := spawn(argv, env); err != nil {
		return false, err
	}
	s.lastSpawn = time.Now()
	return true, nil
}

// AwaitDown waits until nothing serves the Core's ports, for a restart. Once the Core is down the guard against a
// second spawn within seconds no longer applies: it covers a Core still binding its ports, not one that exited.
func (s *Starter) AwaitDown(ctx context.Context, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for {
		s.mu.Lock()
		up := s.registered(ctx)
		if !up {
			s.lastSpawn = time.Time{}
		}
		s.mu.Unlock()
		if !up {
			return nil
		}
		if time.Now().After(deadline) {
			return ErrStillRunning
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(awaitDownEvery):
		}
	}
}

func (s *Starter) disabled() bool {
	if s.UIOff {
		return true
	}
	v := strings.TrimSpace(os.Getenv("OVERDRIVE_SHARE_UI"))
	return v == "off" || v == "0"
}

func (s *Starter) registered(ctx context.Context) bool {
	if s.PortOpen != nil && s.PortOpen(s.listen()) {
		return true
	}
	if s.Probe != nil && s.Probe(ctx) {
		return true
	}
	return false
}

func (s *Starter) listen() string {
	if s.ListenAddr != "" {
		return s.ListenAddr
	}
	return DefaultListen
}

func (s *Starter) ui() string {
	if s.UIAddr != "" {
		return s.UIAddr
	}
	return DefaultUI
}

func (s *Starter) cwd() string {
	if s.Cwd != "" {
		return s.Cwd
	}
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		return home
	}
	return "."
}

// ListenCommand is the same process the session hook starts, bound to loopback.
func ListenCommand(runtime, cwd, listen, ui string) (argv []string, env []string) {
	argv = []string{runtime, "share", "listen", "--cwd", cwd}
	env = []string{
		"OVERDRIVE_SHARE_LISTEN=" + listen,
		"OVERDRIVE_SHARE_UI=" + ui,
	}
	return argv, env
}

// PortsFromBase maps an admin URL onto the listen port (admin port - 1) and the UI host:port.
func PortsFromBase(base string) (listenAddr, uiAddr string, err error) {
	host, port, err := splitHostPort(base)
	if err != nil {
		return "", "", err
	}
	n, err := strconv.Atoi(port)
	if err != nil || n <= 1 || n > 65535 {
		return "", "", fmt.Errorf("core url is not loopback")
	}
	return net.JoinHostPort(host, strconv.Itoa(n-1)), net.JoinHostPort(host, port), nil
}

func splitHostPort(base string) (string, string, error) {
	raw := strings.TrimSpace(base)
	raw = strings.TrimPrefix(raw, "https://")
	raw = strings.TrimPrefix(raw, "http://")
	raw = strings.TrimRight(raw, "/")
	if !strings.Contains(raw, ":") {
		return "", "", fmt.Errorf("core url is not loopback")
	}
	host, port, err := net.SplitHostPort(raw)
	if err != nil {
		return "", "", err
	}
	return host, port, nil
}

// ResolveRuntime finds the Core binary next to the tray, then OVERDRIVE_BIN_DIR, then the user install, then PATH.
func ResolveRuntime() (string, error) {
	name := RuntimeName()
	var candidates []string
	if exe, err := os.Executable(); err == nil {
		candidates = append(candidates, filepath.Join(filepath.Dir(exe), name))
	}
	if dir := strings.TrimSpace(os.Getenv("OVERDRIVE_BIN_DIR")); dir != "" {
		candidates = append(candidates, filepath.Join(dir, name))
	}
	home := strings.TrimSpace(os.Getenv("OVERDRIVE_HOME"))
	if home == "" {
		if user, err := os.UserHomeDir(); err == nil {
			home = filepath.Join(user, ".overdrive")
		}
	}
	if home != "" {
		candidates = append(candidates, filepath.Join(home, "bin", name))
	}
	for _, path := range candidates {
		if executable(path) {
			return path, nil
		}
	}
	if path, err := exec.LookPath(name); err == nil {
		return path, nil
	}
	return "", errors.New("overdrive runtime not found")
}

func RuntimeName() string {
	if os.PathSeparator == '\\' {
		return "overdrive-runtime.exe"
	}
	return "overdrive-runtime"
}

func executable(path string) bool {
	info, err := os.Stat(path)
	if err != nil || info.IsDir() {
		return false
	}
	return info.Mode()&0o111 != 0 || strings.HasSuffix(strings.ToLower(path), ".exe")
}

// DialOpen reports whether a TCP accept is already registered on addr.
func DialOpen(addr string) bool {
	conn, err := net.DialTimeout("tcp", addr, 300*time.Millisecond)
	if err != nil {
		return false
	}
	_ = conn.Close()
	return true
}
