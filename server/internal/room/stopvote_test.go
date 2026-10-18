package room

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/AresiusXP/tutti-frutti/server/internal/game"
	"github.com/AresiusXP/tutti-frutti/server/internal/protocol"
	"github.com/AresiusXP/tutti-frutti/server/internal/store"
)

const stopVotesMetric = "tuttifrutti_stop_votes_total"

// startStopRound starts a round in which p1 has filled one category (so p1
// cannot call basta) and the others are empty-handed.
func (l *lobby3) startStopRound(mod func(*game.Settings)) {
	l.e.t.Helper()
	l.startRound(mod)
	l.send(0, game.CmdSetAnswer, game.SetAnswer{Category: 0, Text: "Ana"})
	l.drain()
}

// stopVoteOf reads the vote as p's client was last told.
func stopVoteOf(t *testing.T, frames []frameRec) *game.StopVoteView {
	t.Helper()
	return lastState(t, frames).View.Round.StopVote
}

func stopMetrics(t *testing.T, reg *prometheus.Registry) map[string]float64 {
	t.Helper()
	out := map[string]float64{}
	for _, r := range stopResults {
		out[r] = metricValue(t, reg, stopVotesMetric, "result", r)
	}
	return out
}

func wantStopMetrics(t *testing.T, reg *prometheus.Registry, want map[string]float64) {
	t.Helper()
	got := stopMetrics(t, reg)
	for _, r := range stopResults {
		if got[r] != want[r] {
			t.Errorf("%s{result=%q} = %v, want %v (all: %v)", stopVotesMetric, r, got[r], want[r], got)
		}
	}
}

