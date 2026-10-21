// Package report delivers what a worker did with a job to the job's origin (spec §4.3, §5.1): the result and then the log, or the
// release of a claim the worker could not carry out. It is the runner's Finish callback. Delivery is retried with a backoff for up to
// ten minutes, and a refusal by the origin (the job is not in a state that takes the call, or is gone) ends it: the origin has
// decided, nothing the worker sends changes that.
package report

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	runs "github.com/wake/purdex/internal/wwd/run"
	"github.com/wake/purdex/internal/wwd/snapshot"
	"github.com/wake/purdex/internal/wwd/summary"
	"github.com/wake/purdex/internal/wwd/wire"
	"github.com/wake/purdex/internal/wwd/worker/runner"
)

// Sink is one origin's side of the delivery (in process for this host's own queue, over TCP for a peer). A 409 is a
// *runner.Conflict and an unknown job is runner.ErrNotFound; any other error is a failure to reach the origin and is retried.
type Sink interface {
	Result(ctx context.Context, id string, req wire.ResultRequest) error
	PutLog(ctx context.Context, id, claimID string, body io.Reader) error
	Release(ctx context.Context, id string, req wire.ReleaseRequest) error
}

const (
	// RetryFor is how long a delivery is retried before it is given up (the lease of the claim is long gone by then).
	RetryFor = 10 * time.Minute
	// maxDetail clips the text of an error sent as a release's detail.
	maxDetail = 200
)

// Config is the Reporter's collaborators; every field has a default.
type Config struct {
	// Now is the clock (time.Now). Wait sleeps for d or until ctx ends (a timer). Both are injected by tests.
	Now  func() time.Time
	Wait func(ctx context.Context, d time.Duration) error
	// Backoff is the first pause between two tries and the longest one; the pause doubles from the first to the longest.
	Backoff, MaxBackoff time.Duration
	// LogRetryFor is how long the upload of the log is retried once the result is taken (default 2 minutes). It does not run under the
	// lease's context: the result makes the job final, the next heartbeat is refused with a 409 and the lease's context ends, which would
	// cut the retries short. The log still goes with the claim, and a 409 or 404 on the log itself ends it at once.
	LogRetryFor time.Duration
	// SuspendFor is how long a repo is held off after a job of it could not be run here (an infra release): the worker does not pick
	// its jobs for that long (default 5 minutes), so a host that cannot run a repo does not claim and release it in a loop.
	SuspendFor time.Duration
	// OriginBackoff is how long a job is not picked again after its origin answered the snapshot request with an error that is the
	// origin's (default 15 seconds); every failure of the same job in a row doubles it, up to OriginBackoffMax (default 5 minutes). The
	// repo is not suspended for it: the failure is often one job's (a commit that is not pinned) and the host is fine. Without the pause
	// the same job would be claimed and given back at the poll rate for as long as the origin stays wrong.
	OriginBackoff, OriginBackoffMax time.Duration
	// TaskBackoff is how long a task of a repo is not picked again after its command was not found on this host (#3908): default 15
	// seconds, doubled by every failure in a row up to TaskBackoffMax (default 5 minutes). Only that task is held off, not the repo:
	// the host is fine and the repo's other tasks run. Other start failures and other infra failures still suspend the whole repo.
	TaskBackoff, TaskBackoffMax time.Duration
	// Summary builds the summary of a run that ended. Nil: built from the files the run left (see parse).
	Summary func(runner.Ended) wire.Summary
	// Digest says whether a summary may carry PassedDigest (WWD-5b-2w): true only while every host of the pool is reachable and reads the
	// field (wire.PoolAtLeast(peers, 3)). Asked once per summary. Nil: never.
	Digest func() bool
	Logf   func(format string, args ...any)
}

// repoKey is a repo of an origin: the repo name is the same across hosts, so it alone would let one origin hold off another's.
type repoKey struct{ source, repo string }

// jobKey is a job of an origin: job ids are made by each origin, so the origin is part of the key.
type jobKey struct{ source, job string }

// taskKey is a task of an origin's repo.
type taskKey struct{ source, repo, task string }

