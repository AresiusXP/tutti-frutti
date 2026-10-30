package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/AresiusXP/tutti-frutti/server/internal/api"
	"github.com/AresiusXP/tutti-frutti/server/internal/auth"
	"github.com/AresiusXP/tutti-frutti/server/internal/catalog"
	"github.com/AresiusXP/tutti-frutti/server/internal/game"
	"github.com/AresiusXP/tutti-frutti/server/internal/protocol"
	"github.com/AresiusXP/tutti-frutti/server/internal/room"
)

const (
	prodOrigin = "https://tuttifrutti.aresius.xyz"
	devOrigin  = "http://localhost:5173"
)

type fakeRooms struct {
	mu      sync.Mutex
	create  func(ctx context.Context, req room.CreateRequest) (string, error)
	infos   map[string]room.Info
	created []room.CreateRequest
	looked  []string
	public  []api.PublicRoom
	browsed []game.Lang
}

func (f *fakeRooms) Create(ctx context.Context, req room.CreateRequest) (string, error) {
	f.mu.Lock()
	f.created = append(f.created, req)
	fn := f.create
	f.mu.Unlock()
	if fn != nil {
		return fn(ctx, req)
	}
	return "KT7P", nil
}

func (f *fakeRooms) Info(code string) (room.Info, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.looked = append(f.looked, code)
	info, ok := f.infos[code]
	return info, ok
}

func (f *fakeRooms) Connect(string, room.Client) (room.Session, error) {
	return nil, room.ErrRoomNotFound
}

func (f *fakeRooms) Stats() room.Stats { return room.Stats{} }

func (f *fakeRooms) PublicRooms(lang game.Lang) []api.PublicRoom {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.browsed = append(f.browsed, lang)
	return slices.Clone(f.public)
}

type restEnv struct {
	srv    *Server
	h      http.Handler
	rooms  *fakeRooms
	guests *auth.Guests
	logs   *bytes.Buffer
	now    *clock
}

type clock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *clock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *clock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = c.t.Add(d)
}

// kt7pInstance is the room instance id the fake hub reports for KT7P.
const kt7pInstance = "01020304-0506-4708-890a-0b0c0d0e0f10"

func newRESTEnv(t *testing.T, tweak func(*Options)) *restEnv {
	t.Helper()
	logs := &bytes.Buffer{}
	logger := slog.New(slog.NewTextHandler(logs, &slog.HandlerOptions{Level: slog.LevelDebug}))
	rooms := &fakeRooms{infos: map[string]room.Info{
		"KT7P": {Code: "KT7P", Phase: game.PhaseLobby, Players: 2, MaxPlayers: 10, Lang: game.LangES, Joinable: true, RoomInstanceID: kt7pInstance},
	}}
	guests := auth.NewGuests([]byte("test-secret"), true, logger)
	clk := &clock{t: time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)}
	opts := Options{
		Logger:         logger,
		Version:        "1.2.3",
		Rooms:          rooms,
		Guests:         guests,
		AllowedOrigins: []string{prodOrigin, devOrigin},
		Now:            clk.Now,
		// The tests pin small limits; production defaults are larger (shared IP).
		Limits: Limits{CreateRate: 10.0 / 60, CreateBurst: 5, LookupRate: 60.0 / 60, LookupBurst: 20},
	}
	if tweak != nil {
		tweak(&opts)
	}
	s := New(opts)
	return &restEnv{srv: s, h: s.Handler(), rooms: rooms, guests: guests, logs: logs, now: clk}
}

type reqOpt func(*http.Request)

func withHeader(k, v string) reqOpt { return func(r *http.Request) { r.Header.Set(k, v) } }
func withOrigin(o string) reqOpt    { return withHeader("Origin", o) }
func withRemote(addr string) reqOpt { return func(r *http.Request) { r.RemoteAddr = addr } }
func withCookie(c *http.Cookie) reqOpt {
	return func(r *http.Request) { r.AddCookie(c) }
}
func withJSON() reqOpt { return withHeader("Content-Type", "application/json") }

func (e *restEnv) do(method, target, body string, opts ...reqOpt) *httptest.ResponseRecorder {
	var rd io.Reader
	if body != "" {
		rd = strings.NewReader(body)
	}
	r := httptest.NewRequest(method, target, rd)
	for _, o := range opts {
		o(r)
	}
	rec := httptest.NewRecorder()
	e.h.ServeHTTP(rec, r)
	return rec
}

func (e *restEnv) post(body string, opts ...reqOpt) *httptest.ResponseRecorder {
	return e.do(http.MethodPost, "/api/rooms", body, append([]reqOpt{withOrigin(prodOrigin), withJSON()}, opts...)...)
}

func guestCookie(t *testing.T, rec *httptest.ResponseRecorder) *http.Cookie {
	t.Helper()
	for _, c := range rec.Result().Cookies() {
		if c.Name == protocol.GuestCookie {
			return c
		}
	}
	t.Fatalf("no %s cookie in response (headers %v)", protocol.GuestCookie, rec.Header())
	return nil
}

func errorCode(t *testing.T, rec *httptest.ResponseRecorder) string {
	t.Helper()
	var body map[string]string
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("body %q is not a JSON object of strings: %v", rec.Body.String(), err)
	}
	return body["error"]
}

const validBody = `{"name":"Ana","avatar":{"emoji":"🍓","color":"#ff0066"}}`

