# Plan — profile stale leader (spec: 2026-09-30-profile-stale-leader-spec.md)

TDD, one commit per task. All paths under `spa/src/lib/profile/`.

## Task 1 — leader.ts `onRecovered` (F1)
- Tests in `leader.test.ts` (fake timers, existing `openWindow` helper):
  1. lead; `vi.setSystemTime(+30 s)` without running timers; then advance to let `renew` run →
     `onRecovered` called once, `changes` (onChange) unchanged, lease rewritten with a new `expiresAt`, `isLeader()` true.
  2. ordinary renewals (advance 10 s normally) → never called.
  3. a lapse where another window claimed meanwhile → `onChange(false)`, not `onRecovered`.
  4. unsubscribe returned by `onRecovered` works; `stop()` → nothing afterwards; listener throwing → others still called.
  5. storageless mode (getItem/setItem throw) → never called; ALSO storageless → storage comes back while it still
     holds this window's own EXPIRED lease → not called (the recovery out of storageless is not a lapse). (review #3)
  6. pagehide during/after a lapse → never called; pageshow (bfcache) → re-contends, not called. (review #2)
  7. read/write race: another window's lease lands between renew's read (own, expired) and its write — fake it by
     spying `setItem` once to write the other window's lease first; `onRecovered` may fire, but the start-side
     callback re-reads `isLeader()` (Task 3) — pin whatever leader.ts does and document it. (review #7)
- Impl: in `renew()`, BEFORE the storageless block, remember `const wasStorageless = storageless`; after it:
  `const lapsed = !wasStorageless && r.ok && r.lease?.windowId === id && r.lease.expiresAt <= now()`;
  after a successful `write()` and `schedule(renew, renewMs)`, if `lapsed` fire recovered listeners (own Set, same
  try/catch + stopped guard as `setLeader`). Add to `Leadership` interface with a doc comment; update header comment
  (`onChange` DOES NOT REPLAY paragraph → mention onRecovered).

## Task 2 — sync-status.ts stale recheck (F2)
- Export `STALE_RECHECK_MS = 2_000`.
- Tests in `sync-status.test.ts`:
  1. extend 'no lease: … finds out by itself': after stale, set `leaseLive = true` WITHOUT a new record,
     advance `STALE_RECHECK_MS` → `stale:false`, and `heard` called again exactly once.
  2. own-record case: channel whose `local().leader` toggles false (lapse) while its own record is > 10 s old and
     leaseLive false → stale; then `leader` true again, advance `STALE_RECHECK_MS` → `remote:false, stale:false`.
  3. while stale and nothing changes: advancing 60 s notifies no listener (dedup) and keeps exactly one timer.
  4. update 'a follower waiting to call its record stale leaves no timer behind either' to also cover the
     stale state (close after stale → 0 timers).
  5. timer hygiene on every branch (review #5): stale → becomes leader → the recheck timer is gone (only the publish
     timer, if any); stale → the record becomes another master's → no timer; own-record recovery → 0 stale timers.
  6. two followers (two module graphs, as the existing cross-window tests do) both stale; lease live again → both
     clear within `STALE_RECHECK_MS`, each with exactly one timer while stale. (review #6)
- Impl: line ~660: `staleTimer = setTimeout(refresh, stale ? STALE_RECHECK_MS : STATUS_STALE_MS - age + 1)`
  (keep the existing fresh-path arithmetic); fix the comment above it and the header paragraph ("It is `stale` only …").

## Task 3 — start.ts wiring (F1b)
- Test (start.test.ts or start.integration.test.ts, whichever already fakes leadership/time — follow the existing
  harness): lead, lapse (clock jump + a `changed()` trigger) → snapshot stale; let renew run → snapshot
  `remote:false, stale:false` immediately (before `STALE_RECHECK_MS`).
  If the harness cannot express a lapse, test via a `contendForLeadership` mock that exposes `onRecovered`.
- The `vi.mock('./leader')` fake in start.test.ts gains `onRecovered` + a `recover()` trigger (it is the only mock of leader).
- Also test (review #4): after the master is cleared / `stop()`, firing the old leadership's recovered listeners
  changes nothing (no driver, no `changed()`, snapshot unchanged) — i.e. `end()` unsubscribed.
- Also: recovered fires while `isLeader()` is false (the race of Task 1.7) → stays follower, no driver.
- Impl: in `enterMasterMode`, `const unrecovered = leadership.onRecovered(() => apply(leadership.isLeader()))`;
  call it in `end()` next to `unsubscribe()`.

## Verification
`cd spa && npx vitest run src/lib/profile`, then full `npx vitest run`, `pnpm run lint`, `pnpm run build`.
