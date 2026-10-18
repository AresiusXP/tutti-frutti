package integration

import (
	"net/http"
	"slices"
	"testing"
	"time"

	"github.com/AresiusXP/tutti-frutti/server/internal/game"
	"github.com/AresiusXP/tutti-frutti/server/internal/protocol"
	"github.com/AresiusXP/tutti-frutti/server/internal/store"
)

// restart stops the server the way cmd/server does on SIGTERM (Hub.Shutdown:
// server_restarting, final snapshot, close 1012) and starts a new Hub and
// httptest server over the same store, with a new clock that starts downtime
// after the old one stopped. The table then talks to the new stack. Nobody has
// reconnected when it returns.
func (tb *table) restart(downtime time.Duration) {
	t := tb.t
	t.Helper()
	old := tb.s
	phase := tb.anyView().Phase
	clients := tb.live()

	if err := old.shutdown(); err != nil {
		t.Fatalf("Hub.Shutdown: %v", err)
	}
	for _, c := range clients {
		c.waitFor("server_restarting", isEvent(protocol.EventServerRestarting))
		c.expectClose(protocol.CloseServiceRestart)
	}
	if got := old.hub.Stats(); got.Rooms != 0 || got.Connections != 0 {
		t.Fatalf("stats after the drain = %+v, want zeros", got)
	}
	g, ok := snapshot(t, old.store, tb.code)
	if !ok {
		t.Fatalf("no final snapshot of room %s in the store", tb.code)
	}
	if g.Phase != phase {
		t.Fatalf("final snapshot phase = %s, want %s", g.Phase, phase)
	}
	old.srv.Close()

	next := newStack(t, stackOpts{store: old.store, start: old.clock.Now().Add(downtime)})
	if next.restored != 1 {
		t.Fatalf("Hub.Start restored %d rooms, want 1", next.restored)
	}
	if got := next.hub.Stats(); got.Rooms != 1 || got.Connections != 0 {
		t.Fatalf("stats after the restore = %+v, want 1 room and no connection", got)
	}
	tb.s = next
}

// rejoinAll reconnects the players in order with the cookies they already
// have, checking that the first one finds everybody else offline.
func (tb *table) rejoinAll() *protocol.State {
	tb.t.Helper()
	first := tb.reconnect(0)
	for _, p := range first.View.Players {
		if p.ID != tb.seats[0].p.id && p.Connected {
			tb.t.Fatalf("player %s is connected right after the restore", p.Name)
		}
	}
	for i := 1; i < len(tb.seats); i++ {
		tb.reconnect(i)
	}
	tb.waitAll("everybody back", func(v game.View) bool {
		for _, p := range v.Players {
			if !p.Connected {
				return false
			}
		}
		return len(v.Players) == len(tb.seats)
	})
	return first
}

