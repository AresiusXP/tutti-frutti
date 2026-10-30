// Command moderate is the owner's moderation tool (docs/MODERATION.md). It does
// nothing by itself: every command is sent over the server's Unix control
// socket to the live game server, which holds the database and the rooms. It
// needs no database credentials and no network; run it inside the server's pod:
//
//	kubectl exec deploy/<server> -- /moderate status
//
// Flags go before the arguments of a command. Every command that changes
// something needs --operator NAME, which is recorded in the audit log.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/AresiusXP/tutti-frutti/server/internal/moderation"
	"github.com/AresiusXP/tutti-frutti/server/internal/safety"
)

const usage = `usage: moderate [--socket PATH] [--json] COMMAND [flags] [arguments]

Flags go before the arguments. --operator NAME is required for every command
that changes something (it is recorded in the audit log).

commands:
  status                                  open reports, oldest open report, restrictions, AI judge
  reports list [--status open|resolved|all] [--limit N]
  reports show ID                         evidence, provenance and what is unverified
  reports resolve --operator NAME --note TEXT [--dismiss] ID
  restrict --operator NAME (--guest ID | --report ID) [--user ID] --reason TEXT [--days N]
  unrestrict --operator NAME ID
  room close --operator NAME [--instance ID] [CODE]
  profile remove --operator NAME --user ID
  judge status
  judge enable|disable --operator NAME

The socket is --socket, else $MODERATION_SOCKET, else ` + moderation.DefaultSocket + `.
`

// doer is what the commands need from the control client.
type doer interface {
	Do(ctx context.Context, path string, req, resp any) error
}

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr, os.Getenv, func(path string) doer { return moderation.NewClient(path) }))
}

// run executes one command line and returns the process exit code: 0 on
// success, 1 when the server refused or failed, 2 for a usage error.
func run(args []string, stdout, stderr io.Writer, getenv func(string) string, newClient func(string) doer) int {
	global := flag.NewFlagSet("moderate", flag.ContinueOnError)
	global.SetOutput(io.Discard)
	socket := global.String("socket", "", "")
	asJSON := global.Bool("json", false, "")
	if err := global.Parse(args); err != nil {
		fmt.Fprint(stderr, usage)
		return 2
	}
	rest := global.Args()
	if len(rest) == 0 {
		fmt.Fprint(stderr, usage)
		return 2
	}
	path := *socket
	if path == "" {
		path = getenv("MODERATION_SOCKET")
	}
	if path == "" {
		path = moderation.DefaultSocket
	}
	c := &cli{out: stdout, err: stderr, json: *asJSON, client: newClient(path)}

	cmd, rest := rest[0], rest[1:]
	var err error
	switch cmd {
	case "status":
		err = c.status(rest)
	case "reports":
		err = c.sub(rest, "reports", map[string]func([]string) error{"list": c.reportsList, "show": c.reportsShow, "resolve": c.reportsResolve})
	case "restrict":
		err = c.restrict(rest)
	case "unrestrict":
		err = c.unrestrict(rest)
	case "room":
		err = c.sub(rest, "room", map[string]func([]string) error{"close": c.roomClose})
	case "profile":
		err = c.sub(rest, "profile", map[string]func([]string) error{"remove": c.profileRemove})
	case "judge":
		err = c.judge(rest)
	case "help", "-h", "--help":
		fmt.Fprint(stdout, usage)
		return 0
	default:
		err = usageError("unknown command %q", cmd)
	}
	var ue *usageErr
	var ae *moderation.APIError
	switch {
	case err == nil:
		return 0
	case errors.As(err, &ue):
		fmt.Fprintf(stderr, "moderate: %v\n\n%s", err, usage)
		return 2
	case errors.As(err, &ae):
		fmt.Fprintf(stderr, "moderate: %s\n", quoteIfNeeded(ae.Error()))
		return 1
	default:
		fmt.Fprintf(stderr, "moderate: %v\n", err)
		return 1
	}
}

