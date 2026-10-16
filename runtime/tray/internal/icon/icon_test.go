package icon

import (
	"bytes"
	"image/png"
	"testing"
)

func TestPNGDecodes(t *testing.T) {
	img, err := png.Decode(bytes.NewReader(PNG()))
	if err != nil {
		t.Fatal(err)
	}
	if img.Bounds().Dx() != 32 || img.Bounds().Dy() != 32 {
		t.Fatalf("bounds %v", img.Bounds())
	}
}

func TestICOHeader(t *testing.T) {
	raw := ICO()
	if len(raw) < 22 || raw[0] != 0 || raw[1] != 0 || raw[2] != 1 || raw[3] != 0 {
		t.Fatalf("header %v", raw[:6])
	}
}
