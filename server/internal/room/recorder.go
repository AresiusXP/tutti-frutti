package room

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"runtime/debug"
	"sync"
	"time"

	"github.com/AresiusXP/tutti-frutti/server/internal/store"
)

// Tunables of the statistics recorder (fields of the recorder, so tests can
// shrink them).
const (
	// statsQueueSize bounds the finished games waiting to be recorded. A game
	// ends once in a while, so this only fills up when the database is down for
	// a very long time; a game that does not fit is refused and stays in its
	// room's outbox, offered again at the room's next snapshot.
	statsQueueSize = 256
	// statsMaxAttempts bounds the attempts per offer of a game: with the
	// backoff below (1 s doubling up to 30 s) that is a little over eight
	// minutes of database trouble. The room keeps the game and offers it again
	// at its next snapshot (and the next process at its start).
	statsMaxAttempts = 20
	// statsMinBackoff and statsMaxBackoff bound the wait between two attempts.
	statsMinBackoff = time.Second
	statsMaxBackoff = 30 * time.Second
	// Results of tuttifrutti_stats_recorded_total: a game recorded; a failed
	// attempt (it is retried); a game that will never be recorded (refused by
	// the store, evicted from a full outbox, unreadable in a snapshot).
	statsOK      = "ok"
	statsError   = "error"
	statsDropped = "dropped"
)

// statsOutcome is how the recorder ended its work on one record.
type statsOutcome int

const (
	// outcomeRecorded: the store has the record (or had it already).
	outcomeRecorded statsOutcome = iota
	// outcomeRefused: the store refused the record for good (ErrInvalidGame);
	// retrying cannot help.
	outcomeRefused
	// outcomeStalled: the record is not stored: the attempts ran out, the
	// recorder is closing or the hub was torn down. Whoever owns the record
	// keeps it (a room keeps it in its snapshot) and may offer it again.
	outcomeStalled
)

// statsJob is one record to deliver. done is called exactly once, from the
// recorder's goroutine, once its fate is known; it must not block (rooms post
// a task to their mailbox).
type statsJob struct {
	rec  store.GameRecord
	done func(statsOutcome)
}

// recorder records finished games in the background, in order, from one
// goroutine: rooms only append to a bounded queue (never blocking, never
// failing), and the goroutine retries a failing store with capped exponential
// backoff on the hub's clock. RecordGame is idempotent per (user, game), so a
// retry after an ambiguous failure never double counts.
//
// The queue is only a delivery vehicle: the durable copy of a record lives in
// its room's snapshot (the outbox, see room.pending) until done reports that
// the store has it. So nothing is ever lost by a full queue, a failure that
// outlasts the attempts or a shutdown: the record stays with its room and is
// offered again (at the room's next snapshot, or by the next process).
//
// Shutdown: after the rooms stopped producing records the hub calls
// beginClose; the goroutine then gives every queued record one last attempt
// without waiting between attempts and exits.
type recorder struct {
	h     *Hub
	rec   Recorder
	log   *slog.Logger
	clock Clock
	m     *metrics

	timeout     time.Duration
	minBackoff  time.Duration
	maxBackoff  time.Duration
	maxAttempts int
	queueSize   int

	wake    chan struct{} // capacity 1: the queue changed
	closeCh chan struct{} // closed by beginClose

	mu      sync.Mutex
	queue   []statsJob
	started bool
	closing bool
	stopped bool // the goroutine is gone (or never needed): nothing is queued any more
}

func newRecorder(h *Hub, rec Recorder) *recorder {
	return &recorder{
		h: h, rec: rec, clock: h.clock, m: h.m,
		log:         h.log.With("component", "stats"),
		timeout:     defaultSaveTimeout,
		minBackoff:  statsMinBackoff,
		maxBackoff:  statsMaxBackoff,
		maxAttempts: statsMaxAttempts,
		queueSize:   statsQueueSize,
		wake:        make(chan struct{}, 1),
		closeCh:     make(chan struct{}),
	}
}

// enqueue queues job and reports whether it was accepted. It never blocks:
// when the recorder is closing or its queue is full the job is refused and
// its owner keeps the record.
func (q *recorder) enqueue(job statsJob) bool {
	q.mu.Lock()
	if q.closing || q.stopped || len(q.queue) >= q.queueSize {
		q.mu.Unlock()
		return false
	}
	q.queue = append(q.queue, job)
	q.m.statsQueue.Set(float64(len(q.queue)))
	start := !q.started
	q.started = true
	q.mu.Unlock()

	if start {
		// The goroutine is one of the hub's, so it is added under the hub's
		// lock like every other (see spawnLocked).
		q.h.mu.Lock()
		q.h.wg.Add(1)
		q.h.mu.Unlock()
		go q.run()
	}
	select {
	case q.wake <- struct{}{}:
	default:
	}
	return true
}

