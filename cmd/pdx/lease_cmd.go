package main

// `pdx lease`: the host resource lease commands (host-resource-lease plan
// P0-2, Task 0.8). P0 has `ls`, a read of what the daemon measures; the
// acquire, release and run verbs arrive with the lease engine. None of them
// needs a Claude Code session: unlike spawn, kill and team they run from any
// shell, so the daemon is asked without an inbox.

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"text/tabwriter"
	"time"

	"github.com/wake/purdex/cmd/pdx/daemonclient"
	"github.com/wake/purdex/internal/config"
	"github.com/wake/purdex/internal/resources"
)

const leaseUsage = "usage: pdx lease ls [--json] [--config <path>]\n" +
	"       pdx lease acquire (--kind <k> | --weight <n>) [--queue | --ask | --wait [<dur>]] [--command <text>] [--cwd <dir>] [--session <sid>] [--tool-use <id>] [--holder-pid <pid>] [--holder-start <text>] [--client-id <uuid>] [--config <path>]\n" +
	"       pdx lease run (--kind <k> | --weight <n>) [--queue | --ask | --wait [<dur>]] [--command <text>] [--client-id <uuid>] [--config <path>] -- <command…>\n" +
	"       pdx lease report [--since 24h] [--json] [--config <path>]   (since: a duration such as 90m or 24h, or days as 7d; at most 14d)\n" +
	"       pdx lease release (<id> | --client-id <uuid>) [--notice] [--json] [--config <path>]\n" +
	"       pdx lease confirm <id> | cancel <id> | done   (your own background request, queued request or position; a pos_ id goes to the position)\n" +
	"       (--queue: a busy host answers at once with your place (exit 15); --ask: for a background command, wait for the answer to the question; --wait: wait for room (a duration is accepted and ignored). With none of them PDX_LEASE_CALLER decides: fg queue, bg ask, else wait. acquire holds for --holder-pid, default the parent of pdx: a bare acquire in a subshell or $(…) names a process that exits at once. Give --holder-pid a long-lived pid, or use pdx lease run.)"

// leaseAttemptTimeout bounds one request to the daemon; a snapshot is a read
// of memory, so a daemon slower than this is not answering.
const leaseAttemptTimeout = 5 * time.Second

// Pressure levels as kern.memorystatus_vm_pressure_level reports them.
const (
	pressureNormal   = 1
	pressureWarn     = 2
	pressureCritical = 4
)

// runSignals are the signals that end a lease command: SIGINT and SIGTERM, and
// for `run`, whose terminal may close under it, SIGHUP as well.
func runSignals(args []string) []os.Signal {
	sigs := []os.Signal{os.Interrupt, syscall.SIGTERM}
	if len(args) > 0 && args[0] == "run" {
		sigs = append(sigs, syscall.SIGHUP)
	}
	return sigs
}

func runLease(args []string) {
	// SIGINT and SIGTERM cancel ctx: acquire turns that into a DELETE and exit
	// 12 (as `pdx lead`).
	ctx, stop := signal.NotifyContext(context.Background(), runSignals(args)...)
	defer stop()
	os.Exit(runLeaseCmd(ctx, args, os.Getenv, os.Stdout, os.Stderr))
}

// leaseRun is what `pdx lease run` does: the gate alias. A var so the tests of the old, daemon-lease implementation (runLeaseRun,
// which L2 removes together with them) can point it back at that.
var leaseRun = func(_ context.Context, args []string, _ func(string) string, stdout, stderr io.Writer, _ []daemonclient.Option) int {
	return runLeaseRunAlias(args, stdout, stderr)
}

