package item

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"
)

// Get returns one item with everything that hangs off it, whatever its
// status. Callers decide who may see hidden and trashed items.
func (s *Store) Get(ctx context.Context, id int64) (*Item, error) {
	var (
		it                                                   Item
		parent, year, rating, ageLimit, runtime, pages       sql.NullInt64
		trashed, addedBy, updatedBy                          sql.NullInt64
		added, updated                                       int64
		sortTitle, original, alt, desc, notes, series, serNo sql.NullString
		loanedTo, loanedOn, trailer, mpaa                    sql.NullString
		region, standard, seasons, audio, video              sql.NullString
		publisher, binding, fileFormat, filePath             sql.NullString
	)
	err := s.db.QueryRowContext(ctx, `
		SELECT id, media_type_id, parent_id, status, trashed_at,
		       title, sort_title, original_title, alt_titles, year, description, notes,
		       own_rating, series, series_number,
		       own, seen, favourite, loaned, loaned_to, loaned_on, trailer, mpaa, age_limit,
		       runtime, region, video_standard, is_tv, seasons, audio_format, video_format,
		       publisher, pages, binding, file_format, file_path,
		       added_at, updated_at, added_by, updated_by
		FROM items WHERE id = ?`, id).Scan(
		&it.ID, &it.MediaTypeID, &parent, &it.Status, &trashed,
		&it.Title, &sortTitle, &original, &alt, &year, &desc, &notes,
		&rating, &series, &serNo,
		&it.Own, &it.Seen, &it.Favourite, &it.Loaned, &loanedTo, &loanedOn, &trailer, &mpaa, &ageLimit,
		&runtime, &region, &standard, &it.IsTV, &seasons, &audio, &video,
		&publisher, &pages, &binding, &fileFormat, &filePath,
		&added, &updated, &addedBy, &updatedBy)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}

	// A NULL scans as Valid=false; .String and .Int64 are then "" and 0,
	// which is exactly the Item's "not set".
	it.ParentID = parent.Int64
	it.SortTitle = sortTitle.String
	it.OriginalTitle = original.String
	if alt.Valid {
		it.AltTitles = strings.Split(alt.String, "\n")
	}
	it.Year = int(year.Int64)
	it.Description = desc.String
	it.Notes = notes.String
	it.OwnRating = int(rating.Int64)
	it.Series = series.String
	it.SeriesNumber = serNo.String
	it.LoanedTo = loanedTo.String
	it.LoanedOn = loanedOn.String
	it.Trailer = trailer.String
	it.MPAA = mpaa.String
	if ageLimit.Valid {
		n := int(ageLimit.Int64)
		it.AgeLimit = &n // & takes the address, giving the *int the field wants
	}
	it.Runtime = int(runtime.Int64)
	it.Region = region.String
	it.VideoStandard = standard.String
	it.Seasons = seasons.String
	it.AudioFormat = audio.String
	it.VideoFormat = video.String
	it.Publisher = publisher.String
	it.Pages = int(pages.Int64)
	it.Binding = binding.String
	it.FileFormat = fileFormat.String
	it.FilePath = filePath.String
	if trashed.Valid {
		it.TrashedAt = time.Unix(trashed.Int64, 0)
	}
	it.AddedAt = time.Unix(added, 0)
	it.UpdatedAt = time.Unix(updated, 0)
	it.AddedBy = addedBy.Int64
	it.UpdatedBy = updatedBy.Int64

	if it.MediaType, err = mediaTypeByID(ctx, s.db, it.MediaTypeID); err != nil {
		return nil, err
	}
	if err := s.loadLinks(ctx, &it); err != nil {
		return nil, err
	}
	return &it, nil
}

