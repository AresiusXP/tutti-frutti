package game

import "strings"

// Points awarded per category.
const (
	// PointsUnique is awarded to a valid answer nobody else has.
	PointsUnique = 10
	// PointsShared is awarded to a valid answer shared with at least one other.
	PointsShared = 5
)

// Status is the verdict on one answer.
type Status string

// Answer statuses.
const (
	StatusEmpty       Status = "empty"
	StatusWrongLetter Status = "wrong_letter"
	StatusRejected    Status = "rejected"
	StatusValid       Status = "valid"
)

// AnswerKey identifies one answer: a player's answer in a category.
type AnswerKey struct {
	Player   string `json:"player"`
	Category int    `json:"category"`
}

// MergeGroup is a set of players whose answers in one category are
// considered the same. Groups of the same category that share a player are
// united transitively.
type MergeGroup struct {
	Category int      `json:"category"`
	Players  []string `json:"players"`
}

// RoundInput is everything ScoreRound needs.
type RoundInput struct {
	Lang   Lang
	Letter string
	// Categories is the number of categories (answers are index-aligned).
	Categories int
	// Participants are the players that took part, in display order.
	Participants []string
	// Answers holds each participant's raw answer per category.
	Answers map[string][]string
	// Rejected lists answers voted out in the review.
	Rejected []AnswerKey
	// Merges lists the answers the table agreed are the same.
	Merges []MergeGroup
}

// Cell is the scored answer of one player in one category.
type Cell struct {
	// Raw is the answer as typed (trimmed).
	Raw string `json:"raw"`
	// Answer is the normalized answer used for matching.
	Answer string `json:"answer"`
	Status Status `json:"status"`
	// Points is 0, PointsShared or PointsUnique.
	Points int `json:"points"`
	// Shared is true for a valid answer that has at least one twin.
	Shared bool `json:"shared"`
}

// PlayerResult is one player's scored round.
type PlayerResult struct {
	// Cells has one entry per category.
	Cells []Cell `json:"cells"`
	Total int    `json:"total"`
}

// RoundResult is the outcome of scoring a round.
type RoundResult struct {
	Round   int                     `json:"round"`
	Letter  string                  `json:"letter"`
	Players map[string]PlayerResult `json:"players"`
}

// ScoreRound scores a round. It is pure and deterministic.
//
// Per category: empty, wrong-letter and rejected answers score 0 (checked in
// that order). Among the remaining valid answers, two are "the same" when
// their normalized text is equal or when a merge group links them (merges
// are transitive and only link valid answers). A valid answer with no twin
// scores PointsUnique, otherwise PointsShared.
func ScoreRound(in RoundInput) RoundResult {
	players := uniqueStrings(in.Participants)
	rejected := make(map[AnswerKey]bool, len(in.Rejected))
	for _, k := range in.Rejected {
		rejected[k] = true
	}

	res := RoundResult{Letter: in.Letter, Players: make(map[string]PlayerResult, len(players))}
	for _, p := range players {
		res.Players[p] = PlayerResult{Cells: make([]Cell, in.Categories)}
	}

	for c := 0; c < in.Categories; c++ {
		var valid []string
		for _, p := range players {
			cell := scoreCell(in, rejected, p, c)
			res.Players[p].Cells[c] = cell
			if cell.Status == StatusValid {
				valid = append(valid, p)
			}
		}

		answers := make(map[string]string, len(valid))
		for _, p := range valid {
			answers[p] = res.Players[p].Cells[c].Answer
		}
		sets := linkAnswers(valid, answers, in.Merges, c)

		for _, p := range valid {
			cell := &res.Players[p].Cells[c]
			cell.Shared = sets.size(p) > 1
			cell.Points = PointsUnique
			if cell.Shared {
				cell.Points = PointsShared
			}
		}
	}

	for _, p := range players {
		r := res.Players[p]
		for _, cell := range r.Cells {
			r.Total += cell.Points
		}
		res.Players[p] = r
	}
	return res
}

// linkAnswers groups the valid answers of one category that count as "the
// same": equal normalized text, or linked by a merge group of that category.
// answers maps each valid player to the normalized answer.
func linkAnswers(valid []string, answers map[string]string, merges []MergeGroup, category int) *unionFind {
	sets := newUnionFind(valid)
	firstWith := make(map[string]string, len(valid))
	for _, p := range valid {
		if first, ok := firstWith[answers[p]]; ok {
			sets.union(first, p)
		} else {
			firstWith[answers[p]] = p
		}
	}
	for _, g := range merges {
		if g.Category != category {
			continue
		}
		var anchor string
		for _, p := range g.Players {
			if !sets.has(p) {
				continue
			}
			if anchor == "" {
				anchor = p
			} else {
				sets.union(anchor, p)
			}
		}
	}
	return sets
}

func scoreCell(in RoundInput, rejected map[AnswerKey]bool, player string, category int) Cell {
	var cell Cell
	if answers := in.Answers[player]; category < len(answers) {
		cell.Raw = strings.TrimSpace(answers[category])
	}
	cell.Answer = Normalize(cell.Raw, in.Lang)
	switch {
	case cell.Answer == "":
		cell.Status = StatusEmpty
	case !StartsWithLetter(cell.Answer, in.Letter, in.Lang):
		cell.Status = StatusWrongLetter
	case rejected[AnswerKey{Player: player, Category: category}]:
		cell.Status = StatusRejected
	default:
		cell.Status = StatusValid
	}
	return cell
}

func uniqueStrings(in []string) []string {
	seen := make(map[string]bool, len(in))
	out := make([]string, 0, len(in))
	for _, s := range in {
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	return out
}

// unionFind is a tiny disjoint-set over player ids.
type unionFind struct {
	parent map[string]string
}

func newUnionFind(members []string) *unionFind {
	u := &unionFind{parent: make(map[string]string, len(members))}
	for _, m := range members {
		u.parent[m] = m
	}
	return u
}

func (u *unionFind) has(m string) bool {
	_, ok := u.parent[m]
	return ok
}

func (u *unionFind) find(m string) string {
	for u.parent[m] != m {
		u.parent[m] = u.parent[u.parent[m]]
		m = u.parent[m]
	}
	return m
}

func (u *unionFind) union(a, b string) { u.parent[u.find(a)] = u.find(b) }

func (u *unionFind) size(m string) int {
	root, n := u.find(m), 0
	for k := range u.parent {
		if u.find(k) == root {
			n++
		}
	}
	return n
}
