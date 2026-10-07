package peers

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"sync"
	"testing"

	ipeers "github.com/wake/purdex/internal/peers"
	"github.com/wake/purdex/internal/store"
)

// namerFixture is a Module whose namer runs over a real, file-backed meta
// store (two pool connections, so concurrent passes really overlap).
func namerFixture(t *testing.T) (*Module, *store.MetaStore) {
	t.Helper()
	meta, err := store.OpenMeta(filepath.Join(t.TempDir(), "meta.db"))
	if err != nil {
		t.Fatalf("open meta: %v", err)
	}
	t.Cleanup(func() { meta.Close() })
	m := New(nil, nil)
	m.logf = t.Logf
	return m.WithPeerNames(meta.PeerNames(), meta.ConversationNames()), meta
}

func cand(sid, registryName string, previousRefs ...string) nameCandidate {
	return nameCandidate{sid: sid, ref: ipeers.RefID(sid), registryName: registryName, previousRefs: previousRefs}
}

func vname(t *testing.T, base, sid string) string {
	t.Helper()
	n, ok := ipeers.VirtualName(base, ipeers.RefID(sid))
	if !ok {
		t.Fatalf("VirtualName(%q, %s) failed", base, sid)
	}
	return n
}

func storedRow(t *testing.T, meta *store.MetaStore, sid string) store.PeerNameEntry {
	t.Helper()
	rows, err := meta.PeerNames().Lookup(context.Background(), []string{sid})
	if err != nil {
		t.Fatalf("lookup: %v", err)
	}
	return rows[sid]
}

const (
	vnLead  = "aaaaaaaa-1111-4111-8111-000000000001"
	vnOld   = "aaaaaaaa-1111-4111-8111-000000000002"
	vnNew   = "aaaaaaaa-1111-4111-8111-000000000003"
	vnOther = "aaaaaaaa-1111-4111-8111-000000000004"
)

// Assigned once: a later pass that sees another registry name — Claude Code
// renames on every start, and /rename — keeps the first name.
func TestNamer_AssignsOnceAndKeepsIt(t *testing.T) {
	m, meta := namerFixture(t)
	ctx := context.Background()
	want := vname(t, "purdex-54", vnNew)
	if got := m.resolveNames(ctx, []nameCandidate{cand(vnNew, "purdex-54")}); got[vnNew] != want {
		t.Fatalf("first pass = %q, want %q", got[vnNew], want)
	}
	if got := m.resolveNames(ctx, []nameCandidate{cand(vnNew, "addr-58")}); got[vnNew] != want {
		t.Fatalf("pass after a rename = %q, want the first name %q", got[vnNew], want)
	}
	if row := storedRow(t, meta, vnNew); row.Source != store.PeerNameSourceRegistry {
		t.Fatalf("source = %q, want registry", row.Source)
	}
}

// A relay successor inherits the nearest named predecessor's name, walking
// its lineage newest first; the suffix is not recomputed.
func TestNamer_LineageInheritsPredecessorName(t *testing.T) {
	m, meta := namerFixture(t)
	ctx := context.Background()
	m.resolveNames(ctx, []nameCandidate{cand(vnLead, "lead"), cand(vnOld, "older")})
	leadName, oldName := vname(t, "lead", vnLead), vname(t, "older", vnOld)

	// Newest first: the unnamed ref is skipped, the lead is the first hit,
	// the older predecessor behind it is never reached.
	got := m.resolveNames(ctx, []nameCandidate{cand(vnNew, "fresh", "_zzzzzz", ipeers.RefID(vnLead), ipeers.RefID(vnOld))})
	if got[vnNew] != leadName {
		t.Fatalf("successor name = %q, want the predecessor's %q (older one is %q)", got[vnNew], leadName, oldName)
	}
	if row := storedRow(t, meta, vnNew); row != (store.PeerNameEntry{Name: leadName, Source: store.PeerNameSourceLineage}) {
		t.Fatalf("stored = %+v, want %q from lineage", row, leadName)
	}
}

// Lineage written after the successor was first seen: the fallback name is
// upgraded once, and from then on the lineage name never moves.
func TestNamer_LateLineageUpgradesOnceThenSticks(t *testing.T) {
	m, meta := namerFixture(t)
	ctx := context.Background()
	m.resolveNames(ctx, []nameCandidate{cand(vnLead, "lead"), cand(vnOther, "other")})
	leadName := vname(t, "lead", vnLead)

	fallback := m.resolveNames(ctx, []nameCandidate{cand(vnNew, "fresh")})[vnNew]
	if fallback != vname(t, "fresh", vnNew) {
		t.Fatalf("pass before lineage = %q", fallback)
	}
	if got := m.resolveNames(ctx, []nameCandidate{cand(vnNew, "fresh", ipeers.RefID(vnLead))}); got[vnNew] != leadName {
		t.Fatalf("pass with lineage = %q, want the upgrade to %q", got[vnNew], leadName)
	}
	// Another lineage answer later (another predecessor's ref) and a pass
	// with none: neither moves a lineage name.
	if got := m.resolveNames(ctx, []nameCandidate{cand(vnNew, "fresh", ipeers.RefID(vnOther))}); got[vnNew] != leadName {
		t.Fatalf("lineage name moved to %q", got[vnNew])
	}
	if got := m.resolveNames(ctx, []nameCandidate{cand(vnNew, "renamed")}); got[vnNew] != leadName {
		t.Fatalf("lineage name moved to %q", got[vnNew])
	}
	if row := storedRow(t, meta, vnNew); row.Source != store.PeerNameSourceLineage {
		t.Fatalf("source = %q, want lineage", row.Source)
	}
}

