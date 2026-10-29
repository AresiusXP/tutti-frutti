package room

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"testing"
	"time"

	"github.com/AresiusXP/tutti-frutti/server/internal/game"
	"github.com/AresiusXP/tutti-frutti/server/internal/protocol"
	"github.com/AresiusXP/tutti-frutti/server/internal/store"
)

func deterministicBots(c *Config) {
	c.BotsEnabled = true
	c.BotFirstDelayMin, c.BotFirstDelayMax = 20*time.Second, 20*time.Second
	c.BotSecondDelayMin, c.BotSecondDelayMax = 40*time.Second, 40*time.Second
	c.BotRand = func(n int) int { return min(1, n-1) }
}

func botIDs(e *env, code string) []string {
	return query(e, code, func(r *room) []string { return r.automatedSeats() })
}

func wantBots(t *testing.T, e *env, code string, want int) {
	t.Helper()
	if got := len(botIDs(e, code)); got != want {
		t.Fatalf("automated seats = %d, want %d", got, want)
	}
}

func TestBotFillDeadlinesAndEligibility(t *testing.T) {
	e := newEnv(t, deterministicBots)
	private := e.create("private", "Private")
	e.join(private, "private", "Private")
	unclaimed := createPublic(e, "waiting", game.LangEN)
	e.connect(unclaimed, "waiting")
	code := createPublic(e, "host", game.LangEN)
	c, _ := e.join(code, "host", "Host")
	e.advance(20*time.Second - time.Millisecond)
	wantBots(t, e, code, 0)
	e.advance(time.Millisecond)
	wantBots(t, e, code, 1)
	e.advance(20*time.Second - time.Millisecond)
	wantBots(t, e, code, 1)
	e.advance(time.Millisecond)
	wantBots(t, e, code, 2)
	e.advance(time.Minute)
	wantBots(t, e, code, 2)
	wantBots(t, e, private, 0)
	wantBots(t, e, unclaimed, 0)
	if e.hub.Stats().Connections != 2 {
		t.Fatalf("bots added sockets: %+v", e.hub.Stats())
	}
	st := lastState(t, c.drain())
	if len(st.View.Players) != 3 || st.View.HostID != "host" {
		t.Fatalf("filled state = %+v", st.View)
	}
	for _, p := range st.View.Players {
		if !p.Connected {
			t.Fatalf("seat %s is disconnected with a human present", p.ID)
		}
	}
}

func TestBotIdentitiesAvoidExistingNormalizedNames(t *testing.T) {
	for _, hostName := range []string{"Ana", "ÁNA"} {
		t.Run(hostName, func(t *testing.T) {
			e := newEnv(t, deterministicBots, func(c *Config) { c.BotRand = func(int) int { return 0 } })
			code := createPublic(e, "host", game.LangES)
			client, _ := e.join(code, "host", hostName)
			e.advance(40 * time.Second)
			wantBots(t, e, code, 2)
			seen := map[string]bool{}
			for _, p := range lastState(t, client.drain()).View.Players {
				if p.Left {
					continue
				}
				name := game.Normalize(p.Name, game.LangES)
				if seen[name] {
					t.Fatalf("duplicate normalized nickname %q", p.Name)
				}
				seen[name] = true
			}
			if len(seen) != 3 {
				t.Fatalf("distinct nicknames = %d, want 3", len(seen))
			}
		})
	}
}

