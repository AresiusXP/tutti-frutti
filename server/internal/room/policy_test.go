package room

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/AresiusXP/tutti-frutti/server/internal/api"
	"github.com/AresiusXP/tutti-frutti/server/internal/game"
	"github.com/AresiusXP/tutti-frutti/server/internal/protocol"
	"github.com/AresiusXP/tutti-frutti/server/internal/safety"
)

// policyEnv is an env whose hub enforces a policy driven by the env's clock.
func policyEnv(t *testing.T, mods ...func(*Config)) (*env, *safety.Policy) {
	t.Helper()
	var p *safety.Policy
	e := newEnv(t, append([]func(*Config){func(c *Config) {
		p = safety.NewPolicy(func() time.Time { return c.Clock.Now() })
		c.Policy = p
	}}, mods...)...)
	return e, p
}

func ctxT(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	t.Cleanup(cancel)
	return ctx
}

func seatedIn(e *env, code string) []string {
	return query(e, code, func(r *room) []string { return r.seatedHumans() })
}

func errorCodes(frames []frameRec) []string {
	var out []string
	for _, f := range frames {
		if k := f.kind(); strings.HasPrefix(k, "error:") {
			out = append(out, strings.TrimPrefix(k, "error:"))
		}
	}
	return out
}

func roomLive(e *env, code string) bool {
	e.hub.mu.Lock()
	defer e.hub.mu.Unlock()
	return e.hub.rooms[code] != nil
}

func joinFrame(code, name string) protocol.JoinRequest {
	return protocol.JoinRequest{Code: code, Name: name, Avatar: testAvatar}
}

// ---------------------------------------------------------------------------
// Admission
// ---------------------------------------------------------------------------

func TestBlockedPairCannotShareARoomInEitherDirection(t *testing.T) {
	for _, tc := range []struct {
		name           string
		blocker, other string
	}{
		{"the blocked player joins the blocker's room", "p1", "p2"},
		{"the blocker joins the blocked player's room", "p2", "p1"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e, policy := policyEnv(t)
			policy.AddBlock(tc.blocker, tc.other)
			code := e.create("p1", "Ana")
			e.join(code, "p1", "Ana")

			c, _ := e.join(code, "p2", "Beto")
			frames := c.drain()
			if got := errorCodes(frames); len(got) != 1 || got[0] != api.ErrCodeJoinUnavailable {
				t.Fatalf("frames = %v, want one error:join_unavailable", kinds(frames))
			}
			if eb := frames[0].errBody(t); eb.Ref != game.CmdJoin || eb.Code != "join_unavailable" {
				t.Errorf("error = %+v", eb)
			}
			if raw := string(frames[0].Raw); strings.Contains(raw, "p1") || strings.Contains(raw, "block") {
				t.Errorf("the refusal reveals who or why: %s", raw)
			}
			if got := seatedIn(e, code); !slices.Equal(got, []string{"p1"}) {
				t.Errorf("seats = %v, the refused guest took one", got)
			}
			if len(c.closeCodes()) != 0 {
				t.Errorf("a refused join must not close the socket: %v", c.closeCodes())
			}
			if v := metricValue(t, e.reg, "tuttifrutti_commands_total", "type", "join", "result", "join_unavailable"); v != 1 {
				t.Errorf("commands_total{join,join_unavailable} = %v", v)
			}
		})
	}
}

func TestUnblockedAndUnrelatedGuestsStillJoin(t *testing.T) {
	e, policy := policyEnv(t)
	code := e.create("p1", "Ana")
	e.join(code, "p1", "Ana")
	policy.AddBlock("p1", "p2")
	policy.AddBlock("someone", "p3")

	c3, _ := e.join(code, "p3", "Cami")
	if got := lastKind(c3.drain()); got != "state" {
		t.Errorf("an unrelated guest was refused: %s", got)
	}
	policy.RemoveBlock("p1", "p2") // an unblock never rejoins anybody, but it lifts the refusal
	c2, _ := e.join(code, "p2", "Beto")
	if got := lastKind(c2.drain()); got != "state" {
		t.Errorf("an unblocked guest was refused: %s", got)
	}
}