func TestStopVoteOverTheWire(t *testing.T) {
	t.Run("asked, then passed by everybody", func(t *testing.T) {
		l := newLobby3(t)
		l.startStopRound(nil)

		l.send(0, game.CmdAskStop, nil)
		frames := l.expectFrames("ask", []string{"event:stop_asked", "state"})
		for i := range 3 {
			ev := frames[i][0].event(t)
			if ev.PlayerID != "p1" || ev.Name != "Ana" {
				t.Errorf("p%d: stop_asked = %+v", i+1, ev)
			}
			v := stopVoteOf(t, frames[i])
			if v == nil || v.CallerID != "p1" || !slices.Equal(v.Voters, []string{"p2", "p3"}) || len(v.Yes) != 0 {
				t.Errorf("p%d: vote = %+v", i+1, v)
			}
			if want := l.e.clock.Now().Add(game.StopVoteDuration); !v.EndsAt.Equal(want) {
				t.Errorf("p%d: endsAt = %v, want %v", i+1, v.EndsAt, want)
			}
		}

		l.send(1, game.CmdVoteStop, game.VoteStop{Yes: true})
		frames = l.expectFrames("first yes", []string{"state"})
		if v := stopVoteOf(t, frames[2]); !slices.Equal(v.Yes, []string{"p2"}) {
			t.Errorf("yes = %v", v.Yes)
		}

		l.send(2, game.CmdVoteStop, game.VoteStop{Yes: true})
		frames = l.expectFrames("second yes", []string{"event:basta", "state"})
		if ev := frames[1][0].event(t); ev.PlayerID != "p1" || ev.Name != "Ana" {
			t.Errorf("basta = %+v", ev)
		}
		v := lastState(t, frames[0]).View
		if v.Phase != game.PhaseGrace || v.Round.CallerID != "p1" || v.Round.StopVote != nil {
			t.Errorf("view after the vote: phase %s caller %q vote %v", v.Phase, v.Round.CallerID, v.Round.StopVote)
		}
		if want := l.e.clock.Now().Add(game.GraceDuration); !v.Round.GraceEndsAt.Equal(want) {
			t.Errorf("graceEndsAt = %v, want %v", v.Round.GraceEndsAt, want)
		}

		// Then the round goes on as after any basta, empty cells and all.
		l.e.advance(game.GraceDuration + game.LateUpdateAllowance)
		st := lastState(t, l.c[1].drain()).View
		if st.Phase != game.PhaseReview || st.Round.Result.Players["p1"].Cells[1].Points != 0 {
			t.Errorf("after the lock: phase %s result %+v", st.Phase, st.Round.Result)
		}
		wantStopMetrics(t, l.e.reg, map[string]float64{"asked": 1, "passed": 1})
		if got := metricValue(t, l.e.reg, "tuttifrutti_basta_total"); got != 1 {
			t.Errorf("basta_total = %v: a vote that passes is one basta", got)
		}
	})

	t.Run("a no rejects it and nobody is named", func(t *testing.T) {
		l := newLobby3(t)
		l.startStopRound(nil)
		l.send(0, game.CmdAskStop, nil)
		l.drain()

		l.send(2, game.CmdVoteStop, game.VoteStop{Yes: false})
		frames := l.expectFrames("no", []string{"event:stop_rejected", "state"})
		for i := range 3 {
			ev := frames[i][0].event(t)
			if ev.PlayerID != "p1" || ev.Name != "Ana" || ev.Reason != game.StopReasonDeclined {
				t.Errorf("p%d: %+v", i+1, ev)
			}
			if strings.Contains(string(frames[i][0].Raw), "p3") || strings.Contains(string(frames[i][0].Raw), "Cami") {
				t.Errorf("the event names the decliner: %s", frames[i][0].Raw)
			}
			if v := lastState(t, frames[i]).View; v.Phase != game.PhasePlaying || v.Round.StopVote != nil {
				t.Errorf("p%d: phase %s vote %v", i+1, v.Phase, v.Round.StopVote)
			}
		}

		// No cooldown: the same player asks again at once.
		l.send(0, game.CmdAskStop, nil)
		l.expectFrames("ask again", []string{"event:stop_asked", "state"})
		wantStopMetrics(t, l.e.reg, map[string]float64{"asked": 2, "declined": 1})
		if got := metricValue(t, l.e.reg, "tuttifrutti_basta_total"); got != 0 {
			t.Errorf("basta_total = %v", got)
		}
	})

	t.Run("15 seconds without agreement reject it, to the nanosecond", func(t *testing.T) {
		l := newLobby3(t)
		l.startStopRound(nil)
		l.send(0, game.CmdAskStop, nil)
		l.send(1, game.CmdVoteStop, game.VoteStop{Yes: true})
		l.drain()

		l.e.advance(game.StopVoteDuration - time.Nanosecond)
		for i, c := range l.c {
			if got := c.drain(); len(got) != 0 {
				t.Fatalf("p%d heard %v before the deadline", i+1, kinds(got))
			}
		}
		l.e.advance(time.Nanosecond)
		frames := l.expectFrames("timeout", []string{"event:stop_rejected", "state"})
		if ev := frames[0][0].event(t); ev.Reason != game.StopReasonTimeout || ev.PlayerID != "p1" {
			t.Errorf("event = %+v", ev)
		}
		wantStopMetrics(t, l.e.reg, map[string]float64{"asked": 1, "timeout": 1})
	})

	t.Run("a basta by someone else ends it", func(t *testing.T) {
		l := newLobby3(t)
		l.startStopRound(nil)
		l.send(0, game.CmdAskStop, nil)
		for cat, a := range roundAnswers[1] {
			l.send(1, game.CmdSetAnswer, game.SetAnswer{Category: cat, Text: a})
		}
		l.drain()
		l.send(1, game.CmdBasta, nil)
		frames := l.expectFrames("basta", []string{"event:stop_rejected", "event:basta", "state"})
		if ev := frames[0][0].event(t); ev.Reason != game.StopReasonEnded || ev.PlayerID != "p1" {
			t.Errorf("stop_rejected = %+v", ev)
		}
		if ev := frames[0][1].event(t); ev.PlayerID != "p2" {
			t.Errorf("basta = %+v", ev)
		}
		wantStopMetrics(t, l.e.reg, map[string]float64{"asked": 1, "ended": 1})
		if got := metricValue(t, l.e.reg, "tuttifrutti_basta_total"); got != 1 {
			t.Errorf("basta_total = %v", got)
		}
	})

	t.Run("the time limit ends it", func(t *testing.T) {
		l := newLobby3(t)
		l.startStopRound(func(s *game.Settings) { s.TimeLimitSeconds = 60 })
		l.e.advance(50 * time.Second)
		l.drain()
		l.send(0, game.CmdAskStop, nil)
		l.drain()
		l.e.advance(10 * time.Second)
		frames := l.expectFrames("limit", []string{"event:stop_rejected", "event:time_up", "state"})
		if ev := frames[0][0].event(t); ev.Reason != game.StopReasonEnded {
			t.Errorf("stop_rejected = %+v", ev)
		}
		wantStopMetrics(t, l.e.reg, map[string]float64{"asked": 1, "ended": 1})
		if got := metricValue(t, l.e.reg, "tuttifrutti_basta_total"); got != 0 {
			t.Errorf("a time limit is not a basta: %v", got)
		}
	})

	t.Run("the asker's connection dropping ends it", func(t *testing.T) {
		l := newLobby3(t)
		l.startStopRound(nil)
		l.send(0, game.CmdAskStop, nil)
		l.drain()
		l.s[0].Disconnect()
		l.e.sync(l.code)
		frames := l.expectFrames("asker gone", []string{"event:stop_rejected", "state"}, 1, 2)
		if ev := frames[1][0].event(t); ev.Reason != game.StopReasonEnded || ev.PlayerID != "p1" {
			t.Errorf("event = %+v", ev)
		}
		if v := lastState(t, frames[2]).View; v.Round.StopVote != nil || v.Phase != game.PhasePlaying {
			t.Errorf("phase %s vote %v", v.Phase, v.Round.StopVote)
		}
		wantStopMetrics(t, l.e.reg, map[string]float64{"asked": 1, "ended": 1})
	})

	t.Run("a voter's connection dropping lets it pass", func(t *testing.T) {
		l := newLobby3(t)
		l.startStopRound(nil)
		l.send(0, game.CmdAskStop, nil)
		l.send(1, game.CmdVoteStop, game.VoteStop{Yes: true})
		l.drain()
		l.s[2].Disconnect()
		l.e.sync(l.code)
		l.expectFrames("voter gone", []string{"event:basta", "state"}, 0, 1)
		wantStopMetrics(t, l.e.reg, map[string]float64{"asked": 1, "passed": 1})
		if got := metricValue(t, l.e.reg, "tuttifrutti_basta_total"); got != 1 {
			t.Errorf("basta_total = %v", got)
		}
	})

	t.Run("a voter leaving lets it pass", func(t *testing.T) {
		l := newLobby3(t)
		l.startStopRound(nil)
		l.send(0, game.CmdAskStop, nil)
		l.send(1, game.CmdVoteStop, game.VoteStop{Yes: true})
		l.drain()
		l.send(2, game.CmdLeave, nil)
		l.expectFrames("voter left", []string{"event:basta", "state"}, 0, 1)
		wantStopMetrics(t, l.e.reg, map[string]float64{"asked": 1, "passed": 1})
	})

	t.Run("nobody left to ask passes it at once", func(t *testing.T) {
		l := newLobby3(t)
		l.startStopRound(nil)
		l.s[1].Disconnect()
		l.s[2].Disconnect()
		l.e.sync(l.code)
		l.drain()
		l.send(0, game.CmdAskStop, nil)
		frames := l.expectFrames("alone", []string{"event:basta", "state"}, 0)
		if ev := frames[0][0].event(t); ev.PlayerID != "p1" {
			t.Errorf("basta = %+v", ev)
		}
		wantStopMetrics(t, l.e.reg, map[string]float64{"asked": 1, "passed": 1})
		if got := metricValue(t, l.e.reg, "tuttifrutti_basta_total"); got != 1 {
			t.Errorf("basta_total = %v", got)
		}
	})

	t.Run("every voter disconnecting passes it", func(t *testing.T) {
		l := newLobby3(t)
		l.startStopRound(nil)
		l.send(0, game.CmdAskStop, nil)
		l.s[1].Disconnect()
		l.e.sync(l.code)
		l.drain()
		l.s[2].Disconnect()
		l.e.sync(l.code)
		l.expectFrames("last voter gone", []string{"event:basta", "state"}, 0)
		wantStopMetrics(t, l.e.reg, map[string]float64{"asked": 1, "passed": 1})
	})

	t.Run("a refused ask or vote counts nothing", func(t *testing.T) {
		l := newLobby3(t)
		l.startStopRound(nil)
		l.send(1, game.CmdVoteStop, game.VoteStop{Yes: true}) // no vote
		for cat, a := range roundAnswers[0] {
			l.send(0, game.CmdSetAnswer, game.SetAnswer{Category: cat, Text: a})
		}
		l.send(0, game.CmdAskStop, nil) // nothing missing
		wantStopMetrics(t, l.e.reg, nil)
	})
}

