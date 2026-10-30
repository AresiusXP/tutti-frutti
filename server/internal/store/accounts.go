package store

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/AresiusXP/tutti-frutti/server/internal/api"
)

// Errors of the Accounts methods. Both implementations return them wrapped,
// so match with errors.Is.
var (
	// ErrUserNotFound: no user with that id (never created or already deleted).
	ErrUserNotFound = errors.New("store: user not found")
	// ErrInvalidAccount: the Google subject, display name or profile cannot be
	// stored (empty, too long, not valid text, a NUL character).
	ErrInvalidAccount = errors.New("store: invalid account data")
	// ErrInvalidGame: the GameRecord cannot be stored (malformed ids, negative
	// or absurd numbers, no results). Retrying it cannot succeed.
	ErrInvalidGame = errors.New("store: invalid game record")
)

// Limits enforced identically by every implementation.
const (
	maxSubBytes     = 255 // Google: "at most 255 ASCII characters"
	maxNameBytes    = 128 // a display name is cut to 16 runes (<= 64 bytes) by the caller
	maxNicknameByte = 64  // game.MaxNameLen runes
	maxAvatarBytes  = 32  // game's avatar field limit
	maxGameStat     = 1_000_000
)

// User is an account. It carries no email address and no token: only what the
// game needs.
type User struct {
	// ID is the opaque user id (a lower-case UUID).
	ID string
	// Name is the Google display name (already cleaned by the caller).
	Name string
	// Profile is the saved nickname and avatar, nil until SaveProfile.
	Profile *api.Profile
	// CreatedAt and LastLoginAt are UTC with microsecond precision.
	CreatedAt   time.Time
	LastLoginAt time.Time
}

// UserResult is what one user got out of one finished game.
type UserResult struct {
	// UserID is the user the result belongs to.
	UserID string `json:"userId"`
	// Won is whether the user's player is one of the winners.
	Won bool `json:"won"`
	// Points is the user's final score.
	Points int `json:"points"`
	// BestRound is the user's best single-round score.
	BestRound int `json:"bestRound"`
	// UniqueAnswers is the number of 10-point answers.
	UniqueAnswers int `json:"uniqueAnswers"`
}

// GameRecord is a finished game, ready to be added to the users' statistics.
// It is also what a room keeps in its snapshot until the game is recorded, so
// its JSON form is part of the room snapshot schema.
type GameRecord struct {
	// ID identifies the finished game (a lower-case UUID): recording the same
	// (user, game) twice counts once.
	ID string `json:"id"`
	// PlayedAt is when the game ended; zero means "now".
	PlayedAt time.Time `json:"playedAt"`
	// Results has one entry per user. A user listed twice counts once (the
	// first entry wins).
	Results []UserResult `json:"results"`
}

// Validate reports whether the record can be stored (ErrInvalidGame otherwise):
// what RecordGame checks before touching anything.
func (rec GameRecord) Validate() error { return validateGame(rec) }

// Accounts is the persistence contract of users and their statistics. All
// methods must be safe for concurrent use and honour ctx cancellation.
type Accounts interface {
	// UpsertGoogleUser returns the user of a Google subject, creating it on
	// first login. Every call refreshes the display name and LastLoginAt and
	// keeps the saved profile.
	UpsertGoogleUser(ctx context.Context, sub, name string) (User, error)
	// GetUser returns the user, or ErrUserNotFound.
	GetUser(ctx context.Context, id string) (User, error)
	// SaveProfile replaces the saved profile (the caller validated it), or
	// returns ErrUserNotFound.
	SaveProfile(ctx context.Context, id string, p api.Profile) error
	// DeleteUser removes the user together with the profile, the statistics and
	// the per-game rows, or returns ErrUserNotFound.
	DeleteUser(ctx context.Context, id string) error
	// RecordGame adds a finished game to the statistics of its users, in one
	// transaction. It is idempotent per (user, game ID): a result that was
	// already recorded is ignored, so retries never double count. Users that no
	// longer exist are skipped silently.
	RecordGame(ctx context.Context, rec GameRecord) error
	// UserStats returns the lifetime statistics (all zero before the first
	// game), or ErrUserNotFound.
	UserStats(ctx context.Context, id string) (api.Stats, error)
}

