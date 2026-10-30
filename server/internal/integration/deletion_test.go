package integration

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"sync/atomic"
	"testing"
	"time"

	"github.com/AresiusXP/tutti-frutti/server/internal/api"
	"github.com/AresiusXP/tutti-frutti/server/internal/auth/authtest"
	"github.com/AresiusXP/tutti-frutti/server/internal/game"
	"github.com/AresiusXP/tutti-frutti/server/internal/judge"
	"github.com/AresiusXP/tutti-frutti/server/internal/protocol"
	"github.com/AresiusXP/tutti-frutti/server/internal/room"
	"github.com/AresiusXP/tutti-frutti/server/internal/store"
)

// stubJudge approves every answer and counts the questions.
type stubJudge struct{ calls atomic.Int32 }

func (j *stubJudge) Judge(_ context.Context, req judge.Request) ([]judge.Result, error) {
	j.calls.Add(1)
	out := make([]judge.Result, len(req.Answers))
	for i := range out {
		out[i] = judge.Result{Known: true, Valid: true, Reason: "looks right"}
	}
	return out, nil
}

// sessionPair is the "tf_session=..." pair of a browser's Cookie header.
func sessionPair(t *testing.T, b *browser) string {
	t.Helper()
	c := b.cookie("tf_session")
	if c == nil {
		t.Fatalf("%s holds no session cookie", b.p.name)
	}
	return (&http.Cookie{Name: c.Name, Value: c.Value}).String()
}

func (s *stack) skipped(source string) float64 {
	return s.metricValue("tuttifrutti_stats_skipped_users_total", map[string]string{"source": source})
}

