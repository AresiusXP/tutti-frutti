package game

import (
	"encoding/json"
	"errors"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"
)

// stopAt is the instant the stop-vote tests act at: well inside the round.
var stopAt = tStart.Add(5 * time.Second)

// newStopGame returns a playing game of n players (letter A, three categories,
// no time limit) in which p1 has filled one category, so p1 cannot call Basta.
func newStopGame(t *testing.T, n int) *Game {
	t.Helper()
	g := newPlaying(t, n, threeCategories)
	fill(t, g, tStart.Add(time.Second), "p1", "Ana")
	return g
}

// askStopAt opens a vote by p1 at now.
func askStopAt(t *testing.T, g *Game, now time.Time) []Event {
	t.Helper()
	events := apply(t, g, now, "p1", AskStop{})
	if want := []EventType{EventStopAsked}; !reflect.DeepEqual(eventTypes(events), want) {
		t.Fatalf("events = %v, want %v", eventTypes(events), want)
	}
	return events
}

func wantTypes(t *testing.T, events []Event, want ...EventType) {
	t.Helper()
	if want == nil {
		want = []EventType{}
	}
	if got := eventTypes(events); !slices.Equal(got, want) {
		t.Fatalf("events = %v, want %v", got, want)
	}
}

func wantRejected(t *testing.T, e Event, asker, name, reason string) {
	t.Helper()
	if e.Type != EventStopRejected || e.PlayerID != asker || e.Name != name || e.Reason != reason {
		t.Fatalf("event = %+v, want stop_rejected of %s (%s) with reason %q", e, asker, name, reason)
	}
}

// wantVote checks the open vote as clients see it and in the state.
func wantVote(t *testing.T, g *Game, viewer string, voters, yes []string) {
	t.Helper()
	v := g.View(viewer).Round.StopVote
	if v == nil {
		t.Fatalf("no vote to stop in the view of %s", viewer)
	}
	if v.CallerID != "p1" || !slices.Equal(v.Voters, voters) || !slices.Equal(v.Yes, yes) {
		t.Fatalf("vote view = %+v, want voters %v yes %v", v, voters, yes)
	}
	if v.Voters == nil || v.Yes == nil {
		t.Fatalf("voters and yes must never be nil: %+v", v)
	}
}

func wantNoVote(t *testing.T, g *Game) {
	t.Helper()
	if g.Round.StopVote != nil {
		t.Fatalf("the vote is still open: %+v", g.Round.StopVote)
	}
	for _, viewer := range []string{"p1", "p2", "p3", "stranger"} {
		if v := g.View(viewer).Round.StopVote; v != nil {
			t.Fatalf("the view of %s still shows a vote: %+v", viewer, v)
		}
	}
}

func TestAskStopOpensAVote(t *testing.T) {
	g := newStopGame(t, 3)
	events := askStopAt(t, g, stopAt)
	if e := events[0]; e.PlayerID != "p1" || e.Name != "Player 1" || e.Reason != "" {
		t.Errorf("event = %+v", e)
	}
	v := g.Round.StopVote
	if v == nil || v.CallerID != "p1" || !v.EndsAt.Equal(stopAt.Add(StopVoteDuration)) || v.Yes == nil || len(v.Yes) != 0 {
		t.Fatalf("vote = %+v", v)
	}
	if StopVoteDuration != 15*time.Second {
		t.Errorf("StopVoteDuration = %v, want 15s", StopVoteDuration)
	}
	if g.Phase != PhasePlaying || g.Round.CallerID != "" || !g.Round.GraceEndsAt.IsZero() {
		t.Errorf("asking must not start the grace: phase %s caller %q", g.Phase, g.Round.CallerID)
	}
	// Everyone sees the same vote, the asker and the spectators too.
	for _, viewer := range []string{"p1", "p2", "p3", "stranger"} {
		wantVote(t, g, viewer, []string{"p2", "p3"}, []string{})
		if got := g.View(viewer).Round.StopVote.EndsAt; !got.Equal(v.EndsAt) {
			t.Errorf("EndsAt for %s = %v", viewer, got)
		}
	}
	if d, ok := g.NextDeadline(); !ok || !d.Equal(v.EndsAt) {
		t.Errorf("NextDeadline = %v, %v, want the end of the vote %v", d, ok, v.EndsAt)
	}
}

