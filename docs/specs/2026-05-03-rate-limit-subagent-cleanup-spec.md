# Spec — Rate-limit subagent cleanup + notification debounce

**Date**: 2026-05-03
**Branch**: `worktree-rate-limit-cleanup`
**Baseline**: origin/main `401a785c` (alpha.289)
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

`internal/agent/codex/status.go:67` and `internal/agent/opencode/status.go:27` use the same pattern and need the same one-line addition for parity. opencode plugin currently does not emit `StopFailure` (no upstream event source per `events.go`), but the catalog entry exists; codex emits `StopFailure` from agent SDK.

### 2.3 `updateSubagents` is idempotent

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
A `LifecycleSubagentStop` mutation against a ref list that does not contain a matching ref is a no-op (returns equivalent slice). This is the safety net that makes Phase 1 safe even if a `StopFailure` payload's `agent_id` does **not** correspond to a real native subagent.

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

```go
case agentpkg.LifecycleStopFailure:
    // Native subagent failure path: payload's agent_id identifies the
    // failing subagent (not the main session). Empirically this is how
    // cc reports rate-limit and other subagent terminations (~98% of
    // observed Task failures in dthn telemetry, 2026-05-03). When the
    // payload carries an agent_id and the parent frame holds a
    // matching native ref, detach it idempotently. updateSubagents is
    // a no-op for non-matching IDs, so a payload that doesn't actually
    // refer to a subagent (e.g. main-session StopFailure with a stale
    // agent_id) is naturally safe.
    if frame != nil {
        agentID, _ := result.Detail["agent_id"].(string)
        if agentID != "" {
            ref := agentpkg.SubagentRef{
                ID:   agentID,
                Type: frame.AgentType,  // §2.4 invariant: native match by ID alone
            }
            applied, stored, merr := m.mutateSubagentsWithRetry(*frame, agentpkg.LifecycleSubagentStop, ref, broadcastTs)
            if merr != nil {
                return nil, FrameTraceMeta{}, merr
            }
            if applied {
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
        }
        break  // no agent_id, or no matching ref — preserves legacy behaviour
    }
    // frame == nil: fall through to existing codex/proxy detach below.
    fallthrough
case agentpkg.LifecycleStop:
    // ... existing logic at frame_ops.go:288-424 unchanged ...
```

Trace reason `native_subagent_detached_on_stop_failure` is new; existing `proxy_subagent_detached_on_stop` covers the `frame==nil` branch and is unchanged.

### 3.3 Why `Type: frame.AgentType` (not raw `agent_type` from payload)

The native ref's `Type` field drives SPA `SubagentDots` colour family logic. Existing code stores `frame.AgentType` (e.g. `cc`) at `SubagentStart` time (`frame_ops.go:164`). For the detach match to find the existing ref, the synthesised ref's `Type` must equal the stored ref's `Type`, which is `frame.AgentType` — not the payload's `agent_type` (`general-purpose`, a sub-variant). `subagentRefMatches` actually only compares `ID` for natives (§2.4), so `Type` is informational here, but using `frame.AgentType` keeps the ref-construction pattern consistent with `frame_ops.go:157-169` where `Type` is set the same way.

### 3.4 Risk: false-positive detach

If a `PdxStopFailure` payload carries an `agent_id` that **happens** to collide with a still-live native `SubagentRef.ID` despite not actually referring to that subagent, Phase 1 will detach a ref that should still be lit. Consequence: SPA shows one fewer dot for ~as long as the subagent is genuinely running.

