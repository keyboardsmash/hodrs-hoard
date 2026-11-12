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
- Users can change their own email and password (`/account`). Changing the
  password requires the current one, and ends the user's other sessions.
- Only admins can change roles, and an admin cannot change their own role.
  This matches PHP4DVD (`legacy/includes/user.update.inc.php`).
- Admins can reset another user's password (`/admin/users/{id}`). **Decided:**
  a reset password is temporary: the user is logged out everywhere and must
  choose a new password at next login. Admins change their own password on
  the account page like everyone else.
- Admins can create users with an initial password; "must change password
  at first login" is ticked by default.
- **Decided:** forced password change. While `must_change_password` is set,
  every page except the password form and logout redirects to
  `/account/password`. The form asks for the current (temporary) password
  too, and the new one must differ from it.
- Users can "Log out everywhere" on their account page, for example after
  losing a device. It ends all their sessions and remember tokens.
- **Later:** renaming users (admins changing a username).

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
  **Decided:** admins switch it on `/admin/settings`; default off. Anonymous
  visitors can see read-only pages only. Account and admin pages, and
  every change, always need a login.

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
  "remember me" (below). Expired sessions are cleaned up hourly.
- **Decided:** "Remember me" is a checkbox on the login form (unticked by
  default). It sets a second cookie, `hoard_remember`, with its own token
  in the `remember_tokens` table (stored as SHA-256, like sessions). When
  a browser has no valid session but a valid remember token, a new session
  is started without a password; the usual checks (disabled user, forced
  password change) still apply.
  - Lifetime: **30 days, sliding**. Each use moves the expiry 30 days
    forward (cookie and database), so it only runs out after 30 days
    without a visit.
  - **Decided:** no token rotation. The token stays the same while it is
    valid. Rotation only helps together with theft detection, and needs a
    grace period so several tabs opening at once don't log the user out.
    **Later:** rotation with theft detection, if needed.
  - PHP4DVD split tokens into a selector and a validator. That isn't
    needed for a 256-bit random token looked up by its SHA-256.
- Logins end as follows (sessions and remember tokens alike):

  | Event | Ends |
  |-------|------|
  | Logout | This browser's session and remember token |
  | User changes own password | All the user's others; this browser stays logged in |
  | "Log out everywhere" (`/account`) | All the user's, on every device |
  | Admin resets password or disables user | All that user's |
  | Expiry | Removed by the hourly cleanup |

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
| Book       | book  | yes      | Binding is a field (see 3.2)   |
| Ebook      | book  | no       | File format: EPUB or PDF       |

- Users (editors and admins) can add their own media types by choosing a
  name and a kind. Seeded types can be renamed but not deleted while items
  use them.
- **Later:** seed Blu-ray, 3D Blu-ray, 4K UHD and audio (CD, vinyl).
- **Decided:** book bindings (hardcover/paperback) are a field on the item,
  not separate media types.
- **Decided:** an item's media type can be changed to another type of the
  same kind (DVD → VHS, Book → Ebook), never to another kind.
- Media-type menus (add form, filters) group the types under their kind
  (Video, Book).

### 3.2 Item fields

Only **title** and **media type** are required. An item that is missing
key fields for its kind is "incomplete" and can be listed with a filter.
**Decided:** an item is incomplete if it has no year, or (video) no
runtime, or (book) no author.

Common fields (all kinds):

| Field          | Notes                                       |
|----------------|---------------------------------------------|
| title          | Required                                    |
| original_title | Title in the original language              |
| alt_titles     | Other titles, one per line ("aka")         |
| sort_title     | Derived ("The Matrix" → "Matrix, The"), editable. **Decided:** only English articles (The, A, An) for now |
| media_type     | Required                                    |
| year           |                                             |
| description    | Plot or synopsis, one text field            |
| credits        | People with a role and order (see below)    |
| countries      | Production countries, several               |
| genres         | Many-to-many                                |
| notes          | Free text; hidden from anonymous visitors   |
| own_rating     | Your own rating, 1–10, empty = unrated      |
| cover          | See 3.5                                     |
| status         | active / hidden / trashed                   |
| added_at       | auto                                        |
| updated_at     | auto                                        |
| added_by       | auto, user who created the item             |
| updated_by     | auto, user who last edited the item         |
| parent_id      | For box sets (see 3.4)                      |
| series         | Series name, autocompletes from existing names |
| series_number  | Position in the series, text ("12", "1.5", "III") |

