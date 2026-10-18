package integration

import (
	"bytes"
	"slices"
	"testing"
	"time"

	"github.com/AresiusXP/tutti-frutti/server/internal/game"
	"github.com/AresiusXP/tutti-frutti/server/internal/protocol"
	"github.com/AresiusXP/tutti-frutti/server/internal/store"
)

// stopVoteFor is the product's time to agree on a vote to stop (docs/DESIGN.md).
const stopVoteFor = 15 * time.Second

// stopTable plays the first moments of round 1 (letter M) with Ana (host),
// Beto and Cami: Ana has filled one category only, so she cannot call Basta;
// Beto has every category filled (he can); Cami skipped the animal.
//
//	             Ana     Beto      Cami
//	Nombre       Marta   Martita   Mauro
//	Animal       -       Mono      -
//	Cosa cocina  -       Mate      Manteca
func stopTable(t *testing.T, s *stack, mod func(*game.Settings)) *table {
	t.Helper()
	tb := newTable(t, s, "Ana", "Beto", "Cami")
	settings := roundSettings()
	if mod != nil {
		mod(&settings)
	}
	tb.configure(settings)
	tb.start()
	tb.stopLetter("M")
	tb.answers(0, "Marta")
	tb.answers(1, "Martita", "Mono", "Mate")
	tb.answers(2, "Mauro", "", "Manteca")
	tb.stage = stagePlaying
	return tb
}

const (
	ana  = 0
	beto = 1
	cami = 2
)

// heard lists the types of the engine events the client got since the mark.
func heard(c *client, since int) []game.EventType {
	var out []game.EventType
	for _, ev := range c.events(since) {
		out = append(out, ev.Type)
	}
	return out
}

// wantEvents checks the engine events (in order) that every live client
// received since the marks taken before the action.
func (tb *table) wantEvents(marks []int, want ...game.EventType) {
	tb.t.Helper()
	for i, st := range tb.seats {
		if st.c == nil || st.c.isClosed() {
			continue
		}
		if got := heard(st.c, marks[i]); !slices.Equal(got, want) {
			tb.t.Fatalf("%s heard %v, want %v; frames:\n%v", st.p.name, got, want, st.c.snapshotFrames()[marks[i]:])
		}
	}
}

// eventOf returns the first event of a type that the seat heard since the mark.
func (tb *table) eventOf(seat, since int, typ game.EventType) game.Event {
	tb.t.Helper()
	for _, ev := range tb.seats[seat].c.events(since) {
		if ev.Type == typ {
			return ev
		}
	}
	tb.t.Fatalf("%s heard no %s", tb.seats[seat].p.name, typ)
	return game.Event{}
}

func (tb *table) waitVote(desc string, pred func(*game.StopVoteView) bool) {
	tb.t.Helper()
	tb.waitAll(desc, func(v game.View) bool {
		return v.Phase == game.PhasePlaying && v.Round != nil && pred(v.Round.StopVote)
	})
}

func (tb *table) waitNoVote() {
	tb.t.Helper()
	tb.waitVote("no vote", func(v *game.StopVoteView) bool { return v == nil })
}

// wantVote checks the vote every live client sees: who asked, who may vote
// and who said yes.
func (tb *table) wantVote(asker int, voters, yes []int) {
	tb.t.Helper()
	ids := func(seats []int) []string {
		out := []string{}
		for _, i := range seats {
			out = append(out, tb.seats[i].p.id)
		}
		return out
	}
	for _, st := range tb.seats {
		if st.c == nil || st.c.isClosed() {
			continue
		}
		v := st.c.view().Round.StopVote
		if v == nil || v.CallerID != tb.seats[asker].p.id || !slices.Equal(v.Voters, ids(voters)) || !slices.Equal(v.Yes, ids(yes)) {
			tb.t.Fatalf("%s sees the vote %+v, want asker %s, voters %v, yes %v", st.p.name, v, tb.seats[asker].p.name, voters, yes)
		}
	}
}

