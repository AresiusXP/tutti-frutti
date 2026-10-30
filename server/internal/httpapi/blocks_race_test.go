package httpapi

import (
	"net/http"
	"net/http/httptest"
	"runtime"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/AresiusXP/tutti-frutti/server/internal/safety"
)

// users is how many goroutines hold or wait for the key (tests only).
func (k *keyedMutex) users(key string) int {
	k.mu.Lock()
	defer k.mu.Unlock()
	if e := k.m[key]; e != nil {
		return e.refs
	}
	return 0
}

func (e *blockEnv) unblock(target string, c *http.Cookie) *httptest.ResponseRecorder {
	return e.do(http.MethodDelete, "/api/blocks/"+target, "", withOrigin(prodOrigin), withCookie(c))
}

// policyMatchesStore fails the test unless the policy knows the pair exactly
// when the database has it.
func (e *blockEnv) policyMatchesStore(t *testing.T, blocker, blocked string) (blockedNow bool) {
	t.Helper()
	list, err := e.mod.ListBlocks(t.Context(), blocker)
	if err != nil {
		t.Fatal(err)
	}
	inStore := slices.ContainsFunc(list, func(b safety.Block) bool { return b.Blocked == blocked })
	if inPolicy := e.policy.Blocks(blocker, blocked); inPolicy != inStore {
		t.Fatalf("the policy says blocked=%v but the database says blocked=%v for %s -> %s", inPolicy, inStore, blocker, blocked)
	}
	return inStore
}

// concurrently starts fn on another goroutine from inside a store hook (that is,
// while the request that runs the hook sits between its database write and its
// policy update) and returns once fn has either finished or queued behind the
// blocker's lock: in the second case it cannot touch the store before the
// request that runs the hook goes on, and no sleep or timing is involved.
// The returned function waits for fn.
func (e *blockEnv) concurrently(blocker string, fn func()) (wait func()) {
	done := make(chan struct{})
	go func() {
		defer close(done)
		fn()
	}()
	for e.srv.blockLocks.users(blocker) < 2 { // the holder, and fn waiting
		select {
		case <-done:
			return func() {}
		default:
			runtime.Gosched()
		}
	}
	return func() { <-done }
}

// An unblock that lands between a block's database write and its policy update
// must not leave the policy with a block the database no longer has.
func TestUnblockBetweenBlockWriteAndPolicyUpdateKeepsPolicyEqualToStore(t *testing.T) {
	e := newBlockEnv(t, nil)
	c, me := e.guest(t)
	const rounds = 25
	for round := range rounds {
		var (
			wait      func()
			unblocked *httptest.ResponseRecorder
		)
		e.mod.afterAdd = func() {
			e.mod.afterAdd = nil
			wait = e.concurrently(me, func() { unblocked = e.unblock("target-id", c) })
		}
		e.now.Advance(time.Hour) // fresh rate-limit tokens
		if rec := e.block(blockBody("target-id", "Beto"), c); rec.Code != http.StatusOK {
			t.Fatalf("round %d: block = %d %s", round, rec.Code, rec.Body)
		}
		wait()
		if unblocked.Code != http.StatusNoContent {
			t.Fatalf("round %d: unblock = %d %s", round, unblocked.Code, unblocked.Body)
		}
		if e.policyMatchesStore(t, me, "target-id") {
			t.Fatalf("round %d: the unblock came after the block, so the pair must not be blocked", round)
		}
	}
}

// A block that lands between an unblock's database delete and its policy update
// must not be lost from the policy: the policy would then let the pair share a
// room while the database says they are blocked.
func TestBlockBetweenUnblockDeleteAndPolicyUpdateKeepsPolicyEqualToStore(t *testing.T) {
	e := newBlockEnv(t, nil)
	c, me := e.guest(t)
	e.now.Advance(time.Hour)
	if rec := e.block(blockBody("target-id", "Beto"), c); rec.Code != http.StatusOK {
		t.Fatalf("setup block = %d %s", rec.Code, rec.Body)
	}
	const rounds = 25
	for round := range rounds {
		var (
			wait      func()
			reblocked *httptest.ResponseRecorder
		)
		e.mod.afterRemove = func() {
			e.mod.afterRemove = nil
			wait = e.concurrently(me, func() { reblocked = e.block(blockBody("target-id", "Beto"), c) })
		}
		e.now.Advance(time.Hour)
		if rec := e.unblock("target-id", c); rec.Code != http.StatusNoContent {
			t.Fatalf("round %d: unblock = %d %s", round, rec.Code, rec.Body)
		}
		wait()
		if reblocked.Code != http.StatusOK {
			t.Fatalf("round %d: block = %d %s", round, reblocked.Code, reblocked.Body)
		}
		if !e.policyMatchesStore(t, me, "target-id") {
			t.Fatalf("round %d: the block came after the unblock, so the pair must be blocked", round)
		}
	}
}

// Many blocks and unblocks of the same pairs at once (run with -race): whatever
// the interleaving, the policy ends up equal to the database.
func TestConcurrentBlocksAndUnblocksLeavePolicyEqualToStore(t *testing.T) {
	e := newBlockEnv(t, nil)
	c, me := e.guest(t)
	targets := []string{"target-a", "target-b", "target-c"}
	var wg sync.WaitGroup
	for worker := range 6 {
		wg.Go(func() {
			for i := range 60 {
				e.now.Advance(time.Hour)
				target := targets[(worker+i)%len(targets)]
				if (worker+i)%3 == 0 {
					e.unblock(target, c)
				} else {
					e.block(blockBody(target, ""), c)
				}
			}
		})
	}
	wg.Wait()
	for _, target := range targets {
		e.policyMatchesStore(t, me, target)
	}
	if n := e.srv.blockLocks.users(me); n != 0 {
		t.Errorf("%d goroutines still hold or wait for the blocker's lock", n)
	}
}
