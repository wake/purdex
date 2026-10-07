package main

// `pdx spawn`, `pdx kill` and `pdx team`: the lead's commands (lead-team-
// relay spec §7.2, §7.3, U20; plan v3 P4-7). Each runs inside the lead's
// Claude Code session, which the daemon attributes by its inbox. API errors
// print `pdx <cmd>: <detail> <code>`, the code last; exit codes are spec §14.

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/wake/purdex/cmd/pdx/daemonclient"
	"github.com/wake/purdex/internal/config"
	ipeers "github.com/wake/purdex/internal/peers"
	"github.com/wake/purdex/internal/team"
)

const spawnUsage = "usage: pdx spawn [--cwd <dir>] [--title <t>] [--model <m>] [--effort <e>] [--config <path>]\n" +
	"       (--cwd defaults to this directory; --effort is low, medium, high, xhigh or max;\n" +
	"        without --model the member runs this host's default model, which is not fixed)"

const (
	// teamAttemptTimeout bounds one request: a spawn POST waits up to
	// team.SpawnPollWaitS daemon-side, plus room (as lead's polls).
	teamAttemptTimeout = 35 * time.Second
	// teamMaxHungPolls: consecutive spawn POSTs with no answer at all before exit 20 (spec §9.1).
	teamMaxHungPolls = 3
	// spawnStartTimeoutHint is the stderr hint of member_start_timeout: a
	// member that never registers ran on a host without the Purdex hooks.
	spawnStartTimeoutHint = "member 沒有在 20 秒內啟動（這台主機需要 Purdex hooks：pdx setup --agent cc）"
)

// teamRefusalCodes are the team-rule refusals these commands can meet: exit
// 13 (spec §14). Any other API code is exit 1.
var teamRefusalCodes = map[string]bool{
	team.ErrNotLead:         true,
	team.ErrTeamFull:        true,
	team.ErrCwdOutsideGrant: true,
	team.ErrNotYourMember:   true,
}

// spawnNewID mints the spawn op id, the idempotency key of every POST of
// one spawn (a test seam).
var spawnNewID = uuid.NewString

// spawnSettleBound caps the whole wait for one spawn, below the Bash tool's
// 10-minute limit (as `pdx lead request --wait` 9m), so an op the daemon
// keeps answering running cannot hold the lead forever. A var only so tests
// can shorten it.
var spawnSettleBound = 9 * time.Minute

func runSpawn(args []string) {
	os.Exit(runSpawnCmd(context.Background(), args, os.Getenv, os.Stdout, os.Stderr))
}

// parseTeamFlags parses fs over args with flags and positionals in any
// order (`pdx kill <ref> --config c` and `pdx kill --config c <ref>`) and
// returns the positionals.
func parseTeamFlags(fs *flag.FlagSet, args []string) ([]string, error) {
	fs.SetOutput(io.Discard)
	var pos []string
	for {
		if err := fs.Parse(args); err != nil {
			return nil, err
		}
		if fs.NArg() == 0 {
			return pos, nil
		}
		pos = append(pos, fs.Arg(0))
		args = fs.Args()[1:]
	}
}

// teamSetup reads the caller's inbox, then the config, and builds the
// restart-aware client. ok=false means a line was written (exit 1).
func teamSetup(cmd, cfgPath string, getenv func(string) string, stderr io.Writer, clientOpts []daemonclient.Option) (*daemonclient.Client, string, bool) {
	inbox := getenv("CLAUDE_CODE_MESSAGING_SOCKET")
	if inbox == "" {
		fmt.Fprintf(stderr, "pdx %s: CLAUDE_CODE_MESSAGING_SOCKET is unset — run inside a Claude Code session\n", cmd)
		return nil, "", false
	}
	cfg, err := config.Load(cfgPath)
	if err != nil {
		fmt.Fprintf(stderr, "pdx %s: %v\n", cmd, err)
		return nil, "", false
	}
	base := fmt.Sprintf("http://%s:%d", resolveDaemonHost(cfg.Bind), cfg.Port)
	opts := append([]daemonclient.Option{daemonclient.WithStderr(stderr), daemonclient.WithAttemptTimeout(teamAttemptTimeout)}, clientOpts...)
	return daemonclient.New(base, cfg.Token, opts...), inbox, true
}

