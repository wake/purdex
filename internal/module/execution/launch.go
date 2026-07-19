package execution

import (
	"context"
	"database/sql"
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

// LaunchReporter durably enqueues the accepted(seq=1) and running(seq=2) reports
// for an execution. It is implemented in the dispatch module (which owns the
// report outbox) and injected here: execution must not import dispatch (that
// would cycle). The accepted report's immutable facts are all readable off the
// passed *Execution, so a reconstructed accepted stays byte-identical (spec §3.3
// durability cut).
type LaunchReporter interface {
	EnqueueAccepted(exec *Execution) error
	EnqueueRunning(exec *Execution) error
}

// launchStore is the slice of *ExecutionStore the durable cut writes through.
type launchStore interface {
	UpsertByDispatch(req NewExecution) (*Execution, bool, error)
	MarkLaunched(execID, sessionCode string) error
	UpdateStatus(execID string, to Status) error
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
	DispatchID     string
	RepoLocation   string
	Prompt         string
	SandboxProfile string
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
	// newID generates the execution_id (and, via SessionNameFor, the session
	// name) before the row is inserted; overridable in tests.
	newID func() string
	logf  func(format string, args ...any)
}

// NewCoordinator builds a launch coordinator. admitter is typically *Admitter;
// store is *ExecutionStore; reporter is the dispatch-side outbox adapter;
// launcher is the real tmux/relay launcher (launcher.go).
func NewCoordinator(admitter repoAdmitter, store launchStore, reporter LaunchReporter, launcher Launcher) *Coordinator {
	return &Coordinator{
		admitter: admitter,
		store:    store,
		reporter: reporter,
		launcher: launcher,
		newID:    newExecutionID,
		logf:     log.Printf,
	}
}

// Accept runs the full durable cut for one dispatch and returns the resulting
// execution row. Admission rejection (ErrRepoBusy / ErrCanonical) and launch
// failure surface as errors for the caller (P.7) to turn into a failed report;
// on launch failure the row is already marked failed here so the repo unblocks.
func (c *Coordinator) Accept(ctx context.Context, req LaunchRequest) (*Execution, error) {
	var result *Execution
	err := c.admitter.WithRepoLock(ctx, req.RepoLocation, func(adm *Admission) error {
		// (a) pre-generate execution_id + deterministic session_name.
		execID := c.newID()
		sessionName := SessionNameFor(execID)

		// (b) insert the row at launch_state=launching (this makes the repo
		// live under the held lock). Idempotent on dispatch_id: a re-delivery
		// returns the existing row and skips launch.
		exec, created, err := c.store.UpsertByDispatch(NewExecution{
			ExecutionID:    execID,
			DispatchID:     req.DispatchID,
			RepoLocation:   adm.CanonicalPath,
			Provider:       "claude",
			SessionName:    sessionName,
			LaunchState:    LaunchLaunching,
			HeadAtStart:    adm.HeadAtStart,
			DirtyAtStart:   adm.DirtyAtStart,
			SandboxProfile: req.SandboxProfile,
		})
		if err != nil {
			return fmt.Errorf("create execution row: %w", err)
		}
		if !created {
			// Already admitted for this dispatch — do not relaunch or re-enqueue.
			result = exec
			return nil
		}

		// (c) durably enqueue accepted(seq=1) BEFORE spawn, so accepted is
		// recoverable even if the daemon dies mid-launch.
		if err := c.reporter.EnqueueAccepted(exec); err != nil {
			return fmt.Errorf("enqueue accepted: %w", err)
		}

		// (d) launch: create session, spawn relay+claude -p, push the prompt,
		// block until the relay connects.
		lr, lerr := c.launcher.Launch(ctx, LaunchSpec{
			ExecutionID:    exec.ExecutionID,
			SessionName:    sessionName,
			Cwd:            adm.CanonicalPath,
			Prompt:         req.Prompt,
			SandboxProfile: req.SandboxProfile,
		})
		if lerr != nil {
			// Mark failed so the single-live guard releases the repo; reconcile
			// (P.9) is a backstop but the live-path failure is handled here.
			if uerr := c.store.UpdateStatus(exec.ExecutionID, StatusFailed); uerr != nil {
				c.logf("[execution] mark failed after launch error execution=%s: %v", exec.ExecutionID, uerr)
			}
			return fmt.Errorf("launch execution %s: %w", exec.ExecutionID, lerr)
		}

		// (e) commit the launched fence + session_code (+ status→running) and
		// enqueue running(seq=2).
		if err := c.store.MarkLaunched(exec.ExecutionID, lr.SessionCode); err != nil {
			return fmt.Errorf("mark launched: %w", err)
		}
		exec.LaunchState = LaunchLaunched
		exec.Status = StatusRunning
		if lr.SessionCode != "" {
			exec.SessionCode = sql.NullString{String: lr.SessionCode, Valid: true}
		}
		if err := c.reporter.EnqueueRunning(exec); err != nil {
			return fmt.Errorf("enqueue running: %w", err)
		}
		result = exec
		return nil
	})
	if err != nil {
		return nil, err
	}
	return result, nil
}
