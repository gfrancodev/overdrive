package whitelabel

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/gfrancodev/overdrive/runtime/tray/internal/icon"
)

func TestTrayPNGFromAssets(t *testing.T) {
	home := t.TempDir()
	assets := filepath.Join(home, "share", "assets")
	if err := os.MkdirAll(assets, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(assets, "mark.png"), icon.PNG(), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := []byte(`{"logo":"/assets/mark.png"}`)
	if err := os.WriteFile(filepath.Join(home, "share", whitelabelFile), cfg, 0o600); err != nil {
		t.Fatal(err)
	}
	out, ok := TrayPNG(home)
	if !ok || len(out) < 8 {
		t.Fatalf("png %v len %d", ok, len(out))
	}
}

func TestTrayPNGDefaultWhenUnset(t *testing.T) {
	if _, ok := TrayPNG(t.TempDir()); ok {
		t.Fatal("expected no custom icon")
	}
}
