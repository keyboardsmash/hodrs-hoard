package auth

import (
	"crypto/rand"
	"crypto/subtle"
	"strings"
)

// NewSetupCode returns a one-time code for the first-run setup page, such
// as "K7QF-M2XA-P4WD". It is printed to the log, so only someone who can
// read the server's log can create the first admin.
//
// rand.Text returns 26 random characters from A-Z and 2-7 (base32). Twelve
// of them give 60 bits, far too many to guess.
func NewSetupCode() string {
	t := rand.Text()
	return t[0:4] + "-" + t[4:8] + "-" + t[8:12]
}

// SetupCodeMatches compares a typed code with the expected one. Case,
// spaces and dashes are ignored, so "k7qf m2xa p4wd" matches too.
func SetupCodeMatches(typed, want string) bool {
	if want == "" {
		return false
	}
	a, b := normaliseCode(typed), normaliseCode(want)
	return subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1
}

func normaliseCode(s string) string {
	s = strings.ToUpper(s)
	// strings.NewReplacer replaces several substrings in one pass.
	return strings.NewReplacer("-", "", " ", "").Replace(s)
}
