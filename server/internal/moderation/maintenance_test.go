package moderation

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/AresiusXP/tutti-frutti/server/internal/safety"
	"github.com/AresiusXP/tutti-frutti/server/internal/store"
)

// fakeClock is a manual clock: After hands out a channel the test fires.
type fakeClock struct {
	mu    sync.Mutex
	now   time.Time
	waits []time.Duration
	fire  chan time.Time
	armed chan struct{}
}

func newFakeClock(now time.Time) *fakeClock {
	return &fakeClock{now: now, fire: make(chan time.Time), armed: make(chan struct{}, 10)}
}

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *fakeClock) set(t time.Time) {
	c.mu.Lock()
	c.now = t
	c.mu.Unlock()
}

func (c *fakeClock) After(d time.Duration) <-chan time.Time {
	c.mu.Lock()
	c.waits = append(c.waits, d)
	c.mu.Unlock()
	c.armed <- struct{}{}
	return c.fire
}

// stale seeds one record of each kind that is past its retention and one that is not.
func seedRetention(t *testing.T, mem *store.Memory, now time.Time) {
	t.Helper()
	ctx := context.Background()
	day := 24 * time.Hour
	id := func(base, n int) string { return fmt.Sprintf("00000000-0000-4000-8000-%012d", base+n) }

	for i, c := range []struct {
		created, resolved time.Time
	}{
		{now.Add(-91 * day), time.Time{}},        // 90 days after filing: due
		{now.Add(-40 * day), now.Add(-31 * day)}, // 30 days after resolving: due
		{now.Add(-40 * day), now.Add(-29 * day)}, // resolved 29 days ago: kept
		{now.Add(-10 * day), time.Time{}},        // open and recent: kept
	} {
		r := safety.Report{
			ID: id(5000, i), ReporterGuest: "reporter", ContentKey: "k", Kind: safety.KindPlayer, Evidence: []byte(`{}`),
			Reason: safety.ReasonOther, Status: safety.StatusOpen, IdempotencyKey: fmt.Sprintf("key-%08d", i),
			CreatedAt: c.created, PurgeAt: safety.PurgeAt(c.created, time.Time{}),
		}
		if _, _, err := mem.CreateReport(ctx, r); err != nil {
			t.Fatal(err)
		}
		if !c.resolved.IsZero() {
			if err := mem.ResolveReport(ctx, r.ID, safety.StatusDismissed, c.resolved); err != nil {
				t.Fatal(err)
			}
		}
	}
	for guest, at := range map[string]time.Time{"old": now.Add(-366 * day), "recent": now.Add(-364 * day)} {
		if _, err := mem.AcceptTerms(ctx, safety.TermsAcceptance{GuestID: guest, Version: "2026-10", AcceptedAt: at, Locale: "en", Age13Confirmed: true}); err != nil {
			t.Fatal(err)
		}
	}
	audit := func(n int, at time.Time) safety.Action {
		return safety.Action{ID: id(8000, n), At: at, Operator: "o", Action: safety.ActionRestrict, TargetKind: safety.TargetGuest, TargetID: "g"}
	}
	for i, c := range []struct {
		guest   string
		expires time.Time
		auditAt time.Time
	}{
		{"expired-long", now.Add(-91 * day), now.Add(-366 * day)}, // purged, and so is its audit record
		{"expired-recent", now.Add(-89 * day), now.Add(-10 * day)},
		{"active", time.Time{}, now.Add(-10 * day)},
	} {
		r := safety.Restriction{ID: id(7000, i), GuestID: c.guest, Reason: "r", Operator: "o", CreatedAt: now.Add(-400 * day), ExpiresAt: c.expires}
		if err := mem.CreateRestriction(ctx, r, audit(i, c.auditAt)); err != nil {
			t.Fatal(err)
		}
	}
}

