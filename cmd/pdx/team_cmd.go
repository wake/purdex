package main

// `pdx spawn`, `pdx kill` and `pdx team`: the lead's commands (lead-team-
// relay spec §7.2, §7.3, U20; plan v3 P4-7). Each runs inside the lead's
// Claude Code session, which the daemon attributes by its inbox. API errors
// print `pdx <cmd>: <detail> <code>`, the code last; exit codes are spec §14.

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/google/uuid"

	"github.com/wake/purdex/cmd/pdx/daemonclient"
	"github.com/wake/purdex/internal/config"
	ipeers "github.com/wake/purdex/internal/peers"
	"github.com/wake/purdex/internal/resources"
	"github.com/wake/purdex/internal/team"
)

const spawnUsage = "usage: pdx spawn [--cwd <dir>] [--title <t>] [--model <m>] [--effort <e>] [--brief-file <f> | --brief <text>] [--config <path>]\n" +
	"       (--cwd defaults to this directory; --effort is low, medium, high, xhigh or max;\n" +
	"        without --model the member runs this host's default model, which is not fixed)"

const killUsage = "usage: pdx kill <ref> [--config <path>]\n" +
	"       (<ref> is _xxxxxx, or an address from pdx team; only a member of your own team)"

const teamUsage = "usage: pdx team [--json] [--config <path>]"

const (
	// teamAttemptTimeout bounds one request: a spawn POST waits up to
	// team.SpawnPollWaitS daemon-side, plus room (as lead's polls).
	teamAttemptTimeout = 35 * time.Second
	// teamMaxHungPolls: consecutive spawn POSTs with no answer at all before exit 20 (spec §9.1).
	teamMaxHungPolls = 3
	// maxLeadAddressBytes is the longest lead address the brief's first line
	// carries: "<alias>/<name or ref>" (the origin resolver's address), the
	// alias at most 64 bytes (config aliasPattern
	// ^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$), the name at most 64
	// (ipeers.RoutableName ^[a-z0-9][a-z0-9-]{1,63}$; a ref is 7). An alias
	// outside that rule (a hand-edited config) can only make the daemon refuse
	// the send, which is reported as a failed brief.
	maxLeadAddressBytes = 64 + 1 + 64
	// teamIDBytes: the team id is the approving request's id, which the
	// daemon stores as a canonical UUID.
	teamIDBytes = 36
	// spawnStartTimeoutHint is the stderr hint of member_start_timeout: a
	// member that never registers ran on a host without the Purdex hooks.
	spawnStartTimeoutHint = "member 沒有在 20 秒內啟動（這台主機需要 Purdex hooks：pdx setup --agent cc）"
	// spawnWaitTimeout is the CLI's own code, not a daemon wire code (so it
	// is not in internal/team): `pdx spawn` stopped waiting after
	// spawnSettleBound while the op may still run, exit 1. Only the daemon's
	// failed{member_start_timeout} — the session killed, its place freed —
	// is exit 14 (critic ruling on PR P4-7).
	spawnWaitTimeout = "spawn_wait_timeout"
)

// teamRefusalCodes are the team-rule refusals these commands can meet: exit
// 13 (spec §14). relay_open is a kill of a member mid-relay (P4-6 review).
// Any other API code is exit 1.
var teamRefusalCodes = map[string]bool{
	team.ErrNotLead:         true,
	team.ErrTeamFull:        true,
	team.ErrCwdOutsideGrant: true,
	team.ErrNotYourMember:   true,
	team.ErrRelayOpen:       true,
	// The task routes' refusals (plan T-1c).
	team.ErrNotMember:         true,
	team.ErrTaskNotFound:      true,
	team.ErrNotTaskOwner:      true,
	team.ErrBadTaskTransition: true,
	team.ErrBlockedByUnknown:  true,
	team.ErrBlockedByCycle:    true,
	team.ErrOwnerNotActive:    true,
}

// spawnNewID mints the spawn op id, the idempotency key of every POST of
// one spawn (a test seam).
var spawnNewID = uuid.NewString

// spawnSettleBound caps the whole wait for one spawn, below the Bash tool's
// 10-minute limit (as `pdx lead request --wait` 9m), so an op the daemon
// keeps answering running cannot hold the lead forever. A var only so tests
// can shorten it.
var spawnSettleBound = 9 * time.Minute

