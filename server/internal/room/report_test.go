package room

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/AresiusXP/tutti-frutti/server/internal/game"
	"github.com/AresiusXP/tutti-frutti/server/internal/judge"
	"github.com/AresiusXP/tutti-frutti/server/internal/safety"
)

func ip(n int) *int { return &n }

func evidenceFor(e *env, code, reporter, kind, instance string, ref safety.Ref) (safety.Evidence, error) {
	e.t.Helper()
	return e.hub.ReportEvidence(context.Background(), code, safety.EvidenceRequest{
		Reporter: reporter, Kind: kind, RoomInstanceID: instance, Ref: ref,
	})
}

// reportLobby is a room with p1 (host, Ana's account), p2 (Beto's account) and
// p3 (a guest) in round 1 (letter A, three categories: Nombre, País, Animal).
func reportLobby(t *testing.T, mods ...func(*Config)) *lobby3 {
	t.Helper()
	e := newEnv(t, mods...)
	l := &lobby3{e: e, code: e.create("p1", "Ana")}
	l.c[0], l.s[0] = e.joinAs(l.code, "p1", "Ana", userAna)
	l.c[1], l.s[1] = e.joinAs(l.code, "p2", "Beto", userBeto)
	l.c[2], l.s[2] = e.join(l.code, "p3", "Cami")
	l.startRound(nil)
	return l
}

func (l *lobby3) toReview() {
	l.e.t.Helper()
	l.fillAnswers()
	l.send(0, game.CmdBasta, nil)
	l.e.advance(game.GraceDuration + game.LateUpdateAllowance)
	if phase := query(l.e, l.code, func(r *room) game.Phase { return r.g.Phase }); phase != game.PhaseReview {
		l.e.t.Fatalf("setup: phase = %s, want review", phase)
	}
	l.drain()
}

func TestAnotherPlayersAnswerIsNeverEvidenceWhileTheRoundIsPlayed(t *testing.T) {
	l := reportLobby(t)
	e := l.e
	l.send(1, game.CmdSetAnswer, game.SetAnswer{Category: 0, Text: "Alberto SECRETO"})
	ref := safety.Ref{PlayerID: "p2", Category: ip(0), Round: ip(1)}

	// Playing: the opponent's answer is hidden from p1 and so from their report.
	ev, err := evidenceFor(e, l.code, "p1", safety.KindAnswer, "", ref)
	if !errors.Is(err, ErrContentUnavailable) {
		t.Fatalf("p1 asking about p2's hidden answer = %v", err)
	}
	if raw, _ := json.Marshal(ev); strings.Contains(string(raw), "SECRETO") {
		t.Fatalf("a hidden answer leaked into %s", raw)
	}
	// The author's own answer is theirs to report.
	if own, err := evidenceFor(e, l.code, "p2", safety.KindAnswer, "", ref); err != nil || own.Data.Text != "Alberto SECRETO" {
		t.Errorf("p2 about their own answer = %+v, %v", own.Data, err)
	}
	// The same in grace: Basta needs a full row, so p3 calls it.
	l.send(2, game.CmdSetAnswer, game.SetAnswer{Category: 0, Text: "Ana"})
	l.send(2, game.CmdSetAnswer, game.SetAnswer{Category: 1, Text: "Alemania"})
	l.send(2, game.CmdSetAnswer, game.SetAnswer{Category: 2, Text: "Avestruz"})
	l.send(2, game.CmdBasta, nil)
	if phase := query(e, l.code, func(r *room) game.Phase { return r.g.Phase }); phase != game.PhaseGrace {
		t.Fatalf("phase = %s, want grace", phase)
	}
	if _, err := evidenceFor(e, l.code, "p1", safety.KindAnswer, "", ref); !errors.Is(err, ErrContentUnavailable) {
		t.Errorf("in grace = %v", err)
	}

	// The review publishes every answer: now the report has its evidence.
	e.advance(game.GraceDuration + game.LateUpdateAllowance)
	ev, err = evidenceFor(e, l.code, "p1", safety.KindAnswer, "", ref)
	if err != nil {
		t.Fatal(err)
	}
	r := query(e, l.code, func(r *room) [2]string { return [2]string{r.instanceID, r.matchID} })
	if !ev.Verified || ev.RoomInstance != r[0] || ev.MatchInstance != r[1] || ev.ContentKey != "answer:r1:c0:p2" {
		t.Errorf("evidence = %+v", ev)
	}
	if ev.TargetGuest != "p2" || ev.TargetUser != userBeto {
		t.Errorf("target = %q / %q: the author and their account come from the room", ev.TargetGuest, ev.TargetUser)
	}
	d := ev.Data
	if d.Source != safety.SourceServer || d.Text != "Alberto SECRETO" || d.Letter != "A" || d.Round != 1 || d.Lang != "es" ||
		d.Category == nil || d.Category.Label != "Nombre" || d.Category.Custom || d.Player == nil || d.Player.Name != "Beto" ||
		d.Author == nil || d.Author.Guest != "p2" || d.Author.User != userBeto {
		t.Errorf("data = %+v", d)
	}
}

