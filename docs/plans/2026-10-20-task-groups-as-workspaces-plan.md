# Task groups as a workspace-level layer — plan (2026-10-20)

Spec: `docs/specs/2026-10-20-task-groups-as-workspaces-spec.md` (§ numbers below are the spec's). Phases are PRs;
tasks are commits; TDD in every task (the test first, seen red). Test tiers per CLAUDE.md: affected files during
development; at the PR layer `vitest related` + guards + both `tsc` + eslint on air26 (SPA), the changed packages +
`make lint` (Go). No full suite before the release.

## TG-0 — sidebar rows (SPA; two PRs)

TG-0a is in flight (`fix/task-group-team-session-dedupe`): `buildGroupView` claims every tab whose session is a live
seat's (spec §5 rule 1), so a guardian tab no longer lands in 「未分組」.

TG-0b (`fix/task-group-rows`), after TG-0a merges:
1. Seat rows with no tab: `TaskGroupView.renderRow`'s no-tab branch draws the same row as `InlineTab` (avatar, host
   badge, persona name, label) with a dimmed style instead of the bare label. Test: a seat with `tabId === null` and a
   persona renders its avatar and name.
2. Persona name colour: `InlineTab.tsx` name span gets `text-text-secondary` when the row is not active (the row's own
   hover/active classes still win). `inline-tab-row-classes.ts` is shared with the host-badge preview and is not touched.
3. Title after the name: for a seat row with a tab, `shownTitle = displayTitle || seatOverride.title`; with no tab, the
   label. Test both.
4. Header outline: `TaskGroupHeader` name span `focus:outline-none focus-visible:ring-1 focus-visible:ring-border-active`.

## TG-1 — daemon (Go; 1f releases and deploys)

1. `internal/module/profiles/validate.go`: `sectionPattern` admits `taskgroups` (exact; `taskgroups.x` stays
   rejected). Tests in the package's validate tests: accept `taskgroups`; reject `taskgroups.x`, `taskgroup`.
2. `internal/core/info_handler.go`: capability `profiles.taskgroups.v1` advertised when the profiles module is
   registered. Test through the real module registration → `/api/info` (the 743 rule).
3. PR layer: `go test ./internal/module/profiles/ ./internal/core/` + `make lint`.

## TG-2a — profile plumbing (SPA; no consumer)

1. Store shape: `stores/useTaskGroupsStore.ts` is rewritten as the world's groups:
   `{ payload: TaskGroupsPayload, local: { tabOrder, placement } }` (local = entries for local-only tabs, §4.1), persisted
   (localStorage, like workspaces), with plain setters only (no actions yet). The old `byHost` state goes; nothing reads
   it after TG-2b, and until then TG-2a keeps the old store file under a new name (`useHostTaskGroupsStore.ts`) so the
   current view keeps working. Test: persistence round trip; the local split.
2. `lib/profile/types.ts`: `SectionKind` + `'taskgroups'`; `ProfileSectionKey` + the literal.
3. `lib/profile/projections.ts`: `PROJECTIONS.taskgroups` (order, groups.*.{name,color,members,createdAt},
   tabOrder.*, placement.*), `SECTION_SCHEMA_ORDINAL.taskgroups = 1`, `sectionKind()`, `shapeTable()`, the
   `@wire:host-id=d1` marker. Guard test `projections.test.ts` snapshot updated in the same commit.
4. `lib/profile/sections.ts`: `buildTaskGroupsSection(world, identity)` — members' hosts and team keys in `tabOrder` /
   `placement` values to wire ids; entries naming tabs outside the synced tab set left out. Tests: wire translation,
   local-only split, dangling entries dropped.
5. `lib/profile/applier.ts` + `apply-to-stores.ts`: `applyTaskGroups(payload, resolver, localTabs)` — wire → local,
   local entries kept, unknown hosts kept as-is (matched later), `isWellFormedSection` case. Tests: round trip with
   `buildTaskGroupsSection`; a remote apply keeps local-only entries.
6. `lib/profile/collector.ts` / `master-world.ts` / `useLocalProfilesStore.ts`: the section in `buildSection`,
   `scheduleWholeWorld`, `primeAll`, `scheduleDiff`, `scheduleHostBearing`; `ParkedWorld` and `isParkedWorld` gain
   `taskgroups`; switching active profile parks and restores it; 「複製為 slave」 copies it. Tests: park/restore; a slave
   edit never reaches the master's section.
7. `lib/profile/executor.ts`: `Shapes` gains the kind; the section is built and pushed only when the SOT host's
   `/api/info` has `profiles.taskgroups.v1` (selector in `useNexHostStore`); without it the section stays local and
   is never `failed`. Integration test with the fake daemon: no capability → no PUT; capability → PUT with ordinal 1.
8. Conflict UI: `sync-view.ts` (`SectionView`, `FIXED_ORDER`), `components/settings/profile/resolve-counts.ts`
   (`countPayload`: groups), locales `settings.profile.resolve.unit.taskgroups` (zh-TW + en). Test: the resolve row
   renders the unit and count.

