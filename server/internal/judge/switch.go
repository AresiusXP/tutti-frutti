package judge

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"

	"github.com/prometheus/client_golang/prometheus"
)

// Judger is what a Switch wraps: anything that judges a category
// (*Cloudflare).
type Judger interface {
	Judge(ctx context.Context, req Request) ([]Result, error)
}

// ErrDisabled is what a switched-off judge answers. It wraps ErrBusy on
// purpose: the room already turns that into the soft "the AI is busy, try
// later" failure the players know, and nothing is sent to the provider.
var ErrDisabled = fmt.Errorf("%w: switched off by the operator", ErrBusy)

// Switch is the operator's kill switch of the AI judge (docs/MODERATION.md):
// while it is off every call fails with ErrDisabled and nothing leaves the
// server. It only lives in memory: a restart turns the judge on again (to turn
// it off for good, remove the Cloudflare keys from the Secret). Safe for
// concurrent use.
//
// What switching off guarantees, exactly:
//
//   - No NEW call reaches the provider: a Judge call that starts after
//     SetEnabled(false) returned fails with ErrDisabled before anything is sent.
//   - A call IN FLIGHT is cancelled: by the time SetEnabled(false) returns, the
//     context of every running call is cancelled, which aborts its HTTP request.
//     The provider may already have received that request, and what was sent
//     cannot be recalled.
//   - No verdict is returned for a call that was still running when the switch
//     went off: when the provider answers (or the cancellation lands) the Switch
//     checks again and returns ErrDisabled, discarding whatever came back, even
//     if the switch was turned on again meanwhile.
//
// That last check is the linearization point: a call whose answer passed it
// before SetEnabled(false) is an answer given before the switch went off. The
// room checks the switch once more when it applies a verdict (room.onJudged), so
// nothing is applied to a game after SetEnabled(false) returned either.
type Switch struct {
	inner Judger
	gauge prometheus.Gauge
	off   atomic.Bool // written under mu, read lock-free by Enabled

	mu       sync.Mutex
	offs     uint64                        // how many times the switch was turned off
	next     uint64                        // id of the next call
	inflight map[uint64]context.CancelFunc // the calls running now
}

// NewSwitch wraps inner (which must not be nil) with the switch on. reg
// receives the tuttifrutti_judge_enabled gauge; nil disables the metric.
func NewSwitch(inner Judger, reg prometheus.Registerer) *Switch {
	s := &Switch{inner: inner, inflight: map[uint64]context.CancelFunc{}, gauge: prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "tuttifrutti_judge_enabled",
		Help: "1 while the AI judge is on, 0 after the operator switched it off (absent without Cloudflare credentials).",
	})}
	s.gauge.Set(1)
	if reg != nil {
		reg.MustRegister(s.gauge)
	}
	return s
}

// Judge implements Judger.
func (s *Switch) Judge(ctx context.Context, req Request) ([]Result, error) {
	// The call is cancelled by its caller or by the switch turning off,
	// whichever comes first.
	callCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	s.mu.Lock()
	if s.off.Load() {
		s.mu.Unlock()
		return nil, ErrDisabled
	}
	id, offs := s.next, s.offs
	s.next++
	s.inflight[id] = cancel
	s.mu.Unlock()

	results, err := s.inner.Judge(callCtx, req)

	s.mu.Lock()
	delete(s.inflight, id)
	wentOff := s.offs != offs || s.off.Load()
	s.mu.Unlock()
	if wentOff {
		return nil, ErrDisabled
	}
	return results, err
}

// Enabled reports whether the judge is on.
func (s *Switch) Enabled() bool { return !s.off.Load() }

// SetEnabled turns the judge on or off. Turning it off cancels every call in
// flight (see Switch for what that does and does not guarantee).
func (s *Switch) SetEnabled(on bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	switch {
	case !on && !s.off.Load():
		s.off.Store(true)
		s.offs++
		for _, cancel := range s.inflight {
			cancel()
		}
		s.gauge.Set(0)
	case on && s.off.Load():
		s.off.Store(false)
		s.gauge.Set(1)
	}
}
