package room

import (
	"context"
	"errors"
	"runtime/debug"
	"slices"

	"github.com/AresiusXP/tutti-frutti/server/internal/catalog"
	"github.com/AresiusXP/tutti-frutti/server/internal/game"
	"github.com/AresiusXP/tutti-frutti/server/internal/safety"
)

// Errors of ReportEvidence. A report whose evidence cannot be produced is still
// accepted by the transport, as unverified (the room instance is gone, the
// content changed or was never visible to the reporter), except ErrNotMember.
var (
	// ErrInstanceMismatch: the room that has the code now is not the instance the
	// reporter was in (the code was reused after the room closed).
	ErrInstanceMismatch = errors.New("room: that room instance is gone")
	// ErrNotMember: the room exists but the reporter is not, and was not, one of
	// its players: it has nothing to corroborate and must not learn about it.
	ErrNotMember = errors.New("room: the reporter is not a player of this room")
	// ErrContentUnavailable: the reported content does not exist (any more) in the
	// reporter's view: it changed, was never published to them (a hidden answer,
	// an AI verdict that is not shown) or the reference is stale.
	ErrContentUnavailable = errors.New("room: the content is not available to the reporter")
)

// EvidenceSource is optional for transports, like PublicRoomLister: it is what
// POST /api/reports needs from the hub. *Hub implements it.
type EvidenceSource interface {
	// ReportEvidence resolves a report through the room's own goroutine and
	// returns the evidence built from the reporter's authorized view. The errors
	// are ErrRoomNotFound, ErrRoomClosed, ErrShuttingDown, ErrInstanceMismatch,
	// ErrNotMember, ErrContentUnavailable, or ctx's error.
	ReportEvidence(ctx context.Context, code string, req safety.EvidenceRequest) (safety.Evidence, error)
}

var _ EvidenceSource = (*Hub)(nil)

type evidenceResult struct {
	ev  safety.Evidence
	err error
}

// ReportEvidence implements EvidenceSource. HTTP handlers never read actor
// state: the lookup is a mailbox task, ordered after everything posted before
// it, and the answer comes back over a channel.
func (h *Hub) ReportEvidence(ctx context.Context, code string, req safety.EvidenceRequest) (safety.Evidence, error) {
	h.mu.Lock()
	r := h.rooms[normalizeCode(code)]
	shuttingDown := h.shuttingDown
	h.mu.Unlock()
	switch {
	case shuttingDown:
		return safety.Evidence{}, ErrShuttingDown
	case r == nil:
		return safety.Evidence{}, ErrRoomNotFound
	}
	ch := make(chan evidenceResult, 1) // buffered: a reply after ctx ended must not block the actor
	r.post(func(r *room) { ch <- r.safeEvidence(req) }, func() { ch <- evidenceResult{err: ErrRoomClosed} })
	select {
	case res := <-ch:
		return res.ev, res.err
	case <-ctx.Done():
		return safety.Evidence{}, ctx.Err()
	}
}

// safeEvidence is evidence with a recover: building evidence never changes the
// room, so a bug in it must not close the room (runTask would) over a report.
func (r *room) safeEvidence(req safety.EvidenceRequest) (res evidenceResult) {
	defer func() {
		if p := recover(); p != nil {
			r.log.Error("building report evidence panicked", "panic", p, "stack", string(debug.Stack()))
			res = evidenceResult{err: ErrContentUnavailable}
		}
	}()
	ev, err := r.evidence(req)
	return evidenceResult{ev, err}
}

// wasMember reports whether id is, or was in this match, a player of the room:
// it has a seat (a seat that left in a started game stays until the rematch) or
// was kicked. Unknown ids are not members, whatever Game.View would answer for
// them.
func (r *room) wasMember(id string) bool {
	if id == "" || r.g.IsAutomated(id) {
		return false
	}
	return slices.ContainsFunc(r.g.Players, func(p game.Player) bool { return p.ID == id }) || slices.Contains(r.g.Banned, id)
}

