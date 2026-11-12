// Package cover stores cover images (spec 3.5).
//
// Every cover is re-encoded as a JPEG, at most MaxSide pixels on its long
// edge, and named by the SHA-256 hash of that JPEG:
//
//	covers/ab/cd/<hash>.jpg        the cover
//	covers/ab/cd/<hash>-thumb.jpg  its thumbnail
//
// where ab and cd are the hash's first four characters, so no folder gets
// too many files. The same picture uploaded twice is stored once. Items
// refer to a cover by its hash; files no item uses are removed by Sweep.
package cover

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"image"
	"image/jpeg"
	_ "image/png" // registers the PNG decoder with image.Decode; "_" imports only for that side effect
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// Limits (spec 3.5).
const (
	// MaxFileBytes is the largest upload accepted.
	MaxFileBytes = 15 << 20 // 15 MB; << shifts left, so this is 15 × 1024 × 1024
	// MaxPixels guards against images that are small files but huge
	// pictures, which would need gigabytes of memory to decode.
	MaxPixels = 50_000_000
	// MaxSide is the longest edge of a stored cover.
	MaxSide = 2000
	// Thumbnails fit inside ThumbWidth×ThumbHeight.
	ThumbWidth, ThumbHeight = 400, 600

	quality      = 90
	thumbQuality = 82
)

// Errors from Save that the user can fix; see Message.
var (
	ErrFormat   = errors.New("cover: not a JPEG or PNG image")
	ErrDamaged  = errors.New("cover: damaged image")
	ErrTooLarge = errors.New("cover: image has too many pixels")
)

// Message returns a user-facing text for an error from Save, or "" for
// errors that are the server's problem.
func Message(err error) string {
	switch {
	case errors.Is(err, ErrFormat):
		return "Only JPEG and PNG images are supported."
	case errors.Is(err, ErrDamaged):
		return "The image file is damaged."
	case errors.Is(err, ErrTooLarge):
		return "The image is too large: at most 50 megapixels."
	}
	return ""
}

// Store keeps covers in a folder.
type Store struct {
	dir string
	now func() time.Time // replaced in tests

	// mu makes writing files and sweeping take turns, so a sweep never
	// removes a file that Save is just reusing.
	mu sync.Mutex
	// busy lets one image be decoded at a time. A large picture takes a
	// few hundred megabytes while it's processed; several at once could
	// run a small server out of memory. A channel with room for one value
	// works as a lock that can be waited on.
	busy chan struct{}
}

// NewStore returns a store that keeps covers in dir. The folder is
// created when the first cover is saved.
func NewStore(dir string) *Store {
	return &Store{dir: dir, now: time.Now, busy: make(chan struct{}, 1)}
}

// ValidHash reports whether s looks like a cover hash: 64 lowercase hex
// characters. Checked before a hash is used in a file path.
func ValidHash(s string) bool {
	if len(s) != 64 {
		return false
	}
	for _, c := range s {
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
			return false
		}
	}
	return true
}

// Path returns the file of a cover or its thumbnail. hash must be valid.
func (s *Store) Path(hash string, thumb bool) string {
	name := hash + ".jpg"
	if thumb {
		name = hash + "-thumb.jpg"
	}
	return filepath.Join(s.dir, hash[:2], hash[2:4], name)
}

