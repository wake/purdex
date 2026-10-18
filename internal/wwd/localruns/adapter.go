package localruns

import (
	"context"

	"github.com/wake/purdex/internal/wwd/queue"
	"github.com/wake/purdex/internal/wwd/wire"
)

// FromQueue is the service's view of the real job queue: RecordLocal, Ended and Result are the queue's own; Running is the list of
// claimed and running jobs narrowed to the fast-lane ones.
func FromQueue(q *queue.Queue) Queue { return queueAdapter{q} }

type queueAdapter struct{ *queue.Queue }

func (a queueAdapter) Running(ctx context.Context) ([]wire.Job, error) {
	jobs, err := a.List(ctx, queue.Filter{States: []wire.JobState{wire.StateClaimed, wire.StateRunning}})
	if err != nil {
		return nil, err
	}
	out := jobs[:0]
	for _, j := range jobs {
		if j.Local {
			out = append(out, j)
		}
	}
	return out, nil
}
