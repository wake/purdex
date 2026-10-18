// Package jobnotify is the daemon's completion notice for wwd jobs (wwd spec §10): when a job ends, the session that submitted it,
// if it is live on this host, is told once, in addition to the `pdx test` process that may still be waiting. It reads only the local
// wwd (events and the job list on its socket) and sends from the session's own inbox, like the watch and assist modules' notices.
//
// Every notice goes through an outbox table (store.go): a job that ended is inserted once, by job id, and delivered from there with
// its tries counted in the table. Nothing about "already told" lives in memory or in a time cursor, so reconnects, restarts and
// overlapping catch-ups can neither lose a notice nor send it twice. One window remains: a crash after a send succeeded and before
// its row says so sends that notice once more (at-least-once); the try was counted first, so it cannot repeat for ever.
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
	logf   func(format string, args ...any)
	now    func() time.Time
	dbPath string // empty: jobnotify.db in the daemon's data directory
	src    source
	send   func(ctx context.Context, sessionID, text string) error
	retry  time.Duration // the pause before the event stream is opened again
	redo   time.Duration // how often the notices still pending are delivered

	// kick asks the delivery loop for a pass. That loop is the only sender, so a notice is never sent twice at once and a slow send
	// never holds the event stream (whose callback only queues).
	kick   chan struct{}
	store  *store
	cancel context.CancelFunc
	wg     sync.WaitGroup
}

// New returns the module with production defaults.
func New() *Module {
	return &Module{logf: log.Printf, now: time.Now, retry: 5 * time.Second, redo: 30 * time.Second, kick: make(chan struct{}, 1)}
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
	path := m.dbPath
	if path == "" {
		path = filepath.Join(c.Cfg.DataDir, "jobnotify.db")
	}
	st, err := openStore(path)
	if err != nil {
		return err
	}
	m.store = st
	// the first run starts now: it does not tell a whole history. Without a window that was written there is no safe start, so the
	// module does not start at all.
	if _, ok, err := st.since(); err != nil {
		st.close()
		return fmt.Errorf("job notice window: %w", err)
	} else if !ok {
		if err := st.setSince(m.now()); err != nil {
			st.close()
			return fmt.Errorf("job notice window: %w", err)
		}
	}
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
	if m.store != nil {
		return m.store.close()
	}
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
	if _, ok := m.enqueue(je.Job); ok {
		m.wake() // the stream's callback only queues: a slow send must not stop it reading
	}
}

// catchUp finds the jobs that ended since the last complete catch-up, less the overlap, and queues the ones not yet in the outbox.
// Every state is listed before anything is queued, and the window moves only when all of them were read. The window is only a
// hint of how far back to look: what was already queued is ignored by job id, and what was queued but not delivered is kept by the
// outbox whatever the window does.
func (m *Module) catchUp(ctx context.Context) {
	began := m.now()
	since, ok, err := m.store.since()
	if err != nil {
		m.logf("jobnotify: catch-up window: %v", err)
		return
	}
	from := time.Time{}
	if ok {
		from = since.Add(-overlap)
	}
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
			if j.Result != nil && j.Result.FinishedAt != nil && !j.Result.FinishedAt.Before(from) {
				ended = append(ended, j)
			}
		}
	}
	for _, j := range ended {
		m.enqueue(j)
	}
	if err := m.store.setSince(began); err != nil {
		m.logf("jobnotify: catch-up window: %v", err)
	}
	m.wake()
}

// enqueue puts the notice of a job that ended into the outbox, once. A fast-lane job is its caller's own process: pdx test prints
// the outcome (and Claude Code reports a backgrounded one), so only a lost run is told — its caller was killed and could not report.
func (m *Module) enqueue(j wire.Job) (string, bool) {
	if !j.State.Terminal() || j.Submitter.Session == "" || (j.Local && !lost(j)) {
		return "", false
	}
	finished := m.now()
	if j.Result != nil && j.Result.FinishedAt != nil {
		finished = *j.Result.FinishedAt
	}
	added, err := m.store.enqueue(j.ID, j.Submitter.Session, Text(j), string(j.State), finished, m.now())
	if err != nil {
		m.logf("jobnotify: queue %s: %v", j.ID, err)
		return "", false
	}
	return j.ID, added
}

// lost reports whether the job failed because its runner was lost (the lease ran out).
func lost(j wire.Job) bool {
	return j.State == wire.StateFailed && j.Result != nil && j.Result.Reason == "lost"
}

// wake asks the delivery loop for a pass over the notices not yet tried.
func (m *Module) wake() {
	select {
	case m.kick <- struct{}{}:
	default:
	}
}

// deliverPending tries the notices still pending; fresh limits it to those never tried (a pass after an event or a catch-up), so a
// burst of events does not spend the tries of the notices that failed. It runs only in the delivery loop (and in tests).
func (m *Module) deliverPending(ctx context.Context, fresh bool) {
	list, err := m.store.pending(m.now())
	if err != nil {
		m.logf("jobnotify: pending notices: %v", err)
		return
	}
	for _, n := range list {
		if ctx.Err() != nil {
			return
		}
		if fresh && n.Tries > 0 {
			continue
		}
		m.try(ctx, n)
	}
}

// try makes one try of a notice. The try is counted in the table before the send. The sixth failed try closes the notice as given
// up: the result is in wwd and `pdx jobs` shows it, the notice was only a reminder.
func (m *Module) try(ctx context.Context, n notice) {
	tries, err := m.store.beginTry(n.JobID, m.now())
	if err != nil {
		m.logf("jobnotify: count a try of %s: %v", n.JobID, err)
		return
	}
	sendErr := m.send(ctx, n.Session, n.Text)
	switch {
	case sendErr == nil:
		err = m.store.settle(n.JobID, statusSent, "", m.now())
	case tries >= maxTries:
		m.logf("jobnotify: %s: session %s not told, given up after %d tries: %v", n.JobID, n.Session, tries, sendErr)
		err = m.store.settle(n.JobID, statusGivenUp, sendErr.Error(), m.now())
	default:
		m.logf("jobnotify: %s: %v (try %d, again later)", n.JobID, sendErr, tries)
		err = m.store.settle(n.JobID, statusPending, sendErr.Error(), m.now())
	}
	if err != nil {
		m.logf("jobnotify: record the outcome of %s: %v", n.JobID, err)
	}
}

// redoLoop delivers the notices still pending, now and then, and drops the old rows.
func (m *Module) redoLoop(ctx context.Context) {
	t := time.NewTicker(m.redo)
	defer t.Stop()
	pass := func() {
		m.deliverPending(ctx, false)
		if err := m.store.prune(m.now()); err != nil {
			m.logf("jobnotify: prune: %v", err)
		}
	}
	pass()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			pass()
		case <-m.kick:
			m.deliverPending(ctx, true)
		}
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
