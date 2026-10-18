package ws

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/AresiusXP/tutti-frutti/server/internal/protocol"
)

// upgradeRequest builds a well-formed upgrade request carrying e's guest
// cookie and the allowed Origin, to be tweaked per case.
func (e *env) upgradeRequest(tweak func(*http.Request)) *http.Request {
	r := httptest.NewRequest(http.MethodGet, "/ws", nil)
	r.RemoteAddr = "203.0.113.9:4444"
	r.AddCookie(&http.Cookie{Name: e.cookie.Name, Value: e.cookie.Value})
	r.Header.Set("Origin", testOrigin)
	r.Header.Set("Connection", "Upgrade")
	r.Header.Set("Upgrade", "websocket")
	r.Header.Set("Sec-WebSocket-Version", "13")
	r.Header.Set("Sec-WebSocket-Key", "dGhlIHNhbXBsZSBub25jZQ==")
	if tweak != nil {
		tweak(r)
	}
	return r
}

func TestRejectedRequestsDoNotConsumeTheUpgradeQuota(t *testing.T) {
	forged := strings.Repeat("A", 22) + "." + strings.Repeat("A", 43)
	rejections := []struct {
		name  string
		want  int
		tweak func(*http.Request)
	}{
		{"foreign origin", 403, func(r *http.Request) { r.Header.Set("Origin", otherOrigin) }},
		{"missing origin", 403, func(r *http.Request) { r.Header.Del("Origin") }},
		{"no cookie", 401, func(r *http.Request) { r.Header.Del("Cookie") }},
		{"forged cookie", 401, func(r *http.Request) {
			r.Header.Set("Cookie", protocol.GuestCookie+"="+forged)
		}},
		{"plain GET", 426, func(r *http.Request) { r.Header.Del("Upgrade"); r.Header.Del("Connection") }},
		{"POST", 405, func(r *http.Request) { r.Method = http.MethodPost }},
		{"bad version", 400, func(r *http.Request) { r.Header.Set("Sec-WebSocket-Version", "8") }},
		{"missing key", 400, func(r *http.Request) { r.Header.Del("Sec-WebSocket-Key") }},
		{"bad key", 400, func(r *http.Request) { r.Header.Set("Sec-WebSocket-Key", "short") }},
	}

	e := newEnv(t, nil) // burst 20 per IP, frozen clock: nothing ever refills
	for round := 0; round < 12; round++ {
		for _, rej := range rejections {
			rec := httptest.NewRecorder()
			e.h.ServeHTTP(rec, e.upgradeRequest(rej.tweak))
			if rec.Code != rej.want {
				t.Fatalf("round %d %s = %d, want %d", round, rej.name, rec.Code, rej.want)
			}
		}
	}
	// 108 rejected requests from the same IP (5x the burst) later...
	for i := 0; i < 20; i++ {
		c, resp, err := e.tryDial()
		if err != nil {
			t.Fatalf("valid upgrade %d refused after rejected traffic: %v (%v)", i+1, err, resp)
		}
		_ = c.CloseNow()
	}
	// ...valid upgrades still hit the limit.
	_, resp, err := e.tryDial()
	if err == nil || resp == nil || resp.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("21st valid upgrade: err=%v resp=%v, want 429", err, resp)
	}
	if resp.Header.Get("Retry-After") == "" {
		t.Error("429 lacks Retry-After")
	}
	body, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(body), `"error":"rate_limited"`) {
		t.Errorf("body = %q", body)
	}
}

func TestRejectedUpgradesFromOtherIPsLeaveTheSharedBucketAlone(t *testing.T) {
	// Behind one proxy every visitor shares the peer IP: a hostile page's
	// cross-origin attempts (403) must not starve real players.
	e := newEnv(t, nil)
	for i := 0; i < 200; i++ {
		_, resp, err := e.tryDial(origin(otherOrigin))
		if err == nil || resp == nil || resp.StatusCode != http.StatusForbidden {
			t.Fatalf("attempt %d: err=%v resp=%v, want 403", i+1, err, resp)
		}
	}
	c := e.dial()
	send(t, c, "ping", "")
	if got := read(t, c); got.T != protocol.TypePong {
		t.Errorf("got %q", got.T)
	}
}