func TestEvidenceOfAnEarlierRoundComesFromTheHistory(t *testing.T) {
	l := reportLobby(t)
	l.fillAnswers()
	l.send(0, game.CmdBasta, nil)
	l.e.advance(game.GraceDuration + game.LateUpdateAllowance)
	l.send(0, game.CmdFinishReview, nil)
	l.send(0, game.CmdNextRound, nil) // round 2: letter phase
	ev, err := evidenceFor(l.e, l.code, "p3", safety.KindAnswer, "", safety.Ref{PlayerID: "p2", Category: ip(1), Round: ip(1)})
	if err != nil {
		t.Fatal(err)
	}
	if ev.Data.Text != "Austria" || ev.Data.Letter != "A" || ev.Data.Round != 1 || ev.TargetGuest != "p2" {
		t.Errorf("evidence = %+v", ev.Data)
	}
}

func TestForgedReferencesGetNoEvidence(t *testing.T) {
	l := reportLobby(t)
	l.toReview()
	e := l.e
	tests := []struct {
		name string
		kind string
		ref  safety.Ref
	}{
		{"unknown player", safety.KindPlayer, safety.Ref{PlayerID: "zzz"}},
		{"a player id with another case", safety.KindPlayer, safety.Ref{PlayerID: "P2"}},
		{"answer of a player that is not there", safety.KindAnswer, safety.Ref{PlayerID: "zzz", Category: ip(0), Round: ip(1)}},
		{"a round that does not exist", safety.KindAnswer, safety.Ref{PlayerID: "p2", Category: ip(0), Round: ip(7)}},
		{"a category past the end", safety.KindAnswer, safety.Ref{PlayerID: "p2", Category: ip(3), Round: ip(1)}},
		{"a negative category", safety.KindAnswer, safety.Ref{PlayerID: "p2", Category: ip(-1), Round: ip(1)}},
		{"no category", safety.KindAnswer, safety.Ref{PlayerID: "p2", Round: ip(1)}},
		{"no round", safety.KindAnswer, safety.Ref{PlayerID: "p2", Category: ip(0)}},
		{"a verdict nobody published", safety.KindAIVerdict, safety.Ref{PlayerID: "p2", Category: ip(0), Round: ip(1)}},
		{"a built-in category as a custom label", safety.KindCustomLabel, safety.Ref{Category: ip(0), Label: "Nombre"}},
		{"a label that is not in the room", safety.KindCustomLabel, safety.Ref{Category: ip(0), Label: "Forjada"}},
		{"an unknown kind", "video", safety.Ref{PlayerID: "p2"}},
		{"an empty kind", "", safety.Ref{PlayerID: "p2"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ev, err := evidenceFor(e, l.code, "p1", tc.kind, "", tc.ref)
			if !errors.Is(err, ErrContentUnavailable) {
				t.Errorf("error = %v, want ErrContentUnavailable", err)
			}
			if raw, _ := json.Marshal(ev); strings.Contains(string(raw), "Austria") || ev.Verified || ev.TargetGuest != "" {
				t.Errorf("evidence for a forged ref: %s", raw)
			}
		})
	}
	// The room is fine after all of that.
	if _, err := evidenceFor(e, l.code, "p1", safety.KindPlayer, "", safety.Ref{PlayerID: "p2"}); err != nil {
		t.Errorf("a valid ref after the forged ones = %v", err)
	}
}

