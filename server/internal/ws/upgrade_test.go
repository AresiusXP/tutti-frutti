package ws

import (
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/coder/websocket"

	"github.com/AresiusXP/tutti-frutti/server/internal/protocol"
)

func TestNewHandlerRequiresDeps(t *testing.T) {
	for name, d := range map[string]Deps{"no rooms": {Guests: nil}, "no guests": {Rooms: &fakeRooms{}}} {
		t.Run(name, func(t *testing.T) {
			defer func() {
				if recover() == nil {
					t.Error("NewHandler did not panic")
				}
			}()
			NewHandler(d)
		})
	}
}

func TestUpgradeRejections(t *testing.T) {
	forged := strings.Repeat("A", 22) + "." + strings.Repeat("A", 43)
	tests := []struct {
		name     string
		opts     []dialOpt
		tweak    func(*Deps)
		wantCode int
		wantBody string
	}{
		{name: "no cookie", opts: []dialOpt{noCookie()}, wantCode: 401, wantBody: "unauthorized"},
		{name: "forged cookie", opts: []dialOpt{rawCookie(forged)}, wantCode: 401, wantBody: "unauthorized"},
		{name: "garbage cookie", opts: []dialOpt{rawCookie("x")}, wantCode: 401, wantBody: "unauthorized"},
		{name: "foreign origin", opts: []dialOpt{origin(otherOrigin)}, wantCode: 403, wantBody: "forbidden_origin"},
		{name: "wrong scheme", opts: []dialOpt{origin("http://tuttifrutti.aresius.xyz")}, wantCode: 403, wantBody: "forbidden_origin"},
		{name: "null origin", opts: []dialOpt{origin("null")}, wantCode: 403, wantBody: "forbidden_origin"},
		{name: "missing origin when origins are configured", opts: []dialOpt{noOrigin()}, wantCode: 403, wantBody: "forbidden_origin"},
		{
			name:     "foreign origin in dev mode",
			opts:     []dialOpt{origin(otherOrigin)},
			tweak:    func(d *Deps) { d.AllowedOrigins = nil },
			wantCode: 403, wantBody: "forbidden_origin",
		},
		{name: "cookie is checked before origin", opts: []dialOpt{noCookie(), origin(otherOrigin)}, wantCode: 401, wantBody: "unauthorized"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := newEnv(t, tt.tweak)
			c, resp, err := e.tryDial(tt.opts...)
			if err == nil {
				_ = c.CloseNow()
				t.Fatal("the upgrade succeeded")
			}
			if resp == nil || resp.StatusCode != tt.wantCode {
				t.Fatalf("response = %v (err %v), want status %d", resp, err, tt.wantCode)
			}
			body, _ := io.ReadAll(resp.Body)
			if !strings.Contains(string(body), `"error":"`+tt.wantBody+`"`) {
				t.Errorf("body = %q, want error %q", body, tt.wantBody)
			}
			if ct := resp.Header.Get("Content-Type"); ct != "application/json" {
				t.Errorf("Content-Type = %q", ct)
			}
			if calls := e.rooms.connectCalls(); len(calls) != 0 {
				t.Errorf("the hub was reached: %v", calls)
			}
		})
	}
}

func TestUpgradeAcceptedOrigins(t *testing.T) {
	tests := []struct {
		name  string
		opts  []dialOpt
		tweak func(*Deps)
	}{
		{name: "allowed origin"},
		{name: "host case-insensitive", opts: []dialOpt{origin("https://TuttiFrutti.Aresius.XYZ")}},
		{name: "default port spelled out", opts: []dialOpt{origin("https://tuttifrutti.aresius.xyz:443")}},
		{name: "second allowed origin", opts: []dialOpt{origin("http://localhost:5173")}, tweak: func(d *Deps) {
			d.AllowedOrigins = []string{testOrigin, "http://localhost:5173"}
		}},
		{name: "dev mode: no origin (non-browser client)", opts: []dialOpt{noOrigin()}, tweak: func(d *Deps) { d.AllowedOrigins = nil }},
		{name: "dev mode: origin equal to the host", opts: nil, tweak: func(d *Deps) { d.AllowedOrigins = nil }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := newEnv(t, tt.tweak)
			opts := tt.opts
			if tt.name == "dev mode: origin equal to the host" {
				opts = []dialOpt{origin(e.srv.URL)}
			}
			c, resp, err := e.tryDial(opts...)
			if err != nil {
				t.Fatalf("dial: %v (%v)", err, resp)
			}
			// The connection works end to end.
			send(t, c, "ping", "")
			if got := read(t, c); got.T != protocol.TypePong {
				t.Errorf("got %q, want pong", got.T)
			}
		})
	}
}