// originHold is how long a job (or a task) is held off and how many failures in a row led to it.
type originHold struct {
	until time.Time
	wait  time.Duration // the wait this failure set
	ended bool          // a task hold: its end was logged (the end is noticed when the worker asks, so it is told once)
}

// Reporter delivers the outcome of jobs.
type Reporter struct {
	cfg Config

	mu         sync.Mutex
	suspended  map[repoKey]time.Time  // an origin's repo -> when the hold ends
	originHeld map[jobKey]originHold  // a job whose origin answered wrongly -> the hold (in memory: a restart starts again at the first wait)
	taskHeld   map[taskKey]originHold // a task whose command was not found here -> the hold (in memory, like originHeld)
}

// New returns a Reporter.
func New(cfg Config) *Reporter {
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	if cfg.Wait == nil {
		cfg.Wait = wait
	}
	if cfg.Backoff <= 0 {
		cfg.Backoff = time.Second
	}
	if cfg.MaxBackoff < cfg.Backoff {
		cfg.MaxBackoff = 30 * time.Second
		if cfg.MaxBackoff < cfg.Backoff {
			cfg.MaxBackoff = cfg.Backoff
		}
	}
	if cfg.SuspendFor <= 0 {
		cfg.SuspendFor = 5 * time.Minute
	}
	if cfg.OriginBackoff <= 0 {
		cfg.OriginBackoff = 15 * time.Second
	}
	if cfg.OriginBackoffMax < cfg.OriginBackoff {
		cfg.OriginBackoffMax = 5 * time.Minute
		if cfg.OriginBackoffMax < cfg.OriginBackoff {
			cfg.OriginBackoffMax = cfg.OriginBackoff
		}
	}
	if cfg.TaskBackoff <= 0 {
		cfg.TaskBackoff = 15 * time.Second
	}
	if cfg.TaskBackoffMax < cfg.TaskBackoff {
		cfg.TaskBackoffMax = 5 * time.Minute
		if cfg.TaskBackoffMax < cfg.TaskBackoff {
			cfg.TaskBackoffMax = cfg.TaskBackoff
		}
	}
	if cfg.LogRetryFor <= 0 {
		cfg.LogRetryFor = 2 * time.Minute
	}
	if cfg.Logf == nil {
		cfg.Logf = log.Printf
	}
	return &Reporter{cfg: cfg, suspended: map[repoKey]time.Time{}, originHeld: map[jobKey]originHold{}, taskHeld: map[taskKey]originHold{}}
}

