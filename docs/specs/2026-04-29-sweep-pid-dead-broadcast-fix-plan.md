# Plan — sweep:pid_dead broadcast asymmetry fix

**Spec**: [2026-04-29-sweep-pid-dead-broadcast-fix-spec.md](2026-04-29-sweep-pid-dead-broadcast-fix-spec.md)
**Issue**: #717
**Approach**: TDD, single-phase, single-PR.

## Phase 1 (only phase)

### Task 1.1 — Write failing tests in `internal/module/agent/sweep_test.go`

Two new tests appended to the file (after the existing `TestSweep_ClearingFramePreservesSiblings` block to keep related tests together).

**Test fixture conventions** (consistent with the existing file):

- Use `newSweepTestModule(t)` for the base module.
- Install `m.core = &core.Core{Events: core.NewEventsBroadcaster(), Tmux: m.tmux}`, add a test subscriber via `m.core.Events.AddTestSubscriber()`, and pair with `defer m.core.Events.RemoveTestSubscriber(sub)`.
- Override `isPidAliveFn` / `processStartTimeFn` / `nowFn` via the existing module-level vars.
- Reset all overrides via `t.Cleanup`.

**Test 1 — `TestSweep_PidDeadBroadcastsStatusClearWhenSessionEmpty`**:

```go
// Setup:
//  - one verified frame on pane "%5" (session "work" via fakeTmux mapping)
//  - isPidAliveFn returns false → sweepOnce calls clearFrame("pid_dead")
//  - session has no other frames → projectionForSession returns nil
//
// Drive: m.sweepOnce()
//
// Assert:
//  1. m.frames.ListByPane("%5") is empty (sanity)
//  2. WS subscriber receives one message
//  3. Unmarshal envelope; the inner Value is a JSON-string of NormalizedEvent
//  4. Unmarshal Value → NormalizedEvent
//  5. event.RawEventName == "sweep:pid_dead"
//  6. event.Status == string(agentpkg.StatusClear)   ← THE ASSERT THAT FAILS BEFORE FIX
//  7. event.AgentType == "<seeded type>"             (from frame.AgentType fallback)
```

**Test 2 — `TestSweep_PidDeadBroadcastsSiblingStatusWhenSessionNonEmpty`**:

```go
// Setup:
//  - two verified frames on the SAME tmux session ("work") but DIFFERENT panes
//      (frame A on "%5", frame B on "%6"; both panes in session "work")
//  - frame A's PID dead, frame B alive
//  - fakeTmux.SetPaneSessionName for both panes mapping to "work"
//
// Drive: m.sweepOnce()
//
// Assert:
//  1. surviving frame B still in DB
//  2. WS subscriber receives the broadcast
//  3. Unmarshal envelope.Value → NormalizedEvent
//  4. event.RawEventName == "sweep:pid_dead"
//  5. event.Status == string(frame B's Status)        ← regression guard against
//                                                       falsely setting clear
//  6. event.AgentType == frame B's AgentType
```

`projectionForSession("work")` will return non-nil with `TopFrame == frame B` because B is alive (and idle/active per fixture), so the third branch in `buildProjectionNormalized` (line 668-670) overrides `Status` and `AgentType`. This documents the "no regression on multi-pane sessions" property.

**Note**: assertion uses `core.HostEvent` envelope shape. The pattern (already used in `handler_test.go`) is:

```go
var env core.HostEvent
json.Unmarshal(rawMsg, &env)
var normalized agentpkg.NormalizedEvent
json.Unmarshal([]byte(env.Value), &normalized)
```

Adapt to whatever the existing event broadcaster envelope is (verify by reading `core.EventsBroadcaster` send path before writing the test; if `m.core.Events.Broadcast(code, "hook", string(payload))` is the call, the subscriber gets a typed struct — read `core/events.go` to confirm shape).

**Run**: `go test ./internal/module/agent/ -run TestSweep_PidDead -v` → expect Test 1 to FAIL with `Status: "" want "clear"`, Test 2 PASS.

### Task 1.2 — Apply 1-line fix at `internal/module/agent/sweep.go:551`

```diff
-	normalized := buildProjectionNormalized(projection, frame.AgentType, "sweep:"+reason, nowFn().UnixNano(), agentpkg.DeriveResult{})
+	normalized := buildProjectionNormalized(projection, frame.AgentType, "sweep:"+reason, nowFn().UnixNano(), agentpkg.DeriveResult{Status: agentpkg.StatusClear})
```

Nothing else changes.

**Run**: `go test ./internal/module/agent/ -run TestSweep_PidDead -v` → both PASS.

### Task 1.3 — Format + full regression run

```bash
gofmt -w internal/module/agent/sweep.go internal/module/agent/sweep_test.go
go test ./internal/module/agent/...
go vet ./...
```

Expect: all pass. Hot spots to watch (these are the existing tests around `afterFrameCleared` / `buildProjectionNormalized`):

- `TestSweep_ClearsDeadFramesByPid` (line 27)
- `TestSweep_ClearsIdleFramesByLastSeen` (line 250)
- `TestSweep_PreservesLiveFrameAfterProbeActivity` (line 504)
- `TestSweep_ClearingFramePreservesSiblings` (line 617)
- `TestSweep_PruneDeadProxyRefs_BroadcastsProjectionAfterDetach` (line 811) — **must still pass**; uses `proxy_pruned` callsite at line 499 which we don't touch.

If any test fails: STOP, do not patch around it; investigate whether the fix has scope creep.

### Task 1.4 — Commit

Single commit, message:

```
fix(daemon): sweep:pid_dead broadcasts status=clear when session empty

When sweep clears the last frame in a session, projectionForSession returns
nil and buildProjectionNormalized's projection==nil branch trusts an empty
result.Status, leaking "" to the WS broadcast. SPA's handleNormalizedEvent
treats event.status === 'clear' specially and falls through on "", leaving
the agent indicator stuck.

Pass StatusClear explicitly at the sweep callsite. Other branches of
buildProjectionNormalized that already force or override Status are
unaffected.

Closes #717
```

## Out of scope (do NOT touch)

- `frame_ops.go` and `buildProjectionNormalized`'s helper contract — Option A keeps the helper signature/semantics intact; only the sweep callsite changes its argument.
- `sweep.go:327` (proxy_canonicalized) and `sweep.go:499` (proxy_pruned) — verified safe.
- `useAgentStore.ts` — no SPA change needed once daemon broadcasts `clear`.
- W2 PR #710 / W3 framework / probe rework — separate concerns.

## Verification gate before opening PR

- [ ] Test 1 (`PidDeadBroadcastsStatusClearWhenSessionEmpty`) fails before fix; Test 2 (`PidDeadBroadcastsSiblingStatusWhenSessionNonEmpty`) passes before fix (the surviving-sibling branch is unaffected by the empty-DeriveResult passthrough). Both pass after fix.
- [ ] `gofmt -l internal/module/agent/sweep.go internal/module/agent/sweep_test.go` prints nothing.
- [ ] All other `internal/module/agent/...` tests still pass.
- [ ] `go vet ./...` clean.
- [ ] No file modifications outside `sweep.go`, `sweep_test.go`, and the spec/plan docs.

## PR

Title: `fix(daemon): sweep:pid_dead broadcasts status=clear (closes #717)`
Body must include: forensic trace excerpt + before/after diff + manual mlab verify section per spec.
