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
	"path/filepath"
	"strings"
	"time"

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
	// Summary builds the summary of a run that ended. Nil: built from the files the run left (see parse).
	Summary func(runner.Ended) wire.Summary
	Logf    func(format string, args ...any)
}

// Reporter delivers the outcome of jobs.
type Reporter struct{ cfg Config }

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
	if cfg.Logf == nil {
		cfg.Logf = log.Printf
	}
	return &Reporter{cfg: cfg}
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
	return func(ctx context.Context, e runner.Ended) {
		sink := sinks(e.Source)
		if sink == nil {
			r.cfg.Logf("report: no sink for origin %q; the outcome of %s is dropped", e.Source, e.Job.ID)
			return
		}
		r.deliver(ctx, sink, e)
	}
}

// deliver does what the kind of ending calls for.
func (r *Reporter) deliver(ctx context.Context, sink Sink, e runner.Ended) {
	switch e.Kind {
	case runner.KindLost:
		// the lease is gone: the origin has the job again or has failed it as lost; whatever this worker saw is not its to report
	case runner.KindCancelled:
		r.postResult(ctx, sink, e, wire.ResultRequest{ClaimID: e.ClaimID, State: wire.StateCancelled, StartedAt: stamp(e.Run.StartedAt), FinishedAt: stamp(e.Run.FinishedAt)})
	case runner.KindRejected:
		r.rejected(ctx, sink, e)
	case runner.KindRan:
		r.ran(ctx, sink, e)
	case runner.KindInfra:
		r.release(ctx, sink, e, wire.ReleaseInfra, e.Err)
	case runner.KindShutdown:
		r.release(ctx, sink, e, wire.ReleaseReclaim, nil)
	default:
		r.cfg.Logf("report: %s of %s is not something to report", e.Kind, e.Job.ID)
	}
}

// rejected: the job was not run as submitted. The result is failed with no exit and the reason as the only line of the summary's tail;
// the log is the same line. No reason field: a rejection is not one of oom, timeout or infra, and the job is not given to anyone else.
func (r *Reporter) rejected(ctx context.Context, sink Sink, e runner.Ended) {
	reason := "rejected"
	var rej *runner.Rejected
	if errors.As(e.Err, &rej) {
		reason = rej.Reason
	}
	req := wire.ResultRequest{
		ClaimID: e.ClaimID, State: wire.StateFailed,
		Summary: &wire.Summary{Failures: []wire.Failure{}, Tail: []string{reason}},
	}
	if r.postResult(ctx, sink, e, req) {
		r.putLog(ctx, sink, e, func() (io.ReadCloser, error) { return io.NopCloser(strings.NewReader(reason + "\n")), nil })
	}
}

// ran: the command started and ended by itself or by its own timeout.
func (r *Reporter) ran(ctx context.Context, sink Sink, e runner.Ended) {
	run := e.Run
	if run.CaptureErr != nil {
		// the log or the summary is incomplete, which a result cannot be built on (spec §5.1): the job goes back
		r.release(ctx, sink, e, wire.ReleaseInfra, fmt.Errorf("the log could not be kept: %w", run.CaptureErr))
		return
	}
	req := wire.ResultRequest{ClaimID: e.ClaimID, StartedAt: stamp(run.StartedAt), FinishedAt: stamp(run.FinishedAt)}
	switch {
	case run.Outcome == "timed_out":
		req.State, req.Reason = wire.StateFailed, "timeout"
	case run.Outcome == "exited" && run.Signal == "" && run.ExitCode == 0:
		req.State = wire.StateSucceeded
		req.Exit = intp(0)
	case run.Outcome == "exited" && run.Signal == "":
		req.State = wire.StateFailed
		req.Exit = intp(run.ExitCode)
	case run.Outcome == "exited": // ended by a signal: there is no exit status
		req.State = wire.StateFailed
	default: // it never started, or an ending that is not this kind's: not a result of the job
		r.release(ctx, sink, e, wire.ReleaseInfra, fmt.Errorf("the command did not run to an end here (%s): %v", run.Outcome, run.Err))
		return
	}
	s := r.summaryOf(e)
	req.Summary = &s
	if r.postResult(ctx, sink, e, req) && run.LogPath != "" {
		r.putLog(ctx, sink, e, func() (io.ReadCloser, error) { return os.Open(run.LogPath) })
	}
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
	in := summary.Input{Log: e.Run.Tail, Root: e.Root}
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

// readCapped reads a file up to maxReport bytes; a file that is not there is empty.
func readCapped(path string) []byte {
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()
	b, _ := io.ReadAll(io.LimitReader(f, maxReport))
	return b
}

// postResult sends the result until it is taken, refused for good, or given up on; true means the origin has it (or has decided).
func (r *Reporter) postResult(ctx context.Context, sink Sink, e runner.Ended, req wire.ResultRequest) bool {
	return r.retry(ctx, e, "result", func(ctx context.Context) error { return sink.Result(ctx, e.Job.ID, req) })
}

// putLog uploads the log with the claim of the run, re-opening it for every try. A log that is not there is not an error of the run.
func (r *Reporter) putLog(ctx context.Context, sink Sink, e runner.Ended, open func() (io.ReadCloser, error)) {
	r.retry(ctx, e, "log", func(ctx context.Context) error {
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
	req := wire.ReleaseRequest{ClaimID: e.ClaimID, Reason: reason}
	if why != nil {
		req.Detail = clip(why.Error(), maxDetail)
	}
	r.retry(ctx, e, "release", func(ctx context.Context) error { return sink.Release(ctx, e.Job.ID, req) })
}

// retry calls do until it succeeds, the origin refuses for good (a 409 or an unknown job: it has decided), RetryFor has passed, or ctx
// ends (the lease is lost, or the worker is stopping). The pause between tries doubles up to MaxBackoff. It returns whether the origin
// has taken the call or decided.
func (r *Reporter) retry(ctx context.Context, e runner.Ended, what string, do func(ctx context.Context) error) bool {
	deadline := r.cfg.Now().Add(RetryFor)
	pause := r.cfg.Backoff
	for {
		err := do(ctx)
		if err == nil {
			return true
		}
		var c *runner.Conflict
		if errors.As(err, &c) {
			r.cfg.Logf("report: 409: the origin of %s is in state %s, so this worker's claim %s is no longer live; the %s is dropped", e.Job.ID, c.State, e.ClaimID, what)
			return true
		}
		if errors.Is(err, runner.ErrNotFound) {
			r.cfg.Logf("report: 404: the origin no longer has job %s (claim %s); the %s is dropped", e.Job.ID, e.ClaimID, what)
			return true
		}
		if ctx.Err() != nil {
			r.cfg.Logf("report: %s of %s not delivered: %v", what, e.Job.ID, ctx.Err())
			return false
		}
		if !r.cfg.Now().Add(pause).Before(deadline) {
			r.cfg.Logf("report: %s of %s given up after %s: %v", what, e.Job.ID, RetryFor, err)
			return false
		}
		if werr := r.cfg.Wait(ctx, pause); werr != nil {
			r.cfg.Logf("report: %s of %s not delivered: %v", what, e.Job.ID, werr)
			return false
		}
		if pause *= 2; pause > r.cfg.MaxBackoff {
			pause = r.cfg.MaxBackoff
		}
	}
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
