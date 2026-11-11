// Package item stores the library: media types, items and what hangs off
// them (credits, genres, lists, identifiers). Spec section 3.
//
// Handlers build an Item from a form, call Create or Update, and show the
// FieldErrors they get back. All rules live here.
package item

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

// Kind groups media types and decides which fields apply (spec 3.1).
type Kind string

const (
	KindVideo Kind = "video"
	KindBook  Kind = "book"
)

// Kinds lists the kinds in menu order.
var Kinds = []Kind{KindVideo, KindBook}

func (k Kind) Valid() bool { return k == KindVideo || k == KindBook }

// Label is the kind's name for menus.
func (k Kind) Label() string {
	switch k {
	case KindVideo:
		return "Video"
	case KindBook:
		return "Book"
	}
	return string(k)
}

// Status is an item's lifecycle state (spec 3.6).
type Status string

const (
	StatusActive  Status = "active"
	StatusHidden  Status = "hidden"
	StatusTrashed Status = "trashed"
)

func (s Status) Valid() bool {
	return s == StatusActive || s == StatusHidden || s == StatusTrashed
}

// Credit roles. Which ones a form shows depends on the kind (RolesFor).
const (
	RoleDirector = "director"
	RoleCast     = "cast"
	RoleWriter   = "writer"
	RoleProducer = "producer"
	RoleComposer = "composer"
	RoleAuthor   = "author"
)

var rolesByKind = map[Kind][]string{
	KindVideo: {RoleDirector, RoleCast, RoleWriter, RoleProducer, RoleComposer},
	KindBook:  {RoleAuthor},
}

// RolesFor returns the credit roles that apply to a kind, in form order.
func RolesFor(k Kind) []string { return rolesByKind[k] }

func validRole(role string) bool {
	for _, roles := range rolesByKind {
		for _, r := range roles {
			if r == role {
				return true
			}
		}
	}
	return false
}

// Credit is one person in one role. Position is the order in the slice.
type Credit struct {
	Role string
	Name string
}

// Fixed choices for some fields. "" means not set.
var (
	VideoStandards = []string{"PAL", "NTSC", "SECAM"}
	Bindings       = []string{"hardcover", "paperback", "other"}
	FileFormats    = []string{"epub", "pdf"}
)

// Item is one thing in the library. Zero values mean "not set": 0 for
// numbers, "" for text, nil for lists. They are stored as NULL.
type Item struct {
	ID          int64
	MediaTypeID int64
	ParentID    int64
	Status      Status

	Title         string
	SortTitle     string // only if typed by a user; see EffectiveSortTitle
	OriginalTitle string
	AltTitles     []string
	Year          int
	Description   string
	Notes         string
	OwnRating     int // 1–10
	Series        string
	SeriesNumber  string

	Own, Seen, Favourite bool
	Loaned               bool
	LoanedTo             string
	LoanedOn             string // YYYY-MM-DD
	Trailer              string
	MPAA                 string
	AgeLimit             *int // a pointer, because 0 ("all ages") is a real value

	Credits     []Credit
	Genres      []string
	Languages   []string
	Subtitles   []string
	Countries   []string
	Identifiers []Identifier

	// Video
	Runtime       int
	Region        string
	VideoStandard string
	IsTV          bool
	Seasons       string
	AudioFormat   string
	VideoFormat   string

	// Book
	Publisher  string
	Pages      int
	Binding    string
	FileFormat string
	FilePath   string

	// Filled in by Get, ignored by Create and Update.
	MediaType *MediaType
	TrashedAt time.Time
	AddedAt   time.Time
	UpdatedAt time.Time
	AddedBy   int64
	UpdatedBy int64
}

// EffectiveSortTitle is the typed sort title, or one derived from the title.
func (it *Item) EffectiveSortTitle() string {
	if it.SortTitle != "" {
		return it.SortTitle
	}
	return DeriveSortTitle(it.Title)
}

// People returns the names credited in role, in order.
func (it *Item) People(role string) []string {
	var names []string
	for _, c := range it.Credits {
		if c.Role == role {
			names = append(names, c.Name)
		}
	}
	return names
}

// Incomplete reports whether key fields for the item's kind are missing
// (spec 3.2). It needs MediaType, which Get fills in.
func (it *Item) Incomplete() bool {
	if it.Year == 0 {
		return true
	}
	if it.MediaType == nil {
		return false
	}
	switch it.MediaType.Kind {
	case KindVideo:
		return it.Runtime == 0
	case KindBook:
		return len(it.People(RoleAuthor)) == 0
	}
	return false
}

