package main

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestShareUIRemoteImageCache(t *testing.T) {
	png := []byte{0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a, 0x00, 0x00}
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "image/png")
		_, _ = w.Write(png)
	}))
	defer srv.Close()
	tlsClient := srv.Client()

	home := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, "share"), 0o700); err != nil {
		t.Fatal(err)
	}
	raw := []byte(`{"logo":"` + srv.URL + `/icon-32.png","favicon":"` + srv.URL + `/icon-32.png","themeColor":"#6d28d9"}`)
	if err := os.WriteFile(filepath.Join(home, "share", shareWhitelabelFile), raw, 0o600); err != nil {
		t.Fatal(err)
	}
	shareUIRemoteHTTPGet = func(_ *http.Client, url string) (*http.Response, error) {
		return tlsClient.Get(url)
	}

	cfg := loadShareUI(home)
	if !strings.HasPrefix(cfg.Logo, "/assets/remote-") || cfg.Favicon != cfg.Logo {
		t.Fatalf("config %+v", cfg)
	}
	cfg2 := loadShareUI(home)
	if cfg2.Logo != cfg.Logo {
		t.Fatalf("cache miss: %s vs %s", cfg2.Logo, cfg.Logo)
	}
}

func TestShareUIRemoteRejectsHTTPAndTraversal(t *testing.T) {
	if _, ok := shareUIAllowedRemoteURL("http://example.com/x.png"); ok {
		t.Fatal("http")
	}
	if _, ok := shareUIAllowedRemoteURL("ftp://example.com/x.png"); ok {
		t.Fatal("ftp")
	}
	if shareUIImageRef("https://user:pass@example.com/x.png") != "" {
		t.Fatal("credentials")
	}
	if shareUIImageRef("/assets/../secret") != "" {
		t.Fatal("traversal")
	}
}
