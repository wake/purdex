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
}

// Locator knows where the watcher is (spec §12.3). It uses the remembered watcher until a delivery fails (Failed) or it is
// gone, and only then looks again, in this order: (1) a watcher awake on a host in targets; (2) the last watcher's host is
// reachable → wake that watcher; (3) the first reachable target starts a new one. Steps 2 and 3 only with auto_wake. The
// remembered watcher is kept in a file, so a restart does not start a second search.
type Locator struct {
	mu       sync.Mutex
	path     string
	mem      remembered
	ask      Asker
	now      func() time.Time
	warnf    func(format string, args ...any)
	warnedOn map[string]string // host → the targets it reported when we last warned
}

// NewLocator reads the remembered watcher from path ("" keeps it in memory). A file that cannot be read is set aside.
func NewLocator(path string, ask Asker, warnf func(string, ...any)) *Locator {
	l := &Locator{path: path, ask: ask, now: time.Now, warnf: warnf, warnedOn: map[string]string{}}
	if path != "" {
		if b, err := os.ReadFile(path); err == nil {
			if json.Unmarshal(b, &l.mem) != nil {
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

// Failed says a delivery to the remembered watcher failed or it is gone: the next Current looks again. The watcher stays as
// the last one, for a wake.
func (l *Locator) Failed() {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.mem.Current == nil {
		return
	}
	l.mem.Current = nil
	l.save()
}

// Current is where events go: the remembered watcher while it is valid, else what the search finds. The cache is not asked
// again while deliveries succeed. targets and autoWake are the settings of this pass.
func (l *Locator) Current(ctx context.Context, targets []string, autoWake bool) (Where, error) {
	l.mu.Lock()
	if c := l.mem.Current; c != nil {
		w := *c
		l.mu.Unlock()
		return w, nil
	}
	l.mu.Unlock()
	return l.search(ctx, targets, autoWake)
}

func (l *Locator) search(ctx context.Context, targets []string, autoWake bool) (Where, error) {
	reachable := map[string]HolderAnswer{} // the targets that answered the holder question
	asked := map[string]bool{}
	var awake *Where
	// (1) the first watcher awake on a target
	for _, alias := range targets {
		a, err := l.ask.Holder(ctx, alias)
		asked[alias] = true
		if err != nil {
			continue // unreachable, or no watch support: not a candidate for anything
		}
		reachable[alias] = a
		l.compareTargets(alias, targets, a)
		if a.State == HolderAwake && a.SessionID != "" && awake == nil {
			awake = &Where{Alias: alias, HostID: a.HostID, SessionID: a.SessionID, State: HolderAwake, At: l.now()}
		}
	}
	if awake != nil {
		l.remember(*awake)
		return *awake, nil
	}
	if !autoWake {
		return Where{}, ErrNoWatcher
	}
	// (2) the last watcher's host is reachable and holds it resting → wake it
	l.mu.Lock()
	last := clone(l.mem.Last)
	l.mu.Unlock()
	if last != nil {
		if a, ok := reachable[last.Alias]; ok && a.State == HolderResting && a.SessionID != "" {
			if woke, err := l.ask.Wake(ctx, last.Alias); err == nil && woke.State == HolderAwake && woke.SessionID != "" {
				w := Where{Alias: last.Alias, HostID: woke.HostID, SessionID: woke.SessionID, State: HolderAwake, At: l.now()}
				l.remember(w)
				return w, nil
			}
		}
	}
	// (3) the first reachable target starts a new one. A target that holds a resting watcher of its own is woken first by
	// the same rule: it is the one a start there would have collided with.
	for _, alias := range targets {
		a, ok := reachable[alias]
		if !ok {
			continue
		}
		var res HolderAnswer
		var err error
		if a.State == HolderResting && a.SessionID != "" {
			res, err = l.ask.Wake(ctx, alias)
		} else {
			res, err = l.ask.Start(ctx, alias)
		}
		if err != nil || res.State != HolderAwake || res.SessionID == "" {
			continue // this one could not: the next target
		}
		w := Where{Alias: alias, HostID: res.HostID, SessionID: res.SessionID, State: HolderAwake, At: l.now()}
		l.remember(w)
		return w, nil
	}
	return Where{}, ErrNoWatcher
}

func (l *Locator) remember(w Where) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.mem.Current, l.mem.Last = &w, &w
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
