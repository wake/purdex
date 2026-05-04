# Spec — Rate-limit subagent cleanup + notification debounce

**Date**: 2026-05-03 (v1 → v2 → v3)
**Branch**: `worktree-rate-limit-cleanup`
**Baseline**: origin/main `401a785c` (alpha.289)

**v3 supersedes v2**. v2 codex review (`task-mopkf73y-c4bjsx`, thread `019ded28-6dad-7930-9df0-d04a29c72724`) found 2 P1 + 3 P2 + 1 fact + 1 nit (residual matrix from v1 → v2 transition). v3 addresses all of them:
- P1: §6.1 `TestStopFailure_NativeDetach_Misses_NoMutation` assertion rewritten — break path DOES execute legacy `UpdateHookPath` (Status=error, LastSeenAt refresh, broadcast); test asserts only "no `mutateSubagentsWithRetry`, no `native_subagent_detached_on_stop_failure` trace step", not "no broadcast change".
- P1: §10 verification plan rewritten — SQL is the immediate cleanup step, not "observe first then SQL if slow". Aligns with §5 / §11.
- P2: §4.6 snippet updated to `JSON.stringify` (was still pipe-join).
- P2: §4.4 cleanup description updated — uses key-builder reverse decode (`JSON.parse(key)[0] === ck`), not prefix-match.
- P2: §5 SQL operational sequence adds `SELECT` backup + `BEGIN/COMMIT` transaction + multi-process check.
- fact: §2.2 opencode "currently does not emit StopFailure" corrected — opencode `session.error` DOES emit `PdxStopFailure` via plugin_template.go; missing piece is `agent_id` in payload, not the event itself.
- nit: §3.2 "two new trace reasons" softened to "one new reason; existing `frame_missing` is reused".

**v2 superseded v1**. v1 codex review (`task-mopk1n84-e61c4i`, thread `019ded1e-ca04-7583-bfbf-b28d2eee134f`) found 3 P1 + 5 P2 + 1 nit findings. v2 addressed all of them:
- P1.1 `mutateSubagentsWithRetry` `applied=true` does **not** mean a ref was actually removed (it only signals `UpsertIfUnchanged` succeeded; `LastSeenAt` always refreshes regardless of ref-list change). v2 adds an explicit pre-check via new helper `findNativeRefByID` so we never broadcast a phantom detach.
- P1.2 dthn cleanup: SQL is the **expected primary cleanup path**, not a fallback; PR-A only handles future StopFailure events, and historical refs whose terminal `StopFailure` predates trace retention will never be re-emitted.
- P1.3 AC5 99% number arithmetic mismatch (1/90 = 98.89%, not 99%). v2 reframes as "≥98.8% suppression over a 100-event window".
- P2 fixes: empty-string agent_id test case, 100-event quantitative test, daemon-restart projection replay risk, fixture-replay regression guard, debounce key collision via JSON-encoded array.
- nit fix: opencode StopFailure emission language tightened.
**Scope**: Two-phase fix for the dthn-class symptom "subagent dots never go away":
- **Phase 1 (PR-A, daemon)**: when an agent fires `StopFailure` carrying a subagent's `agent_id` (the documented hook payload pattern when an in-flight Task subagent terminates abnormally — observed empirically in 98% of dthn's 1573 rate-limit episodes), detach the matching native `SubagentRef` from the parent frame. Today this code path is a no-op (`frame_ops.go:288-300`), so native refs accumulate forever.
- **Phase 2 (PR-B, SPA)**: trailing-edge sliding debounce for `derived='error'` desktop notifications, keyed by `compositeKey + raw_event_name + detail.error`. Suppresses error-storm notification spam while preserving the unread badge.

Phase 3 is operational (one-shot dthn cleanup), not a code change.

**Estimated size**: ~250 LOC daemon (PR-A) + ~150 LOC SPA (PR-B), plus ~600 LOC tests across both PRs. Two separate PRs (correctness vs UX policy).

---

## 1. Symptom & motivation

### 1.1 User-reported gap

tmux session `dthn` (cc agent) accumulated **3944 native `SubagentRef`** entries in `agent_frames.subagents_json` (pane `%50`). The SPA shows the cc tab as "subagent permanently in flight" because every hook broadcast carries the full ref list. Restarting cc was the only way to clear the lights.

Concurrently, the user receives ~1.5 desktop notifications/sec for `rate_limit` errors during a Claude Code rate-limit storm — every `PdxStopFailure` re-fires `derived='error'` → `shouldNotify` → OS notification. cc already shows the rate-limit error in its own UI, so the SPA's flood adds noise without information.

### 1.2 Root cause — data evidence

Chain-level reconciliation against `agent_trace_chains` / `agent_trace_steps` (trace retention window ~6h):

