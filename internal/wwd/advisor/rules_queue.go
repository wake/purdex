package advisor

import (
	"errors"
	"fmt"
	"strings"
	"sync"
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

// hasRoom says whether the host p has a heavy slot free for job j (a queued job takes a heavy slot): derived.HeavyRoomFor, the worker's own
// reading of its facts. A peer whose facts have not come has none.
func hasRoom(p wire.Peer, j queue.QueuedJob) bool {
	return p.Facts != nil && derived.HeavyRoomFor(*p.Facts, j.Class, j.Repo)
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
	// the jobs by the first host that could take each, in the order the hosts were first named: a job is told with the host it waits on
	var hosts []string
	byHost := map[string][]string{}
	for _, j := range jobs {
		since := j.CreatedAt
		if j.Attempts > 0 && j.Touched.After(since) {
			since = j.Touched
		}
		if j.Held || waited(in.Now, since) < queueStalledAfter {
			continue
		}
		for _, p := range pool {
			if canTake(p, j.Labels) && hasRoom(p, j) {
				if _, ok := byHost[p.Info.Alias]; !ok {
					hosts = append(hosts, p.Info.Alias)
				}
				byHost[p.Info.Alias] = append(byHost[p.Info.Alias], j.ID)
				break
			}
		}
	}
	if len(hosts) == 1 {
		v.Holds, v.Detail = true, fmt.Sprintf("佇列停滯：%s 有空位，%s 已等超過 %d 分鐘沒被領", hosts[0], named(byHost[hosts[0]]), int(queueStalledAfter.Minutes()))
	} else if len(hosts) > 1 {
		parts := make([]string, 0, len(hosts))
		for _, h := range hosts {
			parts = append(parts, fmt.Sprintf("%s 有空位，%s", h, named(byHost[h])))
		}
		v.Holds, v.Detail = true, fmt.Sprintf("佇列停滯：%s；以上已等超過 %d 分鐘沒被領", strings.Join(parts, "；"), int(queueStalledAfter.Minutes()))
	}
	return v, nil
}

// QueueUnclaimable is "queue unclaimable": a job that is not on hold has waited 10 minutes or more (from the submission) and no host
// that answers with its worker on would take it, by its labels, for 10 minutes running. A host that is down, or has its worker off (a
// release hold), takes nothing; a label that was quarantined is not in the host's labels. It cannot hold with QueueStalled for one job:
// that one needs a host that takes it.
//
// "For 10 minutes running" is judged by the rule itself, not by one look at the pool: a host answers /v1/info within 3 seconds, and under
// a heavy load (a simulator run ending, load1 50 to 80) one answer is late and the host is unreachable for one poll (#3941). So the rule
// remembers, per job, since when no host has taken it, and a look that finds a host that takes it starts the ten minutes again. The
// memory is the rule's own and lives in the process: after a restart the ten minutes start again (the alert comes later, never wrongly).
// The zero value is ready to use; Eval is safe to call from several goroutines.
type QueueUnclaimable struct {
	mu sync.Mutex
	// since is, per job id, when the first look of the current run of looks found no host that takes it.
	since map[string]time.Time
}

func (*QueueUnclaimable) Kind() wire.AlertKind { return wire.AlertQueueUnclaimable }

func (r *QueueUnclaimable) Eval(in Input) (Verdict, error) {
	jobs, pool, err := queueInput(in)
	if err != nil {
		return Verdict{}, err
	}
	v := Verdict{Key: string(wire.AlertQueueUnclaimable), Level: wire.AlertWatch}
	r.mu.Lock()
	defer r.mu.Unlock()
	next := make(map[string]time.Time, len(jobs)) // a job that is gone from the queue is forgotten
	var ids []string
	for _, j := range jobs {
		if j.Held {
			continue
		}
		taken := false
		for _, p := range pool {
			if canTake(p, j.Labels) {
				taken = true
				break
			}
		}
		if taken {
			continue
		}
		since, ok := r.since[j.ID]
		if !ok {
			since = in.Now
		}
		// a change of the job between two looks (put on hold and released, its labels changed, put back) starts its state anew, though no look saw it
		if j.Touched.After(since) {
			since = j.Touched
		}
		next[j.ID] = since
		if waited(in.Now, j.CreatedAt) >= queueUnclaimableAfter && waited(in.Now, since) >= queueUnclaimableAfter {
			ids = append(ids, j.ID)
		}
	}
	r.since = next
	if len(ids) > 0 {
		v.Holds, v.Detail = true, "沒有主機接得了："+named(ids)
	}
	return v, nil
}
