package execution

import (
	"context"
	"fmt"
	"log"
)

// sessionNamePrefix prefixes the deterministic tmux session name derived from an
// execution_id. The session_name is the durable crash-recovery handle (spec
// §4.3): it must be known before spawn, persisted at row insert, and probeable
// by name after a crash (HasSession). session_code (the deeplink handle) cannot
// serve this role because it is only computable after the tmux session exists.
const sessionNamePrefix = "pdx-exec-"

// Compile-time assertions that the real production types satisfy the durable-cut
// ports (so P.7 wiring can inject them directly).
var (
	_ repoAdmitter = (*Admitter)(nil)
	_ launchStore  = (*ExecutionStore)(nil)
)

// SessionNameFor returns the deterministic tmux session name for an execution.
// Because it is a pure function of the (globally-unique) execution_id, recovery
// can recompute the exact handle a prior daemon spawned with.
func SessionNameFor(executionID string) string { return sessionNamePrefix + executionID }

// LaunchSpec is the fully-resolved instruction handed to a Launcher. It carries
// only daemon-controlled values plus the issue Prompt (which the launcher sends
// over relay stdin as a stream-json message — never via SendKeys — so there is
// no shell/keystroke injection surface).
type LaunchSpec struct {
	ExecutionID    string
	SessionName    string
	Cwd            string
	Prompt         string
	SandboxProfile string
}

// LaunchResult reports what a successful launch produced. SessionCode is the
// deeplink handle derived from the tmux session id after it was created; it is
// only knowable inside Launch, so it is returned here for the durable cut to
// persist via MarkLaunched.
type LaunchResult struct {
	SessionCode string
}

// Launcher starts one execution end-to-end and blocks until the agent's relay
// has connected (or an error occurs): create the named tmux session, spawn
// `pdx relay -- claude -p …` in it, then push the issue prompt over relay stdin.
// The durable cut depends only on this interface so the ordering logic is
// unit-testable with a fake; the real implementation lives in launcher.go.
type Launcher interface {
	Launch(ctx context.Context, spec LaunchSpec) (LaunchResult, error)
}

// LaunchReporter renders the accepted(seq=1) and running(seq=2) reports for an
// execution. It is implemented in the dispatch module (which owns the E4 wire
// format) and injected here: execution must not import dispatch (that would
// cycle). These are pure builders — the store performs the durable enqueue inside
// the same transaction as the transition being reported, so a report can never go
// missing while its transition sticks (see store_report.go). The accepted report's
// immutable facts are all readable off the passed *Execution, so a reconstructed
// accepted stays byte-identical (spec §3.3 durability cut).
type LaunchReporter interface {
	BuildAccepted(exec *Execution) (ReportEnvelope, error)
	BuildRunning(exec *Execution) (ReportEnvelope, error)
	// BuildFailed renders the terminal failed report for a pre-relay launch
	// failure (tmux create / token-file / send-keys / relay-connect timeout). It
	// rides seq=2 on the already-queued accepted(1): the failure happened before
	// running was ever queued, so ListLive-based reconcile (P.9) cannot see the
	// terminal row and would otherwise leave Ploom wedged at accepted.
	// errCode/errMsg populate the report's error object.
	BuildFailed(exec *Execution, errCode, errMsg string) (ReportEnvelope, error)
}

// launchStore is the slice of *ExecutionStore the durable cut writes through.
// Every method commits its state change together with the report it implies, in
// one transaction — that pairing is the durability guarantee, not an optimisation.
type launchStore interface {
	UpsertByDispatchWithReport(req NewExecution, build ReportBuilder) (*Execution, bool, error)
	MarkLaunchedWithReport(execID, sessionCode string, build ReportBuilder) (*Execution, error)
	UpdateStatusWithReport(execID string, to Status, build ReportBuilder) error
}

// repoAdmitter is the admission seam (satisfied by *Admitter). WithRepoLock runs
// the durable cut under a single per-canonical-repo lock so no second admission
// can slip between the single-live check and the row becoming live.
type repoAdmitter interface {
	WithRepoLock(ctx context.Context, repoLocation string, fn func(*Admission) error) error
}

// LaunchRequest is the consumer-facing input for one dispatch. RepoLocation is
// the raw path from Ploom (canonicalised inside admission); Prompt is the issue
// text the agent starts from.
type LaunchRequest struct {
	DispatchID   string
	RepoLocation string
	// RepoLocationJSON is the full Ploom S5 repo_location object as JSON (the
	// consumer marshals cd.Detail.RepoLocation). It is persisted verbatim on the
	// row so the accepted report echoes every contract field (project_id,
	// is_origin, …), not just local_dir. RepoLocation above stays the raw path fed
	// to admission canonicalisation.
	RepoLocationJSON string
	Prompt           string
	SandboxProfile   string
}

// Coordinator performs the M0 launch durable cut (spec §4.3 / plan P.6): under a
// single per-repo admission lock it inserts the execution row (making the repo
// live), durably enqueues the accepted report, launches the agent, and — on
// success — commits the launched fence + session_code and enqueues running. This
// ordering guarantees accepted is durable before spawn and that a crash at any
// point is reconcilable from the row (§5.4).
type Coordinator struct {
	admitter repoAdmitter
	store    launchStore
	reporter LaunchReporter
	launcher Launcher
	// hostPolicy is the daemon's already-resolved sandbox host policy (the sole
	// authority): every dispatch's requested profile is clamped down to it at
	// Accept time (spec §8.1). Callers pass an explicit resolved Profile so the
	// zero value can never silently over-restrict.
	hostPolicy Profile
	// newID generates the execution_id (and, via SessionNameFor, the session
	// name) before the row is inserted; overridable in tests.
	newID func() string
	logf  func(format string, args ...any)
}