// teamReportErr maps a client error to one stderr line and an exit code.
func teamReportErr(cmd string, err error, stderr io.Writer) int {
	var se *daemonclient.StatusError
	switch {
	case errors.Is(err, daemonclient.ErrUnavailable):
		fmt.Fprintf(stderr, "pdx %s: 等了 30 秒 daemon 仍沒有回應 daemon_unavailable\n", cmd)
		return ExitUnavailable
	case errors.Is(err, daemonclient.ErrUnsupported):
		fmt.Fprintf(stderr, "pdx %s: 這個 daemon 沒有這個 /api/team 路由，請先更新 daemon unsupported\n", cmd)
		return ExitUnsupported
	case errors.As(err, &se) && se.API.Error != "":
		fmt.Fprintln(stderr, strings.TrimSpace("pdx "+cmd+": "+sanitizeCell(se.API.Detail)), sanitizeCell(se.API.Error))
		if teamRefusalCodes[se.API.Error] {
			return ExitRefused
		}
		return ExitError
	default:
		fmt.Fprintf(stderr, "pdx %s: %s\n", cmd, sanitizeCell(err.Error()))
		return ExitError
	}
}

// spawnArgs is a parsed, validated `pdx spawn`.
type spawnArgs struct {
	cfgPath, cwd, title, model, effort string
}

// parseSpawnArgs checks the grammar, U20 (a)'s model and effort included,
// before anything is read or asked. ok=false: a usage line was written (exit 2).
func parseSpawnArgs(args []string, stderr io.Writer) (spawnArgs, bool) {
	fs := flag.NewFlagSet("pdx spawn", flag.ContinueOnError)
	var a spawnArgs
	fs.StringVar(&a.cfgPath, "config", "", "")
	fs.StringVar(&a.cwd, "cwd", "", "")
	fs.StringVar(&a.title, "title", "", "")
	fs.StringVar(&a.model, "model", "", "")
	fs.StringVar(&a.effort, "effort", "", "")
	reject := func(msg string) (spawnArgs, bool) {
		fmt.Fprintf(stderr, "pdx spawn: %s\n%s\n", msg, spawnUsage)
		return a, false
	}
	pos, err := parseTeamFlags(fs, args)
	if err != nil {
		return reject(err.Error())
	}
	set := map[string]bool{}
	fs.Visit(func(f *flag.Flag) { set[f.Name] = true })
	switch {
	case len(pos) != 0:
		return reject(fmt.Sprintf("unexpected argument %q", pos[0]))
	case set["model"] && !team.ValidModel(a.model):
		return reject(fmt.Sprintf("--model %q 不是模型名稱（別名如 sonnet、opus，或完整名稱，可加 [1m]）", a.model))
	case set["effort"] && !team.ValidEffort(a.effort):
		return reject(fmt.Sprintf("--effort %q 必須是 %s 之一", a.effort, strings.Join(team.Efforts, "、")))
	case set["title"] && ipeers.ValidateTitle(a.title) != nil:
		return reject("--title: " + ipeers.ValidateTitle(a.title).Error())
	}
	return a, true
}