func TestReconnectOfABlockedPairIsRefused(t *testing.T) {
	for _, who := range []string{"p2", "p1"} { // the blocked player, then the blocker
		t.Run(who, func(t *testing.T) {
			e, policy := policyEnv(t)
			code := e.create("p1", "Ana")
			c1, s1 := e.join(code, "p1", "Ana")
			c2, s2 := e.join(code, "p2", "Beto")
			c1.drain()
			c2.drain()

			// A block that has not been swept yet (the window the sweep closes)
			// leaves a pair seated. Whoever reconnects is refused by the admission.
			policy.AddBlock("p1", "p2")
			if who == "p2" {
				s2.Disconnect()
			} else {
				s1.Disconnect()
			}
			e.sync(code)
			c, s := e.connect(code, who)
			e.handle(code, s, game.CmdJoin, joinFrame(code, "again"))
			if got := errorCodes(c.drain()); !slices.Equal(got, []string{"join_unavailable"}) {
				t.Fatalf("reconnect = %v, want join_unavailable", got)
			}
			if n := joinedCount(e, code); n != 1 {
				t.Errorf("%d players are joined, want only the one that stayed", n)
			}
		})
	}
}

func TestRefusedJoinNeverDisplacesAnAutomatedSeat(t *testing.T) {
	e, policy := policyEnv(t, deterministicBots)
	code := createPublic(e, "host", game.LangEN)
	e.join(code, "host", "Host")
	for i := range 7 {
		e.join(code, fmt.Sprintf("h%d", i), "Human")
	}
	query(e, code, func(r *room) bool {
		for _, id := range []string{"bot:first", "bot:second"} {
			if _, err := r.g.AddAutomated(e.clock.Now(), game.Player{ID: id, Name: "Alex"}); err != nil {
				panic(err)
			}
		}
		r.publishPublic()
		return true
	})
	before := botIDs(e, code)
	if len(before) != 2 {
		t.Fatalf("setup: %d automated seats", len(before))
	}

	policy.AddBlock("blocked-joiner", "h3")
	c, _ := e.join(code, "blocked-joiner", "Nope")
	if got := errorCodes(c.drain()); !slices.Equal(got, []string{"join_unavailable"}) {
		t.Fatalf("join = %v", got)
	}
	if got := botIDs(e, code); !slices.Equal(got, before) {
		t.Fatalf("a refused join displaced the automated seats: %v -> %v", before, got)
	}
	if n := len(seatedIn(e, code)); n != 8 {
		t.Fatalf("human seats = %d, want 8", n)
	}

	// Control: an unrelated human replaces the automated seats as before.
	good, _ := e.join(code, "good", "Good")
	if got := lastKind(good.drain()); got != "state" {
		t.Fatalf("an unrelated human was refused: %s", got)
	}
	if got := len(botIDs(e, code)); got != 0 {
		t.Errorf("automated seats after the admitted join = %d, want 0 (the join replaces them)", got)
	}
}

// ---------------------------------------------------------------------------
// Sweep: "block and leave" and restrictions
// ---------------------------------------------------------------------------

func lobbyOf3(e *env) *lobby3 {
	l := &lobby3{e: e, code: e.create("p1", "Ana")}
	for i, name := range []string{"Ana", "Beto", "Cami"} {
		l.c[i], l.s[i] = e.join(l.code, fmt.Sprintf("p%d", i+1), name)
	}
	l.drain()
	return l
}

func TestSweepRemovesTheBlockerThroughTheNormalLeavePath(t *testing.T) {
	e, policy := policyEnv(t)
	l := lobbyOf3(e)

	policy.AddBlock("p2", "p3") // Beto blocks Cami: Beto goes, Cami stays
	removed, err := e.hub.Sweep(ctxT(t))
	if err != nil {
		t.Fatal(err)
	}
	if got := removed[l.code]; !slices.Equal(got, []string{"p2"}) || len(removed) != 1 {
		t.Fatalf("removed = %v, want only p2 in %s", removed, l.code)
	}
	if got := l.c[1].closeCodes(); !slices.Equal(got, []int{protocol.CloseLeft}) {
		t.Errorf("the blocker's socket was closed with %v, want [%d] like a leave", got, protocol.CloseLeft)
	}
	if got := seatedIn(e, l.code); !slices.Equal(got, []string{"p1", "p3"}) {
		t.Errorf("seats = %v", got)
	}
	st := lastState(t, l.c[2].drain())
	if slices.ContainsFunc(st.View.Players, func(p game.Player) bool { return p.ID == "p2" }) {
		t.Error("the blocked player still sees the blocker in the lobby")
	}
	if len(l.c[2].closeCodes()) != 0 || len(l.c[0].closeCodes()) != 0 {
		t.Error("someone else was closed")
	}
	if again, _ := e.hub.Sweep(ctxT(t)); len(again) != 0 {
		t.Errorf("a second sweep removed %v: it must be idempotent", again)
	}
}

