package game

import (
	"slices"
	"strings"
	"unicode/utf8"
)

// The AI judge is advisory: it never changes a score. The host asks for the
// answers of one review category to be judged (AskJudge); the engine only
// records that the question is open (Round.JudgePending) and hands the room
// layer the data to ask (JudgeQuery). The engine itself never does I/O: the
// room layer calls the judge on another goroutine and reports back with
// ApplyVerdicts or JudgeFailed, which are trusted calls like SetConnected (a
// client cannot send them).

// Verdicts of the AI judge (AIVerdict.Verdict).
const (
	// VerdictValid: the answer looks like a valid example of the category.
	VerdictValid = "valid"
	// VerdictDoubtful: it does not, or the judge is not sure.
	VerdictDoubtful = "doubtful"
)

// Reasons of EventJudgeFailed (Event.Reason).
const (
	// JudgeReasonBusy: the judge's rate limit or daily budget is used up.
	JudgeReasonBusy = "busy"
	// JudgeReasonError: the judge failed or answered nonsense.
	JudgeReasonError = "error"
)

// MaxVerdictReasonLen is the longest stored AIVerdict.Reason, in runes.
const MaxVerdictReasonLen = 100

// AIVerdict is the AI judge's opinion on one answer. It is shown to everybody
// as a suggestion and has no effect on the score.
type AIVerdict struct {
	Category int    `json:"category"`
	Player   string `json:"player"`
	// Verdict is VerdictValid or VerdictDoubtful.
	Verdict string `json:"verdict"`
	// Reason is a short explanation (at most MaxVerdictReasonLen runes).
	Reason string `json:"reason,omitempty"`
}

// AskJudge asks the AI judge to judge the answers of a review category (host
// only, review phase, once per category unless the attempt failed).
type AskJudge struct {
	Category int `json:"category"`
}

// Type implements Command.
func (AskJudge) Type() string { return CmdAskJudge }

// JudgeAnswer is one answer to judge.
type JudgeAnswer struct {
	Player string
	Text   string
}

// JudgeQuery is what the room layer sends to the judge for one category.
type JudgeQuery struct {
	// Round and Category identify the question; the result is dropped when the
	// round moved on.
	Round    int
	Category int
	Lang     Lang
	// Label is the category label in the lobby language.
	Label  string
	Letter string
	// Answers are the non-empty answers that start with the letter.
	Answers []JudgeAnswer
}

func (g *Game) judging(category int) bool {
	return g.Round != nil && slices.Contains(g.Round.JudgePending, category)
}

func (g *Game) judged(category int) bool {
	return g.Round != nil && slices.ContainsFunc(g.Round.Verdicts, func(v AIVerdict) bool { return v.Category == category })
}

// JudgeQuery returns the question for the judge about a category of the
// current round: the answers that are worth judging.
func (g *Game) JudgeQuery(category int) JudgeQuery {
	q := JudgeQuery{Round: g.RoundNum, Category: category, Lang: g.Settings.Lang}
	if g.Round == nil || !g.validCategory(category) {
		return q
	}
	q.Letter = g.Round.Letter
	q.Label = g.Settings.Labels()[category]
	for _, id := range g.Round.Participants {
		answers := g.Round.Answers[id]
		if category >= len(answers) {
			continue
		}
		text := answers[category]
		if Normalize(text, g.Settings.Lang) == "" || !StartsWithLetter(text, g.Round.Letter, g.Settings.Lang) {
			continue
		}
		q.Answers = append(q.Answers, JudgeAnswer{Player: id, Text: text})
	}
	return q
}

func (g *Game) askJudge(host *Player, c AskJudge) error {
	if err := g.requireHost(host); err != nil {
		return err
	}
	if g.Phase != PhaseReview {
		return ErrWrongPhase
	}
	if !g.validCategory(c.Category) {
		return ErrInvalidCategory
	}
	if g.judging(c.Category) {
		return ErrJudgePending
	}
	if g.judged(c.Category) {
		return ErrAlreadyJudged
	}
	if len(g.JudgeQuery(c.Category).Answers) == 0 {
		return ErrNothingToJudge
	}
	g.Round.JudgePending = append(g.Round.JudgePending, c.Category)
	return nil
}

// ApplyVerdicts stores the judge's verdicts for a category of round. It is a
// trusted call from the room layer. It returns false (and stores nothing) when
// the question is no longer open: the round moved on, or the review ended.
// Verdicts about players or answers that were not asked about are ignored, and
// reasons are cleaned and shortened.
func (g *Game) ApplyVerdicts(round, category int, verdicts []AIVerdict) bool {
	if !g.closeJudge(round, category) {
		return false
	}
	asked := map[string]bool{}
	for _, a := range g.JudgeQuery(category).Answers {
		asked[a.Player] = true
	}
	for _, v := range verdicts {
		if !asked[v.Player] || (v.Verdict != VerdictValid && v.Verdict != VerdictDoubtful) {
			continue
		}
		delete(asked, v.Player) // one verdict per answer
		g.Round.Verdicts = append(g.Round.Verdicts, AIVerdict{
			Category: category,
			Player:   v.Player,
			Verdict:  v.Verdict,
			Reason:   cleanReason(v.Reason),
		})
	}
	return true
}

// JudgeFailed closes an open question without verdicts (the host can ask
// again). It is a trusted call from the room layer; reason is JudgeReasonBusy
// or JudgeReasonError. It returns no events when the question is no longer
// open.
func (g *Game) JudgeFailed(round, category int, reason string) []Event {
	if !g.closeJudge(round, category) {
		return nil
	}
	return []Event{{Type: EventJudgeFailed, Round: round, Reason: reason}}
}

// closeJudge removes an open question; it reports whether it was open.
func (g *Game) closeJudge(round, category int) bool {
	if g.Round == nil || g.Phase != PhaseReview || g.RoundNum != round || !g.judging(category) {
		return false
	}
	g.Round.JudgePending = slices.DeleteFunc(g.Round.JudgePending, func(c int) bool { return c == category })
	return true
}

// cleanReason makes the judge's explanation safe to show: invisible and
// control characters are removed, whitespace collapsed, and it is cut to
// MaxVerdictReasonLen runes.
func cleanReason(s string) string {
	s = strings.Join(strings.Fields(stripFormat(s)), " ")
	if utf8.RuneCountInString(s) > MaxVerdictReasonLen {
		s = string([]rune(s)[:MaxVerdictReasonLen])
	}
	return strings.TrimSpace(s)
}
