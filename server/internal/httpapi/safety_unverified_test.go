package httpapi

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/AresiusXP/tutti-frutti/server/internal/room"
	"github.com/AresiusXP/tutti-frutti/server/internal/safety"
)

// A stranger who knows a room code but sends a roomInstanceId that does not
// match gets the room's ErrInstanceMismatch (the room checks the instance
// before the membership, so there is no oracle on who sits in it). The report is
// still taken, because that is also what a reporter of a room that ended and
// whose code was reused looks like (docs/DESIGN.md, "Reports"), but it must be
// uncorroborated evidence that names nobody, and it costs the same limits as
// any other report.
func TestAMismatchingInstanceIsAnUnverifiedReportWithoutATargetAndWithTheSameLimits(t *testing.T) {
	e := newSafetyEnv(t, safetyOpts{})
	e.rooms.reply = func(string, safety.EvidenceRequest) (safety.Evidence, error) {
		return safety.Evidence{}, room.ErrInstanceMismatch
	}
	b := e.browser()
	b.get("/api/session")

	send := func(i int, victim string) reply {
		return b.postJSON("/api/reports", reportBody(func(m map[string]any) {
			m["idempotencyKey"] = "stranger-key-" + string(rune('a'+i))
			m["ref"] = map[string]any{"playerId": victim, "category": 1, "round": 2}
		}))
	}
	for i, victim := range []string{"victim-one", "victim-two", "victim-three"} {
		if r := send(i, victim); r.StatusCode != http.StatusCreated {
			t.Fatalf("report %d = %d %s", i, r.StatusCode, r.body)
		}
	}
	reps := e.safe.reports()
	if len(reps) != 3 {
		t.Fatalf("%d reports stored, want 3", len(reps))
	}
	for _, rep := range reps {
		if rep.EvidenceVerified || rep.TargetGuest != "" || rep.TargetUser != "" || rep.MatchInstance != "" {
			t.Errorf("stored = %+v: a mismatching instance must be unverified and name nobody", rep)
		}
		var data safety.EvidenceData
		if err := json.Unmarshal(rep.Evidence, &data); err != nil {
			t.Fatal(err)
		}
		if data.Source != safety.SourceClient || data.Claimed == nil || data.Author != nil {
			t.Errorf("evidence = %s: want the client's claim only, no author", rep.Evidence)
		}
	}
	// Counted like any stored report.
	if got := e.reportCount("answer", "harassment"); got != 3 {
		t.Errorf("tuttifrutti_reports_total = %v, want 3: unverified reports count too", got)
	}
	// And limited like any other: the 4th within the minute is refused.
	if r := send(3, "victim-four"); r.StatusCode != http.StatusTooManyRequests || r.errCode(t) != "rate_limited" {
		t.Errorf("4th report = %d %s, want 429 rate_limited", r.StatusCode, r.body)
	}

	// Unverified and verified reports spend the same budget: a guest cannot get
	// extra reports by alternating them.
	other := e.browser()
	other.get("/api/session")
	post := func(key string) reply {
		return other.postJSON("/api/reports", reportBody(func(m map[string]any) { m["idempotencyKey"] = key }))
	}
	for i := range 2 {
		if r := post("mixed-key-" + string(rune('a'+i))); r.StatusCode != http.StatusCreated {
			t.Fatalf("unverified %d = %d %s", i, r.StatusCode, r.body)
		}
	}
	e.rooms.reply = func(string, safety.EvidenceRequest) (safety.Evidence, error) { return serverEvidence(), nil }
	if r := post("mixed-key-c"); r.StatusCode != http.StatusCreated {
		t.Fatalf("verified = %d %s", r.StatusCode, r.body)
	}
	if r := post("mixed-key-d"); r.StatusCode != http.StatusTooManyRequests {
		t.Errorf("4th report of a mixed run = %d %s, want 429", r.StatusCode, r.body)
	}
}