func TestPublicPopulationMetricsFollowHumanPresenceAndClosure(t *testing.T) {
	for _, closeRoom := range []bool{false, true} {
		t.Run(fmt.Sprint("explicitLeave=", closeRoom), func(t *testing.T) {
			e := newEnv(t, deterministicBots)
			code := createPublic(e, "host", game.LangEN)
			check := func(visible, humans, bots float64) {
				t.Helper()
				for name, want := range map[string]float64{
					"tuttifrutti_public_lobbies":    visible,
					"tuttifrutti_public_humans":     humans,
					"tuttifrutti_automated_players": bots,
				} {
					if got := metricValue(t, e.reg, name); got != want {
						t.Fatalf("%s = %v, want %v", name, got, want)
					}
				}
			}
			check(0, 0, 0)
			_, host := e.join(code, "host", "Host")
			check(1, 1, 0)
			e.advance(20 * time.Second)
			check(1, 1, 1)
			e.advance(20 * time.Second)
			check(1, 1, 2)
			if count, sum := histogramOf(t, e.reg, "tuttifrutti_public_fill_wait_seconds"); count != 2 || sum != 60 {
				t.Fatalf("fill wait = %d observations, %v seconds", count, sum)
			}
			host.Disconnect()
			e.sync(code)
			check(0, 0, 0)
			_, host = e.join(code, "host", "Host")
			check(1, 1, 2)
			if closeRoom {
				r := e.room(code)
				host.Handle(game.CmdLeave, nil)
				syncRoom(r)
			} else {
				if err := e.hub.Shutdown(context.Background()); err != nil {
					t.Fatal(err)
				}
			}
			check(0, 0, 0)
			// Late socket callbacks after either closure must not subtract again.
			host.Disconnect()
			check(0, 0, 0)
		})
	}
}

func TestBotFlagAndSeatCap(t *testing.T) {
	for _, tc := range []struct {
		enabled   bool
		max, want int
	}{{false, 0, 0}, {true, 1, 1}, {true, 99, 2}} {
		t.Run(fmt.Sprintf("enabled=%v/max=%d", tc.enabled, tc.max), func(t *testing.T) {
			e := newEnv(t, deterministicBots, func(c *Config) { c.BotsEnabled, c.MaxBots = tc.enabled, tc.max })
			code := createPublic(e, "host", game.LangEN)
			e.join(code, "host", "Host")
			e.advance(time.Minute)
			wantBots(t, e, code, tc.want)
		})
	}
}

func TestOfflineHumanSeatBoundsFilling(t *testing.T) {
	e := newEnv(t, deterministicBots)
	code := createPublic(e, "host", game.LangEN)
	e.join(code, "host", "Host")
	_, guest := e.join(code, "guest", "Guest")
	guest.Disconnect()
	e.sync(code)
	e.advance(time.Minute)
	wantBots(t, e, code, 1)
	if got := query(e, code, func(r *room) int { return r.humanSeated() }); got != 2 {
		t.Fatalf("human seats = %d", got)
	}
}

func TestHumanJoinRacesBotDeadline(t *testing.T) {
	e := newEnv(t, deterministicBots, func(c *Config) { c.JoinTimeout = time.Minute })
	code := createPublic(e, "host", game.LangEN)
	e.join(code, "host", "Host")
	_, guest := e.connect(code, "guest")
	join, _ := json.Marshal(protocol.JoinRequest{Code: code, Name: "Guest", Avatar: testAvatar})
	done := make(chan struct{})
	go func() { guest.Handle(game.CmdJoin, join); close(done) }()
	e.clock.Set(t0.Add(20 * time.Second))
	<-done
	e.sync(code)
	if query(e, code, func(r *room) bool { return r.humanSeated() != 2 || len(r.automatedSeats()) > 1 || r.botLonely }) {
		t.Fatal("human join raced filling incorrectly")
	}
	e.advance(time.Minute)
	if len(botIDs(e, code)) > 1 {
		t.Fatal("filling continued after human arrival")
	}
}

func TestBotFillResetsAndHumanArrivalsTrim(t *testing.T) {
	e := newEnv(t, deterministicBots)
	code := createPublic(e, "host", game.LangES)
	_, host := e.join(code, "host", "Host")
	e.advance(19 * time.Second)
	host.Disconnect()
	e.sync(code)
	e.advance(30 * time.Second)
	wantBots(t, e, code, 0)
	_, host = e.join(code, "host", "Host")
	e.advance(19 * time.Second)
	wantBots(t, e, code, 0)
	e.advance(time.Second)
	wantBots(t, e, code, 1)
	_, guest := e.join(code, "guest", "Guest")
	e.advance(time.Minute)
	wantBots(t, e, code, 1)
	// An offline human keeps their seat and still counts toward lobby size.
	guest.Disconnect()
	e.sync(code)
	e.join(code, "third", "Third")
	wantBots(t, e, code, 0)
	if got := query(e, code, func(r *room) int { return r.humanSeated() }); got != 3 {
		t.Fatalf("offline human evicted: %d seats", got)
	}
	_ = host
}

