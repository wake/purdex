package peers

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/wake/purdex/internal/config"
	"github.com/wake/purdex/internal/core"
	"github.com/wake/purdex/internal/module/agent"
	"github.com/wake/purdex/internal/module/session"
	ipeers "github.com/wake/purdex/internal/peers"
	"github.com/wake/purdex/internal/personas"
	"github.com/wake/purdex/internal/store"
)

// The name service (name pool v2 plan §1.3, without the reservations): what the team module asks of the names.

var _ ipeers.NameService = (*Module)(nil)

// svcEnv is rulesEnv with a live session A named purdex-54-anna.
func svcEnv(t *testing.T) *poolEnv {
	t.Helper()
	e := rulesEnv(t)
	e.start(poolSidA, tEarly)
	e.assign(poolSidA, "purdex-54-anna", store.PeerNameSourcePool)
	return e
}

func (e *poolEnv) storedName(sid string) string {
	e.t.Helper()
	return storedRow(e.t, e.meta, sid).Name
}

func TestNameService_CodeOf(t *testing.T) {
	e := svcEnv(t)
	ctx := context.Background()
	code, ok := e.m.CodeOf(ctx, poolSidA)
	assert.True(t, ok)
	assert.Equal(t, "anna", code)
	e.assign(poolSidB, "purdex-54-"+ipeers.RefID(poolSidB)[1:3], store.PeerNameSourcePool)
	_, ok = e.m.CodeOf(ctx, poolSidB)
	assert.False(t, ok, "a ref-suffixed name has no code")
	_, ok = e.m.CodeOf(ctx, "00000000-0000-4000-8000-0000000000ff")
	assert.False(t, ok, "an unknown session")
}

func TestNameService_LeadCandidates(t *testing.T) {
	e := svcEnv(t)
	ctx := context.Background()
	require.NoError(t, e.meta.NameHistory().RecordLead(ctx, "mia", 5))
	got, err := e.m.LeadCandidates(ctx, poolSidA)
	require.NoError(t, err)
	// anna is the session's own name (keep, first); mia is a former lead; hiro is a lead:true persona
	assert.Equal(t, []ipeers.LeadCandidate{{Code: "anna", Keep: true}, {Code: "mia"}, {Code: "hiro"}}, got)

	e.start(poolSidB, tLate)
	e.assign(poolSidB, "purdex-55-hiro", store.PeerNameSourcePool)
	got, err = e.m.LeadCandidates(ctx, poolSidA)
	require.NoError(t, err)
	assert.Equal(t, []ipeers.LeadCandidate{{Code: "anna", Keep: true}, {Code: "mia"}}, got, "hiro is held by another live session")
}

func TestNameService_LeadCandidatesOfASessionWithoutAPoolNameHaveNoKeep(t *testing.T) {
	e := svcEnv(t)
	e.start(poolSidB, tLate)
	e.assign(poolSidB, "purdex-55-"+ipeers.RefID(poolSidB)[1:3], store.PeerNameSourcePool)
	got, err := e.m.LeadCandidates(context.Background(), poolSidB)
	require.NoError(t, err)
	require.NotEmpty(t, got)
	for _, c := range got {
		assert.False(t, c.Keep)
	}
	// no history: rule 1 offers the lead:true personas before the other free names (anna is held by A)
	assert.Equal(t, []ipeers.LeadCandidate{{Code: "hiro"}, {Code: "mia"}}, got)
}

func TestNameService_RenameForLeadKeepsTheOldNameAsAnAlias(t *testing.T) {
	e := svcEnv(t)
	ctx := context.Background()
	old, err := e.m.RenameForLead(ctx, poolSidA, "mia")
	require.NoError(t, err)
	assert.Equal(t, "purdex-54-anna", old)
	assert.Equal(t, "purdex-54-mia", e.storedName(poolSidA))
	al, err := e.meta.PeerNames().Aliases(ctx)
	require.NoError(t, err)
	require.Len(t, al, 1)
	assert.Equal(t, "purdex-54-anna", al[0].Alias)
}

