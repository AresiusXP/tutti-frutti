package room

import (
	"errors"
	"log/slog"
	"slices"
	"time"

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
	game.CmdReviewNav: {}, game.CmdAskJudge: {}, game.CmdFinishReview: {}, game.CmdNextRound: {},
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

// The values of the "reason" label of tuttifrutti_games_finished_total, told
// from the state the engine is in when it announces game_over: every
// configured round was played; no letter is left to play; or the host ended
// the game while rounds and letters remained.
const (
	finishCompleted        = "completed"
	finishLettersExhausted = "letters_exhausted"
	finishEndedByHost      = "ended_by_host"
)

var finishReasons = []string{finishCompleted, finishLettersExhausted, finishEndedByHost}

// The values of the "result" label of tuttifrutti_snapshot_saves_total.
const (
	saveOK    = "ok"
	saveError = "error"
)

var saveResults = []string{saveOK, saveError}

// The values of the "source" label of tuttifrutti_snapshots_deleted_total: the
// janitor removed a stale snapshot, or a room that closed removed its own.
const (
	deletedJanitor    = "janitor"
	deletedRoomClosed = "room_closed"
)

var deletedSources = []string{deletedJanitor, deletedRoomClosed}

// snapshotBuckets suit a database write: 1 ms to 5 s (the saver gives up on a
// write after 10 s, which lands in +Inf).
var snapshotBuckets = []float64{0.001, 0.0025, 0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5}

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

	gamesStarted    prometheus.Counter
	gamesFinished   *prometheus.CounterVec
	timeUps         prometheus.Counter
	letterAutoStops prometheus.Counter

	snapshotSaves    *prometheus.CounterVec
	snapshotDuration prometheus.Histogram
	snapshotLastOK   prometheus.Gauge
	snapshotQueue    prometheus.Gauge
	snapshotsDeleted *prometheus.CounterVec

	roomsRestored   prometheus.Counter
	restoreDuration prometheus.Gauge
	restoreErrors   prometheus.Counter

	statsQueue  prometheus.Gauge
	statsOutbox prometheus.Gauge
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

		gamesStarted: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "tuttifrutti_games_started_total", Help: "Games started by a host (a rematch and a new start count again)."}),
		gamesFinished: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "tuttifrutti_games_finished_total",
			Help: "Games that reached the final phase, by reason: completed (every configured round played), letters_exhausted (no letter left) or ended_by_host (ended early)."},
			[]string{"reason"}),
		timeUps: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "tuttifrutti_time_ups_total", Help: "Rounds that ended because the time limit expired."}),
		letterAutoStops: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "tuttifrutti_letter_auto_stops_total", Help: "Letters picked by the server because the chooser did not tap in time."}),

		snapshotSaves: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "tuttifrutti_snapshot_saves_total",
			Help: "Room snapshot save attempts, by result: ok, or error (encoding or writing failed; a failed write is retried and every attempt counts)."},
			[]string{"result"}),
		snapshotDuration: prometheus.NewHistogram(prometheus.HistogramOpts{
			Name:    "tuttifrutti_snapshot_save_duration_seconds",
			Help:    "Duration of one snapshot write to the store, successful or not.",
			Buckets: snapshotBuckets}),
		snapshotLastOK: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "tuttifrutti_snapshot_last_success_timestamp_seconds",
			Help: "Unix time of the last successful snapshot write; 0 until the first one of this process."}),
		snapshotQueue: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "tuttifrutti_snapshot_save_queue",
			Help: "Rooms with a snapshot waiting to be written, being written or being retried."}),
		snapshotsDeleted: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "tuttifrutti_snapshots_deleted_total",
			Help: "Snapshots deleted, by source: janitor (stale rows) or room_closed (a closed room removed its own)."},
			[]string{"source"}),

		roomsRestored: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "tuttifrutti_rooms_restored_total", Help: "Rooms rebuilt from their snapshots at startup."}),
		restoreDuration: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "tuttifrutti_restore_duration_seconds",
			Help: "How long the startup restore took (loading the snapshots and starting the rooms); 0 until it ran."}),
		restoreErrors: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "tuttifrutti_restore_errors_total",
			Help: "Saved rooms the startup restore could not bring back: unusable snapshots (discarded) and snapshots beyond MAX_ROOMS (left in the store)."}),

		statsQueue: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "tuttifrutti_stats_queue_depth", Help: "Finished games queued in the statistics recorder."}),
		statsOutbox: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "tuttifrutti_stats_outbox_pending",
			Help: "Finished games the rooms keep in their outbox until the store confirms them."}),
	}
	// Constant cardinality: every series exists from the start.
	for _, r := range []string{statsOK, statsError, statsDropped} {
		m.statsRecorded.WithLabelValues(r)
	}
	for _, r := range stopResults {
		m.stopVotes.WithLabelValues(r)
	}
	for _, r := range finishReasons {
		m.gamesFinished.WithLabelValues(r)
	}
	for _, r := range saveResults {
		m.snapshotSaves.WithLabelValues(r)
	}
	for _, r := range deletedSources {
		m.snapshotsDeleted.WithLabelValues(r)
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
	m.gamesStarted = register(reg, m.gamesStarted, log)
	m.gamesFinished = register(reg, m.gamesFinished, log)
	m.timeUps = register(reg, m.timeUps, log)
	m.letterAutoStops = register(reg, m.letterAutoStops, log)
	m.snapshotSaves = register(reg, m.snapshotSaves, log)
	m.snapshotDuration = register(reg, m.snapshotDuration, log)
	m.snapshotLastOK = register(reg, m.snapshotLastOK, log)
	m.snapshotQueue = register(reg, m.snapshotQueue, log)
	m.snapshotsDeleted = register(reg, m.snapshotsDeleted, log)
	m.roomsRestored = register(reg, m.roomsRestored, log)
	m.restoreDuration = register(reg, m.restoreDuration, log)
	m.restoreErrors = register(reg, m.restoreErrors, log)
	m.statsQueue = register(reg, m.statsQueue, log)
	m.statsOutbox = register(reg, m.statsOutbox, log)
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

// gameFinished counts a game that reached the final phase. g is the game as
// the engine left it when it announced game_over.
func (m *metrics) gameFinished(g *game.Game) {
	m.gamesFinished.WithLabelValues(finishReason(g)).Inc()
}

// finishReason tells why a game that just reached the final phase ended. The
// web client ends the last round with end_game, so the command says nothing:
// the state does.
func finishReason(g *game.Game) string {
	switch {
	case g.Settings.Rounds > 0 && g.RoundNum >= g.Settings.Rounds:
		return finishCompleted
	case len(game.AvailableLetters(g.Settings.Lang, g.Settings.ExcludeHardLetters, g.UsedLetters)) == 0:
		return finishLettersExhausted
	}
	return finishEndedByHost
}

// snapshotSaved counts a snapshot written to the store at at, which took took.
func (m *metrics) snapshotSaved(took time.Duration, at time.Time) {
	m.snapshotSaves.WithLabelValues(saveOK).Inc()
	m.snapshotDuration.Observe(took.Seconds())
	m.snapshotLastOK.Set(float64(at.UnixNano()) / 1e9)
}

// snapshotWriteFailed counts a failed write; took is how long it ran.
func (m *metrics) snapshotWriteFailed(took time.Duration) {
	m.snapshotSaves.WithLabelValues(saveError).Inc()
	m.snapshotDuration.Observe(took.Seconds())
}

// snapshotEncodeFailed counts a snapshot that could not even be encoded.
func (m *metrics) snapshotEncodeFailed() {
	m.snapshotSaves.WithLabelValues(saveError).Inc()
	m.snapshotErrors.Inc()
}
