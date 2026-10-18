package room

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/AresiusXP/tutti-frutti/server/internal/game"
	"github.com/AresiusXP/tutti-frutti/server/internal/protocol"
)

func TestJoinHappyPath(t *testing.T) {
	e := newEnv(t)
	code := e.create("host", "Hosty")
	e.clock.Advance(1500 * time.Millisecond)

	hc, hs := e.connect(code, "host")
	if got := hc.drain(); len(got) != 0 {
		t.Fatalf("frames before join: %v", kinds(got))
	}
	e.handle(code, hs, game.CmdJoin, protocol.JoinRequest{Code: code, Name: "Hosty", Avatar: testAvatar})

	frames := hc.drain()
	if got, want := kinds(frames), []string{"state"}; !slices.Equal(got, want) {
		t.Fatalf("host frames = %v, want %v", got, want)
	}
	if frames[0].V != protocol.Version {
		t.Errorf("frame version = %d, want %d", frames[0].V, protocol.Version)
	}
	st := frames[0].state(t)
	if st.Code != code {
		t.Errorf("state code = %q, want %q", st.Code, code)
	}
	if want := t0.Add(1500 * time.Millisecond).UnixMilli(); st.ServerTime != want {
		t.Errorf("serverTime = %d, want %d", st.ServerTime, want)
	}
	if st.View.YouID != "host" || st.View.HostID != "host" || st.View.Phase != game.PhaseLobby {
		t.Errorf("view = you %q host %q phase %s", st.View.YouID, st.View.HostID, st.View.Phase)
	}
	if p := playerView(t, st.View, "host"); !p.Connected || p.Name != "Hosty" {
		t.Errorf("host seat = %+v, want connected", p)
	}

	// A second player: the joiner and the host each get one state.
	gc, gs := e.connect(code, "guest")
	e.handle(code, gs, game.CmdJoin, protocol.JoinRequest{Code: code, Name: "Gus", Avatar: testAvatar})
	if got := kinds(gc.drain()); !slices.Equal(got, []string{"state"}) {
		t.Errorf("guest frames = %v", got)
	}
	hostFrames := hc.drain()
	if got := kinds(hostFrames); !slices.Equal(got, []string{"state"}) {
		t.Fatalf("host frames after the guest joined = %v", got)
	}
	hv := hostFrames[0].state(t).View
	if len(hv.Players) != 2 || !playerView(t, hv, "guest").Connected {
		t.Errorf("host view players = %+v", hv.Players)
	}
	if hv.YouID != "host" {
		t.Errorf("host got a view for %q", hv.YouID)
	}
}

func TestJoinAcceptsLowercaseCode(t *testing.T) {
	e := newEnv(t)
	code := e.create("h", "Host")
	c, s := e.connect(code, "h")
	e.handle(code, s, game.CmdJoin, protocol.JoinRequest{Code: strings.ToLower(code), Name: "Host", Avatar: testAvatar})
	if got := kinds(c.drain()); !slices.Equal(got, []string{"state"}) {
		t.Errorf("frames = %v", got)
	}
}

func TestFrameErrors(t *testing.T) {
	joinReq := func(code, name string) json.RawMessage {
		b, _ := json.Marshal(protocol.JoinRequest{Code: code, Name: name, Avatar: testAvatar})
		return b
	}
	tests := []struct {
		name   string
		joined bool
		typ    string
		d      func(code string) json.RawMessage
		want   string
	}{
		{"command before join", false, game.CmdSetAnswer, nil, protocol.ErrNotJoined},
		{"start before join", false, game.CmdStartGame, nil, protocol.ErrNotJoined},
		{"unknown type before join", false, "frobnicate", nil, protocol.ErrNotJoined},
		{"leave before join", false, game.CmdLeave, nil, protocol.ErrNotJoined},
		{"ping is answered by the transport", false, protocol.TypePing, nil, protocol.ErrNotJoined},
		{"join with another room's code", false, game.CmdJoin, func(string) json.RawMessage { return joinReq("ZZZZ", "Ana") }, protocol.ErrBadMessage},
		{"join without payload", false, game.CmdJoin, nil, protocol.ErrBadMessage},
		{"join with a string payload", false, game.CmdJoin, func(string) json.RawMessage { return json.RawMessage(`"hello"`) }, protocol.ErrBadMessage},
		{"join with broken json", false, game.CmdJoin, func(string) json.RawMessage { return json.RawMessage(`{"code":`) }, protocol.ErrBadMessage},
		{"join with an empty name", false, game.CmdJoin, func(c string) json.RawMessage { return joinReq(c, "") }, "invalid_name"},
		{"join with a long avatar", false, game.CmdJoin, func(c string) json.RawMessage {
			b, _ := json.Marshal(protocol.JoinRequest{Code: c, Name: "Ana", Avatar: game.Avatar{Emoji: strings.Repeat("x", 40)}})
			return b
		}, "invalid_avatar"},
		{"second join", true, game.CmdJoin, func(c string) json.RawMessage { return joinReq(c, "Ana") }, protocol.ErrAlreadyJoined},
		{"unknown type", true, "frobnicate", nil, protocol.ErrBadMessage},
		{"set_connected is not a wire command", true, "set_connected", func(string) json.RawMessage { return json.RawMessage(`{"connected":false}`) }, protocol.ErrBadMessage},
		{"ping reaching the room", true, protocol.TypePing, nil, protocol.ErrBadMessage},
		{"malformed payload", true, game.CmdSetAnswer, func(string) json.RawMessage { return json.RawMessage(`{"category":"zero"}`) }, protocol.ErrBadMessage},
		{"engine error: wrong phase", true, game.CmdBasta, nil, "wrong_phase"},
		{"engine error: invalid settings", true, game.CmdUpdateSettings, func(string) json.RawMessage {
			b, _ := json.Marshal(threeCats(func(s *game.Settings) { s.TimeLimitSeconds = 7 }))
			return b
		}, "invalid_settings"},
		{"engine error: unknown player to kick", true, game.CmdKick, func(string) json.RawMessage { return json.RawMessage(`{"playerId":"nobody"}`) }, "unknown_player"},
		{"engine error: kick yourself", true, game.CmdKick, func(string) json.RawMessage { return json.RawMessage(`{"playerId":"h"}`) }, "cannot_kick_self"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			e := newEnv(t)
			code := e.create("h", "Host")
			other, _ := e.join(code, "other", "Other")
			var c *fakeClient
			var s Session
			if tc.joined {
				c, s = e.join(code, "h", "Host")
			} else {
				c, s = e.connect(code, "newbie") // not seated: the engine validates its join
			}
			c.drain()
			other.drain()

			var d json.RawMessage
			if tc.d != nil {
				d = tc.d(code)
			}
			s.Handle(tc.typ, d)
			e.sync(code)

			frames := c.drain()
			if len(frames) != 1 || frames[0].T != protocol.TypeError {
				t.Fatalf("frames = %v, want one error", kinds(frames))
			}
			eb := frames[0].errBody(t)
			if eb.Code != tc.want || eb.Ref != tc.typ {
				t.Errorf("error = %+v, want code %q ref %q", eb, tc.want, tc.typ)
			}
			if got := other.drain(); len(got) != 0 {
				t.Errorf("the other player saw %v; errors go to the sender only", kinds(got))
			}
			if len(c.closeCodes()) != 0 {
				t.Errorf("the connection was closed: %v", c.closeCodes())
			}
		})
	}
}

