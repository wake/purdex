package main

// `pdx host report` (spec §6, R10): whether the host's load control suits it, from what the daemon stored in its host minutes.
// It reads the daemon's report; it never samples anything itself.

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"regexp"
	"strconv"
	"syscall"
	"time"

	"github.com/wake/purdex/cmd/pdx/daemonclient"
	"github.com/wake/purdex/internal/config"
	"github.com/wake/purdex/internal/resources"
)

const hostUsage = "usage: pdx host report [--since 24h] [--json] [--config <path>]   (since: a duration such as 90m or 24h, or days as 7d; at most 14d)"

func runHost(args []string) {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	os.Exit(runHostCmd(ctx, args, os.Stdout, os.Stderr))
}

// runHostCmd dispatches `pdx host <verb>`.
func runHostCmd(ctx context.Context, args []string, stdout, stderr io.Writer, clientOpts ...daemonclient.Option) int {
	if len(args) > 0 && args[0] == "report" {
		return runHostReport(ctx, args[1:], stdout, stderr, clientOpts)
	}
	msg := "需要一個子指令"
	if len(args) > 0 {
		msg = fmt.Sprintf("unknown subcommand %q", args[0])
	}
	fmt.Fprintf(stderr, "pdx host: %s\n%s\n", msg, hostUsage)
	return ExitUsage
}

// runHostReport implements `pdx host report [--since 24h] [--json]`.
func runHostReport(ctx context.Context, args []string, stdout, stderr io.Writer, clientOpts []daemonclient.Option) int {
	fs := flag.NewFlagSet("pdx host report", flag.ContinueOnError)
	cfgPath := fs.String("config", "", "")
	since := fs.String("since", "24h", "")
	asJSON := fs.Bool("json", false, "")
	pos, err := parseTeamFlags(fs, args)
	if err == nil && len(pos) != 0 {
		err = fmt.Errorf("unexpected argument %q", pos[0])
	}
	if err != nil {
		fmt.Fprintf(stderr, "pdx host: %v\n%s\n", err, hostUsage)
		return ExitUsage
	}
	client, ok := leaseClient("host", *cfgPath, stderr, clientOpts)
	if !ok {
		return ExitError
	}
	var raw json.RawMessage
	if _, err := client.Do(ctx, http.MethodGet, resources.ReportPath+"?since="+url.QueryEscape(*since), nil, &raw); err != nil {
		return teamReportErr("host", err, stderr)
	}
	var rep resources.Report
	var line bytes.Buffer
	if json.Unmarshal(raw, &rep) != nil || json.Compact(&line, raw) != nil {
		fmt.Fprintln(stderr, "pdx host: daemon 的回應不是 report invalid_response")
		return ExitError
	}
	// A daemon from before L2c answers the lease's report; decoding it would read the missing fields as zeros.
	var fields map[string]json.RawMessage
	_ = json.Unmarshal(raw, &fields)
	for _, k := range []string{"heavy", "light", "gate"} {
		if _, ok := fields[k]; !ok {
			fmt.Fprintln(stderr, "pdx host: daemon 的報表是舊格式（缺 "+k+"），請先升級 daemon incompatible_daemon")
			return ExitError
		}
	}
	if *asJSON {
		fmt.Fprintln(stdout, line.String())
		return ExitOK
	}
	formatReport(stdout, rep, time.Local)
	return ExitOK
}

func reportTime(ms int64, loc *time.Location) string {
	return time.UnixMilli(ms).In(loc).Format("01-02 15:04")
}

// safeReason is what a reason looks like when it is the daemon's own name; anything else is quoted, so that its commas and
// brackets cannot pass for the output's own structure.
var safeReason = regexp.MustCompile(`^[a-z][a-z_:]{0,47}$`)

func reportReasonName(r string) string {
	switch {
	case r == "":
		return "(none)"
	case safeReason.MatchString(r):
		return r
	}
	return strconv.QuoteToASCII(r)
}

// formatReport prints the report as plain lines, a section a line.
func formatReport(w io.Writer, r resources.Report, loc *time.Location) {
	fmt.Fprintf(w, "period    %s → %s\n", reportTime(r.Since, loc), reportTime(r.Until, loc))
	if r.Coverage.Minutes == 0 {
		fmt.Fprintf(w, "timeline  no minute rows in the period\n")
		return
	}
	fmt.Fprintf(w, "timeline  %d minute rows, %s → %s\n", r.Coverage.Minutes, reportTime(r.Coverage.From, loc), reportTime(r.Coverage.To, loc))
	switch r.Approx {
	case "":
	case resources.ReportApproxWWDStats:
		fmt.Fprintf(w, "note      part of the period is from wwd's minute stats: full and the zone figures are counted per 5 s sample; the highest load1 is a minute's average, and the gate's queue is not counted\n")
	default:
		fmt.Fprintf(w, "note      part of the period is from wwd's minute rollups: the full and zone figures are approximate (a minute counts as full, or in its worst zone, as a whole)\n")
	}
	fmt.Fprintf(w, "full      %.1f%% of the time, %d runs, longest %ds\n", r.Full.Share*100, r.Full.Runs, r.Full.LongestS)
	fmt.Fprintf(w, "heavy     %d minutes with a heavy command running (most at once %d), highest load1 %.2f, highest memory %.1f%%\n",
		r.Heavy.Minutes, r.Heavy.MaxRunning, r.Heavy.MaxLoad1, r.Heavy.MaxMem)
	if r.Heavy.LegacyMinutes > 0 {
		fmt.Fprintf(w, "          %d earlier minutes had two or more heavy leases held (the retired lease's figure, not counted above)\n", r.Heavy.LegacyMinutes)
	}
	fmt.Fprintf(w, "light     at most %d at once\n", r.Light.MaxRunning)
	fmt.Fprintf(w, "gate      %d minutes with a command waiting for a slot, longest queue %d\n", r.Gate.WaitingMinutes, r.Gate.MaxWaiting)
	if r.Zones.Minutes == 0 {
		fmt.Fprintf(w, "zones     no minute with zone seconds yet\n")
		return
	}
	fmt.Fprintf(w, "zones     normal %.1f%%  warning %.1f%%  danger %.1f%%  (%d of %d minutes counted)\n",
		r.Zones.Normal*100, r.Zones.Warning*100, r.Zones.Danger*100, r.Zones.Minutes, r.Coverage.Minutes)
}

// leaseAttemptTimeout bounds one request to the daemon; a snapshot is a read
// of memory, so a daemon slower than this is not answering.
const leaseAttemptTimeout = 5 * time.Second

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
