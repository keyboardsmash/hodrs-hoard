package cover

import (
	"encoding/binary"
	"image"
)

// Phone cameras store pictures as the sensor saw them and add an EXIF
// "orientation" tag saying how to turn them. Browsers honour the tag, but
// Go's JPEG decoder ignores it, and re-encoding drops it. So we read the
// tag ourselves and turn the pixels before saving.

// jpegOrientation returns the EXIF orientation of a JPEG file: 1 (as is)
// to 8. Anything missing or unreadable gives 1.
func jpegOrientation(data []byte) int {
	// A JPEG file is a list of segments: 0xFF, a marker byte, a 2-byte
	// big-endian length (which counts itself) and the data. It starts with
	// SOI (FFD8). EXIF lives in an APP1 (FFE1) segment near the start.
	if len(data) < 4 || data[0] != 0xFF || data[1] != 0xD8 {
		return 1
	}
	pos := 2
	for pos+4 <= len(data) {
		if data[pos] != 0xFF {
			return 1
		}
		marker := data[pos+1]
		if marker == 0xDA || marker == 0xD9 { // start of image data, or end: no EXIF before it
			return 1
		}
		n := int(binary.BigEndian.Uint16(data[pos+2:]))
		if n < 2 || pos+2+n > len(data) {
			return 1
		}
		seg := data[pos+4 : pos+2+n]
		if marker == 0xE1 && len(seg) > 6 && string(seg[:6]) == "Exif\x00\x00" {
			return tiffOrientation(seg[6:])
		}
		pos += 2 + n
	}
	return 1
}

// tiffOrientation reads tag 0x0112 from the first directory (IFD0) of the
// TIFF structure inside EXIF.
func tiffOrientation(t []byte) int {
	if len(t) < 8 {
		return 1
	}
	// "II" means little-endian (Intel), "MM" big-endian (Motorola).
	// binary.ByteOrder is an interface both orders implement.
	var order binary.ByteOrder
	switch string(t[:2]) {
	case "II":
		order = binary.LittleEndian
	case "MM":
		order = binary.BigEndian
	default:
		return 1
	}
	ifd := int(order.Uint32(t[4:]))
	if ifd < 8 || ifd+2 > len(t) {
		return 1
	}
	count := int(order.Uint16(t[ifd:]))
	for i := 0; i < count; i++ {
		e := ifd + 2 + i*12 // each entry: tag(2) type(2) count(4) value(4)
		if e+12 > len(t) {
			return 1
		}
		if order.Uint16(t[e:]) != 0x0112 {
			continue
		}
		if order.Uint16(t[e+2:]) != 3 { // type 3 = SHORT
			return 1
		}
		if o := int(order.Uint16(t[e+8:])); o >= 1 && o <= 8 {
			return o
		}
		return 1
	}
	return 1
}

// orient turns and flips src so that it shows the right way up, given its
// EXIF orientation. Orientations 5–8 swap width and height.
func orient(src *image.RGBA, o int) *image.RGBA {
	if o <= 1 || o > 8 {
		return src
	}
	w, h := src.Bounds().Dx(), src.Bounds().Dy()
	dw, dh := w, h
	if o >= 5 {
		dw, dh = h, w
	}
	dst := image.NewRGBA(image.Rect(0, 0, dw, dh))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			// Where source pixel (x, y) goes in the result.
			var nx, ny int
			switch o {
			case 2: // mirrored
				nx, ny = w-1-x, y
			case 3: // upside down
				nx, ny = w-1-x, h-1-y
			case 4: // mirrored and upside down
				nx, ny = x, h-1-y
			case 5: // mirrored along the top-left/bottom-right diagonal
				nx, ny = y, x
			case 6: // camera held turned right: turn 90° clockwise
				nx, ny = h-1-y, x
			case 7: // mirrored along the other diagonal
				nx, ny = h-1-y, w-1-x
			case 8: // camera held turned left: turn 90° anticlockwise
				nx, ny = y, w-1-x
			}
			s := src.Pix[y*src.Stride+x*4:]
			d := dst.Pix[ny*dst.Stride+nx*4:]
			copy(d[:4], s[:4])
		}
	}
	return dst
}