func TestCheckUpgrade(t *testing.T) {
	tests := []struct {
		name       string
		tweak      func(*http.Request)
		want       int // 0 = accepted
		wantHeader [2]string
	}{
		{name: "valid"},
		{name: "valid, header tokens in a list", tweak: func(r *http.Request) {
			r.Header.Set("Connection", "keep-alive, Upgrade")
			r.Header.Set("Upgrade", "h2c, WebSocket")
		}},
		{name: "valid, header in several lines", tweak: func(r *http.Request) {
			r.Header.Set("Connection", "keep-alive")
			r.Header.Add("Connection", "upgrade")
		}},
		{name: "HTTP/1.0", tweak: func(r *http.Request) { r.ProtoMajor, r.ProtoMinor = 1, 0 }, want: 426},
		{name: "no Connection header", tweak: func(r *http.Request) { r.Header.Del("Connection") }, want: 426, wantHeader: [2]string{"Upgrade", "websocket"}},
		{name: "Connection without Upgrade", tweak: func(r *http.Request) { r.Header.Set("Connection", "keep-alive") }, want: 426},
		{name: "no Upgrade header", tweak: func(r *http.Request) { r.Header.Del("Upgrade") }, want: 426, wantHeader: [2]string{"Connection", "Upgrade"}},
		{name: "Upgrade to something else", tweak: func(r *http.Request) { r.Header.Set("Upgrade", "h2c") }, want: 426},
		{name: "POST", tweak: func(r *http.Request) { r.Method = http.MethodPost }, want: 405},
		{name: "HEAD", tweak: func(r *http.Request) { r.Method = http.MethodHead }, want: 405},
		{name: "version 8", tweak: func(r *http.Request) { r.Header.Set("Sec-WebSocket-Version", "8") }, want: 400, wantHeader: [2]string{"Sec-WebSocket-Version", "13"}},
		{name: "no version", tweak: func(r *http.Request) { r.Header.Del("Sec-WebSocket-Version") }, want: 400},
		{name: "no key", tweak: func(r *http.Request) { r.Header.Del("Sec-WebSocket-Key") }, want: 400},
		{name: "two keys", tweak: func(r *http.Request) { r.Header.Add("Sec-WebSocket-Key", "dGhlIHNhbXBsZSBub25jZQ==") }, want: 400},
		{name: "key not base64", tweak: func(r *http.Request) { r.Header.Set("Sec-WebSocket-Key", "!!!!") }, want: 400},
		{name: "key of the wrong length", tweak: func(r *http.Request) { r.Header.Set("Sec-WebSocket-Key", "dGhlIHNhbXBsZQ==") }, want: 400},
		{name: "key with surrounding space", tweak: func(r *http.Request) { r.Header.Set("Sec-WebSocket-Key", "  dGhlIHNhbXBsZSBub25jZQ==  ") }},
	}
	e := newEnv(t, nil)
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			ok := checkUpgrade(rec, e.upgradeRequest(tt.tweak))
			if ok != (tt.want == 0) {
				t.Fatalf("checkUpgrade = %v (status %d), want accepted=%v", ok, rec.Code, tt.want == 0)
			}
			if tt.want == 0 {
				return
			}
			if rec.Code != tt.want {
				t.Errorf("status = %d, want %d", rec.Code, tt.want)
			}
			if k := tt.wantHeader[0]; k != "" && rec.Header().Get(k) != tt.wantHeader[1] {
				t.Errorf("header %s = %q, want %q", k, rec.Header().Get(k), tt.wantHeader[1])
			}
			if strings.Contains(rec.Body.String(), "dGhl") || strings.Contains(rec.Body.String(), "!!!!") {
				t.Errorf("the refusal reflects a request header: %q", rec.Body.String())
			}
		})
	}
}

