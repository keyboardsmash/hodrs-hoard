package server

import (
	"bytes"
	"context"
	"image"
	"image/color"
	"image/png"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/keyboardsmash/hodrs-hoard/internal/cover"
	"github.com/keyboardsmash/hodrs-hoard/internal/item"
	"github.com/keyboardsmash/hodrs-hoard/internal/user"
)

// testPNG is a small PNG in one colour; different colours give different
// covers.
func testPNG(t *testing.T, c uint8) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 60, 90))
	for i := range img.Pix {
		img.Pix[i] = c
		if i%4 == 3 {
			img.Pix[i] = 255 // opaque; nearly transparent colours would all be white
		}
	}
	img.Set(0, 0, color.RGBA{0, 0, 0, 255})
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// upload posts form as multipart/form-data, as a browser does when a form
// has a file input. file is sent as cover_file unless it is nil.
func (a *testApp) upload(target string, form url.Values, file []byte, cookies ...*http.Cookie) *httptest.ResponseRecorder {
	a.t.Helper()
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	for k, vs := range form {
		for _, v := range vs {
			mw.WriteField(k, v)
		}
	}
	if file != nil {
		fw, err := mw.CreateFormFile("cover_file", "cover.png")
		if err != nil {
			a.t.Fatal(err)
		}
		fw.Write(file)
	} else {
		// A browser sends an empty file part when no file was chosen.
		mw.CreateFormFile("cover_file", "")
	}
	mw.Close()
	req := httptest.NewRequest("POST", target, &body)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	for _, c := range cookies {
		req.AddCookie(c)
	}
	rec := httptest.NewRecorder()
	a.h.ServeHTTP(rec, req)
	return rec
}

// createdItem returns the item a create redirect points at.
func (a *testApp) createdItem(rec *httptest.ResponseRecorder) *item.Item {
	a.t.Helper()
	loc := rec.Header().Get("Location")
	if rec.Code != http.StatusSeeOther || !strings.HasSuffix(loc, "?msg=item-created") {
		a.t.Fatalf("create: %d %s", rec.Code, rec.Body)
	}
	id, _ := pathIDFrom(strings.TrimSuffix(loc, "?msg=item-created"))
	it, err := a.s.items.Get(context.Background(), id)
	if err != nil {
		a.t.Fatal(err)
	}
	return it
}

func pathIDFrom(path string) (int64, bool) {
	var id int64
	for _, c := range strings.TrimPrefix(path, "/items/") {
		if c < '0' || c > '9' {
			return 0, false
		}
		id = id*10 + int64(c-'0')
	}
	return id, true
}

var hiddenCover = regexp.MustCompile(`name="cover" value="([0-9a-f]{64})"`)