// TestServerRestart: play up to a point, drain the server, start a new one
// over the same store and let the players reconnect with their old cookies:
// the game goes on from where it was, with its timers re-armed.
func TestServerRestart(t *testing.T) {
	cases := []struct {
		name     string
		stage    stage
		downtime time.Duration
		first    func(t *testing.T, tb *table, st *protocol.State)
		after    func(t *testing.T, tb *table)
	}{
		{name: "lobby", stage: stageLobby, after: func(t *testing.T, tb *table) {
			v := tb.view(1)
			if v.Phase != game.PhaseLobby || v.HostID != tb.seats[0].p.id || v.Settings.Rounds != 2 ||
				len(v.Settings.Categories) != 3 || v.Settings.Categories[2].Custom != "Cosa de cocina" {
				t.Fatalf("lobby after the restart: host %q settings %+v", v.HostID, v.Settings)
			}
			tb.start()
		}},
		{name: "letter phase: the restored timer stops the alphabet", stage: stageLetter, after: func(t *testing.T, tb *table) {
			if tb.s.clock.Pending() == 0 {
				t.Fatal("no timer armed after the restore")
			}
			v := tb.view(1)
			if v.Phase != game.PhaseLetter || v.ChooserID != tb.seats[0].p.id || v.Round.Letter != "" {
				t.Fatalf("letter phase after the restart: chooser %q round %+v", v.ChooserID, v.Round)
			}
			avail := game.AvailableLetters(v.Settings.Lang, v.Settings.ExcludeHardLetters, v.UsedLetters)
			want := game.LetterAt(avail, chooserFor)
			tb.s.advance(chooserFor)
			tb.waitAll("auto-stopped letter", func(v game.View) bool {
				return v.Phase == game.PhasePlaying && v.Round.Letter == want
			})
		}},
		{name: "letter phase: the chooser taps stop", stage: stageLetter, after: func(t *testing.T, tb *table) {
			tb.stopLetter("M")
		}},
		{name: "playing keeps the answers", stage: stagePlaying, after: func(t *testing.T, tb *table) {
			for i, st := range tb.seats {
				v := st.c.view()
				if len(v.Round.Answers) != 1 || !slices.Equal(v.Round.Answers[st.p.id], r1Answers[i][:]) {
					t.Fatalf("%s: answers after the restart = %v, want only %v", st.p.name, v.Round.Answers, r1Answers[i])
				}
			}
			tb.mustCmd(0, game.CmdBasta, nil)
			tb.waitPhase(game.PhaseGrace)
			tb.s.advance(graceFor + lateFor)
			tb.waitPhase(game.PhaseReview)
			wantInts(t, "totals after the restart", reviewTotals(tb.view(2), tb.ids()...), r1Unvoted)
			tb.assertNoAnswerLeaks()
		}},
		{name: "grace: the restored timer locks the round", stage: stageGrace, after: func(t *testing.T, tb *table) {
			if v := tb.view(1); v.Phase != game.PhaseGrace || v.Round.CallerID != tb.seats[0].p.id {
				t.Fatalf("grace after the restart: %s caller %q", v.Phase, v.Round.CallerID)
			}
			tb.s.advance(graceFor + lateFor)
			tb.waitPhase(game.PhaseReview)
			wantInts(t, "totals after the restart", reviewTotals(tb.view(1), tb.ids()...), r1Unvoted)
		}},
		{name: "grace that ended while the server was down", stage: stageGrace, downtime: time.Minute,
			first: func(t *testing.T, tb *table, st *protocol.State) {
				if st.View.Phase != game.PhaseReview {
					t.Fatalf("phase right after a restore past the grace deadline = %s, want review", st.View.Phase)
				}
			},
			after: func(t *testing.T, tb *table) {
				wantInts(t, "totals", reviewTotals(tb.view(2), tb.ids()...), r1Unvoted)
				tb.assertNoAnswerLeaks()
			}},
		{name: "review keeps votes and merges", stage: stageVoted, after: func(t *testing.T, tb *table) {
			for _, st := range tb.seats {
				v := st.c.view()
				if v.Phase != game.PhaseReview || len(v.Round.RejectVotes) != 2 || len(v.Round.Proposals) != 1 ||
					len(v.Round.Proposals[0].Votes) != 3 {
					t.Fatalf("%s: review after the restart: votes %d proposals %+v", st.p.name, len(v.Round.RejectVotes), v.Round.Proposals)
				}
				wantInts(t, st.p.name+" totals", reviewTotals(v, tb.ids()...), r1Voted)
			}
			tb.mustCmd(0, game.CmdFinishReview, nil)
			tb.waitPhase(game.PhaseRoundSummary)
			wantInts(t, "running totals", totalScores(tb.view(1), tb.ids()...), r1Voted)
		}},
		{name: "round summary goes on with the next chooser", stage: stageSummary, after: func(t *testing.T, tb *table) {
			wantInts(t, "totals after the restart", totalScores(tb.view(1), tb.ids()...), r1Voted)
			marks := tb.marks()
			tb.mustCmd(0, game.CmdNextRound, nil)
			tb.waitPhase(game.PhaseLetter)
			e, ok := findEvent(tb.seats[2].c.events(marks[2]), game.EventRoundStarted)
			if !ok || e.PlayerID != tb.seats[1].p.id || e.Round != 2 {
				t.Fatalf("round_started = %+v (found %v), want Beto as the chooser of round 2", e, ok)
			}
			if got := tb.view(0).UsedLetters; !slices.Equal(got, []string{"M"}) {
				t.Fatalf("usedLetters = %v", got)
			}
		}},
	}

	forEachStore(t, func(t *testing.T, open func(*testing.T) store.Store) {
		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				s := newStack(t, stackOpts{store: open(t)})
				tb := newTable(t, s, "Ana", "Beto", "Cami")
				tb.advanceTo(tc.stage)

				tb.restart(tc.downtime)
				if info, ok := tb.s.roomInfo(tb.code); !ok || info.Players != 3 {
					t.Fatalf("restored room info = %+v (found %v)", info, ok)
				}
				first := tb.rejoinAll()
				if tc.first != nil {
					tc.first(t, tb, first)
				}
				for _, st := range tb.seats {
					if v := st.c.view(); v.YouID != st.p.id || v.HostID != tb.seats[0].p.id {
						t.Fatalf("%s: seat after the restart: you %q host %q", st.p.name, v.YouID, v.HostID)
					}
				}
				tc.after(t, tb)
			})
		}
	})
}

// TestShutdownRefusesNewWork: once the hub is draining it takes no new rooms
// and no new connections; once the WebSocket endpoint drains, upgrades are
// refused before they happen.
func TestShutdownRefusesNewWork(t *testing.T) {
	s := newStack(t, stackOpts{})
	tb := newTable(t, s, "Ana", "Beto")
	late := s.newPlayer("Late")

	if err := s.shutdownHub(); err != nil {
		t.Fatal(err)
	}
	for _, c := range tb.live() {
		c.expectClose(protocol.CloseServiceRestart)
	}
	r := s.do(http.MethodPost, "/api/rooms", []byte(`{"name":"Late","avatar":{"emoji":"🍓","color":"#f06"}}`), withCookie(late))
	if r.status != http.StatusServiceUnavailable || r.errCode(t) != protocol.ErrServerBusy {
		t.Fatalf("POST /api/rooms while draining = %d %s", r.status, r.body)
	}
	if _, ok := s.roomInfo(tb.code); ok {
		t.Fatal("a drained room is still served")
	}
	c := s.connect(late)
	c.send(game.CmdJoin, c.joinRequest(tb.code))
	c.waitFor("server_busy", isError(protocol.ErrServerBusy))
	c.expectClose(protocol.CloseServiceRestart)
	if got := s.hub.Stats(); got.Rooms != 0 || got.Connections != 0 {
		t.Fatalf("stats = %+v", got)
	}

	// The WebSocket endpoint then drains too (cmd/server does this right after
	// the hub): every socket is gone and new upgrades get 503 before the handshake.
	if err := s.shutdown(); err != nil {
		t.Fatal(err)
	}
	if got := s.wsh.Open(); got != 0 {
		t.Fatalf("ws.Handler.Open() = %d after the drain", got)
	}
	_, resp, err := s.dialWith(t, late, late.cookie, s.origin)
	if err == nil || resp == nil || resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("upgrade while draining: err %v resp %v, want 503", err, resp)
	}
	if resp.Header.Get("Retry-After") == "" {
		t.Fatal("the 503 of a draining endpoint carries no Retry-After")
	}
}
