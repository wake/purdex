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
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/google/uuid"

	"github.com/wake/purdex/cmd/pdx/daemonclient"
	"github.com/wake/purdex/internal/config"
	"github.com/wake/purdex/internal/team"
)

// leadUsage is the grammar-rejection message for `pdx lead` (exit 2).
const leadUsage = "usage: pdx lead request --reason <text> [--name <team name>] [--label <短名>] [--max-members N] [--root <dir>]... [--wait 9m] [--config <path>]\n" +
	"       (--max-members 1..8, default 3; --wait up to 10m, default 9m so the call fits one Bash timeout;\n" +
	"        --label: the tab group's short name, about five Chinese characters / 10 columns, e.g. \"A 線\"; --name is the longer one for the team panel)"

const (
	// leadAttemptTimeout bounds every single request the client makes: 25 s
	// of daemon-side long-poll (team.MaxPollWaitS) plus room. It is the
	// client's per-attempt timeout rather than a per-call context deadline,
	// because the client only reports a silent daemon as ErrNoAnswer when
	// the caller's ctx carries no deadline; a restart inside one poll is
	// still bounded by the client's hard 30 s grace.
	leadAttemptTimeout = 35 * time.Second
	// leadCancelTimeout is the best-effort DELETE's budget (spec §6.1 step 5).
	leadCancelTimeout = 3 * time.Second
	// leadMaxHungPolls is how many consecutive polls may end without any
	// answer before the daemon counts as unavailable (spec §9.1).
	leadMaxHungPolls = 3
)

// runLead is the `pdx lead` switch target. SIGINT and SIGTERM cancel ctx,
// which runLeadCmd turns into a DELETE and exit 12. stop is handed in as
// onCancelled so the first signal restores default handling before the
// DELETE: a second Ctrl-C then terminates the process instead of being
// swallowed for the DELETE's 3 s.
func runLead(args []string) {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	os.Exit(runLeadCmd(ctx, args, os.Getenv, os.Stdout, os.Stderr, uuid.NewString, stop))
}

// leadRefusalCodes are the team-rule refusals of spec §14 that a create can
// answer with (409): all of them are ExitRefused. Later rules this command
// cannot receive are not listed; an unknown code stays ExitError.
var leadRefusalCodes = map[string]bool{
	team.ErrRequestOpen:      true,
	team.ErrAlreadyLead:      true,
	team.ErrMemberCannotLead: true,
}

// leadRequestArgs is the parsed, validated `pdx lead request` invocation.
type leadRequestArgs struct {
	cfgPath    string
	reason     string
	teamName   string // normalised (team.NormaliseTeamName); "" = no name
	teamLabel  string // normalised (team.NormaliseTeamLabel); "" = none requested (the daemon derives one from the name)
	maxMembers int
	roots      []string
	wait       time.Duration
}

// stringList is a repeatable string flag (--root a --root b).
type stringList []string

func (s *stringList) String() string     { return strings.Join(*s, ",") }
func (s *stringList) Set(v string) error { *s = append(*s, v); return nil }

