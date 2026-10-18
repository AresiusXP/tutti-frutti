package ws

import (
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/AresiusXP/tutti-frutti/server/internal/protocol"
	"github.com/AresiusXP/tutti-frutti/server/internal/room"
)

func stateFrame(n int) []byte {
	return []byte(fmt.Sprintf(`{"t":"state","v":1,"d":{"code":"KT7P","n":%d}}`, n))
}

func frameN(t *testing.T, e protocol.Envelope) int {
	t.Helper()
	var d struct{ N int }
	if err := json.Unmarshal(e.D, &d); err != nil {
		t.Fatal(err)
	}
	return d.N
}

func TestRoomFramesAreDeliveredInOrder(t *testing.T) {
	e := newEnv(t, nil)
	c, sess := e.dialJoined()

	const total = 500
	go func() {
		for i := 0; i < total; i++ {
			for !sess.client.Send(stateFrame(i)) {
				time.Sleep(time.Millisecond) // queue full: wait for the writer
			}
		}
	}()
	for i := 0; i < total; i++ {
		got := read(t, c)
		if got.T != protocol.TypeState || frameN(t, got) != i {
			t.Fatalf("frame %d = %q n=%d: out of order", i, got.T, frameN(t, got))
		}
	}
}

func TestFramesSentDuringConnectAreDelivered(t *testing.T) {
	e := newEnv(t, nil)
	e.rooms.onConnect = func(_ string, c room.Client) {
		if !c.Send(stateFrame(1)) {
			t.Error("Send inside Connect was refused")
		}
	}
	c, _ := e.dialJoined()
	if got := read(t, c); got.T != protocol.TypeState {
		t.Errorf("got %q, want the state frame", got.T)
	}
}

func TestCloseFlushesQueuedFramesBeforeTheCloseFrame(t *testing.T) {
	e := newEnv(t, nil)
	c, sess := e.dialJoined()

	for i := 0; i < 20; i++ {
		if !sess.client.Send(stateFrame(i)) {
			t.Fatalf("Send %d refused", i)
		}
	}
	sess.client.Close(protocol.CloseKicked, "kicked")
	if sess.client.Send(stateFrame(99)) {
		t.Error("Send after Close must return false")
	}

	frames, ce := expectClose(t, c)
	if ce.Code != protocol.CloseKicked || ce.Reason != "kicked" {
		t.Errorf("close = %d %q, want 4002 kicked", ce.Code, ce.Reason)
	}
	if len(frames) != 20 {
		t.Fatalf("client got %d frames before the close, want the 20 queued ones", len(frames))
	}
	for i, f := range frames {
		if frameN(t, f) != i {
			t.Fatalf("frame %d out of order", i)
		}
	}
	eventually(t, "disconnect", func() bool { return sess.disconnects.Load() == 1 })
}

func TestCloseCodePropagation(t *testing.T) {
	tests := []struct {
		name       string
		code       int
		reason     string
		wantCode   int
		wantReason string
	}{
		{"replaced", protocol.CloseReplaced, "replaced", 4001, "replaced"},
		{"kicked", protocol.CloseKicked, "kicked", 4002, "kicked"},
		{"room closed", protocol.CloseRoomClosed, "room closed", 4004, "room closed"},
		{"left", protocol.CloseLeft, "", 4005, ""},
		{"service restart", protocol.CloseServiceRestart, "restarting", 1012, "restarting"},
		{"policy", protocol.ClosePolicy, "slow", 1008, "slow"},
		{"normal", 1000, "bye", 1000, "bye"},
		{"reserved 1005 cannot be sent", 1005, "x", 1011, "x"},
		{"reserved 1006 cannot be sent", 1006, "x", 1011, "x"},
		{"zero is not a code", 0, "x", 1011, "x"},
		{"out of range", 5000, "x", 1011, "x"},
		{"negative", -1, "x", 1011, "x"},
		{"private use", 4999, "x", 4999, "x"},
		{"overlong reason is cut", 4001, strings.Repeat("é", 100), 4001, strings.Repeat("é", 61)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := newEnv(t, nil)
			c, sess := e.dialJoined()
			sess.client.Close(tt.code, tt.reason)
			_, ce := expectClose(t, c)
			if int(ce.Code) != tt.wantCode || ce.Reason != tt.wantReason {
				t.Errorf("close = %d %q, want %d %q", ce.Code, ce.Reason, tt.wantCode, tt.wantReason)
			}
			eventually(t, "disconnect", func() bool { return sess.disconnects.Load() == 1 })
		})
	}
}

