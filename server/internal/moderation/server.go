package moderation

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/AresiusXP/tutti-frutti/server/internal/game"
	"github.com/AresiusXP/tutti-frutti/server/internal/room"
	"github.com/AresiusXP/tutti-frutti/server/internal/safety"
	"github.com/AresiusXP/tutti-frutti/server/internal/store"
)

// RoomControl is what the control server needs from the room hub; *room.Hub
// implements it. Both calls run through the rooms' own goroutines.
type RoomControl interface {
	// Sweep makes every live room apply the policy (see room.Hub.Sweep).
	Sweep(ctx context.Context) (map[string][]string, error)
	// CloseRoom closes exactly one room instance.
	CloseRoom(ctx context.Context, t room.CloseTarget) (room.CloseResult, error)
}

// JudgeControl is the AI judge's kill switch; *judge.Switch implements it.
type JudgeControl interface {
	Enabled() bool
	SetEnabled(on bool)
}

// PublicControl is the switch of public games; *room.Hub implements it.
type PublicControl interface {
	PublicEnabled() bool
	// SetPublicEnabled changes nothing and returns ctx's error when ctx is
	// already done. Otherwise it writes the switch (in force at once) and
	// returns once the live public rooms re-evaluated their automated
	// opponents, or room.ErrPublicRoomsPending when ctx ended first.
	SetPublicEnabled(ctx context.Context, on bool) error
}

// Deps are the collaborators of the control Server.
type Deps struct {
	Reports store.Safety
	Mod     store.Moderation
	Policy  *safety.Policy
	Rooms   RoomControl
	// Judge is nil when the server has no AI judge.
	Judge JudgeControl
	// Public is the switch of public games (cmd/server: the room hub); nil
	// refuses the public command.
	Public  PublicControl
	Gauges  *ReportGauges
	Now     func() time.Time
	Log     *slog.Logger
	Version string
}

// Server is the control server: an http.Handler over a Unix socket.
type Server struct {
	d       Deps
	mux     *http.ServeMux
	handler http.Handler
}

// timeout bounds one command (a sweep may touch every live room).
const timeout = 20 * time.Second

// auditTimeout bounds the audit record written right after an action.
const auditTimeout = 5 * time.Second

// detach returns a context for the work that must follow an action the server
// already took (its audit record): it keeps the request's values but not its
// cancellation, so a client that disconnects after the action cannot leave it
// unaudited, and it is bounded by d.
func detach(ctx context.Context, d time.Duration) (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.WithoutCancel(ctx), d)
}

// notStarted refuses a command whose request already ended (the client gave
// up or the deadline passed) before it changes anything.
func notStarted(ctx context.Context) error {
	if ctx.Err() != nil {
		return fail(http.StatusServiceUnavailable, CodeUnavailable, "the request ended before anything changed")
	}
	return nil
}

// NewServer builds the control server.
func NewServer(d Deps) *Server {
	if d.Now == nil {
		d.Now = time.Now
	}
	if d.Log == nil {
		d.Log = slog.Default()
	}
	s := &Server{d: d, mux: http.NewServeMux()}
	route := func(path string, h func(*http.Request, []byte) (any, error)) {
		s.mux.HandleFunc(path, s.command(path, h))
	}
	route(PathStatus, s.status)
	route(PathReportsList, s.reportsList)
	route(PathReportsShow, s.reportsShow)
	route(PathReportsResolve, s.reportsResolve)
	route(PathRestrict, s.restrict)
	route(PathUnrestrict, s.unrestrict)
	route(PathRoomClose, s.roomClose)
	route(PathProfileRemove, s.profileRemove)
	route(PathJudge, s.judge)
	route(PathPublic, s.public)
	s.mux.HandleFunc("/", func(w http.ResponseWriter, _ *http.Request) {
		writeErr(w, http.StatusNotFound, CodeUnknownCommand, "unknown command")
	})
	return s
}

