package room

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/AresiusXP/tutti-frutti/server/internal/api"
	"github.com/AresiusXP/tutti-frutti/server/internal/game"
)

func setPublic(t *testing.T, e *env, on bool) {
	t.Helper()
	if err := e.hub.SetPublicEnabled(ctxT(t), on); err != nil {
		t.Fatalf("SetPublicEnabled(%v): %v", on, err)
	}
}

func publicGauge(t *testing.T, e *env) float64 {
	t.Helper()
	return metricValue(t, e.reg, "tuttifrutti_public_rooms_enabled")
}

func TestPublicSwitchRefusesPublicRoomsOnly(t *testing.T) {
	e := newEnv(t, func(c *Config) { c.PublicDisabled = true })
	if e.hub.PublicEnabled() || publicGauge(t, e) != 0 {
		t.Fatalf("started off: enabled %v, gauge %v", e.hub.PublicEnabled(), publicGauge(t, e))
	}
	_, err := e.hub.Create(context.Background(), CreateRequest{PlayerID: "host", Name: "Host", Avatar: testAvatar, Visibility: visibilityPublic})
	if !errors.Is(err, ErrPublicUnavailable) {
		t.Fatalf("public Create while off = %v, want ErrPublicUnavailable", err)
	}
	for _, visibility := range []string{"", visibilityPrivate} {
		if _, err := e.hub.Create(context.Background(), CreateRequest{PlayerID: "host", Name: "Host", Avatar: testAvatar, Visibility: visibility}); err != nil {
			t.Fatalf("private Create (%q) while public games are off: %v", visibility, err)
		}
	}

	setPublic(t, e, true)
	if !e.hub.PublicEnabled() || publicGauge(t, e) != 1 {
		t.Fatalf("switched on: enabled %v, gauge %v", e.hub.PublicEnabled(), publicGauge(t, e))
	}
	createPublic(e, "host2", game.LangEN)

	setPublic(t, e, false)
	if publicGauge(t, e) != 0 {
		t.Fatalf("switched off: gauge %v", publicGauge(t, e))
	}
}

func TestPublicSwitchEmptiesTheDirectory(t *testing.T) {
	e, _ := policyEnv(t)
	code := createPublic(e, "host", game.LangEN)
	e.join(code, "host", "Host")
	if got := e.hub.PublicRoomsFor("", "caller"); len(got) != 1 {
		t.Fatalf("directory before = %+v", got)
	}

	setPublic(t, e, false)
	if got := e.hub.PublicRooms(""); len(got) != 0 {
		t.Fatalf("directory while off = %+v", got)
	}
	if got := e.hub.PublicRoomsFor(game.LangEN, "caller"); len(got) != 0 {
		t.Fatalf("directory for a caller while off = %+v", got)
	}
	// The lobby is still there for its players and comes back with the switch.
	setPublic(t, e, true)
	if got := e.hub.PublicRooms(""); len(got) != 1 || got[0].Code != code {
		t.Fatalf("directory after = %+v", got)
	}
}

func TestPublicDirectoryAnswersTheListAndTheSwitchTogether(t *testing.T) {
	e, _ := policyEnv(t)
	code := createPublic(e, "host", game.LangEN)
	e.join(code, "host", "Host")
	if rooms, on := e.hub.PublicDirectory("", "caller"); !on || len(rooms) != 1 {
		t.Fatalf("PublicDirectory while on = %+v, %v", rooms, on)
	}
	setPublic(t, e, false)
	if rooms, on := e.hub.PublicDirectory("", "caller"); on || len(rooms) != 0 {
		t.Fatalf("PublicDirectory while off = %+v, %v", rooms, on)
	}
	setPublic(t, e, true)

	// Flipping concurrently never yields a listed room next to "off" (-race).
	stop := make(chan struct{})
	var wg sync.WaitGroup
	wg.Go(func() {
		for on := false; ; on = !on {
			select {
			case <-stop:
				return
			default:
			}
			_ = e.hub.SetPublicEnabled(context.Background(), on)
		}
	})
	for range 2000 {
		if rooms, on := e.hub.PublicDirectory("", ""); !on && len(rooms) != 0 {
			t.Errorf("a list of %d rooms next to public games off", len(rooms))
			break
		}
	}
	close(stop)
	wg.Wait()
}

