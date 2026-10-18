package jobsmod

import (
	"context"
	"encoding/json"
	"time"

	"github.com/wake/purdex/internal/core"
	"github.com/wake/purdex/internal/wwd/wire"
)

// EventJobsChanged is the host event the App hears when a job changes (wwd spec §14a): {host, job_id, state}. It is a notice: the
// App re-reads the job (GET /api/pool/jobs/{id}) or the list. Every client of the host events gets it; it is driven by wwd's own
// changes, a few per job, so it needs no opt-in.
const EventJobsChanged = "jobs.changed"

// retryEvery is the pause before the local wwd's event stream is opened again (wwd not running, or the stream ended).
const retryEvery = 5 * time.Second

// watch keeps the local wwd's event stream open until ctx ends and turns its job events into jobs.changed. wwd being absent is
// retried quietly: the failure is logged when it starts and when it changes, not every pause.
func (m *Module) watch(ctx context.Context) {
	last := ""
	for ctx.Err() == nil {
		err := m.source().Watch(ctx, nil, func(ev wire.Event) bool {
			m.onEvent(ev)
			return false
		})
		if err != nil && ctx.Err() == nil {
			if s := err.Error(); s != last {
				last = s
				m.logf("jobs: wwd events: %v", err)
			}
		} else {
			last = ""
		}
		select {
		case <-ctx.Done():
		case <-time.After(m.retry):
		}
	}
}

// onEvent turns one wwd event into jobs.changed: a job event is a change of that job; anything else is not ours.
func (m *Module) onEvent(ev wire.Event) {
	if ev.Type != wire.EventJob {
		return
	}
	je, err := wire.Decode[wire.JobEvent](ev.Data)
	if err != nil {
		m.logf("jobs: wwd sent a job event that does not decode: %v", err)
		return
	}
	payload := wire.JobsChanged{Host: je.Host, JobID: je.Job.ID, State: je.Job.State}
	if payload.Host == "" || payload.JobID == "" || payload.Validate() != nil {
		m.logf("jobs: wwd sent a job event without a host, id or state; not announced")
		return
	}
	b, err := json.Marshal(payload)
	if err != nil {
		return
	}
	m.broadcast(core.HostEvent{Type: EventJobsChanged, Value: string(b)})
}

// broadcast sends ev to the host events (non-blocking: a client that cannot keep up misses a notice and re-reads).
func (m *Module) broadcast(ev core.HostEvent) {
	m.mu.Lock()
	emit, c := m.emit, m.core
	m.mu.Unlock()
	switch {
	case emit != nil:
		emit(ev)
	case c != nil && c.Events != nil:
		c.Events.BroadcastEvent(ev)
	}
}