func TestRejectedJoinsDoNotDisplaceBotsAndFullAdmissionIsAtomic(t *testing.T) {
	for _, full := range []bool{false, true} {
		t.Run(fmt.Sprint("full=", full), func(t *testing.T) {
			e := newEnv(t, deterministicBots)
			code := createPublic(e, "host", game.LangEN)
			e.join(code, "host", "Host")
			if full {
				for i := range 7 {
					e.join(code, fmt.Sprintf("h%d", i), "Human")
				}
				// Simulate a pre-existing full snapshot (8 humans + 2 bots).
				query(e, code, func(r *room) bool {
					for _, id := range []string{"bot:first", "bot:second"} {
						if _, err := r.g.AddAutomated(e.clock.Now(), game.Player{ID: id, Name: "Alex"}); err != nil {
							panic(err)
						}
					}
					r.publishPublic()
					return true
				})
			} else {
				e.advance(40 * time.Second)
			}
			before := botIDs(e, code)
			bad, _ := e.join(code, "bad", "")
			if got := kinds(bad.drain()); !slices.Contains(got, "error:invalid_name") {
				t.Fatalf("invalid join response = %v", got)
			}
			if !slices.Equal(botIDs(e, code), before) {
				t.Fatal("invalid join displaced bots")
			}
			query(e, code, func(r *room) bool { r.g.Banned = append(r.g.Banned, "banned"); return true })
			banned, _ := e.join(code, "banned", "Banned")
			if got := kinds(banned.drain()); !slices.Contains(got, "error:kicked") {
				t.Fatalf("banned join response = %v", got)
			}
			if !slices.Equal(botIDs(e, code), before) {
				t.Fatal("banned join displaced bots")
			}
			good, _ := e.join(code, "good", "Good")
			st := lastState(t, good.drain())
			want := 1
			if full {
				want = 0
			}
			wantBots(t, e, code, want)
			if st.View.HostID != "host" {
				t.Fatal("human admission changed host")
			}
		})
	}
}

func TestBotSettingsAndKickSuppression(t *testing.T) {
	for _, mode := range []string{"no timer", "unsupported", "kick"} {
		t.Run(mode, func(t *testing.T) {
			e := newEnv(t, deterministicBots)
			code := createPublic(e, "host", game.LangEN)
			_, host := e.join(code, "host", "Host")
			e.advance(40 * time.Second)
			settings := query(e, code, func(r *room) game.Settings { return r.g.Settings.Clean() })
			want := 0
			switch mode {
			case "no timer":
				settings.TimeLimitSeconds = 0
				e.handle(code, host, game.CmdUpdateSettings, game.UpdateSettings{Settings: settings})
			case "unsupported":
				settings.Categories[0] = game.CategoryRef{Custom: "Unusual things"}
				e.handle(code, host, game.CmdUpdateSettings, game.UpdateSettings{Settings: settings})
			case "kick":
				want = 1
				e.handle(code, host, game.CmdKick, game.Kick{PlayerID: botIDs(e, code)[0]})
			}
			e.advance(2 * time.Minute)
			wantBots(t, e, code, want)
			e.waitSaved(code)
			p, g := e.stored(code)
			if p.BotSuppressed != (mode == "kick") || g.Settings.TimeLimitSeconds != settings.TimeLimitSeconds {
				t.Fatalf("suppression/settings not preserved: %+v, %+v", p, g.Settings)
			}
		})
	}
}