| Metric | Value |
|---|---|
| Distinct `agent_id` in `PdxSubagentStart` (window) | 1573 |
| → followed by `PdxSubagentStop` (same `agent_id`) | **32 (2.0%)** |
| → followed by `PdxStopFailure` (same `agent_id`, `error=rate_limit`) | **1541 (97.9%)** |
| → followed by neither | 0 |
| → followed by both | 0 (mutually exclusive) |

Outside the trace retention window, `subagents_json` carries 1432 additional "orphan" refs whose original `SubagentStart` predates trace retention. Each of those refs has a corresponding `PdxStopFailure` row inside the window — i.e. the same code path leaks them.

**Hook payload shape** (`agent_trace_steps.payload_json`, dthn chain `c02151f0...`):
```json
{
  "session_id": "f828329e-...",
  "agent_id": "aac56e6312afceb04",
  "agent_type": "general-purpose",
  "hook_event_name": "StopFailure",
  "error": "rate_limit",
  "last_assistant_message": "You're out of extra usage · resets 2:30pm (Asia/Taipei)"
}
```
The `agent_id` field uniquely identifies the failing **subagent** (not the cc main session). `agent_type=general-purpose` confirms the same — cc's main agent type is `cc`, not `general-purpose`.

### 1.3 Why the daemon doesn't detach

`internal/module/agent/frame_ops.go:288-300` (alpha.289):
```go
case agentpkg.LifecycleStop, agentpkg.LifecycleStopFailure:
    if frame != nil {
        break  // ← cc main frame exists → fall through, no native ref handling
    }
    // L2 codex turn-aware proxy detach (only fires when frame == nil)
```
And `internal/module/agent/sweep.go:398-447` (`pruneDeadProxyRefs`):
```go
for _, ref := range frame.Subagents {
    if !ref.IsProxy { continue }  // ← native refs unreachable
    ...
}
```
Native refs have no other GC path. `LifecycleSessionEnd` deletes the whole frame and would clean them, but cc keeps the main session alive across rate-limit retries, so SessionEnd never fires.

### 1.4 Why notification noise is decoupled

Even after PR-A lands and `subagents_json` stops accumulating, every `PdxStopFailure` still derives to `Status=error` and triggers `useNotificationDispatcher` with `derived='error'`. The hook firing rate is bounded by cc's retry backoff (~1.5 Hz observed); 1 desktop notification per cc Task error is reasonable, ~90/min is not.

### 1.5 Documented behaviour vs emergent

The cc hook contract is not formally documented for the "subagent fails with rate_limit" branch. The 98% observed pattern in dthn's `agent_trace_chains` is treated here as a **high-confidence empirical observation**, not a guaranteed upstream invariant. Phase 1 is therefore designed to be **idempotent and safe even if the assumption is partially wrong** (see §3.4 risk).

---

## 2. Cross-validated facts

### 2.1 Existing `StopFailure` handling parity

`internal/module/agent/frame_ops_l2_test.go` already exercises `StopFailure` parity with `Stop` for codex turn-aware proxy detach (test names containing "stopfailure parity"). Phase 1 extends the same parity concept from proxy refs to native refs, on the cc/opencode wildcard branch.

### 2.2 cc derive does not surface `agent_id` for `PdxStopFailure`

`internal/agent/cc/status.go:83-91` (current):
```go
case "PdxStopFailure":
    return agent.DeriveResult{
        Valid:  true,
        Status: agent.StatusError,
        Detail: map[string]any{
            "error_details": raw["error_details"],
            "error":         raw["error"],
        },
    }
```
`agent_id` is in `raw_event` but not in `result.Detail`. Phase 1 must either (a) extend the derive to add `agent_id` to `Detail`, or (b) parse `req.RawEvent` directly in the handler. Option (a) keeps the boundary clean (provider owns the raw→Detail mapping); chosen.

`internal/agent/codex/status.go:67` and `internal/agent/opencode/status.go:27` use the same pattern and need the same one-line addition for parity. cc emits `PdxStopFailure` reliably (3005 observed in dthn telemetry). codex emits `PdxStopFailure` from agent SDK (catalog entry `FutureOnly:true`). opencode currently emits `PdxStopFailure` via `internal/agent/opencode/plugin_template.go`'s `session.error` handler, but the payload **does not include `agent_id`** — so even with the derive change applied, opencode's native detach remains a silent no-op until the plugin template is updated to surface the failing subagent's `agent_id`. The derive change is still made for parity (cheap, future-proofs the day opencode adds the field), and the test matrix marks the opencode "currently inactive native-detach behaviour, derive contract pinned" status explicitly.

### 2.3 `updateSubagents` is data-idempotent but `mutateSubagentsWithRetry` is **not** observation-idempotent

