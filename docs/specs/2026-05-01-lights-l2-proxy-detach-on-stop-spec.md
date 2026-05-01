# Spec — Lights L2: Proxy detach on Stop (v3 — turn-aware identity, a+b mixed)

**Kickoff**: `kickoff_codex_broker_and_lights_governance.md` §燈號 L2
**Date**: 2026-05-01 (v1) / 2026-05-02 (v2, v3)
**Branch**: `worktree-lights-l2-proxy-detach`
**Baseline**: origin/main `5d40e2a2` (alpha.280)
**Scope**: When a long-lived codex broker finishes a logical dispatch (turn), detach the proxy `SubagentRef` from the parent frame so the lit dot extinguishes — without waiting for the broker process to die.

**v3 supersedes v2** — v2 was blocked by Round-3 codex review (`task-mon4zbs3-ceswha`) for three substantive issues plus six wording/coverage gaps. v3 incorporates all 9 findings plus the a+b mixed strategy from F2 follow-up consulting (`task-mon9h5l9-byczvd`). Review history: v1 → block (`task-mon01q2i-i88vwf`) → v2 → block (`task-mon4zbs3-ceswha`) + F2 consult (`task-mon9h5l9-byczvd`) → v3.

**Estimated size**: ~380-550 LOC (production + tests + catalog + new lifecycle wiring). Single-PR but mid-large — use two-round codex review (standard + 3-parallel adversarial).

---

## 1. Symptom & current state

User-reported: `purdex-big-plan` screenshot — codex review job completed (broker emitted Stop), but the proxy dot on the parent cc tab kept blinking.

The codex `app-server-broker.mjs` is detached cross-session by design — it stays alive across many turns so the next dispatch can reuse the warm broker (governance spec §1). Codex upstream **does not define a `SessionEnd` hook event at all** (`hooks/src/lib.rs:10-17`); broker death cleanup is governance P2/P3 territory. Without targeted Stop detach, the proxy `SubagentRef` attached at `frame_ops.go:230` stays on the parent's `Subagents` slice indefinitely.

### 1.1 The race that v1 missed

`SubagentRef` proxy identity in v1 was `(SourcePID, SourceStartTime)` — both stable across the broker's entire lifetime. Two consecutive `/codex:rescue` dispatches against the same broker share that identity:

```
T0: dispatch 1 SessionStart → attach ref(PID=42, t1)
T1: dispatch 1 ... Stop 1     (in-flight to daemon)
T2: dispatch 2 starts          (broker reuses same PID, sub_id=turn_2)
T3: dispatch 2 first hook arrives at daemon
```

If T3 lands before T1 in daemon's HTTP handler queue, dispatch 2's hook sees the existing ref via `subagentRefMatches((PID,StartTime))` → idempotent no-op. T1's Stop then detaches the ref. **Dispatch 2 runs with no proxy dot** — strictly worse than current.

`mutateSubagentsWithRetry`'s optimistic-concurrency only self-heals the inverse ordering (T1 before T3). The ordering Round-1 review surfaced is real and unfixable without finer identity.

### 1.2 Broker exclusivity narrows but doesn't close the window

`app-server-broker.mjs:62-80` enforces **one active request/stream at a time**: a busy broker rejects new socket connections. So **same broker cannot multiplex turns concurrently**. This narrows the race to in-flight hook events for sequential turns — but the race is still there because daemon's HTTP handler is per-event goroutine (no per-pane serialization, see `handler.go:306`), and the two hook OS processes (`pdx hook` CLIs) for Stop 1 and the next-turn first hook race independently to reach daemon.

Daemon needs turn-aware identity.

## 2. Codex hook semantics (cross-validated facts)

These are upstream-confirmed facts the v3 design depends on. Sources cited from codex 1.0.2 + main branch.

### 2.1 `turn_id` is the dispatch identifier

Each codex turn carries a stable `sub_id` set in `turn_context`. All these hook events fill `turn_id` from `turn_context.sub_id` and stay consistent within one turn:

