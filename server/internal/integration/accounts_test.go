package integration

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/AresiusXP/tutti-frutti/server/internal/api"
	"github.com/AresiusXP/tutti-frutti/server/internal/auth/authtest"
	"github.com/AresiusXP/tutti-frutti/server/internal/game"
	"github.com/AresiusXP/tutti-frutti/server/internal/room"
	"github.com/AresiusXP/tutti-frutti/server/internal/safety"
	"github.com/AresiusXP/tutti-frutti/server/internal/store"
)

// ---------------------------------------------------------------------------
// A browser: a cookie jar that talks to the current server and gives the
// WebSocket the same cookies.
// ---------------------------------------------------------------------------

type browser struct {
	t      *testing.T
	s      *stack
	client *http.Client
	p      *player
}

// newBrowser is a browser that has called GET /api/session (a guest cookie).
func (s *stack) newBrowser(name string) *browser {
	s.t.Helper()
	jar, err := cookiejar.New(nil)
	if err != nil {
		s.t.Fatal(err)
	}
	b := &browser{t: s.t, s: s, client: &http.Client{
		Jar:           jar,
		Timeout:       10 * time.Second,
		Transport:     &http.Transport{DisableKeepAlives: true},
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}}
	b.p = &player{name: name, avatar: game.Avatar{Emoji: "🍓", Color: "#ff0066"}}
	var session api.SessionInfo
	if r := b.do(http.MethodGet, "/api/session", ""); r.status != http.StatusOK {
		s.t.Fatalf("GET /api/session = %d", r.status)
	} else {
		r.jsonBody(s.t, &session)
	}
	if !session.Guest {
		s.t.Fatalf("session = %+v", session)
	}
	b.syncPlayer()
	return b
}

// use points the browser at a (restarted) server: cookies are per host, not
// per port, so the jar keeps working.
func (b *browser) use(s *stack) { b.s = s }

func (b *browser) cookies() []*http.Cookie {
	u, _ := url.Parse(b.s.srv.URL)
	return b.client.Jar.Cookies(u)
}

func (b *browser) cookie(name string) *http.Cookie {
	for _, c := range b.cookies() {
		if c.Name == name {
			return c
		}
	}
	return nil
}

// syncPlayer refreshes the Cookie header the WebSocket uses and the player id.
func (b *browser) syncPlayer() {
	var parts []string
	for _, c := range b.cookies() {
		parts = append(parts, (&http.Cookie{Name: c.Name, Value: c.Value}).String())
		if c.Name == "tf_guest" {
			b.p.id, _, _ = strings.Cut(c.Value, ".")
		}
	}
	b.p.cookie = strings.Join(parts, "; ")
}

