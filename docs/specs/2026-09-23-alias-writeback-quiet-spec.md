# Alias write-back without a problem — spec + plan (#1369)

Status: draft 1 · 2026-09-23 · branch `worktree-alias-writeback-quiet` · base `4cecb6d3` (PR #1370 merged)

## 1. Problem

After a device pulls `hosts`, its own canonical rows add the device's local id to `aliases` (spec
2026-09-23-host-sync-identity §11.2: the sorted, unique first-16 union — `withOwnAlias` / `normaliseAliases`,
lib/profile/sections.ts). So the hash rebuilt from the stores (`ApplyOutcome.hash`) differs from the SOT's, and the
executor's pull path (lib/profile/executor.ts, ~1455) records `pull-hash-mismatch · hosts` — "the stores did not
keep what arrived… will be pushed back" — which stays in the Current block's problem log until Stop sync. The
write-back itself is designed and correct (it happens once; after it every device's build equals the row). A user
sees an alarming problem for normal operation. Reproduced on the real machine (#1366 acceptance, 2026-09-23): B's
only problem after a pull is this one.

A second, related effect (found while fixing #1366, pre-existing, no guard needed): if anything calls `pump('hosts')`
while that pull is still running (e.g. `pumpAll()` after another re-index), `pump` records it in `repump`; the pull
returns `WAIT` on the "mismatch and no stash payload yet — wait for the collector's report" branch, and `run()`
then pumps `hosts` at once because of `repump` → `send()` finds no payload → a `push-payload-missing` problem, then
a retry once the collector reports. Harmless in the end, noisy, and against the rule written right there.

## 2. Design

### 2.1 The apply says when the only difference is `aliases`

`ApplyOutcome`'s ok arm gains an optional `aliasesOnly?: true`. The `hosts` apply (apply-to-stores.ts, ~421) sets it
when the section rebuilt from the stores equals the INCOMING payload once `aliases` is removed from every row of
both (same row keys, same `hostOrder`, same fields otherwise) — compared by `hashSection` of both stripped payloads,
so the comparison is the same canonical form the hashes use. It is set only when the full hashes differ (equal hashes
= no mismatch, nothing to say). Any other difference (a field the stores did not keep, a row dropped or added, an
order change) leaves it unset, and the problem is recorded as today.

Why "any aliases difference" and not "exactly own-id added": the build's aliases are the normalised union of what
arrived, what this device remembers (`syncAliases`) and its own id, capped at 16 — the own id can push another one
out, and an unsorted / over-long incoming list is normalised. Every alias-only difference is that canonical
write-back; none of them is data the stores failed to keep.

Other sections never set it (`settings`, `workspaces`, `tabs.*` have no aliases).

### 2.2 The executor does not record it, and still pushes

In the pull path: `if (mismatch && !outcome.aliasesOnly) problem('pull-hash-mismatch', …)`. Everything else is
unchanged: `pull-applied` with `localHash: outcome.hash` (the section is dirty), the push that follows sends the
rebuilt payload, the pull guard's barrier logic (#1366) is untouched.

### 2.3 A pull that waits for its collector payload is not repumped

In the `awaitsCollector` branch (mismatch, no stash payload yet → `WAIT`), also `repump.delete(key)`: the collector's
report of this very apply is the pump that must come next; a pump that arrived while the pull ran has nothing to
add (the next pump decides again from the state then). This generalises what #1370 did for `releaseBarrier`.

## 3. Not in scope

The wording of `pull-hash-mismatch` for real mismatches; any change to alias normalisation itself; the daemon.

## 4. Plan (TDD, one commit each)

**T1 — apply-to-stores: `aliasesOnly`.** Tests (apply-to-stores hosts tests): (a) a canonical row arriving without
this device's own id → `ok`, hashes differ, `aliasesOnly: true`; (b) an unsorted incoming alias list → same;
(c) the stores changing another field (e.g. a row whose field the store normalises differently) or a row count
difference → `aliasesOnly` absent; (d) build equals incoming → `aliasesOnly` absent. Mutation: always unset → (a)
red; always set → (c) red.

**T2 — executor: no problem on an alias-only mismatch.** Test (executor pull tests, fake apply returning
`aliasesOnly: true` with a different hash): no `pull-hash-mismatch` problem, section dirty, the push goes out with
the collector's payload. Counter-test: without `aliasesOnly` the problem is still recorded. Mutation: drop the
condition → red.

**T3 — executor: no repump into a payload-less push.** Test: `hosts` pull in flight, another `pumpAll()` / `pump`
of `hosts` arrives while it runs, apply returns a mismatch with no stash payload → no `push-payload-missing`; the
push goes out once, after the collector reports, with that payload. Without a guard. Mutation: remove
`repump.delete` → red.

**T4 — end-to-end.** An integration test with two devices sharing the fake daemon (the host-identity integration
harness): B pulls A's profile → B's problem log is empty, B pushes the alias write-back once, the SOT row lists both
local ids, a second pass pushes nothing.

Gates: `cd spa && npx vitest run`, `pnpm run lint`, `npx tsc --noEmit -p tsconfig.app.json`.

## 5. Real machine

Two clients with independent host ids, own profile name. A pushes, B pulls: B's problem log is empty, the SOT
`hosts` row lists both ids (one extra rev), B synced. Clean up: close the wizard, Stop sync on both, REST DELETE.
