package store

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/AresiusXP/tutti-frutti/server/internal/api"
	"github.com/AresiusXP/tutti-frutti/server/internal/game"
)

func profile(nick string) api.Profile {
	return api.Profile{Nickname: nick, Avatar: game.Avatar{Emoji: "🍓", Color: "#ff0066"}}
}

func mustUser(t *testing.T, a Accounts, sub, name string) User {
	t.Helper()
	u, err := a.UpsertGoogleUser(t.Context(), sub, name)
	if err != nil {
		t.Fatalf("UpsertGoogleUser(%q): %v", sub, err)
	}
	return u
}

func mustStats(t *testing.T, a Accounts, id string) api.Stats {
	t.Helper()
	s, err := a.UserStats(t.Context(), id)
	if err != nil {
		t.Fatalf("UserStats(%s): %v", id, err)
	}
	return s
}

func mustRecord(t *testing.T, a Accounts, rec GameRecord) {
	t.Helper()
	if err := a.RecordGame(t.Context(), rec); err != nil {
		t.Fatalf("RecordGame(%s): %v", rec.ID, err)
	}
}

// gameID returns the n-th well-formed game id of the tests.
func gameID(n int) string { return fmt.Sprintf("00000000-0000-4000-8000-%012d", n) }

func rec(n int, results ...UserResult) GameRecord {
	return GameRecord{ID: gameID(n), PlayedAt: baseTime, Results: results}
}

func res(u User, won bool, points, best, unique int) UserResult {
	return UserResult{UserID: u.ID, Won: won, Points: points, BestRound: best, UniqueAnswers: unique}
}

