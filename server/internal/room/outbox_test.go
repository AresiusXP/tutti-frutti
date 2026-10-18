package room

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/AresiusXP/tutti-frutti/server/internal/game"
	"github.com/AresiusXP/tutti-frutti/server/internal/store"
)

// ---- helpers ----

func pendingIDs(e *env, code string) []string { return query(e, code, pendingOf) }

func queryInflight(e *env, code, id string) bool {
	return query(e, code, func(r *room) bool { return r.inflight[id] })
}

func queryGameID(e *env, code string) string {
	return query(e, code, func(r *room) string { return r.gameID })
}

func queryLinks(e *env, code string) map[string]string {
	return query(e, code, func(r *room) map[string]string {
		out := map[string]string{}
		for k, v := range r.users {
			out[k] = v
		}
		return out
	})
}

// rawSnapshot is the stored snapshot of a room, as the store has it.
func rawSnapshot(t *testing.T, e *env, code string) store.Snapshot {
	t.Helper()
	snaps, err := e.store.Memory.LoadAll(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range snaps {
		if s.Code == code {
			return s
		}
	}
	t.Fatalf("no stored snapshot for %s", code)
	return store.Snapshot{}
}

// rawStored decodes the stored snapshot of a room without the sanitising
// decodePersisted does (what is really in the store).
func rawStored(t *testing.T, e *env, code string) Persisted {
	t.Helper()
	var p Persisted
	if err := json.Unmarshal(rawSnapshot(t, e, code).State, &p); err != nil {
		t.Fatal(err)
	}
	return p
}

// restartedEnv shuts old's hub down (as a SIGTERM would) and starts another one
// over the same store and clock, with a registry of its own.
func restartedEnv(t *testing.T, old *env, mods ...func(*Config)) *env {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := old.hub.Shutdown(ctx); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}
	return startedEnv(t, old, mods...)
}

// startedEnv starts another hub over old's store and clock (old's hub must be
// stopped or never started), with a registry of its own.
func startedEnv(t *testing.T, old *env, mods ...func(*Config)) *env {
	t.Helper()
	reg := prometheus.NewRegistry()
	h := old.newHubOn(append([]func(*Config){func(c *Config) { c.Registerer = reg }}, mods...)...)
	if _, err := h.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	return &env{t: t, clock: old.clock, store: old.store, reg: reg, logs: old.logs, hub: h}
}

func (l *lobby3) rejoin(e2 *env, users [3]string) *lobby3 {
	l2 := &lobby3{e: e2, code: l.code}
	for i, name := range []string{"Ana", "Beto", "Cami"} {
		l2.c[i], l2.s[i] = e2.joinAs(l.code, fmt.Sprintf("p%d", i+1), name, users[i])
	}
	l2.drain()
	return l2
}

func setQueueSize(e *env, n int) {
	q := e.hub.recorder
	q.mu.Lock()
	q.queueSize = n
	q.mu.Unlock()
}

func attemptsOf(f *fakeRecorder) []store.GameRecord {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]store.GameRecord(nil), f.attempts...)
}

func sameRecord(t *testing.T, got, want store.GameRecord) {
	t.Helper()
	if got.ID != want.ID || !got.PlayedAt.Equal(want.PlayedAt) || !slices.Equal(got.Results, want.Results) {
		t.Errorf("record = %+v, want the frozen %+v", got, want)
	}
}

// countingRecorder remembers every attempt and forwards it to a real store.
type countingRecorder struct {
	Recorder
	mu       sync.Mutex
	attempts []store.GameRecord
}

func (c *countingRecorder) RecordGame(ctx context.Context, rec store.GameRecord) error {
	c.mu.Lock()
	c.attempts = append(c.attempts, rec)
	c.mu.Unlock()
	return c.Recorder.RecordGame(ctx, rec)
}

func (c *countingRecorder) all() []store.GameRecord {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]store.GameRecord(nil), c.attempts...)
}

func okCount(e *env) float64 {
	return metricValue(e.t, e.reg, "tuttifrutti_stats_recorded_total", "result", "ok")
}

// ---- M1: the game id is minted when the game starts ----

