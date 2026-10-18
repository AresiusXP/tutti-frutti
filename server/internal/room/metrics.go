package room

import (
	"errors"
	"log/slog"
	"slices"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/AresiusXP/tutti-frutti/server/internal/game"
)

// commandTypes bounds the "type" label of tuttifrutti_commands_total: the
// client can send any frame type, but only these are ever used as label
// values.
var commandTypes = map[string]struct{}{
	game.CmdJoin: {}, game.CmdLeave: {}, game.CmdUpdateSettings: {}, game.CmdKick: {},
	game.CmdStartGame: {}, game.CmdStopLetter: {}, game.CmdSetAnswer: {}, game.CmdBasta: {},
	game.CmdAskStop: {}, game.CmdVoteStop: {},
	game.CmdVoteReject: {}, game.CmdProposeMerge: {}, game.CmdVoteMerge: {},
	game.CmdReviewNav: {}, game.CmdFinishReview: {}, game.CmdNextRound: {},
	game.CmdEndGame: {}, game.CmdRematch: {},
}

const (
	typeUnknown = "unknown"
	resultOK    = "ok"
)

// The values of the "result" label of tuttifrutti_stop_votes_total. The set is
// fixed: asked is a vote opened (or passed at once), the other four are how it
// ended. declined, timeout and ended are the Reason of the engine's
// stop_rejected event.
const (
	stopAsked    = "asked"
	stopPassed   = "passed"
	stopDeclined = game.StopReasonDeclined
	stopTimeout  = game.StopReasonTimeout
	stopEnded    = game.StopReasonEnded
)

var stopResults = []string{stopAsked, stopPassed, stopDeclined, stopTimeout, stopEnded}

// metrics are the room-layer Prometheus collectors. They are always usable;
// they are only registered when a Registerer is configured.
type metrics struct {
	rooms          prometheus.Gauge
	connections    prometheus.Gauge
	commands       *prometheus.CounterVec
	rounds         prometheus.Counter
	basta          prometheus.Counter
	stopVotes      *prometheus.CounterVec
	snapshotErrors prometheus.Counter
	statsRecorded  *prometheus.CounterVec
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
		stopVotes: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "tuttifrutti_stop_votes_total",
			Help: "Votes to stop a round, by result: asked (opened, or passed at once), then passed, declined, timeout or ended (the round moved on, or the asker left)."},
			[]string{"result"}),
		snapshotErrors: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "tuttifrutti_snapshot_errors_total", Help: "Failed snapshot writes and deletions."}),
		statsRecorded: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "tuttifrutti_stats_recorded_total",
			Help: "Finished games handed to the statistics store, by result: ok, error (a failed attempt, retried) or dropped (given up)."},
			[]string{"result"}),
	}
	// Constant cardinality: every series exists from the start.
	for _, r := range []string{statsOK, statsError, statsDropped} {
		m.statsRecorded.WithLabelValues(r)
	}
	for _, r := range stopResults {
		m.stopVotes.WithLabelValues(r)
	}
	if reg == nil {
		return m
	}
	m.rooms = register(reg, m.rooms, log)
	m.connections = register(reg, m.connections, log)
	m.commands = register(reg, m.commands, log)
	m.rounds = register(reg, m.rounds, log)
	m.basta = register(reg, m.basta, log)
	m.stopVotes = register(reg, m.stopVotes, log)
	m.snapshotErrors = register(reg, m.snapshotErrors, log)
	m.statsRecorded = register(reg, m.statsRecorded, log)
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

// stopVote counts one step of a vote to stop. Only the fixed results are ever
// used as label values.
func (m *metrics) stopVote(result string) {
	if slices.Contains(stopResults, result) {
		m.stopVotes.WithLabelValues(result).Inc()
	}
}
