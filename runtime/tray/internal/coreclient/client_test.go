package coreclient

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestDefaultAdminURL(t *testing.T) {
	t.Setenv("OVERDRIVE_TRAY_CORE_URL", "")
	t.Setenv("OVERDRIVE_SHARE_UI", "")
	if DefaultBaseURL() != DefaultAdminURL {
		t.Fatalf("default %s", DefaultBaseURL())
	}
	t.Setenv("OVERDRIVE_SHARE_UI", "127.0.0.1:7750")
	if DefaultBaseURL() != "http://127.0.0.1:7750" {
		t.Fatalf("share ui %s", DefaultBaseURL())
	}
	t.Setenv("OVERDRIVE_TRAY_CORE_URL", "http://127.0.0.1:7742")
	if DefaultBaseURL() != "http://127.0.0.1:7742" {
		t.Fatalf("tray url %s", DefaultBaseURL())
	}
}

func TestRejectsNonLoopbackBeforeDial(t *testing.T) {
	c, err := New("http://10.1.1.1:7742")
	if !errors.Is(err, ErrNotLoopback) || c != nil {
		t.Fatalf("non-loopback err=%v client=%v", err, c)
	}
	if _, err := ValidateUIURL("http://user:secret@127.0.0.1:7742/"); !errors.Is(err, ErrNotLoopback) {
		t.Fatal(err)
	}
	if _, err := ValidateUIURL("file:///tmp/ui"); !errors.Is(err, ErrNotLoopback) {
		t.Fatal(err)
	}
}

func TestPauseResumeStopAndUI(t *testing.T) {
	secret := "token=supersecret"
	var hits []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits = append(hits, r.Method+" "+r.URL.Path)
		if r.URL.Path == "/api/core/stop" {
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{"mesh":"running","memory":"checkpointed","stopped":true}`)
			return
		}
		if r.URL.Path == "/api/mesh/pause" {
			_, _ = io.WriteString(w, `{"mesh":"paused","sync":false,"url":"http://127.0.0.1:7742/"}`)
			return
		}
		if r.URL.Path == "/api/mesh/resume" {
			_, _ = io.WriteString(w, `{"mesh":"running","sync":true,"url":"http://127.0.0.1:7742/"}`)
			return
		}
		if r.URL.Path == "/api/core/ui" {
			_, _ = io.WriteString(w, `{"url":"`+srvURLPlaceholder(r)+`","mesh":"running","sync":true}`)
			return
		}
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = io.WriteString(w, secret)
	}))
	defer srv.Close()

	c, err := New(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	ui, err := c.UI(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if ui.URL != srv.URL+"/" && ui.URL != srv.URL {
		// The handler writes the request host, which is the loopback server.
		if !strings.HasPrefix(ui.URL, "http://127.0.0.1:") && !strings.HasPrefix(ui.URL, "http://[::1]:") {
			t.Fatalf("ui url %q", ui.URL)
		}
	}
	if _, err := ValidateUIURL(ui.URL); err != nil {
		t.Fatal(err)
	}
	paused, err := c.Pause(ctx)
	if err != nil || paused.Mesh != "paused" {
		t.Fatalf("pause %+v %v", paused, err)
	}
	resumed, err := c.Resume(ctx)
	if err != nil || resumed.Mesh != "running" {
		t.Fatalf("resume %+v %v", resumed, err)
	}
	stopped, err := c.Stop(ctx)
	if err != nil || !stopped.Stopped {
		t.Fatalf("stop %+v %v", stopped, err)
	}
	want := []string{
		"GET /api/core/ui",
		"POST /api/mesh/pause",
		"POST /api/mesh/resume",
		"POST /api/core/stop",
	}
	if strings.Join(hits, ",") != strings.Join(want, ",") {
		t.Fatalf("hits %v", hits)
	}
}

func srvURLPlaceholder(r *http.Request) string {
	return "http://" + r.Host + "/"
}

func TestErrorOmitsResponseBody(t *testing.T) {
	const secret = "token=supersecret"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = io.WriteString(w, secret)
	}))
	defer srv.Close()
	c, err := New(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	_, err = c.Pause(context.Background())
	if err == nil {
		t.Fatal("expected status error")
	}
	if strings.Contains(err.Error(), secret) || strings.Contains(err.Error(), "supersecret") {
		t.Fatalf("error leaked body: %s", err)
	}
	var se *StatusError
	if !errors.As(err, &se) || se.Code != http.StatusForbidden || se.Path != PathPause {
		t.Fatalf("status error %#v", err)
	}
}

func TestPeersCountAndUnknown(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != PathPeers {
			http.NotFound(w, r)
			return
		}
		_, _ = io.WriteString(w, `{
			"device_id":"dev_self",
			"circles":[{"members":[
				{"device_id":"dev_self","self":true},
				{"device_id":"dev_a"},
				{"device_id":"dev_b","revoked":true},
				{"device_id":"dev_a"}
			]}],
			"links":[{"name":"10.0.0.8:7741","state":"timeout"}],
			"handoffs":["aberto: secret handoff summary"]
		}`)
	}))
	defer srv.Close()
	c, err := New(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	peers, err := c.Peers(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !peers.CountKnown || peers.Count != 1 {
		t.Fatalf("count %+v", peers)
	}
	if len(peers.Links) != 1 || peers.Links[0].State != "timeout" {
		t.Fatalf("links %+v", peers.Links)
	}
	if len(peers.Handoffs) != 1 {
		t.Fatalf("handoffs %+v", peers.Handoffs)
	}

	missing := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, `{"events":["no circles"]}`)
	}))
	defer missing.Close()
	c2, err := New(missing.URL)
	if err != nil {
		t.Fatal(err)
	}
	unknown, err := c2.Peers(context.Background())
	if err != nil || unknown.CountKnown {
		t.Fatalf("unknown %+v %v", unknown, err)
	}
}

func TestEmptyCirclesAreZeroPeers(t *testing.T) {
	got, err := decodePeers([]byte(`{"circles":[]}`))
	if err != nil || !got.CountKnown || got.Count != 0 {
		t.Fatalf("%+v %v", got, err)
	}
	endpoints, err := decodePeers([]byte(`{"circles":[{"peer_endpoints":["10.0.0.2:7741","10.0.0.2:7741","10.0.0.3:7741"]}]}`))
	if err != nil || endpoints.Count != 2 {
		t.Fatalf("%+v %v", endpoints, err)
	}
}

func TestUnreadFailClosed(t *testing.T) {
	const secret = "message body secret"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != PathConversations {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"conversations":[{"preview":"`+secret+`"}]}`)
	}))
	defer srv.Close()
	c, err := New(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	if n, known := c.Unread(context.Background(), nil); known || n != 0 {
		t.Fatalf("invented unread without mesh %d known=%v", n, known)
	}
	n, known := c.Unread(context.Background(), []string{"circle-1"})
	if known || n != 0 {
		t.Fatalf("invented unread %d known=%v", n, known)
	}

	down := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
		_, _ = io.WriteString(w, secret)
	}))
	defer down.Close()
	c2, err := New(down.URL)
	if err != nil {
		t.Fatal(err)
	}
	n, known = c2.Unread(context.Background(), []string{"circle-1"})
	if known || n != 0 || strings.Contains(c2.base, secret) {
		t.Fatalf("failed call unread %d known=%v", n, known)
	}
}

