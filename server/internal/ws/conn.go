package ws

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/coder/websocket"
	"golang.org/x/time/rate"

	"github.com/AresiusXP/tutti-frutti/server/internal/game"
	"github.com/AresiusXP/tutti-frutti/server/internal/protocol"
	"github.com/AresiusXP/tutti-frutti/server/internal/room"
)

const (
	// maxMalformed is the number of malformed frames after which the
	// connection is closed with ClosePolicy.
	maxMalformed = 10
	// maxCloseReason is the longest close reason the WebSocket protocol allows.
	maxCloseReason = 123
)

// conn is one accepted WebSocket. It implements room.Client.
//
// Concurrency model: the HTTP handler goroutine is the only reader (run);
// a single writer goroutine (writeLoop) owns every write, including the close
// frame, so a frame handed to Send can never be written after it. Send and
// Close only touch the queue and flags under mu and never block.
//
// Every read uses ctx, which Close arms to be cancelled after the handler's
// closeGrace: cancelling it makes the library tear the connection down even
// when the reader is blocked in the middle of a frame, so no timer expiry or
// close request can leave a goroutine stuck on a stalled peer.
type conn struct {
	h        *Handler
	ws       *websocket.Conn
	playerID string
	ip       string
	log      *slog.Logger

	ctx    context.Context // bounds every read
	cancel context.CancelFunc

	queue chan []byte

	mu          sync.Mutex
	closed      bool   // Send is refused from now on
	closeCode   int    // the code passed to Close, 0 when Close was never called
	closeReason string // idem
	closeReq    chan struct{}
	grace       *time.Timer // cancels ctx closeGrace after Close
	restartSeen bool        // an event server_restarting is queued or was sent

	dead     chan struct{} // closed by abort: the transport is gone, stop writing
	deadOnce sync.Once

	writerDone chan struct{}
	idle       *time.Timer
	joinTimer  *time.Timer

	// Reader-goroutine state.
	session  room.Session
	limiter  *rate.Limiter
	badCount int
	peerCode int // close code sent by the peer, 0 if none
}

func newConn(h *Handler, ws *websocket.Conn, playerID, ip string) *conn {
	ctx, cancel := context.WithCancel(context.Background())
	return &conn{
		ctx:        ctx,
		cancel:     cancel,
		h:          h,
		ws:         ws,
		playerID:   playerID,
		ip:         ip,
		log:        h.log.With("ip", ip, "player", playerID),
		queue:      make(chan []byte, protocol.SendQueueSize),
		closeReq:   make(chan struct{}),
		dead:       make(chan struct{}),
		limiter:    rate.NewLimiter(protocol.ClientRatePerSecond, protocol.ClientBurst),
		writerDone: make(chan struct{}),
	}
}

// PlayerID implements room.Client.
func (c *conn) PlayerID() string { return c.playerID }

// Send implements room.Client: it enqueues frame without blocking and reports
// false when the queue is full or the connection is closing. The frame must
// not be modified afterwards.
func (c *conn) Send(frame []byte) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return false
	}
	select {
	case c.queue <- frame:
		if isRestartEvent(frame) {
			c.restartSeen = true
		}
		return true
	default:
		return false
	}
}

// Close implements room.Client. It is idempotent and never blocks: the writer
// goroutine flushes the frames queued so far, writes the close frame and tears
// the connection down. After Close returns, Send refuses new frames. A peer
// that does not complete the close within the handler's closeGrace is cut off.
func (c *conn) Close(code int, reason string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return
	}
	c.closed = true
	c.closeCode = sanitizeCloseCode(code)
	c.closeReason = truncateReason(reason)
	c.grace = time.AfterFunc(c.h.closeGrace, c.cancel)
	close(c.closeReq)
}

// shutdown drains the connection for a server shutdown: it queues event
// server_restarting unless one was already sent, then closes with
// CloseServiceRestart. A connection that is already closing keeps its code.
func (c *conn) shutdown() {
	c.mu.Lock()
	if !c.closed && !c.restartSeen {
		select {
		case c.queue <- c.h.restartFrame:
			c.restartSeen = true
		default:
		}
	}
	c.mu.Unlock()
	c.Close(protocol.CloseServiceRestart, "server restarting")
}

var (
	restartEventPrefix = []byte(`{"t":"event"`)
	restartEventType   = []byte(`"type":"server_restarting"`)
)

// isRestartEvent recognises the event server_restarting frame the room layer
// broadcasts, so shutdown does not send a second one.
func isRestartEvent(frame []byte) bool {
	return bytes.HasPrefix(frame, restartEventPrefix) && bytes.Contains(frame, restartEventType)
}

func (c *conn) isClosing() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.closed
}

