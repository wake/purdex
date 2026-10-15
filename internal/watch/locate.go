package watch

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"slices"
	"sync"
	"time"
)

// Where is a watcher: the host it is on and its session.
type Where struct {
	Alias     string      `json:"alias"` // the host's alias in `targets`
	HostID    string      `json:"host_id"`
	SessionID string      `json:"session_id"`
	State     HolderState `json:"state"` // awake or resting, as the host last said
	At        time.Time   `json:"at"`    // when this was learned
}

// HolderAnswer is what a host says about the watcher it holds (the wire answer of POST /api/peers/watch/holder).
type HolderAnswer struct {
	HostID    string
	State     HolderState
	SessionID string
	Targets   []string // that host's setting, nil when it could not say
}

// Errors an Asker answers with. Anything else is a host that could not be reached.
var (
	// ErrNoWatchSupport: the host is an older daemon without the watch routes (a 404 is the same as "no watch support").
	ErrNoWatchSupport = errors.New("the host has no watch support")
	// ErrNoSession: this host cannot start or wake a watcher (the session part is not there, or refused).
	ErrNoSession = errors.New("no watcher can be started or woken here")
)

// Asker is how the locator talks to a host, by its alias in `targets` (this host's own alias answers locally).
type Asker interface {
	Holder(ctx context.Context, alias string) (HolderAnswer, error)
	// Wake asks the host to resume its resting watcher; Start asks it to start a new one. Both answer the holder state after.
	Wake(ctx context.Context, alias string) (HolderAnswer, error)
	Start(ctx context.Context, alias string) (HolderAnswer, error)
}

// ErrNoWatcher: no watcher is awake and none could be woken or started (auto_wake is off, or no target answers).
var ErrNoWatcher = errors.New("no watcher is awake")

// remembered is the file of the locator: the watcher events go to now (Current), and the last one there was (Last), which a
// forgotten current one leaves behind so a wake can find its host.
type remembered struct {
	Current *Where `json:"current,omitempty"`
	Last    *Where `json:"last,omitempty"`
	// Uncertain is a host a wake or start was sent to whose outcome could not be found out (the call and the question after it
	// both got no answer): it may be running a watcher. Until it answers (or uncertainTTL passes) nothing is started elsewhere.
	Uncertain *Uncertain `json:"uncertain,omitempty"`
}

// Uncertain: see remembered.Uncertain.
type Uncertain struct {
	Alias string    `json:"alias"`
	At    time.Time `json:"at"`
}

// uncertainTTL is how long an uncertain host blocks a start elsewhere; a variable for the tests.
var uncertainTTL = 30 * time.Minute

// valid: every watcher in the file names a host, a session and a state a watcher can be in (a file that was edited by hand, or
// is from another version, is not trusted).
func (r remembered) valid() bool {
	for _, w := range []*Where{r.Current, r.Last} {
		if w == nil {
			continue
		}
		if w.Alias == "" || w.SessionID == "" || (w.State != HolderAwake && w.State != HolderResting) {
			return false
		}
	}
	return r.Uncertain == nil || r.Uncertain.Alias != ""
}

// Locator knows where the watcher is (spec §12.3). It uses the remembered watcher until a delivery fails (Failed) or it is
// gone, and only then looks again, in this order: (1) a watcher awake on a host in targets; (2) the last watcher's host is
// reachable → wake that watcher; (3) the first reachable target starts a new one. Steps 2 and 3 only with auto_wake. The
// remembered watcher is kept in a file, so a restart does not start a second search.
type Locator struct {
	mu       sync.Mutex
	search1  chan struct{} // one search at a time (capacity 1): two callers that both missed the cache must not both start a watcher
	path     string
	mem      remembered
	ask      Asker
	now      func() time.Time
	warnf    func(format string, args ...any)
	warnedOn map[string]string // host → the targets it reported when we last warned
}

// NewLocator reads the remembered watcher from path ("" keeps it in memory). A file that cannot be read is set aside.
func NewLocator(path string, ask Asker, warnf func(string, ...any)) *Locator {
	l := &Locator{path: path, ask: ask, now: time.Now, warnf: warnf, warnedOn: map[string]string{}, search1: make(chan struct{}, 1)}
	if path != "" {
		if b, err := os.ReadFile(path); err == nil {
			if json.Unmarshal(b, &l.mem) != nil || !l.mem.valid() {
				_ = os.Rename(path, path+".corrupt")
				l.mem = remembered{}
			}
		}
	}
	return l
}

