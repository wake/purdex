package nex

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// #1866 PR1c (spec 2026-10-08 §3.5, §3.6): a new epoch when bseq runs out.

// bseq reaching its limit (2^53−1; 2 here) starts a new epoch inside the
// hold that numbers the delta: the hello first, then the delta as bseq 1
// of the new epoch. ver goes on.
func TestProjector_BseqAtItsLimitRotatesTheEpoch(t *testing.T) {
	e := newProjEnv(t, fastTiming)
	require.NoError(t, e.slot.acquire(context.Background(), 0))
	e.slot.bseqLimit = 2 // as a holder: only holders touch the counters
	old := e.slot.epoch
	e.slot.release()
	e.rows.set("exc_a", "running")

	var last delta
	for i := 1; i <= 2; i++ {
		e.p.markFrame("exc_a", "tool_use")
		last = nextDelta(t, e.sub)
		assert.Equal(t, old, last.Epoch)
		assert.Equal(t, uint64(i), last.Bseq)
	}
	e.p.markFrame("exc_a", "tool_result")
	ev, _ := nextFrame(t, e.sub)
	h := helloOf(t, ev)
	assert.NotEqual(t, old, h.Epoch)
	assert.Equal(t, `{"epoch":"`+h.Epoch+`","bseq":0}`, ev.Value, "bseq 0 must be spelled out")
	d := nextDelta(t, e.sub)
	assert.Equal(t, h.Epoch, d.Epoch)
	assert.Equal(t, uint64(1), d.Bseq)
	assert.Equal(t, []string{"tool_result"}, d.Cause)
	assert.Greater(t, d.Ver, last.Ver, "ver went back with the epoch")
}