// ask has seat i ask the others to stop and checks that everybody hears of it.
func (tb *table) ask(i int) {
	tb.t.Helper()
	marks := tb.marks()
	tb.mustCmd(i, game.CmdAskStop, nil)
	tb.waitVote("a vote to stop", func(v *game.StopVoteView) bool { return v != nil })
	tb.wantEvents(marks, game.EventStopAsked)
	other := (i + 1) % len(tb.seats)
	if ev := tb.eventOf(other, marks[other], game.EventStopAsked); ev.PlayerID != tb.seats[i].p.id || ev.Name != tb.seats[i].p.name {
		tb.t.Fatalf("stop_asked = %+v", ev)
	}
}

// vote has seat i answer the open vote. It reports the events every live
// client heard in the process.
func (tb *table) vote(i int, yes bool) (marks []int) {
	tb.t.Helper()
	marks = tb.marks()
	tb.mustCmd(i, game.CmdVoteStop, game.VoteStop{Yes: yes})
	return marks
}

// wantRejected checks that the vote failed for the reason, announced to all.
func (tb *table) wantRejected(marks []int, asker int, reason string) {
	tb.t.Helper()
	tb.waitNoVote()
	tb.wantEvents(marks, game.EventStopRejected)
	for i, st := range tb.seats {
		if st.c == nil || st.c.isClosed() {
			continue
		}
		ev := tb.eventOf(i, marks[i], game.EventStopRejected)
		if ev.PlayerID != tb.seats[asker].p.id || ev.Name != tb.seats[asker].p.name || ev.Reason != reason {
			tb.t.Fatalf("%s heard %+v, want the rejection of %s with reason %q", st.p.name, ev, tb.seats[asker].p.name, reason)
		}
		if v := st.c.view(); v.Phase != game.PhasePlaying {
			tb.t.Fatalf("%s: phase = %s after the vote was rejected", st.p.name, v.Phase)
		}
	}
}

func TestStopVoteHappyPath(t *testing.T) {
	forEachStore(t, func(t *testing.T, open func(*testing.T) store.Store) {
		s := newStack(t, stackOpts{store: open(t)})
		tb := stopTable(t, s, nil)
		ids := tb.ids()

		if !tb.view(ana).Settings.StopVote {
			t.Fatal("the vote to stop is not on by default")
		}
		tb.wantCmdError(ana, game.CodeNoStopVote, game.CmdVoteStop, game.VoteStop{Yes: true})
		tb.wantCmdError(beto, game.CodeStopNotNeeded, game.CmdAskStop, nil) // Beto can call basta

		tb.ask(ana)
		tb.wantVote(ana, []int{beto, cami}, nil)
		if v := tb.view(cami).Round.StopVote; !v.EndsAt.Equal(s.clock.Now().Add(stopVoteFor)) {
			t.Fatalf("endsAt = %v, want now + 15 s", v.EndsAt)
		}
		tb.wantCmdError(ana, game.CodeSelfVote, game.CmdVoteStop, game.VoteStop{Yes: true})
		tb.wantCmdError(ana, game.CodeStopVotePending, game.CmdAskStop, nil)
		tb.wantCmdError(beto, game.CodeStopVotePending, game.CmdAskStop, nil)

		marks := tb.vote(beto, true)
		tb.wantEvents(marks) // an ordinary yes is only a new state
		tb.wantVote(ana, []int{beto, cami}, []int{beto})
		marks = tb.vote(beto, true) // twice is the same as once
		tb.wantEvents(marks)
		tb.wantVote(ana, []int{beto, cami}, []int{beto})
		if tb.view(ana).Phase != game.PhasePlaying {
			t.Fatal("one yes of two passed the vote")
		}

		marks = tb.vote(cami, true)
		tb.waitPhase(game.PhaseGrace)
		tb.wantEvents(marks, game.EventBasta)
		if ev := tb.eventOf(beto, marks[beto], game.EventBasta); ev.PlayerID != ids[ana] || ev.Name != "Ana" {
			t.Fatalf("basta = %+v, want Ana's", ev)
		}
		v := tb.view(beto)
		if v.Round.CallerID != ids[ana] || v.Round.StopVote != nil || !v.Round.GraceEndsAt.Equal(s.clock.Now().Add(graceFor)) {
			t.Fatalf("grace: caller %q vote %v graceEndsAt %v", v.Round.CallerID, v.Round.StopVote, v.Round.GraceEndsAt)
		}
		tb.wantCmdError(beto, game.CodeNoStopVote, game.CmdVoteStop, game.VoteStop{Yes: true})
		tb.wantCmdError(cami, game.CodeWrongPhase, game.CmdAskStop, nil)

		// The countdown is a Basta's: answers still arrive during the grace.
		tb.answer(cami, 1, "Mula")
		s.advance(graceFor + lateFor)
		tb.waitPhase(game.PhaseReview)

		// Ana's empty cells are worth 0, everything else counts as usual.
		v = tb.view(beto)
		for cat := 1; cat <= 2; cat++ {
			if c := v.Round.Result.Players[ids[ana]].Cells[cat]; c.Status != game.StatusEmpty || c.Points != 0 {
				t.Errorf("Ana's cell %d = %+v, want empty and worth 0", cat, c)
			}
		}
		wantInts(t, "review totals", reviewTotals(v, ids...), []int{10, 30, 30})
		tb.mustCmd(ana, game.CmdFinishReview, nil)
		tb.waitPhase(game.PhaseRoundSummary)
		wantInts(t, "scores", totalScores(tb.view(cami), ids...), []int{10, 30, 30})
		if n := tb.assertNoAnswerLeaks(); n == 0 {
			t.Error("no playing or grace view was checked for leaks")
		}
	})
}

