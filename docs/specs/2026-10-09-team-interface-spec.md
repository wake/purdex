# Team interface — how lead / member teams look in the Mac App — spec

Owner: interface line (purdex-88). Status: user-confirmed 2026-10-08 → 10-09 (19 rulings + prototype rounds v2 – v5k). Plan: `docs/plans/2026-10-09-team-interface-plan.md`.

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
- R7 The group starts with the team's **label** (§4.8), then the lead's tab, then the members' tabs.
- R8 Collapsible: collapsed shows the label and the lead only; stepping through tabs (left / right) skips the hidden members.
- R9 Collapsing while a member tab is active switches to the lead.
- R10 Opening a member (beads, panel) while collapsed expands the group and switches to it.
- R11 A member opened by hand from the session list also opens inside the lead's group.
- R12 Tabs in a group cannot be pinned (the pin item is disabled).
- R13 Each team gets its own colour, distinct from host colours.

**Lead terminal**
- R14 The Purdex mod shows "lead mode · N members" at the bottom of Claude Code's screen, in the lead's terminal only.

**Floating team panel**
- R15 Hangs from the top of the content area; lists lead + members; shown while the active tab is any tab of the group (lead or member); the active tab's row is marked active.
- R16 Each row: tab title, model, context use, light + state, how many subagents it is running; clicking a row opens / switches to that tab.
- R17 Expanded by default when a lead is created; expanded / one-line is remembered **per team, on this device only**.
- R18 One-line mode: one cell per person (light, model icon, context ring) + the team name.
- R19 "Current task" (Haiku summary) is **not** in scope — moved to the separate "session workbook" idea.

**Prototype rounds (2026-10-09)**
- P1 Model icon shapes: ◆ Opus, ● Sonnet, ▲ Haiku, ★ Fable (may be tuned later).
- P2 Closing the lead's tab closes the group's tabs; every session keeps running; reopening the lead brings the group back with the members unopened.
- P3 The panel cannot be closed (one line is the minimum).
- P4 Left: the hang-under mark is not a left border line; beads hang from the lead's **bot icon**; with several rows each row has its own tick at the same x.
- P5 Panel = a sidebar-like list, top to bottom, not a table; layout "C": line 1 subagent dots → bot → host chip → title; line 2 model / context; active row styled like the sidebar's active row (no side line); no host name; normal text brightness; no top colour bar; full and one-line have the same width; one-line wraps to a second row when the team is large.
- P6 Team name: given with the lead request, editable by the approver, shown at the group's start (Chrome-group style). **Team label** (short, ≤ 10 display width) is on the group; the **name** is in the panel (§4.8).
- P7 "bot / bot + host icon" for beads is a **user setting**; default **bot + host icon**.
- P8 Beads use normal colour whether or not the member has a tab (no fading), and **no** "has a tab" mark.
- P9 Sidebar collapse style: no caret; collapsed = a members icon + one main light per member; clicking that line expands; clicking the tick or the blank part of the bead area collapses.
- P10 Hang-under mark = **tree tick** (one stem, a tick per row, the last row turns), **small rounded corner**, starting at the lead block's lower edge (not blended into it).
- P11 Group cue on the top bar = **team-coloured narrow "lifted button" shadow toward the top-right**, 1 px, colour depth 70 %, on **every** tab of the group; plus a **very faint** team-colour wash on those tabs; **no separator lines** inside the group or after its last tab. Rejected and not to be revived: underlines, tinted plates, frames, top bars, coloured dots, corner folds, badges, bookmarks, edge lines, glow shadows, a bottom-left companion shadow.
- P12 Empty label fallback = the lead's title, cut to ≤ 10 display width with "…", full title in the tooltip (user chose "C" 2026-10-09).

## 3. Data (what the App reads)

