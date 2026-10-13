package resourcesmod

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/wake/purdex/internal/resources"
)

// One pass with 20 queued positions and 20 wait-mode rows, nothing granted (the
// host is in danger): how long stateMu is held per pass (plan §1, alpha.610).
// The figure is logged for the PR body; the bound only catches an accident.
func TestPassOnce_PositionsTiming(t *testing.T) {
	f := newPosFix(t)
	f.setZone("danger", "flat")
	for i := 0; i < 20; i++ {
		sid := "sid-" + string(rune('a'+i))
		_, _, _, err := f.m.store.CreateAttached(positionRow{ID: newPositionID(), SessionID: sid, CreatedAt: int64(1000 + i)},
			leaseRow{ID: "q" + string(rune('a'+i)), ClientID: "cq" + string(rune('a'+i)), Weight: 35, SessionID: sid, HolderPID: 7,
				Scope: resources.ScopeProcess, CreatedAt: int64(1000 + i), Mode: resources.CallQueue, Command: "go test", Cwd: "/w"})
		require.NoError(t, err)
		w := baseRow("w"+string(rune('a'+i)), "cw"+string(rune('a'+i)))
		w.CreatedAt, w.LeaseUntil = int64(2000+i), f.nowMS()+3_600_000
		mustCreate(t, f.m.store, w)
	}
	const passes = 200
	began := time.Now()
	for i := 0; i < passes; i++ {
		f.pass()
	}
	per := time.Since(began) / passes
	t.Logf("one pass with 20 positions and 20 rows: %v", per)
	require.Less(t, per, 50*time.Millisecond)
}
