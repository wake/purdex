package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"github.com/google/uuid"
	"io"
	"math"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/wake/purdex/cmd/pdx/daemonclient"
	"github.com/wake/purdex/internal/config"
	"github.com/wake/purdex/internal/team"
)

// relayUsage is the grammar-rejection message for `pdx relay` (exit 2).
// These are the mod's calls (lead-team-relay spec §8.3) plus `pdx relay <ref>`
// (the lead's member relay, P6-5).
const relayUsage = "usage: pdx relay hello --session <sid> [--version <v>] [--agent cc] [--config <path>]\n" +
	"       pdx relay begin --self --session <sid> --used <pct> --window <n> [--manual] [--config <path>]\n" +
	"       pdx relay ask --session <sid> --used <pct> --window <n> [--request-id <uuid>] [--config <path>]\n" +
	"       pdx relay wait <request_id> [--wait 9m] [--config <path>]\n" +
	"       pdx relay self off|on|status --session <sid> [--config <path>]\n" +
	"       pdx relay report <op> <state> [--new-session <sid>] [--error <e>] [--config <path>]\n" +
	"       pdx relay op <id> [--config <path>]\n" +
	"       pdx relay prompts [--config <path>]\n" +
	"       pdx relay lock|unlock <op> --session <sid> [--config <path>]\n" +
	"       pdx relay claim|seen <op> --session <sid> [--config <path>]\n" +
	"       pdx relay compacted --session <sid> --trigger auto|manual [--config <path>]\n" +
	"       pdx relay <ref|address> [--wait <dur>] [--config <path>]"

const (
	// relayAttemptTimeout bounds one long-poll (team.MaxPollWaitS plus room), as lead's does.
	relayAttemptTimeout = 35 * time.Second
	// relayFinalReadTimeout bounds the one short read a wait makes when its
	// --wait bound fires, to tell a terminal answer that was in flight from
	// a request that is still open.
	relayFinalReadTimeout = 5 * time.Second
	// relayMaxHungPolls: consecutive polls without any answer before exit 20 (spec §9.1, P2b decision).
	relayMaxHungPolls = 3
)

// relayRefusalCodes are the 409 team-rule codes `pdx relay` can meet (spec
// §14): all exit 13. relay_open and bad_transition also print the op the
// daemon sent, on stdout, so the mod can continue from it.
var relayRefusalCodes = map[string]bool{
	team.ErrMemberRelayIsLeads: true,
	team.ErrSelfRelayOff:       true,
	team.ErrSelfRelayPaused:    true,
	team.ErrRelayOpen:          true,
	team.ErrBadTransition:      true,
	team.ErrNotLead:            true, // P6-5: the member relay's refusals
	team.ErrNotYourMember:      true,
	team.ErrRelayUnsupported:   true,
	team.ErrNotYourOp:          true,
	team.ErrNotMember:          true, // a member's ask from a session that is no member
}

// runRelay is the `pdx relay` switch target. SIGINT/SIGTERM cancel ctx;
// a wait that is cancelled exits 12 without touching the request (the
// daemon's lease closes it if nobody polls again).
func runRelay(args []string) {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	os.Exit(runRelayCmd(ctx, args, os.Stdout, os.Stderr))
}

// relayFlags parses the flags common to every subcommand plus the ones the
// caller declares on fs; ok=false means the usage line was written (exit 2).
func relayFlags(fs *flag.FlagSet, args []string, stderr io.Writer) (cfgPath string, ok bool) {
	fs.SetOutput(io.Discard)
	fs.StringVar(&cfgPath, "config", "", "")
	if err := fs.Parse(args); err != nil {
		fmt.Fprintf(stderr, "pdx relay: %s\n%s\n", err.Error(), relayUsage)
		return "", false
	}
	return cfgPath, true
}