// TestDeletingAnAccountWhileAGameIsOn: Ana (the host, signed in) and Beto (signed
// in) play with Cami (a guest). Ana asks the AI judge, then deletes her account
// from another tab while her socket is open. From the response on, that socket is
// a guest's (no AI judge, no statistics), a copy of her cookie identifies nobody
// anywhere, her seat and its scores stay, the others are credited and a new
// login with the same Google account is a new, empty account.
func TestDeletingAnAccountWhileAGameIsOn(t *testing.T) {
	forEachStore(t, func(t *testing.T, open func(*testing.T) store.Store) {
		backend, ok := open(t).(store.Backend)
		if !ok {
			t.Fatal("the store does not keep accounts")
		}
		flaky := &flakyBackend{Backend: backend}
		idp := authtest.New(t, "123-integration.apps.googleusercontent.com", "fake-integration-secret")
		j := &stubJudge{}
		s := newStack(t, stackOpts{store: flaky, idp: idp, hub: func(c *room.Config) { c.Judge = j }})

		anaB, betoB, camiB := s.newBrowser("Ana"), s.newBrowser("Beto"), s.newBrowser("Cami")
		googleAna := authtest.User{Sub: "100000000000000000011", Name: "Ana García"}
		googleBeto := authtest.User{Sub: "100000000000000000012", Name: "Beto Pérez"}
		anaB.googleLogin("/", googleAna)
		betoB.googleLogin("/", googleBeto)
		anaMe, _ := anaB.me()
		betoMe, _ := betoB.me()
		copied := sessionPair(t, anaB) // the cookie somebody could copy to another device

		tb := newTableOf(t, s, 0, anaB.p, betoB.p, camiB.p)
		tb.advanceTo(stageReview)

		// While the account exists the host may ask the judge.
		if st := tb.seats[0].c.lastState(); !st.CanJudge {
			t.Fatal("the signed-in host is not offered the AI judge")
		}
		tb.mustCmd(0, game.CmdAskJudge, game.AskJudge{Category: 0})
		tb.waitAll("the verdicts", func(v game.View) bool { return v.Round != nil && len(v.Round.Verdicts) > 0 })
		if got := j.calls.Load(); got != 1 {
			t.Fatalf("judge calls = %d, want 1", got)
		}

		// ---- the deletion, from another tab ----
		if r := anaB.do(http.MethodDelete, "/api/me", ""); r.status != http.StatusNoContent {
			t.Fatalf("DELETE /api/me = %d %s", r.status, r.body)
		}

		// The open socket lost the account by the time the response arrived: its
		// next question is refused, and its state says so.
		tb.wantCmdError(0, protocol.ErrLoginRequired, game.CmdAskJudge, game.AskJudge{Category: 1})
		st := tb.seats[0].c.waitState("canJudge revoked", func(st *protocol.State) bool { return !st.CanJudge })
		if got := j.calls.Load(); got != 1 {
			t.Errorf("the judge was asked %d times, the second by a deleted account", got)
		}
		// The seat stays: host, name, three players, scores.
		if st.View.HostID != anaB.p.id || len(st.View.Players) != 3 {
			t.Errorf("host %q players %d after the deletion", st.View.HostID, len(st.View.Players))
		}
		if seat, ok := seatOf(st.View, anaB.p.id); !ok || seat.Name != "Ana" || !seat.Connected {
			t.Errorf("Ana's seat = %+v, %v", seat, ok)
		}
		// Another signed-in player keeps theirs.
		if st := tb.seats[1].c.lastState(); !st.CanJudge {
			t.Error("Beto lost the AI judge because Ana deleted her account")
		}

		// ---- a copy of the cookie ----
		thief := s.newPlayer("Thief")
		thief.cookie += "; " + copied
		if r := s.do(http.MethodGet, "/api/me", nil, withCookie(thief)); r.status != http.StatusUnauthorized {
			t.Errorf("GET /api/me with the copied cookie = %d", r.status)
		}
		if r := s.do(http.MethodGet, "/api/session", nil, withCookie(thief)); r.status != http.StatusOK {
			t.Errorf("GET /api/session = %d", r.status)
		} else {
			var info api.SessionInfo
			r.jsonBody(t, &info)
			if info.LoggedIn {
				t.Error("the copied cookie is logged in on /api/session")
			}
		}
		code2 := s.createRoom(thief, "")
		tc := s.connect(thief)
		if joined := tc.join(code2); joined.CanJudge {
			t.Error("a socket opened with the copied cookie can ask the AI judge")
		}
		eventually(t, "the thief's room in the snapshots", func() bool {
			p := persistedOf(t, s.store, code2)
			return bytes.Contains(p.Game, []byte("Thief"))
		})
		if p := persistedOf(t, s.store, code2); len(p.Users) != 0 || len(p.Accounts) != 0 {
			t.Errorf("the copied cookie linked a seat to an account: users %v accounts %v", p.Users, p.Accounts)
		}

		// ---- the game goes on and ends ----
		tb.advanceTo(stageSummary)
		tb.playSecondRound()
		tb.mustCmd(0, game.CmdNextRound, nil)
		tb.waitPhase(game.PhaseFinal)
		wantInts(t, "final totals", totalScores(tb.anyView(), tb.ids()...), []int{50, 30, 40})

		eventually(t, "Beto's game", func() bool {
			stats, err := backend.UserStats(context.Background(), betoMe.ID)
			return err == nil && stats.GamesPlayed == 1
		})
		if stats, _ := backend.UserStats(context.Background(), betoMe.ID); stats.TotalPoints != 30 || stats.Wins != 0 {
			t.Errorf("Beto's stats = %+v, want 30 points and no win", stats)
		}
		if _, err := backend.UserStats(context.Background(), anaMe.ID); !errors.Is(err, store.ErrUserNotFound) {
			t.Errorf("Ana's statistics came back: %v", err)
		}
		flaky.mu.Lock()
		for _, att := range flaky.attempts {
			for _, res := range att.Results {
				if res.UserID == anaMe.ID {
					t.Errorf("a game record credits the deleted account: %+v", att)
				}
			}
		}
		flaky.mu.Unlock()
		if tb.metric("error") != 0 || tb.metric("dropped") != 0 {
			t.Errorf("recorder: error=%v dropped=%v", tb.metric("error"), tb.metric("dropped"))
		}
		for _, src := range []string{"forgotten", "restore", "store"} {
			if got := s.skipped(src); got != 0 {
				t.Errorf("skipped{%s} = %v: the room dropped Ana's link before the game ended, nothing was left to skip", src, got)
			}
		}
		if p := persistedOf(t, s.store, tb.code); p.Users[anaB.p.id] != "" || p.Accounts[anaB.p.id] != "" {
			t.Errorf("the snapshot still links Ana's seat: users %v accounts %v", p.Users, p.Accounts)
		}

		// ---- the same Google account again ----
		anaB.googleLogin("/", googleAna)
		again, _ := anaB.me()
		if again.ID == anaMe.ID || again.Profile != nil || anaB.stats() != (api.Stats{}) {
			t.Errorf("the new account %+v (stats %+v) is not new and empty", again, anaB.stats())
		}
		if r := s.do(http.MethodGet, "/api/me", nil, withCookie(thief)); r.status != http.StatusUnauthorized {
			t.Errorf("the copied cookie works again after the new login: %d", r.status)
		}
	})
}

