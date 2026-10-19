package peers

// Alias retention (name pool v2 spec §6.3): an alias is deleted once its session's lineage has had no live successor for 30
// days, and the table keeps at most store.MaxPeerNameAliases rows (the oldest by created_at go first; a rename trims as it
// inserts, this pass is the backstop). One pass per hour.

import (
	"context"
	"time"

	ipeers "github.com/wake/purdex/internal/peers"
	"github.com/wake/purdex/internal/store"
)

const (
	// aliasRetention is how long an alias outlives the last sign of a live successor of its session.
	aliasRetention = 30 * 24 * time.Hour
	// aliasRetentionEvery is the period of the pass; aliasRetentionFirst is the wait before the first one, so a daemon that
	// has just started is not building a second inventory beside the clients' first ones.
	aliasRetentionEvery = time.Hour
	aliasRetentionFirst = time.Minute
)

// AliasRetainer is what the pass needs of the name store beyond PeerNameStore (store.PeerNameStore has it); a store without it
// (the module's other fakes) is left alone.
type AliasRetainer interface {
	TouchAliases(ctx context.Context, aliases []string, nowMs int64) error
	ExpireAliases(ctx context.Context, cutoffMs int64) (int, error)
	TrimAliases(ctx context.Context, keep int) (int, error)
}

// retainAliases is one pass. An alias whose ref is live now — the live row's own ref, or one it took over from through a relay
// (previous_refs) — is stamped; then every alias with no sign of life for aliasRetention is deleted; then the table is cut to its
// cap. The stamping and deleting need an inventory that can vouch for its rows (liveRefs' ok): a partial one, or one whose
// lineage could not be read, may only be missing a successor, so it changes nothing. A failure is logged and the pass goes on.
func (m *Module) retainAliases(ctx context.Context) {
	r, ok := m.peerNames.(AliasRetainer)
	if !ok {
		return
	}
	if live, whole := m.liveRows(ctx); whole {
		m.retainByLiveness(ctx, r, live)
	}
	if n, err := r.TrimAliases(ctx, store.MaxPeerNameAliases); err != nil {
		m.logf("peers: alias retention: trim: %v", err)
	} else if n > 0 {
		m.logf("peers: alias retention: dropped %d aliases over the cap of %d", n, store.MaxPeerNameAliases)
	}
}

func (m *Module) retainByLiveness(ctx context.Context, r AliasRetainer, live liveRows) {
	nowMs := m.now().UnixMilli()
	rows, err := m.peerNames.Aliases(ctx)
	if err != nil {
		m.logf("peers: alias retention: read the aliases: %v", err)
		return
	}
	var touch []string
	for _, a := range rows {
		if live.has(a) {
			touch = append(touch, a.Alias)
		}
	}
	if err := r.TouchAliases(ctx, touch, nowMs); err != nil {
		// without the stamps a live alias could look dead: delete nothing this pass
		m.logf("peers: alias retention: stamp %d aliases: %v", len(touch), err)
		return
	}
	n, err := r.ExpireAliases(ctx, nowMs-aliasRetention.Milliseconds())
	if err != nil {
		m.logf("peers: alias retention: expire: %v", err)
		return
	}
	if n > 0 {
		m.logf("peers: alias retention: dropped %d aliases with no live successor for %s", n, aliasRetention)
	}
}

// liveRows is what a whole local inventory says is alive. A ref is only 6 base36 digits of a hash, so two sessions can share one:
// own keeps, per ref, the sessions of the live rows that carry it (the row's agent session, an execution's too), and an alias
// counts as alive through its own row only when its session is among them. taken is the refs live rows took over from through
// a relay (previous_refs, which are refs alone): an alias of a predecessor is alive through its successor, a different session.
type liveRows struct {
	own   map[string]map[string]bool // ref -> session ids
	taken map[string]bool            // refs
}

// has reports whether the session alias a stands for has a live row, or a live successor.
func (l liveRows) has(a store.PeerNameAlias) bool {
	return l.own[a.Ref][a.SessionID] || l.taken[a.Ref]
}

// liveRows is the local inventory's liveRows. whole is false when the inventory cannot vouch for it. m.aliasLiveRefs replaces
// it in tests.
func (m *Module) liveRows(ctx context.Context) (live liveRows, whole bool) {
	if m.aliasLiveRefs != nil {
		return m.aliasLiveRefs(ctx)
	}
	snap := m.configSnapshot()
	return envelopeLiveRows(m.localEnvelope(ctx, snap.hostID, snap.alias))
}

// envelopeLiveRows is liveRows over one local envelope. A session and an execution are both named and can both leave an alias, so
// an inventory missing either (executions_unavailable), or its relay lineage, or any row (partial), may only be missing a
// successor: whole is false.
func envelopeLiveRows(env ipeers.Envelope) (live liveRows, whole bool) {
	if !env.OK || env.Partial || env.LineageUnavailable || env.ExecutionsUnavailable {
		return liveRows{}, false
	}
	live = liveRows{own: map[string]map[string]bool{}, taken: map[string]bool{}}
	for _, rec := range env.Peers {
		if rec.Ref != "" && rec.Agent != nil && rec.Agent.SessionID != "" {
			if live.own[rec.Ref] == nil {
				live.own[rec.Ref] = map[string]bool{}
			}
			live.own[rec.Ref][rec.Agent.SessionID] = true
		}
		for _, p := range rec.PreviousRefs {
			live.taken[p] = true
		}
	}
	return live, true
}

// startAliasRetention runs the pass once after aliasRetentionFirst and then every aliasRetentionEvery until Stop (joined
// through reapWG, after stopCtx is cancelled). No-op for a name store that cannot retain.
func (m *Module) startAliasRetention() {
	if _, ok := m.peerNames.(AliasRetainer); !ok {
		return
	}
	first, every := m.aliasRetentionFirst, m.aliasRetentionEvery
	if first <= 0 {
		first = aliasRetentionFirst
	}
	if every <= 0 {
		every = aliasRetentionEvery
	}
	m.reapWG.Add(1)
	go func() {
		defer m.reapWG.Done()
		timer := time.NewTimer(first)
		defer timer.Stop()
		for {
			select {
			case <-m.stopCtx.Done():
				return
			case <-timer.C:
				m.retainAliases(m.stopCtx)
				timer.Reset(every)
			}
		}
	}()
}