// briefMaxBytes is the longest brief whose message (the first line with the
// longest lead address and team id, "\n", the brief) still fits the peers
// text limit, so a brief that passes it is never refused for its size.
var briefMaxBytes = ipeers.MaxTextBytes - len("\n") - len(fmt.Sprintf(team.MemberBriefPrefixFmt,
	strings.Repeat("a", maxLeadAddressBytes), strings.Repeat("0", teamIDBytes)))

// briefReadTimeout bounds reading --brief-file, whose open blocks on a FIFO
// nobody writes to. A var only so tests can shorten it.
var briefReadTimeout = 10 * time.Second

// briefTimeout bounds the brief's one POST (as `pdx msg send`). A var only
// so tests can shorten it.
var briefTimeout = msgSendTimeout

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
		detail := se.API.Detail
		if se.API.Op != nil { // relay_open: the relay to wait for (pdx relay op <id>)
			detail += "（relay op " + se.API.Op.ID + "）"
		}
		fmt.Fprintln(stderr, strings.TrimSpace("pdx "+cmd+": "+sanitizeCell(detail)), sanitizeCell(se.API.Error))
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
	brief                              string
	hasBrief                           bool
}

// parseSpawnArgs checks the grammar, U20 (a)'s model and effort included,
// before anything is read or asked. ok=false: a usage line was written (exit 2).
func parseSpawnArgs(args []string, stderr io.Writer) (spawnArgs, bool) {
	fs := flag.NewFlagSet("pdx spawn", flag.ContinueOnError)
	var a spawnArgs
	var briefFile string
	fs.StringVar(&a.cfgPath, "config", "", "")
	fs.StringVar(&a.cwd, "cwd", "", "")
	fs.StringVar(&a.title, "title", "", "")
	fs.StringVar(&a.model, "model", "", "")
	fs.StringVar(&a.effort, "effort", "", "")
	fs.StringVar(&a.brief, "brief", "", "")
	fs.StringVar(&briefFile, "brief-file", "", "")
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
	case set["brief"] && set["brief-file"]:
		return reject("--brief 與 --brief-file 只能擇一")
	}
	a.hasBrief = set["brief"] || set["brief-file"]
	if set["brief-file"] {
		if a.brief, err = readBriefFile(briefFile); err != nil {
			return reject("--brief-file: " + err.Error())
		}
	}
	if a.hasBrief {
		// Checked now so a brief the daemon would refuse fails before a
		// member opens, not after.
		if strings.TrimSpace(a.brief) == "" {
			return reject("brief 不能為空")
		}
		if len(a.brief) > briefMaxBytes || ipeers.ValidateText(a.brief) != nil {
			return reject(fmt.Sprintf("brief 必須是不超過 %d bytes 的 UTF-8 文字（peers 訊息上限 %d bytes，扣掉首行）", briefMaxBytes, ipeers.MaxTextBytes))
		}
	}
	return a, true
}

// readBriefFile reads at most one byte past briefMaxBytes of path, so a huge
// file, /dev/zero or a FIFO whose writer never closes costs one bounded read
// (the caller refuses anything longer). An open or read that has not ended
// within briefReadTimeout (a FIFO nobody writes to) is an error; its
// goroutine is left behind, and the process exits soon after.
func readBriefFile(path string) (string, error) {
	type result struct {
		b   []byte
		err error
	}
	done := make(chan result, 1)
	go func() {
		f, err := os.Open(path)
		if err != nil {
			done <- result{err: err}
			return
		}
		defer f.Close()
		b, err := io.ReadAll(io.LimitReader(f, int64(briefMaxBytes)+1))
		done <- result{b, err}
	}()
	select {
	case r := <-done:
		return string(r.b), r.err
	case <-time.After(briefReadTimeout):
		return "", fmt.Errorf("%s 內沒有讀完（沒有寫入端的 FIFO？）", briefReadTimeout)
	}
}

// runSpawnCmd implements `pdx spawn` (spec §7.2): one op id, POSTed again
// while the op runs (the daemon joins it), then the member on stdout and
// the brief from the lead's inbox.
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
	if !a.hasBrief {
		return ExitOK
	}
	return sendBrief(ctx, client, inbox, op, a.brief, stderr)
}

// spawnSettle POSTs req until its op leaves running: the same id each time,
// so the daemon joins the op, across a daemon restart too (Idempotent).
// Three consecutive attempts with no answer at all are exit 20 (spec §9.1).
// Past spawnSettleBound it stops waiting: exit 1, spawn_wait_timeout, the op
// named on stderr, because the op may still finish on the daemon.
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
			fmt.Fprintf(stderr, "pdx spawn: spawn %s 在期限內沒有結束，daemon 上可能仍在進行；先用 pdx team 確認，不要直接重開 %s\n",
				req.ID, spawnWaitTimeout)
			return op, ExitError
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

