package room

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/AresiusXP/tutti-frutti/server/internal/game"
	"github.com/AresiusXP/tutti-frutti/server/internal/store"
)

const (
	userA  = "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"
	userB  = "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb"
	userC  = "cccccccc-cccc-4ccc-8ccc-cccccccccccc"
	gameID = "11111111-1111-4111-8111-111111111111"
)

// cells builds a round result cell list from points (0, 5 or 10 each).
func cells(points ...int) []game.Cell {
	out := make([]game.Cell, len(points))
	for i, p := range points {
		out[i] = game.Cell{Points: p, Status: game.StatusValid}
		if p == 0 {
			out[i].Status = game.StatusEmpty
		}
	}
	return out
}

func roundOf(n int, players map[string][]int) game.RoundResult {
	r := game.RoundResult{Round: n, Letter: "A", Players: map[string]game.PlayerResult{}}
	for id, pts := range players {
		total := 0
		for _, p := range pts {
			total += p
		}
		r.Players[id] = game.PlayerResult{Cells: cells(pts...), Total: total}
	}
	return r
}

func finished(players []string, winners []string, history ...game.RoundResult) *game.Game {
	g := &game.Game{Phase: game.PhaseFinal, Winners: winners, History: history, Settings: game.DefaultSettings()}
	for _, id := range players {
		g.Players = append(g.Players, game.Player{ID: id})
	}
	return g
}

func TestBuildRecord(t *testing.T) {
	at := t0
	type want struct {
		ok      bool
		results []store.UserResult
	}
	tests := []struct {
		name  string
		g     *game.Game
		users map[string]string
		want  want
	}{
		{
			name: "two linked players, two rounds",
			g: finished([]string{"p1", "p2", "p3"}, []string{"p2"},
				roundOf(1, map[string][]int{"p1": {10, 10, 5}, "p2": {10, 5, 5}, "p3": {0, 0, 0}}),
				roundOf(2, map[string][]int{"p1": {5, 0, 0}, "p2": {10, 10, 10}, "p3": {5, 5, 5}})),
			users: map[string]string{"p1": userA, "p2": userB},
			want: want{true, []store.UserResult{
				{UserID: userA, Won: false, Points: 25 + 5, BestRound: 25, UniqueAnswers: 2},
				{UserID: userB, Won: true, Points: 20 + 30, BestRound: 30, UniqueAnswers: 4},
			}},
		},
		{
			name: "guests get nothing but still make a game of two",
			g: finished([]string{"p1", "p2"}, []string{"p1"},
				roundOf(1, map[string][]int{"p1": {10, 10}, "p2": {5, 5}})),
			users: map[string]string{"p2": userB},
			want:  want{true, []store.UserResult{{UserID: userB, Points: 10, BestRound: 10}}},
		},
		{
			name: "nobody linked: nothing to record",
			g: finished([]string{"p1", "p2"}, []string{"p1"},
				roundOf(1, map[string][]int{"p1": {10}, "p2": {5}})),
			users: nil,
			want:  want{ok: false},
		},
		{
			name:  "no scored round",
			g:     finished([]string{"p1", "p2"}, []string{"p1", "p2"}),
			users: map[string]string{"p1": userA, "p2": userB},
			want:  want{ok: false},
		},
		{
			name: "a single participant is not a game",
			g: finished([]string{"p1", "p2"}, []string{"p1"},
				roundOf(1, map[string][]int{"p1": {10, 10}})),
			users: map[string]string{"p1": userA, "p2": userB},
			want:  want{ok: false},
		},
		{
			name: "a player who left is credited for the rounds they played but cannot win",
			g: func() *game.Game {
				g := finished([]string{"p1", "p2", "p3"}, []string{"p1"},
					roundOf(1, map[string][]int{"p1": {10, 5}, "p2": {10, 10}, "p3": {5, 5}}),
					roundOf(2, map[string][]int{"p1": {10, 10}, "p3": {0, 0}}))
				g.Players[1].Left = true // p2 left after round 1 although they scored the most
				return g
			}(),
			users: map[string]string{"p1": userA, "p2": userB, "p3": userC},
			want: want{true, []store.UserResult{
				{UserID: userA, Won: true, Points: 35, BestRound: 20, UniqueAnswers: 3},
				{UserID: userB, Won: false, Points: 20, BestRound: 20, UniqueAnswers: 2},
				{UserID: userC, Won: false, Points: 10, BestRound: 10, UniqueAnswers: 0},
			}},
		},
		{
			name: "a late joiner only counts for the rounds they played",
			g: finished([]string{"p1", "p2", "p3"}, []string{"p1"},
				roundOf(1, map[string][]int{"p1": {10}, "p2": {5}}),
				roundOf(2, map[string][]int{"p1": {10}, "p2": {5}, "p3": {5}})),
			users: map[string]string{"p3": userC},
			want:  want{true, []store.UserResult{{UserID: userC, Points: 5, BestRound: 5}}},
		},
		{
			name: "a linked spectator who never played a scored round is left out",
			g: finished([]string{"p1", "p2", "p3"}, []string{"p1"},
				roundOf(1, map[string][]int{"p1": {10}, "p2": {5}})),
			users: map[string]string{"p1": userA, "p3": userC},
			want:  want{true, []store.UserResult{{UserID: userA, Won: true, Points: 10, BestRound: 10, UniqueAnswers: 1}}},
		},
		{
			name: "a tie is a win for everybody who tied",
			g: finished([]string{"p1", "p2"}, []string{"p1", "p2"},
				roundOf(1, map[string][]int{"p1": {10}, "p2": {10}})),
			users: map[string]string{"p1": userA, "p2": userB},
			want: want{true, []store.UserResult{
				{UserID: userA, Won: true, Points: 10, BestRound: 10, UniqueAnswers: 1},
				{UserID: userB, Won: true, Points: 10, BestRound: 10, UniqueAnswers: 1},
			}},
		},
		{
			name: "one user on two devices counts once, the first seat",
			g: finished([]string{"p1", "p2", "p3"}, []string{"p2"},
				roundOf(1, map[string][]int{"p1": {5}, "p2": {10}, "p3": {5}})),
			users: map[string]string{"p1": userA, "p3": userA, "p2": userB},
			want: want{true, []store.UserResult{
				{UserID: userA, Points: 5, BestRound: 5},
				{UserID: userB, Won: true, Points: 10, BestRound: 10, UniqueAnswers: 1},
			}},
		},
		{
			name: "a link to a player that does not exist is ignored",
			g: finished([]string{"p1", "p2"}, []string{"p1"},
				roundOf(1, map[string][]int{"p1": {10}, "p2": {5}})),
			users: map[string]string{"ghost": userC, "p1": userA},
			want:  want{true, []store.UserResult{{UserID: userA, Won: true, Points: 10, BestRound: 10, UniqueAnswers: 1}}},
		},
		{
			name: "only 10-point cells are unique answers; the best round is a single round",
			g: finished([]string{"p1", "p2"}, []string{"p1"},
				roundOf(1, map[string][]int{"p1": {10, 5, 0, 10}, "p2": {5}}),
				roundOf(2, map[string][]int{"p1": {5, 5, 5, 5}, "p2": {10}})),
			users: map[string]string{"p1": userA},
			want:  want{true, []store.UserResult{{UserID: userA, Won: true, Points: 25 + 20, BestRound: 25, UniqueAnswers: 2}}},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			rec, ok := buildRecord(tc.g, tc.users, gameID, at)
			if ok != tc.want.ok {
				t.Fatalf("ok = %v, want %v (record %+v)", ok, tc.want.ok, rec)
			}
			if !ok {
				return
			}
			if rec.ID != gameID || !rec.PlayedAt.Equal(at) {
				t.Errorf("ID %q PlayedAt %v", rec.ID, rec.PlayedAt)
			}
			if fmt.Sprint(rec.Results) != fmt.Sprint(tc.want.results) {
				t.Errorf("results = %+v\nwant     %+v", rec.Results, tc.want.results)
			}
		})
	}
}

