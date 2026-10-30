package safety

import (
	"slices"
	"sync"
	"sync/atomic"
	"time"
)

// Policy is the in-memory index of the moderation records that decide who may
// share a room: the blocked pairs and the restrictions in force. It is the
// single source the room actors consult, so that admission is a pure memory
// lookup inside the actor (no I/O) and the REST handlers and the operator tool
// can change it in one place.
//
// The rules of use (docs/DESIGN.md, "Safety"), which the enforcement argument
// depends on:
//
//   - Load it from the store before the hub accepts joins.
//   - Change it only AFTER the database write succeeded, and only then tell the
//     rooms to re-evaluate themselves (room.Hub.Sweep): every actor reads it
//     inside its own serialized join path, so a join either sees the change or
//     is ordered before the sweep, which then removes whatever the join let in.
//
// Restrictions expire by the clock (ActiveAt), so nothing has to run when one
// ends. All methods are safe for concurrent use and nil-safe: a nil *Policy
// allows everything (tests, tools).
//
// Writers are serialised outside this type: the REST handlers of a block and an
// unblock hold a per-blocker lock (httpapi.keyedMutex) across the database write,
// the Policy update and the sweep, so the policy and the database cannot end up
// disagreeing after two racing requests. The process is a single replica, which
// is why a process-level lock is enough. Lock order, outermost first:
//
//	httpapi blockLocks[blocker]   only the block and unblock handlers take it
//	  -> store                    database I/O
//	  -> room.Hub.Sweep           Hub.mu briefly, then the actors' mailboxes
//	Policy.mu                     a leaf: nothing is called while it is held, so it
//	                              may be taken under any of the above (Hub.Create
//	                              reads it under Hub.mu) and inside an actor
//
// Actors and every admission check only take Policy's read lock and never the
// writers' lock, so a join can never wait for a block in progress and there is no
// cycle with Hub.mu or the mailboxes.
type Policy struct {
	now     func() time.Time
	version atomic.Uint64

	mu           sync.RWMutex
	blocks       map[string]map[string]struct{} // blocker -> blocked
	nblocks      int
	restrictions map[string]Restriction // by id
	byGuest      map[string][]string    // guest id -> restriction ids
	byUser       map[string][]string    // user id -> restriction ids
}

// NewPolicy returns an empty Policy. now defaults to time.Now.
func NewPolicy(now func() time.Time) *Policy {
	if now == nil {
		now = time.Now
	}
	return &Policy{
		now:          now,
		blocks:       map[string]map[string]struct{}{},
		restrictions: map[string]Restriction{},
		byGuest:      map[string][]string{},
		byUser:       map[string][]string{},
	}
}

// Version counts the changes made to the policy; it only ever grows.
func (p *Policy) Version() uint64 {
	if p == nil {
		return 0
	}
	return p.version.Load()
}

