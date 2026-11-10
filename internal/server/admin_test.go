package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/keyboardsmash/hodrs-hoard/internal/user"
)

// addUser creates a user directly in the store and returns it.
func (a *testApp) addUser(name string, role user.Role) *user.User {
	a.t.Helper()
	u, err := a.s.users.Create(context.Background(), user.New{Username: name, Password: testPassword, Role: role})
	if err != nil {
		a.t.Fatal(err)
	}
	return u
}

func (a *testApp) reload(u *user.User) *user.User {
	a.t.Helper()
	got, err := a.s.users.ByID(context.Background(), u.ID)
	if err != nil {
		a.t.Fatal(err)
	}
	return got
}

func wantStatus(t *testing.T, rec *httptest.ResponseRecorder, code int, contains string) {
	t.Helper()
	if rec.Code != code {
		t.Errorf("status = %d, want %d; body: %s", rec.Code, code, rec.Body)
		return
	}
	if contains != "" && !strings.Contains(rec.Body.String(), contains) {
		t.Errorf("body does not contain %q:\n%s", contains, rec.Body)
	}
}

// TestEveryPageRenders executes each page template with realistic data, so
// a template referring to a missing snippet or field fails here, not in
// front of a user.
func TestEveryPageRenders(t *testing.T) {
	a := newAppWithUser(t, "alice", user.RoleAdmin)
	me, _ := a.s.users.ByUsername(context.Background(), "alice")
	other := a.addUser("bob", user.RoleViewer)
	for _, page := range pages {
		for _, target := range []*user.User{me, other} {
			req := httptest.NewRequest("GET", "/", nil)
			req = req.WithContext(context.WithValue(req.Context(), userKey, me))
			rec := httptest.NewRecorder()
			a.s.render(rec, req, http.StatusOK, page, pageData{
				Title: "x", Target: target, Users: []*user.User{me, other},
				Form: formValues{Role: user.RoleEditor},
			})
			if rec.Code != http.StatusOK {
				t.Errorf("%s (target %s): status %d", page, target.Username, rec.Code)
			}
		}
	}
}

func TestAccessByRole(t *testing.T) {
	a := newAppWithUser(t, "admin", user.RoleAdmin)
	a.addUser("editor", user.RoleEditor)
	a.addUser("viewer", user.RoleViewer)
	cookies := map[string]*http.Cookie{
		"admin":  a.login("admin", testPassword),
		"editor": a.login("editor", testPassword),
		"viewer": a.login("viewer", testPassword),
	}
	// Expected status per role for GET requests.
	cases := []struct {
		path                  string
		admin, editor, viewer int
	}{
		{"/", 200, 200, 200},
		{"/account", 200, 200, 200},
		{"/account/password", 200, 200, 200},
		{"/admin/users", 200, 403, 403},
		{"/admin/users/new", 200, 403, 403},
		{"/admin/users/1", 200, 403, 403},
		{"/admin/settings", 200, 403, 403},
	}
	for _, tc := range cases {
		for role, want := range map[string]int{"admin": tc.admin, "editor": tc.editor, "viewer": tc.viewer} {
			if rec := a.do("GET", tc.path, nil, cookies[role]); rec.Code != want {
				t.Errorf("%s GET %s = %d, want %d", role, tc.path, rec.Code, want)
			}
		}
		// Anonymous visitors are sent to log in (anonymous browsing is off).
		if rec := a.do("GET", tc.path, nil); rec.Code != http.StatusSeeOther ||
			!strings.HasPrefix(rec.Header().Get("Location"), "/login") {
			t.Errorf("anonymous GET %s = %d to %q", tc.path, rec.Code, rec.Header().Get("Location"))
		}
	}

	// POSTs are checked too: a viewer can't create users by posting the form.
	form := url.Values{"username": {"sneaky"}, "password": {testPassword}, "password2": {testPassword}, "role": {"admin"}}
	wantStatus(t, a.do("POST", "/admin/users/new", form, cookies["viewer"]), http.StatusForbidden, "")
	wantStatus(t, a.do("POST", "/admin/settings", url.Values{"anonymous_browsing": {"on"}}, cookies["editor"]), http.StatusForbidden, "")
	if _, err := a.s.users.ByUsername(context.Background(), "sneaky"); err == nil {
		t.Error("viewer created a user")
	}
	if a.s.anonymous.Load() {
		t.Error("editor changed a setting")
	}
}

