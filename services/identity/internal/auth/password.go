// Package auth implements credential handling and session lifecycle.
package auth

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"runtime"
	"strings"
	"unicode"

	"golang.org/x/crypto/argon2"
)

// Argon2id parameters.
//
// Chosen to cost roughly 50–100ms on server hardware: slow enough that an
// offline attack on a stolen hash is expensive, fast enough that sign-in stays
// responsive. The parameters are encoded into every hash, so they can be raised
// later without invalidating existing passwords.
const (
	argonTime    uint32 = 3
	argonMemory  uint32 = 64 * 1024 // 64 MiB
	argonKeyLen  uint32 = 32
	argonSaltLen        = 16
)

var (
	ErrPasswordMismatch = errors.New("password does not match")
	ErrHashMalformed    = errors.New("password hash is malformed")
)

// HashPassword derives an Argon2id hash in PHC string format.
func HashPassword(password string) (string, error) {
	salt := make([]byte, argonSaltLen)
	if _, err := rand.Read(salt); err != nil {
		return "", fmt.Errorf("auth: generate salt: %w", err)
	}

	threads := uint8(runtime.NumCPU())
	if threads < 1 {
		threads = 1
	}
	if threads > 4 {
		threads = 4
	}

	digest := argon2.IDKey([]byte(password), salt, argonTime, argonMemory, threads, argonKeyLen)

	return fmt.Sprintf("$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s",
		argon2.Version, argonMemory, argonTime, threads,
		base64.RawStdEncoding.EncodeToString(salt),
		base64.RawStdEncoding.EncodeToString(digest),
	), nil
}

// VerifyPassword checks a password against a stored hash.
//
// Comparison is constant-time so the duration of a failed sign-in does not leak
// how much of the hash matched.
func VerifyPassword(password, encoded string) error {
	parts := strings.Split(encoded, "$")
	if len(parts) != 6 || parts[1] != "argon2id" {
		return ErrHashMalformed
	}

	var version int
	if _, err := fmt.Sscanf(parts[2], "v=%d", &version); err != nil {
		return ErrHashMalformed
	}
	if version != argon2.Version {
		return ErrHashMalformed
	}

	var memory, time uint32
	var threads uint8
	if _, err := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &memory, &time, &threads); err != nil {
		return ErrHashMalformed
	}

	salt, err := base64.RawStdEncoding.DecodeString(parts[4])
	if err != nil {
		return ErrHashMalformed
	}
	expected, err := base64.RawStdEncoding.DecodeString(parts[5])
	if err != nil {
		return ErrHashMalformed
	}

	actual := argon2.IDKey([]byte(password), salt, time, memory, threads, uint32(len(expected)))

	if subtle.ConstantTimeCompare(actual, expected) != 1 {
		return ErrPasswordMismatch
	}
	return nil
}

// PasswordPolicy describes the minimum strength the platform accepts.
type PasswordPolicy struct {
	MinLength      int
	RequireUpper   bool
	RequireLower   bool
	RequireDigit   bool
	RequireSpecial bool
}

// DefaultPasswordPolicy is applied to every realm.
func DefaultPasswordPolicy() PasswordPolicy {
	return PasswordPolicy{
		MinLength:    12,
		RequireUpper: true,
		RequireLower: true,
		RequireDigit: true,
	}
}

// Validate reports every way a password fails the policy, so a form can show
// all of the problems at once instead of one per submission.
func (p PasswordPolicy) Validate(password string) []string {
	var problems []string

	if len([]rune(password)) < p.MinLength {
		problems = append(problems, fmt.Sprintf("must be at least %d characters long", p.MinLength))
	}

	var hasUpper, hasLower, hasDigit, hasSpecial bool
	for _, r := range password {
		switch {
		case unicode.IsUpper(r):
			hasUpper = true
		case unicode.IsLower(r):
			hasLower = true
		case unicode.IsDigit(r):
			hasDigit = true
		case unicode.IsPunct(r) || unicode.IsSymbol(r):
			hasSpecial = true
		}
	}

	if p.RequireUpper && !hasUpper {
		problems = append(problems, "must contain an uppercase letter")
	}
	if p.RequireLower && !hasLower {
		problems = append(problems, "must contain a lowercase letter")
	}
	if p.RequireDigit && !hasDigit {
		problems = append(problems, "must contain a digit")
	}
	if p.RequireSpecial && !hasSpecial {
		problems = append(problems, "must contain a special character")
	}

	return problems
}

// DummyHash is compared against when an email does not exist.
//
// Sign-in must take the same time whether or not the account is real, otherwise
// response timing reveals which emails are registered.
var DummyHash = func() string {
	hash, err := HashPassword("karmarat-timing-equalizer")
	if err != nil {
		return ""
	}
	return hash
}()
