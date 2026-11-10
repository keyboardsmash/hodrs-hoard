// Package auth handles password hashing, login sessions and the first-run
// setup code. HTTP details (cookies, redirects) live in the server package.
package auth

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
	"sync"

	"golang.org/x/crypto/argon2"
)

// Params are the argon2id cost settings. They are stored inside every hash,
// so they can be raised later without breaking existing passwords.
type Params struct {
	Memory  uint32 // KiB
	Time    uint32 // iterations
	Threads uint8
	SaltLen uint32 // bytes
	KeyLen  uint32 // bytes
}

// DefaultParams is RFC 9106's second recommended option (64 MiB, t=3, p=4).
// It's a var, not a const, so tests can lower the cost to run faster.
var DefaultParams = Params{Memory: 64 * 1024, Time: 3, Threads: 4, SaltLen: 16, KeyLen: 32}

// ErrMalformedHash means a stored hash can't be parsed.
var ErrMalformedHash = errors.New("malformed password hash")

// b64 is the PHC string format's base64: standard alphabet, no padding.
var b64 = base64.RawStdEncoding

// HashPassword returns an argon2id hash in PHC string format:
//
//	$argon2id$v=19$m=65536,t=3,p=4$<salt>$<hash>
func HashPassword(password string) (string, error) {
	return hashWith(password, DefaultParams)
}

func hashWith(password string, p Params) (string, error) {
	salt := make([]byte, p.SaltLen)
	// crypto/rand is the OS's secure random source. (math/rand is not.)
	if _, err := rand.Read(salt); err != nil {
		return "", err
	}
	key := argon2.IDKey([]byte(password), salt, p.Time, p.Memory, p.Threads, p.KeyLen)
	return fmt.Sprintf("$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s",
		argon2.Version, p.Memory, p.Time, p.Threads, b64.EncodeToString(salt), b64.EncodeToString(key)), nil
}

// VerifyPassword reports whether password matches hash. needsRehash is true
// when the hash was made with other parameters than DefaultParams, so the
// caller can store a fresh hash after a successful login.
func VerifyPassword(password, hash string) (ok, needsRehash bool, err error) {
	p, salt, want, err := parseHash(hash)
	if err != nil {
		return false, false, err
	}
	got := argon2.IDKey([]byte(password), salt, p.Time, p.Memory, p.Threads, uint32(len(want)))
	// ConstantTimeCompare takes the same time however many bytes match, so
	// response timing doesn't leak how close a guess was.
	if subtle.ConstantTimeCompare(got, want) != 1 {
		return false, false, nil
	}
	d := DefaultParams
	needsRehash = p.Memory != d.Memory || p.Time != d.Time || p.Threads != d.Threads ||
		uint32(len(salt)) != d.SaltLen || uint32(len(want)) != d.KeyLen
	return true, needsRehash, nil
}

// parseHash splits a PHC string into its parameters, salt and key.
func parseHash(hash string) (p Params, salt, key []byte, err error) {
	// "$argon2id$v=19$m=...$salt$key" splits into ["", "argon2id", "v=19", "m=...", salt, key].
	parts := strings.Split(hash, "$")
	if len(parts) != 6 || parts[0] != "" || parts[1] != "argon2id" {
		return p, nil, nil, ErrMalformedHash
	}
	var version int
	if _, err := fmt.Sscanf(parts[2], "v=%d", &version); err != nil || version != argon2.Version {
		return p, nil, nil, ErrMalformedHash
	}
	if _, err := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &p.Memory, &p.Time, &p.Threads); err != nil {
		return p, nil, nil, ErrMalformedHash
	}
	if p.Memory == 0 || p.Time == 0 || p.Threads == 0 {
		return p, nil, nil, ErrMalformedHash
	}
	if salt, err = b64.DecodeString(parts[4]); err != nil || len(salt) == 0 {
		return p, nil, nil, ErrMalformedHash
	}
	if key, err = b64.DecodeString(parts[5]); err != nil || len(key) == 0 {
		return p, nil, nil, ErrMalformedHash
	}
	p.SaltLen, p.KeyLen = uint32(len(salt)), uint32(len(key))
	return p, salt, key, nil
}

// dummyHash is checked against when a login names an unknown user, so that
// failing takes as long as a wrong password does. Otherwise response time
// would reveal which usernames exist.
var (
	dummyOnce sync.Once // runs the function passed to Do exactly once
	dummyHash string
)

// PrepareDummy computes the dummy hash ahead of time. Call it at startup;
// otherwise the first unknown-user login is slower than the rest, which
// would give the game away.
func PrepareDummy() {
	dummyOnce.Do(func() {
		dummyHash, _ = HashPassword(rand.Text())
	})
}

// VerifyDummy spends the same time as VerifyPassword and always fails.
func VerifyDummy(password string) {
	PrepareDummy()
	VerifyPassword(password, dummyHash) //nolint:errcheck // result is irrelevant
}
