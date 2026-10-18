package integration

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/AresiusXP/tutti-frutti/server/internal/game"
	"github.com/AresiusXP/tutti-frutti/server/internal/protocol"
)

// ---------------------------------------------------------------------------
// The WebSocket client.
// ---------------------------------------------------------------------------

// frame is one server frame as recorded by a client.
type frame struct {
	Seq       int
	T         string
	Raw       []byte
	State     *protocol.State
	Event     *game.Event
	Err       *protocol.ErrorBody
	Pong      *protocol.Pong
	DecodeErr error
}

func decodeFrame(seq int, raw []byte) frame {
	f := frame{Seq: seq, Raw: raw}
	var env protocol.Envelope
	if err := json.Unmarshal(raw, &env); err != nil {
		f.DecodeErr = err
		return f
	}
	f.T = env.T
	if env.V != protocol.Version {
		f.DecodeErr = fmt.Errorf("frame version %d", env.V)
		return f
	}
	var err error
	switch env.T {
	case protocol.TypeState:
		var st protocol.State
		err = json.Unmarshal(env.D, &st)
		f.State = &st
	case protocol.TypeEvent:
		var ev game.Event
		err = json.Unmarshal(env.D, &ev)
		f.Event = &ev
	case protocol.TypeError:
		var eb protocol.ErrorBody
		err = json.Unmarshal(env.D, &eb)
		f.Err = &eb
	case protocol.TypePong:
		var pg protocol.Pong
		err = json.Unmarshal(env.D, &pg)
		f.Pong = &pg
	default:
		err = fmt.Errorf("unexpected server frame type %q", env.T)
	}
	f.DecodeErr = err
	return f
}

func (f frame) String() string {
	raw := string(f.Raw)
	if len(raw) > 160 {
		raw = raw[:160] + "..."
	}
	return fmt.Sprintf("#%d %s", f.Seq, raw)
}

func isState(f frame) bool { return f.State != nil }

func isEvent(typ game.EventType) func(frame) bool {
	return func(f frame) bool { return f.Event != nil && f.Event.Type == typ }
}

func isError(code string) func(frame) bool {
	return func(f frame) bool { return f.Err != nil && f.Err.Code == code }
}

// client is one WebSocket connection with everything it received recorded.
type client struct {
	t    *testing.T
	p    *player
	conn *websocket.Conn

	mu          sync.Mutex
	frames      []frame
	notify      chan struct{} // closed and replaced whenever something changes
	closed      bool
	closeCode   int // -1 when the connection ended without a close frame
	closeReason string
	done        chan struct{}
}

// dialWith opens the WebSocket with an explicit cookie and Origin (either may
// be empty to omit the header). On a refused upgrade it returns the response.
func (s *stack) dialWith(t *testing.T, p *player, cookie, origin string, opts ...func(http.Header)) (*client, *http.Response, error) {
	h := http.Header{}
	if cookie != "" {
		h.Set("Cookie", cookie)
	}
	if origin != "" {
		h.Set("Origin", origin)
	}
	n := s.dialSeq.Add(1)
	h.Set("X-Forwarded-For", fmt.Sprintf("10.%d.%d.%d", n>>16&0xff, n>>8&0xff, n&0xff))
	for _, o := range opts {
		o(h)
	}
	ctx, cancel := context.WithTimeout(context.Background(), waitLimit)
	defer cancel()
	conn, resp, err := websocket.Dial(ctx, s.wsURL(), &websocket.DialOptions{HTTPHeader: h})
	if err != nil {
		return nil, resp, err
	}
	conn.SetReadLimit(1 << 20)
	c := &client{
		t: t, p: p, conn: conn,
		notify: make(chan struct{}), done: make(chan struct{}), closeCode: -1,
	}
	go c.readLoop()
	t.Cleanup(func() { _ = conn.CloseNow(); <-c.done })
	return c, resp, nil
}

func (s *stack) tryConnect(p *player) (*client, error) {
	c, resp, err := s.dialWith(s.t, p, p.cookie, s.origin)
	if err != nil {
		status := 0
		if resp != nil {
			status = resp.StatusCode
		}
		return nil, fmt.Errorf("dial as %s: %w (status %d)", p.name, err, status)
	}
	return c, nil
}

// connect opens a WebSocket as p (guest cookie + the allowed Origin).
func (s *stack) connect(p *player) *client {
	s.t.Helper()
	c, err := s.tryConnect(p)
	if err != nil {
		s.t.Fatal(err)
	}
	return c
}

func (c *client) signalLocked() {
	close(c.notify)
	c.notify = make(chan struct{})
}

