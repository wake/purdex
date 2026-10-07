package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"time"

	"github.com/wake/purdex/cmd/pdx/daemonclient"
	"github.com/wake/purdex/internal/config"
	"github.com/wake/purdex/internal/team"
)

// `pdx ask` is the Purdex mod's side of 分流 (spec §6.6, U19). The mod runs
// each subcommand through $.process.run with the daemon's answer on stdout:
//
//	pdx ask begin --session <sid> --tool-use <id> --kind hook_ask (--payload <json> | --payload-file <f>)
//	    → stdout {"id":…} exit 0; exit 13 on 409 no_responders (code on stderr); 20 / 21; 1 otherwise.
//	      A 409 ask_open adopts the open row: stdout {"id":<its id>} exit 0.
//	pdx ask wait <id>
//	    → one bounded round (≤ 9 min of GET ?wait=25 polls), stdout the daemon's
//	      {"state":"still_open"} | {"state":"answered_remote","hook":…} | {"state":"closed","reason":…}
//	      exit 0; a JSON 404 prints {"state":"closed","reason":"not_found"}; 20 / 21 only otherwise.
//	pdx ask report <id> <answered_local|dismissed> [--hook <json> | --hook-file <f>] [--detach]
//	    → stdout the Approval, exit 0; 1 / 20 / 21. --detach: the same report
//	      runs as a process of its own (setsid); this one prints nothing, exit 0.
const askUsage = "usage: pdx ask begin --session <sid> --tool-use <id> --kind hook_ask|hook_permission (--payload <json> | --payload-file <f>) [--config <path>]\n" +
	"       pdx ask wait <id> [--config <path>]\n" +
	"       pdx ask report <id> answered_local|dismissed [--hook <json> | --hook-file <f>] [--detach] [--config <path>]"

const (
	// askAttemptTimeout bounds one poll: 25 s of daemon-side wait plus room (as lead's).
	askAttemptTimeout = 35 * time.Second
	// askFinalReadTimeout bounds the one short read a round makes when its
	// bound fires (as relayFinalReadTimeout).
	askFinalReadTimeout = 5 * time.Second
	// askMaxHungPolls is lead's rule: three polls with no answer at all ⇒ 20.
	askMaxHungPolls = 3
)

// askWaitRound is how long one `pdx ask wait` keeps polling before it
// prints still_open: $.process.run is capped at ten minutes (M24), the mod
// gives it 590 s, so a round ends well inside that. A var only so tests can
// shorten the bound (relay's tests do it with --wait).
var askWaitRound = 9 * time.Minute

func runAsk(args []string) {
	os.Exit(runAskCmd(context.Background(), args, os.Stdout, os.Stderr, time.Now))
}

// askArgs is the parsed grammar of one `pdx ask` invocation.
type askArgs struct {
	verb    string
	cfgPath string
	// begin
	session, toolUse string
	kind             team.Kind
	payload          json.RawMessage
	// wait / report
	id    string
	state team.State
	hook  *team.HookDecision
	// report --detach: run the same report as a process of its own and exit
	detach bool
}

// jsonArg reads an inline JSON flag or a file flag (exactly one may be set).
func jsonArg(inline, file, what string) (json.RawMessage, error) {
	if inline != "" && file != "" {
		return nil, fmt.Errorf("give --%s or --%s-file, not both", what, what)
	}
	raw := []byte(inline)
	if file != "" {
		b, err := os.ReadFile(file)
		if err != nil {
			return nil, err
		}
		raw = b
	}
	if len(strings.TrimSpace(string(raw))) == 0 {
		return nil, nil
	}
	if !json.Valid(raw) {
		return nil, fmt.Errorf("--%s is not valid JSON", what)
	}
	return json.RawMessage(raw), nil
}