func TestStopVoteErrorsOverTheWire(t *testing.T) {
	l := newLobby3(t)
	l.startStopRound(nil)
	e := l.e

	tests := []struct {
		name   string
		player int
		typ    string
		d      any
		want   string
	}{
		{"a vote without a vote", 1, game.CmdVoteStop, game.VoteStop{Yes: true}, game.CodeNoStopVote},
		{"a vote with a bad payload", 1, game.CmdVoteStop, map[string]any{"yes": "yes"}, protocol.ErrBadMessage},
		{"an ask with a bad payload", 0, game.CmdAskStop, []int{1}, protocol.ErrBadMessage},
	}
	for _, tt := range tests {
		l.send(tt.player, tt.typ, tt.d)
		frames := l.c[tt.player].drain()
		if got := kinds(frames); len(got) != 1 || got[0] != "error:"+tt.want {
			t.Errorf("%s: frames = %v, want error:%s", tt.name, got, tt.want)
		}
		if got := metricValue(t, e.reg, "tuttifrutti_commands_total", "type", tt.typ, "result", tt.want); got != 1 {
			t.Errorf("%s: commands_total{%s,%s} = %v", tt.name, tt.typ, tt.want, got)
		}
	}

	// Once it is open, the others can neither ask nor, as the asker, vote.
	l.send(0, game.CmdAskStop, nil)
	l.drain()
	for _, tt := range []struct {
		player int
		typ    string
		d      any
		want   string
	}{
		{1, game.CmdAskStop, nil, game.CodeStopVotePending},
		{0, game.CmdAskStop, nil, game.CodeStopVotePending},
		{0, game.CmdVoteStop, game.VoteStop{Yes: true}, game.CodeSelfVote},
	} {
		l.send(tt.player, tt.typ, tt.d)
		if got := kinds(l.c[tt.player].drain()); len(got) != 1 || got[0] != "error:"+tt.want {
			t.Errorf("%s by p%d: frames = %v, want error:%s", tt.typ, tt.player+1, got, tt.want)
		}
		for i, c := range l.c {
			if i != tt.player && len(c.drain()) != 0 {
				t.Errorf("a refused %s reached p%d", tt.typ, i+1)
			}
		}
	}

	// A no closes the vote; with every category filled, asking is pointless.
	l.send(2, game.CmdVoteStop, game.VoteStop{Yes: false})
	l.drain()
	for cat, a := range roundAnswers[0] {
		l.send(0, game.CmdSetAnswer, game.SetAnswer{Category: cat, Text: a})
	}
	l.drain()
	l.send(0, game.CmdAskStop, nil)
	if got := kinds(l.c[0].drain()); len(got) != 1 || got[0] != "error:"+game.CodeStopNotNeeded {
		t.Errorf("a complete ask: frames = %v", got)
	}

	// The frames a client sends before its join are refused like the others.
	_, stranger := e.connect(l.code, "stranger")
	stranger.Handle(game.CmdAskStop, nil)
	stranger.Handle(game.CmdVoteStop, nil)
	e.sync(l.code)
	if got := metricValue(t, e.reg, "tuttifrutti_commands_total", "type", game.CmdAskStop, "result", protocol.ErrNotJoined); got != 1 {
		t.Errorf("ask_stop before the join: %v", got)
	}
	if got := metricValue(t, e.reg, "tuttifrutti_commands_total", "type", game.CmdVoteStop, "result", protocol.ErrNotJoined); got != 1 {
		t.Errorf("vote_stop before the join: %v", got)
	}
	if got := metricValue(t, e.reg, "tuttifrutti_commands_total", "type", "unknown", "result", protocol.ErrNotJoined); got != 0 {
		t.Errorf("the new commands are counted as unknown: %v", got)
	}
}