// FieldErrors maps form field names to messages. It is an error, so Create
// and Update can return it; handlers show each message next to its field.
type FieldErrors map[string]string

func (fe FieldErrors) Error() string {
	var parts []string
	for f, m := range fe {
		parts = append(parts, f+": "+m)
	}
	return "invalid item: " + strings.Join(parts, "; ")
}

var (
	ErrNotFound = errors.New("not found")
	ErrInUse    = errors.New("media type is used by items")
)

// Limits on what is accepted.
const (
	maxLine  = 500    // one-line fields: titles, names, ...
	maxBlock = 100000 // multi-line fields: description, notes
	maxList  = 500    // entries in one list (cast, genres, ...)
)

// Store reads and writes the library.
type Store struct {
	db  *sql.DB
	now func() time.Time // replaceable in tests
}

func NewStore(db *sql.DB) *Store {
	return &Store{db: db, now: time.Now}
}

// Create validates and saves a new item, and returns its ID. userID is the
// user adding it (0 for none, e.g. an import).
func (s *Store) Create(ctx context.Context, userID int64, it *Item) (int64, error) {
	var id int64
	err := s.inTx(ctx, func(tx *sql.Tx) error {
		mt, err := s.prepare(ctx, tx, it)
		if err != nil {
			return err
		}
		now := s.now().Unix()
		cols, vals := itemColumns(it, mt)
		cols = append(cols, "status", "added_at", "updated_at", "added_by", "updated_by")
		status := it.Status
		if status == "" {
			status = StatusActive
		}
		vals = append(vals, string(status), now, now, nullID(userID), nullID(userID))
		if status == StatusTrashed {
			cols, vals = append(cols, "trashed_at"), append(vals, now)
		}
		q := `INSERT INTO items (` + strings.Join(cols, ", ") + `) VALUES (` + placeholders(len(cols)) + `)`
		res, err := tx.ExecContext(ctx, q, vals...)
		if err != nil {
			return err
		}
		if id, err = res.LastInsertId(); err != nil {
			return err
		}
		return s.saveLinks(ctx, tx, id, it)
	})
	return id, err
}

// Update validates and saves changes to an existing item. Status, added_*
// and parent are not changed here (see SetStatus). The media type may
// change, but only to one of the same kind (spec 3.1).
func (s *Store) Update(ctx context.Context, userID int64, it *Item) error {
	return s.inTx(ctx, func(tx *sql.Tx) error {
		var oldKind Kind
		err := tx.QueryRowContext(ctx, `SELECT m.kind FROM items i JOIN media_types m ON m.id = i.media_type_id
			WHERE i.id = ?`, it.ID).Scan(&oldKind)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}
		mt, err := s.prepare(ctx, tx, it)
		if err != nil {
			return err
		}
		if mt.Kind != oldKind {
			return FieldErrors{"media_type": "An item can't change to a media type of another kind."}
		}
		cols, vals := itemColumns(it, mt)
		cols = append(cols, "updated_at", "updated_by")
		vals = append(vals, s.now().Unix(), nullID(userID))
		sets := make([]string, len(cols))
		for i, c := range cols {
			sets[i] = c + " = ?"
		}
		res, err := tx.ExecContext(ctx, `UPDATE items SET `+strings.Join(sets, ", ")+` WHERE id = ?`,
			append(vals, it.ID)...)
		if err != nil {
			return err
		}
		if n, err := res.RowsAffected(); err != nil {
			return err
		} else if n == 0 {
			return ErrNotFound
		}
		return s.saveLinks(ctx, tx, it.ID, it)
	})
}

// prepare cleans and validates it in place and returns its media type.
func (s *Store) prepare(ctx context.Context, tx *sql.Tx, it *Item) (*MediaType, error) {
	errs := Clean(it)
	mt, err := mediaTypeByID(ctx, tx, it.MediaTypeID)
	switch {
	case errors.Is(err, ErrNotFound):
		errs["media_type"] = "Choose a media type."
	case err != nil:
		return nil, err
	}
	if it.ParentID != 0 {
		if it.ParentID == it.ID {
			errs["parent"] = "An item can't contain itself."
		} else if ok, err := exists(ctx, tx, `SELECT 1 FROM items WHERE id = ?`, it.ParentID); err != nil {
			return nil, err
		} else if !ok {
			errs["parent"] = "The box set doesn't exist."
		}
	}
	if len(errs) > 0 {
		return nil, errs
	}
	return mt, nil
}

