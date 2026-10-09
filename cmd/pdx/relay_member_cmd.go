package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/wake/purdex/cmd/pdx/daemonclient"
	"github.com/wake/purdex/internal/team"
)

// `pdx relay <ref> [--wait <dur>]`, `pdx relay claim` and `pdx relay seen` (plan v3 P6-5; spec §8.2, §8.3, §14).
// The first is the LEAD's: it asks the daemon to relay one of its members (POST /api/team/relays) and, with --wait,
// follows the op to its end. claim and seen are the member's mod's calls on the op's control message.

// relayGetenv is os.Getenv (a test seam): CLAUDE_CODE_MESSAGING_SOCKET names the lead's session to the daemon.
var relayGetenv = os.Getenv

// relayMaxWait is the longest --wait: it fits one foreground Bash call (timeout 600000), like `pdx lead request`.
const relayMaxWait = 9 * time.Minute

var (
	relayRefPattern  = regexp.MustCompile(`^_[0-9a-z]{6}$`)
	relayNamedTarget = regexp.MustCompile(`^.+ \[[0-9a-z]{6}\]$`)
)

// isMemberRelayTarget says whether the first argument names a member: a ref, an address (it holds "/") or the
// "name [xxxxxx]" form. Anything else that is not a known word is a usage error.
func isMemberRelayTarget(s string) bool {
	return relayRefPattern.MatchString(s) || strings.Contains(s, "/") || relayNamedTarget.MatchString(s)
}

// runRelayMember is `pdx relay <ref> [--wait <dur>]`. Without --wait it returns once the daemon has accepted the op
// and prints the op (its id is in it), exit 0. With --wait it follows the op: done → 0; failed member_unresponsive or
// member_gone → 14; any other failed → 1; cancelled{denied} → 10; cancelled{timeout} → 11; any other cancelled → 12;
// the bound reached while the op still awaits a person's approval (RQ-2: the member pool is spent) → 11, the op left
// as it is; the bound reached otherwise → the op on stdout, exit 0. The final op is on stdout.
func runRelayMember(ctx context.Context, target string, args []string, stdout, stderr io.Writer, clientOpts []daemonclient.Option) int {
	fs := flag.NewFlagSet("pdx relay", flag.ContinueOnError)
	wait := fs.Duration("wait", 0, "")
	cfgPath, ok := relayFlags(fs, args, stderr)
	if !ok {
		return ExitUsage
	}
	if fs.NArg() != 0 {
		return relayUsageErr(stderr, fmt.Sprintf("unexpected argument %q", fs.Arg(0)))
	}
	if *wait < 0 || *wait > relayMaxWait {
		return relayUsageErr(stderr, "--wait 必須介於 0 與 9m 之間")
	}
	inbox := relayGetenv("CLAUDE_CODE_MESSAGING_SOCKET")
	if inbox == "" {
		fmt.Fprintln(stderr, "pdx relay: CLAUDE_CODE_MESSAGING_SOCKET is unset — run inside a Claude Code session")
		return ExitError
	}
	attempt := daemonclient.DefaultAttemptTimeout
	if *wait > 0 {
		attempt = relayAttemptTimeout
	}
	client, code := relayClient(cfgPath, stderr, attempt, clientOpts)
	if code != ExitOK {
		return code
	}
	// The id is minted once, before the first attempt: a replay after a lost response is the same op (the daemon answers
	// an id it already opened with that op), never a second one.
	req := team.RelayCreateRequest{ID: relayNewID(), OriginInbox: inbox, Target: target}
	var res team.RelayCreateResponse
	if _, err := client.Do(ctx, http.MethodPost, "/api/team/relays", req, &res, daemonclient.Idempotent()); err != nil {
		return relayReportErr(err, stdout, stderr)
	}
	fmt.Fprintf(stderr, "接力已送出（%s）\n", res.Op.ID)
	if *wait == 0 {
		return relayPrintJSON(stdout, stderr, res.Op)
	}
	return followRelayOp(ctx, client, res.Op, *wait, stdout, stderr)
}

const awaitingApprovalLine = "等待核准：member 額度用完（無人值守）"

