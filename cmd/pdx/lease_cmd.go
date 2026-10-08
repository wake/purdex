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
	"strings"
	"text/tabwriter"
	"time"

	"github.com/wake/purdex/cmd/pdx/daemonclient"
	"github.com/wake/purdex/internal/config"
	"github.com/wake/purdex/internal/resources"
)

const leaseUsage = "usage: pdx lease ls [--json] [--config <path>]"

// leaseAttemptTimeout bounds one request to the daemon; a snapshot is a read
// of memory, so a daemon slower than this is not answering.
const leaseAttemptTimeout = 5 * time.Second

// Pressure levels as kern.memorystatus_vm_pressure_level reports them.
const (
	pressureNormal   = 1
	pressureWarn     = 2
	pressureCritical = 4
)

func runLease(args []string) {
	os.Exit(runLeaseCmd(context.Background(), args, os.Getenv, os.Stdout, os.Stderr))
}

// runLeaseCmd dispatches `pdx lease <verb>`.
func runLeaseCmd(ctx context.Context, args []string, getenv func(string) string, stdout, stderr io.Writer, clientOpts ...daemonclient.Option) int {
	if len(args) == 0 || args[0] != "ls" {
		msg := "需要一個子指令"
		if len(args) > 0 {
			msg = fmt.Sprintf("unknown subcommand %q", args[0])
		}
		fmt.Fprintf(stderr, "pdx lease: %s\n%s\n", msg, leaseUsage)
		return ExitUsage
	}
	return runLeaseLs(ctx, args[1:], stdout, stderr, clientOpts)
}

// leaseClient builds the daemon client from the config, with no inbox.
func leaseClient(cmd, cfgPath string, stderr io.Writer, clientOpts []daemonclient.Option) (*daemonclient.Client, bool) {
	cfg, err := config.Load(cfgPath)
	if err != nil {
		fmt.Fprintf(stderr, "pdx %s: %v\n", cmd, err)
		return nil, false
	}
	base := fmt.Sprintf("http://%s:%d", resolveDaemonHost(cfg.Bind), cfg.Port)
	opts := append([]daemonclient.Option{daemonclient.WithStderr(stderr), daemonclient.WithAttemptTimeout(leaseAttemptTimeout)}, clientOpts...)
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
	return ExitOK
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