// GameSkipper is implemented by both backends: RecordGameSkips is RecordGame
// that also says how many users of the record were not credited because their
// account no longer exists (deleted before or while the record was written: the
// user's row is the foreign key of every statistic, so the result is skipped, never
// retried and never creates the rows again). The statistics recorder uses it
// when it is there, to count them.
type GameSkipper interface {
	RecordGameSkips(ctx context.Context, rec GameRecord) (skipped int, err error)
}

// Backend is everything cmd/server needs from one concrete store: the room
// snapshots, the accounts and the content-safety records. *Memory and
// *Postgres implement it.
type Backend interface {
	Store
	Accounts
	Safety
}

// NewID returns a new random identifier: a version 4 UUID, the form of every
// user and game id.
func NewID() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	b[6] = b[6]&0x0f | 0x40
	b[8] = b[8]&0x3f | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

// ValidID reports whether s is a canonical lower-case UUID, the form of every
// user and game id.
func ValidID(s string) bool {
	if len(s) != 36 {
		return false
	}
	for i := range len(s) {
		switch c := s[i]; {
		case i == 8 || i == 13 || i == 18 || i == 23:
			if c != '-' {
				return false
			}
		case c >= '0' && c <= '9', c >= 'a' && c <= 'f':
		default:
			return false
		}
	}
	return true
}

// plainText reports whether s is valid UTF-8 without control characters (NUL
// above all: PostgreSQL text cannot hold it).
func plainText(s string) bool {
	if !utf8.ValidString(s) {
		return false
	}
	return !strings.ContainsFunc(s, func(r rune) bool { return r < 0x20 || r == 0x7f })
}

func validateUser(sub, name string) error {
	switch {
	case sub == "" || len(sub) > maxSubBytes || !plainText(sub):
		return fmt.Errorf("%w: bad Google subject", ErrInvalidAccount)
	case len(name) > maxNameBytes || !plainText(name):
		return fmt.Errorf("%w: bad display name", ErrInvalidAccount)
	}
	return nil
}

func validateProfile(p api.Profile) error {
	switch {
	case p.Nickname == "" || len(p.Nickname) > maxNicknameByte || !plainText(p.Nickname):
		return fmt.Errorf("%w: bad nickname", ErrInvalidAccount)
	case p.Avatar.Emoji == "" || len(p.Avatar.Emoji) > maxAvatarBytes || !plainText(p.Avatar.Emoji),
		p.Avatar.Color == "" || len(p.Avatar.Color) > maxAvatarBytes || !plainText(p.Avatar.Color):
		return fmt.Errorf("%w: bad avatar", ErrInvalidAccount)
	}
	return nil
}

func validateGame(rec GameRecord) error {
	if !ValidID(rec.ID) {
		return fmt.Errorf("%w: bad game id", ErrInvalidGame)
	}
	if len(rec.Results) == 0 {
		return fmt.Errorf("%w: no results", ErrInvalidGame)
	}
	for _, r := range rec.Results {
		switch {
		case !ValidID(r.UserID):
			return fmt.Errorf("%w: bad user id", ErrInvalidGame)
		case r.Points < 0 || r.Points > maxGameStat,
			r.BestRound < 0 || r.BestRound > maxGameStat,
			r.UniqueAnswers < 0 || r.UniqueAnswers > maxGameStat:
			return fmt.Errorf("%w: statistic out of range", ErrInvalidGame)
		}
	}
	return nil
}

// firstPerUser returns the results of rec without repeated users (the first
// entry of a user wins), in the order of their first appearance.
func firstPerUser(results []UserResult) []UserResult {
	seen := make(map[string]struct{}, len(results))
	out := make([]UserResult, 0, len(results))
	for _, r := range results {
		if _, dup := seen[r.UserID]; dup {
			continue
		}
		seen[r.UserID] = struct{}{}
		out = append(out, r)
	}
	return out
}

// addResult folds one game into stats (what both implementations apply once
// per (user, game)).
func addResult(s api.Stats, r UserResult) api.Stats {
	s.GamesPlayed++
	if r.Won {
		s.Wins++
	}
	s.TotalPoints += r.Points
	s.BestRound = max(s.BestRound, r.BestRound)
	s.UniqueAnswers += r.UniqueAnswers
	return s
}
