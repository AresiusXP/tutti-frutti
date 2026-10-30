package moderation

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/AresiusXP/tutti-frutti/server/internal/api"
	"github.com/AresiusXP/tutti-frutti/server/internal/game"
	"github.com/AresiusXP/tutti-frutti/server/internal/room"
	"github.com/AresiusXP/tutti-frutti/server/internal/safety"
	"github.com/AresiusXP/tutti-frutti/server/internal/store"
)

var t0 = time.Date(2026, 10, 30, 12, 0, 0, 0, time.UTC)

const (
	targetGuest = "guest-target"
	targetUser  = "00000000-0000-4000-8000-0000000000aa"
	otherUser   = "00000000-0000-4000-8000-0000000000bb"
	someID      = "00000000-0000-4000-8000-000000009999"
	instanceID  = "11111111-1111-4111-8111-111111111111"
)

// fakeRooms records what the server asked of the hub.
type fakeRooms struct {
	mu sync.Mutex

	sweeps      int
	sweepErr    error
	removed     map[string][]string
	atSweep     func() // called inside Sweep
	closeResult room.CloseResult
	closeErr    error
	closed      []room.CloseTarget
	atClose     func() // called after the room closed (a client that disconnects)
}

func (f *fakeRooms) Sweep(context.Context) (map[string][]string, error) {
	f.mu.Lock()
	f.sweeps++
	hook, err, removed := f.atSweep, f.sweepErr, f.removed
	f.mu.Unlock()
	if hook != nil {
		hook()
	}
	return removed, err
}

func (f *fakeRooms) CloseRoom(_ context.Context, t room.CloseTarget) (room.CloseResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.closed = append(f.closed, t)
	if f.atClose != nil {
		f.atClose()
	}
	return f.closeResult, f.closeErr
}

type fakeJudge struct {
	on    bool
	onSet func() // called after the switch moved (a client that disconnects)
}

func (j *fakeJudge) Enabled() bool { return j.on }
func (j *fakeJudge) SetEnabled(on bool) {
	j.on = on
	if j.onSet != nil {
		j.onSet()
	}
}

// fakePublic is the switch of public games, with the contract of the hub:
// nothing changes when ctx is already done; once written, a ctx that ends is
// room.ErrPublicRoomsPending. err is what the rooms answer otherwise.
type fakePublic struct {
	on    bool
	sets  []bool
	err   error
	gotCt bool   // the call had a deadline
	onSet func() // called after the switch moved (a client that disconnects)
}

func (p *fakePublic) PublicEnabled() bool { return p.on }
func (p *fakePublic) SetPublicEnabled(ctx context.Context, on bool) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	p.on = on
	p.sets = append(p.sets, on)
	_, p.gotCt = ctx.Deadline()
	if p.onSet != nil {
		p.onSet()
	}
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("%w: %w", room.ErrPublicRoomsPending, err)
	}
	return p.err
}

// failingMod makes some writes fail on demand. With ctxAware, RecordAction
// fails on a done context, like the database does.
type failingMod struct {
	*store.Memory
	failRecord, failCreate, ctxAware bool
}

func (f *failingMod) RecordAction(ctx context.Context, a safety.Action) error {
	if f.failRecord {
		return errors.New("boom")
	}
	if f.ctxAware && ctx.Err() != nil {
		return ctx.Err()
	}
	return f.Memory.RecordAction(ctx, a)
}

func (f *failingMod) CreateRestriction(ctx context.Context, r safety.Restriction, a safety.Action) error {
	if f.failCreate {
		return errors.New("boom")
	}
	return f.Memory.CreateRestriction(ctx, r, a)
}

type syncBuilder struct {
	mu sync.Mutex
	b  strings.Builder
}

func (s *syncBuilder) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuilder) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

type env struct {
	t      *testing.T
	mem    *store.Memory
	mod    *failingMod
	policy *safety.Policy
	rooms  *fakeRooms
	judge  *fakeJudge
	public *fakePublic
	gauges *ReportGauges
	deps   Deps
	srv    *Server
	now    time.Time
	h      http.Handler
}

// withDeps rebuilds the server with changed dependencies (a store wrapper, a
// clock).
func (e *env) withDeps(mutate func(*Deps)) {
	d := e.deps
	mutate(&d)
	e.deps = d
	e.srv = NewServer(d)
	e.h = e.srv.Handler()
}

func newEnv(t *testing.T, withJudge bool) *env {
	t.Helper()
	e := &env{t: t, mem: store.NewMemory(), policy: safety.NewPolicy(func() time.Time { return t0 }), rooms: &fakeRooms{}, public: &fakePublic{on: true}, now: t0}
	e.mod = &failingMod{Memory: e.mem}
	e.gauges = NewReportGauges(nil, func() time.Time { return t0 })
	logs := &syncBuilder{}
	d := Deps{
		Reports: e.mem, Mod: e.mod, Policy: e.policy, Rooms: e.rooms, Public: e.public, Gauges: e.gauges,
		Now: func() time.Time { return e.now }, Log: slog.New(slog.NewTextHandler(logs, &slog.HandlerOptions{Level: slog.LevelDebug})),
		Version: "test",
	}
	if withJudge {
		e.judge = &fakeJudge{on: true}
		d.Judge = e.judge
	}
	e.srv = NewServer(d)
	e.deps = d
	e.h = e.srv.Handler()
	t.Cleanup(func() {
		// The logs hold ids and codes, never what a player or the operator typed.
		for _, secret := range []string{"SECRET-NOTE", "SECRET-REASON", "SECRET-COMMENT"} {
			if strings.Contains(logs.String(), secret) {
				t.Errorf("the log contains typed text %q:\n%s", secret, logs.String())
			}
		}
	})
	return e
}

type result struct {
	status int
	body   string
}

func (r result) into(t *testing.T, v any) {
	t.Helper()
	if r.status != http.StatusOK {
		t.Fatalf("status %d: %s", r.status, r.body)
	}
	if err := json.Unmarshal([]byte(r.body), v); err != nil {
		t.Fatalf("body %q: %v", r.body, err)
	}
}

