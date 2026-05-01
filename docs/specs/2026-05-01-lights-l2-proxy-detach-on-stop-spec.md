# Spec — Lights L2: Proxy detach on Stop (v2 — turn-aware identity)

**Kickoff**: `kickoff_codex_broker_and_lights_governance.md` §燈號 L2
**Date**: 2026-05-01 (v1) / 2026-05-02 (v2)
**Branch**: `worktree-lights-l2-proxy-detach`
**Baseline**: origin/main `5d40e2a2` (alpha.280)
**Scope**: When a hook sender that is currently proxy-attached to a parent frame finishes a logical dispatch (broker turn), detach the proxy `SubagentRef` from the parent so the lit dot on the parent's tab goes out without waiting for the broker process to die.

**v2 supersedes v1 entirely** — v1 used `(SourcePID, SourceStartTime)`-only identity and was blocked by Round-1 codex review (`task-mon01q2i-i88vwf`) for failing to handle the long-lived broker re-dispatch race. v2 adopts turn-aware identity per Round-2 consulting (`task-mon1dp97-jhykb1`) + Round-3 cross-validated K verification (subagent + codex `task-mon1pg64-nnb9uc`).

**Estimated size**: ~300-450 LOC (production + tests). Single-PR but mid-sized — use two-round codex review (standard + adversarial).

---

## 1. Symptom & current state

User-reported: `purdex-big-plan` screenshot — a codex review job completed (broker emitted Stop), but the proxy dot on the parent cc tab kept blinking.

