package peers

// Picking a pool name for a new conversation and settling two live holders of
// one name (name pool spec §4, §5). The pool is opt-in (WithNamePool): a
// Module without it names "<base>-<ref[1:3]>" exactly as before.

import (
	"context"
	"math/rand/v2"
	"sort"
	"strings"
	"time"

	ipeers "github.com/wake/purdex/internal/peers"
	"github.com/wake/purdex/internal/store"
)

// NamePicker chooses one name from the free ones. Random in production, a
// fixed choice in tests.
type NamePicker interface {
	Pick(free []string) string
}

type randomPicker struct{}

func (randomPicker) Pick(free []string) string { return free[rand.IntN(len(free))] }

// RandomNamePicker picks uniformly at random.
func RandomNamePicker() NamePicker { return randomPicker{} }

// WithNamePool turns the name pool on: a conversation seen for the first time
// with no predecessor name gets "<base>-<a free pool name>", and two live
// holders of one pool name are settled by the newcomer rule. It also starts
// keeping the snapshot of what other hosts hold. A nil picker leaves the pool
// off. Returns m for chaining.
func (m *Module) WithNamePool(p NamePicker) *Module {
	m.namePicker = p
	m.remoteNames = nil
	if p != nil {
		m.remoteNames = newRemoteNameSnapshot()
	}
	return m
}