func (r result) errCode(t *testing.T) string {
	t.Helper()
	var e ErrorResponse
	if err := json.Unmarshal([]byte(r.body), &e); err != nil {
		t.Fatalf("body %q: %v", r.body, err)
	}
	return e.Error
}

func (e *env) post(path string, req any) result {
	e.t.Helper()
	return e.postCtx(context.Background(), path, req)
}

// postCtx is post with the request's context (a client that gives up).
func (e *env) postCtx(ctx context.Context, path string, req any) result {
	e.t.Helper()
	var body io.Reader
	switch v := req.(type) {
	case nil:
	case string:
		body = strings.NewReader(v)
	default:
		raw, err := json.Marshal(v)
		if err != nil {
			e.t.Fatal(err)
		}
		body = strings.NewReader(string(raw))
	}
	rec := httptest.NewRecorder()
	e.h.ServeHTTP(rec, httptest.NewRequestWithContext(ctx, http.MethodPost, path, body))
	return result{rec.Code, rec.Body.String()}
}

func (e *env) report(n int, mods ...func(*safety.Report)) safety.Report {
	e.t.Helper()
	r := safety.Report{
		ID: fmt.Sprintf("00000000-0000-4000-8000-%012d", 5000+n), ReporterGuest: "guest-reporter",
		RoomInstance: instanceID, ContentKey: "answer:r1:c0:" + targetGuest,
		Kind: safety.KindAnswer, TargetGuest: targetGuest, TargetUser: targetUser,
		Evidence: json.RawMessage(`{"source":"server","text":"tomate"}`), EvidenceVerified: true,
		Reason: safety.ReasonSpam, Comment: "SECRET-COMMENT", Status: safety.StatusOpen,
		IdempotencyKey: fmt.Sprintf("key-%08d", n), CreatedAt: e.now.Add(-time.Duration(n) * time.Hour),
	}
	r.PurgeAt = safety.PurgeAt(r.CreatedAt, time.Time{})
	for _, m := range mods {
		m(&r)
	}
	if _, _, err := e.mem.CreateReport(context.Background(), r); err != nil {
		e.t.Fatal(err)
	}
	return r
}

func (e *env) actions() []safety.Action {
	e.t.Helper()
	acts, err := e.mem.ListActions(context.Background(), 100)
	if err != nil {
		e.t.Fatal(err)
	}
	return acts
}

// ---------------------------------------------------------------------------

func TestUnknownCommandsMethodsAndBodies(t *testing.T) {
	e := newEnv(t, true)
	if r := e.post("/v1/format-disk", nil); r.status != http.StatusNotFound || r.errCode(t) != CodeUnknownCommand {
		t.Errorf("unknown command: %d %s", r.status, r.body)
	}
	if r := e.post("/", nil); r.status != http.StatusNotFound {
		t.Errorf("root: %d", r.status)
	}
	rec := httptest.NewRecorder()
	e.h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, PathStatus, nil))
	if rec.Code != http.StatusMethodNotAllowed || rec.Header().Get("Allow") != http.MethodPost {
		t.Errorf("GET: %d, Allow %q", rec.Code, rec.Header().Get("Allow"))
	}
	big := `{"id":"` + strings.Repeat("x", MaxRequestBytes) + `"}`
	if r := e.post(PathReportsShow, big); r.status != http.StatusRequestEntityTooLarge || r.errCode(t) != CodeTooLarge {
		t.Errorf("oversized request: %d %s", r.status, r.body)
	}
	for name, body := range map[string]string{
		"unknown field": `{"id":"x","extra":1}`,
		"not JSON":      `nope`,
		"trailing":      `{"id":"x"} {}`,
	} {
		if r := e.post(PathReportsShow, body); r.status != http.StatusBadRequest || r.errCode(t) != CodeBadRequest {
			t.Errorf("%s: %d %s", name, r.status, r.body)
		}
	}
}

func TestEveryMutatingCommandRequiresAnOperator(t *testing.T) {
	e := newEnv(t, true)
	rep := e.report(1)
	e.rooms.closeResult = room.CloseResult{Code: "KT7P", RoomInstanceID: "inst"}
	for _, tc := range []struct {
		path string
		req  any
	}{
		{PathReportsResolve, ResolveRequest{ID: rep.ID, Note: "n"}},
		{PathRestrict, RestrictRequest{GuestID: targetGuest, Reason: "r"}},
		{PathUnrestrict, UnrestrictRequest{ID: someID}},
		{PathRoomClose, RoomCloseRequest{Code: "KT7P"}},
		{PathProfileRemove, ProfileRemoveRequest{UserID: targetUser}},
		{PathJudge, JudgeRequest{Action: "disable"}},
		{PathJudge, JudgeRequest{Action: "enable"}},
		{PathPublic, PublicRequest{Action: "disable"}},
		{PathPublic, PublicRequest{Action: "enable"}},
	} {
		for _, bad := range []string{"", "two words", "semi;colon", strings.Repeat("a", 65)} {
			body := map[string]any{}
			raw, _ := json.Marshal(tc.req)
			_ = json.Unmarshal(raw, &body)
			body["operator"] = bad
			if r := e.post(tc.path, body); r.status != http.StatusBadRequest || r.errCode(t) != CodeOperatorRequired {
				t.Errorf("%s with operator %q: %d %s", tc.path, bad, r.status, r.body)
			}
		}
	}
	if len(e.actions()) != 0 || len(e.rooms.closed) != 0 || e.rooms.sweeps != 0 || !e.judge.on || len(e.public.sets) != 0 {
		t.Error("a command without an operator changed something")
	}
	for _, p := range []string{PathStatus, PathReportsList} { // read-only: no operator
		if r := e.post(p, "{}"); r.status != http.StatusOK {
			t.Errorf("%s: %d %s", p, r.status, r.body)
		}
	}
	if r := e.post(PathJudge, JudgeRequest{Action: "status"}); r.status != http.StatusOK {
		t.Errorf("judge status: %d %s", r.status, r.body)
	}
	if r := e.post(PathPublic, PublicRequest{Action: "status"}); r.status != http.StatusOK {
		t.Errorf("public status: %d %s", r.status, r.body)
	}
}

