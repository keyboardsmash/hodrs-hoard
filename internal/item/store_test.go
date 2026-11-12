package item

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/keyboardsmash/hodrs-hoard/internal/db"
)

// Seeded media type IDs (migration 0004).
const (
	dvd   int64 = 1
	vhs   int64 = 2
	book  int64 = 3
	ebook int64 = 4
)

var ctx = context.Background()

func newStore(t *testing.T) *Store {
	t.Helper()
	s := NewStore(db.OpenTestMigrated(t))
	clock := time.Unix(1_700_000_000, 0) // underscores in numbers are just for reading
	s.now = func() time.Time {
		clock = clock.Add(time.Second) // every call one second later
		return clock
	}
	return s
}

func mustCreate(t *testing.T, s *Store, it *Item) int64 {
	t.Helper()
	id, err := s.Create(ctx, 0, it)
	if err != nil {
		t.Fatalf("Create(%q): %v", it.Title, err)
	}
	return id
}

func mustGet(t *testing.T, s *Store, id int64) *Item {
	t.Helper()
	it, err := s.Get(ctx, id)
	if err != nil {
		t.Fatalf("Get(%d): %v", id, err)
	}
	return it
}

// fieldErr returns the message for field, or "" if err has none.
func fieldErr(err error, field string) string {
	var fe FieldErrors
	if errors.As(err, &fe) {
		return fe[field]
	}
	return ""
}

func TestCreateAndGet(t *testing.T) {
	s := newStore(t)
	age := 15
	in := &Item{
		MediaTypeID:   dvd,
		Title:         "  The   Matrix ",
		SortTitle:     "Matrix, The", // same as derived: not stored
		OriginalTitle: "The Matrix",
		AltTitles:     []string{"Matrix", "", "matrix"},
		Year:          1999,
		Description:   "  Neo learns the truth.\r\n\r\nThen kung fu.  ",
		Notes:         "Bought in Oslo",
		OwnRating:     9,
		Series:        "The Matrix",
		SeriesNumber:  "1",
		Own:           true,
		Seen:          true,
		Loaned:        true,
		LoanedTo:      "Sam",
		LoanedOn:      "2026-01-31",
		Trailer:       "https://example.com/trailer",
		MPAA:          "R",
		AgeLimit:      &age,
		Credits: []Credit{
			{RoleDirector, "Lana Wachowski"}, {RoleCast, "Keanu Reeves"},
			{RoleDirector, "Lilly Wachowski"}, {RoleCast, "Laurence Fishburne"},
			{RoleCast, "keanu reeves"}, {RoleCast, ""},
		},
		Genres:        []string{"Sci-Fi", "Action", "sci-fi"},
		Languages:     []string{"English"},
		Subtitles:     []string{"Swedish", "Norwegian"},
		Countries:     []string{"USA", "Australia"},
		Identifiers:   []Identifier{{IDImdb, "https://www.imdb.com/title/tt0133093/"}, {IDEan, "4006381333931"}, {IDIsbn, " "}},
		Runtime:       136,
		Region:        "2",
		VideoStandard: "pal",
		AudioFormat:   "Dolby Digital 5.1",
		VideoFormat:   "16:9",
	}
	id := mustCreate(t, s, in)
	got := mustGet(t, s, id)

	checks := []struct {
		name      string
		got, want any
	}{
		{"title", got.Title, "The Matrix"},
		{"sort title", got.SortTitle, ""},
		{"effective sort title", got.EffectiveSortTitle(), "Matrix, The"},
		{"alt titles", got.AltTitles, []string{"Matrix"}},
		{"description", got.Description, "Neo learns the truth.\n\nThen kung fu."},
		{"status", got.Status, StatusActive},
		{"media type", got.MediaType.Name, "DVD"},
		{"directors", got.People(RoleDirector), []string{"Lana Wachowski", "Lilly Wachowski"}},
		{"cast", got.People(RoleCast), []string{"Keanu Reeves", "Laurence Fishburne"}},
		{"genres", got.Genres, []string{"Action", "Sci-Fi"}},
		{"subtitles", got.Subtitles, []string{"Swedish", "Norwegian"}},
		{"countries", got.Countries, []string{"USA", "Australia"}},
		{"identifiers", got.Identifiers, []Identifier{{IDEan, "4006381333931"}, {IDImdb, "tt0133093"}}},
		{"video standard", got.VideoStandard, "PAL"},
		{"age limit", *got.AgeLimit, 15},
		{"loan", [3]any{got.Loaned, got.LoanedTo, got.LoanedOn}, [3]any{true, "Sam", "2026-01-31"}},
		{"flags", [3]bool{got.Own, got.Seen, got.Favourite}, [3]bool{true, true, false}},
		{"incomplete", got.Incomplete(), false},
		{"added = updated", got.AddedAt, got.UpdatedAt},
	}
	for _, c := range checks {
		if !reflect.DeepEqual(c.got, c.want) {
			t.Errorf("%s = %#v, want %#v", c.name, c.got, c.want)
		}
	}
}

