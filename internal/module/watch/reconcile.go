package watchmod

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/wake/purdex/internal/module/agent"
	"github.com/wake/purdex/internal/module/session"
	"github.com/wake/purdex/internal/watch"
)

// Reconcile (host-load-control spec §12.4): the one function that owns the holder state. It reads the agent module's answer for the
// tmux session pdx-watch and moves holder.json accordingly; nothing else writes the state of an agent that is running or gone.

const (
	// staleAfter: a holder read (peer route, status, locator) reconciles first when the last reconcile is older than this, so an
	// agent that ended is seen within 30 s of anyone asking, with no SessionEnd plumbing.
	staleAfter = 30 * time.Second
	// safetyNet is the period of the ticker that reconciles whether or not anything asked.
	safetyNet = 5 * time.Minute
	// reconcileBudget bounds one reconcile (a tmux lookup and one owner lookup).
	reconcileBudget = 5 * time.Second
)

// ownerLookup is the agent module's OwnerResolver as reconcile uses it.
type ownerLookup interface {
	ResolveSessionOwner(ctx context.Context, code string) (agent.PaneOwner, bool, error)
}

// paneFinder is the lookup of the pdx-watch tmux session (watch.PaneControl.Find).
type paneFinder interface {
	Find(ctx context.Context) (sessionID, paneID string, found bool, err error)
}

// holderStore is the holder as reconcile uses it (a test wraps it to fail a write after it was committed).
type holderStore interface {
	Get() watch.Holder
	UpdateCommitted(fn func(*watch.Holder)) (committed bool, err error)
}

func (m *Module) hs() holderStore {
	if m.store != nil {
		return m.store
	}
	return m.holder
}

var (
	errNoOwnerLookup = errors.New("the agent module's owner resolver is not available")
	errPaneMoved     = errors.New("pdx-watch changed while it was being looked up; it will be looked up again")
)

// reconcileState is what the module keeps between reconciles.
type reconcileState struct {
	mu   sync.Mutex // one reconcile at a time
	last time.Time  // when the last one finished (attempted: a failing lookup is not retried by every read)
	// registered, when set, is called with the holder after a reconcile found a NEW watcher frame (W3a-2c announces it): once per new
	// frame, in the order the frames were committed, and never with the reconcile lock held (it may take a while, and may itself ask
	// for a reconcile). Whoever commits a frame queues it under the lock; one goroutine at a time drains the queue, so a slow hook
	// delays the later ones but not the reconcile.
	registered func(watch.Holder)
	qmu        sync.Mutex
	queue      []watch.Holder
	draining   bool
}

// owners is the agent module's resolver, looked up in the registry when first needed.
func (m *Module) owners() (ownerLookup, error) {
	if m.ownerSrc != nil {
		return m.ownerSrc, nil
	}
	svc, ok := m.core.Registry.Get(agent.OwnerResolverKey)
	if !ok {
		return nil, errNoOwnerLookup
	}
	o, ok := svc.(ownerLookup)
	if !ok {
		return nil, errNoOwnerLookup
	}
	return o, nil
}

// isWatcher is the check of spec §12.3: the owner is a Claude Code run, in the session's first pane, with a session id.
func isWatcher(o agent.PaneOwner, found bool, firstPane string) bool {
	return found && o.AgentType == "cc" && o.TmuxPaneID == firstPane && o.SessionID != ""
}

// Reconcile moves the holder to what the agent module says about pdx-watch and returns it. A lookup that fails changes nothing
// and is returned as the error (never read as "no agent"). registered says this call found a new watcher frame.
func (m *Module) Reconcile(ctx context.Context) (h watch.Holder, registered bool, err error) {
	h, registered, _, err = m.reconcile(ctx, false)
	return h, registered, err
}

// reconcile runs one reconcile. onlyIfStale makes it do nothing (ran=false) when, once it holds the exclusive right to reconcile,
// the last one is younger than staleAfter: readers that found the holder stale at the same moment queue here and the first one's
// reconcile serves all of them, instead of each repeating the lookups. A clock that went back makes the last reconcile "stale".
func (m *Module) reconcile(ctx context.Context, onlyIfStale bool) (h watch.Holder, registered, ran bool, err error) {
	m.rec.mu.Lock()
	if onlyIfStale && !m.rec.last.IsZero() {
		if age := m.now().Sub(m.rec.last); age >= 0 && age <= staleAfter {
			h = m.hs().Get()
			m.rec.mu.Unlock()
			return h, false, false, nil
		}
	}
	h, registered, err = m.reconcileLocked(ctx)
	m.rec.last = m.now()
	if registered && m.rec.registered != nil {
		m.rec.qmu.Lock()
		m.rec.queue = append(m.rec.queue, h) // in commit order: this is still under the reconcile lock
		m.rec.qmu.Unlock()
	}
	m.rec.mu.Unlock()
	m.drainRegistered()
	return h, registered, true, err
}

// drainRegistered calls the registered hook for the queued frames, oldest first. If another goroutine (or an outer call of this
// very goroutine, from inside a hook) is already draining, the frames it queued are left to that drain: no hook runs twice, none
// runs out of order, and a hook that asks for a reconcile does not wait for itself.
func (m *Module) drainRegistered() {
	m.rec.qmu.Lock()
	if m.rec.draining {
		m.rec.qmu.Unlock()
		return
	}
	m.rec.draining = true
	m.rec.qmu.Unlock()
	for {
		m.rec.qmu.Lock()
		if len(m.rec.queue) == 0 {
			m.rec.draining = false
			m.rec.qmu.Unlock()
			return
		}
		next := m.rec.queue[0]
		m.rec.queue = m.rec.queue[1:]
		m.rec.qmu.Unlock()
		m.rec.registered(next)
	}
}

