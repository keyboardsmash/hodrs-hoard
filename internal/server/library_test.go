package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"strings"
	"testing"

	"github.com/keyboardsmash/hodrs-hoard/internal/item"
	"github.com/keyboardsmash/hodrs-hoard/internal/settings"
	"github.com/keyboardsmash/hodrs-hoard/internal/user"
)

// libFor parses a library query string as seen by u (nil: anonymous).
func libFor(u *user.User, query string) *library {
	r := httptest.NewRequest("GET", "/?"+query, nil)
	if u != nil {
		r = r.WithContext(context.WithValue(r.Context(), userKey, u))
	}
	return parseLibrary(httptest.NewRecorder(), r)
}

func TestParseLibrary(t *testing.T) {
	editor := &user.User{Username: "ed", Role: user.RoleEditor}
	viewer := &user.User{Username: "vi", Role: user.RoleViewer}
	all := "q=alien&type=2&kind=video&genre=5&status=hidden&incomplete=1&own=yes&seen=no&fav=yes" +
		"&loaned=yes&tv=no&sort=year&dir=desc&page=3&view=grid"

	l := libFor(editor, all)
	want := item.Query{Search: "alien", Private: true, MediaTypeID: 2, Kind: item.KindVideo, GenreID: 5,
		Status: item.StatusHidden, Incomplete: true, Own: item.Yes, Seen: item.No, Favourite: item.Yes,
		Loaned: item.Yes, TV: item.No, Sort: item.SortYear, Desc: true, Page: 3}
	if l.Query != want {
		t.Errorf("editor query = %+v\nwant %+v", l.Query, want)
	}
	if l.View != viewGrid || !l.Filtered {
		t.Errorf("view %q, filtered %v", l.View, l.Filtered)
	}

	// A viewer can't ask for hidden items; status is dropped.
	if l := libFor(viewer, all); l.Query.Status != "" || !l.Query.Private || l.Query.Loaned != item.Yes {
		t.Errorf("viewer query = %+v", l.Query)
	}
	// Anonymous visitors: no private search, no loan filter.
	if l := libFor(nil, all); l.Query.Private || l.Query.Loaned != item.Any || l.Query.Status != "" {
		t.Errorf("anonymous query = %+v", l.Query)
	}
	if l := libFor(nil, all); strings.Contains(l.URL(), "loaned") || strings.Contains(l.URL(), "status") {
		t.Errorf("anonymous links keep forbidden filters: %s", l.URL())
	}

	// Junk is ignored and not carried into links.
	l = libFor(editor, "type=abc&kind=audio&genre=-1&own=maybe&sort=price&dir=up&page=0&view=table&incomplete=yes&x=1")
	if l.Query != (item.Query{Private: true}) || l.URL() != "/" || l.Filtered || l.View != viewList {
		t.Errorf("junk: query %+v, url %q, filtered %v, view %q", l.Query, l.URL(), l.Filtered, l.View)
	}
	// Sorting and paging alone don't count as filtering.
	if l := libFor(editor, "sort=year&page=2"); l.Filtered {
		t.Error("sort and page counted as filters")
	}
}

func TestLibraryURLs(t *testing.T) {
	ed := &user.User{Role: user.RoleEditor}
	l := libFor(ed, "q=a+b&own=yes&sort=year&page=4")

	tests := []struct{ got, want string }{
		{l.URL(), "/?own=yes&q=a+b&sort=year"},         // changing nothing still resets the page
		{l.Here(), "/?own=yes&page=4&q=a+b&sort=year"}, // ...except for "here"
		{l.URL("own", ""), "/?q=a+b&sort=year"},        // "" removes
		{l.URL("view", "grid"), "/?own=yes&q=a+b&sort=year&view=grid"},
		{l.PageURL(2), "/?own=yes&page=2&q=a+b&sort=year"},
		{l.PageURL(1), "/?own=yes&q=a+b&sort=year"},
		{l.SortURL("year"), "/?dir=desc&own=yes&q=a+b&sort=year"}, // same column: flip
		{l.SortURL("title"), "/?own=yes&q=a+b"},                   // the default needs no parameter
		{l.ClearURL(), "/?sort=year"},
		{l.SortMark("year"), " ↑"},
		{l.SortMark("title"), ""},
	}
	for i, tt := range tests {
		if tt.got != tt.want {
			t.Errorf("%d: got %q, want %q", i, tt.got, tt.want)
		}
	}
	if d := libFor(ed, "sort=year&dir=desc"); d.SortURL("year") != "/?sort=year" || d.SortMark("year") != " ↓" {
		t.Errorf("descending: %q %q", d.SortURL("year"), d.SortMark("year"))
	}
}