func (s *Store) loadLinks(ctx context.Context, it *Item) error {
	err := eachRow(ctx, s.db, func(r *sql.Rows) error {
		var c Credit
		if err := r.Scan(&c.Role, &c.Name); err != nil {
			return err
		}
		it.Credits = append(it.Credits, c)
		return nil
	}, `SELECT c.role, p.name FROM item_credits c JOIN people p ON p.id = c.person_id
	    WHERE c.item_id = ? ORDER BY c.role, c.position`, it.ID)
	if err != nil {
		return err
	}

	err = eachRow(ctx, s.db, func(r *sql.Rows) error {
		var g string
		if err := r.Scan(&g); err != nil {
			return err
		}
		it.Genres = append(it.Genres, g)
		return nil
	}, `SELECT g.name FROM item_genres ig JOIN genres g ON g.id = ig.genre_id
	    WHERE ig.item_id = ? ORDER BY g.name_key`, it.ID)
	if err != nil {
		return err
	}

	err = eachRow(ctx, s.db, func(r *sql.Rows) error {
		var field, value string
		if err := r.Scan(&field, &value); err != nil {
			return err
		}
		switch field {
		case "language":
			it.Languages = append(it.Languages, value)
		case "subtitle":
			it.Subtitles = append(it.Subtitles, value)
		case "country":
			it.Countries = append(it.Countries, value)
		}
		return nil
	}, `SELECT field, value FROM item_values WHERE item_id = ? ORDER BY field, position`, it.ID)
	if err != nil {
		return err
	}

	return eachRow(ctx, s.db, func(r *sql.Rows) error {
		var id Identifier
		if err := r.Scan(&id.Type, &id.Value); err != nil {
			return err
		}
		it.Identifiers = append(it.Identifiers, id)
		return nil
	}, `SELECT type, value FROM identifiers WHERE item_id = ? ORDER BY type, value`, it.ID)
}

// eachRow runs a query and calls fn for every row.
func eachRow(ctx context.Context, q querier, fn func(*sql.Rows) error, query string, args ...any) error {
	rows, err := q.QueryContext(ctx, query, args...)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		if err := fn(rows); err != nil {
			return err
		}
	}
	return rows.Err()
}

// SetStatus moves an item between active, hidden and trashed (spec 3.6).
func (s *Store) SetStatus(ctx context.Context, userID, id int64, status Status) error {
	if !status.Valid() {
		return errors.New("invalid status " + string(status))
	}
	now := s.now().Unix()
	var trashedAt any
	if status == StatusTrashed {
		trashedAt = now
	}
	res, err := s.db.ExecContext(ctx, `
		UPDATE items SET status = ?,
		    -- keep the first trashed time if it is trashed again
		    trashed_at = CASE WHEN ? IS NULL THEN NULL ELSE coalesce(trashed_at, ?) END,
		    updated_at = ?, updated_by = ?
		WHERE id = ?`, string(status), trashedAt, trashedAt, now, nullID(userID), id)
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

// EmptyTrash deletes all trashed items for good, and the people and genres
// no item uses any more. It returns how many items were deleted.
func (s *Store) EmptyTrash(ctx context.Context) (int, error) {
	var n int64
	err := s.inTx(ctx, func(tx *sql.Tx) error {
		// ON DELETE CASCADE removes the items' credits, genres, lists and
		// identifiers; children of a deleted box set get parent_id NULL.
		res, err := tx.ExecContext(ctx, `DELETE FROM items WHERE status = 'trashed'`)
		if err != nil {
			return err
		}
		if n, err = res.RowsAffected(); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx,
			`DELETE FROM people WHERE id NOT IN (SELECT person_id FROM item_credits)`); err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx,
			`DELETE FROM genres WHERE id NOT IN (SELECT genre_id FROM item_genres)`)
		return err
	})
	return int(n), err
}

// Duplicate is another item that shares an identifier with this one.
type Duplicate struct {
	Identifier
	ItemID int64
	Title  string
	Status Status
}

// Duplicates returns the other items that share an identifier with item
// id, trashed ones included (spec 3.3: a warning, not an error).
func (s *Store) Duplicates(ctx context.Context, id int64) ([]Duplicate, error) {
	var out []Duplicate
	err := eachRow(ctx, s.db, func(r *sql.Rows) error {
		var d Duplicate
		if err := r.Scan(&d.Type, &d.Value, &d.ItemID, &d.Title, &d.Status); err != nil {
			return err
		}
		out = append(out, d)
		return nil
	}, `SELECT o.type, o.value, i.id, i.title, i.status
	    FROM identifiers mine
	    JOIN identifiers o ON o.type = mine.type AND o.value = mine.value AND o.item_id != mine.item_id
	    JOIN items i ON i.id = o.item_id
	    WHERE mine.item_id = ?
	    ORDER BY o.type, o.value, i.sort_key`, id)
	return out, err
}