func wait(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

// Finish is the runner's Finish: sinks gives the Sink of the origin a job came from (Ended.Source). A job whose origin has no sink is
// logged and dropped (its lease will lapse there).
func (r *Reporter) Finish(sinks func(source string) Sink) runner.Finish {
	d := r.Delivery(sinks)
	return func(ctx context.Context, e runner.Ended) { d(ctx, e) }
}

// Delivery is Finish that also says whether the origin took the job's result. True only for a result that was accepted (a job that
// succeeded, failed, was cancelled or was rejected); false for a release, a lost lease, a refusal (409/404) and a delivery that was given
// up (the context ended, the retries ran out). What is recorded about a run belongs to the run whose result the origin took: a run it did
// not take is run again, and that one is the run of the job.
func (r *Reporter) Delivery(sinks func(source string) Sink) func(ctx context.Context, e runner.Ended) bool {
	return func(ctx context.Context, e runner.Ended) bool {
		sink := sinks(e.Source)
		if sink == nil {
			r.cfg.Logf("report: no sink for origin %q; the outcome of %s is dropped", e.Source, e.Job.ID)
			return false
		}
		return r.deliver(ctx, sink, e)
	}
}

// deliver does what the kind of ending calls for, and says whether the origin took a result.
func (r *Reporter) deliver(ctx context.Context, sink Sink, e runner.Ended) bool {
	if e.Kind != runner.KindOrigin {
		r.forgetHold(e.Source, e.Job.ID) // "in a row" ends: this job got its snapshot, whatever came of it
	}
	// A sandboxed run of any outcome can carry an Err that says its unit was not proven stopped (run.Result.Err): what it did cannot
	// be told, so it is not a result of the job whatever the outcome says, and the job goes back as an infrastructure failure. (The
	// workspace is then not reused or removed: that is the runner's, which sees the same Err.) A lost job is dropped as always.
	// A command that is not on the PATH carries an Err too, but says only that: it is held off as a task (ran).
	if e.Run.Err != nil && !commandNotFound(e.Run) && (e.Kind == runner.KindRan || e.Kind == runner.KindCancelled || e.Kind == runner.KindShutdown || e.Kind == runner.KindReclaimed) {
		r.release(ctx, sink, e, wire.ReleaseInfra, fmt.Errorf("the command's unit was not proven stopped: %w", e.Run.Err))
		return false
	}
	switch e.Kind {
	case runner.KindLost:
		// the lease is gone: the origin has the job again or has failed it as lost; whatever this worker saw is not its to report
	case runner.KindCancelled:
		return r.postResult(ctx, sink, e, wire.ResultRequest{ClaimID: e.ClaimID, State: wire.StateCancelled, StartedAt: stamp(e.Run.StartedAt), FinishedAt: stamp(e.Run.FinishedAt)})
	case runner.KindRejected:
		return r.rejected(ctx, sink, e)
	case runner.KindRan:
		if e.Run.Outcome != runs.StartFailed {
			r.forgetTask(e) // the command was found and ran: the run of "not found" is over
		}
		return r.ran(ctx, sink, e)
	case runner.KindInfra:
		r.release(ctx, sink, e, wire.ReleaseInfra, e.Err)
	case runner.KindOrigin:
		r.originFailed(ctx, sink, e)
	case runner.KindReclaimed, runner.KindShutdown:
		// the owner took the machine (Running.Stop), or the worker is stopping: the claim goes back while the lease is still alive, so
		// that a lease that lapses between the stop and the release cannot count an attempt
		r.release(ctx, sink, e, wire.ReleaseReclaim, nil)
	default:
		r.cfg.Logf("report: %s of %s is not something to report", e.Kind, e.Job.ID)
	}
	return false
}

// rejected: the job was not run as submitted. The result is failed with no exit and the reason as the only line of the summary's tail;
// the log is the same line. No reason field: a rejection is not one of oom, timeout or infra, and the job is not given to anyone else.
func (r *Reporter) rejected(ctx context.Context, sink Sink, e runner.Ended) bool {
	reason := "rejected"
	var rej *runner.Rejected
	if errors.As(e.Err, &rej) {
		reason = rej.Reason
	}
	req := wire.ResultRequest{
		ClaimID: e.ClaimID, State: wire.StateFailed,
		Summary: &wire.Summary{Failures: []wire.Failure{}, Tail: []string{reason}},
	}
	if !r.postResult(ctx, sink, e, req) {
		return false
	}
	r.putLog(ctx, sink, e, func() (io.ReadCloser, error) { return io.NopCloser(strings.NewReader(reason + "\n")), nil })
	return true
}

// ran: the command started and ended by itself or by its own timeout.
func (r *Reporter) ran(ctx context.Context, sink Sink, e runner.Ended) bool {
	run := e.Run
	if run.CaptureErr != nil {
		// the log or the summary is incomplete, which a result cannot be built on (spec §5.1): the job goes back
		r.release(ctx, sink, e, wire.ReleaseInfra, fmt.Errorf("the log could not be kept: %w", run.CaptureErr))
		return false
	}
	req := wire.ResultRequest{ClaimID: e.ClaimID, StartedAt: stamp(run.StartedAt), FinishedAt: stamp(run.FinishedAt)}
	switch {
	case run.Outcome == "timed_out" || run.Reason == "timeout":
		req.State, req.Reason = wire.StateFailed, "timeout"
	case run.Reason == "oom":
		// only the sandbox's unit can say so (Linux); a KILL signal alone is not an OOM, and without the unit's word it is a plain failure
		req.State, req.Reason = wire.StateFailed, "oom"
	case run.Outcome == "exited" && run.Signal == "" && run.ExitCode == 0:
		req.State = wire.StateSucceeded
		req.Exit = intp(0)
	case run.Outcome == "exited" && run.Signal == "":
		req.State = wire.StateFailed
		req.Exit = intp(run.ExitCode)
	case run.Outcome == "exited": // ended by a signal: there is no exit status
		req.State = wire.StateFailed
	default: // it never started, or an ending that is not this kind's: not a result of the job
		if commandNotFound(run) {
			r.holdTask(e) // this host lacks the program of this task, which says nothing of the repo's other tasks
		}
		r.release(ctx, sink, e, wire.ReleaseInfra, fmt.Errorf("the command did not run to an end here (%s): %v", run.Outcome, run.Err))
		return false
	}
	s := r.summaryOf(e)
	req.Summary = &s
	if !r.postResult(ctx, sink, e, req) {
		return false
	}
	if run.LogPath != "" {
		r.putLog(ctx, sink, e, func() (io.ReadCloser, error) { return os.Open(run.LogPath) })
	}
	return true
}

func (r *Reporter) summaryOf(e runner.Ended) wire.Summary {
	var s wire.Summary
	if r.cfg.Summary != nil {
		s = r.cfg.Summary(e)
	} else {
		s = r.parse(e)
	}
	if s.Failures == nil {
		s.Failures = []wire.Failure{}
	}
	if s.Tail == nil {
		s.Tail = []string{}
	}
	return s
}

// maxReport is the most of a report file (or the stdout) read to build a summary.
const maxReport = 32 << 20

// parse builds the summary of the task's kind from what the run left in its output directory: the command's stdout (go-json), the
// {report} and {junit} files, and the end of the log for the tail. A report that cannot be read leaves zero counts and the tail.
func (r *Reporter) parse(e runner.Ended) wire.Summary {
	kind := e.Plan.Summary
	if kind == "" {
		kind = "plain"
	}
	in := summary.Input{Log: e.Run.Tail, Root: e.Root, Digest: r.digestOpen()}
	if e.Run.StdoutPath != "" {
		in.Stdout = readCapped(e.Run.StdoutPath)
	}
	if e.Plan.OutDir != "" {
		in.Report = readCapped(filepath.Join(e.Plan.OutDir, "report.json"))
		in.Junit = readCapped(filepath.Join(e.Plan.OutDir, "junit.xml"))
	}
	s, err := summary.Parse(kind, in)
	if err != nil {
		r.cfg.Logf("report: summary of %s (%s): %v", e.Job.ID, kind, err)
	}
	return s
}

// readCapped reads a regular file up to maxReport bytes. A file that is not there, a link, a pipe or a device is empty: the task wrote
// these files, and a pipe would block the worker for ever.
func readCapped(path string) []byte {
	if st, err := os.Lstat(path); err != nil || !st.Mode().IsRegular() {
		return nil
	}
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()
	// one byte more than the limit: a report that long is cut here, and the summary parsers refuse anything over the limit instead of
	// reading a prefix of it as if it were the whole (a prefix that ends on a line would be taken for a complete stream and given a digest)
	b, _ := io.ReadAll(io.LimitReader(f, maxReport+1))
	return b
}

// postResult sends the result until it is taken, refused for good, or given up on; true only when the origin took it. A refused result
// (409, 404) means the claim is not live: the log is not sent either.
func (r *Reporter) postResult(ctx context.Context, sink Sink, e runner.Ended, req wire.ResultRequest) bool {
	var last error
	out := r.retry(ctx, e, "result", func(ctx context.Context) error { last = sink.Result(ctx, e.Job.ID, r.gated(req)); return last })
	if out == abandoned && ctx.Err() != nil && (errors.Is(last, context.Canceled) || errors.Is(last, context.DeadlineExceeded)) {
		// A try was cut short by the lease's context ending: the origin may have taken the result and the answer been lost with the
		// context (a heartbeat that met the finished job and ended the lease is how it happens). A result is idempotent per claim, so one
		// try (retried for a few seconds), on a context of its own, settles it: the origin answers a repeat with the first answer, or, if the claim
		// really is gone, with a 409.
		cctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), confirmTimeout)
		defer cancel()
		if r.retryFor(cctx, e, "result confirmation", confirmTimeout, func(ctx context.Context) error { return sink.Result(ctx, e.Job.ID, r.gated(req)) }) == taken {
			r.cfg.Logf("report: the result of %s was taken after all (confirmed once its lease had ended)", e.Job.ID)
			return true
		}
	}
	return out == taken
}

