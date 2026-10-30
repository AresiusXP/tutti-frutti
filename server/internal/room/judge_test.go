package room

import (
	"context"
	"errors"
	"runtime"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/AresiusXP/tutti-frutti/server/internal/game"
	"github.com/AresiusXP/tutti-frutti/server/internal/judge"
	"github.com/AresiusXP/tutti-frutti/server/internal/protocol"
	"github.com/AresiusXP/tutti-frutti/server/internal/store"
)

// fakeJudge answers when the test lets it: every call blocks until release.
type fakeJudge struct {
	mu       sync.Mutex
	requests []judge.Request
	started  chan struct{}
	gate     chan struct{}
	returned chan struct{}
	// reply builds the answer; the default judges "Ana" valid and the rest not.
	reply func(judge.Request) ([]judge.Result, error)
}

func newFakeJudge() *fakeJudge {
	return &fakeJudge{started: make(chan struct{}, 8), gate: make(chan struct{}, 8), returned: make(chan struct{}, 8)}
}

func (f *fakeJudge) Judge(_ context.Context, req judge.Request) ([]judge.Result, error) {
	f.mu.Lock()
	f.requests = append(f.requests, req)
	f.mu.Unlock()
	f.started <- struct{}{}
	<-f.gate
	defer func() { f.returned <- struct{}{} }()
	if f.reply != nil {
		return f.reply(req)
	}
	out := make([]judge.Result, len(req.Answers))
	for i, a := range req.Answers {
		out[i] = judge.Result{Known: true, Valid: a == "Ana", Reason: "reason of " + a}
	}
	return out, nil
}

func (f *fakeJudge) calls() []judge.Request {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.requests)
}

// newJudgeReview returns a lobby in the review of round 1. The host p1 is
// signed in as hostUser (a guest when empty), p2 is signed in too and p3 is a
// guest. A nil j means the server has no judge.
func newJudgeReview(t *testing.T, j Judge, hostUser string) *lobby3 {
	t.Helper()
	e := newEnv(t, func(c *Config) { c.Judge = j })
	l := &lobby3{e: e, code: e.create("p1", "Ana")}
	l.c[0], l.s[0] = e.joinAs(l.code, "p1", "Ana", hostUser)
	l.c[1], l.s[1] = e.joinAs(l.code, "p2", "Beto", "user-2")
	l.c[2], l.s[2] = e.join(l.code, "p3", "Cami")
	l.startRound(nil)
	l.fillAnswers()
	l.send(0, game.CmdBasta, nil)
	e.advance(game.GraceDuration + game.LateUpdateAllowance)
	if phase := query(e, l.code, func(r *room) game.Phase { return r.g.Phase }); phase != game.PhaseReview {
		t.Fatalf("setup: phase = %s, want review", phase)
	}
	l.drain()
	return l
}

func (l *lobby3) roundView(i int) *game.RoundView {
	l.e.t.Helper()
	return lastState(l.e.t, l.c[i].drain()).View.Round
}

func (l *lobby3) waitVerdicts(n int) {
	l.e.t.Helper()
	eventually(l.e.t, "the verdicts", func() bool {
		l.e.sync(l.code)
		return query(l.e, l.code, func(r *room) int { return len(r.g.Round.Verdicts) }) == n
	})
}

func TestAskJudgeOverTheWire(t *testing.T) {
	j := newFakeJudge()
	l := newJudgeReview(t, j, "user-1")

	l.send(0, game.CmdAskJudge, game.AskJudge{Category: 0})
	frames := [3][]frameRec{l.c[0].drain(), l.c[1].drain(), l.c[2].drain()}
	for i := range frames {
		if got := kinds(frames[i]); !slices.Equal(got, []string{"state"}) {
			t.Fatalf("p%d frames = %v", i+1, got)
		}
		if rv := frames[i][0].state(t).View.Round; !slices.Equal(rv.JudgePending, []int{0}) {
			t.Errorf("p%d sees pending %v", i+1, rv.JudgePending)
		}
	}

	<-j.started
	req := j.calls()[0]
	if req.Lang != "es" || req.Category != "Nombre" || req.Letter != "A" || !slices.Equal(req.Answers, []string{"Ana", "Alberto", "Ana"}) {
		t.Errorf("request = %+v", req)
	}
	j.gate <- struct{}{}
	l.waitVerdicts(3)

	for i := range 3 {
		rv := l.roundView(i)
		if len(rv.JudgePending) != 0 || len(rv.Verdicts) != 3 {
			t.Fatalf("p%d: pending %v verdicts %v", i+1, rv.JudgePending, rv.Verdicts)
		}
		if got := rv.Verdicts[0]; got != (game.AIVerdict{Category: 0, Player: "p1", Verdict: game.VerdictValid, Reason: "reason of Ana"}) {
			t.Errorf("p%d: first verdict = %+v", i+1, got)
		}
		if got := rv.Verdicts[1]; got.Player != "p2" || got.Verdict != game.VerdictDoubtful {
			t.Errorf("p%d: second verdict = %+v", i+1, got)
		}
	}

	l.send(0, game.CmdAskJudge, game.AskJudge{Category: 0})
	l.expectFrames("asking twice", []string{"error:already_judged"}, 0)
	if m := metricValue(t, l.e.reg, "tuttifrutti_commands_total", "type", game.CmdAskJudge, "result", "ok"); m != 1 {
		t.Errorf("ask_judge counted %v times", m)
	}
}