func TestStopVoteDeclined(t *testing.T) {
	forEachStore(t, func(t *testing.T, open func(*testing.T) store.Store) {
		s := newStack(t, stackOpts{store: open(t)})
		tb := stopTable(t, s, nil)

		tb.ask(ana)
		marks := tb.vote(cami, false)
		tb.wantRejected(marks, ana, game.StopReasonDeclined)
		// Nobody is told who said no.
		for _, st := range tb.seats {
			for _, f := range st.c.snapshotFrames()[marks[tb.index(st.p.id)]:] {
				if f.Event != nil && bytes.Contains(f.Raw, []byte(tb.seats[cami].p.id)) {
					t.Errorf("%s: the event names the decliner: %s", st.p.name, f.Raw)
				}
			}
		}
		tb.wantCmdError(beto, game.CodeNoStopVote, game.CmdVoteStop, game.VoteStop{Yes: true})

		// No cooldown: Ana asks again at once, and a yes can still become a no.
		tb.ask(ana)
		tb.vote(beto, true)
		tb.wantVote(ana, []int{beto, cami}, []int{beto})
		marks = tb.vote(beto, false)
		tb.wantRejected(marks, ana, game.StopReasonDeclined)

		// Anybody can ask, one vote at a time.
		tb.ask(cami)
		tb.wantVote(cami, []int{ana, beto}, nil)
		marks = tb.vote(ana, false)
		tb.wantRejected(marks, cami, game.StopReasonDeclined)
	})
}

