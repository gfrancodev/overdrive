package main

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

func resolveORTLibFromEnv() (string, error) {
	v := strings.TrimSpace(os.Getenv("OVERDRIVE_ORT_LIB"))
	if v == "" {
		return "", nil
	}
	if st, err := os.Stat(v); err == nil && !st.IsDir() {
		return v, nil
	}
	return "", fmt.Errorf("ort library missing: %s", v)
}

func bundledORTLibPath(home string) (string, bool) {
	dest := filepath.Join(libDir(home), ortLibName())
	if st, err := os.Stat(dest); err == nil && st.Size() > 0 {
		return dest, true
	}
	return dest, false
}

func ortReleaseURL() (string, error) {
	if strings.TrimSpace(os.Getenv("OVERDRIVE_SKIP_EMBED_DOWNLOAD")) == "1" {
		return "", fmt.Errorf("ort download disabled")
	}
	url := strings.TrimSpace(os.Getenv("OVERDRIVE_ORT_URL"))
	if url == "" {
		url = defaultORTURL()
	}
	if url == "" {
		return "", fmt.Errorf("no ort release for %s/%s", runtime.GOOS, runtime.GOARCH)
	}
	return url, nil
}

func ortArchiveFilename(url string) string {
	if strings.HasSuffix(url, ".zip") {
		return "ort-archive.zip"
	}
	return "ort-archive.tgz"
}
