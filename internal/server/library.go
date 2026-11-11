package server

import (
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/keyboardsmash/hodrs-hoard/internal/item"
)

// The library page (spec 5): the start page, with filters, sorting, views
// and paging. All state lives in the URL's query string, so a view can be
// bookmarked and the back button works. With htmx, filter changes only
// reload the results part of the page.

// viewCookie remembers whether the user last chose the list or the grid.
const viewCookie = "hoard_view"

const (
	viewList = "list"
	viewGrid = "grid"
)

// library is the parsed and checked query string of the library page.
type library struct {
	Query item.Query
	// values holds only the valid, non-default parameters. URL builds
	// links from it, so links never carry junk from a hand-edited URL.
	values url.Values

	View string // list or grid

	// The filter form's current values, as the form uses them.
	Search, Type, Kind, Genre, Status   string
	Own, Seen, Favourite, Loaned, TV    string // "", "yes" or "no"
	Incomplete                          bool
	Sort                                string
	Desc                                bool
	ShowStatus, ShowLoaned, ShowPrivate bool // which filters this user gets

	// Filtered is true when any filter is set, so an empty result means
	// "nothing matches", not "the library is empty".
	Filtered bool
}

// parseLibrary reads the library page's query string. Invalid values are
// ignored, as are filters the user may not use.
func parseLibrary(w http.ResponseWriter, r *http.Request) *library {
	in := r.URL.Query()
	u := currentUser(r.Context())
	l := &library{
		values:      url.Values{},
		ShowStatus:  canEdit(r),
		ShowLoaned:  u != nil, // loans are private (spec 3.2)
		ShowPrivate: u != nil,
	}
	keep := func(key, value string) {
		if value != "" {
			l.values.Set(key, value)
		}
	}
	q := &l.Query
	q.Private = l.ShowPrivate

	l.Search = in.Get("q")
	q.Search = l.Search
	keep("q", l.Search)

	if id, err := strconv.ParseInt(in.Get("type"), 10, 64); err == nil && id > 0 {
		q.MediaTypeID, l.Type = id, in.Get("type")
		keep("type", l.Type)
	}
	if k := item.Kind(in.Get("kind")); k.Valid() {
		q.Kind, l.Kind = k, string(k)
		keep("kind", l.Kind)
	}
	if id, err := strconv.ParseInt(in.Get("genre"), 10, 64); err == nil && id > 0 {
		q.GenreID, l.Genre = id, in.Get("genre")
		keep("genre", l.Genre)
	}
	if l.ShowStatus && in.Get("status") == string(item.StatusHidden) {
		q.Status, l.Status = item.StatusHidden, string(item.StatusHidden)
		keep("status", l.Status)
	}
	if in.Get("incomplete") == "1" {
		q.Incomplete, l.Incomplete = true, true
		keep("incomplete", "1")
	}
	flags := []struct {
		key  string
		dst  *item.Flag
		show *string
		ok   bool
	}{
		{"own", &q.Own, &l.Own, true},
		{"seen", &q.Seen, &l.Seen, true},
		{"fav", &q.Favourite, &l.Favourite, true},
		{"loaned", &q.Loaned, &l.Loaned, l.ShowLoaned},
		{"tv", &q.TV, &l.TV, true},
	}
	for _, f := range flags {
		v := in.Get(f.key)
		if !f.ok || (v != "yes" && v != "no") {
			continue
		}
		*f.dst = map[string]item.Flag{"yes": item.Yes, "no": item.No}[v]
		*f.show = v
		keep(f.key, v)
	}
	l.Filtered = len(l.values) > 0

	for _, s := range item.Sorts {
		if in.Get("sort") == s && s != item.SortTitle { // title is the default
			q.Sort, l.Sort = s, s
			keep("sort", s)
		}
	}
	if l.Sort == "" {
		l.Sort = item.SortTitle
	}
	if in.Get("dir") == "desc" {
		q.Desc, l.Desc = true, true
		keep("dir", "desc")
	}
	if p, err := strconv.Atoi(in.Get("page")); err == nil && p > 1 {
		q.Page = p
		keep("page", in.Get("page"))
	}

	// The view: from the URL if given (and then remembered), else from the
	// cookie, else the list.
	switch v := in.Get("view"); v {
	case viewList, viewGrid:
		l.View = v
		keep("view", v)
		if cookieValue(r, viewCookie) != v {
			setPrefCookie(w, r, viewCookie, v)
		}
	default:
		l.View = viewList
		if cookieValue(r, viewCookie) == viewGrid {
			l.View = viewGrid
		}
	}
	return l
}

// setPrefCookie stores a harmless preference for a year.
func setPrefCookie(w http.ResponseWriter, r *http.Request, name, value string) {
	setCookie(w, r, name, value, 365*24*time.Hour)
}