func TestAskStopGuards(t *testing.T) {
	tests := []struct {
		name  string
		setup func(t *testing.T) (*Game, string)
		want  error
	}{
		{"in the lobby", func(t *testing.T) (*Game, string) { return newLobby(t, 3), "p1" }, ErrWrongPhase},
		{"in the letter phase", func(t *testing.T) (*Game, string) {
			g := newLobby(t, 3)
			apply(t, g, tStart, "p1", StartGame{})
			return g, "p2"
		}, ErrWrongPhase},
		{"in grace", func(t *testing.T) (*Game, string) {
			g := newStopGame(t, 3)
			fill(t, g, stopAt, "p2", "Alba", "Angola", "Alce")
			apply(t, g, stopAt, "p2", Basta{})
			return g, "p1"
		}, ErrWrongPhase},
		{"in the review", func(t *testing.T) (*Game, string) {
			g, _ := newReview(t, ansP1, ansP1, ansP1)
			return g, "p2"
		}, ErrWrongPhase},
		{"a spectator who joined mid-round", func(t *testing.T) (*Game, string) {
			g := newStopGame(t, 3)
			apply(t, g, stopAt, "p4", Join{Name: "Late"})
			return g, "p4"
		}, ErrNotParticipant},
		{"switched off by the host", func(t *testing.T) (*Game, string) {
			g := newPlaying(t, 3, func(s *Settings) { threeCategories(s); s.StopVote = false })
			return g, "p1"
		}, ErrStopVoteDisabled},
		{"switched off beats filled in", func(t *testing.T) (*Game, string) {
			g := newPlaying(t, 3, func(s *Settings) { threeCategories(s); s.StopVote = false })
			fill(t, g, stopAt, "p1", "Ana", "Argentina", "Alce")
			return g, "p1"
		}, ErrStopVoteDisabled},
		{"a vote is already open", func(t *testing.T) (*Game, string) {
			g := newStopGame(t, 3)
			askStopAt(t, g, stopAt)
			return g, "p2"
		}, ErrStopVotePending},
		{"the asker asking again", func(t *testing.T) (*Game, string) {
			g := newStopGame(t, 3)
			askStopAt(t, g, stopAt)
			return g, "p1"
		}, ErrStopVotePending},
		{"pending beats filled in", func(t *testing.T) (*Game, string) {
			g := newStopGame(t, 3)
			askStopAt(t, g, stopAt)
			fill(t, g, stopAt, "p2", "Alba", "Angola", "Alce")
			return g, "p2"
		}, ErrStopVotePending},
		{"every category filled", func(t *testing.T) (*Game, string) {
			g := newStopGame(t, 3)
			fill(t, g, stopAt, "p1", "Ana", "Argentina", "Alce")
			return g, "p1"
		}, ErrStopNotNeeded},
		{"filled in with accents and case is still filled in", func(t *testing.T) (*Game, string) {
			g := newStopGame(t, 3)
			fill(t, g, stopAt, "p1", "ana", "ÁLAVA", "alce")
			return g, "p1"
		}, ErrStopNotNeeded},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g, actor := tt.setup(t)
			expectErr(t, g, stopAt, actor, AskStop{}, tt.want)
		})
	}

	t.Run("a wrong letter does not count as filled in", func(t *testing.T) {
		g := newStopGame(t, 3)
		fill(t, g, stopAt, "p1", "Ana", "Brasil", "Alce")
		wantTypes(t, apply(t, g, stopAt, "p1", AskStop{}), EventStopAsked)
	})
	t.Run("an unknown player", func(t *testing.T) {
		g := newStopGame(t, 3)
		expectErr(t, g, stopAt, "ghost", AskStop{}, ErrUnknownPlayer)
	})
	t.Run("after the time limit locked the round", func(t *testing.T) {
		g := newPlaying(t, 3, func(s *Settings) { threeCategories(s); s.TimeLimitSeconds = 60 })
		events, err := g.Apply(tStart.Add(60*time.Second), "p1", AskStop{})
		if !errors.Is(err, ErrWrongPhase) {
			t.Fatalf("err = %v, want ErrWrongPhase", err)
		}
		wantTypes(t, events, EventTimeUp)
		wantNoVote(t, g)
	})
}

func TestAskStopPassesAtOnceWhenNobodyCanVote(t *testing.T) {
	tests := []struct {
		name  string
		setup func(t *testing.T) *Game
	}{
		{"the others are disconnected", func(t *testing.T) *Game {
			g := newStopGame(t, 3)
			setConnected(t, g, stopAt, "p2", false)
			setConnected(t, g, stopAt, "p3", false)
			return g
		}},
		{"the only other player left", func(t *testing.T) *Game {
			g := newStopGame(t, 2)
			apply(t, g, stopAt, "p2", Leave{})
			return g
		}},
		{"the only other participant is offline and the rest are not playing", func(t *testing.T) *Game {
			g := newStopGame(t, 2)
			apply(t, g, stopAt, "p3", Join{Name: "Late"}) // connected, but not a participant
			setConnected(t, g, stopAt, "p2", false)
			return g
		}},
		{"a kicked player is no voter", func(t *testing.T) *Game {
			g := newStopGame(t, 2)
			apply(t, g, stopAt, "p1", Kick{PlayerID: "p2"})
			return g
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := tt.setup(t)
			at := stopAt.Add(time.Second)
			events := apply(t, g, at, "p1", AskStop{})
			wantTypes(t, events, EventBasta) // no stop_asked: nobody was asked
			if e := events[0]; e.PlayerID != "p1" || e.Name != "Player 1" {
				t.Errorf("event = %+v", e)
			}
			if g.Phase != PhaseGrace || g.Round.CallerID != "p1" || !g.Round.GraceEndsAt.Equal(at.Add(GraceDuration)) {
				t.Errorf("phase %s caller %q graceEndsAt %v", g.Phase, g.Round.CallerID, g.Round.GraceEndsAt)
			}
			wantNoVote(t, g)
		})
	}
}

