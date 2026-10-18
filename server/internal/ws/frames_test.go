package ws

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/AresiusXP/tutti-frutti/server/internal/protocol"
	"github.com/AresiusXP/tutti-frutti/server/internal/room"
)

func TestPingBeforeAndAfterJoin(t *testing.T) {
	e := newEnv(t, nil)
	c := e.dial()

	send(t, c, "ping", "")
	pong := read(t, c)
	if pong.T != protocol.TypePong || pong.V != protocol.Version {
		t.Fatalf("got %+v, want a v1 pong", pong)
	}
	var body protocol.Pong
	if err := json.Unmarshal(pong.D, &body); err != nil {
		t.Fatal(err)
	}
	if body.ServerTime != fixedNow.UnixMilli() {
		t.Errorf("serverTime = %d, want %d", body.ServerTime, fixedNow.UnixMilli())
	}
	if calls := e.rooms.connectCalls(); len(calls) != 0 {
		t.Errorf("ping reached the hub: %v", calls)
	}

	send(t, c, "join", `{"code":"KT7P","name":"Ana","avatar":{"emoji":"🍓","color":"#f06"}}`)
	sess := e.rooms.session(t, 0)
	send(t, c, "ping", `{}`)
	if got := read(t, c); got.T != protocol.TypePong {
		t.Fatalf("got %q after join, want pong", got.T)
	}
	for _, f := range sess.handled() {
		if f.T == protocol.TypePing {
			t.Error("ping was routed to the room")
		}
	}
}

func TestVersionMismatch(t *testing.T) {
	tests := []struct {
		name  string
		frame string
		ref   string
	}{
		{"newer version", `{"t":"ping","v":2}`, "ping"},
		{"older version", `{"t":"join","v":0,"d":{"code":"KT7P"}}`, "join"},
		{"missing version", `{"t":"ping"}`, "ping"},
		{"negative", `{"t":"set_answer","v":-1,"d":{}}`, "set_answer"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := newEnv(t, nil)
			c := e.dial()
			sendRaw(t, c, tt.frame)
			frames := expectCloseCode(t, c, protocol.CloseUnsupportedVersion)
			if len(frames) != 1 {
				t.Fatalf("got %d frames before the close, want the error frame", len(frames))
			}
			if got := errorBody(t, frames[0]); got.Code != protocol.ErrUnsupportedVersion || got.Ref != tt.ref {
				t.Errorf("error = %+v", got)
			}
			if calls := e.rooms.connectCalls(); len(calls) != 0 {
				t.Errorf("a stale client reached the hub: %v", calls)
			}
		})
	}
}

func TestVersionMismatchAfterJoin(t *testing.T) {
	e := newEnv(t, nil)
	c, sess := e.dialJoined()
	sendRaw(t, c, `{"t":"set_answer","v":9,"d":{}}`)
	expectCloseCode(t, c, protocol.CloseUnsupportedVersion)
	eventually(t, "disconnect", func() bool { return sess.disconnects.Load() == 1 })
	for _, f := range sess.handled() {
		if f.T == "set_answer" {
			t.Error("a stale frame was routed to the room")
		}
	}
}

func TestFirstFrameRules(t *testing.T) {
	e := newEnv(t, nil)
	c := e.dial()

	send(t, c, "set_answer", `{"category":0,"text":"Ana"}`)
	expectError(t, c, protocol.ErrNotJoined, "set_answer")
	send(t, c, "basta", "")
	expectError(t, c, protocol.ErrNotJoined, "basta")
	send(t, c, "who_knows", "")
	expectError(t, c, protocol.ErrNotJoined, "who_knows")
	if calls := e.rooms.connectCalls(); len(calls) != 0 {
		t.Fatalf("commands before join reached the hub: %v", calls)
	}

	// The connection is still usable: join now.
	send(t, c, "join", `{"code":"KT7P","name":"Ana","avatar":{"emoji":"🍓","color":"#f06"}}`)
	sess := e.rooms.session(t, 0)
	if calls := e.rooms.connectCalls(); len(calls) != 1 || calls[0] != "KT7P" {
		t.Errorf("Connect calls = %v", calls)
	}
	eventually(t, "join to be handled", func() bool { return len(sess.handled()) == 1 })
}

