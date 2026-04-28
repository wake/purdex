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
- Cheaper review and zero risk to other callers.

`buildProjectionNormalized`'s other callers in `handler.go`:

| Callsite | `projection` arg | Notes |
|---|---|---|
| `handler.go:256` (`error_guard_blocked`) | hard-coded `nil` | passes real `result` with non-empty `result.Status` (derive output is "running"/"error"/etc) |
| `handler.go:311` / `handler.go:373` | runtime variable, may be nil | passes real `result.Status` from derive logic |

None of these are modified by Option A — only the sweep callsite (`sweep.go:551`) changes. Handler's runtime-nil-projection paths continue to passthrough `result.Status` exactly as before. (Option B would also have been safe for these because `if normalized.Status == "" { … }` would only trigger when handler accidentally passes empty status, which today's derive logic doesn't do — but Option A leaves that helper contract untouched, which is the point.)

## Tests

Add to `internal/module/agent/sweep_test.go` (existing file). Set up `core.Core` with broadcaster + test subscriber as `TestSweep_PruneDeadProxyRefs_BroadcastsProjectionAfterDetach` (line 811) does, drive sweep, capture WS message.

**Assertion style** (stricter than the proxy_pruned test, which only does `strings.Contains`): unmarshal the WS envelope, then unmarshal `Value` into `agentpkg.NormalizedEvent`, and assert exact field values. `strings.Contains("status":"clear")` would be brittle (JSON inside JSON, escaping concerns, false positives on `"sweep:proxy_pruned"`-style substrings).

1. **`TestSweep_PidDeadBroadcastsStatusClearWhenSessionEmpty`** — single frame in session; sweep kills it. Assert `Status == "clear"`, `RawEventName == "sweep:pid_dead"`, `AgentType == frame.AgentType`.
2. **`TestSweep_PidDeadBroadcastsSiblingStatusWhenSessionNonEmpty`** — two frames in the same tmux session but on different panes; sweep kills one. Assert `Status == surviving_frame.Status` (not `"clear"`) and `AgentType == surviving_frame.AgentType`.

`pid_dead` is the representative reason for `afterFrameCleared` — `pid_reused` and `idle_timeout` go through the same `sweep.go:551` callsite, so the fix and these two tests cover all three reasons. (Existing `TestSweep_ClearsIdleFramesByLastSeen` at line 250 verifies the DB delete path for `idle_timeout` but does not assert broadcast `Status`, so the new test #1 also fills that regression-protection gap implicitly via the shared callsite.)

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
