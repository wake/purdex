# Plan — Profile Sync P3: slaves, the switcher, Settings › Profile (five PRs)

- Spec: `2026-09-20-profile-sync-spec.md` §4.1 (master / slave / active), §4.3, §4.9, §9.9–§9.11; the
  user's decisions 9–13. The driver's contract is `spa/src/lib/profile/start.ts` and the "as built"
  sections of `2026-09-20-profile-sync-p2b-plan.md`.
- Worktree `.claude/worktrees/profile-sync`, based on `origin/main` alpha.414.
- **Five PRs**, each ≤ 20 files, merged in order; each leaves the app working:
  **P3a** foundations (no UI) → **P3b** slaves and the active pointer → **P3c** standalone tabs removed
  → **P3d** the UI → **P3e** the new-tab "profile" rename (may slip to an issue).
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
- **Commands reach the leader the same way**: `requestSyncNow()` / `requestResolve(section, keep)`
  act locally in the leader; in a follower they append `{id, kind, section?, keep?, at}` to
  `localStorage['purdex-profile-commands']`; the leader consumes and removes them (ids make a
  re-delivery harmless; entries older than 30 s are dropped unexecuted — a stale "Keep local" must
  not fire minutes later against a different conflict). `resolve` also carries the conflict it was
  answering (`sot.rev`), and the leader ignores it if the section's conflict has moved on.
- `hooks/useProfileSync.ts`: `useSyncExternalStore` over the above.

## P3b — slaves and the active pointer

### Task 3 — `stores/useLocalProfilesStore.ts`
Device-local, persisted (`purdex-local-profiles`, `syncManager`-registered so every window agrees on
which profile is on screen):
```ts
interface ParkedWorld { workspaces: Workspace[]; tabs: Record<string, Tab>; activeWorkspaceId: string | null; activeTabId: string | null }
interface LocalProfile { id: string; name: string; createdAt: number; world: ParkedWorld | null }  // world === null ⇔ this one is on screen
slaves: Record<string, LocalProfile>; slaveOrder: string[]
activeProfileId: 'master' | string            // 'master' also when there is no master: the live world
parkedMaster: ParkedWorld | null              // the master's world while a slave is on screen
```
Invariant: **exactly one world is on screen and has `world === null` / `parkedMaster === null`;
every other is parked.** `merge` sanitises anything else back to "master on screen".

### Task 4 — `lib/profile/master-world.ts`: where the master's tab world lives *right now*
```ts
export function readMasterWorld(): { workspaces; tabs; tabOrder }     // live stores, or parkedMaster
export function writeMasterWorld(next, afterWrite?): void             // commitTabWorld, or the parked copy
export function subscribeMasterWorld(fn): () => void                  // live stores + the local-profiles store
```
`collector.ts` and `apply-to-stores.ts` go through it for `workspaces` and `tabs.*` (hosts and
settings are always live — a slave borrows them, decision 9). With the master on screen nothing
changes; with a slave on screen the collector hashes the parked master (so **a slave's workspaces
can never be built into a section**) and an inbound apply updates the parked copy without touching
the screen. `commitTabWorld` is exported from `apply-to-stores.ts` for Task 5.

### Task 5 — `lib/profile/switch-active.ts`
`switchActiveProfile(targetId)`: under `withOperationLock('profile-switch')`, in **one synchronous
block**: park the on-screen world into its slot → take the target's parked world →
`commitTabWorld(target)` → set `activeProfileId`; any throw restores all of it. Refused (`busy`)
when the lock is held or a terminal-affecting operation is running. Also: `copyMasterAsSlave(name)`
(the only copy operation, decision 10 — fresh tab and workspace ids via the existing
`cloneTabWithFreshIds`, or a slave's tab ids would collide with the master's when both exist in
memory), `renameSlave`, `deleteSlave` (never the one on screen), and `saveScreenAsSlave(name)` — what
the wizard calls before a pull (decision 12).
The executor's `pull`-direction reconciliation and every apply go through Task 4, so none of this
changes the driver's contract.

## P3c — standalone tabs removed (§4.3)

**Every tab belongs to exactly one workspace.** This is the one PR that changes the app for a user
who never opens Settings › Profile; both of the user's machines have zero standalone tabs (spec §3.1).
- **Boot adoption**: `main.tsx`, before the first render, runs `adoptStandaloneTabs` (already in
  `lib/profile/sections.ts`) over the live stores — into a workspace named `t('workspace.unsorted')`,
  created only if there is something to adopt. Idempotent; logged once.
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
2. **Wizard** — one confirmed step at a time (decision 10): pick the host (the dev host by default)
   → pick the SOT profile, existing or new (`listProfiles` / `createProfile`) → pick direction —
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
`SyncSection` is left registered until P4a removes the module; its sidebar entry gains "(legacy)".

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
