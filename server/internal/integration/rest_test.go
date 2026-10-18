package integration

import (
	"bytes"
	"encoding/json"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/AresiusXP/tutti-frutti/server/internal/game"
	"github.com/AresiusXP/tutti-frutti/server/internal/httpapi"
	"github.com/AresiusXP/tutti-frutti/server/internal/protocol"
)

const validBody = `{"name":"Ana","avatar":{"emoji":"🍓","color":"#ff0066"}}`

// TestCreateRoomRequestGuards: POST /api/rooms refuses what it should before a
// room is created, with stable error codes.
func TestCreateRoomRequestGuards(t *testing.T) {
	s := newStack(t, stackOpts{})
	p := s.newPlayer("Ana")

	long := `{"name":"` + strings.Repeat("n", 17) + `","avatar":{"emoji":"🍓","color":"#f06"}}`
	invisible := `{"name":"\u200b\u200b\u200d","avatar":{"emoji":"🍓","color":"#f06"}}`
	hugeAvatar := `{"name":"Ana","avatar":{"emoji":"` + strings.Repeat("🍓", 9) + `","color":"#f06"}}`
	oversized := `{"name":"Ana","avatar":{"emoji":"🍓","color":"` + strings.Repeat("c", 5000) + `"}}`

	cases := []struct {
		name   string
		body   string
		opts   []reqOpt
		status int
		code   string
	}{
		{"foreign origin", validBody, []reqOpt{withHeader("Origin", "https://evil.example")}, 403, "forbidden_origin"},
		{"missing origin", validBody, []reqOpt{withoutHeader("Origin")}, 403, "forbidden_origin"},
		{"null origin", validBody, []reqOpt{withHeader("Origin", "null")}, 403, "forbidden_origin"},
		{"text/plain", validBody, []reqOpt{withHeader("Content-Type", "text/plain")}, 415, "unsupported_media_type"},
		{"form data", validBody, []reqOpt{withHeader("Content-Type", "application/x-www-form-urlencoded")}, 415, "unsupported_media_type"},
		{"no content type", validBody, []reqOpt{withoutHeader("Content-Type")}, 415, "unsupported_media_type"},
		{"malformed JSON", `{"name":`, nil, 400, "bad_request"},
		{"not an object", `[1,2]`, nil, 400, "bad_request"},
		{"empty body", ``, nil, 400, "bad_request"},
		{"unknown field", `{"name":"Ana","avatar":{"emoji":"🍓","color":"#f06"},"admin":true}`, nil, 400, "bad_request"},
		{"trailing data", validBody + `{}`, nil, 400, "bad_request"},
		{"body over 4 KiB", oversized, nil, 413, "payload_too_large"},
		{"empty name", `{"name":"","avatar":{"emoji":"🍓","color":"#f06"}}`, nil, 400, game.CodeInvalidName},
		{"blank name", `{"name":"   ","avatar":{"emoji":"🍓","color":"#f06"}}`, nil, 400, game.CodeInvalidName},
		{"name of 17 characters", long, nil, 400, game.CodeInvalidName},
		{"name of invisible characters", invisible, nil, 400, game.CodeInvalidName},
		{"avatar too big", hugeAvatar, nil, 400, game.CodeInvalidAvatar},
		{"unknown language", `{"name":"Ana","avatar":{"emoji":"🍓","color":"#f06"},"lang":"fr"}`, nil, 400, "invalid_lang"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := s.do(http.MethodPost, "/api/rooms", []byte(tc.body), append([]reqOpt{withCookie(p)}, tc.opts...)...)
			if r.status != tc.status || r.errCode(t) != tc.code {
				t.Fatalf("POST /api/rooms = %d %s, want %d %q", r.status, r.body, tc.status, tc.code)
			}
			if got := s.hub.Stats().Rooms; got != 0 {
				t.Fatalf("a refused request created %d rooms", got)
			}
		})
	}

	// The happy paths: Spanish by default, English on request; the caller
	// without a cookie gets one and becomes the host.
	r := s.do(http.MethodPost, "/api/rooms", []byte(validBody), withCookie(p))
	var created struct{ Code string }
	r.jsonBody(t, &created)
	if r.status != http.StatusCreated || len(created.Code) != 4 || r.header.Get("Cache-Control") != "no-store" {
		t.Fatalf("POST /api/rooms = %d %s (cache-control %q)", r.status, r.body, r.header.Get("Cache-Control"))
	}
	if info, _ := s.roomInfo(created.Code); info.Lang != game.LangES || info.Players != 1 || info.Phase != game.PhaseLobby {
		t.Fatalf("new room info = %+v", info)
	}
	en := s.do(http.MethodPost, "/api/rooms", []byte(`{"name":"Bob","avatar":{"emoji":"🍌","color":"#fc0"},"lang":"en"}`))
	var createdEN struct{ Code string }
	en.jsonBody(t, &createdEN)
	if en.status != http.StatusCreated || !slices.ContainsFunc(cookieNames(en.header), func(n string) bool { return n == protocol.GuestCookie }) {
		t.Fatalf("POST /api/rooms without a cookie = %d, set-cookie %v", en.status, en.header["Set-Cookie"])
	}
	if info, _ := s.roomInfo(createdEN.Code); info.Lang != game.LangEN {
		t.Fatalf("English room info = %+v", info)
	}
	// The host of the room is the guest of the cookie: a socket with it is the host.
	hostCookie := strings.SplitN(en.header.Get("Set-Cookie"), ";", 2)[0]
	id, _, _ := strings.Cut(strings.TrimPrefix(hostCookie, protocol.GuestCookie+"="), ".")
	host := &player{name: "Bob", avatar: game.Avatar{Emoji: "🍌", Color: "#fc0"}, cookie: hostCookie, id: id}
	st := s.connect(host).join(createdEN.Code)
	if st.View.HostID != id || st.View.Settings.Lang != game.LangEN {
		t.Fatalf("host %q lang %q, want %q / en", st.View.HostID, st.View.Settings.Lang, id)
	}
	if got := st.View.CategoryLabels; len(got) != 6 || got[0] != "Name" || !slices.Contains(got, "Country") || !slices.Contains(got, "Brand") {
		t.Fatalf("English labels = %v", got)
	}
}