// digestOpen says whether a summary may carry the passed-test digest now (Config.Digest).
func (r *Reporter) digestOpen() bool { return r.cfg.Digest != nil && r.cfg.Digest() }

// gated is req as it is to be sent now: the summary's digest is kept only while the pool still allows it. The result is retried for
// minutes, and a host of the pool may have been rolled back to a version that rejects the field since the summary was built.
func (r *Reporter) gated(req wire.ResultRequest) wire.ResultRequest {
	if req.Summary == nil || req.Summary.PassedDigest == "" || r.digestOpen() {
		return req
	}
	s := *req.Summary
	s.PassedDigest = ""
	req.Summary = &s
	return req
}

// confirmTimeout bounds the one try that settles a result whose answer was lost with the lease's context.
const confirmTimeout = 5 * time.Second

// putLog uploads the log with the claim of the run, re-opening it for every try. A log that is not there is not an error of the run.
func (r *Reporter) putLog(ctx context.Context, sink Sink, e runner.Ended, open func() (io.ReadCloser, error)) {
	// the lease's ctx may end any moment now (see Config.LogRetryFor): the upload has its own, bounded
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), r.cfg.LogRetryFor)
	defer cancel()
	r.retryFor(ctx, e, "log", r.cfg.LogRetryFor, func(ctx context.Context) error {
		f, err := open()
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				return nil // nothing to upload; the result stands
			}
			return fmt.Errorf("open the log: %w", err)
		}
		defer f.Close()
		return sink.PutLog(ctx, e.Job.ID, e.ClaimID, f)
	})
}

