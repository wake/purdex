# Plan — sync conflict fixes (spec: 2026-09-28-sync-conflict-fixes-spec.md)

One PR, three commits (TDD: failing test first in each). All paths under `spa/src/`.
Run: `cd spa && npx vitest run <files>`; at the end full `npx vitest run`, `pnpm run lint`,
`npx tsc -p tsconfig.app.json --noEmit`.

## Task 1 — keep-local keeps the current stores (D2)

- `lib/profile/sync-state.ts` `case 'resolved'`: when `s.conflict !== null && s.currentHash !== s.conflict.localHash`
  → `restoreLocal: null` (base still = `conflict.sot`). Equal hashes keep today's value. Update the header
  comment block (the 409 bullet "the local side being the snapshot that was SENT" and the restore row 0c
  wording) so it says keep-local pushes the live stores.
- `lib/profile/executor.ts`: `SectionLock.currentHash` doc is now true; fix the `answerFor` comment (the guard
  stays — first-reconciliation behaviour unchanged) and the header bullet at ~141-144 to describe the new rule.
- Tests: `sync-state.test.ts` — keep-local after a post-lock edit → `restoreLocal === null`, `base` = conflict.sot,
  `decideSection` → push of the current hash; unchanged case still behaves as before. Move the repro
  `lib/profile/repro-keep-local-terminated.test.ts` into a proper name
  (`executor.keep-local-live.integration.test.ts`), keep its assertions, make it green.

## Task 2 — deterministic stamps in the tab store (D1, store side)

- `types/tab.ts` `RebuildPatch`: `agent-group` / `agent-backfill` records already carry `capturedAt` /
  `agent.updatedAt` from the writer — the store must USE the writer's `capturedAt` for `agent-group`
  (today it overwrites with `now`), and for `agent-backfill` fill / replace take `capturedAt` from
  `record.agent.updatedAt` (add an optional `capturedAt` to the backfill record if cleaner).
- `stores/useTabStore.ts applyRebuildPatch`:
  - `agent-exit` → `capturedAt: patch.exited.at`.
  - `probe-cwd`, `unverified` → keep `prev.capturedAt`.
  - `field` → `Date.now()` (unchanged).
  - `now` is read only where still needed.
- Tests (`useTabStore` rebuild tests): apply each automatic patch under two different `vi.setSystemTime`
  values → identical resulting content; `field` differs.

## Task 3 — deterministic stamps at the writers (D1, writer side)

- `stores/useAgentStore.ts writeProvenanceRecord`: needs the event's `broadcast_ts` (ns) → pass it in from the
  caller (line ~240), `ms = Math.floor(ns / 1e6)`, fallback `Date.now()` when ≤ 0 / not finite. Use it for both
  `agent.updatedAt` and `capturedAt`.
- `lib/rebuild/provenance-probe.ts`: `updatedAt` = `lastSeenAt` ns → ms, same fallback; update the comment
  ("when THIS client saw the agent live") to say it is the daemon's last-seen time so every client writes
  the same bytes.
- Put the ns→ms-with-fallback helper in one place (e.g. `lib/rebuild/provenance.ts`), unit-test it.
- Tests: writer tests assert the stamp equals the daemon value, and the fallback.

## Done when

Repro green; full vitest, lint, tsc clean; each task its own commit.

## Review round 1 (Claude subagent, 2026-09-28) — amendments, these override the tasks above

- **Task 1 +UI.** D2 makes the conflict UI's "keep local also undoes what you did here since" wrong:
  remove that hint (ResolveRow.tsx ~61-63, ~116; zh-TW.json ~1532/~1550 and the en.json twins) and make
  resolve-counts.ts (~5, ~81-84) count the LOCAL side from `currentHash` (what is actually pushed), not
  `conflict.localHash`. Fix sync-status.ts ~129-130 comment. Update ResolveBlock.test.tsx /
  ResolveBlock.integration.test.tsx accordingly.
- **Task 1 simplification.** Since `finish()` clears a `restoreLocal` whose hash equals `currentHash`,
  `resolved keep:'local'` sets `restoreLocal: null` unconditionally. The restore-local machinery (row 0c,
  `local-restored`, executor `restoreLocal()`, parked restore) becomes unreachable from `resolved`; it is
  LEFT IN PLACE in this PR (a follow-up issue removes it). Existing tests asserting the restore after
  keep-local are rewritten to assert the new behaviour.
- **Task 1 answerFor.** Drop the `conflict.localHash !== currentHash` guard in `answerFor`
  (executor.ts ~678): under `push`, keep-local now pushes the live stores, which is what push means.
  Update the header bullet accordingly.
- **Task 3 backfill stamp → frame start.** `last_seen_at` moves on every hook event, so two clients' probes
  usually differ. Daemon: `internal/module/agent/provenance_handler.go` answer gains `started_at` (the
  frame's `StartedAt`, same unit as `last_seen_at`; Go test). SPA: `SessionProvenance.startedAt`
  (host-api.ts), the backfill uses it (ns → ms) for `agent.updatedAt` and `capturedAt`; 0 / missing
  (old daemon) → `Date.now()` as today. Update the spec's D1 table + Residual paragraph.
- **`exited.at`** needs no fallback (parseExit already rejects ≤ 0).
- **Known tests that change:** useTabStore.rebuild.test.ts ~236 ("stamps capturedAt on every write"),
  ~691-700 (exit expects Date.now), executor.test.ts ~636 / ~1659 (restore after keep-local).

Note (2026-09-28): the plan review above ran on a Claude subagent while codex quota was exhausted; quota is
back, so the PR reviews follow the standard codex R1 + R2 (attack → critic) flow.