func cookieNames(h http.Header) []string {
	var out []string
	for _, c := range (&http.Response{Header: h}).Cookies() {
		out = append(out, c.Name)
	}
	return out
}

// TestRoomInfoEndpoint: GET /api/rooms/{code}.
func TestRoomInfoEndpoint(t *testing.T) {
	s := newStack(t, stackOpts{})
	p := s.newPlayer("Ana")
	code := s.createRoom(p, "")

	r := s.do(http.MethodGet, "/api/rooms/"+code, nil)
	var raw map[string]json.RawMessage
	r.jsonBody(t, &raw)
	keys := make([]string, 0, len(raw))
	for k := range raw {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	if r.status != 200 || !slices.Equal(keys, []string{"code", "joinable", "lang", "maxPlayers", "phase", "players"}) {
		t.Fatalf("info = %d %s", r.status, r.body)
	}
	if got := r.header.Get("Cache-Control"); got != "no-store" {
		t.Fatalf("Cache-Control = %q", got)
	}
	if info, ok := s.roomInfo(code); !ok || info.Code != code || info.Phase != game.PhaseLobby || info.Players != 1 ||
		info.MaxPlayers != game.MaxPlayers || info.Lang != game.LangES || !info.Joinable {
		t.Fatalf("info = %+v", info)
	}
	// The code is forgiving about case.
	if info, ok := s.roomInfo(strings.ToLower(code)); !ok || info.Code != code {
		t.Fatalf("lower-case lookup = %+v (found %v)", info, ok)
	}
	for _, bad := range []string{"BCDF", "ABC", "AEIO", "BCDFGH", "0000", "%20%20%20%20"} {
		if _, ok := s.roomInfo(bad); ok {
			t.Fatalf("room %q should not exist", bad)
		}
	}
	// Not under /api/rooms/: the API answers JSON 404s, not HTML.
	if r := s.do(http.MethodGet, "/api/nothing", nil); r.status != 404 || r.errCode(t) != "not_found" {
		t.Fatalf("GET /api/nothing = %d %s", r.status, r.body)
	}
}

// TestCategoriesEndpoint: GET /api/categories?lang= serves the 50-entry
// catalog in one language.
func TestCategoriesEndpoint(t *testing.T) {
	s := newStack(t, stackOpts{})
	type listing struct {
		Lang       string   `json:"lang"`
		Groups     []string `json:"groups"`
		Categories []struct {
			ID      string `json:"id"`
			Group   string `json:"group"`
			Label   string `json:"label"`
			Default bool   `json:"default"`
		} `json:"categories"`
	}
	get := func(query string) (response, listing) {
		r := s.do(http.MethodGet, "/api/categories"+query, nil)
		var l listing
		if r.status == 200 {
			r.jsonBody(t, &l)
		}
		return r, l
	}

	byLang := map[string]listing{}
	for _, tc := range []struct{ query, lang string }{{"", "es"}, {"?lang=es", "es"}, {"?lang=en", "en"}} {
		r, l := get(tc.query)
		if r.status != 200 || l.Lang != tc.lang || len(l.Categories) != 50 || len(l.Groups) == 0 {
			t.Fatalf("GET /api/categories%s = %d lang %q entries %d", tc.query, r.status, l.Lang, len(l.Categories))
		}
		if cc := r.header.Get("Cache-Control"); !strings.Contains(cc, "max-age") {
			t.Fatalf("Cache-Control = %q", cc)
		}
		defaults := 0
		seen := map[string]bool{}
		for _, c := range l.Categories {
			if c.ID == "" || c.Label == "" || c.Group == "" || seen[c.ID] || !slices.Contains(l.Groups, c.Group) {
				t.Fatalf("bad entry %+v", c)
			}
			seen[c.ID] = true
			if c.Default {
				defaults++
			}
		}
		if defaults != 6 {
			t.Fatalf("%d default categories, want 6", defaults)
		}
		byLang[tc.lang] = l
	}
	label := func(l listing, id string) string {
		for _, c := range l.Categories {
			if c.ID == id {
				return c.Label
			}
		}
		return ""
	}
	if label(byLang["es"], "pais") != "País" || label(byLang["en"], "pais") != "Country" ||
		label(byLang["es"], "nombre") != "Nombre" || label(byLang["en"], "nombre") != "Name" {
		t.Fatalf("labels es/en: %q %q %q %q", label(byLang["es"], "pais"), label(byLang["en"], "pais"),
			label(byLang["es"], "nombre"), label(byLang["en"], "nombre"))
	}
	if r, _ := get("?lang=fr"); r.status != 400 || r.errCode(t) != "invalid_lang" {
		t.Fatalf("lang=fr = %d %s", r.status, r.body)
	}
	// Every catalog id is accepted by the engine as a category of a lobby.
	var ids []game.CategoryRef
	for _, c := range byLang["en"].Categories[:10] {
		ids = append(ids, game.CategoryRef{ID: c.ID})
	}
	settings := roundSettings()
	settings.Categories = ids
	tb := newTable(t, s, "Ana", "Beto")
	tb.configure(settings)
}

// TestSessionEndpoint: GET /api/session mints the signed guest cookie once.
func TestSessionEndpoint(t *testing.T) {
	s := newStack(t, stackOpts{})
	r := s.do(http.MethodGet, "/api/session", nil)
	cookies := (&http.Response{Header: r.header}).Cookies()
	if r.status != 200 || len(cookies) != 1 {
		t.Fatalf("GET /api/session = %d, cookies %v", r.status, cookies)
	}
	c := cookies[0]
	if c.Name != protocol.GuestCookie || !c.HttpOnly || c.SameSite != http.SameSiteLaxMode || c.Path != "/" ||
		c.MaxAge != 365*24*60*60 || c.Secure || strings.Count(c.Value, ".") != 1 {
		t.Fatalf("cookie = %+v", c)
	}
	if got := r.header.Get("Cache-Control"); got != "no-store" {
		t.Fatalf("Cache-Control = %q", got)
	}
	// With a valid cookie nothing new is issued; a forged one is replaced.
	again := s.do(http.MethodGet, "/api/session", nil, withHeader("Cookie", c.Name+"="+c.Value))
	if len(again.header["Set-Cookie"]) != 0 {
		t.Fatalf("a valid session got a new cookie: %v", again.header["Set-Cookie"])
	}
	forged := s.do(http.MethodGet, "/api/session", nil, withHeader("Cookie", c.Name+"="+c.Value+"x"))
	if len(forged.header["Set-Cookie"]) != 1 {
		t.Fatalf("a forged cookie was not replaced: %v", forged.header)
	}
	if strings.HasPrefix(forged.header.Get("Set-Cookie"), c.Name+"="+strings.SplitN(c.Value, ".", 2)[0]) {
		t.Fatal("the forged cookie kept the player ID")
	}
}

// TestRESTRateLimits: room creation and room lookups are limited per client
// address, on the injected clock; the answer is 429 rate_limited + Retry-After.
func TestRESTRateLimits(t *testing.T) {
	s := newStack(t, stackOpts{
		limits:         httpapi.Limits{CreateRate: 1, CreateBurst: 2, LookupRate: 1, LookupBurst: 3},
		noForwardedFor: true,
	})
	p := s.newPlayer("Ana")

	var code string
	for i := 1; i <= 2; i++ {
		code = s.createRoom(p, "")
	}
	r := s.do(http.MethodPost, "/api/rooms", []byte(validBody), withCookie(p))
	if r.status != http.StatusTooManyRequests || r.errCode(t) != protocol.ErrRateLimited || r.header.Get("Retry-After") == "" {
		t.Fatalf("third create = %d %s retry-after %q", r.status, r.body, r.header.Get("Retry-After"))
	}
	s.advance(time.Second)
	if _, err := s.tryCreateRoom(p, ""); err != nil {
		t.Fatalf("create after the bucket refilled: %v", err)
	}

	for range 3 {
		if _, ok := s.roomInfo(code); !ok {
			t.Fatal("lookup failed")
		}
	}
	if r := s.do(http.MethodGet, "/api/rooms/"+code, nil); r.status != http.StatusTooManyRequests || r.errCode(t) != protocol.ErrRateLimited {
		t.Fatalf("fourth lookup = %d %s", r.status, r.body)
	}
}

// TestProbes: /healthz, /readyz (503 once the server starts draining) and
// /api/version.
func TestProbes(t *testing.T) {
	s := newStack(t, stackOpts{})
	for path, want := range map[string]string{"/healthz": "ok", "/readyz": "ok"} {
		if r := s.do(http.MethodGet, path, nil); r.status != 200 || !bytes.Equal(bytes.TrimSpace(r.body), []byte(want)) {
			t.Fatalf("GET %s = %d %q", path, r.status, r.body)
		}
	}
	if r := s.do(http.MethodGet, "/api/version", nil); r.status != 200 || !strings.Contains(string(r.body), `"integration"`) {
		t.Fatalf("GET /api/version = %d %s", r.status, r.body)
	}
	s.api.SetReady(false)
	if r := s.do(http.MethodGet, "/readyz", nil); r.status != http.StatusServiceUnavailable {
		t.Fatalf("GET /readyz while draining = %d", r.status)
	}
	if r := s.do(http.MethodGet, "/healthz", nil); r.status != 200 {
		t.Fatalf("GET /healthz while draining = %d", r.status)
	}
}
