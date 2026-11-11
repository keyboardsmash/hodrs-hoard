package server

import (
	"context"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/keyboardsmash/hodrs-hoard/internal/item"
	"github.com/keyboardsmash/hodrs-hoard/internal/user"
)

// Seeded media type IDs (migration 0004).
const (
	typeDVD  = "1"
	typeBook = "3"
)

// addFullItem creates a hidden item with every field set, for render tests.
func (a *testApp) addFullItem(mediaTypeID int64) *item.Item {
	a.t.Helper()
	age := 0
	return a.addItem(&item.Item{
		MediaTypeID: mediaTypeID, Title: "The Full Item", OriginalTitle: "Original", AltTitles: []string{"Alt"},
		Year: 2001, Description: "Line one\nLine two", Notes: "secret note", OwnRating: 7,
		Series: "Saga", SeriesNumber: "2", Own: true, Seen: true, Favourite: true,
		Loaned: true, LoanedTo: "Sam", LoanedOn: "2026-01-02", Trailer: "https://example.com/t",
		MPAA: "PG", AgeLimit: &age, Status: item.StatusHidden,
		Credits: []item.Credit{{Role: item.RoleDirector, Name: "Dee Rector"}, {Role: item.RoleAuthor, Name: "Ann Author"},
			{Role: item.RoleCast, Name: "Cass Member"}},
		Genres: []string{"Drama"}, Languages: []string{"English"}, Subtitles: []string{"Swedish"}, Countries: []string{"UK"},
		Identifiers: []item.Identifier{{Type: item.IDEan, Value: "4006381333931"}, {Type: item.IDImdb, Value: "tt0133093"},
			{Type: item.IDTmdb, Value: "603"}, {Type: item.IDIsbn, Value: "9780306406157"}},
		Runtime: 120, Region: "2", VideoStandard: "PAL", IsTV: true, Seasons: "1-3", AudioFormat: "DD 5.1", VideoFormat: "16:9",
		Publisher: "Pub", Pages: 300, Binding: "paperback", FileFormat: "epub", FilePath: "/books/x.epub",
	})
}

func (a *testApp) addItem(it *item.Item) *item.Item {
	a.t.Helper()
	ctx := context.Background()
	id, err := a.s.items.Create(ctx, 0, it)
	if err != nil {
		a.t.Fatal(err)
	}
	got, err := a.s.items.Get(ctx, id)
	if err != nil {
		a.t.Fatal(err)
	}
	return got
}

// roleCookies creates an editor and a viewer next to the admin and returns
// a logged-in cookie for each.
func roleCookies(a *testApp) map[user.Role]*http.Cookie {
	a.addUser("editor", user.RoleEditor)
	a.addUser("viewer", user.RoleViewer)
	return map[user.Role]*http.Cookie{
		user.RoleAdmin:  a.login("admin", testPassword),
		user.RoleEditor: a.login("editor", testPassword),
		user.RoleViewer: a.login("viewer", testPassword),
	}
}

