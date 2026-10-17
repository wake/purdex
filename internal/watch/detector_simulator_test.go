package watch

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/wake/purdex/internal/resources"
)

// ---- #3072: the simulator's tree counts by its cores only ----

func simTree(cores float64, rssMB uint64) []resources.Proc {
	return []resources.Proc{
		p(1, 0, "launchd", 0, 10),
		p(200, 1, "launchd_sim", cores*100/2, rssMB/2),
		p(201, 200, "backboardd", cores*100/2, rssMB/2),
	}
}

func simRig(t *testing.T) (*rig, *[]string) {
	r := newRig(t)
	var diags []string
	r.d.Diag = func(source, problem string) {
		if problem != "" {
			diags = append(diags, source+": "+problem)
		}
	}
	return r, &diags
}

// Mutation gates: the rule dropped → the event fires (red).
func TestE1_ASimulatorOverOnlyInMemoryRaisesNoEvent(t *testing.T) {
	r, diags := simRig(t)
	r.procs = simTree(0.1, 30000)
	assert.Empty(t, r.run(10*time.Minute))
	require.Len(t, *diags, 1)
	assert.Contains(t, (*diags)[0], "simulator")
}

func TestE1_ASimulatorOverInCoresRaisesTheEvent(t *testing.T) {
	r, _ := simRig(t)
	r.procs = simTree(3, 100)
	assert.Len(t, r.run(10*time.Minute), 1)
}

func TestE1_ANonSimulatorTreeIsUnchanged(t *testing.T) {
	r, diags := simRig(t)
	r.procs = goTest(0.1, 30000)
	assert.Len(t, r.run(10*time.Minute), 1)
	assert.Empty(t, *diags)
}
