package app

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gfrancodev/overdrive/runtime/tray/internal/boot"
	"github.com/gfrancodev/overdrive/runtime/tray/internal/coreclient"
	"github.com/gfrancodev/overdrive/runtime/tray/internal/menu"
	"github.com/gfrancodev/overdrive/runtime/tray/internal/signal"
)

type opener struct {
	urls []string
}

func (o *opener) Open(raw string) error {
	o.urls = append(o.urls, raw)
	return nil
}

func TestControlActionsUseCoreRoutes(t *testing.T) {
	const secret = "secret message body"
	var hits []string
	mux := http.NewServeMux()
	uiURL := ""
	srv := httptest.NewServer(mux)
	defer srv.Close()
	uiURL = srv.URL + "/"
	mux.HandleFunc("/api/core/ui", func(w http.ResponseWriter, r *http.Request) {
		hits = append(hits, r.Method+" "+r.URL.Path)
		_, _ = io.WriteString(w, `{"url":"`+uiURL+`","mesh":"running","sync":true}`)
	})
	mux.HandleFunc("/api/mesh/pause", func(w http.ResponseWriter, r *http.Request) {
		hits = append(hits, r.Method+" "+r.URL.Path)
		_, _ = io.WriteString(w, `{"mesh":"paused","url":"`+uiURL+`"}`)
	})
	mux.HandleFunc("/api/mesh/resume", func(w http.ResponseWriter, r *http.Request) {
		hits = append(hits, r.Method+" "+r.URL.Path)
		_, _ = io.WriteString(w, `{"mesh":"running","url":"`+uiURL+`"}`)
	})
	mux.HandleFunc("/api/core/stop", func(w http.ResponseWriter, r *http.Request) {
		hits = append(hits, r.Method+" "+r.URL.Path)
		_, _ = io.WriteString(w, `{"stopped":true,"memory":"checkpointed"}`)
	})
	mux.HandleFunc("/api/peers", func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, `{"circles":[],"handoffs":["aberto: `+secret+`"]}`)
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = io.WriteString(w, secret)
	})

	client, err := coreclient.New(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	open := &opener{}
	var spawned int
	application := &App{
		Client: client,
		Opener: open,
		Starter: &boot.Starter{
			Spawn: func([]string, []string) error {
				spawned++
				return nil
			},
		},
	}
	ctx := context.Background()
	view, notes, err := application.Refresh(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if view.StatusText != "Status: running" || view.Power.Action != menu.ActionStop {
		t.Fatalf("%+v", view)
	}
	if len(notes) != 0 {
		t.Fatalf("baseline notified %#v", notes)
	}
	if view.Messages.Enabled {
		t.Fatal("messages should stay disabled without a circle")
	}

	if err := application.Activate(ctx, menu.ActionOpen); err != nil {
		t.Fatal(err)
	}
	if err := application.Activate(ctx, menu.ActionPreferences); err != nil {
		t.Fatal(err)
	}
	if err := application.Activate(ctx, menu.ActionPause); err != nil {
		t.Fatal(err)
	}
	if err := application.Activate(ctx, menu.ActionResume); err != nil {
		t.Fatal(err)
	}
	if err := application.Activate(ctx, menu.ActionStop); err != nil {
		t.Fatal(err)
	}
	if spawned != 0 {
		t.Fatal("stop started a core")
	}
	if len(open.urls) != 2 || open.urls[0] != uiURL || open.urls[1] != uiURL {
		t.Fatalf("opened %#v", open.urls)
	}
	for _, hit := range hits {
		if strings.Contains(hit, secret) {
			t.Fatal(hit)
		}
	}
	want := []string{
		"GET /api/core/ui",
		"GET /api/core/ui",
		"GET /api/core/ui",
		"POST /api/mesh/pause",
		"POST /api/mesh/resume",
		"POST /api/core/stop",
	}
	if strings.Join(hits, ",") != strings.Join(want, ",") {
		t.Fatalf("hits %v", hits)
	}
}

func TestDisconnectedCanStartWithoutASecondCore(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "down", http.StatusBadGateway)
	}))
	// Close immediately so the client is unreachable.
	srv.Close()
	client, err := coreclient.New(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	var spawned int
	application := &App{
		Client: client,
		Starter: &boot.Starter{
			Runtime:    "/opt/overdrive-runtime",
			ListenAddr: "127.0.0.1:1",
			UIAddr:     "127.0.0.1:2",
			Cwd:        t.TempDir(),
			PortOpen:   func(string) bool { return false },
			Probe:      func(context.Context) bool { return false },
			Spawn: func(argv []string, env []string) error {
				spawned++
				if strings.Contains(strings.Join(argv, " "), "secret") {
					t.Fatal(argv)
				}
				return nil
			},
		},
	}
	view, _, err := application.Refresh(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if view.Status != menu.Disconnected || view.Power.Action != menu.ActionStart {
		t.Fatalf("%+v", view)
	}
	if err := application.Activate(context.Background(), menu.ActionStart); err != nil {
		t.Fatal(err)
	}
	if spawned != 1 {
		t.Fatalf("spawned %d", spawned)
	}
	application.Starter.PortOpen = func(string) bool { return true }
	if err := application.Activate(context.Background(), menu.ActionStart); err != nil {
		t.Fatal(err)
	}
	if spawned != 1 {
		t.Fatalf("second core spawned %d", spawned)
	}
}

func TestMessagesUsesConversationUnread(t *testing.T) {
	const secret = "secret message preview"
	uiURL := ""
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case coreclient.PathUI:
			_, _ = io.WriteString(w, `{"url":"`+uiURL+`","mesh":"running"}`)
		case coreclient.PathPeers:
			_, _ = io.WriteString(w, `{"circles":[{"id":"circle-1","members":[{"device_id":"dev_self","self":true},{"device_id":"dev_peer"}]}]}`)
		case coreclient.PathConversations:
			if r.URL.Query().Get("mesh_id") != "circle-1" {
				http.NotFound(w, r)
				return
			}
			_, _ = io.WriteString(w, `{"conversations":[
				{"conversation_id":"direct:a:b","unread":2,"preview":"`+secret+`"},
				{"conversation_id":"project:demo","unread":5,"preview":"`+secret+`"}
			]}`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	uiURL = srv.URL + "/"
	client, err := coreclient.New(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	view, _, err := (&App{Client: client}).Refresh(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !view.Messages.Enabled || view.Messages.Title != "Messages: 7" || view.Messages.Action != menu.ActionMessages {
		t.Fatalf("%+v", view.Messages)
	}
	if strings.Contains(view.Messages.Title, secret) || strings.Contains(view.Messages.Tooltip, secret) {
		t.Fatalf("menu leaked preview: %+v", view.Messages)
	}
}

func TestNoticeTextFromRealTransition(t *testing.T) {
	secret := "secret handoff summary"
	var mode string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case coreclient.PathUI:
			_, _ = io.WriteString(w, `{"url":"`+srvURL(r)+`","mesh":"running"}`)
		case coreclient.PathPeers:
			if mode == "next" {
				_, _ = io.WriteString(w, `{"circles":[],"links":[{"name":"peer","state":"timeout"}],"handoffs":["aberto: `+secret+`"]}`)
				return
			}
			_, _ = io.WriteString(w, `{"circles":[],"links":[{"name":"peer","state":"ok"}]}`)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()
	client, err := coreclient.New(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	application := &App{Client: client}
	if _, notes, err := application.Refresh(context.Background()); err != nil || len(notes) != 0 {
		t.Fatal(err, notes)
	}
	mode = "next"
	_, notes, err := application.Refresh(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(notes) != 2 {
		t.Fatalf("%#v", notes)
	}
	for _, note := range notes {
		title, body := signal.PublicText(note.Kind)
		if strings.Contains(title+body, secret) || strings.Contains(body, "peer") {
			t.Fatalf("leaked %s %s", title, body)
		}
	}
}

func srvURL(r *http.Request) string {
	return "http://" + r.Host + "/"
}
