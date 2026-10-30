package room

import (
	"context"
	"slices"
	"sync/atomic"
	"testing"

	"github.com/AresiusXP/tutti-frutti/server/internal/game"
	"github.com/AresiusXP/tutti-frutti/server/internal/judge"
)

// flipJudge answers with a verdict for every answer and switches itself off
// right before it returns: the verdict is "in flight" to the room when the
// operator's switch goes off (the gap the room's own check closes).
type flipJudge struct {
	enabled atomic.Bool
	started chan struct{}
	gate    chan struct{}
}

func (f *flipJudge) Enabled() bool { return f.enabled.Load() }

func (f *flipJudge) Judge(_ context.Context, req judge.Request) ([]judge.Result, error) {
	f.started <- struct{}{}
	<-f.gate
	f.enabled.Store(false)
	out := make([]judge.Result, len(req.Answers))
	for i := range out {
		out[i] = judge.Result{Known: true, Valid: true, Reason: "too late"}
	}
	return out, nil
}

// No verdict is applied to the game once the judge was switched off, whether
// the Switch discarded it (the provider answered after the switch went off) or
// the switch went off while the verdict travelled to the room.
func TestNoVerdictIsAppliedAfterTheJudgeWasSwitchedOff(t *testing.T) {
	failed := func(t *testing.T, l *lobby3) {
		t.Helper()
		eventually(t, "the failure", func() bool {
			l.e.sync(l.code)
			return query(l.e, l.code, func(r *room) int { return len(r.g.Round.JudgePending) }) == 0
		})
		for i := range 3 {
			frames := l.c[i].drain()
			if got := kinds(frames); !slices.Equal(got, []string{"event:judge_failed", "state"}) {
				t.Fatalf("p%d frames = %v, want the soft failure and no verdicts", i+1, got)
			}
			if ev := frames[0].event(t); ev.Reason != game.JudgeReasonBusy {
				t.Errorf("p%d: reason = %q, want %q", i+1, ev.Reason, game.JudgeReasonBusy)
			}
		}
		if n := query(l.e, l.code, func(r *room) int { return len(r.g.Round.Verdicts) }); n != 0 {
			t.Errorf("%d verdicts were applied after the switch went off", n)
		}
	}

	t.Run("the provider answers after the Switch went off", func(t *testing.T) {
		inner := newFakeJudge()
		sw := judge.NewSwitch(inner, nil)
		l := newJudgeReview(t, sw, "user-1")
		l.send(0, game.CmdAskJudge, game.AskJudge{Category: 0})
		l.drain()
		<-inner.started
		sw.SetEnabled(false)
		inner.gate <- struct{}{}
		failed(t, l)
	})

	t.Run("the switch goes off while the verdict travels to the room", func(t *testing.T) {
		f := &flipJudge{started: make(chan struct{}, 1), gate: make(chan struct{}, 1)}
		f.enabled.Store(true)
		l := newJudgeReview(t, f, "user-1")
		l.send(0, game.CmdAskJudge, game.AskJudge{Category: 0})
		l.drain()
		<-f.started
		f.gate <- struct{}{}
		failed(t, l)
	})
}