func TestNameService_RenameForLeadOfARefSuffixedSession(t *testing.T) {
	e := svcEnv(t)
	e.start(poolSidB, tLate)
	e.assign(poolSidB, "purdex-55-"+ipeers.RefID(poolSidB)[1:3], store.PeerNameSourcePool)
	old, err := e.m.RenameForLead(context.Background(), poolSidB, "mia")
	require.NoError(t, err)
	assert.Equal(t, "purdex-55-"+ipeers.RefID(poolSidB)[1:3], old)
	assert.Equal(t, "purdex-55-mia", e.storedName(poolSidB), "the ref suffix is replaced, the base kept")
}

func TestNameService_RenameForLeadErrors(t *testing.T) {
	e := svcEnv(t)
	ctx := context.Background()
	e.start(poolSidB, tLate)
	e.assign(poolSidB, "purdex-55-mia", store.PeerNameSourcePool)

	_, err := e.m.RenameForLead(ctx, poolSidA, "mia")
	assert.ErrorIs(t, err, ipeers.ErrNameTaken, "held by another live session")
	_, err = e.m.RenameForLead(ctx, poolSidA, "zed")
	assert.ErrorIs(t, err, ipeers.ErrNameUnknown, "not a persona code")
	_, err = e.m.RenameForLead(ctx, "00000000-0000-4000-8000-0000000000ff", "hiro")
	assert.ErrorIs(t, err, ipeers.ErrNameLost, "no stored name")
	assert.Equal(t, "purdex-54-anna", e.storedName(poolSidA), "nothing moved")
	al, _ := e.meta.PeerNames().Aliases(ctx)
	assert.Empty(t, al)
}

func TestNameService_RenameForLeadToItsOwnCodeChangesNothing(t *testing.T) {
	e := svcEnv(t)
	ctx := context.Background()
	old, err := e.m.RenameForLead(ctx, poolSidA, "anna")
	require.NoError(t, err)
	assert.Equal(t, "purdex-54-anna", old)
	al, _ := e.meta.PeerNames().Aliases(ctx)
	assert.Empty(t, al, "no alias for a rename that did not happen")
	require.NoError(t, e.m.UndoRenameForLead(ctx, poolSidA, old), "and nothing to undo")
	assert.Equal(t, "purdex-54-anna", e.storedName(poolSidA))
}

func TestNameService_UndoRenameForLead(t *testing.T) {
	e := svcEnv(t)
	ctx := context.Background()
	old, err := e.m.RenameForLead(ctx, poolSidA, "mia")
	require.NoError(t, err)
	require.NoError(t, e.m.UndoRenameForLead(ctx, poolSidA, old))
	assert.Equal(t, "purdex-54-anna", e.storedName(poolSidA))
	al, _ := e.meta.PeerNames().Aliases(ctx)
	assert.Empty(t, al, "the alias of that rename went with it")
	// a second undo has nothing to do
	require.NoError(t, e.m.UndoRenameForLead(ctx, poolSidA, old))
}

func TestNameService_UndoRenameForLeadIsRefusedWhenTheOldNameIsTakenAndSkippedWhenRenamedAgain(t *testing.T) {
	e := svcEnv(t)
	ctx := context.Background()
	old, err := e.m.RenameForLead(ctx, poolSidA, "mia")
	require.NoError(t, err)
	e.start(poolSidB, tLate)
	e.assign(poolSidB, "purdex-54-anna", store.PeerNameSourcePool) // B took the very name A left
	require.ErrorIs(t, e.m.UndoRenameForLead(ctx, poolSidA, old), ipeers.ErrNameTaken)
	assert.Equal(t, "purdex-54-mia", e.storedName(poolSidA), "refused: nothing changes")

	// a later rename of A (by anyone) wins over the undo
	_, ok, err := e.meta.PeerNames().RenamePool(ctx, poolSidA, "purdex-54-mia", "purdex-54-hiro")
	require.NoError(t, err)
	require.True(t, ok)
	require.NoError(t, e.m.UndoRenameForLead(ctx, poolSidA, old))
	assert.Equal(t, "purdex-54-hiro", e.storedName(poolSidA))
}

func TestNameService_UndoWithoutARecordOfTheRenameDoesNothing(t *testing.T) {
	e := svcEnv(t)
	ctx := context.Background()
	_, err := e.m.RenameForLead(ctx, poolSidA, "mia")
	require.NoError(t, err)
	e.m.leadRenames.drop(nameSID(poolSidA), "purdex-54-anna") // a daemon restart forgets the renames in flight
	require.NoError(t, e.m.UndoRenameForLead(ctx, poolSidA, "purdex-54-anna"))
	assert.Equal(t, "purdex-54-mia", e.storedName(poolSidA), "harmless: on a retry, keeping the current name is the new name")
}

