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
	"errors"
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
	JobsAfter(ctx context.Context, after string, limit int) (wire.JobsListResponse, error)
}

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
	// the first run starts now: it does not tell a whole history. With neither a cursor nor a window written there is no safe start,
	// so the module writes the window (the time now) and, if that cannot be done, does not start at all.
	if _, haveCursor, err := st.cursor(); err != nil {
		st.close()
		return fmt.Errorf("job notice cursor: %w", err)
	} else if !haveCursor {
		if _, ok, err := st.since(); err != nil {
			st.close()
			return fmt.Errorf("job notice window: %w", err)
		} else if !ok {
			if err := st.setSince(m.now()); err != nil {
				st.close()
				return fmt.Errorf("job notice window: %w", err)
			}
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

// Stop ends the loops and waits for them as long as ctx allows. The database is closed by Close, which the core calls after Stop, so
// a loop that did not end in time never meets a closed database.
func (m *Module) Stop(ctx context.Context) error {
	if m.cancel != nil {
		m.cancel()
	}
	ended := make(chan struct{})
	go func() { m.wg.Wait(); close(ended) }()
	select {
	case <-ended:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// Close closes the database once the loops have ended. The core calls it after every Stop, also after one that timed out, so a loop
// that is still stuck in a call is waited for a moment and the database is then left to the process's end rather than closed under it.
func (m *Module) Close() error {
	if m.store == nil {
		return nil
	}
	ended := make(chan struct{})
	go func() { m.wg.Wait(); close(ended) }()
	select {
	case <-ended:
		return m.store.close()
	case <-time.After(2 * time.Second):
		return fmt.Errorf("jobnotify: a loop is still running, database left open")
	}
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
	je, err := wire.DecodeLenient[wire.JobEvent](ev.Data)
	if err != nil {
		return
	}
	if added, _ := m.enqueue(je.Job); added {
		m.wake() // the stream's callback only queues: a slow send must not stop it reading
	}
}

// pageSize is how many changed jobs one catch-up read asks for.
const pageSize = 1000

// catchUp reads what changed in wwd since the cursor it stored, page by page, and queues the ended jobs not yet in the outbox. The
// cursor is wwd's own (a position in its change log, never a time of a worker's) and moves only past a page whose jobs were all
// queued, so a job that could not be queued is read again. A job comes back whenever it changes (a log uploaded after the end), which
// the outbox ignores by job id. wwd answering without a cursor (a version that does not know ?after=) is an error, never "nothing new".
// The cursor is kept with the epoch of wwd's database. An answer under another epoch, or a 409 for a cursor past wwd's head, means wwd.db
// was made again: the cursor is dropped and the first-pass window starts again at now (startOver), so the new database's history is not
// told and nothing new is missed. A wwd that gives no epoch (empty) is taken as it always was.
//
// Until the first pass has read everything, the legacy window (the time the module first ran, less the overlap) keeps the history
// that was there before the module from being told: a job made before it is skipped. The window reads the job's created_at, which
// wwd's own clock stamps (a submission cannot carry it); the finished time may be a remote worker's, and a slow clock there must not
// decide that a job which ended after a restart is history. The price: a job made over an hour before the module first ran that ends
// after it is told by the event stream or not at all.
func (m *Module) catchUp(ctx context.Context) {
	cursor, have, err := m.store.cursor()
	if err != nil {
		m.logf("jobnotify: catch-up cursor: %v", err)
		return
	}
	if !have {
		cursor = "0"
	}
	epoch, err := m.store.epoch()
	if err != nil {
		m.logf("jobnotify: catch-up epoch: %v", err)
		return
	}
	since, windowed, err := m.store.since()
	if err != nil {
		m.logf("jobnotify: catch-up window: %v", err)
		return
	}
	floor := since.Add(-overlap)
	restarted := false
	// startOver is for a wwd whose database is another one than the cursor was made by: the cursor is forgotten and the first-pass
	// window is back at now. It happens once per catch-up; a second sign in the same pass is left to the next one.
	startOver := func(why string) bool {
		if restarted {
			m.logf("jobnotify: catch-up: wwd's database changed again (%s); left to the next catch-up", why)
			return false
		}
		restarted = true
		now := m.now()
		if err := m.store.restart(now); err != nil {
			m.logf("jobnotify: catch-up restart: %v", err)
			return false
		}
		m.logf("jobnotify: wwd's database is not the one the cursor was made by (%s): reading again from the start", why)
		cursor, epoch, windowed, floor = "0", "", true, now.Add(-overlap)
		return true
	}
	for {
		page, err := m.src.JobsAfter(ctx, cursor, pageSize)
		var ahead *client.CursorAheadError
		if errors.As(err, &ahead) {
			if startOver("cursor " + cursor + " is past its head " + ahead.Head) {
				continue
			}
			return
		}
		if err != nil {
			if ctx.Err() == nil {
				m.logf("jobnotify: catch-up after %s: %v", cursor, err)
			}
			return
		}
		// An epoch is stored and the answer has none: a wwd that was put back to a version without epochs, whose database may be
		// another one. Nothing can be said about this cursor, so nothing is read and nothing moves until an epoch comes again.
		if epoch != "" && page.Epoch == "" {
			m.logf("jobnotify: catch-up: wwd answered without an epoch although the cursor has %s; waiting for a wwd that has one", epoch)
			return
		}
		// another epoch than the stored one: this page was read from a database the cursor does not belong to, so none of it is used
		if epoch != "" && page.Epoch != epoch {
			if startOver("epoch " + page.Epoch + ", the cursor has " + epoch) {
				continue
			}
			return
		}
		for _, j := range page.Jobs {
			if windowed && j.CreatedAt.Before(floor) {
				continue // made before this module first ran: history, not news
			}
			if _, err := m.enqueue(j); err != nil {
				// the cursor stays before this page: the job is read again by the next catch-up
				m.logf("jobnotify: catch-up could not queue %s, cursor kept: %v", j.ID, err)
				m.wake()
				return
			}
		}
		last := len(page.Jobs) < pageSize
		if last && windowed {
			err = m.store.finishFirstPass(page.Cursor, page.Epoch) // the cursor and the end of the window, together
		} else {
			err = m.store.setCursor(page.Cursor, page.Epoch)
		}
		if err != nil {
			m.logf("jobnotify: catch-up cursor: %v", err)
			return
		}
		cursor = page.Cursor
		if page.Epoch != "" {
			epoch = page.Epoch
		}
		if last {
			break
		}
	}
	m.wake()
}

// enqueue puts the notice of a job that ended into the outbox, once. A fast-lane job is its caller's own process: pdx test prints
// the outcome (and Claude Code reports a backgrounded one), so only a lost run is told — its caller was killed and could not report.
// added is true when the job got a new row; an error means the outbox could not be written.
func (m *Module) enqueue(j wire.Job) (added bool, err error) {
	if !j.State.Terminal() || j.Submitter.Session == "" || (j.Local && !lost(j)) {
		return false, nil
	}
	finished := m.now()
	if j.Result != nil && j.Result.FinishedAt != nil {
		finished = *j.Result.FinishedAt
	}
	added, err = m.store.enqueue(j.ID, j.Submitter.Session, Text(j), string(j.State), finished, m.now())
	if err != nil {
		m.logf("jobnotify: queue %s: %v", j.ID, err)
	}
	return added, err
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
