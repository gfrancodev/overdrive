package main

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

func shareUIAssetWebPath(raw string) string {
	p := strings.TrimSpace(raw)
	if p == shareDefaultLogoPath {
		return p
	}
	if !strings.HasPrefix(p, "/assets/") {
		return ""
	}
	if strings.Contains(p, "..") {
		return ""
	}
	name := strings.TrimPrefix(p, "/assets/")
	name = filepath.Clean(name)
	if name == "." || strings.HasPrefix(name, "..") || strings.Contains(name, string(os.PathSeparator)) {
		return ""
	}
	return "/assets/" + name
}

func shareAssetDiskPath(home, webPath string) (string, bool) {
	safe := shareUIAssetWebPath(webPath)
	if safe == "" || safe == shareDefaultLogoPath {
		return "", false
	}
	name := strings.TrimPrefix(safe, "/assets/")
	root := filepath.Join(home, "share", "assets")
	full := filepath.Join(root, name)
	rel, err := filepath.Rel(root, full)
	if err != nil || strings.HasPrefix(rel, "..") {
		return "", false
	}
	info, err := os.Stat(full)
	if err != nil || info.IsDir() {
		return "", false
	}
	return full, true
}

func serveShareAssets(home string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, methodNotAllowedMsg, http.StatusMethodNotAllowed)
			return
		}
		full, ok := shareAssetDiskPath(home, r.URL.Path)
		if !ok {
			http.NotFound(w, r)
			return
		}
		w.Header().Set(headerCacheControl, "public, max-age=86400")
		http.ServeFile(w, r, full)
	}
}

func serveDashboardBrandImage(home string, pick func(shareUIConfig) string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, methodNotAllowedMsg, http.StatusMethodNotAllowed)
			return
		}
		cfg := loadShareUI(home)
		webPath := pick(cfg)
		if webPath == "" {
			webPath = shareDefaultLogoPath
		}
		if webPath == shareDefaultLogoPath {
			w.Header().Set(headerContentType, "image/png")
			w.Header().Set(headerCacheControl, "public, max-age=86400")
			_, _ = w.Write(dashboardLogo)
			return
		}
		full, ok := shareAssetDiskPath(home, webPath)
		if !ok {
			w.Header().Set(headerContentType, "image/png")
			w.Header().Set(headerCacheControl, "public, max-age=86400")
			_, _ = w.Write(dashboardLogo)
			return
		}
		w.Header().Set(headerCacheControl, "public, max-age=86400")
		http.ServeFile(w, r, full)
	}
}
