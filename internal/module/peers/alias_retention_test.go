package peers

import (
	"context"
	"sync"
	"testing"
	"time"

	ipeers "github.com/wake/purdex/internal/peers"
	"github.com/wake/purdex/internal/store"
)

// Alias retention (name pool v2 spec §6.3): an alias goes once its session's lineage has had no live successor for 30 days; the
// table keeps at most 512 rows. One pass per hour.

const (
	retSidA = "aaaaaaaa-1111-4111-8111-00000000000a"
	retSidB = "bbbbbbbb-1111-4111-8111-00000000000b"
	retSidC = "cccccccc-1111-4111-8111-00000000000c"
)

// aliasesOf makes one alias per session (renamed once) and returns the module over the store.
func aliasesOf(t *testing.T) (*Module, *store.MetaStore) {
	t.Helper()
	m, meta := namerFixture(t)
	ctx := context.Background()
	for i, sid := range []string{retSidA, retSidB, retSidC} {
		old, renamed := "old"+string(rune('a'+i)), "new"+string(rune('a'+i))
		if _, err := meta.PeerNames().Assign(ctx, sid, ipeers.RefID(sid), old, store.PeerNameSourcePool, 10); err != nil {
			t.Fatal(err)
		}
		if _, ok, err := meta.PeerNames().RenamePool(ctx, sid, old, renamed); err != nil || !ok {
			t.Fatalf("rename %s: ok=%v err=%v", sid, ok, err)
		}
	}
	return m, meta
}

