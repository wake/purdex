package agent

// frame_ops_l2_test.go — L2 (proxy-detach-on-stop) tests, extracted from
// frame_ops_test.go for blame / IDE jump ergonomics. Pure file move; zero
// logic change relative to the pre-split state. Continues to share helpers
// (newProxyTestModule, seedFrame) and fakes with frame_ops_test.go via
// the test-package compilation unit.

import (
	"encoding/json"
	"sync"
	"testing"

	agentpkg "github.com/wake/purdex/internal/agent"
	"github.com/wake/purdex/internal/store"
)

// ---------------------------------------------------------------------------
// L2 Phase 2 P2-T4 — subagentRefMatches turn-aware + findProxyRefByBroker
// (spec §3.2 / plan §2 P2-T4)
// ---------------------------------------------------------------------------

// TestSubagentRefMatches_TurnAware pins the spec §3.2.A behavior of
// subagentRefMatches when at least one side is IsProxy=true, plus the
// existing native-ref ID equality semantics (rows f/g — L2/v2 fix
// regression guards).
func TestSubagentRefMatches_TurnAware(t *testing.T) {
	cases := []struct {
		name string
		a    agentpkg.SubagentRef
		b    agentpkg.SubagentRef
		want bool
	}{
		{
			name: "a — both turnIDs empty, process fallback matches",
			a:    agentpkg.SubagentRef{IsProxy: true, SourcePID: 42, SourceStartTime: "t1"},
			b:    agentpkg.SubagentRef{IsProxy: true, SourcePID: 42, SourceStartTime: "t1"},
			want: true,
		},
		{
			name: "b — one side turnID empty (process fallback)",
			a:    agentpkg.SubagentRef{IsProxy: true, SourcePID: 42, SourceStartTime: "t1"},
			b:    agentpkg.SubagentRef{IsProxy: true, SourcePID: 42, SourceStartTime: "t1", SourceTurnID: "t_a"},
			want: true,
		},
		{
			name: "c — both turnIDs non-empty and equal",
			a:    agentpkg.SubagentRef{IsProxy: true, SourcePID: 42, SourceStartTime: "t1", SourceTurnID: "t_a"},
			b:    agentpkg.SubagentRef{IsProxy: true, SourcePID: 42, SourceStartTime: "t1", SourceTurnID: "t_a"},
			want: true,
		},
		{
			name: "d — both turnIDs non-empty but different (turn-level no match)",
			a:    agentpkg.SubagentRef{IsProxy: true, SourcePID: 42, SourceStartTime: "t1", SourceTurnID: "t_a"},
			b:    agentpkg.SubagentRef{IsProxy: true, SourcePID: 42, SourceStartTime: "t1", SourceTurnID: "t_b"},
			want: false,
		},
		{
			name: "e — IsProxy mismatch (cross-namespace)",
			a:    agentpkg.SubagentRef{IsProxy: true, SourcePID: 42, SourceStartTime: "t1"},
			b:    agentpkg.SubagentRef{IsProxy: false, ID: "task-x"},
			want: false,
		},
		{
			name: "f — native ref same ID (L2/v2 regression pin)",
			a:    agentpkg.SubagentRef{IsProxy: false, ID: "task-x"},
			b:    agentpkg.SubagentRef{IsProxy: false, ID: "task-x"},
			want: true,
		},
		{
			name: "g — native ref different ID (L2/v2 regression pin)",
			a:    agentpkg.SubagentRef{IsProxy: false, ID: "task-x"},
			b:    agentpkg.SubagentRef{IsProxy: false, ID: "task-y"},
			want: false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := subagentRefMatches(tc.a, tc.b); got != tc.want {
				t.Fatalf("subagentRefMatches(%+v, %+v) = %v, want %v", tc.a, tc.b, got, tc.want)
			}
			// Symmetry: matches must be symmetric — swap arguments and re-check.
			if got := subagentRefMatches(tc.b, tc.a); got != tc.want {
				t.Fatalf("subagentRefMatches(%+v, %+v) reverse = %v, want %v", tc.b, tc.a, got, tc.want)
			}
		})
	}
}

