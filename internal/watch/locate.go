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
	State     HolderState `json:"state"`           // awake or resting, as the host last said
	At        time.Time   `json:"at"`              // when this was learned
	Since     time.Time   `json:"since,omitempty"` // when the host's holder entered that state (the "awake the longest" rule)
}

// HolderAnswer is what a host says about the watcher it holds (the wire answer of POST /api/peers/watch/holder).
type HolderAnswer struct {
	HostID    string
	State     HolderState
	SessionID string
	Since     time.Time // when it entered that state; zero when the host could not say (an older daemon)
	Targets   []string  // that host's setting, nil when it could not say
	// Starting says a start or wake is in progress on that host (it answered within its grace and goes on in the background): no
	// other host starts one meanwhile.
	Starting bool
}

// Earlier says whether the watcher of a wins over the watcher of b when both are awake (spec §12.3 "Two awake at once"): the one
// awake the longest, which is the earliest since; on a tie (the same second) the smaller host id. A host that cannot say since is
// the youngest. The rule needs no message order, so every daemon that knows both answers picks the same one, whatever the order
// of its targets.
func Earlier(a, b HolderAnswer) bool {
	az, bz := a.Since.IsZero(), b.Since.IsZero()
	switch {
	case az != bz:
		return bz // the one that has a since is older than the one that has none
	case !az && a.Since.Unix() != b.Since.Unix():
		return a.Since.Before(b.Since)
	}
	return a.HostID < b.HostID
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
	// hints are the watchers Prefer was told of; gen counts them. A search that started before a hint and would settle on a worse
	// watcher than that hint looks again (afterProbe is a test seam between the probes and the decision).
	gen        int
	hints      []hint
	afterProbe func()
	// onChange, when set, is called (without the locator's lock held) after the remembered watcher changed.
	onChange func()
}

type hint struct {
	gen int
	a   HolderAnswer
}

const maxHints = 8

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

// SetOnChange sets the function called after the remembered watcher (Current) changed: a search remembered one, a failed delivery
// or a better announced watcher forgot it. It runs without the locator's lock, in the goroutine that made the change.
func (l *Locator) SetOnChange(fn func()) {
	l.mu.Lock()
	l.onChange = fn
	l.mu.Unlock()
}

func (l *Locator) changed() {
	l.mu.Lock()
	fn := l.onChange
	l.mu.Unlock()
	if fn != nil {
		fn()
	}
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

// Prefer is told of a watcher that is awake (another host announced it): when it beats the remembered one by the "awake the
// longest" rule, the remembered one is forgotten (it stays the last one) and the next Current looks again and picks by that rule.
// A watcher announced by the host of the remembered one, with another session, replaces it whatever its since (the host
// restarted its watcher). Anything that is not awake, is the remembered watcher itself, or does not beat it, changes nothing.
func (l *Locator) Prefer(a HolderAnswer) {
	if l.prefer(a) {
		l.changed()
	}
}

// prefer is Prefer; it says whether the remembered watcher was forgotten.
func (l *Locator) prefer(a HolderAnswer) bool {
	if a.State != HolderAwake || a.SessionID == "" {
		return false
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	// Remember it for a search that is in progress (it may have asked this host before the watcher changed): the search compares
	// its pick against the hints that arrived after it began.
	l.gen++
	l.hints = append(l.hints, hint{gen: l.gen, a: a})
	if len(l.hints) > maxHints {
		l.hints = l.hints[len(l.hints)-maxHints:]
	}
	c := l.mem.Current
	if c == nil || (c.HostID == a.HostID && c.SessionID == a.SessionID) {
		return false
	}
	if c.HostID == a.HostID { // the same host now has another watcher awake: the remembered one is an earlier run of it
		l.mem.Current = nil
		l.save()
		return true
	}
	cur := HolderAnswer{HostID: c.HostID, State: c.State, SessionID: c.SessionID, Since: c.Since}
	if !Earlier(a, cur) {
		return false
	}
	l.mem.Current = nil
	l.save()
	return true
}

// Observe records what the host alias (whose id is a.HostID) says now about the watcher the locator knows on that host, the
// remembered one or the last one: the cache is otherwise only written when a search finds a watcher, so a remote watcher that
// rested, was woken or vanished would be reported as it was found. An awake watcher there becomes the remembered one; a resting one
// is no longer a place to deliver to (Current is cleared) but stays the last one, marked resting, for a wake; a host that holds
// none forgets what was known of it. A host the locator knows nothing of is left alone.
//
// asked is the watcher the question was about, as the locator held it when the question went out. The answer arrives later, so it
// is applied only if that watcher is still the one held for that host (same session, not found again since) and no other host has
// become the remembered one meanwhile; otherwise it is stale and dropped (the next refresh asks again).
func (l *Locator) Observe(alias string, asked Where, a HolderAnswer) {
	l.mu.Lock()
	known := func(w *Where) bool { return w != nil && w.HostID == a.HostID }
	same := func(w *Where) bool { return known(w) && w.SessionID == asked.SessionID && w.At.Equal(asked.At) }
	if !same(l.mem.Current) && !(l.mem.Current == nil && same(l.mem.Last)) {
		l.mu.Unlock()
		return
	}
	switch {
	case a.State == HolderAwake && a.SessionID != "":
		w := Where{Alias: alias, HostID: a.HostID, SessionID: a.SessionID, State: HolderAwake, At: l.now(), Since: a.Since}
		l.mem.Current, l.mem.Last = &w, &w
	case a.State == HolderResting && a.SessionID != "":
		w := Where{Alias: alias, HostID: a.HostID, SessionID: a.SessionID, State: HolderResting, At: l.now(), Since: a.Since}
		if known(l.mem.Current) {
			l.mem.Current = nil
		}
		l.mem.Last = &w
	default: // none
		if known(l.mem.Current) {
			l.mem.Current = nil
		}
		if known(l.mem.Last) {
			l.mem.Last = nil
		}
	}
	l.save()
	l.mu.Unlock()
	l.changed()
}

// supersededSince says whether a hint that arrived after generation start beats w (the watcher a search settled on): a watcher
// that is older, or another session of w's own host. The search then did not see it and must look again.
func (l *Locator) supersededSince(start int, w Where) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	pick := HolderAnswer{HostID: w.HostID, State: w.State, SessionID: w.SessionID, Since: w.Since}
	for _, h := range l.hints {
		if h.gen <= start || (h.a.HostID == w.HostID && h.a.SessionID == w.SessionID) {
			continue
		}
		if h.a.HostID == w.HostID || Earlier(h.a, pick) {
			return true
		}
	}
	return false
}

func (l *Locator) generation() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.gen
}