// Clean trims and normalises every field of it in place and returns what
// is wrong. Create and Update call it; handlers can call it too, to show
// all problems at once alongside their own (such as "not a number").
// Checks that need the database (media type, parent) are not done here.
func Clean(it *Item) FieldErrors {
	errs := FieldErrors{}
	line := func(field string, p *string) {
		*p = cleanText(*p)
		if len(*p) > maxLine {
			errs[field] = fmt.Sprintf("Too long (at most %d characters).", maxLine)
		} else if hasControl(*p) {
			errs[field] = "Contains characters that aren't allowed."
		}
	}
	block := func(field string, p *string) {
		*p = cleanBlock(*p)
		if len(*p) > maxBlock {
			errs[field] = "Too long."
		}
	}
	list := func(field string, p *[]string) {
		*p = cleanList(*p)
		if len(*p) > maxList {
			errs[field] = fmt.Sprintf("Too many entries (at most %d).", maxList)
		}
		for _, v := range *p {
			if len(v) > maxLine {
				errs[field] = fmt.Sprintf("An entry is too long (at most %d characters).", maxLine)
			}
		}
	}
	number := func(field string, v, min, max int) {
		if v != 0 && (v < min || v > max) {
			errs[field] = fmt.Sprintf("Must be between %d and %d.", min, max)
		}
	}
	choice := func(field string, p *string, choices []string) {
		*p = strings.TrimSpace(*p)
		if *p == "" {
			return
		}
		for _, c := range choices {
			if strings.EqualFold(*p, c) {
				*p = c
				return
			}
		}
		errs[field] = "Not one of the choices."
	}

	line("title", &it.Title)
	if it.Title == "" {
		errs["title"] = "A title is required."
	}
	line("sort_title", &it.SortTitle)
	if it.SortTitle == DeriveSortTitle(it.Title) {
		it.SortTitle = "" // same as derived: don't freeze it
	}
	line("original_title", &it.OriginalTitle)
	list("alt_titles", &it.AltTitles)
	number("year", it.Year, 1000, 2999)
	block("description", &it.Description)
	block("notes", &it.Notes)
	number("own_rating", it.OwnRating, 1, 10)
	line("series", &it.Series)
	line("series_number", &it.SeriesNumber)

	line("loaned_to", &it.LoanedTo)
	it.LoanedOn = strings.TrimSpace(it.LoanedOn)
	if it.LoanedOn != "" {
		if _, err := time.Parse(time.DateOnly, it.LoanedOn); err != nil {
			errs["loaned_on"] = "Use a date like 2026-01-31."
		}
	}
	if !it.Loaned {
		it.LoanedTo, it.LoanedOn = "", ""
	}
	line("trailer", &it.Trailer)
	if it.Trailer != "" && !strings.HasPrefix(it.Trailer, "https://") && !strings.HasPrefix(it.Trailer, "http://") {
		errs["trailer"] = "Must be a link starting with https://."
	}
	line("mpaa", &it.MPAA)
	if it.AgeLimit != nil && (*it.AgeLimit < 0 || *it.AgeLimit > 21) {
		errs["age_limit"] = "Must be between 0 and 21."
	}

	for i := range it.Credits {
		c := &it.Credits[i] // a pointer into the slice, so the change sticks
		c.Name = cleanText(c.Name)
		if !validRole(c.Role) {
			errs["credits"] = "Unknown role " + c.Role + "."
		} else if len(c.Name) > maxLine {
			errs[c.Role] = fmt.Sprintf("A name is too long (at most %d characters).", maxLine)
		}
	}
	it.Credits = cleanCredits(it.Credits)
	if len(it.Credits) > maxList*2 {
		errs["credits"] = "Too many people."
	}
	list("genres", &it.Genres)
	list("languages", &it.Languages)
	list("subtitles", &it.Subtitles)
	list("countries", &it.Countries)

	var ids []Identifier
	seen := map[Identifier]bool{}
	for _, id := range it.Identifiers {
		if strings.TrimSpace(id.Value) == "" {
			continue
		}
		v, err := NormaliseIdentifier(id.Type, id.Value)
		if err != nil {
			errs["identifier_"+id.Type] = identifierMessage(id.Type, err)
			continue
		}
		n := Identifier{Type: id.Type, Value: v}
		if !seen[n] {
			seen[n] = true
			ids = append(ids, n)
		}
	}
	it.Identifiers = ids

	number("runtime", it.Runtime, 1, 100000)
	line("region", &it.Region)
	choice("video_standard", &it.VideoStandard, VideoStandards)
	line("seasons", &it.Seasons)
	line("audio_format", &it.AudioFormat)
	line("video_format", &it.VideoFormat)

	line("publisher", &it.Publisher)
	number("pages", it.Pages, 1, 1000000)
	choice("binding", &it.Binding, Bindings)
	choice("file_format", &it.FileFormat, FileFormats)
	line("file_path", &it.FilePath)

	if !it.Status.Valid() && it.Status != "" {
		errs["status"] = "Unknown status."
	}
	return errs
}

