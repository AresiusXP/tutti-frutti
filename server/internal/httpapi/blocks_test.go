package httpapi

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/AresiusXP/tutti-frutti/server/internal/api"
	"github.com/AresiusXP/tutti-frutti/server/internal/auth"
	"github.com/AresiusXP/tutti-frutti/server/internal/game"
	"github.com/AresiusXP/tutti-frutti/server/internal/protocol"
	"github.com/AresiusXP/tutti-frutti/server/internal/room"
	"github.com/AresiusXP/tutti-frutti/server/internal/safety"
	"github.com/AresiusXP/tutti-frutti/server/internal/store"
)

// flakyModeration is the in-memory Moderation store with switches to make it fail
// and hooks that run right after a write, before the handler updates the policy
// (the window of the block/unblock race).
type flakyModeration struct {
	*store.Memory
	failAdd, failRemove, failList atomic.Bool
	afterAdd, afterRemove         func()
}

func (f *flakyModeration) AddBlock(ctx context.Context, b safety.Block, max int) (safety.Block, bool, error) {
	if f.failAdd.Load() {
		return safety.Block{}, false, errBoom
	}
	stored, created, err := f.Memory.AddBlock(ctx, b, max)
	if f.afterAdd != nil {
		f.afterAdd()
	}
	return stored, created, err
}

func (f *flakyModeration) RemoveBlock(ctx context.Context, blocker, blocked string) (bool, error) {
	if f.failRemove.Load() {
		return false, errBoom
	}
	ok, err := f.Memory.RemoveBlock(ctx, blocker, blocked)
	if f.afterRemove != nil {
		f.afterRemove()
	}
	return ok, err
}

func (f *flakyModeration) ListBlocks(ctx context.Context, blocker string) ([]safety.Block, error) {
	if f.failList.Load() {
		return nil, errBoom
	}
	return f.Memory.ListBlocks(ctx, blocker)
}

// blockRooms is a Rooms double that can sweep, like the hub: Sweep records what
// the stores and the policy looked like when it was called, which is the order
// the enforcement argument needs (row, then policy, then sweep).
type blockRooms struct {
	*fakeRooms
	policy *safety.Policy
	mod    store.Moderation

	mu         sync.Mutex
	sweeps     int
	removed    map[string][]string
	sweepErr   error
	seenPolicy []bool // per sweep: did the policy already know the pair of the request?
	pair       [2]string
	seenRow    []bool

	infoFor   []string
	listedFor []string
	hidden    map[string]bool
}

func (r *blockRooms) Sweep(ctx context.Context) (map[string][]string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.sweeps++
	if r.pair != ([2]string{}) {
		r.seenPolicy = append(r.seenPolicy, r.policy.Blocks(r.pair[0], r.pair[1]))
		bl, _ := r.mod.ListBlocks(ctx, r.pair[0])
		r.seenRow = append(r.seenRow, slices.ContainsFunc(bl, func(b safety.Block) bool { return b.Blocked == r.pair[1] }))
	}
	if r.sweepErr != nil {
		return nil, r.sweepErr
	}
	return r.removed, nil
}

func (r *blockRooms) InfoFor(code, guestID string) (room.Info, bool) {
	r.mu.Lock()
	r.infoFor = append(r.infoFor, guestID)
	hidden := r.hidden[code]
	r.mu.Unlock()
	info, ok := r.fakeRooms.Info(code)
	if guestID != "" && hidden {
		info.Joinable = false
	}
	return info, ok
}

func (r *blockRooms) PublicRoomsFor(lang game.Lang, guestID string) []api.PublicRoom {
	r.mu.Lock()
	r.listedFor = append(r.listedFor, guestID)
	hidden := r.hidden
	r.mu.Unlock()
	var out []api.PublicRoom
	for _, p := range r.fakeRooms.PublicRooms(lang) {
		if guestID != "" && hidden[p.Code] {
			continue
		}
		out = append(out, p)
	}
	return out
}

type blockEnv struct {
	*restEnv
	rooms  *blockRooms
	mod    *flakyModeration
	policy *safety.Policy
}

