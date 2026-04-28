# Spec — sweep:pid_dead broadcast asymmetry fix

**Issue**: #717
**Date**: 2026-04-29
**Scope**: 1-line daemon fix + 2 new unit tests

## Symptom

After a tmux pane's agent process (observed: opencode killed via Ctrl+C) exits, the SPA agent indicator (icon + status dot) does not clear. Indicator persists at the last seen status (e.g. `idle`/`running`) with `agent_type` still populated.

## Forensic trace (live mlab repro, 2026-04-29 02:18)

1. Process dies (PID 28965).
2. Sweep tick (≤ 2s, `sweep.go:21 sweepInterval`) → `isPidAliveFn(28965) == false`.
3. `clearFrame(frame, "pid_dead")` deletes the row from `agent_frames`. ✅
4. `afterFrameCleared` calls `projectionForSession("purdex-sync")` → returns `nil` because the session has no remaining frames.
5. `buildProjectionNormalized(projection=nil, frame.AgentType, "sweep:pid_dead", …, agentpkg.DeriveResult{})` is invoked at `sweep.go:551`.
6. Inside `buildProjectionNormalized` (`frame_ops.go:651-672`) the `projection == nil` branch returns early with `normalized.Status = string(result.Status) = ""`.
7. WS broadcast emitted with `status=""`, `agent_type="opencode"`, `raw_event_name="sweep:pid_dead"`.
8. SPA `useAgentStore.handleNormalizedEvent`: `event.status === 'clear'` is false → no `clearSession()`. Status block (`useAgentStore.ts:156`) skipped because `if (status)` is falsy on `""`. `agentTypes[key]` is overwritten to `"opencode"` and preserved → indicator stays.

## Root cause

`buildProjectionNormalized` has asymmetric handling between two semantically equivalent "no top frame to display" states:

| Branch | Status set to |
|---|---|
| `projection == nil` (line 661-663) | `string(result.Status)` (passthrough — sweep passes empty) |
| `projection != nil && projection.TopFrame == nil` (line 665-668) | `string(agentpkg.StatusClear)` (forced) |

Sweep's `afterFrameCleared` callsite passes `agentpkg.DeriveResult{}` because it isn't deriving from a hook; it's clearing a frame. The empty string leaks through to broadcast.

The other two `sweep:` callsites pass through `buildProjectionNormalized` for live panes whose projection is non-nil and whose `TopFrame` exists, so the third branch dominates and they correctly carry `TopFrame.Status`. They are unaffected.

| Callsite | Reason | Projection state | Bug exposure |
|---|---|---|---|
| `sweep.go:551` `afterFrameCleared` | `pid_dead`/`pid_reused`/`idle_timeout` | nil when last frame cleared | **broken** |
| `sweep.go:499` `broadcastProxyPruned` | `proxy_pruned` | non-nil + TopFrame | safe |
| `sweep.go:327` (canonicalize broadcast) | `proxy_canonicalized` | non-nil + TopFrame | safe |

## Fix — Option A (chosen)

Make sweep's intent explicit: when sweep clears a frame, broadcast `status=clear`.

```diff
// internal/module/agent/sweep.go:551
- normalized := buildProjectionNormalized(projection, frame.AgentType, "sweep:"+reason, nowFn().UnixNano(), agentpkg.DeriveResult{})
+ normalized := buildProjectionNormalized(projection, frame.AgentType, "sweep:"+reason, nowFn().UnixNano(), agentpkg.DeriveResult{Status: agentpkg.StatusClear})
```

Three projection-state cases after the fix:

- `projection == nil` → `Status = "clear"` ✅ (fixes the bug)
- `projection != nil, TopFrame == nil` → branch overrides with `StatusClear` (already correct, behavior unchanged)
- `projection != nil, TopFrame != nil` → branch overrides with `TopFrame.Status` (already correct, behavior unchanged — sibling pane's frame correctly displayed)

## Why not Option B

A structural fix in `buildProjectionNormalized` (`if projection == nil && normalized.Status == "" { normalized.Status = StatusClear }`) is also valid and more defensive. We choose Option A because:

- Surgical: 1 line, in the broken caller.
- Semantically explicit at the callsite: sweep's intent is "this frame is gone; tell SPA to clear".
- Zero regression risk on `handler.go:256` (the only other `projection==nil` callsite, which already passes a real `result.Status`).
- Cheaper review.

## Tests

Add to `internal/module/agent/sweep_test.go` (existing file). Follow `TestSweep_PruneDeadProxyRefs_BroadcastsProjectionAfterDetach` (line 811) pattern: install `core.Core` with broadcaster + test subscriber, drive sweep, capture WS payload, assert.

1. **`TestSweep_PidDeadBroadcastsStatusClearWhenSessionEmpty`** — single frame in session; sweep kills it. Assert broadcast payload contains `"status":"clear"` and `"raw_event_name":"sweep:pid_dead"`.
2. **`TestSweep_PidDeadBroadcastsSiblingStatusWhenSessionNonEmpty`** — two frames (different panes, same session); sweep kills one. Assert broadcast payload contains the surviving frame's `Status` (not `"clear"`) and the surviving `agent_type`.

## Out of scope

- Any change to `frame_ops.go`.
- Any change to `sweep.go:327` / `sweep.go:499` (proxy callsites — verified safe).
- Liveness probe / heartbeat / `kill -0` polling (sweep already does this every 2s).
- W3 framework concerns / W2 PR #710 (separate, orthogonal — verified zero file overlap).
- SPA-side defensive change (e.g. treating empty `status` as `clear`) — unnecessary once daemon emits the right value.

## mlab live verification (post-merge)

```bash
go build -o /tmp/pdx ./cmd/pdx
/tmp/pdx setup --agent opencode
PDX_DEV_MODE=1 /tmp/pdx serve > /tmp/pdx.log 2>&1 &
tmux new-session -d -s opencode-test
tmux send-keys -t opencode-test 'opencode' Enter
sleep 5
tmux send-keys -t opencode-test C-c
sleep 3
# Expected: SPA opencode indicator clears within ~2s of Ctrl+C.
# Daemon log should show "sweep:pid_dead" with "status":"clear" payload.
```

## Conflict / coordination

- **PR #710 (W2 Phase 1)** — already merged (alpha.251 base). Verified earlier: `frame_ops.go` line 651-672 sandwiched between W2 hunks but unmodified; sweep callsite untouched by W2. Now that #710 is merged, this PR rebases trivially on `b1473710`.
- **`hooks-hotfix-plan` / probe rework** — orthogonal; not in scope.
