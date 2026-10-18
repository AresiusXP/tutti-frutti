package game

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

type unknownCmd struct{}

func (unknownCmd) Type() string { return "mystery" }

func TestNewGame(t *testing.T) {
	g := NewGame(Player{ID: "h", Name: " \u200b Hosty  \u00ad McHostface ", Avatar: Avatar{Emoji: "🦊", Color: "#fa0"}, TotalScore: 99, Left: true}, t0)
	if g.Phase != PhaseLobby || g.HostID != "h" || g.RoundNum != 0 || g.Round != nil {
		t.Errorf("unexpected initial state: %+v", g)
	}
	if len(g.Players) != 1 {
		t.Fatalf("players = %v", g.Players)
	}
	p := g.Players[0]
	if p.Name != "Hosty McHostface" || !p.Connected || !p.ConnectedAt.Equal(t0) || p.JoinedSeq != 1 || p.JoinedRound != 1 || p.Left || p.TotalScore != 0 {
		t.Errorf("host seat = %+v", p)
	}
	if g.Settings.Rounds != 5 || len(g.Settings.Categories) != 6 {
		t.Errorf("settings = %+v", g.Settings)
	}
	if _, ok := g.NextDeadline(); ok {
		t.Error("the lobby has no deadline")
	}
	if events := g.Tick(t0.Add(time.Hour)); len(events) != 0 {
		t.Errorf("Tick in the lobby produced %v", events)
	}
}

func TestApplyGuards(t *testing.T) {
	g := newLobby(t, 2)
	expectErr(t, g, t0, "p1", nil, ErrUnknownCommand)
	expectErr(t, g, t0, "p1", unknownCmd{}, ErrUnknownCommand)
	expectErr(t, g, t0, "p1", &StartGame{}, ErrUnknownCommand) // commands are passed by value
	expectErr(t, g, t0, "ghost", StartGame{}, ErrUnknownPlayer)
	expectErr(t, g, t0, "", Join{Name: "x"}, ErrUnknownPlayer)
}

func TestJoin(t *testing.T) {
	long := strings.Repeat("ñ", MaxNameLen+1)
	tests := []struct {
		name string
		cmd  Join
		want error
	}{
		{"ok", Join{Name: "Bea", Avatar: Avatar{Emoji: "🐙", Color: "#00f"}}, nil},
		{"name is cleaned", Join{Name: "  Bea   Ríos "}, nil},
		{"max length name", Join{Name: strings.Repeat("ñ", MaxNameLen)}, nil},
		{"empty name", Join{Name: ""}, ErrInvalidName},
		{"only zero width characters", Join{Name: "\u200b\u200c\u200d"}, ErrInvalidName},
		{"only a soft hyphen and spaces", Join{Name: " \u00ad \ufeff "}, ErrInvalidName},
		{"invisible padding does not count against the length limit", Join{Name: strings.Repeat("ñ\u200b", MaxNameLen)}, nil},
		{"too long once the invisible characters are gone", Join{Name: strings.Repeat("\u200bñ", MaxNameLen+1)}, ErrInvalidName},
		{"blank name", Join{Name: "   "}, ErrInvalidName},
		{"name too long", Join{Name: long}, ErrInvalidName},
		{"emoji too long", Join{Name: "Bea", Avatar: Avatar{Emoji: strings.Repeat("x", 33)}}, ErrInvalidAvatar},
		{"colour too long", Join{Name: "Bea", Avatar: Avatar{Color: strings.Repeat("x", 33)}}, ErrInvalidAvatar},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := newLobby(t, 1)
			now := t0.Add(5 * time.Second)
			if tt.want != nil {
				expectErr(t, g, now, "bea", tt.cmd, tt.want)
				return
			}
			apply(t, g, now, "bea", tt.cmd)
			p := playerByID(t, g, "bea")
			if !p.Connected || !p.ConnectedAt.Equal(now) || p.JoinedSeq != 2 || p.JoinedRound != 1 || p.Left || p.TotalScore != 0 {
				t.Errorf("new seat = %+v", p)
			}
			if clean, _ := CleanName(tt.cmd.Name); p.Name != clean {
				t.Errorf("name = %q, want %q", p.Name, clean)
			}
			if g.HostID != "p1" {
				t.Errorf("host changed to %s", g.HostID)
			}
		})
	}
}

