package room

import (
	"context"
	"slices"
	"time"

	"github.com/AresiusXP/tutti-frutti/server/internal/game"
	"github.com/AresiusXP/tutti-frutti/server/internal/store"
)

// Recorder persists the results of finished games per signed-in user (the
// store's Accounts.RecordGame). The hub hands records to it from one goroutine,
// retrying failures, and never waits for it on a room's goroutine.
type Recorder interface {
	RecordGame(ctx context.Context, rec store.GameRecord) error
}

// buildRecord computes what a finished game adds to the statistics of the
// users linked to its players, or reports false when it adds nothing:
//
//   - a game only counts with at least two participants (players that appear
//     in a scored round) and one scored round;
//   - only players linked to a user (users: player id -> user id) who took part
//     in at least one scored round get a result; a player that left still
//     counts for the rounds they played, but only g.Winners win;
//   - Points, BestRound and UniqueAnswers are computed from g.History (Points
//     equals the player's final score);
//   - when two players are linked to the same user (two devices) the first
//     seat counts.
func buildRecord(g *game.Game, users map[string]string, gameID string, at time.Time) (store.GameRecord, bool) {
	type tally struct{ points, best, unique int }
	played := map[string]*tally{}
	for _, round := range g.History {
		for id, pr := range round.Players {
			t := played[id]
			if t == nil {
				t = &tally{}
				played[id] = t
			}
			t.points += pr.Total
			t.best = max(t.best, pr.Total)
			for _, cell := range pr.Cells {
				if cell.Points == game.PointsUnique {
					t.unique++
				}
			}
		}
	}
	if len(played) < 2 {
		return store.GameRecord{}, false
	}

	rec := store.GameRecord{ID: gameID, PlayedAt: at}
	seen := map[string]bool{}
	for _, p := range g.Players {
		userID, t := users[p.ID], played[p.ID]
		if userID == "" || t == nil || seen[userID] {
			continue
		}
		seen[userID] = true
		rec.Results = append(rec.Results, store.UserResult{
			UserID:        userID,
			Won:           slices.Contains(g.Winners, p.ID),
			Points:        t.points,
			BestRound:     t.best,
			UniqueAnswers: t.unique,
		})
	}
	return rec, len(rec.Results) > 0
}
