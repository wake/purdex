package daemon

import (
	"context"
	"log"
	"slices"
	"time"

	"github.com/wake/purdex/internal/wwd/labels"
	"github.com/wake/purdex/internal/wwd/queue"
	"github.com/wake/purdex/internal/wwd/wire"
)

// reconcileWindow is how far back ReconcileOutcomes looks for ended jobs whose outcome was not recorded, and reconcileBatch how many it
// takes up at a time (newest first).
const (
	reconcileWindow = time.Hour
	reconcileBatch  = 200
)

// ReconcileOutcomes records again the outcomes of jobs that ended in the last hour and were never recorded: the write that follows a
// result can fail (a locked database), and a worker's repeat of the result is an echo that offers nothing again, so a missed outcome would
// be a missed disagreement for good. The jobs keep what the record needs; recording is idempotent, so a job recorded meanwhile is not
// recorded twice. Called from the queue's sweep, every few seconds; with nothing missed it is one cheap query.
func (d *Daemon) ReconcileOutcomes(ctx context.Context) {
	if d.opts.Queue == nil {
		return
	}
	missed, err := d.opts.Queue.UnrecordedOutcomes(ctx, reconcileWindow, reconcileBatch)
	if err != nil {
		if ctx.Err() == nil {
			log.Printf("wwd: outcomes: looking for outcomes that were not recorded: %v", err)
		}
		return
	}
	// the list is newest first (so a pile of old ones cannot hide the recent), but they are recorded oldest first, as they ended, so a pair
	// is "the earlier job, then the later one" here as it is when the outcomes are recorded as the jobs end
	for i := len(missed) - 1; i >= 0; i-- {
		d.NoteOutcome(ctx, missed[i].Job, missed[i].Produced)
	}
}

// NoteOutcome records how a job a worker ended came out, for the comparison with the same work on another host (spec §7 Disagreement); it
// is the queue's Options.OnEnded. The outcome counts only if the host vouches for the task: it held the task's earned label when it
// claimed the job (the claim's own labels), or it is the task's reference host. A host with neither is not compared: it has no label to
// lose. A job from before the definition hash was kept (schema 7) or a claim that said nothing is not recorded either.
//
// When the record makes a pair with an earlier outcome that came out differently, the disagreement is written by the queue and logged here,
// with everything needed to look it up later; the host whose label it concerns reads it from here and quarantines its own label.
func (d *Daemon) NoteOutcome(ctx context.Context, j wire.Job, p queue.Produced) {
	if d.opts.Queue == nil || j.Result == nil || j.Result.Exit == nil {
		return
	}
	host := j.Result.Host
	if host == "" || p.DefHash == "" || len(p.Labels) == 0 {
		return
	}
	label := labels.TaskLabel(j.Repo.Name, j.Task)
	var standing string
	switch {
	case host == d.referenceHost(j.Repo.Name, j.Task):
		standing = queue.StandingReference
	case slices.Contains(p.Labels, label):
		standing = queue.StandingEarned
	default:
		return
	}
	// the origin's own clock, as for everything the queue orders and keeps by time: the worker's finished_at is the worker's, and a clock more
	// than the retention behind would have the record swept at once (a clock ahead, kept too long)
	at := d.reuseNow()
	found, err := d.opts.Queue.RecordOutcome(ctx, queue.Outcome{
		JobID: j.ID, Host: host, Label: label, Tree: j.Snapshot.Tree, Task: j.Task, Args: j.Args, DefHash: p.DefHash,
		OK: *j.Result.Exit == 0, Standing: standing, At: at,
	})
	if err != nil {
		log.Printf("wwd: outcomes: recording the outcome of %s: %v", j.ID, err)
		return
	}
	for _, f := range found {
		log.Printf("wwd: outcomes: %s disagrees with %s on %s (tree %s): jobs %s and %s; the label of %s is to be quarantined",
			f.Host, f.OtherHost, f.Label, f.Tree, f.Jobs[0], f.Jobs[1], f.Host)
	}
}
