package teammod

import (
	"context"
	"sync"
	"time"

	"github.com/wake/purdex/internal/watch"
)

// wakeWatcherBudget bounds one wake of the load watcher the team module asks for (a start may take 3 minutes to register).
const wakeWatcherBudget = 5 * time.Minute

// watchWake is the module's one waiting wake of the load watcher: switches that come faster than a wake finishes are folded into the
// last one (the watcher is told the state it is in now), so a flapping switch never piles up goroutines.
type watchWake struct {
	mu      sync.Mutex
	pending *[2]string // reason, detail
	running bool
}

// wakeWatcher tells the load watcher that something it should know changed (host-load-control plan W3b-5): the unattended switch, for
// one. It goes through the watch module's service when that is registered, in the background (a wake can take a start's minutes, and
// no lock of this module is held while it runs); the work ends with the module (Stop joins it); a failure is logged, never the caller's.
func (m *Module) wakeWatcher(reason, detail string) {
	m.noticeMu.Lock() // the Add below is ordered against Stop's cancel like the other late Adds
	defer m.noticeMu.Unlock()
	if m.stopping() {
		return
	}
	svc, ok := m.core.Registry.Get(watch.WakerKey)
	if !ok {
		return
	}
	w, ok := svc.(watch.Waker)
	if !ok {
		return
	}
	m.watchWake.mu.Lock()
	m.watchWake.pending = &[2]string{reason, detail}
	if m.watchWake.running {
		m.watchWake.mu.Unlock()
		return
	}
	m.watchWake.running = true
	m.watchWake.mu.Unlock()
	m.sweepWG.Add(1)
	go func() {
		defer m.sweepWG.Done()
		for {
			m.watchWake.mu.Lock()
			next := m.watchWake.pending
			m.watchWake.pending = nil
			if next == nil || m.stopping() {
				m.watchWake.running = false
				m.watchWake.mu.Unlock()
				return
			}
			m.watchWake.mu.Unlock()
			ctx, cancel := context.WithTimeout(m.stopCtx, wakeWatcherBudget)
			if err := w.WakeWatcher(ctx, next[0], next[1]); err != nil && ctx.Err() == nil {
				m.logf("[team] wake the load watcher (%s): %v", next[0], err)
			}
			cancel()
		}
	}()
}

// unattendedWakeDetail is the line the watcher is told: the new state and who changed it (the label is the App's own text).
func unattendedWakeDetail(on bool, label string) string {
	r := []rune(watch.Plain(label))
	if len(r) > 80 {
		r = r[:80]
	}
	state := "關"
	if on {
		state = "開"
	}
	return "無人值守模式 → " + state + "（by " + string(r) + "）"
}
