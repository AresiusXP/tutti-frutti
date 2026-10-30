package api

// Content-safety bodies (docs/DESIGN.md, "Safety"). The domain rules live in
// internal/safety; this file is only the wire contract (and the vocabulary the
// web client needs: report kinds and reasons).

// TermsStatus is what the client needs to decide whether to show the terms
// gate: the version the server requires, the one this guest accepted (null
// when none) and whether the guest still has to accept. It is part of
// GET /api/session and the answer of POST /api/terms/accept.
type TermsStatus struct {
	// RequiredVersion is the current version of the terms (a deploy changes it).
	RequiredVersion string `json:"requiredVersion"`
	// AcceptedVersion is the version this guest accepted, or null.
	AcceptedVersion *string `json:"acceptedVersion" tstype:"string | null,required"`
	// AcceptanceRequired is true until the guest accepted RequiredVersion.
	// Creating or joining a room and uploading a profile answer 403
	// terms_required while it is true; reporting, blocking, leaving, signing
	// out and deleting the account never need the acceptance.
	AcceptanceRequired bool `json:"acceptanceRequired"`
}

// AcceptTermsRequest is the body of POST /api/terms/accept. The guest is the
// one of the tf_guest cookie; the time is the server's. No birth date is
// collected: only the confirmation of being 13 or older.
type AcceptTermsRequest struct {
	// Version must equal TermsStatus.RequiredVersion.
	Version string `json:"version"`
	// Locale is the screen language the terms were shown in: es, en or nl.
	Locale string `json:"locale"`
	// Age13Confirmed must be true.
	Age13Confirmed bool `json:"age13Confirmed"`
}

// Kinds of reportable content (ReportRequest.Kind).
const (
	// ReportKindPlayer reports a player's nickname or avatar.
	ReportKindPlayer = "player"
	// ReportKindCustomLabel reports a custom category label.
	ReportKindCustomLabel = "custom_label"
	// ReportKindAnswer reports a player's answer.
	ReportKindAnswer = "answer"
	// ReportKindAIVerdict reports an AI judge verdict or its reason.
	ReportKindAIVerdict = "ai_verdict"
)

// Reasons of a report (ReportRequest.Reason).
const (
	ReportReasonHarassment    = "harassment"
	ReportReasonHate          = "hate"
	ReportReasonSexual        = "sexual"
	ReportReasonViolence      = "violence"
	ReportReasonSelfHarm      = "self_harm"
	ReportReasonSpam          = "spam"
	ReportReasonImpersonation = "impersonation"
	ReportReasonOther         = "other"
)

// REST error codes of the safety endpoints (the body is {"error":"<code>"}).
const (
	// ErrCodeTermsRequired (403): the guest has not accepted the current terms.
	// The same code is sent as a WebSocket error frame when "join" is refused.
	ErrCodeTermsRequired = "terms_required"
	// ErrCodeTermsVersionMismatch (409): the client accepted another version.
	ErrCodeTermsVersionMismatch = "terms_version_mismatch"
	// ErrCodeAgeConfirmationRequired (400): age13Confirmed is not true.
	ErrCodeAgeConfirmationRequired = "age_confirmation_required"
	// ErrCodeInvalidLocale (400): the locale is not es, en or nl.
	ErrCodeInvalidLocale = "invalid_locale"
	// ErrCodeInvalidReport (400): the report is malformed (unknown kind or
	// reason, missing reference fields, bad idempotency key, comment too long).
	ErrCodeInvalidReport = "invalid_report"
	// ErrCodeNotInRoom (403): the room exists but the reporter is not, and was
	// not, one of its players.
	ErrCodeNotInRoom = "not_in_room"
)

// ReportRef says which content a report is about, in the vocabulary of the room.
// The fields a kind needs: player -> playerId; custom_label -> category + label
// (the label the reporter saw); answer -> round + category + playerId (the
// author); ai_verdict -> round + category + playerId (whose answer was judged).
// The server resolves it against the room; nothing in it is trusted as a target.
type ReportRef struct {
	PlayerID string `json:"playerId,omitempty"`
	Category *int   `json:"category,omitempty"`
	Label    string `json:"label,omitempty"`
	Round    *int   `json:"round,omitempty"`
}

// ReportRequest is the body of POST /api/reports.
type ReportRequest struct {
	Kind string `json:"kind"`
	// RoomInstanceID is the State.roomInstanceId the reporter saw. Optional;
	// when it differs from the room that now has Code, the room is treated as
	// gone.
	RoomInstanceID string `json:"roomInstanceId,omitempty"`
	// Code is the room code, used to find the room while it still exists.
	Code string    `json:"code,omitempty"`
	Ref  ReportRef `json:"ref"`
	// Reason is one of the ReportReason* codes.
	Reason string `json:"reason"`
	// Comment is optional free text, at most 280 characters.
	Comment string `json:"comment,omitempty"`
	// IdempotencyKey (8 to 64 characters of letters, digits, '-', '_') makes a
	// retry return the original report id.
	IdempotencyKey string `json:"idempotencyKey"`
	// ObservedText is the text the client saw, at most 200 characters. It is
	// only stored (as uncorroborated client evidence) when the room instance is
	// gone or the content changed.
	ObservedText string `json:"observedText,omitempty"`
}

// ReportResponse is the body of the 201 answer of POST /api/reports: the report
// exists durably.
type ReportResponse struct {
	ID string `json:"id"`
}

// BlockRequest is the body of POST /api/blocks ("Block and leave"). The blocker
// is the guest of the tf_guest cookie; the target is a player id seen in a
// room (a player id is the guest id of that player). Blocking never needs the
// terms acceptance, and nothing is said to the blocked player.
type BlockRequest struct {
	// PlayerID is the player to block (not the caller, not a bot).
	PlayerID string `json:"playerId"`
	// Name is how the blocker saw that player (nickname), at most 40
	// characters, only used to label the blocker's own block list.
	Name string `json:"name,omitempty"`
}

// BlockedPlayer is one entry of the blocker's own block list.
type BlockedPlayer struct {
	PlayerID string `json:"playerId"`
	// Name is the nickname the blocker saw when blocking.
	Name string `json:"name"`
	// BlockedAt is when the block was made, in Unix milliseconds.
	BlockedAt int64 `json:"blockedAt"`
}

// BlockResponse is the body of the 200 answer of POST /api/blocks. It is only
// sent after the block is stored and enforced in every live room.
type BlockResponse struct {
	Block BlockedPlayer `json:"block"`
	// LeftRooms are the codes of the live rooms the blocker was removed from
	// because the blocked player was in them (empty when none).
	LeftRooms []string `json:"leftRooms" tstype:"string[],required"`
}

// BlocksResponse is the body of GET /api/blocks: the caller's own blocks,
// newest first.
type BlocksResponse struct {
	Blocks []BlockedPlayer `json:"blocks" tstype:"BlockedPlayer[],required"`
}

// REST and WebSocket error codes of blocking and moderation restrictions.
const (
	// ErrCodeInvalidBlock (400): the target is the caller, missing or malformed.
	ErrCodeInvalidBlock = "invalid_block"
	// ErrCodeBlockLimit (409): the guest already has the maximum number of blocks.
	ErrCodeBlockLimit = "block_limit"
	// ErrCodeJoinUnavailable (403 on REST, an error frame on the socket): the
	// room cannot be created or joined by this guest for a safety reason (a
	// block between the guest and a player of the room, or a moderation
	// restriction). It is deliberately generic: it never says who or why.
	ErrCodeJoinUnavailable = "join_unavailable"
)