// release gives the claim back. why is told to the origin as the detail.
func (r *Reporter) release(ctx context.Context, sink Sink, e runner.Ended, reason wire.ReleaseReason, why error) {
	if reason == wire.ReleaseInfra && !commandNotFound(e.Run) {
		r.suspend(e.Source, e.Job.Repo.Name) // whether or not the origin takes the release: this host cannot run this repo now
	}
	req := wire.ReleaseRequest{ClaimID: e.ClaimID, Reason: reason}
	if why != nil {
		req.Detail = clip(why.Error(), maxDetail)
	}
	r.retry(ctx, e, "release", func(ctx context.Context) error { return sink.Release(ctx, e.Job.ID, req) })
}

// originFailed gives back a job whose origin answered the snapshot request with an error of its own (#3379). It is not this host's
// failure: the claim goes back with reason reclaim (no attempt counted, no hold-off of the repo) and the detail says it was the
// origin's, with its status and the start of its words; the log says the same, because the origin does not keep the detail. The job,
// not the repo, is then held off for a while.
func (r *Reporter) originFailed(ctx context.Context, sink Sink, e runner.Ended) {
	detail := originDetail(e.Err)
	r.cfg.Logf("report: %s: %s for %s (%s)", e.Source, detail, e.Job.ID, e.Job.Repo.Name)
	r.holdOff(e.Source, e.Job.ID)
	req := wire.ReleaseRequest{ClaimID: e.ClaimID, Reason: wire.ReleaseReclaim, Detail: detail}
	r.retry(ctx, e, "release", func(ctx context.Context) error { return sink.Release(ctx, e.Job.ID, req) })
}

// originDetail is what the origin said, for the release's detail and the log.
func originDetail(err error) string {
	var oe *snapshot.OriginError
	switch {
	case errors.As(err, &oe):
		return fmt.Sprintf("origin snapshot %d: %s", oe.Status, clip(oe.Body, maxDetail))
	case errors.Is(err, snapshot.ErrBusy):
		return "origin snapshot busy"
	case err != nil:
		return "origin snapshot: " + clip(err.Error(), maxDetail)
	}
	return "origin snapshot: failed"
}