func TestTheGameIDIsMintedAtTheStartAndKeptInEverySnapshotOfTheGame(t *testing.T) {
	rec := &fakeRecorder{}
	l := newLinkedLobby3(t, [3]string{userA, userB, ""}, withRecorder(rec))
	l.e.waitSaved(l.code)
	if p := rawStored(t, l.e, l.code); p.GameID != "" {
		t.Fatalf("the lobby snapshot has a game id: %q", p.GameID)
	}

	l.startRound(nil)
	l.e.waitSaved(l.code)
	id := rawStored(t, l.e, l.code).GameID
	if !store.ValidID(id) {
		t.Fatalf("the snapshot of a game in progress has game id %q", id)
	}
	if got := queryGameID(l.e, l.code); got != id {
		t.Errorf("room game id %q, snapshot %q", got, id)
	}

	step := func(what string) {
		t.Helper()
		l.e.waitSaved(l.code)
		if got := rawStored(t, l.e, l.code).GameID; got != id {
			t.Errorf("after %s the snapshot has game id %q, want %q", what, got, id)
		}
	}
	l.fillAnswers()
	l.send(0, game.CmdBasta, nil)
	step("basta")
	l.e.advance(game.GraceDuration + game.LateUpdateAllowance)
	step("the review started")
	l.send(0, game.CmdFinishReview, nil)
	step("the round summary")
	l.send(0, game.CmdNextRound, nil)
	step("game over")

	got := waitDone(t, rec, 1)[0]
	if got.ID != id {
		t.Errorf("recorded game id %q, want the one minted at the start: %q", got.ID, id)
	}

	l.send(0, game.CmdRematch, nil)
	l.e.waitSaved(l.code)
	if got := rawStored(t, l.e, l.code).GameID; got != "" {
		t.Errorf("the rematch left game id %q in the lobby snapshot", got)
	}
	finishGame(t, l)
	second := waitDone(t, rec, 2)[1]
	if second.ID == id || !store.ValidID(second.ID) {
		t.Errorf("the second game has id %q (first %q): a rematch must start a new game", second.ID, id)
	}
}

// The crash that M1 is about: the process dies after the statistics were
// committed but before the final snapshot was durable. The room comes back from
// an older snapshot and plays the end of the game again; it must be recorded
// under the id it already has, so the store counts it once.
func TestAGameReplayedAfterACrashIsRecordedUnderTheSameID(t *testing.T) {
	ctx := context.Background()
	mem := store.NewMemory()
	ua, err := mem.UpsertGoogleUser(ctx, "sub-a", "Ana")
	if err != nil {
		t.Fatal(err)
	}
	ub, err := mem.UpsertGoogleUser(ctx, "sub-b", "Beto")
	if err != nil {
		t.Fatal(err)
	}
	rec := &countingRecorder{Recorder: mem}
	users := [3]string{ua.ID, ub.ID, ""}
	l := newLinkedLobby3(t, users, withRecorder(rec))

	l.startRound(nil)
	l.fillAnswers()
	l.send(0, game.CmdBasta, nil)
	l.e.advance(game.GraceDuration + game.LateUpdateAllowance)
	l.send(0, game.CmdFinishReview, nil)
	l.e.waitSaved(l.code)
	pre := rawSnapshot(t, l.e, l.code)
	if _, g, err := decodePersisted(pre); err != nil || g.Phase != game.PhaseRoundSummary {
		t.Fatalf("setup: the snapshot is in phase %v (%v), want the round summary", g.Phase, err)
	}

	l.send(0, game.CmdNextRound, nil)
	eventually(t, "the game to be committed", func() bool {
		st, err := mem.UserStats(ctx, ua.ID)
		return err == nil && st.GamesPlayed == 1
	})
	first := rec.all()[0]

	// The process dies: the snapshot the store has is the one from before the end.
	sctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := l.e.hub.Shutdown(sctx); err != nil {
		t.Fatal(err)
	}
	pre.UpdatedAt = l.e.clock.Now()
	if err := l.e.store.Memory.Save(ctx, pre); err != nil {
		t.Fatal(err)
	}

	e2 := startedEnv(t, l.e, withRecorder(rec))
	l2 := l.rejoin(e2, users)
	l2.send(0, game.CmdNextRound, nil) // the host ends the game again
	eventually(t, "the replayed game to be offered", func() bool { return okCount(e2) == 1 })

	all := rec.all()
	if len(all) != 2 || all[1].ID != first.ID {
		ids := []string{}
		for _, r := range all {
			ids = append(ids, r.ID)
		}
		t.Fatalf("attempts under ids %v, want the same id twice (%s)", ids, first.ID)
	}
	for who, want := range map[string]totals{
		ua.ID: {games: 1, wins: 0, points: 25},
		ub.ID: {games: 1, wins: 1, points: 30},
	} {
		st, err := mem.UserStats(ctx, who)
		if err != nil {
			t.Fatal(err)
		}
		if st.GamesPlayed != want.games || st.Wins != want.wins || st.TotalPoints != want.points {
			t.Errorf("stats of %s after the replay = %+v, want %+v (the game counts once)", who, st, want)
		}
	}
}

type totals struct{ games, wins, points int }

