package server

import (
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"

	"github.com/keyboardsmash/hodrs-hoard/internal/item"
)

// itemForm holds the item form's values as typed. Numbers stay strings
// here, so a form sent back with an error shows exactly what the user
// typed ("19x9"), not a cleaned-up value.
type itemForm struct {
	MediaTypeID   int64
	Title         string
	SortTitle     string
	OriginalTitle string
	AltTitles     string // one per line
	Year          string
	Description   string
	Notes         string
	OwnRating     string
	Series        string
	SeriesNumber  string

	Own, Seen, Favourite, Loaned bool
	LoanedTo, LoanedOn           string
	Trailer, MPAA, AgeLimit      string

	Credits     map[string]string // role → one name per line
	Genres      string            // comma-separated, like the lists below
	Languages   string
	Subtitles   string
	Countries   string
	Identifiers map[string]string // type → comma-separated values

	Runtime, Region, VideoStandard string
	IsTV                           bool
	Seasons                        string
	AudioFormat, VideoFormat       string

	Publisher, Pages, Binding string
	FileFormat, FilePath      string

	// Cover is the hash of the cover the form carries (see readCover).
	Cover string
}

// newItemForm is the empty form for a new item.
func newItemForm(mediaTypeID int64) itemForm {
	return itemForm{MediaTypeID: mediaTypeID, Own: true}
}

// itemFormFrom fills the form from a saved item, for editing.
func itemFormFrom(it *item.Item) itemForm {
	f := itemForm{
		MediaTypeID:   it.MediaTypeID,
		Title:         it.Title,
		SortTitle:     it.SortTitle,
		OriginalTitle: it.OriginalTitle,
		AltTitles:     strings.Join(it.AltTitles, "\n"),
		Year:          numberText(it.Year),
		Description:   it.Description,
		Notes:         it.Notes,
		OwnRating:     numberText(it.OwnRating),
		Series:        it.Series,
		SeriesNumber:  it.SeriesNumber,
		Own:           it.Own,
		Seen:          it.Seen,
		Favourite:     it.Favourite,
		Loaned:        it.Loaned,
		LoanedTo:      it.LoanedTo,
		LoanedOn:      it.LoanedOn,
		Trailer:       it.Trailer,
		MPAA:          it.MPAA,
		Credits:       map[string]string{},
		Genres:        strings.Join(it.Genres, ", "),
		Languages:     strings.Join(it.Languages, ", "),
		Subtitles:     strings.Join(it.Subtitles, ", "),
		Countries:     strings.Join(it.Countries, ", "),
		Identifiers:   map[string]string{},
		Runtime:       numberText(it.Runtime),
		Region:        it.Region,
		VideoStandard: it.VideoStandard,
		IsTV:          it.IsTV,
		Seasons:       it.Seasons,
		AudioFormat:   it.AudioFormat,
		VideoFormat:   it.VideoFormat,
		Publisher:     it.Publisher,
		Pages:         numberText(it.Pages),
		Binding:       it.Binding,
		FileFormat:    it.FileFormat,
		FilePath:      it.FilePath,
		Cover:         it.Cover,
	}
	if it.AgeLimit != nil {
		f.AgeLimit = strconv.Itoa(*it.AgeLimit)
	}
	for _, c := range it.Credits {
		if f.Credits[c.Role] != "" {
			f.Credits[c.Role] += "\n"
		}
		f.Credits[c.Role] += c.Name
	}
	for _, id := range it.Identifiers {
		if f.Identifiers[id.Type] != "" {
			f.Identifiers[id.Type] += ", "
		}
		f.Identifiers[id.Type] += id.Value
	}
	return f
}

func numberText(n int) string {
	if n == 0 {
		return ""
	}
	return strconv.Itoa(n)
}

// readItemForm reads a posted item form. Only fields that apply to kind
// are read; credit roles and identifier types come from the form page
// (see creditRoles and identifierTypes).
func readItemForm(r *http.Request, kind item.Kind, idTypes []string) itemForm {
	p := r.PostForm
	get := p.Get
	on := func(name string) bool { return get(name) == "on" } // a ticked checkbox sends "on"
	mt, _ := strconv.ParseInt(get("media_type"), 10, 64)
	f := itemForm{
		MediaTypeID:   mt,
		Title:         get("title"),
		SortTitle:     get("sort_title"),
		OriginalTitle: get("original_title"),
		AltTitles:     get("alt_titles"),
		Year:          get("year"),
		Description:   get("description"),
		Notes:         get("notes"),
		OwnRating:     get("own_rating"),
		Series:        get("series"),
		SeriesNumber:  get("series_number"),
		Own:           on("own"),
		Seen:          on("seen"),
		Favourite:     on("favourite"),
		Loaned:        on("loaned"),
		LoanedTo:      get("loaned_to"),
		LoanedOn:      get("loaned_on"),
		Trailer:       get("trailer"),
		MPAA:          get("mpaa"),
		AgeLimit:      get("age_limit"),
		Credits:       map[string]string{},
		Genres:        get("genres"),
		Languages:     get("languages"),
		Countries:     get("countries"),
		Identifiers:   map[string]string{},
	}
	for _, role := range item.RolesFor(kind) {
		f.Credits[role] = get("credit_" + role)
	}
	for _, t := range idTypes {
		f.Identifiers[t] = get("id_" + t)
	}
	switch kind {
	case item.KindVideo:
		f.Subtitles = get("subtitles")
		f.Runtime = get("runtime")
		f.Region = get("region")
		f.VideoStandard = get("video_standard")
		f.IsTV = on("is_tv")
		f.Seasons = get("seasons")
		f.AudioFormat = get("audio_format")
		f.VideoFormat = get("video_format")
	case item.KindBook:
		f.Publisher = get("publisher")
		f.Pages = get("pages")
		f.Binding = get("binding")
		f.FileFormat = get("file_format")
		f.FilePath = get("file_path")
	}
	return f
}

