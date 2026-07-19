# Spec: OpenCode L1 — child (subagent) session events no longer hijack the parent light

Date: 2026-07-19
Scope: `internal/agent/opencode/` (plugin template + test mirror/fixtures). Daemon and SPA untouched.
Size: small (single phase). Reframed after codex spec review (task-mrrc2hts-ne3zbg).

## Problem (verified live, 2026-07-19)

On `tmux` session `istdb` (opencode running), the parent session's status light collapses
to **idle** every time a subagent (Task tool) finishes, even though the parent is still
running. Daemon log smoking gun — subagent completion produces a spurious parent `PdxStop`:

```
12:45:32 istdb  (opencode) PdxStop status=idle → projection_built top_status=idle   subagents=0   ← parent wrongly idle
12:45:50 hermes (cc)       PdxSubagentStop     → projection_built top_status=running subagents=0   ← cc parent stays running (correct)
```

The hook transport is healthy — every opencode event fires and broadcasts. This is a
pre-existing **semantic** bug (memory L1, never shipped), not a version-update regression.

## Root cause (the general form)

The opencode plugin has **no concept of parent vs child session**. opencode spawns each
subagent (Task tool) as a **child session** with its own full lifecycle, and the plugin
re-emits every child lifecycle event as a parent-level `Pdx*` event. The daemon matches a
frame by **`(tmuxPaneID, senderPID, senderStartTime)`** — NOT by opencode `session_id`
(`frame_ops.go:86` `GetByIdentity`). Because one opencode process serves parent + all its
children over one tmux pane with one sender identity, **every child lifecycle event mutates
the single parent frame**:

| child bus event | plugin emits | daemon derives (`status.go`) | effect on parent frame |
|---|---|---|---|
| `session.created` (has `info.parentID`) | `PdxSessionStart` | `StatusIdle` (L15) | parent flaps to idle (masked by following `chat.message` running, but still a wrong projection + broadcast) |
| `session.status` idle | `PdxStop` | `StatusIdle` (L23) | **parent light collapses to idle** ← the visible bug |
| `session.error` | `PdxStopFailure` | `StatusError` (L34) | parent light turns **error/red** |
| `session.deleted` | `PdxSessionEnd` | `StatusClear` (L36) → `deleted_frame` (`frame_ops.go:93`) | **parent frame is deleted** ← worse than idle flap |

The subagent's real, intended representation is already carried by `PdxSubagentStart` /
`PdxSubagentStop` (emitted from `tool.execute.before/after`), which the daemon treats as
**detail-only** (`frame_ops.go:80` — they add/remove a subagent dot, they do NOT own the
frame). So the child session's *own* lifecycle events are pure noise at the parent level and
must not be emitted.

### Upstream schema (authoritative, checked against current source)

- opencode issue [#30043](https://github.com/anomalyco/opencode/issues/30043) (CLOSED,
  maintainer: "for now you can fetch the session and see if it has a parent"): `session.status`
  does **not** carry `parentID`. Filtering on the status event alone is impossible. Same bug the
  Warp opencode plugin hit.
- `Session.Info` (`packages/opencode/src/session/session.ts`) has `parentID: optional(SessionID)`,
  populated only for child sessions.
- `session.created` publishes the **full info**: `events.publish(Event.Created, { sessionID, info: result })`
  (`session.ts:537`). So `event.properties.info.parentID` is the authoritative, event-carried
  signal — no SDK fetch needed. Schema stable 1.14.23 → 1.18.3.
- Live env: `istdb` runs 1.17.9; PATH `opencode` is 1.18.3 (next restart uses it). Fix must be
  version-independent — the `session.created` / `info.parentID` path satisfies this.

## Fix (plugin-only): gate the whole child-session lifecycle

Maintain `subagentSessions = new Map()` mapping **childSessionID → parentSessionID**, learned
from `session.created`. For any known child session, **suppress every parent-level lifecycle
emit**. The child is already represented by the subagent dot (`PdxSubagentStart/Stop`).

Resolve the session id defensively in every handler (runtime publishes `{ sessionID, info }` but
the generated SDK type lists only `info` for `session.created`/`session.deleted`, so top-level
`sessionID` is not guaranteed across versions):

```js
const sid = event.properties.sessionID || event.properties.info?.id || ''
```

