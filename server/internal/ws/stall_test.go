package ws

import (
	"fmt"
	"testing"
	"time"

	"github.com/AresiusXP/tutti-frutti/server/internal/protocol"
)

var joinJSON = fmt.Sprintf(`{"t":"join","v":%d,"d":{"code":"KT7P","name":"Ana","avatar":{"emoji":"🍓","color":"#f06"}}}`, protocol.Version)

// A peer that completes a frame header, sends two payload bytes and stalls
// leaves the server's reader blocked in the middle of a frame. Neither the
// idle timeout nor a room-initiated close may leave it (or the writer) behind.
func TestIdleTimeoutInterruptsAReadBlockedOnAPartialFrame(t *testing.T) {
	e := newEnv(t,
		func(d *Deps) { d.ReadIdleTimeout = 200 * time.Millisecond },
		// The library's own close handshake would hold the reader for 5 s:
		// the connection must be cut by the grace timer long before.
		func(h *Handler) { h.closeGrace = 150 * time.Millisecond },
	)
	rc := e.rawDial()
	rc.write(textFrame(joinJSON))
	sess := e.rooms.session(t, 0)

	rc.write(partialFrame(4000, 2)) // header declaring 4000 bytes, 2 of them sent, then silence
	start := time.Now()

	code, reason, eof := rc.untilClosed(3 * time.Second)
	elapsed := time.Since(start)
	if code != 1001 || reason != "idle" {
		t.Errorf("close = %d %q, want 1001 idle", code, reason)
	}
	if !eof {
		t.Error("the server did not drop the TCP connection")
	}
	if elapsed > 2*time.Second {
		t.Errorf("took %v to cut a stalled reader (idle 200ms + grace 150ms)", elapsed)
	}
	eventually(t, "Disconnect", func() bool { return sess.disconnects.Load() == 1 })
	assertNoConnGoroutines(t) // reader and writer are both gone
	time.Sleep(50 * time.Millisecond)
	if n := sess.disconnects.Load(); n != 1 {
		t.Errorf("Disconnect called %d times, want exactly 1", n)
	}
	if got := len(sess.handled()); got != 1 {
		t.Errorf("session handled %d frames, want only the join: the partial frame must not be delivered", got)
	}
	if got := counter(t, e.reg, "tuttifrutti_ws_closes_total", map[string]string{"code": "1001"}); got != 1 {
		t.Errorf("1001 closes counted = %v, want 1", got)
	}
}

func TestIdleTimeoutInterruptsAReadBlockedBeforeJoin(t *testing.T) {
	e := newEnv(t,
		func(d *Deps) { d.ReadIdleTimeout = 150 * time.Millisecond },
		func(h *Handler) { h.closeGrace = 100 * time.Millisecond },
	)
	rc := e.rawDial()
	rc.write(partialFrame(10, 1))
	code, _, eof := rc.untilClosed(3 * time.Second)
	if code != 1001 || !eof {
		t.Errorf("close = %d eof=%v, want 1001 and EOF", code, eof)
	}
	assertNoConnGoroutines(t)
}

func TestFrameTimeoutBoundsTheRestOfAFrameOnceItsHeaderArrived(t *testing.T) {
	e := newEnv(t, nil, func(h *Handler) {
		h.frameTimeout = 150 * time.Millisecond
		h.closeGrace = 100 * time.Millisecond
	})
	rc := e.rawDial()
	rc.write(textFrame(joinJSON))
	sess := e.rooms.session(t, 0)

	// The idle timeout is the default 60 s: only the frame timer can end this.
	rc.write(partialFrame(3000, 10))
	start := time.Now()
	code, reason, eof := rc.untilClosed(5 * time.Second)
	if code != protocol.ClosePolicy || reason != "frame timeout" || !eof {
		t.Errorf("close = %d %q eof=%v, want 1008 frame timeout and EOF", code, reason, eof)
	}
	if d := time.Since(start); d > 2*time.Second {
		t.Errorf("took %v", d)
	}
	eventually(t, "Disconnect", func() bool { return sess.disconnects.Load() == 1 })
	assertNoConnGoroutines(t)
}

func TestFrameTimerDoesNotFireForPromptFrames(t *testing.T) {
	e := newEnv(t, nil, func(h *Handler) { h.frameTimeout = 100 * time.Millisecond })
	rc := e.rawDial()
	rc.write(textFrame(joinJSON))
	sess := e.rooms.session(t, 0)
	for i := 0; i < 5; i++ {
		time.Sleep(80 * time.Millisecond) // longer than the frame timeout in total
		rc.write(textFrame(fmt.Sprintf(`{"t":"set_answer","v":%d,"d":{"category":0,"text":"x"}}`, protocol.Version)))
	}
	eventually(t, "frames routed", func() bool { return len(sess.handled()) == 6 })
	if n := sess.disconnects.Load(); n != 0 {
		t.Errorf("a connection sending complete frames was cut (%d disconnects)", n)
	}
}

func TestCloseRequestInterruptsAStalledReader(t *testing.T) {
	// A room-initiated Close (kick, shutdown) must not wait for a peer that
	// stopped in the middle of a frame either.
	e := newEnv(t, nil, func(h *Handler) { h.closeGrace = 150 * time.Millisecond })
	rc := e.rawDial()
	rc.write(textFrame(joinJSON))
	sess := e.rooms.session(t, 0)
	rc.write(partialFrame(2000, 2))
	time.Sleep(50 * time.Millisecond)

	sess.client.Close(protocol.CloseKicked, "kicked")
	code, reason, eof := rc.untilClosed(3 * time.Second)
	if code != protocol.CloseKicked || reason != "kicked" || !eof {
		t.Errorf("close = %d %q eof=%v", code, reason, eof)
	}
	eventually(t, "Disconnect", func() bool { return sess.disconnects.Load() == 1 })
	assertNoConnGoroutines(t)
}

func TestPeerThatNeverAnswersTheCloseIsCutOffAfterTheGrace(t *testing.T) {
	e := newEnv(t, nil, func(h *Handler) { h.closeGrace = 200 * time.Millisecond })
	_, sess := e.dialJoined() // the client never reads, so it never echoes the close
	start := time.Now()
	sess.client.Close(protocol.CloseServiceRestart, "restart")
	eventually(t, "the unresponsive peer to be cut off", func() bool { return sess.disconnects.Load() == 1 })
	if d := time.Since(start); d > 2*time.Second {
		t.Errorf("took %v with a 200ms grace", d)
	}
}
