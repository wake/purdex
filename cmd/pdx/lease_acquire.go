package main

// `pdx lease acquire` and `release` (host-resource-lease plan Tasks 1.7, P1-3a).
// acquire asks the daemon for room, waits for it, and prints one JSON line;
// release gives it back. The pool is advice (spec D-5): when the daemon cannot
// be asked or answers anything unexpected, acquire says so and exits 0, and
// the caller runs its command.

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"time"

	"github.com/google/uuid"

	"github.com/wake/purdex/cmd/pdx/daemonclient"
	iagent "github.com/wake/purdex/internal/agent"
	ipeers "github.com/wake/purdex/internal/peers"
	"github.com/wake/purdex/internal/resources"
)

const (
	// leasePollAttempt is the client's per-attempt timeout for a lease request:
	// one long poll (25 s) plus the slack lead.go gives its polls.
	leasePollAttempt = 35 * time.Second
	// leaseMaxHungPolls: polls that end with no answer at all before the
	// daemon counts as gone (and the caller runs: fail open).
	leaseMaxHungPolls = 3
	// leaseReleaseTimeout bounds a release or a cancel (best effort).
	leaseReleaseTimeout = 3 * time.Second
	// leaseDefaultWait is how long a request may queue before it is let in.
	leaseDefaultWait = 5 * time.Minute
)

// Seams: the parent pid and the start text of a pid (as ps prints it, which is
// what the sweeper judges a holder by).
var (
	leaseParentPID   = os.Getppid
	leaseHolderStart = func(ctx context.Context, pid int) (string, error) {
		snap, err := iagent.SnapshotProcesses(ctx)
		if err != nil {
			return "", err
		}
		return snap.StartTime(pid)
	}
)

// acquireOpts is a validated acquire request.
type acquireOpts struct {
	kind        string
	weight      int
	wait        time.Duration
	session     string
	toolUse     string
	holderPID   int
	holderStart string // "" for the CLI to look it up
	clientID    string
}

// acquireOutcome is how an acquire ended.
type acquireOutcome struct {
	resp      resources.LeaseResponse
	failOpen  string // non-empty: the caller runs anyway, and this is why
	cancelled bool   // the caller was interrupted; the lease was cancelled
}

// acquireLine is the one JSON line acquire prints.
type acquireLine struct {
	ID           string `json:"id,omitempty"`
	Granted      bool   `json:"granted"`
	Overrun      bool   `json:"overrun,omitempty"`
	WaitedMS     int64  `json:"waited_ms,omitempty"`
	HostMeasured int    `json:"host_measured,omitempty"`
	FailOpen     string `json:"fail_open,omitempty"`
}

func (o acquireOutcome) line() acquireLine {
	if o.failOpen != "" {
		return acquireLine{Granted: true, FailOpen: o.failOpen}
	}
	r := o.resp
	return acquireLine{ID: r.ID, Granted: r.Granted, Overrun: r.Overrun, WaitedMS: r.WaitedMS, HostMeasured: r.Host.Measured}
}

// parseAcquireFlags reads and checks the flags of acquire; msg is a usage
// error (exit 2, before any call).
func parseAcquireFlags(args []string) (o acquireOpts, cfgPath string, msg string) {
	fs := flag.NewFlagSet("pdx lease acquire", flag.ContinueOnError)
	cfg := fs.String("config", "", "")
	kind := fs.String("kind", "", "")
	weight := fs.Int("weight", 0, "")
	wait := fs.Duration("wait", leaseDefaultWait, "")
	session := fs.String("session", "", "")
	toolUse := fs.String("tool-use", "", "")
	holderPID := fs.Int("holder-pid", 0, "")
	holderStart := fs.String("holder-start", "", "")
	clientID := fs.String("client-id", "", "")
	pos, err := parseTeamFlags(fs, args)
	switch {
	case err != nil:
		return o, "", err.Error()
	case len(pos) != 0:
		return o, "", fmt.Sprintf("unexpected argument %q", pos[0])
	case (*kind == "") == (*weight == 0):
		return o, "", "give exactly one of --kind and --weight"
	case *weight != 0 && (*weight < 1 || *weight > resources.MaxExplicitWeight):
		return o, "", fmt.Sprintf("--weight must be between 1 and %d", resources.MaxExplicitWeight)
	case *wait < 0 || *wait > resources.MaxWaitS*time.Second:
		return o, "", fmt.Sprintf("--wait must be between 0 and %ds", resources.MaxWaitS)
	case *clientID != "" && !validLeaseClientID(*clientID):
		return o, "", "--client-id must be a lower-case UUID v4"
	case *holderPID < 0:
		return o, "", "--holder-pid must be a pid"
	case *holderStart != "" && !validHolderStartText(*holderStart):
		return o, "", "--holder-start must be a process start time as ps prints it"
	}
	o = acquireOpts{kind: *kind, weight: *weight, wait: *wait, session: *session, toolUse: *toolUse,
		holderPID: *holderPID, holderStart: *holderStart, clientID: *clientID}
	if o.clientID == "" {
		o.clientID = uuid.NewString()
	}
	return o, *cfg, ""
}

