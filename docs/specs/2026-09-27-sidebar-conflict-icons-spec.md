# Sidebar conflict icons — spec (2026-09-27)

User request (zh-TW, paraphrased): surface Profile Sync locks in the sidebar.
1. The profile block at the top of the sidebar (Home row) shows a conflict icon; clicking it shows the conflict info.
2. Every workspace row shows a conflict icon when its tabs are locked; clicking it shows the info AND lets the user
   resolve it right there. When the lock is gone, the icon disappears.

Decisions taken with the user (do not re-open):
- D1 The profile popover is **info only**: `settings` / `workspaces` rows link to Settings › Profile; `tabs.<ws>`
  rows lead to the workspace row's panel (when that workspace is on screen). Resolving happens in the workspace
  panel and in Settings › Profile, never in the profile popover.
- D2 While a **slave** is on screen, workspace rows never show the icon (the sidebar's workspaces are the slave's;
  a demoted master shares ids, so matching by id would lie). The Home row icon still shows (locks are the
  master's). The master item in the Home switcher menu also carries the icon.
- D3 **Wide sidebar only.** The narrow activity bar is out of scope.

## 1. What counts

"Needs attention" = the master's `status.locks` (ExecutorStatus) has at least one entry — `locked:conflict`,
`locked:reset` or `locked:invalid`. Profile-level states are NOT counted and keep their existing surfaces:
`locked:schema` (`status.schemaLock`), a gone profile (`profileIsGone`), `blocked`.

- Source: `useProfileSync()` only. No new state is stored; the icon is a pure reading of the snapshot, so it
  disappears the moment the executor (or the leader, in a follower window) drops the lock.
- Follower windows read the leader's published status and show the same icons. A **stale** follower still shows
  them (a lock is a fact, not a promise — same rule as CurrentBlock).
- No master → nothing (the profile popover and all icons are absent).
- The profile is gone (`profileIsGone`) → nothing: the executor keeps its last sections/locks but none of them can
  be resolved; Settings › Profile already says why.

## 2. One reading: `lib/profile/conflict-view.ts` (new, pure)

```ts
export interface LockView { key: string; kind: SectionView['kind']; workspaceId: string | null; status: SectionLock['status'] }
/** Every lock of the master, in `describeSections` order ([] without a master / status / when the profile is gone). */
export function locksOf(sync: ProfileSyncSnapshot): LockView[]
/** The lock of `tabs.<workspaceId>`, or null. */
export function tabsLockOf(sync: ProfileSyncSnapshot, workspaceId: string): SectionLock | null
```
Order uses `describeSections(keys, null)` (names are not needed for ordering beyond the fixed order; tabs
sections fall back to key order — the popover renders names separately via the master world).

## 3. Home row (profile block)

- `HomeRow` renders, when `locksOf(sync).length > 0`, a button **outside** the switcher trigger button (so a click
  never opens the switcher / selects Home), placed between the label button and the row's end:
  Phosphor `WarningCircle` size 14, `text-amber-500`, `data-testid="home-conflict-button"`, `aria-label` /
  `title` = `profile.conflict.button` ("{count} sync conflicts" / 「{count} 個同步衝突」).
- Click toggles a `FloatingPanel` (`testId="profile-conflict-panel"`, title `profile.conflict.title`) anchored to
  that button. Content: one line per `LockView`:
  - label: same wording as Settings › Profile's section labels (`settings.profile.current.label.*`), with the tabs
    workspace name taken from the MASTER world (`readMasterWorld()`), never the live store;
  - reason: `settings.profile.resolve.why.conflict|reset|invalid.<reason>` (invalid reason from
    `status.detail[key].invalidReason`, fallback `unknown`);
  - action:
    - `settings` / `workspaces` / `other` → button "Resolve in Settings" (`profile.conflict.open_settings`) →
      `setLocation('/settings/profile')`, closes the panel;
    - `tabs.<ws>` and the master is on screen (§5) and the workspace exists in the live store → button "Show"
      (`profile.conflict.show_workspace`) → `useConflictPanelStore.openFor(wsId)`, closes this panel;
    - `tabs.<ws>` while a slave is on screen → text `profile.conflict.on_master` + button "Switch to master"
      (`profile.conflict.switch_to_master`) → `useProfileSwitcherStore.getState().chooseProfile(MASTER_PROFILE_ID)`;
    - `tabs.<ws>` with the master on screen but no such workspace (SOT holds tabs of a workspace this device has
      not taken) → the Settings button.
- The panel closes itself when the lock list becomes empty.

## 4. Home switcher menu

The master entry's `trailing` gets, next to the existing sync dot, a `WarningCircle` size 12 amber
(`data-testid="profile-item-conflict"`, `aria-label` = `profile.conflict.button`) when `locksOf(sync).length > 0`.
Display only; no handler.

## 5. Workspace row

- "Master on screen" = `useLocalProfilesStore.activeProfileId === MASTER_PROFILE_ID` AND `readMasterWorld().settled`.
  Only then does a row look up `tabsLockOf(sync, workspace.id)`.
- With a lock: a button in the header after the name (before the + / chevron), Phosphor `WarningCircle` size 14
  amber, always visible (not hover-only), `data-testid="ws-conflict-button-<wsId>"`, `aria-label`/`title` =
  `profile.conflict.workspace_button` ("Sync conflict in {name}"). Its pointer-down/click must not start the
  dnd-kit drag nor select the workspace (`stopPropagation` on click and pointerdown).
- Click toggles a `FloatingPanel` (`testId="ws-conflict-panel-<wsId>"`, title = `profile.conflict.workspace_title`
  with the workspace name, width 360) anchored to the button, content: `<ul>` with the existing `ResolveRow`
  (sectionKey `tabs.<wsId>`, kind `tabs`, label = `settings.profile.current.label.tabs`, lock, invalidReason,
  `fromLeader = sync.remote`, `disabled = sync.blocked !== null`). `ResolveRow`/`useResolveContext` are reused
  UNCHANGED — the freeze/check/TTL contract stays in one place.
- Open state lives in a tiny store `useConflictPanelStore` (`openWsId: string | null`, `openFor(id)`, `close()`),
  so the Home popover can open a workspace's panel. A row renders its panel only when `openWsId === workspace.id`
  AND the row currently has a lock; when the lock disappears the panel content vanishes with it (the row
  closes the store entry in an effect).
- When `openFor(wsId)` comes from the Home popover, the row also scrolls itself into view
  (`scrollIntoView({ block: 'nearest' })`).
- After a successful resolve the lock disappears → icon and panel disappear. While the command is "sent" the
  ResolveRow shows its own "sent" line (existing behaviour).

## 6. i18n (en + zh-TW)

`profile.conflict.button`, `.title`, `.open_settings`, `.show_workspace`, `.on_master`, `.switch_to_master`,
`.workspace_button`, `.workspace_title`. zh-TW: 「{count} 個同步衝突」「同步衝突」「到設定處理」「前往工作區」
「衝突在 master，切回 master 才能處理」「切回 master」「「{name}」有同步衝突」「「{name}」的同步衝突」.
(`master` wording follows the existing `profile.master` value in each locale.)

## 7. Out of scope

Narrow activity bar; resolving `settings`/`workspaces` outside Settings; any executor / status change.

## 8. Tests

- `conflict-view.test.ts`: no master / null status / profile gone → []; order; `tabsLockOf` hit/miss.
- `HomeRow.test.tsx`: no lock → no button; lock → button with count; click opens panel, doesn't open switcher;
  row actions per case in §3 (settings → navigate; tabs + master on screen → store `openFor`; slave on screen →
  switch button calls `chooseProfile('master'...)`); lock removed → button gone.
- `ProfileSwitcher.test.tsx`: master item conflict icon present only with locks.
- `WorkspaceRow.test.tsx`: lock on its own tabs → icon; lock on another ws → none; slave on screen → none; click
  opens panel with `profile-resolve-row-tabs.<ws>`; keep-local confirm → `requestResolve` called with the lock;
  lock removed → icon gone; clicking the icon does not call `onSelectWorkspace`.
- Mutation checks (deliverable): flip the master-on-screen guard, flip `length > 0`, drop stopPropagation — each
  must turn a test red.
- Live acceptance: two clients on the same SOT profile, create a real `tabs.<ws>` conflict, resolve from the
  sidebar, icon disappears in both windows.
