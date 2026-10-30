package room

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/AresiusXP/tutti-frutti/server/internal/game"
	"github.com/AresiusXP/tutti-frutti/server/internal/protocol"
	"github.com/AresiusXP/tutti-frutti/server/internal/safety"
	"github.com/AresiusXP/tutti-frutti/server/internal/store"
)

// forget calls Hub.ForgetAccount like DELETE /api/me does and checks it
// answered.
func forget(t *testing.T, h *Hub, user string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := h.ForgetAccount(ctx, user); err != nil {
		t.Fatalf("ForgetAccount: %v", err)
	}
}

func skipped(e *env, source string) float64 {
	return metricValue(e.t, e.reg, "tuttifrutti_stats_skipped_users_total", "source", source)
}

// snapshotText is the stored snapshot of a room as text, to look for ids in.
func snapshotText(t *testing.T, e *env, code string) string {
	t.Helper()
	return string(rawSnapshot(t, e, code).State)
}

// ---- the AI judge: an open socket loses it ----

func TestForgetAccountRevokesTheJudgeOfAnOpenSocket(t *testing.T) {
	j := newFakeJudge()
	l := newJudgeReview(t, j, "user-1")
	e := l.e

	// The host may ask while the account exists.
	if st := lastState(t, e.broadcastProbe(l)); !st.CanJudge {
		t.Fatal("the signed-in host cannot ask the judge before the deletion")
	}

	forget(t, e.hub, "user-1")

	// The host's socket is told at once: the button goes away.
	st := lastState(t, l.c[0].drain())
	if st.CanJudge {
		t.Error("the state after the deletion still offers the judge")
	}
	// Everybody gets the same view: the seat, the scores and the answers stay.
	if st.View.HostID != "p1" || playerView(t, st.View, "p1").Name != "Ana" {
		t.Errorf("the deleted account's seat changed: host %q, %+v", st.View.HostID, st.View.Players)
	}
	// The other signed-in player is untouched.
	if st := lastState(t, l.c[1].drain()); !st.CanJudge {
		t.Error("another account lost the judge")
	}

	// And the open socket cannot ask any more, whatever its client believes.
	l.send(0, game.CmdAskJudge, game.AskJudge{Category: 0})
	l.expectFrames("ask after the deletion", []string{"error:" + protocol.ErrLoginRequired}, 0)
	if n := len(j.calls()); n != 0 {
		t.Errorf("the judge was called %d times for a deleted account", n)
	}
	if got := metricValue(t, e.reg, "tuttifrutti_commands_total", "type", game.CmdAskJudge, "result", protocol.ErrLoginRequired); got != 1 {
		t.Errorf("login_required counted %v times", got)
	}
}

// broadcastProbe asks the room for a fresh state of the host without changing
// anything: the state the host would see now.
func (e *env) broadcastProbe(l *lobby3) []frameRec {
	e.t.Helper()
	query(e, l.code, func(r *room) bool { r.broadcast(r.clock.Now(), nil); return true })
	return l.c[0].drain()
}

// TestForgetAccountReachesAnUnjoinedSocket: a socket that attached and has not
// joined yet carries the account too; its join must not bring the links back.
func TestForgetAccountReachesAnUnjoinedSocket(t *testing.T) {
	rec := &fakeRecorder{}
	e := newEnv(t, withRecorder(rec))
	code := e.create("p1", "Ana")
	c := newFakeClient("p1")
	c.user = userA
	s, err := e.hub.Connect(code, c)
	if err != nil {
		t.Fatal(err)
	}

	forget(t, e.hub, userA)
	e.handle(code, s, game.CmdJoin, protocol.JoinRequest{Code: code, Name: "Ana", Avatar: testAvatar})

	if links := queryLinks(e, code); len(links) != 0 {
		t.Errorf("a join after the deletion linked %v", links)
	}
	if got := query(e, code, func(r *room) map[string]string { return r.accounts }); len(got) != 0 {
		t.Errorf("a join after the deletion recorded the account for reports: %v", got)
	}
	if st := lastState(t, c.drain()); st.CanJudge {
		t.Error("canJudge after the deletion")
	}
}