func TestVoteStop(t *testing.T) {
	t.Run("a yes from everyone passes the vote like a basta", func(t *testing.T) {
		g := newStopGame(t, 3)
		askStopAt(t, g, stopAt)

		at := stopAt.Add(2 * time.Second)
		wantTypes(t, apply(t, g, at, "p2", VoteStop{Yes: true}))
		wantVote(t, g, "p3", []string{"p2", "p3"}, []string{"p2"})
		if g.Phase != PhasePlaying {
			t.Fatalf("phase = %s: one yes of two is not enough", g.Phase)
		}

		at = stopAt.Add(3 * time.Second)
		events := apply(t, g, at, "p3", VoteStop{Yes: true})
		wantTypes(t, events, EventBasta)
		if e := events[0]; e.PlayerID != "p1" || e.Name != "Player 1" {
			t.Errorf("event = %+v, want the basta of the asker", e)
		}
		if g.Phase != PhaseGrace || g.Round.CallerID != "p1" || !g.Round.GraceEndsAt.Equal(at.Add(GraceDuration)) {
			t.Errorf("phase %s caller %q graceEndsAt %v", g.Phase, g.Round.CallerID, g.Round.GraceEndsAt)
		}
		wantNoVote(t, g)
		if d, ok := g.NextDeadline(); !ok || !d.Equal(at.Add(GraceDuration+LateUpdateAllowance)) {
			t.Errorf("NextDeadline = %v, %v", d, ok)
		}
	})

	t.Run("a yes twice counts once", func(t *testing.T) {
		g := newStopGame(t, 3)
		askStopAt(t, g, stopAt)
		apply(t, g, stopAt, "p2", VoteStop{Yes: true})
		wantTypes(t, apply(t, g, stopAt, "p2", VoteStop{Yes: true}))
		if g.Phase != PhasePlaying {
			t.Fatal("the same voter twice must not pass the vote")
		}
		wantVote(t, g, "p1", []string{"p2", "p3"}, []string{"p2"})
	})

	t.Run("the order of the votes does not matter", func(t *testing.T) {
		g := newStopGame(t, 3)
		askStopAt(t, g, stopAt)
		apply(t, g, stopAt, "p3", VoteStop{Yes: true})
		wantVote(t, g, "p1", []string{"p2", "p3"}, []string{"p3"})
		wantTypes(t, apply(t, g, stopAt, "p2", VoteStop{Yes: true}), EventBasta)
	})

	t.Run("a single no rejects it at once", func(t *testing.T) {
		g := newStopGame(t, 4)
		askStopAt(t, g, stopAt)
		apply(t, g, stopAt, "p2", VoteStop{Yes: true})
		apply(t, g, stopAt, "p3", VoteStop{Yes: true})
		events := apply(t, g, stopAt, "p4", VoteStop{Yes: false})
		wantTypes(t, events, EventStopRejected)
		wantRejected(t, events[0], "p1", "Player 1", StopReasonDeclined)
		if g.Phase != PhasePlaying {
			t.Errorf("phase = %s", g.Phase)
		}
		wantNoVote(t, g)
		// The decliner is named nowhere: the event only names the asker, and
		// neither the state nor the view keep a trace of the vote.
		b, _ := json.Marshal(events[0])
		if strings.Contains(string(b), "p4") || strings.Contains(string(b), "Player 4") {
			t.Errorf("the event names the decliner: %s", b)
		}
		for _, out := range []any{g.View("p1"), g.Round} {
			if b, _ := json.Marshal(out); strings.Contains(string(b), `"stopVote":{`) {
				t.Errorf("a trace of the vote is left: %s", b)
			}
		}
	})

	t.Run("the first voter can say no", func(t *testing.T) {
		g := newStopGame(t, 3)
		askStopAt(t, g, stopAt)
		wantTypes(t, apply(t, g, stopAt, "p3", VoteStop{Yes: false}), EventStopRejected)
		expectErr(t, g, stopAt, "p2", VoteStop{Yes: true}, ErrNoStopVote)
	})

	t.Run("a voter may change a yes into a no", func(t *testing.T) {
		g := newStopGame(t, 3)
		askStopAt(t, g, stopAt)
		apply(t, g, stopAt, "p2", VoteStop{Yes: true})
		events := apply(t, g, stopAt, "p2", VoteStop{Yes: false})
		wantTypes(t, events, EventStopRejected)
		wantRejected(t, events[0], "p1", "Player 1", StopReasonDeclined)
		wantNoVote(t, g)
	})

	errs := []struct {
		name  string
		setup func(t *testing.T) *Game
		actor string
		yes   bool
		want  error
	}{
		{"no vote while playing", func(t *testing.T) *Game { return newStopGame(t, 3) }, "p2", true, ErrNoStopVote},
		{"no vote while playing, a no", func(t *testing.T) *Game { return newStopGame(t, 3) }, "p2", false, ErrNoStopVote},
		{"no vote in the lobby", func(t *testing.T) *Game { return newLobby(t, 3) }, "p2", true, ErrNoStopVote},
		{"no vote in the letter phase", func(t *testing.T) *Game {
			g := newLobby(t, 3)
			apply(t, g, tStart, "p1", StartGame{})
			return g
		}, "p2", true, ErrNoStopVote},
		{"no vote in grace", func(t *testing.T) *Game {
			g := newPlaying(t, 3, threeCategories)
			fill(t, g, stopAt, "p1", "Ana", "Argentina", "Alce")
			apply(t, g, stopAt, "p1", Basta{})
			return g
		}, "p2", true, ErrNoStopVote},
		{"no vote in the review", func(t *testing.T) *Game {
			g, _ := newReview(t, ansP1, ansP1, ansP1)
			return g
		}, "p2", true, ErrNoStopVote},
		{"the asker cannot vote yes", func(t *testing.T) *Game {
			g := newStopGame(t, 3)
			askStopAt(t, g, stopAt)
			return g
		}, "p1", true, ErrSelfVote},
		{"the asker cannot vote no", func(t *testing.T) *Game {
			g := newStopGame(t, 3)
			askStopAt(t, g, stopAt)
			return g
		}, "p1", false, ErrSelfVote},
		{"a spectator cannot vote", func(t *testing.T) *Game {
			g := newStopGame(t, 3)
			apply(t, g, stopAt, "p4", Join{Name: "Late"})
			askStopAt(t, g, stopAt)
			return g
		}, "p4", false, ErrNotParticipant},
		{"an unknown player cannot vote", func(t *testing.T) *Game {
			g := newStopGame(t, 3)
			askStopAt(t, g, stopAt)
			return g
		}, "ghost", true, ErrUnknownPlayer},
	}
	for _, tt := range errs {
		t.Run(tt.name, func(t *testing.T) {
			expectErr(t, tt.setup(t), stopAt, tt.actor, VoteStop{Yes: tt.yes}, tt.want)
		})
	}

	t.Run("a refused vote leaves the open vote alone", func(t *testing.T) {
		g := newStopGame(t, 3)
		askStopAt(t, g, stopAt)
		expectErr(t, g, stopAt, "p1", VoteStop{Yes: false}, ErrSelfVote)
		wantVote(t, g, "p2", []string{"p2", "p3"}, []string{})
	})
}