func TestCreateMinimal(t *testing.T) {
	s := newStore(t)
	got := mustGet(t, s, mustCreate(t, s, &Item{MediaTypeID: book, Title: "Untitled"}))
	if got.AgeLimit != nil || got.Year != 0 || got.Credits != nil || got.AddedBy != 0 {
		t.Errorf("empty fields not empty: %+v", got)
	}
	if !got.Incomplete() {
		t.Error("book without year and author should be incomplete")
	}
}

func TestCreateValidation(t *testing.T) {
	s := newStore(t)
	tests := []struct {
		field string
		item  Item
	}{
		{"title", Item{MediaTypeID: dvd, Title: "   "}},
		{"media_type", Item{MediaTypeID: 99, Title: "x"}},
		{"year", Item{MediaTypeID: dvd, Title: "x", Year: 99}},
		{"own_rating", Item{MediaTypeID: dvd, Title: "x", OwnRating: 11}},
		{"loaned_on", Item{MediaTypeID: dvd, Title: "x", Loaned: true, LoanedOn: "31/1/2026"}},
		{"trailer", Item{MediaTypeID: dvd, Title: "x", Trailer: "javascript:alert(1)"}},
		{"video_standard", Item{MediaTypeID: dvd, Title: "x", VideoStandard: "MESECAM"}},
		{"binding", Item{MediaTypeID: book, Title: "x", Binding: "spiral"}},
		{"identifier_isbn", Item{MediaTypeID: book, Title: "x", Identifiers: []Identifier{{IDIsbn, "9780306406158"}}}},
		{"credits", Item{MediaTypeID: dvd, Title: "x", Credits: []Credit{{"grip", "Bob"}}}},
		{"parent", Item{MediaTypeID: dvd, Title: "x", ParentID: 42}},
	}
	for _, tt := range tests {
		_, err := s.Create(ctx, 0, &tt.item)
		if fieldErr(err, tt.field) == "" {
			t.Errorf("%s: err = %v, want a field error", tt.field, err)
		}
	}
	// The check digit message should help the user.
	_, err := s.Create(ctx, 0, &Item{MediaTypeID: book, Title: "x", Identifiers: []Identifier{{IDIsbn, "9780306406158"}}})
	if msg := fieldErr(err, "identifier_isbn"); msg != "Check digit doesn't match; is it mistyped?" {
		t.Errorf("isbn message = %q", msg)
	}
	var n int
	s.db.QueryRow(`SELECT count(*) FROM items`).Scan(&n)
	if n != 0 {
		t.Errorf("%d items saved despite errors", n)
	}
}

func TestLoanClearedWhenNotLoaned(t *testing.T) {
	s := newStore(t)
	got := mustGet(t, s, mustCreate(t, s, &Item{MediaTypeID: dvd, Title: "x", LoanedTo: "Sam", LoanedOn: "2026-01-01"}))
	if got.LoanedTo != "" || got.LoanedOn != "" {
		t.Errorf("loan kept on an item that isn't loaned: %q %q", got.LoanedTo, got.LoanedOn)
	}
}

