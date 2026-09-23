package main

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

func modelDir(home string) string {
	if v := strings.TrimSpace(os.Getenv("OVERDRIVE_MODEL_DIR")); v != "" {
		return v
	}
	return filepath.Join(home, "models", "minilm-l6-v2")
}

func atomicWriteFile(path string, r io.Reader, perm os.FileMode) error {
	dir := filepath.Dir(path)
	if err := hookMkdirAll(dir, 0o700); err != nil {
		return err
	}
	tmp, err := hookCreateTemp(dir, ".download-*")
	if err != nil {
		return err
	}
	tmpPath := ""
	if f, ok := tmp.(*os.File); ok {
		tmpPath = f.Name()
	}
	if _, err := hookIOCopy(tmp, r); err != nil {
		_ = tmp.Close()
		if tmpPath != "" {
			_ = hookRemove(tmpPath)
		}
		return err
	}
	if err := tmp.Close(); err != nil {
		if tmpPath != "" {
			_ = hookRemove(tmpPath)
		}
		return err
	}
	if err := hookChmod(tmpPath, perm); err != nil {
		_ = hookRemove(tmpPath)
		return err
	}
	return hookRename(tmpPath, path)
}

var downloadHTTPGet = func(client *http.Client, url string) (*http.Response, error) {
	return client.Get(url)
}

func downloadURL(url, dest string) error {
	if strings.TrimSpace(url) == "" {
		return fmt.Errorf("empty download url")
	}
	client := &http.Client{Timeout: 120 * time.Second}
	resp, err := downloadHTTPGet(client, url)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("download %s: HTTP %d", url, resp.StatusCode)
	}
	return atomicWriteFile(dest, resp.Body, 0o644)
}

func ensureFile(url, dest string) error {
	return hookEnsureFile(url, dest)
}

func ensureFileImpl(url, dest string) error {
	if st, err := os.Stat(dest); err == nil && st.Size() > 0 {
		return nil
	}
	if strings.TrimSpace(os.Getenv("OVERDRIVE_SKIP_EMBED_DOWNLOAD")) == "1" {
		return fmt.Errorf("download disabled")
	}
	if override := strings.TrimSpace(os.Getenv("OVERDRIVE_MODEL_URL")); override != "" && strings.Contains(dest, "model") {
		url = override
	}
	return downloadURL(url, dest)
}

func ensureMiniLMPack(home string) error {
	dir := modelDir(home)
	if err := ensureFile(miniLMModelURL, filepath.Join(dir, "model.onnx")); err != nil {
		return err
	}
	if err := ensureFile(miniLMVocabURL, filepath.Join(dir, "vocab.txt")); err != nil {
		return err
	}
	_ = ensureFile(miniLMTokenizerURL, filepath.Join(dir, "tokenizer.json"))
	return nil
}

var ortLibNames = map[string]string{
	"darwin":  "libonnxruntime.dylib",
	"windows": "onnxruntime.dll",
}

func ortLibName() string {
	if name, ok := ortLibNames[runtimePlatformOS()]; ok {
		return name
	}
	return "libonnxruntime.so"
}

var runtimePlatformOS = func() string {
	return runtime.GOOS
}

var ortReleaseURLs = map[string]string{
	"linux/amd64":     ortLinuxAMD64URL,
	"linux/arm64":     ortLinuxARM64URL,
	"darwin/amd64":    ortDarwinAMD64URL,
	"darwin/arm64":    ortDarwinARM64URL,
	"windows/amd64":   ortWindowsAMD64URL,
	"windows/arm64":   ortWindowsARM64URL,
}

var defaultORTURL = func() string {
	return ortReleaseURLs[runtime.GOOS+"/"+runtime.GOARCH]
}

func defaultORTURLFor(platform string) string {
	return ortReleaseURLs[platform]
}

func extractORTArchive(archivePath, libDir string) error {
	switch {
	case strings.HasSuffix(archivePath, ".tgz"), strings.HasSuffix(archivePath, ".tar.gz"):
		f, err := os.Open(archivePath)
		if err != nil {
			return err
		}
		defer f.Close()
		gz, err := gzip.NewReader(f)
		if err != nil {
			return err
		}
		defer gz.Close()
		tr := tar.NewReader(gz)
		for {
			hdr, err := tr.Next()
			if err == io.EOF {
				break
			}
			if err != nil {
				return err
			}
			base := filepath.Base(hdr.Name)
			if hdr.Typeflag != tar.TypeReg {
				continue
			}
			if !strings.Contains(base, "onnxruntime") || !(strings.HasSuffix(base, ".so") || strings.HasSuffix(base, ".dylib")) {
				continue
			}
			dest := filepath.Join(libDir, ortLibName())
			return atomicWriteFile(dest, tr, 0o755)
		}
		return fmt.Errorf("onnxruntime library not found in archive")
	case strings.HasSuffix(archivePath, ".zip"):
		return extractORTZip(archivePath, libDir)
	default:
		return fmt.Errorf("unsupported ort archive: %s", archivePath)
	}
}

func extractORTZip(path, libDir string) error {
	r, err := zip.OpenReader(path)
	if err != nil {
		return err
	}
	defer r.Close()
	for _, f := range r.File {
		if !matchesORTZipEntry(f.Name) {
			continue
		}
		rc, err := f.Open()
		if err != nil {
			return err
		}
		dest := filepath.Join(libDir, ortLibName())
		err = atomicWriteFile(dest, rc, 0o755)
		_ = rc.Close()
		return err
	}
	return fmt.Errorf("onnxruntime dll not found in zip")
}

func ensureORTLib(home string) (string, error) {
	if path, err := resolveORTLibFromEnv(); err != nil {
		return "", err
	} else if path != "" {
		return path, nil
	}
	dest, ok := bundledORTLibPath(home)
	if ok {
		return dest, nil
	}
	url, err := ortReleaseURL()
	if err != nil {
		return "", err
	}
	tmpDir, err := hookMkdirTemp("", "overdrive-ort-*")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(tmpDir)
	archive := filepath.Join(tmpDir, ortArchiveFilename(url))
	if err := downloadURL(url, archive); err != nil {
		return "", err
	}
	if err := extractORTArchive(archive, libDir(home)); err != nil {
		return "", err
	}
	return dest, nil
}
