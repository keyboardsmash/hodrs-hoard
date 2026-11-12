package cover

import (
	"bytes"
	"encoding/binary"
	"hash/crc32"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestFit(t *testing.T) {
	tests := []struct{ w, h, maxW, maxH, wantW, wantH int }{
		{100, 150, 400, 600, 100, 150}, // fits: never enlarged
		{800, 1200, 400, 600, 400, 600},
		{4000, 3000, 2000, 2000, 2000, 1500},
		{1000, 3000, 400, 600, 200, 600},
		{5000, 1, 400, 600, 400, 1}, // stays at least one pixel
	}
	for _, tc := range tests {
		w, h := fit(tc.w, tc.h, tc.maxW, tc.maxH)
		if w != tc.wantW || h != tc.wantH {
			t.Errorf("fit(%d, %d, %d, %d) = %d×%d, want %d×%d", tc.w, tc.h, tc.maxW, tc.maxH, w, h, tc.wantW, tc.wantH)
		}
	}
}

func TestSpansAddUp(t *testing.T) {
	for _, tc := range [][2]int{{10, 3}, {2000, 400}, {7, 7}, {1001, 1000}} {
		for i, sp := range spans(tc[0], tc[1]) {
			var sum float32
			for _, w := range sp.weights {
				sum += w
			}
			if sum < 0.999 || sum > 1.001 {
				t.Errorf("spans(%d, %d)[%d] weights add up to %v", tc[0], tc[1], i, sum)
			}
		}
	}
}

func filled(w, h int, c color.RGBA) *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.SetRGBA(x, y, c)
		}
	}
	return img
}

func TestScaleSolidColour(t *testing.T) {
	c := color.RGBA{200, 30, 90, 255}
	out := scale(filled(97, 61, c), 13, 8)
	if b := out.Bounds(); b.Dx() != 13 || b.Dy() != 8 {
		t.Fatalf("size = %v", b)
	}
	for y := 0; y < 8; y++ {
		for x := 0; x < 13; x++ {
			if got := out.RGBAAt(x, y); got != c {
				t.Fatalf("pixel (%d, %d) = %v, want %v", x, y, got, c)
			}
		}
	}
}

func TestScaleAveragesEveryPixel(t *testing.T) {
	// A one-pixel black and white checkerboard. A scaler that skips pixels
	// would give black or white; averaging gives grey.
	img := image.NewRGBA(image.Rect(0, 0, 100, 100))
	for y := 0; y < 100; y++ {
		for x := 0; x < 100; x++ {
			v := uint8(0)
			if (x+y)%2 == 0 {
				v = 255
			}
			img.SetRGBA(x, y, color.RGBA{v, v, v, 255})
		}
	}
	out := scale(img, 10, 10)
	for y := 0; y < 10; y++ {
		for x := 0; x < 10; x++ {
			if g := out.RGBAAt(x, y).R; g < 126 || g > 129 {
				t.Fatalf("pixel (%d, %d) = %d, want about 128", x, y, g)
			}
		}
	}
}

func TestScaleKeepsHalves(t *testing.T) {
	// Left half red, right half blue: scaled to 2×1, each half keeps its colour.
	img := filled(100, 50, color.RGBA{255, 0, 0, 255})
	for y := 0; y < 50; y++ {
		for x := 50; x < 100; x++ {
			img.SetRGBA(x, y, color.RGBA{0, 0, 255, 255})
		}
	}
	out := scale(img, 2, 1)
	if l, r := out.RGBAAt(0, 0), out.RGBAAt(1, 0); l != (color.RGBA{255, 0, 0, 255}) || r != (color.RGBA{0, 0, 255, 255}) {
		t.Errorf("halves = %v, %v", l, r)
	}
}

