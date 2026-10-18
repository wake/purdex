// Package runner takes one job from "chosen" to "command ended" (spec §4.2, §5): claim it under the host's lock table, keep the
// lease alive while the workspace is made and the command runs, stop by its own monotonic deadline when the lease cannot be renewed,
// and stop when the origin says cancel. It does not choose jobs (package pick) and does not deliver results (the Finish callback
// does, while this package goes on renewing).
package runner

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/wake/purdex/internal/wwd/run"
	"github.com/wake/purdex/internal/wwd/snapshot"
	"github.com/wake/purdex/internal/wwd/wire"
	"github.com/wake/purdex/internal/wwd/worker/locks"
	"github.com/wake/purdex/internal/wwd/worker/pick"
)

// Conflict is the origin's 409 {state}: the job is not in a state that allows the call. For a claim it also covers a held job
// (state queued): the job is not ours to take this round.
type Conflict struct{ State wire.JobState }

func (e *Conflict) Error() string { return fmt.Sprintf("runner: conflict: the job is %s", e.State) }

// ErrNotFound: the origin has no such job.
var ErrNotFound = errors.New("runner: no such job")

// Origin is one origin's worker protocol (in process for this host's own queue, over TCP for a peer). A refused transition is a
// *Conflict, an unknown job is ErrNotFound; anything else is a failure to reach the origin.
type Origin interface {
	Claim(ctx context.Context, id string, req wire.ClaimRequest) (wire.ClaimResponse, error)
	Start(ctx context.Context, id, claimID string) (wire.Lease, error)
	Heartbeat(ctx context.Context, id, claimID string) (wire.HeartbeatResponse, error)
}

// Exec makes a workspace and runs the command in it. Run's ctx ending means: end the command (TERM, then KILL).
type Exec interface {
	Materialize(ctx context.Context, job wire.Job, claimID string) (snapshot.Workspace, error)
	// Prepare finds the job's task in the workspace's manifest (the snapshot's, not the worker's), checks the arguments and works out
	// what to run. It runs after the workspace is made and before the job is started. A *Rejected means the job is not runnable as
	// submitted; any other error is the host's trouble.
	Prepare(ctx context.Context, job wire.Job, ws snapshot.Workspace) (Plan, error)
	// Run runs the plan Prepare made; nothing is worked out again.
	Run(ctx context.Context, job wire.Job, ws snapshot.Workspace, plan Plan) run.Result
}

// Plan is what to run, fixed once by Prepare.
type Plan struct {
	Argv    []string
	Dir     string
	Timeout time.Duration
	TaskEnv []string
	// Summary is the task's summary kind (go-json, vitest-json, junit, plain) and OutDir the directory Run writes to (log, stdout,
	// report.json, junit.xml): what the reporter needs to build the result's summary.
	Summary string
	OutDir  string
}

// Rejected is Prepare's answer for a job that must not run: unknown task, arguments the manifest does not allow. The job fails, it is
// not an infrastructure failure (nothing is retried and no host is held off).
type Rejected struct{ Reason string }

func (e *Rejected) Error() string { return "runner: rejected: " + e.Reason }

// Kind says how a Work call ended.
type Kind string

const (
	// KindSkipped: nothing was claimed (locks busy, 409, not found, origin unreachable). Err says why. A 409 on a claim, including the
	// one a held job gets, is "skip this job this round": the caller goes on to the next candidate and does not retry this one now.
	KindSkipped Kind = "skipped"
	// KindRan: the command ended by itself or by its own timeout; Run holds how.
	KindRan Kind = "ran"
	// KindCancelled: the origin asked to stop (heartbeat cancel); the command was ended if it had started. The result to post is
	// state cancelled; the origin turns it into superseded when a newer job did that.
	KindCancelled Kind = "cancelled"
	// KindLost: the lease is gone (409 from the origin, or no renewal before the worker's own deadline). The command was ended and
	// the outcome must be discarded.
	KindLost Kind = "lost"
	// KindShutdown: the worker's own context ended while the job was held; the job should be released (reason reclaim).
	KindShutdown Kind = "shutdown"
	// KindRejected: Prepare refused the job; nothing was started. Err is the *Rejected. The result to post is state failed with no
	// exit, the reason in summary.tail.
	KindRejected Kind = "rejected"
	// KindInfra: the host could not run the job (workspace could not be made, start refused by a transport failure…). Err says why;
	// the job should be released (reason infra).
	KindInfra Kind = "infra"
)