// parseAskArgs validates the grammar; ok=false means a usage line was
// written and the caller exits 2 before any config load.
func parseAskArgs(args []string, stderr io.Writer) (askArgs, bool) {
	var a askArgs
	reject := func(msg string) (askArgs, bool) {
		fmt.Fprintf(stderr, "pdx ask: %s\n%s\n", msg, askUsage)
		return a, false
	}
	if len(args) == 0 {
		return reject("a subcommand is required")
	}
	a.verb = args[0]
	fs := flag.NewFlagSet("pdx ask "+a.verb, flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	fs.StringVar(&a.cfgPath, "config", "", "")
	var kind, payload, payloadFile, hook, hookFile string
	switch a.verb {
	case "begin":
		fs.StringVar(&a.session, "session", "", "")
		fs.StringVar(&a.toolUse, "tool-use", "", "")
		fs.StringVar(&kind, "kind", string(team.KindHookAsk), "")
		fs.StringVar(&payload, "payload", "", "")
		fs.StringVar(&payloadFile, "payload-file", "", "")
	case "wait":
		// --config only (registered above): a stray --hook on wait is a
		// usage error, not a silently ignored flag.
	case "report":
		fs.StringVar(&hook, "hook", "", "")
		fs.StringVar(&hookFile, "hook-file", "", "")
		fs.BoolVar(&a.detach, "detach", false, "")
	default:
		return reject(fmt.Sprintf("unknown subcommand %q", a.verb))
	}
	// Positionals come first for wait/report (`pdx ask wait <id>`); flag
	// parses from the first flag on.
	rest := args[1:]
	var pos []string
	for len(rest) > 0 && !strings.HasPrefix(rest[0], "-") {
		pos = append(pos, rest[0])
		rest = rest[1:]
	}
	if err := fs.Parse(rest); err != nil {
		return reject(err.Error())
	}
	if fs.NArg() != 0 {
		return reject(fmt.Sprintf("unexpected argument %q", fs.Arg(0)))
	}
	switch a.verb {
	case "begin":
		if len(pos) != 0 {
			return reject(fmt.Sprintf("unexpected argument %q", pos[0]))
		}
		if a.session == "" || a.toolUse == "" {
			return reject("--session and --tool-use are required")
		}
		a.kind = team.Kind(kind)
		if !team.IsHookKind(a.kind) {
			return reject("--kind must be hook_ask or hook_permission")
		}
		p, err := jsonArg(payload, payloadFile, "payload")
		if err != nil {
			return reject(err.Error())
		}
		if p == nil {
			return reject("--payload or --payload-file is required")
		}
		a.payload = p
	case "wait":
		if len(pos) != 1 || pos[0] == "" {
			return reject("wait takes exactly one <id>")
		}
		a.id = pos[0]
	case "report":
		if len(pos) != 2 || pos[0] == "" {
			return reject("report takes <id> and <state>")
		}
		a.id, a.state = pos[0], team.State(pos[1])
		if a.state != team.StateAnsweredLocal && a.state != team.StateDismissed {
			return reject("state must be answered_local or dismissed")
		}
		h, err := jsonArg(hook, hookFile, "hook")
		if err != nil {
			return reject(err.Error())
		}
		if h != nil {
			a.hook = new(team.HookDecision)
			if err := json.Unmarshal(h, a.hook); err != nil {
				return reject("--hook: " + err.Error())
			}
		}
	}
	return a, true
}

// runAskCmd implements `pdx ask` and returns the exit code. now is the
// clock the wait round is measured on; clientOpts are appended for tests.
func runAskCmd(ctx context.Context, args []string, stdout, stderr io.Writer, now func() time.Time, clientOpts ...daemonclient.Option) int {
	a, ok := parseAskArgs(args, stderr)
	if !ok {
		return ExitUsage
	}
	if a.detach {
		// The mod's terminal answer must reach the daemon even when the
		// report outlives the hook that started it (P8a-2 R2: the engine may
		// end a hook's children once it returns, and a report lost after a
		// remote CAS would leave the card on the remote answer for good).
		// The same report runs as a setsid'd process of its own; this one
		// exits at once. A report is idempotent, so a duplicate is harmless.
		if err := startDetachedFn(append([]string{"ask"}, withoutDetach(args)...)); err != nil {
			fmt.Fprintf(stderr, "pdx ask: report --detach: %v\n", err)
			return ExitError
		}
		return ExitOK
	}
	cfg, err := config.Load(a.cfgPath)
	if err != nil {
		fmt.Fprintf(stderr, "pdx ask: %v\n", err)
		return ExitError
	}
	base := fmt.Sprintf("http://%s:%d", resolveDaemonHost(cfg.Bind), cfg.Port)
	opts := append([]daemonclient.Option{daemonclient.WithStderr(stderr), daemonclient.WithAttemptTimeout(askAttemptTimeout)}, clientOpts...)
	client := daemonclient.New(base, cfg.Token, opts...)
	switch a.verb {
	case "begin":
		return askBegin(ctx, client, a, stdout, stderr)
	case "wait":
		return askWait(ctx, client, a.id, stdout, stderr, now)
	default:
		return askReport(ctx, client, a, stdout, stderr)
	}
}

// withoutDetach drops every --detach / -detach / --detach=… flag; flag
// values (--hook's JSON) never start with "-", so they are never touched.
func withoutDetach(args []string) []string {
	out := make([]string, 0, len(args))
	for _, a := range args {
		if strings.HasPrefix(a, "-") && strings.TrimLeft(strings.SplitN(a, "=", 2)[0], "-") == "detach" {
			continue
		}
		out = append(out, a)
	}
	return out
}

// startDetachedFn starts `pdx <args>` detached; a var so tests can see the argv.
var startDetachedFn = func(args []string) error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	return startDetachedExe(exe, args)
}