func newBlockEnv(t *testing.T, tweak func(*Options)) *blockEnv {
	t.Helper()
	mod := &flakyModeration{Memory: store.NewMemory()}
	policy := safety.NewPolicy(nil)
	rooms := &blockRooms{fakeRooms: &fakeRooms{infos: map[string]room.Info{
		"KT7P": {Code: "KT7P", Phase: game.PhaseLobby, Players: 2, MaxPlayers: 10, Lang: game.LangES, Joinable: true},
	}}, policy: policy, mod: mod, hidden: map[string]bool{}}
	e := newRESTEnv(t, func(o *Options) {
		o.Rooms = rooms
		o.Moderation = mod
		o.Policy = policy
		o.Sessions = auth.NewSessions([]byte("test-secret"), true)
		if tweak != nil {
			tweak(o)
		}
	})
	return &blockEnv{restEnv: e, rooms: rooms, mod: mod, policy: policy}
}

// guest mints a guest cookie and returns it with its player id.
func (e *blockEnv) guest(t *testing.T) (*http.Cookie, string) {
	t.Helper()
	c := guestCookie(t, e.do(http.MethodGet, "/api/session", ""))
	id, _, _ := strings.Cut(c.Value, ".")
	return c, id
}

func (e *blockEnv) block(body string, c *http.Cookie, opts ...reqOpt) *httptest.ResponseRecorder {
	opts = append([]reqOpt{withOrigin(prodOrigin), withJSON()}, opts...)
	if c != nil {
		opts = append(opts, withCookie(c))
	}
	return e.do(http.MethodPost, "/api/blocks", body, opts...)
}

func blockBody(player, name string) string {
	b, _ := json.Marshal(api.BlockRequest{PlayerID: player, Name: name})
	return string(b)
}

func decodeBlock(t *testing.T, rec *httptest.ResponseRecorder) api.BlockResponse {
	t.Helper()
	var out api.BlockResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("body %q: %v", rec.Body.String(), err)
	}
	return out
}

func TestBlockPlayer(t *testing.T) {
	e := newBlockEnv(t, nil)
	c, me := e.guest(t)
	e.rooms.pair = [2]string{me, "target-id"}
	e.rooms.removed = map[string][]string{"KT7P": {me}, "ZZZ2": {me, "someone"}, "BBBB": {"someone-else"}}

	rec := e.block(blockBody("target-id", "Beto"), c)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}
	got := decodeBlock(t, rec)
	if got.Block.PlayerID != "target-id" || got.Block.Name != "Beto" || got.Block.BlockedAt != e.now.Now().UnixMilli() {
		t.Errorf("block = %+v", got.Block)
	}
	if !slices.Equal(got.LeftRooms, []string{"KT7P", "ZZZ2"}) {
		t.Errorf("leftRooms = %v, want only the rooms the blocker was removed from, sorted", got.LeftRooms)
	}
	if ct := rec.Header().Get("Cache-Control"); ct != "no-store" {
		t.Errorf("Cache-Control = %q", ct)
	}
	if !e.policy.Blocks(me, "target-id") {
		t.Error("the policy does not know the block")
	}
	list, _ := e.mod.ListBlocks(context.Background(), me)
	if len(list) != 1 || list[0].Blocked != "target-id" || list[0].BlockedName != "Beto" {
		t.Errorf("stored = %+v", list)
	}
	// The order of the argument: when the rooms were swept, the row was durable
	// and the policy knew the pair.
	if e.rooms.sweeps != 1 || !slices.Equal(e.rooms.seenPolicy, []bool{true}) || !slices.Equal(e.rooms.seenRow, []bool{true}) {
		t.Errorf("sweeps = %d, policy known at the sweep = %v, row stored at the sweep = %v", e.rooms.sweeps, e.rooms.seenPolicy, e.rooms.seenRow)
	}

	// Nothing to leave: an empty array, never null.
	e.rooms.removed = nil
	e.rooms.pair = [2]string{me, "other-target"}
	rec = e.block(blockBody("other-target", ""), c, withRemote("10.0.0.2:1"))
	if raw := rec.Body.String(); !strings.Contains(raw, `"leftRooms":[]`) {
		t.Errorf("body = %s, want leftRooms as an empty array", raw)
	}
}

func TestBlockIsIdempotentAndStillSweeps(t *testing.T) {
	e := newBlockEnv(t, nil)
	c, me := e.guest(t)
	first := decodeBlock(t, e.block(blockBody("target-id", "Beto"), c))
	e.now.Advance(time.Hour)
	rec := e.block(blockBody("target-id", "Other name"), c)
	if rec.Code != http.StatusOK {
		t.Fatalf("second block: %d", rec.Code)
	}
	if again := decodeBlock(t, rec); again.Block != first.Block {
		t.Errorf("a replay changed the block: %+v vs %+v", again.Block, first.Block)
	}
	if e.rooms.sweeps != 2 {
		t.Errorf("sweeps = %d: a retry must finish the enforcement even when the row existed", e.rooms.sweeps)
	}
	if list, _ := e.mod.ListBlocks(context.Background(), me); len(list) != 1 {
		t.Errorf("%d rows", len(list))
	}
}