// Handler returns the HTTP handler (tests drive it directly).
func (s *Server) Handler() http.Handler { return s.mux }

// Serve answers requests on l until ctx is done, then shuts down gracefully.
func (s *Server) Serve(ctx context.Context, l net.Listener) error {
	srv := &http.Server{
		Handler:           s.mux,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      timeout + 5*time.Second,
		MaxHeaderBytes:    8 << 10,
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		<-ctx.Done()
		sctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(sctx)
	}()
	err := srv.Serve(l)
	<-done
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}

// errCmd is an error that knows its HTTP status and protocol code.
type errCmd struct {
	status  int
	code    string
	message string
}

func (e *errCmd) Error() string { return e.code + ": " + e.message }

func fail(status int, code, format string, args ...any) error {
	return &errCmd{status, code, fmt.Sprintf(format, args...)}
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, status int, code, message string) {
	writeJSON(w, status, ErrorResponse{Error: code, Message: message})
}

// command wraps a handler: POST only, a size-limited JSON body, a deadline, and
// uniform errors. Nothing a request carries is logged except the command path,
// the status and the operator's name.
func (s *Server) command(path string, h func(*http.Request, []byte) (any, error)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.Header().Set("Allow", http.MethodPost)
			writeErr(w, http.StatusMethodNotAllowed, CodeMethodNotAllowed, "use POST")
			return
		}
		body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, MaxRequestBytes))
		if err != nil {
			var tooBig *http.MaxBytesError
			if errors.As(err, &tooBig) {
				writeErr(w, http.StatusRequestEntityTooLarge, CodeTooLarge, fmt.Sprintf("a request is at most %d bytes", MaxRequestBytes))
				return
			}
			writeErr(w, http.StatusBadRequest, CodeBadRequest, "could not read the request")
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), timeout)
		defer cancel()
		out, err := h(r.WithContext(ctx), body)
		if err != nil {
			var ec *errCmd
			if errors.As(err, &ec) {
				s.d.Log.Info("moderation command refused", "command", path, "code", ec.code)
				writeErr(w, ec.status, ec.code, ec.message)
				return
			}
			s.d.Log.Error("moderation command failed", "command", path, "err", err)
			writeErr(w, http.StatusInternalServerError, CodeInternal, "the command failed; see the server log")
			return
		}
		writeJSON(w, http.StatusOK, out)
	}
}

// decode reads body into v, refusing unknown fields and anything after the
// value except whitespace: a second Decode must hit the end of the input, which
// is stricter than Decoder.More (it ignores a stray closing } or ]).
func decode(body []byte, v any) error {
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		return fail(http.StatusBadRequest, CodeBadRequest, "malformed request: %v", err)
	}
	var extra json.RawMessage
	if err := dec.Decode(&extra); !errors.Is(err, io.EOF) {
		return fail(http.StatusBadRequest, CodeBadRequest, "unexpected data after the request")
	}
	return nil
}

// operator validates the declared operator name. The name is what the person
// at the keyboard says: the access boundary is the right to exec into the pod.
func operator(name string) (string, error) {
	if !safety.ValidOperator(name) {
		return "", fail(http.StatusBadRequest, CodeOperatorRequired,
			"--operator NAME is required for every action that changes something (1 to %d letters, digits, . _ - @)", safety.MaxIDLen)
	}
	return name, nil
}

// text cleans operator-typed text and checks its length.
func text(field, s string, max int, required bool) (string, error) {
	s = game.CleanText(s)
	switch {
	case required && s == "":
		return "", fail(http.StatusBadRequest, CodeBadRequest, "%s is required", field)
	case utf8.RuneCountInString(s) > max:
		return "", fail(http.StatusBadRequest, CodeBadRequest, "%s is longer than %d characters", field, max)
	}
	return s, nil
}

