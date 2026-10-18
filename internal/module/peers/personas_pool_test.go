package peers

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"slices"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/wake/purdex/internal/config"
	"github.com/wake/purdex/internal/core"
	"github.com/wake/purdex/internal/journal"
	"github.com/wake/purdex/internal/module/hostconfig"
	ipeers "github.com/wake/purdex/internal/peers"
	"github.com/wake/purdex/internal/personas"
)

// fakePersonas is a personas.Reader that tells its readers when the test says a change happened.
type fakePersonas struct {
	set  personas.Set
	err  error
	subs []func()
}

func (f *fakePersonas) Personas() (personas.Set, error) { return f.set, f.err }
func (f *fakePersonas) OnPersonasChange(fn func()) func() {
	f.subs = append(f.subs, fn)
	idx := len(f.subs) - 1
	return func() { f.subs[idx] = func() {} }
}
func (f *fakePersonas) change(items ...personas.Persona) {
	f.set = personas.Set{Items: items}
	for _, fn := range f.subs {
		fn()
	}
}

func pp(code, name string, retired bool) personas.Persona {
	return personas.Persona{Code: code, Name: name, Retired: retired}
}

func TestPersonaPool_BuildPool(t *testing.T) {
	rec, pick := buildPool(personas.Set{})
	if !slices.Equal(pick, ipeers.NamePool[:]) || len(rec) != len(ipeers.NamePool) || rec["adam"] != "Adam" {
		t.Fatalf("no personas: pickable %d, recognised %d, adam=%q; want the built-in 30", len(pick), len(rec), rec["adam"])
	}
	rec, pick = buildPool(personas.Set{Items: []personas.Persona{pp("hiro", "Hiro T.", false), pp("anna", "Anna", true), pp("mia", "Mia", false)}})
	if !slices.Equal(pick, []string{"hiro", "mia"}) {
		t.Errorf("pickable = %v, want the active personas only", pick)
	}
	for _, c := range []string{"hiro", "anna", "mia", "adam", "kenji"} {
		if _, ok := rec[c]; !ok {
			t.Errorf("%q is not recognised: personas (retired too) and the built-in names all are", c)
		}
	}
	if rec["hiro"] != "Hiro T." || rec["anna"] != "Anna" {
		t.Errorf("display names = %q, %q", rec["hiro"], rec["anna"])
	}
}

// Only the active personas are picked; a retired one is never picked but still recognised.
// Mutation gate: pick retired -> red; drop the retired code from the recognised set -> red.
func TestPersonaPool_TheNamingPicksFromTheActivePersonas(t *testing.T) {
	t.Cleanup(ipeers.ResetPool)
	e := newPoolEnv(t)
	f := &fakePersonas{set: personas.Set{Items: []personas.Persona{pp("anna", "Anna", true), pp("hiro", "Hiro", false)}}}
	e.m.bindPersonas(f)

	e.start(poolSidA, tEarly)
	got := e.m.resolveNames(context.Background(), []nameCandidate{cand(poolSidA, "purdex-54")})[poolSidA]
	if got != "purdex-54-hiro" {
		t.Fatalf("name = %q, want purdex-54-hiro (anna is retired and first in the list)", got)
	}
	if p, ok := ipeers.PoolNameOf("purdex-88-anna"); !ok || p != "anna" {
		t.Errorf("a retired code is not recognised: %q %v", p, ok)
	}
	// hiro is the only free active persona and is held now: the next conversation takes its ref suffix, not anna
	e.start(poolSidB, tLate)
	got = e.m.resolveNames(context.Background(), []nameCandidate{cand(poolSidB, "purdex-54")})[poolSidB]
	if _, ok := ipeers.PoolNameOf(got); ok {
		t.Errorf("name = %q: a retired persona (or a built-in name) was picked when the active ones were used up", got)
	}
}

func TestPersonaPool_WithoutPersonasTheBuiltInNamesArePicked(t *testing.T) {
	t.Cleanup(ipeers.ResetPool)
	e := newPoolEnv(t)
	e.m.bindPersonas(&fakePersonas{})
	e.start(poolSidA, tEarly)
	got := e.m.resolveNames(context.Background(), []nameCandidate{cand(poolSidA, "purdex-54")})[poolSidA]
	if got != "purdex-54-adam" {
		t.Fatalf("name = %q, want purdex-54-adam", got)
	}
}

