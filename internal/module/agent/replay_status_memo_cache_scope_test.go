package agent

import (
	"sync"
	"testing"

	agentpkg "github.com/wake/purdex/internal/agent"
)

// -----------------------------------------------------------------------------
// T3: the cache lives for one replay round only (and A4 rename window)
// -----------------------------------------------------------------------------

// Two consecutive rounds with a pane->session change in between: the second
// round must reflect the new mapping (nothing is carried across rounds).
func TestReplayStatus_CacheDoesNotSurviveAcrossRounds(t *testing.T) {
	fx := newReplayFixture(t, 1, 1)
	installQuietDetector(t, fx.m)

	fx.m.probeIntentDisp.replayStatus()
	if _, ok := readActiveIntent(fx.m, "s0", agentpkg.ProbeIntentKindProcessDead); !ok {
		t.Fatalf("round 1: s0 not armed")
	}

	// The pane now belongs to session "s9" (and s9 has a status of its own).
	fx.fake.SetPaneSessionName("%00", "s9")
	fx.setStatus("s9", agentpkg.StatusRunning)

	fx.m.probeIntentDisp.replayStatus()

	cur, ok := readActiveIntent(fx.m, "s9", agentpkg.ProbeIntentKindProcessDead)
	if !ok || cur.paneID != "%00" {
		t.Fatalf("round 2: s9 should be armed on %%00 from the NEW mapping, got %+v ok=%v", cur, ok)
	}
	if _, ok := readActiveIntent(fx.m, "s0", agentpkg.ProbeIntentKindProcessDead); ok {
		t.Fatalf("round 2: s0 must no longer be armed (its pane moved to s9)")
	}
}

// The un-cached projectionForSession is live, even right after a replay round
// and while one is running in another goroutine (run with -race).
func TestProjectionForSession_StaysLiveWhileReplayRuns(t *testing.T) {
	fx := newReplayFixture(t, 3, 2)
	installQuietDetector(t, fx.m)

	stop := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-stop:
				return
			default:
				_, _ = fx.m.projectionForSession("s1")
			}
		}
	}()
	for i := 0; i < 5; i++ {
		fx.m.probeIntentDisp.replayStatus()
	}
	close(stop)
	wg.Wait()

	fx.fake.SetPaneSessionName("%10", "moved")
	fx.fake.SetPaneSessionName("%11", "moved")
	proj, err := fx.m.projectionForSession("moved")
	if err != nil || proj == nil {
		t.Fatalf("live projection after rename: proj=%v err=%v", proj, err)
	}
	if old, _ := fx.m.projectionForSession("s1"); old != nil {
		t.Fatalf("live projection still resolves the old name: %+v", old)
	}
}

// A4: an EXTERNAL tmux rename lands after the cache loaded but before apply.
// The round then uses the cached (old) name — the documented window — and the
// rename's own re-evaluation (RenameSession -> captureProbeIntentReevalLocked,
// which uses the live projection) corrects it.
func TestReplayStatus_ExternalRenameAfterCacheLoad_WindowThenCorrected(t *testing.T) {
	fx := newReplayFixture(t, 1, 1)
	installQuietDetector(t, fx.m)

	rc := &replayProjectionCache{}
	snap := fx.m.snapshotStatuses(rc) // barrier 1: names cached here

	fx.fake.SetPaneSessionName("%00", "r0") // barrier 2: external rename

	for session, e := range snap {
		fx.m.probeIntentDisp.applyStatusWith(session, e.agentType, e.status, rc)
	}
	// Known window: the cached name "s0" still arms.
	if cur, ok := readActiveIntent(fx.m, "s0", agentpkg.ProbeIntentKindProcessDead); !ok || cur.paneID != "%00" {
		t.Fatalf("window not reproduced: s0 entry=%+v ok=%v (cached old name should arm)", cur, ok)
	}

	// The follow-up pdx rename hook corrects it using live state.
	fx.m.RenameSession("s0", "r0")

	if _, ok := readActiveIntent(fx.m, "s0", agentpkg.ProbeIntentKindProcessDead); ok {
		t.Fatalf("after rename: old name still armed")
	}
	cur, ok := readActiveIntent(fx.m, "r0", agentpkg.ProbeIntentKindProcessDead)
	if !ok || cur.paneID != "%00" {
		t.Fatalf("after rename: r0 should be armed on %%00, got %+v ok=%v", cur, ok)
	}
}
