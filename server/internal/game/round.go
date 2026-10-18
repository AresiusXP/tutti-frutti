package game

import (
	"slices"
	"strings"
	"time"
	"unicode/utf8"
)

func (g *Game) availableLetters() []string {
	return AvailableLetters(g.Settings.Lang, g.Settings.ExcludeHardLetters, g.UsedLetters)
}

// beginRound starts the next round in the letter phase. If no letters are
// left the game ends instead.
func (g *Game) beginRound(now time.Time) []Event {
	if len(g.availableLetters()) == 0 {
		return g.finish()
	}
	g.RoundNum++
	r := &Round{
		Number:          g.RoundNum,
		LetterStartedAt: now,
		Answers:         map[string][]string{},
		RejectVotes:     []RejectVote{},
		Proposals:       []MergeProposal{},
	}
	for _, p := range g.Players {
		if !p.Left && p.JoinedRound <= g.RoundNum {
			r.Participants = append(r.Participants, p.ID)
			r.Answers[p.ID] = make([]string, len(g.Settings.Categories))
		}
	}
	g.Round = r
	g.Phase = PhaseLetter
	g.pickChooser()
	return []Event{{Type: EventRoundStarted, Round: g.RoundNum, PlayerID: g.ChooserID}}
}

// pickChooser sets the chooser: the host in round 1, then the next
// participant in join order after the previous chooser (wrapping around, and
// skipping players who left, even if the previous chooser is gone).
func (g *Game) pickChooser() {
	var first, next *Player
	for i := range g.Players {
		p := &g.Players[i]
		if !g.isParticipant(p.ID) {
			continue
		}
		if first == nil {
			first = p
		}
		if next == nil && p.JoinedSeq > g.ChooserSeq {
			next = p
		}
	}

	chosen := next
	switch {
	case g.RoundNum == 1 && g.seat(g.HostID) != nil && g.isParticipant(g.HostID):
		chosen = g.seat(g.HostID)
	case chosen == nil:
		chosen = first
	}
	g.ChooserID, g.ChooserSeq = "", 0
	if chosen != nil {
		g.ChooserID, g.ChooserSeq = chosen.ID, chosen.JoinedSeq
	}
}

func (g *Game) stopLetter(now time.Time, p *Player) ([]Event, error) {
	if g.Phase != PhaseLetter {
		return nil, ErrWrongPhase
	}
	if p.ID != g.ChooserID {
		return nil, ErrNotYourTurn
	}
	letter := LetterAt(g.availableLetters(), now.Sub(g.Round.LetterStartedAt))
	return g.revealLetter(now, letter), nil
}

// revealLetter fixes the round's letter and starts the playing phase at the
// instant at (the time limit counts from there).
func (g *Game) revealLetter(at time.Time, letter string) []Event {
	g.Round.Letter = letter
	g.UsedLetters = append(g.UsedLetters, letter)
	g.Phase = PhasePlaying
	if g.Settings.TimeLimitSeconds > 0 {
		g.Round.PlayingEndsAt = at.Add(time.Duration(g.Settings.TimeLimitSeconds) * time.Second)
	}
	return []Event{{Type: EventLetterRevealed, Letter: letter, Round: g.RoundNum}}
}

func (g *Game) setAnswer(p *Player, c SetAnswer) error {
	if g.Phase != PhasePlaying && g.Phase != PhaseGrace {
		return ErrWrongPhase
	}
	if !g.isParticipant(p.ID) {
		return ErrNotParticipant
	}
	if c.Category < 0 || c.Category >= len(g.Settings.Categories) {
		return ErrInvalidCategory
	}
	text := strings.TrimSpace(stripFormat(c.Text))
	if utf8.RuneCountInString(text) > MaxAnswerLen {
		return ErrAnswerTooLong
	}
	g.Round.Answers[p.ID][c.Category] = text
	return nil
}

func (g *Game) basta(now time.Time, p *Player) ([]Event, error) {
	if g.Phase != PhasePlaying {
		return nil, ErrWrongPhase
	}
	if !g.isParticipant(p.ID) {
		return nil, ErrNotParticipant
	}
	if !g.allAnswered(p.ID) {
		return nil, ErrBastaNotAllowed
	}
	g.Phase = PhaseGrace
	g.Round.CallerID = p.ID
	g.Round.GraceEndsAt = now.Add(GraceDuration)
	return []Event{{Type: EventBasta, PlayerID: p.ID, Name: p.Name}}, nil
}

// allAnswered reports whether every category of the player is non-empty and
// starts with the round's letter.
func (g *Game) allAnswered(id string) bool {
	answers := g.Round.Answers[id]
	if len(answers) != len(g.Settings.Categories) {
		return false
	}
	return !slices.ContainsFunc(answers, func(a string) bool {
		return !StartsWithLetter(a, g.Round.Letter, g.Settings.Lang)
	})
}