type usageErr struct{ msg string }

func (e *usageErr) Error() string { return e.msg }

func usageError(format string, args ...any) error { return &usageErr{fmt.Sprintf(format, args...)} }

type cli struct {
	out, err io.Writer
	json     bool
	client   doer
}

func (c *cli) call(path string, req, resp any) error {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	return c.client.Do(ctx, path, req, resp)
}

// flags builds a FlagSet that reports problems as usage errors.
func flags(name string) *flag.FlagSet {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	return fs
}

// parse reads the flags of a command and returns its positional arguments. A
// flag after the first argument is a mistake (the standard flag package stops at
// the first argument), so it is refused instead of being taken as an argument.
func parse(fs *flag.FlagSet, args []string, positionals int) ([]string, error) {
	if err := fs.Parse(args); err != nil {
		return nil, usageError("%v", err)
	}
	pos := fs.Args()
	for _, a := range pos {
		if strings.HasPrefix(a, "-") && len(a) > 1 {
			return nil, usageError("flags go before the arguments: found %q after an argument", a)
		}
	}
	if len(pos) != positionals {
		return nil, usageError("%s takes %d argument(s), got %d", fs.Name(), positionals, len(pos))
	}
	return pos, nil
}

func (c *cli) sub(args []string, group string, subs map[string]func([]string) error) error {
	if len(args) == 0 {
		return usageError("%s needs a subcommand", group)
	}
	f, ok := subs[args[0]]
	if !ok {
		return usageError("unknown command \"%s %s\"", group, args[0])
	}
	return f(args[1:])
}

// ---- output helpers ----

// safe quotes text a player or the operator typed: control and invisible format
// characters (terminal escape sequences, bidi overrides) are shown as escapes
// and can never act on the operator's terminal.
func safe(s string) string { return strconv.QuoteToGraphic(s) }

func quoteIfNeeded(s string) string {
	if q := safe(s); q != `"`+s+`"` {
		return strings.Trim(q, `"`)
	}
	return s
}

func (c *cli) printJSON(v any) {
	enc := json.NewEncoder(c.out)
	enc.SetIndent("", "  ")
	_ = enc.Encode(v)
}

func ago(now, t time.Time) string {
	if t.IsZero() {
		return "never"
	}
	d := now.Sub(t).Round(time.Minute)
	switch {
	case d < time.Minute:
		return "just now"
	case d < 48*time.Hour:
		return fmt.Sprintf("%s ago", d.Truncate(time.Minute))
	}
	return fmt.Sprintf("%d days ago", int(d.Hours()/24))
}

func stamp(t time.Time) string {
	if t.IsZero() {
		return "-"
	}
	return t.UTC().Format(time.RFC3339)
}

func (c *cli) warn(w string) {
	if w != "" {
		fmt.Fprintf(c.out, "WARNING: %s\n", w)
	}
}

// ---- commands ----

func (c *cli) status(args []string) error {
	if _, err := parse(flags("status"), args, 0); err != nil {
		return err
	}
	var st moderation.StatusResponse
	if err := c.call(moderation.PathStatus, struct{}{}, &st); err != nil {
		return err
	}
	if c.json {
		c.printJSON(st)
		return nil
	}
	fmt.Fprintf(c.out, "server version:       %s\n", st.Version)
	fmt.Fprintf(c.out, "open reports:         %d\n", st.OpenReports)
	if st.OpenReports > 0 {
		fmt.Fprintf(c.out, "oldest open report:   %s (filed %s)\n", (time.Duration(st.OldestOpenAgeSeconds) * time.Second).String(), stamp(st.OldestOpenAt))
	}
	fmt.Fprintf(c.out, "active restrictions:  %d\n", st.ActiveRestrictions)
	fmt.Fprintf(c.out, "blocks:               %d\n", st.Blocks)
	switch {
	case !st.Judge.Configured:
		fmt.Fprintln(c.out, "AI judge:             not configured")
	case st.Judge.Enabled:
		fmt.Fprintln(c.out, "AI judge:             on")
	default:
		fmt.Fprintln(c.out, "AI judge:             OFF (switched off by an operator; a restart turns it on again)")
	}
	return nil
}