// evidence builds the evidence of a report, on the room's goroutine. Everything
// in it comes from the reporter's own View (never Game state directly), so a
// player cannot learn, by reporting, what the game hides from them: another
// player's answer while the round is played, an AI verdict that is not shown.
// The client's reference is only used to find the content; the text, the
// author and the ids in the result are the room's.
func (r *room) evidence(req safety.EvidenceRequest) (safety.Evidence, error) {
	if r.stopping {
		return safety.Evidence{}, ErrShuttingDown
	}
	if req.RoomInstanceID != "" && req.RoomInstanceID != r.instanceID {
		return safety.Evidence{}, ErrInstanceMismatch
	}
	if !r.wasMember(req.Reporter) {
		return safety.Evidence{}, ErrNotMember
	}
	v := r.g.View(req.Reporter)
	ev := safety.Evidence{
		Verified:      true,
		RoomInstance:  r.instanceID,
		MatchInstance: r.matchID,
		Data: safety.EvidenceData{
			Source: safety.SourceServer,
			Lang:   string(v.Settings.Lang),
			Phase:  string(v.Phase),
		},
	}
	ref := req.Ref
	category := func() (int, bool) {
		if ref.Category == nil || *ref.Category < 0 || *ref.Category >= len(v.Settings.Categories) {
			return 0, false
		}
		return *ref.Category, true
	}
	describe := func(c int) *safety.EvidenceCategory {
		return &safety.EvidenceCategory{Index: c, Label: v.CategoryLabels[c], Custom: v.Settings.Categories[c].Custom != ""}
	}
	player := func(id string) (game.Player, bool) {
		i := slices.IndexFunc(v.Players, func(p game.Player) bool { return p.ID == id })
		if i < 0 {
			return game.Player{}, false
		}
		return v.Players[i], true
	}

	switch req.Kind {
	case safety.KindPlayer:
		p, ok := player(ref.PlayerID)
		if !ok {
			return safety.Evidence{}, ErrContentUnavailable
		}
		ev.Data.Player = presentation(p)
		r.attribute(&ev, p.ID)

	case safety.KindCustomLabel:
		c, ok := category()
		if !ok || v.Settings.Categories[c].Custom == "" || v.Settings.Categories[c].Custom != game.CleanText(ref.Label) {
			return safety.Evidence{}, ErrContentUnavailable
		}
		label := v.Settings.Categories[c].Custom
		ref.Label = label
		ev.Data.Text, ev.Data.Category = label, describe(c)
		if a, known := r.labelAuthors[label]; known {
			ev.TargetGuest, ev.TargetUser = a.Guest, a.User
			ev.Data.Author = &a
		}

	case safety.KindAnswer:
		c, ok := category()
		p, found := player(ref.PlayerID)
		if !ok || !found || ref.Round == nil {
			return safety.Evidence{}, ErrContentUnavailable
		}
		text, letter, visible := visibleAnswer(v, *ref.Round, c, p.ID)
		if !visible || text == "" {
			return safety.Evidence{}, ErrContentUnavailable
		}
		ev.Data.Round, ev.Data.Letter, ev.Data.Text = *ref.Round, letter, text
		ev.Data.Category, ev.Data.Player = describe(c), presentation(p)
		r.attribute(&ev, p.ID)

	case safety.KindAIVerdict:
		c, ok := category()
		if !ok || ref.Round == nil || v.Round == nil || v.Round.Number != *ref.Round {
			return safety.Evidence{}, ErrContentUnavailable
		}
		i := slices.IndexFunc(v.Round.Verdicts, func(a game.AIVerdict) bool { return a.Category == c && a.Player == ref.PlayerID })
		if i < 0 {
			return safety.Evidence{}, ErrContentUnavailable
		}
		verdict := v.Round.Verdicts[i]
		answer, _, _ := visibleAnswer(v, v.Round.Number, c, ref.PlayerID)
		ev.Data.Round, ev.Data.Letter, ev.Data.Category = v.Round.Number, v.Round.Letter, describe(c)
		ev.Data.Verdict = &safety.EvidenceVerdict{Value: verdict.Verdict, Reason: verdict.Reason, Answer: answer}
		// The subject is the AI judge, not a player: no target.

	default:
		return safety.Evidence{}, ErrContentUnavailable
	}
	ev.ContentKey = safety.ContentKey(req.Kind, ref)
	return ev, nil
}

// presentation is a player's nickname and avatar as the reporter sees them.
func presentation(p game.Player) *safety.EvidencePlayer {
	return &safety.EvidencePlayer{ID: p.ID, Name: p.Name, Emoji: p.Avatar.Emoji, Color: p.Avatar.Color, Left: p.Left}
}

