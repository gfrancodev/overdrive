package main

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestMeshIceServersSTUNOnlyAndTURN(t *testing.T) {
	t.Setenv("OVERDRIVE_TURN_URLS", "")
	t.Setenv("OVERDRIVE_TURN_USERNAME", "")
	t.Setenv("OVERDRIVE_TURN_CREDENTIAL", "")
	if turnConfigured() {
		t.Fatal("turn should be off")
	}
	stun := meshIceServers()
	if len(stun) != 1 || stun[0].URLs != defaultSTUNURL {
		t.Fatalf("stun %+v", stun)
	}
	script := callRuntimeScript()
	if !strings.Contains(script, "window.OVERDRIVE_CALL=") || !strings.Contains(script, defaultSTUNURL) || !strings.Contains(script, `"turnConfigured":false`) {
		t.Fatal(script)
	}

	t.Setenv("OVERDRIVE_TURN_URLS", " turn:example:3478?transport=udp , turns:example:5349 ")
	t.Setenv("OVERDRIVE_TURN_USERNAME", "circle")
	t.Setenv("OVERDRIVE_TURN_CREDENTIAL", "secret")
	if !turnConfigured() {
		t.Fatal("turn should be on")
	}
	servers := meshIceServers()
	if len(servers) != 2 || servers[1].Username != "circle" || servers[1].Credential != "secret" {
		t.Fatalf("turn %+v", servers)
	}
	urls, ok := servers[1].URLs.([]string)
	if !ok || len(urls) != 2 {
		t.Fatalf("urls %#v", servers[1].URLs)
	}
}

func TestTwoPeerDashboardsExposeCallSurface(t *testing.T) {
	t.Setenv("OVERDRIVE_TURN_URLS", "")
	homeA := t.TempDir()
	homeB := t.TempDir()
	setupTestEnv(t, homeA)
	srvA := httptest.NewServer(shareDashboardHandler(homeA))
	srvB := httptest.NewServer(shareDashboardHandler(homeB))
	t.Cleanup(srvA.Close)
	t.Cleanup(srvB.Close)

	for _, base := range []string{srvA.URL, srvB.URL} {
		res, err := http.Get(base + "/")
		if err != nil {
			t.Fatal(err)
		}
		body, err := io.ReadAll(res.Body)
		res.Body.Close()
		if err != nil {
			t.Fatal(err)
		}
		if res.StatusCode != http.StatusOK {
			t.Fatalf("%s status %d", base, res.StatusCode)
		}
		page := string(body)
		for _, needle := range []string{
			`id="callPanel"`,
			`id="callNatHint"`,
			`window.OVERDRIVE_CALL=`,
			defaultSTUNURL,
			"Safari and Firefox",
			"OVERDRIVE_TURN_URLS",
		} {
			if !strings.Contains(page, needle) {
				t.Fatalf("%s missing %q", base, needle)
			}
		}
	}
}
