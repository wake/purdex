# Task groups as a workspace-level layer — plan r4 (2026-10-20)

Spec: `docs/specs/2026-10-20-task-groups-as-workspaces-spec.md` r4 (§ numbers below are the spec's). Phases are PRs
(≤ 800 lines / ≤ 20 files with tests); tasks are commits; TDD in every task (the test first, seen red). Test tiers per
CLAUDE.md: affected files while developing; at the PR layer `vitest related` + guards + locale completeness + both
`tsc` + eslint on air26. No full suite before the release. Every phase is SPA except TG-6.

Reviews: r1 by codex `task-mvfs2clf-c2dr4g`; r2 by an Opus reviewer (codex quota exhausted until 10-25, 26 findings, all
answered in r3: see the spec header).

## TG-0b — #3662 (done, waiting for review)

## TG-1a — group key and the group store

1. `lib/task-groups/group-key.ts`: `teamGroupKey(view.key)`, `manualGroupKey(id)`, `UNGROUPED`, `parseGroupKey`,
   `teamKeyOfGroupKey`. Tests: round trips; NUL inside an id refused.
2. `stores/useManualGroupsStore.ts` (new file, key `purdex-task-groups`): state per spec §4.1, persisted, `merge`
   sanitising names, colours, limits, duplicate members (no sorting); plain setters; `syncManager.register` (the
   `collector.test.ts` key list grows). Tests.
3. Settings plumbing: `types.ts` `SettingsStorageKey`; `projections.ts` `settingsPaths('purdex-task-groups', ['order',
   'groups'])`, ordinal 12 with its comment; host translation for `groups.*.members[].host` in the builder
   (`sections.ts`, beside the `purdex-host-settings` branch) **followed by the sort by (wire host, root)**, and in
   `settingsFromWire`; the collector's and `apply-to-stores`' store maps; guard snapshot. Tests: local ↔ wire, an unknown
   wire host kept, an ordinal-11 payload clears the store.
4. Host rewrite: the store in `host-reresolve.ts` `planRewrite` and its re-read list; host removal (`host-lifecycle.ts`
   → `rewriteHostRefs`) turns its local ids to wire ids. Tests: a kept wire id becomes local once the host is
   recognised; a removed host's members go out as wire ids.
5. Integration test (style of `tabs-local-only.integration.test.ts`): a group created on device A reaches device B
   through the real collector and applier; a slave on screen edits the master's groups; **two devices with different
   local host ids** adding the same two members in a different order converge with no push-back.

## TG-1b — ranks and the tab fields

1. `lib/task-groups/rank.ts`: `rankBetween(a?, b?)` (fractional index, base 62), `compareRank`, the shared limits
   (generator 32, check 128) and `rebalance(ranks)`. Tests: between any two, before the first, after the last; repeated
   inserts at one spot hit the cap and rebalance; the check limit is above the generator limit.
2. `types/tab.ts` `group?: string`, `groupRank?: string`; `useTabStore` `setTabGroup`, `setTabGroupRank`.
3. `projections.ts` `tabs.*.group`, `tabs.*.groupRank`, tabs ordinal 5; the builder translates the team host in `group`
   to wire beside `layoutToWire` (`sections.ts`); `tabsFromWire` translates it back beside `layoutFromWire` (it runs in
   `apply-to-stores` before `applyTabs`); `applyTabs` copies both fields explicitly (absent = cleared); `isTabsPayload`
   checks types and limits before translation. Tests: `hash(build(apply(p))) === hash(p)` with the fields; malformed
   values refused; an unknown wire host kept.
4. Host rewrite: `rewriteTabsHosts` and the parked-world rewrite cover `group`. Tests: host recognised later; host removed.
5. Carry tests: world switch park / restore; 「複製為 slave」; a local-only interface tab across a remote apply; the
   Electron tab transfer between windows (`useElectronIpc.ts`).
6. purdex-ios: confirm its tab append carries unknown tab fields unchanged (repo read, no build); add a tab with `group`
   / `groupRank` to the shared phone-tab fixture. If iOS drops them, stop and report before merging.

## TG-2a — new actions

1. `lib/task-groups/manual-edits.ts` (pure, new file beside `edits.ts`): new group (empty allowed), rename, colour,
   delete, add / remove / move member (local host, root), limits, no duplicates. Tests incl. two hosts in one group.
2. `lib/task-groups/manual-actions.ts`: the verbs on `useManualGroupsStore`; adding a member reads the root from the
   cache or the daemon (`no-root` otherwise); deleting / taking out also clears the matching tabs' `group` in the
   on-screen world (spec §5). Tests.

## TG-2r — roots out of React

`lib/task-groups/root-cache.ts` (persisted, device-local; `rootOf(hostId, sessionId)`) and `startSessionRootsLifecycle`
(the read / debounce / once-per-session logic moved out of `useSessionRoots`; reads while the effective task-group view
is on; gate `team.relay_quota.v1`; drops a host's entries on removal / daemon-id mismatch, and entries no tab of any
world refers to). `useSessionRoots` becomes a thin reader. Tests: offline host still answers; cleanup keeps parked-world
references.

## TG-2b — the sidebar on the new store

1. `view-model.ts` / `TaskGroupView.tsx`: manual groups from `useManualGroupsStore` in `order`, membership by (host,
   root); no per-host blocks, no `unsafe` rows. Tests updated.
2. `drag.ts` without the same-host rule and `row-menu.ts` listing every group, both on the new actions. Tests.

## TG-2c — delete the old path

`lib/task-groups/api.ts`, `loader.ts`, `edits.ts`, `actions.ts`, `useTaskGroupsStore.ts`, their tests and strings, the
lifecycle hook in `main.tsx`. A guard test that nothing imports them. Pure deletion: only additions count toward the
line limit.

## TG-3a — view store and headers

1. `stores/useGroupViewStore.ts` (device-local): `selectedGroup`, `lastActiveTab[groupKey]`, `expanded[groupKey]`
   (team keys reconciled per host after its roster is read, manual keys against the store), `lastByView`. Tests.
2. `TaskGroupHeader` / `TeamGroupHeader`: chevron, header click (select / toggle), chevron toggles, focus ring only; the
   three manual group states of §6.1; selecting from the sidebar activates the group's last / first tab, an empty group
   sets the active tab to none. Tests.
3. `HomeRow` toggle restores the last workspace / group and its tab, with the fall-back. Tests.

## TG-3b — one definition of a tab's group (team-module files: 1f releases)

1. `lib/team/team-index.ts`: the inclusive seat map beside `byTabId`; `panelTeam` (`team-structure.ts`) reads it. Tests:
   a pinned seat tab routes to the team panel; the workspace view's run drawing is unchanged.
2. `lib/task-groups/placement.ts`: `groupOfTab` (§5; watcher-area skip by `isWatchTabId`). Tests: a split tab with a solo
   primary and a seat secondary is the team's; a pinned team tab; an ended agent keeps its group.
3. `buildGroupView` uses it (replacing #3654's first-live-pane rule). Tests.
4. Selection follows the active tab (subscriber; watcher-area active tab leaves it); the automatic write as a hook
   registered on `useTabStore`'s `setActiveTab` action (§5). Tests: a new tab in a group and a pane detached into a tab
   are stamped; `addTab`'s implicit activation, a rehydrate from a second window (two stores over one storage), a tab
   arriving from another window, a reopened tab and an apply are not.

## TG-3c — header menu and +

1. Manual group header menu: rename, colour, 「刪除任務編組」 with a confirm. Tests.
2. `BottomNav` in the task-group view (wide and narrow activity bars): 「新增任務編組」. Tests.

## TG-3d — drag in the sidebar

Drag decisions (pure) and wiring with dnd-kit like `InlineTabList`: rank writes, seat moves (all its tabs →
`memberOrder`), plain tabs after members, moves to a manual group / 「未分組」, no drop on teams. Tests.

## TG-4a — the range

`getVisibleTabIds(…, view)` computing the group range outside React. Every reader: `App.tsx` (TabBar, `useTabWorkspace
Actions` close slices, the empty state at `App.tsx:254`), `useShortcuts` (`visibleTabIds` — no member-collapse filter in
the task-group view —, ⌘1–9, prev/next, ⌘W `ownedElsewhere`), `features/workspace/hooks.ts:337`, the merge-into-tab
targets; the watcher area keeps priority; the narrow / left rule (§6). A test per reader.

## TG-4b — the bar's order in the task-group view

`TabBar` group mode: zones and order of §6.2 (lead, seats with all their tabs, plain tabs by rank), no collapse, no run
frame; empty group. Tests (a collapsed team still lists every tab; a seat with two tabs; tabs from two workspaces).

## TG-4c — drag in the bar in the task-group view

Lead fixed, nothing before it, seat moves with its tabs (`memberOrder`), plain tabs by rank, pinned zone by rank. Tests.

## TG-5 — personal panel

5a design pass on a real screen (Opus), shown to the user; 5b `panel-view.ts` case + strip + one-line form (absorbing
`OwnTitleStrip`); 5c full form and settings on TG-2a actions, tab-switch survival test.

## TG-6 — cleanup (1f)

Daemon route, key, capability, `taskgroups.go`; NP2 L9 / §7 / §12; `sync-inventory.html` S19.

## Order and ownership

TG-1a → 1b → 2a → 2r → 2b → 2c → 3a → 3b → 3c → 3d → 4a → 4b → 4c, stacked where useful — one Sonnet member. TG-5a by the
lead after TG-2a; 5b/5c after TG-3b. TG-6 after TG-4c ships. Merging waits for the review rule the user picks while codex
is out (until 10-25).
