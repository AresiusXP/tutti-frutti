package integration

import (
	"net/http"
	"slices"
	"testing"

	"github.com/AresiusXP/tutti-frutti/server/internal/game"
	"github.com/AresiusXP/tutti-frutti/server/internal/store"
)

// The Dutch fixture: letter I, categories Naam, Land and a custom one.
//
//	             Ana           Beto           Cami
//	Naam         Ida     10    Ilse     10    Ivo      10
//	Land         IJsland  5    Ĳsland    5    Ysland    0   (not an I: wrong letter)
//	Ding in tas  Inkt     5    Ijsbijl  10    Inkt      5
//	total                20             25             15      without votes
//
// With Ana and Cami voting Beto's "Ĳsland" out, Ana's IJsland becomes unique:
// Ana 25, Beto 20, Cami 15.
var dutchAnswers = [3][3]string{
	{"Ida", "IJsland", "Inkt"},
	{"Ilse", "\u0132sland", "Ijsbijl"},
	{"Ivo", "Ysland", "Inkt"},
}

var (
	dutchLabels   = []string{"Naam", "Land", "Ding in de tas"}
	dutchUnvoted  = []int{20, 25, 15}
	dutchVoted    = []int{25, 20, 15}
	dutchDefaults = []string{"Naam", "Land", "Dier", "Eten", "Kleur", "Merk"}
)

func dutchSettings() game.Settings {
	s := game.DefaultSettings()
	s.Lang = game.LangNL
	s.Rounds = 2
	s.Categories = []game.CategoryRef{{ID: "nombre"}, {ID: "pais"}, {Custom: "Ding in de tas"}}
	return s
}

// dutchTable creates a Dutch room over REST and seats Ana, Beto and Cami.
func dutchTable(t *testing.T, s *stack) *table {
	t.Helper()
	players := []*player{s.newPlayer("Ana"), s.newPlayer("Beto"), s.newPlayer("Cami")}
	return newTableIn(t, s, "nl", 0, players...)
}

// dutchToReview plays the lobby, the letter I and the answers of dutchAnswers
// up to the review.
func dutchToReview(t *testing.T, tb *table) {
	t.Helper()
	tb.configure(dutchSettings())
	tb.start()
	tb.stopLetter("I")
	for i := range dutchAnswers {
		tb.answers(i, dutchAnswers[i][:]...)
	}
	// "Ysland" is not an I: Cami may not call basta, Ana may.
	tb.wantCmdError(2, game.CodeBastaNotAllowed, game.CmdBasta, nil)
	tb.mustCmd(0, game.CmdBasta, nil)
	tb.waitPhase(game.PhaseGrace)
	tb.s.advance(graceFor + lateFor)
	tb.waitPhase(game.PhaseReview)
}

