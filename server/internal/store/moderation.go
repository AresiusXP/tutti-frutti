package store

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/AresiusXP/tutti-frutti/server/internal/safety"
)

// Errors of the Moderation methods (match with errors.Is).
var (
	// ErrInvalidBlock: the block cannot be stored (retrying cannot succeed).
	ErrInvalidBlock = errors.New("store: invalid block")
	// ErrBlockLimit: the blocker already has safety.MaxBlocksPerGuest blocks.
	ErrBlockLimit = errors.New("store: block limit reached")
	// ErrInvalidModeration: a restriction or an audit record cannot be stored.
	ErrInvalidModeration = errors.New("store: invalid moderation record")
	// ErrRestrictionNotFound: no restriction with that id (never made, or purged).
	ErrRestrictionNotFound = errors.New("store: restriction not found")
	// ErrRestrictionEnded: the restriction is no longer in force (revoked or
	// expired), so there is nothing to revoke.
	ErrRestrictionEnded = errors.New("store: restriction already ended")
	// ErrAlreadyRestricted: the guest already has a restriction in force, and
	// there is at most one.
	ErrAlreadyRestricted = errors.New("store: guest already restricted")
)

// FullBackend is everything cmd/server needs from one concrete store: the
// snapshots, the accounts, the content-safety records and the moderation
// records. *Memory and *Postgres implement it.
type FullBackend interface {
	Backend
	Moderation
}

// ModerationPurge counts what PurgeModeration deleted.
type ModerationPurge struct {
	// Restrictions are the restrictions that ended more than
	// safety.RestrictionRetention ago.
	Restrictions int64
	// Actions are the audit records older than safety.AuditRetention.
	Actions int64
}

// Moderation is the persistence contract of blocks, restrictions and the audit
// log of the operator's actions. Like Safety, its methods must be safe for
// concurrent use and honour ctx; callers keep database I/O off the room actors
// and update safety.Policy only after a write returned nil.
type Moderation interface {
	// AddBlock stores the block. It is idempotent: when the pair exists nothing
	// changes and the stored block is returned with created false (the limit is
	// not checked then). A new block beyond maxBlocks for the blocker is
	// ErrBlockLimit.
	AddBlock(ctx context.Context, b safety.Block, maxBlocks int) (stored safety.Block, created bool, err error)
	// RemoveBlock deletes the block and reports whether there was one.
	RemoveBlock(ctx context.Context, blocker, blocked string) (bool, error)
	// ListBlocks returns the blocker's own blocks, newest first.
	ListBlocks(ctx context.Context, blocker string) ([]safety.Block, error)
	// LoadBlocks returns every block (startup: it fills safety.Policy).
	LoadBlocks(ctx context.Context) ([]safety.Block, error)

	// CreateRestriction stores a restriction and its audit record in one
	// transaction. There is at most ONE restriction in force per guest (not
	// revoked, not expired at r.CreatedAt): while one is, a second is
	// ErrAlreadyRestricted and nothing is stored (no audit record either), also
	// under concurrency.
	CreateRestriction(ctx context.Context, r safety.Restriction, audit safety.Action) error
	// RevokeRestriction lifts a restriction (RevokedAt = now) and stores the
	// audit record, in one transaction. ErrRestrictionNotFound when there is
	// none, ErrRestrictionEnded when it is not in force any more.
	RevokeRestriction(ctx context.Context, id string, now time.Time, audit safety.Action) (safety.Restriction, error)
	// GetRestriction returns a restriction, or ErrRestrictionNotFound.
	GetRestriction(ctx context.Context, id string) (safety.Restriction, error)
	// LoadRestrictions returns the restrictions in force at now (startup).
	LoadRestrictions(ctx context.Context, now time.Time) ([]safety.Restriction, error)

	// ResolveReportAudited closes a report (Safety.ResolveReport, including its
	// compare-and-set) and stores the audit record, in one transaction: a
	// report that is not open leaves no audit record. ErrReportNotFound when
	// there is no report, ErrReportNotOpen when it was already resolved.
	ResolveReportAudited(ctx context.Context, id, status string, now time.Time, audit safety.Action) error
	// RecordAction stores an audit record of an action that changed nothing in
	// this store (closing a room, removing a profile, the judge switch).
	RecordAction(ctx context.Context, a safety.Action) error
	// ListActions returns the newest audit records first.
	ListActions(ctx context.Context, limit int) ([]safety.Action, error)

	// ClearProfile deletes the saved profile text of an account: the nickname,
	// the avatar and the Google display name used as the default nickname. The
	// statistics stay. ErrUserNotFound when the user does not exist.
	ClearProfile(ctx context.Context, userID string) error

	// PurgeModeration deletes the restrictions that ended more than
	// safety.RestrictionRetention before now and the audit records older than
	// safety.AuditRetention.
	PurgeModeration(ctx context.Context, now time.Time) (ModerationPurge, error)
}

