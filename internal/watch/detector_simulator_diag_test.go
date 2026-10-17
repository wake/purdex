package watch

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The Diag is cleared when the memory is back under y_mb, so the receiver (which keeps only changes) is told again the next time.
func TestE1_ASimulatorDiagIsClearedWhenItsMemoryIsBackUnderTheThreshold(t *testing.T) {
	r := newRig(t)
	var calls []string
	r.d.Diag = func(source, problem string) {
		if source == "simulator 200" { // one source per tree, so a second device's recovery cannot clear this one's line
			calls = append(calls, problem)
		}
	}
	r.procs = simTree(0.1, 30000)
	r.run(2 * time.Minute)
	r.procs = simTree(0.1, 100)
	r.run(time.Minute)
	r.procs = simTree(0.1, 30000)
	r.run(2 * time.Minute)
	require.Len(t, calls, 3)
	assert.NotEmpty(t, calls[0])
	assert.Empty(t, calls[1])
	assert.NotEmpty(t, calls[2])
}