func TestStopVoteDeadlineIsExclusive(t *testing.T) {
	t.Run("Tick", func(t *testing.T) {
		g := newStopGame(t, 3)
		askStopAt(t, g, stopAt)
		end := stopAt.Add(StopVoteDuration)

		if events := g.Tick(end.Add(-time.Nanosecond)); len(events) != 0 || g.Round.StopVote == nil {
			t.Fatalf("1ns before the deadline: events %v vote %v", events, g.Round.StopVote)
		}
		events := g.Tick(end)
		wantTypes(t, events, EventStopRejected)
		wantRejected(t, events[0], "p1", "Player 1", StopReasonTimeout)
		wantNoVote(t, g)
		if g.Phase != PhasePlaying {
			t.Errorf("phase = %s: a timeout leaves the round going", g.Phase)
		}
		if d, ok := g.NextDeadline(); ok {
			t.Errorf("NextDeadline = %v: nothing is left to wait for", d)
		}
		if events := g.Tick(end.Add(time.Hour)); len(events) != 0 {
			t.Errorf("a second tick produced %v", events)
		}
	})

	t.Run("a yes 1ns before the deadline still counts", func(t *testing.T) {
		g := newStopGame(t, 2)
		askStopAt(t, g, stopAt)
		wantTypes(t, apply(t, g, stopAt.Add(StopVoteDuration-time.Nanosecond), "p2", VoteStop{Yes: true}), EventBasta)
	})

	t.Run("a yes at the deadline is too late and the vote reports its timeout", func(t *testing.T) {
		g := newStopGame(t, 2)
		askStopAt(t, g, stopAt)
		events, err := g.Apply(stopAt.Add(StopVoteDuration), "p2", VoteStop{Yes: true})
		if !errors.Is(err, ErrNoStopVote) {
			t.Fatalf("err = %v, want ErrNoStopVote", err)
		}
		wantTypes(t, events, EventStopRejected)
		wantRejected(t, events[0], "p1", "Player 1", StopReasonTimeout)
		if g.Phase != PhasePlaying {
			t.Errorf("phase = %s", g.Phase)
		}
	})

	t.Run("a connection change at the deadline reports the timeout first", func(t *testing.T) {
		g := newStopGame(t, 3)
		askStopAt(t, g, stopAt)
		apply(t, g, stopAt, "p2", VoteStop{Yes: true})
		// p3 is the last voter: leaving would pass the vote, but not after the deadline.
		events := setConnected(t, g, stopAt.Add(StopVoteDuration), "p3", false)
		wantTypes(t, events, EventStopRejected)
		wantRejected(t, events[0], "p1", "Player 1", StopReasonTimeout)
		if g.Phase != PhasePlaying {
			t.Errorf("phase = %s", g.Phase)
		}
	})

	t.Run("an ask at the deadline of the previous vote is a new vote", func(t *testing.T) {
		g := newStopGame(t, 3)
		askStopAt(t, g, stopAt)
		at := stopAt.Add(StopVoteDuration)
		events := apply(t, g, at, "p2", AskStop{})
		wantTypes(t, events, EventStopRejected, EventStopAsked)
		if v := g.Round.StopVote; v == nil || v.CallerID != "p2" || !v.EndsAt.Equal(at.Add(StopVoteDuration)) {
			t.Errorf("vote = %+v", v)
		}
	})
}

func TestStopVoteAndTheTimeLimit(t *testing.T) {
	limit := 60 * time.Second
	newLimited := func(t *testing.T) *Game {
		g := newPlaying(t, 3, func(s *Settings) { threeCategories(s); s.TimeLimitSeconds = 60 })
		fill(t, g, tStart.Add(time.Second), "p1", "Ana")
		return g
	}

	tests := []struct {
		name         string
		askAfter     time.Duration // since the start of the round
		wantDeadline time.Duration // NextDeadline since the start of the round
	}{
		{"the vote ends before the limit", 10 * time.Second, 10*time.Second + StopVoteDuration},
		{"the vote would end after the limit", 50 * time.Second, limit},
		{"the vote ends exactly at the limit", limit - StopVoteDuration, limit},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := newLimited(t)
			askStopAt(t, g, tStart.Add(tt.askAfter))
			if d, ok := g.NextDeadline(); !ok || !d.Equal(tStart.Add(tt.wantDeadline)) {
				t.Fatalf("NextDeadline = %v, %v, want round start + %v", d, ok, tt.wantDeadline)
			}
		})
	}

	t.Run("the time limit ends the vote before time_up", func(t *testing.T) {
		for _, askAfter := range []time.Duration{50 * time.Second, limit - StopVoteDuration} {
			g := newLimited(t)
			askStopAt(t, g, tStart.Add(askAfter))
			events := g.Tick(tStart.Add(limit))
			wantTypes(t, events, EventStopRejected, EventTimeUp)
			wantRejected(t, events[0], "p1", "Player 1", StopReasonEnded)
			if g.Phase != PhaseGrace || g.Round.CallerID != "" {
				t.Errorf("phase %s caller %q", g.Phase, g.Round.CallerID)
			}
			wantNoVote(t, g)
		}
	})

	t.Run("an answer vote that arrives after the limit finds the round locked", func(t *testing.T) {
		g := newLimited(t)
		askStopAt(t, g, tStart.Add(50*time.Second))
		events, err := g.Apply(tStart.Add(limit), "p2", VoteStop{Yes: true})
		if !errors.Is(err, ErrNoStopVote) {
			t.Fatalf("err = %v", err)
		}
		wantTypes(t, events, EventStopRejected, EventTimeUp)
	})

	t.Run("a very late tick reports the timeout, the time limit and the lock in order", func(t *testing.T) {
		g := newLimited(t)
		askStopAt(t, g, tStart.Add(10*time.Second))
		events := g.Tick(tStart.Add(time.Hour))
		wantTypes(t, events, EventStopRejected, EventTimeUp, EventRoundLocked)
		wantRejected(t, events[0], "p1", "Player 1", StopReasonTimeout)
		if g.Phase != PhaseReview {
			t.Errorf("phase = %s", g.Phase)
		}
		wantNoVote(t, g)
	})

	t.Run("a vote that passes before the limit is a normal basta", func(t *testing.T) {
		g := newLimited(t)
		askStopAt(t, g, tStart.Add(10*time.Second))
		at := tStart.Add(12 * time.Second)
		wantTypes(t, apply(t, g, at, "p2", VoteStop{Yes: true}))
		wantTypes(t, apply(t, g, at, "p3", VoteStop{Yes: true}), EventBasta)
		// The time limit still applies to what is left of the round, but the
		// grace phase has its own deadline.
		if d, ok := g.NextDeadline(); !ok || !d.Equal(at.Add(GraceDuration+LateUpdateAllowance)) {
			t.Errorf("NextDeadline = %v, %v", d, ok)
		}
	})
}

