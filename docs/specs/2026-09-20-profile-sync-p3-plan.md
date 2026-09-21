# Plan — Profile Sync P3: slaves, the switcher, Settings › Profile (five PRs)

- Spec: `2026-09-20-profile-sync-spec.md` §4.1 (master / slave / active), §4.3, §4.9, §9.9–§9.11; the
  user's decisions 9–13. The driver's contract is `spa/src/lib/profile/start.ts` and the "as built"
  sections of `2026-09-20-profile-sync-p2b-plan.md`.
- Worktree `.claude/worktrees/profile-sync`, based on `origin/main` alpha.414.
- **Six PRs**, each ≤ 20 files, merged in order; each leaves the app working:
  **P3a** foundations (no UI) → **P3b** slaves and the active pointer → **P3c-1** every tab gets a
  workspace → **P3c-2** the dead standalone code goes → **P3d** the UI → **P3e** the new-tab "profile"
  rename (may slip to an issue). *(Revised after the codex plan review `task-mu992a03-9xfv6x`: ten
  findings, three critical, all accepted; spec §9.12.)*
- **Invariant: a user who never opens Settings › Profile sees today's app** (spec Law 4). P3c is the
  one exception by design — it removes a concept — and is called out below.
- Every task: subagent, TDD, one commit per task, `git commit --only`, Bash prefixed with
  `cd /Users/wake/Workspace/wake/purdex/.claude/worktrees/profile-sync/spa &&`, scratch files prefixed
  with the task id. Every prompt carries *"if the plan contradicts itself or a premise does not
  hold, stop and say so"*; mutation results are a deliverable.
- **No mutation testing while a page served from this worktree is open** (spec §9.10).
- Before each PR: `npx vitest run && pnpm run lint && npx tsc -p tsconfig.app.json --noEmit &&
  pnpm run build`.

## Measured baseline (2026-09-20, alpha.414) — and where the spec was wrong

- ⚠ **`getScopeTabs` does not exist** (spec §4.3). The standalone branch is an inline block in
  `closeTabInWorkspace` (`features/workspace/store.ts:196-202, :215`).
- ⚠ **Standalone tabs are not three call sites but a concept with three live producers** and
  17 production files / ~12 test files: (1) `insertTab` with no active workspace **silently no-ops**
  (`store.ts:147`), leaving the tab in `tabOrder` only — reached by `handleAddTab`
  (`hooks.ts:128-133`), four shortcuts (`useShortcuts.ts:71-98`) and `useElectronIpc.ts:29`, and
  pinned as intended by `store-tabs.test.ts:58`; (2) explicit `insertTab(id, null)` via
  drag-to-home-header (`computeDragEndAction.ts:126-131`); (3) **deleting a workspace while keeping
  its tabs** — `removeWorkspace` never touches `useTabStore`, and
  `WorkspaceSettingsPage.tsx:188-207` deliberately goes Home with them; tear-off / merge
  (`useWorkspaceWindowActions.ts:21-32`) has the same shape. A second copy of `handleSelectHome`
  lives in `useShortcuts.ts:103-116`. With zero workspaces every tab is standalone and the empty
  state is a hard-coded Chinese string (`TabContent.tsx:20-26`).
- ⚠ **`profileSyncState()` cannot back a React subscription**: a fresh object and a fresh
  `problems` array per call, no `subscribe`; the executor *does* emit `onStatus` on every change
  (`executor.ts:194`) but `start.ts:264-266` swallows it into `lastStatus`.
- ⚠ **Status is leader-only and most windows are followers** (`start.ts:112-114`): in a follower
  `status` is `null` and `syncNow()` / `resolve()` have no executor to reach.
- ⚠ **Nobody designed how a slave meets the collector.** Switching `active` to a slave puts the
  slave's workspaces and tabs in the live stores — and the collector reads the live stores, so it
  would push a slave's world to the SOT. Decision 9: slaves never reach the daemon, *and* the master
  keeps syncing while a slave is active.
- `lib/profile/start.ts:77` already imports `effectiveDeviceName` / `useDeviceStateStore` from the
  module P4b deletes. "Named after the host" (spec §4.9, §6.2) means **this computer's device name**
  — the code has no other candidate.
- Replacing the whole tab world: `commitTabWorld` (`apply-to-stores.ts:448`, module-private) already
  does it right — re-derives `tabOrder`, filters `visitHistory`, keeps or re-points `activeTabId`,
  three-store rollback, **no rehydrate** of the tab/workspace stores. Terminals are released by React
  unmount only (`useTerminalWs.ts:138-143`, `useTerminal.ts:120-126`); switching back re-attaches
  (tmux state is on the host; browser scrollback is lost).
- Settings: a purdex-scoped section is one `registerSettingsSection({id, label, order, component})`
  plus a named `SETTINGS_ORDER` constant (`settings-order.ts:39-42` bans literals; bands 0–4 core,
  11–19 module-owned, 20–29 tail). Sync is registered as a *module* (`register-modules/index.tsx:
  265-276`); `SnapshotHistoryPage` is reachable only through it.
- i18n: flat JSON, `spa/src/locales/{en,zh-TW}.json`; `locale-completeness.test.ts` fails the suite
  on one missing or empty key and pins placeholder parity; `t()` returns the key for a typo, never
  `undefined` (so `t(x) ?? 'fallback'` is dead code — do not copy it).
- Menus: no primitive with arrow keys / `role="menu"`; `BrowserToolbarMenu` is `position: absolute`
  with no portal and would be clipped under the 44 px narrow bar (`overflow-hidden`);
  `FloatingPanel` is portal-based and Escape/outside-click aware. `ConfirmDialog` is the shared
  confirmation (`testIdPrefix`, `busy`).
- The wide bar's Home title is already overloaded: active + expanded toggles expand instead of
  selecting (`HomeRow.tsx:61-67`), and it is a drop target (`home-header`).
- New-tab "profile": 22 files; the persisted field `purdex-newtab-layout.profiles` is a **synced
  settings path** (`projections.ts:63`), so renaming the field is a shape change and needs a
  `migrate`.

## P3a — foundations (no UI)

### Task 1 — `lib/device-name.ts`
Move `normalizeDeviceName`, `effectiveDeviceName`, `resolveDefaultDeviceName` and the persisted
`deviceName` out of `useDeviceStateStore` / `lib/device-state/device-name.ts` into a module that
survives P4b: `stores/useDeviceNameStore.ts` (same storage key `purdex-device-state`, same persisted
shape `{deviceName}`, so nothing migrates) + `lib/device-name.ts`. `useDeviceStateStore` re-exports
for its remaining callers (they die in P4b). `start.ts` imports the new home.

### Task 2 — a subscribable, cross-window sync status
- `start.ts`: re-emit instead of swallowing. `subscribeProfileSync(fn): () => void` and a **cached**
  `profileSyncSnapshot()` whose identity changes only when something did (status, leader, blocked,
  problems) — what `useSyncExternalStore` needs.
- **Everything is scoped to the master it was made for** (PR review, codex R1/R2): the master is
  persisted and every window rehydrates it in its own time, so for a moment two windows are on
  different masters. `masterTag = hostId|profileId|attachGeneration`; one channel per tag. A status
  record of another tag is "no record"; a command lives under its tag's key prefix and names the
  tag in its payload as well; a window that lets go of a master removes that master's keys only
  (there is no resend — a command removed by a late window is a lost user action). Expired commands
  of other tags are swept by whoever opens a channel; never by a user without a master.