func TestStatus(t *testing.T) {
	e := newEnv(t, true)
	var st StatusResponse
	e.post(PathStatus, "{}").into(t, &st)
	if st.OpenReports != 0 || st.OldestOpenAgeSeconds != 0 || !st.OldestOpenAt.IsZero() || st.Version != "test" ||
		st.Judge != (JudgeStatus{Configured: true, Enabled: true}) || st.Public != (PublicStatus{Configured: true, Enabled: true}) {
		t.Errorf("empty status = %+v", st)
	}
	e.public.on = false
	e.post(PathStatus, "{}").into(t, &st)
	if st.Public != (PublicStatus{Configured: true, Enabled: false}) {
		t.Errorf("status with public games off = %+v", st.Public)
	}
	e.report(1) // filed 1 h ago
	e.report(3) // filed 3 h ago
	e.report(2, func(r *safety.Report) { r.Status = safety.StatusDismissed })
	e.policy.AddBlock("a", "b")
	e.policy.AddRestriction(safety.Restriction{ID: "r1", GuestID: "g"})
	e.post(PathStatus, "{}").into(t, &st)
	if st.OpenReports != 2 || st.OldestOpenAgeSeconds != 3*3600 || !st.OldestOpenAt.Equal(t0.Add(-3*time.Hour)) ||
		st.ActiveRestrictions != 1 || st.Blocks != 1 {
		t.Errorf("status = %+v", st)
	}
	if open, age := e.gauges.Values(); open != 2 || age != 3*time.Hour {
		t.Errorf("the status command must refresh the gauges: %d, %v", open, age)
	}
	if r := e.post(PathStatus, `{"x":1}`); r.status != http.StatusBadRequest {
		t.Errorf("status with a body: %d", r.status)
	}
	noJudge := newEnv(t, false)
	noJudge.post(PathStatus, "{}").into(t, &st)
	if st.Judge != (JudgeStatus{}) {
		t.Errorf("judge without credentials = %+v", st.Judge)
	}
}

func TestReportsListAndShow(t *testing.T) {
	e := newEnv(t, false)
	a := e.report(1)
	b := e.report(2)
	c := e.report(3, func(r *safety.Report) { r.EvidenceVerified, r.TargetGuest, r.TargetUser = false, "", "" })
	if err := e.mem.ResolveReport(context.Background(), b.ID, safety.StatusDismissed, t0); err != nil {
		t.Fatal(err)
	}

	var open ReportsListResponse
	e.post(PathReportsList, ReportsListRequest{}).into(t, &open)
	if len(open.Reports) != 2 || open.Reports[0].ID != c.ID || open.Reports[1].ID != a.ID {
		t.Fatalf("open reports = %+v, want the oldest first", open.Reports)
	}
	if got := open.Reports[0]; got.EvidenceVerified || got.HasTarget || got.Comment != "SECRET-COMMENT" || got.Kind != "answer" {
		t.Errorf("summary = %+v", got)
	}
	var resolved, all ReportsListResponse
	e.post(PathReportsList, ReportsListRequest{Status: "resolved"}).into(t, &resolved)
	e.post(PathReportsList, ReportsListRequest{Status: "all", Limit: 2}).into(t, &all)
	if len(resolved.Reports) != 1 || resolved.Reports[0].ID != b.ID || len(all.Reports) != 2 {
		t.Errorf("resolved %d, all(limit 2) %d", len(resolved.Reports), len(all.Reports))
	}
	for _, req := range []ReportsListRequest{{Status: "bogus"}, {Limit: 101}, {Limit: -1}} {
		if r := e.post(PathReportsList, req); r.status != http.StatusBadRequest {
			t.Errorf("%+v: %d", req, r.status)
		}
	}

	var view ReportView
	e.post(PathReportsShow, ReportRef{ID: a.ID}).into(t, &view)
	if view.ID != a.ID || !view.EvidenceVerified || view.TargetGuest != targetGuest || view.TargetUser != targetUser ||
		view.ReporterGuest != "guest-reporter" || view.RoomInstance != a.RoomInstance || view.Comment != "SECRET-COMMENT" ||
		string(view.Evidence) != `{"source":"server","text":"tomate"}` || view.ContentKey != a.ContentKey || view.Status != "open" {
		t.Errorf("view = %+v", view)
	}
	if r := e.post(PathReportsShow, ReportRef{ID: someID}); r.status != http.StatusNotFound || r.errCode(t) != CodeNotFound {
		t.Errorf("unknown report: %d %s", r.status, r.body)
	}
	if r := e.post(PathReportsShow, ReportRef{ID: "nope"}); r.status != http.StatusNotFound {
		t.Errorf("malformed report id: %d", r.status)
	}
}

func TestResolveReport(t *testing.T) {
	e := newEnv(t, false)
	a := e.report(1)
	b := e.report(2)
	e.gauges.Set(store.ReportStats{Open: 99, OldestOpen: t0})

	var out ResolveResponse
	e.post(PathReportsResolve, ResolveRequest{Operator: "owner", ID: a.ID, Note: "removed the label  \u200bSECRET-NOTE"}).into(t, &out)
	if out.Status != safety.StatusActioned {
		t.Errorf("status = %q", out.Status)
	}
	e.post(PathReportsResolve, ResolveRequest{Operator: "owner", ID: b.ID, Note: "nothing wrong", Dismiss: true}).into(t, &out)
	if out.Status != safety.StatusDismissed {
		t.Errorf("status = %q", out.Status)
	}
	got, _ := e.mem.GetReport(context.Background(), a.ID)
	if got.Status != safety.StatusActioned || !got.ResolvedAt.Equal(t0) || !got.PurgeAt.Equal(t0.Add(30*24*time.Hour)) {
		t.Errorf("report = %+v", got)
	}
	acts := e.actions()
	if len(acts) != 2 {
		t.Fatalf("audit = %+v", acts)
	}
	for _, act := range acts {
		if act.Operator != "owner" || act.Action != safety.ActionReportResolve || act.TargetKind != safety.TargetReport || act.ReportID == "" || act.TargetID != act.ReportID {
			t.Errorf("audit record = %+v", act)
		}
	}
	if !strings.Contains(acts[0].Details+acts[1].Details, "removed the label SECRET-NOTE") {
		t.Errorf("the operator's note is not in the audit: %+v", acts)
	}
	if open, _ := e.gauges.Values(); open != 0 {
		t.Errorf("open gauge = %d after resolving the last open reports", open)
	}

	if r := e.post(PathReportsResolve, ResolveRequest{Operator: "owner", ID: a.ID, Note: "again"}); r.status != http.StatusConflict || r.errCode(t) != CodeAlreadyResolved {
		t.Errorf("resolving twice: %d %s", r.status, r.body)
	}
	if r := e.post(PathReportsResolve, ResolveRequest{Operator: "owner", ID: someID, Note: "x"}); r.status != http.StatusNotFound {
		t.Errorf("unknown report: %d", r.status)
	}
	if r := e.post(PathReportsResolve, ResolveRequest{Operator: "owner", ID: a.ID, Note: "  "}); r.status != http.StatusBadRequest {
		t.Errorf("empty note: %d", r.status)
	}
	if len(e.actions()) != 2 {
		t.Error("a refused resolution left an audit record")
	}
}