// The real hostconfig module and the peers module in one core: a PUT of personas through the route reaches the name pool,
// the picker and the journal's display names (the capability personas.v1 announces exactly this). The readers run off the
// request, so the pool is waited for.
// Mutation gate: do not look the Reader up in Start -> the pool never follows -> red.
func TestPersonaPool_APutThroughTheRealHostconfigReachesThePoolAndTheJournal(t *testing.T) {
	t.Cleanup(ipeers.ResetPool)
	c := core.New(core.CoreDeps{Config: &config.Config{DataDir: t.TempDir(), HostID: "mlab"}, Registry: core.NewServiceRegistry()})
	hc := hostconfig.New()
	require.NoError(t, hc.Init(c))
	t.Cleanup(func() { _ = hc.Close() })
	m := &Module{core: c}
	m.startPersonaPool()
	t.Cleanup(func() {
		if m.personaCancel != nil {
			m.personaCancel()
		}
	})

	mux := http.NewServeMux()
	hc.RegisterRoutes(mux)
	body := `{"items":[{"code":"hiro","name":"Hiro Tanaka"},{"code":"anna","name":"Anna K.","retired":true}],"baseRevision":0}`
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodPut, "/api/hostconfig/personas", strings.NewReader(body)))
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())

	require.Eventually(t, func() bool { return slices.Equal(ipeers.PickablePool(), []string{"hiro"}) }, 2*time.Second, 5*time.Millisecond,
		"the pool did not follow the PUT: %v", ipeers.PickablePool())
	if d, ok := ipeers.PoolDisplayName("anna"); !ok || d != "Anna K." {
		t.Errorf("a retired persona's display name = %q, %v", d, ok)
	}
	if got := journal.DisplayName("mlab/purdex-3f-hiro"); got != "Hiro Tanaka" {
		t.Errorf("the journal shows %q, want the persona's display name", got)
	}
}

// The hostconfig module tells its readers from goroutines of their own, so two changes can be applied out of order: the pool
// must end up as the LAST stored set, whichever callback finishes last (NP-1b R2).
// Mutation gate: drop the mutex of bindPersonas -> the older read overwrites the newer pool -> red.
func TestPersonaPool_ConcurrentChangesEndOnTheLatestSet(t *testing.T) {
	t.Cleanup(ipeers.ResetPool)
	e := newPoolEnv(t)
	older := personas.Set{Items: []personas.Persona{pp("anna", "Anna", false)}}
	newer := personas.Set{Items: []personas.Persona{pp("hiro", "Hiro", false)}}
	r := &blockingPersonas{first: make(chan struct{}), release: make(chan struct{}), sets: []personas.Set{older, newer}}
	e.m.bindPersonas(r) // the binding's own first read, before the reader is armed, is an empty set
	r.arm()             // from now on: the 1st read returns older and waits to be released, the 2nd returns newer
	done := make(chan struct{}, 2)
	go func() { r.fire(); done <- struct{}{} }() // callback A: reads older, then waits
	<-r.first                                    // A is inside Personas()
	go func() { r.fire(); done <- struct{}{} }() // callback B: reads newer, and finishes first when nothing orders them
	time.Sleep(50 * time.Millisecond)            // let B run as far as it can
	close(r.release)                             // now A finishes
	<-done
	<-done
	if got := ipeers.PickablePool(); !slices.Equal(got, []string{"hiro"}) {
		t.Fatalf("the pool is %v after the older callback finished last, want the latest set [hiro]", got)
	}
}

// blockingPersonas is a Reader whose reads are numbered: the 1st armed read returns sets[0] and waits for release, the
// next returns sets[1].
type blockingPersonas struct {
	first   chan struct{}
	release chan struct{}
	sets    []personas.Set
	mu      sync.Mutex
	calls   int
	armed   bool
	fn      func()
}

func (b *blockingPersonas) arm()  { b.mu.Lock(); b.armed, b.calls = true, 0; b.mu.Unlock() }
func (b *blockingPersonas) fire() { b.fn() }
func (b *blockingPersonas) OnPersonasChange(fn func()) func() {
	b.fn = fn
	return func() {}
}
func (b *blockingPersonas) Personas() (personas.Set, error) {
	b.mu.Lock()
	if !b.armed {
		b.mu.Unlock()
		return personas.Set{}, nil
	}
	b.calls++
	n := b.calls
	b.mu.Unlock()
	if n == 1 {
		close(b.first)
		<-b.release
		return b.sets[0], nil
	}
	return b.sets[1], nil
}

// fakeScheduler stands in for time.AfterFunc: the test decides when a retry runs.
type fakeScheduler struct {
	mu      sync.Mutex
	pending map[int]func()
	delays  []time.Duration
	total   int
}

func (s *fakeScheduler) after(d time.Duration, fn func()) (stop func()) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.total++
	id := s.total
	if s.pending == nil {
		s.pending = map[int]func(){}
	}
	s.pending[id] = fn
	s.delays = append(s.delays, d)
	return func() {
		s.mu.Lock()
		delete(s.pending, id)
		s.mu.Unlock()
	}
}

// snapshot is the retries scheduled now, left scheduled.
func (s *fakeScheduler) snapshot() []func() {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]func(), 0, len(s.pending))
	for _, fn := range s.pending {
		out = append(out, fn)
	}
	return out
}

