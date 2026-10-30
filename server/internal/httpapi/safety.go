package httpapi

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"golang.org/x/time/rate"

	"github.com/AresiusXP/tutti-frutti/server/internal/api"
	"github.com/AresiusXP/tutti-frutti/server/internal/game"
	"github.com/AresiusXP/tutti-frutti/server/internal/ratelimit"
	"github.com/AresiusXP/tutti-frutti/server/internal/room"
	"github.com/AresiusXP/tutti-frutti/server/internal/safety"
	"github.com/AresiusXP/tutti-frutti/server/internal/store"
)

// Limits of the safety endpoints (docs/DESIGN.md, "Safety").
const (
	// Reports: at most 20 a day per guest and per account, 3 a minute in a
	// burst. Both are token buckets (the day bucket refills 20 tokens a day).
	reportBurstPerMinute = 3
	reportsPerDay        = 20
	// The per-IP bucket of POST /api/reports (like room creation it is a global
	// bucket behind the homelab's Traefik): it caps what cookie churn can write.
	reportIPRate  = rate.Limit(30.0 / 60)
	reportIPBurst = 10
	// Accepting the terms writes a row per guest: 120 a minute in bursts of 40
	// lets a launch crowd through while capping a flood.
	termsRate  = rate.Limit(120.0 / 60)
	termsBurst = 40

	// safetyTimeout bounds one store or room call of the safety endpoints.
	safetyTimeout = 5 * time.Second
)

// newReportMetric counts the reports that were stored, by kind and reason (both
// from fixed sets: never client text).
func newReportMetric() *prometheus.CounterVec {
	v := prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "tuttifrutti_reports_total",
		Help: "Content reports stored, by kind and reason.",
	}, []string{"kind", "reason"})
	for _, k := range safety.Kinds {
		for _, r := range safety.Reasons {
			v.WithLabelValues(k, r)
		}
	}
	return v
}

func (s *Server) registerSafety() {
	if s.guests == nil {
		return
	}
	if s.terms != nil {
		s.mux.Handle("POST /api/terms/accept",
			ratelimit.Middleware(s.termsLimit, s.limitKey)(http.HandlerFunc(s.handleAcceptTerms)))
	}
	if s.safety != nil && s.rooms != nil {
		s.mux.Handle("POST /api/reports",
			ratelimit.Middleware(s.reportIPLimit, s.limitKey)(http.HandlerFunc(s.handleReport)))
	}
	s.registerBlocks()
}

// retryable answers a failure of a dependency the client may simply retry:
// 503 server_busy (a code every client already treats as "try again").
func retryable(w http.ResponseWriter) {
	w.Header().Set("Retry-After", "2")
	writeError(w, http.StatusServiceUnavailable, "server_busy")
}

// guestOf returns the guest id of the request, minting the cookie when there is
// none, and whether it was minted by this call (a fresh guest cannot have
// accepted anything).
func (s *Server) guestOf(w http.ResponseWriter, r *http.Request) (id string, fresh bool) {
	if id, ok := s.guests.Identify(r); ok {
		return id, false
	}
	return s.guests.Issue(w), true
}

// termsStatus is the TermsStatus of a guest. An error means the store could not
// tell (never "not accepted"). Without a gate acceptance is never required.
func (s *Server) termsStatus(ctx context.Context, guestID string, fresh bool) (api.TermsStatus, error) {
	if s.terms == nil {
		return api.TermsStatus{RequiredVersion: safety.TermsVersion}, nil
	}
	st := api.TermsStatus{RequiredVersion: s.terms.Version(), AcceptanceRequired: true}
	if fresh {
		return st, nil
	}
	ctx, cancel := context.WithTimeout(ctx, safetyTimeout)
	defer cancel()
	ok, err := s.terms.Accepted(ctx, guestID)
	if err != nil {
		return api.TermsStatus{}, err
	}
	if ok {
		version := s.terms.Version()
		st.AcceptedVersion, st.AcceptanceRequired = &version, false
	}
	return st, nil
}

// requireTerms enforces the terms on an endpoint that stores something a player
// wrote. It answers 403 terms_required when the guest has not accepted (or
// there is no guest identity), and a retryable 503 when it cannot tell: an
// unknown acceptance fails closed. Without a gate it allows everything.
func (s *Server) requireTerms(w http.ResponseWriter, r *http.Request, guestID string, fresh bool) bool {
	if s.terms == nil {
		return true
	}
	st, err := s.termsStatus(r.Context(), guestID, fresh)
	switch {
	case err != nil:
		s.log.Warn("the terms gate could not tell; refusing (retryable)", "err", err, "path", r.URL.Path)
		retryable(w)
		return false
	case st.AcceptanceRequired:
		writeError(w, http.StatusForbidden, api.ErrCodeTermsRequired)
		return false
	}
	return true
}