// Ended is what Work reports.
type Ended struct {
	Kind    Kind
	Source  string
	Job     wire.Job
	ClaimID string
	Run     run.Result // for KindRan, KindCancelled and KindLost, when the command started
	Plan    Plan       // what Prepare made; zero when it failed or was not reached
	Root    string     // the workspace root (ws.Dir); empty when no workspace was made
	// Quarantined: the command ended with Run.Err set (its process tree could not be proven gone). The workspace is left in place and the
	// lock keys stay held, for as long as this worker runs; the reporter releases the job as infra whatever Kind says.
	Quarantined bool
	Err         error
}

// Config is the worker's identity and collaborators.
type Config struct {
	Worker      string
	Labels      []string
	Fingerprint map[string]string
	Locks       *locks.Table
	Exec        Exec
	// Unit is what lease_s and renew_s are counted in (time.Second; a test makes it small).
	Unit time.Duration
	// Now is the monotonic clock (time.Now).
	Now func() time.Time
}

// Controller runs jobs for one worker.
type Controller struct{ cfg Config }

func New(cfg Config) *Controller {
	if cfg.Unit <= 0 {
		cfg.Unit = time.Second
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	return &Controller{cfg: cfg}
}

// Finish is called once the command has ended (or the job was given up), with the workspace still there. It delivers the result; the
// lease keeps being renewed until it returns, and its ctx ends if the lease is lost. The workspace is removed after it returns.
type Finish func(ctx context.Context, e Ended)

// stop is why the run was ended from outside.
type stop int

const (
	stopNone stop = iota
	stopCancelled
	stopLost
)

type state struct {
	mu sync.Mutex
	s  stop
}

// set records why: a lost lease overrides a cancel (nothing can be posted any more); otherwise the first reason stays.
func (st *state) set(s stop) {
	st.mu.Lock()
	defer st.mu.Unlock()
	if st.s == stopNone || s == stopLost {
		st.s = s
	}
}

func (st *state) get() stop {
	st.mu.Lock()
	defer st.mu.Unlock()
	return st.s
}

// deadlineFor: the lease runs from before the request was sent. The origin's own deadline begins when it handles the request, which is
// not earlier, so this one is never later than the origin's: the worker has stopped before the origin can give the job to someone else.
func (c *Controller) deadlineFor(sent time.Time, leaseS int) time.Time {
	return sent.Add(time.Duration(leaseS) * c.cfg.Unit)
}

// Work claims the candidate's job and sees it through. finish may be nil.
func (c *Controller) Work(ctx context.Context, origin Origin, cand pick.Scored, finish Finish) Ended {
	job := cand.Job
	ended := Ended{Source: cand.Source, Job: job}
	owner := cand.Source + "/" + job.ID // a job id is only unique per origin; the table is host-wide

	var cr wire.ClaimResponse
	var sent time.Time
	hold, err := c.cfg.Locks.Acquire(owner, job.Locks, func() error {
		sent = c.cfg.Now()
		var e error
		cr, e = origin.Claim(ctx, job.ID, wire.ClaimRequest{Worker: c.cfg.Worker, Labels: c.cfg.Labels, Fingerprint: c.cfg.Fingerprint})
		return e
	})
	if err != nil {
		ended.Kind, ended.Err = KindSkipped, err
		return ended
	}
	quarantined := false
	defer func() {
		if !quarantined {
			hold.Release()
		}
	}()
	job, ended.Job, ended.ClaimID = cr.Job, cr.Job, cr.ClaimID

	st := &state{}
	renewCtx, endRenew := context.WithCancel(ctx) // the lease: lives until Finish returns
	defer endRenew()
	runCtx, endRun := context.WithCancel(renewCtx) // the work: ends on cancel or loss
	defer endRun()
	lease := &leaseClock{changed: make(chan struct{}, 1), deadline: c.deadlineFor(sent, cr.LeaseS), renewAt: sent.Add(time.Duration(cr.RenewS) * c.cfg.Unit)}
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		c.renew(renewCtx, origin, job.ID, cr.ClaimID, lease, st, endRun, endRenew)
	}()

	var ws snapshot.Workspace
	var res run.Result
	var plan Plan
	var runErr error
	started := false
	if ws, err = c.cfg.Exec.Materialize(runCtx, job, cr.ClaimID); err != nil {
		runErr = err
	} else if st.get() == stopNone && runCtx.Err() == nil {
		var perr error
		if plan, perr = c.cfg.Exec.Prepare(runCtx, job, ws); perr != nil {
			runErr = perr
		} else if st.get() == stopNone && runCtx.Err() == nil { // not stopped while the plan was made
			serr := c.start(runCtx, origin, job.ID, cr.ClaimID, lease, st)
			switch {
			case errors.Is(serr, errStopped):
				// the reason is in st; Kind comes from there
			case serr == nil:
				started = true
			case isConflict(serr) || c.expired(lease):
				st.set(stopLost) // refused, or the call ran into the lease deadline: the origin may already have given the job away
			default:
				runErr = fmt.Errorf("start: %w", serr)
			}
		}
	}
	if started && st.get() == stopNone {
		// fail closed: if Run panics, nothing proves its tree gone, so the lock keys must not be let go by the deferred release
		quarantined = true
		res = c.cfg.Exec.Run(runCtx, job, ws, plan)
	}
	// A run that could not prove its process tree (a sandbox unit) gone may still be writing the workspace: the workspace stays and so do
	// the lock keys, so no next job reuses either. (A start that failed never ran anything and is no such case.)
	quarantined = res.Err != nil && res.Outcome != run.StartFailed
	if !quarantined {
		hold.Release() // the command is gone: its lock keys are free again while the result is delivered
	}

	var rej *Rejected
	switch {
	case st.get() == stopLost || errors.Is(runErr, snapshot.ErrStaleClaim):
		ended.Kind = KindLost
	case st.get() == stopCancelled:
		ended.Kind = KindCancelled
	case ctx.Err() != nil:
		ended.Kind = KindShutdown
	case errors.As(runErr, &rej):
		ended.Kind, ended.Err = KindRejected, runErr
	case runErr != nil:
		ended.Kind, ended.Err = KindInfra, runErr
	default:
		ended.Kind = KindRan
	}
	ended.Run, ended.Plan, ended.Root, ended.Quarantined = res, plan, ws.Dir, quarantined
	if finish != nil {
		finish(renewCtx, ended)
	}
	if st.get() == stopLost {
		ended.Kind = KindLost // lost while the result was being delivered: it was not delivered, whatever Kind said when Finish began
	}
	if ws.Cleanup != nil && !quarantined {
		_ = ws.Cleanup()
	}
	endRenew()
	wg.Wait()
	return ended
}

