package probe

import (
	"context"
	"hash/fnv"
	"log"
	"time"
)

// watchPollInterval is the cadence at which Watch re-captures the pane.
// Test files override via SetWatchPollIntervalForTest.
var watchPollInterval = 500 * time.Millisecond

// Watch starts a long-lived screen-change watcher on the target. The watcher
// emits ScreenChanged on every tick whose capture hash differs from the
// rolling baseline, and ScreenStable when the hash has matched for
// IdleStableTicks consecutive ticks (counter then resets). The watcher
// persists until StopWatch is called — callbacks do NOT terminate the
// watcher (kickoff Decision 13: watch-loop-owned).
//
// Probe layer is dumb: it does NOT track emit-once state. Orchestrator owns
// transition dedup via currentStatus per session.
//
// Capture mode is selected by opts:
//
//	TopLines > 0      → CapturePaneTopLines(target, TopLines)
//	BottomLines > 0   → CapturePaneContent(target, BottomLines)  // legacy
//	both == 0         → CapturePaneContent(target, 0)            // full pane
//
// TopLines and BottomLines are mutually exclusive. Setting both is a caller
// programming error: Watch logs a warning and returns without registering.
func (p *Prober) Watch(target string, opts WatchOptions, cb ScreenChangeCallback) {
	if opts.TopLines > 0 && opts.BottomLines > 0 {
		log.Printf("[probe] Watch(%q): TopLines=%d and BottomLines=%d are mutually exclusive; ignoring",
			target, opts.TopLines, opts.BottomLines)
		return
	}

	captureFn := p.makeCaptureFn(target, opts)
	idleStable := opts.IdleStableTicks
	if idleStable == 0 {
		idleStable = 3
	}

	p.watcherMu.Lock()
	if existing, ok := p.watchers[target]; ok {
		existing.cancel()
	}
	ctx, cancel := context.WithCancel(context.Background())
	id := &struct{}{}
	p.watchers[target] = watchEntry{cancel: cancel, id: id}
	p.watcherMu.Unlock()

	go p.watchLoop(ctx, id, target, captureFn, idleStable, cb)
}

// StopWatch cancels the active watcher for the given target. Idempotent.
func (p *Prober) StopWatch(target string) {
	p.watcherMu.Lock()
	if entry, ok := p.watchers[target]; ok {
		entry.cancel()
		delete(p.watchers, target)
	}
	p.watcherMu.Unlock()
}

// StopAllWatches cancels all active watchers. Used during daemon shutdown.
func (p *Prober) StopAllWatches() {
	p.watcherMu.Lock()
	for target, entry := range p.watchers {
		entry.cancel()
		delete(p.watchers, target)
	}
	p.watcherMu.Unlock()
}

// HasWatcher reports whether a watcher is currently registered for the given
// target. Intended for tests that verify StopWatch was invoked as part of
// cleanup paths (frame sweep, session rename, etc).
func (p *Prober) HasWatcher(target string) bool {
	p.watcherMu.Lock()
	defer p.watcherMu.Unlock()
	_, ok := p.watchers[target]
	return ok
}

// makeCaptureFn returns a closure that captures pane content per opts.
// The returned function reports (content, ok=true) on success or
// ("", ok=false) on tmux error so the caller can skip the tick without
// firing a false event.
func (p *Prober) makeCaptureFn(target string, opts WatchOptions) func() (string, bool) {
	switch {
	case opts.TopLines > 0:
		n := opts.TopLines
		return func() (string, bool) {
			content, err := p.tmux.CapturePaneTopLines(target, n)
			if err != nil {
				return "", false
			}
			return content, true
		}
	case opts.BottomLines > 0:
		n := opts.BottomLines
		return func() (string, bool) {
			content, err := p.tmux.CapturePaneContent(target, n)
			if err != nil {
				return "", false
			}
			return content, true
		}
	default:
		return func() (string, bool) {
			content, err := p.tmux.CapturePaneContent(target, 0)
			if err != nil {
				return "", false
			}
			return content, true
		}
	}
}

func hashContent(s string) uint32 {
	h := fnv.New32a()
	h.Write([]byte(s))
	return h.Sum32()
}

// watchLoop is the dumb screen-change primitive. It re-captures every
// watchPollInterval and fires ScreenChanged on each diff tick, ScreenStable
// every idleStable consecutive identical-hash ticks. No emit-once flags;
// orchestrator owns transition dedup.
func (p *Prober) watchLoop(
	ctx context.Context,
	id *struct{},
	target string,
	capture func() (string, bool),
	idleStable int,
	cb ScreenChangeCallback,
) {
	cleanup := func() {
		p.watcherMu.Lock()
		if entry, ok := p.watchers[target]; ok && entry.id == id {
			delete(p.watchers, target)
		}
		p.watcherMu.Unlock()
	}

	baselineContent, ok := capture()
	if !ok {
		// R2 fix: baseline-fail self-cleanup. Watcher map already holds our
		// entry; the goroutine is about to exit, so we must drop our own
		// entry (only-if-still-mine via id) lest HasWatcher report a stale
		// live watcher.
		cleanup()
		return
	}
	baselineHash := hashContent(baselineContent)

	ticker := time.NewTicker(watchPollInterval)
	defer ticker.Stop()
	defer cleanup()

	stableCount := 0
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			current, ok := capture()
			if !ok {
				// tmux error — skip this tick, do NOT fire a false event.
				continue
			}
			currentHash := hashContent(current)
			if currentHash != baselineHash {
				cb(ScreenChangeEvent{
					Kind:       ScreenChanged,
					Target:     target,
					Content:    current,
					OccurredAt: p.now(),
				})
				baselineHash = currentHash
				baselineContent = current
				stableCount = 0
				continue
			}
			// Hash unchanged — count stable tick.
			_ = baselineContent // keep last-good content for ScreenStable payload
			stableCount++
			if stableCount >= idleStable {
				cb(ScreenChangeEvent{
					Kind:       ScreenStable,
					Target:     target,
					Content:    current,
					OccurredAt: p.now(),
				})
				stableCount = 0 // re-arm; continuous-stable panes re-fire every N ticks
			}
		}
	}
}