func TestUpgradeIdentity(t *testing.T) {
	e := newEnv(t, nil)
	_, sess := e.dialJoined()
	if got := sess.client.PlayerID(); got != e.id {
		t.Errorf("PlayerID = %q, want the cookie's guest id %q", got, e.id)
	}
}

func TestUpgradeNotAWebSocketRequest(t *testing.T) {
	e := newEnv(t, nil)
	req, _ := http.NewRequest(http.MethodGet, e.srv.URL, nil)
	req.AddCookie(&http.Cookie{Name: e.cookie.Name, Value: e.cookie.Value})
	req.Header.Set("Origin", testOrigin)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUpgradeRequired {
		t.Errorf("plain GET = %d, want 426", resp.StatusCode)
	}

	req, _ = http.NewRequest(http.MethodPost, e.srv.URL, nil)
	req.AddCookie(&http.Cookie{Name: e.cookie.Name, Value: e.cookie.Value})
	req.Header.Set("Origin", testOrigin)
	req.Header.Set("Connection", "Upgrade")
	req.Header.Set("Upgrade", "websocket")
	resp2, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp2.Body.Close()
	if resp2.StatusCode != http.StatusMethodNotAllowed {
		t.Errorf("POST upgrade = %d, want 405", resp2.StatusCode)
	}
}

func TestUpgradeRateLimit(t *testing.T) {
	e := newEnv(t, nil)
	for i := 0; i < 20; i++ {
		c, _, err := e.tryDial()
		if err != nil {
			t.Fatalf("upgrade %d: %v", i+1, err)
		}
		_ = c.Close(websocket.StatusNormalClosure, "")
	}
	c, resp, err := e.tryDial()
	if err == nil {
		_ = c.CloseNow()
		t.Fatal("the 21st upgrade was accepted")
	}
	if resp == nil || resp.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("response = %v, want 429", resp)
	}
	if ra := resp.Header.Get("Retry-After"); ra != "1" {
		t.Errorf("Retry-After = %q, want 1", ra)
	}
	body, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(body), `"error":"rate_limited"`) {
		t.Errorf("body = %q", body)
	}
}

func TestUpgradeRateLimitIsPerClientIP(t *testing.T) {
	t.Run("X-Forwarded-For ignored by default", func(t *testing.T) {
		e := newEnv(t, nil)
		for i := 0; i < 20; i++ {
			c, _, err := e.tryDial(forwardedFor("9.9.9." + string(rune('0'+i%10))))
			if err != nil {
				t.Fatalf("upgrade %d: %v", i+1, err)
			}
			_ = c.CloseNow()
		}
		if _, resp, err := e.tryDial(forwardedFor("8.8.8.8")); err == nil || resp.StatusCode != 429 {
			t.Errorf("spoofed XFF dodged the limit: err=%v resp=%v", err, resp)
		}
	})
	t.Run("trusted proxy: rightmost entry decides", func(t *testing.T) {
		e := newEnv(t, func(d *Deps) { d.TrustForwardedFor = true })
		for i := 0; i < 20; i++ {
			c, _, err := e.tryDial(forwardedFor("6.6.6." + string(rune('0'+i%10)) + ", 198.51.100.7"))
			if err != nil {
				t.Fatalf("upgrade %d: %v", i+1, err)
			}
			_ = c.CloseNow()
		}
		if _, resp, err := e.tryDial(forwardedFor("1.2.3.4, 198.51.100.7")); err == nil || resp.StatusCode != 429 {
			t.Errorf("rotating the spoofable entries dodged the limit: err=%v resp=%v", err, resp)
		}
		c, _, err := e.tryDial(forwardedFor("198.51.100.8"))
		if err != nil {
			t.Fatalf("another client behind the proxy must not be limited: %v", err)
		}
		_ = c.CloseNow()
	})
}

func TestUpgradeDisablesCompression(t *testing.T) {
	e := newEnv(t, nil)
	h := http.Header{}
	h.Set("Cookie", (&http.Cookie{Name: e.cookie.Name, Value: e.cookie.Value}).String())
	h.Set("Origin", testOrigin)
	h.Set("Sec-WebSocket-Extensions", "permessage-deflate")
	c, resp, err := websocket.Dial(t.Context(), e.url(), &websocket.DialOptions{HTTPHeader: h, CompressionMode: websocket.CompressionContextTakeover})
	if err != nil {
		t.Fatal(err)
	}
	defer c.CloseNow()
	if ext := resp.Header.Get("Sec-WebSocket-Extensions"); ext != "" {
		t.Errorf("server negotiated %q, want no compression", ext)
	}
}