func TestStopVotePassedThenReview(t *testing.T) {
	g := newStopGame(t, 3)
	fill(t, g, stopAt, "p2", "Alba", "Angola", "Alce")
	fill(t, g, stopAt, "p3", "Ana", "Argentina", "")
	askStopAt(t, g, stopAt)
	apply(t, g, stopAt, "p2", VoteStop{Yes: true})
	passAt := stopAt.Add(2 * time.Second)
	wantTypes(t, apply(t, g, passAt, "p3", VoteStop{Yes: true}), EventBasta)

	// The countdown is the one of a Basta: answers are still accepted.
	apply(t, g, passAt.Add(time.Second), "p1", SetAnswer{Category: 1, Text: "Alemania"})
	if events := g.Tick(passAt.Add(GraceDuration + LateUpdateAllowance - time.Nanosecond)); len(events) != 0 {
		t.Fatalf("early tick: %v", events)
	}
	events := g.Tick(passAt.Add(GraceDuration + LateUpdateAllowance))
	wantTypes(t, events, EventRoundLocked)

	res := liveResult(t, g)
	// p1: Ana (shared with p3: "Ana" == "Ana" → 5), Alemania (unique 10), empty (0).
	if c := cell(t, res, "p1", 0); c.Points != 5 {
		t.Errorf("p1 category 0 = %+v", c)
	}
	if c := cell(t, res, "p1", 1); c.Points != 10 {
		t.Errorf("p1 category 1 = %+v", c)
	}
	if c := cell(t, res, "p1", 2); c.Points != 0 || c.Status != StatusEmpty {
		t.Errorf("p1 category 2 = %+v, want an empty cell worth 0", c)
	}
	if c := cell(t, res, "p3", 2); c.Points != 0 || c.Status != StatusEmpty {
		t.Errorf("p3 category 2 = %+v, want an empty cell worth 0", c)
	}
	if got := res.Players["p1"].Total; got != 15 {
		t.Errorf("p1 total = %d, want 15", got)
	}
	apply(t, g, passAt.Add(time.Minute), "p1", FinishReview{})
	if got := g.Players[0].TotalScore; got != 15 {
		t.Errorf("p1 score = %d", got)
	}
}

func TestStopVoteLiveElectorate(t *testing.T) {
	t.Run("a voter who disconnects stops counting and may complete the vote", func(t *testing.T) {
		g := newStopGame(t, 3)
		askStopAt(t, g, stopAt)
		apply(t, g, stopAt, "p2", VoteStop{Yes: true})
		at := stopAt.Add(3 * time.Second)
		events := setConnected(t, g, at, "p3", false)
		wantTypes(t, events, EventBasta)
		if g.Phase != PhaseGrace || g.Round.CallerID != "p1" || !g.Round.GraceEndsAt.Equal(at.Add(GraceDuration)) {
			t.Errorf("phase %s caller %q graceEndsAt %v", g.Phase, g.Round.CallerID, g.Round.GraceEndsAt)
		}
		wantNoVote(t, g)
	})

	t.Run("a voter who disconnects without the others agreeing changes nothing yet", func(t *testing.T) {
		g := newStopGame(t, 4)
		askStopAt(t, g, stopAt)
		apply(t, g, stopAt, "p2", VoteStop{Yes: true})
		wantTypes(t, setConnected(t, g, stopAt, "p4", false))
		if g.Phase != PhasePlaying {
			t.Fatalf("phase = %s: p3 has not voted", g.Phase)
		}
		wantVote(t, g, "p1", []string{"p2", "p3"}, []string{"p2"})
		wantTypes(t, apply(t, g, stopAt, "p3", VoteStop{Yes: true}), EventBasta)
	})

	t.Run("every voter offline passes it", func(t *testing.T) {
		g := newStopGame(t, 3)
		askStopAt(t, g, stopAt)
		wantTypes(t, setConnected(t, g, stopAt, "p2", false))
		wantVote(t, g, "p1", []string{"p3"}, []string{})
		wantTypes(t, setConnected(t, g, stopAt, "p3", false), EventBasta)
		if g.Phase != PhaseGrace {
			t.Errorf("phase = %s", g.Phase)
		}
	})

	t.Run("offline players do not block a vote that is already open", func(t *testing.T) {
		g := newStopGame(t, 3)
		setConnected(t, g, stopAt, "p3", false)
		askStopAt(t, g, stopAt)
		wantVote(t, g, "p1", []string{"p2"}, []string{})
		wantTypes(t, apply(t, g, stopAt, "p2", VoteStop{Yes: true}), EventBasta)
	})

	t.Run("a player who reconnects joins the electorate", func(t *testing.T) {
		g := newStopGame(t, 3)
		setConnected(t, g, stopAt, "p3", false)
		askStopAt(t, g, stopAt)
		at := stopAt.Add(time.Second)
		wantTypes(t, setConnected(t, g, at, "p3", true))
		wantVote(t, g, "p1", []string{"p2", "p3"}, []string{})
		wantTypes(t, apply(t, g, at, "p2", VoteStop{Yes: true}))
		if g.Phase != PhasePlaying {
			t.Fatal("p3 is a voter again, the vote cannot pass without them")
		}
		wantTypes(t, apply(t, g, at, "p3", VoteStop{Yes: true}), EventBasta)
	})

	t.Run("a yes survives a dropped connection", func(t *testing.T) {
		g := newStopGame(t, 3)
		askStopAt(t, g, stopAt)
		apply(t, g, stopAt, "p2", VoteStop{Yes: true})
		setConnected(t, g, stopAt, "p2", false)
		wantVote(t, g, "p1", []string{"p3"}, []string{}) // yes is only reported for voters
		setConnected(t, g, stopAt, "p2", true)
		wantVote(t, g, "p1", []string{"p2", "p3"}, []string{"p2"})
		wantTypes(t, apply(t, g, stopAt, "p3", VoteStop{Yes: true}), EventBasta)
	})

	t.Run("the connection of a bystander is irrelevant", func(t *testing.T) {
		g := newStopGame(t, 3)
		apply(t, g, stopAt, "p4", Join{Name: "Late"})
		askStopAt(t, g, stopAt)
		wantTypes(t, setConnected(t, g, stopAt, "p4", false))
		wantTypes(t, setConnected(t, g, stopAt, "p4", true))
		wantVote(t, g, "p1", []string{"p2", "p3"}, []string{})
	})

	t.Run("a voter who leaves stops counting", func(t *testing.T) {
		g := newStopGame(t, 3)
		askStopAt(t, g, stopAt)
		apply(t, g, stopAt, "p2", VoteStop{Yes: true})
		at := stopAt.Add(time.Second)
		events := apply(t, g, at, "p3", Leave{})
		wantTypes(t, events, EventBasta)
		if g.Phase != PhaseGrace || !g.Round.GraceEndsAt.Equal(at.Add(GraceDuration)) {
			t.Errorf("phase %s graceEndsAt %v", g.Phase, g.Round.GraceEndsAt)
		}
	})

	t.Run("a voter who leaves while others still have to vote changes nothing else", func(t *testing.T) {
		g := newStopGame(t, 4)
		askStopAt(t, g, stopAt)
		wantTypes(t, apply(t, g, stopAt, "p4", Leave{}))
		wantVote(t, g, "p1", []string{"p2", "p3"}, []string{})
	})

	t.Run("a kicked voter stops counting", func(t *testing.T) {
		g := newStopGame(t, 3)
		askStopAt(t, g, stopAt)
		apply(t, g, stopAt, "p2", VoteStop{Yes: true})
		events := apply(t, g, stopAt, "p1", Kick{PlayerID: "p3"})
		wantTypes(t, events, EventKicked, EventBasta)
		if g.Phase != PhaseGrace || g.Round.CallerID != "p1" {
			t.Errorf("phase %s caller %q", g.Phase, g.Round.CallerID)
		}
	})
}

