// Package icon draws a small tray mark without linking the experience module.
package icon

import (
	"bytes"
	"encoding/binary"
	"image"
	"image/color"
	_ "image/gif"
	_ "image/jpeg"
	"image/png"
	"math"
	"os"
	"sync"
)

var (
	oncePNG  sync.Once
	pngBytes []byte
	onceICO  sync.Once
	icoBytes []byte
)

// PNG is a 32x32 mark for Linux and macOS.
func PNG() []byte {
	oncePNG.Do(func() {
		pngBytes = encodePNG(mark())
	})
	return pngBytes
}

// ICO is a 32-bit icon for the Windows notification area.
func ICO() []byte {
	onceICO.Do(func() {
		icoBytes = encodeICO(mark())
	})
	return icoBytes
}

func mark() *image.NRGBA {
	const s = 32
	img := image.NewNRGBA(image.Rect(0, 0, s, s))
	cx, cy := 15.5, 15.5
	for y := 0; y < s; y++ {
		for x := 0; x < s; x++ {
			d := math.Hypot(float64(x)-cx, float64(y)-cy)
			if (d <= 13 && d >= 8.5) || d <= 3 {
				img.SetNRGBA(x, y, color.NRGBA{R: 0x3D, G: 0xDC, B: 0x97, A: 0xFF})
			}
		}
	}
	return img
}

func encodePNG(img image.Image) []byte {
	var buf bytes.Buffer
	_ = png.Encode(&buf, img)
	return buf.Bytes()
}

// FromPath decodes an image file and returns 32×32 PNG and ICO bytes for the tray.
func FromPath(path string) (png []byte, ico []byte, err error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, nil, err
	}
	img, _, err := image.Decode(bytes.NewReader(raw))
	if err != nil {
		return nil, nil, err
	}
	nrgba := resize32(img)
	return encodePNG(nrgba), encodeICO(nrgba), nil
}

func resize32(src image.Image) *image.NRGBA {
	bounds := src.Bounds()
	w, h := bounds.Dx(), bounds.Dy()
	if w <= 0 || h <= 0 {
		return mark()
	}
	dst := image.NewNRGBA(image.Rect(0, 0, 32, 32))
	for y := 0; y < 32; y++ {
		for x := 0; x < 32; x++ {
			sx := bounds.Min.X + x*w/32
			sy := bounds.Min.Y + y*h/32
			dst.Set(x, y, src.At(sx, sy))
		}
	}
	return dst
}

func encodeICO(img *image.NRGBA) []byte {
	const size = 32
	xor := make([]byte, size*size*4)
	for y := 0; y < size; y++ {
		srcY := size - 1 - y
		for x := 0; x < size; x++ {
			c := img.NRGBAAt(x, srcY)
			i := (y*size + x) * 4
			xor[i] = c.B
			xor[i+1] = c.G
			xor[i+2] = c.R
			xor[i+3] = c.A
		}
	}
	maskRow := 4
	mask := make([]byte, size*maskRow)
	header := make([]byte, 40)
	binary.LittleEndian.PutUint32(header[0:], 40)
	binary.LittleEndian.PutUint32(header[4:], size)
	binary.LittleEndian.PutUint32(header[8:], size*2)
	binary.LittleEndian.PutUint16(header[12:], 1)
	binary.LittleEndian.PutUint16(header[14:], 32)
	imageSize := uint32(len(header) + len(xor) + len(mask))
	dir := make([]byte, 6+16)
	binary.LittleEndian.PutUint16(dir[2:], 1)
	dir[4] = size
	dir[5] = size
	binary.LittleEndian.PutUint16(dir[6+2:], 1)
	binary.LittleEndian.PutUint16(dir[6+4:], 32)
	binary.LittleEndian.PutUint32(dir[6+8:], imageSize)
	binary.LittleEndian.PutUint32(dir[6+12:], 22)
	out := append(dir, header...)
	out = append(out, xor...)
	out = append(out, mask...)
	return out
}
