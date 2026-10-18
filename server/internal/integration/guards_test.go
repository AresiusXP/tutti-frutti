package integration

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/AresiusXP/tutti-frutti/server/internal/auth"
	"github.com/AresiusXP/tutti-frutti/server/internal/game"
	"github.com/AresiusXP/tutti-frutti/server/internal/protocol"
	"github.com/AresiusXP/tutti-frutti/server/internal/ws"
)

// lobbyOf creates a room and returns its code and a connected, not yet joined
// client of the host.
func lobbyOf(t *testing.T, s *stack) (code string, host *player, c *client) {
	t.Helper()
	host = s.newPlayer("Ana")
	code = s.createRoom(host, "")
	return code, host, s.connect(host)
}

func (c *client) sendBinary(b []byte) {
	c.t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), waitLimit)
	defer cancel()
	if err := c.conn.Write(ctx, websocket.MessageBinary, b); err != nil {
		c.t.Fatalf("%s: send binary: %v", c.p.name, err)
	}
}

// TestFirstFrameMustBeJoin: before "join" only ping is answered; any other
// frame gets not_joined and the connection stays usable.
func TestFirstFrameMustBeJoin(t *testing.T) {
	s := newStack(t, stackOpts{})
	code, _, c := lobbyOf(t, s)

	for _, typ := range []string{game.CmdStartGame, game.CmdSetAnswer, game.CmdLeave, "no_such_command"} {
		m := c.mark()
		c.send(typ, nil)
		f := c.waitSince(m, "not_joined for "+typ, isError(protocol.ErrNotJoined))
		if f.Err.Ref != typ {
			t.Fatalf("error ref = %q, want %q", f.Err.Ref, typ)
		}
	}
	c.expectPong() // ping works before the join
	if lastState := c.lastState(); lastState != nil {
		t.Fatalf("a state frame was sent before the join: %+v", lastState)
	}

	st := c.join(code)
	if st.Code != code || st.View.Phase != game.PhaseLobby {
		t.Fatalf("state after the join = %+v", st)
	}
	if got := c.roundTrip(game.CmdJoin, c.joinRequest(code)); got != protocol.ErrAlreadyJoined {
		t.Fatalf("second join: %q, want already_joined", got)
	}
	if got := c.roundTrip("no_such_command", nil); got != protocol.ErrBadMessage {
		t.Fatalf("unknown command after the join: %q, want bad_message", got)
	}
	// A client cannot flip its own connection flag (that would skew votes).
	if got := c.roundTrip("set_connected", map[string]any{"connected": false}); got != protocol.ErrBadMessage {
		t.Fatalf("set_connected: %q, want bad_message", got)
	}
	if p, _ := seatOf(c.view(), c.p.id); !p.Connected {
		t.Fatal("set_connected changed the seat")
	}
	// A join for a nickname the engine refuses leaves the connection unjoined but open.
	bad := s.newPlayer("")
	b := s.connect(bad)
	b.send(game.CmdJoin, b.joinRequest(code))
	b.waitFor("invalid_name", isError(game.CodeInvalidName))
	b.expectPong()
	if got := b.roundTrip(game.CmdLeave, nil); got != protocol.ErrNotJoined {
		t.Fatalf("command after a rejected join: %q, want not_joined", got)
	}
}

// TestPingPong: the pong carries the server time, before and after the join.
func TestPingPong(t *testing.T) {
	var s *stack
	s = newStack(t, stackOpts{wsNow: func() time.Time { return s.clock.Now() }})
	code, _, c := lobbyOf(t, s)

	if got, want := c.expectPong().ServerTime, s.clock.Now().UnixMilli(); got != want {
		t.Fatalf("pong before the join = %d, want %d", got, want)
	}
	c.join(code)
	s.advance(20 * time.Second)
	if got, want := c.expectPong().ServerTime, s.clock.Now().UnixMilli(); got != want {
		t.Fatalf("pong after the join = %d, want %d", got, want)
	}
	if got := c.lastState().ServerTime; got != epoch.UnixMilli() {
		t.Fatalf("state serverTime = %d, want the join time %d", got, epoch.UnixMilli())
	}
}

