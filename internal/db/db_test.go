package db

import (
	"context"
	"io"
	"log/slog"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"
)

var discard = slog.New(slog.NewTextHandler(io.Discard, nil))

func TestOpenCreatesFolderAndSetsPragmas(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "folder", FileName)
	d, err := Open(context.Background(), path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer d.Close()

	var journal string
	var foreignKeys int
	if err := d.QueryRow("PRAGMA journal_mode").Scan(&journal); err != nil {
		t.Fatal(err)
	}
	if err := d.QueryRow("PRAGMA foreign_keys").Scan(&foreignKeys); err != nil {
		t.Fatal(err)
	}
	if journal != "wal" || foreignKeys != 1 {
		t.Errorf("journal_mode = %q, foreign_keys = %d; want wal, 1", journal, foreignKeys)
	}
}

func TestOpenRejectsBadPath(t *testing.T) {
	if _, err := Open(context.Background(), filepath.Join(t.TempDir(), "x.db?mode=ro")); err == nil {
		t.Error("Open accepted a path with '?', want error")
	}
}

func TestLoadMigrationsRejectsBadFiles(t *testing.T) {
	cases := map[string]fstest.MapFS{
		"gap": {
			"migrations/0001_a.sql": {Data: []byte("SELECT 1")},
			"migrations/0003_c.sql": {Data: []byte("SELECT 1")},
		},
		"duplicate": {
			"migrations/0001_a.sql": {Data: []byte("SELECT 1")},
			"migrations/0001_b.sql": {Data: []byte("SELECT 1")},
		},
		"bad name": {
			"migrations/1_a.sql": {Data: []byte("SELECT 1")},
		},
	}
	// Ranging over a map gives key and value, like dict.items() in Python.
	for name, fsys := range cases {
		if _, err := loadMigrations(fsys); err == nil {
			t.Errorf("%s: loadMigrations succeeded, want error", name)
		}
	}
}

func TestMigrate(t *testing.T) {
	d := OpenTest(t)
	ctx := context.Background()

	// Running twice must be safe: the second run finds nothing to do.
	for i := 0; i < 2; i++ {
		if err := Migrate(ctx, d, discard); err != nil {
			t.Fatalf("Migrate run %d: %v", i+1, err)
		}
	}

	var count int
	if err := d.QueryRowContext(ctx, "SELECT COUNT(*) FROM schema_migrations").Scan(&count); err != nil {
		t.Fatal(err)
	}
	want, _ := loadMigrations(migrationsFS)
	if count != len(want) {
		t.Errorf("schema_migrations has %d rows, want %d", count, len(want))
	}

	// The settings table exists and UTF-8 survives a round trip.
	if _, err := d.ExecContext(ctx, "INSERT INTO settings (key, value) VALUES (?, ?)", "greeting", "Hej Höðr"); err != nil {
		t.Fatal(err)
	}
	var v string
	if err := d.QueryRowContext(ctx, "SELECT value FROM settings WHERE key = ?", "greeting").Scan(&v); err != nil {
		t.Fatal(err)
	}
	if v != "Hej Höðr" {
		t.Errorf("value = %q", v)
	}
}

func TestMigrateRefusesNewerDatabase(t *testing.T) {
	d := OpenTest(t)
	ctx := context.Background()
	if err := Migrate(ctx, d, discard); err != nil {
		t.Fatal(err)
	}
	if _, err := d.ExecContext(ctx,
		"INSERT INTO schema_migrations (version, name, applied_at) VALUES (?, ?, CURRENT_TIMESTAMP)",
		9999, "from_the_future"); err != nil {
		t.Fatal(err)
	}
	err := Migrate(ctx, d, discard)
	if err == nil || !strings.Contains(err.Error(), "upgrade hoard") {
		t.Errorf("Migrate = %v, want an 'upgrade hoard' error", err)
	}
}

func TestMigrateRollsBackFailedMigration(t *testing.T) {
	d := OpenTest(t)
	ctx := context.Background()
	fsys := fstest.MapFS{
		"migrations/0001_good.sql": {Data: []byte("CREATE TABLE good (id INTEGER PRIMARY KEY)")},
		// The first statement succeeds, the second fails; both must be undone.
		"migrations/0002_bad.sql": {Data: []byte("CREATE TABLE half (id INTEGER); THIS IS NOT SQL;")},
	}

	if err := migrate(ctx, d, fsys, discard); err == nil {
		t.Fatal("migrate succeeded, want error from 0002")
	}

	applied, err := appliedVersions(ctx, d)
	if err != nil {
		t.Fatal(err)
	}
	if !applied[1] || applied[2] {
		t.Errorf("applied = %v, want only version 1", applied)
	}
	if _, err := d.ExecContext(ctx, "SELECT * FROM half"); err == nil {
		t.Error("table from the failed migration exists, want it rolled back")
	}
}