// The race the plan pins (C5): two passes over the same new session, one that
// sees its lineage and one that does not, run at once. Whichever inserts
// first, both end on the lineage name — the lineage pass either inserts it or
// upgrades the other's fallback row, and nothing ever overwrites a lineage row.
func TestNamer_ConcurrentPassesEndOnLineageName(t *testing.T) {
	m, meta := namerFixture(t)
	ctx := context.Background()
	m.resolveNames(ctx, []nameCandidate{cand(vnLead, "lead")})
	leadName := vname(t, "lead", vnLead)
	for i := 0; i < 30; i++ {
		sid := fmt.Sprintf("bbbbbbbb-2222-4222-8222-%012d", i)
		var wg sync.WaitGroup
		start := make(chan struct{})
		var withLineage, without map[string]string
		wg.Add(2)
		go func() {
			defer wg.Done()
			<-start
			withLineage = m.resolveNames(ctx, []nameCandidate{cand(sid, "fresh", ipeers.RefID(vnLead))})
		}()
		go func() {
			defer wg.Done()
			<-start
			without = m.resolveNames(ctx, []nameCandidate{cand(sid, "fresh")})
		}()
		close(start)
		wg.Wait()
		if withLineage[sid] != leadName {
			t.Fatalf("%s: the lineage pass answered %q, want %q", sid, withLineage[sid], leadName)
		}
		if got := without[sid]; got != leadName && got != vname(t, "fresh", sid) {
			t.Fatalf("%s: the plain pass answered %q", sid, got)
		}
		if row := storedRow(t, meta, sid); row != (store.PeerNameEntry{Name: leadName, Source: store.PeerNameSourceLineage}) {
			t.Fatalf("%s: stored %+v, want the lineage name", sid, row)
		}
		if got := m.resolveNames(ctx, []nameCandidate{cand(sid, "fresh")}); got[sid] != leadName {
			t.Fatalf("%s: next pass = %q, want %q", sid, got[sid], leadName)
		}
	}
}

// A manual /clear is a new conversation: a new session id with no lineage is
// named afresh from its own registry name and ref.
func TestNamer_ManualClearGetsNewName(t *testing.T) {
	m, _ := namerFixture(t)
	ctx := context.Background()
	if ipeers.RefID(vnOld)[1:3] == ipeers.RefID(vnNew)[1:3] {
		t.Fatal("fixture: the two refs share a suffix; pick other session ids")
	}
	first := m.resolveNames(ctx, []nameCandidate{cand(vnOld, "purdex-54")})[vnOld]
	second := m.resolveNames(ctx, []nameCandidate{cand(vnNew, "purdex-54")})[vnNew]
	if second == first || second != vname(t, "purdex-54", vnNew) {
		t.Fatalf("after /clear = %q (before %q), want %q", second, first, vname(t, "purdex-54", vnNew))
	}
}

// Base order (spec §3.2): the live registry name, else the recorded
// conversation name, else (executions) the cwd basename; nothing ⇒ no name
// and no row.
func TestNamer_BaseSourceOrder(t *testing.T) {
	m, meta := namerFixture(t)
	ctx := context.Background()
	const conv, dir, none = vnOld, vnNew, vnOther
	if err := meta.ConversationNames().Upsert(ctx, conv, "remembered", 1); err != nil {
		t.Fatal(err)
	}
	got := m.resolveNames(ctx, []nameCandidate{
		cand(vnLead, "live-name"),
		cand(conv, "q34psn"), // a ref-shaped registry name is not routable
		{sid: dir, ref: ipeers.RefID(dir), dirBase: "My Project"},
		cand(none, "Bad Name"),
	})
	want := map[string]string{
		vnLead: vname(t, "live-name", vnLead),
		conv:   vname(t, "remembered", conv),
		dir:    vname(t, "my-project", dir),
	}
	if len(got) != len(want) {
		t.Fatalf("names = %v, want %v", got, want)
	}
	for sid, w := range want {
		if got[sid] != w {
			t.Errorf("%s = %q, want %q", sid, got[sid], w)
		}
	}
	sources := map[string]string{vnLead: store.PeerNameSourceRegistry, conv: store.PeerNameSourceConversationName, dir: store.PeerNameSourceDir}
	for sid, src := range sources {
		if row := storedRow(t, meta, sid); row.Source != src {
			t.Errorf("%s source = %q, want %q", sid, row.Source, src)
		}
	}
	if row := storedRow(t, meta, none); row != (store.PeerNameEntry{}) {
		t.Errorf("no base, but a row was stored: %+v", row)
	}
}

