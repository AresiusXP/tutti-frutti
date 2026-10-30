package moderation

import (
	"context"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/AresiusXP/tutti-frutti/server/internal/store"
)

// ReportGauges are the two gauges the owner's daily check can also read from
// Prometheus: how many reports wait and how long the oldest has waited. They
// are refreshed from the database by the maintenance loop and after a report is
// resolved, and bumped in memory when a report is filed (ReportCreated); the age
// is computed at every scrape, so it keeps growing between refreshes.
//
// A metric alone is not a notification process: Alertmanager routes the
// chart's warning and critical alerts (TuttiFruttiReportWaiting,
// TuttiFruttiUrgentReport) to the owner's phone through ntfy, and
// docs/MODERATION.md defines the daily check.
type ReportGauges struct {
	now func() time.Time

	mu     sync.Mutex
	open   int
	oldest time.Time
}

// NewReportGauges registers the gauges in reg (nil: no metrics). now defaults to
// time.Now.
func NewReportGauges(reg prometheus.Registerer, now func() time.Time) *ReportGauges {
	if now == nil {
		now = time.Now
	}
	g := &ReportGauges{now: now}
	if reg != nil {
		reg.MustRegister(
			prometheus.NewGaugeFunc(prometheus.GaugeOpts{
				Name: "tuttifrutti_reports_open",
				Help: "Content reports waiting for the operator (status open).",
			}, func() float64 { open, _ := g.snapshot(); return float64(open) }),
			prometheus.NewGaugeFunc(prometheus.GaugeOpts{
				Name: "tuttifrutti_report_oldest_open_age_seconds",
				Help: "Age of the oldest open content report in seconds; 0 when none is open.",
			}, func() float64 { _, age := g.snapshot(); return age.Seconds() }),
		)
	}
	return g
}

func (g *ReportGauges) snapshot() (int, time.Duration) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.open <= 0 || g.oldest.IsZero() {
		return max(g.open, 0), 0
	}
	return g.open, max(g.now().Sub(g.oldest), 0)
}

// Set replaces what the gauges show (the database's answer).
func (g *ReportGauges) Set(st store.ReportStats) {
	g.mu.Lock()
	g.open, g.oldest = st.Open, st.OldestOpen
	g.mu.Unlock()
}

// ReportCreated implements httpapi.ReportObserver: one more open report, filed
// at the given time.
func (g *ReportGauges) ReportCreated(at time.Time) {
	g.mu.Lock()
	g.open++
	if g.oldest.IsZero() || at.Before(g.oldest) {
		g.oldest = at
	}
	g.mu.Unlock()
}

// Refresh reads the open reports from the store. A failure leaves the gauges as
// they were and is returned.
func (g *ReportGauges) Refresh(ctx context.Context, s store.Safety) (store.ReportStats, error) {
	st, err := s.ReportStats(ctx)
	if err != nil {
		return store.ReportStats{}, err
	}
	g.Set(st)
	return st, nil
}

// Values returns the open reports and the age of the oldest (what the gauges
// show now).
func (g *ReportGauges) Values() (open int, oldestAge time.Duration) { return g.snapshot() }