func TestAnonymousBrowsing(t *testing.T) {
	a := newAppWithUser(t, "admin", user.RoleAdmin)
	admin := a.login("admin", testPassword)

	wantRedirect(t, a.do("GET", "/", nil), "/login")

	wantRedirect(t, a.do("POST", "/admin/settings", url.Values{"anonymous_browsing": {"on"}}, admin), "/admin/settings?msg=saved")
	wantStatus(t, a.do("GET", "/admin/settings", nil, admin), 200, "checked")

	// Anonymous visitors may now browse, and see a login link...
	wantStatus(t, a.do("GET", "/", nil), 200, `<a href="/login">Log in</a>`)
	// ...but account and admin pages still need a login.
	wantRedirect(t, a.do("GET", "/account", nil), "/login?next=%2Faccount")
	wantRedirect(t, a.do("GET", "/admin/users", nil), "/login?next=%2Fadmin%2Fusers")
	wantRedirect(t, a.do("POST", "/admin/settings", url.Values{}), "/login")

	// The setting survives a restart.
	s2, err := New(context.Background(), "test", a.s.log, a.db)
	if err != nil {
		t.Fatal(err)
	}
	if !s2.anonymous.Load() {
		t.Error("setting lost after restart")
	}

	// An unticked checkbox isn't sent at all, which means "off".
	a.do("POST", "/admin/settings", url.Values{}, admin)
	wantRedirect(t, a.do("GET", "/", nil), "/login")
}

func TestCreateUserAndForcedPasswordChange(t *testing.T) {
	a := newAppWithUser(t, "admin", user.RoleAdmin)
	admin := a.login("admin", testPassword)

	form := url.Values{"username": {"Carol"}, "email": {"carol@example.com"}, "role": {"editor"},
		"password": {"temporary pass"}, "password2": {"temporary pass"}, "must_change": {"on"}}

	bad := url.Values{}
	for k, v := range form {
		bad[k] = v
	}
	bad.Set("password2", "something else")
	wantStatus(t, a.do("POST", "/admin/users/new", bad, admin), 422, "don&#39;t match")

	rec := a.do("POST", "/admin/users/new", form, admin)
	if rec.Code != http.StatusSeeOther || !strings.HasSuffix(rec.Header().Get("Location"), "?msg=created") {
		t.Fatalf("create: %d %q", rec.Code, rec.Header().Get("Location"))
	}
	wantStatus(t, a.do("GET", rec.Header().Get("Location"), nil, admin), 200, "User created.")
	wantStatus(t, a.do("POST", "/admin/users/new", form, admin), 422, "already taken")
	wantStatus(t, a.do("GET", "/admin/users", nil, admin), 200, "must change password")

	// Carol logs in and is held on the password page.
	carol := a.login("carol", "temporary pass")
	for _, path := range []string{"/", "/account", "/admin/users"} {
		wantRedirect(t, a.do("GET", path, nil, carol), "/account/password")
	}
	wantRedirect(t, a.do("POST", "/account/email", url.Values{"email": {"x@example.com"}}, carol), "/account/password")
	wantStatus(t, a.do("GET", "/account/password", nil, carol), 200, "Choose a new password")

	change := func(current, pw, pw2 string) *httptest.ResponseRecorder {
		return a.do("POST", "/account/password", url.Values{"current": {current}, "password": {pw}, "password2": {pw2}}, carol)
	}
	wantStatus(t, change("wrong", "carols own password", "carols own password"), 422, "current password is wrong")
	wantStatus(t, change("temporary pass", "carols own password", "typo"), 422, "don&#39;t match")
	wantStatus(t, change("temporary pass", "temporary pass", "temporary pass"), 422, "must differ")
	wantRedirect(t, change("temporary pass", "carols own password", "carols own password"), "/account?msg=password")

	// Free to go now.
	wantStatus(t, a.do("GET", "/", nil, carol), 200, ">Carol</a> (editor)")
	a.login("carol", "carols own password")
}

func TestAdminEditUser(t *testing.T) {
	a := newAppWithUser(t, "admin", user.RoleAdmin)
	adminCookie := a.login("admin", testPassword)
	me, _ := a.s.users.ByUsername(context.Background(), "admin")
	bob := a.addUser("bob", user.RoleViewer)
	bobURL := userURL(bob.ID)

	wantRedirect(t, a.do("POST", bobURL+"/profile", url.Values{"email": {"bob@example.com"}, "role": {"editor"}}, adminCookie), bobURL+"?msg=saved")
	if got := a.reload(bob); got.Role != user.RoleEditor || got.Email != "bob@example.com" {
		t.Errorf("after save: %+v", got)
	}
	// Bob's next request sees the new role at once.
	bobCookie := a.login("bob", testPassword)
	wantStatus(t, a.do("GET", "/", nil, bobCookie), 200, "(editor)")

	// Own role: no menu on the form, and a crafted POST can't change it.
	wantStatus(t, a.do("GET", userURL(me.ID), nil, adminCookie), 200, "can't change your own role")
	a.do("POST", userURL(me.ID)+"/profile", url.Values{"role": {"viewer"}}, adminCookie)
	if a.reload(me).Role != user.RoleAdmin {
		t.Error("admin changed their own role")
	}

	// Errors from the store are shown on the page.
	a.s.users.UpdateEmail(context.Background(), me.ID, "admin@example.com")
	wantStatus(t, a.do("POST", bobURL+"/profile", url.Values{"email": {"admin@example.com"}, "role": {"editor"}}, adminCookie), 422, "already has that email")
	wantStatus(t, a.do("POST", userURL(me.ID)+"/disable", url.Values{}, adminCookie), 422, "can&#39;t disable your own account")

	for _, path := range []string{"/admin/users/999", "/admin/users/abc"} {
		wantStatus(t, a.do("GET", path, nil, adminCookie), 404, "")
	}
}

