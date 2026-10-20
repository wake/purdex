package notifylabel

import (
	"context"
	"sync"
	"time"
)

// Key is the service-registry key of the Labeler: the agent module owns it, the push module reads the same instance.
const Key = "notifylabel.labeler"

const (
	defaultTTL        = 30 * time.Second
	defaultMaxEntries = 4096
	readTimeout       = 5 * time.Second
	queueSize         = 256
)

// Sources are where a read gets its facts. Every one may do I/O; the Labeler calls them only from its background
// goroutine, never under a lock and never from Label.
type Sources struct {
	// Code is the persona code a session's name ends in (peers' NameService.CodeOf). It cannot say a read failed, so a
	// failure reads as "no persona" until the next reread.
	Code func(ctx context.Context, sessionID string) (string, bool)
	// Group is the display name of the team the session sits in ("" = no team). An error keeps the last known value.
	Group func(ctx context.Context, sessionID string) (string, error)
	// Display is the display name of a persona code (peers.PoolDisplayName): a memory lookup, called on every Label so a
	// renamed persona shows at once.
	Display func(code string) (string, bool)
}

// Overlay is a second, memory-only source of a session's group, used where no team has one: the groups the App wrote back
// (notify-title spec §3.2, N-2). It is called on every Label, so it must not do I/O.
type Overlay interface {
	GroupOf(sessionID string) string
}

// Option configures a Labeler.
type Option func(*Labeler)

// WithClock sets the clock the entries' age is read from.
func WithClock(now func() time.Time) Option { return func(l *Labeler) { l.now = now } }

// WithTTL sets how old an entry may be before the next Label asks for a reread.
func WithTTL(d time.Duration) Option { return func(l *Labeler) { l.ttl = d } }

// WithOverlay sets the memory-only source of the groups the App wrote back.
func WithOverlay(o Overlay) Option { return func(l *Labeler) { l.overlay = o } }

// WithMaxEntries bounds the cache.
func WithMaxEntries(n int) Option { return func(l *Labeler) { l.maxEntries = n } }

type entry struct {
	code   string
	group  string
	readAt time.Time
}

// Labeler works out the Label of a session from memory only. What it knows about a session (its persona code and its team)
// is read by one background goroutine: a Label that finds no entry, or one older than the ttl, queues a read and answers
// with what it has now, so the emitter that calls it never waits on SQLite or any other I/O (notify-title spec §3.1). A
// cold session is therefore unlabelled until its first read lands, and a persona or team change shows within one ttl.
type Labeler struct {
	src        Sources
	now        func() time.Time
	ttl        time.Duration
	maxEntries int
	overlay    Overlay

	mu      sync.Mutex
	cache   map[string]entry
	pending map[string]bool
	stopped bool
	started bool

	queue chan string
	done  chan struct{}
	wg    sync.WaitGroup
}

// New returns a Labeler; Start runs its reader.
func New(src Sources, opts ...Option) *Labeler {
	l := &Labeler{src: src, now: time.Now, ttl: defaultTTL, maxEntries: defaultMaxEntries,
		cache: map[string]entry{}, pending: map[string]bool{}, queue: make(chan string, queueSize), done: make(chan struct{})}
	for _, o := range opts {
		o(l)
	}
	return l
}

// Start runs the background reader. Calling it twice, or after Stop, does nothing.
func (l *Labeler) Start() {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.started || l.stopped {
		return
	}
	l.started = true
	l.wg.Add(1)
	go l.run()
}

// Stop ends the reader and waits for it. Label still answers afterwards, from the cache, and queues nothing.
func (l *Labeler) Stop() {
	l.mu.Lock()
	if l.stopped {
		l.mu.Unlock()
		return
	}
	l.stopped = true
	close(l.done)
	l.mu.Unlock()
	l.wg.Wait()
}

// Label is the label of a session. sessionName is the tmux session name. It reads memory only.
func (l *Labeler) Label(sessionID, sessionName string) Label {
	out := Label{Tmux: sessionName}
	if sessionID == "" {
		return out
	}
	l.mu.Lock()
	e, ok := l.cache[sessionID]
	if (!ok || l.now().Sub(e.readAt) > l.ttl) && !l.pending[sessionID] && l.started && !l.stopped {
		select {
		case l.queue <- sessionID:
			l.pending[sessionID] = true
		default: // the reader is far behind; the next event asks again
		}
	}
	l.mu.Unlock()
	if ok && e.code != "" && l.src.Display != nil {
		if name, found := l.src.Display(e.code); found {
			out.Name = name
		}
	}
	out.Group = e.group
	if out.Group == "" && l.overlay != nil {
		out.Group = l.overlay.GroupOf(sessionID)
	}
	return out
}

func (l *Labeler) run() {
	defer l.wg.Done()
	for {
		select {
		case <-l.done:
			return
		case sid := <-l.queue:
			l.read(sid)
		}
	}
}

// read asks the sources, outside any lock, then stores the answer. A group error keeps the last known group; either way
// the entry is stamped, so a failing store is asked once per ttl, not once per event.
func (l *Labeler) read(sid string) {
	ctx, cancel := context.WithTimeout(context.Background(), readTimeout)
	defer cancel()
	go func() {
		select {
		case <-l.done:
			cancel()
		case <-ctx.Done():
		}
	}()
	var code string
	if l.src.Code != nil {
		code, _ = l.src.Code(ctx, sid)
	}
	group, groupErr := "", error(nil)
	if l.src.Group != nil {
		group, groupErr = l.src.Group(ctx, sid)
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.pending, sid)
	e := l.cache[sid]
	e.code, e.readAt = code, l.now()
	if groupErr == nil {
		e.group = group
	}
	if _, had := l.cache[sid]; !had && len(l.cache) >= l.maxEntries {
		l.evictLocked()
	}
	l.cache[sid] = e
}

// evictLocked makes room: entries older than ten ttls go first; if that is not enough the cache starts over (a cold
// entry is only a missing label until its next read).
func (l *Labeler) evictLocked() {
	cutoff := l.now().Add(-10 * l.ttl)
	for k, e := range l.cache {
		if e.readAt.Before(cutoff) {
			delete(l.cache, k)
		}
	}
	if len(l.cache) >= l.maxEntries {
		l.cache = map[string]entry{}
	}
}

// WaitIdle waits until no read is queued or running, up to timeout; false when it ran out (for tests, which need the
// background read to have landed before they look at a label).
func (l *Labeler) WaitIdle(timeout time.Duration) bool {
	for deadline := time.Now().Add(timeout); time.Now().Before(deadline); time.Sleep(time.Millisecond) {
		if l.idle() {
			return true
		}
	}
	return l.idle()
}

// idle says no read is queued or running.
func (l *Labeler) idle() bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	return len(l.pending) == 0
}

// size is the number of cached sessions (for tests).
func (l *Labeler) size() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return len(l.cache)
}