// startDetachedExe starts exe in a session of its own (setsid), stdio on
// /dev/null, and does not wait: it outlives this process and whatever
// started it.
func startDetachedExe(exe string, args []string) error {
	devnull, err := os.OpenFile(os.DevNull, os.O_RDWR, 0)
	if err != nil {
		return err
	}
	defer devnull.Close()
	cmd := exec.Command(exe, args...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = devnull, devnull, devnull
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		return err
	}
	return cmd.Process.Release()
}

func askBegin(ctx context.Context, client *daemonclient.Client, a askArgs, stdout, stderr io.Writer) int {
	var out team.AskBeginResponse
	// Idempotent(): the daemon keeps one open row per (session, tool_use_id)
	// (Task 8a.2), so a replay after a lost response cannot open a second
	// row — it is answered 409 ask_open with the row the first send opened,
	// and that answer is a success below. Without the replay a drop after
	// the send would be ErrSentNoResponse → exit 1 → the mod runs the native
	// dialog alone while a row sits open for the phone.
	_, err := client.Do(ctx, http.MethodPost, "/api/ask/begin",
		team.AskBeginRequest{SessionID: a.session, ToolUseID: a.toolUse, Kind: a.kind, Payload: a.payload}, &out, daemonclient.Idempotent())
	if err != nil {
		var se *daemonclient.StatusError
		switch {
		case errors.As(err, &se) && se.API.Error == team.ErrAskOpen && se.API.Approval != nil:
			// The row for this tool use is already open (a replayed or retried
			// begin): adopt it — the same stdout as a 201.
			out.ID = se.API.Approval.ID
		case errors.As(err, &se) && se.API.Error == team.ErrNoResponders:
			// Same shape as pdx relay: `pdx ask: <detail> <code>`, the code
			// the LAST stderr token (the mod's stderrCode() reads it).
			fmt.Fprintf(stderr, "pdx ask: %s %s\n", sanitizeCell(se.API.Detail), team.ErrNoResponders)
			return ExitRefused
		default:
			return askReportErr(err, stderr)
		}
	}
	if out.ID == "" {
		// A 2xx or ask_open without an id (version skew, partial body) cannot
		// be waited on or reported: failing here sends the mod to the native
		// dialog alone instead of leaving a row nobody can close.
		fmt.Fprintln(stderr, "pdx ask: daemon 回應缺少 id invalid_response")
		return ExitError
	}
	b, _ := json.Marshal(out)
	fmt.Fprintln(stdout, string(b))
	return ExitOK
}

// validWaitState reports whether the daemon answered with a state the mod
// knows how to act on.
func validWaitState(s string) bool {
	return s == team.AskStillOpen || s == team.AskAnsweredRemote || s == team.AskClosed
}

