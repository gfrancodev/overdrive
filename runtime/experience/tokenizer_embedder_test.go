package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestStubAndHashedEmbedder(t *testing.T) {
	resetRuntimeGlobals()
	home := t.TempDir()
	t.Setenv("OVERDRIVE_HOME", home)
	t.Setenv("OVERDRIVE_EMBEDDER", "stub")
	v1 := embedText("hello world")
	v2 := embedText("hello world")
	if len(v1) != hashedDims || len(v2) != hashedDims {
		t.Fatal("dim")
	}
	if embedderName() != "stub" {
		t.Fatalf("embedder %s", embedderName())
	}
	t.Setenv("OVERDRIVE_EMBEDDER", "hashed")
	resetRuntimeGlobals()
	if embedderName() != "hashed" {
		t.Fatal("hashed embedder")
	}
}

func TestWordPieceTokenizer(t *testing.T) {
	dir := t.TempDir()
	vocab := filepath.Join(dir, "vocab.txt")
	lines := []string{"[CLS]", "[SEP]", "hello", "world", "##ing"}
	if err := os.WriteFile(vocab, []byte(strings.Join(lines, "\n")), 0o644); err != nil {
		t.Fatal(err)
	}
	tok, err := loadWordPieceTokenizer(vocab)
	if err != nil {
		t.Fatal(err)
	}
	ids, mask, types := tok.Encode("hello world", 8)
	if len(ids) != 8 || len(mask) != 8 || len(types) != 8 {
		t.Fatal("encode lengths")
	}
	if ids[0] != tok.cls {
		t.Fatal("cls token")
	}
	if err := os.WriteFile(vocab, []byte(""), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := loadWordPieceTokenizer(vocab); err == nil {
		t.Fatal("empty vocab")
	}
}

func TestLookupTokenFallback(t *testing.T) {
	vocab := map[string]int64{"a": 1}
	if lookupToken(vocab, "missing", 99) != 99 {
		t.Fatal("fallback")
	}
}