func TestStopVoteSwitchedOffByTheHost(t *testing.T) {
	l := newLobby3(t)
	l.startStopRound(func(s *game.Settings) { s.StopVote = false })
	l.send(0, game.CmdAskStop, nil)
	if got := kinds(l.c[0].drain()); len(got) != 1 || got[0] != "error:"+game.CodeStopVoteDisabled {
		t.Fatalf("frames = %v", got)
	}
	wantStopMetrics(t, l.e.reg, nil)
	if got := metricValue(t, l.e.reg, "tuttifrutti_commands_total", "type", game.CmdAskStop, "result", game.CodeStopVoteDisabled); got != 1 {
		t.Errorf("commands_total = %v", got)
	}
}

func TestStopVoteMetricLabelsAreFixed(t *testing.T) {
	l := newLobby3(t)
	reg := l.e.reg
	got := func() []string {
		v := labelValuesOf(t, reg, stopVotesMetric, "result")
		slices.Sort(v)
		return v
	}
	want := []string{"asked", "declined", "ended", "passed", "timeout"}
	if g := got(); !slices.Equal(g, want) {
		t.Fatalf("labels before any vote = %v, want %v (every series exists from the start)", g, want)
	}

	// Plenty of votes, and a label that is not one of ours: still five series.
	l.startStopRound(nil)
	for range 6 {
		l.send(0, game.CmdAskStop, nil)
		l.send(1, game.CmdVoteStop, game.VoteStop{Yes: false})
	}
	l.e.hub.m.stopVote("evil-" + strings.Repeat("x", 40))
	l.e.hub.m.stopVote("")
	l.e.hub.m.stopVote(string(game.EventStopAsked))
	if g := got(); !slices.Equal(g, want) {
		t.Errorf("labels after the votes = %v, want %v", g, want)
	}
	wantStopMetrics(t, reg, map[string]float64{"asked": 6, "declined": 6})

	// The commands are known command types, never "unknown".
	for _, typ := range []string{game.CmdAskStop, game.CmdVoteStop} {
		if _, ok := commandTypes[typ]; !ok {
			t.Errorf("%s is not a known command type for tuttifrutti_commands_total", typ)
		}
	}
	for _, typ := range allCommandTypes() {
		if _, ok := commandTypes[typ]; !ok {
			t.Errorf("command %s has no label value", typ)
		}
	}
	if len(commandTypes) != len(allCommandTypes()) {
		t.Errorf("commandTypes has %d entries for %d commands", len(commandTypes), len(allCommandTypes()))
	}
}

