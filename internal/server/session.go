package server

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/keyboardsmash/hodrs-hoard/internal/auth"
	"github.com/keyboardsmash/hodrs-hoard/internal/user"
)

// A browser can hold two cookies:
//
//   - hoard_session: the login session (spec 2.6). Gone when the browser
//     closes; the server also ends it after 12 h idle or 7 days.
//   - hoard_remember: only after "Remember me". Lasts 30 days from its last
//     use. When the session has ended, loadUser uses it to start a new one.
const (
	sessionCookie  = "hoard_session"
	rememberCookie = "hoard_remember"
)

// loadUser finds the logged-in user, if any, and stores it in the request
// context for handlers and templates. It tries the session cookie first and
// falls back to the remember cookie.
func (s *Server) loadUser(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if isPublic(r.URL.Path) {
			next.ServeHTTP(w, r)
			return
		}
		ctx := r.Context()
		var u *user.User
		staleSession := false

		if c, err := r.Cookie(sessionCookie); err == nil {
			u, err = s.userForSession(ctx, c.Value)
			switch {
			case errors.Is(err, auth.ErrNoSession):
				staleSession = true
			case err != nil:
				s.serverError(w, r, err)
				return
			}
		}

		if u == nil {
			if c, err := r.Cookie(rememberCookie); err == nil {
				u, err = s.userForRemember(ctx, c.Value)
				switch {
				case errors.Is(err, auth.ErrNoRememberToken):
					clearCookie(w, r, rememberCookie)
				case err != nil:
					s.serverError(w, r, err)
					return
				default:
					// Remembered: start a new session, and send the remember
					// cookie again so the browser's expiry slides too.
					token, err := s.newSession(w, r, u)
					if err != nil {
						s.serverError(w, r, err)
						return
					}
					// Later code in this request must see the new session,
					// not the expired one the browser sent.
					ctx = context.WithValue(ctx, sessionTokenKey, token)
					setCookie(w, r, rememberCookie, c.Value, auth.RememberLifetime)
					staleSession = false // newSession replaced the cookie
					s.log.Info("login via remember me", "username", u.Username)
				}
			}
		}

		if staleSession {
			clearCookie(w, r, sessionCookie) // stop the browser sending it
		}
		if u != nil {
			r = r.WithContext(context.WithValue(ctx, userKey, u))
		}
		next.ServeHTTP(w, r)
	})
}

// userForSession returns the active user for a session token.
func (s *Server) userForSession(ctx context.Context, token string) (*user.User, error) {
	id, err := s.sessions.Lookup(ctx, token)
	if err != nil {
		return nil, err
	}
	u, err := s.activeUser(ctx, id)
	if errors.Is(err, errInactive) {
		return nil, auth.ErrNoSession
	}
	return u, err
}

// userForRemember returns the active user for a remember token.
func (s *Server) userForRemember(ctx context.Context, token string) (*user.User, error) {
	id, err := s.remember.Use(ctx, token)
	if err != nil {
		return nil, err
	}
	u, err := s.activeUser(ctx, id)
	if errors.Is(err, errInactive) {
		return nil, auth.ErrNoRememberToken
	}
	return u, err
}

var errInactive = errors.New("user disabled or missing")

// activeUser loads a user for a session or remember token. If the user is
// disabled or gone, all their logins are ended and errInactive returned.
func (s *Server) activeUser(ctx context.Context, id int64) (*user.User, error) {
	u, err := s.users.ByID(ctx, id)
	if errors.Is(err, user.ErrNotFound) || (err == nil && u.Disabled()) {
		if err := s.endAllSessions(ctx, id); err != nil {
			return nil, err
		}
		return nil, errInactive
	}
	return u, err
}

// login is called after a successful password check. It ends whatever
// login the browser already had and starts a new one. Fresh tokens on every
// login prevent session fixation (an attacker planting a known token before
// the victim logs in). With remember, it also sets a remember cookie.
func (s *Server) login(w http.ResponseWriter, r *http.Request, u *user.User, remember bool) error {
	if err := s.endBrowserSession(r); err != nil {
		return err
	}
	if _, err := s.newSession(w, r, u); err != nil {
		return err
	}
	if !remember {
		if _, err := r.Cookie(rememberCookie); err == nil {
			clearCookie(w, r, rememberCookie) // its token was deleted above
		}
		return nil
	}
	token, err := s.remember.Create(r.Context(), u.ID)
	if err != nil {
		return err
	}
	setCookie(w, r, rememberCookie, token, auth.RememberLifetime)
	return nil
}

