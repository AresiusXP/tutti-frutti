package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/AresiusXP/tutti-frutti/server/internal/api"
	"github.com/AresiusXP/tutti-frutti/server/internal/game"
	"github.com/AresiusXP/tutti-frutti/server/internal/room"
	"github.com/AresiusXP/tutti-frutti/server/internal/store"
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

// switchedRooms is a hub whose public games can be switched off. Its
// PublicDirectory answers the list and the switch together, like the hub.
type switchedRooms struct {
	*fakeRooms
	on        bool
	calls     *int
	stateRead *int
}

func (s switchedRooms) PublicDirectory(lang game.Lang, _ string) ([]api.PublicRoom, bool) {
	*s.calls++
	return s.PublicRooms(lang), s.on
}

// PublicEnabled must not be needed: a second read could disagree with the list.
func (s switchedRooms) PublicEnabled() bool { *s.stateRead++; return s.on }

func TestPublicRoomsPaused(t *testing.T) {
	for _, on := range []bool{true, false} {
		t.Run(map[bool]string{true: "on", false: "off"}[on], func(t *testing.T) {
			// The fake lists a room even while off: the handler must not show it next to "paused".
			rooms := &fakeRooms{public: []api.PublicRoom{{Code: "KT7P", Lang: game.LangEN, Players: 1, MaxPlayers: 10, CategoryLabels: []string{"Name"}}}}
			calls, reads := 0, 0
			e := newRESTEnv(t, func(o *Options) { o.Rooms = switchedRooms{rooms, on, &calls, &reads} })
			rec := e.do(http.MethodGet, "/api/rooms?lang=en", "")
			if rec.Code != http.StatusOK {
				t.Fatalf("listing: %d %s", rec.Code, rec.Body)
			}
			if calls != 1 || reads != 0 || len(rooms.browsed) != 1 || rooms.browsed[0] != game.LangEN {
				t.Fatalf("the list and the switch must come from one PublicDirectory call: calls %d, separate reads %d, browsed %v", calls, reads, rooms.browsed)
			}
			body := strings.TrimSpace(rec.Body.String())
			if !on {
				if body != `{"rooms":[],"paused":true}` {
					t.Fatalf("paused listing: %s", body)
				}
				return
			}
			if strings.Contains(body, "paused") || !strings.Contains(body, "KT7P") {
				t.Fatalf("listing while on: %s", body)
			}
		})
	}
}

// Against the real hub: the directory and the anonymous lookup of a code follow
// the switch (a paused public room is not joinable for a caller without a seat).
func TestPublicSwitchWithTheRealHub(t *testing.T) {
	hub := room.NewHub(room.Config{Store: store.NewMemory()})
	t.Cleanup(func() { _ = hub.Shutdown(context.Background()) })
	e := newRESTEnv(t, func(o *Options) { o.Rooms = hub })
	create := func(visibility string) string {
		code, err := hub.Create(context.Background(), room.CreateRequest{PlayerID: "host-" + visibility, Name: "Host", Visibility: visibility})
		if err != nil {
			t.Fatal(err)
		}
		return code
	}
	public, private := create("public"), create("private")
	joinable := func(code string) bool {
		t.Helper()
		rec := e.do(http.MethodGet, "/api/rooms/"+code, "") // no cookie: the anonymous lookup
		var info api.RoomInfo
		if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &info) != nil {
			t.Fatalf("GET /api/rooms/%s: %d %s", code, rec.Code, rec.Body)
		}
		return info.Joinable
	}
	if !joinable(public) || !joinable(private) {
		t.Fatal("both rooms are joinable while public games are on")
	}
	if err := hub.SetPublicEnabled(context.Background(), false); err != nil {
		t.Fatal(err)
	}
	if joinable(public) {
		t.Error("an anonymous lookup reports a paused public room as joinable")
	}
	if !joinable(private) {
		t.Error("a private room must stay joinable while public games are off")
	}
	if rec := e.do(http.MethodGet, "/api/rooms", ""); strings.TrimSpace(rec.Body.String()) != `{"rooms":[],"paused":true}` {
		t.Errorf("directory while off: %d %s", rec.Code, rec.Body)
	}
	if err := hub.SetPublicEnabled(context.Background(), true); err != nil {
		t.Fatal(err)
	}
	if !joinable(public) {
		t.Error("the public room is joinable again once public games are back")
	}
}

func TestCreatePublicRoomWhilePaused(t *testing.T) {
	e := newRESTEnv(t, nil)
	e.rooms.create = func(context.Context, room.CreateRequest) (string, error) { return "", room.ErrPublicUnavailable }
	rec := e.post(strings.TrimSuffix(validBody, "}") + `,"visibility":"public"}`)
	// A 403, never a 5xx: it is the operator's decision, not a failure (and a
	// 5xx would feed TuttiFruttiHighErrorRate while the owner is away).
	if rec.Code != http.StatusForbidden || errorCode(t, rec) != api.ErrCodePublicUnavailable {
		t.Fatalf("public create while paused: %d %s, want 403 public_unavailable", rec.Code, rec.Body)
	}
}