// followRelayOp long-polls the op until it is terminal or the bound passes (the --wait bound is a cancellation, not a
// context deadline: see runRelayWait).
func followRelayOp(ctx context.Context, client *daemonclient.Client, op team.RelayOp, wait time.Duration, stdout, stderr io.Writer) int {
	deadline, cancel := context.WithCancel(ctx)
	defer cancel()
	bound := time.AfterFunc(wait, cancel)
	defer bound.Stop()
	hung, told := 0, false
	for {
		if op.State == team.RelayAwaitingApproval && !told {
			fmt.Fprintln(stderr, awaitingApprovalLine)
			told = true
		}
		if op.State.Terminal() {
			return finishRelayOp(op, stdout, stderr)
		}
		if ctx.Err() != nil {
			fmt.Fprintln(stderr, "pdx relay: 等待已中斷，接力仍在進行")
			return ExitCancelled
		}
		if deadline.Err() != nil {
			// The bound fired, maybe while a terminal answer was in flight: read once more, short, so the outcome is the
			// daemon's state and not the scheduler's.
			fctx, fcancel := context.WithTimeout(ctx, relayFinalReadTimeout)
			var final team.RelayOp
			_, ferr := client.Do(fctx, http.MethodGet, "/api/relay/ops/"+op.ID+"?wait=0", nil, &final)
			fcancel()
			if ferr == nil && final.ID != "" {
				op = final
				if op.State.Terminal() {
					return finishRelayOp(op, stdout, stderr)
				}
			}
			if op.State == team.RelayAwaitingApproval {
				fmt.Fprintln(stderr, "pdx relay: --wait 已到，接力仍在等待核准（留著；核准後 member 仍會接力）")
				relayPrintJSON(stdout, stderr, op)
				return ExitTimeout
			}
			fmt.Fprintln(stderr, "pdx relay: --wait 已到，接力仍在進行")
			return relayPrintJSON(stdout, stderr, op)
		}
		var polled team.RelayOp
		_, err := client.Do(deadline, http.MethodGet, fmt.Sprintf("/api/relay/ops/%s?wait=%d", op.ID, team.MaxPollWaitS), nil, &polled)
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
		op = polled
	}
}

// finishRelayOp prints the terminal op and maps it to the exit code.
func finishRelayOp(op team.RelayOp, stdout, stderr io.Writer) int {
	if code := relayPrintJSON(stdout, stderr, op); code != ExitOK {
		return code
	}
	switch op.State {
	case team.RelayDone:
		return ExitOK
	case team.RelayFailed:
		if op.Reason == team.RelayReasonMemberUnresponsive || op.Reason == team.RelayReasonMemberGone {
			return ExitMemberFailed
		}
		return ExitError
	default: // cancelled
		switch op.Reason {
		case team.RelayReasonDenied:
			return ExitDenied
		case team.RelayReasonTimeout:
			return ExitTimeout
		}
		return ExitCancelled
	}
}

// runRelayClaim is `pdx relay claim <op> --session <sid>`: the member's mod takes the op. stdout is the claim JSON
// (the op and the lead); not_your_op and bad_transition are exit 13 (bad_transition with the op on stdout).
func runRelayClaim(ctx context.Context, args []string, stdout, stderr io.Writer, clientOpts []daemonclient.Option) int {
	return runRelayOpPost(ctx, "claim", args, stdout, stderr, clientOpts, func(sid string) any { return team.RelayClaimRequest{SessionID: sid} }, func() any { return &team.RelayClaimResponse{} })
}

// runRelaySeen is `pdx relay seen <op> --session <sid>`: the mod saw the control message (it may claim later). stdout is
// the op JSON.
func runRelaySeen(ctx context.Context, args []string, stdout, stderr io.Writer, clientOpts []daemonclient.Option) int {
	return runRelayOpPost(ctx, "seen", args, stdout, stderr, clientOpts, func(sid string) any { return team.RelaySeenRequest{SessionID: sid} }, func() any { return &team.RelayOp{} })
}

func runRelayOpPost(ctx context.Context, verb string, args []string, stdout, stderr io.Writer, clientOpts []daemonclient.Option, body func(string) any, out func() any) int {
	if len(args) == 0 || strings.TrimSpace(args[0]) == "" || strings.HasPrefix(args[0], "-") {
		return relayUsageErr(stderr, "需要 <op>")
	}
	opID := args[0]
	fs := flag.NewFlagSet("pdx relay "+verb, flag.ContinueOnError)
	var sid string
	fs.StringVar(&sid, "session", "", "")
	cfgPath, ok := relayFlags(fs, args[1:], stderr)
	if !ok {
		return ExitUsage
	}
	if fs.NArg() != 0 || strings.TrimSpace(sid) == "" || !relayOpIDPattern.MatchString(opID) {
		return relayUsageErr(stderr, "需要 <op> 與 --session")
	}
	client, code := relayClient(cfgPath, stderr, daemonclient.DefaultAttemptTimeout, clientOpts)
	if code != ExitOK {
		return code
	}
	res := out()
	// claim is a compare-and-set and seen sets seen_at once: a replay is safe either way.
	if _, err := client.Do(ctx, http.MethodPost, "/api/relay/ops/"+opID+"/"+verb, body(sid), res, daemonclient.Idempotent()); err != nil {
		return relayReportErr(err, stdout, stderr)
	}
	return relayPrintJSON(stdout, stderr, res)
}
