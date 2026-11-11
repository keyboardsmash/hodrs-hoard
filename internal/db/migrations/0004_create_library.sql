-- Media types and items (spec section 3).
--
-- Conventions, as in 0002:
--   - Timestamps are Unix seconds (INTEGER, UTC).
--   - Optional values are NULL, never "" or 0.
--   - *_key columns hold a lowercased copy made in Go, for case-insensitive
--     uniqueness and sorting. SQLite's NOCASE only folds ASCII letters.

CREATE TABLE media_types (
    id         INTEGER PRIMARY KEY,
    name       TEXT    NOT NULL,
    name_key   TEXT    NOT NULL UNIQUE,
    -- The kind decides which fields and metadata providers apply.
    kind       TEXT    NOT NULL CHECK (kind IN ('video', 'book')),
    physical   INTEGER NOT NULL CHECK (physical IN (0, 1)),
    -- Order within menus; new types go last.
    sort_order INTEGER NOT NULL DEFAULT 0
);

INSERT INTO media_types (name, name_key, kind, physical, sort_order) VALUES
    ('DVD',   'dvd',   'video', 1, 10),
    ('VHS',   'vhs',   'video', 1, 20),
    ('Book',  'book',  'book',  1, 30),
    ('Ebook', 'ebook', 'book',  0, 40);

CREATE TABLE items (
    id             INTEGER PRIMARY KEY,
    media_type_id  INTEGER NOT NULL REFERENCES media_types (id),
    -- Box sets (spec 3.4): the item this one is part of. Emptying the trash
    -- of a box set turns its children into stand-alone items.
    parent_id      INTEGER REFERENCES items (id) ON DELETE SET NULL,
    status         TEXT    NOT NULL DEFAULT 'active'
                           CHECK (status IN ('active', 'hidden', 'trashed')),
    trashed_at     INTEGER,

    title          TEXT    NOT NULL,
    -- sort_title is only set when a user typed one; otherwise it is derived
    -- from the title. sort_key is always set: the effective sort title,
    -- lowercased.
    sort_title     TEXT,
    sort_key       TEXT    NOT NULL,
    original_title TEXT,
    alt_titles     TEXT,   -- one per line
    year           INTEGER,
    description    TEXT,
    notes          TEXT,
    own_rating     INTEGER CHECK (own_rating BETWEEN 1 AND 10),
    series         TEXT,
    series_key     TEXT,
    series_number  TEXT,

    own            INTEGER NOT NULL DEFAULT 1 CHECK (own IN (0, 1)),
    seen           INTEGER NOT NULL DEFAULT 0 CHECK (seen IN (0, 1)),
    favourite      INTEGER NOT NULL DEFAULT 0 CHECK (favourite IN (0, 1)),
    loaned         INTEGER NOT NULL DEFAULT 0 CHECK (loaned IN (0, 1)),
    loaned_to      TEXT,
    loaned_on      TEXT,   -- a date, 'YYYY-MM-DD'
    trailer        TEXT,   -- URL
    mpaa           TEXT,   -- certificate, e.g. 'PG-13'
    age_limit      INTEGER CHECK (age_limit BETWEEN 0 AND 21),

    -- Video
    runtime        INTEGER,  -- minutes
    region         TEXT,
    video_standard TEXT CHECK (video_standard IN ('PAL', 'NTSC', 'SECAM')),
    is_tv          INTEGER NOT NULL DEFAULT 0 CHECK (is_tv IN (0, 1)),
    seasons        TEXT,
    audio_format   TEXT,
    video_format   TEXT,

    -- Book
    publisher      TEXT,
    pages          INTEGER,
    binding        TEXT CHECK (binding IN ('hardcover', 'paperback', 'other')),
    file_format    TEXT CHECK (file_format IN ('epub', 'pdf')),
    file_path      TEXT,

    -- Lowercased title, other titles, people and notes, for free-text
    -- search (spec 5). Rebuilt by Go on every save.
    search_text    TEXT    NOT NULL,

    added_at       INTEGER NOT NULL,
    updated_at     INTEGER NOT NULL,
    added_by       INTEGER REFERENCES users (id),
    updated_by     INTEGER REFERENCES users (id)
);

CREATE INDEX items_media_type ON items (media_type_id);
CREATE INDEX items_parent ON items (parent_id);
CREATE INDEX items_status_sort ON items (status, sort_key);

-- People appear in credits. One row per person, shared by all their items.
CREATE TABLE people (
    id       INTEGER PRIMARY KEY,
    name     TEXT NOT NULL,
    name_key TEXT NOT NULL UNIQUE
);

CREATE TABLE item_credits (
    item_id   INTEGER NOT NULL REFERENCES items (id) ON DELETE CASCADE,
    person_id INTEGER NOT NULL REFERENCES people (id),
    role      TEXT    NOT NULL CHECK (role IN ('director', 'cast', 'writer', 'producer', 'composer', 'author')),
    -- Order within the role: 0 is listed first.
    position  INTEGER NOT NULL,
    PRIMARY KEY (item_id, role, position)
);

CREATE INDEX item_credits_person ON item_credits (person_id);

CREATE TABLE genres (
    id       INTEGER PRIMARY KEY,
    name     TEXT NOT NULL,
    name_key TEXT NOT NULL UNIQUE
);

CREATE TABLE item_genres (
    item_id  INTEGER NOT NULL REFERENCES items (id) ON DELETE CASCADE,
    genre_id INTEGER NOT NULL REFERENCES genres (id),
    PRIMARY KEY (item_id, genre_id)
);

CREATE INDEX item_genres_genre ON item_genres (genre_id);

-- Simple multi-value lists: languages, subtitles, countries.
CREATE TABLE item_values (
    item_id  INTEGER NOT NULL REFERENCES items (id) ON DELETE CASCADE,
    field    TEXT    NOT NULL CHECK (field IN ('language', 'subtitle', 'country')),
    position INTEGER NOT NULL,
    value    TEXT    NOT NULL,
    PRIMARY KEY (item_id, field, position)
);

-- External identifiers (spec 3.3). Normalised by Go before saving.
-- Indexed but not unique: two items with the same ISBN get a warning.
CREATE TABLE identifiers (
    item_id INTEGER NOT NULL REFERENCES items (id) ON DELETE CASCADE,
    type    TEXT    NOT NULL,
    value   TEXT    NOT NULL,
    PRIMARY KEY (item_id, type, value)
);

CREATE INDEX identifiers_value ON identifiers (type, value);