// NewCoordinator builds a launch coordinator. admitter is typically *Admitter;
// store is *ExecutionStore; reporter is the dispatch-side outbox adapter;
// launcher is the real tmux/relay launcher (launcher.go). hostPolicy is the
// daemon's resolved sandbox host policy (see ResolveHostPolicy) — the authority a
// dispatch's requested profile is clamped against.
func NewCoordinator(admitter repoAdmitter, store launchStore, reporter LaunchReporter, launcher Launcher, hostPolicy Profile) *Coordinator {
	return &Coordinator{
		admitter:   admitter,
		store:      store,
		reporter:   reporter,
		launcher:   launcher,
		hostPolicy: hostPolicy,
		newID:      newExecutionID,
		logf:       log.Printf,
	}
}

// Accept runs the full durable cut for one dispatch and returns the resulting
// execution row. Admission rejection (ErrRepoBusy / ErrCanonical) and launch
// failure surface as errors for the caller (P.7) to turn into a failed report;
// on launch failure the row is already marked failed here so the repo unblocks.
func (c *Coordinator) Accept(ctx context.Context, req LaunchRequest) (*Execution, error) {
	// Clamp the requested sandbox profile against the daemon host policy BEFORE
	// taking any repo lock, so an unknown request fails closed (ErrUnknownProfile)
	// with no row and no lock held. The clamped effective value is what lands on
	// the row and drives the launch --permission-mode flag; the raw request never
	// widens past the host policy (spec §8.1, only-narrows).
	effProfile, err := Clamp(req.SandboxProfile, c.hostPolicy)
	if err != nil {
		return nil, err
	}
	effective := effProfile.String()

	var result *Execution
	err = c.admitter.WithRepoLock(ctx, req.RepoLocation, func(adm *Admission) error {
		// (a) pre-generate execution_id + deterministic session_name.
		execID := c.newID()
		sessionName := SessionNameFor(execID)

		// (b) insert the row at launch_state=launching (this makes the repo live
		// under the held lock) AND durably queue accepted(seq=1) in the same
		// transaction, so accepted is recoverable before spawn and a row can never
		// occupy the repo without a report Ploom will eventually receive. Idempotent
		// on dispatch_id: a re-delivery returns the existing row, skips the enqueue
		// and skips launch.
		exec, created, err := c.store.UpsertByDispatchWithReport(NewExecution{
			ExecutionID:      execID,
			DispatchID:       req.DispatchID,
			RepoLocation:     adm.CanonicalPath,
			RepoLocationJSON: req.RepoLocationJSON,
			Provider:         "claude",
			SessionName:      sessionName,
			LaunchState:      LaunchLaunching,
			HeadAtStart:      adm.HeadAtStart,
			DirtyAtStart:     adm.DirtyAtStart,
			SandboxProfile:   effective,
		}, c.reporter.BuildAccepted)
		if err != nil {
			return fmt.Errorf("create execution row: %w", err)
		}
		if !created {
			// Already admitted for this dispatch — do not relaunch or re-enqueue.
			result = exec
			return nil
		}

		// (c) launch: create session, spawn relay+claude -p, push the prompt,
		// block until the relay connects.
		lr, lerr := c.launcher.Launch(ctx, LaunchSpec{
			ExecutionID:    exec.ExecutionID,
			SessionName:    sessionName,
			Cwd:            adm.CanonicalPath,
			Prompt:         req.Prompt,
			SandboxProfile: effective,
		})
		if lerr != nil {
			// Mark failed (releasing the repo for the single-live guard) AND queue the
			// terminal failed(seq=2) report atomically. The failure hit before running
			// was queued, so a committed-failed row with no report would be invisible
			// to ListLive-based reconcile (P.9) and leave Ploom wedged at accepted;
			// pairing them means either both land or the row stays live for reconcile
			// to finish. accepted(1) is already queued, so the outbox replays
			// accepted→failed on the next flush.
			if uerr := c.store.UpdateStatusWithReport(exec.ExecutionID, StatusFailed,
				func(row *Execution) (ReportEnvelope, error) {
					return c.reporter.BuildFailed(row, "launch_failed", lerr.Error())
				}); uerr != nil {
				c.logf("[execution] mark failed + report after launch error execution=%s: %v", exec.ExecutionID, uerr)
			}
			return fmt.Errorf("launch execution %s: %w", exec.ExecutionID, lerr)
		}

		// (d) commit the launched fence + session_code (+ status→running) and queue
		// running(seq=2) — one transaction, so a running row always has its report.
		launched, err := c.store.MarkLaunchedWithReport(exec.ExecutionID, lr.SessionCode, c.reporter.BuildRunning)
		if err != nil {
			return fmt.Errorf("mark launched: %w", err)
		}
		result = launched
		return nil
	})
	if err != nil {
		return nil, err
	}
	return result, nil
}