func TestJoinStripsInvisibleCharactersFromTheName(t *testing.T) {
	g := newLobby(t, 1)
	apply(t, g, t0.Add(time.Second), "bea", Join{Name: "\u200b Be\u00ada \u2060Ríos\ufeff "})
	if got := playerByID(t, g, "bea").Name; got != "Bea Ríos" {
		t.Errorf("name = %q, want %q", got, "Bea Ríos")
	}
	if got, err := CleanName("\u200b"); err != ErrInvalidName || got != "" {
		t.Errorf("CleanName(zero width space) = %q, %v", got, err)
	}
	if got, err := CleanName("\u200bAna\u00ad"); err != nil || got != "Ana" {
		t.Errorf("CleanName = %q, %v", got, err)
	}
}

func TestJoinCapacity(t *testing.T) {
	g := newLobby(t, MaxPlayers)
	expectErr(t, g, t0.Add(time.Minute), "late", Join{Name: "Late"}, ErrLobbyFull)

	// A seat frees up when somebody leaves.
	apply(t, g, t0.Add(time.Minute), pid(5), Leave{})
	apply(t, g, t0.Add(time.Minute), "late", Join{Name: "Late"})
	if got := len(g.Players); got != MaxPlayers {
		t.Errorf("players = %d, want %d", got, MaxPlayers)
	}
}

// The room layer owns the sockets: a wire-level join from an existing seat
// must never touch Connected or ConnectedAt.
func TestJoinWhileSeatedNeverChangesConnectionState(t *testing.T) {
	t.Run("a disconnected seat stays disconnected", func(t *testing.T) {
		g := newLobby(t, 2)
		setConnected(t, g, t0.Add(10*time.Second), "p2", false)

		apply(t, g, t0.Add(20*time.Second), "p2", Join{Name: "Another name"})
		p := playerByID(t, g, "p2")
		if p.Connected || !p.ConnectedAt.Equal(t0.Add(2*time.Second)) {
			t.Errorf("join changed the connection state: %+v", p)
		}
		if p.Name != "Player 2" || p.JoinedSeq != 2 || len(g.Players) != 2 {
			t.Errorf("join must keep the seat as it was: %+v", g.Players)
		}

		// Only the trusted SetConnected connects it.
		setConnected(t, g, t0.Add(30*time.Second), "p2", true)
		if p := playerByID(t, g, "p2"); !p.Connected || !p.ConnectedAt.Equal(t0.Add(30*time.Second)) {
			t.Errorf("after SetConnected: %+v", p)
		}
	})
	t.Run("a connected seat is untouched, ConnectedAt included", func(t *testing.T) {
		g := newLobby(t, 2)
		setConnected(t, g, t0.Add(10*time.Second), "p2", false)
		setConnected(t, g, t0.Add(20*time.Second), "p2", true)
		before := snapshot(t, g)
		apply(t, g, t0.Add(30*time.Second), "p2", Join{Name: "x"})
		if after := snapshot(t, g); after != before {
			t.Errorf("join on a connected seat changed the game:\nbefore %s\n after %s", before, after)
		}
	})
	t.Run("a join cannot skew the host election", func(t *testing.T) {
		g := newLobby(t, 3)
		setConnected(t, g, t0.Add(10*time.Second), "p2", false)
		setConnected(t, g, t0.Add(10*time.Second), "p3", false)
		apply(t, g, t0.Add(20*time.Second), "p3", Join{Name: "x"}) // must not count as connected
		apply(t, g, t0.Add(30*time.Second), "p1", Leave{})
		// Nobody is connected, so the earliest joiner wins. If the join had
		// connected p3 it would have been elected instead.
		if g.HostID != "p2" {
			t.Errorf("host = %s, want p2", g.HostID)
		}
	})
	t.Run("a join cannot skew the vote thresholds", func(t *testing.T) {
		g, now := newReview(t, ansP1, ansP1, ansP1)
		setConnected(t, g, now, "p3", false)
		apply(t, g, now, "p3", Join{Name: "x"}) // p3 stays out of the electorate
		apply(t, g, now, "p2", VoteReject{Category: 0, Target: "p1"})
		// Connected others than the author: only p2, so one vote rejects. With
		// p3 wrongly connected it would be 1 of 2 and not enough.
		if got := cell(t, liveResult(t, g), "p1", 0); got.Status != StatusRejected {
			t.Errorf("p1 = %+v, want rejected (electorate is p2 alone)", got)
		}
	})
}

