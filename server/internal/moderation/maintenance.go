package moderation

import (
	"context"
	"log/slog"
	"time"

	"github.com/AresiusXP/tutti-frutti/server/internal/safety"
	"github.com/AresiusXP/tutti-frutti/server/internal/store"
)

// Clock is the maintenance loop's time source, injectable in tests.
type Clock interface {
	Now() time.Time
	// After returns a channel that receives once d has elapsed.
	After(d time.Duration) <-chan time.Time
}

type systemClock struct{}

func (systemClock) Now() time.Time                         { return time.Now() }
func (systemClock) After(d time.Duration) <-chan time.Time { return time.After(d) }

// SystemClock is the wall clock.
func SystemClock() Clock { return systemClock{} }

// Defaults of Maintenance.
const (
	// DefaultEvery is how often the retention purge runs: once a day (and once
	// at start).
	DefaultEvery = 24 * time.Hour
	// stepTimeout bounds one database step of a run.
	stepTimeout = 30 * time.Second
)

// Maintenance deletes what the retention rules of docs/MODERATION.md say, once
// a day and once when the server starts:
//
//   - reports 30 days after they were resolved, and 90 days after they were
//     filed at the latest (safety.PurgeAt, applied by Safety.PurgeReports);
//   - terms acceptances older than a year (safety.AcceptanceTTL; the gate
//     already treats them as not accepted);
//   - restrictions 90 days after they ended (expired or revoked);
//   - audit records of the operator's actions after a year.
//
// Blocks are never purged here: they last until the blocker removes them. It
// also refreshes the open-report gauges. Each step is independent (one failing
// does not stop the others) and only counts are logged, never report text.
type Maintenance struct {
	Safety store.Safety
	Mod    store.Moderation
	Gauges *ReportGauges
	Clock  Clock
	Log    *slog.Logger
	// Every is the interval between runs (DefaultEvery).
	Every time.Duration
}

// Result is what one run deleted. Errors counts the steps that failed.
type Result struct {
	Reports      int64
	Terms        int64
	Restrictions int64
	Actions      int64
	Errors       int
}

func (m *Maintenance) defaults() (Clock, *slog.Logger, time.Duration) {
	clock, log, every := m.Clock, m.Log, m.Every
	if clock == nil {
		clock = SystemClock()
	}
	if log == nil {
		log = slog.Default()
	}
	if every <= 0 {
		every = DefaultEvery
	}
	return clock, log, every
}

// RunOnce runs every step once.
func (m *Maintenance) RunOnce(ctx context.Context) Result {
	clock, log, _ := m.defaults()
	now := clock.Now()
	var res Result
	step := func(name string, f func(context.Context) error) {
		sctx, cancel := context.WithTimeout(ctx, stepTimeout)
		defer cancel()
		if err := f(sctx); err != nil {
			res.Errors++
			log.Warn("maintenance step failed", "step", name, "err", err)
		}
	}
	step("reports", func(ctx context.Context) (err error) {
		res.Reports, err = m.Safety.PurgeReports(ctx, now)
		return err
	})
	step("terms", func(ctx context.Context) (err error) {
		res.Terms, err = m.Safety.PurgeTerms(ctx, safety.AcceptancePurgeBefore(now))
		return err
	})
	step("moderation", func(ctx context.Context) error {
		p, err := m.Mod.PurgeModeration(ctx, now)
		res.Restrictions, res.Actions = p.Restrictions, p.Actions
		return err
	})
	if m.Gauges != nil {
		step("gauges", func(ctx context.Context) error {
			_, err := m.Gauges.Refresh(ctx, m.Safety)
			return err
		})
	}
	log.Info("maintenance run", "reports_purged", res.Reports, "terms_purged", res.Terms,
		"restrictions_purged", res.Restrictions, "audit_purged", res.Actions, "errors", res.Errors)
	return res
}

// Run runs once now and then every Every until ctx is done.
func (m *Maintenance) Run(ctx context.Context) {
	clock, _, every := m.defaults()
	for {
		m.RunOnce(ctx)
		select {
		case <-ctx.Done():
			return
		case <-clock.After(every):
		}
	}
}
