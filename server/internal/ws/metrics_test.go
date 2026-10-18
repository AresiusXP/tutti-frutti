package ws

import (
	"fmt"
	"strconv"
	"strings"
	"testing"

	"github.com/coder/websocket"
	"github.com/prometheus/client_golang/prometheus"

	"github.com/AresiusXP/tutti-frutti/server/internal/protocol"
)

const (
	closesMetric = "tuttifrutti_ws_closes_total"
	framesMetric = "tuttifrutti_ws_frames_total"
)

func TestMetricsFrames(t *testing.T) {
	e := newEnv(t, nil)
	c, sess := e.dialJoined()

	send(t, c, "ping", "")
	read(t, c)
	send(t, c, "set_answer", `{"text":"x"}`)
	send(t, c, "evil_type_"+strings.Repeat("z", 20), `{}`)
	sendRaw(t, c, "not json")
	read(t, c) // bad_message

	sess.client.Send([]byte(`{"t":"state","v":1,"d":{}}`))
	sess.client.Send([]byte(`{"t":"event","v":1,"d":{}}`))
	sess.client.Send([]byte(`{"t":"surprise","v":1,"d":{}}`))
	sess.client.Send([]byte(`{"v":1,"t":"state","d":{}}`)) // type not first: not recognised cheaply
	read(t, c)
	read(t, c)
	read(t, c)
	read(t, c)
	eventually(t, "frames to be counted", func() bool {
		return counter(t, e.reg, framesMetric, map[string]string{"dir": "out", "type": "other"}) == 2
	})

	want := []struct {
		dir, typ string
		n        float64
	}{
		{"in", "join", 1},
		{"in", "ping", 1},
		{"in", "set_answer", 1},
		{"in", "other", 2}, // the unknown type and the malformed frame
		{"out", "pong", 1},
		{"out", "error", 1},
		{"out", "state", 1},
		{"out", "event", 1},
		{"out", "other", 2}, // an unknown type and a frame whose "t" is not first
	}
	for _, w := range want {
		if got := counter(t, e.reg, framesMetric, map[string]string{"dir": w.dir, "type": w.typ}); got != w.n {
			t.Errorf("frames{dir=%s,type=%s} = %v, want %v", w.dir, w.typ, got, w.n)
		}
	}
	for _, typ := range labelValues(t, e.reg, framesMetric, "type") {
		if strings.Contains(typ, "evil") || strings.Contains(typ, "surprise") {
			t.Errorf("client-controlled frame type leaked into a label: %q", typ)
		}
	}
}

func TestStopVoteFramesHaveTheirOwnLabel(t *testing.T) {
	e := newEnv(t, nil)
	c, sess := e.dialJoined()
	send(t, c, "ask_stop", "")
	send(t, c, "vote_stop", `{"yes":true}`)
	send(t, c, "vote_stop", `{"yes":false}`)
	eventually(t, "the frames to reach the room", func() bool { return len(sess.handled()) >= 4 })
	for typ, n := range map[string]float64{"ask_stop": 1, "vote_stop": 2} {
		if got := counter(t, e.reg, framesMetric, map[string]string{"dir": "in", "type": typ}); got != n {
			t.Errorf("frames{dir=in,type=%s} = %v, want %v", typ, got, n)
		}
	}
	if got := counter(t, e.reg, framesMetric, map[string]string{"dir": "in", "type": "other"}); got != 0 {
		t.Errorf("the new commands were counted as other: %v", got)
	}
}

func TestMetricsCloses(t *testing.T) {
	e := newEnv(t, nil)

	// 1000: the client says goodbye.
	c1 := e.dial()
	_ = c1.Close(websocket.StatusNormalClosure, "")
	// 1006: the client vanishes.
	c2 := e.dial()
	_ = c2.CloseNow()
	// 4002, 4999 (not enumerated) and 4001 closed by the room.
	for _, code := range []int{protocol.CloseKicked, 4999, protocol.CloseReplaced} {
		c, sess := e.dialJoined()
		sess.client.Close(code, "")
		expectClose(t, c)
	}
	// 4003 and 1009 from protocol violations, 1008 from the rate limit.
	c3 := e.dial()
	sendRaw(t, c3, fmt.Sprintf(`{"t":"ping","v":%d}`, protocol.Version+1))
	expectClose(t, c3)
	c4 := e.dial()
	sendRaw(t, c4, strings.Repeat("a", protocol.MaxMessageBytes+10))
	expectClose(t, c4)
	c5 := e.dial()
	for i := 0; i < 10; i++ {
		sendRaw(t, c5, "junk")
	}
	expectClose(t, c5)
	// 1001 from the browser navigating away.
	c6 := e.dial()
	_ = c6.Close(websocket.StatusGoingAway, "")

	want := map[string]float64{
		"1000": 1, "1006": 1, "4002": 1, "other": 1, "4001": 1,
		"4003": 1, "1009": 1, "1008": 1, "1001": 1,
	}
	eventually(t, "close metrics", func() bool {
		for code, n := range want {
			if counter(t, e.reg, closesMetric, map[string]string{"code": code}) != n {
				return false
			}
		}
		return true
	})
	for _, code := range labelValues(t, e.reg, closesMetric, "code") {
		if _, ok := want[code]; !ok {
			t.Errorf("unexpected close label %q", code)
		}
	}
}