func TestCleanLinks(t *testing.T) {
	g := finished([]string{"p1", "p2"}, []string{"p1"}, roundOf(1, map[string][]int{"p1": {10}, "p2": {5}}))
	users, id := cleanLinks(g, map[string]string{
		"p1": userA, "p2": "not-a-uuid", "ghost": userB, "p3": strings.ToUpper(userC),
	}, gameID)
	if len(users) != 1 || users["p1"] != userA {
		t.Errorf("users = %v, want only p1", users)
	}
	if id != gameID {
		t.Errorf("gameID = %q, want it kept in the final phase", id)
	}
	if _, id := cleanLinks(g, nil, "not-a-uuid"); id != "" {
		t.Errorf("a malformed game id was kept: %q", id)
	}
	g.Phase = game.PhasePlaying
	if _, id := cleanLinks(g, nil, gameID); id != gameID {
		t.Errorf("the id of a game in progress was dropped: %q", id)
	}
	g.Phase = game.PhaseLobby
	if _, id := cleanLinks(g, nil, gameID); id != "" {
		t.Errorf("a game id in the lobby was kept: %q", id)
	}
	if users, _ := cleanLinks(g, nil, ""); users != nil {
		t.Errorf("users = %v, want nil", users)
	}
}

// ---- recorder fake ----

type fakeRecorder struct {
	mu       sync.Mutex
	attempts []store.GameRecord
	done     []store.GameRecord
	fail     int           // fail the next n attempts with err
	err      error         // the error of a failing attempt
	gate     chan struct{} // when set, an attempt blocks until it is closed or its context ends
	hang     bool          // an attempt blocks until its context ends
	panics   int           // panic in the next n attempts
}

