package ws_test

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/AresiusXP/tutti-frutti/server/internal/auth"
	"github.com/AresiusXP/tutti-frutti/server/internal/httpapi"
	"github.com/AresiusXP/tutti-frutti/server/internal/protocol"
	"github.com/AresiusXP/tutti-frutti/server/internal/room"
	"github.com/AresiusXP/tutti-frutti/server/internal/store"
	"github.com/AresiusXP/tutti-frutti/server/internal/ws"
)

// TestSmokeWithTheRealHub drives the transport against the real room.Hub
// (REST create, WebSocket join, reconnect replacement, drain on shutdown) so a
// contract mismatch between the two layers shows up here.
func TestSmokeWithTheRealHub(t *testing.T) {
	const origin = "https://tuttifrutti.aresius.xyz"
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	hub := room.NewHub(room.Config{Store: store.NewMemory(), Logger: logger})
	if _, err := hub.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = hub.Shutdown(ctx)
	})

	guests := auth.NewGuests([]byte("secret"), false, logger)
	api := httpapi.New(httpapi.Options{Logger: logger, Rooms: hub, Guests: guests, AllowedOrigins: []string{origin}})
	api.Mux().Handle("GET /ws", ws.NewHandler(ws.Deps{
		Rooms: hub, Guests: guests, AllowedOrigins: []string{origin}, Logger: logger, Registerer: api.Registry(),
	}))
	srv := httptest.NewServer(api.Handler())
	defer srv.Close()

	req, _ := http.NewRequest(http.MethodPost, srv.URL+"/api/rooms",
		strings.NewReader(`{"name":"Ana","avatar":{"emoji":"🍓","color":"#ff0066"}}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", origin)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	var created struct{ Code string }
	_ = json.NewDecoder(resp.Body).Decode(&created)
	resp.Body.Close()
	if resp.StatusCode != http.StatusCreated || len(created.Code) != 4 {
		t.Fatalf("create = %d %+v", resp.StatusCode, created)
	}
	cookie := resp.Cookies()[0]

	dial := func() *websocket.Conn {
		t.Helper()
		h := http.Header{}
		h.Set("Cookie", (&http.Cookie{Name: cookie.Name, Value: cookie.Value}).String())
		h.Set("Origin", origin)
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		c, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(srv.URL, "http")+"/ws", &websocket.DialOptions{HTTPHeader: h})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = c.CloseNow() })
		return c
	}
	write := func(c *websocket.Conn, s string) {
		t.Helper()
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := c.Write(ctx, websocket.MessageText, []byte(s)); err != nil {
			t.Fatal(err)
		}
	}
	next := func(c *websocket.Conn) protocol.Envelope {
		t.Helper()
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_, data, err := c.Read(ctx)
		if err != nil {
			t.Fatalf("read: %v", err)
		}
		var env protocol.Envelope
		if err := json.Unmarshal(data, &env); err != nil {
			t.Fatal(err)
		}
		return env
	}
	join := `{"t":"join","v":1,"d":{"code":"` + created.Code + `","name":"Ana","avatar":{"emoji":"🍓","color":"#ff0066"}}}`

	a := dial()
	write(a, join)
	var sawState bool
	for i := 0; i < 3 && !sawState; i++ {
		sawState = next(a).T == protocol.TypeState
	}
	if !sawState {
		t.Fatal("no state frame after join")
	}
	write(a, `{"t":"ping","v":1}`)
	for {
		if next(a).T == protocol.TypePong {
			break
		}
	}

	// The same guest reconnecting replaces the first socket.
	b := dial()
	write(b, join)
	for {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		_, _, err := a.Read(ctx)
		cancel()
		if err != nil {
			if websocket.CloseStatus(err) != protocol.CloseReplaced {
				t.Fatalf("first socket ended with %v, want close 4001", err)
			}
			break
		}
	}

	// Shutdown drains the remaining socket with 1012.
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := hub.Shutdown(ctx); err != nil {
		t.Fatal(err)
	}
	for {
		rctx, rcancel := context.WithTimeout(context.Background(), 5*time.Second)
		_, _, err := b.Read(rctx)
		rcancel()
		if err != nil {
			if websocket.CloseStatus(err) != protocol.CloseServiceRestart {
				t.Fatalf("second socket ended with %v, want close 1012", err)
			}
			break
		}
	}
	deadline := time.Now().Add(5 * time.Second)
	for hub.Stats().Connections != 0 {
		if time.Now().After(deadline) {
			t.Fatalf("hub still counts %d connections", hub.Stats().Connections)
		}
		time.Sleep(5 * time.Millisecond)
	}
}
