// Package user stores user accounts (spec section 2).
package user

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/mail"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/keyboardsmash/hodrs-hoard/internal/auth"
)

// Role decides what a user may do. Declaring a named string type means a
// plain string can't be passed where a Role is expected by accident.
type Role string

const (
	RoleViewer Role = "viewer"
	RoleEditor Role = "editor"
	RoleAdmin  Role = "admin"
)

// Valid reports whether r is one of the three roles.
func (r Role) Valid() bool {
	return r == RoleViewer || r == RoleEditor || r == RoleAdmin
}

// Limits for user input.
const (
	MaxUsernameLen   = 64   // characters
	MinPasswordLen   = 10   // characters
	MaxPasswordBytes = 1024 // bytes; stops absurdly large form posts
	maxEmailLen      = 254  // RFC 5321 limit
)

// User is one account. Zero time values mean "never" (for LastLoginAt) or
// "not disabled" (for DisabledAt).
type User struct {
	ID                 int64
	Username           string
	Email              string // empty if none
	PasswordHash       string
	Role               Role
	CreatedAt          time.Time
	LastLoginAt        time.Time
	DisabledAt         time.Time
	MustChangePassword bool
}

// Disabled reports whether an admin has disabled the user.
func (u *User) Disabled() bool { return !u.DisabledAt.IsZero() }

// New holds the input for creating a user. The password is plain text here
// and hashed by Create.
type New struct {
	Username           string
	Email              string
	Password           string
	Role               Role
	MustChangePassword bool
}

// FieldError is a validation problem with one form field. Handlers show
// Message next to Field.
type FieldError struct {
	Field   string
	Message string
}

func (e *FieldError) Error() string { return e.Field + ": " + e.Message }

// Errors callers can test for with errors.Is.
var (
	ErrNotFound  = errors.New("user not found")
	ErrSetupDone = errors.New("setup already done: users exist")
)

// Store reads and writes the users table.
type Store struct {
	db *sql.DB
	// Now returns the current time. Tests may replace it.
	Now func() time.Time
}

// NewStore returns a user store using db.
func NewStore(db *sql.DB) *Store {
	return &Store{db: db, Now: time.Now}
}

// UsernameKey is the case-insensitive form of a username, used for lookups
// and uniqueness. strings.ToLower handles all Unicode letters, unlike
// SQLite's NOCASE, so "Höðr" and "HÖÐR" are the same user.
func UsernameKey(username string) string {
	return strings.ToLower(strings.TrimSpace(username))
}

// ValidateUsername checks a username: 1-64 characters, letters, digits and
// . _ - @ only. Unicode letters are allowed.
func ValidateUsername(username string) error {
	username = strings.TrimSpace(username)
	n := utf8.RuneCountInString(username)
	if n == 0 {
		return &FieldError{"username", "Username is required."}
	}
	if n > MaxUsernameLen {
		return &FieldError{"username", fmt.Sprintf("Username can be at most %d characters.", MaxUsernameLen)}
	}
	// Ranging over a string yields runes (Unicode code points), not bytes.
	for _, r := range username {
		if !unicode.IsLetter(r) && !unicode.IsDigit(r) && !strings.ContainsRune("._-@", r) {
			return &FieldError{"username", "Username can only contain letters, digits and . _ - @"}
		}
	}
	return nil
}

// ValidatePassword checks the length rules. There are no complexity rules:
// length matters most, and the rules annoy users into weaker choices.
func ValidatePassword(password string) error {
	if utf8.RuneCountInString(password) < MinPasswordLen {
		return &FieldError{"password", fmt.Sprintf("Password must be at least %d characters.", MinPasswordLen)}
	}
	if len(password) > MaxPasswordBytes {
		return &FieldError{"password", "Password is too long."}
	}
	return nil
}

// NormaliseEmail trims an email address and checks that it is a plain
// address (no display name). An empty string is fine: email is optional.
func NormaliseEmail(email string) (string, error) {
	email = strings.TrimSpace(email)
	if email == "" {
		return "", nil
	}
	addr, err := mail.ParseAddress(email)
	if err != nil || addr.Address != email || len(email) > maxEmailLen {
		return "", &FieldError{"email", "Email address is not valid."}
	}
	return email, nil
}

// Create validates n, hashes the password and inserts the user.
func (s *Store) Create(ctx context.Context, n New) (*User, error) {
	return s.create(ctx, n, false)
}

// CreateFirstAdmin creates the initial admin, but only while there are no
// users at all. If another request created one first it returns
// ErrSetupDone, so two people running setup at once can't both succeed.
func (s *Store) CreateFirstAdmin(ctx context.Context, n New) (*User, error) {
	n.Role = RoleAdmin
	return s.create(ctx, n, true)
}