func validLeaseClientID(id string) bool { return ipeers.IsUUID(id) && id[14] == '4' }

func validHolderStartText(s string) bool {
	t, err := ipeers.ParseProcStart(s)
	return err == nil && !t.IsZero()
}

// runLeaseAcquire implements `pdx lease acquire`.
func runLeaseAcquire(ctx context.Context, args []string, stdout, stderr io.Writer, clientOpts []daemonclient.Option) int {
	o, cfgPath, msg := parseAcquireFlags(args)
	if msg != "" {
		fmt.Fprintf(stderr, "pdx lease: %s\n%s\n", msg, leaseUsage)
		return ExitUsage
	}
	client, ok := leaseClientT("lease", cfgPath, stderr, leasePollAttempt, clientOpts)
	if !ok {
		// No usable config is a daemon we cannot ask: the caller runs.
		printAcquire(stdout, acquireOutcome{failOpen: "config_unreadable"})
		return ExitOK
	}
	out := leaseAcquire(ctx, client, o, stderr)
	printAcquire(stdout, out)
	if out.cancelled {
		return ExitCancelled
	}
	return ExitOK
}

func printAcquire(stdout io.Writer, o acquireOutcome) {
	b, _ := json.Marshal(o.line())
	fmt.Fprintln(stdout, string(b))
}

// leaseAcquire asks for room and waits for it. It is the part `pdx lease run`
// shares: the outcome says whether the caller may go ahead (always, unless it
// was interrupted) and how it got there.
func leaseAcquire(ctx context.Context, client *daemonclient.Client, o acquireOpts, stderr io.Writer) acquireOutcome {
	if o.holderPID == 0 {
		o.holderPID = leaseParentPID()
	}
	if o.holderStart == "" {
		sctx, cancel := context.WithTimeout(ctx, 5*time.Second)
		start, err := leaseHolderStart(sctx, o.holderPID)
		cancel()
		if err != nil || start == "" {
			return acquireOutcome{failOpen: "holder_start_unknown"}
		}
		o.holderStart = start
	}
	req := resources.LeaseRequest{ClientID: o.clientID, Kind: o.kind, Weight: o.weight, WaitS: int(o.wait / time.Second),
		SessionID: o.session, HolderPID: o.holderPID, HolderStart: o.holderStart, ToolUseID: o.toolUse}
	if o.session != "" {
		req.Scope = resources.ScopeSessionNew
	} else {
		req.Scope = resources.ScopeProcess
	}

	var resp resources.LeaseResponse
	// The client id makes the create idempotent: a POST whose connection
	// dropped after it went out may be replayed.
	if _, err := client.Do(ctx, http.MethodPost, "/api/resources/leases", req, &resp, daemonclient.Idempotent()); err != nil {
		if ctx.Err() != nil {
			return leaseCancel(client, o.clientID, stderr)
		}
		return acquireOutcome{failOpen: leaseFailReason(err)}
	}
	hung := 0
	for !resp.Granted {
		if ctx.Err() != nil {
			return leaseCancel(client, o.clientID, stderr)
		}
		if resp.State == resources.StateEnded {
			// Ended without a grant (cancelled by someone, abandoned): nothing
			// to wait for.
			return acquireOutcome{failOpen: "ended_" + resp.EndReason}
		}
		var polled resources.LeaseResponse
		_, err := client.Do(ctx, http.MethodGet, fmt.Sprintf("/api/resources/leases/%s?wait=%d", url.PathEscape(resp.ID), resources.MaxPollS), nil, &polled)
		if err != nil {
			if ctx.Err() != nil {
				return leaseCancel(client, o.clientID, stderr)
			}
			if errors.Is(err, daemonclient.ErrNoAnswer) || errors.Is(err, context.DeadlineExceeded) {
				if hung++; hung >= leaseMaxHungPolls {
					return acquireOutcome{failOpen: "daemon_not_answering"}
				}
				continue
			}
			return acquireOutcome{failOpen: leaseFailReason(err)}
		}
		hung = 0
		resp = polled
	}
	return acquireOutcome{resp: resp}
}