func TestJoinFailureAllowsRetry(t *testing.T) {
	e := newEnv(t)
	code := e.create("h", "Host")
	c, s := e.connect(code, "newbie")
	e.handle(code, s, game.CmdJoin, protocol.JoinRequest{Code: code, Name: "", Avatar: testAvatar})
	if got := kinds(c.drain()); !slices.Equal(got, []string{"error:invalid_name"}) {
		t.Fatalf("frames = %v", got)
	}
	e.handle(code, s, game.CmdJoin, protocol.JoinRequest{Code: code, Name: "Newbie", Avatar: testAvatar})
	if got := kinds(c.drain()); !slices.Equal(got, []string{"state"}) {
		t.Errorf("frames after the retry = %v", got)
	}
}

func TestLobbyFull(t *testing.T) {
	e := newEnv(t)
	code := e.create("p0", "P")
	for i := 1; i < game.MaxPlayers; i++ {
		e.join(code, playerN(i), "P")
	}
	c, s := e.join(code, "late", "Late")
	frames := c.drain()
	if got := kinds(frames); !slices.Equal(got, []string{"error:lobby_full"}) {
		t.Fatalf("frames = %v", got)
	}
	// Still not joined.
	e.handle(code, s, game.CmdSetAnswer, game.SetAnswer{})
	if got := kinds(c.drain()); !slices.Equal(got, []string{"error:not_joined"}) {
		t.Errorf("frames = %v", got)
	}
}

func TestViewsAreFilteredPerViewer(t *testing.T) {
	l := newLobby3(t)
	l.startRound(nil)

	mine := []string{"Axolotl", "Arrecife", "Anaconda"}
	for cat, a := range mine {
		l.send(0, game.CmdSetAnswer, game.SetAnswer{Category: cat, Text: a})
	}
	l.send(1, game.CmdSetAnswer, game.SetAnswer{Category: 0, Text: "Abeja"})

	f1, f2, f3 := l.c[0].drain(), l.c[1].drain(), l.c[2].drain()
	for _, f := range [][]frameRec{f1, f2, f3} {
		if len(f) != 4 {
			t.Fatalf("each set_answer must broadcast one state to everyone: got %v", kinds(f))
		}
	}

	// p1 sees its own answers and nobody else's.
	v1 := lastState(t, f1).View
	if got := v1.Round.Answers["p1"]; !slices.Equal(got, mine) {
		t.Errorf("p1 sees its answers %v, want %v", got, mine)
	}
	if _, ok := v1.Round.Answers["p2"]; ok {
		t.Error("p1 sees p2's answers while playing")
	}
	// p2 and p3 never see p1's answers, on the wire either.
	for name, frames := range map[string][]frameRec{"p2": f2, "p3": f3} {
		for _, f := range frames {
			for _, secret := range mine {
				if strings.Contains(string(f.Raw), secret) {
					t.Fatalf("%s received %q in %s", name, secret, f.Raw)
				}
			}
		}
	}
	// ... but they see the fill progress.
	v2 := lastState(t, f2).View
	if v2.YouID != "p2" || v2.Round.Filled["p1"] != 3 || v2.Round.Filled["p2"] != 1 {
		t.Errorf("p2 view: you %q filled %v", v2.YouID, v2.Round.Filled)
	}
	if got := v2.Round.Answers["p2"]; len(got) == 0 || got[0] != "Abeja" {
		t.Errorf("p2 sees its own answers %v", got)
	}
	// p1 does not see p2's answer text either.
	for _, f := range f1 {
		if strings.Contains(string(f.Raw), "Abeja") {
			t.Fatalf("p1 received p2's answer in %s", f.Raw)
		}
	}

	// Calling basta moves to grace: still hidden. The review reveals them.
	l.send(0, game.CmdBasta, nil)
	for _, f := range l.c[1].drain() {
		if strings.Contains(string(f.Raw), "Axolotl") {
			t.Fatalf("grace leaked an answer: %s", f.Raw)
		}
	}
	l.drain()
	l.e.advance(game.GraceDuration + game.LateUpdateAllowance)
	rv := lastState(t, l.c[1].drain()).View
	if rv.Phase != game.PhaseReview {
		t.Fatalf("phase = %s", rv.Phase)
	}
	if got := rv.Round.Answers["p1"]; !slices.Equal(got, mine) {
		t.Errorf("review: p2 sees p1's answers %v, want %v", got, mine)
	}
}

