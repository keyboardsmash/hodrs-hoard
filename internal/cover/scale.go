package cover

import (
	"image"
	"image/draw"
	"math"
)

// The standard library decodes and encodes images but has no scaler, so
// this file has a small one. Covers are only ever made smaller, which
// allows a simple, good-looking method: area averaging. Each output pixel
// is the average of the source pixels it covers; source pixels that only
// partly fall inside it count for that part.
//
// Scaling runs in two passes (first the width, then the height), which
// gives the same result as averaging whole rectangles but is much faster.

// fit returns the size of a w×h image shrunk to fit inside maxW×maxH,
// keeping its proportions. Images that already fit keep their size.
func fit(w, h, maxW, maxH int) (int, int) {
	f := math.Min(float64(maxW)/float64(w), float64(maxH)/float64(h))
	if f >= 1 {
		return w, h
	}
	// max(1, ...) keeps a very thin image at least one pixel wide.
	return max(1, int(math.Round(float64(w)*f))), max(1, int(math.Round(float64(h)*f)))
}

// toRGBA copies any decoded image into a plain RGBA image starting at
// (0, 0). Transparent parts are put on white, since JPEG has no
// transparency. draw.Draw has fast paths for the types the JPEG and PNG
// decoders return.
func toRGBA(src image.Image) *image.RGBA {
	b := src.Bounds()
	dst := image.NewRGBA(image.Rect(0, 0, b.Dx(), b.Dy()))
	// An interface check: does src have an Opaque method? All the standard
	// image types do; it is true when no pixel is transparent.
	if o, ok := src.(interface{ Opaque() bool }); ok && o.Opaque() {
		draw.Draw(dst, dst.Bounds(), src, b.Min, draw.Src)
		return dst
	}
	draw.Draw(dst, dst.Bounds(), image.White, image.Point{}, draw.Src)
	draw.Draw(dst, dst.Bounds(), src, b.Min, draw.Over)
	return dst
}

// span is how one output pixel along an axis is made: from the source
// pixels first, first+1, ... with the given weights (which add up to 1).
type span struct {
	first   int
	weights []float32
}

// spans works out the spans for shrinking an axis from n to m pixels.
func spans(n, m int) []span {
	scale := float64(n) / float64(m) // source pixels per output pixel, ≥ 1
	out := make([]span, m)
	for i := range out { // "range out" counts i from 0 to len(out)-1
		start := float64(i) * scale
		end := start + scale
		first := int(start)
		last := min(int(math.Ceil(end)), n) // one past the last source pixel
		w := make([]float32, last-first)
		for j := first; j < last; j++ {
			// The part of source pixel j (from j to j+1) inside [start, end).
			covered := math.Min(end, float64(j+1)) - math.Max(start, float64(j))
			w[j-first] = float32(covered / scale)
		}
		out[i] = span{first: first, weights: w}
	}
	return out
}

// scale shrinks src to w×h. It must not be asked to enlarge.
func scale(src *image.RGBA, w, h int) *image.RGBA {
	sw, sh := src.Bounds().Dx(), src.Bounds().Dy()
	if w == sw && h == sh {
		return src
	}
	// Width first: sw×sh → w×sh.
	tmp := image.NewRGBA(image.Rect(0, 0, w, sh))
	xs := spans(sw, w)
	for y := 0; y < sh; y++ {
		// Pix is the raw bytes, 4 per pixel (R, G, B, A), row after row;
		// Stride is the number of bytes per row.
		row := src.Pix[y*src.Stride:]
		out := tmp.Pix[y*tmp.Stride:]
		for x, sp := range xs {
			var r, g, b, a float32
			for k, wt := range sp.weights {
				p := row[(sp.first+k)*4:]
				r += wt * float32(p[0])
				g += wt * float32(p[1])
				b += wt * float32(p[2])
				a += wt * float32(p[3])
			}
			setPixel(out[x*4:], r, g, b, a)
		}
	}
	// Then height: w×sh → w×h.
	dst := image.NewRGBA(image.Rect(0, 0, w, h))
	ys := spans(sh, h)
	for y, sp := range ys {
		out := dst.Pix[y*dst.Stride:]
		for x := 0; x < w; x++ {
			var r, g, b, a float32
			for k, wt := range sp.weights {
				p := tmp.Pix[(sp.first+k)*tmp.Stride+x*4:]
				r += wt * float32(p[0])
				g += wt * float32(p[1])
				b += wt * float32(p[2])
				a += wt * float32(p[3])
			}
			setPixel(out[x*4:], r, g, b, a)
		}
	}
	return dst
}

// setPixel rounds the averaged channels back to bytes.
func setPixel(p []uint8, r, g, b, a float32) {
	p[0], p[1], p[2], p[3] = toByte(r), toByte(g), toByte(b), toByte(a)
}

func toByte(v float32) uint8 {
	// Weights add up to 1 only approximately, so clamp.
	return uint8(min(max(v+0.5, 0), 255))
}