func TestPublicSwitchAnonymousLookup(t *testing.T) {
	e, _ := policyEnv(t)
	public := createPublic(e, "host", game.LangEN)
	e.join(public, "host", "Host")
	private := e.create("owner", "Owner")
	e.join(private, "owner", "Owner")
	joinable := func(code, guest string) bool {
		t.Helper()
		var info Info
		var ok bool
		if guest == "" {
			info, ok = e.hub.Info(code)
		} else {
			info, ok = e.hub.InfoFor(code, guest)
		}
		if !ok {
			t.Fatalf("lookup of %s failed", code)
		}
		return info.Joinable
	}
	if !joinable(public, "") || !joinable(private, "") {
		t.Fatal("both rooms are joinable while public games are on")
	}

	setPublic(t, e, false)
	if joinable(public, "") {
		t.Error("Info (anonymous) reports a paused public room as joinable")
	}
	if info, _ := e.hub.InfoFor(public, ""); info.Joinable {
		t.Error("InfoFor with no caller reports a paused public room as joinable")
	}
	if joinable(public, "stranger") {
		t.Error("InfoFor(stranger) reports a paused public room as joinable")
	}
	if !joinable(public, "host") {
		t.Error("the seated host must still be told the room is joinable (a reconnect)")
	}
	if !joinable(private, "") {
		t.Error("a private room must stay joinable")
	}

	setPublic(t, e, true)
	if !joinable(public, "") {
		t.Error("the public room is joinable again once public games are back")
	}
}

func TestSetPublicEnabledWithADoneContextChangesNothing(t *testing.T) {
	e := newEnv(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := e.hub.SetPublicEnabled(ctx, false); !errors.Is(err, context.Canceled) || errors.Is(err, ErrPublicRoomsPending) {
		t.Fatalf("SetPublicEnabled(done ctx) = %v, want context.Canceled and nothing changed", err)
	}
	if !e.hub.PublicEnabled() || publicGauge(t, e) != 1 {
		t.Fatalf("the switch moved although the context was done: enabled %v, gauge %v", e.hub.PublicEnabled(), publicGauge(t, e))
	}
	createPublic(e, "host", game.LangEN)
}

func TestSetPublicEnabledIsInForceEvenWhenARoomIsSlow(t *testing.T) {
	e := newEnv(t)
	code := createPublic(e, "host", game.LangEN)
	release := make(chan struct{})
	e.room(code).post(func(*room) { <-release }, nil) // the room's goroutine is busy
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	err := e.hub.SetPublicEnabled(ctx, false)
	close(release)
	if !errors.Is(err, ErrPublicRoomsPending) || !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("SetPublicEnabled with a stuck room = %v, want ErrPublicRoomsPending", err)
	}
	if e.hub.PublicEnabled() {
		t.Fatal("the switch must be in force even though a room did not confirm")
	}
	if _, err := e.hub.Create(context.Background(), CreateRequest{PlayerID: "p", Name: "P", Avatar: testAvatar, Visibility: visibilityPublic}); !errors.Is(err, ErrPublicUnavailable) {
		t.Fatalf("public Create after the switch = %v", err)
	}
}