func TestCoverUploadAndServe(t *testing.T) {
	a := newAppWithUser(t, "admin", user.RoleAdmin)
	cookies := roleCookies(a)
	ed := cookies[user.RoleEditor]

	wantStatus(t, a.do("GET", "/items/new?type="+typeDVD, nil, ed), 200, `enctype="multipart/form-data"`)
	it := a.createdItem(a.upload("/items/new", dvdForm(), testPNG(t, 200), ed))
	if !cover.ValidHash(it.Cover) {
		t.Fatalf("Cover = %q", it.Cover)
	}
	full, thumb := "/covers/"+it.Cover+".jpg", "/covers/"+it.Cover+"-thumb.jpg"

	wantStatus(t, a.do("GET", itemURL(it.ID), nil, ed), 200, `href="`+full+`"`)
	wantStatus(t, a.do("GET", "/", nil, ed), 200, `src="`+thumb+`"`)
	wantStatus(t, a.do("GET", "/?view=grid", nil, ed), 200, `<img class="cover" src="`+thumb+`"`)

	for _, path := range []string{full, thumb} {
		rec := a.do("GET", path, nil, cookies[user.RoleViewer])
		wantStatus(t, rec, 200, "")
		if ct := rec.Header().Get("Content-Type"); ct != "image/jpeg" {
			t.Errorf("%s: Content-Type = %q", path, ct)
		}
		if cc := rec.Header().Get("Cache-Control"); !strings.Contains(cc, "immutable") || !strings.Contains(cc, "private") {
			t.Errorf("%s: Cache-Control = %q", path, cc)
		}
		if _, _, err := image.Decode(rec.Body); err != nil {
			t.Errorf("%s: not an image: %v", path, err)
		}
		// A browser that has it gets "not modified".
		req := httptest.NewRequest("GET", path, nil)
		req.AddCookie(ed)
		req.Header.Set("If-None-Match", rec.Header().Get("ETag"))
		rec2 := httptest.NewRecorder()
		a.h.ServeHTTP(rec2, req)
		if rec2.Code != http.StatusNotModified {
			t.Errorf("%s with If-None-Match: %d", path, rec2.Code)
		}
	}

	// Covers need a login like the rest, unless anonymous browsing is on.
	if rec := a.do("GET", full, nil); rec.Code != http.StatusSeeOther {
		t.Errorf("anonymous: %d", rec.Code)
	}
	for _, bad := range []string{
		"/covers/" + strings.Repeat("0", 64) + ".jpg", // valid but not stored
		"/covers/" + it.Cover,                         // no .jpg
		"/covers/" + strings.ToUpper(it.Cover) + ".jpg",
		"/covers/" + it.Cover + "-small.jpg",
		"/covers/..%2Fhoard.db",
	} {
		wantStatus(t, a.do("GET", bad, nil, ed), http.StatusNotFound, "")
	}

	// The filter.
	a.addItem(&item.Item{MediaTypeID: 1, Title: "Coverless"})
	rec := a.do("GET", "/?cover=yes", nil, ed)
	if body := rec.Body.String(); !strings.Contains(body, "The Matrix") || strings.Contains(body, "Coverless") {
		t.Error("?cover=yes lists the wrong items")
	}
	rec = a.do("GET", "/?cover=no", nil, ed)
	if body := rec.Body.String(); strings.Contains(body, "The Matrix") || !strings.Contains(body, "Coverless") {
		t.Error("?cover=no lists the wrong items")
	}
}

func TestCoverKeptAcrossFormError(t *testing.T) {
	a := newAppWithUser(t, "admin", user.RoleAdmin)
	ed := roleCookies(a)[user.RoleEditor]

	f := dvdForm()
	f.Set("title", "")
	rec := a.upload("/items/new", f, testPNG(t, 100), ed)
	wantStatus(t, rec, http.StatusUnprocessableEntity, "A title is required.")
	m := hiddenCover.FindStringSubmatch(rec.Body.String())
	if m == nil {
		t.Fatal("form doesn't carry the uploaded cover")
	}
	wantStatus(t, rec, http.StatusUnprocessableEntity, `src="/covers/`+m[1]+`-thumb.jpg"`)

	// Sent again with the hash and no file, as the browser would.
	f.Set("title", "The Matrix")
	f.Set("cover", m[1])
	if it := a.createdItem(a.upload("/items/new", f, nil, ed)); it.Cover != m[1] {
		t.Errorf("Cover = %q, want %q", it.Cover, m[1])
	}

	// A hash whose file doesn't exist (swept, or made up) is refused.
	f.Set("cover", strings.Repeat("a", 64))
	wantStatus(t, a.upload("/items/new", f, nil, ed), http.StatusUnprocessableEntity, "The uploaded cover was lost")
	f.Set("cover", "../../hoard.db")
	wantStatus(t, a.upload("/items/new", f, nil, ed), http.StatusUnprocessableEntity, "The uploaded cover was lost")
}

