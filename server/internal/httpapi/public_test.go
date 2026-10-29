package httpapi

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/AresiusXP/tutti-frutti/server/internal/api"
	"github.com/AresiusXP/tutti-frutti/server/internal/game"
)

func TestPublicRoomCreationVisibility(t *testing.T) {
	for _, visibility := range []string{"", "private", "public", "PUBLIC", "friends"} {
		t.Run(visibility, func(t *testing.T) {
			e := newRESTEnv(t, nil)
			body := strings.TrimSuffix(validBody, "}") + `,"visibility":"` + visibility + `"}`
			rec := e.post(body)
			if visibility != "" && visibility != "private" && visibility != "public" {
				if rec.Code != http.StatusBadRequest || len(e.rooms.created) != 0 {
					t.Fatalf("invalid visibility: %d %s, creates %v", rec.Code, rec.Body, e.rooms.created)
				}
				return
			}
			if rec.Code != http.StatusCreated || len(e.rooms.created) != 1 || e.rooms.created[0].Visibility != visibility {
				t.Fatalf("visibility not forwarded: %d %s, creates %v", rec.Code, rec.Body, e.rooms.created)
			}
		})
	}
}

func TestPublicRooms(t *testing.T) {
	for _, lang := range []string{"", "es", "en", "nl", "fr", "EN"} {
		t.Run(lang, func(t *testing.T) {
			e := newRESTEnv(t, nil)
			e.rooms.public = []api.PublicRoom{{Code: "KT7P", Lang: game.LangEN, Players: 1, MaxPlayers: 10, CategoryLabels: []string{"Name"}, Rounds: 5, TimeLimitSeconds: 90}}
			rec := e.do(http.MethodGet, "/api/rooms?lang="+lang, "")
			if lang != "" && !game.Lang(lang).Valid() {
				if rec.Code != http.StatusBadRequest || errorCode(t, rec) != "invalid_lang" || len(e.rooms.browsed) != 0 {
					t.Fatalf("invalid filter: %d %s", rec.Code, rec.Body)
				}
				return
			}
			if rec.Code != http.StatusOK || rec.Header().Get("Cache-Control") != "no-store" {
				t.Fatalf("listing: %d %s", rec.Code, rec.Body)
			}
			if len(e.rooms.browsed) != 1 || e.rooms.browsed[0] != game.Lang(lang) {
				t.Fatalf("filter not forwarded: %v", e.rooms.browsed)
			}
			var got api.PublicRoomsResponse
			if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil || len(got.Rooms) != 1 || got.Rooms[0].Code != "KT7P" {
				t.Fatalf("listing body: %s (%v)", rec.Body, err)
			}
			if len(rec.Result().Cookies()) != 0 {
				t.Fatal("browsing must not mint identity")
			}
		})
	}
}

func TestPublicRoomsEmptyAndDraining(t *testing.T) {
	e := newRESTEnv(t, nil)
	rec := e.do(http.MethodGet, "/api/rooms", "")
	if rec.Code != http.StatusOK || strings.TrimSpace(rec.Body.String()) != `{"rooms":[]}` {
		t.Fatalf("empty listing: %d %s", rec.Code, rec.Body)
	}
	e.srv.SetReady(false)
	rec = e.do(http.MethodGet, "/api/rooms", "")
	if rec.Code != http.StatusServiceUnavailable || rec.Header().Get("Retry-After") != "2" || errorCode(t, rec) != "server_busy" {
		t.Fatalf("drain listing: %d %s", rec.Code, rec.Body)
	}
}

func TestPublicRoomsIndependentRateLimit(t *testing.T) {
	e := newRESTEnv(t, func(o *Options) { o.Limits.BrowseBurst = 1; o.Limits.BrowseRate = 1 })
	if r := e.do(http.MethodGet, "/api/rooms", ""); r.Code != http.StatusOK {
		t.Fatalf("first browse: %d", r.Code)
	}
	if r := e.do(http.MethodGet, "/api/rooms", ""); r.Code != http.StatusTooManyRequests || r.Header().Get("Retry-After") == "" {
		t.Fatalf("second browse: %d %v", r.Code, r.Header())
	}
	if r := e.do(http.MethodGet, "/api/rooms/KT7P", ""); r.Code != http.StatusOK {
		t.Fatalf("browsing consumed reconnect budget: %d", r.Code)
	}
	if r := e.post(validBody); r.Code != http.StatusCreated {
		t.Fatalf("browsing consumed creation budget: %d", r.Code)
	}
}

func TestPublicRoomsHubDraining(t *testing.T) {
	e := newRESTEnv(t, func(o *Options) { o.Rooms = drainingRooms{&fakeRooms{}} })
	r := e.do(http.MethodGet, "/api/rooms", "")
	if r.Code != http.StatusServiceUnavailable || r.Header().Get("Retry-After") != "2" || errorCode(t, r) != "server_busy" {
		t.Fatalf("hub draining: %d %s", r.Code, r.Body)
	}
}