// sendBrief sends the brief to the new member through POST
// /api/peers/send, from the lead's inbox so the member's replies go to the
// lead, after the one-line prefix (spec §7.2). One request, never replayed:
// a send that may have arrived must not arrive twice. A failure is exit 1
// with the member already on stdout (coordinator decision 14).
func sendBrief(ctx context.Context, client *daemonclient.Client, inbox string, op team.SpawnOp, brief string, stderr io.Writer) int {
	text := fmt.Sprintf(team.MemberBriefPrefixFmt, op.LeadAddress, op.TeamID) + "\n" + brief
	sctx, cancel := context.WithTimeout(ctx, briefTimeout)
	defer cancel()
	req := ipeers.SendRequest{To: op.Member.Address, Text: text, OriginInbox: inbox}
	if _, err := client.Once(sctx, http.MethodPost, "/api/peers/send", req, nil); err != nil {
		detail, code := briefErr(err)
		fmt.Fprintf(stderr, "pdx spawn: member 已開啟，但 brief 沒送出（%s）；請用 pdx msg send %s 手動送 %s\n",
			sanitizeCell(detail), sanitizeCell(op.Member.Address), sanitizeCell(code))
		return ExitError
	}
	return ExitOK
}

// briefErr is a failed brief's detail and code, the code last on stderr as
// for every API error: the daemon's own code when it sent one, else the
// CLI's — unsupported (plain 404), no_answer (the bound ran out),
// invalid_response (an answer with no code), daemon_unavailable (no answer).
func briefErr(err error) (detail, code string) {
	var se *daemonclient.StatusError
	switch {
	case errors.As(err, &se) && se.API.Error != "":
		return se.API.Detail, se.API.Error
	case se != nil:
		return se.Error(), "invalid_response"
	case errors.Is(err, daemonclient.ErrUnsupported):
		return "這個 daemon 沒有 /api/peers/send", daemonclient.ErrUnsupported.Error()
	case errors.Is(err, context.DeadlineExceeded):
		return fmt.Sprintf("daemon %s 內沒有回應", briefTimeout), daemonclient.ErrNoAnswer.Error()
	default:
		return err.Error(), daemonclient.ErrUnavailable.Error()
	}
}

func runKill(args []string) {
	os.Exit(runKillCmd(context.Background(), args, os.Getenv, os.Stdout, os.Stderr))
}

func runTeam(args []string) {
	os.Exit(runTeamCmd(context.Background(), args, os.Getenv, os.Stdout, os.Stderr))
}

// runKillCmd implements `pdx kill <ref>` (spec §7.3): the target goes as
// typed, the daemon matches it among the caller's members only. A replay is
// safe (a killed member answers 200 again), so the POST is Idempotent.
func runKillCmd(ctx context.Context, args []string, getenv func(string) string, stdout, stderr io.Writer, clientOpts ...daemonclient.Option) int {
	fs := flag.NewFlagSet("pdx kill", flag.ContinueOnError)
	cfgPath := fs.String("config", "", "")
	pos, err := parseTeamFlags(fs, args)
	if err == nil && (len(pos) != 1 || strings.TrimSpace(pos[0]) == "") {
		err = errors.New("需要剛好一個 <ref>（_xxxxxx 或 <host>/<name>）")
	}
	if err != nil {
		fmt.Fprintf(stderr, "pdx kill: %v\n%s\n", err, killUsage)
		return ExitUsage
	}
	client, inbox, ok := teamSetup("kill", *cfgPath, getenv, stderr, clientOpts)
	if !ok {
		return ExitError
	}
	var m team.Member
	if _, err := client.Do(ctx, http.MethodPost, "/api/team/kill", team.KillRequest{OriginInbox: inbox, Target: pos[0]}, &m, daemonclient.Idempotent()); err != nil {
		return teamReportErr("kill", err, stderr)
	}
	out, err := json.Marshal(m)
	if err != nil {
		fmt.Fprintf(stderr, "pdx kill: %v\n", err)
		return ExitError
	}
	fmt.Fprintln(stdout, string(out))
	return ExitOK
}

// teamResourcesTimeout bounds the table's one resources request, retries
// included. A var only so tests can shorten it.
var teamResourcesTimeout = 5 * time.Second