// askWait is one round: polls until an answer, or until askWaitRound — on
// now() after an answered poll, and by a timer for a poll still in flight.
func askWait(ctx context.Context, client *daemonclient.Client, id string, stdout, stderr io.Writer, now func() time.Time) int {
	start := now()
	// The bound is a cancellation, not a context deadline (as pdx relay
	// wait, PR #1726): the client applies its per-attempt timeout
	// (ErrNoAnswer, counted below) only to a ctx without a deadline, so
	// three hung polls still end in 20 before the bound does.
	round, cancel := context.WithCancel(ctx)
	defer cancel()
	bound := time.AfterFunc(askWaitRound, cancel)
	defer bound.Stop()
	hung := 0
	for {
		if ctx.Err() != nil {
			return askReportErr(ctx.Err(), stderr)
		}
		if round.Err() != nil {
			// The bound fired, maybe with an answer in flight: one short read
			// (no long poll) under a fresh context decides; still_open when it
			// fails too, and the mod's next round finds out.
			fctx, fcancel := context.WithTimeout(ctx, askFinalReadTimeout)
			var w team.AskWaitResponse
			_, err := client.Do(fctx, http.MethodGet, fmt.Sprintf("/api/ask/wait/%s?wait=0", id), nil, &w)
			fcancel()
			if gone, ok := askRowGone(err); ok {
				return printJSON(stdout, gone)
			}
			if err != nil || !validWaitState(w.State) {
				w = team.AskWaitResponse{State: team.AskStillOpen}
			}
			return printJSON(stdout, w)
		}
		var w team.AskWaitResponse
		_, err := client.Do(round, http.MethodGet, fmt.Sprintf("/api/ask/wait/%s?wait=%d", id, team.MaxPollWaitS), nil, &w)
		if err != nil {
			if ctx.Err() != nil || round.Err() != nil {
				continue // the loop head says which one
			}
			if errors.Is(err, daemonclient.ErrNoAnswer) || errors.Is(err, context.DeadlineExceeded) {
				hung++
				if hung >= askMaxHungPolls {
					fmt.Fprintln(stderr, "pdx ask: daemon 沒有回應")
					return ExitUnavailable
				}
				continue
			}
			if gone, ok := askRowGone(err); ok {
				return printJSON(stdout, gone)
			}
			return askReportErr(err, stderr)
		}
		hung = 0
		if !validWaitState(w.State) {
			fmt.Fprintln(stderr, "pdx ask: daemon 回應的 state 無法辨識 invalid_response")
			return ExitError
		}
		if w.State != team.AskStillOpen || now().Sub(start) >= askWaitRound {
			return printJSON(stdout, w)
		}
	}
}

// askRowGone maps a JSON 404 (the row is gone: a reset team.db) to the
// closed answer that tells the mod to stop looping.
func askRowGone(err error) (team.AskWaitResponse, bool) {
	var se *daemonclient.StatusError
	if errors.As(err, &se) && se.Status == http.StatusNotFound {
		return team.AskWaitResponse{State: team.AskClosed, Reason: team.ErrNotFound}, true
	}
	return team.AskWaitResponse{}, false
}

func askReport(ctx context.Context, client *daemonclient.Client, a askArgs, stdout, stderr io.Writer) int {
	var ap team.Approval
	// A report is idempotent on the daemon (it answers the row as it is), so a replay is safe.
	_, err := client.Do(ctx, http.MethodPost, "/api/ask/report/"+a.id, team.AskReportRequest{State: a.state, Hook: a.hook}, &ap, daemonclient.Idempotent())
	if err != nil {
		return askReportErr(err, stderr)
	}
	return printJSON(stdout, ap)
}

func printJSON(stdout io.Writer, v any) int {
	b, err := json.Marshal(v)
	if err != nil {
		return ExitError
	}
	fmt.Fprintln(stdout, string(b))
	return ExitOK
}

// askReportErr maps a client error to stderr and an exit code (spec §14).
// Every line ends with the bare code (`pdx ask: <detail> <code>`), the
// shape pdx relay uses, so a mod can read the code as the last token.
func askReportErr(err error, stderr io.Writer) int {
	var se *daemonclient.StatusError
	switch {
	case errors.Is(err, daemonclient.ErrUnavailable):
		fmt.Fprintln(stderr, "pdx ask: 等了 30 秒 daemon 仍沒有回應 daemon_unavailable")
		return ExitUnavailable
	case errors.Is(err, daemonclient.ErrUnsupported):
		fmt.Fprintln(stderr, "pdx ask: 這個 daemon 沒有 /api/ask 路由，請先更新 daemon unsupported")
		return ExitUnsupported
	case errors.As(err, &se):
		fmt.Fprintf(stderr, "pdx ask: %s %s\n", sanitizeCell(se.API.Detail), sanitizeCell(se.API.Error))
		return ExitError
	default:
		fmt.Fprintf(stderr, "pdx ask: %v\n", err)
		return ExitError
	}
}