// TestUnsupportedVersion: a frame with another protocol version is answered
// with unsupported_version and the socket is closed with 4003.
func TestUnsupportedVersion(t *testing.T) {
	for _, tc := range []struct {
		name   string
		joined bool
		frame  string
		ref    string
	}{
		{"future version before the join", false, `{"t":"ping","v":2}`, "ping"},
		{"missing version before the join", false, `{"t":"ping"}`, "ping"},
		{"negative version before the join", false, `{"t":"join","v":-1,"d":{}}`, "join"},
		{"future version after the join", true, `{"t":"set_answer","v":2,"d":{"category":0,"text":"x"}}`, "set_answer"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := newStack(t, stackOpts{})
			code, _, c := lobbyOf(t, s)
			if tc.joined {
				c.join(code)
			}
			c.sendRaw(tc.frame)
			f := c.waitFor("unsupported_version", isError(protocol.ErrUnsupportedVersion))
			if f.Err.Ref != tc.ref {
				t.Fatalf("error ref = %q, want %q", f.Err.Ref, tc.ref)
			}
			c.expectClose(protocol.CloseUnsupportedVersion)
		})
	}
}

// TestOversizeFrame: a frame over MaxMessageBytes ends the connection with 1009.
func TestOversizeFrame(t *testing.T) {
	big := func(n int) string {
		return `{"t":"set_answer","v":1,"d":{"category":0,"text":"` + strings.Repeat("a", n) + `"}}`
	}
	for _, tc := range []struct {
		name      string
		joined    bool
		frame     string
		wantError bool // the library may cut a huge frame before the server answers
	}{
		{"one byte over the limit", false, strings.Repeat("x", protocol.MaxMessageBytes+1), true},
		{"exactly the limit is only malformed", false, "", false},
		{"a frame of 70 KB", false, big(70_000), false},
		{"over the limit after the join", true, big(protocol.MaxMessageBytes), true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := newStack(t, stackOpts{})
			code, _, c := lobbyOf(t, s)
			var other *client
			if tc.joined {
				c.join(code)
				guest := s.newPlayer("Beto")
				other = s.connect(guest)
				other.join(code)
			}
			if tc.frame == "" {
				// A frame of exactly MaxMessageBytes passes the size check (it is just not JSON).
				c.sendRaw(strings.Repeat("x", protocol.MaxMessageBytes))
				c.waitFor("bad_message", isError(protocol.ErrBadMessage))
				c.expectPong()
				return
			}
			c.sendRaw(tc.frame)
			if tc.wantError {
				c.waitFor("message_too_large", isError(protocol.ErrMessageTooLarge))
			}
			c.expectClose(protocol.CloseTooBig)
			if other != nil {
				waitConnected(t, c.p.id, false, other)
			}
		})
	}
}

// TestMalformedFramesThreshold: nine malformed frames are tolerated, the tenth
// closes the connection with 1008.
func TestMalformedFramesThreshold(t *testing.T) {
	s := newStack(t, stackOpts{})
	_, _, c := lobbyOf(t, s)

	malformed := []func(){
		func() { c.sendRaw(`{not json`) },
		func() { c.sendRaw(`[]`) },
		func() { c.sendRaw(`"join"`) },
		func() { c.sendRaw(`{"v":1}`) },
		func() { c.sendRaw(`{"t":"","v":1}`) },
		func() { c.sendBinary([]byte{1, 2, 3}) },
		func() { c.sendRaw(`{"t":"join","v":1}`) },            // join without a payload
		func() { c.sendRaw(`{"t":"join","v":1,"d":"BCDF"}`) }, // payload of the wrong type
		func() { c.sendRaw(``) },
	}
	for i, send := range malformed {
		m := c.mark()
		send()
		c.waitSince(m, "bad_message", isError(protocol.ErrBadMessage))
		if c.isClosed() {
			t.Fatalf("closed after %d malformed frames", i+1)
		}
	}
	c.expectPong() // still alive after nine
	c.sendRaw(`{still not json`)
	c.waitFor("tenth bad_message", func(f frame) bool {
		return f.Err != nil && f.Err.Code == protocol.ErrBadMessage && f.Seq >= len(malformed)
	})
	c.expectClose(protocol.ClosePolicy)
	if got := s.metricValue("tuttifrutti_ws_closes_total", map[string]string{"code": "1008"}); got != 1 {
		eventually(t, "the 1008 close to be counted", func() bool {
			return s.metricValue("tuttifrutti_ws_closes_total", map[string]string{"code": "1008"}) == 1
		})
	}
}

// TestConnectionRateLimit uses the injected clock for the per-connection token
// bucket (20/s, burst 40): the 41st frame without refill is refused with
// rate_limited and 1008, and time passing gives tokens back.
func TestConnectionRateLimit(t *testing.T) {
	var s *stack
	s = newStack(t, stackOpts{wsNow: func() time.Time { return s.clock.Now() }})
	_, _, c := lobbyOf(t, s)

	for i := range protocol.ClientBurst {
		if got := c.expectPong(); got.ServerTime != epoch.UnixMilli() {
			t.Fatalf("pong %d = %+v", i, got)
		}
	}
	// 125 ms refills 2.5 tokens (20 per second).
	s.advance(125 * time.Millisecond)
	c.expectPong()
	c.expectPong()
	if c.isClosed() {
		t.Fatal("closed while tokens were left")
	}
	m := c.mark()
	c.send(protocol.TypePing, nil)
	c.waitSince(m, "rate_limited", isError(protocol.ErrRateLimited))
	c.expectClose(protocol.ClosePolicy)
}

