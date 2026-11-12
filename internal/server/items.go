package server

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/keyboardsmash/hodrs-hoard/internal/item"
	"github.com/keyboardsmash/hodrs-hoard/internal/user"
)

// Item pages (spec 3–4). Viewing uses browse; everything that changes an
// item needs an editor, and emptying the trash an admin.

// canEdit reports whether the current user may change items.
func canEdit(r *http.Request) bool {
	u := currentUser(r.Context())
	return u != nil && u.Can(user.RoleEditor)
}

func itemURL(id int64) string { return "/items/" + strconv.FormatInt(id, 10) }

// pathID reads the {id} in the URL. On failure it answers 404.
func pathID(w http.ResponseWriter, r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.NotFound(w, r)
		return 0, false
	}
	return id, true
}

// loadItem loads the item named in the URL. Hidden and trashed items exist
// only for editors (spec 3.6); everyone else gets the same 404 as for a
// missing item. On failure it has answered and returns nil.
func (s *Server) loadItem(w http.ResponseWriter, r *http.Request) *item.Item {
	id, ok := pathID(w, r)
	if !ok {
		return nil
	}
	it, err := s.items.Get(r.Context(), id)
	if errors.Is(err, item.ErrNotFound) || (err == nil && it.Status != item.StatusActive && !canEdit(r)) {
		http.NotFound(w, r)
		return nil
	}
	if err != nil {
		s.serverError(w, r, err)
		return nil
	}
	return it
}

func (s *Server) handleItem(w http.ResponseWriter, r *http.Request) {
	it := s.loadItem(w, r)
	if it == nil {
		return
	}
	ctx := r.Context()
	dups, err := s.items.Duplicates(ctx, it.ID)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	data := pageData{Title: it.Title, Item: it, CreditRoles: item.RolesFor(it.MediaType.Kind)}
	// Duplicates are a hint for whoever maintains the catalogue, and may
	// point at hidden or trashed items, so only editors see them.
	if canEdit(r) {
		data.Duplicates = dups
	}
	data.AddedBy = s.username(r, it.AddedBy)
	data.UpdatedBy = s.username(r, it.UpdatedBy)
	s.render(w, r, http.StatusOK, "item.html", data)
}

// username returns a user's name for "added by" lines, or "".
func (s *Server) username(r *http.Request, id int64) string {
	if id == 0 {
		return ""
	}
	u, err := s.users.ByID(r.Context(), id)
	if err != nil {
		return "(unknown)"
	}
	return u.Username
}

// handleItemNew shows the media type choice, or with ?type= the form.
func (s *Server) handleItemNew(w http.ResponseWriter, r *http.Request) {
	types, err := s.items.MediaTypes(r.Context())
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	id, _ := strconv.ParseInt(r.URL.Query().Get("type"), 10, 64)
	mt := findType(types, id)
	if mt == nil {
		s.render(w, r, http.StatusOK, "item_new.html", pageData{Title: "Add item", MediaTypes: types})
		return
	}
	s.renderItemForm(w, r, http.StatusOK, nil, mt, newItemForm(mt.ID), nil)
}

func findType(types []*item.MediaType, id int64) *item.MediaType {
	for _, mt := range types {
		if mt.ID == id {
			return mt
		}
	}
	return nil
}

func (s *Server) handleItemCreate(w http.ResponseWriter, r *http.Request) {
	if !parseItemForm(w, r) {
		return
	}
	id, _ := strconv.ParseInt(r.PostForm.Get("media_type"), 10, 64)
	mt, err := s.items.MediaType(r.Context(), id)
	if errors.Is(err, item.ErrNotFound) {
		http.Redirect(w, r, "/items/new", http.StatusSeeOther) // choose a type first
		return
	}
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	f := readItemForm(r, mt.Kind, identifierTypes(mt.Kind, nil))
	it, errs := f.toItem()
	if err := s.readCover(r, &f, it, "", errs); err != nil {
		s.serverError(w, r, err)
		return
	}
	if len(errs) == 0 {
		var newID int64
		if newID, err = s.items.Create(r.Context(), currentUser(r.Context()).ID, it); err == nil {
			s.log.Info("item added", "id", newID, "title", it.Title, "by", currentUser(r.Context()).Username)
			http.Redirect(w, r, itemURL(newID)+"?msg=item-created", http.StatusSeeOther)
			return
		}
	}
	s.itemFormError(w, r, nil, mt, f, it, errs, err)
}

func (s *Server) handleItemEditForm(w http.ResponseWriter, r *http.Request) {
	it := s.loadItem(w, r)
	if it == nil {
		return
	}
	s.renderItemForm(w, r, http.StatusOK, it, it.MediaType, itemFormFrom(it), nil)
}

func (s *Server) handleItemEdit(w http.ResponseWriter, r *http.Request) {
	old := s.loadItem(w, r)
	if old == nil || !parseItemForm(w, r) {
		return
	}
	// The kind decides which fields the form has; it can't change, so read
	// the form as the item's current kind.
	kind := old.MediaType.Kind
	f := readItemForm(r, kind, identifierTypes(kind, old))
	it, errs := f.toItem()
	it.ID = old.ID
	it.ParentID = old.ParentID // not on the form yet (spec 3.4)
	err := s.readCover(r, &f, it, old.Cover, errs)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	if len(errs) == 0 {
		if err = s.items.Update(r.Context(), currentUser(r.Context()).ID, it); err == nil {
			http.Redirect(w, r, itemURL(it.ID)+"?msg=saved", http.StatusSeeOther)
			return
		}
	}
	s.itemFormError(w, r, old, old.MediaType, f, it, errs, err)
}

