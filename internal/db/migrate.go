package db

import (
	"context"
	"database/sql"
	"embed"
	"fmt"
	"io/fs"
	"log/slog"
	"path"
	"regexp"
	"sort"
	"strconv"
	"time"
)

// migrationsFS holds migrations/*.sql, compiled into the binary.
//
//go:embed migrations/*.sql
var migrationsFS embed.FS

// Migration is one numbered SQL file.
type Migration struct {
	Version int
	Name    string
	SQL     string
}

// File names look like 0001_create_settings.sql.
var migrationName = regexp.MustCompile(`^(\d{4})_([a-z0-9_]+)\.sql$`)

// Migrate applies all pending migrations.
func Migrate(ctx context.Context, d *sql.DB, log *slog.Logger) error {
	return migrate(ctx, d, migrationsFS, log)
}

// migrate is Migrate with the files passed in, so tests can use fake ones.
func migrate(ctx context.Context, d *sql.DB, fsys fs.FS, log *slog.Logger) error {
	migrations, err := loadMigrations(fsys)
	if err != nil {
		return err
	}

	if _, err := d.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS schema_migrations (
		version    INTEGER PRIMARY KEY,
		name       TEXT NOT NULL,
		applied_at DATETIME NOT NULL
	)`); err != nil {
		return fmt.Errorf("creating schema_migrations: %w", err)
	}

	applied, err := appliedVersions(ctx, d)
	if err != nil {
		return err
	}

	// A version we don't know means the database was migrated by a newer
	// hoard. Running old code against it could damage data, so stop.
	known := make(map[int]bool, len(migrations))
	for _, m := range migrations {
		known[m.Version] = true
	}
	for v := range applied {
		if !known[v] {
			return fmt.Errorf("database has migration %04d, which this version of hoard doesn't know; upgrade hoard", v)
		}
	}

	for _, m := range migrations {
		if applied[m.Version] {
			continue
		}
		if err := apply(ctx, d, m); err != nil {
			return fmt.Errorf("migration %04d_%s: %w", m.Version, m.Name, err)
		}
		log.Info("applied migration", "version", m.Version, "name", m.Name)
	}
	return nil
}

// apply runs one migration and records it, in a single transaction. SQLite
// can roll back schema changes, so a failing migration leaves nothing
// half-done.
func apply(ctx context.Context, d *sql.DB, m Migration) error {
	tx, err := d.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	// Rollback after a successful Commit is a harmless no-op, so deferring
	// it is the usual way to undo the transaction on any early return.
	defer tx.Rollback()

	if _, err := tx.ExecContext(ctx, m.SQL); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx,
		"INSERT INTO schema_migrations (version, name, applied_at) VALUES (?, ?, ?)",
		m.Version, m.Name, time.Now().UTC(),
	); err != nil {
		return err
	}
	return tx.Commit()
}

// appliedVersions returns the set of applied versions. Go has no set type;
// map[int]bool is the usual stand-in.
func appliedVersions(ctx context.Context, d *sql.DB) (map[int]bool, error) {
	rows, err := d.QueryContext(ctx, "SELECT version FROM schema_migrations")
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	applied := map[int]bool{}
	for rows.Next() {
		var v int
		if err := rows.Scan(&v); err != nil {
			return nil, err
		}
		applied[v] = true
	}
	return applied, rows.Err() // rows.Err reports errors that ended the loop early
}

// loadMigrations reads migrations/*.sql, sorted by version.
// Versions must be 1, 2, 3, ... with no gaps or duplicates.
func loadMigrations(fsys fs.FS) ([]Migration, error) {
	const dir = "migrations"
	entries, err := fs.ReadDir(fsys, dir)
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", dir, err)
	}

	var migrations []Migration
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		match := migrationName.FindStringSubmatch(e.Name())
		if match == nil {
			return nil, fmt.Errorf("%s/%s: name must look like 0001_some_name.sql", dir, e.Name())
		}
		version, _ := strconv.Atoi(match[1]) // can't fail: the regexp matched 4 digits
		body, err := fs.ReadFile(fsys, path.Join(dir, e.Name()))
		if err != nil {
			return nil, err
		}
		migrations = append(migrations, Migration{Version: version, Name: match[2], SQL: string(body)})
	}

	sort.Slice(migrations, func(i, j int) bool { return migrations[i].Version < migrations[j].Version })
	for i, m := range migrations {
		if m.Version != i+1 {
			return nil, fmt.Errorf("%s: expected migration %04d, found %04d_%s", dir, i+1, m.Version, m.Name)
		}
	}
	return migrations, nil
}