func TestSweepInAStartedGameFlagsTheSeatLeftAndKeepsTheScore(t *testing.T) {
	e, policy := policyEnv(t)
	l := lobbyOf3(e)
	l.startRound(nil)

	policy.AddBlock("p3", "p1")
	removed, err := e.hub.Sweep(ctxT(t))
	if err != nil || !slices.Equal(removed[l.code], []string{"p3"}) {
		t.Fatalf("Sweep = %v, %v", removed, err)
	}
	left := query(e, l.code, func(r *room) []string {
		var ids []string
		for _, p := range r.g.Players {
			if p.Left {
				ids = append(ids, p.ID)
			}
		}
		return ids
	})
	if !slices.Equal(left, []string{"p3"}) {
		t.Errorf("seats flagged Left = %v, want [p3]", left)
	}
	// The blocker cannot come back while the pair would share the room.
	c, s := e.connect(l.code, "p3")
	e.handle(l.code, s, game.CmdJoin, joinFrame(l.code, "Cami"))
	if got := errorCodes(c.drain()); !slices.Equal(got, []string{"join_unavailable"}) {
		t.Errorf("a blocker reclaiming the seat of a started game = %v", got)
	}
	// Once the blocked player has left, the blocker may reclaim the seat.
	l.send(0, game.CmdLeave, nil)
	c2, s2 := e.connect(l.code, "p3")
	e.handle(l.code, s2, game.CmdJoin, joinFrame(l.code, "Cami"))
	if got := lastKind(c2.drain()); got != "state" {
		t.Errorf("after the blocked player left, the join = %s", got)
	}
}

func TestSweepClosesARoomWhoseOnlyHumansAreGone(t *testing.T) {
	e, policy := policyEnv(t)
	code := e.create("p1", "Ana")
	c, _ := e.join(code, "p1", "Ana")
	c.drain()
	policy.AddRestriction(safety.Restriction{ID: "r1", GuestID: "p1", CreatedAt: e.clock.Now()})
	removed, err := e.hub.Sweep(ctxT(t))
	if err != nil || !slices.Equal(removed[code], []string{"p1"}) {
		t.Fatalf("Sweep = %v, %v", removed, err)
	}
	if roomLive(e, code) {
		t.Error("a room with no humans left must close like any room everybody left")
	}
	if len(c.closeCodes()) == 0 {
		t.Error("the connection of the removed player was not closed")
	}
}

func TestRestrictedGuestCannotCreateJoinOrReconnectAndIsRemoved(t *testing.T) {
	e, policy := policyEnv(t)
	code := e.create("host", "Hugo")
	e.join(code, "host", "Hugo")
	c, s := e.join(code, "bad", "Mala")
	c.drain()

	policy.AddRestriction(safety.Restriction{ID: "r1", GuestID: "bad", CreatedAt: e.clock.Now(), ExpiresAt: e.clock.Now().Add(time.Hour)})
	removed, err := e.hub.Sweep(ctxT(t))
	if err != nil || !slices.Equal(removed[code], []string{"bad"}) {
		t.Fatalf("Sweep = %v, %v", removed, err)
	}
	if got := c.closeCodes(); !slices.Equal(got, []int{protocol.CloseLeft}) {
		t.Errorf("closes = %v", got)
	}
	s.Disconnect()

	if _, err := e.hub.Create(ctxT(t), CreateRequest{PlayerID: "bad", Name: "Mala", Avatar: testAvatar}); !errors.Is(err, ErrRestricted) {
		t.Errorf("Create = %v, want ErrRestricted", err)
	}
	c2, s2 := e.connect(code, "bad")
	e.handle(code, s2, game.CmdJoin, joinFrame(code, "Mala"))
	if got := errorCodes(c2.drain()); !slices.Equal(got, []string{"join_unavailable"}) {
		t.Errorf("join = %v", got)
	}
	if info, _ := e.hub.InfoFor(code, "bad"); info.Joinable {
		t.Error("a restricted guest is told the room is joinable")
	}

	// The restriction expires by the clock: nothing has to run.
	e.clock.Advance(time.Hour)
	c3, s3 := e.connect(code, "bad")
	e.handle(code, s3, game.CmdJoin, joinFrame(code, "Mala"))
	if got := lastKind(c3.drain()); got != "state" {
		t.Errorf("after the restriction expired the join = %s", got)
	}

	// A revocation takes effect at once.
	policy.AddRestriction(safety.Restriction{ID: "r2", GuestID: "bad", CreatedAt: e.clock.Now()})
	if _, err := e.hub.Create(ctxT(t), CreateRequest{PlayerID: "bad", Name: "Mala", Avatar: testAvatar}); !errors.Is(err, ErrRestricted) {
		t.Errorf("Create under r2 = %v", err)
	}
	policy.RemoveRestriction("r2")
	if _, err := e.hub.Create(ctxT(t), CreateRequest{PlayerID: "bad", Name: "Mala", Avatar: testAvatar}); err != nil {
		t.Errorf("Create after the revocation = %v", err)
	}
}

