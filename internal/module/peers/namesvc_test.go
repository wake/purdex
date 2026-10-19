package peers

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/wake/purdex/internal/config"
	"github.com/wake/purdex/internal/core"
	"github.com/wake/purdex/internal/module/agent"
	"github.com/wake/purdex/internal/module/session"
	ipeers "github.com/wake/purdex/internal/peers"
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
	e.m.leadRenames.drop(nameSID(poolSidA)) // a daemon restart forgets the renames in flight
	require.NoError(t, e.m.UndoRenameForLead(ctx, poolSidA, "purdex-54-anna"))
	assert.Equal(t, "purdex-54-mia", e.storedName(poolSidA), "harmless: on a retry, keeping the current name is the new name")
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