func TestItemAccess(t *testing.T) {
	a := newAppWithUser(t, "admin", user.RoleAdmin)
	c := roleCookies(a)
	active := a.addItem(&item.Item{MediaTypeID: 1, Title: "Active"})
	hidden := a.addItem(&item.Item{MediaTypeID: 1, Title: "Hidden", Status: item.StatusHidden})
	trashed := a.addItem(&item.Item{MediaTypeID: 1, Title: "Trashed", Status: item.StatusTrashed})

	cases := []struct {
		method, path          string
		admin, editor, viewer int
	}{
		{"GET", itemURL(active.ID), 200, 200, 200},
		{"GET", itemURL(hidden.ID), 200, 200, 404},
		{"GET", itemURL(trashed.ID), 200, 200, 404},
		{"GET", "/items/999", 404, 404, 404},
		{"GET", "/items/abc", 404, 404, 404},
		{"GET", "/items/new", 200, 200, 403},
		{"GET", "/items/new?type=" + typeDVD, 200, 200, 403},
		{"GET", itemURL(active.ID) + "/edit", 200, 200, 403},
		{"GET", "/media-types", 200, 200, 403},
		{"GET", "/trash", 200, 200, 403},
		{"POST", itemURL(active.ID) + "/hide", 303, 303, 403},
		{"POST", "/items/new", 303, 303, 403}, // no type: back to the type choice
		{"POST", "/media-types", 422, 422, 403},
	}
	for _, tc := range cases {
		for role, want := range map[user.Role]int{user.RoleAdmin: tc.admin, user.RoleEditor: tc.editor, user.RoleViewer: tc.viewer} {
			var form url.Values
			if tc.method == "POST" {
				form = url.Values{}
			}
			if rec := a.do(tc.method, tc.path, form, c[role]); rec.Code != want {
				t.Errorf("%s %s %s = %d, want %d", role, tc.method, tc.path, rec.Code, want)
			}
		}
		// Anonymous visitors go to the login page (anonymous browsing is off).
		if rec := a.do(tc.method, tc.path, url.Values{}); rec.Code != http.StatusSeeOther ||
			!strings.HasPrefix(rec.Header().Get("Location"), "/login") {
			t.Errorf("anonymous %s %s = %d to %q", tc.method, tc.path, rec.Code, rec.Header().Get("Location"))
		}
	}

	// Only admins empty the trash.
	wantStatus(t, a.do("POST", "/trash/empty", url.Values{}, c[user.RoleEditor]), http.StatusForbidden, "")
	if _, err := a.s.items.Get(context.Background(), trashed.ID); err != nil {
		t.Errorf("editor emptied the trash: %v", err)
	}
	wantRedirect(t, a.do("POST", "/trash/empty", url.Values{}, c[user.RoleAdmin]), "/trash?msg=trash-emptied")
	wantStatus(t, a.do("GET", itemURL(trashed.ID), nil, c[user.RoleAdmin]), http.StatusNotFound, "")
}

// itemForm is a valid DVD form; tests change single fields.
func dvdForm() url.Values {
	return url.Values{
		"media_type":      {typeDVD},
		"title":           {"The Matrix"},
		"year":            {"1999"},
		"runtime":         {"136"},
		"credit_director": {"Lana Wachowski\nLilly Wachowski"},
		"credit_cast":     {"Keanu Reeves\r\nCarrie-Anne Moss"},
		"genres":          {"Sci-Fi, Action"},
		"id_imdb":         {"https://www.imdb.com/title/tt0133093/"},
		"id_ean":          {""},
		"own":             {"on"},
		"notes":           {"Shelf 3"},
	}
}

func TestItemCreateAndEdit(t *testing.T) {
	a := newAppWithUser(t, "admin", user.RoleAdmin)
	c := roleCookies(a)[user.RoleEditor]

	wantStatus(t, a.do("GET", "/items/new", nil, c), 200, `href="/items/new?type=1">DVD</a>`)
	rec := a.do("GET", "/items/new?type="+typeDVD, nil, c)
	wantStatus(t, rec, 200, "Video details")
	// The media type menu only offers the kind's types.
	if body := rec.Body.String(); !strings.Contains(body, ">VHS</option>") || strings.Contains(body, ">Book</option>") {
		t.Error("add form should offer video types only")
	}
	wantStatus(t, a.do("GET", "/items/new?type="+typeBook, nil, c), 200, "Book details")

	rec = a.do("POST", "/items/new", dvdForm(), c)
	if rec.Code != http.StatusSeeOther || !strings.HasSuffix(rec.Header().Get("Location"), "?msg=item-created") {
		t.Fatalf("create: %d %s", rec.Code, rec.Body)
	}
	loc := strings.TrimSuffix(rec.Header().Get("Location"), "?msg=item-created")
	rec = a.do("GET", loc, nil, c)
	for _, want := range []string{"The Matrix (1999)", "Lana Wachowski, Lilly Wachowski", "Keanu Reeves, Carrie-Anne Moss",
		"Action, Sci-Fi", `href="https://www.imdb.com/title/tt0133093/"`, "136 min", "by editor", "Shelf 3"} {
		if !strings.Contains(rec.Body.String(), want) {
			t.Errorf("item page lacks %q", want)
		}
	}

	// The edit form shows the saved values.
	rec = a.do("GET", loc+"/edit", nil, c)
	wantStatus(t, rec, 200, "Lana Wachowski\nLilly Wachowski</textarea>")
	wantStatus(t, rec, 200, `value="tt0133093"`)

	f := dvdForm()
	f.Set("title", "The Matrix Reloaded")
	f.Set("media_type", "2") // VHS: same kind
	wantRedirect(t, a.do("POST", loc+"/edit", f, c), loc+"?msg=saved")
	wantStatus(t, a.do("GET", loc, nil, c), 200, "VHS")

	// Another kind is refused, even if posted by hand.
	f.Set("media_type", typeBook)
	wantStatus(t, a.do("POST", loc+"/edit", f, c), http.StatusUnprocessableEntity, "another kind")
}

