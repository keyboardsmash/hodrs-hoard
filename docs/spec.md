# Höðrs Hoard — Specification

Source of truth for features and behaviour. When a decision changes, update
this file in the same commit as the code.

Status markers used below:

- **Decided**: agreed, build it this way.
- **Open**: needs a decision before it is built.
- **Later**: agreed in principle, not in the current scope.

---

## 1. Overview

Höðrs Hoard is a self-hosted catalogue for physical and digital media. It
replaces PHP4DVD (`legacy/`). It runs as a single Go binary, either as a
server (PostgreSQL or SQLite) or as a desktop app (SQLite).

### 1.1 Goals

- Catalogue films and books, both physical and digital, in one place.
- Fast manual entry. Only title and media type are required.
- Import an existing PHP4DVD collection without losing data.
- Easy to self-host: a static binary, a container image or an AppImage.

### 1.2 Non-goals (for now)

- Streaming or playing media files.
- Public or social features (sharing lists, reviews).

---

## 2. Users

### 2.1 Roles

Based on PHP4DVD's three permission levels (`legacy/includes/users.inc.php`).

| Role   | Can view | Can add/edit items | Can manage users |
|--------|----------|--------------------|------------------|
| viewer | yes      | no                 | no               |
| editor | yes      | yes                | no               |
| admin  | yes      | yes                | yes              |

**Decided.**

### 2.2 User fields

| Field         | Required | Notes                                         |
|---------------|----------|-----------------------------------------------|
| username      | yes      | Unique, case-insensitive                      |
| email         | no       | Unique if set                                 |
| password      | yes      | Stored as an argon2id or bcrypt hash, never plain or MD5 |
| role          | yes      | viewer / editor / admin                       |
| created_at    | auto     |                                               |
| last_login_at | auto     |                                               |

### 2.3 Operations

#### Create user
- An admin creates users from the user management page.
- On first start with an empty database, the app asks for an initial admin
  account. There is no default `admin`/`admin` login.
- **Decided:** Do not allow self-registration.

#### Update user
- Users can change their own email and password.
- Only admins can change roles, and an admin cannot change their own role.
  This matches PHP4DVD (`legacy/includes/user.update.inc.php`).
- Admins can reset another user's password.

#### Delete user
- Only admins can delete users.
- The last remaining admin cannot be deleted or demoted.
- An admin cannot delete their own account.
- Items are not owned by users, so deleting a user does not affect the
  catalogue.
- Users should only be disable. Flag that keeps history (who added or edited what).

### 2.4 Authentication

- Session cookie, plus an optional "remember me" token (PHP4DVD used
  selector/token pairs in an `auth` table).
- Desktop mode. Default Option to skip login entirely and use a single
  implicit admin.
- A setting to allow anonymous visitors to be able to browse read-only.

### 2.5 Importing PHP4DVD users

- PHP4DVD stores unsalted MD5 password hashes, which must not be reused.
- import username, email and role, and require a new password on first login.

---

## 3. Library (items and media types)

### 3.1 Media types

Every item has exactly one media type. Types are grouped into a *kind*,
which decides which fields and metadata providers apply.

Initial (seeded) types:

| Media type | Kind  | Physical | Notes                          |
|------------|-------|----------|--------------------------------|
| DVD        | video | yes      |                                |
| VHS        | video | yes      |                                |
| Book       | book  | yes      | Hardcover/paperback: **Open**  |
| Ebook      | book  | no       | File format: EPUB or PDF       |

- Users (editors and admins) can add their own media types by choosing a
  name and a kind. Seeded types can be renamed but not deleted while items
  use them.
- **Later:** seed Blu-ray, 3D Blu-ray, 4K UHD and audio (CD, vinyl).
- **Open:** should physical book bindings (hardcover/paperback) be separate
  media types or a field on the item?
- Option to group media types by kind. 

### 3.2 Item fields

Only **title** and **media type** are required. An item that is missing
key fields for its kind is "incomplete" and can be listed with a filter.

Common fields (all kinds):

| Field          | Notes                                       |
|----------------|---------------------------------------------|
| title          | Required                                    |
| sort_title     | Derived ("The Matrix" → "Matrix, The"), editable |
| media_type     | Required                                    |
| year           |                                             |
| description    | Plot or synopsis                            |
| genres         | Many-to-many                                |
| notes          | Free text, private                          |
| cover          | See 3.5                                     |
| status         | active / hidden / trashed                   |
| added_at       | auto                                        |
| updated_at     | auto                                        |
| parent_id      | For box sets (see 3.4)                      |

Video fields (DVD, VHS):

| Field     | Notes                                     |
|-----------|-------------------------------------------|
| runtime   | Minutes                                   |
| directors | People, many-to-many                      |
| cast      | People, many-to-many, with order          |
| languages | Audio languages                           |
| subtitles |                                           |
| region    | DVD region code / VHS PAL/NTSC: **Open**  |