func TestUnreadSumsConversations(t *testing.T) {
	const secret = "secret message preview"
	var queries []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != PathConversations || r.Method != http.MethodGet {
			http.NotFound(w, r)
			return
		}
		queries = append(queries, r.URL.RawQuery)
		mesh := r.URL.Query().Get("mesh_id")
		switch mesh {
		case "circle-a":
			_, _ = io.WriteString(w, `{"conversations":[
				{"conversation_id":"direct:a:b","unread":2,"preview":"`+secret+`"},
				{"conversation_id":"project:demo","unread":0,"preview":"`+secret+`"}
			]}`)
		case "circle-b":
			_, _ = io.WriteString(w, `{"conversations":[{"conversation_id":"direct:a:c","unread":5,"preview":"`+secret+`"}]}`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	c, err := New(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	n, known := c.Unread(context.Background(), []string{"circle-a", "circle-a", " circle-b "})
	if !known || n != 7 {
		t.Fatalf("unread %d known=%v", n, known)
	}
	if strings.Join(queries, "&") != "mesh_id=circle-a&mesh_id=circle-b" && strings.Join(queries, " ") != "mesh_id=circle-a mesh_id=circle-b" {
		t.Fatalf("queries %v", queries)
	}
	peers, err := decodePeers([]byte(`{"circles":[{"id":"circle-a"},{"id":"circle-b"},{"id":"circle-a"}]}`))
	if err != nil || strings.Join(peers.MeshIDs, ",") != "circle-a,circle-b" {
		t.Fatalf("mesh ids %+v %v", peers.MeshIDs, err)
	}
	empty, ok := sumConversationUnread([]byte(`{"conversations":[]}`))
	if !ok || empty != 0 {
		t.Fatalf("empty %d %v", empty, ok)
	}
}

func TestRedirectIsRejected(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "http://10.1.1.1/secret", http.StatusFound)
	}))
	defer srv.Close()
	c, err := New(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	_, err = c.UI(context.Background())
	if !errors.Is(err, ErrUnreachable) {
		t.Fatal(err)
	}
	if strings.Contains(err.Error(), "10.1.1.1") || strings.Contains(err.Error(), "secret") {
		t.Fatalf("redirect leaked: %s", err)
	}
}
