package store

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/AresiusXP/tutti-frutti/server/internal/safety"
)

var _ Moderation = (*Postgres)(nil)

const (
	// lockBlockerSQL takes a transaction-scoped advisory lock on a key: the
	// blocker's rows ("block:<guest>", so two concurrent blocks cannot both slip
	// under the limit) or a guest's restrictions ("restrict:<guest>").
	lockBlockerSQL = `SELECT pg_advisory_xact_lock(hashtext($1))`
	existingBlock  = `SELECT blocker_guest, blocked_guest, blocked_name, created_at FROM blocks
WHERE blocker_guest = $1 AND blocked_guest = $2`
	countBlocksSQL = `SELECT count(*) FROM blocks WHERE blocker_guest = $1`
	insertBlockSQL = `INSERT INTO blocks (blocker_guest, blocked_guest, blocked_name, created_at) VALUES ($1, $2, $3, $4)`
	deleteBlockSQL = `DELETE FROM blocks WHERE blocker_guest = $1 AND blocked_guest = $2`
	listBlocksSQL  = `SELECT blocker_guest, blocked_guest, blocked_name, created_at FROM blocks
WHERE blocker_guest = $1 ORDER BY created_at DESC, blocked_guest ASC`
	loadBlocksSQL = `SELECT blocker_guest, blocked_guest, blocked_name, created_at FROM blocks
ORDER BY created_at DESC, blocked_guest ASC`

	restrictionColumns   = `id, guest_id, user_id, reason, report_id, created_at, expires_at, revoked_at, operator`
	insertRestrictionSQL = `INSERT INTO restrictions (` + restrictionColumns + `) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)`
	getRestrictionSQL    = `SELECT ` + restrictionColumns + ` FROM restrictions WHERE id = $1`
	// activeRestrictionSQL finds a restriction of the guest in force at $2 (the
	// ActiveAt rule, and loadRestrictionsSQL's).
	activeRestrictionSQL = `SELECT id FROM restrictions
WHERE guest_id = $1 AND revoked_at IS NULL AND (expires_at IS NULL OR expires_at > $2) LIMIT 1`
	// revokeRestrictionSQL only touches a restriction that is still in force.
	revokeRestrictionSQL = `UPDATE restrictions SET revoked_at = $2
WHERE id = $1 AND revoked_at IS NULL AND (expires_at IS NULL OR expires_at > $2)
RETURNING ` + restrictionColumns
	loadRestrictionsSQL = `SELECT ` + restrictionColumns + ` FROM restrictions
WHERE revoked_at IS NULL AND (expires_at IS NULL OR expires_at > $1) ORDER BY created_at, id`

	actionColumns   = `id, at, operator, action, target_kind, target_id, report_id, details`
	insertActionSQL = `INSERT INTO moderation_actions (` + actionColumns + `) VALUES ($1, $2, $3, $4, $5, $6, $7, $8)`
	listActionsSQL  = `SELECT ` + actionColumns + ` FROM moderation_actions ORDER BY at DESC, id DESC LIMIT $1`

	clearProfileSQL = `UPDATE users SET nickname = NULL, avatar_emoji = NULL, avatar_color = NULL, name = '' WHERE id = $1`

	// An ended restriction goes 90 days after the earlier of its expiry and its
	// revocation; one with neither is in force and stays.
	purgeRestrictionsSQL = `DELETE FROM restrictions
WHERE LEAST(coalesce(expires_at, 'infinity'::timestamptz), coalesce(revoked_at, 'infinity'::timestamptz)) < $1`
	purgeActionsSQL = `DELETE FROM moderation_actions WHERE at < $1`
)

func scanBlock(row pgx.Row) (safety.Block, error) {
	var b safety.Block
	if err := row.Scan(&b.Blocker, &b.Blocked, &b.BlockedName, &b.CreatedAt); err != nil {
		return safety.Block{}, err
	}
	b.CreatedAt = b.CreatedAt.UTC()
	return b, nil
}

