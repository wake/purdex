# sweep:pid_dead Broadcast Clear-Status Hotfix — Implementation Plan

- **Spec**: `docs/specs/2026-04-29-sweep-pid-dead-broadcast-clear-spec.md`
- **Date**: 2026-04-29
- **Base**: `7463883f` (main @ alpha.249)
- **Worktree**: `.claude/worktrees/sweep-pid-dead-broadcast`
- **Branch**: `worktree-sweep-pid-dead-broadcast`
- **Tracking**: #717
- **Plan revision**: v1.1 (2026-04-29) — incorporates codex plan review job `task-moizmm1e-zakq8b` (1 P2 + 1 P3, 0 P0/P1, both addressed).

## 1. Scope summary

Production change is a 3-line guard inside an existing 22-line
function. Tests are split between unit-level (invariant on
`buildProjectionNormalized`) and integration-level (sweep pipeline
broadcast).

| File | Action | Lines |
|------|--------|-------|
| `internal/module/agent/frame_ops.go` | Edit `projection == nil` branch in `buildProjectionNormalized` (line 659) — add `if normalized.Status == "" { normalized.Status = string(agentpkg.StatusClear) }` guard | +3 / -0 |
| `internal/module/agent/frame_ops_test.go` | Append `TestBuildProjectionNormalized_NilProjectionGuard` covering the four invariant states (spec §5.2) | ~ +90 |
| `internal/module/agent/sweep_test.go` | Append `TestSweep_PidDead_BroadcastsClearWhenSessionEmpties` and `TestSweep_PidDead_PreservesSiblingStatus` (spec §5.3) | ~ +160 |
| `docs/specs/2026-04-29-sweep-pid-dead-broadcast-clear-spec.md` | Already in tree (commits `882ade35` + `9bf62824`) | — |
| `docs/specs/2026-04-29-sweep-pid-dead-broadcast-clear-plan.md` | This file | +(this) |

No other production files touched. No SPA changes.

## 2. Phase split — single phase, 4 ordered TDD tasks

### Task T1 — Add failing unit tests on `buildProjectionNormalized` (TDD red)

**Files:** `internal/module/agent/frame_ops_test.go`

**What:**

Append a single table-driven test
`TestBuildProjectionNormalized_NilProjectionGuard` covering the four
states from spec §5.2:

| sub-test | projection | TopFrame | result.Status | Expected `Status` | Notes |
|----------|-----------|----------|---------------|-------------------|-------|
| `nil_projection_empty_status` | nil | — | `""` | `"clear"` | New guard fires; the bug fix |
| `nil_projection_non_empty_status` | nil | — | `"running"` | `"running"` | Guard skipped; protects handler.go:211 |
| `non_nil_projection_no_top_frame` | non-nil | nil | `""` | `"clear"` | Branch [B] unchanged |
| `non_nil_projection_with_top_frame` | non-nil | non-nil (`StatusRunning`, `AgentType: "cc"`) | `""` | `"running"` | Branch [C] unchanged; also asserts `AgentType` is overridden from TopFrame |

Implementation:

```go
func TestBuildProjectionNormalized_NilProjectionGuard(t *testing.T) {
    cases := []struct {
        name        string
        projection  *SessionProjection
        result      agentpkg.DeriveResult
        wantStatus  string
        wantAgent   string
    }{
        {
            name:       "nil_projection_empty_status",
            projection: nil,
            result:     agentpkg.DeriveResult{},
            wantStatus: string(agentpkg.StatusClear),
            wantAgent:  "fallback",
        },
        {
            name:       "nil_projection_non_empty_status",
            projection: nil,
            result:     agentpkg.DeriveResult{Status: agentpkg.StatusRunning},
            wantStatus: string(agentpkg.StatusRunning),
            wantAgent:  "fallback",
        },
        {
            name:       "non_nil_projection_no_top_frame",
            projection: &SessionProjection{},
            result:     agentpkg.DeriveResult{},
            wantStatus: string(agentpkg.StatusClear),
            wantAgent:  "fallback",
        },
        {
            name: "non_nil_projection_with_top_frame",
            projection: &SessionProjection{
                TopFrame: &store.Frame{AgentType: "cc", Status: agentpkg.StatusRunning},
            },
            result:     agentpkg.DeriveResult{},
            wantStatus: string(agentpkg.StatusRunning),
            wantAgent:  "cc",
        },
    }
    for _, tc := range cases {
        t.Run(tc.name, func(t *testing.T) {
            got := buildProjectionNormalized(tc.projection, "fallback", "test_event", 0, tc.result)
            if got.Status != tc.wantStatus {
                t.Fatalf("Status = %q, want %q", got.Status, tc.wantStatus)
            }
            if got.AgentType != tc.wantAgent {
                t.Fatalf("AgentType = %q, want %q", got.AgentType, tc.wantAgent)
            }
        })
    }
}
```

**Assertions to add to `frame_ops_test.go` imports** (if not already
present): `agentpkg "github.com/wake/purdex/internal/agent"` and
`"github.com/wake/purdex/internal/store"`.