func TestStopVoteSpectatorsCannotVote(t *testing.T) {
	s := newStack(t, stackOpts{})
	tb := stopTable(t, s, nil)
	dani := s.connect(s.newPlayer("Dani")) // joins mid-round: watches until the next one
	dani.join(tb.code)

	if got := dani.roundTrip(game.CmdVoteStop, game.VoteStop{Yes: true}); got != game.CodeNoStopVote {
		t.Fatalf("a vote without a vote: %q", got)
	}
	if got := dani.roundTrip(game.CmdAskStop, nil); got != game.CodeNotParticipant {
		t.Fatalf("a spectator asking: %q", got)
	}
	tb.ask(ana)
	dani.waitState("the vote", func(st *protocol.State) bool { return st.View.Round.StopVote != nil })
	if v := dani.view().Round.StopVote; !slices.Equal(v.Voters, []string{tb.seats[beto].p.id, tb.seats[cami].p.id}) {
		t.Fatalf("the spectator is in the electorate: %v", v.Voters)
	}
	if got := dani.roundTrip(game.CmdVoteStop, game.VoteStop{Yes: false}); got != game.CodeNotParticipant {
		t.Fatalf("a spectator voting: %q", got)
	}
	tb.wantVote(ana, []int{beto, cami}, nil) // still open
	// A spectator's connection is irrelevant to the vote.
	dani.drop()
	tb.wantVote(ana, []int{beto, cami}, nil)
}

func TestStopVoteTimeout(t *testing.T) {
	forEachStore(t, func(t *testing.T, open func(*testing.T) store.Store) {
		s := newStack(t, stackOpts{store: open(t)})
		tb := stopTable(t, s, nil)

		tb.ask(ana)
		tb.vote(beto, true)
		marks := tb.marks()
		s.advance(stopVoteFor - time.Nanosecond)
		tb.wantVote(ana, []int{beto, cami}, []int{beto})
		tb.wantEvents(marks)

		s.advance(time.Nanosecond)
		tb.wantRejected(marks, ana, game.StopReasonTimeout)
		tb.wantCmdError(cami, game.CodeNoStopVote, game.CmdVoteStop, game.VoteStop{Yes: true})

		// No cooldown after a timeout either.
		tb.ask(ana)
		tb.wantVote(ana, []int{beto, cami}, nil)
	})
}

func TestStopVoteTimeLimitEndsIt(t *testing.T) {
	s := newStack(t, stackOpts{})
	tb := stopTable(t, s, func(st *game.Settings) { st.TimeLimitSeconds = 60 })
	s.advance(50 * time.Second)
	tb.ask(ana) // would run until 65 s, the limit is at 60 s
	marks := tb.marks()
	s.advance(10 * time.Second)
	tb.waitPhase(game.PhaseGrace)
	tb.wantEvents(marks, game.EventStopRejected, game.EventTimeUp)
	if ev := tb.eventOf(beto, marks[beto], game.EventStopRejected); ev.Reason != game.StopReasonEnded || ev.PlayerID != tb.seats[ana].p.id {
		t.Fatalf("stop_rejected = %+v", ev)
	}
	if v := tb.view(cami); v.Round.CallerID != "" || v.Round.StopVote != nil {
		t.Fatalf("caller %q vote %v", v.Round.CallerID, v.Round.StopVote)
	}
}

func TestStopVoteEndedByABasta(t *testing.T) {
	s := newStack(t, stackOpts{})
	tb := stopTable(t, s, nil)
	tb.ask(ana)
	marks := tb.marks()
	tb.mustCmd(beto, game.CmdBasta, nil)
	tb.waitPhase(game.PhaseGrace)
	tb.wantEvents(marks, game.EventStopRejected, game.EventBasta)
	if ev := tb.eventOf(cami, marks[cami], game.EventStopRejected); ev.Reason != game.StopReasonEnded || ev.PlayerID != tb.seats[ana].p.id {
		t.Fatalf("stop_rejected = %+v", ev)
	}
	if ev := tb.eventOf(cami, marks[cami], game.EventBasta); ev.PlayerID != tb.seats[beto].p.id {
		t.Fatalf("basta = %+v", ev)
	}
	if v := tb.view(ana); v.Round.CallerID != tb.seats[beto].p.id || v.Round.StopVote != nil {
		t.Fatalf("caller %q vote %v", v.Round.CallerID, v.Round.StopVote)
	}
}

