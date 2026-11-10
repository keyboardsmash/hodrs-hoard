package db

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
)

// OpenTest opens a fresh, empty database in a temporary folder that is
// removed when the test ends. Migrations are not applied.
//
// It is exported so other packages' tests (users, items, ...) can use it,
// which is why it lives in a normal .go file rather than a _test.go file.
func OpenTest(t *testing.T) *sql.DB {
	t.Helper() // report failures at the caller's line, not here
	d, err := Open(context.Background(), filepath.Join(t.TempDir(), FileName))
	if err != nil {
		t.Fatalf("open test database: %v", err)
	}
	t.Cleanup(func() { d.Close() }) // runs when the test ends
	return d
}