- **The generation hand-over** (critic): the same master attached again is a new tag. A window that
  closes its channel for the next generation of the SAME master first carries over every `syncNow`
  under its own prefix that is still in time — same id, same `at`, `master` renamed — and then
  removes the old keys. A `resolve` is never carried over (an attach is a new first reconciliation;
  the confirmed lock was the old driver's), and nothing is when the master itself changed. Done in
  `close(true, successor)`; a window that is itself the new leader finds it by its take-over scan.
- **The leader publishes, followers read.** The leader writes `{at, leader: windowId, master: tag,
  status, blocked, problems}` to `localStorage['purdex-profile-status']` on change (throttled to one
  write per 250 ms, trailing); every window listens to the native `storage` event for that key. A
  follower's snapshot is the published one, marked `remote: true`; older than 10 s with no leader
  lease → `stale: true`. Cleared when the master is cleared.
- **Commands reach the leader the same way, one key per command** (review #5: an array in one key
  is read-modify-write and loses commands by construction — two followers both read `[]`; the leader
  clears what a follower has just appended): `localStorage['purdex-profile-cmd:<encoded tag>:<id>'] =
  {kind, section?, keep?, lock?, master, at}`. The leader executes and removes the key; a command
  older than 30 s — or more than 5 s in the future (a clock set back) — is removed unexecuted. Two
  leaders in a hand-over may both execute one — harmless, because `resolve` on a section that is no
  longer locked and `syncNow` are both no-ops in the reducer.
- **A `resolve` is bound to the lock the user was looking at** (review #6, widened by the PR
  review): it carries the section's `SectionLock` — `{status, currentHash, sot: {rev, hash},
  conflict: {localHash, sot} | null}`, published per locked section in `ExecutorStatus.locks` — and
  the leader refuses it unless the executor holds that very lock now, field by field. The pair alone
  is not enough: `locked:reset` / `locked:invalid` have none, and under them the local content, the
  SOT and even the kind of lock can change while "no pair" stays true — `Keep local` would push
  content, or overwrite a SOT, nobody confirmed.
- `hooks/useProfileSync.ts`: `useSyncExternalStore` over the above.

## P3b — slaves and the active pointer

**Rewritten after the plan review (spec §9.12).** The first draft swapped the live stores' contents
and called the switch "one synchronous block". That holds inside the window that switches and
nowhere else: the tab store, the workspace store and the local-profiles store are three persisted
stores that other windows rehydrate one by one, in no guaranteed order — so a *leader* in another
window could see a slave's world in the live stores while still believing the master was on screen,
and report it to the SOT.

### Task 3 — `stores/useLocalProfilesStore.ts`
Device-local, persisted (`purdex-local-profiles`, `syncManager`-registered):
```ts
interface ParkedWorld { workspaces: Workspace[]; tabs: Record<string, Tab>; activeWorkspaceId: string | null; activeTabId: string | null }
interface LocalProfile { id: string; name: string; createdAt: number; world: ParkedWorld | null }  // null ⇔ on screen
slaves: Record<string, LocalProfile>; slaveOrder: string[]
activeProfileId: 'master' | string
parkedMaster: ParkedWorld | null
worldEpoch: number                            // see Task 4
```
Invariant: exactly one world is on screen (`world === null` / `parkedMaster === null`); `merge`
sanitises anything else back to "master on screen".

### Task 4 — `lib/profile/master-world.ts`: where the master's tab world is — *or that nobody can say yet*
**A world tag and an epoch barrier.** `useTabStore` and `useWorkspaceStore` each gain two persisted
fields, `worldId: 'master' | string` and `worldEpoch: number`. A switch writes the **same** epoch
and world id into all three stores in its synchronous block. Then:
```ts
export type MasterWorldRead =
  | { settled: true; world: { workspaces; tabs; tabOrder }; onScreen: boolean }
  | { settled: false }                                            // the three stores disagree