func (c *cli) reportsList(args []string) error {
	fs := flags("reports list")
	status := fs.String("status", "open", "open, resolved or all")
	limit := fs.Int("limit", 20, "")
	if _, err := parse(fs, args, 0); err != nil {
		return err
	}
	var res moderation.ReportsListResponse
	if err := c.call(moderation.PathReportsList, moderation.ReportsListRequest{Status: *status, Limit: *limit}, &res); err != nil {
		return err
	}
	if c.json {
		c.printJSON(res)
		return nil
	}
	if len(res.Reports) == 0 {
		fmt.Fprintf(c.out, "no %s reports\n", *status)
		return nil
	}
	now := time.Now()
	for _, r := range res.Reports {
		verified := "VERIFIED"
		if !r.EvidenceVerified {
			verified = "UNVERIFIED"
		}
		target := "no target"
		if r.HasTarget {
			target = "target known"
		}
		fmt.Fprintf(c.out, "%s  %-9s %-12s %-14s %-10s %s  filed %s\n", r.ID, r.Status, r.Kind, r.Reason, verified, target, ago(now, r.CreatedAt))
		if r.Comment != "" {
			fmt.Fprintf(c.out, "    reporter says (unverified): %s\n", safe(r.Comment))
		}
	}
	return nil
}

func (c *cli) reportsShow(args []string) error {
	pos, err := parse(flags("reports show"), args, 1)
	if err != nil {
		return err
	}
	var v moderation.ReportView
	if err := c.call(moderation.PathReportsShow, moderation.ReportRef{ID: pos[0]}, &v); err != nil {
		return err
	}
	if c.json {
		c.printJSON(v)
		return nil
	}
	c.renderReport(v, time.Now())
	return nil
}

