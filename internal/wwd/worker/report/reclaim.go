package report

import (
	"context"
	"errors"
	"strconv"

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

// held is a Running under the worker-unique token yield.ReclaimAll sees as the job's id. The real job id is unique only within its
// origin and the claim id is the origin's own choice, so neither (nor both) can route a release safely: the token is the position in
// this call's list, and the origin, job id and claim of that position are what the release is made with.
type held struct {
	Running
	token string
}

func (h held) ID() string                     { return h.token }
func (h held) ClaimID() string                { return h.Claim }
func (h held) Stop(ctx context.Context) error { return h.StopJob(ctx) }

// releaser gives a job back (reason reclaim) to the sink of the origin it came from.
type releaser struct {
	sinks func(source string) Sink
	by    map[string]Running // token -> the job
}

func (r releaser) Release(ctx context.Context, token, claimID string) error {
	j, ok := r.by[token]
	if !ok || j.Claim != claimID {
		return errors.New("reclaim: a release for a job that was not asked about")
	}
	sink := r.sinks(j.Source)
	if sink == nil {
		return errors.New("no sink for the origin of " + j.JobID)
	}
	err := sink.Release(ctx, j.JobID, wire.ReleaseRequest{ClaimID: claimID, Reason: wire.ReleaseReclaim})
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
	rel := releaser{sinks: sinks, by: make(map[string]Running, len(jobs))}
	ys := make([]yield.Job, len(jobs))
	for i, j := range jobs {
		token := strconv.Itoa(i)
		rel.by[token] = j
		ys[i] = held{Running: j, token: token}
	}
	return yield.ReclaimAll(ctx, ys, rel)
}
