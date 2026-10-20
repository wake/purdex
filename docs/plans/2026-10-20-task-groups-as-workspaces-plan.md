# Task groups as a workspace-level layer — plan r5.1 (2026-10-20)

Spec: `docs/specs/2026-10-20-task-groups-as-workspaces-spec.md` r5 (§ numbers below are the spec's). Phases are PRs
(≤ 800 lines / ≤ 20 files with tests); tasks are commits; TDD in every task (the test first, seen red). Test tiers per
CLAUDE.md: affected files while developing; at the PR layer `vitest related` + guards + locale completeness + both
`tsc` + eslint on air26. No full suite before the release. Every phase is SPA except TG-6. Where a phase may run over,
its **split** line says where to cut it; cut there and report before going on.

Reviews: r1 by codex `task-mvfs2clf-c2dr4g`; r2 / r3 by an Opus reviewer while the codex quota was out; r4 by codex
`task-mvfw3qzd-pbuzxj` (answered in r5); r5 by codex `task-mvfwim2s-7f97oq` (every earlier finding resolved, one new
important answered in r5.1: see the spec header).

## TG-0b — #3662 (merged, 4a887d935)

## TG-1a-1 — group key and the group store

1. `lib/task-groups/group-key.ts`: `teamGroupKey(view.key)`, `manualGroupKey(id)`, `UNGROUPED`, `parseGroupKey`,
   `teamKeyOfGroupKey`. Tests: round trips; NUL inside an id refused.
2. `stores/useManualGroupsStore.ts` (new file, key `purdex-task-groups`): state per spec §4.1, persisted, `merge`
   sanitising names, colours, limits, duplicate members of its own form (no sorting); plain setters;
   `syncManager.register` (the `collector.test.ts` key list grows). Tests.
3. Settings plumbing: `types.ts` `SettingsStorageKey`; `projections.ts` `settingsPaths('purdex-task-groups', ['order',
   'groups'])`, ordinal 12 with its comment; in the builder (`sections.ts`, beside the `purdex-host-settings` branch)
   **translate `groups.*.members[].host` to wire, then de-duplicate, then sort by (wire host, root)**; `settingsFromWire`
   translates back; the collector's and `apply-to-stores`' store maps; guard snapshot. Tests: local ↔ wire; an unknown
   wire host kept; one member held under its local id and under its kept wire id builds once; an ordinal-11 payload
   clears the store.

## TG-1a-2 — the store in the host rewrite; old ordinals; convergence

1. Host rewrite: the store in `host-reresolve.ts` `planRewrite`, its rollback and its re-read list; host removal
   (`host-lifecycle.ts` → `rewriteHostRefs`) turns its local ids to wire ids; duplicates the mapping creates collapse.
   Tests: a kept wire id becomes local once the host is recognised; a removed host's members go out as wire ids; a
   rewrite that makes two members equal leaves one.
2. Executor integration for an ordinal-11 SOT: a clean local settings section takes it (the store clears); a dirty one
   does not pull over it.
3. Integration test (style of `tabs-local-only.integration.test.ts`): a group created on device A reaches device B
   through the real collector and applier; a slave on screen edits the master's groups; **two devices with different
   local host ids** adding the same two members in a different order converge with no push-back.

## TG-1b-1 — ranks and the tab fields

1. `lib/task-groups/rank.ts`: `rankBetween(a?, b?)` (fractional index, base 62), `compareRank`, **`compareGroupTabs`
   (ranked first by rank, then tab id; then unranked by `createdAt`, then tab id — spec §4.2)**, the shared limits
   (generator 32, check 128) and `rebalance(ranks)`. Tests: between any two, before the first, after the last; repeated
   inserts at one spot hit the cap and rebalance; the check limit is above the generator limit; equal ranks order by tab
   id; unranked after ranked, by `createdAt`.
2. `types/tab.ts` `group?: string`, `groupRank?: string`; `useTabStore` `setTabGroup`, `setTabGroupRank`.
3. `projections.ts` `tabs.*.group`, `tabs.*.groupRank`, tabs ordinal 5; the builder translates the team host in `group`
   to wire beside `layoutToWire` (`sections.ts`); `tabsFromWire` translates it back beside `layoutFromWire` (it runs in
   `apply-to-stores` before `applyTabs`); `applyTabs` copies both fields explicitly (absent = cleared); `isTabsPayload`
   checks types and limits before translation. Tests: `hash(build(apply(p))) === hash(p)` with the fields; malformed
   values refused; an unknown wire host kept.
4. The phone-tab fixture (`phone-tab.fixture.test.ts`) gets a tab with `group` / `groupRank`; the test asserts it passes
   `isWellFormedSection`, the fields exist after apply, the rebuilt hash equals the fixture's, and that it is red before
   the fixture's ordinal / shape are updated. purdex-ios needs no change (spec §4.2, checked).
- **Split** if over: task 4 goes to TG-1b-2.

## TG-1b-2 — the tab rewrite, carry, old ordinals

1. Host rewrite: `rewriteTabsHosts` covers `group` with the layouts. Tests in three places — on screen, the parked
   master, a parked slave — for a host recognised later and a host removed.
2. Carry tests: world switch park / restore; 「複製為 slave」; a local-only interface tab across a remote apply; the
   Electron tab transfer between windows (`useElectronIpc.ts`).
3. Executor integration for an ordinal-4 SOT: clean local tabs section → the fields clear; dirty → not pulled over.

**TG-1a-1 … TG-1b-2 are stacked and merge together into one release** (spec §4.4): ready one by one, released by 88
together, 1f told before.

## TG-2a — new actions

1. `lib/task-groups/manual-edits.ts` (pure, new file beside `edits.ts`): new group (empty allowed), rename, colour,
   delete, add / remove / move member (local host, root), limits, no duplicates. Tests incl. two hosts in one group.
2. `lib/task-groups/manual-actions.ts`: the verbs on `useManualGroupsStore`; adding a member takes the root the caller
   gives (`string | null`; `null` = `no-root`, refused before the store is touched) — the cache-then-daemon read is
   TG-2r's `resolveRoot`, wired in TG-2b (r5.2, 88's ruling after the TG-2a critic: the root cache does not exist yet
   here, and "ask the daemon if the cache lacks it" is written in one place only); deleting / taking out also clears
   the matching tabs' `group` in the on-screen world (spec §5). Tests.