// runLeaseCmd dispatches `pdx lease <verb>`.
func runLeaseCmd(ctx context.Context, args []string, getenv func(string) string, stdout, stderr io.Writer, clientOpts ...daemonclient.Option) int {
	if len(args) > 0 {
		switch args[0] {
		case "ls":
			return runLeaseLs(ctx, args[1:], stdout, stderr, clientOpts)
		case "acquire":
			return runLeaseAcquire(ctx, args[1:], getenv, stdout, stderr, clientOpts)
		case "release":
			return runLeaseRelease(ctx, args[1:], stdout, stderr, clientOpts)
		case "run":
			return leaseRun(ctx, args[1:], getenv, stdout, stderr, clientOpts)
		case "report":
			return runLeaseReport(ctx, args[1:], stdout, stderr, clientOpts)
		case "confirm", "cancel", "done":
			return runLeaseVerb(ctx, args[0], args[1:], stdout, stderr, clientOpts)
		}
	}
	msg := "需要一個子指令"
	if len(args) > 0 {
		msg = fmt.Sprintf("unknown subcommand %q", args[0])
	}
	fmt.Fprintf(stderr, "pdx lease: %s\n%s\n", msg, leaseUsage)
	return ExitUsage
}

// leaseClient builds the daemon client from the config, with no inbox.
func leaseClient(cmd, cfgPath string, stderr io.Writer, clientOpts []daemonclient.Option) (*daemonclient.Client, bool) {
	return leaseClientT(cmd, cfgPath, stderr, leaseAttemptTimeout, clientOpts)
}

// leaseGrace is how long a lease command keeps trying a daemon that does not
// answer. It is shorter than the client's 30 s: the pool is advice, and a
// command that waits half a minute for advice before it runs has made the
// advice a cost (spec D-5).
const leaseGrace = 5 * time.Second

// leaseClientT is leaseClient with the per-attempt timeout chosen.
func leaseClientT(cmd, cfgPath string, stderr io.Writer, attempt time.Duration, clientOpts []daemonclient.Option) (*daemonclient.Client, bool) {
	cfg, err := config.Load(cfgPath)
	if err != nil {
		fmt.Fprintf(stderr, "pdx %s: %v\n", cmd, err)
		return nil, false
	}
	base := fmt.Sprintf("http://%s:%d", resolveDaemonHost(cfg.Bind), cfg.Port)
	opts := append([]daemonclient.Option{daemonclient.WithStderr(stderr), daemonclient.WithAttemptTimeout(attempt), daemonclient.WithGrace(leaseGrace)}, clientOpts...)
	return daemonclient.New(base, cfg.Token, opts...), true
}

// getResources is one GET /api/resources: the daemon's body as written, and
// decoded. An answer that is not a snapshot is an error.
func getResources(ctx context.Context, client *daemonclient.Client) (json.RawMessage, resources.Snapshot, error) {
	var raw json.RawMessage
	var snap resources.Snapshot
	if _, err := client.Do(ctx, http.MethodGet, "/api/resources", nil, &raw); err != nil {
		return nil, snap, err
	}
	if err := json.Unmarshal(raw, &snap); err != nil {
		return nil, snap, fmt.Errorf("the answer is not a resources snapshot: %w", err)
	}
	return raw, snap, nil
}

// runLeaseLs implements `pdx lease ls [--json]`: the host line and one row
// per session. --json is the daemon's body, compacted.
func runLeaseLs(ctx context.Context, args []string, stdout, stderr io.Writer, clientOpts []daemonclient.Option) int {
	fs := flag.NewFlagSet("pdx lease ls", flag.ContinueOnError)
	cfgPath := fs.String("config", "", "")
	asJSON := fs.Bool("json", false, "")
	pos, err := parseTeamFlags(fs, args)
	if err == nil && len(pos) != 0 {
		err = fmt.Errorf("unexpected argument %q", pos[0])
	}
	if err != nil {
		fmt.Fprintf(stderr, "pdx lease: %v\n%s\n", err, leaseUsage)
		return ExitUsage
	}
	client, ok := leaseClient("lease", *cfgPath, stderr, clientOpts)
	if !ok {
		return ExitError
	}
	raw, snap, err := getResources(ctx, client)
	if err != nil {
		if errors.Is(err, daemonclient.ErrUnsupported) {
			fmt.Fprintf(stderr, "pdx lease: 這個 daemon 沒有 /api/resources，請先更新 daemon %s\n", daemonclient.ErrUnsupported)
			return ExitError
		}
		return teamReportErr("lease", err, stderr)
	}
	if *asJSON {
		var line bytes.Buffer
		if err := json.Compact(&line, raw); err != nil {
			fmt.Fprintln(stderr, "pdx lease: daemon 的回應不是 JSON invalid_response")
			return ExitError
		}
		fmt.Fprintln(stdout, line.String())
		return ExitOK
	}
	return printLeaseTable(stdout, stderr, snap)
}