func TestOnlyPlayersOfTheRoomGetEvidence(t *testing.T) {
	l := reportLobby(t)
	l.toReview()
	e := l.e
	valid := safety.Ref{PlayerID: "p2", Category: ip(0), Round: ip(1)}
	for _, reporter := range []string{"stranger", "", "P1", "bot:Nube"} {
		for _, kind := range safety.Kinds {
			ev, err := evidenceFor(e, l.code, reporter, kind, "", valid)
			if !errors.Is(err, ErrNotMember) {
				t.Errorf("reporter %q, kind %s: error = %v, want ErrNotMember", reporter, kind, err)
			}
			if ev.Verified || ev.Data.Text != "" || ev.Data.Player != nil {
				t.Errorf("reporter %q learned %+v", reporter, ev.Data)
			}
		}
		// A forged ref is no different from a valid one for a stranger: no oracle.
		if _, err := evidenceFor(e, l.code, reporter, safety.KindAnswer, "", safety.Ref{PlayerID: "nobody", Category: ip(9), Round: ip(9)}); !errors.Is(err, ErrNotMember) {
			t.Errorf("reporter %q with a forged ref: %v", reporter, err)
		}
	}

	// A player who left the game still was one of its players.
	l.send(2, game.CmdLeave, nil)
	if _, err := evidenceFor(e, l.code, "p3", safety.KindPlayer, "", safety.Ref{PlayerID: "p1"}); err != nil {
		t.Errorf("a player who left = %v", err)
	}
}

func TestAKickedPlayerWasAMember(t *testing.T) {
	e := newEnv(t)
	code := e.create("p1", "Ana")
	_, s1 := e.join(code, "p1", "Ana")
	e.join(code, "p2", "Beto")
	e.handle(code, s1, game.CmdKick, game.Kick{PlayerID: "p2"})
	ev, err := evidenceFor(e, code, "p2", safety.KindPlayer, "", safety.Ref{PlayerID: "p1"})
	if err != nil || ev.Data.Player == nil || ev.Data.Player.Name != "Ana" || ev.TargetGuest != "p1" {
		t.Errorf("a kicked player reporting the host = %+v, %v", ev.Data, err)
	}
	if _, err := evidenceFor(e, code, "p2", safety.KindPlayer, "", safety.Ref{PlayerID: "p2"}); !errors.Is(err, ErrContentUnavailable) {
		t.Errorf("the kicked player's own seat is gone: %v", err)
	}
}

func TestInstanceMismatchAndCodeReuse(t *testing.T) {
	e := newEnv(t, func(c *Config) { c.CodeSource = codeSource("BBBB", "BBBB") })
	code := e.create("p1", "Ana")
	c1, s1 := e.join(code, "p1", "Ana")
	old := lastState(t, c1.drain()).RoomInstanceID

	// The instance id the client names is checked against the room.
	if _, err := evidenceFor(e, code, "p1", safety.KindPlayer, old, safety.Ref{PlayerID: "p1"}); err != nil {
		t.Fatalf("with the right instance id: %v", err)
	}
	if _, err := evidenceFor(e, code, "p1", safety.KindPlayer, "11111111-1111-4111-8111-111111111111", safety.Ref{PlayerID: "p1"}); !errors.Is(err, ErrInstanceMismatch) {
		t.Errorf("with another instance id = %v", err)
	}
	if _, err := evidenceFor(e, "ZZZZ", "p1", safety.KindPlayer, "", safety.Ref{PlayerID: "p1"}); !errors.Is(err, ErrRoomNotFound) {
		t.Errorf("an unknown code = %v", err)
	}

	// The room closes (everybody left) and its code is reused by another room.
	e.handle(code, s1, game.CmdLeave, nil)
	eventually(t, "the code to be free", func() bool { return !e.hub.codeInUse(code) })
	again := e.create("p2", "Beto")
	if again != code {
		t.Fatalf("the code was not reused: %s", again)
	}
	c2, _ := e.join(again, "p2", "Beto")
	fresh := lastState(t, c2.drain()).RoomInstanceID
	if fresh == old {
		t.Fatal("a new room has the instance id of the old one")
	}

	// A report about the old room must not be answered from the new one, not
	// even for a reporter that sits in the new room.
	for _, reporter := range []string{"p1", "p2"} {
		ev, err := evidenceFor(e, code, reporter, safety.KindPlayer, old, safety.Ref{PlayerID: "p2"})
		if !errors.Is(err, ErrInstanceMismatch) {
			t.Errorf("reporter %s naming the old instance = %v", reporter, err)
		}
		if ev.Data.Player != nil || ev.Verified {
			t.Errorf("the new room's data answered a report about the old one: %+v", ev.Data)
		}
	}
	if ev, err := evidenceFor(e, code, "p2", safety.KindPlayer, fresh, safety.Ref{PlayerID: "p2"}); err != nil || ev.RoomInstance != fresh {
		t.Errorf("naming the new instance = %+v, %v", ev, err)
	}
}