// parseLeadRequestArgs validates the grammar. ok=false means a usage line
// was written to stderr and the caller must exit 2 without loading config.
func parseLeadRequestArgs(args []string, stderr io.Writer) (leadRequestArgs, bool) {
	fs := flag.NewFlagSet("pdx lead request", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	var a leadRequestArgs
	var roots stringList
	fs.StringVar(&a.cfgPath, "config", "", "")
	fs.StringVar(&a.reason, "reason", "", "")
	var name, label string
	fs.StringVar(&name, "name", "", "")
	fs.StringVar(&label, "label", "", "")
	fs.IntVar(&a.maxMembers, "max-members", 0, "")
	fs.Var(&roots, "root", "")
	fs.DurationVar(&a.wait, "wait", time.Duration(team.DefaultWaitS)*time.Second, "")
	reject := func(msg string) (leadRequestArgs, bool) {
		fmt.Fprintf(stderr, "pdx lead: %s\n%s\n", msg, leadUsage)
		return a, false
	}
	if err := fs.Parse(args); err != nil {
		return reject(err.Error())
	}
	if fs.NArg() != 0 {
		return reject(fmt.Sprintf("unexpected argument %q", fs.Arg(0)))
	}
	if strings.TrimSpace(a.reason) == "" {
		return reject("--reason 不能為空")
	}
	// D-N2: the daemon is the authority, but a bad name is refused here
	// before any HTTP call; blank means "no name".
	var err error
	if a.teamName, err = team.NormaliseTeamName(name); err != nil {
		return reject(fmt.Sprintf("--name 無效：%v", err))
	}
	// The label has a width rule of its own (about five Chinese characters):
	// refused here too, with the rule spelt out, before any HTTP call.
	if a.teamLabel, err = team.NormaliseTeamLabel(label); err != nil {
		return reject(fmt.Sprintf("--label 無效：%v", err))
	}
	// 0 is "not given": the daemon applies its default (team.DefaultMaxMembers).
	if a.maxMembers < 0 || a.maxMembers > team.MaxMaxMembers {
		return reject(fmt.Sprintf("--max-members 必須在 1 到 %d 之間", team.MaxMaxMembers))
	}
	if a.wait <= 0 || a.wait > time.Duration(team.MaxWaitS)*time.Second {
		return reject(fmt.Sprintf("--wait 必須大於 0 且不超過 %ds", team.MaxWaitS))
	}
	// wait_s is whole seconds and omitted when 0, so a sub-second --wait
	// would silently become the daemon's default.
	if a.wait < time.Second {
		return reject("--wait must be at least 1s")
	}
	for _, r := range roots {
		abs, err := filepath.Abs(r)
		if err != nil {
			return reject(fmt.Sprintf("--root %q: %v", r, err))
		}
		a.roots = append(a.roots, filepath.Clean(abs))
	}
	return a, true
}

// runLeadCmd implements `pdx lead request` (spec §6.1) and returns the exit
// code (spec §14). Grammar rejections return 2 before any config load or
// request. newID makes the request id; onCancelled (may be nil) runs once
// when ctx is cancelled, before the best-effort DELETE; clientOpts are
// appended to the daemonclient options so tests can inject a fake clock and
// transport.
func runLeadCmd(ctx context.Context, args []string, getenv func(string) string, stdout, stderr io.Writer,
	newID func() string, onCancelled func(), clientOpts ...daemonclient.Option) int {
	if len(args) == 0 || args[0] != "request" {
		fmt.Fprintln(stderr, leadUsage)
		return ExitUsage
	}
	a, ok := parseLeadRequestArgs(args[1:], stderr)
	if !ok {
		return ExitUsage
	}

	inbox := getenv("CLAUDE_CODE_MESSAGING_SOCKET")
	if inbox == "" {
		fmt.Fprintln(stderr, "pdx lead: CLAUDE_CODE_MESSAGING_SOCKET is unset — run inside a Claude Code session")
		return ExitError
	}
	cfg, err := config.Load(a.cfgPath)
	if err != nil {
		fmt.Fprintf(stderr, "pdx lead: %v\n", err)
		return ExitError
	}
	base := fmt.Sprintf("http://%s:%d", resolveDaemonHost(cfg.Bind), cfg.Port)
	opts := append([]daemonclient.Option{
		daemonclient.WithStderr(stderr),
		daemonclient.WithAttemptTimeout(leadAttemptTimeout),
	}, clientOpts...)
	client := daemonclient.New(base, cfg.Token, opts...)

	id := newID()
	fmt.Fprintf(stderr, "申請 lead 中（%s），請在 Purdex 介面核准；這個呼叫必須在前景等待（Bash timeout 600000）\n", id)

	create := team.CreateApprovalRequest{
		ID:          id,
		Kind:        team.KindLead,
		OriginInbox: inbox,
		Reason:      a.reason,
		TeamName:    a.teamName,
		TeamLabel:   a.teamLabel,
		MaxMembers:  a.maxMembers,
		Roots:       a.roots,
		WaitS:       int(a.wait / time.Second),
	}
	var ap team.Approval
	// The id is a client UUID and the daemon's create is idempotent on it,
	// so a POST whose connection dropped after it went out may be replayed.
	_, err = client.Do(ctx, http.MethodPost, "/api/team/approvals", create, &ap, daemonclient.Idempotent())
	if err != nil {
		if ctx.Err() != nil {
			// The signal arrived mid-create; the row may exist, so cancel it.
			return leadCancel(client, id, stderr, onCancelled)
		}
		return leadReportErr(err, stderr)
	}

	// The hard lock (spec §6.6): while the request is open this session's
	// PreToolUse hooks ask the daemon, which denies them. The flag is the
	// gate the hook checks before calling; it goes up right after the
	// daemon confirmed the row and comes down on every exit path below —
	// approval, denial, timeout, the signal path through leadCancel, every
	// error. The session id is the daemon's attribution of this caller
	// (Approval.origin.session_id): the CLI knows only its inbox. A SIGKILL
	// skips the defer; the daemon then removes the flag with its first {}.
	// The flag carries this request's id so that the defer never lowers a
	// flag a later request of the same session has raised (hooklock.go).
	if lock := team.HookLockPath(cfg.DataDir, team.HookAgentCC, ap.Origin.SessionID); lock == "" {
		fmt.Fprintln(stderr, "pdx lead: 無法建立硬鎖旗標（data_dir 或 session id 為空），這次只有軟鎖")
	} else {
		writeHookLock(lock, id, stderr)
		defer removeHookLock(lock, id)
	}

	hung := 0
	for ap.State == team.StateOpen {
		if ctx.Err() != nil {
			return leadCancel(client, id, stderr, onCancelled)
		}
		var polled team.Approval
		_, err := client.Do(ctx, http.MethodGet,
			fmt.Sprintf("/api/team/approvals/%s?wait=%d", id, team.MaxPollWaitS), nil, &polled)
		if err != nil {
			if ctx.Err() != nil {
				return leadCancel(client, id, stderr, onCancelled)
			}
			if errors.Is(err, daemonclient.ErrNoAnswer) || errors.Is(err, context.DeadlineExceeded) {
				// The poll ran out its own timeout with no answer at all.
				hung++
				if hung >= leadMaxHungPolls {
					fmt.Fprintln(stderr, "pdx lead: daemon 沒有回應")
					return ExitUnavailable
				}
				continue
			}
			return leadReportErr(err, stderr)
		}
		hung = 0
		ap = polled
	}
	return leadFinish(ap, stdout, stderr)
}

// leadCancel is spec §6.1 step 5: best-effort DELETE under a fresh 3 s
// context with no retry, then exit 12. The parent ctx is already done, so
// the DELETE gets its own. onCancelled runs first so signal handling is back
// to default while the DELETE is in flight.
func leadCancel(client *daemonclient.Client, id string, stderr io.Writer, onCancelled func()) int {
	if onCancelled != nil {
		onCancelled()
	}
	dctx, cancel := context.WithTimeout(context.Background(), leadCancelTimeout)
	defer cancel()
	if _, err := client.Once(dctx, http.MethodDelete, "/api/team/approvals/"+id, nil, nil); err != nil {
		fmt.Fprintf(stderr, "pdx lead: 取消申請時 daemon 回應：%v\n", err)
	}
	fmt.Fprintf(stderr, "pdx lead: 已取消申請（%s）\n", id)
	return ExitCancelled
}

// leadReportErr maps a client error to stderr text and an exit code.
func leadReportErr(err error, stderr io.Writer) int {
	var se *daemonclient.StatusError
	switch {
	case errors.Is(err, daemonclient.ErrUnavailable):
		fmt.Fprintln(stderr, "pdx lead: daemon_unavailable — 等了 30 秒 daemon 仍沒有回應")
		return ExitUnavailable
	case errors.Is(err, daemonclient.ErrUnsupported):
		fmt.Fprintln(stderr, "pdx lead: unsupported — 這個 daemon 沒有 /api/team 路由，請先更新 daemon")
		return ExitUnsupported
	case errors.As(err, &se) && leadRefusalCodes[se.API.Error]:
		if se.API.Error == team.ErrRequestOpen {
			openID := ""
			if se.API.Approval != nil {
				openID = sanitizeCell(se.API.Approval.ID)
			}
			fmt.Fprintf(stderr, "pdx lead: request_open — 已有一筆申請等待核准（%s）\n", openID)
			return ExitRefused
		}
		fmt.Fprintf(stderr, "pdx lead: %s — team 規則拒絕這筆申請\n", se.API.Error)
		return ExitRefused
	default:
		fmt.Fprintf(stderr, "pdx lead: %v\n", err)
		return ExitError
	}
}

// leadGrantOutput is what an approved request prints on stdout, one JSON
// line (spec §6.1 step 4). The approval creates the team in the same
// transaction and the team's id is the request's id (plan v3 deviation 1),
// so TeamID needs no second call.
type leadGrantOutput struct {
	RequestID string      `json:"request_id"`
	TeamID    string      `json:"team_id,omitempty"`
	Grant     *team.Grant `json:"grant"`
}

// leadFinish maps a closed Approval to output and exit code (spec §14).
func leadFinish(ap team.Approval, stdout, stderr io.Writer) int {
	switch ap.State {
	case team.StateApproved:
		grant := ap.Grant
		if grant == nil {
			var p team.LeadPayload
			if json.Unmarshal(ap.Payload, &p) == nil {
				grant = &team.Grant{MaxMembers: p.MaxMembers, Roots: p.Roots}
				if p.TeamName != "" {
					name := p.TeamName
					grant.TeamName = &name
				}
				if p.TeamLabel != "" {
					label := p.TeamLabel
					grant.TeamLabel = &label
				}
			}
		}
		out, err := json.Marshal(leadGrantOutput{RequestID: ap.ID, TeamID: ap.ID, Grant: grant})
		if err != nil {
			fmt.Fprintf(stderr, "pdx lead: %v\n", err)
			return ExitError
		}
		fmt.Fprintln(stdout, string(out))
		// U20 (b): the new lead is told to choose each member's model. It
		// goes to stderr, so stdout stays the grant JSON alone.
		fmt.Fprintln(stderr, team.ReminderAtActivation)
		return ExitOK
	case team.StateDenied:
		fmt.Fprintf(stderr, "pdx lead: 申請已被拒絕%s\n", leadDecidedBy(ap))
		return ExitDenied
	case team.StateTimeout:
		fmt.Fprintln(stderr, "pdx lead: 申請逾時，視同拒絕")
		return ExitTimeout
	case team.StateCancelled:
		fmt.Fprintln(stderr, "pdx lead: 申請已取消")
		return ExitCancelled
	case team.StateAbandoned:
		fmt.Fprintln(stderr, "pdx lead: 申請已失效（lease 到期或來源 session 已結束）")
		return ExitCancelled
	default:
		fmt.Fprintf(stderr, "pdx lead: 未知狀態 %q\n", sanitizeCell(string(ap.State)))
		return ExitError
	}
}

// leadDecidedBy renders "（由 <label> 處理）" when the daemon says who decided.
func leadDecidedBy(ap team.Approval) string {
	if ap.DecidedBy == nil || ap.DecidedBy.Label == "" {
		return ""
	}
	return fmt.Sprintf("（由 %s 處理）", sanitizeCell(ap.DecidedBy.Label))
}
