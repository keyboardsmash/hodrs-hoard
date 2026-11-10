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
replaces PHP4DVD (`legacy/`). It runs as a single Go binary with an SQLite
database, either as a server or as a desktop app.

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
| username      | yes      | Unique, case-insensitive (Unicode-aware). Up to 64 letters, digits and `. _ - @` |
| email         | no       | Unique if set                                 |
| password      | yes      | At least 10 characters, no complexity rules. Stored as an argon2id hash, never plain or MD5 |
| role          | yes      | viewer / editor / admin                       |
| created_at    | auto     |                                               |
| last_login_at | auto     |                                               |
| disabled_at   | auto     | Set when an admin disables the user (see 2.3) |
| must_change_password | auto | Set for imported PHP4DVD users (see 2.5) |

### 2.3 Operations

#### Create user
- An admin creates users from the user management page.
- On first start with an empty database, the app asks for an initial admin
  account. There is no default `admin`/`admin` login.
- **Decided:** first-run setup is protected by a one-time setup code. While
  no users exist, hoard prints a code (`XXXX-XXXX-XXXX`) to its log at
  startup, and every page redirects to `/setup`, which asks for it. Only
  someone who can read the server log can create the first admin. A new
  code is made at each start until setup is done; then `/setup` is closed.
- **Decided:** Do not allow self-registration.

#### Update user
- Users can change their own email and password.
- Only admins can change roles, and an admin cannot change their own role.
  This matches PHP4DVD (`legacy/includes/user.update.inc.php`).
- Admins can reset another user's password.

#### Disable user
- **Decided:** users are disabled, never deleted, so the history of who
  added or edited what is kept.
- Only admins can disable users, and they can enable them again.
- A disabled user cannot log in, and their active sessions and "remember
  me" tokens are revoked.
- The username stays reserved while the user is disabled.
- The last remaining active admin cannot be disabled or demoted.
- An admin cannot disable their own account.
- Items are not owned by users, so disabling a user does not affect the
  catalogue.

### 2.4 Authentication

- Session cookie, plus an optional "remember me" token (PHP4DVD used
  selector/token pairs in an `auth` table).
- Desktop mode. Default Option to skip login entirely and use a single
  implicit admin.
- A setting to allow anonymous visitors to be able to browse read-only.

### 2.5 Importing PHP4DVD users

- PHP4DVD stores unsalted MD5 password hashes, which must not be reused.
- **Decided:** import username, email and role, and require a new password
  on first login.

### 2.6 Sessions, passwords and CSRF

- **Decided:** passwords are hashed with argon2id (RFC 9106 second
  recommended parameters: 64 MiB, 3 iterations, 4 threads) in PHC string
  format. Hashes with older parameters are upgraded at the next login.
- Logins with an unknown username take as long as a wrong password, and
  all failures show the same message, so usernames can't be probed.
- **Decided:** sessions are stored server-side in the `sessions` table. The
  cookie (`hoard_session`) holds a random 256-bit token; the database only
  stores its SHA-256, so a copy of the database can't hijack sessions.
- A session ends after **12 hours without activity** or **7 days after
  login**, whichever comes first, or at logout. The cookie itself is a
  browser-session cookie; staying logged in across browser restarts is
  "remember me" (milestone 2d). Expired sessions are cleaned up hourly.
- Cookie flags: `HttpOnly`, `SameSite=Lax`, and `Secure` when the request
  came over HTTPS (directly, or via a proxy setting `X-Forwarded-Proto`).
- Every login issues a new session token (no session fixation).
- **Decided:** CSRF protection uses Go's `http.CrossOriginProtection`:
  cross-origin POST/PUT/PATCH/DELETE requests are rejected based on the
  `Sec-Fetch-Site` and `Origin` headers. Forms need no hidden tokens, which
  also suits htmx. Consequence: GET requests must never change data.
