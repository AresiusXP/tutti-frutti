package game

import (
	"slices"
	"time"
)

// connectedParticipants returns the participants who are seated and
// connected: the electorate of the review votes.
func (g *Game) connectedParticipants() map[string]bool {
	out := map[string]bool{}
	for _, id := range g.Round.Participants {
		if p := g.seat(id); p != nil && p.Connected {
			out[id] = true
		}
	}
	return out
}

// rejectedAnswers returns the answers whose reject votes pass: strictly more
// than half of the connected participants other than the author voted yes.
// Votes from players who are not eligible are ignored.
func (g *Game) rejectedAnswers() []AnswerKey {
	voters := g.connectedParticipants()
	yes := map[AnswerKey]int{}
	for _, v := range g.Round.RejectVotes {
		if voters[v.Voter] && v.Voter != v.Target {
			yes[AnswerKey{Player: v.Target, Category: v.Category}]++
		}
	}
	var out []AnswerKey
	for key, n := range yes {
		eligible := len(voters)
		if voters[key.Player] {
			eligible--
		}
		if n*2 > eligible {
			out = append(out, key)
		}
	}
	return out
}

// passed reports whether a merge proposal has a majority of yes votes among
// the connected participants (voters).
func passed(pr MergeProposal, voters map[string]bool) bool {
	return yesVotes(pr, voters)*2 > len(voters)
}

func yesVotes(pr MergeProposal, voters map[string]bool) int {
	n := 0
	for _, v := range pr.Votes {
		if v.Yes && voters[v.Voter] {
			n++
		}
	}
	return n
}

// mergeGroups turns the passed proposals into merge groups for scoring.
func (g *Game) mergeGroups() []MergeGroup {
	voters := g.connectedParticipants()
	var out []MergeGroup
	for _, pr := range g.Round.Proposals {
		if passed(pr, voters) {
			out = append(out, MergeGroup{Category: pr.Category, Players: []string{pr.PlayerA, pr.PlayerB}})
		}
	}
	return out
}

// score computes the live result of the current round from the answers, the
// reject votes and the passed merge proposals.
func (g *Game) score() RoundResult {
	res := ScoreRound(RoundInput{
		Lang:         g.Settings.Lang,
		Letter:       g.Round.Letter,
		Categories:   len(g.Settings.Categories),
		Participants: g.Round.Participants,
		Answers:      g.Round.Answers,
		Rejected:     g.rejectedAnswers(),
		Merges:       g.mergeGroups(),
	})
	res.Round = g.RoundNum
	return res
}

func (g *Game) reviewPlayer(p *Player) error {
	if g.Phase != PhaseReview {
		return ErrWrongPhase
	}
	if !g.isParticipant(p.ID) {
		return ErrNotParticipant
	}
	return nil
}

func (g *Game) validCategory(c int) bool { return c >= 0 && c < len(g.Settings.Categories) }

func (g *Game) reviewNav(host *Player, c ReviewNav) error {
	if err := g.requireHost(host); err != nil {
		return err
	}
	if g.Phase != PhaseReview {
		return ErrWrongPhase
	}
	if !g.validCategory(c.Category) {
		return ErrInvalidCategory
	}
	g.Round.ReviewCategory = c.Category
	return nil
}

func (g *Game) voteReject(p *Player, c VoteReject) error {
	if err := g.reviewPlayer(p); err != nil {
		return err
	}
	if !g.validCategory(c.Category) {
		return ErrInvalidCategory
	}
	if !g.isParticipant(c.Target) {
		return ErrUnknownPlayer
	}
	if c.Target == p.ID {
		return ErrSelfVote
	}
	if Normalize(g.Round.Answers[c.Target][c.Category], g.Settings.Lang) == "" {
		return ErrEmptyAnswer
	}

	vote := RejectVote{Category: c.Category, Target: c.Target, Voter: p.ID}
	if c.Withdraw {
		g.Round.RejectVotes = slices.DeleteFunc(g.Round.RejectVotes, func(v RejectVote) bool { return v == vote })
	} else if !slices.Contains(g.Round.RejectVotes, vote) {
		g.Round.RejectVotes = append(g.Round.RejectVotes, vote)
	}
	return nil
}