func allCommandTypes() []string {
	return []string{
		game.CmdJoin, game.CmdLeave, game.CmdUpdateSettings, game.CmdKick, game.CmdStartGame, game.CmdStopLetter,
		game.CmdSetAnswer, game.CmdBasta, game.CmdAskStop, game.CmdVoteStop, game.CmdVoteReject, game.CmdProposeMerge,
		game.CmdVoteMerge, game.CmdReviewNav, game.CmdFinishReview, game.CmdNextRound, game.CmdEndGame, game.CmdRematch,
	}
}

func labelValuesOf(t *testing.T, reg *prometheus.Registry, name, label string) []string {
	t.Helper()
	families, err := reg.Gather()
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, mf := range families {
		if mf.GetName() != name {
			continue
		}
		for _, m := range mf.GetMetric() {
			for _, l := range m.GetLabel() {
				if l.GetName() == label {
					out = append(out, l.GetValue())
				}
			}
		}
	}
	return out
}

// ---- persistence ----

func TestSnapshotCarriesTheVoteButRestoreDropsIt(t *testing.T) {
	// The asker matters: the restore marks the seats disconnected one by one,
	// so a vote asked by the last seat would pass by itself (every voter gone,
	// the asker not yet), while one asked by the first is ended by the way.
	for _, asker := range []int{0, 2} {
		t.Run(fmt.Sprintf("asked by p%d", asker+1), func(t *testing.T) {
			l := newLobby3(t)
			e, code := l.e, l.code
			l.startStopRound(nil)
			l.send(asker, game.CmdAskStop, nil)
			if asker != 1 {
				l.send(1, game.CmdVoteStop, game.VoteStop{Yes: true})
			}

			// The restart: the final snapshot still has the vote (it is part of
			// the round and round-trips).
			if err := e.hub.Shutdown(context.Background()); err != nil {
				t.Fatal(err)
			}
			_, saved := e.stored(code)
			if v := saved.Round.StopVote; v == nil || v.CallerID != fmt.Sprintf("p%d", asker+1) || !slices.Equal(v.Yes, []string{"p2"}) {
				t.Fatalf("the snapshot of a round with a vote = %+v", v)
			}

			// The next process drops it, without a word, and the round carries on.
			reg := prometheus.NewRegistry()
			e.hub = e.newHubOn(func(c *Config) { c.Registerer = reg })
			if n, err := e.hub.Start(context.Background()); err != nil || n != 1 {
				t.Fatalf("Start = %d, %v", n, err)
			}
			if phase := query(e, code, func(r *room) game.Phase { return r.g.Phase }); phase != game.PhasePlaying {
				t.Fatalf("phase after the restore = %s: a stale vote with no voters must not pass by itself", phase)
			}
			if vote := query(e, code, func(r *room) *game.StopVote { return r.g.Round.StopVote }); vote != nil {
				t.Errorf("the vote survived the restore: %+v", vote)
			}
			if open := query(e, code, func(r *room) bool { return r.stopOpen }); open {
				t.Error("the room thinks a vote is open")
			}
			wantStopMetrics(t, reg, nil)
			if got := metricValue(t, reg, "tuttifrutti_basta_total"); got != 0 {
				t.Errorf("basta_total = %v", got)
			}

			// Players come back: nothing to vote on, but the asker can ask again.
			c1, s1 := e.join(code, "p1", "Ana")
			c2, _ := e.join(code, "p2", "Beto")
			c3, s3 := e.join(code, "p3", "Cami")
			for i, c := range []*fakeClient{c1, c2, c3} {
				if v := lastState(t, c.drain()).View; v.Phase != game.PhasePlaying || v.Round.StopVote != nil {
					t.Errorf("p%d sees phase %s vote %v", i+1, v.Phase, v.Round.StopVote)
				}
			}
			e.handle(code, []Session{s1, nil, s3}[asker], game.CmdAskStop, nil)
			if got := kinds(c2.drain()); !slices.Equal(got, []string{"event:stop_asked", "state"}) {
				t.Errorf("a new ask after the restore: %v", got)
			}
			wantStopMetrics(t, reg, map[string]float64{"asked": 1})
		})
	}
}

