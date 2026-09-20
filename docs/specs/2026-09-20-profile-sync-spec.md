# Spec — Profile Sync: one profile is one whole Purdex state, synced through the dev host

- **Date:** 2026-09-20
- **Status:** Draft
- **Base commit:** `a4f3e957` (1.0.0-alpha.410)
- **User decisions:** captured live on 2026-09-20; every numbered decision in §4 is the user's own
  choice, recorded so it is not re-derived. Where a decision was refined by a measurement, the
  refinement is marked and the reason given.
- **Replaces:** device-state backup (`2026-09-14-device-state-backup-spec.md`), workspace snapshot,
  and the Sync module. Their teardown is part of this work (§6, phases P4a/P4b).
- **Depends on:** `internal/core/events.go` (host-events broadcast), `useHostStore.devHostId`.

## 1. Problem

Two workstations are in daily simultaneous use — air-2019 and air-2026 — and each runs its own
Purdex with its own workspaces and tabs. The daemon on mlab already holds proof of the split:

```
$ sqlite3 ~/.config/pdx/device_state.db 'select device_name, workspace_count, tab_count from device_state'
Wakes-Air-2026.local|7|11
Wakes-Air-2019-Ori.local|7|16
```

Two unrelated worlds, 7 workspaces each, uploaded to the same daemon, never reconciled. The user's
words for the consequence: the two sides' tabs get confused and he cannot switch context quickly.

Three features already circle this problem and none of them solves it:

- **device-state backup** uploads a structure snapshot per client and offers *replace* or *merge*
  buttons — one-way, manual, and merge only ever adds.
- **workspace snapshot** stores one capture in `localStorage` — single machine, no transport.
- **the Sync module** has an engine, three-way merge, six contributors, a daemon package and seven
  routes; all three machines have **0 rows** in every `sync.db` table. It has never been used.

What is needed is not another backup. It is a **synchronisation architecture**: one named,
switchable bundle of "everything Purdex knows" that two clients hold the same view of, within about
a second, with conflicts surfaced to a human rather than merged behind his back.

## 2. Goals / non-goals

### Goals

1. A **profile** is one complete Purdex state: hosts (with their tokens), all settings, the
   workspace list, and every workspace's tabs.
2. The **dev host daemon holds the source of truth**. Clients hold a pre-SOT document and
   reconcile against it.
3. **Sub-second propagation** on the happy path, over the existing host-events WebSocket.
4. **No silent overwrite, ever.** Concurrent divergence stops the section and asks the user which
   side wins.
5. **A schema change in one client cannot corrupt an older one.** The older client stops and says
   "upgrade me".
6. The **top-left Home button becomes the profile switcher** (today it has one function — focus a
   tab that belongs to no workspace — and both machines have zero such tabs).
7. **Working offline is normal, not degraded.** The dev host being down means changes accumulate,
   not that the app locks.
8. Removing device-state, workspace snapshot and the Sync module is part of this work, not a
   follow-up.

### Non-goals

- **No automatic merge.** No three-way merge, no CRDT, no last-writer-wins. The only automatic
  resolution is "both sides are already identical".
- **No multi-user.** One person, several machines. Tokens sync because they are all his.
- **No profile transport between dev hosts.** A profile lives on one daemon.
- **No mirroring of focus.** Two clients are two viewports, not one screen.
- **tmux sessions are not in scope.** They belong to a host and are addressed by tabs; a profile
  never owns, creates or kills one.
- **No migration.** Alpha convention (`feedback_no_alpha_migration`): the user runs the wizard once
  per machine.

## 3. Measured facts this design rests on

All measurements taken 2026-09-20 against `a4f3e957` and the three live daemons.

### 3.1 Live state

| Where | Fact |
|---|---|
| `device_state.db` on mlab | 2 rows, both written by clients on alpha.402; a26 7 ws / 11 tabs, a19 7 ws / 16 tabs |
| Standalone tabs (tab in no workspace) | **0 on both machines** (parsed from the stored payloads) |
| Pane split nodes | **0 on both machines** — nobody has split a tab |
| `sync.db` (all 4 tables, all 3 hosts) | 0 rows |
| `host_config.db` on mlab | 2 rows (projects / commands, first written 09-17) |

### 3.2 What the client already has

- **The collector already exists, as a one-purpose version of what this spec needs.**
  `spa/src/lib/device-state/uploader.ts:51` subscribes to the tab / workspace / host / device-name
  stores, debounces 5 s trailing, hashes, and uploads only when the hash changed, targeting
  `selectDevHostId` and requiring a `connected` runtime status. This is the shape of the Profile
  collector; it gains sections, compare-and-set and an inbound direction.
- **The hashing primitives exist.** `spa/src/lib/device-state/payload.ts:83 structuralKey()` is a
  recursive key-sorted JSON serialisation with `capturedAt` stripped; `:89 hashPayload()` is
  SHA-256 over it (async — `crypto.subtle`). Both are typed to `WorkspaceSnapshot`, so
  `lib/profile/hash.ts` is a generic copy (no `capturedAt` rule — sections have none); the
  originals are deleted with their module in P4b. *(corrected in P2a, §9.3)*
- **The applier exists.** `spa/src/lib/device-state/restore.ts:101 runDeviceStateRestore()` already
  does the hard parts of writing a foreign document into the live stores: an operation lock, a
  well-formedness guard, `markMissingHosts`, `reattachByName`, per-tab `remapLayoutSessions`, a
  `-prev` backup, `replaceTabSnapshot`, `syncSessionStore`. Profile apply reuses its **pure helpers and the
  lock**; its commit step (`replaceTabSnapshot`) replaces the whole world and cannot take a single
  `tabs.<ws>` section, so the applier is new per-section functions (§4.7). *(corrected in P2a)*
- **`clientId` currently comes from the module being deleted.** `uploader.ts:117` reads
  `useSyncStore.getState().getClientId()`. Identity must move before Sync can go (§6, P4a).
- **The SPA does not know its own version.** `__APP_VERSION__` is defined only in
  `electron.vite.config.ts:56` for the Electron bundles; `spa/vite.config.ts` has no `define`.
  `resolveAppVersion()` (`uploader.ts:24`) goes through IPC and **returns `''` in a plain browser**.
  → *This kills "compare app versions to decide who is newer" (§4.5).*

### 3.3 Store inventory (what a profile is made of)

Persisted stores, their keys, and where each field lands. `L` = device-local (never synced).