func TestStopVoteEnds(t *testing.T) {
	asker := []struct {
		name string
		act  func(t *testing.T, g *Game) []Event
		want []EventType
	}{
		{"the asker disconnects", func(t *testing.T, g *Game) []Event {
			return setConnected(t, g, stopAt.Add(time.Second), "p1", false)
		}, []EventType{EventStopRejected}},
		{"the asker leaves", func(t *testing.T, g *Game) []Event {
			return apply(t, g, stopAt.Add(time.Second), "p1", Leave{})
		}, []EventType{EventStopRejected, EventHostChanged}},
	}
	for _, tt := range asker {
		t.Run(tt.name, func(t *testing.T) {
			g := newStopGame(t, 3)
			askStopAt(t, g, stopAt)
			events := tt.act(t, g)
			wantTypes(t, events, tt.want...)
			wantRejected(t, events[0], "p1", "Player 1", StopReasonEnded)
			if g.Phase != PhasePlaying {
				t.Errorf("phase = %s", g.Phase)
			}
			wantNoVote(t, g)
		})
	}

	t.Run("the asker is kicked by the host", func(t *testing.T) {
		g := newStopGame(t, 3)
		fill(t, g, stopAt, "p2", "Alba")
		apply(t, g, stopAt, "p2", AskStop{})
		events := apply(t, g, stopAt, "p1", Kick{PlayerID: "p2"})
		wantTypes(t, events, EventKicked, EventStopRejected)
		wantRejected(t, events[1], "p2", "Player 2", StopReasonEnded)
		wantNoVote(t, g)
	})

	t.Run("the asker coming back does not revive the vote", func(t *testing.T) {
		g := newStopGame(t, 3)
		askStopAt(t, g, stopAt)
		setConnected(t, g, stopAt.Add(time.Second), "p1", false)
		wantTypes(t, setConnected(t, g, stopAt.Add(2*time.Second), "p1", true))
		wantNoVote(t, g)
		expectErr(t, g, stopAt.Add(3*time.Second), "p2", VoteStop{Yes: true}, ErrNoStopVote)
	})

	t.Run("a basta by someone else ends the vote first", func(t *testing.T) {
		g := newStopGame(t, 3)
		askStopAt(t, g, stopAt)
		fill(t, g, stopAt, "p2", "Alba", "Angola", "Alce")
		at := stopAt.Add(time.Second)
		events := apply(t, g, at, "p2", Basta{})
		wantTypes(t, events, EventStopRejected, EventBasta)
		wantRejected(t, events[0], "p1", "Player 1", StopReasonEnded)
		if e := events[1]; e.PlayerID != "p2" || e.Name != "Player 2" {
			t.Errorf("basta event = %+v", e)
		}
		if g.Phase != PhaseGrace || g.Round.CallerID != "p2" || !g.Round.GraceEndsAt.Equal(at.Add(GraceDuration)) {
			t.Errorf("phase %s caller %q graceEndsAt %v", g.Phase, g.Round.CallerID, g.Round.GraceEndsAt)
		}
		wantNoVote(t, g)
	})

	t.Run("a basta by the asker once everything is filled in", func(t *testing.T) {
		g := newStopGame(t, 3)
		askStopAt(t, g, stopAt)
		fill(t, g, stopAt, "p1", "Ana", "Argentina", "Alce") // answers still work during a vote
		events := apply(t, g, stopAt, "p1", Basta{})
		wantTypes(t, events, EventStopRejected, EventBasta)
		wantRejected(t, events[0], "p1", "Player 1", StopReasonEnded)
		wantNoVote(t, g)
	})

	t.Run("a refused basta leaves the vote open", func(t *testing.T) {
		g := newStopGame(t, 3)
		askStopAt(t, g, stopAt)
		expectErr(t, g, stopAt, "p2", Basta{}, ErrBastaNotAllowed)
		wantVote(t, g, "p1", []string{"p2", "p3"}, []string{})
	})

	t.Run("the vote does not carry over to the next round", func(t *testing.T) {
		g := newStopGame(t, 2)
		askStopAt(t, g, stopAt)
		apply(t, g, stopAt, "p2", VoteStop{Yes: true})
		at := stopAt.Add(GraceDuration + LateUpdateAllowance)
		g.Tick(at)
		apply(t, g, at, "p1", FinishReview{})
		apply(t, g, at, "p1", NextRound{})
		if g.Phase != PhaseLetter || g.Round.StopVote != nil {
			t.Fatalf("phase %s vote %v", g.Phase, g.Round.StopVote)
		}
		apply(t, g, at, g.ChooserID, StopLetter{})
		wantTypes(t, apply(t, g, at, "p1", AskStop{}), EventStopAsked)
	})
}

