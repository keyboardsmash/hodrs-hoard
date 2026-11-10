package user

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/keyboardsmash/hodrs-hoard/internal/auth"
	"github.com/keyboardsmash/hodrs-hoard/internal/db"
)

func TestMain(m *testing.M) {
	// Cheap hashing keeps tests fast; see auth.DefaultParams.
	auth.DefaultParams = auth.Params{Memory: 64, Time: 1, Threads: 1, SaltLen: 16, KeyLen: 32}
	os.Exit(m.Run())
}

func newStore(t *testing.T) *Store {
	t.Helper()
	return NewStore(db.OpenTestMigrated(t))
}

const goodPassword = "long enough password"

// fieldOf returns the field of a *FieldError, or "" for other errors.
func fieldOf(err error) string {
	var fe *FieldError
	if errors.As(err, &fe) {
		return fe.Field
	}
	return ""
}

func TestValidateUsername(t *testing.T) {
	good := []string{"alice", "Höðr", "bob.smith", "a_b-c", "me@home", "  padded  ", strings.Repeat("x", 64)}
	for _, u := range good {
		if err := ValidateUsername(u); err != nil {
			t.Errorf("ValidateUsername(%q) = %v, want ok", u, err)
		}
	}
	bad := []string{"", "   ", "has space", "semi;colon", "tab\tname", "<script>", strings.Repeat("x", 65)}
	for _, u := range bad {
		if err := ValidateUsername(u); fieldOf(err) != "username" {
			t.Errorf("ValidateUsername(%q) = %v, want username error", u, err)
		}
	}
}

func TestValidatePassword(t *testing.T) {
	if err := ValidatePassword("short"); fieldOf(err) != "password" {
		t.Errorf("short password: %v", err)
	}
	// Ten characters, but more than ten bytes: length counts characters.
	if err := ValidatePassword("ööööööööö"); fieldOf(err) != "password" {
		t.Errorf("9 characters: %v", err)
	}
	if err := ValidatePassword("öööööööööö"); err != nil {
		t.Errorf("10 characters: %v", err)
	}
	if err := ValidatePassword(strings.Repeat("x", MaxPasswordBytes+1)); fieldOf(err) != "password" {
		t.Errorf("too long: %v", err)
	}
}

func TestNormaliseEmail(t *testing.T) {
	cases := map[string]string{
		"":                  "",
		"  ":                "",
		" a@example.com ":   "a@example.com",
		"Mixed@Example.COM": "Mixed@Example.COM",
	}
	for in, want := range cases {
		if got, err := NormaliseEmail(in); err != nil || got != want {
			t.Errorf("NormaliseEmail(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	for _, in := range []string{"nope", "Alice <a@example.com>", "a@", "@b.com", "a b@c.com"} {
		if _, err := NormaliseEmail(in); fieldOf(err) != "email" {
			t.Errorf("NormaliseEmail(%q) = %v, want email error", in, err)
		}
	}
}

func TestCreateAndLookup(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)

	u, err := s.Create(ctx, New{Username: " Höðr ", Email: "hodr@example.com", Password: goodPassword, Role: RoleEditor})
	if err != nil {
		t.Fatal(err)
	}
	if u.ID == 0 || u.Username != "Höðr" {
		t.Errorf("created %+v", u)
	}

	// Lookups are case-insensitive, including non-ASCII letters.
	for _, name := range []string{"Höðr", "HÖÐR", "höðr", " hÖðR "} {
		got, err := s.ByUsername(ctx, name)
		if err != nil {
			t.Fatalf("ByUsername(%q): %v", name, err)
		}
		if got.ID != u.ID || got.Username != "Höðr" || got.Email != "hodr@example.com" ||
			got.Role != RoleEditor || got.Disabled() || !got.LastLoginAt.IsZero() {
			t.Errorf("ByUsername(%q) = %+v", name, got)
		}
		// The password is stored hashed, and the hash verifies.
		if ok, _, err := auth.VerifyPassword(goodPassword, got.PasswordHash); !ok || err != nil {
			t.Errorf("stored hash does not verify: %v", err)
		}
	}

	if _, err := s.ByUsername(ctx, "nobody"); !errors.Is(err, ErrNotFound) {
		t.Errorf("unknown user: %v", err)
	}
	if _, err := s.ByID(ctx, 999); !errors.Is(err, ErrNotFound) {
		t.Errorf("unknown ID: %v", err)
	}

	if err := s.TouchLogin(ctx, u.ID); err != nil {
		t.Fatal(err)
	}
	got, _ := s.ByID(ctx, u.ID)
	if got.LastLoginAt.IsZero() {
		t.Error("TouchLogin did not set LastLoginAt")
	}
}

func TestCreateDuplicates(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)
	mustCreate := func(n New) {
		t.Helper()
		if _, err := s.Create(ctx, n); err != nil {
			t.Fatalf("Create(%q): %v", n.Username, err)
		}
	}
	mustCreate(New{Username: "Höðr", Email: "Hodr@Example.com", Password: goodPassword, Role: RoleViewer})

	if _, err := s.Create(ctx, New{Username: "HÖÐR", Password: goodPassword, Role: RoleViewer}); fieldOf(err) != "username" {
		t.Errorf("duplicate username: %v", err)
	}
	if _, err := s.Create(ctx, New{Username: "other", Email: "hodr@example.COM", Password: goodPassword, Role: RoleViewer}); fieldOf(err) != "email" {
		t.Errorf("duplicate email: %v", err)
	}
	// Email is optional: several users without one are fine.
	mustCreate(New{Username: "a", Password: goodPassword, Role: RoleViewer})
	mustCreate(New{Username: "b", Email: "  ", Password: goodPassword, Role: RoleViewer})

	if n, _ := s.Count(ctx); n != 3 {
		t.Errorf("Count = %d, want 3", n)
	}
}

func TestCreateValidates(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)
	cases := []struct {
		n     New
		field string
	}{
		{New{Username: "", Password: goodPassword, Role: RoleViewer}, "username"},
		{New{Username: "x", Email: "bad", Password: goodPassword, Role: RoleViewer}, "email"},
		{New{Username: "x", Password: "short", Role: RoleViewer}, "password"},
		{New{Username: "x", Password: goodPassword, Role: "root"}, "role"},
	}
	for _, tc := range cases {
		if _, err := s.Create(ctx, tc.n); fieldOf(err) != tc.field {
			t.Errorf("Create(%+v) = %v, want %s error", tc.n, err, tc.field)
		}
	}
	if n, _ := s.Count(ctx); n != 0 {
		t.Errorf("invalid input created %d users", n)
	}
}

func TestCreateFirstAdmin(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)

	u, err := s.CreateFirstAdmin(ctx, New{Username: "admin", Password: goodPassword, Role: RoleViewer})
	if err != nil {
		t.Fatal(err)
	}
	if u.Role != RoleAdmin {
		t.Errorf("role = %s, want admin whatever was passed", u.Role)
	}
	if _, err := s.CreateFirstAdmin(ctx, New{Username: "second", Password: goodPassword}); !errors.Is(err, ErrSetupDone) {
		t.Errorf("second CreateFirstAdmin: %v, want ErrSetupDone", err)
	}
	if n, _ := s.Count(ctx); n != 1 {
		t.Errorf("Count = %d, want 1", n)
	}
}