func TestARestoredGameWithoutAnIDGetsOne(t *testing.T) {
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
	// What a server from before games had an id wrote: no "gameId", no "outbox".
	snap := rawSnapshot(t, l.e, l.code)
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(snap.State, &fields); err != nil {
		t.Fatal(err)
	}
	delete(fields, "gameId")
	delete(fields, "outbox")
	snap.State, _ = json.Marshal(fields)
	if err := l.e.store.Memory.Save(ctx, snap); err != nil {
		t.Fatal(err)
	}

	rec2 := &fakeRecorder{}
	e2 := startedEnv(t, l.e, withRecorder(rec2))
	id := queryGameID(e2, l.code)
	if !store.ValidID(id) {
		t.Fatalf("the restored game has id %q", id)
	}
	e2.waitSaved(l.code)
	if got := rawStored(t, e2, l.code).GameID; got != id {
		t.Errorf("the snapshot written after the restore has game id %q, want %q", got, id)
	}
	l2 := l.rejoin(e2, [3]string{userA, userB, ""})
	l2.send(0, game.CmdBasta, nil)
	e2.advance(game.GraceDuration + game.LateUpdateAllowance)
	l2.send(0, game.CmdFinishReview, nil)
	l2.send(0, game.CmdNextRound, nil)
	if got := waitDone(t, rec2, 1)[0]; got.ID != id {
		t.Errorf("recorded under %q, want the id given at the restore: %q", got.ID, id)
	}
}

// ---- M2: the outbox ----

func TestARematchWhileTheStoreFailsKeepsTheGameInTheOutboxAndARestartDeliversItOnce(t *testing.T) {
	failing := &fakeRecorder{fail: 1 << 20}
	l := newLinkedLobby3(t, [3]string{userA, userB, ""}, withRecorder(failing))
	finishGame(t, l)
	eventually(t, "the first attempt", func() bool { a, _ := failing.counts(); return a >= 1 })
	first := attemptsOf(failing)[0]

	l.send(0, game.CmdRematch, nil) // the final phase is gone; the game is not recorded yet
	l.e.waitSaved(l.code)
	p := rawStored(t, l.e, l.code)
	if p.GameID != "" || len(p.Pending) != 1 {
		t.Fatalf("lobby snapshot: game id %q, outbox %+v; want no game id and the finished game in the outbox", p.GameID, p.Pending)
	}
	sameRecord(t, p.Pending[0], first)

	rec2 := &fakeRecorder{}
	e2 := restartedEnv(t, l.e, withRecorder(rec2))
	got := waitDone(t, rec2, 1)
	sameRecord(t, got[0], first)
	eventually(t, "the outbox to empty after the commit", func() bool { return len(pendingIDs(e2, l.code)) == 0 })
	eventually(t, "the snapshot to forget the game", func() bool { return len(rawStored(t, e2, l.code).Pending) == 0 })
	e2.syncAll()
	if a, _ := rec2.counts(); a != 1 {
		t.Errorf("%d attempts after the restart, want exactly 1", a)
	}
	if v := metricValue(t, e2.reg, "tuttifrutti_stats_recorded_total", "result", "ok"); v != 1 {
		t.Errorf("ok = %v, want 1", v)
	}

	// A third process finds nothing left to do.
	rec3 := &fakeRecorder{}
	e3 := restartedEnv(t, e2, withRecorder(rec3))
	e3.syncAll()
	if a, _ := rec3.counts(); a != 0 {
		t.Errorf("a game that was committed was offered again by the next process (%d attempts)", a)
	}
}

func TestTheRecordIsFrozenAtGameOver(t *testing.T) {
	failing := &fakeRecorder{fail: 1 << 20}
	l := newLinkedLobby3(t, [3]string{userA, userB, ""}, withRecorder(failing))
	finishGame(t, l)
	eventually(t, "the first attempt", func() bool { a, _ := failing.counts(); return a >= 1 })
	first := attemptsOf(failing)[0]

	// Everything that can happen to the room afterwards: a guest signs in, a
	// player leaves, the host starts the rematch, the host comes back as somebody else.
	l.c[2], l.s[2] = l.e.joinAs(l.code, "p3", "Cami", userC)
	l.send(1, game.CmdLeave, nil)
	l.send(0, game.CmdRematch, nil)
	l.c[0], l.s[0] = l.e.joinAs(l.code, "p1", "Ana", userC)
	if links := queryLinks(l.e, l.code); links["p1"] != userC {
		t.Fatalf("setup: after the rematch the links are %v, p1 should follow the new connection", links)
	}

	failing.setFail(0)
	l.e.clock.Advance(time.Minute)
	got := waitDone(t, failing, 1)
	sameRecord(t, got[0], first)
}

func TestTheOutboxSurvivesTheSnapshotRoundTrip(t *testing.T) {
	g := finished([]string{"p1", "p2"}, []string{"p1"}, roundOf(1, map[string][]int{"p1": {10}, "p2": {5}}))
	r1 := store.GameRecord{ID: "11111111-1111-4111-8111-111111111111", PlayedAt: t0, Results: []store.UserResult{
		{UserID: userA, Won: true, Points: 25, BestRound: 25, UniqueAnswers: 2},
		{UserID: userB, Won: false, Points: 5, BestRound: 5, UniqueAnswers: 0},
	}}
	r2 := store.GameRecord{ID: "22222222-2222-4222-8222-222222222222", PlayedAt: t0.Add(time.Hour), Results: []store.UserResult{{UserID: userC, Points: 7, BestRound: 7}}}
	data, err := encodeRoomState(Persisted{Code: "BCDF", CreatedAt: t0, LastActive: t0, Pending: []store.GameRecord{r1, r2}, Outbox: outboxVersion}, g)
	if err != nil {
		t.Fatal(err)
	}
	p, _, err := decodePersisted(store.Snapshot{Code: "BCDF", State: data})
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Pending) != 2 || p.droppedPending != 0 {
		t.Fatalf("pending = %+v (dropped %d)", p.Pending, p.droppedPending)
	}
	sameRecord(t, p.Pending[0], r1)
	sameRecord(t, p.Pending[1], r2)
	if !strings.Contains(string(data), `"pending"`) || !strings.Contains(string(data), `"outbox":1`) {
		t.Errorf("snapshot JSON lacks the outbox fields: %s", data)
	}
}

