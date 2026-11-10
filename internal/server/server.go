// Package server wires up HTTP routes and renders the web UI.
package server

import (
	"bytes"
	"context"
	"database/sql"
	"embed"
	"encoding/json"
	"fmt"
	"html/template"
	"log/slog"
	"net/http"
	"sync/atomic"
	"time"

	"github.com/keyboardsmash/hodrs-hoard/internal/auth"
	"github.com/keyboardsmash/hodrs-hoard/internal/settings"
	"github.com/keyboardsmash/hodrs-hoard/internal/user"
)

// The //go:embed directive compiles the templates into the binary, so the
// static build needs no files next to it. It must sit directly above the var.
//
//go:embed templates/*.html
var templateFS embed.FS

// AppName is the display name used in page titles and the UI.
const AppName = "Höðrs Hoard"

// pages are the page templates. Each is parsed together with layout.html
// and partials.html (shared snippets).
var pages = []string{
	"index.html", "login.html", "setup.html", "error.html",
	"account.html", "password.html",
	"admin_users.html", "admin_user_new.html", "admin_user.html", "admin_settings.html",
}

// Server holds what the HTTP handlers need.
type Server struct {
	version   string
	started   time.Time
	log       *slog.Logger
	db        *sql.DB
	users     *user.Store
	sessions  *auth.Sessions
	remember  *auth.RememberTokens
	settings  *settings.Store
	templates map[string]*template.Template

	// anonymous mirrors the anonymous browsing setting. It's read on every
	// anonymous request, so it's kept in memory; the admin settings page
	// updates both the database and this copy. (Only one hoard process may
	// use a database, so the copy can't go stale.)
	anonymous atomic.Bool

	// setupDone is true once at least one user exists. atomic.Bool is safe
	// to read and write from many requests (goroutines) at once.
	setupDone atomic.Bool
	// setupCode must be entered on /setup. Set once in New, then only read.
	setupCode string
}

// New parses the templates, checks whether first-run setup is needed and
// returns a ready Server. If no users exist yet it logs the setup code.
func New(ctx context.Context, version string, log *slog.Logger, db *sql.DB) (*Server, error) {
	tmpl := make(map[string]*template.Template, len(pages))
	for _, p := range pages {
		t, err := template.ParseFS(templateFS, "templates/layout.html", "templates/partials.html", "templates/"+p)
		if err != nil {
			return nil, err
		}
		tmpl[p] = t
	}

	s := &Server{
		version:   version,
		started:   time.Now(),
		log:       log,
		db:        db,
		users:     user.NewStore(db),
		sessions:  auth.NewSessions(db),
		remember:  auth.NewRememberTokens(db),
		settings:  settings.NewStore(db),
		templates: tmpl,
	}

	anon, err := s.settings.Bool(ctx, settings.AnonymousBrowsing, false)
	if err != nil {
		return nil, fmt.Errorf("reading setting %s: %w", settings.AnonymousBrowsing, err)
	}
	s.anonymous.Store(anon)

	auth.PrepareDummy()

	n, err := s.users.Count(ctx)
	if err != nil {
		return nil, fmt.Errorf("counting users: %w", err)
	}
	if n > 0 {
		s.setupDone.Store(true)
	} else {
		s.setupCode = auth.NewSetupCode()
		log.Warn("no users yet: open /setup in a browser and enter this setup code to create the first admin",
			"setup_code", s.setupCode)
	}
	return s, nil
}

