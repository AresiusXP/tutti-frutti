package safety

import (
	"sync"
	"testing"
	"time"
)

func TestValidBlockTarget(t *testing.T) {
	for _, tc := range []struct {
		name            string
		blocker, target string
		want            bool
	}{
		{"another guest", "guest-a", "guest-b", true},
		{"self", "guest-a", "guest-a", false},
		{"empty", "guest-a", "", false},
		{"automated seat", "guest-a", "bot:Ana", false},
		{"malformed", "guest-a", "a b", false},
		{"too long", "guest-a", string(make([]byte, MaxIDLen+1)), false},
		{"base64url id", "guest-a", "Zm9vLWJhci1iYXo_Zm9vLQ", true},
	} {
		if got := ValidBlockTarget(tc.blocker, tc.target); got != tc.want {
			t.Errorf("%s: ValidBlockTarget(%q, %q) = %v", tc.name, tc.blocker, tc.target, got)
		}
	}
}

func TestRestrictionTimes(t *testing.T) {
	t0 := time.Date(2026, 10, 30, 12, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		name    string
		r       Restriction
		now     time.Time
		active  bool
		endedAt time.Time
	}{
		{"until revoked", Restriction{CreatedAt: t0}, t0.Add(1000 * time.Hour), true, time.Time{}},
		{"timed, running", Restriction{ExpiresAt: t0.Add(time.Hour)}, t0, true, t0.Add(time.Hour)},
		{"timed, expired", Restriction{ExpiresAt: t0.Add(time.Hour)}, t0.Add(time.Hour), false, t0.Add(time.Hour)},
		{"revoked", Restriction{RevokedAt: t0}, t0, false, t0},
		{"revoked before its expiry", Restriction{ExpiresAt: t0.Add(time.Hour), RevokedAt: t0}, t0, false, t0},
		{"expired before it was revoked", Restriction{ExpiresAt: t0, RevokedAt: t0.Add(time.Hour)}, t0.Add(2 * time.Hour), false, t0},
	} {
		if got := tc.r.ActiveAt(tc.now); got != tc.active {
			t.Errorf("%s: ActiveAt = %v", tc.name, got)
		}
		if got := tc.r.EndedAt(); !got.Equal(tc.endedAt) {
			t.Errorf("%s: EndedAt = %v, want %v", tc.name, got, tc.endedAt)
		}
	}
	if got := RestrictionPurgeBefore(t0); !got.Equal(t0.Add(-90 * 24 * time.Hour)) {
		t.Errorf("RestrictionPurgeBefore = %v", got)
	}
	if got := AuditPurgeBefore(t0); !got.Equal(t0.Add(-365 * 24 * time.Hour)) {
		t.Errorf("AuditPurgeBefore = %v", got)
	}
}

func TestValidOperator(t *testing.T) {
	for s, want := range map[string]bool{
		"owner": true, "a.b-c_d@e": true, "": false, "two words": false, "ñ": false, "semi;colon": false,
		"x" + string(make([]byte, 64)): false,
	} {
		if got := ValidOperator(s); got != want {
			t.Errorf("ValidOperator(%q) = %v", s, got)
		}
	}
}

func TestPolicyBlocks(t *testing.T) {
	p := NewPolicy(nil)
	if p.Conflict("a", "b") || p.Blocks("a", "b") {
		t.Fatal("an empty policy blocks")
	}
	v0 := p.Version()
	p.AddBlock("a", "b")
	if !p.Blocks("a", "b") || p.Blocks("b", "a") {
		t.Error("a block is directed")
	}
	if !p.Conflict("a", "b") || !p.Conflict("b", "a") {
		t.Error("a block is enforced in both directions")
	}
	if p.Conflict("a", "c") || p.Conflict("a", "a") {
		t.Error("unrelated or identical guests conflict")
	}
	p.AddBlock("a", "b") // idempotent
	if n, _ := p.Counts(); n != 1 {
		t.Errorf("blocks = %d, want 1", n)
	}
	if p.Version() <= v0 {
		t.Error("the version must grow with every change")
	}
	p.RemoveBlock("a", "b")
	p.RemoveBlock("a", "b") // idempotent
	if p.Conflict("a", "b") {
		t.Error("an unblock did not take effect")
	}
	if n, _ := p.Counts(); n != 0 {
		t.Errorf("blocks = %d, want 0", n)
	}
}

