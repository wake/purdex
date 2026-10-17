package teammod

import (
	"context"
	"time"

	"github.com/wake/purdex/internal/watch"
)

// wakeWatcherBudget bounds one wake of the load watcher the team module asks for.
const wakeWatcherBudget = 30 * time.Second

// wakeWatcher tells the load watcher that something it should know changed (host-load-control plan W3b-5): the unattended switch, for
// one. It goes through the watch module's service when that is registered, in the background (a wake can take a start's minutes, and
// no lock of this module is held while it runs); a failure is logged, never the caller's.
func (m *Module) wakeWatcher(reason, detail string) {
	svc, ok := m.core.Registry.Get(watch.WakerKey)
	if !ok {
		return
	}
	w, ok := svc.(watch.Waker)
	if !ok {
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), wakeWatcherBudget)
		defer cancel()
		if err := w.WakeWatcher(ctx, reason, detail); err != nil {
			m.logf("[team] wake the load watcher (%s): %v", reason, err)
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
