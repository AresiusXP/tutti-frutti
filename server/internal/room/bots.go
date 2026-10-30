package room

import (
	"encoding/json"
	"slices"
	"time"

	"github.com/AresiusXP/tutti-frutti/server/internal/bot"
	"github.com/AresiusXP/tutti-frutti/server/internal/game"
	"github.com/AresiusXP/tutti-frutti/server/internal/store"
)

// botPlan contains only dictionary answers and this seat's own submitted cells.
// It is discarded across phase/round changes and when the humans disconnect.
type botPlan struct {
	id         string
	at         time.Time
	chooser    bool
	answers    []string
	category   int
	complete   bool
	bastaAfter time.Time
}

func (r *room) botRandom(n int) int {
	r.h.botRandMu.Lock()
	defer r.h.botRandMu.Unlock()
	return r.h.cfg.BotRand(n)
}

func (r *room) botDelay(low, high time.Duration) time.Duration {
	if high <= low {
		return low
	}
	return low + time.Duration(r.botRandom(int(high-low)+1))
}

func (r *room) humanSeated() int {
	n := 0
	for _, p := range r.g.Players {
		if !p.Left && !r.g.IsAutomated(p.ID) {
			n++
		}
	}
	return n
}

func (r *room) humanConnected() int {
	n := 0
	for _, p := range r.g.Players {
		if s := r.players[p.ID]; !p.Left && p.Connected && !r.g.IsAutomated(p.ID) && s != nil && !s.dead {
			n++
		}
	}
	return n
}

func (r *room) automatedSeats() []string {
	var ids []string
	for _, p := range r.g.Players {
		if !p.Left && r.g.IsAutomated(p.ID) {
			ids = append(ids, p.ID)
		}
	}
	return ids
}

func (r *room) botSettingsSupported() bool {
	return r.g.Settings.TimeLimitSeconds > 0 && bot.Supported(r.g.Settings)
}

func (r *room) botCapacity() int {
	return min(r.h.cfg.MaxBots, max(0, 3-r.humanSeated()))
}

// applyJoin admits humans atomically even when automated seats filled a lobby.
// The trial validates bans, identity and profile before any seat is displaced.
func (r *room) applyJoin(now time.Time, id string, cmd game.Join) ([]game.Event, error) {
	if r.g.Phase != game.PhaseLobby || r.seated() < game.MaxPlayers || len(r.automatedSeats()) == 0 {
		return r.g.Apply(now, id, cmd)
	}
	// An existing human reclaiming their seat needs no displacement trial.
	if slices.ContainsFunc(r.g.Players, func(p game.Player) bool { return p.ID == id && !p.Left && !r.g.IsAutomated(id) }) {
		return r.g.Apply(now, id, cmd)
	}
	raw, err := json.Marshal(r.g)
	if err != nil {
		return nil, err
	}
	var trial game.Game
	if err := json.Unmarshal(raw, &trial); err != nil {
		return nil, err
	}
	var events []game.Event
	for _, botID := range r.automatedSeats() {
		more, err := trial.Apply(now, botID, game.Leave{})
		events = append(events, more...)
		if err != nil {
			return nil, err
		}
	}
	more, err := trial.Apply(now, id, cmd)
	if err != nil {
		return nil, err
	}
	r.g = &trial
	return append(events, more...), nil
}

// reconcileBots is part of the same commit as the human command. It never
// creates seats; it trims lobby seats, mirrors human presence, and schedules.
func (r *room) reconcileBots(now time.Time) ([]game.Event, bool) {
	var events []game.Event
	changed := false
	ids := r.automatedSeats()
	if r.g.Phase == game.PhaseLobby {
		keep := r.botCapacity()
		if !r.botSettingsSupported() {
			keep = 0
		}
		for _, id := range ids[min(keep, len(ids)):] {
			more, err := r.g.Apply(now, id, game.Leave{})
			events = append(events, more...)
			changed = changed || err == nil
		}
	}
	present := r.humanConnected() > 0
	for _, p := range r.g.Players {
		if !p.Left && r.g.IsAutomated(p.ID) && p.Connected != present {
			more, err := r.g.SetConnected(now, p.ID, present)
			events = append(events, more...)
			changed = changed || err == nil
		}
	}
	host := r.players[r.g.HostID]
	lonely := r.h.cfg.BotsEnabled && r.h.PublicEnabled() && r.visibility == visibilityPublic && r.claimed &&
		!r.botSuppressed && r.g.Phase == game.PhaseLobby && r.humanConnected() == 1 &&
		r.botCapacity() > 0 && host != nil && !host.dead && !r.g.IsAutomated(r.g.HostID) && r.botSettingsSupported()
	if !lonely {
		r.botLonely = false
		r.botLonelySince = time.Time{}
		r.botFillAt = [2]time.Time{}
		r.botFillStep = 0
	} else if !r.botLonely {
		r.botLonely = true
		r.botLonelySince = now
		r.botFillStep = len(r.automatedSeats())
		r.botFillAt[0] = now.Add(r.botDelay(r.h.cfg.BotFirstDelayMin, r.h.cfg.BotFirstDelayMax))
		r.botFillAt[1] = now.Add(r.botDelay(r.h.cfg.BotSecondDelayMin, r.h.cfg.BotSecondDelayMax))
		// Custom ranges may overlap; the second seat still follows the first.
		r.botFillAt[1] = maxTime(r.botFillAt[1], r.botFillAt[0].Add(time.Millisecond))
	}
	if !present {
		r.botPlans, r.botRound, r.botPhase = nil, nil, ""
	} else if r.botRound != r.g.Round || r.botPhase != r.g.Phase {
		r.botRound, r.botPhase = r.g.Round, r.g.Phase
		r.prepareBots(now)
	} else {
		seats := r.automatedSeats()
		r.botPlans = slices.DeleteFunc(r.botPlans, func(plan botPlan) bool { return !slices.Contains(seats, plan.id) })
	}
	return events, changed
}