func TestSession(t *testing.T) {
	e := newRESTEnv(t, nil)

	t.Run("mints a guest cookie", func(t *testing.T) {
		rec := e.do(http.MethodGet, "/api/session", "")
		if rec.Code != http.StatusOK || strings.TrimSpace(rec.Body.String()) != `{"guest":true,"googleEnabled":false,"loggedIn":false,"terms":{"requiredVersion":"2026-10","acceptedVersion":null,"acceptanceRequired":false}}` {
			t.Fatalf("= %d %q", rec.Code, rec.Body.String())
		}
		if cc := rec.Header().Get("Cache-Control"); cc != "no-store" {
			t.Errorf("Cache-Control = %q, want no-store", cc)
		}
		if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
			t.Errorf("Content-Type = %q", ct)
		}
		c := guestCookie(t, rec)
		if !c.HttpOnly || !c.Secure || c.SameSite != http.SameSiteLaxMode || c.Path != "/" || c.MaxAge != 365*24*3600 {
			t.Errorf("cookie attributes: %+v", c)
		}
	})
	t.Run("keeps a valid cookie", func(t *testing.T) {
		first := guestCookie(t, e.do(http.MethodGet, "/api/session", ""))
		rec := e.do(http.MethodGet, "/api/session", "", withCookie(first))
		if rec.Code != http.StatusOK || len(rec.Result().Cookies()) != 0 {
			t.Errorf("= %d with cookies %v; want 200 and no new cookie", rec.Code, rec.Result().Cookies())
		}
	})
	t.Run("replaces a forged cookie", func(t *testing.T) {
		forged := &http.Cookie{Name: protocol.GuestCookie, Value: strings.Repeat("A", 22) + "." + strings.Repeat("A", 43)}
		rec := e.do(http.MethodGet, "/api/session", "", withCookie(forged))
		if got := guestCookie(t, rec); got.Value == forged.Value {
			t.Error("forged cookie was kept")
		}
	})
	t.Run("needs no origin (safe method)", func(t *testing.T) {
		if rec := e.do(http.MethodGet, "/api/session", "", withOrigin("https://evil.example")); rec.Code != http.StatusOK {
			t.Errorf("= %d, want 200: safe methods are not origin checked", rec.Code)
		}
	})
}

func TestCategories(t *testing.T) {
	e := newRESTEnv(t, nil)
	tests := []struct {
		name     string
		target   string
		wantCode int
		wantLang string
	}{
		{"default is es", "/api/categories", 200, "es"},
		{"es", "/api/categories?lang=es", 200, "es"},
		{"en", "/api/categories?lang=en", 200, "en"},
		{"nl", "/api/categories?lang=nl", 200, "nl"},
		{"empty lang is default", "/api/categories?lang=", 200, "es"},
		{"unknown lang", "/api/categories?lang=fr", 400, ""},
		{"uppercase is not accepted", "/api/categories?lang=EN", 400, ""},
		{"uppercase nl is not accepted", "/api/categories?lang=NL", 400, ""},
		{"region tag is not accepted", "/api/categories?lang=nl-NL", 400, ""},
		{"the language name is not accepted", "/api/categories?lang=dutch", 400, ""},
		{"junk", "/api/categories?lang=%00", 400, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := e.do(http.MethodGet, tt.target, "")
			if rec.Code != tt.wantCode {
				t.Fatalf("= %d %q, want %d", rec.Code, rec.Body.String(), tt.wantCode)
			}
			if tt.wantCode != 200 {
				if got := errorCode(t, rec); got != "invalid_lang" {
					t.Errorf("error = %q, want invalid_lang", got)
				}
				if cc := rec.Header().Get("Cache-Control"); strings.Contains(cc, "max-age") {
					t.Errorf("errors must not be cacheable, Cache-Control = %q", cc)
				}
				return
			}
			if cc := rec.Header().Get("Cache-Control"); cc != "public, max-age=3600" {
				t.Errorf("Cache-Control = %q", cc)
			}
			var got catalog.Listing
			if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
				t.Fatal(err)
			}
			want := catalog.ForLang(tt.wantLang)
			if got.Lang != tt.wantLang || len(got.Categories) != 80 || got.Categories[0] != want.Categories[0] {
				t.Errorf("listing lang=%q n=%d first=%+v", got.Lang, len(got.Categories), got.Categories[0])
			}
		})
	}
	t.Run("dutch listing", func(t *testing.T) {
		rec := e.do(http.MethodGet, "/api/categories?lang=nl", "")
		var got catalog.Listing
		if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
			t.Fatal(err)
		}
		want := catalog.ForLang("nl")
		if got.Lang != "nl" || !slices.Equal(got.Groups, want.Groups) || !slices.Equal(got.Categories, want.Categories) {
			t.Fatalf("listing = %+v", got)
		}
		for _, c := range got.Categories {
			if c.ID == "pais" && (c.Label != "Land" || !c.Default || c.Group != "lugares") {
				t.Errorf("pais = %+v", c)
			}
			if c.ID == "nombre" && c.Label != "Naam" {
				t.Errorf("nombre = %+v", c)
			}
		}
		if body := rec.Body.String(); !strings.HasPrefix(body, `{"lang":"nl","groups":["personas",`) ||
			!strings.Contains(body, `{"id":"pais","group":"lugares","label":"Land","default":true}`) {
			t.Errorf("body = %.200s", body)
		}
	})
	t.Run("needs no deps", func(t *testing.T) {
		rec := do(newTestServer().Handler(), http.MethodGet, "/api/categories?lang=en")
		if rec.Code != http.StatusOK {
			t.Errorf("= %d, want 200 on a bare server", rec.Code)
		}
	})
}