export function readMasterWorld(): MasterWorldRead
export function writeMasterWorld(next, afterWrite?): 'ok' | 'unsettled'
export function subscribeMasterWorld(fn): () => void
```
`settled` ⇔ the three `worldEpoch`s are equal **and** the two live stores carry the same `worldId`
**and** that id equals `activeProfileId`. Unsettled: the collector reports **nothing** for
`workspaces` / `tabs.*` / `settings` and re-checks on the next store change (a rehydrate that never
arrives leaves it silent, never wrong — the executor sees an ordinary clean section); an apply
answers `busy`. With the master on screen and settled nothing changes from today. Tests drive the
three rehydrates in every order, including one that never arrives, and assert that **no report ever
carries a slave's content** (each world's tabs hold a sentinel).

- **`settings` is not "always live"** (review #1, critical): `purdex-workspace-settings.workspaces`
  is a synced field keyed by workspace id. The settings builder projects only the ids of the
  **master** world; the applier keeps local entries whose id is not the master's. Same rule for any
  other listed settings field keyed by workspace id — Task 4 starts by grepping the eight stores for
  one and reports what it finds.
- **Hosts** stay live (a slave borrows them, decision 9) — but removing a host marks
  `host-removed` panes in **every** world: the live one through `deleteHostCascade`, every parked one
  (master and slaves) through `markHostRemovedPanes` (review #3).
- `commitTabWorld` is exported from `apply-to-stores.ts` and stamps `worldId` / `worldEpoch`.

### Task 5 — `lib/profile/switch-active.ts`
`switchActiveProfile(targetId)`: `withOperationLock('profile-switch')`; **one synchronous block**:
park the on-screen world → take the target's → `commitTabWorld(target, {worldId, worldEpoch: n+1})`
→ write `activeProfileId` and the epoch to the local-profiles store; any throw restores all three.
Afterwards (not in the block) it asks every host for its sessions, so the reconciliation that only
ever looks at the live tabs (`useMultiHostEventWs.ts:149`, `useTabStore.ts:852`) runs over the world
that has just come on screen (review #4). What that leaves: **a parked world does not hear about a
session that closed while it was parked** — for the master that is exactly a section that was
offline for a while, and it is said so in the code; rebuild works the same for both (decision 13)
because its inputs travel with the tab.
- `copyMasterAsSlave(name)` — the only copy (decision 10); fresh tab / pane / workspace ids.
- **`promoteToMaster(slaveId)`** — a *move*, never a copy (decision 10: "沒有複製為 master"): the
  slave's world takes the master slot and the previous master world, if there was one, becomes a
  slave named after it. Only while no master is attached (the wizard stops sync first).
- `saveScreenAsSlave(name)`, `renameSlave`, `deleteSlave` (never the one on screen).

### As built (for P3d) — spec §9.13 has the why

`lib/profile/switch-active.ts` — every refusal has written nothing; `write-failed` has put everything back:

```ts
switchActiveProfile(targetId: 'master' | string): Promise<SwitchResult>
//   { ok: true } | { ok: false, reason: 'busy' | 'unsettled' | 'superseded' | 'not-found'
//                     | 'already-on-screen' | 'bad-world' | 'bad-epoch' }
//               | { ok: false, reason: 'write-failed', detail: string }
promoteToMaster(slaveId: string, demotedName: string): Promise<PromoteResult>   // ASYNC since C1'
//   { ok: true, demotedId } | reason: 'master-attached' | 'busy' | 'unsettled' | 'superseded'
//                           | 'not-found' | 'bad-name' | 'bad-epoch' | 'write-failed' (+ detail)
copyMasterAsSlave(name): CopyResult      // { ok: true, id } | 'unsettled' | 'bad-name' | 'bad-world' | 'write-failed'
saveScreenAsSlave(name): CopyResult      // works while unsettled, too
renameSlave(id, name)                    // 'not-found' | 'bad-name'
reorderSlaves(order: string[])           // 'bad-order'
deleteSlave(id)                          // 'not-found' | 'on-screen'
```

What the UI must do with the reasons: `busy` (an operation lock, or the cross-window lock not granted
in 3 s) and `unsettled` are **retryable** — an `unsettled` refusal has already asked the stores to
catch up, so the same click a moment later works; `superseded` means another window's switch won and
this window is about to show ITS world — say so, do not retry; `master-attached` → the wizard stops
the sync first.

`stores/useLocalProfilesStore.ts` — persisted fields: `slaves: Record<id, { id, name, createdAt,
world: ParkedWorld | null }>` (`world === null` ⇔ on screen), `slaveOrder: string[]`,
`activeProfileId: 'master' | id`, `parkedMaster: ParkedWorld | null`, `worldEpoch`, `relabelCount`
(+1 per promote). `MASTER_PROFILE_ID = 'master'`. The UI reads these and calls switch-active.ts —
never the store's actions directly (they do not touch the tab stores).

`lib/profile/master-world.ts` — `readMasterWorld()`: `{ settled: true, onScreen, world }` or
`{ settled: false, reason }`, `reason` ∈ `'epoch-mismatch' | 'world-mismatch' | 'behind-fence' |
'junk-epoch' | 'no-parked-master'`. Only `no-parked-master` is permanent; `junk-epoch` is healed by
the next switch; the others by a rehydrate, which every user-facing refusal asks for.

`lib/host-lifecycle.ts` — `deleteHostWithUndoToast(hostId, closeTabs, { deleted, worldSkipped })`;
the undo of `deleteHostCascade` returns `{ worldSkipped: boolean }`.

## P3c — standalone tabs removed (§4.3), in two PRs

**P3c-1 — every tab gets a workspace** (the policy and the invariant; the standalone UI still
compiles but becomes unreachable). **P3c-2 — the dead code goes** (`isStandaloneTab`,
`reorderStandaloneTabOrder`, the two drag actions, the `home-header` drop target, `HomeRow`'s list,
`getVisibleTabIds`' Home branches, both `handleSelectHome`s, the collector's census, and their
tests rewritten). Measured at 17 production + ~12 test files, which is why it is two PRs
(review #9).

**Every tab belongs to exactly one workspace.** This is the one PR that changes the app for a user
who never opens Settings › Profile; both of the user's machines have zero standalone tabs (spec §3.1).
- **Adoption is a standing invariant, not a boot step** (review #7): a subscriber installed in
  `main.tsx` before the first render runs `adoptStandaloneTabs` (already in
  `lib/profile/sections.ts`) **whenever** the tab or workspace store changes and a tab belongs to no
  workspace — into `t('workspace.unsorted')`, created only when needed. That covers the producer the
  first draft missed, device-state's restore and merge (`device-state/merge.ts:146`,
  `restore.ts:127`; they live until P4b), and any other nobody has found. It runs on the on-screen
  world only and stamps nothing when the world is unsettled (Task 4). **Two windows adopting at
  once**: the workspace id is derived from a fixed seed (`unsorted`) rather than generated, so both
  produce the same workspace and the cross-window rehydrate converges; for the same reason two
  *machines* that both upgrade with standalone tabs end with one `Unsorted`, not two.
- The existing first-workspace flow (`MigrateTabsDialog`, `App.tsx:190-226`) no longer has tabs to
  offer — there are none without a workspace — and is removed in P3c-2.
- **A target-workspace policy for every producer**: `insertTab` with no target → the active
  workspace → else the first workspace → else **create** `Unsorted` and use it (never a silent
  no-op). `insertTab(id, null)` is removed from the type. Deleting a workspace with "keep these
  tabs" moves them to the next workspace (or a new `Unsorted`) instead of going Home; tear-off /
  merge already move their tabs first.
- **Removed**: `isStandaloneTab`, `reorderStandaloneTabOrder`, the `move-tab-to-standalone` and
  `reorder-standalone-tabs` drag actions, the `home-header` drop target, `HomeRow`'s tab list, the
  Home branches of `getVisibleTabIds`, `handleSelectHome` and its twin in `useShortcuts.ts`
  (`switch-workspace-home` becomes "open the profile switcher" in P3d; until then it focuses the
  first workspace), `closeTabInWorkspace`'s standalone branch, the collector's standalone census and
  `buildProfileDocument`'s `standaloneTabIds`.
- **`activeWorkspaceId === null` becomes unreachable when a workspace exists**; with zero workspaces
  the empty state is `WorkspaceEmptyState` with an i18n string (the hard-coded Chinese one goes).
- Tests premised on standalone tabs are **rewritten to the new rule, not deleted** — each asserts
  where the tab went.
- The Home button itself stays (it becomes the switcher in P3d); in this PR a click focuses the
  first workspace, so there is no dead control between the two merges.

### As built (for P3d) — spec §9.14 has the why

**The Home button is a plain button now, in both bars.** No tab list, no chevron, no drop target, no
unread badge, no status dot; a click calls `onSelectHome` whatever the row's state or the tab
position (the "active + expanded → toggle" overload of the baseline is gone).
- Wide: `features/workspace/components/HomeRow.tsx`, props `{ isActive, onSelectHome }` and nothing
  else; the wrapper `<div>` carries **`data-testid="home-header"`** (the `<button>` inside has the
  logo and `t('nav.home')`). It renders inside `ActivityBarWide`'s `DndContext` but registers
  nothing with it, and renders fine without one.
- Narrow: the first `<button>` of `ActivityBarNarrow.tsx`, found by **`title={t('nav.home')}`**; its
  `relative group` wrapper now holds the button only.
- `isActive` / the purple ring = `!activeWorkspaceId`: zero workspaces, or the moment before the
  invariant re-points a `null` pointer. With a workspace it is never lit.
- What a click does today: `App.handleSelectHome` → `handleSelectWorkspace(workspaces[0].id)` (nothing
  with zero workspaces); its twin is the `switch-workspace-home` shortcut in `useShortcuts.ts`. P3d
  replaces both with "open the profile switcher".
- `ActivityBarProps` (`activity-bar-props.ts`) no longer has `standaloneTabIds`,
  `activeStandaloneTabId`, `onReorderStandaloneTabs`, `onMoveTabToStandalone`; `onSelectHome` is the
  one Home prop.

**The workspace side P3d can lean on.**
- `UNSORTED_WORKSPACE_ID = 'unsorted'` (`features/workspace/store.ts`), `ensureUnsortedWorkspace()`,
  and `insertTab(tabId, workspaceId?, afterTabId?)` — no target → active → first → a new `Unsorted`;
  `null` is not a target.
- `startStandaloneAdoption(): () => void` (`features/workspace/lib/adopt-standalone.ts`, installed in
  `main.tsx` before the first render; returns its stop): zero owners → `Unsorted`, several → the
  first workspace keeps the tab, a `null` `activeWorkspaceId` → the workspace of the tab on screen,
  else the first. It acts `ADOPTION_SETTLE_MS` (500) after the last MEMBERSHIP change **or, for a
  tab that has needed repair without a break for `ADOPTION_MAX_WAIT_MS` (3000), at that moment —
  whichever comes first** (a membership that never goes quiet cannot starve it), and only on a
  settled world — so a profile switch (Task 5) that lands a world with ownerless tabs sees them
  adopted half a second after it settles, in the world on screen only.
- **`getVisibleTabIds({ tabs, tabOrder, activeTabId, workspaces, activeWorkspaceId })`** — the bar AND the
  range of close-others / close-right / the tab shortcuts: the active workspace's tabs; with
  workspaces but a `null` (or dangling) pointer, the workspace that owns `activeTabId`, else the
  first; `tabOrder` only with zero workspaces. Never wider than one workspace while one exists.
- For that half second a tab can be in no workspace. It is in no bar, the content area shows it if
  it is active, the `close-tab` shortcut closes it (in the visible set OR owned by nobody), and it
  closes whole (`closeTabInWorkspace`; focus → the active workspace). Nothing else treats it as a kind.
- **`repairTabOwnership(world: OwnershipWorld, { unsortedName, newWorkspaceId })`**
  (`lib/profile/sections.ts`) → `{ workspaces, activeWorkspaceId, adopted, dropped, membershipChanged }`
  is the ONE rule — first owner keeps, zero owners → Unsorted, the pointer follows the tab on screen
  only when the repair moved it. Callers: the invariant, and `switch-active.ts` on a world it is
  about to park or copy; `opts.only` repairs the given tab ids and nothing else (the deadline).
  **For the switcher UI:** `switchActiveProfile` can now answer `busy` for one more reason —
  something to repair and `tabOwnershipQuiet()` (adopt-standalone.ts) not yet true. It is retryable
  like every other `busy`; the wait ends after 500 ms of quiet membership or once everything that
  needs repair has needed it for `ADOPTION_MAX_WAIT_MS`, whichever comes first — **3 s at worst**
  (a `junk-epoch` world excepted: no age accrues while unsettled). A retry loop of ~250 ms for up to
  ~4 s covers it; by then the invariant has usually repaired the screen and the switch has nothing to do.
- `HOME_WS_KEY` (`useLayoutStore`) has no reader left but `reconcileWorkspaceExpanded`, which keeps
  the key alive; P3d may delete it with the rest of "Home".

## P3d — the UI

### P3d — how it is cut (written 2026-09-21, after P3a–P3c shipped; supersedes any PR count above)

Four PRs, each ≤ 20 files, merged in order; each leaves a working app and adds **one** reachable
thing. #1255 (re-run the session reconciliation after a switch) lands first, on its own.

| PR | Reachable afterwards | Contents |
|---|---|---|
| **P3d-1** | the Home button opens a menu — **where there is a slave** | `components/Menu.tsx` (the primitive) + `ProfileSwitcher` in both bars + its i18n. **With no slave the Home button is NOT a menu, in any PR** — **confirmed by the user, 2026-09-22: for someone with no local profile a click on Home does what it does today** (as built; this row first said the menu would shrink to a single `Set up sync…` item for everyone). Why: this plan's own iron rule is that a user who never touched the feature sees today's app, and turning every user's Home click into a menu breaks it — the iron rule wins. The entry for everyone is the Settings sidebar's *Profile*; `Set up sync…` is P3d-3's, in the *Current* block. |
| **P3d-2** | Settings › Profile exists: *Current* + *Profiles* | the section registration (`SETTINGS_ORDER.PROFILE`), the *Current* block (read-only state, Auto-sync, Sync now, Stop sync) and the *Profiles* block (slaves: copy master / save screen / rename / delete; **every local profile, the master included: name / icon / colour** — `setProfileAppearance`, with the two existing pickers, see "P3d-1 — As built"; SOT profiles: rename / delete). A master can still only be attached through the dev hook. |
| **P3d-3** | a user can attach a master without the dev hook | the *Wizard* (decision 10's five steps). `Settings › Sync` leaves the sidebar **here**, not earlier: until the wizard exists it is the only sync UI a user has. |
| **P3d-4** | conflicts can be resolved from the UI | the *Resolve* block, PRODUCT.md §3.9, and the real-machine acceptance of the whole of P3 (below), **through the UI**. **Also (moved here from P3d-2, which found the snapshot does not carry them):** `ExecutorStatus` publishes, per section, its `rev` and whether it is `failing` / `backingOff`, plus a `lastSyncedAt`; the *Current* block then shows the three — a revision on every row, the time of the last sync, and "settings are waiting for workspaces" also while `workspaces` keeps FAILING (today it is said only while `workspaces` is locked: a failing section reads `pending`, like one that is through in a moment, and `problems` is a log with no "over" — `sync-view.ts`, `settingsWaitForWorkspaces`). |

**What the UI must say, from the as-built contracts** (P3a / P3b / P3c "As built"):
- `switchActiveProfile` → `busy`: retry every ≈ 250 ms for ≈ 4 s, silently (it is the 3 s
  ownership gate or another window holding the world lock); only then a toast. `unsettled` /
  `superseded`: "another window just switched — try again", never a red error. `write-failed`:
  the one real error (storage full), with `detail`.
- `readMasterWorld()` unsettled reasons are **state, not errors**: `epoch-mismatch`,
  `world-mismatch`, `behind-fence` → "catching up with another window"; `junk-epoch` and
  `no-parked-master` → the *Current* block says sync is paused and why, in words, with the one way
  out (`junk-epoch`: switch profile once; `no-parked-master`: there is no master world on this
  device — run the wizard). The `world-unsettled` problem (P3b) is what makes the first group
  visible when it lasts.
- Sync state comes from `useProfileSync()` only (P3a). In a follower window every figure is the
  leader's and is labelled so; `stale: true` is said in words. `requestResolve(section, keep, lock)`
  must be given the **lock the user is looking at** — the panel passes the `SectionLock` it rendered,
  never a fresh read at click time.
- `settings` waits for `workspaces` (pull and push gates, P3b). When `workspaces` is locked or
  failing and `settings` is dirty, the *Current* block says "settings are waiting for workspaces" —
  otherwise a user sees a theme change that never leaves the device and no reason.
- `promoteToMaster` is refused while attached (`master-attached`): the wizard's step 1 (stop sync)
  is what makes step 3 possible; the order is not cosmetic.
- The undo of a host delete may answer `worldSkipped` (P3b) — already a toast; nothing to add.

**Design rules for all four PRs** (so that four subagents produce one UI): existing primitives only —
`FloatingPanel` (portal, Escape, outside click), `ConfirmDialog` (`testIdPrefix`, `busy`), the
settings row / section components the other built-in sections use (read two of them first and copy
their structure, spacing and tone), Phosphor icons, Tailwind tokens already in use; no new colour,
no new font size. Every string through `t()`, en + zh-TW in the same commit. Every control has a
`data-testid` (acceptance drives the UI). For a user with no master and no slaves the app is today's
plus ONE sidebar entry, Settings › *Profile*: a *Current* block that says what Profile Sync is in two
sentences (and, from P3d-3, offers the wizard — `profile-setup-start`), and the *Profiles* block with
one row, the master. (Changed 2026-09-22; it first said "nothing but one `Set up sync…` entry".
Why the master's row is not hidden: since the user's decision of 2026-09-21 every profile, the master
included, has a name, an icon and a colour — that row is where an ordinary user edits them.)


### P3d-1 — As built (for P3d-2)

- **`components/Menu.tsx`** — `<Menu trigger={ref} open onClose items label placement? testId? />`; an entry is
  `{ divider: true }` or `{ id, label, icon?, hint?, trailing?, title?, checked?, disabled?, busy?, keepOpen?,
  onSelect, testId? }`. `checked` defined (true or false) → `menuitemradio`; `keepOpen` → the owner closes the
  menu itself (async work); `busy` → spinner + `aria-busy` + `aria-disabled`, not activatable. `placement`:
  `bottom-start` | `right-start`, flipped / clamped into the viewport, below the Electron title bar. The TRIGGER
  owns `aria-haspopup="menu"` / `aria-expanded`. Its own portal, not a `FloatingPanel` (that is a titled,
  draggable `role="dialog"`); it shares that file's conventions and its `TITLE_BAR_HEIGHT`.
  - **Escape**: an open menu ASSUMES it is the topmost layer (nothing opens over a menu; z-index 100). Its
    Escape listener is on `document` in the **capture** phase and calls `stopImmediatePropagation()` — so the
    `FloatingPanel` / `ConfirmDialog` it may have been opened from does not close with it. It did not join
    `FloatingPanel`'s private `openPanels` stack: `ConfirmDialog` is not in that stack, so joining it would have
    fixed one of the two. The listener exists only while open; an IME-composition Escape is neither handled nor
    withheld.
  - **Entries changing while open**: if the focused item was removed (focus fell to `<body>`), focus goes to the
    checked item → the first available → the menu itself. An item that is still there keeps focus even if it
    turned busy / disabled (the arrow keys work from it), and focus the user moved elsewhere is never taken back.
- **The MENU exists only where there is a slave** (what the button shows does not depend on it — see below). `useProfileSwitcherTrigger(onSelectHome)` →
  `{ enabled, open, onClick, current, label, triggerProps }`, used by `HomeRow` (chevron, `bottom-start`)
  and `ActivityBarNarrow` (`right-start`). With no slave a click still selects Home, and
  `switch-workspace-home` still focuses the first workspace; with one, the shortcut sets `open` and the menu
  takes focus. **P3d-2 adds the `Settings › Profile` item** at the `TODO(P3d-2)` in `ProfileSwitcher.tsx` and,
  if the entry is to be discoverable with no slave, widens `enabled` in the hook — one place.
- **`stores/useProfileSwitcherStore.ts` OWNS the switch under way** (not persisted; idle = no timer at all):
  `open`, `pending: { targetId, generation, startedAt, abandoned } | null`, `setOpen(open)`,
  `chooseProfile(targetId): boolean` (false while one is pending), plus `hasLocalSlaves()` and
  `__resetProfileSwitcherForTest()`. Why the store: the two bars are two components sharing `open`, and a change
  of bar width remounts the menu — a component-owned attempt let a second switch start beside the first.
  - every answer is checked against `pending.generation`; a stale one is dropped.
  - `busy` → retried every `BUSY_RETRY_MS` (250) until `BUSY_RETRY_TOTAL_MS` (4000) has passed since the click,
    then a toast, menu stays; `unsettled` → the neutral toast, menu stays; `superseded` → the same toast, menu
    closes; `write-failed` → toast with `detail`; `not-found` / anything else → a toast naming it; success and
    `already-on-screen` → menu closes. **There is one toast and it has one look** (`useUndoToast`): "never a red
    error" is a matter of wording.
  - **closing the menu is "stop trying", not "undo"**: a pending retry timer is cleared and the switch is over;
    a call already in flight runs to its end — `pending` stays meanwhile (`abandoned: true`, so no second switch
    from a re-opened menu), a success is handled as a success (the world DID change), a real failure is still
    said, and a `busy` is dropped without a retry or a word.
- **`features/workspace/components/ProfileSwitcher.tsx`** (not `components/` as Task 6 says: it sits with the
  two bars that render it) only reads: the chosen item is `busy` from the click to the last answer, the others
  are disabled.
- **The master's dot** reads `useProfileSync()`: `blocked` (`suspended` → syncing, else problem) → `status.profile`
  (`locked:*` → locked, `synced`, `pending` → syncing) → unknown (no status, or `remote && stale`). It does **not**
  read `problems`: that is a log with no "over" signal; the *Current* block is where it is listed.
- **Every profile — the master included — has a name, an icon and a colour** (user decision, 2026-09-21: "Home
  becomes the profile's name, `Home` when it has none; a profile can pick the logo / a Phosphor icon + a
  colour"). Data model, in `useLocalProfilesStore` (device-local, persisted, sanitised by `merge`; NOT in the
  SOT — per device for now, syncing it would take a section of its own, outside P3):
  - `ProfileAppearance { icon?, iconWeight?, color? }`; `LocalProfile extends ProfileAppearance`; the master gets
    `master: MasterAppearance = { name: string | null, …ProfileAppearance }` (`{ name: null }` by default). A
    slave's name stays required; only the master can be unnamed.
  - **The shapes are the app's own, so the existing pickers fit**: `icon` / `iconWeight` exactly as
    `Workspace.icon` / `Workspace.iconWeight` (`IconWeight` in `types/tab.ts`; validated with
    `isPhosphorIconName` / `isIconWeight` from `lib/host-color.ts` — an unknown name would be painted as TEXT by
    `WorkspaceIcon`); `color` is a host's strict `#rrggbb` (`isValidHostColor`, stored lower-case). **For
    P3d-2**: the icon picker is `features/workspace/components/WorkspaceIconPicker.tsx` (`currentIcon`,
    `onSelect`, `onCancel`, `inline?`, `currentWeight?`, `onWeightChange?` — store-agnostic, reusable as is);
    the colour side is `HOST_COLOR_PRESETS` (`lib/host-color.ts`) + `components/hosts/HostColorLayerEditor.tsx`
    (`color`, `alpha`, `inherited`, `onChange`, `onClose` — store-agnostic; pass `layer="main"`, ignore alpha).
    `components/hosts/HostColorField.tsx` is NOT reusable: it takes a `hostId` and writes `useHostStore` itself.
  - **A name carries nothing invisible.** `normalizeLocalProfileName` removes `\p{Cc}`, the bidi controls and the
    zero-width / invisible format characters (the list is in the store file; ZWJ included on purpose — an emoji
    family comes apart, the rule has no zero-width hole), THEN trims and cuts at 64 code points; no Unicode
    normalisation. Every way in goes through it, `merge` included, so an older build's name is cleaned on load.
    `normalizeDeviceName` is untouched — another field, another PR.
  - `setProfileAppearance(id | 'master', { name?, icon?, iconWeight?, color? })` — absent key = unchanged, `null`
    = clear; one bad value refuses the whole patch (`not-found` / `bad-name` / `bad-icon` / `bad-weight` /
    `bad-color`); clearing the icon clears its weight. Dev hook: `profiles.setAppearance(id, patch)` and
    `profiles.appearance(id)`.
  - **`promoteSlave`: the look goes with the WORLD, not the label** — the promoted slave's name / icon / colour
    become the master's, the old master's go to the demoted slave; `demotedName` names the demoted slave only
    when the old master had no name. `switch-active.ts`'s rollback snapshot includes `master`.
  - **A copy starts plain**: `copyMasterAsSlave` / `saveScreenAsSlave` give the new slave its name and no icon,
    no colour.
