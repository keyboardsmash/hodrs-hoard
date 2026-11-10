package server

import (
	"context"
	"database/sql"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"

	"github.com/keyboardsmash/hodrs-hoard/internal/auth"
	"github.com/keyboardsmash/hodrs-hoard/internal/db"
	"github.com/keyboardsmash/hodrs-hoard/internal/user"
)

func TestMain(m *testing.M) {
	// Cheap hashing keeps tests fast; see auth.DefaultParams.
	auth.DefaultParams = auth.Params{Memory: 64, Time: 1, Threads: 1, SaltLen: 16, KeyLen: 32}
	os.Exit(m.Run())
}

const testPassword = "long enough password"

// testApp bundles a server on a fresh database with its handler.
type testApp struct {
	t  *testing.T
	s  *Server
	db *sql.DB
	h  http.Handler
}

// newApp starts a server on an empty database (setup not done yet).
func newApp(t *testing.T) *testApp {
	t.Helper()
	d := db.OpenTestMigrated(t)
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	s, err := New(context.Background(), "test", log, d)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return &testApp{t: t, s: s, db: d, h: s.Handler()}
}

// newAppWithUser starts a server where setup is done and the given user
// exists. The server is created after the user, like a restart.
func newAppWithUser(t *testing.T, username string, role user.Role) *testApp {
	t.Helper()
	d := db.OpenTestMigrated(t)
	if _, err := user.NewStore(d).Create(context.Background(),
		user.New{Username: username, Password: testPassword, Role: role}); err != nil {
		t.Fatal(err)
	}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	s, err := New(context.Background(), "test", log, d)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return &testApp{t: t, s: s, db: d, h: s.Handler()}
}

// do sends a request through the handler in memory (no network port).
// A non-nil form makes it a form POST. Cookies are added to the request.
func (a *testApp) do(method, target string, form url.Values, cookies ...*http.Cookie) *httptest.ResponseRecorder {
	a.t.Helper()
	var body io.Reader
	if form != nil {
		body = strings.NewReader(form.Encode())
	}
	req := httptest.NewRequest(method, target, body)
	if form != nil {
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	for _, c := range cookies {
		req.AddCookie(c)
	}
	rec := httptest.NewRecorder()
	a.h.ServeHTTP(rec, req)
	return rec
}

// login posts the login form and returns the session cookie.
func (a *testApp) login(username, password string) *http.Cookie {
	a.t.Helper()
	rec := a.do("POST", "/login", url.Values{"username": {username}, "password": {password}})
	if rec.Code != http.StatusSeeOther {
		a.t.Fatalf("login: status %d, body %s", rec.Code, rec.Body)
	}
	c := sessionCookieFrom(rec)
	if c == nil {
		a.t.Fatal("login: no session cookie set")
	}
	return c
}

func sessionCookieFrom(rec *httptest.ResponseRecorder) *http.Cookie {
	for _, c := range rec.Result().Cookies() {
		if c.Name == sessionCookie {
			return c
		}
	}
	return nil
}

func wantRedirect(t *testing.T, rec *httptest.ResponseRecorder, location string) {
	t.Helper()
	if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != location {
		t.Errorf("got %d to %q, want 303 to %q", rec.Code, rec.Header().Get("Location"), location)
	}
}

func TestHealth(t *testing.T) {
	a := newApp(t)
	rec := a.do("GET", "/healthz", nil)
	// /healthz works before setup and without login.
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("Content-Type = %q", ct)
	}
	var body health
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body.Status != "ok" || body.Version != "test" || body.Database != "ok" {
		t.Errorf("body = %+v", body)
	}
}

func TestHealthDatabaseDown(t *testing.T) {
	a := newApp(t)
	a.db.Close() // every query now fails with "database is closed"
	rec := a.do("GET", "/healthz", nil)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", rec.Code)
	}
	var body health
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body.Status != "error" || body.Database != "unreachable" {
		t.Errorf("body = %+v", body)
	}
}

func TestSetupCodeOnlyWithoutUsers(t *testing.T) {
	if newApp(t).s.setupCode == "" {
		t.Error("empty database: no setup code generated")
	}
	if code := newAppWithUser(t, "alice", user.RoleAdmin).s.setupCode; code != "" {
		t.Errorf("users exist: setup code %q generated", code)
	}
}

func TestBeforeSetupEverythingRedirects(t *testing.T) {
	a := newApp(t)
	for _, path := range []string{"/", "/login", "/nope"} {
		wantRedirect(t, a.do("GET", path, nil), "/setup")
	}
	wantRedirect(t, a.do("POST", "/login", url.Values{}), "/setup")
	if rec := a.do("GET", "/setup", nil); rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "Setup code") {
		t.Errorf("GET /setup = %d", rec.Code)
	}
}

