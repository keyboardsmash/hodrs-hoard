package item

import (
	"errors"
	"regexp"
	"strings"
	"unicode"
)

// This file turns what users type into the stored form: identifiers
// (spec 3.3), sort titles, keys and lists.

// Identifier types (spec 3.3).
const (
	IDImdb = "imdb"
	IDTmdb = "tmdb"
	IDIsbn = "isbn"
	IDEan  = "ean"
)

// IdentifierTypes lists the known types in display order.
var IdentifierTypes = []string{IDIsbn, IDEan, IDImdb, IDTmdb}

// Identifier is one external ID of an item.
type Identifier struct {
	Type  string
	Value string
}

var (
	errIdentifierType = errors.New("unknown identifier type")
	errDigits         = errors.New("wrong number of digits")
	errCheckDigit     = errors.New("check digit doesn't match; is it mistyped?")
	errImdb           = errors.New("not an IMDb ID (tt followed by digits) or IMDb link")
	errTmdb           = errors.New("not a TMDb link or ID like movie/603 or tv/1399")
)

// NormaliseIdentifier checks an identifier and returns it in stored form:
//
//   - isbn: 13 digits. ISBN-10 is converted. Hyphens and spaces are dropped.
//   - ean: 13 digits. A 12-digit UPC-A gets a leading 0.
//   - imdb: "tt" plus digits, taken from a pasted link if needed.
//   - tmdb: "movie/<n>" or "tv/<n>", from a link or typed; a bare number
//     is taken as a film.
func NormaliseIdentifier(typ, value string) (string, error) {
	value = strings.TrimSpace(value)
	switch typ {
	case IDIsbn:
		return normaliseISBN(value)
	case IDEan:
		return normaliseEAN(value)
	case IDImdb:
		return normaliseIMDb(value)
	case IDTmdb:
		return normaliseTMDb(value)
	}
	return "", errIdentifierType
}

// stripSeparators removes the hyphens and spaces people put in ISBNs.
func stripSeparators(s string) string {
	// strings.Map calls the function for every character; returning -1
	// drops it.
	return strings.Map(func(r rune) rune {
		if r == '-' || r == ' ' || r == ' ' {
			return -1
		}
		return r
	}, s)
}

func allDigits(s string) bool {
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return s != ""
}

func normaliseISBN(v string) (string, error) {
	v = strings.ToUpper(stripSeparators(v))
	v = strings.TrimPrefix(v, "ISBN")
	v = strings.TrimPrefix(v, ":")
	switch len(v) {
	case 10:
		// ISBN-10: nine digits and a check digit, which may be X (= 10).
		if !allDigits(v[:9]) || !(allDigits(v[9:]) || v[9] == 'X') {
			return "", errDigits
		}
		if !isbn10Valid(v) {
			return "", errCheckDigit
		}
		// ISBN-13 = "978" + the first nine digits + a new EAN check digit.
		body := "978" + v[:9]
		return body + string(eanCheckDigit(body)), nil
	case 13:
		if !allDigits(v) {
			return "", errDigits
		}
		if !strings.HasPrefix(v, "978") && !strings.HasPrefix(v, "979") {
			return "", errors.New("an ISBN-13 starts with 978 or 979")
		}
		if eanCheckDigit(v[:12]) != v[12] {
			return "", errCheckDigit
		}
		return v, nil
	}
	return "", errDigits
}

// isbn10Valid: the digits weighted 10, 9, ..., 1 must sum to a multiple of 11.
func isbn10Valid(v string) bool {
	sum := 0
	for i := 0; i < 10; i++ {
		d := int(v[i] - '0') // a byte minus '0' gives the digit's value
		if v[i] == 'X' {
			d = 10
		}
		sum += d * (10 - i)
	}
	return sum%11 == 0
}

func normaliseEAN(v string) (string, error) {
	v = stripSeparators(v)
	if !allDigits(v) {
		return "", errDigits
	}
	if len(v) == 12 {
		v = "0" + v // UPC-A is an EAN-13 with a leading zero
	}
	if len(v) != 13 {
		return "", errDigits
	}
	if eanCheckDigit(v[:12]) != v[12] {
		return "", errCheckDigit
	}
	return v, nil
}

