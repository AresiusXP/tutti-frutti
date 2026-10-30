package store

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/AresiusXP/tutti-frutti/server/internal/safety"
)

var _ Safety = (*Postgres)(nil)

const (
	getTermsSQL = `SELECT guest_id, version, accepted_at, locale, age13_confirmed
FROM terms_acceptances WHERE guest_id = $1 AND version = $2`
	// acceptTermsSQL inserts the acceptance, or returns the row that is already
	// there: the first acceptance of a version is kept, unless it is older than
	// the acceptance TTL (it no longer counts), when the new one replaces it.
	// The no-op SET keeps RETURNING working on the untouched row.
	acceptTermsSQL = `INSERT INTO terms_acceptances (guest_id, version, accepted_at, locale, age13_confirmed)
VALUES ($1, $2, $3, $4, $5)
ON CONFLICT (guest_id, version) DO UPDATE SET
  accepted_at = CASE WHEN terms_acceptances.accepted_at <= EXCLUDED.accepted_at - make_interval(secs => $6::double precision)
                     THEN EXCLUDED.accepted_at ELSE terms_acceptances.accepted_at END,
  locale = CASE WHEN terms_acceptances.accepted_at <= EXCLUDED.accepted_at - make_interval(secs => $6::double precision)
                THEN EXCLUDED.locale ELSE terms_acceptances.locale END
RETURNING guest_id, version, accepted_at, locale, age13_confirmed`

	reportColumns = `id, reporter_guest, reporter_user, room_instance, match_instance, content_key, kind,
target_guest, target_user, evidence_json, evidence_verified, reason, comment, status, idempotency_key,
created_at, resolved_at, purge_at`
	insertReportSQL = `INSERT INTO reports (` + reportColumns + `)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18)
ON CONFLICT (reporter_guest, idempotency_key) DO NOTHING
RETURNING id`
	existingReportSQL = `SELECT id FROM reports WHERE reporter_guest = $1 AND idempotency_key = $2`
	getReportSQL      = `SELECT ` + reportColumns + ` FROM reports WHERE id = $1`
	purgeReportsSQL   = `DELETE FROM reports WHERE purge_at <= $1`
	purgeTermsSQL     = `DELETE FROM terms_acceptances WHERE accepted_at < $1`
	reportStatsSQL    = `SELECT count(*), min(created_at) FROM reports WHERE status = 'open'`
)

// resolveReportSQL is safety.PurgeAt in SQL: the earlier of created + 90 days
// and resolved + 30 days (the intervals come from the constants, not from
// input). It is a compare-and-set on status = 'open': a concurrent resolution
// waits for the row lock, re-evaluates the predicate on the first one's row and
// updates nothing, so the first resolution's status, resolved_at and purge_at
// are kept.
var resolveReportSQL = fmt.Sprintf(`UPDATE reports SET status = $2, resolved_at = $3::timestamptz,
purge_at = LEAST(created_at + interval '%d seconds', $3::timestamptz + interval '%d seconds')
WHERE id = $1 AND status = 'open'`, int64(safety.MaxRetention/time.Second), int64(safety.ResolvedRetention/time.Second))

const reportStatusSQL = `SELECT status FROM reports WHERE id = $1`

// querier is what the pool and a transaction share.
type querier interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

// resolveOpenReport runs the compare-and-set of resolveReportSQL on q (the
// pool, or the transaction that also writes the audit record) and tells
// "resolved" (nil), "no such report" and "not open any more" apart.
func resolveOpenReport(ctx context.Context, q querier, id, status string, now time.Time) error {
	tag, err := q.Exec(ctx, resolveReportSQL, id, status, normalizeTime(now))
	if err != nil {
		return err
	}
	if tag.RowsAffected() > 0 {
		return nil
	}
	var current string
	switch err := q.QueryRow(ctx, reportStatusSQL, id).Scan(&current); {
	case errors.Is(err, pgx.ErrNoRows):
		return fmt.Errorf("%w: %q", ErrReportNotFound, id)
	case err != nil:
		return err
	}
	return fmt.Errorf("%w: %q is %s", ErrReportNotOpen, id, current)
}

// nullable maps the empty string to SQL NULL.
func nullable(s string) any {
	if s == "" {
		return nil
	}
	return s
}