func (c *client) readLoop() {
	for {
		typ, data, err := c.conn.Read(context.Background())
		if err != nil {
			var ce websocket.CloseError
			reason := ""
			if errors.As(err, &ce) {
				reason = ce.Reason
			}
			c.mu.Lock()
			c.closed = true
			c.closeCode = int(websocket.CloseStatus(err))
			c.closeReason = reason
			c.signalLocked()
			c.mu.Unlock()
			close(c.done)
			return
		}
		c.mu.Lock()
		f := decodeFrame(len(c.frames), data)
		if typ != websocket.MessageText {
			f.DecodeErr = fmt.Errorf("non-text server frame (type %v)", typ)
		}
		c.frames = append(c.frames, f)
		c.signalLocked()
		c.mu.Unlock()
	}
}

func (c *client) writeRaw(b []byte) error {
	ctx, cancel := context.WithTimeout(context.Background(), waitLimit)
	defer cancel()
	return c.conn.Write(ctx, websocket.MessageText, b)
}

func (c *client) trySend(t string, d any) error {
	env := protocol.Envelope{T: t, V: protocol.Version}
	if d != nil {
		raw, err := json.Marshal(d)
		if err != nil {
			return err
		}
		env.D = raw
	}
	b, err := json.Marshal(env)
	if err != nil {
		return err
	}
	return c.writeRaw(b)
}

// send writes one client frame {"t":t,"v":protocol.Version,"d":d}.
func (c *client) send(t string, d any) {
	c.t.Helper()
	if err := c.trySend(t, d); err != nil {
		c.t.Fatalf("%s: send %s: %v", c.p.name, t, err)
	}
}

// sendRaw writes arbitrary text.
func (c *client) sendRaw(s string) {
	c.t.Helper()
	if err := c.writeRaw([]byte(s)); err != nil {
		c.t.Fatalf("%s: send raw: %v", c.p.name, err)
	}
}

func (c *client) joinRequest(code string) protocol.JoinRequest {
	return protocol.JoinRequest{Code: code, Name: c.p.name, Avatar: c.p.avatar}
}

// tryJoin sends "join" and waits for the first state frame after it.
func (c *client) tryJoin(code string) (*protocol.State, error) {
	m := c.mark()
	if err := c.trySend(game.CmdJoin, c.joinRequest(code)); err != nil {
		return nil, err
	}
	f, err := c.await(m, "state after join", func(f frame) bool {
		return f.State != nil || (f.Err != nil && f.Err.Ref == game.CmdJoin)
	})
	if err != nil {
		return nil, err
	}
	if f.Err != nil {
		return nil, fmt.Errorf("%s: join refused: %s", c.p.name, f.Err.Code)
	}
	return f.State, nil
}

// join sends "join" and waits for the room's first state frame.
func (c *client) join(code string) *protocol.State {
	c.t.Helper()
	st, err := c.tryJoin(code)
	if err != nil {
		c.t.Fatal(err)
	}
	return st
}

// mark is the number of frames recorded so far (an index for waitSince).
func (c *client) mark() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.frames)
}

func (c *client) isClosed() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.closed
}

func (c *client) snapshotFrames() []frame {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]frame(nil), c.frames...)
}

func (c *client) dumpLocked() string {
	var b strings.Builder
	start := max(0, len(c.frames)-8)
	for _, f := range c.frames[start:] {
		b.WriteString("    " + f.String() + "\n")
	}
	if c.closed {
		fmt.Fprintf(&b, "    (closed: code %d %q)\n", c.closeCode, c.closeReason)
	}
	return b.String()
}

// await waits for a frame at index >= since that satisfies pred.
func (c *client) await(since int, desc string, pred func(frame) bool) (frame, error) {
	timeout := time.NewTimer(waitLimit)
	defer timeout.Stop()
	i := since
	for {
		c.mu.Lock()
		for ; i < len(c.frames); i++ {
			if c.frames[i].DecodeErr != nil {
				err := fmt.Errorf("%s: undecodable server frame %s: %v", c.p.name, c.frames[i], c.frames[i].DecodeErr)
				c.mu.Unlock()
				return frame{}, err
			}
			if pred(c.frames[i]) {
				f := c.frames[i]
				c.mu.Unlock()
				return f, nil
			}
		}
		closed, ch := c.closed, c.notify
		if closed {
			err := fmt.Errorf("%s: connection closed while waiting for %s; last frames:\n%s", c.p.name, desc, c.dumpLocked())
			c.mu.Unlock()
			return frame{}, err
		}
		c.mu.Unlock()
		select {
		case <-ch:
		case <-timeout.C:
			c.mu.Lock()
			err := fmt.Errorf("%s: timed out waiting for %s; last frames:\n%s", c.p.name, desc, c.dumpLocked())
			c.mu.Unlock()
			return frame{}, err
		}
	}
}

// waitFor waits for any frame (from the start of the connection) matching pred.
func (c *client) waitFor(desc string, pred func(frame) bool) frame {
	c.t.Helper()
	return c.waitSince(0, desc, pred)
}

// waitSince waits for a frame recorded at index >= since matching pred.
func (c *client) waitSince(since int, desc string, pred func(frame) bool) frame {
	c.t.Helper()
	f, err := c.await(since, desc, pred)
	if err != nil {
		c.t.Fatal(err)
	}
	return f
}