func TestItemFormErrorsKeepInput(t *testing.T) {
	a := newAppWithUser(t, "admin", user.RoleAdmin)
	c := roleCookies(a)[user.RoleEditor]

	f := dvdForm()
	f.Set("title", "  ")
	f.Set("year", "19x9")
	f.Set("id_ean", "4006381333932") // wrong check digit
	rec := a.do("POST", "/items/new", f, c)
	wantStatus(t, rec, http.StatusUnprocessableEntity, "Must be a whole number.")
	for _, want := range []string{
		"A title is required.",          // from the store, shown together with the number error
		"Check digit doesn&#39;t match", // html/template escapes the apostrophe
		`value="19x9"`,                  // what was typed, not a cleaned value
		`value="4006381333932"`,
		"Keanu Reeves\r\nCarrie-Anne Moss</textarea>", // exactly as typed
	} {
		if !strings.Contains(rec.Body.String(), want) {
			t.Errorf("error page lacks %q", want)
		}
	}
	if p, _ := a.s.items.List(context.Background(), item.Query{}); p.Total != 0 {
		t.Error("item saved despite errors")
	}
}

func TestItemStatusFlow(t *testing.T) {
	a := newAppWithUser(t, "admin", user.RoleAdmin)
	c := roleCookies(a)
	ed := c[user.RoleEditor]
	it := a.addItem(&item.Item{MediaTypeID: 1, Title: "Alien"})
	u := itemURL(it.ID)

	wantStatus(t, a.do("GET", "/", nil, c[user.RoleViewer]), 200, "Alien")

	wantRedirect(t, a.do("POST", u+"/hide", url.Values{}, ed), u+"?msg=hidden")
	wantStatus(t, a.do("GET", u+"?msg=hidden", nil, ed), 200, "This item is hidden")
	if rec := a.do("GET", "/", nil, c[user.RoleViewer]); strings.Contains(rec.Body.String(), "Alien") {
		t.Error("hidden item listed on the start page")
	}

	wantRedirect(t, a.do("POST", u+"/unhide", url.Values{}, ed), u+"?msg=unhidden")
	wantStatus(t, a.do("GET", u, nil, c[user.RoleViewer]), 200, "Alien")

	wantRedirect(t, a.do("POST", u+"/trash", url.Values{}, ed), u+"?msg=trashed")
	wantStatus(t, a.do("GET", "/trash", nil, ed), 200, "Alien")
	wantStatus(t, a.do("GET", u, nil, ed), 200, "Restore")
	if strings.Contains(a.do("GET", "/trash", nil, ed).Body.String(), "Empty trash") {
		t.Error("editor sees the Empty trash button")
	}
	wantStatus(t, a.do("GET", "/trash", nil, c[user.RoleAdmin]), 200, "Empty trash")

	wantRedirect(t, a.do("POST", u+"/restore", url.Values{}, ed), u+"?msg=restored")
	wantStatus(t, a.do("GET", "/trash", nil, ed), 200, "The trash is empty.")
}