// TestUnknownRoom: joining a room that does not exist (or whose code is not
// even well formed) answers room_not_found and closes with 4004; a known code
// is accepted whatever its case and padding.
func TestUnknownRoom(t *testing.T) {
	s := newStack(t, stackOpts{})
	code, _, c := lobbyOf(t, s)

	for _, bad := range []string{"BCDF", "bcdf", "ABC", "AEIO", "BCDFG", "", "    ", "0000"} {
		p := s.newPlayer("Zed")
		z := s.connect(p)
		z.send(game.CmdJoin, protocol.JoinRequest{Code: bad, Name: "Zed", Avatar: p.avatar})
		f := z.waitFor("room_not_found for "+bad, isError(protocol.ErrRoomNotFound))
		if f.Err.Ref != game.CmdJoin {
			t.Fatalf("code %q: error ref %q", bad, f.Err.Ref)
		}
		z.expectClose(protocol.CloseRoomClosed)
	}

	// Case and spaces do not matter for a real room.
	c.send(game.CmdJoin, protocol.JoinRequest{Code: "  " + strings.ToLower(code) + " ", Name: "Ana", Avatar: c.p.avatar})
	c.waitFor("state", isState)
}

// TestUpgradeRefusals: no cookie, a bad cookie or a foreign Origin never get
// to upgrade.
func TestUpgradeRefusals(t *testing.T) {
	s := newStack(t, stackOpts{})
	p := s.newPlayer("Ana")
	valid := p.cookie
	name, value, _ := strings.Cut(valid, "=")
	other := auth.NewGuests([]byte("another-secret-another-secret-0123456789"), false, nil)
	foreign := func() string {
		rec := httptest.NewRecorder()
		other.Issue(rec)
		c := rec.Result().Cookies()[0]
		return (&http.Cookie{Name: c.Name, Value: c.Value}).String()
	}()

	cookies := []struct {
		name   string
		cookie string
	}{
		{"no cookie", ""},
		{"garbage", name + "=garbage"},
		{"truncated", name + "=" + value[:len(value)-5]},
		{"tampered signature", name + "=" + value[:len(value)-1] + flip(value[len(value)-1])},
		{"signed with another secret", foreign},
		{"wrong cookie name", "session=" + value},
		{"oversized", name + "=" + strings.Repeat("a", 5000)},
	}
	for _, tc := range cookies {
		t.Run("cookie: "+tc.name, func(t *testing.T) {
			_, resp, err := s.dialWith(t, p, tc.cookie, s.origin)
			if err == nil || resp == nil || resp.StatusCode != http.StatusUnauthorized {
				t.Fatalf("upgrade with %s: err %v status %v, want 401", tc.name, err, resp)
			}
			var body struct{ Error string }
			readJSON(t, resp, &body)
			if body.Error != protocol.ErrUnauthorized {
				t.Fatalf("401 body error = %q", body.Error)
			}
		})
	}

	origins := []struct {
		name   string
		origin string
	}{
		{"foreign host", "https://evil.example"},
		{"null", "null"},
		{"missing", ""},
		{"same host other port", "http://127.0.0.1:1"},
		{"same host other scheme", strings.Replace(s.origin, "http://", "https://", 1)},
		{"origin with a path", s.origin + "/lobby"},
		{"origin with userinfo", strings.Replace(s.origin, "http://", "http://me@", 1)},
	}
	for _, tc := range origins {
		t.Run("origin: "+tc.name, func(t *testing.T) {
			_, resp, err := s.dialWith(t, p, valid, tc.origin)
			if err == nil || resp == nil || resp.StatusCode != http.StatusForbidden {
				t.Fatalf("upgrade with origin %q: err %v status %v, want 403", tc.origin, err, resp)
			}
			var body struct{ Error string }
			readJSON(t, resp, &body)
			if body.Error != "forbidden_origin" {
				t.Fatalf("403 body error = %q", body.Error)
			}
		})
	}

	// The allowed origin (in another case) with the valid cookie goes through.
	c, _, err := s.dialWith(t, p, valid, strings.ToUpper(s.origin[:4])+s.origin[4:])
	if err != nil {
		t.Fatalf("upgrade with a valid cookie and origin: %v", err)
	}
	c.expectPong()
	// A plain GET (no upgrade headers) is refused with a 4xx, never served.
	r := s.do(http.MethodGet, "/ws", nil, withCookie(p))
	if r.status < 400 || r.status >= 500 {
		t.Fatalf("GET /ws without upgrade headers = %d", r.status)
	}
}

