package server

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/keyboardsmash/hodrs-hoard/internal/item"
)

// Media type page (spec 3.1): list, add, rename, delete. Editors and up.

func (s *Server) handleMediaTypes(w http.ResponseWriter, r *http.Request) {
	s.renderMediaTypes(w, r, http.StatusOK, formValues{Kind: item.KindVideo, Physical: true}, "")
}

func (s *Server) renderMediaTypes(w http.ResponseWriter, r *http.Request, status int, form formValues, errMsg string) {
	types, err := s.items.MediaTypes(r.Context())
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	s.render(w, r, status, "media_types.html", pageData{
		Title: "Media types", MediaTypes: types, Form: form, Error: errMsg,
	})
}

func (s *Server) handleMediaTypeCreate(w http.ResponseWriter, r *http.Request) {
	if !parseForm(w, r) {
		return
	}
	f := r.PostForm
	form := formValues{Name: f.Get("name"), Kind: item.Kind(f.Get("kind")), Physical: f.Get("physical") == "on"}
	mt, err := s.items.CreateMediaType(r.Context(), form.Name, form.Kind, form.Physical)
	if err != nil {
		s.mediaTypeError(w, r, form, err)
		return
	}
	s.log.Info("media type added", "name", mt.Name, "kind", mt.Kind, "by", currentUser(r.Context()).Username)
	http.Redirect(w, r, "/media-types?msg=type-created", http.StatusSeeOther)
}

func (s *Server) handleMediaTypeRename(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok || !parseForm(w, r) {
		return
	}
	name := r.PostForm.Get("name")
	if err := s.items.RenameMediaType(r.Context(), id, name); err != nil {
		s.mediaTypeError(w, r, formValues{Kind: item.KindVideo, Physical: true}, err)
		return
	}
	s.log.Info("media type renamed", "id", id, "name", name, "by", currentUser(r.Context()).Username)
	http.Redirect(w, r, "/media-types?msg=saved", http.StatusSeeOther)
}

func (s *Server) handleMediaTypeDelete(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	if err := s.items.DeleteMediaType(r.Context(), id); err != nil {
		s.mediaTypeError(w, r, formValues{Kind: item.KindVideo, Physical: true}, err)
		return
	}
	s.log.Info("media type deleted", "id", id, "by", currentUser(r.Context()).Username)
	http.Redirect(w, r, "/media-types?msg=type-deleted", http.StatusSeeOther)
}

// mediaTypeError shows a rule or validation error on the media type page.
func (s *Server) mediaTypeError(w http.ResponseWriter, r *http.Request, form formValues, err error) {
	var fe item.FieldErrors
	switch {
	case errors.As(err, &fe):
		for _, k := range sortedKeys(fe) {
			s.renderMediaTypes(w, r, http.StatusUnprocessableEntity, form, fe[k])
			return // one message is enough here
		}
	case errors.Is(err, item.ErrInUse):
		s.renderMediaTypes(w, r, http.StatusUnprocessableEntity, form,
			"This media type is used by items, including any in the trash, so it can't be deleted.")
	case errors.Is(err, item.ErrNotFound):
		http.NotFound(w, r)
	default:
		s.serverError(w, r, err)
	}
}

// itemCount formats a count for the media type list.
func itemCount(n int) string {
	if n == 1 {
		return "1 item"
	}
	return strconv.Itoa(n) + " items"
}