// leaseFailReason is a short, stable name for why the daemon could not be used.
func leaseFailReason(err error) string {
	var se *daemonclient.StatusError
	switch {
	case errors.Is(err, daemonclient.ErrUnavailable):
		return "daemon_unavailable"
	case errors.Is(err, daemonclient.ErrUnsupported):
		return "unsupported"
	case errors.As(err, &se) && se.API.Error != "":
		return se.API.Error
	case errors.As(err, &se):
		return fmt.Sprintf("http_%d", se.Status)
	}
	return "error"
}

// leaseCancel is the caller being interrupted: a best-effort DELETE by client
// id under a fresh 3 s context (the parent is done), no retry.
func leaseCancel(client *daemonclient.Client, clientID string, stderr io.Writer) acquireOutcome {
	dctx, cancel := context.WithTimeout(context.Background(), leaseReleaseTimeout)
	defer cancel()
	if _, err := client.Once(dctx, http.MethodDelete, "/api/resources/leases?client_id="+url.QueryEscape(clientID), nil, nil); err != nil {
		fmt.Fprintf(stderr, "pdx lease: 取消申請時 daemon 回應：%v\n", err)
	}
	return acquireOutcome{cancelled: true}
}

// runLeaseRelease implements `pdx lease release (<id> | --client-id <uuid>)`.
// Releasing is best effort: the sweeper is the backstop, so a daemon that
// cannot be reached is a line on stderr and exit 0.
func runLeaseRelease(ctx context.Context, args []string, stdout, stderr io.Writer, clientOpts []daemonclient.Option) int {
	fs := flag.NewFlagSet("pdx lease release", flag.ContinueOnError)
	cfgPath := fs.String("config", "", "")
	clientID := fs.String("client-id", "", "")
	asJSON := fs.Bool("json", false, "")
	pos, err := parseTeamFlags(fs, args)
	switch {
	case err != nil:
	case *clientID != "" && len(pos) != 0, *clientID == "" && len(pos) != 1:
		err = errors.New("give exactly one of <id> and --client-id")
	case *clientID != "" && !validLeaseClientID(*clientID):
		err = errors.New("--client-id must be a lower-case UUID v4")
	}
	if err != nil {
		fmt.Fprintf(stderr, "pdx lease: %v\n%s\n", err, leaseUsage)
		return ExitUsage
	}
	path := ""
	if *clientID != "" {
		path = "/api/resources/leases?client_id=" + url.QueryEscape(*clientID)
	} else {
		path = "/api/resources/leases/" + url.PathEscape(pos[0])
	}
	client, ok := leaseClientT("lease", *cfgPath, stderr, leaseReleaseTimeout, clientOpts)
	if !ok {
		return ExitOK
	}
	rctx, cancel := context.WithTimeout(ctx, leaseReleaseTimeout)
	defer cancel()
	var raw json.RawMessage
	if _, err := client.Once(rctx, http.MethodDelete, path, nil, &raw); err != nil {
		var se *daemonclient.StatusError
		if !(errors.As(err, &se) && se.API.Error == resources.ErrNoLease) { // nothing to release is not worth a line
			fmt.Fprintf(stderr, "pdx lease: 釋放沒成功（%s）；daemon 會在 holder 結束或逾時後自己收回\n", leaseFailReason(err))
		}
		return ExitOK
	}
	if *asJSON {
		var compact json.RawMessage = raw
		fmt.Fprintln(stdout, string(compact))
	}
	return ExitOK
}
