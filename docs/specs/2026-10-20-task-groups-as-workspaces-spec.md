# Task groups as a workspace-level layer — spec (2026-10-20)

Owner: B line lead (88). Status: draft r4.
- r1 → r2: codex plan review `task-mvfs2clf-c2dr4g` (a new world section needed its own world fence, slave-copy remap and
  apply fence) → groups ride the existing `settings` section, a tab's place rides its own tab.
- r2 → r3: an independent Opus review (codex quota exhausted until 10-25): explicit field copy and type checks in the tabs
  applier, "absent = cleared", local ids in stores with wire translation at the section edge, string ranks, no automatic
  writes besides one, a single definition of a tab's group, PRs split to size.
- r3 → r4: the same reviewer's incremental pass: members sorted in wire form at build time, both new places in the host
  re-resolve / removal rewrite, the automatic write inside the `setActiveTab` action (never a rehydrate), bounded ranks,
  pinned seat tabs counted as the team's everywhere.

The sidebar's task-group view (name pool v2 spec, NP2 §7, U11) stops being a second listing of the same tabs and becomes
a layer **parallel to workspaces**: a group has its own tabs in the top bar, its own tab order, its own active tab, and it
is collapsed, selected and created the way a workspace is. Manual groups leave the hosts' `host_config` and travel with
the profile (Profile Sync) under the same per-section conflict handling as everything else there.

## 1. User decisions (2026-10-20, after the 761 acceptance)

| # | Decision |
|---|---|
| D1 | In the task-group view the top bar shows **the selected group's tabs**, all of them, in order. The lead's tab is first and cannot be dragged; every other tab can. "和原本 workspace 模式下一樣". |
| D2 | Tabs that are not sessions (settings, file editor, …) are handled **as a workspace handles them**: a tab belongs to the group it was opened in and is dragged within the bar like any other tab — never before the lead. |
| D3 | Switching between the workspace view and the task-group view shows **the last workspace / the last group viewed in that view**. |
| D4 | The persona name in a task-group row is only slightly dimmer than hover/active, not muted. |
| D5 | The muted text after the persona name prefers **the tab's title**; the pdx label only when the seat has no tab. |
| D6 | A session that is not in a team gets a **personal panel** shaped like the team panel, where its task group can be edited (§7). |
| D7 | 「team / 任務編組，就是和 workspace 平行對等的層級。應該要跟著 profile sync 走到全 client 裝置，提供一樣的衝突處理覆蓋機制」. In the task-group view the sidebar's + creates a task group, as + creates a workspace in the workspace view. |
| D8 | A group header collapses and expands like a workspace's (a chevron replaces the count), has no outline, and a click on it selects that group. |

