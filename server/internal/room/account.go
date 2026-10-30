package room

import (
	"context"
	"errors"
	"sort"
	"sync"
	"time"

	"github.com/AresiusXP/tutti-frutti/server/internal/safety"
	"github.com/AresiusXP/tutti-frutti/server/internal/store"
)

// Deleting an account while its owner is in a game (docs/DESIGN.md, "Deleting an
// account while a game is on").
//
// A seat is the guest's, whatever account the player was signed in with; the
// account only adds links on top of it: the connection's own (session.userID: the
// AI judge, the statistics and report attribution of later joins), the room's
// (users for the statistics, accounts and labelAuthors for reports) and the
// finished games waiting in the outbox (pending). Deleting the account removes
// the links and nothing else: the seat, its answers and its scores stay.
//
// Hub.ForgetAccount is the entry point. It runs after the store deleted the
// account and does two things before the HTTP response:
//
//  1. it remembers the id (forgotten), so that a connection that was identified
//     before the deletion and attaches to a room after it still comes up as a
//     guest (onConnect), and
//  2. it posts one task per live room, which drops every link in the room's own
//     goroutine, saves the room and tells the players their state changed (canJudge).
//
// Tasks are ordered with the frames of the room's mailbox. A frame that reaches
// a room after the deletion returned is queued behind the task, so whatever the
// room does with it, it does without the account; a frame that was queued before
// was accepted while the account existed, which is before the response.
// Rooms created after ForgetAccount snapshot the room list never have the
// link (their sockets pass through onConnect, which consults forgotten), and rooms
// restored at a later start check the accounts store (forgetDeletedAccounts).

const (
	// forgottenTTL is how long the hub remembers a deleted account. It only has to
	// outlive the connections identified before the deletion, which either attach
	// to a room within the transport's join timeout or are closed; a connection
	// opened later asks the sessions, which know the account is gone.
	forgottenTTL = 5 * time.Minute
	// maxForgotten bounds the memory of deleted accounts.
	maxForgotten = 4096
	// accountCheckTimeout bounds one store lookup of the restore.
	accountCheckTimeout = 3 * time.Second
)

// Values of the "source" label of tuttifrutti_stats_skipped_users_total: where a
// user's share of a finished game was given up because the account is gone.
const (
	skipForgotten = "forgotten" // a room dropped it when the account was deleted
	skipRestore   = "restore"   // a restored snapshot named an account deleted while the server was down
	skipStore     = "store"     // the store skipped it at delivery (the account was deleted meanwhile)
)

var skipSources = []string{skipForgotten, skipRestore, skipStore}

// AccountForgetter is optional for transports, like PublicRoomLister: what
// DELETE /api/me needs from the hub once the store deleted the account.
type AccountForgetter interface {
	// ForgetAccount drops every live link to the (already deleted) account: the
	// rooms' state, their outboxes and the connections that were opened as that
	// user. The guest seats stay. It returns once every room has done it, or ctx's
	// error; the rooms were handed the order either way.
	ForgetAccount(ctx context.Context, userID string) error
}

var _ AccountForgetter = (*Hub)(nil)

// AccountDirectory is what the restore asks the accounts store: whether the
// accounts named by a snapshot still exist. store.Accounts implements it;
// Config.Accounts, or else the Recorder when that implements it, provides it.
type AccountDirectory interface {
	// GetUser returns the user, or an error wrapping store.ErrUserNotFound.
	GetUser(ctx context.Context, id string) (store.User, error)
}

// forgottenAccounts is the hub's memory of the accounts deleted by this process.
type forgottenAccounts struct {
	mu    sync.Mutex
	until map[string]time.Time
}

func (f *forgottenAccounts) add(id string, now time.Time) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.until == nil {
		f.until = map[string]time.Time{}
	}
	if len(f.until) >= maxForgotten {
		for k, u := range f.until {
			if !now.Before(u) {
				delete(f.until, k)
			}
		}
		if len(f.until) >= maxForgotten {
			clear(f.until)
		}
	}
	f.until[id] = now.Add(forgottenTTL)
}