func (s *Store) create(ctx context.Context, n New, onlyIfEmpty bool) (*User, error) {
	if err := ValidateUsername(n.Username); err != nil {
		return nil, err
	}
	email, err := NormaliseEmail(n.Email)
	if err != nil {
		return nil, err
	}
	if err := ValidatePassword(n.Password); err != nil {
		return nil, err
	}
	if !n.Role.Valid() {
		return nil, &FieldError{"role", "Role must be viewer, editor or admin."}
	}

	// Hash before the transaction starts: it takes ~100 ms and the
	// transaction holds the database write lock.
	hash, err := auth.HashPassword(n.Password)
	if err != nil {
		return nil, err
	}

	u := &User{
		Username:           strings.TrimSpace(n.Username),
		Email:              email,
		PasswordHash:       hash,
		Role:               n.Role,
		CreatedAt:          time.Unix(s.Now().Unix(), 0), // whole seconds, as stored
		MustChangePassword: n.MustChangePassword,
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback() // no-op after Commit

	// The checks below run inside the transaction, which holds the write
	// lock (see _txlock=immediate in internal/db), so nothing can slip in
	// between a check and the insert. The UNIQUE indexes are a backstop.
	if onlyIfEmpty {
		var count int
		if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM users`).Scan(&count); err != nil {
			return nil, err
		}
		if count > 0 {
			return nil, ErrSetupDone
		}
	}
	if taken, err := exists(ctx, tx, `SELECT 1 FROM users WHERE username_key = ?`, UsernameKey(u.Username)); err != nil {
		return nil, err
	} else if taken {
		return nil, &FieldError{"username", "That username is already taken."}
	}
	if email != "" {
		if taken, err := exists(ctx, tx, `SELECT 1 FROM users WHERE lower(email) = lower(?)`, email); err != nil {
			return nil, err
		} else if taken {
			return nil, &FieldError{"email", "Another user already has that email address."}
		}
	}

	res, err := tx.ExecContext(ctx,
		`INSERT INTO users (username, username_key, email, password_hash, role, created_at, must_change_password)
		 VALUES (?, ?, ?, ?, ?, ?, ?)`,
		u.Username, UsernameKey(u.Username), nullString(email), u.PasswordHash, string(u.Role),
		u.CreatedAt.Unix(), u.MustChangePassword)
	if err != nil {
		return nil, err
	}
	if u.ID, err = res.LastInsertId(); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return u, nil
}

// Count returns the number of users, disabled ones included.
func (s *Store) Count(ctx context.Context) (int, error) {
	var n int
	err := s.db.QueryRowContext(ctx, `SELECT count(*) FROM users`).Scan(&n)
	return n, err
}

// ByID returns the user with the given ID, or ErrNotFound.
func (s *Store) ByID(ctx context.Context, id int64) (*User, error) {
	return s.one(ctx, `WHERE id = ?`, id)
}

// ByUsername finds a user case-insensitively, or returns ErrNotFound.
func (s *Store) ByUsername(ctx context.Context, username string) (*User, error) {
	return s.one(ctx, `WHERE username_key = ?`, UsernameKey(username))
}

// TouchLogin records a successful login.
func (s *Store) TouchLogin(ctx context.Context, id int64) error {
	_, err := s.db.ExecContext(ctx, `UPDATE users SET last_login_at = ? WHERE id = ?`, s.Now().Unix(), id)
	return err
}

// SetPasswordHash replaces a user's stored hash, for example when login
// re-hashes a password with newer parameters.
func (s *Store) SetPasswordHash(ctx context.Context, id int64, hash string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE users SET password_hash = ? WHERE id = ?`, hash, id)
	return err
}

// one runs a single-row SELECT on users with the given WHERE clause.
// `args ...any` is a variadic parameter: any number of values of any type.
func (s *Store) one(ctx context.Context, where string, args ...any) (*User, error) {
	var (
		u                  User
		email              sql.NullString // NULL-able columns scan into Null* types
		role               string
		created            int64
		lastLogin, disable sql.NullInt64
	)
	err := s.db.QueryRowContext(ctx,
		`SELECT id, username, email, password_hash, role, created_at, last_login_at, disabled_at, must_change_password
		 FROM users `+where, args...,
	).Scan(&u.ID, &u.Username, &email, &u.PasswordHash, &role, &created, &lastLogin, &disable, &u.MustChangePassword)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	u.Email = email.String
	u.Role = Role(role)
	u.CreatedAt = time.Unix(created, 0)
	if lastLogin.Valid {
		u.LastLoginAt = time.Unix(lastLogin.Int64, 0)
	}
	if disable.Valid {
		u.DisabledAt = time.Unix(disable.Int64, 0)
	}
	return &u, nil
}

// querier is satisfied by both *sql.DB and *sql.Tx.
type querier interface {
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

func exists(ctx context.Context, q querier, query string, args ...any) (bool, error) {
	var one int
	err := q.QueryRowContext(ctx, query, args...).Scan(&one)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	return err == nil, err
}

// nullString stores an empty string as SQL NULL, which the partial email
// index relies on.
func nullString(s string) sql.NullString {
	return sql.NullString{String: s, Valid: s != ""}
}
