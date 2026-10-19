//go:build overdrive_ort_integration

package main

import (
	"testing"
)

// MiniLM vectors leave meanPoolL2 with unit length, so their dot product is the cosine.
func cosine(a, b []float32) float64 {
	var dot float64
	for i := range a {
		dot += float64(a[i]) * float64(b[i])
	}
	return dot
}

// The build tag asks for the real ONNX Runtime and MiniLM, so anything missing fails instead of skipping: a skip
// here hid that no Linux machine ever got the library.
func TestTheRealMiniLMTellsMeaningsApart(t *testing.T) {
	home := t.TempDir()
	resetRuntimeGlobals()
	t.Cleanup(resetRuntimeGlobals)
	t.Setenv("OVERDRIVE_HOME", home)
	t.Setenv("OVERDRIVE_SKIP_EMBED_DOWNLOAD", "0")
	t.Setenv("OVERDRIVE_EMBEDDER", "")

	old := ortSessionOpener
	ortSessionOpener = openORTSessionFFI
	t.Cleanup(func() { ortSessionOpener = old })

	emb, err := buildMiniLMEmbedder(home)
	if err != nil {
		t.Fatalf("MiniLM did not load: %v", err)
	}
	t.Cleanup(func() {
		if c, ok := emb.ort.(interface{ Close() }); ok {
			c.Close()
		}
	})
	login, err := emb.embed("fix the login bug")
	if err != nil {
		t.Fatal(err)
	}
	signIn, err := emb.embed("repair the broken sign-in")
	if err != nil {
		t.Fatal(err)
	}
	cake, err := emb.embed("bake a chocolate cake")
	if err != nil {
		t.Fatal(err)
	}
	if len(login) != miniLMDims {
		t.Fatalf("vector size %d", len(login))
	}
	near, far := cosine(login, signIn), cosine(login, cake)
	if near <= far {
		t.Fatalf("the model does not tell meanings apart: login/sign-in %.3f, login/cake %.3f", near, far)
	}
	t.Logf("login/sign-in %.3f, login/cake %.3f", near, far)
}
