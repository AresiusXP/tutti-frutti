// Package safety holds the user-generated-content safety rules of the game
// (docs/DESIGN.md, "Safety"): the version of the terms players accept, the
// gate that checks the acceptance, and the vocabulary and retention rules of
// content reports. It is pure domain code: no database access (it talks to
// whatever implements TermsStore), no HTTP and no room state, so the store,
// the room layer and the REST handlers can all depend on it.
package safety

import (
	"context"
	"errors"
	"sync"
	"time"
)

// TermsVersion is the version of the terms and community rules a player has to
// accept before creating or joining a room or uploading a profile. Changing it
// is a deploy: every guest has to accept again (the gate keeps accepted guests
// in memory only for the life of the process).
const TermsVersion = "2026-10"

// AcceptanceTTL is how long an acceptance counts: the privacy page promises it
// is kept up to 365 days and then the guest is asked again. Past it the gate
// treats the guest as not having accepted (whatever is still in the table), and
// accepting again replaces the old row; a daily job deletes the old rows.
const AcceptanceTTL = 365 * 24 * time.Hour

// AcceptanceExpired reports whether an acceptance made at acceptedAt no longer
// counts at now.
func AcceptanceExpired(acceptedAt, now time.Time) bool {
	return !now.Before(acceptedAt.Add(AcceptanceTTL))
}

// AcceptancePurgeBefore is the cut-off of the acceptance purge: acceptances
// made before it are deleted.
func AcceptancePurgeBefore(now time.Time) time.Time { return now.Add(-AcceptanceTTL) }

// Locales are the screen languages a player can accept the terms in; the
// accepted locale is stored as evidence of what the player was shown.
var Locales = []string{"es", "en", "nl"}

// ValidLocale reports whether locale is one of Locales.
func ValidLocale(locale string) bool {
	for _, l := range Locales {
		if l == locale {
			return true
		}
	}
	return false
}

// Errors of AcceptInput.Validate (and Terms.Accept).
var (
	// ErrVersionMismatch: the client accepted another version than the one the
	// server requires (a cached client from before a deploy).
	ErrVersionMismatch = errors.New("safety: terms version mismatch")
	// ErrInvalidLocale: the locale is not one of Locales.
	ErrInvalidLocale = errors.New("safety: invalid locale")
	// ErrAgeNotConfirmed: the player did not confirm to be 13 or older.
	ErrAgeNotConfirmed = errors.New("safety: age confirmation missing")
	// ErrNoGuest: there is no guest identity to attach the acceptance to.
	ErrNoGuest = errors.New("safety: no guest identity")
)

// TermsAcceptance is the record that a guest accepted one version of the
// terms. The guest id is the player id of the signed guest cookie; no birth
// date is ever collected, only the confirmation of being 13 or older.
type TermsAcceptance struct {
	GuestID        string
	Version        string
	AcceptedAt     time.Time
	Locale         string
	Age13Confirmed bool
}

// AcceptInput is what a client sends to accept the terms.
type AcceptInput struct {
	Version        string
	Locale         string
	Age13Confirmed bool
}

// Validate checks the input against the version the server requires.
func (in AcceptInput) Validate(required string) error {
	switch {
	case in.Version != required:
		return ErrVersionMismatch
	case !ValidLocale(in.Locale):
		return ErrInvalidLocale
	case !in.Age13Confirmed:
		return ErrAgeNotConfirmed
	}
	return nil
}

// TermsStore is the persistence the gate needs; store.Safety implements it.
type TermsStore interface {
	// TermsAcceptance returns the guest's acceptance of version, and whether
	// there is one.
	TermsAcceptance(ctx context.Context, guestID, version string) (TermsAcceptance, bool, error)
	// AcceptTerms records an acceptance. It is idempotent: when the guest
	// already accepted that version the stored (first) record is returned
	// unchanged, unless it is older than AcceptanceTTL (it no longer counts):
	// then the new acceptance replaces it.
	AcceptTerms(ctx context.Context, a TermsAcceptance) (TermsAcceptance, error)
}

