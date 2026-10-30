package integration

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/AresiusXP/tutti-frutti/server/internal/api"
	"github.com/AresiusXP/tutti-frutti/server/internal/game"
	"github.com/AresiusXP/tutti-frutti/server/internal/protocol"
	"github.com/AresiusXP/tutti-frutti/server/internal/safety"
	"github.com/AresiusXP/tutti-frutti/server/internal/store"
)

func (s *stack) acceptTerms(p *player) response {
	s.t.Helper()
	body := fmt.Sprintf(`{"version":%q,"locale":"es","age13Confirmed":true}`, safety.TermsVersion)
	return s.do(http.MethodPost, "/api/terms/accept", []byte(body), withCookie(p))
}

func (s *stack) report(p *player, body map[string]any) response {
	s.t.Helper()
	raw, _ := json.Marshal(body)
	return s.do(http.MethodPost, "/api/reports", raw, withCookie(p))
}

func (s *stack) storedReport(r response) safety.Report {
	s.t.Helper()
	var resp api.ReportResponse
	r.jsonBody(s.t, &resp)
	rep, err := s.backend.GetReport(s.t.Context(), resp.ID)
	if err != nil {
		s.t.Fatalf("report %q: %v", resp.ID, err)
	}
	return rep
}

// TestTermsGateEndToEnd walks a guest through the gate over the real REST and
// WebSocket endpoints: refused everywhere until the acceptance, then through.
func TestTermsGateEndToEnd(t *testing.T) {
	s := newStack(t, stackOpts{terms: true})
	ana, beto := s.newPlayer("Ana"), s.newPlayer("Beto")

	// REST: no room without the terms.
	if _, err := s.tryCreateRoom(ana, ""); err == nil || !strings.Contains(err.Error(), "403") {
		t.Fatalf("POST /api/rooms without the terms: %v", err)
	}
	var session api.SessionInfo
	s.do(http.MethodGet, "/api/session", nil, withCookie(ana)).jsonBody(t, &session)
	if !session.Terms.AcceptanceRequired || session.Terms.RequiredVersion != safety.TermsVersion || session.Terms.AcceptedVersion != nil {
		t.Fatalf("session terms = %+v", session.Terms)
	}
	if r := s.acceptTerms(ana); r.status != http.StatusOK {
		t.Fatalf("accept = %d %s", r.status, r.body)
	}
	code := s.createRoom(ana, "")

	// WebSocket: the host joins, the other guest is refused and keeps the socket.
	ca := s.connect(ana)
	st := ca.join(code)
	if !store.ValidID(st.RoomInstanceID) || !store.ValidID(st.MatchInstanceID) {
		t.Fatalf("instance ids = %q / %q", st.RoomInstanceID, st.MatchInstanceID)
	}
	if info, ok := s.roomInfo(code); !ok || info.RoomInstanceID != st.RoomInstanceID {
		t.Fatalf("GET /api/rooms/%s roomInstanceId = %q (%v), State says %q", code, info.RoomInstanceID, ok, st.RoomInstanceID)
	}
	cb := s.connect(beto)
	if _, err := cb.tryJoin(code); err == nil || !strings.Contains(err.Error(), protocol.ErrTermsRequired) {
		t.Fatalf("join without the terms: %v", err)
	}
	if info, _ := s.roomInfo(code); info.Players != 1 {
		t.Errorf("seats = %d: the refused guest must not take a seat", info.Players)
	}
	if cb.isClosed() {
		t.Error("the refused connection was closed")
	}
	if r := s.acceptTerms(beto); r.status != http.StatusOK {
		t.Fatalf("accept = %d", r.status)
	}
	if st := cb.join(code); st.View.YouID != beto.id {
		t.Fatalf("join after accepting: %+v", st.View)
	}
	// Commands of someone who never joined are not_joined, terms or not.
	other := s.newPlayer("Cami")
	cc := s.connect(other)
	cc.send(game.CmdSetAnswer, game.SetAnswer{Category: 0, Text: "x"})
	cc.waitFor("not_joined", isError(protocol.ErrNotJoined))
}

// TestAnOldClientIsToldToReload: a client of protocol 4 has no terms screen, so
// it must not get as far as an unexplained terms_required.
func TestAnOldClientIsToldToReload(t *testing.T) {
	s := newStack(t, stackOpts{terms: true})
	p := s.newPlayer("Ana")
	room := s.acceptAndCreate(p)
	c := s.connect(p)
	c.sendRaw(fmt.Sprintf(`{"t":"join","v":4,"d":{"code":%q,"name":"Ana","avatar":{"emoji":"🍓","color":"#f06"}}}`, room))
	c.waitFor("unsupported_version", isError(protocol.ErrUnsupportedVersion))
	c.expectClose(protocol.CloseUnsupportedVersion)
	if protocol.Version != 5 {
		t.Errorf("protocol.Version = %d, want 5", protocol.Version)
	}
}

func (s *stack) acceptAndCreate(p *player) string {
	s.t.Helper()
	if r := s.acceptTerms(p); r.status != http.StatusOK {
		s.t.Fatalf("accept = %d %s", r.status, r.body)
	}
	return s.createRoom(p, "")
}

