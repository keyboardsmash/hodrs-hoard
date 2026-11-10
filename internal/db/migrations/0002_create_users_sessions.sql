-- Users and login sessions (spec section 2).
--
-- Timestamps are Unix seconds (INTEGER, UTC). Integers compare and sort
-- correctly in SQL, which the session expiry queries rely on.

CREATE TABLE users (
    id                   INTEGER PRIMARY KEY,
    -- username as the user typed it, shown in the UI.
    username             TEXT    NOT NULL,
    -- Lowercased username for case-insensitive lookups and uniqueness.
    -- Lowercased in Go because SQLite's NOCASE only folds ASCII letters.
    username_key         TEXT    NOT NULL UNIQUE,
    email                TEXT,
    password_hash        TEXT    NOT NULL,
    role                 TEXT    NOT NULL CHECK (role IN ('viewer', 'editor', 'admin')),
    created_at           INTEGER NOT NULL,
    last_login_at        INTEGER,
    disabled_at          INTEGER,
    must_change_password INTEGER NOT NULL DEFAULT 0 CHECK (must_change_password IN (0, 1))
);

-- Email is optional but unique when set. A partial index (WHERE ...) only
-- covers rows with an email, so many users can have none.
CREATE UNIQUE INDEX users_email_key ON users (lower(email)) WHERE email IS NOT NULL;

CREATE TABLE sessions (
    -- SHA-256 of the cookie token, hex. The token itself is never stored.
    token_hash   TEXT    PRIMARY KEY,
    user_id      INTEGER NOT NULL REFERENCES users (id),
    created_at   INTEGER NOT NULL,
    last_seen_at INTEGER NOT NULL,
    -- Absolute end of the session, regardless of activity.
    expires_at   INTEGER NOT NULL
);

CREATE INDEX sessions_user_id ON sessions (user_id);
CREATE INDEX sessions_expires_at ON sessions (expires_at);