func TestBlockRequiresAGuestCookieAndNeverMintsOne(t *testing.T) {
	e := newBlockEnv(t, nil)
	rec := e.block(blockBody("target-id", ""), nil)
	if rec.Code != http.StatusUnauthorized || errorCode(t, rec) != "unauthorized" {
		t.Errorf("status %d %s", rec.Code, rec.Body)
	}
	if len(rec.Result().Cookies()) != 0 {
		t.Error("a cookie was minted")
	}
	forged := &http.Cookie{Name: protocol.GuestCookie, Value: "forged.value"}
	if rec := e.block(blockBody("target-id", ""), forged); rec.Code != http.StatusUnauthorized {
		t.Errorf("forged cookie: %d", rec.Code)
	}
	for _, rec := range []*httptest.ResponseRecorder{
		e.do(http.MethodGet, "/api/blocks", ""),
		e.do(http.MethodDelete, "/api/blocks/target-id", "", withOrigin(prodOrigin)),
	} {
		if rec.Code != http.StatusUnauthorized {
			t.Errorf("%s: %d", rec.Result().Request.Method, rec.Code)
		}
	}
	if e.rooms.sweeps != 0 {
		t.Error("an unauthenticated request swept the rooms")
	}
}

func TestBlockOriginAndContentType(t *testing.T) {
	e := newBlockEnv(t, nil)
	c, _ := e.guest(t)
	for name, tc := range map[string]struct {
		opts []reqOpt
		want int
	}{
		"no origin":      {[]reqOpt{withJSON(), withCookie(c)}, http.StatusForbidden},
		"foreign origin": {[]reqOpt{withOrigin("https://evil.example"), withJSON(), withCookie(c)}, http.StatusForbidden},
		"not JSON":       {[]reqOpt{withOrigin(prodOrigin), withHeader("Content-Type", "text/plain"), withCookie(c)}, http.StatusUnsupportedMediaType},
	} {
		rec := e.do(http.MethodPost, "/api/blocks", blockBody("target-id", ""), tc.opts...)
		if rec.Code != tc.want {
			t.Errorf("%s: %d, want %d", name, rec.Code, tc.want)
		}
	}
	rec := e.do(http.MethodDelete, "/api/blocks/target-id", "", withCookie(c)) // no Origin
	if rec.Code != http.StatusForbidden || errorCode(t, rec) != "forbidden_origin" {
		t.Errorf("DELETE without Origin: %d %s", rec.Code, rec.Body)
	}
	if e.rooms.sweeps != 0 {
		t.Error("a refused request swept the rooms")
	}
}

func TestBlockValidation(t *testing.T) {
	e := newBlockEnv(t, nil)
	c, me := e.guest(t)
	tests := []struct {
		name string
		body string
		want int
		code string
	}{
		{"self", blockBody(me, "Me"), 400, api.ErrCodeInvalidBlock},
		{"empty id", blockBody("", "x"), 400, api.ErrCodeInvalidBlock},
		{"malformed id", blockBody("a b", ""), 400, api.ErrCodeInvalidBlock},
		{"id with slash", blockBody("a/b", ""), 400, api.ErrCodeInvalidBlock},
		{"too long id", blockBody(strings.Repeat("a", 65), ""), 400, api.ErrCodeInvalidBlock},
		{"automated seat", blockBody("bot:Ana", ""), 400, api.ErrCodeInvalidBlock},
		{"unknown field", `{"playerId":"target-id","extra":1}`, 400, "bad_request"},
		{"not JSON", `nope`, 400, "bad_request"},
		{"trailing data", blockBody("target-id", "") + `{}`, 400, "bad_request"},
		{"too large", `{"playerId":"target-id","name":"` + strings.Repeat("x", 5000) + `"}`, 413, "payload_too_large"},
	}
	for i, tc := range tests {
		rec := e.block(tc.body, c, withRemote(fmt.Sprintf("10.1.0.%d:1", i)))
		if rec.Code != tc.want || errorCode(t, rec) != tc.code {
			t.Errorf("%s: %d %s, want %d %s", tc.name, rec.Code, rec.Body, tc.want, tc.code)
		}
	}
	if list, _ := e.mod.ListBlocks(context.Background(), me); len(list) != 0 {
		t.Errorf("an invalid request stored %d blocks", len(list))
	}
	if e.rooms.sweeps != 0 {
		t.Error("an invalid request swept the rooms")
	}
}