// TestDutchGame plays a whole game in a Dutch lobby over real sockets: the
// room is created with lang "nl", the listing and the labels are Dutch, the
// settings rules of nl apply, the letter I takes "IJsland" and "Ĳsland" and
// refuses "Ysland", the review scores them as one answer, and the language
// survives the snapshots and the rematch.
func TestDutchGame(t *testing.T) {
	forEachStore(t, func(t *testing.T, open func(*testing.T) store.Store) {
		s := newStack(t, stackOpts{store: open(t)})
		tb := dutchTable(t, s)
		ids := tb.ids()

		// ---- lobby ----
		if info, ok := s.roomInfo(tb.code); !ok || info.Lang != game.LangNL || info.Phase != game.PhaseLobby || info.Players != 3 {
			t.Fatalf("lobby info = %+v (found %v)", info, ok)
		}
		for i := range tb.seats {
			v := tb.view(i)
			if v.Settings.Lang != game.LangNL || !slices.Equal(v.CategoryLabels, dutchDefaults) {
				t.Fatalf("%s lobby view: language %q labels %v", tb.seats[i].p.name, v.Settings.Lang, v.CategoryLabels)
			}
		}
		// The categories the picker lists are the ones the lobby uses.
		var listing struct {
			Lang       string `json:"lang"`
			Categories []struct {
				ID      string `json:"id"`
				Label   string `json:"label"`
				Default bool   `json:"default"`
			} `json:"categories"`
		}
		r := s.do(http.MethodGet, "/api/categories?lang=nl", nil)
		r.jsonBody(t, &listing)
		var defaults []string
		for _, c := range listing.Categories {
			if c.Default {
				defaults = append(defaults, c.Label)
			}
		}
		if r.status != http.StatusOK || listing.Lang != "nl" || len(listing.Categories) != 80 || !slices.Equal(defaults, dutchDefaults) {
			t.Fatalf("GET /api/categories?lang=nl = %d lang %q entries %d defaults %v", r.status, listing.Lang, len(listing.Categories), defaults)
		}

		// ---- settings: the Dutch rules ----
		bad := func(mod func(*game.Settings)) game.UpdateSettings {
			st := dutchSettings()
			mod(&st)
			return game.UpdateSettings{Settings: st}
		}
		for name, update := range map[string]game.UpdateSettings{
			"24 rounds do not fit the 23 letters":      bad(func(st *game.Settings) { st.Rounds = 24 }),
			"custom equal to the English label":        bad(func(st *game.Settings) { st.Categories[2] = game.CategoryRef{Custom: "Country"} }),
			"custom equal to the Spanish label":        bad(func(st *game.Settings) { st.Categories[2] = game.CategoryRef{Custom: "País"} }),
			"custom equal to the Dutch label":          bad(func(st *game.Settings) { st.Categories[2] = game.CategoryRef{Custom: "  LAND "} }),
			"custom equal to a Dutch label not in use": bad(func(st *game.Settings) { st.Categories[2] = game.CategoryRef{Custom: "iets engs"} }),
			"unknown language":                         bad(func(st *game.Settings) { st.Lang = "fr" }),
		} {
			if got := tb.cmd(0, game.CmdUpdateSettings, update); got != game.CodeInvalidSettings {
				t.Fatalf("%s: update_settings = %q, want %q", name, got, game.CodeInvalidSettings)
			}
		}
		tb.configure(dutchSettings())
		if v := tb.view(2); v.Settings.Lang != game.LangNL || !slices.Equal(v.CategoryLabels, dutchLabels) {
			t.Fatalf("settings not applied: %+v labels %v", v.Settings, v.CategoryLabels)
		}
		// 23 rounds is the limit of the Dutch alphabet without its hard letters, 26 with them.
		tb.mustCmd(0, game.CmdUpdateSettings, bad(func(st *game.Settings) { st.Rounds = 23 }))
		tb.wantCmdError(0, game.CodeInvalidSettings, game.CmdUpdateSettings, bad(func(st *game.Settings) { st.Rounds = 26 }))
		tb.mustCmd(0, game.CmdUpdateSettings, bad(func(st *game.Settings) { st.Rounds = 26; st.ExcludeHardLetters = false }))
		tb.configure(dutchSettings())

		// ---- round 1: the letter I ----
		tb.start()
		tb.stopLetter("I")
		if v := tb.view(1); v.Round.Letter != "I" || !slices.Equal(v.UsedLetters, []string{"I"}) {
			t.Fatalf("round view: letter %q used %v", v.Round.Letter, v.UsedLetters)
		}
		for i := range dutchAnswers {
			tb.answers(i, dutchAnswers[i][:]...)
		}
		tb.wantCmdError(2, game.CodeBastaNotAllowed, game.CmdBasta, nil)
		if v := tb.view(0); v.Phase != game.PhasePlaying {
			t.Fatalf("a refused basta moved the room to %s", v.Phase)
		}
		tb.mustCmd(0, game.CmdBasta, nil)
		tb.waitPhase(game.PhaseGrace)
		tb.s.advance(graceFor + lateFor)
		tb.waitPhase(game.PhaseReview)
		tb.assertNoAnswerLeaks()

		v := tb.view(1)
		wantInts(t, "totals in the review", reviewTotals(v, ids...), dutchUnvoted)
		res := v.Round.Result
		for _, w := range []struct {
			player int
			cat    int
			raw    string
			answer string
			status game.Status
			points int
		}{
			{0, 1, "IJsland", "ijsland", game.StatusValid, 5},
			{1, 1, "\u0132sland", "ijsland", game.StatusValid, 5},
			{2, 1, "Ysland", "ysland", game.StatusWrongLetter, 0},
			{1, 2, "Ijsbijl", "ijsbijl", game.StatusValid, 10},
		} {
			c := res.Players[ids[w.player]].Cells[w.cat]
			if c.Raw != w.raw || c.Answer != w.answer || c.Status != w.status || c.Points != w.points {
				t.Errorf("%s category %d = %+v, want %q / %q / %s / %d", tb.seats[w.player].p.name, w.cat, c, w.raw, w.answer, w.status, w.points)
			}
		}

		// Ana and Cami vote Beto's "Ĳsland" out: Ana's IJsland is unique now.
		tb.mustCmd(0, game.CmdVoteReject, game.VoteReject{Category: 1, Target: ids[1]})
		tb.mustCmd(2, game.CmdVoteReject, game.VoteReject{Category: 1, Target: ids[1]})
		tb.waitAll("votes applied", func(v game.View) bool { return reviewTotals(v, ids...)[1] == dutchVoted[1] })
		wantInts(t, "totals after the votes", reviewTotals(tb.view(2), ids...), dutchVoted)

		tb.mustCmd(0, game.CmdFinishReview, nil)
		tb.waitPhase(game.PhaseRoundSummary)
		wantInts(t, "running totals", totalScores(tb.view(1), ids...), dutchVoted)

		// The saved room is a Dutch room.
		s.flush()
		eventually(t, "the Dutch snapshot", func() bool {
			g, ok := snapshot(t, s.store, tb.code)
			return ok && g.Phase == game.PhaseRoundSummary && g.Settings.Lang == game.LangNL
		})

		// ---- round 2 and the end: the letters of the Dutch alphabet ----
		tb.mustCmd(0, game.CmdNextRound, nil)
		tb.waitPhase(game.PhaseLetter)
		tb.stopLetter("A") // chooser: Beto
		if v := tb.view(0); v.Round.Letter != "A" || !slices.Equal(v.UsedLetters, []string{"I", "A"}) {
			t.Fatalf("round 2: letter %q used %v", v.Round.Letter, v.UsedLetters)
		}
		tb.answers(0, "Anna", "Argentini\u00eb", "Appel")
		tb.answers(1, "Aad", "Angola", "Ansicht")
		tb.answers(2, "Anna", "Armeni\u00eb", "Appel")
		tb.mustCmd(0, game.CmdBasta, nil)
		tb.waitPhase(game.PhaseGrace)
		tb.s.advance(graceFor + lateFor)
		tb.waitPhase(game.PhaseReview)
		tb.mustCmd(0, game.CmdFinishReview, nil)
		tb.waitPhase(game.PhaseRoundSummary)
		// Ana 25 + 5+10+5, Beto 20 + 10+10+10, Cami 15 + 5+10+5
		wantInts(t, "running totals after round 2", totalScores(tb.view(1), ids...), []int{45, 50, 35})
		tb.mustCmd(0, game.CmdNextRound, nil) // the 2 rounds are played: the game is over
		tb.waitPhase(game.PhaseFinal)
		if v := tb.view(2); !slices.Equal(v.Winners, []string{ids[1]}) || v.Settings.Lang != game.LangNL {
			t.Fatalf("final: winners %v language %q", v.Winners, v.Settings.Lang)
		}

		// ---- rematch: still Dutch ----
		tb.mustCmd(0, game.CmdRematch, nil)
		tb.waitPhase(game.PhaseLobby)
		for i := range tb.seats {
			v := tb.view(i)
			if v.Settings.Lang != game.LangNL || v.Settings.Rounds != 2 || !slices.Equal(v.CategoryLabels, dutchLabels) {
				t.Fatalf("%s after the rematch: %+v labels %v", tb.seats[i].p.name, v.Settings, v.CategoryLabels)
			}
		}
		if info, ok := s.roomInfo(tb.code); !ok || info.Lang != game.LangNL || info.Phase != game.PhaseLobby {
			t.Fatalf("info after the rematch = %+v (found %v)", info, ok)
		}
	})
}

