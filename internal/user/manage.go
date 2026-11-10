package user

import (
	"context"
	"database/sql"
	"errors"
	"strings"

	"github.com/keyboardsmash/hodrs-hoard/internal/auth"
)

// This file holds changes to existing users (spec section 2.3). The rules
// about roles and disabling are checked here, inside transactions, so no
// handler can forget them and two requests can't race past them.

// List returns all users, disabled ones included, sorted by username.
func (s *Store) List(ctx context.Context) ([]*User, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+userColumns+` FROM users ORDER BY username_key`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var users []*User
	for rows.Next() {
		u, err := scanUser(rows)
		if err != nil {
			return nil, err
		}
		users = append(users, u)
	}
	return users, rows.Err()
}

// UpdateEmail sets a user's email. An empty string removes it.
func (s *Store) UpdateEmail(ctx context.Context, id int64, email string) error {
	email, err := NormaliseEmail(email)
	if err != nil {
		return err
	}
	return s.inTx(ctx, func(tx *sql.Tx) error {
		if _, err := oneFrom(ctx, tx, `WHERE id = ?`, id); err != nil {
			return err
		}
		return setEmail(ctx, tx, id, email)
	})
}

// SetRole changes the role of user id. actorID is the admin doing it: an
// admin can't change their own role, and the last active admin can't be
// demoted.
func (s *Store) SetRole(ctx context.Context, actorID, id int64, role Role) error {
	return s.UpdateProfile(ctx, actorID, id, nil, role)
}

// UpdateProfile changes email and role together, as the admin's edit form
// does: either both are saved or neither. A nil email leaves it unchanged.
// (A pointer is the usual Go way to say "optional".)
func (s *Store) UpdateProfile(ctx context.Context, actorID, id int64, email *string, role Role) error {
	if !role.Valid() {
		return &FieldError{"role", "Role must be viewer, editor or admin."}
	}
	var normalised string
	if email != nil {
		var err error
		if normalised, err = NormaliseEmail(*email); err != nil { // *email reads the value it points to
			return err
		}
	}
	return s.inTx(ctx, func(tx *sql.Tx) error {
		u, err := oneFrom(ctx, tx, `WHERE id = ?`, id)
		if err != nil {
			return err
		}
		if u.Role != role {
			if id == actorID {
				return ErrOwnRole
			}
			if u.Role == RoleAdmin && !u.Disabled() {
				if err := checkNotLastAdmin(ctx, tx); err != nil {
					return err
				}
			}
			if _, err := tx.ExecContext(ctx, `UPDATE users SET role = ? WHERE id = ?`, string(role), id); err != nil {
				return err
			}
		}
		if email != nil {
			return setEmail(ctx, tx, id, normalised)
		}
		return nil
	})
}

// setEmail stores an already normalised email, checking that no other user
// has it.
func setEmail(ctx context.Context, tx *sql.Tx, id int64, email string) error {
	if email != "" {
		taken, err := exists(ctx, tx, `SELECT 1 FROM users WHERE lower(email) = lower(?) AND id != ?`, email, id)
		if err != nil {
			return err
		}
		if taken {
			return &FieldError{"email", "Another user already has that email address."}
		}
	}
	_, err := tx.ExecContext(ctx, `UPDATE users SET email = ? WHERE id = ?`, nullString(email), id)
	return err
}

// Disable disables user id. Admins can't disable themselves, and the last
// active admin can't be disabled. Disabling a disabled user does nothing.
// The caller should also end the user's sessions.
func (s *Store) Disable(ctx context.Context, actorID, id int64) error {
	return s.inTx(ctx, func(tx *sql.Tx) error {
		u, err := oneFrom(ctx, tx, `WHERE id = ?`, id)
		if err != nil {
			return err
		}
		if u.Disabled() {
			return nil
		}
		if id == actorID {
			return ErrOwnAccount
		}
		if u.Role == RoleAdmin {
			if err := checkNotLastAdmin(ctx, tx); err != nil {
				return err
			}
		}
		_, err = tx.ExecContext(ctx, `UPDATE users SET disabled_at = ? WHERE id = ?`, s.Now().Unix(), id)
		return err
	})
}

// Enable lets a disabled user log in again.
func (s *Store) Enable(ctx context.Context, id int64) error {
	return s.inTx(ctx, func(tx *sql.Tx) error {
		if _, err := oneFrom(ctx, tx, `WHERE id = ?`, id); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, `UPDATE users SET disabled_at = NULL WHERE id = ?`, id)
		return err
	})
}

// SetPassword validates and stores a new password. mustChange marks it as
// temporary (an admin reset), so the user has to pick their own at next
// login. The caller should end the user's other sessions.
func (s *Store) SetPassword(ctx context.Context, id int64, password string, mustChange bool) error {
	if err := ValidatePassword(password); err != nil {
		return err
	}
	hash, err := auth.HashPassword(password) // slow: done before the transaction
	if err != nil {
		return err
	}
	return s.inTx(ctx, func(tx *sql.Tx) error {
		if _, err := oneFrom(ctx, tx, `WHERE id = ?`, id); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx,
			`UPDATE users SET password_hash = ?, must_change_password = ? WHERE id = ?`, hash, mustChange, id)
		return err
	})
}

// ChangeOwnPassword is SetPassword for users changing their own password:
// the current password must be right and the new one must differ. It
// clears the "must change password" flag.
func (s *Store) ChangeOwnPassword(ctx context.Context, id int64, current, next string) error {
	u, err := s.ByID(ctx, id)
	if err != nil {
		return err
	}
	ok, _, err := auth.VerifyPassword(current, u.PasswordHash)
	if err != nil {
		return err
	}
	if !ok {
		return &FieldError{"current", "Your current password is wrong."}
	}
	if current == next {
		return ErrSamePassword
	}
	return s.SetPassword(ctx, id, next, false)
}

// checkNotLastAdmin returns ErrLastAdmin unless at least two active admins
// exist, so one of them may be demoted or disabled.
func checkNotLastAdmin(ctx context.Context, tx *sql.Tx) error {
	var n int
	if err := tx.QueryRowContext(ctx,
		`SELECT count(*) FROM users WHERE role = 'admin' AND disabled_at IS NULL`).Scan(&n); err != nil {
		return err
	}
	if n < 2 {
		return ErrLastAdmin
	}
	return nil
}

// inTx runs fn in a transaction: committed if fn returns nil, rolled back
// otherwise. `fn func(*sql.Tx) error` is a function passed as a value.
func (s *Store) inTx(ctx context.Context, fn func(*sql.Tx) error) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback() // no-op after Commit
	if err := fn(tx); err != nil {
		return err
	}
	return tx.Commit()
}

// Message returns a user-facing sentence for errors from this package, or
// "" for errors that aren't the user's to fix (database failures).
func Message(err error) string {
	var fe *FieldError
	if errors.As(err, &fe) {
		return fe.Message
	}
	for _, known := range []error{ErrOwnRole, ErrOwnAccount, ErrLastAdmin, ErrSamePassword, ErrNotFound} {
		if errors.Is(err, known) {
			s := known.Error()
			return strings.ToUpper(s[:1]) + s[1:] + "."
		}
	}
	return ""
}
