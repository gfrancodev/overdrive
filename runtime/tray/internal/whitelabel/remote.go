package whitelabel

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const remoteMaxBytes = 4 << 20

var remoteHTTPGet = func(client *http.Client, rawURL string) (*http.Response, error) {
	return client.Get(rawURL)
}

func allowedRemoteURL(raw string) bool {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil {
		return false
	}
	return len(raw) <= 2048
}

func remoteCachePath(home string) string {
	return filepath.Join(home, "share", "whitelabel-remote-cache.json")
}

func readRemoteCache(home string) map[string]string {
	raw, err := os.ReadFile(remoteCachePath(home))
	if err != nil && os.IsNotExist(err) {
		raw, err = os.ReadFile(filepath.Join(home, "share", "ui-remote-cache.json"))
	}
	if err != nil {
		return map[string]string{}
	}
	out := map[string]string{}
	if json.Unmarshal(raw, &out) != nil {
		return map[string]string{}
	}
	return out
}

func writeRemoteCache(home string, cache map[string]string) error {
	dir := filepath.Join(home, "share")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	raw, err := json.Marshal(cache)
	if err != nil {
		return err
	}
	return os.WriteFile(remoteCachePath(home), raw, 0o600)
}

func remoteFileName(rawURL, ext string) string {
	sum := sha256.Sum256([]byte(rawURL))
	if ext == "" {
		ext = ".png"
	}
	return "remote-" + hex.EncodeToString(sum[:8]) + ext
}

func fetchRemote(home, rawURL string) (string, error) {
	if !allowedRemoteURL(rawURL) {
		return "", fmt.Errorf("url not allowed")
	}
	assetsDir := filepath.Join(home, "share", "assets")
	if err := os.MkdirAll(assetsDir, 0o700); err != nil {
		return "", err
	}
	cache := readRemoteCache(home)
	if name := strings.TrimSpace(cache[rawURL]); name != "" {
		full := filepath.Join(assetsDir, name)
		if _, err := os.Stat(full); err == nil {
			return full, nil
		}
	}
	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := remoteHTTPGet(client, rawURL)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("http %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, remoteMaxBytes+1))
	if err != nil || len(body) == 0 || len(body) > remoteMaxBytes {
		return "", fmt.Errorf("bad body")
	}
	ext := ".png"
	lower := strings.ToLower(rawURL)
	if strings.HasSuffix(lower, ".ico") {
		ext = ".ico"
	}
	name := remoteFileName(rawURL, ext)
	dest := filepath.Join(assetsDir, name)
	if err := os.WriteFile(dest, body, 0o600); err != nil {
		return "", err
	}
	cache[rawURL] = name
	_ = writeRemoteCache(home, cache)
	return dest, nil
}