Mitigation:
- Native `agent_id` is cc-controlled (cc's internal Task ID). Collision requires cc to issue identical IDs to two different subagents in the same frame, which contradicts cc's own Task tool semantics.
- Even if collision happens, `updateSubagents` removes one ref; if a later `PdxSubagentStop` re-fires with the same ID against an empty list, that's also a no-op (idempotent). No status-leak corruption — the worst observable effect is a transiently-missing dot.

This risk is judged acceptable in exchange for fixing the unbounded accumulation. AC4 enforces the idempotency contract via tests.

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
const debounceKey = `${compositeKey}|${normalizedEventName}|${String(detailError ?? '')}`
```

Three components:
1. `compositeKey` (host:sessionCode) — different sessions/hosts notify independently.
2. `normalizedEventName` — different event names (e.g. `StopFailure` vs `Stop`) notify independently.
3. `detail.error` — `rate_limit` storm doesn't suppress a different error type (`auth_failed`, etc.) on the same session.

`detailError` defaults to empty string (not undefined) so `error=undefined` events still get a stable key.

### 4.3 State location

Module-level `Map<string, { silentUntil: number }>` in `useNotificationDispatcher.ts`. Not zustand. Rationale: ephemeral dispatcher-private state, no cross-component subscription, no persistence. Aligns with the existing `seenAtRef` pattern (current dedup is also module-level state).

### 4.4 Cleanup

Two cleanup paths:
1. **Per-session**: on `clearSession(hostId, sessionCode)` (agent store action) and `removeHost(hostId)` — wipe entries whose key prefix matches. Hooks already exist; debounce module subscribes.
2. **TTL self-cleanup**: in the `shouldNotify` hot path, if an existing entry's `silentUntil < now - 5×WINDOW_MS` it is stale and can be removed in-place. Bounds memory across long-running app.

### 4.5 Scope limits

- Only gates `derived === 'error'`. `waiting` and `idle` notifications stay one-per-event.
- Does **not** suppress unread badge (`useAgentStore.unread[ck]` mutation in `handleNormalizedEvent`). The badge stays sticky regardless of debounce.
- Does **not** suppress hook broadcasts at the daemon side. Trace, history, and DB writes are unchanged.

### 4.6 `shouldNotify` integration

```ts
function shouldNotify(args): boolean {
  // ... existing checks ...
  if (derived === 'error') {
    const key = `${ck}|${eventName}|${String(args.detail?.error ?? '')}`
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

---

## 5. Design — Phase 3 (operational)

After PR-A merges and a fresh daemon restart picks up the binary:

1. Observe whether `agent_frames.subagents_json` for pane `%50` (dthn) self-heals when cc fires the next `PdxStopFailure` (each one detaches one ref).
2. If self-heal is too slow (3944 refs × ~0.7s/event ≈ 45 min), one-shot SQL: `UPDATE agent_frames SET subagents_json='[]' WHERE pane_id='%50';` followed by `pdx serve` restart so the in-memory `m.subagents` map reloads from DB. SPA reconciles via the next hook broadcast.

Not a code change. Captured here so PR-A's verification plan can reference it.

---

## 6. TDD test plan

### 6.1 PR-A daemon tests (`internal/module/agent/`)

| Test | Behaviour asserted |
|---|---|
| `TestStopFailure_NativeDetach_Hits` | `frame.Subagents` has native ref `{ID: X}`; `PdxStopFailure` payload `{agent_id: X}` arrives; ref removed; trace reason `native_subagent_detached_on_stop_failure` |
| `TestStopFailure_NativeDetach_Misses_Idempotent` | `frame.Subagents` empty; payload `{agent_id: X}`; no error, no broadcast change, trace reason `proxy_subagent_stop_no_match` (existing) or new `native_subagent_no_match` — choose during plan |
| `TestStopFailure_NoAgentId_LegacyBehaviour` | payload omits `agent_id`; existing `frame != nil` break path preserved; no detach, no trace change |
| `TestStopFailure_PreservesProxyRefs` | `frame.Subagents` has `{ID:X, IsProxy:false}` AND `{ID:Y, IsProxy:true, SourcePID:P}`; payload `{agent_id:X}`; only X removed, Y untouched |
| `TestStopFailure_FrameNil_FallthroughToProxyDetach` | `frame == nil`; payload `{agent_id:X}`; existing codex/proxy detach branch reached unchanged |
| `TestStopFailureDerive_AgentIdInDetail` (per provider × 3) | cc/codex/opencode `deriveStatus("PdxStopFailure", {agent_id: "x"})` returns `Detail["agent_id"] == "x"` |
| `TestStopFailureDerive_AgentIdMissing` | derive omits `agent_id` from raw → `Detail["agent_id"] == nil` |

Optimistic-concurrency retry path (`mutateSubagentsWithRetry`) is already covered by existing tests; this PR doesn't change its contract.

### 6.2 PR-B SPA tests (`spa/src/hooks/`)

| Test | Behaviour asserted |
|---|---|
| `debounce__first_error_passes` | First `derived=error` for a key returns `shouldNotify=true` |
| `debounce__second_error_within_window_blocked_and_extends` | Second arrival 30s later: `false`, `silentUntil` extended to `t2 + 60s` |
| `debounce__error_after_silence_window_passes` | Arrival 70s after last: `true` (window expired) |
| `debounce__different_keys_independent` | Same session, different `detail.error` → both pass; same error, different sessions → both pass |
| `debounce__clear_session_resets` | `clearSession` removes key; next arrival passes |
| `debounce__waiting_not_debounced` | `derived=waiting` for the same session passes every time |
| `debounce__unread_badge_unaffected` | Even when notification suppressed, `useAgentStore.unread[ck]=true` still set |
| `debounce__ttl_cleanup` | After 5×WINDOW_MS no activity, `shouldNotify` removes stale entries |

---

## 7. Acceptance criteria

- **AC1**: dthn class symptom gone — after PR-A, `agent_frames.subagents_json` for any cc pane no longer accumulates beyond live subagent count.
- **AC2**: Existing `Stop`/`StopFailure` proxy-detach behaviour for `frame == nil` path bit-identical (regression guard via existing `frame_ops_l2_test.go`).
- **AC3**: `PdxStopFailure` with no `agent_id` (main-session failure) preserves legacy behaviour.
- **AC4**: PR-A is idempotent — replaying any `PdxStopFailure` against an already-detached ref is a no-op.
- **AC5**: PR-B suppresses ≥99% of redundant `derived=error` notifications during a 1.5 Hz storm (60s window @ 1.5 Hz = 1 notification per 90 events).
- **AC6**: PR-B preserves unread badge semantics — sticky after first error, doesn't clear on subsequent debounced arrivals.
- **AC7**: PR-B `clearSession` / `removeHost` clean debounce state for the affected keys.
- **AC8**: Both PRs ship with no new lint / type errors and full test coverage of the matrix in §6.
- **AC9**: PR size cap: PR-A ≤ 500 LOC production+tests; PR-B ≤ 300 LOC production+tests. (Spec docs not counted.)

---

## 8. Risks / known limitations

| # | Risk | Mitigation / acceptance |
|---|---|---|
| R1 | cc upstream changes how `StopFailure` payload is shaped | Phase 1 idempotent; if `agent_id` no longer appears, code becomes silent no-op. Plan §verification has live-fixture replay step |
| R2 | False-positive native detach (§3.4) | Bounded blast radius (one missing dot, transient); idempotency ACL covers replay |
| R3 | PR-B 60s window misses a "second wave" rate-limit storm | User intent (§1.4): cc UI shows error already; missed notification is preferable to flood. Window can be tuned later via env/config if needed |
| R4 | Module-level Map memory leak | TTL self-cleanup in §4.4 + per-session cleanup hooks bound the working set |
| R5 | Different cc Task subagent's `agent_id` collides with a live one | cc's Task tool issues unique IDs by design; collision is a cc upstream bug, not ours to defend against |

---

## 9. Out of scope

- Rate-limit retry-throttling for cc itself (upstream concern)
- daemon-side broadcast suppression / coalescing (would mask trace fidelity)
- adding a `last_seen_at` timestamp to native refs to enable a TTL sweep (broader schema change; revisit if Phase 1 turns out insufficient)
- opencode plugin emitting `StopFailure` (currently doesn't; out of scope here)
- Migrating PR-B's debounce state into zustand (architectural divergence, no concrete benefit)
- SPA-side suppression of `unread` badge during error storms (deliberately preserved)

---

## 10. Verification plan

1. **PR-A unit tests**: `go test ./internal/module/agent/... ./internal/agent/...` green.
2. **PR-A live verification (mlab)**: with daemon built from PR-A, simulate dthn-class flow via `pdx hook --agent cc PdxSubagentStart` then `pdx hook --agent cc PdxStopFailure` with the same `agent_id` and confirm `subagents_json` in DB drops by one ref. Replay with mismatched `agent_id` to confirm no-op.
3. **PR-A dthn cleanup**: after PR-A merges to main and a fresh daemon binary is in place, observe `agent_frames.subagents_json` for pane `%50` over 5 min — should drain at the rate cc fires `PdxStopFailure`. If still not converging fully, run the SQL from §5.
4. **PR-B unit tests**: `cd spa && npx vitest run` for `useNotificationDispatcher.test.ts` green.
5. **PR-B manual SPA**: trigger 30 consecutive `derived=error` events for a session in dev; confirm exactly one OS notification fires, unread badge sticks, subsequent same-error events do not re-trigger.

---

## 11. PR sequencing

- **PR-A first**: correctness fix; lands without behavioural change for existing `frame == nil` paths.
- **dthn cleanup** (operational, ad-hoc): immediately after PR-A merges and daemon restarts.
- **PR-B second**: UX-only; depends conceptually on PR-A (without it, debounce hides the symptom of unbounded ref accumulation), but does not technically depend on PR-A code changes — could land independently if needed.
- **One bump PR per merged feature PR**, per CLAUDE.md convention.