// Handler returns the router with all routes registered.
//
// Since Go 1.22 the standard ServeMux understands methods and wildcards in
// patterns ("GET /items/{id}"), so no router library is needed.
// "GET /{$}" matches exactly "/" and nothing below it.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", s.handleHealth)
	mux.HandleFunc("GET /login", s.handleLoginForm)
	mux.HandleFunc("POST /login", s.handleLogin)
	mux.HandleFunc("POST /logout", s.handleLogout)
	mux.HandleFunc("GET /setup", s.handleSetupForm)
	mux.HandleFunc("POST /setup", s.handleSetup)

	// Read-only pages: logged-in users, and anonymous visitors when allowed.
	mux.Handle("GET /{$}", s.browse(s.handleIndex))

	// Own account: any logged-in user.
	viewer := func(h http.HandlerFunc) http.Handler { return s.requireRole(user.RoleViewer, h) }
	mux.Handle("GET /account", viewer(s.handleAccount))
	mux.Handle("POST /account/email", viewer(s.handleAccountEmail))
	mux.Handle("GET /account/password", viewer(s.handlePasswordForm))
	mux.Handle("POST /account/password", viewer(s.handlePassword))
	mux.Handle("POST /account/logout-everywhere", viewer(s.handleLogoutEverywhere))

	// Admin pages. {id} is a wildcard; handlers read it with r.PathValue.
	admin := func(h http.HandlerFunc) http.Handler { return s.requireRole(user.RoleAdmin, h) }
	mux.Handle("GET /admin/users", admin(s.handleAdminUsers))
	mux.Handle("GET /admin/users/new", admin(s.handleAdminUserNewForm))
	mux.Handle("POST /admin/users/new", admin(s.handleAdminUserNew))
	mux.Handle("GET /admin/users/{id}", admin(s.handleAdminUser))
	mux.Handle("POST /admin/users/{id}/profile", admin(s.handleAdminUserProfile))
	mux.Handle("POST /admin/users/{id}/password", admin(s.handleAdminUserPassword))
	mux.Handle("POST /admin/users/{id}/disable", admin(s.handleAdminUserDisable))
	mux.Handle("POST /admin/users/{id}/enable", admin(s.handleAdminUserEnable))
	mux.Handle("GET /admin/settings", admin(s.handleAdminSettings))
	mux.Handle("POST /admin/settings", admin(s.handleAdminSettingsSave))

	// CSRF protection from the standard library (Go 1.25+). It rejects
	// cross-site POST/PUT/DELETE requests using the browser's Sec-Fetch-Site
	// and Origin headers, so forms need no hidden tokens. GET and HEAD must
	// therefore never change anything.
	csrf := http.NewCrossOriginProtection()
	csrf.SetDenyHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.log.Warn("blocked cross-origin request", "method", r.Method, "path", r.URL.Path,
			"origin", r.Header.Get("Origin"), "sec_fetch_site", r.Header.Get("Sec-Fetch-Site"))
		http.Error(w, "cross-origin request blocked", http.StatusForbidden)
	}))

	// Middleware wraps from the inside out: a request passes logRequests
	// first and the mux last.
	var h http.Handler = mux
	h = s.passwordGate(h)
	h = s.setupGate(h)
	h = s.loadUser(h)
	h = csrf.Handler(h)
	h = securityHeaders(h)
	return s.logRequests(h)
}

