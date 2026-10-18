// Package jobnotify is the daemon's completion notice for wwd jobs (wwd spec §10): when a job ends, the session that submitted it,
// if it is live on this host, is told once, in addition to the `pdx test` process that may still be waiting. It reads only the local
// wwd (events and the job list on its socket) and sends from the session's own inbox, like the watch and assist modules' notices.
package jobnotify

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"path/filepath"
	"sync"
	"time"

	"github.com/wake/purdex/internal/core"
	"github.com/wake/purdex/internal/gate"
	peersmod "github.com/wake/purdex/internal/module/peers"
	"github.com/wake/purdex/internal/wwd/client"
	"github.com/wake/purdex/internal/wwd/wire"
)

// source is the part of the wwd client the module reads; *client.Client is one.
type source interface {
	Watch(ctx context.Context, ready chan<- struct{}, fn func(wire.Event) (done bool)) error
	Jobs(ctx context.Context, f client.JobsQuery) ([]wire.Job, error)
}

// terminalStates are the states a catch-up asks the list for.
var terminalStates = []wire.JobState{wire.StateSucceeded, wire.StateFailed, wire.StateCancelled, wire.StateSuperseded, wire.StateExpired}

// Module is the job-completion notifier.
type Module struct {
	logf  func(format string, args ...any)
	now   func() time.Time
	dir   string
	src   source
	send  func(ctx context.Context, sessionID, text string) error
	retry time.Duration // the pause before the event stream is opened again
	// redo is how often notices that could not be delivered are tried again, up to maxTries in all.
	redo time.Duration

	mu     sync.Mutex // serialises handling: an event and a catch-up never tell one job twice
	tries  map[string]int
	late   map[string]wire.Job // jobs whose notice could not be delivered yet, by id
	store  *store
	cancel context.CancelFunc
	wg     sync.WaitGroup
}

// New returns the module with production defaults.
func New() *Module {
	return &Module{logf: log.Printf, now: time.Now, dir: gate.DefaultDir(), retry: 5 * time.Second, redo: 30 * time.Second}
}

func (m *Module) Name() string           { return "jobnotify" }
func (m *Module) Dependencies() []string { return []string{"peers"} }

func (m *Module) Init(c *core.Core) error {
	if m.src == nil {
		m.src = client.New(client.DefaultSocket())
	}
	if m.send == nil {
		m.send = func(ctx context.Context, sessionID, text string) error {
			ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
			defer cancel()
			return peersmod.NotifySelf(ctx, c, m.logf, sessionID, text)
		}
	}
	m.store = openStore(filepath.Join(m.dir, "wwd-notified.json"), m.now(), m.logf)
	m.tries, m.late = map[string]int{}, map[string]wire.Job{}
	return nil
}

func (m *Module) RegisterRoutes(*http.ServeMux) {}

func (m *Module) Start(context.Context) error {
	ctx, cancel := context.WithCancel(context.Background())
	m.cancel = cancel
	m.wg.Add(2)
	go func() {
		defer m.wg.Done()
		m.run(ctx)
	}()
	go func() {
		defer m.wg.Done()
		m.redoLoop(ctx)
	}()
	return nil
}

func (m *Module) Stop(context.Context) error {
	if m.cancel != nil {
		m.cancel()
	}
	m.wg.Wait()
	return nil
}

// run keeps the event stream open: wwd not running, or a stream that ended, is retried; every (re)connect first looks for jobs
// that ended while the stream was down.
func (m *Module) run(ctx context.Context) {
	for ctx.Err() == nil {
		ready := make(chan struct{})
		done := make(chan struct{})
		var cu sync.WaitGroup
		cu.Add(1)
		go func() {
			defer cu.Done()
			select {
			case <-ready:
				m.catchUp(ctx) // after the stream is open, so a job ending right now is in the stream or in the list
			case <-done:
			}
		}()
		err := m.src.Watch(ctx, ready, func(ev wire.Event) bool {
			m.onEvent(ctx, ev)
			return false
		})
		close(done)
		cu.Wait() // a catch-up in flight ends with the context, before this loop (and Stop) go on
		if err != nil && ctx.Err() == nil {
			m.logf("jobnotify: wwd events: %v", err)
		}
		select {
		case <-ctx.Done():
		case <-time.After(m.retry):
		}
	}
}

