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
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/google/uuid"

	"github.com/wake/purdex/cmd/pdx/daemonclient"
	ipeers "github.com/wake/purdex/internal/peers"
	"github.com/wake/purdex/internal/team"
)

// `pdx adopt` and `pdx release` (adopt plan PL-1e, adopt spec D-U24-2 / D-U24-3): the lead takes a running session
// of this host in as a member after a click in Purdex.app (or at once in 無人值守模式), and lets one go again.

const adoptUsage = "usage: pdx adopt <ref|session-id|address> [--wait 9m] [--config <path>]\n" +
	"       (<ref>: _xxxxxx or xxxxxx; address: <host>/_xxxxxx or \"<host>/<name> [xxxxxx]\"; --wait up to 10m, default 9m so the call fits one Bash timeout)"

const releaseUsage = "usage: pdx release <ref> [--config <path>]"

// runAdopt is the `pdx adopt` switch target; SIGINT and SIGTERM cancel ctx, which runAdoptCmd turns into a
// DELETE and exit 12 (as runLead).
func runAdopt(args []string) {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	os.Exit(runAdoptCmd(ctx, args, os.Getenv, os.Stdout, os.Stderr, uuid.NewString, stop))
}

func runRelease(args []string) {
	os.Exit(runReleaseCmd(context.Background(), args, os.Getenv, os.Stdout, os.Stderr))
}

// adoptArgs is a parsed, validated `pdx adopt`.
type adoptArgs struct {
	cfgPath string
	target  string
	wait    time.Duration
}

// validAdoptTarget is the grammar of a target, checked before anything is read or asked: a ref ("_xxxxxx" or
// "xxxxxx"), a session id (UUID), "<host>/" followed by either, or "<host>/<name> [xxxxxx]". A bare name is
// not a target (the ref decides which conversation).
func validAdoptTarget(t string) bool {
	t = strings.TrimSpace(t)
	if t == "" {
		return false
	}
	ref := func(s string) bool {
		if !strings.HasPrefix(s, "_") {
			s = "_" + s
		}
		return ipeers.IsRef(s)
	}
	isUUID := func(s string) bool { _, err := uuid.Parse(s); return err == nil && len(s) == 36 }
	host, sess, qualified := ipeers.SplitAddress(t)
	if !qualified {
		return ref(t) || isUUID(t)
	}
	if host == "" {
		return false
	}
	if i := strings.LastIndex(sess, " ["); i > 0 && strings.HasSuffix(sess, "]") {
		return ref(sess[i+2:len(sess)-1]) && ipeers.RoutableName(sess[:i])
	}
	return ref(sess) || isUUID(sess)
}