**Verify red:**

```bash
go test ./internal/module/agent/ -run TestBuildProjectionNormalized_NilProjectionGuard -v
```

Expected: `nil_projection_empty_status` fails with
`Status = "", want "clear"`. Other three sub-tests pass against the
unchanged function.

**Commit:** `test(agent): add failing nil-projection guard invariant test for #717`

---

### Task T2 — Add failing integration tests on sweep pipeline (TDD red)

**Files:** `internal/module/agent/sweep_test.go`

**What:**

Append two tests that drive the full sweep → DB delete → broadcast
pipeline end-to-end, using the existing
`m.core.Events.AddTestSubscriber()` capture pattern (see
`TestSweep_PruneDeadProxyRefs_BroadcastsProjectionAfterDetach` at
`sweep_test.go:811` for reference).

Both tests use the same wire-format decoder helper to assert exact
field values, not substring matches:

```go
func decodeSweepBroadcast(t *testing.T, raw []byte) agentpkg.NormalizedEvent {
    t.Helper()
    var hostEvent core.HostEvent
    if err := json.Unmarshal(raw, &hostEvent); err != nil {
        t.Fatalf("unmarshal HostEvent: %v (raw=%s)", err, raw)
    }
    var normalized agentpkg.NormalizedEvent
    if err := json.Unmarshal([]byte(hostEvent.Value), &normalized); err != nil {
        t.Fatalf("unmarshal NormalizedEvent: %v (value=%s)", err, hostEvent.Value)
    }
    return normalized
}
```

(Place the helper near the top of `sweep_test.go`, just below the
`newSweepTestModule` helper.)

#### Test A — `TestSweep_PidDead_BroadcastsClearWhenSessionEmpties`

Reproduces the #717 symptom:

1. Wire `m.core` with a real `EventsBroadcaster` and add a test
   subscriber.
2. Upsert one frame (PID 99999, AgentType "opencode") in pane "%5"
   of session "work".
3. Stub `isPidAliveFn = func(int) bool { return false }`.
4. Call `m.sweepOnce()`.
5. Drain the subscriber's `SendCh()` looking for a broadcast.
6. Decode it with `decodeSweepBroadcast`.
7. Assert:
   - `normalized.Status == "clear"`
   - `normalized.RawEventName == "sweep:pid_dead"`
   - `normalized.AgentType == "opencode"` (fallback preserved when
     projection is nil)

#### Test B — `TestSweep_PidDead_PreservesSiblingStatus`

Confirms the guard does not over-clear when siblings exist (this
test would *also* pass against Option A — included to prove Option B
does not regress relative to A):

1. Same setup but two frames in pane "%5":
   - cc parent — `PID: 200`, alive, `StatusIdle`, **`StartedAt: 10`**, `LastSeenAt: 10`, `ProcessStartTime: "A"`, `Verified: true`
   - codex child — `PID: 300`, **dead**, `StatusRunning`, **`StartedAt: 20`**, `LastSeenAt: 20`, `PPID: 200`, `ProcessStartTime: "B"`, `Verified: true`
2. Stub `isPidAliveFn` so `pid != 300` is alive;
   `processStartTimeFn` returns `"A"` for 200, `"B"` for 300;
   `nowFn` pinned to a fixed instant.
3. Call `m.sweepOnce()`.
4. Capture `sweep:pid_dead` broadcast.
5. Decode and assert:
   - `normalized.Status == "idle"` (TopFrame.Status, branch [C])
   - `normalized.RawEventName == "sweep:pid_dead"`
   - `normalized.AgentType == "cc"` (TopFrame override)

**Stability note (codex P2 0.94)**: `buildPaneProjection` /
`projectionSortGreater` first compare `StartedAt`, then fall back to
`FrameID` (assigned a random UUID by `frames.Upsert`). If both
frames default `StartedAt` to `0`, tie-break flips to a non-
deterministic FrameID order and the test goes flaky. Explicit
non-equal `StartedAt` (10 vs 20) eliminates the tie. After sweep
deletes the codex child, the cc parent is the unambiguous surviving
TopFrame regardless of FrameID UUID.

**Verify red:**

```bash
go test ./internal/module/agent/ -run "TestSweep_PidDead_(BroadcastsClearWhenSessionEmpties|PreservesSiblingStatus)" -v
```

Expected: Test A fails on `Status == ""` (the bug). Test B may pass
already against the unfixed code — that is fine; B is a regression
guard, not a bug witness.

**Commit:** `test(agent): add failing sweep:pid_dead broadcast assertions for #717`

---

### Task T3 — Apply the 3-line guard (TDD green)

**Files:** `internal/module/agent/frame_ops.go`

**What:**

Edit lines 659-661 of `buildProjectionNormalized`:

```diff
 if projection == nil {
+    if normalized.Status == "" {
+        normalized.Status = string(agentpkg.StatusClear)
+    }
     return normalized
 }
```

No comments. No helper function. No restructure. The guard reads as
plain English ("if no status was supplied, default to clear") at the
exact branch where the invariant matters.

