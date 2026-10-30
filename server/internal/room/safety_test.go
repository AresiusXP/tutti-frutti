package room

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/AresiusXP/tutti-frutti/server/internal/api"
	"github.com/AresiusXP/tutti-frutti/server/internal/game"
	"github.com/AresiusXP/tutti-frutti/server/internal/protocol"
	"github.com/AresiusXP/tutti-frutti/server/internal/safety"
	"github.com/AresiusXP/tutti-frutti/server/internal/store"
)

const (
	userAna  = "00000000-0000-4000-8000-0000000000a1"
	userBeto = "00000000-0000-4000-8000-0000000000b2"
)

// fakeGate is a TermsGate the test controls.
type fakeGate struct {
	mu       sync.Mutex
	accepted map[string]bool
	err      error
	calls    int
}

func newGate(accepted ...string) *fakeGate {
	g := &fakeGate{accepted: map[string]bool{}}
	for _, id := range accepted {
		g.accepted[id] = true
	}
	return g
}

func (g *fakeGate) Accepted(_ context.Context, id string) (bool, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.calls++
	if g.err != nil {
		return false, g.err
	}
	return g.accepted[id], nil
}

func (g *fakeGate) set(id string, ok bool) {
	g.mu.Lock()
	g.accepted[id] = ok
	g.mu.Unlock()
}

func (g *fakeGate) fail(err error) {
	g.mu.Lock()
	g.err = err
	g.mu.Unlock()
}

func (g *fakeGate) callCount() int {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.calls
}

func withGate(g *fakeGate) func(*Config) { return func(c *Config) { c.Terms = g } }

// sendJoin sends a join frame on an existing session.
func (e *env) sendJoin(code string, s Session, name string) {
	e.t.Helper()
	e.handle(code, s, game.CmdJoin, protocol.JoinRequest{Code: code, Name: name, Avatar: testAvatar})
}

func lastKind(frames []frameRec) string {
	if len(frames) == 0 {
		return ""
	}
	return frames[len(frames)-1].kind()
}

func joinedCount(e *env, code string) int {
	return query(e, code, func(r *room) int { return len(r.players) })
}

// ---------------------------------------------------------------------------
// Terms enforcement at the join
// ---------------------------------------------------------------------------

func TestJoinWithoutTermsIsRefusedAndRetryableOnTheSameConnection(t *testing.T) {
	gate := newGate()
	e := newEnv(t, withGate(gate))
	code := e.create("p1", "Ana")
	c, s := e.connect(code, "p1")

	e.sendJoin(code, s, "Ana")
	frames := c.drain()
	if got := kinds(frames); len(got) != 1 || got[0] != "error:terms_required" {
		t.Fatalf("frames = %v, want one error:terms_required", got)
	}
	if eb := frames[0].errBody(t); eb.Ref != game.CmdJoin || eb.Code != protocol.ErrTermsRequired {
		t.Errorf("error = %+v", eb)
	}
	if n := joinedCount(e, code); n != 0 {
		t.Fatalf("%d players joined without accepting", n)
	}
	if len(c.closeCodes()) != 0 {
		t.Errorf("the refused connection was closed: %v", c.closeCodes())
	}
	// A command before a successful join is still not_joined: nothing slips through.
	e.handle(code, s, game.CmdSetAnswer, game.SetAnswer{Category: 0, Text: "x"})
	if got := lastKind(c.drain()); got != "error:not_joined" {
		t.Errorf("set_answer after a refused join = %s", got)
	}
	if v := metricValue(t, e.reg, "tuttifrutti_commands_total", "type", "join", "result", "terms_required"); v != 1 {
		t.Errorf("commands_total{join,terms_required} = %v", v)
	}

	// The guest accepts over REST and joins again on the same connection.
	gate.set("p1", true)
	e.sendJoin(code, s, "Ana")
	if got := lastKind(c.drain()); got != "state" {
		t.Fatalf("after accepting: %s, want state", got)
	}
	if n := joinedCount(e, code); n != 1 {
		t.Errorf("joined = %d", n)
	}
}

