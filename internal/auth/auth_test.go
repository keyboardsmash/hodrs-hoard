package auth

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/keyboardsmash/hodrs-hoard/internal/db"
)

// TestMain runs before the tests in this package. Real argon2 parameters
// take ~100 ms per hash, so tests use cheap ones.
func TestMain(m *testing.M) {
	DefaultParams = Params{Memory: 64, Time: 1, Threads: 1, SaltLen: 16, KeyLen: 32}
	os.Exit(m.Run())
}

func TestHashAndVerify(t *testing.T) {
	h, err := HashPassword("correct horse battery")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(h, "$argon2id$v=19$m=64,t=1,p=1$") {
		t.Errorf("hash = %q, want PHC argon2id format", h)
	}

	ok, rehash, err := VerifyPassword("correct horse battery", h)
	if err != nil || !ok || rehash {
		t.Errorf("right password: ok=%v rehash=%v err=%v", ok, rehash, err)
	}
	ok, _, err = VerifyPassword("wrong horse battery", h)
	if err != nil || ok {
		t.Errorf("wrong password: ok=%v err=%v", ok, err)
	}

	// The same password hashes differently each time (random salt).
	h2, _ := HashPassword("correct horse battery")
	if h == h2 {
		t.Error("two hashes of the same password are equal; salt not random?")
	}
}

func TestVerifyNeedsRehash(t *testing.T) {
	old, err := hashWith("password123", Params{Memory: 32, Time: 1, Threads: 1, SaltLen: 16, KeyLen: 32})
	if err != nil {
		t.Fatal(err)
	}
	ok, rehash, err := VerifyPassword("password123", old)
	if err != nil || !ok || !rehash {
		t.Errorf("old params: ok=%v rehash=%v err=%v, want rehash", ok, rehash, err)
	}
}

func TestVerifyMalformed(t *testing.T) {
	good, _ := HashPassword("x")
	parts := strings.Split(good, "$")
	cases := []string{
		"",
		"plain",
		"5f4dcc3b5aa765d61d8327deb882cf99", // MD5, as PHP4DVD stored
		"$argon2i$v=19$m=64,t=1,p=1$" + parts[4] + "$" + parts[5],
		"$argon2id$v=18$m=64,t=1,p=1$" + parts[4] + "$" + parts[5],
		"$argon2id$v=19$m=0,t=1,p=1$" + parts[4] + "$" + parts[5],
		"$argon2id$v=19$m=64,t=1,p=1$!!!$" + parts[5],
		"$argon2id$v=19$m=64,t=1,p=1$" + parts[4] + "$",
		good + "$extra",
	}
	for _, h := range cases {
		if ok, _, err := VerifyPassword("x", h); err != ErrMalformedHash || ok {
			t.Errorf("VerifyPassword(%q) = %v, %v; want ErrMalformedHash", h, ok, err)
		}
	}
}

func TestSetupCode(t *testing.T) {
	code := NewSetupCode()
	if len(code) != 14 || code[4] != '-' || code[9] != '-' {
		t.Fatalf("code = %q, want XXXX-XXXX-XXXX", code)
	}
	if NewSetupCode() == code {
		t.Error("two setup codes are equal")
	}
	typed := strings.ToLower(strings.ReplaceAll(code, "-", " "))
	if !SetupCodeMatches(typed, code) {
		t.Errorf("%q should match %q", typed, code)
	}
	if SetupCodeMatches("AAAA-AAAA-AAAA", code) || SetupCodeMatches("", code) {
		t.Error("wrong code matched")
	}
	if SetupCodeMatches("", "") {
		t.Error("empty code matched when no code is set")
	}
}

// newSessions returns a session store on a fresh database with one user
// (ID 1), and a clock the test can move forward.
func newSessions(t *testing.T) (*Sessions, *time.Time) {
	t.Helper()
	d := db.OpenTestMigrated(t)
	_, err := d.Exec(`INSERT INTO users (id, username, username_key, password_hash, role, created_at)
		VALUES (1, 'alice', 'alice', 'x', 'admin', 0)`)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Unix(1_700_000_000, 0)
	s := NewSessions(d)
	// The closure reads `now` through its address, so the test can change
	// the time by assigning to *clock.
	s.Now = func() time.Time { return now }
	return s, &now
}

