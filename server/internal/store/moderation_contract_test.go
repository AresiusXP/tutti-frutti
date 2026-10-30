package store

import (
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/AresiusXP/tutti-frutti/server/internal/safety"
)

func restrictionID(n int) string { return fmt.Sprintf("00000000-0000-4000-8000-%012d", 7000+n) }
func actionID(n int) string      { return fmt.Sprintf("00000000-0000-4000-8000-%012d", 8000+n) }

func sampleRestriction(n int, mods ...func(*safety.Restriction)) safety.Restriction {
	r := safety.Restriction{
		ID: restrictionID(n), GuestID: "guest-target", Reason: "harassing nicknames", Operator: "owner",
		CreatedAt: safetyBase,
	}
	for _, m := range mods {
		m(&r)
	}
	return r
}

func sampleAction(n int, action string, mods ...func(*safety.Action)) safety.Action {
	a := safety.Action{
		ID: actionID(n), At: safetyBase, Operator: "owner", Action: action,
		TargetKind: safety.TargetGuest, TargetID: "guest-target", Details: "note",
	}
	for _, m := range mods {
		m(&a)
	}
	return a
}

// moderationBackend is what the contract needs: both halves of the store and
// the accounts (ClearProfile touches a user).
type moderationBackend interface {
	Safety
	Moderation
	Accounts
}

