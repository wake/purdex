# Plan — Profile Sync P2b: transport and wiring (two PRs)

- Spec: `2026-09-20-profile-sync-spec.md` §4.4–§4.7, §9.4–§9.6. P2a's contract for this phase is the
  header of `spa/src/lib/profile/sync-state.ts` ("driver contract") and the "As built" / "After the
  PR review" sections of `2026-09-20-profile-sync-p2a-plan.md`. P1's wire shapes are read from
  `internal/module/profiles/handler*.go`, not from the spec (measured below).
- Worktree `.claude/worktrees/profile-sync`, based on `origin/main` alpha.412.
- **Two PRs**, because the whole is over 20 files:
  - **P2b-1 — transport** (branch `worktree-profile-sync-p2b`): the CAS client, the `'profile'`
    host event, `useProfileStore`, client identity, one projection correction. Still unwired: no
    service starts, the app is unchanged.
  - **P2b-2 — driver** (branch `worktree-profile-sync-p2b2`, after P2b-1 merges): collector,
    executor, apply wiring, leader election, `startProfileSync()` in `main.tsx`.
- **Invariant for both PRs: with no master set, the app behaves as today.** The driver returns
  before it subscribes, hashes or fetches anything. (P2b-1 does write one new `localStorage` key,
  `purdex-client-identity`; nothing reads it but the four existing `clientId` callers.)
- **Revised after the codex plan review** (`task-mu90jwwz-h2c507`, 17 findings, 5 critical, all
  accepted; spec §9.7). The first draft's leader election (Web Locks) and its "followers reach the
  leader through `syncManager`" premise were both wrong; see Tasks 5, 9 and 10.
- Every task: subagent, TDD (failing test first), one commit per task, `git commit --only`, every
  Bash prefixed with `cd /Users/wake/Workspace/wake/purdex/.claude/worktrees/profile-sync/spa &&`.
  Parallel tasks run only their own test file. Every task prompt carries: *"if the plan contradicts
  itself or cannot be implemented as written, report it — do not silently pick a reading"*, and
  mutation results are a deliverable.
- Before each PR: `npx vitest run && pnpm run lint && npx tsc -p tsconfig.app.json --noEmit &&
  pnpm run build`.

## Measured baseline (2026-09-20, alpha.412)

**P1 wire shapes (from the Go source).**
- There is **no per-profile index route**. The index is `GET /api/profiles` →
  `{profiles: [{id, name, createdAt, updatedAt, sections: SectionMeta[], attachments: []}]}`;
  `SectionMeta` = `{section, rev, hash, fingerprint, ordinal, writer, updatedAt}`; tombstones are
  omitted. `GET /api/profiles/{id}` → `{sections: {<key>: Section}}` **with payloads**.
  `GET …/sections/{section}` → `Section`; **404 for a tombstone and for an unknown profile alike**.
- `PUT …/sections/{s}` body `{clientId, baseRev, hash, fingerprint, ordinal, payload}` →
  200 `{rev, applied}` · 409 `{reason:'conflict', rev, hash?, payload?}` (absent: exactly
  `{reason, rev: 0}`) · 409 `{reason:'schema', fingerprint, ordinal}` · 400/404/413 **text/plain** ·
  **503 + `Retry-After: 1`**, text/plain, a normal outcome · 500.
  `DELETE …/sections/{s}?baseRev=N&clientId=…` → 200 **`{rev}` only (no `applied`)** · same 409.
- 409 bodies are JSON (`writeJSONStatus`); every other error is `http.Error` text. A `Response`
  body can be read once, so the client branches on `status === 409` **before** reading.
- `profile` event value: `{profileId, section, rev, hash, writerClientId, deleted?}`; on a delete
  **`hash` is `""`** (no `omitempty`) and `deleted: true`.

**SPA.**
- `hostFetch(hostId, path, init)` (`lib/host-api.ts:193`) returns a raw `Response`, throws only what
  `fetch` throws, **has no timeout**; `init.signal` passes through.
- Background services are `startXxx(): () => void`, called once from `main.tsx` before render,
  never React hooks (`main.tsx:19-41`). "A host just (re)connected" has no callback bus; the
  precedent is watching `useHostStore.runtime[id].status` transitions
  (`lib/host-config-loader.ts:11-37`). `runtime` is not persisted.