// TermsAcceptance implements safety.TermsStore.
func (p *Postgres) TermsAcceptance(ctx context.Context, guestID, version string) (safety.TermsAcceptance, bool, error) {
	if err := ctx.Err(); err != nil {
		return safety.TermsAcceptance{}, false, err
	}
	a, err := scanAcceptance(p.pool.QueryRow(ctx, getTermsSQL, guestID, version))
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return safety.TermsAcceptance{}, false, nil
	case err != nil:
		return safety.TermsAcceptance{}, false, fmt.Errorf("%sterms acceptance: %w", opErrPrefix, err)
	}
	return a, true, nil
}

// AcceptTerms implements safety.TermsStore.
func (p *Postgres) AcceptTerms(ctx context.Context, a safety.TermsAcceptance) (safety.TermsAcceptance, error) {
	if err := ctx.Err(); err != nil {
		return safety.TermsAcceptance{}, err
	}
	if err := validateAcceptance(a); err != nil {
		return safety.TermsAcceptance{}, err
	}
	stored, err := scanAcceptance(p.pool.QueryRow(ctx, acceptTermsSQL,
		a.GuestID, a.Version, normalizeTime(a.AcceptedAt), a.Locale, a.Age13Confirmed,
		safety.AcceptanceTTL.Seconds()))
	if err != nil {
		return safety.TermsAcceptance{}, fmt.Errorf("%saccept terms: %w", opErrPrefix, err)
	}
	return stored, nil
}

func scanAcceptance(row pgx.Row) (safety.TermsAcceptance, error) {
	var a safety.TermsAcceptance
	if err := row.Scan(&a.GuestID, &a.Version, &a.AcceptedAt, &a.Locale, &a.Age13Confirmed); err != nil {
		return safety.TermsAcceptance{}, err
	}
	a.AcceptedAt = a.AcceptedAt.UTC()
	return a, nil
}

// CreateReport implements Safety. The insert is ON CONFLICT DO NOTHING on
// (reporter_guest, idempotency_key); a conflict returns the original id. A row
// purged between the two statements is simply inserted again.
func (p *Postgres) CreateReport(ctx context.Context, r safety.Report) (string, bool, error) {
	if err := ctx.Err(); err != nil {
		return "", false, err
	}
	if err := validateReport(r); err != nil {
		return "", false, err
	}
	var resolved any
	if !r.ResolvedAt.IsZero() {
		resolved = normalizeTime(r.ResolvedAt)
	}
	for range 2 {
		var id string
		err := p.pool.QueryRow(ctx, insertReportSQL,
			r.ID, r.ReporterGuest, nullable(r.ReporterUser), r.RoomInstance, nullable(r.MatchInstance),
			r.ContentKey, r.Kind, nullable(r.TargetGuest), nullable(r.TargetUser), []byte(r.Evidence),
			r.EvidenceVerified, r.Reason, r.Comment, r.Status, r.IdempotencyKey,
			normalizeTime(r.CreatedAt), resolved, normalizeTime(r.PurgeAt),
		).Scan(&id)
		switch {
		case err == nil:
			return id, true, nil
		case !errors.Is(err, pgx.ErrNoRows):
			return "", false, fmt.Errorf("%screate report: %w", opErrPrefix, err)
		}
		switch err := p.pool.QueryRow(ctx, existingReportSQL, r.ReporterGuest, r.IdempotencyKey).Scan(&id); {
		case err == nil:
			return id, false, nil
		case !errors.Is(err, pgx.ErrNoRows):
			return "", false, fmt.Errorf("%screate report: %w", opErrPrefix, err)
		}
	}
	return "", false, fmt.Errorf("%screate report: the idempotency row keeps disappearing", opErrPrefix)
}