Video fields (DVD, VHS):

| Field     | Notes                                     |
|-----------|-------------------------------------------|
| runtime   | Minutes                                   |
| languages | Audio languages                           |
| subtitles |                                           |
| region    | Free text, suggestions per media type: DVD 0–8, Blu-ray A/B/C, "region free" |
| video_standard | PAL, NTSC, SECAM or empty           |
| is_tv     | TV series (checkbox); lists can filter films vs TV |
| seasons   | Text, e.g. "4" or "1–3"                  |
| audio_format | Free text, e.g. "Dolby Digital 5.1"  |
| video_format | Free text, e.g. "16:9 anamorphic"    |

Book fields (Book, Ebook):

| Field     | Notes                            |
|-----------|----------------------------------|
| publisher |                                  |
| pages     |                                  |
| language  |                                  |
| binding   | Physical books: hardcover, paperback, other or empty |
| file      | Ebook only: path or uploaded file, format (EPUB/PDF) |

**Decided:** credits are one many-to-many link from items to people, with
a role and an order: director, cast, writer, producer and composer for
video; author for books. More roles (translator, illustrator, ...) are
just new values. A person is shared across items, so a search for a name
finds films and books alike.

**Decided:** one `description` field. PHP4DVD's short plot outline and
taglines are not kept as fields (the importer moves them to notes).

**Decided:** no public rating (IMDb/TMDb rating and votes); those go stale.
Instead an optional own rating, which the later IMDb ratings CSV import
fills. Like own/seen/favourite it belongs to the item, not to a user.

**Decided:** series are two optional fields on every item, for all kinds
(book series and film series alike), not a separate table. Lists can sort
by series and then number.

**Decided:** region and video standard are two separate optional fields,
since a DVD has both, a Blu-ray only a region and a VHS tape only a
standard.

**Decided:** Keep all PHP4DVD fields to keep as-is: own, seen, favourite, loans
(loaned/loandate/loanname), trailer, MPAA/PG rating. PHP4DVD's `pg` is a
minimum viewer age and becomes `age_limit`; `mpaa` is the certificate text.
Loan details (who and since when) are hidden from anonymous visitors, like
notes.

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
- **Decided** (milestone 4):
  - Uploads: JPEG and PNG only, at most 15 MB and 50 megapixels. No new
    dependency: the scaler (area averaging, shrinking only) is our own.
  - Every cover is re-encoded as JPEG (quality 90), scaled to fit
    2000 px on the long edge. This strips EXIF (such as GPS positions);
    the EXIF orientation is applied first, so phone photos stand upright.
    Transparent PNG areas become white.
  - The hash is the SHA-256 of the stored JPEG; items store it in
    `items.cover`. The same picture is stored once, shared by items.
  - Thumbnail: `<hash>-thumb.jpg` next to the cover, fitting 400×600.
  - Served at `/covers/<hash>.jpg` and `/covers/<hash>-thumb.jpg` to
    whoever may browse; cached by browsers for good (`private, immutable`).
  - Uploaded on the add and edit forms, which can also remove the cover.
    A form with errors keeps the uploaded cover, so the file needn't be
    chosen again.
  - Files no item uses (trashed items still count) are deleted when the
    trash is emptied and by an hourly cleanup, once unused for an hour.
  - Shown on the item page (linking to the full size), in the grid and as
    a small thumbnail in the list. A "has cover" filter finds items
    without one; a missing cover doesn't make an item incomplete.
  - **Later:** fetching a cover from a URL (with metadata providers,
    milestone 6).

