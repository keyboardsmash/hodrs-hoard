// Package settings reads and writes runtime settings that admins change in
// the UI. They live in the settings table as text key/value pairs.
package settings

import (
	"context"
	"database/sql"
	"errors"
	"strconv"
)

// Keys of known settings.
const (
	// AnonymousBrowsing lets visitors who aren't logged in browse
	// read-only (spec section 2.4). Default: false.
	AnonymousBrowsing = "anonymous_browsing"
)

// Store reads and writes the settings table.
type Store struct{ db *sql.DB }

// NewStore returns a settings store using db.
func NewStore(db *sql.DB) *Store { return &Store{db: db} }

// Bool returns a boolean setting, or def if it isn't set.
func (s *Store) Bool(ctx context.Context, key string, def bool) (bool, error) {
	var v string
	err := s.db.QueryRowContext(ctx, `SELECT value FROM settings WHERE key = ?`, key).Scan(&v)
	if errors.Is(err, sql.ErrNoRows) {
		return def, nil
	}
	if err != nil {
		return def, err
	}
	b, err := strconv.ParseBool(v) // accepts "true", "false", "1", "0", ...
	if err != nil {
		return def, err
	}
	return b, nil
}

// SetBool stores a boolean setting, replacing any earlier value.
func (s *Store) SetBool(ctx context.Context, key string, v bool) error {
	// "ON CONFLICT ... DO UPDATE" is SQLite's upsert: insert, or update the
	// existing row with the same key.
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO settings (key, value) VALUES (?, ?)
		 ON CONFLICT (key) DO UPDATE SET value = excluded.value`,
		key, strconv.FormatBool(v))
	return err
}