// runSpawnCmd implements `pdx spawn` (spec §7.2): one op id, POSTed again
// while the op runs (the daemon joins it), then the member on stdout.
func runSpawnCmd(ctx context.Context, args []string, getenv func(string) string, stdout, stderr io.Writer, clientOpts ...daemonclient.Option) int {
	a, ok := parseSpawnArgs(args, stderr)
	if !ok {
		return ExitUsage
	}
	client, inbox, ok := teamSetup("spawn", a.cfgPath, getenv, stderr, clientOpts)
	if !ok {
		return ExitError
	}
	cwd, err := filepath.Abs(a.cwd) // "" is the working directory (coordinator decision 6)
	if err != nil {
		fmt.Fprintf(stderr, "pdx spawn: %v\n", err)
		return ExitError
	}
	if a.model == "" {
		fmt.Fprintln(stderr, team.ReminderNoModel) // U20 (c): the spawn goes on
	}
	req := team.SpawnRequest{ID: spawnNewID(), OriginInbox: inbox, Cwd: cwd, Title: a.title, Model: a.model, Effort: a.effort}
	op, code := spawnSettle(ctx, client, req, stderr)
	if code != ExitOK {
		return code
	}
	switch {
	case op.State == team.SpawnFailed && op.Reason == team.SpawnReasonStartTimeout:
		fmt.Fprintf(stderr, "pdx spawn: %s %s\n", spawnStartTimeoutHint, team.SpawnReasonStartTimeout)
		return ExitMemberFailed
	case op.State == team.SpawnFailed:
		fmt.Fprintf(stderr, "pdx spawn: spawn %s 失敗 %s\n", sanitizeCell(op.ID), sanitizeCell(op.Reason))
		return ExitError
	case op.State != team.SpawnDone || op.Member == nil:
		fmt.Fprintf(stderr, "pdx spawn: daemon 回了無法辨識的 spawn（state %q） invalid_response\n", sanitizeCell(string(op.State)))
		return ExitError
	}
	m := op.Member
	out, _ := json.Marshal(spawnOutput{Ref: m.Ref, Address: m.Address, TmuxSession: m.TmuxSession,
		SessionID: m.SessionID, HostID: m.HostID, SpawnOp: op.ID}) // strings only: cannot fail
	fmt.Fprintln(stdout, string(out))
	return ExitOK
}

// spawnSettle POSTs req until its op leaves running: the same id each time,
// so the daemon joins the op, across a daemon restart too (Idempotent).
// Three consecutive attempts with no answer at all are exit 20 (spec §9.1).
// Past spawnSettleBound it gives up with exit 14: the member did not start
// in time, though the op may still finish (stderr names it).
func spawnSettle(ctx context.Context, client *daemonclient.Client, req team.SpawnRequest, stderr io.Writer) (team.SpawnOp, int) {
	// A cancellation, not a deadline: the client bounds each attempt
	// (ErrNoAnswer, counted below) only under a ctx without a deadline.
	bounded, cancel := context.WithCancel(ctx)
	defer cancel()
	defer time.AfterFunc(spawnSettleBound, cancel).Stop()
	hung := 0
	for {
		var op team.SpawnOp
		_, err := client.Do(bounded, http.MethodPost, "/api/team/spawns", req, &op, daemonclient.Idempotent())
		switch {
		case err == nil && op.State != team.SpawnRunning:
			return op, ExitOK
		case ctx.Err() == nil && bounded.Err() != nil:
			fmt.Fprintf(stderr, "pdx spawn: spawn %s 在期限內沒有結束，daemon 上可能仍在進行；先用 pdx team 確認，再決定是否重開 %s\n",
				req.ID, team.SpawnReasonStartTimeout)
			return op, ExitMemberFailed
		case err == nil: // running: the same body again joins the op
			hung = 0
		case ctx.Err() == nil && (errors.Is(err, daemonclient.ErrNoAnswer) || errors.Is(err, context.DeadlineExceeded)):
			if hung++; hung >= teamMaxHungPolls {
				fmt.Fprintln(stderr, "pdx spawn: daemon 沒有回應 daemon_unavailable")
				return op, ExitUnavailable
			}
		default:
			return op, teamReportErr("spawn", err, stderr)
		}
	}
}

// spawnOutput is what a done spawn prints on stdout, one JSON line (§7.2 step 6).
type spawnOutput struct {
	Ref         string `json:"ref"`
	Address     string `json:"address"`
	TmuxSession string `json:"tmux_session"`
	SessionID   string `json:"session_id"`
	HostID      string `json:"host_id"`
	SpawnOp     string `json:"spawn_op"`
}
