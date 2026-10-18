package game

import (
	"slices"
	"time"
)

// openStopVote returns the open vote to stop, or nil.
func (g *Game) openStopVote() *StopVote {
	if g.Round == nil {
		return nil
	}
	return g.Round.StopVote
}

// stopElectorate returns the voters of v: the seated, connected participants
// other than the asker, in join order (never nil). It is evaluated live, like
// the review votes: someone who disconnects stops counting, someone who
// reconnects counts again (their earlier "yes", if any, is still on record).
func (g *Game) stopElectorate(v *StopVote) []string {
	out := []string{}
	for _, id := range g.Round.Participants {
		if p := g.seat(id); id != v.CallerID && p != nil && p.Connected {
			out = append(out, id)
		}
	}
	return out
}

// nameOf returns the nickname of a player, whether seated or not.
func (g *Game) nameOf(id string) (name string) {
	for _, p := range g.Players {
		if p.ID == id {
			name = p.Name
		}
	}
	return name
}

// askStop opens a vote to stop the round for a participant who has not filled
// every category (one who has calls Basta). With nobody left to ask the vote
// passes at once.
func (g *Game) askStop(now time.Time, p *Player) ([]Event, error) {
	if g.Phase != PhasePlaying {
		return nil, ErrWrongPhase
	}
	if !g.isParticipant(p.ID) {
		return nil, ErrNotParticipant
	}
	if !g.Settings.StopVote {
		return nil, ErrStopVoteDisabled
	}
	if g.Round.StopVote != nil {
		return nil, ErrStopVotePending
	}
	if g.allAnswered(p.ID) {
		return nil, ErrStopNotNeeded
	}
	v := &StopVote{CallerID: p.ID, EndsAt: now.Add(StopVoteDuration), Yes: []string{}}
	g.Round.StopVote = v
	if len(g.stopElectorate(v)) == 0 {
		return g.passStopVote(now), nil
	}
	return []Event{{Type: EventStopAsked, PlayerID: p.ID, Name: p.Name}}, nil
}

// voteStop records a voter's answer to the open vote. A "no" rejects it at
// once (and names nobody); a "yes" counts once and may complete the vote.
func (g *Game) voteStop(now time.Time, p *Player, c VoteStop) ([]Event, error) {
	v := g.openStopVote()
	if v == nil {
		return nil, ErrNoStopVote
	}
	if !g.isParticipant(p.ID) {
		return nil, ErrNotParticipant
	}
	if p.ID == v.CallerID {
		return nil, ErrSelfVote
	}
	if !c.Yes {
		return g.endStopVote(StopReasonDeclined), nil
	}
	if !slices.Contains(v.Yes, p.ID) {
		v.Yes = append(v.Yes, p.ID)
	}
	return g.settleStopVote(now), nil
}

// settleStopVote re-evaluates the open vote after anything that can change
// it (a yes, a connection change, a player leaving). It ends the vote when its
// asker is gone and passes it when every current voter said yes (so also when
// nobody is left to vote). It does nothing when there is no vote.
func (g *Game) settleStopVote(now time.Time) []Event {
	v := g.openStopVote()
	if v == nil {
		return nil
	}
	if asker := g.seat(v.CallerID); asker == nil || !asker.Connected {
		return g.endStopVote(StopReasonEnded)
	}
	for _, id := range g.stopElectorate(v) {
		if !slices.Contains(v.Yes, id) {
			return nil
		}
	}
	return g.passStopVote(now)
}

// endStopVote clears the open vote, if any, and announces that it failed.
func (g *Game) endStopVote(reason string) []Event {
	v := g.openStopVote()
	if v == nil {
		return nil
	}
	g.Round.StopVote = nil
	return []Event{{Type: EventStopRejected, PlayerID: v.CallerID, Name: g.nameOf(v.CallerID), Reason: reason}}
}

// passStopVote turns the open vote into the Basta call of its asker.
func (g *Game) passStopVote(now time.Time) []Event {
	v := g.Round.StopVote
	g.Round.StopVote = nil
	return g.startGrace(now, v.CallerID)
}

// startGrace is the transition of every Basta call, whether the caller had
// every category filled or the others agreed to stop: the grace countdown
// begins.
func (g *Game) startGrace(now time.Time, callerID string) []Event {
	g.Phase = PhaseGrace
	g.Round.CallerID = callerID
	g.Round.GraceEndsAt = now.Add(GraceDuration)
	return []Event{{Type: EventBasta, PlayerID: callerID, Name: g.nameOf(callerID)}}
}

// stopVoteView is the vote as clients see it: the live electorate and the
// yes votes among it, in the order they were cast.
func (g *Game) stopVoteView(v *StopVote) *StopVoteView {
	voters := g.stopElectorate(v)
	out := &StopVoteView{CallerID: v.CallerID, EndsAt: v.EndsAt, Voters: voters, Yes: []string{}}
	for _, id := range v.Yes {
		if slices.Contains(voters, id) {
			out.Yes = append(out.Yes, id)
		}
	}
	return out
}