func TestRestoreDropsAStaleVoteInEveryPhase(t *testing.T) {
	// A vote is only ever open while playing, but whatever a snapshot says,
	// no vote is restored.
	stale := &game.StopVote{CallerID: "k", EndsAt: t0.Add(time.Hour), Yes: []string{"g"}}
	build := func(t *testing.T, until string) *game.Game {
		t.Helper()
		g := game.NewGame(game.Player{ID: "h", Name: "Host", Avatar: testAvatar}, t0)
		apply := func(actor string, cmd game.Command) {
			t.Helper()
			if _, err := g.Apply(t0, actor, cmd); err != nil {
				t.Fatalf("%T: %v", cmd, err)
			}
		}
		apply("g", game.Join{Name: "Guest", Avatar: testAvatar})
		apply("k", game.Join{Name: "Kid", Avatar: testAvatar})
		apply("h", threeCats(nil))
		apply("h", game.StartGame{})
		if until == "letter" {
			return g
		}
		apply("h", game.StopLetter{})
		if until == "playing" {
			apply("k", game.AskStop{})
			return g
		}
		for cat, a := range roundAnswers[0] {
			apply("h", game.SetAnswer{Category: cat, Text: a})
		}
		apply("h", game.Basta{})
		if until == "grace" {
			return g
		}
		g.Tick(t0.Add(game.GraceDuration + game.LateUpdateAllowance))
		return g // review
	}
	for _, phase := range []string{"letter", "playing", "grace", "review"} {
		t.Run(phase, func(t *testing.T) {
			e := newEnv(t)
			g := build(t, phase)
			if phase != "playing" {
				g.Round.StopVote = stale
			}
			if g.Round.StopVote == nil {
				t.Fatal("setup: no vote")
			}
			b, err := encodePersisted("BBBB", t0, t0, g)
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(string(b), `"stopVote":{`) {
				t.Fatalf("setup: the snapshot has no vote: %s", b)
			}
			if err := e.store.Memory.Save(context.Background(), store.Snapshot{Code: "BBBB", State: b, UpdatedAt: t0}); err != nil {
				t.Fatal(err)
			}
			if n, err := e.hub.Start(context.Background()); err != nil || n != 1 {
				t.Fatalf("Start = %d, %v", n, err)
			}
			if vote := query(e, "BBBB", func(r *room) *game.StopVote { return r.g.Round.StopVote }); vote != nil {
				t.Errorf("the vote survived: %+v", vote)
			}
			if got := query(e, "BBBB", func(r *room) game.Phase { return r.g.Phase }); got != g.Phase {
				t.Errorf("phase = %s, want %s: restoring must not move the round", got, g.Phase)
			}
		})
	}

	t.Run("a lobby has no round to drop it from", func(t *testing.T) {
		e := newEnv(t)
		state := newPersistedGame(t, "BBBB", func(*game.Game) {})
		if err := e.store.Memory.Save(context.Background(), store.Snapshot{Code: "BBBB", State: state, UpdatedAt: t0}); err != nil {
			t.Fatal(err)
		}
		if n, err := e.hub.Start(context.Background()); err != nil || n != 1 {
			t.Fatalf("Start = %d, %v", n, err)
		}
	})
}