- **Later:** login rate limiting (per IP and per username).
- **Later:** command-line admin recovery, for example
  `hoard user reset-password <name>`, for admins locked out of the web UI.

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
| added_by       | auto, user who created the item             |
| updated_by     | auto, user who last edited the item         |
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
  | `HOARD_DATA_DIR`  | per-user data folder, see below | Holds `hoard.db` (SQLite), later covers |

- Default data folder: `$XDG_DATA_HOME/hodrs-hoard` (usually
  `~/.local/share/hodrs-hoard`) on Linux, `~/Library/Application Support/hodrs-hoard`
  on macOS. Suits the desktop version; servers and containers set
  `HOARD_DATA_DIR` (for example `/data`). Everything the app stores lives
  in this one folder, so backing it up is enough.
- `GET /healthz` returns JSON (`status`, `version`, `uptime`, `database`)
  for monitoring and container health checks. It answers 503 when the
  database is unreachable.
- **Decided:** database is SQLite only, for server and desktop alike, through
  `database/sql` with `modernc.org/sqlite` (pure Go, no cgo). WAL mode and
  foreign keys are on. Running several app instances against one shared
  database is not supported.
- Migrations: numbered SQL files in `internal/db/migrations/`, embedded in
  the binary and applied at startup. Applied versions are recorded in
  `schema_migrations`. hoard refuses to start on a database migrated by a
  newer version.
- Distribution: static binary (`hoard`), container image, AppImage. Wails v3
  desktop is the last milestone.
- **Decided:** platforms are Linux and macOS. Windows is not supported: a
  Windows build fails on purpose (`cmd/hoard/unsupported_windows.go`).

---

## 9. Internationalisation

- **Later:** which UI languages to ship. PHP4DVD has 10 (`legacy/lang/`).
  Suggested: English first, with strings kept translatable from day one.

---

## Appendix A. Milestones

1. Skeleton: binary, configuration, HTTP server, health page.
2. Database and migrations (SQLite), users and auth (section 2).
   - 2a. Database connection, migration runner, health check.
   - 2b. Users, password hashing (argon2id), sessions, login/logout,
     first-run admin setup, CSRF protection.
   - 2c. User management: admin pages, own account page, forced password
     change, anonymous browsing setting.
   - 2d. "Remember me" tokens.
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
| 2026-11-09 | No self-registration; admins create users.            |
| 2026-11-09 | Desktop mode skips login by default (implicit admin). |
| 2026-11-09 | Optional read-only browsing for anonymous visitors.   |
| 2026-11-09 | Keep PHP4DVD fields: own, seen, favourite, loans, trailer, MPAA rating. |
| 2026-11-09 | Users are disabled, never deleted, to keep edit history. |
| 2026-11-09 | PHP4DVD users imported with a forced password change. |
| 2026-11-09 | Passwords hashed with argon2id (`golang.org/x/crypto`). |
| 2026-11-09 | PostgreSQL driver: pgx v5 via `database/sql`.         |
| 2026-11-09 | Default data folder is the per-user data dir (desktop-friendly). |
| 2026-11-09 | Milestone 2 split into 2a-2d; "remember me" is 2d.    |
| 2026-11-09 | Windows not supported; Windows builds fail on purpose. |
| 2026-11-10 | PostgreSQL support removed; SQLite only (replaces the pgx decision). Database is always `hoard.db` in the data folder. |
| 2026-11-10 | Dependencies pinned by `go.mod`/`go.sum`; `vendor/` is not committed (the SQLite driver alone is ~140 MB vendored). |
| 2026-11-10 | First-run setup requires a one-time code printed to the server log. |
| 2026-11-10 | Sessions: server-side, token hashed in the database, 12 h idle / 7 days absolute. |
| 2026-11-10 | CSRF protection via stdlib `http.CrossOriginProtection` (header-based, no form tokens). |
| 2026-11-10 | Login rate limiting and CLI admin recovery deferred (Later). |
