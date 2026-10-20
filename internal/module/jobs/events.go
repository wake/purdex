package jobsmod

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"time"

	"github.com/wake/purdex/internal/core"
	"github.com/wake/purdex/internal/wwd/client"
	"github.com/wake/purdex/internal/wwd/wire"
)

// EventJobsChanged is the host event the App hears when a job changes (wwd spec §14a): {host, job_id, state}. It is a notice: the
// App re-reads the job (GET /api/pool/jobs/{id}) or the list. Every client of the host events gets it; it is driven by wwd's own
// changes, a few per job, so it needs no opt-in.
const EventJobsChanged = "jobs.changed"

// retryEvery is the pause before the local wwd's event stream is opened again (wwd not running, or the stream ended).
const retryEvery = 5 * time.Second

const (
	// pageSize is how many changed jobs one catch-up read asks for.
	pageSize = 1000
	// maxCatchUp is the most jobs.changed a catch-up sends after the stream was down: the newest changes. The App also re-reads the list
	// when its host-events connection comes back; this only says which jobs moved while only this daemon's link to wwd was cut.
	maxCatchUp = 200
)

// watch keeps the local wwd's event stream open until ctx ends and turns its job events into jobs.changed. wwd's stream does not
// replay what happened while it was down, so every time the stream is open, the changes since the last read are looked up in the
// job list (catchUp). wwd being absent is retried quietly: the failure is logged when it starts and when it changes, not every pause.
func (m *Module) watch(ctx context.Context) {
	last := ""
	for ctx.Err() == nil {
		ready := make(chan struct{})
		done := make(chan struct{})
		var cu sync.WaitGroup
		cu.Add(1)
		go func() {
			defer cu.Done()
			select {
			case <-ready:
				m.catchUp(ctx) // after the stream is open, so a change right now is in the stream or in the list
			case <-done:
				select { // a stream that opened and ended at once still gets its look
				case <-ready:
					m.catchUp(ctx)
				default:
				}
			}
		}()
		err := m.source().Watch(ctx, ready, func(ev wire.Event) bool {
			m.onEvent(ev)
			return false
		})
		close(done)
		cu.Wait()
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

// catchUp reads what changed in wwd since the cursor of the last read and announces the newest maxCatchUp of those jobs. The first
// read of this daemon's life only takes the cursor (what is there already is not news). A different database epoch, or a cursor past
// wwd's head, is a wwd.db made again: it is read from the start. An answer without an epoch under a known one is not trusted: nothing
// is announced and nothing moves until an epoch comes again. A job whose live event was also seen is announced twice; the App re-reads.
func (m *Module) catchUp(ctx context.Context) {
	m.mu.Lock()
	cursor, epoch, based := m.cursor, m.epoch, m.based
	m.mu.Unlock()
	if !based || cursor == "" {
		cursor = "0"
	}
	silent := !based
	restarted := false
	var newest []wire.Job
	for {
		page, err := m.source().JobsAfter(ctx, cursor, pageSize)
		var ahead *client.CursorAheadError
		switch {
		case errors.As(err, &ahead) || (err == nil && epoch != "" && page.Epoch != "" && page.Epoch != epoch):
			if restarted {
				m.logf("jobs: catch-up: wwd's database changed again; left to the next one")
				return
			}
			restarted, cursor, epoch, silent, newest = true, "0", "", false, nil // another database: all of it is news
			continue
		case err != nil:
			if ctx.Err() == nil {
				m.logf("jobs: catch-up after %s: %v", cursor, err)
			}
			return
		case epoch != "" && page.Epoch == "":
			m.logf("jobs: catch-up: wwd answered without an epoch although the cursor has %s; waiting for a wwd that has one", epoch)
			return
		}
		if !silent {
			newest = append(newest, page.Jobs...)
			if len(newest) > maxCatchUp {
				newest = append([]wire.Job(nil), newest[len(newest)-maxCatchUp:]...)
			}
		}
		cursor = page.Cursor
		if page.Epoch != "" {
			epoch = page.Epoch
		}
		if len(page.Jobs) < pageSize {
			break
		}
	}
	m.mu.Lock()
	m.cursor, m.epoch, m.based = cursor, epoch, true
	m.mu.Unlock()
	for _, j := range newest {
		m.announce(m.hostOf(j), j)
	}
}

// onEvent turns one wwd event into jobs.changed: a job event is a change of that job; anything else is not ours.
func (m *Module) onEvent(ev wire.Event) {
	if ev.Type != wire.EventJob {
		return
	}
	je, err := wire.DecodeLenient[wire.JobEvent](ev.Data)
	if err != nil {
		m.logf("jobs: wwd sent a job event that does not decode: %v", err)
		return
	}
	m.announce(je.Host, je.Job)
}

// hostOf is the host a job of the local wwd's list is on: its origin (the list is the local wwd's own).
func (m *Module) hostOf(j wire.Job) string { return j.Origin }

// announce sends jobs.changed for a job, if host, id and state are usable.
func (m *Module) announce(host string, j wire.Job) {
	payload := wire.JobsChanged{Host: host, JobID: j.ID, State: j.State}
	if payload.Host == "" || payload.JobID == "" || payload.Validate() != nil {
		m.logf("jobs: wwd sent a job without a host, id or state; not announced")
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