func identifierMessage(typ string, err error) string {
	if errors.Is(err, errIdentifierType) {
		return "Unknown identifier type " + typ + "."
	}
	// Capitalise the first letter: "check digit ..." → "Check digit ...".
	msg := err.Error()
	msg = strings.ToUpper(msg[:1]) + msg[1:]
	if !strings.HasSuffix(msg, "?") {
		msg += "."
	}
	return msg
}

// cleanCredits drops empty names and the same person twice in one role.
func cleanCredits(in []Credit) []Credit {
	var out []Credit
	seen := map[Credit]bool{}
	for _, c := range in {
		k := Credit{Role: c.Role, Name: key(c.Name)}
		if c.Name == "" || seen[k] {
			continue
		}
		seen[k] = true
		out = append(out, c)
	}
	return out
}

// itemColumns returns the item table's editable columns and their values.
// Zero values become NULL.
func itemColumns(it *Item, mt *MediaType) ([]string, []any) {
	var cols []string
	var vals []any
	add := func(col string, v any) {
		cols = append(cols, col)
		vals = append(vals, v)
	}
	add("media_type_id", mt.ID)
	add("parent_id", nullID(it.ParentID))
	add("title", it.Title)
	add("sort_title", nullText(it.SortTitle))
	add("sort_key", key(it.EffectiveSortTitle()))
	add("original_title", nullText(it.OriginalTitle))
	add("alt_titles", nullText(strings.Join(it.AltTitles, "\n")))
	add("year", nullInt(it.Year))
	add("description", nullText(it.Description))
	add("notes", nullText(it.Notes))
	add("own_rating", nullInt(it.OwnRating))
	add("series", nullText(it.Series))
	add("series_key", nullText(key(it.Series)))
	add("series_number", nullText(it.SeriesNumber))
	add("own", it.Own)
	add("seen", it.Seen)
	add("favourite", it.Favourite)
	add("loaned", it.Loaned)
	add("loaned_to", nullText(it.LoanedTo))
	add("loaned_on", nullText(it.LoanedOn))
	add("trailer", nullText(it.Trailer))
	add("mpaa", nullText(it.MPAA))
	if it.AgeLimit != nil {
		add("age_limit", *it.AgeLimit)
	} else {
		add("age_limit", nil)
	}
	add("runtime", nullInt(it.Runtime))
	add("region", nullText(it.Region))
	add("video_standard", nullText(it.VideoStandard))
	add("is_tv", it.IsTV)
	add("seasons", nullText(it.Seasons))
	add("audio_format", nullText(it.AudioFormat))
	add("video_format", nullText(it.VideoFormat))
	add("publisher", nullText(it.Publisher))
	add("pages", nullInt(it.Pages))
	add("binding", nullText(it.Binding))
	add("file_format", nullText(it.FileFormat))
	add("file_path", nullText(it.FilePath))
	text, private := searchColumns(it)
	add("search_text", text)
	add("search_private", private)
	return cols, vals
}

// SearchVersion is the version of what searchColumns builds. Raise it
// when that changes; the server then calls RebuildSearch at startup.
const SearchVersion = 1

// searchColumns builds the lowercased search texts (spec 5). Notes and the
// borrower go in the private one, which only logged-in users search.
func searchColumns(it *Item) (text, private string) {
	people := make([]string, len(it.Credits))
	for i, c := range it.Credits {
		people[i] = c.Name
	}
	text = searchText(it.Title, it.OriginalTitle, strings.Join(it.AltTitles, "\n"),
		it.Series, strings.Join(people, "\n"))
	return text, searchText(it.Notes, it.LoanedTo)
}

// RebuildSearch recomputes the search texts of all items and returns how
// many it updated.
func (s *Store) RebuildSearch(ctx context.Context) (int, error) {
	var ids []int64
	err := eachRow(ctx, s.db, func(r *sql.Rows) error {
		var id int64
		err := r.Scan(&id)
		ids = append(ids, id)
		return err
	}, `SELECT id FROM items ORDER BY id`)
	if err != nil {
		return 0, err
	}
	for _, id := range ids {
		it, err := s.Get(ctx, id)
		if err != nil {
			return 0, err
		}
		text, private := searchColumns(it)
		if _, err := s.db.ExecContext(ctx, `UPDATE items SET search_text = ?, search_private = ? WHERE id = ?`,
			text, private, id); err != nil {
			return 0, err
		}
	}
	return len(ids), nil
}