- **Roster**: `team.roster` WS frames → `useTeamRosterStore.byHost` (`spa/src/lib/team/roster.ts`, `roster-ws.ts`), the whole host roster per frame; nothing else is fetched.
- **Views**: `useTeamViews(memberOrder?)` / `selectTeamViews` (`spa/src/lib/team/team-views.ts`) → `TeamView{key, hostId, teamId, createdAt, colorIndex (fnv1a32(teamId) % 8), lead: Seat, members: Seat[]}`, `Seat{role, session: RosterSession, state, origin, joinedAt, label, tabId, workspaceId, paneIndex}`; members ordered by `memberOrder[key]` then `joined_at`.
- **Tab membership**: `teamOfTab({views, tabId, tabsById, sessionsByHost})` (same file) — decided from the tab's own panes; the primary pane's team wins.
- **Name / label**: `TeamRoster.team_name` (parsed today), `TeamRoster.team_label` (parsed by the team-label line's TL-2, `docs/specs/2026-10-09-team-label-spec.md` D-L10; `""` when absent). This spec adds both to `TeamView` (`name`, `label`).
- **Per-seat readings**: `RosterSession.model`, `.effort`, `.context{used_percentage, window, model_id, effort}`; lights, unread and subagents from `useAgentStore` keyed by `(hostId, tmux session code)` — the code is the session list entry whose `name` equals `RosterSession.tmux_session` (the same match `shownSessions` makes).
- **Device-local UI state** (new, persisted on this device, never synced): member order per team, collapse per team, panel mode per team, the bead setting (§4.9).

## 4. Behaviour

### 4.1 Membership and colour
- A tab belongs to a team iff `teamOfTab` says so. Role = that result's role.
- Colour = `TEAM_COLORS[colorIndex]` — eight colours away from the host blue and the four light colours (`#a78bfa #2dd4bf #f472b6 #fb923c #e879f9 #a3a3ff #5eead4 #fda4af`). The label capsule, the shadow, the wash, the tick's team accents and the panel accents use it.
- A team with no open tab and a closed lead tab still exists in the sidebar as a ghost row (§4.6) and in no other surface.

### 4.2 Top tab bar group
- Order inside a workspace's normal (unpinned) zone: the group sits where its lead tab is; inside it: label → lead → members in team order (R5). Member tabs of a team whose lead tab is in another workspace are not grouped (they stay where they are; `chooseTab` already prefers the lead's workspace).
- **Label**: a capsule in team colour, text = §4.8 label; clicking it toggles collapse (R8). Collapsed: the label shows `+N` (hidden member tabs) next to the text.
- **Cue** (P11): every tab of the group gets the narrow shadow and the wash (§5); separators inside the group and after its last tab are hidden; the separators before the label and after the group follow today's rule (hidden next to the group).
- **Collapse** (R8–R10): one collapse state per team, shared with the sidebar (§4.3). Collapsed hides member tabs; left/right tab stepping skips them; collapsing while a member is active activates the lead (R9); any open-member action expands first (R10).
- **Drag**: tabs reorder within the group only (members; the lead stays first); a drop outside snaps back; dragging a non-group tab into the group is refused (snap back); the group as a whole is not draggable in v1.
- **Pin** (R12): the context menu's pin item is disabled for group tabs (tooltip: why). A pinned tab is never grouped.
- **Release / end** (R6): the tab leaves the group at the next roster frame and becomes a normal tab right after the group; never closed by this.
- Existing tab indicators (light, unread, approval hand, host badge, U1-3 corner symbol) are unchanged on group tabs.

### 4.3 Left tab list (tab position left or both)
- The lead's row is today's row. Member tabs are **not** listed as rows; they are beads (R1).
- **Beads**: one row of beads under the lead row, team order, wrapping to more rows; bead = bot icon (agent type icon) with its light dot, plus the host icon when the setting says so (P7); unread overlays as today; tooltip = the member's title. No opened/unopened difference (P8).
- **Tick** (P4, P10): one stem from just under the lead's bot icon, starting at the lead block's lower edge; a horizontal tick per bead row; the last row turns with a 3 px radius; 1 px line in the muted text colour at 70 %.
- **Click** a bead: R3 (open into the group / switch), R10 (expand if collapsed).
- **Drag** beads to reorder (R4, R5): within the row(s) of that lead; outside snaps back.
- **Collapse** (P9): collapsed = a members icon followed by one main light dot per member, on the line under the lead; clicking that line expands; clicking the tick or blank bead area collapses. Shared state with the top group.
- A member whose lead has no tab: shown as beads under the ghost lead row (§4.6).