func TestPageNumbers(t *testing.T) {
	tests := []struct {
		cur, last int
		want      []int
	}{
		{1, 1, []int{1}},
		{1, 3, []int{1, 2, 3}},
		{1, 10, []int{1, 2, 3, 0, 10}},
		{5, 10, []int{1, 0, 3, 4, 5, 6, 7, 0, 10}},
		{4, 10, []int{1, 2, 3, 4, 5, 6, 0, 10}},
		{10, 10, []int{1, 0, 8, 9, 10}},
	}
	for _, tt := range tests {
		if got := pageNumbers(tt.cur, tt.last); !reflect.DeepEqual(got, tt.want) {
			t.Errorf("pageNumbers(%d, %d) = %v, want %v", tt.cur, tt.last, got, tt.want)
		}
	}
}

// doHX sends a GET the way htmx does, asking to replace target.
func (a *testApp) doHX(path, target string, c *http.Cookie, extra ...string) *httptest.ResponseRecorder {
	req := httptest.NewRequest("GET", path, nil)
	req.Header.Set("HX-Request", "true")
	req.Header.Set("HX-Target", target)
	for i := 0; i+1 < len(extra); i += 2 {
		req.Header.Set(extra[i], extra[i+1])
	}
	req.AddCookie(c)
	rec := httptest.NewRecorder()
	a.h.ServeHTTP(rec, req)
	return rec
}

func TestLibraryPageParts(t *testing.T) {
	a := newAppWithUser(t, "admin", user.RoleAdmin)
	c := a.login("admin", testPassword)
	a.addItem(&item.Item{MediaTypeID: 1, Title: "Alien", Year: 1979})
	a.addItem(&item.Item{MediaTypeID: 3, Title: "Dune", Year: 1965})

	full := a.do("GET", "/?q=alien", nil, c)
	wantStatus(t, full, 200, "<html")
	for _, want := range []string{`id="filters"`, `value="alien"`, "Alien", "htmx-2.0.4.min.js"} {
		if !strings.Contains(full.Body.String(), want) {
			t.Errorf("full page lacks %q", want)
		}
	}
	if strings.Contains(full.Body.String(), ">Dune<") {
		t.Error("search didn't filter")
	}

	results := a.doHX("/?q=alien", "results", c)
	wantStatus(t, results, 200, "Alien")
	if b := results.Body.String(); strings.Contains(b, "<html") || strings.Contains(b, `id="filters"`) {
		t.Error("results part contains more than the results")
	}
	if !strings.Contains(results.Header().Get("Vary"), "HX-Request") {
		t.Error("parts must vary by HX-Request")
	}

	lib := a.doHX("/?sort=year", "library", c)
	if b := lib.Body.String(); strings.Contains(b, "<html") || !strings.Contains(b, `id="filters"`) {
		t.Error("library part should hold the form and nothing outside it")
	}
	// Sorted by year: Dune (1965) before Alien (1979).
	if b := lib.Body.String(); strings.Index(b, ">Dune<") > strings.Index(b, ">Alien<") {
		t.Error("sort by year not applied")
	}

	// History restore and unknown targets get the whole page.
	wantStatus(t, a.doHX("/", "results", c, "HX-History-Restore-Request", "true"), 200, "<html")
	wantStatus(t, a.doHX("/", "nonsense", c), 200, "<html")
}

func TestLibraryEmptyStates(t *testing.T) {
	a := newAppWithUser(t, "admin", user.RoleAdmin)
	c := a.login("admin", testPassword)
	wantStatus(t, a.do("GET", "/", nil, c), 200, "The library is empty.")
	a.addItem(&item.Item{MediaTypeID: 1, Title: "Alien"})
	wantStatus(t, a.do("GET", "/?q=zzz", nil, c), 200, "No items match.")
	wantStatus(t, a.do("GET", "/?page=5", nil, c), 200, "There is no page 5.")
}

func TestLibraryPrivacy(t *testing.T) {
	a := newAppWithUser(t, "admin", user.RoleAdmin)
	c := roleCookies(a)
	a.s.anonymous.Store(true)
	a.addItem(&item.Item{MediaTypeID: 1, Title: "Alien", Notes: "signed by the cast", Loaned: true, LoanedTo: "Sam"})
	a.addItem(&item.Item{MediaTypeID: 1, Title: "Hidden one", Status: item.StatusHidden})

	anon := a.do("GET", "/?q=signed", nil)
	wantStatus(t, anon, 200, "No items match.")
	if b := a.do("GET", "/", nil).Body.String(); strings.Contains(b, `name="loaned"`) || strings.Contains(b, ">loaned<") ||
		strings.Contains(b, `name="status"`) || strings.Contains(b, "/toggle/") || strings.Contains(b, "Hidden one") {
		t.Error("anonymous visitor sees loan filter or mark, status filter, toggles or hidden items")
	}
	// An anonymous loan filter in the URL is ignored, not applied.
	wantStatus(t, a.do("GET", "/?loaned=no", nil), 200, "Alien")

	viewer := a.do("GET", "/?q=signed", nil, c[user.RoleViewer])
	wantStatus(t, viewer, 200, "Alien")
	if b := viewer.Body.String(); !strings.Contains(b, ">loaned<") || strings.Contains(b, "/toggle/") || strings.Contains(b, `name="status"`) {
		t.Error("viewer should see the loaned mark, but no toggles or status filter")
	}
	wantStatus(t, a.do("GET", "/?status=hidden", nil, c[user.RoleViewer]), 200, "Alien") // ignored for viewers

	ed := a.do("GET", "/?status=hidden", nil, c[user.RoleEditor])
	wantStatus(t, ed, 200, "Hidden one")
	if strings.Contains(ed.Body.String(), ">Alien<") {
		t.Error("status=hidden shows active items")
	}
}