func (c *cli) renderReport(v moderation.ReportView, now time.Time) {
	w := c.out
	fmt.Fprintf(w, "report %s  [%s]\n", v.ID, v.Status)
	fmt.Fprintf(w, "  kind / reason:   %s / %s\n", v.Kind, v.Reason)
	fmt.Fprintf(w, "  filed:           %s (%s)\n", stamp(v.CreatedAt), ago(now, v.CreatedAt))
	if !v.ResolvedAt.IsZero() {
		fmt.Fprintf(w, "  resolved:        %s\n", stamp(v.ResolvedAt))
	}
	fmt.Fprintf(w, "  deleted on:      %s (retention)\n", stamp(v.PurgeAt))
	if v.EvidenceVerified {
		fmt.Fprintln(w, "  evidence:        VERIFIED: the room copied it from the reporter's own view")
	} else {
		fmt.Fprintln(w, "  evidence:        UNVERIFIED: the room was gone, its code reused, the instance id did not match (anybody who knows a code can send one) or the content changed; only the reporting client's claim. Never act on it alone")
	}
	fmt.Fprintf(w, "  room instance:   %s\n  match instance:  %s\n  content key:     %s\n", dash(v.RoomInstance), dash(v.MatchInstance), safe(v.ContentKey))
	fmt.Fprintf(w, "  reporter:        guest %s%s\n", dash(v.ReporterGuest), account(v.ReporterUser))
	if v.TargetGuest == "" {
		fmt.Fprintln(w, "  target:          none known (use the room's own evidence below; restrict --guest only if you are sure who it is)")
	} else {
		fmt.Fprintf(w, "  target:          guest %s%s\n", v.TargetGuest, account(v.TargetUser))
	}
	fmt.Fprintf(w, "  reporter's comment (UNVERIFIED, typed by the reporter): %s\n", safe(v.Comment))

	var ev safety.EvidenceData
	if err := json.Unmarshal(v.Evidence, &ev); err != nil {
		fmt.Fprintf(w, "  evidence JSON (unreadable: %v): %s\n", err, safe(string(v.Evidence)))
		return
	}
	fmt.Fprintf(w, "  evidence (source %s):\n", dash(ev.Source))
	if ev.Lang != "" || ev.Phase != "" || ev.Round != 0 || ev.Letter != "" {
		fmt.Fprintf(w, "    lang %s, phase %s, round %d, letter %s\n", dash(ev.Lang), dash(ev.Phase), ev.Round, dash(ev.Letter))
	}
	if ev.Category != nil {
		custom := ""
		if ev.Category.Custom {
			custom = " (custom label)"
		}
		fmt.Fprintf(w, "    category %d: %s%s\n", ev.Category.Index, safe(ev.Category.Label), custom)
	}
	if ev.Player != nil {
		left := ""
		if ev.Player.Left {
			left = " (left)"
		}
		fmt.Fprintf(w, "    player %s: name %s, avatar %s %s%s\n", ev.Player.ID, safe(ev.Player.Name), safe(ev.Player.Emoji), safe(ev.Player.Color), left)
	}
	if ev.Text != "" {
		fmt.Fprintf(w, "    text: %s\n", safe(ev.Text))
	}
	if ev.Automated {
		fmt.Fprintln(w, "    the content belongs to an automated seat")
	}
	if ev.Author != nil {
		fmt.Fprintf(w, "    author: guest %s%s\n", ev.Author.Guest, account(ev.Author.User))
	}
	if ev.Verdict != nil {
		fmt.Fprintf(w, "    AI verdict: %s, reason %s, about the answer %s\n", ev.Verdict.Value, safe(ev.Verdict.Reason), safe(ev.Verdict.Answer))
	}
	if ev.Claimed != nil {
		raw, _ := json.Marshal(ev.Claimed)
		fmt.Fprintf(w, "    claimed reference (UNVERIFIED, from the client): %s\n", safe(string(raw)))
	}
	if ev.ObservedText != "" {
		fmt.Fprintf(w, "    observed text (UNVERIFIED, from the client): %s\n", safe(ev.ObservedText))
	}
}

func dash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

func account(user string) string {
	if user == "" {
		return ""
	}
	return " (account " + user + ")"
}

func (c *cli) reportsResolve(args []string) error {
	fs := flags("reports resolve")
	op := fs.String("operator", "", "")
	note := fs.String("note", "", "")
	dismiss := fs.Bool("dismiss", false, "")
	pos, err := parse(fs, args, 1)
	if err != nil {
		return err
	}
	var res moderation.ResolveResponse
	if err := c.call(moderation.PathReportsResolve, moderation.ResolveRequest{Operator: *op, ID: pos[0], Note: *note, Dismiss: *dismiss}, &res); err != nil {
		return err
	}
	if c.json {
		c.printJSON(res)
		return nil
	}
	fmt.Fprintf(c.out, "report %s is now %s\n", res.ID, res.Status)
	c.warn(res.Warning)
	return nil
}

func (c *cli) restrict(args []string) error {
	fs := flags("restrict")
	op := fs.String("operator", "", "")
	guest := fs.String("guest", "", "")
	report := fs.String("report", "", "")
	user := fs.String("user", "", "")
	reason := fs.String("reason", "", "")
	days := fs.Int("days", 0, "")
	if _, err := parse(fs, args, 0); err != nil {
		return err
	}
	var res moderation.RestrictResponse
	req := moderation.RestrictRequest{Operator: *op, GuestID: *guest, ReportID: *report, UserID: *user, Reason: *reason, Days: *days}
	if err := c.call(moderation.PathRestrict, req, &res); err != nil {
		return err
	}
	if c.json {
		c.printJSON(res)
		return nil
	}
	until := "until you lift it"
	if !res.ExpiresAt.IsZero() {
		until = "until " + stamp(res.ExpiresAt)
	}
	fmt.Fprintf(c.out, "restriction %s: guest %s%s restricted %s; removed from %d live room(s)\n", res.ID, res.GuestID, account(res.UserID), until, res.RemovedFromRooms)
	fmt.Fprintf(c.out, "lift it with: moderate unrestrict --operator %s %s\n", quoteIfNeeded(*op), res.ID)
	c.warn(res.Warning)
	return nil
}