func TestStopVoteConnections(t *testing.T) {
	t.Run("the asker's connection dropping ends it", func(t *testing.T) {
		s := newStack(t, stackOpts{})
		tb := stopTable(t, s, nil)
		tb.ask(ana)
		tb.vote(beto, true)
		marks := tb.marks()
		tb.seats[ana].c.drop()
		tb.waitNoVote()
		tb.wantEvents(marks, game.EventStopRejected)
		if ev := tb.eventOf(beto, marks[beto], game.EventStopRejected); ev.Reason != game.StopReasonEnded || ev.PlayerID != tb.seats[ana].p.id {
			t.Fatalf("stop_rejected = %+v", ev)
		}

		// Coming back does not revive it; she may ask again.
		tb.reconnect(ana)
		if v := tb.view(ana); v.Phase != game.PhasePlaying || v.Round.StopVote != nil {
			t.Fatalf("after the reconnect: phase %s vote %v", v.Phase, v.Round.StopVote)
		}
		tb.ask(ana)
		tb.wantVote(ana, []int{beto, cami}, nil)
	})

	t.Run("a voter's connection dropping lets it pass", func(t *testing.T) {
		s := newStack(t, stackOpts{})
		tb := stopTable(t, s, nil)
		tb.ask(ana)
		tb.vote(beto, true)
		marks := tb.marks()
		tb.seats[cami].c.drop()
		tb.waitPhase(game.PhaseGrace)
		tb.wantEvents(marks, game.EventBasta)
		if v := tb.view(beto); v.Round.CallerID != tb.seats[ana].p.id || v.Round.StopVote != nil {
			t.Fatalf("caller %q vote %v", v.Round.CallerID, v.Round.StopVote)
		}
	})

	t.Run("a player who is offline does not block a new vote and counts again when back", func(t *testing.T) {
		s := newStack(t, stackOpts{})
		tb := stopTable(t, s, nil)
		tb.seats[cami].c.drop()
		tb.waitAll("Cami offline", func(v game.View) bool {
			p, _ := seatOf(v, tb.seats[cami].p.id)
			return !p.Connected
		})
		tb.ask(ana)
		tb.wantVote(ana, []int{beto}, nil)

		tb.reconnect(cami)
		tb.waitVote("Cami back among the voters", func(v *game.StopVoteView) bool {
			return v != nil && len(v.Voters) == 2
		})
		tb.wantVote(ana, []int{beto, cami}, nil)
		tb.vote(beto, true)
		if tb.view(ana).Phase != game.PhasePlaying {
			t.Fatal("the vote passed without the player who is back")
		}
		marks := tb.vote(cami, true)
		tb.waitPhase(game.PhaseGrace)
		tb.wantEvents(marks, game.EventBasta)
	})

	t.Run("a voter leaving lets it pass", func(t *testing.T) {
		s := newStack(t, stackOpts{})
		tb := stopTable(t, s, nil)
		tb.ask(ana)
		tb.vote(beto, true)
		marks := tb.marks()
		tb.seats[cami].c.send(game.CmdLeave, nil)
		tb.seats[cami].c.expectClose(protocol.CloseLeft)
		tb.waitPhase(game.PhaseGrace)
		tb.wantEvents(marks, game.EventBasta)
	})

	t.Run("nobody left to ask passes it at once, without a vote", func(t *testing.T) {
		s := newStack(t, stackOpts{})
		tb := stopTable(t, s, nil)
		tb.seats[beto].c.drop()
		tb.seats[cami].c.drop()
		tb.waitAll("the others are offline", func(v game.View) bool {
			b, _ := seatOf(v, tb.seats[beto].p.id)
			c, _ := seatOf(v, tb.seats[cami].p.id)
			return !b.Connected && !c.Connected
		})
		marks := tb.marks()
		tb.mustCmd(ana, game.CmdAskStop, nil)
		tb.waitPhase(game.PhaseGrace)
		tb.wantEvents(marks, game.EventBasta)
		if v := tb.view(ana); v.Round.CallerID != tb.seats[ana].p.id || v.Round.StopVote != nil {
			t.Fatalf("caller %q vote %v", v.Round.CallerID, v.Round.StopVote)
		}
	})
}