// TestUpgradeAdmission: the per-guest connection cap and the per-IP upgrade
// bucket answer 429 before the upgrade.
func TestUpgradeAdmission(t *testing.T) {
	t.Run("per guest cap", func(t *testing.T) {
		s := newStack(t, stackOpts{})
		p := s.newPlayer("Ana")
		var clients []*client
		for i := range ws.DefaultMaxConnsPerGuest {
			c, _, err := s.dialWith(t, p, p.cookie, s.origin)
			if err != nil {
				t.Fatalf("connection %d: %v", i+1, err)
			}
			clients = append(clients, c)
		}
		_, resp, err := s.dialWith(t, p, p.cookie, s.origin)
		if err == nil || resp == nil || resp.StatusCode != http.StatusTooManyRequests {
			t.Fatalf("connection over the cap: err %v resp %v, want 429", err, resp)
		}
		var body struct{ Error string }
		readJSON(t, resp, &body)
		if body.Error != "too_many_connections" || resp.Header.Get("Retry-After") == "" {
			t.Fatalf("cap refusal = %+v retry-after %q", body, resp.Header.Get("Retry-After"))
		}
		// Freeing a slot lets the guest in again.
		clients[0].drop()
		eventually(t, "a slot to be released", func() bool {
			c, _, err := s.dialWith(t, p, p.cookie, s.origin)
			if err != nil {
				return false
			}
			c.expectPong()
			return true
		})
	})

	t.Run("per IP bucket", func(t *testing.T) {
		// A frozen ws clock: the bucket never refills, so the burst is exact.
		s := newStack(t, stackOpts{wsNow: func() time.Time { return epoch }})
		sameIP := func(h http.Header) { h.Set("X-Forwarded-For", "203.0.113.7") }
		accepted, limited := 0, 0
		for range 400 {
			if limited > 0 {
				break
			}
			p := s.newPlayer("Ana")
			c, resp, err := s.dialWith(t, p, p.cookie, s.origin, sameIP)
			if err == nil {
				accepted++
				_ = c
				continue
			}
			if resp == nil || resp.StatusCode != http.StatusTooManyRequests {
				t.Fatalf("dial: %v (resp %v)", err, resp)
			}
			if resp.Header.Get("Retry-After") == "" {
				t.Fatal("429 without Retry-After")
			}
			limited++
		}
		if accepted < 10 || limited == 0 {
			t.Fatalf("from one IP: %d accepted, %d limited; want a burst of upgrades then 429s", accepted, limited)
		}
		// Another client address has its own bucket.
		p := s.newPlayer("Beto")
		if c, _, err := s.dialWith(t, p, p.cookie, s.origin); err != nil {
			t.Fatalf("another IP was limited too: %v", err)
		} else {
			c.expectPong()
		}
	})
}

// TestJoinDeadlines: a connection that never completes a "join" is closed with
// 1008, by the transport's own timer and by the room's (on the injected clock).
func TestJoinDeadlines(t *testing.T) {
	t.Run("transport timer, pings do not extend it", func(t *testing.T) {
		s := newStack(t, stackOpts{ws: func(d *ws.Deps) { d.JoinTimeout = 100 * time.Millisecond }})
		_, _, c := lobbyOf(t, s)
		deadline := time.Now().Add(waitLimit)
		for !c.isClosed() && time.Now().Before(deadline) {
			_ = c.trySend(protocol.TypePing, nil)
			time.Sleep(20 * time.Millisecond)
		}
		c.expectClose(protocol.ClosePolicy)
	})

	t.Run("room timer on the ManualClock", func(t *testing.T) {
		s := newStack(t, stackOpts{})
		code, _, _ := lobbyOf(t, s)
		// Connected to the room (the first frame was a join) but never accepted.
		bad := s.newPlayer("")
		b := s.connect(bad)
		b.send(game.CmdJoin, b.joinRequest(code))
		b.waitFor("invalid_name", isError(game.CodeInvalidName))

		s.advance(9 * time.Second)
		if b.isClosed() {
			t.Fatal("closed before the join deadline")
		}
		s.advance(2 * time.Second)
		b.expectClose(protocol.ClosePolicy)
		if info, ok := s.roomInfo(code); !ok || info.Players != 1 {
			t.Fatalf("room info = %+v (found %v)", info, ok)
		}
	})
}

func flip(b byte) string {
	if b == 'A' {
		return "B"
	}
	return "A"
}
