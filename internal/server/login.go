package server

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strings"

	"github.com/keyboardsmash/hodrs-hoard/internal/auth"
	"github.com/keyboardsmash/hodrs-hoard/internal/user"
)

// sessionCookie is the cookie that holds the session token.
const sessionCookie = "hoard_session"

// maxFormBytes caps login and setup form posts.
const maxFormBytes = 64 << 10 // 64 KiB; `<<` is a bit shift, 64 * 1024

// ctxKey is a private type for context keys. Because no other package can
// name this type, nothing else can read or overwrite our values by accident.
type ctxKey int

const userKey ctxKey = 0

// currentUser returns the logged-in user, or nil.
func currentUser(ctx context.Context) *user.User {
	// `.(*user.User)` is a type assertion: it gets the concrete type back out
	// of the `any` the context stores. With `u, _ :=` a missing value gives
	// nil instead of a panic.
	u, _ := ctx.Value(userKey).(*user.User)
	return u
}

// isPublic lists paths that work without login and before setup.
func isPublic(path string) bool {
	return path == "/healthz"
}

// loadUser reads the session cookie and, if it's valid, stores the user in
// the request context for handlers and templates.
func (s *Server) loadUser(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := r.Cookie(sessionCookie)
		if err != nil || isPublic(r.URL.Path) {
			next.ServeHTTP(w, r) // no cookie: anonymous
			return
		}
		u, err := s.userForToken(r.Context(), c.Value)
		switch {
		case errors.Is(err, auth.ErrNoSession):
			clearSessionCookie(w, r) // stale cookie; stop the browser sending it
		case err != nil:
			s.serverError(w, r, err)
			return
		default:
			r = r.WithContext(context.WithValue(r.Context(), userKey, u))
		}
		next.ServeHTTP(w, r)
	})
}

// userForToken returns the active user for a session token. Sessions of
// disabled or missing users are deleted.
func (s *Server) userForToken(ctx context.Context, token string) (*user.User, error) {
	id, err := s.sessions.Lookup(ctx, token)
	if err != nil {
		return nil, err
	}
	u, err := s.users.ByID(ctx, id)
	if errors.Is(err, user.ErrNotFound) || (err == nil && u.Disabled()) {
		if err := s.sessions.DeleteForUser(ctx, id); err != nil {
			return nil, err
		}
		return nil, auth.ErrNoSession
	}
	return u, err
}

// setupGate sends every request to /setup until the first admin exists.
func (s *Server) setupGate(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !s.setupDone.Load() && r.URL.Path != "/setup" && !isPublic(r.URL.Path) {
			http.Redirect(w, r, "/setup", http.StatusSeeOther)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) handleLoginForm(w http.ResponseWriter, r *http.Request) {
	next := safeNext(r.URL.Query().Get("next"))
	if currentUser(r.Context()) != nil {
		http.Redirect(w, r, next, http.StatusSeeOther)
		return
	}
	s.render(w, r, http.StatusOK, "login.html", pageData{Title: "Log in", Form: formValues{Next: next}})
}

func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, maxFormBytes)
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	username := r.PostForm.Get("username")
	password := r.PostForm.Get("password")
	next := safeNext(r.PostForm.Get("next"))

	u, err := s.checkLogin(r.Context(), username, password)
	if errors.Is(err, errBadLogin) {
		s.log.Info("login failed", "username", username, "remote", r.RemoteAddr)
		// One message for every failure, so it doesn't reveal whether the
		// username exists or the account is disabled.
		s.render(w, r, http.StatusUnauthorized, "login.html", pageData{
			Title: "Log in",
			Error: "Wrong username or password.",
			Form:  formValues{Username: username, Next: next},
		})
		return
	}
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	if err := s.startSession(w, r, u); err != nil {
		s.serverError(w, r, err)
		return
	}
	s.log.Info("login", "username", u.Username)
	http.Redirect(w, r, next, http.StatusSeeOther)
}

var errBadLogin = errors.New("wrong username or password")

// checkLogin verifies the credentials and returns the user, or errBadLogin.
// Every failure path spends about the same time on hashing.
func (s *Server) checkLogin(ctx context.Context, username, password string) (*user.User, error) {
	if len(password) > user.MaxPasswordBytes {
		return nil, errBadLogin
	}
	u, err := s.users.ByUsername(ctx, username)
	if errors.Is(err, user.ErrNotFound) {
		auth.VerifyDummy(password)
		return nil, errBadLogin
	}
	if err != nil {
		return nil, err
	}
	ok, rehash, err := auth.VerifyPassword(password, u.PasswordHash)
	if err != nil {
		return nil, err // a malformed stored hash is a server problem
	}
	if !ok || u.Disabled() {
		return nil, errBadLogin
	}
	if rehash {
		// Upgrade the stored hash to the current parameters. Login still
		// works if this fails, so only log it.
		if h, err := auth.HashPassword(password); err == nil {
			if err := s.users.SetPasswordHash(ctx, u.ID, h); err != nil {
				s.log.Warn("re-hash password", "username", u.Username, "err", err)
			}
		}
	}
	return u, nil
}

