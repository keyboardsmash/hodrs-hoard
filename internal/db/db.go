// Package db opens the SQLite database and runs migrations.
//
// The rest of the app uses the standard *sql.DB that Open returns.
package db

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	// A blank import (`_`) runs a package's init function without using its
	// names. This one registers the "sqlite" driver with database/sql, which
	// is how sql.Open finds it by name.
	_ "modernc.org/sqlite"
)

// FileName is the database file inside the data folder.
const FileName = "hoard.db"

// Open opens (or creates) the SQLite database at path and checks that it
// works. The parent folder is created if needed.
func Open(ctx context.Context, path string) (*sql.DB, error) {
	// '?' would be read as the start of connection options below.
	if strings.ContainsAny(path, "?#") {
		return nil, fmt.Errorf("database path %q must not contain ? or #", path)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, fmt.Errorf("creating database folder: %w", err)
	}

	// sql.Open only prepares the pool; the first real connection happens on
	// PingContext, so that's where a broken file or folder shows up.
	sqlDB, err := sql.Open("sqlite", dsn(path))
	if err != nil {
		return nil, err
	}
	if err := sqlDB.PingContext(ctx); err != nil {
		sqlDB.Close()
		return nil, fmt.Errorf("opening database %s: %w", path, err)
	}
	return sqlDB, nil
}

// dsn adds connection settings to the file path. modernc.org/sqlite applies
// each _pragma to every new connection in the pool.
//   - foreign_keys: SQLite ignores foreign keys unless this is on.
//   - busy_timeout: wait up to 5 s for a lock instead of failing at once.
//   - journal_mode(WAL): readers don't block the writer and vice versa.
//   - _txlock=immediate: transactions take the write lock when they begin,
//     which avoids "database is locked" errors when two of them upgrade at once.
func dsn(path string) string {
	return path + "?_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)" +
		"&_pragma=journal_mode(WAL)&_txlock=immediate"
}
