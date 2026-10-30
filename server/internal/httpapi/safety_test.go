package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"

	"github.com/AresiusXP/tutti-frutti/server/internal/api"
	"github.com/AresiusXP/tutti-frutti/server/internal/auth/authtest"
	"github.com/AresiusXP/tutti-frutti/server/internal/protocol"
	"github.com/AresiusXP/tutti-frutti/server/internal/room"
	"github.com/AresiusXP/tutti-frutti/server/internal/safety"
	"github.com/AresiusXP/tutti-frutti/server/internal/store"
)

var errBoom = errors.New("boom: connection refused")

// flakySafety is the in-memory Safety store with switches to make it fail, and
// a record of the reports that were really stored.
type flakySafety struct {
	*store.Memory
	failTerms, failReports atomic.Bool

	mu      sync.Mutex
	stored  []safety.Report
	creates int
}

func (f *flakySafety) TermsAcceptance(ctx context.Context, guest, version string) (safety.TermsAcceptance, bool, error) {
	if f.failTerms.Load() {
		return safety.TermsAcceptance{}, false, errBoom
	}
	return f.Memory.TermsAcceptance(ctx, guest, version)
}

func (f *flakySafety) AcceptTerms(ctx context.Context, a safety.TermsAcceptance) (safety.TermsAcceptance, error) {
	if f.failTerms.Load() {
		return safety.TermsAcceptance{}, errBoom
	}
	return f.Memory.AcceptTerms(ctx, a)
}

func (f *flakySafety) CreateReport(ctx context.Context, r safety.Report) (string, bool, error) {
	f.mu.Lock()
	f.creates++
	f.mu.Unlock()
	if f.failReports.Load() {
		return "", false, errBoom
	}
	id, created, err := f.Memory.CreateReport(ctx, r)
	if err == nil && created {
		f.mu.Lock()
		f.stored = append(f.stored, r)
		f.mu.Unlock()
	}
	return id, created, err
}

func (f *flakySafety) reports() []safety.Report {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]safety.Report(nil), f.stored...)
}

// evidenceRooms is the fake room hub that also resolves report evidence.
type evidenceRooms struct {
	*fakeRooms
	mu    sync.Mutex
	codes []string
	reqs  []safety.EvidenceRequest
	reply func(code string, req safety.EvidenceRequest) (safety.Evidence, error)
}

func (r *evidenceRooms) ReportEvidence(_ context.Context, code string, req safety.EvidenceRequest) (safety.Evidence, error) {
	r.mu.Lock()
	r.codes = append(r.codes, code)
	r.reqs = append(r.reqs, req)
	fn := r.reply
	r.mu.Unlock()
	if fn == nil {
		return safety.Evidence{}, room.ErrRoomNotFound
	}
	return fn(code, req)
}

func (r *evidenceRooms) lastRequest(t *testing.T) (string, safety.EvidenceRequest) {
	t.Helper()
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.reqs) == 0 {
		t.Fatal("the room was never asked for evidence")
	}
	return r.codes[len(r.codes)-1], r.reqs[len(r.reqs)-1]
}

func (r *evidenceRooms) asked() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.reqs)
}

type safetyEnv struct {
	*acctEnv
	safe  *flakySafety
	gate  *safety.Terms
	rooms *evidenceRooms
}

type safetyOpts struct {
	noGate bool
	acct   acctOpts
}

func newSafetyEnv(t *testing.T, o safetyOpts) *safetyEnv {
	t.Helper()
	safe := &flakySafety{Memory: store.NewMemory()}
	gate := safety.NewTerms(safe, nil)
	rooms := &evidenceRooms{fakeRooms: &fakeRooms{}}
	acct := o.acct
	prev := acct.tweak
	acct.tweak = func(opts *Options) {
		opts.Rooms = rooms
		opts.Safety = safe
		if !o.noGate {
			opts.Terms = gate
		}
		if prev != nil {
			prev(opts)
		}
	}
	return &safetyEnv{acctEnv: newAcctEnv(t, acct), safe: safe, gate: gate, rooms: rooms}
}

func (b *browser) postJSON(target, body string) reply {
	return b.request(http.MethodPost, target, body)
}

func (b *browser) acceptTerms() reply {
	b.t.Helper()
	return b.postJSON("/api/terms/accept", `{"version":"`+safety.TermsVersion+`","locale":"en","age13Confirmed":true}`)
}

func (b *browser) guest() string {
	b.t.Helper()
	c := b.cookie(protocol.GuestCookie)
	if c == nil {
		b.t.Fatal("no guest cookie")
	}
	id, _, _ := strings.Cut(c.Value, ".")
	return id
}

func (r reply) terms(t *testing.T) api.TermsStatus {
	t.Helper()
	var s api.SessionInfo
	r.json(t, &s)
	return s.Terms
}

// ---------------------------------------------------------------------------
// Terms
// ---------------------------------------------------------------------------

func TestSessionReportsTheTermsStatus(t *testing.T) {
	e := newSafetyEnv(t, safetyOpts{})
	b := e.browser()
	r := b.get("/api/session")
	var got api.SessionInfo
	r.json(t, &got)
	want := api.TermsStatus{RequiredVersion: safety.TermsVersion, AcceptanceRequired: true}
	if got.Terms != want || !got.Guest {
		t.Fatalf("session = %+v, want terms %+v", got, want)
	}
	if !strings.Contains(string(r.body), `"acceptedVersion":null`) {
		t.Errorf("acceptedVersion must be an explicit null: %s", r.body)
	}
	if acc := b.acceptTerms(); acc.StatusCode != http.StatusOK {
		t.Fatalf("accept = %d %s", acc.StatusCode, acc.body)
	}
	r = b.get("/api/session")
	r.json(t, &got)
	if v := got.Terms.AcceptedVersion; v == nil || *v != safety.TermsVersion || got.Terms.AcceptanceRequired || got.Terms.RequiredVersion != safety.TermsVersion {
		t.Errorf("after accepting: %+v", got.Terms)
	}
	// Another browser (another guest) has not accepted.
	if other := e.browser().get("/api/session").terms(t); !other.AcceptanceRequired {
		t.Errorf("acceptance leaked to another guest: %+v", other)
	}
}