func TestOnlyASignedInHostCanAskTheJudge(t *testing.T) {
	t.Run("a player who is not the host", func(t *testing.T) {
		j := newFakeJudge()
		l := newJudgeReview(t, j, "user-1")
		l.send(1, game.CmdAskJudge, game.AskJudge{Category: 0})
		l.expectFrames("ask by a player", []string{"error:not_host"}, 1)
		if n := len(j.calls()); n != 0 {
			t.Errorf("the judge was called %d times", n)
		}
	})

	t.Run("a guest host", func(t *testing.T) {
		j := newFakeJudge()
		l := newJudgeReview(t, j, "")
		l.send(0, game.CmdAskJudge, game.AskJudge{Category: 0})
		l.expectFrames("ask by a guest", []string{"error:" + protocol.ErrLoginRequired}, 0)
		if n := len(j.calls()); n != 0 {
			t.Errorf("the judge was called %d times", n)
		}
		l.send(0, game.CmdReviewNav, game.ReviewNav{Category: 1})
		if st := lastState(t, l.c[0].drain()); st.CanJudge {
			t.Error("a guest must not be offered the judge")
		}
	})

	t.Run("a server without a judge", func(t *testing.T) {
		l := newJudgeReview(t, nil, "user-1")
		l.send(0, game.CmdAskJudge, game.AskJudge{Category: 0})
		l.expectFrames("ask without a judge", []string{"error:" + protocol.ErrJudgeUnavailable}, 0)
		l.send(0, game.CmdReviewNav, game.ReviewNav{Category: 1})
		if st := lastState(t, l.c[0].drain()); st.CanJudge {
			t.Error("canJudge must be false without a judge")
		}
	})

	t.Run("a judge the operator switched off", func(t *testing.T) {
		sw := judge.NewSwitch(newFakeJudge(), nil)
		sw.SetEnabled(false)
		l := newJudgeReview(t, sw, "user-1")
		l.send(0, game.CmdAskJudge, game.AskJudge{Category: 0})
		l.expectFrames("ask while the judge is off", []string{"error:" + protocol.ErrJudgeUnavailable}, 0)
		l.send(0, game.CmdReviewNav, game.ReviewNav{Category: 1})
		if st := lastState(t, l.c[0].drain()); st.CanJudge {
			t.Error("canJudge must be false while the operator switched the judge off")
		}
		sw.SetEnabled(true)
		l.send(0, game.CmdReviewNav, game.ReviewNav{Category: 0})
		if st := lastState(t, l.c[0].drain()); !st.CanJudge {
			t.Error("canJudge must come back when the judge is switched on again")
		}
	})

	t.Run("canJudge follows the account", func(t *testing.T) {
		l := newJudgeReview(t, newFakeJudge(), "user-1")
		l.send(0, game.CmdReviewNav, game.ReviewNav{Category: 1})
		for i, want := range []bool{true, true, false} {
			if got := lastState(t, l.c[i].drain()).CanJudge; got != want {
				t.Errorf("p%d canJudge = %v, want %v", i+1, got, want)
			}
		}
	})
}