func TestNewHandlerDefaults(t *testing.T) {
	h := NewHandler(Deps{Rooms: &fakeRooms{}, Guests: newGuests()})
	if h.readIdleTimeout != protocol.ReadIdleTimeout || h.writeTimeout != protocol.WriteTimeout || h.joinTimeout != protocol.JoinTimeout {
		t.Errorf("timeouts = %v %v %v", h.readIdleTimeout, h.writeTimeout, h.joinTimeout)
	}
	if protocol.JoinTimeout != 10*time.Second {
		t.Errorf("protocol.JoinTimeout = %v, want 10s", protocol.JoinTimeout)
	}
	if h.maxConns != 2000 || h.maxConnsPerGuest != 4 || DefaultMaxConns != 2000 || DefaultMaxConnsPerGuest != 4 {
		t.Errorf("caps = %d %d", h.maxConns, h.maxConnsPerGuest)
	}
	if h.upgradeRate != 5 || h.upgradeBurst != 60 {
		t.Errorf("upgrade limit = %v burst %d, want 300/min burst 60", h.upgradeRate, h.upgradeBurst)
	}
	custom := NewHandler(Deps{Rooms: &fakeRooms{}, Guests: newGuests(), JoinTimeout: time.Second, MaxConns: 7, MaxConnsPerGuest: 2, UpgradeRate: 1, UpgradeBurst: 3})
	if custom.joinTimeout != time.Second || custom.maxConns != 7 || custom.maxConnsPerGuest != 2 || custom.upgradeRate != 1 || custom.upgradeBurst != 3 {
		t.Error("explicit Deps were not honoured")
	}
}

func TestJoinDeadline(t *testing.T) {
	t.Run("pings do not extend it", func(t *testing.T) {
		e := newEnv(t, func(d *Deps) { d.JoinTimeout = 250 * time.Millisecond })
		c := e.dial()
		start := time.Now()
		var frames []protocol.Envelope
		var ce websocket.CloseError
		for {
			// Keep the connection alive at the transport level with pings.
			send(t, c, "ping", "")
			env, err := tryRead(c, waitLimit)
			if err != nil {
				got, ok := asCloseError(err)
				if !ok {
					t.Fatalf("read: %v", err)
				}
				ce = got
				break
			}
			frames = append(frames, env)
			time.Sleep(30 * time.Millisecond)
			if time.Since(start) > 3*time.Second {
				t.Fatal("the unjoined socket outlived the join deadline")
			}
		}
		if ce.Code != protocol.ClosePolicy || ce.Reason != "join timeout" {
			t.Errorf("close = %d %q, want 1008 join timeout", ce.Code, ce.Reason)
		}
		if d := time.Since(start); d < 200*time.Millisecond {
			t.Errorf("closed after %v, before the deadline", d)
		}
		for _, f := range frames {
			if f.T != protocol.TypePong {
				t.Errorf("unexpected frame %q before the close: join timeout closes without an error frame", f.T)
			}
		}
		if len(frames) < 3 {
			t.Errorf("only %d pongs: the test did not ping through the deadline", len(frames))
		}
		if calls := e.rooms.connectCalls(); len(calls) != 0 {
			t.Errorf("the hub was reached: %v", calls)
		}
	})
	t.Run("a socket that never speaks is closed", func(t *testing.T) {
		e := newEnv(t, func(d *Deps) { d.JoinTimeout = 150 * time.Millisecond })
		c := e.dial()
		_, ce := expectClose(t, c)
		if ce.Code != protocol.ClosePolicy || ce.Reason != "join timeout" {
			t.Errorf("close = %d %q", ce.Code, ce.Reason)
		}
	})
	t.Run("commands before join do not satisfy it", func(t *testing.T) {
		e := newEnv(t, func(d *Deps) { d.JoinTimeout = 200 * time.Millisecond })
		c := e.dial()
		send(t, c, "basta", "")
		expectError(t, c, protocol.ErrNotJoined, "basta")
		_, ce := expectClose(t, c)
		if ce.Code != protocol.ClosePolicy || ce.Reason != "join timeout" {
			t.Errorf("close = %d %q", ce.Code, ce.Reason)
		}
	})
	t.Run("a failed join does not satisfy it either", func(t *testing.T) {
		e := newEnv(t, func(d *Deps) { d.JoinTimeout = 5 * time.Second })
		c := e.dial()
		send(t, c, "join", `{"code":"ZZZZ"}`)
		expectCloseCode(t, c, protocol.CloseRoomClosed)
	})
	t.Run("a socket that joins in time is untouched", func(t *testing.T) {
		e := newEnv(t, func(d *Deps) { d.JoinTimeout = 200 * time.Millisecond })
		c, sess := e.dialJoined()
		time.Sleep(600 * time.Millisecond) // three deadlines later
		send(t, c, "ping", "")
		if got := read(t, c); got.T != protocol.TypePong {
			t.Fatalf("got %q after the join deadline", got.T)
		}
		send(t, c, "set_answer", `{"category":0,"text":"x"}`)
		eventually(t, "command to be routed", func() bool { return len(sess.handled()) == 2 })
		if n := sess.disconnects.Load(); n != 0 {
			t.Errorf("Disconnect called %d times on a healthy joined socket", n)
		}
	})
}