func TestCleanPending(t *testing.T) {
	mk := func(i int) store.GameRecord {
		return store.GameRecord{ID: fmt.Sprintf("00000000-0000-4000-8000-%012d", i), PlayedAt: t0, Results: []store.UserResult{{UserID: userA, Points: i}}}
	}
	bad := store.GameRecord{ID: "not-a-uuid", Results: []store.UserResult{{UserID: userA}}}
	empty := store.GameRecord{ID: mk(99).ID}
	negative := store.GameRecord{ID: mk(98).ID, Results: []store.UserResult{{UserID: userA, Points: -1}}}

	got, dropped := cleanPending([]store.GameRecord{bad, mk(1), mk(1), empty, negative, mk(2)})
	if len(got) != 2 || got[0].ID != mk(1).ID || got[1].ID != mk(2).ID || dropped != 4 {
		t.Errorf("cleanPending = %v (dropped %d), want games 1 and 2 and 4 dropped", got, dropped)
	}
	if got, dropped := cleanPending(nil); got != nil || dropped != 0 {
		t.Errorf("cleanPending(nil) = %v, %d", got, dropped)
	}

	var many []store.GameRecord
	for i := 1; i <= maxPendingRecords+3; i++ {
		many = append(many, mk(i))
	}
	got, dropped = cleanPending(many)
	if len(got) != maxPendingRecords || got[0].ID != mk(4).ID || got[len(got)-1].ID != mk(maxPendingRecords+3).ID || dropped != 3 {
		t.Errorf("cleanPending of %d records kept %d (first %s), dropped %d; want the newest %d", len(many), len(got), got[0].ID, dropped, maxPendingRecords)
	}
}

func TestUnreadableOutboxRecordsInASnapshotAreDroppedAndCounted(t *testing.T) {
	rec := &fakeRecorder{}
	e := newEnv(t, withRecorder(rec))
	g := finished([]string{"p1", "p2"}, []string{"p1"}, roundOf(1, map[string][]int{"p1": {10}, "p2": {5}}))
	good := store.GameRecord{ID: "11111111-1111-4111-8111-111111111111", PlayedAt: t0, Results: []store.UserResult{{UserID: userA, Points: 10}}}
	bad := store.GameRecord{ID: "garbage", Results: []store.UserResult{{UserID: userA}}}
	data, err := encodeRoomState(Persisted{Code: "BCDF", CreatedAt: t0, LastActive: t0, GameID: gameID, Outbox: outboxVersion,
		Pending: []store.GameRecord{bad, good, good}}, g)
	if err != nil {
		t.Fatal(err)
	}
	if err := e.store.Save(context.Background(), store.Snapshot{Code: "BCDF", State: data, UpdatedAt: t0}); err != nil {
		t.Fatal(err)
	}
	e2 := startedEnv(t, e, withRecorder(rec))
	got := waitDone(t, rec, 1)
	if len(got) != 1 || got[0].ID != good.ID {
		t.Errorf("recorded %+v, want only the good record, once", got)
	}
	if v := metricValue(t, e2.reg, "tuttifrutti_stats_recorded_total", "result", "dropped"); v != 2 {
		t.Errorf("dropped = %v, want 2 (the garbage and the repeat)", v)
	}
}

func TestAFinalSnapshotWithTheOutboxMarkerDoesNotRebuildItsGame(t *testing.T) {
	g := finished([]string{"p1", "p2"}, []string{"p1"}, roundOf(1, map[string][]int{"p1": {10}, "p2": {5}}))
	save := func(e *env, marker int) {
		data, err := encodeRoomState(Persisted{Code: "BCDF", CreatedAt: t0, LastActive: t0, Users: map[string]string{"p1": userA},
			GameID: gameID, Outbox: marker}, g)
		if err != nil {
			t.Fatal(err)
		}
		if err := e.store.Save(context.Background(), store.Snapshot{Code: "BCDF", State: data, UpdatedAt: t0}); err != nil {
			t.Fatal(err)
		}
	}

	// Written by a server with the outbox, which committed the game: nothing to do.
	rec := &fakeRecorder{}
	e := newEnv(t, withRecorder(rec))
	save(e, outboxVersion)
	e2 := startedEnv(t, e, withRecorder(rec))
	e2.syncAll()
	if a, _ := rec.counts(); a != 0 {
		t.Errorf("a final snapshot with an empty outbox was rebuilt (%d attempts)", a)
	}

	// Written by a server without it: the game may not have been recorded, so it is
	// rebuilt (that is TestSnapshotLinksAreSanitisedOnRestore too).
	rec3 := &fakeRecorder{}
	e3 := newEnv(t, withRecorder(rec3))
	save(e3, 0)
	startedEnv(t, e3, withRecorder(rec3))
	got := waitDone(t, rec3, 1)[0]
	if got.ID != gameID || len(got.Results) != 1 || got.Results[0].UserID != userA {
		t.Errorf("legacy rebuild = %+v", got)
	}
}

