package integration

import (
	"bytes"
	"encoding/json"
	"slices"
	"testing"
	"time"

	"github.com/AresiusXP/tutti-frutti/server/internal/game"
	"github.com/AresiusXP/tutti-frutti/server/internal/protocol"
)

// ---------------------------------------------------------------------------
// table: a room with several players, each on a real socket.
// ---------------------------------------------------------------------------

type seat struct {
	p *player
	c *client
}

type table struct {
	t       *testing.T
	s       *stack
	code    string
	seats   []*seat // seats[0] is the host
	clients []*client
	// sent records every answer value each player ever sent, by round.
	sent  map[int]map[string][]string
	stage stage
}

// newTable creates a room over REST as the first name and connects and joins
// every player in order.
func newTable(t *testing.T, s *stack, names ...string) *table {
	t.Helper()
	return newTableGap(t, s, 0, names...)
}

// newTableGap is newTable with the clock advanced by gap before each join, so
// that the players' ConnectedAt differ (host election).
func newTableGap(t *testing.T, s *stack, gap time.Duration, names ...string) *table {
	t.Helper()
	players := make([]*player, len(names))
	for i, n := range names {
		players[i] = s.newPlayer(n)
	}
	return newTableOf(t, s, gap, players...)
}

// newTableOf is newTableGap for players that already exist (they may carry an
// account session cookie): the first one hosts.
func newTableOf(t *testing.T, s *stack, gap time.Duration, players ...*player) *table {
	t.Helper()
	return newTableIn(t, s, "", gap, players...)
}

// newTableIn is newTableOf for a room created with the given lobby language
// ("" leaves the default).
func newTableIn(t *testing.T, s *stack, lang string, gap time.Duration, players ...*player) *table {
	t.Helper()
	tb := &table{t: t, s: s, sent: map[int]map[string][]string{}}
	names := make([]string, len(players))
	for i, p := range players {
		tb.seats = append(tb.seats, &seat{p: p})
		names[i] = p.name
	}
	tb.code = s.createRoom(tb.seats[0].p, lang)
	for i := range tb.seats {
		if gap > 0 {
			s.advance(gap)
		}
		tb.reconnect(i)
	}
	tb.waitAll("every player seated and connected", func(v game.View) bool {
		if len(v.Players) != len(names) {
			return false
		}
		for _, p := range v.Players {
			if !p.Connected {
				return false
			}
		}
		return true
	})
	return tb
}

// reconnect opens a new socket for seat i and joins the room with it.
func (tb *table) reconnect(i int) *protocol.State {
	tb.t.Helper()
	c := tb.s.connect(tb.seats[i].p)
	tb.seats[i].c = c
	tb.clients = append(tb.clients, c)
	return c.join(tb.code)
}

// index returns the seat index of a player id.
func (tb *table) index(id string) int {
	for i, st := range tb.seats {
		if st.p.id == id {
			return i
		}
	}
	tb.t.Fatalf("no seat for player %q", id)
	return -1
}

func (tb *table) live() []*client {
	var out []*client
	for _, st := range tb.seats {
		if st.c != nil && !st.c.isClosed() {
			out = append(out, st.c)
		}
	}
	return out
}

// view is the latest view of seat i.
func (tb *table) view(i int) game.View {
	tb.t.Helper()
	return tb.seats[i].c.view()
}

// anyView is the latest view of the first live client.
func (tb *table) anyView() game.View {
	tb.t.Helper()
	live := tb.live()
	if len(live) == 0 {
		tb.t.Fatal("no live client")
	}
	return live[0].view()
}

// waitAll waits until the latest state of every live client satisfies pred.
func (tb *table) waitAll(desc string, pred func(game.View) bool) {
	tb.t.Helper()
	for _, c := range tb.live() {
		c.waitState(desc, func(st *protocol.State) bool { return pred(st.View) })
	}
}

func (tb *table) waitPhase(phase game.Phase) {
	tb.t.Helper()
	tb.waitAll("phase "+string(phase), func(v game.View) bool { return v.Phase == phase })
}

// marks returns the current frame mark of every seat's client (0 for none).
func (tb *table) marks() []int {
	out := make([]int, len(tb.seats))
	for i, st := range tb.seats {
		if st.c != nil {
			out[i] = st.c.mark()
		}
	}
	return out
}

// cmd sends a command as seat i and waits for the room's reply on that
// connection and for the resulting state on every other live connection. It
// returns "" when the command was accepted, else the error code.
func (tb *table) cmd(i int, typ string, payload any) string {
	tb.t.Helper()
	return tb.cmdExcept(i, nil, typ, payload)
}

// cmdExcept is cmd for commands after which the seats in except get no state
// (the kicked player).
func (tb *table) cmdExcept(i int, except []int, typ string, payload any) string {
	tb.t.Helper()
	actor := tb.seats[i].c
	marks := map[*client]int{}
	for j, st := range tb.seats {
		if st.c != nil && !st.c.isClosed() && !slices.Contains(except, j) {
			marks[st.c] = st.c.mark()
		}
	}
	m := actor.mark()
	actor.send(typ, payload)
	f := actor.waitSince(m, "reply to "+typ, func(f frame) bool {
		return f.State != nil || (f.Err != nil && f.Err.Ref == typ)
	})
	if f.Err != nil {
		return f.Err.Code
	}
	for c, mark := range marks {
		if c != actor {
			c.waitSince(mark, "state after "+typ, isState)
		}
	}
	return ""
}