// start is origin.Start under the lease's call lock, bounded by the lease deadline, applying the lease it answers with.
func (c *Controller) start(ctx context.Context, origin Origin, id, claimID string, l *leaseClock, st *state) error {
	l.call.Lock()
	defer l.call.Unlock()
	// A heartbeat that held the lock before us may have answered cancel (applied before it let go of the lock): then the job must not
	// be started at all.
	if st.get() != stopNone || ctx.Err() != nil {
		return errStopped
	}
	deadline, _ := l.get()
	sctx, end := context.WithDeadline(ctx, deadline)
	defer end()
	sent := c.cfg.Now()
	resp, err := origin.Start(sctx, id, claimID)
	if err != nil {
		return err
	}
	l.extend(c.deadlineFor(sent, resp.LeaseS), sent.Add(time.Duration(resp.RenewS)*c.cfg.Unit))
	return nil
}

func (c *Controller) expired(l *leaseClock) bool {
	deadline, _ := l.get()
	return !c.cfg.Now().Before(deadline)
}

// errStopped: start was not called because the job was already cancelled or lost.
var errStopped = errors.New("runner: stopped before start")

func isConflict(err error) bool {
	var c *Conflict
	return errors.As(err, &c) || errors.Is(err, ErrNotFound)
}

// leaseClock is the worker's own view of its lease: when it must have been renewed by, and when to renew next.
type leaseClock struct {
	// call serialises the calls that carry a lease (start, heartbeat): with one in flight at a time their answers come back in the order
	// the origin made them, so the newest answer is always the last one applied. Two in flight could be applied in the wrong order and
	// leave the worker a longer lease than the origin's.
	call     sync.Mutex
	mu       sync.Mutex
	deadline time.Time
	renewAt  time.Time
	// changed wakes the renew loop when start moves the lease while it sleeps toward the old deadline.
	changed chan struct{}
}