// TestAConnectionIdentifiedBeforeTheDeletionIsAGuestIfItAttachesAfter: the
// transport reads the session when the socket opens and the room is told when
// the first frame arrives; the deletion can fall in between.
func TestAConnectionIdentifiedBeforeTheDeletionIsAGuestIfItAttachesAfter(t *testing.T) {
	rec := &fakeRecorder{}
	e := newEnv(t, withRecorder(rec))
	code := e.create("p1", "Ana")
	forget(t, e.hub, userA) // no room has the account yet

	c, _ := e.joinAs(code, "p1", "Ana", userA) // the client still says userA
	if links := queryLinks(e, code); len(links) != 0 {
		t.Errorf("links = %v", links)
	}
	if user := query(e, code, func(r *room) string { return r.players["p1"].userID }); user != "" {
		t.Errorf("the session carries %q", user)
	}
	_ = c

	// The memory of a deletion is bounded in time: long after, the id is free
	// (it never comes back: ids are random UUIDs, and the sessions refuse the
	// cookie anyway).
	e.clock.Set(e.clock.Now().Add(forgottenTTL + time.Second))
	if e.hub.forgotten.has(userA, e.clock.Now()) {
		t.Error("the hub remembers a deleted account forever")
	}
}

func TestForgottenAccountsAreBounded(t *testing.T) {
	var f forgottenAccounts
	now := t0
	for i := range maxForgotten + 50 {
		f.add(fmt.Sprintf("user-%d", i), now)
		if len(f.until) > maxForgotten {
			t.Fatalf("the memory grew to %d entries", len(f.until))
		}
	}
}

func TestForgetAccountOfSomebodyNotInAnyRoomChangesNothing(t *testing.T) {
	e := newEnv(t)
	code := e.create("p1", "Ana")
	c, _ := e.join(code, "p1", "Ana")
	c.drain()
	e.waitSaved(code)
	saves := e.store.saveCount(code)

	forget(t, e.hub, userC)
	if err := e.hub.ForgetAccount(context.Background(), ""); err != nil {
		t.Errorf("ForgetAccount(\"\") = %v", err)
	}
	e.waitSaved(code)
	if got := e.store.saveCount(code); got != saves {
		t.Errorf("a room that had no link to the account saved again (%d -> %d)", saves, got)
	}
	if frames := c.drain(); len(frames) != 0 {
		t.Errorf("an unrelated player was sent %v", kinds(frames))
	}
}

// ---- the links and the outbox ----

// pendingLobby plays a whole game in which p1 (host), p2 and p3 are signed in as
// users A, B and C, with a store that is down: the finished game stays in the
// outbox. The host's custom label is on the settings.
func pendingLobby(t *testing.T, users [3]string) (*lobby3, *fakeRecorder) {
	t.Helper()
	rec := &fakeRecorder{fail: 1 << 20}
	l := newLinkedLobby3(t, users, withRecorder(rec))
	l.startRound(func(s *game.Settings) {
		s.Categories = []game.CategoryRef{{ID: "nombre"}, {ID: "pais"}, {Custom: "Cosa rara"}}
	})
	l.fillAnswers()
	l.send(0, game.CmdBasta, nil)
	l.e.advance(game.GraceDuration + game.LateUpdateAllowance)
	l.send(0, game.CmdFinishReview, nil)
	l.send(0, game.CmdNextRound, nil)
	if phase := query(l.e, l.code, func(r *room) game.Phase { return r.g.Phase }); phase != game.PhaseFinal {
		t.Fatalf("setup: phase = %s, want final", phase)
	}
	eventually(t, "the game in the outbox", func() bool { return len(pendingIDs(l.e, l.code)) == 1 })
	l.drain()
	return l, rec
}

