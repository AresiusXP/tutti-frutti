// Package moderation is the operator's side of the safety layer
// (docs/MODERATION.md): a control server inside the game server process that
// speaks a small JSON protocol over a Unix domain socket, the client the
// operator's command (cmd/moderate) uses, and the daily maintenance loop that
// purges what the retention rules say.
//
// All actions run in the live process, which holds the database and the room
// hub: the command line never needs database credentials and the control
// socket is the only way in (mode 0600 under the server's own user, not exposed
// by any Service or Ingress). The real access boundary is the right to
// `kubectl exec` into the server's pod.
package moderation

import (
	"encoding/json"
	"fmt"
	"time"
)

// MaxRequestBytes bounds a request of the control protocol.
const MaxRequestBytes = 8 << 10

// Paths of the control protocol. Every command is a POST with a JSON body and
// answers JSON; an error is an ErrorResponse with a non-2xx status.
const (
	PathStatus         = "/v1/status"
	PathReportsList    = "/v1/reports/list"
	PathReportsShow    = "/v1/reports/show"
	PathReportsResolve = "/v1/reports/resolve"
	PathRestrict       = "/v1/restrict"
	PathUnrestrict     = "/v1/unrestrict"
	PathRoomClose      = "/v1/room/close"
	PathProfileRemove  = "/v1/profile/remove"
	PathJudge          = "/v1/judge"
)

// Error codes of ErrorResponse.Error.
const (
	CodeBadRequest         = "bad_request"
	CodeUnknownCommand     = "unknown_command"
	CodeMethodNotAllowed   = "method_not_allowed"
	CodeTooLarge           = "request_too_large"
	CodeOperatorRequired   = "operator_required"
	CodeNotFound           = "not_found"
	CodeConflict           = "conflict"
	CodeUnavailable        = "unavailable"
	CodeInternal           = "internal"
	CodeNoVerifiedTarget   = "no_verified_target"
	CodeUserNotProven      = "user_not_proven"
	CodeAlreadyRestricted  = "already_restricted"
	CodeAlreadyEnded       = "already_ended"
	CodeAlreadyResolved    = "already_resolved"
	CodeInstanceMismatch   = "instance_mismatch"
	CodeJudgeNotConfigured = "judge_not_configured"
)

// ErrorResponse is the body of every failed command.
type ErrorResponse struct {
	Error   string `json:"error"`
	Message string `json:"message,omitempty"`
}

// APIError is an ErrorResponse as the client returns it.
type APIError struct {
	Status  int
	Code    string
	Message string
}

func (e *APIError) Error() string {
	if e.Message != "" {
		return fmt.Sprintf("%s: %s", e.Code, e.Message)
	}
	return e.Code
}

// JudgeStatus is the state of the AI judge's kill switch.
type JudgeStatus struct {
	// Configured is false when the server has no Cloudflare credentials.
	Configured bool `json:"configured"`
	// Enabled is whether the judge is on (always false when not configured).
	Enabled bool `json:"enabled"`
}

// StatusResponse answers PathStatus: what the owner checks every day.
type StatusResponse struct {
	Version string `json:"version"`
	// OpenReports is the number of reports waiting for the operator.
	OpenReports int `json:"openReports"`
	// OldestOpenAt is when the oldest open report was filed, zero when none.
	OldestOpenAt time.Time `json:"oldestOpenAt"`
	// OldestOpenAgeSeconds is its age now (0 when there is none).
	OldestOpenAgeSeconds int64 `json:"oldestOpenAgeSeconds"`
	// ActiveRestrictions is the number of restrictions in force.
	ActiveRestrictions int `json:"activeRestrictions"`
	// Blocks is the total number of blocks (a count, never who blocked whom).
	Blocks int         `json:"blocks"`
	Judge  JudgeStatus `json:"judge"`
}

// ReportsListRequest asks for reports. Status is open (default), resolved or all.
type ReportsListRequest struct {
	Status string `json:"status,omitempty"`
	Limit  int    `json:"limit,omitempty"`
}

// ReportSummary is one line of the report list. Comment is what the reporter
// typed: UNVERIFIED free text.
type ReportSummary struct {
	ID               string    `json:"id"`
	CreatedAt        time.Time `json:"createdAt"`
	Kind             string    `json:"kind"`
	Reason           string    `json:"reason"`
	Status           string    `json:"status"`
	EvidenceVerified bool      `json:"evidenceVerified"`
	ContentKey       string    `json:"contentKey"`
	HasTarget        bool      `json:"hasTarget"`
	Comment          string    `json:"comment"`
}

