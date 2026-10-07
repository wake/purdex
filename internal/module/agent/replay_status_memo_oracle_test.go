package agent

import (
	"fmt"
	"testing"
	"time"

	agentpkg "github.com/wake/purdex/internal/agent"
	"github.com/wake/purdex/internal/store"
)

// -----------------------------------------------------------------------------
// T2: semantic equivalence against an independent baseline Module (A5)
// -----------------------------------------------------------------------------

func (fx *replayFixture) addFrame(t *testing.T, session, pane, frameID, parent, agentType string, pid int, startedAt int64) {
	t.Helper()
	fx.fake.SetPaneSessionName(pane, session)
	if _, err := fx.m.frames.Upsert(store.Frame{
		FrameID:          frameID,
		PaneID:           pane,
		ParentFrameID:    parent,
		AgentType:        agentType,
		PID:              pid,
		PPID:             1,
		ProcessStartTime: "Sun Apr 20 01:30:00 2026",
		Status:           agentpkg.StatusRunning,
		StartedAt:        startedAt,
		LastSeenAt:       startedAt + 20,
		Verified:         true,
	}); err != nil {
		t.Fatalf("addFrame %s: %v", frameID, err)
	}
}

func (fx *replayFixture) setStatus(session string, st agentpkg.Status) {
	fx.m.mu.Lock()
	fx.m.currentStatus[session] = st
	fx.m.mu.Unlock()
}

// replayState is the observable result compared between the two Modules.
type replayState struct {
	active map[string]string // "session/kind" -> "agent|pane|pid"
	status map[string]agentpkg.Status
}

func captureReplayState(m *Module) replayState {
	m.mu.Lock()
	defer m.mu.Unlock()
	st := replayState{active: map[string]string{}, status: map[string]agentpkg.Status{}}
	for session, per := range m.activeProbeIntents {
		for kind, cur := range per {
			st.active[fmt.Sprintf("%s/%s", session, kind)] = fmt.Sprintf("%s|%s|%d", cur.agentType, cur.paneID, cur.senderPID)
		}
	}
	for session, s := range m.currentStatus {
		st.status[session] = s
	}
	return st
}

// settleDetectors waits until every armed intent's quiet detector goroutine has
// started, so the two Modules are compared at an explicit convergence point.
func settleDetectors(t *testing.T, m *Module, started *int32Counter) {
	t.Helper()
	m.mu.Lock()
	armed := 0
	for _, per := range m.activeProbeIntents {
		armed += len(per)
	}
	m.mu.Unlock()
	waitFor(t, 2*time.Second, func() bool { return started.get() == armed }, "detector goroutines started == armed intents")
}

// baselineReplay is the pre-change algorithm: legacy snapshot (rc == nil) then
// a per-session applyStatus (rc == nil). between runs after the snapshot.
func baselineReplay(m *Module, between func()) {
	snap := m.snapshotStatuses(nil)
	if between != nil {
		between()
	}
	for session, e := range snap {
		m.probeIntentDisp.applyStatus(session, e.agentType, e.status)
	}
}

// memoReplay is the new algorithm with the same optional interleave point.
func memoReplay(m *Module, between func()) {
	if between == nil {
		m.probeIntentDisp.replayStatus()
		return
	}
	rc := &replayProjectionCache{}
	snap := m.snapshotStatuses(rc)
	between()
	for session, e := range snap {
		m.probeIntentDisp.applyStatusWith(session, e.agentType, e.status, rc)
	}
}

type oracleCase struct {
	name string
	// build seeds a fresh fixture; it is called once per Module.
	build func(t *testing.T) *replayFixture
	// between (optional) runs after the snapshot, before the apply loop.
	between func(fx *replayFixture) func()
	// after (optional) runs on both Modules once replay finished.
	after func(fx *replayFixture)
	// wantArmed is a sanity floor on the final armed intents (-1 = unchecked).
	wantArmed int
}

