package server

import (
	"net/http"

	"github.com/keyboardsmash/hodrs-hoard/internal/user"
)

// Own account pages. Routes use requireRole(viewer), so currentUser is
// never nil here.

func (s *Server) handleAccount(w http.ResponseWriter, r *http.Request) {
	u := currentUser(r.Context())
	s.render(w, r, http.StatusOK, "account.html", pageData{
		Title: "Your account",
		Form:  formValues{Email: u.Email},
	})
}

func (s *Server) handleAccountEmail(w http.ResponseWriter, r *http.Request) {
	if !parseForm(w, r) {
		return
	}
	u := currentUser(r.Context())
	email := r.PostForm.Get("email")
	if err := s.users.UpdateEmail(r.Context(), u.ID, email); err != nil {
		s.formError(w, r, err, "account.html", pageData{Title: "Your account", Form: formValues{Email: email}})
		return
	}
	http.Redirect(w, r, "/account?msg=saved", http.StatusSeeOther)
}

func (s *Server) handlePasswordForm(w http.ResponseWriter, r *http.Request) {
	s.render(w, r, http.StatusOK, "password.html", s.passwordPage(r))
}

func (s *Server) passwordPage(r *http.Request) pageData {
	forced := currentUser(r.Context()).MustChangePassword
	title := "Change password"
	if forced {
		title = "Choose a new password"
	}
	return pageData{Title: title, Forced: forced}
}

func (s *Server) handlePassword(w http.ResponseWriter, r *http.Request) {
	if !parseForm(w, r) {
		return
	}
	u := currentUser(r.Context())
	f := r.PostForm
	if f.Get("password") != f.Get("password2") {
		data := s.passwordPage(r)
		data.Error = "The new passwords don't match."
		s.render(w, r, http.StatusUnprocessableEntity, "password.html", data)
		return
	}
	if err := s.users.ChangeOwnPassword(r.Context(), u.ID, f.Get("current"), f.Get("password")); err != nil {
		s.formError(w, r, err, "password.html", s.passwordPage(r))
		return
	}
	// End the user's other logins (a stolen session, say); keep this one.
	if err := s.endOtherSessions(r, u.ID); err != nil {
		s.serverError(w, r, err)
		return
	}
	s.log.Info("password changed", "username", u.Username)
	http.Redirect(w, r, "/account?msg=password", http.StatusSeeOther)
}

// parseForm reads a posted form, capped at maxFormBytes. On failure it
// answers 400 and returns false, so handlers can just `return`.
func parseForm(w http.ResponseWriter, r *http.Request) bool {
	r.Body = http.MaxBytesReader(w, r.Body, maxFormBytes)
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return false
	}
	return true
}

// formError shows err on the form page if it's something the user can fix
// (user.Message knows it), otherwise answers 500.
func (s *Server) formError(w http.ResponseWriter, r *http.Request, err error, page string, data pageData) {
	msg := user.Message(err)
	if msg == "" {
		s.serverError(w, r, err)
		return
	}
	data.Error = msg
	s.render(w, r, http.StatusUnprocessableEntity, page, data)
}