// holdOff holds a job of an origin off for the first wait, or twice the last one when it failed again before that was long over,
// up to the longest. A hold that ended long ago starts again at the first wait. An origin failure is rare, so this is where the table
// is swept of the holds that are long over.
func (r *Reporter) holdOff(source, job string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	now := r.cfg.Now()
	for k, h := range r.originHeld {
		if r.longOver(now, h) {
			delete(r.originHeld, k)
		}
	}
	k := jobKey{source, job}
	wait := r.cfg.OriginBackoff
	if old, ok := r.originHeld[k]; ok && !r.longOver(now, old) {
		wait = min(old.wait*2, r.cfg.OriginBackoffMax)
	}
	r.originHeld[k] = originHold{until: now.Add(wait), wait: wait}
}

// commandNotFound says the run never started because its program is not on this host's PATH (run.lookPath): a lack of one tool of one
// task. Any other way not to start (a workspace that cannot be made, a unit systemd could not set up) is the host's, and a run that did
// start is not this.
func commandNotFound(res runs.Result) bool {
	return res.Outcome == runs.StartFailed && errors.Is(res.Err, exec.ErrNotFound)
}

// holdTask holds the task of a job off for the first wait, or twice the last one when it failed the same way again before that was long
// over, up to the longest; the beginning is logged with when the task may be taken again.
func (r *Reporter) holdTask(e runner.Ended) {
	r.mu.Lock()
	now := r.cfg.Now()
	for k, h := range r.taskHeld {
		if r.longOver(now, h) {
			delete(r.taskHeld, k)
		}
	}
	k := taskKey{e.Source, e.Job.Repo.Name, e.Job.Task}
	wait := r.cfg.TaskBackoff
	if old, ok := r.taskHeld[k]; ok && !r.taskLongOver(now, old) {
		wait = min(old.wait*2, r.cfg.TaskBackoffMax)
	}
	h := originHold{until: now.Add(wait), wait: wait}
	r.taskHeld[k] = h
	r.mu.Unlock()
	r.cfg.Logf("report: %s %s/%s: its command is not on this host's PATH; the task is not taken again until %s (%s)",
		k.source, k.repo, k.task, h.until.Format(time.RFC3339), wait)
}

// taskLongOver is longOver for a task hold, which has its own longest wait.
func (r *Reporter) taskLongOver(now time.Time, h originHold) bool {
	return !now.Before(h.until.Add(r.cfg.TaskBackoffMax))
}

// forgetTask ends a task's run of "command not found": a run of it found its command.
func (r *Reporter) forgetTask(e runner.Ended) {
	r.mu.Lock()
	k := taskKey{e.Source, e.Job.Repo.Name, e.Job.Task}
	h, held := r.taskHeld[k]
	delete(r.taskHeld, k)
	r.mu.Unlock()
	if held && !h.ended {
		r.cfg.Logf("report: %s %s/%s: the task ran, so its hold is over", k.source, k.repo, k.task)
	}
}

// TaskHeld says whether the worker must not pick a job of this task now: its command was not found on this host less than the wait ago.
// It is pick.Env.TaskHeld, asked for every candidate of every round. The end of a hold is logged once, when it is first noticed; the entry
// stays (so the next failure doubles) until its run of failures is long over.
func (r *Reporter) TaskHeld(source, repo, task string) bool {
	r.mu.Lock()
	k := taskKey{source, repo, task}
	h, ok := r.taskHeld[k]
	if !ok {
		r.mu.Unlock()
		return false
	}
	now := r.cfg.Now()
	if r.taskLongOver(now, h) {
		delete(r.taskHeld, k)
		r.mu.Unlock()
		return false
	}
	if now.Before(h.until) {
		r.mu.Unlock()
		return true
	}
	told := h.ended
	h.ended = true
	r.taskHeld[k] = h
	r.mu.Unlock()
	if !told {
		r.cfg.Logf("report: %s %s/%s: the hold of the task is over; it may be taken again", source, repo, task)
	}
	return false
}

// longOver says a hold ended more than the longest wait ago: its run of failures is over.
func (r *Reporter) longOver(now time.Time, h originHold) bool {
	return !now.Before(h.until.Add(r.cfg.OriginBackoffMax))
}

// forgetHold ends a job's run of origin failures.
func (r *Reporter) forgetHold(source, job string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.originHeld, jobKey{source, job})
}

