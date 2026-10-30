package room

import (
	"context"
	"errors"
	"fmt"
	"runtime/debug"
	"time"

	"github.com/AresiusXP/tutti-frutti/server/internal/game"
	"github.com/AresiusXP/tutti-frutti/server/internal/judge"
	"github.com/AresiusXP/tutti-frutti/server/internal/protocol"
)

// judgeTimeout bounds one call to the judge (its HTTP client has its own,
// shorter timeout; this also covers waiting for a free slot).
const judgeTimeout = 30 * time.Second

// judgeEnabled reports whether the server has an AI judge that is switched on.
// A Judge may implement Enabled() bool (the operator's kill switch,
// judge.Switch); one that does not is always on.
func (r *room) judgeEnabled() bool {
	j := r.h.cfg.Judge
	if j == nil {
		return false
	}
	if sw, ok := j.(interface{ Enabled() bool }); ok {
		return sw.Enabled()
	}
	return true
}

// canJudge reports whether s may ask the AI judge: the server has one that is
// switched on and the player is signed in. Guests cannot, so that the shared
// allowance is not spent anonymously. The state a player sees refreshes with
// the next frame after the operator flips the switch; an ask_judge in between
// is refused by judgeRefusal.
func (r *room) canJudge(s *session) bool {
	return r.judgeEnabled() && s.userID != ""
}

// judgeRefusal is the error code for an ask_judge that must not reach the
// engine, or "".
func (r *room) judgeRefusal(s *session) string {
	switch {
	case !r.judgeEnabled():
		return protocol.ErrJudgeUnavailable
	case s.userID == "":
		return protocol.ErrLoginRequired
	}
	return ""
}

// startJudge sends the question the engine just opened to the judge, off the
// room's goroutine; the answer comes back as a task (onJudged). The engine
// stays pure: it only records that the question is open.
func (r *room) startJudge(category int) {
	q := r.g.JudgeQuery(category)
	j := r.h.cfg.Judge
	req := judge.Request{Lang: string(q.Lang), Category: q.Label, Letter: q.Letter}
	for _, a := range q.Answers {
		req.Answers = append(req.Answers, a.Text)
	}
	go func() {
		results, err := r.callJudge(j, req)
		r.post(func(r *room) { r.onJudged(q, results, err) }, nil)
	}()
}

// callJudge calls the judge and turns a panic into an error: this goroutine
// is not under the room's recover, and a panic there would stop the server.
func (r *room) callJudge(j Judge, req judge.Request) (results []judge.Result, err error) {
	defer func() {
		if p := recover(); p != nil {
			r.log.Error("the judge panicked", "panic", p, "stack", string(debug.Stack()))
			err = fmt.Errorf("judge panicked: %v", p)
		}
	}()
	ctx, cancel := context.WithTimeout(context.Background(), judgeTimeout)
	defer cancel()
	return j.Judge(ctx, req)
}

// onJudged applies the judge's answer on the room's goroutine. A result for a
// question that is no longer open (the round moved on) is dropped.
func (r *room) onJudged(q game.JudgeQuery, results []judge.Result, err error) {
	now := r.clock.Now()
	prev := r.g.Phase

	// The operator may have switched the judge off while the answer travelled
	// to this goroutine: nothing is applied after that (the Switch already
	// discards what is answered later).
	if err == nil && !r.judgeEnabled() {
		err = judge.ErrDisabled
	}

	var verdicts []game.AIVerdict
	if err == nil {
		for i, res := range results {
			if i >= len(q.Answers) || !res.Known {
				continue
			}
			v := game.VerdictDoubtful
			if res.Valid {
				v = game.VerdictValid
			}
			verdicts = append(verdicts, game.AIVerdict{Player: q.Answers[i].Player, Verdict: v, Reason: res.Reason})
		}
		if len(verdicts) == 0 {
			err = errors.New("the judge gave no verdict")
		}
	}

	if err != nil {
		reason := game.JudgeReasonError
		if errors.Is(err, judge.ErrBusy) {
			reason = game.JudgeReasonBusy
		} else {
			r.log.Warn("the AI judge failed", "room", r.code, "err", err)
		}
		if events := r.g.JudgeFailed(q.Round, q.Category, reason); len(events) > 0 {
			r.commit(now, prev, events, false)
		}
		return
	}
	if r.g.ApplyVerdicts(q.Round, q.Category, verdicts) {
		r.commit(now, prev, nil, false)
	}
}
