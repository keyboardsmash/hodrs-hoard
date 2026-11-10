package server

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"github.com/keyboardsmash/hodrs-hoard/internal/auth"
	"github.com/keyboardsmash/hodrs-hoard/internal/user"
)

func cookieFrom(rec *httptest.ResponseRecorder, name string) *http.Cookie {
	var found *http.Cookie
	for _, c := range rec.Result().Cookies() {
		if c.Name == name {
			found = c // the last Set-Cookie for a name is the one browsers keep
		}
	}
	return found
}

// loginRemember logs in with "Remember me" ticked and returns both cookies.
func (a *testApp) loginRemember(username, password string) (session, remember *http.Cookie) {
	a.t.Helper()
	rec := a.do("POST", "/login", url.Values{"username": {username}, "password": {password}, "remember": {"on"}})
	if rec.Code != http.StatusSeeOther {
		a.t.Fatalf("login: %d", rec.Code)
	}
	session, remember = cookieFrom(rec, sessionCookie), cookieFrom(rec, rememberCookie)
	if session == nil || remember == nil {
		a.t.Fatalf("login with remember: session %v, remember %v", session, remember)
	}
	return session, remember
}

// wantRemembered checks that a request with only the remember cookie is
// logged in, and returns the new session cookie it got.
func (a *testApp) wantRemembered(remember *http.Cookie) *http.Cookie {
	a.t.Helper()
	rec := a.do("GET", "/account", nil, remember)
	if rec.Code != http.StatusOK {
		a.t.Fatalf("remembered request: %d to %q", rec.Code, rec.Header().Get("Location"))
	}
	s := cookieFrom(rec, sessionCookie)
	if s == nil || s.Value == "" {
		a.t.Fatal("remembered request got no new session cookie")
	}
	return s
}

func (a *testApp) wantForgotten(remember *http.Cookie) {
	a.t.Helper()
	rec := a.do("GET", "/account", nil, remember)
	if rec.Code != http.StatusSeeOther {
		a.t.Fatalf("forgotten token still logs in: %d", rec.Code)
	}
	if c := cookieFrom(rec, rememberCookie); c == nil || c.MaxAge >= 0 {
		a.t.Errorf("dead remember cookie not cleared: %+v", c)
	}
}

func TestRememberMe(t *testing.T) {
	a := newAppWithUser(t, "alice", user.RoleViewer)

	// Without the checkbox, no remember cookie.
	rec := a.do("POST", "/login", url.Values{"username": {"alice"}, "password": {testPassword}})
	if cookieFrom(rec, rememberCookie) != nil {
		t.Error("remember cookie set without the checkbox")
	}

	session, remember := a.loginRemember("alice", testPassword)
	if session.MaxAge != 0 {
		t.Errorf("session cookie MaxAge = %d, want 0 (browser session)", session.MaxAge)
	}
	if remember.MaxAge != int(auth.RememberLifetime/time.Second) || !remember.HttpOnly ||
		remember.SameSite != http.SameSiteLaxMode || remember.Path != "/" {
		t.Errorf("remember cookie: %+v", remember)
	}

	// The browser was closed: only the remember cookie is left.
	newSession := a.wantRemembered(remember)
	if newSession.Value == session.Value {
		t.Error("remembered login reused the old session token")
	}
	// The browser also gets the remember cookie again, so its expiry slides.
	rec = a.do("GET", "/", nil, remember)
	if c := cookieFrom(rec, rememberCookie); c == nil || c.Value != remember.Value || c.MaxAge <= 0 {
		t.Errorf("remember cookie not refreshed: %+v", c)
	}
	// The new session works on its own.
	wantStatus(t, a.do("GET", "/account", nil, newSession), 200, "")
}

func TestRememberAfterSessionExpired(t *testing.T) {
	a := newAppWithUser(t, "alice", user.RoleViewer)
	session, remember := a.loginRemember("alice", testPassword)

	// 13 hours idle: the session is gone, the remember token is not.
	later := time.Now().Add(13 * time.Hour)
	a.s.sessions.Now = func() time.Time { return later }
	a.s.remember.Now = a.s.sessions.Now

	rec := a.do("GET", "/account", nil, session, remember)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d", rec.Code)
	}
	// The response must leave a working session cookie, not a deletion.
	c := cookieFrom(rec, sessionCookie)
	if c == nil || c.Value == "" || c.MaxAge < 0 {
		t.Fatalf("session cookie after re-login: %+v", c)
	}
	wantStatus(t, a.do("GET", "/account", nil, c), 200, "")
}

func TestRememberSlidingAndExpiry(t *testing.T) {
	a := newAppWithUser(t, "alice", user.RoleViewer)
	_, remember := a.loginRemember("alice", testPassword)
	now := time.Now()
	a.s.remember.Now = func() time.Time { return now } // reads `now` when called

	for i := 0; i < 3; i++ {
		now = now.Add(29 * 24 * time.Hour)
		a.wantRemembered(remember)
	}
	now = now.Add(31 * 24 * time.Hour)
	a.wantForgotten(remember)
}

