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

func TestCropNormalizedBox(t *testing.T) {
	// 1000x1000 image with a distinct red region in [200, 300, 600, 700]
	src := image.NewRGBA(image.Rect(0, 0, 1000, 1000))
	for y := 0; y < 1000; y++ {
		for x := 0; x < 1000; x++ {
			if y >= 200 && y < 600 && x >= 300 && x < 700 {
				src.Set(x, y, color.RGBA{R: 220, G: 30, B: 30, A: 255})
			} else {
				src.Set(x, y, color.RGBA{R: 240, G: 240, B: 240, A: 255})
			}
		}
	}
	var jpgBuf bytes.Buffer
	if err := jpeg.Encode(&jpgBuf, src, &jpeg.Options{Quality: 95}); err != nil {
		t.Fatalf("failed to encode jpeg: %v", err)
	}

	// Crop [200, 300, 600, 700] (400x400) with 0 padding and maxDim=800
	cropped, err := CropNormalizedBox(jpgBuf.Bytes(), [4]int{200, 300, 600, 700}, 0, 800, 90)
	if err != nil {
		t.Fatalf("CropNormalizedBox failed: %v", err)
	}
	decoded, _, err := image.Decode(bytes.NewReader(cropped))
	if err != nil {
		t.Fatalf("failed to decode cropped jpeg: %v", err)
	}
	if decoded.Bounds().Dx() != 400 || decoded.Bounds().Dy() != 400 {
		t.Errorf("expected 400x400 crop, got %dx%d", decoded.Bounds().Dx(), decoded.Bounds().Dy())
	}
	center := decoded.At(200, 200)
	r, g, b, _ := center.RGBA()
	if r>>8 < 180 || g>>8 > 80 || b>>8 > 80 {
		t.Errorf("expected red center pixel, got RGB=(%d,%d,%d)", r>>8, g>>8, b>>8)
	}

	// Test downscaling + padding clamping near edge
	scaled, err := CropNormalizedBox(jpgBuf.Bytes(), [4]int{0, 0, 900, 900}, 0.1, 300, 85)
	if err != nil {
		t.Fatalf("CropNormalizedBox with padding+scale failed: %v", err)
	}
	decodedScaled, _, err := image.Decode(bytes.NewReader(scaled))
	if err != nil {
		t.Fatalf("failed to decode scaled crop: %v", err)
	}
	if decodedScaled.Bounds().Dx() != 300 || decodedScaled.Bounds().Dy() != 300 {
		t.Errorf("expected 300x300 scaled crop, got %dx%d", decodedScaled.Bounds().Dx(), decodedScaled.Bounds().Dy())
	}

	// Degenerate / inverted boxes must error
	for _, bad := range [][4]int{{0, 0, 0, 0}, {500, 500, 400, 600}, {100, 100, 102, 500}} {
		if _, err := CropNormalizedBox(jpgBuf.Bytes(), bad, 0.05, 800, 85); err == nil {
			t.Errorf("expected error for invalid box %v", bad)
		}
	}
}