// startSession ends any session the browser already has and starts a new
// one. A fresh token on every login prevents session fixation (an attacker
// planting a known token before the victim logs in).
func (s *Server) startSession(w http.ResponseWriter, r *http.Request, u *user.User) error {
	if c, err := r.Cookie(sessionCookie); err == nil {
		if err := s.sessions.Delete(r.Context(), c.Value); err != nil {
			return err
		}
	}
	token, err := s.sessions.Create(r.Context(), u.ID)
	if err != nil {
		return err
	}
	if err := s.users.TouchLogin(r.Context(), u.ID); err != nil {
		return err
	}
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookie,
		Value:    token,
		Path:     "/",
		HttpOnly: true, // JavaScript can't read it
		Secure:   isHTTPS(r),
		SameSite: http.SameSiteLaxMode, // not sent on cross-site POSTs
		// No MaxAge: the browser drops the cookie when it closes. The server
		// also ends the session after auth.IdleTimeout / auth.MaxLifetime.
	})
	return nil
}

func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	if c, err := r.Cookie(sessionCookie); err == nil {
		if err := s.sessions.Delete(r.Context(), c.Value); err != nil {
			s.serverError(w, r, err)
			return
		}
	}
	if u := currentUser(r.Context()); u != nil {
		s.log.Info("logout", "username", u.Username)
	}
	clearSessionCookie(w, r)
	http.Redirect(w, r, "/login", http.StatusSeeOther)
}

func clearSessionCookie(w http.ResponseWriter, r *http.Request) {
	http.SetCookie(w, &http.Cookie{
		Name: sessionCookie, Value: "", Path: "/", MaxAge: -1, // MaxAge < 0 deletes it
		HttpOnly: true, Secure: isHTTPS(r), SameSite: http.SameSiteLaxMode,
	})
}

func (s *Server) handleSetupForm(w http.ResponseWriter, r *http.Request) {
	if s.setupDone.Load() {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	s.render(w, r, http.StatusOK, "setup.html", pageData{Title: "Setup"})
}

func (s *Server) handleSetup(w http.ResponseWriter, r *http.Request) {
	if s.setupDone.Load() {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxFormBytes)
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	f := r.PostForm
	form := formValues{Code: f.Get("code"), Username: f.Get("username"), Email: f.Get("email")}
	fail := func(msg string) {
		s.render(w, r, http.StatusUnprocessableEntity, "setup.html", pageData{Title: "Setup", Error: msg, Form: form})
	}

	if !auth.SetupCodeMatches(form.Code, s.setupCode) {
		s.log.Warn("setup: wrong setup code", "remote", r.RemoteAddr)
		form.Code = ""
		fail("The setup code is wrong. Check the server log for the current code.")
		return
	}
	if f.Get("password") != f.Get("password2") {
		fail("The passwords don't match.")
		return
	}

	u, err := s.users.CreateFirstAdmin(r.Context(), user.New{
		Username: form.Username,
		Email:    form.Email,
		Password: f.Get("password"),
	})
	var fe *user.FieldError
	switch {
	case errors.As(err, &fe): // errors.As finds a *FieldError and stores it in fe
		fail(fe.Message)
		return
	case errors.Is(err, user.ErrSetupDone):
		s.setupDone.Store(true)
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	case err != nil:
		s.serverError(w, r, err)
		return
	}

	s.setupDone.Store(true)
	s.log.Info("setup: created first admin", "username", u.Username)
	if err := s.startSession(w, r, u); err != nil {
		s.serverError(w, r, err)
		return
	}
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

// safeNext returns next if it is a path on this site, otherwise "/".
// Without this check, a link like /login?next=https://evil.example would
// send users to another site after logging in (an "open redirect").
func safeNext(next string) string {
	// "//host" and "/\host" are treated by browsers as links to another host.
	if !strings.HasPrefix(next, "/") || strings.HasPrefix(next, "//") || strings.HasPrefix(next, "/\\") {
		return "/"
	}
	u, err := url.Parse(next)
	if err != nil || u.Scheme != "" || u.Host != "" {
		return "/"
	}
	return next
}

// isHTTPS reports whether the browser used HTTPS, either directly or via a
// reverse proxy that sets X-Forwarded-Proto. Cookies are then marked Secure
// so the browser never sends them over plain HTTP.
func isHTTPS(r *http.Request) bool {
	return r.TLS != nil || strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https")
}