func TestToRGBAPutsTransparencyOnWhite(t *testing.T) {
	img := image.NewNRGBA(image.Rect(5, 5, 7, 6)) // a non-zero origin, as sub-images have
	img.SetNRGBA(5, 5, color.NRGBA{0, 0, 0, 0})   // fully transparent
	img.SetNRGBA(6, 5, color.NRGBA{255, 0, 0, 255})
	out := toRGBA(img)
	if got := out.RGBAAt(0, 0); got != (color.RGBA{255, 255, 255, 255}) {
		t.Errorf("transparent pixel = %v, want white", got)
	}
	if got := out.RGBAAt(1, 0); got != (color.RGBA{255, 0, 0, 255}) {
		t.Errorf("red pixel = %v", got)
	}
}

// corners is a 3×2 image with a different colour in each corner pixel.
func corners() *image.RGBA {
	img := filled(3, 2, color.RGBA{0, 0, 0, 255})
	img.SetRGBA(0, 0, color.RGBA{255, 0, 0, 255}) // top left: red
	img.SetRGBA(2, 0, color.RGBA{0, 255, 0, 255}) // top right: green
	img.SetRGBA(0, 1, color.RGBA{0, 0, 255, 255}) // bottom left: blue
	img.SetRGBA(2, 1, color.RGBA{255, 255, 0, 255})
	return img
}

func TestOrient(t *testing.T) {
	red := color.RGBA{255, 0, 0, 255}
	// Where the top-left (red) pixel ends up, and the result's size.
	tests := []struct {
		o          int
		w, h, x, y int
	}{
		{1, 3, 2, 0, 0},
		{2, 3, 2, 2, 0},
		{3, 3, 2, 2, 1},
		{4, 3, 2, 0, 1},
		{5, 2, 3, 0, 0},
		{6, 2, 3, 1, 0}, // turned clockwise: top left goes to top right
		{7, 2, 3, 1, 2},
		{8, 2, 3, 0, 2}, // turned anticlockwise: top left goes to bottom left
	}
	for _, tc := range tests {
		out := orient(corners(), tc.o)
		if b := out.Bounds(); b.Dx() != tc.w || b.Dy() != tc.h {
			t.Errorf("orientation %d: size %v, want %d×%d", tc.o, b, tc.w, tc.h)
			continue
		}
		if got := out.RGBAAt(tc.x, tc.y); got != red {
			t.Errorf("orientation %d: (%d, %d) = %v, want red", tc.o, tc.x, tc.y, got)
		}
	}
	// Orientation 6 also: the bottom-left (blue) pixel goes to the top left.
	if got := orient(corners(), 6).RGBAAt(0, 0); got != (color.RGBA{0, 0, 255, 255}) {
		t.Errorf("orientation 6: top left = %v, want blue", got)
	}
}

// exifSegment builds an APP1 segment with an EXIF orientation tag.
func exifSegment(orientation uint16, order binary.ByteOrder) []byte {
	var tiff bytes.Buffer
	if order == binary.LittleEndian {
		tiff.WriteString("II")
	} else {
		tiff.WriteString("MM")
	}
	b2 := make([]byte, 2)
	b4 := make([]byte, 4)
	put2 := func(v uint16) { order.PutUint16(b2, v); tiff.Write(b2) }
	put4 := func(v uint32) { order.PutUint32(b4, v); tiff.Write(b4) }
	put2(42)
	put4(8) // IFD0 right after the header
	put2(2) // two entries: an unrelated one first
	put2(0x010F)
	put2(2)
	put4(4)
	put4(0)
	put2(0x0112)
	put2(3)
	put4(1)
	put2(orientation)
	put2(0)
	put4(0) // no next IFD

	data := append([]byte("Exif\x00\x00"), tiff.Bytes()...)
	seg := []byte{0xFF, 0xE1, 0, 0}
	binary.BigEndian.PutUint16(seg[2:], uint16(len(data)+2))
	return append(seg, data...)
}

// jpegWithExif encodes img as a JPEG and inserts an EXIF segment.
func jpegWithExif(t *testing.T, img image.Image, seg []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, nil); err != nil {
		t.Fatal(err)
	}
	b := buf.Bytes()
	// Right after SOI (the first two bytes).
	return append(append(append([]byte{}, b[:2]...), seg...), b[2:]...)
}

