package peers

import (
	"context"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	ipeers "github.com/wake/purdex/internal/peers"
	"github.com/wake/purdex/internal/store"
)

// The held names are read under the same pool generation as the free names (codex R2 on NP-3c-3): names held by sessions are
// recognised by the pool of one generation, and a pick that read them under the previous one could hand a persona code that a
// live session already ends in to a second session. The probe is the liveness check that the registry read calls for every live
// entry: it tries to take the write side of the generation lock at that moment, which fails only while a reader holds it.

type genProbe struct{ calls, unlocked atomic.Int32 }

func (e *poolEnv) probeGeneration() *genProbe {
	p := &genProbe{}
	orig := e.m.liveness.PidAlive
	e.m.liveness.PidAlive = func(pid int) bool {
		p.calls.Add(1)
		if e.m.poolGen.TryLock() {
			e.m.poolGen.Unlock()
			p.unlocked.Add(1)
		}
		return orig(pid)
	}
	return p
}

func (p *genProbe) check(t *testing.T) {
	t.Helper()
	require.Positive(t, p.calls.Load(), "the held names were not read at all")
	assert.Zero(t, p.unlocked.Load(), "the held names were read outside the generation lock")
}

func TestPoolGeneration_TheHeldNamesAreReadUnderTheGenerationLock(t *testing.T) {
	ctx := context.Background()

	t.Run("backfill", func(t *testing.T) {
		e := newBackfillEnv(t)
		a := e.live(poolSidA, e.refName(poolSidA))
		p := e.probeGeneration()
		e.backfill(a)
		p.check(t)
	})
	t.Run("a new conversation is named", func(t *testing.T) {
		e := rulesEnv(t)
		e.start(poolSidA, tEarly)
		p := e.probeGeneration()
		e.nameOf(poolSidA)
		p.check(t)
	})
	t.Run("a collision is settled", func(t *testing.T) {
		e := rulesEnv(t)
		holder := e.start(poolSidA, tEarly)
		resumed := e.start(poolSidB, tLate)
		e.assign(poolSidA, "purdex-54-anna", store.PeerNameSourcePool)
		e.assign(poolSidB, "purdex-54-anna", store.PeerNameSourcePool)
		p := e.probeGeneration()
		e.settle([]ipeers.Entry{holder, resumed}, nil)
		p.check(t)
	})
	t.Run("the lead name card", func(t *testing.T) {
		e := svcEnv(t)
		p := e.probeGeneration()
		_, err := e.m.LeadCandidates(ctx, poolSidA)
		require.NoError(t, err)
		p.check(t)
	})
	t.Run("a member's name is reserved", func(t *testing.T) {
		e := reserveEnv(t)
		p := e.probeGeneration()
		_, err := e.m.ReserveMember(ctx, poolSidA, "op-1", "tm-1", tNow)
		require.NoError(t, err)
		p.check(t)
	})
	t.Run("an exact name is reserved", func(t *testing.T) {
		e := reserveEnv(t)
		p := e.probeGeneration()
		require.NoError(t, e.m.ReserveExact(ctx, "mia", "op-1", "tm-1", tNow))
		p.check(t)
	})
	t.Run("a rename for a lead", func(t *testing.T) {
		e := svcEnv(t)
		p := e.probeGeneration()
		_, err := e.m.RenameForLead(ctx, poolSidA, "mia")
		require.NoError(t, err)
		p.check(t)
	})
}
