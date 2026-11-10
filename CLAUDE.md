# Höðrs Hoard

Self-hosted catalogue for physical and digital media (films, books, ebooks and
more). Successor to PHP4DVD, rewritten in Go. License: GPL-3.0.

## Naming
- Display name: Höðrs Hoard (UTF-8; use in UI, docs, window titles)
- Slug: hodrs-hoard (repo, image names, config dirs; ASCII only)
- Go module path: github.com/keyboardsmash/hodrs-hoard
- Binary: hoard

## Rules
- legacy/ is the old PHP4DVD code. Read it for reference, never modify it.
- docs/spec.md is the source of truth for features. Keep it updated when
  decisions change, and ask before deviating from it.
- dev/ holds the development container. Don't change it unless asked.
- Work in small steps. Every milestone must build, run and be testable.
- Never commit secrets or API keys. Configuration comes from env vars or a
  config file that is git-ignored.

## Stack
- Go, standard library first; few dependencies. Pin them with go.mod and
  go.sum (commit both); don't commit vendor/.
- No cgo in the server build: use modernc.org/sqlite, not mattn/go-sqlite3.
- Database: SQLite only, for server and desktop alike. No PostgreSQL.
  The database file lives in the data folder (HOARD_DATA_DIR).
- Platforms: Linux and macOS. Windows is not supported.
- Frontend: Go html/template + htmx. No npm, no node_modules.
- Desktop (last milestone): Wails v3, same UI.
- Distribution: static binary, container image, AppImage.

## Data model decisions
- Items have a user-selectable media type (DVD, Blu-ray, 3D Blu-ray, 4K,
  book, ebook EPUB/PDF, ...). Users can add their own types.
- Only title and media type are required. Everything else is optional;
  incomplete items can be listed with a filter.
- External identifiers (IMDb, TMDb, ISBN, EAN/UPC barcode, ...) live in a
  separate identifiers table: (item, type, value). Values are normalised
  (barcodes and ISBNs stored as 13 digits, check digit validated), indexed,
  NOT unique. Duplicates give a warning, not an error.
- Design for box sets: an item may contain child items (parent/child),
  even if the UI doesn't use it at first.
- Covers stored in hashed subfolders (covers/ab/cd/<hash>.jpg), with
  thumbnails generated on upload.
- Item status: active, hidden or trashed. No hard delete except emptying
  the trash.
- Fields the user edited by hand are never overwritten by a metadata refresh.

## Features (see docs/spec.md for detail)
- One add form with an optional lookup box (title search, pasted IMDb/TMDb
  link, ISBN or barcode). Lookups fill the form; saving never depends on them.
- Barcode input via USB scanner (keyboard input) first; online barcode
  lookup later via pluggable providers.
- Metadata providers are pluggable: TMDb for film/TV, Open Library for books,
  EPUB/PDF metadata read from files.
- Export (all or selected items) to a zip with JSON/CSV plus covers.
  Backup = scheduled full export with retention. Restore = import.
- Importer for an existing PHP4DVD MySQL database and covers folder.
- Later: IMDb ratings CSV import.

## Working style
- I'm new to Go (background: Python, PHP, Terraform). Add brief comments
  explaining non-obvious Go constructs.
- Write tests for data handling, normalisation and importers.
- Before larger changes, outline the plan and wait for my go-ahead.