// gatedReports makes every GetReport wait until n callers have read the report,
// so that all of them see it open before any of them writes: the window of the
// check-then-write race, forced.
type gatedReports struct {
	store.Safety
	arrived sync.WaitGroup
}

func (g *gatedReports) GetReport(ctx context.Context, id string) (safety.Report, error) {
	r, err := g.Safety.GetReport(ctx, id)
	g.arrived.Done()
	g.arrived.Wait()
	return r, err
}

func TestConcurrentResolvesHaveExactlyOneWinner(t *testing.T) {
	e := newEnv(t, false)
	a := e.report(1)
	const tries = 8
	gate := &gatedReports{Safety: e.mem}
	gate.arrived.Add(tries)
	var tick atomic.Int64
	e.withDeps(func(d *Deps) {
		d.Reports = gate
		d.Now = func() time.Time { return t0.Add(time.Duration(tick.Add(1)) * time.Minute) }
	})

	results := make([]result, tries)
	var wg sync.WaitGroup
	for i := range tries {
		wg.Go(func() {
			results[i] = e.post(PathReportsResolve, ResolveRequest{Operator: "owner", ID: a.ID, Note: fmt.Sprint("note ", i), Dismiss: i%2 == 1})
		})
	}
	wg.Wait()
	winners := 0
	var won ResolveResponse
	for i, r := range results {
		switch {
		case r.status == http.StatusOK:
			winners++
			r.into(t, &won)
		case r.status != http.StatusConflict || r.errCode(t) != CodeAlreadyResolved:
			t.Errorf("resolution %d: %d %s, want 409 already_resolved", i, r.status, r.body)
		}
	}
	if winners != 1 {
		t.Fatalf("%d resolutions succeeded, want exactly 1", winners)
	}
	if acts := e.actions(); len(acts) != 1 {
		t.Errorf("%d audit records, want 1: %+v", len(acts), acts)
	}
	got, err := e.mem.GetReport(context.Background(), a.ID)
	if err != nil || got.Status != won.Status {
		t.Fatalf("report = %+v, %v; want the winner's status %q", got, err, won.Status)
	}
	// A later resolution keeps the first one's resolved_at and purge_at.
	e.withDeps(func(d *Deps) { d.Reports = e.mem })
	if r := e.post(PathReportsResolve, ResolveRequest{Operator: "owner", ID: a.ID, Note: "late"}); r.status != http.StatusConflict {
		t.Fatalf("a late resolution: %d %s", r.status, r.body)
	}
	if again, _ := e.mem.GetReport(context.Background(), a.ID); !again.ResolvedAt.Equal(got.ResolvedAt) || !again.PurgeAt.Equal(got.PurgeAt) || again.Status != got.Status {
		t.Errorf("a late resolution changed the report: %+v -> %+v", got, again)
	}
}

// gatedMod makes every CreateRestriction wait until n callers are about to
// write, after each has passed the server's own policy check.
type gatedMod struct {
	store.Moderation
	arrived sync.WaitGroup
}

func (g *gatedMod) CreateRestriction(ctx context.Context, r safety.Restriction, a safety.Action) error {
	g.arrived.Done()
	g.arrived.Wait()
	return g.Moderation.CreateRestriction(ctx, r, a)
}

func TestConcurrentRestrictsHaveExactlyOneWinner(t *testing.T) {
	e := newEnv(t, false)
	const tries = 8
	gate := &gatedMod{Moderation: e.mem}
	gate.arrived.Add(tries)
	e.withDeps(func(d *Deps) { d.Mod = gate })

	results := make([]result, tries)
	var wg sync.WaitGroup
	for i := range tries {
		wg.Go(func() {
			results[i] = e.post(PathRestrict, RestrictRequest{Operator: "owner", GuestID: targetGuest, Reason: fmt.Sprint("reason ", i)})
		})
	}
	wg.Wait()
	winners := 0
	for i, r := range results {
		switch {
		case r.status == http.StatusOK:
			winners++
		case r.status != http.StatusConflict || r.errCode(t) != CodeAlreadyRestricted:
			t.Errorf("restrict %d: %d %s, want 409 already_restricted", i, r.status, r.body)
		}
	}
	if winners != 1 {
		t.Fatalf("%d restrictions succeeded, want exactly 1", winners)
	}
	if rs, _ := e.mem.LoadRestrictions(context.Background(), t0); len(rs) != 1 {
		t.Errorf("%d restrictions stored in force, want 1", len(rs))
	}
	if acts := e.actions(); len(acts) != 1 {
		t.Errorf("%d audit records, want 1: %+v", len(acts), acts)
	}
	if _, n := e.policy.Counts(); n != 1 {
		t.Errorf("the policy holds %d restrictions, want 1", n)
	}
}

