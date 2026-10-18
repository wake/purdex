package peers

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

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