func TestSetupFlow(t *testing.T) {
	a := newApp(t)
	form := func(code, pw2 string) url.Values {
		return url.Values{"code": {code}, "username": {"Admin"}, "email": {"admin@example.com"},
			"password": {testPassword}, "password2": {pw2}}
	}
	countUsers := func() int {
		n, _ := a.s.users.Count(context.Background())
		return n
	}

	rec := a.do("POST", "/setup", form("WRNG-CODE-XXXX", testPassword))
	if rec.Code != http.StatusUnprocessableEntity || !strings.Contains(rec.Body.String(), "setup code is wrong") {
		t.Errorf("wrong code: %d", rec.Code)
	}
	rec = a.do("POST", "/setup", form(a.s.setupCode, "something else"))
	if rec.Code != http.StatusUnprocessableEntity || !strings.Contains(rec.Body.String(), "don&#39;t match") {
		t.Errorf("mismatched passwords: %d %s", rec.Code, rec.Body)
	}
	bad := form(a.s.setupCode, "short")
	bad.Set("password", "short")
	if rec := a.do("POST", "/setup", bad); rec.Code != http.StatusUnprocessableEntity {
		t.Errorf("short password: %d", rec.Code)
	}
	if n := countUsers(); n != 0 {
		t.Fatalf("failed setups created %d users", n)
	}

	// A lowercase code with spaces is accepted.
	typed := strings.ToLower(strings.ReplaceAll(a.s.setupCode, "-", " "))
	rec = a.do("POST", "/setup", form(typed, testPassword))
	wantRedirect(t, rec, "/")
	cookie := sessionCookieFrom(rec)
	if cookie == nil {
		t.Fatal("setup did not log the admin in")
	}

	// The new admin is logged in.
	rec = a.do("GET", "/", nil, cookie)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "Admin (admin)") {
		t.Errorf("GET / after setup: %d %s", rec.Code, rec.Body)
	}

	// Setup is closed now, even with the right code.
	wantRedirect(t, a.do("GET", "/setup", nil), "/login")
	f := form(a.s.setupCode, testPassword)
	f.Set("username", "intruder")
	wantRedirect(t, a.do("POST", "/setup", f), "/login")
	if n := countUsers(); n != 1 {
		t.Errorf("users = %d, want 1", n)
	}
}

func TestLoginLogout(t *testing.T) {
	a := newAppWithUser(t, "Höðr", user.RoleEditor)

	// Not logged in: / redirects to the login page.
	wantRedirect(t, a.do("GET", "/", nil), "/login")
	if rec := a.do("GET", "/login", nil); rec.Code != http.StatusOK {
		t.Errorf("GET /login = %d", rec.Code)
	}

	// Wrong password and unknown user give the same answer.
	for _, creds := range [][2]string{{"Höðr", "wrong password!"}, {"nobody", testPassword}} {
		rec := a.do("POST", "/login", url.Values{"username": {creds[0]}, "password": {creds[1]}})
		if rec.Code != http.StatusUnauthorized || !strings.Contains(rec.Body.String(), "Wrong username or password.") {
			t.Errorf("login %v: %d", creds, rec.Code)
		}
		if sessionCookieFrom(rec) != nil {
			t.Errorf("login %v: session cookie set", creds)
		}
	}

	// Usernames are case-insensitive.
	cookie := a.login("HÖÐR", testPassword)
	if !cookie.HttpOnly || cookie.SameSite != http.SameSiteLaxMode || cookie.Secure {
		t.Errorf("cookie flags: %+v", cookie)
	}
	rec := a.do("GET", "/", nil, cookie)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "Höðr (editor)") {
		t.Errorf("GET / logged in: %d", rec.Code)
	}
	if cc := rec.Header().Get("Cache-Control"); cc != "no-store" {
		t.Errorf("Cache-Control = %q", cc)
	}
	// Logged in: the login page sends you on.
	wantRedirect(t, a.do("GET", "/login", nil, cookie), "/")

	wantRedirect(t, a.do("POST", "/logout", url.Values{}, cookie), "/login")
	// The old cookie no longer works, even if a browser kept it.
	wantRedirect(t, a.do("GET", "/", nil, cookie), "/login")
}

func TestLoginReplacesExistingSession(t *testing.T) {
	a := newAppWithUser(t, "alice", user.RoleAdmin)
	first := a.login("alice", testPassword)

	rec := a.do("POST", "/login", url.Values{"username": {"alice"}, "password": {testPassword}}, first)
	second := sessionCookieFrom(rec)
	if second == nil || second.Value == first.Value {
		t.Fatal("login did not issue a new token")
	}
	wantRedirect(t, a.do("GET", "/", nil, first), "/login")
	if rec := a.do("GET", "/", nil, second); rec.Code != http.StatusOK {
		t.Errorf("new session: %d", rec.Code)
	}
}

