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
| **P3d-1** | the Home button opens a menu | `components/Menu.tsx` (the primitive) + `ProfileSwitcher` in both bars + its i18n. With no master and no slaves the menu is `Set up sync…` (→ Settings › Profile, which until P3d-2 is the existing Settings page — so the item is hidden until P3d-2 lands; the menu then has nothing to show and the button keeps today's behaviour. **No dead control between merges.**) |
| **P3d-2** | Settings › Profile exists: *Current* + *Profiles* | the section registration (`SETTINGS_ORDER.PROFILE`), the *Current* block (read-only state, Auto-sync, Sync now, Stop sync) and the *Profiles* block (slaves: copy master / save screen / rename / delete; SOT profiles: rename / delete). A master can still only be attached through the dev hook. |
| **P3d-3** | a user can attach a master without the dev hook | the *Wizard* (decision 10's five steps). `Settings › Sync` leaves the sidebar **here**, not earlier: until the wizard exists it is the only sync UI a user has. |
| **P3d-4** | conflicts can be resolved from the UI | the *Resolve* block, PRODUCT.md §3.9, and the real-machine acceptance of the whole of P3 (below), **through the UI**. |

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
`data-testid` (acceptance drives the UI). Nothing renders for a user with no master and no slaves
except the one `Set up sync…` entry and an empty *Current* block that says what Profile Sync is in
two sentences and offers the wizard.


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
- **The switcher exists only where there is a slave.** `useProfileSwitcherTrigger(onSelectHome)` →
  `{ enabled, open, onClick, currentName, onSlave, triggerProps }`, used by `HomeRow` (chevron, `bottom-start`)
  and `ActivityBarNarrow` (`right-start`). With no slave both buttons are exactly what they were, and
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
- **The Home button says which world is on screen — only where there is a slave.**

  | | wide (`HomeRow`) | narrow |
  |---|---|---|
  | no slave | `Home`; no `aria-label`, no `title` | `title="Home"`; no `aria-label`, no marker |
  | slave exists, master on screen | label + `title` = `t('profile.master')` | `title="Home"`, no marker |
  | a slave on screen | label (CSS-truncated) + `title` = its name | `title` = its name + `home-profile-marker` |

  With a slave the trigger's `aria-label` is `t('profile.switcher.trigger', { name })` ("Profile: Scratch") in
  both bars. The marker is a 7 px `bg-accent` dot at the icon's bottom-right — not the unread badge's corner,
  colour or shape — and it means one thing: what is on screen never syncs.
- test ids: `home-button`, `home-label` (wide), `home-switcher-chevron` (wide), `home-profile-marker` (narrow),
  `profile-switcher-menu`, `profile-item-master`, `profile-item-<slaveId>`, `profile-sync-dot` (`data-state`);
  busy = `aria-busy="true"` on the item.
- i18n: `profile.master`, `profile.switcher.label`, `profile.switcher.trigger`, `profile.sync.*`,
  `profile.switch.*`. zh-TW keeps "profile" as a noun (as `hosts.undo_world_skipped` already did): master =
  「主要 profile」, slave = 「本機 profile」.

### Task 6 — `components/ProfileSwitcher.tsx`
Portal-based menu anchored to the Home button (both bars), `role="menu"` with arrow keys, Home/End,
Enter, Escape, outside-click, focus returned to the button — the first real menu primitive in the
repo, kept in `components/Menu.tsx` so it can be reused. Items: the master (badge + the sync state
dot from Task 2; absent when there is no master), the slaves, a divider, `Settings › Profile`.
Choosing one calls `switchActiveProfile`; `busy` shows a toast. With no master and no slaves the
menu has one item, `Set up sync…` — the default path stays today's app plus one discoverable entry.
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
