package peers

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"sync"
	"testing"
	"time"

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
	lookup, assign, byRefs, adopt error
}

func (f failingNames) AdoptLineage(ctx context.Context, sid, name string) (store.PeerNameEntry, error) {
	if f.adopt != nil {
		return store.PeerNameEntry{}, f.adopt
	}
	return f.PeerNameStore.AdoptLineage(ctx, sid, name)
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

// A failed lineage upgrade hands out no name that pass: the stored fallback
// is about to be replaced, so showing it would announce an address the
// conversation loses on the next pass. The failures of a pass are logged as
// one line.
func TestNamer_AdoptLineageErrorGivesNoName(t *testing.T) {
	m, meta := namerFixture(t)
	ctx := context.Background()
	m.resolveNames(ctx, []nameCandidate{cand(vnLead, "lead"), cand(vnNew, "fresh"), cand(vnOther, "other")})
	logs := &logSink{}
	m.logf = logs.logf
	m.WithPeerNames(failingNames{PeerNameStore: meta.PeerNames(), adopt: errors.New("busy")}, meta.ConversationNames())
	for pass := 1; pass <= 2; pass++ {
		got := m.resolveNames(ctx, []nameCandidate{
			cand(vnNew, "fresh", ipeers.RefID(vnLead)),
			cand(vnOther, "other", ipeers.RefID(vnLead)),
		})
		if len(got) != 0 {
			t.Fatalf("pass %d: names = %v, want none while the upgrade fails", pass, got)
		}
		if n := len(logs.all()); n != pass {
			t.Fatalf("pass %d: %d log lines %q, want one per pass", pass, n, logs.all())
		}
	}
	if row := storedRow(t, meta, vnNew); row.Source != store.PeerNameSourceRegistry {
		t.Fatalf("stored %+v, want the untouched fallback row", row)
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

// whoami, the origin resolver (lead and team notices are built from its
// Address: team spawn_handler.go, team_handler.go) and GET /api/peers give a
// conversation one address — the same namer over the same store — whichever
// of them sees the conversation first.
func TestVirtualAddress_WhoamiOriginAndEnvelopeAgree(t *testing.T) {
	f := newTitleFixture(t)
	want := map[int]string{
		10: "a/" + vname(t, "n10", "sid-1"),
		20: "a/" + vname(t, "n20", "sid-2"),
	}
	// pid 20 is first seen by whoami, pid 10 by the inventory.
	if res := f.m.whoami(context.Background(), f.inbox(20)); res.err != nil || res.rec.Address != want[20] || res.rec.Name != want[20][2:] {
		t.Fatalf("whoami(20) = %+v / %+v, want %s", res.rec, res.err, want[20])
	}
	snap := f.m.configSnapshot()
	env := f.m.localEnvelope(context.Background(), snap.hostID, snap.alias)
	r := &OriginResolver{m: f.m}
	for pid, sid := range map[int]string{10: "sid-1", 20: "sid-2"} {
		var row *ipeers.PeerRecord
		for i := range env.Peers {
			if a := env.Peers[i].Agent; a != nil && a.SessionID == sid && a.PID == pid {
				row = &env.Peers[i]
			}
		}
		if row == nil || row.Address != want[pid] || row.Agent.PeerName != fmt.Sprintf("n%d", pid) {
			t.Fatalf("envelope row for %s = %+v, want address %s", sid, row, want[pid])
		}
		if res := f.m.whoami(context.Background(), f.inbox(pid)); res.rec.Address != want[pid] {
			t.Errorf("whoami(%d) = %q, want %q", pid, res.rec.Address, want[pid])
		}
		if o, ok, err := r.ResolveOrigin(f.inbox(pid)); !ok || err != nil || o.Address != want[pid] || o.Name != row.Agent.PeerName {
			t.Errorf("ResolveOrigin(%d) = %+v ok=%v err=%v, want address %s", pid, o, ok, err, want[pid])
		}
		if o, ok, err := r.ResolveOriginBySession(sid); !ok || err != nil || o.Address != want[pid] {
			t.Errorf("ResolveOriginBySession(%s) = %+v ok=%v err=%v, want address %s", sid, o, ok, err, want[pid])
		}
	}
}

// A store that cannot be read costs the names, never correctness: every
// row, whoami and the origin resolver fall back to the ref form together.
func TestVirtualAddress_StoreErrorFallsBackToRef(t *testing.T) {
	f := newTitleFixture(t)
	f.m.WithPeerNames(failingNames{PeerNameStore: f.m.peerNames, lookup: errors.New("boom")}, nil)
	want := "a/" + ipeers.RefID("sid-1")
	snap := f.m.configSnapshot()
	for _, row := range f.m.localEnvelope(context.Background(), snap.hostID, snap.alias).Peers {
		if row.Agent != nil && row.Agent.SessionID == "sid-1" && (row.Address != want || row.Name != "") {
			t.Errorf("row name/address = %q/%q, want \"\"/%s", row.Name, row.Address, want)
		}
	}
	if res := f.m.whoami(context.Background(), f.inbox(10)); res.rec.Address != want {
		t.Errorf("whoami = %q, want %q", res.rec.Address, want)
	}
	if o, _, _ := (&OriginResolver{m: f.m}).ResolveOrigin(f.inbox(10)); o.Address != want {
		t.Errorf("origin address = %q, want %q", o.Address, want)
	}
}

// blockingNames is a PeerNameStore whose Lookup waits for its ctx to end — a
// name store that hangs — and signals entered once it is waiting.
type blockingNames struct {
	PeerNameStore
	entered chan struct{}
}

func (b blockingNames) Lookup(ctx context.Context, _ []string) (map[string]store.PeerNameEntry, error) {
	select {
	case b.entered <- struct{}{}:
	default:
	}
	<-ctx.Done()
	return nil, ctx.Err()
}

func newBlockingNames(m *Module) blockingNames {
	return blockingNames{PeerNameStore: m.peerNames, entered: make(chan struct{}, 1)}
}

// within runs f and fails the test if it has not returned after d, so a hung
// name store fails this test rather than the whole package's timeout.
func within(t *testing.T, d time.Duration, what string, f func()) {
	t.Helper()
	done := make(chan struct{})
	go func() {
		defer close(done)
		f()
	}()
	select {
	case <-done:
	case <-time.After(d):
		t.Fatalf("%s did not return within %v", what, d)
	}
}

// The inventory's one budget covers the namer too: a hung name store costs
// that pass its names (every row takes the ref form), never the request.
func TestVirtualAddress_EnvelopeNamerStaysInBudget(t *testing.T) {
	f := newTitleFixture(t)
	f.m.budget = 300 * time.Millisecond
	f.m.WithPeerNames(newBlockingNames(f.m), nil)
	snap := f.m.configSnapshot()
	var env ipeers.Envelope
	within(t, 5*time.Second, "localEnvelope", func() {
		env = f.m.localEnvelope(context.Background(), snap.hostID, snap.alias)
	})
	seen := false
	for _, row := range env.Peers {
		if row.Agent != nil && row.Agent.SessionID == "sid-1" && row.Agent.PID == 10 {
			seen = true
			if want := "a/" + ipeers.RefID("sid-1"); row.Address != want || row.Name != "" {
				t.Errorf("row name/address = %q/%q, want the ref form %s", row.Name, row.Address, want)
			}
		}
	}
	if !seen {
		t.Fatalf("no row for sid-1 in %+v", env)
	}
}

// The self verbs name the caller under the request's ctx, bounded by
// namerTimeout, and never while holding titleMu: a hung name store answers a
// cancelled request at once, answers any request within the bound (in the
// ref form), and never blocks another self verb.
func TestVirtualAddress_SelfVerbsNamerIsBoundedAndOutsideTitleMu(t *testing.T) {
	f := newTitleFixture(t)
	blocking := newBlockingNames(f.m)
	f.m.WithPeerNames(blocking, nil)
	ref20 := "a/" + ipeers.RefID("sid-2")

	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	verbs := map[string]func(context.Context) selfResult{
		"whoami":  func(ctx context.Context) selfResult { return f.m.whoami(ctx, f.inbox(20)) },
		"claim":   func(ctx context.Context) selfResult { return f.m.claim(ctx, f.inbox(20), "purdex-tester") },
		"release": func(ctx context.Context) selfResult { return f.m.release(ctx, f.inbox(20)) },
	}
	for name, verb := range verbs {
		start := time.Now()
		var res selfResult
		within(t, 5*time.Second, name+" (cancelled request)", func() { res = verb(cancelled) })
		if el := time.Since(start); el >= namerTimeout/2 {
			t.Errorf("%s on a cancelled request took %v; the request's ctx must reach the namer", name, el)
		}
		if res.err != nil || res.rec.Address != ref20 {
			t.Errorf("%s = %+v / %+v, want address %s", name, res.rec, res.err, ref20)
		}
	}

	// A request that never ends: bounded by namerTimeout, waiting outside titleMu.
	select {
	case <-blocking.entered:
	default:
	}
	done := make(chan selfResult, 1)
	go func() { done <- f.m.whoami(context.Background(), f.inbox(20)) }()
	select {
	case <-blocking.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("whoami never reached the name store")
	}
	if !f.m.titleMu.TryLock() {
		t.Fatal("titleMu is held while whoami waits on the name store")
	}
	f.m.titleMu.Unlock()
	select {
	case res := <-done:
		if res.rec.Address != ref20 {
			t.Errorf("whoami = %q, want the ref form %s", res.rec.Address, ref20)
		}
	case <-time.After(namerTimeout + 3*time.Second):
		t.Fatalf("whoami not bounded by namerTimeout (%v)", namerTimeout)
	}
}