func TestCloseIsIdempotentAndDisconnectIsCalledOnce(t *testing.T) {
	e := newEnv(t, nil)
	c, sess := e.dialJoined()

	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			sess.client.Close(4001+i%2, "dup")
			sess.client.Close(protocol.CloseKicked, "again")
			sess.client.Send(stateFrame(i))
		}()
	}
	wg.Wait()
	sess.client.Close(protocol.CloseLeft, "late")

	_, ce := expectClose(t, c)
	if ce.Code != 4001 && ce.Code != 4002 {
		t.Errorf("close code = %d, want the first Close to win (4001 or 4002)", ce.Code)
	}
	eventually(t, "disconnect", func() bool { return sess.disconnects.Load() >= 1 })
	time.Sleep(50 * time.Millisecond)
	if n := sess.disconnects.Load(); n != 1 {
		t.Errorf("Disconnect called %d times, want exactly 1", n)
	}
}

func TestDisconnectExactlyOnceOnEveryEnding(t *testing.T) {
	endings := map[string]func(t *testing.T, e *env, c *websocket.Conn, s *fakeSession){
		"client closes cleanly": func(t *testing.T, _ *env, c *websocket.Conn, _ *fakeSession) {
			_ = c.Close(websocket.StatusNormalClosure, "bye")
		},
		"client drops the TCP connection": func(t *testing.T, _ *env, c *websocket.Conn, _ *fakeSession) {
			_ = c.CloseNow()
		},
		"room closes it": func(t *testing.T, _ *env, c *websocket.Conn, s *fakeSession) {
			s.client.Close(protocol.CloseKicked, "")
			expectClose(t, c)
		},
		"protocol violation": func(t *testing.T, _ *env, c *websocket.Conn, _ *fakeSession) {
			sendRaw(t, c, `{"t":"x","v":7}`)
			expectClose(t, c)
		},
	}
	for name, end := range endings {
		t.Run(name, func(t *testing.T) {
			e := newEnv(t, nil)
			c, sess := e.dialJoined()
			end(t, e, c, sess)
			eventually(t, "disconnect", func() bool { return sess.disconnects.Load() >= 1 })
			time.Sleep(50 * time.Millisecond)
			if n := sess.disconnects.Load(); n != 1 {
				t.Errorf("Disconnect called %d times, want 1", n)
			}
			// The room may keep calling after the socket is gone.
			if sess.client.Send(stateFrame(1)) {
				t.Error("Send after the connection ended must return false")
			}
			sess.client.Close(4001, "late")
		})
	}
}

func TestNoDisconnectWithoutASession(t *testing.T) {
	e := newEnv(t, nil)
	c := e.dial()
	_ = c.Close(websocket.StatusNormalClosure, "")
	eventually(t, "close metric", func() bool {
		return counter(t, e.reg, "tuttifrutti_ws_closes_total", map[string]string{"code": "1000"}) == 1
	})
	e.rooms.mu.Lock()
	defer e.rooms.mu.Unlock()
	if len(e.rooms.sessions) != 0 {
		t.Error("a session was created without a join")
	}
}

func TestSlowClientIsDisconnected(t *testing.T) {
	e := newEnv(t, func(d *Deps) { d.WriteTimeout = 200 * time.Millisecond })
	c, sess := e.dialJoined()
	_ = c // the client never reads

	big := []byte(`{"t":"state","v":1,"d":{"pad":"` + strings.Repeat("x", 256<<10) + `"}}`)
	start := time.Now()
	refused := false
	var slowestSend time.Duration
	for i := 0; i < 4000; i++ {
		s := time.Now()
		ok := sess.client.Send(big)
		if d := time.Since(s); d > slowestSend {
			slowestSend = d
		}
		if !ok {
			refused = true
			break
		}
	}
	if !refused {
		t.Fatalf("Send never reported a full queue after %v", time.Since(start))
	}
	if slowestSend > 100*time.Millisecond {
		t.Errorf("a Send call took %v: it must never block", slowestSend)
	}

	// The room reacts to a refused Send by closing the client.
	sess.client.Close(protocol.ClosePolicy, "slow client")
	sess.client.Close(protocol.ClosePolicy, "slow client")
	eventually(t, "the stalled connection to be torn down", func() bool { return sess.disconnects.Load() == 1 })
	time.Sleep(50 * time.Millisecond)
	if n := sess.disconnects.Load(); n != 1 {
		t.Errorf("Disconnect called %d times, want 1", n)
	}
	if sess.client.Send(big) {
		t.Error("Send after teardown must return false")
	}
}