// ReportsListResponse answers PathReportsList.
type ReportsListResponse struct {
	Reports []ReportSummary `json:"reports"`
}

// ReportRef names a report.
type ReportRef struct {
	ID string `json:"id"`
}

// ReportView is a whole report for the operator: the evidence the room copied
// (or, when EvidenceVerified is false, what the client says it saw, which is
// UNVERIFIED), the provenance ids and the reporter's own words (UNVERIFIED).
type ReportView struct {
	ID               string          `json:"id"`
	Status           string          `json:"status"`
	Kind             string          `json:"kind"`
	Reason           string          `json:"reason"`
	CreatedAt        time.Time       `json:"createdAt"`
	ResolvedAt       time.Time       `json:"resolvedAt"`
	PurgeAt          time.Time       `json:"purgeAt"`
	EvidenceVerified bool            `json:"evidenceVerified"`
	ContentKey       string          `json:"contentKey"`
	RoomInstance     string          `json:"roomInstance"`
	MatchInstance    string          `json:"matchInstance"`
	ReporterGuest    string          `json:"reporterGuest"`
	ReporterUser     string          `json:"reporterUser"`
	TargetGuest      string          `json:"targetGuest"`
	TargetUser       string          `json:"targetUser"`
	Comment          string          `json:"comment"`
	Evidence         json.RawMessage `json:"evidence"`
}

// ResolveRequest resolves a report: actioned by default, dismissed with Dismiss.
type ResolveRequest struct {
	Operator string `json:"operator"`
	ID       string `json:"id"`
	Note     string `json:"note"`
	Dismiss  bool   `json:"dismiss,omitempty"`
}

// ResolveResponse answers PathReportsResolve.
type ResolveResponse struct {
	ID     string `json:"id"`
	Status string `json:"status"`
	// Warning is set when the action happened but something around it did not
	// (the audit record).
	Warning string `json:"warning,omitempty"`
}

// RestrictRequest restricts a guest identity (and, with a report that proves
// the link, the account behind it). Exactly one of GuestID and ReportID names
// the target; Days 0 lasts until it is lifted.
type RestrictRequest struct {
	Operator string `json:"operator"`
	GuestID  string `json:"guestId,omitempty"`
	ReportID string `json:"reportId,omitempty"`
	UserID   string `json:"userId,omitempty"`
	Reason   string `json:"reason"`
	Days     int    `json:"days,omitempty"`
}

// RestrictResponse answers PathRestrict.
type RestrictResponse struct {
	ID        string    `json:"id"`
	GuestID   string    `json:"guestId"`
	UserID    string    `json:"userId,omitempty"`
	ExpiresAt time.Time `json:"expiresAt"`
	// RemovedFromRooms counts the live rooms the sweep removed a player from:
	// the restricted guest, or the account's seat.
	RemovedFromRooms int    `json:"removedFromRooms"`
	Warning          string `json:"warning,omitempty"`
}

// UnrestrictRequest lifts a restriction.
type UnrestrictRequest struct {
	Operator string `json:"operator"`
	ID       string `json:"id"`
}

// UnrestrictResponse answers PathUnrestrict.
type UnrestrictResponse struct {
	ID      string `json:"id"`
	GuestID string `json:"guestId"`
}

// RoomCloseRequest closes a room instance: by Code, by InstanceID, or both (then
// the room holding the code must be that instance).
type RoomCloseRequest struct {
	Operator   string `json:"operator"`
	Code       string `json:"code,omitempty"`
	InstanceID string `json:"instanceId,omitempty"`
}

// RoomCloseResponse answers PathRoomClose.
type RoomCloseResponse struct {
	Code       string `json:"code"`
	InstanceID string `json:"instanceId"`
	Warning    string `json:"warning,omitempty"`
}

// ProfileRemoveRequest deletes the saved profile text of an account.
type ProfileRemoveRequest struct {
	Operator string `json:"operator"`
	UserID   string `json:"userId"`
}

// ProfileRemoveResponse answers PathProfileRemove.
type ProfileRemoveResponse struct {
	UserID  string `json:"userId"`
	Warning string `json:"warning,omitempty"`
}

// JudgeRequest is the AI judge's kill switch: Action is status, enable or disable.
type JudgeRequest struct {
	Operator string `json:"operator,omitempty"`
	Action   string `json:"action"`
}

// JudgeResponse answers PathJudge.
type JudgeResponse struct {
	Judge   JudgeStatus `json:"judge"`
	Warning string      `json:"warning,omitempty"`
}