**Verify green:**

```bash
go test ./internal/module/agent/ -run "TestBuildProjectionNormalized_NilProjectionGuard|TestSweep_PidDead_" -v
```

Expected: all six sub-tests pass.

Also run the full agent test suite to confirm no regression:

```bash
go test ./internal/module/agent/...
```

**Commit:** `fix(agent): default sweep:pid_dead broadcast to clear when session empties`

---

### Task T4 — Full repo verification

**What:**

Run the complete daemon test suite + lint + build:

```bash
go test ./...
go vet ./...
go build ./...
```

SPA is untouched, but run vitest for sanity (per CLAUDE.md
"Codex sandbox 無網路" — Claude must run pnpm tests manually):

```bash
cd spa && pnpm install --frozen-lockfile && npx vitest run --reporter=dot
cd spa && pnpm run lint
```

If `pnpm install --frozen-lockfile` is unavailable (offline / cache
miss), fall back to running tests against whatever node_modules is
already present:

```bash
cd spa && pnpm exec vitest run --reporter=dot
cd spa && pnpm exec eslint .
```

If both paths fail, document the gap in the PR body so the user can
run vitest on mlab during manual verification.

If any test or lint fails outside the patched area, investigate
before declaring T4 done. Do **not** widen scope to fix unrelated
breakage — open a separate issue.

**No commit** for T4 (verification only).

---

## 3. PR plan

After T1-T4 succeed:

1. Push `worktree-sweep-pid-dead-broadcast` to origin.
2. `gh pr create` with body:
   - **Summary**: 3 bullets — root cause, fix, behavior change at
     handler.go:266/328 (desired).
   - **Test plan**: lift §8 from spec.
   - **Closes**: #717.
3. Round 1 review: `/codex:review --base main` (standard).
4. Round 2 review: 3-parallel `/codex:adversarial-review` runs:
   - Attack: bugs / races / boundary conditions in the guard +
     callsite audit reasoning.
   - Defense: validate Option B vs A again under adversarial
     re-reading of the spec; check architectural fit.
   - File quality: `frame_ops.go` SRP / size / responsibility
     creep.
5. Aggregate findings (severity / confidence / complexity table per
   `feedback_dev_process`). Fix high-confidence + low-complexity +
   high-relevance items inline; defer the rest as `gh issue`.
6. Merge (squash). Squash commit message follows existing convention
   (`fix(agent): ...`).

## 4. Bump PR (after merge)

Independent PR from a fresh worktree off `origin/main`:

- `VERSION` → `1.0.0-alpha.250`
- `package.json` + `spa/package.json` → `1.0.0-alpha.250`
- `CHANGELOG.md` → entry under `## [1.0.0-alpha.250]` referencing
  #717

Per `feedback_bump_base_origin_not_local`: enter the bump worktree
and immediately `git reset --hard origin/main` to avoid pulling in
parallel session commits from local `main`.

**Pre-bump remote check (codex P3 0.62)**: local files only prove no
in-flight bump in *this* checkout. Before reserving alpha.250,
verify no other branch already claims it:

```bash
gh pr list --search "alpha.250 in:title is:open" --json number,title
gh api repos/wake/purdex/branches --paginate --jq '.[].name' | grep -i bump
```

If any open PR or branch already targets alpha.250, jump to the
next free version and update plan + commit message accordingly.

## 5. Out of scope (do not regress into)

- ❌ Modifying `sweep.go` (any line). The fix is intentionally at
  `frame_ops.go` per spec §3.
- ❌ Modifying `handler.go`, `module.go`, `probe_orchestrator.go`.
- ❌ Modifying SPA files.
- ❌ Modifying the WS payload schema (`core.HostEvent` /
  `agentpkg.NormalizedEvent` field set). The fix populates an
  existing field; it does not add or rename fields.
- ❌ Adding liveness probe / heartbeat / process-tree watcher
  features.
- ❌ Refactoring `buildProjectionNormalized` (collapsing branches,
  extracting helpers, adding comments).
- ❌ Touching unrelated agent tests "while we're in there".

## 6. Rollback

The fix is purely additive (3 conditional lines inside an existing
branch). To roll back, delete those 3 lines. No database
migration, no SPA contract change, no external state change.

## 7. Verification checklist (before opening PR)

- [ ] T1: 4 invariant sub-tests added, all 4 pass after T3
- [ ] T2: 2 sweep integration tests added, Test A demonstrably red
      before T3 and green after
- [ ] T3: 3-line guard applied at frame_ops.go:659; no other
      production changes
- [ ] T4: `go test ./...` green, `go vet ./...` green,
      `go build ./...` green
- [ ] T4: `cd spa && npx vitest run` green, `pnpm run lint` green
- [ ] No new comments added inside `buildProjectionNormalized`
- [ ] No new helpers / abstractions introduced
- [ ] Diff stat matches §1 budget (≤ +260 / -0 across 3 files
      excluding spec/plan)
- [ ] Manual mlab repro from spec §8 still drafted in PR body
      (executed by user)
