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
	m.aliasLiveRefs = func(context.Context) (map[string]bool, bool) {
		// A is live itself; B is only a previous ref of a live row; C has nothing
		return map[string]bool{ipeers.RefID(retSidA): true, ipeers.RefID(retSidB): true}, true
	}
	m.retainAliases(context.Background())
	got := aliasNames(t, meta)
	if len(got) != 2 || got[0] != "olda" || got[1] != "oldb" {
		t.Fatalf("aliases = %v, want olda and oldb", got)
	}
	// 31 days after that stamp the same two are dead too
	m.now = func() time.Time { return later.Add(31 * 24 * time.Hour) }
	m.aliasLiveRefs = func(context.Context) (map[string]bool, bool) { return map[string]bool{}, true }
	m.retainAliases(context.Background())
	if got := aliasNames(t, meta); len(got) != 0 {
		t.Fatalf("aliases = %v, want none", got)
	}
}

// Inside the 30 days nothing is deleted, live or not.
func TestRetainAliases_NothingGoesBeforeThirtyDays(t *testing.T) {
	m, meta := aliasesOf(t)
	later := time.Now().Add(29 * 24 * time.Hour)
	m.now = func() time.Time { return later }
	m.aliasLiveRefs = func(context.Context) (map[string]bool, bool) { return map[string]bool{}, true }
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
	m.aliasLiveRefs = func(context.Context) (map[string]bool, bool) { return nil, false }
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
	m.aliasLiveRefs = func(context.Context) (map[string]bool, bool) { return nil, false }
	m.retainAliases(context.Background())
	if len(rec.keep) != 1 || rec.keep[0] != store.MaxPeerNameAliases {
		t.Fatalf("trim asked with %v, want [%d]", rec.keep, store.MaxPeerNameAliases)
	}
}

// A store that cannot retain (the module's other fakes) is left alone.
func TestRetainAliases_ANameStoreWithoutRetentionIsSkipped(t *testing.T) {
	m := New(nil, nil)
	m.logf = t.Logf
	m.aliasLiveRefs = func(context.Context) (map[string]bool, bool) {
		t.Fatal("asked for live refs without a retainer")
		return nil, false
	}
	m.retainAliases(context.Background()) // nil store
	m.WithPeerNames(failingNames{PeerNameStore: nil}, nil)
	m.retainAliases(context.Background())
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
	e.m.aliasLiveRefs = func(context.Context) (map[string]bool, bool) {
		select {
		case ran <- struct{}{}:
		default:
		}
		return nil, false
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
	m.aliasLiveRefs = func(context.Context) (map[string]bool, bool) {
		mu.Lock()
		passes++
		mu.Unlock()
		return nil, false
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
