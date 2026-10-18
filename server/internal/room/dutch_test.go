package room

import (
	"context"
	"fmt"
	"slices"
	"testing"

	"github.com/AresiusXP/tutti-frutti/server/internal/game"
	"github.com/AresiusXP/tutti-frutti/server/internal/protocol"
)

// newLobby3In is newLobby3 for a room created in lang.
func newLobby3In(t *testing.T, lang game.Lang) *lobby3 {
	t.Helper()
	e := newEnv(t)
	code, err := e.hub.Create(context.Background(), CreateRequest{PlayerID: "p1", Name: "Ana", Avatar: testAvatar, Lang: lang})
	if err != nil {
		t.Fatalf("Create(%s): %v", lang, err)
	}
	e.sync(code)
	l := &lobby3{e: e, code: code}
	for i, name := range []string{"Ana", "Beto", "Cami"} {
		l.c[i], l.s[i] = e.join(code, fmt.Sprintf("p%d", i+1), name)
	}
	l.drain()
	return l
}

var dutchLabels = []string{"Naam", "Land", "Dier"}

// A Dutch room from creation to the final: the language survives a settings
// update that keeps it, the letters come from the Dutch alphabet, "IJsland" and
// "Ĳsland" score as the same answer, and the room restarts as a Dutch room.
func TestDutchRoomSurvivesARestart(t *testing.T) {
	l := newLobby3In(t, game.LangNL)
	e, code := l.e, l.code
	if info, ok := e.hub.Info(code); !ok || info.Lang != game.LangNL {
		t.Fatalf("Info = %+v, %v", info, ok)
	}
	if got := query(e, code, func(r *room) game.Lang { return r.g.Settings.Lang }); got != game.LangNL {
		t.Fatalf("lobby language = %q", got)
	}

	// The host keeps the language while changing the rest.
	settings := game.DefaultSettings()
	settings.Lang = game.LangNL
	settings.Rounds = 23
	settings.Categories = []game.CategoryRef{{ID: "nombre"}, {ID: "pais"}, {ID: "animal"}}
	l.send(0, game.CmdUpdateSettings, game.UpdateSettings{Settings: settings})
	v := lastState(t, l.c[2].drain()).View
	if v.Settings.Lang != game.LangNL || v.Settings.Rounds != 23 || !slices.Equal(v.CategoryLabels, dutchLabels) {
		t.Fatalf("view after the update: %+v labels %v", v.Settings, v.CategoryLabels)
	}
	// 24 rounds do not fit the 23 playable letters of a Dutch lobby.
	l.drain()
	settings.Rounds = 24
	l.send(0, game.CmdUpdateSettings, game.UpdateSettings{Settings: settings})
	if got := l.c[0].drain(); len(got) != 1 || got[0].T != protocol.TypeError || got[0].errBody(t).Code != game.CodeInvalidSettings {
		t.Fatalf("frames after 24 rounds = %v", kinds(got))
	}
	settings.Rounds = 1
	l.send(0, game.CmdUpdateSettings, game.UpdateSettings{Settings: settings})
	l.drain()

	// Letter I is the 9th of the 23 playable letters: 8 ticks after the start.
	l.send(0, game.CmdStartGame, nil)
	e.advance(8 * game.LetterTickDuration)
	l.send(0, game.CmdStopLetter, nil)
	v = lastState(t, l.c[1].drain()).View
	if v.Phase != game.PhasePlaying || v.Round.Letter != "I" {
		t.Fatalf("phase %s letter %q, want playing with I", v.Phase, v.Round.Letter)
	}
	for i, answers := range [3][3]string{
		{"Ida", "IJsland", "Ijsvogel"},
		{"Ilse", "\u0132sland", "Impala"},
		{"Ivo", "Ysland", "\u0133svogel"},
	} {
		for cat, a := range answers {
			l.send(i, game.CmdSetAnswer, game.SetAnswer{Category: cat, Text: a})
		}
	}
	// "Ysland" is not an I: p3 cannot call basta; p1 can.
	l.drain()
	l.send(2, game.CmdBasta, nil)
	if got := l.c[2].drain(); len(got) != 1 || got[0].T != protocol.TypeError || got[0].errBody(t).Code != game.CodeBastaNotAllowed {
		t.Fatalf("p3's basta: frames = %v", kinds(got))
	}
	l.send(0, game.CmdBasta, nil)
	l.drain()
	e.advance(game.GraceDuration + game.LateUpdateAllowance)
	review := lastState(t, l.c[1].drain()).View
	if review.Phase != game.PhaseReview || review.Round.Result == nil {
		t.Fatalf("phase %s result %v", review.Phase, review.Round.Result)
	}
	for _, want := range []struct {
		player string
		cat    int
		status game.Status
		points int
	}{
		{"p1", 1, game.StatusValid, game.PointsShared},
		{"p2", 1, game.StatusValid, game.PointsShared},
		{"p3", 1, game.StatusWrongLetter, 0},
		{"p1", 2, game.StatusValid, game.PointsShared},
		{"p3", 2, game.StatusValid, game.PointsShared},
		{"p2", 2, game.StatusValid, game.PointsUnique},
	} {
		got := review.Round.Result.Players[want.player].Cells[want.cat]
		if got.Status != want.status || got.Points != want.points {
			t.Errorf("%s category %d = %+v, want %s / %d", want.player, want.cat, got, want.status, want.points)
		}
	}
	l.send(0, game.CmdFinishReview, nil)
	l.send(0, game.CmdNextRound, nil)
	final := lastState(t, l.c[0].drain()).View
	if final.Phase != game.PhaseFinal || final.Settings.Lang != game.LangNL {
		t.Fatalf("phase %s language %q", final.Phase, final.Settings.Lang)
	}
	// p1: 10+5+5, p2: 10+5+10, p3: 10+0+5
	for id, want := range map[string]int{"p1": 20, "p2": 25, "p3": 15} {
		if got := playerView(t, final, id).TotalScore; got != want {
			t.Errorf("%s total = %d, want %d", id, got, want)
		}
	}
	if !slices.Equal(final.Winners, []string{"p2"}) {
		t.Errorf("winners = %v", final.Winners)
	}

	// The process restarts: the room comes back as a Dutch room.
	if err := e.hub.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
	if p, g := e.stored(code); p.Code != code || g.Settings.Lang != game.LangNL {
		t.Fatalf("the snapshot lost the language: %q", g.Settings.Lang)
	}
	e.hub = e.newHubOn()
	if n, err := e.hub.Start(context.Background()); err != nil || n != 1 {
		t.Fatalf("Start = %d, %v", n, err)
	}
	if info, ok := e.hub.Info(code); !ok || info.Lang != game.LangNL || info.Phase != game.PhaseFinal {
		t.Fatalf("Info after the restart = %+v, %v", info, ok)
	}
	c2, _ := e.join(code, "p2", "Beto")
	back := lastState(t, c2.drain()).View
	if back.Settings.Lang != game.LangNL || !slices.Equal(back.CategoryLabels, dutchLabels) {
		t.Errorf("restored view: language %q labels %v", back.Settings.Lang, back.CategoryLabels)
	}
	if c := back.Round.Result.Players["p2"].Cells[1]; c.Raw != "\u0132sland" || c.Answer != "ijsland" {
		t.Errorf("restored result cell = %+v", c)
	}

	// The rematch of a restored room is Dutch too.
	_, s1 := e.join(code, "p1", "Ana")
	e.handle(code, s1, game.CmdRematch, nil)
	rematch := query(e, code, func(r *room) game.Settings { return r.g.Settings })
	if rematch.Lang != game.LangNL || rematch.Rounds != 1 {
		t.Errorf("settings after the rematch = %+v", rematch)
	}
}