func TestJoinReclaimStillEnforcesTheRules(t *testing.T) {
	t.Run("a brand-new seat is created connected at now", func(t *testing.T) {
		g := newLobby(t, 1)
		apply(t, g, t0.Add(5*time.Second), "bea", Join{Name: "Bea"})
		if p := playerByID(t, g, "bea"); !p.Connected || !p.ConnectedAt.Equal(t0.Add(5*time.Second)) {
			t.Errorf("bea = %+v", p)
		}
	})
	t.Run("a left seat is refused when the lobby is full, and stays left", func(t *testing.T) {
		g := newPlaying(t, MaxPlayers, threeCategories)
		apply(t, g, tStart.Add(time.Second), "p3", Leave{})
		apply(t, g, tStart.Add(time.Second), "late", Join{Name: "Late"})
		expectErr(t, g, tStart.Add(2*time.Second), "p3", Join{Name: "Back"}, ErrLobbyFull)
		if !playerByID(t, g, "p3").Left {
			t.Error("p3 must stay Left")
		}
	})
	t.Run("a banned id is refused whatever the seat state", func(t *testing.T) {
		g := newPlaying(t, 3, threeCategories)
		apply(t, g, tStart.Add(time.Second), "p1", Kick{PlayerID: "p3"})
		expectErr(t, g, tStart.Add(2*time.Second), "p3", Join{Name: "Back"}, ErrKicked)
		if p := playerByID(t, g, "p3"); !p.Left || p.Connected {
			t.Errorf("p3 = %+v", p)
		}
	})
	t.Run("reclaiming a seat when there is no host makes the player host without connecting them", func(t *testing.T) {
		g := newPlaying(t, 2, threeCategories)
		apply(t, g, tStart.Add(time.Second), "p1", Leave{})
		apply(t, g, tStart.Add(time.Second), "p2", Leave{})
		apply(t, g, tStart.Add(2*time.Second), "p2", Join{Name: "Back"})
		if p := playerByID(t, g, "p2"); g.HostID != "p2" || p.Connected {
			t.Errorf("host %q p2 %+v", g.HostID, p)
		}
	})
}

func TestLeaveInLobbyFreesTheSeat(t *testing.T) {
	g := newLobby(t, 3)
	apply(t, g, t0.Add(time.Minute), "p2", Leave{})
	assertStrings(t, "players", playerIDs(g), []string{"p1", "p3"})

	// They can come back as a new player, behind the others.
	apply(t, g, t0.Add(2*time.Minute), "p2", Join{Name: "Back"})
	assertStrings(t, "players", playerIDs(g), []string{"p1", "p3", "p2"})
	if got := playerByID(t, g, "p2").JoinedSeq; got != 4 {
		t.Errorf("JoinedSeq = %d, want 4", got)
	}
}

func TestSetConnected(t *testing.T) {
	g := newLobby(t, 2)
	setConnected(t, g, t0.Add(10*time.Second), "p2", false)
	p := playerByID(t, g, "p2")
	if p.Connected || !p.ConnectedAt.Equal(t0.Add(2*time.Second)) {
		t.Errorf("after disconnect: %+v (ConnectedAt must be kept)", p)
	}

	setConnected(t, g, t0.Add(20*time.Second), "p2", true)
	if p := playerByID(t, g, "p2"); !p.Connected || !p.ConnectedAt.Equal(t0.Add(20*time.Second)) {
		t.Errorf("after reconnect: %+v", p)
	}

	setConnected(t, g, t0.Add(30*time.Second), "p2", true)
	if got := playerByID(t, g, "p2").ConnectedAt; !got.Equal(t0.Add(20 * time.Second)) {
		t.Errorf("a redundant connect must not restamp ConnectedAt, got %v", got)
	}

	// A disconnected host stays host and keeps the seat.
	events := setConnected(t, g, t0.Add(40*time.Second), "p1", false)
	if g.HostID != "p1" || len(events) != 0 {
		t.Errorf("host = %s events = %v, a disconnected host keeps the role", g.HostID, events)
	}
	if len(g.Players) != 2 {
		t.Errorf("players = %v, disconnected players keep their seat", g.Players)
	}
}