func TestJoinFailsClosedWhenTheGateCannotTell(t *testing.T) {
	gate := newGate("p1")
	gate.fail(errors.New("database down"))
	e := newEnv(t, withGate(gate))
	code := e.create("p1", "Ana")
	c, s := e.connect(code, "p1")

	e.sendJoin(code, s, "Ana")
	frames := c.drain()
	if got := kinds(frames); len(got) != 1 || got[0] != "error:server_busy" {
		t.Fatalf("frames = %v: an unknown acceptance must refuse with the retryable server_busy, even for a guest that did accept", got)
	}
	if n := joinedCount(e, code); n != 0 {
		t.Fatalf("%d players joined on an unknown acceptance", n)
	}
	if !strings.Contains(e.logs.String(), "refusing the join") {
		t.Errorf("the refusal was not logged:\n%s", e.logs.String())
	}

	gate.fail(nil)
	e.sendJoin(code, s, "Ana")
	if got := lastKind(c.drain()); got != "state" {
		t.Errorf("after the gate recovered: %s", got)
	}
}

func TestEveryJoinAsksTheGateAgain(t *testing.T) {
	gate := newGate("p1", "p2")
	e := newEnv(t, withGate(gate))
	code := e.create("p1", "Ana")
	_, s1 := e.join(code, "p1", "Ana")
	_, s2 := e.join(code, "p2", "Beto")
	if gate.callCount() != 2 {
		t.Fatalf("gate calls = %d, want one per join", gate.callCount())
	}

	// p2 reconnects (a new socket) after a deploy that raised the terms version:
	// the gate no longer knows them, the seat they hold stays untouched.
	s2.Disconnect()
	e.sync(code)
	gate.set("p2", false)
	c2b, s2b := e.connect(code, "p2")
	e.sendJoin(code, s2b, "Beto")
	if got := lastKind(c2b.drain()); got != "error:terms_required" {
		t.Fatalf("reconnect without accepting: %s", got)
	}
	seat := query(e, code, func(r *room) []game.Player { return slices.Clone(r.g.Players) })
	if len(seat) != 2 || seat[1].ID != "p2" || seat[1].Connected {
		t.Errorf("seats = %+v: the refused reconnect must leave p2's seat disconnected and intact", seat)
	}
	_ = s1

	gate.set("p2", true)
	e.sendJoin(code, s2b, "Beto")
	if got := lastKind(c2b.drain()); got != "state" {
		t.Errorf("reconnect after accepting: %s", got)
	}
}

func TestARestoredRoomStillRequiresTheTerms(t *testing.T) {
	gate := newGate("p1", "p2")
	e := newEnv(t, withGate(gate))
	code := e.create("p1", "Ana")
	e.join(code, "p1", "Ana")
	e.join(code, "p2", "Beto")
	if err := e.hub.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}

	fresh := newGate("p1") // the restart: p2 never accepted this version
	e.hub = e.newHubOn(withGate(fresh))
	if n, err := e.hub.Start(context.Background()); err != nil || n != 1 {
		t.Fatalf("Start = %d, %v", n, err)
	}
	c1, s1 := e.connect(code, "p1")
	e.sendJoin(code, s1, "Ana")
	if got := lastKind(c1.drain()); got != "state" {
		t.Fatalf("p1: %s", got)
	}
	c2, s2 := e.connect(code, "p2")
	e.sendJoin(code, s2, "Beto")
	if got := lastKind(c2.drain()); got != "error:terms_required" {
		t.Errorf("p2 reclaiming a seat without accepting: %s", got)
	}
}