func TestJoinThenCommandsAreRoutedWithRawPayload(t *testing.T) {
	e := newEnv(t, nil)
	c, sess := e.dialJoined()

	cmds := []struct{ t, d string }{
		{"update_settings", `{"settings":{"rounds":3,"unknown_to_us":[1,2,{"x":null}]}}`},
		{"set_answer", `{"category":2,"text":"  Ñandú  "}`},
		{"basta", ``},
		{"vote_reject", `{"category":1,"targetPlayer":"p2","withdraw":true}`},
		{"not_a_command", `[1,2,3]`},
		{"join", `{"code":"KT7P","name":"Again","avatar":{"emoji":"x","color":"y"}}`},
		{"leave", `{}`},
	}
	for _, cmd := range cmds {
		send(t, c, cmd.t, cmd.d)
	}
	eventually(t, "all frames to be handled", func() bool { return len(sess.handled()) == 1+len(cmds) })

	got := sess.handled()
	if got[0].T != "join" || !strings.Contains(got[0].D, `"name":"Ana"`) {
		t.Errorf("first handled frame = %+v, want the raw join", got[0])
	}
	for i, cmd := range cmds {
		if got[i+1].T != cmd.t || got[i+1].D != cmd.d {
			t.Errorf("frame %d = %+v, want t=%q d=%q (order and payload bytes preserved)", i+1, got[i+1], cmd.t, cmd.d)
		}
	}
	if calls := e.rooms.connectCalls(); len(calls) != 1 {
		t.Errorf("Connect called %d times, want once: %v", len(calls), calls)
	}
}

func TestJoinCodeIsNormalisedForTheHubButPayloadIsUntouched(t *testing.T) {
	e := newEnv(t, nil)
	c := e.dial()
	raw := `{"code":" kt7p ","name":"Ana","avatar":{"emoji":"x","color":"y"}}`
	send(t, c, "join", raw)
	sess := e.rooms.session(t, 0)
	if calls := e.rooms.connectCalls(); len(calls) != 1 || calls[0] != "KT7P" {
		t.Errorf("Connect calls = %v, want [KT7P]", calls)
	}
	eventually(t, "join", func() bool { return len(sess.handled()) == 1 })
	if got := sess.handled()[0].D; got != raw {
		t.Errorf("payload = %q, want the raw %q", got, raw)
	}
}