// abort marks the transport as gone: Send is refused, the writer stops and the
// socket is torn down without a close handshake.
func (c *conn) abort() {
	c.mu.Lock()
	c.closed = true
	c.mu.Unlock()
	c.deadOnce.Do(func() { close(c.dead) })
	c.cancel()
	_ = c.ws.CloseNow()
}

// writeLoop is the single writer goroutine.
func (c *conn) writeLoop() {
	defer close(c.writerDone)
	for {
		select {
		case f := <-c.queue:
			if !c.writeFrame(f) {
				c.abort()
				return
			}
		case <-c.closeReq:
			c.flushAndClose()
			return
		case <-c.dead:
			return
		}
	}
}

func (c *conn) writeFrame(frame []byte) bool {
	ctx, cancel := context.WithTimeout(context.Background(), c.h.writeTimeout)
	defer cancel()
	if err := c.ws.Write(ctx, websocket.MessageText, frame); err != nil {
		return false
	}
	c.h.m.frameOut(frame)
	return true
}

// flushAndClose writes what was queued before Close (within one WriteTimeout
// for the whole backlog), then the close frame, and waits for the handshake.
func (c *conn) flushAndClose() {
	c.mu.Lock()
	code, reason := c.closeCode, c.closeReason
	c.mu.Unlock()

	ctx, cancel := context.WithTimeout(context.Background(), c.h.writeTimeout)
	defer cancel()
	for {
		select {
		case f := <-c.queue:
			if err := c.ws.Write(ctx, websocket.MessageText, f); err != nil {
				c.abort()
				return
			}
			c.h.m.frameOut(f)
			continue
		default:
		}
		break
	}
	if err := c.ws.Close(websocket.StatusCode(code), reason); err != nil {
		c.log.Debug("ws close handshake failed", "err", err)
	}
}

// run reads frames until the connection ends, then tears everything down. It
// runs on the HTTP handler goroutine.
func (c *conn) run() {
	c.idle = time.AfterFunc(c.h.readIdleTimeout, func() { c.Close(int(websocket.StatusGoingAway), "idle") })
	c.joinTimer = time.AfterFunc(c.h.joinTimeout, func() { c.Close(protocol.ClosePolicy, "join timeout") })
	go c.writeLoop()
	c.log.Debug("ws connected")

	graceful := false
	defer func() { c.teardown(graceful) }()
	graceful = c.readLoop()
}

// teardown stops the timers and the writer, reports the disconnect to the room
// (exactly once, since it runs once per connection) and records the metrics.
func (c *conn) teardown(graceful bool) {
	c.idle.Stop()
	c.joinTimer.Stop()
	if !graceful {
		c.abort()
	}
	<-c.writerDone

	// From here on Close is a no-op, so no timer can be armed after the stop.
	c.mu.Lock()
	c.closed = true
	if c.grace != nil {
		c.grace.Stop()
	}
	c.mu.Unlock()
	c.cancel()

	if c.session != nil {
		c.session.Disconnect()
	}

	c.mu.Lock()
	code := c.closeCode
	c.mu.Unlock()
	if code == 0 {
		code = c.peerCode
	}
	if code == 0 {
		code = 1006
	}
	c.h.m.closed(code)
	c.log.Debug("ws closed", "code", code)
}

// readLoop returns true when it stopped on purpose after starting a close
// (the writer finishes the handshake), false when the transport failed.
func (c *conn) readLoop() (graceful bool) {
	for {
		typ, data, tooBig, err := c.readMessage()
		if err != nil {
			if status := websocket.CloseStatus(err); status != -1 {
				c.peerCode = int(status)
			}
			return false
		}
		c.idle.Reset(c.h.readIdleTimeout)
		if c.isClosing() {
			return true
		}
		if tooBig {
			c.sendError(protocol.ErrMessageTooLarge, "")
			c.Close(protocol.CloseTooBig, "message too large")
			return true
		}
		if !c.limiter.AllowN(c.h.now(), 1) {
			c.sendError(protocol.ErrRateLimited, "")
			c.Close(protocol.ClosePolicy, "rate limited")
			return true
		}
		if c.handleFrame(typ, data) {
			return true
		}
	}
}

// readMessage reads one message of at most MaxMessageBytes; a longer one is
// reported with tooBig and the rest of it is left unread (the close handshake
// discards it), so memory stays bounded.
func (c *conn) readMessage() (typ websocket.MessageType, data []byte, tooBig bool, err error) {
	typ, r, err := c.ws.Reader(c.ctx)
	if err != nil {
		return 0, nil, false, err
	}
	// The header is in: the rest of the frame must follow promptly.
	frame := time.AfterFunc(c.h.frameTimeout, func() { c.Close(protocol.ClosePolicy, "frame timeout") })
	data, err = io.ReadAll(io.LimitReader(r, protocol.MaxMessageBytes+1))
	frame.Stop()
	if err != nil {
		return 0, nil, false, err
	}
	return typ, data, len(data) > protocol.MaxMessageBytes, nil
}

