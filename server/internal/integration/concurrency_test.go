package integration

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"math/rand/v2"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/AresiusXP/tutti-frutti/server/internal/game"
	"github.com/AresiusXP/tutti-frutti/server/internal/protocol"
)

// cseat is one player of a concurrently played room.
type cseat struct {
	idx int
	p   *player
	mu  sync.Mutex
	c   *client
	all []*client  // every socket the seat ever had
	rng *rand.Rand // owned by the seat's goroutine
}

func (cs *cseat) client() *client {
	cs.mu.Lock()
	defer cs.mu.Unlock()
	return cs.c
}

func (cs *cseat) setClient(c *client) {
	cs.mu.Lock()
	defer cs.mu.Unlock()
	cs.c = c
	cs.all = append(cs.all, c)
}

type roomRun struct {
	s     *stack
	idx   int
	rng   *rand.Rand
	code  string
	seats []*cseat
}

// answer is what seat j types in category k: unique per player and category,
// and it starts with the letter A (the first letter of the alphabet, which the
// chooser gets when the clock has not moved).
func answer(j, k int) string { return fmt.Sprintf("A%d%d", j, k) }

func (r *roomRun) ids() []string {
	out := make([]string, len(r.seats))
	for i, cs := range r.seats {
		out[i] = cs.p.id
	}
	return out
}

// connect opens a socket for the seat and joins the room.
func (r *roomRun) connect(cs *cseat) error {
	c, err := r.s.tryConnect(cs.p)
	if err != nil {
		return err
	}
	cs.setClient(c)
	_, err = c.tryJoin(r.code)
	return err
}

// flap makes the seat lose its connection and come back: abruptly closing the
// socket, or opening the new socket before the old one is gone (the old one
// must then be closed by the server with 4001).
func (r *roomRun) flap(cs *cseat) error {
	old := cs.client()
	if cs.rng.IntN(2) == 0 {
		old.drop()
		return r.connect(cs)
	}
	if err := r.connect(cs); err != nil {
		return err
	}
	return old.tryExpectClose(protocol.CloseReplaced)
}

// eachSeat runs fn for every seat concurrently.
func (r *roomRun) eachSeat(fn func(cs *cseat) error) error {
	errs := make([]error, len(r.seats))
	var wg sync.WaitGroup
	for i, cs := range r.seats {
		wg.Add(1)
		go func() {
			defer wg.Done()
			errs[i] = fn(cs)
		}()
	}
	wg.Wait()
	return errors.Join(errs...)
}

// everyone waits until every seat's latest state satisfies pred.
func (r *roomRun) everyone(desc string, pred func(v game.View) bool) error {
	return r.eachSeat(func(cs *cseat) error {
		_, err := cs.client().awaitState(desc, func(st *protocol.State) bool { return pred(st.View) })
		return err
	})
}

func (r *roomRun) command(cs *cseat, typ string, payload any) error {
	code, err := cs.client().tryRoundTrip(typ, payload)
	if err != nil {
		return err
	}
	if code != "" {
		return fmt.Errorf("%s: %s rejected with %q", cs.p.name, typ, code)
	}
	return nil
}

// runAll runs one step of the game in every room concurrently and fails the
// test with every error.
func runAll(t *testing.T, rooms []*roomRun, step string, fn func(r *roomRun) error) {
	t.Helper()
	errs := make([]error, len(rooms))
	var wg sync.WaitGroup
	for i, r := range rooms {
		wg.Add(1)
		go func() {
			defer wg.Done()
			defer func() {
				if p := recover(); p != nil {
					errs[i] = fmt.Errorf("panic: %v", p)
				}
			}()
			errs[i] = fn(r)
		}()
	}
	wg.Wait()
	for i, err := range errs {
		if err != nil {
			t.Errorf("step %q, room %d (%s): %v", step, i, rooms[i].code, err)
		}
	}
	if t.Failed() {
		t.FailNow()
	}
}