func (g *Game) proposeMerge(p *Player, c ProposeMerge) error {
	if err := g.reviewPlayer(p); err != nil {
		return err
	}
	if !g.validCategory(c.Category) {
		return ErrInvalidCategory
	}
	if !g.isParticipant(c.PlayerA) || !g.isParticipant(c.PlayerB) {
		return ErrUnknownPlayer
	}
	if c.PlayerA == c.PlayerB {
		return ErrInvalidMerge
	}

	res := g.score()
	var valid []string
	answers := map[string]string{}
	for _, id := range g.Round.Participants {
		if cell := res.Players[id].Cells[c.Category]; cell.Status == StatusValid {
			valid = append(valid, id)
			answers[id] = cell.Answer
		}
	}
	if !slices.Contains(valid, c.PlayerA) || !slices.Contains(valid, c.PlayerB) {
		return ErrInvalidMerge
	}
	sets := linkAnswers(valid, answers, g.mergeGroups(), c.Category)
	if sets.find(c.PlayerA) == sets.find(c.PlayerB) {
		return ErrAlreadySame
	}
	voters := g.connectedParticipants()
	for _, pr := range g.Round.Proposals {
		if pr.Category == c.Category && samePair(pr, c.PlayerA, c.PlayerB) && !passed(pr, voters) {
			return ErrProposalExists
		}
	}

	g.Round.Proposals = append(g.Round.Proposals, MergeProposal{
		ID:         g.NextProposalID,
		Category:   c.Category,
		PlayerA:    c.PlayerA,
		PlayerB:    c.PlayerB,
		ProposedBy: p.ID,
		Votes:      []MergeVote{{Voter: p.ID, Yes: true}},
	})
	g.NextProposalID++
	return nil
}

func samePair(pr MergeProposal, a, b string) bool {
	return pr.PlayerA == a && pr.PlayerB == b || pr.PlayerA == b && pr.PlayerB == a
}

func (g *Game) voteMerge(p *Player, c VoteMerge) error {
	if err := g.reviewPlayer(p); err != nil {
		return err
	}
	for i := range g.Round.Proposals {
		pr := &g.Round.Proposals[i]
		if pr.ID != c.ProposalID {
			continue
		}
		for j := range pr.Votes {
			if pr.Votes[j].Voter == p.ID {
				pr.Votes[j].Yes = c.Yes
				return nil
			}
		}
		pr.Votes = append(pr.Votes, MergeVote{Voter: p.ID, Yes: c.Yes})
		return nil
	}
	return ErrUnknownProposal
}

// finishReview scores the round, adds the round totals to the running totals
// and moves to the round summary.
func (g *Game) finishReview(host *Player) ([]Event, error) {
	if err := g.requireHost(host); err != nil {
		return nil, err
	}
	if g.Phase != PhaseReview {
		return nil, ErrWrongPhase
	}
	res := g.score()
	for id, pr := range res.Players {
		for i := range g.Players {
			if g.Players[i].ID == id {
				g.Players[i].TotalScore += pr.Total
			}
		}
	}
	g.History = append(g.History, res)
	g.Phase = PhaseRoundSummary
	return []Event{{Type: EventRoundScored, Round: g.RoundNum}}, nil
}

func (g *Game) nextRound(now time.Time, host *Player) ([]Event, error) {
	if err := g.requireHost(host); err != nil {
		return nil, err
	}
	if g.Phase != PhaseRoundSummary {
		return nil, ErrWrongPhase
	}
	if g.Settings.Rounds > 0 && g.RoundNum >= g.Settings.Rounds {
		return g.finish(), nil
	}
	return g.beginRound(now), nil
}

func (g *Game) endGame(host *Player) ([]Event, error) {
	if err := g.requireHost(host); err != nil {
		return nil, err
	}
	if g.Phase != PhaseRoundSummary {
		return nil, ErrWrongPhase
	}
	return g.finish(), nil
}

// finish ends the game and computes the winners: the seated players with the
// highest total (several on a tie).
func (g *Game) finish() []Event {
	g.Phase = PhaseFinal
	g.Winners = nil
	best := -1
	for _, p := range g.Players {
		if p.Left {
			continue
		}
		switch {
		case p.TotalScore > best:
			best = p.TotalScore
			g.Winners = []string{p.ID}
		case p.TotalScore == best:
			g.Winners = append(g.Winners, p.ID)
		}
	}
	return []Event{{Type: EventGameOver, Winners: slices.Clone(g.Winners)}}
}

// rematch returns to the lobby with the same players (those who have not
// left) and the same settings, with scores, letters and history reset.
func (g *Game) rematch(host *Player) ([]Event, error) {
	if err := g.requireHost(host); err != nil {
		return nil, err
	}
	if g.Phase != PhaseFinal {
		return nil, ErrWrongPhase
	}
	g.Players = slices.DeleteFunc(g.Players, func(p Player) bool { return p.Left })
	for i := range g.Players {
		g.Players[i].TotalScore = 0
		g.Players[i].JoinedRound = 1
	}
	g.Phase = PhaseLobby
	g.UsedLetters = []string{}
	g.History = []RoundResult{}
	g.Winners = nil
	g.RoundNum = 0
	g.ChooserID, g.ChooserSeq = "", 0
	g.Round = nil
	return []Event{{Type: EventRematch}}, nil
}