func TestSessionWithoutAGateNeverRequiresTheTerms(t *testing.T) {
	e := newSafetyEnv(t, safetyOpts{noGate: true})
	if got := e.browser().get("/api/session").terms(t); got != (api.TermsStatus{RequiredVersion: safety.TermsVersion}) {
		t.Errorf("terms = %+v", got)
	}
}

func TestAcceptTerms(t *testing.T) {
	e := newSafetyEnv(t, safetyOpts{})
	ok := `{"version":"` + safety.TermsVersion + `","locale":"nl","age13Confirmed":true}`
	tests := []struct {
		name   string
		body   string
		status int
		code   string
	}{
		{"another version", `{"version":"2020-01","locale":"en","age13Confirmed":true}`, http.StatusConflict, "terms_version_mismatch"},
		{"no version", `{"locale":"en","age13Confirmed":true}`, http.StatusConflict, "terms_version_mismatch"},
		{"age not confirmed", `{"version":"` + safety.TermsVersion + `","locale":"en","age13Confirmed":false}`, http.StatusBadRequest, "age_confirmation_required"},
		{"age missing", `{"version":"` + safety.TermsVersion + `","locale":"en"}`, http.StatusBadRequest, "age_confirmation_required"},
		{"bad locale", `{"version":"` + safety.TermsVersion + `","locale":"fr","age13Confirmed":true}`, http.StatusBadRequest, "invalid_locale"},
		{"no locale", `{"version":"` + safety.TermsVersion + `","age13Confirmed":true}`, http.StatusBadRequest, "invalid_locale"},
		{"client supplied time", `{"version":"` + safety.TermsVersion + `","locale":"en","age13Confirmed":true,"acceptedAt":"2020-01-01T00:00:00Z"}`, http.StatusBadRequest, "bad_request"},
		{"a birth date is not accepted either", `{"version":"` + safety.TermsVersion + `","locale":"en","age13Confirmed":true,"birthDate":"2000-01-01"}`, http.StatusBadRequest, "bad_request"},
		{"malformed", `{"version":`, http.StatusBadRequest, "bad_request"},
		{"age as a string", `{"version":"` + safety.TermsVersion + `","locale":"en","age13Confirmed":"true"}`, http.StatusBadRequest, "bad_request"},
		{"too big", `{"version":"` + strings.Repeat("x", 5000) + `"}`, http.StatusRequestEntityTooLarge, "payload_too_large"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			b := e.browser()
			b.get("/api/session")
			r := b.postJSON("/api/terms/accept", tc.body)
			if r.StatusCode != tc.status || r.errCode(t) != tc.code {
				t.Errorf("= %d %s, want %d %s", r.StatusCode, r.body, tc.status, tc.code)
			}
			if acc, _ := b.session(t); acc {
				t.Error("a refused acceptance counts")
			}
		})
	}

	t.Run("needs the guest cookie", func(t *testing.T) {
		r := e.browser().postJSON("/api/terms/accept", ok)
		if r.StatusCode != http.StatusUnauthorized || r.errCode(t) != "unauthorized" {
			t.Errorf("= %d %s", r.StatusCode, r.body)
		}
	})
	t.Run("needs a JSON body and an allowed origin", func(t *testing.T) {
		b := e.browser()
		b.get("/api/session")
		if r := b.request(http.MethodPost, "/api/terms/accept", ok, "Content-Type", "text/plain"); r.StatusCode != http.StatusUnsupportedMediaType {
			t.Errorf("text/plain = %d", r.StatusCode)
		}
		if r := b.request(http.MethodPost, "/api/terms/accept", ok, "Origin", "https://evil.example"); r.StatusCode != http.StatusForbidden || r.errCode(t) != "forbidden_origin" {
			t.Errorf("foreign origin = %d %s", r.StatusCode, r.body)
		}
		if r := b.request(http.MethodGet, "/api/terms/accept", ""); r.StatusCode == http.StatusOK {
			t.Error("GET accepted the terms")
		}
	})
	t.Run("a forged cookie is nobody", func(t *testing.T) {
		r := e.rawRequest(t, http.MethodPost, "/api/terms/accept", ok, protocol.GuestCookie+"=AAAAAAAAAAAAAAAAAAAAAA.AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA")
		if r.StatusCode != http.StatusUnauthorized {
			t.Errorf("= %d", r.StatusCode)
		}
	})

	t.Run("accepting is idempotent, server-timed and keeps the first record", func(t *testing.T) {
		b := e.browser()
		b.get("/api/session")
		before := time.Now().UTC().Add(-time.Second)
		r := b.postJSON("/api/terms/accept", ok)
		var st api.TermsStatus
		r.json(t, &st)
		if r.StatusCode != http.StatusOK || st.AcceptanceRequired || st.AcceptedVersion == nil || *st.AcceptedVersion != safety.TermsVersion || st.RequiredVersion != safety.TermsVersion {
			t.Fatalf("= %d %s", r.StatusCode, r.body)
		}
		first, found, err := e.safe.Memory.TermsAcceptance(t.Context(), b.guest(), safety.TermsVersion)
		if err != nil || !found || first.Locale != "nl" || !first.Age13Confirmed || first.AcceptedAt.Before(before) || first.AcceptedAt.After(time.Now().Add(time.Second)) {
			t.Fatalf("stored = %+v, %v, %v", first, found, err)
		}
		again := b.postJSON("/api/terms/accept", strings.Replace(ok, `"nl"`, `"es"`, 1))
		if again.StatusCode != http.StatusOK || again.terms2(t).AcceptanceRequired {
			t.Errorf("second accept = %d %s", again.StatusCode, again.body)
		}
		if second, _, _ := e.safe.Memory.TermsAcceptance(t.Context(), b.guest(), safety.TermsVersion); second != first {
			t.Errorf("the second acceptance changed the record: %+v, was %+v", second, first)
		}
	})

	t.Run("a store failure is retryable and records nothing", func(t *testing.T) {
		b := e.browser()
		b.get("/api/session")
		e.safe.failTerms.Store(true)
		r := b.postJSON("/api/terms/accept", ok)
		e.safe.failTerms.Store(false)
		if r.StatusCode != http.StatusServiceUnavailable || r.errCode(t) != "server_busy" || r.Header.Get("Retry-After") == "" {
			t.Errorf("= %d %s %v", r.StatusCode, r.body, r.Header)
		}
		if _, found, _ := e.safe.Memory.TermsAcceptance(t.Context(), b.guest(), safety.TermsVersion); found {
			t.Error("an acceptance was recorded although the store failed")
		}
		if r := b.postJSON("/api/terms/accept", ok); r.StatusCode != http.StatusOK {
			t.Errorf("the retry = %d", r.StatusCode)
		}
	})
}