The codex `app-server-broker.mjs` is detached cross-session by design — it stays alive across many turns so the next dispatch can reuse the warm broker (governance spec §1). This means **codex `SessionEnd` hook never fires for the broker process during normal use** (and codex upstream actually doesn't define a `SessionEnd` hook event at all — see §2.3 below). The proxy `SubagentRef` attached at `frame_ops.go:230` therefore stays on the parent's `Subagents` slice until either:

- the broker process eventually dies (governance P3 territory, not L2), or
- `pruneDeadProxyRefs` (`frame_ops.go:1086`) runs at the next SessionStart on the parent frame and notices the source PID is dead — but PID stays alive, so this doesn't fire.

Neither helps for the user-visible symptom. The dot stays lit indefinitely.

### 1.1 The race that v1 missed (and why naïve `(PID, StartTime)` identity is wrong)

`SubagentRef` proxy identity is `(SourcePID, SourceStartTime)` — both stable across the broker's entire lifetime. Two consecutive `/codex:review` dispatches against the same broker share that identity. Race scenario:

```
T0: dispatch 1 SessionStart → attach ref(PID=42, t1)
T1: dispatch 1 ... Stop 1   (in-flight to daemon)
T2: dispatch 2 starts       (broker reuses same PID, sub_id=turn_2)
T3: dispatch 2 first hook arrives at daemon
```

If T3 lands before T1 in daemon's HTTP handler queue, dispatch 2's hook sees the existing ref via `subagentRefMatches((PID,StartTime))` → idempotent no-op. Then T1's Stop arrives and detaches the ref. **Dispatch 2 runs with no proxy dot lit** — strictly worse than current behavior.

`mutateSubagentsWithRetry`'s optimistic-concurrency only self-heals the inverse ordering (T1 before T3). The ordering Round-1 review surfaced is real and unfixable without finer identity.

### 1.2 What broker exclusivity buys us (and what it doesn't)

`app-server-broker.mjs:62-80` enforces **one active request/stream at a time**: a busy broker rejects new socket connections. So **same broker cannot multiplex turns concurrently**. This narrows the race to "in-flight hook events for sequential turns" — but the race is still there because daemon's HTTP handler is per-event goroutine (no per-pane serialization, see `handler.go:306`), and the two hook OS processes (`pdx hook` CLIs) for Stop 1 and the next-turn first hook race independently to reach daemon.

So we can't rely on broker exclusivity alone — daemon needs turn-aware identity.

## 2. Codex hook semantics (cross-validated facts)

These are upstream-confirmed facts the v2 design depends on. Sources cited from codex 1.0.2 + main branch.

### 2.1 `turn_id` is the dispatch identifier

Each codex turn carries a stable `sub_id` set in `turn_context`. All these hook events fill `turn_id` from `turn_context.sub_id` and stay consistent within one turn:

| Hook | Has `turn_id`? | Cite |
|------|---------------|------|
| `SessionStart` | ❌ | [`session_start.rs:31-38`](https://github.com/openai/codex/blob/main/codex-rs/hooks/src/events/session_start.rs#L31-L38) |
| `UserPromptSubmit` | ✅ | [`user_prompt_submit.rs:18-27`](https://github.com/openai/codex/blob/main/codex-rs/hooks/src/events/user_prompt_submit.rs#L18-L27) + [`hook_runtime.rs:272-292`](https://github.com/openai/codex/blob/main/codex-rs/core/src/hook_runtime.rs#L272-L292) |
| `PreToolUse` | ✅ | [`hook_runtime.rs:126-144`](https://github.com/openai/codex/blob/main/codex-rs/core/src/hook_runtime.rs#L126-L144) |
| `PostToolUse` | ✅ | [`hook_runtime.rs:209-233`](https://github.com/openai/codex/blob/main/codex-rs/core/src/hook_runtime.rs#L209-L233) |
| `PermissionRequest` | ✅ | [`permission_request.rs:31-42`](https://github.com/openai/codex/blob/main/codex-rs/hooks/src/events/permission_request.rs#L31-L42) |
| `Stop` | ✅ | [`hook_runtime.rs:246-260`](https://github.com/openai/codex/blob/main/codex-rs/core/src/hook_runtime.rs#L246-L260) |
| `SessionEnd` | **N/A — codex has no SessionEnd hook event** | [`hooks/src/lib.rs:10-17`](https://github.com/openai/codex/blob/main/codex-rs/hooks/src/lib.rs#L10-L17) |

### 2.2 `SessionStart` is one-shot per pending source

`run_pending_session_start_hooks` consumes the pending source via `take_pending_session_start_source()` and returns false if there isn't one ([`hook_runtime.rs:94-122`](https://github.com/openai/codex/blob/main/codex-rs/core/src/hook_runtime.rs#L94-L122)). This means:

- New thread (`startThread`) → fires SessionStart once
- Resume thread (`resumeThread`) on a broker that already holds the session → typically does **not** re-create a pending source → SessionStart does **not** re-fire
- The first hook the daemon sees for a resumed dispatch is **`UserPromptSubmit`** (assuming the dispatch carries user input; see §2.4)

### 2.3 No `SessionEnd` hook from codex — broker death cleanup is governance territory

Codex upstream's hook event list does not include `SessionEnd` ([`hooks/src/lib.rs:10-17`](https://github.com/openai/codex/blob/main/codex-rs/hooks/src/lib.rs#L10-L17)). When the broker process dies, **no codex hook fires** — broker death cleanup must come from:

- daemon process/socket sweep (governance P2/P3 — not L2's responsibility)
- `pruneDeadProxyRefs` periodic scan in the existing SessionStart filter-merge path (already implemented)
- `sweep_pid_dead_broadcast` (`sweep.go`) — already broadcasts when process dies

L2 does not change broker death handling. SessionEnd-driven detach paths in `frame_ops.go:79-127` are for **cc** and **opencode** providers (which do emit SessionEnd) and remain unchanged.

### 2.4 Not every turn fires `UserPromptSubmit`

`run_user_prompt_submit_hooks` is called from two paths in `run_turn` ([`turn.rs:124-161`](https://github.com/openai/codex/blob/main/codex-rs/core/src/session/turn.rs#L124-L161), [`turn.rs:285-314`](https://github.com/openai/codex/blob/main/codex-rs/core/src/session/turn.rs#L285-L314)):

1. Initial `run_turn` when `input` is non-empty
2. `inspect_pending_input` when pending input contains a `TurnItem::UserMessage`

So these turn types **do not fire UserPromptSubmit**:
- `input.is_empty()` continuation turns (tool-only follow-up)
- `review/start` turns (codex `/review` command, not the same as purdex `/codex:review`)
- `thread/compact/start` turns
- mailbox/queued continuations without a UserMessage

For these, the **first hook the daemon sees is a tool hook** (`PreToolUse` typically), which also carries `turn_id` (§2.1). So if we treat `PreToolUse` as another upsert trigger we cover them. But to keep scope manageable, **v2 treats UserPromptSubmit as the canonical upsert trigger** and accepts that non-prompt turns get `(PID, StartTime)`-only identity (see §3.4 known limitation).

### 2.5 Provider hook spec divergence (fallback strategy)

| Provider | Dispatch identity | Cite |
|----------|------------------|------|
| codex | `turn_id` (per-turn sub_id) | §2.1 above |
| cc | none — hook payload has `session_id`, `transcript_path`, `cwd`, `hook_event_name` only ([Anthropic hooks reference](https://docs.anthropic.com/en/docs/claude-code/hooks)) | falls back to `(PID, StartTime)` |
| opencode | `messageID` from `chat.message` event ([`plugin_template.go:96-123`](../../internal/agent/opencode/plugin_template.go)) | falls back to `(session_id, messageID)` already in `PdxUserPromptSubmit` payload, OR `(PID, StartTime)` |

**v2 only treats codex with full turn-aware identity**. cc/opencode keep current `(PID, StartTime)` matching — those providers don't have the long-lived broker problem (cc/opencode session lifecycles match process lifecycles), so the race in §1.1 doesn't apply to them.

## 3. Strategy

### 3.1 Identity model

Add field to `internal/agent/subagent.go::SubagentRef`:

```go
type SubagentRef struct {
    // ... existing fields ...
    SourceTurnID string `json:"source_turn_id,omitempty"`  // v2: codex turn-aware identity
}
```

`omitempty` keeps wire format backward-compatible for cc/opencode and for codex events that don't carry turn_id (SessionStart). No DB migration: `subagents_json TEXT` is opaque JSON blob (`frames.go:42`); old rows deserialize to `SourceTurnID = ""` cleanly (Go json zero-value semantics).

### 3.2 Match semantics

Update `subagentRefMatches` (`frame_ops.go:631-639`):

```go
func subagentRefMatches(a, b SubagentRef) bool {
    if a.IsProxy != b.IsProxy { return false }
    if a.IsProxy {
        // v2: PID+StartTime is the broker-process identity; turn_id (when both
        // sides carry it) further narrows to a specific dispatch. If either
        // side has empty turn_id (cc/opencode, SessionStart-attached codex
        // ref before any UserPromptSubmit upsert), fall back to the
        // process-level match.
        if a.SourcePID != b.SourcePID || a.SourceStartTime != b.SourceStartTime {
            return false
        }
        if a.SourceTurnID == "" || b.SourceTurnID == "" {
            return true  // process-level match
        }
        return a.SourceTurnID == b.SourceTurnID  // turn-level match
    }
    return a.ID == b.ID
}
```

### 3.3 Triggers — two attach paths + one targeted detach path

**A. SessionStart attach (existing, kept)**
- `frame_ops.go:210-249` SessionStart fast-path: attach proxy ref with `SourceTurnID=""` (codex SessionStart doesn't carry turn_id; see §2.1)

**B. UserPromptSubmit attach/upsert (new)**
- New case in `applyFrameEvent` lifecycle switch: `case agentpkg.LifecycleUserPromptSubmit:`
- If `frame == nil` and a proxy parent exists, do one of:
  - **Existing ref with `SourceTurnID == ""`** (SessionStart attached it earlier this broker lifetime): in-place upsert `SourceTurnID = req.TurnID` (parsed from `req.RawEvent`)
  - **Existing ref with `SourceTurnID != ""`** (previous turn's ref still attached): replace with new ref carrying new turn_id (this is the resumeThread case where SessionStart didn't fire and dispatch 1's ref leaked)
  - **No existing ref** (resumeThread first-time, SessionStart didn't fire): first-time attach with `SourceTurnID = req.TurnID`

**C. Stop targeted detach (new — the L2 core)**
- New case in `applyFrameEvent` lifecycle switch: `case agentpkg.LifecycleStop, agentpkg.LifecycleStopFailure:`
- If `frame != nil` → break (standalone agent, lights-rebuild ProbeIntent dispatcher handles status update)
- If `frame == nil`:
  - Parse `turn_id` from `req.RawEvent`
  - If `turn_id != ""`: targeted detach via new `removeProxyRefForSenderTurn(paneID, senderPID, senderStartTime, turnID, broadcastTs)` — only removes ref where ALL three identity fields match
  - If `turn_id == ""` (codex provider but malformed payload, or cc/opencode): fall back to existing `removeProxyRefForSender(paneID, senderPID, senderStartTime, broadcastTs)` — process-level wildcard detach

**D. SessionEnd wildcard detach (existing, unchanged)**
- `frame_ops.go:79-127` SessionEnd branch: keeps existing process-level detach (`removeProxyRefForSender` without turn_id) — semantically correct (whole broker dying, all turns gone)

**E. sweep canonicalize (existing, unchanged)**
- `sweep.go:204-211, 434` sweep canonicalization: keeps process-level identity; sweep is reasoning about dead processes, not about per-turn liveness

### 3.4 Known limitations (explicit in spec, surfaced to user)

1. **Non-prompt turns get process-level identity** — `review`/`compact`/tool-only continuations don't fire `UserPromptSubmit`, so during these turns the proxy ref keeps the previous turn's `SourceTurnID` (or empty). The Stop event for such a turn carries a different `turn_id` than the ref → targeted detach finds no match → no detach happens. **Behavior: dot stays lit through the non-prompt turn**, which is actually correct UX (broker is still doing work for the user).
2. **Out-of-order attach** — if dispatch 2's UserPromptSubmit lands before dispatch 1's Stop, the upsert overwrites dispatch 1's `SourceTurnID` to `turn_2`; dispatch 1's Stop (carrying `turn_1`) finds no `(PID, StartTime, turn_1)` match → no detach. The dot transitions seamlessly from dispatch 1 to dispatch 2 without flicker — this is the desired behavior, but means dispatch 1's "completion" is implicit (next dispatch starting = previous dispatch ended).
3. **cc/opencode unchanged** — these providers keep current `(PID, StartTime)` matching; race in §1.1 is codex-broker-specific and doesn't affect them.
4. **Codex schema evolution** — turn_id is stable in codex 1.x main but hooks subsystem is still evolving (§2.6 of consulting result). Daemon parses turn_id with fail-soft fallback to `""`, so a future schema change degrades to current v1 behavior, not crashes.

## 4. Modifications

| File | Change | LOC |
|------|--------|-----|
| `internal/agent/subagent.go` | Add `SourceTurnID string` field with `json:"source_turn_id,omitempty"` | ~5 |
| `internal/module/agent/raw_codex_event.go` *(new)* | Parse `turn_id` from `req.RawEvent` for codex provider; nil/error returns `""` (fail-soft) | ~30 |
| `internal/module/agent/frame_ops.go` | (a) `subagentRefMatches` turn-aware update (§3.2). (b) SessionStart fast-path `attachProxyRefWithRetry` keeps `SourceTurnID=""` (no change to `frame_ops.go:217-230` ref construction). (c) New `LifecycleUserPromptSubmit` case in `applyFrameEvent` switch. (d) New `LifecycleStop, LifecycleStopFailure` case. (e) `mutateSubagentsWithRetry` SubagentStart branch — handle in-place SourceTurnID upsert when match found but new ref has non-empty turn_id while existing has empty (or different). (f) New `removeProxyRefForSenderTurn` + `detachProxyRefForSenderTurnWithRetry` mirroring existing helpers. | ~150-200 |
| `internal/module/agent/handler.go` | No change (RawEvent already in EventRequest) | 0 |
| `internal/module/agent/frame_ops_test.go` | New test `TestApplyFrameEvent_TurnAwareProxyDetach` with full table matrix (§5) | ~250-350 |
| **Total** | | **~300-450 LOC** |

## 5. Test matrix

`TestApplyFrameEvent_TurnAwareProxyDetach`:

| # | Setup | Action | Expected | Validates |
|---|-------|--------|----------|-----------|
| 1 | parent cc + 1 ref(IsProxy, PID=42, t1, turnID="") (SessionStart attached) | UserPromptSubmit from PID=42 with raw turn_id="t_a" | ref upserts SourceTurnID="t_a" in-place | §3.3.B in-place upsert |
| 2 | parent cc + 1 ref(PID=42, t1, turnID="t_a") | Stop from PID=42 with raw turn_id="t_a" | ref detached (turn-targeted match) | §3.3.C targeted detach |
| 3 | parent cc + 1 ref(PID=42, t1, turnID="t_a") | Stop from PID=42 with raw turn_id="t_b" | ref kept (turn mismatch) | dispatch 1 Stop late, dispatch 2 already in flight |
| 4 | parent cc + 1 ref(PID=42, t1, turnID="t_a") | UserPromptSubmit from PID=42 with raw turn_id="t_b" | ref's SourceTurnID overwritten to "t_b" | dispatch 2 starting, ref transitions cleanly |
| 5 | parent cc + 1 ref(PID=42, t1, turnID="t_a") | StopFailure from PID=42 with raw turn_id="t_a" | ref detached | StopFailure parity with Stop |
| 6 | parent cc + 0 refs (resumeThread first dispatch, no SessionStart) | UserPromptSubmit from PID=42 with raw turn_id="t_a" | first-time attach, ref(PID=42, t1, turnID="t_a") created | §3.3.B resumeThread first-time |
| 7 | parent cc + 1 ref(PID=42, t1, turnID="t_a") | Stop from PID=42 with raw turn_id="" (parse fail / cc-style) | ref detached via process-level fallback | §3.3.C wildcard fallback |
| 8 | parent cc + 1 ref(PID=42, t1, turnID="") + 1 ref(PID=43, t2, turnID="") (two brokers) | Stop from PID=42 with raw turn_id="" | only PID=42 ref detached, PID=43 untouched | multi-ref isolation |
| 9 | parent cc + 1 ref(PID=42, t1, turnID="t_a") | SessionEnd from PID=42 (no turn_id) | ref detached via wildcard (existing path unchanged) | §3.3.D unchanged behavior |
| 10 | sender owns own frame (frame != nil) + standalone Stop | Stop from sender's own PID | sender's frame.Subagents unchanged, no proxy detach attempted | §3.3.C `frame != nil` short-circuit |
| 11 | parent cc + 1 native ref(IsProxy=false, ID="task-x") | Stop from PID=42 turn_id="t_a" | native ref untouched (subagentRefMatches IsProxy gate) | native isolation |
| 12 | parent cc + 1 ref(PID=42, t1, turnID="t_a"). Sequential: Stop(t_a) → Stop(t_a) again | call 1: detached / call 2: no-op (already gone) | idempotent | retry-safe |
| 13 | parent cc + 1 ref(PID=42, t1, turnID=""). Sequential: UserPromptSubmit(t_a) → Stop(t_a) | call 1: in-place upsert turnID="t_a" / call 2: targeted detach succeeds | full lifecycle | end-to-end happy path |
| 14 | concurrent: two goroutines call (a) UserPromptSubmit(t_b) on existing ref(t_a), (b) Stop(t_a) | exactly one of: {ref→t_b, no Stop detach} or {ref detached then re-attached as t_b}. Never: ref disappears with no Stop trace | no torn writes | optimistic concurrency under turn changes |
| 15 | opencode UserPromptSubmit (no turn_id in raw) | attach via existing path | ref attached with SourceTurnID="" (provider fallback) | §2.5 fallback for opencode |
| 16 | cc Stop with no turn_id in raw | wildcard detach via §3.3.C fallback | ref detached process-level | §2.5 cc fallback |

Rows 1-7 are codex-specific lifecycle. Row 8 = multi-broker isolation. Row 9 = SessionEnd unchanged regression guard. Row 10 = standalone short-circuit (I2 from v1). Row 11 = native isolation (I3 from v1). Row 12 = idempotency (I1 from v1). Row 13 = end-to-end. Row 14 = concurrency under turn changes. Rows 15-16 = cc/opencode fallback.

## 6. Out of scope

- **Governance P2/P3** (broker kill/sweep) — separate workstream. L2 does not extend broker lifetime; only changes when proxy dot extinguishes.
- **Standalone codex Stop main-agent light** — governed by `worktree-probe-intent-bidirectional-grace` (J3 dispatcher pre-grace). L2 leaves `frame != nil` Stop handling unchanged.
- **Native subagent Stop dots** — SubagentStart/Stop already drive native dots correctly via `mutateSubagentsWithRetry`. L2 does not touch the SubagentStart/Stop case (only adds a turn-aware in-place upsert path, which native refs never enter due to `IsProxy` gate).
- **cc/opencode turn-aware identity** — those providers' hook payloads don't carry per-turn identity, and they don't have the long-lived broker problem. L2 does not retrofit them. (opencode's `messageID` could be wired in a future PR if a similar bug surfaces; no current evidence of one.)
- **Non-prompt turn light behavior** — review/compact/tool-only turns don't fire UserPromptSubmit; their Stop won't find a turn_id match and won't detach. The dot stays lit during such turns. Acknowledged as correct UX (broker is still busy on user's behalf).
- **L1 / L3 / L4** — separate phases per kickoff.

## 7. Risks & mitigations

| Risk | Mitigation |
|------|-----------|
| Codex schema evolution drops/renames `turn_id` | `raw_codex_event.go` parses fail-soft to `""`. Daemon degrades to v1 behavior (process-level identity), not crashes. Future schema change → followup phase. |
| In-place upsert in `mutateSubagentsWithRetry` introduces new retry path | Add explicit unit tests row 14 (concurrent upsert + Stop). Reuse existing UpsertIfUnchanged etag mechanism — same correctness story as existing attach path. |
| Non-prompt turn keeps stale `SourceTurnID` | §3.4 known limitation 1; documented as correct UX. Followup: if user reports stale-light bugs, add `PreToolUse` as additional upsert trigger (codex source confirms it carries turn_id). |
| `req.RawEvent` size growth bypasses `MaxRawEventBytes` | Existing 64KB cap in `path_hint_extractor.go:15` already protects parser. New parser shares the same Unmarshal path. |
| Trace reason vocabulary expansion | New reasons added: `proxy_subagent_detached_on_stop_turn` (turn-targeted), `proxy_subagent_detached_on_stop` (wildcard fallback), `proxy_subagent_upserted_on_user_prompt`, `proxy_subagent_attached_on_user_prompt` (resumeThread first-time), `stop_without_proxy`, `user_prompt_without_proxy_parent`. AC enforces `rg` checklist (§8 AC5). |
| AC1 v1 wording over-restricted test helpers | v2 §8 AC1 explicitly allows test-only unexported helpers in `_test.go`. |
| Hook propagation latency unclear | Removed v1 §8's "50-100ms typical" claim. New §9 verification uses **trace timestamps** (daemon-side) rather than wall-clock guesses. |
| Provider drift between cc/opencode/codex paths in same code | Codex-specific logic isolated in `raw_codex_event.go` and gated by `req.AgentType == "codex"`. cc/opencode paths route to existing wildcard helpers untouched. |

## 8. Acceptance criteria

| AC | Description |
|----|-------------|
| **AC1** | Production code: no new exported types other than the new `SourceTurnID` field on existing `SubagentRef`. New helpers (`removeProxyRefForSenderTurn`, `detachProxyRefForSenderTurnWithRetry`, `parseCodexTurnID` etc.) are unexported. Test-only helpers in `_test.go` are unrestricted. |
| **AC2** | All 16 test rows in §5 pass. |
| **AC3** | All existing tests in `internal/module/agent/...` and `internal/agent/...` pass unchanged (no test edits outside the new test file and `subagent_test.go` if a JSON round-trip test exists for `SubagentRef`). |
| **AC4** | `cd spa && pnpm run lint && pnpm run build` and `go build ./... && go test ./...` pass. Race tests (`go test -race ./internal/module/agent/...`) pass for the new test rows. |
| **AC5** | Trace reason vocabulary stays additive; PR review must run `rg 'proxy_subagent_(detached|upserted|attached)_on_(stop|user_prompt)'` and confirm only the §7-listed strings exist. |
| **AC6** | LOC bound: `frame_ops.go` change ≤ 200 lines including comments; new `raw_codex_event.go` ≤ 50 lines; new test ≤ 400 lines. Total PR diff ≤ 700 lines including spec + plan + tests. |
| **AC7** | `SubagentRef` JSON round-trip preserves `SourceTurnID` when set, omits it when empty (`omitempty` semantics). |

## 9. Verification (post-merge live check)

On `mlab` after deploying the daemon update, capture daemon trace logs (`/tmp/daemon-trace-*.json` or equivalent) and verify:

1. **Single dispatch**: spawn `/codex:rescue` from cc tab → SessionStart attaches `(PID, StartTime, "")` → UserPromptSubmit upserts `SourceTurnID=t_a` → Stop detaches `(PID, StartTime, t_a)`. Verify `frame_meta.Reason` sequence: `proxy_subagent_attached` → `proxy_subagent_upserted_on_user_prompt` → `proxy_subagent_detached_on_stop_turn`.
2. **Sequential dispatches**: dispatch 1 finishes, dispatch 2 starts. Verify ref's `SourceTurnID` transitions `t_1 → t_2` via UserPromptSubmit upsert (row 4 / row 13). Dot stays lit continuously.
3. **Concurrent dispatches** (manually trigger via `/codex:adversarial-review` 3-parallel mode if available): verify no spurious dot extinction during transition between turns.
4. **Resume thread**: kill broker → next dispatch → verify SessionStart fires once for new broker → subsequent dispatches use UserPromptSubmit upsert path (no second SessionStart in trace).
5. **Non-prompt turn** (review/compact if reproducible): verify dot stays lit (Stop with mismatched turn_id finds no match, logs `stop_without_proxy`).

Verification framework checks **trace reasons**, not wall-clock timing, since hook propagation latency varies per `daemon-hook-pipeline-lag-analysis §2.5`.

## 10. Single-PR delivery

Spec, plan, implementation, tests in one PR (kickoff phase 2). **Two-round codex review** required given turn-aware identity touches concurrency-critical paths:

- **Round 1**: standard review focused on identity model correctness, in-place upsert race-safety, fallback behavior across providers.
- **Round 2**: 3-parallel adversarial — attack (find race / boundary / PID-reuse), defense (validate provider isolation / resumeThread coverage), file-health (frame_ops growth / SRP).

Followup issues filed for: Stop-vs-SessionEnd ordering edge cases not mitigated by turn_id (governance P3 territory), opencode `messageID` retrofit if a similar bug surfaces, codex hook schema capability detection.