func (tb *table) mustCmd(i int, typ string, payload any) {
	tb.t.Helper()
	if code := tb.cmd(i, typ, payload); code != "" {
		tb.t.Fatalf("%s sent %s: rejected with %q", tb.seats[i].p.name, typ, code)
	}
}

func (tb *table) wantCmdError(i int, want, typ string, payload any) {
	tb.t.Helper()
	if got := tb.cmd(i, typ, payload); got != want {
		tb.t.Fatalf("%s sent %s: error %q, want %q", tb.seats[i].p.name, typ, got, want)
	}
}

// roundSettings are the settings of the fixture games: three categories (two
// from the catalog and a custom one), two rounds, hard letters excluded.
func roundSettings() game.Settings {
	s := game.DefaultSettings()
	s.Rounds = 2
	s.Categories = []game.CategoryRef{{ID: "nombre"}, {ID: "animal"}, {Custom: "Cosa de cocina"}}
	return s
}

func (tb *table) configure(s game.Settings) {
	tb.t.Helper()
	tb.mustCmd(0, game.CmdUpdateSettings, game.UpdateSettings{Settings: s})
	want := s.Labels()
	tb.waitAll("settings applied", func(v game.View) bool { return slices.Equal(v.CategoryLabels, want) })
}

func (tb *table) start() {
	tb.t.Helper()
	tb.mustCmd(0, game.CmdStartGame, nil)
	tb.waitPhase(game.PhaseLetter)
}

// stopLetter makes the chooser stop the alphabet on letter: it advances the
// clock to the middle of that letter's 120 ms slot (the round must have just
// started) and taps STOP.
func (tb *table) stopLetter(letter string) {
	tb.t.Helper()
	v := tb.anyView()
	avail := game.AvailableLetters(v.Settings.Lang, v.Settings.ExcludeHardLetters, v.UsedLetters)
	idx := slices.Index(avail, letter)
	if idx < 0 || time.Duration(idx+1)*letterTick >= chooserFor {
		tb.t.Fatalf("letter %q is not reachable by tapping (available %v)", letter, avail)
	}
	tb.s.advance(time.Duration(idx)*letterTick + letterTick/2)
	tb.mustCmd(tb.index(v.ChooserID), game.CmdStopLetter, nil)
	tb.waitAll("letter "+letter, func(v game.View) bool {
		return v.Phase == game.PhasePlaying && v.Round != nil && v.Round.Letter == letter
	})
}

// answer sends set_answer and records the value for the leak check.
func (tb *table) answer(i, category int, text string) {
	tb.t.Helper()
	round := tb.anyView().RoundNum
	if tb.sent[round] == nil {
		tb.sent[round] = map[string][]string{}
	}
	id := tb.seats[i].p.id
	if text != "" {
		tb.sent[round][id] = append(tb.sent[round][id], text)
	}
	tb.mustCmd(i, game.CmdSetAnswer, game.SetAnswer{Category: category, Text: text})
}

func (tb *table) answers(i int, texts ...string) {
	tb.t.Helper()
	for cat, text := range texts {
		if text != "" {
			tb.answer(i, cat, text)
		}
	}
}

// The timings of docs/DESIGN.md. The tests move the clock with these literals,
// not with the engine's constants, so a change of the product's timings shows
// up as a failing test.
const (
	graceFor   = 3 * time.Second        // basta -> inputs lock
	lateFor    = 500 * time.Millisecond // late updates still accepted after the lock
	chooserFor = 10 * time.Second       // the chooser's time before the auto-stop
	letterTick = 120 * time.Millisecond // slot of each letter of the silent alphabet
)

// ---------------------------------------------------------------------------
// The round-1 fixture (letter M) and the stage ladder.
// ---------------------------------------------------------------------------

// r1Answers: Ana, Beto and Cami in round 1 (categories Nombre, Animal, "Cosa
// de cocina"). Hand-computed points:
//
//	             Ana          Beto            Cami
//	Nombre       Marta   10   Martita   10    Mauro    10
//	Animal       Mono    10   Perro      0    (empty)   0   (wrong letter, empty)
//	Cosa cocina  Mate     5   Mate       5    Manteca  10
//	total                25              15             20      without votes
//
// With the votes of castR1Votes (Ana and Cami reject Beto's Mate; Ana and Beto
// agree that Marta and Martita are the same):
//
//	Nombre       5 / 5 / 10, Animal 10 / 0 / 0, Cosa 10 / 0 / 10
//	total       25              5              20
var r1Answers = [3][3]string{
	{"Marta", "Mono", "Mate"},
	{"Martita", "Perro", "Mate"},
	{"Mauro", "", "Manteca"},
}

var (
	r1Unvoted = []int{25, 15, 20}
	r1Voted   = []int{25, 5, 20}
)

type stage int