func TestDecodeRefusesAnythingAfterTheValue(t *testing.T) {
	type req struct {
		A int `json:"a"`
	}
	for name, body := range map[string]string{
		"second object":    `{"a":1}{"b":2}`,
		"second object ws": `{"a":1} {"a":2}`,
		"garbage":          `{"a":1} garbage`,
		"stray brace":      `{"a":1}}`,
		"stray bracket":    `{"a":1}]`,
		"number":           `{"a":1} 7`,
	} {
		err := decode([]byte(body), &req{})
		var ec *errCmd
		if !errors.As(err, &ec) || ec.status != http.StatusBadRequest || ec.code != CodeBadRequest {
			t.Errorf("%s: decode(%q) = %v, want a clean 400 bad_request", name, body, err)
		}
	}
	for name, body := range map[string]string{
		"plain":               `{"a":1}`,
		"trailing whitespace": "{\"a\":1} \n\t\r\n",
	} {
		var v req
		if err := decode([]byte(body), &v); err != nil || v.A != 1 {
			t.Errorf("%s: decode(%q) = %+v, %v", name, body, v, err)
		}
	}
}

func TestRestrictByGuestStoresThenEnforcesAndAudits(t *testing.T) {
	e := newEnv(t, false)
	e.rooms.removed = map[string][]string{"KT7P": {targetGuest}, "ZZZ2": {targetGuest}}
	var atSweep struct{ policy, row bool }
	e.rooms.atSweep = func() {
		atSweep.policy = e.policy.Restricted(targetGuest)
		rs, _ := e.mem.LoadRestrictions(context.Background(), e.now)
		atSweep.row = len(rs) == 1
	}

	var out RestrictResponse
	e.post(PathRestrict, RestrictRequest{Operator: "owner", GuestID: targetGuest, Reason: "SECRET-REASON", Days: 7}).into(t, &out)
	if !atSweep.policy || !atSweep.row {
		t.Errorf("when the rooms were swept the policy knew it = %v and the row was stored = %v: both must come first", atSweep.policy, atSweep.row)
	}
	if out.GuestID != targetGuest || out.UserID != "" || out.RemovedFromRooms != 2 || !out.ExpiresAt.Equal(t0.Add(7*24*time.Hour)) || !store.ValidID(out.ID) {
		t.Errorf("response = %+v", out)
	}
	rs, _ := e.mem.LoadRestrictions(context.Background(), t0)
	if len(rs) != 1 || rs[0].Operator != "owner" || rs[0].Reason != "SECRET-REASON" || rs[0].ReportID != "" || !rs[0].ExpiresAt.Equal(out.ExpiresAt) {
		t.Errorf("stored = %+v", rs)
	}
	acts := e.actions()
	if len(acts) != 1 || acts[0].Action != safety.ActionRestrict || acts[0].TargetKind != safety.TargetGuest || acts[0].TargetID != targetGuest || acts[0].Operator != "owner" {
		t.Errorf("audit = %+v", acts)
	}
	if !e.policy.Restricted(targetGuest) {
		t.Error("the policy does not know the restriction")
	}

	r := e.post(PathRestrict, RestrictRequest{Operator: "owner", GuestID: targetGuest, Reason: "again"})
	if r.status != http.StatusConflict || r.errCode(t) != CodeAlreadyRestricted || !strings.Contains(r.body, out.ID) {
		t.Errorf("second restriction: %d %s", r.status, r.body)
	}

	var forever RestrictResponse // until lifted: no expiry
	e.post(PathRestrict, RestrictRequest{Operator: "owner", GuestID: "other-guest", Reason: "spam"}).into(t, &forever)
	if !forever.ExpiresAt.IsZero() {
		t.Errorf("expiry = %v, want none", forever.ExpiresAt)
	}
}

func TestRestrictRefusalsStoreNothing(t *testing.T) {
	e := newEnv(t, false)
	unverified := e.report(1, func(r *safety.Report) { r.TargetGuest, r.TargetUser, r.EvidenceVerified = "", "", false })
	verified := e.report(2)
	tests := []struct {
		name string
		req  RestrictRequest
		want int
		code string
	}{
		{"no target", RestrictRequest{Operator: "o", Reason: "r"}, 400, CodeBadRequest},
		{"both targets", RestrictRequest{Operator: "o", Reason: "r", GuestID: "g", ReportID: verified.ID}, 400, CodeBadRequest},
		{"no reason", RestrictRequest{Operator: "o", GuestID: "g"}, 400, CodeBadRequest},
		{"reason too long", RestrictRequest{Operator: "o", GuestID: "g", Reason: strings.Repeat("x", safety.MaxReasonRunes+1)}, 400, CodeBadRequest},
		{"negative days", RestrictRequest{Operator: "o", GuestID: "g", Reason: "r", Days: -1}, 400, CodeBadRequest},
		{"too many days", RestrictRequest{Operator: "o", GuestID: "g", Reason: "r", Days: safety.MaxRestrictionDays + 1}, 400, CodeBadRequest},
		{"bad guest id", RestrictRequest{Operator: "o", GuestID: "a b", Reason: "r"}, 400, CodeBadRequest},
		{"an automated seat", RestrictRequest{Operator: "o", GuestID: "bot:Ana", Reason: "r"}, 400, CodeBadRequest},
		{"unknown report", RestrictRequest{Operator: "o", ReportID: someID, Reason: "r"}, 404, CodeNotFound},
		{"a report that names nobody", RestrictRequest{Operator: "o", ReportID: unverified.ID, Reason: "r"}, 422, CodeNoVerifiedTarget},
		{"an account without a report", RestrictRequest{Operator: "o", GuestID: "g", UserID: targetUser, Reason: "r"}, 422, CodeUserNotProven},
		{"an account the report did not prove", RestrictRequest{Operator: "o", ReportID: verified.ID, UserID: otherUser, Reason: "r"}, 422, CodeUserNotProven},
	}
	for _, tc := range tests {
		if r := e.post(PathRestrict, tc.req); r.status != tc.want || r.errCode(t) != tc.code {
			t.Errorf("%s: %d %s, want %d %s", tc.name, r.status, r.body, tc.want, tc.code)
		}
	}
	if rs, _ := e.mem.LoadRestrictions(context.Background(), t0); len(rs) != 0 || len(e.actions()) != 0 || e.rooms.sweeps != 0 {
		t.Errorf("a refused restriction stored %d rows, %d audit records, %d sweeps", len(rs), len(e.actions()), e.rooms.sweeps)
	}
}