func (p *Postgres) queryBlocks(ctx context.Context, what, sql string, args ...any) ([]safety.Block, error) {
	rows, err := p.pool.Query(ctx, sql, args...)
	if err != nil {
		return nil, fmt.Errorf("%s%s: %w", opErrPrefix, what, err)
	}
	defer rows.Close()
	var out []safety.Block
	for rows.Next() {
		b, err := scanBlock(rows)
		if err != nil {
			return nil, fmt.Errorf("%s%s: %w", opErrPrefix, what, err)
		}
		out = append(out, b)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("%s%s: %w", opErrPrefix, what, err)
	}
	return out, nil
}

// AddBlock implements Moderation. The count and the insert run in one
// transaction under an advisory lock on the blocker, so the limit holds under
// concurrency.
func (p *Postgres) AddBlock(ctx context.Context, b safety.Block, maxBlocks int) (safety.Block, bool, error) {
	if err := validateBlock(b); err != nil {
		return safety.Block{}, false, err
	}
	b.CreatedAt = normalizeTime(b.CreatedAt)
	var (
		stored  safety.Block
		created bool
	)
	err := pgx.BeginFunc(ctx, p.pool, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, lockBlockerSQL, "block:"+b.Blocker); err != nil {
			return err
		}
		switch existing, err := scanBlock(tx.QueryRow(ctx, existingBlock, b.Blocker, b.Blocked)); {
		case err == nil:
			stored = existing
			return nil
		case !errors.Is(err, pgx.ErrNoRows):
			return err
		}
		var n int
		if err := tx.QueryRow(ctx, countBlocksSQL, b.Blocker).Scan(&n); err != nil {
			return err
		}
		if n >= maxBlocks {
			return fmt.Errorf("%w: %d", ErrBlockLimit, maxBlocks)
		}
		if _, err := tx.Exec(ctx, insertBlockSQL, b.Blocker, b.Blocked, b.BlockedName, b.CreatedAt); err != nil {
			return err
		}
		stored, created = b, true
		return nil
	})
	switch {
	case errors.Is(err, ErrBlockLimit):
		return safety.Block{}, false, err
	case err != nil:
		return safety.Block{}, false, fmt.Errorf("%sadd block: %w", opErrPrefix, err)
	}
	return stored, created, nil
}

// RemoveBlock implements Moderation.
func (p *Postgres) RemoveBlock(ctx context.Context, blocker, blocked string) (bool, error) {
	tag, err := p.pool.Exec(ctx, deleteBlockSQL, blocker, blocked)
	if err != nil {
		return false, fmt.Errorf("%sremove block: %w", opErrPrefix, err)
	}
	return tag.RowsAffected() > 0, nil
}

// ListBlocks implements Moderation.
func (p *Postgres) ListBlocks(ctx context.Context, blocker string) ([]safety.Block, error) {
	return p.queryBlocks(ctx, "list blocks", listBlocksSQL, blocker)
}

// LoadBlocks implements Moderation.
func (p *Postgres) LoadBlocks(ctx context.Context) ([]safety.Block, error) {
	return p.queryBlocks(ctx, "load blocks", loadBlocksSQL)
}

func scanRestriction(row pgx.Row) (safety.Restriction, error) {
	var (
		r                safety.Restriction
		userID, reportID *string
		created          time.Time
		expires, revoked *time.Time
		utc              = func(t *time.Time) time.Time { return t.UTC() }
	)
	if err := row.Scan(&r.ID, &r.GuestID, &userID, &r.Reason, &reportID, &created, &expires, &revoked, &r.Operator); err != nil {
		return safety.Restriction{}, err
	}
	r.UserID, r.ReportID = deref(userID), deref(reportID)
	r.CreatedAt = created.UTC()
	if expires != nil {
		r.ExpiresAt = utc(expires)
	}
	if revoked != nil {
		r.RevokedAt = utc(revoked)
	}
	return r, nil
}

func insertAction(ctx context.Context, tx pgx.Tx, a safety.Action) error {
	_, err := tx.Exec(ctx, insertActionSQL, a.ID, normalizeTime(a.At), a.Operator, a.Action, a.TargetKind, a.TargetID,
		nullable(a.ReportID), a.Details)
	return err
}