func TestStopVoteHasNoCooldown(t *testing.T) {
	t.Run("after a no", func(t *testing.T) {
		g := newStopGame(t, 3)
		askStopAt(t, g, stopAt)
		apply(t, g, stopAt, "p2", VoteStop{Yes: false})
		again := askStopAt(t, g, stopAt) // the very same instant
		if again[0].PlayerID != "p1" {
			t.Errorf("event = %+v", again[0])
		}
		wantVote(t, g, "p1", []string{"p2", "p3"}, []string{})
		// The new vote does not remember the old one.
		if !g.Round.StopVote.EndsAt.Equal(stopAt.Add(StopVoteDuration)) {
			t.Errorf("EndsAt = %v", g.Round.StopVote.EndsAt)
		}
	})
	t.Run("after a timeout", func(t *testing.T) {
		g := newStopGame(t, 3)
		askStopAt(t, g, stopAt)
		at := stopAt.Add(StopVoteDuration)
		g.Tick(at)
		askStopAt(t, g, at)
		wantTypes(t, apply(t, g, at, "p2", VoteStop{Yes: true}))
		wantTypes(t, apply(t, g, at, "p3", VoteStop{Yes: true}), EventBasta)
	})
	t.Run("another player may ask right after, and ask again many times", func(t *testing.T) {
		g := newStopGame(t, 3)
		apply(t, g, stopAt, "p2", AskStop{})
		apply(t, g, stopAt, "p1", VoteStop{Yes: false})
		for range 5 {
			wantTypes(t, apply(t, g, stopAt, "p3", AskStop{}), EventStopAsked)
			wantTypes(t, apply(t, g, stopAt, "p1", VoteStop{Yes: false}), EventStopRejected)
		}
	})
}

func TestStopVoteSettings(t *testing.T) {
	t.Run("on by default and toggled by the host in the lobby", func(t *testing.T) {
		g := newLobby(t, 3)
		if !g.Settings.StopVote || !DefaultSettings().StopVote || !g.View("p2").Settings.StopVote {
			t.Fatal("the vote to stop must be on by default")
		}
		s := g.Settings
		s.StopVote = false
		apply(t, g, t0, "p1", UpdateSettings{Settings: s})
		if g.Settings.StopVote || g.View("p2").Settings.StopVote || g.Settings.Clean().StopVote {
			t.Error("the setting was not stored")
		}
		expectErr(t, g, t0, "p2", UpdateSettings{Settings: DefaultSettings()}, ErrNotHost)
		if err := g.Settings.Validate(); err != nil {
			t.Errorf("both values are valid: %v", err)
		}
	})

	t.Run("the setting cannot change while playing", func(t *testing.T) {
		g := newStopGame(t, 3)
		s := g.Settings
		s.StopVote = false
		expectErr(t, g, stopAt, "p1", UpdateSettings{Settings: s}, ErrWrongPhase)
		wantTypes(t, apply(t, g, stopAt, "p1", AskStop{}), EventStopAsked)
	})

	t.Run("a rematch keeps it either way", func(t *testing.T) {
		for _, on := range []bool{true, false} {
			g := newPlaying(t, 2, func(s *Settings) { threeCategories(s); s.StopVote = on })
			g.Phase = PhaseFinal // skip the rounds
			g.Winners = []string{"p1"}
			apply(t, g, stopAt, "p1", Rematch{})
			if g.Settings.StopVote != on || g.Round != nil {
				t.Errorf("after the rematch StopVote = %v (round %v), want %v", g.Settings.StopVote, g.Round, on)
			}
		}
	})

	t.Run("a snapshot from before the setting restores with it off", func(t *testing.T) {
		g := newStopGame(t, 3)
		var raw map[string]json.RawMessage
		if err := json.Unmarshal([]byte(snapshot(t, g)), &raw); err != nil {
			t.Fatal(err)
		}
		var settings map[string]json.RawMessage
		if err := json.Unmarshal(raw["settings"], &settings); err != nil {
			t.Fatal(err)
		}
		if _, ok := settings["stopVote"]; !ok {
			t.Fatal("the snapshot has no stopVote field to remove")
		}
		delete(settings, "stopVote")
		raw["settings"], _ = json.Marshal(settings)
		b, _ := json.Marshal(raw)

		var old Game
		if err := json.Unmarshal(b, &old); err != nil {
			t.Fatal(err)
		}
		if old.Settings.StopVote || old.Round.StopVote != nil {
			t.Fatalf("settings %+v vote %v", old.Settings, old.Round.StopVote)
		}
		expectErr(t, &old, stopAt, "p1", AskStop{}, ErrStopVoteDisabled)
		// Everything else works as before.
		fill(t, &old, stopAt, "p1", "Ana", "Argentina", "Alce")
		wantTypes(t, apply(t, &old, stopAt, "p1", Basta{}), EventBasta)
	})
}