func TestAdminResetPassword(t *testing.T) {
	a := newAppWithUser(t, "admin", user.RoleAdmin)
	adminCookie := a.login("admin", testPassword)
	bob := a.addUser("bob", user.RoleViewer)
	bobCookie := a.login("bob", testPassword)

	form := url.Values{"password": {"reset password"}, "password2": {"reset password"}}
	wantRedirect(t, a.do("POST", userURL(bob.ID)+"/password", form, adminCookie), userURL(bob.ID)+"?msg=password-reset")

	// Bob is logged out, and must change the new password.
	wantRedirect(t, a.do("GET", "/", nil, bobCookie), "/login")
	bobCookie = a.login("bob", "reset password")
	wantRedirect(t, a.do("GET", "/", nil, bobCookie), "/account/password")

	// Admins reset their own password via the account page instead.
	me, _ := a.s.users.ByUsername(context.Background(), "admin")
	wantRedirect(t, a.do("POST", userURL(me.ID)+"/password", form, adminCookie), "/account/password")
	if a.reload(me).MustChangePassword {
		t.Error("admin reset their own password via the admin form")
	}
}

func TestAdminDisableEnable(t *testing.T) {
	a := newAppWithUser(t, "admin", user.RoleAdmin)
	adminCookie := a.login("admin", testPassword)
	bob := a.addUser("bob", user.RoleEditor)
	bobCookie := a.login("bob", testPassword)

	wantRedirect(t, a.do("POST", userURL(bob.ID)+"/disable", url.Values{}, adminCookie), userURL(bob.ID)+"?msg=disabled")
	var n int
	a.db.QueryRow(`SELECT count(*) FROM sessions WHERE user_id = ?`, bob.ID).Scan(&n)
	if n != 0 {
		t.Errorf("%d sessions left after disabling", n)
	}
	wantRedirect(t, a.do("GET", "/", nil, bobCookie), "/login")
	wantStatus(t, a.do("POST", "/login", url.Values{"username": {"bob"}, "password": {testPassword}}), 401, "")
	wantStatus(t, a.do("GET", "/admin/users", nil, adminCookie), 200, `class="disabled"`)

	wantRedirect(t, a.do("POST", userURL(bob.ID)+"/enable", url.Values{}, adminCookie), userURL(bob.ID)+"?msg=enabled")
	a.login("bob", testPassword)
}

func TestOwnAccount(t *testing.T) {
	a := newAppWithUser(t, "alice", user.RoleViewer)
	cookie := a.login("alice", testPassword)
	other := a.login("alice", testPassword) // a second browser

	wantRedirect(t, a.do("POST", "/account/email", url.Values{"email": {"alice@example.com"}}, cookie), "/account?msg=saved")
	wantStatus(t, a.do("GET", "/account?msg=saved", nil, cookie), 200, `value="alice@example.com"`)
	wantStatus(t, a.do("POST", "/account/email", url.Values{"email": {"nope"}}, cookie), 422, "not valid")

	form := url.Values{"current": {testPassword}, "password": {"brand new password"}, "password2": {"brand new password"}}
	wantRedirect(t, a.do("POST", "/account/password", form, cookie), "/account?msg=password")
	// This session survives; the other browser is logged out.
	wantStatus(t, a.do("GET", "/account", nil, cookie), 200, "")
	wantRedirect(t, a.do("GET", "/", nil, other), "/login")
}

func TestMessages(t *testing.T) {
	a := newAppWithUser(t, "alice", user.RoleViewer)
	cookie := a.login("alice", testPassword)
	wantStatus(t, a.do("GET", "/account?msg=saved", nil, cookie), 200, `<p class="message">Changes saved.</p>`)
	rec := a.do("GET", "/account?msg="+url.QueryEscape("<b>Your account is hacked</b>"), nil, cookie)
	if strings.Contains(rec.Body.String(), "hacked") || strings.Contains(rec.Body.String(), `class="message"`) {
		t.Error("unknown message key was shown")
	}
}