func TestPolicyRestrictions(t *testing.T) {
	t0 := time.Date(2026, 10, 30, 12, 0, 0, 0, time.UTC)
	now := t0
	p := NewPolicy(func() time.Time { return now })
	p.AddRestriction(Restriction{ID: "r1", GuestID: "g1", UserID: "u1", ExpiresAt: t0.Add(time.Hour)})
	p.AddRestriction(Restriction{ID: "r2", GuestID: "g2"})
	if !p.Restricted("g1") || !p.Restricted("g2") || !p.RestrictedUser("u1") || p.Restricted("g3") || p.RestrictedUser("u2") {
		t.Error("restrictions in force are not reported")
	}
	if _, active := p.Counts(); active != 2 {
		t.Errorf("active restrictions = %d, want 2", active)
	}
	now = t0.Add(time.Hour) // r1 expires by the clock, nothing has to run
	if p.Restricted("g1") || p.RestrictedUser("u1") || !p.Restricted("g2") {
		t.Error("an expired restriction still applies")
	}
	if _, active := p.Counts(); active != 1 {
		t.Errorf("active restrictions = %d, want 1", active)
	}
	p.RemoveRestriction("r2")
	if p.Restricted("g2") {
		t.Error("a revoked restriction still applies")
	}
	if got, ok := p.RestrictionOfGuest("g2"); ok || got.ID != "" {
		t.Errorf("RestrictionOfGuest = %+v, %v", got, ok)
	}
	if p.Restricted("") || p.RestrictedUser("") {
		t.Error("the empty id is restricted")
	}
}

func TestPolicyLoadReplacesEverything(t *testing.T) {
	p := NewPolicy(nil)
	p.AddBlock("old", "old2")
	p.AddRestriction(Restriction{ID: "old", GuestID: "old"})
	p.Load(
		[]Block{{Blocker: "a", Blocked: "b"}, {Blocker: "a", Blocked: "c"}},
		[]Restriction{{ID: "r", GuestID: "g", UserID: "u"}},
	)
	if p.Conflict("old", "old2") || p.Restricted("old") {
		t.Error("Load kept the previous records")
	}
	if !p.Blocks("a", "b") || !p.Blocks("a", "c") || !p.Restricted("g") || !p.RestrictedUser("u") {
		t.Error("Load did not index the records")
	}
}

func TestNilPolicyAllowsEverything(t *testing.T) {
	var p *Policy
	p.AddBlock("a", "b")
	p.RemoveBlock("a", "b")
	p.AddRestriction(Restriction{ID: "x", GuestID: "g"})
	p.RemoveRestriction("x")
	p.Load(nil, nil)
	if p.Blocks("a", "b") || p.Conflict("a", "b") || p.Restricted("g") || p.RestrictedUser("u") || p.Version() != 0 {
		t.Error("a nil policy restricts")
	}
	if b, r := p.Counts(); b != 0 || r != 0 {
		t.Errorf("Counts = %d, %d", b, r)
	}
}

func TestPolicyConcurrentUse(t *testing.T) {
	p := NewPolicy(nil)
	var wg sync.WaitGroup
	for i := range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			a := string(rune('a' + i))
			for range 500 {
				p.AddBlock(a, "z")
				_ = p.Conflict("z", a)
				p.AddRestriction(Restriction{ID: a, GuestID: a})
				_ = p.Restricted(a)
				p.RemoveBlock(a, "z")
				p.RemoveRestriction(a)
				_, _ = p.Counts()
			}
		}()
	}
	wg.Wait()
	if b, r := p.Counts(); b != 0 || r != 0 {
		t.Errorf("after everything was removed: %d blocks, %d restrictions", b, r)
	}
}