// attribute names the author of the content as the room knows them. An
// automated seat is flagged and is never a target.
func (r *room) attribute(ev *safety.Evidence, id string) {
	if r.g.IsAutomated(id) {
		ev.Data.Automated = true
		return
	}
	ev.TargetGuest, ev.TargetUser = id, r.accounts[id]
	ev.Data.Author = &safety.Author{Guest: id, User: r.accounts[id]}
}

// visibleAnswer returns the text of player's answer in category of round, as the
// viewer's View shows it, with the round's letter. It is not visible when the
// View hides it (another player's answer while the round is played) or the
// round is not in the View.
func visibleAnswer(v game.View, round, category int, player string) (text, letter string, ok bool) {
	if v.Round != nil && v.Round.Number == round {
		answers, shown := v.Round.Answers[player]
		if !shown || category >= len(answers) {
			return "", "", false
		}
		return answers[category], v.Round.Letter, true
	}
	i := slices.IndexFunc(v.History, func(h game.RoundResult) bool { return h.Round == round })
	if i < 0 {
		return "", "", false
	}
	res, shown := v.History[i].Players[player]
	if !shown || category >= len(res.Cells) {
		return "", "", false
	}
	return res.Cells[category].Raw, v.History[i].Letter, true
}

// ---- provenance bookkeeping ----

// restoreInstance takes the instance ids and the report provenance of a
// restored snapshot. A snapshot from before the safety layer has no ids: the
// room gets new ones (nobody had seen the old room's, so no report can name
// them).
func (r *room) restoreInstance(p Persisted) {
	r.instanceID, r.matchID = p.InstanceID, p.MatchID
	r.labelAuthors, r.accounts = p.LabelAuthors, p.Accounts
	if r.instanceID == "" {
		r.instanceID = r.h.newInstanceID()
		r.log.Info("restored a snapshot without a room instance id: minted one")
	}
	if r.matchID == "" {
		r.matchID = r.h.newInstanceID()
	}
}

// linkAccount remembers which account the player that just joined is signed in
// with (none: a guest), for reports. The latest join wins.
func (r *room) linkAccount(s *session) {
	if r.g.IsAutomated(s.id) {
		return
	}
	if s.userID == "" {
		delete(r.accounts, s.id)
		return
	}
	if r.accounts == nil {
		r.accounts = map[string]string{}
	}
	r.accounts[s.id] = s.userID
}

// trackInstances renews the match id when a rematch starts a new match and
// drops the account links of seats that no longer exist.
func (r *room) trackInstances(events []game.Event) {
	for _, ev := range events {
		if ev.Type == game.EventRematch {
			r.matchID = r.h.newInstanceID()
		}
	}
	for id := range r.accounts {
		if !slices.ContainsFunc(r.g.Players, func(p game.Player) bool { return p.ID == id }) {
			delete(r.accounts, id)
		}
	}
}

// customLabelSet is the set of custom category labels of s.
func customLabelSet(s game.Settings) map[string]bool {
	set := map[string]bool{}
	for _, c := range s.Categories {
		if c.Custom != "" {
			set[c.Custom] = true
		}
	}
	return set
}

// recordLabelAuthors runs after an accepted settings update by session by. A
// custom label that is new is authored by whoever submitted it, with the
// account their connection carried; a label that was only kept or reordered
// keeps its first author; one that existed without a record (a legacy
// snapshot) stays unknown rather than being credited to the current host.
// Labels that are gone are forgotten.
func (r *room) recordLabelAuthors(prev map[string]bool, by *session) {
	var next map[string]safety.Author
	for _, c := range r.g.Settings.Categories {
		label := c.Custom
		if label == "" {
			continue
		}
		a, known := r.labelAuthors[label]
		switch {
		case known:
		case prev[label]:
			continue
		default:
			a = safety.Author{Guest: by.id, User: by.userID}
		}
		if next == nil {
			next = map[string]safety.Author{}
		}
		next[label] = a
	}
	r.labelAuthors = next
}

// publicLabels splits the categories of a lobby for the public directory: the
// built-in ones by label (lobby language, selection order) and how many are
// custom. A custom label is text a player typed and never leaves the room
// through the directory.
func publicLabels(s game.Settings) (labels []string, custom int) {
	labels = []string{}
	for _, ref := range s.Categories {
		switch {
		case ref.ID == "":
			custom++
		default:
			if c, ok := catalog.ByID(ref.ID); ok {
				labels = append(labels, catalog.Label(c, string(s.Lang)))
			} else {
				labels = append(labels, ref.ID)
			}
		}
	}
	return labels, custom
}
