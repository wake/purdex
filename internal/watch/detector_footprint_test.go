package watch

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ---- #3072: a tree's memory is its physical footprint, not the sum of shared RSS ----

// Mutation gates: footprint substitution dropped → the event fires (red).
func TestE1_FootprintReplacesRSSWhenTheTreeIsOverTheRSSThreshold(t *testing.T) {
	r := newRig(t)
	r.procs = goTest(0.1, 4000) // 4000 MB of RSS, nothing on the cores
	r.fp = map[int]uint64{100: 100 * mib, 101: 100 * mib}
	assert.Empty(t, r.run(3*time.Minute))
	assert.Positive(t, r.fpCalls)
}

// The footprint still over the threshold: the event, and peak_mb is the footprint sum.
func TestE1_PeakMBIsTheFootprintSum(t *testing.T) {
	r := newRig(t)
	r.procs = goTest(0.1, 4000)
	r.fp = map[int]uint64{100: 500 * mib, 101: 1500 * mib}
	evs := r.run(3 * time.Minute)
	require.Len(t, evs, 1)
	assert.EqualValues(t, 2000, evs[0].PeakMB)
}

// A process whose footprint is not ok counts by its RSS.
func TestE1_AProcessWithoutAFootprintCountsByItsRSS(t *testing.T) {
	r := newRig(t)
	r.procs = goTest(0.1, 4000) // the go child holds 2000 MB of RSS
	r.fp = map[int]uint64{100: 1 * mib}
	evs := r.run(3 * time.Minute)
	require.Len(t, evs, 1)
	assert.EqualValues(t, 2000+1, evs[0].PeakMB)
}

// Mutation gates: the guard dropped → the callback is asked for a tree under the threshold (red).
func TestE1_TreesUnderTheRSSThresholdNeverAskForAFootprint(t *testing.T) {
	r := newRig(t)
	r.procs = goTest(0.1, 100)
	r.fp = map[int]uint64{}
	assert.Empty(t, r.run(3*time.Minute))
	assert.Zero(t, r.fpCalls)
}

func TestE1_NoFootprintCallbackIsTheRSSBehaviour(t *testing.T) {
	r := newRig(t)
	r.procs = goTest(0.1, 4000)
	assert.Len(t, r.run(3*time.Minute), 1)
}