func TestSetConnectedErrors(t *testing.T) {
	g := newLobby(t, 2)
	before := snapshot(t, g)
	for _, id := range []string{"ghost", ""} {
		events, err := g.SetConnected(t0.Add(time.Second), id, true)
		if err != ErrUnknownPlayer || len(events) != 0 {
			t.Errorf("SetConnected(%q) = %v, %v; want ErrUnknownPlayer", id, events, err)
		}
	}
	if after := snapshot(t, g); after != before {
		t.Errorf("a failed SetConnected changed the state:\nbefore %s\n after %s", before, after)
	}

	// A player who left in a started game has no seat to flag.
	g = newPlaying(t, 3, nil)
	apply(t, g, tStart.Add(time.Second), "p3", Leave{})
	if _, err := g.SetConnected(tStart.Add(2*time.Second), "p3", true); err != ErrUnknownPlayer {
		t.Errorf("SetConnected for a player who left = %v, want ErrUnknownPlayer", err)
	}
	if playerByID(t, g, "p3").Connected {
		t.Error("a player who left must stay disconnected")
	}
}

func TestSetConnectedRunsDueTransitionsFirst(t *testing.T) {
	g := newLobby(t, 2)
	apply(t, g, tStart, "p1", StartGame{})

	// The 10 s chooser deadline passed before the socket event is handled: the
	// auto-stop is applied (and reported) first, then the flag changes.
	events, err := g.SetConnected(tStart.Add(ChooserTimeout), "p2", false)
	if err != nil {
		t.Fatal(err)
	}
	if e, ok := hasEvent(events, EventLetterRevealed); !ok || e.Letter != "D" {
		t.Errorf("events = %+v, want the auto-stop letter_revealed", events)
	}
	if g.Phase != PhasePlaying || playerByID(t, g, "p2").Connected {
		t.Errorf("phase %s, p2 connected %v", g.Phase, playerByID(t, g, "p2").Connected)
	}

	// Tick events are returned even when the player is unknown.
	g = newLobby(t, 2)
	apply(t, g, tStart, "p1", StartGame{})
	events, err = g.SetConnected(tStart.Add(ChooserTimeout), "ghost", true)
	if err != ErrUnknownPlayer {
		t.Fatalf("err = %v", err)
	}
	if _, ok := hasEvent(events, EventLetterRevealed); !ok || g.Phase != PhasePlaying {
		t.Errorf("events = %+v phase %s", events, g.Phase)
	}
}

func TestHostTransfer(t *testing.T) {
	// setup builds a lobby of p1..p4 where connection times are shaped by
	// disconnect/reconnect events.
	tests := []struct {
		name     string
		setup    func(t *testing.T, g *Game)
		wantHost string
	}{
		{
			name:     "earliest ConnectedAt wins",
			setup:    func(t *testing.T, g *Game) {},
			wantHost: "p2", // p2 joined before p3 and p4
		},
		{
			name: "a reconnect moves a player to the back of the queue",
			setup: func(t *testing.T, g *Game) {
				now := t0.Add(time.Minute)
				setConnected(t, g, now, "p2", false)
				setConnected(t, g, now.Add(time.Second), "p2", true)
			},
			wantHost: "p3",
		},
		{
			name: "disconnected players are skipped",
			setup: func(t *testing.T, g *Game) {
				setConnected(t, g, t0.Add(time.Minute), "p2", false)
			},
			wantHost: "p3",
		},
		{
			name: "nobody connected gives the earliest joiner",
			setup: func(t *testing.T, g *Game) {
				for _, p := range []string{"p4", "p2", "p3"} {
					setConnected(t, g, t0.Add(time.Minute), p, false)
				}
			},
			wantHost: "p2",
		},
		{
			name: "ties on ConnectedAt are broken by JoinedSeq",
			setup: func(t *testing.T, g *Game) {
				now := t0.Add(time.Minute)
				for _, p := range []string{"p4", "p3", "p2"} {
					setConnected(t, g, now, p, false)
				}
				for _, p := range []string{"p4", "p3", "p2"} {
					setConnected(t, g, now.Add(time.Second), p, true)
				}
			},
			wantHost: "p2",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := newLobby(t, 4)
			tt.setup(t, g)
			events := apply(t, g, t0.Add(time.Hour), "p1", Leave{})
			if g.HostID != tt.wantHost {
				t.Errorf("host = %s, want %s", g.HostID, tt.wantHost)
			}
			if e, ok := hasEvent(events, EventHostChanged); !ok || e.PlayerID != tt.wantHost {
				t.Errorf("events = %+v, want host_changed -> %s", events, tt.wantHost)
			}
		})
	}
}

