package safety

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"time"
	"unicode/utf8"

	"github.com/AresiusXP/tutti-frutti/server/internal/api"
	"github.com/AresiusXP/tutti-frutti/server/internal/game"
)

// Kinds of content a player can report (the wire values live in internal/api
// so the web client gets them generated).
const (
	// KindPlayer: a player's presentation (nickname and avatar).
	KindPlayer = api.ReportKindPlayer
	// KindCustomLabel: a custom category label written by a host.
	KindCustomLabel = api.ReportKindCustomLabel
	// KindAnswer: a player's answer in a category.
	KindAnswer = api.ReportKindAnswer
	// KindAIVerdict: the AI judge's verdict (and reason) about an answer.
	KindAIVerdict = api.ReportKindAIVerdict
)

// Kinds lists every report kind.
var Kinds = []string{KindPlayer, KindCustomLabel, KindAnswer, KindAIVerdict}

// Reasons of a report (fixed codes; the client translates them).
const (
	ReasonHarassment    = api.ReportReasonHarassment
	ReasonHate          = api.ReportReasonHate
	ReasonSexual        = api.ReportReasonSexual
	ReasonViolence      = api.ReportReasonViolence
	ReasonSelfHarm      = api.ReportReasonSelfHarm
	ReasonSpam          = api.ReportReasonSpam
	ReasonImpersonation = api.ReportReasonImpersonation
	ReasonOther         = api.ReportReasonOther
)

// Reasons lists every report reason.
var Reasons = []string{
	ReasonHarassment, ReasonHate, ReasonSexual, ReasonViolence,
	ReasonSelfHarm, ReasonSpam, ReasonImpersonation, ReasonOther,
}

// Statuses of a report. A new report is open; a moderator closes it as
// actioned (the content or the player was acted on) or dismissed.
const (
	StatusOpen      = "open"
	StatusActioned  = "actioned"
	StatusDismissed = "dismissed"
)

// Statuses lists every report status.
var Statuses = []string{StatusOpen, StatusActioned, StatusDismissed}

// Limits of a report.
const (
	// MaxCommentRunes bounds the free-text comment of a report.
	MaxCommentRunes = 280
	// MaxObservedRunes bounds the text a client says it saw (only used when the
	// room is gone).
	MaxObservedRunes = 200
	// MaxIDLen bounds the identifiers a client sends (idempotency key, instance
	// ids, player ids).
	MaxIDLen = 64
	// MinKeyLen is the shortest accepted idempotency key.
	MinKeyLen = 8
)

// Retention of reports: a report is deleted 90 days after it was filed at the
// latest, and 30 days after a moderator resolved it when that comes first.
const (
	MaxRetention      = 90 * 24 * time.Hour
	ResolvedRetention = 30 * 24 * time.Hour
)

// PurgeAt is when a report has to be deleted: created + 90 days, or resolved +
// 30 days when that is earlier. A zero resolved means the report is open.
func PurgeAt(created, resolved time.Time) time.Time {
	at := created.Add(MaxRetention)
	if !resolved.IsZero() {
		if r := resolved.Add(ResolvedRetention); r.Before(at) {
			at = r
		}
	}
	return at
}

// Expired reports whether a report with this purge time has to be deleted at
// now (the purge job's predicate; the store applies it in SQL).
func Expired(purgeAt, now time.Time) bool { return !now.Before(purgeAt) }

// ValidKind, ValidReason and ValidStatus report whether s is a known code.
func ValidKind(s string) bool   { return contains(Kinds, s) }
func ValidReason(s string) bool { return contains(Reasons, s) }
func ValidStatus(s string) bool { return contains(Statuses, s) }

func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

// ValidToken reports whether s is a plausible identifier: 1 to MaxIDLen of
// letters, digits, '-', '_' and ':' (guest ids are base64url, automated seats
// are "bot:<name>", instance ids are UUIDs).
func ValidToken(s string) bool {
	if s == "" || len(s) > MaxIDLen {
		return false
	}
	for i := range len(s) {
		c := s[i]
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9', c == '-', c == '_', c == ':':
		default:
			return false
		}
	}
	return true
}

// Ref says which content a report is about, in the vocabulary of the room (no
// database ids). The fields a kind needs are:
//
//	player        PlayerID
//	custom_label  Category + Label (the label the reporter saw)
//	answer        Round + Category + PlayerID (the author)
//	ai_verdict    Round + Category + PlayerID (whose answer was judged)
//
// Nothing in a Ref is trusted as a target: the room resolves it against its own
// state, and the label is only compared with the server's copy.
type Ref struct {
	PlayerID string `json:"playerId,omitempty"`
	Category *int   `json:"category,omitempty"`
	Label    string `json:"label,omitempty"`
	Round    *int   `json:"round,omitempty"`
}

// ErrInvalidRef: the ref does not have the fields its kind needs.
var ErrInvalidRef = errors.New("safety: invalid report reference")

// Validate checks that r has what kind needs, with sane bounds.
func (r Ref) Validate(kind string) error {
	validCat := func() bool { return r.Category != nil && *r.Category >= 0 && *r.Category < game.MaxCategories }
	validRound := func() bool { return r.Round != nil && *r.Round >= 1 && *r.Round <= 1000 }
	switch kind {
	case KindPlayer:
		if ValidToken(r.PlayerID) {
			return nil
		}
	case KindCustomLabel:
		if validCat() && r.Label != "" && utf8.RuneCountInString(r.Label) <= 4*game.MaxCustomLen {
			return nil
		}
	case KindAnswer, KindAIVerdict:
		if validCat() && validRound() && ValidToken(r.PlayerID) {
			return nil
		}
	}
	return fmt.Errorf("%w for kind %q", ErrInvalidRef, kind)
}