func TestNameService_RenameForLeadThatLosesTheCompareAndSetIsLost(t *testing.T) {
	e := svcEnv(t)
	e.m.peerNames = renameStore{PeerNameStore: e.m.peerNames, answer: func(string) (store.PeerNameEntry, bool, error) {
		return store.PeerNameEntry{Name: "purdex-54-hiro"}, false, nil // another rename got there first
	}}
	_, err := e.m.RenameForLead(context.Background(), poolSidA, "mia")
	assert.ErrorIs(t, err, ipeers.ErrNameLost)
	_, recorded := e.m.leadRenames.get(nameSID(poolSidA), "purdex-54-anna")
	assert.False(t, recorded, "a rename that did not happen leaves nothing to undo")
}

// A collection that retires the code between the checks and the write is seen: the rename checks pickable under the same
// generation lock as the write (codex R1 on NP-3c-2).
func TestNameService_RenameForLeadChecksPickableAtTheMomentOfTheWrite(t *testing.T) {
	e := svcEnv(t)
	f := &fakePersonas{set: personas.Set{Items: []personas.Persona{leadP("hiro"), pp("anna", "Anna", false), pp("mia", "Mia", false)}}}
	e.m.bindPersonas(f)
	e.m.beforeLeadRename = func() { f.change(leadP("hiro"), pp("anna", "Anna", false), pp("mia", "Mia", true)) } // mia retired
	_, err := e.m.RenameForLead(context.Background(), poolSidA, "mia")
	assert.ErrorIs(t, err, ipeers.ErrNameUnknown)
	assert.Equal(t, "purdex-54-anna", e.storedName(poolSidA), "nothing was written")
}

// While the rename holds the generation read lock, a collection change waits: it cannot retire the code between the check and
// the write.
func TestNameService_AnApplyingCollectionWaitsForARenameInFlight(t *testing.T) {
	e := svcEnv(t)
	f := &fakePersonas{set: personas.Set{Items: []personas.Persona{leadP("hiro"), pp("anna", "Anna", false), pp("mia", "Mia", false)}}}
	e.m.bindPersonas(f)
	applied := make(chan struct{})
	e.m.inLeadRename = func() {
		go func() {
			f.change(leadP("hiro"), pp("anna", "Anna", false), pp("mia", "Mia", true)) // mia retired
			close(applied)
		}()
		select {
		case <-applied:
			t.Error("the collection was applied while a rename held the generation lock")
		case <-time.After(150 * time.Millisecond):
		}
	}
	old, err := e.m.RenameForLead(context.Background(), poolSidA, "mia")
	require.NoError(t, err)
	assert.Equal(t, "purdex-54-anna", old)
	assert.Equal(t, "purdex-54-mia", e.storedName(poolSidA), "mia was pickable when the rename was checked and written")
	<-applied // and the change goes through once the rename is done
}

// Two renames of one session by different approvals: each undo reverses its own rename only (codex R2 on NP-3c-2).
func TestNameService_AnUndoReversesOnlyTheRenameItBelongsTo(t *testing.T) {
	e := svcEnv(t)
	ctx := context.Background()
	first, err := e.m.RenameForLead(ctx, poolSidA, "mia") // anna -> mia
	require.NoError(t, err)
	second, err := e.m.RenameForLead(ctx, poolSidA, "hiro") // mia -> hiro
	require.NoError(t, err)
	assert.Equal(t, "purdex-54-mia", second)

	// the first approval fails to close: its undo finds the name moved on and leaves the second rename alone
	require.NoError(t, e.m.UndoRenameForLead(ctx, poolSidA, first))
	assert.Equal(t, "purdex-54-hiro", e.storedName(poolSidA))
	// the second approval's own undo still works
	require.NoError(t, e.m.UndoRenameForLead(ctx, poolSidA, second))
	assert.Equal(t, "purdex-54-mia", e.storedName(poolSidA))
}

