package server

import (
	"context"
	"errors"
	"io"
	"io/fs"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/keyboardsmash/hodrs-hoard/internal/cover"
	"github.com/keyboardsmash/hodrs-hoard/internal/item"
)

// Covers (spec 3.5): uploading on the item form, serving the files, and
// removing files no item uses.

// coverGrace is how long an unused cover file is kept. A cover uploaded
// with a form that had errors isn't used by any item until the form is
// saved; this gives the user time to fix the form.
const coverGrace = time.Hour

// maxItemFormBytes caps an item form post, cover included. It's larger
// than cover.MaxFileBytes, so a slightly-too-large cover gets a message on
// the form instead of losing everything typed.
const maxItemFormBytes = cover.MaxFileBytes + 5<<20

// parseItemForm reads an item form, which is multipart/form-data because
// of the cover upload. Plain url-encoded posts work too.
func parseItemForm(w http.ResponseWriter, r *http.Request) bool {
	r.Body = http.MaxBytesReader(w, r.Body, maxItemFormBytes)
	// Up to 1 MB of the upload is kept in memory, the rest in a temporary
	// file that the HTTP server deletes after the request.
	err := r.ParseMultipartForm(1 << 20)
	if errors.Is(err, http.ErrNotMultipart) {
		err = nil // ParseMultipartForm has already read a url-encoded body
	}
	var tooBig *http.MaxBytesError
	switch {
	case errors.As(err, &tooBig):
		http.Error(w, "The upload is too large: covers can be at most 15 MB.", http.StatusRequestEntityTooLarge)
		return false
	case err != nil:
		http.Error(w, "bad request", http.StatusBadRequest)
		return false
	}
	return true
}

// readCover decides the item's cover from the form and sets it on f and
// it. In order:
//   - the hash the form carries: the saved cover, or one uploaded with an
//     earlier attempt at this form that had errors;
//   - "Remove the cover" clears it;
//   - a new upload replaces it.
//
// saved is the item's cover before this edit ("" for a new item). Problems
// the user can fix go in errs; the error return is for server failures.
func (s *Server) readCover(r *http.Request, f *itemForm, it *item.Item, saved string, errs item.FieldErrors) error {
	hash := r.PostForm.Get("cover")
	// A hash other than the saved one must be a stored file. Has also
	// keeps the file from being swept while the form is being fixed.
	if hash != "" && hash != saved && !s.covers.Has(hash) {
		hash = saved
		errs["cover"] = "The uploaded cover was lost. Please choose the file again."
	}
	if r.PostForm.Get("cover_remove") == "on" {
		hash = ""
	}

	file, header, err := r.FormFile("cover_file")
	switch {
	case errors.Is(err, http.ErrMissingFile), errors.Is(err, http.ErrNotMultipart):
		// no file chosen, or a plain form without file uploads
	case err != nil:
		return err
	default:
		defer file.Close()
		data, err := io.ReadAll(io.LimitReader(file, cover.MaxFileBytes+1))
		if err != nil {
			return err
		}
		if header.Size > cover.MaxFileBytes || len(data) > cover.MaxFileBytes {
			errs["cover"] = "The file is too large: at most 15 MB."
			break // leaves the switch
		}
		h, err := s.covers.Save(data)
		if msg := cover.Message(err); msg != "" {
			errs["cover"] = msg
		} else if err != nil {
			return err
		} else {
			hash = h
		}
	}
	f.Cover, it.Cover = hash, hash
	return nil
}

// handleCover serves /covers/<hash>.jpg and /covers/<hash>-thumb.jpg.
func (s *Server) handleCover(w http.ResponseWriter, r *http.Request) {
	base, ok := strings.CutSuffix(r.PathValue("file"), ".jpg")
	hash, thumb := strings.CutSuffix(base, "-thumb")
	if !ok || !cover.ValidHash(hash) {
		http.NotFound(w, r)
		return
	}
	f, err := os.Open(s.covers.Path(hash, thumb))
	if errors.Is(err, fs.ErrNotExist) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	defer f.Close()
	h := w.Header()
	// The name is the content's hash, so the file at a URL never changes
	// and browsers may keep it forever. "private": only the browser, not
	// shared caches, since covers can need a login.
	h.Set("Cache-Control", "private, max-age=31536000, immutable")
	h.Set("Content-Type", "image/jpeg")
	h.Set("ETag", `"`+base+`"`)
	// ServeContent handles If-None-Match (answering 304) and range
	// requests. A zero time means no Last-Modified header.
	http.ServeContent(w, r, "", time.Time{}, f)
}

// sweepCovers removes cover files that no item uses (see cover.Sweep).
func (s *Server) sweepCovers(ctx context.Context) (int64, error) {
	used, err := s.items.CoverHashes(ctx)
	if err != nil {
		return 0, err
	}
	return s.covers.Sweep(used, coverGrace)
}