func TestCreateRoom(t *testing.T) {
	t.Run("creates a room and issues the cookie", func(t *testing.T) {
		e := newRESTEnv(t, nil)
		rec := e.post(validBody)
		if rec.Code != http.StatusCreated || strings.TrimSpace(rec.Body.String()) != `{"code":"KT7P"}` {
			t.Fatalf("= %d %q", rec.Code, rec.Body.String())
		}
		if cc := rec.Header().Get("Cache-Control"); cc != "no-store" {
			t.Errorf("Cache-Control = %q", cc)
		}
		c := guestCookie(t, rec)
		r := httptest.NewRequest(http.MethodGet, "/", nil)
		r.AddCookie(c)
		id, ok := e.guests.Identify(r)
		if !ok {
			t.Fatal("issued cookie does not verify")
		}
		if len(e.rooms.created) != 1 {
			t.Fatalf("Create called %d times", len(e.rooms.created))
		}
		got := e.rooms.created[0]
		want := room.CreateRequest{PlayerID: id, Name: "Ana", Avatar: game.Avatar{Emoji: "🍓", Color: "#ff0066"}}
		if got != want {
			t.Errorf("Create got %+v, want %+v", got, want)
		}
	})
	t.Run("reuses the guest id of a valid cookie", func(t *testing.T) {
		e := newRESTEnv(t, nil)
		c := guestCookie(t, e.do(http.MethodGet, "/api/session", ""))
		r := httptest.NewRequest(http.MethodGet, "/", nil)
		r.AddCookie(c)
		id, _ := e.guests.Identify(r)

		rec := e.post(validBody, withCookie(c))
		if rec.Code != http.StatusCreated {
			t.Fatalf("= %d", rec.Code)
		}
		if len(rec.Result().Cookies()) != 0 {
			t.Error("a valid cookie must not be re-issued")
		}
		if e.rooms.created[0].PlayerID != id {
			t.Errorf("PlayerID = %q, want the cookie's %q", e.rooms.created[0].PlayerID, id)
		}
	})
	t.Run("forged cookie is replaced", func(t *testing.T) {
		e := newRESTEnv(t, nil)
		forged := &http.Cookie{Name: protocol.GuestCookie, Value: strings.Repeat("B", 22) + "." + strings.Repeat("B", 43)}
		rec := e.post(validBody, withCookie(forged))
		if rec.Code != http.StatusCreated {
			t.Fatalf("= %d", rec.Code)
		}
		if guestCookie(t, rec).Value == forged.Value || e.rooms.created[0].PlayerID == strings.Repeat("B", 22) {
			t.Error("forged guest id was trusted")
		}
	})
	t.Run("passes the language through", func(t *testing.T) {
		for _, lang := range game.Langs() {
			e := newRESTEnv(t, nil)
			rec := e.post(`{"name":"Ana","avatar":{"emoji":"🍓","color":"#fff"},"lang":"` + string(lang) + `"}`)
			if rec.Code != http.StatusCreated || e.rooms.created[0].Lang != lang {
				t.Errorf("%s: = %d, lang %q", lang, rec.Code, e.rooms.created[0].Lang)
			}
		}
	})
	t.Run("honours the request context", func(t *testing.T) {
		e := newRESTEnv(t, nil)
		var ctxErr error
		e.rooms.create = func(ctx context.Context, _ room.CreateRequest) (string, error) {
			ctxErr = ctx.Err()
			return "KT7P", nil
		}
		e.post(validBody)
		if ctxErr != nil {
			t.Errorf("request context already done: %v", ctxErr)
		}
	})
}

func TestCreateRoomRejections(t *testing.T) {
	big := `{"name":"` + strings.Repeat("a", 5000) + `","avatar":{"emoji":"x","color":"y"}}`
	tests := []struct {
		name     string
		body     string
		opts     []reqOpt
		wantCode int
		wantErr  string
	}{
		{name: "no content type", body: validBody, opts: []reqOpt{withHeader("Content-Type", "")}, wantCode: 415, wantErr: "unsupported_media_type"},
		{name: "text/plain", body: validBody, opts: []reqOpt{withHeader("Content-Type", "text/plain")}, wantCode: 415, wantErr: "unsupported_media_type"},
		{name: "form encoded", body: validBody, opts: []reqOpt{withHeader("Content-Type", "application/x-www-form-urlencoded")}, wantCode: 415, wantErr: "unsupported_media_type"},
		{name: "garbage content type", body: validBody, opts: []reqOpt{withHeader("Content-Type", ";;;")}, wantCode: 415, wantErr: "unsupported_media_type"},
		{name: "json with charset is fine", body: validBody, opts: []reqOpt{withHeader("Content-Type", "application/json; charset=utf-8")}, wantCode: 201},
		{name: "json uppercase is fine", body: validBody, opts: []reqOpt{withHeader("Content-Type", "Application/JSON")}, wantCode: 201},
		{name: "unknown field", body: `{"name":"Ana","avatar":{"emoji":"x","color":"y"},"admin":true}`, wantCode: 400, wantErr: "bad_request"},
		{name: "unknown nested field", body: `{"name":"Ana","avatar":{"emoji":"x","color":"y","size":3}}`, wantCode: 400, wantErr: "bad_request"},
		{name: "malformed json", body: `{"name":`, wantCode: 400, wantErr: "bad_request"},
		{name: "empty body", body: "", wantCode: 400, wantErr: "bad_request"},
		{name: "array body", body: `[]`, wantCode: 400, wantErr: "bad_request"},
		{name: "wrong field type", body: `{"name":5}`, wantCode: 400, wantErr: "bad_request"},
		{name: "trailing json value", body: validBody + validBody, wantCode: 400, wantErr: "bad_request"},
		{name: "trailing garbage", body: validBody + " nope", wantCode: 400, wantErr: "bad_request"},
		{name: "trailing whitespace is fine", body: validBody + "\n  \n", wantCode: 201},
		{name: "invalid lang", body: `{"name":"Ana","avatar":{"emoji":"x","color":"y"},"lang":"fr"}`, wantCode: 400, wantErr: "invalid_lang"},
		{name: "uppercase lang", body: `{"name":"Ana","avatar":{"emoji":"x","color":"y"},"lang":"NL"}`, wantCode: 400, wantErr: "invalid_lang"},
		{name: "region lang", body: `{"name":"Ana","avatar":{"emoji":"x","color":"y"},"lang":"nl-NL"}`, wantCode: 400, wantErr: "invalid_lang"},
		{name: "body over 4 KiB", body: big, wantCode: 413, wantErr: "payload_too_large"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := newRESTEnv(t, nil)
			rec := e.post(tt.body, tt.opts...)
			if rec.Code != tt.wantCode {
				t.Fatalf("= %d %q, want %d", rec.Code, rec.Body.String(), tt.wantCode)
			}
			if tt.wantErr != "" {
				if got := errorCode(t, rec); got != tt.wantErr {
					t.Errorf("error = %q, want %q", got, tt.wantErr)
				}
				if len(e.rooms.created) != 0 {
					t.Error("a rejected request reached the hub")
				}
			}
		})
	}

	t.Run("a body of exactly 4 KiB is accepted", func(t *testing.T) {
		e := newRESTEnv(t, nil)
		pad := maxBodyBytes - len(`{"name":"","avatar":{"emoji":"x","color":"y"}}`)
		body := `{"name":"` + strings.Repeat("a", pad) + `","avatar":{"emoji":"x","color":"y"}}`
		if len(body) != maxBodyBytes {
			t.Fatalf("test body is %d bytes", len(body))
		}
		if rec := e.post(body); rec.Code != http.StatusCreated {
			t.Errorf("= %d %q", rec.Code, rec.Body.String())
		}
	})
}

