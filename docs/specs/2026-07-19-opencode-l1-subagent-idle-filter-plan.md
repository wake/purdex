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

`subagentSessions = new Set()`. Child identified by `event.properties.info?.parentID` on `session.created`.
Gate **all** child lifecycle emits; parent behavior unchanged. `PdxSubagentStart/Stop` untouched.

| Event | Child (in Set / has parentID) | Parent |
|---|---|---|
| `session.created` | add to Set; **no** `PdxSessionStart` | emit `PdxSessionStart` |
| `session.status` idle | **no** `PdxStop` | existing suppress + `PdxStop` |
| `session.error` | **no** `PdxStopFailure`; do not arm `suppressIdleForSession` | existing `PdxStopFailure` + arm suppress |
| `session.deleted` | delete from Set; **no** `PdxSessionEnd` | `subagentSessions.clear()`; emit `PdxSessionEnd` |

Ordering in each handler: **child-check first**, then existing parent logic.

## Tasks (each its own commit; subagent TDD)

### T1 — Red: contract cases for child gating
Add to `plugin_template_contract_test.go` (against `pluginSimState`, which T2 updates):
- `session.created` with `properties.info.parentID` → mirror returns `ok=false` (no emit) **and** registers child.
- `session.created` without `parentID` → `PdxSessionStart` (unchanged).
- registered child `session.status` idle → `ok=false`; unregistered/parent idle → `PdxStop`.
- registered child `session.error` → `ok=false` **and** `suppressIdleForSession` NOT armed (assert via a following parent-style idle for that id still behaving correctly).
- registered child `session.deleted` → `ok=false`, removed from Set.
- parent `session.deleted` → `PdxSessionEnd` **and** Set cleared (assert a previously-registered child id is gone).
- multi-subagent: register 3 children, 3 child idles suppressed, parent idle → exactly one `PdxStop`.
- full sequence child created→idle→error→deleted then parent idle → only parent `PdxStop`; both Sets clean.
Run: tests fail against current mirror (which still emits). Commit red.

### T2 — Green: JS template + mirror in lockstep
1. `plugin_template.go` `renderManagedPlugin`: add `const subagentSessions = new Set()`; implement the 4-event gating table above (child-check first; parent-delete `subagentSessions.clear()`).
2. `pluginSimState`: add `subagentSessions map[string]bool`; mirror identical logic in `simulateBusEvent` for `session.created` (read `properties.info.parentID`), `session.status`, `session.error`, `session.deleted`. `newPluginSimState` initializes the map.
3. Keep `emit()`-parity: no new event names → `opencodeEventSpecs` / `validateSpecsCoverEmitted` unaffected.
Run: T1 cases green; existing contract tests (incl. `ChatMessageClearsStaleErrorSuppression`, `StaleSuppressionWithoutChatMessage`) still green. Commit green.

### T3 — Fixture: subagent session.created
Add `testdata/opencode-1.14.23-payloads/session.created.subagent.json` (or inline in tests if the
harness prefers) with `properties.info.parentID` set. Wire into the contract loader if fixture-driven.
Commit.

### T4 — Bun integration: full child lifecycle sequence
Extend `plugin_template_bun_integration_test.go`: append an IIFE firing, against one stub `pdx` that
appends every invocation's `(eventName, stdin)` to a capture file, the sequence:
`session.created(parentID)` → `session.status idle` → `session.error` → `session.deleted` →
parent `session.created` → parent `session.status idle`. Assert the capture contains **only** the
parent `PdxSessionStart` and parent `PdxStop` (no child-derived events). Skip if `bun` not in PATH
(existing guard pattern). Commit.

### T5 — Verify + docs
- `go test ./internal/agent/opencode/... -count=1 -race` and `go test ./... -count=1` green.
- Confirm `plugin_template_test.go` structural assertions still pass.
- PR body: root-cause table + daemon-log before/after + the live verification steps from spec.
- Open follow-up issue: SDK-fetch fallback for the plugin-reload miss window (spec §Design decision 4).

## Out of scope (guard against scope creep)
Daemon/SPA code; `PdxSubagentStart/Stop`; new catalog events; `session.idle` (deprecated); the
#30043 upstream patch; SDK-fetch fallback (follow-up issue only).

## Review
Codex plan review (this doc) → subagent TDD (T1–T5) → PR codex round-1 standard (Go-only, sandbox OK)
→ adversarial round only if round-1 non-trivial → squash merge → separate bump PR.