func TestTheOutboxIsBoundedAndTheOldestGameIsGivenUp(t *testing.T) {
	rec := &fakeRecorder{}
	l := newLinkedLobby3(t, [3]string{userA, userB, ""}, withRecorder(rec))
	setQueueSize(l.e, 0) // nothing is delivered: every game stays in the outbox
	var ids []string
	for range maxPendingRecords + 2 {
		finishGame(t, l)
		pend := pendingIDs(l.e, l.code)
		ids = append(ids, pend[len(pend)-1])
		l.send(0, game.CmdRematch, nil)
	}
	if got := pendingIDs(l.e, l.code); !slices.Equal(got, ids[2:]) {
		t.Fatalf("outbox = %v, want the newest %d games %v", got, maxPendingRecords, ids[2:])
	}
	l.e.waitSaved(l.code)
	var stored []string
	for _, p := range rawStored(t, l.e, l.code).Pending {
		stored = append(stored, p.ID)
	}
	if !slices.Equal(stored, ids[2:]) {
		t.Errorf("snapshot outbox = %v, want %v", stored, ids[2:])
	}
	if v := metricValue(t, l.e.reg, "tuttifrutti_stats_recorded_total", "result", "dropped"); v != 2 {
		t.Errorf("dropped = %v, want 2 (the two oldest games)", v)
	}
	if a, _ := rec.counts(); a != 0 {
		t.Errorf("%d attempts although the queue was closed", a)
	}

	// Room again in the queue: the next snapshot offers everything, oldest first.
	setQueueSize(l.e, statsQueueSize)
	l.send(0, game.CmdUpdateSettings, threeCats(nil))
	got := waitDone(t, rec, maxPendingRecords)
	var gotIDs []string
	for _, r := range got {
		gotIDs = append(gotIDs, r.ID)
	}
	if !slices.Equal(gotIDs, ids[2:]) {
		t.Errorf("recorded %v, want %v in order", gotIDs, ids[2:])
	}
	eventually(t, "the outbox to empty", func() bool { return len(pendingIDs(l.e, l.code)) == 0 })
}

func TestAFullQueueLeavesTheGameInTheOutboxUntilTheNextSnapshot(t *testing.T) {
	rec := &fakeRecorder{}
	l := newLinkedLobby3(t, [3]string{userA, userB, ""}, withRecorder(rec))
	setQueueSize(l.e, 0)
	finishGame(t, l)
	ids := pendingIDs(l.e, l.code)
	if len(ids) != 1 {
		t.Fatalf("outbox = %v", ids)
	}
	if a, _ := rec.counts(); a != 0 {
		t.Fatalf("%d attempts although the queue was full", a)
	}
	if queryInflight(l.e, l.code, ids[0]) {
		t.Error("a refused game is marked as being delivered")
	}
	l.e.waitSaved(l.code)
	if p := rawStored(t, l.e, l.code); len(p.Pending) != 1 || p.Pending[0].ID != ids[0] {
		t.Errorf("snapshot outbox = %+v", p.Pending)
	}

	setQueueSize(l.e, statsQueueSize)
	l.send(0, game.CmdRematch, nil)
	if got := waitDone(t, rec, 1); got[0].ID != ids[0] {
		t.Errorf("recorded %s, want %s", got[0].ID, ids[0])
	}
}

func TestACommittedGameLeavesTheOutboxAndTheNextSnapshotForgetsIt(t *testing.T) {
	rec := &fakeRecorder{}
	l := newLinkedLobby3(t, [3]string{userA, userB, ""}, withRecorder(rec))
	finishGame(t, l)
	waitDone(t, rec, 1)
	eventually(t, "the outbox to empty", func() bool { return len(pendingIDs(l.e, l.code)) == 0 })
	// The snapshot is written without anybody asking for it: the commit is a
	// reason to save (a final-phase room has no other).
	eventually(t, "the snapshot to forget the game", func() bool { return len(rawStored(t, l.e, l.code).Pending) == 0 })
	if p := rawStored(t, l.e, l.code); !store.ValidID(p.GameID) {
		t.Errorf("the final snapshot lost the game id: %q", p.GameID)
	}
}