func TestCreateRoomErrors(t *testing.T) {
	tests := []struct {
		name     string
		err      error
		wantCode int
		wantErr  string
	}{
		{"invalid name", game.ErrInvalidName, 400, "invalid_name"},
		{"invalid avatar", game.ErrInvalidAvatar, 400, "invalid_avatar"},
		{"wrapped engine error", fmt.Errorf("create: %w", game.ErrInvalidName), 400, "invalid_name"},
		{"too many rooms", room.ErrTooManyRooms, 503, "server_busy"},
		{"wrapped too many rooms", fmt.Errorf("hub: %w", room.ErrTooManyRooms), 503, "server_busy"},
		{"shutting down", room.ErrShuttingDown, 503, "server_busy"},
		{"unexpected", errors.New("db password is hunter2"), 500, "internal"},
		{"context canceled", context.Canceled, 500, "internal"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := newRESTEnv(t, nil)
			e.rooms.create = func(context.Context, room.CreateRequest) (string, error) { return "", tt.err }
			rec := e.post(validBody)
			if rec.Code != tt.wantCode || errorCode(t, rec) != tt.wantErr {
				t.Fatalf("= %d %q, want %d %s", rec.Code, rec.Body.String(), tt.wantCode, tt.wantErr)
			}
			if strings.Contains(rec.Body.String(), "hunter2") {
				t.Error("internal error text leaked to the client")
			}
			if tt.wantCode == 500 && !strings.Contains(e.logs.String(), "create room failed") {
				t.Errorf("internal errors must be logged, logs: %s", e.logs.String())
			}
		})
	}
}

// drainingRooms is a room.Rooms that is shutting down: it has forgotten its
// rooms (they live in the snapshot store and come back after the restart).
type drainingRooms struct{ *fakeRooms }

func (drainingRooms) ShuttingDown() bool { return true }

// While the server drains, an unknown room must read as "try again", never as
// "does not exist": the room is restored by the next pod and a 404 would make
// clients give up on a game that is still alive.
func TestRoomInfoWhileDraining(t *testing.T) {
	e := newRESTEnv(t, func(o *Options) {
		o.Rooms = drainingRooms{&fakeRooms{}}
	})
	rec := e.do(http.MethodGet, "/api/rooms/KT7P", "")
	if rec.Code != http.StatusServiceUnavailable || errorCode(t, rec) != "server_busy" {
		t.Fatalf("= %d %q, want 503 server_busy", rec.Code, rec.Body.String())
	}
	if ra := rec.Header().Get("Retry-After"); ra != "2" {
		t.Errorf("Retry-After = %q, want 2", ra)
	}
	// A malformed code is still a plain 404: it can never exist.
	if rec := e.do(http.MethodGet, "/api/rooms/nope", ""); rec.Code != http.StatusNotFound {
		t.Errorf("malformed code while draining = %d, want 404", rec.Code)
	}
}