`internal/module/agent/frame_ops.go:872-895`:
```go
case agentpkg.LifecycleSubagentStop:
    filtered := make([]agentpkg.SubagentRef, 0, len(current))
    for _, existing := range current {
        if !subagentRefMatches(existing, ref) {
            filtered = append(filtered, existing)
        }
    }
    return filtered
```
A `LifecycleSubagentStop` mutation against a ref list that does not contain a matching ref returns the equivalent (de-duplicated) slice — **data unchanged**.

However, the persistence wrapper `mutateSubagentsWithRetry` (`frame_ops.go:1244-1267`) does NOT propagate this no-op signal:
```go
current.Subagents = updateSubagents(current.Subagents, lifecycle, ref)  // may be unchanged
current.LastSeenAt = broadcastTs                                        // ALWAYS refreshes
ok, stored, err := m.frames.UpsertIfUnchanged(current, expected)        // commits the row
if ok {
    return true, stored, nil  // ← `applied=true` even when Subagents didn't change
}
```
`applied=true` only signals "DB write succeeded under optimistic-concurrency", not "the target ref was actually present and detached". Any caller that interprets `applied=true` as proof of detach will broadcast a phantom event.

**Implication for Phase 1**: PR-A must pre-check ref presence via a pure read helper (§3.2) before invoking `mutateSubagentsWithRetry`. The mutate helper is still safe to call on a missing ref (writes the same `Subagents` back with refreshed `LastSeenAt`), but the caller must not claim a detach happened.

### 2.4 `subagentRefMatches` for native refs

`frame_ops.go:913-932` — native refs match on `ID` alone (cross-kind never matches). So a `StopFailure` synthesised `SubagentRef{ID: agent_id, Type: frame.AgentType, IsProxy: false}` will only match native refs with the same `ID`. Proxy refs with the same `ID` (impossible by construction — proxy IDs are `proxy:<type>:<pid>:<startTime>`) are not at risk.

### 2.5 SPA notification dispatcher current state

`spa/src/hooks/useNotificationDispatcher.ts`:
- `shouldNotify` (lines 60-93) gates on `derived in {waiting, idle, error}` plus suppression rules.
- `derived='error'` always passes when not focused, regardless of how recently the same session had an error.
- Per-session/event dedup uses `prevEvents → currentEvents` set diff (lines ~82) to detect "new event arrived" — this is **not** a time-window debounce; it just compares two snapshots.

PR-B adds a parallel debounce layer specifically for `derived='error'`.

---

## 3. Design — Phase 1 (PR-A, daemon)

### 3.1 Provider derive change

For all three providers (cc, codex, opencode), `PdxStopFailure` derive adds `agent_id` to `Detail`:
```go
case "PdxStopFailure":
    return agent.DeriveResult{
        Valid:  true,
        Status: agent.StatusError,
        Detail: map[string]any{
            "error_details": raw["error_details"],
            "error":         raw["error"],
            "agent_id":      raw["agent_id"],  // NEW; nil-safe (raw["agent_id"] returns nil if absent)
        },
    }
```

When the upstream payload omits `agent_id` (e.g. main-session `StopFailure`), `raw["agent_id"]` is `nil`, downstream extracts `""` via type assertion and skips the detach branch — preserving today's behaviour for that case.

### 3.2 Handler `LifecycleStopFailure` extension

`frame_ops.go:288` `case agentpkg.LifecycleStop, agentpkg.LifecycleStopFailure:` — split the joint case:

New pure helper in `frame_ops.go` (placed adjacent to `findProxyRefByBroker`):
```go
// findNativeRefByID returns the index of a native (non-proxy) ref with the
// given ID, or -1 when no such ref exists. Pure read, no side effects.
// Callers use this to gate destructive native-ref operations so that a
// missing ref is never reported as a successful detach.
func findNativeRefByID(refs []agentpkg.SubagentRef, id string) int {
    if id == "" {
        return -1
    }
    for i, r := range refs {
        if !r.IsProxy && r.ID == id {
            return i
        }
    }
    return -1
}
```