// itemFormError shows the form again with what went wrong: errs from
// reading the form, or err from the store. Anything else is a 500.
func (s *Server) itemFormError(w http.ResponseWriter, r *http.Request, old *item.Item, mt *item.MediaType,
	f itemForm, it *item.Item, errs item.FieldErrors, err error) {
	var storeErrs item.FieldErrors
	switch {
	case errors.As(err, &storeErrs):
		errs = storeErrs
	case errors.Is(err, item.ErrNotFound):
		http.NotFound(w, r)
		return
	case err != nil:
		s.serverError(w, r, err)
		return
	default:
		// The form had values that aren't numbers. Also run the store's
		// checks, so every problem shows at once.
		for k, v := range item.Clean(it) {
			if _, ok := errs[k]; !ok {
				errs[k] = v
			}
		}
	}
	s.renderItemForm(w, r, http.StatusUnprocessableEntity, old, mt, f, errs)
}

// otherErrors returns the messages the item form has no field for (such
// as an unknown credit role); the form lists them at the top.
func otherErrors(errs item.FieldErrors, kind item.Kind, idTypes []string) []string {
	shown := map[string]bool{}
	for _, k := range []string{"cover", "media_type", "title", "sort_title", "original_title", "alt_titles", "year",
		"description", "notes", "own_rating", "series", "series_number", "loaned_to", "loaned_on",
		"trailer", "mpaa", "age_limit", "genres", "languages", "countries"} {
		shown[k] = true
	}
	for _, k := range item.RolesFor(kind) {
		shown[k] = true
	}
	for _, t := range idTypes {
		shown["identifier_"+t] = true
	}
	video := []string{"subtitles", "runtime", "region", "video_standard", "seasons", "audio_format", "video_format"}
	book := []string{"publisher", "pages", "binding", "file_format", "file_path"}
	for _, k := range map[item.Kind][]string{item.KindVideo: video, item.KindBook: book}[kind] {
		shown[k] = true
	}
	var out []string
	for _, k := range sortedKeys(errs) {
		if !shown[k] {
			out = append(out, errs[k])
		}
	}
	return out
}

// renderItemForm shows the add form (old == nil) or the edit form.
func (s *Server) renderItemForm(w http.ResponseWriter, r *http.Request, status int, old *item.Item,
	mt *item.MediaType, f itemForm, errs item.FieldErrors) {
	ctx := r.Context()
	all, err := s.items.MediaTypes(ctx)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	// Only types of the same kind: the kind can't change (spec 3.1).
	var types []*item.MediaType
	for _, t := range all {
		if t.Kind == mt.Kind {
			types = append(types, t)
		}
	}
	series, err := s.items.SeriesNames(ctx)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	idTypes := identifierTypes(mt.Kind, old)
	data := pageData{
		Title:       "Add " + mt.Name,
		Item:        old,
		MediaType:   mt,
		MediaTypes:  types,
		ItemForm:    f,
		FieldErrors: errs,
		SeriesNames: series,
		CreditRoles: item.RolesFor(mt.Kind),
		IDTypes:     idTypes,
	}
	if old != nil {
		data.Title = "Edit " + old.Title
	}
	if len(errs) > 0 {
		data.Error = "Please correct the marked fields."
		data.OtherErrors = otherErrors(errs, mt.Kind, idTypes)
	}
	s.render(w, r, status, "item_form.html", data)
}

// handleItemStatus returns a handler that moves an item to status.
func (s *Server) handleItemStatus(status item.Status, msg string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		it := s.loadItem(w, r)
		if it == nil {
			return
		}
		u := currentUser(r.Context())
		if err := s.items.SetStatus(r.Context(), u.ID, it.ID, status); err != nil {
			s.serverError(w, r, err)
			return
		}
		s.log.Info("item status changed", "id", it.ID, "title", it.Title, "from", it.Status, "to", status, "by", u.Username)
		http.Redirect(w, r, itemURL(it.ID)+"?msg="+msg, http.StatusSeeOther)
	}
}

func (s *Server) handleTrash(w http.ResponseWriter, r *http.Request) {
	page, _ := strconv.Atoi(r.URL.Query().Get("page"))
	p, err := s.items.List(r.Context(), item.Query{Status: item.StatusTrashed, Sort: item.SortTrashed, Desc: true, Page: page})
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	s.render(w, r, http.StatusOK, "trash.html", pageData{Title: "Trash", Page: p})
}

func (s *Server) handleEmptyTrash(w http.ResponseWriter, r *http.Request) {
	n, err := s.items.EmptyTrash(r.Context())
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	s.log.Info("trash emptied", "items", n, "by", currentUser(r.Context()).Username)
	// Remove the covers only those items used. Failing here doesn't undo
	// anything; the hourly cleanup tries again.
	if files, err := s.sweepCovers(r.Context()); err != nil {
		s.log.Error("deleting unused covers", "err", err)
	} else if files > 0 {
		s.log.Info("deleted unused covers", "files", files)
	}
	http.Redirect(w, r, "/trash?msg=trash-emptied", http.StatusSeeOther)
}