func TestForgetAccountDropsEveryLinkAndKeepsTheSeat(t *testing.T) {
	l, _ := pendingLobby(t, [3]string{userA, userB, userC})
	e := l.e

	before := query(e, l.code, func(r *room) game.View { return r.g.View("p2") })
	if links := queryLinks(e, l.code); len(links) != 3 {
		t.Fatalf("setup: links = %v", links)
	}
	forget(t, e.hub, userB)

	// The room's live state.
	links := queryLinks(e, l.code)
	if len(links) != 2 || links["p2"] != "" || links["p1"] != userA || links["p3"] != userC {
		t.Errorf("links after the deletion = %v", links)
	}
	accounts := query(e, l.code, func(r *room) map[string]string { return r.accounts })
	if len(accounts) != 2 || accounts["p2"] != "" {
		t.Errorf("report links after the deletion = %v", accounts)
	}
	rec := query(e, l.code, func(r *room) []store.GameRecord { return r.pending })
	if len(rec) != 1 || len(rec[0].Results) != 2 {
		t.Fatalf("the outbox after the deletion = %+v", rec)
	}
	for _, res := range rec[0].Results {
		if res.UserID == userB {
			t.Errorf("the outbox still credits the deleted account: %+v", res)
		}
	}
	if got := metricValue(t, e.reg, "tuttifrutti_stats_outbox_pending"); got != 1 {
		t.Errorf("outbox gauge = %v, want the record kept for the others", got)
	}
	if got := skipped(e, skipForgotten); got != 1 {
		t.Errorf("skipped{forgotten} = %v, want 1", got)
	}

	// The guest seat stays, with its scores and its answers.
	after := query(e, l.code, func(r *room) game.View { return r.g.View("p2") })
	if playerView(t, after, "p2") != playerView(t, before, "p2") {
		t.Errorf("the seat changed: %+v -> %+v", playerView(t, before, "p2"), playerView(t, after, "p2"))
	}
	if after.Phase != before.Phase || len(after.History) != len(before.History) || after.History[0].Players["p2"].Total != before.History[0].Players["p2"].Total {
		t.Error("the game changed")
	}

	// The saved snapshot no longer names the account anywhere.
	e.waitSaved(l.code)
	if text := snapshotText(t, e, l.code); strings.Contains(text, userB) {
		t.Errorf("the snapshot still names the deleted account:\n%s", text)
	} else if !strings.Contains(text, userA) || !strings.Contains(text, userC) {
		t.Error("the snapshot lost the other accounts")
	}
}

func TestForgetAccountTakesTheLabelAuthorsAccountButNotTheGuest(t *testing.T) {
	l, _ := pendingLobby(t, [3]string{userA, userB, userC})
	e := l.e
	authors := query(e, l.code, func(r *room) map[string]safety.Author { return r.labelAuthors })
	if got := authors["Cosa rara"]; got != (safety.Author{Guest: "p1", User: userA}) {
		t.Fatalf("setup: author = %+v", got)
	}
	forget(t, e.hub, userA)
	authors = query(e, l.code, func(r *room) map[string]safety.Author { return r.labelAuthors })
	if got := authors["Cosa rara"]; got != (safety.Author{Guest: "p1"}) {
		t.Errorf("author after the deletion = %+v, want the guest only", got)
	}
}

// TestAGameOnlyTheDeletedUserHadIsDropped: nothing is left to record, so the
// record leaves the outbox (and the gauge) instead of retrying for ever.
func TestAGameOnlyTheDeletedUserHadIsDropped(t *testing.T) {
	l, rec := pendingLobby(t, [3]string{userA, "", ""})
	e := l.e
	forget(t, e.hub, userA)

	if ids := pendingIDs(e, l.code); len(ids) != 0 {
		t.Errorf("outbox = %v, want it empty", ids)
	}
	if got := metricValue(t, e.reg, "tuttifrutti_stats_outbox_pending"); got != 0 {
		t.Errorf("outbox gauge = %v", got)
	}
	if got := skipped(e, skipForgotten); got != 1 {
		t.Errorf("skipped{forgotten} = %v", got)
	}
	e.waitSaved(l.code)
	if p := rawStored(t, e, l.code); len(p.Pending) != 0 || strings.Contains(snapshotText(t, e, l.code), userA) {
		t.Errorf("the snapshot still has the record: %+v", p.Pending)
	}
	// The recorder was given the original while the store was down; it is not
	// told anything new, and a later attempt is the store's to skip.
	if n, _ := rec.counts(); n == 0 {
		t.Error("setup: the recorder never saw the record")
	}
}