// testAccountsContract is the behaviour every Accounts implementation must
// show. newAccounts must return an empty backend that is cleaned up when the
// test ends.
func testAccountsContract(t *testing.T, newAccounts func(t *testing.T) Accounts) {
	t.Helper()

	t.Run("upsert creates a user once per Google subject", func(t *testing.T) {
		a := newAccounts(t)
		first := mustUser(t, a, "sub-1", "Ana")
		if !ValidID(first.ID) {
			t.Errorf("ID = %q, want a lower-case UUID", first.ID)
		}
		if first.Name != "Ana" || first.Profile != nil {
			t.Errorf("new user = %+v, want name Ana and no profile", first)
		}
		if first.CreatedAt.IsZero() || !first.CreatedAt.Equal(first.LastLoginAt) {
			t.Errorf("CreatedAt %v / LastLoginAt %v of a new user must be equal and set", first.CreatedAt, first.LastLoginAt)
		}
		if first.CreatedAt.Location() != time.UTC {
			t.Errorf("CreatedAt location = %v, want UTC", first.CreatedAt.Location())
		}

		again := mustUser(t, a, "sub-1", "Ana")
		other := mustUser(t, a, "sub-2", "Beto")
		if again.ID != first.ID {
			t.Errorf("same subject got id %s, want %s", again.ID, first.ID)
		}
		if other.ID == first.ID {
			t.Error("two subjects share one user id")
		}
	})

	t.Run("login refreshes the name and the login time but keeps the profile", func(t *testing.T) {
		a := newAccounts(t)
		first := mustUser(t, a, "sub-1", "Ana")
		if err := a.SaveProfile(t.Context(), first.ID, profile("Anita")); err != nil {
			t.Fatal(err)
		}
		time.Sleep(5 * time.Millisecond)

		again := mustUser(t, a, "sub-1", "Ana Maria")
		if again.Name != "Ana Maria" {
			t.Errorf("Name = %q, want the new display name", again.Name)
		}
		if !again.LastLoginAt.After(first.LastLoginAt) {
			t.Errorf("LastLoginAt %v did not move past %v", again.LastLoginAt, first.LastLoginAt)
		}
		if !again.CreatedAt.Equal(first.CreatedAt) {
			t.Errorf("CreatedAt changed: %v -> %v", first.CreatedAt, again.CreatedAt)
		}
		if want := profile("Anita"); again.Profile == nil || *again.Profile != want {
			t.Errorf("Profile = %+v, want %+v kept", again.Profile, want)
		}
		got, err := a.GetUser(t.Context(), first.ID)
		if err != nil || got.Name != "Ana Maria" || got.Profile == nil || got.Profile.Nickname != "Anita" {
			t.Errorf("GetUser = %+v, %v", got, err)
		}
	})

	t.Run("upsert rejects data that cannot be stored", func(t *testing.T) {
		a := newAccounts(t)
		cases := []struct{ name, sub, display string }{
			{"empty subject", "", "Ana"},
			{"subject too long", strings.Repeat("1", 256), "Ana"},
			{"NUL in the subject", "su\x00b", "Ana"},
			{"control character in the subject", "su\nb", "Ana"},
			{"subject not UTF-8", "su\xffb", "Ana"},
			{"NUL in the name", "sub", "An\x00a"},
			{"name too long", "sub", strings.Repeat("ñ", 65)},
			{"name not UTF-8", "sub", "An\xffa"},
		}
		for _, tc := range cases {
			if _, err := a.UpsertGoogleUser(t.Context(), tc.sub, tc.display); !errors.Is(err, ErrInvalidAccount) {
				t.Errorf("%s: error = %v, want ErrInvalidAccount", tc.name, err)
			}
		}
		if _, err := a.UpsertGoogleUser(t.Context(), strings.Repeat("1", 255), ""); err != nil {
			t.Errorf("a 255-byte subject and an empty name are valid: %v", err)
		}
		if _, err := a.UpsertGoogleUser(t.Context(), "multibyte", strings.Repeat("ñ", 64)); err != nil {
			t.Errorf("a 64-rune name is valid: %v", err)
		}
	})

	t.Run("unknown users are not found", func(t *testing.T) {
		a := newAccounts(t)
		for _, id := range []string{gameID(1), "", "not-a-uuid", "ABCDEF00-0000-4000-8000-000000000002", "'; DROP TABLE users; --"} {
			if _, err := a.GetUser(t.Context(), id); !errors.Is(err, ErrUserNotFound) {
				t.Errorf("GetUser(%q) = %v, want ErrUserNotFound", id, err)
			}
			if err := a.SaveProfile(t.Context(), id, profile("Ana")); !errors.Is(err, ErrUserNotFound) {
				t.Errorf("SaveProfile(%q) = %v, want ErrUserNotFound", id, err)
			}
			if err := a.DeleteUser(t.Context(), id); !errors.Is(err, ErrUserNotFound) {
				t.Errorf("DeleteUser(%q) = %v, want ErrUserNotFound", id, err)
			}
			if _, err := a.UserStats(t.Context(), id); !errors.Is(err, ErrUserNotFound) {
				t.Errorf("UserStats(%q) = %v, want ErrUserNotFound", id, err)
			}
		}
	})

	t.Run("profile round trip", func(t *testing.T) {
		a := newAccounts(t)
		u := mustUser(t, a, "sub-1", "Ana")
		other := mustUser(t, a, "sub-2", "Beto")
		want := api.Profile{Nickname: "Ñandú 🍉", Avatar: game.Avatar{Emoji: "🦆", Color: "hsl(200 50% 50%)"}}
		if err := a.SaveProfile(t.Context(), u.ID, want); err != nil {
			t.Fatal(err)
		}
		got, err := a.GetUser(t.Context(), u.ID)
		if err != nil {
			t.Fatal(err)
		}
		if got.Profile == nil || *got.Profile != want {
			t.Errorf("Profile = %+v, want %+v", got.Profile, want)
		}
		if o, _ := a.GetUser(t.Context(), other.ID); o.Profile != nil {
			t.Errorf("another user's profile changed: %+v", o.Profile)
		}

		replaced := profile("Anita")
		if err := a.SaveProfile(t.Context(), u.ID, replaced); err != nil {
			t.Fatal(err)
		}
		got, _ = a.GetUser(t.Context(), u.ID)
		if got.Profile == nil || *got.Profile != replaced {
			t.Errorf("Profile after replace = %+v, want %+v", got.Profile, replaced)
		}

		// The returned user does not alias the store.
		got.Profile.Nickname = "mutated"
		again, _ := a.GetUser(t.Context(), u.ID)
		if again.Profile.Nickname != "Anita" {
			t.Error("mutating a returned profile changed the stored one")
		}
	})

	t.Run("profile validation", func(t *testing.T) {
		a := newAccounts(t)
		u := mustUser(t, a, "sub-1", "Ana")
		if err := a.SaveProfile(t.Context(), u.ID, profile("Ana")); err != nil {
			t.Fatal(err)
		}
		bad := map[string]api.Profile{
			"empty nickname": {Nickname: "", Avatar: profile("").Avatar},
			"NUL nickname":   profile("A\x00na"),
			"long nickname":  profile(strings.Repeat("a", 65)),
			"empty emoji":    {Nickname: "Ana", Avatar: game.Avatar{Emoji: "", Color: "#fff"}},
			"empty color":    {Nickname: "Ana", Avatar: game.Avatar{Emoji: "🍓", Color: ""}},
			"long emoji":     {Nickname: "Ana", Avatar: game.Avatar{Emoji: strings.Repeat("a", 33), Color: "#fff"}},
			"NUL color":      {Nickname: "Ana", Avatar: game.Avatar{Emoji: "🍓", Color: "#f\x00f"}},
			"non-UTF-8":      {Nickname: "An\xffa", Avatar: profile("").Avatar},
		}
		for name, p := range bad {
			if err := a.SaveProfile(t.Context(), u.ID, p); !errors.Is(err, ErrInvalidAccount) {
				t.Errorf("%s: error = %v, want ErrInvalidAccount", name, err)
			}
		}
		got, _ := a.GetUser(t.Context(), u.ID)
		if got.Profile == nil || got.Profile.Nickname != "Ana" {
			t.Errorf("a refused profile changed the stored one: %+v", got.Profile)
		}
	})

	t.Run("a user without games has zero stats", func(t *testing.T) {
		a := newAccounts(t)
		u := mustUser(t, a, "sub-1", "Ana")
		if got := mustStats(t, a, u.ID); got != (api.Stats{}) {
			t.Errorf("stats = %+v, want zeros", got)
		}
	})

	t.Run("recorded games add up", func(t *testing.T) {
		a := newAccounts(t)
		ana := mustUser(t, a, "sub-ana", "Ana")
		beto := mustUser(t, a, "sub-beto", "Beto")

		mustRecord(t, a, rec(1, res(ana, true, 50, 30, 3), res(beto, false, 40, 25, 1)))
		mustRecord(t, a, rec(2, res(ana, false, 20, 12, 0), res(beto, true, 60, 35, 4)))
		mustRecord(t, a, rec(3, res(ana, true, 15, 15, 2)))

		wantAna := api.Stats{GamesPlayed: 3, Wins: 2, TotalPoints: 85, BestRound: 30, UniqueAnswers: 5}
		wantBeto := api.Stats{GamesPlayed: 2, Wins: 1, TotalPoints: 100, BestRound: 35, UniqueAnswers: 5}
		if got := mustStats(t, a, ana.ID); got != wantAna {
			t.Errorf("Ana = %+v, want %+v", got, wantAna)
		}
		if got := mustStats(t, a, beto.ID); got != wantBeto {
			t.Errorf("Beto = %+v, want %+v", got, wantBeto)
		}
	})

	t.Run("recording is idempotent per user and game", func(t *testing.T) {
		a := newAccounts(t)
		ana := mustUser(t, a, "sub-ana", "Ana")
		beto := mustUser(t, a, "sub-beto", "Beto")

		first := rec(1, res(ana, true, 50, 30, 3))
		mustRecord(t, a, first)
		mustRecord(t, a, first)
		mustRecord(t, a, first)
		want := api.Stats{GamesPlayed: 1, Wins: 1, TotalPoints: 50, BestRound: 30, UniqueAnswers: 3}
		if got := mustStats(t, a, ana.ID); got != want {
			t.Errorf("after 3 identical records: %+v, want %+v", got, want)
		}

		// The same game again with different numbers for Ana (a bug elsewhere
		// must not corrupt her) and a result for somebody new: only the new
		// pair counts.
		mustRecord(t, a, rec(1, res(ana, false, 999, 999, 99), res(beto, false, 10, 10, 1)))
		if got := mustStats(t, a, ana.ID); got != want {
			t.Errorf("Ana after the game was replayed with other numbers: %+v, want %+v", got, want)
		}
		if got, wantBeto := mustStats(t, a, beto.ID), (api.Stats{GamesPlayed: 1, TotalPoints: 10, BestRound: 10, UniqueAnswers: 1}); got != wantBeto {
			t.Errorf("Beto = %+v, want %+v", got, wantBeto)
		}

		// Another game id is another game.
		mustRecord(t, a, rec(2, res(ana, false, 5, 5, 0)))
		if got := mustStats(t, a, ana.ID); got.GamesPlayed != 2 || got.TotalPoints != 55 {
			t.Errorf("a different game did not count: %+v", got)
		}
	})

	t.Run("a user listed twice counts once, the first entry wins", func(t *testing.T) {
		a := newAccounts(t)
		ana := mustUser(t, a, "sub-ana", "Ana")
		mustRecord(t, a, rec(1, res(ana, true, 50, 30, 3), res(ana, false, 5, 5, 0)))
		want := api.Stats{GamesPlayed: 1, Wins: 1, TotalPoints: 50, BestRound: 30, UniqueAnswers: 3}
		if got := mustStats(t, a, ana.ID); got != want {
			t.Errorf("stats = %+v, want %+v", got, want)
		}
	})

	t.Run("users that no longer exist are skipped", func(t *testing.T) {
		a := newAccounts(t)
		ana := mustUser(t, a, "sub-ana", "Ana")
		gone := mustUser(t, a, "sub-gone", "Gone")
		if err := a.DeleteUser(t.Context(), gone.ID); err != nil {
			t.Fatal(err)
		}
		stranger := User{ID: "11111111-2222-4333-8444-555555555555"}

		mustRecord(t, a, rec(1, res(gone, true, 90, 40, 5), res(stranger, true, 1, 1, 1), res(ana, false, 20, 10, 1)))
		if got, want := mustStats(t, a, ana.ID), (api.Stats{GamesPlayed: 1, TotalPoints: 20, BestRound: 10, UniqueAnswers: 1}); got != want {
			t.Errorf("Ana = %+v, want %+v", got, want)
		}
		if _, err := a.UserStats(t.Context(), gone.ID); !errors.Is(err, ErrUserNotFound) {
			t.Errorf("a deleted user came back: %v", err)
		}
		// Recording must not resurrect the deleted user.
		if _, err := a.GetUser(t.Context(), gone.ID); !errors.Is(err, ErrUserNotFound) {
			t.Errorf("GetUser of a deleted user = %v", err)
		}
	})

	t.Run("RecordGameSkips counts the users whose account is gone", func(t *testing.T) {
		a := newAccounts(t)
		sk, ok := a.(GameSkipper)
		if !ok {
			t.Skip("the backend does not report skipped users")
		}
		ana := mustUser(t, a, "sub-ana", "Ana")
		gone := mustUser(t, a, "sub-gone", "Gone")
		if err := a.DeleteUser(t.Context(), gone.ID); err != nil {
			t.Fatal(err)
		}
		stranger := User{ID: "11111111-2222-4333-8444-555555555555"}

		skipped, err := sk.RecordGameSkips(t.Context(), rec(1, res(gone, true, 90, 40, 5), res(stranger, true, 1, 1, 1), res(ana, false, 20, 10, 1), res(gone, false, 1, 1, 1)))
		if err != nil || skipped != 2 {
			t.Errorf("RecordGameSkips = %d, %v; want 2 (a user listed twice counts once), nil", skipped, err)
		}
		// Replaying is not a skip: the live user was recorded before, and counts nothing.
		if skipped, err := sk.RecordGameSkips(t.Context(), rec(1, res(ana, false, 20, 10, 1))); err != nil || skipped != 0 {
			t.Errorf("replay = %d, %v; want 0, nil", skipped, err)
		}
		if got := mustStats(t, a, ana.ID); got.GamesPlayed != 1 {
			t.Errorf("Ana = %+v, want the game counted once", got)
		}
		if _, err := sk.RecordGameSkips(t.Context(), GameRecord{ID: "nope"}); !errors.Is(err, ErrInvalidGame) {
			t.Errorf("an invalid record = %v, want ErrInvalidGame", err)
		}
	})

	t.Run("invalid records are refused and change nothing", func(t *testing.T) {
		a := newAccounts(t)
		ana := mustUser(t, a, "sub-ana", "Ana")
		ok := res(ana, true, 10, 10, 1)
		bad := map[string]GameRecord{
			"no id":             {Results: []UserResult{ok}},
			"malformed game id": {ID: "game-1", Results: []UserResult{ok}},
			"upper-case id":     {ID: "ABCDEF00-0000-4000-8000-000000000001", Results: []UserResult{ok}},
			"no results":        {ID: gameID(1)},
			"malformed user id": {ID: gameID(1), Results: []UserResult{ok, {UserID: "x", Points: 1}}},
			"negative points":   {ID: gameID(1), Results: []UserResult{ok, {UserID: ana.ID, Points: -1}}},
			"negative best":     {ID: gameID(1), Results: []UserResult{{UserID: ana.ID, BestRound: -1}}},
			"negative unique":   {ID: gameID(1), Results: []UserResult{{UserID: ana.ID, UniqueAnswers: -1}}},
			"absurd points":     {ID: gameID(1), Results: []UserResult{{UserID: ana.ID, Points: 2_000_000}}},
		}
		for name, r := range bad {
			if err := a.RecordGame(t.Context(), r); !errors.Is(err, ErrInvalidGame) {
				t.Errorf("%s: error = %v, want ErrInvalidGame", name, err)
			}
		}
		if got := mustStats(t, a, ana.ID); got != (api.Stats{}) {
			t.Errorf("a refused record changed the stats: %+v", got)
		}
	})

	t.Run("deleting a user removes everything that was theirs", func(t *testing.T) {
		a := newAccounts(t)
		ana := mustUser(t, a, "sub-ana", "Ana")
		beto := mustUser(t, a, "sub-beto", "Beto")
		if err := a.SaveProfile(t.Context(), ana.ID, profile("Anita")); err != nil {
			t.Fatal(err)
		}
		mustRecord(t, a, rec(1, res(ana, true, 50, 30, 3), res(beto, false, 40, 25, 1)))

		if err := a.DeleteUser(t.Context(), ana.ID); err != nil {
			t.Fatal(err)
		}
		if _, err := a.GetUser(t.Context(), ana.ID); !errors.Is(err, ErrUserNotFound) {
			t.Errorf("GetUser after delete = %v", err)
		}
		if err := a.DeleteUser(t.Context(), ana.ID); !errors.Is(err, ErrUserNotFound) {
			t.Errorf("second DeleteUser = %v, want ErrUserNotFound", err)
		}
		if got, want := mustStats(t, a, beto.ID), (api.Stats{GamesPlayed: 1, TotalPoints: 40, BestRound: 25, UniqueAnswers: 1}); got != want {
			t.Errorf("Beto's stats changed: %+v", got)
		}

		// Logging in again with the same Google account starts from scratch:
		// a new user, no profile, no games, and the old game can be recorded
		// for it again as if it were new.
		back := mustUser(t, a, "sub-ana", "Ana")
		if back.ID == ana.ID {
			t.Error("a deleted user's id was reused")
		}
		if back.Profile != nil {
			t.Errorf("the profile survived the deletion: %+v", back.Profile)
		}
		if got := mustStats(t, a, back.ID); got != (api.Stats{}) {
			t.Errorf("the stats survived the deletion: %+v", got)
		}
		mustRecord(t, a, rec(1, res(back, true, 7, 7, 0)))
		if got := mustStats(t, a, back.ID); got.GamesPlayed != 1 || got.TotalPoints != 7 {
			t.Errorf("recording for the re-created user = %+v", got)
		}
	})

	t.Run("concurrent recording is exact", func(t *testing.T) {
		a := newAccounts(t)
		users := make([]User, 4)
		for i := range users {
			users[i] = mustUser(t, a, fmt.Sprintf("sub-%d", i), fmt.Sprintf("U%d", i))
		}
		const games, replays = 12, 3
		var wg sync.WaitGroup
		for g := 1; g <= games; g++ {
			for range replays {
				wg.Go(func() {
					results := []UserResult{
						res(users[0], g%2 == 0, g, g, 1),
						res(users[1], false, 2*g, g, 0),
						res(users[2], true, 3, 3, 0),
						res(users[3], false, 1, 1, 1),
					}
					// Opposite orders: a database that locked the users in
					// the order of the slice would deadlock here.
					if g%2 == 1 {
						slices.Reverse(results)
					}
					if err := a.RecordGame(context.Background(), rec(g, results...)); err != nil {
						t.Errorf("RecordGame(%d): %v", g, err)
					}
				})
			}
		}
		wg.Wait()

		sum := games * (games + 1) / 2
		want := []api.Stats{
			{GamesPlayed: games, Wins: games / 2, TotalPoints: sum, BestRound: games, UniqueAnswers: games},
			{GamesPlayed: games, TotalPoints: 2 * sum, BestRound: games},
			{GamesPlayed: games, Wins: games, TotalPoints: 3 * games, BestRound: 3},
			{GamesPlayed: games, TotalPoints: games, BestRound: 1, UniqueAnswers: games},
		}
		for i, u := range users {
			if got := mustStats(t, a, u.ID); got != want[i] {
				t.Errorf("%s = %+v, want %+v (a replay double counted or a game was lost)", u.Name, got, want[i])
			}
		}
	})

	t.Run("a cancelled context is honoured", func(t *testing.T) {
		a := newAccounts(t)
		u := mustUser(t, a, "sub-1", "Ana")
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		if _, err := a.UpsertGoogleUser(ctx, "sub-1", "Ana"); !errors.Is(err, context.Canceled) {
			t.Errorf("UpsertGoogleUser: %v", err)
		}
		if _, err := a.GetUser(ctx, u.ID); !errors.Is(err, context.Canceled) {
			t.Errorf("GetUser: %v", err)
		}
		if err := a.SaveProfile(ctx, u.ID, profile("Ana")); !errors.Is(err, context.Canceled) {
			t.Errorf("SaveProfile: %v", err)
		}
		if err := a.RecordGame(ctx, rec(1, res(u, true, 1, 1, 1))); !errors.Is(err, context.Canceled) {
			t.Errorf("RecordGame: %v", err)
		}
		if _, err := a.UserStats(ctx, u.ID); !errors.Is(err, context.Canceled) {
			t.Errorf("UserStats: %v", err)
		}
		if err := a.DeleteUser(ctx, u.ID); !errors.Is(err, context.Canceled) {
			t.Errorf("DeleteUser: %v", err)
		}
		if _, err := a.GetUser(t.Context(), u.ID); err != nil {
			t.Errorf("the user must survive a cancelled DeleteUser: %v", err)
		}
	})
}