func printLeaseTable(stdout, stderr io.Writer, snap resources.Snapshot) int {
	if !snap.Available {
		reason := snap.Reason
		if reason == "" {
			reason = "no reason given"
		}
		fmt.Fprintf(stdout, "host  unavailable (%s)\n", sanitizeCell(reason))
		return ExitOK
	}
	h := snap.Host
	if h.Zone != "" { // a daemon from before lease v2 has no zone
		fmt.Fprintln(stdout, hostStatusLine(snap, leaseNow()))
	}
	line := fmt.Sprintf("host  %d/%d  load %.2f/%d  mem %.0f%%  pressure %s",
		h.Measured, snap.Capacity, h.Load1, h.NCPU, h.Mem, pressureName(h.Pressure))
	if h.Full {
		line += "  FULL"
	}
	fmt.Fprintln(stdout, line)

	tw := tabwriter.NewWriter(stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "SESSION\tPID\tCPU\tMEM\tRSS\tPROCS\tTMUX")
	for _, s := range snap.Sessions {
		cells := []string{
			shortSession(s.SessionID),
			fmt.Sprint(s.PID),
			fmt.Sprintf("%.1f%%", s.CPU),
			fmt.Sprintf("%.1f%%", s.Mem),
			humanBytes(s.RSSBytes),
			fmt.Sprint(s.Procs),
			s.Tmux,
		}
		for i, c := range cells {
			if cells[i] = sanitizeCell(c); c == "" {
				cells[i] = "-"
			}
		}
		fmt.Fprintln(tw, strings.Join(cells, "\t"))
	}
	if err := tw.Flush(); err != nil {
		fmt.Fprintf(stderr, "pdx lease: %v\n", err)
		return ExitError
	}
	if err := printLeaseSections(stdout, snap); err != nil {
		fmt.Fprintf(stderr, "pdx lease: %v\n", err)
		return ExitError
	}
	return ExitOK
}

// leaseNow is the clock of the age columns (a test seam).
var leaseNow = time.Now

// shortSeconds is a duration in seconds as 37s, 5m or 2h.
func shortSeconds(n int64) string {
	switch {
	case n < 60:
		return fmt.Sprintf("%ds", max(n, 0))
	case n < 3600:
		return fmt.Sprintf("%dm", n/60)
	}
	return fmt.Sprintf("%dh", n/3600)
}

// printLeaseSections writes the lease half of `pdx lease ls`: HOLDERS (what is
// held), WAITERS (the queue) and RECENT (the last ended, an overrun marked
// 超量: spec R6), each only when it has rows.
func printLeaseSections(w io.Writer, snap resources.Snapshot) error {
	cell := func(s string) string {
		if s = sanitizeCell(s); s == "" {
			return "-"
		}
		return s
	}
	if len(snap.Leases) > 0 {
		rows := [][]string{{"HOLDERS", "SESSION", "KIND", "WEIGHT", "CHARGE", "USE", "AGE"}}
		for _, l := range snap.Leases {
			age := shortSeconds(l.AgeS)
			if l.Overrun {
				age += " 超量"
			}
			rows = append(rows, []string{"", cell(shortSession(l.SessionID)), cell(l.Kind), fmt.Sprint(l.Weight),
				fmt.Sprintf("%.0f", l.Charge), fmt.Sprintf("%.0f%%", l.Use), age})
		}
		fmt.Fprintln(w)
		if err := alignRows(w, rows, 2); err != nil {
			return err
		}
	}
	if err := printPositions(w, snap, leaseNow()); err != nil {
		return err
	}
	if len(snap.Waiters) > 0 {
		header := []string{"WAITERS", "SESSION", "KIND", "WEIGHT", "WAITED", "DEADLINE IN"}
		withReason := false
		for _, q := range snap.Waiters {
			withReason = withReason || q.Reason != ""
		}
		if withReason {
			header = append(header, "REASON")
		}
		rows := [][]string{header}
		for _, q := range snap.Waiters {
			row := []string{fmt.Sprintf("#%d", q.Position), cell(shortSession(q.SessionID)), cell(q.Kind),
				fmt.Sprint(q.Weight), shortSeconds(q.WaitedS), shortSeconds(q.DeadlineInS)}
			if withReason {
				row = append(row, waitReasonText(q, leaseNow()))
			}
			rows = append(rows, row)
		}
		fmt.Fprintln(w)
		if err := alignRows(w, rows, 2); err != nil {
			return err
		}
	}
	if len(snap.Recent) > 0 {
		rows := [][]string{{"RECENT", "SESSION", "KIND", "WEIGHT", "ENDED", "REASON", "WAITED"}}
		for _, e := range snap.Recent {
			reason := cell(e.EndReason)
			if e.Overrun {
				reason += " 超量"
			}
			rows = append(rows, []string{"", cell(shortSession(e.SessionID)), cell(e.Kind), fmt.Sprint(e.Weight),
				shortSeconds(int64(leaseNow().Sub(e.EndedAt).Seconds())) + " ago", reason, shortSeconds(e.WaitedMS / 1000)})
		}
		fmt.Fprintln(w)
		if err := alignRows(w, rows, 2); err != nil {
			return err
		}
	}
	return nil
}