// teamHostShares is each session's share of the host (D-1 units, host
// percent) from one GET /api/resources, keyed by session id. It is
// best-effort: any failure, an unavailable sample or a daemon without the
// route gives nil, and the table shows "-". The team table must never break,
// or print an error line, because of this call.
func teamHostShares(ctx context.Context, client *daemonclient.Client) map[string]resources.SessionUse {
	ctx, cancel := context.WithTimeout(ctx, teamResourcesTimeout)
	defer cancel()
	_, snap, err := getResources(ctx, client)
	if err != nil || !snap.Available {
		return nil
	}
	out := make(map[string]resources.SessionUse, len(snap.Sessions))
	for _, u := range snap.Sessions {
		// One row per session is the contract; if a daemon lists a twin, the
		// busier row is the session's load.
		if prev, ok := out[u.SessionID]; !ok || u.Use > prev.Use {
			out[u.SessionID] = u
		}
	}
	return out
}

// runTeamCmd implements `pdx team [--json]` (spec §7.3, U20 (e)): --json is
// the daemon's view as is; the table's MODEL and EFFORT are what each
// member actually runs (its statusline reading), "-" until its first one.
// CPU and MEM are the member's share of the host in whole percents (host
// resource lease, review #14), joined by session id from /api/resources.
func runTeamCmd(ctx context.Context, args []string, getenv func(string) string, stdout, stderr io.Writer, clientOpts ...daemonclient.Option) int {
	fs := flag.NewFlagSet("pdx team", flag.ContinueOnError)
	cfgPath := fs.String("config", "", "")
	asJSON := fs.Bool("json", false, "")
	pos, err := parseTeamFlags(fs, args)
	if err == nil && len(pos) != 0 {
		err = fmt.Errorf("unexpected argument %q", pos[0])
	}
	if err != nil {
		fmt.Fprintf(stderr, "pdx team: %v\n%s\n", err, teamUsage)
		return ExitUsage
	}
	client, inbox, ok := teamSetup("team", *cfgPath, getenv, stderr, clientOpts)
	if !ok {
		return ExitError
	}
	var raw json.RawMessage
	if _, err := client.Do(ctx, http.MethodGet, "/api/team?origin_inbox="+url.QueryEscape(inbox), nil, &raw); err != nil {
		return teamReportErr("team", err, stderr)
	}
	var v team.TeamView
	var line bytes.Buffer
	if json.Unmarshal(raw, &v) != nil || json.Compact(&line, raw) != nil {
		fmt.Fprintln(stderr, "pdx team: daemon 的回應不是 team view invalid_response")
		return ExitError
	}
	if *asJSON {
		fmt.Fprintln(stdout, line.String())
		return ExitOK
	}
	// The resources call is optional, so it gets a client of its own whose
	// stderr is discarded: the shared client would print its "daemon restarting"
	// line for a failure the table already hides.
	var shares map[string]resources.SessionUse
	if quiet, _, ok := teamSetup("team", *cfgPath, getenv, io.Discard, clientOpts); ok {
		shares = teamHostShares(ctx, quiet)
	}
	// D-N9: the team's name, when it has one, on a line of its own above the
	// table; sanitised like a table cell, since it is printed into a terminal.
	if v.Team.TeamName != "" {
		fmt.Fprintf(stdout, "team: %s\n", sanitizeCell(v.Team.TeamName))
	}
	tw := tabwriter.NewWriter(stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "ADDRESS\tREF\tTITLE\tSTATE\tCTX\tCPU\tMEM\tMODEL\tEFFORT\tCWD\tTMUX")
	for _, m := range v.Members {
		pct, model, effort := "", "", ""
		if c := m.Context; c != nil {
			if c.UsedPercentage != nil {
				pct = fmt.Sprintf("%.0f%%", *c.UsedPercentage)
			}
			model, effort = c.ModelID, c.Effort
		}
		cpu, mem := "", ""
		if u, ok := shares[m.SessionID]; ok {
			cpu, mem = fmt.Sprintf("%.0f%%", u.CPU), fmt.Sprintf("%.0f%%", u.Mem)
		}
		cells := []string{m.Address, m.Ref, m.Title, string(m.State), pct, cpu, mem, model, effort, m.Cwd, m.TmuxSession}
		for i, c := range cells {
			if cells[i] = sanitizeCell(c); c == "" {
				cells[i] = "-"
			}
		}
		fmt.Fprintln(tw, strings.Join(cells, "\t"))
	}
	if err := tw.Flush(); err != nil {
		fmt.Fprintf(stderr, "pdx team: %v\n", err)
		return ExitError
	}
	return ExitOK
}