// parseAdoptArgs validates the grammar. ok=false: a usage line was written (exit 2, before any config load).
func parseAdoptArgs(args []string, stderr io.Writer) (adoptArgs, bool) {
	fs := flag.NewFlagSet("pdx adopt", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	var a adoptArgs
	fs.StringVar(&a.cfgPath, "config", "", "")
	fs.DurationVar(&a.wait, "wait", time.Duration(team.DefaultWaitS)*time.Second, "")
	reject := func(msg string) (adoptArgs, bool) {
		fmt.Fprintf(stderr, "pdx adopt: %s\n%s\n", msg, adoptUsage)
		return a, false
	}
	pos, err := parseTeamFlags(fs, args)
	if err != nil {
		return reject(err.Error())
	}
	if len(pos) != 1 {
		return reject("需要剛好一個 <ref>")
	}
	if !validAdoptTarget(pos[0]) {
		return reject(fmt.Sprintf("%q 不是 ref（_xxxxxx）、session id 或帶 ref 的地址", pos[0]))
	}
	a.target = strings.TrimSpace(pos[0])
	if a.wait <= 0 || a.wait > time.Duration(team.MaxWaitS)*time.Second {
		return reject(fmt.Sprintf("--wait 必須大於 0 且不超過 %ds", team.MaxWaitS))
	}
	if a.wait < time.Second {
		return reject("--wait must be at least 1s")
	}
	return a, true
}

// runAdoptCmd implements `pdx adopt` and returns the exit code (spec §14): 0 approved (one JSON line on
// stdout), 10 denied, 11 timeout, 12 cancelled or abandoned, 13 refused by a team rule (code last on
// stderr), 20/21 the daemon. newID makes the request id; onCancelled runs once when ctx is cancelled,
// before the best-effort DELETE.
func runAdoptCmd(ctx context.Context, args []string, getenv func(string) string, stdout, stderr io.Writer,
	newID func() string, onCancelled func(), clientOpts ...daemonclient.Option) int {
	a, ok := parseAdoptArgs(args, stderr)
	if !ok {
		return ExitUsage
	}
	client, inbox, ok := teamSetup("adopt", a.cfgPath, getenv, stderr, append([]daemonclient.Option{daemonclient.WithAttemptTimeout(leadAttemptTimeout)}, clientOpts...))
	if !ok {
		return ExitError
	}
	id := newID()
	fmt.Fprintf(stderr, "申請納入 %s 中（%s），請在 Purdex 介面核准；這個呼叫必須在前景等待（Bash timeout 600000）\n", sanitizeCell(a.target), id)
	create := team.CreateApprovalRequest{ID: id, Kind: team.KindAdopt, OriginInbox: inbox, Target: a.target, WaitS: int(a.wait / time.Second)}
	var ap team.Approval
	// The id is a client UUID and the daemon's create is idempotent on it, so a dropped POST may be replayed.
	if _, err := client.Do(ctx, http.MethodPost, "/api/team/approvals", create, &ap, daemonclient.Idempotent()); err != nil {
		if ctx.Err() != nil {
			return leadCancel(client, id, stderr, onCancelled)
		}
		return adoptReportErr(err, stderr)
	}
	hung := 0
	for ap.State == team.StateOpen {
		if ctx.Err() != nil {
			return leadCancel(client, id, stderr, onCancelled)
		}
		var polled team.Approval
		_, err := client.Do(ctx, http.MethodGet, fmt.Sprintf("/api/team/approvals/%s?wait=%d", id, team.MaxPollWaitS), nil, &polled)
		if err != nil {
			if ctx.Err() != nil {
				return leadCancel(client, id, stderr, onCancelled)
			}
			if errors.Is(err, daemonclient.ErrNoAnswer) || errors.Is(err, context.DeadlineExceeded) {
				hung++
				if hung >= leadMaxHungPolls {
					fmt.Fprintln(stderr, "pdx adopt: daemon 沒有回應")
					return ExitUnavailable
				}
				continue
			}
			return adoptReportErr(err, stderr)
		}
		hung = 0
		ap = polled
	}
	return adoptFinish(ap, stdout, stderr)
}

// adoptReportErr maps a client error of the create or a poll: the shared team table (exit 13 for a rule),
// plus the hint for a ref two sessions share.
func adoptReportErr(err error, stderr io.Writer) int {
	var se *daemonclient.StatusError
	if errors.As(err, &se) && se.API.Error == team.ErrAdoptTargetAmbiguous {
		// Before the standard line: the code stays the last word on stderr (the machine-readable contract).
		fmt.Fprintln(stderr, "pdx adopt: 兩個 session 用了同一個 ref，請改用 session id（pdx peers 看得到）")
	}
	return teamReportErr("adopt", err, stderr)
}

// adoptOutput is what an approved adoption prints on stdout, one JSON line.
type adoptOutput struct {
	RequestID string `json:"request_id"`
	TeamID    string `json:"team_id"`
	Ref       string `json:"ref"`
	Address   string `json:"address"`
	SessionID string `json:"session_id"`
}

// adoptFinish maps a closed Approval to output and exit code.
func adoptFinish(ap team.Approval, stdout, stderr io.Writer) int {
	switch ap.State {
	case team.StateApproved:
		p, err := team.AdoptPayloadOf(ap)
		if err != nil {
			fmt.Fprintf(stderr, "pdx adopt: daemon 回了無法辨識的申請： %v invalid_response\n", err)
			return ExitError
		}
		out, err := json.Marshal(adoptOutput{RequestID: ap.ID, TeamID: p.TeamID, Ref: p.TargetRef, Address: p.TargetAddress, SessionID: p.TargetSessionID})
		if err != nil {
			fmt.Fprintf(stderr, "pdx adopt: %v\n", err)
			return ExitError
		}
		fmt.Fprintln(stdout, string(out))
		return ExitOK
	case team.StateDenied:
		fmt.Fprintf(stderr, "pdx adopt: 申請已被拒絕%s\n", leadDecidedBy(ap))
		return ExitDenied
	case team.StateTimeout:
		fmt.Fprintln(stderr, "pdx adopt: 申請逾時，視同拒絕")
		return ExitTimeout
	case team.StateCancelled:
		if ap.CloseReason != "" { // cancelled by a re-check at the click: a team rule refused it
			fmt.Fprintf(stderr, "pdx adopt: 核准時被規則取消 %s\n", sanitizeCell(ap.CloseReason))
			return ExitRefused
		}
		fmt.Fprintln(stderr, "pdx adopt: 申請已取消")
		return ExitCancelled
	case team.StateAbandoned:
		fmt.Fprintln(stderr, "pdx adopt: 申請已失效（lease 到期或來源 session 已結束）")
		return ExitCancelled
	default:
		fmt.Fprintf(stderr, "pdx adopt: 未知狀態 %q\n", sanitizeCell(string(ap.State)))
		return ExitError
	}
}

// runReleaseCmd implements `pdx release <ref>` (D-U24-3): the member keeps running as an ordinary session.
// A replay is safe (a released member answers 200 again), so the POST is Idempotent.
func runReleaseCmd(ctx context.Context, args []string, getenv func(string) string, stdout, stderr io.Writer, clientOpts ...daemonclient.Option) int {
	fs := flag.NewFlagSet("pdx release", flag.ContinueOnError)
	cfgPath := fs.String("config", "", "")
	pos, err := parseTeamFlags(fs, args)
	if err == nil && (len(pos) != 1 || strings.TrimSpace(pos[0]) == "") {
		err = errors.New("需要剛好一個 <ref>（_xxxxxx 或 <host>/<name>）")
	}
	if err != nil {
		fmt.Fprintf(stderr, "pdx release: %v\n%s\n", err, releaseUsage)
		return ExitUsage
	}
	client, inbox, ok := teamSetup("release", *cfgPath, getenv, stderr, clientOpts)
	if !ok {
		return ExitError
	}
	var m team.Member
	if _, err := client.Do(ctx, http.MethodPost, "/api/team/release", team.ReleaseRequest{OriginInbox: inbox, Target: pos[0]}, &m, daemonclient.Idempotent()); err != nil {
		return teamReportErr("release", err, stderr)
	}
	out, err := json.Marshal(m)
	if err != nil {
		fmt.Fprintf(stderr, "pdx release: %v\n", err)
		return ExitError
	}
	fmt.Fprintln(stdout, string(out))
	return ExitOK
}