// relayClient loads the config and builds the restart-aware client.
func relayClient(cfgPath string, stderr io.Writer, attempt time.Duration, clientOpts []daemonclient.Option) (*daemonclient.Client, int) {
	cfg, err := config.Load(cfgPath)
	if err != nil {
		fmt.Fprintf(stderr, "pdx relay: %v\n", err)
		return nil, ExitError
	}
	base := fmt.Sprintf("http://%s:%d", resolveDaemonHost(cfg.Bind), cfg.Port)
	opts := append([]daemonclient.Option{daemonclient.WithStderr(stderr), daemonclient.WithAttemptTimeout(attempt)}, clientOpts...)
	return daemonclient.New(base, cfg.Token, opts...), ExitOK
}

// runRelayCmd implements `pdx relay <sub>` and returns the exit code (spec
// §14). Grammar rejections return 2 before any config load.
func runRelayCmd(ctx context.Context, args []string, stdout, stderr io.Writer, clientOpts ...daemonclient.Option) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, relayUsage)
		return ExitUsage
	}
	switch args[0] {
	case "hello":
		return runRelayHello(ctx, args[1:], stdout, stderr, clientOpts)
	case "begin":
		return runRelayBegin(ctx, args[1:], stdout, stderr, clientOpts)
	case "ask":
		return runRelayAsk(ctx, args[1:], stdout, stderr, clientOpts)
	case "wait":
		return runRelayWait(ctx, args[1:], stdout, stderr, clientOpts)
	case "self":
		return runRelaySelf(ctx, args[1:], stdout, stderr, clientOpts)
	case "report":
		return runRelayReport(ctx, args[1:], stdout, stderr, clientOpts)
	case "op":
		return runRelayOp(ctx, args[1:], stdout, stderr, clientOpts)
	case "prompts":
		return runRelayPrompts(ctx, args[1:], stdout, stderr, clientOpts)
	case "claim":
		return runRelayClaim(ctx, args[1:], stdout, stderr, clientOpts)
	case "seen":
		return runRelaySeen(ctx, args[1:], stdout, stderr, clientOpts)
	case "compacted":
		return runRelayCompacted(ctx, args[1:], stdout, stderr, clientOpts)
	case "lock":
		return runRelayLock(args[1:], stdout, stderr, true)
	case "unlock":
		return runRelayLock(args[1:], stdout, stderr, false)
	default:
		if isMemberRelayTarget(args[0]) {
			return runRelayMember(ctx, args[0], args[1:], stdout, stderr, clientOpts)
		}
		fmt.Fprintf(stderr, "pdx relay: unknown subcommand %q\n%s\n", args[0], relayUsage)
		return ExitUsage
	}
}

// runRelayCompacted is `pdx relay compacted --session <sid> --trigger auto|manual` (P7-2): the mod reports a compaction it
// did not intercept; the daemon decides whether the lead is told. Not retried (Idempotent is left off): a retry could tell
// the lead twice. stdout is the daemon's {"noticed":bool}.
func runRelayCompacted(ctx context.Context, args []string, stdout, stderr io.Writer, clientOpts []daemonclient.Option) int {
	fs := flag.NewFlagSet("pdx relay compacted", flag.ContinueOnError)
	var req team.RelayCompactedRequest
	fs.StringVar(&req.SessionID, "session", "", "")
	fs.StringVar(&req.Trigger, "trigger", "", "")
	cfgPath, ok := relayFlags(fs, args, stderr)
	if !ok {
		return ExitUsage
	}
	switch {
	case fs.NArg() != 0:
		return relayUsageErr(stderr, fmt.Sprintf("unexpected argument %q", fs.Arg(0)))
	case strings.TrimSpace(req.SessionID) == "":
		return relayUsageErr(stderr, "--session 不能為空")
	case req.Trigger != "auto" && req.Trigger != "manual":
		return relayUsageErr(stderr, "--trigger must be auto or manual")
	}
	client, code := relayClient(cfgPath, stderr, daemonclient.DefaultAttemptTimeout, clientOpts)
	if code != ExitOK {
		return code
	}
	var res team.RelayCompactedResponse
	if _, err := client.Do(ctx, http.MethodPost, "/api/relay/compacted", req, &res); err != nil {
		return relayReportErr(err, stdout, stderr)
	}
	return relayPrintJSON(stdout, stderr, res)
}