func TestScrubAccountLinks(t *testing.T) {
	rec := func(id string, users ...string) store.GameRecord {
		r := store.GameRecord{ID: id}
		for _, u := range users {
			r.Results = append(r.Results, store.UserResult{UserID: u, Points: 1})
		}
		return r
	}
	gone := func(id string) bool { return id == userB }

	t.Run("nothing to do", func(t *testing.T) {
		pending := []store.GameRecord{rec("g1", userA)}
		got, sc := scrubAccountLinks(map[string]string{"p1": userA}, nil, nil, pending, gone)
		if sc.any() || len(got) != 1 || got[0].Results[0].UserID != userA {
			t.Errorf("got %+v %+v", got, sc)
		}
	})
	t.Run("nil everything", func(t *testing.T) {
		if got, sc := scrubAccountLinks(nil, nil, nil, nil, gone); got != nil || sc.any() {
			t.Errorf("got %+v %+v", got, sc)
		}
	})
	t.Run("records are never edited in place", func(t *testing.T) {
		original := rec("g1", userA, userB, userC)
		results := original.Results
		got, sc := scrubAccountLinks(nil, nil, nil, []store.GameRecord{original}, gone)
		if len(got) != 1 || len(got[0].Results) != 2 || sc.participations != 1 {
			t.Fatalf("got %+v %+v", got, sc)
		}
		if len(results) != 3 || results[1].UserID != userB {
			t.Errorf("the shared results slice was edited: %+v", results)
		}
	})
	t.Run("everything at once", func(t *testing.T) {
		users := map[string]string{"p1": userA, "p2": userB}
		accounts := map[string]string{"p2": userB, "p3": userB}
		authors := map[string]safety.Author{"x": {Guest: "p2", User: userB}, "y": {Guest: "p1", User: userA}, "z": {Guest: "p3"}}
		got, sc := scrubAccountLinks(users, accounts, authors,
			[]store.GameRecord{rec("g1", userB), rec("g2", userA, userB), rec("g3", userC)}, gone)
		if sc != (scrubbed{links: 3, labels: 1, participations: 2, records: 1}) {
			t.Errorf("scrubbed = %+v", sc)
		}
		if len(users) != 1 || len(accounts) != 0 || authors["x"] != (safety.Author{Guest: "p2"}) || authors["y"].User != userA {
			t.Errorf("users %v accounts %v authors %v", users, accounts, authors)
		}
		if len(got) != 2 || got[0].ID != "g2" || got[1].ID != "g3" {
			t.Errorf("pending = %+v", got)
		}
	})
}

// ---- restore: the account was deleted while the server was down ----

// accountsWith is a Memory store that keeps the accounts of the tests: it is
// both the Recorder and the directory a restore asks.
func accountsWith(t *testing.T, subs ...string) (*store.Memory, []string) {
	t.Helper()
	mem := store.NewMemory()
	var ids []string
	for _, sub := range subs {
		u, err := mem.UpsertGoogleUser(context.Background(), sub, sub)
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, u.ID)
	}
	return mem, ids
}