// runPending runs what is scheduled now, oldest first (a retry may schedule the next one), and reports how many ran.
func (s *fakeScheduler) runPending() int {
	s.mu.Lock()
	ids := make([]int, 0, len(s.pending))
	for id := range s.pending {
		ids = append(ids, id)
	}
	sort.Ints(ids)
	fns := make([]func(), 0, len(ids))
	for _, id := range ids {
		fns = append(fns, s.pending[id])
		delete(s.pending, id)
	}
	s.mu.Unlock()
	for _, fn := range fns {
		fn()
	}
	return len(fns)
}

// The change is told once; a read that fails then must not leave the pool on the older set until the next change: it is
// retried, with a delay that grows, until it reads (NP-1b R2).
// Mutation gate: do not schedule a retry -> the pool stays on the older set -> red.
func TestPersonaPool_AFailedReadIsRetriedUntilItSucceeds(t *testing.T) {
	t.Cleanup(ipeers.ResetPool)
	e := newPoolEnv(t)
	sched := &fakeScheduler{}
	e.m.afterFunc = sched.after
	f := &fakePersonas{set: personas.Set{Items: []personas.Persona{pp("anna", "Anna", false)}}}
	e.m.bindPersonas(f)
	require.Equal(t, []string{"anna"}, ipeers.PickablePool())

	f.err = errors.New("database is busy")
	f.change(pp("hiro", "Hiro", false)) // the PUT: the one notification, and the read of it fails
	require.Equal(t, []string{"anna"}, ipeers.PickablePool(), "the failed read keeps the pool as it was")
	require.Equal(t, 1, sched.runPending(), "no retry was scheduled")
	require.Equal(t, []string{"anna"}, ipeers.PickablePool(), "a retry that fails again changes nothing")

	f.err = nil
	require.Equal(t, 1, sched.runPending(), "the failed retry did not schedule the next")
	assert.Equal(t, []string{"hiro"}, ipeers.PickablePool(), "the pool did not converge on the stored set")
	assert.Equal(t, 0, sched.runPending(), "a successful read leaves no retry behind")
	require.GreaterOrEqual(t, len(sched.delays), 2)
	assert.Greater(t, sched.delays[1], sched.delays[0], "the delay does not grow")
}

// A read that never works is retried a bounded number of times, then given up (the next change starts over); a stopped
// binding retries nothing.
// Mutation gate: no bound -> red; a retry that ignores the stop -> red.
func TestPersonaPool_TheRetriesAreBoundedAndStopWithTheBinding(t *testing.T) {
	t.Cleanup(ipeers.ResetPool)
	e := newPoolEnv(t)
	sched := &fakeScheduler{}
	e.m.afterFunc = sched.after
	f := &fakePersonas{err: errors.New("stored personas do not read")}
	e.m.bindPersonas(f) // the first read fails too
	for i := 0; i < 100 && sched.runPending() > 0; i++ {
	}
	assert.Equal(t, personaRetryMax, sched.total, "the retries are not bounded by personaRetryMax")

	sched2 := &fakeScheduler{}
	e.m.afterFunc = sched2.after
	f2 := &fakePersonas{err: errors.New("busy")}
	cancel := e.m.bindPersonas(f2)
	require.Equal(t, 1, sched2.total)
	fired := sched2.snapshot() // a timer that had already fired and waits for the lock when the binding stops
	cancel()
	f2.err = nil
	f2.set = personas.Set{Items: []personas.Persona{pp("mia", "Mia", false)}}
	assert.Equal(t, 0, sched2.runPending(), "cancel left a retry scheduled")
	for _, fn := range fired {
		fn()
	}
	assert.NotEqual(t, []string{"mia"}, ipeers.PickablePool(), "a stopped binding still applied a retry that had already fired")
}

// A change of the collection rebuilds the pool; a read that fails keeps the pool as it was.
// Mutation gate: do not subscribe -> the pool never follows -> red; reset on error -> red.
func TestPersonaPool_FollowsChangesAndSurvivesAFailedRead(t *testing.T) {
	t.Cleanup(ipeers.ResetPool)
	e := newPoolEnv(t)
	f := &fakePersonas{}
	cancel := e.m.bindPersonas(f)
	if got := ipeers.PickablePool(); !slices.Equal(got, ipeers.NamePool[:]) {
		t.Fatalf("start: %v", got)
	}
	f.change(pp("hiro", "Hiro", false))
	if got := ipeers.PickablePool(); !slices.Equal(got, []string{"hiro"}) {
		t.Fatalf("after a change: %v", got)
	}
	f.err = errors.New("stored personas do not read")
	f.change(pp("mia", "Mia", false))
	if got := ipeers.PickablePool(); !slices.Equal(got, []string{"hiro"}) {
		t.Fatalf("a failed read changed the pool: %v", got)
	}
	f.err = nil
	cancel()
	f.change(pp("mia", "Mia", false))
	if got := ipeers.PickablePool(); !slices.Equal(got, []string{"hiro"}) {
		t.Fatalf("a cancelled binding still followed: %v", got)
	}
}