func relayUsageErr(stderr io.Writer, msg string) int {
	fmt.Fprintf(stderr, "pdx relay: %s\n%s\n", msg, relayUsage)
	return ExitUsage
}

// relayPrintJSON writes v as one JSON line on stdout.
func relayPrintJSON(stdout, stderr io.Writer, v any) int {
	out, err := json.Marshal(v)
	if err != nil {
		fmt.Fprintf(stderr, "pdx relay: %v\n", err)
		return ExitError
	}
	fmt.Fprintln(stdout, string(out))
	return ExitOK
}

// relayReportErr maps a client error to stderr text, stdout (the op on the
// two refusals that carry one) and an exit code. On every StatusError the
// line is `pdx relay: <detail> <code>` — the API code is the LAST
// whitespace-separated stderr token (P5b-2's `stderrCode()` and P8a-2's
// `ask.js` read it that way); sanitizeCell strips newlines and control
// characters from the detail, so nothing can follow the code.
func relayReportErr(err error, stdout, stderr io.Writer) int {
	var se *daemonclient.StatusError
	switch {
	case errors.Is(err, daemonclient.ErrUnavailable):
		fmt.Fprintln(stderr, "pdx relay: 等了 30 秒 daemon 仍沒有回應 daemon_unavailable")
		return ExitUnavailable
	case errors.Is(err, daemonclient.ErrUnsupported):
		fmt.Fprintln(stderr, "pdx relay: 這個 daemon 沒有 /api/relay 路由，請先更新 daemon unsupported")
		return ExitUnsupported
	case errors.As(err, &se) && relayRefusalCodes[se.API.Error]:
		fmt.Fprintf(stderr, "pdx relay: %s %s\n", sanitizeCell(se.API.Detail), se.API.Error)
		if se.API.Op != nil {
			if out, merr := json.Marshal(se.API.Op); merr == nil {
				fmt.Fprintln(stdout, string(out))
			}
		}
		return ExitRefused
	case errors.As(err, &se):
		fmt.Fprintf(stderr, "pdx relay: %s %s\n", sanitizeCell(se.API.Detail), sanitizeCell(se.API.Error))
		return ExitError
	default:
		fmt.Fprintf(stderr, "pdx relay: %v\n", err)
		return ExitError
	}
}

func runRelayHello(ctx context.Context, args []string, stdout, stderr io.Writer, clientOpts []daemonclient.Option) int {
	fs := flag.NewFlagSet("pdx relay hello", flag.ContinueOnError)
	var req team.RelayHelloRequest
	fs.StringVar(&req.SessionID, "session", "", "")
	fs.StringVar(&req.ModVersion, "version", "", "")
	fs.StringVar(&req.Agent, "agent", "cc", "")
	cfgPath, ok := relayFlags(fs, args, stderr)
	if !ok {
		return ExitUsage
	}
	if fs.NArg() != 0 || strings.TrimSpace(req.SessionID) == "" {
		return relayUsageErr(stderr, "--session 不能為空")
	}
	client, code := relayClient(cfgPath, stderr, daemonclient.DefaultAttemptTimeout, clientOpts)
	if code != ExitOK {
		return code
	}
	var res team.RelayHelloResponse
	if _, err := client.Do(ctx, http.MethodPost, "/api/relay/hello", req, &res, daemonclient.Idempotent()); err != nil {
		return relayReportErr(err, stdout, stderr)
	}
	return relayPrintJSON(stdout, stderr, res)
}

// relayNewID mints the client-side request id of a begin (a test seam).
var relayNewID = uuid.NewString