- Stores have no `subscribeWithSelector`; a subscriber gets `(next, prev)` and hand-diffs.
- `HostEvent.type` is a closed union (`lib/host-events.ts:3-16`); dispatch modules are
  `(hostId, event) => void`, parse `event.value` themselves and swallow parse errors
  (`storage-backup/backup-ws-dispatch.ts:17-27`); `useMultiHostEventWs.ts:242-265` is the `if`
  chain they hang off, `hostId` by closure.
- `clientId` lives in `useSyncStore` (`lib/sync/use-sync-store.ts:120-126,166-172`, persisted under
  `purdex-sync-state`), format already `^c_[0-9a-f]{12}$`. Production callers outside the Sync UI:
  `stores/useBackupStore.ts:152`, `lib/storage-backup/backup-ws-dispatch.ts:25`,
  `lib/device-state/uploader.ts:117`, `components/settings/device-state/DeviceStateSection.tsx:64`.
  The first two belong to Storage-backup, which is **not** deleted in P4.
- **A bare `store.setState(patch)` skips the invariants of eight of the ten settings stores** —
  they live in `persist`'s `merge` / `onRehydrateStorage` and in the setters only:
  `useI18nStore` keeps the translator **`t` as a state field** rebuilt by hand (a raw patch leaves
  the whole UI in the old language), `useThemeStore` needs `registerTheme` per custom theme and
  `applyThemeToDom`, `useLayoutStore` needs `healLayoutInvariant`, the rest sanitise.
  `useUISettingsStore.terminalSettingsVersion` is bumped only by `bumpTerminalSettingsVersion`.
  `sanitizeHostConfig` likewise runs only in `useHostStore`'s persist `merge`.
- **The cross-window path already does exactly what an apply needs**: `browserStorage.setItem` →
  `syncManager.notify` → other windows call `store.persist.rehydrate()`, which runs `merge` and
  `onRehydrateStorage` and ends in a normal `set` (so subscribers fire).
- **No leader election exists.** Every `start*()` runs in every window. Two windows share one
  persisted `clientId`, so `writerClientId` self-filtering would make each discard the other's
  broadcasts, and a `syncManager` rehydrate is a full-state replace that would destroy an open
  flight's token.
- `useTabStore` needs `tabs`, `tabOrder`, `activeTabId`, `visitHistory` kept consistent
  (`replaceTabSnapshot`, `lib/snapshot/restore.ts:272-312`, is the reference); `applyTabs` returns
  `{tabs, workspaces}` only. `removeHost` also drops `runtime[id]`; `applyHosts` does not.
- `withOperationLock(owner, body, onRefused, parent?)` (`stores/useRebuildStore.ts:190`): no
  queueing — refused means `body` never runs.
- `hashSection` is `crypto.subtle`-backed and **cannot be flushed by fake timers**
  (`device-state/uploader.test.ts:23-27` documents it); tests mock `./hash` to `structuralKey`.
- `useModuleEnabledStore.ts:64-66` states in code that module on/off is **device-local by intent**
  ("a host with limited resources can turn off modules it doesn't want to run") — yet P2a listed
  `purdex-module-enabled.enabled` in the `settings` projection.

## Task order (plan review #17)

P2b-1: **1 → (2 ∥ 3) → 4 → 5** — 2 and 5 both add a `STORAGE_KEYS` entry, so they are not parallel;
4 uses 2's `getClientId` only in its test. Main session runs the whole suite + lint + tsc after
each wave. P2b-2: **6 → (7 ∥ 8) → 9 → 10 → 11**, same rule.