func (b *browser) do(method, path, body string) response {
	b.t.Helper()
	var rd io.Reader
	if body != "" {
		rd = strings.NewReader(body)
	}
	req, err := http.NewRequest(method, b.s.srv.URL+path, rd)
	if err != nil {
		b.t.Fatal(err)
	}
	if method != http.MethodGet {
		req.Header.Set("Origin", b.s.origin)
	}
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := b.client.Do(req)
	if err != nil {
		b.t.Fatalf("%s %s: %v", method, path, err)
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	return response{status: resp.StatusCode, header: resp.Header, body: data}
}

// loginStart is the first leg of the Google login.
func (b *browser) loginStart(returnTo string) response {
	return b.do(http.MethodGet, "/api/auth/google/login?returnTo="+url.QueryEscape(returnTo), "")
}

// googleLogin runs the whole login as user and returns where the callback
// sent the browser.
func (b *browser) googleLogin(returnTo string, user authtest.User) string {
	b.t.Helper()
	start := b.loginStart(returnTo)
	if start.status != http.StatusFound {
		b.t.Fatalf("login start = %d %s", start.status, start.body)
	}
	cb, err := b.s.idp.Authorize(start.header.Get("Location"), user)
	if err != nil {
		b.t.Fatalf("the provider refused the request: %v", err)
	}
	u, _ := url.Parse(cb)
	r := b.do(http.MethodGet, u.RequestURI(), "")
	if r.status != http.StatusFound {
		b.t.Fatalf("callback = %d %s", r.status, r.body)
	}
	b.syncPlayer()
	return r.header.Get("Location")
}

func (b *browser) me() (api.UserInfo, int) {
	b.t.Helper()
	r := b.do(http.MethodGet, "/api/me", "")
	var me api.UserInfo
	if r.status == http.StatusOK {
		r.jsonBody(b.t, &me)
	}
	return me, r.status
}

func (b *browser) stats() api.Stats {
	b.t.Helper()
	r := b.do(http.MethodGet, "/api/me/stats", "")
	if r.status != http.StatusOK {
		b.t.Fatalf("GET /api/me/stats = %d %s", r.status, r.body)
	}
	var st api.Stats
	r.jsonBody(b.t, &st)
	return st
}

// waitStats waits for the statistics to reach want. While it waits it moves
// the clock, so that a recorder sleeping in its backoff retries.
func (b *browser) waitStats(want api.Stats) {
	b.t.Helper()
	deadline := time.Now().Add(waitLimit)
	var got api.Stats
	for {
		if got = b.stats(); got == want {
			return
		}
		if time.Now().After(deadline) {
			b.t.Fatalf("%s: stats = %+v, want %+v", b.p.name, got, want)
		}
		b.s.advance(time.Second)
		time.Sleep(2 * time.Millisecond)
	}
}

// ---------------------------------------------------------------------------
// A store whose statistics can be made to fail.
// ---------------------------------------------------------------------------

type flakyBackend struct {
	store.Backend

	mu       sync.Mutex
	err      error
	attempts []store.GameRecord
	recorded []store.GameRecord
}

func (f *flakyBackend) RecordGame(ctx context.Context, rec store.GameRecord) error {
	f.mu.Lock()
	f.attempts = append(f.attempts, rec)
	err := f.err
	f.mu.Unlock()
	if err != nil {
		return err
	}
	if err := f.Backend.RecordGame(ctx, rec); err != nil {
		return err
	}
	f.mu.Lock()
	f.recorded = append(f.recorded, rec)
	f.mu.Unlock()
	return nil
}

func (f *flakyBackend) setError(err error) {
	f.mu.Lock()
	f.err = err
	f.mu.Unlock()
}

func (f *flakyBackend) counts() (attempts, recorded int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.attempts), len(f.recorded)
}

func (f *flakyBackend) lastRecord() store.GameRecord {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.recorded[len(f.recorded)-1]
}