func TestStopVoteSetting(t *testing.T) {
	t.Run("the host switches it in the lobby, nobody else", func(t *testing.T) {
		s := newStack(t, stackOpts{})
		tb := newTable(t, s, "Ana", "Beto", "Cami")
		settings := roundSettings()
		settings.StopVote = false
		tb.wantCmdError(beto, game.CodeNotHost, game.CmdUpdateSettings, game.UpdateSettings{Settings: settings})
		tb.mustCmd(ana, game.CmdUpdateSettings, game.UpdateSettings{Settings: settings})
		tb.waitAll("switched off", func(v game.View) bool { return !v.Settings.StopVote })
		settings.StopVote = true
		tb.mustCmd(ana, game.CmdUpdateSettings, game.UpdateSettings{Settings: settings})
		tb.waitAll("switched on", func(v game.View) bool { return v.Settings.StopVote })
	})

	t.Run("off: asking is refused and basta needs everything filled in", func(t *testing.T) {
		s := newStack(t, stackOpts{})
		tb := stopTable(t, s, func(st *game.Settings) { st.StopVote = false })
		if tb.view(beto).Settings.StopVote {
			t.Fatal("the setting did not reach the players")
		}
		tb.wantCmdError(ana, game.CodeStopVoteDisabled, game.CmdAskStop, nil)
		tb.wantCmdError(cami, game.CodeStopVoteDisabled, game.CmdAskStop, nil)
		tb.wantCmdError(beto, game.CodeNoStopVote, game.CmdVoteStop, game.VoteStop{Yes: true})
		tb.wantCmdError(ana, game.CodeBastaNotAllowed, game.CmdBasta, nil)
		tb.mustCmd(beto, game.CmdBasta, nil)
		tb.waitPhase(game.PhaseGrace)
	})
}

func TestStopVoteMetrics(t *testing.T) {
	s := newStack(t, stackOpts{})
	tb := stopTable(t, s, nil)
	count := func(result string) float64 {
		return s.metricValue("tuttifrutti_stop_votes_total", map[string]string{"result": result})
	}
	want := func(asked, passed, declined, timeout, ended float64) {
		t.Helper()
		got := []float64{count("asked"), count("passed"), count("declined"), count("timeout"), count("ended")}
		if !slices.Equal(got, []float64{asked, passed, declined, timeout, ended}) {
			t.Fatalf("stop votes (asked, passed, declined, timeout, ended) = %v, want %v", got, []float64{asked, passed, declined, timeout, ended})
		}
	}
	want(0, 0, 0, 0, 0)
	if got := s.labelValues("tuttifrutti_stop_votes_total", "result"); len(got) != 5 {
		t.Fatalf("the series exist from the start: %v", got)
	}

	tb.ask(ana)
	tb.vote(cami, false)
	want(1, 0, 1, 0, 0)
	tb.ask(ana)
	s.advance(stopVoteFor)
	tb.waitNoVote()
	want(2, 0, 1, 1, 0)
	tb.ask(ana)
	tb.seats[ana].c.drop()
	tb.waitNoVote()
	want(3, 0, 1, 1, 1)
	tb.reconnect(ana)
	tb.ask(ana)
	tb.vote(beto, true)
	tb.vote(cami, true)
	tb.waitPhase(game.PhaseGrace)
	want(4, 1, 1, 1, 1)
	if got := s.metricValue("tuttifrutti_basta_total", nil); got != 1 {
		t.Errorf("basta_total = %v: only the vote that passed is a basta", got)
	}
	for typ, n := range map[string]float64{game.CmdAskStop: 4, game.CmdVoteStop: 3} {
		if got := s.metricValue("tuttifrutti_commands_total", map[string]string{"type": typ, "result": "ok"}); got != n {
			t.Errorf("commands_total{%s,ok} = %v, want %v", typ, got, n)
		}
	}
	for _, typ := range s.labelValues("tuttifrutti_commands_total", "type") {
		if typ == "unknown" {
			t.Errorf("a command was counted as unknown")
		}
	}
	if got := s.labelValues("tuttifrutti_stop_votes_total", "result"); len(got) != 5 {
		t.Errorf("result labels = %v", got)
	}
}