// toItem converts the form to an item. It reports values that aren't
// numbers; everything else is checked by the item store.
func (f itemForm) toItem() (*item.Item, item.FieldErrors) {
	errs := item.FieldErrors{}
	num := func(field, s string) int {
		s = strings.TrimSpace(s)
		if s == "" {
			return 0
		}
		n, err := strconv.Atoi(s)
		if err != nil {
			errs[field] = "Must be a whole number."
		}
		return n
	}
	it := &item.Item{
		MediaTypeID:   f.MediaTypeID,
		Title:         f.Title,
		SortTitle:     f.SortTitle,
		OriginalTitle: f.OriginalTitle,
		AltTitles:     item.SplitLines(f.AltTitles),
		Year:          num("year", f.Year),
		Description:   f.Description,
		Notes:         f.Notes,
		OwnRating:     num("own_rating", f.OwnRating),
		Series:        f.Series,
		SeriesNumber:  f.SeriesNumber,
		Own:           f.Own,
		Seen:          f.Seen,
		Favourite:     f.Favourite,
		Loaned:        f.Loaned,
		LoanedTo:      f.LoanedTo,
		LoanedOn:      f.LoanedOn,
		Trailer:       f.Trailer,
		MPAA:          f.MPAA,
		Genres:        item.SplitComma(f.Genres),
		Languages:     item.SplitComma(f.Languages),
		Subtitles:     item.SplitComma(f.Subtitles),
		Countries:     item.SplitComma(f.Countries),
		Runtime:       num("runtime", f.Runtime),
		Region:        f.Region,
		VideoStandard: f.VideoStandard,
		IsTV:          f.IsTV,
		Seasons:       f.Seasons,
		AudioFormat:   f.AudioFormat,
		VideoFormat:   f.VideoFormat,
		Publisher:     f.Publisher,
		Pages:         num("pages", f.Pages),
		Binding:       f.Binding,
		FileFormat:    f.FileFormat,
		FilePath:      f.FilePath,
	}
	if strings.TrimSpace(f.AgeLimit) != "" {
		n := num("age_limit", f.AgeLimit)
		it.AgeLimit = &n
	}
	// Map iteration order is random in Go, so go through roles and types in
	// a fixed order to keep credits and identifiers stable.
	for _, role := range sortedKeys(f.Credits) {
		for _, name := range item.SplitLines(f.Credits[role]) {
			it.Credits = append(it.Credits, item.Credit{Role: role, Name: name})
		}
	}
	for _, t := range sortedKeys(f.Identifiers) {
		for _, v := range strings.Split(f.Identifiers[t], ",") {
			it.Identifiers = append(it.Identifiers, item.Identifier{Type: t, Value: v})
		}
	}
	return it, errs
}

func sortedKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	return keys
}

// identifierTypes returns the identifier inputs a form shows: the usual
// ones for the kind, plus any other type the item already has, so editing
// never drops one.
func identifierTypes(kind item.Kind, it *item.Item) []string {
	types := []string{item.IDEan, item.IDImdb, item.IDTmdb}
	if kind == item.KindBook {
		types = []string{item.IDIsbn, item.IDEan}
	}
	if it != nil {
		for _, id := range it.Identifiers {
			if !slices.Contains(types, id.Type) {
				types = append(types, id.Type)
			}
		}
	}
	return types
}

// Labels and links used by the templates (see templateFuncs).

var roleLabels = map[string]string{
	item.RoleDirector: "Directors",
	item.RoleCast:     "Cast",
	item.RoleWriter:   "Writers",
	item.RoleProducer: "Producers",
	item.RoleComposer: "Music",
	item.RoleAuthor:   "Authors",
}

var identifierLabels = map[string]string{
	item.IDIsbn: "ISBN",
	item.IDEan:  "EAN / UPC barcode",
	item.IDImdb: "IMDb",
	item.IDTmdb: "TMDb",
}

func label(m map[string]string, k string) string {
	if l, ok := m[k]; ok {
		return l
	}
	return k
}

// identifierURL links IMDb and TMDb IDs to their pages; others get "".
func identifierURL(id item.Identifier) string {
	switch id.Type {
	case item.IDImdb:
		return "https://www.imdb.com/title/" + url.PathEscape(id.Value) + "/"
	case item.IDTmdb:
		return "https://www.themoviedb.org/" + id.Value // stored as movie/603 or tv/1399
	}
	return ""
}