func TestMetricsCloseLabelsAreBounded(t *testing.T) {
	for code := 0; code < 6000; code++ {
		l := closeLabel(code)
		if l != otherLabel && l != strconv.Itoa(code) {
			t.Fatalf("closeLabel(%d) = %q", code, l)
		}
	}
	enumerated := 0
	for code := 0; code < 6000; code++ {
		if closeLabel(code) != otherLabel {
			enumerated++
		}
	}
	if enumerated != 12 {
		t.Errorf("%d close codes have their own label, want the 12 the server emits", enumerated)
	}
	for _, typ := range []string{"", "x", "join", "STATE", strings.Repeat("a", 100)} {
		if l := typeLabel(typ); l != otherLabel && l != typ {
			t.Errorf("typeLabel(%q) = %q", typ, l)
		}
	}
	if typeLabel("STATE") != otherLabel {
		t.Error("labels are case-sensitive")
	}
}

func TestFrameTypeOf(t *testing.T) {
	tests := []struct {
		frame string
		want  string
	}{
		{`{"t":"state","v":1,"d":{}}`, "state"},
		{`{"t":"event","v":1}`, "event"},
		{`{"t":"error","v":1,"d":{"code":"x"}}`, "error"},
		{`{"t":"pong","v":1}`, "pong"},
		{`{"t":"nope","v":1}`, "other"},
		{`{"v":1,"t":"state"}`, "other"},
		{`{"t":"state`, "other"},
		{`{"t":"` + strings.Repeat("s", 40) + `"}`, "other"},
		{``, "other"},
		{`[]`, "other"},
	}
	for _, tt := range tests {
		if got := frameTypeOf([]byte(tt.frame)); got != tt.want {
			t.Errorf("frameTypeOf(%q) = %q, want %q", tt.frame, got, tt.want)
		}
	}
}

func TestSharedRegistryDoesNotPanic(t *testing.T) {
	reg := prometheus.NewRegistry()
	a := newMetrics(reg)
	b := newMetrics(reg)
	a.closed(1000)
	b.closed(1000)
	if got := counter(t, reg, closesMetric, map[string]string{"code": "1000"}); got != 2 {
		t.Errorf("shared counter = %v, want 2", got)
	}
	a.opened()
	b.opened()
	b.released()
	if got := gauge(t, reg, "tuttifrutti_ws_connections_open"); got != 1 {
		t.Errorf("shared gauge = %v, want 1", got)
	}
	a.sendDrop()
	b.sendDrop()
	if got := counter(t, reg, sendDropsMetric, nil); got != 2 {
		t.Errorf("shared send-drop counter = %v, want 2", got)
	}
	if newMetrics(nil) != nil {
		t.Error("nil registerer must disable metrics")
	}
	var none *metrics
	none.opened()
	none.released()
	none.closed(1)
	none.frameIn("x")
	none.frameOut(nil)
	none.sendDrop()
}

const sendDropsMetric = "tuttifrutti_ws_send_drops_total"