func TestRestrictByReportUsesTheReportsTargetAndProvenAccount(t *testing.T) {
	e := newEnv(t, false)
	rep := e.report(1)
	var out RestrictResponse
	e.post(PathRestrict, RestrictRequest{Operator: "owner", ReportID: rep.ID, UserID: targetUser, Reason: "harassment"}).into(t, &out)
	if out.GuestID != targetGuest || out.UserID != targetUser {
		t.Errorf("response = %+v: the target comes from the report, never from the command line", out)
	}
	if !e.policy.Restricted(targetGuest) || !e.policy.RestrictedUser(targetUser) {
		t.Error("the guest and the account must both be restricted")
	}
	if acts := e.actions(); len(acts) != 1 || acts[0].ReportID != rep.ID {
		t.Errorf("audit = %+v", acts)
	}
	// Without --user only the guest is restricted, even if the report knows the account.
	e2 := newEnv(t, false)
	rep2 := e2.report(1)
	var guestOnly RestrictResponse
	e2.post(PathRestrict, RestrictRequest{Operator: "owner", ReportID: rep2.ID, Reason: "x"}).into(t, &guestOnly)
	if guestOnly.UserID != "" || e2.policy.RestrictedUser(targetUser) {
		t.Error("the account was restricted without --user")
	}
}

func TestRestrictAnswersEvenWhenTheSweepFailsAndFailsWhenTheStoreDoes(t *testing.T) {
	e := newEnv(t, false)
	e.rooms.sweepErr = room.ErrShuttingDown
	var out RestrictResponse
	e.post(PathRestrict, RestrictRequest{Operator: "owner", GuestID: targetGuest, Reason: "x"}).into(t, &out)
	if out.Warning == "" || !e.policy.Restricted(targetGuest) {
		t.Errorf("response = %+v: the restriction stands and the operator is told the rooms were not swept", out)
	}

	e2 := newEnv(t, false)
	e2.mod.failCreate = true
	if r := e2.post(PathRestrict, RestrictRequest{Operator: "owner", GuestID: targetGuest, Reason: "x"}); r.status != http.StatusInternalServerError {
		t.Errorf("store failure: %d %s", r.status, r.body)
	}
	if e2.policy.Restricted(targetGuest) || e2.rooms.sweeps != 0 {
		t.Error("the policy changed although the write failed")
	}
}

func TestUnrestrict(t *testing.T) {
	e := newEnv(t, false)
	var r1 RestrictResponse
	e.post(PathRestrict, RestrictRequest{Operator: "owner", GuestID: targetGuest, Reason: "x"}).into(t, &r1)

	e.now = t0.Add(time.Hour)
	var out UnrestrictResponse
	e.post(PathUnrestrict, UnrestrictRequest{Operator: "owner2", ID: r1.ID}).into(t, &out)
	if out.ID != r1.ID || out.GuestID != targetGuest {
		t.Errorf("response = %+v", out)
	}
	if e.policy.Restricted(targetGuest) {
		t.Error("still restricted")
	}
	acts := e.actions()
	if len(acts) != 2 || acts[0].Action != safety.ActionUnrestrict || acts[0].Operator != "owner2" {
		t.Errorf("audit = %+v (newest first)", acts)
	}
	if r := e.post(PathUnrestrict, UnrestrictRequest{Operator: "owner", ID: r1.ID}); r.status != http.StatusConflict || r.errCode(t) != CodeAlreadyEnded {
		t.Errorf("lifting twice: %d %s", r.status, r.body)
	}
	if r := e.post(PathUnrestrict, UnrestrictRequest{Operator: "owner", ID: someID}); r.status != http.StatusNotFound {
		t.Errorf("unknown: %d", r.status)
	}
	if len(e.actions()) != 2 {
		t.Error("a refused unrestrict was audited")
	}
	e.post(PathRestrict, RestrictRequest{Operator: "owner", GuestID: targetGuest, Reason: "again"}).into(t, &r1) // allowed again
}

func TestRoomClose(t *testing.T) {
	e := newEnv(t, false)
	e.rooms.closeResult = room.CloseResult{Code: "KT7P", RoomInstanceID: instanceID}
	var out RoomCloseResponse
	e.post(PathRoomClose, RoomCloseRequest{Operator: "owner", Code: "kt7p", InstanceID: instanceID}).into(t, &out)
	if out.Code != "KT7P" || out.InstanceID != instanceID {
		t.Errorf("response = %+v", out)
	}
	if got := e.rooms.closed; len(got) != 1 || got[0].Code != "KT7P" || got[0].InstanceID != instanceID {
		t.Errorf("the hub was asked %+v: the code is normalised and the instance id travels with it", got)
	}
	acts := e.actions()
	if len(acts) != 1 || acts[0].Action != safety.ActionRoomClose || acts[0].TargetKind != safety.TargetRoom || acts[0].TargetID != instanceID {
		t.Errorf("audit = %+v", acts)
	}

	e.post(PathRoomClose, RoomCloseRequest{Operator: "owner", InstanceID: instanceID}).into(t, &out)
	if got := e.rooms.closed[1]; got.Code != "" || got.InstanceID == "" {
		t.Errorf("close by instance = %+v", got)
	}

	for _, tc := range []struct {
		name string
		err  error
		want int
		code string
	}{
		{"not found", room.ErrRoomNotFound, 404, CodeNotFound},
		{"closed meanwhile", room.ErrRoomClosed, 404, CodeNotFound},
		{"code reused", room.ErrInstanceMismatch, 409, CodeInstanceMismatch},
		{"draining", room.ErrShuttingDown, 503, CodeUnavailable},
	} {
		e.rooms.closeErr = tc.err
		if r := e.post(PathRoomClose, RoomCloseRequest{Operator: "owner", Code: "KT7P"}); r.status != tc.want || r.errCode(t) != tc.code {
			t.Errorf("%s: %d %s", tc.name, r.status, r.body)
		}
	}
	e.rooms.closeErr = nil
	for name, req := range map[string]RoomCloseRequest{
		"nothing":      {Operator: "owner"},
		"bad code":     {Operator: "owner", Code: "KT7P KT7P"},
		"long code":    {Operator: "owner", Code: "ABCDEFGHI"},
		"bad instance": {Operator: "owner", InstanceID: "a b"},
	} {
		if r := e.post(PathRoomClose, req); r.status != http.StatusBadRequest {
			t.Errorf("%s: %d", name, r.status)
		}
	}
	if n := len(e.actions()); n != 2 {
		t.Errorf("%d audit records: only the two closures that happened are audited", n)
	}

	e.mod.failRecord = true // a closure that happened but could not be audited says so
	var warned RoomCloseResponse
	e.post(PathRoomClose, RoomCloseRequest{Operator: "owner", Code: "KT7P"}).into(t, &warned)
	if warned.Warning == "" {
		t.Error("an unaudited closure must warn the operator")
	}
}