func TestBotRoundLifecycleAndRematch(t *testing.T) {
	e := newEnv(t, deterministicBots)
	code := createPublic(e, "host", game.LangEN)
	c, host := e.join(code, "host", "Host")
	e.handle(code, host, game.CmdUpdateSettings, threeCats(func(s *game.Settings) {
		s.Lang, s.Rounds, s.TimeLimitSeconds = game.LangEN, 2, 60
	}))
	e.advance(40 * time.Second)
	ids := botIDs(e, code)
	// A deliberate kick suppresses additions, but the remaining bot plays.
	e.handle(code, host, game.CmdKick, game.Kick{PlayerID: ids[1]})
	e.handle(code, host, game.CmdStartGame, nil)
	e.handle(code, host, game.CmdStopLetter, nil)
	c.drain()
	e.advance(5 * time.Second)
	v := lastState(t, c.drain()).View
	if v.Phase != game.PhasePlaying {
		t.Fatalf("early phase = %s", v.Phase)
	}
	filled := query(e, code, func(r *room) int {
		n := 0
		for _, answer := range r.g.Round.Answers[ids[0]] {
			if answer != "" {
				n++
			}
		}
		return n
	})
	if filled != 1 {
		t.Fatalf("first progressive update filled %d cells", filled)
	}
	e.advance(19 * time.Second)
	if phase := query(e, code, func(r *room) game.Phase { return r.g.Phase }); phase != game.PhasePlaying {
		t.Fatal("bot called stop before 25 seconds")
	}
	e.advance(time.Second)
	if phase := query(e, code, func(r *room) game.Phase { return r.g.Phase }); phase != game.PhaseGrace {
		t.Fatalf("bot did not call stop at 25 seconds: %s", phase)
	}
	e.advance(game.GraceDuration + game.LateUpdateAllowance)
	e.handle(code, host, game.CmdFinishReview, nil)
	e.handle(code, host, game.CmdNextRound, nil)
	if chooser := query(e, code, func(r *room) string { return r.g.ChooserID }); chooser != ids[0] {
		t.Fatalf("round 2 chooser = %s", chooser)
	}
	e.advance(2 * time.Second)
	if phase := query(e, code, func(r *room) game.Phase { return r.g.Phase }); phase != game.PhasePlaying {
		t.Fatalf("automated chooser did not stop alphabet: %s", phase)
	}
	if n := metricValue(t, e.reg, "tuttifrutti_letter_auto_stops_total"); n != 0 {
		t.Fatalf("automated tap counted as timeout: %v", n)
	}
	e.advance(65 * time.Second)
	e.handle(code, host, game.CmdFinishReview, nil)
	e.handle(code, host, game.CmdNextRound, nil)
	if phase := query(e, code, func(r *room) game.Phase { return r.g.Phase }); phase != game.PhaseFinal {
		t.Fatalf("final phase = %s", phase)
	}
	wantBots(t, e, code, 1)
	e.handle(code, host, game.CmdRematch, nil)
	if query(e, code, func(r *room) bool { return r.botSuppressed }) {
		t.Fatal("rematch kept kick suppression")
	}
	e.advance(40 * time.Second)
	wantBots(t, e, code, 2)
}

func TestBotBlanksFiniteRoundAndNoFillAfterStart(t *testing.T) {
	e := newEnv(t, deterministicBots, func(c *Config) { c.BotRand = func(int) int { return 0 } })
	code := createPublic(e, "host", game.LangES)
	_, host := e.join(code, "host", "Host")
	e.advance(20 * time.Second)
	ids := botIDs(e, code)
	e.handle(code, host, game.CmdStartGame, nil)
	e.handle(code, host, game.CmdStopLetter, nil)
	e.advance(91 * time.Second)
	wantBots(t, e, code, 1)
	if query(e, code, func(r *room) bool {
		return r.g.Phase != game.PhaseReview || r.g.Round.CallerID != "" ||
			slices.ContainsFunc(r.g.Round.Answers[ids[0]], func(a string) bool { return a != "" })
	}) {
		t.Fatal("blank bot called stop or finite round did not complete")
	}
}

