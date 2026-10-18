package game

import (
	"slices"
	"time"
)

const maxAvatarField = 32

// join seats a player. A brand-new player gets a new seat, created connected
// at now (the room layer only calls Join for a live socket). A player who is
// already known, either still seated or having left earlier in a started
// game, only reclaims their seat: Left is cleared and the score, join order
// and name are kept.
//
// Join never changes Connected or ConnectedAt of an existing seat. The room
// layer owns the sockets and is the only authority on connection state, so it
// must call SetConnected(now, id, true) after a successful Join of a returning
// player. (A seat that was flagged Left is always disconnected, because
// leaving disconnects it, and stays so until the room layer says otherwise.)
// An existing seat is also refused for banned ids (ErrKicked) and, when it
// was Left, for a full lobby (ErrLobbyFull). A brand-new player cannot join in
// the final phase (ErrWrongPhase); they can once the host starts the rematch
// lobby.
func (g *Game) join(now time.Time, id string, c Join) error {
	if id == "" {
		return ErrUnknownPlayer
	}
	if slices.Contains(g.Banned, id) {
		return ErrKicked
	}
	if i := slices.IndexFunc(g.Players, func(p Player) bool { return p.ID == id }); i >= 0 {
		p := &g.Players[i]
		if p.Left {
			if g.seatedCount() >= MaxPlayers {
				return ErrLobbyFull
			}
			p.Left = false
		}
		g.ensureHost(id)
		return nil
	}

	// New players wait for the rematch: in the final phase they would only be
	// swept into it without ever having played.
	if g.Phase == PhaseFinal {
		return ErrWrongPhase
	}
	name, err := CleanName(c.Name)
	if err != nil {
		return err
	}
	if len(c.Avatar.Emoji) > maxAvatarField || len(c.Avatar.Color) > maxAvatarField {
		return ErrInvalidAvatar
	}
	if g.seatedCount() >= MaxPlayers {
		return ErrLobbyFull
	}

	joinedRound := 1
	if g.Phase != PhaseLobby {
		joinedRound = g.RoundNum + 1
	}
	g.Players = append(g.Players, Player{
		ID: id, Name: name, Avatar: c.Avatar,
		Connected: true, ConnectedAt: now,
		JoinedSeq: g.NextSeq, JoinedRound: joinedRound,
	})
	g.NextSeq++
	g.ensureHost(id)
	return nil
}

// ensureHost makes id the host if the game has none (everybody had left).
func (g *Game) ensureHost(id string) {
	if g.HostID == "" {
		g.HostID = id
	}
}

// SetConnected records that a player's connection opened (connected) or closed.
// It is not a Command: only the trusted room layer may call it, so a client
// cannot change its own flag and skew the live vote thresholds or the host
// election. Like Apply it first runs Tick(now) and returns those events.
//
// Becoming connected stamps ConnectedAt (a redundant call changes nothing);
// disconnecting keeps ConnectedAt. A disconnected player keeps their seat, and
// a disconnected host stays host: the role only moves when the host leaves or
// is kicked. It returns ErrUnknownPlayer for a player who is not seated.
func (g *Game) SetConnected(now time.Time, playerID string, connected bool) ([]Event, error) {
	events := g.Tick(now)
	p := g.seat(playerID)
	if p == nil {
		return events, ErrUnknownPlayer
	}
	if connected && !p.Connected {
		p.ConnectedAt = now
	}
	p.Connected = connected
	return events, nil
}

// dropPlayer removes a player who left or was kicked: from the list in the
// lobby, otherwise by flagging them Left so scores and history stay intact.
// It hands the host role over if needed.
func (g *Game) dropPlayer(id string, kicked bool) []Event {
	var events []Event
	if kicked {
		g.Banned = append(g.Banned, id)
		events = append(events, Event{Type: EventKicked, PlayerID: id})
	}
	if g.Phase == PhaseLobby {
		g.Players = slices.DeleteFunc(g.Players, func(p Player) bool { return p.ID == id })
	} else if p := g.seat(id); p != nil {
		p.Left = true
		p.Connected = false
	}
	if g.HostID == id {
		events = append(events, g.electHost()...)
	}
	return events
}

// electHost picks a new host among the seated players: the connected player
// with the earliest ConnectedAt (ties by JoinedSeq). If nobody is connected
// the earliest joiner is chosen. With no seated player the game has no host.
func (g *Game) electHost() []Event {
	var best *Player
	for i := range g.Players {
		c := &g.Players[i]
		if !c.Left && (best == nil || hostRank(c, best)) {
			best = c
		}
	}
	g.HostID = ""
	if best != nil {
		g.HostID = best.ID
	}
	return []Event{{Type: EventHostChanged, PlayerID: g.HostID}}
}

// hostRank reports whether a should be preferred over b as host.
func hostRank(a, b *Player) bool {
	if a.Connected != b.Connected {
		return a.Connected
	}
	if a.Connected && !a.ConnectedAt.Equal(b.ConnectedAt) {
		return a.ConnectedAt.Before(b.ConnectedAt)
	}
	return a.JoinedSeq < b.JoinedSeq
}

func (g *Game) kick(host *Player, c Kick) ([]Event, error) {
	if err := g.requireHost(host); err != nil {
		return nil, err
	}
	if c.PlayerID == host.ID {
		return nil, ErrCannotKickSelf
	}
	if g.seat(c.PlayerID) == nil {
		return nil, ErrUnknownPlayer
	}
	return g.dropPlayer(c.PlayerID, true), nil
}

func (g *Game) updateSettings(host *Player, c UpdateSettings) error {
	if err := g.requireHost(host); err != nil {
		return err
	}
	if g.Phase != PhaseLobby {
		return ErrWrongPhase
	}
	s := c.Settings.Clean()
	if err := s.Validate(); err != nil {
		return err
	}
	g.Settings = s
	return nil
}

func (g *Game) startGame(now time.Time, host *Player) ([]Event, error) {
	if err := g.requireHost(host); err != nil {
		return nil, err
	}
	if g.Phase != PhaseLobby {
		return nil, ErrWrongPhase
	}
	if g.seatedCount() < MinPlayers {
		return nil, ErrNotEnoughPlayers
	}
	if err := g.Settings.Validate(); err != nil {
		return nil, err
	}
	g.UsedLetters = []string{}
	g.History = []RoundResult{}
	g.Winners = nil
	g.RoundNum = 0
	g.ChooserSeq = 0
	events := []Event{{Type: EventGameStarted}}
	return append(events, g.beginRound(now)...), nil
}