func TestReconnectKeepsSeatAndAnswers(t *testing.T) {
	l := newLobby3(t)
	l.startRound(nil)
	l.send(1, game.CmdSetAnswer, game.SetAnswer{Category: 0, Text: "Almeja"})
	l.drain()

	l.s[1].Disconnect()
	l.e.sync(l.code)
	if got := kinds(l.c[1].drain()); len(got) != 0 {
		t.Errorf("a disconnected client received %v", got)
	}
	for _, i := range []int{0, 2} {
		frames := l.c[i].drain()
		if got := kinds(frames); !slices.Equal(got, []string{"state"}) {
			t.Fatalf("p%d frames = %v", i+1, got)
		}
		v := lastState(t, frames).View
		if p := playerView(t, v, "p2"); p.Connected {
			t.Errorf("p%d still sees p2 connected", i+1)
		}
		if len(v.Players) != 3 {
			t.Errorf("players = %d, want 3 (the seat is kept)", len(v.Players))
		}
	}
	seq := query(l.e, l.code, func(r *room) int { return r.g.Players[1].JoinedSeq })

	// Back with a new socket.
	c2, _ := l.e.join(l.code, "p2", "Beto")
	frames := c2.drain()
	if got := kinds(frames); !slices.Equal(got, []string{"state"}) {
		t.Fatalf("reconnect frames = %v", got)
	}
	v := lastState(t, frames).View
	p := playerView(t, v, "p2")
	if !p.Connected || p.JoinedSeq != seq {
		t.Errorf("seat after reconnect = %+v (joinedSeq was %d)", p, seq)
	}
	if len(v.Players) != 3 {
		t.Errorf("players = %d after reconnect, want 3 (no duplicate seat)", len(v.Players))
	}
	if got := v.Round.Answers["p2"]; len(got) == 0 || got[0] != "Almeja" {
		t.Errorf("answers after reconnect = %v, want them kept", got)
	}
	if v.Phase != game.PhasePlaying || v.Round.Letter != "A" {
		t.Errorf("reconnect view: phase %s letter %q", v.Phase, v.Round.Letter)
	}
	// The others saw the player come back.
	for _, i := range []int{0, 2} {
		v := lastState(t, l.c[i].drain()).View
		if !playerView(t, v, "p2").Connected {
			t.Errorf("p%d does not see p2 reconnected", i+1)
		}
	}
}

func TestReplacedConnection(t *testing.T) {
	l := newLobby3(t)

	// The same player opens a second socket.
	c1b, s1b := l.e.join(l.code, "p1", "Ana")

	old := l.c[0].drain()
	if got := kinds(old); !slices.Equal(got, []string{"event:replaced"}) {
		t.Fatalf("old connection frames = %v, want only the replaced event", got)
	}
	if got := l.c[0].closeCodes(); !slices.Equal(got, []int{protocol.CloseReplaced}) {
		t.Errorf("old connection closes = %v, want [%d]", got, protocol.CloseReplaced)
	}
	if got := kinds(c1b.drain()); !slices.Equal(got, []string{"state"}) {
		t.Errorf("new connection frames = %v", got)
	}
	// The others see a state with p1 still connected (no flicker).
	for _, i := range []int{1, 2} {
		frames := l.c[i].drain()
		if got := kinds(frames); !slices.Equal(got, []string{"state"}) {
			t.Fatalf("p%d frames = %v", i+1, got)
		}
		if !playerView(t, lastState(t, frames).View, "p1").Connected {
			t.Errorf("p%d sees p1 disconnected after the replacement", i+1)
		}
	}
	if st := l.e.hub.Stats(); st.Connections != 3 {
		t.Errorf("connections = %d, want 3", st.Connections)
	}

	// The old socket's late Disconnect and frames change nothing.
	l.s[0].Disconnect()
	l.s[0].Handle(game.CmdUpdateSettings, mustJSON(t, threeCats(nil)))
	l.e.sync(l.code)
	for i, c := range []*fakeClient{l.c[0], c1b, l.c[1], l.c[2]} {
		if got := c.drain(); len(got) != 0 {
			t.Errorf("client %d received %v after the stale Disconnect", i, kinds(got))
		}
	}
	connected := query(l.e, l.code, func(r *room) bool { return r.g.Players[0].Connected })
	if !connected {
		t.Error("the stale Disconnect marked the replaced seat disconnected")
	}
	if st := l.e.hub.Stats(); st.Connections != 3 {
		t.Errorf("connections after the stale Disconnect = %d, want 3", st.Connections)
	}

	// The live one still disconnects the seat.
	s1b.Disconnect()
	l.e.sync(l.code)
	if v := lastState(t, l.c[1].drain()).View; playerView(t, v, "p1").Connected {
		t.Error("p1 still connected after its live socket dropped")
	}
}