func TestRestrictedAccountIsRefusedAndRemovedWhateverTheGuest(t *testing.T) {
	e, policy := policyEnv(t)
	code := e.create("host", "Hugo")
	e.join(code, "host", "Hugo")
	c, _ := e.joinAs(code, "guest-a", "Ana", userAna)
	c.drain()

	// The restriction names the account (a report proved the link) and another
	// guest identity of the same person.
	policy.AddRestriction(safety.Restriction{ID: "r1", GuestID: "other-guest", UserID: userAna, CreatedAt: e.clock.Now()})
	removed, err := e.hub.Sweep(ctxT(t))
	if err != nil || !slices.Equal(removed[code], []string{"guest-a"}) {
		t.Fatalf("Sweep = %v, %v: the seat of the restricted account must go", removed, err)
	}
	c2, _ := e.joinAs(code, "guest-a", "Ana", userAna)
	if got := errorCodes(c2.drain()); !slices.Equal(got, []string{"join_unavailable"}) {
		t.Errorf("join by the restricted account = %v", got)
	}
	if _, err := e.hub.Create(ctxT(t), CreateRequest{PlayerID: "guest-z", UserID: userAna, Name: "Ana", Avatar: testAvatar}); !errors.Is(err, ErrRestricted) {
		t.Errorf("Create by the restricted account = %v", err)
	}
	c3, _ := e.join(code, "guest-a", "Ana") // the same guest, signed out: not restricted
	if got := lastKind(c3.drain()); got != "state" {
		t.Errorf("the guest without the account = %s", got)
	}
}

func TestRestoredRoomSweepsAPairThePolicyForbids(t *testing.T) {
	e, _ := policyEnv(t)
	code := e.create("p1", "Ana")
	e.join(code, "p1", "Ana")
	e.join(code, "p2", "Beto")
	e.join(code, "p3", "Cami")
	e.waitSaved(code)
	ctx := ctxT(t)
	if err := e.hub.Shutdown(ctx); err != nil {
		t.Fatal(err)
	}

	// The block was stored just before the restart; the new process loads the
	// policy before Start, as cmd/server does.
	policy := safety.NewPolicy(e.clock.Now)
	policy.AddBlock("p1", "p2")
	h2 := e.newHubOn(func(c *Config) { c.Policy = policy })
	if n, err := h2.Start(ctx); err != nil || n != 1 {
		t.Fatalf("Start = %d, %v", n, err)
	}
	h2.mu.Lock()
	r := h2.rooms[code]
	h2.mu.Unlock()
	if r == nil {
		t.Fatal("the room was not restored")
	}
	ch := make(chan []string, 1)
	r.post(func(r *room) { ch <- r.seatedHumans() }, func() { ch <- nil })
	if got := <-ch; !slices.Equal(got, []string{"p2", "p3"}) {
		t.Errorf("restored seats = %v, want the blocker p1 gone", got)
	}
}