func TestUpdate(t *testing.T) {
	s := newStore(t)
	var userID int64
	err := s.db.QueryRow(`INSERT INTO users (username, username_key, password_hash, role, created_at)
		VALUES ('ed', 'ed', 'x', 'editor', 0) RETURNING id`).Scan(&userID)
	if err != nil {
		t.Fatal(err)
	}
	id := mustCreate(t, s, &Item{MediaTypeID: dvd, Title: "Alien", Credits: []Credit{{RoleDirector, "Ridley Scott"}},
		Genres: []string{"Horror"}, Identifiers: []Identifier{{IDImdb, "tt0078748"}}})
	before := mustGet(t, s, id)

	it := mustGet(t, s, id)
	it.Title = "Aliens"
	it.SortTitle = "Alien 2"
	it.Credits = []Credit{{RoleDirector, "James Cameron"}}
	it.Genres = []string{"Action"}
	it.Identifiers = nil
	if err := s.Update(ctx, userID, it); err != nil {
		t.Fatal(err)
	}
	got := mustGet(t, s, id)
	if got.Title != "Aliens" || got.SortTitle != "Alien 2" {
		t.Errorf("title = %q / %q", got.Title, got.SortTitle)
	}
	if !reflect.DeepEqual(got.People(RoleDirector), []string{"James Cameron"}) || !reflect.DeepEqual(got.Genres, []string{"Action"}) {
		t.Errorf("links not replaced: %v %v", got.Credits, got.Genres)
	}
	if got.Identifiers != nil {
		t.Errorf("identifiers = %v, want none", got.Identifiers)
	}
	if got.UpdatedBy != userID || got.AddedBy != 0 || !got.UpdatedAt.After(before.UpdatedAt) || !got.AddedAt.Equal(before.AddedAt) {
		t.Errorf("audit fields: added %v by %d, updated %v by %d", got.AddedAt, got.AddedBy, got.UpdatedAt, got.UpdatedBy)
	}

	// Same kind: allowed. Another kind: refused.
	it.MediaTypeID = vhs
	if err := s.Update(ctx, 0, it); err != nil {
		t.Errorf("DVD to VHS: %v", err)
	}
	it.MediaTypeID = book
	if err := s.Update(ctx, 0, it); fieldErr(err, "media_type") == "" {
		t.Errorf("DVD to Book = %v, want a media_type error", err)
	}
	if got := mustGet(t, s, id); got.MediaTypeID != vhs {
		t.Errorf("media type = %d after refused change, want VHS", got.MediaTypeID)
	}

	it.ID = 999
	if err := s.Update(ctx, 0, it); !errors.Is(err, ErrNotFound) {
		t.Errorf("Update(missing) = %v, want ErrNotFound", err)
	}
}

func TestPeopleShared(t *testing.T) {
	s := newStore(t)
	mustCreate(t, s, &Item{MediaTypeID: book, Title: "a", Credits: []Credit{{RoleAuthor, "Ursula K. Le Guin"}}})
	id := mustCreate(t, s, &Item{MediaTypeID: book, Title: "b", Credits: []Credit{{RoleAuthor, "ursula k. le guin"}}})
	var n int
	s.db.QueryRow(`SELECT count(*) FROM people`).Scan(&n)
	if n != 1 {
		t.Errorf("%d people, want 1 shared", n)
	}
	// The first spelling wins.
	if got := mustGet(t, s, id).People(RoleAuthor); got[0] != "Ursula K. Le Guin" {
		t.Errorf("author = %q", got[0])
	}
}

