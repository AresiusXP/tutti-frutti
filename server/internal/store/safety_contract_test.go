package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/AresiusXP/tutti-frutti/server/internal/safety"
)

var safetyBase = time.Date(2026, time.October, 30, 12, 0, 0, 123456000, time.UTC)

func reportID(n int) string { return fmt.Sprintf("00000000-0000-4000-8000-%012d", 5000+n) }

// sampleReport is a valid report; mods customise it.
func sampleReport(n int, mods ...func(*safety.Report)) safety.Report {
	r := safety.Report{
		ID:               reportID(n),
		ReporterGuest:    "guest-reporter",
		RoomInstance:     "11111111-1111-4111-8111-111111111111",
		MatchInstance:    "22222222-2222-4222-8222-222222222222",
		ContentKey:       "answer:r1:c0:guest-target",
		Kind:             safety.KindAnswer,
		TargetGuest:      "guest-target",
		Evidence:         json.RawMessage(`{"source":"server","text":"tomate"}`),
		EvidenceVerified: true,
		Reason:           safety.ReasonSpam,
		Comment:          "looks like spam",
		Status:           safety.StatusOpen,
		IdempotencyKey:   fmt.Sprintf("key-%08d", n),
		CreatedAt:        safetyBase,
		PurgeAt:          safety.PurgeAt(safetyBase, time.Time{}),
	}
	for _, m := range mods {
		m(&r)
	}
	return r
}

func mustCreate(t *testing.T, s Safety, r safety.Report) string {
	t.Helper()
	id, created, err := s.CreateReport(t.Context(), r)
	if err != nil || !created {
		t.Fatalf("CreateReport(%s) = %q, %v, %v", r.ID, id, created, err)
	}
	return id
}

// sameJSON reports whether two JSON documents are equivalent (PostgreSQL's jsonb
// does not keep the key order or the white space).
func sameJSON(t *testing.T, a, b []byte) bool {
	t.Helper()
	var x, y any
	if err := json.Unmarshal(a, &x); err != nil {
		t.Fatalf("not JSON: %q", a)
	}
	if err := json.Unmarshal(b, &y); err != nil {
		t.Fatalf("not JSON: %q", b)
	}
	return reflect.DeepEqual(x, y)
}

