package whitelabel

import (
	"os"
	"path/filepath"
	"strings"
)

// Home is OVERDRIVE_HOME or ~/.overdrive.
func Home() string {
	if v := strings.TrimSpace(os.Getenv("OVERDRIVE_HOME")); v != "" {
		return v
	}
	user, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(user, ".overdrive")
}