func (f *forgottenAccounts) has(id string, now time.Time) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	until, ok := f.until[id]
	return ok && now.Before(until)
}

// ForgetAccount implements AccountForgetter.
func (h *Hub) ForgetAccount(ctx context.Context, userID string) error {
	if userID == "" {
		return nil
	}
	h.forgotten.add(userID, h.clock.Now())

	h.mu.Lock()
	rooms := make([]*room, 0, len(h.rooms))
	for _, r := range h.rooms {
		rooms = append(rooms, r)
	}
	h.mu.Unlock()

	done := make([]chan struct{}, len(rooms))
	for i, r := range rooms {
		ch := make(chan struct{})
		done[i] = ch
		r.post(func(r *room) {
			defer close(ch)
			r.forgetAccount(userID)
		}, func() { close(ch) })
	}
	for _, ch := range done {
		select {
		case <-ch:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return nil
}

// dropForgottenAccount makes a connection that was identified as a user deleted
// since a guest. It runs in onConnect, in the room's goroutine, so it is ordered
// with forgetAccount: either the session is attached before that task (which
// then clears it) or after it (when the hub already remembers the account).
func (r *room) dropForgottenAccount(s *session) {
	if s.userID != "" && r.h.forgotten.has(s.userID, r.clock.Now()) {
		s.userID = ""
	}
}

// forgetAccount runs in the room's goroutine: it removes every link to userID
// from the room's live state and saves the result.
func (r *room) forgetAccount(userID string) {
	if r.closed {
		return
	}
	now := r.clock.Now()
	revoked, notify := 0, false
	for s := range r.sessions {
		if s.userID == userID {
			s.userID = ""
			revoked++
			notify = notify || s.joined
		}
	}
	var sc scrubbed
	r.pending, sc = scrubAccountLinks(r.users, r.accounts, r.labelAuthors, r.pending, func(id string) bool { return id == userID })
	r.syncOutbox()
	if revoked == 0 && !sc.any() {
		return
	}
	r.h.m.statsSkipped.WithLabelValues(skipForgotten).Add(float64(sc.participations))
	r.log.Info("forgot a deleted account", "sessions", revoked, "links", sc.links, "labels", sc.labels,
		"outbox_participations", sc.participations, "outbox_records", sc.records)
	if r.stopping {
		return // the final snapshot is taken; the next start checks the accounts again
	}
	r.dirty = true
	if notify {
		// The player's canJudge changed with the account: tell them (everybody
		// gets the same, unchanged, state).
		r.broadcast(now, nil)
	}
	r.persist(now)
	r.rearm()
}

// scrubbed counts what scrubAccountLinks removed.
type scrubbed struct {
	links          int // player -> user links (statistics and reports)
	labels         int // custom-label authors that lost their account
	participations int // results of the user taken out of the outbox's records
	records        int // outbox records left without any result, removed
}

func (s scrubbed) any() bool { return s != scrubbed{} }

// scrubAccountLinks removes the accounts for which gone is true from the places
// of a room's state that name one: the statistics links (users), the report
// links (accounts), the authors of custom labels (they keep their guest id) and
// the outbox. A record keeps the results of everybody else; one that is left
// with none is removed. users, accounts and authors are edited in place; the
// outbox is returned as a new slice and records are never edited (the recorder
// may hold a copy of one: the results of a changed record are a new slice).
func scrubAccountLinks(users, accounts map[string]string, authors map[string]safety.Author, pending []store.GameRecord, gone func(id string) bool) ([]store.GameRecord, scrubbed) {
	var sc scrubbed
	for player, user := range users {
		if gone(user) {
			delete(users, player)
			sc.links++
		}
	}
	for player, user := range accounts {
		if gone(user) {
			delete(accounts, player)
			sc.links++
		}
	}
	for label, a := range authors {
		if a.User != "" && gone(a.User) {
			a.User = ""
			authors[label] = a
			sc.labels++
		}
	}
	if len(pending) == 0 {
		return pending, sc
	}
	kept := make([]store.GameRecord, 0, len(pending))
	for _, rec := range pending {
		results := make([]store.UserResult, 0, len(rec.Results))
		for _, res := range rec.Results {
			if gone(res.UserID) {
				sc.participations++
				continue
			}
			results = append(results, res)
		}
		switch {
		case len(results) == len(rec.Results):
			kept = append(kept, rec)
		case len(results) == 0:
			sc.records++
		default:
			rec.Results = results
			kept = append(kept, rec)
		}
	}
	return kept, sc
}

// accountDirectory is where the restore looks accounts up: Config.Accounts, or
// the Recorder when that is a store that keeps accounts (cmd/server passes the
// store as both). Nil: restored snapshots are trusted as they are (tests).
func (h *Hub) accountDirectory() AccountDirectory {
	if h.cfg.Accounts != nil {
		return h.cfg.Accounts
	}
	if d, ok := h.cfg.Recorder.(AccountDirectory); ok {
		return d
	}
	return nil
}

// linkedAccounts lists, sorted, the accounts a restored snapshot names.
func linkedAccounts(p *Persisted) []string {
	set := map[string]struct{}{}
	for _, user := range p.Users {
		set[user] = struct{}{}
	}
	for _, user := range p.Accounts {
		set[user] = struct{}{}
	}
	for _, a := range p.LabelAuthors {
		if a.User != "" {
			set[a.User] = struct{}{}
		}
	}
	for _, rec := range p.Pending {
		for _, res := range rec.Results {
			set[res.UserID] = struct{}{}
		}
	}
	ids := make([]string, 0, len(set))
	for id := range set {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

// forgetDeletedAccounts is the restore's version of ForgetAccount. A snapshot is
// written while the process runs and read by the next one: an account deleted
// in between (or while the shutdown was writing the final snapshots) is named by
// a snapshot nobody could scrub. So the accounts a snapshot names are looked up
// in the store before the room is rebuilt, and the ones that are gone are
// removed exactly as ForgetAccount would have. It runs on the restoring
// goroutine, never in an actor.
//
// A lookup that fails for any other reason keeps the links (and logs it): the
// restore itself has just read the same store, and the links grant nothing by
// themselves (every capability comes from a live, verified connection; a result
// for a missing user is skipped by the store).
func (h *Hub) forgetDeletedAccounts(ctx context.Context, p *Persisted) {
	dir := h.accountDirectory()
	if dir == nil {
		return
	}
	ids := linkedAccounts(p)
	if len(ids) == 0 {
		return
	}
	gone := map[string]bool{}
	for _, id := range ids {
		lookup, cancel := context.WithTimeout(ctx, accountCheckTimeout)
		_, err := dir.GetUser(lookup, id)
		cancel()
		switch {
		case err == nil:
		case errors.Is(err, store.ErrUserNotFound):
			gone[id] = true
		default:
			h.log.Warn("could not check whether an account of a restored room still exists; keeping its links", "room", p.Code, "err", err)
		}
	}
	if len(gone) == 0 {
		return
	}
	var sc scrubbed
	p.Pending, sc = scrubAccountLinks(p.Users, p.Accounts, p.LabelAuthors, p.Pending, func(id string) bool { return gone[id] })
	h.m.statsSkipped.WithLabelValues(skipRestore).Add(float64(sc.participations))
	h.log.Info("dropped the links of deleted accounts from a restored room", "room", p.Code, "accounts", len(gone),
		"links", sc.links, "labels", sc.labels, "outbox_participations", sc.participations, "outbox_records", sc.records)
}