// CreateRestriction implements Moderation.
func (p *Postgres) CreateRestriction(ctx context.Context, r safety.Restriction, audit safety.Action) error {
	if err := validateRestriction(r); err != nil {
		return err
	}
	if err := validateAction(audit); err != nil {
		return err
	}
	r = normalizeRestriction(r)
	var expires any
	if !r.ExpiresAt.IsZero() {
		expires = r.ExpiresAt
	}
	err := pgx.BeginFunc(ctx, p.pool, func(tx pgx.Tx) error {
		// Serialise the restrictions of one guest: a partial unique index cannot
		// express "not expired" (the clock is not part of a row), so the check and
		// the insert run under a transaction-scoped advisory lock keyed by guest.
		// The lock is released at commit/rollback, so a second restrict waits and
		// then sees the first one's committed row.
		if _, err := tx.Exec(ctx, lockBlockerSQL, "restrict:"+r.GuestID); err != nil {
			return err
		}
		var existing string
		switch err := tx.QueryRow(ctx, activeRestrictionSQL, r.GuestID, r.CreatedAt).Scan(&existing); {
		case err == nil:
			return fmt.Errorf("%w: %q", ErrAlreadyRestricted, r.GuestID)
		case !errors.Is(err, pgx.ErrNoRows):
			return err
		}
		if _, err := tx.Exec(ctx, insertRestrictionSQL, r.ID, r.GuestID, nullable(r.UserID), r.Reason,
			nullable(r.ReportID), r.CreatedAt, expires, nil, r.Operator); err != nil {
			return err
		}
		return insertAction(ctx, tx, audit)
	})
	switch {
	case errors.Is(err, ErrAlreadyRestricted):
		return err
	case err != nil:
		return fmt.Errorf("%screate restriction: %w", opErrPrefix, err)
	}
	return nil
}

// RevokeRestriction implements Moderation.
func (p *Postgres) RevokeRestriction(ctx context.Context, id string, now time.Time, audit safety.Action) (safety.Restriction, error) {
	if err := validateAction(audit); err != nil {
		return safety.Restriction{}, err
	}
	if !ValidID(id) {
		return safety.Restriction{}, fmt.Errorf("%w: %q", ErrRestrictionNotFound, id)
	}
	now = normalizeTime(now)
	var out safety.Restriction
	err := pgx.BeginFunc(ctx, p.pool, func(tx pgx.Tx) error {
		r, err := scanRestriction(tx.QueryRow(ctx, revokeRestrictionSQL, id, now))
		switch {
		case err == nil:
			out = r
			return insertAction(ctx, tx, audit)
		case !errors.Is(err, pgx.ErrNoRows):
			return err
		}
		existing, err := scanRestriction(tx.QueryRow(ctx, getRestrictionSQL, id))
		switch {
		case errors.Is(err, pgx.ErrNoRows):
			return fmt.Errorf("%w: %q", ErrRestrictionNotFound, id)
		case err != nil:
			return err
		}
		out = existing
		return fmt.Errorf("%w: %q", ErrRestrictionEnded, id)
	})
	switch {
	case errors.Is(err, ErrRestrictionNotFound), errors.Is(err, ErrRestrictionEnded):
		return out, err
	case err != nil:
		return safety.Restriction{}, fmt.Errorf("%srevoke restriction: %w", opErrPrefix, err)
	}
	return out, nil
}

// GetRestriction implements Moderation.
func (p *Postgres) GetRestriction(ctx context.Context, id string) (safety.Restriction, error) {
	if !ValidID(id) {
		return safety.Restriction{}, fmt.Errorf("%w: %q", ErrRestrictionNotFound, id)
	}
	r, err := scanRestriction(p.pool.QueryRow(ctx, getRestrictionSQL, id))
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return safety.Restriction{}, fmt.Errorf("%w: %q", ErrRestrictionNotFound, id)
	case err != nil:
		return safety.Restriction{}, fmt.Errorf("%sget restriction: %w", opErrPrefix, err)
	}
	return r, nil
}