func TestReplacedWhileInGameKeepsAnswers(t *testing.T) {
	l := newLobby3(t)
	l.startRound(nil)
	l.send(0, game.CmdSetAnswer, game.SetAnswer{Category: 0, Text: "Aji"})
	l.drain()
	c, _ := l.e.join(l.code, "p1", "Ana")
	v := lastState(t, c.drain()).View
	if got := v.Round.Answers["p1"]; len(got) == 0 || got[0] != "Aji" {
		t.Errorf("answers = %v", got)
	}
}

func TestKick(t *testing.T) {
	l := newLobby3(t)
	l.send(0, game.CmdKick, game.Kick{PlayerID: "p2"})

	if got, want := kinds(l.c[1].drain()), []string{"event:kicked"}; !slices.Equal(got, want) {
		t.Errorf("kicked player frames = %v, want %v", got, want)
	}
	if got := l.c[1].closeCodes(); !slices.Equal(got, []int{protocol.CloseKicked}) {
		t.Errorf("kicked player closes = %v, want [%d]", got, protocol.CloseKicked)
	}
	for _, i := range []int{0, 2} {
		frames := l.c[i].drain()
		if got, want := kinds(frames), []string{"event:kicked", "state"}; !slices.Equal(got, want) {
			t.Fatalf("p%d frames = %v, want %v", i+1, got, want)
		}
		if ev := frames[0].event(t); ev.PlayerID != "p2" {
			t.Errorf("kicked event = %+v", ev)
		}
		if v := lastState(t, frames).View; len(v.Players) != 2 {
			t.Errorf("players after the kick = %d, want 2 (lobby seat removed)", len(v.Players))
		}
		if len(l.c[i].closeCodes()) != 0 {
			t.Errorf("p%d was closed", i+1)
		}
	}

	// The kicked socket is gone for good.
	l.s[1].Handle(game.CmdLeave, nil)
	l.s[1].Disconnect()
	l.e.sync(l.code)
	if got := l.c[0].drain(); len(got) != 0 {
		t.Errorf("stale frames of a kicked player reached the room: %v", kinds(got))
	}
	// And so is the player.
	again, _ := l.e.join(l.code, "p2", "Beto")
	if got := kinds(again.drain()); !slices.Equal(got, []string{"error:kicked"}) {
		t.Errorf("a kicked player joining again got %v", got)
	}
	if info, _ := l.e.hub.Info(l.code); info.Players != 2 {
		t.Errorf("Info.Players = %d, want 2", info.Players)
	}
}

func TestKickDuringGame(t *testing.T) {
	l := newLobby3(t)
	l.startRound(nil)
	l.send(0, game.CmdKick, game.Kick{PlayerID: "p3"})

	if got := l.c[2].closeCodes(); !slices.Equal(got, []int{protocol.CloseKicked}) {
		t.Errorf("closes = %v", got)
	}
	frames := l.c[1].drain()
	if got, want := kinds(frames), []string{"event:kicked", "state"}; !slices.Equal(got, want) {
		t.Fatalf("frames = %v, want %v", got, want)
	}
	v := lastState(t, frames).View
	if p := playerView(t, v, "p3"); !p.Left || p.Connected {
		t.Errorf("kicked seat = %+v, want left and disconnected", p)
	}
	if v.Phase != game.PhasePlaying {
		t.Errorf("phase = %s", v.Phase)
	}
}

func TestLeaveInLobby(t *testing.T) {
	l := newLobby3(t)
	l.send(2, game.CmdLeave, nil)

	if got := l.c[2].closeRecs(); len(got) != 1 || got[0].code != protocol.CloseLeft {
		t.Errorf("leaver closes = %v, want one %d", got, protocol.CloseLeft)
	}
	if got := l.c[2].drain(); len(got) != 0 {
		t.Errorf("the leaver received %v", kinds(got))
	}
	for _, i := range []int{0, 1} {
		frames := l.c[i].drain()
		if got := kinds(frames); !slices.Equal(got, []string{"state"}) {
			t.Fatalf("p%d frames = %v", i+1, got)
		}
		if v := lastState(t, frames).View; len(v.Players) != 2 {
			t.Errorf("players = %d, want 2", len(v.Players))
		}
	}
	if st := l.e.hub.Stats(); st.Connections != 2 {
		t.Errorf("connections = %d, want 2", st.Connections)
	}
}

func TestHostLeavesHandsOverTheRoom(t *testing.T) {
	l := newLobby3(t)
	l.send(0, game.CmdLeave, nil)

	frames := l.c[1].drain()
	if got, want := kinds(frames), []string{"event:host_changed", "state"}; !slices.Equal(got, want) {
		t.Fatalf("frames = %v, want %v", got, want)
	}
	if ev := frames[0].event(t); ev.PlayerID != "p2" {
		t.Errorf("new host = %q, want p2", ev.PlayerID)
	}
	if v := lastState(t, frames).View; v.HostID != "p2" {
		t.Errorf("view host = %q", v.HostID)
	}
	if got := l.c[0].closeCodes(); !slices.Equal(got, []int{protocol.CloseLeft}) {
		t.Errorf("closes = %v", got)
	}
}