// handleFrame processes one client message; it returns true when the
// connection is being closed and the read loop must stop.
func (c *conn) handleFrame(typ websocket.MessageType, data []byte) (stop bool) {
	if typ != websocket.MessageText {
		c.h.m.frameIn(otherLabel)
		return c.malformed("")
	}
	var env protocol.Envelope
	if err := json.Unmarshal(data, &env); err != nil || env.T == "" {
		c.h.m.frameIn(otherLabel)
		return c.malformed("")
	}
	c.h.m.frameIn(env.T)

	if env.V != protocol.Version {
		c.sendError(protocol.ErrUnsupportedVersion, env.T)
		c.Close(protocol.CloseUnsupportedVersion, "unsupported version")
		return true
	}
	if env.T == protocol.TypePing {
		c.sendFrame(protocol.TypePong, protocol.Pong{ServerTime: c.h.now().UnixMilli()})
		return false
	}
	if c.session == nil {
		return c.join(env)
	}
	c.session.Handle(env.T, env.D)
	return false
}

// join handles the first non-ping frame, which must be "join".
func (c *conn) join(env protocol.Envelope) (stop bool) {
	if env.T != game.CmdJoin {
		c.sendError(protocol.ErrNotJoined, env.T)
		return false
	}
	var req struct {
		Code string `json:"code"`
	}
	if err := json.Unmarshal(env.D, &req); err != nil {
		return c.malformed(env.T)
	}
	code, ok := normalizeCode(req.Code)
	if !ok {
		c.sendError(protocol.ErrRoomNotFound, env.T)
		c.Close(protocol.CloseRoomClosed, "room not found")
		return true
	}

	sess, err := c.h.rooms.Connect(code, c)
	if err != nil {
		return c.connectFailed(env.T, err)
	}
	c.session = sess
	c.joinTimer.Stop()
	sess.Handle(env.T, env.D)
	return false
}

func (c *conn) connectFailed(ref string, err error) (stop bool) {
	switch {
	case errors.Is(err, room.ErrRoomNotFound):
		c.sendError(protocol.ErrRoomNotFound, ref)
		c.Close(protocol.CloseRoomClosed, "room not found")
	case errors.Is(err, room.ErrRoomClosed):
		c.sendError(protocol.ErrRoomClosed, ref)
		c.Close(protocol.CloseRoomClosed, "room closed")
	case errors.Is(err, room.ErrShuttingDown), errors.Is(err, room.ErrTooManyRooms):
		c.sendError(protocol.ErrServerBusy, ref)
		c.Close(protocol.CloseServiceRestart, "server busy")
	default:
		c.log.Error("ws connect failed", "err", err)
		c.sendError("internal", ref)
		c.Close(int(websocket.StatusInternalError), "internal error")
	}
	return true
}

// malformed counts a malformed frame, answers bad_message and closes the
// connection after maxMalformed of them.
func (c *conn) malformed(ref string) (stop bool) {
	c.badCount++
	c.sendError(protocol.ErrBadMessage, ref)
	if c.badCount >= maxMalformed {
		c.Close(protocol.ClosePolicy, "too many malformed frames")
		return true
	}
	return false
}

func (c *conn) sendError(code, ref string) {
	c.sendFrame(protocol.TypeError, protocol.ErrorBody{Code: code, Ref: ref})
}

// sendFrame encodes and queues a server frame. A client whose queue is full is
// slow: it is closed so it can reconnect for a fresh snapshot.
func (c *conn) sendFrame(t string, payload any) {
	d, err := json.Marshal(payload)
	if err != nil {
		c.log.Error("ws encode failed", "type", t, "err", err)
		return
	}
	frame, err := json.Marshal(protocol.Envelope{T: t, V: protocol.Version, D: d})
	if err != nil {
		c.log.Error("ws encode failed", "type", t, "err", err)
		return
	}
	if !c.Send(frame) {
		c.Close(protocol.ClosePolicy, "send queue full")
	}
}

// sanitizeCloseCode maps codes that cannot be sent on the wire to 1011.
func sanitizeCloseCode(code int) int {
	switch {
	case code >= 1000 && code <= 1014 && code != 1004 && code != 1005 && code != 1006:
		return code
	case code >= 3000 && code <= 4999:
		return code
	}
	return int(websocket.StatusInternalError)
}

// truncateReason cuts reason to the protocol maximum on a rune boundary.
func truncateReason(reason string) string {
	if len(reason) <= maxCloseReason {
		return reason
	}
	reason = reason[:maxCloseReason]
	for !utf8.ValidString(reason) {
		reason = reason[:len(reason)-1]
	}
	return reason
}
