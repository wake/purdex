package main

// `pdx lease`: only `pdx lease run` is left, a deprecated alias of `pdx gate` (lease_run_alias.go). The host resource lease it
// belonged to was retired when the gate replaced it (WWD-0a removed the other verbs and the daemon's lease routes); scripts that
// still call `pdx lease run` keep working until they move to `pdx gate` (WWD-6).

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/wake/purdex/cmd/pdx/daemonclient"
	"github.com/wake/purdex/internal/config"
	"github.com/wake/purdex/internal/resources"
)

const leaseUsage = "usage: pdx lease run (--kind <k> | --weight <n>) [--queue | --ask | --wait [<dur>]] [--command <text>] [--client-id <uuid>] [--config <path>] -- <command…>\n" +
	"       (a deprecated alias of pdx gate; the other lease verbs are gone, pdx lease report is pdx host report)"

// leaseAttemptTimeout bounds one request to the daemon; a snapshot is a read
// of memory, so a daemon slower than this is not answering.
const leaseAttemptTimeout = 5 * time.Second

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
	ctx, stop := signal.NotifyContext(context.Background(), runSignals(args)...)
	defer stop()
	os.Exit(runLeaseCmd(ctx, args, os.Getenv, os.Stdout, os.Stderr))
}

// leaseRun is what `pdx lease run` does: the gate alias (a var so tests can point it elsewhere).
var leaseRun = func(_ context.Context, args []string, getenv func(string) string, stdout, stderr io.Writer, _ []daemonclient.Option) int {
	return runLeaseRunAlias(args, getenv, stdout, stderr)
}

// runLeaseCmd dispatches `pdx lease <verb>`: run, and the pointer for report. Every other verb was removed (WWD-0a).
func runLeaseCmd(ctx context.Context, args []string, getenv func(string) string, stdout, stderr io.Writer, clientOpts ...daemonclient.Option) int {
	if len(args) > 0 {
		switch args[0] {
		case "run":
			return leaseRun(ctx, args[1:], getenv, stdout, stderr, clientOpts)
		case "report":
			return runLeaseReportMoved(stderr)
		case "ls", "acquire", "release", "confirm", "cancel", "done":
			fmt.Fprintf(stderr, "pdx lease: %q 已移除（租約已由 pdx gate 取代）；只剩 pdx lease run，它是 pdx gate 的別名\n", args[0])
			return ExitUsage
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