func TestSweepDuringShutdownIsRefused(t *testing.T) {
	e, _ := policyEnv(t)
	e.create("p1", "Ana")
	if err := e.hub.Shutdown(ctxT(t)); err != nil {
		t.Fatal(err)
	}
	if _, err := e.hub.Sweep(ctxT(t)); !errors.Is(err, ErrShuttingDown) {
		t.Errorf("Sweep = %v, want ErrShuttingDown", err)
	}
}

// ---------------------------------------------------------------------------
// Directory and RoomInfo
// ---------------------------------------------------------------------------

func TestDirectoryAndRoomInfoHideRoomsHoldingABlockedHuman(t *testing.T) {
	e, policy := policyEnv(t)
	pub := createPublic(e, "host", game.LangEN)
	e.join(pub, "host", "Host")
	e.join(pub, "guest", "Guest")
	other := createPublic(e, "host2", game.LangEN)
	e.join(other, "host2", "Host2")
	priv := e.create("host3", "Host3")
	e.join(priv, "host3", "Host3")

	codes := func(rooms []api.PublicRoom) []string {
		var out []string
		for _, r := range rooms {
			out = append(out, r.Code)
		}
		slices.Sort(out)
		return out
	}
	both := []string{pub, other}
	slices.Sort(both)
	if got := codes(e.hub.PublicRoomsFor("", "")); !slices.Equal(got, both) {
		t.Fatalf("anonymous directory = %v, want %v", got, both)
	}

	policy.AddBlock("caller", "guest") // the caller blocked a player of the first room
	if got := codes(e.hub.PublicRoomsFor("", "caller")); !slices.Equal(got, []string{other}) {
		t.Errorf("directory for the blocker = %v, want only %s", got, other)
	}
	policy.RemoveBlock("caller", "guest")
	policy.AddBlock("guest", "caller2") // the other direction: a human of the room blocked the caller
	if got := codes(e.hub.PublicRoomsFor("", "caller2")); !slices.Equal(got, []string{other}) {
		t.Errorf("directory for the blocked player = %v, want only %s", got, other)
	}
	if got := codes(e.hub.PublicRoomsFor("", "stranger")); !slices.Equal(got, both) {
		t.Errorf("directory for an unrelated caller = %v", got)
	}
	if got := codes(e.hub.PublicRoomsFor("", "")); !slices.Equal(got, both) {
		t.Errorf("an anonymous caller must see everything: %v", got)
	}
	raw, _ := json.Marshal(e.hub.PublicRoomsFor("", "stranger"))
	for _, secret := range []string{"guest", "host2", "members"} {
		if strings.Contains(string(raw), secret) {
			t.Errorf("the directory leaks %q: %s", secret, raw)
		}
	}

	// RoomInfo: not joinable, like a full room, for public and private rooms.
	policy.AddBlock("caller", "host3")
	policy.AddBlock("caller", "guest")
	for _, code := range []string{pub, priv} {
		info, ok := e.hub.InfoFor(code, "caller")
		if !ok || info.Joinable {
			t.Errorf("InfoFor(%s, caller) = %+v, %v: want joinable=false", code, info, ok)
		}
		if plain, _ := e.hub.Info(code); !plain.Joinable {
			t.Errorf("Info(%s) = %+v: an anonymous caller still sees the room as joinable", code, plain)
		}
		if other, _ := e.hub.InfoFor(code, "stranger"); !other.Joinable {
			t.Errorf("InfoFor(%s, stranger) = %+v", code, other)
		}
	}
	info, _ := e.hub.InfoFor(pub, "caller")
	plain, _ := e.hub.Info(pub)
	info.Joinable = true
	if info != plain {
		t.Errorf("the only difference between the two answers must be joinable: %+v vs %+v", info, plain)
	}
}

func TestDirectoryIndexFollowsTheSeats(t *testing.T) {
	e, policy := policyEnv(t)
	pub := createPublic(e, "host", game.LangEN)
	e.join(pub, "host", "Host")
	policy.AddBlock("caller", "late")
	if got := e.hub.PublicRoomsFor("", "caller"); len(got) != 1 {
		t.Fatalf("before the blocked player joined the room must be listed: %d", len(got))
	}
	c, s := e.join(pub, "late", "Late")
	c.drain()
	if got := e.hub.PublicRoomsFor("", "caller"); len(got) != 0 {
		t.Fatalf("the room holds a blocked human and must be hidden: %d", len(got))
	}
	e.handle(pub, s, game.CmdLeave, nil)
	if got := e.hub.PublicRoomsFor("", "caller"); len(got) != 1 {
		t.Errorf("after the blocked human left the room must be listed again: %d", len(got))
	}
}