func TestEvidenceAfterTheHubStopped(t *testing.T) {
	e := newEnv(t)
	code := e.create("p1", "Ana")
	e.join(code, "p1", "Ana")
	if err := e.hub.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := evidenceFor(e, code, "p1", safety.KindPlayer, "", safety.Ref{PlayerID: "p1"}); !errors.Is(err, ErrShuttingDown) {
		t.Errorf("during the shutdown = %v", err)
	}
}

func TestCustomLabelEvidence(t *testing.T) {
	e := newEnv(t)
	code := e.create("p1", "Ana")
	_, s1 := e.joinAs(code, "p1", "Ana", userAna)
	e.joinAs(code, "p2", "Beto", userBeto)
	e.handle(code, s1, game.CmdUpdateSettings, customSettings("Cosas de  gatos"))
	label := "Cosas de gatos" // the room cleans the label (inner whitespace collapsed)

	ev, err := evidenceFor(e, code, "p2", safety.KindCustomLabel, "", safety.Ref{Category: ip(3), Label: label})
	if err != nil {
		t.Fatal(err)
	}
	if ev.TargetGuest != "p1" || ev.TargetUser != userAna || ev.Data.Text != label || !ev.Data.Category.Custom || ev.Data.Category.Index != 3 || ev.Data.Author.Guest != "p1" {
		t.Errorf("evidence = %+v / %+v", ev, ev.Data)
	}
	if !strings.HasPrefix(ev.ContentKey, "label:3:") {
		t.Errorf("content key = %q", ev.ContentKey)
	}
	// The client's copy is compared, not trusted: a label that is not the one in
	// the room (or another index) gets nothing.
	for name, ref := range map[string]safety.Ref{
		"another text":  {Category: ip(3), Label: "Cosas de perros"},
		"another index": {Category: ip(2), Label: label},
		"out of range":  {Category: ip(8), Label: label},
	} {
		if ev, err := evidenceFor(e, code, "p2", safety.KindCustomLabel, "", ref); !errors.Is(err, ErrContentUnavailable) || ev.Data.Text != "" {
			t.Errorf("%s: %+v, %v", name, ev.Data, err)
		}
	}
	// The host changes the label: the old reference is stale.
	e.handle(code, s1, game.CmdUpdateSettings, customSettings("Otra cosa"))
	if _, err := evidenceFor(e, code, "p2", safety.KindCustomLabel, "", safety.Ref{Category: ip(3), Label: label}); !errors.Is(err, ErrContentUnavailable) {
		t.Errorf("a changed label = %v", err)
	}
}

func TestLegacyLabelEvidenceHasNoAuthor(t *testing.T) {
	e := newEnv(t)
	code := e.create("p1", "Ana")
	_, s1 := e.join(code, "p1", "Ana")
	e.handle(code, s1, game.CmdUpdateSettings, customSettings("Cosas viejas"))
	if err := e.hub.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
	rewrite2(t, e, code, func(m map[string]json.RawMessage) { delete(m, "labelAuthors") })
	e.hub = e.newHubOn()
	if _, err := e.hub.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	ev, err := evidenceFor(e, code, "p1", safety.KindCustomLabel, "", safety.Ref{Category: ip(3), Label: "Cosas viejas"})
	if err != nil {
		t.Fatal(err)
	}
	if ev.TargetGuest != "" || ev.TargetUser != "" || ev.Data.Author != nil || ev.Data.Text != "Cosas viejas" {
		t.Errorf("a legacy label: %+v", ev)
	}
}

func TestPlayerEvidenceNamesTheAccountOnlyWhenKnown(t *testing.T) {
	l := reportLobby(t)
	e := l.e
	ev, err := evidenceFor(e, l.code, "p3", safety.KindPlayer, "", safety.Ref{PlayerID: "p2"})
	if err != nil || ev.TargetGuest != "p2" || ev.TargetUser != userBeto || ev.Data.Player.Emoji != testAvatar.Emoji || ev.ContentKey != "player:p2" {
		t.Errorf("signed-in player: %+v, %v", ev, err)
	}
	ev, err = evidenceFor(e, l.code, "p1", safety.KindPlayer, "", safety.Ref{PlayerID: "p3"})
	if err != nil || ev.TargetGuest != "p3" || ev.TargetUser != "" {
		t.Errorf("guest player: %+v, %v", ev, err)
	}
}