// lockPool serialises picking and storing a pool name (and settling two
// holders of one). The wait gives up when ctx does, so a slow holder of the lock
// costs its waiters their name for this pass (the ref form, retried next pass),
// never their budget.
func (m *Module) lockPool(ctx context.Context) (unlock func(), err error) {
	m.poolOnce.Do(func() { m.poolSem = make(chan struct{}, 1) })
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	select {
	case m.poolSem <- struct{}{}:
		m.reservations.newSection() // a failed rebuild of the reservations is tried once per section (namereserve.go)
		return func() { <-m.poolSem }, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// awaitPoolReady waits, within ctx, for the refresher's first pass over the
// other hosts: a pool name picked before that would be picked blind to what
// they hold (name pool spec §4: scan all addresses once). Before Start there is
// no refresher and nothing to wait for. A caller whose ctx ends first gets no
// name this pass.
func (m *Module) awaitPoolReady(ctx context.Context) error {
	if m.poolReady == nil {
		return nil
	}
	select {
	case <-m.poolReady:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// heldPoolNames is every pool name a live conversation holds right now: the
// stored pool names of the live local registry entries (read fresh, so a pick
// another pass made a moment ago is seen), the names the other hosts hold, and
// the names picked earlier in this pass. Dead conversations hold nothing: their
// rows stay in peer_names but no live entry points at them. Called with poolMu
// held.
func (m *Module) heldPoolNames(ctx context.Context, picked map[string]bool) (map[string]bool, error) {
	held := make(map[string]bool, len(picked))
	for n := range picked {
		held[n] = true
	}
	// the names reserved for spawns that have not produced their session yet (namereserve.go)
	m.ensureReservations(ctx)
	for n := range m.reservations.codes(m.now()) {
		held[n] = true
	}
	entries, _, err := ipeers.ReadRegistry(m.registryDir, m.liveness)
	if err != nil {
		return nil, err
	}
	sids := make([]string, 0, len(entries))
	for _, e := range entries {
		if e.SessionID != "" && !e.IsProxy {
			sids = append(sids, e.SessionID)
		}
	}
	rows, err := m.peerNames.Lookup(ctx, sids)
	if err != nil {
		return nil, err
	}
	for _, r := range rows {
		if n, ok := ipeers.PoolNameOf(r.Name); ok {
			held[n] = true
		}
	}
	if m.remoteNames != nil {
		for n := range m.remoteNames.held(m.now()) {
			held[n] = true
		}
	}
	return held, nil
}

// freePoolNames is the pickable pool (the active personas, or the built-in names while there are none) minus held, in pool
// order.
func freePoolNames(held map[string]bool) []string {
	pickable := ipeers.PickablePool()
	free := make([]string, 0, len(pickable))
	for _, n := range pickable {
		if !held[n] {
			free = append(free, n)
		}
	}
	return free
}

// assignPooled stores the name of a conversation seen for the first time:
// "<base>-<a free pool name>" with source pool, else (every pool name held)
// refName with its own source. Picking and storing run under the pool lock, so
// concurrent passes never hand one name to two conversations (the lock wait and
// the wait for the first look at the other hosts both end with ctx); picked carries
// the names this pass already took, for conversations (executions) no registry
// entry shows. The stored row is returned: with Assign's first-writer-wins, a
// concurrent pass that named the same conversation first wins.
func (m *Module) assignPooled(ctx context.Context, sid, ref, base, refName, refSource, tmuxName string, nowMs int64, picked map[string]bool) (store.PeerNameEntry, error) {
	if err := m.awaitPoolReady(ctx); err != nil {
		return store.PeerNameEntry{}, err
	}
	unlock, err := m.lockPool(ctx)
	if err != nil {
		return store.PeerNameEntry{}, err
	}
	defer unlock()
	// A session born from a spawn takes the name its op reserved (matched by the op's tmux session name, NP-0 result b′).
	m.ensureReservations(ctx)
	pick, ok := "", false
	if r, found := m.reservations.byTmux(tmuxName, m.now()); found {
		pick, ok = r.code, true
	} else {
		var err error
		if pick, ok, err = m.pickOther(ctx, picked); err != nil {
			return store.PeerNameEntry{}, err
		}
	}
	// The registry read cannot be cancelled: a budget it ran past means no
	// name this pass rather than a pick made after the caller gave up.
	if err := ctx.Err(); err != nil {
		return store.PeerNameEntry{}, err
	}
	name, source := refName, refSource
	if ok {
		if n, ok := ipeers.VirtualNameWith(base, pick); ok {
			name, source = n, store.PeerNameSourcePool
		}
	} else {
		m.logf("peers: name pool: every pool name is held, %s takes its ref suffix", sid)
	}
	row, err := m.peerNames.Assign(ctx, sid, ref, name, source, nowMs)
	if err != nil {
		return store.PeerNameEntry{}, err
	}
	if n, ok := ipeers.PoolNameOf(row.Name); ok {
		picked[n] = true
	}
	return row, nil
}

// poolHolder is one live local conversation holding a pool name.
type poolHolder struct {
	sid   string // as the pass keyed it
	key   string // lowercase
	ref   string
	prev  []string // relay lineage, newest first
	name  string   // its virtual name
	pool  string   // the pool name at its end
	start time.Time
}

// inLineage reports whether a and b are one relay lineage: one's ref is among
// the other's predecessors. A predecessor and its successor never count as two
// holders of one name.
func inLineage(a, b poolHolder) bool {
	for _, r := range a.prev {
		if r == b.ref {
			return true
		}
	}
	for _, r := range b.prev {
		if r == a.ref {
			return true
		}
	}
	return false
}

// settlePoolCollisions applies the newcomer rule (spec §5) to the live local
// conversations of an inventory pass: when two live conversations outside one
// lineage hold the same pool name — whether a resume found its old name taken,
// or two hosts picked it in one snapshot window — the one whose process
// started later picks a new name (RenamePool, a compare-and-set on the name the
// pass just read); equal starts: the larger session id yields, and a local
// conversation equal to a remote holder yields. A remote holder is judged by
// the snapshot (the other daemon renames its own conversation).
//
// names is the pass's answer keyed by session id and is updated in place for a
// renamed conversation. entries are the pass's live registry entries (already
// read, so this adds no fork to a pass); the rename itself picks from names
// read fresh. A failed rename keeps the stored name for this pass.
func (m *Module) settlePoolCollisions(ctx context.Context, entries []ipeers.Entry, proxyPIDs map[int]bool, lineage map[string][]string, names map[string]string) {
	if m.namePicker == nil || m.peerNames == nil {
		return
	}
	var holders []poolHolder
	seen := map[string]bool{}
	for _, e := range entries {
		key := strings.ToLower(e.SessionID)
		if key == "" || e.IsProxy || proxyPIDs[e.PID] || seen[key] {
			continue
		}
		name := names[e.SessionID]
		pool, ok := ipeers.PoolNameOf(name)
		if !ok {
			continue
		}
		seen[key] = true
		start, err := ipeers.ParseProcStart(e.ProcStart)
		if err != nil {
			start = m.now() // a registry entry always parses; unreachable, and the newest if not
		}
		holders = append(holders, poolHolder{sid: e.SessionID, key: key, ref: ipeers.RefID(e.SessionID),
			prev: lineage[e.SessionID], name: name, pool: pool, start: start})
	}
	if len(holders) == 0 {
		return
	}
	var remote map[string]time.Time
	if m.remoteNames != nil {
		remote = m.remoteNames.held(m.now())
	}
	var yielding []poolHolder
	for _, l := range holders {
		if rs, held := remote[l.pool]; held && !rs.After(l.start) {
			yielding = append(yielding, l)
			continue
		}
		for _, o := range holders {
			if o.key == l.key || o.pool != l.pool || inLineage(l, o) {
				continue
			}
			if o.start.Before(l.start) || (o.start.Equal(l.start) && o.key < l.key) {
				yielding = append(yielding, l)
				break
			}
		}
	}
	if len(yielding) == 0 {
		return
	}
	sort.Slice(yielding, func(i, j int) bool { return yielding[i].key < yielding[j].key })

	unlock, err := m.lockPool(ctx)
	if err != nil {
		m.logf("peers: name pool: %d conversation(s) share a pool name but the naming lock was not free in time, settled next pass: %v", len(yielding), err)
		return
	}
	defer unlock()
	picked := map[string]bool{}
	for _, l := range yielding {
		pick, ok, err := m.pickOther(ctx, picked)
		if err != nil {
			m.logf("peers: name pool: cannot read the held names, %s keeps %s this pass: %v", l.sid, l.name, err)
			continue
		}
		base := strings.TrimSuffix(l.name, "-"+l.pool)
		var newName string
		if ok {
			newName, _ = ipeers.VirtualNameWith(base, pick)
		} else {
			newName, _ = ipeers.VirtualName(base, l.ref)
		}
		if newName == "" || newName == l.name {
			continue
		}
		row, ok, err := m.peerNames.RenamePool(ctx, l.key, l.name, newName)
		switch {
		case err != nil:
			m.logf("peers: name pool: renaming %s from %s failed, it keeps its name this pass: %v", l.sid, l.name, err)
		case !ok:
			m.logf("peers: name pool: %s changed under the rename of %s, keeping what is stored", l.sid, l.name)
			if ipeers.RoutableName(row.Name) {
				names[l.sid] = row.Name
			}
		default:
			if n, isPool := ipeers.PoolNameOf(row.Name); isPool {
				picked[n] = true
			}
			if ipeers.RoutableName(row.Name) {
				names[l.sid] = row.Name
			}
		}
	}
}