| Hook | Has `turn_id`? | Cite |
|------|---------------|------|
| `SessionStart` | ❌ | [`session_start.rs:31-38`](https://github.com/openai/codex/blob/main/codex-rs/hooks/src/events/session_start.rs#L31-L38) |
| `UserPromptSubmit` | ✅ | [`user_prompt_submit.rs:18-27`](https://github.com/openai/codex/blob/main/codex-rs/hooks/src/events/user_prompt_submit.rs#L18-L27), [`hook_runtime.rs:272-292`](https://github.com/openai/codex/blob/main/codex-rs/core/src/hook_runtime.rs#L272-L292) |
| `PreToolUse` | ✅ (when handler provides payload) | [`hook_runtime.rs:126-144`](https://github.com/openai/codex/blob/main/codex-rs/core/src/hook_runtime.rs#L126-L144), [`registry.rs:1792-1796`](https://github.com/openai/codex/blob/main/codex-rs/core/src/tools/registry.rs#L1792-L1796) |
| `PostToolUse` | ✅ | [`hook_runtime.rs:209-233`](https://github.com/openai/codex/blob/main/codex-rs/core/src/hook_runtime.rs#L209-L233) |
| `PermissionRequest` | ✅ | [`permission_request.rs:31-42`](https://github.com/openai/codex/blob/main/codex-rs/hooks/src/events/permission_request.rs#L31-L42) |
| `Stop` | ✅ | [`hook_runtime.rs:246-260`](https://github.com/openai/codex/blob/main/codex-rs/core/src/hook_runtime.rs#L246-L260) |
| `SessionEnd` | **N/A — codex has no SessionEnd hook event** | [`hooks/src/lib.rs:10-17`](https://github.com/openai/codex/blob/main/codex-rs/hooks/src/lib.rs#L10-L17) |

### 2.2 `SessionStart` is one-shot per pending source

`run_pending_session_start_hooks` consumes the pending source via `take_pending_session_start_source()` and returns false if there isn't one ([`hook_runtime.rs:94-122`](https://github.com/openai/codex/blob/main/codex-rs/core/src/hook_runtime.rs#L94-L122)). New thread (`startThread`) → fires SessionStart once. Resume thread (`resumeThread`) on a broker that already holds the session → typically does **not** re-create a pending source → SessionStart does **not** re-fire. The first hook the daemon sees for a resumed dispatch is `UserPromptSubmit` (if dispatch carries user input) or `PreToolUse` (tool-only).

### 2.3 No `SessionEnd` from codex — broker death cleanup is governance territory

L2 does not change broker death handling. SessionEnd-driven detach paths in `frame_ops.go:79-127` are for **cc** and **opencode** (which do emit SessionEnd) and remain unchanged.

### 2.4 Not every turn fires `UserPromptSubmit` — but most fire `PreToolUse`

`run_user_prompt_submit_hooks` is called only when `input` is non-empty or pending input contains a `TurnItem::UserMessage` ([`turn.rs:124-161`](https://github.com/openai/codex/blob/main/codex-rs/core/src/session/turn.rs#L124-L161), [`turn.rs:285-314`](https://github.com/openai/codex/blob/main/codex-rs/core/src/session/turn.rs#L285-L314)). These turn types skip UserPromptSubmit:

- `input.is_empty()` continuation turns (tool-only follow-up)
- `review/start` turns
- `thread/compact/start` turns
- mailbox/queued continuations without a UserMessage

For these, the **first hook the daemon sees is `PreToolUse`** (when at least one tool fires). PreToolUse carries `turn_id` per §2.1. So adding PreToolUse as a third attach/upsert trigger covers review/compact/tool-only turns. **Strategy a (extend PreToolUse) is adopted in v3** — see §3.3 below.

**Caveat**: not every tool fires PreToolUse. `ToolHandler::pre_tool_use_payload()` has default `None` — only handlers that provide a payload trigger the hook ([`registry.rs:1792-1796, 2286-2304`](https://github.com/openai/codex/blob/main/codex-rs/core/src/tools/registry.rs#L1792-L1796)). Empty-input + pre-tool-payload-None turns therefore still don't trigger any L2 attach. **Strategy b (acknowledge residual stale-light)** — see §3.5 known limitation L1.

### 2.5 Provider hook spec divergence (fallback strategy)

| Provider | Dispatch identity | Cite |
|----------|------------------|------|
| codex | `turn_id` (per-turn sub_id) | §2.1 above |
| cc | none — hook payload has `session_id`, `transcript_path`, `cwd`, `hook_event_name` only ([Anthropic hooks reference](https://docs.anthropic.com/en/docs/claude-code/hooks)) | falls back to `(PID, StartTime)` |
| opencode | `messageID` from `chat.message` event ([`plugin_template.go:96-123`](../../internal/agent/opencode/plugin_template.go)) | falls back to `(session_id, messageID)` already in `PdxUserPromptSubmit` payload, OR `(PID, StartTime)` |

**v3 only treats codex with full turn-aware identity**. cc/opencode keep current `(PID, StartTime)` matching — see §3.5 known limitation L3 for the evidence boundary.

## 3. Strategy

### 3.1 Identity model

Add field to `internal/agent/subagent.go::SubagentRef`:

```go
type SubagentRef struct {
    // ... existing fields ...
    SourceTurnID string `json:"source_turn_id,omitempty"`  // v3: codex turn-aware identity
}
```

`omitempty` keeps wire format backward-compatible. No DB migration: `subagents_json TEXT` is opaque JSON blob (`frames.go:42`); old rows deserialize to `SourceTurnID = ""` cleanly (Go json zero-value semantics).

### 3.2 Two distinct lookup helpers (F1 fix)

v2's blocker was conflating two semantically different lookups into `subagentRefMatches`. v3 separates them:

**A. `subagentRefMatches` — turn-aware equality, used by Stop targeted detach**

```go
func subagentRefMatches(a, b SubagentRef) bool {
    if a.IsProxy != b.IsProxy { return false }
    if a.IsProxy {
        if a.SourcePID != b.SourcePID || a.SourceStartTime != b.SourceStartTime {
            return false
        }
        if a.SourceTurnID == "" || b.SourceTurnID == "" {
            return true  // process-level fallback for unset-turn refs
        }
        return a.SourceTurnID == b.SourceTurnID  // turn-level equality
    }
    return a.ID == b.ID
}
```

**B. `findProxyRefByBroker` (new, unexported) — process-level lookup, used by attach/upsert**

```go
// findProxyRefByBroker returns the index of the proxy ref matching this
// broker (PID + StartTime), regardless of SourceTurnID. Used for upsert/replace
// where we want to mutate a single broker's ref to a new turn_id, not insert
// a duplicate. Returns -1 if not found.
func findProxyRefByBroker(refs []SubagentRef, pid int, startTime string) int {
    for i, r := range refs {
        if r.IsProxy && r.SourcePID == pid && r.SourceStartTime == startTime {
            return i
        }
    }
    return -1
}
```

These two helpers serve different intents and intentionally have different semantics. Inline comments cross-reference each other to prevent future "DRY them up" refactors.

### 3.3 Triggers — three attach paths + one targeted detach path

**A. SessionStart attach (existing, kept)**
- `frame_ops.go:210-249` SessionStart fast-path: attach proxy ref with `SourceTurnID=""` (codex SessionStart doesn't carry turn_id; §2.1)
- Behavior unchanged from current code

**B. UserPromptSubmit attach/upsert (new)**
- New case in `applyFrameEvent` lifecycle switch: `case agentpkg.LifecycleUserPromptSubmit:`
- Only acts when `frame == nil` and a proxy parent exists (mirrors SessionStart fast-path's `frame == nil` gate)
- Parses `turn_id` from `req.RawEvent` via new `parseCodexTurnID(req.AgentType, req.RawEvent) string` (returns "" on error, agent mismatch, or missing field — fail-soft)
- Calls new `upsertProxyRefForBroker` helper:
  - If `findProxyRefByBroker(parent.Subagents, pid, startTime) >= 0` → replace `SourceTurnID` in-place via narrow column update (NOT via `mutateSubagentsWithRetry` SubagentStart, which would skip the mutation)
  - If not found → append new ref with `SourceTurnID=req.TurnID`

**C. PreToolUse attach/upsert (new — strategy a from F2)**
- Reuses the **same** lifecycle case body as B (UserPromptSubmit) — both trigger upsert with the parsed turn_id
- Requires catalog change: `internal/agent/codex/events.go:91-97` — `PdxPreToolUse` lifecycle moves from `LifecycleNone` + `HookHandlingUnsupported` to `LifecycleUserPromptSubmit` (semantically the same intent for L2: "broker is starting work for a turn") + `HookHandlingHandled`
- Rationale: PreToolUse is the canonical attach trigger for non-prompt turns (review/compact/tool-only) per §2.4. Reusing `LifecycleUserPromptSubmit` keeps the switch case count minimal; the alternative (new `LifecycleToolUse`) would force a wider lifecycle vocabulary change without semantic benefit for L2

**D. Stop targeted detach (new — the L2 core)**
- New case: `case agentpkg.LifecycleStop, agentpkg.LifecycleStopFailure:`
- If `frame != nil` → `break` (standalone agent, lights-rebuild ProbeIntent dispatcher handles status update)
- If `frame == nil`:
  - Parse `turn_id` from `req.RawEvent`
  - Three sub-cases (F3 fix — codex parse-failure conservatism):

    | Provider | Parsed turn_id | Action | Trace reason |
    |----------|---------------|--------|--------------|
    | codex | non-empty | targeted detach via new `removeProxyRefForSenderTurn(paneID, pid, startTime, turnID)` — only removes ref where ALL three identity fields match | `proxy_subagent_detached_on_stop_turn` |
    | codex | empty + ref exists with non-empty `SourceTurnID` | **skip detach** (governance sweep handles); emit conservative trace | `proxy_subagent_stop_parse_failed` |
    | codex | empty + ref exists with empty `SourceTurnID` (SessionStart attached but no UserPromptSubmit/PreToolUse upsert) | wildcard detach via `removeProxyRefForSender` | `proxy_subagent_detached_on_stop` |
    | cc / opencode | (any) | wildcard detach via `removeProxyRefForSender` | `proxy_subagent_detached_on_stop` |

**E. SessionEnd wildcard detach (existing, unchanged)**
- `frame_ops.go:79-127` SessionEnd branch: keeps existing process-level detach. Semantically correct (whole broker dying, all turns gone). cc/opencode emit SessionEnd; codex doesn't (§2.3) — cc/opencode unaffected, codex never enters this path.

**F. sweep + recovery attach call sites (existing, SourceTurnID="" — F4 fix)**
- `frame_ops.go:993` `reconcileCreatedFrameAsProxy` (post-Upsert canonicalize)
- `frame_ops.go:1133` `canonicalizeDescendantsAfterUpsert`
- `sweep.go:202` sweep canonicalize
- All three are **daemon-side recovery / canonicalize paths** with no hook event context → no turn_id available → set `SourceTurnID=""`. A subsequent codex Stop with non-empty turn_id will skip detach (per §3.3.D conservative fallback) and wait for the next UserPromptSubmit/PreToolUse upsert to populate turn_id, OR for governance sweep cleanup. Documented as expected.

### 3.4 In-place upsert mechanics

The upsert in §3.3.B/C must mutate a single proxy ref's `SourceTurnID` field on an existing parent frame without losing other refs. Implementation:

1. Reload parent frame via `m.frames.GetByIdentity(...)` (same pattern as `mutateSubagentsWithRetry`)
2. Call `findProxyRefByBroker` (§3.2.B) to locate the ref index
3. If found: mutate `refs[i].SourceTurnID = newTurnID` and `refs[i].StartedAt = broadcastTs` (refresh recency)
4. If not found: append new ref with full `(PID, StartTime, turnID, IsProxy=true, ID=...)`
5. Write via `UpsertIfUnchanged(frame, expected)` with optimistic concurrency retry (max `proxyUpsertMaxAttempts=3`, same as existing helpers)
6. On retry conflict: re-read parent, re-run steps 2-5 (fresh `findProxyRefByBroker` against current `refs`)

This is a **new helper** `upsertProxyRefForBroker(parent, pid, startTime, turnID, broadcastTs) (bool, store.Frame, error)` — does NOT reuse `mutateSubagentsWithRetry` because that helper's SubagentStart branch uses `subagentRefMatches` (turn-aware), which would treat a `(PID, StartTime, turn_b)` request against an existing `(PID, StartTime, turn_a)` ref as "no match → append" (would create the duplicate F1 warned about).

### 3.5 Known limitations (residual stale-light — strategy b from F2)

1. **Empty-tool turns (PreToolUse pre_tool_use_payload=None)** — extremely rare turns that have no UserMessage AND whose tools all return `None` from `pre_tool_use_payload()`. Their Stop carries a turn_id with no upsert ever recording it on the ref. **Behavior**: Stop sees `SourceTurnID != stop.turn_id` → skip detach (per §3.3.D conservative). **Outcome**: stale-light persists until next upsert overwrites turn_id, or governance sweep removes the broker. This is an **accepted residual limitation**, not "correct UX". (F2.b explicit acknowledgment per Round-3 finding 2.)
2. **Out-of-order attach (UserPromptSubmit/PreToolUse before late Stop)** — if dispatch 2's first hook lands before dispatch 1's Stop, the upsert overwrites `SourceTurnID` to `t_2`; dispatch 1's Stop (carrying `t_1`) finds no match → no detach. The dot transitions seamlessly from dispatch 1 to dispatch 2. **This is intentional** (visually correct for back-to-back dispatches), but means dispatch 1's "completion" is implicit — observable only via the late Stop's `proxy_subagent_stop_no_match` trace.
3. **cc/opencode evidence boundary (F6 fix)** — current cc/opencode integrations tie logical session lifecycle to process/session lifecycle, and no broker redispatch path exists in this repo. Any future long-lived proxy/broker for these providers must add a per-dispatch identity (e.g. opencode `messageID` or a new identity field) **before** enabling Stop-based proxy detach. v3 codifies this as a forward-looking constraint, not a permanent claim of safety.
4. **Codex schema evolution** — turn_id parsing is fail-soft (returns ""); a future schema change degrades to v1 behavior selectively per §3.3.D table (NOT blanket wildcard — F3 fix). Failure mode is observable via trace reason `proxy_subagent_stop_parse_failed`.

## 4. Modifications

| File | Change | LOC est |
|------|--------|---------|
| `internal/agent/subagent.go` | Add `SourceTurnID string` field with `json:"source_turn_id,omitempty"` | ~5 |
| `internal/agent/codex/events.go` | `PdxPreToolUse`: `Lifecycle: LifecycleUserPromptSubmit` (was `LifecycleNone`); `Handling: HookHandlingHandled` (was `HookHandlingUnsupported`); remove `FutureOnly` if set | ~10 |
| `internal/agent/codex/hooks.go` | Confirm `checkCodexEvent` handles new lifecycle classification correctly (likely no change; verify in implementation) | 0-10 |
| `internal/module/agent/raw_codex_event.go` *(new)* | `parseCodexTurnID(agentType, rawEvent) string` — returns "" on AgentType != "codex", JSON parse error, or missing field | ~30 |
| `internal/module/agent/frame_ops.go` | (a) `subagentRefMatches` turn-aware update (§3.2.A). (b) New `findProxyRefByBroker` helper (§3.2.B). (c) New `upsertProxyRefForBroker` helper (§3.4). (d) New `removeProxyRefForSenderTurn` + `detachProxyRefForSenderTurnWithRetry` mirroring existing helpers. (e) New `LifecycleUserPromptSubmit` case in `applyFrameEvent` switch (handles both UserPromptSubmit and PreToolUse via shared upsert path). (f) New `LifecycleStop, LifecycleStopFailure` case with three-sub-case dispatch (§3.3.D table). (g) Inline comments in §3.2.A/B helpers cross-referencing each other to prevent DRY refactor regressions | ~200-280 |
| `internal/module/agent/handler.go` | No change (RawEvent already in EventRequest) | 0 |
| `internal/module/agent/frame_ops_test.go` | New test file/section `TestApplyFrameEvent_TurnAwareProxyDetach` with full table matrix (§5) | ~280-400 |
| **Total** | | **~525-735 LOC raw / ~380-550 LOC effective** (counting comment lines once) |

## 5. Test matrix

`TestApplyFrameEvent_TurnAwareProxyDetach`:

| # | Setup | Action | Expected | Validates |
|---|-------|--------|----------|-----------|
| 1 | parent cc + 1 ref(PID=42, t1, turnID="") (SessionStart attached) | UserPromptSubmit from PID=42, raw turn_id="t_a" | `findProxyRefByBroker` hits → in-place upsert SourceTurnID="t_a" | §3.3.B + §3.4 in-place upsert |
| 2 | parent cc + 1 ref(PID=42, t1, turnID="") | PreToolUse from PID=42, raw turn_id="t_a" | same as #1 (PreToolUse uses same upsert path) | §3.3.C catalog wiring |
| 3 | parent cc + 1 ref(PID=42, t1, turnID="t_a") | Stop from PID=42, raw turn_id="t_a" | targeted detach succeeds | §3.3.D codex non-empty turn_id |
| 4 | parent cc + 1 ref(PID=42, t1, turnID="t_a") | Stop from PID=42, raw turn_id="t_b" | ref kept (turn mismatch), trace `proxy_subagent_stop_no_match` | dispatch 1 late Stop |
| 5 | parent cc + 1 ref(PID=42, t1, turnID="t_a") | UserPromptSubmit from PID=42, raw turn_id="t_b" | ref's SourceTurnID overwritten t_a→t_b in-place (single ref still, NOT appended) | §3.4 explicit no-duplicate guard |
| 6 | parent cc + 1 ref(PID=42, t1, turnID="t_a") | StopFailure from PID=42, raw turn_id="t_a" | targeted detach succeeds | StopFailure parity with Stop |
| 7 | parent cc + 0 refs (resumeThread first dispatch, no SessionStart) | UserPromptSubmit from PID=42, raw turn_id="t_a" | first-time attach via append, ref(PID=42, t1, turnID="t_a") created | §3.3.B resumeThread |
| 8 | parent cc + 1 ref(PID=42, t1, turnID="t_a") | Stop from PID=42, raw missing/malformed turn_id, AgentType=codex | **skip detach**, trace `proxy_subagent_stop_parse_failed` | §3.3.D codex parse-failure conservative |
| 9 | parent cc + 1 ref(PID=42, t1, turnID="") | Stop from PID=42, raw turn_id="" or malformed, AgentType=codex | wildcard detach via `removeProxyRefForSender`, trace `proxy_subagent_detached_on_stop` | §3.3.D codex empty-ref wildcard fallback |
| 10 | parent cc + 1 ref(PID=42, t1, turnID="t_a") + 1 ref(PID=43, t2, turnID="t_x") (two brokers) | Stop from PID=42, raw turn_id="t_a" | only PID=42 ref detached, PID=43 untouched | multi-broker isolation |
| 11 | parent cc + 1 ref(PID=42, t1, turnID="t_a") | SessionEnd from PID=42 (no turn_id, AgentType=cc as proxy) | wildcard detach via existing path (unchanged) | §3.3.E SessionEnd unchanged regression guard |
| 12 | sender owns own frame (frame != nil) + standalone Stop | Stop from sender's own PID | sender's frame.Subagents unchanged, no proxy detach attempted | §3.3.D `frame != nil` short-circuit |
| 13 | parent cc + 1 native ref(IsProxy=false, ID="task-x") | Stop from PID=42, raw turn_id="t_a" | native ref untouched (subagentRefMatches IsProxy gate) | native isolation |
| 14 | parent cc + 1 ref(PID=42, t1, turnID="t_a"). Sequential: Stop(t_a) → Stop(t_a) again | call 1: detached / call 2: no-op (already gone, trace `proxy_subagent_stop_no_match`) | idempotent | retry-safe |
| 15 | parent cc + 1 ref(PID=42, t1, turnID=""). Sequential: UserPromptSubmit(t_a) → PreToolUse(t_a) → PreToolUse(t_a) → Stop(t_a) | call 1: upsert turnID=t_a / call 2-3: same-turn no-op (turnID already t_a, no change) / call 4: targeted detach | full lifecycle + same-turn idempotent | end-to-end happy path + F5 same-turn no-op |
| 16 | parent cc + 1 ref(PID=42, t1, turnID="t_a"). Concurrent: goroutine A calls UserPromptSubmit(t_b), goroutine B calls Stop(t_a). `sync.WaitGroup.Wait` after both | **Final state is exactly one of**: (i) ref(PID=42, t1, turnID="t_b") with no Stop detach trace; OR (ii) detach trace `proxy_subagent_detached_on_stop_turn` followed by attach trace with new ref(PID=42, t1, turnID="t_b"). **Forbidden final states**: zero refs, two refs (any combination), single ref with turnID="t_a" | F5 explicit no-dup / no-zero-ref / no-stale-turn guard | concurrency safety under turn changes |
| 17 | opencode UserPromptSubmit (no codex turn_id in raw, AgentType=opencode) | attach via existing path | ref attached with SourceTurnID="" (provider fallback) | §2.5 opencode fallback unchanged |
| 18 | cc Stop with no turn_id in raw, ref has empty SourceTurnID | wildcard detach via §3.3.D fallback | ref detached process-level | §2.5 cc fallback unchanged |
| 19 | parent cc + 1 ref(PID=42, t1, turnID="t_a") | Stop from PID=42 with **stale SourceStartTime=t_OLD** (PID-reused scenario) | ref kept (SourceStartTime mismatch), trace `proxy_subagent_stop_no_match` | F7 PID-reuse safety |
| 20 | parent cc + 0 refs | PreToolUse from PID=42 with no proxy parent in pane | trace `user_prompt_without_proxy_parent` (or PreToolUse equivalent), no attach | F4 no-parent fallback path |

Rows 1-9 are codex-specific lifecycle. Row 10 = multi-broker isolation. Rows 11-13 = regression guards (SessionEnd / standalone / native). Rows 14-15 = idempotency. Row 16 = concurrency (F5 strict assertions). Rows 17-18 = cc/opencode fallback. Row 19 = PID-reuse (F7). Row 20 = no-parent fallback (F4).

### 5.1 Concurrency test pattern

Row 16 uses `sync.WaitGroup` + two goroutines + frame store fake mutex — same pattern as `internal/store/agent_event_test.go:198` (per Round-3 finding 5 cite). Each goroutine calls `m.applyFrameEvent` directly with a stub `EventRequest`. After `Wait()`, the test reads parent.Subagents and asserts on the three forbidden states explicitly via separate assertions (not just an `Equal` check that conflates outcomes).

## 6. Out of scope

- **Governance P2/P3** (broker kill/sweep) — separate workstream. L2 does not extend broker lifetime; only changes when proxy dot extinguishes.
- **Standalone codex Stop main-agent light** — governed by `worktree-probe-intent-bidirectional-grace` (J3 dispatcher pre-grace). L2 leaves `frame != nil` Stop handling unchanged.
- **Native subagent Stop dots** — SubagentStart/Stop already drive native dots correctly via `mutateSubagentsWithRetry`. L2 does not touch the SubagentStart/Stop case.
- **cc/opencode turn-aware identity** — those providers' hook payloads don't carry per-turn identity, and they don't have the long-lived broker problem. L2 does not retrofit them. Future requirement codified in §3.5 L3.
- **Empty-tool turn dot extinction** — §3.5 L1 acknowledged residual stale-light. Future fix (if user reports) would add Stop turn_id correlation against a daemon-local `lastTurnIDByRef` cache, not an upstream change.
- **L1 / L3 / L4** — separate phases per kickoff.

## 7. Risks & mitigations

| Risk | Mitigation |
|------|-----------|
| Codex schema drops/renames `turn_id` | `parseCodexTurnID` fail-soft. §3.3.D table covers all four parse outcomes. Followup phase if upstream changes. |
| `findProxyRefByBroker` and `subagentRefMatches` semantically diverge over time | Inline cross-reference comments (§3.2 + §4(g)). PR review checklist asserts both helpers exist and aren't merged. |
| In-place upsert in `upsertProxyRefForBroker` introduces new retry path | Reuses existing UpsertIfUnchanged etag mechanism. Row 16 explicitly tests concurrency. |
| Catalog change for `PdxPreToolUse` (`HookHandlingUnsupported` → `Handled`) breaks existing code paths | grep `HookHandlingUnsupported` and `LifecycleNone` consumers in `internal/agent/codex/` to confirm no path depends on `PdxPreToolUse` being unsupported. |
| Empty-tool turns leave stale-light | §3.5 L1 acknowledged. Followup metric: count `proxy_subagent_stop_parse_failed` and `proxy_subagent_stop_no_match` traces in production to gauge frequency. |
| Trace reason vocabulary expansion | New reasons: `proxy_subagent_detached_on_stop_turn`, `proxy_subagent_detached_on_stop` (existing reuse for wildcard), `proxy_subagent_stop_no_match`, `proxy_subagent_stop_parse_failed`, `proxy_subagent_upserted_on_user_prompt`, `proxy_subagent_attached_on_user_prompt`, `user_prompt_without_proxy_parent`. AC enforces `rg` checklist (§8 AC5). |
| `req.RawEvent` parser bypass attack | Reuses existing 64KB cap in `path_hint_extractor.go:15`. New parser shares the same Unmarshal path. |
| Provider drift between cc/opencode/codex paths in same code | Codex-specific logic isolated in `raw_codex_event.go` and gated by `req.AgentType == "codex"`. cc/opencode paths route to existing wildcard helpers untouched. |
| Hook propagation latency unclear | §9 verification uses **trace timestamps** (daemon-side) rather than wall-clock guesses. |

## 8. Acceptance criteria

| AC | Description |
|----|-------------|
| **AC1** | Production code adds no new exported types. `SourceTurnID` is the only new exported API surface, as a field on existing `SubagentRef`. New helpers (`findProxyRefByBroker`, `upsertProxyRefForBroker`, `removeProxyRefForSenderTurn`, `detachProxyRefForSenderTurnWithRetry`, `parseCodexTurnID`) are unexported. Test-only helpers in `_test.go` are unrestricted. |
| **AC2** | All 20 test rows in §5 pass, including row 16 concurrency assertions. |
| **AC3** | All existing tests in `internal/module/agent/...` and `internal/agent/...` pass unchanged (test edits limited to the new test file, plus a JSON round-trip test for `SubagentRef` if one exists in `subagent_test.go`). |
| **AC4** | `cd spa && pnpm run lint && pnpm run build` and `go build ./... && go test ./...` pass. Race tests (`go test -race ./internal/module/agent/...`) pass for the new test rows, especially row 16. |
| **AC5** | Trace reason vocabulary stays additive; PR review must run `rg 'proxy_subagent_(detached|upserted|attached|stop)_on_(stop|user_prompt)|proxy_subagent_stop_(no_match|parse_failed)|user_prompt_without_proxy_parent'` and confirm only the §7-listed strings exist. |
| **AC6** | LOC bound: `frame_ops.go` change ≤ 280 lines including comments; new `raw_codex_event.go` ≤ 50 lines; catalog change in `events.go` ≤ 10 lines; new test ≤ 400 lines. Total PR diff ≤ 850 lines including spec + plan + tests. |
| **AC7** | `SubagentRef` JSON round-trip preserves `SourceTurnID` when set, omits it when empty (`omitempty` semantics). |
| **AC8** | `PdxPreToolUse` catalog change does not break existing `internal/agent/codex/` consumers — verify via `grep HookHandlingUnsupported` + `grep LifecycleNone` for any path that depends on PreToolUse being unhandled. |

## 9. Verification (post-merge live check)

On `mlab` after deploying the daemon update, capture daemon trace logs and verify:

1. **Single dispatch with prompt**: spawn `/codex:rescue` from cc tab → SessionStart attaches `(PID, StartTime, "")` → UserPromptSubmit upserts `SourceTurnID=t_a` → Stop detaches `(PID, StartTime, t_a)`. Trace sequence: `proxy_subagent_attached` → `proxy_subagent_upserted_on_user_prompt` → `proxy_subagent_detached_on_stop_turn`.
2. **Sequential dispatches**: dispatch 1 finishes, dispatch 2 starts. Verify ref's `SourceTurnID` transitions `t_1 → t_2` via UserPromptSubmit upsert (row 5 / row 16). Dot stays lit continuously. Trace shows `proxy_subagent_upserted_on_user_prompt` between dispatches.
3. **Resume-thread (same broker)**: keep the same broker process alive, dispatch on an existing/resumed thread. Verify **no new SessionStart trace appears** and UserPromptSubmit (or PreToolUse) performs the turn upsert. (F9 fix — was incorrectly testing new-broker case in v2.)
4. **Rapid-fire sequential dispatch**: trigger 3 back-to-back `/codex:rescue` dispatches via concurrent terminal commands. Verify final state has no zero-ref, no duplicate refs, single ref with the latest dispatch's turn_id.
5. **Tool-only turn (review/compact)**: trigger codex `review/start` (or use `/codex:adversarial-review` if it routes through review). Verify PreToolUse upserts turn_id → Stop detaches → dot extinguishes. Confirms strategy a coverage for non-prompt turns.
6. **Empty-tool turn (residual)**: if reproducible (rare), verify trace shows `proxy_subagent_stop_parse_failed` (or no-match) and dot stays lit until next upsert. Confirms §3.5 L1 documented behavior.
7. **Concurrent dispatch on different brokers**: verify multi-broker isolation (row 10).

Verification framework checks **trace reasons**, not wall-clock timing, since hook propagation latency varies per `daemon-hook-pipeline-lag-analysis §2.5`.

## 10. Single-PR delivery

Spec, plan, implementation, tests in one PR (kickoff phase 2). **Two-round codex review** required given turn-aware identity touches concurrency-critical paths AND modifies the catalog (governance kickoff §407 calls catalog changes "謹慎區"):

- **Round 1**: standard review focused on identity model correctness, in-place upsert race-safety, fallback behavior across providers, catalog migration safety.
- **Round 2**: 3-parallel adversarial — attack (find race / boundary / PID-reuse / catalog-consumer break), defense (validate provider isolation / resumeThread coverage / strategy a+b boundary), file-health (frame_ops growth / SRP / new helper organization).

Followup issues filed for: opencode `messageID` retrofit if a similar bug surfaces (§3.5 L3 forward constraint), governance P2/P3 sweep handling of empty-tool stale-light refs (§3.5 L1 cleanup mechanism), codex hook schema capability detection.
