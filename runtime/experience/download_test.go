package main

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAtomicWriteFileAndDownloadURL(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "nested", "out.txt")
	if err := atomicWriteFile(path, strings.NewReader("hello"), 0o644); err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(path)
	if err != nil || string(body) != "hello" {
		t.Fatal("atomic write")
	}
	if err := downloadURL("", path); err == nil {
		t.Fatal("empty url should fail")
	}
}

func TestDownloadURLWithMockServer(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("payload"))
	}))
	defer srv.Close()
	dest := filepath.Join(t.TempDir(), "file.bin")
	oldGet := downloadHTTPGet
	downloadHTTPGet = func(client *http.Client, url string) (*http.Response, error) {
		return client.Get(srv.URL)
	}
	defer func() { downloadHTTPGet = oldGet }()
	if err := downloadURL(srv.URL, dest); err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(dest)
	if err != nil || string(body) != "payload" {
		t.Fatal("download content")
	}
}

func TestEnsureFileSkipDownload(t *testing.T) {
	dir := t.TempDir()
	dest := filepath.Join(dir, "model.onnx")
	t.Setenv("OVERDRIVE_SKIP_EMBED_DOWNLOAD", "1")
	if err := ensureFile("http://example.com/model", dest); err == nil {
		t.Fatal("expected download disabled error")
	}
	if err := os.WriteFile(dest, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := ensureFile("http://example.com/model", dest); err != nil {
		t.Fatal(err)
	}
}

func TestExtractORTArchiveTgz(t *testing.T) {
	dir := t.TempDir()
	archive := filepath.Join(dir, "ort.tgz")
	var buf bytes.Buffer
	gzw := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gzw)
	hdr := &tar.Header{Name: "onnxruntime-linux/libonnxruntime.so", Mode: 0o755, Size: 4, Typeflag: tar.TypeReg}
	if err := tw.WriteHeader(hdr); err != nil {
		t.Fatal(err)
	}
	if _, err := tw.Write([]byte("lib!")); err != nil {
		t.Fatal(err)
	}
	_ = tw.Close()
	_ = gzw.Close()
	if err := os.WriteFile(archive, buf.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
	libDir := filepath.Join(dir, "lib")
	if err := extractORTArchive(archive, libDir); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(libDir, ortLibName())); err != nil {
		t.Fatal(err)
	}
}