const (
	stageCreated stage = iota // everybody joined the lobby
	stageLobby                // settings configured
	stageLetter               // game started
	stagePlaying              // letter M chosen, every answer in
	stageGrace                // Ana called basta
	stageReview               // grace over
	stageVoted                // reject votes and merge cast
	stageSummary              // review finished
)

// advanceTo drives the game of three players (Ana, Beto, Cami) up to a stage.
func (tb *table) advanceTo(target stage) {
	tb.t.Helper()
	for tb.stage < target {
		tb.stage++
		switch tb.stage {
		case stageLobby:
			tb.configure(roundSettings())
		case stageLetter:
			tb.start()
		case stagePlaying:
			tb.stopLetter("M")
			for i := range r1Answers {
				tb.answers(i, r1Answers[i][:]...)
			}
		case stageGrace:
			tb.mustCmd(0, game.CmdBasta, nil)
			tb.waitPhase(game.PhaseGrace)
		case stageReview:
			tb.s.advance(graceFor + lateFor)
			tb.waitPhase(game.PhaseReview)
		case stageVoted:
			tb.castR1Votes()
		case stageSummary:
			tb.mustCmd(0, game.CmdFinishReview, nil)
			tb.waitPhase(game.PhaseRoundSummary)
		}
	}
}

func (tb *table) castR1Votes() {
	tb.t.Helper()
	ana, beto := tb.seats[0].p.id, tb.seats[1].p.id
	tb.mustCmd(0, game.CmdVoteReject, game.VoteReject{Category: 2, Target: beto})
	tb.mustCmd(2, game.CmdVoteReject, game.VoteReject{Category: 2, Target: beto})
	tb.mustCmd(0, game.CmdProposeMerge, game.ProposeMerge{Category: 0, PlayerA: ana, PlayerB: beto})
	tb.mustCmd(2, game.CmdVoteMerge, game.VoteMerge{ProposalID: 1, Yes: false})
	tb.mustCmd(1, game.CmdVoteMerge, game.VoteMerge{ProposalID: 1, Yes: true})
	tb.waitAll("votes applied", func(v game.View) bool { return reviewTotals(v, tb.ids()...)[1] == r1Voted[1] })
}

func (tb *table) ids() []string {
	out := make([]string, len(tb.seats))
	for i, st := range tb.seats {
		out[i] = st.p.id
	}
	return out
}

// reviewTotals are the live round totals of the given players in the review
// (or the round's final totals in the summary).
func reviewTotals(v game.View, ids ...string) []int {
	out := make([]int, len(ids))
	if v.Round == nil || v.Round.Result == nil {
		for i := range out {
			out[i] = -1
		}
		return out
	}
	for i, id := range ids {
		out[i] = v.Round.Result.Players[id].Total
	}
	return out
}

// totalScores are the running totals of the given players.
func totalScores(v game.View, ids ...string) []int {
	out := make([]int, len(ids))
	for i, id := range ids {
		out[i] = -1
		for _, p := range v.Players {
			if p.ID == id {
				out[i] = p.TotalScore
			}
		}
	}
	return out
}

func seatOf(v game.View, id string) (game.Player, bool) {
	for _, p := range v.Players {
		if p.ID == id {
			return p, true
		}
	}
	return game.Player{}, false
}

// ---------------------------------------------------------------------------
// Privacy of the answers.
// ---------------------------------------------------------------------------

// assertNoAnswerLeaks checks every state frame that every client of the table
// ever received while a round was in the playing or grace phase: the raw JSON
// must contain no answer typed by another player (unless the viewer typed the
// same text), and Round.Answers must only hold the viewer's own. It returns
// the number of frames it checked.
func (tb *table) assertNoAnswerLeaks() int {
	tb.t.Helper()
	checked := 0
	for _, c := range tb.clients {
		for _, f := range c.snapshotFrames() {
			if f.State == nil {
				continue
			}
			v := f.State.View
			if (v.Phase != game.PhasePlaying && v.Phase != game.PhaseGrace) || v.Round == nil {
				continue
			}
			checked++
			for id := range v.Round.Answers {
				if id != c.p.id {
					tb.t.Errorf("%s: round %d %s view carries the answers of %s: %s", c.p.name, v.RoundNum, v.Phase, id, f)
				}
			}
			own := tb.sent[v.RoundNum][c.p.id]
			for id, texts := range tb.sent[v.RoundNum] {
				if id == c.p.id {
					continue
				}
				for _, text := range texts {
					if slices.Contains(own, text) {
						continue
					}
					quoted, _ := json.Marshal(text)
					if bytes.Contains(f.Raw, quoted) {
						tb.t.Errorf("%s: %s view leaks %s's answer %s: %s", c.p.name, v.Phase, id, quoted, f)
					}
				}
			}
			if v.Round.Result != nil {
				tb.t.Errorf("%s: %s view already carries a result: %s", c.p.name, v.Phase, f)
			}
		}
	}
	return checked
}

func wantInts(t *testing.T, what string, got, want []int) {
	t.Helper()
	if !slices.Equal(got, want) {
		t.Fatalf("%s = %v, want %v", what, got, want)
	}
}