// maxCached bounds the guests remembered as having accepted; beyond it the
// cache starts again (a miss only costs one indexed read).
const maxCached = 50_000

// Terms is the terms gate: it answers whether a guest accepted the current
// version, and records acceptances. An acceptance counts for AcceptanceTTL (a
// year); positive answers are cached with the time of the acceptance, so the
// cache expires entries by the same rule as the table. A new version needs a
// deploy, so the version itself never changes under the cache; a negative or
// failed lookup is never cached. A failed lookup is an error, never "not accepted": callers
// fail closed and tell the client to retry. It is safe for concurrent use.
type Terms struct {
	store   TermsStore
	version string
	now     func() time.Time

	mu       sync.Mutex
	accepted map[string]time.Time // guest id -> when it accepted
}

// NewTerms returns a gate for TermsVersion over s. now defaults to time.Now.
func NewTerms(s TermsStore, now func() time.Time) *Terms {
	return NewTermsVersion(s, TermsVersion, now)
}

// NewTermsVersion is NewTerms for another version (tests).
func NewTermsVersion(s TermsStore, version string, now func() time.Time) *Terms {
	if now == nil {
		now = time.Now
	}
	return &Terms{store: s, version: version, now: now, accepted: map[string]time.Time{}}
}

// Version is the version the gate requires.
func (t *Terms) Version() string { return t.version }

func (t *Terms) remember(guestID string, at time.Time) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if len(t.accepted) >= maxCached {
		clear(t.accepted)
	}
	t.accepted[guestID] = at
}

// cached reports whether the guest is remembered as accepted and the acceptance
// still counts; an expired entry is dropped.
func (t *Terms) cached(guestID string) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	at, ok := t.accepted[guestID]
	if ok && AcceptanceExpired(at, t.now()) {
		delete(t.accepted, guestID)
		return false
	}
	return ok
}

// Acceptance returns the guest's acceptance of the required version; one older
// than AcceptanceTTL does not count (ok is false).
func (t *Terms) Acceptance(ctx context.Context, guestID string) (TermsAcceptance, bool, error) {
	if guestID == "" {
		return TermsAcceptance{}, false, nil
	}
	a, ok, err := t.store.TermsAcceptance(ctx, guestID, t.version)
	if err != nil {
		return TermsAcceptance{}, false, err
	}
	if ok && AcceptanceExpired(a.AcceptedAt, t.now()) {
		return TermsAcceptance{}, false, nil
	}
	if ok {
		t.remember(guestID, a.AcceptedAt)
	}
	return a, ok, nil
}

// Accepted reports whether the guest accepted the required version. An error
// means the answer is unknown.
func (t *Terms) Accepted(ctx context.Context, guestID string) (bool, error) {
	if guestID == "" {
		return false, nil
	}
	if t.cached(guestID) {
		return true, nil
	}
	_, ok, err := t.Acceptance(ctx, guestID)
	return ok, err
}

// Accept validates in and records the guest's acceptance (idempotent: the
// first acceptance of the version is kept, with its original time, until it
// expires). The time is the server's, never the client's.
func (t *Terms) Accept(ctx context.Context, guestID string, in AcceptInput) (TermsAcceptance, error) {
	if guestID == "" {
		return TermsAcceptance{}, ErrNoGuest
	}
	if err := in.Validate(t.version); err != nil {
		return TermsAcceptance{}, err
	}
	a, err := t.store.AcceptTerms(ctx, TermsAcceptance{
		GuestID:        guestID,
		Version:        t.version,
		AcceptedAt:     t.now().UTC(),
		Locale:         in.Locale,
		Age13Confirmed: true,
	})
	if err != nil {
		return TermsAcceptance{}, err
	}
	t.remember(guestID, a.AcceptedAt)
	return a, nil
}
