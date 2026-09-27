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