func TestAGameTheStoreRefusesLeavesTheOutbox(t *testing.T) {
	rec := &fakeRecorder{fail: 1, err: fmt.Errorf("wrapped: %w", store.ErrInvalidGame)}
	l := newLinkedLobby3(t, [3]string{userA, userB, ""}, withRecorder(rec))
	finishGame(t, l)
	eventually(t, "the outbox to give the game up", func() bool { return len(pendingIDs(l.e, l.code)) == 0 })
	eventually(t, "the snapshot to forget it", func() bool { return len(rawStored(t, l.e, l.code).Pending) == 0 })
	l.send(0, game.CmdRematch, nil)
	l.e.clock.Advance(time.Hour)
	l.e.syncAll()
	if a, _ := rec.counts(); a != 1 {
		t.Errorf("%d attempts: a game the store refused for good was offered again", a)
	}
}

func TestAGameInFlightIsNotOfferedAgain(t *testing.T) {
	gate := make(chan struct{})
	rec := &fakeRecorder{gate: gate}
	l := newLinkedLobby3(t, [3]string{userA, userB, ""}, withRecorder(rec))
	finishGame(t, l)
	eventually(t, "the attempt to start", func() bool { a, _ := rec.counts(); return a == 1 })
	l.send(0, game.CmdRematch, nil) // a snapshot
	l.send(0, game.CmdUpdateSettings, threeCats(nil))
	if n := l.e.hub.recorder.pending(); n != 0 {
		t.Errorf("%d copies of the game queued behind the one in flight", n)
	}
	close(gate)
	waitDone(t, rec, 1)
	l.e.syncAll()
	if a, _ := rec.counts(); a != 1 {
		t.Errorf("%d attempts for one game", a)
	}
}

func TestARoomClosedForGoodHandsItsStalledGamesToTheRecorder(t *testing.T) {
	rec := &fakeRecorder{fail: 1 << 20}
	l := newLinkedLobby3(t, [3]string{userA, userB, ""}, withRecorder(rec))
	l.e.hub.recorder.maxAttempts = 1
	finishGame(t, l)
	id := pendingIDs(l.e, l.code)[0]
	eventually(t, "the recorder to stall", func() bool { return !queryInflight(l.e, l.code, id) })

	// Everybody leaves: the room and its snapshot are deleted, and the game with them
	// unless the recorder takes it.
	rec.setFail(0)
	l.send(0, game.CmdLeave, nil)
	l.send(1, game.CmdLeave, nil)
	l.s[2].Handle(game.CmdLeave, nil)
	got := waitDone(t, rec, 1)
	if got[0].ID != id {
		t.Errorf("recorded %s, want %s", got[0].ID, id)
	}
}

func TestARoomClosedForGoodWithoutAnyRoomInTheQueueCountsTheGameAsDropped(t *testing.T) {
	rec := &fakeRecorder{fail: 1 << 20}
	l := newLinkedLobby3(t, [3]string{userA, userB, ""}, withRecorder(rec))
	l.e.hub.recorder.maxAttempts = 1
	finishGame(t, l)
	id := pendingIDs(l.e, l.code)[0]
	eventually(t, "the recorder to stall", func() bool { return !queryInflight(l.e, l.code, id) })
	setQueueSize(l.e, 0)
	l.send(0, game.CmdLeave, nil)
	l.send(1, game.CmdLeave, nil)
	l.s[2].Handle(game.CmdLeave, nil)
	eventually(t, "the game to be given up", func() bool {
		return metricValue(t, l.e.reg, "tuttifrutti_stats_recorded_total", "result", "dropped") == 1
	})
}

// ---- M3: a player's link is frozen once they took part ----

func TestTheLinkIsFrozenOnceThePlayerTookPart(t *testing.T) {
	rec := &fakeRecorder{}
	l := newLinkedLobby3(t, [3]string{userA, userB, ""}, withRecorder(rec))
	l.startRound(nil) // round 1: all three are participants

	// Whoever holds p1's guest cookie reconnects signed in as somebody else...
	l.c[0], l.s[0] = l.e.joinAs(l.code, "p1", "Ana", userC)
	if got := queryLinks(l.e, l.code)["p1"]; got != userA {
		t.Fatalf("p1 is linked to %q after a reconnect as userC, want %q", got, userA)
	}
	// ...or signed out (a session that expired must not cost the credit).
	l.c[0], l.s[0] = l.e.joinAs(l.code, "p1", "Ana", "")
	if got := queryLinks(l.e, l.code)["p1"]; got != userA {
		t.Fatalf("p1 is linked to %q after a reconnect as a guest, want %q", got, userA)
	}
	// A guest who signs in during the game gets no credit for it.
	l.c[2], l.s[2] = l.e.joinAs(l.code, "p3", "Cami", userC)
	if got, linked := queryLinks(l.e, l.code)["p3"]; linked {
		t.Fatalf("p3, a guest who played, got linked to %q mid-game", got)
	}

	// Once the round is scored nothing changes either.
	l.fillAnswers()
	l.send(0, game.CmdBasta, nil)
	l.e.advance(game.GraceDuration + game.LateUpdateAllowance)
	l.send(0, game.CmdFinishReview, nil)
	l.c[0], l.s[0] = l.e.joinAs(l.code, "p1", "Ana", userC)
	l.c[1], l.s[1] = l.e.joinAs(l.code, "p2", "Beto", "")
	l.c[2], l.s[2] = l.e.joinAs(l.code, "p3", "Cami", userC)
	links := queryLinks(l.e, l.code)
	if links["p1"] != userA || links["p2"] != userB || links["p3"] != "" || len(links) != 2 {
		t.Fatalf("links after the round was scored = %v, want p1 and p2 as they were", links)
	}

	l.send(0, game.CmdNextRound, nil)
	got := waitDone(t, rec, 1)[0]
	if len(got.Results) != 2 || got.Results[0] != wantA || got.Results[1] != wantB {
		t.Errorf("results = %+v, want %+v and %+v: userC gets nothing", got.Results, wantA, wantB)
	}
}