func runRelayBegin(ctx context.Context, args []string, stdout, stderr io.Writer, clientOpts []daemonclient.Option) int {
	fs := flag.NewFlagSet("pdx relay begin", flag.ContinueOnError)
	var req team.RelayBeginRequest
	used := fs.Float64("used", -1, "")
	fs.BoolVar(&req.Self, "self", false, "")
	fs.BoolVar(&req.Manual, "manual", false, "") // a person's /relay (MR-1): the mod sends it, the daemon still asks a person to approve
	fs.StringVar(&req.SessionID, "session", "", "")
	fs.IntVar(&req.Window, "window", -1, "") // -1: not given (the grammar requires it)
	cfgPath, ok := relayFlags(fs, args, stderr)
	if !ok {
		return ExitUsage
	}
	switch {
	case fs.NArg() != 0:
		return relayUsageErr(stderr, fmt.Sprintf("unexpected argument %q", fs.Arg(0)))
	case !req.Self:
		return relayUsageErr(stderr, "--self is required (a member relay is `pdx relay <ref>`, P6)")
	case strings.TrimSpace(req.SessionID) == "":
		return relayUsageErr(stderr, "--session 不能為空")
	case math.IsNaN(*used) || math.IsInf(*used, 0) || *used < 0 || *used > 100:
		return relayUsageErr(stderr, "--used 必須是 0 到 100 之間的數字")
	case req.Window < 0:
		return relayUsageErr(stderr, "--window <n> 是必要的，且不能是負數")
	}
	req.UsedPercentage = *used
	// The request id is minted here, once, before the first attempt: a
	// replay after a lost response carries the same id and the daemon
	// answers with the op that id opened — whatever its state by then —
	// so a retry can never open a second request (PR #1726 A-1).
	req.RequestID = relayNewID()
	client, code := relayClient(cfgPath, stderr, daemonclient.DefaultAttemptTimeout, clientOpts)
	if code != ExitOK {
		return code
	}
	var res team.RelayBeginResponse
	if _, err := client.Do(ctx, http.MethodPost, "/api/relay/begin", req, &res, daemonclient.Idempotent()); err != nil {
		return relayReportErr(err, stdout, stderr)
	}
	fmt.Fprintf(stderr, "接力申請已送出（%s），等待核准；接著執行 pdx relay wait %s\n", res.Op.ID, res.RequestID)
	return relayPrintJSON(stdout, stderr, res)
}

// runRelayAsk is the member's mod asking its lead to relay it (member relay ask §5; mod-internal, listed with begin).
// The request id is minted here when not given, so a retry of one call carries the same id and is the same ask. It
// prints the daemon's answer as one JSON line; refusals (not_member, relay_open, relay_unsupported) are exit 13.
func runRelayAsk(ctx context.Context, args []string, stdout, stderr io.Writer, clientOpts []daemonclient.Option) int {
	fs := flag.NewFlagSet("pdx relay ask", flag.ContinueOnError)
	var req team.RelayAskRequest
	used := fs.Float64("used", -1, "") // a decimal, as begin takes it: the mod's percent is fractional
	fs.StringVar(&req.SessionID, "session", "", "")
	fs.IntVar(&req.Window, "window", -1, "") // -1: not given (the grammar requires it)
	fs.StringVar(&req.RequestID, "request-id", "", "")
	cfgPath, ok := relayFlags(fs, args, stderr)
	if !ok {
		return ExitUsage
	}
	switch {
	case fs.NArg() != 0:
		return relayUsageErr(stderr, fmt.Sprintf("unexpected argument %q", fs.Arg(0)))
	case strings.TrimSpace(req.SessionID) == "":
		return relayUsageErr(stderr, "--session 不能為空")
	case math.IsNaN(*used) || math.IsInf(*used, 0) || *used < 0 || *used > 100:
		return relayUsageErr(stderr, "--used 必須是 0 到 100 之間的數字")
	case req.Window < 0:
		return relayUsageErr(stderr, "--window <n> 是必要的，且不能是負數")
	}
	if req.RequestID == "" {
		req.RequestID = relayNewID()
	} else if u, err := uuid.Parse(req.RequestID); err != nil || u.Version() != 4 {
		return relayUsageErr(stderr, "--request-id 必須是 UUID v4")
	}
	req.UsedPct = int(math.Floor(*used)) // the wire carries whole percent points
	client, code := relayClient(cfgPath, stderr, daemonclient.DefaultAttemptTimeout, clientOpts)
	if code != ExitOK {
		return code
	}
	var res team.RelayAskResponse
	if _, err := client.Do(ctx, http.MethodPost, "/api/relay/ask", req, &res, daemonclient.Idempotent()); err != nil {
		return relayReportErr(err, stdout, stderr)
	}
	return relayPrintJSON(stdout, stderr, res)
}

