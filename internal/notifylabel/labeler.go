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
	defaultStopWait   = 3 * time.Second
	defaultRetention  = time.Hour
)

// Sources are where a read gets its facts. Every one may do I/O; the Labeler calls them only from its background
// goroutine, never under a lock and never from Label.
type Sources struct {
	// Code is the persona code a session's name ends in (peers' CodeOfErr). A session with no persona is ("", false, nil);
	// an error keeps the last known code.
	Code func(ctx context.Context, sessionID string) (string, bool, error)
	// Group is the display name of the team the session sits in ("" = no team). An error keeps the last known value.
	Group func(ctx context.Context, sessionID string) (string, error)
	// Root is the root of the session's relay chain (team.LineageRootResolver.RootSessionOf: the session with no predecessor, itself
	// when it has none). An error clears the root (a session whose root is not known has no overlay group, spec §3.1); "" with no
	// error is "not known" and keeps the root known before. nil: the session id is the key (a host with no lineage).
	Root func(ctx context.Context, sessionID string) (string, error)
	// Display is the display name of a persona code (peers.PoolDisplayName): a memory lookup, called on every Label so a
	// renamed persona shows at once.
	Display func(code string) (string, bool)
}

// Overlay is a second, memory-only source of a session's group, used where no team has one: the groups the App wrote back
// (notify-title spec §3.2, N-2). It is called on every Label, so it must not do I/O. It is asked with the **root session id**
// of the session's relay chain (Sources.Root), the key the App writes its groups under; a Labeler made without a Root source
// asks with the session id itself.
type Overlay interface {
	GroupOf(rootSessionID string) string
}

// OverlayKey is the service-registry key of the Overlay: the module that keeps the groups the App wrote back registers it, the
// agent module's Labeler asks for it (it may start before or after that module, and then has no overlay until it is there).
const OverlayKey = "notifylabel.overlay"

// Option configures a Labeler.
type Option func(*Labeler)

// WithClock sets the clock the entries' age is read from.
func WithClock(now func() time.Time) Option { return func(l *Labeler) { l.now = now } }

// WithTTL sets how old an entry may be before the next Label asks for a reread.
func WithTTL(d time.Duration) Option { return func(l *Labeler) { l.ttl = d } }

// WithOverlay sets the memory-only source of the groups the App wrote back.
func WithOverlay(o Overlay) Option { return func(l *Labeler) { l.overlay = o } }

// WithRetention sets how long an entry that nobody asked for is kept (and reread by the sweep).
func WithRetention(d time.Duration) Option { return func(l *Labeler) { l.retention = d } }

// WithStopWait sets how long Stop waits for a read that is still running.
func WithStopWait(d time.Duration) Option { return func(l *Labeler) { l.stopWait = d } }

// WithMaxEntries bounds the cache.
func WithMaxEntries(n int) Option { return func(l *Labeler) { l.maxEntries = n } }

type entry struct {
	code   string
	group  string
	root   string    // the root of the session's relay chain; "" until the first read that knew it
	readAt time.Time // when the sources were last read
	usedAt time.Time // when Label last asked for the session
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
	stopWait   time.Duration
	retention  time.Duration
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
	l := &Labeler{src: src, now: time.Now, ttl: defaultTTL, maxEntries: defaultMaxEntries, stopWait: defaultStopWait, retention: defaultRetention,
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

// Stop ends the reader and waits for it, up to the stop wait: a source that ignores its context (a store call with no
// deadline) cannot hold the daemon's shutdown; the reader is then left to finish on its own. Label still answers afterwards, from the cache, and queues nothing.
func (l *Labeler) Stop() {
	l.mu.Lock()
	if l.stopped {
		l.mu.Unlock()
		return
	}
	l.stopped = true
	close(l.done)
	l.mu.Unlock()
	ended := make(chan struct{})
	go func() { l.wg.Wait(); close(ended) }()
	select {
	case <-ended:
	case <-time.After(l.stopWait):
	}
}

// Label is the label of a session. sessionName is the tmux session name. It reads memory only.
func (l *Labeler) Label(sessionID, sessionName string) Label {
	out := Label{Tmux: sessionName}
	if sessionID == "" {
		return out
	}
	l.mu.Lock()
	e, ok := l.cache[sessionID]
	if ok {
		e.usedAt = l.now()
		l.cache[sessionID] = e
	}
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
		key := sessionID // no Root source: the session is its own key
		if l.src.Root != nil {
			key = e.root // a root not read yet is not guessed
		}
		if key != "" {
			out.Group = l.overlay.GroupOf(key)
		}
	}
	return out
}

func (l *Labeler) run() {
	defer l.wg.Done()
	tick := time.NewTicker(l.ttl / 2) // with the ttl/2 age test in sweep: an entry is never older than the ttl at the next sweep
	defer tick.Stop()
	for {
		select {
		case <-l.done:
			return
		case <-tick.C:
			l.sweep()
		case sid := <-l.queue:
			l.read(sid)
		}
	}
}

// sweep keeps the entries in use current while nobody is asking: a session that has been asked for within the retention
// and whose entry is older than the ttl is queued for a reread (so the notification that ends a long quiet task carries
// today's name, not the one from its last event); an entry nobody asked for within the retention is dropped. It gathers
// under the lock and reads after it, through the reader.
func (l *Labeler) sweep() {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	for sid, e := range l.cache {
		switch {
		case now.Sub(e.usedAt) > l.retention:
			delete(l.cache, sid)
		case now.Sub(e.readAt) > l.ttl/2 && !l.pending[sid] && l.started && !l.stopped:
			select {
			case l.queue <- sid:
				l.pending[sid] = true
			default: // the reader is behind; the next sweep asks again
			}
		}
	}
}

// read asks the sources, outside any lock, then stores the answer. A group error keeps the last known group; either way
// the entry is stamped, so a failing store is asked once per ttl, not once per event.
func (l *Labeler) read(sid string) {
	l.mu.Lock()
	stopped := l.stopped
	l.mu.Unlock()
	if stopped { // queued before Stop: do not ask the sources
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), readTimeout)
	defer cancel()
	go func() {
		select {
		case <-l.done:
			cancel()
		case <-ctx.Done():
		}
	}()
	var (
		code    string
		codeErr error
	)
	if l.src.Code != nil {
		code, _, codeErr = l.src.Code(ctx, sid)
	}
	group, groupErr := "", error(nil)
	if l.src.Group != nil {
		group, groupErr = l.src.Group(ctx, sid)
	}
	root, rootErr := "", error(nil)
	if l.src.Root != nil {
		root, rootErr = l.src.Root(ctx, sid)
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.pending, sid)
	if l.stopped { // the reader outlived Stop (a source that ignored its context): its answer is thrown away
		return
	}
	e := l.cache[sid]
	e.readAt = l.now()
	if _, had := l.cache[sid]; !had {
		e.usedAt = e.readAt
	}
	if codeErr == nil {
		e.code = code
	}
	if groupErr == nil {
		e.group = group
	}
	if rootErr != nil {
		e.root = "" // a root that could not be read leaves the group empty (spec §3.1), not the group of a root known before
	} else if root != "" {
		e.root = root // "" with no error is "not known": the root known before stays
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