// TestReportsEndToEnd: a report is resolved through the real room actor.
func TestReportsEndToEnd(t *testing.T) {
	s := newStack(t, stackOpts{terms: true})
	ana, beto := s.newPlayer("Ana"), s.newPlayer("Beto")
	code := s.acceptAndCreate(ana)
	if r := s.acceptTerms(beto); r.status != http.StatusOK {
		t.Fatal(r.status)
	}
	ca, cb := s.connect(ana), s.connect(beto)
	state := ca.join(code)
	cb.join(code)

	// Beto reports Ana's nickname: verified, target from the room.
	r := s.report(beto, map[string]any{
		"kind": "player", "code": code, "roomInstanceId": state.RoomInstanceID,
		"ref": map[string]any{"playerId": ana.id}, "reason": "impersonation", "idempotencyKey": "e2e-report-1",
	})
	if r.status != http.StatusCreated {
		t.Fatalf("report = %d %s", r.status, r.body)
	}
	rep := s.storedReport(r)
	if !rep.EvidenceVerified || rep.TargetGuest != ana.id || rep.ReporterGuest != beto.id || rep.RoomInstance != state.RoomInstanceID ||
		rep.MatchInstance != state.MatchInstanceID || rep.ContentKey != "player:"+ana.id || rep.Kind != "player" {
		t.Errorf("stored = %+v", rep)
	}
	var data safety.EvidenceData
	if err := json.Unmarshal(rep.Evidence, &data); err != nil || data.Player == nil || data.Player.Name != "Ana" || data.Source != "server" {
		t.Errorf("evidence = %s", rep.Evidence)
	}

	// A stranger (never in the room) is refused; the room's data stays private.
	stranger := s.newPlayer("Intruso")
	r = s.report(stranger, map[string]any{
		"kind": "player", "code": code, "ref": map[string]any{"playerId": ana.id}, "reason": "spam", "idempotencyKey": "e2e-stranger-1",
	})
	if r.status != http.StatusForbidden || r.errCode(t) != "not_in_room" {
		t.Errorf("a stranger's report = %d %s", r.status, r.body)
	}

	// The game starts. Ana's answer is hidden from Beto while the round is
	// played: his report about it is taken, uncorroborated, and the hidden text
	// is nowhere in the stored evidence.
	ca.send(game.CmdStartGame, nil)
	ca.waitState("the letter phase", func(st *protocol.State) bool { return st.View.Phase == game.PhaseLetter })
	ca.send(game.CmdStopLetter, nil)
	ca.waitState("playing", func(st *protocol.State) bool { return st.View.Phase == game.PhasePlaying })
	ca.send(game.CmdSetAnswer, game.SetAnswer{Category: 0, Text: "ZZsecretoZZ"})
	ca.waitState("the answer saved", func(st *protocol.State) bool { return st.View.Round.Answers[ana.id][0] == "ZZsecretoZZ" })
	r = s.report(beto, map[string]any{
		"kind": "answer", "code": code, "roomInstanceId": state.RoomInstanceID,
		"ref":    map[string]any{"playerId": ana.id, "category": 0, "round": 1},
		"reason": "other", "idempotencyKey": "e2e-hidden-1", "observedText": "something beto claims",
	})
	if r.status != http.StatusCreated {
		t.Fatalf("report of a hidden answer = %d %s", r.status, r.body)
	}
	hidden := s.storedReport(r)
	if hidden.EvidenceVerified || hidden.TargetGuest != "" || strings.Contains(string(hidden.Evidence), "ZZsecretoZZ") {
		t.Errorf("a hidden answer reached a report: %+v / %s", hidden, hidden.Evidence)
	}
	if !strings.Contains(string(hidden.Evidence), "something beto claims") || !strings.Contains(string(hidden.Evidence), `"source":"client"`) {
		t.Errorf("the client's claim is not stored as such: %s", hidden.Evidence)
	}
	// Ana can report her own answer, with the server's copy of it.
	r = s.report(ana, map[string]any{
		"kind": "answer", "code": code, "ref": map[string]any{"playerId": ana.id, "category": 0, "round": 1},
		"reason": "other", "idempotencyKey": "e2e-own-1",
	})
	if own := s.storedReport(r); !own.EvidenceVerified || !strings.Contains(string(own.Evidence), "ZZsecretoZZ") {
		t.Errorf("own answer evidence = %+v / %s", own, own.Evidence)
	}

	// The room is gone: the same report is still taken, uncorroborated.
	ca.send(game.CmdLeave, nil)
	cb.send(game.CmdLeave, nil)
	eventually(t, "the room to close", func() bool { _, ok := s.roomInfo(code); return !ok })
	r = s.report(beto, map[string]any{
		"kind": "player", "code": code, "roomInstanceId": state.RoomInstanceID,
		"ref": map[string]any{"playerId": ana.id}, "reason": "harassment", "idempotencyKey": "e2e-gone-1", "observedText": "Ana",
	})
	if gone := s.storedReport(r); gone.EvidenceVerified || gone.TargetGuest != "" || gone.RoomInstance != state.RoomInstanceID {
		t.Errorf("after the room closed: %+v", gone)
	}
	if got := s.metricValue("tuttifrutti_reports_total", map[string]string{"kind": "player", "reason": "impersonation"}); got != 1 {
		t.Errorf("tuttifrutti_reports_total{player,impersonation} = %v", got)
	}
}
