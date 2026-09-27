# Sidebar conflict icons — plan (2026-09-27)

Spec: `docs/specs/2026-09-27-sidebar-conflict-icons-spec.md`. One PR, SPA only. TDD per task, one commit per task.
Run from `spa/`: `npx vitest run <files>`, `pnpm run lint`, `npx tsc -p tsconfig.app.json --noEmit`.

## Task 1 — the reading + the panel store + i18n

Files:
- `spa/src/lib/profile/conflict-view.ts` (+ `.test.ts`): `locksOf(sync)`, `tabsLockOf(sync, wsId)` per spec §2.
  - `locksOf`: `sync.master === null || sync.status === null || profileIsGone(sync)` → `[]`; else
    `describeSections(Object.keys(sync.status.locks), null)` mapped to `{ key, kind, workspaceId: kind==='tabs' ?
    workspaceIdOf(key) : null, status: locks[key].status }`.
  - `tabsLockOf`: same guards, then `sync.status.locks['tabs.' + wsId] ?? null` — build the key with the SAME helper
    projections.ts uses to name a tabs section if one exists (grep `tabs.` builder in `lib/profile/projections.ts`);
    otherwise inline template.
- `spa/src/hooks/useMasterOnScreen.ts` (+ test): `useSyncExternalStore` over `useTabStore`, `useWorkspaceStore`,
  `useLocalProfilesStore` subscriptions (same trio as CurrentBlock's `subscribeWorld`), snapshot =
  `activeProfileId === MASTER_PROFILE_ID && readMasterWorld().settled` (boolean). Also export
  `useMasterWorkspaces()` → the master world's workspaces array or null (same as CurrentBlock's `masterWorkspaces`;
  the array is the store's own so identity is stable). Do NOT refactor CurrentBlock.
- `spa/src/stores/useConflictPanelStore.ts` (+ test): zustand, not persisted: `openWsId: string | null`,
  `openFor(id)`, `close()`, `toggle(id)`.
- `spa/src/locales/en.json`, `zh-TW.json`: the `profile.conflict.*` keys of spec §6 (locale-completeness test must
  stay green).

## Task 2 — workspace row

Files:
- `spa/src/features/workspace/components/WorkspaceConflict.tsx` (+ test): `WorkspaceConflict({ workspace })`
  renders nothing unless `useMasterOnScreen()` and `tabsLockOf(useProfileSync(), workspace.id) !== null`. Renders
  the button (spec §5) and, when `useConflictPanelStore.openWsId === workspace.id`, a `FloatingPanel` with
  `<ul><ResolveRow …/></ul>`. `onPointerDown`/`onClick` stopPropagation. Effect: if the store says open for this
  ws but there is no lock (or not master on screen) → `close()`. Effect on open-from-store: `buttonRef.current
  ?.scrollIntoView?.({ block: 'nearest' })` (optional-call: jsdom lacks it).
  `FloatingPanel` onClose → `close()`.
- `WorkspaceRow.tsx`: mount `<WorkspaceConflict workspace={workspace} />` after the name `<button>`, before the
  + button. Keep the existing tests green.
- Tests mock `lib/profile/start` (`requestResolve`) and `components/settings/profile/resolve-counts` as
  ResolveBlock.test.tsx does; set sync via `setLocalSnapshot` (lib/profile/sync-status) or mock `useProfileSync`
  — prefer the real channel if a test helper exists (`__resetSyncStatusForTest`). Cover spec §8's WorkspaceRow list.

## Task 3 — Home row + switcher menu

Files:
- `spa/src/features/workspace/components/ProfileConflict.tsx` (+ test): `ProfileConflictButton()` = button +
  `FloatingPanel` (local `useState` open). Rows per spec §3 using `locksOf`, `useMasterOnScreen`,
  `useMasterWorkspaces`, live `useWorkspaceStore` (existence check), `useLocation` for Settings,
  `useProfileSwitcherStore.getState().chooseProfile(MASTER_PROFILE_ID)`, `useConflictPanelStore.getState().openFor`.
  Labels: same keys as CurrentBlock's `sectionLabel` (`settings.profile.current.label.*`); reasons:
  `settings.profile.resolve.why.*` exactly like ResolveRow's `why()` (conflict / reset / invalid.<reason with - →
  _>; do not append `invalid_only`). Effect: close when `locksOf` becomes empty.