// Has reports whether a cover is stored. It also marks the file as
// recently used, so Sweep leaves it alone for a while: a form that carries
// a cover hash between attempts keeps it alive this way.
func (s *Store) Has(hash string) bool {
	if !ValidHash(hash) {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.now()
	os.Chtimes(s.Path(hash, true), now, now) // the thumbnail too, or Sweep would take it
	return os.Chtimes(s.Path(hash, false), now, now) == nil
}

// Save stores a JPEG or PNG image as a cover and returns its hash.
func (s *Store) Save(data []byte) (string, error) {
	// DecodeConfig reads only the header: the format and the size.
	cfg, format, err := image.DecodeConfig(bytes.NewReader(data))
	if errors.Is(err, image.ErrFormat) || (err == nil && format != "jpeg" && format != "png") {
		return "", ErrFormat
	}
	if err != nil || cfg.Width <= 0 || cfg.Height <= 0 {
		return "", ErrDamaged
	}
	if cfg.Width*cfg.Height > MaxPixels {
		return "", ErrTooLarge
	}

	s.busy <- struct{}{}        // wait for a free slot (blocks while the channel is full)
	defer func() { <-s.busy }() // and free it again when done

	img, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return "", ErrDamaged
	}
	pic := toRGBA(img)
	// The long-edge limit is a square box, so it doesn't matter that
	// orient may swap width and height afterwards.
	w, h := fit(pic.Bounds().Dx(), pic.Bounds().Dy(), MaxSide, MaxSide)
	pic = scale(pic, w, h)
	if format == "jpeg" {
		pic = orient(pic, jpegOrientation(data))
	}
	full, err := encode(pic, quality)
	if err != nil {
		return "", err
	}
	tw, th := fit(pic.Bounds().Dx(), pic.Bounds().Dy(), ThumbWidth, ThumbHeight)
	thumb, err := encode(scale(pic, tw, th), thumbQuality)
	if err != nil {
		return "", err
	}

	sum := sha256.Sum256(full) // an array of 32 bytes
	hash := hex.EncodeToString(sum[:])

	s.mu.Lock()
	defer s.mu.Unlock()
	// The thumbnail first: Has looks at the cover, so a cover file never
	// exists without its thumbnail.
	if err := s.write(s.Path(hash, true), thumb); err != nil {
		return "", err
	}
	if err := s.write(s.Path(hash, false), full); err != nil {
		return "", err
	}
	return hash, nil
}

func encode(img image.Image, q int) ([]byte, error) {
	var buf bytes.Buffer
	err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: q})
	return buf.Bytes(), err
}

// write saves data to path. The file is written under a temporary name and
// then renamed, which is atomic: the file is either complete or not there.
// A file that exists already has the same content (the name is its hash),
// so it's only marked as recently used.
func (s *Store) write(path string, data []byte) error {
	now := s.now()
	if err := os.Chtimes(path, now, now); err == nil {
		return nil
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil { // 0o755: rwx for the owner, r-x for others
		return err
	}
	tmp, err := os.CreateTemp(dir, ".tmp-*")
	if err != nil {
		return err
	}
	_, err = tmp.Write(data)
	if cerr := tmp.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = os.Chmod(tmp.Name(), 0o644)
	}
	if err == nil {
		err = os.Rename(tmp.Name(), path)
	}
	if err != nil {
		os.Remove(tmp.Name())
		return err
	}
	return os.Chtimes(path, now, now)
}

// Sweep removes cover files whose hash isn't in used and that haven't
// been used for at least grace, plus temporary files left by a crash. The
// grace period protects covers that were just uploaded to a form that
// hasn't been saved yet. It returns how many files it removed.
func (s *Store) Sweep(used map[string]bool, grace time.Duration) (int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	cutoff := s.now().Add(-grace)
	var n int64
	// WalkDir calls the function for every file and folder below s.dir.
	err := filepath.WalkDir(s.dir, func(path string, d fs.DirEntry, err error) error {
		if errors.Is(err, fs.ErrNotExist) && path == s.dir {
			return fs.SkipAll // no covers yet
		}
		if err != nil || d.IsDir() {
			return err
		}
		name := d.Name()
		hash := strings.TrimSuffix(strings.TrimSuffix(name, ".jpg"), "-thumb")
		temp := strings.HasPrefix(name, ".tmp-")
		if !temp && (!ValidHash(hash) || used[hash]) {
			return nil // not ours, or in use
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		if info.ModTime().After(cutoff) {
			return nil
		}
		if err := os.Remove(path); err != nil {
			return err
		}
		n++
		return nil
	})
	return n, err
}