func TestContentCommandsNeedTheTermsToo(t *testing.T) {
	l := newLobby3(t, withGate(newGate("p1", "p2", "p3")))
	l.startRound(nil)

	// Second line of defence: a session that is joined but lost its acceptance
	// (it cannot happen through join; this is what the command check is for).
	l.e.room(l.code).post(func(r *room) {
		for s := range r.sessions {
			if s.id == "p2" {
				s.termsOK = false
			}
		}
	}, nil)
	l.e.sync(l.code)

	l.send(1, game.CmdSetAnswer, game.SetAnswer{Category: 0, Text: "Almeja"})
	if got := lastKind(l.c[1].drain()); got != "error:terms_required" {
		t.Errorf("set_answer without terms: %s", got)
	}
	if a := query(l.e, l.code, func(r *room) string { return r.g.Round.Answers["p2"][0] }); a != "" {
		t.Errorf("the answer was stored: %q", a)
	}
	// Players that accepted are unaffected, and so are the commands that carry no text.
	l.send(2, game.CmdSetAnswer, game.SetAnswer{Category: 0, Text: "Avion"})
	if got := lastKind(l.c[2].drain()); got != "state" {
		t.Errorf("p3's set_answer: %s", got)
	}
	l.send(1, game.CmdAskStop, nil)
	if got := lastKind(l.c[1].drain()); strings.HasSuffix(got, "terms_required") {
		t.Errorf("ask_stop needs no terms: %s", got)
	}
}

func TestSettingsWithCustomLabelsNeedTheTerms(t *testing.T) {
	l := newLobby3(t, withGate(newGate("p1", "p2", "p3")))
	l.e.room(l.code).post(func(r *room) {
		for s := range r.sessions {
			if s.id == "p1" {
				s.termsOK = false
			}
		}
	}, nil)
	l.e.sync(l.code)
	l.send(0, game.CmdUpdateSettings, threeCats(func(s *game.Settings) {
		s.Categories = append(s.Categories, game.CategoryRef{Custom: "Cosas de gatos"})
	}))
	if got := lastKind(l.c[0].drain()); got != "error:terms_required" {
		t.Errorf("update_settings without terms: %s", got)
	}
	if custom := query(l.e, l.code, func(r *room) int { return len(customLabelSet(r.g.Settings)) }); custom != 0 {
		t.Error("the label was stored")
	}
	if a := query(l.e, l.code, func(r *room) int { return len(r.labelAuthors) }); a != 0 {
		t.Error("an author was recorded for a refused label")
	}
}

func TestLeavingNeedsNoTerms(t *testing.T) {
	l := newLobby3(t, withGate(newGate("p1", "p2", "p3")))
	l.e.room(l.code).post(func(r *room) {
		for s := range r.sessions {
			s.termsOK = false
		}
	}, nil)
	l.e.sync(l.code)
	l.send(2, game.CmdLeave, nil)
	if got := l.c[2].closeCodes(); len(got) != 1 || got[0] != protocol.CloseLeft {
		t.Errorf("leave without terms closed with %v", got)
	}
	if n := joinedCount(l.e, l.code); n != 2 {
		t.Errorf("joined = %d", n)
	}
}

func TestWithoutAGateNothingIsRequired(t *testing.T) {
	e := newEnv(t)
	code := e.create("p1", "Ana")
	c, _ := e.join(code, "p1", "Ana")
	if got := lastKind(c.drain()); got != "state" {
		t.Errorf("join without a gate: %s", got)
	}
}

// ---------------------------------------------------------------------------
// Instance ids
// ---------------------------------------------------------------------------

type seqSource struct {
	mu sync.Mutex
	n  byte
}

func (s *seqSource) Read(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range p {
		s.n++
		p[i] = s.n
	}
	return len(p), nil
}

func TestInstanceIDsComeFromTheInjectedSource(t *testing.T) {
	e := newEnv(t, func(c *Config) { c.IDSource = &seqSource{} })
	code := e.create("p1", "Ana")
	c, _ := e.join(code, "p1", "Ana")
	st := lastState(t, c.drain())
	if st.RoomInstanceID != "01020304-0506-4708-890a-0b0c0d0e0f10" {
		t.Errorf("roomInstanceId = %q", st.RoomInstanceID)
	}
	if st.MatchInstanceID != "11121314-1516-4718-991a-1b1c1d1e1f20" {
		t.Errorf("matchInstanceId = %q", st.MatchInstanceID)
	}
	for _, id := range []string{st.RoomInstanceID, st.MatchInstanceID} {
		if !store.ValidID(id) {
			t.Errorf("%q is not a UUID", id)
		}
	}
}