// LoadRestrictions implements Moderation.
func (p *Postgres) LoadRestrictions(ctx context.Context, now time.Time) ([]safety.Restriction, error) {
	rows, err := p.pool.Query(ctx, loadRestrictionsSQL, normalizeTime(now))
	if err != nil {
		return nil, fmt.Errorf("%sload restrictions: %w", opErrPrefix, err)
	}
	defer rows.Close()
	var out []safety.Restriction
	for rows.Next() {
		r, err := scanRestriction(rows)
		if err != nil {
			return nil, fmt.Errorf("%sload restrictions: %w", opErrPrefix, err)
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("%sload restrictions: %w", opErrPrefix, err)
	}
	return out, nil
}

// ResolveReportAudited implements Moderation.
func (p *Postgres) ResolveReportAudited(ctx context.Context, id, status string, now time.Time, audit safety.Action) error {
	if err := validateAction(audit); err != nil {
		return err
	}
	if status != safety.StatusActioned && status != safety.StatusDismissed {
		return fmt.Errorf("%w: cannot resolve with status %q", ErrInvalidReport, status)
	}
	if !ValidID(id) {
		return fmt.Errorf("%w: %q", ErrReportNotFound, id)
	}
	err := pgx.BeginFunc(ctx, p.pool, func(tx pgx.Tx) error {
		if err := resolveOpenReport(ctx, tx, id, status, now); err != nil {
			return err
		}
		return insertAction(ctx, tx, audit)
	})
	switch {
	case errors.Is(err, ErrReportNotFound), errors.Is(err, ErrReportNotOpen):
		return err
	case err != nil:
		return fmt.Errorf("%sresolve report: %w", opErrPrefix, err)
	}
	return nil
}

// RecordAction implements Moderation.
func (p *Postgres) RecordAction(ctx context.Context, a safety.Action) error {
	if err := validateAction(a); err != nil {
		return err
	}
	if err := pgx.BeginFunc(ctx, p.pool, func(tx pgx.Tx) error { return insertAction(ctx, tx, a) }); err != nil {
		return fmt.Errorf("%srecord action: %w", opErrPrefix, err)
	}
	return nil
}

// ListActions implements Moderation.
func (p *Postgres) ListActions(ctx context.Context, limit int) ([]safety.Action, error) {
	rows, err := p.pool.Query(ctx, listActionsSQL, clampLimit(limit, 50, 1000))
	if err != nil {
		return nil, fmt.Errorf("%slist actions: %w", opErrPrefix, err)
	}
	defer rows.Close()
	var out []safety.Action
	for rows.Next() {
		var (
			a      safety.Action
			report *string
		)
		if err := rows.Scan(&a.ID, &a.At, &a.Operator, &a.Action, &a.TargetKind, &a.TargetID, &report, &a.Details); err != nil {
			return nil, fmt.Errorf("%slist actions: %w", opErrPrefix, err)
		}
		a.At, a.ReportID = a.At.UTC(), deref(report)
		out = append(out, a)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("%slist actions: %w", opErrPrefix, err)
	}
	return out, nil
}

// ClearProfile implements Moderation.
func (p *Postgres) ClearProfile(ctx context.Context, userID string) error {
	if !ValidID(userID) {
		return fmt.Errorf("%w: %q", ErrUserNotFound, userID)
	}
	tag, err := p.pool.Exec(ctx, clearProfileSQL, userID)
	switch {
	case err != nil:
		return fmt.Errorf("%sclear profile: %w", opErrPrefix, err)
	case tag.RowsAffected() == 0:
		return fmt.Errorf("%w: %q", ErrUserNotFound, userID)
	}
	return nil
}

// PurgeModeration implements Moderation.
func (p *Postgres) PurgeModeration(ctx context.Context, now time.Time) (ModerationPurge, error) {
	var out ModerationPurge
	err := pgx.BeginFunc(ctx, p.pool, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, purgeRestrictionsSQL, normalizeTime(safety.RestrictionPurgeBefore(now)))
		if err != nil {
			return err
		}
		out.Restrictions = tag.RowsAffected()
		tag, err = tx.Exec(ctx, purgeActionsSQL, normalizeTime(safety.AuditPurgeBefore(now)))
		if err != nil {
			return err
		}
		out.Actions = tag.RowsAffected()
		return nil
	})
	if err != nil {
		return ModerationPurge{}, fmt.Errorf("%spurge moderation: %w", opErrPrefix, err)
	}
	return out, nil
}
