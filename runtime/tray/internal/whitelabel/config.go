package whitelabel

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
)

const (
	defaultLogoWeb = "/logo.png"
	whitelabelFile = "whitelabel.json"
	legacyUIFile   = "ui.json"
)

type config struct {
	Logo    string `json:"logo"`
	Favicon string `json:"favicon"`
}

func loadConfig(home string) config {
	var out config
	raw, err := readWhitelabelJSON(home)
	if err != nil {
		return out
	}
	var in config
	if json.Unmarshal(raw, &in) != nil {
		return out
	}
	if ref := imageRef(strings.TrimSpace(in.Logo)); ref != "" {
		out.Logo = ref
	} else if ref := imageRef(strings.TrimSpace(in.Favicon)); ref != "" {
		out.Logo = ref
	}
	return out
}

func readWhitelabelJSON(home string) ([]byte, error) {
	primary := filepath.Join(home, "share", whitelabelFile)
	raw, err := os.ReadFile(primary)
	if err == nil {
		return raw, nil
	}
	if !os.IsNotExist(err) {
		return nil, err
	}
	return os.ReadFile(filepath.Join(home, "share", legacyUIFile))
}

func imageRef(raw string) string {
	if raw == "" || raw == defaultLogoWeb {
		return ""
	}
	if strings.HasPrefix(raw, "/assets/") && !strings.Contains(raw, "..") {
		name := strings.TrimPrefix(raw, "/assets/")
		if name != "" && name != "." {
			return raw
		}
	}
	if allowedRemoteURL(raw) {
		return raw
	}
	return ""
}