func TestRoomInfo(t *testing.T) {
	tests := []struct {
		name       string
		path       string
		wantCode   int
		wantLookup string // "" means the hub must not be hit
	}{
		{"found", "/api/rooms/KT7P", 200, "KT7P"},
		{"lowercase", "/api/rooms/kt7p", 200, "KT7P"},
		{"mixed case", "/api/rooms/Kt7p", 200, "KT7P"},
		{"padded", "/api/rooms/%20KT7P%20", 200, "KT7P"},
		{"unknown but well formed", "/api/rooms/BBBB", 404, "BBBB"},
		{"too short", "/api/rooms/KT7", 404, ""},
		{"too long", "/api/rooms/KT7PX", 404, ""},
		{"vowel", "/api/rooms/KA7P", 404, ""},
		{"look-alike digit 0", "/api/rooms/K0TP", 404, ""},
		{"look-alike digit 1", "/api/rooms/K1TP", 404, ""},
		{"punctuation", "/api/rooms/KT-P", 404, ""},
		{"non-ascii", "/api/rooms/KT7%C3%91", 404, ""},
		{"nul byte", "/api/rooms/KT7%00", 404, ""},
		{"embedded space", "/api/rooms/K%20TP", 404, ""},
		{"trailing newline", "/api/rooms/KT7P%0A", 200, "KT7P"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := newRESTEnv(t, nil)
			rec := e.do(http.MethodGet, tt.path, "")
			if rec.Code != tt.wantCode {
				t.Fatalf("= %d %q, want %d", rec.Code, rec.Body.String(), tt.wantCode)
			}
			if cc := rec.Header().Get("Cache-Control"); cc != "no-store" {
				t.Errorf("Cache-Control = %q, want no-store", cc)
			}
			switch tt.wantCode {
			case 200:
				var info room.Info
				if err := json.Unmarshal(rec.Body.Bytes(), &info); err != nil {
					t.Fatal(err)
				}
				if info != e.rooms.infos["KT7P"] {
					t.Errorf("info = %+v", info)
				}
			case 404:
				if got := errorCode(t, rec); got != "room_not_found" {
					t.Errorf("error = %q", got)
				}
			}
			var looked string
			if len(e.rooms.looked) > 0 {
				looked = e.rooms.looked[0]
			}
			if looked != tt.wantLookup || len(e.rooms.looked) > 1 {
				t.Errorf("hub lookups = %v, want [%q]", e.rooms.looked, tt.wantLookup)
			}
		})
	}

	t.Run("info of a Dutch room", func(t *testing.T) {
		e := newRESTEnv(t, nil)
		e.rooms.infos["NL4D"] = room.Info{Code: "NL4D", Phase: game.PhaseLobby, Players: 1, MaxPlayers: 10, Lang: game.LangNL, Joinable: true}
		if body := e.do(http.MethodGet, "/api/rooms/NL4D", "").Body.String(); !strings.Contains(body, `"lang":"nl"`) {
			t.Errorf("body %s lacks the Dutch language", body)
		}
	})
	t.Run("info JSON shape", func(t *testing.T) {
		e := newRESTEnv(t, nil)
		body := e.do(http.MethodGet, "/api/rooms/KT7P", "").Body.String()
		for _, key := range []string{`"code":"KT7P"`, `"phase":"lobby"`, `"players":2`, `"maxPlayers":10`, `"lang":"es"`, `"joinable":true`, `"roomInstanceId":"` + kt7pInstance + `"`} {
			if !strings.Contains(body, key) {
				t.Errorf("body %s lacks %s", body, key)
			}
		}
	})
}

func TestRoomCodeNormalization(t *testing.T) {
	tests := []struct {
		in   string
		want string
		ok   bool
	}{
		{"KT7P", "KT7P", true},
		{"kt7p", "KT7P", true},
		{" kt7p\t", "KT7P", true},
		{"BCDF", "BCDF", true},
		{"2389", "2389", true},
		{"", "", false},
		{"KT7", "", false},
		{"KT7PP", "", false},
		{"KT7A", "", false},
		{"kt7ñ", "", false},
		{"K T7P", "", false},
	}
	for _, tt := range tests {
		got, ok := normalizeRoomCode(tt.in)
		if got != tt.want || ok != tt.ok {
			t.Errorf("normalizeRoomCode(%q) = %q, %v; want %q, %v", tt.in, got, ok, tt.want, tt.ok)
		}
	}
}

func TestOriginChecks(t *testing.T) {
	tests := []struct {
		name     string
		method   string
		origin   string // "-" = no header
		wantCode int
	}{
		{"POST allowed origin", http.MethodPost, prodOrigin, 201},
		{"POST second allowed origin", http.MethodPost, devOrigin, 201},
		{"POST host case-insensitive", http.MethodPost, "https://TuttiFrutti.Aresius.XYZ", 201},
		{"POST default port", http.MethodPost, "https://tuttifrutti.aresius.xyz:443", 201},
		{"POST missing origin", http.MethodPost, "-", 403},
		{"POST wrong host", http.MethodPost, "https://evil.example", 403},
		{"POST wrong scheme", http.MethodPost, "http://tuttifrutti.aresius.xyz", 403},
		{"POST wrong port", http.MethodPost, "https://tuttifrutti.aresius.xyz:8443", 403},
		{"POST null origin", http.MethodPost, "null", 403},
		{"POST suffix attack", http.MethodPost, "https://tuttifrutti.aresius.xyz.evil.example", 403},
		{"PUT missing origin", http.MethodPut, "-", 403},
		{"PATCH wrong origin", http.MethodPatch, "https://evil.example", 403},
		{"DELETE missing origin", http.MethodDelete, "-", 403},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := newRESTEnv(t, nil)
			opts := []reqOpt{withJSON()}
			if tt.origin != "-" {
				opts = append(opts, withOrigin(tt.origin))
			}
			rec := e.do(tt.method, "/api/rooms", validBody, opts...)
			if tt.wantCode == 403 {
				if rec.Code != 403 || errorCode(t, rec) != "forbidden_origin" {
					t.Fatalf("= %d %q, want 403 forbidden_origin", rec.Code, rec.Body.String())
				}
				if len(e.rooms.created) != 0 || len(rec.Result().Cookies()) != 0 {
					t.Error("a cross-origin request had side effects")
				}
				return
			}
			if rec.Code != tt.wantCode {
				t.Fatalf("= %d %q, want %d", rec.Code, rec.Body.String(), tt.wantCode)
			}
		})
	}

	t.Run("safe methods are not checked", func(t *testing.T) {
		e := newRESTEnv(t, nil)
		for _, m := range []string{http.MethodGet, http.MethodHead, http.MethodOptions} {
			if rec := e.do(m, "/api/rooms/KT7P", "", withOrigin("https://evil.example")); rec.Code == http.StatusForbidden {
				t.Errorf("%s was origin checked", m)
			}
			if rec := e.do(m, "/api/rooms/KT7P", ""); rec.Code == http.StatusForbidden {
				t.Errorf("%s without origin was refused", m)
			}
		}
	})
	t.Run("unsafe methods anywhere under /api are guarded", func(t *testing.T) {
		e := newRESTEnv(t, nil)
		if rec := e.do(http.MethodPost, "/api/auth/logout", ""); rec.Code != http.StatusForbidden {
			t.Errorf("= %d, want 403", rec.Code)
		}
	})
	t.Run("not active without REST dependencies", func(t *testing.T) {
		rec := do(newTestServer().Handler(), http.MethodPost, "/api/version")
		if rec.Code != http.StatusNotFound {
			t.Errorf("= %d, want the existing 404 behaviour", rec.Code)
		}
	})
	t.Run("empty allow list: same host accepted, others not", func(t *testing.T) {
		e := newRESTEnv(t, func(o *Options) { o.AllowedOrigins = nil })
		same := e.post(validBody, withOrigin("http://example.com"))
		if same.Code != http.StatusCreated {
			t.Errorf("same-host origin = %d, want 201 (httptest host is example.com)", same.Code)
		}
		if rec := e.post(validBody, withOrigin(prodOrigin)); rec.Code != http.StatusForbidden {
			t.Errorf("foreign origin = %d, want 403", rec.Code)
		}
	})
}

