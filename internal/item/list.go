package item

import (
	"context"
	"database/sql"
	"strings"
)

// Listing items: filters, sorting and paging (spec 5).

// PerPage is the number of items on one page of a list.
const PerPage = 50

// Flag is a yes/no filter that can also be off.
type Flag int

const (
	Any Flag = iota // iota counts up from 0 in a const block: Any=0, Yes=1, No=2
	Yes
	No
)

// Sort orders for List.
const (
	SortTitle     = "title"
	SortYear      = "year"
	SortAdded     = "added"
	SortMediaType = "media_type"
	SortSeries    = "series"
	// SortTrashed is for the trash page; it isn't offered in Sorts.
	SortTrashed = "trashed"
)

// Sorts lists the sort orders in menu order.
var Sorts = []string{SortTitle, SortYear, SortAdded, SortMediaType, SortSeries}

// sortSQL is the ORDER BY for each sort. {dir} becomes ASC or DESC; columns
// without it only break ties.
var sortSQL = map[string]string{
	SortTitle: "i.sort_key {dir}, i.id",
	// NULLS LAST keeps items without a year (or series) at the end either way.
	SortYear:  "i.year {dir} NULLS LAST, i.sort_key, i.id",
	SortAdded: "i.added_at {dir}, i.id {dir}",
	// Grouped by kind first, as in the menus (see MediaTypes).
	SortMediaType: kindOrder + " {dir}, m.sort_order {dir}, m.name_key {dir}, i.sort_key, i.id",
	SortTrashed: "i.trashed_at {dir}, i.id {dir}",
	// Series numbers are text ("1.5", "III"); sort numerically where they
	// are numbers, then as text.
	SortSeries: "i.series_key {dir} NULLS LAST, CAST(i.series_number AS REAL) {dir}, i.series_number {dir}, i.sort_key, i.id",
}

// kindOrder puts video before book.
const kindOrder = "CASE m.kind WHEN 'video' THEN 0 ELSE 1 END"

// Query says which items List returns. The zero value lists active items
// by title, first page.
type Query struct {
	Search      string
	MediaTypeID int64
	Kind        Kind
	GenreID     int64
	// Status: "" means active. Callers must not let viewers ask for
	// hidden items (spec 3.6).
	Status     Status
	Incomplete bool
	Own        Flag
	Seen       Flag
	Favourite  Flag
	Loaned     Flag
	TV         Flag

	Sort string // one of Sorts; "" means title
	Desc bool
	Page int // 1-based; 0 means 1
}

// Summary is an item as shown in lists.
type Summary struct {
	ID           int64
	Title        string
	Year         int
	MediaType    string
	Kind         Kind
	Series       string
	SeriesNumber string
	Runtime      int
	Status       Status
	Own          bool
	Seen         bool
	Favourite    bool
	Loaned       bool
	IsTV         bool
	Incomplete   bool
	// Directors for video, authors for books; at most three.
	People []string
}

// Page is one page of List results.
type Page struct {
	Items []Summary
	Total int // matching items on all pages
	Page  int
	Pages int
}

// incompleteSQL matches items missing key fields for their kind (spec 3.2).
const incompleteSQL = `(i.year IS NULL
	OR (m.kind = 'video' AND i.runtime IS NULL)
	OR (m.kind = 'book' AND NOT EXISTS (
	    SELECT 1 FROM item_credits c WHERE c.item_id = i.id AND c.role = 'author')))`