// forgetCurrent drops the remembered watcher (it stays the last one).
func (l *Locator) forgetCurrent() {
	l.mu.Lock()
	l.mem.Current = nil
	l.save()
	l.mu.Unlock()
	l.changed()
}

// Failed says a delivery to w failed or w is gone: the next Current looks again. Only when w is still the remembered watcher: a
// late failure of an earlier delivery must not clear what a search found since. The watcher stays as the last one, for a wake.
func (l *Locator) Failed(w Where) {
	l.mu.Lock()
	c := l.mem.Current
	if c == nil || c.HostID != w.HostID || c.SessionID != w.SessionID {
		l.mu.Unlock()
		return
	}
	l.mem.Current = nil
	l.save()
	l.mu.Unlock()
	l.changed()
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
	return l.searchSettled(ctx, targets, autoWake)
}

// searchSettled runs search and looks again (a few times) when a watcher that Prefer was told of while it ran beats what it settled on.
func (l *Locator) searchSettled(ctx context.Context, targets []string, autoWake bool) (Where, error) {
	for attempt := 0; ; attempt++ {
		start := l.generation()
		w, err := l.search(ctx, targets, autoWake)
		if err != nil || attempt >= 2 || !l.supersededSince(start, w) {
			return w, err
		}
		l.forgetCurrent()
	}
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
			return &Where{Alias: alias, HostID: res.HostID, SessionID: res.SessionID, State: HolderAwake, At: l.now(), Since: res.Since}, true
		}
		if res.Starting {
			return nil, false // it is coming up there: not settled, and nothing is started elsewhere meanwhile
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
		return &Where{Alias: alias, HostID: a.HostID, SessionID: a.SessionID, State: HolderAwake, At: l.now(), Since: a.Since}, true
	}
	if a.Starting {
		return nil, false // the call was cut off and the host says the start is going on there
	}
	return nil, true
}

func (l *Locator) search(ctx context.Context, targets []string, autoWake bool) (Where, error) {
	reachable := map[string]HolderAnswer{} // the targets that answered the holder question
	var awake *Where
	var awakeAns *HolderAnswer // the answer the awake pick came from
	// (1) the watcher awake the longest on a target (spec §12.3), not the first one in the order of targets
	for i, p := range l.probeAll(ctx, targets) {
		if p.err != nil {
			continue // unreachable, or no watch support: not a candidate for anything
		}
		alias := targets[i]
		reachable[alias] = p.a
		l.compareTargets(alias, targets, p.a)
		if p.a.State == HolderAwake && p.a.SessionID != "" && (awake == nil || Earlier(p.a, *awakeAns)) {
			awake = &Where{Alias: alias, HostID: p.a.HostID, SessionID: p.a.SessionID, State: HolderAwake, At: l.now(), Since: p.a.Since}
			ans := p.a
			awakeAns = &ans
		}
	}
	if l.afterProbe != nil {
		l.afterProbe()
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

// Uncertain is the host whose start or wake has not been seen to end (the alias), and when that was noted.
func (l *Locator) Uncertain() (alias string, at time.Time, ok bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if u := l.mem.Uncertain; u != nil {
		return u.Alias, u.At, true
	}
	return "", time.Time{}, false
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
	l.mem.Current, l.mem.Last = &w, &w
	l.mem.Uncertain = nil // a watcher is accounted for
	l.save()
	l.mu.Unlock()
	l.changed()
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