func storeErr(err error) error {
	switch {
	case errors.Is(err, store.ErrReportNotFound):
		return fail(http.StatusNotFound, CodeNotFound, "no such report (never filed, or purged)")
	case errors.Is(err, store.ErrReportNotOpen):
		return fail(http.StatusConflict, CodeAlreadyResolved, "the report is already resolved (another resolution came first and stands)")
	case errors.Is(err, store.ErrAlreadyRestricted):
		return fail(http.StatusConflict, CodeAlreadyRestricted, "the guest is already restricted; unrestrict it first to change it")
	case errors.Is(err, store.ErrRestrictionNotFound):
		return fail(http.StatusNotFound, CodeNotFound, "no such restriction")
	case errors.Is(err, store.ErrUserNotFound):
		return fail(http.StatusNotFound, CodeNotFound, "no such account")
	case errors.Is(err, store.ErrInvalidModeration), errors.Is(err, store.ErrInvalidReport):
		return fail(http.StatusBadRequest, CodeBadRequest, "refused by the store: %v", err)
	}
	return err // an internal failure: the database
}

func (s *Server) audit(operator, action, kind, target, reportID, details string) safety.Action {
	return safety.Action{
		ID: store.NewID(), At: s.d.Now().UTC(), Operator: operator, Action: action,
		TargetKind: kind, TargetID: target, ReportID: reportID, Details: details,
	}
}

func (s *Server) refreshGauges(ctx context.Context) {
	if s.d.Gauges == nil {
		return
	}
	if _, err := s.d.Gauges.Refresh(ctx, s.d.Reports); err != nil {
		s.d.Log.Warn("refreshing the report gauges failed", "err", err)
	}
}

func (s *Server) judgeStatus() JudgeStatus {
	if s.d.Judge == nil {
		return JudgeStatus{}
	}
	return JudgeStatus{Configured: true, Enabled: s.d.Judge.Enabled()}
}

func (s *Server) publicStatus() PublicStatus {
	if s.d.Public == nil {
		return PublicStatus{}
	}
	return PublicStatus{Configured: true, Enabled: s.d.Public.PublicEnabled()}
}

// ---- commands ----

func (s *Server) status(r *http.Request, body []byte) (any, error) {
	if err := decode(body, &struct{}{}); err != nil {
		return nil, err
	}
	st, err := s.d.Reports.ReportStats(r.Context())
	if err != nil {
		return nil, err
	}
	if s.d.Gauges != nil {
		s.d.Gauges.Set(st)
	}
	blocks, restrictions := s.d.Policy.Counts()
	out := StatusResponse{
		Version: s.d.Version, OpenReports: st.Open, OldestOpenAt: st.OldestOpen,
		ActiveRestrictions: restrictions, Blocks: blocks, Judge: s.judgeStatus(), Public: s.publicStatus(),
	}
	if st.Open > 0 && !st.OldestOpen.IsZero() {
		out.OldestOpenAgeSeconds = max(int64(s.d.Now().Sub(st.OldestOpen).Seconds()), 0)
	}
	return out, nil
}

func (s *Server) reportsList(r *http.Request, body []byte) (any, error) {
	var req ReportsListRequest
	if err := decode(body, &req); err != nil {
		return nil, err
	}
	filter := req.Status
	switch filter {
	case "":
		filter = safety.StatusOpen
	case safety.StatusOpen, store.ReportsResolved, store.ReportsAll:
	default:
		return nil, fail(http.StatusBadRequest, CodeBadRequest, "--status is open, resolved or all")
	}
	if req.Limit < 0 || req.Limit > 100 {
		return nil, fail(http.StatusBadRequest, CodeBadRequest, "--limit is 1 to 100")
	}
	reports, err := s.d.Reports.ListReports(r.Context(), filter, req.Limit)
	if err != nil {
		return nil, storeErr(err)
	}
	out := ReportsListResponse{Reports: make([]ReportSummary, 0, len(reports))}
	for _, rep := range reports {
		out.Reports = append(out.Reports, ReportSummary{
			ID: rep.ID, CreatedAt: rep.CreatedAt, Kind: rep.Kind, Reason: rep.Reason, Status: rep.Status,
			EvidenceVerified: rep.EvidenceVerified, ContentKey: rep.ContentKey,
			HasTarget: rep.TargetGuest != "", Comment: rep.Comment,
		})
	}
	return out, nil
}

