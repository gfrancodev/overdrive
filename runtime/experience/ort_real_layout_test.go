package main

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"os"
	"path/filepath"
	"testing"
)

type tarEntry struct {
	name, body, link string
}

func writeTgz(t *testing.T, entries []tarEntry) string {
	t.Helper()
	var buf bytes.Buffer
	gzw := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gzw)
	for _, e := range entries {
		hdr := &tar.Header{Name: e.name, Mode: 0o755, Size: int64(len(e.body)), Typeflag: tar.TypeReg}
		if e.link != "" {
			hdr = &tar.Header{Name: e.name, Mode: 0o777, Linkname: e.link, Typeflag: tar.TypeSymlink}
		}
		if err := tw.WriteHeader(hdr); err != nil {
			t.Fatal(err)
		}
		if e.link == "" {
			if _, err := tw.Write([]byte(e.body)); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gzw.Close(); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "ort.tgz")
	if err := os.WriteFile(path, buf.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func extractedORT(t *testing.T, archive string) string {
	t.Helper()
	libDir := filepath.Join(t.TempDir(), "lib")
	if err := extractORTArchive(archive, libDir); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(filepath.Join(libDir, ortLibName()))
	if err != nil {
		t.Fatal(err)
	}
	return string(got)
}

// The Linux archives ship the library as libonnxruntime.so.1.18.1, with libonnxruntime.so a symlink to it. Only an
// unversioned regular file was accepted, so no Linux machine ever got the library and every embedder was hashed.
func TestTheLinuxArchiveGivesTheVersionedLibrary(t *testing.T) {
	archive := writeTgz(t, []tarEntry{
		{name: "onnxruntime-linux-x64-1.18.1/lib/libonnxruntime.so", link: "libonnxruntime.so.1.18.1"},
		{name: "onnxruntime-linux-x64-1.18.1/lib/libonnxruntime_providers_shared.so", body: "providers"},
		{name: "onnxruntime-linux-x64-1.18.1/lib/libonnxruntime.so.1.18.1", body: "runtime"},
	})
	if got := extractedORT(t, archive); got != "runtime" {
		t.Fatalf("extracted %q", got)
	}
}

// The macOS archives carry a debug-symbols copy under the same name, inside a .dSYM bundle.
func TestTheMacArchiveSkipsTheDebugSymbols(t *testing.T) {
	archive := writeTgz(t, []tarEntry{
		{name: "onnxruntime-osx-arm64-1.18.1/lib/libonnxruntime.1.18.1.dylib.dSYM/Contents/Resources/DWARF/libonnxruntime.1.18.1.dylib", body: "dwarf"},
		{name: "onnxruntime-osx-arm64-1.18.1/lib/libonnxruntime_providers_shared.dylib", body: "providers"},
		{name: "onnxruntime-osx-arm64-1.18.1/lib/libonnxruntime.dylib", link: "libonnxruntime.1.18.1.dylib"},
		{name: "onnxruntime-osx-arm64-1.18.1/lib/libonnxruntime.1.18.1.dylib", body: "runtime"},
	})
	if got := extractedORT(t, archive); got != "runtime" {
		t.Fatalf("extracted %q", got)
	}
}

// Any DLL named after onnxruntime was taken, and the providers DLL has no OrtGetApiBase.
func TestTheWindowsArchiveGivesOnnxruntimeDLL(t *testing.T) {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for _, f := range []struct{ name, body string }{
		{"onnxruntime-win-arm64-1.18.1/lib/onnxruntime_providers_shared.dll", "providers"},
		{"onnxruntime-win-arm64-1.18.1/lib/onnxruntime.dll", "runtime"},
	} {
		w, err := zw.Create(f.name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write([]byte(f.body)); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	archive := filepath.Join(t.TempDir(), "ort.zip")
	if err := os.WriteFile(archive, buf.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := extractedORT(t, archive); got != "runtime" {
		t.Fatalf("extracted %q", got)
	}
}

// The OrtApi table opens with CreateStatus, GetErrorCode and GetErrorMessage (onnxruntime_c_api.h). The indices
// skipped those three, so every call landed three functions early: on Windows the first one took the runtime down.
func TestORTAPIIndicesFollowTheHeader(t *testing.T) {
	for name, pair := range map[string][2]int{
		"CreateEnv":                      {ortIdxCreateEnv, 3},
		"CreateSession":                  {ortIdxCreateSession, 7},
		"Run":                            {ortIdxRun, 9},
		"CreateSessionOptions":           {ortIdxCreateSessionOptions, 10},
		"CreateTensorWithDataAsOrtValue": {ortIdxCreateTensorWithDataAsOrtValue, 49},
		"GetTensorMutableData":           {ortIdxGetTensorMutableData, 51},
		"CreateCpuMemoryInfo":            {ortIdxCreateCpuMemoryInfo, 69},
		"ReleaseEnv":                     {ortIdxReleaseEnv, 92},
		"ReleaseStatus":                  {ortIdxReleaseStatus, 93},
		"ReleaseMemoryInfo":              {ortIdxReleaseMemoryInfo, 94},
		"ReleaseSession":                 {ortIdxReleaseSession, 95},
		"ReleaseValue":                   {ortIdxReleaseValue, 96},
		"ReleaseSessionOptions":          {ortIdxReleaseSessionOptions, 100},
	} {
		if pair[0] != pair[1] {
			t.Errorf("%s is at %d, the header puts it at %d", name, pair[0], pair[1])
		}
	}
}

// A session that opened but could not run was still chosen, and every text then got the hashed fallback under
// MiniLM's name and size. The model now has to embed once before it is used.
func TestMiniLMIsChosenOnlyWhenTheModelRuns(t *testing.T) {
	for name, runner := range map[string]modelRunner{"fails": errModelRunner{}, "short output": shortHiddenRunner{}} {
		t.Run(name, func(t *testing.T) {
			home := t.TempDir()
			lib := filepath.Join(home, "libonnxruntime.so")
			if err := os.WriteFile(lib, []byte("fake-lib"), 0o644); err != nil {
				t.Fatal(err)
			}
			dir := modelDir(home)
			if err := os.MkdirAll(dir, 0o700); err != nil {
				t.Fatal(err)
			}
			for file, content := range map[string]string{
				"model.onnx": "model", "vocab.txt": "[CLS]\n[SEP]\nhello\n", "tokenizer.json": "{}",
			} {
				if err := os.WriteFile(filepath.Join(dir, file), []byte(content), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			resetORTLibForTest()
			resetRuntimeGlobals()
			t.Cleanup(resetRuntimeGlobals)
			t.Setenv("OVERDRIVE_HOME", home)
			t.Setenv("OVERDRIVE_ORT_LIB", lib)
			t.Setenv("OVERDRIVE_SKIP_EMBED_DOWNLOAD", "1")
			t.Setenv("OVERDRIVE_EMBEDDER", "")
			old := ortSessionOpener
			ortSessionOpener = func(string, string) (modelRunner, error) { return runner, nil }
			t.Cleanup(func() { ortSessionOpener = old })

			if _, ok := tryMiniLM(home); ok {
				t.Fatal("a model that cannot embed was chosen")
			}
			initEmbedder(home)
			if embedderName() != "hashed" || embedDim() != hashedDims {
				t.Fatalf("embedder %s/%d", embedderName(), embedDim())
			}
		})
	}
}