// TestFindProxyRefByBroker covers the new process-level lookup helper
// per spec §3.2.B. Lookup is by (PID, StartTime) only — turnID is
// intentionally NOT compared so attach/upsert can locate the existing
// broker ref to mutate-in-place rather than appending a duplicate
// (spec §3.2 F1 fix).
func TestFindProxyRefByBroker(t *testing.T) {
	refs := []agentpkg.SubagentRef{
		{IsProxy: true, SourcePID: 42, SourceStartTime: "t1", SourceTurnID: "t_a"},
		{IsProxy: true, SourcePID: 43, SourceStartTime: "t2", SourceTurnID: "t_b"},
		{IsProxy: false, ID: "task-x"}, // native ref must never match
	}
	cases := []struct {
		name      string
		pid       int
		startTime string
		want      int
	}{
		{name: "a — match first proxy ref", pid: 42, startTime: "t1", want: 0},
		{name: "b — match second proxy ref", pid: 43, startTime: "t2", want: 1},
		{name: "c — no match (PID/StartTime miss)", pid: 99, startTime: "tX", want: -1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := findProxyRefByBroker(refs, tc.pid, tc.startTime); got != tc.want {
				t.Fatalf("findProxyRefByBroker(refs, %d, %q) = %d, want %d", tc.pid, tc.startTime, got, tc.want)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// L2 Phase 2 P2-T5 — upsertProxyRefForBroker
// (spec §3.4 / plan §2 P2-T5)
// ---------------------------------------------------------------------------

// TestUpsertProxyRefForBroker_AppendsWhenNoExistingRef covers case (a):
// parent has no matching broker ref → helper appends one with full identity
// (PID, StartTime, turnID) and ID = "proxy:codex:<pid>:<startTime>"
// (matches SessionStart fast-path at frame_ops.go:218).
func TestUpsertProxyRefForBroker_AppendsWhenNoExistingRef(t *testing.T) {
	m := newProxyTestModule(t)
	parent := seedFrame(t, m, "%5", "cc", 100, "t100", 50)

	persisted, stored, err := m.upsertProxyRefForBroker(parent, 42, "t1", "t_a", 100)
	if err != nil {
		t.Fatalf("upsertProxyRefForBroker: %v", err)
	}
	if !persisted {
		t.Fatalf("persisted = false, want true")
	}
	if len(stored.Subagents) != 1 {
		t.Fatalf("Subagents count = %d, want 1; refs=%+v", len(stored.Subagents), stored.Subagents)
	}
	got := stored.Subagents[0]
	wantID := "proxy:codex:42:t1"
	if !got.IsProxy || got.SourcePID != 42 || got.SourceStartTime != "t1" ||
		got.SourceTurnID != "t_a" || got.ID != wantID || got.Type != "codex" || got.StartedAt != 100 {
		t.Fatalf("appended ref = %+v, want IsProxy=true PID=42 StartTime=t1 TurnID=t_a ID=%q Type=codex StartedAt=100", got, wantID)
	}
	if stored.LastSeenAt != 100 {
		t.Fatalf("stored.LastSeenAt = %d, want 100", stored.LastSeenAt)
	}
}

// TestUpsertProxyRefForBroker_InPlaceFromEmptyTurnID covers case (b):
// parent already has a proxy ref with empty SourceTurnID (SessionStart
// attached without turn_id) → helper mutates SourceTurnID in-place; still
// 1 ref (no append).
func TestUpsertProxyRefForBroker_InPlaceFromEmptyTurnID(t *testing.T) {
	m := newProxyTestModule(t)
	parent := seedFrame(t, m, "%5", "cc", 100, "t100", 50)
	parent.Subagents = []agentpkg.SubagentRef{{
		ID:              "proxy:codex:42:t1",
		Type:            "codex",
		StartedAt:       50,
		SourcePID:       42,
		SourceStartTime: "t1",
		IsProxy:         true,
		// SourceTurnID intentionally empty.
	}}
	if _, err := m.frames.Upsert(parent); err != nil {
		t.Fatalf("seed parent ref: %v", err)
	}
	parent, _ = func() (store.Frame, error) {
		got, err := m.frames.GetByIdentity("%5", 100, "t100")
		return *got, err
	}()

	persisted, stored, err := m.upsertProxyRefForBroker(parent, 42, "t1", "t_a", 200)
	if err != nil {
		t.Fatalf("upsertProxyRefForBroker: %v", err)
	}
	if !persisted {
		t.Fatalf("persisted = false, want true")
	}
	if len(stored.Subagents) != 1 {
		t.Fatalf("Subagents count = %d, want 1 (in-place mutation, not append); refs=%+v", len(stored.Subagents), stored.Subagents)
	}
	got := stored.Subagents[0]
	if got.SourceTurnID != "t_a" {
		t.Fatalf("ref.SourceTurnID = %q, want t_a (in-place upsert)", got.SourceTurnID)
	}
	if got.SourcePID != 42 || got.SourceStartTime != "t1" || !got.IsProxy {
		t.Fatalf("ref identity drifted: %+v", got)
	}
	if got.StartedAt != 200 {
		t.Fatalf("ref.StartedAt = %d, want 200 (recency refresh)", got.StartedAt)
	}
}

// TestUpsertProxyRefForBroker_InPlaceOverwritesExistingTurnID covers case (c):
// parent already has a proxy ref with SourceTurnID="t_a" → helper mutates
// SourceTurnID to "t_b" in-place; still 1 ref (NOT appended). Spec §3.4
// guards against the v3 F1 race where a turn-aware lookup would mistake
// (PID, t1, t_b) for "no existing ref" and append a duplicate.
func TestUpsertProxyRefForBroker_InPlaceOverwritesExistingTurnID(t *testing.T) {
	m := newProxyTestModule(t)
	parent := seedFrame(t, m, "%5", "cc", 100, "t100", 50)
	parent.Subagents = []agentpkg.SubagentRef{{
		ID:              "proxy:codex:42:t1",
		Type:            "codex",
		StartedAt:       50,
		SourcePID:       42,
		SourceStartTime: "t1",
		IsProxy:         true,
		SourceTurnID:    "t_a",
	}}
	if _, err := m.frames.Upsert(parent); err != nil {
		t.Fatalf("seed parent ref: %v", err)
	}
	reloaded, err := m.frames.GetByIdentity("%5", 100, "t100")
	if err != nil || reloaded == nil {
		t.Fatalf("reload parent: %v / %v", err, reloaded)
	}

	persisted, stored, err := m.upsertProxyRefForBroker(*reloaded, 42, "t1", "t_b", 300)
	if err != nil {
		t.Fatalf("upsertProxyRefForBroker: %v", err)
	}
	if !persisted {
		t.Fatalf("persisted = false, want true")
	}
	if len(stored.Subagents) != 1 {
		t.Fatalf("Subagents count = %d, want 1 (in-place overwrite, not append); refs=%+v", len(stored.Subagents), stored.Subagents)
	}
	if stored.Subagents[0].SourceTurnID != "t_b" {
		t.Fatalf("ref.SourceTurnID = %q, want t_b (overwrite)", stored.Subagents[0].SourceTurnID)
	}
}

// TestUpsertProxyRefForBroker_DowngradesOnEmptyParse pins the round-3
// trade-off resolution: when the incoming turnID == "" (parse failure on a
// malformed PreToolUse/UserPromptSubmit hook payload), the helper
// unconditionally overwrites the ref's SourceTurnID with the empty value
// rather than preserving the previous turn.
//
// This is a deliberate design choice on a physical-tradeoff boundary, NOT
// a one-true-answer. The previous turn_id cannot be safely preserved
// because a legitimate late Stop(t_old) would then targeted-detach the
// ref of the next turn — the more common race in normal codex CLI traffic
// (every turn boundary). Downgrading to empty accepts the rarer
// double-malformed case (parse-failed upsert + parse-failed empty-turn
// Stop) as a known limitation; spec §3.4 documents this. See PR #801
// round-2 A2 / round-3 P2 / consulting-review history for the full
// derivation.
func TestUpsertProxyRefForBroker_DowngradesOnEmptyParse(t *testing.T) {
	m := newProxyTestModule(t)
	parent := seedFrame(t, m, "%5", "cc", 100, "t100", 50)
	parent.Subagents = []agentpkg.SubagentRef{{
		ID:              "proxy:codex:42:t1",
		Type:            "codex",
		StartedAt:       50,
		SourcePID:       42,
		SourceStartTime: "t1",
		IsProxy:         true,
		SourceTurnID:    "t_a",
	}}
	if _, err := m.frames.Upsert(parent); err != nil {
		t.Fatalf("seed parent ref: %v", err)
	}
	reloaded, err := m.frames.GetByIdentity("%5", 100, "t100")
	if err != nil || reloaded == nil {
		t.Fatalf("reload parent: %v / %v", err, reloaded)
	}

	// Incoming turnID = "" simulates a malformed UserPromptSubmit/PreToolUse
	// raw_event whose parseCodexTurnID returned "". The existing ref carries
	// SourceTurnID="t_a"; the spec choice is to OVERWRITE it with "" so that
	// a late legitimate Stop(t_a) cannot targeted-detach this ref.
	persisted, stored, err := m.upsertProxyRefForBroker(*reloaded, 42, "t1", "", 300)
	if err != nil {
		t.Fatalf("upsertProxyRefForBroker: %v", err)
	}
	if !persisted {
		t.Fatalf("persisted = false, want true")
	}
	if len(stored.Subagents) != 1 {
		t.Fatalf("Subagents count = %d, want 1; refs=%+v", len(stored.Subagents), stored.Subagents)
	}
	if stored.Subagents[0].SourceTurnID != "" {
		t.Fatalf("ref.SourceTurnID = %q, want \"\" (downgrade on empty parse — spec §3.4 known-limitation choice)", stored.Subagents[0].SourceTurnID)
	}
	if stored.Subagents[0].StartedAt != 300 {
		t.Fatalf("ref.StartedAt = %d, want 300 (broadcastTs still bumps)", stored.Subagents[0].StartedAt)
	}
}

// TestUpsertProxyRefForBroker_RetryOnConflict covers case (d): the first
// UpsertIfUnchanged conflicts (a concurrent writer bumped LastSeenAt
// between the caller's read and our write); the helper reloads, re-runs
// findProxyRefByBroker against the fresh refs, and the second attempt
// succeeds. Mirrors the race-injection pattern from
// TestProxySubagent_ConcurrentAttachesBothLand (PR17 #632).
func TestUpsertProxyRefForBroker_RetryOnConflict(t *testing.T) {
	m := newProxyTestModule(t)
	parent := seedFrame(t, m, "%5", "cc", 100, "t100", 50)

	// Inject a concurrent racer write BEFORE our upsert call. The racer
	// adds an unrelated proxy ref and bumps LastSeenAt to 60. Our caller's
	// `parent` snapshot is still at LastSeenAt=50, so the helper's first
	// UpsertIfUnchanged(expected=50) fails; the helper reloads, sees the
	// racer's ref + LastSeenAt=60, and the second attempt succeeds —
	// preserving both the racer's ref and our newly-appended ref.
	racer := agentpkg.SubagentRef{
		ID:              "proxy:codex:999:t999",
		Type:            "codex",
		StartedAt:       55,
		SourcePID:       999,
		SourceStartTime: "t999",
		IsProxy:         true,
		SourceTurnID:    "t_x",
	}
	cur, err := m.frames.GetByIdentity("%5", 100, "t100")
	if err != nil || cur == nil {
		t.Fatalf("racer baseline read: %v / %v", err, cur)
	}
	cur.Subagents = append(cur.Subagents, racer)
	cur.LastSeenAt = 60
	if _, err := m.frames.Upsert(*cur); err != nil {
		t.Fatalf("racer Upsert: %v", err)
	}

	// Call helper with the STALE parent (LastSeenAt=50). First attempt
	// conflicts, reload picks up racer + LastSeenAt=60, second attempt
	// merges and succeeds.
	persisted, stored, err := m.upsertProxyRefForBroker(parent, 42, "t1", "t_a", 100)
	if err != nil {
		t.Fatalf("upsertProxyRefForBroker: %v", err)
	}
	if !persisted {
		t.Fatalf("persisted = false, want true after retry")
	}
	if len(stored.Subagents) != 2 {
		t.Fatalf("Subagents count = %d, want 2 (racer + new); refs=%+v", len(stored.Subagents), stored.Subagents)
	}
	pids := map[int]bool{}
	for _, ref := range stored.Subagents {
		pids[ref.SourcePID] = true
	}
	if !pids[42] || !pids[999] {
		t.Fatalf("Subagents PIDs = %v, want both 42 and 999", pids)
	}
	if stored.LastSeenAt != 100 {
		t.Fatalf("stored.LastSeenAt = %d, want 100", stored.LastSeenAt)
	}
}

// ---------------------------------------------------------------------------
// L2 Phase 2 P2-T6 — pane-scan removeProxyRefForSenderTurn +
// detachProxyRefForSenderTurnWithRetry + subagentsContainProxySenderTurn
// (spec §3.3.D / plan §2 P2-T6)
// ---------------------------------------------------------------------------

// TestSubagentsContainProxySenderTurn covers the new pure filter helper.
// Mirrors subagentsContainProxySender (frame_ops.go:831-838) plus a turnID
// match — three identity fields must all align.
func TestSubagentsContainProxySenderTurn(t *testing.T) {
	cases := []struct {
		name string
		refs []agentpkg.SubagentRef
		want bool
	}{
		{
			name: "a — three full match",
			refs: []agentpkg.SubagentRef{
				{IsProxy: true, SourcePID: 42, SourceStartTime: "t1", SourceTurnID: "t_a"},
			},
			want: true,
		},
		{
			name: "b — PID match but turn_id differs",
			refs: []agentpkg.SubagentRef{
				{IsProxy: true, SourcePID: 42, SourceStartTime: "t1", SourceTurnID: "t_b"},
			},
			want: false,
		},
		{
			name: "c — turn_id match but PID differs",
			refs: []agentpkg.SubagentRef{
				{IsProxy: true, SourcePID: 99, SourceStartTime: "tX", SourceTurnID: "t_a"},
			},
			want: false,
		},
		{
			name: "d — IsProxy=false (native) never matches",
			refs: []agentpkg.SubagentRef{
				{IsProxy: false, SourcePID: 42, SourceStartTime: "t1", SourceTurnID: "t_a"},
			},
			want: false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := subagentsContainProxySenderTurn(tc.refs, 42, "t1", "t_a"); got != tc.want {
				t.Fatalf("subagentsContainProxySenderTurn(%+v, 42, t1, t_a) = %v, want %v", tc.refs, got, tc.want)
			}
		})
	}
}

// TestRemoveProxyRefForSenderTurn_PaneScan covers the top-level pane-scan
// helper. Mirrors removeProxyRefForSender control flow but adds a
// turnID gate so only the matching turn's ref is detached. 5 cases.
func TestRemoveProxyRefForSenderTurn_PaneScan(t *testing.T) {
	t.Run("a — single frame with matching ref detaches", func(t *testing.T) {
		m := newProxyTestModule(t)
		f := seedFrame(t, m, "%5", "cc", 100, "t100", 50)
		f.Subagents = []agentpkg.SubagentRef{{
			IsProxy: true, SourcePID: 42, SourceStartTime: "t1", SourceTurnID: "t_a",
			ID: "proxy:codex:42:t1", Type: "codex", StartedAt: 50,
		}}
		if _, err := m.frames.Upsert(f); err != nil {
			t.Fatalf("seed: %v", err)
		}

		removed, _, _, _, err := m.removeProxyRefForSenderTurn("%5", 42, "t1", "t_a", 100)
		if err != nil {
			t.Fatalf("removeProxyRefForSenderTurn: %v", err)
		}
		if !removed {
			t.Fatalf("removed = false, want true")
		}
		final, _ := m.frames.GetByIdentity("%5", 100, "t100")
		if final == nil || len(final.Subagents) != 0 {
			t.Fatalf("Subagents = %+v, want empty after detach", final.Subagents)
		}
	})

	t.Run("b — two frames carry same broker but different turnIDs; only matching frame's ref drops", func(t *testing.T) {
		m := newProxyTestModule(t)
		f1 := seedFrame(t, m, "%5", "cc", 100, "t100", 50)
		f1.Subagents = []agentpkg.SubagentRef{{
			IsProxy: true, SourcePID: 42, SourceStartTime: "t1", SourceTurnID: "t_a",
			ID: "proxy:codex:42:t1", Type: "codex", StartedAt: 50,
		}}
		if _, err := m.frames.Upsert(f1); err != nil {
			t.Fatalf("seed f1: %v", err)
		}
		f2 := seedFrame(t, m, "%5", "cc", 200, "t200", 51)
		f2.Subagents = []agentpkg.SubagentRef{{
			IsProxy: true, SourcePID: 42, SourceStartTime: "t1", SourceTurnID: "t_b",
			ID: "proxy:codex:42:t1", Type: "codex", StartedAt: 51,
		}}
		if _, err := m.frames.Upsert(f2); err != nil {
			t.Fatalf("seed f2: %v", err)
		}

		removed, _, _, _, err := m.removeProxyRefForSenderTurn("%5", 42, "t1", "t_b", 100)
		if err != nil {
			t.Fatalf("removeProxyRefForSenderTurn: %v", err)
		}
		if !removed {
			t.Fatalf("removed = false, want true (f2 should drop)")
		}
		// f1 still has its t_a ref.
		final1, _ := m.frames.GetByIdentity("%5", 100, "t100")
		if final1 == nil || len(final1.Subagents) != 1 || final1.Subagents[0].SourceTurnID != "t_a" {
			t.Fatalf("f1 Subagents = %+v, want unchanged with turnID=t_a", final1.Subagents)
		}
		// f2's ref dropped.
		final2, _ := m.frames.GetByIdentity("%5", 200, "t200")
		if final2 == nil || len(final2.Subagents) != 0 {
			t.Fatalf("f2 Subagents = %+v, want empty after detach", final2.Subagents)
		}
	})

	t.Run("c — pane has no frame matching → returns false", func(t *testing.T) {
		m := newProxyTestModule(t)
		seedFrame(t, m, "%5", "cc", 100, "t100", 50)
		// Pane has 1 frame but no proxy ref at all.

		removed, _, _, _, err := m.removeProxyRefForSenderTurn("%5", 42, "t1", "t_a", 100)
		if err != nil {
			t.Fatalf("removeProxyRefForSenderTurn: %v", err)
		}
		if removed {
			t.Fatalf("removed = true, want false (no matching ref)")
		}
	})

	t.Run("d — IsProxy=false ref with coincidental identity is NOT dropped", func(t *testing.T) {
		m := newProxyTestModule(t)
		f := seedFrame(t, m, "%5", "cc", 100, "t100", 50)
		// Native ref with the same SourcePID/StartTime/TurnID strings —
		// must remain because IsProxy=false (cross-namespace isolation).
		f.Subagents = []agentpkg.SubagentRef{{
			IsProxy: false, SourcePID: 42, SourceStartTime: "t1", SourceTurnID: "t_a",
			ID: "task-x", Type: "cc", StartedAt: 50,
		}}
		if _, err := m.frames.Upsert(f); err != nil {
			t.Fatalf("seed: %v", err)
		}

		removed, _, _, _, err := m.removeProxyRefForSenderTurn("%5", 42, "t1", "t_a", 100)
		if err != nil {
			t.Fatalf("removeProxyRefForSenderTurn: %v", err)
		}
		if removed {
			t.Fatalf("removed = true, want false (native ref must not be dropped)")
		}
		final, _ := m.frames.GetByIdentity("%5", 100, "t100")
		if final == nil || len(final.Subagents) != 1 {
			t.Fatalf("Subagents = %+v, want native ref preserved", final.Subagents)
		}
	})

	t.Run("e — turnID mismatch on matching broker → ref NOT dropped", func(t *testing.T) {
		m := newProxyTestModule(t)
		f := seedFrame(t, m, "%5", "cc", 100, "t100", 50)
		f.Subagents = []agentpkg.SubagentRef{{
			IsProxy: true, SourcePID: 42, SourceStartTime: "t1", SourceTurnID: "t_a",
			ID: "proxy:codex:42:t1", Type: "codex", StartedAt: 50,
		}}
		if _, err := m.frames.Upsert(f); err != nil {
			t.Fatalf("seed: %v", err)
		}

		removed, _, _, _, err := m.removeProxyRefForSenderTurn("%5", 42, "t1", "t_b", 100)
		if err != nil {
			t.Fatalf("removeProxyRefForSenderTurn: %v", err)
		}
		if removed {
			t.Fatalf("removed = true, want false (turnID mismatch)")
		}
		final, _ := m.frames.GetByIdentity("%5", 100, "t100")
		if final == nil || len(final.Subagents) != 1 || final.Subagents[0].SourceTurnID != "t_a" {
			t.Fatalf("Subagents = %+v, want unchanged turn t_a", final.Subagents)
		}
	})

	// Round-2 A1 invariant: when called with turnID == "" (the empty-Stop
	// fallback path), the helper must NOT drop a ref that has been upgraded
	// to a non-empty SourceTurnID. Mirrors the TOCTOU window where Stop's
	// case (c) ListByPane saw an empty ref but a concurrent UserPromptSubmit
	// upsert upgraded it before our detach helper ran.
	t.Run("f — turnID == \"\" rejects upgraded turn-aware ref (round-2 A1)", func(t *testing.T) {
		m := newProxyTestModule(t)
		f := seedFrame(t, m, "%5", "cc", 100, "t100", 50)
		f.Subagents = []agentpkg.SubagentRef{{
			IsProxy: true, SourcePID: 42, SourceStartTime: "t1", SourceTurnID: "t_a",
			ID: "proxy:codex:42:t1", Type: "codex", StartedAt: 50,
		}}
		if _, err := m.frames.Upsert(f); err != nil {
			t.Fatalf("seed: %v", err)
		}

		removed, _, _, _, err := m.removeProxyRefForSenderTurn("%5", 42, "t1", "", 100)
		if err != nil {
			t.Fatalf("removeProxyRefForSenderTurn: %v", err)
		}
		if removed {
			t.Fatalf("removed = true, want false (empty-turn helper must not drop upgraded ref)")
		}
		final, _ := m.frames.GetByIdentity("%5", 100, "t100")
		if final == nil || len(final.Subagents) != 1 || final.Subagents[0].SourceTurnID != "t_a" {
			t.Fatalf("Subagents = %+v, want preserved turn=t_a", final.Subagents)
		}
	})

	// Round-2 A1 positive path: turnID == "" matches refs with SourceTurnID
	// == "" (the legitimate case (c) detach when SessionStart attached a ref
	// but no UserPromptSubmit/PreToolUse ever upserted a turn).
	t.Run("g — turnID == \"\" matches empty ref (round-2 A1 positive)", func(t *testing.T) {
		m := newProxyTestModule(t)
		f := seedFrame(t, m, "%5", "cc", 100, "t100", 50)
		f.Subagents = []agentpkg.SubagentRef{{
			IsProxy: true, SourcePID: 42, SourceStartTime: "t1", /* SourceTurnID="" */
			ID: "proxy:codex:42:t1", Type: "codex", StartedAt: 50,
		}}
		if _, err := m.frames.Upsert(f); err != nil {
			t.Fatalf("seed: %v", err)
		}

		removed, _, _, _, err := m.removeProxyRefForSenderTurn("%5", 42, "t1", "", 100)
		if err != nil {
			t.Fatalf("removeProxyRefForSenderTurn: %v", err)
		}
		if !removed {
			t.Fatalf("removed = false, want true (empty turn matches empty ref)")
		}
		final, _ := m.frames.GetByIdentity("%5", 100, "t100")
		if final == nil || len(final.Subagents) != 0 {
			t.Fatalf("Subagents = %+v, want empty after detach", final.Subagents)
		}
	})
}

// TestDetachProxyRefForSenderTurnWithRetry_RetryOnConflict covers the
// retry path on the frame-level helper. Mirrors detachProxyRefWithRetry
// retry semantics with the turn-aware filter.
func TestDetachProxyRefForSenderTurnWithRetry_RetryOnConflict(t *testing.T) {
	m := newProxyTestModule(t)
	f := seedFrame(t, m, "%5", "cc", 100, "t100", 50)
	f.Subagents = []agentpkg.SubagentRef{{
		IsProxy: true, SourcePID: 42, SourceStartTime: "t1", SourceTurnID: "t_a",
		ID: "proxy:codex:42:t1", Type: "codex", StartedAt: 50,
	}}
	if _, err := m.frames.Upsert(f); err != nil {
		t.Fatalf("seed: %v", err)
	}
	owner, err := m.frames.GetByIdentity("%5", 100, "t100")
	if err != nil || owner == nil {
		t.Fatalf("owner read: %v / %v", err, owner)
	}

	// Inject a concurrent racer write that adds an unrelated ref and
	// bumps LastSeenAt away from the snapshot the helper carries. Helper's
	// first UpsertIfUnchanged conflicts; reload picks up racer + new
	// LastSeenAt; second attempt detaches our matching ref and preserves
	// the racer.
	cur, err := m.frames.GetByIdentity("%5", 100, "t100")
	if err != nil || cur == nil {
		t.Fatalf("racer baseline: %v / %v", err, cur)
	}
	cur.Subagents = append(cur.Subagents, agentpkg.SubagentRef{
		IsProxy: true, SourcePID: 999, SourceStartTime: "t999", SourceTurnID: "t_x",
		ID: "proxy:codex:999:t999", Type: "codex", StartedAt: 55,
	})
	cur.LastSeenAt = owner.LastSeenAt + 10
	if _, err := m.frames.Upsert(*cur); err != nil {
		t.Fatalf("racer Upsert: %v", err)
	}

	detached, stored, err := m.detachProxyRefForSenderTurnWithRetry(*owner, 42, "t1", "t_a", 100)
	if err != nil {
		t.Fatalf("detachProxyRefForSenderTurnWithRetry: %v", err)
	}
	if !detached {
		t.Fatalf("detached = false, want true after retry")
	}
	// Final state: only racer's ref remains.
	if len(stored.Subagents) != 1 {
		t.Fatalf("Subagents count = %d, want 1 (racer preserved); refs=%+v", len(stored.Subagents), stored.Subagents)
	}
	if stored.Subagents[0].SourcePID != 999 {
		t.Fatalf("surviving ref PID = %d, want 999 (racer)", stored.Subagents[0].SourcePID)
	}
}

// ---------------------------------------------------------------------------
// L2 Phase 3 P3-T7a/T7b/T8a/T8b/T9 — TurnAwareProxyDetach integration tests
// (spec §5 rows 1-20 / plan §3 P3-T7a..T9)
//
// All rows live in the single TestApplyFrameEvent_TurnAwareProxyDetach
// table-driven test below. Subtests are named by spec §5 row number for
// fast triage on failure.
// ---------------------------------------------------------------------------

// turnAwareEnv captures the shared liveness/proc seams that every codex L2
// integration row toggles. Most rows want findProxyParent to succeed via a
// direct PPID hit; helper centralizes the cleanup boilerplate.
func turnAwareEnvAlive(t *testing.T, parentPID int, parentStartTime string) {
	t.Helper()
	origInfo := readProcessInfoFn
	origStart := processStartTimeFn
	origAlive := isPidAliveFn
	readProcessInfoFn = func(pid int) (agentpkg.ProcessInfo, error) {
		// Senders' PPID points to the parent (covers row setups where the
		// codex sender is a direct child of the cc parent process).
		return agentpkg.ProcessInfo{PID: pid, PPID: parentPID}, nil
	}
	processStartTimeFn = func(pid int) (string, error) {
		if pid == parentPID {
			return parentStartTime, nil
		}
		return "other", nil
	}
	isPidAliveFn = func(int) bool { return true }
	t.Cleanup(func() {
		readProcessInfoFn = origInfo
		processStartTimeFn = origStart
		isPidAliveFn = origAlive
	})
}

// turnAwareEnvNoParent stubs the seams so findProxyParent always returns nil
// (used by row 20 PreToolUse no-parent guard test).
func turnAwareEnvNoParent(t *testing.T) {
	t.Helper()
	origInfo := readProcessInfoFn
	origStart := processStartTimeFn
	origAlive := isPidAliveFn
	readProcessInfoFn = func(pid int) (agentpkg.ProcessInfo, error) {
		// PPID=1 -> walk terminates immediately, no proxy parent.
		return agentpkg.ProcessInfo{PID: pid, PPID: 1}, nil
	}
	processStartTimeFn = func(int) (string, error) { return "other", nil }
	isPidAliveFn = func(int) bool { return true }
	t.Cleanup(func() {
		readProcessInfoFn = origInfo
		processStartTimeFn = origStart
		isPidAliveFn = origAlive
	})
}

// seedProxyRef seeds a parent frame with the supplied proxy refs. Returns
// the reloaded parent (so callers see the LastSeenAt UpsertIfUnchanged
// uses for optimistic-concurrency).
func seedProxyRef(t *testing.T, m *Module, paneID, parentType string, parentPID int, parentStartTime string, lastSeenAt int64, refs []agentpkg.SubagentRef) store.Frame {
	t.Helper()
	parent := seedFrame(t, m, paneID, parentType, parentPID, parentStartTime, lastSeenAt)
	if len(refs) > 0 {
		parent.Subagents = refs
		if _, err := m.frames.Upsert(parent); err != nil {
			t.Fatalf("seedProxyRef upsert: %v", err)
		}
		reloaded, err := m.frames.GetByIdentity(paneID, parentPID, parentStartTime)
		if err != nil || reloaded == nil {
			t.Fatalf("seedProxyRef reload: %v / %v", err, reloaded)
		}
		parent = *reloaded
	}
	return parent
}

// rawTurn returns a raw codex hook payload carrying turn_id=value (or no
// turn_id when value is the empty string).
func rawTurn(value string) json.RawMessage {
	if value == "" {
		return json.RawMessage(`{}`)
	}
	return json.RawMessage(`{"turn_id":"` + value + `"}`)
}

func TestApplyFrameEvent_TurnAwareProxyDetach(t *testing.T) {
	// Row 1: parent cc + 1 ref(PID=42, t1, turnID="") (SessionStart attached)
	// → UserPromptSubmit from PID=42 raw turn_id="t_a" → in-place upsert
	// SourceTurnID="t_a"; trace upserted_on_user_prompt.
	t.Run("row01_user_prompt_in_place_upsert_from_empty_turn", func(t *testing.T) {
		m := newProxyTestModule(t)
		seedProxyRef(t, m, "%5", "cc", 100, "t100", 50, []agentpkg.SubagentRef{{
			ID: "proxy:codex:42:t1", Type: "codex", StartedAt: 50,
			SourcePID: 42, SourceStartTime: "t1", IsProxy: true,
		}})
		turnAwareEnvAlive(t, 100, "t100")

		req := EventRequest{
			TmuxSession: "work", TmuxPaneID: "%5",
			PurdexName: "PdxUserPromptSubmit",
			AgentType:  "codex", SenderPID: 42, SenderStartTime: "t1",
			RawEvent: rawTurn("t_a"),
		}
		_, meta, err := m.applyFrameEvent(req, agentpkg.DeriveResult{Valid: true, Status: agentpkg.StatusRunning}, 200)
		if err != nil {
			t.Fatalf("applyFrameEvent: %v", err)
		}
		if meta.Reason != "proxy_subagent_upserted_on_user_prompt" {
			t.Fatalf("reason = %q, want proxy_subagent_upserted_on_user_prompt; meta=%+v", meta.Reason, meta)
		}
		final, err := m.frames.GetByIdentity("%5", 100, "t100")
		if err != nil || final == nil {
			t.Fatalf("reload parent: %v / %v", err, final)
		}
		if len(final.Subagents) != 1 {
			t.Fatalf("Subagents count = %d, want 1 (in-place upsert); refs=%+v", len(final.Subagents), final.Subagents)
		}
		got := final.Subagents[0]
		if got.SourceTurnID != "t_a" || got.SourcePID != 42 || got.SourceStartTime != "t1" || !got.IsProxy {
			t.Fatalf("ref after upsert = %+v, want PID=42 StartTime=t1 turnID=t_a IsProxy=true", got)
		}
	})

	// Row 5: parent cc + 1 ref(PID=42, t1, turnID="t_a")
	// → UserPromptSubmit from PID=42 raw turn_id="t_b"
	// → ref.SourceTurnID overwritten t_a→t_b in-place (still 1 ref).
	t.Run("row05_user_prompt_overwrites_existing_turn_in_place", func(t *testing.T) {
		m := newProxyTestModule(t)
		seedProxyRef(t, m, "%5", "cc", 100, "t100", 50, []agentpkg.SubagentRef{{
			ID: "proxy:codex:42:t1", Type: "codex", StartedAt: 50,
			SourcePID: 42, SourceStartTime: "t1", IsProxy: true,
			SourceTurnID: "t_a",
		}})
		turnAwareEnvAlive(t, 100, "t100")

		req := EventRequest{
			TmuxSession: "work", TmuxPaneID: "%5",
			PurdexName: "PdxUserPromptSubmit",
			AgentType:  "codex", SenderPID: 42, SenderStartTime: "t1",
			RawEvent: rawTurn("t_b"),
		}
		_, meta, err := m.applyFrameEvent(req, agentpkg.DeriveResult{Valid: true, Status: agentpkg.StatusRunning}, 300)
		if err != nil {
			t.Fatalf("applyFrameEvent: %v", err)
		}
		if meta.Reason != "proxy_subagent_upserted_on_user_prompt" {
			t.Fatalf("reason = %q, want proxy_subagent_upserted_on_user_prompt", meta.Reason)
		}
		final, _ := m.frames.GetByIdentity("%5", 100, "t100")
		if final == nil || len(final.Subagents) != 1 {
			t.Fatalf("Subagents = %+v, want 1 ref (in-place overwrite, no append)", final.Subagents)
		}
		if final.Subagents[0].SourceTurnID != "t_b" {
			t.Fatalf("ref.SourceTurnID = %q, want t_b", final.Subagents[0].SourceTurnID)
		}
	})

	// Row 7: parent cc + 0 refs (resumeThread first dispatch, no SessionStart)
	// → UserPromptSubmit from PID=42 raw turn_id="t_a" AgentType=codex
	// → first-time attach via append; ref ID == "proxy:codex:42:t1".
	t.Run("row07_user_prompt_first_time_attach_resume_thread", func(t *testing.T) {
		m := newProxyTestModule(t)
		seedProxyRef(t, m, "%5", "cc", 100, "t100", 50, nil)
		turnAwareEnvAlive(t, 100, "t100")

		req := EventRequest{
			TmuxSession: "work", TmuxPaneID: "%5",
			PurdexName: "PdxUserPromptSubmit",
			AgentType:  "codex", SenderPID: 42, SenderStartTime: "t1",
			RawEvent: rawTurn("t_a"),
		}
		_, meta, err := m.applyFrameEvent(req, agentpkg.DeriveResult{Valid: true, Status: agentpkg.StatusRunning}, 200)
		if err != nil {
			t.Fatalf("applyFrameEvent: %v", err)
		}
		if meta.Reason != "proxy_subagent_attached_on_user_prompt" {
			t.Fatalf("reason = %q, want proxy_subagent_attached_on_user_prompt", meta.Reason)
		}
		final, _ := m.frames.GetByIdentity("%5", 100, "t100")
		if final == nil || len(final.Subagents) != 1 {
			t.Fatalf("Subagents = %+v, want exactly 1 ref after first attach", final.Subagents)
		}
		got := final.Subagents[0]
		wantID := "proxy:codex:42:t1"
		if got.ID != wantID {
			t.Fatalf("ref.ID = %q, want %q (deterministic, mirrors SessionStart fast-path)", got.ID, wantID)
		}
		if got.Type != "codex" || !got.IsProxy || got.SourcePID != 42 || got.SourceStartTime != "t1" || got.SourceTurnID != "t_a" {
			t.Fatalf("ref = %+v, want IsProxy=true Type=codex PID=42 StartTime=t1 turnID=t_a", got)
		}

		// Subsequent same-broker upsert reuses ID (no second ref).
		req2 := req
		req2.RawEvent = rawTurn("t_b")
		_, meta2, err := m.applyFrameEvent(req2, agentpkg.DeriveResult{Valid: true, Status: agentpkg.StatusRunning}, 300)
		if err != nil {
			t.Fatalf("applyFrameEvent #2: %v", err)
		}
		if meta2.Reason != "proxy_subagent_upserted_on_user_prompt" {
			t.Fatalf("reason #2 = %q, want proxy_subagent_upserted_on_user_prompt", meta2.Reason)
		}
		final2, _ := m.frames.GetByIdentity("%5", 100, "t100")
		if final2 == nil || len(final2.Subagents) != 1 {
			t.Fatalf("Subagents after re-upsert = %+v, want still 1 ref", final2.Subagents)
		}
		if final2.Subagents[0].ID != wantID {
			t.Fatalf("re-upsert ref.ID drift: %q, want %q", final2.Subagents[0].ID, wantID)
		}
	})

	// Row 7b (round-2 D1): parent cc + 0 refs (resumeThread first dispatch,
	// no SessionStart) → first hook is PdxPreToolUse from PID=42 raw
	// turn_id="t_a" AgentType=codex → first-time attach via append; ref ID
	// == "proxy:codex:42:t1". Pins spec §2.2: the first hook on a recovered
	// thread can legitimately be PreToolUse (not just UserPromptSubmit) when
	// the codex CLI emits PreToolUse before its first UserPromptSubmit.
	// Row 7 covered UserPromptSubmit only; this case proves the same upsert
	// path runs for PreToolUse.
	t.Run("row07b_pre_tool_first_time_attach_resume_thread", func(t *testing.T) {
		m := newProxyTestModule(t)
		seedProxyRef(t, m, "%5", "cc", 100, "t100", 50, nil)
		turnAwareEnvAlive(t, 100, "t100")

		req := EventRequest{
			TmuxSession: "work", TmuxPaneID: "%5",
			PurdexName: "PdxPreToolUse",
			AgentType:  "codex", SenderPID: 42, SenderStartTime: "t1",
			RawEvent: rawTurn("t_a"),
		}
		_, meta, err := m.applyFrameEvent(req, agentpkg.DeriveResult{Valid: true, Status: agentpkg.StatusRunning}, 200)
		if err != nil {
			t.Fatalf("applyFrameEvent: %v", err)
		}
		if meta.Reason != "proxy_subagent_attached_on_user_prompt" {
			t.Fatalf("reason = %q, want proxy_subagent_attached_on_user_prompt (PreToolUse shares the upsert path)", meta.Reason)
		}
		final, _ := m.frames.GetByIdentity("%5", 100, "t100")
		if final == nil || len(final.Subagents) != 1 {
			t.Fatalf("Subagents = %+v, want exactly 1 ref after first PreToolUse attach", final.Subagents)
		}
		got := final.Subagents[0]
		wantID := "proxy:codex:42:t1"
		if got.ID != wantID {
			t.Fatalf("ref.ID = %q, want %q", got.ID, wantID)
		}
		if got.Type != "codex" || !got.IsProxy || got.SourcePID != 42 || got.SourceStartTime != "t1" || got.SourceTurnID != "t_a" {
			t.Fatalf("ref = %+v, want IsProxy=true Type=codex PID=42 StartTime=t1 turnID=t_a", got)
		}
	})

	// Row 17: opencode UserPromptSubmit (AgentType != codex) → must break
	// early; existing generic path runs. Subagents on the cc parent must
	// remain unchanged. (Spec §5 row 17 isolation guard.)
	t.Run("row17_opencode_user_prompt_breaks_early_no_mutation", func(t *testing.T) {
		m := newProxyTestModule(t)
		// Register opencode provider distinct from cc/codex (matches §5 row).
		m.registry.Register(&fakeAgentProvider{
			typeName: "opencode",
			derive:   func(string, json.RawMessage) agentpkg.DeriveResult { return agentpkg.DeriveResult{Valid: true, Status: agentpkg.StatusIdle} },
		})
		parent := seedProxyRef(t, m, "%5", "cc", 100, "t100", 50, nil)
		// Empty refs; we assert on Subagents staying empty/zero-length.
		_ = parent
		turnAwareEnvAlive(t, 100, "t100")

		req := EventRequest{
			TmuxSession: "work", TmuxPaneID: "%5",
			PurdexName: "PdxUserPromptSubmit",
			AgentType:  "opencode", SenderPID: 99, SenderStartTime: "t99",
			RawEvent: rawTurn("ignored"),
		}
		_, meta, err := m.applyFrameEvent(req, agentpkg.DeriveResult{Valid: true, Status: agentpkg.StatusRunning}, 200)
		if err != nil {
			t.Fatalf("applyFrameEvent: %v", err)
		}
		// Generic path runs (cross-type proxy fast-path is gated to
		// LifecycleSessionStart and does NOT run for UserPromptSubmit), so
		// the opencode sender either attaches as proxy via reconcile or
		// creates a standalone frame. EITHER way — the cc parent's
		// Subagents must NOT have a SourceTurnID="ignored" written by
		// the new lifecycle case.
		final, err := m.frames.GetByIdentity("%5", 100, "t100")
		if err != nil || final == nil {
			t.Fatalf("reload parent: %v / %v", err, final)
		}
		for _, ref := range final.Subagents {
			if ref.SourceTurnID == "ignored" {
				t.Fatalf("opencode UserPromptSubmit leaked SourceTurnID=ignored onto cc parent ref %+v", ref)
			}
		}
		// Round-2 D3: strengthen the isolation guard. The cc parent must
		// have zero Subagents (UserPromptSubmit must not attach any ref —
		// proxy reconcile + fast-path are SessionStart-gated) AND meta.Reason
		// must not be drawn from the L2 vocabulary (any new reason added by
		// the L2 dispatch would mean opencode was incorrectly routed through
		// the codex-only branch).
		if len(final.Subagents) != 0 {
			t.Errorf("Subagents = %+v, want empty (opencode UserPromptSubmit must not attach a ref)", final.Subagents)
		}
		l2Reasons := map[string]struct{}{
			"proxy_subagent_attached_on_user_prompt":  {},
			"proxy_subagent_upserted_on_user_prompt":  {},
			"proxy_subagent_detached_on_stop_turn":    {},
			"proxy_subagent_stop_no_match":            {},
			"proxy_subagent_stop_parse_failed":        {},
			"pre_tool_without_proxy_parent_skipped":   {},
		}
		if _, isL2 := l2Reasons[meta.Reason]; isL2 {
			t.Errorf("meta.Reason = %q, must not be in L2 vocabulary for opencode UserPromptSubmit", meta.Reason)
		}
	})

	// Row 17b: cc UserPromptSubmit on existing cc frame (AgentType != codex)
	// → must break early; existing narrow UpdateHookPath runs. Subagents
	// unchanged; no SourceTurnID written.
	t.Run("row17b_cc_user_prompt_breaks_early_keeps_existing_subagents", func(t *testing.T) {
		m := newProxyTestModule(t)
		// Seed cc frame for PID=10. UserPromptSubmit lands on this frame.
		f := seedFrame(t, m, "%5", "cc", 10, "t10", 50)
		f.Subagents = []agentpkg.SubagentRef{{
			IsProxy: false, ID: "task-existing", Type: "cc", StartedAt: 50,
		}}
		if _, err := m.frames.Upsert(f); err != nil {
			t.Fatalf("seed cc subagent: %v", err)
		}
		// Must use a process info stub that returns valid info for PID=10
		// (frame != nil path goes through readProcessInfoFn at frame_ops.go:251).
		origInfo := readProcessInfoFn
		readProcessInfoFn = func(pid int) (agentpkg.ProcessInfo, error) {
			return agentpkg.ProcessInfo{PID: pid, PPID: 1}, nil
		}
		t.Cleanup(func() { readProcessInfoFn = origInfo })

		req := EventRequest{
			TmuxSession: "work", TmuxPaneID: "%5",
			PurdexName: "PdxUserPromptSubmit",
			AgentType:  "cc", SenderPID: 10, SenderStartTime: "t10",
			RawEvent: rawTurn("ignored"),
		}
		_, meta, err := m.applyFrameEvent(req, agentpkg.DeriveResult{Valid: true, Status: agentpkg.StatusRunning}, 200)
		if err != nil {
			t.Fatalf("applyFrameEvent: %v", err)
		}
		// New lifecycle case must NOT have surfaced any of the new reasons.
		switch meta.Reason {
		case "proxy_subagent_attached_on_user_prompt",
			"proxy_subagent_upserted_on_user_prompt",
			"pre_tool_without_proxy_parent":
			t.Fatalf("cc UserPromptSubmit ran new lifecycle case; reason=%q", meta.Reason)
		}
		final, err := m.frames.GetByIdentity("%5", 10, "t10")
		if err != nil || final == nil {
			t.Fatalf("reload cc frame: %v / %v", err, final)
		}
		if len(final.Subagents) != 1 || final.Subagents[0].ID != "task-existing" {
			t.Fatalf("Subagents = %+v, want existing native ref preserved", final.Subagents)
		}
		for _, ref := range final.Subagents {
			if ref.SourceTurnID == "ignored" {
				t.Fatalf("cc UserPromptSubmit leaked SourceTurnID=ignored onto ref %+v", ref)
			}
		}
	})

	// Row 2: parent cc + 1 ref(PID=42, t1, turnID="") (SessionStart attached)
	// → PreToolUse from PID=42 raw turn_id="t_a" → shares the same upsert
	// path as UserPromptSubmit; SourceTurnID set to "t_a" in-place.
	t.Run("row02_pre_tool_use_shares_upsert_path", func(t *testing.T) {
		m := newProxyTestModule(t)
		seedProxyRef(t, m, "%5", "cc", 100, "t100", 50, []agentpkg.SubagentRef{{
			ID: "proxy:codex:42:t1", Type: "codex", StartedAt: 50,
			SourcePID: 42, SourceStartTime: "t1", IsProxy: true,
		}})
		turnAwareEnvAlive(t, 100, "t100")

		req := EventRequest{
			TmuxSession: "work", TmuxPaneID: "%5",
			PurdexName: "PdxPreToolUse",
			AgentType:  "codex", SenderPID: 42, SenderStartTime: "t1",
			RawEvent: rawTurn("t_a"),
		}
		// PdxPreToolUse derive returns Status="" Detail-only; passing the
		// equivalent DeriveResult here mirrors the codex deriveCodexStatus
		// case body (catalog wired in P1-T1).
		_, meta, err := m.applyFrameEvent(req, agentpkg.DeriveResult{Valid: true, Detail: map[string]any{}}, 200)
		if err != nil {
			t.Fatalf("applyFrameEvent: %v", err)
		}
		if meta.Reason != "proxy_subagent_upserted_on_user_prompt" {
			t.Fatalf("reason = %q, want proxy_subagent_upserted_on_user_prompt (PreToolUse shares path)", meta.Reason)
		}
		final, _ := m.frames.GetByIdentity("%5", 100, "t100")
		if final == nil || len(final.Subagents) != 1 {
			t.Fatalf("Subagents = %+v, want exactly 1 ref after PreToolUse upsert", final.Subagents)
		}
		if final.Subagents[0].SourceTurnID != "t_a" {
			t.Fatalf("ref.SourceTurnID = %q, want t_a", final.Subagents[0].SourceTurnID)
		}
	})

	// Row 20: pane has 0 frames + 0 refs → PreToolUse from PID=42 with no
	// proxy parent (findProxyParent returns nil), AgentType=codex → new
	// lifecycle case must return early with Reason="pre_tool_without_proxy_parent",
	// NOT fall through to the generic frame-create path. (Spec §3.3.C.1
	// + §5 row 20 — PreToolUse Status="" would otherwise materialize a
	// standalone idle frame.)
	t.Run("row20_pre_tool_no_parent_skips_no_frame_created", func(t *testing.T) {
		m := newProxyTestModule(t)
		// No seeded frames; pane is empty.
		turnAwareEnvNoParent(t)

		// Snapshot frame count before.
		before, err := m.frames.ListByPane("%5")
		if err != nil {
			t.Fatalf("ListByPane before: %v", err)
		}
		if len(before) != 0 {
			t.Fatalf("pane has %d frames before, want 0", len(before))
		}

		req := EventRequest{
			TmuxSession: "work", TmuxPaneID: "%5",
			PurdexName: "PdxPreToolUse",
			AgentType:  "codex", SenderPID: 42, SenderStartTime: "t1",
			RawEvent: rawTurn("t_a"),
		}
		_, meta, err := m.applyFrameEvent(req, agentpkg.DeriveResult{Valid: true, Detail: map[string]any{}}, 200)
		if err != nil {
			t.Fatalf("applyFrameEvent: %v", err)
		}
		if meta.Decision != "skipped" || meta.Reason != "pre_tool_without_proxy_parent" {
			t.Fatalf("meta = %+v, want Decision=skipped Reason=pre_tool_without_proxy_parent", meta)
		}

		// Strict: m.frames count unchanged, no new standalone frame.
		after, err := m.frames.ListByPane("%5")
		if err != nil {
			t.Fatalf("ListByPane after: %v", err)
		}
		if len(after) != 0 {
			t.Fatalf("pane has %d frames after, want 0 (no idle standalone frame); frames=%+v", len(after), after)
		}
		// currentStatus must NOT have been written for this session.
		if _, ok := m.currentStatus["work"]; ok {
			t.Fatalf("currentStatus[work] was written; PreToolUse no-parent must not broadcast status")
		}
	})

	// Row 3: parent cc + 1 ref(PID=42, t1, turnID="t_a")
	// → Stop from PID=42 raw turn_id="t_a", AgentType=codex
	// → targeted detach via removeProxyRefForSenderTurn; trace
	// proxy_subagent_detached_on_stop_turn.
	t.Run("row03_codex_stop_targeted_detach_match", func(t *testing.T) {
		m := newProxyTestModule(t)
		seedProxyRef(t, m, "%5", "cc", 100, "t100", 50, []agentpkg.SubagentRef{{
			ID: "proxy:codex:42:t1", Type: "codex", StartedAt: 50,
			SourcePID: 42, SourceStartTime: "t1", IsProxy: true, SourceTurnID: "t_a",
		}})
		// Stop sender does not need PPID walk (detach is pane-scan), but
		// readProcessInfoFn is invoked once when frame == nil after the L2
		// case breaks (none here — case returns before generic path).
		turnAwareEnvAlive(t, 100, "t100")

		req := EventRequest{
			TmuxSession: "work", TmuxPaneID: "%5",
			PurdexName: "PdxStop",
			AgentType:  "codex", SenderPID: 42, SenderStartTime: "t1",
			RawEvent: rawTurn("t_a"),
		}
		_, meta, err := m.applyFrameEvent(req, agentpkg.DeriveResult{Valid: true, Status: agentpkg.StatusIdle}, 200)
		if err != nil {
			t.Fatalf("applyFrameEvent: %v", err)
		}
		if meta.Reason != "proxy_subagent_detached_on_stop_turn" {
			t.Fatalf("reason = %q, want proxy_subagent_detached_on_stop_turn; meta=%+v", meta.Reason, meta)
		}
		final, _ := m.frames.GetByIdentity("%5", 100, "t100")
		if final == nil || len(final.Subagents) != 0 {
			t.Fatalf("Subagents = %+v, want empty after targeted detach", final.Subagents)
		}
	})

	// Row 4: parent cc + 1 ref(PID=42, t1, turnID="t_a")
	// → Stop from PID=42 raw turn_id="t_b" (turn mismatch)
	// → ref kept; trace proxy_subagent_stop_no_match.
	t.Run("row04_codex_stop_turn_mismatch_keeps_ref", func(t *testing.T) {
		m := newProxyTestModule(t)
		seedProxyRef(t, m, "%5", "cc", 100, "t100", 50, []agentpkg.SubagentRef{{
			ID: "proxy:codex:42:t1", Type: "codex", StartedAt: 50,
			SourcePID: 42, SourceStartTime: "t1", IsProxy: true, SourceTurnID: "t_a",
		}})
		turnAwareEnvAlive(t, 100, "t100")

		req := EventRequest{
			TmuxSession: "work", TmuxPaneID: "%5",
			PurdexName: "PdxStop",
			AgentType:  "codex", SenderPID: 42, SenderStartTime: "t1",
			RawEvent: rawTurn("t_b"),
		}
		_, meta, err := m.applyFrameEvent(req, agentpkg.DeriveResult{Valid: true, Status: agentpkg.StatusIdle}, 200)
		if err != nil {
			t.Fatalf("applyFrameEvent: %v", err)
		}
		if meta.Reason != "proxy_subagent_stop_no_match" {
			t.Fatalf("reason = %q, want proxy_subagent_stop_no_match", meta.Reason)
		}
		final, _ := m.frames.GetByIdentity("%5", 100, "t100")
		if final == nil || len(final.Subagents) != 1 || final.Subagents[0].SourceTurnID != "t_a" {
			t.Fatalf("Subagents = %+v, want unchanged turn t_a", final.Subagents)
		}
	})

	// Row 6: parent cc + 1 ref(PID=42, t1, turnID="t_a")
	// → StopFailure from PID=42 raw turn_id="t_a"
	// → targeted detach (StopFailure parity with Stop).
	t.Run("row06_codex_stop_failure_parity_with_stop", func(t *testing.T) {
		m := newProxyTestModule(t)
		seedProxyRef(t, m, "%5", "cc", 100, "t100", 50, []agentpkg.SubagentRef{{
			ID: "proxy:codex:42:t1", Type: "codex", StartedAt: 50,
			SourcePID: 42, SourceStartTime: "t1", IsProxy: true, SourceTurnID: "t_a",
		}})
		turnAwareEnvAlive(t, 100, "t100")

		req := EventRequest{
			TmuxSession: "work", TmuxPaneID: "%5",
			PurdexName: "PdxStopFailure",
			AgentType:  "codex", SenderPID: 42, SenderStartTime: "t1",
			RawEvent: rawTurn("t_a"),
		}
		_, meta, err := m.applyFrameEvent(req, agentpkg.DeriveResult{Valid: true, Status: agentpkg.StatusIdle}, 200)
		if err != nil {
			t.Fatalf("applyFrameEvent: %v", err)
		}
		if meta.Reason != "proxy_subagent_detached_on_stop_turn" {
			t.Fatalf("reason = %q, want proxy_subagent_detached_on_stop_turn (StopFailure parity)", meta.Reason)
		}
		final, _ := m.frames.GetByIdentity("%5", 100, "t100")
		if final == nil || len(final.Subagents) != 0 {
			t.Fatalf("Subagents = %+v, want empty after StopFailure detach", final.Subagents)
		}
	})

	// Row 8: parent cc + 1 ref(PID=42, t1, turnID="t_a")
	// → Stop from PID=42 with missing turn_id, AgentType=codex
	// → matching broker ref has SourceTurnID="t_a" (non-empty) → SKIP detach;
	// trace proxy_subagent_stop_parse_failed.
	t.Run("row08_codex_stop_parse_failed_skip_with_existing_turn", func(t *testing.T) {
		m := newProxyTestModule(t)
		seedProxyRef(t, m, "%5", "cc", 100, "t100", 50, []agentpkg.SubagentRef{{
			ID: "proxy:codex:42:t1", Type: "codex", StartedAt: 50,
			SourcePID: 42, SourceStartTime: "t1", IsProxy: true, SourceTurnID: "t_a",
		}})
		turnAwareEnvAlive(t, 100, "t100")

		req := EventRequest{
			TmuxSession: "work", TmuxPaneID: "%5",
			PurdexName: "PdxStop",
			AgentType:  "codex", SenderPID: 42, SenderStartTime: "t1",
			RawEvent: rawTurn(""),
		}
		_, meta, err := m.applyFrameEvent(req, agentpkg.DeriveResult{Valid: true, Status: agentpkg.StatusIdle}, 200)
		if err != nil {
			t.Fatalf("applyFrameEvent: %v", err)
		}
		if meta.Reason != "proxy_subagent_stop_parse_failed" {
			t.Fatalf("reason = %q, want proxy_subagent_stop_parse_failed", meta.Reason)
		}
		final, _ := m.frames.GetByIdentity("%5", 100, "t100")
		if final == nil || len(final.Subagents) != 1 || final.Subagents[0].SourceTurnID != "t_a" {
			t.Fatalf("Subagents = %+v, want unchanged (skip detach)", final.Subagents)
		}
	})

	// Row 9: parent cc + 1 ref(PID=42, t1, turnID="") (SessionStart attached
	// but never upserted with turn_id) → Stop from PID=42 with empty
	// turn_id, AgentType=codex → matching broker ref also has empty
	// SourceTurnID → wildcard detach via removeProxyRefForSender; trace
	// proxy_subagent_detached_on_stop.
	t.Run("row09_codex_stop_empty_ref_wildcard_fallback", func(t *testing.T) {
		m := newProxyTestModule(t)
		seedProxyRef(t, m, "%5", "cc", 100, "t100", 50, []agentpkg.SubagentRef{{
			ID: "proxy:codex:42:t1", Type: "codex", StartedAt: 50,
			SourcePID: 42, SourceStartTime: "t1", IsProxy: true,
			// SourceTurnID intentionally empty.
		}})
		turnAwareEnvAlive(t, 100, "t100")

		req := EventRequest{
			TmuxSession: "work", TmuxPaneID: "%5",
			PurdexName: "PdxStop",
			AgentType:  "codex", SenderPID: 42, SenderStartTime: "t1",
			RawEvent: rawTurn(""),
		}
		_, meta, err := m.applyFrameEvent(req, agentpkg.DeriveResult{Valid: true, Status: agentpkg.StatusIdle}, 200)
		if err != nil {
			t.Fatalf("applyFrameEvent: %v", err)
		}
		if meta.Reason != "proxy_subagent_detached_on_stop" {
			t.Fatalf("reason = %q, want proxy_subagent_detached_on_stop", meta.Reason)
		}
		final, _ := m.frames.GetByIdentity("%5", 100, "t100")
		if final == nil || len(final.Subagents) != 0 {
			t.Fatalf("Subagents = %+v, want empty after wildcard detach", final.Subagents)
		}
	})

	// Row 8b (round-2 A1 race regression): parent cc + 1 ref(PID=42, t1,
	// turnID="") races a UserPromptSubmit upsert(turn=t_a) against an empty-
	// turn malformed Stop (case (c) wildcard fallback path). Without the A1
	// fix, the case-(c) wildcard detach (removeProxyRefForSender) could
	// observe empty SourceTurnID at ListByPane time, then drop the upgraded
	// ref under (PID, StartTime) wildcard match between scan and helper.
	// With the fix, case (c) re-issues the detach via
	// removeProxyRefForSenderTurn(..., "") which re-verifies SourceTurnID ==
	// "" inside the optimistic-concurrency loop and bails when the ref has
	// been upgraded to a non-empty turn_id.
	//
	// All race orderings must converge on a stable end state with exactly
	// one ref carrying SourceTurnID="t_a"; never zero refs (would mean the
	// upgraded ref was wildcard-dropped) and never one ref with empty
	// turn_id (would mean upsert silently rolled back).
	t.Run("row08b_concurrent_empty_stop_vs_upsert_upgrade_race", func(t *testing.T) {
		m := newProxyTestModule(t)
		seedProxyRef(t, m, "%5", "cc", 100, "t100", 50, []agentpkg.SubagentRef{{
			ID: "proxy:codex:42:t1", Type: "codex", StartedAt: 50,
			SourcePID: 42, SourceStartTime: "t1", IsProxy: true,
			// SourceTurnID intentionally empty (matches case (c) entry).
		}})
		turnAwareEnvAlive(t, 100, "t100")

		base := EventRequest{
			TmuxSession: "work", TmuxPaneID: "%5",
			AgentType: "codex", SenderPID: 42, SenderStartTime: "t1",
		}
		var wg sync.WaitGroup
		wg.Add(2)
		go func() {
			defer wg.Done()
			req := base
			req.PurdexName = "PdxUserPromptSubmit"
			req.RawEvent = rawTurn("t_a")
			_, _, err := m.applyFrameEvent(req, agentpkg.DeriveResult{Valid: true, Status: agentpkg.StatusRunning}, 200)
			if err != nil {
				t.Errorf("UserPromptSubmit: %v", err)
			}
		}()
		go func() {
			defer wg.Done()
			req := base
			req.PurdexName = "PdxStop"
			req.RawEvent = rawTurn("") // malformed → empty turn_id
			_, _, err := m.applyFrameEvent(req, agentpkg.DeriveResult{Valid: true, Status: agentpkg.StatusIdle}, 200)
			if err != nil {
				t.Errorf("Stop: %v", err)
			}
		}()
		wg.Wait()

		final, _ := m.frames.GetByIdentity("%5", 100, "t100")
		if final == nil {
			t.Fatalf("parent frame missing after race")
		}
		// Forbidden (i): zero refs (would mean wildcard detach dropped the
		// ref AFTER upsert successfully upgraded it — the A1 bug shape).
		if len(final.Subagents) == 0 {
			t.Errorf("forbidden: zero refs after empty-Stop vs upsert race (A1 regression)")
		}
		// Forbidden (ii): two-or-more refs (would mean upsert appended a
		// fresh ref alongside an undeleted upgraded one — F1 regression).
		if len(final.Subagents) >= 2 {
			t.Errorf("forbidden: %d refs, want exactly 1; refs=%+v", len(final.Subagents), final.Subagents)
		}
		// Forbidden (iii): single ref with empty SourceTurnID (would mean
		// upsert's upgrade silently rolled back, leaving the ref reachable
		// only via the now-expanded empty-turn detach contract).
		if len(final.Subagents) == 1 && final.Subagents[0].SourceTurnID == "" {
			t.Errorf("forbidden: single ref retains empty turn_id; ref=%+v", final.Subagents[0])
		}
	})

	// Row 10: parent cc + 1 ref(PID=42, t1, turnID="t_a") + 1 ref(PID=43,
	// t2, turnID="t_x") → Stop from PID=42 raw turn_id="t_a"
	// → only PID=42 ref dropped; PID=43 untouched.
	t.Run("row10_codex_stop_multi_broker_isolation", func(t *testing.T) {
		m := newProxyTestModule(t)
		seedProxyRef(t, m, "%5", "cc", 100, "t100", 50, []agentpkg.SubagentRef{
			{ID: "proxy:codex:42:t1", Type: "codex", StartedAt: 50,
				SourcePID: 42, SourceStartTime: "t1", IsProxy: true, SourceTurnID: "t_a"},
			{ID: "proxy:codex:43:t2", Type: "codex", StartedAt: 50,
				SourcePID: 43, SourceStartTime: "t2", IsProxy: true, SourceTurnID: "t_x"},
		})
		turnAwareEnvAlive(t, 100, "t100")

		req := EventRequest{
			TmuxSession: "work", TmuxPaneID: "%5",
			PurdexName: "PdxStop",
			AgentType:  "codex", SenderPID: 42, SenderStartTime: "t1",
			RawEvent: rawTurn("t_a"),
		}
		_, meta, err := m.applyFrameEvent(req, agentpkg.DeriveResult{Valid: true, Status: agentpkg.StatusIdle}, 200)
		if err != nil {
			t.Fatalf("applyFrameEvent: %v", err)
		}
		if meta.Reason != "proxy_subagent_detached_on_stop_turn" {
			t.Fatalf("reason = %q, want proxy_subagent_detached_on_stop_turn", meta.Reason)
		}
		final, _ := m.frames.GetByIdentity("%5", 100, "t100")
		if final == nil || len(final.Subagents) != 1 {
			t.Fatalf("Subagents count = %d, want 1 (PID=43 ref kept); refs=%+v", len(final.Subagents), final.Subagents)
		}
		if final.Subagents[0].SourcePID != 43 || final.Subagents[0].SourceTurnID != "t_x" {
			t.Fatalf("surviving ref = %+v, want PID=43 turnID=t_x", final.Subagents[0])
		}
	})

	// Mixed-broker H1/v2 fix: parent cc + 1 ref(PID=42, t1, turnID="") +
	// 1 ref(PID=43, t2, turnID="t_x") → Stop from PID=42 with empty turn_id
	// → matching broker (PID=42) has empty SourceTurnID → wildcard detach
	// PID=42 ref. Must NOT be misled by PID=43's turnID="t_x" into the
	// parse_failed skip path.
	t.Run("row09b_codex_stop_mixed_broker_uses_matching_ref_only", func(t *testing.T) {
		m := newProxyTestModule(t)
		seedProxyRef(t, m, "%5", "cc", 100, "t100", 50, []agentpkg.SubagentRef{
			{ID: "proxy:codex:42:t1", Type: "codex", StartedAt: 50,
				SourcePID: 42, SourceStartTime: "t1", IsProxy: true /* SourceTurnID="" */},
			{ID: "proxy:codex:43:t2", Type: "codex", StartedAt: 50,
				SourcePID: 43, SourceStartTime: "t2", IsProxy: true, SourceTurnID: "t_x"},
		})
		turnAwareEnvAlive(t, 100, "t100")

		req := EventRequest{
			TmuxSession: "work", TmuxPaneID: "%5",
			PurdexName: "PdxStop",
			AgentType:  "codex", SenderPID: 42, SenderStartTime: "t1",
			RawEvent: rawTurn(""),
		}
		_, meta, err := m.applyFrameEvent(req, agentpkg.DeriveResult{Valid: true, Status: agentpkg.StatusIdle}, 200)
		if err != nil {
			t.Fatalf("applyFrameEvent: %v", err)
		}
		if meta.Reason != "proxy_subagent_detached_on_stop" {
			t.Fatalf("reason = %q, want proxy_subagent_detached_on_stop (matching broker has empty turn → wildcard)", meta.Reason)
		}
		final, _ := m.frames.GetByIdentity("%5", 100, "t100")
		if final == nil || len(final.Subagents) != 1 {
			t.Fatalf("Subagents count = %d, want 1 (PID=43 kept); refs=%+v", len(final.Subagents), final.Subagents)
		}
		if final.Subagents[0].SourcePID != 43 {
			t.Fatalf("surviving ref PID = %d, want 43", final.Subagents[0].SourcePID)
		}
	})

	// Row 11: parent opencode + 1 ref(PID=42, t1, turnID="", Type=cc)
	// (cc as proxy under opencode parent — legitimate cross-type case via
	// existing PR-2b proxy collapse) → SessionEnd from PID=42, AgentType=cc
	// → wildcard detach via existing removeProxyRefForSender (SessionEnd
	// path unchanged; T8a does NOT add a SessionEnd handler).
	t.Run("row11_session_end_cross_type_cc_under_opencode_unchanged", func(t *testing.T) {
		m := newProxyTestModule(t)
		// Register opencode provider so registry resolves the parent type.
		m.registry.Register(&fakeAgentProvider{
			typeName: "opencode",
			derive:   func(string, json.RawMessage) agentpkg.DeriveResult { return agentpkg.DeriveResult{Valid: true, Status: agentpkg.StatusIdle} },
		})
		seedProxyRef(t, m, "%5", "opencode", 100, "t100", 50, []agentpkg.SubagentRef{{
			ID: "proxy:cc:42:t1", Type: "cc", StartedAt: 50,
			SourcePID: 42, SourceStartTime: "t1", IsProxy: true,
		}})
		// Seam: SessionEnd path doesn't traverse PPID for detach (it's
		// pane-scan). readProcessInfoFn still needs to satisfy generic
		// post-switch code if SessionEnd hits frame == nil branch.
		origInfo := readProcessInfoFn
		readProcessInfoFn = func(pid int) (agentpkg.ProcessInfo, error) {
			return agentpkg.ProcessInfo{PID: pid, PPID: 1}, nil
		}
		t.Cleanup(func() { readProcessInfoFn = origInfo })

		req := EventRequest{
			TmuxSession: "work", TmuxPaneID: "%5",
			PurdexName: "PdxSessionEnd",
			AgentType:  "cc", SenderPID: 42, SenderStartTime: "t1",
			RawEvent: json.RawMessage(`{}`),
		}
		_, meta, err := m.applyFrameEvent(req, agentpkg.DeriveResult{Valid: true, Status: agentpkg.StatusClear}, 200)
		if err != nil {
			t.Fatalf("applyFrameEvent: %v", err)
		}
		// Existing SessionEnd path's reason for cross-type proxy detach.
		if meta.Reason != "proxy_subagent_detached" {
			t.Fatalf("reason = %q, want proxy_subagent_detached (existing SessionEnd path)", meta.Reason)
		}
		final, _ := m.frames.GetByIdentity("%5", 100, "t100")
		if final == nil || len(final.Subagents) != 0 {
			t.Fatalf("Subagents = %+v, want empty after SessionEnd wildcard detach", final.Subagents)
		}
	})

	// Row 12: sender owns own frame + Stop → frame.Subagents unchanged,
	// no proxy detach attempt. (Spec §3.3.D `frame != nil` short-circuit.)
	t.Run("row12_stop_sender_owns_own_frame_short_circuit", func(t *testing.T) {
		m := newProxyTestModule(t)
		// Sender PID=42 owns its own codex frame; frame.Subagents has 1
		// native ref (must remain untouched).
		f := seedFrame(t, m, "%5", "codex", 42, "t1", 50)
		f.Subagents = []agentpkg.SubagentRef{{
			IsProxy: false, ID: "task-existing", Type: "codex", StartedAt: 50,
		}}
		if _, err := m.frames.Upsert(f); err != nil {
			t.Fatalf("seed sender frame: %v", err)
		}
		origInfo := readProcessInfoFn
		readProcessInfoFn = func(pid int) (agentpkg.ProcessInfo, error) {
			return agentpkg.ProcessInfo{PID: pid, PPID: 1}, nil
		}
		t.Cleanup(func() { readProcessInfoFn = origInfo })

		req := EventRequest{
			TmuxSession: "work", TmuxPaneID: "%5",
			PurdexName: "PdxStop",
			AgentType:  "codex", SenderPID: 42, SenderStartTime: "t1",
			RawEvent: rawTurn("t_a"),
		}
		_, meta, err := m.applyFrameEvent(req, agentpkg.DeriveResult{Valid: true, Status: agentpkg.StatusIdle}, 200)
		if err != nil {
			t.Fatalf("applyFrameEvent: %v", err)
		}
		// New L2 stop reasons must NOT have surfaced — short-circuit via
		// `frame != nil` lets the existing generic post-switch path run.
		switch meta.Reason {
		case "proxy_subagent_detached_on_stop_turn",
			"proxy_subagent_detached_on_stop",
			"proxy_subagent_stop_no_match",
			"proxy_subagent_stop_parse_failed":
			t.Fatalf("Stop ran new L2 detach path on sender's own frame; reason=%q", meta.Reason)
		}
		final, _ := m.frames.GetByIdentity("%5", 42, "t1")
		if final == nil || len(final.Subagents) != 1 || final.Subagents[0].ID != "task-existing" {
			t.Fatalf("Subagents = %+v, want native ref preserved", final.Subagents)
		}
	})

	// Row 13: parent cc + 1 native ref(IsProxy=false, ID="task-x") with
	// PID/turnID strings that COINCIDENTALLY match the Stop's identity
	// → native ref must NOT be dropped. (Cross-namespace isolation —
	// removeProxyRefForSenderTurn / subagentsContainProxySenderTurn are
	// already gated on IsProxy=true; this row confirms the case body
	// doesn't bypass that gate via some other path.)
	t.Run("row13_stop_native_ref_isolation", func(t *testing.T) {
		m := newProxyTestModule(t)
		seedProxyRef(t, m, "%5", "cc", 100, "t100", 50, []agentpkg.SubagentRef{{
			IsProxy: false, ID: "task-x", Type: "cc", StartedAt: 50,
			SourcePID: 42, SourceStartTime: "t1", SourceTurnID: "t_a",
		}})
		turnAwareEnvAlive(t, 100, "t100")

		req := EventRequest{
			TmuxSession: "work", TmuxPaneID: "%5",
			PurdexName: "PdxStop",
			AgentType:  "codex", SenderPID: 42, SenderStartTime: "t1",
			RawEvent: rawTurn("t_a"),
		}
		_, meta, err := m.applyFrameEvent(req, agentpkg.DeriveResult{Valid: true, Status: agentpkg.StatusIdle}, 200)
		if err != nil {
			t.Fatalf("applyFrameEvent: %v", err)
		}
		// turnID != "" path → removeProxyRefForSenderTurn IsProxy gate
		// prevents drop → trace stop_no_match.
		if meta.Reason != "proxy_subagent_stop_no_match" {
			t.Fatalf("reason = %q, want proxy_subagent_stop_no_match (native ref must not match)", meta.Reason)
		}
		final, _ := m.frames.GetByIdentity("%5", 100, "t100")
		if final == nil || len(final.Subagents) != 1 || final.Subagents[0].ID != "task-x" {
			t.Fatalf("Subagents = %+v, want native ref preserved", final.Subagents)
		}
	})

	// Row 18: cc Stop with no turn_id in raw, ref has empty SourceTurnID
	// → wildcard detach via §3.3.D non-codex fallback (process-level
	// removeProxyRefForSender).
	t.Run("row18_cc_stop_wildcard_fallback", func(t *testing.T) {
		m := newProxyTestModule(t)
		// Parent opencode + cross-type cc proxy ref (legitimate cc-as-
		// proxy-under-opencode shape from PR-2b).
		m.registry.Register(&fakeAgentProvider{
			typeName: "opencode",
			derive:   func(string, json.RawMessage) agentpkg.DeriveResult { return agentpkg.DeriveResult{Valid: true, Status: agentpkg.StatusIdle} },
		})
		seedProxyRef(t, m, "%5", "opencode", 100, "t100", 50, []agentpkg.SubagentRef{{
			ID: "proxy:cc:42:t1", Type: "cc", StartedAt: 50,
			SourcePID: 42, SourceStartTime: "t1", IsProxy: true,
			// SourceTurnID intentionally empty (cc never populates it).
		}})
		origInfo := readProcessInfoFn
		readProcessInfoFn = func(pid int) (agentpkg.ProcessInfo, error) {
			return agentpkg.ProcessInfo{PID: pid, PPID: 1}, nil
		}
		t.Cleanup(func() { readProcessInfoFn = origInfo })

		req := EventRequest{
			TmuxSession: "work", TmuxPaneID: "%5",
			PurdexName: "PdxStop",
			AgentType:  "cc", SenderPID: 42, SenderStartTime: "t1",
			RawEvent: json.RawMessage(`{}`),
		}
		_, meta, err := m.applyFrameEvent(req, agentpkg.DeriveResult{Valid: true, Status: agentpkg.StatusIdle}, 200)
		if err != nil {
			t.Fatalf("applyFrameEvent: %v", err)
		}
		if meta.Reason != "proxy_subagent_detached_on_stop" {
			t.Fatalf("reason = %q, want proxy_subagent_detached_on_stop (cc wildcard fallback)", meta.Reason)
		}
		final, _ := m.frames.GetByIdentity("%5", 100, "t100")
		if final == nil || len(final.Subagents) != 0 {
			t.Fatalf("Subagents = %+v, want empty after cc Stop wildcard detach", final.Subagents)
		}
	})

	// Row 18b (round-2 D2): opencode Stop with no turn_id in raw, ref has
	// empty SourceTurnID → wildcard detach via §3.3.D non-codex fallback
	// (case (b) at frame_ops.go:289). Pins the opencode Stop branch
	// explicitly — row 11 covers cc SessionEnd under opencode parent and
	// row 18 covers cc Stop, but the AgentType=opencode + Stop combination
	// had no dedicated row before this.
	t.Run("row18b_opencode_stop_wildcard_fallback", func(t *testing.T) {
		m := newProxyTestModule(t)
		// Parent cc + cross-type opencode proxy ref (legitimate
		// opencode-under-cc shape from PR-2b).
		m.registry.Register(&fakeAgentProvider{
			typeName: "opencode",
			derive:   func(string, json.RawMessage) agentpkg.DeriveResult { return agentpkg.DeriveResult{Valid: true, Status: agentpkg.StatusIdle} },
		})
		seedProxyRef(t, m, "%5", "cc", 100, "t100", 50, []agentpkg.SubagentRef{{
			ID: "proxy:opencode:42:t1", Type: "opencode", StartedAt: 50,
			SourcePID: 42, SourceStartTime: "t1", IsProxy: true,
			// SourceTurnID intentionally empty (opencode never populates it).
		}})
		origInfo := readProcessInfoFn
		readProcessInfoFn = func(pid int) (agentpkg.ProcessInfo, error) {
			return agentpkg.ProcessInfo{PID: pid, PPID: 1}, nil
		}
		t.Cleanup(func() { readProcessInfoFn = origInfo })

		req := EventRequest{
			TmuxSession: "work", TmuxPaneID: "%5",
			PurdexName: "PdxStop",
			AgentType:  "opencode", SenderPID: 42, SenderStartTime: "t1",
			RawEvent: json.RawMessage(`{}`),
		}
		_, meta, err := m.applyFrameEvent(req, agentpkg.DeriveResult{Valid: true, Status: agentpkg.StatusIdle}, 200)
		if err != nil {
			t.Fatalf("applyFrameEvent: %v", err)
		}
		if meta.Reason != "proxy_subagent_detached_on_stop" {
			t.Fatalf("reason = %q, want proxy_subagent_detached_on_stop (opencode wildcard fallback)", meta.Reason)
		}
		final, _ := m.frames.GetByIdentity("%5", 100, "t100")
		if final == nil || len(final.Subagents) != 0 {
			t.Fatalf("Subagents = %+v, want empty after opencode Stop wildcard detach", final.Subagents)
		}
	})

	// Row 14: parent cc + 1 ref(PID=42, t1, turnID="t_a"). Sequential:
	// Stop(t_a) → Stop(t_a) again → call 1 detached, call 2 no-op trace
	// stop_no_match (idempotent / retry-safe).
	t.Run("row14_codex_stop_idempotent_second_call_no_match", func(t *testing.T) {
		m := newProxyTestModule(t)
		seedProxyRef(t, m, "%5", "cc", 100, "t100", 50, []agentpkg.SubagentRef{{
			ID: "proxy:codex:42:t1", Type: "codex", StartedAt: 50,
			SourcePID: 42, SourceStartTime: "t1", IsProxy: true, SourceTurnID: "t_a",
		}})
		turnAwareEnvAlive(t, 100, "t100")

		req := EventRequest{
			TmuxSession: "work", TmuxPaneID: "%5",
			PurdexName: "PdxStop",
			AgentType:  "codex", SenderPID: 42, SenderStartTime: "t1",
			RawEvent: rawTurn("t_a"),
		}
		_, meta1, err := m.applyFrameEvent(req, agentpkg.DeriveResult{Valid: true, Status: agentpkg.StatusIdle}, 200)
		if err != nil {
			t.Fatalf("Stop call 1: %v", err)
		}
		if meta1.Reason != "proxy_subagent_detached_on_stop_turn" {
			t.Fatalf("call 1 reason = %q, want proxy_subagent_detached_on_stop_turn", meta1.Reason)
		}

		_, meta2, err := m.applyFrameEvent(req, agentpkg.DeriveResult{Valid: true, Status: agentpkg.StatusIdle}, 300)
		if err != nil {
			t.Fatalf("Stop call 2: %v", err)
		}
		if meta2.Reason != "proxy_subagent_stop_no_match" {
			t.Fatalf("call 2 reason = %q, want proxy_subagent_stop_no_match (idempotent no-op)", meta2.Reason)
		}
		final, _ := m.frames.GetByIdentity("%5", 100, "t100")
		if final == nil || len(final.Subagents) != 0 {
			t.Fatalf("Subagents = %+v, want empty after both Stops", final.Subagents)
		}
	})

	// Row 15: parent cc + 1 ref(PID=42, t1, turnID="") (SessionStart attached).
	// Sequential lifecycle: UserPromptSubmit(t_a) → PreToolUse(t_a) ×2 → Stop(t_a).
	// Verifies the full attach → upsert → upsert(idempotent) → detach pipeline
	// stays at exactly 1 ref through every middle step and lands at 0 after Stop.
	t.Run("row15_codex_full_lifecycle_sequential", func(t *testing.T) {
		m := newProxyTestModule(t)
		seedProxyRef(t, m, "%5", "cc", 100, "t100", 50, []agentpkg.SubagentRef{{
			ID: "proxy:codex:42:t1", Type: "codex", StartedAt: 50,
			SourcePID: 42, SourceStartTime: "t1", IsProxy: true,
		}})
		turnAwareEnvAlive(t, 100, "t100")

		base := EventRequest{
			TmuxSession: "work", TmuxPaneID: "%5",
			AgentType: "codex", SenderPID: 42, SenderStartTime: "t1",
			RawEvent: rawTurn("t_a"),
		}

		steps := []struct {
			name       string
			purdex     string
			result     agentpkg.DeriveResult
			ts         int64
			wantReason string
			wantRefs   int
			wantTurnID string
		}{
			{"user_prompt", "PdxUserPromptSubmit", agentpkg.DeriveResult{Valid: true, Status: agentpkg.StatusRunning}, 100, "proxy_subagent_upserted_on_user_prompt", 1, "t_a"},
			{"pre_tool_1", "PdxPreToolUse", agentpkg.DeriveResult{Valid: true, Detail: map[string]any{}}, 200, "proxy_subagent_upserted_on_user_prompt", 1, "t_a"},
			{"pre_tool_2", "PdxPreToolUse", agentpkg.DeriveResult{Valid: true, Detail: map[string]any{}}, 300, "proxy_subagent_upserted_on_user_prompt", 1, "t_a"},
		}
		for _, step := range steps {
			req := base
			req.PurdexName = step.purdex
			_, meta, err := m.applyFrameEvent(req, step.result, step.ts)
			if err != nil {
				t.Fatalf("step %s: %v", step.name, err)
			}
			if meta.Reason != step.wantReason {
				t.Fatalf("step %s reason = %q, want %q", step.name, meta.Reason, step.wantReason)
			}
			final, _ := m.frames.GetByIdentity("%5", 100, "t100")
			if final == nil || len(final.Subagents) != step.wantRefs {
				t.Fatalf("step %s Subagents count = %d, want %d; refs=%+v", step.name, len(final.Subagents), step.wantRefs, final.Subagents)
			}
			if final.Subagents[0].SourceTurnID != step.wantTurnID {
				t.Fatalf("step %s ref.SourceTurnID = %q, want %q", step.name, final.Subagents[0].SourceTurnID, step.wantTurnID)
			}
		}

		// Final Stop(t_a) → targeted detach.
		stopReq := base
		stopReq.PurdexName = "PdxStop"
		_, stopMeta, err := m.applyFrameEvent(stopReq, agentpkg.DeriveResult{Valid: true, Status: agentpkg.StatusIdle}, 400)
		if err != nil {
			t.Fatalf("Stop step: %v", err)
		}
		if stopMeta.Reason != "proxy_subagent_detached_on_stop_turn" {
			t.Fatalf("Stop reason = %q, want proxy_subagent_detached_on_stop_turn", stopMeta.Reason)
		}
		final, _ := m.frames.GetByIdentity("%5", 100, "t100")
		if final == nil || len(final.Subagents) != 0 {
			t.Fatalf("Subagents after Stop = %+v, want empty", final.Subagents)
		}
	})

	// Row 15b: 3-goroutine same-turn upsert race. parent cc + 1 ref(PID=42,
	// t1, turnID=""). Three concurrent calls — UserPromptSubmit(t_a) +
	// PreToolUse(t_a) + PreToolUse(t_a) — must converge to exactly one
	// ref with turnID="t_a". Forbidden final states asserted independently
	// (no `Equal` shortcut that conflates outcomes).
	t.Run("row15b_concurrent_same_turn_upsert_race", func(t *testing.T) {
		m := newProxyTestModule(t)
		seedProxyRef(t, m, "%5", "cc", 100, "t100", 50, []agentpkg.SubagentRef{{
			ID: "proxy:codex:42:t1", Type: "codex", StartedAt: 50,
			SourcePID: 42, SourceStartTime: "t1", IsProxy: true,
		}})
		turnAwareEnvAlive(t, 100, "t100")

		base := EventRequest{
			TmuxSession: "work", TmuxPaneID: "%5",
			AgentType: "codex", SenderPID: 42, SenderStartTime: "t1",
			RawEvent: rawTurn("t_a"),
		}
		callers := []struct {
			purdex string
			result agentpkg.DeriveResult
			ts     int64
		}{
			{"PdxUserPromptSubmit", agentpkg.DeriveResult{Valid: true, Status: agentpkg.StatusRunning}, 100},
			{"PdxPreToolUse", agentpkg.DeriveResult{Valid: true, Detail: map[string]any{}}, 200},
			{"PdxPreToolUse", agentpkg.DeriveResult{Valid: true, Detail: map[string]any{}}, 300},
		}
		var wg sync.WaitGroup
		for _, c := range callers {
			wg.Add(1)
			go func(c struct {
				purdex string
				result agentpkg.DeriveResult
				ts     int64
			}) {
				defer wg.Done()
				req := base
				req.PurdexName = c.purdex
				_, _, err := m.applyFrameEvent(req, c.result, c.ts)
				if err != nil {
					// Don't fail from the goroutine; record via t.Errorf
					// from the callsite is unsafe. Instead use t.Errorf
					// (testing.T.Errorf is goroutine-safe per docs).
					t.Errorf("applyFrameEvent: %v", err)
				}
			}(c)
		}
		wg.Wait()

		final, _ := m.frames.GetByIdentity("%5", 100, "t100")
		if final == nil {
			t.Fatalf("parent frame missing after race")
		}
		// Forbidden state (i): ≥ 2 refs.
		if len(final.Subagents) >= 2 {
			t.Errorf("forbidden: Subagents count = %d, want exactly 1; refs=%+v", len(final.Subagents), final.Subagents)
		}
		// Forbidden state (ii): 0 refs.
		if len(final.Subagents) == 0 {
			t.Errorf("forbidden: Subagents empty after concurrent upsert; want exactly 1 ref")
		}
		// Forbidden state (iii): ref with turnID != "t_a".
		if len(final.Subagents) == 1 && final.Subagents[0].SourceTurnID != "t_a" {
			t.Errorf("forbidden: ref.SourceTurnID = %q, want t_a; ref=%+v", final.Subagents[0].SourceTurnID, final.Subagents[0])
		}
	})

	// Row 16: parent cc + 1 ref(PID=42, t1, turnID="t_a"). Concurrent:
	// goroutine A UserPromptSubmit(t_b), goroutine B Stop(t_a). Two valid
	// final states (per spec §5 row 16). Forbidden states asserted
	// independently.
	t.Run("row16_concurrent_turn_change_vs_stop_race", func(t *testing.T) {
		m := newProxyTestModule(t)
		seedProxyRef(t, m, "%5", "cc", 100, "t100", 50, []agentpkg.SubagentRef{{
			ID: "proxy:codex:42:t1", Type: "codex", StartedAt: 50,
			SourcePID: 42, SourceStartTime: "t1", IsProxy: true, SourceTurnID: "t_a",
		}})
		turnAwareEnvAlive(t, 100, "t100")

		base := EventRequest{
			TmuxSession: "work", TmuxPaneID: "%5",
			AgentType: "codex", SenderPID: 42, SenderStartTime: "t1",
		}
		var wg sync.WaitGroup
		wg.Add(2)
		go func() {
			defer wg.Done()
			req := base
			req.PurdexName = "PdxUserPromptSubmit"
			req.RawEvent = rawTurn("t_b")
			_, _, err := m.applyFrameEvent(req, agentpkg.DeriveResult{Valid: true, Status: agentpkg.StatusRunning}, 200)
			if err != nil {
				t.Errorf("UserPromptSubmit: %v", err)
			}
		}()
		go func() {
			defer wg.Done()
			req := base
			req.PurdexName = "PdxStop"
			req.RawEvent = rawTurn("t_a")
			_, _, err := m.applyFrameEvent(req, agentpkg.DeriveResult{Valid: true, Status: agentpkg.StatusIdle}, 200)
			if err != nil {
				t.Errorf("Stop: %v", err)
			}
		}()
		wg.Wait()

		final, _ := m.frames.GetByIdentity("%5", 100, "t100")
		if final == nil {
			t.Fatalf("parent frame missing after race")
		}
		// Independent forbidden state assertions (NOT joined via single
		// Equal; spec §5 row 16 + §5.1 explicit requirement).
		// Forbidden (i): zero refs.
		if len(final.Subagents) == 0 {
			t.Errorf("forbidden: zero refs after concurrent attach+detach race")
		}
		// Forbidden (ii): 2+ refs.
		if len(final.Subagents) >= 2 {
			t.Errorf("forbidden: %d refs, want exactly 1; refs=%+v", len(final.Subagents), final.Subagents)
		}
		// Forbidden (iii): single ref with turnID = "t_a" (Stop's turn,
		// neither UserPromptSubmit-overwritten t_b nor a fresh attach
		// after detach).
		if len(final.Subagents) == 1 && final.Subagents[0].SourceTurnID == "t_a" {
			t.Errorf("forbidden: single ref retains stale turnID=t_a; ref=%+v", final.Subagents[0])
		}
	})

	// Row 19: parent cc + 1 ref(PID=42, t1, turnID="t_a"). Stop arrives
	// from PID=42 with stale SourceStartTime=t_OLD (PID-reuse scenario).
	// Ref kept (StartTime mismatch); trace stop_no_match.
	t.Run("row19_codex_stop_pid_reuse_stale_start_time", func(t *testing.T) {
		m := newProxyTestModule(t)
		seedProxyRef(t, m, "%5", "cc", 100, "t100", 50, []agentpkg.SubagentRef{{
			ID: "proxy:codex:42:t1", Type: "codex", StartedAt: 50,
			SourcePID: 42, SourceStartTime: "t1", IsProxy: true, SourceTurnID: "t_a",
		}})
		turnAwareEnvAlive(t, 100, "t100")

		req := EventRequest{
			TmuxSession: "work", TmuxPaneID: "%5",
			PurdexName: "PdxStop",
			AgentType:  "codex", SenderPID: 42, SenderStartTime: "t_OLD",
			RawEvent: rawTurn("t_a"),
		}
		_, meta, err := m.applyFrameEvent(req, agentpkg.DeriveResult{Valid: true, Status: agentpkg.StatusIdle}, 200)
		if err != nil {
			t.Fatalf("applyFrameEvent: %v", err)
		}
		if meta.Reason != "proxy_subagent_stop_no_match" {
			t.Fatalf("reason = %q, want proxy_subagent_stop_no_match (PID-reuse stale)", meta.Reason)
		}
		final, _ := m.frames.GetByIdentity("%5", 100, "t100")
		if final == nil || len(final.Subagents) != 1 {
			t.Fatalf("Subagents = %+v, want unchanged (PID-reuse safety)", final.Subagents)
		}
		if final.Subagents[0].SourceTurnID != "t_a" || final.Subagents[0].SourceStartTime != "t1" {
			t.Fatalf("ref drifted: %+v, want PID=42 StartTime=t1 turnID=t_a", final.Subagents[0])
		}
	})
}
