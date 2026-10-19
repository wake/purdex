package peers

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	ipeers "github.com/wake/purdex/internal/peers"
	"github.com/wake/purdex/internal/personas"
	"github.com/wake/purdex/internal/store"
)

// The naming passes pick by rule 3 (name pool v2 spec §6.2): a newcomer first, then the name least paired with any lead, the
// lead:true personas last. The history and the lead flags are read under the pool lock.

func leadP(code string) personas.Persona { return personas.Persona{Code: code, Name: code, Lead: true} }

// rulesEnv is a pool env following three personas: hiro (lead:true) first, then anna and mia.
func rulesEnv(t *testing.T) *poolEnv {
	t.Helper()
	t.Cleanup(ipeers.ResetPool)
	e := newPoolEnv(t)
	e.m.WithNameHistory(e.meta.NameHistory())
	e.m.bindPersonas(&fakePersonas{set: personas.Set{Items: []personas.Persona{leadP("hiro"), pp("anna", "Anna", false), pp("mia", "Mia", false)}}})
	return e
}

func (e *poolEnv) nameOf(sid string) string {
	e.t.Helper()
	return e.m.resolveNames(context.Background(), []nameCandidate{cand(sid, "purdex-54")})[sid]
}

func TestPoolRules_LeadPersonasAreTakenLast(t *testing.T) {
	e := rulesEnv(t)
	e.start(poolSidA, tEarly)
	assert.Equal(t, "purdex-54-anna", e.nameOf(poolSidA), "hiro is first in the pool but lead:true")
}

func TestPoolRules_AFamiliarNameIsNotANewcomer(t *testing.T) {
	e := rulesEnv(t)
	require.NoError(t, e.meta.NameHistory().RecordPair(context.Background(), "adam", "anna", 1))
	e.start(poolSidA, tEarly)
	assert.Equal(t, "purdex-54-mia", e.nameOf(poolSidA))
}

type fakeFamiliar map[string]bool

func (f fakeFamiliar) RemoteFamiliar() map[string]bool { return f }

func TestPoolRules_AFormerLeadIsNotANewcomer(t *testing.T) {
	e := rulesEnv(t)
	require.NoError(t, e.meta.NameHistory().RecordLead(context.Background(), "anna", 1))
	e.start(poolSidA, tEarly)
	assert.Equal(t, "purdex-54-mia", e.nameOf(poolSidA))
}

func TestPoolRules_AFamiliarNameOfAnotherHostIsNotANewcomer(t *testing.T) {
	e := rulesEnv(t)
	e.m.WithFamiliarSource(fakeFamiliar{"anna": true}) // anna is familiar on another host only
	e.start(poolSidA, tEarly)
	assert.Equal(t, "purdex-54-mia", e.nameOf(poolSidA), "without the remote set it would be anna, the first newcomer")
}

func TestPoolRules_WithNoNewcomerTheLeastPairedNameIsPicked(t *testing.T) {
	e := rulesEnv(t)
	ctx := context.Background()
	for i := 0; i < 3; i++ { // anna worked with a lead three times here, mia once
		require.NoError(t, e.meta.NameHistory().RecordPair(ctx, "adam", "anna", int64(i+1)))
	}
	require.NoError(t, e.meta.NameHistory().RecordPair(ctx, "adam", "mia", 9))
	e.start(poolSidA, tEarly)
	assert.Equal(t, "purdex-54-mia", e.nameOf(poolSidA), "both are familiar: the one with fewer pairs, not the first")
}

type failingHistory struct{}

func (failingHistory) Leads(context.Context) ([]store.NameLead, error) {
	return nil, errors.New("history down")
}
func (failingHistory) Pairs(context.Context) ([]store.NamePair, error) { return nil, nil }
func (failingHistory) Familiar(context.Context, int) ([]string, error) { return nil, nil }

func TestPoolRules_AnUnreadableHistoryNamesAsIfThereWasNone(t *testing.T) {
	e := rulesEnv(t)
	e.m.WithNameHistory(failingHistory{})
	e.start(poolSidA, tEarly)
	assert.Equal(t, "purdex-54-anna", e.nameOf(poolSidA), "still named, lead:true last")
	assert.True(t, e.logged("history"), "the failure is logged")
}

func TestPoolRules_ASettledCollisionPicksByRule3(t *testing.T) {
	e := rulesEnv(t)
	holder := e.start(poolSidA, tEarly)
	resumed := e.start(poolSidB, tLate)
	e.assign(poolSidA, "purdex-54-anna", store.PeerNameSourcePool)
	e.assign(poolSidB, "purdex-54-anna", store.PeerNameSourcePool)
	names := e.settle([]ipeers.Entry{holder, resumed}, nil)
	assert.Equal(t, "purdex-54-anna", names[poolSidA])
	assert.Equal(t, "purdex-54-mia", names[poolSidB], "free are hiro (lead:true) and mia: the newcomer mia")
}

func TestPoolRules_TheLeadFlagsFollowThePersonas(t *testing.T) {
	t.Cleanup(ipeers.ResetPool)
	m, _ := namerFixture(t)
	f := &fakePersonas{set: personas.Set{Items: []personas.Persona{leadP("hiro"), pp("anna", "Anna", false)}}}
	m.bindPersonas(f)
	assert.Equal(t, map[string]bool{"hiro": true}, m.leadCodes())
	f.change(pp("hiro", "Hiro", false), leadP("anna"))
	assert.Equal(t, map[string]bool{"anna": true}, m.leadCodes(), "the flag moved with the collection")
}