func (f *fakeRecorder) RecordGame(ctx context.Context, rec store.GameRecord) error {
	f.mu.Lock()
	f.attempts = append(f.attempts, rec)
	gate, hang := f.gate, f.hang
	f.mu.Unlock()
	if gate != nil {
		select {
		case <-gate:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	if hang {
		<-ctx.Done()
		return ctx.Err()
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.panics > 0 {
		f.panics--
		panic("fakeRecorder: RecordGame blew up")
	}
	if f.fail > 0 {
		f.fail--
		if f.err != nil {
			return f.err
		}
		return errStoreDown
	}
	f.done = append(f.done, rec)
	return nil
}

func (f *fakeRecorder) counts() (attempts, done int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.attempts), len(f.done)
}

func (f *fakeRecorder) recorded() []store.GameRecord {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]store.GameRecord(nil), f.done...)
}

func (f *fakeRecorder) setFail(n int) {
	f.mu.Lock()
	f.fail = n
	f.mu.Unlock()
}

// withRecorder configures a hub with rec and fast, deterministic backoff.
func withRecorder(rec Recorder) func(*Config) {
	return func(c *Config) { c.Recorder = rec }
}

// newLinkedLobby3 is newLobby3 with p1, p2 and p3 signed in as users (""
// means a guest).
func newLinkedLobby3(t *testing.T, users [3]string, mods ...func(*Config)) *lobby3 {
	t.Helper()
	e := newEnv(t, mods...)
	if e.hub.recorder != nil {
		e.hub.recorder.minBackoff = time.Second
		e.hub.recorder.maxBackoff = 8 * time.Second
	}
	l := &lobby3{e: e, code: e.create("p1", "Ana")}
	for i, name := range []string{"Ana", "Beto", "Cami"} {
		l.c[i], l.s[i] = e.joinAs(l.code, fmt.Sprintf("p%d", i+1), name, users[i])
	}
	l.drain()
	return l
}

// The game of finishGame, by hand: letter A, one round.
//
//	nombre: Ana, Alberto, Ana        -> p1 5, p2 10, p3 5
//	pais:   Argentina, Austria, Alemania -> 10, 10, 10
//	animal: Aguila, Alce, Avestruz   -> 10, 10, 10
//
// p1 25 (2 unique), p2 30 (3 unique, winner), p3 25 (2 unique).
var (
	wantA = store.UserResult{UserID: userA, Won: false, Points: 25, BestRound: 25, UniqueAnswers: 2}
	wantB = store.UserResult{UserID: userB, Won: true, Points: 30, BestRound: 30, UniqueAnswers: 3}
	wantC = store.UserResult{UserID: userC, Won: false, Points: 25, BestRound: 25, UniqueAnswers: 2}
)

// waitBackoff returns once the recorder sleeps on the hub's clock (the only
// timer besides the rooms' own), so advancing the clock now is what wakes it.
func waitBackoff(t *testing.T, e *env) {
	t.Helper()
	eventually(t, "the recorder to wait for its backoff", func() bool { return e.otherTimers() == 1 })
}

func waitDone(t *testing.T, f *fakeRecorder, n int) []store.GameRecord {
	t.Helper()
	eventually(t, fmt.Sprintf("%d recorded games", n), func() bool {
		_, done := f.counts()
		return done >= n
	})
	return f.recorded()
}

func TestGameOverRecordsTheLinkedPlayers(t *testing.T) {
	rec := &fakeRecorder{}
	l := newLinkedLobby3(t, [3]string{userA, userB, ""}, withRecorder(rec))
	finishGame(t, l)

	got := waitDone(t, rec, 1)
	if len(got) != 1 {
		t.Fatalf("recorded %d games, want 1", len(got))
	}
	r := got[0]
	if !store.ValidID(r.ID) {
		t.Errorf("game id %q is not a UUID", r.ID)
	}
	if len(r.Results) != 2 || r.Results[0] != wantA || r.Results[1] != wantB {
		t.Errorf("results = %+v, want %+v and %+v (the guest p3 gets nothing)", r.Results, wantA, wantB)
	}
	if !r.PlayedAt.Equal(l.e.clock.Now()) {
		t.Errorf("PlayedAt = %v, want the end of the game %v", r.PlayedAt, l.e.clock.Now())
	}
	if v := metricValue(t, l.e.reg, "tuttifrutti_stats_recorded_total", "result", "ok"); v != 1 {
		t.Errorf("stats_recorded_total{ok} = %v, want 1", v)
	}

	// The same numbers as the engine's final scores.
	v := query(l.e, l.code, func(r *room) game.View { return r.g.View("p1") })
	for _, p := range v.Players {
		for _, res := range r.Results {
			if res.UserID == map[string]string{"p1": userA, "p2": userB}[p.ID] && res.Points != p.TotalScore {
				t.Errorf("%s: recorded %d points, engine says %d", p.Name, res.Points, p.TotalScore)
			}
		}
	}

	// The snapshot carries the links and the id of the game being recorded.
	l.e.waitSaved(l.code)
	p, _ := l.e.stored(l.code)
	if p.GameID != r.ID || p.Users["p1"] != userA || p.Users["p2"] != userB || len(p.Users) != 2 {
		t.Errorf("snapshot users %v gameId %q, want links of p1 and p2 and game %s", p.Users, p.GameID, r.ID)
	}
}