func TestBlockNameIsCleanedAndCut(t *testing.T) {
	e := newBlockEnv(t, nil)
	c, _ := e.guest(t)
	long := strings.Repeat("ñ", 60)
	rec := e.block(blockBody("target-id", "\u200bBe\u0000to\u202e  Pérez\n"), c)
	if got := decodeBlock(t, rec).Block.Name; got != "Beto Pérez" {
		t.Errorf("name = %q, want the cleaned nickname", got)
	}
	rec = e.block(blockBody("target-2", long), c, withRemote("10.0.0.9:1"))
	if got := decodeBlock(t, rec).Block.Name; got != strings.Repeat("ñ", safety.MaxBlockNameRunes) {
		t.Errorf("name has %d runes, want %d", len([]rune(got)), safety.MaxBlockNameRunes)
	}
}

func TestBlockLimit(t *testing.T) {
	e := newBlockEnv(t, func(o *Options) {
		o.Limits = Limits{} // the defaults
	})
	c, me := e.guest(t)
	for i := range safety.MaxBlocksPerGuest {
		if _, _, err := e.mod.AddBlock(context.Background(), safety.Block{Blocker: me, Blocked: fmt.Sprintf("t%03d", i), CreatedAt: e.now.Now()}, 1000); err != nil {
			t.Fatal(err)
		}
	}
	rec := e.block(blockBody("one-too-many", ""), c)
	if rec.Code != http.StatusConflict || errorCode(t, rec) != api.ErrCodeBlockLimit {
		t.Errorf("block %d: %d %s, want 409 block_limit", safety.MaxBlocksPerGuest+1, rec.Code, rec.Body)
	}
	if e.policy.Blocks(me, "one-too-many") || e.rooms.sweeps != 0 {
		t.Error("a refused block changed the policy or swept")
	}
	// Re-blocking someone already blocked is not a new block.
	if rec := e.block(blockBody("t007", ""), c, withRemote("10.0.0.3:1")); rec.Code != http.StatusOK {
		t.Errorf("a replay at the limit = %d", rec.Code)
	}
	// Unblocking makes room.
	if rec := e.do(http.MethodDelete, "/api/blocks/t007", "", withOrigin(prodOrigin), withCookie(c), withRemote("10.0.0.4:1")); rec.Code != http.StatusNoContent {
		t.Fatalf("unblock: %d", rec.Code)
	}
	if rec := e.block(blockBody("one-too-many", ""), c, withRemote("10.0.0.5:1")); rec.Code != http.StatusOK {
		t.Errorf("after an unblock: %d %s", rec.Code, rec.Body)
	}
}

func TestBlockFailsClosedWhenTheStoreOrTheSweepFails(t *testing.T) {
	e := newBlockEnv(t, nil)
	c, me := e.guest(t)

	e.mod.failAdd.Store(true)
	rec := e.block(blockBody("target-id", ""), c)
	if rec.Code != http.StatusServiceUnavailable || errorCode(t, rec) != "server_busy" || rec.Header().Get("Retry-After") == "" {
		t.Errorf("store failure: %d %s", rec.Code, rec.Body)
	}
	if e.policy.Blocks(me, "target-id") {
		t.Error("the policy learned of a block that was not stored: it would be enforced but not durable")
	}
	if e.rooms.sweeps != 0 {
		t.Error("swept after a failed write")
	}
	e.mod.failAdd.Store(false)

	// The row is durable and the policy knows it, but the rooms could not be
	// swept: not acknowledged (the client retries), and the retry completes it.
	e.rooms.sweepErr = room.ErrShuttingDown
	rec = e.block(blockBody("target-id", ""), c, withRemote("10.0.0.2:1"))
	if rec.Code != http.StatusServiceUnavailable || errorCode(t, rec) != "server_busy" {
		t.Errorf("sweep failure: %d %s", rec.Code, rec.Body)
	}
	if !e.policy.Blocks(me, "target-id") {
		t.Error("admission must already refuse the pair: the row is durable")
	}
	e.rooms.sweepErr = nil
	if rec := e.block(blockBody("target-id", ""), c, withRemote("10.0.0.3:1")); rec.Code != http.StatusOK {
		t.Errorf("retry: %d %s", rec.Code, rec.Body)
	}
}