// beginClose tells the goroutine that no record will be added any more: it
// drains the queue and exits.
func (q *recorder) beginClose() {
	q.mu.Lock()
	if q.closing {
		q.mu.Unlock()
		return
	}
	q.closing = true
	if !q.started {
		q.stopped = true
	}
	q.mu.Unlock()
	close(q.closeCh)
}

func (q *recorder) isClosing() bool {
	q.mu.Lock()
	defer q.mu.Unlock()
	return q.closing
}

// pending is the number of records waiting (tests).
func (q *recorder) pending() int {
	q.mu.Lock()
	defer q.mu.Unlock()
	return len(q.queue)
}

func (q *recorder) run() {
	defer q.h.wg.Done()
	defer func() {
		q.mu.Lock()
		q.stopped = true
		left := q.queue
		q.queue = nil
		q.m.statsQueue.Set(0)
		q.mu.Unlock()
		for _, job := range left {
			q.finish(job, outcomeStalled)
		}
		if len(left) > 0 {
			q.log.Warn("game statistics left for the next start: the rooms keep them in their snapshots", "games", len(left))
		}
	}()
	for {
		job, ok := q.next()
		if !ok {
			return
		}
		q.finish(job, q.deliver(job.rec))
	}
}

// finish reports the outcome to the owner of the job. A callback that panics
// must not take the recorder (and the process) down.
func (q *recorder) finish(job statsJob, o statsOutcome) {
	defer func() {
		if p := recover(); p != nil {
			q.log.Error("a statistics callback panicked", "game", job.rec.ID, "panic", p)
		}
	}()
	if job.done != nil {
		job.done(o)
	}
}

// next waits for the next job. It reports false when the recorder is
// closing and nothing is left, or the hub is torn down.
func (q *recorder) next() (statsJob, bool) {
	for {
		q.mu.Lock()
		if len(q.queue) > 0 {
			job := q.queue[0]
			q.queue = q.queue[1:]
			q.m.statsQueue.Set(float64(len(q.queue)))
			q.mu.Unlock()
			return job, true
		}
		closing := q.closing
		q.mu.Unlock()
		if closing || q.h.baseCtx.Err() != nil {
			return statsJob{}, false
		}
		select {
		case <-q.wake:
		case <-q.closeCh:
		case <-q.h.baseCtx.Done():
		}
	}
}

// deliver records one game, retrying until it succeeds, the error is
// permanent, the attempts run out, or the hub shuts down. Metrics: every
// failed attempt is an "error"; "dropped" is only a game the store refused for
// good (a game that merely stalls stays with its room).
func (q *recorder) deliver(rec store.GameRecord) statsOutcome {
	backoff := q.minBackoff
	for attempt := 1; ; attempt++ {
		ctx, cancel := context.WithTimeout(q.h.baseCtx, q.timeout)
		err := q.attempt(ctx, rec)
		cancel()
		switch {
		case err == nil:
			q.m.statsRecorded.WithLabelValues(statsOK).Inc()
			return outcomeRecorded
		case q.h.baseCtx.Err() != nil:
			return outcomeStalled
		case errors.Is(err, store.ErrInvalidGame):
			q.m.statsRecorded.WithLabelValues(statsDropped).Inc()
			q.log.Error("game statistics refused by the store; giving up", "game", rec.ID, "err", err)
			return outcomeRefused
		}
		q.m.statsRecorded.WithLabelValues(statsError).Inc()
		if q.isClosing() || attempt >= q.maxAttempts {
			q.log.Warn("game statistics not recorded; the room keeps them for later", "game", rec.ID, "attempts", attempt, "err", err)
			return outcomeStalled
		}
		q.log.Warn("recording game statistics failed; will retry", "game", rec.ID, "attempt", attempt, "retry_in", backoff, "err", err)
		q.sleep(backoff)
		backoff = min(backoff*2, q.maxBackoff)
	}
}

// attempt calls the store. A panic in it must not take the process down (the
// goroutine is not a room's): it is an error like any other.
func (q *recorder) attempt(ctx context.Context, rec store.GameRecord) (err error) {
	defer func() {
		if p := recover(); p != nil {
			q.log.Error("the statistics store panicked", "game", rec.ID, "panic", p, "stack", string(debug.Stack()))
			err = fmt.Errorf("room: statistics store panicked: %v", p)
		}
	}()
	return q.rec.RecordGame(ctx, rec)
}

// sleep waits d on the hub's clock, or until the recorder starts closing or
// the hub is torn down.
func (q *recorder) sleep(d time.Duration) {
	fired := make(chan struct{}, 1)
	t := q.clock.AfterFunc(d, func() {
		select {
		case fired <- struct{}{}:
		default:
		}
	})
	defer t.Stop()
	select {
	case <-fired:
	case <-q.closeCh:
	case <-q.h.baseCtx.Done():
	}
}