func TestJPEGOrientation(t *testing.T) {
	img := filled(8, 8, color.RGBA{1, 2, 3, 255})
	for _, order := range []binary.ByteOrder{binary.LittleEndian, binary.BigEndian} {
		for _, o := range []uint16{1, 3, 6, 8} {
			data := jpegWithExif(t, img, exifSegment(o, order))
			if got := jpegOrientation(data); got != int(o) {
				t.Errorf("%v orientation %d: got %d", order, o, got)
			}
		}
	}
	var plain bytes.Buffer
	jpeg.Encode(&plain, img, nil)
	cases := map[string][]byte{
		"no EXIF":     plain.Bytes(),
		"empty":       nil,
		"not a JPEG":  []byte("hello world"),
		"bad value":   jpegWithExif(t, img, exifSegment(9, binary.BigEndian)),
		"truncated":   jpegWithExif(t, img, exifSegment(6, binary.BigEndian))[:30],
		"huge length": {0xFF, 0xD8, 0xFF, 0xE1, 0xFF, 0xFF, 'E', 'x'},
	}
	for name, data := range cases {
		if got := jpegOrientation(data); got != 1 {
			t.Errorf("%s: got %d, want 1", name, got)
		}
	}
}

func encodePNG(t *testing.T, img image.Image) []byte {
	t.Helper()
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func decodeFile(t *testing.T, path string) image.Image {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	img, format, err := image.Decode(f)
	if err != nil || format != "jpeg" {
		t.Fatalf("%s: format %q, err %v", path, format, err)
	}
	return img
}

func TestSave(t *testing.T) {
	s := NewStore(t.TempDir())
	hash, err := s.Save(encodePNG(t, filled(3000, 4500, color.RGBA{10, 120, 200, 255})))
	if err != nil {
		t.Fatal(err)
	}
	if !ValidHash(hash) {
		t.Fatalf("hash %q", hash)
	}
	want := filepath.Join(s.dir, hash[:2], hash[2:4], hash+".jpg")
	if s.Path(hash, false) != want {
		t.Errorf("path = %q, want %q", s.Path(hash, false), want)
	}
	if b := decodeFile(t, s.Path(hash, false)).Bounds(); b.Dx() != 1333 || b.Dy() != 2000 {
		t.Errorf("cover size = %v, want 1333×2000", b)
	}
	if b := decodeFile(t, s.Path(hash, true)).Bounds(); b.Dx() != 400 || b.Dy() != 600 {
		t.Errorf("thumbnail size = %v, want 400×600", b)
	}
	if !s.Has(hash) {
		t.Error("Has = false")
	}

	// The same picture again gives the same file.
	again, err := s.Save(encodePNG(t, filled(3000, 4500, color.RGBA{10, 120, 200, 255})))
	if err != nil || again != hash {
		t.Errorf("second save = %q, %v; want the same hash", again, err)
	}
	// No temporary files are left behind.
	leftovers, _ := filepath.Glob(filepath.Join(s.dir, "*", "*", ".tmp-*"))
	if len(leftovers) > 0 {
		t.Errorf("temporary files left: %v", leftovers)
	}
}

func TestSaveSmallImageKeepsSize(t *testing.T) {
	s := NewStore(t.TempDir())
	hash, err := s.Save(encodePNG(t, filled(120, 180, color.RGBA{0, 0, 0, 255})))
	if err != nil {
		t.Fatal(err)
	}
	for _, thumb := range []bool{false, true} {
		if b := decodeFile(t, s.Path(hash, thumb)).Bounds(); b.Dx() != 120 || b.Dy() != 180 {
			t.Errorf("thumb=%v: size %v, want 120×180", thumb, b)
		}
	}
}

func TestSaveTurnsPhonePhotos(t *testing.T) {
	// A landscape picture marked "turn 90° clockwise" is stored portrait.
	s := NewStore(t.TempDir())
	hash, err := s.Save(jpegWithExif(t, filled(300, 200, color.RGBA{50, 50, 50, 255}), exifSegment(6, binary.LittleEndian)))
	if err != nil {
		t.Fatal(err)
	}
	if b := decodeFile(t, s.Path(hash, false)).Bounds(); b.Dx() != 200 || b.Dy() != 300 {
		t.Errorf("size = %v, want 200×300", b)
	}
}

func TestSaveRejects(t *testing.T) {
	s := NewStore(t.TempDir())
	gif := []byte("GIF89a\x01\x00\x01\x00\x00\x00\x00;")
	// A PNG header claiming 10000×10000 pixels: refused before decoding.
	huge := encodePNG(t, filled(1, 1, color.RGBA{}))
	binary.BigEndian.PutUint32(huge[16:], 10000)
	binary.BigEndian.PutUint32(huge[20:], 10000)
	// The header chunk ends with a checksum of its type and data.
	binary.BigEndian.PutUint32(huge[29:], crc32.ChecksumIEEE(huge[12:29]))
	png := encodePNG(t, filled(50, 50, color.RGBA{1, 2, 3, 255}))

	tests := []struct {
		name string
		data []byte
		want error
	}{
		{"text", []byte("not an image at all"), ErrFormat},
		{"empty", nil, ErrFormat},
		{"gif", gif, ErrFormat},
		{"too many pixels", huge, ErrTooLarge},
		{"truncated", png[:len(png)/2], ErrDamaged},
	}
	for _, tc := range tests {
		if _, err := s.Save(tc.data); err != tc.want {
			t.Errorf("%s: err = %v, want %v", tc.name, err, tc.want)
		}
		if Message(tc.want) == "" {
			t.Errorf("%s: no message for %v", tc.name, tc.want)
		}
	}
	if entries, _ := os.ReadDir(s.dir); len(entries) > 0 {
		t.Errorf("files written for rejected images: %v", entries)
	}
}

func TestValidHash(t *testing.T) {
	ok := "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	if !ValidHash(ok) {
		t.Error("valid hash refused")
	}
	for _, bad := range []string{"", "abc", ok[:63], ok + "0", "0123456789ABCDEF" + ok[16:], "../" + ok[3:]} {
		if ValidHash(bad) {
			t.Errorf("ValidHash(%q) = true", bad)
		}
	}
}

