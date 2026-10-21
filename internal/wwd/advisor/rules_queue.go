package advisor

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/wake/purdex/internal/wwd/derived"
	"github.com/wake/purdex/internal/wwd/queue"
	"github.com/wake/purdex/internal/wwd/wire"
	"github.com/wake/purdex/internal/wwd/worker/pick"
)

// The four rules of the queue (spec §13.3, WWD-9 S3-3b), level watch: they tell the watcher what the patrol used to see by looking at the
// queue. They judge the queue of this host (the origin of the jobs) against the pool as this host sees it, and they are written only where
// [advisor] queue_rules is on. The alert of each is one for the host, its key the kind's name, its text naming up to three jobs.
const (
	// queueStalledAfter: a job that a free slot of the pool could take, and was not taken for this long.
	queueStalledAfter = 5 * time.Minute
	// queueBacklogAfter: the oldest job that is not on hold has waited this long.
	queueBacklogAfter = 20 * time.Minute
	// queueUnclaimableAfter: a job that no host with its worker on can take, for this long.
	queueUnclaimableAfter = 10 * time.Minute
	// jobRequeuedAt: a job that was put back (and counted) this many times. Attempts already includes every lapse of a lease (queue.QueuedJob).
	jobRequeuedAt = 3

	// queueNamed is how many jobs an alert names.
	queueNamed = 3
)

var errQueueUnavailable = errors.New("the queue or the pool is not available")

// queueInput is what the rules read: the queued jobs (oldest first) and the pool. A replay has neither, and a rule is then unjudged.
func queueInput(in Input) ([]queue.QueuedJob, []wire.Peer, error) {
	if in.Queue == nil || in.Pool == nil {
		return nil, nil, errQueueUnavailable
	}
	jobs, err := in.Queue()
	if err != nil {
		return nil, nil, err
	}
	return jobs, in.Pool(), nil
}

// canTake says whether the host p takes a job that asks for these labels: it answers, its worker is on, and by its labels (and whether it
// takes only explicitly labelled jobs) a worker of its own would take it: pick.Eligible, the rule Rank applies.
func canTake(p wire.Peer, labels []string) bool {
	return p.Reachable && p.Info.Worker.Enabled && pick.Eligible(labels, p.Info.Labels, p.Info.Worker.ExplicitLabelsOnly)
}

// hasRoom says whether the host p has a heavy slot free (a queued job takes a heavy slot): derived.HasHeavyRoom, which the worker's loop
// asks of its own record before it claims.
func hasRoom(p wire.Peer) bool {
	return p.Facts != nil && derived.HasHeavyRoom(*p.Facts)
}

func waited(now, since time.Time) time.Duration {
	if d := now.Sub(since); d > 0 {
		return d
	}
	return 0
}

// named is "id、id、id" for up to queueNamed jobs, and for more the first queueNamed and how many there are in all.
func named(ids []string) string {
	if len(ids) <= queueNamed {
		return strings.Join(ids, "、")
	}
	return fmt.Sprintf("%s 等 %d 件", strings.Join(ids[:queueNamed], "、"), len(ids))
}

// QueueBacklog is "queue backlog": the oldest job that is not on hold has waited 20 minutes or more (counted from its submission).
type QueueBacklog struct{}

func (QueueBacklog) Kind() wire.AlertKind { return wire.AlertQueueBacklog }

func (QueueBacklog) Eval(in Input) (Verdict, error) {
	jobs, _, err := queueInput(in)
	if err != nil {
		return Verdict{}, err
	}
	v := Verdict{Key: string(wire.AlertQueueBacklog), Level: wire.AlertWatch}
	for _, j := range jobs { // oldest first: the first one that is not held is the oldest
		if j.Held {
			continue
		}
		if w := waited(in.Now, j.CreatedAt); w >= queueBacklogAfter {
			v.Holds, v.Detail = true, fmt.Sprintf("佇列積壓：最老的 job %s 已等 %d 分鐘", j.ID, int(w.Minutes()))
		}
		break
	}
	return v, nil
}

// JobRequeued is "job requeued": a job that was put back in the queue three times or more (a job on hold included: it is still coming back).
type JobRequeued struct{}

func (JobRequeued) Kind() wire.AlertKind { return wire.AlertJobRequeued }

func (JobRequeued) Eval(in Input) (Verdict, error) {
	jobs, _, err := queueInput(in)
	if err != nil {
		return Verdict{}, err
	}
	v := Verdict{Key: string(wire.AlertJobRequeued), Level: wire.AlertWatch}
	var ids []string
	for _, j := range jobs {
		if j.Attempts >= jobRequeuedAt {
			ids = append(ids, fmt.Sprintf("%s（%d 次）", j.ID, j.Attempts))
		}
	}
	if len(ids) > 0 {
		v.Holds, v.Detail = true, "job 反覆被放回："+named(ids)
	}
	return v, nil
}

// QueueStalled is "queue stalled": a job that is not on hold has not been taken for 5 minutes or more although a host that could take it has a
// heavy slot free. "Not taken" counts from the submission, and for a job that was already put back (Attempts > 0) from when it last changed
// (queue.QueuedJob.Touched): one put back a minute ago has not been waiting since it was submitted. A job never put back ignores Touched, so
// a person moving it does not start the clock again.
type QueueStalled struct{}

func (QueueStalled) Kind() wire.AlertKind { return wire.AlertQueueStalled }

func (QueueStalled) Eval(in Input) (Verdict, error) {
	jobs, pool, err := queueInput(in)
	if err != nil {
		return Verdict{}, err
	}
	v := Verdict{Key: string(wire.AlertQueueStalled), Level: wire.AlertWatch}
	var ids []string
	var host string
	for _, j := range jobs {
		since := j.CreatedAt
		if j.Attempts > 0 && j.Touched.After(since) {
			since = j.Touched
		}
		if j.Held || waited(in.Now, since) < queueStalledAfter {
			continue
		}
		for _, p := range pool {
			if canTake(p, j.Labels) && hasRoom(p) {
				ids = append(ids, j.ID)
				if host == "" {
					host = p.Info.Alias
				}
				break
			}
		}
	}
	if len(ids) > 0 {
		v.Holds, v.Detail = true, fmt.Sprintf("佇列停滯：%s 有空位，%s 已等超過 %d 分鐘沒被領", host, named(ids), int(queueStalledAfter.Minutes()))
	}
	return v, nil
}

// QueueUnclaimable is "queue unclaimable": a job that is not on hold has waited 10 minutes or more (from the submission) and no host
// that answers with its worker on would take it, by its labels. A host that is down, or has its worker off (a release hold), takes nothing;
// a label that was quarantined is not in the host's labels. It cannot hold with QueueStalled for one job: that one needs a host that takes it.
type QueueUnclaimable struct{}

func (QueueUnclaimable) Kind() wire.AlertKind { return wire.AlertQueueUnclaimable }

func (QueueUnclaimable) Eval(in Input) (Verdict, error) {
	jobs, pool, err := queueInput(in)
	if err != nil {
		return Verdict{}, err
	}
	v := Verdict{Key: string(wire.AlertQueueUnclaimable), Level: wire.AlertWatch}
	var ids []string
	for _, j := range jobs {
		if j.Held || waited(in.Now, j.CreatedAt) < queueUnclaimableAfter {
			continue
		}
		taken := false
		for _, p := range pool {
			if canTake(p, j.Labels) {
				taken = true
				break
			}
		}
		if !taken {
			ids = append(ids, j.ID)
		}
	}
	if len(ids) > 0 {
		v.Holds, v.Detail = true, "沒有主機接得了："+named(ids)
	}
	return v, nil
}
