# Spec — Lights L2: Proxy detach on Stop

**Kickoff**: `kickoff_codex_broker_and_lights_governance.md` §燈號 L2
**Date**: 2026-05-01
**Branch**: `worktree-lights-l2-proxy-detach`
**Baseline**: origin/main `5d40e2a2` (alpha.280)
**Scope**: When a hook sender that is currently proxy-attached to a parent frame emits `LifecycleStop` / `LifecycleStopFailure`, detach the proxy `SubagentRef` from the parent so the lit dot on the parent's tab goes out without waiting for `LifecycleSessionEnd`.

**Estimated size**: ~30 LOC production + ~120 LOC table-driven tests = ~150 LOC.

---

## 1. Symptom & current state

User-reported: `purdex-big-plan` screenshot — a codex review job completed (broker emitted Stop), but the proxy dot on the parent cc tab kept blinking. The broker stays alive across tasks (by design, see governance spec §1), so `LifecycleSessionEnd` never fires for that PID; the proxy ref stays attached to the parent frame indefinitely.

Concretely: in `internal/module/agent/frame_ops.go::applyFrameEvent` the lifecycle switch (`frame_ops.go:78`) handles only `LifecycleSessionEnd` and `LifecycleSubagentStart/Stop`. `LifecycleStop` and `LifecycleStopFailure` are emitted by both `internal/agent/cc/events.go:61,68` and `internal/agent/codex/events.go:55,62`, but `applyFrameEvent` has no matching case — they fall through to the SessionStart fast-path (which short-circuits on its `lifecycle == LifecycleSessionStart` guard at `frame_ops.go:210`) and then to the create-frame path (`frame_ops.go:251` onward), which does not touch proxy refs on parent frames.

The proxy ref the SessionStart fast-path attached at `frame_ops.go:230` therefore lives on the parent's `Subagents` slice until either:
- `LifecycleSessionEnd` finally fires (only when the broker process actually exits — for a long-running codex broker, that may be hours or never), or
- `pruneDeadProxyRefs` (`frame_ops.go:1086`) runs at the next SessionStart on the parent frame and notices the source PID is dead.

Neither helps for the user-visible symptom: the broker is still alive (so `pruneDeadProxyRefs` keeps the ref) and the broker's own SessionEnd may not fire for a long time.

## 2. The four invariants

A correct L2 implementation must preserve all four:

| # | Invariant | Why |
|---|-----------|-----|
| **I1** | **Idempotent** under `Stop → SessionEnd` and `Stop → Stop` repeats | A broker that does eventually exit will fire SessionEnd after Stop; a misbehaving hook may double-fire Stop. `removeProxyRefForSender` is already a no-op when no matching ref exists; the L2 case must call it without precondition checks that would diverge. |
| **I2** | **No effect on standalone agents** (`frame != nil` for the sender) | A `LifecycleStop` whose sender owns its own frame is a normal main-agent stop — its lit state is governed by the lights-rebuild ProbeIntent dispatcher (`worktree-probe-intent-bidirectional-grace`), not by proxy detach. L2 must `break` out of the case in this branch and let the existing fall-through (narrow column update at `frame_ops.go:311`) handle it unchanged. |
| **I3** | **No effect on native subagent refs** (`IsProxy=false`) | `removeProxyRefForSender::subagentsContainProxySender` (`frame_ops.go:833`) already filters by `ref.IsProxy && ref.SourcePID == … && ref.SourceStartTime == …`. L2 reuses this helper unmodified — native refs created via `mutateSubagentsWithRetry` from the SubagentStart path are invisible to it. |
| **I4** | **Re-attach via next SessionStart works unchanged** | When the broker dispatches the next prompt, codex emits a new `LifecycleSessionStart` from the same PID. `applyFrameEvent` enters the SessionStart fast-path (`frame_ops.go:210`), `findProxyParent` (`frame_ops.go:971`) finds the same cc parent via PPID walk, and `attachProxyRefWithRetry` (`frame_ops.go:230`) attaches a fresh proxy ref. The L2 detach must not leave any stale state on the parent frame that would make this re-attach a no-op or duplicate. Since `removeProxyRefForSender` removes the ref entirely (not just marks it), re-attach starts clean. |

## 3. Modification

Single `case` insertion into the lifecycle switch in `applyFrameEvent` (after the `LifecycleSessionEnd` case, before `LifecycleSubagentStart, LifecycleSubagentStop`):

