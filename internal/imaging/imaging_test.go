package imaging

import (
	"bytes"
	"encoding/binary"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"testing"
)

func TestOptimizeImageDownscaleAndFormat(t *testing.T) {
	// Create a 2400x1200 RGBA PNG image with transparency
	src := image.NewRGBA(image.Rect(0, 0, 2400, 1200))
	for y := 0; y < 1200; y++ {
		for x := 0; x < 2400; x++ {
			src.Set(x, y, color.RGBA{R: 10, G: 100, B: 200, A: 128})
		}
	}
	var pngBuf bytes.Buffer
	if err := png.Encode(&pngBuf, src); err != nil {
		t.Fatalf("failed to encode test PNG: %v", err)
	}

	optBytes, err := OptimizeImage(pngBuf.Bytes(), 1600, 85)
	if err != nil {
		t.Fatalf("OptimizeImage returned unexpected error: %v", err)
	}

	decoded, format, err := image.Decode(bytes.NewReader(optBytes))
	if err != nil {
		t.Fatalf("failed to decode optimized image: %v", err)
	}
	if format != "jpeg" {
		t.Errorf("expected output format jpeg, got %s", format)
	}

	bounds := decoded.Bounds()
	if bounds.Dx() != 1600 || bounds.Dy() != 800 {
		t.Errorf("expected dimensions 1600x800, got %dx%d", bounds.Dx(), bounds.Dy())
	}
}

func TestEXIFOrientationParsingAndRotation(t *testing.T) {
	// Create a 400x200 image
	src := image.NewRGBA(image.Rect(0, 0, 400, 200))
	var jpgBuf bytes.Buffer
	if err := jpeg.Encode(&jpgBuf, src, &jpeg.Options{Quality: 90}); err != nil {
		t.Fatalf("failed to encode jpeg: %v", err)
	}
	raw := jpgBuf.Bytes()

	// Inject a minimal APP1 EXIF header with Orientation = 6 (Rotate 90 CW)
	// APP1 marker: FF E1, length, "Exif\x00\x00", Little Endian TIFF header "II", 0x002A, offset 8, 1 entry, tag 0x0112, type 3 (SHORT), count 1, value 6
	exifPayload := []byte{
		'E', 'x', 'i', 'f', 0, 0,
		'I', 'I', 0x2A, 0x00, 0x08, 0x00, 0x00, 0x00, // TIFF header
		0x01, 0x00, // 1 IFD entry
		0x12, 0x01, // Tag 0x0112 (Orientation)
		0x03, 0x00, // Type SHORT
		0x01, 0x00, 0x00, 0x00, // Count 1
		0x06, 0x00, 0x00, 0x00, // Value = 6 (90 CW)
		0x00, 0x00, 0x00, 0x00, // Next IFD
	}
	segLen := uint16(len(exifPayload) + 2)
	var withExif bytes.Buffer
	withExif.Write(raw[:2]) // FF D8
	withExif.Write([]byte{0xFF, 0xE1})
	_ = binary.Write(&withExif, binary.BigEndian, segLen)
	withExif.Write(exifPayload)
	withExif.Write(raw[2:])

	if orient := readEXIFOrientation(withExif.Bytes()); orient != 6 {
		t.Fatalf("expected EXIF orientation 6, got %d", orient)
	}

	optBytes, err := OptimizeImage(withExif.Bytes(), 1600, 85)
	if err != nil {
		t.Fatalf("OptimizeImage failed: %v", err)
	}
	decoded, _, err := image.Decode(bytes.NewReader(optBytes))
	if err != nil {
		t.Fatalf("decode failed: %v", err)
	}
	// 400x200 rotated 90 CW becomes 200x400
	if decoded.Bounds().Dx() != 200 || decoded.Bounds().Dy() != 400 {
		t.Errorf("expected rotated dimensions 200x400, got %dx%d", decoded.Bounds().Dx(), decoded.Bounds().Dy())
	}
}

func TestOptimizeImageInvalidBytesFallback(t *testing.T) {
	garbage := []byte("not-an-image")
	out, err := OptimizeImage(garbage, 1600, 85)
	if err == nil {
		t.Errorf("expected error on invalid image bytes")
	}
	if !bytes.Equal(out, garbage) {
		t.Errorf("expected original bytes returned on fallback")
	}
}
