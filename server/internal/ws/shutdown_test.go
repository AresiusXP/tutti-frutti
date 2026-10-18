package ws

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/AresiusXP/tutti-frutti/server/internal/game"
	"github.com/AresiusXP/tutti-frutti/server/internal/protocol"
)

// outcome is what a client saw until its connection ended.
type outcome struct {
	frames []protocol.Envelope
	code   int
	reason string
	err    error
}

// watch reads c in the background until it closes, like a browser that keeps
// answering the close handshake.
func watch(c *websocket.Conn) <-chan outcome {
	ch := make(chan outcome, 1)
	go func() {
		var out outcome
		for {
			ctx, cancel := context.WithTimeout(context.Background(), waitLimit)
			_, data, err := c.Read(ctx)
			cancel()
			if err != nil {
				if ce, ok := asCloseError(err); ok {
					out.code, out.reason = int(ce.Code), ce.Reason
				} else {
					out.err = err
				}
				ch <- out
				return
			}
			var env protocol.Envelope
			if err := json.Unmarshal(data, &env); err != nil {
				out.err = err
				ch <- out
				return
			}
			out.frames = append(out.frames, env)
		}
	}()
	return ch
}

func await(t *testing.T, name string, ch <-chan outcome) outcome {
	t.Helper()
	select {
	case out := <-ch:
		if out.err != nil {
			t.Fatalf("%s: %v", name, out.err)
		}
		return out
	case <-time.After(2 * waitLimit):
		t.Fatalf("%s: never closed", name)
		return outcome{}
	}
}

func restartEvents(frames []protocol.Envelope) int {
	n := 0
	for _, f := range frames {
		var ev game.Event
		if f.T == protocol.TypeEvent && json.Unmarshal(f.D, &ev) == nil && ev.Type == protocol.EventServerRestarting {
			n++
		}
	}
	return n
}

func shutdownIn(h *Handler, d time.Duration) <-chan error {
	ch := make(chan error, 1)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), d)
		defer cancel()
		ch <- h.Shutdown(ctx)
	}()
	return ch
}

func TestShutdownDrainsSocketsInEveryState(t *testing.T) {
	e := newEnv(t, func(d *Deps) { d.MaxConnsPerGuest = 100 })

	unjoined := e.dial()
	joined, sessJoined := e.dialJoined()
	informed, sessInformed := e.dialJoined()
	closing, sessClosing := e.dialJoined()

	// The room layer already told this one (Hub.Shutdown ran first)...
	if !sessInformed.client.Send([]byte(`{"t":"event","v":1,"d":{"type":"server_restarting"}}`)) {
		t.Fatal("Send refused")
	}
	// ...and this one is mid-close for another reason.
	sessClosing.client.Send(stateFrame(1))
	sessClosing.client.Close(protocol.CloseKicked, "kicked")

	outs := map[string]<-chan outcome{
		"unjoined": watch(unjoined), "joined": watch(joined), "informed": watch(informed), "closing": watch(closing),
	}
	done := shutdownIn(e.h, 10*time.Second)
	if err := <-done; err != nil {
		t.Fatalf("Shutdown = %v", err)
	}

	for _, name := range []string{"unjoined", "joined", "informed"} {
		out := await(t, name, outs[name])
		if out.code != protocol.CloseServiceRestart || out.reason != "server restarting" {
			t.Errorf("%s: close = %d %q, want 1012 server restarting", name, out.code, out.reason)
		}
		if n := restartEvents(out.frames); n != 1 {
			t.Errorf("%s: received %d server_restarting events (%d frames), want exactly 1", name, n, len(out.frames))
		}
		if len(out.frames) != 1 {
			t.Errorf("%s: %d frames, want only the event", name, len(out.frames))
		}
	}
	out := await(t, "closing", outs["closing"])
	if out.code != protocol.CloseKicked || out.reason != "kicked" {
		t.Errorf("a socket already closing must keep its code: %d %q", out.code, out.reason)
	}
	if len(out.frames) != 1 || out.frames[0].T != protocol.TypeState {
		t.Errorf("closing: frames = %+v, want its queued state frame flushed", out.frames)
	}

	// Shutdown returned: every pump is gone and every session was told.
	if n := e.h.Open(); n != 0 {
		t.Errorf("Open = %d after Shutdown", n)
	}
	assertNoConnGoroutines(t)
	for name, s := range map[string]*fakeSession{"joined": sessJoined, "informed": sessInformed, "closing": sessClosing} {
		if n := s.disconnects.Load(); n != 1 {
			t.Errorf("%s: Disconnect called %d times, want 1", name, n)
		}
	}
	if g := gauge(t, e.reg, "tuttifrutti_ws_connections_open"); g != 0 {
		t.Errorf("open gauge = %v", g)
	}
	if got := counter(t, e.reg, "tuttifrutti_ws_closes_total", map[string]string{"code": "1012"}); got != 3 {
		t.Errorf("1012 closes counted = %v, want 3", got)
	}

	// Idempotent.
	start := time.Now()
	if err := e.h.Shutdown(context.Background()); err != nil {
		t.Errorf("second Shutdown = %v", err)
	}
	if time.Since(start) > time.Second {
		t.Error("second Shutdown was not immediate")
	}

	// No new upgrades afterwards.
	_, resp, err := e.tryDial()
	if err == nil || resp == nil || resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("upgrade after Shutdown: err=%v resp=%v, want 503", err, resp)
	}
	if resp.Header.Get("Retry-After") != "5" {
		t.Errorf("Retry-After = %q, want 5", resp.Header.Get("Retry-After"))
	}
}