// Flags that SetFlag can change.
const (
	FlagOwn       = "own"
	FlagSeen      = "seen"
	FlagFavourite = "favourite"
)

// SetFlag sets own, seen or favourite on one item, for the in-place
// toggles in the library list.
func (s *Store) SetFlag(ctx context.Context, userID, id int64, flag string, on bool) error {
	switch flag {
	case FlagOwn, FlagSeen, FlagFavourite: // the column name comes from this fixed list only
	default:
		return errors.New("unknown flag " + flag)
	}
	res, err := s.db.ExecContext(ctx, `UPDATE items SET `+flag+` = ?, updated_at = ?, updated_by = ? WHERE id = ?`,
		on, s.now().Unix(), nullID(userID), id)
	if err != nil {
		return err
	}
	if n, err := res.RowsAffected(); err != nil {
		return err
	} else if n == 0 {
		return ErrNotFound
	}
	return nil
}

// saveLinks replaces the item's credits, genres, lists and identifiers.
func (s *Store) saveLinks(ctx context.Context, tx *sql.Tx, id int64, it *Item) error {
	for _, table := range []string{"item_credits", "item_genres", "item_values", "identifiers"} {
		if _, err := tx.ExecContext(ctx, `DELETE FROM `+table+` WHERE item_id = ?`, id); err != nil {
			return err
		}
	}

	positions := map[string]int{} // next position per role
	for _, c := range it.Credits {
		pid, err := upsertName(ctx, tx, "people", c.Name)
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO item_credits (item_id, person_id, role, position) VALUES (?, ?, ?, ?)`,
			id, pid, c.Role, positions[c.Role]); err != nil {
			return err
		}
		positions[c.Role]++
	}

	for _, g := range it.Genres {
		gid, err := upsertName(ctx, tx, "genres", g)
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO item_genres (item_id, genre_id) VALUES (?, ?)`, id, gid); err != nil {
			return err
		}
	}

	lists := []struct {
		field  string
		values []string
	}{{"language", it.Languages}, {"subtitle", it.Subtitles}, {"country", it.Countries}}
	for _, l := range lists {
		for i, v := range l.values {
			if _, err := tx.ExecContext(ctx,
				`INSERT INTO item_values (item_id, field, position, value) VALUES (?, ?, ?, ?)`,
				id, l.field, i, v); err != nil {
				return err
			}
		}
	}

	for _, ident := range it.Identifiers {
		if _, err := tx.ExecContext(ctx, `INSERT INTO identifiers (item_id, type, value) VALUES (?, ?, ?)`,
			id, ident.Type, ident.Value); err != nil {
			return err
		}
	}
	return nil
}

// upsertName returns the id of the person or genre with this name
// (ignoring case), creating it if needed. The first spelling saved wins.
func upsertName(ctx context.Context, tx *sql.Tx, table, name string) (int64, error) {
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO `+table+` (name, name_key) VALUES (?, ?) ON CONFLICT (name_key) DO NOTHING`,
		name, key(name)); err != nil {
		return 0, err
	}
	var id int64
	err := tx.QueryRowContext(ctx, `SELECT id FROM `+table+` WHERE name_key = ?`, key(name)).Scan(&id)
	return id, err
}

func (s *Store) inTx(ctx context.Context, fn func(*sql.Tx) error) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	// Rollback after a successful Commit does nothing, so deferring it is
	// a safe way to undo everything on any early return.
	defer tx.Rollback()
	if err := fn(tx); err != nil {
		return err
	}
	return tx.Commit()
}

// querier is satisfied by both *sql.DB and *sql.Tx.
type querier interface {
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

func exists(ctx context.Context, q querier, query string, args ...any) (bool, error) {
	var one int
	err := q.QueryRowContext(ctx, query, args...).Scan(&one)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	return err == nil, err
}

func placeholders(n int) string {
	return strings.TrimSuffix(strings.Repeat("?, ", n), ", ")
}

// nullText, nullInt and nullID store empty values as SQL NULL. A nil `any`
// is sent to the database as NULL.
func nullText(s string) any {
	if s == "" {
		return nil
	}
	return s
}

func nullInt(n int) any {
	if n == 0 {
		return nil
	}
	return n
}

func nullID(id int64) any {
	if id == 0 {
		return nil
	}
	return id
}
