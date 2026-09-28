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
