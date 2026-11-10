package auth

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

// RememberLifetime is how long a "remember me" token lasts without use.
// Each use moves the expiry forward again (sliding expiry), so someone who
// visits at least once in this period stays logged in.
const RememberLifetime = 30 * 24 * time.Hour

// ErrNoRememberToken means the token is unknown or has expired.
var ErrNoRememberToken = errors.New("no valid remember token")

// RememberTokens stores "remember me" tokens in the remember_tokens table.
//
// PHP4DVD split such tokens into a selector and a validator. That's needed
// when the validator is stored with a slow hash, or compared after a
// lookup. A 256-bit random token stored as a SHA-256 needs neither, so one
// token works the same way as a session token.
type RememberTokens struct {
	db *sql.DB
	// Now returns the current time. Tests replace it to move time forward.
	Now func() time.Time
}

// NewRememberTokens returns a token store using db.
func NewRememberTokens(db *sql.DB) *RememberTokens {
	return &RememberTokens{db: db, Now: time.Now}
}

// Create makes a token for userID and returns it for the cookie.
func (t *RememberTokens) Create(ctx context.Context, userID int64) (string, error) {
	token, err := newToken()
	if err != nil {
		return "", err
	}
	now := t.Now().Unix()
	_, err = t.db.ExecContext(ctx,
		`INSERT INTO remember_tokens (token_hash, user_id, created_at, last_used_at, expires_at)
		 VALUES (?, ?, ?, ?, ?)`,
		hashToken(token), userID, now, now, now+int64(RememberLifetime/time.Second))
	if err != nil {
		return "", err
	}
	return token, nil
}

// Use checks a token and, if it's valid, returns its user and moves the
// expiry forward. Expired tokens are deleted and give ErrNoRememberToken.
// The token itself stays the same (no rotation; see spec 2.6).
func (t *RememberTokens) Use(ctx context.Context, token string) (int64, error) {
	if token == "" {
		return 0, ErrNoRememberToken
	}
	h := hashToken(token)
	var userID, expires int64
	err := t.db.QueryRowContext(ctx,
		`SELECT user_id, expires_at FROM remember_tokens WHERE token_hash = ?`, h,
	).Scan(&userID, &expires)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, ErrNoRememberToken
	}
	if err != nil {
		return 0, err
	}

	now := t.Now().Unix()
	if now >= expires {
		if _, err := t.db.ExecContext(ctx, `DELETE FROM remember_tokens WHERE token_hash = ?`, h); err != nil {
			return 0, err
		}
		return 0, ErrNoRememberToken
	}
	if _, err := t.db.ExecContext(ctx,
		`UPDATE remember_tokens SET last_used_at = ?, expires_at = ? WHERE token_hash = ?`,
		now, now+int64(RememberLifetime/time.Second), h); err != nil {
		return 0, err
	}
	return userID, nil
}

// Delete removes one token (logout). Unknown tokens are not an error.
func (t *RememberTokens) Delete(ctx context.Context, token string) error {
	_, err := t.db.ExecContext(ctx, `DELETE FROM remember_tokens WHERE token_hash = ?`, hashToken(token))
	return err
}

// DeleteForUser removes all tokens of a user.
func (t *RememberTokens) DeleteForUser(ctx context.Context, userID int64) error {
	_, err := t.db.ExecContext(ctx, `DELETE FROM remember_tokens WHERE user_id = ?`, userID)
	return err
}

// DeleteOthers removes all tokens of a user except keepToken. An empty
// keepToken removes them all.
func (t *RememberTokens) DeleteOthers(ctx context.Context, userID int64, keepToken string) error {
	_, err := t.db.ExecContext(ctx, `DELETE FROM remember_tokens WHERE user_id = ? AND token_hash != ?`,
		userID, hashToken(keepToken))
	return err
}

// DeleteExpired removes expired tokens and returns how many were removed.
func (t *RememberTokens) DeleteExpired(ctx context.Context) (int64, error) {
	res, err := t.db.ExecContext(ctx, `DELETE FROM remember_tokens WHERE expires_at <= ?`, t.Now().Unix())
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}