// Load replaces the whole policy with the stored records (startup).
func (p *Policy) Load(blocks []Block, restrictions []Restriction) {
	if p == nil {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.blocks = map[string]map[string]struct{}{}
	p.nblocks = 0
	p.restrictions = map[string]Restriction{}
	p.byGuest = map[string][]string{}
	p.byUser = map[string][]string{}
	for _, b := range blocks {
		p.addBlockLocked(b.Blocker, b.Blocked)
	}
	for _, r := range restrictions {
		p.addRestrictionLocked(r)
	}
	p.version.Add(1)
}

func (p *Policy) addBlockLocked(blocker, blocked string) {
	set := p.blocks[blocker]
	if set == nil {
		set = map[string]struct{}{}
		p.blocks[blocker] = set
	}
	if _, ok := set[blocked]; !ok {
		set[blocked] = struct{}{}
		p.nblocks++
	}
}

// AddBlock records that blocker blocked blocked. Call it after the row is
// stored.
func (p *Policy) AddBlock(blocker, blocked string) {
	if p == nil {
		return
	}
	p.mu.Lock()
	p.addBlockLocked(blocker, blocked)
	p.version.Add(1)
	p.mu.Unlock()
}

// RemoveBlock forgets the block (an unblock). Call it after the row is gone.
func (p *Policy) RemoveBlock(blocker, blocked string) {
	if p == nil {
		return
	}
	p.mu.Lock()
	if set := p.blocks[blocker]; set != nil {
		if _, ok := set[blocked]; ok {
			delete(set, blocked)
			p.nblocks--
			if len(set) == 0 {
				delete(p.blocks, blocker)
			}
		}
	}
	p.version.Add(1)
	p.mu.Unlock()
}

// Blocks reports whether blocker blocked blocked (directed).
func (p *Policy) Blocks(blocker, blocked string) bool {
	if p == nil {
		return false
	}
	p.mu.RLock()
	defer p.mu.RUnlock()
	_, ok := p.blocks[blocker][blocked]
	return ok
}

// Conflict reports whether a and b must not share a room: either blocked the
// other.
func (p *Policy) Conflict(a, b string) bool {
	if p == nil || a == b {
		return false
	}
	p.mu.RLock()
	defer p.mu.RUnlock()
	if _, ok := p.blocks[a][b]; ok {
		return true
	}
	_, ok := p.blocks[b][a]
	return ok
}

func (p *Policy) addRestrictionLocked(r Restriction) {
	if old, ok := p.restrictions[r.ID]; ok {
		p.removeRestrictionLocked(old.ID)
	}
	p.restrictions[r.ID] = r
	p.byGuest[r.GuestID] = append(p.byGuest[r.GuestID], r.ID)
	if r.UserID != "" {
		p.byUser[r.UserID] = append(p.byUser[r.UserID], r.ID)
	}
}

func (p *Policy) removeRestrictionLocked(id string) {
	r, ok := p.restrictions[id]
	if !ok {
		return
	}
	delete(p.restrictions, id)
	drop := func(idx map[string][]string, key string) {
		ids := slices.DeleteFunc(idx[key], func(s string) bool { return s == id })
		if len(ids) == 0 {
			delete(idx, key)
		} else {
			idx[key] = ids
		}
	}
	drop(p.byGuest, r.GuestID)
	if r.UserID != "" {
		drop(p.byUser, r.UserID)
	}
}

// AddRestriction puts a restriction in force (or replaces the one with the
// same id). Call it after the row is stored.
func (p *Policy) AddRestriction(r Restriction) {
	if p == nil {
		return
	}
	p.mu.Lock()
	p.addRestrictionLocked(r)
	p.version.Add(1)
	p.mu.Unlock()
}

// RemoveRestriction lifts a restriction (a revocation). Call it after the row
// is updated.
func (p *Policy) RemoveRestriction(id string) {
	if p == nil {
		return
	}
	p.mu.Lock()
	p.removeRestrictionLocked(id)
	p.version.Add(1)
	p.mu.Unlock()
}

func (p *Policy) activeLocked(idx map[string][]string, key string) (Restriction, bool) {
	now := p.now()
	for _, id := range idx[key] {
		if r := p.restrictions[id]; r.ActiveAt(now) {
			return r, true
		}
	}
	return Restriction{}, false
}

// Restricted reports whether the guest has a restriction in force.
func (p *Policy) Restricted(guestID string) bool {
	_, ok := p.RestrictionOfGuest(guestID)
	return ok
}

// RestrictionOfGuest returns the restriction in force of the guest.
func (p *Policy) RestrictionOfGuest(guestID string) (Restriction, bool) {
	if p == nil || guestID == "" {
		return Restriction{}, false
	}
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.activeLocked(p.byGuest, guestID)
}

// RestrictedUser reports whether the account has a restriction in force.
func (p *Policy) RestrictedUser(userID string) bool {
	if p == nil || userID == "" {
		return false
	}
	p.mu.RLock()
	defer p.mu.RUnlock()
	_, ok := p.activeLocked(p.byUser, userID)
	return ok
}

// Counts returns the number of blocks and of restrictions in force.
func (p *Policy) Counts() (blocks, restrictions int) {
	if p == nil {
		return 0, 0
	}
	p.mu.RLock()
	defer p.mu.RUnlock()
	now := p.now()
	for _, r := range p.restrictions {
		if r.ActiveAt(now) {
			restrictions++
		}
	}
	return p.nblocks, restrictions
}