func TestDisabledUser(t *testing.T) {
	a := newAppWithUser(t, "alice", user.RoleAdmin)
	cookie := a.login("alice", testPassword)

	if _, err := a.db.Exec(`UPDATE users SET disabled_at = 1 WHERE username_key = 'alice'`); err != nil {
		t.Fatal(err)
	}
	// The existing session stops working...
	wantRedirect(t, a.do("GET", "/", nil, cookie), "/login")
	// ...and logging in fails with the usual message.
	rec := a.do("POST", "/login", url.Values{"username": {"alice"}, "password": {testPassword}})
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("disabled login: %d", rec.Code)
	}
}

func TestLoginNext(t *testing.T) {
	a := newAppWithUser(t, "alice", user.RoleAdmin)

	wantRedirect(t, a.do("GET", "/?view=grid", nil), "/login?next=%2F%3Fview%3Dgrid")

	rec := a.do("POST", "/login", url.Values{"username": {"alice"}, "password": {testPassword}, "next": {"/?view=grid"}})
	wantRedirect(t, rec, "/?view=grid")

	rec = a.do("POST", "/login", url.Values{"username": {"alice"}, "password": {testPassword}, "next": {"https://evil.example/"}})
	wantRedirect(t, rec, "/")
}

func TestSafeNext(t *testing.T) {
	cases := map[string]string{
		"":                      "/",
		"/":                     "/",
		"/items?page=2":         "/items?page=2",
		"items":                 "/",
		"//evil.example":        "/",
		"/\\evil.example":       "/",
		"https://evil.example/": "/",
		"javascript:alert(1)":   "/",
	}
	for in, want := range cases {
		if got := safeNext(in); got != want {
			t.Errorf("safeNext(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestSecureCookieBehindHTTPSProxy(t *testing.T) {
	a := newAppWithUser(t, "alice", user.RoleAdmin)
	req := httptest.NewRequest("POST", "/login",
		strings.NewReader(url.Values{"username": {"alice"}, "password": {testPassword}}.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("X-Forwarded-Proto", "https")
	rec := httptest.NewRecorder()
	a.h.ServeHTTP(rec, req)
	if c := sessionCookieFrom(rec); c == nil || !c.Secure {
		t.Errorf("cookie over HTTPS proxy: %+v, want Secure", c)
	}
}

func TestCrossOriginPostBlocked(t *testing.T) {
	a := newAppWithUser(t, "alice", user.RoleAdmin)
	cookie := a.login("alice", testPassword)
	form := url.Values{"username": {"alice"}, "password": {testPassword}}.Encode()

	cases := []struct {
		name    string
		headers map[string]string
		want    int
	}{
		{"cross-site fetch", map[string]string{"Sec-Fetch-Site": "cross-site"}, http.StatusForbidden},
		{"foreign origin", map[string]string{"Origin": "https://evil.example"}, http.StatusForbidden},
		{"same origin", map[string]string{"Sec-Fetch-Site": "same-origin"}, http.StatusSeeOther},
		{"matching origin", map[string]string{"Origin": "http://example.com"}, http.StatusSeeOther},
	}
	for _, tc := range cases {
		for _, path := range []string{"/login", "/logout"} {
			// httptest requests have Host example.com.
			req := httptest.NewRequest("POST", path, strings.NewReader(form))
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			for k, v := range tc.headers {
				req.Header.Set(k, v)
			}
			if path == "/logout" {
				// Use a throwaway session so the shared one survives.
				req.AddCookie(a.login("alice", testPassword))
			}
			rec := httptest.NewRecorder()
			a.h.ServeHTTP(rec, req)
			if rec.Code != tc.want {
				t.Errorf("%s POST %s = %d, want %d", tc.name, path, rec.Code, tc.want)
			}
		}
	}
	// The blocked cross-site logout did not end the session.
	if rec := a.do("GET", "/", nil, cookie); rec.Code != http.StatusOK {
		t.Errorf("session ended by blocked request: %d", rec.Code)
	}
}

func TestRouting(t *testing.T) {
	a := newAppWithUser(t, "alice", user.RoleAdmin)
	cookie := a.login("alice", testPassword)
	cases := []struct {
		method, path string
		want         int
	}{
		{http.MethodGet, "/nope", http.StatusNotFound},
		{http.MethodPost, "/healthz", http.StatusMethodNotAllowed},
		{http.MethodGet, "/logout", http.StatusMethodNotAllowed}, // logout must be a POST
	}
	for _, tc := range cases {
		if rec := a.do(tc.method, tc.path, nil, cookie); rec.Code != tc.want {
			t.Errorf("%s %s = %d, want %d", tc.method, tc.path, rec.Code, tc.want)
		}
	}
}

func TestSecurityHeaders(t *testing.T) {
	rec := newApp(t).do("GET", "/setup", nil)
	for k, want := range map[string]string{
		"X-Frame-Options":        "DENY",
		"X-Content-Type-Options": "nosniff",
	} {
		if got := rec.Header().Get(k); got != want {
			t.Errorf("%s = %q, want %q", k, got, want)
		}
	}
}