// TestConcurrencySmoke plays ten rooms of five players at the same time, with
// players dropping and reconnecting (and double-connecting) at random while
// they type and while the answers are reviewed. Run it under -race.
func TestConcurrencySmoke(t *testing.T) {
	const roomCount, perRoom = 10, 5
	s := newStack(t, stackOpts{})

	rooms := make([]*roomRun, roomCount)
	for i := range rooms {
		rooms[i] = &roomRun{s: s, idx: i, rng: rand.New(rand.NewPCG(7, uint64(i)))}
	}

	// ---- lobbies ----
	runAll(t, rooms, "lobby", func(r *roomRun) error {
		for j := range perRoom {
			p, err := s.tryNewPlayer(fmt.Sprintf("R%dP%d", r.idx, j))
			if err != nil {
				return err
			}
			r.seats = append(r.seats, &cseat{idx: j, p: p, rng: rand.New(rand.NewPCG(11, uint64(r.idx*perRoom+j)))})
		}
		code, err := s.tryCreateRoom(r.seats[0].p, "")
		if err != nil {
			return err
		}
		r.code = code
		for _, cs := range r.seats { // in order: the host is the first seat
			if err := r.connect(cs); err != nil {
				return err
			}
		}
		return r.everyone("everybody connected", func(v game.View) bool {
			if len(v.Players) != perRoom {
				return false
			}
			for _, p := range v.Players {
				if !p.Connected {
					return false
				}
			}
			return true
		})
	})
	if got := s.hub.Stats(); got.Rooms != roomCount || got.Connections != roomCount*perRoom {
		t.Fatalf("stats after the lobbies = %+v", got)
	}
	codes := map[string]bool{}
	for _, r := range rooms {
		codes[r.code] = true
	}
	if len(codes) != roomCount {
		t.Fatalf("room codes are not unique: %v", codes)
	}

	// ---- start: the host is the chooser of round 1 and taps STOP at once ----
	settings := roundSettings()
	settings.Rounds = 1
	runAll(t, rooms, "start", func(r *roomRun) error {
		host := r.seats[0]
		if err := r.command(host, game.CmdUpdateSettings, game.UpdateSettings{Settings: settings}); err != nil {
			return err
		}
		if err := r.command(host, game.CmdStartGame, nil); err != nil {
			return err
		}
		if err := r.everyone("letter phase", func(v game.View) bool { return v.Phase == game.PhaseLetter && v.ChooserID == host.p.id }); err != nil {
			return err
		}
		if err := r.command(host, game.CmdStopLetter, nil); err != nil {
			return err
		}
		return r.everyone("letter A", func(v game.View) bool { return v.Phase == game.PhasePlaying && v.Round.Letter == "A" })
	})

	// ---- everybody types while some players drop and reconnect ----
	runAll(t, rooms, "typing", func(r *roomRun) error {
		plans := make([][2]bool, len(r.seats)) // flap before, flap after
		for i := range plans {
			plans[i] = [2]bool{r.rng.IntN(100) < 60, r.rng.IntN(100) < 40}
		}
		return r.eachSeat(func(cs *cseat) error {
			if plans[cs.idx][0] {
				if err := r.flap(cs); err != nil {
					return fmt.Errorf("%s: flap before typing: %w", cs.p.name, err)
				}
			}
			want := []string{answer(cs.idx, 0), answer(cs.idx, 1), answer(cs.idx, 2)}
			for attempt := 0; ; attempt++ {
				c := cs.client()
				for k, a := range want {
					if err := c.trySend(game.CmdSetAnswer, game.SetAnswer{Category: k, Text: a}); err != nil {
						break // the socket died under us: reconnect below
					}
				}
				_, err := c.awaitState("own answers", func(st *protocol.State) bool {
					return st.View.Phase == game.PhasePlaying && slices.Equal(st.View.Round.Answers[cs.p.id], want)
				})
				if err == nil {
					break
				}
				if attempt == 3 || !c.isClosed() {
					return fmt.Errorf("%s: %w", cs.p.name, err)
				}
				if err := r.connect(cs); err != nil {
					return err
				}
			}
			if plans[cs.idx][1] {
				if err := r.flap(cs); err != nil {
					return fmt.Errorf("%s: flap after typing: %w", cs.p.name, err)
				}
			}
			return nil
		})
	})

	// ---- consistent state: same room for everybody, answers kept, others' hidden ----
	runAll(t, rooms, "consistency while playing", func(r *roomRun) error {
		ids := r.ids()
		return r.eachSeat(func(cs *cseat) error {
			st, err := cs.client().awaitState("everybody is back with 3 answers filled", func(st *protocol.State) bool {
				v := st.View
				if v.Phase != game.PhasePlaying || len(v.Players) != perRoom {
					return false
				}
				for _, p := range v.Players {
					if !p.Connected || v.Round.Filled[p.ID] != 3 {
						return false
					}
				}
				return true
			})
			if err != nil {
				return fmt.Errorf("%s: %w", cs.p.name, err)
			}
			v := st.View
			if st.Code != r.code || v.YouID != cs.p.id || v.HostID != ids[0] || v.Round.Letter != "A" ||
				!slices.Equal(v.Round.Participants, ids) || len(v.Round.Answers) != 1 {
				return fmt.Errorf("%s: inconsistent view: code %q you %q host %q participants %v answers %d",
					cs.p.name, st.Code, v.YouID, v.HostID, v.Round.Participants, len(v.Round.Answers))
			}
			return nil
		})
	})

	// ---- basta, grace: the clock moves for every room at once ----
	runAll(t, rooms, "basta", func(r *roomRun) error {
		if err := r.command(r.seats[2], game.CmdBasta, nil); err != nil {
			return err
		}
		return r.everyone("grace", func(v game.View) bool { return v.Phase == game.PhaseGrace && v.Round.CallerID == r.seats[2].p.id })
	})
	s.advance(graceFor + lateFor)

	// ---- review, with more players dropping and coming back ----
	runAll(t, rooms, "review", func(r *roomRun) error {
		if err := r.everyone("review", func(v game.View) bool { return v.Phase == game.PhaseReview }); err != nil {
			return err
		}
		flapping := map[int]bool{}
		for range 2 {
			flapping[r.rng.IntN(perRoom)] = true
		}
		return r.eachSeat(func(cs *cseat) error {
			if flapping[cs.idx] {
				if err := r.flap(cs); err != nil {
					return err
				}
			}
			ids := r.ids()
			_, err := cs.client().awaitState("review with everybody's points", func(st *protocol.State) bool {
				v := st.View
				if v.Phase != game.PhaseReview || len(v.Players) != perRoom || v.Round.Result == nil {
					return false
				}
				for _, p := range v.Players {
					if !p.Connected {
						return false
					}
				}
				for j, id := range ids {
					if !slices.Equal(v.Round.Answers[id], []string{answer(j, 0), answer(j, 1), answer(j, 2)}) || v.Round.Result.Players[id].Total != 30 {
						return false
					}
				}
				return true
			})
			return err
		})
	})

	// ---- finish: every player ties on 30 points ----
	runAll(t, rooms, "finish", func(r *roomRun) error {
		host := r.seats[0]
		if err := r.command(host, game.CmdFinishReview, nil); err != nil {
			return err
		}
		if err := r.everyone("summary", func(v game.View) bool { return v.Phase == game.PhaseRoundSummary }); err != nil {
			return err
		}
		if err := r.command(host, game.CmdEndGame, nil); err != nil {
			return err
		}
		ids := r.ids()
		return r.everyone("final with five winners", func(v game.View) bool {
			if v.Phase != game.PhaseFinal || len(v.Winners) != perRoom {
				return false
			}
			for _, id := range ids {
				if p, _ := seatOf(v, id); p.TotalScore != 30 || !slices.Contains(v.Winners, id) {
					return false
				}
			}
			return true
		})
	})

	// ---- privacy over every frame of every connection ----
	checked := 0
	for _, r := range rooms {
		for _, cs := range r.seats {
			for _, c := range cs.all {
				for _, f := range c.snapshotFrames() {
					if f.State == nil || (f.State.View.Phase != game.PhasePlaying && f.State.View.Phase != game.PhaseGrace) {
						continue
					}
					checked++
					for j := range r.seats {
						if j == cs.idx {
							continue
						}
						for k := range 3 {
							quoted, _ := json.Marshal(answer(j, k))
							if bytes.Contains(f.Raw, quoted) {
								t.Errorf("room %d: %s saw %s's answer %s while %s: %s", r.idx, cs.p.name, r.seats[j].p.name, quoted, f.State.View.Phase, f)
							}
						}
					}
				}
			}
		}
	}
	if checked < roomCount*perRoom*5 {
		t.Errorf("only %d playing/grace frames checked", checked)
	}

	// ---- everybody goes away, some politely, most not ----
	if got := s.hub.Stats(); got.Rooms != roomCount {
		t.Fatalf("stats before the exodus = %+v", got)
	}
	var wg sync.WaitGroup
	for _, r := range rooms {
		for _, cs := range r.seats {
			if cs.idx%2 == 0 { // the others are closed by the server's drain (1012)
				wg.Add(1)
				go func() {
					defer wg.Done()
					cs.client().drop()
				}()
			}
		}
	}
	wg.Wait()
	if err := s.shutdown(); err != nil {
		t.Fatalf("shutdown: %v", err)
	}
	if got := s.hub.Stats(); got.Rooms != 0 || got.Connections != 0 {
		t.Fatalf("hub stats after shutdown = %+v, want zeros", got)
	}
	if got := s.wsh.Open(); got != 0 {
		t.Fatalf("ws handler still has %d sockets", got)
	}
	for _, r := range rooms {
		for _, cs := range r.seats {
			select {
			case <-cs.client().done:
			case <-time.After(waitLimit):
				t.Fatalf("%s: socket still open after the shutdown", cs.p.name)
			}
		}
	}
	if got := s.metricValue("tuttifrutti_rooms", nil); got != 0 {
		t.Errorf("rooms gauge = %v", got)
	}
	if got := s.metricValue("tuttifrutti_connections", nil); got != 0 {
		t.Errorf("connections gauge = %v", got)
	}
	if got := s.metricValue("tuttifrutti_rounds_total", nil); got != roomCount {
		t.Errorf("rounds_total = %v, want %d", got, roomCount)
	}
	if got := s.metricValue("tuttifrutti_basta_total", nil); got != roomCount {
		t.Errorf("basta_total = %v, want %d", got, roomCount)
	}
	// The final snapshots of all the rooms are in the store.
	for _, r := range rooms {
		g, ok := snapshot(t, s.store, r.code)
		if !ok || g.Phase != game.PhaseFinal || len(g.Winners) != perRoom {
			t.Errorf("room %d: final snapshot = %+v (found %v)", r.idx, g, ok)
		}
	}
}