func TestRateLimits(t *testing.T) {
	post := func(e *restEnv, remote string, opts ...reqOpt) *httptest.ResponseRecorder {
		return e.post(validBody, append([]reqOpt{withRemote(remote)}, opts...)...)
	}
	get := func(e *restEnv, remote string, opts ...reqOpt) *httptest.ResponseRecorder {
		return e.do(http.MethodGet, "/api/rooms/KT7P", "", append([]reqOpt{withRemote(remote)}, opts...)...)
	}

	t.Run("POST /api/rooms 10/min burst 5", func(t *testing.T) {
		e := newRESTEnv(t, nil)
		for i := 0; i < 5; i++ {
			if rec := post(e, "203.0.113.1:1000"); rec.Code != http.StatusCreated {
				t.Fatalf("request %d = %d", i+1, rec.Code)
			}
		}
		rec := post(e, "203.0.113.1:1000")
		if rec.Code != http.StatusTooManyRequests || errorCode(t, rec) != "rate_limited" {
			t.Fatalf("6th request = %d %q, want 429 rate_limited", rec.Code, rec.Body.String())
		}
		if ra := rec.Header().Get("Retry-After"); ra != "6" {
			t.Errorf("Retry-After = %q, want 6", ra)
		}
		if len(e.rooms.created) != 5 {
			t.Errorf("hub saw %d creations, want 5", len(e.rooms.created))
		}
		// The port does not matter, another IP has its own bucket.
		if rec := post(e, "203.0.113.1:2000"); rec.Code != http.StatusTooManyRequests {
			t.Errorf("same IP other port = %d, want 429", rec.Code)
		}
		if rec := post(e, "203.0.113.2:1000"); rec.Code != http.StatusCreated {
			t.Errorf("other IP = %d, want 201", rec.Code)
		}
		e.now.Advance(6 * time.Second)
		if rec := post(e, "203.0.113.1:1000"); rec.Code != http.StatusCreated {
			t.Errorf("after refill = %d, want 201", rec.Code)
		}
	})
	t.Run("GET /api/rooms/{code} 60/min burst 20", func(t *testing.T) {
		e := newRESTEnv(t, nil)
		for i := 0; i < 20; i++ {
			if rec := get(e, "203.0.113.1:1000"); rec.Code != http.StatusOK {
				t.Fatalf("request %d = %d", i+1, rec.Code)
			}
		}
		rec := get(e, "203.0.113.1:1000")
		if rec.Code != http.StatusTooManyRequests || rec.Header().Get("Retry-After") != "1" {
			t.Fatalf("21st request = %d Retry-After=%q, want 429 and 1", rec.Code, rec.Header().Get("Retry-After"))
		}
		if len(e.rooms.looked) != 20 {
			t.Errorf("hub saw %d lookups, want 20", len(e.rooms.looked))
		}
		e.now.Advance(time.Second)
		if rec := get(e, "203.0.113.1:1000"); rec.Code != http.StatusOK {
			t.Errorf("after 1s = %d, want 200", rec.Code)
		}
	})
	t.Run("rejected lookups of unknown rooms still count", func(t *testing.T) {
		e := newRESTEnv(t, nil)
		for i := 0; i < 20; i++ {
			e.do(http.MethodGet, "/api/rooms/BBBB", "", withRemote("203.0.113.1:1"))
		}
		if rec := get(e, "203.0.113.1:1"); rec.Code != http.StatusTooManyRequests {
			t.Errorf("= %d, want 429", rec.Code)
		}
	})
	t.Run("the two limits are independent", func(t *testing.T) {
		e := newRESTEnv(t, nil)
		for i := 0; i < 5; i++ {
			post(e, "203.0.113.1:1")
		}
		if rec := get(e, "203.0.113.1:1"); rec.Code != http.StatusOK {
			t.Errorf("lookup after exhausting creations = %d", rec.Code)
		}
	})
	t.Run("X-Forwarded-For is ignored unless trusted", func(t *testing.T) {
		e := newRESTEnv(t, nil)
		for i := 0; i < 5; i++ {
			post(e, "203.0.113.1:1", withHeader("X-Forwarded-For", fmt.Sprintf("9.9.9.%d", i)))
		}
		if rec := post(e, "203.0.113.1:1", withHeader("X-Forwarded-For", "9.9.9.200")); rec.Code != http.StatusTooManyRequests {
			t.Errorf("spoofing XFF bypassed the limit: %d", rec.Code)
		}
	})
	t.Run("trusted: rightmost entry is the client, spoofed ones are ignored", func(t *testing.T) {
		e := newRESTEnv(t, func(o *Options) { o.TrustForwardedFor = true })
		proxy := "10.0.0.1:1"
		for i := 0; i < 5; i++ {
			// The proxy appends the real client (198.51.100.7); the attacker rotates the left part.
			rec := post(e, proxy, withHeader("X-Forwarded-For", fmt.Sprintf("9.9.9.%d, 198.51.100.7", i)))
			if rec.Code != http.StatusCreated {
				t.Fatalf("request %d = %d", i+1, rec.Code)
			}
		}
		if rec := post(e, proxy, withHeader("X-Forwarded-For", "9.9.9.200, 198.51.100.7")); rec.Code != http.StatusTooManyRequests {
			t.Errorf("rotating the spoofable part bypassed the limit: %d", rec.Code)
		}
		if rec := post(e, proxy, withHeader("X-Forwarded-For", "198.51.100.8")); rec.Code != http.StatusCreated {
			t.Errorf("a different real client = %d, want 201", rec.Code)
		}
	})
	t.Run("IPv6 addresses of one /64 share a bucket", func(t *testing.T) {
		e := newRESTEnv(t, nil)
		for i := 0; i < 5; i++ {
			post(e, fmt.Sprintf("[2001:db8:1:2::%x]:1", i+1))
		}
		if rec := post(e, "[2001:db8:1:2::ffff]:1"); rec.Code != http.StatusTooManyRequests {
			t.Errorf("= %d, want 429", rec.Code)
		}
		if rec := post(e, "[2001:db8:1:3::1]:1"); rec.Code != http.StatusCreated {
			t.Errorf("other /64 = %d, want 201", rec.Code)
		}
	})
	t.Run("origin is checked before the limiter", func(t *testing.T) {
		e := newRESTEnv(t, nil)
		for i := 0; i < 10; i++ {
			e.post(validBody, withRemote("203.0.113.1:1"), withOrigin("https://evil.example"))
		}
		if rec := post(e, "203.0.113.1:1"); rec.Code != http.StatusCreated {
			t.Errorf("forbidden requests consumed the budget: %d", rec.Code)
		}
	})
}