func TestTheLinkFollowsTheLatestConnectionUntilTheGameStarts(t *testing.T) {
	rec := &fakeRecorder{}
	l := newLinkedLobby3(t, [3]string{userA, userB, ""}, withRecorder(rec))
	l.c[0], l.s[0] = l.e.joinAs(l.code, "p1", "Ana", userC)
	l.c[2], l.s[2] = l.e.joinAs(l.code, "p3", "Cami", userA)
	l.c[1], l.s[1] = l.e.joinAs(l.code, "p2", "Beto", "")
	links := queryLinks(l.e, l.code)
	if links["p1"] != userC || links["p3"] != userA || links["p2"] != "" || len(links) != 2 {
		t.Errorf("links = %v: in the lobby they must follow the latest connection", links)
	}
}

func TestAPlayerWhoJoinsMidGameIsFrozenFromTheirFirstRound(t *testing.T) {
	rec := &fakeRecorder{}
	l := newLinkedLobby3(t, [3]string{userA, userB, ""}, withRecorder(rec))
	l.startRound(func(s *game.Settings) { s.Rounds = 2 })

	// p4 sits down during round 1: not a participant yet.
	l.e.joinAs(l.code, "p4", "Dani", "")
	l.e.joinAs(l.code, "p4", "Dani", userC)
	if got := queryLinks(l.e, l.code)["p4"]; got != userC {
		t.Fatalf("p4 (not in a round yet) is linked to %q, want it to follow the connection: %q", got, userC)
	}

	l.fillAnswers()
	l.send(0, game.CmdBasta, nil)
	l.e.advance(game.GraceDuration + game.LateUpdateAllowance)
	l.send(0, game.CmdFinishReview, nil)
	l.send(0, game.CmdNextRound, nil) // round 2: p4 takes part
	l.e.joinAs(l.code, "p4", "Dani", "")
	if got := queryLinks(l.e, l.code)["p4"]; got != userC {
		t.Errorf("p4 is linked to %q in round 2, want %q (frozen)", got, userC)
	}
	l.e.joinAs(l.code, "p4", "Dani", userA)
	if got := queryLinks(l.e, l.code)["p4"]; got != userC {
		t.Errorf("p4 is linked to %q after a reconnect as userA, want %q (frozen)", got, userC)
	}
}

func TestLinksCanBeRefreshedAfterTheRematch(t *testing.T) {
	rec := &fakeRecorder{}
	l := newLinkedLobby3(t, [3]string{userA, userB, ""}, withRecorder(rec))
	finishGame(t, l)
	first := waitDone(t, rec, 1)[0]

	// In the final phase the game is still over: no change.
	l.c[0], l.s[0] = l.e.joinAs(l.code, "p1", "Ana", userC)
	if got := queryLinks(l.e, l.code)["p1"]; got != userA {
		t.Fatalf("p1 is linked to %q in the final phase, want %q", got, userA)
	}

	l.send(0, game.CmdRematch, nil)
	l.c[0], l.s[0] = l.e.joinAs(l.code, "p1", "Ana", userC)
	if got := queryLinks(l.e, l.code)["p1"]; got != userC {
		t.Fatalf("p1 is linked to %q after the rematch, want a fresh link to %q", got, userC)
	}
	finishGame(t, l)
	second := waitDone(t, rec, 2)[1]
	if second.ID == first.ID || second.Results[0].UserID != userC {
		t.Errorf("second game = %+v: p1 should now count for userC", second)
	}
	if first.Results[0].UserID != userA {
		t.Errorf("first game = %+v: it belongs to userA", first)
	}
}

func TestAPlayerWhoPlayedARoundStaysFrozenEvenIfTheyMissTheNextOne(t *testing.T) {
	rec := &fakeRecorder{}
	l := newLinkedLobby3(t, [3]string{userA, userB, userC}, withRecorder(rec))
	l.startRound(func(s *game.Settings) { s.Rounds = 2 })
	l.fillAnswers()
	l.send(0, game.CmdBasta, nil)
	l.e.advance(game.GraceDuration + game.LateUpdateAllowance)
	l.send(0, game.CmdFinishReview, nil)
	l.send(2, game.CmdLeave, nil) // p3 left after round 1: not a participant of round 2
	l.send(0, game.CmdNextRound, nil)
	if in := query(l.e, l.code, func(r *room) bool { return slices.Contains(r.g.Round.Participants, "p3") }); in {
		t.Fatal("setup: p3 is a participant of round 2")
	}

	// Back with the seat of round 1, signed in as somebody else.
	l.e.joinAs(l.code, "p3", "Cami", userA)
	if got := queryLinks(l.e, l.code)["p3"]; got != userC {
		t.Errorf("p3 is linked to %q, want %q: a scored round freezes the link", got, userC)
	}
}