| Event | New behavior |
|---|---|
| `session.created` | `parentID = event.properties.info?.parentID`. If truthy → `subagentSessions.set(sid, parentID)` and **return without emitting `PdxSessionStart`**. Parent (no `parentID`) → emit `PdxSessionStart` unchanged. |
| `session.status` idle | if `subagentSessions.has(sid)` → **return** (no `PdxStop`). Else existing `suppressIdleForSession` + `PdxStop` path unchanged. |
| `session.error` | if `subagentSessions.has(sid)` → **return** (no `PdxStopFailure`, and do **not** arm `suppressIdleForSession`). Else existing behavior unchanged. |
| `session.deleted` | if `subagentSessions.has(sid)` → `subagentSessions.delete(sid)` and **return** (no `PdxSessionEnd`). Else (this sid is a **parent**): delete every entry whose value === `sid` (its children only — never a sibling parent's children), then emit `PdxSessionEnd` unchanged. |

`PdxSubagentStart` / `PdxSubagentStop` (from `tool.execute.before/after`) are **unchanged** —
they remain the sole, correct source of subagent presence.

### Design decisions (resolved)

1. **Gate all four child events, not just idle.** Verified against `status.go` + `frame_ops.go`:
   each child event mutates the parent frame. Gating only `PdxStop` (the original memory plan)
   would leave the created/error/deleted holes — deleted being the most severe (frame deletion).
2. **Child `session.error` is fully skipped**, so `suppressIdleForSession` is never armed for a
   child. This also dissolves the ordering hazard (a child-armed suppression that a child idle
   would otherwise never clear). Both structures stay clean for children with no extra bookkeeping.
3. **`Map`, parent-scoped cleanup.** A single opencode process can host more than one root
   session over its lifetime (session switcher). A flat `Set` + blanket `clear()` on any parent
   delete would forget a *sibling* parent's still-live children, re-opening the leak for them.
   Keying child→parent and deleting only the matching parent's children on parent delete is
   correct and bounds growth. Per-child `session.deleted` also prunes. Worst case (children never
   individually deleted) the map is O(child sessions seen in this process) — negligible.
4. **No SDK fetch fallback in this PR.** The fix assumes the plugin observes a child's
   `session.created` before the child's later events — true for the normal lifecycle. The only
   miss window is a child already mid-flight when the plugin is (re)loaded (`pdx setup` / reload).
   In that window an unknown child's `session.status` idle emits a false `PdxStop`, its
   `session.error` a false `PdxStopFailure` (parent turns red), and its `session.deleted` a false
   `PdxSessionEnd` (**parent frame deleted**) — all identical to today's behavior, so **not a
   regression**, but the deleted/error blast radius is larger than idle. Documented limitation;
   SDK-fetch hardening (fetch session parentID for an unknown sid before emitting) tracked as a
   follow-up issue.

### Non-goals

- No daemon-side or SPA-side change (fix at the emit source).
- No change to `PdxSubagentStart` / `PdxSubagentStop`.
- No new catalog event (`opencodeEventSpecs` unchanged → template/spec parity intact).
- Not adopting the deprecated `session.idle` event; not implementing the #30043 upstream patch.

## Test strategy (TDD)

Update the Go-side JS-mirror (`pluginSimState`, `plugin_template_contract_test.go`) in lockstep
with the JS template; regenerate the byte-exact template comparison for the added lines.

Contract cases:
1. subagent `session.created` (`info.parentID` set) → **no** `PdxSessionStart`; child registered.
2. parent `session.created` (no `parentID`) → `PdxSessionStart` emitted; not registered.
3. child `session.created` with **no top-level `sessionID`** (only `info.id` + `info.parentID`) →
   still registered under `info.id` (proves the `sessionID || info.id` fallback).
4. registered child `session.status` idle → **no** `PdxStop`. Parent idle → `PdxStop` (unchanged).
5. registered child `session.error` → **no** `PdxStopFailure`; assert white-box
   `suppressIdleForSession[childID] == false` (NOT armed).
6. registered child `session.deleted` → **no** `PdxSessionEnd`; removed from map.
7. parent `session.deleted` → `PdxSessionEnd` emitted **and** only that parent's children pruned.
8. **sibling parents**: register children of parent A and parent B; delete A → A's children pruned,
   B's children still gated (B child idle still suppressed). Guards the Map/scoped-cleanup fix.
9. multi-subagent: N child idles suppressed; parent's own idle emits exactly one `PdxStop`.
10. sequence: child created→idle→error→deleted then parent idle → only the parent `PdxStop` survives;
    both structures clean afterward.
11. existing `ChatMessageClearsStaleErrorSuppression` / `StaleSuppressionWithoutChatMessage`
    (parent path) must stay green.

Rendered-JS static guards (`plugin_template_test.go`, run even without Bun): assert the template
contains `subagentSessions`, reads `event.properties.info?.parentID`, and places the child branch
before the `PdxSessionStart` emit — so a no-Bun CI still catches removal of the gate.

Fixtures: add a subagent `session.created` variant carrying `info.parentID` under
`testdata/opencode-1.14.23-payloads/` (schema unchanged across versions); include a variant
omitting top-level `sessionID` for case 3.

Bun integration (`plugin_template_bun_integration_test.go`): extend beyond the current single
`session.created` to drive a realistic sequence through the real rendered JS against a stub `pdx`
that **appends** every invocation — the stub reads the event name from `$4`
(`[pdxPath, 'hook', '--agent', 'opencode', eventName]`) and appends `eventName<TAB>stdin` as JSONL
(no `jq`); `emit()` awaits `proc.exited` so order is deterministic. Sequence:
`parent created → child created(parentID) → child idle → child error → child deleted → parent idle
→ parent deleted`. Assert the capture contains **only** parent `PdxSessionStart`, parent `PdxStop`,
parent `PdxSessionEnd` (no child-derived events). Guards against "mirror and JS drift wrong together".

Daemon-level regression: skipped — fix is at the emit source, and the Bun sequence + contract cases
are the coverage floor. (The root-cause daemon derivations were verified by reading `status.go` /
`frame_ops.go`, not asserted in this PR.)

## Verification

- `go test ./internal/agent/opencode/... -count=1 -race` green; `go test ./... -count=1` green.
- Live (mlab): rebuild `bin/pdx`, `pdx setup --agent opencode`, run an opencode session that
  spawns a subagent; confirm daemon log shows no parent `PdxStop` / `PdxStopFailure` /
  `PdxSessionEnd` at child lifecycle points, parent light stays running, and real parent idle
  (end of prompt cycle) still emits `PdxStop`.

## Process

Single phase. Codex spec review done (reframe adopted). Next: plan → codex plan review →
subagent TDD → PR → codex round-1 (Go-only → sandbox OK), adversarial round only if round-1
surfaces non-trivial findings. Squash merge + separate bump PR. Follow-up issue: SDK-fetch
fallback for the plugin-reload miss window.