// List returns one page of items matching q.
func (s *Store) List(ctx context.Context, q Query) (*Page, error) {
	where, args := q.where()
	from := ` FROM items i JOIN media_types m ON m.id = i.media_type_id WHERE ` + where

	var total int
	if err := s.db.QueryRowContext(ctx, `SELECT count(*)`+from, args...).Scan(&total); err != nil {
		return nil, err
	}

	p := &Page{Total: total, Page: max(q.Page, 1), Pages: max((total+PerPage-1)/PerPage, 1)}
	order, ok := sortSQL[q.Sort]
	if !ok {
		order = sortSQL[SortTitle]
	}
	dir := "ASC"
	if q.Desc {
		dir = "DESC"
	}
	query := `SELECT i.id, i.title, i.year, m.name, m.kind, i.series, i.series_number, i.runtime,
	                 i.status, i.own, i.seen, i.favourite, i.loaned, i.is_tv, ` + incompleteSQL +
		from + ` ORDER BY ` + orderBy(order, dir) + ` LIMIT ? OFFSET ?`
	args = append(args, PerPage, (p.Page-1)*PerPage)

	byID := map[int64]*Summary{}
	err := eachRow(ctx, s.db, func(r *sql.Rows) error {
		var (
			sm                   Summary
			year, runtime        sql.NullInt64
			series, seriesNumber sql.NullString
		)
		if err := r.Scan(&sm.ID, &sm.Title, &year, &sm.MediaType, &sm.Kind, &series, &seriesNumber,
			&runtime, &sm.Status, &sm.Own, &sm.Seen, &sm.Favourite, &sm.Loaned, &sm.IsTV,
			&sm.Incomplete); err != nil {
			return err
		}
		sm.Year, sm.Runtime = int(year.Int64), int(runtime.Int64)
		sm.Series, sm.SeriesNumber = series.String, seriesNumber.String
		p.Items = append(p.Items, sm)
		return nil
	}, query, args...)
	if err != nil {
		return nil, err
	}
	if len(p.Items) == 0 {
		return p, nil
	}

	// Fetch the people for the whole page in one query.
	ids := make([]any, len(p.Items))
	for i := range p.Items {
		ids[i] = p.Items[i].ID
		byID[p.Items[i].ID] = &p.Items[i]
	}
	err = eachRow(ctx, s.db, func(r *sql.Rows) error {
		var id int64
		var name string
		if err := r.Scan(&id, &name); err != nil {
			return err
		}
		if sm := byID[id]; len(sm.People) < 3 {
			sm.People = append(sm.People, name)
		}
		return nil
	}, `SELECT c.item_id, p.name FROM item_credits c JOIN people p ON p.id = c.person_id
	    WHERE c.role IN ('director', 'author') AND c.item_id IN (`+placeholders(len(ids))+`)
	    ORDER BY c.item_id, c.role, c.position`, ids...)
	return p, err
}

// orderBy fills in {dir}. Only "ASC" or "DESC" ever reach it, never user
// input.
func orderBy(template, dir string) string {
	return strings.ReplaceAll(template, "{dir}", dir)
}

// where builds the WHERE clause for q. Values always go in args as ?
// placeholders, never into the SQL text.
func (q Query) where() (string, []any) {
	var conds []string
	var args []any
	add := func(cond string, a ...any) {
		conds = append(conds, cond)
		args = append(args, a...)
	}

	status := q.Status
	if !status.Valid() {
		status = StatusActive
	}
	add("i.status = ?", string(status))

	// Every word must appear somewhere, like PHP4DVD's search.
	for _, word := range strings.Fields(strings.ToLower(q.Search)) {
		add(`i.search_text LIKE ? ESCAPE '\'`, "%"+escapeLike(word)+"%")
	}
	if q.MediaTypeID != 0 {
		add("i.media_type_id = ?", q.MediaTypeID)
	}
	if q.Kind.Valid() {
		add("m.kind = ?", string(q.Kind))
	}
	if q.GenreID != 0 {
		add("EXISTS (SELECT 1 FROM item_genres g WHERE g.item_id = i.id AND g.genre_id = ?)", q.GenreID)
	}
	if q.Incomplete {
		add(incompleteSQL)
	}
	flags := []struct {
		col string
		f   Flag
	}{{"own", q.Own}, {"seen", q.Seen}, {"favourite", q.Favourite}, {"loaned", q.Loaned}, {"is_tv", q.TV}}
	for _, fl := range flags {
		switch fl.f {
		case Yes:
			add("i." + fl.col + " = 1")
		case No:
			add("i." + fl.col + " = 0")
		}
	}
	return strings.Join(conds, " AND "), args
}

// escapeLike makes % and _ in a search word match themselves.
func escapeLike(s string) string {
	return strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(s)
}