**Test clock (plan review #16).** Every test with a debounce or a retry timer mocks `./hash` to the
synchronous `structuralKey` (the `uploader.test.ts:23-27` precedent): a `crypto.subtle` digest
resolves off the microtask queue and fake timers cannot flush it, so an un-mocked test can go green
having verified only that the timer *fired*. `hashSection` itself stays covered by `hash.test.ts`.

## P2b-1 — transport

### Task 1 — projection correction (`projections.ts`, `types.ts`, docs)
Remove `purdex-module-enabled` from `PROJECTIONS.settings`, `SettingsStorageKey` and everything
keyed on it (nine stores). **Bump `SECTION_SCHEMA_ORDINAL.settings` to 2** and update the guard
snapshot in the same commit — the protocol says a projection change bumps the ordinal, and P1's
routes have been public since alpha.411, so "nothing can have written the old shape" is a belief,
not a fact (plan review #13). Spec §3.3 / §4.2 updated; §9.7 records it as a reversal of a P2a
choice, **flagged for the user**: the evidence is the store's own comment
(`useModuleEnabledStore.ts:64-66`), and he may overrule it.

### Task 2 — `lib/client-identity.ts`
`getClientId(): string`. Own key `purdex-client-identity` via `browserStorage`, **not** a zustand
store. **Storage is the truth; there is no permanent in-memory cache** (plan review #11): each call
reads the key; if it holds a valid `^c_[0-9a-f]{12}$` it is returned. Otherwise: adopt
`purdex-sync-state`'s `state.clientId` if valid, else generate (6 random bytes → hex); **write, then
read back and return what is stored** — two windows racing the first call converge on the last
writer on their next call instead of each caching its own forever. `useSyncStore.getClientId`
delegates **and still `set({clientId})`** so the state field other Sync code reads
(`register-sync.ts:58`, `use-sync-store.test.ts:114-116`) keeps its contract (#12). The four
production callers switch over. Tests: adoption, generation, malformed value replaced, the
two-realm race (`vi.resetModules()` + shared `localStorage`, the
`usePlaceholderFilesStore.cross-window.test.ts` precedent), the Sync store field still populated.

### Task 3 — `lib/profile/api.ts`: the CAS client
```ts
export type PutOutcome =
  | { kind: 'applied'; rev: number } | { kind: 'converged'; rev: number }
  | { kind: 'conflict'; rev: number; hash: string | null; payload: unknown | null }
  | { kind: 'schema'; fingerprint: string; ordinal: number }
  | { kind: 'failed'; reason: 'network' | 'timeout' | 'aborted' | 'contended' | 'not-found' | 'too-large' | 'rejected' | 'unauthorized' | 'server' | 'malformed'; status: number; retryAfterMs?: number }
export type ListOutcome = { ok: true; profiles: ProfileIndexEntry[] } | { ok: false; reason: …; status: number }
listProfiles / createProfile / renameProfile / deleteProfile / getSection / putSection / deleteSection / putAttachment / deleteAttachment
```
- **No function in this module throws**, including when `hostFetch` itself throws synchronously
  (unknown host → `getDaemonBase`): the whole call is inside `try`. Reads return discriminated
  results too (#7, #14) — "the list request failed" must be unrepresentable as "the list is empty".
- Every request takes an `AbortSignal` (the executor's) combined with `AbortSignal.timeout(15 s)`.
- Branch on `status === 409` before reading the body; `reason` picks conflict / schema; a 409 that
  is not that JSON is `failed/malformed`. DELETE's 200 is `{rev}` → `applied`. 503 →
  `failed/contended` + `retryAfterMs`. 401/403 → `unauthorized`. `getSection` 404 → `{ok: true,
  section: null}`. **Every field is validated, not cast** (`rev` a non-negative safe integer, `hash`
  64-hex or absent, `payload` an object).
- Status → outcome table checked row by row against `handler_sections.go` in the tests, one test
  per row, plus: a hung request → `timeout` under fake timers; an aborted one → `aborted`.

### Task 4 — `'profile'` event
`'profile'` into `HostEvent.type`; one branch in `useMultiHostEventWs.ts` beside `backup:done`.
`lib/profile/profile-ws-dispatch.ts`: parse and **validate** the value; `deleted: true` or
`hash === ''` → `hash: null`; hand `{hostId, profileId, section, rev, hash, writerClientId}` to a
listener registered with `setProfileEventListener(fn | null)`. No listener → dropped. It does not
filter own events (the reducer wants `own`). Malformed → ignored; never throws on the WS path.

### Task 5 — two stores, because one could not serve both needs (plan review #2)
- **`stores/useProfileStore.ts` — the control plane.** `purdex-profile`, `version: 1`,
  **registered with `syncManager`**: `masterHostId`, `masterProfileId`, `autoSync` (default `true`),
  `setMaster / clearMaster / setAutoSync`. Every window must agree on these, or a follower's
  `detach()` never reaches the leader and a window that was open before the attach never queues
  for leadership.
- **`lib/profile/section-store.ts` — the leader's working state.** Plain `browserStorage` under
  `purdex-profile-sections`, keyed by `masterProfileId` (a different master never sees these
  bases): per section `{base, currentHash, conflict?}` and a **payload stash for the hashes a
  conflict retains**. Only the leader writes it; it is deliberately *not* a synced zustand store,
  because a cross-window rehydrate is a full-state replace and would swap bases under a live
  driver. `conflict` and its payloads persist so that a restart restores the lock with **the
  snapshot that was sent**, not whatever the stores hold by then (#10; spec §4.6.2).
  `clearMaster` / a change of `masterProfileId` wipes it.

### P2b-1 as built, and after its PR review (spec §9.8)

Split into **P2b-1a** (PR #1242: Tasks 1–2, 20 files) and **P2b-1b** (Tasks 3–5 + the review fixes)
— the whole was 31 files. What P2b-2 must know:

- **`getDaemonBase` does not throw for an unknown host — it silently falls back to the active host,
  then to `127.0.0.1:7860`** (`useHostStore.ts:318-325`; this plan's first draft said it throws). A
  CAS aimed at a master host that has left the store would have been delivered to *another daemon*.
  `api.ts` checks `hosts[hostId]` first and answers `failed/unknown-host` without sending a byte.
- `AbortSignal.timeout` is **not driven by fake timers**; `api.ts` hand-rolls `AbortController` +
  `setTimeout`, clears it on every path, and races the request against the abort so a transport
  that ignores the signal still ends on time. Request *building* (path encoding, `JSON.stringify`)
  happens inside the protected entry too — a lone surrogate or a `BigInt` is a `failed/rejected`,
  never a throw.
- `api.ts` results: `Result<T>` = `{kind:'ok', value}` | `Failure`; `PutOutcome`, `DeleteOutcome`,
  `DeleteProfileOutcome`. `getSection` 404 → `ok/null`. One bad row in `listProfiles` makes the
  whole result `malformed` (a dropped row would read as "the profile is gone").
- `profile-ws-dispatch`: **`subscribeProfileEvents(fn)` returns an unsubscribe that removes only
  itself** (a late cleanup from a previous driver cannot silence the next one). Only the two wire
  shapes the daemon emits are accepted — `deleted:true` with `hash:""`, or a 64-hex `hash` without
  `deleted`; a contradictory event is dropped (events are an optimisation; the reindex catches up).
- `client-identity`: `getClientId()` reads storage every time; **`isClientIdPersisted()`** — the
  driver refuses to attach when it is false (an id that does not survive a reload must not be
  written into an attachment).
- `useProfileStore` (synced control plane): `masterHostId`, `masterProfileId`, `autoSync`,
  `setMaster → boolean`, `clearMaster`, `setAutoSync`, `selectMaster`; `merge` sanitises half-set or
  malformed masters to `null`.
- **`section-store`: one thing, one key — and no fencing** (critic C-1/C-2, spec §9.8).
  `purdex-profile-sections:<profileId>:s:<sectionKey>` holds `{base, currentHash, conflict?}`;
  `…:p:<hash>` holds one payload, content-addressed (rewriting it is idempotent). A write to
  `settings` cannot touch `hosts`, a stash write cannot touch a section, and another profile's keys
  are out of reach by prefix. **The generation fence was removed on purpose**: `localStorage` has no
  cross-process transaction, so "read, compare, write" narrowed the window and closed nothing, while
  the API read as a guarantee. Leader exclusion is the lease's job (Task 10), and only the lease's.
  API: `loadSectionStore`, `saveSection`, `saveConflict(profileId, key, section, payloads)`,
  `dropSection`, `putStash`, `getStash`, `pruneStash`, `clearSectionStore(profileId?)` →
  `'ok' | 'failed'`. `saveConflict` writes **payloads first, the section last**, so a stored
  conflict always has its payloads; `saveSection` refuses a conflict; on load a conflict whose
  payload is missing is dropped and the base kept.
  - *Residual, stated not solved:* the same section key written by two leaders is last-writer-wins.
    Both values are bases a leader wrote after a completed CAS; if the older wins, the next push
    409s and either converges or becomes one conflict the user resolves — a false conflict, never
    silent loss, because the SOT is guarded by the daemon. In that same window one leader's
    `pruneStash` can remove a payload the other has just written; that conflict then does not
    survive a restart.
  - *Known limitation — this is not conformance:* the payload cap is the daemon's 5 MiB, and
    whether a payload is actually kept is decided by the browser's quota (~5–10 MB per origin,
    shared). When it does not fit, the conflict lives in memory only and **the sent snapshot is
    gone after a restart** — the user would then be choosing against a moving target, which is
    exactly what spec §4.6.2 exists to prevent. Tracked in #1244 (IndexedDB). Real payloads are
    KB-sized today; the executor must still surface it (`onProblem`) rather than hide it.

## P2b-2 — driver

### Task 6 — reducer additions (`sync-state.ts`)
- **`locked:invalid`** (#8, #9): status + `{type:'locked', reason:'invalid', rev}`. The SOT holds
  something this client refuses to apply (ill-formed, `rejected` settings, a `hosts` payload that
  removes or re-points the master's own host). Recorded with the SOT rev it refers to; **a SOT
  observation with a higher rev unlocks it** (someone fixed it), `resolved keep:'local'` rebases on
  the SOT and pushes the local copy over it; `keep:'sot'` is refused. No timer ever re-fetches a
  payload already judged unusable.
- `restoreSectionState` accepts an optional persisted `conflict` and restores `locked:conflict`.
- `profileStatus` ranks `locked:invalid` between `locked:conflict` and `pending`.
Same TDD / mutation / property-test discipline as P2a; the property generator gains the new event.

### Task 7 — `lib/profile/apply-to-stores.ts`
Per kind: read slices → `isWellFormedSection` → P2a `apply*` → write → **`await
store.persist.rehydrate()`** on each store written (the proven cross-window path: runs `merge` /
`onRehydrateStorage` — sanitise, heal, DOM, the i18n `t`). The task **first proves, per store
family, in a test**: (a) `persist` has flushed to `localStorage` synchronously by the time
`rehydrate()` reads; (b) the store's hook really ran (i18n `t` switches language; theme DOM
attribute moves; `healLayoutInvariant` fires; a `migrate` does not run on a same-version rehydrate);
(c) **`useI18nStore`'s rehydrate does not override the applied `activeLocaleId` via
`detectLocale`**. If any of these fails for a store, stop and report — that store then gets an
explicit adapter instead. The write and the rehydrate are done without an intervening `await`
wherever the store allows, and the function returns **the hash recomputed from the stores
afterwards**, so a sanitiser's change shows up honestly as a dirty section.
- `settings`: `rejected` non-empty → `{ok:false, invalid}`. `terminalRenderer` in the patch →
  `bumpTerminalSettingsVersion()`.
- `hosts`: refuse (`invalid`) a payload that removes `masterHostId`, **changes its `ip` / `port` /
  `token`** (#9: the credentials in hand are the ones that just fetched this payload, so they are
  the ones known to work), or leaves zero hosts. Drop `runtime` rows of removed hosts.
- `workspaces` / `tabs.<id>`: inside `withOperationLock('profile-sync', …)`; refused → `{ok:false,
  busy}` (retried, never locked). After `applyTabs`: `tabOrder = deriveTabOrder(…)`, `visitHistory`
  filtered, global `activeTabId` kept if it survives, else the active workspace's, else `null`; both
  stores in one try/rollback like `replaceTabSnapshot`. Panes of removed hosts → `terminated:
  'host-removed'` via a small pure per-layout helper. Session codes are host-scoped and both clients
  talk to the same hosts, so no reattach step.

### Task 8 — `lib/profile/collector.ts`
Subscribes to the nine settings stores, hosts, workspaces, tabs; hand-diffs projected slices only;
**per-section 500 ms trailing debounce**; builds inside try/catch (a workspace id the daemon would
reject skips that one `tabs.*`, reported, never the whole document); **always passes all nine
settings stores**; reports `(key, hash | null, payload)`; a vanished section reports `null`.
`primeAll()`. Also **`watchUnsyncedStores()`** (#3): while a master is set, *every* window listens
to the native `storage` event and calls `persist.rehydrate()` for projected stores that are not
`syncManager`-registered (`purdex-editor-settings` today — derived by checking the registry, not
hard-coded), so a follower's edit reaches the leader's memory and a leader's apply reaches the
follower's UI.

### Task 9 — `lib/profile/executor.ts`
In-memory `Record<key, SectionSyncState>` seeded from the section store; `dispatch → reduceSection →
persist → decideSection → act`.
- **Lifecycle** (#6): an executor has a generation and an `AbortController`. `dispose()` aborts
  every request and flips `disposed`; **every continuation checks it after every `await` and drops
  its result** — a disposed executor's state machines are garbage, so "zero terminal events" is
  correct for them and nothing of theirs is persisted. A new master gets a new executor.
- **One network write at a time per profile** (#7): pushes and deletes go through a FIFO. A `schema`
  outcome sets the profile schema lock **before the next write is dequeued**, so no write can start
  after it — which is what makes "`locked:schema` is the whole profile" true rather than eventual.
  Reads are not serialised.
- **`reindex` is profile-level single-flight** (#14): many sections asking share one
  `listProfiles`. `ok:false` → **no event at all**, sections stay stale, retry with backoff
  (2 s → 30 s cap, reset on success). Only a well-formed `ok:true` list that lacks the master id
  means the profile is gone → every section `locked:reset`. Otherwise `profileLock(index,
  shapeTable())`, then one `sot-index{epoch: indexEpoch}` per known section, then
  `reconcileSectionSet`.
- `push`/`delete`: dispatch `push-started`; **send only if the resulting `inFlight === token` and
  this window still holds the lease** (Task 10); each `PutOutcome` → exactly one terminal event
  (`conflict` stashes the SOT payload; `contended` → `push-failed` + retry after `retryAfterMs`).
- `pull`: `canApplyPull` → `getSection` → Task 7 → `pull-applied` (recomputed hash); `busy` →
  retry; `invalid` → `locked{invalid, rev}`.
- `restore-local`: `canRestoreLocal` → Task 7 with the stashed payload → `local-restored`.
- Attachment (#5): `putAttachment` on attach and on every reconnect (idempotent; refreshes
  `lastSeen`); `deleteAttachment` on detach. Without it the daemon's "409 while attached" protects
  nothing and spec acceptance 12 cannot pass.
- Remote events for another profile are ignored; `own = writerClientId === getClientId()`.

### Task 10 — `lib/profile/leader.ts`: a lease, not a lock
`navigator.locks` exists only in secure contexts, and the Electron dev window loads
`http://100.64.0.2:5174` (#4) — and a `locks.request()` promise only resolves once the lock is
*released*, so the first draft's `acquireLeadership(): Promise<release>` could never have returned
(#1). Instead: `purdex-profile-leader = {windowId, expiresAt}` in `localStorage` (synchronous and
shared by every same-origin window, secure or not). Acquire: if absent or expired, write own record,
**wait a random 50–150 ms, read back**, lead only if it is still ours. Renew every 2 s with a 6 s
TTL; release on `pagehide`; a `storage` event on the key wakes waiters. `onLead(cb)` / `onLose(cb)`;
losing the lease disposes the executor. A brief double-leader on takeover is possible and tolerated:
the CAS protects the SOT, and the executor re-checks the lease before every write. Clock injected.

### Task 11 — `lib/profile/start.ts` + `main.tsx`
`startProfileSync()`: subscribes to `useProfileStore` (synced, so every window sees attach/detach);
no master → nothing else; master set → contend for the lease → on lead: section store, collector,
executor, WS listener, and a `useHostStore` watcher that turns `runtime[masterHostId].status`
becoming `connected` into `reconnected` for every section (and `reachable = false` otherwise).
Followers run `watchUnsyncedStores()` only. `import.meta.env.DEV` only:
`window.__purdexProfileSync = {attach(hostId, profileId), detach(), state()}` — attach/detach go
through the same code path P3's wizard will call (attachment included).

### Acceptance for P2b-2 (real machine — run, not assumed)
Worktree dev server `:5175` against the mlab daemon; **two Playwright browser contexts = two clients
with distinct `clientId`s**. Spec §6 items that belong to this phase, by their spec number:
1 first push (every section at rev 1) · 2 second client pulls · 3 propagation, **wall time
recorded** · 4 converged → `applied:false` · 5 conflict locks one section only · 6 offline edits
flush · **6a** merely-behind is not a conflict · **6b** a dirty section refuses an inbound apply ·
**6c** section lifecycle, no orphaned `tabs.*` row in the daemon · **7** lowered
`SECTION_SCHEMA_ORDINAL.settings` → whole profile `locked:schema`, **zero further writes** in
`requests` · 8/9 focus and split ratios are not mirrored · **12** `DELETE /api/profiles/{id}` is 409
while attached, 200 after both detach · **13** the pulled client reaches every host without
re-entering a token · plus: no master → zero `/api/profiles` requests; two windows of one context →
exactly one of them issues writes.
**Not covered here, stated plainly:** the cross-*machine* run on air-2026 that spec §6 asks for. The
App on a26 loads the main checkout's `:5174`, which this isolated worktree session cannot `git pull`;
that run is the user's acceptance after P3, and the report says so.

## Risks
- **Rehydrate-after-write** is an assumption until Task 7's per-store proofs pass; the fallback
  (explicit adapters for the stores that fail) is sized at a few dozen lines each.
- **A lease is not a lock.** Two leaders can overlap for up to one jitter window on takeover; the
  design tolerates it rather than preventing it (Task 10).
- **A follower window's sync status is stale** until P3 reads the section store on demand.
