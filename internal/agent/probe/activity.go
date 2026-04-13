package probe

import (
	"context"
	"hash/fnv"
	"time"
)

const (
	activityPollInterval = 500 * time.Millisecond
	activityCaptureLines = 10
)

// StartWatch begins monitoring the given tmux target for screen changes.
// When a change is detected, cb is called once and the goroutine exits.
// If a watcher already exists for the target, it is stopped first.
func (p *Prober) StartWatch(target string, cb ActivityCallback) {
	p.watcherMu.Lock()
	if existing, ok := p.watchers[target]; ok {
		existing.cancel()
	}
	ctx, cancel := context.WithCancel(context.Background())
	id := &struct{}{}
	p.watchers[target] = watchEntry{cancel: cancel, id: id}
	p.watcherMu.Unlock()

	go p.activityLoop(ctx, id, target, cb)
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

func (p *Prober) activityLoop(ctx context.Context, id *struct{}, target string, cb ActivityCallback) {
	defer func() {
		p.watcherMu.Lock()
		if entry, ok := p.watchers[target]; ok && entry.id == id {
			delete(p.watchers, target)
		}
		p.watcherMu.Unlock()
	}()

	baseline, ok := p.hashCapture(target)
	if !ok {
		// Initial capture failed — can't establish baseline, exit
		return
	}
	ticker := time.NewTicker(activityPollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			current, ok := p.hashCapture(target)
			if !ok {
				continue // tmux error — skip this tick, don't trigger false change
			}
			if current != baseline {
				cb(target)
				return
			}
		}
	}
}

func (p *Prober) hashCapture(target string) (uint32, bool) {
	content, err := p.tmux.CapturePaneContent(target, activityCaptureLines)
	if err != nil {
		return 0, false
	}
	h := fnv.New32a()
	h.Write([]byte(content))
	return h.Sum32(), true
}