func TestGameOverWithOnlyGuestsRecordsNothing(t *testing.T) {
	rec := &fakeRecorder{}
	l := newLinkedLobby3(t, [3]string{}, withRecorder(rec))
	finishGame(t, l)
	l.e.syncAll()
	if attempts, _ := rec.counts(); attempts != 0 {
		t.Errorf("%d attempts for a game without linked players", attempts)
	}
	l.e.waitSaved(l.code)
	if p, _ := l.e.stored(l.code); !store.ValidID(p.GameID) || len(p.Users) != 0 || len(p.Pending) != 0 {
		t.Errorf("snapshot has users %v / pending %v for a game of guests (and the game id %q)", p.Users, p.Pending, p.GameID)
	}
	for _, r := range []string{"ok", "error", "dropped"} {
		if v := metricValue(t, l.e.reg, "tuttifrutti_stats_recorded_total", "result", r); v != 0 {
			t.Errorf("stats_recorded_total{%s} = %v", r, v)
		}
	}
}

func TestWithoutARecorderNothingIsLinked(t *testing.T) {
	l := newLinkedLobby3(t, [3]string{userA, userB, userC})
	finishGame(t, l)
	l.e.waitSaved(l.code)
	if p, _ := l.e.stored(l.code); len(p.Users) != 0 || p.GameID != "" {
		t.Errorf("users %v / gameId %q kept although statistics are off", p.Users, p.GameID)
	}
	if l.e.hub.recorder != nil {
		t.Error("a recorder exists without Config.Recorder")
	}
}

func TestRematchStartsANewGame(t *testing.T) {
	rec := &fakeRecorder{}
	l := newLinkedLobby3(t, [3]string{userA, userB, userC}, withRecorder(rec))
	finishGame(t, l)
	first := waitDone(t, rec, 1)[0]
	if len(first.Results) != 3 {
		t.Fatalf("first game results = %+v", first.Results)
	}

	l.send(0, game.CmdRematch, nil)
	l.e.waitSaved(l.code)
	// The raw snapshot (decodePersisted would clean a stale id away).
	snaps, err := l.e.store.Memory.LoadAll(context.Background())
	if err != nil || len(snaps) != 1 {
		t.Fatalf("snapshots = %d, %v", len(snaps), err)
	}
	var raw Persisted
	if err := json.Unmarshal(snaps[0].State, &raw); err != nil {
		t.Fatal(err)
	}
	if raw.GameID != "" {
		t.Errorf("the game id %q survived the rematch in the snapshot", raw.GameID)
	}
	if p, _ := l.e.stored(l.code); len(p.Users) != 3 {
		t.Errorf("the links did not survive the rematch: %v", p.Users)
	}

	finishGame(t, l)
	second := waitDone(t, rec, 2)[1]
	if second.ID == first.ID {
		t.Error("a rematch reused the id of the previous game")
	}
	if len(second.Results) != 3 || second.Results[1] != wantB {
		t.Errorf("second game results = %+v", second.Results)
	}
}

func TestAPlayerWhoLeftMidGameIsStillCredited(t *testing.T) {
	rec := &fakeRecorder{}
	l := newLinkedLobby3(t, [3]string{userA, userB, userC}, withRecorder(rec))
	l.startRound(nil)
	l.fillAnswers()
	l.send(0, game.CmdBasta, nil)
	l.e.advance(game.GraceDuration + game.LateUpdateAllowance)
	l.send(2, game.CmdLeave, nil) // p3 leaves during the review, after playing the round
	l.send(0, game.CmdFinishReview, nil)
	l.send(0, game.CmdNextRound, nil)

	got := waitDone(t, rec, 1)[0]
	if len(got.Results) != 3 || got.Results[0] != wantA || got.Results[1] != wantB || got.Results[2] != wantC {
		t.Errorf("results = %+v, want A %+v, B %+v, C %+v (C left but played)", got.Results, wantA, wantB, wantC)
	}
	if got.Results[2].Won {
		t.Error("a player who left won")
	}
}

func TestLeavingTheLobbyDropsTheLink(t *testing.T) {
	rec := &fakeRecorder{}
	l := newLinkedLobby3(t, [3]string{userA, userB, userC}, withRecorder(rec))
	l.send(2, game.CmdLeave, nil) // p3 leaves before the game starts: the seat is removed
	l.e.waitSaved(l.code)
	if p, _ := l.e.stored(l.code); len(p.Users) != 2 || p.Users["p3"] != "" {
		t.Errorf("users = %v, want p3's link gone with the seat", p.Users)
	}
	users := query(l.e, l.code, func(r *room) map[string]string { return map[string]string{"p3": r.users["p3"]} })
	if users["p3"] != "" {
		t.Errorf("the room still links p3 to %q", users["p3"])
	}
}

