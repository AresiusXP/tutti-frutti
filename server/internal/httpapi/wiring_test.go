package httpapi_test

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/AresiusXP/tutti-frutti/server/internal/auth"
	"github.com/AresiusXP/tutti-frutti/server/internal/httpapi"
	"github.com/AresiusXP/tutti-frutti/server/internal/protocol"
	"github.com/AresiusXP/tutti-frutti/server/internal/room"
	"github.com/AresiusXP/tutti-frutti/server/internal/ws"
)

type hub struct {
	mu       sync.Mutex
	rooms    map[string]room.Info
	sessions []*session
}

type session struct {
	client      room.Client
	mu          sync.Mutex
	frames      []string
	disconnects int
}

func (s *session) Handle(t string, _ json.RawMessage) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.frames = append(s.frames, t)
}

func (s *session) Disconnect() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.disconnects++
}

func (s *session) seen() ([]string, int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.frames...), s.disconnects
}

func (h *hub) Create(_ context.Context, req room.CreateRequest) (string, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.rooms["KT7P"] = room.Info{Code: "KT7P", Phase: "lobby", Players: 1, MaxPlayers: 10, Lang: "es", Joinable: true}
	return "KT7P", nil
}

func (h *hub) Info(code string) (room.Info, bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	i, ok := h.rooms[code]
	return i, ok
}

func (h *hub) Connect(code string, c room.Client) (room.Session, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if _, ok := h.rooms[code]; !ok {
		return nil, room.ErrRoomNotFound
	}
	s := &session{client: c}
	h.sessions = append(h.sessions, s)
	return s, nil
}

func (h *hub) Stats() room.Stats { return room.Stats{} }

// TestWebSocketMountedOnTheRESTServer wires the pieces the way cmd/server does
// and walks the browser flow: session cookie, room creation, WebSocket join.
func TestWebSocketMountedOnTheRESTServer(t *testing.T) {
	const origin = "https://tuttifrutti.aresius.xyz"
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	h := &hub{rooms: map[string]room.Info{}}
	guests := auth.NewGuests([]byte("secret"), true, logger)
	allowed := []string{origin}

	api := httpapi.New(httpapi.Options{
		Logger: logger, Version: "test", Rooms: h, Guests: guests, AllowedOrigins: allowed,
	})
	api.Mux().Handle("GET /ws", ws.NewHandler(ws.Deps{
		Rooms: h, Guests: guests, AllowedOrigins: allowed, Logger: logger, Registerer: api.Registry(),
	}))
	srv := httptest.NewServer(api.Handler())
	defer srv.Close()

	// 1. POST /api/rooms mints the guest cookie and creates the room.
	req, _ := http.NewRequest(http.MethodPost, srv.URL+"/api/rooms",
		strings.NewReader(`{"name":"Ana","avatar":{"emoji":"🍓","color":"#f06"}}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", origin)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("POST /api/rooms = %d", resp.StatusCode)
	}
	var cookie *http.Cookie
	for _, c := range resp.Cookies() {
		if c.Name == protocol.GuestCookie {
			cookie = c
		}
	}
	if cookie == nil {
		t.Fatal("no guest cookie")
	}

	// 2. The WebSocket upgrade works through the logging/metrics middleware.
	hdr := http.Header{}
	hdr.Set("Cookie", (&http.Cookie{Name: cookie.Name, Value: cookie.Value}).String())
	hdr.Set("Origin", origin)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	c, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(srv.URL, "http")+"/ws", &websocket.DialOptions{HTTPHeader: hdr})
	if err != nil {
		t.Fatalf("dial /ws: %v", err)
	}
	defer c.CloseNow()
	if err := c.Write(ctx, websocket.MessageText, []byte(`{"t":"join","v":1,"d":{"code":"KT7P","name":"Ana"}}`)); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		h.mu.Lock()
		n := len(h.sessions)
		h.mu.Unlock()
		if n == 1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the join never reached the hub")
		}
		time.Sleep(5 * time.Millisecond)
	}
	if err := c.Write(ctx, websocket.MessageText, []byte(`{"t":"ping","v":1}`)); err != nil {
		t.Fatal(err)
	}
	if _, data, err := c.Read(ctx); err != nil || !strings.Contains(string(data), `"t":"pong"`) {
		t.Fatalf("pong = %s, %v", data, err)
	}
	h.sessions[0].client.Close(protocol.CloseKicked, "bye")
	if _, _, err := c.Read(ctx); websocket.CloseStatus(err) != protocol.CloseKicked {
		t.Fatalf("read after Close = %v, want close 4002", err)
	}
	for {
		frames, disconnects := h.sessions[0].seen()
		if disconnects == 1 {
			if len(frames) != 1 || frames[0] != "join" {
				t.Errorf("session saw %v, want [join]", frames)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("Disconnect was never called")
		}
		time.Sleep(5 * time.Millisecond)
	}

	// 3. The upgrade is counted as 101 and the ws metrics share the registry.
	mresp, err := http.Get(srv.URL + "/metrics")
	if err != nil {
		t.Fatal(err)
	}
	defer mresp.Body.Close()
	body, _ := io.ReadAll(mresp.Body)
	for _, want := range []string{
		`tuttifrutti_http_requests_total{code="101",method="GET",route="GET /ws"} 1`,
		`tuttifrutti_ws_closes_total{code="4002"} 1`,
		`tuttifrutti_ws_frames_total{dir="in",type="join"} 1`,
		`tuttifrutti_ws_frames_total{dir="out",type="pong"} 1`,
	} {
		if !strings.Contains(string(body), want) {
			t.Errorf("/metrics lacks %q", want)
		}
	}
}