// A rename that waited for the pool lock while an identical one went through is a success, not a held name.
func TestNameService_ARenameToTheSameCodeThatJustWentThroughIsASuccess(t *testing.T) {
	e := svcEnv(t)
	ctx := context.Background()
	unlock, err := e.m.lockPool(ctx)
	require.NoError(t, err)
	done := make(chan error, 1)
	go func() {
		_, err := e.m.RenameForLead(ctx, poolSidA, "mia")
		done <- err
	}()
	time.Sleep(100 * time.Millisecond) // it is waiting for the lock
	_, won, err := e.meta.PeerNames().RenamePool(ctx, poolSidA, "purdex-54-anna", "purdex-54-mia")
	require.NoError(t, err)
	require.True(t, won)
	unlock()
	require.NoError(t, <-done)
	assert.Equal(t, "purdex-54-mia", e.storedName(poolSidA))
}

// Only persona codes are written to the history (spec §6.1): a built-in name is not one once there are personas, a retired
// persona's code still is.
func TestNameService_TheHistoryTakesPersonaCodesOnly(t *testing.T) {
	e := svcEnv(t) // personas hiro, anna, mia; A is named purdex-54-anna
	ctx := context.Background()
	f := &fakePersonas{set: personas.Set{Items: []personas.Persona{leadP("hiro"), pp("anna", "Anna", true), pp("mia", "Mia", false)}}}
	e.m.bindPersonas(f) // anna is retired now

	require.NoError(t, e.m.RecordPairCodes(ctx, "hiro", "adam")) // adam is a built-in name, not a persona
	require.NoError(t, e.m.RecordPairCodes(ctx, "adam", "mia"))
	pairs, _ := e.meta.NameHistory().Pairs(ctx)
	assert.Empty(t, pairs)
	require.NoError(t, e.m.RecordPairCodes(ctx, "hiro", "anna"))
	pairs, _ = e.meta.NameHistory().Pairs(ctx)
	assert.Len(t, pairs, 1, "a retired persona is still a persona")

	e.start(poolSidB, tLate)
	e.assign(poolSidB, "purdex-55-adam", store.PeerNameSourcePool)
	_, ok := e.m.CodeOf(ctx, poolSidB)
	assert.False(t, ok, "a session still named after a built-in name has no persona code")
	require.NoError(t, e.m.RecordLead(ctx, poolSidB))
	leads, _ := e.meta.NameHistory().Leads(ctx)
	assert.Empty(t, leads)
}

func TestNameService_RecordLeadWritesOnlyForAPersonaName(t *testing.T) {
	e := svcEnv(t)
	ctx := context.Background()
	require.NoError(t, e.m.RecordLead(ctx, poolSidA))
	leads, err := e.meta.NameHistory().Leads(ctx)
	require.NoError(t, err)
	require.Len(t, leads, 1)
	assert.Equal(t, "anna", leads[0].Code)

	e.start(poolSidB, tLate)
	e.assign(poolSidB, "purdex-55-"+ipeers.RefID(poolSidB)[1:3], store.PeerNameSourcePool)
	require.NoError(t, e.m.RecordLead(ctx, poolSidB))
	leads, _ = e.meta.NameHistory().Leads(ctx)
	assert.Len(t, leads, 1, "a ref-suffixed name is not a persona code")
}

func TestNameService_RecordPairCodesNeedsBothToBePersonaCodes(t *testing.T) {
	e := svcEnv(t)
	ctx := context.Background()
	require.NoError(t, e.m.RecordPairCodes(ctx, "hiro", "anna"))
	require.NoError(t, e.m.RecordPairCodes(ctx, "hiro", "zed"))
	require.NoError(t, e.m.RecordPairCodes(ctx, "", "anna"))
	pairs, err := e.meta.NameHistory().Pairs(ctx)
	require.NoError(t, err)
	require.Len(t, pairs, 1)
	assert.Equal(t, store.NamePair{LeadCode: "hiro", MemberCode: "anna", Count: 1, LastAt: pairs[0].LastAt}, pairs[0])
}

func TestNameService_IsRegisteredInTheCore(t *testing.T) {
	cfg := &config.Config{DataDir: t.TempDir()}
	c := core.New(core.CoreDeps{Config: cfg, Registry: core.NewServiceRegistry()})
	c.Registry.Register(session.RegistryKey, &fakeSessions{})
	c.Registry.Register(agent.OwnerResolverKey, &fakeOwners{})
	m := New(nil, nil)
	require.NoError(t, m.Init(c))
	v, ok := c.Registry.Get(ipeers.NameServiceKey)
	require.True(t, ok)
	assert.Same(t, m, v)
}
