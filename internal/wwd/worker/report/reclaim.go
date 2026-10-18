package report

import (
	"context"
	"errors"

	"github.com/wake/purdex/internal/wwd/wire"
	"github.com/wake/purdex/internal/wwd/worker/runner"
	"github.com/wake/purdex/internal/wwd/worker/yield"
)

// Running is a job the worker holds, as Reclaim needs it: where it came from, its claim, and how to stop it.
type Running struct {
	Source  string
	JobID   string
	Claim   string
	StopJob func(ctx context.Context) error // ends the job's processes and returns when none is left
}

// job is Running as yield.Job.
type job struct{ Running }

func (j job) ID() string                     { return j.JobID }
func (j job) ClaimID() string                { return j.Claim }
func (j job) Stop(ctx context.Context) error { return j.StopJob(ctx) }

// releaser gives a job back (reason reclaim) to the sink of the origin it came from.
type releaser struct {
	sinks  func(source string) Sink
	source map[string]string // job id -> origin
}

func (r releaser) Release(ctx context.Context, jobID, claimID string) error {
	sink := r.sinks(r.source[jobID])
	if sink == nil {
		return errors.New("no sink for the origin of " + jobID)
	}
	err := sink.Release(ctx, jobID, wire.ReleaseRequest{ClaimID: claimID, Reason: wire.ReleaseReclaim})
	var c *runner.Conflict
	if errors.As(err, &c) || errors.Is(err, runner.ErrNotFound) {
		return nil // the origin has decided about this claim (it has the job again, or not at all): nothing more to give back
	}
	return err
}

// Reclaim is yield.ReclaimAll for the jobs the worker holds: each is stopped, and once its processes are confirmed gone it is given back
// to its own origin (release, reason reclaim). A job whose stop failed is not given back (yield.ReclaimAll's rule); every error is
// returned, and the worker tries again at its next reading of the owner.
func (r *Reporter) Reclaim(ctx context.Context, jobs []Running, sinks func(source string) Sink) error {
	rel := releaser{sinks: sinks, source: make(map[string]string, len(jobs))}
	ys := make([]yield.Job, len(jobs))
	for i, j := range jobs {
		rel.source[j.JobID] = j.Source
		ys[i] = job{j}
	}
	return yield.ReclaimAll(ctx, ys, rel)
}