func TestRoutesNeedTheirDependencies(t *testing.T) {
	tests := []struct {
		name   string
		tweak  func(*Options)
		method string
		path   string
		want   int
	}{
		{"session without guests", func(o *Options) { o.Guests = nil }, http.MethodGet, "/api/session", 404},
		{"create without guests", func(o *Options) { o.Guests = nil }, http.MethodPost, "/api/rooms", 404},
		{"lookup without guests still works", func(o *Options) { o.Guests = nil }, http.MethodGet, "/api/rooms/KT7P", 200},
		{"lookup without rooms", func(o *Options) { o.Rooms = nil }, http.MethodGet, "/api/rooms/KT7P", 404},
		{"create without rooms", func(o *Options) { o.Rooms = nil }, http.MethodPost, "/api/rooms", 404},
		{"session without rooms still works", func(o *Options) { o.Rooms = nil }, http.MethodGet, "/api/session", 200},
		{"categories always", func(o *Options) { o.Rooms, o.Guests = nil, nil }, http.MethodGet, "/api/categories", 200},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := newRESTEnv(t, tt.tweak)
			rec := e.do(tt.method, tt.path, validBody, withOrigin(prodOrigin), withJSON())
			if rec.Code != tt.want {
				t.Errorf("= %d %q, want %d", rec.Code, rec.Body.String(), tt.want)
			}
		})
	}
}

func TestReadinessChecks(t *testing.T) {
	t.Run("all passing", func(t *testing.T) {
		var calls int
		e := newRESTEnv(t, func(o *Options) {
			o.ReadyChecks = []func(context.Context) error{
				func(context.Context) error { calls++; return nil },
				func(context.Context) error { calls++; return nil },
			}
		})
		rec := e.do(http.MethodGet, "/readyz", "")
		if rec.Code != http.StatusOK || rec.Body.String() != "ok" || calls != 2 {
			t.Errorf("= %d %q after %d checks", rec.Code, rec.Body.String(), calls)
		}
	})
	t.Run("failing check", func(t *testing.T) {
		e := newRESTEnv(t, func(o *Options) {
			o.ReadyChecks = []func(context.Context) error{
				func(context.Context) error { return errors.New("postgres://user:hunter2@db unreachable") },
			}
		})
		rec := e.do(http.MethodGet, "/readyz", "")
		if rec.Code != http.StatusServiceUnavailable || rec.Body.String() != "not ready" {
			t.Errorf("= %d %q, want 503 not ready", rec.Code, rec.Body.String())
		}
		if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/plain") {
			t.Errorf("Content-Type = %q", ct)
		}
		if strings.Contains(rec.Body.String(), "hunter2") {
			t.Error("the failure reason leaked into the response")
		}
		if !strings.Contains(e.logs.String(), "readiness check failed") {
			t.Error("the failure must be logged")
		}
		if live := e.do(http.MethodGet, "/healthz", ""); live.Code != http.StatusOK {
			t.Errorf("/healthz = %d: liveness must not depend on readiness checks", live.Code)
		}
	})
	t.Run("a failing check stops the others", func(t *testing.T) {
		var second bool
		e := newRESTEnv(t, func(o *Options) {
			o.ReadyChecks = []func(context.Context) error{
				func(context.Context) error { return errors.New("down") },
				func(context.Context) error { second = true; return nil },
			}
		})
		if rec := e.do(http.MethodGet, "/readyz", ""); rec.Code != http.StatusServiceUnavailable {
			t.Errorf("= %d", rec.Code)
		}
		if second {
			t.Error("checks after the first failure should not run")
		}
	})
	t.Run("checks get a 2s deadline", func(t *testing.T) {
		var remaining time.Duration
		e := newRESTEnv(t, func(o *Options) {
			o.ReadyChecks = []func(context.Context) error{
				func(ctx context.Context) error {
					dl, ok := ctx.Deadline()
					if !ok {
						return errors.New("no deadline")
					}
					remaining = time.Until(dl)
					return nil
				},
			}
		})
		if rec := e.do(http.MethodGet, "/readyz", ""); rec.Code != http.StatusOK {
			t.Fatalf("= %d %q", rec.Code, rec.Body.String())
		}
		if remaining <= time.Second || remaining > 2*time.Second {
			t.Errorf("deadline in %v, want about 2s", remaining)
		}
	})
	t.Run("a hanging check is cut off", func(t *testing.T) {
		e := newRESTEnv(t, func(o *Options) {
			o.ReadyChecks = []func(context.Context) error{
				func(ctx context.Context) error { <-ctx.Done(); return ctx.Err() },
			}
		})
		start := time.Now()
		rec := e.do(http.MethodGet, "/readyz", "")
		if rec.Code != http.StatusServiceUnavailable {
			t.Errorf("= %d, want 503", rec.Code)
		}
		if d := time.Since(start); d < time.Second || d > 4*time.Second {
			t.Errorf("took %v, want about 2s", d)
		}
	})
	t.Run("shutting down skips the checks", func(t *testing.T) {
		var calls int
		e := newRESTEnv(t, func(o *Options) {
			o.ReadyChecks = []func(context.Context) error{func(context.Context) error { calls++; return nil }}
		})
		e.srv.SetReady(false)
		rec := e.do(http.MethodGet, "/readyz", "")
		if rec.Code != http.StatusServiceUnavailable || rec.Body.String() != "shutting down" || calls != 0 {
			t.Errorf("= %d %q, %d calls", rec.Code, rec.Body.String(), calls)
		}
	})
}