func TestLeaveDuringGame(t *testing.T) {
	l := newLobby3(t)
	l.startRound(nil)
	l.send(2, game.CmdLeave, nil)

	frames := l.c[0].drain()
	if got := kinds(frames); !slices.Equal(got, []string{"state"}) {
		t.Fatalf("frames = %v", got)
	}
	v := lastState(t, frames).View
	if p := playerView(t, v, "p3"); !p.Left {
		t.Errorf("p3 seat = %+v, want Left", p)
	}
	if _, ok := l.e.hub.Info(l.code); !ok {
		t.Error("the room closed although two players are left")
	}
	// The player may come back and reclaim the seat.
	c, _ := l.e.join(l.code, "p3", "Cami")
	if v := lastState(t, c.drain()).View; playerView(t, v, "p3").Left || !playerView(t, v, "p3").Connected {
		t.Errorf("reclaimed seat = %+v", playerView(t, v, "p3"))
	}
}

func TestEveryoneLeavingClosesTheRoom(t *testing.T) {
	e := newEnv(t)
	code := e.create("h", "Host")
	c, s := e.join(code, "h", "Host")
	spectator, _ := e.connect(code, "ghost") // attached, never joined
	e.waitSaved(code)
	if !e.store.has(code) {
		t.Fatal("setup: no snapshot")
	}

	e.handle(code, s, game.CmdLeave, nil)

	if got := c.closeCodes(); !slices.Equal(got, []int{protocol.CloseLeft}) {
		t.Errorf("leaver closes = %v", got)
	}
	if got := spectator.closeCodes(); !slices.Equal(got, []int{protocol.CloseRoomClosed}) {
		t.Errorf("attached client closes = %v, want [%d]", got, protocol.CloseRoomClosed)
	}
	if _, ok := e.hub.Info(code); ok {
		t.Error("the room is still registered")
	}
	eventually(t, "the snapshot to be deleted", func() bool { return !e.store.has(code) })
	if st := e.hub.Stats(); st != (Stats{}) {
		t.Errorf("Stats = %+v, want empty", st)
	}
}

func TestSlowClientIsClosedAndOthersAreUnaffected(t *testing.T) {
	l := newLobby3(t)
	l.c[1].setRefuse(true)

	l.send(0, game.CmdUpdateSettings, threeCats(nil))

	if got := l.c[1].closeRecs(); len(got) != 1 || got[0].code != protocol.ClosePolicy {
		t.Fatalf("slow client closes = %v, want one %d", got, protocol.ClosePolicy)
	}
	for _, i := range []int{0, 2} {
		frames := l.c[i].drain()
		if got, want := kinds(frames), []string{"state", "state"}; !slices.Equal(got, want) {
			t.Fatalf("p%d frames = %v, want the settings state then the disconnect state", i+1, got)
		}
		if len(frames[0].state(t).View.Settings.Categories) != 3 {
			t.Errorf("p%d did not get the new settings", i+1)
		}
		if playerView(t, frames[1].state(t).View, "p2").Connected {
			t.Errorf("p%d still sees the slow client connected", i+1)
		}
		if len(l.c[i].closeCodes()) != 0 {
			t.Errorf("p%d was closed", i+1)
		}
	}
	if st := l.e.hub.Stats(); st.Connections != 2 {
		t.Errorf("connections = %d, want 2", st.Connections)
	}

	// The transport's own Disconnect afterwards is a no-op.
	l.s[1].Disconnect()
	l.e.sync(l.code)
	if got := l.c[0].drain(); len(got) != 0 {
		t.Errorf("p1 received %v after the slow client's Disconnect", kinds(got))
	}
	// The seat is kept and the player can come back with a fresh state.
	c, _ := l.e.join(l.code, "p2", "Beto")
	if v := lastState(t, c.drain()).View; !playerView(t, v, "p2").Connected || len(v.Settings.Categories) != 3 {
		t.Errorf("reconnect after the slow close: %+v", playerView(t, v, "p2"))
	}
}

func TestSlowClientOnErrorFrame(t *testing.T) {
	l := newLobby3(t)
	l.c[1].setRefuse(true)
	l.send(1, game.CmdBasta, nil) // wrong phase: the error cannot be delivered
	if got := l.c[1].closeCodes(); !slices.Equal(got, []int{protocol.ClosePolicy}) {
		t.Errorf("closes = %v", got)
	}
	if v := lastState(t, l.c[0].drain()).View; playerView(t, v, "p2").Connected {
		t.Error("the slow client is still connected")
	}
}

func TestSlowClientDuringShutdownEvent(t *testing.T) {
	l := newLobby3(t)
	l.c[1].setRefuse(true)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := l.e.hub.Shutdown(ctx); err != nil {
		t.Fatal(err)
	}
	if got := l.c[1].closeCodes(); !slices.Equal(got, []int{protocol.ClosePolicy}) {
		t.Errorf("slow client closes = %v, want only the policy close", got)
	}
	if got := l.c[0].closeCodes(); !slices.Equal(got, []int{protocol.CloseServiceRestart}) {
		t.Errorf("healthy client closes = %v", got)
	}
}

