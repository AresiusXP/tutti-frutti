package store

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"
)

// ErrInvalidSnapshot is returned (wrapped, with details) by Save when the
// snapshot cannot be stored: empty or malformed code, or a state that is not
// valid JSON. Every Store implementation applies the same validation.
var ErrInvalidSnapshot = errors.New("store: invalid snapshot")

var nulEscape = []byte(`\u0000`)

// validateSnapshot enforces what every implementation accepts. The rules are
// the intersection of "valid JSON" and what a PostgreSQL text/jsonb column can
// hold, so that Memory and Postgres behave identically.
func validateSnapshot(s Snapshot) error {
	switch {
	case s.Code == "":
		return fmt.Errorf("%w: empty code", ErrInvalidSnapshot)
	case !utf8.ValidString(s.Code) || strings.ContainsRune(s.Code, 0):
		return fmt.Errorf("%w: code is not valid text", ErrInvalidSnapshot)
	case !json.Valid(s.State):
		return fmt.Errorf("%w: state of %q is not valid JSON", ErrInvalidSnapshot, s.Code)
	case !utf8.Valid(s.State):
		return fmt.Errorf("%w: state of %q is not valid UTF-8", ErrInvalidSnapshot, s.Code)
	case hasNULString(s.State):
		return fmt.Errorf("%w: state of %q contains a NUL character (unsupported by jsonb)", ErrInvalidSnapshot, s.Code)
	}
	return nil
}

// hasNULString reports whether a JSON string (value or object key) decodes to
// something containing U+0000, which PostgreSQL's jsonb rejects. The input is
// already known to be valid JSON.
func hasNULString(state []byte) bool {
	if !bytes.Contains(state, nulEscape) {
		return false
	}
	dec := json.NewDecoder(bytes.NewReader(state))
	dec.UseNumber()
	for {
		tok, err := dec.Token()
		if err != nil {
			return false
		}
		if str, ok := tok.(string); ok && strings.ContainsRune(str, 0) {
			return true
		}
	}
}

// normalizeTime brings a timestamp to what both implementations store: UTC at
// microsecond precision (the resolution of timestamptz).
func normalizeTime(t time.Time) time.Time {
	return t.UTC().Truncate(time.Microsecond)
}