// TestDutchServerRestart: a Dutch room restored from its snapshot comes back
// as a Dutch room, in the middle of the review, with the ligature answer as
// typed and its votes in place.
func TestDutchServerRestart(t *testing.T) {
	forEachStore(t, func(t *testing.T, open func(*testing.T) store.Store) {
		s := newStack(t, stackOpts{store: open(t)})
		tb := dutchTable(t, s)
		dutchToReview(t, tb)
		ids := tb.ids()
		tb.mustCmd(0, game.CmdVoteReject, game.VoteReject{Category: 1, Target: ids[1]})

		tb.restart(0)
		if info, ok := tb.s.roomInfo(tb.code); !ok || info.Lang != game.LangNL || info.Phase != game.PhaseReview || info.Players != 3 {
			t.Fatalf("restored room info = %+v (found %v)", info, ok)
		}
		first := tb.rejoinAll()
		if first.View.Settings.Lang != game.LangNL || !slices.Equal(first.View.CategoryLabels, dutchLabels) {
			t.Fatalf("first state after the restart: language %q labels %v", first.View.Settings.Lang, first.View.CategoryLabels)
		}
		for _, st := range tb.seats {
			v := st.c.view()
			if v.Phase != game.PhaseReview || v.Round.Letter != "I" || len(v.Round.RejectVotes) != 1 {
				t.Fatalf("%s: review after the restart: phase %s letter %q votes %d", st.p.name, v.Phase, v.Round.Letter, len(v.Round.RejectVotes))
			}
			if c := v.Round.Result.Players[ids[1]].Cells[1]; c.Raw != "\u0132sland" || c.Answer != "ijsland" {
				t.Fatalf("%s: Beto's land after the restart = %+v", st.p.name, c)
			}
		}
		// The second vote the restored room needs; the totals are the voted ones.
		tb.mustCmd(2, game.CmdVoteReject, game.VoteReject{Category: 1, Target: ids[1]})
		tb.waitAll("votes applied", func(v game.View) bool { return reviewTotals(v, ids...)[1] == dutchVoted[1] })
		wantInts(t, "totals after the restart", reviewTotals(tb.view(0), ids...), dutchVoted)
		tb.mustCmd(0, game.CmdFinishReview, nil)
		tb.waitPhase(game.PhaseRoundSummary)
		wantInts(t, "running totals", totalScores(tb.view(1), ids...), dutchVoted)

		// And the restored room can be restarted again from the summary.
		tb.restart(0)
		tb.rejoinAll()
		if v := tb.view(1); v.Settings.Lang != game.LangNL || v.Phase != game.PhaseRoundSummary {
			t.Fatalf("second restart: phase %s language %q", v.Phase, v.Settings.Lang)
		}
		tb.mustCmd(0, game.CmdNextRound, nil)
		tb.waitPhase(game.PhaseLetter)
		tb.stopLetter("A")
	})
}