func pressureName(level int) string {
	switch level {
	case pressureNormal:
		return "normal"
	case pressureWarn:
		return "warn"
	case pressureCritical:
		return "critical"
	}
	return "-"
}

// shortSession is the first 8 bytes of a session id (they are ASCII uuids;
// sanitizeCell covers anything else).
func shortSession(id string) string {
	if len(id) > 8 {
		return id[:8]
	}
	return id
}

func humanBytes(b uint64) string {
	const mib, gib = 1 << 20, 1 << 30
	if b >= gib {
		return fmt.Sprintf("%.1fG", float64(b)/gib)
	}
	return fmt.Sprintf("%.0fM", float64(b)/mib)
}

// zoneText, trendText and the wait reasons in Chinese, as `pdx lease ls` shows them.
var (
	zoneText   = map[string]string{"normal": "一般", "warning": "警戒", "danger": "危險", "unknown": "未知"}
	trendText  = map[string]string{"rising": "上升", "flat": "持平", "falling": "下降", "unknown": "未知"}
	reasonText = map[string]string{
		"gap": "間隔中", "danger": "危險區", "warning_flat": "警戒且持平",
		"rising": "用量上升", "behind": "排在後面", "cut": "等待基準", "parked": "保留中", "load": "負載上限",
	}
)

// hostStatusLine is the first line of `pdx lease ls` under lease v2: the zone,
// where the usage is heading and how much of the pause after the last grant is
// left.
func hostStatusLine(snap resources.Snapshot, now time.Time) string {
	name := func(m map[string]string, k string) string {
		if v, ok := m[k]; ok {
			return v
		}
		return "未知"
	}
	gap := "無"
	if left := (snap.GapUntil - now.UnixMilli() + 999) / 1000; snap.GapUntil > 0 && left > 0 {
		gap = fmt.Sprintf("%ds", left)
	}
	return fmt.Sprintf("主機：%s · 用量%s · 間隔剩 %s", name(zoneText, snap.Host.Zone), name(trendText, snap.Host.Trend), gap)
}

// waitReasonText is a waiter's reason in Chinese with how long it has lasted;
// an unknown name is shown quoted so it cannot pass for the table's structure.
func waitReasonText(q resources.WaiterView, now time.Time) string {
	if q.Reason == "" {
		return "-"
	}
	name, ok := reasonText[q.Reason]
	if !ok {
		name = strconv.QuoteToASCII(q.Reason)
	}
	if q.Reason == "parked" && q.ParkedUntil > 0 {
		return name + "，到 " + time.UnixMilli(q.ParkedUntil).In(now.Location()).Format("15:04")
	}
	if q.ReasonSince > 0 {
		name += " " + shortSeconds((now.UnixMilli()-q.ReasonSince)/1000)
	}
	return name
}