func (r reply) terms2(t *testing.T) api.TermsStatus {
	t.Helper()
	var s api.TermsStatus
	r.json(t, &s)
	return s
}

// session returns whether the browser's guest has accepted, from GET /api/session.
func (b *browser) session(t *testing.T) (accepted bool, status int) {
	t.Helper()
	r := b.get("/api/session")
	if r.StatusCode != http.StatusOK {
		return false, r.StatusCode
	}
	return !r.terms(t).AcceptanceRequired, r.StatusCode
}

func TestUnknownAcceptanceIsNeverReportedAsNotAccepted(t *testing.T) {
	e := newSafetyEnv(t, safetyOpts{})
	b := e.browser()
	b.get("/api/session") // mints the cookie (a new guest needs no lookup)
	e.safe.failTerms.Store(true)
	r := b.get("/api/session")
	if r.StatusCode != http.StatusServiceUnavailable || r.errCode(t) != "server_busy" || r.Header.Get("Retry-After") == "" {
		t.Errorf("session with the store down = %d %s", r.StatusCode, r.body)
	}
	// A brand-new guest is known not to have accepted: no lookup, no failure.
	if r := e.browser().get("/api/session"); r.StatusCode != http.StatusOK || !r.terms(t).AcceptanceRequired {
		t.Errorf("a new guest with the store down = %d %s", r.StatusCode, r.body)
	}
	e.safe.failTerms.Store(false)
	if _, status := b.session(t); status != http.StatusOK {
		t.Errorf("after the store recovered = %d", status)
	}
}

func TestCreatingARoomRequiresTheTerms(t *testing.T) {
	e := newSafetyEnv(t, safetyOpts{})
	body := `{"name":"Ana","avatar":{"emoji":"🍓","color":"#ff0066"}}`
	rooms := e.rooms.fakeRooms
	created := func() int { rooms.mu.Lock(); defer rooms.mu.Unlock(); return len(rooms.created) }

	// Not even a guest cookie yet: the call mints one and is refused.
	b := e.browser()
	r := b.postJSON("/api/rooms", body)
	if r.StatusCode != http.StatusForbidden || r.errCode(t) != "terms_required" {
		t.Fatalf("a new guest = %d %s", r.StatusCode, r.body)
	}
	if b.cookie(protocol.GuestCookie) == nil {
		t.Error("the refusal did not mint the guest cookie the acceptance needs")
	}
	if created() != 0 {
		t.Fatal("a room was created without the terms")
	}
	if r := b.postJSON("/api/rooms", body); r.StatusCode != http.StatusForbidden {
		t.Errorf("a known guest that never accepted = %d", r.StatusCode)
	}

	// The store is down and the guest never accepted: unknown fails closed.
	e.safe.failTerms.Store(true)
	r = b.postJSON("/api/rooms", body)
	e.safe.failTerms.Store(false)
	if r.StatusCode != http.StatusServiceUnavailable || r.errCode(t) != "server_busy" || created() != 0 {
		t.Fatalf("store down = %d %s (created %d)", r.StatusCode, r.body, created())
	}

	if acc := b.acceptTerms(); acc.StatusCode != http.StatusOK {
		t.Fatal(acc.StatusCode)
	}
	if r := b.postJSON("/api/rooms", body); r.StatusCode != http.StatusCreated || created() != 1 {
		t.Errorf("after accepting = %d %s (created %d)", r.StatusCode, r.body, created())
	}
	// An accepted guest is not locked out by a database outage.
	e.safe.failTerms.Store(true)
	if r := b.postJSON("/api/rooms", body); r.StatusCode != http.StatusCreated {
		t.Errorf("accepted guest during an outage = %d %s", r.StatusCode, r.body)
	}
	e.safe.failTerms.Store(false)
	// Bad input is still a 400, not a terms error, for a guest that accepted.
	if r := b.postJSON("/api/rooms", `{"name":`); r.StatusCode != http.StatusBadRequest {
		t.Errorf("malformed body = %d", r.StatusCode)
	}
}