// requireTermsOfRequest is requireTerms for an endpoint that does not mint
// the guest cookie (an account request): without a valid guest cookie there is
// no one who could have accepted.
func (s *Server) requireTermsOfRequest(w http.ResponseWriter, r *http.Request) bool {
	if s.terms == nil || s.guests == nil {
		return true
	}
	id, ok := s.guests.Identify(r)
	return s.requireTerms(w, r, id, !ok)
}

func (s *Server) handleAcceptTerms(w http.ResponseWriter, r *http.Request) {
	guestID, ok := s.guests.Identify(r)
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	if !isJSON(r) {
		writeError(w, http.StatusUnsupportedMediaType, "unsupported_media_type")
		return
	}
	var req api.AcceptTermsRequest
	if !decodeOrFail(w, r, &req) {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), safetyTimeout)
	defer cancel()
	a, err := s.terms.Accept(ctx, guestID, safety.AcceptInput{Version: req.Version, Locale: req.Locale, Age13Confirmed: req.Age13Confirmed})
	switch {
	case errors.Is(err, safety.ErrVersionMismatch):
		writeError(w, http.StatusConflict, api.ErrCodeTermsVersionMismatch)
	case errors.Is(err, safety.ErrInvalidLocale):
		writeError(w, http.StatusBadRequest, api.ErrCodeInvalidLocale)
	case errors.Is(err, safety.ErrAgeNotConfirmed):
		writeError(w, http.StatusBadRequest, api.ErrCodeAgeConfirmationRequired)
	case err != nil:
		s.log.Warn("recording the terms acceptance failed", "err", err)
		retryable(w)
	default:
		version := a.Version
		writeJSON(w, http.StatusOK, api.TermsStatus{RequiredVersion: s.terms.Version(), AcceptedVersion: &version})
	}
}

// decodeOrFail reads a JSON body into v; on failure it has already answered
// (413 or 400) and reports false.
func decodeOrFail(w http.ResponseWriter, r *http.Request, v any) bool {
	if err := decodeBody(w, r, v); err != nil {
		var tooBig *http.MaxBytesError
		if errors.As(err, &tooBig) {
			writeError(w, http.StatusRequestEntityTooLarge, "payload_too_large")
			return false
		}
		writeError(w, http.StatusBadRequest, "bad_request")
		return false
	}
	return true
}

type bucket struct {
	l   *ratelimit.Keyed
	key string
}

// allowReport charges one report to the buckets of the guest and, when signed
// in, of the account: a minute burst and a day budget each. The first bucket
// that is empty refuses it.
func (s *Server) allowReport(guestID, userID string) (bool, time.Duration) {
	buckets := []bucket{{s.reportBurst, "g:" + guestID}, {s.reportDaily, "g:" + guestID}}
	if userID != "" {
		buckets = append(buckets, bucket{s.reportBurst, "u:" + userID}, bucket{s.reportDaily, "u:" + userID})
	}
	for _, b := range buckets {
		if ok, retry := b.l.Allow(b.key); !ok {
			return false, retry
		}
	}
	return true, 0
}

// reportInput is a validated, cleaned report request.
type reportInput struct {
	kind, reason, comment, key, code, instance, observed string
	ref                                                  safety.Ref
}

// parseReport validates a request and cleans its texts. The comment, the
// observed text and the label go through the same cleaners as nicknames and
// answers (invisible and control characters removed, whitespace collapsed).
func parseReport(req api.ReportRequest) (reportInput, bool) {
	in := reportInput{
		kind: req.Kind, reason: req.Reason, key: req.IdempotencyKey,
		comment:  game.CleanText(req.Comment),
		observed: game.CleanText(req.ObservedText),
		instance: req.RoomInstanceID,
		ref:      safety.Ref(req.Ref),
	}
	in.ref.Label = game.CleanText(in.ref.Label)
	if req.Code != "" {
		code, ok := normalizeRoomCode(req.Code)
		if !ok {
			return in, false
		}
		in.code = code
	}
	switch {
	case !safety.ValidKind(in.kind), !safety.ValidReason(in.reason):
	case !safety.ValidToken(in.key) || len(in.key) < safety.MinKeyLen:
	case in.instance != "" && !safety.ValidToken(in.instance):
	case in.instance == "" && in.code == "":
	case runeLen(in.comment) > safety.MaxCommentRunes, runeLen(in.observed) > safety.MaxObservedRunes:
	case in.ref.Validate(in.kind) != nil:
	default:
		return in, true
	}
	return in, false
}

func runeLen(s string) int { return len([]rune(s)) }

// Refusals of resolveEvidence.
var (
	errNotInRoom = errors.New("httpapi: the reporter is not in the room")
	errTryAgain  = errors.New("httpapi: the room cannot answer now")
)