// CleanupSessions deletes expired sessions and remember tokens now and then
// every hour, until ctx is cancelled. main runs it in its own goroutine.
func (s *Server) CleanupSessions(ctx context.Context) {
	ticker := time.NewTicker(time.Hour)
	defer ticker.Stop()
	// A map from a name to a method value: s.sessions.DeleteExpired is a
	// function already bound to s.sessions, so it can be called later.
	cleanups := map[string]func(context.Context) (int64, error){
		"sessions":        s.sessions.DeleteExpired,
		"remember tokens": s.remember.DeleteExpired,
	}
	for {
		for what, cleanup := range cleanups {
			if n, err := cleanup(ctx); err != nil {
				if ctx.Err() == nil { // errors during shutdown are expected
					s.log.Error("deleting expired "+what, "err", err)
				}
			} else if n > 0 {
				s.log.Debug("deleted expired "+what, "count", n)
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

// health is the JSON body of /healthz. The `json:"..."` struct tags set the
// key names used when encoding.
type health struct {
	Status   string `json:"status"`
	Version  string `json:"version"`
	Uptime   string `json:"uptime"`
	Database string `json:"database"`
}

// health checks the database with a short timeout, so a hanging database
// can't make health checks hang too.
func (s *Server) health(ctx context.Context) health {
	h := health{
		Status:   "ok",
		Version:  s.version,
		Uptime:   time.Since(s.started).Round(time.Second).String(),
		Database: "ok",
	}
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	if err := s.db.PingContext(ctx); err != nil {
		s.log.Warn("health: database unreachable", "err", err)
		h.Status = "error"
		h.Database = "unreachable"
	}
	return h
}

// handleHealth is for monitoring and container health checks. It answers
// 503 Service Unavailable when the database can't be reached.
// The method receiver `(s *Server)` makes this a method on Server.
func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	h := s.health(r.Context())
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	if h.Status != "ok" {
		w.WriteHeader(http.StatusServiceUnavailable)
	}
	if err := json.NewEncoder(w).Encode(h); err != nil {
		s.log.Error("write health response", "err", err)
	}
}

// handleIndex renders the start page, which for now shows the health status.
func (s *Server) handleIndex(w http.ResponseWriter, r *http.Request) {
	s.render(w, r, http.StatusOK, "index.html", pageData{Health: s.health(r.Context())})
}

// pageData is what page templates receive. One struct for all pages keeps
// things simple while there are only a few; fields a page doesn't use stay
// empty.
type pageData struct {
	AppName        string
	Title          string
	User           *user.User // nil when nobody is logged in
	AllowAnonymous bool       // anonymous browsing is on (shows a "Log in" link)
	Message        string     // confirmation after a save, see messages
	Error          string
	Form           formValues
	Health         health
	MinPasswordLen int

	// Account and admin pages.
	Users     []*user.User // user list
	Target    *user.User   // the user an admin page is about
	Roles     []user.Role  // choices for role menus
	Anonymous bool         // the anonymous browsing setting, for its checkbox
	Forced    bool         // the user must change their password now
}

// messages are the confirmations a page shows after a redirect with
// ?msg=<key>. Only these fixed texts can appear, so a crafted link can't put
// arbitrary words on the page.
var messages = map[string]string{
	"created":        "User created.",
	"saved":          "Changes saved.",
	"password":       "Password changed.",
	"password-reset": "Password reset. The user must choose a new one at next login.",
	"disabled":       "User disabled. Their sessions have been ended.",
	"enabled":        "User enabled.",

	"logged-out-everywhere": "You have been logged out on all devices.",
}

// formValues are submitted values shown again after an error. Passwords are
// never echoed back.
type formValues struct {
	Code     string
	Username string
	Email    string
	Role     user.Role
	Next     string
	Remember bool // the "Remember me" checkbox on the login form
	// MustChange is the "must change password" checkbox on the new-user form.
	MustChange bool
}

// render executes a page template. It renders into a buffer first, so a
// template error gives a clean 500 instead of half a page.
func (s *Server) render(w http.ResponseWriter, r *http.Request, status int, page string, data pageData) {
	data.AppName = AppName
	data.User = currentUser(r.Context())
	data.AllowAnonymous = s.anonymous.Load()
	data.MinPasswordLen = user.MinPasswordLen
	data.Roles = user.Roles
	if data.Message == "" {
		data.Message = messages[r.URL.Query().Get("msg")] // unknown keys give ""
	}

	var buf bytes.Buffer
	if err := s.templates[page].ExecuteTemplate(&buf, "layout", data); err != nil {
		s.serverError(w, r, fmt.Errorf("render %s: %w", page, err))
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	// Pages can show private data; don't let browsers or proxies cache them.
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	buf.WriteTo(w) //nolint:errcheck // a failed write means the client went away
}

// serverError logs err and sends a generic 500 page; details stay in the log.
func (s *Server) serverError(w http.ResponseWriter, r *http.Request, err error) {
	s.log.Error("request failed", "method", r.Method, "path", r.URL.Path, "err", err)
	http.Error(w, "internal server error", http.StatusInternalServerError)
}

// securityHeaders adds headers that are cheap protection for every page.
func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("X-Content-Type-Options", "nosniff") // don't guess content types
		h.Set("X-Frame-Options", "DENY")           // no embedding in frames (clickjacking)
		h.Set("Referrer-Policy", "same-origin")
		next.ServeHTTP(w, r)
	})
}

// statusRecorder wraps a ResponseWriter to remember the status code.
// Embedding http.ResponseWriter (a field with no name) means all its methods
// are available on statusRecorder; we only override WriteHeader.
type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (r *statusRecorder) WriteHeader(code int) {
	r.status = code
	r.ResponseWriter.WriteHeader(code)
}

// logRequests is middleware: it wraps a handler and logs each request.
func (s *Server) logRequests(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(rec, r)
		s.log.Debug("request",
			"method", r.Method,
			"path", r.URL.Path,
			"status", rec.status,
			"duration", time.Since(start),
		)
	})
}