func TestReplayStatus_EquivalentToPerCallBaseline(t *testing.T) {
	cases := []oracleCase{
		{
			name:      "a_codex_running_armed",
			build:     func(t *testing.T) *replayFixture { return newReplayFixture(t, 2, 2) },
			wantArmed: 2,
		},
		{
			name: "b_codex_not_in_shouldActive",
			build: func(t *testing.T) *replayFixture {
				fx := newReplayFixture(t, 2, 1)
				fx.setStatus("s0", agentpkg.StatusIdle)
				return fx
			},
			wantArmed: 1,
		},
		{
			name: "c_cc_without_intents",
			build: func(t *testing.T) *replayFixture {
				fx := newReplayFixture(t, 0, 0)
				fx.m.registry.Register(&fakeAgentProvider{typeName: "cc-noprobes"})
				fx.addFrame(t, "cc", "%c0", "frame-cc", "", "cc-noprobes", 7001, 100)
				fx.setStatus("cc", agentpkg.StatusRunning)
				fx.addFrame(t, "cx", "%x0", "frame-cx", "", "codex", 7002, 100)
				fx.setStatus("cx", agentpkg.StatusRunning)
				return fx
			},
			wantArmed: 1,
		},
		{
			name: "d_session_without_top_frame",
			build: func(t *testing.T) *replayFixture {
				fx := newReplayFixture(t, 1, 1)
				fx.setStatus("ghost", agentpkg.StatusRunning)
				return fx
			},
			wantArmed: 1,
		},
		{
			name: "e_multiple_frames_and_panes_for_one_session",
			build: func(t *testing.T) *replayFixture {
				fx := newReplayFixture(t, 0, 0)
				fx.addFrame(t, "multi", "%m0", "frame-m0-root", "", "codex", 7101, 100)
				fx.addFrame(t, "multi", "%m0", "frame-m0-child", "frame-m0-root", "codex", 7102, 200)
				fx.addFrame(t, "multi", "%m1", "frame-m1", "", "codex", 7103, 300)
				fx.setStatus("multi", agentpkg.StatusWaiting)
				return fx
			},
			wantArmed: 1,
		},
		{
			// A3: the snapshot says Idle; afterwards the top frame is removed
			// and the status flips to Running (what the sweep's
			// afterFrameCleared does). Neither algorithm arms in this round;
			// the next valid hook event (applyStatus) corrects it.
			name: "f_interleave_top_frame_removed_exposes_running",
			build: func(t *testing.T) *replayFixture {
				fx := newReplayFixture(t, 0, 0)
				fx.addFrame(t, "w", "%w0", "frame-w-root", "", "codex", 7201, 100)
				fx.addFrame(t, "w", "%w0", "frame-w-top", "frame-w-root", "codex", 7202, 200)
				fx.setStatus("w", agentpkg.StatusIdle)
				return fx
			},
			between: func(fx *replayFixture) func() {
				return func() {
					if err := fx.m.frames.Delete("frame-w-top"); err != nil {
						panic(err)
					}
					fx.setStatus("w", agentpkg.StatusRunning)
				}
			},
			// The shared "after" below applies the corrective hook event; the
			// armed count asserted here is the post-correction one.
			after: func(fx *replayFixture) {
				if _, ok := readActiveIntent(fx.m, "w", agentpkg.ProbeIntentKindProcessDead); ok {
					panic("armed during the replay round itself (A3 window not reproduced)")
				}
				fx.m.probeIntentDisp.applyStatus("w", "codex", agentpkg.StatusRunning)
			},
			wantArmed: 1,
		},
		{
			// A2: ListAll fails once and PaneSessionName fails once; neither
			// failure may poison the round. Single session so the failing
			// lookup lands on the same session in both Modules.
			name: "g_fail_once_not_poisoning",
			build: func(t *testing.T) *replayFixture {
				fx := newReplayFixture(t, 1, 1)
				failed := false
				fx.m.listFramesFn = func() ([]store.Frame, error) {
					if !failed {
						failed = true
						return nil, fmt.Errorf("injected ListAll failure")
					}
					return fx.m.frames.ListAll()
				}
				fx.tmx.failOnce["%00"] = true
				return fx
			},
			wantArmed: -1,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			base := tc.build(t)
			baseStarts := installQuietDetector(t, base.m)
			neu := tc.build(t)
			neuStarts := installQuietDetector(t, neu.m)

			var baseBetween, neuBetween func()
			if tc.between != nil {
				baseBetween, neuBetween = tc.between(base), tc.between(neu)
			}
			baselineReplay(base.m, baseBetween)
			memoReplay(neu.m, neuBetween)
			if tc.after != nil {
				tc.after(base)
				tc.after(neu)
			}
			settleDetectors(t, base.m, baseStarts)
			settleDetectors(t, neu.m, neuStarts)

			want, got := captureReplayState(base.m), captureReplayState(neu.m)
			if fmt.Sprint(want.active) != fmt.Sprint(got.active) {
				t.Errorf("activeProbeIntents differ:\n baseline=%v\n memo    =%v", want.active, got.active)
			}
			if fmt.Sprint(want.status) != fmt.Sprint(got.status) {
				t.Errorf("currentStatus differ:\n baseline=%v\n memo    =%v", want.status, got.status)
			}
			if tc.wantArmed >= 0 && len(got.active) != tc.wantArmed {
				t.Errorf("armed intents = %d (%v), want %d", len(got.active), got.active, tc.wantArmed)
			}
		})
	}
}