func TestMetricsAndLoggingForREST(t *testing.T) {
	e := newRESTEnv(t, nil)
	cookie := guestCookie(t, e.do(http.MethodGet, "/api/session", ""))
	for _, code := range []string{"KT7P", "BBBB", "CCCC", "zzzz", "nonsense"} {
		e.do(http.MethodGet, "/api/rooms/"+code, "")
	}
	e.post(`{"name":"SECRETNICK","avatar":{"emoji":"x","color":"y"}}`, withCookie(cookie))
	e.post(validBody, withOrigin("https://evil.example"))

	body := e.do(http.MethodGet, "/metrics", "").Body.String()
	for _, want := range []string{
		`tuttifrutti_http_requests_total{code="200",method="GET",route="GET /api/session"} 1`,
		`tuttifrutti_http_requests_total{code="200",method="GET",route="GET /api/rooms/{code}"} 1`,
		`tuttifrutti_http_requests_total{code="404",method="GET",route="GET /api/rooms/{code}"} 4`,
		`tuttifrutti_http_requests_total{code="201",method="POST",route="POST /api/rooms"} 1`,
		`tuttifrutti_http_requests_total{code="403",method="POST",route="unmatched"} 1`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("/metrics lacks %q", want)
		}
	}
	for _, leaked := range []string{"BBBB", "CCCC", "zzzz", "nonsense", "KT7P"} {
		if strings.Contains(body, leaked) {
			t.Errorf("/metrics label cardinality leak: %q", leaked)
		}
	}

	logs := e.logs.String()
	for _, secret := range []string{cookie.Value, "SECRETNICK", "tf_guest"} {
		if strings.Contains(logs, secret) {
			t.Errorf("logs contain %q:\n%s", secret, logs)
		}
	}
	if !strings.Contains(logs, "http request") {
		t.Error("requests are no longer logged")
	}
}

func TestSwitchingProtocolsIsNotALatencySample(t *testing.T) {
	e := newRESTEnv(t, nil)
	e.srv.Mux().HandleFunc("GET /ws", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusSwitchingProtocols)
	})
	e.do(http.MethodGet, "/ws", "")
	e.do(http.MethodGet, "/api/version", "")

	body := e.do(http.MethodGet, "/metrics", "").Body.String()
	if !strings.Contains(body, `tuttifrutti_http_requests_total{code="101",method="GET",route="GET /ws"} 1`) {
		t.Error("the upgrade is not counted")
	}
	if strings.Contains(body, `tuttifrutti_http_request_duration_seconds_count{method="GET",route="GET /ws"}`) {
		t.Error("a hijacked connection's lifetime must not be observed as request latency")
	}
	if !strings.Contains(body, `tuttifrutti_http_request_duration_seconds_count{method="GET",route="GET /api/version"} 1`) {
		t.Error("ordinary requests must still be observed")
	}
}

func TestCustomRegistry(t *testing.T) {
	reg := prometheus.NewRegistry()
	extra := prometheus.NewCounter(prometheus.CounterOpts{Name: "tuttifrutti_test_extra_total", Help: "x"})
	reg.MustRegister(extra)
	extra.Inc()

	e := newRESTEnv(t, func(o *Options) { o.Registry = reg })
	if e.srv.Registry() != reg {
		t.Fatal("Registry() is not the registry passed in Options")
	}
	e.do(http.MethodGet, "/api/version", "")
	body := e.do(http.MethodGet, "/metrics", "").Body.String()
	for _, want := range []string{
		"tuttifrutti_test_extra_total 1",
		`tuttifrutti_http_requests_total{code="200",method="GET",route="GET /api/version"} 1`,
		"go_goroutines",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("/metrics lacks %q", want)
		}
	}
}