func TestPanicClosesOnlyTheBrokenRoom(t *testing.T) {
	e := newEnv(t)
	bad := e.create("a1", "A")
	good := e.create("b1", "B")
	a1, s1 := e.join(bad, "a1", "A")
	a2, _ := e.join(bad, "a2", "A2")
	b1, sb := e.join(good, "b1", "B")
	e.waitSaved(bad)
	a1.drain()
	a2.drain()
	b1.drain()

	a2.mu.Lock()
	a2.panicSend = true
	a2.mu.Unlock()
	badRoom := e.room(bad)
	s1.Handle(game.CmdUpdateSettings, mustJSON(t, threeCats(nil))) // the broadcast reaches a2: panics
	syncRoom(badRoom)

	if got := a1.closeCodes(); !slices.Equal(got, []int{protocol.CloseRoomClosed}) {
		t.Errorf("a1 closes = %v, want [%d]", got, protocol.CloseRoomClosed)
	}
	if _, ok := e.hub.Info(bad); ok {
		t.Error("the broken room is still registered")
	}
	eventually(t, "the broken room's snapshot to be deleted", func() bool { return !e.store.has(bad) })
	if !strings.Contains(e.logs.String(), "room panicked") {
		t.Error("the panic was not logged")
	}

	// The other room carries on.
	e.handle(good, sb, game.CmdUpdateSettings, threeCats(nil))
	if got := kinds(b1.drain()); !slices.Equal(got, []string{"state"}) {
		t.Errorf("healthy room frames = %v", got)
	}
	if st := e.hub.Stats(); st.Rooms != 1 || st.Connections != 1 {
		t.Errorf("Stats = %+v, want 1 room, 1 connection", st)
	}
}

func TestMailboxOverflowClosesTheClient(t *testing.T) {
	e := newEnv(t)
	code := e.create("h", "Host")
	c, s := e.join(code, "h", "Host")
	c.drain()

	r := e.room(code)
	gate := make(chan struct{})
	entered := make(chan struct{})
	r.post(func(*room) { close(entered); <-gate }, nil) // the room is busy
	<-entered

	for range maxPendingFrames + 10 {
		s.Handle(game.CmdReviewNav, nil)
	}
	if got := c.closeRecs(); len(got) == 0 || got[0].code != protocol.ClosePolicy {
		t.Fatalf("closes = %v, want a policy close for the flood", got)
	}
	close(gate)
	e.sync(code)
	if got := len(c.drain()); got != maxPendingFrames {
		t.Errorf("frames handled = %d, want the %d that fitted", got, maxPendingFrames)
	}
}

func TestFramesAfterTheRoomClosedAreDropped(t *testing.T) {
	e := newEnv(t)
	code := e.create("h", "Host")
	c, s := e.join(code, "h", "Host")
	r := e.room(code)
	r.post(func(r *room) { r.closeRoom(protocol.CloseRoomClosed, "test", true) }, nil)
	eventually(t, "the room to stop", func() bool {
		_, ok := e.hub.Info(code)
		return !ok
	})
	eventually(t, "the room goroutine to stop", func() bool {
		return r.mb.push(task{run: func(*room) {}}, 0) == pushClosed
	})
	c.drain()
	s.Handle(game.CmdUpdateSettings, mustJSON(t, threeCats(nil)))
	s.Disconnect()
	s.Disconnect()
	if got := c.drain(); len(got) != 0 {
		t.Errorf("frames after close: %v", kinds(got))
	}
	if got := c.closeCodes(); !slices.Equal(got, []int{protocol.CloseRoomClosed}) {
		t.Errorf("closes = %v", got)
	}
}