func maxTime(a, b time.Time) time.Time {
	if a.After(b) {
		return a
	}
	return b
}

// botFillPending reports whether a lonely lobby still waits for an automated
// seat. The switch of public games is read here too, so a fill that was
// scheduled before the operator switched them off never happens.
func (r *room) botFillPending() bool {
	return r.botLonely && r.h.PublicEnabled() && r.botFillStep < r.h.cfg.MaxBots && len(r.automatedSeats()) < r.botCapacity()
}

func (r *room) nextBotDeadline() time.Time {
	var at time.Time
	consider := func(t time.Time) {
		if !t.IsZero() && (at.IsZero() || t.Before(at)) {
			at = t
		}
	}
	if r.botFillPending() {
		consider(r.botFillAt[r.botFillStep])
	}
	for _, plan := range r.botPlans {
		consider(plan.at)
	}
	return at
}

func (r *room) prepareBots(now time.Time) {
	r.botPlans = nil
	if r.g.Round == nil {
		return
	}
	if r.g.Phase == game.PhaseLetter {
		if r.g.IsAutomated(r.g.ChooserID) {
			r.botPlans = []botPlan{{id: r.g.ChooserID, chooser: true, at: now.Add(r.botDelay(time.Second, 4*time.Second))}}
		}
		return
	}
	if r.g.Phase != game.PhasePlaying || r.g.Round.PlayingEndsAt.IsZero() {
		return
	}
	for _, id := range r.automatedSeats() {
		if !slices.Contains(r.g.Round.Participants, id) {
			continue
		}
		plan := botPlan{id: id, complete: true, bastaAfter: now.Add(25 * time.Second)}
		variant := uint64(r.botRandom(1 << 30))
		// Only this bot's cells are read, to preserve submissions on reconnect.
		own := r.g.Round.Answers[id]
		for i, category := range r.g.Settings.Categories {
			answer := own[i]
			if answer == "" && r.botRandom(10) != 0 {
				answer = bot.Answer(r.g.Settings.Lang, category.ID, r.g.Round.Letter, variant+uint64(i))
			}
			plan.answers = append(plan.answers, answer)
			plan.complete = plan.complete && answer != ""
		}
		plan.at = r.nextAnswerAt(now, len(plan.answers))
		r.botPlans = append(r.botPlans, plan)
	}
}

// Bound each typing gap inside the finite round. A late wake schedules from
// the current instant, rather than replaying a burst of backdated answers.
func (r *room) nextAnswerAt(now time.Time, remaining int) time.Time {
	end := r.g.Round.PlayingEndsAt.Add(-time.Second)
	if !now.Before(end) {
		return time.Time{}
	}
	gap := r.botDelay(4*time.Second, 10*time.Second)
	gap = min(gap, end.Sub(now)/time.Duration(remaining+1))
	if gap < time.Millisecond {
		return time.Time{}
	}
	return now.Add(gap)
}

// runBot consumes one due action. Every action is removed/rescheduled before
// Apply, including rejected commands, so an overdue timer cannot spin.
func (r *room) runBot(now time.Time) bool {
	if r.botFillPending() && !now.Before(r.botFillAt[r.botFillStep]) {
		r.botFillStep++
		variant := uint64(r.botRandom(1 << 30))
		var p game.Player
		// Each language has 24 distinct identities, more than the room's ten
		// seats. Consecutive variants avoid both human and automated names.
		for offset := range 24 {
			name, avatar := bot.Identity(r.g.Settings.Lang, variant+uint64(offset))
			normalized := game.Normalize(name, r.g.Settings.Lang)
			if !slices.ContainsFunc(r.g.Players, func(seat game.Player) bool {
				return !seat.Left && game.Normalize(seat.Name, r.g.Settings.Lang) == normalized
			}) {
				p = game.Player{ID: "bot:" + store.NewID(), Name: name, Avatar: avatar}
				break
			}
		}
		if p.ID == "" {
			r.rearm()
			return true
		}
		prev := r.g.Phase
		events, err := r.g.AddAutomated(now, p)
		if err != nil {
			r.log.Warn("adding automated seat failed", "err", err)
		} else {
			r.h.m.filled(now.Sub(r.botLonelySince))
		}
		r.commit(now, prev, events, true)
		return true
	}
	for i := range r.botPlans {
		plan := &r.botPlans[i]
		if plan.at.IsZero() || now.Before(plan.at) {
			continue
		}
		plan.at = time.Time{}
		var cmd game.Command
		switch {
		case plan.chooser:
			cmd = game.StopLetter{}
			r.stopTap = true
		case plan.category < len(plan.answers):
			cmd = game.SetAnswer{Category: plan.category, Text: plan.answers[plan.category]}
			plan.category++
			if plan.category < len(plan.answers) {
				plan.at = r.nextAnswerAt(now, len(plan.answers)-plan.category)
			} else if plan.complete {
				plan.at = maxTime(plan.bastaAfter, now.Add(r.botDelay(time.Second, 4*time.Second)))
				if !plan.at.Before(r.g.Round.PlayingEndsAt) {
					plan.at = time.Time{}
				}
			}
		default:
			cmd = game.Basta{}
		}
		prev := r.g.Phase
		events, _ := r.g.Apply(now, plan.id, cmd)
		r.commit(now, prev, events, false)
		return true
	}
	return false
}
