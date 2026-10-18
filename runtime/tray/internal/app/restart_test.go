package app

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gfrancodev/overdrive/runtime/tray/internal/boot"
	"github.com/gfrancodev/overdrive/runtime/tray/internal/coreclient"
	"github.com/gfrancodev/overdrive/runtime/tray/internal/menu"
)

// fakeCore answers the admin API until it is stopped, and comes back when the starter spawns it.
type fakeCore struct {
	up       atomic.Bool
	stops    atomic.Int32
	spawned  atomic.Int32
	stopFail bool
	stuck    bool
}

func (f *fakeCore) app(t *testing.T) *App {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != coreclient.PathStop || r.Method != http.MethodPost {
			http.NotFound(w, r)
			return
		}
		f.stops.Add(1)
		if f.stopFail {
			http.Error(w, "no", http.StatusInternalServerError)
			return
		}
		if !f.stuck {
			f.up.Store(false)
		}
		_, _ = io.WriteString(w, `{"stopped":true}`)
	}))
	t.Cleanup(srv.Close)
	client, err := coreclient.New(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	prev := restartWait
	restartWait = 400 * time.Millisecond
	t.Cleanup(func() { restartWait = prev })
	return &App{
		Client: client,
		Starter: &boot.Starter{
			Runtime:  "/opt/overdrive-runtime",
			PortOpen: func(string) bool { return f.up.Load() },
			Probe:    func(context.Context) bool { return f.up.Load() },
			Spawn: func(argv []string, _ []string) error {
				if len(argv) < 3 || argv[0] != "/opt/overdrive-runtime" || argv[1] != "share" || argv[2] != "listen" {
					t.Fatalf("spawned %v", argv)
				}
				f.spawned.Add(1)
				f.up.Store(true)
				return nil
			},
		},
	}
}

func TestRestartStopsTheCoreAndStartsItAgain(t *testing.T) {
	core := &fakeCore{}
	application := core.app(t)
	ctx := context.Background()
	// The tray started this Core a moment ago, so the guard against a second spawn is still armed.
	if err := application.Activate(ctx, menu.ActionStart); err != nil {
		t.Fatal(err)
	}
	if err := application.Activate(ctx, menu.ActionRestart); err != nil {
		t.Fatal(err)
	}
	if core.stops.Load() != 1 || core.spawned.Load() != 2 || !core.up.Load() {
		t.Fatalf("stops %d spawned %d up %v", core.stops.Load(), core.spawned.Load(), core.up.Load())
	}
}

func TestRestartDoesNotStartASecondCoreBesideOneThatWontStop(t *testing.T) {
	core := &fakeCore{stuck: true}
	core.up.Store(true)
	application := core.app(t)
	err := application.Activate(context.Background(), menu.ActionRestart)
	if err == nil || !strings.Contains(err.Error(), "did not stop") {
		t.Fatalf("err %v", err)
	}
	if core.spawned.Load() != 0 {
		t.Fatalf("spawned %d beside a running Core", core.spawned.Load())
	}
}

func TestRestartReportsAStopTheCoreRefused(t *testing.T) {
	core := &fakeCore{stopFail: true}
	core.up.Store(true)
	application := core.app(t)
	if err := application.Activate(context.Background(), menu.ActionRestart); err == nil {
		t.Fatal("a refused stop passed for a restart")
	}
	if core.spawned.Load() != 0 {
		t.Fatalf("spawned %d", core.spawned.Load())
	}
}

func TestRestartNeedsAStarter(t *testing.T) {
	if err := (&App{}).Activate(context.Background(), menu.ActionRestart); err == nil {
		t.Fatal("restart without a starter")
	}
}