Bugs reported with it: a session shown by two tabs was listed twice — fixed by #3654; a team seat with no open tab showed
no avatar and no persona name — TG-0b (#3662). D4, D5 and the outline are TG-0b too.

## 2. What this replaces

| Where | Old text | Now |
|---|---|---|
| NP2 L9 | "A manual task group only gathers sessions of one host. **[待確認]** — stored per host" | Replaced: a group gathers sessions of **any** host; groups are stored in the profile (§4). |
| NP2 §7, first bullets | groups in hostconfig `task_groups`, capability `task_groups.v1`, CAS PUT per host | Replaced by §4. The hostconfig row is retired (§8). |
| NP2 §7 | "groups in creation order"; "A group with none collapses and offers 「清除」"; "Clicking a session switches to its workspace and tab" | Kept and extended by §6. |
| NP2 §12 item 4 | "A manual task group holds sessions of one host only (L9)" | Dropped. |
| NP2 §9.3 / §9.6 | the muted title after the name goes through `labelOf` | Task-group rows only: the tab's title first (D5, #3662). |
| TI §4.4 | "3. otherwise nothing" (the panel area) | A solo agent session shows the personal panel (§7). |
| `getVisibleTabIds` | "never wider than one workspace" | In the task-group view the range is the selected group (§6.2). |
| #3654 | a tab's session for the team claim = its first live tmux pane | Any pane that shows a seat (§3, one function for the sidebar and the bar). |

NP2 L10 stays: membership is keyed by lineage root, and a session in a live team shows under its team only.

## 3. Words

- **Group**: a team (from the daemons' rosters, never stored by the App), a manual task group (stored, §4.1), or
  「未分組」 (implicit).
- **Group key** (`lib/task-groups/group-key.ts`, make / parse): `team\0<TeamView.key>` (that is `team\0<local host
  id>\0<team id>`), `group\0<group id>`, or `ungrouped`. Stores on the device hold the **local** form, so a team's group
  key and its `memberOrder` / `collapsed` key (`TeamView.key`) convert by adding or removing the prefix. Only the
  section builders and appliers see wire ids (§4).
- **A tab's seat**: the first pane in pre-order whose session is a live team seat's session, **pinned tabs included**.
  `buildTeamIndex` gains this map beside `byTabId` (which keeps excluding pinned tabs for the workspace view's run
  drawing, TI); the sidebar, the group bar, the selection and the panel routing (`panelTeam`) read the inclusive one, so
  a pinned seat tab is the team's everywhere and shows the team panel.
- **A tab's agent session**: the first `tmux-session` pane in pre-order carrying `rebuild.agent.sessionId`, live or
  ended. The id stays on the pane after the agent exits, so a tab keeps its root — and its group — after its session
  ends. A tab with neither a seat nor an agent session is a **plain tab**.
- **Watcher-area tabs** (the `watch` workspace) are in no group: never listed in the task-group view, never in a group's
  bar. They live in the watcher area.

## 4. Storage and sync

### 4.1 Group definitions: a settings store

A new store `useManualGroupsStore` (storage key `purdex-task-groups`, a new file — the old per-host
`useTaskGroupsStore` stays untouched until TG-2c deletes it), registered with `syncManager`, listed in
`PROJECTIONS.settings`:

```ts
interface ManualGroupsState {
  order: string[]                                   // manual group ids, sidebar order (new groups appended)
  groups: Record<string, {
    name: string                                    // 1–64 bytes, printable (today's rule)
    color?: string                                  // #rrggbb
    members: Array<{ host: string; root: string }>  // LOCAL host id + lineage root; a (host, root) in at most one group
    createdAt: number
  }>
}
```

- **Host ids**: local in the store; the settings builder writes them as wire ids (`sections.ts`, beside the
  `purdex-host-settings` branch) and `settingsFromWire` reads them back. A wire id this device does not know is kept as
  received and matches nothing until the host is known. The store joins the host re-resolve rewrite (`host-reresolve.ts`
  `planRewrite` and its re-read list), so a kept wire id becomes local when the host is recognised, and a removed host's
  local id becomes its wire id (`host-lifecycle.ts` → `rewriteHostRefs`), exactly like the other host-bearing stores.
- Limits 32 groups, 64 members each; group ids 8 hex. **Members are sorted by (wire host, root) when the section is
  built**, after translation — local ids differ per device, so sorting in the store would make two devices rebuild
  different payloads and push each other's back forever. The store only removes duplicates.
- `SECTION_SCHEMA_ORDINAL.settings` 11 → 12. An ordinal-11 payload has no `purdex-task-groups`: applying it clears the
  store (the ordinal-11 precedent, "absent = cleared"); the executor only lands an older-shape payload on a clean local
  section (`executor.ts` header), so nothing local is lost.
- **Master and slave**: settings belong to the master and a slave borrows them, so every world sees and edits the
  master's groups. A group deleted while a slave is on screen leaves the parked master tabs' `group` naming a group that
  no longer exists — rule 3 (§5) then ignores it.

### 4.2 A tab's place: two tab fields

Two optional tab fields, listed in `PROJECTIONS.tabs` (`tabs.*.group`, `tabs.*.groupRank`):

- `group?: string` — the tab's **placement**, a group key (§3). Read only when rules 1–2 (§5) do not place the tab.
- `groupRank?: string` — the tab's position in its group's bar: a fractional-index string (`lib/task-groups/rank.ts`,
  `rankBetween(a, b)`), so moving one tab writes one tab. Inserting again and again at one spot lengthens the string, so
  the generator keeps ranks ≤ 32 characters: at that point it re-ranks the group's tabs evenly (rare; several tab
  writes). The payload check accepts up to 128 — one shared constant module holds both limits. No rank → after the ranked
  tabs, in workspace order.

The tabs applier copies them explicitly (`applyTabs` builds each tab field by field: `group` / `groupRank` are copied when
present, **absent = cleared**), and `isTabsPayload` checks their types (`string`, the limits above; `group` must parse
as a group key) — before any translation runs. The team host id inside `group` is translated like the pane layouts: to
wire in the builder beside `layoutToWire` (`sections.ts`), back in `tabsFromWire` beside `layoutFromWire` (run by
`apply-to-stores` before `applyTabs`), and the re-resolve / host-removal rewrite (`rewriteTabsHosts`, parked worlds)
rewrites it with the layouts. Riding the tab, the
fields are carried by the world switch (park / restore pass the tab objects), 「複製為 slave」 (`structuredClone`; group
keys name no tabs, so nothing is remapped), local-only interface tabs (kept whole on apply), and the Electron tab
transfer between windows (the tab's JSON). `SECTION_SCHEMA_ORDINAL.tabs` 4 → 5, "absent = cleared" as in §4.1.

Phone writers: the iOS app appends tabs to `tabs.<workspace>` under a gate that compares the existing tabs with the SOT
(qr-pairing spec §5.2). Before TG-1b ships, purdex-ios is checked to carry unknown tab fields through unchanged, and the
shared fixture gets a tab with `group` / `groupRank`.

### 4.3 Conflicts

Unchanged mechanism: group edits lock and resolve as part of `settings`, tab placement and order as part of their
`tabs.<workspace>` section — 「留本機」/「用同步版本」 in Settings › Profile and, for tabs, the workspace row's alert. The
grain is coarse (a group rename and an unrelated settings change on two devices at once lock `settings`); accepted.

### 4.4 Old clients and the ordinals

Both ordinals move when TG-1a / TG-1b merge, so a client older than that locks `settings` / `tabs` with `locked:schema`
until it updates — the standing rule for a projection change. All of this user's App clients load the SPA from the dev
server or update with each release, so the window is short; it is accepted even though the first consumers land later.

### 4.5 Roots

A session's lineage root never changes, so the App keeps the roots it reads (`GET /api/team/unattended`) in a
device-local persisted cache `(local host id, agent session id) → root` (`lib/task-groups/root-cache.ts`, plain
reader `rootOf`). A lifecycle of its own — not the sidebar view — reads the roots of the session tabs whenever the
task-group view is on, gated on the endpoint's capability `team.relay_quota.v1`; it drops a host's entries when the host
is removed or its daemon id mismatches, and entries no tab of any world (on screen or parked) refers to. Writing a member
still needs a root read from the daemon (NP2 §7: never a session's own id).

## 5. Which group a tab is in

`groupOfTab(tab, ctx)` (`lib/task-groups/placement.ts`, pure), first match wins:

1. The tab has a seat (§3) → that team.
2. The tab's agent session (§3) has a root whose (host, root) is a member of a manual group → that group.
3. Its `group` names a group that exists (a live team, a manual group in the store) → there.
4. 「未分組」.

Watcher-area tabs are skipped before rule 1 (§3).

**The one automatic write** (everything else is a user action). It runs inside the `setActiveTab` **action** of this
window — every call, a repeat of the current id included — through a hook the placement module registers on the tab
store. It never runs for `addTab`'s implicit activation (a tab added while none is active), nor for state that arrives by
persist rehydrate (other windows share `activeTabId` through localStorage), an apply, a restore or a world switch: those
set state without calling the action. When it runs, the **effective** view is the task-group view (§6), and the tab is
matched by none of rules 1–3, has no `group`, is not a watcher-area tab (`isWatchTabId`), and was created less than 10 s
ago by this device's clock, the tab is placed in the selected group (a team or a manual group; never 「未分組」). This
covers every way a tab is opened there — new tab, a pane detached into a tab — without a tab factory, while tabs that
arrive with their own fields (another window, a reopened tab) or old unplaced tabs are left alone.

User writes:
- moving a tab to another group in the sidebar (§6.1): a tab with an agent session becomes a member (root required) and
  loses its `group`; a plain tab gets `group`;
- 「移出」 (onto 「未分組」): the member is removed and the tab's `group` cleared;
- deleting a group: its members go, and the `group` of the on-screen world's tabs naming it is cleared.

## 6. The task-group view

The view exists in the wide sidebar (as today). When the sidebar is narrow or the tabs sit in the sidebar
(`tabPosition: left`), the App behaves as in the workspace view (range, selection), whatever the stored view is.

### 6.1 Sidebar

- Order: teams (roster order), manual groups (`order`), 「未分組」 last (left out when empty).
- **Header**: a chevron (CaretDown/CaretRight, `aria-expanded`) replaces the count. Clicking the header selects the
  group (§6.2); clicking the header of the selected, expanded group collapses it (`WorkspaceRow`'s rule); the chevron
  alone toggles. Focus shows only as `focus-visible:ring-1 focus-visible:ring-border-active`. Double-click renames
  (manual) / opens `TeamEditPopover` (team).
- Expanded state per device, keyed by group key; team keys are reconciled per host once that host's roster is read
  (`useTeamUiStore`'s rule), manual keys against the store.
- A manual group's states:
  - with tabs → listed like any group;
  - with members but no tab and every member's host readable → collapsed with 「清除」 (NP2 §7);
  - with no members and no tabs (e.g. just created) → header plus a muted hint row 「拖曳 session 到這裡」, no 「清除」.
- Right-click on a manual group header: rename, colour (the team palette), 「刪除任務編組」 (asks first when it holds
  tabs or members).
- **Rows** drag as in the workspace view:
  - within a group: reorder (one `groupRank` write); in a team the lead never moves and nothing drops before it; a seat
    moves with all its tabs (writes this device's `memberOrder`); plain tabs reorder among themselves after the members;
  - onto another manual group or 「未分組」: §5's user writes;
  - teams take no drop; team seats do not leave their team (L10). Any host's session can go into any group.
- **+** in the bottom nav, in this view: 「新增任務編組」 — creates 「任務編組 N」 (first unused N), appends it, selects it
  and starts its rename. The workspace view's + is unchanged.

### 6.2 Selected group and the top bar

- Per device: `selectedGroup`, each group's last active tab, and the last selection per view (§6.3). Tab ids are
  world-specific: one that no longer exists falls back as below.
- **Selection follows the active tab**: in the effective task-group view, whenever the active tab changes (a click, a
  notification, search, the panel, ⌘-keys, a rehydrate from another window), `selectedGroup` becomes `groupOfTab` of it
  — after the automatic write when there was one. A watcher-area active tab leaves the selection as it is (the watcher
  area shows instead of the bar).
- Selecting a group from the sidebar activates its last active tab, else its first tab; an **empty group** sets the
  active tab to none and shows the empty state, as an empty workspace does.
- The bar of a **manual group or 「未分組」**: every tab of the group, pinned tabs first, each zone by `groupRank`.
- The bar of a **team**: the lead first (pinned or not — never draggable); then the other pinned tabs by `groupRank`;
  then each seat in the device's member order, a seat's tabs together (its chosen tab first); then the plain tabs placed
  in the team by `groupRank`. The member collapse (`useTeamUiStore.collapsed`) is applied neither to the bar nor to the
  shortcut range here — D1 lists every tab. No run frame.
- The range drives close-others, close-right, ⌘1–9, prev/next, ⌘W's ownership check, the empty state and the merge-into-
  tab targets. `getVisibleTabIds` takes the view; it computes the group range outside React (`currentTeamState()`, the
  group store, the root cache).
- New tabs are still inserted into a workspace (the active tab's), so the workspace view is unaffected; the team tab
  lifecycle keeps running on workspaces as today.

### 6.3 Switching views (D3)

Per device, the last workspace selected in the workspace view and the last group selected in the task-group view.
Switching restores that one and its last active tab; if it is gone, the view selects the workspace / group of the
current tab.

## 7. Personal panel (D6)

For a tab whose agent session is not a seat, the panel area and the title bar show a personal panel built from the team
panel's parts:
- Title-bar strip / one-line form: the group's capsule (name and colour; 「未分組」 grey) and the session's avatar and
  context ring.
- Full form: one card like the lead's (avatar, persona name, title, context %, the last status line), a workbook entry
  where the session has one (it absorbs `OwnWorkbookPanel` / `OwnTitleStrip`), and a settings block: the group (move,
  take out, new group), the group's name and colour.
- The layout is fixed in a design pass on a real screen (Opus, the 10-16 rule) and shown to the user before it is built.

## 8. Retiring the hostconfig `task_groups`

Checked 2026-10-20 (read-only): neither mlab's nor air26's `host_config.db` has a `task_groups` row; mserv has no pdx
daemon. Nothing is imported. The SPA stops reading and writing `PUT /api/hostconfig/task-groups` in TG-2b and deletes the
code in TG-2c; the daemon's route, `task_groups.v1` and `taskgroups.go` go in TG-6, with the NP2 text and the sync
inventory's S19 row.

## 9. Phases

Each ≤ 800 lines / ≤ 20 files including tests. All SPA except TG-6.

| Phase | Scope |
|---|---|
| TG-0b | #3662: seat rows without a tab, persona name colour, title prefers the tab's title, header outline. |
| TG-1a | `group-key.ts`; `useManualGroupsStore` in the settings projection with host translation, wire-sorted members, the re-resolve / removal rewrite; ordinal 12. |
| TG-1b | `rank.ts`; tab fields `group` / `groupRank`: types, setters, projection, ordinal 5, applier copy + type checks, team-host translation and rewrite; the purdex-ios check. |
| TG-2a | New edits and actions on the new store (beside the old ones). |
| TG-2r | Root cache and its lifecycle out of React; `useSessionRoots` as a reader. |
| TG-2b | The sidebar view on the new store (no per-host blocks); drag and row menu across hosts. |
| TG-2c | Delete the SPA's hostconfig path and the old store; guard that nothing imports them. A pure deletion: the line limit counts additions only. |
| TG-3a | `useGroupViewStore`; header chevron / click / focus; group states; view-switch memory. |
| TG-3b | The inclusive seat map in `buildTeamIndex` (panel routing on it); `groupOfTab` (§5) under the sidebar; selection follows the active tab; the one automatic write in `setActiveTab`. Team-module files: 1f releases. |
| TG-3c | Header menu (rename, colour, delete); + new group. |
| TG-3d | Row drag in the sidebar. |
| TG-4a | The range: `getVisibleTabIds` with the view and every caller; the narrow / left rule. |
| TG-4b | The top bar in the task-group view: order, zones, member run, no collapse; empty group. |
| TG-4c | Drag in the top bar in the task-group view. |
| TG-5a / 5b / 5c | Personal panel: design pass (user sees it) / routing and strip / full form and settings. |
| TG-6 | Daemon cleanup (route, capability, `taskgroups.go`), NP2 text, sync inventory — 1f. |

## 10. Risks and open points

1. Settings-grain conflicts for group edits (§4.3) — accepted.
2. The ordinals move before the consumers (§4.4) — accepted.
3. Member order stays per device (`memberOrder`); plain-tab ranks sync.
4. The automatic write's 10 s window: a tab opened and activated later than that, in a group, stays in 「未分組」 until
   the user moves it.
5. Rank re-balancing at the 32-character cap writes several tabs, possibly in several `tabs.<workspace>` sections; it is
   rare by construction.
