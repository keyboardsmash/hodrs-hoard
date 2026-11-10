package server

import (
	"net/http"
	"net/url"

	"github.com/keyboardsmash/hodrs-hoard/internal/user"
)

// Access control. Every route is wrapped in exactly one of these (see
// Handler), so a page's rule is visible next to its route:
//
//   - browse: read-only pages. Logged-in users, plus anonymous visitors
//     when anonymous browsing is on.
//   - requireRole: logged-in users whose role is at least the given one.
//
// The checks run on POST requests too. Hiding a button is never enough.

// browse allows logged-in users, and anonymous visitors if the setting is on.
func (s *Server) browse(h http.HandlerFunc) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if currentUser(r.Context()) == nil && !s.anonymous.Load() {
			redirectToLogin(w, r)
			return
		}
		h(w, r)
	})
}

// requireRole allows logged-in users whose role includes min. Anonymous
// visitors go to the login page; users without the role get a 403.
func (s *Server) requireRole(min user.Role, h http.HandlerFunc) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		u := currentUser(r.Context())
		if u == nil {
			redirectToLogin(w, r)
			return
		}
		if !u.Can(min) {
			s.log.Warn("forbidden", "username", u.Username, "role", u.Role, "method", r.Method, "path", r.URL.Path)
			s.render(w, r, http.StatusForbidden, "error.html", pageData{
				Title: "Not allowed",
				Error: "Your account doesn't have access to this page.",
			})
			return
		}
		h(w, r)
	})
}

// redirectToLogin sends the visitor to the login page, which brings them
// back to this page afterwards (for GET requests; a POST can't be repeated
// by a redirect).
func redirectToLogin(w http.ResponseWriter, r *http.Request) {
	target := "/login"
	if r.Method == http.MethodGet && r.URL.RequestURI() != "/" {
		target += "?next=" + url.QueryEscape(r.URL.RequestURI())
	}
	http.Redirect(w, r, target, http.StatusSeeOther)
}

// passwordGate keeps users with a temporary password (set by an admin, or
// imported from PHP4DVD) on the password page until they choose their own.
func (s *Server) passwordGate(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		u := currentUser(r.Context())
		if u != nil && u.MustChangePassword {
			switch r.URL.Path {
			case "/account/password", "/logout", "/healthz":
				// allowed
			default:
				http.Redirect(w, r, "/account/password", http.StatusSeeOther)
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}