func aliasNames(t *testing.T, meta *store.MetaStore) []string {
	t.Helper()
	rows, err := meta.PeerNames().Aliases(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, r := range rows {
		out = append(out, r.Alias)
	}
	return out
}

// 31 days on, an alias whose ref is live (itself, or as a previous ref of the live successor) is stamped and stays, and one with
// no live ref is deleted. Mutation gate: do not stamp → red; expire without the cutoff → red; ignore previous refs → red.
func TestRetainAliases_AnAliasWithALiveSuccessorStaysAndADeadOneGoes(t *testing.T) {
	m, meta := aliasesOf(t)
	later := time.Now().Add(31 * 24 * time.Hour)
	m.now = func() time.Time { return later }
	m.aliasLiveRefs = func(context.Context) (liveRows, bool) {
		// A is live itself; B is only a previous ref of a live row; C has nothing
		return liveRows{own: map[string]map[string]bool{ipeers.RefID(retSidA): {retSidA: true}}, taken: map[string]bool{ipeers.RefID(retSidB): true}}, true
	}
	m.retainAliases(context.Background())
	got := aliasNames(t, meta)
	if len(got) != 2 || got[0] != "olda" || got[1] != "oldb" {
		t.Fatalf("aliases = %v, want olda and oldb", got)
	}
	// 31 days after that stamp the same two are dead too
	m.now = func() time.Time { return later.Add(31 * 24 * time.Hour) }
	m.aliasLiveRefs = func(context.Context) (liveRows, bool) { return liveRows{}, true }
	m.retainAliases(context.Background())
	if got := aliasNames(t, meta); len(got) != 0 {
		t.Fatalf("aliases = %v, want none", got)
	}
}

// Two sessions can share a ref (a ref is 6 base36 digits of a hash). A live row that carries the dead alias's ref but is another
// session does not keep the alias alive: it would route the old address to a stranger forever. A row of the alias's own session
// does. Mutation gate: match on the ref alone → red.
func TestRetainAliases_ALiveStrangerWithTheSameRefDoesNotKeepAnAlias(t *testing.T) {
	m, meta := aliasesOf(t)
	m.now = func() time.Time { return time.Now().Add(31 * 24 * time.Hour) }
	m.aliasLiveRefs = func(context.Context) (liveRows, bool) {
		return liveRows{own: map[string]map[string]bool{
			ipeers.RefID(retSidA): {"some-other-session": true}, // A's ref, not A
			ipeers.RefID(retSidB): {retSidB: true},              // B itself
		}}, true
	}
	m.retainAliases(context.Background())
	if got := aliasNames(t, meta); len(got) != 1 || got[0] != "oldb" {
		t.Fatalf("aliases = %v, want only oldb", got)
	}
}

// Inside the 30 days nothing is deleted, live or not.
func TestRetainAliases_NothingGoesBeforeThirtyDays(t *testing.T) {
	m, meta := aliasesOf(t)
	later := time.Now().Add(29 * 24 * time.Hour)
	m.now = func() time.Time { return later }
	m.aliasLiveRefs = func(context.Context) (liveRows, bool) { return liveRows{}, true }
	m.retainAliases(context.Background())
	if got := aliasNames(t, meta); len(got) != 3 {
		t.Fatalf("aliases = %v, want all three", got)
	}
}

// An inventory that cannot vouch (partial, or its lineage unreadable) stamps nothing and deletes nothing: a miss may only be a
// missing row. Mutation gate: delete on a partial inventory → red.
func TestRetainAliases_AnInventoryThatCannotVouchDeletesNothing(t *testing.T) {
	m, meta := aliasesOf(t)
	m.now = func() time.Time { return time.Now().Add(90 * 24 * time.Hour) }
	m.aliasLiveRefs = func(context.Context) (liveRows, bool) { return liveRows{}, false }
	m.retainAliases(context.Background())
	if got := aliasNames(t, meta); len(got) != 3 {
		t.Fatalf("aliases = %v, want all three", got)
	}
}

// retainRecorder sees the cap the pass asks for.
type retainRecorder struct {
	*store.PeerNameStore
	mu   sync.Mutex
	keep []int
}

func (r *retainRecorder) TrimAliases(ctx context.Context, keep int) (int, error) {
	r.mu.Lock()
	r.keep = append(r.keep, keep)
	r.mu.Unlock()
	return r.PeerNameStore.TrimAliases(ctx, keep)
}

// The pass trims to 512 even when the inventory cannot vouch (the cap does not depend on liveness). Mutation gate: no trim → red.
func TestRetainAliases_TheCapIsAskedForEvenWithoutAnInventory(t *testing.T) {
	m, meta := namerFixture(t)
	rec := &retainRecorder{PeerNameStore: meta.PeerNames()}
	m.WithPeerNames(rec, meta.ConversationNames())
	m.aliasLiveRefs = func(context.Context) (liveRows, bool) { return liveRows{}, false }
	m.retainAliases(context.Background())
	if len(rec.keep) != 1 || rec.keep[0] != store.MaxPeerNameAliases {
		t.Fatalf("trim asked with %v, want [%d]", rec.keep, store.MaxPeerNameAliases)
	}
}

// A store that cannot retain (the module's other fakes) is left alone.
func TestRetainAliases_ANameStoreWithoutRetentionIsSkipped(t *testing.T) {
	m := New(nil, nil)
	m.logf = t.Logf
	m.aliasLiveRefs = func(context.Context) (liveRows, bool) {
		t.Fatal("asked for live refs without a retainer")
		return liveRows{}, false
	}
	m.retainAliases(context.Background()) // nil store
	m.WithPeerNames(failingNames{PeerNameStore: nil}, nil)
	m.retainAliases(context.Background())
}

// What an inventory says is alive: a row's ref together with the session it belongs to (an execution row carries one too), and the
// refs its rows took over from. An inventory that cannot vouch for its rows — partial, lineage unreadable, executions unreadable (an
// execution is named and aliased like a session), or not ok — is no basis for a deletion. Mutation gate: drop any one of the four
// conditions → red; ignore previous refs → red; ignore the session → red.
func TestEnvelopeLiveRows_OnlyAnInventoryThatCanVouchCounts(t *testing.T) {
	rows := []ipeers.PeerRecord{
		{Ref: "_aaaaaa", PreviousRefs: []string{"_bbbbbb", "_cccccc"}, Agent: &ipeers.AgentInfo{SessionID: "sid-a"}},
		{Ref: "_dddddd", RowKind: ipeers.RowKindExecution, Agent: &ipeers.AgentInfo{SessionID: "sid-d"}},
		{Ref: "_eeeeee"}, // no agent: no session to match
		{Ref: ""},
	}
	live, ok := envelopeLiveRows(ipeers.Envelope{OK: true, Peers: rows})
	if !ok || !live.has(store.PeerNameAlias{Ref: "_aaaaaa", SessionID: "sid-a"}) || !live.has(store.PeerNameAlias{Ref: "_dddddd", SessionID: "sid-d"}) {
		t.Fatalf("live = %+v ok = %v, want the session and the execution row alive", live, ok)
	}
	if live.has(store.PeerNameAlias{Ref: "_aaaaaa", SessionID: "sid-stranger"}) || live.has(store.PeerNameAlias{Ref: "_eeeeee", SessionID: "sid-e"}) {
		t.Fatalf("live = %+v: a ref without its session must not count as the session", live)
	}
	for _, ref := range []string{"_bbbbbb", "_cccccc"} {
		if !live.has(store.PeerNameAlias{Ref: ref, SessionID: "sid-gone"}) {
			t.Fatalf("live = %+v: %s was taken over by a live row, whoever held it", live, ref)
		}
	}
	for name, env := range map[string]ipeers.Envelope{
		"not ok":                 {OK: false, Peers: rows},
		"partial":                {OK: true, Partial: true, Peers: rows},
		"lineage unavailable":    {OK: true, LineageUnavailable: true, Peers: rows},
		"executions unavailable": {OK: true, ExecutionsUnavailable: true, Peers: rows},
	} {
		if live, ok := envelopeLiveRows(env); ok || len(live.own) != 0 || len(live.taken) != 0 {
			t.Errorf("%s: live = %+v ok = %v, want none", name, live, ok)
		}
	}
}

// Start begins the loop: a Module with a retaining name store runs a pass after the first delay without anything calling it.
// Mutation gate: Start does not start the loop → red.
func TestModuleStart_StartsTheAliasRetentionLoop(t *testing.T) {
	e := newDeliverEnv(t, envOpts{fixture: fixtureOpts{noSweep: true}})
	meta, err := store.OpenMeta(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { meta.Close() })
	e.m.WithPeerNames(meta.PeerNames(), meta.ConversationNames())
	ran := make(chan struct{}, 1)
	e.m.aliasLiveRefs = func(context.Context) (liveRows, bool) {
		select {
		case ran <- struct{}{}:
		default:
		}
		return liveRows{}, false
	}
	e.m.aliasRetentionFirst = time.Millisecond
	if err := e.m.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() { e.m.Stop(context.Background()) })
	select {
	case <-ran:
	case <-time.After(10 * time.Second):
		t.Fatal("no retention pass ran after Start")
	}
}

// The loop runs a pass after the first delay and then every period, and Stop ends it. Mutation gate: no loop → red.
func TestAliasRetention_LoopRunsPassesAndStopEndsIt(t *testing.T) {
	m, _ := aliasesOf(t)
	var mu sync.Mutex
	passes := 0
	m.aliasLiveRefs = func(context.Context) (liveRows, bool) {
		mu.Lock()
		passes++
		mu.Unlock()
		return liveRows{}, false
	}
	m.aliasRetentionFirst, m.aliasRetentionEvery = 2*time.Millisecond, 5*time.Millisecond
	m.startAliasRetention()
	deadline := time.Now().Add(10 * time.Second)
	for {
		mu.Lock()
		n := passes
		mu.Unlock()
		if n >= 3 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("only %d passes ran", n)
		}
		time.Sleep(2 * time.Millisecond)
	}
	if err := m.Stop(context.Background()); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	before := passes
	mu.Unlock()
	time.Sleep(40 * time.Millisecond)
	mu.Lock()
	after := passes
	mu.Unlock()
	if after != before {
		t.Fatalf("passes kept running after Stop: %d -> %d", before, after)
	}
}
