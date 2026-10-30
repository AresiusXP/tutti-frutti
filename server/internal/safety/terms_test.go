package safety

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

// fakeStore is a TermsStore that can fail and counts its lookups.
type fakeStore struct {
	mu      sync.Mutex
	rows    map[[2]string]TermsAcceptance
	fail    error
	lookups int
}

func newFakeStore() *fakeStore { return &fakeStore{rows: map[[2]string]TermsAcceptance{}} }

func (f *fakeStore) TermsAcceptance(_ context.Context, guest, version string) (TermsAcceptance, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.lookups++
	if f.fail != nil {
		return TermsAcceptance{}, false, f.fail
	}
	a, ok := f.rows[[2]string{guest, version}]
	return a, ok, nil
}

func (f *fakeStore) AcceptTerms(_ context.Context, a TermsAcceptance) (TermsAcceptance, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.fail != nil {
		return TermsAcceptance{}, f.fail
	}
	k := [2]string{a.GuestID, a.Version}
	if old, ok := f.rows[k]; ok && !AcceptanceExpired(old.AcceptedAt, a.AcceptedAt) {
		return old, nil
	}
	f.rows[k] = a
	return a, nil
}

func (f *fakeStore) setFail(err error) {
	f.mu.Lock()
	f.fail = err
	f.mu.Unlock()
}

func (f *fakeStore) lookupCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.lookups
}

var okInput = AcceptInput{Version: TermsVersion, Locale: "en", Age13Confirmed: true}

func TestAcceptInputValidate(t *testing.T) {
	tests := []struct {
		name string
		in   AcceptInput
		want error
	}{
		{"valid", okInput, nil},
		{"other version", AcceptInput{Version: "2020-01", Locale: "en", Age13Confirmed: true}, ErrVersionMismatch},
		{"no version", AcceptInput{Locale: "en", Age13Confirmed: true}, ErrVersionMismatch},
		{"unknown locale", AcceptInput{Version: TermsVersion, Locale: "fr", Age13Confirmed: true}, ErrInvalidLocale},
		{"locale is case sensitive", AcceptInput{Version: TermsVersion, Locale: "EN", Age13Confirmed: true}, ErrInvalidLocale},
		{"no locale", AcceptInput{Version: TermsVersion, Age13Confirmed: true}, ErrInvalidLocale},
		{"age not confirmed", AcceptInput{Version: TermsVersion, Locale: "es"}, ErrAgeNotConfirmed},
		{"version checked first", AcceptInput{Version: "x", Locale: "fr"}, ErrVersionMismatch},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.in.Validate(TermsVersion); !errors.Is(got, tc.want) {
				t.Errorf("Validate = %v, want %v", got, tc.want)
			}
		})
	}
	for _, l := range Locales {
		if !ValidLocale(l) {
			t.Errorf("locale %q is not valid", l)
		}
	}
}

