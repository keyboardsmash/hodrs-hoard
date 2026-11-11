package item

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

// MediaType is a user-selectable type such as DVD or Book (spec 3.1).
type MediaType struct {
	ID       int64
	Name     string
	Kind     Kind
	Physical bool
	// Items is the number of items of this type, trashed ones included.
	// Only List fills it in.
	Items int
}

// MediaTypes returns all media types in menu order: by kind, then by
// their sort order.
func (s *Store) MediaTypes(ctx context.Context) ([]*MediaType, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT m.id, m.name, m.kind, m.physical,
		       (SELECT count(*) FROM items i WHERE i.media_type_id = m.id)
		FROM media_types m
		ORDER BY `+kindOrder+`, m.sort_order, m.name_key`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*MediaType
	for rows.Next() {
		var m MediaType
		if err := rows.Scan(&m.ID, &m.Name, &m.Kind, &m.Physical, &m.Items); err != nil {
			return nil, err
		}
		out = append(out, &m)
	}
	return out, rows.Err()
}

// MediaType returns one media type.
func (s *Store) MediaType(ctx context.Context, id int64) (*MediaType, error) {
	return mediaTypeByID(ctx, s.db, id)
}

func mediaTypeByID(ctx context.Context, q querier, id int64) (*MediaType, error) {
	var m MediaType
	err := q.QueryRowContext(ctx, `SELECT id, name, kind, physical FROM media_types WHERE id = ?`, id).
		Scan(&m.ID, &m.Name, &m.Kind, &m.Physical)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return &m, err
}

// cleanTypeName checks a media type name and returns it trimmed.
func cleanTypeName(name string) (string, error) {
	name = cleanText(name)
	switch {
	case name == "":
		return "", FieldErrors{"name": "A name is required."}
	case len(name) > 64:
		return "", FieldErrors{"name": "Too long (at most 64 characters)."}
	case hasControl(name):
		return "", FieldErrors{"name": "Contains characters that aren't allowed."}
	}
	return name, nil
}

// CreateMediaType adds a media type at the end of its kind's menu.
func (s *Store) CreateMediaType(ctx context.Context, name string, kind Kind, physical bool) (*MediaType, error) {
	name, err := cleanTypeName(name)
	if err != nil {
		return nil, err
	}
	if !kind.Valid() {
		return nil, FieldErrors{"kind": "Choose a kind."}
	}
	var m *MediaType
	err = s.inTx(ctx, func(tx *sql.Tx) error {
		if err := checkTypeNameFree(ctx, tx, name, 0); err != nil {
			return err
		}
		res, err := tx.ExecContext(ctx, `
			INSERT INTO media_types (name, name_key, kind, physical, sort_order)
			VALUES (?, ?, ?, ?, (SELECT coalesce(max(sort_order), 0) + 10 FROM media_types))`,
			name, key(name), string(kind), physical)
		if err != nil {
			return err
		}
		id, err := res.LastInsertId()
		if err != nil {
			return err
		}
		m, err = mediaTypeByID(ctx, tx, id)
		return err
	})
	return m, err
}

// RenameMediaType changes a media type's name. The kind can't change,
// since items of the type have fields for that kind.
func (s *Store) RenameMediaType(ctx context.Context, id int64, name string) error {
	name, err := cleanTypeName(name)
	if err != nil {
		return err
	}
	return s.inTx(ctx, func(tx *sql.Tx) error {
		if _, err := mediaTypeByID(ctx, tx, id); err != nil {
			return err
		}
		if err := checkTypeNameFree(ctx, tx, name, id); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, `UPDATE media_types SET name = ?, name_key = ? WHERE id = ?`,
			name, key(name), id)
		return err
	})
}

func checkTypeNameFree(ctx context.Context, tx *sql.Tx, name string, exceptID int64) error {
	taken, err := exists(ctx, tx, `SELECT 1 FROM media_types WHERE name_key = ? AND id != ?`, key(name), exceptID)
	if err != nil {
		return err
	}
	if taken {
		return FieldErrors{"name": fmt.Sprintf("There is already a media type called %s.", name)}
	}
	return nil
}

// DeleteMediaType removes a media type that no item uses, trashed items
// included. Otherwise it returns ErrInUse.
func (s *Store) DeleteMediaType(ctx context.Context, id int64) error {
	return s.inTx(ctx, func(tx *sql.Tx) error {
		if _, err := mediaTypeByID(ctx, tx, id); err != nil {
			return err
		}
		used, err := exists(ctx, tx, `SELECT 1 FROM items WHERE media_type_id = ? LIMIT 1`, id)
		if err != nil {
			return err
		}
		if used {
			return ErrInUse
		}
		_, err = tx.ExecContext(ctx, `DELETE FROM media_types WHERE id = ?`, id)
		return err
	})
}

// Names for autocomplete and filter menus.

// Genre is a genre with the number of items that use it.
type Genre struct {
	ID    int64
	Name  string
	Items int
}

// Genres returns all genres used by at least one item, by name.
func (s *Store) Genres(ctx context.Context) ([]Genre, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT g.id, g.name, count(*)
		FROM genres g JOIN item_genres ig ON ig.genre_id = g.id
		GROUP BY g.id ORDER BY g.name_key`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Genre
	for rows.Next() {
		var g Genre
		if err := rows.Scan(&g.ID, &g.Name, &g.Items); err != nil {
			return nil, err
		}
		out = append(out, g)
	}
	return out, rows.Err()
}

// SeriesNames returns the distinct series names in use, for autocomplete.
func (s *Store) SeriesNames(ctx context.Context) ([]string, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT min(series) FROM items WHERE series_key IS NOT NULL
		GROUP BY series_key ORDER BY series_key`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var n string
		if err := rows.Scan(&n); err != nil {
			return nil, err
		}
		out = append(out, n)
	}
	return out, rows.Err()
}