func TestBotStaleActionsCannotTouchNextRound(t *testing.T) {
	e := newEnv(t, deterministicBots)
	code := createPublic(e, "host", game.LangES)
	_, host := e.join(code, "host", "Host")
	e.advance(20 * time.Second)
	e.handle(code, host, game.CmdStartGame, nil)
	e.handle(code, host, game.CmdStopLetter, nil)
	// The only human can end this round immediately; bots are not voters.
	e.handle(code, host, game.CmdAskStop, nil)
	e.advance(game.GraceDuration + game.LateUpdateAllowance)
	e.handle(code, host, game.CmdFinishReview, nil)
	e.handle(code, host, game.CmdNextRound, nil)
	e.advance(2 * time.Second)
	if query(e, code, func(r *room) bool {
		return r.g.RoundNum != 2 || r.g.Phase != game.PhasePlaying ||
			slices.ContainsFunc(r.g.Round.Answers[r.automatedSeats()[0]], func(a string) bool { return a != "" })
	}) {
		t.Fatal("old round's prepared answer reached the new round")
	}
}

func TestHumanHostSuccessionWithBotSeat(t *testing.T) {
	e := newEnv(t, deterministicBots)
	code := createPublic(e, "host", game.LangES)
	_, host := e.join(code, "host", "Host")
	e.advance(40 * time.Second)
	e.join(code, "guest", "Guest")
	e.handle(code, host, game.CmdLeave, nil)
	if got := query(e, code, func(r *room) string { return r.g.HostID }); got != "guest" {
		t.Fatalf("host = %q, want human guest", got)
	}
}

func TestBotsSuspendAndDoNotKeepRoomAlive(t *testing.T) {
	e := newEnv(t, deterministicBots, func(c *Config) { c.IdleTTL = 20 * time.Second })
	code := createPublic(e, "host", game.LangES)
	_, host := e.join(code, "host", "Host")
	e.advance(20 * time.Second)
	e.handle(code, host, game.CmdStartGame, nil)
	e.handle(code, host, game.CmdStopLetter, nil)
	host.Disconnect()
	e.sync(code)
	lastActive := query(e, code, func(r *room) time.Time { return r.lastActive })
	e.advance(10 * time.Second)
	if query(e, code, func(r *room) bool {
		for _, p := range r.g.Players {
			if r.g.IsAutomated(p.ID) && p.Connected {
				return true
			}
		}
		for _, answer := range r.g.Round.Answers[r.automatedSeats()[0]] {
			if answer != "" {
				return true
			}
		}
		return !r.lastActive.Equal(lastActive) || len(r.botPlans) > 0
	}) {
		t.Fatal("automation remained active without humans")
	}
	e.advance(10 * time.Second)
	if _, ok := e.hub.Info(code); ok {
		t.Fatal("bots prevented idle closure")
	}
}

func TestLastHumanLeaveClosesBotRoom(t *testing.T) {
	e := newEnv(t, deterministicBots)
	code := createPublic(e, "host", game.LangES)
	c, host := e.join(code, "host", "Host")
	e.advance(40 * time.Second)
	r := e.room(code)
	host.Handle(game.CmdLeave, nil)
	syncRoom(r)
	if _, ok := e.hub.Info(code); ok {
		t.Fatal("last human left a bot-only room")
	}
	if !slices.Contains(c.closeCodes(), protocol.CloseLeft) {
		t.Fatalf("leave close = %v", c.closeCodes())
	}
}

