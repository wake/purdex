# Alias write-back without a problem — spec + plan (#1369)

Status: rev 2 (codex plan review task-mudtuczk-t7jt07: #1 strict alias rule, #2 apply returns its payload, #3 the
`hostOrder` counter-case) · R1 (PR #1376: the payload goes back only if a rebuild after the hash still equals it,
§2.3) · 2026-09-23 · branch `worktree-alias-writeback-quiet` · base `4cecb6d3` (PR #1370 merged)

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
only when the full hashes differ (equal = no mismatch, nothing to say) and BOTH hold:

1. **Everything but `aliases` is equal.** The rebuilt section equals the INCOMING payload once `aliases` is removed
   from every row of both — compared by `hashSection` of both stripped payloads (the same canonical form the hashes
   use: object keys sorted recursively, arrays in order — so row keys, every other field and `hostOrder` all count).
2. **Each row's `aliases` changed only in the ways the canonical write-back changes them (rev 2, codex plan review
   #1 — "any aliases difference" would also silence an alias the stores LOST).** For every row key `k` present in
   both, with `local = plan.byRow.get(k)` (the local host that row was applied to), `in` = the incoming row's
   aliases normalised (`normaliseAliases`), `out` = the rebuilt row's aliases (absent = `[]`):
   - every alias of `out` that is not in `in` is `local` itself (the own id the build adds — `withOwnAlias`); and
   - every alias of `in` that is not in `out` was displaced by the cap: `out` has `MAX_HOST_ALIASES` entries and
     every one of them sorts before it.
   An unsorted or duplicated incoming list passes (normalised first). Anything else — an alias missing without the
   cap, an extra alias that is not this row's own id (e.g. a remembered `syncAliases` entry the incoming row did not
   carry) — leaves it unset and the problem is recorded as today. Erring that way costs a problem line, never data.

Other sections never set it (`settings`, `workspaces`, `tabs.*` have no aliases).

### 2.2 The executor does not record it, and still pushes

In the pull path: `if (mismatch && !outcome.aliasesOnly) problem('pull-hash-mismatch', …)`. Everything else is
unchanged: `pull-applied` with `localHash: outcome.hash` (the section is dirty), the push that follows sends the
rebuilt payload, the pull guard's barrier logic (#1366) is untouched.

### 2.3 The apply hands back the payload it rebuilt (rev 2, codex plan review #2)

draft 1 deleted the pending `repump` in the `awaitsCollector` branch. codex showed a way to stay stuck with it: the
collector reports a hash only once (`lastHash`, collector.ts ~289), so when the rebuilt hash is one it reported
before and the executor's stash has since pruned (`pruneMemoryStash`), no report ever comes — and that was the only
pump left. (The same wait exists on main without any repump; the repump then only turns it into a loop of
`push-payload-missing` retries.)

So the executor stops depending on the collector here: the ok arm of `ApplyOutcome` gains `payload?: unknown` —
the very payload the `hash` was computed from (every branch already builds it to hash it: `buildHostsSection`,
`buildSettingsSection`, `buildWorkspacesSection`, `buildTabsSection`; `null` hash → no payload). In the pull path,
before the `awaitsCollector` decision: `if (outcome.hash !== null && outcome.payload !== undefined)
stash.set(outcome.hash, outcome.payload)`. `awaitsCollector` is then false for every such outcome, the section goes
on to `afterPull` and pushes that payload; the #1370 `releaseBarrier(awaitingPayload)` stays as it is (it only
excludes a key while `awaitsCollector`). The branch itself stays as a fallback for an outcome without a payload
(a test double, a future branch). The collector's later report of the same hash sets the same stash entry and pumps
— harmless (the push already went, or goes once: `send` takes one in-flight token).

`pruneMemoryStash` keeps the entry: the section's `currentHash` is `outcome.hash` after `pull-applied`.

**R1 (PR #1376): a payload the stores moved away from is not handed back.** The apply builds the payload, then
awaits `hashSection` — and a user edit of the same section can land in that await. The payload is then a stale
snapshot: pushed, it would overwrite the SOT with the old content until the collector's report of the edit pushes
again (the collector path never pushed a stale snapshot). So `rebuilt` (apply-to-stores.ts) takes the build as a
function and, after the hash, builds once more synchronously; the payload goes back only if that rebuild has the
same `structuralKey` as the one hashed. Otherwise the outcome is `{ ok: true, hash }` — the hash still the one of
what the apply rebuilt, so `localHash` keeps its meaning — and the executor falls back to `awaitsCollector`, where
the collector reports the edit. All four branches build that way (`hosts`, `settings`, `workspaces`, `tabs.<id>`;
an unsettled master world or a vanished workspace in the rebuild counts as moved). `aliasesOnly` is still decided
on the payload that was hashed. The executor needs no check of its own: from the apply's return to the stash only
microtasks run, and a user event is a macrotask.

## 3. Not in scope

The wording of `pull-hash-mismatch` for real mismatches; any change to alias normalisation itself; the daemon.

## 4. Plan (TDD, one commit each)

**T1 — apply-to-stores: `payload` on every ok outcome.** Tests: each section kind's ok outcome carries the payload
whose `hashSection` is `hash`; a `null` hash carries none. Mutation: drop it from one branch → red.

**T2 — apply-to-stores: `aliasesOnly`.** Tests (apply-to-stores hosts tests): (a) a canonical row arriving without
this device's own id → `ok`, hashes differ, `aliasesOnly: true`; (b) an unsorted / duplicated incoming alias list →
same; (c) 16 incoming aliases, this device's own id sorts among them → one displaced by the cap → `aliasesOnly:
true`; (d) an incoming alias the rebuilt row lacks without the cap → absent; (e) the rebuilt row carries an alias
that is neither incoming nor its own id → absent; (f) another field differs, a row added / dropped, `hostOrder`
differs → absent; (g) build equals incoming → absent. Mutations: always unset → (a) red; skip rule 2 → (d)/(e) red;
skip rule 1 → (f) red.

**T3 — executor: no problem on an alias-only mismatch; the push needs no collector.** Tests (executor pull tests):
(a) fake apply returning `aliasesOnly: true`, a different hash and its payload → no `pull-hash-mismatch`, section
dirty, the push goes out with THAT payload without any collector report; (b) without `aliasesOnly` the problem is
still recorded; (c) the codex #2 case: a `hosts` pull in flight, another `pump('hosts')` arrives meanwhile, the
collector never reports (dedup) → no `push-payload-missing`, exactly one push with the outcome's payload, the
section settles. No guard. Mutations: drop the `aliasesOnly` condition → (a) red; drop the `stash.set` → (a)/(c)
red.

**T4 — end-to-end.** An integration test with two devices sharing the fake daemon (the host-identity integration
harness): B pulls A's profile → B's problem log is empty, B pushes the alias write-back once, the SOT row lists both
local ids, a second pass pushes nothing.

Gates: `cd spa && npx vitest run`, `pnpm run lint`, `npx tsc --noEmit -p tsconfig.app.json`.

## 5. Real machine

Two clients with independent host ids, own profile name. A pushes, B pulls: B's problem log is empty, the SOT
`hosts` row lists both ids (one extra rev), B synced. Clean up: close the wizard, Stop sync on both, REST DELETE.
