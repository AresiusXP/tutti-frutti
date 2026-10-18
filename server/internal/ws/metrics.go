package ws

import (
	"bytes"
	"errors"
	"strconv"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/AresiusXP/tutti-frutti/server/internal/game"
	"github.com/AresiusXP/tutti-frutti/server/internal/protocol"
)

// otherLabel is the bounded catch-all for label values we do not enumerate.
const otherLabel = "other"

// closeLabels are the close codes that get their own label value: the ones
// this server emits plus the ones browsers send. Everything else is "other".
var closeLabels = map[int]string{}

// frameTypes is the closed set of frame types that get their own label value.
var frameTypes = map[string]bool{}

func init() {
	for _, code := range []int{
		1000, 1001, 1006, 1011,
		protocol.ClosePolicy, protocol.CloseTooBig, protocol.CloseServiceRestart,
		protocol.CloseReplaced, protocol.CloseKicked, protocol.CloseUnsupportedVersion,
		protocol.CloseRoomClosed, protocol.CloseLeft,
	} {
		closeLabels[code] = strconv.Itoa(code)
	}
	for _, t := range []string{
		game.CmdJoin, game.CmdLeave, game.CmdUpdateSettings, game.CmdKick, game.CmdStartGame,
		game.CmdStopLetter, game.CmdSetAnswer, game.CmdBasta, game.CmdAskStop, game.CmdVoteStop, game.CmdVoteReject,
		game.CmdProposeMerge, game.CmdVoteMerge, game.CmdReviewNav, game.CmdFinishReview,
		game.CmdNextRound, game.CmdEndGame, game.CmdRematch,
		protocol.TypePing, protocol.TypePong, protocol.TypeState, protocol.TypeEvent, protocol.TypeError,
	} {
		frameTypes[t] = true
	}
}

func closeLabel(code int) string {
	if l, ok := closeLabels[code]; ok {
		return l
	}
	return otherLabel
}

func typeLabel(t string) string {
	if frameTypes[t] {
		return t
	}
	return otherLabel
}

// frameTypeOf extracts the "t" of an encoded outbound frame without decoding
// it. Frames produced by encoding protocol.Envelope start with {"t":"...";
// anything else is "other".
func frameTypeOf(frame []byte) string {
	const prefix = `{"t":"`
	if !bytes.HasPrefix(frame, []byte(prefix)) {
		return otherLabel
	}
	rest := frame[len(prefix):]
	end := bytes.IndexByte(rest, '"')
	if end < 0 || end > 32 {
		return otherLabel
	}
	return typeLabel(string(rest[:end]))
}

// metrics are the WebSocket Prometheus collectors. A nil *metrics is a no-op.
type metrics struct {
	closes *prometheus.CounterVec
	frames *prometheus.CounterVec
	open   prometheus.Gauge
	drops  prometheus.Counter
}

func newMetrics(reg prometheus.Registerer) *metrics {
	if reg == nil {
		return nil
	}
	return &metrics{
		closes: register(reg, prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "tuttifrutti_ws_closes_total",
			Help: "WebSocket connections ended, by close code (\"other\" for codes not enumerated).",
		}, []string{"code"})),
		frames: register(reg, prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "tuttifrutti_ws_frames_total",
			Help: "WebSocket frames by direction (in/out) and frame type (\"other\" for unknown types).",
		}, []string{"dir", "type"})),
		open: register(reg, prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "tuttifrutti_ws_connections_open",
			Help: "Live WebSocket connections, joined or not (tuttifrutti_connections counts the joined ones).",
		})),
		drops: register(reg, prometheus.NewCounter(prometheus.CounterOpts{
			Name: "tuttifrutti_ws_send_drops_total",
			Help: "Slow clients closed (1008) because their send queue was full; each one reconnects for a fresh snapshot.",
		})),
	}
}

// register registers c, reusing the collector that is already registered
// under the same name (several handlers can share one registry).
func register[C prometheus.Collector](reg prometheus.Registerer, c C) C {
	if err := reg.Register(c); err != nil {
		var are prometheus.AlreadyRegisteredError
		if errors.As(err, &are) {
			if existing, ok := are.ExistingCollector.(C); ok {
				return existing
			}
		}
		panic("ws: registering metrics: " + err.Error())
	}
	return c
}

func (m *metrics) opened() {
	if m != nil {
		m.open.Inc()
	}
}

func (m *metrics) released() {
	if m != nil {
		m.open.Dec()
	}
}

func (m *metrics) closed(code int) {
	if m != nil {
		m.closes.WithLabelValues(closeLabel(code)).Inc()
	}
}

// sendDrop counts a client whose send queue overflowed (once per connection).
func (m *metrics) sendDrop() {
	if m != nil {
		m.drops.Inc()
	}
}

func (m *metrics) frameIn(t string) {
	if m != nil {
		m.frames.WithLabelValues("in", typeLabel(t)).Inc()
	}
}

func (m *metrics) frameOut(frame []byte) {
	if m != nil {
		m.frames.WithLabelValues("out", frameTypeOf(frame)).Inc()
	}
}
