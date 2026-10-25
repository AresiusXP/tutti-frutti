package game

import (
	"slices"
	"time"
)

// View is the snapshot of the game sent to one viewer. It is built by
// Game.View and shares no memory with the game.
//
// Visibility rules:
//   - the letter is only present once the letter phase is over;
//   - while the phase is playing or grace, Round.Answers only contains the
//     viewer's own answers and Round.Filled tells how many categories every
//     participant has filled in;
//   - from the review on, everyone sees every answer and the live result.
type View struct {
	Phase      Phase  `json:"phase"`
	YouID      string `json:"youId"`
	HostID     string `json:"hostId"`
	MaxPlayers int    `json:"maxPlayers"`
	// Players are all seats in join order, including those who left (Left).
	Players  []Player `json:"players"`
	Settings Settings `json:"settings"`
	// CategoryLabels are the labels of Settings.Categories in the lobby
	// language.
	CategoryLabels []string `json:"categoryLabels"`
	UsedLetters    []string `json:"usedLetters"`
	RoundNum       int      `json:"roundNum"`
	ChooserID      string   `json:"chooserId,omitempty"`
	// Round is nil in the lobby.
	Round   *RoundView    `json:"round,omitempty"`
	History []RoundResult `json:"history"`
	Winners []string      `json:"winners,omitempty"`
}

// RoundView is the filtered state of the current round.
type RoundView struct {
	Number int `json:"number"`
	// Letter is empty during the letter phase.
	Letter       string   `json:"letter"`
	Participants []string `json:"participants"`
	// Answers: own answers only in playing/grace, everybody's afterwards.
	Answers map[string][]string `json:"answers"`
	// Filled counts the non-empty answers of every participant.
	Filled   map[string]int `json:"filled"`
	CallerID string         `json:"callerId,omitempty"`
	// TimeLimitEndsAt is the round deadline when a time limit is set.
	TimeLimitEndsAt time.Time `json:"timeLimitEndsAt,omitzero"`
	// GraceEndsAt is when inputs lock (basta or time-up); updates are still
	// accepted for LateUpdateAllowance afterwards.
	GraceEndsAt    time.Time `json:"graceEndsAt,omitzero"`
	ReviewCategory int       `json:"reviewCategory"`
	// StopVote is the open vote to stop (playing phase only).
	StopVote *StopVoteView `json:"stopVote,omitempty"`
	// RejectVotes and Proposals are only filled during the review.
	RejectVotes []RejectVote   `json:"rejectVotes"`
	Proposals   []ProposalView `json:"proposals"`
	// JudgePending are the categories the AI judge is working on and Verdicts
	// its advisory opinions; both are only filled during the review.
	JudgePending []int       `json:"judgePending"`
	Verdicts     []AIVerdict `json:"verdicts"`
	// Result is the live result in the review and the final one in the round
	// summary and final phases; nil before the review.
	Result *RoundResult `json:"result,omitempty"`
}

// StopVoteView is the open vote to stop with its live electorate.
type StopVoteView struct {
	CallerID string    `json:"callerId"`
	EndsAt   time.Time `json:"endsAt"`
	// Voters are the connected participants other than the asker right now.
	Voters []string `json:"voters"`
	// Yes are the voters that already said yes.
	Yes []string `json:"yes"`
}

// ProposalView is a merge proposal with its live tally.
type ProposalView struct {
	// MergeProposal is flattened into the JSON object (`tstype` makes the
	// generated TypeScript interface extend it instead of nesting it).
	MergeProposal `tstype:",extends"`
	// Yes counts the yes votes of connected participants.
	Yes int `json:"yes"`
	// Passed is true while the proposal has a majority.
	Passed bool `json:"passed"`
}

// Clone returns a deep copy of the result.
func (r RoundResult) Clone() RoundResult {
	out := r
	out.Players = make(map[string]PlayerResult, len(r.Players))
	for id, pr := range r.Players {
		pr.Cells = slices.Clone(pr.Cells)
		out.Players[id] = pr
	}
	return out
}

// View returns the snapshot for viewer (a player id; unknown ids get the
// spectator view).
func (g *Game) View(viewer string) View {
	v := View{
		Phase:          g.Phase,
		YouID:          viewer,
		HostID:         g.HostID,
		MaxPlayers:     MaxPlayers,
		Players:        slices.Clone(g.Players),
		Settings:       g.Settings.Clean(),
		CategoryLabels: g.Settings.Labels(),
		UsedLetters:    slices.Clone(g.UsedLetters),
		RoundNum:       g.RoundNum,
		ChooserID:      g.ChooserID,
		History:        make([]RoundResult, len(g.History)),
		Winners:        slices.Clone(g.Winners),
	}
	if v.Players == nil {
		v.Players = []Player{}
	}
	if v.UsedLetters == nil {
		v.UsedLetters = []string{}
	}
	for i, r := range g.History {
		v.History[i] = r.Clone()
	}
	if g.Round != nil {
		rv := g.roundView(viewer)
		v.Round = &rv
	}
	return v
}

func (g *Game) roundView(viewer string) RoundView {
	r := g.Round
	rv := RoundView{
		Number:          r.Number,
		Letter:          r.Letter,
		Participants:    slices.Clone(r.Participants),
		Answers:         map[string][]string{},
		Filled:          map[string]int{},
		CallerID:        r.CallerID,
		TimeLimitEndsAt: r.PlayingEndsAt,
		GraceEndsAt:     r.GraceEndsAt,
		ReviewCategory:  r.ReviewCategory,
		RejectVotes:     []RejectVote{},
		Proposals:       []ProposalView{},
		JudgePending:    []int{},
		Verdicts:        []AIVerdict{},
	}
	if rv.Participants == nil {
		rv.Participants = []string{}
	}

	hidden := g.Phase == PhasePlaying || g.Phase == PhaseGrace
	for id, answers := range r.Answers {
		for _, a := range answers {
			if a != "" {
				rv.Filled[id]++
			}
		}
		if !hidden || id == viewer {
			rv.Answers[id] = slices.Clone(answers)
		}
	}

	if v := r.StopVote; v != nil && g.Phase == PhasePlaying {
		rv.StopVote = g.stopVoteView(v)
	}

	switch g.Phase {
	case PhaseReview:
		res := g.score().Clone()
		rv.Result = &res
	case PhaseRoundSummary, PhaseFinal:
		if n := len(g.History); n > 0 && g.History[n-1].Round == g.RoundNum {
			res := g.History[n-1].Clone()
			rv.Result = &res
		}
	}
	if g.Phase == PhaseReview {
		voters := g.connectedParticipants()
		rv.RejectVotes = slices.Clone(r.RejectVotes)
		rv.JudgePending = append(rv.JudgePending, r.JudgePending...)
		rv.Verdicts = append(rv.Verdicts, r.Verdicts...)
		for _, pr := range r.Proposals {
			pr.Votes = slices.Clone(pr.Votes)
			rv.Proposals = append(rv.Proposals, ProposalView{
				MergeProposal: pr,
				Yes:           yesVotes(pr, voters),
				Passed:        passed(pr, voters),
			})
		}
	}
	return rv
}