func (c *cli) unrestrict(args []string) error {
	fs := flags("unrestrict")
	op := fs.String("operator", "", "")
	pos, err := parse(fs, args, 1)
	if err != nil {
		return err
	}
	var res moderation.UnrestrictResponse
	if err := c.call(moderation.PathUnrestrict, moderation.UnrestrictRequest{Operator: *op, ID: pos[0]}, &res); err != nil {
		return err
	}
	if c.json {
		c.printJSON(res)
		return nil
	}
	fmt.Fprintf(c.out, "restriction %s lifted: guest %s may play again\n", res.ID, res.GuestID)
	return nil
}

func (c *cli) roomClose(args []string) error {
	fs := flags("room close")
	op := fs.String("operator", "", "")
	instance := fs.String("instance", "", "")
	if err := fs.Parse(args); err != nil {
		return usageError("%v", err)
	}
	pos := fs.Args()
	for _, a := range pos {
		if strings.HasPrefix(a, "-") && len(a) > 1 {
			return usageError("flags go before the arguments: found %q after an argument", a)
		}
	}
	if len(pos) > 1 || (len(pos) == 0 && *instance == "") {
		return usageError("room close takes a room CODE, or --instance ID, or both")
	}
	req := moderation.RoomCloseRequest{Operator: *op, InstanceID: *instance}
	if len(pos) == 1 {
		req.Code = pos[0]
	}
	var res moderation.RoomCloseResponse
	if err := c.call(moderation.PathRoomClose, req, &res); err != nil {
		return err
	}
	if c.json {
		c.printJSON(res)
		return nil
	}
	fmt.Fprintf(c.out, "closed room %s (instance %s); everyone in it was disconnected and its saved state deleted\n", res.Code, res.InstanceID)
	c.warn(res.Warning)
	return nil
}

func (c *cli) profileRemove(args []string) error {
	fs := flags("profile remove")
	op := fs.String("operator", "", "")
	user := fs.String("user", "", "")
	if _, err := parse(fs, args, 0); err != nil {
		return err
	}
	var res moderation.ProfileRemoveResponse
	if err := c.call(moderation.PathProfileRemove, moderation.ProfileRemoveRequest{Operator: *op, UserID: *user}, &res); err != nil {
		return err
	}
	if c.json {
		c.printJSON(res)
		return nil
	}
	fmt.Fprintf(c.out, "saved profile text of account %s removed (statistics kept)\n", res.UserID)
	c.warn(res.Warning)
	return nil
}

func (c *cli) judge(args []string) error {
	if len(args) == 0 || (args[0] != "status" && args[0] != "enable" && args[0] != "disable") {
		return usageError("use: judge status | judge enable|disable --operator NAME")
	}
	action := args[0]
	fs := flags("judge " + action)
	op := fs.String("operator", "", "")
	if _, err := parse(fs, args[1:], 0); err != nil {
		return err
	}
	var res moderation.JudgeResponse
	if err := c.call(moderation.PathJudge, moderation.JudgeRequest{Operator: *op, Action: action}, &res); err != nil {
		return err
	}
	if c.json {
		c.printJSON(res)
		return nil
	}
	state := "off"
	if res.Judge.Enabled {
		state = "on"
	}
	fmt.Fprintf(c.out, "AI judge is %s\n", state)
	c.warn(res.Warning)
	return nil
}