func TestProfileRemove(t *testing.T) {
	e := newEnv(t, false)
	u, _ := e.mem.UpsertGoogleUser(context.Background(), "sub-1", "Name")
	_ = e.mem.SaveProfile(context.Background(), u.ID, api.Profile{Nickname: "Nick", Avatar: game.Avatar{Emoji: "🍓", Color: "#fff"}})
	var out ProfileRemoveResponse
	e.post(PathProfileRemove, ProfileRemoveRequest{Operator: "owner", UserID: u.ID}).into(t, &out)
	got, _ := e.mem.GetUser(context.Background(), u.ID)
	if got.Profile != nil || got.Name != "" || out.UserID != u.ID {
		t.Errorf("user = %+v", got)
	}
	acts := e.actions()
	if len(acts) != 1 || acts[0].Action != safety.ActionProfileRemove || acts[0].TargetKind != safety.TargetUser || acts[0].TargetID != u.ID {
		t.Errorf("audit = %+v", acts)
	}
	if r := e.post(PathProfileRemove, ProfileRemoveRequest{Operator: "owner", UserID: someID}); r.status != http.StatusNotFound {
		t.Errorf("unknown user: %d", r.status)
	}
	if r := e.post(PathProfileRemove, ProfileRemoveRequest{Operator: "owner", UserID: "nope"}); r.status != http.StatusBadRequest {
		t.Errorf("malformed user: %d", r.status)
	}
}

func TestJudgeKillSwitch(t *testing.T) {
	e := newEnv(t, true)
	var out JudgeResponse
	e.post(PathJudge, JudgeRequest{Operator: "owner", Action: "disable"}).into(t, &out)
	if e.judge.on || out.Judge != (JudgeStatus{Configured: true, Enabled: false}) {
		t.Errorf("after disable: on=%v %+v", e.judge.on, out)
	}
	e.post(PathJudge, JudgeRequest{Action: "status"}).into(t, &out)
	if out.Judge.Enabled {
		t.Errorf("status = %+v", out)
	}
	e.now = t0.Add(time.Minute)
	e.post(PathJudge, JudgeRequest{Operator: "owner", Action: "enable"}).into(t, &out)
	if !e.judge.on || !out.Judge.Enabled {
		t.Errorf("after enable: on=%v %+v", e.judge.on, out)
	}
	acts := e.actions()
	if len(acts) != 2 || acts[0].Action != safety.ActionJudgeEnable || acts[1].Action != safety.ActionJudgeDisable || acts[0].TargetKind != safety.TargetJudge {
		t.Errorf("audit = %+v", acts)
	}
	if r := e.post(PathJudge, JudgeRequest{Operator: "owner", Action: "explode"}); r.status != http.StatusBadRequest {
		t.Errorf("unknown action: %d", r.status)
	}
	none := newEnv(t, false)
	if r := none.post(PathJudge, JudgeRequest{Operator: "owner", Action: "disable"}); r.status != http.StatusConflict || r.errCode(t) != CodeJudgeNotConfigured {
		t.Errorf("no judge: %d %s", r.status, r.body)
	}
	if r := none.post(PathJudge, JudgeRequest{Action: "status"}); r.status != http.StatusConflict {
		t.Errorf("status with no judge: %d", r.status)
	}
}

func TestPublicGamesSwitch(t *testing.T) {
	e := newEnv(t, true)
	var out PublicResponse
	e.post(PathPublic, PublicRequest{Operator: "owner", Action: "disable"}).into(t, &out)
	if e.public.on || out.Public != (PublicStatus{Configured: true, Enabled: false}) || out.Warning != "" {
		t.Errorf("after disable: on=%v %+v", e.public.on, out)
	}
	if !e.public.gotCt {
		t.Error("the wait for the rooms must be bounded")
	}
	e.post(PathPublic, PublicRequest{Action: "status"}).into(t, &out)
	if out.Public.Enabled || len(e.public.sets) != 1 {
		t.Errorf("status = %+v (sets %v): status must not flip anything", out, e.public.sets)
	}
	e.now = t0.Add(time.Minute)
	e.post(PathPublic, PublicRequest{Operator: "owner", Action: "enable"}).into(t, &out)
	if !e.public.on || !out.Public.Enabled {
		t.Errorf("after enable: on=%v %+v", e.public.on, out)
	}
	acts := e.actions()
	if len(acts) != 2 || acts[0].Action != safety.ActionPublicEnable || acts[1].Action != safety.ActionPublicDisable ||
		acts[0].TargetKind != safety.TargetPublic || acts[0].Operator != "owner" || acts[1].Details != "disabled" {
		t.Errorf("audit = %+v", acts)
	}
	if r := e.post(PathPublic, PublicRequest{Operator: "owner", Action: "explode"}); r.status != http.StatusBadRequest {
		t.Errorf("unknown action: %d", r.status)
	}

	// The rooms did not all confirm in time: the switch is in force anyway, and audited.
	e.public.err = fmt.Errorf("%w: %w", room.ErrPublicRoomsPending, context.DeadlineExceeded)
	e.post(PathPublic, PublicRequest{Operator: "owner", Action: "disable"}).into(t, &out)
	if e.public.on || out.Public.Enabled || !strings.Contains(out.Warning, "in force") || len(e.actions()) != 3 {
		t.Errorf("slow rooms: on=%v %+v, %d audit records", e.public.on, out, len(e.actions()))
	}
	// The audit record could not be stored: the switch stands and the operator is warned.
	e.public.err = nil
	e.mod.failRecord = true
	e.post(PathPublic, PublicRequest{Operator: "owner", Action: "enable"}).into(t, &out)
	if !e.public.on || !strings.Contains(out.Warning, "audit record") || strings.HasPrefix(out.Warning, ";") {
		t.Errorf("failed audit: on=%v %+v", e.public.on, out)
	}

	none := newEnv(t, false)
	none.withDeps(func(d *Deps) { d.Public = nil })
	if r := none.post(PathPublic, PublicRequest{Operator: "owner", Action: "disable"}); r.status != http.StatusConflict || r.errCode(t) != CodePublicNotConfigured {
		t.Errorf("no hub: %d %s", r.status, r.body)
	}
	var st StatusResponse
	none.post(PathStatus, "{}").into(t, &st)
	if st.Public != (PublicStatus{}) {
		t.Errorf("status with no hub = %+v", st.Public)
	}
}