func TestHostTransferInGameSkipsPlayersWhoLeft(t *testing.T) {
	g := newPlaying(t, 4, nil)
	apply(t, g, tStart.Add(time.Second), "p2", Leave{})
	apply(t, g, tStart.Add(2*time.Second), "p1", Leave{})
	if g.HostID != "p3" {
		t.Errorf("host = %s, want p3", g.HostID)
	}
	if p := playerByID(t, g, "p1"); !p.Left || p.Connected {
		t.Errorf("p1 = %+v, want left and disconnected", p)
	}
}

func TestLastPlayerLeavingLeavesNoHost(t *testing.T) {
	g := newLobby(t, 2)
	apply(t, g, t0.Add(time.Minute), "p1", Leave{})
	apply(t, g, t0.Add(time.Minute), "p2", Leave{})
	if g.HostID != "" || len(g.Players) != 0 {
		t.Errorf("host %q players %v", g.HostID, g.Players)
	}
	// Whoever joins next becomes host.
	apply(t, g, t0.Add(2*time.Minute), "p9", Join{Name: "New"})
	if g.HostID != "p9" {
		t.Errorf("host = %q, want p9", g.HostID)
	}
}

func TestKick(t *testing.T) {
	t.Run("host kicks in the lobby", func(t *testing.T) {
		g := newLobby(t, 3)
		events := apply(t, g, t0.Add(time.Minute), "p1", Kick{PlayerID: "p2"})
		if e, ok := hasEvent(events, EventKicked); !ok || e.PlayerID != "p2" {
			t.Errorf("events = %+v", events)
		}
		assertStrings(t, "players", playerIDs(g), []string{"p1", "p3"})
		expectErr(t, g, t0.Add(2*time.Minute), "p2", Join{Name: "Again"}, ErrKicked)
		if _, err := g.SetConnected(t0.Add(2*time.Minute), "p2", true); err != ErrUnknownPlayer {
			t.Errorf("SetConnected for a kicked player = %v, want ErrUnknownPlayer", err)
		}
	})
	t.Run("only the host", func(t *testing.T) {
		g := newLobby(t, 3)
		expectErr(t, g, t0.Add(time.Minute), "p2", Kick{PlayerID: "p3"}, ErrNotHost)
	})
	t.Run("not yourself", func(t *testing.T) {
		g := newLobby(t, 3)
		expectErr(t, g, t0.Add(time.Minute), "p1", Kick{PlayerID: "p1"}, ErrCannotKickSelf)
	})
	t.Run("unknown target", func(t *testing.T) {
		g := newLobby(t, 3)
		expectErr(t, g, t0.Add(time.Minute), "p1", Kick{PlayerID: "ghost"}, ErrUnknownPlayer)
	})
	t.Run("in game the seat stays flagged as left", func(t *testing.T) {
		g := newPlaying(t, 3, nil)
		apply(t, g, tStart.Add(time.Second), "p1", Kick{PlayerID: "p3"})
		if p := playerByID(t, g, "p3"); !p.Left || p.Connected {
			t.Errorf("p3 = %+v", p)
		}
		expectErr(t, g, tStart.Add(2*time.Second), "p3", Join{Name: "Back"}, ErrKicked)
		expectErr(t, g, tStart.Add(2*time.Second), "p3", SetAnswer{Category: 0, Text: "x"}, ErrUnknownPlayer)
		expectErr(t, g, tStart.Add(2*time.Second), "p1", Kick{PlayerID: "p3"}, ErrUnknownPlayer)
	})
	t.Run("a player who left in game reclaims their seat", func(t *testing.T) {
		g := newPlaying(t, 3, threeCategories)
		g.Players[2].TotalScore = 7
		apply(t, g, tStart.Add(time.Second), "p3", Leave{})
		if got := g.seatedCount(); got != 2 {
			t.Fatalf("seated = %d", got)
		}
		apply(t, g, tStart.Add(2*time.Second), "p3", Join{Name: "Back"})
		p := playerByID(t, g, "p3")
		// Leaving disconnected the seat; the join only clears Left. The room
		// layer reports the live socket through SetConnected.
		if p.Left || p.Connected || !p.ConnectedAt.Equal(t0.Add(3*time.Second)) {
			t.Errorf("p3 = %+v, want reclaimed but still disconnected with the old ConnectedAt", p)
		}
		if p.Name != "Player 3" || p.JoinedRound != 1 || p.TotalScore != 7 || p.JoinedSeq != 3 {
			t.Errorf("the seat must come back as it was: %+v", p)
		}
		setConnected(t, g, tStart.Add(2*time.Second), "p3", true)
		if p := playerByID(t, g, "p3"); !p.Connected || !p.ConnectedAt.Equal(tStart.Add(2*time.Second)) {
			t.Errorf("after SetConnected: %+v", p)
		}
		if n := strings.Count(snapshot(t, g), `"id":"p3"`); n != 1 {
			t.Errorf("p3 appears %d times in the player list", n)
		}
		assertStrings(t, "participants", g.Round.Participants, []string{"p1", "p2", "p3"})
		apply(t, g, tStart.Add(3*time.Second), "p3", SetAnswer{Category: 0, Text: "Ana"})
	})
	t.Run("coming back needs a free seat", func(t *testing.T) {
		g := newPlaying(t, MaxPlayers, threeCategories)
		apply(t, g, tStart.Add(time.Second), "p3", Leave{})
		apply(t, g, tStart.Add(time.Second), "late", Join{Name: "Late"})
		expectErr(t, g, tStart.Add(2*time.Second), "p3", Join{Name: "Back"}, ErrLobbyFull)
	})
}