// testSafetyContract is the behaviour every Safety implementation must show.
// newSafety must return an empty backend that is cleaned up when the test ends.
func testSafetyContract(t *testing.T, newSafety func(t *testing.T) Safety) {
	t.Helper()

	t.Run("terms acceptances", func(t *testing.T) {
		s := newSafety(t)
		ctx := t.Context()
		if _, ok, err := s.TermsAcceptance(ctx, "guest-1", "2026-10"); ok || err != nil {
			t.Fatalf("before accepting: ok=%v err=%v", ok, err)
		}
		first := safety.TermsAcceptance{GuestID: "guest-1", Version: "2026-10", AcceptedAt: safetyBase, Locale: "nl", Age13Confirmed: true}
		got, err := s.AcceptTerms(ctx, first)
		if err != nil {
			t.Fatal(err)
		}
		if got.GuestID != "guest-1" || got.Version != "2026-10" || got.Locale != "nl" || !got.Age13Confirmed || !got.AcceptedAt.Equal(safetyBase) {
			t.Errorf("AcceptTerms = %+v", got)
		}
		stored, ok, err := s.TermsAcceptance(ctx, "guest-1", "2026-10")
		if err != nil || !ok || stored != got {
			t.Errorf("TermsAcceptance = %+v, %v, %v; want %+v", stored, ok, err, got)
		}

		// Idempotent: the first acceptance of a version is kept untouched.
		again, err := s.AcceptTerms(ctx, safety.TermsAcceptance{GuestID: "guest-1", Version: "2026-10",
			AcceptedAt: safetyBase.Add(time.Hour), Locale: "es", Age13Confirmed: true})
		if err != nil || again != got {
			t.Errorf("second AcceptTerms = %+v, %v; want the first record %+v", again, err, got)
		}

		// Another version and another guest are other rows.
		if _, ok, _ := s.TermsAcceptance(ctx, "guest-1", "2027-01"); ok {
			t.Error("an acceptance of one version counts for another")
		}
		if _, ok, _ := s.TermsAcceptance(ctx, "guest-2", "2026-10"); ok {
			t.Error("an acceptance of one guest counts for another")
		}
		if _, err := s.AcceptTerms(ctx, safety.TermsAcceptance{GuestID: "guest-1", Version: "2027-01", AcceptedAt: safetyBase, Locale: "en", Age13Confirmed: true}); err != nil {
			t.Fatal(err)
		}
		if a, ok, _ := s.TermsAcceptance(ctx, "guest-1", "2027-01"); !ok || a.Locale != "en" {
			t.Errorf("the second version = %+v, %v", a, ok)
		}
	})

	t.Run("terms acceptances are validated", func(t *testing.T) {
		s := newSafety(t)
		good := safety.TermsAcceptance{GuestID: "guest-1", Version: "2026-10", AcceptedAt: safetyBase, Locale: "en", Age13Confirmed: true}
		for name, mod := range map[string]func(*safety.TermsAcceptance){
			"no guest":        func(a *safety.TermsAcceptance) { a.GuestID = "" },
			"bad guest":       func(a *safety.TermsAcceptance) { a.GuestID = "a b" },
			"no version":      func(a *safety.TermsAcceptance) { a.Version = "" },
			"bad locale":      func(a *safety.TermsAcceptance) { a.Locale = "fr" },
			"age not checked": func(a *safety.TermsAcceptance) { a.Age13Confirmed = false },
			"no time":         func(a *safety.TermsAcceptance) { a.AcceptedAt = time.Time{} },
		} {
			a := good
			mod(&a)
			if _, err := s.AcceptTerms(t.Context(), a); !errors.Is(err, ErrInvalidTerms) {
				t.Errorf("%s: error = %v, want ErrInvalidTerms", name, err)
			}
		}
		if _, ok, _ := s.TermsAcceptance(t.Context(), "guest-1", "2026-10"); ok {
			t.Error("an invalid acceptance was stored")
		}
	})

	t.Run("a report round-trips", func(t *testing.T) {
		s := newSafety(t)
		user, target := "00000000-0000-4000-8000-0000000000aa", "00000000-0000-4000-8000-0000000000bb"
		want := sampleReport(1, func(r *safety.Report) { r.ReporterUser, r.TargetUser = user, target })
		id, created, err := s.CreateReport(t.Context(), want)
		if err != nil || !created || id != want.ID {
			t.Fatalf("CreateReport = %q, %v, %v", id, created, err)
		}
		got, err := s.GetReport(t.Context(), id)
		if err != nil {
			t.Fatal(err)
		}
		if !sameJSON(t, got.Evidence, want.Evidence) {
			t.Errorf("evidence = %s, want %s", got.Evidence, want.Evidence)
		}
		got.Evidence, want.Evidence = nil, nil
		if !got.CreatedAt.Equal(want.CreatedAt.Truncate(time.Microsecond)) || !got.PurgeAt.Equal(want.PurgeAt.Truncate(time.Microsecond)) || !got.ResolvedAt.IsZero() {
			t.Errorf("times = %v %v %v", got.CreatedAt, got.PurgeAt, got.ResolvedAt)
		}
		got.CreatedAt, want.CreatedAt, got.PurgeAt, want.PurgeAt = time.Time{}, time.Time{}, time.Time{}, time.Time{}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("report = %+v\nwant     %+v", got, want)
		}
	})

	t.Run("optional ids stay empty", func(t *testing.T) {
		s := newSafety(t)
		r := sampleReport(2, func(r *safety.Report) { r.MatchInstance, r.TargetGuest, r.RoomInstance = "", "", "" })
		mustCreate(t, s, r)
		got, err := s.GetReport(t.Context(), r.ID)
		if err != nil || got.ReporterUser != "" || got.MatchInstance != "" || got.TargetGuest != "" || got.TargetUser != "" || got.RoomInstance != "" {
			t.Errorf("optional fields = %+v, %v", got, err)
		}
	})

	t.Run("a retry returns the original id", func(t *testing.T) {
		s := newSafety(t)
		first := sampleReport(3)
		mustCreate(t, s, first)
		retry := sampleReport(4, func(r *safety.Report) { r.IdempotencyKey, r.Reason = first.IdempotencyKey, safety.ReasonHate })
		id, created, err := s.CreateReport(t.Context(), retry)
		if err != nil || created || id != first.ID {
			t.Fatalf("retry = %q, %v, %v; want %q, false", id, created, err, first.ID)
		}
		if _, err := s.GetReport(t.Context(), retry.ID); !errors.Is(err, ErrReportNotFound) {
			t.Errorf("the retry's own id exists: %v", err)
		}
		if got, _ := s.GetReport(t.Context(), first.ID); got.Reason != first.Reason {
			t.Errorf("a retry changed the stored report: %+v", got)
		}
		// The same key from another reporter is another report.
		other := sampleReport(5, func(r *safety.Report) { r.ReporterGuest, r.IdempotencyKey = "guest-other", first.IdempotencyKey })
		if id, created, err := s.CreateReport(t.Context(), other); err != nil || !created || id != other.ID {
			t.Errorf("another reporter with the same key = %q, %v, %v", id, created, err)
		}
	})

	t.Run("concurrent retries store one report", func(t *testing.T) {
		s := newSafety(t)
		const n = 8
		ids, created := make([]string, n), make([]bool, n)
		var wg sync.WaitGroup
		for i := range n {
			wg.Go(func() {
				r := sampleReport(100+i, func(r *safety.Report) { r.IdempotencyKey = "same-key-for-all" })
				var err error
				if ids[i], created[i], err = s.CreateReport(t.Context(), r); err != nil {
					t.Errorf("CreateReport: %v", err)
				}
			})
		}
		wg.Wait()
		stored := 0
		for i := range n {
			if created[i] {
				stored++
			}
			if ids[i] != ids[0] {
				t.Errorf("ids differ: %v", ids)
				break
			}
		}
		if stored != 1 {
			t.Errorf("%d goroutines created the report, want exactly 1", stored)
		}
	})

	t.Run("invalid reports are refused and nothing is stored", func(t *testing.T) {
		s := newSafety(t)
		long := strings.Repeat("ñ", safety.MaxCommentRunes+1)
		tests := map[string]func(*safety.Report){
			"id":              func(r *safety.Report) { r.ID = "nope" },
			"no reporter":     func(r *safety.Report) { r.ReporterGuest = "" },
			"reporter user":   func(r *safety.Report) { r.ReporterUser = "not-a-uuid" },
			"target user":     func(r *safety.Report) { r.TargetUser = "not-a-uuid" },
			"room instance":   func(r *safety.Report) { r.RoomInstance = "a b" },
			"match instance":  func(r *safety.Report) { r.MatchInstance = "a b" },
			"content key":     func(r *safety.Report) { r.ContentKey = "" },
			"kind":            func(r *safety.Report) { r.Kind = "video" },
			"target guest":    func(r *safety.Report) { r.TargetGuest = "a b" },
			"reason":          func(r *safety.Report) { r.Reason = "dislike" },
			"status":          func(r *safety.Report) { r.Status = "wontfix" },
			"short key":       func(r *safety.Report) { r.IdempotencyKey = "short" },
			"key charset":     func(r *safety.Report) { r.IdempotencyKey = "key with spaces" },
			"comment length":  func(r *safety.Report) { r.Comment = long },
			"comment control": func(r *safety.Report) { r.Comment = "a\x00b" },
			"no evidence":     func(r *safety.Report) { r.Evidence = nil },
			"evidence json":   func(r *safety.Report) { r.Evidence = json.RawMessage(`{"a":`) },
			"evidence NUL":    func(r *safety.Report) { r.Evidence = json.RawMessage(`{"text":"a\u0000b"}`) },
			"evidence size": func(r *safety.Report) {
				r.Evidence = json.RawMessage(`{"t":"` + strings.Repeat("x", maxEvidenceBytes) + `"}`)
			},
			"no created at":   func(r *safety.Report) { r.CreatedAt = time.Time{} },
			"purge too late":  func(r *safety.Report) { r.PurgeAt = r.CreatedAt.Add(safety.MaxRetention + time.Second) },
			"purge not after": func(r *safety.Report) { r.PurgeAt = r.CreatedAt },
		}
		for name, mod := range tests {
			r := sampleReport(10, mod)
			if _, _, err := s.CreateReport(t.Context(), r); !errors.Is(err, ErrInvalidReport) {
				t.Errorf("%s: error = %v, want ErrInvalidReport", name, err)
			}
		}
		if _, err := s.GetReport(t.Context(), reportID(10)); !errors.Is(err, ErrReportNotFound) {
			t.Errorf("an invalid report was stored: %v", err)
		}
	})

	t.Run("text that looks like a NUL escape is plain text", func(t *testing.T) {
		s := newSafety(t)
		// The six characters \u0000 typed by a player are escaped by encoding/json
		// as \\u0000: valid for jsonb and kept as typed.
		raw, err := json.Marshal(map[string]string{"text": `\u0000 and a quote " and <b>`})
		if err != nil {
			t.Fatal(err)
		}
		r := sampleReport(11, func(r *safety.Report) { r.Evidence = raw; r.Comment = `\u0000 <b>x</b> 'q'` })
		mustCreate(t, s, r)
		got, err := s.GetReport(t.Context(), r.ID)
		if err != nil || !sameJSON(t, got.Evidence, raw) || got.Comment != r.Comment {
			t.Errorf("stored = %s / %q, %v", got.Evidence, got.Comment, err)
		}
	})

	t.Run("resolving shortens the retention", func(t *testing.T) {
		s := newSafety(t)
		early, late := sampleReport(20), sampleReport(21)
		mustCreate(t, s, early)
		mustCreate(t, s, late)
		day := 24 * time.Hour
		if err := s.ResolveReport(t.Context(), early.ID, safety.StatusActioned, safetyBase.Add(2*day)); err != nil {
			t.Fatal(err)
		}
		if err := s.ResolveReport(t.Context(), late.ID, safety.StatusDismissed, safetyBase.Add(80*day)); err != nil {
			t.Fatal(err)
		}
		e, _ := s.GetReport(t.Context(), early.ID)
		l, _ := s.GetReport(t.Context(), late.ID)
		if e.Status != safety.StatusActioned || !e.ResolvedAt.Equal(safetyBase.Add(2*day).Truncate(time.Microsecond)) ||
			!e.PurgeAt.Equal(safetyBase.Add(32*day).Truncate(time.Microsecond)) {
			t.Errorf("early = %v %v %v", e.Status, e.ResolvedAt, e.PurgeAt)
		}
		if l.Status != safety.StatusDismissed || !l.PurgeAt.Equal(safetyBase.Add(90*day).Truncate(time.Microsecond)) {
			t.Errorf("late = %v %v: never later than 90 days after the report", l.Status, l.PurgeAt)
		}
		if err := s.ResolveReport(t.Context(), reportID(99), safety.StatusActioned, safetyBase); !errors.Is(err, ErrReportNotFound) {
			t.Errorf("resolving an unknown report = %v", err)
		}
		for _, status := range []string{safety.StatusOpen, "banana", ""} {
			if err := s.ResolveReport(t.Context(), early.ID, status, safetyBase); !errors.Is(err, ErrInvalidReport) {
				t.Errorf("resolving with status %q = %v", status, err)
			}
		}
	})

	t.Run("purging deletes what is due and frees the idempotency key", func(t *testing.T) {
		s := newSafety(t)
		old, fresh, resolved := sampleReport(30), sampleReport(31, func(r *safety.Report) {
			r.CreatedAt = safetyBase.Add(60 * 24 * time.Hour)
			r.PurgeAt = safety.PurgeAt(r.CreatedAt, time.Time{})
		}), sampleReport(32)
		mustCreate(t, s, old)
		mustCreate(t, s, fresh)
		mustCreate(t, s, resolved)
		if err := s.ResolveReport(t.Context(), resolved.ID, safety.StatusActioned, safetyBase.Add(10*24*time.Hour)); err != nil {
			t.Fatal(err)
		}
		// 89 days: nothing is due.
		if n, err := s.PurgeReports(t.Context(), safetyBase.Add(89*24*time.Hour)); err != nil || n != 1 {
			t.Fatalf("purge at day 89 = %d, %v; want only the report resolved at day 10 (due at day 40)", n, err)
		}
		if _, err := s.GetReport(t.Context(), resolved.ID); !errors.Is(err, ErrReportNotFound) {
			t.Errorf("the resolved report survived its purge time: %v", err)
		}
		// The 90th day is the limit, inclusive.
		if n, err := s.PurgeReports(t.Context(), safetyBase.Add(90*24*time.Hour)); err != nil || n != 1 {
			t.Fatalf("purge at day 90 = %d, %v, want 1", n, err)
		}
		if _, err := s.GetReport(t.Context(), old.ID); !errors.Is(err, ErrReportNotFound) {
			t.Errorf("the old report survived: %v", err)
		}
		if _, err := s.GetReport(t.Context(), fresh.ID); err != nil {
			t.Errorf("the fresh report was purged: %v", err)
		}
		if n, _ := s.PurgeReports(t.Context(), safetyBase.Add(90*24*time.Hour)); n != 0 {
			t.Errorf("a second purge deleted %d", n)
		}
		again := sampleReport(33, func(r *safety.Report) { r.IdempotencyKey = old.IdempotencyKey })
		if _, created, err := s.CreateReport(t.Context(), again); err != nil || !created {
			t.Errorf("the key of a purged report is not free: %v, %v", created, err)
		}
	})

	t.Run("a cancelled context stores nothing", func(t *testing.T) {
		s := newSafety(t)
		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		if _, _, err := s.CreateReport(ctx, sampleReport(40)); err == nil {
			t.Error("CreateReport with a cancelled context succeeded")
		}
		if _, err := s.AcceptTerms(ctx, safety.TermsAcceptance{GuestID: "g", Version: "v", AcceptedAt: safetyBase, Locale: "en", Age13Confirmed: true}); err == nil {
			t.Error("AcceptTerms with a cancelled context succeeded")
		}
		if _, err := s.GetReport(t.Context(), reportID(40)); !errors.Is(err, ErrReportNotFound) {
			t.Errorf("the report exists: %v", err)
		}
	})
}

// testReportsSurviveAccounts: reports name accounts by id only, without a
// foreign key, so deleting an account (or its room) leaves them untouched.
func testReportsSurviveAccounts(t *testing.T, b Backend) {
	t.Helper()
	u := mustUser(t, b, "sub-reported", "Beto")
	r := sampleReport(50, func(r *safety.Report) { r.TargetUser, r.ReporterUser = u.ID, u.ID })
	mustCreate(t, b, r)
	if err := b.DeleteUser(t.Context(), u.ID); err != nil {
		t.Fatal(err)
	}
	got, err := b.GetReport(t.Context(), r.ID)
	if err != nil || got.TargetUser != u.ID || got.ReporterUser != u.ID {
		t.Errorf("the report did not survive the deletion of the account: %+v, %v", got, err)
	}
	// And a report about a user that never existed is as good.
	ghost := sampleReport(51, func(r *safety.Report) { r.TargetUser = "00000000-0000-4000-8000-00000000dead" })
	mustCreate(t, b, ghost)
}