// HeldOff says whether the worker must not pick this job of source now: its origin answered the snapshot request wrongly less than the
// wait ago. It is pick.Env.HeldOff, asked for every candidate of every round, so it looks at one entry (and forgets it when its hold is
// long over); the sweep of the others is holdOff's.
func (r *Reporter) HeldOff(source, job string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	now := r.cfg.Now()
	k := jobKey{source, job}
	h, ok := r.originHeld[k]
	if !ok {
		return false
	}
	if r.longOver(now, h) {
		delete(r.originHeld, k)
		return false
	}
	return now.Before(h.until)
}

// outcome is how a delivery ended.
type outcome int

const (
	taken     outcome = iota // the origin took it
	refused                  // the origin will not take it (409, 404): the claim is not live, nothing more is sent for it
	abandoned                // RetryFor passed or ctx ended
)

// retry calls do until it succeeds, the origin refuses for good (a 409 or an unknown job: it has decided), RetryFor has passed, or ctx
// ends (the lease is lost, or the worker is stopping). The pause between tries doubles up to MaxBackoff.
func (r *Reporter) retry(ctx context.Context, e runner.Ended, what string, do func(ctx context.Context) error) outcome {
	return r.retryFor(ctx, e, what, RetryFor, do)
}

// retryFor is retry with its own bound.
func (r *Reporter) retryFor(ctx context.Context, e runner.Ended, what string, bound time.Duration, do func(ctx context.Context) error) outcome {
	deadline := r.cfg.Now().Add(bound)
	pause := r.cfg.Backoff
	for {
		err := do(ctx)
		if err == nil {
			return taken
		}
		var c *runner.Conflict
		if errors.As(err, &c) {
			r.cfg.Logf("report: 409: the origin of %s is in state %s, so this worker's claim %s is no longer live; the %s is dropped", e.Job.ID, c.State, e.ClaimID, what)
			return refused
		}
		if errors.Is(err, runner.ErrNotFound) {
			r.cfg.Logf("report: 404: the origin no longer has job %s (claim %s); the %s is dropped", e.Job.ID, e.ClaimID, what)
			return refused
		}
		if ctx.Err() != nil {
			r.cfg.Logf("report: %s of %s not delivered: %v", what, e.Job.ID, ctx.Err())
			return abandoned
		}
		if !r.cfg.Now().Add(pause).Before(deadline) {
			r.cfg.Logf("report: %s of %s given up after %s: %v", what, e.Job.ID, bound, err)
			return abandoned
		}
		// every failed try says why: a delivery that keeps failing for ten minutes must not be silent until it gives up (the log upload
		// that met a 500 on every try, for a missing directory, showed nothing)
		r.cfg.Logf("report: %s of %s failed, trying again in %s: %v", what, e.Job.ID, pause, err)
		if werr := r.cfg.Wait(ctx, pause); werr != nil {
			r.cfg.Logf("report: %s of %s not delivered: %v", what, e.Job.ID, werr)
			return abandoned
		}
		if pause *= 2; pause > r.cfg.MaxBackoff {
			pause = r.cfg.MaxBackoff
		}
	}
}

// suspend holds a repo of an origin off for SuspendFor from now.
func (r *Reporter) suspend(source, repo string) {
	if repo == "" {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.suspended[repoKey{source, repo}] = r.cfg.Now().Add(r.cfg.SuspendFor)
}

// Suspended says whether the worker must not pick jobs of repo from source now: a job of it ended in an infrastructure failure less
// than SuspendFor ago. It is pick.Env.Suspended. The origin is in the key, so an origin that makes jobs fail cannot hold off another
// origin's repo of the same name; a host that really cannot run a repo fails once per origin before it stops trying them all.
func (r *Reporter) Suspended(source, repo string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	k := repoKey{source, repo}
	until, ok := r.suspended[k]
	if !ok {
		return false
	}
	if !r.cfg.Now().Before(until) {
		delete(r.suspended, k)
		return false
	}
	return true
}

func stamp(t time.Time) *time.Time {
	if t.IsZero() {
		return nil
	}
	u := t.UTC()
	return &u
}

func intp(n int) *int { return &n }

func clip(s string, n int) string {
	s = strings.Join(strings.Fields(s), " ")
	if len(s) > n {
		s = s[:n]
	}
	return s
}