func TestRememberDisabledUser(t *testing.T) {
	a := newAppWithUser(t, "admin", user.RoleAdmin)
	bob := a.addUser("bob", user.RoleViewer)
	_, remember := a.loginRemember("bob", testPassword)

	a.db.Exec(`UPDATE users SET disabled_at = 1 WHERE id = ?`, bob.ID)
	a.wantForgotten(remember)
	var n int
	a.db.QueryRow(`SELECT count(*) FROM remember_tokens WHERE user_id = ?`, bob.ID).Scan(&n)
	if n != 0 {
		t.Errorf("%d remember tokens left for a disabled user", n)
	}
}

func TestRememberStillForcesPasswordChange(t *testing.T) {
	a := newAppWithUser(t, "alice", user.RoleViewer)
	_, remember := a.loginRemember("alice", testPassword)
	a.db.Exec(`UPDATE users SET must_change_password = 1`)
	wantRedirect(t, a.do("GET", "/", nil, remember), "/account/password")
}

func TestLogoutForgetsBrowser(t *testing.T) {
	a := newAppWithUser(t, "alice", user.RoleViewer)
	session, remember := a.loginRemember("alice", testPassword)

	rec := a.do("POST", "/logout", url.Values{}, session, remember)
	for _, name := range []string{sessionCookie, rememberCookie} {
		if c := cookieFrom(rec, name); c == nil || c.MaxAge >= 0 {
			t.Errorf("logout did not clear %s: %+v", name, c)
		}
	}
	a.wantForgotten(remember)
}

func TestLoginWithoutRememberDropsOldToken(t *testing.T) {
	a := newAppWithUser(t, "alice", user.RoleViewer)
	_, remember := a.loginRemember("alice", testPassword)

	// Someone logs in again on this browser without ticking the box.
	rec := a.do("POST", "/login", url.Values{"username": {"alice"}, "password": {testPassword}}, remember)
	if c := cookieFrom(rec, rememberCookie); c == nil || c.MaxAge >= 0 {
		t.Errorf("old remember cookie not cleared: %+v", c)
	}
	a.wantForgotten(remember)
}

func TestPasswordChangeKeepsOnlyThisBrowser(t *testing.T) {
	a := newAppWithUser(t, "alice", user.RoleViewer)
	_, rememberA := a.loginRemember("alice", testPassword)        // laptop
	sessionB, rememberB := a.loginRemember("alice", testPassword) // phone

	// The laptop was closed and reopened: this request logs in from the
	// remember cookie AND changes the password. The new session it gets
	// must survive "end the other sessions".
	form := url.Values{"current": {testPassword}, "password": {"another password"}, "password2": {"another password"}}
	rec := a.do("POST", "/account/password", form, rememberA)
	wantRedirect(t, rec, "/account?msg=password")
	wantStatus(t, a.do("GET", "/account", nil, cookieFrom(rec, sessionCookie)), 200, "")
	a.wantRemembered(rememberA)

	// The phone is logged out completely.
	wantRedirect(t, a.do("GET", "/account", nil, sessionB), "/login?next=%2Faccount")
	a.wantForgotten(rememberB)
}

func TestAdminActionsForgetRememberTokens(t *testing.T) {
	a := newAppWithUser(t, "admin", user.RoleAdmin)
	admin := a.login("admin", testPassword)
	bob := a.addUser("bob", user.RoleViewer)

	_, remember := a.loginRemember("bob", testPassword)
	form := url.Values{"password": {"reset password"}, "password2": {"reset password"}}
	wantRedirect(t, a.do("POST", userURL(bob.ID)+"/password", form, admin), userURL(bob.ID)+"?msg=password-reset")
	a.wantForgotten(remember)

	// Bob sets his own password, ticks "Remember me", then gets disabled.
	bobSession, _ := a.loginRemember("bob", "reset password")
	change := url.Values{"current": {"reset password"}, "password": {"bobs password!"}, "password2": {"bobs password!"}}
	a.do("POST", "/account/password", change, bobSession)
	_, remember = a.loginRemember("bob", "bobs password!")
	a.do("POST", userURL(bob.ID)+"/disable", url.Values{}, admin)
	a.wantForgotten(remember)
}

func TestLogoutEverywhere(t *testing.T) {
	a := newAppWithUser(t, "alice", user.RoleViewer)
	a.addUser("bob", user.RoleViewer)
	sessionA, rememberA := a.loginRemember("alice", testPassword)
	sessionB, rememberB := a.loginRemember("alice", testPassword)
	bobSession, bobRemember := a.loginRemember("bob", testPassword)

	wantStatus(t, a.do("GET", "/account", nil, sessionA), 200, "Log out everywhere")
	rec := a.do("POST", "/account/logout-everywhere", url.Values{}, sessionA, rememberA)
	wantRedirect(t, rec, "/login?msg=logged-out-everywhere")
	wantStatus(t, a.do("GET", "/login?msg=logged-out-everywhere", nil), 200, "logged out on all devices")

	for _, c := range []*http.Cookie{sessionA, sessionB} {
		wantRedirect(t, a.do("GET", "/account", nil, c), "/login?next=%2Faccount")
	}
	a.wantForgotten(rememberA)
	a.wantForgotten(rememberB)
	// Other users are not affected.
	wantStatus(t, a.do("GET", "/account", nil, bobSession), 200, "")
	a.wantRemembered(bobRemember)

	// Anonymous visitors can't use it.
	wantRedirect(t, a.do("POST", "/account/logout-everywhere", url.Values{}), "/login")
}
