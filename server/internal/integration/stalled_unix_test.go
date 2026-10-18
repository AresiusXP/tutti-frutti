//go:build unix

package integration

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"syscall"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/AresiusXP/tutti-frutti/server/internal/game"
	"github.com/AresiusXP/tutti-frutti/server/internal/protocol"
)

// dialStalled opens a WebSocket as p whose TCP receive buffer is tiny and that
// is never read from: whatever the server sends piles up in the kernel and then
// in the server's own send queue.
func (s *stack) dialStalled(t *testing.T, p *player) *websocket.Conn {
	t.Helper()
	dialer := &net.Dialer{Control: func(_, _ string, c syscall.RawConn) error {
		var serr error
		if err := c.Control(func(fd uintptr) {
			serr = syscall.SetsockoptInt(int(fd), syscall.SOL_SOCKET, syscall.SO_RCVBUF, 1024)
		}); err != nil {
			return err
		}
		return serr
	}}
	hc := &http.Client{Transport: &http.Transport{DialContext: dialer.DialContext, DisableKeepAlives: true}}
	h := http.Header{}
	h.Set("Cookie", p.cookie)
	h.Set("Origin", s.origin)
	n := s.dialSeq.Add(1)
	h.Set("X-Forwarded-For", fmt.Sprintf("10.%d.%d.%d", n>>16&0xff, n>>8&0xff, n&0xff))
	ctx, cancel := context.WithTimeout(context.Background(), waitLimit)
	defer cancel()
	conn, _, err := websocket.Dial(ctx, s.wsURL(), &websocket.DialOptions{HTTPClient: hc, HTTPHeader: h})
	if err != nil {
		t.Fatalf("dial as %s: %v", p.name, err)
	}
	t.Cleanup(func() { _ = conn.CloseNow() })
	return conn
}

// TestSendDropsAreCountedForAClientThatStopsReading: a player's connection
// stalls (its phone went into a tunnel), the room keeps talking to it, the
// connection's send queue overflows and the server cuts it off. The drop is
// counted exactly once, and the other players are not affected.
func TestSendDropsAreCountedForAClientThatStopsReading(t *testing.T) {
	// The per-connection rate limit is not what is being tested: its clock runs
	// a thousand times faster than real time, so the others can talk freely.
	began := time.Now()
	s := newStack(t, stackOpts{wsNow: func() time.Time { return epoch.Add(time.Since(began) * 1000) }})
	tb := newTable(t, s, "Ana", "Beto", "Cami")
	tb.advanceTo(stagePlaying)
	if got := s.metricValue(mSendDrops, nil); got != 0 {
		t.Fatalf("ws_send_drops_total = %v before anything stalled", got)
	}

	zed := s.newPlayer("Zed")
	conn := s.dialStalled(t, zed)
	join, _ := json.Marshal(protocol.JoinRequest{Code: tb.code, Name: "Zed", Avatar: game.Avatar{Emoji: "🍓", Color: "#e11d48"}})
	env, _ := json.Marshal(protocol.Envelope{T: game.CmdJoin, V: protocol.Version, D: join})
	ctx, cancel := context.WithTimeout(context.Background(), waitLimit)
	defer cancel()
	if err := conn.Write(ctx, websocket.MessageText, env); err != nil {
		t.Fatalf("join as Zed: %v", err)
	}
	eventually(t, "Zed to be seated", func() bool { return len(tb.anyView().Players) == 4 })

	// Everybody else keeps changing the room (each accepted command sends every
	// joined player a state frame) while Zed reads nothing.
	sent := 0
	for round := 0; s.metricValue(mSendDrops, nil) == 0; round++ {
		if round > 100 {
			t.Fatalf("no send queue overflowed after %d commands", sent)
		}
		for i := range 3 {
			for range 40 { // one at a time: the players that do read must never fall behind
				tb.mustCmd(i, game.CmdSetAnswer, game.SetAnswer{Category: 0, Text: r1Answers[i][0]})
				sent++
			}
		}
	}
	t.Logf("%d commands until the queue overflowed", sent)
	if got := s.metricValue(mSendDrops, nil); got != 1 {
		t.Errorf("ws_send_drops_total = %v, want 1", got)
	}
	eventually(t, "the room to mark Zed's seat disconnected", func() bool {
		for _, p := range tb.anyView().Players {
			if p.Name == "Zed" {
				return !p.Connected
			}
		}
		return false
	})
	for i := range 3 { // the others still play
		tb.mustCmd(i, game.CmdSetAnswer, game.SetAnswer{Category: 0, Text: r1Answers[i][0]})
	}
	if got := s.metricValue(mSendDrops, nil); got != 1 {
		t.Errorf("ws_send_drops_total = %v after the drop, want it to stay 1", got)
	}
}