func TestExtractORTArchiveZip(t *testing.T) {
	dir := t.TempDir()
	archive := filepath.Join(dir, "ort.zip")
	buf := new(bytes.Buffer)
	zw := zip.NewWriter(buf)
	w, err := zw.Create("onnxruntime-win/onnxruntime.dll")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.Write([]byte("dll!")); err != nil {
		t.Fatal(err)
	}
	_ = zw.Close()
	if err := os.WriteFile(archive, buf.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
	libDir := filepath.Join(dir, "lib")
	if err := extractORTArchive(archive, libDir); err != nil {
		t.Fatal(err)
	}
}

func TestExtractORTArchiveUnsupported(t *testing.T) {
	path := filepath.Join(t.TempDir(), "file.bin")
	if err := os.WriteFile(path, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := extractORTArchive(path, t.TempDir()); err == nil {
		t.Fatal("unsupported archive")
	}
}

func TestDefaultORTURLAndModelDir(t *testing.T) {
	if defaultORTURLFor("linux/amd64") == "" {
		t.Fatal("linux amd64 url")
	}
	if defaultORTURLFor("unknown/arch") != "" {
		t.Fatal("unknown platform")
	}
	if ortLibName() == "" {
		t.Fatal("ort lib name")
	}
	oldOS := runtimePlatformOS
	runtimePlatformOS = func() string { return "darwin" }
	if ortLibName() != "libonnxruntime.dylib" {
		t.Fatal("darwin ort lib")
	}
	runtimePlatformOS = func() string { return "windows" }
	if ortLibName() != "onnxruntime.dll" {
		t.Fatal("windows ort lib")
	}
	runtimePlatformOS = oldOS
	if turbovecLibName() == "" {
		t.Fatal("turbovec lib name")
	}
	runtimePlatformOS = func() string { return "darwin" }
	if turbovecLibName() != "liboverdrive_turbovec_ffi.dylib" {
		t.Fatal("darwin turbovec lib")
	}
	runtimePlatformOS = oldOS
	home := t.TempDir()
	t.Setenv("OVERDRIVE_MODEL_DIR", filepath.Join(home, "custom-models"))
	if modelDir(home) != filepath.Join(home, "custom-models") {
		t.Fatal("model dir override")
	}
}

func TestEnsureORTLibFromEnvAndCache(t *testing.T) {
	home := t.TempDir()
	lib := filepath.Join(home, "libonnxruntime.so")
	if err := os.WriteFile(lib, []byte("ort"), 0o644); err != nil {
		t.Fatal(err)
	}
	resetORTLibForTest()
	t.Setenv("OVERDRIVE_ORT_LIB", lib)
	t.Setenv("OVERDRIVE_SKIP_EMBED_DOWNLOAD", "1")
	got, err := ensureORTLib(home)
	if err != nil || got != lib {
		t.Fatalf("env lib: %q err=%v", got, err)
	}
	resetORTLibForTest()
	t.Setenv("OVERDRIVE_ORT_LIB", "")
	dest := filepath.Join(libDir(home), ortLibName())
	if err := os.MkdirAll(libDir(home), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dest, []byte("cached"), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err = ensureORTLib(home)
	if err != nil || got != dest {
		t.Fatalf("cached lib: %q err=%v", got, err)
	}
}

func TestEnsureORTLibDownloadPath(t *testing.T) {
	home := t.TempDir()
	resetORTLibForTest()
	t.Setenv("OVERDRIVE_SKIP_EMBED_DOWNLOAD", "0")
	t.Setenv("OVERDRIVE_ORT_URL", "")
	if runtimePlatform() != "" {
		if defaultORTURL() == "" {
			_, err := ensureORTLib(home)
			if err == nil {
				t.Fatal("expected unsupported platform error")
			}
		}
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var buf bytes.Buffer
		gzw := gzip.NewWriter(&buf)
		tw := tar.NewWriter(gzw)
		hdr := &tar.Header{Name: "onnxruntime/libonnxruntime.so", Mode: 0o755, Size: 3, Typeflag: tar.TypeReg}
		_ = tw.WriteHeader(hdr)
		_, _ = tw.Write([]byte("ort"))
		_ = tw.Close()
		_ = gzw.Close()
		_, _ = w.Write(buf.Bytes())
	}))
	defer srv.Close()
	resetORTLibForTest()
	t.Setenv("OVERDRIVE_SKIP_EMBED_DOWNLOAD", "0")
	t.Setenv("OVERDRIVE_ORT_URL", srv.URL+"/ort.tgz")
	oldGet := downloadHTTPGet
	downloadHTTPGet = func(client *http.Client, url string) (*http.Response, error) {
		return client.Get(srv.URL + "/ort.tgz")
	}
	defer func() { downloadHTTPGet = oldGet }()
	got, err := ensureORTLib(home)
	if err != nil {
		t.Fatal(err)
	}
	if got != filepath.Join(libDir(home), ortLibName()) {
		t.Fatalf("got %q", got)
	}
}

func runtimePlatform() string {
	return os.Getenv("GOOS") + "/" + os.Getenv("GOARCH")
}

func TestEnsureORTLibDisabled(t *testing.T) {
	home := t.TempDir()
	t.Setenv("OVERDRIVE_SKIP_EMBED_DOWNLOAD", "1")
	_, err := ensureORTLib(home)
	if err == nil {
		t.Fatal("expected disabled")
	}
}