func TestRestoreDropsTheLinksOfAccountsDeletedWhileTheServerWasDown(t *testing.T) {
	mem, ids := accountsWith(t, "a", "b", "c")
	a, b, c := ids[0], ids[1], ids[2]
	rec := &fakeRecorder{fail: 1 << 20}
	l := newLinkedLobby3(t, [3]string{a, b, c}, func(cfg *Config) { cfg.Recorder = rec; cfg.Accounts = mem })
	l.startRound(func(s *game.Settings) {
		s.Categories = []game.CategoryRef{{ID: "nombre"}, {ID: "pais"}, {Custom: "Cosa rara"}}
	})
	l.fillAnswers()
	l.send(0, game.CmdBasta, nil)
	l.e.advance(game.GraceDuration + game.LateUpdateAllowance)
	l.send(0, game.CmdFinishReview, nil)
	l.send(0, game.CmdNextRound, nil)
	eventually(t, "the game in the outbox", func() bool { return len(pendingIDs(l.e, l.code)) == 1 })

	// The server stops: its final snapshot names all three accounts. While it is
	// down b deletes their account (nobody can scrub the snapshot).
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := l.e.hub.Shutdown(ctx); err != nil {
		t.Fatal(err)
	}
	if text := snapshotText(t, l.e, l.code); !strings.Contains(text, b) {
		t.Fatalf("setup: the final snapshot does not name the account:\n%s", text)
	}
	if err := mem.DeleteUser(ctx, b); err != nil {
		t.Fatal(err)
	}

	rec2 := &fakeRecorder{fail: 1 << 20}
	e2 := startedEnv(t, l.e, func(cfg *Config) { cfg.Recorder = rec2; cfg.Accounts = mem })

	links := queryLinks(e2, l.code)
	if len(links) != 2 || links["p1"] != a || links["p3"] != c || links["p2"] != "" {
		t.Errorf("links after the restore = %v", links)
	}
	if accounts := query(e2, l.code, func(r *room) map[string]string { return r.accounts }); len(accounts) != 2 || accounts["p2"] != "" {
		t.Errorf("report links after the restore = %v", accounts)
	}
	pending := query(e2, l.code, func(r *room) []store.GameRecord { return r.pending })
	if len(pending) != 1 || len(pending[0].Results) != 2 {
		t.Fatalf("outbox after the restore = %+v", pending)
	}
	for _, res := range pending[0].Results {
		if res.UserID == b {
			t.Errorf("the restored outbox credits the deleted account: %+v", res)
		}
	}
	if got := skipped(e2, skipRestore); got != 1 {
		t.Errorf("skipped{restore} = %v, want 1", got)
	}
	// The seat is still there, and the room re-saved without the account.
	e2.waitSaved(l.code)
	if text := snapshotText(t, e2, l.code); strings.Contains(text, b) {
		t.Errorf("the re-saved snapshot still names the deleted account:\n%s", text)
	}
	v := query(e2, l.code, func(r *room) game.View { return r.g.View("p2") })
	if playerView(t, v, "p2").Name != "Beto" || v.Phase != game.PhaseFinal {
		t.Errorf("the seat did not survive: %+v", v.Players)
	}
	// The recorder is only offered what the others earned.
	eventually(t, "the restored outbox to be offered", func() bool { n, _ := rec2.counts(); return n >= 1 })
	for _, r := range attemptsOf(rec2) {
		for _, res := range r.Results {
			if res.UserID == b {
				t.Errorf("the recorder was offered the deleted account: %+v", r)
			}
		}
	}
}

func TestRestoreKeepsLinksWhenTheAccountsStoreCannotAnswer(t *testing.T) {
	mem, ids := accountsWith(t, "a")
	broken := &brokenDirectory{Memory: mem}
	rec := &fakeRecorder{fail: 1 << 20}
	l := newLinkedLobby3(t, [3]string{ids[0], "", ""}, func(cfg *Config) { cfg.Recorder = rec })
	l.e.waitSaved(l.code)

	e2 := restartedEnv(t, l.e, func(cfg *Config) { cfg.Recorder = &fakeRecorder{}; cfg.Accounts = broken })
	if links := queryLinks(e2, l.code); links["p1"] != ids[0] {
		t.Errorf("links = %v: an error of the store is not a deletion", links)
	}
	if !strings.Contains(e2.logs.String(), "could not check whether an account") {
		t.Error("the failed check was not logged")
	}
}

type brokenDirectory struct{ *store.Memory }

func (*brokenDirectory) GetUser(context.Context, string) (store.User, error) {
	return store.User{}, errStoreDown
}

func TestRestoreWithoutADirectoryTrustsTheSnapshot(t *testing.T) {
	rec := &fakeRecorder{fail: 1 << 20}
	l := newLinkedLobby3(t, [3]string{userA, "", ""}, withRecorder(rec))
	l.e.waitSaved(l.code)
	e2 := restartedEnv(t, l.e, withRecorder(&fakeRecorder{}))
	if links := queryLinks(e2, l.code); links["p1"] != userA {
		t.Errorf("links = %v", links)
	}
}

// TestTheRecorderIsTheDefaultDirectory: cmd/server passes the store as the
// Recorder, which is enough for the restore to check accounts.
func TestTheRecorderIsTheDefaultDirectory(t *testing.T) {
	mem, ids := accountsWith(t, "a")
	e := newEnv(t, func(c *Config) { c.Recorder = mem })
	if dir := e.hub.accountDirectory(); dir == nil {
		t.Fatal("a store that is the Recorder is not used as the directory")
	}
	_ = ids
	e2 := newEnv(t, withRecorder(&fakeRecorder{}))
	if e2.hub.accountDirectory() != nil {
		t.Error("a Recorder that keeps no accounts is not a directory")
	}
	explicit := &brokenDirectory{Memory: mem}
	e3 := newEnv(t, func(c *Config) { c.Recorder = mem; c.Accounts = explicit })
	if e3.hub.accountDirectory() != AccountDirectory(explicit) {
		t.Error("Config.Accounts does not win")
	}
}