// runRelayWait long-polls the self_relay approval row (spec §8.7 (b)):
// each GET ?wait=25 renews the lease; the loop runs until the row closes
// or --wait (default 9 min, cap 10 min) runs out. Exit: 0 approved with the
// Approval on stdout; 0 still open when --wait ran out, with
// the approval row's JSON — {"id":…,"kind":"self_relay","state":"open",…} — on stdout (always — even when no poll has answered yet —
// so the mod's waitLoop can tell "call again" from "approved" by `state`
// alone); 10 denied; 11 timeout; 12 cancelled or abandoned, or ctx
// cancelled; 20 / 21 as everywhere.
func runRelayWait(ctx context.Context, args []string, stdout, stderr io.Writer, clientOpts []daemonclient.Option) int {
	// The positional comes first (flag stops at the first non-flag), so
	// `pdx relay wait <id> --wait 9m` parses: id, then the flags.
	if len(args) == 0 || strings.TrimSpace(args[0]) == "" || strings.HasPrefix(args[0], "-") {
		return relayUsageErr(stderr, "需要一個 request_id")
	}
	id := args[0]
	fs := flag.NewFlagSet("pdx relay wait", flag.ContinueOnError)
	wait := fs.Duration("wait", time.Duration(team.DefaultWaitS)*time.Second, "")
	cfgPath, ok := relayFlags(fs, args[1:], stderr)
	if !ok {
		return ExitUsage
	}
	if fs.NArg() != 0 {
		return relayUsageErr(stderr, fmt.Sprintf("unexpected argument %q", fs.Arg(0)))
	}
	if *wait <= 0 || *wait > time.Duration(team.MaxWaitS)*time.Second {
		return relayUsageErr(stderr, fmt.Sprintf("--wait 必須大於 0 且不超過 %ds", team.MaxWaitS))
	}
	client, code := relayClient(cfgPath, stderr, relayAttemptTimeout, clientOpts)
	if code != ExitOK {
		return code
	}
	// The --wait bound is a cancellation, not a context deadline: the client
	// applies its per-attempt timeout (ErrNoAnswer, counted below) only to a
	// ctx without a deadline, and the hung-daemon rule of spec §9.1 must
	// still end a wait with 20 before the bound does.
	deadline, cancel := context.WithCancel(ctx)
	defer cancel()
	bound := time.AfterFunc(*wait, cancel)
	defer bound.Stop()
	hung := 0
	var ap team.Approval
	for {
		if ctx.Err() != nil {
			fmt.Fprintln(stderr, "pdx relay: 等待已中斷，申請仍然開著")
			return ExitCancelled
		}
		if deadline.Err() != nil {
			// The bound fired, possibly while a terminal answer was in
			// flight (PR #1726 A-2): read the row once more, short and
			// without a long poll, under a fresh context, so the outcome is
			// the daemon's state and not the scheduler's.
			final, fctx, fcancel := team.Approval{}, context.Context(nil), context.CancelFunc(nil)
			fctx, fcancel = context.WithTimeout(ctx, relayFinalReadTimeout)
			_, ferr := client.Do(fctx, http.MethodGet, fmt.Sprintf("/api/relay/wait/%s?wait=0", id), nil, &final)
			fcancel()
			if ferr == nil && final.ID != "" {
				ap = final
				if ap.State != team.StateOpen {
					return relayFinish(ap, stdout, stderr)
				}
			}
			fmt.Fprintln(stderr, "pdx relay: --wait 已到，申請仍在等待核准；請再呼叫一次 pdx relay wait")
			// Always a row with state "open" (the last polled row when there is
			// one, a bare state otherwise): the mod re-calls on this shape.
			if ap.ID == "" {
				ap = team.Approval{ID: id, Kind: team.KindSelfRelay, State: team.StateOpen}
			}
			return relayPrintJSON(stdout, stderr, ap)
		}
		var polled team.Approval
		_, err := client.Do(deadline, http.MethodGet, fmt.Sprintf("/api/relay/wait/%s?wait=%d", id, team.MaxPollWaitS), nil, &polled)
		if err != nil {
			if ctx.Err() != nil || deadline.Err() != nil {
				continue // the loop head reports which one
			}
			if errors.Is(err, daemonclient.ErrNoAnswer) || errors.Is(err, context.DeadlineExceeded) {
				hung++
				if hung >= relayMaxHungPolls {
					fmt.Fprintln(stderr, "pdx relay: daemon 沒有回應")
					return ExitUnavailable
				}
				continue
			}
			return relayReportErr(err, stdout, stderr)
		}
		hung = 0
		ap = polled
		if ap.State != team.StateOpen {
			return relayFinish(ap, stdout, stderr)
		}
	}
}