- **What the Home button shows — one rule, no "only with a slave" branch**: the name, icon and colour of the
  profile ON SCREEN. Unnamed → `t('nav.home')`; no icon → the Purdex logo (the very `<img>` it always had); the
  colour tints a Phosphor icon (`ProfileIcon` in `ProfileSwitcher.tsx` — a workspace-style icon with a host-style
  tint; **it does nothing to the logo, which is a bitmap** — P3d-2's editor should say so or offer the colour
  only with an icon). With nothing set and no slave both buttons are today's, attribute for attribute (pinned).

  | | wide (`HomeRow`) | narrow |
  |---|---|---|
  | nothing set, no slave | logo + `Home`; no `title`, no `aria-*` | logo, `title="Home"`; no `aria-*` |
  | named / icon set | icon + name (CSS-truncated); `title` = name (only when named); `aria-label` = name or `Home` | icon; `title` = `aria-label` = name or `Home` |
  | a slave exists | + chevron, `aria-haspopup` / `aria-expanded`; `aria-label` = `t('profile.switcher.trigger', { name })` | the same, no chevron |

  In the menu every item is icon + name; the master's name is `Home` until it has one, and it alone carries the
  `t('profile.master')` tag (the Menu item's `hint`) and the sync dot. **`profile.master` is the one key for the
  word** (en `Master`, zh-TW 「主要」).
- test ids: `home-button`, `home-label` (wide), `home-switcher-chevron` (wide), `profile-icon` (`data-icon`,
  `data-weight`; absent while the logo shows), `profile-switcher-menu`, `profile-item-master`, `profile-item-<slaveId>`, `profile-sync-dot` (`data-state`);
  busy = `aria-busy="true"` on the item.
- i18n: `profile.master`, `profile.switcher.label`, `profile.switcher.trigger`, `profile.sync.*`,
  `profile.switch.*`. zh-TW keeps "profile" as a noun (as `hosts.undo_world_skipped` already did): master =
  「主要」, slave = 「本機 profile」.

### P3d-2 — As built (for P3d-3 / P3d-4)

Shipped as **three** PRs, not one (≤ 20 files each): **A** the section + the two *Profiles* blocks, **B** the
*Current* block + the switcher's entry, **C** the review fixes (F1–F3, the section labels, this text).
`components/settings/profile/` holds all of it; `SETTINGS_ORDER.PROFILE = 3` (last of the core band), section id
`profile`, route `/settings/profile`. `Settings › Sync` is untouched (it leaves with P3d-3).

**`ProfileSection.tsx`** — three blocks in this order: *Current*, *Profiles on this device*, *Profiles on the
sync host* (the last only while a master is attached). Opening the page writes no storage, starts no timer, and
with no master asks no host (pinned). It owns the ONE `useSotProfiles(hostId)` fetch and hands the master's SOT
**name** to the *Current* block — the snapshot only has the id.

**Current (`CurrentBlock.tsx` + `StopSyncControl.tsx`)**
- Sync state: `useProfileSync()` only. Preferences (`autoSync`, `masterEndpoint`, `pendingDetach`):
  `useProfileStore`. Host name / address: `useHostStore`. The master world (unsettled reason, workspace names):
  `readMasterWorld()` behind plain subscriptions to the three stores — NOT `subscribeMasterWorld`, which asks
  for a recovery rehydrate; a settings page must not. All of that lives in the half that is mounted only with a
  master; without one the block is two sentences and the `TODO(P3d-3)`.
- One `<section>`, three children in fixed places: the master-dependent half, `StopSyncControl`, the problem
  log. `StopSyncControl` is outside the first on purpose (below).
- The overall reading is `syncDotOf` (**`lib/profile/sync-view.ts`**, moved out of `ProfileSwitcher.tsx`
  unchanged, with `SYNC_DOT_CLASS`) — the switcher's dot and this page cannot disagree. `sync-view.ts` also has
  `settingsWaitForWorkspaces(status)` (true exactly when `settings` is `pending` and `workspaces` is `locked:*`;
  the FAILING case is P3d-4's, see the table) and `describeSections(keys, masterWorkspaces)`.
- **Sections are labelled for a person**: Hosts / Settings / Workspaces, then `Tabs · <workspace name>` in the
  workspace order — name and order from the MASTER world, never the live store (a slave on screen has another
  list, and a demoted master shares ids with it). A workspace not on this device yet, and a world nobody can
  read right now, have their own words; an id is never shown. `data-section` and the tooltip keep the raw key.
- A follower's figures carry a "reported by the window that is syncing" badge (state row and section list) and
  `data-source="leader"`; `stale` is a sentence. `blocked` ×3 and the unsettled reasons ×3 are sentences in a
  notice tone, never red. `Sync now` is disabled while `blocked` (no driver runs: the press would do nothing)
  and answers with a line that stays until the snapshot next changes (no timer).
- NOT shown, because the snapshot does not carry it: a revision per section (only a locked one has
  `locks[key].sot.rev`) and the time of the last sync → P3d-4.
- **Stop sync — what the outcome means.** `detachMaster()` now answers `DetachResult`: `{ ok: true }` or
  `{ ok: false, reason: 'daemon-not-told', detail }` (a 404 counts as told: the profile, and its attachments,
  are not there). This device has stopped syncing EITHER WAY — the master is cleared first. When the daemon was
  not told, start.ts writes `useProfileStore.pendingDetach = { hostId, profileId, endpoint, detail, at }`: persisted,
  synced across windows, **independent of the master** (survives `clearMaster`, moves no generation), cleared by
  a successful `retryPendingDetach()`, by `clearPendingDetach(hostId, profileId)` (the user gives up) or by
  attaching to that very profile again (`setMaster`; and a retry made while attached to it deletes nothing). It
  is written AFTER an `await`, so the store is re-read from storage first — a master another window set
  meanwhile is not written over. One slot: a second failure replaces the first.
  **The attachment is on ONE daemon (review F4).** `endpoint` is the master's `masterEndpoint`, read before
  `clearMaster` — the same idea, for the same reason: the api layer resolves a host's address on every request,
  and a retry that followed the host id to an edited address would remove this client from a profile of the same
  id on ANOTHER daemon. `dropAttachment(master, endpoint)` therefore sends nothing unless the host is there and
  still at that address (`endpointOfHost`, the one writer of the `"<ip>:<port>"` form — `useProfileStore.ts`),
  and answers `endpoint-changed` / `host-gone` / `endpoint-unknown` instead (`DetachResult`); `detachMaster`
  itself obeys it too (a master that is `blocked: master-endpoint-changed` is NOT told at the new address — it
  is remembered with the old one). A record without an endpoint (older than the field; dev builds only) is kept
  as `endpoint: null` and never sent; the setter refuses to write one. The notice (`data-state` =
  `retryable | endpoint-changed | host-gone | endpoint-unknown`) offers *Try again* only while `retryable`;
  otherwise it shows the address then and the address now, says what to do, and leaves *Dismiss*.
  **`detail` is a short reason, never a transcript**: the failure class plus the HTTP status (`timeout`,
  `server (HTTP 502)`), a thrown error by its `name`; cut at 120 code points by the store. Not the transport's
  message — it is persisted and shown, and nobody has checked it for a URL, a header or a body. (The
  `detach-failed` entry of the problem log now carries the same short reason.) The dialog stays up, `busy`,
  until the answer — which is why `StopSyncControl` is mounted outside the half that unmounts with the master.
  The notice (what happened, what it means for the other devices, *Try again*, *Dismiss*) is the store's: it is
  there after a reload and without a master. **Not covered:** the two best-effort drops inside `attachMaster`
  (switching master; an attach that was superseded) can leave the same ghost and still only log `detach-failed`
  — for P3d-3, whose wizard is what calls them. *(Done in P3d-3: both write `pendingDetach` — see "P3d-3 — As built".)*

**Profiles on this device (`LocalProfilesBlock` / `LocalProfileRow` / `ProfileAppearanceEditor`)**
- The master first (unnamed → `Home`, tagged `profile.master`), then `slaveOrder`. Reads the stores; the worlds
  are moved by `switch-active.ts` only. A **switch** is `useProfileSwitcherStore.chooseProfile` — the Home
  menu's own path (silent `busy` retries, its one toast); the store needed no change.
- Appearance through `setProfileAppearance`: name (the `DeviceNameField` draft pattern; what WILL be kept is
  previewed when it differs from what is typed; a slave's cannot be blank, the master's can → `Home`), icon
  (`WorkspaceIconPicker` inline in a `FloatingPanel`, as `HostIconField`; "use default" clears it), colour
  (`HOST_COLOR_PRESETS` + a `#rrggbb` field — `HostColorLayerEditor` does not fit: an alpha slider and a host's
  "Main" layer heading). **A name changed in another window while it is being edited here**: nothing typed →
  the input follows; a draft → kept, and the change is said, with Save (the user's own overwrite) and "Use
  that one". A profile deleted elsewhere closes its editor.
- **`profile-rules.ts` holds the page's DECISIONS, one edit each**: `COLOR_NEEDS_ICON` / `canTintProfile` —
  **confirmed by the user, 2026-09-22: an icon first, then a colour**: with no icon the profile shows the logo, a bitmap the colour cannot tint, so
  the colour control is disabled and says why, and a stored colour is KEPT; `false` offers the colour always.
  `defaultSlaveName(deviceName, taken)` — the device name with the next free number (a default never makes a
  duplicate; the number survives the 64 code point cut). `sotScopeOf` / `sotActionStillValid` (below).
- Delete (`ConfirmDialog`; never the one on screen — disabled, with the reason), ▲▼ (`reorderSlaves`), and below
  the list *Copy the master* / *Save what is on screen* (`ensureDefaultDeviceName()` is asked for at that click,
  never by opening the page). Every refusal reason has its own sentence; `unsettled` is in an info tone.
  `promoteToMaster` is not here (the wizard's step 3).

**Profiles on the sync host (`SotProfilesBlock` + `useSotProfiles`)**
- Loading / failed (with retry) / empty / rows; never polled. Rename; delete only where the FETCHED index shows
  no attachment, and never the profile this device syncs with; a 409 `attached` is rendered as the devices it
  names and the list is fetched again.
- **An action belongs to the master it was opened under** (review F1: a confirmation opened for `p2` on host A
  must not become "delete `p2` on host B" because another window moved the master). The scope is
  `sotScopeOf(hostId, attachedProfileId)`; every open action (confirmation, rename draft, refusal, status, busy)
  is dropped in the render that sees another scope; a send is checked again with `sotActionStillValid` — the
  scope it was OPENED under, and the profile still in the fetched list — and said when dropped; an answer that
  arrives after the scope moved sets nothing. `useSotProfiles` drops a previous host's late answer, shows
  `loading` (not the old list) until the new host answers, and sets nothing after unmount.

**The switcher** — a divider and a plain item `profile-item-settings` → `setLocation('/settings/profile')`
(`useRouteSync` opens the tab, as `TitleBar` does for `/settings/sync`). The menu still exists only with a slave.

**For P3d-3**: the wizard's entry goes at the `TODO(P3d-3)` in `CurrentBlock.tsx`'s no-master half (button,
testid `profile-setup-start`, key `settings.profile.current.setup`); the `no-parked-master` sentence in the
same file has a `TODO(P3d-3)` to link to the wizard; the SOT block shows only with a master — the wizard's
host picker is what lets it (or its list) exist without one. **For P3d-4**: the `TODO(P3d-4)` at
`profile-current-locked-note` is where the Resolve rows replace the sentence.

**test ids**
- section: `profile-section`
- *Current*: `profile-current-block` (`data-state` none|attached), `profile-current-master`, `profile-current-host`,
  `profile-current-state` (`data-state`, `data-source`), `profile-current-source`, `profile-current-stale`,
  `profile-current-blocked` (`data-reason`), `profile-current-world` (`data-reason`), `profile-current-schema`,
  `profile-current-settings-waiting`, `profile-current-sections`, `profile-current-no-status`,
  `profile-current-section-<key>` (`data-section` = the raw key, `data-status`, `data-source`),
  `profile-current-section-rev-<key>`, `profile-current-locked-note`, `profile-auto-sync`, `profile-sync-now`,
  `profile-sync-asked`, `profile-current-problems`, `profile-current-problem`
- Stop sync: `profile-stop-sync`, `profile-stop-sync-dialog` / `-cancel` / `-confirm`, `profile-detach-leftover`
  (`data-state`, `data-host`, `data-profile`), `profile-detach-retry` (`aria-busy`), `profile-detach-dismiss`,
  `profile-detach-retry-failed`
- local profiles: `profile-local-block`, `profile-local-status` (`data-tone`), `profile-row-<id>` (`data-master`,
  `data-on-screen`), `profile-row-name-<id>`, `profile-row-master-badge`, `profile-row-on-screen-<id>`,
  `profile-icon`, `profile-row-switch-<id>` (`aria-busy`), `profile-row-edit-<id>`, `profile-row-up-<id>`,
  `profile-row-down-<id>`, `profile-row-delete-<id>`, `profile-row-delete-blocked-<id>`, `profile-delete-dialog`
  / `-cancel` / `-confirm`, `profile-new-copy`, `profile-new-save`, `profile-new-form` (`data-kind`),
  `profile-new-name`, `profile-new-create`, `profile-new-cancel`, `profile-new-error` (`data-tone`)
- the editor: `profile-edit-<id>`, `profile-edit-name`, `profile-edit-name-save`, `profile-edit-name-preview`,
  `profile-edit-name-changed`, `profile-edit-name-take`, `profile-edit-icon`, `profile-edit-icon-default`,
  `floating-panel`, `profile-edit-color-<#hex>` (×8), `profile-edit-color-hex`, `profile-edit-color-none`,
  `profile-edit-color-needs-icon`, `profile-edit-error` (`data-reason`)
- SOT profiles: `profile-sot-block` (`data-state` loading|error|empty|rows), `profile-sot-refresh`,
  `profile-sot-loading`, `profile-sot-error`, `profile-sot-retry`, `profile-sot-empty`, `profile-sot-row-<id>`,
  `profile-sot-name-<id>`, `profile-sot-current-<id>`, `profile-sot-devices-<id>`, `profile-sot-rename-<id>`,
  `profile-sot-rename-input` / `-save` / `-cancel`, `profile-sot-delete-<id>`, `profile-sot-delete-blocked-<id>`,
  `profile-sot-attached-<id>`, `profile-sot-delete-dialog` / `-cancel` / `-confirm`, `profile-sot-status`
- the switcher: `profile-item-settings`
- i18n: `settings.section.profile`, `settings.profile.description`, `settings.profile.local.*` (+ `.error.*`),
  `settings.profile.sot.*`, `settings.profile.current.*` (+ `.blocked.*`, `.world.*`, `.schema.*`, `.section.*`
  with `:` → `_`, `.label.*`), `settings.profile.detach.*`, `profile.switcher.settings`

### P3d-3 — As built (for P3d-4)

`components/settings/profile/wizard/` — `ProfileWizard.tsx` (the order, the premises, the stop step, the run's
screen), `WizardChoiceSteps.tsx` (the three steps that only choose), `wizard-run.ts` (the run: no React),
`wizard-shared.ts` (button / notice classes, the master world as a subscription, reason → i18n key).

**Where it opens.** IN PLACE of the *Current* block's master-dependent half — not a dialog: no settings page has
a user-confirmed multi-step flow to copy (Peers › Pair is an automated flow with a step line), and the design
rules forbid a new primitive. `CurrentBlock` owns `wizardOpen` (component state: leaving the page closes it,
nothing is persisted, and it does not follow the master — the wizard's first step clears the master and its last
sets one). Two ways in: `profile-setup-start` (no master) and `profile-setup-change` (attached; the same wizard,
from its first step). While it is open the plain *Stop sync* row is not offered beside it; `StopSyncControl`
stays mounted, because the "host was not told" notice is the store's and the wizard must not hide it.

**The steps, and what each one checks before it is shown** (`brokenPremise`, run on every store change and again
at every click that moves on — never while a run is under way; the wizard goes back to the latest step that
still stands and says why, `profile-wizard-notice`):

| # | step | does | premise (else → where, `data-reason`) |
|---|---|---|---|
| — | refused | nothing. `isClientIdPersisted()` false → `client-id`; `readMasterWorld()` `junk-epoch` / `no-parked-master` → that reason. Checked once, when the wizard opens. | — |
| 1 | `stop` (only with a master) | `detachMaster()` on the button — the call *Stop sync* makes. `{ ok: false }` → the step STAYS, says it (`profile-wizard-stop-not-told`), and *Continue* is the user's | a master is attached (else → `sot`, `stopped-elsewhere`) |
| 2 | `sot` | host `<select>` (dev host if connected, else the first connected; others listed, disabled, "— not connected"); `useSotProfiles` (now carries the failure's `reason`) loading / error + retry / empty / rows; *A new profile* — name required, default = device name — is created (`createProfile`) when the step is confirmed | no master (else → `stop`, `attached-elsewhere`) |
| 3 | `local` | the master + every slave, the master chosen by default, the one on screen badged; the consequence of a move in words. Nothing is promoted here | + host still in the store and connected (else → `sot`, `host-gone` / `host-offline`); Next is disabled while the world is catching up |
| 4 | `direction` | push / pull, nothing pre-chosen for an existing profile. **Push only while the profile is EMPTY as last seen** (`seen.empty` — at step 2's confirm, or as `prepareRun` found it since); "this visit created it" only picks the wording (`pull_new` / `pull_empty`), it is never by itself the reason | + the chosen slave still exists (else → `local`, `local-gone`, master chosen) |
| 5 | `run` | the sub-steps as a list, all `pending`, and *Start*; nothing has been done before it is pressed | the same — and *Start* (and every *Try again*) goes through `prepareRun`, below |

There is no way to a step but through the one before it: the step list is `<li>` text, *Next* is the only door,
*Back* exists on 3, 4 and the not-yet-started 5.

**`no-parked-master` — the plan was wrong to send it to the wizard.** `promoteToMaster` and `copyMasterAsSlave`
both refuse an unsettled world, and this state does not settle by waiting; only the store's `merge` (a reload)
repairs it. The wizard therefore refuses to start and says so; the *Current* block's sentence now ends with
"Reload the app to repair it", and its `TODO(P3d-3)` became the explanation. `junk-epoch`: refused likewise,
"switch profile once". The three transient reasons do not refuse: step 3 waits for them.

**Step 5 — the order is `promote → copy → attach`, and the copy is `copyMasterAsSlave`, never
`saveScreenAsSlave`.** A pull replaces THE MASTER's world as it is when the attach is made — after the promote —
which is not "what is on screen":

| on screen | chosen as master | world the pull replaces | primitive, when | what `saveScreenAsSlave` would have kept |
|---|---|---|---|---|
| master | the master | the master's = the screen | `copyMasterAsSlave`, before the attach (no promote) | the same world |
| master | a parked slave S | S's (the parked master by then) | `copyMasterAsSlave`, AFTER the promote | **the old master** — which the promote has just kept as a slave anyway; S would be lost |
| slave S | S | S's = the screen (labelled master by then) | `copyMasterAsSlave`, after the promote | the same world |
| slave S | the (parked) master | the parked master's | `copyMasterAsSlave`, before the attach (no promote) | **S** — which the pull never touches |
| slave S | a parked slave T | T's (the parked master by then) | `copyMasterAsSlave`, after the promote | **S** — likewise |

One primitive at one point in time is right in every row because "the master's world, wherever it is" is by
construction what the attach hands to the executor. Pinned row by row against the REAL switch-active.ts
(`wizard-run.test.ts`: at the moment `attachMaster` is called, the master's sentinel and the copy's sentinel are
the same) and end to end (`ProfileWizard.integration.test.tsx`). The counts step 4 shows are of that same world
(`worldToBeMaster`). The demoted master is named after the device and never like the copy.
A sub-step that fails STOPS the run (`runPlan`): nothing after it is attempted, nothing before it is undone, and
the screen lists each sub-step's state plus what that means now. *Try again* re-runs from the failed sub-step
only; a reason no retry cures (`master-attached`, `not-found`, `superseded`, `unknown-host`, …) offers *Start
over* instead — the wizard from its first step, from the state as it is then.

**`attachMaster`'s reasons** (start.ts): its own refusals `invalid-direction`, `client-id-not-persisted`,
`unknown-host`, `invalid-profile-id`; `superseded`; the PUT's `FailureReason` — `network`, `timeout`, `aborted`,
`contended`, `not-found`, `too-large`, `rejected`, `unauthorized`, `server`, `malformed` (`unknown-host` again);
**and, when the PUT throws, that error's `message`**. `ATTACH_REASONS` is the closed list of the first fifteen,
each with its own sentence (`settings.profile.wizard.attach.*`); anything else leaves `wizard-run.ts` as `other`,
and `reasonKey` / `requestKey` are closed lists too — no `Error.message`, transport message or body is shown or
stored. `write-failed` is shown without its `detail`.

**Before anything irreversible: `prepareRun(draft)` — ONE door, in `wizard-run.ts` (review F1).** It answers a
frozen plan or the reason there is none; the component only presents the answer. It checks this device's
premises (`brokenLocalPremise`: no master attached · the host in the store and connected · the chosen slave
still there — before the host is asked AND again after it answered), then RE-LISTS the host's profiles and
compares the chosen one's fingerprint — every live section's `[section, rev, hash]`, sorted; the index carries
them, no payload is fetched, and a deleted section is simply one that is no longer listed (the index has no
tombstone flag) — with the one captured when the user confirmed step 2 (`seen`). The attack it closes: a profile
seen EMPTY is offered push only, with no "replaces what is there" warning; another device pushes a whole world
into it; *Start* would have overwritten that in silence. Now: `profile-changed` → nothing runs, back to the
direction step, the direction UN-chosen, what is offered recomputed from what is there now (both directions and
the warning), notice `profile-changed` / `profile-emptied`; `profile-gone` → back to step 2; `list-failed` →
nothing runs, `profile-wizard-check-failed` says why (a sentence by failure class), *Start* stays. A retry asks
again (`promoted = true` once the promote went through: the chosen slave is the master by then and is not looked
for among the slaves; a `profile-changed` then also re-points the choice at the master, so the next plan does not
promote twice).
**What is left, and why no rev is handed down.** `attachMaster` takes no rev and the executor could not use one:
under `push` it answers every conflict of the first reconciliation with keep-local — each PUT is a CAS on the rev
its OWN index read gave, rebased and re-sent on a 409 (executor.ts, THE FIRST RECONCILIATION). A push is thus an
unconditional overwrite for as long as that reconciliation lasts. The window that remains is from the re-list to
the end of the first reconciliation; a device writing into the profile in those seconds is overwritten, which is
what the user was told a push does. (Under `pull`, an EMPTY SOT takes nothing and the ordinary rules push — so
"pull from empty" would be harmless, but it is still not offered: it would say the opposite of what happens.)

**A create whose outcome is not known is never simply sent again — `createSotProfile` (review F2).** The daemon
gives every POST a new id and allows equal names. Unknown = `timeout` / `network` / `aborted` / thrown — and
`server` / `malformed` too (a 5xx or an unreadable 2xx may follow the commit; counting them in costs one list
request). Then the host is listed and OURS is looked for: same name, no section, no device attached, **and an id
that is not in the BASELINE** — the ids the host had listed, in this wizard visit, before the visit's FIRST POST
to that host (kept in a ref, set once per host; ids, not `createdAt`: the daemon's clock is not this device's).
Found (the newest, if an earlier lost attempt left one too) → adopted, notice `create-adopted`. Not found →
`not-created`, said. The list cannot be read → `unknown`; from any non-definite outcome on, the next press for
that host + name LOOKS FIRST and sends nothing until a list has been read. No baseline (the list had never been
read when *A new profile* was chosen): ours cannot be told from one that was always there → `same-name`: neither
adopted nor doubled; the list is reloaded and the user picks it or renames. A definite refusal (`rejected`,
`unauthorized`, `too-large`, `contended`, `not-found`, `unknown-host`) is just that.

**The result is a toast, too — `announceRun` (real-machine acceptance F6).** The Settings tab belongs to the
master's world and a pull REPLACES that world: page, wizard and "done" line unmount when the first section is
applied. A finished run is therefore always said through `useUndoToast` (no world owns it; the switcher's
precedent) — `toast.done`: *Now syncing with “X” on H.* / `toast.done_saved`: *… The workspaces and tabs this
device had are kept as the local profile “Y”.* — a push's as well, beside the "done" line. A FAILED run is a
toast only when the wizard is gone (`toast.stopped_promote` / `_save` / `_attach`: which step, never why). The
run's promise outlives the component: it announces, and sets state only while mounted.

**`useProfileStore.pendingDetaches` — a keyed LIST (review F3); it was one slot, `pendingDetach`.** Master A→B
with A's drop failing, then B→C with B's failing, wrote B over A and A's ghost was never mentioned again. Now:
`PendingDetach[]`, oldest first, at most one per `pendingDetachKey` = `JSON.stringify([endpoint, hostId,
profileId])`; `addPendingDetach` merges (same key → replaced in place, else appended), capped at
`PENDING_DETACH_MAX` = 20 (oldest dropped); `clearPendingDetach(key)`, `retryPendingDetach(key)` and a re-attach
(`setMaster`: the record of that very endpoint + host + profile) each remove ONE record — a same-id profile on
another daemon stays a ghost there. start.ts still re-reads storage before it writes, so the merge is onto what
storage holds. **Migration:** `merge` reads `pendingDetaches` record by record and then alpha.420's single
`pendingDetach` object, added as a record unless its key is already listed; the old key is not written back.
`StopSyncControl` renders one notice per record (`LeftoverItem`, its own retry state), each inside
`profile-detach-item-<pendingDetachTestId>` (`<host>.<profile>.<endpoint>`, every other character → `_`); the
existing `profile-detach-*` ids are on every notice.

**start.ts — the two best-effort drops inside `attachMaster` now write `pendingDetaches`.** *Switching master*:
the DELETE goes to the address the OLD attachment was made at (`masterEndpoint`, read before anything else —
`dropAttachment(previous, previousAt)`, so a re-pointed host is not told at its new address), and a drop that did
not get through is remembered AFTER `setMaster` (before it the old master still IS the master, and
`rememberPendingDetach` keeps nothing about the master). *Overtaken by another window*: there is no older address
to compare with, but there is the one the PUT was just sent to — read in the same turn as the PUT (`putAt`); the
take-down goes there or nowhere and is remembered with it. If the host had no address to read, nothing can be
remembered (the setter refuses a record without one) and it stays a `detach-failed` problem. The wizard itself
never switches master without detaching first; the dev hook still can.

**`Settings › Sync` left the sidebar — except for whoever still needs it (review F4).** Contributions gained
`visible?: () => boolean` (`settings-contribution-types.ts`; applied in `listContributions`, so a hidden section
has no row AND no route; false or throwing → hidden; read at the shell's next render — nothing subscribes). The
`sync` module still declares its section, visible while `activeProviderId !== null || pendingConflicts.length >
0 || pendingRemoteBundle !== null`: pending conflicts and a pending bundle are persisted and only `SyncSection`
resolves or dismisses them; a provider that is on means the user is using the old Sync (every sync is a button
on that page), and the page is where it is switched off — after which, with nothing pending, the entry is gone.
Everybody else sees no Sync entry. `TitleBar`'s conflict icon is back unchanged: its predicate is a subset of
that condition, so the click always lands. What only that page offers (provider, sync host, *Sync now*,
per-contributor toggles, `.purdex-sync` export / import, conflict resolution, snapshot history) is unreachable
for a user with the provider off and nothing pending; **none of it runs by itself** — `syncNow`, `push` and
`applyImport` have no caller but that page — so nothing is left running that cannot be stopped. P4a deletes it.

**test ids**
- entry: `profile-setup-start`, `profile-setup-change`
- shell: `profile-wizard` (`data-step` = refused|stop|sot|local|direction|run), `profile-wizard-close`,
  `profile-wizard-steps`, `profile-wizard-step-<stop|sot|local|direction|run>` (`data-state` current|done|todo),
  `profile-wizard-notice` (`data-reason`), `profile-wizard-refused` (`data-reason`), `profile-wizard-back`,
  `profile-wizard-next` (`aria-busy` while creating)
- stop: `profile-wizard-stop-confirm` (`aria-busy`), `profile-wizard-stop-not-told` (`data-reason`),
  `profile-wizard-stop-continue`
- sot: `profile-wizard-host`, `profile-wizard-host-option-<hostId>`, `profile-wizard-host-none`,
  `profile-wizard-profiles` (`data-state` loading|error|empty|rows), `profile-wizard-profiles-loading`,
  `profile-wizard-profiles-error` (`data-reason`), `profile-wizard-profiles-retry`, `profile-wizard-profiles-empty`,
  `profile-wizard-profile-<profileId>`, `profile-wizard-profile-new`, `profile-wizard-new-name`,
  `profile-wizard-create-error` (`data-reason`)
- local: `profile-wizard-local-<master|slaveId>`, `profile-wizard-local-on-screen-<id>`,
  `profile-wizard-local-consequence`, `profile-wizard-local-world` (`data-reason`)
- direction: `profile-wizard-direction-push`, `profile-wizard-direction-pull`, `profile-wizard-pull-unavailable`,
  `profile-wizard-push-warning`, `profile-wizard-pull-replaces`, `profile-wizard-save-first`,
  `profile-wizard-save-name`, `profile-wizard-save-name-error`, `profile-wizard-no-copy-warning`
- run: `profile-wizard-summary`, `profile-wizard-substep-<promote|save|attach>` (`data-state`
  pending|running|done|failed), `profile-wizard-start`, `profile-wizard-failure` (`data-step`, `data-reason`),
  `profile-wizard-retry`, `profile-wizard-restart`, `profile-wizard-done`, `profile-wizard-checking`,
  `profile-wizard-check-failed` (`data-reason`)
- added by the review fixes: `profile-wizard-create-error` also carries `data-outcome`
  (failed|not-created|unknown|same-name); `profile-wizard-notice` reasons + `profile-changed` /
  `profile-emptied` / `profile-gone` / `create-adopted`; `profile-detach-item-<host>.<profile>.<endpoint>`
- i18n: `settings.profile.current.setup` / `.change` / `.change_desc`, `settings.profile.wizard.*` (`.step.*`,
  `.refused.*`, `.notice.*`, `.stop.*`, `.sot.*`, `.request.*`, `.local.*`, `.direction.*`, `.run.*` +
  `.run.state.*`, `.now.*`, `.promote.*`, `.save.*`, `.attach.*`, `.toast.*`)

**For P3d-4.** The Resolve rows still go at `TODO(P3d-4)` (`profile-current-locked-note`), in `Attached` — which
is not mounted while the wizard is open, so the two never show together. A `profile-gone` master's one way out is
now reachable: `profile-setup-change` → stop → choose again; the *Current* block's `profile_gone` sentence may
point at it. The acceptance run drives the ids above; "wizard → existing → pull with save as slave" produces a
slave named after the device (`Laptop`, `Laptop 2`, …) holding the pre-pull MASTER world. The SOT block still
shows only with a master; the wizard's step 2 is the only list without one. Not done here: a way to delete the
empty profile a user created in step 2 and then abandoned (it is listed next time, push-only because it holds
nothing; deletable from the SOT block once attached to another).

### Task 6 — `components/ProfileSwitcher.tsx`
Portal-based menu anchored to the Home button (both bars), `role="menu"` with arrow keys, Home/End,
Enter, Escape, outside-click, focus returned to the button — the first real menu primitive in the
repo, kept in `components/Menu.tsx` so it can be reused. Items: the master (badge + the sync state
dot from Task 2; absent when there is no master), the slaves, a divider, `Settings › Profile`.
Choosing one calls `switchActiveProfile`; `busy` shows a toast. ~~With no master and no slaves the
menu has one item, `Set up sync…`~~ **As built, and confirmed by the user on 2026-09-22: with no slave there is no menu at all** — the Home
button does what it always did, and the discoverable entry is Settings › *Profile* in the sidebar
(`Set up sync…` goes into its *Current* block with P3d-3). Why: a menu on every user's Home click
contradicts the iron rule ("a user who never touched the feature sees today's app"), and the iron
rule is the one that was promised to the user.
The wide bar's row keeps its label and gains a chevron; the overloaded expand toggle goes (there is
no tab list under it after P3c).

### Task 7 — `Settings › Profile` (`components/settings/profile/`), purdex scope, built-in band
`SETTINGS_ORDER.PROFILE` in the core band. Four blocks (spec §4.9):
1. **Current** — master name, host, per-section state and revision (Task 2; a follower shows the
   leader's, labelled), last sync, **Auto-sync** toggle, **Sync now**, **Stop sync** (confirm).
   `blocked` reasons in words: endpoint changed (with the two addresses), profile gone, suspended.
2. **Wizard** — one confirmed step at a time, **the five of decision 10 in its order**: *stop sync*
   (if a master is attached) → *pick the SOT profile*, existing or new, on the chosen host (the dev
   host by default; `listProfiles` / `createProfile`) → ***pick which local profile becomes the
   master*** — the one on screen or any slave; choosing a slave runs `promoteToMaster` (a move: the
   previous master world becomes a slave; there is no "copy as master") (review #8, critical — the
   first draft had dropped this step) → *direction* —
   **a new profile offers push only**; **pull first shows what will be replaced, offers to save the
   current screen as a slave named after this device (default on, decision 12), and only then
   calls `attachMaster(…, 'pull')`** → done. Refuses to start when `isClientIdPersisted()` is false,
   with the reason. Every `attachMaster` failure reason has its own sentence.
3. **Profiles** — slaves: copy master as slave, rename, delete (not the one on screen). SOT
   profiles on the master's host: rename, delete — the delete button is enabled only when the
   *fetched* index shows no attachments, and a 409 `attached` answer is rendered as the list of
   devices still attached.
4. **Resolve** — one row per locked section: `locked:conflict` → `Keep local` / `Take SOT` with a
   summary in counts (tabs, workspaces, hosts, settings keys — not a diff); `locked:invalid` → why,
   and `Keep local` only; `locked:reset` and profile-level `locked:schema` → the explanation and the
   one way out. All through Task 2's `requestResolve`.
**`Settings › Sync` leaves the sidebar in this PR** (review #10: the spec says *replaces*, and two
sync entry points with different stores behind them is not an IA anyone chose). The Sync module's
`settings` contribution is dropped; the module, its engine and `SnapshotHistoryPage` stay until P4a
deletes them. `TitleBar.tsx:31`'s icon predicate, which mirrors the Sync banner, goes with it.
*(As built, after review F4: the section stays DECLARED with a `visible()` — listed only while the old Sync has
something pending or is switched on — and the icon stays with it. See "P3d-3 — As built".)*

### Task 8 — i18n (en + zh-TW, every key in both, placeholders paired) and PRODUCT.md §3.9 Profile
(master / slave / active / SOT in the vocabulary's own format; the PR body lists the IA, visual and
interaction impact §7 of that file asks for).

### Acceptance for P3 (real machine — run, not assumed)
Two Playwright contexts against the mlab daemon, **through the UI, not the dev hook**: wizard → new
profile → push on A; wizard → existing → pull on B with "save as slave" → B has a slave holding its
old world; switch B to the slave, edit it, **nothing reaches the daemon** while the master keeps
receiving A's edits into its parked world; switch back and see them; resolve a real conflict from
the panel in a *follower* window; stop sync; delete the profile once both are detached.

## P3e — the new-tab "profile" becomes "layout preset" (may slip)
Identifiers, file names, i18n keys and UI copy (22 files). The persisted field
`purdex-newtab-layout.profiles` → `presets` with a `version: 2` `migrate`, the projection path with
it, and `SECTION_SCHEMA_ORDINAL.settings` → 4. If the night runs out it becomes an issue: PRODUCT.md
§3.9 then records the collision explicitly.

## Risks
- **P3b's indirection is the dangerous part**: one missed call site and a slave's workspaces reach
  the SOT. Task 4's test asserts it structurally — with a slave on screen, every section the
  collector reports hashes equal to what it reported for the parked master, whatever is done to the
  slave.
- **P3c is a behaviour change outside Profile Sync**; its PR says so first thing.
- A follower acting on a published status that is a few hundred ms old: commands carry the
  conflict they answer, so a stale click is a no-op, not a wrong answer.