// eanCheckDigit computes the 13th digit of an EAN-13 from the first 12:
// weights alternate 1 and 3, and the check digit tops the sum up to a
// multiple of 10.
func eanCheckDigit(first12 string) byte {
	sum := 0
	for i := 0; i < 12; i++ {
		d := int(first12[i] - '0')
		if i%2 == 1 {
			d *= 3
		}
		sum += d
	}
	return byte('0' + (10-sum%10)%10)
}

// imdbID finds "tt1234567" in a typed ID or a link like
// https://www.imdb.com/title/tt0133093/?ref_=...
var imdbID = regexp.MustCompile(`(?:^|[^a-z0-9])(tt\d{7,10})(?:$|[^0-9])`)

func normaliseIMDb(v string) (string, error) {
	m := imdbID.FindStringSubmatch(strings.ToLower(v))
	if m == nil {
		return "", errImdb
	}
	return m[1], nil
}

// tmdbLink matches https://www.themoviedb.org/movie/603-the-matrix and the
// typed form "movie/603".
var tmdbLink = regexp.MustCompile(`(?:^|themoviedb\.org/(?:[a-z]{2}(?:-[a-z]{2})?/)?)(movie|tv)/(\d+)(?:$|[^0-9])`)

func normaliseTMDb(v string) (string, error) {
	v = strings.ToLower(v)
	if allDigits(v) {
		if n := strings.TrimLeft(v, "0"); n != "" {
			return "movie/" + n, nil
		}
		return "", errTmdb
	}
	m := tmdbLink.FindStringSubmatch(v)
	if m == nil {
		return "", errTmdb
	}
	return m[1] + "/" + m[2], nil
}

// leadingArticles are moved to the end of the sort title (spec 3.2:
// English only for now).
var leadingArticles = []string{"The", "A", "An"}

// DeriveSortTitle turns "The Matrix" into "Matrix, The".
func DeriveSortTitle(title string) string {
	title = strings.TrimSpace(title)
	for _, a := range leadingArticles {
		// The article must be followed by a space and something more, so
		// "A" alone and "Anaconda" stay as they are.
		if len(title) > len(a)+1 && strings.EqualFold(title[:len(a)], a) && title[len(a)] == ' ' {
			rest := strings.TrimSpace(title[len(a)+1:])
			if rest == "" {
				break
			}
			return rest + ", " + title[:len(a)]
		}
	}
	return title
}

// key lowercases s for case-insensitive matching and sorting.
func key(s string) string {
	return strings.ToLower(strings.TrimSpace(s))
}

// cleanText trims s and collapses runs of whitespace inside one-line
// fields, so "  The   Matrix " is stored as "The Matrix".
func cleanText(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

// cleanBlock trims a multi-line field and normalises line endings.
func cleanBlock(s string) string {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	return strings.TrimSpace(s)
}

// cleanList trims every entry, drops empty ones and later duplicates
// (ignoring case), and keeps the order.
func cleanList(values []string) []string {
	var out []string
	seen := map[string]bool{}
	for _, v := range values {
		v = cleanText(v)
		k := key(v)
		if v == "" || seen[k] {
			continue
		}
		seen[k] = true
		out = append(out, v)
	}
	return out
}

// SplitLines splits a text box with one entry per line (credits, alt
// titles) into a clean list.
func SplitLines(s string) []string {
	return cleanList(strings.Split(strings.ReplaceAll(s, "\r\n", "\n"), "\n"))
}

// SplitComma splits comma-separated text (genres, languages, ...) into a
// clean list.
func SplitComma(s string) []string {
	return cleanList(strings.Split(s, ","))
}

// searchText builds the lowercased text the free-text search runs on.
func searchText(parts ...string) string {
	var b strings.Builder
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		if b.Len() > 0 {
			b.WriteByte('\n')
		}
		b.WriteString(strings.ToLower(p))
	}
	return b.String()
}

// hasControl reports whether a one-line value contains control characters
// (tabs and newlines included), which have no place in a title.
func hasControl(s string) bool {
	return strings.IndexFunc(s, unicode.IsControl) >= 0
}
