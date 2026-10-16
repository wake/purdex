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
// and prints the op (its id is in it), exit 0. With --wait it follows the op: done → 0; failed member_unresponsive, member_gone,
// member_unseen, member_blocked or member_busy_timeout → 14 (the last three explained on stderr, #2439); any other failed → 1; cancelled{denied} → 10; cancelled{timeout} → 11; any other cancelled → 12;
// the bound reached while the op still awaits a person's approval (RQ-2: the member pool is spent) → 11, the op left
// as it is; the bound reached otherwise → the op on stdout, exit 0. The final op is on stdout.
func runRelayMember(ctx context.Context, target string, args []string, stdout, stderr io.Writer, clientOpts []daemonclient.Option) int {
	fs := flag.NewFlagSet("pdx relay", flag.ContinueOnError)
	wait := fs.Duration("wait", 0, "")
	deny := fs.Bool("deny", false, "")
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
	if *deny && *wait != 0 {
		return relayUsageErr(stderr, "--deny 不能搭配 --wait")
	}
	inbox := relayGetenv("CLAUDE_CODE_MESSAGING_SOCKET")
	if inbox == "" {
		fmt.Fprintln(stderr, "pdx relay: CLAUDE_CODE_MESSAGING_SOCKET is unset — run inside a Claude Code session")
		return ExitError
	}
	if *deny {
		return runRelayDeny(ctx, target, inbox, cfgPath, stdout, stderr, clientOpts)
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
	return followRelayOp(ctx, client, res.Op, *wait, stdout, stderr, func(op team.RelayOp) int { return finishRelayOp(op, stdout, stderr) })
}

// runRelayDeny is `pdx relay <ref> --deny`: the lead declines the member's open ask (RT-2b T21). Exit 0 with the ask's id on stdout;
// no open ask, not your member and the like are the refusals of every relay call, exit 13.
func runRelayDeny(ctx context.Context, target, inbox, cfgPath string, stdout, stderr io.Writer, clientOpts []daemonclient.Option) int {
	client, code := relayClient(cfgPath, stderr, daemonclient.DefaultAttemptTimeout, clientOpts)
	if code != ExitOK {
		return code
	}
	var res team.RelayDenyResponse
	// not Idempotent(): a replay after a lost response would find no open ask and report a deny that did succeed as no_open_ask
	if _, err := client.Do(ctx, http.MethodPost, "/api/team/relays/deny", team.RelayDenyRequest{OriginInbox: inbox, Target: target}, &res); err != nil {
		return relayReportErr(err, stdout, stderr)
	}
	fmt.Fprintf(stderr, "已拒絕 %s 的接力申請\n", target)
	return relayPrintJSON(stdout, stderr, res)
}

const awaitingApprovalLine = "等待核准：member 額度用完（無人值守）"

// followRelayOp long-polls the op until it is terminal or the bound passes (the --wait bound is a cancellation, not a
// context deadline: see runRelayWait).
func followRelayOp(ctx context.Context, client *daemonclient.Client, op team.RelayOp, wait time.Duration, stdout, stderr io.Writer, finish func(team.RelayOp) int) int {
	deadline, cancel := context.WithCancel(ctx)
	defer cancel()
	bound := time.AfterFunc(wait, cancel)
	defer bound.Stop()
	hung, told := 0, false
	progress := newRelayProgress(stderr, op)
	for {
		if op.State == team.RelayAwaitingApproval && !told {
			fmt.Fprintln(stderr, awaitingApprovalLine)
			told = true
		}
		if op.State.Terminal() {
			return finish(op)
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
					return finish(op)
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
		progress.report(polled)
		op = polled
	}
}

// relayNow is the clock of the progress lines; a var so tests can step it.
var relayNow = time.Now

// relayHeartbeat is how long a state may last without a line before one says the relay is still going.
const relayHeartbeat = time.Minute

// relayProgress prints, on stderr, one line per change of a --wait op (state, or the member's mod having seen the control
// message) with the seconds since the wait began, and a line every relayHeartbeat while a state lasts: the seed turn after
// `cleared` can run for many minutes, and a terminal that stays silent that long looks hung and gets interrupted (Aigora,
// 2026-10-11). Output only: the exit code and stdout are the op's, not this.
type relayProgress struct {
	w       io.Writer
	start   time.Time
	lastOut time.Time
	state   team.RelayState
	seen    bool
}

func newRelayProgress(w io.Writer, op team.RelayOp) *relayProgress {
	now := relayNow()
	return &relayProgress{w: w, start: now, lastOut: now, state: op.State, seen: op.SeenAt != 0}
}

func (p *relayProgress) report(op team.RelayOp) {
	now := relayNow()
	elapsed := int(now.Sub(p.start).Seconds())
	switch {
	case op.State != p.state:
		fmt.Fprintf(p.w, "接力 %s → %s（%s，%ds）\n", p.state, op.State, relayStateNote(op.State), elapsed)
		p.state, p.lastOut = op.State, now
	case op.SeenAt != 0 && !p.seen:
		fmt.Fprintf(p.w, "接力 %s：member 的 mod 已看到控制訊息（%ds）\n", op.State, elapsed)
		p.lastOut = now
	case now.Sub(p.lastOut) >= relayHeartbeat && !op.State.Terminal():
		fmt.Fprintf(p.w, "接力仍在進行（%s，%s，已 %ds）\n", op.State, relayStateNote(op.State), elapsed)
		p.lastOut = now
	}
	p.seen = p.seen || op.SeenAt != 0
}

// relayStateNote says what a state means to the lead.
func relayStateNote(s team.RelayState) string {
	switch s {
	case team.RelayAwaitingApproval:
		return "等待核准"
	case team.RelayRequested:
		return "已送出，等 member 接手"
	case team.RelayClaimed:
		return "member 已接手"
	case team.RelayWriting:
		return "member 正在寫交接檔"
	case team.RelayWritten:
		return "交接檔已寫好，清除中"
	case team.RelayCleared:
		return "已換新 session，等它的第一輪結束"
	case team.RelayDone:
		return "完成"
	case team.RelayFailed:
		return "失敗"
	case team.RelayCancelled:
		return "已取消"
	}
	return string(s)
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
		switch op.Reason {
		case team.RelayReasonMemberUnresponsive, team.RelayReasonMemberGone:
			return ExitMemberFailed
		case team.RelayReasonMemberUnseen, team.RelayReasonMemberBlocked, team.RelayReasonMemberBusyTimeout:
			fmt.Fprintln(stderr, memberNoAnswerHint(op.Reason))
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

// memberNoAnswerHint says why a member's mod did not take the relay (#2439), so the lead knows what to look at.
func memberNoAnswerHint(reason string) string {
	switch reason {
	case team.RelayReasonMemberBlocked:
		return "pdx relay: member 正在等待輸入（權限或提示），它的 mod 沒有回應接力；請到它的分頁處理後再試 " + reason
	case team.RelayReasonMemberBusyTimeout:
		return "pdx relay: member 這一輪跑超過 60 分鐘仍未結束，接力沒有進行；等它的回合結束後再試 " + reason
	}
	return fmt.Sprintf("pdx relay: member 的 mod 在 %d 秒內沒有確認收到接力；它的終端機可能卡在對話框（例如「Mods: Enable hot reloading?」），"+
		"或 Claude Code 沒有在執行；請到它的分頁看看 %s", team.RelayClaimTimeoutS, reason)
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
