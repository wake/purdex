# Plan: OpenCode L1 — child session lifecycle gating

Spec: `2026-07-19-opencode-l1-subagent-idle-filter-spec.md`. Single phase, TDD.
All paths relative to repo root; all edits in worktree `opencode-l1-idle-filter`.

## Files in scope

- `internal/agent/opencode/plugin_template.go` — `renderManagedPlugin` JS (production).
- `internal/agent/opencode/plugin_template_contract_test.go` — `pluginSimState` JS-mirror + contract cases.
- `internal/agent/opencode/plugin_template_bun_integration_test.go` — real-JS Bun sequence test.
- `internal/agent/opencode/testdata/opencode-1.14.23-payloads/` — add subagent `session.created` fixture.
- `internal/agent/opencode/plugin_template_test.go` — structural assertions; confirm still green (marker + emit parity; not a byte-golden, so added lines are fine).

## Design invariants (from spec)

`subagentSessions = new Map()` keyed **childSessionID → parentSessionID**. Child identified by
`event.properties.info?.parentID` on `session.created`. Session id resolved defensively in every
handler: `sid = event.properties.sessionID || event.properties.info?.id || ''`. Gate **all** child
lifecycle emits; parent behavior unchanged. `PdxSubagentStart/Stop` untouched.

| Event | Child (has parentID / in map) | Parent |
|---|---|---|
| `session.created` | `set(sid, parentID)`; **no** `PdxSessionStart` | emit `PdxSessionStart` |
| `session.status` idle | **no** `PdxStop` | existing suppress + `PdxStop` |
| `session.error` | **no** `PdxStopFailure`; do not arm `suppressIdleForSession` | existing `PdxStopFailure` + arm suppress |
| `session.deleted` | `delete(sid)`; **no** `PdxSessionEnd` | delete entries whose value === `sid` (only this parent's children); emit `PdxSessionEnd` |

Ordering in each handler: **child-check first**, then existing parent logic.

## Tasks (each its own commit; subagent TDD)

### T1 — Red: contract cases for child gating
Add to `plugin_template_contract_test.go` (against `pluginSimState`, which T2 updates). Mirror
returns `ok=false` where no emit is expected:
- child `session.created` (`properties.info.parentID`) → `ok=false` **and** registers child.
- child `session.created` with **no top-level `sessionID`** (only `info.id`+`info.parentID`) → registered under `info.id`.
- parent `session.created` (no `parentID`) → `PdxSessionStart` (unchanged).
- registered child `session.status` idle → `ok=false`; unregistered/parent idle → `PdxStop`.
- registered child `session.error` → `ok=false` **and** white-box assert `state.suppressIdleForSession[childID] == false`.
- registered child `session.deleted` → `ok=false`, removed from map.
- parent `session.deleted` → `PdxSessionEnd` **and** only that parent's children pruned (previously-registered child id gone).
- **sibling parents**: children of parent A and B registered; delete A → A's child gone from map, B's child idle still `ok=false` (suppressed).
- multi-subagent: 3 children registered, 3 child idles `ok=false`, parent idle → exactly one `PdxStop`.
- full sequence child created→idle→error→deleted then parent idle → only parent `PdxStop`; map + suppress both clean.
Run: tests fail against current mirror. Commit red.

### T2 — Green: JS template + mirror in lockstep
1. `plugin_template.go` `renderManagedPlugin`: add `const subagentSessions = new Map()`; add a
   `sid = event.properties.sessionID || event.properties.info?.id || ''` resolution in the gated
   handlers; implement the 4-event gating table (child-check first; parent-delete prunes only
   entries whose value === sid).
2. `pluginSimState`: add `subagentSessions map[string]string` (child→parent); mirror identical
   logic in `simulateBusEvent` for `session.created` (read `properties.info.parentID`, sid fallback
   to `info.id`), `session.status`, `session.error`, `session.deleted`. `newPluginSimState` inits it.
3. Keep `emit()`-parity: no new event names → `opencodeEventSpecs` / `validateSpecsCoverEmitted` unaffected.
Run: T1 green; existing contract tests (incl. `ChatMessageClearsStaleErrorSuppression`,
`StaleSuppressionWithoutChatMessage`) still green. Commit green.

### T3 — Fixtures + rendered-JS static guards
1. Add `testdata/opencode-1.14.23-payloads/session.created.subagent.json` with `properties.info.parentID`
   (and a no-top-level-`sessionID` variant for the fallback case, inline in the test if simpler).
2. Add structural assertions in `plugin_template_test.go`: rendered body contains `subagentSessions`,
   `event.properties.info?.parentID`, and the child branch precedes the `PdxSessionStart` emit — so
   a Bun-less CI still catches gate removal.
Commit.

### T4 — Bun integration: realistic lifecycle sequence
Extend `plugin_template_bun_integration_test.go`. Stub `pdx` reads event name from `$4`
(`[pdxPath,'hook','--agent','opencode',eventName]`) and **appends** `eventName<TAB>stdin` as JSONL
to the capture file (no `jq`; `emit` awaits `proc.exited` → deterministic order). Fire the sequence:
`parent created → child created(parentID) → child idle → child error → child deleted → parent idle
→ parent deleted`. Assert capture contains **only** parent `PdxSessionStart`, `PdxStop`,
`PdxSessionEnd` (no child-derived events). Skip if `bun` not in PATH (existing guard). Commit.

### T5 — Verify + docs
- `go test ./internal/agent/opencode/... -count=1 -race` and `go test ./... -count=1` green.
- Confirm `plugin_template_test.go` structural + parity assertions pass.
- PR body: root-cause table + daemon-log before/after + live verification steps from spec.
- Open follow-up issue: SDK-fetch fallback for the plugin-reload miss window covering
  idle/**error**/**deleted** blast radius (spec §Design decision 4).

## Out of scope (guard against scope creep)
Daemon/SPA code; `PdxSubagentStart/Stop`; new catalog events; `session.idle` (deprecated); the
#30043 upstream patch; SDK-fetch fallback (follow-up issue only).

## Review
Codex plan review (this doc) → subagent TDD (T1–T5) → PR codex round-1 standard (Go-only, sandbox OK)
→ adversarial round only if round-1 non-trivial → squash merge → separate bump PR.