func TestUpdateSettings(t *testing.T) {
	custom := DefaultSettings()
	custom.Lang = LangEN
	custom.Rounds = 3
	custom.TimeLimitSeconds = 120
	custom.Categories = []CategoryRef{{ID: "pais"}, {ID: "animal"}, {Custom: "  Cosas   rojas "}}
	invalid := DefaultSettings()
	invalid.Rounds = 99
	dup := DefaultSettings()
	dup.Categories = append(ids("nombre", "animal"), CategoryRef{Custom: "país"})

	t.Run("host updates", func(t *testing.T) {
		g := newLobby(t, 2)
		apply(t, g, t0.Add(time.Minute), "p1", UpdateSettings{Settings: custom})
		if g.Settings.Lang != LangEN || g.Settings.Rounds != 3 || g.Settings.TimeLimitSeconds != 120 {
			t.Errorf("settings = %+v", g.Settings)
		}
		if got := g.Settings.Categories[2].Custom; got != "Cosas rojas" {
			t.Errorf("custom label %q was not cleaned", got)
		}
		custom.Categories[0].ID = "mutated"
		if g.Settings.Categories[0].ID != "pais" {
			t.Error("the game must not alias the command's slice")
		}
	})
	t.Run("only the host", func(t *testing.T) {
		g := newLobby(t, 2)
		expectErr(t, g, t0.Add(time.Minute), "p2", UpdateSettings{Settings: custom}, ErrNotHost)
	})
	t.Run("only in the lobby", func(t *testing.T) {
		g := newPlaying(t, 2, nil)
		expectErr(t, g, tStart.Add(time.Second), "p1", UpdateSettings{Settings: DefaultSettings()}, ErrWrongPhase)
	})
	t.Run("validated", func(t *testing.T) {
		g := newLobby(t, 2)
		expectErr(t, g, t0.Add(time.Minute), "p1", UpdateSettings{Settings: invalid}, ErrInvalidSettings)
		expectErr(t, g, t0.Add(time.Minute), "p1", UpdateSettings{Settings: dup}, ErrInvalidSettings)
	})
}

