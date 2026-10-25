package game

import (
	"strings"
	"testing"
)

// reviewAnswers: category 0 (name) has p1 "Ana", p2 "Ana", p3 "Bruno" (wrong
// letter), category 1 has p2 empty.
var (
	judgeP1 = []string{"Ana", "Argentina", "Alce"}
	judgeP2 = []string{"Ana", "", "Alce"}
	judgeP3 = []string{"Bruno", "Austria", "Alce"}
)

func TestJudgeQueryHandsOverOnlyAnswersWorthJudging(t *testing.T) {
	g, _ := newReview(t, judgeP1, judgeP2, judgeP3)

	q := g.JudgeQuery(0)
	if q.Round != 1 || q.Category != 0 || q.Letter != "A" || q.Lang != g.Settings.Lang || q.Label == "" {
		t.Fatalf("query = %+v", q)
	}
	if len(q.Answers) != 2 || q.Answers[0] != (JudgeAnswer{"p1", "Ana"}) || q.Answers[1] != (JudgeAnswer{"p2", "Ana"}) {
		t.Errorf("answers = %+v (the wrong letter must not be sent)", q.Answers)
	}
	if got := g.JudgeQuery(1).Answers; len(got) != 2 {
		t.Errorf("an empty answer must not be sent, got %+v", got)
	}
	if got := g.JudgeQuery(9); len(got.Answers) != 0 {
		t.Errorf("an unknown category has nothing to judge, got %+v", got)
	}
}

func TestAskJudgeGuards(t *testing.T) {
	g, now := newReview(t, judgeP1, judgeP2, judgeP3)

	expectErr(t, g, now, "p2", AskJudge{Category: 0}, ErrNotHost)
	expectErr(t, g, now, "p1", AskJudge{Category: 3}, ErrInvalidCategory)
	expectErr(t, g, now, "p1", AskJudge{Category: -1}, ErrInvalidCategory)

	h := newPlaying(t, 2, nil)
	expectErr(t, h, tStart.Add(1), "p1", AskJudge{Category: 0}, ErrWrongPhase)

	// Nothing to judge: every answer is empty or has the wrong letter.
	n, now := newReview(t, judgeP1, judgeP2, judgeP3)
	n.Round.Answers["p1"][2], n.Round.Answers["p2"][2], n.Round.Answers["p3"][2] = "", "Zebra", ""
	expectErr(t, n, now, "p1", AskJudge{Category: 2}, ErrNothingToJudge)
}

func TestAskJudgeOpensOneQuestionPerCategory(t *testing.T) {
	g, now := newReview(t, judgeP1, judgeP2, judgeP3)

	if events := apply(t, g, now, "p1", AskJudge{Category: 0}); len(events) != 0 {
		t.Errorf("asking is silent (the state tells), got %v", events)
	}
	if v := g.View("p2"); len(v.Round.JudgePending) != 1 || v.Round.JudgePending[0] != 0 {
		t.Errorf("everybody sees the pending question, got %v", v.Round.JudgePending)
	}
	expectErr(t, g, now, "p1", AskJudge{Category: 0}, ErrJudgePending)
	apply(t, g, now, "p1", AskJudge{Category: 1}) // other categories are independent
	if len(g.Round.JudgePending) != 2 {
		t.Errorf("pending = %v", g.Round.JudgePending)
	}
}