## TG-2b — the store goes live (SPA; after TG-1 is deployed)

1. `lib/task-groups/edits.ts` on the new payload: `newGroup` (no root required — an empty group is allowed), rename,
   colour, delete, add/remove/move member `{host, root}` (a (host, root) in at most one group), limits 32 / 64.
   Pure, with tests (including cross-host members).
2. `lib/task-groups/actions.ts`: the same verbs on the store (writes are local, the collector syncs); root still
   required to add a member (`no-root`); `clearTaskGroup` keeps its "nothing alive" check. Tests.
3. Root cache: `hooks/useSessionRoots.ts` persists `(wire host, session id) → root` device-locally and reads it before
   asking the daemon. Test: an offline host's tab keeps its group.
4. `lib/task-groups/view-model.ts` + `TaskGroupView.tsx`: manual groups from the store (no per-host blocks, no
   `unsafe` section); `drag.ts` `acceptsDrag` drops the same-host rule; `row-menu.ts` lists every group. Tests updated.
5. Remove the SPA's hostconfig path: `lib/task-groups/api.ts`, `loader.ts`, `useHostTaskGroupsStore.ts`, the
   `taskgroups.invalid/unsafe*` strings that only they used. Test: nothing imports them (guard).
6. The task-group view shows the section's lock like `WorkspaceConflict` (alert on the view's header row → the
   resolve row). Test.

## TG-3 — sidebar as a workspace-level layer (SPA)

1. `lib/task-groups/placement.ts` (pure): `groupOfTab(tab, ctx)` implementing §5's four rules; `placementWrites`
   for the three write moments. Tests per rule and per write moment, including the watcher-area exclusion.
2. Device-local store `stores/useGroupViewStore.ts`: `selectedGroup`, `lastActiveTab[groupKey]`, `expanded[groupKey]`
   (reconciled against live keys, like `reconcileWorkspaceExpanded`), `lastByView {workspace, groups}`. Tests.
3. Placement wiring: plain tabs placed on open (tab-store add hook), session tabs placed when they stop being session
   tabs, auto-join when a placed tab gains a root (§5). Tests with the stores.
4. `TaskGroupHeader` / `TeamGroupHeader`: chevron replaces the count, header click selects / toggles (WorkspaceRow
   rule), chevron toggles only, focus-visible ring only, empty manual group has no chevron and keeps 「清除」. Tests.
5. Header context menu for manual groups: rename, colour (team palette), 「刪除任務編組」 with a confirm when it holds
   tabs. Tests.
6. Row drag: reorder within a group writes `tabOrder` (lead fixed; a member move also writes this device's
   `memberOrder`); moving between manual groups / 「未分組」 per §6.1; teams take no drop. Tests on the drag decisions
   (pure) and one component test.
7. Bottom nav + in the task-group view: 「新增任務編組」 creates 「任務編組 N」, selects it, starts the rename. Test.
8. View switch memory (§6.3) in `HomeRow` toggle: restore last workspace / group and its last tab; fall back to the
   current tab's. Tests both directions and the fall-back.

## TG-4 — the top bar (SPA)

1. `getVisibleTabIds` takes the view: in the task-group view, the selected group's tabs — pinned first, then
   `tabOrder[groupKey]` (+ local entries), then the rest in workspace order; a team's lead first. Tests.
2. `TabBar` in the task-group view: the lead is not draggable and nothing drops before it; no team-run frame;
   `handleReorderTabs` writes `tabOrder` (and `memberOrder` for team members) instead of the workspace order. Tests.
3. Range users: close-others / close-right (`features/workspace/hooks.ts`), `useShortcuts` (⌘1–9, prev/next),
   `TabContextMenu` — all through the view-aware range. Tests for one of each.
4. Activating a tab selects its group (`handleSelectTab` in the task-group view); an empty group shows the empty
   state. Tests.

## TG-5 — personal panel (SPA)

1. Design pass on a real screen (Opus; style taken from the team panel as shipped in 761): strip, one-line, full form,
   settings block. Reviewed by the user before building.
2. Build per the design: `panel-view.ts` gains the solo-agent case (absorbing `OwnWorkbookPanel` / `OwnTitleStrip`),
   the group editor reuses TG-2b/TG-3 actions. Tab-switch survival test (the tab-hosted checklist).

## TG-6 — cleanup (daemon + SPA; 1f)

1. Remove `PUT /api/hostconfig/task-groups`, the `task_groups` key handling, `task_groups.v1`.
2. NP2 L9 / §7 / §12 text, `sync-inventory.html` S19.

## Order and ownership

TG-0b and TG-1 can run at once. TG-2a needs nothing; TG-2b needs TG-1 deployed on every host. TG-3 needs TG-2b;
TG-4 needs TG-3. TG-5's design can start any time; its build needs TG-3. One Sonnet member takes TG-1 → TG-4 in
order (stacked where the next starts before the previous merges); small fixes stay with subagents.