// ---- the recorder: a result for a deleted account is skipped, not retried ----

func TestTheRecorderSkipsADeletedUserAndRecordsTheOthers(t *testing.T) {
	mem, ids := accountsWith(t, "a", "b")
	e := newEnv(t, func(c *Config) { c.Recorder = mem })
	if err := mem.DeleteUser(context.Background(), ids[1]); err != nil {
		t.Fatal(err)
	}

	rec := store.GameRecord{ID: store.NewID(), PlayedAt: t0, Results: []store.UserResult{
		{UserID: ids[0], Won: true, Points: 30, BestRound: 30, UniqueAnswers: 3},
		{UserID: ids[1], Points: 20, BestRound: 20, UniqueAnswers: 2},
	}}
	outcome := make(chan statsOutcome, 1)
	if !e.hub.recorder.enqueue(statsJob{rec: rec, done: func(o statsOutcome) { outcome <- o }}) {
		t.Fatal("enqueue refused")
	}
	select {
	case o := <-outcome:
		if o != outcomeRecorded {
			t.Fatalf("outcome = %v, want recorded", o)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("the record was never delivered (retrying?)")
	}

	if st, err := mem.UserStats(context.Background(), ids[0]); err != nil || st.GamesPlayed != 1 || st.Wins != 1 {
		t.Errorf("the other user's stats = %+v, %v", st, err)
	}
	if _, err := mem.UserStats(context.Background(), ids[1]); err == nil {
		t.Error("the deleted account came back")
	}
	if got := skipped(e, skipStore); got != 1 {
		t.Errorf("skipped{store} = %v, want 1", got)
	}
	for _, r := range []string{statsError, statsDropped} {
		if got := metricValue(t, e.reg, "tuttifrutti_stats_recorded_total", "result", r); got != 0 {
			t.Errorf("stats_recorded{%s} = %v for a skipped user", r, got)
		}
	}
	if got := okCount(e); got != 1 {
		t.Errorf("stats_recorded{ok} = %v", got)
	}
}

// ---- concurrency ----

// TestDeleteWhileTheJudgeIsAsked: the order of the deletion and the question is
// whatever it is, but whichever came second is consistent with it, and once the
// deletion returned the account is out.
func TestDeleteWhileTheJudgeIsAsked(t *testing.T) {
	for range 20 {
		j := newFakeJudge()
		l := newJudgeReview(t, j, "user-1")

		var wg sync.WaitGroup
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := l.e.hub.ForgetAccount(context.Background(), "user-1"); err != nil {
				t.Error(err)
			}
		}()
		l.send(0, game.CmdAskJudge, game.AskJudge{Category: 0})
		wg.Wait()

		accepted := len(drainKinds(l.c[0], "error:"+protocol.ErrLoginRequired)) == 0
		if n := len(j.calls()); accepted && n > 1 || !accepted && n != 0 {
			// A question accepted before the deletion may or may not have reached the
			// judge yet (it runs on its own goroutine); a refused one never does.
			t.Fatalf("accepted=%v but the judge was called %d times", accepted, n)
		}
		// After the deletion, a question for another category is refused.
		l.send(0, game.CmdAskJudge, game.AskJudge{Category: 1})
		l.expectFrames("ask after the deletion", []string{"error:" + protocol.ErrLoginRequired}, 0)
		if accepted {
			close(j.gate) // let the first question end
		}
	}
}

// drainKinds returns the frames of c whose kind is want.
func drainKinds(c *fakeClient, want string) []frameRec {
	var out []frameRec
	for _, f := range c.drain() {
		if f.kind() == want {
			out = append(out, f)
		}
	}
	return out
}

