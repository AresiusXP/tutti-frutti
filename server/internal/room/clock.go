package room

import (
	"sync"
	"time"
)

type systemClock struct{}

func (systemClock) Now() time.Time { return time.Now() }

func (systemClock) AfterFunc(d time.Duration, f func()) Timer { return time.AfterFunc(d, f) }

// SystemClock returns the real clock (time.Now and time.AfterFunc). It is the
// Hub's default.
func SystemClock() Clock { return systemClock{} }

// callbackGrace bounds how long ManualClock.Advance waits for a fired callback
// to return, so a callback that blocks cannot stall the test clock.
const callbackGrace = 100 * time.Millisecond

// ManualClock is a Clock whose time only moves when Advance or Set is called.
// It exists for deterministic tests (of this package and of the packages that
// use a Hub): nothing fires on its own, not even a timer created with a
// non-positive duration; due timers fire inside Advance/Set.
//
// A fired function runs in its own goroutine, like time.AfterFunc. Advance
// fires the due timers one at a time in (deadline, creation) order, sets Now to
// each timer's deadline while it fires, and waits until the callback has
// returned (or, for a callback that blocks, for a short grace period) before
// moving on, so that a test can rely on the callback's effects once Advance
// returns. A callback may create further timers; those that are due within
// the same Advance fire in that call too.
//
// A ManualClock is safe for concurrent use.
type ManualClock struct {
	mu     sync.Mutex
	now    time.Time
	seq    uint64
	timers []*manualTimer
}

var _ Clock = (*ManualClock)(nil)

// NewManualClock returns a ManualClock set to start.
func NewManualClock(start time.Time) *ManualClock {
	return &ManualClock{now: start}
}

// Now implements Clock.
func (c *ManualClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

// AfterFunc implements Clock. The timer fires during the first Advance or Set
// that reaches Now()+d.
func (c *ManualClock) AfterFunc(d time.Duration, f func()) Timer {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.seq++
	t := &manualTimer{c: c, when: c.now.Add(d), seq: c.seq, f: f}
	c.timers = append(c.timers, t)
	return t
}

// Advance moves the clock forward by d (a negative d fires nothing and moves
// nothing) and fires every timer that became due, in order.
func (c *ManualClock) Advance(d time.Duration) {
	c.mu.Lock()
	target := c.now.Add(max(d, 0))
	c.mu.Unlock()
	c.advanceTo(target)
}

// Set moves the clock to t and fires every timer that became due. Setting a
// time in the past just moves the clock back without firing anything.
func (c *ManualClock) Set(t time.Time) {
	c.mu.Lock()
	if t.Before(c.now) {
		c.now = t
		c.mu.Unlock()
		return
	}
	c.mu.Unlock()
	c.advanceTo(t)
}

// Pending returns the number of timers that have neither fired nor been
// stopped.
func (c *ManualClock) Pending() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.timers)
}

func (c *ManualClock) advanceTo(target time.Time) {
	for {
		c.mu.Lock()
		next := -1
		for i, t := range c.timers {
			if t.when.After(target) {
				continue
			}
			if next < 0 || t.before(c.timers[next]) {
				next = i
			}
		}
		if next < 0 {
			if target.After(c.now) {
				c.now = target
			}
			c.mu.Unlock()
			return
		}
		t := c.timers[next]
		c.timers = append(c.timers[:next], c.timers[next+1:]...)
		if t.when.After(c.now) {
			c.now = t.when
		}
		c.mu.Unlock()
		run(t.f)
	}
}

// run starts f in its own goroutine and waits until it has returned, or for
// callbackGrace if it blocks.
func run(f func()) {
	started := make(chan struct{})
	finished := make(chan struct{})
	go func() {
		close(started)
		defer close(finished)
		f()
	}()
	<-started
	grace := time.NewTimer(callbackGrace)
	defer grace.Stop()
	select {
	case <-finished:
	case <-grace.C:
	}
}

type manualTimer struct {
	c    *ManualClock
	when time.Time
	seq  uint64
	f    func()
}

func (t *manualTimer) before(o *manualTimer) bool {
	if !t.when.Equal(o.when) {
		return t.when.Before(o.when)
	}
	return t.seq < o.seq
}

// Stop implements Timer.
func (t *manualTimer) Stop() bool {
	c := t.c
	c.mu.Lock()
	defer c.mu.Unlock()
	for i, x := range c.timers {
		if x == t {
			c.timers = append(c.timers[:i], c.timers[i+1:]...)
			return true
		}
	}
	return false
}