// TestAnAccountDeletedWhileTheServerIsDown: the final snapshot names the account
// and nobody could scrub it. The next process looks the accounts up before it
// rebuilds the room: the seat is back, the account links and the deleted user's
// share of the finished game are not, and the game is still recorded for the
// others.
func TestAnAccountDeletedWhileTheServerIsDown(t *testing.T) {
	forEachStore(t, func(t *testing.T, open func(*testing.T) store.Store) {
		backend, ok := open(t).(store.Backend)
		if !ok {
			t.Fatal("the store does not keep accounts")
		}
		flaky := &flakyBackend{Backend: backend}
		flaky.setError(errors.New("the statistics store is down")) // the game stays in the outbox
		idp := authtest.New(t, "123-integration.apps.googleusercontent.com", "fake-integration-secret")
		s := newStack(t, stackOpts{store: flaky, idp: idp})

		anaB, betoB, camiB := s.newBrowser("Ana"), s.newBrowser("Beto"), s.newBrowser("Cami")
		anaB.googleLogin("/", authtest.User{Sub: "100000000000000000021", Name: "Ana García"})
		betoB.googleLogin("/", authtest.User{Sub: "100000000000000000022", Name: "Beto Pérez"})
		anaMe, _ := anaB.me()
		betoMe, _ := betoB.me()

		tb := newTableOf(t, s, 0, anaB.p, betoB.p, camiB.p)
		tb.advanceTo(stageSummary)
		tb.playSecondRound()
		tb.mustCmd(0, game.CmdNextRound, nil)
		tb.waitPhase(game.PhaseFinal)
		eventually(t, "the finished game in the snapshot", func() bool {
			p := persistedOf(t, s.store, tb.code)
			return len(p.Pending) == 1 && len(p.Users) == 2 && len(p.Accounts) == 2
		})
		if p := persistedOf(t, s.store, tb.code); len(p.Pending[0].Results) != 2 {
			t.Fatalf("setup: the outbox = %+v", p.Pending)
		}

		// The server stops; while it is down Ana deletes her account (here: the
		// store, which is all a restart sees).
		oldAttempts := 0
		tb.restartWith(time.Minute, func() {
			flaky.mu.Lock()
			oldAttempts = len(flaky.attempts)
			flaky.mu.Unlock()
			if err := backend.DeleteUser(context.Background(), anaMe.ID); err != nil {
				t.Fatal(err)
			}
		})

		if got := tb.s.skipped("restore"); got != 1 {
			t.Errorf("skipped{restore} = %v, want 1 (Ana's share of the finished game)", got)
		}
		eventually(t, "the restored room's snapshot without Ana", func() bool {
			p := persistedOf(t, tb.s.store, tb.code)
			return len(p.Users) == 1 && len(p.Accounts) == 1 && len(p.Pending) == 1 && len(p.Pending[0].Results) == 1
		})
		p := persistedOf(t, tb.s.store, tb.code)
		if p.Users[betoB.p.id] != betoMe.ID || p.Pending[0].Results[0].UserID != betoMe.ID {
			t.Errorf("the restored room lost Beto: users %v outbox %+v", p.Users, p.Pending)
		}

		// Everybody comes back with the cookies they had: Ana's is a guest's now,
		// and her seat and her score are where she left them.
		tb.rejoinAll()
		wantInts(t, "totals after the restore", totalScores(tb.anyView(), tb.ids()...), []int{50, 30, 40})
		if st := tb.seats[0].c.lastState(); st.CanJudge {
			t.Error("Ana's reconnected socket carries a deleted account")
		}
		anaB.use(tb.s)
		if _, status := anaB.me(); status != http.StatusUnauthorized {
			t.Errorf("Ana's old cookie works on the new process: %d", status)
		}

		// The store recovers: only Beto is credited, Ana does not come back.
		flaky.setError(nil)
		betoB.use(tb.s)
		betoB.waitStats(api.Stats{GamesPlayed: 1, Wins: 0, TotalPoints: 30, BestRound: betoStatsBest(t, tb), UniqueAnswers: betoStatsUnique(t, tb)})
		if _, err := backend.UserStats(context.Background(), anaMe.ID); !errors.Is(err, store.ErrUserNotFound) {
			t.Errorf("Ana's statistics came back: %v", err)
		}
		flaky.mu.Lock()
		defer flaky.mu.Unlock()
		if len(flaky.attempts) == oldAttempts {
			t.Error("the new process never offered the game")
		}
		for _, att := range flaky.attempts[oldAttempts:] {
			if len(att.Results) != 1 || att.Results[0].UserID != betoMe.ID {
				t.Errorf("the new process offered %+v, want Beto's result only", att)
			}
		}
	})
}

// betoStatsBest and betoStatsUnique are Beto's best round and unique answers of
// the fixture game (see TestFullGameThreePlayersTwoRounds and playSecondRound).
func betoStatsBest(t *testing.T, tb *table) int {
	t.Helper()
	best := 0
	for _, r := range tb.anyView().History {
		best = max(best, r.Players[tb.seats[1].p.id].Total)
	}
	return best
}

func betoStatsUnique(t *testing.T, tb *table) int {
	t.Helper()
	n := 0
	for _, r := range tb.anyView().History {
		for _, c := range r.Players[tb.seats[1].p.id].Cells {
			if c.Points == game.PointsUnique {
				n++
			}
		}
	}
	return n
}