func TestInstanceIDsAreStableAcrossStatesAndDifferPerRoom(t *testing.T) {
	e := newEnv(t)
	a, b := e.create("p1", "Ana"), e.create("p2", "Beto")
	ca, sa := e.join(a, "p1", "Ana")
	cb, _ := e.join(b, "p2", "Beto")
	first := lastState(t, ca.drain())
	e.handle(a, sa, game.CmdUpdateSettings, threeCats(nil))
	second := lastState(t, ca.drain())
	if first.RoomInstanceID == "" || first.RoomInstanceID != second.RoomInstanceID || first.MatchInstanceID != second.MatchInstanceID {
		t.Errorf("ids changed between states: %+v / %+v", first, second)
	}
	other := lastState(t, cb.drain())
	if other.RoomInstanceID == first.RoomInstanceID || other.MatchInstanceID == first.MatchInstanceID || first.RoomInstanceID == first.MatchInstanceID {
		t.Error("ids are not unique")
	}
}

// The preflight (GET /api/rooms/{code}) carries the instance id so a client that remembers a room can tell
// it from an unrelated one that got the same code later ("resume recent game").
func TestRoomInfoCarriesTheRoomInstanceID(t *testing.T) {
	e := newEnv(t)
	a, b := e.create("p1", "Ana"), e.create("p2", "Beto")
	ca, sa := e.join(a, "p1", "Ana")
	e.join(b, "p2", "Beto")
	state := lastState(t, ca.drain())

	infoA, ok := e.hub.Info(a)
	if !ok || !store.ValidID(infoA.RoomInstanceID) || infoA.RoomInstanceID != state.RoomInstanceID {
		t.Fatalf("Info.RoomInstanceID = %q (%v), State.RoomInstanceID = %q", infoA.RoomInstanceID, ok, state.RoomInstanceID)
	}
	if byCaller, ok := e.hub.InfoFor(a, "p1"); !ok || byCaller.RoomInstanceID != infoA.RoomInstanceID {
		t.Errorf("InfoFor = %+v (%v), want the same instance id as Info", byCaller, ok)
	}
	if anonymous, ok := e.hub.InfoFor(a, ""); !ok || anonymous.RoomInstanceID != infoA.RoomInstanceID {
		t.Errorf("InfoFor(anonymous) = %+v (%v), want the same instance id as Info", anonymous, ok)
	}
	if infoB, _ := e.hub.Info(b); infoB.RoomInstanceID == infoA.RoomInstanceID {
		t.Error("two rooms share an instance id")
	}

	// A settings change does not change it.
	e.handle(a, sa, game.CmdUpdateSettings, threeCats(nil))
	if again, _ := e.hub.Info(a); again.RoomInstanceID != infoA.RoomInstanceID {
		t.Errorf("the instance id changed: %q -> %q", infoA.RoomInstanceID, again.RoomInstanceID)
	}
}

func TestInstanceIDsSurviveARestartAndTheRematchRenewsTheMatch(t *testing.T) {
	l := newLobby3(t)
	e, code := l.e, l.code
	finishGame(t, l)
	st := query(e, code, func(r *room) [2]string { return [2]string{r.instanceID, r.matchID} })
	room1, match1 := st[0], st[1]
	if room1 == "" || match1 == "" {
		t.Fatalf("ids = %v", st)
	}

	// A restart keeps both.
	if err := e.hub.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
	p, _ := e.stored(code)
	if p.InstanceID != room1 || p.MatchID != match1 {
		t.Fatalf("the snapshot holds %q/%q, want %q/%q", p.InstanceID, p.MatchID, room1, match1)
	}
	e.hub = e.newHubOn()
	if _, err := e.hub.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	c1, s1 := e.join(code, "p1", "Ana")
	restored := lastState(t, c1.drain())
	if restored.RoomInstanceID != room1 || restored.MatchInstanceID != match1 {
		t.Errorf("after the restart: %q/%q, want %q/%q", restored.RoomInstanceID, restored.MatchInstanceID, room1, match1)
	}
	if info, ok := e.hub.Info(code); !ok || info.RoomInstanceID != room1 {
		t.Errorf("after the restart Info.RoomInstanceID = %q (%v), want %q", info.RoomInstanceID, ok, room1)
	}

	// The rematch keeps the room and renews the match; a restart then keeps the new one.
	e.handle(code, s1, game.CmdRematch, nil)
	after := lastState(t, c1.drain())
	if after.RoomInstanceID != room1 {
		t.Errorf("the rematch changed the room instance id: %q", after.RoomInstanceID)
	}
	if info, _ := e.hub.Info(code); info.RoomInstanceID != room1 {
		t.Errorf("the rematch changed Info.RoomInstanceID: %q", info.RoomInstanceID)
	}
	if after.MatchInstanceID == match1 || !store.ValidID(after.MatchInstanceID) {
		t.Errorf("the rematch kept the match instance id %q", after.MatchInstanceID)
	}
	e.waitSaved(code)
	if p, _ := e.stored(code); p.MatchID != after.MatchInstanceID || p.InstanceID != room1 {
		t.Errorf("the rematch was not persisted: %+v", p)
	}
}