// failingNames wraps the real store and fails the chosen calls.
type failingNames struct {
	PeerNameStore
	lookup, assign, byRefs error
}

func (f failingNames) Lookup(ctx context.Context, sids []string) (map[string]store.PeerNameEntry, error) {
	if f.lookup != nil {
		return nil, f.lookup
	}
	return f.PeerNameStore.Lookup(ctx, sids)
}

func (f failingNames) Assign(ctx context.Context, sid, ref, name, source string, nowMs int64) (store.PeerNameEntry, error) {
	if f.assign != nil {
		return store.PeerNameEntry{}, f.assign
	}
	return f.PeerNameStore.Assign(ctx, sid, ref, name, source, nowMs)
}

func (f failingNames) ByRefs(ctx context.Context, refs []string) (map[string]string, error) {
	if f.byRefs != nil {
		return nil, f.byRefs
	}
	return f.PeerNameStore.ByRefs(ctx, refs)
}

// A store error never invents a name: the row goes without one this pass
// (its address takes the ref form) and nothing unpersisted is handed out.
func TestNamer_StoreErrorGivesNoName(t *testing.T) {
	_, meta := namerFixture(t)
	ctx := context.Background()
	boom := errors.New("disk on fire")
	for _, f := range []failingNames{
		{PeerNameStore: meta.PeerNames(), lookup: boom},
		{PeerNameStore: meta.PeerNames(), assign: boom},
		{PeerNameStore: meta.PeerNames(), byRefs: boom},
	} {
		m := New(nil, nil)
		m.logf = t.Logf
		m.WithPeerNames(f, meta.ConversationNames())
		got := m.resolveNames(ctx, []nameCandidate{cand(vnNew, "purdex-54", ipeers.RefID(vnLead))})
		if len(got) != 0 {
			t.Errorf("%+v: names = %v, want none", f, got)
		}
	}
	if row := storedRow(t, meta, vnNew); row != (store.PeerNameEntry{}) {
		t.Fatalf("stored %+v after failing passes", row)
	}
	// No store at all: no names, no panic.
	if got := New(nil, nil).resolveNames(ctx, []nameCandidate{cand(vnNew, "purdex-54")}); len(got) != 0 {
		t.Fatalf("no store: names = %v", got)
	}
}

// failingConv is a ConversationNameReader whose read fails.
type failingConv struct{ err error }

func (f failingConv) All(context.Context) (map[string]string, error) { return nil, f.err }

// An unreadable conversation_names table must not let a conversation fall
// through to a lower source: the name is assigned once and for good, so a
// cwd-basename name given while the recorded name could not be read would
// never be corrected. Such a conversation goes unnamed this pass and is
// named from its conversation name on the next; one with a usable registry
// name is named as usual meanwhile.
func TestNamer_ConversationNameReadErrorDefersTheName(t *testing.T) {
	m, meta := namerFixture(t)
	ctx := context.Background()
	if err := meta.ConversationNames().Upsert(ctx, vnNew, "remembered", 1); err != nil {
		t.Fatal(err)
	}
	needsConv := nameCandidate{sid: vnNew, ref: ipeers.RefID(vnNew), registryName: "Bad Name", dirBase: "proj"}
	m.WithPeerNames(meta.PeerNames(), failingConv{errors.New("conv table locked")})
	got := m.resolveNames(ctx, []nameCandidate{needsConv, cand(vnLead, "live-name")})
	if _, named := got[vnNew]; named || got[vnLead] != vname(t, "live-name", vnLead) {
		t.Fatalf("names = %v, want only %s named", got, vnLead)
	}
	if row := storedRow(t, meta, vnNew); row != (store.PeerNameEntry{}) {
		t.Fatalf("stored %+v while the conversation name was unreadable", row)
	}
	m.WithPeerNames(meta.PeerNames(), meta.ConversationNames())
	if got := m.resolveNames(ctx, []nameCandidate{needsConv}); got[vnNew] != vname(t, "remembered", vnNew) {
		t.Fatalf("next pass = %v, want the conversation name", got)
	}
	if row := storedRow(t, meta, vnNew); row.Source != store.PeerNameSourceConversationName {
		t.Fatalf("source = %q, want conversation_name", row.Source)
	}
}

// Two live entries for one session (a resume pair) are one conversation:
// one name, keyed by the session id as given.
func TestNamer_DuplicateCandidatesShareOneName(t *testing.T) {
	m, _ := namerFixture(t)
	got := m.resolveNames(context.Background(), []nameCandidate{cand(vnNew, "zeta"), cand(vnNew, "alpha")})
	if want := vname(t, "alpha", vnNew); len(got) != 1 || got[vnNew] != want {
		t.Fatalf("names = %v, want {%s: %s}", got, vnNew, want)
	}
}
