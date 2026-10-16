package main

import (
	"bytes"
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

const (
	shareUIRemoteMaxBytes = 4 << 20
	shareUIRemoteTimeout  = 30 * time.Second
)

var shareUIRemoteHTTPGet = func(client *http.Client, rawURL string) (*http.Response, error) {
	return client.Get(rawURL)
}

func shareUIAllowedRemoteURL(raw string) (string, bool) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Scheme != "https" || u.Host == "" {
		return "", false
	}
	if u.User != nil {
		return "", false
	}
	if len(raw) > 2048 {
		return "", false
	}
	return u.String(), true
}

func shareUIImageRef(raw string) string {
	p := strings.TrimSpace(raw)
	if p == shareDefaultLogoPath {
		return p
	}
	if path := shareUIAssetWebPath(p); path != "" {
		return path
	}
	if u, ok := shareUIAllowedRemoteURL(p); ok {
		return u
	}
	return ""
}

func shareUIRemoteCachePath(home string) string {
	return filepath.Join(home, "share", "whitelabel-remote-cache.json")
}

func readShareUIRemoteCache(home string) map[string]string {
	path := shareUIRemoteCachePath(home)
	raw, err := os.ReadFile(path)
	if err != nil && os.IsNotExist(err) {
		legacy := filepath.Join(home, "share", "ui-remote-cache.json")
		raw, err = os.ReadFile(legacy)
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

func writeShareUIRemoteCache(home string, cache map[string]string) error {
	dir := filepath.Join(home, "share")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	raw, err := json.Marshal(cache)
	if err != nil {
		return err
	}
	return atomicWriteFile(shareUIRemoteCachePath(home), strings.NewReader(string(raw)), 0o600)
}

func shareUIRemoteFileName(rawURL, ext string) string {
	sum := sha256.Sum256([]byte(rawURL))
	if ext == "" {
		ext = ".img"
	}
	if !strings.HasPrefix(ext, ".") {
		ext = "." + ext
	}
	return "remote-" + hex.EncodeToString(sum[:8]) + ext
}

func shareUIImageExt(contentType string, rawURL string) string {
	ct := strings.ToLower(strings.TrimSpace(strings.Split(contentType, ";")[0]))
	switch ct {
	case "image/png":
		return ".png"
	case "image/jpeg", "image/jpg":
		return ".jpg"
	case "image/webp":
		return ".webp"
	case "image/gif":
		return ".gif"
	case "image/x-icon", "image/vnd.microsoft.icon":
		return ".ico"
	case "image/svg+xml":
		return ".svg"
	}
	path := strings.ToLower(rawURL)
	for _, ext := range []string{".png", ".jpg", ".jpeg", ".webp", ".gif", ".ico", ".svg"} {
		if strings.HasSuffix(path, ext) {
			if ext == ".jpeg" {
				return ".jpg"
			}
			return ext
		}
	}
	return ".png"
}

func shareUIImageLooksValid(head []byte, ext string) bool {
	if len(head) < 4 {
		return false
	}
	switch ext {
	case ".png":
		return head[0] == 0x89 && head[1] == 'P' && head[2] == 'N' && head[3] == 'G'
	case ".jpg", ".jpeg":
		return head[0] == 0xff && head[1] == 0xd8
	case ".gif":
		return len(head) >= 6 && head[0] == 'G' && head[1] == 'I' && head[2] == 'F'
	case ".webp":
		return len(head) >= 12 && head[0] == 'R' && head[1] == 'I' && head[2] == 'F' && head[3] == 'F'
	case ".ico":
		return head[0] == 0 && head[1] == 0 && head[2] == 1 && head[3] == 0
	case ".svg":
		s := strings.ToLower(string(head))
		return strings.Contains(s, "<svg")
	default:
		return true
	}
}

func cacheShareUIRemoteImage(home, rawURL string) (string, error) {
	allowed, ok := shareUIAllowedRemoteURL(rawURL)
	if !ok {
		return "", fmt.Errorf("remote image url not allowed")
	}
	assetsDir := filepath.Join(home, "share", "assets")
	if err := os.MkdirAll(assetsDir, 0o700); err != nil {
		return "", err
	}
	cache := readShareUIRemoteCache(home)
	if name := strings.TrimSpace(cache[allowed]); name != "" {
		full := filepath.Join(assetsDir, name)
		if _, err := os.Stat(full); err == nil {
			return "/assets/" + name, nil
		}
	}
	client := &http.Client{Timeout: shareUIRemoteTimeout}
	resp, err := shareUIRemoteHTTPGet(client, allowed)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("remote image HTTP %d", resp.StatusCode)
	}
	ext := shareUIImageExt(resp.Header.Get("Content-Type"), allowed)
	limited := io.LimitReader(resp.Body, shareUIRemoteMaxBytes+1)
	body, err := io.ReadAll(limited)
	if err != nil {
		return "", err
	}
	if len(body) == 0 || len(body) > shareUIRemoteMaxBytes {
		return "", fmt.Errorf("remote image size out of range")
	}
	if !shareUIImageLooksValid(body, ext) {
		return "", fmt.Errorf("remote image format mismatch")
	}
	name := shareUIRemoteFileName(allowed, ext)
	dest := filepath.Join(assetsDir, name)
	if err := atomicWriteFile(dest, bytes.NewReader(body), 0o600); err != nil {
		return "", err
	}
	cache[allowed] = name
	_ = writeShareUIRemoteCache(home, cache)
	return "/assets/" + name, nil
}

func resolveShareUIImage(home, ref, fallback string) string {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return fallback
	}
	if ref == shareDefaultLogoPath {
		return ref
	}
	if path := shareUIAssetWebPath(ref); path != "" {
		return path
	}
	if _, ok := shareUIAllowedRemoteURL(ref); ok {
		cached, err := cacheShareUIRemoteImage(home, ref)
		if err == nil && cached != "" {
			return cached
		}
		return fallback
	}
	return fallback
}

func materializeShareUIAssets(home string, cfg shareUIConfig) shareUIConfig {
	logo := resolveShareUIImage(home, cfg.Logo, shareDefaultLogoPath)
	cfg.Logo = logo
	if cfg.Favicon != "" {
		cfg.Favicon = resolveShareUIImage(home, cfg.Favicon, logo)
	}
	if cfg.AppleTouchIcon != "" {
		cfg.AppleTouchIcon = resolveShareUIImage(home, cfg.AppleTouchIcon, logo)
	}
	return cfg
}

func warmShareWhitelabelAssets(home string) {
	_ = loadShareUI(home)
}