func TestStartGame(t *testing.T) {
	t.Run("starts the first round with the host as chooser", func(t *testing.T) {
		g := newLobby(t, 3)
		events := apply(t, g, tStart, "p1", StartGame{})
		if g.Phase != PhaseLetter || g.RoundNum != 1 || g.ChooserID != "p1" {
			t.Errorf("phase %s round %d chooser %s", g.Phase, g.RoundNum, g.ChooserID)
		}
		assertStrings(t, "participants", g.Round.Participants, []string{"p1", "p2", "p3"})
		if !g.Round.LetterStartedAt.Equal(tStart) {
			t.Errorf("LetterStartedAt = %v", g.Round.LetterStartedAt)
		}
		for _, id := range g.Round.Participants {
			if got := len(g.Round.Answers[id]); got != 6 {
				t.Errorf("%s has %d answer slots, want 6", id, got)
			}
		}
		got := eventTypes(events)
		if len(got) != 2 || got[0] != EventGameStarted || got[1] != EventRoundStarted {
			t.Errorf("events = %v", got)
		}
		if e, _ := hasEvent(events, EventRoundStarted); e.Round != 1 || e.PlayerID != "p1" {
			t.Errorf("round_started = %+v", e)
		}
	})
	t.Run("only the host", func(t *testing.T) {
		g := newLobby(t, 2)
		expectErr(t, g, tStart, "p2", StartGame{}, ErrNotHost)
	})
	t.Run("needs two players", func(t *testing.T) {
		g := newLobby(t, 1)
		expectErr(t, g, tStart, "p1", StartGame{}, ErrNotEnoughPlayers)
	})
	t.Run("disconnected players still count as seated", func(t *testing.T) {
		g := newLobby(t, 2)
		setConnected(t, g, tStart, "p2", false)
		apply(t, g, tStart, "p1", StartGame{})
	})
	t.Run("only once", func(t *testing.T) {
		g := newLobby(t, 2)
		apply(t, g, tStart, "p1", StartGame{})
		expectErr(t, g, tStart, "p1", StartGame{}, ErrWrongPhase)
	})
	t.Run("invalid settings", func(t *testing.T) {
		g := newLobby(t, 2)
		g.Settings.Categories = g.Settings.Categories[:2]
		expectErr(t, g, tStart, "p1", StartGame{}, ErrInvalidSettings)
	})
}

func TestMidGameJoinIsASpectatorUntilNextRound(t *testing.T) {
	g := newPlaying(t, 3, threeCategories)
	apply(t, g, tStart.Add(time.Second), "p4", Join{Name: "Late"})

	late := playerByID(t, g, "p4")
	if late.JoinedRound != 2 || late.TotalScore != 0 {
		t.Fatalf("late joiner = %+v", late)
	}
	assertStrings(t, "participants", g.Round.Participants, []string{"p1", "p2", "p3"})
	expectErr(t, g, tStart.Add(2*time.Second), "p4", SetAnswer{Category: 0, Text: "Ana"}, ErrNotParticipant)
	expectErr(t, g, tStart.Add(2*time.Second), "p4", Basta{}, ErrNotParticipant)

	// Finish round 1 and move on: the joiner takes part in round 2.
	for _, p := range []string{"p1", "p2", "p3"} {
		fill(t, g, tStart.Add(3*time.Second), p, "Ana", "Argentina", "Alce")
	}
	now := lockRound(t, g, tStart.Add(4*time.Second), "p1")
	expectErr(t, g, now, "p4", VoteReject{Category: 0, Target: "p1"}, ErrNotParticipant)
	apply(t, g, now, "p1", FinishReview{})
	apply(t, g, now, "p1", NextRound{})

	assertStrings(t, "participants round 2", g.Round.Participants, []string{"p1", "p2", "p3", "p4"})
	if g.ChooserID != "p2" {
		t.Errorf("chooser = %s, want p2", g.ChooserID)
	}
}

// finalGame returns a game of three players (p1 host) in the final phase.
func finalGame(t *testing.T) (*Game, time.Time) {
	t.Helper()
	g, now := newReview(t, ansP1, ansP1, ansP1)
	apply(t, g, now, "p1", FinishReview{})
	apply(t, g, now, "p1", EndGame{})
	if g.Phase != PhaseFinal {
		t.Fatalf("setup: phase = %s", g.Phase)
	}
	return g, now
}