func TestStalledWriterIsAbortedByWriteTimeoutWithoutAnyClose(t *testing.T) {
	e := newEnv(t, func(d *Deps) { d.WriteTimeout = 150 * time.Millisecond })
	_, sess := e.dialJoined()

	big := []byte(`{"t":"state","v":1,"d":{"pad":"` + strings.Repeat("x", 256<<10) + `"}}`)
	for i := 0; i < 4000 && sess.client.Send(big); i++ {
	}
	eventually(t, "WriteTimeout to abort the connection", func() bool { return sess.disconnects.Load() == 1 })
	if counter(t, e.reg, "tuttifrutti_ws_closes_total", map[string]string{"code": "1006"}) != 1 {
		t.Error("an aborted connection must be counted as 1006")
	}
}

func TestQueueFullPath(t *testing.T) {
	// No transport: the queue logic alone, deterministically.
	h := NewHandler(Deps{Rooms: &fakeRooms{}, Guests: newGuests()})
	c := newConn(h, nil, "p1", "127.0.0.1")

	for i := 0; i < protocol.SendQueueSize; i++ {
		if !c.Send(stateFrame(i)) {
			t.Fatalf("Send %d refused before the queue is full", i)
		}
	}
	if c.Send(stateFrame(99)) {
		t.Fatal("Send must return false when the queue is full")
	}
	if c.isClosing() {
		t.Error("a full queue alone does not close the connection: that is the room's decision")
	}
	c.Close(protocol.ClosePolicy, "slow")
	c.Close(4001, "ignored")
	if c.closeCode != protocol.ClosePolicy || c.closeReason != "slow" {
		t.Errorf("close = %d %q, want the first call to win", c.closeCode, c.closeReason)
	}
	if c.Send(stateFrame(1)) {
		t.Error("Send after Close must return false")
	}
}

func TestSendDoesNotBlockWhenTheWriterIsStuck(t *testing.T) {
	var sent atomic.Int32
	h := NewHandler(Deps{Rooms: &fakeRooms{}, Guests: newGuests()})
	c := newConn(h, nil, "p1", "127.0.0.1")
	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := 0; i < 10_000; i++ {
			c.Send(stateFrame(i))
			sent.Add(1)
		}
		c.Close(1000, "")
	}()
	select {
	case <-done:
	case <-time.After(waitLimit):
		t.Fatalf("Send blocked after %d calls", sent.Load())
	}
}

func TestPlayerIDIsStable(t *testing.T) {
	e := newEnv(t, nil)
	_, s1 := e.dialJoined()
	_, s2 := e.dialJoined()
	if s1.client.PlayerID() != s2.client.PlayerID() || s1.client.PlayerID() != e.id {
		t.Errorf("player ids = %q, %q; want both %q", s1.client.PlayerID(), s2.client.PlayerID(), e.id)
	}
}

func TestNoGoroutineLeaks(t *testing.T) {
	e := newEnv(t, func(d *Deps) {
		d.WriteTimeout = 200 * time.Millisecond
		d.TrustForwardedFor = true
		d.MaxConnsPerGuest = 100
	})
	before := connGoroutines()

	var clients []*websocket.Conn
	var sessions []*fakeSession
	for i := 0; i < 24; i++ {
		c, s := e.dialJoined(forwardedFor(fmt.Sprintf("10.0.0.%d", i)))
		clients = append(clients, c)
		sessions = append(sessions, s)
	}
	if got := connGoroutines(); got < 24*2 {
		t.Fatalf("only %d connection goroutines for 24 clients: the leak detector is blind", got)
	}

	// End them in every way.
	for i, c := range clients {
		switch i % 4 {
		case 0:
			_ = c.Close(websocket.StatusNormalClosure, "")
		case 1:
			_ = c.CloseNow()
		case 2:
			sessions[i].client.Close(protocol.CloseKicked, "")
			expectClose(t, c)
		case 3:
			sendRaw(t, c, `{"t":"x","v":9}`)
			expectClose(t, c)
		}
	}
	for i, s := range sessions {
		eventually(t, "session %d disconnect", func() bool { return s.disconnects.Load() == 1 }, i)
	}
	assertNoConnGoroutines(t)
	if got := connGoroutines(); got != before {
		t.Errorf("connection goroutines = %d, want %d", got, before)
	}
}
