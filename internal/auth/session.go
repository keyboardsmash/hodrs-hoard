package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"time"
)

// Session lifetimes (spec section 2.6).
const (
	// IdleTimeout ends a session that hasn't been used for this long.
	IdleTimeout = 12 * time.Hour
	// MaxLifetime ends a session this long after login, however active.
	MaxLifetime = 7 * 24 * time.Hour
	// touchInterval limits how often last_seen_at is written, so every page
	// view doesn't cost a database write.
	touchInterval = time.Minute
)

// ErrNoSession means the token is unknown, expired or idle too long.
var ErrNoSession = errors.New("no valid session")

// Sessions stores login sessions in the sessions table.
type Sessions struct {
	db *sql.DB
	// Now returns the current time. Tests replace it to move time forward.
	Now func() time.Time
}

// NewSessions returns a session store using db.
func NewSessions(db *sql.DB) *Sessions {
	return &Sessions{db: db, Now: time.Now}
}

// Create starts a session for userID and returns the token for the cookie.
func (s *Sessions) Create(ctx context.Context, userID int64) (string, error) {
	raw := make([]byte, 32) // 256 bits: impossible to guess
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	token := base64.RawURLEncoding.EncodeToString(raw)
	now := s.Now().Unix()
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO sessions (token_hash, user_id, created_at, last_seen_at, expires_at)
		 VALUES (?, ?, ?, ?, ?)`,
		hashToken(token), userID, now, now, now+int64(MaxLifetime/time.Second))
	if err != nil {
		return "", err
	}
	return token, nil
}

// Lookup returns the user ID for a valid token and records the activity.
// Expired sessions are deleted and give ErrNoSession.
func (s *Sessions) Lookup(ctx context.Context, token string) (int64, error) {
	if token == "" {
		return 0, ErrNoSession
	}
	h := hashToken(token)
	var userID, lastSeen, expires int64
	err := s.db.QueryRowContext(ctx,
		`SELECT user_id, last_seen_at, expires_at FROM sessions WHERE token_hash = ?`, h,
	).Scan(&userID, &lastSeen, &expires)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, ErrNoSession
	}
	if err != nil {
		return 0, err
	}

	now := s.Now().Unix()
	if now >= expires || now-lastSeen >= int64(IdleTimeout/time.Second) {
		if _, err := s.db.ExecContext(ctx, `DELETE FROM sessions WHERE token_hash = ?`, h); err != nil {
			return 0, err
		}
		return 0, ErrNoSession
	}
	if now-lastSeen >= int64(touchInterval/time.Second) {
		if _, err := s.db.ExecContext(ctx,
			`UPDATE sessions SET last_seen_at = ? WHERE token_hash = ?`, now, h); err != nil {
			return 0, err
		}
	}
	return userID, nil
}

// Delete ends one session (logout). Unknown tokens are not an error.
func (s *Sessions) Delete(ctx context.Context, token string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM sessions WHERE token_hash = ?`, hashToken(token))
	return err
}

// DeleteForUser ends all sessions of a user, for example when the user is
// disabled or their password is reset.
func (s *Sessions) DeleteForUser(ctx context.Context, userID int64) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM sessions WHERE user_id = ?`, userID)
	return err
}

// DeleteExpired removes sessions that are past their lifetime or idle too
// long, and returns how many were removed.
func (s *Sessions) DeleteExpired(ctx context.Context) (int64, error) {
	now := s.Now().Unix()
	res, err := s.db.ExecContext(ctx,
		`DELETE FROM sessions WHERE expires_at <= ? OR last_seen_at <= ?`,
		now, now-int64(IdleTimeout/time.Second))
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// hashToken is what the database stores instead of the token, so a copy of
// the database (a backup, say) can't be used to take over sessions.
// The token is random and long, so a fast hash is enough here.
func hashToken(token string) string {
	sum := sha256.Sum256([]byte(token)) // returns a [32]byte array
	return hex.EncodeToString(sum[:])   // [:] turns the array into a slice
}