func (s *Server) handleReport(w http.ResponseWriter, r *http.Request) {
	guestID, ok := s.guests.Identify(r)
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	if !isJSON(r) {
		writeError(w, http.StatusUnsupportedMediaType, "unsupported_media_type")
		return
	}
	userID := ""
	if s.sessions != nil {
		userID, _ = s.sessions.Identify(r)
	}
	if ok, retry := s.allowReport(guestID, userID); !ok {
		ratelimit.Reject(w, retry)
		return
	}
	var req api.ReportRequest
	if !decodeOrFail(w, r, &req) {
		return
	}
	in, ok := parseReport(req)
	if !ok {
		writeError(w, http.StatusBadRequest, api.ErrCodeInvalidReport)
		return
	}

	ev, verified, err := s.resolveEvidence(r.Context(), guestID, in)
	switch {
	case errors.Is(err, errNotInRoom):
		writeError(w, http.StatusForbidden, api.ErrCodeNotInRoom)
		return
	case err != nil:
		retryable(w)
		return
	}
	raw, err := ev.JSON()
	if err != nil {
		s.log.Error("encoding report evidence failed", "err", err)
		writeError(w, http.StatusInternalServerError, "internal")
		return
	}
	now := s.now().UTC()
	rep := safety.Report{
		ID:               store.NewID(),
		ReporterGuest:    guestID,
		ReporterUser:     userID,
		RoomInstance:     ev.RoomInstance,
		MatchInstance:    ev.MatchInstance,
		ContentKey:       ev.ContentKey,
		Kind:             in.kind,
		TargetGuest:      ev.TargetGuest,
		TargetUser:       ev.TargetUser,
		Evidence:         raw,
		EvidenceVerified: verified,
		Reason:           in.reason,
		Comment:          in.comment,
		Status:           safety.StatusOpen,
		IdempotencyKey:   in.key,
		CreatedAt:        now,
		PurgeAt:          safety.PurgeAt(now, time.Time{}),
	}
	ctx, cancel := context.WithTimeout(r.Context(), safetyTimeout)
	defer cancel()
	id, created, err := s.safety.CreateReport(ctx, rep)
	switch {
	case errors.Is(err, store.ErrInvalidReport):
		// Everything was validated above: this is a bug, not the client's fault.
		s.log.Error("the store refused a report we validated", "err", err)
		writeError(w, http.StatusInternalServerError, "internal")
		return
	case err != nil:
		// Nothing was stored (or we cannot tell): the client retries with the
		// same idempotency key and ends up with one report either way.
		s.log.Warn("storing a report failed", "err", err)
		retryable(w)
		return
	}
	status := http.StatusOK
	if created {
		s.reports.WithLabelValues(in.kind, in.reason).Inc()
		if s.reportsSeen != nil {
			s.reportsSeen.ReportCreated(rep.CreatedAt)
		}
		status = http.StatusCreated
	}
	writeJSON(w, status, api.ReportResponse{ID: id})
}

// resolveEvidence asks the room for the evidence of a report. It returns the
// evidence and whether it is verified. errNotInRoom means the room exists and
// the reporter is not one of its players; errTryAgain means the room could not
// answer now (draining, timeout). When the room instance is gone, the content
// changed or was never visible to the reporter, the report is still taken, with
// the client's text stored as uncorroborated evidence that never names a target.
func (s *Server) resolveEvidence(ctx context.Context, guestID string, in reportInput) (safety.Evidence, bool, error) {
	if src, ok := s.rooms.(room.EvidenceSource); ok && in.code != "" {
		ctx, cancel := context.WithTimeout(ctx, safetyTimeout)
		defer cancel()
		ev, err := src.ReportEvidence(ctx, in.code, safety.EvidenceRequest{
			Reporter: guestID, Kind: in.kind, RoomInstanceID: in.instance, Ref: in.ref,
		})
		switch {
		case err == nil:
			return ev, true, nil
		case errors.Is(err, room.ErrNotMember):
			return safety.Evidence{}, false, errNotInRoom
		case errors.Is(err, room.ErrShuttingDown):
			return safety.Evidence{}, false, errTryAgain
		case errors.Is(err, room.ErrRoomNotFound), errors.Is(err, room.ErrRoomClosed),
			errors.Is(err, room.ErrInstanceMismatch), errors.Is(err, room.ErrContentUnavailable):
			// taken as unverified below
		default:
			s.log.Warn("resolving report evidence failed", "err", err)
			return safety.Evidence{}, false, errTryAgain
		}
	}
	claimed := in.ref
	return safety.Evidence{
		RoomInstance: in.instance,
		ContentKey:   safety.ContentKey(in.kind, in.ref),
		Data: safety.EvidenceData{
			Source:       safety.SourceClient,
			Claimed:      &claimed,
			ObservedText: in.observed,
		},
	}, false, nil
}