func TestRunOncePurgesWhatTheRetentionRulesSay(t *testing.T) {
	now := time.Date(2026, 10, 30, 12, 0, 0, 0, time.UTC)
	mem := store.NewMemory()
	seedRetention(t, mem, now)
	reg := prometheus.NewRegistry()
	gauges := NewReportGauges(reg, func() time.Time { return now })
	var logs bytes.Buffer
	m := &Maintenance{
		Safety: mem, Mod: mem, Gauges: gauges, Clock: newFakeClock(now),
		Log: slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug})),
	}
	res := m.RunOnce(context.Background())
	if res != (Result{Reports: 2, Terms: 1, Restrictions: 1, Actions: 1}) {
		t.Errorf("result = %+v, want 2 reports, 1 acceptance, 1 restriction and 1 audit record", res)
	}
	for _, id := range []string{"00000000-0000-4000-8000-000000005000", "00000000-0000-4000-8000-000000005001"} {
		if _, err := mem.GetReport(context.Background(), id); !errors.Is(err, store.ErrReportNotFound) {
			t.Errorf("report %s survived", id)
		}
	}
	for _, id := range []string{"00000000-0000-4000-8000-000000005002", "00000000-0000-4000-8000-000000005003"} {
		if _, err := mem.GetReport(context.Background(), id); err != nil {
			t.Errorf("report %s was purged: %v", id, err)
		}
	}
	if _, ok, _ := mem.TermsAcceptance(context.Background(), "old", "2026-10"); ok {
		t.Error("the old acceptance survived")
	}
	if _, ok, _ := mem.TermsAcceptance(context.Background(), "recent", "2026-10"); !ok {
		t.Error("the recent acceptance was purged")
	}
	// The gauges were refreshed from the store: report 5003 is open (and 5002 is resolved).
	if open, age := gauges.Values(); open != 1 || age != 10*24*time.Hour {
		t.Errorf("gauges after the run = %d open, oldest %v", open, age)
	}
	// Only counts in the log.
	if !strings.Contains(logs.String(), "reports_purged=2") || !strings.Contains(logs.String(), "terms_purged=1") ||
		strings.Contains(logs.String(), "guest") || strings.Contains(logs.String(), "reporter") {
		t.Errorf("log = %s", logs.String())
	}
	if again := m.RunOnce(context.Background()); again != (Result{}) {
		t.Errorf("a second run purged %+v", again)
	}
}

// blocksSurvive: blocks are not purged by time; only an unblock removes one.
func TestMaintenanceNeverPurgesBlocks(t *testing.T) {
	now := time.Date(2026, 10, 30, 12, 0, 0, 0, time.UTC)
	mem := store.NewMemory()
	if _, _, err := mem.AddBlock(context.Background(), safety.Block{Blocker: "a", Blocked: "b", CreatedAt: now.Add(-5 * 365 * 24 * time.Hour)}, 10); err != nil {
		t.Fatal(err)
	}
	(&Maintenance{Safety: mem, Mod: mem, Clock: newFakeClock(now)}).RunOnce(context.Background())
	if bl, _ := mem.LoadBlocks(context.Background()); len(bl) != 1 {
		t.Errorf("blocks after the purge = %d: they last until the blocker removes them", len(bl))
	}
}

type failingPurge struct {
	*store.Memory
	failReports, failTerms, failMod bool
}

func (f *failingPurge) PurgeReports(ctx context.Context, now time.Time) (int64, error) {
	if f.failReports {
		return 0, errors.New("boom")
	}
	return f.Memory.PurgeReports(ctx, now)
}

func (f *failingPurge) PurgeTerms(ctx context.Context, before time.Time) (int64, error) {
	if f.failTerms {
		return 0, errors.New("boom")
	}
	return f.Memory.PurgeTerms(ctx, before)
}

func (f *failingPurge) PurgeModeration(ctx context.Context, now time.Time) (store.ModerationPurge, error) {
	if f.failMod {
		return store.ModerationPurge{}, errors.New("boom")
	}
	return f.Memory.PurgeModeration(ctx, now)
}

