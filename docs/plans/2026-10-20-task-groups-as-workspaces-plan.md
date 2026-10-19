# Task groups as a workspace-level layer — plan r2 (2026-10-20)

Spec: `docs/specs/2026-10-20-task-groups-as-workspaces-spec.md` r2 (§ numbers below are the spec's). Phases are PRs
(≤ 800 lines / ≤ 20 files with tests); tasks are commits; TDD in every task (the test first, seen red). Test tiers per
CLAUDE.md: affected files while developing; at the PR layer `vitest related` + guards + locale completeness + both
`tsc` + eslint on air26. No full suite before the release. Every phase is SPA except TG-6.

r2 answers the plan review `task-mvfs2clf-c2dr4g`: no new section and no daemon phase (groups ride `settings`, a tab's
place rides its own tab, §4); one definition of a tab's session (§3); a creation stamp in the tab factory instead of a
store hook (§5); the team bar keeps the member run (§6.2); the root cache keyed by local host and gated on its own
endpoint (§4.5); phases split to size.

## TG-0b — sidebar rows (in flight: `fix/task-group-rows`)

Seat rows without a tab get avatar + persona name (dimmed); persona name `text-text-secondary`; the title after it is
the tab's title first; the header name has no UA outline. Brief: `b-taskgroup-rows-subagent.md`.

## TG-1a — group key and the group store (no consumer)

1. `lib/task-groups/group-key.ts`: `teamGroupKey(wireHost, teamId)`, `manualGroupKey(id)`, `UNGROUPED`,
   `parseGroupKey`, and `groupKeyOfTeamView(view, identity)` (local host → wire). Tests: round trips, NUL in ids
   refused, a host without a daemon id keeps its local id.
2. `stores/useTaskGroupsStore.ts` rewritten as `purdex-task-groups` (`order`, `groups` with wire members), persisted,
   `merge` sanitising what arrives (names, colours, duplicate members, limits), plain setters. The old per-host store is
   renamed `useHostTaskGroupsStore.ts` so the current view keeps working until TG-2b.
3. `lib/profile/types.ts` `SettingsStorageKey` + the key; `projections.ts` `settingsPaths('purdex-task-groups',
   ['order', 'groups'])`, settings ordinal 12 with its comment; the collector's and `apply-to-stores`' store maps; guard
   snapshot `projections.test.ts`.
4. Integration test in the style of `tabs-local-only.integration.test.ts`: a group created on device A reaches B's
   store through the real collector and applier; members' hosts are not translated; an ordinal-11 payload leaves the
   store alone; a slave on screen sees and edits the master's groups.

## TG-1b — the tab fields (no consumer)

1. `types/tab.ts`: `Tab.group?: string`, `Tab.groupRank?: number`; `useTabStore` setters `setTabGroup`,
   `setTabGroupRank` (no-op on an unknown tab).
2. `projections.ts`: `tabs.*.group`, `tabs.*.groupRank`; tabs ordinal 5; `applier.ts` upcast — an ordinal-4 payload
   keeps the local values of both fields. `sections.ts` / `applier.ts` tests for the two fields.
3. Carry tests: 「複製為 slave」 (`switch-active.ts` `copyWorld`) keeps both fields on the copied tabs; an interface tab
   (local-only) keeps them across a remote apply; a world switch parks and restores them (they are on the tab).

## TG-2a — edits and actions on the new store; root cache

1. `lib/task-groups/edits.ts` on the new state: `newGroup` without a root (empty allowed), `renameGroup`,
   `setGroupColor`, `deleteGroup`, `addMember` / `removeMember` / `moveMember` with `{host, root}` (wire), one group per
   member, limits. Pure tests, including members of two hosts in one group.
2. `lib/task-groups/actions.ts`: the same verbs on the store; adding a member resolves the session's root from the
   cache or the daemon (`no-root` otherwise) and its host's wire id; `clearTaskGroup` keeps its "nothing alive" check.
3. Root cache `lib/task-groups/root-cache.ts` (device-local persisted, `(local host, session id) → root`, forget with the
   host) and `hooks/useSessionRoots.ts` on it: gate `team.relay_quota.v1`; a plain `rootOf(hostId, sessionId)` reader
   for non-React code. Tests: an offline host's root still answers; a forgotten host's entries go.

## TG-2b — the view on the new store; hostconfig path out

1. `view-model.ts` / `TaskGroupView.tsx`: manual groups from the new store in `order`, membership by (wire host, root);
   no per-host blocks, no `unsafe` rows; teams and 「未分組」 unchanged. Tests updated.
2. `drag.ts` drops the same-host rule; `row-menu.ts` lists every group. Tests.
3. Remove `lib/task-groups/api.ts`, `loader.ts`, `useHostTaskGroupsStore.ts`, their strings and the lifecycle hook in
   `main.tsx`; a guard test that nothing imports them.

## TG-3a — selection, expansion, view memory

1. `stores/useGroupViewStore.ts` (device-local): `selectedGroup`, `lastActiveTab[groupKey]`, `expanded[groupKey]`
   (reconciled against live keys, like `reconcileWorkspaceExpanded`), `lastByView {workspace, groups}`. Tests.
2. `TaskGroupHeader` / `TeamGroupHeader`: chevron instead of the count, header click selects / toggles (the
   `WorkspaceRow` rule), chevron toggles only, focus-visible ring only, empty manual group: no chevron, 「清除」 kept.
   Tests.
3. `HomeRow` view toggle restores the last workspace / group and its last tab, falling back to the current tab's.
   Tests both ways and the fall-back.

## TG-3b — placement (§5)

1. `lib/task-groups/placement.ts` (pure): `groupOfTab(tab, ctx)` (rules 1–4) and `transitionWrite(prev, next)`.
   Tests per rule; the watcher-area exclusion; an unknown root never writes 「未分組」.
2. Creation stamp in the tab factory: find the one function every user-open path goes through (`addTab` or its
   wrapper) and set `group` there from `useGroupViewStore` / the active tab's group; prove apply, rehydrate, restore and
   world switch do not pass through it (a test per path that a tab arriving that way gets no stamp).
3. A subscriber for the transition stamp and auto-join (tab store + roots + team views + group store), started next to
   `startTeamTabLifecycle`. Tests with the real stores: a session ends → keeps its group; a placed tab gains a root →
   joins.

## TG-3c — header menu and +

1. Manual group header context menu: rename, colour (team palette), 「刪除任務編組」 (confirm when it holds tabs;
   members dropped, placements cleared). Tests.
2. `BottomNav` in the task-group view: 「新增任務編組」 creates 「任務編組 N」, selects it, starts the rename;
   the workspace view's + untouched (both `ActivityBarWide` and `ActivityBarNarrow`). Tests.

## TG-3d — row drag in the sidebar

1. Drag decisions (pure, `drag.ts`): reorder within a group (rank between neighbours, renumber when too close), the
   team rules (lead fixed, members among members → `memberOrder`, plain tabs after the members), moves to a manual group
   / 「未分組」, no drop on teams. Tests.
2. `TaskGroupView` wiring with dnd-kit like `InlineTabList`; one component test.

## TG-4a — the view-aware range

1. `getVisibleTabIds` takes the view and, in the task-group view, returns the selected group's range (§6.2 order).
   Tests.
2. Every caller: `App.tsx` (TabBar and `useTabWorkspaceActions(displayTabs)`: close-others / close-right slices),
   `useShortcuts` (⌘1–9, prev/next, close), the merge-into-tab targets, shown-hosts and watch-area callers. A test for
   each kind of caller.

## TG-4b — the top bar in the task-group view

1. `TabBar` group mode: lead first and not draggable, the member run kept together (no frame), plain tabs after it;
   `handleReorderTabs` writes `groupRank` / `memberOrder` instead of the workspace order. Tests.
2. Activating a tab selects its group (`handleSelectTab`); selecting a group activates its last tab; an empty group
   shows the empty state. Tests.

## TG-5 — personal panel

- 5a: design pass on a real screen (Opus), style from the shipped team panel; shown to the user.
- 5b: `panel-view.ts` solo-agent case, title strip and one-line form (absorbing `OwnTitleStrip`).
- 5c: full form and settings block (group move / out / new, name, colour) on TG-2a actions; tab-switch survival test.

## TG-6 — cleanup (1f)

Daemon: `PUT /api/hostconfig/task-groups`, the `task_groups` key, `task_groups.v1`, `taskgroups.go`. Docs: NP2 L9 / §7 /
§12, `sync-inventory.html` S19.

## Order and ownership

TG-0b now (subagent). TG-1a → TG-1b → TG-2a → TG-2b → TG-3a → 3b → 3c → 3d → TG-4a → 4b in order, stacked where the
next starts before the previous merges — one Sonnet member. TG-5a (design) by the lead any time after TG-2a; 5b/5c after
TG-3a. TG-6 after TG-4b ships.