Book fields (Book, Ebook):

| Field     | Notes                            |
|-----------|----------------------------------|
| authors   | People, many-to-many             |
| publisher |                                  |
| pages     |                                  |
| language  |                                  |
| series    | Name and number: **Open**        |
| file      | Ebook only: path or uploaded file, format (EPUB/PDF) |

**Decided:** Keep all PHP4DVD fields to keep as-is: own, seen, favourite, loans
(loaned/loandate/loanname), trailer, MPAA/PG rating.

### 3.3 Identifiers

Stored in a separate table: `(item_id, type, value)`.

- Types: `imdb`, `tmdb`, `isbn`, `ean` (EAN/UPC barcode). More can be added.
- Values are normalised before saving:
  - ISBN-10 is converted to ISBN-13. ISBN and EAN are stored as 13 digits
    with the check digit validated.
  - UPC-A (12 digits) is padded to EAN-13.
  - IMDb IDs are stored as `tt` plus digits, extracted from pasted URLs.
- Indexed, **not unique**. A duplicate gives a warning in the UI, not an
  error.

### 3.4 Box sets (parent/child)

- An item can contain child items (for example, a DVD box set containing
  three films).
- Supported in the data model from the start. The UI for this is **Later**.

### 3.5 Covers

- Stored by content hash: `covers/ab/cd/<hash>.jpg`.
- A thumbnail is generated on upload.
- **Later:** maximum stored size, and whether to re-encode the original.

### 3.6 Item lifecycle

- Statuses: `active` (default), `hidden` (not shown in normal lists),
  `trashed`.
- No hard delete, except by emptying the trash (admins only).

### 3.7 Manual edits vs. metadata refresh

- Each field records whether a user edited it by hand.
- A metadata refresh never overwrites a hand-edited field.

---

## 4. Adding items

- One add form per item, with an optional lookup box at the top.
- The lookup box accepts a title, a pasted IMDb or TMDb link, an ISBN or a
  barcode. Lookups only fill in the form. Saving never depends on a lookup
  succeeding.
- Barcode input: a USB scanner acting as a keyboard, typing into the lookup
  box.
- **Later:** online barcode lookup through pluggable providers.

---

## 5. Browsing and search

- **Open:** list and grid views, sorting (title, year, added, media type),
  filters (media type, kind, genre, status, incomplete), and free-text
  search. Use PHP4DVD's search page as the baseline.

---

## 6. Metadata providers — Later

Pluggable interface. Initial providers:

- TMDb: film and TV.
- Open Library: books.
- Local file: EPUB/PDF embedded metadata.

API keys come from configuration and are never committed.

---

## 7. Import, export and backup — Later

- **Export:** all or selected items to a zip with JSON and CSV plus covers.
- **Import:** the same zip format. This is also how backups are restored.
- **Backup:** a scheduled full export with retention.
- **PHP4DVD importer:** reads the MySQL database and the covers folder. The
  column mapping will be in an appendix to this document.
- IMDb ratings CSV import.

---

## 8. Configuration and deployment

- Configuration from env vars, or a git-ignored config file.
- Environment variables (added as milestones need them):

  | Variable          | Default | Notes                                  |
  |-------------------|---------|----------------------------------------|
  | `HOARD_ADDR`      | `:8080` | Listen address, `host:port`            |
  | `HOARD_LOG_LEVEL` | `info`  | `debug`, `info`, `warn` or `error`     |

- `GET /healthz` returns JSON (`status`, `version`, `uptime`) for monitoring
  and container health checks.
- Database: SQLite (default) or PostgreSQL, using the same code.
- Distribution: static binary (`hoard`), container image, AppImage. Wails v3
  desktop is the last milestone.

---

## 9. Internationalisation

- **Later:** which UI languages to ship. PHP4DVD has 10 (`legacy/lang/`).
  Suggested: English first, with strings kept translatable from day one.

---

## Appendix A. Milestones

1. Skeleton: binary, configuration, HTTP server, health page.
2. Database and migrations (SQLite and PostgreSQL), users and auth (section 2).
3. Media types and items: create, list, edit and trash (sections 3–4,
   manual entry only).
4. Covers.
5. PHP4DVD importer.
6. Metadata providers.
7. Export, import and backup.
8. Packaging and desktop.

## Appendix B. PHP4DVD → Höðrs Hoard field mapping

*To be written with the importer milestone.*

## Appendix C. Decision log

| Date       | Decision                                              |
|------------|-------------------------------------------------------|
| 2026-11-09 | Initial media types: DVD, VHS, Book, Ebook.           |
| 2026-11-09 | Three user roles (viewer/editor/admin), as in PHP4DVD. |
