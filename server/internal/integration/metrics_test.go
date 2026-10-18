package integration

import (
	"fmt"
	"net/http"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/coder/websocket"

	"github.com/AresiusXP/tutti-frutti/server/internal/game"
	"github.com/AresiusXP/tutti-frutti/server/internal/protocol"
)

// scrape fetches /metrics over HTTP, like Prometheus.
func (s *stack) scrape() string {
	s.t.Helper()
	r := s.do(http.MethodGet, "/metrics", nil)
	if r.status != 200 {
		s.t.Fatalf("GET /metrics = %d", r.status)
	}
	return string(r.body)
}

// promSeries returns the value text of every series line of name in a text
// exposition, keyed by its label block ("" for none).
func promSeries(text, name string) map[string]string {
	out := map[string]string{}
	for _, line := range strings.Split(text, "\n") {
		if !strings.HasPrefix(line, name) {
			continue
		}
		rest := line[len(name):]
		labels := ""
		switch {
		case strings.HasPrefix(rest, "{"):
			end := strings.LastIndex(rest, "}")
			labels, rest = rest[1:end], rest[end+1:]
		case strings.HasPrefix(rest, " "):
		default:
			continue // another metric sharing the prefix
		}
		out[labels] = strings.TrimSpace(rest)
	}
	return out
}

var labelRE = regexp.MustCompile(`(\w+)="([^"]*)"`)

func labelsOf(block string) map[string]string {
	out := map[string]string{}
	for _, m := range labelRE.FindAllStringSubmatch(block, -1) {
		out[m[1]] = m[2]
	}
	return out
}