func TestStopVoteJSON(t *testing.T) {
	g := newStopGame(t, 3)
	askStopAt(t, g, stopAt)
	apply(t, g, stopAt, "p3", VoteStop{Yes: true})

	// The state round-trips with the vote in it and behaves the same.
	g2 := restore(t, g)
	if a, b := snapshot(t, g), snapshot(t, g2); a != b {
		t.Fatalf("round trip differs:\n%s\n%s", a, b)
	}
	if !strings.Contains(snapshot(t, g), `"stopVote":{"callerId":"p1","endsAt":"2026-01-02T12:01:20Z","yes":["p3"]}`) {
		t.Errorf("state JSON = %s", snapshot(t, g))
	}
	for _, viewer := range []string{"p1", "p2", "p3", "stranger"} {
		if a, b := viewJSON(t, g, viewer), viewJSON(t, g2, viewer); a != b {
			t.Errorf("view of %s differs:\n%s\n%s", viewer, a, b)
		}
	}
	if !strings.Contains(viewJSON(t, g, "p2"), `"stopVote":{"callerId":"p1","endsAt":"2026-01-02T12:01:20Z","voters":["p2","p3"],"yes":["p3"]}`) {
		t.Errorf("view JSON = %s", viewJSON(t, g, "p2"))
	}
	wantTypes(t, apply(t, g2, stopAt, "p2", VoteStop{Yes: true}), EventBasta)
	d1, ok1 := g.NextDeadline()
	d2, ok2 := restore(t, g).NextDeadline()
	if !ok1 || !ok2 || !d1.Equal(d2) {
		t.Errorf("NextDeadline %v,%v became %v,%v", d1, ok1, d2, ok2)
	}

	// Without a vote the key is absent from both.
	if h := newStopGame(t, 3); strings.Contains(snapshot(t, h), `"stopVote":{`) || strings.Contains(viewJSON(t, h, "p1"), `"stopVote":{`) {
		t.Error("a game without a vote must not mention one")
	}

	e, _ := json.Marshal(Event{Type: EventStopRejected, PlayerID: "p1", Name: "Ana", Reason: StopReasonTimeout})
	if string(e) != `{"type":"stop_rejected","playerId":"p1","name":"Ana","reason":"timeout"}` {
		t.Errorf("event JSON = %s", e)
	}
	e, _ = json.Marshal(Event{Type: EventStopAsked, PlayerID: "p1", Name: "Ana"})
	if string(e) != `{"type":"stop_asked","playerId":"p1","name":"Ana"}` {
		t.Errorf("event JSON = %s", e)
	}
}

func TestStopVoteViewOnlyWhilePlaying(t *testing.T) {
	vote := &StopVote{CallerID: "p1", EndsAt: stopAt, Yes: []string{"p2"}}
	for _, name := range []string{
		"letter", "playing", "grace", "grace by time limit", "review", "round_summary", "final",
	} {
		t.Run(name, func(t *testing.T) {
			g := gameInPhase(t, name)
			if g.Phase == PhasePlaying {
				apply(t, g, stopAt, "p1", AskStop{})
				if g.View("p3").Round.StopVote == nil {
					t.Fatal("no vote in the playing phase")
				}
				return
			}
			// The engine never leaves a vote behind, but the view must not
			// show a stale one whatever the state says.
			g.Round.StopVote = vote
			for _, viewer := range []string{"p1", "p2", "stranger"} {
				if v := g.View(viewer).Round.StopVote; v != nil {
					t.Errorf("phase %s shows a vote to %s: %+v", g.Phase, viewer, v)
				}
			}
		})
	}
	for _, name := range []string{"lobby", "after rematch"} {
		g := gameInPhase(t, name)
		if g.Round != nil || g.View("p1").Round != nil {
			t.Errorf("%s: a lobby has no round, hence no vote", name)
		}
	}
}

func TestParseStopCommands(t *testing.T) {
	tests := []struct {
		typ     string
		payload string
		want    Command
	}{
		{"ask_stop", ``, AskStop{}},
		{"ask_stop", `null`, AskStop{}},
		{"ask_stop", `{}`, AskStop{}},
		{"ask_stop", `{"ignored":1}`, AskStop{}},
		{"vote_stop", `{"yes":true}`, VoteStop{Yes: true}},
		{"vote_stop", `{"yes":false}`, VoteStop{Yes: false}},
		{"vote_stop", ` {"yes": true} `, VoteStop{Yes: true}},
	}
	for _, tt := range tests {
		got, err := ParseCommand(tt.typ, json.RawMessage(tt.payload))
		if err != nil || !reflect.DeepEqual(got, tt.want) || got.Type() != tt.typ {
			t.Errorf("ParseCommand(%q, %q) = %#v, %v; want %#v", tt.typ, tt.payload, got, err, tt.want)
		}
	}
	if CmdAskStop != "ask_stop" || CmdVoteStop != "vote_stop" {
		t.Error("the wire names changed")
	}

	for _, tt := range []struct{ typ, payload string }{
		{"vote_stop", `{"yes":"yes"}`},
		{"vote_stop", `{"yes":1}`},
		{"vote_stop", `{"yes":`},
		{"vote_stop", `[true]`},
		{"vote_stop", `true`},
		{"vote_stop", `"yes"`},
		{"ask_stop", `{"x":`},
		{"ask_stop", `[]`},
		{"ask_stop", `7`},
	} {
		cmd, err := ParseCommand(tt.typ, json.RawMessage(tt.payload))
		if !errors.Is(err, ErrBadPayload) || cmd != nil {
			t.Errorf("ParseCommand(%q, %q) = %#v, %v; want ErrBadPayload", tt.typ, tt.payload, cmd, err)
		}
	}
}

func TestStopErrorCodes(t *testing.T) {
	for _, tt := range []struct {
		err  error
		code string
	}{
		{ErrStopVoteDisabled, "stop_vote_disabled"},
		{ErrStopVotePending, "stop_vote_pending"},
		{ErrNoStopVote, "no_stop_vote"},
		{ErrStopNotNeeded, "stop_not_needed"},
	} {
		if got := ErrorCode(tt.err); got != tt.code {
			t.Errorf("ErrorCode(%v) = %q, want %q", tt.err, got, tt.code)
		}
	}
}
