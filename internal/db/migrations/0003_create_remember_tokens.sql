-- "Remember me" tokens (spec section 2.6). When a browser's session has
-- ended, a valid remember token starts a new one without a password.
--
-- Timestamps are Unix seconds, as in 0002.

CREATE TABLE remember_tokens (
    -- SHA-256 of the cookie token, hex. The token itself is never stored.
    token_hash   TEXT    PRIMARY KEY,
    user_id      INTEGER NOT NULL REFERENCES users (id),
    created_at   INTEGER NOT NULL,
    last_used_at INTEGER NOT NULL,
    -- Moves forward each time the token is used (sliding expiry).
    expires_at   INTEGER NOT NULL
);

CREATE INDEX remember_tokens_user_id ON remember_tokens (user_id);
CREATE INDEX remember_tokens_expires_at ON remember_tokens (expires_at);