func TestStopVoteFramesBeforeTheJoinAndRateLimit(t *testing.T) {
	var s *stack
	s = newStack(t, stackOpts{wsNow: func() time.Time { return s.clock.Now() }})
	code, _, host := lobbyOf(t, s)

	// Like every other frame, they need a join first.
	p := s.newPlayer("Early")
	early := s.connect(p)
	for _, typ := range []string{game.CmdAskStop, game.CmdVoteStop} {
		m := early.mark()
		early.send(typ, game.VoteStop{Yes: true})
		if f := early.waitSince(m, "not_joined", isError(protocol.ErrNotJoined)); f.Err.Ref != typ {
			t.Fatalf("not_joined refers to %q, want %q", f.Err.Ref, typ)
		}
	}
	if early.isClosed() {
		t.Fatal("a frame before the join closed the socket")
	}

	// And they spend the connection's tokens like the others: the burst runs
	// out, then the socket is closed with 1008 and rate_limited.
	host.join(code)
	m := host.mark()
	for range protocol.ClientBurst + 5 {
		_ = host.trySend(game.CmdAskStop, nil)
	}
	host.waitSince(m, "rate_limited", isError(protocol.ErrRateLimited))
	host.expectClose(protocol.ClosePolicy)
}

func TestStopVoteRestart(t *testing.T) {
	forEachStore(t, func(t *testing.T, open func(*testing.T) store.Store) {
		// The asker matters: the restore marks the seats offline one at a time,
		// so a vote asked by the last seat would pass by itself once every voter
		// is gone, while one asked by the first ends on the way.
		for _, tc := range []struct {
			name     string
			asker    int
			downtime time.Duration
		}{
			{"asked by the first seat, back within the 15 s", ana, time.Second},
			{"asked by the last seat, back within the 15 s", cami, time.Second},
			{"asked by the last seat, back after the 15 s", cami, time.Minute},
		} {
			t.Run(tc.name, func(t *testing.T) {
				s := newStack(t, stackOpts{store: open(t)})
				tb := stopTable(t, s, nil)
				tb.ask(tc.asker)
				yes := beto
				tb.vote(yes, true)
				tb.wantVote(tc.asker, tb.othersOf(tc.asker), []int{yes})

				tb.restartWith(tc.downtime, func() {
					g, ok := snapshot(t, s.store, tb.code)
					if !ok || g.Round == nil || g.Round.StopVote == nil || g.Round.StopVote.CallerID != tb.seats[tc.asker].p.id {
						t.Fatalf("the final snapshot of a round with a vote: %+v", g.Round)
					}
				})
				first := tb.rejoinAll()
				if v := first.View; v.Phase != game.PhasePlaying || v.Round.StopVote != nil {
					t.Fatalf("after the restart: phase %s vote %v (a stale vote must be dropped, not passed)", v.Phase, v.Round.StopVote)
				}
				tb.waitAll("playing without a vote", func(v game.View) bool {
					return v.Phase == game.PhasePlaying && v.Round.StopVote == nil
				})
				tb.wantCmdError(beto, game.CodeNoStopVote, game.CmdVoteStop, game.VoteStop{Yes: true})

				// The round is intact: the asker asks again and the vote works.
				tb.ask(tc.asker)
				tb.wantVote(tc.asker, tb.othersOf(tc.asker), nil)
				for _, i := range tb.othersOf(tc.asker) {
					tb.vote(i, true)
				}
				tb.waitPhase(game.PhaseGrace)
			})
		}
	})
}

// othersOf lists the seats other than i, in seat order.
func (tb *table) othersOf(i int) []int {
	var out []int
	for j := range tb.seats {
		if j != i {
			out = append(out, j)
		}
	}
	return out
}