func TestMediaTypes(t *testing.T) {
	s := newStore(t)
	types, err := s.MediaTypes(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, m := range types {
		names = append(names, m.Name)
	}
	if !reflect.DeepEqual(names, []string{"DVD", "VHS", "Book", "Ebook"}) {
		t.Errorf("seeded = %v", names)
	}

	br, err := s.CreateMediaType(ctx, " Blu-ray ", KindVideo, true)
	if err != nil || br.Name != "Blu-ray" || br.Kind != KindVideo {
		t.Fatalf("CreateMediaType = %+v, %v", br, err)
	}
	types, _ = s.MediaTypes(ctx)
	if types[2].Name != "Blu-ray" {
		t.Errorf("new video type should be listed with the video types: %v", types[2].Name)
	}
	if _, err := s.CreateMediaType(ctx, "dvd", KindVideo, true); fieldErr(err, "name") == "" {
		t.Errorf("duplicate name: %v", err)
	}
	if _, err := s.CreateMediaType(ctx, "Tape", "audio", true); fieldErr(err, "kind") == "" {
		t.Errorf("bad kind: %v", err)
	}
	if err := s.RenameMediaType(ctx, br.ID, "VHS"); fieldErr(err, "name") == "" {
		t.Errorf("rename to taken name: %v", err)
	}
	if err := s.RenameMediaType(ctx, br.ID, "blu-ray"); err != nil {
		t.Errorf("rename to own name in other case: %v", err)
	}
	if err := s.RenameMediaType(ctx, 99, "x"); !errors.Is(err, ErrNotFound) {
		t.Errorf("rename missing: %v", err)
	}

	id := mustCreate(t, s, &Item{MediaTypeID: br.ID, Title: "x"})
	if err := s.SetStatus(ctx, 0, id, StatusTrashed); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteMediaType(ctx, br.ID); !errors.Is(err, ErrInUse) {
		t.Errorf("delete used by a trashed item = %v, want ErrInUse", err)
	}
	if _, err := s.EmptyTrash(ctx); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteMediaType(ctx, br.ID); err != nil {
		t.Errorf("delete unused: %v", err)
	}
}

func TestStatusAndTrash(t *testing.T) {
	s := newStore(t)
	box := mustCreate(t, s, &Item{MediaTypeID: dvd, Title: "Box", Credits: []Credit{{RoleDirector, "Only Here"}}})
	child := mustCreate(t, s, &Item{MediaTypeID: dvd, Title: "Child", ParentID: box, Credits: []Credit{{RoleDirector, "Shared"}}})
	other := mustCreate(t, s, &Item{MediaTypeID: dvd, Title: "Other", Credits: []Credit{{RoleDirector, "Shared"}}})
	mustCreate(t, s, &Item{MediaTypeID: dvd, Title: "Trashed too", Credits: []Credit{{RoleDirector, "Shared"}}, Status: StatusTrashed})

	if err := s.SetStatus(ctx, 0, other, StatusHidden); err != nil {
		t.Fatal(err)
	}
	if err := s.SetStatus(ctx, 0, box, StatusTrashed); err != nil {
		t.Fatal(err)
	}
	if got := mustGet(t, s, box); got.TrashedAt.IsZero() {
		t.Error("trashed_at not set")
	}
	if err := s.SetStatus(ctx, 0, 999, StatusActive); !errors.Is(err, ErrNotFound) {
		t.Errorf("SetStatus(missing) = %v", err)
	}

	n, err := s.EmptyTrash(ctx)
	if err != nil || n != 2 {
		t.Fatalf("EmptyTrash = %d, %v; want 2", n, err)
	}
	if _, err := s.Get(ctx, box); !errors.Is(err, ErrNotFound) {
		t.Errorf("trashed item still there: %v", err)
	}
	if got := mustGet(t, s, child); got.ParentID != 0 {
		t.Errorf("child's parent = %d, want 0 after the box set was deleted", got.ParentID)
	}
	if got := mustGet(t, s, other); got.Status != StatusHidden || !got.TrashedAt.IsZero() {
		t.Errorf("hidden item changed: %v", got.Status)
	}
	var people []string
	eachRow(ctx, s.db, func(r *sql.Rows) error {
		var n string
		r.Scan(&n)
		people = append(people, n)
		return nil
	}, `SELECT name FROM people ORDER BY name`)
	if !reflect.DeepEqual(people, []string{"Shared"}) {
		t.Errorf("people after emptying trash = %v, want [Shared]", people)
	}
}

func TestDuplicates(t *testing.T) {
	s := newStore(t)
	a := mustCreate(t, s, &Item{MediaTypeID: book, Title: "A", Identifiers: []Identifier{{IDIsbn, "0-306-40615-2"}}})
	b := mustCreate(t, s, &Item{MediaTypeID: book, Title: "B", Identifiers: []Identifier{{IDIsbn, "9780306406157"}}})
	mustCreate(t, s, &Item{MediaTypeID: book, Title: "C", Identifiers: []Identifier{{IDEan, "9780306406157"}}})

	d, err := s.Duplicates(ctx, a)
	if err != nil {
		t.Fatal(err)
	}
	// The same ISBN, typed differently, matches; the same digits as an EAN don't.
	if len(d) != 1 || d[0].ItemID != b || d[0].Value != "9780306406157" {
		t.Errorf("Duplicates = %+v, want item %d", d, b)
	}
}

func titles(p *Page) []string {
	out := []string{}
	for _, it := range p.Items {
		out = append(out, it.Title)
	}
	return out
}

func TestListFilters(t *testing.T) {
	s := newStore(t)
	horror := []string{"Horror"}
	mustCreate(t, s, &Item{MediaTypeID: dvd, Title: "Alien", Year: 1979, Runtime: 117, Genres: horror, Own: true, Seen: true,
		Credits: []Credit{{RoleDirector, "Ridley Scott"}}})
	mustCreate(t, s, &Item{MediaTypeID: vhs, Title: "The Thing", Year: 1982, Genres: horror, Own: true, Favourite: true,
		Notes: "100% creepy"})
	mustCreate(t, s, &Item{MediaTypeID: dvd, Title: "Twin Peaks", Year: 1990, Runtime: 47, IsTV: true, Loaned: true, LoanedTo: "Lena"})
	mustCreate(t, s, &Item{MediaTypeID: book, Title: "Höðrs saga", Year: 1200, Credits: []Credit{{RoleAuthor, "Snorri"}}})
	mustCreate(t, s, &Item{MediaTypeID: ebook, Title: "Draft", AltTitles: []string{"Hidden one"}, Status: StatusHidden})

	var horrorID int64
	gs, _ := s.Genres(ctx)
	for _, g := range gs {
		if g.Name == "Horror" {
			horrorID = g.ID
		}
	}

	tests := []struct {
		name string
		q    Query
		want []string
	}{
		{"all active", Query{}, []string{"Alien", "Höðrs saga", "The Thing", "Twin Peaks"}},
		{"search title", Query{Search: "thing"}, []string{"The Thing"}},
		{"search non-ASCII case", Query{Search: "HÖÐRS"}, []string{"Höðrs saga"}},
		{"search person", Query{Search: "scott"}, []string{"Alien"}},
		{"search all words", Query{Search: "ridley alien"}, []string{"Alien"}},
		{"search words anywhere", Query{Search: "ridley thing"}, []string{}},
		{"notes are private", Query{Search: "creepy"}, []string{}},
		{"search notes", Query{Search: "creepy", Private: true}, []string{"The Thing"}},
		{"search borrower", Query{Search: "lena", Private: true}, []string{"Twin Peaks"}},
		{"borrower is private", Query{Search: "lena"}, []string{}},
		{"private and public words", Query{Search: "creepy thing", Private: true}, []string{"The Thing"}},
		{"percent is literal", Query{Search: "%", Private: true}, []string{"The Thing"}},
		{"underscore is literal", Query{Search: "_"}, []string{}},
		{"media type", Query{MediaTypeID: dvd}, []string{"Alien", "Twin Peaks"}},
		{"kind", Query{Kind: KindBook}, []string{"Höðrs saga"}},
		{"genre", Query{GenreID: horrorID}, []string{"Alien", "The Thing"}},
		{"incomplete", Query{Incomplete: true}, []string{"The Thing"}},
		{"own", Query{Own: Yes}, []string{"Alien", "The Thing"}},
		{"not own", Query{Own: No}, []string{"Höðrs saga", "Twin Peaks"}},
		{"seen", Query{Seen: Yes}, []string{"Alien"}},
		{"favourite", Query{Favourite: Yes}, []string{"The Thing"}},
		{"loaned", Query{Loaned: Yes}, []string{"Twin Peaks"}},
		{"tv", Query{TV: Yes}, []string{"Twin Peaks"}},
		{"films", Query{TV: No, Kind: KindVideo}, []string{"Alien", "The Thing"}},
		{"hidden", Query{Status: StatusHidden}, []string{"Draft"}},
		{"hidden search alt title", Query{Status: StatusHidden, Search: "one"}, []string{"Draft"}},
		{"trashed", Query{Status: StatusTrashed}, []string{}},
		{"bad status means active", Query{Status: "nonsense", Search: "draft"}, []string{}},
	}
	for _, tt := range tests {
		p, err := s.List(ctx, tt.q)
		if err != nil {
			t.Fatalf("%s: %v", tt.name, err)
		}
		if got := titles(p); !reflect.DeepEqual(got, tt.want) {
			t.Errorf("%s: got %q, want %q", tt.name, got, tt.want)
		}
		if p.Total != len(tt.want) {
			t.Errorf("%s: total = %d, want %d", tt.name, p.Total, len(tt.want))
		}
	}

	p, _ := s.List(ctx, Query{Search: "alien"})
	if a := p.Items[0]; !reflect.DeepEqual(a.People, []string{"Ridley Scott"}) || a.MediaType != "DVD" || a.Incomplete {
		t.Errorf("summary = %+v", a)
	}
}

func TestListSortAndPaging(t *testing.T) {
	s := newStore(t)
	for i := 1; i <= 120; i++ {
		mustCreate(t, s, &Item{MediaTypeID: dvd, Title: fmt.Sprintf("Film %03d", i), Year: 1900 + i%7})
	}
	p, err := s.List(ctx, Query{Page: 3})
	if err != nil {
		t.Fatal(err)
	}
	if p.Total != 120 || p.Pages != 3 || len(p.Items) != 20 || p.Items[0].Title != "Film 101" {
		t.Errorf("page 3: total %d, pages %d, %d items, first %q", p.Total, p.Pages, len(p.Items), p.Items[0].Title)
	}
	p, _ = s.List(ctx, Query{Page: 9})
	if len(p.Items) != 0 || p.Pages != 3 {
		t.Errorf("page past the end: %d items", len(p.Items))
	}
	p, _ = s.List(ctx, Query{Sort: SortAdded, Desc: true})
	if p.Items[0].Title != "Film 120" {
		t.Errorf("newest first = %q", p.Items[0].Title)
	}
	p, _ = s.List(ctx, Query{Sort: "nonsense"})
	if p.Items[0].Title != "Film 001" {
		t.Errorf("unknown sort should sort by title, got %q", p.Items[0].Title)
	}
}

func TestListSortNulls(t *testing.T) {
	s := newStore(t)
	mustCreate(t, s, &Item{MediaTypeID: dvd, Title: "No year"})
	mustCreate(t, s, &Item{MediaTypeID: dvd, Title: "Old", Year: 1950})
	mustCreate(t, s, &Item{MediaTypeID: dvd, Title: "New", Year: 2020})
	mustCreate(t, s, &Item{MediaTypeID: book, Title: "Part 10", Series: "Saga", SeriesNumber: "10"})
	mustCreate(t, s, &Item{MediaTypeID: book, Title: "Part 2", Series: "saga", SeriesNumber: "2"})
	mustCreate(t, s, &Item{MediaTypeID: book, Title: "Part 1.5", Series: "Saga", SeriesNumber: "1.5"})
	mustCreate(t, s, &Item{MediaTypeID: book, Title: "An Abbey", Series: "Abbey"})
	// A type added later still sorts with its kind.
	br, err := s.CreateMediaType(ctx, "Blu-ray", KindVideo, true)
	if err != nil {
		t.Fatal(err)
	}
	mustCreate(t, s, &Item{MediaTypeID: br.ID, Title: "Zulu"})

	tests := []struct {
		q    Query
		want []string
	}{
		{Query{Sort: SortYear, Kind: KindVideo}, []string{"Old", "New", "No year", "Zulu"}},
		{Query{Sort: SortYear, Kind: KindVideo, Desc: true}, []string{"New", "Old", "No year", "Zulu"}},
		{Query{Sort: SortSeries, Kind: KindBook}, []string{"An Abbey", "Part 1.5", "Part 2", "Part 10"}},
		{Query{Sort: SortTitle, Kind: KindBook}, []string{"An Abbey", "Part 1.5", "Part 10", "Part 2"}},
		{Query{Sort: SortMediaType}, []string{"New", "No year", "Old", "Zulu", "An Abbey", "Part 1.5", "Part 10", "Part 2"}},
		{Query{Sort: SortMediaType, Desc: true}, []string{"An Abbey", "Part 1.5", "Part 10", "Part 2", "Zulu", "New", "No year", "Old"}},
	}
	for _, tt := range tests {
		p, err := s.List(ctx, tt.q)
		if err != nil {
			t.Fatal(err)
		}
		if got := titles(p); !reflect.DeepEqual(got, tt.want) {
			t.Errorf("%+v: got %q, want %q", tt.q, got, tt.want)
		}
	}
}

func TestRebuildSearch(t *testing.T) {
	s := newStore(t)
	id := mustCreate(t, s, &Item{MediaTypeID: dvd, Title: "Alien", Notes: "Signed copy"})
	// Simulate a database from before migration 0005: notes in the public
	// text, nothing in the private one.
	if _, err := s.db.Exec(`UPDATE items SET search_text = 'alien signed copy', search_private = '' WHERE id = ?`, id); err != nil {
		t.Fatal(err)
	}
	if p, _ := s.List(ctx, Query{Search: "signed"}); p.Total != 1 {
		t.Fatal("setup: old data should leak")
	}
	n, err := s.RebuildSearch(ctx)
	if err != nil || n != 1 {
		t.Fatalf("RebuildSearch = %d, %v", n, err)
	}
	if p, _ := s.List(ctx, Query{Search: "signed"}); p.Total != 0 {
		t.Error("notes still in the public search text")
	}
	if p, _ := s.List(ctx, Query{Search: "signed", Private: true}); p.Total != 1 {
		t.Error("notes not in the private search text")
	}
}

func TestSetFlag(t *testing.T) {
	s := newStore(t)
	id := mustCreate(t, s, &Item{MediaTypeID: dvd, Title: "Alien"})
	before := mustGet(t, s, id)
	for _, f := range []string{FlagSeen, FlagFavourite} {
		if err := s.SetFlag(ctx, 0, id, f, true); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.SetFlag(ctx, 0, id, FlagOwn, false); err != nil {
		t.Fatal(err)
	}
	got := mustGet(t, s, id)
	if got.Own || !got.Seen || !got.Favourite || !got.UpdatedAt.After(before.UpdatedAt) {
		t.Errorf("flags = own %v seen %v favourite %v", got.Own, got.Seen, got.Favourite)
	}
	if err := s.SetFlag(ctx, 0, id, "loaned; DROP TABLE items", true); err == nil {
		t.Error("unknown flag accepted")
	}
	if err := s.SetFlag(ctx, 0, 999, FlagSeen, true); !errors.Is(err, ErrNotFound) {
		t.Errorf("missing item: %v", err)
	}
}

func TestCover(t *testing.T) {
	s := newStore(t)
	h1 := strings.Repeat("ab", 32)
	h2 := strings.Repeat("cd", 32)
	a := mustCreate(t, s, &Item{MediaTypeID: 1, Title: "Alien", Cover: h1})
	b := mustCreate(t, s, &Item{MediaTypeID: 1, Title: "Brazil", Cover: h1}) // shared
	c := mustCreate(t, s, &Item{MediaTypeID: 1, Title: "Casablanca"})
	if got := mustGet(t, s, a).Cover; got != h1 {
		t.Errorf("Cover = %q", got)
	}

	// Anything but a hash is refused, by Clean and by the database.
	_, err := s.Create(ctx, 0, &Item{MediaTypeID: 1, Title: "X", Cover: "../../etc/passwd"})
	if fieldErr(err, "cover") == "" {
		t.Errorf("bad cover: err = %v", err)
	}
	if _, err := s.db.Exec(`UPDATE items SET cover = 'nope' WHERE id = ?`, c); err == nil {
		t.Error("database accepted a bad cover")
	}

	// The filter, and the hash in list results.
	p, err := s.List(ctx, Query{HasCover: Yes})
	if err != nil {
		t.Fatal(err)
	}
	if got := titles(p); !slices.Equal(got, []string{"Alien", "Brazil"}) {
		t.Errorf("with cover = %v", got)
	}
	if p.Items[0].Cover != h1 {
		t.Errorf("Summary.Cover = %q", p.Items[0].Cover)
	}
	p, _ = s.List(ctx, Query{HasCover: No})
	if got := titles(p); !slices.Equal(got, []string{"Casablanca"}) {
		t.Errorf("without cover = %v", got)
	}

	// Covers in use: trashed items count until the trash is emptied.
	it := mustGet(t, s, b)
	it.Cover = h2
	if err := s.Update(ctx, 0, it); err != nil {
		t.Fatal(err)
	}
	s.SetStatus(ctx, 0, b, StatusTrashed)
	used, err := s.CoverHashes(ctx)
	if err != nil || len(used) != 2 || !used[h1] || !used[h2] {
		t.Errorf("CoverHashes = %v, %v", used, err)
	}
	s.EmptyTrash(ctx)
	if used, _ = s.CoverHashes(ctx); len(used) != 1 || !used[h1] {
		t.Errorf("after emptying the trash: %v", used)
	}
}
