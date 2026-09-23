//go:build overdrive_ort_integration

package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestRealORTSessionWhenAvailable(t *testing.T) {
	home := t.TempDir()
	resetORTLibForTest()
	resetRuntimeGlobals()
	t.Setenv("OVERDRIVE_HOME", home)
	t.Setenv("OVERDRIVE_SKIP_EMBED_DOWNLOAD", "0")

	if lib := os.Getenv("OVERDRIVE_ORT_LIB"); lib != "" {
		t.Setenv("OVERDRIVE_ORT_LIB", lib)
	}

	if err := ensureMiniLMPack(home); err != nil {
		t.Skip("minilm pack unavailable: " + err.Error())
	}
	libPath, err := resolveORTLibPath(home)
	if err != nil {
		t.Skip("ort lib unavailable: " + err.Error())
	}
	model := miniLMModelPath(home)
	if _, err := os.Stat(model); err != nil {
		t.Skip("model missing")
	}

	old := ortSessionOpener
	ortSessionOpener = openORTSessionFFI
	defer func() { ortSessionOpener = old }()

	sess, err := tryORTSession(home, model)
	if err != nil {
		t.Fatalf("real ort session: %v (lib=%s)", err, libPath)
	}
	tok, err := loadWordPieceTokenizer(miniLMVocabPath(home))
	if err != nil {
		t.Fatal(err)
	}
	ids, mask, types := tok.Encode("integration test sentence", 16)
	out, err := sess.Run(ids, mask, types)
	if err != nil {
		t.Fatal(err)
	}
	if len(out) < miniLMDims {
		t.Fatalf("hidden size %d", len(out))
	}
	if c, ok := sess.(interface{ Close() }); ok {
		c.Close()
	}
	_ = filepath.Base(libPath)
}
