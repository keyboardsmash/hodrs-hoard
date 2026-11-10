package user

import (
	"context"
	"errors"
	"testing"

	"github.com/keyboardsmash/hodrs-hoard/internal/auth"
)

// seed creates users with the given roles, named u0, u1, ..., and returns them.
func seed(t *testing.T, s *Store, roles ...Role) []*User {
	t.Helper()
	var users []*User
	for i, r := range roles {
		u, err := s.Create(context.Background(), New{
			Username: string(rune('a'+i)) + "user", Password: goodPassword, Role: r,
		})
		if err != nil {
			t.Fatal(err)
		}
		users = append(users, u)
	}
	return users
}

func reload(t *testing.T, s *Store, u *User) *User {
	t.Helper()
	got, err := s.ByID(context.Background(), u.ID)
	if err != nil {
		t.Fatal(err)
	}
	return got
}

func TestRoleAtLeast(t *testing.T) {
	cases := []struct {
		r, min Role
		want   bool
	}{
		{RoleAdmin, RoleViewer, true}, {RoleAdmin, RoleAdmin, true},
		{RoleEditor, RoleViewer, true}, {RoleEditor, RoleAdmin, false},
		{RoleViewer, RoleEditor, false}, {"", RoleViewer, false}, {"root", RoleViewer, false},
	}
	for _, tc := range cases {
		if got := tc.r.AtLeast(tc.min); got != tc.want {
			t.Errorf("%q.AtLeast(%q) = %v", tc.r, tc.min, got)
		}
	}
}

func TestList(t *testing.T) {
	s := newStore(t)
	seed(t, s, RoleViewer, RoleAdmin)
	s.Create(context.Background(), New{Username: "Aaron", Password: goodPassword, Role: RoleViewer})
	users, err := s.List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, u := range users {
		names = append(names, u.Username)
	}
	// Sorted case-insensitively.
	if len(names) != 3 || names[0] != "Aaron" || names[1] != "auser" || names[2] != "buser" {
		t.Errorf("List = %v", names)
	}
}

func TestUpdateEmail(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)
	u := seed(t, s, RoleViewer, RoleViewer)

	if err := s.UpdateEmail(ctx, u[0].ID, "a@example.com"); err != nil {
		t.Fatal(err)
	}
	// Saving the same address again (in another case) is fine for its owner...
	if err := s.UpdateEmail(ctx, u[0].ID, "A@example.com"); err != nil {
		t.Errorf("own email again: %v", err)
	}
	// ...but not for someone else.
	if err := s.UpdateEmail(ctx, u[1].ID, "a@EXAMPLE.com"); fieldOf(err) != "email" {
		t.Errorf("taken email: %v", err)
	}
	if err := s.UpdateEmail(ctx, u[1].ID, "not an email"); fieldOf(err) != "email" {
		t.Errorf("invalid email: %v", err)
	}
	if err := s.UpdateEmail(ctx, u[0].ID, ""); err != nil || reload(t, s, u[0]).Email != "" {
		t.Errorf("removing email: %v", err)
	}
	if err := s.UpdateEmail(ctx, 999, ""); !errors.Is(err, ErrNotFound) {
		t.Errorf("unknown user: %v", err)
	}
}

func TestSetRoleRules(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)
	u := seed(t, s, RoleAdmin, RoleAdmin, RoleViewer)
	admin1, admin2, viewer := u[0], u[1], u[2]

	if err := s.SetRole(ctx, admin1.ID, viewer.ID, RoleEditor); err != nil {
		t.Fatal(err)
	}
	if r := reload(t, s, viewer).Role; r != RoleEditor {
		t.Errorf("role = %s, want editor", r)
	}
	if err := s.SetRole(ctx, admin1.ID, admin1.ID, RoleViewer); !errors.Is(err, ErrOwnRole) {
		t.Errorf("own role: %v", err)
	}
	// Saving your own unchanged role (e.g. the edit form) is not an error.
	if err := s.SetRole(ctx, admin1.ID, admin1.ID, RoleAdmin); err != nil {
		t.Errorf("own role unchanged: %v", err)
	}
	if err := s.SetRole(ctx, admin1.ID, viewer.ID, "root"); fieldOf(err) != "role" {
		t.Errorf("invalid role: %v", err)
	}

	// Two admins: one may demote the other.
	if err := s.SetRole(ctx, admin1.ID, admin2.ID, RoleEditor); err != nil {
		t.Fatalf("demote second admin: %v", err)
	}
	// Now admin1 is the last one. Even "someone else" (a future CLI, say)
	// can't demote them.
	if err := s.SetRole(ctx, 0, admin1.ID, RoleEditor); !errors.Is(err, ErrLastAdmin) {
		t.Errorf("demote last admin: %v", err)
	}
}