- `HomeRow.tsx`: mount `<ProfileConflictButton />` between the label button and the switcher.
- `ProfileSwitcher.tsx`: master entry `trailing` → fragment of the conflict icon (when `locksOf(sync).length>0`)
  and the existing dot.
- Tests per spec §8 (HomeRow / ProfileSwitcher / ProfileConflict).

## Task 4 — mutation checks + full suite

Apply each mutation of spec §8 temporarily, confirm a red test, revert (report which test caught each). Then full
`npx vitest run`, lint, tsc, `pnpm run build`.

## Acceptance (main session)

Two clients (distinct host ids) on the same SOT profile via worktree dev server; create a `tabs.<ws>` conflict
(both edit the same workspace's tabs with auto-sync off, then turn it on); verify Home icon + workspace icon in both,
resolve from the workspace panel, icons gone in both; Home popover actions (settings link, show workspace, slave →
switch to master).

## Plan review amendments (Claude reviewer standing in for codex; all adopted — these OVERRIDE the text above and spec where they conflict)

- **C1** `tabsLockOf`: `if (!isSyncableWorkspaceId(id)) return null` BEFORE building the key; never call `tabsSectionKey`
  (it throws on unsyncable ids, projections.ts:158-163) in render. Test with an id like `ws.bad!` → null, no throw.
- **I1** Only the icon button lives inside the header `div {...listeners}`. The `FloatingPanel` is rendered as a
  SIBLING of the header div inside the row's outer div (portal events bubble through the React tree; inside the
  header a panel drag would start a workspace drag). Test: pointerdown on the panel's `floating-panel-handle` does
  not reach the header's listeners.
- **I2** NO `stopPropagation` on the conflict button (WorkspaceRow.test.tsx:139-173 pins that inner header buttons
  must not block pointer-down; `distance: 5` separates click from drag; the header div has no onClick). Add the
  new button to those "does not block pointer-down" tests. Drop the "drop stopPropagation" mutation from spec §8.
- **I3** Escape inside the ConfirmDialog must not also close the FloatingPanel. Fix in the shared components:
  ConfirmDialog handles Escape so that the panel can tell (e.g. listen in the capture phase and `preventDefault()`),
  and FloatingPanel's Escape handler returns early on `e.defaultPrevented`. Verify listener ORDER actually makes it
  work (the panel mounts first); add a test (dialog inside panel: Escape closes the dialog only; a second Escape
  closes the panel). Keep existing ConfirmDialog/FloatingPanel tests green.
- **I4** Tests that press keep-local/take-sot must set up `useProfileStore` master + `masterEndpoint` and a
  `useHostStore` host at that endpoint (copy ResolveBlock.test.tsx's fixture); call `__resetSyncStatusForTest`
  before render.
- **I5** The row's close effect closes the store entry ONLY when the lock is gone or a slave is actually active
  (`readMasterWorld().settled && !onScreen`… i.e. settled-and-not-master). While merely unsettled, hide icon+panel
  but keep `openWsId`.
- **M1** i18n placeholders are `{{name}}` / `{{count}}` (useI18nStore.ts:50).
- **M2** `settings.profile.current.label.tabs` takes `{ workspace: <name> }`.
- **M3** master on screen = `const r = readMasterWorld(); r.settled && r.onScreen` (verify field name).
- **M4** Home popover, `tabs.<ws>` row with the master active but world unsettled → the Settings button.
- **M5** `locksOf(sync, masterWorkspaces)` passes the master workspaces to `describeSections` (null allowed).
- **M6** Key the workspace `ResolveRow` by `JSON.stringify([master.hostId, master.profileId, key])` as ResolveBlock does.
- **M7** ProfileSwitcher `useMemo` deps include the lock count. `busy` replaces `trailing` (Menu.tsx:258-260) — accepted.
- **M8** On unmount, a row whose id equals `openWsId` closes the store entry.