func (s *Server) reportsShow(r *http.Request, body []byte) (any, error) {
	var req ReportRef
	if err := decode(body, &req); err != nil {
		return nil, err
	}
	rep, err := s.d.Reports.GetReport(r.Context(), req.ID)
	if err != nil {
		return nil, storeErr(err)
	}
	return ReportView{
		ID: rep.ID, Status: rep.Status, Kind: rep.Kind, Reason: rep.Reason, CreatedAt: rep.CreatedAt,
		ResolvedAt: rep.ResolvedAt, PurgeAt: rep.PurgeAt, EvidenceVerified: rep.EvidenceVerified,
		ContentKey: rep.ContentKey, RoomInstance: rep.RoomInstance, MatchInstance: rep.MatchInstance,
		ReporterGuest: rep.ReporterGuest, ReporterUser: rep.ReporterUser,
		TargetGuest: rep.TargetGuest, TargetUser: rep.TargetUser, Comment: rep.Comment, Evidence: rep.Evidence,
	}, nil
}

func (s *Server) reportsResolve(r *http.Request, body []byte) (any, error) {
	var req ResolveRequest
	if err := decode(body, &req); err != nil {
		return nil, err
	}
	op, err := operator(req.Operator)
	if err != nil {
		return nil, err
	}
	note, err := text("--note", req.Note, safety.MaxDetailsRunes-30, true)
	if err != nil {
		return nil, err
	}
	ctx := r.Context()
	rep, err := s.d.Reports.GetReport(ctx, req.ID)
	if err != nil {
		return nil, storeErr(err)
	}
	if rep.Status != safety.StatusOpen {
		return nil, fail(http.StatusConflict, CodeAlreadyResolved, "the report is already %s", rep.Status)
	}
	status := safety.StatusActioned
	if req.Dismiss {
		status = safety.StatusDismissed
	}
	audit := s.audit(op, safety.ActionReportResolve, safety.TargetReport, rep.ID, rep.ID, status+": "+note)
	if err := s.d.Mod.ResolveReportAudited(ctx, rep.ID, status, s.d.Now().UTC(), audit); err != nil {
		return nil, storeErr(err)
	}
	s.d.Log.Info("report resolved", "report", rep.ID, "status", status, "operator", op)
	s.refreshGauges(ctx)
	return ResolveResponse{ID: rep.ID, Status: status}, nil
}