func TestUpdateProfileIsAtomic(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)
	u := seed(t, s, RoleAdmin, RoleViewer)
	admin, viewer := u[0], u[1]
	s.UpdateEmail(ctx, admin.ID, "taken@example.com")

	// The role change is fine but the email is taken: nothing is saved.
	email := "taken@example.com"
	if err := s.UpdateProfile(ctx, admin.ID, viewer.ID, &email, RoleEditor); fieldOf(err) != "email" {
		t.Fatalf("taken email: %v", err)
	}
	if r := reload(t, s, viewer).Role; r != RoleViewer {
		t.Errorf("role changed to %s although the save failed", r)
	}

	// The email is fine but the role change breaks a rule: nothing is saved.
	email = "new@example.com"
	if err := s.UpdateProfile(ctx, admin.ID, admin.ID, &email, RoleViewer); !errors.Is(err, ErrOwnRole) {
		t.Fatalf("own role: %v", err)
	}
	if e := reload(t, s, admin).Email; e != "taken@example.com" {
		t.Errorf("email changed to %s although the save failed", e)
	}

	if err := s.UpdateProfile(ctx, admin.ID, viewer.ID, &email, RoleEditor); err != nil {
		t.Fatal(err)
	}
	if got := reload(t, s, viewer); got.Role != RoleEditor || got.Email != email {
		t.Errorf("after save: %+v", got)
	}
}

func TestDisableEnable(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)
	u := seed(t, s, RoleAdmin, RoleAdmin, RoleEditor)
	admin1, admin2, editor := u[0], u[1], u[2]

	if err := s.Disable(ctx, admin1.ID, admin1.ID); !errors.Is(err, ErrOwnAccount) {
		t.Errorf("disable self: %v", err)
	}
	if err := s.Disable(ctx, admin1.ID, editor.ID); err != nil || !reload(t, s, editor).Disabled() {
		t.Fatalf("disable editor: %v", err)
	}
	// Disabled users keep their username reserved.
	if _, err := s.Create(ctx, New{Username: editor.Username, Password: goodPassword, Role: RoleViewer}); fieldOf(err) != "username" {
		t.Errorf("reuse of disabled username: %v", err)
	}
	if err := s.Enable(ctx, editor.ID); err != nil || reload(t, s, editor).Disabled() {
		t.Errorf("enable: %v", err)
	}

	if err := s.Disable(ctx, admin1.ID, admin2.ID); err != nil {
		t.Fatalf("disable second admin: %v", err)
	}
	if err := s.Disable(ctx, 0, admin1.ID); !errors.Is(err, ErrLastAdmin) {
		t.Errorf("disable last admin: %v", err)
	}
	// A disabled admin doesn't count, and demoting one is allowed.
	if err := s.SetRole(ctx, admin1.ID, admin2.ID, RoleViewer); err != nil {
		t.Errorf("demote disabled admin: %v", err)
	}
	if err := s.Disable(ctx, admin1.ID, 999); !errors.Is(err, ErrNotFound) {
		t.Errorf("unknown user: %v", err)
	}
}

func TestPasswords(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)
	u := seed(t, s, RoleViewer)[0]
	verify := func(pw string) bool {
		ok, _, _ := auth.VerifyPassword(pw, reload(t, s, u).PasswordHash)
		return ok
	}

	// Admin reset: temporary password.
	if err := s.SetPassword(ctx, u.ID, "temporary password", true); err != nil {
		t.Fatal(err)
	}
	if !verify("temporary password") || !reload(t, s, u).MustChangePassword {
		t.Error("reset did not set the password and the must-change flag")
	}
	if err := s.SetPassword(ctx, u.ID, "short", true); fieldOf(err) != "password" {
		t.Errorf("short reset: %v", err)
	}

	// The user changes it.
	if err := s.ChangeOwnPassword(ctx, u.ID, "wrong current", "my own password"); fieldOf(err) != "current" {
		t.Errorf("wrong current password: %v", err)
	}
	if err := s.ChangeOwnPassword(ctx, u.ID, "temporary password", "temporary password"); !errors.Is(err, ErrSamePassword) {
		t.Errorf("same password: %v", err)
	}
	if err := s.ChangeOwnPassword(ctx, u.ID, "temporary password", "short"); fieldOf(err) != "password" {
		t.Errorf("short new password: %v", err)
	}
	if err := s.ChangeOwnPassword(ctx, u.ID, "temporary password", "my own password"); err != nil {
		t.Fatal(err)
	}
	if !verify("my own password") || reload(t, s, u).MustChangePassword {
		t.Error("change did not set the password or clear the must-change flag")
	}
}

func TestMessage(t *testing.T) {
	cases := map[error]string{
		ErrLastAdmin:                       "The last active admin can't be disabled or demoted.",
		&FieldError{"email", "Bad email."}: "Bad email.",
		errors.New("disk I/O error"):       "",
	}
	for err, want := range cases {
		if got := Message(err); got != want {
			t.Errorf("Message(%v) = %q, want %q", err, got, want)
		}
	}
}