func TestJudgeFailureIsAnnouncedAndCanBeRetried(t *testing.T) {
	for name, tc := range map[string]struct {
		err    error
		reason string
	}{
		"busy":  {judge.ErrBusy, game.JudgeReasonBusy},
		"error": {errors.New("boom"), game.JudgeReasonError},
	} {
		t.Run(name, func(t *testing.T) {
			j := newFakeJudge()
			j.reply = func(judge.Request) ([]judge.Result, error) { return nil, tc.err }
			l := newJudgeReview(t, j, "user-1")

			l.send(0, game.CmdAskJudge, game.AskJudge{Category: 1})
			l.drain()
			<-j.started
			j.gate <- struct{}{}
			eventually(t, "the failure", func() bool {
				l.e.sync(l.code)
				return query(l.e, l.code, func(r *room) int { return len(r.g.Round.JudgePending) }) == 0
			})
			for i := range 3 {
				frames := l.c[i].drain()
				if got := kinds(frames); !slices.Equal(got, []string{"event:judge_failed", "state"}) {
					t.Fatalf("p%d frames = %v", i+1, got)
				}
				if ev := frames[0].event(t); ev.Reason != tc.reason {
					t.Errorf("p%d: reason = %q, want %q", i+1, ev.Reason, tc.reason)
				}
				if rv := frames[1].state(t).View.Round; len(rv.JudgePending) != 0 || len(rv.Verdicts) != 0 {
					t.Errorf("p%d: %+v", i+1, rv)
				}
			}

			j.reply = nil
			l.send(0, game.CmdAskJudge, game.AskJudge{Category: 1}) // the host may try again
			<-j.started
			j.gate <- struct{}{}
			l.waitVerdicts(3)
		})
	}
}

func TestJudgeWithoutAnyVerdictCountsAsAFailure(t *testing.T) {
	j := newFakeJudge()
	j.reply = func(req judge.Request) ([]judge.Result, error) { return make([]judge.Result, len(req.Answers)), nil }
	l := newJudgeReview(t, j, "user-1")
	l.send(0, game.CmdAskJudge, game.AskJudge{Category: 0})
	<-j.started
	j.gate <- struct{}{}
	eventually(t, "the failure", func() bool {
		l.e.sync(l.code)
		return query(l.e, l.code, func(r *room) int { return len(r.g.Round.JudgePending) }) == 0
	})
	if n := query(l.e, l.code, func(r *room) int { return len(r.g.Round.Verdicts) }); n != 0 {
		t.Errorf("verdicts = %d", n)
	}
}

func TestAJudgeThatPanicsDoesNotTakeTheServerDown(t *testing.T) {
	j := newFakeJudge()
	j.reply = func(judge.Request) ([]judge.Result, error) { panic("model exploded") }
	l := newJudgeReview(t, j, "user-1")
	l.send(0, game.CmdAskJudge, game.AskJudge{Category: 0})
	<-j.started
	j.gate <- struct{}{}
	eventually(t, "the failure", func() bool {
		l.e.sync(l.code)
		return query(l.e, l.code, func(r *room) int { return len(r.g.Round.JudgePending) }) == 0
	})
	if !strings.Contains(l.e.logs.String(), "the judge panicked") {
		t.Error("the panic was not logged")
	}
}

func TestAJudgeAnswerAfterTheReviewIsDropped(t *testing.T) {
	j := newFakeJudge()
	l := newJudgeReview(t, j, "user-1")
	l.send(0, game.CmdAskJudge, game.AskJudge{Category: 0})
	<-j.started

	l.send(0, game.CmdFinishReview, nil) // the host moves on while the judge thinks
	l.drain()
	j.gate <- struct{}{}
	<-j.returned
	for range 20 { // the result is posted right after the call returns
		l.e.sync(l.code)
		runtime.Gosched()
	}
	if frames := l.c[0].drain(); len(frames) != 0 {
		t.Errorf("a stale result must be silent, got %v", kinds(frames))
	}
	if n := query(l.e, l.code, func(r *room) int { return len(r.g.Round.Verdicts) }); n != 0 {
		t.Errorf("verdicts = %d", n)
	}
}

func TestAPendingQuestionDoesNotSurviveARestart(t *testing.T) {
	j := newFakeJudge()
	l := newJudgeReview(t, j, "user-1")
	l.send(0, game.CmdAskJudge, game.AskJudge{Category: 0})
	<-j.started
	query(l.e, l.code, func(r *room) bool { r.persist(r.clock.Now()); return true })
	l.e.waitSaved(l.code)
	if _, g := l.e.stored(l.code); len(g.Round.JudgePending) != 1 {
		t.Fatalf("setup: the snapshot has pending %v", g.Round.JudgePending)
	}

	snaps, err := l.e.store.Memory.LoadAll(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	copyStore := store.NewMemory() // the first hub keeps writing to the original
	for _, s := range snaps {
		if err := copyStore.Save(context.Background(), s); err != nil {
			t.Fatal(err)
		}
	}
	h2 := l.e.newHubOn(func(c *Config) { c.Store = copyStore; c.Judge = newFakeJudge() })
	if n, err := h2.Start(context.Background()); err != nil || n != 1 {
		t.Fatalf("Start = %d, %v", n, err)
	}
	if pending := query(&env{t: t, hub: h2}, l.code, func(r *room) []int { return r.g.Round.JudgePending }); len(pending) != 0 {
		t.Errorf("pending after the restart = %v", pending)
	}
	j.gate <- struct{}{} // let the first process's call end
}