// scanReport reads one row of reportColumns.
func scanReport(row pgx.Row) (safety.Report, error) {
	var (
		r                           safety.Report
		reporterUser, matchInstance *string
		targetGuest, targetUser     *string
		evidence                    []byte
		created, purge              time.Time
		resolved                    *time.Time
	)
	if err := row.Scan(
		&r.ID, &r.ReporterGuest, &reporterUser, &r.RoomInstance, &matchInstance, &r.ContentKey, &r.Kind,
		&targetGuest, &targetUser, &evidence, &r.EvidenceVerified, &r.Reason, &r.Comment, &r.Status,
		&r.IdempotencyKey, &created, &resolved, &purge); err != nil {
		return safety.Report{}, err
	}
	r.ReporterUser, r.MatchInstance = deref(reporterUser), deref(matchInstance)
	r.TargetGuest, r.TargetUser = deref(targetGuest), deref(targetUser)
	r.Evidence = evidence
	r.CreatedAt, r.PurgeAt = created.UTC(), purge.UTC()
	if resolved != nil {
		r.ResolvedAt = resolved.UTC()
	}
	return r, nil
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

// GetReport implements Safety.
func (p *Postgres) GetReport(ctx context.Context, id string) (safety.Report, error) {
	if !ValidID(id) {
		return safety.Report{}, fmt.Errorf("%w: %q", ErrReportNotFound, id)
	}
	r, err := scanReport(p.pool.QueryRow(ctx, getReportSQL, id))
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return safety.Report{}, fmt.Errorf("%w: %q", ErrReportNotFound, id)
	case err != nil:
		return safety.Report{}, fmt.Errorf("%sget report: %w", opErrPrefix, err)
	}
	return r, nil
}

// ListReports implements Safety.
func (p *Postgres) ListReports(ctx context.Context, filter string, limit int) ([]safety.Report, error) {
	if !validReportFilter(filter) {
		return nil, fmt.Errorf("%w: unknown report filter %q", ErrInvalidReport, filter)
	}
	where, order := "", "created_at DESC, id DESC"
	switch filter {
	case safety.StatusOpen:
		where, order = "WHERE status = 'open'", "created_at ASC, id ASC"
	case ReportsResolved:
		where = "WHERE status <> 'open'"
	}
	rows, err := p.pool.Query(ctx, `SELECT `+reportColumns+` FROM reports `+where+` ORDER BY `+order+` LIMIT $1`,
		clampLimit(limit, 20, 100))
	if err != nil {
		return nil, fmt.Errorf("%slist reports: %w", opErrPrefix, err)
	}
	defer rows.Close()
	var out []safety.Report
	for rows.Next() {
		r, err := scanReport(rows)
		if err != nil {
			return nil, fmt.Errorf("%slist reports: %w", opErrPrefix, err)
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("%slist reports: %w", opErrPrefix, err)
	}
	return out, nil
}

// ReportStats implements Safety.
func (p *Postgres) ReportStats(ctx context.Context) (ReportStats, error) {
	var (
		st     ReportStats
		oldest *time.Time
	)
	if err := p.pool.QueryRow(ctx, reportStatsSQL).Scan(&st.Open, &oldest); err != nil {
		return ReportStats{}, fmt.Errorf("%sreport stats: %w", opErrPrefix, err)
	}
	if oldest != nil {
		st.OldestOpen = oldest.UTC()
	}
	return st, nil
}

// PurgeTerms implements Safety.
func (p *Postgres) PurgeTerms(ctx context.Context, before time.Time) (int64, error) {
	tag, err := p.pool.Exec(ctx, purgeTermsSQL, normalizeTime(before))
	if err != nil {
		return 0, fmt.Errorf("%spurge terms: %w", opErrPrefix, err)
	}
	return tag.RowsAffected(), nil
}

// ResolveReport implements Safety. purge_at becomes safety.PurgeAt(created,
// now), computed in SQL so the report is read and written in one statement,
// and only an open report is updated (see resolveReportSQL).
func (p *Postgres) ResolveReport(ctx context.Context, id, status string, now time.Time) error {
	if status != safety.StatusActioned && status != safety.StatusDismissed {
		return fmt.Errorf("%w: cannot resolve with status %q", ErrInvalidReport, status)
	}
	if !ValidID(id) {
		return fmt.Errorf("%w: %q", ErrReportNotFound, id)
	}
	switch err := resolveOpenReport(ctx, p.pool, id, status, now); {
	case errors.Is(err, ErrReportNotFound), errors.Is(err, ErrReportNotOpen):
		return err
	case err != nil:
		return fmt.Errorf("%sresolve report: %w", opErrPrefix, err)
	}
	return nil
}

// PurgeReports implements Safety.
func (p *Postgres) PurgeReports(ctx context.Context, now time.Time) (int64, error) {
	tag, err := p.pool.Exec(ctx, purgeReportsSQL, normalizeTime(now))
	if err != nil {
		return 0, fmt.Errorf("%spurge reports: %w", opErrPrefix, err)
	}
	return tag.RowsAffected(), nil
}