func TestShutdownWithNoSockets(t *testing.T) {
	e := newEnv(t, nil)
	if err := e.h.Shutdown(context.Background()); err != nil {
		t.Fatalf("Shutdown = %v", err)
	}
	if err := e.h.Shutdown(context.Background()); err != nil {
		t.Fatalf("second Shutdown = %v", err)
	}
}

func TestShutdownAfterTheRoomsAlreadyClosedEverything(t *testing.T) {
	// cmd/server runs Hub.Shutdown first: most sockets are already closing
	// with 1012 when Handler.Shutdown starts.
	e := newEnv(t, func(d *Deps) { d.MaxConnsPerGuest = 100 })
	var outs []<-chan outcome
	var sessions []*fakeSession
	for i := 0; i < 5; i++ {
		c, s := e.dialJoined()
		s.client.Send([]byte(`{"t":"event","v":1,"d":{"type":"server_restarting"}}`))
		s.client.Close(protocol.CloseServiceRestart, "server restarting")
		outs = append(outs, watch(c))
		sessions = append(sessions, s)
	}
	if err := <-shutdownIn(e.h, 10*time.Second); err != nil {
		t.Fatalf("Shutdown = %v", err)
	}
	for i, ch := range outs {
		out := await(t, "client", ch)
		if out.code != protocol.CloseServiceRestart || restartEvents(out.frames) != 1 {
			t.Errorf("client %d: code %d, %d restart events", i, out.code, restartEvents(out.frames))
		}
		if n := sessions[i].disconnects.Load(); n != 1 {
			t.Errorf("client %d: Disconnect called %d times", i, n)
		}
	}
	if e.h.Open() != 0 {
		t.Errorf("Open = %d", e.h.Open())
	}
}

func TestShutdownIsSafeToCallConcurrently(t *testing.T) {
	e := newEnv(t, func(d *Deps) { d.MaxConnsPerGuest = 100 })
	var outs []<-chan outcome
	for i := 0; i < 6; i++ {
		c, _ := e.dialJoined()
		outs = append(outs, watch(c))
	}
	var wg sync.WaitGroup
	errs := make(chan error, 8)
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			errs <- e.h.Shutdown(ctx)
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Errorf("Shutdown = %v", err)
		}
	}
	for _, ch := range outs {
		if out := await(t, "client", ch); out.code != protocol.CloseServiceRestart || restartEvents(out.frames) != 1 {
			t.Errorf("code %d with %d restart events, want 1012 and exactly one", out.code, restartEvents(out.frames))
		}
	}
}

func TestShutdownGivesUpWhenTheContextExpires(t *testing.T) {
	// The grace is long: only Shutdown's own expiry can release the stalled
	// client (which never reads, so never answers the close).
	e := newEnv(t, nil, func(h *Handler) { h.closeGrace = time.Minute })
	_, sess := e.dialJoined()
	e.dial() // an unjoined staller as well

	start := time.Now()
	ctx, cancel := context.WithTimeout(context.Background(), 250*time.Millisecond)
	defer cancel()
	err := e.h.Shutdown(ctx)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Shutdown = %v, want context deadline exceeded", err)
	}
	if d := time.Since(start); d > 2*time.Second {
		t.Errorf("Shutdown took %v with a 250ms context", d)
	}

	// The remaining connections are torn down anyway.
	eventually(t, "stalled sockets to be aborted", func() bool { return e.h.Open() == 0 })
	if n := sess.disconnects.Load(); n != 1 {
		t.Errorf("Disconnect called %d times, want 1", n)
	}
	assertNoConnGoroutines(t)
}