func mustJSON(t testing.TB, v any) json.RawMessage {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// ---- idle rooms ----

func TestUnclaimedRoomIsClosed(t *testing.T) {
	e := newEnv(t, func(c *Config) { c.UnclaimedTTL = time.Minute; c.IdleTTL = 10 * time.Minute })
	code := e.create("h", "Host")
	e.waitSaved(code)
	if !e.store.has(code) {
		t.Fatal("setup: the new room has no snapshot")
	}
	if n := e.clock.Pending(); n != 1 {
		t.Fatalf("pending timers = %d, want just the unclaimed one", n)
	}

	e.advance(59 * time.Second)
	if _, ok := e.hub.Info(code); !ok {
		t.Fatal("closed too early")
	}
	e.advance(time.Second)
	if _, ok := e.hub.Info(code); ok {
		t.Fatal("an unclaimed room outlived UnclaimedTTL")
	}
	eventually(t, "the snapshot to be deleted", func() bool { return !e.store.has(code) })
	if st := e.hub.Stats(); st != (Stats{}) {
		t.Errorf("Stats = %+v", st)
	}
	if e.clock.Pending() != 0 {
		t.Errorf("pending timers = %d after the room closed", e.clock.Pending())
	}
	if _, err := e.hub.Connect(code, newFakeClient("h")); err == nil {
		t.Error("Connect to a closed room succeeded")
	}
}

func TestJoiningClaimsTheRoom(t *testing.T) {
	e := newEnv(t, func(c *Config) { c.UnclaimedTTL = time.Minute; c.IdleTTL = 10 * time.Minute })
	code := e.create("h", "Host")
	e.advance(30 * time.Second)
	_, s := e.join(code, "h", "Host")
	e.advance(5 * time.Minute) // far past UnclaimedTTL: a player is in
	if _, ok := e.hub.Info(code); !ok {
		t.Fatal("a room with a player in it was closed")
	}
	s.Disconnect()
	e.sync(code)
	// From now on IdleTTL applies, counted from the disconnect.
	e.advance(10*time.Minute - time.Second)
	if _, ok := e.hub.Info(code); !ok {
		t.Fatal("closed before IdleTTL")
	}
	e.advance(time.Second)
	if _, ok := e.hub.Info(code); ok {
		t.Fatal("a claimed room outlived IdleTTL")
	}
}

func TestIdleRoomIsClosedAfterTheLastClientLeft(t *testing.T) {
	e := newEnv(t, func(c *Config) { c.IdleTTL = 10 * time.Minute })
	code := e.create("h", "Host")
	_, s1 := e.join(code, "h", "Host")
	_, s2 := e.join(code, "g", "Guest")
	e.waitSaved(code)

	s1.Disconnect()
	e.sync(code)
	e.advance(24 * time.Hour) // one player is still connected
	if _, ok := e.hub.Info(code); !ok {
		t.Fatal("a room with a connected player was closed")
	}
	// Still a single timer, and not the idle one while somebody is in.
	if n := e.clock.Pending(); n != 1 {
		t.Errorf("pending timers = %d, want 1", n)
	}
	if got := query(e, code, func(r *room) time.Time { return r.wakeAt }); got.Before(e.clock.Now()) {
		t.Errorf("the room is waiting for %v, which is in the past", got)
	}

	s2.Disconnect()
	e.sync(code)
	if n := e.clock.Pending(); n != 1 {
		t.Fatalf("pending timers = %d, want the idle timer", n)
	}
	e.advance(9 * time.Minute)
	// Somebody comes back and leaves again: the clock restarts.
	_, s3 := e.join(code, "h", "Host")
	s3.Disconnect()
	e.sync(code)
	e.advance(9 * time.Minute)
	if _, ok := e.hub.Info(code); !ok {
		t.Fatal("the idle clock did not restart on a connection")
	}
	e.advance(time.Minute)
	if _, ok := e.hub.Info(code); ok {
		t.Fatal("the idle room was not closed")
	}
	eventually(t, "the snapshot to be deleted", func() bool { return !e.store.has(code) })
}

func TestIdleRoomKeepsPlayingByItself(t *testing.T) {
	// Nobody is connected, but the game keeps its clock (the chooser's
	// auto-stop) until the idle TTL closes the room.
	e := newEnv(t, func(c *Config) { c.IdleTTL = time.Hour })
	l := &lobby3{e: e, code: e.create("p1", "Ana")}
	for i, name := range []string{"Ana", "Beto"} {
		l.c[i], l.s[i] = e.join(l.code, fmt.Sprintf("p%d", i+1), name)
	}
	l.send(0, game.CmdStartGame, nil)
	l.s[0].Disconnect()
	l.s[1].Disconnect()
	e.sync(l.code)

	e.advance(game.ChooserTimeout)
	g := query(e, l.code, func(r *room) game.Phase { return r.g.Phase })
	if g != game.PhasePlaying {
		t.Fatalf("phase = %s, want the auto-stop to have run", g)
	}
	e.advance(time.Hour)
	if _, ok := e.hub.Info(l.code); ok {
		t.Error("the idle room was not closed")
	}
}

// ---- timers ----

func TestSingleTimerIsReplacedAfterEveryChange(t *testing.T) {
	l := newLobby3(t)
	wakeAt := func() time.Time { return query(l.e, l.code, func(r *room) time.Time { return r.wakeAt }) }
	pending := func() int { return l.e.clock.Pending() }

	// A lobby has nothing but the heartbeat snapshot to wait for.
	heartbeat := func() time.Time {
		return query(l.e, l.code, func(r *room) time.Time { return r.lastSaved.Add(r.h.heartbeat) })
	}
	if pending() != 1 || !wakeAt().Equal(heartbeat()) {
		t.Fatalf("lobby with clients: %d timers, wake at %v; want 1 at the heartbeat %v", pending(), wakeAt(), heartbeat())
	}
	l.send(0, game.CmdUpdateSettings, threeCats(func(s *game.Settings) { s.TimeLimitSeconds = 60 }))
	if pending() != 1 || !wakeAt().Equal(heartbeat()) {
		t.Fatalf("after update_settings: %d timers, wake at %v; want 1 at the (moved) heartbeat %v", pending(), wakeAt(), heartbeat())
	}

	l.send(0, game.CmdStartGame, nil)
	if pending() != 1 || !wakeAt().Equal(t0.Add(game.ChooserTimeout)) {
		t.Fatalf("letter phase: %d timers, wake at %v; want 1 at +10s", pending(), wakeAt().Sub(t0))
	}

	l.send(0, game.CmdStopLetter, nil) // letter A at +0s; the limit ends at +60s
	if pending() != 1 {
		t.Fatalf("playing: %d timers, want 1", pending())
	}
	// The earliest of the periodic snapshot (+5s) and the time limit (+60s).
	if want := t0.Add(defaultSnapshotEvery); !wakeAt().Equal(want) {
		t.Errorf("playing: wake at %v, want %v (periodic snapshot)", wakeAt().Sub(t0), want.Sub(t0))
	}

	// The periodic timer re-arms itself and stays single.
	l.e.advance(defaultSnapshotEvery)
	if pending() != 1 || !wakeAt().Equal(t0.Add(2*defaultSnapshotEvery)) {
		t.Errorf("after a periodic fire: %d timers, wake at %v", pending(), wakeAt().Sub(t0))
	}

	// Filling the answers calls basta: grace replaces the timer.
	for cat, a := range []string{"Ana", "Argentina", "Aguila"} {
		l.send(0, game.CmdSetAnswer, game.SetAnswer{Category: cat, Text: a})
	}
	now := l.e.clock.Now()
	l.send(0, game.CmdBasta, nil)
	wantGrace := now.Add(game.GraceDuration + game.LateUpdateAllowance)
	if pending() != 1 || !wakeAt().Equal(wantGrace) {
		t.Errorf("grace: %d timers, wake at %v, want 1 at %v", pending(), wakeAt().Sub(t0), wantGrace.Sub(t0))
	}

	l.e.advance(game.GraceDuration + game.LateUpdateAllowance)
	phase := query(l.e, l.code, func(r *room) game.Phase { return r.g.Phase })
	if phase != game.PhaseReview {
		t.Fatalf("phase = %s, want review", phase)
	}
	// Review has no deadline and nothing to snapshot: only the heartbeat.
	if pending() != 1 || !wakeAt().Equal(heartbeat()) {
		t.Errorf("review: %d timers, wake at %v; want 1 at the heartbeat %v", pending(), wakeAt(), heartbeat())
	}
}

func TestChooserAutoStop(t *testing.T) {
	l := newLobby3(t)
	l.send(0, game.CmdStartGame, nil)
	l.drain()

	l.e.advance(game.ChooserTimeout - time.Millisecond)
	for i, c := range l.c {
		if got := c.drain(); len(got) != 0 {
			t.Fatalf("p%d got %v before the deadline", i+1, kinds(got))
		}
	}
	l.e.advance(time.Millisecond)

	want := game.LetterAt(game.AvailableLetters(game.LangES, true, nil), game.ChooserTimeout)
	for i, c := range l.c {
		frames := c.drain()
		if got, wantKinds := kinds(frames), []string{"event:letter_revealed", "state"}; !slices.Equal(got, wantKinds) {
			t.Fatalf("p%d frames = %v, want %v", i+1, got, wantKinds)
		}
		if ev := frames[0].event(t); ev.Letter != want || ev.Round != 1 {
			t.Errorf("event = %+v, want letter %s of round 1", ev, want)
		}
		if v := frames[1].state(t).View; v.Phase != game.PhasePlaying || v.Round.Letter != want {
			t.Errorf("state: phase %s letter %q", v.Phase, v.Round.Letter)
		}
	}
}

func TestTimeLimitExpiry(t *testing.T) {
	l := newLobby3(t)
	l.startRound(func(s *game.Settings) { s.TimeLimitSeconds = 60 })
	l.send(0, game.CmdSetAnswer, game.SetAnswer{Category: 0, Text: "Ana"})
	l.drain()

	l.e.advance(60*time.Second - time.Millisecond)
	if got := l.c[1].drain(); len(got) != 0 {
		t.Fatalf("frames before the limit: %v", kinds(got))
	}
	l.e.advance(time.Millisecond)
	for i, c := range l.c {
		frames := c.drain()
		if got, want := kinds(frames), []string{"event:time_up", "state"}; !slices.Equal(got, want) {
			t.Fatalf("p%d frames = %v, want %v", i+1, got, want)
		}
		v := frames[1].state(t).View
		if v.Phase != game.PhaseGrace || v.Round.CallerID != "" {
			t.Errorf("p%d: phase %s caller %q; want grace without a caller", i+1, v.Phase, v.Round.CallerID)
		}
	}
	l.e.advance(game.LateUpdateAllowance)
	for i, c := range l.c {
		frames := c.drain()
		if got, want := kinds(frames), []string{"event:round_locked", "state"}; !slices.Equal(got, want) {
			t.Fatalf("p%d frames = %v, want %v", i+1, got, want)
		}
		if v := frames[1].state(t).View; v.Phase != game.PhaseReview {
			t.Errorf("phase = %s, want review", v.Phase)
		}
	}
}

// A tick that arrives very late catches up in one go (engine guarantee), and
// the room reports every transition.
func TestLateTickCatchesUp(t *testing.T) {
	l := newLobby3(t)
	l.startRound(func(s *game.Settings) { s.TimeLimitSeconds = 60 })

	// Simulate a timer that fires an hour late: the room's timer is cancelled,
	// the clock jumps, and the room re-arms for a deadline that is long past.
	query(l.e, l.code, func(r *room) struct{} { r.stopWake(); return struct{}{} })
	l.e.clock.Set(t0.Add(time.Hour))
	query(l.e, l.code, func(r *room) struct{} { r.rearm(); return struct{}{} })
	l.e.advance(0) // fires the overdue timer

	frames := l.c[1].drain()
	var got []string
	for _, k := range kinds(frames) {
		if k != "state" {
			got = append(got, k)
		}
	}
	if want := []string{"event:time_up", "event:round_locked"}; !slices.Equal(got, want) {
		t.Fatalf("events = %v, want %v", got, want)
	}
	if v := lastState(t, frames).View; v.Phase != game.PhaseReview {
		t.Errorf("phase = %s", v.Phase)
	}
}

func TestBastaRejectedForIncompleteAnswersOnlyAnswersTheCaller(t *testing.T) {
	l := newLobby3(t)
	l.startRound(nil)
	l.send(1, game.CmdSetAnswer, game.SetAnswer{Category: 0, Text: "Abeja"})
	l.drain()
	l.send(1, game.CmdBasta, nil)
	if got := kinds(l.c[1].drain()); !slices.Equal(got, []string{"error:basta_not_allowed"}) {
		t.Errorf("caller frames = %v", got)
	}
	for _, i := range []int{0, 2} {
		if got := l.c[i].drain(); len(got) != 0 {
			t.Errorf("p%d received %v", i+1, kinds(got))
		}
	}
}
