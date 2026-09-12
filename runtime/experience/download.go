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
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".download-*")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	if _, err := io.Copy(tmp, r); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmpPath)
		return err
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpPath)
		return err
	}
	if err := os.Chmod(tmpPath, perm); err != nil {
		_ = os.Remove(tmpPath)
		return err
	}
	return os.Rename(tmpPath, path)
}

func downloadURL(url, dest string) error {
	if strings.TrimSpace(url) == "" {
		return fmt.Errorf("empty download url")
	}
	client := &http.Client{Timeout: 120 * time.Second}
	resp, err := client.Get(url)
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

func ortLibName() string {
	switch runtime.GOOS {
	case "darwin":
		return "libonnxruntime.dylib"
	case "windows":
		return "onnxruntime.dll"
	default:
		return "libonnxruntime.so"
	}
}

func defaultORTURL() string {
	switch runtime.GOOS + "/" + runtime.GOARCH {
	case "linux/amd64":
		return ortLinuxAMD64URL
	case "linux/arm64":
		return ortLinuxARM64URL
	case "darwin/amd64":
		return ortDarwinAMD64URL
	case "darwin/arm64":
		return ortDarwinARM64URL
	case "windows/amd64":
		return ortWindowsAMD64URL
	case "windows/arm64":
		return ortWindowsARM64URL
	default:
		return ""
	}
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
		base := filepath.Base(f.Name)
		if !strings.Contains(base, "onnxruntime") || !strings.HasSuffix(base, ".dll") {
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
	if v := strings.TrimSpace(os.Getenv("OVERDRIVE_ORT_LIB")); v != "" {
		if st, err := os.Stat(v); err == nil && !st.IsDir() {
			return v, nil
		}
		return "", fmt.Errorf("ort library missing: %s", v)
	}
	dest := filepath.Join(libDir(home), ortLibName())
	if st, err := os.Stat(dest); err == nil && st.Size() > 0 {
		return dest, nil
	}
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
	tmpDir, err := os.MkdirTemp("", "overdrive-ort-*")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(tmpDir)
	archive := filepath.Join(tmpDir, "ort-archive")
	if strings.HasSuffix(url, ".zip") {
		archive += ".zip"
	} else {
		archive += ".tgz"
	}
	if err := downloadURL(url, archive); err != nil {
		return "", err
	}
	if err := extractORTArchive(archive, libDir(home)); err != nil {
		return "", err
	}
	return dest, nil
}