func TestListBlocksNewestFirstAndPrivate(t *testing.T) {
	e := newBlockEnv(t, nil)
	c, me := e.guest(t)
	c2, _ := e.guest(t)
	for i, name := range []string{"Ana", "Beto", "Cami"} {
		e.now.Advance(time.Minute)
		rec := e.block(blockBody(fmt.Sprintf("t%d", i), name), c, withRemote(fmt.Sprintf("10.0.1.%d:1", i)))
		if rec.Code != http.StatusOK {
			t.Fatal(rec.Body)
		}
	}
	rec := e.do(http.MethodGet, "/api/blocks", "", withCookie(c))
	if rec.Code != http.StatusOK || rec.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("status %d, cache %q", rec.Code, rec.Header().Get("Cache-Control"))
	}
	var list api.BlocksResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &list); err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, b := range list.Blocks {
		names = append(names, b.Name)
	}
	if !slices.Equal(names, []string{"Cami", "Beto", "Ana"}) {
		t.Errorf("blocks = %v, want newest first", names)
	}
	// Somebody else's list is their own.
	rec = e.do(http.MethodGet, "/api/blocks", "", withCookie(c2))
	if !strings.Contains(rec.Body.String(), `"blocks":[]`) {
		t.Errorf("another guest's list = %s", rec.Body)
	}
	// And nobody can read who blocked them.
	if strings.Contains(rec.Body.String(), me) {
		t.Error("a list names a blocker")
	}
	e.mod.failList.Store(true)
	if rec := e.do(http.MethodGet, "/api/blocks", "", withCookie(c)); rec.Code != http.StatusServiceUnavailable {
		t.Errorf("list failure: %d", rec.Code)
	}
}

func TestUnblockIsIdempotentAndNeverRejoins(t *testing.T) {
	e := newBlockEnv(t, nil)
	c, me := e.guest(t)
	e.block(blockBody("target-id", "Beto"), c)
	sweeps := e.rooms.sweeps

	for i := range 2 {
		rec := e.do(http.MethodDelete, "/api/blocks/target-id", "", withOrigin(prodOrigin), withCookie(c), withRemote(fmt.Sprintf("10.0.2.%d:1", i)))
		if rec.Code != http.StatusNoContent || rec.Body.Len() != 0 {
			t.Errorf("unblock #%d: %d %q", i+1, rec.Code, rec.Body)
		}
	}
	if e.policy.Blocks(me, "target-id") {
		t.Error("the policy still holds the block")
	}
	if list, _ := e.mod.ListBlocks(context.Background(), me); len(list) != 0 {
		t.Errorf("the row is still there: %+v", list)
	}
	if e.rooms.sweeps != sweeps {
		t.Error("an unblock swept the rooms: it must never move anybody")
	}
	// A block that never existed is the same answer.
	if rec := e.do(http.MethodDelete, "/api/blocks/never-blocked", "", withOrigin(prodOrigin), withCookie(c), withRemote("10.0.2.9:1")); rec.Code != http.StatusNoContent {
		t.Errorf("unblocking a stranger: %d", rec.Code)
	}
	if rec := e.do(http.MethodDelete, "/api/blocks/a%20b", "", withOrigin(prodOrigin), withCookie(c), withRemote("10.0.2.10:1")); rec.Code != http.StatusBadRequest || errorCode(t, rec) != api.ErrCodeInvalidBlock {
		t.Errorf("malformed id: %d %s", rec.Code, rec.Body)
	}

	// A failing store keeps the policy as it was: the block still stands.
	e.block(blockBody("target-id", "Beto"), c, withRemote("10.0.2.11:1"))
	e.mod.failRemove.Store(true)
	rec := e.do(http.MethodDelete, "/api/blocks/target-id", "", withOrigin(prodOrigin), withCookie(c), withRemote("10.0.2.12:1"))
	if rec.Code != http.StatusServiceUnavailable || !e.policy.Blocks(me, "target-id") {
		t.Errorf("unblock with a failing store: %d, policy still blocks = %v", rec.Code, e.policy.Blocks(me, "target-id"))
	}
}

