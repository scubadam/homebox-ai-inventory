package imaging

import (
	"bytes"
	"encoding/binary"
	"image"
	"image/color"
	"image/jpeg"
	_ "image/png" // Register PNG decoder
	"math"

	"golang.org/x/image/draw"
)

// OptimizeImage decodes an input image (JPEG/PNG), applies EXIF orientation if present,
// downscales it so that its largest dimension is at most maxDim, composites any alpha channel
// onto a white RGB background, and re-encodes as an optimized JPEG.
// If decoding fails, the original bytes are returned unchanged as a safe fallback.
func OptimizeImage(input []byte, maxDim int, quality int) ([]byte, error) {
	if maxDim <= 0 {
		maxDim = 1600
	}
	if quality <= 0 || quality > 100 {
		quality = 85
	}

	img, _, err := image.Decode(bytes.NewReader(input))
	if err != nil {
		return input, err
	}

	// Apply EXIF orientation if present in JPEG header
	orientation := readEXIFOrientation(input)
	if orientation > 1 {
		img = applyOrientation(img, orientation)
	}

	bounds := img.Bounds()
	w := bounds.Dx()
	h := bounds.Dy()

	targetW, targetH := w, h
	if w > maxDim || h > maxDim {
		if w >= h {
			targetW = maxDim
			targetH = int(math.Round(float64(h) * float64(maxDim) / float64(w)))
		} else {
			targetH = maxDim
			targetW = int(math.Round(float64(w) * float64(maxDim) / float64(h)))
		}
		if targetW < 1 {
			targetW = 1
		}
		if targetH < 1 {
			targetH = 1
		}
	}

	// Create clean RGB destination image (compositing transparency over white)
	dst := image.NewRGBA(image.Rect(0, 0, targetW, targetH))
	draw.Draw(dst, dst.Bounds(), &image.Uniform{C: color.White}, image.Point{}, draw.Src)

	if targetW != w || targetH != h {
		draw.CatmullRom.Scale(dst, dst.Bounds(), img, bounds, draw.Over, nil)
	} else {
		draw.Draw(dst, dst.Bounds(), img, bounds.Min, draw.Over)
	}

	var out bytes.Buffer
	if err := jpeg.Encode(&out, dst, &jpeg.Options{Quality: quality}); err != nil {
		return input, err
	}
	return out.Bytes(), nil
}

// readEXIFOrientation inspects a JPEG byte slice for an APP1 EXIF segment and returns
// the EXIF Orientation tag (1-8), or 1 if not found.
func readEXIFOrientation(data []byte) int {
	if len(data) < 4 || data[0] != 0xFF || data[1] != 0xD8 {
		return 1
	}
	offset := 2
	for offset+4 <= len(data) {
		if data[offset] != 0xFF {
			break
		}
		marker := data[offset+1]
		if marker == 0xDA || marker == 0xD9 { // SOS or EOI
			break
		}
		segLen := int(binary.BigEndian.Uint16(data[offset+2 : offset+4]))
		if segLen < 2 || offset+2+segLen > len(data) {
			break
		}
		// Check APP1 (0xFFE1) with "Exif\x00\x00"
		if marker == 0xE1 && segLen >= 8 {
			seg := data[offset+4 : offset+2+segLen]
			if len(seg) > 14 && string(seg[:6]) == "Exif\x00\x00" {
				return parseTIFFOrientation(seg[6:])
			}
		}
		offset += 2 + segLen
	}
	return 1
}

func parseTIFFOrientation(tiff []byte) int {
	if len(tiff) < 8 {
		return 1
	}
	var bo binary.ByteOrder
	switch string(tiff[:2]) {
	case "II":
		bo = binary.LittleEndian
	case "MM":
		bo = binary.BigEndian
	default:
		return 1
	}
	if bo.Uint16(tiff[2:4]) != 0x002A {
		return 1
	}
	ifdOffset := int(bo.Uint32(tiff[4:8]))
	if ifdOffset < 8 || ifdOffset+2 > len(tiff) {
		return 1
	}
	numEntries := int(bo.Uint16(tiff[ifdOffset : ifdOffset+2]))
	pos := ifdOffset + 2
	for i := 0; i < numEntries; i++ {
		if pos+12 > len(tiff) {
			break
		}
		tag := bo.Uint16(tiff[pos : pos+2])
		if tag == 0x0112 { // Orientation tag
			val := int(bo.Uint16(tiff[pos+8 : pos+10]))
			if val >= 1 && val <= 8 {
				return val
			}
			return 1
		}
		pos += 12
	}
	return 1
}

func applyOrientation(src image.Image, orientation int) image.Image {
	b := src.Bounds()
	w, h := b.Dx(), b.Dy()

	switch orientation {
	case 2: // Flip horizontal
		dst := image.NewRGBA(image.Rect(0, 0, w, h))
		for y := 0; y < h; y++ {
			for x := 0; x < w; x++ {
				dst.Set(w-1-x, y, src.At(b.Min.X+x, b.Min.Y+y))
			}
		}
		return dst
	case 3: // Rotate 180
		dst := image.NewRGBA(image.Rect(0, 0, w, h))
		for y := 0; y < h; y++ {
			for x := 0; x < w; x++ {
				dst.Set(w-1-x, h-1-y, src.At(b.Min.X+x, b.Min.Y+y))
			}
		}
		return dst
	case 4: // Flip vertical
		dst := image.NewRGBA(image.Rect(0, 0, w, h))
		for y := 0; y < h; y++ {
			for x := 0; x < w; x++ {
				dst.Set(x, h-1-y, src.At(b.Min.X+x, b.Min.Y+y))
			}
		}
		return dst
	case 5: // Transpose
		dst := image.NewRGBA(image.Rect(0, 0, h, w))
		for y := 0; y < h; y++ {
			for x := 0; x < w; x++ {
				dst.Set(y, x, src.At(b.Min.X+x, b.Min.Y+y))
			}
		}
		return dst
	case 6: // Rotate 90 CW
		dst := image.NewRGBA(image.Rect(0, 0, h, w))
		for y := 0; y < h; y++ {
			for x := 0; x < w; x++ {
				dst.Set(h-1-y, x, src.At(b.Min.X+x, b.Min.Y+y))
			}
		}
		return dst
	case 7: // Transverse
		dst := image.NewRGBA(image.Rect(0, 0, h, w))
		for y := 0; y < h; y++ {
			for x := 0; x < w; x++ {
				dst.Set(h-1-y, w-1-x, src.At(b.Min.X+x, b.Min.Y+y))
			}
		}
		return dst
	case 8: // Rotate 270 CW
		dst := image.NewRGBA(image.Rect(0, 0, h, w))
		for y := 0; y < h; y++ {
			for x := 0; x < w; x++ {
				dst.Set(y, w-1-x, src.At(b.Min.X+x, b.Min.Y+y))
			}
		}
		return dst
	default:
		return src
	}
}