func TestMaxConnsCap(t *testing.T) {
	e := newEnv(t, func(d *Deps) { d.MaxConns = 3 })
	var open []*websocket.Conn
	for i := 0; i < 3; i++ {
		ck, _ := e.guest()
		open = append(open, e.dial(withCookie(ck)))
	}
	eventually(t, "3 sockets to be counted", func() bool { return e.h.Open() == 3 })
	if g := gauge(t, e.reg, "tuttifrutti_ws_connections_open"); g != 3 {
		t.Errorf("gauge = %v, want 3", g)
	}

	// The 4th is refused before the upgrade, over and over, without touching
	// the upgrade bucket (burst 20, frozen clock).
	for i := 0; i < 40; i++ {
		ck, _ := e.guest()
		_, resp, err := e.tryDial(withCookie(ck))
		if err == nil || resp == nil || resp.StatusCode != http.StatusServiceUnavailable {
			t.Fatalf("refusal %d: err=%v resp=%v, want 503", i+1, err, resp)
		}
		if resp.Header.Get("Retry-After") != "5" {
			t.Fatalf("Retry-After = %q, want 5", resp.Header.Get("Retry-After"))
		}
		body, _ := io.ReadAll(resp.Body)
		if !strings.Contains(string(body), `"error":"server_busy"`) {
			t.Fatalf("body = %q", body)
		}
	}
	if e.h.Open() != 3 {
		t.Errorf("Open = %d after refusals", e.h.Open())
	}

	// A freed slot is usable at once.
	_ = open[0].Close(websocket.StatusNormalClosure, "")
	eventually(t, "a slot to free up", func() bool { return e.h.Open() == 2 })
	ck, _ := e.guest()
	open[0] = e.dial(withCookie(ck))
	for _, c := range open {
		_ = c.CloseNow()
	}
	eventually(t, "all slots to free up", func() bool { return e.h.Open() == 0 })

	// 4 tokens were spent on successful upgrades; the 40 refusals spent none:
	// 16 more fit in the burst of 20, then the bucket answers 429.
	for i := 0; i < 16; i++ {
		c, resp, err := e.tryDial()
		if err != nil {
			t.Fatalf("upgrade %d after the refusals: %v (%v): refusals consumed the bucket", i+1, err, resp)
		}
		_ = c.CloseNow()
		eventually(t, "slot release", func() bool { return e.h.Open() == 0 })
	}
	if _, resp, err := e.tryDial(); err == nil || resp.StatusCode != http.StatusTooManyRequests {
		t.Errorf("21st upgrade: err=%v resp=%v, want 429", err, resp)
	}
}

