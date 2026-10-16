package main

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestShareAssetPathAndServe(t *testing.T) {
	home := t.TempDir()
	assets := filepath.Join(home, "share", "assets")
	if err := os.MkdirAll(assets, 0o700); err != nil {
		t.Fatal(err)
	}
	png := []byte{0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a}
	if err := os.WriteFile(filepath.Join(assets, "icon-32.png"), png, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, ok := shareAssetDiskPath(home, "/assets/../secret"); ok {
		t.Fatal("traversal")
	}
	disk, ok := shareAssetDiskPath(home, "/assets/icon-32.png")
	if !ok || disk == "" {
		t.Fatalf("disk path %q %v", disk, ok)
	}

	req := httptest.NewRequest(http.MethodGet, "/assets/icon-32.png", nil)
	rec := httptest.NewRecorder()
	serveShareAssets(home)(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d", rec.Code)
	}

	cfgRaw := []byte(`{"logo":"/assets/icon-32.png","favicon":"/assets/icon-32.png","appleTouchIcon":"/assets/icon-32.png","themeColor":"#6d28d9"}`)
	if err := os.WriteFile(filepath.Join(home, "share", "ui.json"), cfgRaw, 0o600); err != nil {
		t.Fatal(err)
	}
	req = httptest.NewRequest(http.MethodGet, "/logo.png", nil)
	rec = httptest.NewRecorder()
	serveDashboardBrandImage(home, func(c shareUIConfig) string { return c.Logo })(rec, req)
	if rec.Code != http.StatusOK || rec.Body.Len() != len(png) {
		t.Fatalf("logo %d body %d", rec.Code, rec.Body.Len())
	}
}