func TestBlockWritesAreRateLimitedPerGuest(t *testing.T) {
	e := newBlockEnv(t, nil)
	c, _ := e.guest(t)
	c2, _ := e.guest(t)
	for i := range blocksPerHour {
		rec := e.block(blockBody(fmt.Sprintf("t%03d", i), ""), c, withRemote(fmt.Sprintf("10.2.%d.%d:1", i/200, i%200)))
		if rec.Code != http.StatusOK {
			t.Fatalf("block %d: %d %s", i+1, rec.Code, rec.Body)
		}
	}
	rec := e.block(blockBody("one-more", ""), c, withRemote("10.3.0.1:1"))
	if rec.Code != http.StatusTooManyRequests || rec.Header().Get("Retry-After") == "" {
		t.Errorf("block %d: %d, want 429 with Retry-After", blocksPerHour+1, rec.Code)
	}
	if rec := e.do(http.MethodDelete, "/api/blocks/t001", "", withOrigin(prodOrigin), withCookie(c), withRemote("10.3.0.2:1")); rec.Code != http.StatusTooManyRequests {
		t.Errorf("unblock shares the budget: %d", rec.Code)
	}
	if rec := e.block(blockBody("t001", ""), c2, withRemote("10.3.0.3:1")); rec.Code != http.StatusOK {
		t.Errorf("another guest is limited by the first one's budget: %d", rec.Code)
	}
	e.now.Advance(time.Hour)
	if rec := e.block(blockBody("one-more", ""), c, withRemote("10.3.0.4:1")); rec.Code != http.StatusOK {
		t.Errorf("an hour later: %d", rec.Code)
	}
}

func TestBlockPerIPBucketCapsCookieChurn(t *testing.T) {
	e := newBlockEnv(t, nil)
	var last int
	for i := range blockIPBurst + 2 {
		c, _ := e.guest(t)
		last = e.block(blockBody("target-id", ""), c).Code // same remote address for all
		if i < blockIPBurst && last != http.StatusOK {
			t.Fatalf("request %d: %d", i+1, last)
		}
	}
	if last != http.StatusTooManyRequests {
		t.Errorf("past the burst: %d, want 429", last)
	}
}

func TestBlockingNeverNeedsTheTerms(t *testing.T) {
	e := newBlockEnv(t, func(o *Options) {
		o.Terms = safety.NewTerms(store.NewMemory(), nil) // nobody accepted anything
	})
	c, _ := e.guest(t)
	if rec := e.block(blockBody("target-id", ""), c); rec.Code != http.StatusOK {
		t.Errorf("block without accepted terms: %d %s", rec.Code, rec.Body)
	}
}

func TestBlockEndpointsAreNotRegisteredWithoutAnEnforcer(t *testing.T) {
	// The room double of the other tests cannot sweep: a block nobody can
	// enforce must not be offered.
	e := newRESTEnv(t, func(o *Options) {
		o.Moderation = store.NewMemory()
		o.Policy = safety.NewPolicy(nil)
	})
	c := guestCookie(t, e.do(http.MethodGet, "/api/session", ""))
	rec := e.do(http.MethodPost, "/api/blocks", blockBody("target-id", ""), withOrigin(prodOrigin), withJSON(), withCookie(c))
	if rec.Code != http.StatusNotFound {
		t.Errorf("POST /api/blocks = %d, want 404", rec.Code)
	}
	nobody := newBlockEnv(t, func(o *Options) { o.Moderation = nil })
	cc, _ := nobody.guest(t)
	if rec := nobody.block(blockBody("target-id", ""), cc); rec.Code != http.StatusNotFound {
		t.Errorf("POST /api/blocks without a store = %d, want 404", rec.Code)
	}
}