func TestPublicSwitchAdmitsOnlyThePlayersAlreadySeated(t *testing.T) {
	e, _ := policyEnv(t)
	public := createPublic(e, "host", game.LangEN)
	_, host := e.join(public, "host", "Host")
	guest, guestSession := e.join(public, "guest", "Guest")
	private := e.create("owner", "Owner")
	e.join(private, "owner", "Owner")
	e.handle(public, host, game.CmdStartGame, nil)

	setPublic(t, e, false)

	// A newcomer, by code: the generic refusal, no seat, the socket stays.
	c, _ := e.join(public, "newcomer", "New")
	frames := c.drain()
	if got := errorCodes(frames); len(got) != 1 || got[0] != api.ErrCodeJoinUnavailable {
		t.Fatalf("newcomer frames = %v, want one error:join_unavailable", kinds(frames))
	}
	if len(c.closeCodes()) != 0 {
		t.Errorf("a refused join must not close the socket: %v", c.closeCodes())
	}
	if got := seatedIn(e, public); !slices.Equal(got, []string{"host", "guest"}) {
		t.Fatalf("seats = %v", got)
	}
	if info, ok := e.hub.InfoFor(public, "newcomer"); !ok || info.Joinable {
		t.Errorf("InfoFor(newcomer) = %+v, %v: want not joinable", info, ok)
	}
	if info, ok := e.hub.InfoFor(public, "guest"); !ok || !info.Joinable {
		t.Errorf("InfoFor(seated guest) = %+v, %v: want joinable", info, ok)
	}

	// A seated player reconnects and keeps playing.
	guestSession.Disconnect()
	e.sync(public)
	guest.drain()
	again, _ := e.join(public, "guest", "Guest")
	if got := lastKind(again.drain()); got != "state" {
		t.Fatalf("the seated guest's reconnect = %s, want state", got)
	}

	// Private rooms are not affected.
	other, _ := e.join(private, "newcomer", "New")
	if got := lastKind(other.drain()); got != "state" {
		t.Fatalf("join of a private room while public games are off = %s", got)
	}

	// Back on: the newcomer gets in (a game in progress still takes new players).
	setPublic(t, e, true)
	back, _ := e.join(public, "late", "Late")
	if got := lastKind(back.drain()); got != "state" {
		t.Fatalf("join after the switch went on again = %s", got)
	}
}

func TestPublicSwitchStopsAutomatedOpponents(t *testing.T) {
	e := newEnv(t, deterministicBots)
	code := createPublic(e, "host", game.LangEN)
	e.join(code, "host", "Host")

	// A fill that was already scheduled does not happen once the switch is off.
	e.advance(19 * time.Second)
	setPublic(t, e, false)
	e.advance(2 * time.Minute)
	wantBots(t, e, code, 0)

	// Back on: the lonely host's lobby is filled again, counting from now.
	setPublic(t, e, true)
	e.advance(20*time.Second - time.Millisecond)
	wantBots(t, e, code, 0)
	e.advance(time.Millisecond)
	wantBots(t, e, code, 1)

	// Off with a seat already taken: the automated player stays, no second one.
	setPublic(t, e, false)
	e.advance(2 * time.Minute)
	wantBots(t, e, code, 1)
}

// After SetPublicEnabled(false) returns, no public room is created and no new
// player is seated in a public room, whatever runs concurrently (-race).
func TestPublicSwitchRacesWithCreatesAndJoins(t *testing.T) {
	e, _ := policyEnv(t, func(c *Config) { c.MaxRooms = 10000 })
	public := createPublic(e, "host", game.LangEN)
	e.join(public, "host", "Host")
	r := e.room(public)

	var off atomic.Bool // set once SetPublicEnabled(false) returned
	var wg sync.WaitGroup
	errs := make(chan string, 64)
	for w := range 4 {
		wg.Go(func() {
			for i := range 25 {
				after := off.Load()
				id := fmt.Sprintf("c%d-%d", w, i)
				_, err := e.hub.Create(context.Background(), CreateRequest{PlayerID: id, Name: id, Avatar: testAvatar, Visibility: visibilityPublic})
				if after && err == nil {
					errs <- "a public room was created after the switch went off"
				}
				if err != nil && !errors.Is(err, ErrPublicUnavailable) {
					errs <- "Create: " + err.Error()
				}
			}
		})
	}
	for w := range 2 {
		wg.Go(func() {
			for i := range 4 {
				after := off.Load()
				id := fmt.Sprintf("j%d-%d", w, i)
				c := newFakeClient(id)
				s, err := e.hub.Connect(public, c)
				if err != nil {
					errs <- "Connect: " + err.Error()
					return
				}
				frame, _ := json.Marshal(joinFrame(public, id))
				s.Handle(game.CmdJoin, frame)
				// Ordered after the join in the room's mailbox.
				seated := make(chan bool, 1)
				r.post(func(r *room) { seated <- slices.Contains(r.seatedHumans(), id) }, func() { seated <- false })
				if <-seated && after {
					errs <- id + " was seated after the switch went off"
				}
			}
		})
	}
	setPublic(t, e, false)
	off.Store(true)
	wg.Wait()
	close(errs)
	for msg := range errs {
		t.Error(msg)
	}
}