// persistedOf decodes the saved snapshot of a room, accounts included.
func persistedOf(t *testing.T, st store.Store, code string) room.Persisted {
	t.Helper()
	snaps, err := st.LoadAll(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, sn := range snaps {
		if sn.Code == code {
			var p room.Persisted
			if err := json.Unmarshal(sn.State, &p); err != nil {
				t.Fatal(err)
			}
			return p
		}
	}
	t.Fatalf("no snapshot of room %s", code)
	return room.Persisted{}
}

// playSecondRound plays round 2 of a two-round game from the round summary of
// round 1 and ends in the round summary of round 2: the letter is picked by
// the chooser's timeout and the answers are hand-computed (see
// TestFullGameThreePlayersTwoRounds).
//
//	Nombre: Ana "aa", Beto "AA" (the same), Cami "bb"  -> 5, 5, 10
//	Animal: cc, dd, (empty)                            -> 10, 10, 0
//	Cosa:   ee, ff, gg                                 -> 10, 10, 10
//
// Round 2 scores 25, 25 and 20.
func (tb *table) playSecondRound() {
	tb.t.Helper()
	tb.mustCmd(0, game.CmdNextRound, nil)
	tb.waitPhase(game.PhaseLetter)
	v := tb.anyView()
	letter := game.LetterAt(game.AvailableLetters(v.Settings.Lang, v.Settings.ExcludeHardLetters, v.UsedLetters), chooserFor)
	tb.s.advance(chooserFor)
	tb.waitAll("auto-stopped letter", func(v game.View) bool {
		return v.Phase == game.PhasePlaying && v.Round.Letter == letter
	})
	r2 := [3][3]string{
		{letter + "aa", letter + "cc", letter + "ee"},
		{letter + "AA", letter + "dd", letter + "ff"},
		{letter + "bb", "", letter + "gg"},
	}
	for i := range r2 {
		tb.answers(i, r2[i][:]...)
	}
	tb.mustCmd(1, game.CmdBasta, nil)
	tb.waitPhase(game.PhaseGrace)
	tb.s.advance(graceFor + lateFor)
	tb.waitPhase(game.PhaseReview)
	tb.mustCmd(0, game.CmdFinishReview, nil)
	tb.waitPhase(game.PhaseRoundSummary)
}

// castVotes casts the votes of round 1 (see r1Answers) whatever the id of the
// merge proposal is: after a rematch the engine keeps counting proposals.
func (tb *table) castVotes() {
	tb.t.Helper()
	ana, beto := tb.seats[0].p.id, tb.seats[1].p.id
	tb.mustCmd(0, game.CmdVoteReject, game.VoteReject{Category: 2, Target: beto})
	tb.mustCmd(2, game.CmdVoteReject, game.VoteReject{Category: 2, Target: beto})
	tb.mustCmd(0, game.CmdProposeMerge, game.ProposeMerge{Category: 0, PlayerA: ana, PlayerB: beto})
	id := tb.view(0).Round.Proposals[0].ID
	tb.mustCmd(2, game.CmdVoteMerge, game.VoteMerge{ProposalID: id, Yes: false})
	tb.mustCmd(1, game.CmdVoteMerge, game.VoteMerge{ProposalID: id, Yes: true})
	tb.waitAll("votes applied", func(v game.View) bool { return reviewTotals(v, tb.ids()...)[1] == r1Voted[1] })
}

func (tb *table) metric(result string) float64 {
	return tb.s.metricValue("tuttifrutti_stats_recorded_total", map[string]string{"result": result})
}

// TestAccountsEndToEnd runs the whole account story over real HTTP and
// WebSocket connections: Google login against a fake provider, a game of three
// players (two signed in as different users, one guest) with a rematch, and the
// statistics that come out of it, through a failing store, a restart in the
// middle of a game, a restart after the game and a user who deletes their
// account while the game is on.
func TestAccountsEndToEnd(t *testing.T) {
	forEachStore(t, func(t *testing.T, open func(*testing.T) store.Store) {
		backend, ok := open(t).(store.Backend)
		if !ok {
			t.Fatal("the store does not keep accounts")
		}
		flaky := &flakyBackend{Backend: backend}
		idp := authtest.New(t, "123-integration.apps.googleusercontent.com", "fake-integration-secret")
		s := newStack(t, stackOpts{store: flaky, idp: idp})

		anaB, betoB, camiB := s.newBrowser("Ana"), s.newBrowser("Beto"), s.newBrowser("Cami")
		googleAna := authtest.User{Sub: "100000000000000000001", Name: "Ana García"}
		googleCami := authtest.User{Sub: "100000000000000000002", Name: "Camila Fernández de Kirchner"}

		// ---- the session endpoint ----
		r := anaB.do(http.MethodGet, "/api/session", "")
		var session api.SessionInfo
		r.jsonBody(t, &session)
		if session != (api.SessionInfo{Guest: true, GoogleEnabled: true, LoggedIn: false, Terms: gateOff}) {
			t.Fatalf("session = %+v", session)
		}
		if _, status := anaB.me(); status != http.StatusUnauthorized {
			t.Fatalf("GET /api/me without a login = %d", status)
		}

		// ---- logins that do not happen ----
		start := anaB.loginStart("/")
		deny, _ := idp.Deny(start.header.Get("Location"))
		du, _ := url.Parse(deny)
		if r := anaB.do(http.MethodGet, du.RequestURI(), ""); r.status != http.StatusFound || r.header.Get("Location") != "/?login=cancelled" {
			t.Fatalf("a refused consent = %d %q", r.status, r.header.Get("Location"))
		}
		start = anaB.loginStart("/")
		cb, _ := idp.Authorize(start.header.Get("Location"), googleAna)
		cu, _ := url.Parse(cb)
		q := cu.Query()
		q.Set("state", "forged")
		if r := anaB.do(http.MethodGet, cu.Path+"?"+q.Encode(), ""); r.status != http.StatusFound || r.header.Get("Location") != "/?login=failed" {
			t.Fatalf("a forged state = %d %q", r.status, r.header.Get("Location"))
		}
		if _, status := anaB.me(); status != http.StatusUnauthorized {
			t.Fatalf("a failed login logged Ana in (%d)", status)
		}
		if got := s.metricValue("tuttifrutti_logins_total", map[string]string{"result": "cancelled"}); got != 1 {
			t.Errorf("logins_total{cancelled} = %v", got)
		}
		if got := s.metricValue("tuttifrutti_logins_total", map[string]string{"result": "failed"}); got != 1 {
			t.Errorf("logins_total{failed} = %v", got)
		}

		// ---- real logins ----
		if loc := anaB.googleLogin("/", googleAna); loc != "/" {
			t.Fatalf("Ana was sent to %q", loc)
		}
		if loc := camiB.googleLogin("/", googleCami); loc != "/" {
			t.Fatalf("Cami was sent to %q", loc)
		}
		anaMe, status := anaB.me()
		camiMe, status2 := camiB.me()
		if status != 200 || status2 != 200 || anaMe.ID == camiMe.ID || anaMe.Name != "Ana García" || camiMe.Name != "Camila Fernández" {
			t.Fatalf("users = %+v (%d) and %+v (%d)", anaMe, status, camiMe, status2)
		}
		if got := s.metricValue("tuttifrutti_logins_total", map[string]string{"result": "ok"}); got != 2 {
			t.Errorf("logins_total{ok} = %v", got)
		}
		if r := anaB.do(http.MethodPut, "/api/me/profile", `{"nickname":"Anita","avatar":{"emoji":"🍉","color":"#00aa55"}}`); r.status != http.StatusNoContent {
			t.Fatalf("PUT profile = %d %s", r.status, r.body)
		}
		if me, _ := anaB.me(); me.Profile == nil || me.Profile.Nickname != "Anita" || me.Profile.Avatar.Emoji != "🍉" {
			t.Fatalf("profile = %+v", me.Profile)
		}
		if st := anaB.stats(); st != (api.Stats{}) {
			t.Fatalf("a new user has stats %+v", st)
		}
		// The guest is not logged in and has no account.
		if _, status := betoB.me(); status != http.StatusUnauthorized {
			t.Fatalf("Beto (a guest) GET /api/me = %d", status)
		}
		if r := betoB.do(http.MethodGet, "/api/me/stats", ""); r.status != http.StatusUnauthorized {
			t.Fatalf("Beto (a guest) GET /api/me/stats = %d", r.status)
		}
		if betoB.cookie("tf_session") != nil {
			t.Fatal("a guest holds a session")
		}
		// Logging in did not change the player ids: the guest cookies stay.
		if anaB.p.id == "" || camiB.p.id == "" || anaB.p.id == betoB.p.id {
			t.Fatalf("player ids: %q %q %q", anaB.p.id, betoB.p.id, camiB.p.id)
		}

		// ---- game 1: the statistics store is down when the game ends ----
		flaky.setError(errors.New("database is down"))
		tb := newTableOf(t, s, 0, anaB.p, betoB.p, camiB.p)
		tb.advanceTo(stageSummary)
		tb.playSecondRound()
		ids := tb.ids()
		wantInts(t, "totals after round 2", totalScores(tb.anyView(), ids...), []int{50, 30, 40})

		// The snapshot already knows who is who (so a restart can record the game).
		tb.mustCmd(0, game.CmdNextRound, nil)
		tb.waitPhase(game.PhaseFinal)
		eventually(t, "the finished game in the snapshot", func() bool {
			p := persistedOf(t, s.store, tb.code)
			return p.GameID != "" && len(p.Users) == 2
		})
		if p := persistedOf(t, s.store, tb.code); p.Users[anaB.p.id] != anaMe.ID || p.Users[camiB.p.id] != camiMe.ID || p.Users[betoB.p.id] != "" {
			t.Fatalf("snapshot users = %v, want Ana and Cami (and not the guest)", p.Users)
		}
		eventually(t, "a first failed attempt", func() bool { a, _ := flaky.counts(); return a >= 1 })
		if st := anaB.stats(); st != (api.Stats{}) {
			t.Fatalf("Ana's stats = %+v before the store recovered", st)
		}

		// The game does not wait for the statistics: the rematch works at once.
		tb.mustCmd(0, game.CmdRematch, nil)
		tb.waitPhase(game.PhaseLobby)
		if tb.metric("error") < 1 {
			t.Errorf("stats_recorded_total{error} = %v after a failed attempt", tb.metric("error"))
		}

		flaky.setError(nil)
		anaGame1 := api.Stats{GamesPlayed: 1, Wins: 1, TotalPoints: 50, BestRound: 25, UniqueAnswers: 4}
		camiGame1 := api.Stats{GamesPlayed: 1, Wins: 0, TotalPoints: 40, BestRound: 20, UniqueAnswers: 4}
		anaB.waitStats(anaGame1)
		camiB.waitStats(camiGame1)
		if a, n := flaky.counts(); a < 2 || n != 1 {
			t.Errorf("attempts %d recorded %d: want at least one retry and exactly one recorded game", a, n)
		}
		rec := flaky.lastRecord()
		if len(rec.Results) != 2 || rec.Results[0].UserID != anaMe.ID || rec.Results[1].UserID != camiMe.ID {
			t.Fatalf("the record = %+v: it must hold Ana and Cami, and nobody else (Beto is a guest)", rec)
		}
		if tb.metric("ok") != 1 || tb.metric("dropped") != 0 {
			t.Errorf("stats_recorded_total ok=%v dropped=%v", tb.metric("ok"), tb.metric("dropped"))
		}
		game1 := rec.ID

		// ---- game 2: the server restarts in the middle of the review ----
		tb.stage = stageCreated
		tb.advanceTo(stageReview)
		tb.castVotes()
		eventually(t, "the links in the snapshot", func() bool { return len(persistedOf(t, s.store, tb.code).Users) == 2 })
		tb.restart(30 * time.Second)
		s = tb.s
		for _, b := range []*browser{anaB, betoB, camiB} {
			b.use(s)
		}
		// Only the host comes back. Cami never reconnects: whether she gets her
		// statistics depends on the link the snapshot kept.
		tb.reconnect(0)
		tb.mustCmd(0, game.CmdFinishReview, nil)
		tb.waitPhase(game.PhaseRoundSummary)
		tb.mustCmd(0, game.CmdEndGame, nil) // the host ends the game after one round
		tb.waitPhase(game.PhaseFinal)

		anaGame2 := api.Stats{GamesPlayed: 2, Wins: 2, TotalPoints: 75, BestRound: 25, UniqueAnswers: 6}
		camiGame2 := api.Stats{GamesPlayed: 2, Wins: 0, TotalPoints: 60, BestRound: 20, UniqueAnswers: 6}
		anaB.waitStats(anaGame2)
		camiB.waitStats(camiGame2)
		rec = flaky.lastRecord()
		if rec.ID == game1 {
			t.Error("the rematch reused the id of the first game")
		}
		if len(rec.Results) != 2 || rec.Results[0].Won != true || rec.Results[1].Won != false {
			t.Errorf("game 2 record = %+v", rec)
		}

		// ---- the server restarts after the game was committed: nothing to do ----
		eventually(t, "the snapshot to forget the committed game", func() bool {
			p := persistedOf(t, s.store, tb.code)
			return p.GameID == rec.ID && len(p.Pending) == 0
		})
		attemptsBefore, _ := flaky.counts()
		tb.restart(time.Minute)
		s = tb.s
		for _, b := range []*browser{anaB, betoB, camiB} {
			b.use(s)
		}
		s.flush()
		s.advance(time.Minute)
		time.Sleep(20 * time.Millisecond) // a record offered by the restore would have been delivered by now
		if a, _ := flaky.counts(); a != attemptsBefore {
			t.Errorf("attempts after the restart = %d, want %d: a committed game is not recorded again", a, attemptsBefore)
		}
		if tb.metric("ok") != 0 {
			t.Errorf("the new process recorded %v games", tb.metric("ok"))
		}
		if st := anaB.stats(); st != anaGame2 {
			t.Errorf("Ana's stats after the restart = %+v, want %+v", st, anaGame2)
		}
		if st := camiB.stats(); st != camiGame2 {
			t.Errorf("Cami's stats after the restart = %+v, want %+v", st, camiGame2)
		}

		// ---- game 3: Cami deletes her account while the game is on ----
		tb.rejoinAll()
		tb.mustCmd(0, game.CmdRematch, nil)
		tb.waitPhase(game.PhaseLobby)
		tb.stage = stageCreated
		tb.advanceTo(stageReview)
		tb.mustCmd(0, game.CmdFinishReview, nil) // no votes: Ana 25, Beto 15, Cami 20
		tb.waitPhase(game.PhaseRoundSummary)
		if r := camiB.do(http.MethodDelete, "/api/me", ""); r.status != http.StatusNoContent {
			t.Fatalf("DELETE /api/me = %d %s", r.status, r.body)
		}
		if camiB.cookie("tf_session") != nil {
			t.Error("the session cookie survived the deletion")
		}
		if _, status := camiB.me(); status != http.StatusUnauthorized {
			t.Errorf("GET /api/me after the deletion = %d", status)
		}
		tb.mustCmd(0, game.CmdEndGame, nil)
		tb.waitPhase(game.PhaseFinal)
		anaGame3 := api.Stats{GamesPlayed: 3, Wins: 3, TotalPoints: 100, BestRound: 25, UniqueAnswers: 8}
		anaB.waitStats(anaGame3)
		if tb.metric("error") != 0 || tb.metric("dropped") != 0 {
			t.Errorf("a deleted user is skipped silently: error=%v dropped=%v", tb.metric("error"), tb.metric("dropped"))
		}
		if _, err := backend.UserStats(context.Background(), camiMe.ID); !errors.Is(err, store.ErrUserNotFound) {
			t.Errorf("Cami's statistics survived the deletion: %v", err)
		}
		// The same Google account starts over.
		camiB.googleLogin("/", googleCami)
		again, _ := camiB.me()
		if again.ID == camiMe.ID || again.Profile != nil || camiB.stats() != (api.Stats{}) {
			t.Errorf("the new account %+v (stats %+v) is not empty", again, camiB.stats())
		}

		// ---- Beto, the guest, was never affected ----
		if _, status := betoB.me(); status != http.StatusUnauthorized {
			t.Errorf("Beto GET /api/me = %d", status)
		}
		for _, att := range flaky.attempts {
			for _, res := range att.Results {
				if res.UserID == betoB.p.id || res.UserID == "" {
					t.Errorf("a record mentions the guest: %+v", res)
				}
			}
		}
	})
}

// gateOff is the terms status of a server without the terms gate (these
// harnesses do not enable it): the version is reported, nothing is required.
var gateOff = api.TermsStatus{RequiredVersion: safety.TermsVersion}