// TestMetricsAfterAGame plays one and a half rounds and checks that /metrics
// tells the truth: gauges, counters per command and result, closes per code,
// and no label values taken from client input.
func TestMetricsAfterAGame(t *testing.T) {
	s := newStack(t, stackOpts{})
	tb := newTable(t, s, "Ana", "Beto", "Cami")
	val := func(name string, labels map[string]string) float64 { return s.metricValue(name, labels) }
	cmd := func(typ, result string) float64 {
		return val("tuttifrutti_commands_total", map[string]string{"type": typ, "result": result})
	}

	if val("tuttifrutti_rooms", nil) != 1 || val("tuttifrutti_connections", nil) != 3 {
		t.Fatalf("gauges rooms=%v connections=%v, want 1 and 3", val("tuttifrutti_rooms", nil), val("tuttifrutti_connections", nil))
	}
	if cmd("join", "ok") != 3 {
		t.Fatalf("join ok = %v", cmd("join", "ok"))
	}

	// ---- one full round, with a few refusals on the way ----
	tb.advanceTo(stageLetter)
	tb.wantCmdError(1, game.CodeNotYourTurn, game.CmdStopLetter, nil)
	tb.advanceTo(stagePlaying)
	tb.wantCmdError(1, game.CodeBastaNotAllowed, game.CmdBasta, nil)
	tb.advanceTo(stageSummary)
	tb.mustCmd(0, game.CmdNextRound, nil)
	tb.waitPhase(game.PhaseLetter)

	want := map[[2]string]float64{
		{"update_settings", "ok"}:        1,
		{"start_game", "ok"}:             1,
		{"stop_letter", "not_your_turn"}: 1,
		{"stop_letter", "ok"}:            1,
		{"set_answer", "ok"}:             8,
		{"basta", "basta_not_allowed"}:   1,
		{"basta", "ok"}:                  1,
		{"vote_reject", "ok"}:            2,
		{"propose_merge", "ok"}:          1,
		{"vote_merge", "ok"}:             2,
		{"finish_review", "ok"}:          1,
		{"next_round", "ok"}:             1,
	}
	for k, n := range want {
		if got := cmd(k[0], k[1]); got != n {
			t.Errorf("commands_total{type=%q,result=%q} = %v, want %v", k[0], k[1], got, n)
		}
	}
	if got := val("tuttifrutti_rounds_total", nil); got != 2 {
		t.Errorf("rounds_total = %v, want 2 (round 2 just started)", got)
	}
	if got := val("tuttifrutti_basta_total", nil); got != 1 {
		t.Errorf("basta_total = %v, want 1", got)
	}
	if got := val("tuttifrutti_snapshot_errors_total", nil); got != 0 {
		t.Errorf("snapshot_errors_total = %v", got)
	}

	// ---- client-chosen strings never become label values ----
	for i := range 30 { // spread over the sockets: each has its own rate limit
		typ := fmt.Sprintf("garbage_%d", i)
		tb.seats[i%3].c.roundTrip(typ, nil) // joined: bad_message
	}
	fresh := s.connect(s.newPlayer("Zed"))
	for i := range 5 {
		m := fresh.mark()
		fresh.send(fmt.Sprintf("early_%d", i), nil) // not joined: not_joined
		fresh.waitSince(m, "not_joined", isError(protocol.ErrNotJoined))
	}
	if got := cmd("unknown", "bad_message"); got != 30 {
		t.Errorf("unknown/bad_message = %v, want 30", got)
	}
	// A frame before the join is answered by the transport, not by a room: it is
	// not a room command, but it is counted as an inbound frame of type "other".
	if got := cmd("unknown", "not_joined"); got != 0 {
		t.Errorf("unknown/not_joined = %v, want 0 (the transport answers before any room sees the frame)", got)
	}
	if got := val("tuttifrutti_ws_frames_total", map[string]string{"dir": "in", "type": "other"}); got != 35 {
		t.Errorf("ws_frames_total{in,other} = %v, want 35 (30 garbage + 5 early)", got)
	}
	for _, name := range []string{"tuttifrutti_commands_total", "tuttifrutti_ws_frames_total"} {
		for _, v := range append(s.labelValues(name, "type"), s.labelValues(name, "result")...) {
			if strings.Contains(v, "garbage") || strings.Contains(v, "early_") {
				t.Errorf("%s has the client-controlled label value %q", name, v)
			}
		}
	}
	commandTypes := map[string]bool{"unknown": true}
	for _, c := range []string{game.CmdJoin, game.CmdLeave, game.CmdUpdateSettings, game.CmdKick, game.CmdStartGame, game.CmdStopLetter,
		game.CmdSetAnswer, game.CmdBasta, game.CmdVoteReject, game.CmdProposeMerge, game.CmdVoteMerge, game.CmdReviewNav,
		game.CmdFinishReview, game.CmdNextRound, game.CmdEndGame, game.CmdRematch} {
		commandTypes[c] = true
	}
	for _, v := range s.labelValues("tuttifrutti_commands_total", "type") {
		if !commandTypes[v] {
			t.Errorf("commands_total type label %q is not a known command", v)
		}
	}

	// HTTP: any path or method is reduced to a route pattern.
	for i := range 20 {
		s.do(http.MethodGet, fmt.Sprintf("/zzz-%d", i), nil)
		s.do(http.MethodGet, fmt.Sprintf("/api/zzz-%d", i), nil)
		s.do(http.MethodGet, fmt.Sprintf("/api/rooms/ZZ%02d", i), nil)
	}
	s.do("BREW", "/healthz", nil)
	routes := map[string]bool{}
	for _, v := range s.labelValues("tuttifrutti_http_requests_total", "route") {
		routes[v] = true
	}
	for _, m := range s.labelValues("tuttifrutti_http_requests_total", "method") {
		if !slices.Contains([]string{"GET", "POST", "OTHER", "HEAD", "PUT", "PATCH", "DELETE", "OPTIONS", "CONNECT", "TRACE"}, m) {
			t.Errorf("http method label %q", m)
		}
	}
	for r := range routes {
		if strings.Contains(r, "zzz") || strings.Contains(r, "ZZ0") || strings.Contains(r, tb.code) {
			t.Errorf("http route label %q carries the request path", r)
		}
	}
	if len(routes) > 14 {
		t.Errorf("%d distinct http routes: %v", len(routes), routes)
	}

	// ---- every way a socket can end shows up under its close code ----
	closes := func(code string) float64 {
		return val("tuttifrutti_ws_closes_total", map[string]string{"code": code})
	}
	waitCloses := func(code string, n float64) {
		t.Helper()
		eventually(t, fmt.Sprintf("closes_total{code=%s} = %v", code, n), func() bool { return closes(code) == n })
	}
	tb.cmdExcept(0, []int{2}, game.CmdKick, game.Kick{PlayerID: tb.seats[2].p.id}) // 4002
	tb.seats[1].c.send(game.CmdLeave, nil)                                         // 4005
	tb.seats[1].c.expectClose(protocol.CloseLeft)
	tb.reconnect(0) // replaces Ana's socket: 4001
	waitCloses("4002", 1)
	waitCloses("4005", 1)
	waitCloses("4001", 1)
	if got := cmd("kick", "ok"); got != 1 {
		t.Errorf("kick ok = %v", got)
	}
	if got := cmd("leave", "ok"); got != 1 {
		t.Errorf("leave ok = %v", got)
	}

	if err := tb.seats[0].c.conn.Close(websocket.StatusNormalClosure, "bye"); err != nil { // 1000
		t.Fatalf("clean close: %v", err)
	}
	waitCloses("1000", 1)
	tb.reconnect(1)
	tb.seats[1].c.drop() // no close frame: 1006
	eventually(t, "an abnormal close", func() bool { return closes("1006") >= 1 })

	big := s.connect(s.newPlayer("Big"))
	big.sendRaw(strings.Repeat("x", protocol.MaxMessageBytes+1))
	big.expectClose(protocol.CloseTooBig)
	old := s.connect(s.newPlayer("Old"))
	old.sendRaw(`{"t":"ping","v":9}`)
	old.expectClose(protocol.CloseUnsupportedVersion)
	lost := s.connect(s.newPlayer("Lost"))
	lost.send(game.CmdJoin, protocol.JoinRequest{Code: "BCDF", Name: "Lost"})
	lost.expectClose(protocol.CloseRoomClosed)
	waitCloses("1009", 1)
	waitCloses("4003", 1)
	waitCloses("4004", 1)

	// The scrape agrees with the registry and exposes every documented series.
	text := s.scrape()
	for _, name := range []string{
		"tuttifrutti_rooms", "tuttifrutti_connections", "tuttifrutti_commands_total", "tuttifrutti_rounds_total",
		"tuttifrutti_basta_total", "tuttifrutti_ws_closes_total", "tuttifrutti_snapshot_errors_total",
	} {
		if !strings.Contains(text, "# TYPE "+name+" ") {
			t.Errorf("/metrics has no %s", name)
		}
	}
	if got := promSeries(text, "tuttifrutti_rounds_total")[""]; got != "2" {
		t.Errorf("scraped rounds_total = %q", got)
	}
	if got := promSeries(text, "tuttifrutti_basta_total")[""]; got != "1" {
		t.Errorf("scraped basta_total = %q", got)
	}
	for block := range promSeries(text, "tuttifrutti_ws_closes_total") {
		code := labelsOf(block)["code"]
		if code != "other" && !regexp.MustCompile(`^\d{4}$`).MatchString(code) {
			t.Errorf("ws_closes_total code label %q", code)
		}
	}
	if got := promSeries(text, "tuttifrutti_ws_closes_total"); len(got) > 14 {
		t.Errorf("%d distinct close-code series: %v", len(got), got)
	}

	// Gauges follow the sockets and the rooms, down to zero after the drain.
	tb.reconnect(0)
	connected := val("tuttifrutti_connections", nil)
	if got := float64(s.hub.Stats().Connections); connected != got || got < 1 {
		t.Errorf("connections gauge %v, Stats %v", connected, got)
	}
	if err := s.shutdown(); err != nil {
		t.Fatal(err)
	}
	tb.seats[0].c.expectClose(protocol.CloseServiceRestart)
	if val("tuttifrutti_rooms", nil) != 0 || val("tuttifrutti_connections", nil) != 0 {
		t.Errorf("after the drain: rooms %v connections %v", val("tuttifrutti_rooms", nil), val("tuttifrutti_connections", nil))
	}
	waitCloses("1012", 2) // Ana's socket and Zed's, who never joined a room
	if v, ok := s.metric("tuttifrutti_ws_connections_open", nil); ok && v != 0 {
		t.Errorf("ws_connections_open = %v after the drain", v)
	}
}