// URL returns the library URL with the given parameters changed, as
// key/value pairs; an empty value removes the parameter. Any change other
// than the page goes back to page 1, since the old page number means
// nothing for a new filter or sort.
//
//	{{.Lib.URL "sort" "year" "dir" ""}}
func (l *library) URL(kv ...string) string {
	v := url.Values{}
	for k, vals := range l.values {
		v[k] = vals
	}
	keepPage := false
	for i := 0; i+1 < len(kv); i += 2 {
		k, val := kv[i], kv[i+1]
		if k == "page" {
			keepPage = true
		}
		if val == "" {
			v.Del(k)
		} else {
			v.Set(k, val)
		}
	}
	if !keepPage {
		v.Del("page")
	}
	if len(v) == 0 {
		return "/"
	}
	return "/?" + v.Encode()
}

// ClearURL drops the filters but keeps sorting and view.
func (l *library) ClearURL() string {
	v := url.Values{}
	for _, k := range []string{"sort", "dir", "view"} {
		if val := l.values.Get(k); val != "" {
			v.Set(k, val)
		}
	}
	if len(v) == 0 {
		return "/"
	}
	return "/?" + v.Encode()
}

// Here is the URL of the current view, page included; toggles return to it.
func (l *library) Here() string { return l.URL("page", l.values.Get("page")) }

// SortURL links a column header: the same column flips the direction,
// another column sorts ascending.
func (l *library) SortURL(sort string) string {
	dir := ""
	if l.Sort == sort && !l.Desc {
		dir = "desc"
	}
	if sort == item.SortTitle {
		sort = "" // the default
	}
	return l.URL("sort", sort, "dir", dir)
}

// SortMark is an arrow for the column the list is sorted by.
func (l *library) SortMark(sort string) string {
	if l.Sort != sort {
		return ""
	}
	if l.Desc {
		return " ↓"
	}
	return " ↑"
}

// PageURL links page n.
func (l *library) PageURL(n int) string {
	if n <= 1 {
		return l.URL("page", "")
	}
	return l.URL("page", strconv.Itoa(n))
}

// pageNumbers lists the page links to show: the first and last page, and
// two on each side of the current one. 0 stands for a gap ("…").
func pageNumbers(current, last int) []int {
	var out []int
	for n := 1; n <= last; n++ {
		if n == 1 || n == last || (n >= current-2 && n <= current+2) {
			out = append(out, n)
		} else if len(out) > 0 && out[len(out)-1] != 0 {
			out = append(out, 0)
		}
	}
	return out
}

func (s *Server) handleLibrary(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	lib := parseLibrary(w, r)
	p, err := s.items.List(ctx, lib.Query)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	types, err := s.items.MediaTypes(ctx)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	genres, err := s.items.Genres(ctx)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	data := pageData{Lib: lib, Page: p, MediaTypes: types, Genres: genres, PageNumbers: pageNumbers(p.Page, p.Pages)}

	// htmx asks for part of the page. HX-Target is the id of the element
	// it will replace: the results while filtering, the whole library
	// section for sort, view and page links (they change the form too).
	if htmxPartial(r) {
		switch t := r.Header.Get("HX-Target"); t {
		case "results", "library":
			s.renderPart(w, r, "index.html", t, data)
			return
		}
	}
	s.render(w, r, http.StatusOK, "index.html", data)
}

// htmxPartial reports whether htmx asked for a page part. When htmx
// restores history it has no copy of, it asks for the whole page.
func htmxPartial(r *http.Request) bool {
	return r.Header.Get("HX-Request") == "true" && r.Header.Get("HX-History-Restore-Request") != "true"
}

// toggle is what the "toggle" template needs for one own/seen/favourite
// button.
type toggle struct {
	ID    int64
	Flag  string
	On    bool
	Label string
	Back  string // where the no-JavaScript form returns to
}

var flagLabels = map[string]string{item.FlagOwn: "Own", item.FlagSeen: "Seen", item.FlagFavourite: "Favourite"}

// newToggle is a template function: {{template "toggle" (toggle .ID "seen" .Seen $.Lib.Here)}}.
func newToggle(id int64, flag string, on bool, back string) toggle {
	return toggle{ID: id, Flag: flag, On: on, Label: flagLabels[flag], Back: back}
}

// handleToggle sets own, seen or favourite from the library list. htmx gets
// the new button back; without JavaScript the browser returns to the list.
func (s *Server) handleToggle(w http.ResponseWriter, r *http.Request) {
	it := s.loadItem(w, r)
	if it == nil || !parseForm(w, r) {
		return
	}
	flag := r.PathValue("flag")
	if _, ok := flagLabels[flag]; !ok {
		http.NotFound(w, r)
		return
	}
	on := r.PostForm.Get("value") == "on"
	err := s.items.SetFlag(r.Context(), currentUser(r.Context()).ID, it.ID, flag, on)
	if errors.Is(err, item.ErrNotFound) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	back := safeNext(r.PostForm.Get("back"))
	if htmxPartial(r) {
		s.execute(w, r, http.StatusOK, "index.html", "toggle", newToggle(it.ID, flag, on, back))
		return
	}
	http.Redirect(w, r, back, http.StatusSeeOther)
}

// initial is the big letter on a grid card's placeholder cover: the first
// letter of the sort title, so "The Matrix" shows M.
func initial(title string) string {
	r, _ := utf8.DecodeRuneInString(item.DeriveSortTitle(title)) // the first character, not byte
	if r == utf8.RuneError {
		return "?"
	}
	return strings.ToUpper(string(r))
}