func TestSweep(t *testing.T) {
	s := NewStore(t.TempDir())
	now := time.Now()
	s.now = func() time.Time { return now }

	if n, err := s.Sweep(nil, time.Hour); n != 0 || err != nil {
		t.Fatalf("sweep without folder = %d, %v", n, err)
	}

	img := func(c uint8) []byte { return encodePNG(t, filled(10, 10, color.RGBA{c, c, c, 255})) }
	used, _ := s.Save(img(1))
	old, _ := s.Save(img(2))
	recent, _ := s.Save(img(3))
	// A leftover temporary file, and a file that isn't ours.
	tmp := filepath.Join(s.dir, used[:2], used[2:4], ".tmp-123")
	os.WriteFile(tmp, []byte("x"), 0o644)
	foreign := filepath.Join(s.dir, "README")
	os.WriteFile(foreign, []byte("x"), 0o644)

	// Two hours pass; "recent" is used by a form in the meantime.
	now = now.Add(2 * time.Hour)
	twoHoursAgo := now.Add(-2 * time.Hour)
	os.Chtimes(tmp, twoHoursAgo, twoHoursAgo)
	os.Chtimes(foreign, twoHoursAgo, twoHoursAgo)
	if !s.Has(recent) {
		t.Fatal("Has(recent) = false")
	}

	n, err := s.Sweep(map[string]bool{used: true}, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if n != 3 { // old's cover and thumbnail, and the temporary file
		t.Errorf("removed %d files, want 3", n)
	}
	exists := func(p string) bool { _, err := os.Stat(p); return err == nil }
	for _, h := range []string{used, recent} {
		if !exists(s.Path(h, false)) || !exists(s.Path(h, true)) {
			t.Errorf("%s removed", h[:8])
		}
	}
	if exists(s.Path(old, false)) || exists(s.Path(old, true)) || s.Has(old) {
		t.Error("unused old cover kept")
	}
	if exists(tmp) {
		t.Error("temporary file kept")
	}
	if !exists(foreign) {
		t.Error("foreign file removed")
	}
}