// testModerationContract is the behaviour every Moderation implementation must
// show. newBackend returns an empty backend cleaned up when the test ends.
func testModerationContract(t *testing.T, newBackend func(t *testing.T) moderationBackend) {
	t.Helper()

	t.Run("blocks: add, list newest first, idempotent, limit, remove", func(t *testing.T) {
		s := newBackend(t)
		ctx := t.Context()
		at := func(i int) time.Time { return safetyBase.Add(time.Duration(i) * time.Minute) }
		b1 := safety.Block{Blocker: "g1", Blocked: "g2", BlockedName: "Ana", CreatedAt: at(1)}
		got, created, err := s.AddBlock(ctx, b1, 3)
		if err != nil || !created || got.Blocked != "g2" || got.BlockedName != "Ana" || !got.CreatedAt.Equal(at(1)) {
			t.Fatalf("AddBlock = %+v, %v, %v", got, created, err)
		}
		// The same pair again changes nothing, whatever the new name and time.
		again, created, err := s.AddBlock(ctx, safety.Block{Blocker: "g1", Blocked: "g2", BlockedName: "Other", CreatedAt: at(9)}, 3)
		if err != nil || created || again != got {
			t.Errorf("second AddBlock = %+v, %v, %v; want the original, created=false", again, created, err)
		}
		for i, blocked := range []string{"g3", "g4"} {
			if _, created, err := s.AddBlock(ctx, safety.Block{Blocker: "g1", Blocked: blocked, CreatedAt: at(2 + i)}, 3); err != nil || !created {
				t.Fatalf("AddBlock(%s) = %v, %v", blocked, created, err)
			}
		}
		if _, _, err := s.AddBlock(ctx, safety.Block{Blocker: "g1", Blocked: "g5", CreatedAt: at(5)}, 3); !errors.Is(err, ErrBlockLimit) {
			t.Errorf("a fourth block = %v, want ErrBlockLimit", err)
		}
		// An existing pair is not a new block: the limit does not apply.
		if _, created, err := s.AddBlock(ctx, b1, 3); err != nil || created {
			t.Errorf("replaying a block at the limit = %v, %v", created, err)
		}
		// The limit is per blocker.
		if _, created, err := s.AddBlock(ctx, safety.Block{Blocker: "g9", Blocked: "g2", CreatedAt: at(6)}, 3); err != nil || !created {
			t.Errorf("another blocker = %v, %v", created, err)
		}

		list, err := s.ListBlocks(ctx, "g1")
		if err != nil || len(list) != 3 || list[0].Blocked != "g4" || list[1].Blocked != "g3" || list[2].Blocked != "g2" {
			t.Errorf("ListBlocks = %+v, %v (want g4, g3, g2: newest first)", list, err)
		}
		if other, _ := s.ListBlocks(ctx, "nobody"); len(other) != 0 {
			t.Errorf("a stranger's list = %+v", other)
		}
		all, err := s.LoadBlocks(ctx)
		if err != nil || len(all) != 4 {
			t.Errorf("LoadBlocks = %d blocks, %v; want 4", len(all), err)
		}

		if ok, err := s.RemoveBlock(ctx, "g1", "g2"); err != nil || !ok {
			t.Errorf("RemoveBlock = %v, %v", ok, err)
		}
		if ok, err := s.RemoveBlock(ctx, "g1", "g2"); err != nil || ok {
			t.Errorf("removing it again = %v, %v; want false, nil (idempotent)", ok, err)
		}
		if _, created, err := s.AddBlock(ctx, safety.Block{Blocker: "g1", Blocked: "g5", CreatedAt: at(7)}, 3); err != nil || !created {
			t.Errorf("after an unblock there is room for another: %v, %v", created, err)
		}
	})

	t.Run("blocks: invalid input is refused", func(t *testing.T) {
		s := newBackend(t)
		for name, b := range map[string]safety.Block{
			"self":        {Blocker: "g1", Blocked: "g1", CreatedAt: safetyBase},
			"bot":         {Blocker: "g1", Blocked: "bot:Ana", CreatedAt: safetyBase},
			"empty":       {Blocker: "g1", CreatedAt: safetyBase},
			"long name":   {Blocker: "g1", Blocked: "g2", BlockedName: string(make([]rune, safety.MaxBlockNameRunes+1)), CreatedAt: safetyBase},
			"NUL name":    {Blocker: "g1", Blocked: "g2", BlockedName: "a\x00b", CreatedAt: safetyBase},
			"no time":     {Blocker: "g1", Blocked: "g2"},
			"bad blocker": {Blocker: "g 1", Blocked: "g2", CreatedAt: safetyBase},
		} {
			if _, _, err := s.AddBlock(t.Context(), b, 10); !errors.Is(err, ErrInvalidBlock) {
				t.Errorf("%s: err = %v, want ErrInvalidBlock", name, err)
			}
		}
	})

	t.Run("blocks: concurrent adds respect the limit", func(t *testing.T) {
		s := newBackend(t)
		const limit, tries = 5, 20
		var wg sync.WaitGroup
		results := make([]error, tries)
		for i := range tries {
			wg.Go(func() {
				_, _, results[i] = s.AddBlock(t.Context(), safety.Block{Blocker: "g1", Blocked: fmt.Sprintf("target-%02d", i), CreatedAt: safetyBase}, limit)
			})
		}
		wg.Wait()
		ok := 0
		for _, err := range results {
			switch {
			case err == nil:
				ok++
			case !errors.Is(err, ErrBlockLimit):
				t.Errorf("unexpected error %v", err)
			}
		}
		if list, _ := s.ListBlocks(t.Context(), "g1"); ok != limit || len(list) != limit {
			t.Errorf("accepted %d blocks, stored %d, limit %d", ok, len(list), limit)
		}
	})

	t.Run("restrictions: create with audit, load the ones in force, revoke", func(t *testing.T) {
		s := newBackend(t)
		ctx := t.Context()
		forever := sampleRestriction(1)
		timed := sampleRestriction(2, func(r *safety.Restriction) {
			r.GuestID, r.UserID, r.ExpiresAt = "guest-2", userID(1), safetyBase.Add(48*time.Hour)
		})
		past := sampleRestriction(3, func(r *safety.Restriction) {
			r.GuestID, r.ExpiresAt = "guest-3", safetyBase.Add(time.Hour)
		})
		for i, r := range []safety.Restriction{forever, timed, past} {
			if err := s.CreateRestriction(ctx, r, sampleAction(i+1, safety.ActionRestrict, func(a *safety.Action) { a.TargetID = r.GuestID })); err != nil {
				t.Fatalf("CreateRestriction(%d): %v", i, err)
			}
		}
		if err := s.CreateRestriction(ctx, forever, sampleAction(9, safety.ActionRestrict)); err == nil {
			t.Error("a duplicate restriction id was stored")
		}
		if acts, _ := s.ListActions(ctx, 10); len(acts) != 3 {
			t.Errorf("audit rows = %d, want 3 (the failed duplicate must leave none)", len(acts))
		}

		now := safetyBase.Add(2 * time.Hour) // `past` has expired
		load, err := s.LoadRestrictions(ctx, now)
		if err != nil || len(load) != 2 || load[0].ID != forever.ID || load[1].ID != timed.ID {
			t.Fatalf("LoadRestrictions = %+v, %v", load, err)
		}
		if got := load[1]; got.UserID != userID(1) || !got.ExpiresAt.Equal(timed.ExpiresAt) || got.Operator != "owner" || got.Reason == "" {
			t.Errorf("restriction round trip = %+v", got)
		}

		revoked, err := s.RevokeRestriction(ctx, forever.ID, now, sampleAction(4, safety.ActionUnrestrict))
		if err != nil || !revoked.RevokedAt.Equal(now) {
			t.Fatalf("RevokeRestriction = %+v, %v", revoked, err)
		}
		if _, err := s.RevokeRestriction(ctx, forever.ID, now, sampleAction(5, safety.ActionUnrestrict)); !errors.Is(err, ErrRestrictionEnded) {
			t.Errorf("revoking twice = %v, want ErrRestrictionEnded", err)
		}
		if _, err := s.RevokeRestriction(ctx, past.ID, now, sampleAction(6, safety.ActionUnrestrict)); !errors.Is(err, ErrRestrictionEnded) {
			t.Errorf("revoking an expired restriction = %v, want ErrRestrictionEnded", err)
		}
		if _, err := s.RevokeRestriction(ctx, restrictionID(99), now, sampleAction(7, safety.ActionUnrestrict)); !errors.Is(err, ErrRestrictionNotFound) {
			t.Errorf("revoking an unknown id = %v, want ErrRestrictionNotFound", err)
		}
		if _, err := s.RevokeRestriction(ctx, "not-a-uuid", now, sampleAction(8, safety.ActionUnrestrict)); !errors.Is(err, ErrRestrictionNotFound) {
			t.Errorf("revoking a malformed id = %v, want ErrRestrictionNotFound", err)
		}
		if got, err := s.GetRestriction(ctx, forever.ID); err != nil || got.RevokedAt.IsZero() {
			t.Errorf("GetRestriction after revoke = %+v, %v", got, err)
		}
		if load, _ := s.LoadRestrictions(ctx, now); len(load) != 1 || load[0].ID != timed.ID {
			t.Errorf("after the revocation: %+v", load)
		}
		if acts, _ := s.ListActions(ctx, 10); len(acts) != 4 {
			t.Errorf("audit rows = %d, want 4 (only the successful revocation is audited)", len(acts))
		}
	})

	t.Run("restrictions: invalid records are refused", func(t *testing.T) {
		s := newBackend(t)
		ctx := t.Context()
		audit := sampleAction(1, safety.ActionRestrict)
		for name, r := range map[string]safety.Restriction{
			"bad id":      sampleRestriction(1, func(r *safety.Restriction) { r.ID = "x" }),
			"no guest":    sampleRestriction(1, func(r *safety.Restriction) { r.GuestID = "" }),
			"bad user":    sampleRestriction(1, func(r *safety.Restriction) { r.UserID = "x" }),
			"no reason":   sampleRestriction(1, func(r *safety.Restriction) { r.Reason = "" }),
			"NUL reason":  sampleRestriction(1, func(r *safety.Restriction) { r.Reason = "a\x00" }),
			"no operator": sampleRestriction(1, func(r *safety.Restriction) { r.Operator = "" }),
			"expires before it starts": sampleRestriction(1, func(r *safety.Restriction) {
				r.ExpiresAt = r.CreatedAt
			}),
		} {
			if err := s.CreateRestriction(ctx, r, audit); !errors.Is(err, ErrInvalidModeration) {
				t.Errorf("%s: err = %v, want ErrInvalidModeration", name, err)
			}
		}
		if err := s.CreateRestriction(ctx, sampleRestriction(1), sampleAction(1, "bogus")); !errors.Is(err, ErrInvalidModeration) {
			t.Errorf("an unknown action = %v", err)
		}
	})

	t.Run("audit: record, list newest first, validate", func(t *testing.T) {
		s := newBackend(t)
		ctx := t.Context()
		for i := range 3 {
			a := sampleAction(i+1, safety.ActionRoomClose, func(a *safety.Action) {
				a.At = safetyBase.Add(time.Duration(i) * time.Minute)
				a.TargetKind, a.TargetID = safety.TargetRoom, fmt.Sprintf("instance-%d", i)
			})
			if err := s.RecordAction(ctx, a); err != nil {
				t.Fatal(err)
			}
		}
		acts, err := s.ListActions(ctx, 2)
		if err != nil || len(acts) != 2 || acts[0].TargetID != "instance-2" || acts[1].TargetID != "instance-1" {
			t.Fatalf("ListActions = %+v, %v", acts, err)
		}
		if got := acts[0]; got.Operator != "owner" || got.Action != safety.ActionRoomClose || got.Details != "note" || !got.At.Equal(safetyBase.Add(2*time.Minute)) {
			t.Errorf("action round trip = %+v", got)
		}
		for name, a := range map[string]safety.Action{
			"no operator": sampleAction(9, safety.ActionRoomClose, func(a *safety.Action) { a.Operator = "" }),
			"bad kind":    sampleAction(9, safety.ActionRoomClose, func(a *safety.Action) { a.TargetKind = "x" }),
			"no target":   sampleAction(9, safety.ActionRoomClose, func(a *safety.Action) { a.TargetID = "" }),
			"bad report":  sampleAction(9, safety.ActionRoomClose, func(a *safety.Action) { a.ReportID = "x" }),
			"NUL details": sampleAction(9, safety.ActionRoomClose, func(a *safety.Action) { a.Details = "a\x00b" }),
		} {
			if err := s.RecordAction(ctx, a); !errors.Is(err, ErrInvalidModeration) {
				t.Errorf("%s: err = %v", name, err)
			}
		}
	})

	t.Run("resolving a report writes the audit record in the same step", func(t *testing.T) {
		s := newBackend(t)
		ctx := t.Context()
		id := mustCreate(t, s, sampleReport(1))
		audit := sampleAction(1, safety.ActionReportResolve, func(a *safety.Action) {
			a.TargetKind, a.TargetID, a.ReportID = safety.TargetReport, id, id
		})
		now := safetyBase.Add(time.Hour)
		if err := s.ResolveReportAudited(ctx, id, safety.StatusActioned, now, audit); err != nil {
			t.Fatal(err)
		}
		got, err := s.GetReport(ctx, id)
		if err != nil || got.Status != safety.StatusActioned || !got.ResolvedAt.Equal(now) || !got.PurgeAt.Equal(now.Add(safety.ResolvedRetention)) {
			t.Errorf("report after resolving = %+v, %v", got, err)
		}
		if acts, _ := s.ListActions(ctx, 10); len(acts) != 1 || acts[0].ReportID != id {
			t.Errorf("audit = %+v", acts)
		}
		if err := s.ResolveReportAudited(ctx, reportID(404), safety.StatusDismissed, now, sampleAction(2, safety.ActionReportResolve)); !errors.Is(err, ErrReportNotFound) {
			t.Errorf("unknown report = %v", err)
		}
		if err := s.ResolveReportAudited(ctx, id, "open", now, sampleAction(3, safety.ActionReportResolve)); !errors.Is(err, ErrInvalidReport) {
			t.Errorf("status open = %v", err)
		}
		if acts, _ := s.ListActions(ctx, 10); len(acts) != 1 {
			t.Errorf("failed resolutions left %d audit rows", len(acts))
		}
	})

	t.Run("listing reports and the stats of the open ones", func(t *testing.T) {
		s := newBackend(t)
		ctx := t.Context()
		if st, err := s.ReportStats(ctx); err != nil || st.Open != 0 || !st.OldestOpen.IsZero() {
			t.Fatalf("empty ReportStats = %+v, %v", st, err)
		}
		at := func(i int) time.Time { return safetyBase.Add(time.Duration(i) * time.Hour) }
		var ids []string
		for i := 1; i <= 4; i++ {
			ids = append(ids, mustCreate(t, s, sampleReport(i, func(r *safety.Report) {
				r.CreatedAt, r.PurgeAt = at(i), safety.PurgeAt(at(i), time.Time{})
			})))
		}
		if err := s.ResolveReport(ctx, ids[0], safety.StatusDismissed, at(10)); err != nil {
			t.Fatal(err)
		}
		if err := s.ResolveReport(ctx, ids[2], safety.StatusActioned, at(10)); err != nil {
			t.Fatal(err)
		}
		st, err := s.ReportStats(ctx)
		if err != nil || st.Open != 2 || !st.OldestOpen.Equal(at(2)) {
			t.Errorf("ReportStats = %+v, %v; want 2 open, oldest at(2)", st, err)
		}
		ids3 := func(rs []safety.Report) []string {
			var out []string
			for _, r := range rs {
				out = append(out, r.ID)
			}
			return out
		}
		open, err := s.ListReports(ctx, safety.StatusOpen, 10)
		if err != nil || fmt.Sprint(ids3(open)) != fmt.Sprint([]string{ids[1], ids[3]}) {
			t.Errorf("open reports = %v, %v (want oldest first: %v)", ids3(open), err, []string{ids[1], ids[3]})
		}
		resolved, _ := s.ListReports(ctx, ReportsResolved, 10)
		if fmt.Sprint(ids3(resolved)) != fmt.Sprint([]string{ids[2], ids[0]}) {
			t.Errorf("resolved reports = %v (newest first)", ids3(resolved))
		}
		all, _ := s.ListReports(ctx, ReportsAll, 3)
		if fmt.Sprint(ids3(all)) != fmt.Sprint([]string{ids[3], ids[2], ids[1]}) {
			t.Errorf("all reports, limit 3 = %v (newest first)", ids3(all))
		}
		if len(all) > 0 && (all[0].Evidence == nil || all[0].Comment == "") {
			t.Errorf("a listed report must be complete: %+v", all[0])
		}
		if _, err := s.ListReports(ctx, "bogus", 10); !errors.Is(err, ErrInvalidReport) {
			t.Errorf("unknown filter = %v", err)
		}
	})

	t.Run("purging terms removes the old acceptances only", func(t *testing.T) {
		s := newBackend(t)
		ctx := t.Context()
		old := safetyBase.Add(-400 * 24 * time.Hour)
		for guest, at := range map[string]time.Time{"old": old, "new": safetyBase} {
			if _, err := s.AcceptTerms(ctx, safety.TermsAcceptance{GuestID: guest, Version: "2026-10", AcceptedAt: at, Locale: "en", Age13Confirmed: true}); err != nil {
				t.Fatal(err)
			}
		}
		n, err := s.PurgeTerms(ctx, safety.AcceptancePurgeBefore(safetyBase))
		if err != nil || n != 1 {
			t.Fatalf("PurgeTerms = %d, %v", n, err)
		}
		if _, ok, _ := s.TermsAcceptance(ctx, "old", "2026-10"); ok {
			t.Error("the old acceptance survived")
		}
		if _, ok, _ := s.TermsAcceptance(ctx, "new", "2026-10"); !ok {
			t.Error("the recent acceptance was purged")
		}
	})

	t.Run("accepting again after a year replaces the stale row", func(t *testing.T) {
		s := newBackend(t)
		ctx := t.Context()
		first := safety.TermsAcceptance{GuestID: "g", Version: "2026-10", AcceptedAt: safetyBase, Locale: "nl", Age13Confirmed: true}
		if _, err := s.AcceptTerms(ctx, first); err != nil {
			t.Fatal(err)
		}
		within := first
		within.AcceptedAt, within.Locale = safetyBase.Add(safety.AcceptanceTTL-time.Hour), "es"
		got, err := s.AcceptTerms(ctx, within)
		if err != nil || got.Locale != "nl" || !got.AcceptedAt.Equal(safetyBase) {
			t.Errorf("within the year the first record is kept: %+v, %v", got, err)
		}
		after := first
		after.AcceptedAt, after.Locale = safetyBase.Add(safety.AcceptanceTTL), "es"
		got, err = s.AcceptTerms(ctx, after)
		if err != nil || got.Locale != "es" || !got.AcceptedAt.Equal(after.AcceptedAt) {
			t.Errorf("after a year the acceptance is replaced: %+v, %v", got, err)
		}
		if stored, ok, _ := s.TermsAcceptance(ctx, "g", "2026-10"); !ok || !stored.AcceptedAt.Equal(after.AcceptedAt) {
			t.Errorf("stored = %+v", stored)
		}
	})

	t.Run("purging moderation: ended restrictions after 90 days, audit after a year", func(t *testing.T) {
		s := newBackend(t)
		ctx := t.Context()
		now := safetyBase.Add(400 * 24 * time.Hour)
		day := 24 * time.Hour
		mk := func(n int, guest string, mod func(*safety.Restriction)) safety.Restriction {
			r := sampleRestriction(n, func(r *safety.Restriction) { r.GuestID, r.CreatedAt = guest, safetyBase })
			mod(&r)
			if err := s.CreateRestriction(ctx, r, sampleAction(100+n, safety.ActionRestrict, func(a *safety.Action) { a.At = now })); err != nil {
				t.Fatal(err)
			}
			return r
		}
		expiredLong := mk(1, "expired-long", func(r *safety.Restriction) { r.ExpiresAt = now.Add(-91 * day) })
		expiredRecent := mk(2, "expired-recent", func(r *safety.Restriction) { r.ExpiresAt = now.Add(-89 * day) })
		active := mk(3, "active", func(*safety.Restriction) {})
		timedActive := mk(4, "timed", func(r *safety.Restriction) { r.ExpiresAt = now.Add(day) })
		revokedLong := mk(5, "revoked-long", func(*safety.Restriction) {})
		revokedRecent := mk(6, "revoked-recent", func(*safety.Restriction) {})
		if _, err := s.RevokeRestriction(ctx, revokedLong.ID, now.Add(-100*day), sampleAction(200, safety.ActionUnrestrict, func(a *safety.Action) { a.At = now })); err != nil {
			t.Fatal(err)
		}
		if _, err := s.RevokeRestriction(ctx, revokedRecent.ID, now.Add(-10*day), sampleAction(201, safety.ActionUnrestrict, func(a *safety.Action) { a.At = now })); err != nil {
			t.Fatal(err)
		}
		// Audit: one record 366 days old, one 364.
		for i, age := range []time.Duration{366 * day, 364 * day} {
			if err := s.RecordAction(ctx, sampleAction(300+i, safety.ActionRoomClose, func(a *safety.Action) {
				a.At, a.TargetKind, a.TargetID = now.Add(-age), safety.TargetRoom, "x"
			})); err != nil {
				t.Fatal(err)
			}
		}

		p, err := s.PurgeModeration(ctx, now)
		if err != nil {
			t.Fatal(err)
		}
		if p.Restrictions != 2 || p.Actions != 1 {
			t.Errorf("PurgeModeration = %+v, want 2 restrictions and 1 audit record", p)
		}
		for _, gone := range []safety.Restriction{expiredLong, revokedLong} {
			if _, err := s.GetRestriction(ctx, gone.ID); !errors.Is(err, ErrRestrictionNotFound) {
				t.Errorf("%s survived its retention: %v", gone.GuestID, err)
			}
		}
		for _, kept := range []safety.Restriction{expiredRecent, active, timedActive, revokedRecent} {
			if _, err := s.GetRestriction(ctx, kept.ID); err != nil {
				t.Errorf("%s was purged: %v", kept.GuestID, err)
			}
		}
		if again, _ := s.PurgeModeration(ctx, now); again != (ModerationPurge{}) {
			t.Errorf("a second purge deleted %+v", again)
		}
	})

	t.Run("clearing a profile removes the text and keeps the statistics", func(t *testing.T) {
		s := newBackend(t)
		ctx := t.Context()
		u, err := s.UpsertGoogleUser(ctx, "sub-1", "Google Name")
		if err != nil {
			t.Fatal(err)
		}
		if err := s.SaveProfile(ctx, u.ID, profile("Nick")); err != nil {
			t.Fatal(err)
		}
		if err := s.RecordGame(ctx, GameRecord{ID: gameID(1), Results: []UserResult{{UserID: u.ID, Won: true, Points: 30, BestRound: 20, UniqueAnswers: 2}}}); err != nil {
			t.Fatal(err)
		}
		if err := s.ClearProfile(ctx, u.ID); err != nil {
			t.Fatal(err)
		}
		got, err := s.GetUser(ctx, u.ID)
		if err != nil || got.Profile != nil || got.Name != "" {
			t.Errorf("user after ClearProfile = %+v, %v", got, err)
		}
		if st, err := s.UserStats(ctx, u.ID); err != nil || st.GamesPlayed != 1 || st.TotalPoints != 30 {
			t.Errorf("stats after ClearProfile = %+v, %v (must stay)", st, err)
		}
		if err := s.ClearProfile(ctx, u.ID); err != nil {
			t.Errorf("clearing twice = %v, want nil (idempotent)", err)
		}
		if err := s.ClearProfile(ctx, userID(404)); !errors.Is(err, ErrUserNotFound) {
			t.Errorf("unknown user = %v", err)
		}
		if err := s.ClearProfile(ctx, "not-a-uuid"); !errors.Is(err, ErrUserNotFound) {
			t.Errorf("malformed user = %v", err)
		}
	})

	t.Run("resolving a report is a compare-and-set: one winner, one audit row, first times kept", func(t *testing.T) {
		s := newBackend(t)
		ctx := t.Context()
		id := mustCreate(t, s, sampleReport(1))
		const tries = 16
		statusOf := func(i int) string {
			if i%2 == 0 {
				return safety.StatusActioned
			}
			return safety.StatusDismissed
		}
		at := func(i int) time.Time { return safetyBase.Add(time.Duration(i+1) * time.Hour) }
		start := make(chan struct{})
		results := make([]error, tries)
		var wg sync.WaitGroup
		for i := range tries {
			wg.Go(func() {
				audit := sampleAction(10+i, safety.ActionReportResolve, func(a *safety.Action) {
					a.TargetKind, a.TargetID, a.ReportID, a.Details = safety.TargetReport, id, id, fmt.Sprint(i)
				})
				<-start
				results[i] = s.ResolveReportAudited(ctx, id, statusOf(i), at(i), audit)
			})
		}
		close(start)
		wg.Wait()
		winner := -1
		for i, err := range results {
			switch {
			case err == nil && winner < 0:
				winner = i
			case err == nil:
				t.Errorf("resolutions %d and %d both succeeded", winner, i)
			case !errors.Is(err, ErrReportNotOpen):
				t.Errorf("resolution %d = %v, want ErrReportNotOpen", i, err)
			}
		}
		if winner < 0 {
			t.Fatal("no resolution succeeded")
		}
		acts, err := s.ListActions(ctx, 100)
		if err != nil || len(acts) != 1 || acts[0].Details != fmt.Sprint(winner) {
			t.Fatalf("audit rows = %+v, %v; want exactly the winner's (%d)", acts, err, winner)
		}
		got, err := s.GetReport(ctx, id)
		if err != nil || got.Status != statusOf(winner) || !got.ResolvedAt.Equal(at(winner)) ||
			!got.PurgeAt.Equal(safety.PurgeAt(got.CreatedAt, at(winner))) {
			t.Fatalf("report = %+v, %v; want the first resolution (%d: %s at %v)", got, err, winner, statusOf(winner), at(winner))
		}

		// Later resolutions, either way, change nothing.
		if err := s.ResolveReport(ctx, id, safety.StatusActioned, at(99)); !errors.Is(err, ErrReportNotOpen) {
			t.Errorf("ResolveReport on a resolved report = %v, want ErrReportNotOpen", err)
		}
		if err := s.ResolveReportAudited(ctx, id, safety.StatusDismissed, at(99), sampleAction(500, safety.ActionReportResolve)); !errors.Is(err, ErrReportNotOpen) {
			t.Errorf("ResolveReportAudited on a resolved report = %v, want ErrReportNotOpen", err)
		}
		if again, _ := s.GetReport(ctx, id); again.Status != got.Status || !again.ResolvedAt.Equal(got.ResolvedAt) || !again.PurgeAt.Equal(got.PurgeAt) {
			t.Errorf("a later resolution changed the report: %+v -> %+v", got, again)
		}
		if acts, _ := s.ListActions(ctx, 100); len(acts) != 1 {
			t.Errorf("a refused resolution left %d audit rows, want 1", len(acts))
		}
		if err := s.ResolveReport(ctx, reportID(404), safety.StatusActioned, at(0)); !errors.Is(err, ErrReportNotFound) {
			t.Errorf("an unknown report = %v, want ErrReportNotFound (not ErrReportNotOpen)", err)
		}
	})

	t.Run("restrictions: at most one in force per guest, under concurrency too", func(t *testing.T) {
		s := newBackend(t)
		ctx := t.Context()
		const tries = 16
		create := func(n int, guest string, mods ...func(*safety.Restriction)) error {
			r := sampleRestriction(n, append([]func(*safety.Restriction){func(r *safety.Restriction) { r.GuestID = guest }}, mods...)...)
			return s.CreateRestriction(ctx, r, sampleAction(n, safety.ActionRestrict, func(a *safety.Action) { a.TargetID = guest }))
		}

		start := make(chan struct{})
		results := make([]error, tries)
		var wg sync.WaitGroup
		for i := range tries {
			wg.Go(func() {
				<-start
				results[i] = create(100+i, "contested")
			})
		}
		close(start)
		wg.Wait()
		won := 0
		for i, err := range results {
			switch {
			case err == nil:
				won++
			case !errors.Is(err, ErrAlreadyRestricted):
				t.Errorf("restrict %d = %v, want ErrAlreadyRestricted", i, err)
			}
		}
		if won != 1 {
			t.Fatalf("%d concurrent restrictions of one guest succeeded, want exactly 1", won)
		}
		load, err := s.LoadRestrictions(ctx, safetyBase)
		if err != nil || len(load) != 1 {
			t.Fatalf("restrictions in force = %+v, %v; want 1", load, err)
		}
		if acts, _ := s.ListActions(ctx, 100); len(acts) != 1 {
			t.Errorf("%d audit rows, want 1: a refused restriction must leave none", len(acts))
		}

		// Another guest is independent; the same guest is free again once the
		// restriction is lifted or has expired.
		if err := create(200, "other"); err != nil {
			t.Errorf("another guest = %v", err)
		}
		if _, err := s.RevokeRestriction(ctx, load[0].ID, safetyBase.Add(time.Minute), sampleAction(201, safety.ActionUnrestrict)); err != nil {
			t.Fatal(err)
		}
		if err := create(202, "contested", func(r *safety.Restriction) {
			r.CreatedAt, r.ExpiresAt = safetyBase.Add(2*time.Minute), safetyBase.Add(time.Hour)
		}); err != nil {
			t.Errorf("a new restriction after the revocation = %v", err)
		}
		if err := create(203, "contested", func(r *safety.Restriction) { r.CreatedAt = safetyBase.Add(30 * time.Minute) }); !errors.Is(err, ErrAlreadyRestricted) {
			t.Errorf("a second one while the timed one is in force = %v, want ErrAlreadyRestricted", err)
		}
		if err := create(204, "contested", func(r *safety.Restriction) { r.CreatedAt = safetyBase.Add(time.Hour) }); err != nil {
			t.Errorf("a new restriction once the timed one has expired = %v", err)
		}
	})
}

func userID(n int) string { return fmt.Sprintf("00000000-0000-4000-8000-%012d", 9000+n) }
