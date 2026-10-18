package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
)

// The dashboard must work with the WAN off: every script, style and module it needs comes from the Core.
func TestDashboardLoadsNothingFromTheInternet(t *testing.T) {
	html := string(dashboardHTML)
	m := regexp.MustCompile(`(?s)<script type="importmap">(.*?)</script>`).FindStringSubmatch(html)
	if m == nil {
		t.Fatal("dashboard has no import map")
	}
	var importMap struct {
		Imports map[string]string `json:"imports"`
	}
	if err := json.Unmarshal([]byte(m[1]), &importMap); err != nil {
		t.Fatalf("import map: %v", err)
	}
	if importMap.Imports["three"] != dashboardThreePath {
		t.Fatalf("three resolves to %q, want %q", importMap.Imports["three"], dashboardThreePath)
	}
	for name, target := range importMap.Imports {
		if !strings.HasPrefix(target, "/") {
			t.Fatalf("module %s loads from %s", name, target)
		}
	}
	external := []*regexp.Regexp{
		regexp.MustCompile(`<script[^>]+src=["']?(https?:)?//`),
		regexp.MustCompile(`<link[^>]+href=["']?(https?:)?//[^>]*rel=["']?(stylesheet|modulepreload|preload)`),
		regexp.MustCompile(`<link[^>]+rel=["']?(stylesheet|modulepreload|preload)[^>]*href=["']?(https?:)?//`),
		regexp.MustCompile(`@import\s+(url\()?["']?(https?:)?//`),
		regexp.MustCompile(`@font-face[^}]*url\(["']?(https?:)?//`),
		regexp.MustCompile(`import\(\s*["'](https?:)?//`),
		regexp.MustCompile(`from\s+["'](https?:)?//`),
	}
	for _, re := range external {
		if loc := re.FindStringIndex(html); loc != nil {
			t.Fatalf("dashboard loads from the internet: %q", html[loc[0]:min(loc[1]+80, len(html))])
		}
	}
}

func TestCoreServesThreeForTheGlobe(t *testing.T) {
	home := t.TempDir()
	h := shareDashboardHandler(home)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, dashboardThreePath, nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("status %d", rr.Code)
	}
	if ct := rr.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/javascript") {
		t.Fatalf("content type %q", ct)
	}
	if cc := rr.Header().Get("Cache-Control"); !strings.Contains(cc, "immutable") {
		t.Fatalf("a versioned module should be cached for good: %q", cc)
	}
	body := rr.Body.String()
	if !strings.Contains(body[:min(len(body), 200)], "SPDX-License-Identifier: MIT") || !strings.Contains(body, `const t="170"`) {
		t.Fatalf("not three r170: %.200s", body)
	}

	rr = httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest(http.MethodPost, dashboardThreePath, nil))
	if rr.Code != http.StatusMethodNotAllowed {
		t.Fatalf("POST status %d", rr.Code)
	}
}