func validateBlock(b safety.Block) error {
	bad := func(what string) error { return fmt.Errorf("%w: %s", ErrInvalidBlock, what) }
	switch {
	case !safety.ValidToken(b.Blocker):
		return bad("blocker")
	case !safety.ValidBlockTarget(b.Blocker, b.Blocked):
		return bad("blocked")
	case utf8.RuneCountInString(b.BlockedName) > safety.MaxBlockNameRunes || !plainText(b.BlockedName):
		return bad("name")
	case b.CreatedAt.IsZero():
		return bad("time")
	}
	return nil
}

func validateRestriction(r safety.Restriction) error {
	bad := func(what string) error { return fmt.Errorf("%w: restriction %s", ErrInvalidModeration, what) }
	switch {
	case !ValidID(r.ID):
		return bad("id")
	case !safety.ValidToken(r.GuestID):
		return bad("guest")
	case r.UserID != "" && !ValidID(r.UserID):
		return bad("user")
	case r.Reason == "" || utf8.RuneCountInString(r.Reason) > safety.MaxReasonRunes || !plainText(r.Reason):
		return bad("reason")
	case r.ReportID != "" && !ValidID(r.ReportID):
		return bad("report")
	case !safety.ValidOperator(r.Operator):
		return bad("operator")
	case r.CreatedAt.IsZero():
		return bad("time")
	case !r.ExpiresAt.IsZero() && !r.ExpiresAt.After(r.CreatedAt):
		return bad("expiry")
	case !r.RevokedAt.IsZero():
		return bad("already revoked")
	}
	return nil
}

var (
	auditActions = []string{
		safety.ActionReportResolve, safety.ActionRestrict, safety.ActionUnrestrict, safety.ActionRoomClose,
		safety.ActionProfileRemove, safety.ActionJudgeDisable, safety.ActionJudgeEnable,
	}
	auditTargets = []string{safety.TargetReport, safety.TargetGuest, safety.TargetUser, safety.TargetRoom, safety.TargetJudge}
)

func validateAction(a safety.Action) error {
	bad := func(what string) error { return fmt.Errorf("%w: audit %s", ErrInvalidModeration, what) }
	switch {
	case !ValidID(a.ID):
		return bad("id")
	case a.At.IsZero():
		return bad("time")
	case !safety.ValidOperator(a.Operator):
		return bad("operator")
	case !slices.Contains(auditActions, a.Action):
		return bad("action")
	case !slices.Contains(auditTargets, a.TargetKind):
		return bad("target kind")
	case a.TargetID == "" || len(a.TargetID) > 128 || !plainText(a.TargetID):
		return bad("target")
	case a.ReportID != "" && !ValidID(a.ReportID):
		return bad("report")
	case utf8.RuneCountInString(a.Details) > safety.MaxDetailsRunes || !plainText(strings.ReplaceAll(a.Details, "\n", " ")):
		return bad("details")
	}
	return nil
}

// clampLimit bounds a listing size.
func clampLimit(n, def, upper int) int {
	if n <= 0 {
		return def
	}
	return min(n, upper)
}