// TestDutchRoomCreationOverREST: the language of POST /api/rooms.
func TestDutchRoomCreationOverREST(t *testing.T) {
	s := newStack(t, stackOpts{})
	for _, lang := range []string{"es", "en", "nl"} {
		p := s.newPlayer("Ana")
		code := s.createRoom(p, lang)
		if info, ok := s.roomInfo(code); !ok || string(info.Lang) != lang {
			t.Errorf("room created with lang %q: info %+v (found %v)", lang, info, ok)
		}
		c := s.connect(p)
		if st := c.join(code); string(st.View.Settings.Lang) != lang {
			t.Errorf("room created with lang %q: lobby language %q", lang, st.View.Settings.Lang)
		}
	}
	for _, lang := range []string{"fr", "NL", "nl-NL", "dutch"} {
		p := s.newPlayer("Ana")
		_, err := s.tryCreateRoom(p, lang)
		if err == nil {
			t.Errorf("a room was created with lang %q", lang)
		}
	}
	// The default is still Spanish.
	if info, ok := s.roomInfo(s.createRoom(s.newPlayer("Beto"), "")); !ok || info.Lang != game.LangES {
		t.Errorf("default lang: %+v (found %v)", info, ok)
	}
	p := s.newPlayer("Ana")
	b := []byte(`{"name":"Ana","avatar":{"emoji":"🍓","color":"#f06"},"lang":"fr"}`)
	if r := s.do(http.MethodPost, "/api/rooms", b, withCookie(p)); r.status != http.StatusBadRequest || r.errCode(t) != "invalid_lang" {
		t.Errorf("POST /api/rooms lang=fr = %d %s", r.status, r.body)
	}
}