## TG-2r — roots out of React

`lib/task-groups/root-cache.ts` (persisted, device-local; `rootOf(hostId, sessionId)` — a pure cache read — and
`resolveRoot(hostId, sessionId): Promise<string | null>`: the cache's answer if it has one; otherwise it reads that host
through the lifecycle's existing daemon read path (`GET /api/team/unattended`, the code moved out of `useSessionRoots`),
writes the answer into the cache and returns it; `null` when the read fails or the capability is missing. The daemon
read lives in `root-cache` / the lifecycle only — nobody else writes one. Tests: cache hit; miss then daemon read;
daemon unreadable) and `startSessionRootsLifecycle`
(the read / debounce / once-per-session logic moved out of `useSessionRoots`; reads while the effective task-group view
is on; gate `team.relay_quota.v1`; drops a host's entries on removal / daemon-id mismatch, and entries no tab of any
world refers to). `useSessionRoots` becomes a thin reader. Tests: offline host still answers; cleanup keeps parked-world
references.

## TG-2b — the sidebar on the new store

1. `view-model.ts` / `TaskGroupView.tsx`: manual groups from `useManualGroupsStore` in `order`, membership by (host,
   root); no per-host blocks, no `unsafe` rows. Tests updated.
2. `drag.ts` without the same-host rule and `row-menu.ts` listing every group, both on the new actions. The callers
   (「加入／移到編組」) get the root with `resolveRoot` and show `no-root` only when it answers `null`. Wiring test: a cache
   miss reads the daemon, and only when the daemon cannot answer either is it `no-root`. Tests.
- **Split** if over: task 2 goes to its own PR.

## TG-2c — delete the old path

`lib/task-groups/api.ts`, `loader.ts`, `edits.ts`, `actions.ts`, `useTaskGroupsStore.ts`, their tests and strings, the
lifecycle hook in `main.tsx`. A guard test that nothing imports them. Pure deletion: only additions count toward the
line limit (about 14 files).

## TG-3a-1 — the inclusive seat map (team-module files: 1f releases)

`lib/team/team-index.ts`: the inclusive seat map beside `byTabId`; `panelTeam` (`team-structure.ts`) reads it. Tests: a
pinned seat tab routes to the team panel; **the `byTabId` readers stay on `byTabId`** — the pin guard (`isTeamTab`), the
team tab lifecycle, the workspace view's member collapse, `buildTeamDisplay`'s marks and beads, the run drawing — each
pinned by an unchanged test.

## TG-3a-2 — one definition of a tab's group, one order

1. `lib/task-groups/effective-view.ts`: the reader of spec §3. Tests: top + wide, both + wide, both + narrow, left +
   wide, the stored view off.
2. `lib/task-groups/placement.ts`: `groupOfTab` (§5; watcher-area skip by `isWatchTabId`). Tests: a split tab with a
   solo primary and a seat secondary is the team's; a pinned team tab; an ended agent keeps its group.
3. `lib/task-groups/group-order.ts`: `groupTabs(groupKey, ctx)` in the order of §6.2 with `compareGroupTabs`. Tests: a
   team with the lead pinned and unpinned; a seat with two tabs; plain tabs after the members; tabs from two workspaces
   with equal ranks give the same order whichever is read first.