// Remembered is the watcher events go to now (nil: none is known) and the last one there was.
func (l *Locator) Remembered() (current, last *Where) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return clone(l.mem.Current), clone(l.mem.Last)
}

func clone(w *Where) *Where {
	if w == nil {
		return nil
	}
	c := *w
	return &c
}

// Failed says a delivery to w failed or w is gone: the next Current looks again. Only when w is still the remembered watcher: a
// late failure of an earlier delivery must not clear what a search found since. The watcher stays as the last one, for a wake.
func (l *Locator) Failed(w Where) {
	l.mu.Lock()
	defer l.mu.Unlock()
	c := l.mem.Current
	if c == nil || c.HostID != w.HostID || c.SessionID != w.SessionID {
		return
	}
	l.mem.Current = nil
	l.save()
}

// probeBudget is how long one target may take to answer the holder question; the targets are asked side by side, so a dead
// host costs one budget, not one per target. A variable for the tests.
var probeBudget = 5 * time.Second

// Current is where events go: the remembered watcher while it is valid, else what the search finds. The cache is not asked
// again while deliveries succeed. targets and autoWake are the settings of this pass. One search runs at a time; a caller that
// waits for it gives up when its ctx ends.
func (l *Locator) Current(ctx context.Context, targets []string, autoWake bool) (Where, error) {
	if w, ok := l.cached(); ok {
		return w, nil
	}
	select {
	case l.search1 <- struct{}{}:
	case <-ctx.Done():
		return Where{}, ctx.Err()
	}
	defer func() { <-l.search1 }()
	if w, ok := l.cached(); ok { // another caller's search found it while this one waited
		return w, nil
	}
	return l.search(ctx, targets, autoWake)
}

func (l *Locator) cached() (Where, bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if c := l.mem.Current; c != nil {
		return *c, true
	}
	return Where{}, false
}

type probeResult struct {
	a   HolderAnswer
	err error
}

// probeAll asks every target for its holder side by side, each within probeBudget.
func (l *Locator) probeAll(ctx context.Context, targets []string) []probeResult {
	out := make([]probeResult, len(targets))
	var wg sync.WaitGroup
	for i, alias := range targets {
		wg.Add(1)
		go func(i int, alias string) {
			defer wg.Done()
			cctx, cancel := context.WithTimeout(ctx, probeBudget)
			defer cancel()
			out[i].a, out[i].err = l.ask.Holder(cctx, alias)
		}(i, alias)
	}
	wg.Wait()
	return out
}

// try runs act (a wake or a start) on alias and returns the watcher awake there afterwards, if there is one. settled says the
// outcome is known: an answer, or a refusal (ErrNoSession). Any other error leaves it unknown — the call may have worked and
// the answer got lost — so the host is asked once more; if it cannot say either, the caller must not go on to start a watcher
// elsewhere.
func (l *Locator) try(ctx context.Context, alias string, act func(context.Context, string) (HolderAnswer, error)) (w *Where, settled bool) {
	res, err := act(ctx, alias)
	if err == nil {
		if res.State == HolderAwake && res.SessionID != "" {
			return &Where{Alias: alias, HostID: res.HostID, SessionID: res.SessionID, State: HolderAwake, At: l.now()}, true
		}
		return nil, true
	}
	if errors.Is(err, ErrNoSession) {
		return nil, true
	}
	cctx, cancel := context.WithTimeout(ctx, probeBudget)
	defer cancel()
	a, qerr := l.ask.Holder(cctx, alias)
	if qerr != nil {
		return nil, false
	}
	if a.State == HolderAwake && a.SessionID != "" {
		return &Where{Alias: alias, HostID: a.HostID, SessionID: a.SessionID, State: HolderAwake, At: l.now()}, true
	}
	return nil, true
}