func TestJoinWithBadPayload(t *testing.T) {
	tests := []struct {
		name  string
		frame string
	}{
		{"missing d", `{"t":"join","v":1}`},
		{"d is a number", `{"t":"join","v":1,"d":5}`},
		{"d is a string", `{"t":"join","v":1,"d":"KT7P"}`},
		{"d is an array", `{"t":"join","v":1,"d":[]}`},
		{"code is a number", `{"t":"join","v":1,"d":{"code":5}}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := newEnv(t, nil)
			c := e.dial()
			sendRaw(t, c, tt.frame)
			expectError(t, c, protocol.ErrBadMessage, "join")
			if calls := e.rooms.connectCalls(); len(calls) != 0 {
				t.Errorf("the hub was reached: %v", calls)
			}
			// Still open.
			send(t, c, "ping", "")
			if got := read(t, c); got.T != protocol.TypePong {
				t.Errorf("got %q", got.T)
			}
		})
	}
}

func TestJoinWithInvalidCodeNeverReachesTheHub(t *testing.T) {
	for _, code := range []string{"", "KT7", "KT7PX", "KA7P", "K0TP", "kt-p", "ñññ7"} {
		t.Run(fmt.Sprintf("code %q", code), func(t *testing.T) {
			e := newEnv(t, nil)
			c := e.dial()
			send(t, c, "join", fmt.Sprintf(`{"code":%q,"name":"Ana"}`, code))
			frames := expectCloseCode(t, c, protocol.CloseRoomClosed)
			if len(frames) != 1 || errorBody(t, frames[0]).Code != protocol.ErrRoomNotFound {
				t.Errorf("frames = %+v, want room_not_found", frames)
			}
			if calls := e.rooms.connectCalls(); len(calls) != 0 {
				t.Errorf("the hub was reached: %v", calls)
			}
		})
	}
}

func TestJoinConnectErrors(t *testing.T) {
	tests := []struct {
		name      string
		err       error
		wantError string
		wantClose int
	}{
		{"room not found", room.ErrRoomNotFound, protocol.ErrRoomNotFound, protocol.CloseRoomClosed},
		{"wrapped room not found", fmt.Errorf("hub: %w", room.ErrRoomNotFound), protocol.ErrRoomNotFound, protocol.CloseRoomClosed},
		{"room closed", room.ErrRoomClosed, protocol.ErrRoomClosed, protocol.CloseRoomClosed},
		{"shutting down", room.ErrShuttingDown, protocol.ErrServerBusy, protocol.CloseServiceRestart},
		{"too many rooms", room.ErrTooManyRooms, protocol.ErrServerBusy, protocol.CloseServiceRestart},
		{"unexpected", fmt.Errorf("db exploded"), "internal", 1011},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := newEnv(t, nil)
			e.rooms.connectErr = tt.err
			c := e.dial()
			send(t, c, "join", `{"code":"KT7P","name":"Ana"}`)
			frames := expectCloseCode(t, c, tt.wantClose)
			if len(frames) != 1 {
				t.Fatalf("frames = %+v", frames)
			}
			if got := errorBody(t, frames[0]); got.Code != tt.wantError || got.Ref != "join" {
				t.Errorf("error = %+v, want %q ref join", got, tt.wantError)
			}
			if strings.Contains(string(frames[0].D), "exploded") {
				t.Error("internal error text leaked to the client")
			}
			e.rooms.mu.Lock()
			n := len(e.rooms.sessions)
			e.rooms.mu.Unlock()
			if n != 0 {
				t.Error("no session must exist after a failed connect")
			}
		})
	}
}

func TestRoomNotFoundFromTheHub(t *testing.T) {
	e := newEnv(t, nil)
	c := e.dial()
	send(t, c, "join", `{"code":"ZZZZ","name":"Ana"}`)
	frames := expectCloseCode(t, c, protocol.CloseRoomClosed)
	if len(frames) != 1 || errorBody(t, frames[0]).Code != protocol.ErrRoomNotFound {
		t.Errorf("frames = %+v", frames)
	}
	if calls := e.rooms.connectCalls(); len(calls) != 1 || calls[0] != "ZZZZ" {
		t.Errorf("Connect calls = %v", calls)
	}
}

func TestOversizeFrame(t *testing.T) {
	pad := func(n int) string {
		base := `{"t":"set_answer","v":1,"d":{"text":""}}`
		return `{"t":"set_answer","v":1,"d":{"text":"` + strings.Repeat("a", n-len(base)) + `"}}`
	}
	t.Run("exactly the limit is accepted", func(t *testing.T) {
		e := newEnv(t, nil)
		c, sess := e.dialJoined()
		frame := pad(protocol.MaxMessageBytes)
		if len(frame) != protocol.MaxMessageBytes {
			t.Fatalf("test frame is %d bytes", len(frame))
		}
		sendRaw(t, c, frame)
		eventually(t, "frame to be handled", func() bool { return len(sess.handled()) == 2 })
	})
	for _, size := range []int{protocol.MaxMessageBytes + 1, protocol.MaxMessageBytes + 2, 3 * protocol.MaxMessageBytes, 1 << 20} {
		t.Run(fmt.Sprintf("%d bytes", size), func(t *testing.T) {
			e := newEnv(t, nil)
			c, sess := e.dialJoined()
			ctx, cancel := context.WithTimeout(context.Background(), waitLimit)
			defer cancel()
			// The server may close while we are still writing a huge frame.
			_ = c.Write(ctx, websocket.MessageText, []byte(pad(size)))

			frames, ce := expectClose(t, c)
			if ce.Code != protocol.CloseTooBig {
				t.Fatalf("close code = %d (%q), want 1009", ce.Code, ce.Reason)
			}
			if size <= 3*protocol.MaxMessageBytes {
				if len(frames) != 1 || errorBody(t, frames[0]).Code != protocol.ErrMessageTooLarge {
					t.Errorf("frames = %+v, want message_too_large", frames)
				}
			}
			eventually(t, "disconnect", func() bool { return sess.disconnects.Load() == 1 })
			if got := len(sess.handled()); got != 1 {
				t.Errorf("session handled %d frames, want only the join", got)
			}
		})
	}
}

func TestMalformedFrames(t *testing.T) {
	bad := []string{`not json`, `{"t":`, `[]`, `null`, `{"v":1}`, `{"t":"","v":1}`, `{"t":5,"v":1}`, `{"t":"ping","v":"1"}`, `42`, `""`}

	t.Run("each is answered with bad_message and the connection survives", func(t *testing.T) {
		e := newEnv(t, nil)
		c := e.dial()
		for _, frame := range bad[:9] {
			sendRaw(t, c, frame)
			expectError(t, c, protocol.ErrBadMessage, "")
		}
		send(t, c, "ping", "")
		if got := read(t, c); got.T != protocol.TypePong {
			t.Errorf("connection should still work, got %q", got.T)
		}
	})
	t.Run("the tenth closes with 1008", func(t *testing.T) {
		e := newEnv(t, nil)
		c, sess := e.dialJoined()
		for i := 0; i < 9; i++ {
			sendRaw(t, c, bad[i])
			expectError(t, c, protocol.ErrBadMessage, "")
		}
		sendRaw(t, c, bad[9])
		frames := expectCloseCode(t, c, protocol.ClosePolicy)
		if len(frames) != 1 || errorBody(t, frames[0]).Code != protocol.ErrBadMessage {
			t.Errorf("frames before close = %+v", frames)
		}
		eventually(t, "disconnect", func() bool { return sess.disconnects.Load() == 1 })
	})
	t.Run("valid frames do not reset the count", func(t *testing.T) {
		e := newEnv(t, nil)
		c := e.dial()
		for i := 0; i < 9; i++ {
			sendRaw(t, c, "garbage")
			expectError(t, c, protocol.ErrBadMessage, "")
			send(t, c, "ping", "")
			if got := read(t, c); got.T != protocol.TypePong {
				t.Fatalf("got %q", got.T)
			}
		}
		sendRaw(t, c, "garbage")
		expectCloseCode(t, c, protocol.ClosePolicy)
	})
	t.Run("binary frames are malformed", func(t *testing.T) {
		e := newEnv(t, nil)
		c := e.dial()
		ctx, cancel := context.WithTimeout(context.Background(), waitLimit)
		defer cancel()
		if err := c.Write(ctx, websocket.MessageBinary, []byte(`{"t":"ping","v":1}`)); err != nil {
			t.Fatal(err)
		}
		expectError(t, c, protocol.ErrBadMessage, "")
	})
}

func TestPerConnectionRateLimit(t *testing.T) {
	e := newEnv(t, nil)
	c, sess := e.dialJoined() // the join consumed one token of the burst

	// Frozen clock: no refill, so exactly ClientBurst-1 more frames fit.
	for i := 0; i < protocol.ClientBurst-1; i++ {
		send(t, c, "ping", "")
		if got := read(t, c); got.T != protocol.TypePong {
			t.Fatalf("frame %d: got %q", i+1, got.T)
		}
	}
	send(t, c, "ping", "")
	frames := expectCloseCode(t, c, protocol.ClosePolicy)
	if len(frames) != 1 || errorBody(t, frames[0]).Code != protocol.ErrRateLimited {
		t.Errorf("frames before close = %+v, want rate_limited", frames)
	}
	eventually(t, "disconnect", func() bool { return sess.disconnects.Load() == 1 })
}

func TestPerConnectionRateLimitRefills(t *testing.T) {
	var now = newFakeClock()
	e := newEnv(t, func(d *Deps) { d.Now = now.Now })
	c := e.dial()
	for i := 0; i < protocol.ClientBurst; i++ {
		send(t, c, "ping", "")
		read(t, c)
	}
	now.Advance(time.Second) // 20 tokens/s
	for i := 0; i < protocol.ClientRatePerSecond; i++ {
		send(t, c, "ping", "")
		if got := read(t, c); got.T != protocol.TypePong {
			t.Fatalf("after refill, frame %d: got %q", i+1, got.T)
		}
	}
	send(t, c, "ping", "")
	expectCloseCode(t, c, protocol.ClosePolicy)
}

func TestIdleTimeout(t *testing.T) {
	t.Run("closes a silent connection", func(t *testing.T) {
		e := newEnv(t, func(d *Deps) { d.ReadIdleTimeout = 150 * time.Millisecond })
		c, sess := e.dialJoined()
		start := time.Now()
		_, ce := expectClose(t, c)
		if ce.Code != websocket.StatusGoingAway {
			t.Errorf("close code = %d (%q), want 1001", ce.Code, ce.Reason)
		}
		if d := time.Since(start); d < 100*time.Millisecond {
			t.Errorf("closed after %v, want about the idle timeout", d)
		}
		eventually(t, "disconnect", func() bool { return sess.disconnects.Load() == 1 })
	})
	t.Run("every frame refreshes the deadline", func(t *testing.T) {
		e := newEnv(t, func(d *Deps) { d.ReadIdleTimeout = 250 * time.Millisecond })
		c := e.dial()
		for i := 0; i < 8; i++ {
			time.Sleep(80 * time.Millisecond) // 640ms in total, > timeout
			send(t, c, "ping", "")
			if got := read(t, c); got.T != protocol.TypePong {
				t.Fatalf("got %q", got.T)
			}
		}
		_, ce := expectClose(t, c)
		if ce.Code != websocket.StatusGoingAway {
			t.Errorf("close code = %d, want 1001 once the pings stop", ce.Code)
		}
	})
	t.Run("an unjoined connection also times out", func(t *testing.T) {
		e := newEnv(t, func(d *Deps) { d.ReadIdleTimeout = 100 * time.Millisecond })
		c := e.dial()
		_, ce := expectClose(t, c)
		if ce.Code != websocket.StatusGoingAway {
			t.Errorf("close code = %d, want 1001", ce.Code)
		}
	})
}

type fakeClock struct{ t atomicTime }

func newFakeClock() *fakeClock {
	c := &fakeClock{}
	c.t.Store(fixedNow)
	return c
}

func (c *fakeClock) Now() time.Time          { return c.t.Load() }
func (c *fakeClock) Advance(d time.Duration) { c.t.Store(c.t.Load().Add(d)) }