// since is the time a state is entered: now, but never before the previous state's, so a wall clock that went back cannot make a
// new watcher look as if it had been awake longer than the ones before it (§12.3 picks the earliest since).
func (m *Module) since(prev time.Time) time.Time {
	if now := m.now(); !now.Before(prev) {
		return now
	}
	return prev
}

func (m *Module) reconcileLocked(ctx context.Context) (watch.Holder, bool, error) {
	cur := m.hs().Get()
	sid, pane, found, err := m.panes.Find(ctx)
	if err != nil {
		return cur, false, fmt.Errorf("find %s: %w", watch.PaneSession, err)
	}
	if !found { // no tmux session, so no agent in it
		return m.rest(cur)
	}
	code, err := session.EncodeSessionID(sid)
	if err != nil {
		return cur, false, err
	}
	owners, err := m.owners()
	if err != nil {
		return cur, false, err
	}
	owner, ok, err := owners.ResolveSessionOwner(ctx, code)
	if err != nil {
		return cur, false, fmt.Errorf("owner of %s: %w", watch.PaneSession, err)
	}
	if !isWatcher(owner, ok, pane) {
		return m.rest(cur)
	}
	sameFrame := cur.State == watch.HolderAwake && cur.FrameID == owner.FrameID
	if !sameFrame {
		// A new watcher is about to be recorded (and announced): the session and its first pane must still be the ones the owner
		// was asked about. tmux can restart, or a pane can be added in front, between the lookup and the answer.
		sid2, pane2, found2, err := m.panes.Find(ctx)
		if err != nil {
			return cur, false, fmt.Errorf("find %s: %w", watch.PaneSession, err)
		}
		if !found2 || sid2 != sid || pane2 != pane {
			return cur, false, errPaneMoved
		}
	}
	committed, err := m.hs().UpdateCommitted(func(h *watch.Holder) {
		if !sameFrame {
			h.Since = m.since(cur.Since)
		}
		h.State, h.SessionID = watch.HolderAwake, owner.SessionID
		h.FrameID, h.PaneID, h.TmuxSessionID = owner.FrameID, pane, sid
		h.PID, h.ProcessStartTime, h.TranscriptPath = owner.PID, owner.ProcessStartTime, owner.TranscriptPath
	})
	// Registration is what was committed, not whether the write came back clean: a directory fsync that failed after the rename
	// has changed the holder (memory follows the file), and the next reconcile would see the same frame and never announce it.
	return m.hs().Get(), !sameFrame && committed, err
}

// rest: no watcher is there. An awake holder becomes resting with its session id and transcript path kept (the agent rested,
// exited or crashed); the rest of what was recorded about the run is cleared. Any other state stays as it is.
func (m *Module) rest(cur watch.Holder) (watch.Holder, bool, error) {
	if cur.State != watch.HolderAwake {
		return cur, false, nil
	}
	_, err := m.hs().UpdateCommitted(func(h *watch.Holder) {
		h.State, h.Since = watch.HolderResting, m.since(cur.Since)
		h.FrameID, h.PaneID, h.TmuxSessionID, h.PID, h.ProcessStartTime = "", "", "", 0, ""
	})
	return m.hs().Get(), false, err
}

// enter admits one reconcile that no Stop has begun: it counts it for Stop to wait for and gives it a context that ends with the
// module, with the caller's, and after reconcileBudget. done must be called when it is over.
func (m *Module) enter(parent context.Context) (ctx context.Context, done func(), ok bool) {
	return m.enterFor(parent, reconcileBudget)
}

// enterFor is enter with a budget of its own (the registration's announce-and-settle gets standDownBudget, not a reconcile's).
func (m *Module) enterFor(parent context.Context, budget time.Duration) (ctx context.Context, done func(), ok bool) {
	m.runMu.Lock()
	defer m.runMu.Unlock()
	if !m.running {
		return nil, nil, false
	}
	m.wg.Add(1)
	ctx, cancel := context.WithTimeout(m.runCtx, budget)
	stop := context.AfterFunc(parent, cancel)
	return ctx, func() { stop(); cancel(); m.wg.Done() }, true
}

// fresh reconciles when the last reconcile is older than staleAfter (or never happened) and the module is running; a failure is
// logged and the holder is read as it is. It is what a holder read (peer route, status, the locator's question) calls first.
func (m *Module) fresh(ctx context.Context) {
	cctx, done, ok := m.enter(ctx)
	if !ok {
		return
	}
	defer done()
	if _, _, _, err := m.reconcile(cctx, true); err != nil {
		m.logf("[watch] reconcile: %v", err)
	}
}

// reconcileBackground is a reconcile no caller waits for (a SessionStart, the ticker, the start of the daemon).
func (m *Module) reconcileBackground(why string) {
	ctx, done, ok := m.enter(context.Background())
	if !ok {
		return
	}
	defer done()
	if _, _, err := m.Reconcile(ctx); err != nil {
		m.logf("[watch] reconcile (%s): %v", why, err)
	}
}