### 3.6 Item lifecycle

- Statuses: `active` (default), `hidden` (not shown in normal lists),
  `trashed`.
- No hard delete, except by emptying the trash (admins only).
- **Decided:** hidden items are only visible to editors and admins, through
  the status filter. Viewers and anonymous visitors never see them.

### 3.7 Manual edits vs. metadata refresh

- Each field records whether a user edited it by hand.
- A metadata refresh never overwrites a hand-edited field.
- **Decided:** deferred to milestone 6 (metadata providers). Until then
  every field is entered by hand anyway. Data imported from PHP4DVD
  counts as not hand-edited.

---

## 4. Adding items

- One add form per item, with an optional lookup box at the top.
- The lookup box accepts a title, a pasted IMDb or TMDb link, an ISBN or a
  barcode. Lookups only fill in the form. Saving never depends on a lookup
  succeeding.
- Barcode input: a USB scanner acting as a keyboard, typing into the lookup
  box.
- **Decided:** adding starts by choosing the media type; the form then
  shows the fields for its kind. **Later:** remember the last type used,
  or offer "add another of this type" after saving.
- **Decided:** one input per identifier type; several values are separated
  by commas.
- **Decided:** credits are entered in one text box per role, one name per
  line, in order. Genres are comma-separated text. Names are matched to
  existing people and genres ignoring case; new ones are created as
  needed. **Later:** autocomplete.
- **Later:** online barcode lookup through pluggable providers.

---

## 5. Browsing and search

