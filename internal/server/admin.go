package server

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/keyboardsmash/hodrs-hoard/internal/settings"
	"github.com/keyboardsmash/hodrs-hoard/internal/user"
)

// Admin pages. Routes use requireRole(admin); the rules about which changes
// are allowed (own role, last admin, ...) are enforced by the user store.

func (s *Server) handleAdminUsers(w http.ResponseWriter, r *http.Request) {
	users, err := s.users.List(r.Context())
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	s.render(w, r, http.StatusOK, "admin_users.html", pageData{Title: "Users", Users: users})
}

func (s *Server) handleAdminUserNewForm(w http.ResponseWriter, r *http.Request) {
	s.render(w, r, http.StatusOK, "admin_user_new.html", pageData{
		Title: "New user",
		Form:  formValues{Role: user.RoleViewer, MustChange: true},
	})
}

func (s *Server) handleAdminUserNew(w http.ResponseWriter, r *http.Request) {
	if !parseForm(w, r) {
		return
	}
	f := r.PostForm
	form := formValues{
		Username:   f.Get("username"),
		Email:      f.Get("email"),
		Role:       user.Role(f.Get("role")),
		MustChange: f.Get("must_change") == "on", // unticked checkboxes aren't sent at all
	}
	page := pageData{Title: "New user", Form: form}
	if f.Get("password") != f.Get("password2") {
		page.Error = "The passwords don't match."
		s.render(w, r, http.StatusUnprocessableEntity, "admin_user_new.html", page)
		return
	}
	u, err := s.users.Create(r.Context(), user.New{
		Username:           form.Username,
		Email:              form.Email,
		Password:           f.Get("password"),
		Role:               form.Role,
		MustChangePassword: form.MustChange,
	})
	if err != nil {
		s.formError(w, r, err, "admin_user_new.html", page)
		return
	}
	s.log.Info("user created", "username", u.Username, "role", u.Role, "by", currentUser(r.Context()).Username)
	http.Redirect(w, r, userURL(u.ID)+"?msg=created", http.StatusSeeOther)
}

// targetUser loads the user named by the {id} in the URL. On failure it
// has already answered (404 or 500) and returns nil.
func (s *Server) targetUser(w http.ResponseWriter, r *http.Request) *user.User {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.NotFound(w, r)
		return nil
	}
	u, err := s.users.ByID(r.Context(), id)
	if errors.Is(err, user.ErrNotFound) {
		http.NotFound(w, r)
		return nil
	}
	if err != nil {
		s.serverError(w, r, err)
		return nil
	}
	return u
}

func userURL(id int64) string { return "/admin/users/" + strconv.FormatInt(id, 10) }

func (s *Server) handleAdminUser(w http.ResponseWriter, r *http.Request) {
	t := s.targetUser(w, r)
	if t == nil {
		return
	}
	s.render(w, r, http.StatusOK, "admin_user.html", s.userPage(t))
}

func (s *Server) userPage(t *user.User) pageData {
	return pageData{Title: t.Username, Target: t, Form: formValues{Email: t.Email, Role: t.Role}}
}

// adminUserError shows a rule or validation error on the user's page.
func (s *Server) adminUserError(w http.ResponseWriter, r *http.Request, t *user.User, err error) {
	s.formError(w, r, err, "admin_user.html", s.userPage(t))
}

func (s *Server) handleAdminUserProfile(w http.ResponseWriter, r *http.Request) {
	t := s.targetUser(w, r)
	if t == nil || !parseForm(w, r) {
		return
	}
	actor := currentUser(r.Context())
	email := r.PostForm.Get("email")
	role := user.Role(r.PostForm.Get("role"))
	if t.ID == actor.ID {
		role = t.Role // the form has no role menu for yourself; don't trip ErrOwnRole
	}
	// &email passes a pointer to email: "set the email to this".
	if err := s.users.UpdateProfile(r.Context(), actor.ID, t.ID, &email, role); err != nil {
		page := s.userPage(t)
		page.Form = formValues{Email: email, Role: role} // show what was typed
		s.formError(w, r, err, "admin_user.html", page)
		return
	}
	if role != t.Role {
		s.log.Info("role changed", "username", t.Username, "from", t.Role, "to", role, "by", actor.Username)
	}
	http.Redirect(w, r, userURL(t.ID)+"?msg=saved", http.StatusSeeOther)
}