// A switch never moves without its audit record: a request that already ended
// changes nothing, and a client that disconnects once the switch moved still
// gets the record written (with a server-owned, bounded context).
func TestSwitchesNeverMoveWithoutTheirAuditRecord(t *testing.T) {
	type sw struct {
		name   string
		path   string
		req    func(action string) any
		on     func(e *env) bool
		onSet  func(e *env, f func())
		action string
	}
	for _, s := range []sw{
		{"public games", PathPublic, func(a string) any { return PublicRequest{Operator: "owner", Action: a} },
			func(e *env) bool { return e.public.on }, func(e *env, f func()) { e.public.onSet = f }, safety.ActionPublicDisable},
		{"AI judge", PathJudge, func(a string) any { return JudgeRequest{Operator: "owner", Action: a} },
			func(e *env) bool { return e.judge.on }, func(e *env, f func()) { e.judge.onSet = f }, safety.ActionJudgeDisable},
	} {
		t.Run(s.name+": the request already ended", func(t *testing.T) {
			e := newEnv(t, true)
			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			r := e.postCtx(ctx, s.path, s.req("disable"))
			if r.status != http.StatusServiceUnavailable || r.errCode(t) != CodeUnavailable {
				t.Errorf("status %d %s, want 503 unavailable", r.status, r.body)
			}
			if !s.on(e) || len(e.actions()) != 0 {
				t.Errorf("the switch moved (%v) or was audited (%d) for a request that had ended", !s.on(e), len(e.actions()))
			}
		})
		t.Run(s.name+": the client disconnects right after the switch moved", func(t *testing.T) {
			e := newEnv(t, true)
			e.mod.ctxAware = true // like the database: a cancelled context fails the write
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			s.onSet(e, cancel)
			e.postCtx(ctx, s.path, s.req("disable"))
			if s.on(e) {
				t.Fatal("the switch did not move")
			}
			acts := e.actions()
			if len(acts) != 1 || acts[0].Action != s.action || acts[0].Operator != "owner" {
				t.Errorf("audit = %+v, want the %s record although the client left", acts, s.action)
			}
		})
	}
	t.Run("a room closure is audited although the client left", func(t *testing.T) {
		e := newEnv(t, true)
		e.mod.ctxAware = true
		e.rooms.closeResult = room.CloseResult{Code: "KT7P", RoomInstanceID: instanceID}
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		e.rooms.atClose = cancel
		e.postCtx(ctx, PathRoomClose, RoomCloseRequest{Operator: "owner", Code: "KT7P"})
		if acts := e.actions(); len(acts) != 1 || acts[0].Action != safety.ActionRoomClose {
			t.Errorf("audit = %+v", acts)
		}
	})
}

func TestLoadPolicyLoadsBlocksAndTheRestrictionsInForce(t *testing.T) {
	mem := store.NewMemory()
	ctx := context.Background()
	for i, b := range []string{"b1", "b2"} {
		if _, _, err := mem.AddBlock(ctx, safety.Block{Blocker: "a", Blocked: b, CreatedAt: t0.Add(time.Duration(i))}, 10); err != nil {
			t.Fatal(err)
		}
	}
	mk := func(n int, mod func(*safety.Restriction)) {
		r := safety.Restriction{ID: fmt.Sprintf("00000000-0000-4000-8000-%012d", n), GuestID: fmt.Sprintf("g%d", n), Reason: "r", Operator: "o", CreatedAt: t0.Add(-time.Hour)}
		mod(&r)
		audit := safety.Action{ID: fmt.Sprintf("00000000-0000-4000-8000-%012d", 100+n), At: t0, Operator: "o", Action: safety.ActionRestrict, TargetKind: safety.TargetGuest, TargetID: r.GuestID}
		if err := mem.CreateRestriction(ctx, r, audit); err != nil {
			t.Fatal(err)
		}
	}
	mk(1, func(*safety.Restriction) {})
	mk(2, func(r *safety.Restriction) { r.ExpiresAt = t0.Add(time.Hour) })
	mk(3, func(r *safety.Restriction) { r.ExpiresAt = t0.Add(-time.Minute) })

	p := safety.NewPolicy(func() time.Time { return t0 })
	nb, nr, err := LoadPolicy(ctx, mem, p, t0)
	if err != nil || nb != 2 || nr != 2 {
		t.Fatalf("LoadPolicy = %d, %d, %v", nb, nr, err)
	}
	if !p.Blocks("a", "b1") || !p.Blocks("a", "b2") || !p.Restricted("g1") || !p.Restricted("g2") || p.Restricted("g3") {
		t.Error("the policy does not reflect the store")
	}
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if _, _, err := LoadPolicy(cancelled, mem, safety.NewPolicy(nil), t0); err == nil {
		t.Error("a store that cannot answer must be an error: the server must not start with an empty policy")
	}
}