`applyFrameEvent` switch:
```go
case agentpkg.LifecycleStopFailure:
    // Native subagent failure path: payload's agent_id identifies the
    // failing subagent (not the main session). Empirically this is how
    // cc reports rate-limit and other subagent terminations (~98% of
    // observed Task failures in dthn telemetry, 2026-05-03).
    //
    // Two-step gate: pre-check ref presence with the pure helper, then
    // mutate. mutateSubagentsWithRetry's `applied=true` does NOT mean a
    // ref was removed — it only means UpsertIfUnchanged committed
    // (LastSeenAt always refreshes). Without the pre-check we'd
    // broadcast a phantom "detached" trace step on every mismatched
    // payload (§2.3).
    if frame != nil {
        agentID, _ := result.Detail["agent_id"].(string)
        if agentID == "" || findNativeRefByID(frame.Subagents, agentID) < 0 {
            // No payload agent_id, or payload references a ref we don't
            // hold. Trace as no_match for observability; preserve legacy
            // post-switch UpdateHookPath / projection refresh by breaking.
            // Note: we do NOT mutate frame here. Even though
            // mutateSubagentsWithRetry would be data-safe (returns same
            // slice), it would refresh LastSeenAt and consume an OCC
            // attempt for no benefit.
            break
        }
        ref := agentpkg.SubagentRef{
            ID:   agentID,
            Type: frame.AgentType,  // §2.4: native match by ID alone
        }
        applied, stored, merr := m.mutateSubagentsWithRetry(*frame, agentpkg.LifecycleSubagentStop, ref, broadcastTs)
        if merr != nil {
            return nil, FrameTraceMeta{}, merr
        }
        if !applied {
            // Frame deleted mid-flight (concurrent sweep / SessionEnd).
            // Trace as frame_missing so handler treats it like the
            // frame == nil branch.
            projection, perr := m.projectPane(req.TmuxPaneID)
            return projection, FrameTraceMeta{
                Decision: "skipped",
                Reason:   "frame_missing",
                Before:   before,
                After:    map[string]any{},
            }, perr
        }
        projection, perr := m.projectPane(req.TmuxPaneID)
        return projection, FrameTraceMeta{
            FrameID:       stored.FrameID,
            ParentFrameID: stored.ParentFrameID,
            Decision:      "updated_frame",
            Reason:        "native_subagent_detached_on_stop_failure",
            Before:        before,
            After:         summarizeFrame(&stored),
        }, perr
    }
    // frame == nil: fall through to existing codex/proxy detach below.
    fallthrough
case agentpkg.LifecycleStop:
    // ... existing logic at frame_ops.go:288-424 unchanged ...
```

Trace reasons:
- **New**: `native_subagent_detached_on_stop_failure` — agent_id matched a native ref, ref removed
- **Reused (existing)**: `frame_missing` — `findNativeRefByID` ≥0 but `mutateSubagentsWithRetry` returned `applied=false` (concurrent SessionEnd / sweep deleted the frame mid-flight). Same string already used elsewhere in `frame_ops.go` for the same semantic ("frame disappeared between read and write"), so reuse is intentional, not collision.
- **No-match no-op (no new reason)**: `findNativeRefByID < 0` → `break` → legacy post-switch `UpdateHookPath` path (status/lastSeen update, normalized broadcast). Existing trace pipeline records that branch as it always has.

Existing `proxy_subagent_detached_on_stop` covers the `frame==nil` branch and is unchanged.

### 3.3 Why `Type: frame.AgentType` (not raw `agent_type` from payload)

The native ref's `Type` field drives SPA `SubagentDots` colour family logic. Existing code stores `frame.AgentType` (e.g. `cc`) at `SubagentStart` time (`frame_ops.go:164`). For the detach match to find the existing ref, the synthesised ref's `Type` must equal the stored ref's `Type`, which is `frame.AgentType` — not the payload's `agent_type` (`general-purpose`, a sub-variant). `subagentRefMatches` actually only compares `ID` for natives (§2.4), so `Type` is informational here, but using `frame.AgentType` keeps the ref-construction pattern consistent with `frame_ops.go:157-169` where `Type` is set the same way.

### 3.4 Risk: false-positive detach

If a `PdxStopFailure` payload carries an `agent_id` that **happens** to collide with a still-live native `SubagentRef.ID` despite not actually referring to that subagent, Phase 1 will detach a ref that should still be lit. Consequence: SPA shows one fewer dot for ~as long as the subagent is genuinely running.