// A client whose queue overflows is counted once, however many frames the
// room still tries to send before it closes it; a refusal because the
// connection is already closing is not an overflow.
func TestSendDropsAreCountedOncePerConnection(t *testing.T) {
	reg := prometheus.NewRegistry()
	h := NewHandler(Deps{Rooms: &fakeRooms{}, Guests: newGuests(), Registerer: reg})
	fill := func(c *conn) {
		for i := 0; i < protocol.SendQueueSize; i++ {
			if !c.Send(stateFrame(i)) {
				t.Fatalf("Send %d refused before the queue is full", i)
			}
		}
	}

	slow := newConn(h, nil, "p1", "127.0.0.1")
	fill(slow)
	if got := counter(t, reg, sendDropsMetric, nil); got != 0 {
		t.Fatalf("drops = %v with a full but not overflowed queue", got)
	}
	for range 3 {
		if slow.Send(stateFrame(99)) {
			t.Fatal("Send must refuse when the queue is full")
		}
	}
	if got := counter(t, reg, sendDropsMetric, nil); got != 1 {
		t.Errorf("drops = %v after three refused frames of one connection, want 1", got)
	}
	slow.Close(protocol.ClosePolicy, "slow client")
	slow.Send(stateFrame(100))
	if got := counter(t, reg, sendDropsMetric, nil); got != 1 {
		t.Errorf("drops = %v after Close, want 1", got)
	}

	other := newConn(h, nil, "p2", "127.0.0.1")
	fill(other)
	other.Close(protocol.CloseKicked, "kicked") // closing: Send refuses without overflowing
	other.Send(stateFrame(1))
	if got := counter(t, reg, sendDropsMetric, nil); got != 1 {
		t.Errorf("drops = %v: a refusal on a closing connection is not a drop", got)
	}

	third := newConn(h, nil, "p3", "127.0.0.1")
	fill(third)
	third.Send(stateFrame(1))
	if got := counter(t, reg, sendDropsMetric, nil); got != 2 {
		t.Errorf("drops = %v after a second slow connection, want 2", got)
	}
	if got := labelValues(t, reg, sendDropsMetric, "code"); len(got) != 0 {
		t.Errorf("the counter has labels %v, want none", got)
	}
}

func TestRegisterConflictingCollectorPanics(t *testing.T) {
	reg := prometheus.NewRegistry()
	reg.MustRegister(prometheus.NewGauge(prometheus.GaugeOpts{Name: closesMetric, Help: "squatter"}))
	defer func() {
		if recover() == nil {
			t.Error("a name clash with a different collector type must not be ignored silently")
		}
	}()
	newMetrics(reg)
}

func TestNothingSensitiveIsLogged(t *testing.T) {
	e := newEnv(t, nil)
	c := e.dial()
	send(t, c, "join", `{"code":"KT7P","name":"SECRETNICK","avatar":{"emoji":"🍓","color":"#f06"}}`)
	sess := e.rooms.session(t, 0)
	send(t, c, "set_answer", `{"category":1,"text":"SECRETANSWER"}`)
	sendRaw(t, c, "SECRETGARBAGE")
	read(t, c)
	sess.client.Close(protocol.CloseKicked, "bye")
	expectClose(t, c)
	eventually(t, "teardown", func() bool { return sess.disconnects.Load() == 1 })
	eventually(t, "closed log", func() bool { return strings.Contains(e.logs.String(), "ws closed") })

	logs := e.logs.String()
	for _, secret := range []string{"SECRETNICK", "SECRETANSWER", "SECRETGARBAGE", e.cookie.Value, "tf_guest"} {
		if strings.Contains(logs, secret) {
			t.Errorf("logs leak %q:\n%s", secret, logs)
		}
	}
	for _, want := range []string{"ws connected", "ws closed", "code=4002", "player=" + e.id, "ip=127.0.0.1"} {
		if !strings.Contains(logs, want) {
			t.Errorf("logs lack %q:\n%s", want, logs)
		}
	}
}

func TestHelpers(t *testing.T) {
	t.Run("sanitizeCloseCode", func(t *testing.T) {
		for code, want := range map[int]int{
			999: 1011, 1000: 1000, 1001: 1001, 1003: 1003, 1004: 1011, 1005: 1011, 1006: 1011, 1007: 1007,
			1008: 1008, 1012: 1012, 1014: 1014, 1015: 1011, 2999: 1011, 3000: 3000, 4005: 4005, 4999: 4999, 5000: 1011,
		} {
			if got := sanitizeCloseCode(code); got != want {
				t.Errorf("sanitizeCloseCode(%d) = %d, want %d", code, got, want)
			}
		}
	})
	t.Run("truncateReason", func(t *testing.T) {
		if got := truncateReason("short"); got != "short" {
			t.Errorf("got %q", got)
		}
		long := strings.Repeat("a", 200)
		if got := truncateReason(long); len(got) != maxCloseReason {
			t.Errorf("len = %d", len(got))
		}
		got := truncateReason(strings.Repeat("é", 100))
		if len(got) > maxCloseReason || strings.ContainsRune(got, '\uFFFD') || len(got)%2 != 0 {
			t.Errorf("cut inside a rune: %q (%d bytes)", got, len(got))
		}
	})
	t.Run("normalizeCode", func(t *testing.T) {
		for in, want := range map[string]string{"KT7P": "KT7P", " kt7p ": "KT7P", "bcdf": "BCDF", "2389": "2389", "": "", "KT7": "", "KA7P": "", "KT7PP": "", "ñ": ""} {
			got, ok := normalizeCode(in)
			if got != want || ok != (want != "") {
				t.Errorf("normalizeCode(%q) = %q, %v", in, got, ok)
			}
		}
	})
}