func TestJoiningAgainRefreshesTheLink(t *testing.T) {
	rec := &fakeRecorder{}
	l := newLinkedLobby3(t, [3]string{userA, userB, userC}, withRecorder(rec))
	l.s[0].Disconnect()
	l.s[1].Disconnect()
	l.e.sync(l.code)
	// p1 comes back signed out, p2 comes back as another account.
	l.c[0], l.s[0] = l.e.joinAs(l.code, "p1", "Ana", "")
	l.c[1], l.s[1] = l.e.joinAs(l.code, "p2", "Beto", userC)
	l.drain()
	// p3 is also userC's: the first seat in join order (p2) keeps it.
	users := query(l.e, l.code, func(r *room) map[string]string {
		out := map[string]string{}
		for k, v := range r.users {
			out[k] = v
		}
		return out
	})
	if _, linked := users["p1"]; linked || users["p2"] != userC || users["p3"] != userC {
		t.Errorf("users = %v, want p1 a guest, p2 and p3 on %s", users, userC)
	}

	finishGame(t, l)
	got := waitDone(t, rec, 1)[0]
	if len(got.Results) != 1 || got.Results[0].UserID != userC || got.Results[0] != (store.UserResult{UserID: userC, Won: true, Points: 30, BestRound: 30, UniqueAnswers: 3}) {
		t.Errorf("results = %+v: p2 (the first seat of %s) wins with 30", got.Results, userC)
	}
}

func TestRestartKeepsTheLinks(t *testing.T) {
	rec := &fakeRecorder{}
	l := newLinkedLobby3(t, [3]string{userA, userB, ""}, withRecorder(rec))
	l.startRound(nil)
	l.fillAnswers()
	l.e.waitSaved(l.code)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := l.e.hub.Shutdown(ctx); err != nil {
		t.Fatal(err)
	}

	rec2 := &fakeRecorder{}
	h2 := l.e.newHubOn(withRecorder(rec2))
	if n, err := h2.Start(context.Background()); err != nil || n != 1 {
		t.Fatalf("Start = %d, %v", n, err)
	}
	e2 := &env{t: t, clock: l.e.clock, store: l.e.store, reg: l.e.reg, logs: l.e.logs, hub: h2}
	l2 := &lobby3{e: e2, code: l.code}
	for i, id := range []string{"p1", "p2", "p3"} {
		// The players come back (p2 comes back signed out).
		user := [3]string{userA, "", ""}[i]
		l2.c[i], l2.s[i] = e2.joinAs(l.code, id, []string{"Ana", "Beto", "Cami"}[i], user)
	}
	l2.drain()
	l2.send(0, game.CmdBasta, nil)
	e2.advance(game.GraceDuration + game.LateUpdateAllowance)
	l2.send(0, game.CmdFinishReview, nil)
	l2.send(0, game.CmdNextRound, nil)

	got := waitDone(t, rec2, 1)[0]
	// The round was in progress: both links were restored from the snapshot and
	// are frozen, so p2 coming back as a guest does not cost userB the credit.
	if len(got.Results) != 2 || got.Results[0] != wantA || got.Results[1] != wantB {
		t.Errorf("results = %+v, want %+v and %+v", got.Results, wantA, wantB)
	}
}

func TestRestartInTheFinalPhaseRecordsAgain(t *testing.T) {
	failing := &fakeRecorder{fail: 1 << 20}
	l := newLinkedLobby3(t, [3]string{userA, userB, ""}, withRecorder(failing))
	finishGame(t, l)
	eventually(t, "a first attempt", func() bool { a, _ := failing.counts(); return a >= 1 })
	l.e.waitSaved(l.code)
	p, _ := l.e.stored(l.code)
	if p.GameID == "" {
		t.Fatal("the snapshot of a finished game has no game id")
	}

	// The process dies (here: shuts down) before the game was recorded.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := l.e.hub.Shutdown(ctx); err != nil {
		t.Fatal(err)
	}
	if _, done := failing.counts(); done != 0 {
		t.Fatalf("setup: the game was recorded")
	}

	rec2 := &fakeRecorder{}
	h2 := l.e.newHubOn(withRecorder(rec2))
	if _, err := h2.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	got := waitDone(t, rec2, 1)[0]
	if got.ID != p.GameID {
		t.Errorf("recorded game id %q, want the persisted %q", got.ID, p.GameID)
	}
	if len(got.Results) != 2 || got.Results[0] != wantA || got.Results[1] != wantB {
		t.Errorf("results after the restart = %+v", got.Results)
	}
}

func TestOldSnapshotsWithoutAccountFieldsRestore(t *testing.T) {
	e := newEnv(t, withRecorder(&fakeRecorder{}))
	g := finished([]string{"p1", "p2"}, []string{"p1"}, roundOf(1, map[string][]int{"p1": {10}, "p2": {5}}))
	// What a pre-accounts server wrote: no "users", no "gameId".
	data, err := encodePersisted("BCDF", t0, t0, g)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "users") || strings.Contains(string(data), "gameId") {
		t.Fatalf("encodePersisted writes the new fields: %s", data)
	}
	if err := e.store.Save(context.Background(), store.Snapshot{Code: "BCDF", State: data, UpdatedAt: t0}); err != nil {
		t.Fatal(err)
	}
	h := e.newHubOn(withRecorder(&fakeRecorder{}))
	if n, err := h.Start(context.Background()); err != nil || n != 1 {
		t.Fatalf("Start = %d, %v", n, err)
	}
	if info, ok := h.Info("BCDF"); !ok || info.Phase != game.PhaseFinal {
		t.Errorf("Info = %+v, %v", info, ok)
	}
}