// TestDeleteWhileTheGameEnds: the account is deleted at a random moment of the
// last round; the others are credited, the deleted account never reappears and
// nothing is retried, dropped or errors.
func TestDeleteWhileTheGameEnds(t *testing.T) {
	for i := range 12 {
		mem, ids := accountsWith(t, "a", "b", "c")
		l := newLinkedLobby3(t, [3]string{ids[0], ids[1], ids[2]}, func(c *Config) { c.Recorder = mem })
		e := l.e
		l.startRound(nil)
		l.fillAnswers()
		l.send(0, game.CmdBasta, nil)
		e.advance(game.GraceDuration + game.LateUpdateAllowance)
		l.send(0, game.CmdFinishReview, nil)

		var wg sync.WaitGroup
		wg.Add(1)
		go func() {
			defer wg.Done()
			// The server's order: the store, then the rooms.
			if err := mem.DeleteUser(context.Background(), ids[1]); err != nil {
				t.Error(err)
			}
			if err := e.hub.ForgetAccount(context.Background(), ids[1]); err != nil {
				t.Error(err)
			}
		}()
		if i%2 == 0 {
			wg.Wait() // before the game ends
		}
		l.send(0, game.CmdNextRound, nil)
		wg.Wait()

		eventually(t, "the game to be recorded", func() bool {
			st, err := mem.UserStats(context.Background(), ids[0])
			return err == nil && st.GamesPlayed == 1
		})
		if st, err := mem.UserStats(context.Background(), ids[2]); err != nil || st.GamesPlayed != 1 {
			t.Errorf("the third player's stats = %+v, %v", st, err)
		}
		if _, err := mem.UserStats(context.Background(), ids[1]); err == nil {
			t.Error("the deleted account has statistics")
		}
		eventually(t, "an empty outbox", func() bool { e.sync(l.code); return len(pendingIDs(e, l.code)) == 0 })
		for _, r := range []string{statsError, statsDropped} {
			if got := metricValue(t, e.reg, "tuttifrutti_stats_recorded_total", "result", r); got != 0 {
				t.Errorf("stats_recorded{%s} = %v", r, got)
			}
		}
		// Whichever side won the race, the snapshot ends without the account.
		e.waitSaved(l.code)
		e.sync(l.code)
		e.waitSaved(l.code)
		if text := snapshotText(t, e, l.code); strings.Contains(text, ids[1]) {
			t.Errorf("iteration %d: the snapshot names the deleted account:\n%s", i, text)
		}
	}
}

// TestPersistedSnapshotWithADeletedUserOnlyHasItsSeat: the snapshot JSON that
// ForgetAccount produces is what decodePersisted reads: a seat, no account.
func TestPersistedSnapshotAfterForgetDecodes(t *testing.T) {
	l, _ := pendingLobby(t, [3]string{userA, userB, userC})
	forget(t, l.e.hub, userB)
	l.e.waitSaved(l.code)
	p, g := l.e.stored(l.code)
	if len(p.Users) != 2 || len(p.Accounts) != 2 || len(p.Pending) != 1 {
		t.Errorf("persisted = users %v accounts %v pending %v", p.Users, p.Accounts, p.Pending)
	}
	if len(g.Players) != 3 {
		t.Errorf("players = %d", len(g.Players))
	}
	raw, _ := json.Marshal(p)
	if strings.Contains(string(raw), userB) {
		t.Errorf("decoded snapshot names the account: %s", raw)
	}
}

// TestForgetAccountInAPublicRoom: public rooms keep no statistics link but do keep
// the report link of every seat, and it goes with the account too.
func TestForgetAccountInAPublicRoom(t *testing.T) {
	e := newEnv(t, withRecorder(&fakeRecorder{}))
	code, err := e.hub.Create(context.Background(), CreateRequest{PlayerID: "p1", Name: "Ana", Avatar: testAvatar, Visibility: visibilityPublic})
	if err != nil {
		t.Fatal(err)
	}
	e.sync(code)
	e.joinAs(code, "p1", "Ana", userA)
	if got := query(e, code, func(r *room) map[string]string { return r.accounts }); got["p1"] != userA {
		t.Fatalf("setup: report links = %v", got)
	}
	if links := queryLinks(e, code); len(links) != 0 {
		t.Fatalf("setup: a public room linked %v for statistics", links)
	}

	forget(t, e.hub, userA)
	if got := query(e, code, func(r *room) map[string]string { return r.accounts }); len(got) != 0 {
		t.Errorf("report links after the deletion = %v", got)
	}
	e.waitSaved(code)
	if text := snapshotText(t, e, code); strings.Contains(text, userA) {
		t.Errorf("the snapshot of a public room still names the account:\n%s", text)
	}
}