func (l *leaseClock) extend(deadline, renewAt time.Time) {
	l.mu.Lock()
	defer l.mu.Unlock()
	// the newest answer wins even when it is shorter: each one is a fresh relative lease from the origin, and a shortened lease
	// must not be outlived
	l.deadline = deadline
	l.renewAt = renewAt
	select {
	case l.changed <- struct{}{}:
	default:
	}
}

func (l *leaseClock) get() (deadline, renewAt time.Time) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.deadline, l.renewAt
}

// renew heartbeats on the origin's schedule and ends the work when the lease is lost or cancel is answered. endRun ends the command;
// endRenew also ends result delivery (nothing can be posted once the lease is gone).
func (c *Controller) renew(ctx context.Context, origin Origin, id, claimID string, l *leaseClock, st *state, endRun, endRenew context.CancelFunc) {
	for {
		deadline, renewAt := l.get()
		wake := renewAt
		if deadline.Before(wake) {
			wake = deadline
		}
		timer := time.NewTimer(wake.Sub(c.cfg.Now()))
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-l.changed:
			timer.Stop()
			continue // the lease moved: work out the next wake-up again
		case <-timer.C:
		}
		now := c.cfg.Now()
		if !now.Before(deadline) {
			st.set(stopLost)
			endRun()
			endRenew()
			return
		}
		if now.Before(renewAt) {
			continue
		}
		l.call.Lock()
		deadline, _ = l.get() // a start that held the lock may have moved it
		if !c.cfg.Now().Before(deadline) {
			l.call.Unlock()
			st.set(stopLost)
			endRun()
			endRenew()
			return
		}
		sent := c.cfg.Now()
		// the call itself is bound by the lease deadline: a connection that hangs must not keep the command alive past it
		hbCtx, endHB := context.WithDeadline(ctx, deadline)
		resp, err := origin.Heartbeat(hbCtx, id, claimID)
		endHB()
		if err == nil {
			l.extend(c.deadlineFor(sent, resp.LeaseS), sent.Add(time.Duration(resp.RenewS)*c.cfg.Unit))
			// cancel is applied before the lock is let go, so that a start waiting for it sees it
			if resp.Cancel && st.get() == stopNone {
				st.set(stopCancelled)
				endRun() // the command ends; the lease goes on being renewed while the cancelled result is posted
			}
		}
		l.call.Unlock()
		switch {
		case err == nil:
		case isConflict(err):
			st.set(stopLost)
			endRun()
			endRenew()
			return
		case ctx.Err() != nil:
			return
		default:
			// the origin could not be reached: try again soon, but never past the deadline
			retry := time.Duration(1) * c.cfg.Unit / 4
			l.mu.Lock()
			l.renewAt = c.cfg.Now().Add(retry)
			l.mu.Unlock()
		}
	}
}