// ---------------------------------------------------------------------------
// The operator's room takedown
// ---------------------------------------------------------------------------

func TestCloseRoomClosesExactlyThatInstance(t *testing.T) {
	e, _ := policyEnv(t)
	a := e.create("pa", "Ana")
	b := e.create("pb", "Beto")
	ca, _ := e.join(a, "pa", "Ana")
	cb, _ := e.join(b, "pb", "Beto")
	e.waitSaved(a)
	e.waitSaved(b)
	idA := query(e, a, func(r *room) string { return r.instanceID })
	idB := query(e, b, func(r *room) string { return r.instanceID })
	ctx := ctxT(t)

	if _, err := e.hub.CloseRoom(ctx, CloseTarget{}); !errors.Is(err, ErrRoomNotFound) {
		t.Errorf("an empty target = %v", err)
	}
	if _, err := e.hub.CloseRoom(ctx, CloseTarget{Code: "ZZZZ"}); !errors.Is(err, ErrRoomNotFound) {
		t.Errorf("an unknown code = %v", err)
	}
	if _, err := e.hub.CloseRoom(ctx, CloseTarget{InstanceID: "no-such-instance"}); !errors.Is(err, ErrRoomNotFound) {
		t.Errorf("an unknown instance = %v", err)
	}
	// A code whose room is another instance (the code was reused) is refused.
	if _, err := e.hub.CloseRoom(ctx, CloseTarget{Code: a, InstanceID: idB}); !errors.Is(err, ErrInstanceMismatch) {
		t.Errorf("a code with the wrong instance = %v, want ErrInstanceMismatch", err)
	}
	if len(ca.closeCodes()) != 0 || len(cb.closeCodes()) != 0 {
		t.Fatal("a refused close closed a room")
	}

	res, err := e.hub.CloseRoom(ctx, CloseTarget{InstanceID: idA})
	if err != nil || res.Code != a || res.RoomInstanceID != idA {
		t.Fatalf("CloseRoom by instance = %+v, %v", res, err)
	}
	if got := ca.closeCodes(); !slices.Equal(got, []int{protocol.CloseRoomClosed}) {
		t.Errorf("closes of the closed room = %v, want [%d]", got, protocol.CloseRoomClosed)
	}
	if len(cb.closeCodes()) != 0 {
		t.Error("the other room was closed")
	}
	eventually(t, "the snapshot of the closed room is deleted", func() bool { return !e.store.has(a) })
	if !e.store.has(b) {
		t.Error("the other room's snapshot is gone")
	}
	if _, ok := e.hub.Info(a); ok {
		t.Error("the closed room is still listed")
	}

	res, err = e.hub.CloseRoom(ctx, CloseTarget{Code: strings.ToLower(b), InstanceID: idB})
	if err != nil || res.Code != b {
		t.Errorf("CloseRoom by code and instance = %+v, %v", res, err)
	}
}

// ---------------------------------------------------------------------------
// The race: joins and reconnects against a block, under -race
// ---------------------------------------------------------------------------

