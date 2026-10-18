package room

import (
	"errors"
	"log/slog"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/AresiusXP/tutti-frutti/server/internal/game"
)

// commandTypes bounds the "type" label of tuttifrutti_commands_total: the
// client can send any frame type, but only these are ever used as label
// values.
var commandTypes = map[string]struct{}{
	game.CmdJoin: {}, game.CmdLeave: {}, game.CmdUpdateSettings: {}, game.CmdKick: {},
	game.CmdStartGame: {}, game.CmdStopLetter: {}, game.CmdSetAnswer: {}, game.CmdBasta: {},
	game.CmdVoteReject: {}, game.CmdProposeMerge: {}, game.CmdVoteMerge: {},
	game.CmdReviewNav: {}, game.CmdFinishReview: {}, game.CmdNextRound: {},
	game.CmdEndGame: {}, game.CmdRematch: {},
}

const (
	typeUnknown = "unknown"
	resultOK    = "ok"
)

// metrics are the room-layer Prometheus collectors. They are always usable;
// they are only registered when a Registerer is configured.
type metrics struct {
	rooms          prometheus.Gauge
	connections    prometheus.Gauge
	commands       *prometheus.CounterVec
	rounds         prometheus.Counter
	basta          prometheus.Counter
	snapshotErrors prometheus.Counter
}

func newMetrics(reg prometheus.Registerer, log *slog.Logger) *metrics {
	m := &metrics{
		rooms: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "tuttifrutti_rooms", Help: "Live rooms."}),
		connections: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "tuttifrutti_connections", Help: "Clients attached to a room."}),
		commands: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "tuttifrutti_commands_total",
			Help: "Client frames handled by a room, by type and result (ok or the error code)."},
			[]string{"type", "result"}),
		rounds: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "tuttifrutti_rounds_total", Help: "Rounds started."}),
		basta: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "tuttifrutti_basta_total", Help: "Basta calls."}),
		snapshotErrors: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "tuttifrutti_snapshot_errors_total", Help: "Failed snapshot writes and deletions."}),
	}
	if reg == nil {
		return m
	}
	m.rooms = register(reg, m.rooms, log)
	m.connections = register(reg, m.connections, log)
	m.commands = register(reg, m.commands, log)
	m.rounds = register(reg, m.rounds, log)
	m.basta = register(reg, m.basta, log)
	m.snapshotErrors = register(reg, m.snapshotErrors, log)
	return m
}

// register registers c and returns it; when an equivalent collector is
// already registered (a Hub re-created on the same registry, as tests do) the
// existing one is returned so the new Hub keeps feeding the same series.
func register[C prometheus.Collector](reg prometheus.Registerer, c C, log *slog.Logger) C {
	err := reg.Register(c)
	if err == nil {
		return c
	}
	var are prometheus.AlreadyRegisteredError
	if errors.As(err, &are) {
		if existing, ok := are.ExistingCollector.(C); ok {
			return existing
		}
	}
	log.Warn("registering room metrics failed", "err", err)
	return c
}

// command counts one client frame; result is resultOK or an error code.
func (m *metrics) command(t, result string) {
	if _, ok := commandTypes[t]; !ok {
		t = typeUnknown
	}
	m.commands.WithLabelValues(t, result).Inc()
}