func TestAnAIVerdictIsOnlyEvidenceOnceItIsPublished(t *testing.T) {
	j := newFakeJudge()
	l := newJudgeReview(t, j, userAna)
	e := l.e
	ref := safety.Ref{PlayerID: "p1", Category: ip(0), Round: ip(1)}

	l.send(0, game.CmdAskJudge, game.AskJudge{Category: 0})
	<-j.started // the judge is working: nothing is published yet
	if _, err := evidenceFor(e, l.code, "p2", safety.KindAIVerdict, "", ref); !errors.Is(err, ErrContentUnavailable) {
		t.Fatalf("an unpublished verdict = %v", err)
	}
	j.gate <- struct{}{}
	<-j.returned
	l.waitVerdicts(3)

	ev, err := evidenceFor(e, l.code, "p3", safety.KindAIVerdict, "", ref)
	if err != nil {
		t.Fatal(err)
	}
	v := ev.Data.Verdict
	if v == nil || v.Value != game.VerdictValid || v.Reason != "reason of Ana" || v.Answer != "Ana" {
		t.Errorf("verdict = %+v", v)
	}
	if ev.TargetGuest != "" || ev.TargetUser != "" || ev.ContentKey != "verdict:r1:c0:p1" || ev.Data.Category.Label != "Nombre" || ev.Data.Letter != "A" {
		t.Errorf("evidence = %+v / %+v", ev, ev.Data)
	}
	// A verdict of a category nobody asked about does not exist.
	if _, err := evidenceFor(e, l.code, "p3", safety.KindAIVerdict, "", safety.Ref{PlayerID: "p1", Category: ip(1), Round: ip(1)}); !errors.Is(err, ErrContentUnavailable) {
		t.Errorf("a verdict that was not asked for = %v", err)
	}
	// Once the review is over the client no longer shows verdicts: no evidence.
	l.send(0, game.CmdFinishReview, nil)
	if _, err := evidenceFor(e, l.code, "p3", safety.KindAIVerdict, "", ref); !errors.Is(err, ErrContentUnavailable) {
		t.Errorf("a verdict after the review = %v", err)
	}
	_ = judge.Request{}
}

func TestEvidenceKeepsWhatPlayersTypedAndStaysValidJSON(t *testing.T) {
	e := newEnv(t)
	code := e.create("p1", "Ana")
	_, s1 := e.join(code, "p1", "Ana")
	c2, s2 := e.connect(code, "p2")
	nick := `B"</b>"\u0000😀`
	e.handle(code, s2, game.CmdJoin, struct {
		Code   string      `json:"code"`
		Name   string      `json:"name"`
		Avatar game.Avatar `json:"avatar"`
	}{code, nick, testAvatar})
	_ = c2
	e.handle(code, s1, game.CmdUpdateSettings, customSettings(`<b>"x"</b> & 'q' \u0000`))

	for _, ref := range []struct {
		kind string
		ref  safety.Ref
		text string
	}{
		{safety.KindPlayer, safety.Ref{PlayerID: "p2"}, ""},
		{safety.KindCustomLabel, safety.Ref{Category: ip(3), Label: `<b>"x"</b> & 'q' \u0000`}, `<b>"x"</b> & 'q' \u0000`},
	} {
		ev, err := evidenceFor(e, code, "p1", ref.kind, "", ref.ref)
		if err != nil {
			t.Fatalf("%s: %v", ref.kind, err)
		}
		raw, err := ev.JSON()
		if err != nil || !json.Valid(raw) {
			t.Fatalf("%s: evidence is not valid JSON (%v): %s", ref.kind, err, raw)
		}
		var back safety.EvidenceData
		if err := json.Unmarshal(raw, &back); err != nil {
			t.Fatal(err)
		}
		if ref.kind == safety.KindPlayer && back.Player.Name != nick {
			t.Errorf("nickname = %q, want %q (stored as typed, JSON-escaped)", back.Player.Name, nick)
		}
		if ref.text != "" && back.Text != ref.text {
			t.Errorf("label = %q, want %q", back.Text, ref.text)
		}
	}
}