| Store | key | Into section | Device-local part |
|---|---|---|---|
| `features/workspace/store.ts` | `purdex-workspaces` | `workspaces` (id, name, icon, iconWeight, moduleConfig) and, per workspace, its tab order. **The store holds a `Workspace[]`; order is array order** — the `{order, Record}` of §4.2 is a conversion | `activeWorkspaceId`, each `Workspace.activeTabId` |
| `stores/useTabStore.ts` | `purdex-tabs` (v3) | `tabs.<wsId>` (the `Tab` objects) | `activeTabId`; `visitHistory` (memory only, never persisted); `tabOrder` (derived, §4.3) |
| `stores/useHostStore.ts` | `purdex-hosts` | `hosts` (`hosts`, `hostOrder`; every `HostConfig` field — `id name ip port token order color colors icon iconWeight`) | `activeHostId`, **`devHostId`**, `runtime` |
| `stores/useLayoutStore.ts` | `purdex-layout` | `settings` — **only `tabPosition`** | regions (views, widths, mode, activeViewId), `activityBarWidth`, `activityBarWideSize`, `workspaceExpanded` |
| `stores/useUISettingsStore.ts` | `purdex-ui-settings` (v4) | `settings` | `terminalSettingsVersion` (a reconnect bump counter, not a preference) |
| `stores/useEditorSettingsStore.ts` | `purdex-editor-settings` | **not synced** *(P2b-2, §9.9 — pending the user's confirmation)* | the whole store: its own source says editor preferences are "a device-local choice (the small-screen laptop may want fontSize 11 while the big monitor uses 14) rather than shared config" |
| `stores/useThemeStore.ts` | `purdex-themes` | `settings` | — |
| `stores/useI18nStore.ts` | `purdex-i18n` | `settings` | — |
| `stores/useNotificationSettingsStore.ts` | `purdex-notification-settings` | `settings` | — |
| `stores/useModuleEnabledStore.ts` | `purdex-module-enabled` | **not synced** *(P2b, §9.7 — pending the user's confirmation)* | the whole store: its own source says module on/off is a per-device preference ("a host with limited resources can turn off modules it doesn't want to run") and it is deliberately not `syncManager`-registered |
| `stores/useWorkspaceSettingsStore.ts` | `purdex-workspace-settings` | `settings` | — |
| `stores/useHostSettingsStore.ts` | `purdex-host-settings` | `settings` | — |
| `stores/useNewTabLayoutStore.ts` | `purdex-newtab-layout` | `settings` — `profiles` only | `activeEditingProfile` (editor UI state), `knownIds` (derived) (see §7, naming) |

Not in a profile at all: `useSessionStore`, `useHistoryStore`, `useBrowserHistoryStore`,
`useRecentFilesStore`, `usePlaceholderFilesStore`, `useHeadlessLauncherMemoryStore`,
`usePathCacheStore` — runtime caches and per-machine history.

Window geometry is **not** in any SPA store today (Electron owns it), so the "geometry is local"
rule costs nothing to honour.

### 3.4 Tab and workspace shapes

- `Tab` (`spa/src/types/tab.ts:5`) = `{id, pinned, locked, createdAt, layout: PaneLayout}`. It has
  no title, no cwd, no workspace id.
- `Workspace` (`:156`) = `{id, name, icon?, iconWeight?, tabs: string[], activeTabId, moduleConfig?}`
  — **workspace membership and per-workspace tab order live here.**
- `PaneLayout` is a recursive tree: `{type:'split', id, direction, children[], sizes: number[]}` or
  `{type:'leaf', pane}`. **Split ratios are `sizes` on each split node** — interleaved with the
  structure, which is why applying a section has to preserve them (§4.7).
- Rebuild provenance is on the pane content, not the tab: `PaneRebuildRecord` (`tab.ts:45`) =
  `{sessionName, tmuxInstance, cwd?, cwdSource?, agent?, resumeCommandOverride?, unverified?,
  capturedAt}`. It is part of tab structure and therefore part of a profile — **this is what makes
  rebuild work identically for a master and a slave** (decision 13).
- **Two orderings exist today.** `useTabStore.tabOrder` is a global order; `Workspace.tabs` is the
  per-workspace order, and the comment at `useTabStore.ts:441-450` says the TabBar renders from
  `workspace.tabs` and that a mismatch "silently regresses the clustering UX". Sectioning by
  workspace makes `workspace.tabs` the only order that is stored; `tabOrder` becomes derived.

### 3.5 Transport

- `HostEvent` (`internal/core/events.go:14`) is exactly three strings: `{type, session, value}`.
  Richer producers already marshal JSON into `value` — `agent/handler.go:684`,
  `backup/handler.go:129` (`Broadcast("", "backup:done", string(payload))`). Profile events follow
  that pattern; **no transport change is needed.**
- `/ws/host-events` is registered at `core.go:249`; subscribers get a 64-deep buffer and are
  dropped when it fills. **A dropped event must therefore never be the only path to correctness**
  (§4.6 reconcile-on-connect).
- The SPA's `HostEvent` type (`spa/src/lib/host-events.ts:3`) has a **closed union** of event
  types; `'profile'` must be added there and dispatched in `useMultiHostEventWs.ts` following the
  `lib/<feature>/<feature>-ws-dispatch.ts` pattern (`backup-ws-dispatch.ts`, `agent-ws/index.ts`).
- Authed access: `hostFetch(hostId, path, init)` (`spa/src/lib/host-api.ts:193`).

### 3.6 Daemon conventions

- No shared storage layer: each module owns `OpenStore()` on its own sqlite file under `DataDir`,
  WAL pragma, `":memory:"` special-cased for tests, injectable `now func() int64`
  (`devicestate/store.go:37`).
- **Migrations are `CREATE TABLE IF NOT EXISTS` in `migrate()`; there is no schema-version table
  anywhere in the daemon.**
- `internal/module/hostconfig/` (module 55 / store 151 / handler 149 / validate 193 lines) is the
  smallest recent template; modules are registered in `cmd/pdx/main.go` (`:283` for devicestate).
- Existing upsert semantics in devicestate are **timestamp-based** (`store.go:76`, newer
  `captured_at` wins, equal overwrites). That is exactly the silent-overwrite behaviour this spec
  replaces with revision-based compare-and-set.

### 3.7 What has to be removed, and how big it is

| Thing | Files / lines |
|---|---|
| Sync module, SPA `lib/sync/` | 61 files ≈ 8,984 lines incl. tests (`file-provider` 182, engine 199, three-way-merge 193, use-sync-store 285, 6 contributors) |
| Sync UI | `SyncSection.tsx` 426 (+498 test), `features/settings/sections/sync-history/` (7 files) |
| Sync daemon | `internal/module/sync/` handler 267 / store 455 / module 58; `sync.db` 4 tables; 7 routes |
| device-state SPA | `lib/device-state/` (payload, restore, merge, uploader, api, identity, device-name), `useDeviceStateStore`, `components/settings/device-state/` |
| device-state daemon | `internal/module/devicestate/` 52+133+157+118; `device_state.db`; 4 routes |
| workspace snapshot | `lib/snapshot/` capture 167 / restore 547 / storage 102 / filter 22 / types 38 (+ ~2,000 test lines); keys `purdex-workspace-snapshot`, `-prev` |
| Host › Snapshots | `SnapshotsSection.tsx` 181 + blocks (shared 277, RebuildRecordsBlock 148, ClientSnapshotBlock 131, TmuxBlock 89, TabsBlock 59) — **only the rebuild-records block survives** |

Coupling to untangle: `ClientSnapshotBlock.tsx:128` renders `<DeviceStateSection/>`, and
`lib/device-state/*` imports `snapshot/types`, `snapshot/storage`, `snapshot/restore`,
`snapshot/capture`. The parts worth keeping (`structuralKey`, `hashPayload`, the restore pipeline)
move to `lib/profile/` **before** the teardown phases delete their old homes.

## 4. Design

### 4.1 Concepts

Added to the PRODUCT.md §3 vocabulary as **§3.9 Profile** (a doc task in P3; §7 of that file
requires an explicit PR listing IA impact).

- **Profile** — one complete Purdex state: `hosts`, `settings`, `workspaces`, and each workspace's
  tabs. It is the unit of synchronisation and of switching.
- **SOT** — the copy on the dev host daemon. A daemon may hold many profiles.
- **pre-SOT** — the client's own copy of a profile, assembled from the live stores by the collector.
  It is the only thing that talks to the daemon; stores never do.
- **master** — the one profile per client that is synced with the SOT. It is the client's only
  source of `hosts` and `settings`. **It stays synced whether or not it is the active profile**
  (decision 9).
- **slave** — a local-only profile holding `workspaces` + tabs only. It borrows the master's hosts
  and settings. Slaves never reach the daemon (decision 9).
- **active** — a device-local pointer saying which profile's workspaces/tabs are on screen. Switched
  by the top-left button. Switching active never starts or stops syncing.

```
  client (a19)                                  dev host daemon
  ┌────────────────────────────────┐            ┌──────────────────────┐
  │ stores  ──collector──▶ pre-SOT │◀──CAS ────▶│ profile "default"    │
  │         ◀──applier───          │  + ws event│   hosts      rev 12  │
  │                                │            │   settings   rev 41  │
  │ master   = default             │            │   workspaces rev  9  │
  │ slaves   = [a19-scratch]       │            │   tabs.<ws>  rev …   │
  │ active   = a19-scratch  (local)│            │ profile "experiment" │
  └────────────────────────────────┘            └──────────────────────┘
```

### 4.2 The profile document

Four kinds of section. Each is stored, revisioned, hashed and locked **independently**.

| Section key | Payload |
|---|---|
| `hosts` | `{hosts: Record<hostId, HostConfig>, hostOrder: string[]}` |
| `settings` | `{<storeKey>: <partialized state>}` for the stores marked `settings` in §3.3 |
| `workspaces` | `{order: string[], workspaces: Record<wsId, {name, icon?, iconWeight?, moduleConfig?}>}` |
| `tabs.<workspaceId>` | `{order: string[], tabs: Record<tabId, Tab>}` — one section per workspace |

Per-workspace tab sections are the point of decision 3: two machines working in different
workspaces never touch the same section, so they never conflict.

**Projections.** Each section declares, in code, the exact list of field paths the collector copies:

```ts
// lib/profile/projections.ts
export const PROJECTIONS = {
  hosts:      ['hosts.*.id', 'hosts.*.name', 'hosts.*.ip', 'hosts.*.port', 'hosts.*.token', 'hostOrder',
               'hosts.*.order', 'hosts.*.color', 'hosts.*.colors', 'hosts.*.icon', 'hosts.*.iconWeight'],
  workspaces: ['order', 'workspaces.*.name', 'workspaces.*.icon', 'workspaces.*.iconWeight', 'workspaces.*.moduleConfig'],
  tabs:       ['order', 'tabs.*.id', 'tabs.*.pinned', 'tabs.*.locked', 'tabs.*.createdAt', 'tabs.*.layout',
               '!tabs.*.layout..sizes'],       // exclusion: split ratios at any depth (decision 8)
  settings:   [/* '<storeKey>.<field>' — every synced field listed one by one, per store; no
                  whole-store entries. Three stores have no `partialize`, and three persisted
                  fields are not preferences (`terminalSettingsVersion`, `activeEditingProfile`,
                  `knownIds`); `purdex-module-enabled` is not listed at all (§9.7); the full list is
                  in the P2a plan and in projections.ts */
               'purdex-layout.tabPosition'],   // ← the only field taken from useLayoutStore
} as const
```

The projection is both the collector's instruction and the section's shape declaration (§4.5). A
field is synced if and only if it is listed. Anything not listed is device-local by construction —
there is no second allowlist to keep in step.

**Device-local, never in any section** (decision 8): `activeWorkspaceId`, `activeTabId`,
`visitHistory`, `activeHostId`, `devHostId`, the whole of `useLayoutStore` except `tabPosition`,
**every `sizes` array on every split node**, window geometry, and the profile bookkeeping itself
(`clientId`, `masterProfileId`, `activeProfileId`, `autoSync`).

Note the asymmetry the user asked for explicitly: a split's **structure** (that the tab is split,
in which direction, into what) is in `tabs.<ws>` via `layout`; its **ratios** are not. §4.7 says
how `sizes` survives an apply.

### 4.3 Standalone tabs are removed

`handleSelectHome` (`App.tsx:180`) focuses the first tab belonging to no workspace. Both machines
have zero such tabs (§3.1), PRODUCT.md §3.2 already defines a Tab as "Workspace 內的工作單位", and
the button that reaches them becomes the profile switcher. Therefore: **every tab belongs to exactly
one workspace.** If a client holds standalone tabs when it first builds a profile, they are moved
into a workspace named `未分類` / `Unsorted` (created only if needed) and the user is told. The
standalone branches in `useWorkspaceStore.getScopeTabs` (`store.ts:196-215`) and `handleSelectHome`
are deleted in P3.

Consequence taken deliberately: `useTabStore.tabOrder` stops being authoritative and is rebuilt
from the workspaces' orders on apply, retiring the dual-ordering hazard noted at
`useTabStore.ts:441-450`.

### 4.4 Section state, and the pre-SOT state machine

Per section: `synced` | `pending` | `locked:conflict`.
Per profile: `idle` (no master) | `locked:schema` | worst-of-its-sections.

```
        ┌── local change ──▶ pending ──── flush ok ───▶ synced
        │                      │  ▲                        │
  synced┤                      │  └── offline / autoSync=off
        │                      ▼
        │                 409 conflict ──▶ locked:conflict ──user picks──▶ synced
        └── shape mismatch on connect ──▶ locked:schema (whole profile) ──upgrade──▶ synced
```

- **`pending`** is the normal offline state (decision 1: the app does not go read-only). Local edits
  keep landing in pre-SOT; the collector keeps hashing. When the dev host returns, each dirty
  section takes the decision of §4.6.1. A section whose hash equals the SOT's is **not written**
  (decision 4, middle branch) — this is the "驗算，沒改變就不寫回" the user asked for.
- **`synced` means clean, not "recently talked to the daemon"**: `currentHash == baseHash`. The
  distinction is what §4.6.1 rests on.
- **`locked:conflict` is per section** and means exactly what the user said: that section stops
  writing out *and* refuses to apply inbound. Other sections keep syncing.
- **`locked:schema` is the whole profile** — an old client must not write anything once any shape
  has moved.

### 4.5 Shape: fingerprint detects, ordinal decides direction

Two values per section, both produced by the client:

- **`fingerprint`** = SHA-256 of the section's projection list (§4.2), sorted. It changes exactly
  when the set of synced fields changes — and the projection is the thing a developer *must* edit to
  change that set, so it cannot be forgotten the way a version bump can.
- **`ordinal`** = `SECTION_SCHEMA_ORDINAL[kind]`, a hand-maintained integer in the SPA. Bumped when
  the projection changes, and **also** when only a value domain changes (a new enum member, a
  re-interpreted field) — the case a fingerprint cannot see.

*Refinement of decision 6, forced by §3.2:* the original sketch used the app version to decide which
side is newer. The SPA has no version in a browser, so the **ordinal is the sole direction signal**.

On connect, per section, the client compares its `(fingerprint, ordinal)` with the SOT's:

| Comparison | Outcome |
|---|---|
| fingerprint equal | normal operation (ordinals may differ; the higher one is written on the next flush, and **the stored ordinal never decreases** — the daemon keeps `max(stored, incoming)`) |
| fingerprint differs, `mine.ordinal > sot.ordinal` | I am newer: I may write, and my write replaces the stored shape |
| fingerprint differs, `mine.ordinal < sot.ordinal` | `locked:schema` — panel says the SOT was written by a newer Purdex, upgrade this client |
| fingerprint differs, ordinals equal | `locked:schema` — a shape changed without an ordinal bump. Fail closed; this is a developer error and the panel says so |

A test asserts that every projection change in the repo is accompanied by an ordinal bump, by
snapshotting `{kind: [fingerprint, ordinal]}` — the snapshot's failure message tells the developer
to bump.

### 4.6 The protocol

One write verb, compare-and-set on a section:

```
PUT /api/profiles/{profileId}/sections/{section}
  { clientId, baseRev, hash, fingerprint, ordinal, payload }

  SOT.rev == baseRev                     → 200 {rev: SOT.rev+1, applied: true}   store, broadcast
  SOT.rev != baseRev && SOT.hash == hash → 200 {rev: SOT.rev,  applied: false}   converged, no write
  SOT.rev != baseRev && SOT.hash != hash → 409 {reason:'conflict', rev, hash, payload}
  shape mismatch (§4.5)                  → 409 {reason:'schema', fingerprint, ordinal}
```

The daemon is deliberately dumb: it compares, stores, broadcasts. It never merges, never inspects a
payload beyond validating envelope and size, and never decides who is newer.

**Notification.** `Broadcast("", "profile", json)` where the value carries
`{profileId, section, rev, hash, writerClientId}` — the established string-JSON-in-`value` pattern
(§3.5). A client applies an inbound event only when `profileId` is its master and `writerClientId`
is not its own, and it fetches the section rather than trusting a payload off the wire.

**Reconcile on connect, because events can be dropped** (§3.5, 64-deep buffer): on every
(re)connection to the dev host, the client `GET`s the profile's section index
(`{section, rev, hash, fingerprint, ordinal}[]`) and runs the decision of §4.6.1 for each section.
This is also what closes the offline window; the event stream is an optimisation on top of it.

#### 4.6.1 The base, and what "stale" means

A client cannot tell "I am merely behind" from "we both moved" by comparing the current hash with
the SOT's — it needs to remember **what it last agreed with**. Each section therefore persists, on
the client, next to its payload:

- **`baseRev`** — the SOT revision this client last agreed with (last successful push, or last pull)
- **`baseHash`** — the section's hash at that revision

`dirty` ⟺ `currentHash != baseHash`. The decision is then fast-forward logic:

| local | SOT | Action |
|---|---|---|
| clean | `rev == baseRev` | nothing |
| clean | `rev > baseRev` | **pull** — fetch and apply; set base to the new rev/hash. No user involved |
| dirty | `rev == baseRev` | **push** — CAS with `baseRev`; on 200 set base to the returned rev and the pushed hash |
| dirty | `rev > baseRev` | **conflict** — do not push, do not apply; enter `locked:conflict` |
| either | `rev < baseRev` | **`locked:schema`-style stop**: a revision cannot go backwards, so the SOT profile was deleted and recreated. The panel says so and offers push or pull as a fresh start |
| either | section absent on SOT | §4.6.3 |

*Restated in P2a (§9.4), without changing any row's meaning:* each side is a pair `{rev, hash}` and
**"absent" is `hash === null`** — on the SOT that covers both "never created" and a tombstone, which
the client cannot and need not tell apart. "The SOT moved" is then `sot.hash ≠ base.hash ∨ (sot.hash
≠ null ∧ sot.rev > base.rev)` — **an absent side has no revision to compare** (§9.6 C-1: a section
created and deleted again elsewhere is just as absent as before, whatever revisions went by), which is what lets a client that has just deleted a section (base `{6, null}`) read an
index that no longer lists it as *agreement* rather than as a deletion to pull. Creating over an
absent SOT always sends `baseRev 0`, because that is the only create the daemon accepts, over nothing
or over a tombstone.

The CAS of §4.6 is the enforcement of this table across the race window; the table is what stops a
client from ever *starting* a write it knows is stale.

*Consequence for `pending`:* a `pending` section is simply a dirty one that has not been pushed yet
because the host is unreachable or auto-sync is off. Nothing about the decision changes when it
becomes reachable again — a dirty section whose SOT did not move flushes; one whose SOT moved
conflicts.

#### 4.6.2 Apply and flush are mutually exclusive

Per section, one lock covers both directions. Two rules follow, and both exist to keep the promise
that nothing is overwritten silently:

1. **An inbound event is applied only to a clean section.** If the section is dirty, the event is
   not applied; it is recorded as "the SOT moved" and the section takes the dirty rows of §4.6.1 —
   push if its rev still matches (it will not), otherwise conflict.
2. **An event arriving while a push is in flight is deferred**, not applied. It sets a flag; when
   the push resolves (200 or 409) the section re-runs §4.6.1. Applying it eagerly would overwrite
   the very payload the impending 409 is about to ask the user to choose between.

3. **Knowledge of the SOT never goes backwards by accident.** Every change to a section's base,
   SOT view or flight — and every reconnect — bumps an *index epoch*; an index response carries the
   one it was requested at and is discarded (and re-requested) if the section has moved on since.
   **A local edit does not bump it** (PR review, §9.6): freshness of the index is a fact about the
   SOT, and a user who keeps typing must not be able to starve the section of an index forever. Only an epoch-matching index
   may lower the known revision — which is exactly the "profile was recreated" signal.
4. **A decision is only as good as the state it was made in.** A push carries a token
   `{hash, baseRev, epoch}`; the state machine refuses to open a flight for a stale token, and the
   transport sends only a flight that was opened. A decision overtaken by an event dies at that
   boundary instead of reaching the wire.

When a push returns 409, the client keeps **the payload it sent** as the "local" side of the
conflict. The live stores may have moved on since; the user is choosing between two known
snapshots, not between the SOT and a moving target. The state machine holds hashes only; it names
the payloads that must be retained (the one in flight, both sides of an open conflict) and the
transport keeps exactly those. A locked section still *learns* of newer SOT revisions — it refuses
to apply them, not to know about them — so `Keep local` is written against the newest one.
**An edit made after the user chose `Keep local`, but before the sent snapshot has been put back,
cancels the restore** (§9.6): what he typed after choosing wins over the snapshot he chose.

#### 4.6.3 Sections are created and deleted

`tabs.<wsId>` sections come and go with workspaces, so the section set is itself state.

- `DELETE /api/profiles/{id}/sections/{section}?baseRev=N&clientId=…` — CAS on `baseRev`, same
  conflict semantics. **The daemon keeps a tombstone** (`deleted = 1`, `rev + 1`, payload dropped)
  rather than removing the row, so a section's revisions are strictly increasing for the life of
  the profile: a recreated section continues from the tombstone's rev instead of restarting at 1,
  which is what stops a retried stale `DELETE` from destroying the recreated content (ABA).
  Tombstones read as "absent" on every GET. A tombstone **keeps its fingerprint and ordinal**, and a
  recreate passes the same §4.5 gate as any write — deleting a section never lowers the stored
  ordinal or lets an older shape back in.
- **`workspaces` is the authority on which `tabs.*` sections should exist.** A client that has
  applied `workspaces` deletes the `tabs.*` sections for workspaces that are gone, and creates them
  for workspaces it gained.
- **Cross-section writes are not atomic and must not pretend to be.** Creating a workspace is two
  writes (`workspaces`, then `tabs.<new>`); a reader may observe either alone. Both partial states
  are defined and harmless:
  - a workspace with no `tabs` section yet → renders as an empty workspace;
  - a `tabs.<wsId>` section whose workspace is not in `workspaces` → **kept, not deleted**, and not
    rendered. It is either an arrival that overtook its workspace, or a workspace deleted elsewhere;
    the next `workspaces` apply resolves which.
- *Who does what (settled in P2a, §9.5).* The two rules above are reconciled by what the client
  **knew**: a `tabs.<id>` whose workspace this client had and the `workspaces` apply just removed is
  deleted locally; one whose workspace it never saw is kept unrendered. Everything else is ordinary
  section sync, not a special path: deleting a workspace locally makes its `tabs.<id>` vanish from
  the local document → the section's state machine sees `hash: null` and issues the CAS `DELETE`;
  other clients then see an absent SOT over a clean local copy and pull the deletion — which is
  also how a kept-unrendered section is eventually collected; a removal that meets a moved SOT is a
  conflict like any other; and two clients creating the same empty `tabs.<id>` converge, because
  both send the same `{order: [], tabs: {}}`.
- A client never deletes a section merely because it does not recognise it (forward compatibility
  with a newer client's section kinds; unknown kinds are carried, never rewritten).

**Latency budget** for goal 3: collector debounce 500 ms trailing (down from the uploader's 5 s,
which was tuned for a whole-state upload) + one `PUT` + one broadcast + one `GET` of the changed
section. The 1 s target is a budget on this path, and it is what §7 measures.

### 4.7 Collector and applier

**Collector** (`lib/profile/collector.ts`, from `device-state/uploader.ts`): subscribes to the
stores in §3.3; on change, rebuilds only the affected sections through their projections; computes
`structuralKey` → `hashPayload`; drops the section if the hash is unchanged; otherwise queues a
flush. Debounce 500 ms trailing, per section. Refuses to run for a section in `locked:*`.

**Applier** (`lib/profile/applier.ts`, reusing `device-state/restore.ts`'s pipeline): takes the
operation lock, validates well-formedness, then per section:

- `hosts` — replace `hosts` + `hostOrder`; keep local `activeHostId` and `devHostId`. A host present
  locally but absent in the incoming payload is removed; `markMissingHosts` semantics are preserved
  for tabs pointing at it.
- `settings` — replace each store's persisted slice; `tabPosition` only, out of `useLayoutStore`.
- `workspaces` — replace the list and order; keep local `activeWorkspaceId`, falling back to the
  first workspace if it vanished.
- `tabs.<ws>` — replace that workspace's tabs and order, then **restore split ratios**: walk the
  incoming `layout` tree and, for each split node id present in the local tree, copy the local
  `sizes`; for a new split node, distribute evenly. Then `remapLayoutSessions` and `syncSessionStore`
  exactly as the device-state restore does today. Keep local `activeTabId` if it still exists.

Both directions are pure functions over `(local, incoming)` plus one commit step, so the whole of
§4.7 is testable without a daemon.

*Measured in P2a:* split node ids are `generateId()` and travel inside `layout`, so both sides share
them after one apply — that is what makes the id match work; `sizes` are percentages summing to 100
by convention, and a new split is distributed evenly. `useLayoutStore.healLayoutInvariant` widens
the device-local `activityBarWidth` when `tabPosition` becomes `'left'`; that is a layout invariant
of the store, not a sync leak, and is accepted. Applying `settings` yields per-store **patches** of
the listed fields that differ, nothing else. Applying `tabs.<A>` removes an arriving tab from any
other workspace still listing it (one workspace per tab, §4.3), so it can dirty `tabs.<B>`; that is
correct and converges when B's own section lands. A payload is checked for well-formedness first —
`order` must equal the record's key set, no unknown keys, no `sizes` anywhere — and a payload that
fails is not applied at all: never a partial apply.

### 4.8 Daemon: `internal/module/profiles`

Modelled on `internal/module/hostconfig` (§3.6). `profiles.db`:

```sql
CREATE TABLE IF NOT EXISTS profiles (
  id          TEXT PRIMARY KEY,
  name        TEXT NOT NULL,
  created_at  INTEGER NOT NULL,
  updated_at  INTEGER NOT NULL);

CREATE TABLE IF NOT EXISTS profile_sections (
  profile_id  TEXT NOT NULL,
  section     TEXT NOT NULL,
  rev         INTEGER NOT NULL,
  hash        TEXT NOT NULL,
  fingerprint TEXT NOT NULL,
  ordinal     INTEGER NOT NULL,
  payload     TEXT NOT NULL,
  writer      TEXT NOT NULL,
  updated_at  INTEGER NOT NULL,
  deleted     INTEGER NOT NULL DEFAULT 0,   -- tombstone, §4.6.3
  PRIMARY KEY (profile_id, section));

CREATE TABLE IF NOT EXISTS profile_attachments (
  client_id   TEXT PRIMARY KEY,
  profile_id  TEXT NOT NULL,
  device_name TEXT NOT NULL,
  attached_at INTEGER NOT NULL,
  last_seen   INTEGER NOT NULL);
```

Routes (all through the module's `RegisterRoutes`):

| Route | Purpose |
|---|---|
| `GET /api/profiles` | list: profiles + per-section index + attachments |
| `POST /api/profiles` | create `{name}` → `{id}` (decision 15: random id, names may repeat) |
| `PATCH /api/profiles/{id}` | rename |
| `DELETE /api/profiles/{id}` | **409 while any attachment exists** (decision 16) |
| `GET /api/profiles/{id}` | every section's payload (used by pull) |
| `GET /api/profiles/{id}/sections/{section}` | one section |
| `PUT /api/profiles/{id}/sections/{section}` | the CAS of §4.6 |
| `DELETE /api/profiles/{id}/sections/{section}` | CAS-guarded section removal (§4.6.3) |
| `PUT /api/profiles/{id}/attachment` | `{clientId, deviceName}` — this client's master is now this profile |
| `DELETE /api/profiles/{id}/attachment?clientId=…` | detach (stop sync / switch master); only when the attachment belongs to `{id}` |

Validation mirrors `devicestate/validate.go`: `clientId` `^c_[0-9a-f]{12}$`, name 1–64 runes,
section key `^(hosts|settings|workspaces|tabs\.[A-Za-z0-9_-]{1,64})$`, payload ≤ 5 MB → 413,
`payload` must parse as a JSON object.

### 4.9 UI

**Top-left button → profile switcher** (`ActivityBarNarrow.tsx:154`, `HomeRow.tsx`,
`App.tsx:180`). Click opens a menu: the master (badged, with sync state), then slaves, then
`Settings › Profile`. Picking one sets the active pointer and swaps workspaces/tabs; it does not
touch syncing.

**`Settings › Profile`** — a new section replacing `Settings › Sync`:

1. *Current* — master name, SOT host, per-section state and revision, last sync time,
   **auto-sync toggle** and a **Sync now** button (decision 11; both device-local, never synced).
2. *Wizard* — the master switch, one confirmation per step (decision 10):
   `Stop sync` → `Pick SOT profile (existing | new)` → `Pick which local profile becomes master` →
   `Direction: push (local overwrites SOT) / pull (SOT overwrites local)` → `Start sync`.
   A newly created SOT profile offers push only. A pull onto a client that has local state first
   saves that state as a slave, named after the host, **with its own confirmation** (decision 12).
3. *Profiles* — the local list; `Copy master as slave` (decision 10: the only copy operation);
   rename; delete a slave; delete a SOT profile (disabled while attached, §4.8).
4. *Resolve* — shown when any section is `locked:*`. One row per locked section with
   `Keep local` / `Take SOT`, a summary of what differs (counts, not a diff view), and for
   `locked:schema` the explanation and no choice but upgrading.

Law 4 (progressive complexity) is honoured by the default path: a user who never opens
`Settings › Profile` has no master, syncs nothing, and sees exactly today's app.

### 4.10 What does not change

- tmux, sessions, `session_meta`, rebuild and resume templates: untouched. Rebuild works the same
  for a master and a slave because its inputs are part of tab structure (§3.4, decision 13).
- The host-events transport, ticket auth, and `hostFetch`.
- `lib/storage/sync.ts` — the cross-window `syncManager` bridge shares a name with the Sync module
  but is an unrelated concern and stays.

## 5. Phases

Sized against the project rule (≤ 800 diff lines **or** ≤ 20 files per PR).

| Phase | Content | Codex |
|---|---|---|
| **P1** | `internal/module/profiles` (store + handler + validate + module), registration in `main.go`, the `profile` broadcast, Go tests for CAS / converged / conflict / schema / attachment-blocks-delete | R1 + R2 |
| **P2a** | `lib/profile/` pure core: projections, section builders, `structuralKey`/`hashPayload` moved in, fingerprint + ordinal, the state machine as a reducer, the applier's pure `(local, incoming) → next` functions incl. `sizes` preservation. No transport, no UI | R1 + R2 |
| **P2b** | Transport and wiring: CAS client, `profile-ws-dispatch.ts`, `'profile'` added to the `HostEvent` union and `useMultiHostEventWs`, collector subscriptions, reconcile-on-connect, `useProfileStore` (device-local: clientId, master, active, autoSync) | R1 + R2 |
| **P3** | UI: switcher, `Settings › Profile` (4 blocks), i18n en + zh-TW, standalone-tab removal (§4.3), PRODUCT.md §3.9 | R1 + R2 |
| **P4a** | Remove the Sync module (SPA lib + UI + history pages + daemon package + routes). Identity already moved in P2b | none (deletion) |
| **P4b** | Remove device-state (SPA + daemon + table) and workspace snapshot; trim Host › Snapshots to the rebuild-records block | none (deletion) |

P1 and P2a are independent and may run in parallel. P4a/P4b must follow P2b (identity) and P3
(the UI that replaces them).

## 6. Acceptance (real machine — must be run, not assumed)

On mlab (worktree dev server :5175) and air-2026, with mlab as dev host:

1. **First profile.** a26: wizard → new SOT profile `default` → push. `GET /api/profiles` shows four
   section kinds (`hosts`, `settings`, `workspaces`, `tabs.<ws>` × 7) each at rev 1.
2. **Second client pulls.** a19: wizard → existing `default` → pull. Its previous state is saved as a
   slave named after the host (confirmed in a dialog). After the pull, a19's workspaces, tabs, hosts
   and settings equal a26's.
3. **Sub-second propagation.** Rename a workspace on a26; a19 reflects it. Measure the wall time
   from mutation to applied state; record the number.
4. **Converged write does nothing.** Make the same edit on both sides; assert the second flush
   returns `applied:false` and `rev` does not advance.
5. **Conflict locks one section only.** Stop the mlab daemon; edit workspace A's tabs on a26 and the
   same workspace's tabs on a19; restart. The first flush wins; the second gets 409 and that section
   enters `locked:conflict` — while a different workspace's tabs and `settings` keep syncing.
   Resolve with `Keep local` on one side and confirm both converge.
6. **Offline is not read-only.** With the daemon down, open tabs, change settings, switch workspaces
   on both machines; nothing blocks; state shows `pending`; on restart everything flushes.
6a. **Being merely behind is not a conflict** (§4.6.1, clean + SOT ahead). Close a19 entirely; make
    ten edits on a26; reopen a19. Every section fast-forwards with no prompt, and no section enters
    `locked:conflict`.
6b. **A dirty section refuses an inbound apply** (§4.6.2 rule 1). With auto-sync off on a19, edit
    workspace A's tabs there; edit the same workspace on a26 and let it push. a19 must not silently
    adopt a26's version: the section stays dirty and, on `Sync now`, goes to `locked:conflict` with
    a19's own edit offered as the local side.
6c. **Section lifecycle** (§4.6.3). Create a workspace on a26 → a19 gains the workspace and its
    tabs section. Delete it on a19 → a26 loses both, and the daemon has no orphaned `tabs.<wsId>`
    row. During the create, verify that observing `workspaces` before `tabs.<new>` renders an empty
    workspace rather than an error.
7. **Schema lock.** Run a client with a lowered `SECTION_SCHEMA_ORDINAL.settings` against a SOT
   written by the current one; assert the whole profile goes `locked:schema`, writes stop, and the
   panel names the section.
8. **Focus is not mirrored.** Switch tabs and workspaces on a26; a19's active tab, active workspace,
   sidebar widths and split ratios do not move.
9. **Split ratios survive.** Split a tab on a26, drag the divider to an uneven ratio; on a19 the same
   tab is split the same way with a19's own ratio; neither ratio follows the other.
10. **Slave.** `Copy master as slave` on a19, switch active to it, edit it; assert nothing reaches the
    daemon, and that the master keeps syncing while the slave is active (decision 9).
11. **Rebuild on a slave.** Rebuild a tmux session from a tab inside a slave profile; it works
    exactly as from the master (decision 13).
12. **Delete is refused while attached.** `DELETE /api/profiles/default` returns 409 while either
    machine has it as master; after both stop syncing, it succeeds.
13. **Tokens travelled.** a19, after the pull, connects to every host without re-entering a token
    (decision 7).

## 7. Risks

- **Whole-section writes get large.** A `settings` or `tabs.<ws>` section is rewritten in full on
  every change. Today's whole-state payloads are well inside the 5 MB cap, but the cap is enforced
  and the acceptance run records the real sizes. If one section dominates, it splits further — the
  protocol does not change.
- **Tokens now sit in a daemon database.** They are already in each client's `localStorage` in
  plaintext (§3.3) and the daemon file is in `~/.config/pdx/` on the user's own machine, so this is
  not a new exposure class — but it is a second copy, and `profiles.db` should be created 0600 like
  its siblings. Stated so the trade-off is on the record (decision 7).
- **The 1 s target is a budget, not a guarantee.** Broadcast subscribers are dropped when their
  64-deep buffer fills (§3.5); reconcile-on-connect is what makes that survivable, and step 3 of §6
  measures the happy path rather than asserting it.
- **A large conflict may not survive a restart — a known gap against §4.6.2, not a trade-off that
  meets it.** The client keeps the two snapshots of an open conflict in `localStorage` (~5–10 MB per
  origin, shared with every other key, no transactions). When they do not fit, the conflict lives in
  memory only; restart before resolving it and the snapshot that was sent is gone, so the user
  would choose against the live state instead. Section payloads are KB-sized today, which is why
  this ships; #1244 moves the store to IndexedDB. *(P2b-1, §9.8)*
- **`profile` is already a word in this codebase.** `useNewTabLayoutStore` persists `{profiles,
  knownIds, activeEditingProfile}` for the new-tab layout editor (308 lines, and already on the
  inventory page's "decide" list). With PRODUCT.md §3 being a strict vocabulary, that concept must
  be renamed or removed; P3 renames it to *new-tab layout preset* if it still exists.
- **Deleting ~9,000 lines of Sync** touches the module registry and the settings IA. P4a is a pure
  deletion, verified by declaration-level byte comparison of what remains
  (`feedback_pure_move_verification`), not by diffstat.

## 8. Open questions

None blocking. Two to revisit after the acceptance run:

1. Whether `tabs.<ws>` should split further (per tab) if §7's size measurement shows a single
   workspace dominating.
2. Whether the resolve panel should show a structural diff rather than counts — deferred until a
   real conflict has been resolved by hand at least once.

## 9. Review log

### 9.1 Plan review — codex `task-mu8vu5fo-t16rbh` (gpt-5.6-sol), 2026-09-20

Eight findings, all confidence ≥ 0.94, all accepted:

| # | Sev. | Finding | Resolution |
|---|---|---|---|
| 1 | critical | idempotent section DELETE + rev restart = ABA (delete → recreate at rev 1 → retried delete destroys it) | tombstones; revisions strictly increase (§4.6.3, §4.8) |
| 2 | important | equal fingerprint let an older client lower the stored ordinal | `max(stored, incoming)` (§4.5) |
| 3 | important | `DeleteProfile` ∥ `PutAttachment` not atomic → attachment to a deleted profile | profile existence folded into each write statement (plan Task 1) |
| 4 | important | attachment DELETE had no defined `clientId` source | query parameter, must match `{id}` (§4.8) |
| 5 | important | broadcast untested; wire key unspecified | injected broadcaster, tagged struct, per-outcome tests (plan Task 4) |
| 6 | important | `:memory:` tests cannot race | file-backed WAL concurrency tests (plan Task 2) |
| 7 | minor | 5 MiB applied to the body, spec says payload | payload cap + envelope allowance (plan Task 4) |
| 8 | minor | "nine routes" — there are ten | corrected |

Also found while applying these: the §4.6 `PUT` body had no source for `writer`; `clientId` added.

### 9.2 PR #1237 review — R1 + R2 attacker (gpt-5.6-sol), 2026-09-20

| # | Source | Sev. | Finding | Resolution |
|---|---|---|---|---|
| R1-1 | R1 | P2 | between `DeleteProfile`'s two statements a concurrent section UPDATE / recreate / tombstone still hit, so a write was acknowledged and broadcast, then swept (`sections.go`) | fixed `0b7354d0` |
| A-1 | attacker | high | same two statements: a crash or a failed sweep left `hosts` sections (tokens) orphaned forever — the retry is a 404 | fixed `0b7354d0` |

One root cause, one fix: `DeleteProfile` is a single transaction whose first statement is the
conditional delete (a write first, so no WAL snapshot upgrade), followed by the section sweep.
Failure rolls both back and is retryable; after commit every section write path returns
`ErrProfileNotFound`. Guarded by a fault-injection test that is red against the two-statement form.
The attacker reported **no** interleaving giving two `PutApplied`, a non-increasing rev, or a
bypassed schema gate.

**R2 critic** (incremental, `--base bc6ea04b`): agrees with R1-1 and A-1 and confirms `0b7354d0`
closes both (only `tx.Exec` inside the transaction; the classifying re-read runs after the rollback;
the first statement is a write). One evidenced spec drift:

| # | Sev. | Finding | Resolution |
|---|---|---|---|
| C-1 | medium | recreating over a tombstone overwrote the stored ordinal (7 → 1 by an old client), against §4.5 "never decreases" | tombstones keep their shape and pass the schema gate, before `baseRev`; recreate uses `MAX(ordinal, ?)` (§4.6.3) |

Stop condition met: R1 has no critical/P1, and the critic raised no evidenced objection to a
finding — its one addition is fixed.

### 9.3 P2a — spec corrected against measurement, 2026-09-20

Before the P2a plan was written the SPA side was measured file by file. Corrections applied above:
line references in §3.2–§3.4; `workspaces` is an array; `visitHistory` is never persisted;
**`HostConfig` has five persisted fields the `hosts` projection omitted** (`order` — required —
`color`, `colors`, `icon`, `iconWeight`; decision 7 says the section travels whole); the `settings`
projection lists fields per store because three stores have no `partialize` and three persisted
fields are not preferences; the applier cannot reuse `replaceTabSnapshot` (whole-world commit);
`structuralKey`/`hashPayload` are copied generically rather than moved; `useNewTabLayoutStore` is
308 lines, not 417.

### 9.4 P2a plan review — codex `task-mu8xn90i-xcdz0h` (gpt-5.6-sol), 2026-09-20

Seventeen findings (six critical), all accepted; the measured baseline was spot-checked and held.
The critical ones were all in the section state machine, before a line of it existed:

| # | Finding | Resolution |
|---|---|---|
| 1, 2, 10 | `sot: null` meant both "never existed" and "tombstone"; a delete event has no hash; after a successful delete the next index read as "deleted elsewhere, pull" and 404'd forever | both sides are `{rev, hash \| null}`; "moved" compares hash and rev; create-over-absent sends `baseRev 0` (§4.6.1) |
| 3 | a late index response rewound the known SOT rev → a push the client knew was stale | epochs (§4.6.2 rule 3) |
| 4 | race between deciding a push and starting it | flight tokens (§4.6.2 rule 4) |
| 5 | a conflict kept hashes only — the sent snapshot was unrecoverable | `retainedHashes`; the transport stashes by hash |
| 6 | `Take SOT` was inexpressible; `Keep local` targeted the rev at lock time and would 409 again | `forcePull`; a locked section keeps learning; keep-local rebases on the newest known rev and restores the sent snapshot |
| 7–9 | delete had no in-flight type; terminal events clearing the flight were untested; `push-converged` could set the base to a hash never sent | typed; one shared invariant test; base = the *sent* hash |
| 11 | store state types are not exported | structural types in the lib, pinned from the test files |
| 12 | standalone tabs dropped, against §4.3 | pure `adoptStandaloneTabs` |
| 13 | weak well-formedness guard broke the round trip | `order` must equal the record's key set |
| 14 | 3 ∥ 4 ∥ 5 was not independent | `1 → 2 → (3 ∥ 4) → (5 ∥ 6)` |
| 15, 16 | `..` depth-zero ambiguity; round trip must be stated on hashes | specified and tested |

### 9.5 P2a as built, 2026-09-20

Contradictions found while implementing, reported by the subagents rather than resolved silently,
and ruled on by the main session (full list in the P2a plan, "As built"):

- §4.6.1 decision order: **restore-local precedes reindex** — a fresh, restored or reconnected
  section is index-stale by design (nothing is decided before an index is seen; `reconnected` is
  reconcile-on-connect), so a local restore must not queue behind a network step or it lands late
  and overwrites newer edits.
- A 409 on a section that is clean by the time it arrives does not lock; a sent *delete* that met a
  409 is restorable to "absent"; a 409 never lowers the known SOT revision.
- §4.6.3's two rules about `tabs.*` of unknown workspaces were contradictory as written; reconciled
  by what the client previously knew (text added to §4.6.3).
- `settings` apply produces patches; a listed field absent from a present store means "cleared".
- The canonical-JSON idiom inherited from device-state was wrong for integer-like keys and
  `__proto__`; `lib/profile/hash.ts` does not share it.

### 9.6 PR #1239 review — R1 + R2 attacker (gpt-5.6-sol), 2026-09-20

| # | Source | Sev. | Finding | Resolution |
|---|---|---|---|---|
| A-1 / R1-1 | attacker, R1 | high / P1 | the `settings` guard admitted payloads no builder can produce — unlisted fields, unknown stores, wrong-typed values. With "a listed field absent from a present store means cleared", `{"purdex-ui-settings":{"terminalSettingsVersion":1}}` wiped every UI preference and could never converge | known stores may carry only `PROJECTIONS`-listed fields and at least one; unknown stores are refused (a new store changes the fingerprint, so the schema lock owns forward compatibility, not the guard); `applySettings` compares value shape against the local value and returns `{patches, rejected}` — any rejection means no patch at all |
| A-2 | attacker | high | an edit made between `Keep local` and the restore was overwritten by the restored snapshot | a `local-changed` cancels a pending restore; `local-restored` is accepted only for the pending hash; `canRestoreLocal` lets the driver check before it writes (§4.6.2) |
| A-3 | attacker | medium | continuous local edits invalidated every index response → the section never synced while the user typed | a separate index epoch that local edits do not move (§4.6.2 rule 3) |
| R1-2 | R1 | P2 | a late `409 {rev: 0}` replaced a newer live SOT learnt during the flight with "absent" | the absent answer is ignored when a live SOT was recorded mid-flight |
| A-4 | attacker | low | `applier.ts` mixes four appliers with their guards — the seam A-1 lived in | #1240 |

Found while fixing A-3, by the subagent, against the main session's instruction and with a
counter-example: a reconnect **must** invalidate index requests in flight (events were lost while
disconnected, so a pre-disconnect answer is stale by definition). `reconnected` always bumps the
index epoch.

**R2 critic** (incremental, `--base 52e641a8`): agrees with all five and confirms R1-1/A-1, A-2 and
A-3 closed — no new stale-push path after splitting the index epoch from the flight epoch; the
shape check cannot refuse a legitimate value (it opened the ten stores: no listed field is nullable
or spans shapes); refusing unknown settings stores does not contradict §4.6.3, which is about
section *kinds*. No spec drift of Important or above. One evidenced objection, to the *fix* of R1-2:

| # | Sev. | Finding | Resolution |
|---|---|---|---|
| C-1 | high | "a live SOT was learnt during the flight" does not prove it is newer than the 409 — events and HTTP responses travel on different channels with no causal order. Create (rev 5, event seen) → delete (rev 6, event lost) → my PUT meets the tombstone: the `409 {rev: 0}` is the *authoritative* one, and keeping `{5, H8}` presents deleted content as the SOT and makes `Keep local` push `baseRev 5` into a daemon that only accepts 0 | R1 and the critic describe the two directions of one ambiguity the client cannot resolve, so it does not guess: the flight closes, `sot` is left alone, **nothing locks**, the index is marked stale, and the authoritative index decides — absent → push `baseRev 0`; live and different → conflict; live and equal → synced. Two consequences adopted with it: **no convergence fold while the index is stale** (stale means "what I know of the SOT is not to be trusted", and declaring `synced` on it is wrong; the fold happens one step later, when the index lands), and **an absent side has no revision** in `sotMoved` (§4.6.1) |

### 9.7 P2b plan — measured, then reviewed (codex `task-mu90jwwz-h2c507`), 2026-09-20

**Measured before planning** (full list in the P2b plan): P1 has no per-profile index route — the
index is `GET /api/profiles`; a delete event carries `hash: ""`; `hostFetch` has no timeout; a bare
`setState(patch)` skips the invariants of eight of the ten settings stores (the i18n translator `t`
is a *state field*), while `persist.rehydrate()` — the path cross-window sync already uses — runs
them all; there is no leader election anywhere in the SPA; `clientId` has two callers that outlive
the Sync module.

**One P2a choice reversed, for the user to confirm:** `useModuleEnabledStore` says in its own source
that module on/off is device-local by intent ("a host with limited resources can turn off modules it
doesn't want to run"). It is removed from the `settings` projection (ordinal → 2). Putting it back
is one projection line and one more ordinal bump.

**Plan review: 17 findings, 5 critical, all accepted.** Web Locks cannot elect the leader (secure
contexts only — the Electron dev window is plain HTTP — and the draft's acquire could never return)
→ a `localStorage` lease with a heartbeat. A device-local, unsynced profile store splits the control
plane across windows → two stores: a synced control plane, and a leader-only section store.
`useEditorSettingsStore` is not `syncManager`-registered, so follower edits never reached the leader
→ every window rehydrates unregistered projected stores on the native `storage` event. No task ever
called the attachment routes, so nothing protected a profile from deletion → attach / reconnect /
detach do. Also: executor generations and abort on teardown; **one network write at a time per
profile**, so a `schema` answer stops all writes rather than eventually; `locked:invalid` for a SOT
payload this client refuses to apply (never re-fetched on a timer; unlocked by a newer revision);
a `hosts` payload may not remove or re-point the master's own host; conflicts and their payloads
persist across restarts; the client id is read from storage every time; reindex is single-flight and
a failed list is never an empty list.

### 9.8 PR #1242 / P2b-1b review — R1 + two attackers (gpt-5.6-sol), 2026-09-20

R1 over the whole of P2b-1: no findings. Two attackers, scoped by focus to each half:

| # | Sev. | Finding | Resolution |
|---|---|---|---|
| A-1 | high | with storage unavailable, the realm-only fallback client id is still written to the daemon (pre-existing in the Sync store, heavier once attachments exist) | `isClientIdPersisted()`; the driver refuses to attach without it. Existing callers unchanged |
| A-2 | medium | first-run, two windows: the losing id can be used before they converge (pre-existing; this PR narrows it — an existing install adopts its old id) | #1243 |
| B-1 | high | `section-store` read-modify-write: overlapping leaders silently overwrite each other — a lost base is a false conflict, a lost stash is an unrecoverable one | write generations; a superseded leader is `fenced` |
| B-2 | high | the 5 MiB cap counted one payload, not the document or the origin's quota; a conflict could persist without its payloads; the test mocked `setItem` away | 1 MiB cap, **conflict + payloads in one `setItem`**, real-storage boundary tests; IndexedDB → #1244 |
| B-3 | high | a loaded conflict could point at a stash entry the loader had just discarded | referential integrity on load — the conflict is dropped, the base kept |
| B-4 | medium | path encoding and `JSON.stringify` ran outside the protected entry, breaking "never throws" | moved inside |
| B-5 | medium | the WS parser treated a contradictory event as a delete (**the main session had specified that lenient rule; it was wrong**) | only the two wire shapes the daemon emits |
| B-6 | medium | one listener slot, cleared unconditionally — a previous driver's late cleanup silenced the next | an unsubscribe that removes only itself |
| B-7 | low | `api.ts` is ~630 lines of several concerns | #1240 |

Also found while implementing, by a subagent, against this plan's own text: **`getDaemonBase` falls
back to another host for an unknown id instead of throwing**, so a CAS could have reached the wrong
daemon; the client refuses before sending.

**R2 critic** (incremental, `--base 34985254`): agrees with all eight and finds the handling of
A-1, A-2 (an issue, not a fix), B-3, B-4, B-5 and B-6 sound; no other wire drift of Important or
above. Two evidenced objections, both to `section-store`:

| # | Sev. | Objection | Resolution |
|---|---|---|---|
| C-1 | high | the generation fence is itself a non-atomic read-compare-write on `localStorage`: A reads g1, B claims g2, A writes its whole g1 document back over B's claim and data — B-1 again, just narrower | **one key per section, one content-addressed key per payload; the fence removed**, not patched. A stale write has no unrelated data left to destroy. What remains (same key, two leaders, last writer wins → at worst a false conflict) is written down as a residual, not claimed as prevented |
| C-2 | high | the 1 MiB cap meant a larger conflict silently failed to persist, against Task 5 and §4.6.2 — and "kept in memory, re-derived after a restart" must not be described as meeting the contract | the cap is the daemon's 5 MiB again and the browser's quota decides; when it does not fit, that is recorded as a **known gap** (§7, #1244), in those words |

### 9.9 P2b-2 — what integration found, 2026-09-20

The driver is where five separately-built parts first met, and most of what it turned up was a
contract that read fine on its own and was wrong in company. Full list in the P2b plan ("P2b-2 as
built"); the ones that change this spec's meaning:

- **A second store leaves the profile, for the user to confirm**: `useEditorSettingsStore` declares
  itself device-local in its own source, with the very scenario this project exists for (a small
  laptop and a big monitor). `settings` ordinal → 3.
- **§4.7 "apply" is `setState` + `persist.rehydrate()`**, proven store by store — except the tab and
  workspace stores, which must *not* be rehydrated (it rebuilds every tab object and re-renders every
  pane); and a rehydrate's in-place heal is invisible until an empty `setState` follows it.
- **§4.6.3 ordering is a correctness rule, not a nicety**: `tabs.*` is not pulled until `hosts` and
  `workspaces` are *settled* — synced, indexed, SOT unmoved, idle. "Synced" alone also describes a
  section that is clean but behind, and applying tabs against stale hosts marks another client's
  live panes `host-removed` and pushes that back to everyone.
- **§4.6.1 "the profile was recreated" is not per-section.** A profile that vanishes from the list is
  handled above the state machines (`profileGone`): telling each of them "absent" would pull
  deletions and erase the local state. A *failed* list tells them nothing.
- **A pull reports two hashes** — what the SOT held, and what the stores hold after the apply — so
  that a sanitiser's correction is pushed back instead of being mistaken for the SOT's content.
- **An empty, never-agreed `tabs.<id>` facing a SOT with content is a placeholder**, not a local
  edit; otherwise every workspace arriving from another machine could open as a conflict.
- **A persisted conflict needs only its local side's payload** (the sent snapshot, §4.6.2); the SOT
  side is re-fetchable.

### 9.10 P2b-2 real-machine acceptance, 2026-09-20 (first pass)

Worktree dev server `http://100.64.0.2:5175` (plain HTTP on the tailnet IP — the same kind of origin
the Electron dev window loads), the live mlab daemon (alpha.411), two Playwright browser contexts =
two clients with their own `clientId` and `localStorage`. Hosts were seeded through a 0600
storage-state file; the token never appeared on a command line or in output. Driven through the
dev-only `window.__purdexProfileSync` hook, which calls the same `attachMaster` / `detachMaster`
P3's wizard will.

| Spec §6 | Result |
|---|---|
| iron rule (no master) | **pass** — 0 requests to `/api/profiles`, no `purdex-profile*` key, before attach |
| 1 first push | **pass** — `hosts`, `settings` (ordinal 3), `workspaces` at rev 1; adding two workspaces created `tabs.<id>` × 2 and moved `workspaces` to rev 2 |
| 2 second client pulls | **pass after a manual `Take SOT`** — see the second finding below |
| 3 propagation | **pass — 519 ms** from the rename on A to the store change on B (500 ms debounce + 19 ms for PUT → broadcast → GET → apply). Only `workspaces` advanced |
| 4 converged | **pass** — the same rename on both at once advanced the rev exactly once; both `synced`, no lock |
| 5 conflict locks one section | **pass** — different renames at once: A applied, B got 409 and only `workspaces` went `locked:conflict`; B's local text was not overwritten; a tab B added elsewhere meanwhile still synced (`tabs.<id>` rev 2); `Keep local` on B converged both |
| 8 focus not mirrored | **pass** — A switched active workspace; B's did not move and no section advanced |
| 12 delete refused while attached | **pass** — 409 `{reason: 'attached'}` with two attachments |

**Two real findings, neither reachable by the 8,459 unit tests:**

1. **`crypto.subtle` does not exist outside a secure context**, and `http://100.64.0.2:5175` is not
   one — so not a single section hash could be computed: `Cannot read properties of undefined
   (reading 'digest')`. The P2a measurement had flagged exactly this and the main session filed it
   under "pre-existing exposure"; it is in fact the main path, because plain HTTP on the tailnet IP
   is how both workstations load the app in dev mode. jsdom has Node's `crypto.subtle`, so every test
   was green. Fixed with a pure-JS SHA-256 fallback in `lib/crypto-hash.ts`, byte-identical to
   WebCrypto (NIST vectors, padding boundaries, 200 seeded random inputs) — two clients on different
   paths must agree on every hash or they never converge. ~15 ms/MiB.
2. **Attach had no direction.** A brand-new client's empty `workspaces` list is a payload with a
   hash; it has never agreed with the SOT; the SOT has content → the decision table says conflict,
   correctly — it cannot know which side should win. What was missing is the user's decision 10: the
   wizard's **push / pull**. `attachMaster` takes a direction, persisted in the control store as
   `pendingDirection` until the first full settle; during that initial reconciliation a conflict is
   resolved by the direction instead of being put to the user, and `push` also removes `tabs.*`
   another client left behind. `pull` overwrites local state, and decision 12 requires saving it as
   a slave first — slaves arrive in P3, so until the wizard exists only the dev hook calls this path.

**A lesson about the procedure itself.** The first pass polluted its own SOT: a subagent was fixing
`crypto-hash.ts` in the same worktree while client A's page was open, and its mutation tests —
SHA-256 deliberately broken three ways — were **hot-reloaded into the live page by Vite**, which then
pushed sections hashed and fingerprinted by a mutant. The next, correct build found a different
fingerprint at the same ordinal and went `locked:schema` — which is, at least, the schema lock failing
closed on a real machine. Rule since: **no mutation testing in a worktree while a page served from it
is open**; close the browser and the dev server first.

**Second pass, after the direction fix** (fresh profile; A attached with `push`, B — which had a
workspace of its own — with `pull`):

| Spec §6 | Result |
|---|---|
| 2 second client pulls | **pass, unattended** — B ended with A's two workspaces and three tabs, its own workspace gone (that is what `pull` means), every section `synced`, `pendingDirection` cleared; **every section still at rev 1 with A as writer — B wrote nothing** |
| 9 split ratios | **pass** — B got the split with its own `[50, 50]`; B dragging to `[20, 80]` left A at `[70, 30]` and wrote nothing |
| 6a merely behind | **pass** — B paused, A renamed five times, B resumed: fast-forward to the last name, no lock |
| 6 / 6b a dirty section refuses an inbound apply | **pass** — B's unsynced tab was not replaced by A's; `Keep local` converged both on B's version. One departure from the letter of 6b: B went `locked:conflict` *when A's event arrived*, not on `Sync now`. Locking is local bookkeeping and sends nothing; learning of the conflict earlier is more useful than later. §6 6b should read "is `locked:conflict` no later than `Sync now`" |
| 6c section lifecycle | **pass** — A created a workspace, B gained it and its tabs; B deleted it, A lost both; **no `tabs.<id>` row left on the daemon** |
| two windows, one context | **pass** — exactly one led; an edit made in the *follower* window reached the daemon through the leader, **exactly once**, and B saw it; closing the leader window, the follower took over within the lease TTL |
| 7 schema lock | **FAIL → fixed** — see below |

Offline was simulated with `autoSync: false` rather than by stopping the mlab daemon (the user's
working environment): the state machine treats `!reachable` and `!autoSync` in the same branch.

**Third real finding.** A `settings` write with another fingerprint and ordinal 99 (a "newer Purdex",
by `curl`) was accepted and broadcast, as it should be. Both clients answered with that *section*
`locked:invalid` — and a later edit on B **was written out**. §4.4 says the whole profile locks and
an older client writes nothing. `profileLock` was evaluated only on reindex, while clients learn of
a new revision through the event — which carries no fingerprint — and pull; the fetched section's
`fingerprint` / `ordinal` went unread. The pull path now compares shape before applying.

Also seen, self-healing but wrong in passing: when a `tabs.<id>` deletion overtook the `workspaces`
change that caused it, the receiver emptied a workspace it still had and nearly recreated the
section on the SOT. A deletion of `tabs.<id>` for a workspace that still exists locally now waits.

**Third pass, after those two fixes** (fresh profile again): item 7 **passes** — both clients read
`profile: locked:schema`, name `settings`, verdict `sot-is-newer`; an edit made on each afterwards
produced **zero writes** on the daemon while staying in the local stores. Item 6c again, with a
subscriber on A watching for the bad intermediate state: the workspace B deleted was **never seen
present-but-emptied** on A, the daemon kept no `tabs.<id>` row, and neither client reported a
`pull-hash-mismatch` or `push-payload-missing`.

Afterwards: both browsers closed, the dev server stopped, **every acceptance profile deleted from
the daemon** (`profiles: []`), the storage-state file that held the token removed.

**Not run here:** item 13 in its real form (a second *machine* reaching every host with the pulled
tokens — both contexts were seeded with the same host), and the cross-machine run on air-2026. The
App there loads the main checkout's `:5174`, which this isolated worktree session cannot `git pull`;
that run is the user's acceptance.

### 9.11 PR #1247 / P2b-2b review — R1 + two attackers (gpt-5.6-sol), 2026-09-20

R1 reviewed the whole of P2b-2; each attacker was scoped by its focus to one half.

| # | Source | Sev. | Finding | Resolution |
|---|---|---|---|---|
| R1-1 | R1 | P1 | `detachMaster` awaited the server-side cleanup first — up to a 15 s timeout during which the driver kept pulling and pushing after the user had said stop | `clearMaster()` first (the driver dies synchronously), then the best-effort `deleteAttachment` |
| R1-2 / B-1 | R1, attacker B | P1 / high | re-attaching to the **same** master: with `pull`, the kept bases made a locally dirty section an ordinary push — the local edit overwrote the SOT, the opposite of what was asked; and `pendingDirection` could **stay set forever**, so an ordinary conflict hours later was answered silently by a stale direction | every attach is a fresh reconciliation (section store cleared, the whole master mode rebuilt, `attachGeneration` tells other windows); one reconciliation period per executor — once settled it answers no lock, reports `stale-direction` and asks to be cleared; the settle check runs after every round, including rounds with no action |
| B-2 | attacker B | high | the master host's `ip` / `port` edited in place: the executor went on using the *old* daemon's bases against the new address | the attach-time endpoint is **persisted**; a different one blocks the driver (`master-endpoint-changed`) until the user re-attaches or puts it back. A token change alone rebuilds the driver and keeps the bases |
| B-3 | attacker B | medium | on reload / leader hand-over, syncing began before the attachment PUT had succeeded — a window in which the profile could be deleted from under it | reachability is `attached && connected`; nothing is requested before the attachment is confirmed. (Measured first: an executor that never received `onReconnected` *does* issue requests once the collector reports, so withholding that call gated nothing.) An attachment **404 means the profile is gone**: no retry, `blocked: 'profile-gone'` |
| B-4 | attacker B | medium | the control store's `syncManager.register` at module load creates a `BroadcastChannel`, so "no master, no effect" is false | **refuted with evidence**: `lib/storage/sync.ts:12` — the channel is a singleton, and 18 stores on `main` already register at load; this adds one entry to a Map. The wording of the rule was made exact instead |
| A-4 | attacker A | high | a workspace whose id cannot form a `tabs.<id>` key was skipped for tabs but still pushed inside `workspaces`; every other client's guard then refused the payload and locked — self-propagating poison | such a workspace is **device-local**: the builder leaves it out, the applier keeps the local one in place, the collector reports it |
| A-2, A-3 | attacker A | high | rollbacks covered less than the apply: removing a workspace also cleared a third store; a settings rollback restored fields but not the theme/locale registries, the DOM or the translator | the third store joins the snapshot; a settings rollback re-registers what was dropped and re-runs the rehydrate path; a rollback that itself fails says so (`rollback incomplete`) instead of being swallowed |
| A-1 | attacker A | high | a failed hosts apply does not restore three cache stores | the claim "they are re-fetched" was **checked rather than repeated, and is only one third true**: the execution store recovers, `nex-host` and the execution list do not (nothing re-runs `ensure` for a host that reappears under the same id). The rollback now calls `ensure`; from the first staged write to the end of the rollback there is no `await`, so the connection layer never sees the intermediate host list and `runtime` is restored truthfully. The same gap in the app's own delete → Undo path is #1248 |
| A-5 | attacker A | medium | the lease's release is read-then-remove; a window frozen in between for longer than the TTL deletes the next leader's lease | recorded in the header as a residual, with a test of what actually happens (one extra hand-over) |
| A-6 / B-5 | both | low | `executor.ts` (~1,200 lines), `apply-to-stores.ts`, `collector.ts` do too much | #1240 |

Three times in this phase a subagent was told *"verify this premise; if it does not hold, stop and say
so"* and it did not hold as stated: the rehydrate path (held, with two side effects nobody had
predicted), "the caches re-fetch" (one third), and "restore `runtime` as reconnecting" (wrong — the
WebSocket had never been touched). Each would have shipped as a plausible comment otherwise.

_(critic verdicts to follow.)_