func TestARecorderCallbackThatPanicsDoesNotStopTheRecorder(t *testing.T) {
	rec := &fakeRecorder{}
	e := newEnv(t, withRecorder(rec))
	q := e.hub.recorder
	mk := func(i int) store.GameRecord {
		return store.GameRecord{ID: fmt.Sprintf("00000000-0000-4000-8000-%012d", i), Results: []store.UserResult{{UserID: userA}}}
	}
	if !q.enqueue(statsJob{rec: mk(1), done: func(statsOutcome) { panic("callback blew up") }}) {
		t.Fatal("refused")
	}
	var outcome statsOutcome = -1
	var mu sync.Mutex
	if !q.enqueue(statsJob{rec: mk(2), done: func(o statsOutcome) { mu.Lock(); outcome = o; mu.Unlock() }}) {
		t.Fatal("refused")
	}
	waitDone(t, rec, 2)
	eventually(t, "the second callback", func() bool { mu.Lock(); defer mu.Unlock(); return outcome == outcomeRecorded })
	if !strings.Contains(e.logs.String(), "callback panicked") {
		t.Error("the panic was not logged")
	}
}

func TestAGameThatLostItsIDGetsOneAtGameOver(t *testing.T) {
	rec := &fakeRecorder{}
	l := newLinkedLobby3(t, [3]string{userA, userB, ""}, withRecorder(rec))
	l.startRound(nil)
	query(l.e, l.code, func(r *room) bool { r.gameID = ""; return true }) // cannot happen; the room copes anyway
	l.fillAnswers()
	l.send(0, game.CmdBasta, nil)
	l.e.advance(game.GraceDuration + game.LateUpdateAllowance)
	l.send(0, game.CmdFinishReview, nil)
	l.send(0, game.CmdNextRound, nil)
	got := waitDone(t, rec, 1)[0]
	if !store.ValidID(got.ID) || len(got.Results) != 2 {
		t.Errorf("recorded %+v", got)
	}
	l.e.waitSaved(l.code)
	if p := rawStored(t, l.e, l.code); p.GameID != got.ID {
		t.Errorf("snapshot game id %q, recorded %q", p.GameID, got.ID)
	}
}

func TestTheOutboxNeverHoldsTheSameGameTwice(t *testing.T) {
	l := newLinkedLobby3(t, [3]string{userA, userB, ""}, withRecorder(&fakeRecorder{}))
	rec := store.GameRecord{ID: gameID, PlayedAt: t0, Results: []store.UserResult{{UserID: userA, Points: 1}}}
	ids := query(l.e, l.code, func(r *room) []string {
		r.addPending(rec)
		r.addPending(rec)
		return pendingOf(r)
	})
	if len(ids) != 1 {
		t.Errorf("outbox = %v, want the game once", ids)
	}
}

func pendingOf(r *room) []string {
	ids := []string{}
	for _, p := range r.pending {
		ids = append(ids, p.ID)
	}
	return ids
}

func TestAnAnswerAboutAGameTheOutboxNoLongerHoldsIsHarmless(t *testing.T) {
	l := newLinkedLobby3(t, [3]string{userA, userB, ""}, withRecorder(&fakeRecorder{}))
	l.e.waitSaved(l.code)
	saves := func() int { l.e.store.mu.Lock(); defer l.e.store.mu.Unlock(); return len(l.e.store.saves) }
	before := saves()
	query(l.e, l.code, func(r *room) bool {
		r.inflight = map[string]bool{gameID: true}
		r.onStatsDone(gameID, outcomeRecorded) // evicted meanwhile, say
		if r.inflight[gameID] {
			t.Error("the answer left the game marked as in flight")
		}
		return true
	})
	l.e.waitSaved(l.code)
	if got := saves(); got != before {
		t.Errorf("an answer about an unknown game caused %d snapshots", got-before)
	}

	// And a room that is shutting down only forgets the record: its final
	// snapshot is already on its way and nothing else is saved.
	query(l.e, l.code, func(r *room) bool {
		r.pending = []store.GameRecord{{ID: gameID}}
		r.stopping = true
		r.onStatsDone(gameID, outcomeRecorded)
		r.stopping = false
		if len(r.pending) != 0 {
			t.Error("the record stayed in the outbox")
		}
		return true
	})
	l.e.waitSaved(l.code)
	if got := saves(); got != before {
		t.Errorf("a stopping room saved %d snapshots", got-before)
	}
}