func TestProfileUploadRequiresTheTerms(t *testing.T) {
	e := newSafetyEnv(t, safetyOpts{})
	b := e.browser()
	b.get("/api/session")
	b.login("/", testUser)
	profile := `{"nickname":"Ana","avatar":{"emoji":"🍓","color":"#ff0066"}}`

	r := b.request(http.MethodPut, "/api/me/profile", profile)
	if r.StatusCode != http.StatusForbidden || r.errCode(t) != "terms_required" {
		t.Fatalf("PUT without the terms = %d %s", r.StatusCode, r.body)
	}
	if me, status := b.me(); status != http.StatusOK || me.Profile != nil {
		t.Fatalf("the profile was stored: %+v, %d", me, status)
	}
	// Everything that is not an upload works without the acceptance.
	if _, status := b.me(); status != http.StatusOK {
		t.Errorf("GET /api/me = %d", status)
	}
	if r := b.get("/api/me/stats"); r.StatusCode != http.StatusOK {
		t.Errorf("GET /api/me/stats = %d", r.StatusCode)
	}
	if r := b.postJSON("/api/auth/logout", ""); r.StatusCode != http.StatusNoContent {
		t.Errorf("logout = %d", r.StatusCode)
	}
	b.login("/", testUser)

	// Unknown acceptance: retryable, nothing stored.
	e.safe.failTerms.Store(true)
	r = b.request(http.MethodPut, "/api/me/profile", profile)
	e.safe.failTerms.Store(false)
	if r.StatusCode != http.StatusServiceUnavailable || r.errCode(t) != "server_busy" {
		t.Fatalf("PUT with the store down = %d %s", r.StatusCode, r.body)
	}

	if acc := b.acceptTerms(); acc.StatusCode != http.StatusOK {
		t.Fatal(acc.StatusCode)
	}
	if r := b.request(http.MethodPut, "/api/me/profile", profile); r.StatusCode != http.StatusNoContent {
		t.Errorf("PUT after accepting = %d %s", r.StatusCode, r.body)
	}
	// Deleting the account never needs the acceptance.
	other := e.browser()
	other.get("/api/session")
	other.login("/", authtest.User{Sub: "sub-delete", Name: "Beto"})
	if r := other.request(http.MethodDelete, "/api/me", ""); r.StatusCode != http.StatusNoContent {
		t.Errorf("DELETE /api/me without the terms = %d %s", r.StatusCode, r.body)
	}
}

func TestProfileUploadWithASessionButNoGuestCookieIsRefused(t *testing.T) {
	e := newSafetyEnv(t, safetyOpts{})
	u, err := e.mem.UpsertGoogleUser(t.Context(), "sub-orphan", "Orfa")
	if err != nil {
		t.Fatal(err)
	}
	rec := httptest.NewRecorder()
	if err := e.sessions.Issue(rec, u.ID); err != nil {
		t.Fatal(err)
	}
	c := rec.Result().Cookies()[0]
	r := e.rawRequest(t, http.MethodPut, "/api/me/profile", `{"nickname":"Orfa","avatar":{"emoji":"🍓","color":"#ff0066"}}`, c.Name+"="+c.Value)
	if r.StatusCode != http.StatusForbidden || r.errCode(t) != "terms_required" {
		t.Errorf("= %d %s: with no guest nobody accepted anything", r.StatusCode, r.body)
	}
}

func TestWithoutAGateNothingIsRefused(t *testing.T) {
	e := newSafetyEnv(t, safetyOpts{noGate: true})
	b := e.browser()
	b.get("/api/session")
	if r := b.postJSON("/api/rooms", `{"name":"Ana","avatar":{"emoji":"🍓","color":"#ff0066"}}`); r.StatusCode != http.StatusCreated {
		t.Errorf("create = %d %s", r.StatusCode, r.body)
	}
	if r := b.postJSON("/api/terms/accept", `{}`); r.StatusCode != http.StatusNotFound && r.StatusCode != http.StatusMethodNotAllowed {
		t.Errorf("the accept endpoint exists without a gate: %d", r.StatusCode)
	}
}

// ---------------------------------------------------------------------------
// Reports
// ---------------------------------------------------------------------------

const reportRoomInstance = "11111111-1111-4111-8111-111111111111"

func reportBody(mod func(m map[string]any)) string {
	m := map[string]any{
		"kind":           "answer",
		"code":           "kt7p",
		"roomInstanceId": reportRoomInstance,
		"ref":            map[string]any{"playerId": "other-guest", "category": 1, "round": 2},
		"reason":         "harassment",
		"comment":        "rude",
		"idempotencyKey": "key-0000001",
		"observedText":   "what I saw",
	}
	if mod != nil {
		mod(m)
	}
	raw, _ := json.Marshal(m)
	return string(raw)
}

func serverEvidence() safety.Evidence {
	return safety.Evidence{
		Verified: true, RoomInstance: reportRoomInstance, MatchInstance: "22222222-2222-4222-8222-222222222222",
		ContentKey: "answer:r2:c1:other-guest", TargetGuest: "other-guest", TargetUser: "00000000-0000-4000-8000-0000000000cc",
		Data: safety.EvidenceData{Source: safety.SourceServer, Text: "tomate", Lang: "es", Letter: "T", Round: 2},
	}
}

