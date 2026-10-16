package whitelabel

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/gfrancodev/overdrive/runtime/tray/internal/icon"
)

// TrayPNG returns a 32×32 tray icon from whitelabel logo when configured.
func TrayPNG(home string) ([]byte, bool) {
	path, ok := logoDiskPath(home)
	if !ok {
		return nil, false
	}
	png, _, err := icon.FromPath(path)
	if err != nil || len(png) == 0 {
		return nil, false
	}
	return png, true
}

// TrayICO returns a Windows tray icon from whitelabel logo when configured.
func TrayICO(home string) ([]byte, bool) {
	path, ok := logoDiskPath(home)
	if !ok {
		return nil, false
	}
	_, ico, err := icon.FromPath(path)
	if err != nil || len(ico) == 0 {
		return nil, false
	}
	return ico, true
}

func logoDiskPath(home string) (string, bool) {
	if home == "" {
		return "", false
	}
	cfg := loadConfig(home)
	ref := cfg.Logo
	if ref == "" {
		return "", false
	}
	if strings.HasPrefix(ref, "/assets/") {
		name := strings.TrimPrefix(ref, "/assets/")
		full := filepath.Join(home, "share", "assets", filepath.Clean(name))
		if rel, err := filepath.Rel(filepath.Join(home, "share", "assets"), full); err != nil || strings.HasPrefix(rel, "..") {
			return "", false
		}
		if _, err := os.Stat(full); err != nil {
			return "", false
		}
		return full, true
	}
	if allowedRemoteURL(ref) {
		full, err := fetchRemote(home, ref)
		if err != nil || full == "" {
			return "", false
		}
		return full, true
	}
	return "", false
}
