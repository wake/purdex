# Task groups as a workspace-level layer — spec (2026-10-20)

Owner: B line lead (88). Status: draft r2 — revised after the plan review (codex `task-mvfs2clf-c2dr4g`: a new world
section needed its own world fence, slave copy remap and apply fence; r2 rides the existing settings and tabs sections
instead, which already have all three).

The sidebar's task-group view (name pool v2 spec, NP2 §7, U11) stops being a second listing of the same tabs and becomes
a layer **parallel to workspaces**: a group has its own tabs in the top bar, its own tab order, its own active tab, and it
is collapsed, selected and created the way a workspace is. Manual groups leave the hosts' `host_config` and travel with
the profile (Profile Sync) under the same per-section conflict handling as everything else there.

## 1. User decisions (2026-10-20, after the 761 acceptance)

Asked plainly with examples, answered by the user. Quoted where the wording matters.

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

Bugs reported with it: a session shown by two tabs was listed twice (the guardian tab) and 「未分組」 looked reordered when
the seat's chosen tab switched — fixed by #3654 (every tab showing a seat's session is the team's); a team seat with no
open tab shows no avatar and no persona name — TG-0b.

## 2. What this replaces

| Where | Old text | Now |
|---|---|---|
| NP2 L9 | "A manual task group only gathers sessions of one host. **[待確認]** — stored per host, so no cross-host sync is needed" | Replaced: a group gathers sessions of **any** host; groups are stored in the profile (§4). |
| NP2 §7, first bullets | groups in hostconfig `task_groups`, capability `task_groups.v1`, CAS PUT per host | Replaced by §4. The hostconfig row is retired (§8). |
| NP2 §7 | "groups in creation order"; "A group with none collapses and offers 「清除」"; "Clicking a session switches to its workspace and tab" | Kept, extended by §6 (chevron, header click, the group's tabs). New groups are appended, so creation order is the default order. |
| NP2 §12 item 4 | "A manual task group holds sessions of one host only (L9)" | Dropped. |
| NP2 §9.3 / §9.6 | the muted title after the name goes through `labelOf` | In the task-group rows only: the tab's title first (D5). Panel, beads, tooltips unchanged. |
| TI §4.4 | "3. otherwise nothing" (the panel area) | A solo agent session shows the personal panel (§7). |
| `getVisibleTabIds` | "never wider than one workspace" | In the task-group view the range is the selected group (§6.2). |

NP2 L10 stays: membership is keyed by lineage root, and a session in a live team shows under its team only.

## 3. Words

- **Group**: a team (from the daemons' rosters, never stored by the App), a manual task group (stored, §4.1), or
  「未分組」 (implicit).
- **Group key** (one module, `lib/task-groups/group-key.ts`, serialize and parse): `team\0<wire host id>\0<team id>`,
  `group\0<group id>`, or `ungrouped`. NUL separates the parts, as `teamKeyOf` does. The team part uses the **wire** host
  id (`host-identity.ts` `wireIdOfHost`, `d1_…`; the local id for a host with no daemon id), so one key means one team on
  every device and the same string is used everywhere — stored, synced and device-local alike. `TeamView` (local host
  id) is converted once, where a key is made from it.
- **A tab's session**: the first live `tmux-session` pane of the tab in pre-order (the primary pane first) — exactly
  `SessionTab` today (#3654). A **session tab** is one whose session has an agent session id
  (`rebuild.agent.sessionId`), so its lineage root can be known. Every other tab — UI pages, editors, a plain shell —
  is a **plain tab**.

## 4. Storage and sync

### 4.1 Group definitions: a settings store

A new persisted store `purdex-task-groups` (`useTaskGroupsStore` rewritten), listed in `PROJECTIONS.settings`, so it
travels in the profile's `settings` section like the other synced settings:

```ts
interface TaskGroupsState {
  order: string[]                                   // manual group ids, sidebar order (new groups appended)
  groups: Record<string, {
    name: string                                    // 1–64 bytes, printable (today's rule)
    color?: string                                  // #rrggbb
    members: Array<{ host: string; root: string }>  // WIRE host id + lineage root; a (host, root) is in at most one group
    createdAt: number
  }>
}
```

- **Wire ids in the store itself**, like `purdex-host-looks.looks` and `purdex-shown-hosts.ids`: the builder and the
  applier pass them through verbatim, readers resolve wire → local with the host identity. A member whose host this
  device does not know is kept and simply matches nothing here.
- Limits 32 groups, 64 members each (today's); group ids 8 hex (`newGroupId`).
- `SECTION_SCHEMA_ORDINAL.settings` 11 → 12; the guard snapshot moves with it. An ordinal-11 payload lacks the store and
  leaves it alone on apply (the ordinal-9/10 precedent).
- **Master and slave**: settings belong to the master and a slave borrows them (Profile Sync §4.1), so every world on a
  device sees the master's groups. Edits made while a slave is on screen edit those groups.
- **No daemon change and no capability**: the `settings` section already exists on every daemon.

### 4.2 A tab's place: two tab fields

Each tab gets two optional fields, listed in `PROJECTIONS.tabs` (`tabs.*.group`, `tabs.*.groupRank`), so they travel
in the tab's own `tabs.<workspace>` section:

- `group?: string` — the tab's **placement**: a group key (§3). Read only when the derived rules do not place the tab
  (§5).
- `groupRank?: number` — the tab's position in its group's bar (§6.2). Lower first; a tab without a rank comes after the
  ranked ones, in workspace order.

Riding the tabs section means the existing mechanisms carry them with no new code paths: the world fence and switch
(`switch-active.ts`), 「複製為 slave」 (`copyWorld` copies the tab, fields included; group keys hold no tab ids, so nothing
needs remapping), and local-only tabs (`tabs-local-only`: an interface tab stays on its device with its fields).
`SECTION_SCHEMA_ORDINAL.tabs` 4 → 5; an ordinal-4 payload lacks both fields, and applying it keeps the local values
(upcast, like `upcastLegacyTabs`).

### 4.3 Conflicts

Unchanged mechanism: group edits lock and resolve as part of `settings`, tab placement and order as part of their
`tabs.<workspace>` section — 「留本機」/「用同步版本」 in Settings › Profile and, for tabs, the workspace row's alert
(`WorkspaceConflict`). The grain is coarse (a group rename and an unrelated settings change on two devices at once lock
`settings`); accepted.

### 4.4 Old clients

Both ordinals move, so a client that predates this locks `settings` and `tabs` with `locked:schema` until it updates —
the standing rule for every projection change. All of this user's App clients load the SPA from the dev server, or
update with each release.

### 4.5 Root cache

A session's lineage root never changes, so the App keeps the roots it reads (`GET /api/team/unattended`) in a
**device-local persisted** map `(local host id, agent session id) → root`, dropped with the host (the task-groups
lifecycle's forget rule). It is read without a React hook too (placement, auto-join). Reads are gated on the endpoint's
own capability `team.relay_quota.v1`, no longer on `task_groups.v1`. A tab whose host is offline keeps its group.
Writing a member still needs a root read from the daemon (NP2 §7: never a session's own id).

## 5. Which group a tab is in

First match wins:

1. The tab's session is a live team seat's session → that team (#3654).
2. The tab is a session tab whose (wire host, root) is a member of a manual group → that group.
3. Its `group` field names a group that exists (a team key whose team is live, a manual group in the store) → there.
4. 「未分組」.

The `group` field is written:
- **when a tab is created by the user** (the tab factory used by every open path, never by apply, rehydrate, restore or
  a world switch): in the task-group view the selected group; in the workspace view the group of the tab that is active
  at that moment; none → not set;
- **when a tab is moved** to another group in the sidebar (§6.1);
- **when a tab stops matching rules 1–2** (its session ended, the agent exited, it left a team): the group it was in,
  but only when that group was known (a root that was read); an unknown root never writes 「未分組」 over a placement.

Auto-join: a tab whose `group` is manual group G, that becomes a session tab whose root is known and in no group, adds
(host, root) to G ("a session started inside a group joins it").

Tabs of the watcher area other than session tabs are in no group: they live in the watcher area only.

## 6. The task-group view

### 6.1 Sidebar

- Order: teams (roster order), manual groups (`order`), 「未分組」 last (left out when empty) — as today.
- **Header**: a chevron (CaretDown/CaretRight, `aria-expanded`) replaces the count. Clicking the header selects the
  group (§6.2); clicking the header of the selected, expanded group collapses it — `WorkspaceRow`'s rule. The chevron
  alone toggles without selecting. Focus shows only as `focus-visible:ring-1 focus-visible:ring-border-active`.
  Double-click renames (manual) / opens `TeamEditPopover` (team), as today.
- Expanded state per device, keyed by group key, reconciled against live keys like `workspaceExpanded`. A manual group
  with nothing to show stays collapsed and offers 「清除」 (NP2 §7); it has no chevron.
- Right-click on a manual group header: rename, colour (the team palette), 「刪除任務編組」 (its sessions fall to
  「未分組」, tabs placed in it lose the placement; asks first when it holds tabs).
- **Rows** are draggable as in the workspace view:
  - within a group, a drag reorders (writes `groupRank`); in a team the lead never moves and nothing drops before it,
    members reorder among themselves (writes this device's `memberOrder`), plain tabs among themselves after the members;
  - onto another manual group: a session tab becomes its member (root required), a plain tab gets `group`;
  - onto 「未分組」: takes it out (membership removed / `group` cleared);
  - teams take no drop, and team seats do not leave their team (NP2 L10). Any host's session can go into any group.
- Row text (TG-0b): the persona name `text-text-secondary` (hover/active as today); the muted title is the tab's title,
  the pdx label only for a seat with no tab; a seat with no tab shows its avatar and persona name, dimmed.
- **+** in the bottom nav, in this view: 「新增任務編組」 — creates 「任務編組 N」 (first unused N), appends it, selects
  it and starts its rename. The workspace view's + is unchanged.

### 6.2 Selected group and the top bar

- Per device: `selectedGroup` (a group key) and each group's last active tab (like `Workspace.activeTabId`).
- Selecting a group shows its tabs in the top bar and activates its last active tab, else its first; an empty group
  shows the empty state, as an empty workspace does.
- Activating a tab any other way (notification, search, a panel click) selects the tab's group.
- The bar of a **manual group or 「未分組」**: pinned tabs first (as in a workspace), then every tab by `groupRank`.
- The bar of a **team**: pinned first, then the lead, then the members in the device's member order (`memberOrder`,
  the same order as the panel and the workspace view's team run), then the plain tabs placed in the team by
  `groupRank`. Like a workspace's team run, the members stay together: a plain tab dropped among them lands after them,
  never before the lead. The run frame is not drawn — the whole bar is the team.
- The same range drives close-others, close-right, ⌘1–9, prev/next and the merge-into-tab targets
  (`getVisibleTabIds` becomes view-aware; `App.tsx` passes the view).
- New tabs are still inserted into a workspace (the active tab's workspace), so the workspace view is unaffected; the
  team tab lifecycle (`startTeamTabLifecycle`) keeps running on workspaces as today.

### 6.3 Switching views (D3)

Per device, the last workspace selected in the workspace view and the last group selected in the task-group view.
Switching views restores that one and its last active tab; if it no longer exists, the view selects the workspace /
group of the current tab.

## 7. Personal panel (D6)

For a solo **agent** session (not a team seat) the panel area and the title bar show a personal panel built from the
team panel's parts:
- Title-bar strip / one-line form: the group's capsule (its name and colour; 「未分組」 grey) and the session's avatar and
  context ring.
- Full form: one card like the lead's (avatar, persona name, title, context %, the last status line), a workbook entry
  where the session has one (it absorbs today's `OwnWorkbookPanel` / `OwnTitleStrip`), and a settings block: the group
  (move to a group, take out, new group), the group's name and colour.
- The layout is fixed in a design pass on a real screen (Opus, the 10-16 rule) and shown to the user before it is built.

## 8. Retiring the hostconfig `task_groups`

Checked 2026-10-20 (read-only): neither mlab's nor air26's `host_config.db` has a `task_groups` row, and mserv has no
pdx daemon. Nothing is imported. The SPA stops reading and writing `PUT /api/hostconfig/task-groups` in TG-2b; the
daemon's route, `task_groups.v1` and `taskgroups.go` go in TG-6, with the NP2 text and the sync inventory's S19 row.

## 9. Phases

Each ≤ 800 lines / ≤ 20 files including tests. All SPA except TG-6.

| Phase | Scope |
|---|---|
| TG-0b | Sidebar rows: seat rows without a tab get avatar + persona name; persona name colour; title prefers the tab's title; header outline. (TG-0a = #3654, merged.) |
| TG-1a | `group-key.ts`; the `purdex-task-groups` store (wire members) in the settings projection, ordinal 12; guard and integration tests. No consumer. |
| TG-1b | Tab fields `group` / `groupRank` in the tabs projection, ordinal 5 with the upcast; copy-to-slave and local-only carry tests. No consumer. |
| TG-2a | Edits and actions on the new store (empty group allowed, cross-host members); root cache (§4.5). |
| TG-2b | The view reads the new store (no per-host blocks, no `unsafe` rows); drag and row menu across hosts; the SPA's hostconfig path removed. |
| TG-3a | `useGroupViewStore` (selected group, last tab per group, expanded, last per view); header chevron / click / focus; view-switch memory. |
| TG-3b | Placement (§5): `groupOfTab`, the creation stamp, the transition stamp, auto-join. |
| TG-3c | Header menu (rename, colour, delete); + new group. |
| TG-3d | Row drag in the sidebar (ranks, member order, moves). |
| TG-4a | View-aware range: `getVisibleTabIds` and every caller (App, shown-hosts, watch area), close range, shortcuts, merge targets. |
| TG-4b | The top bar in the task-group view (lead first, member run, plain tabs by rank, drag rules); activation selects the group; empty state. |
| TG-5a / 5b / 5c | Personal panel: design pass (user sees it) / routing and strip / full form and settings. |
| TG-6 | Daemon cleanup (route, capability, `taskgroups.go`), NP2 text, sync inventory — 1f. |

## 10. Risks and open points

1. Settings-grain conflicts for group edits (§4.3) — accepted.
2. A host without a daemon id has no stable wire id: its members and team keys match only on the device that wrote them.
3. Member order stays per device (`memberOrder`, TI); a team's bar order of members can differ between devices. Plain
   tab ranks sync.
4. Ranks are numbers between neighbours; when two are too close the group's ranks are renumbered, which writes every
   tab of the group (possibly several `tabs.<workspace>` sections).
