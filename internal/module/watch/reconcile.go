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

var errNoOwnerLookup = errors.New("the agent module's owner resolver is not available")

// reconcileState is what the module keeps between reconciles.
type reconcileState struct {
	mu   sync.Mutex // one reconcile at a time
	last time.Time  // when the last one finished (attempted: a failing lookup is not retried by every read)
	// registered, when set, is called with the holder after a reconcile found a NEW watcher frame (W3a-2c announces it).
	registered func(watch.Holder)
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
	m.rec.mu.Lock()
	defer m.rec.mu.Unlock()
	defer func() { m.rec.last = m.now() }()
	h, registered, err = m.reconcileLocked(ctx)
	if registered && m.rec.registered != nil {
		m.rec.registered(h)
	}
	return h, registered, err
}

func (m *Module) reconcileLocked(ctx context.Context) (watch.Holder, bool, error) {
	cur := m.holder.Get()
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
	registered := false
	err = m.holder.Update(func(h *watch.Holder) {
		if !(h.State == watch.HolderAwake && h.FrameID == owner.FrameID) {
			registered = true
			h.Since = m.now()
		}
		h.State, h.SessionID = watch.HolderAwake, owner.SessionID
		h.FrameID, h.PaneID, h.TmuxSessionID = owner.FrameID, pane, sid
		h.PID, h.ProcessStartTime, h.TranscriptPath = owner.PID, owner.ProcessStartTime, owner.TranscriptPath
	})
	return m.holder.Get(), registered && err == nil, err
}

// rest: no watcher is there. An awake holder becomes resting with its session id and transcript path kept (the agent rested,
// exited or crashed); the rest of what was recorded about the run is cleared. Any other state stays as it is.
func (m *Module) rest(cur watch.Holder) (watch.Holder, bool, error) {
	if cur.State != watch.HolderAwake {
		return cur, false, nil
	}
	err := m.holder.Update(func(h *watch.Holder) {
		h.State, h.Since = watch.HolderResting, m.now()
		h.FrameID, h.PaneID, h.TmuxSessionID, h.PID, h.ProcessStartTime = "", "", "", 0, ""
	})
	return m.holder.Get(), false, err
}

// fresh reconciles when the last reconcile is older than staleAfter (or never happened) and the module has started; a failure is
// logged and the holder is read as it is. It is what a holder read (peer route, status, the locator's question) calls first.
func (m *Module) fresh(ctx context.Context) {
	if !m.started.Load() {
		return
	}
	m.rec.mu.Lock()
	current := !m.rec.last.IsZero() && m.now().Sub(m.rec.last) <= staleAfter
	m.rec.mu.Unlock()
	if current {
		return
	}
	cctx, cancel := context.WithTimeout(ctx, reconcileBudget)
	defer cancel()
	if _, _, err := m.Reconcile(cctx); err != nil {
		m.logf("[watch] reconcile: %v", err)
	}
}

// reconcileBackground is a reconcile no caller waits for (a SessionStart, the ticker, the start of the daemon).
func (m *Module) reconcileBackground(why string) {
	ctx, cancel := context.WithTimeout(context.Background(), reconcileBudget)
	defer cancel()
	if _, _, err := m.Reconcile(ctx); err != nil {
		m.logf("[watch] reconcile (%s): %v", why, err)
	}
}