func (m *Module) onEvent(ctx context.Context, ev wire.Event) {
	if ev.Type != wire.EventJob {
		return
	}
	je, err := wire.Decode[wire.JobEvent](ev.Data)
	if err != nil {
		return
	}
	m.handle(ctx, je.Job, false)
}

// catchUp tells the jobs that ended since the last complete catch-up and were not told. Every state is listed before anything is
// handled, and the window moves only when all of them were read: a list that failed leaves the window where it was, so the next
// try still sees what ended meanwhile. The window moves to when this catch-up began, not to what the events carried.
func (m *Module) catchUp(ctx context.Context) {
	began := m.now()
	since := m.store.since()
	var ended []wire.Job
	for _, st := range terminalStates {
		jobs, err := m.src.Jobs(ctx, client.JobsQuery{State: st})
		if err != nil {
			if ctx.Err() == nil {
				m.logf("jobnotify: catch-up %s: %v", st, err)
			}
			return
		}
		for _, j := range jobs {
			if j.Result != nil && j.Result.FinishedAt != nil && j.Result.FinishedAt.After(since) {
				ended = append(ended, j)
			}
		}
	}
	for _, j := range ended {
		m.handle(ctx, j, false)
	}
	// the window does not pass a job whose notice is still waiting: after a restart the catch-up must find it again
	bound := began
	m.mu.Lock()
	for _, j := range m.late {
		if j.Result != nil && j.Result.FinishedAt != nil && j.Result.FinishedAt.Before(bound) {
			bound = j.Result.FinishedAt.Add(-time.Nanosecond)
		}
	}
	m.mu.Unlock()
	if err := m.store.advance(bound); err != nil {
		m.logf("jobnotify: catch-up window: %v", err)
	}
}

// maxTries is how many times a notice is tried in all: a session that was only just started may not have its inbox yet, one that
// is not on this host never will.
const maxTries = 6

// handle tells the submitter of a job that ended, once. A notice that cannot be delivered is tried again by redoLoop, up to
// maxTries; then it is given up (a session that is not live on this host cannot be reached) and the job counts as told.
func (m *Module) handle(ctx context.Context, j wire.Job, retry bool) {
	if !j.State.Terminal() || j.Submitter.Session == "" {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.store.has(j.ID) {
		return
	}
	if _, waiting := m.late[j.ID]; waiting && !retry {
		return // redoLoop owns it
	}
	if err := m.send(ctx, j.Submitter.Session, Text(j)); err != nil {
		if m.tries[j.ID]++; m.tries[j.ID] < maxTries && ctx.Err() == nil {
			m.late[j.ID] = j
			m.logf("jobnotify: %s: %v (try %d, again later)", j.ID, err, m.tries[j.ID])
			return
		}
		m.logf("jobnotify: %s: %v (given up)", j.ID, err)
	}
	delete(m.late, j.ID)
	delete(m.tries, j.ID)
	if err := m.store.mark(j.ID); err != nil {
		m.logf("jobnotify: remember %s: %v", j.ID, err)
	}
}

// redoLoop tries again the notices that could not be delivered.
func (m *Module) redoLoop(ctx context.Context) {
	t := time.NewTicker(m.redo)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			m.redoOnce(ctx)
		}
	}
}

func (m *Module) redoOnce(ctx context.Context) {
	m.mu.Lock()
	jobs := make([]wire.Job, 0, len(m.late))
	for _, j := range m.late {
		jobs = append(jobs, j)
	}
	m.mu.Unlock()
	for _, j := range jobs {
		m.handle(ctx, j, true)
	}
}

// Text is the notice for a job that ended.
func Text(j wire.Job) string {
	what := string(j.State)
	r := j.Result
	switch {
	case r != nil && r.CacheHit:
		what = "命中快取（通過）"
	case j.State == wire.StateSucceeded:
		what = "通過"
	case j.State == wire.StateFailed:
		what = "失敗"
		if r != nil {
			if r.Exit != nil {
				what += fmt.Sprintf("（exit %d）", *r.Exit)
			}
			if r.Reason != "" {
				what += "：" + r.Reason
			}
		}
	case j.State == wire.StateCancelled:
		what = "已取消"
	case j.State == wire.StateSuperseded:
		what = "被取代"
		if r != nil && r.SupersededBy != nil {
			what += "（" + *r.SupersededBy + "）"
		}
	case j.State == wire.StateExpired:
		what = "排隊逾時，未執行"
	}
	return fmt.Sprintf("[wwd] %s %s（%s）— pdx jobs show %s", j.Task, what, j.ID, j.ID)
}