func TestTermsGate(t *testing.T) {
	ctx := context.Background()
	t0 := time.Date(2026, 10, 30, 12, 0, 0, 0, time.UTC)
	clock := t0
	newGate := func() (*Terms, *fakeStore) {
		fs := newFakeStore()
		return NewTerms(fs, func() time.Time { return clock }), fs
	}

	t.Run("a guest that never accepted is not accepted", func(t *testing.T) {
		g, _ := newGate()
		if ok, err := g.Accepted(ctx, "guest-1"); ok || err != nil {
			t.Fatalf("Accepted = %v, %v", ok, err)
		}
		if ok, err := g.Accepted(ctx, ""); ok || err != nil {
			t.Fatalf("an empty guest id = %v, %v", ok, err)
		}
	})

	t.Run("accept is idempotent and keeps the first record", func(t *testing.T) {
		g, _ := newGate()
		first, err := g.Accept(ctx, "guest-1", AcceptInput{Version: TermsVersion, Locale: "nl", Age13Confirmed: true})
		if err != nil {
			t.Fatal(err)
		}
		if first.Version != TermsVersion || first.Locale != "nl" || !first.Age13Confirmed || !first.AcceptedAt.Equal(t0) || first.GuestID != "guest-1" {
			t.Errorf("acceptance = %+v", first)
		}
		clock = t0.Add(time.Hour)
		again, err := g.Accept(ctx, "guest-1", AcceptInput{Version: TermsVersion, Locale: "es", Age13Confirmed: true})
		clock = t0
		if err != nil {
			t.Fatal(err)
		}
		if again != first {
			t.Errorf("a second acceptance changed the record: %+v, was %+v", again, first)
		}
		if ok, _ := g.Accepted(ctx, "guest-1"); !ok {
			t.Error("not accepted after accepting")
		}
	})

	t.Run("invalid input stores nothing", func(t *testing.T) {
		g, fs := newGate()
		for _, in := range []AcceptInput{
			{Version: "old", Locale: "en", Age13Confirmed: true},
			{Version: TermsVersion, Locale: "xx", Age13Confirmed: true},
			{Version: TermsVersion, Locale: "en"},
		} {
			if _, err := g.Accept(ctx, "guest-1", in); err == nil {
				t.Errorf("%+v accepted", in)
			}
		}
		if _, err := g.Accept(ctx, "", okInput); !errors.Is(err, ErrNoGuest) {
			t.Errorf("no guest = %v", err)
		}
		if len(fs.rows) != 0 {
			t.Errorf("rows stored: %v", fs.rows)
		}
	})

	t.Run("an acceptance of another version does not count", func(t *testing.T) {
		g, fs := newGate()
		fs.rows[[2]string{"guest-1", "2020-01"}] = TermsAcceptance{GuestID: "guest-1", Version: "2020-01", Age13Confirmed: true}
		if ok, err := g.Accepted(ctx, "guest-1"); ok || err != nil {
			t.Errorf("Accepted = %v, %v", ok, err)
		}
	})

	t.Run("a failing store is an error, never not accepted, and is not cached", func(t *testing.T) {
		g, fs := newGate()
		boom := errors.New("database down")
		fs.setFail(boom)
		if ok, err := g.Accepted(ctx, "guest-1"); ok || !errors.Is(err, boom) {
			t.Fatalf("Accepted = %v, %v; want the store's error", ok, err)
		}
		fs.setFail(nil)
		if _, err := g.Accept(ctx, "guest-1", okInput); err != nil {
			t.Fatal(err)
		}
		fs.setFail(boom)
		// Accepted guests are remembered for the life of the process: the
		// outage does not lock them out.
		if ok, err := g.Accepted(ctx, "guest-1"); !ok || err != nil {
			t.Errorf("cached Accepted during an outage = %v, %v", ok, err)
		}
		if _, err := g.Accept(ctx, "guest-2", okInput); !errors.Is(err, boom) {
			t.Errorf("Accept during an outage = %v", err)
		}
		if ok, err := g.Accepted(ctx, "guest-2"); ok || !errors.Is(err, boom) {
			t.Errorf("an unknown guest during an outage = %v, %v", ok, err)
		}
	})

	t.Run("positive answers are cached, negative ones are not", func(t *testing.T) {
		g, fs := newGate()
		for range 3 {
			if ok, _ := g.Accepted(ctx, "never"); ok {
				t.Fatal("accepted")
			}
		}
		if n := fs.lookupCount(); n != 3 {
			t.Errorf("lookups of a guest that never accepted = %d, want 3 (not cached)", n)
		}
		fs.rows[[2]string{"yes", TermsVersion}] = TermsAcceptance{GuestID: "yes", Version: TermsVersion, AcceptedAt: t0, Age13Confirmed: true}
		before := fs.lookupCount()
		for range 5 {
			if ok, _ := g.Accepted(ctx, "yes"); !ok {
				t.Fatal("not accepted")
			}
		}
		if n := fs.lookupCount() - before; n != 1 {
			t.Errorf("lookups of an accepted guest = %d, want 1", n)
		}
	})

	t.Run("an acceptance older than a year no longer counts and the cache expires with it", func(t *testing.T) {
		g, fs := newGate()
		defer func() { clock = t0 }()
		if _, err := g.Accept(ctx, "guest-1", okInput); err != nil {
			t.Fatal(err)
		}
		clock = t0.Add(AcceptanceTTL - time.Second)
		if ok, err := g.Accepted(ctx, "guest-1"); !ok || err != nil {
			t.Fatalf("one second before the year: %v, %v", ok, err)
		}
		before := fs.lookupCount()
		clock = t0.Add(AcceptanceTTL)
		if ok, err := g.Accepted(ctx, "guest-1"); ok || err != nil {
			t.Fatalf("after a year the cached acceptance must be gone: %v, %v", ok, err)
		}
		if fs.lookupCount() == before {
			t.Error("an expired cache entry must be checked against the store, not trusted")
		}
		if _, ok, _ := g.Acceptance(ctx, "guest-1"); ok {
			t.Error("Acceptance reports an expired row as accepted")
		}
		// Accepting again replaces the stale row and counts for another year.
		again, err := g.Accept(ctx, "guest-1", AcceptInput{Version: TermsVersion, Locale: "es", Age13Confirmed: true})
		if err != nil || !again.AcceptedAt.Equal(clock) || again.Locale != "es" {
			t.Fatalf("re-accept = %+v, %v", again, err)
		}
		if ok, _ := g.Accepted(ctx, "guest-1"); !ok {
			t.Error("not accepted after re-accepting")
		}
	})

	t.Run("expiry is a pure function of the two times", func(t *testing.T) {
		at := t0
		for _, tc := range []struct {
			now  time.Time
			want bool
		}{
			{at, false}, {at.Add(AcceptanceTTL - time.Nanosecond), false}, {at.Add(AcceptanceTTL), true}, {at.Add(2 * AcceptanceTTL), true},
		} {
			if got := AcceptanceExpired(at, tc.now); got != tc.want {
				t.Errorf("AcceptanceExpired(now = at+%v) = %v", tc.now.Sub(at), got)
			}
		}
		if got := AcceptancePurgeBefore(t0); !got.Equal(t0.Add(-AcceptanceTTL)) {
			t.Errorf("AcceptancePurgeBefore = %v", got)
		}
	})

	t.Run("the cache is bounded", func(t *testing.T) {
		g, _ := newGate()
		for i := range maxCached + 10 {
			g.remember(string(rune('a'+i%26))+time.Duration(i).String(), t0)
		}
		g.mu.Lock()
		n := len(g.accepted)
		g.mu.Unlock()
		if n > maxCached {
			t.Errorf("cache holds %d guests, bound %d", n, maxCached)
		}
	})
}
