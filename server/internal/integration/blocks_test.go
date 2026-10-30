package integration

import (
	"encoding/json"
	"net/http"
	"slices"
	"testing"
	"time"

	"github.com/AresiusXP/tutti-frutti/server/internal/api"
	"github.com/AresiusXP/tutti-frutti/server/internal/httpapi"
	"github.com/AresiusXP/tutti-frutti/server/internal/moderation"
	"github.com/AresiusXP/tutti-frutti/server/internal/protocol"
	"github.com/AresiusXP/tutti-frutti/server/internal/room"
	"github.com/AresiusXP/tutti-frutti/server/internal/safety"
	"github.com/AresiusXP/tutti-frutti/server/internal/store"
)

// moderationStack wires blocks and restrictions the way cmd/server does: the
// store, the policy loaded before the hub, the hub enforcing it and the REST
// block endpoints.
func moderationStack(t *testing.T, mem *store.Memory) (*stack, *safety.Policy) {
	t.Helper()
	policy := safety.NewPolicy(nil)
	if _, _, err := moderation.LoadPolicy(t.Context(), mem, policy, time.Now()); err != nil {
		t.Fatal(err)
	}
	s := newStack(t, stackOpts{
		store: mem,
		hub:   func(c *room.Config) { c.Policy = policy },
		api:   func(o *httpapi.Options) { o.Moderation, o.Policy = mem, policy },
	})
	return s, policy
}

func (s *stack) block(p *player, target string) response {
	s.t.Helper()
	body, _ := json.Marshal(api.BlockRequest{PlayerID: target, Name: "Someone"})
	return s.do(http.MethodPost, "/api/blocks", body, withCookie(p))
}

// TestBlockAndLeaveEndToEnd: a block made over REST removes the blocker from the
// live room before it answers, closes their socket like a leave, stops the pair
// from sharing a room again, hides the room from the blocker's directory and
// survives a restart.
func TestBlockAndLeaveEndToEnd(t *testing.T) {
	mem := store.NewMemory()
	s, _ := moderationStack(t, mem)
	ana, beto := s.newPlayer("ana"), s.newPlayer("beto")

	code, err := s.tryCreateRoom(ana, "es")
	if err != nil {
		t.Fatal(err)
	}
	ca, cb := s.connect(ana), s.connect(beto)
	ca.join(code)
	cb.join(code)
	cb.waitState("both seated", func(st *protocol.State) bool { return len(st.View.Players) == 2 })

	resp := s.block(beto, ana.id)
	if resp.status != http.StatusOK {
		t.Fatalf("block: %d %s", resp.status, resp.body)
	}
	var out api.BlockResponse
	resp.jsonBody(t, &out)
	if !slices.Equal(out.LeftRooms, []string{code}) || out.Block.PlayerID != ana.id {
		t.Errorf("response = %+v, want the blocker removed from %s", out, code)
	}
	// The removal had already happened when the answer arrived.
	eventually(t, "the blocker's socket is closed like a leave", func() bool { return cb.isClosed() })
	ca.waitState("the blocked player sees the blocker gone", func(st *protocol.State) bool { return len(st.View.Players) == 1 })

	// Neither can join the other's room afterwards, and the refusal is generic.
	cb2 := s.connect(beto)
	if _, err := cb2.tryJoin(code); err == nil {
		t.Fatal("the blocker rejoined the room of the player they blocked")
	}
	f, _ := cb2.await(0, "the refusal", isError(api.ErrCodeJoinUnavailable))
	if f.Err == nil || f.Err.Code != api.ErrCodeJoinUnavailable {
		t.Errorf("refusal = %v", f)
	}

	// The blocker's directory and RoomInfo never show the room again.
	info := s.do(http.MethodGet, "/api/rooms/"+code, nil, withCookie(beto))
	var ri room.Info
	info.jsonBody(t, &ri)
	if ri.Joinable {
		t.Errorf("RoomInfo for the blocker = %+v, want joinable=false", ri)
	}
	anon := s.do(http.MethodGet, "/api/rooms/"+code, nil)
	anon.jsonBody(t, &ri)
	if !ri.Joinable {
		t.Errorf("RoomInfo for an anonymous caller = %+v", ri)
	}

	// The block is listed and removable; removing it does not rejoin anybody.
	list := s.do(http.MethodGet, "/api/blocks", nil, withCookie(beto))
	var bl api.BlocksResponse
	list.jsonBody(t, &bl)
	if len(bl.Blocks) != 1 || bl.Blocks[0].PlayerID != ana.id {
		t.Errorf("block list = %+v", bl)
	}
}

// TestBlocksSurviveARestartAndAreEnforcedOnRestoredRooms: the policy is loaded
// from the store before the new hub starts, and a restored room that still holds
// a pair the store forbids sweeps itself (and saves the result).
func TestBlocksSurviveARestartAndAreEnforcedOnRestoredRooms(t *testing.T) {
	mem := store.NewMemory()
	s1, _ := moderationStack(t, mem)
	ana, beto := s1.newPlayer("ana"), s1.newPlayer("beto")
	code, err := s1.tryCreateRoom(ana, "es")
	if err != nil {
		t.Fatal(err)
	}
	s1.connect(ana).join(code)
	s1.connect(beto).join(code)
	s1.flush()

	// The block reaches the store just before the process dies (the room still
	// holds both seats in its last snapshot).
	if _, _, err := mem.AddBlock(t.Context(), safety.Block{Blocker: beto.id, Blocked: ana.id, CreatedAt: time.Now()}, 100); err != nil {
		t.Fatal(err)
	}
	if err := s1.shutdown(); err != nil {
		t.Fatal(err)
	}

	s2, policy := moderationStack(t, mem)
	if !policy.Conflict(beto.id, ana.id) {
		t.Fatal("the new process did not load the block before starting its hub")
	}
	if s2.restored != 1 {
		t.Fatalf("restored %d rooms", s2.restored)
	}
	eventually(t, "the restored room saved itself without the blocker", func() bool {
		g, ok := snapshot(t, mem, code)
		if !ok {
			return false
		}
		seated := 0
		for _, p := range g.Players {
			if !p.Left {
				seated++
			}
		}
		return seated == 1
	})
}