// TestBlockRacingWithJoinsAndReconnectsNeverLeavesTheirPairSeated hammers the
// argument of policy.go: for every interleaving of "B joins/reconnects" and
// "A blocks B (policy update, then sweep)", once the sweep has returned (the
// moment the REST handler acknowledges the block) the pair does not share the
// room, and nobody can be admitted next to the other afterwards.
func TestBlockRacingWithJoinsAndReconnectsNeverLeavesTheirPairSeated(t *testing.T) {
	rounds := 150
	if testing.Short() {
		rounds = 30
	}
	for _, reconnect := range []bool{false, true} {
		t.Run(fmt.Sprint("reconnect=", reconnect), func(t *testing.T) {
			e, policy := policyEnv(t)
			for i := range rounds {
				a, b := fmt.Sprintf("a%d", i), fmt.Sprintf("b%d", i)
				code := e.create(a, "Ana")
				e.join(code, a, "Ana")
				if reconnect {
					// B is already seated, but disconnected: the race is a reconnect.
					_, s := e.join(code, b, "Beto")
					s.Disconnect()
					e.sync(code)
				}

				start := make(chan struct{})
				var (
					wg    sync.WaitGroup
					acked []string
				)
				wg.Add(2)
				go func() { // B arrives
					defer wg.Done()
					<-start
					s, err := e.hub.Connect(code, newFakeClient(b))
					if err != nil {
						return // the room closed under it: nothing was seated
					}
					raw, _ := json.Marshal(joinFrame(code, "Beto"))
					s.Handle(game.CmdJoin, raw)
				}()
				go func() { // A blocks B: policy update (after the DB write), then the sweep
					defer wg.Done()
					<-start
					policy.AddBlock(a, b)
					removed, err := e.hub.Sweep(context.Background())
					if err != nil {
						t.Errorf("Sweep: %v", err)
					}
					acked = removed[code]
				}()
				close(start)
				wg.Wait()
				if !roomLive(e, code) {
					continue
				}
				syncRoom(e.room(code)) // B's join frame, if any, has been handled
				seated := seatedIn(e, code)
				if slices.Contains(seated, a) && slices.Contains(seated, b) {
					t.Fatalf("round %d: the blocked pair shares the room after the block was acknowledged: %v (the sweep removed %v)", i, seated, acked)
				}

				// After the acknowledgement nobody gets in next to the other.
				for _, joiner := range []string{a, b} {
					other := map[string]string{a: b, b: a}[joiner]
					if !roomLive(e, code) || !slices.Contains(seatedIn(e, code), other) {
						continue // the other one is not here: joining is legitimate
					}
					c, s := e.connect(code, joiner)
					e.handle(code, s, game.CmdJoin, joinFrame(code, "again"))
					if got := errorCodes(c.drain()); !slices.Equal(got, []string{"join_unavailable"}) {
						t.Fatalf("round %d: %s was admitted next to %s after the acknowledgement (%v)", i, joiner, other, got)
					}
				}
			}
		})
	}
}

// TestManyConcurrentBlocksAndJoinsKeepTheInvariant mixes many pairs over many
// rooms at once and checks the invariant of every room at the end.
func TestManyConcurrentBlocksAndJoinsKeepTheInvariant(t *testing.T) {
	e, policy := policyEnv(t)
	const rooms, per = 12, 5
	codes := make([]string, rooms)
	for i := range rooms {
		codes[i] = e.create(fmt.Sprintf("host%d", i), "Host")
		e.join(codes[i], fmt.Sprintf("host%d", i), "Host")
	}
	var wg sync.WaitGroup
	for i := range rooms {
		for j := range per {
			wg.Add(1)
			go func() {
				defer wg.Done()
				id := fmt.Sprintf("p%d-%d", i, j)
				s, err := e.hub.Connect(codes[i], newFakeClient(id))
				if err != nil {
					return
				}
				raw, _ := json.Marshal(joinFrame(codes[i], "P"))
				s.Handle(game.CmdJoin, raw)
				if j%2 == 0 { // a reconnect on top
					s.Disconnect()
					if s2, err := e.hub.Connect(codes[i], newFakeClient(id)); err == nil {
						s2.Handle(game.CmdJoin, raw)
					}
				}
			}()
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := range per - 1 {
				policy.AddBlock(fmt.Sprintf("p%d-%d", i, j), fmt.Sprintf("p%d-%d", i, j+1))
				policy.AddBlock(fmt.Sprintf("host%d", i), fmt.Sprintf("p%d-%d", i, j))
				if _, err := e.hub.Sweep(context.Background()); err != nil {
					t.Errorf("Sweep: %v", err)
				}
			}
		}()
	}
	wg.Wait()
	if _, err := e.hub.Sweep(context.Background()); err != nil { // the last acknowledged change
		t.Fatal(err)
	}
	e.syncAll()
	for _, code := range codes {
		if !roomLive(e, code) {
			continue
		}
		seated := seatedIn(e, code)
		for _, a := range seated {
			for _, b := range seated {
				if a != b && policy.Conflict(a, b) {
					t.Errorf("room %s: %s and %s share it but are in conflict", code, a, b)
				}
			}
		}
	}
}