func (s *Server) handleAdminUserPassword(w http.ResponseWriter, r *http.Request) {
	t := s.targetUser(w, r)
	if t == nil || !parseForm(w, r) {
		return
	}
	actor := currentUser(r.Context())
	if t.ID == actor.ID {
		// Your own password goes through /account/password, which asks for
		// the current one.
		http.Redirect(w, r, "/account/password", http.StatusSeeOther)
		return
	}
	f := r.PostForm
	if f.Get("password") != f.Get("password2") {
		page := s.userPage(t)
		page.Error = "The passwords don't match."
		s.render(w, r, http.StatusUnprocessableEntity, "admin_user.html", page)
		return
	}
	if err := s.users.SetPassword(r.Context(), t.ID, f.Get("password"), true); err != nil {
		s.adminUserError(w, r, t, err)
		return
	}
	if err := s.sessions.DeleteForUser(r.Context(), t.ID); err != nil {
		s.serverError(w, r, err)
		return
	}
	s.log.Info("password reset", "username", t.Username, "by", actor.Username)
	http.Redirect(w, r, userURL(t.ID)+"?msg=password-reset", http.StatusSeeOther)
}

func (s *Server) handleAdminUserDisable(w http.ResponseWriter, r *http.Request) {
	t := s.targetUser(w, r)
	if t == nil {
		return
	}
	actor := currentUser(r.Context())
	if err := s.users.Disable(r.Context(), actor.ID, t.ID); err != nil {
		s.adminUserError(w, r, t, err)
		return
	}
	// Also checked on every request (see userForToken), but there is no
	// reason to keep the sessions around.
	if err := s.sessions.DeleteForUser(r.Context(), t.ID); err != nil {
		s.serverError(w, r, err)
		return
	}
	s.log.Info("user disabled", "username", t.Username, "by", actor.Username)
	http.Redirect(w, r, userURL(t.ID)+"?msg=disabled", http.StatusSeeOther)
}

func (s *Server) handleAdminUserEnable(w http.ResponseWriter, r *http.Request) {
	t := s.targetUser(w, r)
	if t == nil {
		return
	}
	if err := s.users.Enable(r.Context(), t.ID); err != nil {
		s.adminUserError(w, r, t, err)
		return
	}
	s.log.Info("user enabled", "username", t.Username, "by", currentUser(r.Context()).Username)
	http.Redirect(w, r, userURL(t.ID)+"?msg=enabled", http.StatusSeeOther)
}

func (s *Server) handleAdminSettings(w http.ResponseWriter, r *http.Request) {
	s.render(w, r, http.StatusOK, "admin_settings.html", pageData{
		Title:     "Settings",
		Anonymous: s.anonymous.Load(),
	})
}

func (s *Server) handleAdminSettingsSave(w http.ResponseWriter, r *http.Request) {
	if !parseForm(w, r) {
		return
	}
	anon := r.PostForm.Get("anonymous_browsing") == "on"
	if err := s.settings.SetBool(r.Context(), settings.AnonymousBrowsing, anon); err != nil {
		s.serverError(w, r, err)
		return
	}
	s.anonymous.Store(anon) // only after the database write succeeded
	s.log.Info("setting changed", "key", settings.AnonymousBrowsing, "value", anon,
		"by", currentUser(r.Context()).Username)
	http.Redirect(w, r, "/admin/settings?msg=saved", http.StatusSeeOther)
}