Mitigation:
- Native `agent_id` is cc-controlled (cc's internal Task ID). Collision requires cc to issue identical IDs to two different subagents in the same frame, which contradicts cc's own Task tool semantics.
- Pre-check via `findNativeRefByID` (§3.2) ensures the detach ONLY fires when a matching ref actually exists. A spoofed/stale `agent_id` that doesn't match any held ref is a silent no-op trace step, not a phantom detach broadcast.
- Even if collision happens and detach fires, the worst observable effect is a transiently-missing dot — the genuinely-running subagent's eventual `SubagentStop` against an empty list is itself a no-op (`updateSubagents` returns equivalent slice). No status corruption.

This risk is judged acceptable in exchange for fixing the unbounded accumulation. AC4 enforces the no-phantom-broadcast contract via tests.

### 3.5 Trace observability

Add a new step reason constant `native_subagent_detached_on_stop_failure` in `frame_ops.go` (search for existing `proxy_subagent_detached_on_stop` and place adjacent). Existing trace pipeline propagates it through `applyFrameEvent → handleEvent → trace.Frame()` without other changes.

---

## 4. Design — Phase 2 (PR-B, SPA debounce)

### 4.1 Debounce semantics

**Trailing-edge sliding window**, not fixed-window throttle:

- First `derived='error'` for a given `(compositeKey, eventName, detailError)` triple: notify, set `silentUntil = now + WINDOW_MS`.
- Subsequent `derived='error'` arrivals while `now < silentUntil`: do not notify, **extend** `silentUntil = now + WINDOW_MS`.
- Subsequent arrivals after `now ≥ silentUntil`: notify again, reset window.

User-facing semantics: "one notification per error episode, where an episode ends when no error of the same kind arrives for `WINDOW_MS` consecutive ms."

`WINDOW_MS = 60_000`. Rationale: rate-limit storms typically last for the entire reset window (5–10 min) once they begin; 60s of silence is a strong signal the episode is genuinely over. Not the rate-limit reset duration directly, because the user already sees the rate-limit message in cc's own UI (per user input 2026-05-03).

### 4.2 Key composition

```ts
const debounceKey = JSON.stringify([compositeKey, normalizedEventName, String(detailError ?? '')])
```

`JSON.stringify` over a 3-element array is collision-free across any string content (separator characters in `compositeKey` / `eventName` / `detailError` are escaped, unlike a `|`-joined string). Stable string output that survives Map key equality.

Three components:
1. `compositeKey` (host:sessionCode) — different sessions/hosts notify independently.
2. `normalizedEventName` — different event names (e.g. `StopFailure` vs `Stop`) notify independently.
3. `detail.error` — `rate_limit` storm doesn't suppress a different error type (`auth_failed`, etc.) on the same session.

`detailError` defaults to empty string (not undefined) so `error=undefined` events still get a stable key. All "missing-error" events on a session collapse to one debounce bucket; this is the conservative choice (under-notify rather than over-notify on unknown error categories).

### 4.3 State location

Module-level `Map<string, { silentUntil: number }>` in `useNotificationDispatcher.ts`. Not zustand. Rationale: ephemeral dispatcher-private state, no cross-component subscription, no persistence. Aligns with the existing `seenAtRef` pattern (current dedup is also module-level state).

### 4.4 Cleanup

Two cleanup paths:
1. **Per-session / per-host**: on `clearSession(hostId, sessionCode)` and `removeHost(hostId)` — iterate Map keys and decode each via `JSON.parse(key)` (the array form `[compositeKey, eventName, errorString]`). Compare `parsedKey[0]` to the target compositeKey, or compare `parsedKey[0].split(':')[0]` to the target hostId for `removeHost`. Prefix string match does **not** work because keys are JSON-stringified arrays (`"[\"hostA:sX\",\"Stop\",\"\"]"`). Helper `forEachDebounceKey((parsed, rawKey) => ...)` keeps the parse-once-per-iteration pattern in one place.
2. **TTL self-cleanup**: in the `shouldNotify` hot path, if an existing entry's `silentUntil < now - 5×WINDOW_MS` it is stale and can be removed in-place. Bounds memory across long-running app.

### 4.5 Scope limits

- Only gates `derived === 'error'`. `waiting` and `idle` notifications stay one-per-event.
- Does **not** suppress unread badge (`useAgentStore.unread[ck]` mutation in `handleNormalizedEvent`). The badge stays sticky regardless of debounce.
- Does **not** suppress hook broadcasts at the daemon side. Trace, history, and DB writes are unchanged.

### 4.6 `shouldNotify` integration

```ts
function buildDebounceKey(ck: string, eventName: string, detailError: unknown): string {
  return JSON.stringify([ck, eventName, String(detailError ?? '')])
}

function shouldNotify(args): boolean {
  // ... existing checks ...
  if (derived === 'error') {
    const key = buildDebounceKey(ck, eventName, args.detail?.error)
    const now = Date.now()
    const entry = errorDebounceState.get(key)
    if (entry && now < entry.silentUntil) {
      entry.silentUntil = now + WINDOW_MS  // extend
      return false
    }
    errorDebounceState.set(key, { silentUntil: now + WINDOW_MS })
    // fall through, return true at end
  }
  // ... existing return logic ...
}
```

Same `buildDebounceKey` helper used by cleanup paths (§4.4) so encode/decode stay symmetric.

---

## 5. Design — Phase 3 (operational)

After PR-A merges and a fresh daemon binary is in place, **SQL cleanup is the expected primary path**, not a fallback.

Reasoning: PR-A only handles `PdxStopFailure` events arriving *after* the fix is deployed. dthn's 3944 historical refs were terminated by `PdxStopFailure` events that already happened — their corresponding rows are recorded in `agent_trace_chains` (those still inside retention) and gone from the upstream stream. cc's hook contract issues each `agent_id` exactly once across `SubagentStart` / `SubagentStop` / `StopFailure`; a ref that already received its terminal event will never receive another. Therefore self-heal cannot recover historical refs.

Operational sequence:
1. Confirm PR-A daemon binary is deployed (`pdx --version` ≥ next alpha).
2. Confirm no second `pdx serve` instance is running (`pgrep -lf 'pdx serve'` should return one PID — the production daemon — and nothing else; abort if a dev instance is also live).
3. Stop daemon (`brew services stop pdx` or `kill <pid>`).
4. **Backup** affected rows before destructive UPDATE:
   ```sql
   .headers on
   .mode insert agent_frames_pre_cleanup_backup
   SELECT pane_id, frame_id, agent_type, subagents_json, last_seen_at
   FROM agent_frames
   WHERE json_array_length(subagents_json) > 100;
   ```
   Pipe to a `.sql` file (`> ~/.config/pdx/backups/2026-05-03-pre-cleanup.sql`) so a `RESTORE` is a one-liner if the cleanup turns out wrong.
5. **Inspect** which panes actually need cleanup:
   ```sql
   SELECT pane_id, agent_type, json_array_length(subagents_json) AS n_refs
   FROM agent_frames
   WHERE json_array_length(subagents_json) > 100
   ORDER BY n_refs DESC;
   ```
6. **Cleanup** in a transaction so a partial failure rolls back:
   ```sql
   BEGIN;
   UPDATE agent_frames SET subagents_json='[]'
   WHERE json_array_length(subagents_json) > 100;
   COMMIT;
   ```
   Threshold `100` is conservative — well above any plausible live subagent count, well below dthn's 3944. Adjust per inspection output if a different cutoff fits.
7. Start daemon. In-memory `m.subagents` map rebuilds from DB on first projection.
8. SPA reconciles via the next hook broadcast (or via WS replay on reconnect).

Self-heal observation is **opportunistic** — useful only if a long-running cc session is still firing live `PdxStopFailure` events whose `agent_id` happens to match an existing ref (rare, since terminated subagents don't fire again).

Not a code change. Captured here so PR-A's verification plan can reference it.

---

## 6. TDD test plan

### 6.1 PR-A daemon tests (`internal/module/agent/`)

| Test | Behaviour asserted |
|---|---|
| `TestStopFailure_NativeDetach_Hits` | `frame.Subagents` has native ref `{ID: X}`; `PdxStopFailure` payload `{agent_id: X}` arrives; ref removed; trace reason `native_subagent_detached_on_stop_failure` |
| `TestStopFailure_NativeDetach_Misses_NoMutation` | `frame.Subagents` empty (or contains only refs whose IDs differ from payload `agent_id`); payload `{agent_id: X}` arrives; assertions: (a) `applyFrameEvent` returned `FrameTraceMeta.Reason != "native_subagent_detached_on_stop_failure"` (the only behaviourally-distinct trace signal of the new code path); (b) `mutateSubagentsWithRetry` not invoked — verified either via a spy/call-counter wrapper around the frame store interface, or by asserting the trace pipeline emits the legacy `UpdateHookPath` step rather than a frame-level mutation step. Plan §test-instrumentation will pick the concrete mechanism; "subagents bit-identical pre/post" alone is **not** sufficient evidence (a miss-ref mutate would also write back the same slice). (c) Legacy post-switch `UpdateHookPath` path DOES execute — `frame.Status` becomes `error`, `LastSeenAt` refreshes, normalized event broadcast — preserving v0 behaviour bit-exactly for unrecognised payloads. Together (a)+(b)+(c) prove pre-check correctness without breaking legacy semantics. |
| `TestStopFailure_NoAgentId_LegacyBehaviour` | payload omits `agent_id` field entirely; existing `frame != nil` break path preserved; no detach, no trace change |
| `TestStopFailure_EmptyAgentId_LegacyBehaviour` | payload contains `agent_id: ""` (explicit empty string from upstream); same behaviour as missing field. Locks down the empty-string branch since `findNativeRefByID` returns -1 early when id is empty |
| `TestStopFailure_PreservesProxyRefs` | `frame.Subagents` has `{ID:X, IsProxy:false}` AND `{ID:Y, IsProxy:true, SourcePID:P}`; payload `{agent_id:X}`; only X removed, Y untouched |
| `TestStopFailure_FrameNil_FallthroughToProxyDetach` | `frame == nil`; payload `{agent_id:X}`; existing codex/proxy detach branch reached unchanged |
| `TestStopFailure_FrameDeletedMidFlight` | `findNativeRefByID` returns ≥0 but `mutateSubagentsWithRetry` returns `applied=false` (concurrent SessionEnd / sweep deleted frame); trace reason `frame_missing` |
| `TestStopFailure_FixtureReplay_DthnPayload` | Replay a recorded production `PdxStopFailure` payload from dthn telemetry against a frame seeded with the corresponding native ref; assert ref removed. Acts as version-pinned regression guard against cc upstream payload-shape drift |
| `TestStopFailureDerive_AgentIdInDetail` (per provider × 3) | cc/codex/opencode `deriveStatus("PdxStopFailure", {agent_id: "x"})` returns `Detail["agent_id"] == "x"` |
| `TestStopFailureDerive_AgentIdMissing` | derive when raw payload omits `agent_id` field → `Detail["agent_id"] == nil` (cc/codex) or absent (opencode `detailSubset`) |
| `TestFindNativeRefByID_*` | Pure helper unit tests: hits native ref by ID; returns -1 for proxy refs with same ID; returns -1 for empty id; returns -1 for not-found |

Optimistic-concurrency retry path (`mutateSubagentsWithRetry`) is already covered by existing tests; this PR doesn't change its contract.

### 6.2 PR-B SPA tests (`spa/src/hooks/`)

| Test | Behaviour asserted |
|---|---|
| `debounce__first_error_passes` | First `derived=error` for a key returns `shouldNotify=true` |
| `debounce__second_error_within_window_blocked_and_extends` | Second arrival 30s later: `false`, `silentUntil` extended to `t2 + 60s` |
| `debounce__error_after_silence_window_passes` | Arrival 70s after last: `true` (window expired) |
| `debounce__storm_100_events_yields_one_notification` | Quantitative AC5 anchor: drive 100 consecutive `derived=error` events on the same key over 60s simulated time; assert exactly **1** call passes through. Also asserts the sliding extension never lets a second notification slip through at the original window boundary |
| `debounce__different_keys_independent` | Same session, different `detail.error` → both pass; same error, different sessions → both pass; same error, same session, different `eventName` → both pass |
| `debounce__key_uses_json_array_not_pipe_join` | Key built from `compositeKey` containing `\|` and `detail.error` containing `\|` does NOT collide. Locks down JSON-stringify approach |
| `debounce__clear_session_resets` | `clearSession` removes key; next arrival passes |
| `debounce__remove_host_resets` | `removeHost` removes all keys for that host; next arrival on a session under that host passes |
| `debounce__waiting_not_debounced` | `derived=waiting` for the same session passes every time |
| `debounce__unread_badge_unaffected` | Even when notification suppressed, `useAgentStore.unread[ck]=true` still set by `handleNormalizedEvent` |
| `debounce__ttl_cleanup` | After 5×WINDOW_MS no activity for a key, `shouldNotify` removes stale entries (memory bound test) |
| `debounce__test_reset_helper` | `__resetDebounceStateForTests` clears module-level Map between tests so test order doesn't matter |

---

## 7. Acceptance criteria

- **AC1**: dthn class symptom gone — after PR-A + the §5 SQL cleanup, `agent_frames.subagents_json` for any cc pane no longer accumulates beyond live subagent count under sustained `PdxStopFailure` traffic.
- **AC2**: Existing `Stop`/`StopFailure` proxy-detach behaviour for `frame == nil` path bit-identical (regression guard via existing `frame_ops_l2_test.go`).
- **AC3**: `PdxStopFailure` with no `agent_id` (or empty-string `agent_id`, or `agent_id` not matching any held native ref) preserves legacy behaviour: no mutation, no phantom trace step.
- **AC4**: PR-A never broadcasts a phantom "detached" event. The pre-check `findNativeRefByID` ensures the `native_subagent_detached_on_stop_failure` trace reason is only emitted when a ref was actually removed.
- **AC5**: PR-B suppresses **≥98.8%** of redundant `derived=error` notifications during a sustained storm. Reference scenario: 100 consecutive same-key events within 60s yields exactly 1 notification (99% suppression). Real-world dthn-class storm at 1.5 Hz over 33 min ≈ 3000 events / 1 notification = 99.97%.
- **AC6**: PR-B preserves unread badge semantics — sticky after first error, doesn't clear on subsequent debounced arrivals.
- **AC7**: PR-B `clearSession` / `removeHost` clean debounce state for the affected keys.
- **AC8**: Both PRs ship with no new lint / type errors and full test coverage of the matrix in §6.
- **AC9**: PR size cap: PR-A ≤ 500 LOC production+tests; PR-B ≤ 300 LOC production+tests. (Spec docs not counted.) **AC9 amendment 2026-05-04**: cap is informational, not blocking. PR-A landed at ~945 LOC (R2 race-safety additions); PR-B landed at ~498 LOC after R2 finding fixes (sweep throttle + hard cap + colon-safe cleanup + unread suppression test rewrite). The §6.2 12-row test matrix plus per-test `beforeEach`/`afterEach` isolation + `lastEvents` seeding for cleanup tests + R2 sweep-throttle / hard-cap / colon-safe / unread-suppression coverage exceed the original conservative budget. Future similarly-scoped PRs should treat AC9 as a planning anchor rather than a hard ceiling — surface the overage in the PR body and verify each test row maps to a spec contract before approving.

---

## 8. Risks / known limitations

| # | Risk | Mitigation / acceptance |
|---|---|---|
| R1 | cc upstream changes how `StopFailure` payload is shaped | If `agent_id` no longer appears, pre-check returns -1 → silent no-op (legacy break path). `TestStopFailure_FixtureReplay_DthnPayload` (§6.1) acts as version-pinned regression guard so silent failure to detach is detected at PR-CI time, not runtime |
| R2 | False-positive native detach (§3.4) | Pre-check via `findNativeRefByID` ensures detach only fires on a real held ref. Worst case: one missing dot, transient |
| R3 | PR-B 60s window misses a "second wave" rate-limit storm | User intent (§1.4): cc UI shows error already; missed notification is preferable to flood. Window can be tuned later via env/config if needed |
| R4 | Module-level Map memory leak | TTL self-cleanup in §4.4 + per-session cleanup hooks bound the working set. Test: `debounce__ttl_cleanup` |
| R5 | Different cc Task subagent's `agent_id` collides with a live one | cc's Task tool issues unique IDs by design; collision is a cc upstream bug, not ours to defend against |
| R6 | dthn-style accumulated `subagents_json` (3944 refs) bloats projection / replay cost on daemon restart | Phase 3 SQL cleanup is the **expected** primary mitigation (§5 reframed). PR-A alone cannot recover historical refs. Once cleared, ongoing PR-A behaviour prevents re-accumulation. Out of scope: schema-level GC like `last_seen_at` per ref (would require migration; not justified for a one-time cleanup) |
| R7 | opencode `PdxStopFailure` upstream emission status uncertain | §2.2 / §9 acknowledge the uncertainty. Plan must verify against opencode plugin fixtures before finalizing the opencode derive change. If opencode never emits this event in practice, the derive change is dead-code-safe (no behavioural drift) |

---

## 9. Out of scope

- Rate-limit retry-throttling for cc itself (upstream concern)
- daemon-side broadcast suppression / coalescing (would mask trace fidelity)
- adding a `last_seen_at` timestamp to native refs to enable a TTL sweep (broader schema change; revisit if Phase 1 turns out insufficient)
- opencode plugin extending `PdxStopFailure` payload with `agent_id` (currently emits the event but without `agent_id`, so opencode native detach is a silent no-op until the plugin template is updated; that template change is out of scope here)
- Migrating PR-B's debounce state into zustand (architectural divergence, no concrete benefit)
- SPA-side suppression of `unread` badge during error storms (deliberately preserved)

---

## 10. Verification plan

1. **PR-A unit tests**: `go test ./internal/module/agent/... ./internal/agent/...` green.
2. **PR-A live verification (mlab)**: with daemon built from PR-A, simulate dthn-class flow via `pdx hook --agent cc PdxSubagentStart` then `pdx hook --agent cc PdxStopFailure` with the same `agent_id` and confirm `subagents_json` in DB drops by one ref. Replay with mismatched `agent_id` and confirm: (a) no `mutateSubagentsWithRetry` invocation in trace; (b) frame `Status` still updates to `error` and `LastSeenAt` still refreshes (legacy behaviour preserved).
3. **PR-A dthn cleanup**: after PR-A merges to main and a fresh daemon binary is deployed, run §5 SQL operational sequence directly. Self-heal observation is **not** the primary recovery path — historical refs whose terminal `StopFailure` is already in the past will never be re-emitted by cc, so observation alone won't drain the accumulated 3944 refs. After SQL cleanup, watch `SELECT pane_id, json_array_length(subagents_json) FROM agent_frames` for one cc Task cycle to confirm zero re-accumulation under live `PdxStopFailure` traffic.
4. **PR-B unit tests**: `cd spa && npx vitest run` for `useNotificationDispatcher.test.ts` green.
5. **PR-B manual SPA**: trigger 30 consecutive `derived=error` events for a session in dev; confirm exactly one OS notification fires, unread badge sticks, subsequent same-error events do not re-trigger.

---

## 11. PR sequencing

- **PR-A first**: correctness fix; lands without behavioural change for existing `frame == nil` paths.
- **dthn cleanup** (operational, expected primary path per §5): immediately after PR-A merges and daemon restarts. SQL `UPDATE agent_frames SET subagents_json='[]'` for affected panes.
- **PR-B second**: UX-only; depends conceptually on PR-A (without it, debounce hides the symptom of unbounded ref accumulation), but does not technically depend on PR-A code changes — could land independently if needed.
- **One bump PR per merged feature PR**, per CLAUDE.md convention.