func TestSnapshotLinksAreSanitisedOnRestore(t *testing.T) {
	rec := &fakeRecorder{}
	e := newEnv(t, withRecorder(rec))
	g := finished([]string{"p1", "p2"}, []string{"p1"}, roundOf(1, map[string][]int{"p1": {10}, "p2": {5}}))
	data, err := encodeRoomState(Persisted{
		Code: "BCDF", CreatedAt: t0, LastActive: t0,
		Users:  map[string]string{"p1": userA, "p2": "garbage", "ghost": userB},
		GameID: gameID,
	}, g)
	if err != nil {
		t.Fatal(err)
	}
	if err := e.store.Save(context.Background(), store.Snapshot{Code: "BCDF", State: data, UpdatedAt: t0}); err != nil {
		t.Fatal(err)
	}
	h := e.newHubOn(withRecorder(rec))
	if _, err := h.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	got := waitDone(t, rec, 1)[0]
	if got.ID != gameID || len(got.Results) != 1 || got.Results[0].UserID != userA {
		t.Errorf("record = %+v, want only p1's valid link", got)
	}
}

// ---- the recorder itself ----

func TestRecorderRetriesWithBackoffWithoutBlockingTheRoom(t *testing.T) {
	rec := &fakeRecorder{fail: 3}
	l := newLinkedLobby3(t, [3]string{userA, userB, ""}, withRecorder(rec))
	finishGame(t, l)

	eventually(t, "the first attempt", func() bool { a, _ := rec.counts(); return a == 1 })
	// The room carries on while the store is failing: a rematch works at once.
	l.send(0, game.CmdRematch, nil)
	if phase := query(l.e, l.code, func(r *room) game.Phase { return r.g.Phase }); phase != game.PhaseLobby {
		t.Fatalf("phase = %s: the failing recorder blocked the room", phase)
	}

	// Backoff 1 s, 2 s, 4 s on the hub's clock; nothing happens before.
	for i, wait := range []time.Duration{time.Second, 2 * time.Second, 4 * time.Second} {
		waitBackoff(t, l.e)
		l.e.clock.Advance(wait - time.Millisecond)
		time.Sleep(10 * time.Millisecond)
		if a, _ := rec.counts(); a != i+1 {
			t.Fatalf("after %v of backoff there are %d attempts, want %d: the retry is early", wait-time.Millisecond, a, i+1)
		}
		l.e.clock.Advance(time.Millisecond)
		eventually(t, fmt.Sprintf("attempt %d", i+2), func() bool { a, _ := rec.counts(); return a == i+2 })
	}
	got := waitDone(t, rec, 1)
	if len(got) != 1 || len(got[0].Results) != 2 {
		t.Errorf("recorded %+v", got)
	}
	if a, _ := rec.counts(); a != 4 {
		t.Errorf("attempts = %d, want 4", a)
	}
	ok := metricValue(t, l.e.reg, "tuttifrutti_stats_recorded_total", "result", "ok")
	errs := metricValue(t, l.e.reg, "tuttifrutti_stats_recorded_total", "result", "error")
	dropped := metricValue(t, l.e.reg, "tuttifrutti_stats_recorded_total", "result", "dropped")
	if ok != 1 || errs != 3 || dropped != 0 {
		t.Errorf("metrics ok=%v error=%v dropped=%v, want 1 3 0", ok, errs, dropped)
	}
}

func TestRecorderBackoffIsCapped(t *testing.T) {
	rec := &fakeRecorder{fail: 1 << 20}
	l := newLinkedLobby3(t, [3]string{userA, userB, ""}, withRecorder(rec))
	finishGame(t, l)
	eventually(t, "the first attempt", func() bool { a, _ := rec.counts(); return a == 1 })
	// 1, 2, 4, 8, 8, 8 ... (maxBackoff 8 s).
	want := 1
	for _, wait := range []time.Duration{1, 2, 4, 8, 8, 8} {
		waitBackoff(t, l.e)
		l.e.clock.Advance(wait * time.Second)
		want++
		eventually(t, fmt.Sprintf("attempt %d", want), func() bool { a, _ := rec.counts(); return a == want })
	}
}