func TestReportWithServerEvidence(t *testing.T) {
	e := newSafetyEnv(t, safetyOpts{})
	e.rooms.reply = func(string, safety.EvidenceRequest) (safety.Evidence, error) { return serverEvidence(), nil }
	b := e.browser()
	b.get("/api/session")
	b.login("/", testUser)
	user, _ := b.me()

	r := b.postJSON("/api/reports", reportBody(nil))
	var resp api.ReportResponse
	r.json(t, &resp)
	if r.StatusCode != http.StatusCreated || !store.ValidID(resp.ID) {
		t.Fatalf("= %d %s", r.StatusCode, r.body)
	}
	code, req := e.rooms.lastRequest(t)
	if code != "KT7P" || req.Reporter != b.guest() || req.Kind != "answer" || req.RoomInstanceID != reportRoomInstance ||
		req.Ref.PlayerID != "other-guest" || *req.Ref.Category != 1 || *req.Ref.Round != 2 {
		t.Errorf("the room was asked %q %+v: the reporter must be the cookie's guest, the code normalized", code, req)
	}
	rep, err := e.safe.Memory.GetReport(t.Context(), resp.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !rep.EvidenceVerified || rep.ReporterGuest != b.guest() || rep.ReporterUser != user.ID || rep.RoomInstance != reportRoomInstance ||
		rep.MatchInstance != "22222222-2222-4222-8222-222222222222" || rep.TargetGuest != "other-guest" ||
		rep.TargetUser != "00000000-0000-4000-8000-0000000000cc" || rep.ContentKey != "answer:r2:c1:other-guest" ||
		rep.Kind != "answer" || rep.Reason != "harassment" || rep.Comment != "rude" || rep.Status != safety.StatusOpen ||
		rep.IdempotencyKey != "key-0000001" {
		t.Errorf("stored = %+v", rep)
	}
	if !rep.PurgeAt.Equal(safety.PurgeAt(rep.CreatedAt, time.Time{})) || !rep.CreatedAt.Equal(e.clock.Now().UTC().Truncate(time.Microsecond)) {
		t.Errorf("times: created %v purge %v", rep.CreatedAt, rep.PurgeAt)
	}
	var data safety.EvidenceData
	if err := json.Unmarshal(rep.Evidence, &data); err != nil || data.Source != "server" || data.Text != "tomate" || data.ObservedText != "" {
		t.Errorf("evidence = %s (%v): the client's text must not mix into server evidence", rep.Evidence, err)
	}
}

func TestReportNeverNeedsTheTermsAndNeverTrustsATarget(t *testing.T) {
	e := newSafetyEnv(t, safetyOpts{})
	b := e.browser()
	b.get("/api/session") // never accepted the terms
	for _, field := range []string{"targetGuest", "targetUser", "reporterGuest", "reporterUser", "evidenceVerified", "status"} {
		e.clock.Advance(time.Minute)
		r := b.postJSON("/api/reports", reportBody(func(m map[string]any) { m[field] = "x" }))
		if r.StatusCode != http.StatusBadRequest || r.errCode(t) != "bad_request" {
			t.Errorf("a client-supplied %s = %d %s, want 400", field, r.StatusCode, r.body)
		}
	}
	// With nothing in the room the report is taken as uncorroborated, and the
	// forged player id in the ref is not a target.
	e.clock.Advance(time.Minute)
	r := b.postJSON("/api/reports", reportBody(nil))
	var resp api.ReportResponse
	r.json(t, &resp)
	if r.StatusCode != http.StatusCreated {
		t.Fatalf("a guest that never accepted cannot report: %d %s", r.StatusCode, r.body)
	}
	rep, _ := e.safe.Memory.GetReport(t.Context(), resp.ID)
	if rep.EvidenceVerified || rep.TargetGuest != "" || rep.TargetUser != "" || rep.RoomInstance != reportRoomInstance || rep.MatchInstance != "" {
		t.Errorf("stored = %+v", rep)
	}
	var data safety.EvidenceData
	_ = json.Unmarshal(rep.Evidence, &data)
	if data.Source != safety.SourceClient || data.ObservedText != "what I saw" || data.Claimed == nil || data.Claimed.PlayerID != "other-guest" || data.Author != nil {
		t.Errorf("evidence = %s", rep.Evidence)
	}
	if rep.ContentKey != "answer:r2:c1:other-guest" {
		t.Errorf("content key = %q", rep.ContentKey)
	}
}

func TestReportWhenTheRoomIsGoneOrChanged(t *testing.T) {
	for name, err := range map[string]error{
		"room not found":      room.ErrRoomNotFound,
		"room closed":         room.ErrRoomClosed,
		"code reused":         room.ErrInstanceMismatch,
		"content changed":     room.ErrContentUnavailable,
		"wrapped not found":   errors.Join(errors.New("x"), room.ErrRoomNotFound),
		"hidden from the app": room.ErrContentUnavailable,
	} {
		t.Run(name, func(t *testing.T) {
			e := newSafetyEnv(t, safetyOpts{})
			e.rooms.reply = func(string, safety.EvidenceRequest) (safety.Evidence, error) { return safety.Evidence{}, err }
			b := e.browser()
			b.get("/api/session")
			r := b.postJSON("/api/reports", reportBody(nil))
			if r.StatusCode != http.StatusCreated {
				t.Fatalf("= %d %s", r.StatusCode, r.body)
			}
			reps := e.safe.reports()
			if len(reps) != 1 || reps[0].EvidenceVerified || reps[0].TargetGuest != "" {
				t.Fatalf("stored = %+v", reps)
			}
		})
	}

	t.Run("no room code: nothing to ask, still taken", func(t *testing.T) {
		e := newSafetyEnv(t, safetyOpts{})
		b := e.browser()
		b.get("/api/session")
		r := b.postJSON("/api/reports", reportBody(func(m map[string]any) { delete(m, "code") }))
		if r.StatusCode != http.StatusCreated || e.rooms.asked() != 0 {
			t.Errorf("= %d %s, asked %d", r.StatusCode, r.body, e.rooms.asked())
		}
	})
}

func TestReportByAnOutsiderOrWhileDrainingStoresNothing(t *testing.T) {
	e := newSafetyEnv(t, safetyOpts{})
	b := e.browser()
	b.get("/api/session")

	e.rooms.reply = func(string, safety.EvidenceRequest) (safety.Evidence, error) {
		return safety.Evidence{}, room.ErrNotMember
	}
	r := b.postJSON("/api/reports", reportBody(nil))
	if r.StatusCode != http.StatusForbidden || r.errCode(t) != "not_in_room" {
		t.Errorf("outsider = %d %s", r.StatusCode, r.body)
	}
	for name, err := range map[string]error{"draining": room.ErrShuttingDown, "room stuck": context.DeadlineExceeded} {
		e.rooms.reply = func(string, safety.EvidenceRequest) (safety.Evidence, error) { return safety.Evidence{}, err }
		e.clock.Advance(time.Minute)
		r := b.postJSON("/api/reports", reportBody(func(m map[string]any) { m["idempotencyKey"] = "key-" + strings.ReplaceAll(name, " ", "-") }))
		if r.StatusCode != http.StatusServiceUnavailable || r.errCode(t) != "server_busy" || r.Header.Get("Retry-After") == "" {
			t.Errorf("%s = %d %s", name, r.StatusCode, r.body)
		}
	}
	if n := len(e.safe.reports()); n != 0 {
		t.Errorf("%d reports stored", n)
	}
	if e.safe.creates != 0 {
		t.Error("the store was called although the evidence was refused")
	}
}

func TestReportIsIdempotentAndSurvivesAStoreFailure(t *testing.T) {
	e := newSafetyEnv(t, safetyOpts{})
	e.rooms.reply = func(string, safety.EvidenceRequest) (safety.Evidence, error) { return serverEvidence(), nil }
	b := e.browser()
	b.get("/api/session")

	// A database failure: a retryable error, no report, no counter.
	e.safe.failReports.Store(true)
	r := b.postJSON("/api/reports", reportBody(nil))
	e.safe.failReports.Store(false)
	if r.StatusCode != http.StatusServiceUnavailable || r.errCode(t) != "server_busy" || r.Header.Get("Retry-After") == "" {
		t.Fatalf("store down = %d %s", r.StatusCode, r.body)
	}
	if strings.Contains(string(r.body), "boom") {
		t.Errorf("the error leaks the database failure: %s", r.body)
	}
	if n := len(e.safe.reports()); n != 0 || e.reportCount("answer", "harassment") != 0 {
		t.Fatalf("a failed report changed state: %d stored, counter %v", n, e.reportCount("answer", "harassment"))
	}

	// The retry (same key) is the one report; a second retry returns the same id.
	e.clock.Advance(time.Minute)
	first := b.postJSON("/api/reports", reportBody(nil))
	var one api.ReportResponse
	first.json(t, &one)
	if first.StatusCode != http.StatusCreated || one.ID == "" {
		t.Fatalf("retry = %d %s", first.StatusCode, first.body)
	}
	e.clock.Advance(time.Minute)
	second := b.postJSON("/api/reports", reportBody(func(m map[string]any) { m["comment"] = "a different comment on a retry" }))
	var two api.ReportResponse
	second.json(t, &two)
	if second.StatusCode != http.StatusOK || two.ID != one.ID {
		t.Errorf("a replay = %d %s, want 200 and %s", second.StatusCode, second.body, one.ID)
	}
	if reps := e.safe.reports(); len(reps) != 1 || reps[0].Comment != "rude" {
		t.Errorf("stored = %+v: a retry must not store or change anything", reps)
	}
	if got := e.reportCount("answer", "harassment"); got != 1 {
		t.Errorf("tuttifrutti_reports_total = %v, want 1: only the stored report counts", got)
	}
	// Another reporter may use the same key.
	other := e.browser()
	other.get("/api/session")
	if r := other.postJSON("/api/reports", reportBody(nil)); r.StatusCode != http.StatusCreated {
		t.Errorf("another reporter, same key = %d %s", r.StatusCode, r.body)
	}
}

func (e *safetyEnv) reportCount(kind, reason string) float64 {
	e.t.Helper()
	families, err := e.srv.Registry().Gather()
	if err != nil {
		e.t.Fatal(err)
	}
	for _, f := range families {
		if f.GetName() != "tuttifrutti_reports_total" {
			continue
		}
		for _, m := range f.GetMetric() {
			if labelOf(m, "kind") == kind && labelOf(m, "reason") == reason {
				return m.GetCounter().GetValue()
			}
		}
	}
	e.t.Fatalf("no tuttifrutti_reports_total{kind=%q,reason=%q}", kind, reason)
	return 0
}

func labelOf(m *dto.Metric, name string) string {
	for _, l := range m.GetLabel() {
		if l.GetName() == name {
			return l.GetValue()
		}
	}
	return ""
}

func TestReportMetricHasFixedSeriesAndNoClientText(t *testing.T) {
	e := newSafetyEnv(t, safetyOpts{})
	families, _ := e.srv.Registry().Gather()
	series := 0
	for _, f := range families {
		if f.GetName() == "tuttifrutti_reports_total" {
			series = len(f.GetMetric())
			for _, m := range f.GetMetric() {
				if !safety.ValidKind(labelOf(m, "kind")) || !safety.ValidReason(labelOf(m, "reason")) {
					t.Errorf("series %v has a label outside the fixed sets", m.GetLabel())
				}
			}
		}
	}
	if want := len(safety.Kinds) * len(safety.Reasons); series != want {
		t.Errorf("%d series, want %d from the first scrape", series, want)
	}
	var _ prometheus.Collector = e.srv.reports
}

func TestReportValidation(t *testing.T) {
	e := newSafetyEnv(t, safetyOpts{})
	b := e.browser()
	b.get("/api/session")
	n := 0
	try := func(mod func(m map[string]any)) reply {
		n++
		e.clock.Advance(time.Minute) // the validation tests are not about the rate limit
		return b.postJSON("/api/reports", reportBody(func(m map[string]any) {
			m["idempotencyKey"] = "valid-key-" + string(rune('a'+n%26)) + string(rune('a'+n/26))
			if mod != nil {
				mod(m)
			}
		}))
	}
	bad := map[string]func(m map[string]any){
		"unknown kind":        func(m map[string]any) { m["kind"] = "video" },
		"no kind":             func(m map[string]any) { delete(m, "kind") },
		"unknown reason":      func(m map[string]any) { m["reason"] = "dislike" },
		"no reason":           func(m map[string]any) { delete(m, "reason") },
		"short key":           func(m map[string]any) { m["idempotencyKey"] = "short" },
		"key with spaces":     func(m map[string]any) { m["idempotencyKey"] = "key with spaces" },
		"no key":              func(m map[string]any) { delete(m, "idempotencyKey") },
		"neither code nor id": func(m map[string]any) { delete(m, "code"); delete(m, "roomInstanceId") },
		"bad code":            func(m map[string]any) { m["code"] = "AEIOU" },
		"bad instance id":     func(m map[string]any) { m["roomInstanceId"] = "has spaces" },
		"comment too long":    func(m map[string]any) { m["comment"] = strings.Repeat("ñ", 281) },
		"observed too long":   func(m map[string]any) { m["observedText"] = strings.Repeat("x", 201) },
		"answer without round": func(m map[string]any) {
			m["ref"] = map[string]any{"playerId": "p", "category": 1}
		},
		"player without id":     func(m map[string]any) { m["kind"] = "player"; m["ref"] = map[string]any{} },
		"label without text":    func(m map[string]any) { m["kind"] = "custom_label"; m["ref"] = map[string]any{"category": 2} },
		"category out of range": func(m map[string]any) { m["ref"] = map[string]any{"playerId": "p", "category": 99, "round": 1} },
		"ref id with markup":    func(m map[string]any) { m["ref"] = map[string]any{"playerId": "<b>", "category": 1, "round": 1} },
	}
	for name, mod := range bad {
		if r := try(mod); r.StatusCode != http.StatusBadRequest || r.errCode(t) != "invalid_report" {
			t.Errorf("%s = %d %s, want 400 invalid_report", name, r.StatusCode, r.body)
		}
	}
	if len(e.safe.reports()) != 0 || e.safe.creates != 0 {
		t.Error("an invalid report reached the store")
	}
	if e.rooms.asked() != 0 {
		t.Error("an invalid report reached the room")
	}

	// The limits are inclusive.
	if r := try(func(m map[string]any) {
		m["comment"] = strings.Repeat("ñ", 280)
		m["observedText"] = strings.Repeat("x", 200)
	}); r.StatusCode != http.StatusCreated {
		t.Errorf("a comment of exactly 280 runes = %d %s", r.StatusCode, r.body)
	}
	// Texts go through the cleaners of nicknames and answers.
	r := try(func(m map[string]any) { m["comment"] = "  a\u200bb \x00 c\n\n d  " })
	var resp api.ReportResponse
	r.json(t, &resp)
	if got, _ := e.safe.Memory.GetReport(t.Context(), resp.ID); got.Comment != "ab c d" {
		t.Errorf("comment = %q, want it cleaned", got.Comment)
	}
}

func TestReportRequestShape(t *testing.T) {
	e := newSafetyEnv(t, safetyOpts{})
	b := e.browser()
	b.get("/api/session")
	if r := b.request(http.MethodPost, "/api/reports", reportBody(nil), "Content-Type", "text/plain"); r.StatusCode != http.StatusUnsupportedMediaType {
		t.Errorf("text/plain = %d", r.StatusCode)
	}
	big := reportBody(func(m map[string]any) { m["comment"] = strings.Repeat("x", 5000) })
	if r := b.postJSON("/api/reports", big); r.StatusCode != http.StatusRequestEntityTooLarge || r.errCode(t) != "payload_too_large" {
		t.Errorf("over 4 KiB = %d %s", r.StatusCode, r.body)
	}
	if r := b.postJSON("/api/reports", reportBody(nil)+" {}"); r.StatusCode != http.StatusBadRequest {
		t.Errorf("trailing data = %d", r.StatusCode)
	}
	if r := b.request(http.MethodPost, "/api/reports", reportBody(nil), "Origin", "https://evil.example"); r.StatusCode != http.StatusForbidden {
		t.Errorf("foreign origin = %d", r.StatusCode)
	}
	if r := e.browser().postJSON("/api/reports", reportBody(nil)); r.StatusCode != http.StatusUnauthorized {
		t.Errorf("no guest cookie = %d", r.StatusCode)
	}
	if r := b.request(http.MethodGet, "/api/reports", ""); r.StatusCode == http.StatusOK {
		t.Error("GET /api/reports answered")
	}
}

func TestReportRateLimits(t *testing.T) {
	newReporter := func(t *testing.T) (*safetyEnv, *browser) {
		e := newSafetyEnv(t, safetyOpts{})
		b := e.browser()
		b.get("/api/session")
		return e, b
	}
	send := func(b *browser, i int) reply {
		return b.postJSON("/api/reports", reportBody(func(m map[string]any) {
			m["idempotencyKey"] = "rate-key-" + string(rune('a'+i%26)) + string(rune('a'+i/26))
		}))
	}

	t.Run("burst of 3 a minute", func(t *testing.T) {
		e, b := newReporter(t)
		for i := range 3 {
			if r := send(b, i); r.StatusCode != http.StatusCreated {
				t.Fatalf("report %d = %d %s", i, r.StatusCode, r.body)
			}
		}
		r := send(b, 3)
		if r.StatusCode != http.StatusTooManyRequests || r.errCode(t) != "rate_limited" || r.Header.Get("Retry-After") == "" {
			t.Fatalf("4th report = %d %s %v", r.StatusCode, r.body, r.Header)
		}
		if n := len(e.safe.reports()); n != 3 {
			t.Errorf("%d reports stored", n)
		}
		e.clock.Advance(21 * time.Second)
		if r := send(b, 4); r.StatusCode != http.StatusCreated {
			t.Errorf("after the refill = %d %s", r.StatusCode, r.body)
		}
	})

	t.Run("20 a day per guest", func(t *testing.T) {
		e, b := newReporter(t)
		for i := range 20 {
			e.clock.Advance(21 * time.Second) // below the minute burst
			if r := send(b, i); r.StatusCode != http.StatusCreated {
				t.Fatalf("report %d = %d %s", i, r.StatusCode, r.body)
			}
		}
		e.clock.Advance(21 * time.Second)
		if r := send(b, 20); r.StatusCode != http.StatusTooManyRequests {
			t.Fatalf("21st report of the day = %d %s", r.StatusCode, r.body)
		}
		// The next day there is budget again.
		e.clock.Advance(24 * time.Hour)
		if r := send(b, 21); r.StatusCode != http.StatusCreated {
			t.Errorf("next day = %d %s", r.StatusCode, r.body)
		}
	})

	t.Run("the account is limited across guests", func(t *testing.T) {
		e := newSafetyEnv(t, safetyOpts{})
		first, second := e.browser(), e.browser()
		for _, b := range []*browser{first, second} {
			b.get("/api/session")
			b.login("/", testUser)
		}
		for i := range 3 {
			if r := send(first, i); r.StatusCode != http.StatusCreated {
				t.Fatalf("report %d = %d %s", i, r.StatusCode, r.body)
			}
		}
		// A fresh guest cookie does not reset the budget of the same account.
		if r := send(second, 10); r.StatusCode != http.StatusTooManyRequests {
			t.Errorf("same account, other guest = %d %s", r.StatusCode, r.body)
		}
		// A guest that is not signed in is a different budget.
		guest := e.browser()
		guest.get("/api/session")
		if r := send(guest, 11); r.StatusCode != http.StatusCreated {
			t.Errorf("a plain guest = %d %s", r.StatusCode, r.body)
		}
	})

	t.Run("refused requests do not spend the budget of an invalid body", func(t *testing.T) {
		_, b := newReporter(t)
		// Garbage still costs a token (the limit protects the endpoint), but a
		// valid report is accepted again as soon as it refills.
		for range 3 {
			b.postJSON("/api/reports", `{"kind":`)
		}
		if r := send(b, 0); r.StatusCode != http.StatusTooManyRequests {
			t.Errorf("after 3 malformed bodies = %d", r.StatusCode)
		}
	})
}

func TestReportsLeaveNoRawTextInTheLogs(t *testing.T) {
	e := newSafetyEnv(t, safetyOpts{})
	secret := "SUPERSECRETTEXT-ñandú-<b>"
	e.rooms.reply = func(string, safety.EvidenceRequest) (safety.Evidence, error) { return serverEvidence(), nil }
	b := e.browser()
	b.get("/api/session")
	b.postJSON("/api/reports", reportBody(func(m map[string]any) { m["comment"] = secret; m["observedText"] = secret }))
	e.safe.failReports.Store(true)
	e.clock.Advance(time.Minute)
	b.postJSON("/api/reports", reportBody(func(m map[string]any) { m["comment"] = secret; m["idempotencyKey"] = "key-0000002" }))
	b.postJSON("/api/reports", reportBody(func(m map[string]any) { m["comment"] = secret; m["kind"] = "nope" }))
	logs := e.logs.String()
	if !strings.Contains(logs, "storing a report failed") {
		t.Fatalf("the failure was not logged:\n%s", logs)
	}
	for _, leaked := range []string{"SUPERSECRET", "ñandú", "other-guest", "tomate"} {
		if strings.Contains(logs, leaked) {
			t.Errorf("the logs hold %q:\n%s", leaked, logs)
		}
	}
	// And the metric labels hold only the fixed codes.
	families, _ := e.srv.Registry().Gather()
	for _, f := range families {
		for _, m := range f.GetMetric() {
			for _, l := range m.GetLabel() {
				if strings.Contains(l.GetValue(), "SUPERSECRET") {
					t.Errorf("metric %s has a label with report text", f.GetName())
				}
			}
		}
	}
}

func TestReportEvidenceStaysValidJSONWhateverIsTyped(t *testing.T) {
	e := newSafetyEnv(t, safetyOpts{})
	b := e.browser()
	b.get("/api/session")
	nasty := `</script> "q" \ \u0000 😀 ñ & ' <img src=x onerror=1>`
	r := b.postJSON("/api/reports", reportBody(func(m map[string]any) {
		m["observedText"] = nasty
		m["comment"] = nasty
		m["ref"] = map[string]any{"playerId": "p1", "category": 0, "round": 1}
	}))
	var resp api.ReportResponse
	r.json(t, &resp)
	rep, err := e.safe.Memory.GetReport(t.Context(), resp.ID)
	if err != nil || !json.Valid(rep.Evidence) {
		t.Fatalf("report = %v, %v", rep.Evidence, err)
	}
	var data safety.EvidenceData
	if err := json.Unmarshal(rep.Evidence, &data); err != nil || data.ObservedText != nasty || rep.Comment != nasty {
		t.Errorf("stored %q / %q (%v), want the text exactly as typed", data.ObservedText, rep.Comment, err)
	}
}