func TestDirectoryAndRoomInfoKnowTheCaller(t *testing.T) {
	e := newBlockEnv(t, nil)
	c, me := e.guest(t)
	e.rooms.public = []api.PublicRoom{{Code: "AAAA"}, {Code: "BBBB"}}
	e.rooms.hidden["AAAA"] = true
	e.rooms.hidden["KT7P"] = true

	read := func(opts ...reqOpt) []string {
		rec := e.do(http.MethodGet, "/api/rooms", "", opts...)
		var out api.PublicRoomsResponse
		if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
			t.Fatal(err)
		}
		var codes []string
		for _, r := range out.Rooms {
			codes = append(codes, r.Code)
		}
		return codes
	}
	if got := read(withCookie(c)); !slices.Equal(got, []string{"BBBB"}) {
		t.Errorf("directory for the caller = %v", got)
	}
	if got := read(); !slices.Equal(got, []string{"AAAA", "BBBB"}) {
		t.Errorf("an anonymous caller must see everything: %v", got)
	}
	if got := read(withCookie(&http.Cookie{Name: c.Name, Value: "forged.cookie"})); !slices.Equal(got, []string{"AAAA", "BBBB"}) {
		t.Errorf("a forged cookie identifies nobody: %v", got)
	}
	if !slices.Contains(e.rooms.listedFor, me) || !slices.Contains(e.rooms.listedFor, "") {
		t.Errorf("the directory was asked for %v", e.rooms.listedFor)
	}

	info := func(opts ...reqOpt) room.Info {
		rec := e.do(http.MethodGet, "/api/rooms/KT7P", "", opts...)
		var out room.Info
		if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil || rec.Code != 200 {
			t.Fatalf("%d %s %v", rec.Code, rec.Body, err)
		}
		return out
	}
	if got := info(withCookie(c)); got.Joinable {
		t.Errorf("RoomInfo for the blocked caller = %+v, want joinable false", got)
	}
	if got := info(); !got.Joinable {
		t.Errorf("RoomInfo for an anonymous caller = %+v", got)
	}
}

func TestCreateRoomRestrictedAndAccountForwarded(t *testing.T) {
	e := newBlockEnv(t, nil)
	c, me := e.guest(t)

	e.rooms.create = func(context.Context, room.CreateRequest) (string, error) { return "", room.ErrRestricted }
	rec := e.post(validBody, withCookie(c))
	if rec.Code != http.StatusForbidden || errorCode(t, rec) != api.ErrCodeJoinUnavailable {
		t.Errorf("restricted create: %d %s, want 403 join_unavailable", rec.Code, rec.Body)
	}
	if raw := strings.ToLower(rec.Body.String()); strings.Contains(raw, "restrict") || strings.Contains(raw, "moderat") {
		t.Errorf("the refusal says why: %s", raw)
	}

	// The signed-in account travels to the hub, which refuses a restricted one.
	e.rooms.create = nil
	user := "00000000-0000-4000-8000-0000000000a1"
	sess := httptest.NewRecorder()
	if err := e.srv.sessions.Issue(sess, user); err != nil {
		t.Fatal(err)
	}
	if rec := e.post(validBody, withCookie(c), withCookie(sess.Result().Cookies()[0])); rec.Code != http.StatusCreated {
		t.Fatalf("create: %d %s", rec.Code, rec.Body)
	}
	e.rooms.mu.Lock()
	last := e.rooms.created[len(e.rooms.created)-1]
	e.rooms.mu.Unlock()
	if last.PlayerID != me || last.UserID != user {
		t.Errorf("CreateRequest = %+v, want the guest and the account", last)
	}
}

func TestReportObserverSeesStoredReportsOnly(t *testing.T) {
	var (
		mu   sync.Mutex
		seen []time.Time
	)
	e := newSafetyEnv(t, safetyOpts{acct: acctOpts{tweak: func(o *Options) {
		o.Reports = observerFunc(func(at time.Time) {
			mu.Lock()
			seen = append(seen, at)
			mu.Unlock()
		})
	}}})
	e.rooms.reply = func(string, safety.EvidenceRequest) (safety.Evidence, error) { return serverEvidence(), nil }
	b := e.browser()
	b.get("/api/session")

	if r := b.postJSON("/api/reports", reportBody(nil)); r.StatusCode != http.StatusCreated {
		t.Fatalf("report = %d %s", r.StatusCode, r.body)
	}
	if r := b.postJSON("/api/reports", reportBody(nil)); r.StatusCode != http.StatusOK { // an idempotent replay
		t.Fatalf("replay = %d %s", r.StatusCode, r.body)
	}
	e.safe.failReports.Store(true)
	if r := b.postJSON("/api/reports", reportBody(func(m map[string]any) { m["idempotencyKey"] = "key-0000002" })); r.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("failing store = %d", r.StatusCode)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(seen) != 1 {
		t.Errorf("the observer heard of %d reports, want exactly the one that was stored", len(seen))
	}
}

type observerFunc func(time.Time)

func (f observerFunc) ReportCreated(at time.Time) { f(at) }