func TestJoinInFinal(t *testing.T) {
	t.Run("a new player is refused", func(t *testing.T) {
		g, now := finalGame(t)
		expectErr(t, g, now, "p9", Join{Name: "Newcomer"}, ErrWrongPhase)
		// The phase is checked before the nickname.
		expectErr(t, g, now, "p9", Join{Name: ""}, ErrWrongPhase)
	})
	t.Run("a refused joiner is not swept into the rematch", func(t *testing.T) {
		g, now := finalGame(t)
		expectErr(t, g, now, "p9", Join{Name: "Newcomer"}, ErrWrongPhase)
		apply(t, g, now, "p1", Rematch{})
		assertStrings(t, "players", playerIDs(g), []string{"p1", "p2", "p3"})
	})
	t.Run("they can join once the game is back in the lobby", func(t *testing.T) {
		g, now := finalGame(t)
		apply(t, g, now, "p1", Rematch{})
		apply(t, g, now, "p9", Join{Name: "Newcomer"})
		p := playerByID(t, g, "p9")
		if p.JoinedRound != 1 || p.TotalScore != 0 || p.Left || !p.Connected {
			t.Errorf("p9 = %+v", p)
		}
		assertStrings(t, "players", playerIDs(g), []string{"p1", "p2", "p3", "p9"})
	})
	t.Run("a seated player is accepted but not reconnected by join", func(t *testing.T) {
		g, now := finalGame(t)
		setConnected(t, g, now, "p2", false)
		apply(t, g, now.Add(time.Second), "p2", Join{Name: "ignored"})
		if p := playerByID(t, g, "p2"); p.Connected || p.Left {
			t.Errorf("p2 = %+v, want still disconnected", p)
		}
		setConnected(t, g, now.Add(2*time.Second), "p2", true)
		if !playerByID(t, g, "p2").Connected {
			t.Error("SetConnected must connect the seat")
		}
	})
	t.Run("a player who left may reclaim the seat", func(t *testing.T) {
		g, now := finalGame(t)
		apply(t, g, now, "p3", Leave{})
		apply(t, g, now.Add(time.Second), "p3", Join{Name: "ignored"})
		p := playerByID(t, g, "p3")
		if p.Left || p.Connected || p.JoinedSeq != 3 || p.TotalScore != 15 {
			t.Errorf("p3 = %+v, want reclaimed and still disconnected", p)
		}
		apply(t, g, now.Add(2*time.Second), "p1", Rematch{})
		assertStrings(t, "players", playerIDs(g), []string{"p1", "p2", "p3"})
	})
	t.Run("a kicked player stays banned", func(t *testing.T) {
		g, now := finalGame(t)
		apply(t, g, now, "p1", Kick{PlayerID: "p3"})
		expectErr(t, g, now, "p3", Join{Name: "x"}, ErrKicked)
	})
}

func TestJoinDuringSummaryTakesPartNextRound(t *testing.T) {
	g := newPlaying(t, 2, threeCategories)
	for _, p := range []string{"p1", "p2"} {
		fill(t, g, tStart.Add(time.Second), p, "Ana", "Argentina", "Alce")
	}
	now := lockRound(t, g, tStart.Add(2*time.Second), "p1")
	apply(t, g, now, "p1", FinishReview{})
	apply(t, g, now, "p3", Join{Name: "Third"})
	if got := playerByID(t, g, "p3").JoinedRound; got != 2 {
		t.Errorf("JoinedRound = %d, want 2", got)
	}
	apply(t, g, now, "p1", NextRound{})
	assertStrings(t, "participants", g.Round.Participants, []string{"p1", "p2", "p3"})
}

func ExampleGame_Apply() {
	now := time.Date(2026, 1, 2, 12, 0, 0, 0, time.UTC)
	g := NewGame(Player{ID: "ana", Name: "Ana"}, now)
	_, _ = g.Apply(now, "bea", Join{Name: "Bea"})
	events, _ := g.Apply(now, "ana", StartGame{})
	for _, e := range events {
		fmt.Println(e.Type)
	}
	// Output:
	// game_started
	// round_started
}