func TestARunWhoseStepFailsStillRunsTheOthers(t *testing.T) {
	now := time.Date(2026, 10, 30, 12, 0, 0, 0, time.UTC)
	mem := store.NewMemory()
	seedRetention(t, mem, now)
	f := &failingPurge{Memory: mem, failReports: true}
	m := &Maintenance{Safety: f, Mod: f, Clock: newFakeClock(now), Log: slog.New(slog.DiscardHandler)}
	res := m.RunOnce(context.Background())
	if res.Errors != 1 || res.Reports != 0 || res.Terms != 1 || res.Restrictions != 1 || res.Actions != 1 {
		t.Errorf("result = %+v: only the reports step failed", res)
	}
	f.failReports, f.failTerms, f.failMod = false, true, true
	res = m.RunOnce(context.Background())
	if res.Errors != 2 || res.Reports != 2 {
		t.Errorf("result = %+v", res)
	}
}

func TestRunRunsAtStartThenOnceADayAndStopsWithTheContext(t *testing.T) {
	now := time.Date(2026, 10, 30, 12, 0, 0, 0, time.UTC)
	mem := store.NewMemory()
	clock := newFakeClock(now)
	m := &Maintenance{Safety: mem, Mod: mem, Clock: clock, Log: slog.New(slog.DiscardHandler)}

	// Something becomes due between the first run and the next day.
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { m.Run(ctx); close(done) }()

	<-clock.armed // the run at start finished and the loop waits for a day
	seedRetention(t, mem, now)
	clock.fire <- now // the day passes
	<-clock.armed     // the second run finished
	if _, err := mem.GetReport(context.Background(), "00000000-0000-4000-8000-000000005000"); !errors.Is(err, store.ErrReportNotFound) {
		t.Error("the second run did not purge")
	}
	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not stop with its context")
	}
	for i, w := range clock.waits {
		if w != 24*time.Hour {
			t.Errorf("wait #%d = %v, want one day", i, w)
		}
	}
	if len(clock.waits) < 2 {
		t.Errorf("waits = %v", clock.waits)
	}
}

func TestReportGauges(t *testing.T) {
	now := time.Date(2026, 10, 30, 12, 0, 0, 0, time.UTC)
	clock := now
	reg := prometheus.NewRegistry()
	g := NewReportGauges(reg, func() time.Time { return clock })
	value := func(name string) float64 {
		mfs, err := reg.Gather()
		if err != nil {
			t.Fatal(err)
		}
		for _, mf := range mfs {
			if mf.GetName() == name {
				return mf.GetMetric()[0].GetGauge().GetValue()
			}
		}
		t.Fatalf("%s is not registered", name)
		return 0
	}
	if value("tuttifrutti_reports_open") != 0 || value("tuttifrutti_report_oldest_open_age_seconds") != 0 {
		t.Error("an empty server must show zeros")
	}
	g.ReportCreated(now.Add(-time.Hour))
	g.ReportCreated(now) // a newer report does not make the oldest newer
	if value("tuttifrutti_reports_open") != 2 || value("tuttifrutti_report_oldest_open_age_seconds") != 3600 {
		t.Errorf("after two reports: %v open, oldest %v s", value("tuttifrutti_reports_open"), value("tuttifrutti_report_oldest_open_age_seconds"))
	}
	clock = now.Add(time.Hour) // the age grows between refreshes: it is computed at the scrape
	if got := value("tuttifrutti_report_oldest_open_age_seconds"); got != 7200 {
		t.Errorf("age = %v, want 7200", got)
	}
	g.Set(store.ReportStats{})
	if value("tuttifrutti_reports_open") != 0 || value("tuttifrutti_report_oldest_open_age_seconds") != 0 {
		t.Error("no open reports must show zeros")
	}
	NewReportGauges(nil, nil) // no registry: no metrics, no panic
}