func (s *Server) restrict(r *http.Request, body []byte) (any, error) {
	var req RestrictRequest
	if err := decode(body, &req); err != nil {
		return nil, err
	}
	op, err := operator(req.Operator)
	if err != nil {
		return nil, err
	}
	reason, err := text("--reason", req.Reason, safety.MaxReasonRunes, true)
	if err != nil {
		return nil, err
	}
	if req.Days < 0 || req.Days > safety.MaxRestrictionDays {
		return nil, fail(http.StatusBadRequest, CodeBadRequest, "--days is 1 to %d (leave it out to restrict until you lift it)", safety.MaxRestrictionDays)
	}
	if (req.GuestID == "") == (req.ReportID == "") {
		return nil, fail(http.StatusBadRequest, CodeBadRequest, "give exactly one of --guest and --report")
	}
	ctx := r.Context()

	guest, user := req.GuestID, ""
	if req.ReportID != "" {
		rep, err := s.d.Reports.GetReport(ctx, req.ReportID)
		if err != nil {
			return nil, storeErr(err)
		}
		if rep.TargetGuest == "" {
			return nil, fail(http.StatusUnprocessableEntity, CodeNoVerifiedTarget,
				"the report names no player (its evidence is the client's own claim, or about the AI): use --guest if you know who it is")
		}
		guest = rep.TargetGuest
		if req.UserID != "" {
			if req.UserID != rep.TargetUser {
				return nil, fail(http.StatusUnprocessableEntity, CodeUserNotProven,
					"--user must be the account this report proved was behind the guest; the guest-to-account link is only known from a report")
			}
			user = req.UserID
		}
	} else {
		if req.UserID != "" {
			return nil, fail(http.StatusUnprocessableEntity, CodeUserNotProven,
				"--user needs --report: only a report proves which account sat behind a guest")
		}
		if !safety.ValidBlockTarget("", guest) {
			return nil, fail(http.StatusBadRequest, CodeBadRequest, "--guest is not a valid guest id")
		}
	}
	if existing, ok := s.d.Policy.RestrictionOfGuest(guest); ok {
		return nil, fail(http.StatusConflict, CodeAlreadyRestricted, "the guest is already restricted (%s); unrestrict it first to change it", existing.ID)
	}

	now := s.d.Now().UTC()
	res := safety.Restriction{
		ID: store.NewID(), GuestID: guest, UserID: user, Reason: reason, ReportID: req.ReportID,
		CreatedAt: now, Operator: op,
	}
	details := reason
	if req.Days > 0 {
		res.ExpiresAt = now.Add(time.Duration(req.Days) * 24 * time.Hour)
		details = fmt.Sprintf("%d days: %s", req.Days, reason)
	}
	audit := s.audit(op, safety.ActionRestrict, safety.TargetGuest, guest, req.ReportID, details)
	if err := s.d.Mod.CreateRestriction(ctx, res, audit); err != nil {
		return nil, storeErr(err)
	}
	// After the database write, like the blocks: the policy first, then every
	// live room applies it (room/policy.go).
	s.d.Policy.AddRestriction(res)
	out := RestrictResponse{ID: res.ID, GuestID: guest, UserID: user, ExpiresAt: res.ExpiresAt}
	removed, err := s.d.Rooms.Sweep(ctx)
	if err != nil {
		s.d.Log.Warn("sweeping the rooms after a restriction failed", "err", err)
		out.Warning = "the restriction is stored and refuses the guest from now on, but the live rooms could not be swept"
	}
	out.RemovedFromRooms = len(removed)
	s.d.Log.Info("guest restricted", "restriction", res.ID, "operator", op, "days", req.Days, "removed_from_rooms", out.RemovedFromRooms)
	return out, nil
}

func (s *Server) unrestrict(r *http.Request, body []byte) (any, error) {
	var req UnrestrictRequest
	if err := decode(body, &req); err != nil {
		return nil, err
	}
	op, err := operator(req.Operator)
	if err != nil {
		return nil, err
	}
	ctx := r.Context()
	cur, err := s.d.Mod.GetRestriction(ctx, req.ID)
	if err != nil {
		return nil, storeErr(err)
	}
	audit := s.audit(op, safety.ActionUnrestrict, safety.TargetGuest, cur.GuestID, cur.ReportID, "restriction "+cur.ID)
	revoked, err := s.d.Mod.RevokeRestriction(ctx, req.ID, s.d.Now().UTC(), audit)
	if errors.Is(err, store.ErrRestrictionEnded) {
		return nil, fail(http.StatusConflict, CodeAlreadyEnded, "that restriction has already ended")
	}
	if err != nil {
		return nil, storeErr(err)
	}
	s.d.Policy.RemoveRestriction(revoked.ID)
	s.d.Log.Info("restriction lifted", "restriction", revoked.ID, "operator", op)
	return UnrestrictResponse{ID: revoked.ID, GuestID: revoked.GuestID}, nil
}

