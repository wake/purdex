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
	send  func(sessionID, text string) error
	retry time.Duration // the pause before the event stream is opened again

	mu     sync.Mutex // serialises handling: an event and a catch-up never tell one job twice
	store  *store
	cancel context.CancelFunc
	wg     sync.WaitGroup
}

// New returns the module with production defaults.
func New() *Module {
	return &Module{logf: log.Printf, now: time.Now, dir: gate.DefaultDir(), retry: 5 * time.Second}
}

func (m *Module) Name() string           { return "jobnotify" }
func (m *Module) Dependencies() []string { return []string{"peers"} }

func (m *Module) Init(c *core.Core) error {
	if m.src == nil {
		m.src = client.New(client.DefaultSocket())
	}
	if m.send == nil {
		m.send = func(sessionID, text string) error {
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			return peersmod.NotifySelf(ctx, c, m.logf, sessionID, text)
		}
	}
	m.store = openStore(filepath.Join(m.dir, "wwd-notified.json"), m.now())
	return nil
}

func (m *Module) RegisterRoutes(*http.ServeMux) {}

func (m *Module) Start(context.Context) error {
	ctx, cancel := context.WithCancel(context.Background())
	m.cancel = cancel
	m.wg.Add(1)
	go func() {
		defer m.wg.Done()
		m.run(ctx)
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
		go func() {
			select {
			case <-ready:
				m.catchUp(ctx) // after the stream is open, so a job ending right now is in the stream or in the list
			case <-done:
			}
		}()
		err := m.src.Watch(ctx, ready, func(ev wire.Event) bool {
			m.onEvent(ev)
			return false
		})
		close(done)
		if err != nil && ctx.Err() == nil {
			m.logf("jobnotify: wwd events: %v", err)
		}
		select {
		case <-ctx.Done():
		case <-time.After(m.retry):
		}
	}
}

func (m *Module) onEvent(ev wire.Event) {
	if ev.Type != wire.EventJob {
		return
	}
	je, err := wire.Decode[wire.JobEvent](ev.Data)
	if err != nil {
		return
	}
	m.handle(je.Job)
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
		m.handle(j)
	}
	if err := m.store.advance(began); err != nil {
		m.logf("jobnotify: catch-up window: %v", err)
	}
}

// handle tells the submitter of a job that ended, once. A fast-lane job is its caller's own process: that process prints the
// outcome, so it is not told again. A session that is not live on this host cannot be reached, which is not an error to keep trying.
func (m *Module) handle(j wire.Job) {
	if !j.State.Terminal() || j.Local || j.Submitter.Session == "" {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.store.has(j.ID) {
		return
	}
	if err := m.send(j.Submitter.Session, Text(j)); err != nil {
		m.logf("jobnotify: %s: %v", j.ID, err)
	}
	if err := m.store.mark(j.ID); err != nil {
		m.logf("jobnotify: remember %s: %v", j.ID, err)
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