func TestApplyVerdictsStoresAdvisoryOpinions(t *testing.T) {
	g, now := newReview(t, judgeP1, judgeP2, judgeP3)
	apply(t, g, now, "p1", AskJudge{Category: 0})
	before := g.score()

	long := strings.Repeat("é", MaxVerdictReasonLen+20)
	ok := g.ApplyVerdicts(1, 0, []AIVerdict{
		{Category: 7, Player: "p1", Verdict: VerdictValid, Reason: "  A\u200b girl's\n\tname "},
		{Player: "p2", Verdict: VerdictDoubtful, Reason: long},
		{Player: "p2", Verdict: VerdictValid},                // a second verdict for the same answer
		{Player: "p3", Verdict: VerdictValid},                // p3's answer was not asked about
		{Player: "ghost", Verdict: VerdictValid},             // not a player
		{Player: "p1", Verdict: "maybe", Reason: "whatever"}, // not a verdict
	})
	if !ok {
		t.Fatal("ApplyVerdicts refused an open question")
	}
	got := g.Round.Verdicts
	if len(got) != 2 {
		t.Fatalf("verdicts = %+v", got)
	}
	if got[0] != (AIVerdict{Category: 0, Player: "p1", Verdict: VerdictValid, Reason: "A girl's name"}) {
		t.Errorf("first verdict = %+v (category is the question's, reason is cleaned)", got[0])
	}
	if got[1].Verdict != VerdictDoubtful || len([]rune(got[1].Reason)) != MaxVerdictReasonLen {
		t.Errorf("second verdict = %+v", got[1])
	}
	if len(g.Round.JudgePending) != 0 {
		t.Errorf("the question must be closed, pending = %v", g.Round.JudgePending)
	}
	if after := g.score(); after.Players["p1"].Total != before.Players["p1"].Total || after.Players["p2"].Total != before.Players["p2"].Total {
		t.Error("a verdict must never change the score")
	}
	if v := g.View("p3"); len(v.Round.Verdicts) != 2 {
		t.Errorf("everybody sees the verdicts, got %v", v.Round.Verdicts)
	}
	expectErr(t, g, now, "p1", AskJudge{Category: 0}, ErrAlreadyJudged)
	if g.ApplyVerdicts(1, 0, nil) {
		t.Error("a closed question must not take a second answer")
	}
}

func TestJudgeFailedLetsTheHostTryAgain(t *testing.T) {
	g, now := newReview(t, judgeP1, judgeP2, judgeP3)
	apply(t, g, now, "p1", AskJudge{Category: 0})

	events := g.JudgeFailed(1, 0, JudgeReasonBusy)
	if len(events) != 1 || events[0].Type != EventJudgeFailed || events[0].Reason != JudgeReasonBusy {
		t.Fatalf("events = %v", events)
	}
	if len(g.Round.JudgePending) != 0 || len(g.Round.Verdicts) != 0 {
		t.Errorf("a failure leaves no trace, got %+v", g.Round)
	}
	if events := g.JudgeFailed(1, 0, JudgeReasonError); len(events) != 0 {
		t.Errorf("a question that is not open cannot fail, got %v", events)
	}
	apply(t, g, now, "p1", AskJudge{Category: 0})
}

func TestStaleJudgeResultsAreDropped(t *testing.T) {
	g, now := newReview(t, judgeP1, judgeP2, judgeP3)
	apply(t, g, now, "p1", AskJudge{Category: 0})

	if g.ApplyVerdicts(2, 0, []AIVerdict{{Player: "p1", Verdict: VerdictValid}}) {
		t.Error("a result for another round must be dropped")
	}
	if events := g.JudgeFailed(2, 0, JudgeReasonError); len(events) != 0 {
		t.Error("a failure for another round must be dropped")
	}

	apply(t, g, now, "p1", FinishReview{})
	if g.ApplyVerdicts(1, 0, []AIVerdict{{Player: "p1", Verdict: VerdictValid}}) {
		t.Error("a result after the review must be dropped")
	}
	if len(g.Round.Verdicts) != 0 {
		t.Errorf("verdicts = %+v", g.Round.Verdicts)
	}
	if v := g.View("p1"); len(v.Round.Verdicts) != 0 || len(v.Round.JudgePending) != 0 {
		t.Errorf("verdicts only show during the review, got %+v", v.Round)
	}
}

func TestJudgeCommandIsAWireCommand(t *testing.T) {
	cmd, err := ParseCommand("ask_judge", []byte(`{"category":2}`))
	if err != nil || cmd != (AskJudge{Category: 2}) {
		t.Fatalf("ParseCommand = %#v, %v", cmd, err)
	}
	if got := ErrorCode(ErrJudgePending); got != "judge_pending" {
		t.Errorf("code = %q", got)
	}
	if ErrorCode(ErrAlreadyJudged) != "already_judged" || ErrorCode(ErrNothingToJudge) != "nothing_to_judge" {
		t.Error("error codes are not wired")
	}
}
