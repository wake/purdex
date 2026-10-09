# Team interface — how lead / member teams look in the Mac App — spec

Owner: interface line (purdex-88). Status: user-confirmed 2026-10-08 → 10-09 (19 rulings + prototype rounds v2 – v5k). Plan: `docs/plans/2026-10-09-team-interface-plan.md`.

**Round 2 (user 2026-10-10):** no team pills on the top bar or in the left list, no top-bar collapse, a live trial of the group shadow (R7, R8, P6, P11, §4.1, §4.2, §4.3, §4.8, §5; each marked *round 2*).

**Amended 2026-10-09** by the session workbook spec (`docs/specs/2026-10-09-session-workbook-spec.md` §10.1, a later user decision): the team panel shares one resizable panel area with the workbook. Changed here: R16, R19, P3, P5, §3, §4.4, §4.11, §5, §6 (each marked *amended*); the TI-3 screenshot gate (same day) amended §4.1, §4.3 and §5 to the prototype (left-list capsule, collapse plate, muted tick). The panel part is built as workbook plan WA-2a (`docs/plans/2026-10-09-session-workbook-plan.md`), which replaces TI-4.

**Supersedes** (display parts only; the data parts stay): `docs/specs/2026-10-08-lead-adopt-release-spec.md` D-U24-5 / D-U24-6; `docs/specs/2026-10-08-unattended-adopt-plan.md` PL-2c, PL-3a, PL-3b and "User-visible behaviour" 31–45 (its "Display-first reorder" section already pointed here); `docs/specs/2026-10-08-team-display-confirm-pack.md` (never answered — the user stated the requirements directly instead); `docs/pages/team-groups-mock.html` (hand-drawn, replaced by the SPA prototype).

**Visual reference:** the SPA prototype on branch `worktree-team-ui` at `f61748aa` (`spa/proto-team.html`, `spa/src/proto/team/*`, real components in `spa/src/components/team/*`). Every value in §5 is that prototype's default after 重設. Where this text and the prototype disagree, this text wins and the prototype is fixed.

---

## 1. Goal

A lead and its members (U23 teams) read as one unit in the App: in the top tab bar, in the left tab list, and in a floating team panel; and a lead's own terminal says it is in lead mode. Clients only render; the daemon roster is the source (§3).

## 2. User decisions (do not reopen)

Numbered as recorded (memory `kickoff_team_interface`); later rounds override earlier ones where they conflict.

**Left list** (exists only when the tab position is left or both)
- R1 Under the lead row, one horizontal row of **every** member (with or without a tab).
- R2 *(v1 round, replaced)* each bead = bot icon + light, no capsule, no name (name in the tooltip).
- R3 Clicking a member: no tab → open it in the lead's group; has a tab → switch to it (never a second tab).
- R4 Drag only within one lead; dropped outside it snaps back; never into another workspace.
- R5 One member order shared by the left beads, the top group and the panel; dragging anywhere updates all three; the lead is always first.
- R6 A member that is released or ends: with a tab → the tab leaves the group and becomes a normal tab (not closed); without a tab → it disappears.