func TestMaxConnsPerGuestCap(t *testing.T) {
	e := newEnv(t, func(d *Deps) { d.MaxConnsPerGuest = 2 })
	a1 := e.dial()
	a2 := e.dial()
	eventually(t, "2 sockets", func() bool { return e.h.Open() == 2 })

	for i := 0; i < 40; i++ {
		_, resp, err := e.tryDial()
		if err == nil || resp == nil || resp.StatusCode != http.StatusTooManyRequests {
			t.Fatalf("attempt %d: err=%v resp=%v, want 429", i+1, err, resp)
		}
		if resp.Header.Get("Retry-After") == "" {
			t.Fatal("429 lacks Retry-After")
		}
		body, _ := io.ReadAll(resp.Body)
		if !strings.Contains(string(body), `"error":"too_many_connections"`) {
			t.Fatalf("body = %q, want too_many_connections and not rate_limited: the bucket must be untouched", body)
		}
	}

	// Another guest is unaffected.
	other, _ := e.guest()
	b := e.dial(withCookie(other))
	if e.h.Open() != 3 {
		t.Errorf("Open = %d, want 3", e.h.Open())
	}

	// A closed socket frees the guest's slot.
	_ = a1.Close(websocket.StatusNormalClosure, "")
	eventually(t, "the slot to free", func() bool { return e.h.Open() == 2 })
	a3 := e.dial()
	for _, c := range []*websocket.Conn{a2, a3, b} {
		_ = c.CloseNow()
	}
	eventually(t, "all slots to free", func() bool { return e.h.Open() == 0 })
	e.h.mu.Lock()
	defer e.h.mu.Unlock()
	if len(e.h.perGuest) != 0 {
		t.Errorf("per-guest counters leaked: %v", e.h.perGuest)
	}
}

func TestConnectionCountersReturnToZero(t *testing.T) {
	e := newEnv(t, func(d *Deps) {
		d.UpgradeBurst = 12
		d.MaxConnsPerGuest = 100
		d.WriteTimeout = 200 * time.Millisecond
	})
	assertZero := func(stage string) {
		t.Helper()
		eventually(t, stage+": counters to return to zero", func() bool {
			e.h.mu.Lock()
			defer e.h.mu.Unlock()
			return e.h.total == 0 && len(e.h.perGuest) == 0 && len(e.h.conns) == 0
		})
		if g := gauge(t, e.reg, "tuttifrutti_ws_connections_open"); g != 0 {
			t.Errorf("%s: gauge = %v, want 0", stage, g)
		}
	}

	// Clean close, abrupt TCP close, room-initiated close, protocol violation.
	c1 := e.dial()
	_ = c1.Close(websocket.StatusNormalClosure, "")
	c2 := e.dial()
	_ = c2.CloseNow()
	rc := e.rawDial()
	_ = rc.nc.Close()
	c3, s3 := e.dialJoined()
	s3.client.Close(protocol.CloseKicked, "")
	expectClose(t, c3)
	c4 := e.dial()
	sendRaw(t, c4, `{"t":"x","v":9}`)
	expectClose(t, c4)
	assertZero("after every kind of ending")

	// Handshake that fails after admission: the hijack is impossible on a
	// recorder, so Accept answers 501.
	rec := httptest.NewRecorder()
	e.h.ServeHTTP(rec, e.upgradeRequest(nil))
	if rec.Code != http.StatusNotImplemented {
		t.Fatalf("handshake failure = %d, want 501", rec.Code)
	}
	assertZero("after a failed handshake")

	// Rate-limited after admission: the slot is given back. 7 of the 12 tokens
	// are left (5 were spent above, one of them on the recorder request).
	var open []*websocket.Conn
	for {
		c, resp, err := e.tryDial()
		if err != nil {
			if resp == nil || resp.StatusCode != http.StatusTooManyRequests {
				t.Fatalf("dial: %v (%v)", err, resp)
			}
			break
		}
		open = append(open, c)
	}
	if len(open) == 0 {
		t.Fatal("no upgrade succeeded before the bucket ran dry")
	}
	for i := 0; i < 10; i++ {
		if _, _, err := e.tryDial(); err == nil {
			t.Fatal("upgrade accepted with an empty bucket")
		}
	}
	eventually(t, "rate-limited requests not to hold slots", func() bool { return e.h.Open() == len(open) })
	for _, c := range open {
		_ = c.CloseNow()
	}
	assertZero("after rate-limited attempts")
}