4. `buildGroupView` uses `groupOfTab` and `groupTabs` (replacing #3654's first-live-pane rule). Tests.

## TG-3b — view store, selection, the automatic write

1. `stores/useGroupViewStore.ts` (device-local): `selectedGroup`, `lastActiveTab[groupKey]`, `expanded[groupKey]` (team
   keys reconciled per host after its roster is read, manual keys against the store), `lastByView`. It joins the host
   re-resolve / removal rewrite (`planRewrite`, rollback, re-read). Tests incl. a host recognised and removed.
2. The selection subscriber on `activeTabId` (spec §6.2): selection, the group's last active tab, the view's last
   selection; nothing for a watcher-area tab or none. Tests: a click, a notification, a rehydrate from a second window
   (two stores over one storage), an apply that removes the active tab — each updates; a watcher-area tab does not.
3. The fresh-tab write (spec §5): `createTab` adds to a `WeakSet`; `addTab` and `detachPane` place a fresh tab in the
   same `set()`. Tests — placed: ⌘T, + in the bar, `openSingletonTab` (settings, files), a browser tab, a notification
   opening a non-seat session, a pane detached into a tab, a History reopen; the selection stays on the group after
   each; the placed tab has no `groupRank` and is last in `groupTabs` even when the group already ends with unranked
   tabs of another workspace. Not placed: an Electron single-tab transfer and a workspace transfer, ⌘⇧T reopen, a rehydrate, an apply, a
   world switch, the workspace view, the narrow sidebar, 「未分組」 selected, a seat tab, a watcher-area tab.
4. Guard test: the production callers of `createTab` are a listed set, each handing the tab to `addTab` or
   `detachPane`.
- **Split** if over: tasks 3–4 go to their own PR.

## TG-3c — headers and selection from the sidebar

1. `TaskGroupHeader` / `TeamGroupHeader`: chevron, header click (select / toggle), chevron toggles, focus ring only; the
   three manual group states of §6.1. Tests.
2. Selecting from the sidebar activates the group's last tab, else `groupTabs(…)[0]`; an empty group sets the active tab
   to none. Tests.
3. `HomeRow` toggle restores the last workspace / group and its tab, with the fall-back (§6.3). Tests.

## TG-3d — header menu and +

1. Manual group header menu: rename, colour, 「刪除任務編組」 with a confirm. Tests.
2. `BottomNav` in the task-group view (wide activity bar only; the narrow one behaves as the workspace view, spec §6): 「新增任務編組」. Tests.

## TG-3e — drag in the sidebar

Drag decisions (pure) and wiring with dnd-kit like `InlineTabList`: rank writes (a drop among unranked tabs also ranks
the unranked tabs before the drop point, spec §4.2), seat moves (all its tabs → `memberOrder`), plain tabs after members,
moves to a manual group / 「未分組」, no drop on teams. Tests incl. a drop between two unranked tabs.
- **Split** if over: the pure decisions first, the wiring second.

## TG-4a — the range

`getVisibleTabIds(…, view)` returning `groupTabs(selectedGroup)` in the effective task-group view. Direct callers:
`App.tsx` and `useShortcuts` (`visibleTabIds` without the member-collapse filter in the task-group view, ⌘1–9,
prev/next, ⌘W `ownedElsewhere`). App's downstream readers — `useTabWorkspaceActions` close slices, the empty state at
`App.tsx:254`, `features/workspace/hooks.ts:337`, the merge-into-tab targets — each get a regression test. The watcher
area keeps priority. A property test: for random groups the range equals the bar's order.
- **Split** if over: `useShortcuts` to its own PR.

## TG-4b — the bar's order in the task-group view

`TabBar` group mode from `groupTabs`: zones of §6.2 (lead, seats with all their tabs, plain tabs by rank), no collapse,
no run frame; empty group. Tests (a collapsed team still lists every tab; a seat with two tabs; tabs from two
workspaces).
- **Split** if over: the empty group and zones' styling second.

## TG-4c — drag in the bar in the task-group view

Lead fixed, nothing before it, seat moves with its tabs (`memberOrder`), plain tabs by rank, pinned zone by rank; a drop
among unranked tabs (the same pure decision as TG-3e); a re-rank at the cap. Tests.

## TG-5 — personal panel

5a design pass on a real screen (Opus), shown to the user; 5b `panel-view.ts` case + strip + one-line form (absorbing
`OwnTitleStrip`); 5c full form and settings on TG-2a actions, tab-switch survival test.

## TG-6 — cleanup (1f)

Daemon route, key, capability, `taskgroups.go`; NP2 L9 / §7 / §12; `sync-inventory.html` S19.

## Order and ownership

TG-1a-1 → 1a-2 → 1b-1 → 1b-2 (stacked, one release) → 2a → 2r → 2b → 2c → 3a-1 → 3a-2 → 3b → 3c → 3d → 3e → 4a → 4b →
4c — one Sonnet member. TG-3a-1 needs 1f's release as well as 88's. TG-5a by the lead after TG-2a; 5b/5c after TG-3b.
TG-6 after TG-4c ships.