// newSession creates a session for u, records the login, sets the session
// cookie and returns the new token.
func (s *Server) newSession(w http.ResponseWriter, r *http.Request, u *user.User) (string, error) {
	token, err := s.sessions.Create(r.Context(), u.ID)
	if err != nil {
		return "", err
	}
	if err := s.users.TouchLogin(r.Context(), u.ID); err != nil {
		return "", err
	}
	// Max age 0: no expiry date, so the browser drops it when it closes.
	setCookie(w, r, sessionCookie, token, 0)
	return token, nil
}

// sessionTokenKey holds a session token that loadUser created during this
// request. It overrides the (expired) one in the request's cookie.
const sessionTokenKey ctxKey = 1

// sessionToken returns this browser's current session token, or "".
func sessionToken(r *http.Request) string {
	if t, ok := r.Context().Value(sessionTokenKey).(string); ok {
		return t
	}
	return cookieValue(r, sessionCookie)
}

// endBrowserSession deletes the session and remember token this browser
// sent. The cookies themselves are left to the caller.
func (s *Server) endBrowserSession(r *http.Request) error {
	if t := sessionToken(r); t != "" {
		if err := s.sessions.Delete(r.Context(), t); err != nil {
			return err
		}
	}
	if c, err := r.Cookie(rememberCookie); err == nil {
		if err := s.remember.Delete(r.Context(), c.Value); err != nil {
			return err
		}
	}
	return nil
}

// endAllSessions ends every session and remember token of a user: after an
// admin disables them or resets their password, or "Log out everywhere".
func (s *Server) endAllSessions(ctx context.Context, userID int64) error {
	if err := s.sessions.DeleteForUser(ctx, userID); err != nil {
		return err
	}
	return s.remember.DeleteForUser(ctx, userID)
}

// endOtherSessions ends a user's sessions and remember tokens except the
// ones this browser sent, after the user changed their own password.
func (s *Server) endOtherSessions(r *http.Request, userID int64) error {
	// A missing token is ""; no token hashes to the same
	// value as "", so then all tokens of that kind are deleted.
	if err := s.sessions.DeleteOthers(r.Context(), userID, sessionToken(r)); err != nil {
		return err
	}
	return s.remember.DeleteOthers(r.Context(), userID, cookieValue(r, rememberCookie))
}

func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	if err := s.endBrowserSession(r); err != nil {
		s.serverError(w, r, err)
		return
	}
	if u := currentUser(r.Context()); u != nil {
		s.log.Info("logout", "username", u.Username)
	}
	clearCookie(w, r, sessionCookie)
	clearCookie(w, r, rememberCookie)
	http.Redirect(w, r, "/login", http.StatusSeeOther)
}

// handleLogoutEverywhere ends all of the user's sessions and remember
// tokens, on every device, for example after losing a laptop.
func (s *Server) handleLogoutEverywhere(w http.ResponseWriter, r *http.Request) {
	u := currentUser(r.Context())
	if err := s.endAllSessions(r.Context(), u.ID); err != nil {
		s.serverError(w, r, err)
		return
	}
	s.log.Info("logout everywhere", "username", u.Username)
	clearCookie(w, r, sessionCookie)
	clearCookie(w, r, rememberCookie)
	http.Redirect(w, r, "/login?msg=logged-out-everywhere", http.StatusSeeOther)
}

func cookieValue(r *http.Request, name string) string {
	if c, err := r.Cookie(name); err == nil {
		return c.Value
	}
	return ""
}

// setCookie sets one of our cookies. maxAge 0 means a browser-session cookie.
func setCookie(w http.ResponseWriter, r *http.Request, name, value string, maxAge time.Duration) {
	http.SetCookie(w, &http.Cookie{
		Name:     name,
		Value:    value,
		Path:     "/",
		MaxAge:   int(maxAge / time.Second),
		HttpOnly: true, // JavaScript can't read it
		Secure:   isHTTPS(r),
		SameSite: http.SameSiteLaxMode, // not sent on cross-site POSTs
	})
}

// clearCookie tells the browser to delete a cookie.
func clearCookie(w http.ResponseWriter, r *http.Request, name string) {
	http.SetCookie(w, &http.Cookie{
		Name: name, Value: "", Path: "/", MaxAge: -1, // MaxAge < 0 deletes it
		HttpOnly: true, Secure: isHTTPS(r), SameSite: http.SameSiteLaxMode,
	})
}