func (s *Server) roomClose(r *http.Request, body []byte) (any, error) {
	var req RoomCloseRequest
	if err := decode(body, &req); err != nil {
		return nil, err
	}
	op, err := operator(req.Operator)
	if err != nil {
		return nil, err
	}
	if req.Code == "" && req.InstanceID == "" {
		return nil, fail(http.StatusBadRequest, CodeBadRequest, "give a room CODE or --instance ID")
	}
	if req.InstanceID != "" && !safety.ValidToken(req.InstanceID) {
		return nil, fail(http.StatusBadRequest, CodeBadRequest, "--instance is not a valid room instance id")
	}
	if req.Code != "" && (len(req.Code) > 8 || !safety.ValidToken(req.Code)) {
		return nil, fail(http.StatusBadRequest, CodeBadRequest, "that is not a room code")
	}
	ctx := r.Context()
	res, err := s.d.Rooms.CloseRoom(ctx, room.CloseTarget{Code: strings.ToUpper(req.Code), InstanceID: req.InstanceID})
	switch {
	case errors.Is(err, room.ErrRoomNotFound), errors.Is(err, room.ErrRoomClosed):
		return nil, fail(http.StatusNotFound, CodeNotFound, "no live room matches (it may have ended already)")
	case errors.Is(err, room.ErrInstanceMismatch):
		return nil, fail(http.StatusConflict, CodeInstanceMismatch, "the room that has that code now is not that instance (the code was reused)")
	case errors.Is(err, room.ErrShuttingDown):
		return nil, fail(http.StatusServiceUnavailable, CodeUnavailable, "the server is shutting down")
	case err != nil:
		return nil, err
	}
	out := RoomCloseResponse{Code: res.Code, InstanceID: res.RoomInstanceID}
	auditCtx, cancel := detach(ctx, auditTimeout) // the room is closed: record it even if the client is gone
	defer cancel()
	if err := s.d.Mod.RecordAction(auditCtx, s.audit(op, safety.ActionRoomClose, safety.TargetRoom, res.RoomInstanceID, "", "code "+res.Code)); err != nil {
		s.d.Log.Error("the audit record of a room closure could not be stored", "room", res.Code, "operator", op, "err", err)
		out.Warning = "the room was closed but its audit record could not be stored (see the server log)"
	}
	s.d.Log.Info("room closed by the operator", "room", res.Code, "operator", op)
	return out, nil
}

func (s *Server) profileRemove(r *http.Request, body []byte) (any, error) {
	var req ProfileRemoveRequest
	if err := decode(body, &req); err != nil {
		return nil, err
	}
	op, err := operator(req.Operator)
	if err != nil {
		return nil, err
	}
	if !store.ValidID(req.UserID) {
		return nil, fail(http.StatusBadRequest, CodeBadRequest, "--user is not a valid account id")
	}
	ctx := r.Context()
	if err := s.d.Mod.ClearProfile(ctx, req.UserID); err != nil {
		return nil, storeErr(err)
	}
	out := ProfileRemoveResponse{UserID: req.UserID}
	auditCtx, cancel := detach(ctx, auditTimeout) // the profile is gone: record it even if the client is gone
	defer cancel()
	if err := s.d.Mod.RecordAction(auditCtx, s.audit(op, safety.ActionProfileRemove, safety.TargetUser, req.UserID, "", "profile text removed")); err != nil {
		s.d.Log.Error("the audit record of a profile removal could not be stored", "operator", op, "err", err)
		out.Warning = "the profile was removed but its audit record could not be stored (see the server log)"
	}
	s.d.Log.Info("profile removed by the operator", "operator", op)
	return out, nil
}

