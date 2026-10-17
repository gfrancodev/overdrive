package icon

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
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

// trayARGB mirrors fyne.io/systray's Linux conversion: the low byte of each premultiplied 16-bit channel.
func trayARGB(c color.Color) [4]byte {
	r, g, b, a := c.RGBA()
	return [4]byte{byte(a), byte(r), byte(g), byte(b)}
}

func TestFromPathKeepsLogoColorsThroughTheLinuxTray(t *testing.T) {
	src := image.NewNRGBA(image.Rect(0, 0, 32, 32))
	for y := 0; y < 32; y++ {
		for x := 0; x < 32; x++ {
			src.SetNRGBA(x, y, color.NRGBA{R: 89, G: 22, B: 111, A: 254})
		}
	}
	src.SetNRGBA(0, 0, color.NRGBA{R: 255, G: 0, B: 255, A: 1})
	var buf bytes.Buffer
	if err := png.Encode(&buf, src); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "logo.png")
	if err := os.WriteFile(path, buf.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
	trayPNG, _, err := FromPath(path)
	if err != nil {
		t.Fatal(err)
	}
	img, err := png.Decode(bytes.NewReader(trayPNG))
	if err != nil {
		t.Fatal(err)
	}
	if got := trayARGB(img.At(16, 16)); got != [4]byte{255, 89, 22, 111} {
		t.Fatalf("a nearly opaque logo pixel must reach the tray unchanged, got ARGB %v", got)
	}
	if got := trayARGB(img.At(0, 0)); got != [4]byte{} {
		t.Fatalf("a nearly transparent pixel must reach the tray fully transparent, got ARGB %v", got)
	}
}

func TestICOHeader(t *testing.T) {
	raw := ICO()
	if len(raw) < 22 || raw[0] != 0 || raw[1] != 0 || raw[2] != 1 || raw[3] != 0 {
		t.Fatalf("header %v", raw[:6])
	}
}