// relayFinish maps a closed self_relay Approval to output and exit code (spec §14).
func relayFinish(ap team.Approval, stdout, stderr io.Writer) int {
	switch ap.State {
	case team.StateApproved:
		return relayPrintJSON(stdout, stderr, ap)
	case team.StateDenied:
		fmt.Fprintf(stderr, "pdx relay: 接力申請已被拒絕%s\n", leadDecidedBy(ap))
		return ExitDenied
	case team.StateTimeout:
		fmt.Fprintln(stderr, "pdx relay: 接力申請逾時，視同拒絕")
		return ExitTimeout
	case team.StateCancelled:
		fmt.Fprintln(stderr, "pdx relay: 接力申請已取消")
		return ExitCancelled
	case team.StateAbandoned:
		fmt.Fprintln(stderr, "pdx relay: 接力申請已失效（lease 到期或來源 session 已結束）")
		return ExitCancelled
	default:
		fmt.Fprintf(stderr, "pdx relay: 未知狀態 %q\n", sanitizeCell(string(ap.State)))
		return ExitError
	}
}

func runRelaySelf(ctx context.Context, args []string, stdout, stderr io.Writer, clientOpts []daemonclient.Option) int {
	if len(args) == 0 {
		return relayUsageErr(stderr, "需要 off、on 或 status")
	}
	action := args[0]
	if action != "off" && action != "on" && action != "status" {
		return relayUsageErr(stderr, fmt.Sprintf("unknown action %q", action))
	}
	fs := flag.NewFlagSet("pdx relay self", flag.ContinueOnError)
	req := team.RelaySelfRequest{Action: action}
	fs.StringVar(&req.SessionID, "session", "", "")
	cfgPath, ok := relayFlags(fs, args[1:], stderr)
	if !ok {
		return ExitUsage
	}
	if fs.NArg() != 0 || strings.TrimSpace(req.SessionID) == "" {
		return relayUsageErr(stderr, "--session 不能為空")
	}
	client, code := relayClient(cfgPath, stderr, daemonclient.DefaultAttemptTimeout, clientOpts)
	if code != ExitOK {
		return code
	}
	var res team.RelaySelfResponse
	if _, err := client.Do(ctx, http.MethodPost, "/api/relay/self", req, &res, daemonclient.Idempotent()); err != nil {
		return relayReportErr(err, stdout, stderr)
	}
	return relayPrintJSON(stdout, stderr, res)
}