func TestPublicGamesHaveInstanceIDsToo(t *testing.T) {
	e := newEnv(t)
	code := createPublic(e, "host", game.LangEN)
	c, s := e.join(code, "host", "Host")
	st := lastState(t, c.drain())
	if st.Visibility != "public" || !store.ValidID(st.RoomInstanceID) || !store.ValidID(st.MatchInstanceID) {
		t.Fatalf("state = %+v", st)
	}
	e.waitSaved(code)
	if p, _ := e.stored(code); p.InstanceID != st.RoomInstanceID || p.MatchID != st.MatchInstanceID {
		t.Errorf("a public room's ids are not persisted: %+v", p)
	}
	_ = s
}

// rewrite edits the stored snapshot of a room as an older server would have
// written it.
func rewrite(t *testing.T, e *env, code string, edit func(map[string]json.RawMessage)) {
	t.Helper()
	e.waitSaved(code)
	snaps, err := e.store.Memory.LoadAll(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, snap := range snaps {
		if snap.Code != code {
			continue
		}
		var m map[string]json.RawMessage
		if err := json.Unmarshal(snap.State, &m); err != nil {
			t.Fatal(err)
		}
		edit(m)
		snap.State, err = json.Marshal(m)
		if err != nil {
			t.Fatal(err)
		}
		if err := e.store.Memory.Save(context.Background(), snap); err != nil {
			t.Fatal(err)
		}
		return
	}
	t.Fatalf("no snapshot for %s", code)
}

func TestRestoreMintsIDsForASnapshotWithoutThem(t *testing.T) {
	for name, edit := range map[string]func(map[string]json.RawMessage){
		"snapshot from before the safety layer": func(m map[string]json.RawMessage) {
			delete(m, "instanceId")
			delete(m, "matchId")
			delete(m, "labelAuthors")
			delete(m, "accounts")
		},
		"malformed ids": func(m map[string]json.RawMessage) {
			m["instanceId"], m["matchId"] = json.RawMessage(`"not-a-uuid"`), json.RawMessage(`"A"`)
		},
	} {
		t.Run(name, func(t *testing.T) {
			e := newEnv(t)
			code := e.create("p1", "Ana")
			e.join(code, "p1", "Ana")
			old := query(e, code, func(r *room) [2]string { return [2]string{r.instanceID, r.matchID} })
			rewrite(t, e, code, edit)
			if err := e.hub.Shutdown(context.Background()); err != nil {
				t.Fatal(err)
			}
			// The shutdown wrote a final snapshot with the ids: edit that one.
			rewrite2(t, e, code, edit)

			e.hub = e.newHubOn()
			if n, err := e.hub.Start(context.Background()); err != nil || n != 1 {
				t.Fatalf("Start = %d, %v: the snapshot must restore", n, err)
			}
			now := query(e, code, func(r *room) [2]string { return [2]string{r.instanceID, r.matchID} })
			if !store.ValidID(now[0]) || !store.ValidID(now[1]) || now[0] == now[1] {
				t.Fatalf("minted ids = %v", now)
			}
			if now == old {
				t.Error("the old ids came back from nowhere")
			}
			if !strings.Contains(e.logs.String(), "minted one") {
				t.Errorf("the legacy restore is not logged:\n%s", e.logs.String())
			}
			// And they are persisted from now on: a second restart keeps them.
			c, _ := e.join(code, "p1", "Ana")
			if st := lastState(t, c.drain()); st.RoomInstanceID != now[0] || st.MatchInstanceID != now[1] {
				t.Errorf("state ids %q/%q, want %v", st.RoomInstanceID, st.MatchInstanceID, now)
			}
			e.waitSaved(code)
			if p, _ := e.stored(code); p.InstanceID != now[0] || p.MatchID != now[1] {
				t.Errorf("minted ids not persisted: %+v", p)
			}
		})
	}
}

// rewrite2 edits the snapshot without waiting for the (stopped) saver.
func rewrite2(t *testing.T, e *env, code string, edit func(map[string]json.RawMessage)) {
	t.Helper()
	snaps, err := e.store.Memory.LoadAll(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, snap := range snaps {
		if snap.Code != code {
			continue
		}
		var m map[string]json.RawMessage
		if err := json.Unmarshal(snap.State, &m); err != nil {
			t.Fatal(err)
		}
		edit(m)
		snap.State, _ = json.Marshal(m)
		if err := e.store.Memory.Save(context.Background(), snap); err != nil {
			t.Fatal(err)
		}
		return
	}
	t.Fatalf("no snapshot for %s", code)
}

func TestRestoreKeepsValidIDs(t *testing.T) {
	e := newEnv(t)
	code := e.create("p1", "Ana")
	e.join(code, "p1", "Ana")
	want := query(e, code, func(r *room) [2]string { return [2]string{r.instanceID, r.matchID} })
	if err := e.hub.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
	e.hub = e.newHubOn()
	if _, err := e.hub.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := query(e, code, func(r *room) [2]string { return [2]string{r.instanceID, r.matchID} }); got != want {
		t.Errorf("ids after the restart = %v, want %v", got, want)
	}
	if strings.Contains(e.logs.String(), "minted one") {
		t.Error("a snapshot with ids was treated as a legacy one")
	}
}

// ---------------------------------------------------------------------------
// Provenance
// ---------------------------------------------------------------------------

func customSettings(labels ...string) game.UpdateSettings {
	return threeCats(func(s *game.Settings) {
		for _, l := range labels {
			s.Categories = append(s.Categories, game.CategoryRef{Custom: l})
		}
	})
}

func authorsOf(e *env, code string) map[string]safety.Author {
	return query(e, code, func(r *room) map[string]safety.Author {
		out := map[string]safety.Author{}
		for k, v := range r.labelAuthors {
			out[k] = v
		}
		return out
	})
}

func TestCustomLabelAuthorship(t *testing.T) {
	e := newEnv(t)
	code := e.create("p1", "Ana")
	_, s1 := e.joinAs(code, "p1", "Ana", userAna)
	_, s2 := e.joinAs(code, "p2", "Beto", userBeto)
	e.join(code, "p3", "Cami")

	e.handle(code, s1, game.CmdUpdateSettings, customSettings("Cosas de gatos"))
	got := authorsOf(e, code)
	want := safety.Author{Guest: "p1", User: userAna}
	if len(got) != 1 || got["Cosas de gatos"] != want {
		t.Fatalf("authors = %+v, want the host at submission time", got)
	}

	// Kept and reordered: the first author stays; a new label belongs to whoever
	// submits it now.
	e.handle(code, s1, game.CmdUpdateSettings, threeCats(func(s *game.Settings) {
		s.Categories = []game.CategoryRef{{Custom: "Cosas de gatos"}, {ID: "nombre"}, {Custom: "Algo azul"}, {ID: "pais"}}
	}))
	got = authorsOf(e, code)
	if got["Cosas de gatos"] != want || got["Algo azul"] != want || len(got) != 2 {
		t.Fatalf("after the reorder: %+v", got)
	}

	// The host leaves the lobby: p2 inherits the host role and adds a label of
	// their own; the labels of the first host stay theirs.
	e.handle(code, s1, game.CmdLeave, nil)
	e.handle(code, s2, game.CmdUpdateSettings, threeCats(func(s *game.Settings) {
		s.Categories = []game.CategoryRef{{Custom: "Cosas de gatos"}, {ID: "nombre"}, {Custom: "Algo azul"}, {Custom: "Frutas raras"}}
	}))
	got = authorsOf(e, code)
	if got["Cosas de gatos"] != want || got["Algo azul"] != want {
		t.Errorf("labels of the first host were re-attributed: %+v", got)
	}
	if got["Frutas raras"] != (safety.Author{Guest: "p2", User: userBeto}) {
		t.Errorf("the new host's label: %+v", got["Frutas raras"])
	}

	// A label that was dropped is forgotten; typing it again is a new submission.
	e.handle(code, s2, game.CmdUpdateSettings, threeCats(nil))
	if got = authorsOf(e, code); len(got) != 0 {
		t.Errorf("authors of dropped labels survived: %+v", got)
	}
	e.handle(code, s2, game.CmdUpdateSettings, customSettings("Cosas de gatos"))
	if got = authorsOf(e, code); got["Cosas de gatos"] != (safety.Author{Guest: "p2", User: userBeto}) {
		t.Errorf("a re-submitted label = %+v", got)
	}
}

func TestLabelAuthorsAreSavedAndSurviveARestartAndRematch(t *testing.T) {
	e := newEnv(t)
	code := e.create("p1", "Ana")
	_, s1 := e.joinAs(code, "p1", "Ana", userAna)
	e.join(code, "p2", "Beto")
	e.handle(code, s1, game.CmdUpdateSettings, customSettings("Cosas de gatos"))
	e.waitSaved(code)
	want := safety.Author{Guest: "p1", User: userAna}
	if p, _ := e.stored(code); p.LabelAuthors["Cosas de gatos"] != want || p.Accounts["p1"] != userAna {
		t.Fatalf("snapshot = %+v / %+v", p.LabelAuthors, p.Accounts)
	}
	if err := e.hub.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
	e.hub = e.newHubOn()
	if _, err := e.hub.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := authorsOf(e, code); got["Cosas de gatos"] != want {
		t.Errorf("after the restart: %+v", got)
	}
}

func TestALegacyLabelStaysUnknownAndIsNotCreditedToTheCurrentHost(t *testing.T) {
	e := newEnv(t)
	code := e.create("p1", "Ana")
	_, s1 := e.join(code, "p1", "Ana")
	e.handle(code, s1, game.CmdUpdateSettings, customSettings("Cosas viejas"))
	if err := e.hub.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
	rewrite2(t, e, code, func(m map[string]json.RawMessage) { delete(m, "labelAuthors"); delete(m, "accounts") })

	e.hub = e.newHubOn()
	if _, err := e.hub.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	_, s := e.join(code, "p1", "Ana")
	if got := authorsOf(e, code); len(got) != 0 {
		t.Fatalf("a legacy label has an author: %+v", got)
	}
	// The host edits the settings, keeping the old label and adding a new one.
	e.handle(code, s, game.CmdUpdateSettings, customSettings("Cosas viejas", "Cosas nuevas"))
	got := authorsOf(e, code)
	if _, known := got["Cosas viejas"]; known {
		t.Errorf("the legacy label was credited to the current host: %+v", got)
	}
	if got["Cosas nuevas"].Guest != "p1" {
		t.Errorf("the new label has no author: %+v", got)
	}
}

func TestPersistedProvenanceIsCleaned(t *testing.T) {
	e := newEnv(t)
	code := e.create("p1", "Ana")
	_, s1 := e.joinAs(code, "p1", "Ana", userAna)
	e.handle(code, s1, game.CmdUpdateSettings, customSettings("Cosas de gatos"))
	if err := e.hub.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
	rewrite2(t, e, code, func(m map[string]json.RawMessage) {
		m["labelAuthors"] = json.RawMessage(`{"Cosas de gatos":{"guest":"p1","user":"nope"},"Fantasma":{"guest":"p1"}}`)
		m["accounts"] = json.RawMessage(`{"p1":"` + userAna + `","ghost":"` + userBeto + `","p1x":"bad"}`)
	})
	snaps, _ := e.store.Memory.LoadAll(context.Background())
	p, _, err := decodePersisted(snaps[0])
	if err != nil {
		t.Fatal(err)
	}
	if len(p.LabelAuthors) != 0 {
		t.Errorf("authors = %+v: a malformed author and a label that does not exist must go", p.LabelAuthors)
	}
	if len(p.Accounts) != 1 || p.Accounts["p1"] != userAna {
		t.Errorf("accounts = %+v: only the seat that exists, with a well-formed id, stays", p.Accounts)
	}
}

func TestAccountLinksFollowTheLatestJoinInEveryKindOfRoom(t *testing.T) {
	e := newEnv(t)
	code := createPublic(e, "p1", game.LangEN)
	_, s1 := e.joinAs(code, "p1", "Ana", userAna)
	accounts := func() map[string]string {
		return query(e, code, func(r *room) map[string]string {
			out := map[string]string{}
			for k, v := range r.accounts {
				out[k] = v
			}
			return out
		})
	}
	if got := accounts(); got["p1"] != userAna {
		t.Fatalf("public room accounts = %v: reports need them in public rooms too", got)
	}
	// The same guest comes back signed out: the link goes.
	s1.Disconnect()
	e.sync(code)
	_, _ = e.join(code, "p1", "Ana")
	if got := accounts(); len(got) != 0 {
		t.Errorf("accounts after a guest join = %v", got)
	}
	// And the statistics link of a public room is still not kept.
	if users := query(e, code, func(r *room) int { return len(r.users) }); users != 0 {
		t.Errorf("a public room keeps %d statistics links", users)
	}
}

// ---------------------------------------------------------------------------
// The public directory
// ---------------------------------------------------------------------------

func TestPublicDirectoryDoesNotLeakCustomLabels(t *testing.T) {
	e := newEnv(t)
	code := createPublic(e, "host", game.LangES)
	_, s := e.join(code, "host", "Host")
	secret := "Insulto muy feo"
	e.handle(code, s, game.CmdUpdateSettings, threeCats(func(st *game.Settings) {
		st.Rounds = 3
		st.Categories = []game.CategoryRef{{ID: "nombre"}, {Custom: secret}, {ID: "pais"}, {Custom: "Otra cosa fea"}}
	}))
	rooms := e.hub.PublicRooms("")
	if len(rooms) != 1 {
		t.Fatalf("rooms = %+v", rooms)
	}
	r := rooms[0]
	if !slices.Equal(r.CategoryLabels, []string{"Nombre", "País"}) || r.CustomCategories != 2 {
		t.Errorf("entry = %+v: built-in labels as today and a count of the custom ones", r)
	}
	raw, _ := json.Marshal(api.PublicRoomsResponse{Rooms: rooms})
	for _, leaked := range []string{secret, "Otra cosa fea", "fea"} {
		if bytes.Contains(raw, []byte(leaked)) {
			t.Errorf("the directory leaks %q: %s", leaked, raw)
		}
	}
	if !bytes.Contains(raw, []byte(`"customCategories":2`)) {
		t.Errorf("no customCategories in %s", raw)
	}
	// Inside the room the real labels are intact.
	c2, _ := e.join(code, "p2", "Beto")
	if labels := lastState(t, c2.drain()).View.CategoryLabels; !slices.Contains(labels, secret) {
		t.Errorf("labels inside the room = %v", labels)
	}
	// Only built-in categories: no marker.
	e.handle(code, s, game.CmdUpdateSettings, threeCats(nil))
	if rooms = e.hub.PublicRooms(""); len(rooms) != 1 || rooms[0].CustomCategories != 0 || len(rooms[0].CategoryLabels) != 3 {
		t.Errorf("entry = %+v", rooms)
	}
}