func TestSnapshotsFromBeforeTheSettingRestoreWithItOff(t *testing.T) {
	e := newEnv(t)
	g := game.NewGame(game.Player{ID: "h", Name: "Host", Avatar: testAvatar}, t0)
	b, err := encodePersisted("BBBB", t0, t0, g)
	if err != nil {
		t.Fatal(err)
	}
	old := strings.Replace(string(b), `,"stopVote":true`, "", 1)
	if old == string(b) {
		t.Fatalf("setup: no stopVote in %s", b)
	}
	if err := e.store.Memory.Save(context.Background(), store.Snapshot{Code: "BBBB", State: []byte(old), UpdatedAt: t0}); err != nil {
		t.Fatal(err)
	}
	if n, err := e.hub.Start(context.Background()); err != nil || n != 1 {
		t.Fatalf("Start = %d, %v", n, err)
	}
	if on := query(e, "BBBB", func(r *room) bool { return r.g.Settings.StopVote }); on {
		t.Error("an old snapshot restored with the vote to stop on")
	}
	// The host can switch it on again in the lobby.
	_, host := e.join("BBBB", "h", "Host")
	s := game.DefaultSettings()
	e.handle("BBBB", host, game.CmdUpdateSettings, game.UpdateSettings{Settings: s})
	if on := query(e, "BBBB", func(r *room) bool { return r.g.Settings.StopVote }); !on {
		t.Error("the host could not switch the setting on")
	}
}

func TestStopOpenMirrorFollowsTheVote(t *testing.T) {
	l := newLobby3(t)
	l.startStopRound(nil)
	open := func() bool { return query(l.e, l.code, func(r *room) bool { return r.stopOpen }) }
	if open() {
		t.Fatal("open before any ask")
	}
	l.send(0, game.CmdAskStop, nil)
	if !open() {
		t.Error("not open after an ask")
	}
	l.send(1, game.CmdVoteStop, game.VoteStop{Yes: false})
	if open() {
		t.Error("still open after a no")
	}
}

func TestRoomWakesForTheVoteDeadline(t *testing.T) {
	l := newLobby3(t)
	l.startStopRound(nil)
	l.send(0, game.CmdAskStop, nil)
	wake := query(l.e, l.code, func(r *room) time.Time { return r.wakeAt })
	if want := l.e.clock.Now().Add(game.StopVoteDuration); wake.After(want) {
		t.Errorf("the room wakes at %v, after the end of the vote %v", wake, want)
	}
	l.e.advance(game.StopVoteDuration)
	if open := query(l.e, l.code, func(r *room) bool { return r.g.Round.StopVote != nil }); open {
		t.Error("the vote outlived its deadline")
	}
}