func TestRestoredBotsWaitForHumansAndIgnoreAdditionFlag(t *testing.T) {
	e := newEnv(t, deterministicBots)
	code := createPublic(e, "host", game.LangEN)
	_, host := e.join(code, "host", "Host")
	e.advance(40 * time.Second)
	ids := botIDs(e, code)
	e.handle(code, host, game.CmdKick, game.Kick{PlayerID: ids[1]})
	if err := e.hub.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
	h := e.newHubOn(func(c *Config) { deterministicBots(c); c.BotsEnabled = false })
	if n, err := h.Start(context.Background()); err != nil || n != 1 {
		t.Fatalf("restore = %d, %v", n, err)
	}
	e.hub = h
	wantBots(t, e, code, 1)
	if len(h.PublicRooms("")) != 0 {
		t.Fatal("restored room was listed before host reconnected")
	}
	if query(e, code, func(r *room) bool {
		return !r.botSuppressed || r.visibility != visibilityPublic || slices.ContainsFunc(r.g.Players, func(p game.Player) bool { return p.Connected })
	}) {
		t.Fatal("restore lost suppression/visibility or connected a seat")
	}
	e.advance(time.Minute)
	_, host = e.join(code, "host", "Host")
	e.handle(code, host, game.CmdStartGame, nil)
	e.handle(code, host, game.CmdStopLetter, nil)
	e.advance(11 * time.Second)
	if !query(e, code, func(r *room) bool {
		return slices.ContainsFunc(r.g.Round.Answers[ids[0]], func(a string) bool { return a != "" })
	}) {
		t.Fatal("restored bot did not act with additions disabled")
	}
}

func TestRestoredLobbyHonorsLowerBotCap(t *testing.T) {
	for _, started := range []bool{false, true} {
		t.Run(fmt.Sprint("started=", started), func(t *testing.T) {
			e := newEnv(t, deterministicBots)
			code := createPublic(e, "host", game.LangEN)
			_, host := e.join(code, "host", "Host")
			e.advance(40 * time.Second)
			wantBots(t, e, code, 2)
			if started {
				e.handle(code, host, game.CmdStartGame, nil)
			}
			if err := e.hub.Shutdown(context.Background()); err != nil {
				t.Fatal(err)
			}
			h := e.newHubOn(func(c *Config) { deterministicBots(c); c.MaxBots = 1 })
			if n, err := h.Start(context.Background()); err != nil || n != 1 {
				t.Fatalf("restore = %d, %v", n, err)
			}
			e.hub = h
			want := 1
			if started {
				want = 2 // Never remove an opponent from an in-progress game.
			}
			wantBots(t, e, code, want)
			e.join(code, "host", "Host")
			wantBots(t, e, code, want)
		})
	}
}

func TestPublicGamesNeverKeepAccountLinksOrRecords(t *testing.T) {
	for _, automated := range []bool{false, true} {
		t.Run(fmt.Sprint("bots=", automated), func(t *testing.T) {
			recorder := &fakeRecorder{}
			e := newEnv(t, deterministicBots, func(c *Config) { c.Recorder = recorder; c.BotsEnabled = automated })
			code := createPublic(e, "host", game.LangES)
			_, host := e.joinAs(code, "host", "Host", userA)
			if automated {
				e.advance(40 * time.Second)
			} else {
				e.joinAs(code, "guest", "Guest", userB)
			}
			e.handle(code, host, game.CmdUpdateSettings, threeCats(func(s *game.Settings) { s.TimeLimitSeconds = 60 }))
			e.handle(code, host, game.CmdStartGame, nil)
			e.handle(code, host, game.CmdStopLetter, nil)
			e.advance(65 * time.Second)
			e.handle(code, host, game.CmdFinishReview, nil)
			e.handle(code, host, game.CmdNextRound, nil)
			e.waitSaved(code)
			p, g := e.stored(code)
			if g.Phase != game.PhaseFinal || len(p.Users) != 0 || p.GameID != "" || len(p.Pending) != 0 || len(recorder.recorded()) != 0 {
				t.Fatalf("public stats leaked: phase %s, %+v", g.Phase, p)
			}
			// Even a snapshot containing injected old links/outbox records is
			// scrubbed before any restored room can offer them to the recorder.
			p.Users, p.GameID = map[string]string{"host": userA}, gameID
			p.Pending = []store.GameRecord{{ID: gameID, PlayedAt: t0, Results: []store.UserResult{{UserID: userA, Points: 10}}}}
			raw, _ := json.Marshal(p)
			clean, _, err := decodePersisted(store.Snapshot{Code: code, State: raw})
			if err != nil || len(clean.Users) != 0 || clean.GameID != "" || len(clean.Pending) != 0 {
				t.Fatalf("restored public account data = %+v, %v", clean, err)
			}
		})
	}
}