// awaitState waits until the latest state frame satisfies pred.
func (c *client) awaitState(desc string, pred func(*protocol.State) bool) (*protocol.State, error) {
	timeout := time.NewTimer(waitLimit)
	defer timeout.Stop()
	for {
		c.mu.Lock()
		var latest *protocol.State
		for i := len(c.frames) - 1; i >= 0; i-- {
			if c.frames[i].DecodeErr != nil {
				err := fmt.Errorf("%s: undecodable server frame %s: %v", c.p.name, c.frames[i], c.frames[i].DecodeErr)
				c.mu.Unlock()
				return nil, err
			}
			if c.frames[i].State != nil {
				latest = c.frames[i].State
				break
			}
		}
		if latest != nil && pred(latest) {
			c.mu.Unlock()
			return latest, nil
		}
		closed, ch := c.closed, c.notify
		if closed {
			err := fmt.Errorf("%s: connection closed while waiting for state %s; last frames:\n%s", c.p.name, desc, c.dumpLocked())
			c.mu.Unlock()
			return nil, err
		}
		c.mu.Unlock()
		select {
		case <-ch:
		case <-timeout.C:
			c.mu.Lock()
			err := fmt.Errorf("%s: timed out waiting for state %s; last frames:\n%s", c.p.name, desc, c.dumpLocked())
			c.mu.Unlock()
			return nil, err
		}
	}
}

// waitState waits until the latest state satisfies pred and returns it.
func (c *client) waitState(desc string, pred func(*protocol.State) bool) *protocol.State {
	c.t.Helper()
	st, err := c.awaitState(desc, pred)
	if err != nil {
		c.t.Fatal(err)
	}
	return st
}

// lastState is the most recent state frame (nil before the first one).
func (c *client) lastState() *protocol.State {
	c.mu.Lock()
	defer c.mu.Unlock()
	for i := len(c.frames) - 1; i >= 0; i-- {
		if c.frames[i].State != nil {
			return c.frames[i].State
		}
	}
	return nil
}

// view is the latest state's view; it fails the test before the first state.
func (c *client) view() game.View {
	c.t.Helper()
	st := c.lastState()
	if st == nil {
		c.t.Fatalf("%s: no state received yet", c.p.name)
	}
	return st.View
}

// events returns the event frames recorded at index >= since.
func (c *client) events(since int) []game.Event {
	c.mu.Lock()
	defer c.mu.Unlock()
	var out []game.Event
	for _, f := range c.frames[min(since, len(c.frames)):] {
		if f.Event != nil {
			out = append(out, *f.Event)
		}
	}
	return out
}

// tryExpectClose waits for the connection to end and checks the close code.
func (c *client) tryExpectClose(code int) error {
	select {
	case <-c.done:
	case <-time.After(waitLimit):
		c.mu.Lock()
		defer c.mu.Unlock()
		return fmt.Errorf("%s: connection still open, want close %d; last frames:\n%s", c.p.name, code, c.dumpLocked())
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closeCode != code {
		return fmt.Errorf("%s: close code = %d (%q), want %d", c.p.name, c.closeCode, c.closeReason, code)
	}
	return nil
}

// expectClose waits for the server to close the connection with code.
func (c *client) expectClose(code int) {
	c.t.Helper()
	if err := c.tryExpectClose(code); err != nil {
		c.t.Fatal(err)
	}
}

// drop closes the socket abruptly, like a phone losing its connection.
func (c *client) drop() {
	_ = c.conn.CloseNow()
	<-c.done
}

// roundTrip sends a command and waits for the room's reply: a state frame
// (accepted, returns "") or an error frame referring to it (returns its code).
func (c *client) roundTrip(typ string, payload any) string {
	c.t.Helper()
	m := c.mark()
	c.send(typ, payload)
	f := c.waitSince(m, "reply to "+typ, func(f frame) bool {
		return f.State != nil || (f.Err != nil && f.Err.Ref == typ)
	})
	if f.Err != nil {
		return f.Err.Code
	}
	return ""
}

// tryRoundTrip is roundTrip for goroutines other than the test's: it reports
// failures as errors instead of failing the test.
func (c *client) tryRoundTrip(typ string, payload any) (string, error) {
	m := c.mark()
	if err := c.trySend(typ, payload); err != nil {
		return "", err
	}
	f, err := c.await(m, "reply to "+typ, func(f frame) bool {
		return f.State != nil || (f.Err != nil && f.Err.Ref == typ)
	})
	if err != nil {
		return "", err
	}
	if f.Err != nil {
		return f.Err.Code, nil
	}
	return "", nil
}

// expectPong pings and waits for the pong.
func (c *client) expectPong() protocol.Pong {
	c.t.Helper()
	m := c.mark()
	c.send(protocol.TypePing, nil)
	f := c.waitSince(m, "pong", func(f frame) bool { return f.Pong != nil })
	return *f.Pong
}
