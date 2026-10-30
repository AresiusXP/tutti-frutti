package safety

import (
	"strings"
	"time"
)

// Limits of blocking (docs/DESIGN.md, "Safety").
const (
	// MaxBlocksPerGuest bounds the blocks one guest keeps.
	MaxBlocksPerGuest = 100
	// MaxBlockNameRunes bounds the nickname stored with a block (it only labels
	// the blocker's own list).
	MaxBlockNameRunes = 40
	// AutomatedPrefix is the prefix of the ids of automated seats; they can
	// never be blocked (they are not people).
	AutomatedPrefix = "bot:"
)

// ValidBlockTarget reports whether target is a player id one guest may block:
// a well-formed id that is neither the blocker itself nor an automated seat.
func ValidBlockTarget(blocker, target string) bool {
	return ValidToken(target) && target != blocker && !strings.HasPrefix(target, AutomatedPrefix)
}

// Block is "blocker does not want to share a room with blocked". It is keyed by
// guest id (the player id of the tf_guest cookie): clearing cookies or using
// another browser evades it, and nothing tries to prevent that. A block is
// directed (it belongs to the blocker's list) but enforced in both directions.
// A block never becomes a punishment and never expires: it lasts until the
// blocker removes it.
type Block struct {
	Blocker string
	Blocked string
	// BlockedName is the nickname the blocker saw; it only labels their list.
	BlockedName string
	CreatedAt   time.Time
}

// Restrictions: a restriction of a guest (and, when a report proved the link,
// of the account behind it) stops it from creating rooms and joining or
// reconnecting to them, and removes it from the rooms it is in.
const (
	// MaxReasonRunes bounds the operator's reason of a restriction.
	MaxReasonRunes = 200
	// MaxRestrictionDays bounds a timed restriction.
	MaxRestrictionDays = 3650
	// RestrictionRetention is how long a restriction row stays after it ended
	// (expired or revoked); an active one is never purged.
	RestrictionRetention = 90 * 24 * time.Hour
)

// Restriction is a moderation restriction of a guest identity.
type Restriction struct {
	ID string
	// GuestID is the restricted guest identity.
	GuestID string
	// UserID is the restricted account, only set when a report proved that the
	// account sat behind the guest.
	UserID string
	// Reason is the operator's reason (not shown to the player).
	Reason string
	// ReportID is the report that led to it, when there is one.
	ReportID  string
	CreatedAt time.Time
	// ExpiresAt is zero for a restriction that lasts until it is revoked.
	ExpiresAt time.Time
	// RevokedAt is zero while the restriction has not been lifted.
	RevokedAt time.Time
	Operator  string
}

// ActiveAt reports whether the restriction is in force at now.
func (r Restriction) ActiveAt(now time.Time) bool {
	if !r.RevokedAt.IsZero() {
		return false
	}
	return r.ExpiresAt.IsZero() || now.Before(r.ExpiresAt)
}

// EndedAt is when the restriction stopped being in force: the earlier of its
// expiry and its revocation; zero while it has neither.
func (r Restriction) EndedAt() time.Time {
	switch {
	case r.RevokedAt.IsZero():
		return r.ExpiresAt
	case r.ExpiresAt.IsZero(), r.RevokedAt.Before(r.ExpiresAt):
		return r.RevokedAt
	}
	return r.ExpiresAt
}

// RestrictionPurgeBefore is the cut-off of the restriction purge: a restriction
// that ended before it (more than RestrictionRetention before now) is deleted.
func RestrictionPurgeBefore(now time.Time) time.Time { return now.Add(-RestrictionRetention) }

// Moderation actions of the audit log (moderation_actions.action) and the kinds
// of target they are about (moderation_actions.target_kind).
const (
	ActionReportResolve = "report_resolve"
	ActionRestrict      = "restrict"
	ActionUnrestrict    = "unrestrict"
	ActionRoomClose     = "room_close"
	ActionProfileRemove = "profile_remove"
	ActionJudgeDisable  = "judge_disable"
	ActionJudgeEnable   = "judge_enable"
	ActionPublicDisable = "public_disable"
	ActionPublicEnable  = "public_enable"

	TargetReport = "report"
	TargetGuest  = "guest"
	TargetUser   = "user"
	TargetRoom   = "room"
	TargetJudge  = "judge"
	TargetPublic = "public_games"
)

// AuditRetention is how long a moderation_actions row is kept.
const AuditRetention = 365 * 24 * time.Hour

// AuditPurgeBefore is the cut-off of the audit purge: records older than it are
// deleted.
func AuditPurgeBefore(now time.Time) time.Time { return now.Add(-AuditRetention) }

// MaxDetailsRunes bounds the free text of an audit record.
const MaxDetailsRunes = 500

// Action is one audit record of a moderation action: who did what to which
// target. Details carry the operator's note, never player-written text.
type Action struct {
	ID         string
	At         time.Time
	Operator   string
	Action     string
	TargetKind string
	TargetID   string
	ReportID   string
	Details    string
}

// ValidOperator reports whether s is an acceptable operator name: 1 to 64
// characters of letters, digits, '.', '_', '-' and '@'. The name is what the
// operator declares on the command line; the real access boundary is Kubernetes
// exec rights (docs/MODERATION.md).
func ValidOperator(s string) bool {
	if s == "" || len(s) > MaxIDLen {
		return false
	}
	for i := range len(s) {
		c := s[i]
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9', c == '.', c == '_', c == '-', c == '@':
		default:
			return false
		}
	}
	return true
}
