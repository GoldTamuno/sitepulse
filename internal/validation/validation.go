// Package validation holds small, dependency-free validators for
// user-supplied input. We're not reaching for a full validation framework
// (e.g. go-playground/validator with struct tags) yet — for the handful of
// fields auth needs (email, password), plain functions are easier to read,
// easier to unit test in isolation, and don't require learning a tag DSL.
// If validation needs grow significantly (many DTOs, nested structs), that
// trade-off tips the other way — worth revisiting then, not now.
package validation

import (
	"errors"
	"net/mail"
	"net/url"
	"unicode"
)

var (
	ErrInvalidEmail     = errors.New("validation: invalid email address")
	ErrPasswordTooShort = errors.New("validation: password must be at least 12 characters")
	ErrPasswordTooWeak  = errors.New("validation: password must include at least 3 of: uppercase, lowercase, digit, symbol")
	ErrPasswordTooLong  = errors.New("validation: password must be under 128 characters")

	ErrInvalidURL       = errors.New("validation: URL must be a valid http:// or https:// address with a host")
	ErrInvalidName      = errors.New("validation: name must be between 1 and 100 characters")
	ErrIntervalTooShort = errors.New("validation: interval must be at least 10 seconds")
	ErrIntervalTooLong  = errors.New("validation: interval must be at most 86400 seconds (24 hours)")
	ErrTimeoutInvalid   = errors.New("validation: timeout must be between 1 and 60 seconds")
	ErrTimeoutExceedsInterval = errors.New("validation: timeout must be less than the check interval")
)

// ValidateURL requires an absolute http(s) URL with a host. We reject
// everything else explicitly rather than just calling url.Parse and moving
// on — url.Parse is lenient by design (it happily accepts relative paths,
// empty strings, or a bare "javascript:" scheme), which is correct for a
// general-purpose parser but wrong for "is this a checkable website."
// Restricting to http/https here also closes off a class of SSRF-adjacent
// mistakes (e.g. a monitor pointed at "file:///etc/passwd") before it ever
// reaches the checker worker in Phase 4.
func ValidateURL(rawURL string) error {
	u, err := url.Parse(rawURL)
	if err != nil {
		return ErrInvalidURL
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return ErrInvalidURL
	}
	if u.Host == "" {
		return ErrInvalidURL
	}
	return nil
}

func ValidateName(name string) error {
	if len(name) < 1 || len(name) > 100 {
		return ErrInvalidName
	}
	return nil
}

// ValidateInterval and ValidateTimeout mirror the CHECK constraints already
// enforced at the database level (see migrations/000002). Validating here
// too is intentional duplication, not redundancy: catching it in the
// service layer returns a clean 400 with a specific message immediately,
// instead of a raw Postgres constraint-violation error bubbling up that
// would need translating anyway. The DB constraint remains as the last
// line of defense against any code path that bypasses this validator.
func ValidateInterval(seconds int) error {
	if seconds < 10 {
		return ErrIntervalTooShort
	}
	if seconds > 86400 {
		return ErrIntervalTooLong
	}
	return nil
}

func ValidateTimeout(timeoutSeconds, intervalSeconds int) error {
	if timeoutSeconds < 1 || timeoutSeconds > 60 {
		return ErrTimeoutInvalid
	}
	if timeoutSeconds >= intervalSeconds {
		return ErrTimeoutExceedsInterval
	}
	return nil
}

// ValidateEmail uses net/mail's RFC 5322 parser rather than a hand-rolled
// regex. Email regexes are notoriously easy to get subtly wrong (either
// rejecting valid addresses or accepting garbage); the standard library
// already solved this correctly.
func ValidateEmail(email string) error {
	if _, err := mail.ParseAddress(email); err != nil {
		return ErrInvalidEmail
	}
	return nil
}

// ValidatePassword enforces a length-first policy over strict composition
// rules. NIST SP 800-63B (the modern authority on this) explicitly
// recommends favoring length over forced complexity — "P@ssw0rd1" satisfies
// most composition rules and is trivially guessable, while a 20-character
// passphrase with no digits or symbols is far stronger. We still ask for a
// little composition variety as a backstop against single-character-class
// passwords, but the primary defense is the 12-character minimum.
func ValidatePassword(password string) error {
	if len(password) < 12 {
		return ErrPasswordTooShort
	}
	if len(password) > 128 {
		return ErrPasswordTooLong
	}

	var hasUpper, hasLower, hasDigit, hasSymbol bool
	for _, r := range password {
		switch {
		case unicode.IsUpper(r):
			hasUpper = true
		case unicode.IsLower(r):
			hasLower = true
		case unicode.IsDigit(r):
			hasDigit = true
		case unicode.IsPunct(r) || unicode.IsSymbol(r):
			hasSymbol = true
		}
	}
	classes := 0
	for _, ok := range []bool{hasUpper, hasLower, hasDigit, hasSymbol} {
		if ok {
			classes++
		}
	}
	if classes < 3 {
		return ErrPasswordTooWeak
	}

	return nil
}