// ContentKey is a stable key of the reported content inside one room instance,
// so a moderator sees all the reports about the same thing together. The label
// of a custom label is hashed into it: a label that changed is another key.
func ContentKey(kind string, r Ref) string {
	n := func(p *int) int {
		if p == nil {
			return -1
		}
		return *p
	}
	switch kind {
	case KindPlayer:
		return "player:" + r.PlayerID
	case KindCustomLabel:
		sum := sha256.Sum256([]byte(r.Label))
		return fmt.Sprintf("label:%d:%s", n(r.Category), hex.EncodeToString(sum[:4]))
	case KindAnswer:
		return fmt.Sprintf("answer:r%d:c%d:%s", n(r.Round), n(r.Category), r.PlayerID)
	case KindAIVerdict:
		return fmt.Sprintf("verdict:r%d:c%d:%s", n(r.Round), n(r.Category), r.PlayerID)
	}
	return kind
}

// EvidenceRequest is what the room needs to resolve a report. Reporter comes
// from the guest cookie; RoomInstanceID is the instance the client says it was
// in (optional: empty means "whatever the room with the code is now").
type EvidenceRequest struct {
	Reporter       string
	Kind           string
	RoomInstanceID string
	Ref            Ref
}

// Author is who submitted a piece of content: the guest id (the player id) and
// the account id the connection carried at that time, when signed in.
type Author struct {
	Guest string `json:"guest"`
	User  string `json:"user,omitempty"`
}

// Evidence sources.
const (
	// SourceServer: the text was copied by the room from its own state, from the
	// reporter's authorized view.
	SourceServer = "server"
	// SourceClient: the room is gone or the content changed; the text is what
	// the reporting client says it saw. It is uncorroborated and must never be
	// actioned automatically.
	SourceClient = "client"
)

// Evidence is the outcome of resolving a report.
type Evidence struct {
	// Verified is true when the room produced the evidence itself.
	Verified bool
	// RoomInstance and MatchInstance are the room's ids (MatchInstance may be
	// empty when unknown).
	RoomInstance  string
	MatchInstance string
	// ContentKey identifies the content (see ContentKey).
	ContentKey string
	// TargetGuest and TargetUser are the author of the content as the room
	// knows them; empty when unknown or when the subject is the AI judge or an
	// automated seat. They are never taken from the client.
	TargetGuest string
	TargetUser  string
	// Data is the evidence stored with the report.
	Data EvidenceData
}

// JSON encodes the evidence. The result is always valid JSON (invalid UTF-8
// becomes U+FFFD, control characters are escaped).
func (e Evidence) JSON() (json.RawMessage, error) { return json.Marshal(e.Data) }

// EvidenceData is the JSON stored in reports.evidence_json. Texts are stored as
// the server saw them (raw, never HTML-escaped or truncated by the store); they
// are data for a moderator, never markup.
type EvidenceData struct {
	Source string `json:"source"`
	Lang   string `json:"lang,omitempty"`
	Phase  string `json:"phase,omitempty"`
	Round  int    `json:"round,omitempty"`
	Letter string `json:"letter,omitempty"`
	// Category is the category the content belongs to (answers, verdicts and
	// custom labels).
	Category *EvidenceCategory `json:"category,omitempty"`
	// Player is the reported player (kind player).
	Player *EvidencePlayer `json:"player,omitempty"`
	// Text is the label or the answer exactly as the server holds it.
	Text string `json:"text,omitempty"`
	// Author is who submitted the content, when known.
	Author *Author `json:"author,omitempty"`
	// Automated is true when the content belongs to an automated seat.
	Automated bool `json:"automated,omitempty"`
	// Verdict is the AI verdict as shown to the reporter (kind ai_verdict).
	Verdict *EvidenceVerdict `json:"verdict,omitempty"`
	// Claimed and ObservedText are only set with SourceClient: the reference
	// and the text the client sent. Uncorroborated.
	Claimed      *Ref   `json:"claimed,omitempty"`
	ObservedText string `json:"observedText,omitempty"`
}

// EvidenceCategory is a category of the room.
type EvidenceCategory struct {
	Index  int    `json:"index"`
	Label  string `json:"label"`
	Custom bool   `json:"custom,omitempty"`
}

// EvidencePlayer is a player's presentation.
type EvidencePlayer struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Emoji string `json:"emoji"`
	Color string `json:"color"`
	Left  bool   `json:"left,omitempty"`
}

// EvidenceVerdict is an AI verdict with the answer it judged.
type EvidenceVerdict struct {
	Value  string `json:"value"`
	Reason string `json:"reason,omitempty"`
	Answer string `json:"answer"`
}

// Report is a stored content report. There are no foreign keys to rooms or
// accounts: a report survives the deletion of the room and of the accounts it
// names (TargetUser and ReporterUser are plain ids, see the account deletion
// notes in docs/DESIGN.md).
type Report struct {
	ID               string
	ReporterGuest    string
	ReporterUser     string // "" for a guest
	RoomInstance     string // "" when the client did not say and the room is gone
	MatchInstance    string // "" when unknown
	ContentKey       string
	Kind             string
	TargetGuest      string // "" when unknown (never client-supplied when the room exists)
	TargetUser       string
	Evidence         json.RawMessage
	EvidenceVerified bool
	Reason           string
	Comment          string
	Status           string
	IdempotencyKey   string
	CreatedAt        time.Time
	ResolvedAt       time.Time // zero while open
	PurgeAt          time.Time
}
