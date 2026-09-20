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
- **The leader publishes, followers read.** The leader writes `{at, leader: windowId, status,
  blocked, problems}` to `localStorage['purdex-profile-status']` on change (throttled to one write
  per 250 ms, trailing); every window listens to the native `storage` event for that key. A
  follower's snapshot is the published one, marked `remote: true`; older than 10 s with no leader
  lease → `stale: true`. Cleared when the master is cleared.
- **Commands reach the leader the same way, one key per command** (review #5: an array in one key
  is read-modify-write and loses commands by construction — two followers both read `[]`; the leader
  clears what a follower has just appended): `localStorage['purdex-profile-cmd:<id>'] = {kind,
  section?, keep?, conflict?, at}`. The leader executes and removes the key; a command older than
  30 s is removed unexecuted. Two leaders in a hand-over may both execute one — harmless, because
  `resolve` on a section that is no longer locked and `syncNow` are both no-ops in the reducer.
- **A `resolve` is bound to the conflict the user was looking at** (review #6): it carries
  `{localHash, sot: {rev, hash}}` and the leader refuses it unless the section's current conflict is
  that one. `sot.rev` alone is not enough — the local side can change under an unchanged revision,
  and `Keep local` would push content nobody confirmed.
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

## P3d — the UI

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