func TestShutdownWaitsForAdmittedUpgradesThatHaveNotRegisteredYet(t *testing.T) {
	e := newEnv(t, nil)
	if e.h.acquire("g") != admitOK { // an upgrade between admission and Accept
		t.Fatal("acquire refused")
	}
	done := shutdownIn(e.h, 10*time.Second)
	select {
	case err := <-done:
		t.Fatalf("Shutdown returned (%v) while an admitted upgrade was in flight", err)
	case <-time.After(100 * time.Millisecond):
	}
	if e.h.acquire("g2") != admitBusy {
		t.Error("new admissions must be refused once Shutdown started")
	}
	e.h.release("g")
	select {
	case err := <-done:
		if err != nil {
			t.Errorf("Shutdown = %v", err)
		}
	case <-time.After(waitLimit):
		t.Fatal("Shutdown did not return after the last slot was released")
	}
}

func TestRegisterDuringShutdownReportsIt(t *testing.T) {
	e := newEnv(t, nil)
	c := newConn(e.h, nil, "p", "127.0.0.1")
	if shuttingDown, forced := e.h.register(c); shuttingDown || forced {
		t.Errorf("register reported shuttingDown=%v forced=%v before any Shutdown", shuttingDown, forced)
	}
	e.h.unregister(c)
	if err := e.h.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
	shuttingDown, forced := e.h.register(c)
	if !shuttingDown {
		t.Error("register must tell the upgrade that raced with Shutdown to drain itself")
	}
	if forced {
		t.Error("a Shutdown that completed in time must not force late registrations")
	}
	e.h.unregister(c)
	c.shutdown() // as ServeHTTP does: queues the event and closes
	if c.closeCode != protocol.CloseServiceRestart || len(c.queue) != 1 {
		t.Errorf("code %d, queued %d", c.closeCode, len(c.queue))
	}
}

// An upgrade admitted before Shutdown can finish Accept after Shutdown gave up
// waiting (its context expired). It is missing from the forced-teardown
// snapshot, so register must tell it to tear itself down at once instead of
// starting the polite close grace.
func TestRegisterAfterShutdownDeadlineIsForced(t *testing.T) {
	e := newEnv(t, nil)
	// One admitted upgrade that never registers keeps Shutdown waiting.
	if e.h.acquire("g") != admitOK {
		t.Fatal("acquire")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if err := e.h.Shutdown(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Shutdown = %v, want deadline exceeded", err)
	}
	c := newConn(e.h, nil, "p", "127.0.0.1")
	shuttingDown, forced := e.h.register(c)
	if !shuttingDown || !forced {
		t.Errorf("late register: shuttingDown=%v forced=%v, want both true", shuttingDown, forced)
	}
	e.h.unregister(c)
	e.h.release("g")
}

func TestShutdownRacingWithUpgrades(t *testing.T) {
	e := newEnv(t, func(d *Deps) {
		d.MaxConnsPerGuest = 1000
		d.MaxConns = 1000
		d.UpgradeRate = 1e6
		d.UpgradeBurst = 1 << 20
	})
	const clients = 60
	var wg sync.WaitGroup
	results := make(chan string, clients)
	start := make(chan struct{})
	for i := 0; i < clients; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			ctx, cancel := context.WithTimeout(context.Background(), 3*waitLimit)
			defer cancel()
			h := http.Header{}
			h.Set("Cookie", (&http.Cookie{Name: e.cookie.Name, Value: e.cookie.Value}).String())
			h.Set("Origin", testOrigin)
			c, resp, err := websocket.Dial(ctx, e.url(), &websocket.DialOptions{HTTPHeader: h})
			if err != nil {
				if resp != nil && resp.StatusCode == http.StatusServiceUnavailable {
					results <- "refused"
				} else {
					results <- "dial error: " + err.Error()
				}
				return
			}
			defer c.CloseNow()
			_ = c.Write(ctx, websocket.MessageText, []byte(joinJSON))
			for {
				_, _, err := c.Read(ctx)
				if err != nil {
					if ce, ok := asCloseError(err); ok && ce.Code == protocol.CloseServiceRestart {
						results <- "drained"
					} else {
						results <- "unexpected end: " + err.Error()
					}
					return
				}
			}
		}()
	}
	close(start)
	time.Sleep(15 * time.Millisecond)
	if err := e.h.Shutdown(context.Background()); err != nil {
		t.Fatalf("Shutdown = %v", err)
	}
	wg.Wait()
	close(results)
	counts := map[string]int{}
	for r := range results {
		counts[r]++
	}
	for r, n := range counts {
		if r != "refused" && r != "drained" {
			t.Errorf("%d clients: %s", n, r)
		}
	}
	if counts["refused"]+counts["drained"] != clients {
		t.Errorf("outcomes = %v", counts)
	}
	if n := e.h.Open(); n != 0 {
		t.Errorf("Open = %d, want 0", n)
	}
	assertNoConnGoroutines(t)
}
