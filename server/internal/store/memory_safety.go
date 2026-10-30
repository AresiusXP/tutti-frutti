package store

import (
	"bytes"
	"context"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/AresiusXP/tutti-frutti/server/internal/safety"
)

var (
	_ Safety     = (*Memory)(nil)
	_ Moderation = (*Memory)(nil)
)

type termsKey struct{ guest, version string }

type reportKey struct{ guest, key string }

// memSafety is the in-memory state of the Safety methods; Memory embeds it by
// value behind safeMu.
type memSafety struct {
	terms     map[termsKey]safety.TermsAcceptance
	reports   map[string]safety.Report // by id
	reportKey map[reportKey]string     // (reporter, idempotency key) -> id
	mod       memModeration
}

// TermsAcceptance implements safety.TermsStore.
func (m *Memory) TermsAcceptance(ctx context.Context, guestID, version string) (safety.TermsAcceptance, bool, error) {
	if err := ctx.Err(); err != nil {
		return safety.TermsAcceptance{}, false, err
	}
	m.safeMu.Lock()
	defer m.safeMu.Unlock()
	a, ok := m.safe.terms[termsKey{guestID, version}]
	return a, ok, nil
}

// AcceptTerms implements safety.TermsStore.
func (m *Memory) AcceptTerms(ctx context.Context, a safety.TermsAcceptance) (safety.TermsAcceptance, error) {
	if err := ctx.Err(); err != nil {
		return safety.TermsAcceptance{}, err
	}
	if err := validateAcceptance(a); err != nil {
		return safety.TermsAcceptance{}, err
	}
	a.AcceptedAt = normalizeTime(a.AcceptedAt)
	m.safeMu.Lock()
	defer m.safeMu.Unlock()
	k := termsKey{a.GuestID, a.Version}
	if existing, ok := m.safe.terms[k]; ok && !safety.AcceptanceExpired(existing.AcceptedAt, a.AcceptedAt) {
		return existing, nil
	}
	if m.safe.terms == nil {
		m.safe.terms = map[termsKey]safety.TermsAcceptance{}
	}
	m.safe.terms[k] = a
	return a, nil
}

// CreateReport implements Safety.
func (m *Memory) CreateReport(ctx context.Context, r safety.Report) (string, bool, error) {
	if err := ctx.Err(); err != nil {
		return "", false, err
	}
	if err := validateReport(r); err != nil {
		return "", false, err
	}
	m.safeMu.Lock()
	defer m.safeMu.Unlock()
	k := reportKey{r.ReporterGuest, r.IdempotencyKey}
	if id, ok := m.safe.reportKey[k]; ok {
		return id, false, nil
	}
	if m.safe.reports == nil {
		m.safe.reports, m.safe.reportKey = map[string]safety.Report{}, map[reportKey]string{}
	}
	r.CreatedAt, r.PurgeAt = normalizeTime(r.CreatedAt), normalizeTime(r.PurgeAt)
	if !r.ResolvedAt.IsZero() {
		r.ResolvedAt = normalizeTime(r.ResolvedAt)
	}
	r.Evidence = bytes.Clone(r.Evidence)
	m.safe.reports[r.ID] = r
	m.safe.reportKey[k] = r.ID
	return r.ID, true, nil
}

// GetReport implements Safety.
func (m *Memory) GetReport(ctx context.Context, id string) (safety.Report, error) {
	if err := ctx.Err(); err != nil {
		return safety.Report{}, err
	}
	m.safeMu.Lock()
	defer m.safeMu.Unlock()
	r, ok := m.safe.reports[id]
	if !ok {
		return safety.Report{}, fmt.Errorf("%w: %q", ErrReportNotFound, id)
	}
	r.Evidence = bytes.Clone(r.Evidence)
	return r, nil
}

// ResolveReport implements Safety.
func (m *Memory) ResolveReport(ctx context.Context, id, status string, now time.Time) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	m.safeMu.Lock()
	defer m.safeMu.Unlock()
	return m.resolveReportLocked(id, status, now)
}

// resolveReportLocked closes an open report (a compare-and-set: one that is
// not open is left as it is and gives ErrReportNotOpen); safeMu must be held.
func (m *Memory) resolveReportLocked(id, status string, now time.Time) error {
	if status != safety.StatusActioned && status != safety.StatusDismissed {
		return fmt.Errorf("%w: cannot resolve with status %q", ErrInvalidReport, status)
	}
	r, ok := m.safe.reports[id]
	if !ok {
		return fmt.Errorf("%w: %q", ErrReportNotFound, id)
	}
	if r.Status != safety.StatusOpen {
		return fmt.Errorf("%w: %q is %s", ErrReportNotOpen, id, r.Status)
	}
	now = normalizeTime(now)
	r.Status, r.ResolvedAt, r.PurgeAt = status, now, normalizeTime(safety.PurgeAt(r.CreatedAt, now))
	m.safe.reports[id] = r
	return nil
}

// PurgeReports implements Safety.
func (m *Memory) PurgeReports(ctx context.Context, now time.Time) (int64, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	now = normalizeTime(now)
	m.safeMu.Lock()
	defer m.safeMu.Unlock()
	var n int64
	for id, r := range m.safe.reports {
		if safety.Expired(r.PurgeAt, now) {
			delete(m.safe.reports, id)
			delete(m.safe.reportKey, reportKey{r.ReporterGuest, r.IdempotencyKey})
			n++
		}
	}
	return n, nil
}

// ListReports implements Safety.
func (m *Memory) ListReports(ctx context.Context, filter string, limit int) ([]safety.Report, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if !validReportFilter(filter) {
		return nil, fmt.Errorf("%w: unknown report filter %q", ErrInvalidReport, filter)
	}
	m.safeMu.Lock()
	defer m.safeMu.Unlock()
	var out []safety.Report
	for _, r := range m.safe.reports {
		switch {
		case filter == safety.StatusOpen && r.Status != safety.StatusOpen,
			filter == ReportsResolved && r.Status == safety.StatusOpen:
			continue
		}
		r.Evidence = bytes.Clone(r.Evidence)
		out = append(out, r)
	}
	slices.SortFunc(out, func(a, b safety.Report) int {
		n := a.CreatedAt.Compare(b.CreatedAt)
		if n == 0 {
			n = strings.Compare(a.ID, b.ID)
		}
		if filter != safety.StatusOpen {
			n = -n
		}
		return n
	})
	return out[:min(len(out), clampLimit(limit, 20, 100))], nil
}

// ReportStats implements Safety.
func (m *Memory) ReportStats(ctx context.Context) (ReportStats, error) {
	if err := ctx.Err(); err != nil {
		return ReportStats{}, err
	}
	m.safeMu.Lock()
	defer m.safeMu.Unlock()
	var st ReportStats
	for _, r := range m.safe.reports {
		if r.Status != safety.StatusOpen {
			continue
		}
		st.Open++
		if st.OldestOpen.IsZero() || r.CreatedAt.Before(st.OldestOpen) {
			st.OldestOpen = r.CreatedAt
		}
	}
	return st, nil
}

// PurgeTerms implements Safety.
func (m *Memory) PurgeTerms(ctx context.Context, before time.Time) (int64, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	before = normalizeTime(before)
	m.safeMu.Lock()
	defer m.safeMu.Unlock()
	var n int64
	for k, a := range m.safe.terms {
		if a.AcceptedAt.Before(before) {
			delete(m.safe.terms, k)
			n++
		}
	}
	return n, nil
}