```go
case agentpkg.LifecycleStop, agentpkg.LifecycleStopFailure:
    // L2: detach proxy ref on the parent frame when the proxy-attached
    // sender (broker) finishes a task. SessionEnd may not fire for hours
    // for long-running brokers; without this case the lit dot stays on
    // the parent until SessionEnd or pruneDeadProxyRefs eventually clears
    // it.
    //
    // Standalone case (frame != nil): sender owns its own frame, lit
    // state is governed by the main-agent ProbeIntent dispatcher. Fall
    // through to the existing narrow-column update path so behavior is
    // unchanged.
    if frame != nil {
        break
    }
    // frame == nil: sender is proxy-attached to a parent in the same pane.
    // Mirror the SessionEnd branch's frame==nil handling
    // (frame_ops.go:113-127): probe the pane for a matching proxy ref and
    // detach. removeProxyRefForSender is idempotent: returns removed=false
    // with no error when no matching ref exists, so a redundant
    // Stop→SessionEnd or Stop→Stop pair is safe.
    removed, parentFrame, parentBefore, parentAfter, derr := m.removeProxyRefForSender(
        req.TmuxPaneID, req.SenderPID, req.SenderStartTime, broadcastTs,
    )
    if derr != nil {
        return nil, FrameTraceMeta{}, derr
    }
    if removed {
        projection, perr := m.projectPane(req.TmuxPaneID)
        return projection, FrameTraceMeta{
            FrameID:       parentFrame.FrameID,
            ParentFrameID: parentFrame.ParentFrameID,
            Decision:      "updated_frame",
            Reason:        "proxy_subagent_detached_on_stop",
            Before:        parentBefore,
            After:         parentAfter,
        }, perr
    }
    projection, perr := m.projectPane(req.TmuxPaneID)
    return projection, FrameTraceMeta{
        Decision: "skipped",
        Reason:   "stop_without_frame_or_proxy",
        Before:   before,
        After:    map[string]any{},
    }, perr
```

`break` (not `fallthrough`) is intentional — Go switch falls through to the post-switch code (the SessionStart fast-path block + create-frame path), which is exactly the unchanged path I2 requires.

### Trace reasons added

Two new `FrameTraceMeta.Reason` strings introduced:
- `proxy_subagent_detached_on_stop` — successful detach; mirrors `proxy_subagent_detached` (used by SessionEnd branch) but distinguishable in trace dumps so we can tell L2 from SessionEnd in production diagnostics.
- `stop_without_frame_or_proxy` — Stop arrived from a sender with no frame and no proxy ref; either (a) Stop fired after SessionEnd already detached, or (b) hook arrived before any SessionStart attached. Either way: no-op, surface as skipped.

## 4. Test matrix

`internal/module/agent/frame_ops_test.go` adds one table-driven test `TestApplyFrameEvent_LifecycleStop_ProxyDetach` with the following rows:

| # | Setup | Lifecycle | Expected `Decision` | Expected `Reason` | Parent `Subagents` after |
|---|-------|-----------|---------------------|-------------------|-------------------------|
| 1 | parent cc frame + 1 proxy ref(IsProxy=true, SourcePID=42) | `Stop` from PID=42 | `updated_frame` | `proxy_subagent_detached_on_stop` | `[]` (proxy gone) |
| 2 | parent cc frame + 1 proxy ref(IsProxy=true, SourcePID=42) | `StopFailure` from PID=42 | `updated_frame` | `proxy_subagent_detached_on_stop` | `[]` |
| 3 | parent cc frame + 1 native ref(IsProxy=false, ID="task-x") | `Stop` from PID=42 | `skipped` | `stop_without_frame_or_proxy` | unchanged (native preserved) |
| 4 | parent cc frame + no refs | `Stop` from PID=42 | `skipped` | `stop_without_frame_or_proxy` | `[]` |
| 5 | sender owns its own frame | `Stop` from sender's PID | (whatever default narrow-update returns) | not `proxy_subagent_detached_on_stop` and not `stop_without_frame_or_proxy` | sender's frame.Subagents unchanged |
| 6 | parent + proxy ref. First call: `Stop`. Second call: `Stop` again | sequential | call 1: `updated_frame` / call 2: `skipped` (idempotent) | call 1: `proxy_subagent_detached_on_stop` / call 2: `stop_without_frame_or_proxy` | `[]` after both |
| 7 | parent + proxy ref. First call: `Stop`. Second call: `SessionEnd` from same PID | sequential | call 1: `updated_frame` / call 2: `skipped` (already detached) | call 1: `proxy_subagent_detached_on_stop` / call 2: `session_end_without_frame` | `[]` after both |
| 8 | parent + proxy ref(SourcePID=42, SourceStartTime="t1"). Detach via Stop. Re-attach via new SessionStart from PID=99 (broker re-spawn) | sequential | call 1: `updated_frame` / call 2: `updated_frame` | call 1: `proxy_subagent_detached_on_stop` / call 2: `proxy_subagent_attached` | `[{IsProxy:true, SourcePID:99, SourceStartTime:"t99"}]` |

Row 5 (standalone) is the **I2 regression guard**. Row 6 / 7 are **I1 idempotency guards**. Row 8 is the **I4 re-attach guard**. Row 3 is the **I3 native-isolation guard**.