func TestCoverBadUploads(t *testing.T) {
	a := newAppWithUser(t, "admin", user.RoleAdmin)
	ed := roleCookies(a)[user.RoleEditor]

	rec := a.upload("/items/new", dvdForm(), []byte("GIF89a not really"), ed)
	wantStatus(t, rec, http.StatusUnprocessableEntity, "Only JPEG and PNG images are supported.")
	wantStatus(t, rec, http.StatusUnprocessableEntity, `value="The Matrix"`) // the rest is kept

	// Just over the limit: a message on the form.
	rec = a.upload("/items/new", dvdForm(), make([]byte, cover.MaxFileBytes+1), ed)
	wantStatus(t, rec, http.StatusUnprocessableEntity, "The file is too large")
	wantStatus(t, rec, http.StatusUnprocessableEntity, `value="The Matrix"`)

	// Far over: the request is cut off.
	rec = a.upload("/items/new", dvdForm(), make([]byte, maxItemFormBytes+1), ed)
	wantStatus(t, rec, http.StatusRequestEntityTooLarge, "at most 15 MB")

	if p, _ := a.s.items.List(context.Background(), item.Query{}); p.Total != 0 {
		t.Errorf("%d items created", p.Total)
	}
}

func TestCoverReplaceAndRemove(t *testing.T) {
	a := newAppWithUser(t, "admin", user.RoleAdmin)
	ed := roleCookies(a)[user.RoleEditor]
	it := a.createdItem(a.upload("/items/new", dvdForm(), testPNG(t, 1), ed))
	edit := itemURL(it.ID) + "/edit"
	get := func() string {
		got, err := a.s.items.Get(context.Background(), it.ID)
		if err != nil {
			t.Fatal(err)
		}
		return got.Cover
	}

	// The edit form shows the cover and carries its hash.
	rec := a.do("GET", edit, nil, ed)
	wantStatus(t, rec, 200, `name="cover" value="`+it.Cover+`"`)
	wantStatus(t, rec, 200, "Remove the cover")

	// Saving without a file keeps it, even if its file has gone missing.
	f := dvdForm()
	f.Set("cover", it.Cover)
	os.Remove(a.s.covers.Path(it.Cover, false))
	wantRedirect(t, a.upload(edit, f, nil, ed), itemURL(it.ID)+"?msg=saved")
	if get() != it.Cover {
		t.Error("cover lost on save")
	}

	// A new file replaces it.
	wantRedirect(t, a.upload(edit, f, testPNG(t, 2), ed), itemURL(it.ID)+"?msg=saved")
	replaced := get()
	if replaced == it.Cover || replaced == "" {
		t.Errorf("not replaced: %q", replaced)
	}

	// Remove.
	f.Set("cover", replaced)
	f.Set("cover_remove", "on")
	wantRedirect(t, a.upload(edit, f, nil, ed), itemURL(it.ID)+"?msg=saved")
	if got := get(); got != "" {
		t.Errorf("not removed: %q", got)
	}
}

func TestEmptyTrashRemovesCovers(t *testing.T) {
	a := newAppWithUser(t, "admin", user.RoleAdmin)
	c := roleCookies(a)
	ed, admin := c[user.RoleEditor], c[user.RoleAdmin]

	trashed := a.createdItem(a.upload("/items/new", dvdForm(), testPNG(t, 1), ed))
	kept := a.createdItem(a.upload("/items/new", dvdForm(), testPNG(t, 2), ed))
	// A cover uploaded with a form that was never finished.
	f := dvdForm()
	f.Set("title", "")
	m := hiddenCover.FindStringSubmatch(a.upload("/items/new", f, testPNG(t, 3), ed).Body.String())
	if m == nil {
		t.Fatal("no pending cover")
	}
	pending := m[1]

	a.do("POST", itemURL(trashed.ID)+"/trash", url.Values{}, ed)
	// Make every file old enough to be swept.
	old := time.Now().Add(-2 * time.Hour)
	for _, h := range []string{trashed.Cover, kept.Cover, pending} {
		for _, thumb := range []bool{false, true} {
			os.Chtimes(a.s.covers.Path(h, thumb), old, old)
		}
	}
	wantRedirect(t, a.do("POST", "/trash/empty", url.Values{}, admin), "/trash?msg=trash-emptied")

	exists := func(h string) bool { _, err := os.Stat(a.s.covers.Path(h, true)); return err == nil }
	if exists(trashed.Cover) {
		t.Error("cover of the deleted item kept")
	}
	if exists(pending) {
		t.Error("abandoned upload kept")
	}
	if !exists(kept.Cover) {
		t.Error("cover in use removed")
	}
}