// A2 direct assertions: a failed lookup is retried by the next call and is
// never cached as an empty name / empty projection set.
func TestProjectionForSessionWith_FailuresAreNotCached(t *testing.T) {
	fx := newReplayFixture(t, 1, 1)
	m := fx.m
	rc := &replayProjectionCache{}

	failed := false
	m.listFramesFn = func() ([]store.Frame, error) {
		if !failed {
			failed = true
			return nil, fmt.Errorf("injected ListAll failure")
		}
		return m.frames.ListAll()
	}
	if _, err := m.projectionForSessionWith("s0", rc); err == nil {
		t.Fatalf("first call: want the ListAll error, got nil")
	}
	if rc.loaded {
		t.Fatalf("failed ListAll must not mark the cache loaded")
	}

	fx.tmx.failOnce["%00"] = true
	proj, err := m.projectionForSessionWith("s0", rc)
	if err != nil {
		t.Fatalf("second call: %v", err)
	}
	if proj != nil {
		t.Fatalf("PaneSessionName failed once: want no projection for this call, got %+v", proj)
	}
	if _, cached := rc.paneName["%00"]; cached {
		t.Fatalf("failed PaneSessionName must not be cached (as empty or otherwise)")
	}

	proj, err = m.projectionForSessionWith("s0", rc)
	if err != nil || proj == nil || proj.TopFrame == nil || proj.TopFrame.PaneID != "%00" {
		t.Fatalf("third call must retry and succeed: proj=%+v err=%v", proj, err)
	}
}

// -----------------------------------------------------------------------------
// T2h: A1 barrier — the sweep deleted the frame row after the cache was filled
// -----------------------------------------------------------------------------

func TestReplayStatus_DoesNotArmFrameDeletedAfterCacheLoad(t *testing.T) {
	fx := newReplayFixture(t, 1, 1)
	starts := installQuietDetector(t, fx.m)
	// The first PaneSessionName call happens strictly after the projections
	// were loaded into the cache and strictly before the apply phase; delete
	// the row there (what clearFrame does before it takes m.mu).
	fx.tmx.onFirst = func() {
		if err := fx.m.frames.Delete("frame-s0-0"); err != nil {
			panic(err)
		}
	}

	fx.m.probeIntentDisp.replayStatus()

	if _, ok := readActiveIntent(fx.m, "s0", agentpkg.ProbeIntentKindProcessDead); ok {
		t.Fatalf("armed a detector for a frame whose DB row was already deleted")
	}
	if n := starts.get(); n != 0 {
		t.Fatalf("detector goroutines started = %d, want 0", n)
	}
}