func TestRecorderStallsAfterTheAttemptsRunOutAndTheRoomKeepsTheGame(t *testing.T) {
	rec := &fakeRecorder{fail: 1 << 20}
	l := newLinkedLobby3(t, [3]string{userA, userB, ""}, withRecorder(rec))
	l.e.hub.recorder.maxAttempts = 3
	finishGame(t, l)
	eventually(t, "the first attempt", func() bool { a, _ := rec.counts(); return a == 1 })
	first := func() string { rec.mu.Lock(); defer rec.mu.Unlock(); return rec.attempts[0].ID }()

	for i, wait := range []time.Duration{time.Second, 2 * time.Second} {
		waitBackoff(t, l.e)
		l.e.clock.Advance(wait)
		eventually(t, fmt.Sprintf("attempt %d", i+2), func() bool { a, _ := rec.counts(); return a == i+2 })
	}
	eventually(t, "the recorder to stop after the third attempt", func() bool {
		return !queryInflight(l.e, l.code, first)
	})
	if a, _ := rec.counts(); a != 3 {
		t.Errorf("attempts = %d, want 3", a)
	}
	if e := metricValue(t, l.e.reg, "tuttifrutti_stats_recorded_total", "result", "error"); e != 3 {
		t.Errorf("error = %v, want one per failed attempt", e)
	}
	if d := metricValue(t, l.e.reg, "tuttifrutti_stats_recorded_total", "result", "dropped"); d != 0 {
		t.Errorf("dropped = %v: a game that stalled is not lost, it stays in the room's outbox", d)
	}
	if ids := pendingIDs(l.e, l.code); len(ids) != 1 || ids[0] != first {
		t.Fatalf("outbox = %v, want the stalled game %s", ids, first)
	}

	// The store is back; the room's next snapshot (the rematch) offers the
	// stalled game again, and the next game is not stuck behind it.
	rec.setFail(0)
	l.send(0, game.CmdRematch, nil)
	finishGame(t, l)
	got := waitDone(t, rec, 2)
	if got[0].ID != first || got[1].ID == first {
		t.Errorf("recorded %s then %s, want the stalled game %s first and a new one second", got[0].ID, got[1].ID, first)
	}
	eventually(t, "the outbox to empty", func() bool { return len(pendingIDs(l.e, l.code)) == 0 })
}

func TestRecorderDropsAGameTheStoreRefusesWithoutRetrying(t *testing.T) {
	rec := &fakeRecorder{fail: 1, err: fmt.Errorf("wrapped: %w", store.ErrInvalidGame)}
	l := newLinkedLobby3(t, [3]string{userA, userB, ""}, withRecorder(rec))
	finishGame(t, l)
	eventually(t, "the game to be dropped", func() bool {
		return metricValue(t, l.e.reg, "tuttifrutti_stats_recorded_total", "result", "dropped") == 1
	})
	l.e.clock.Advance(time.Minute)
	time.Sleep(10 * time.Millisecond)
	if a, _ := rec.counts(); a != 1 {
		t.Errorf("attempts = %d: a permanent error was retried", a)
	}
	if v := metricValue(t, l.e.reg, "tuttifrutti_stats_recorded_total", "result", "error"); v != 0 {
		t.Errorf("error = %v: a refused game is dropped, not an outage", v)
	}
}

func TestRecorderSurvivesAPanickingStore(t *testing.T) {
	rec := &fakeRecorder{panics: 1}
	l := newLinkedLobby3(t, [3]string{userA, userB, ""}, withRecorder(rec))
	finishGame(t, l)
	eventually(t, "the panicking attempt", func() bool { a, _ := rec.counts(); return a == 1 })
	eventually(t, "the panic to count as an error", func() bool {
		return metricValue(t, l.e.reg, "tuttifrutti_stats_recorded_total", "result", "error") == 1
	})
	l.e.clock.Advance(time.Second)
	waitDone(t, rec, 1)
}

func TestRecorderQueueIsBounded(t *testing.T) {
	gate := make(chan struct{})
	rec := &fakeRecorder{gate: gate}
	e := newEnv(t, withRecorder(rec))
	q := e.hub.recorder
	q.queueSize = 2

	mk := func(i int) store.GameRecord {
		return store.GameRecord{ID: fmt.Sprintf("00000000-0000-4000-8000-%012d", i), Results: []store.UserResult{{UserID: userA}}}
	}
	accepted := func(i int) bool { return q.enqueue(statsJob{rec: mk(i)}) }
	if !accepted(1) {
		t.Fatal("the first game was refused")
	}
	eventually(t, "the worker to take the first game", func() bool { a, _ := rec.counts(); return a == 1 })
	if !accepted(2) || !accepted(3) {
		t.Fatal("games 2 and 3 fit in the queue")
	}
	if accepted(4) || accepted(5) { // the queue holds 2: refused, and the owner keeps them
		t.Error("a full queue accepted a game")
	}
	if n := q.pending(); n != 2 {
		t.Errorf("pending = %d, want 2", n)
	}
	close(gate)
	waitDone(t, rec, 3)
	ids := []string{}
	for _, r := range rec.recorded() {
		ids = append(ids, r.ID[len(r.ID)-1:])
	}
	if strings.Join(ids, "") != "123" {
		t.Errorf("recorded %v, want the first three games in order", ids)
	}
}