func TestSessionLifecycle(t *testing.T) {
	ctx := context.Background()
	s, _ := newSessions(t)

	token, err := s.Create(ctx, 1)
	if err != nil {
		t.Fatal(err)
	}
	if id, err := s.Lookup(ctx, token); err != nil || id != 1 {
		t.Fatalf("Lookup = %d, %v; want 1", id, err)
	}
	// The database must hold the hash, not the token.
	var stored string
	s.db.QueryRow(`SELECT token_hash FROM sessions`).Scan(&stored)
	if stored == token || stored != hashToken(token) {
		t.Errorf("stored %q; want the SHA-256 of the token", stored)
	}

	if _, err := s.Lookup(ctx, "not-a-token"); err != ErrNoSession {
		t.Errorf("unknown token: %v, want ErrNoSession", err)
	}
	if _, err := s.Lookup(ctx, ""); err != ErrNoSession {
		t.Errorf("empty token: %v, want ErrNoSession", err)
	}

	if err := s.Delete(ctx, token); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Lookup(ctx, token); err != ErrNoSession {
		t.Errorf("after Delete: %v, want ErrNoSession", err)
	}
}

func TestSessionIdleTimeout(t *testing.T) {
	ctx := context.Background()
	s, clock := newSessions(t)
	token, _ := s.Create(ctx, 1)

	// Activity just inside the idle timeout keeps the session alive...
	*clock = clock.Add(IdleTimeout - time.Second)
	if _, err := s.Lookup(ctx, token); err != nil {
		t.Fatalf("before idle timeout: %v", err)
	}
	// ...and moves the timeout forward.
	*clock = clock.Add(IdleTimeout - time.Second)
	if _, err := s.Lookup(ctx, token); err != nil {
		t.Fatalf("after activity: %v", err)
	}
	*clock = clock.Add(IdleTimeout)
	if _, err := s.Lookup(ctx, token); err != ErrNoSession {
		t.Fatalf("idle too long: %v, want ErrNoSession", err)
	}
}

func TestSessionMaxLifetime(t *testing.T) {
	ctx := context.Background()
	s, clock := newSessions(t)
	start := *clock
	token, _ := s.Create(ctx, 1)

	// Stay active, but pass the absolute limit.
	for clock.Sub(start) < MaxLifetime-IdleTimeout {
		*clock = clock.Add(IdleTimeout / 2)
		if _, err := s.Lookup(ctx, token); err != nil {
			t.Fatalf("at %v: %v", clock.Sub(start), err)
		}
	}
	*clock = start.Add(MaxLifetime)
	if _, err := s.Lookup(ctx, token); err != ErrNoSession {
		t.Fatalf("past max lifetime: %v, want ErrNoSession", err)
	}
}

func TestSessionDeleteForUserAndExpired(t *testing.T) {
	ctx := context.Background()
	s, clock := newSessions(t)
	a, _ := s.Create(ctx, 1)
	b, _ := s.Create(ctx, 1)

	if err := s.DeleteForUser(ctx, 1); err != nil {
		t.Fatal(err)
	}
	for _, tok := range []string{a, b} {
		if _, err := s.Lookup(ctx, tok); err != ErrNoSession {
			t.Errorf("after DeleteForUser: %v", err)
		}
	}

	s.Create(ctx, 1) // will be idle too long by the time we clean up
	*clock = clock.Add(IdleTimeout)
	fresh, _ := s.Create(ctx, 1)
	n, err := s.DeleteExpired(ctx)
	if err != nil || n != 1 {
		t.Errorf("DeleteExpired = %d, %v; want 1", n, err)
	}
	if _, err := s.Lookup(ctx, fresh); err != nil {
		t.Errorf("fresh session removed: %v", err)
	}
}