### Test fixture pattern

Reuse the existing test harness around `m.applyFrameEvent` (see `frame_ops_test.go::TestApplyFrameEvent_LifecycleSessionEnd_ProxyDetach` if present, or the equivalent fixture used by the SessionEnd branch). Construct an in-memory `Module` with a `frames` store fake; pre-seed parent frame + Subagents list via direct store insert; call `applyFrameEvent` with `EventRequest{Lifecycle: ...}`; assert on returned `FrameTraceMeta` + post-call store state.

No new mocks needed — `removeProxyRefForSender` and `projectPane` are already test-doubleable via the existing `m.frames` interface.

## 5. Out of scope

Explicitly NOT covered by L2:

- **Governance P2/P3**: killing orphan brokers is a separate workstream (`worktree-governance-p2-…`, planned). L2 does not change the broker's lifetime; it only changes when the parent's UI dot goes out.
- **Standalone codex Stop main-agent light**: governed by the lights-rebuild ProbeIntent dispatcher (`worktree-probe-intent-bidirectional-grace`). L2 leaves `frame != nil` Stop handling unchanged.
- **Native subagent Stop dots**: SubagentStart/Stop already drive native dots correctly via `mutateSubagentsWithRetry`. L2 does not touch the SubagentStart/Stop case.
- **Re-attach optimization**: The next SessionStart re-walks PPID and re-attaches via `findProxyParent` + `attachProxyRefWithRetry`. We do not introduce a "soft-detach with re-attach hint" optimization; the cost is one PPID walk per dispatch, already paid in the existing SessionStart fast-path.
- **L1 (opencode subagent idle filter)**, **L3 (codex spawn/close hook)**, **L4 (opencode SOT migration)** — separate phases per kickoff.

## 6. Risks & mitigations

| Risk | Mitigation |
|------|-----------|
| Stop fires from a PID that legitimately should have its own frame (e.g. a future codex CLI mode where Stop runs in a fresh sub-process) | I2 — `frame != nil` short-circuits. The new case only acts on `frame == nil`. |
| `removeProxyRefForSender` returns an error mid-detach (storage failure) | Same surface as the SessionEnd branch already exposes: return the error to handler, hook re-fires on the next event, parent state remains as-is (proxy ref still attached, no orphan introduced). |
| Stop arrives before any SessionStart has attached (race) | I1 / row 4 — `removed=false` returned, surface as `skipped` with `stop_without_frame_or_proxy`. No error. The SessionStart fast-path on the upcoming SessionStart still works. |
| Concurrent SessionEnd and Stop on the same PID land out of order | Both call `removeProxyRefForSender`; the helper uses `detachProxyRefWithRetry` (`frame_ops.go:892`) which is optimistic-concurrency safe. The losing call sees `subagentsContainProxySender == false` on its retry and returns `removed=false`. |
| Trace reason name `proxy_subagent_detached_on_stop` collides with future hook events | Suffix `_on_stop` is unique in the current trace reason vocabulary (grep verified). Future additions should use the same suffix discipline. |

## 7. Acceptance criteria

| AC | Description |
|----|-------------|
| **AC1** | Adding the `case agentpkg.LifecycleStop, agentpkg.LifecycleStopFailure:` block to `applyFrameEvent` is the only production change. No new exported functions, no new types, no signature changes. |
| **AC2** | All 8 test rows in §4 pass. |
| **AC3** | All existing tests in `internal/module/agent/...` pass unchanged (no test modifications outside the new test). |
| **AC4** | `cd spa && pnpm run lint && pnpm run build` and `go build ./... && go test ./...` pass. |
| **AC5** | Trace reason vocabulary stays additive — only `proxy_subagent_detached_on_stop` and `stop_without_frame_or_proxy` are new strings. |
| **AC6** | The change to `frame_ops.go` is < 50 lines including the new comment block; the new test is < 200 lines. |

## 8. Verification (post-merge live check)

On `mlab` after deploying the daemon update:

1. Spawn a codex review through cc (creates proxy ref on cc parent frame).
2. Wait for codex review to finish (broker emits `Stop`).
3. Inspect `purdex-big-plan` (or any cc tab with the codex review history): the proxy dot should disappear within one hook propagation cycle (~50-100 ms typical).
4. Without dispatching a new prompt, the dot stays out (no spurious re-attach).
5. Dispatch a new prompt to the same broker: a fresh proxy dot lights up (re-attach via SessionStart fast-path works — I4 verified live).

## 9. Single-PR delivery

Spec, plan, implementation, tests in one PR (kickoff phase 2). One round of standard codex review. No adversarial round needed (50-150 LOC, single file, no architectural surface). If round 1 surfaces unexpected complexity, escalate to adversarial round.