func TestRoomsAreNotBlockedByAStuckRecorder(t *testing.T) {
	rec := &fakeRecorder{hang: true}
	l := newLinkedLobby3(t, [3]string{userA, userB, ""}, withRecorder(rec))
	finishGame(t, l)
	eventually(t, "the recorder to hang", func() bool { a, _ := rec.counts(); return a == 1 })
	l.send(0, game.CmdRematch, nil)
	finishGame(t, l) // the second game is only queued behind the stuck one
	if n := l.e.hub.recorder.pending(); n != 1 {
		t.Errorf("pending = %d, want 1 game queued behind the stuck one", n)
	}
}

func TestShutdownFlushesTheQueueAndGivesUpCleanly(t *testing.T) {
	t.Run("a store that recovered gets the queued games", func(t *testing.T) {
		rec := &fakeRecorder{fail: 1}
		l := newLinkedLobby3(t, [3]string{userA, userB, ""}, withRecorder(rec))
		finishGame(t, l)
		eventually(t, "the failed attempt", func() bool { a, _ := rec.counts(); return a == 1 })

		// No clock advance: the retry is still waiting for its backoff.
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := l.e.hub.Shutdown(ctx); err != nil {
			t.Fatalf("Shutdown: %v", err)
		}
		if got := rec.recorded(); len(got) != 1 {
			t.Errorf("recorded %d games, want the shutdown to retry once and succeed", len(got))
		}
	})
	t.Run("a store that is still down: no hang, the games wait in the snapshot for the next start", func(t *testing.T) {
		rec := &fakeRecorder{fail: 1 << 20}
		l := newLinkedLobby3(t, [3]string{userA, userB, ""}, withRecorder(rec))
		finishGame(t, l)
		eventually(t, "the failed attempt", func() bool { a, _ := rec.counts(); return a == 1 })
		l.send(0, game.CmdRematch, nil)
		finishGame(t, l) // a second game waits in the queue

		before, _ := rec.counts()
		started := time.Now()
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := l.e.hub.Shutdown(ctx); err != nil {
			t.Fatalf("Shutdown: %v", err)
		}
		if time.Since(started) > 3*time.Second {
			t.Errorf("Shutdown took %v", time.Since(started))
		}
		if v := metricValue(t, l.e.reg, "tuttifrutti_stats_recorded_total", "result", "dropped"); v != 0 {
			t.Errorf("dropped = %v: nothing is lost, the games are in the final snapshot", v)
		}
		if _, done := rec.counts(); done != 0 {
			t.Error("a game was recorded by a store that was down")
		}
		// Exactly one last try for each of the two games: a closing recorder
		// does not hammer a store that is down.
		if after, _ := rec.counts(); after-before != 2 {
			t.Errorf("%d attempts during the shutdown, want 2 (one per queued game)", after-before)
		}

		// The next process records both, once each.
		rec2 := &fakeRecorder{}
		e2 := restartedEnv(t, l.e, withRecorder(rec2))
		got := waitDone(t, rec2, 2)
		if got[0].ID == got[1].ID {
			t.Errorf("recorded the same game twice: %s", got[0].ID)
		}
		e2.syncAll()
		if a, _ := rec2.counts(); a != 2 {
			t.Errorf("%d attempts after the restart, want 2", a)
		}
	})
	t.Run("a store that hangs is cut off by the shutdown budget", func(t *testing.T) {
		rec := &fakeRecorder{hang: true}
		l := newLinkedLobby3(t, [3]string{userA, userB, ""}, withRecorder(rec))
		finishGame(t, l)
		eventually(t, "the hanging attempt", func() bool { a, _ := rec.counts(); return a == 1 })
		ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
		defer cancel()
		err := l.e.hub.Shutdown(ctx)
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Errorf("Shutdown = %v, want the budget to run out", err)
		}
		// The cleanup of newEnv checks that no goroutine is left behind.
	})
	t.Run("records after the shutdown are dropped", func(t *testing.T) {
		rec := &fakeRecorder{}
		e := newEnv(t, withRecorder(rec))
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := e.hub.Shutdown(ctx); err != nil {
			t.Fatal(err)
		}
		if e.hub.recorder.enqueue(statsJob{rec: store.GameRecord{ID: gameID, Results: []store.UserResult{{UserID: userA}}}}) {
			t.Error("a closed recorder accepted a game")
		}
		if a, _ := rec.counts(); a != 0 {
			t.Errorf("attempts = %d after the shutdown", a)
		}
	})
}

func TestStatsMetricHasConstantCardinality(t *testing.T) {
	e := newEnv(t, withRecorder(&fakeRecorder{}))
	if n := seriesCount(t, e.reg, "tuttifrutti_stats_recorded_total"); n != 3 {
		t.Errorf("stats_recorded_total has %d series at start, want 3 (ok, error, dropped)", n)
	}
	l := newLinkedLobby3(t, [3]string{userA, userB, ""}, withRecorder(&fakeRecorder{fail: 2}))
	finishGame(t, l)
	l.e.clock.Advance(time.Hour)
	if n := seriesCount(t, l.e.reg, "tuttifrutti_stats_recorded_total"); n != 3 {
		t.Errorf("stats_recorded_total has %d series after a game, want 3", n)
	}
}
