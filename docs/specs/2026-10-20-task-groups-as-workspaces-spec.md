# Task groups as a workspace-level layer — spec (2026-10-20)

Owner: B line lead (88). Status: draft for codex review together with its plan.

The sidebar's task-group view (name pool v2 spec, NP2 §7, U11) stops being a second listing of the same tabs and becomes
a layer **parallel to workspaces**: a group has its own tabs in the top bar, its own tab order, its own active tab, and
it is collapsed, selected and created the way a workspace is. Manual groups leave the hosts' `host_config` and travel with
the profile (Profile Sync), with the same per-section conflict handling as workspaces.

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

Two bugs reported with it are fixed in phase TG-0 (§9): a team seat with no open tab shows no avatar and no persona name,
and a session shown by two tabs is listed twice (the guardian tab) — which also made 「未分組」 look reordered on a click.

## 2. What this replaces

| Where | Old text | Now |
|---|---|---|
| NP2 L9 | "A manual task group only gathers sessions of one host. **[待確認]** — stored per host, so no cross-host sync is needed" | Replaced: a group gathers sessions of **any** host; groups are stored in the profile (§4). |
| NP2 §7, first bullets | groups in hostconfig `task_groups`, capability `task_groups.v1`, CAS PUT per host | Replaced by §4. The hostconfig row is retired (§8). |
| NP2 §7 | "groups in creation order"; "A group with none collapses and offers 「清除」"; "Clicking a session switches to its workspace and tab" | Kept, extended by §6 (chevron, header click, the group's tabs). |
| NP2 §12 item 4 | "A manual task group holds sessions of one host only (L9)" | Dropped. |
| NP2 §9.3 / §9.6 | the muted title after the name goes through `labelOf` | In the task-group rows only: the tab's title first (D5). Panel, beads, tooltips unchanged. |
| TI §4.4 | "3. otherwise nothing" (the panel area) | A solo agent session shows the personal panel (§7). |
| `getVisibleTabIds` | "never wider than one workspace" | In the task-group view the range is the selected group (§6.2). |

NP2 L10 stays: membership is keyed by lineage root, and a session in a live team shows under its team only.

## 3. Words

- **Group**: a team (from the daemons' rosters, never stored by the App), a manual task group (stored, §4), or
  「未分組」 (implicit).
- **Group key**: `team:<wire host id>:<team id>`, `group:<group id>`, or `ungrouped`. Team keys use the wire host id
  (`host-identity.ts`, `d1_…`) so that a key means the same team on every device.
- **Session tab**: a tab whose primary live pane is a `tmux-session` with an agent session id (`rebuild.agent.sessionId`),
  i.e. one whose lineage root can be known. Every other tab — UI pages, editors, a plain shell — is a **plain tab**.

## 4. Storage and sync

### 4.1 The `taskgroups` profile section

A fifth `SectionKind`, one section per profile world (key `taskgroups`), built and applied like `workspaces`:

```ts
interface TaskGroupsPayload {
  order: string[]                                  // manual group ids, sidebar order (new groups are appended)
  groups: Record<string, {
    name: string                                   // 1–64 bytes, printable (same rule as today)
    color?: string                                 // #rrggbb
    members: Array<{ host: string; root: string }> // wire host id + lineage root; a (host, root) is in at most one group
    createdAt: number
  }>
  tabOrder: Record<string, string[]>               // group key → tab ids in bar order (a team's lead is never listed: it is first)
  placement: Record<string, string>                // tab id → group key, for plain tabs (§5)
}
```

- Limits: 32 groups, 64 members per group (today's), tab-order and placement entries only for tabs the world has.
- Group ids are 8 hex (`edits.ts` `newGroupId`, as today); two devices creating groups at once do not collide.
- Host ids go through the same wire translation as tab layouts (`layoutToWire` / `layoutFromWire`): the builder takes
  the `HostIdentity`, the applier the `WireResolver`, the section is host-bearing (`collector.scheduleHostBearing`), and
  an identity conflict stops building it like every host-bearing section. A host with no daemon id keeps its local id,
  so its members do not match on another device; this is said in the group's tooltip there ("a host this Mac does not
  know").
- **World part**: the section belongs to the profile world, like `workspaces` and `tabs.*`: `ParkedWorld` gains it, a
  slave profile has its own groups, the collector reads it through `master-world.ts`, and 「複製為 slave」 copies it.
  Rationale: `tabOrder` and `placement` name tab ids, which are world-specific.
- **Projection, fingerprint, ordinal**: `PROJECTIONS.taskgroups` lists the payload's paths; `SECTION_SCHEMA_ORDINAL`
  gets `taskgroups: 1`; the `shapeTable()` guard snapshot moves with it. An older client skips the unknown kind
  ("carried, never rewritten", PS §4.6.3), so it is not locked — it just does not see groups.
- **Pull gate**: none. A `tabOrder` / `placement` entry naming a tab the world does not have (yet) is ignored on read
  and dropped on the next local write.
- **Local-only tabs** (`tabs-local-only` spec: settings, new-tab, dashboard, …) are never in a synced `tabs.*`
  section, so their `tabOrder` / `placement` entries are kept **per device**: the builder leaves them out, the applier
  keeps the local ones (the same split as tabs).

### 4.2 Conflicts

Exactly the workspace mechanism: the section is `synced | pending | locked:conflict | locked:reset | locked:invalid`
(`sync-state.ts`); `ResolveRow` offers 「留本機」/「用同步版本」 for it in Settings › Profile, with its own unit label
and count (`resolve-counts.ts`, `settings.profile.resolve.unit.taskgroups`). The task-group view shows the lock the way
the workspace view does (`WorkspaceConflict`): an alert icon on the view's header row opening the same resolve row.

### 4.3 Daemon and rollout

- `internal/module/profiles/validate.go` `sectionPattern` admits `taskgroups`; the daemon still never reads payloads.
- `/api/info` advertises `profiles.taskgroups.v1`. The SPA builds and pushes the section only when the profile's SOT
  daemon advertises it; before that the groups live in the local store only and the view says nothing (a single
  device works; a second device simply does not see them yet).
- Order: the daemon phase ships and is deployed on every host before the SPA phase that pushes (§9).

### 4.4 Root cache

A session's lineage root is a fact that never changes, so the App keeps the roots it has read (`useSessionRoots`,
from `GET /api/team/unattended`) in a **device-local persisted** map `(wire host, session id) → root`. A tab whose
host is offline keeps its group instead of falling into 「未分組」. Writes still require a root read from the daemon
(NP2 §7's "never stores a session's own id as its root" stays).

## 5. Which group a tab is in

First match wins:

1. The tab shows a live team seat's session (any of the tab's panes, matched by session as `seatLookup` does) → that
   team. Team first: every tab showing a seat is the team's, not only the seat's chosen tab (fixes the double listing).
2. It is a session tab whose (host, root) is a member of a manual group → that group.
3. It has a `placement` entry naming a group that exists → that group.
4. 「未分組」.

Placement is written:
- when a **plain tab** is opened: the group of the tab that was active (in either view); none → 「未分組」;
- when a tab is dragged onto a group in the sidebar (§6.1) and it is not a session tab;
- when a session tab **stops** being one (its session ends, the agent exits): its group at that moment, so it does not
  jump.

When a tab placed in manual group G becomes a session tab whose root is known and in no group, the root is added to G
("a session started inside a group joins it"). Tabs of the watcher area other than session tabs are in no group (they
live in the watcher area only).

## 6. The task-group view

### 6.1 Sidebar

- Order: teams (roster order), manual groups (`order`), 「未分組」 last (left out when empty) — as today.
- **Header**: a chevron (CaretDown/CaretRight, `aria-expanded`) replaces the count. Clicking the header selects the
  group (§6.2); clicking the header of the selected, expanded group collapses it — `WorkspaceRow`'s rule. The
  chevron alone toggles without selecting. Focus shows only as `focus-visible:ring-1 focus-visible:ring-border-active`
  (no UA outline). Double-click renames (manual) / opens `TeamEditPopover` (team), as today.
- Expanded state per device, keyed by group key, reconciled against the live keys like `workspaceExpanded`. A manual
  group with nothing to show stays collapsed and offers 「清除」 (NP2 §7); it has no chevron.
- Right-click on a manual group header: rename, colour (the team palette), 「刪除任務編組」 (its sessions and tabs go to
  「未分組」; asks first when it holds tabs).
- **Rows**: dragging reorders within the group (writes `tabOrder`; a team's lead row never moves and nothing drops
  before it; moving a member also writes this device's team member order, so the panel follows). Dragging a solo row
  onto another manual group moves it there (session tab: membership, root required; plain tab: placement); onto
  「未分組」 takes it out. Teams take no drop; team rows do not leave their team (NP2 L10). Any host's session can go
  into any group.
- Row text: the persona name `text-text-secondary` (hover/active as today); the muted title is the tab's title, the
  pdx label only for a seat with no tab (D4, D5). A seat with no open tab shows its avatar and persona name like the
  others, dimmed (TG-0).
- **+** (bottom nav) in this view: 「新增任務編組」 — creates 「任務編組 N」 (first unused N), appends it, selects it and
  starts its rename. In the workspace view + is unchanged.

### 6.2 Selected group and the top bar

- Per device: `selectedGroup` (a group key) and each group's last active tab (like `Workspace.activeTabId`).
- Selecting a group shows its tabs in the top bar and activates its last active tab, else its first tab; an empty
  group shows the empty state, as an empty workspace does.
- Activating a tab in any other way (notification, search, a click in the panel) selects the tab's group.
- The bar lists the group's tabs: pinned first (as in a workspace), then `tabOrder[groupKey]`, then tabs not yet in
  the order in workspace order. For a team: the lead first, never draggable, nothing drops before it (TI R4/R5
  semantics inside the group). The team-run frame of the workspace view is not drawn here — the whole bar is the team.
- The same range is used by close-others, close-right, ⌘1–9 and prev/next (`getVisibleTabIds` becomes view-aware).
- New tabs are still inserted into a workspace (the active tab's workspace), so the workspace view is unaffected.

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
- The exact layout is fixed in a design pass on a real screen (Opus, the 10-16 rule) before it is built.

## 8. Retiring the hostconfig `task_groups`

Checked 2026-10-20 (read-only): neither mlab's nor air26's `host_config.db` has a `task_groups` row, and mserv has no
pdx daemon. So nothing is imported. The SPA stops reading and writing `PUT /api/hostconfig/task-groups` in the storage
phase; the daemon's route, `task_groups.v1` and `taskgroups.go` are removed in the last phase, together with the NP2
text and the sync inventory's S19 row ("每台一份" → profile).

## 9. Phases

Each ≤ 800 lines / ≤ 20 files including tests; stacked where noted.

| Phase | Scope | Notes |
|---|---|---|
| TG-0 | Sidebar rows: team-first claim of every tab showing a seat; seat rows without a tab get avatar + persona name; persona name colour; title prefers the tab's title; header outline. | SPA. The first item is the PR in flight (`fix/task-group-team-session-dedupe`). |
| TG-1 | Daemon: `sectionPattern` admits `taskgroups`; `profiles.taskgroups.v1` in `/api/info`. | Daemon → 1f releases and deploys. |
| TG-2a | Profile plumbing: `SectionKind`, projection, ordinal, builder, applier, guard, wire ids, `ParkedWorld`, resolve label/count, capability gate. No consumer. | SPA. |
| TG-2b | `useTaskGroupsStore` on the section (world part, local + synced); actions rewritten on it; cross-host members; root cache; TaskGroupView reads it; hostconfig reads/writes removed. | SPA, after TG-1 is deployed. |
| TG-3 | Sidebar: header chevron/click/selection, expanded state, per-view memory, + new group, header menu (rename, colour, delete), row drag reorder, placement rules §5. | SPA. |
| TG-4 | Top bar: view-aware range, group order, lead rule, shortcuts and close range. | SPA. |
| TG-5 | Personal panel: design pass, then build. | SPA. |
| TG-6 | Cleanup: daemon `task_groups` route and capability, NP2 text, sync inventory. | Daemon → 1f. |

## 10. Risks and open points

1. `tabOrder` / `placement` name tab ids; local-only tabs need the per-device split of §4.1 — the applier must not
   wipe local entries when it applies a remote payload.
2. One section for all groups means two devices editing groups at once lock the whole section (the same grain as
   `workspaces`). Accepted.
3. A host without a daemon id: its members match only on the device that added them (§4.1).
4. Team member order stays per device (TI); the bar's order of a team group syncs. A drag in the bar updates this
   device's member order; another device keeps its own panel order until it drags.
