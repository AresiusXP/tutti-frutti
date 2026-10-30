package judge

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// blockingJudge is a provider that does not answer until the test releases it
// (or, with honorCtx, until its context is cancelled).
type blockingJudge struct {
	honorCtx bool
	started  chan struct{}
	release  chan struct{}
	// observed carries the context's error as the provider saw it when it stopped.
	observed chan error
}

func newBlockingJudge(honorCtx bool) *blockingJudge {
	return &blockingJudge{
		honorCtx: honorCtx,
		started:  make(chan struct{}, 4), release: make(chan struct{}), observed: make(chan error, 4),
	}
}

func (b *blockingJudge) Judge(ctx context.Context, _ Request) ([]Result, error) {
	b.started <- struct{}{}
	if b.honorCtx {
		select {
		case <-ctx.Done():
			b.observed <- ctx.Err()
			return nil, ctx.Err()
		case <-b.release:
		}
	} else {
		<-b.release
	}
	b.observed <- ctx.Err()
	return []Result{{Known: true, Valid: true, Reason: "a verdict"}}, nil
}

type outcome struct {
	results []Result
	err     error
}

func start(s *Switch, ctx context.Context, r Request) <-chan outcome {
	out := make(chan outcome, 1)
	go func() {
		results, err := s.Judge(ctx, r)
		out <- outcome{results, err}
	}()
	return out
}

func within[T any](t *testing.T, what string, ch <-chan T) T {
	t.Helper()
	select {
	case v := <-ch:
		return v
	case <-time.After(10 * time.Second):
		t.Fatalf("timed out waiting for %s", what)
		panic("unreachable")
	}
}

func TestSwitchingOffCancelsTheCallInFlightAndReturnsErrDisabled(t *testing.T) {
	inner := newBlockingJudge(true)
	s := NewSwitch(inner, nil)
	out := start(s, context.Background(), Request{})
	within(t, "the provider call to start", inner.started)

	s.SetEnabled(false)

	if err := within(t, "the provider to see the cancellation", inner.observed); !errors.Is(err, context.Canceled) {
		t.Errorf("the provider's context error = %v, want context.Canceled", err)
	}
	got := within(t, "the Judge call", out)
	if !errors.Is(got.err, ErrDisabled) || !errors.Is(got.err, ErrBusy) || got.results != nil {
		t.Errorf("in-flight call after switching off = %+v, want ErrDisabled (an ErrBusy) and no results", got)
	}
}

// A provider that ignores its context and answers anyway: the verdict must not
// get out.
func TestAVerdictAnsweredAfterSwitchingOffIsDiscarded(t *testing.T) {
	inner := newBlockingJudge(false)
	s := NewSwitch(inner, nil)
	out := start(s, context.Background(), Request{})
	within(t, "the provider call to start", inner.started)

	s.SetEnabled(false)
	close(inner.release) // the provider answers after the switch went off
	if err := within(t, "the provider to answer", inner.observed); !errors.Is(err, context.Canceled) {
		t.Errorf("the provider's context error = %v, want it cancelled by the switch", err)
	}
	got := within(t, "the Judge call", out)
	if !errors.Is(got.err, ErrDisabled) || got.results != nil {
		t.Errorf("verdict answered after switching off = %+v, want ErrDisabled and nothing", got)
	}
}

// Switching off and on again while a call runs does not bring its verdict back:
// the call began in the epoch that was switched off.
func TestAVerdictIsDiscardedEvenIfTheSwitchIsBackOn(t *testing.T) {
	inner := newBlockingJudge(false)
	s := NewSwitch(inner, nil)
	out := start(s, context.Background(), Request{})
	within(t, "the provider call to start", inner.started)

	s.SetEnabled(false)
	s.SetEnabled(true)
	close(inner.release)
	within(t, "the provider to answer", inner.observed)
	got := within(t, "the Judge call", out)
	if !errors.Is(got.err, ErrDisabled) || got.results != nil {
		t.Errorf("call that spanned an off/on = %+v, want ErrDisabled and nothing", got)
	}

	// A call that starts after the switch is back on is a normal call.
	next := newBlockingJudge(false)
	close(next.release)
	s2 := NewSwitch(next, nil)
	s2.SetEnabled(false)
	s2.SetEnabled(true)
	if got := within(t, "the second Judge call", start(s2, context.Background(), Request{})); got.err != nil || len(got.results) != 1 {
		t.Errorf("call after switching back on = %+v, want the verdict", got)
	}
}

func TestSwitchStillHonoursTheCallersContext(t *testing.T) {
	inner := newBlockingJudge(true)
	s := NewSwitch(inner, nil)

	ctx, cancel := context.WithCancel(context.Background())
	cancelled := start(s, ctx, Request{})
	within(t, "the call to start", inner.started)
	cancel()
	if got := within(t, "the cancelled call", cancelled); !errors.Is(got.err, context.Canceled) || errors.Is(got.err, ErrDisabled) {
		t.Errorf("a call cancelled by its caller = %v, want context.Canceled and not ErrDisabled", got.err)
	}
	within(t, "the provider to see it", inner.observed)
}

// End to end with the real client: the HTTP request to the provider is aborted
// when the operator switches the judge off.
func TestSwitchingOffAbortsTheHTTPRequestToTheProvider(t *testing.T) {
	arrived := make(chan struct{}, 1)
	dropped := make(chan struct{}, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body) // the server only notices a dropped connection once the body is read
		arrived <- struct{}{}
		<-r.Context().Done() // ends when the client drops the connection
		dropped <- struct{}{}
	}))
	t.Cleanup(srv.Close)
	cf, err := New(Config{AccountID: "acc1", APIToken: testToken, BaseURL: srv.URL, CallsPerMinute: 600})
	if err != nil {
		t.Fatal(err)
	}
	s := NewSwitch(cf, nil)

	out := start(s, context.Background(), req("Argentina"))
	within(t, "the request to reach the provider", arrived)
	s.SetEnabled(false)
	within(t, "the provider to see the connection drop", dropped)
	got := within(t, "the Judge call", out)
	if !errors.Is(got.err, ErrDisabled) || got.results != nil {
		t.Errorf("call aborted by the switch = %+v, want ErrDisabled", got)
	}
}