func (l *Locator) search(ctx context.Context, targets []string, autoWake bool) (Where, error) {
	reachable := map[string]HolderAnswer{} // the targets that answered the holder question
	var awake *Where
	// (1) the first watcher awake on a target
	for i, p := range l.probeAll(ctx, targets) {
		if p.err != nil {
			continue // unreachable, or no watch support: not a candidate for anything
		}
		alias := targets[i]
		reachable[alias] = p.a
		l.compareTargets(alias, targets, p.a)
		if p.a.State == HolderAwake && p.a.SessionID != "" && awake == nil {
			awake = &Where{Alias: alias, HostID: p.a.HostID, SessionID: p.a.SessionID, State: HolderAwake, At: l.now()}
		}
	}
	if awake != nil {
		l.remember(*awake)
		return *awake, nil
	}
	if !autoWake {
		return Where{}, ErrNoWatcher
	}
	if l.blockedByUncertain(reachable) {
		return Where{}, ErrNoWatcher // a host that may hold a watcher has not answered yet: do not start another
	}
	// (2) the last watcher's host is reachable and holds it resting → wake it
	woken := map[string]bool{} // hosts a wake was tried on: not woken again below
	l.mu.Lock()
	last := clone(l.mem.Last)
	l.mu.Unlock()
	if last != nil {
		if a, ok := reachable[last.Alias]; ok && a.State == HolderResting && a.SessionID != "" {
			woken[last.Alias] = true
			w, settled := l.try(ctx, last.Alias, l.ask.Wake)
			if w != nil {
				l.remember(*w)
				return *w, nil
			}
			if !settled {
				l.setUncertain(last.Alias)
				return Where{}, ErrNoWatcher // it may be awake there: try again later rather than start a second one
			}
		}
	}
	// (3) the first reachable target starts a new one. A target that holds a resting watcher of its own is woken instead: it
	// is the one a start there would have collided with.
	for _, alias := range targets {
		a, ok := reachable[alias]
		if !ok {
			continue
		}
		act := l.ask.Start
		if a.State == HolderResting && a.SessionID != "" {
			if woken[alias] {
				continue // already tried, and it did not come up
			}
			act = l.ask.Wake
		}
		w, settled := l.try(ctx, alias, act)
		if w != nil {
			l.remember(*w)
			return *w, nil
		}
		if !settled {
			l.setUncertain(alias)
			return Where{}, ErrNoWatcher
		}
	}
	return Where{}, ErrNoWatcher
}

// blockedByUncertain says whether an earlier wake or start left a host whose outcome is still unknown. The host answering the
// holder question settles it (a watcher awake there was found above; otherwise the normal steps go on); so does uncertainTTL.
func (l *Locator) blockedByUncertain(reachable map[string]HolderAnswer) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	u := l.mem.Uncertain
	if u == nil {
		return false
	}
	if _, answered := reachable[u.Alias]; answered || l.now().Sub(u.At) > uncertainTTL {
		l.mem.Uncertain = nil
		l.save()
		return false
	}
	return true
}

func (l *Locator) setUncertain(alias string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.mem.Uncertain = &Uncertain{Alias: alias, At: l.now()}
	l.save()
}

func (l *Locator) remember(w Where) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.mem.Current, l.mem.Last = &w, &w
	l.mem.Uncertain = nil // a watcher is accounted for
	l.save()
}

// save writes the file; a failure is not fatal (the next search finds the watcher again) and is only logged.
func (l *Locator) save() {
	if l.path == "" {
		return
	}
	b, err := json.MarshalIndent(l.mem, "", " ")
	if err == nil {
		_, err = writeAtomic(l.path, b)
	}
	if err != nil && l.warnf != nil {
		l.warnf("[watch] cannot keep the remembered watcher: %v", err)
	}
}

// compareTargets warns, once per change, when a host reports a targets list other than ours: the two would send events to,
// and start a watcher on, different hosts.
func (l *Locator) compareTargets(alias string, ours []string, a HolderAnswer) {
	if a.Targets == nil || l.warnf == nil {
		return
	}
	theirs := fmt.Sprint(a.Targets)
	l.mu.Lock()
	prev, seen := l.warnedOn[alias]
	same := slices.Equal(ours, a.Targets)
	switch {
	case same:
		delete(l.warnedOn, alias)
		l.mu.Unlock()
		return
	case seen && prev == theirs:
		l.mu.Unlock()
		return
	}
	l.warnedOn[alias] = theirs
	l.mu.Unlock()
	l.warnf("[watch] the watch targets of %s differ from ours: they say %v, we say %v", alias, a.Targets, ours)
}
