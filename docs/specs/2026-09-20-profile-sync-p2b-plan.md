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
- **Invariant for both PRs: with no master set, the app is byte-for-byte today's app.** The driver
  returns before it subscribes, hashes or fetches anything.
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

## P2b-1 — transport

### Task 1 — projection correction (docs + `projections.ts`)
Remove `purdex-module-enabled` from `PROJECTIONS.settings`, `SettingsStorageKey` and everything
keyed on it (nine stores, not ten); update the guard snapshot. **No ordinal bump**: nothing is wired
and no SOT holds a `settings` section, so there is no older shape to be newer than — say so in the
commit. Spec §3.3 and §4.2 updated; logged in §9.7 as a reversal of a P2a choice, flagged for the
user (he can overrule it; the store's own comment is the evidence).

### Task 2 — `lib/client-identity.ts`
`getClientId(): string` — the one source of this client's id. Own persisted key
`purdex-client-identity` (`STORAGE_KEYS.CLIENT_IDENTITY`), **not** a zustand store: a plain
`browserStorage` read/write with an in-memory cache, because identity must not be replaced by a
cross-window rehydrate mid-request. First call: adopt the existing id from `purdex-sync-state`
(`state.clientId`) if it matches `^c_[0-9a-f]{12}$`, else generate (6 random bytes → hex). Never
changes afterwards. `useSyncStore.getClientId` delegates to it; the four production callers switch
to it. Tests: adoption, generation, stability across calls, a malformed stored value is replaced,
format; the backup dispatch tests keep passing.

### Task 3 — `lib/profile/api.ts`: the CAS client
```ts
export type PutOutcome =
  | { kind: 'applied'; rev: number } | { kind: 'converged'; rev: number }
  | { kind: 'conflict'; rev: number; hash: string | null; payload: unknown | null }
  | { kind: 'schema'; fingerprint: string; ordinal: number }
  | { kind: 'failed'; reason: 'network' | 'timeout' | 'contended' | 'not-found' | 'too-large' | 'rejected' | 'malformed'; status: number; retryAfterMs?: number }
export function listProfiles(hostId): Promise<ProfileIndexEntry[]>
export function createProfile(hostId, name) / renameProfile / deleteProfile
export function getProfileSections(hostId, profileId): Promise<Record<string, SectionRecord>>
export function getSection(hostId, profileId, section): Promise<SectionRecord | null>   // 404 → null
export function putSection(hostId, profileId, section, body): Promise<PutOutcome>
export function deleteSection(hostId, profileId, section, baseRev, clientId): Promise<PutOutcome>
export function putAttachment / deleteAttachment
```
- **`putSection` / `deleteSection` never throw.** Every outcome — network error, timeout, non-JSON
  409, a 200 without a finite `rev` — is a `PutOutcome`; this is what lets the executor guarantee
  one terminal event per flight. `AbortSignal.timeout(15_000)` on every request (injectable).
- Branch on `status === 409` before reading; `reason` decides conflict vs schema; an unparseable 409
  is `failed/malformed`. DELETE's 200 is `{rev}` → `applied`. 503 → `failed/contended` with
  `retryAfterMs` from the header. Response fields are **validated, not cast** (`rev` finite integer,
  `hash` 64-hex or absent).
- Read functions throw a `ProfileApiError(status, message)` (the `DeviceStateApiError` shape).
Tests mock `hostFetch` with real `Response` objects, one test per row above, plus a hung request
resolving to `failed/timeout` under fake timers.

### Task 4 — `'profile'` event: union + `lib/profile/profile-ws-dispatch.ts`
Add `'profile'` to `HostEvent.type`; one branch in `useMultiHostEventWs.ts` next to `backup:done`.
`dispatchProfileWsEvent(hostId, event)` parses and **validates** the value, maps
`deleted: true` or `hash === ''` → `hash: null`, and hands `{hostId, profileId, section, rev, hash,
writerClientId}` to a registered listener (`setProfileEventListener(fn | null)`) — the driver
registers in P2b-2; with no listener the event is dropped. It does **not** filter own events: the
reducer needs `own`, and "own" means `writerClientId === getClientId()` **and** this window is the
leader (decided by the driver). Malformed → ignored, never throws on the WS path.

### Task 5 — `stores/useProfileStore.ts`
Device-local, persisted under `purdex-profile` (`version: 1`), **not registered with
`syncManager`** (a rehydrate is a full-state replace; section bases must not be swapped under a live
driver — the leader is the only writer, followers read on demand in P3).
```ts
masterHostId: string | null      // the dev host the master lives on, pinned at attach time
masterProfileId: string | null
autoSync: boolean                // default true
sections: Record<string, { base: Held; currentHash: string | null }>   // what restoreSectionState takes
setMaster(hostId, profileId) / clearMaster()      // clearMaster wipes `sections`
setAutoSync(v) / putSectionBase(key, persisted) / dropSection(key)
```
Only `base` and `currentHash` persist — flights, locks, conflicts and epochs are rebuilt by
reconcile after a restart (P2a's `restoreSectionState` contract). `masterHostId` is stored rather
than derived from `devHostId`, so that changing the dev host does not silently re-point a master at
another daemon.

## P2b-2 — driver

### Task 6 — `lib/profile/apply-to-stores.ts`: commit an applied section
One function per kind, each: read the slices → `isWellFormedSection` → P2a `apply*` → write →
**`await store.persist.rehydrate()`** on every store written. Rehydrate is the proven cross-window
path and runs each store's `merge` / `onRehydrateStorage` (sanitise, heal, DOM, `t`), so no
per-store adapter is written. Specifics:
- `settings`: `rejected` non-empty → return `{ok:false}` (caller locks the section). If the patch
  for `purdex-ui-settings` contains `terminalRenderer`, call `bumpTerminalSettingsVersion()` after.
- `hosts`: also delete `runtime` rows of removed hosts. **Never apply a payload that removes the
  master's own host** (`masterHostId`) — that would cut the transport mid-apply; return
  `{ok:false, reason:'would-remove-master-host'}` and lock. Zero hosts is refused the same way.
- `workspaces` / `tabs.<id>`: inside `withOperationLock('profile-sync', …)`; refused → `{ok:false,
  reason:'busy'}` (the executor retries on the next tick, no lock). After `applyTabs`: `tabOrder =
  deriveTabOrder(…)`, `visitHistory` filtered to it, global `activeTabId` kept if it survives else
  the active workspace's `activeTabId`, else `null`; both stores written in one try/rollback like
  `replaceTabSnapshot`. Panes of removed hosts: mark `terminated: 'host-removed'` with a small pure
  helper over one tab's layout (the snapshot-typed `markMissingHosts` cannot be reused).
  tmux session codes are host-scoped and both clients talk to the same hosts, so **no reattach
  step**: a synced `tmux-session` pane is already valid here.
- Returns the hash **recomputed from the stores after the write** (`build*` + `hashSection`), which
  is what `pull-applied` reports — if a sanitiser changed something, the section is honestly dirty
  and pushes the sanitised form.

### Task 7 — `lib/profile/collector.ts`
`startCollector({onSectionHash})`: subscribes to the nine settings stores, hosts, workspaces, tabs;
hand-diffs only the projected slices; **per-section 500 ms trailing debounce**; on fire builds the
affected sections (`buildProfileDocument` wrapped in try/catch — a workspace id the daemon would
reject must not take every section down; that workspace's `tabs.*` is skipped and reported), hashes,
and reports `(key, hash | null, payload)`. **Always passes all nine settings stores.** A section
that disappeared reports `hash: null`. The payload goes to a stash keyed by hash, pruned to
`retainedHashes` ∪ current. `primeAll()` computes every section once (on start and after attach).

### Task 8 — `lib/profile/executor.ts`
Owns `Record<sectionKey, SectionSyncState>` in memory (seeded by `restoreSectionState` from
`useProfileStore`), and one loop: `dispatch(key, event)` → `reduceSection` → persist `{base,
currentHash}` if changed → `decideSection(state, {reachable, autoSync})` → run the action. One
action in progress per section.
- `reindex` → `listProfiles`, select the master; **profile missing from the list → every section
  `locked:reset`** (a recreated profile has a new id, so "gone" is the reset signal); else
  `profileLock(index, shapeTable())` — a schema lock stops all writes; else one
  `sot-index{epoch: indexEpoch, entry}` per known section, `entry: null` when not listed; then
  `reconcileSectionSet` for `tabs.*`.
- `push` / `delete` → dispatch `push-started{token}`; **send only if the resulting
  `inFlight === token`**; map `PutOutcome` → exactly one terminal event (`conflict` also stashes the
  SOT payload by hash; `schema` → profile schema lock + `push-failed`; `failed/contended` → retry
  after `retryAfterMs`; any other failure → `push-failed`).
- `pull` → `canApplyPull` → `getSection` (404 → deletion, `rev: state.sot.rev`) → Task 6 →
  `pull-applied` with the recomputed hash; `{ok:false, 'busy'}` → nothing, retried next tick;
  any other `{ok:false}` → `locked{conflict}` is wrong here → a new **`locked:invalid`-style
  outcome is not added**: report it through `onProblem` and leave the section clean-but-behind (it
  will retry on the next event); P3 surfaces it.
- `restore-local` → `canRestoreLocal` → apply the stashed payload via Task 6 → `local-restored`.
- `lock-conflict` / `lock-reset` → dispatch `locked`.
- Remote events: `own = writerClientId === getClientId()`; `profileId !== master` → ignored.
- Retry: a failed push/pull is retried on the next collector tick, reconnect, or a 30 s timer —
  never a hot loop.

### Task 9 — `lib/profile/leader.ts` + `lib/profile/start.ts` + `main.tsx`
`acquireLeadership(): Promise<() => void>` — `navigator.locks.request('purdex-profile-sync',
{mode:'exclusive'}, () => new Promise(release => …))`, held for the window's lifetime; a second
window's promise stays pending until the first closes, then it takes over (and starts with every
section `indexStale`, by `restoreSectionState`). No `navigator.locks` (old runtime, jsdom) → lead
immediately. Followers run nothing: their edits reach the leader through the existing `syncManager`
rehydrate of the app stores, and the leader's applies reach them the same way.
`startProfileSync()`: no master → subscribe to `useProfileStore` only and wait; master set → lead →
collector + executor + the WS listener + a `useHostStore` watcher that dispatches `reconnected` to
every section when `runtime[masterHostId].status` becomes `connected` (and `reachable = false`
otherwise). `clearMaster()` tears it all down. One line in `main.tsx`.
In `import.meta.env.DEV` only, `window.__purdexProfileSync = {attach(hostId, profileId), detach(),
state()}` — the hook the real-machine acceptance uses until P3's UI exists.

### Acceptance for P2b-2 (real machine, run not assumed)
Worktree dev server `:5175` against the mlab daemon, two Playwright sessions = two clients with
distinct `clientId`s (separate browser contexts). Create a profile by `curl`, attach both through
the dev hook, then: (1) first client pushes — `GET /api/profiles` shows `hosts`, `settings`,
`workspaces`, one `tabs.*` per workspace at rev 1; (2) second client, clean, pulls; (3) rename a
workspace on A → B reflects it; **record the wall time**; (4) same edit on both → `applied:false`;
(5) daemon stopped, edit the same workspace on both, restart → second flush 409 → that section
`locked:conflict`, others keep syncing; (6) offline edits flush on return; (7) split ratios and
focus do not move on the other side; (8) no master → `requests` shows zero `/api/profiles` calls.

## Risks
- **The rehydrate-after-write trick** depends on `persist` flushing synchronously to
  `localStorage` before `rehydrate()` reads it. It does today (`browserStorage` is sync); Task 6
  asserts it with a test per store family rather than assuming it.
- **A follower window shows stale sync status** until P3 reads `useProfileStore` on demand.
- The 30 s retry timer is the only clock in the driver; everything else is event-driven.