- **Decided** (baseline for milestone 3, from PHP4DVD's search page):
  - Views: a table list and a grid. Items without a cover show a
    placeholder in the grid.
  - Sorting: title (by sort title), year, date added, media type and
    series; ascending or descending.
  - Filters: media type, kind, genre, status, incomplete, and own, seen,
    favourite, loaned, has cover (milestone 4).
  - Free-text search across title, original title, alternative titles,
    people and notes,
    using simple `LIKE` matching. **Later:** SQLite full-text search
    (FTS5) if it gets slow or word stemming is needed.
  - 50 items per page.
- **Decided** (milestone 3c):
  - The library is the start page. Filters, sorting, view and page are
    all in the URL, so views can be bookmarked. Default sort: title.
  - The last chosen view (list or grid) is remembered in a cookie.
  - With htmx, filter changes reload only the results; everything also
    works without JavaScript.
  - Editors and admins can toggle own, seen and favourite in the list.
  - Notes and borrowers are searched only for logged-in users. Anonymous
    visitors get no loaned filter or loaned mark either.

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
  | `HOARD_DATA_DIR`  | per-user data folder, see below | Holds `hoard.db` (SQLite) and `covers/` |

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
   - 3a. Schema and item store: media types, items, credits, genres,
     identifiers with normalisation, filters, sorting and paging.
   - 3b. Media type page, item add/edit/view forms, hide/trash/restore,
     trash page.
   - 3c. Library page: list and grid views, filters, search, paging; htmx.
4. Covers: upload on the item forms, re-encoding, thumbnails, serving,
   cleanup of unused files (section 3.5).
5. PHP4DVD importer.
6. Metadata providers.
7. Export, import and backup.
8. Packaging and desktop.

## Appendix B. PHP4DVD → Höðrs Hoard field mapping

Draft; finalised with the importer milestone.

| PHP4DVD                    | Höðrs Hoard                                   |
|----------------------------|-----------------------------------------------|
| name / nameorder           | title / sort_title                            |
| aka                        | alt_titles                                    |
| format                     | media_type (created if unknown)               |
| year, duration             | year, runtime                                 |
| imdbid                     | identifiers (type imdb)                       |
| plots, plotoutline         | description = plots, else plotoutline; a different plotoutline goes to notes |
| taglines                   | notes                                         |
| director, cast, writer, producer, music | credits (director, cast, writer, producer, composer) |
| genres, languages, country | genres, languages, countries                  |
| subtitles, audio, video    | subtitles, audio_format, video_format         |
| tv, seasons                | is_tv, seasons                                |
| rating, votes              | not imported                                  |
| own, seen, favourite       | same                                          |
| loaned, loandate, loanname | loans                                         |
| trailer, mpaa, pg          | same                                          |
| notes, added               | notes, added_at                               |

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
| 2026-11-10 | Admin password resets are temporary: forced change at next login. |
| 2026-11-10 | Forced password change asks for the current password too. |
| 2026-11-10 | Renaming users deferred (Later). |
| 2026-11-10 | Plain HTML forms for user pages; htmx arrives with milestone 3. |
| 2026-11-10 | "Remember me": 30 days, sliding expiry, separate hashed token, no rotation. |
| 2026-11-10 | "Log out everywhere" button on the account page. |
| 2026-11-10 | Book binding is an item field, not separate media types. |
| 2026-11-10 | Region and video standard (PAL/NTSC/SECAM) are two optional fields. |
| 2026-11-10 | Series: name and number fields on every item, all kinds. |
| 2026-11-10 | Browsing baseline: list and grid, LIKE search, 50 per page. |
| 2026-11-11 | Credits: one item–people link with role and order (director, cast, writer, producer, composer, author). |
| 2026-11-11 | Titles: original_title plus alt_titles (PHP4DVD aka). |
| 2026-11-11 | TV series: is_tv flag and seasons text on video items. |
| 2026-11-11 | No public rating/votes; optional own rating (1–10) per item instead. |
| 2026-11-11 | One description field; plot outline and taglines go to notes on import. |
| 2026-11-11 | Countries: multi-value field on all kinds. |
| 2026-11-11 | Audio and video format: free-text fields on video items. |
| 2026-11-11 | Media-type menus are grouped by kind. |
| 2026-11-11 | Incomplete = no year, or no runtime (video), or no author (book). |
| 2026-11-11 | Hidden items are visible to editors and admins only. |
| 2026-11-11 | sort_title moves English articles only (The, A, An) for now. |
| 2026-11-11 | Credits: one text box per role, one name per line; genres comma-separated. |
| 2026-11-11 | Hand-edit tracking (3.7) deferred to milestone 6. |
| 2026-11-11 | htmx 2.x is vendored as one committed file, served from the binary. |
| 2026-11-11 | Adding starts by choosing the media type. Later: remember last type / "add another". |
| 2026-11-11 | An item's media type can't change to another kind. |
| 2026-11-11 | Notes and loan details are hidden from anonymous visitors. |
| 2026-11-11 | Several identifiers of one type: comma-separated in one input. |
| 2026-11-11 | Library view (list/grid) remembered in a cookie; filters and sort only in the URL. |
| 2026-11-11 | In-list toggles for own/seen/favourite: editors and admins only. |
| 2026-11-11 | Loaned filter and mark hidden from anonymous visitors. |
| 2026-11-11 | Library default sort: title. |
| 2026-11-11 | Notes and borrowers searchable by logged-in users only (separate search column). |
| 2026-11-11 | htmx 2.0.4 (0BSD licence), `internal/server/static/htmx-2.0.4.min.js`, SHA-256 e209dda5c8235479f3166defc7750e1dbcd5a5c1808b7792fc2e6733768fb447, identical on unpkg and jsdelivr. |
| 2026-11-12 | Covers: JPEG and PNG uploads only; own scaler, no `golang.org/x/image`. |
| 2026-11-12 | Covers are always re-encoded as JPEG (strips EXIF, applies orientation); at most 15 MB / 50 MP in, 2000 px long edge stored, thumbnails 400×600. |
| 2026-11-12 | Cover thumbnails in the list view too; "has cover" library filter. |
| 2026-11-12 | Fetching covers from a URL deferred to milestone 6. |
