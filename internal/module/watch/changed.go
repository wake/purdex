package watchmod

import (
	"encoding/json"
	"sync"

	"github.com/wake/purdex/internal/core"
	"github.com/wake/purdex/internal/watch"
)

// The watcher as the App sees it (host-load-control spec §12.6): /api/watch/status names the watcher this host's events go to,
// and watch.changed on the event stream carries the same fields whenever one of them changes.

// ChangedEventType is the event stream type of the notice.
const ChangedEventType = "watch.changed"

// WatcherView is the watcher events go to: its host, the session, and whether it is awake or resting; TmuxSession is the tmux
// session it lives in, which the App's watcher tab binds to. "None" is the absence of this object, and is a different answer from
// resting (a resting watcher's tab opens its tmux session at a shell and offers to wake it).
type WatcherView struct {
	HostID      string `json:"host_id"`
	Alias       string `json:"alias"`
	SessionID   string `json:"session_id"`
	State       string `json:"state"`
	TmuxSession string `json:"tmux_session"`
}

// HolderPanes is this host's holder with the tmux ids a client needs to attach to it.
type HolderPanes struct {
	State         string `json:"state"`
	SessionID     string `json:"session_id,omitempty"`
	TmuxSessionID string `json:"tmux_session_id,omitempty"`
	PaneID        string `json:"pane_id,omitempty"`
}

// ChangedPayload is the payload of watch.changed (and what the status adds): the watcher events go to (nil: none) and this host's
// holder with its pane ids. It is compared as a value to decide whether anything changed, so it holds nothing that moves without
// the watcher changing (no last turn, no last delivery, no since).
type ChangedPayload struct {
	Watcher *WatcherView `json:"watcher"`
	Holder  HolderPanes  `json:"holder"`
}

// watcherNow is the watcher events go to: the one the locator remembers, else this host's own when it holds one (awake or
// resting). A remembered watcher on this very host is read from the live holder, not from what the locator last learned.
func (m *Module) watcherNow() *WatcherView {
	own := m.hs().Get()
	m.core.CfgMu.RLock()
	ownID, ownAlias := m.core.Cfg.HostID, m.core.Cfg.PeerAlias()
	m.core.CfgMu.RUnlock()
	selfView := func() *WatcherView {
		return &WatcherView{HostID: ownID, Alias: ownAlias, SessionID: own.SessionID, State: string(own.State), TmuxSession: watch.PaneSession}
	}
	if cur, _ := m.loc.Remembered(); cur != nil {
		if cur.HostID == ownID && own.State != watch.HolderNone {
			return selfView()
		}
		return &WatcherView{HostID: cur.HostID, Alias: cur.Alias, SessionID: cur.SessionID, State: string(cur.State), TmuxSession: watch.PaneSession}
	}
	if own.State != watch.HolderNone {
		return selfView()
	}
	return nil
}

func (m *Module) payloadNow() ChangedPayload {
	own := m.hs().Get()
	return ChangedPayload{
		Watcher: m.watcherNow(),
		Holder:  HolderPanes{State: string(own.State), SessionID: own.SessionID, TmuxSessionID: own.TmuxSessionID, PaneID: own.PaneID},
	}
}

// changeState is what the module remembers of the last notice.
type changeState struct {
	mu   sync.Mutex
	last string // the last payload sent (or the one the module started with), as JSON
}

// notifyChanged sends watch.changed when the payload differs from the last one sent: a reconcile that changed nothing, or only the
// last turn or last delivery, sends nothing. The reading, the comparison and the send are under one lock, so notices leave in the
// order of the states and the last one sent is the state of the last caller.
func (m *Module) notifyChanged() {
	if m.beforeChgLock != nil {
		m.beforeChgLock()
	}
	m.chg.mu.Lock()
	defer m.chg.mu.Unlock()
	// The state is read INSIDE the lock: a caller that read it before waiting for the lock could send an older state after a newer
	// one and leave the client (and the comparison) on it. Read here, the last notice sent is always the state of the last caller.
	b, err := json.Marshal(m.payloadNow())
	if err != nil {
		m.logf("[watch] encode watch.changed: %v", err)
		return
	}
	if string(b) == m.chg.last {
		return
	}
	m.chg.last = string(b)
	ev := core.HostEvent{Type: ChangedEventType, Value: string(b)}
	switch {
	case m.emit != nil:
		m.emit(ev)
	case m.core != nil && m.core.Events != nil:
		m.core.Events.BroadcastEvent(ev)
	}
}

// baseline records the payload as it is without sending it: the state the module starts with is not a change.
func (m *Module) baseline() {
	b, err := json.Marshal(m.payloadNow())
	if err != nil {
		return
	}
	m.chg.mu.Lock()
	m.chg.last = string(b)
	m.chg.mu.Unlock()
}
