package main

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

type fakeModelRunner struct{}

func (fakeModelRunner) Run(inputIDs, attentionMask, tokenTypeIDs []int64) ([]float32, error) {
	seqLen := len(inputIDs)
	if seqLen == 0 {
		return nil, fmt.Errorf("invalid token tensors")
	}
	return make([]float32, seqLen*miniLMDims), nil
}

func TestMiniLMEmbedderWithFakeRunner(t *testing.T) {
	dir := t.TempDir()
	vocab := filepath.Join(dir, "vocab.txt")
	if err := os.WriteFile(vocab, []byte("[CLS]\n[SEP]\nhello\nworld\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	tok, err := loadWordPieceTokenizer(vocab)
	if err != nil {
		t.Fatal(err)
	}
	emb := &miniLMEmbedder{tok: tok, ort: fakeModelRunner{}}
	if emb.Name() != "minilm" || emb.Dim() != miniLMDims {
		t.Fatal("minilm meta")
	}
	vec := emb.Embed("hello world")
	if len(vec) != miniLMDims {
		t.Fatalf("embed dim %d", len(vec))
	}
	vec = emb.Embed("")
	if len(vec) != miniLMDims {
		t.Fatal("fail-open embed")
	}
}

func TestOrtStatusErrorReleasesStatus(t *testing.T) {
	called := false
	old := ortReleaseStatusFn
	ortReleaseStatusFn = func(api, status uintptr) {
		called = api == 7 && status == 3
	}
	defer func() { ortReleaseStatusFn = old }()
	if ortStatusError(7, 3) == nil || !called {
		t.Fatal("release status hook")
	}
}

func TestOrtCallWithInjectedSyscall(t *testing.T) {
	old := ortSyscallN
	ortSyscallN = func(trap uintptr, args ...uintptr) (uintptr, uintptr, uintptr) {
		return 0, 0, 0
	}
	defer func() { ortSyscallN = old }()
	if ortCall(1) != 0 {
		t.Fatal("syscall dispatch")
	}
}

func TestOrtCommonHelpers(t *testing.T) {
	if ortStatusError(0, 0) != nil {
		t.Fatal("nil status")
	}
	if ortStatusError(0, 1) == nil {
		t.Fatal("non-zero status")
	}
	if apiFn(0, 0) != 0 {
		t.Fatal("zero api")
	}
	if ortCall(0) == 0 {
		t.Fatal("zero fn should fail")
	}
	if miniLMModelPath("/tmp") == "" || miniLMVocabPath("/tmp") == "" {
		t.Fatal("paths")
	}
}

func TestTryORTSessionWithInjectedOpener(t *testing.T) {
	home := t.TempDir()
	lib := filepath.Join(home, "libonnxruntime.so")
	if err := os.WriteFile(lib, []byte("fake-lib"), 0o644); err != nil {
		t.Fatal(err)
	}
	resetORTLibForTest()
	t.Setenv("OVERDRIVE_ORT_LIB", lib)
	t.Setenv("OVERDRIVE_SKIP_EMBED_DOWNLOAD", "1")

	old := ortSessionOpener
	ortSessionOpener = func(libPath, modelPath string) (modelRunner, error) {
		if libPath != lib {
			t.Fatalf("lib path %q", libPath)
		}
		return fakeModelRunner{}, nil
	}
	defer func() { ortSessionOpener = old }()

	sess, err := tryORTSession(home, "/tmp/model.onnx")
	if err != nil {
		t.Fatal(err)
	}
	ids, mask, types := []int64{1, 2, 3}, []int64{1, 1, 1}, []int64{0, 0, 0}
	out, err := sess.Run(ids, mask, types)
	if err != nil || len(out) != 3*miniLMDims {
		t.Fatalf("run out=%d err=%v", len(out), err)
	}
}

func TestTryMiniLMFullPathWithFakeORT(t *testing.T) {
	home := t.TempDir()
	lib := filepath.Join(home, "libonnxruntime.so")
	if err := os.WriteFile(lib, []byte("fake-lib"), 0o644); err != nil {
		t.Fatal(err)
	}
	dir := modelDir(home)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	for name, content := range map[string]string{
		"model.onnx": "model", "vocab.txt": "[CLS]\n[SEP]\nhello\n", "tokenizer.json": "{}",
	} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	resetORTLibForTest()
	resetRuntimeGlobals()
	t.Setenv("OVERDRIVE_HOME", home)
	t.Setenv("OVERDRIVE_ORT_LIB", lib)
	t.Setenv("OVERDRIVE_SKIP_EMBED_DOWNLOAD", "1")
	t.Setenv("OVERDRIVE_EMBEDDER", "minilm")

	old := ortSessionOpener
	ortSessionOpener = func(libPath, modelPath string) (modelRunner, error) {
		return fakeModelRunner{}, nil
	}
	defer func() { ortSessionOpener = old }()

	emb, ok := tryMiniLM(home)
	if !ok || emb == nil {
		t.Fatal("tryMiniLM should succeed with fake ort")
	}
	vec := emb.Embed("hello integration path")
	if len(vec) != miniLMDims {
		t.Fatalf("vec len %d", len(vec))
	}
	initEmbedder(home)
	if embedderName() != "minilm" {
		t.Fatalf("embedder %s", embedderName())
	}
}

func TestEnsureMiniLMPackLocalFiles(t *testing.T) {
	home := t.TempDir()
	dir := modelDir(home)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	for name := range map[string]string{"model.onnx": "m", "vocab.txt": "v", "tokenizer.json": "t"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := ensureMiniLMPack(home); err != nil {
		t.Fatal(err)
	}
}

func TestFakeORTSessionWhenTagged(t *testing.T) {
	lib := filepath.Join(t.TempDir(), "lib.so")
	if err := os.WriteFile(lib, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	sess, err := openORTSessionFFI(lib, "/tmp/model.onnx")
	if err != nil {
		t.Skip("fake ort build tag not enabled")
	}
	ids, mask, types := []int64{101, 102}, []int64{1, 1}, []int64{0, 0}
	out, err := sess.Run(ids, mask, types)
	if err != nil || len(out) != 2*miniLMDims {
		t.Fatalf("fake session run: len=%d err=%v", len(out), err)
	}
	if closer, ok := sess.(interface{ Close() }); ok {
		closer.Close()
	}
}
