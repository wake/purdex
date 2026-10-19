package daemon

import (
	"context"
	"log"
	"slices"

	"github.com/wake/purdex/internal/wwd/labels"
	"github.com/wake/purdex/internal/wwd/queue"
	"github.com/wake/purdex/internal/wwd/wire"
)

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
