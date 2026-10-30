package store

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"
	"unicode/utf8"

	"github.com/AresiusXP/tutti-frutti/server/internal/safety"
)

// Errors of the Safety methods. Both implementations return them wrapped, so
// match with errors.Is.
var (
	// ErrInvalidTerms: the acceptance cannot be stored (bad guest id, version or
	// locale, or the age was not confirmed).
	ErrInvalidTerms = errors.New("store: invalid terms acceptance")
	// ErrInvalidReport: the report cannot be stored (retrying cannot succeed).
	ErrInvalidReport = errors.New("store: invalid report")
	// ErrReportNotFound: no report with that id (never filed, or purged).
	ErrReportNotFound = errors.New("store: report not found")
	// ErrReportNotOpen: the report is already resolved (actioned or dismissed).
	// Resolving is a compare-and-set on the open status, so of two concurrent
	// resolutions exactly one wins and the other gets this; the first
	// resolution's status, resolved_at and purge_at are never overwritten.
	ErrReportNotOpen = errors.New("store: report is not open")
)

// maxEvidenceBytes bounds evidence_json (a report is a few hundred bytes; the
// REST body is limited to 4 KiB).
const maxEvidenceBytes = 8 << 10

// Safety is the persistence contract of the content-safety layer: terms
// acceptances and reports. All methods must be safe for concurrent use and
// honour ctx cancellation. Callers keep database I/O off the room actors (the
// REST handlers and the room's off-actor gate call it).
type Safety interface {
	safety.TermsStore

	// CreateReport stores a report and returns its id. It is idempotent per
	// (ReporterGuest, IdempotencyKey): when the pair already exists nothing is
	// stored, the id of the original report is returned and created is false.
	// A nil error means the report is durable.
	CreateReport(ctx context.Context, r safety.Report) (id string, created bool, err error)
	// GetReport returns a report, or ErrReportNotFound.
	GetReport(ctx context.Context, id string) (safety.Report, error)
	// ResolveReport closes an OPEN report (status actioned or dismissed),
	// stamping ResolvedAt = now and PurgeAt = safety.PurgeAt(CreatedAt, now); or
	// ErrReportNotFound. It is a compare-and-set: a report that is not open any
	// more is left untouched and gives ErrReportNotOpen.
	ResolveReport(ctx context.Context, id, status string, now time.Time) error
	// PurgeReports deletes the reports whose PurgeAt is not after now and
	// returns how many it deleted (the daily maintenance loop calls it).
	PurgeReports(ctx context.Context, now time.Time) (int64, error)
	// ListReports returns at most limit reports (default 20, at most 100) of
	// the given filter: StatusOpen (oldest first, what the operator triages),
	// ReportsResolved (actioned or dismissed) or ReportsAll, the latter two
	// newest first.
	ListReports(ctx context.Context, filter string, limit int) ([]safety.Report, error)
	// ReportStats counts the open reports and gives the filing time of the
	// oldest one (zero when there is none).
	ReportStats(ctx context.Context) (ReportStats, error)
	// PurgeTerms deletes the terms acceptances made before the cut-off
	// (safety.AcceptancePurgeBefore) and returns how many it deleted.
	PurgeTerms(ctx context.Context, before time.Time) (int64, error)
}

// Filters of ListReports besides safety.StatusOpen.
const (
	// ReportsResolved selects the actioned and dismissed reports.
	ReportsResolved = "resolved"
	// ReportsAll selects every report.
	ReportsAll = "all"
)

// ReportStats is what the operator's status and the gauges need to know about
// the open reports.
type ReportStats struct {
	// Open is the number of reports waiting for the operator.
	Open int
	// OldestOpen is when the oldest open report was filed; zero when none.
	OldestOpen time.Time
}

func validReportFilter(f string) bool {
	return f == safety.StatusOpen || f == ReportsResolved || f == ReportsAll
}

func validateAcceptance(a safety.TermsAcceptance) error {
	switch {
	case !safety.ValidToken(a.GuestID):
		return fmt.Errorf("%w: bad guest id", ErrInvalidTerms)
	case !safety.ValidToken(a.Version):
		return fmt.Errorf("%w: bad version", ErrInvalidTerms)
	case !safety.ValidLocale(a.Locale):
		return fmt.Errorf("%w: bad locale", ErrInvalidTerms)
	case !a.Age13Confirmed:
		return fmt.Errorf("%w: age not confirmed", ErrInvalidTerms)
	case a.AcceptedAt.IsZero():
		return fmt.Errorf("%w: no time", ErrInvalidTerms)
	}
	return nil
}

// validateReport checks what the database would otherwise have to: both
// implementations apply the same rules.
func validateReport(r safety.Report) error {
	bad := func(what string) error { return fmt.Errorf("%w: %s", ErrInvalidReport, what) }
	switch {
	case !ValidID(r.ID):
		return bad("id")
	case !safety.ValidToken(r.ReporterGuest):
		return bad("reporter guest")
	case r.ReporterUser != "" && !ValidID(r.ReporterUser):
		return bad("reporter user")
	case r.RoomInstance != "" && !safety.ValidToken(r.RoomInstance):
		return bad("room instance")
	case r.MatchInstance != "" && !safety.ValidToken(r.MatchInstance):
		return bad("match instance")
	case r.ContentKey == "" || len(r.ContentKey) > 160 || !plainText(r.ContentKey):
		return bad("content key")
	case !safety.ValidKind(r.Kind):
		return bad("kind")
	case r.TargetGuest != "" && !safety.ValidToken(r.TargetGuest):
		return bad("target guest")
	case r.TargetUser != "" && !ValidID(r.TargetUser):
		return bad("target user")
	case len(r.Evidence) == 0 || len(r.Evidence) > maxEvidenceBytes || !json.Valid(r.Evidence):
		return bad("evidence")
	case hasNULEscape(r.Evidence):
		return bad("evidence holds a NUL (jsonb cannot store it)")
	case !safety.ValidReason(r.Reason):
		return bad("reason")
	case utf8.RuneCountInString(r.Comment) > safety.MaxCommentRunes || !plainText(r.Comment):
		return bad("comment")
	case !safety.ValidStatus(r.Status):
		return bad("status")
	case !safety.ValidToken(r.IdempotencyKey) || len(r.IdempotencyKey) < safety.MinKeyLen:
		return bad("idempotency key")
	case r.CreatedAt.IsZero():
		return bad("created at")
	case !r.PurgeAt.After(r.CreatedAt) || r.PurgeAt.After(r.CreatedAt.Add(safety.MaxRetention)):
		return bad("purge at")
	}
	return nil
}

// hasNULEscape reports whether the JSON text holds the escape \u0000 (an odd
// number of backslashes before the u): PostgreSQL's jsonb refuses it. The six
// characters \u0000 typed by a player are escaped as \\u0000 (an even number)
// and are fine.
func hasNULEscape(b []byte) bool {
	needle := []byte(`\u0000`)
	for from := 0; ; {
		i := bytes.Index(b[from:], needle)
		if i < 0 {
			return false
		}
		i += from
		n := 0
		for j := i; j >= 0 && b[j] == '\\'; j-- {
			n++
		}
		if n%2 == 1 {
			return true
		}
		from = i + 1
	}
}