func TestPrivateFieldsHiddenFromAnonymous(t *testing.T) {
	a := newAppWithUser(t, "admin", user.RoleAdmin)
	c := roleCookies(a)
	a.s.anonymous.Store(true)
	it := a.addItem(&item.Item{MediaTypeID: 1, Title: "Alien", Notes: "secret note",
		Loaned: true, LoanedTo: "Sam Lender", LoanedOn: "2026-01-02"})

	rec := a.do("GET", itemURL(it.ID), nil)
	wantStatus(t, rec, 200, "Alien")
	for _, private := range []string{"secret note", "Sam Lender", "2026-01-02", "Added "} {
		if strings.Contains(rec.Body.String(), private) {
			t.Errorf("anonymous visitor sees %q", private)
		}
	}
	rec = a.do("GET", itemURL(it.ID), nil, c[user.RoleViewer])
	for _, want := range []string{"secret note", "to Sam Lender since 2026-01-02"} {
		if !strings.Contains(rec.Body.String(), want) {
			t.Errorf("viewer doesn't see %q", want)
		}
	}
	// Editing still needs a login.
	wantRedirect(t, a.do("GET", itemURL(it.ID)+"/edit", nil), "/login?next=%2Fitems%2F1%2Fedit")
}

func TestDuplicateWarning(t *testing.T) {
	a := newAppWithUser(t, "admin", user.RoleAdmin)
	c := roleCookies(a)
	isbn := []item.Identifier{{Type: item.IDIsbn, Value: "0-306-40615-2"}}
	first := a.addItem(&item.Item{MediaTypeID: 3, Title: "First", Identifiers: isbn})
	a.addItem(&item.Item{MediaTypeID: 3, Title: "Second", Identifiers: isbn})

	wantStatus(t, a.do("GET", itemURL(first.ID), nil, c[user.RoleEditor]), 200, "Another item has the same ISBN 9780306406157")
	if rec := a.do("GET", itemURL(first.ID), nil, c[user.RoleViewer]); strings.Contains(rec.Body.String(), "Another item") {
		t.Error("viewer sees the duplicate warning")
	}
}

func TestMediaTypePage(t *testing.T) {
	a := newAppWithUser(t, "admin", user.RoleAdmin)
	ed := roleCookies(a)[user.RoleEditor]

	wantRedirect(t, a.do("POST", "/media-types", url.Values{"name": {"Blu-ray"}, "kind": {"video"}, "physical": {"on"}}, ed),
		"/media-types?msg=type-created")
	rec := a.do("GET", "/media-types?msg=type-created", nil, ed)
	wantStatus(t, rec, 200, `value="Blu-ray"`)
	wantStatus(t, rec, 200, "Media type added.")

	wantStatus(t, a.do("POST", "/media-types", url.Values{"name": {"blu-ray"}, "kind": {"video"}}, ed),
		http.StatusUnprocessableEntity, "There is already a media type called blu-ray.")

	types, _ := a.s.items.MediaTypes(context.Background())
	br := findByName(types, "Blu-ray")
	path := "/media-types/" + itoa(br.ID)
	wantRedirect(t, a.do("POST", path+"/rename", url.Values{"name": {"Blu-ray Disc"}}, ed), "/media-types?msg=saved")

	a.addItem(&item.Item{MediaTypeID: br.ID, Title: "x"})
	wantStatus(t, a.do("POST", path+"/delete", url.Values{}, ed), http.StatusUnprocessableEntity, "can&#39;t be deleted")
	wantStatus(t, a.do("POST", "/media-types/4/delete", url.Values{}, ed), http.StatusSeeOther, "") // Ebook, unused
	wantStatus(t, a.do("POST", "/media-types/999/delete", url.Values{}, ed), http.StatusNotFound, "")
}

func findByName(types []*item.MediaType, name string) *item.MediaType {
	for _, t := range types {
		if t.Name == name {
			return t
		}
	}
	return nil
}

func itoa(n int64) string { return strings.TrimPrefix(itemURL(n), "/items/") }
