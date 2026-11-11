package item

import (
	"reflect"
	"testing"
)

func TestNormaliseIdentifier(t *testing.T) {
	// A table-driven test: one struct per case, all run by the same loop.
	tests := []struct {
		typ, in, want string // want "" means an error is expected
	}{
		// ISBN-13, with and without separators.
		{IDIsbn, "978-0-306-40615-7", "9780306406157"},
		{IDIsbn, "9780306406157", "9780306406157"},
		{IDIsbn, " ISBN 978 0 306 40615 7 ", "9780306406157"},
		{IDIsbn, "ISBN:9780306406157", "9780306406157"},
		{IDIsbn, "979-10-90636-07-1", "9791090636071"},
		{IDIsbn, "9780306406158", ""}, // wrong check digit
		{IDIsbn, "9770306406157", ""}, // not 978/979
		{IDIsbn, "978030640615", ""},  // 12 digits
		{IDIsbn, "97803064061X7", ""}, // letter
		// ISBN-10 is converted to ISBN-13.
		{IDIsbn, "0-306-40615-2", "9780306406157"},
		{IDIsbn, "0-8044-2957-X", "9780804429573"},
		{IDIsbn, "0-8044-2957-x", "9780804429573"},
		{IDIsbn, "0306406153", ""}, // wrong check digit
		{IDIsbn, "X306406152", ""}, // X only allowed last
		{IDIsbn, "", ""},

		// EAN-13, and UPC-A padded to 13 digits.
		{IDEan, "4006381333931", "4006381333931"},
		{IDEan, "4 006381 333931", "4006381333931"},
		{IDEan, "036000291452", "0036000291452"},
		{IDEan, "4006381333932", ""}, // wrong check digit
		{IDEan, "036000291453", ""},  // wrong UPC check digit
		{IDEan, "40063813", ""},      // EAN-8 not supported
		{IDEan, "400638133393a", ""},

		// IMDb: an ID or a link.
		{IDImdb, "tt0133093", "tt0133093"},
		{IDImdb, "TT0133093", "tt0133093"},
		{IDImdb, "https://www.imdb.com/title/tt0133093/?ref_=fn_al_tt_1", "tt0133093"},
		{IDImdb, "https://m.imdb.com/title/tt10872600/", "tt10872600"},
		{IDImdb, "imdb.com/title/tt0133093", "tt0133093"},
		{IDImdb, "0133093", ""},
		{IDImdb, "tt123", ""},                                // too short
		{IDImdb, "https://www.imdb.com/name/nm0000206/", ""}, // a person
		{IDImdb, "att0133093", ""},

		// TMDb: films and TV share numbers, so the type is kept.
		{IDTmdb, "https://www.themoviedb.org/movie/603-the-matrix", "movie/603"},
		{IDTmdb, "https://www.themoviedb.org/tv/1399-game-of-thrones?language=sv-SE", "tv/1399"},
		{IDTmdb, "movie/603", "movie/603"},
		{IDTmdb, "TV/1399", "tv/1399"},
		{IDTmdb, "603", "movie/603"},
		{IDTmdb, "0603", "movie/603"},
		{IDTmdb, "0", ""},
		{IDTmdb, "https://www.themoviedb.org/person/6384-keanu-reeves", ""},
		{IDTmdb, "the matrix", ""},

		{"asin", "B000000000", ""}, // unknown type
	}
	for _, tt := range tests {
		got, err := NormaliseIdentifier(tt.typ, tt.in)
		if tt.want == "" {
			if err == nil {
				t.Errorf("NormaliseIdentifier(%s, %q) = %q, want error", tt.typ, tt.in, got)
			}
			continue
		}
		if err != nil || got != tt.want {
			t.Errorf("NormaliseIdentifier(%s, %q) = %q, %v; want %q", tt.typ, tt.in, got, err, tt.want)
		}
	}
}

func TestDeriveSortTitle(t *testing.T) {
	tests := map[string]string{
		"The Matrix":           "Matrix, The",
		"the matrix":           "matrix, the",
		"A Beautiful Mind":     "Beautiful Mind, A",
		"An American Tail":     "American Tail, An",
		"  The  Thing ":        "Thing, The",
		"Theory of Everything": "Theory of Everything",
		"Anaconda":             "Anaconda",
		"A":                    "A",
		"The ":                 "The",
		"Matrix":               "Matrix",
		"Den of Thieves":       "Den of Thieves", // only English articles
		"":                     "",
	}
	for in, want := range tests {
		if got := DeriveSortTitle(in); got != want {
			t.Errorf("DeriveSortTitle(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestSplitLists(t *testing.T) {
	got := SplitLines("Keanu Reeves\r\n  Laurence   Fishburne \n\nkeanu reeves\nCarrie-Anne Moss\n")
	want := []string{"Keanu Reeves", "Laurence Fishburne", "Carrie-Anne Moss"}
	if !reflect.DeepEqual(got, want) { // DeepEqual compares slices element by element
		t.Errorf("SplitLines = %q, want %q", got, want)
	}

	got = SplitComma(" Sci-Fi, Action,,sci-fi ,  Thriller")
	want = []string{"Sci-Fi", "Action", "Thriller"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("SplitComma = %q, want %q", got, want)
	}

	if got := SplitComma("  "); got != nil {
		t.Errorf("SplitComma(blank) = %q, want nil", got)
	}
}