**Top group**
- R7 The group starts with the team's **label** (§4.8), then the lead's tab, then the members' tabs. *Round 2 (user 2026-10-10): no label on the top bar — the group starts with the lead's tab.*
- R8 Collapsible: collapsed shows the label and the lead only; stepping through tabs (left / right) skips the hidden members. *Round 2 (user 2026-10-10): the top bar has no collapse control; the group collapses only from the left list (§4.3), and the top bar follows that shared state (collapsed → only the lead's tab shows, stepping skips the hidden members).*
- R9 Collapsing while a member tab is active switches to the lead.
- R10 Opening a member (beads, panel) while collapsed expands the group and switches to it.
- R11 A member opened by hand from the session list also opens inside the lead's group.
- R12 Tabs in a group cannot be pinned (the pin item is disabled).
- R13 Each team gets its own colour, distinct from host colours.

**Lead terminal**
- R14 The Purdex mod shows "lead mode · N members" at the bottom of Claude Code's screen, in the lead's terminal only.

**Floating team panel**
- R15 Hangs from the top of the content area; lists lead + members; shown while the active tab is any tab of the group (lead or member); the active tab's row is marked active.
- R16 Each row: tab title, model, context use, light + state, how many subagents it is running; clicking a row opens / switches to that tab. *Amended (workbook §10.1): where the host has the workbook, clicking a row opens that seat's workbook and the row's bot icon opens / switches to the tab (§4.4).*
- R17 Expanded by default when a lead is created; expanded / one-line is remembered **per team, on this device only**.
- R18 One-line mode: one cell per person (light, model icon, context ring) + the team name.
- R19 *(replaced 2026-10-09 by workbook §10.1)* Full-mode rows carry 「正在做的任務」: the first sentence of the conversation's latest workbook `status`; no workbook → no line (§4.4).

**Prototype rounds (2026-10-09)**
- P1 Model icon shapes: ◆ Opus, ● Sonnet, ▲ Haiku, ★ Fable (may be tuned later).
- P2 Closing the lead's tab closes the group's tabs; every session keeps running; reopening the lead brings the group back with the members unopened.
- P3 The panel cannot be closed (one line is the minimum). *Amended (workbook §10.1): this holds for the team view; a workbook opened from a tab's 「工作簿」 toggle closes with that toggle (§4.4).*
- P4 Left: the hang-under mark is not a left border line; beads hang from the lead's **bot icon**; with several rows each row has its own tick at the same x.
- P5 Panel = a sidebar-like list, top to bottom, not a table; layout "C": line 1 subagent dots → bot → host chip → title; line 2 model / context; active row styled like the sidebar's active row (no side line); no host name; normal text brightness; no top colour bar; full and one-line have the same width; one-line wraps to a second row when the team is large. *Amended (workbook §10.1): the panel area is resizable and has an expanded mode, so "the same width" now means full and one-line both take the area's current width.*
- P6 Team name: given with the lead request, editable by the approver, shown at the group's start (Chrome-group style). **Team label** (short, ≤ 10 display width) is on the group; the **name** is in the panel (§4.8). *Round 2 (user 2026-10-10): name and label pills are no longer shown on the top bar or in the left list; they stay in the team panel's header.*
- P7 "bot / bot + host icon" for beads is a **user setting**; default **bot + host icon**.
- P8 Beads use normal colour whether or not the member has a tab (no fading), and **no** "has a tab" mark.
- P9 Sidebar collapse style: no caret; collapsed = a members icon + one main light per member; clicking that line expands; clicking the tick or the blank part of the bead area collapses.
- P10 Hang-under mark = **tree tick** (one stem, a tick per row, the last row turns), **small rounded corner**, starting at the lead block's lower edge (not blended into it).
- P11 Group cue on the top bar = **team-coloured narrow "lifted button" shadow toward the top-right**, 1 px, colour depth 70 %, on **every** tab of the group; plus a **very faint** team-colour wash on those tabs; **no separator lines** inside the group or after its last tab. Rejected and not to be revived: underlines, tinted plates, frames, top bars, coloured dots, corner folds, badges, bookmarks, edge lines, glow shadows, a bottom-left companion shadow. *Round 2 (user 2026-10-10): "the top-right-only shadow looks like it floats; let the shadow spread a little to the left and bottom — just a try". The user reopened the shadow's spread (glow / bottom-left) for a live trial: §4.2 Cue lists the variants; the others in the rejected list stay rejected.*
- P12 Empty label fallback = the lead's title, cut to ≤ 10 display width with "…", full title in the tooltip (user chose "C" 2026-10-09).

## 3. Data (what the App reads)

- **Roster**: `team.roster` WS frames → `useTeamRosterStore.byHost` (`spa/src/lib/team/roster.ts`, `roster-ws.ts`), the whole host roster per frame; nothing else is fetched.
- **Views**: `useTeamViews(memberOrder?)` / `selectTeamViews` (`spa/src/lib/team/team-views.ts`) → `TeamView{key, hostId, teamId, createdAt, colorIndex (fnv1a32(teamId) % 8), lead: Seat, members: Seat[]}`, `Seat{role, session: RosterSession, state, origin, joinedAt, label, tabId, workspaceId, paneIndex}`; members ordered by `memberOrder[key]` then `joined_at`.
- **Tab membership**: `teamOfTab({views, tabId, tabsById, sessionsByHost})` (same file) — decided from the tab's own panes; the primary pane's team wins.
- **Name / label**: `TeamRoster.team_name` (parsed today), `TeamRoster.team_label` (parsed by the team-label line's TL-2, `docs/specs/2026-10-09-team-label-spec.md` D-L10; `""` when absent). This spec adds both to `TeamView` (`name`, `label`). *Panel edit (user 2026-10-10):* `TeamRoster.team_color` — `0–7` (an index into `TEAM_COLORS`) or absent / `null` = automatic; `TeamView.colorIndex` uses it when set (§4.1). Written by the daemon's team edit (TR-1, §4.12).
- **Per-seat readings**: `RosterSession.model`, `.effort`, `.context{used_percentage, window, model_id, effort}`; lights, unread and subagents from `useAgentStore` keyed by `(hostId, tmux session code)` — the code is the session list entry whose `name` equals `RosterSession.tmux_session` (the same match `shownSessions` makes).
- **Device-local UI state** (new, persisted on this device, never synced): member order per team, collapse per team, panel mode per team, the ghost row's workspace per team, the bead setting (§4.9). Entries of a team are pruned when a roster frame from its connected host no longer lists it (the team ended) or when its host is deleted — never on a disconnect, an endpoint change or an App reload. *Amended (workbook §10.1):* also the panel area's width and expanded flag (one pair for the area), the workbook drilled into per team (§4.4; pruned with the team's other entries and by the back control), and the 「工作簿」 toggle per tab (pruned when the tab closes).
- **Workbook readings** *(amended)*: the latest `status` per conversation and the entries, from the workbook data layer (workbook plan WA-1, `useWorkbookStore`, host events `workbook.entry` / `workbook.status`); present only on a host whose `/api/info` lists `workbook.v1`.
- **Mod read (new, §4.10)**: `GET /mod/v1/team?session_id=<sid>` on the mod socket → `{role: "lead" | "member" | "none", members: <active member count>}`.

## 4. Behaviour

### 4.1 Membership and colour
- A tab belongs to a team iff `teamOfTab` says so. Role = that result's role.
- Colour = `TEAM_COLORS[colorIndex]`, `colorIndex` = the team's chosen `team_color` when set, else `fnv1a32(teamId) % 8` (*panel edit, user 2026-10-10*) — eight colours away from the host blue and the four light colours (`#a78bfa #2dd4bf #f472b6 #fb923c #e879f9 #a3a3ff #5eead4 #fda4af`). The shadow, the wash and the panel accents use it (round 2: no label capsule on the top bar or in the left list). The left list's tick does not (§5, muted; *amended at the TI-3 screenshot gate*).
- A team with no open tab and a closed lead tab still exists in the sidebar as a ghost row (§4.6) and in no other surface.

### 4.2 Top tab bar group
- Order inside a workspace's normal (unpinned) zone: the group sits where its lead tab is; inside it: label → lead → members in team order (R5). The workspace's own tab order is kept equal to what is shown: the group's tabs are one contiguous run starting at the lead, in team order (re-normalised whenever membership, team order or the tab list changes), so tab stepping, ⌘1–8 and the sidebar all agree with the bar. Member tabs of a team whose lead tab is in another workspace are not grouped (they stay where they are; `chooseTab` already prefers the lead's workspace).
- ~~**Label**: a capsule in team colour, text = §4.8 label; clicking it toggles collapse (R8). Collapsed: the label shows `+N` (hidden member tabs) next to the text.~~ *Round 2: removed — no label and no collapse control on the top bar.*
- **Cue** (P11): every tab of the group gets the narrow shadow and the wash (§5); separators inside the group and after its last tab are hidden; the separators before the group's first tab and after the group follow today's rule (hidden next to the group).
- **Shadow trial** *(round 2, user 2026-10-10)*: a temporary setting 設定 → 介面 → 分頁 「（試用）群組陰影」 (device-local, `useTeamUiStore`) switches the group shadow live between: **V0** today's `1px -1px 0 <c70>`; **V1** V0 + a soft halo `0 0 4px <c30>`; **V2** V0 + a bottom-left spread `-1px 1px 3px <c40>`; **V3** a fuller lift `1px -1px 0 <c60>, -2px 2px 5px <c35>` (`<cNN>` = the team colour at NN % in oklab, the §5 light-theme darkening applies). Default V2. Wash and separators unchanged. After the user picks, the setting is removed and the pick becomes §5's value. *Ended (user 2026-10-10): none of V1–V3 was kept; the group shadow stays V0 and the setting is removed.*
- **Collapse** (R8–R10, *round 2*): one collapse state per team, set only from the sidebar (§4.3); the top bar has no control and follows it. Collapsed hides member tabs on the top bar; left/right tab stepping skips them; collapsing while a member is active activates the lead (R9); any open-member action expands first (R10).
- **Drag**: tabs reorder within the group only (members; the lead stays first); a drop outside snaps back; dragging a non-group tab into the group is refused (snap back); the group as a whole is not draggable in v1.
- **Pin** (R12): the context menu's pin item is disabled for group tabs (tooltip: why). A pinned tab is never grouped.
- **Release / end** (R6): the tab leaves the group at the next roster frame and becomes a normal tab right after the group (a consequence of the normalised order); never closed by this.
- Existing tab indicators (light, unread, approval hand, host badge, U1-3 corner symbol) are unchanged on group tabs.

### 4.3 Left tab list (tab position left or both)
- The lead's row is today's row. Member tabs are **not** listed as rows; they are beads (R1).
- ~~**Label capsule** *(amended at the TI-3 screenshot gate, as in the prototype)*: the team's label capsule sits above the lead's row; clicking it toggles the shared collapse; tooltip "<name> (<label>)"; on a ghost row it is faded like the row.~~ *Round 2 (user 2026-10-10): removed. Two teams are told apart by structure (lead row, tick, beads); no team colour in the left list. Collapse stays here only (P9: the collapse line, the tick, the blank bead area).*
- **Beads**: one row of beads under the lead row, team order, wrapping to more rows; bead = bot icon (agent type icon) with its light dot, plus the host icon when the setting says so (P7); unread overlays as today; tooltip = the member's title. No opened/unopened difference (P8).
- **Tick** (P4, P10): one stem from just under the lead's bot icon, starting at the lead block's lower edge; a horizontal tick per bead row; the last row turns with a 3 px radius; 1 px line in the muted text colour at 70 %.
- **Click** a bead: R3 (open into the group / switch), R10 (expand if collapsed).
- **Drag** beads to reorder (R4, R5): within the row(s) of that lead; outside snaps back.
- **Collapse** (P9): collapsed = a members icon followed by one main light dot per member, on the line under the lead, with a rounded hover plate (none at rest), as in the prototype; clicking that line expands; clicking the tick or blank bead area collapses. Shared state with the top group.
- A member whose lead has no tab: shown as beads under the ghost lead row (§4.6).

### 4.4 Floating team panel *(amended: one panel area shared with the workbook, workbook §10.1)*
- **Panel area**: one area anchored at the top of the content area, above the pane, never covering the tab bar. It shows a **team view** (below) or a **workbook view** (workbook spec §10.1). Its width is set by dragging its edge (clamped; the bounds are in the plan) and an **expanded** mode, toggled from the area's header, takes most of the content area; width and expanded are one device-local pair for the whole area and survive tab switches and reloads.
- **What the area shows** for the active tab, first match wins:
  1. the tab's 「工作簿」 toggle is on → that tab's conversation's workbook;
  2. the tab is a tab of a team (R15) → that team's team view, or the workbook drilled into from it (remembered per team);
  3. otherwise nothing.
  Switching tabs never clears either memory, so switching away and back shows the same thing (tab-hosted rule).
- **Full mode** (P5): header = team colour accent + team **name** (§4.8) + member count + a control to switch to one-line; rows top to bottom: lead (fixed first), then members in team order; row line 1 = subagent dots → bot icon (light) → host chip → title; line 2 = model icon + model name, effort, context (ring + percent); *amended (R19)* line 3 = 「正在做的任務」, the first sentence of the conversation's latest `status`, one line with an ellipsis, the whole `status` on hover; no line when the conversation has no workbook or its host lacks `workbook.v1`. Active row (the active tab's seat) styled as the sidebar's active row. Members draggable (R5); the lead is not.
- **One-line mode** (R18): one cell per person — bot + light, context ring around the model shape (P1) — then the team name; wraps to a second row when it does not fit; takes the area's current width, like full mode (P5).
- **Header height** *(user 2026-10-10)*: the header row has one fixed height, the same in full and one-line mode (full mode's header height); one-line cells are sized to fit it (the ring at full mode's 20 px). Toggling never moves the name, the switch or the expand control; when the cells wrap, the extra rows sit below the header row without changing its height.
- **Click** *(amended)*: a full-mode row on a host with `workbook.v1` → that seat's workbook in the same area, with a back control to the team view; the row's bot icon → R3 + R10 (open / switch the tab). A row on a host without `workbook.v1`, and every one-line cell → R3 + R10 as before.
- **Ended members** (workbook §10.1: an ended member still opens its workbook): a seat that leaves the team's roster (released, killed, gone — R6) is remembered on this device per team (newest first, at most 20, forgotten with the team's other entries, §3). Full mode shows them as a collapsed 「已結束 (N)」 group at the bottom of the team view; clicking one opens its workbook (back returns). A drilled-in seat that ends keeps its workbook view until back. A team that ended entirely has no panel, so its members' workbooks have no way in in v1 (no standalone page, workbook spec §10). *(88's ruling, shown to the user.)*
- **Header click** *(user 2026-10-10)*: a single click anywhere on the panel header toggles full ⇄ one-line (the small switch control stays); the header's other controls (expand, the switch itself) and the resize edge do not toggle. On the team name, a single click waits the double-click interval before toggling, so a double-click never toggles. **Double-click the team name** → the edit popover (§4.12).
- **Mode memory** (R17): per team key, device-local; a team not seen before starts in full mode. The team view has no close control (P3); a workbook opened by a tab's toggle closes with that toggle, which returns a team tab to its team view.
- Values missing from the roster (a lead's model, an unknown context) show as "—", never as 0.

### 4.5 Opening and closing tabs
- **Open a member** (bead, panel, session list — R3, R10, R11): if it has a tab → activate it; else open a tab for its tmux session inserted after the group's last tab in the **lead tab's workspace**; if the lead has no tab, the lead's tab is opened first (in the ghost row's workspace, else the active one) and the member after it.
- **Session list** (R11): `openSessionTab` learns where a member goes; a non-team session opens as today.
- **Close the lead tab** (P2), by **any** path (tab ✕, ⌘W, context menu, a terminated / ended pane's close, closing a workspace): every tab of the group closes too (no history entries for the members); sessions keep running; the team becomes a ghost row (§4.6). A **locked** member tab is not closed (the lock wins); it stays open as a normal tab until the lead's tab is back, then rejoins the group. Closing a member's tab only closes that tab; it stays a member (bead unchanged — P8).

### 4.6 Ghost lead row
- A team whose lead tab is closed shows, in the sidebar of the workspace the lead tab was closed from, a faded lead row with its beads. Clicking the lead row reopens the lead tab there (the group comes back, members unopened — P2); clicking a bead opens that member (§4.5). The ghost disappears when the team ends. The workspace is remembered device-locally per team.

### 4.7 Order (R5)
- `memberOrder[teamKey]` (session ids), device-local, written by any of the three drags, read by `useTeamViews(memberOrder)`; members not in it follow by join time; stale ids are ignored.

### 4.8 Name and label (P6, P12)
- **Group label** = `team_label`; if `""`: the lead's title (`Seat.label`) cut to ≤ 10 display width by the shared width rule (`spa/src/lib/textwidth.ts`, team-label D-L1) with "…" appended (the "…" counts within the 10), full title in the tooltip. A label that is not `""` is shown whole (the daemon guarantees ≤ 10).
- **Panel name** = `team_name`; if `""`: the lead's title, not cut.
- Tooltips on the label and the panel header show both: "<name> (<label>)" when both exist.
- *Round 2 (user 2026-10-10):* the label is shown only in the team panel's header (the top bar and the left list carry no pill); the fallback rule above still applies there.

### 4.9 Settings
- 設定 → 介面 → 分頁: "member 顆粒顯示主機圖示" (bot + host icon), default on (P7). Stored in `useTeamUiStore` — device-local, not synced by Profile Sync (unlike the other interface settings); moving it into the synced UI settings needs a settings-ordinal bump and is left for later.
- The old "分頁群組顯示 team" toggle (PL-3a) is **dropped**: the user's rulings make teams always grouped.

### 4.10 Lead mode in the terminal (R14)
- Rendered by the Purdex mod through `ui.render` `{component: 'SessionMode'}` — the dim mode labels at the right of Claude Code's prompt footer (2.1.294 types: "A hook adds a mode by rewriting `modes`"): appends `lead mode · N members` (`1 member` singular; `0 members` shown too) while this session is a lead. Alternatives if the user wants a separate row after seeing it: `PromptHint.tail` or the `AbovePrompt` band — decided on a live screenshot during implementation.
- Data: the mod asks the daemon over its own mod socket, `GET /mod/v1/team?session_id=<sid>` → `{role, members}` (`members` = members whose state is `active`; the mod socket otherwise only takes `POST /mod/v1/events`, U1 spec §6.1; the TCP `/api/team` needs a token and an `origin_inbox` the mod does not have). Asked on `session.start` and every 15 s; role `lead` → the label; any other role or a failed read → the last good value (no label before the first good read).
- Mod rules M-U1-2 / M-U1-3 / M-U1-5 hold: the hook lives in `events.js` (the interface line's file, which already owns the socket client and a `ui.render{component:'ToolUse'}` hook) and registers `ui.render` **with** the `{component: 'SessionMode'}` matcher; `register.js` is not touched. The daemon read is a small addition to the mod socket (the team module answers it; agreed with purdex-1f, whose module it reads).

### 4.11 Not in v1
- ~~Renaming a team after approval (team-name D-N5)~~ — *now in scope (user 2026-10-10), §4.12*; syncing order / collapse / panel mode across devices; dragging a whole group; light-theme polish (the user does not use it); merging dots across panes (spec U1 §7 limit).

### 4.12 Editing a team's name, label and colour *(user 2026-10-10)*
- **Where**: double-click the team name in the panel header (full or one-line mode). Not on the top bar or the left list (they carry no pill, round 2).
- **What**: a small popover under the header: **名稱** (team name, the daemon's team-name rules), **短標籤** (label, ≤ 10 display width with a live width hint, the team-label rules), **顏色** (the eight `TEAM_COLORS` swatches + 「自動」 = the team-id colour). 儲存 / Enter saves, 取消 / Esc / a click outside discards. Saving sends all three values (the roster's plus the edits).
- **Stored by the daemon, the same on every device** (Macs, iPhone, `pdx team`): TR-1 `PUT /api/team/appearance` (all three values every time; a label left empty is derived from the name); the new values arrive back through the next `team.roster` frame (the popover closes on the 200; the panel shows the roster's values, never a local copy). A 400 shows the daemon's message under the field; a 409 (`not_live`: the team ended meanwhile) closes the popover with a toast.
- **Hosts**: the edit goes to the lead's host (the team's host). A daemon without the edit capability (`team.edit.v1` absent) → double-click does nothing (no popover).
- The new colour applies everywhere the team colour is used (panel accent, group shadow and wash); the label is used where §4.8 says (the panel header's tooltip).

## 5. Final visual values (prototype defaults at `f61748aa`)

| What | Value |
|---|---|
| Shadow direction / width | top-right, crisp `1px -1px 0 <c>` (no blur) — *round 2: V0 — the §4.2 Shadow trial ended 2026-10-10, none of V1–V3 kept* |
| Shadow colour depth | `color-mix(in oklab, <team> 70%, transparent)`; light theme first darkens the team colour `color-mix(in oklab, <team>, black 25%)` |
| Shadow scope | every tab of the group (lead included) |
| Wash | dark `color-mix(in oklab, <team> 6%, transparent)`, light 8 % |
| Separators | hidden inside the group and after its last tab |
| Label | team-colour capsule, dark text — *round 2: team panel header only (not on the top bar, not in the left list)* |
| Tick | rail, 3 px corner, from the block's lower edge, muted text colour at 70 %, 1 px; no team colour (the capsule carries it) |
| Beads | bot + host icon (setting), no open mark |
| Sidebar collapse | members icon + one light per member, rounded hover plate, none at rest (prototype value) |
| Panel | full by default, layout C; both modes take the resizable area's current width *(amended)* |
| Model icons | ◆ Opus ● Sonnet ▲ Haiku ★ Fable |

## 6. Coordination

- Data: team-label TL-2 (1f line) parses `team_label` before the interface reads it; the interface PR that needs it waits for TL-2 or reads `""`.
- U1-3c (lights over all panes, corner symbol) touches `SortableTab`, `TabIcon`, `useTabDisplay`, `renderInlineTabIcon`, `InlineTab`; the interface PRs that touch the same files start after U1-3 merges.
- The mod line (§4.10) adds a read to the mod socket answered by the team module (purdex-1f's) → agreed with purdex-1f before its PR; `register.js` is not touched.
- *Amended:* the panel area (§4.4) is built as workbook plan WA-2a in place of TI-4, after WA-1 (the workbook data layer); without a `workbook.v1` host it behaves as the team panel alone.