### 4.4 Floating team panel
- Shown while the **active tab** is a tab of a team (R15), anchored at the top of the content area, above the pane, never covering the tab bar; one panel (the active tab's team).
- **Full mode** (P5): header = team colour accent + team **name** (§4.8) + member count + a control to switch to one-line; rows top to bottom: lead (fixed first), then members in team order; row line 1 = subagent dots → bot icon (light) → host chip → title; line 2 = model icon + model name, effort, context (ring + percent). Active row (the active tab's seat) styled as the sidebar's active row. Members draggable (R5); the lead is not.
- **One-line mode** (R18): one cell per person — bot + light, context ring around the model shape (P1) — then the team name; wraps to a second row when it does not fit; same width as full mode (P5).
- **Click** a row / cell: R3 + R10.
- **Mode memory** (R17): per team key, device-local; a team not seen before starts in full mode. No close control (P3).
- Values missing from the roster (a lead's model, an unknown context) show as "—", never as 0.
- Not in v1: the "current task" line (R19).

### 4.5 Opening and closing tabs
- **Open a member** (bead, panel, session list — R3, R10, R11): if it has a tab → activate it; else open a tab for its tmux session inserted after the group's last tab in the **lead tab's workspace**; if the lead has no tab, the lead's tab is opened first (in the ghost row's workspace, else the active one) and the member after it.
- **Session list** (R11): `openSessionTab` learns where a member goes; a non-team session opens as today.
- **Close the lead tab** (P2): closes every tab of the group in one action (no history entries for the members); sessions keep running; the team becomes a ghost row (§4.6). Closing a member's tab only closes that tab; it stays a member (bead unchanged — P8).

### 4.6 Ghost lead row
- A team whose lead tab is closed shows, in the sidebar of the workspace the lead tab was closed from, a faded lead row with its beads. Clicking the lead row reopens the lead tab there (the group comes back, members unopened — P2); clicking a bead opens that member (§4.5). The ghost disappears when the team ends. The workspace is remembered device-locally per team.

### 4.7 Order (R5)
- `memberOrder[teamKey]` (session ids), device-local, written by any of the three drags, read by `useTeamViews(memberOrder)`; members not in it follow by join time; stale ids are ignored.

### 4.8 Name and label (P6, P12)
- **Group label** = `team_label`; if `""`: the lead's title (`Seat.label`) cut to ≤ 10 display width by the shared width rule (`spa/src/lib/textwidth.ts`, team-label D-L1) with "…" appended (the "…" counts within the 10), full title in the tooltip. A label that is not `""` is shown whole (the daemon guarantees ≤ 10).
- **Panel name** = `team_name`; if `""`: the lead's title, not cut.
- Tooltips on the label and the panel header show both: "<name> (<label>)" when both exist.

### 4.9 Settings
- 設定 → 介面 → 分頁: "member 顆粒顯示主機圖示" (bot + host icon), default on (P7). Device-local like the other interface settings.
- The old "分頁群組顯示 team" toggle (PL-3a) is **dropped**: the user's rulings make teams always grouped.

### 4.10 Lead mode in the terminal (R14)
- Rendered by the Purdex mod through `ui.render` `{component: 'SessionMode'}` — the dim mode labels at the right of Claude Code's prompt footer (2.1.294 types: "A hook adds a mode by rewriting `modes`"): appends `lead mode · N members` (`1 member` singular; `0 members` shown too) while this session is a lead. Alternatives if the user wants a separate row after seeing it: `PromptHint.tail` or the `AbovePrompt` band — decided on a live screenshot during implementation.
- Data: the mod's `s.role` (from `pdx relay hello`) says lead; N = the live member count from the daemon's `GET /api/team` over the mod socket, refreshed on session start, on each relay hello, and every 15 s while the role is lead; a failed read keeps the last value; not lead → no label.
- Mod rules M-U1-2 / M-U1-3 / M-U1-5 hold: a new hooks file registers `ui.render` with the `SessionMode` matcher; `register.js` (owned by the lead/team line) only gains the import and one call, agreed with purdex-1f first.

### 4.11 Not in v1
- Current task line (R19); renaming a team after approval (team-name D-N5); syncing order / collapse / panel mode across devices; dragging a whole group; light-theme polish (the user does not use it); merging dots across panes (spec U1 §7 limit).

## 5. Final visual values (prototype defaults at `f61748aa`)

| What | Value |
|---|---|
| Shadow direction / width | top-right, crisp `1px -1px 0 <c>` (no blur) |
| Shadow colour depth | `color-mix(in oklab, <team> 70%, transparent)`; light theme first darkens the team colour `color-mix(in oklab, <team>, black 25%)` |
| Shadow scope | every tab of the group (lead included) |
| Wash | dark `color-mix(in oklab, <team> 6%, transparent)`, light 8 % |
| Separators | hidden inside the group and after its last tab |
| Label | team-colour capsule, dark text; `+N` when collapsed |
| Tick | rail, 3 px corner, from the block's lower edge, muted text colour at 70 %, 1 px |
| Beads | bot + host icon (setting), no open mark |
| Sidebar collapse | members icon + one light per member |
| Panel | full by default, layout C, same width both modes |
| Model icons | ◆ Opus ● Sonnet ▲ Haiku ★ Fable |

## 6. Coordination

- Data: team-label TL-2 (1f line) parses `team_label` before the interface reads it; the interface PR that needs it waits for TL-2 or reads `""`.
- U1-3c (lights over all panes, corner symbol) touches `SortableTab`, `TabIcon`, `useTabDisplay`, `renderInlineTabIcon`, `InlineTab`; the interface PRs that touch the same files start after U1-3 merges.
- The mod line (§4.10) touches `register.js` → agreed with purdex-1f before its PR.
