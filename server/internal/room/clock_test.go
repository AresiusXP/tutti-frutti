package room

import (
	"slices"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestSystemClock(t *testing.T) {
	c := SystemClock()
	before := time.Now()
	if n := c.Now(); n.Before(before) {
		t.Errorf("Now() = %v, before %v", n, before)
	}
	fired := make(chan struct{})
	timer := c.AfterFunc(time.Millisecond, func() { close(fired) })
	<-fired
	if timer.Stop() {
		t.Error("Stop() after the timer fired = true")
	}
	stopped := c.AfterFunc(time.Hour, func() { t.Error("stopped timer fired") })
	if !stopped.Stop() {
		t.Error("Stop() before firing = false")
	}
}

func TestManualClockNowAndSet(t *testing.T) {
	c := NewManualClock(t0)
	if !c.Now().Equal(t0) {
		t.Fatalf("Now() = %v, want %v", c.Now(), t0)
	}
	c.Advance(90 * time.Second)
	if want := t0.Add(90 * time.Second); !c.Now().Equal(want) {
		t.Errorf("after Advance Now() = %v, want %v", c.Now(), want)
	}
	c.Advance(-time.Hour) // a negative step moves nothing
	if want := t0.Add(90 * time.Second); !c.Now().Equal(want) {
		t.Errorf("after Advance(-1h) Now() = %v, want %v", c.Now(), want)
	}
	c.Set(t0.Add(time.Hour))
	if want := t0.Add(time.Hour); !c.Now().Equal(want) {
		t.Errorf("after Set Now() = %v, want %v", c.Now(), want)
	}
	fired := false
	c.AfterFunc(time.Minute, func() { fired = true })
	c.Set(t0) // back in time: nothing fires, the timer is still pending
	if fired || c.Pending() != 1 {
		t.Errorf("Set into the past: fired=%v pending=%d", fired, c.Pending())
	}
}

func TestManualClockFiresInOrder(t *testing.T) {
	c := NewManualClock(t0)
	var mu sync.Mutex
	var order []string
	var seenAt []time.Time
	add := func(name string, d time.Duration) {
		c.AfterFunc(d, func() {
			mu.Lock()
			defer mu.Unlock()
			order = append(order, name)
			seenAt = append(seenAt, c.Now())
		})
	}
	add("c", 3*time.Second)
	add("a1", time.Second)
	add("b", 2*time.Second)
	add("a2", time.Second) // same deadline: creation order
	add("late", time.Hour)

	if c.Pending() != 5 {
		t.Fatalf("Pending() = %d, want 5", c.Pending())
	}
	c.Advance(10 * time.Second)

	mu.Lock()
	defer mu.Unlock()
	if want := []string{"a1", "a2", "b", "c"}; !slices.Equal(order, want) {
		t.Errorf("fired %v, want %v", order, want)
	}
	// Now() equals each timer's deadline while it fires.
	for i, want := range []time.Duration{time.Second, time.Second, 2 * time.Second, 3 * time.Second} {
		if !seenAt[i].Equal(t0.Add(want)) {
			t.Errorf("timer %d saw %v, want %v", i, seenAt[i].Sub(t0), want)
		}
	}
	if c.Pending() != 1 {
		t.Errorf("Pending() = %d, want 1 (the late timer)", c.Pending())
	}
	if want := t0.Add(10 * time.Second); !c.Now().Equal(want) {
		t.Errorf("Now() = %v, want %v", c.Now(), want)
	}
}

func TestManualClockStop(t *testing.T) {
	c := NewManualClock(t0)
	var fired atomic.Int32
	keep := c.AfterFunc(time.Second, func() { fired.Add(1) })
	drop := c.AfterFunc(time.Second, func() { fired.Add(100) })
	if !drop.Stop() {
		t.Error("Stop() of a pending timer = false")
	}
	if drop.Stop() {
		t.Error("second Stop() = true")
	}
	c.Advance(time.Second)
	if fired.Load() != 1 {
		t.Errorf("fired = %d, want only the kept timer", fired.Load())
	}
	if keep.Stop() {
		t.Error("Stop() after firing = true")
	}
	if c.Pending() != 0 {
		t.Errorf("Pending() = %d, want 0", c.Pending())
	}
}

func TestManualClockCallbackEffectsVisibleAfterAdvance(t *testing.T) {
	c := NewManualClock(t0)
	var n atomic.Int32
	for range 20 {
		c.AfterFunc(time.Second, func() { n.Add(1) })
	}
	c.Advance(time.Second)
	if n.Load() != 20 {
		t.Errorf("callbacks run = %d, want 20 by the time Advance returns", n.Load())
	}
}

func TestManualClockCallbackMayRearm(t *testing.T) {
	c := NewManualClock(t0)
	var ticks atomic.Int32
	var arm func()
	arm = func() {
		c.AfterFunc(time.Second, func() {
			ticks.Add(1)
			arm()
		})
	}
	arm()
	c.Advance(5 * time.Second)
	if ticks.Load() != 5 {
		t.Errorf("ticks = %d, want 5 within one Advance", ticks.Load())
	}
	if c.Pending() != 1 {
		t.Errorf("Pending() = %d, want 1", c.Pending())
	}
}

func TestManualClockNonPositiveTimerWaitsForAdvance(t *testing.T) {
	c := NewManualClock(t0)
	var fired atomic.Bool
	c.AfterFunc(0, func() { fired.Store(true) })
	c.AfterFunc(-time.Second, func() {})
	if fired.Load() || c.Pending() != 2 {
		t.Fatalf("a manual clock fires nothing on its own: fired=%v pending=%d", fired.Load(), c.Pending())
	}
	c.Advance(0)
	if !fired.Load() || c.Pending() != 0 {
		t.Errorf("after Advance(0): fired=%v pending=%d", fired.Load(), c.Pending())
	}
}

func TestManualClockBlockedCallbackDoesNotStallAdvance(t *testing.T) {
	c := NewManualClock(t0)
	release := make(chan struct{})
	finished := make(chan struct{})
	c.AfterFunc(time.Second, func() {
		<-release
		close(finished)
	})
	var second atomic.Bool
	c.AfterFunc(2*time.Second, func() { second.Store(true) })

	c.Advance(3 * time.Second) // returns although the first callback is still blocked
	if !second.Load() {
		t.Error("the second timer did not fire")
	}
	close(release)
	<-finished
}