func runRelayReport(ctx context.Context, args []string, stdout, stderr io.Writer, clientOpts []daemonclient.Option) int {
	if len(args) < 2 {
		return relayUsageErr(stderr, "需要 <op> 與 <state>")
	}
	opID, state := args[0], team.RelayState(args[1])
	fs := flag.NewFlagSet("pdx relay report", flag.ContinueOnError)
	req := team.RelayReportRequest{State: state}
	fs.StringVar(&req.NewSessionID, "new-session", "", "")
	fs.StringVar(&req.Error, "error", "", "")
	cfgPath, ok := relayFlags(fs, args[2:], stderr)
	if !ok {
		return ExitUsage
	}
	if fs.NArg() != 0 || strings.TrimSpace(opID) == "" {
		return relayUsageErr(stderr, "需要 <op> 與 <state>")
	}
	// claimed is not reportable (P5a-2b codex R1): a self op is claimed by
	// its approval's close, a member op by P6's claim route; the daemon
	// answers 400, so the grammar refuses it here first.
	switch state {
	case team.RelayWriting, team.RelayWritten, team.RelayCleared, team.RelayDone, team.RelayFailed, team.RelayCancelled:
	default:
		return relayUsageErr(stderr, fmt.Sprintf("unknown state %q", string(state)))
	}
	if state == team.RelayCleared && strings.TrimSpace(req.NewSessionID) == "" {
		return relayUsageErr(stderr, "cleared 需要 --new-session")
	}
	if (state == team.RelayFailed || state == team.RelayCancelled) && strings.TrimSpace(req.Error) == "" {
		return relayUsageErr(stderr, "failed / cancelled 需要 --error")
	}
	client, code := relayClient(cfgPath, stderr, daemonclient.DefaultAttemptTimeout, clientOpts)
	if code != ExitOK {
		return code
	}
	// Idempotent per (op, state) on the daemon (spec §8.3), so a replay is safe.
	var op team.RelayOp
	if _, err := client.Do(ctx, http.MethodPost, "/api/relay/ops/"+opID+"/report", req, &op, daemonclient.Idempotent()); err != nil {
		return relayReportErr(err, stdout, stderr)
	}
	return relayPrintJSON(stdout, stderr, op)
}

func runRelayOp(ctx context.Context, args []string, stdout, stderr io.Writer, clientOpts []daemonclient.Option) int {
	if len(args) == 0 || strings.TrimSpace(args[0]) == "" || strings.HasPrefix(args[0], "-") {
		return relayUsageErr(stderr, "需要一個 op id")
	}
	id := args[0]
	fs := flag.NewFlagSet("pdx relay op", flag.ContinueOnError)
	cfgPath, ok := relayFlags(fs, args[1:], stderr)
	if !ok {
		return ExitUsage
	}
	if fs.NArg() != 0 {
		return relayUsageErr(stderr, fmt.Sprintf("unexpected argument %q", fs.Arg(0)))
	}
	client, code := relayClient(cfgPath, stderr, daemonclient.DefaultAttemptTimeout, clientOpts)
	if code != ExitOK {
		return code
	}
	var op team.RelayOp
	if _, err := client.Do(ctx, http.MethodGet, "/api/relay/ops/"+id, nil, &op); err != nil {
		return relayReportErr(err, stdout, stderr)
	}
	return relayPrintJSON(stdout, stderr, op)
}

// runRelayPrompts prints this host's relay prompts — the effective bodies,
// the defaults, the fixed parts and the variables — as one JSON line (spec
// §8.8). The mod calls it before each write, fix and seed prompt and falls
// back to its own copy on any non-zero exit: 20 unreachable, 21 a daemon
// from before P9a, 1 otherwise.
func runRelayPrompts(ctx context.Context, args []string, stdout, stderr io.Writer, clientOpts []daemonclient.Option) int {
	fs := flag.NewFlagSet("pdx relay prompts", flag.ContinueOnError)
	cfgPath, ok := relayFlags(fs, args, stderr)
	if !ok {
		return ExitUsage
	}
	if fs.NArg() != 0 {
		return relayUsageErr(stderr, fmt.Sprintf("unexpected argument %q", fs.Arg(0)))
	}
	client, code := relayClient(cfgPath, stderr, daemonclient.DefaultAttemptTimeout, clientOpts)
	if code != ExitOK {
		return code
	}
	var p team.RelayPrompts
	if _, err := client.Do(ctx, http.MethodGet, "/api/relay/prompts", nil, &p); err != nil {
		return relayReportErr(err, stdout, stderr)
	}
	return relayPrintJSON(stdout, stderr, p)
}