func TestViewCookie(t *testing.T) {
	a := newAppWithUser(t, "admin", user.RoleAdmin)
	c := a.login("admin", testPassword)
	a.addItem(&item.Item{MediaTypeID: 1, Title: "Alien"})

	rec := a.do("GET", "/?view=grid", nil, c)
	wantStatus(t, rec, 200, `<ul class="grid">`)
	view := cookieFrom(rec, viewCookie)
	if view == nil || view.Value != viewGrid || view.MaxAge <= 0 {
		t.Fatalf("view cookie = %+v", view)
	}
	wantStatus(t, a.do("GET", "/", nil, c, view), 200, `<ul class="grid">`)
	wantStatus(t, a.do("GET", "/?view=list", nil, c, view), 200, `<table class="library">`)
	// A forged cookie value falls back to the list.
	wantStatus(t, a.do("GET", "/", nil, c, &http.Cookie{Name: viewCookie, Value: "<script>"}), 200, `<table class="library">`)
}

func TestToggle(t *testing.T) {
	a := newAppWithUser(t, "admin", user.RoleAdmin)
	c := roleCookies(a)
	it := a.addItem(&item.Item{MediaTypeID: 1, Title: "Alien"})
	path := itemURL(it.ID) + "/toggle/seen"
	ctx := context.Background()

	// Without JavaScript: a redirect back to the list.
	wantRedirect(t, a.do("POST", path, url.Values{"value": {"on"}, "back": {"/?q=alien&page=2"}}, c[user.RoleEditor]), "/?q=alien&page=2")
	if got, _ := a.s.items.Get(ctx, it.ID); !got.Seen {
		t.Error("seen not set")
	}
	// An off-site "back" is not followed.
	wantRedirect(t, a.do("POST", path, url.Values{"value": {"on"}, "back": {"https://evil.example/"}}, c[user.RoleEditor]), "/")

	// With htmx: the new button.
	req := httptest.NewRequest("POST", path, strings.NewReader(url.Values{"value": {"off"}, "back": {"/"}}.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("HX-Request", "true")
	req.AddCookie(c[user.RoleEditor])
	rec := httptest.NewRecorder()
	a.h.ServeHTTP(rec, req)
	wantStatus(t, rec, 200, `aria-pressed="false"`)
	if b := rec.Body.String(); strings.Contains(b, "<html") || !strings.Contains(b, `name="value" value="on"`) {
		t.Errorf("toggle answer: %s", b)
	}
	if got, _ := a.s.items.Get(ctx, it.ID); got.Seen {
		t.Error("seen not cleared")
	}

	wantStatus(t, a.do("POST", path, url.Values{"value": {"on"}}, c[user.RoleViewer]), http.StatusForbidden, "")
	wantStatus(t, a.do("POST", itemURL(it.ID)+"/toggle/loaned", url.Values{"value": {"on"}}, c[user.RoleEditor]), http.StatusNotFound, "")
	wantStatus(t, a.do("POST", "/items/999/toggle/seen", url.Values{"value": {"on"}}, c[user.RoleEditor]), http.StatusNotFound, "")
}

func TestStaticFiles(t *testing.T) {
	a := newApp(t) // before setup: static files must still load
	rec := a.do("GET", "/static/htmx-2.0.4.min.js", nil)
	wantStatus(t, rec, 200, "htmx")
	if cc := rec.Header().Get("Cache-Control"); !strings.Contains(cc, "immutable") {
		t.Errorf("Cache-Control = %q", cc)
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/javascript") {
		t.Errorf("Content-Type = %q", ct)
	}
	wantStatus(t, a.do("GET", "/static/nope.js", nil), http.StatusNotFound, "")
}

func TestSearchRebuiltAtStartup(t *testing.T) {
	a := newAppWithUser(t, "admin", user.RoleAdmin)
	ctx := context.Background()
	it := a.addItem(&item.Item{MediaTypeID: 1, Title: "Alien", Notes: "signed"})
	// Pretend the database is from before migration 0005.
	a.db.Exec(`UPDATE items SET search_text = 'alien signed', search_private = '' WHERE id = ?`, it.ID)
	a.s.settings.SetInt(ctx, settings.SearchVersion, 0)

	if _, err := New(ctx, "test", a.s.log, a.db, a.s.covers); err != nil {
		t.Fatal(err)
	}
	if p, _ := a.s.items.List(ctx, item.Query{Search: "signed"}); p.Total != 0 {
		t.Error("notes still publicly searchable after restart")
	}
	if v, _ := a.s.settings.Int(ctx, settings.SearchVersion, 0); v != item.SearchVersion {
		t.Errorf("search version = %d", v)
	}
}