func (s *Server) judge(r *http.Request, body []byte) (any, error) {
	var req JudgeRequest
	if err := decode(body, &req); err != nil {
		return nil, err
	}
	if s.d.Judge == nil {
		return nil, fail(http.StatusConflict, CodeJudgeNotConfigured, "the server has no AI judge (no Cloudflare credentials)")
	}
	switch req.Action {
	case "status":
		return JudgeResponse{Judge: s.judgeStatus()}, nil
	case "enable", "disable":
	default:
		return nil, fail(http.StatusBadRequest, CodeBadRequest, "use judge enable, disable or status")
	}
	op, err := operator(req.Operator)
	if err != nil {
		return nil, err
	}
	on := req.Action == "enable"
	if err := notStarted(r.Context()); err != nil {
		return nil, err
	}
	s.d.Judge.SetEnabled(on)
	action := safety.ActionJudgeDisable
	if on {
		action = safety.ActionJudgeEnable
	}
	out := JudgeResponse{Judge: s.judgeStatus()}
	// The switch moved: its record is written even if the client is gone.
	auditCtx, cancel := detach(r.Context(), auditTimeout)
	defer cancel()
	if err := s.d.Mod.RecordAction(auditCtx, s.audit(op, action, safety.TargetJudge, "ai-judge", "", req.Action+"d")); err != nil {
		s.d.Log.Error("the audit record of the judge switch could not be stored", "operator", op, "err", err)
		out.Warning = "the switch was flipped but its audit record could not be stored (see the server log)"
	}
	s.d.Log.Info("AI judge switched by the operator", "enabled", on, "operator", op)
	return out, nil
}

// public is the switch of public games, the owner's measure for an absence
// (docs/MODERATION.md): while off nobody can create or newly join a public
// room, the directory is empty and no new automated opponent is added (those
// already seated keep playing); players already seated finish their game and
// private rooms are unaffected. It lives in memory (PUBLIC_ROOMS_ENABLED is
// what a restart comes back to).
func (s *Server) public(r *http.Request, body []byte) (any, error) {
	var req PublicRequest
	if err := decode(body, &req); err != nil {
		return nil, err
	}
	if s.d.Public == nil {
		return nil, fail(http.StatusConflict, CodePublicNotConfigured, "this server has no room hub to switch")
	}
	switch req.Action {
	case "status":
		return PublicResponse{Public: s.publicStatus()}, nil
	case "enable", "disable":
	default:
		return nil, fail(http.StatusBadRequest, CodeBadRequest, "use public enable, disable or status")
	}
	op, err := operator(req.Operator)
	if err != nil {
		return nil, err
	}
	on := req.Action == "enable"
	out := PublicResponse{}
	// SetPublicEnabled changes nothing when the request already ended (the
	// client gave up); once it wrote the switch, the switch is in force
	// whatever the rooms answer (they only re-evaluate their automated
	// opponents), and their wait is bounded so that the audit has time.
	roomsCtx, cancel := context.WithTimeout(r.Context(), timeout/2)
	defer cancel()
	switch err := s.d.Public.SetPublicEnabled(roomsCtx, on); {
	case errors.Is(err, room.ErrPublicRoomsPending):
		s.d.Log.Warn("the live rooms did not all apply the switch of public games in time", "err", err)
		out.Warning = "the switch is in force, but not every live room confirmed it in time (automated opponents may follow at the next change)"
	case err != nil:
		return nil, fail(http.StatusServiceUnavailable, CodeUnavailable, "the request ended before anything changed")
	}
	out.Public = s.publicStatus()
	action := safety.ActionPublicDisable
	if on {
		action = safety.ActionPublicEnable
	}
	// The switch moved: its record is written even if the client is gone.
	auditCtx, cancelAudit := detach(r.Context(), auditTimeout)
	defer cancelAudit()
	if err := s.d.Mod.RecordAction(auditCtx, s.audit(op, action, safety.TargetPublic, "public-games", "", req.Action+"d")); err != nil {
		s.d.Log.Error("the audit record of the switch of public games could not be stored", "operator", op, "err", err)
		out.Warning = strings.TrimPrefix(out.Warning+"; ", "; ") + "the switch was flipped but its audit record could not be stored (see the server log)"
	}
	s.d.Log.Info("public games switched by the operator", "enabled", on, "operator", op)
	return out, nil
}
