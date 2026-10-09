# Team interface — plan

Spec: `docs/specs/2026-10-09-team-interface-spec.md` (rulings R1–R19, P1–P12; behaviour §4; values §5). Data shipped: `spa/src/lib/team/*` (#1996 / #1997), team name (TN-2). Depends on the team-label line's **TL-2** (`team_label` parse, `spa/src/lib/textwidth.ts`) and on **U1-3** (lights over all panes) for the files both touch.

Format as the U1-3 plan: contracts, rules, named tests and mutation gates; implementers write the code (TDD: each named test is written red first). Line numbers are as of `ff90f472`; re-check before editing.

**Porting rule.** The prototype on `worktree-team-ui` at `f61748aa` already draws every piece with real components (`spa/src/components/team/*`, edits in `TabBar.tsx`, `SortableTab.tsx`, `InlineTabList.tsx`). Port those components, **keeping only the final variant** (spec §5) and deleting every comparison option (`TeamGroupStyle` beyond the shadow, corner / badge / bookmark / edge / companion code, `sidebarStyle`, `hookStyle` other than the rail, `openMark`, `shadowStrength` / `shadowDepth` / `shadowScope` / `labelTint` as options — they become constants). The prototype entry (`spa/proto-team.html`, `spa/src/proto/team/*`) is **not** merged; the branch is kept for later design rounds.

Eight PRs, each ≤ 800 lines diff / ≤ 20 files:

| PR | Content | Depends on | Est. lines |
|---|---|---|---|
| **TI-1a** | foundation: `TeamView.name` / `.label`, label / name rules, `useTeamUiStore` (device-local order / collapse / panel mode / ghost workspace), bead setting, the real `TeamDisplayProvider` (no surface uses it yet) | TL-2 | ~550 |
| **TI-1b** | actions: open a seat (R3, R10, R11, §4.5), collapse (R8, R9), tab stepping and ⌘1–8 skip hidden members, pin disabled (R12), session-list placement | a | ~550 |
| **TI-1c** | tab lifecycle: one subscriber that keeps each group contiguous in team order (R5, R6 placement) and closes the group when the lead tab goes by any path (P2, locks respected) | a | ~450 |
| **TI-2** | top tab bar group: label capsule, shadow + wash, separators, collapse `+N`, drag within the group | 1b, U1-3 | ~600 |
| **TI-3** | left list: beads, tick, sidebar collapse, ghost lead row, bead drag, the setting row | 1b, U1-3 | ~650 |
| **TI-4** | floating team panel: full / one-line, model icons, context ring, row click / drag | 1b | ~550 |
| **TI-5a** | daemon: `GET /mod/v1/team?session_id=` on the mod socket → `{role, members}` | — (agree with 1f) | ~300 |
| **TI-5b** | mod: `lead mode · N members` in the `SessionMode` footer, in `events.js` | 5a | ~250 |

TI-2, TI-3 and TI-4 are independent of each other once TI-1b and 1c are in. TI-5a/5b are independent of the SPA. With one member the order is TI-1a → 1b → 1c → 2 → 3 → 4 → 5a → 5b.

Common rules:
- SPA: `cd spa && npx vitest run <affected files>` during development; **the full vitest once before merge, `--maxWorkers=3`, after asking purdex-1f for the slot**; `pnpm run lint`; `npx tsc -p tsconfig.app.json --noEmit`; `pnpm run build`.
- Tab-hosted checklist (CLAUDE.md): none of these is a tab-hosted component; the panel is mounted in the shell above the pane area, so its state is in stores, not `useState` (mode, order) — tested with a switch-away / switch-back.
- **Screenshot gate** for TI-2 / TI-3 / TI-4 / TI-5: real App screens (dev server on the worktree's own port, `playwright cli -s=<worktree>`) matching the prototype at `f61748aa` side by side, dark theme; the lead reviews them before merge.
- Each task one commit; parallel subagents in one worktree commit with `git commit --only <files>`.

---

## TI-1a — foundation

Files: `spa/src/lib/team/team-views.ts` (+ test), new `spa/src/lib/team/team-names.ts` (+ test), new `spa/src/lib/team/team-index.ts` (+ test), new `spa/src/stores/useTeamUiStore.ts` (+ test), `spa/src/lib/team/roster-ws.ts` (prune ended teams), the host-deletion action (`spa/src/lib/host-lifecycle.ts` delete path — **not** `forgetHost`), `spa/src/stores/useUISettingsStore.ts`, new `spa/src/components/team/team-display.ts` (ported, final variant only), new `spa/src/components/team/TeamDisplayProvider.tsx` (+ test), `spa/src/App.tsx` (mount the provider; no surface reads it yet).

- `TeamView` gains `name: string` (`team_name`) and `label: string` (`team_label`, `""` when absent — parsed by TL-2).
- `team-names.ts`: `groupLabel(view): {text, full, truncated}` — `label` if not `""`; else the lead's `Seat.label` cut to ≤ 10 display width with `textWidth` (TL-2's `spa/src/lib/textwidth.ts`) — the cut keeps whole grapheme clusters and the "…" (width 1) counts inside the 10 (spec §4.8, P12); `panelName(view): {text, unnamed}` — `name`, else the lead's label uncut; `tooltipOf(view)` = `"<name> (<label>)"` when both exist.
- `useTeamUiStore` (zustand `persist`, key `purdex-team-ui`, device-local, not in profile sync): `memberOrder: Record<teamKey, string[]>`, `collapsed: Record<teamKey, boolean>`, `panelMode: Record<teamKey, 'full' | 'line'>` (absent = `full`, R17), `ghostWorkspace: Record<teamKey, string>`; actions `setMemberOrder`, `setCollapsed`, `setPanelMode`, `setGhostWorkspace`, `forgetTeams(hostId, liveTeamKeys)` and `forgetHostTeams(hostId)`. Pruning has exactly two triggers (plan review #5): (1) `handleRosterEvent` after a frame from a connected host → `forgetTeams(hostId, keys in that frame)` (a snapshot after a reconnect also prunes teams that ended meanwhile); (2) the **host deletion** action → `forgetHostTeams`. `forgetHost` (also called on an endpoint / token change and on the WS hook's unmount, `useMultiHostEventWs.ts` ~:90, ~:306) does **not** prune. The `memberOrder` record changes identity only through `setMemberOrder` (other slices are separate keys), so `useTeamViews(memberOrder)` recomputes only on a reorder or a roster / tab change.
- `useUISettingsStore.teamBeadHost: boolean` (default `true`, P7) + setter.
- `team-index.ts` (plan review #6–#8): `buildTeamIndex(views, tabsById, sessionsByHost)` → `{byTabId: Map<tabId, {key, role, seat}>, byKey: Map<teamKey, TeamView>, bySession: Map<hostId\0tmuxName, {key, seat}>}`, built **once** per input change (one pass over seats and tabs, the same name match as `shownSessions`); `teamOfTab` callers in the UI read the index (O(1)), never call `teamOfTab` per tab.
- `TeamDisplayProvider`: two contexts. **Structure** (`tabMark(tabId)`, `sidebarHidden`, `sidebarBeads`, `ghostLeads(workspaceId)`, `panelTeam(activeTabId)`, colours, labels, collapse, order, action callbacks — wired in TI-1b; no-ops here) whose value changes identity only when a **structural signature** changes (team keys, seat ids and roles, tab ids, order, collapse, label / name, colour, ghost workspaces) — a roster frame that only moves a seat's model / effort / context leaves it untouched; **live readings** are not in a context: the panel (TI-4) selects them from `useTeamRosterStore` per seat.

Tests: `team-index.test.ts` — index equals `teamOfTab` for lead / member / non-team / two-teams-in-one-tab tabs; built once per input (spy on the builder). `team-names.test.ts` — label shown whole; empty label → lead title cut (ASCII 12 → 9 + "…"; CJK 6 chars → 4 + "…"; exactly 10 → whole; emoji / ZWJ cluster not split, using `testdata/textwidth/cases.json` cases); panel name fallback uncut; tooltip both. `useTeamUiStore.test.ts` / `roster-ws` / host-lifecycle tests — persist round trip; an ended team's keys pruned by the next roster frame; **kept** on a disconnect, an endpoint repoint (`forgetHost`) and an unmount of the WS hook; pruned on host deletion; `memberOrder` identity unchanged by `setCollapsed` / `setPanelMode`. `team-views.test.ts` — `name` / `label` mapped, `""` when the roster lacks them. `TeamDisplayProvider.test.tsx` — `tabMark` for lead / member / non-team tabs; `sidebarHidden` true for member tabs only; `panelTeam` for a member tab; **a roster frame that only changes a seat's context does not re-render a structure consumer** (render counter); a membership change does.
Mutation gates: cut by UTF-16 length → the CJK / emoji cases red; `"…"` outside the 10 → "exactly 10" red; prune inside `forgetHost` → "kept on an endpoint repoint / unmount" red; context readings in the structural signature → "only changes a seat's context does not re-render" red.

---

## TI-1b — actions

Files: new `spa/src/lib/team/team-actions.ts` (+ test), `spa/src/lib/open-session-tab.ts` (+ test), `spa/src/hooks/useShortcuts.ts` (~:42–60 and `switch-tab-N`), `spa/src/components/TabContextMenu.tsx` (~:83), `spa/src/stores/useTabStore.ts` (`togglePin` guard ~:870), `spa/src/features/workspace/store.ts` (`insertTab` use), `TeamDisplayProvider.tsx` (wire callbacks), tests.

- `openTeamSeat(teamKey, sessionId)` (spec §4.5): seat has a tab → activate (`activateTab`); else open a tab for its tmux session (`createTab` tmux-session content as `openSessionTab` builds it) inserted with `insertTab(id, leadWs, lastGroupTabId)`; lead has no tab → open the lead first in `ghostWorkspace[teamKey]` (else the active workspace) and clear the ghost entry; collapsed → `setCollapsed(false)` first (R10). A seat whose tmux session is not in the host's session list (not yet listed) → no-op with a toast "session 尚未出現在清單" (never a tab to nowhere).
- `openSessionTab(hostId, session)`: if `teamOfSession(hostId, session.name)` finds a **member** seat → `openTeamSeat` (R11); a lead or a non-team session → today's behaviour. `teamOfSession` is a small selector over the views (same name match as `shownSessions`).
- Closing the lead is **not** here: it must hold for every close path, so it is TI-1c's subscriber.
- `toggleTeamCollapse(teamKey)`: expand, or collapse and — when the active tab is that team's member — activate the lead (R9).
- Stepping (R8): `useShortcuts` `prev-tab` / `next-tab` and `switch-tab-N` / `switch-tab-last` work on the **visible** tab list: member tabs of a collapsed team are skipped (a pure `visibleTabIds(tabs, collapsedTeams, teamOfTab)` used by both and by TabBar later).
- Pin (R12): `TabContextMenu` disables pin for a tab in a team (title explains); `useTabStore.togglePin` refuses one too (store-level guard reading the team index, so a shortcut cannot pin it either).

Tests (`team-actions.test.ts`, `open-session-tab.test.ts`, `useShortcuts` tests, `TabContextMenu` test):
`opens an unopened member after the group's last tab in the lead's workspace`; `switches to a member's existing tab (never a second)`; `reopens a closed lead first, in the ghost workspace`; `expands a collapsed group before opening`; `session list opens a member into the group`; `session list opens a lead / non-team session as before`; `collapsing on a member activates the lead`; `next-tab skips hidden members`; `switch-tab-3 counts visible tabs only`; `pin disabled in the menu and refused by togglePin for a team tab`; `unlisted session → toast, no tab`.
Mutation gates: insert after the lead instead of the group's last tab → first test red; drop the collapse expand → "expands a collapsed group" red; step over all tabs → "next-tab skips" red; pin guard only in the menu → "refused by togglePin" red.

---

## TI-1c — tab lifecycle (plan review #2, #3, #9)

Files: new `spa/src/lib/team/team-tab-lifecycle.ts` (+ test), `App.tsx` (start it once), `spa/src/features/workspace/store.ts` (a batch reorder / close used by it, if missing), tests.

- One subscriber, started once at App level, watching the tab store, the workspace store, the team index and `useTeamUiStore.memberOrder`:
  - **Order**: for each workspace holding a lead tab, `normalizeGroupOrder(ws.tabs, group)` = the group's tabs (lead, then members present in that workspace in team order) as one contiguous run at the lead's index; other tabs keep their relative order. Written only when it differs (idempotent — the write triggers the subscriber again and must be a no-op). A tab that left the team (R6) is no longer in the run and therefore sits right after it; a member tab in another workspace is left alone.
  - **Lead gone** (P2): when a lead tab id disappears from the tab store (any path: tab ✕, ⌘W `useShortcuts.ts` ~:57, context menu, `TerminatedPane` / `WorkerEndedPane` close, `WorkspaceSettingsPage` workspace removal), close that team's other tabs in the same workspace with `closeTabInWorkspace(id, {skipHistory: true})`, skipping **locked** ones (they stay as normal tabs until the lead's tab is back — spec §4.5), and `setGhostWorkspace(teamKey, wsId)`. A lead tab moved to another workspace is not "gone".
- Guarded against re-entry (a close it issues does not re-trigger the "lead gone" branch for the same team).

Tests: `group tabs are made contiguous in team order at the lead's index`; `normalisation is a no-op when already in order (no write)`; `a released member's tab ends up right after the group (R6)`; `a member tab in another workspace is not moved`; `closing the lead by ✕ closes the group`; `… by ⌘W (useShortcuts) closes the group`; `… by a TerminatedPane close closes the group`; `… by removing the workspace leaves nothing behind`; `a locked member tab survives and rejoins when the lead reopens`; `members' closes add no history`; `ghost workspace recorded`; `moving the lead tab to another workspace does not close the group`.
Mutation gates: hook only `handleCloseTab` → the ⌘W / TerminatedPane tests red; ignore locks → "locked member survives" red; write on every notification → "no write" red (spy); normalise across workspaces → "other workspace not moved" red.

---

## TI-2 — top tab bar group

Files: `spa/src/components/TabBar.tsx`, `spa/src/components/SortableTab.tsx`, new `spa/src/components/team/TeamTabGroup.tsx` (ported: `TeamTabGroupFrame`, `TeamGroupLabel`, shadow + wash overlay), tests.

- Segments: the normal zone is split into runs; a team run = label + lead + visible members (spec §4.2), built by a pure `groupSegments(tabs, tabMark, collapsed)` (from the prototype's TabBar segment code).
- Label capsule: text / tooltip from `groupLabel`; `+N` when collapsed; click → `toggleTeamCollapse`.
- Each group tab: shadow `1px -1px 0 color-mix(in oklab, <team> 70%, transparent)` (light theme darkens first, spec §5) and wash 6 % / 8 %, drawn by a pointer-transparent overlay so the tab's own background / active highlight stay; separators inside the group and after its last tab hidden (prototype commit `f61748aa`).
- Drag: within the group (members only; the lead stays first) → `setMemberOrder`; out of / into a group → snap back (refused in `handleDragEnd`); pinned zone untouched.
- `SortableTab` gains `group?: TeamTabMark`; its existing indicators are unchanged.

Tests (`TabBar.team.test.tsx`, `groupSegments.test.ts`): `group renders label → lead → members in team order`; `label text and tooltip from groupLabel (empty label → cut lead title + tooltip)`; `collapsed shows label +N and the lead only`; `label click collapses / expands`; `every group tab has the shadow and wash; a non-group tab has neither`; `no separator inside the group or after it`; `member drag reorders memberOrder`; `drag across the group boundary snaps back`; `lead cannot be dragged behind a member`; `pinned tab never grouped`; `member tab of a team whose lead is in another workspace stays ungrouped`.
Mutation gates: shadow on the lead skipped → "every group tab" red; separators kept → "no separator" red; allow cross-boundary drop → "snaps back" red.
Screenshot gate: dark, expanded and collapsed, lead active and member active, next to the prototype.

---

## TI-3 — left list

Files: `spa/src/features/workspace/components/InlineTabList.tsx`, `WorkspaceRow.tsx` (ghost rows), new ported `spa/src/components/team/{TeamSidebarBlock,TeamHook,TeamMemberBeads,TeamSeatIcon,TeamGhostLeadRow,useMemberDrag}.tsx`, `spa/src/components/settings/AppearanceSection.tsx` (the setting row, under 分頁), locale keys, tests.

- `InlineTabList`: member tabs are not rows (`sidebarHidden`); under the lead row a `TeamSidebarBlock` with the beads (`sidebarBeads`), the rail tick (3 px corner, from the block's lower edge — only the rail is ported), wrapping rows.
- Bead = `TeamSeatIcon` (agent icon + light dot from `useAgentStore` by `(hostId, code)`, unread overlay) + host icon when `teamBeadHost`; tooltip = title; click → `openTeamSeat`; drag within the block → `setMemberOrder`; drop outside snaps back.
- Collapse (P9): collapsed line = members icon + one light per member; click the line → expand; click the tick or blank bead area → collapse (shared `collapsed`).
- Ghost lead row (spec §4.6): `ghostLeads(workspaceId)` rendered faded at the end of that workspace's list; click → `openTeamSeat(lead)`.
- Setting row: 「member 顆粒顯示主機圖示」 (zh-TW / en keys).

Tests: `member tabs are not listed as rows`; `beads in team order, wrap to rows`; `bead light follows the agent store`; `a bead with a tab and one without render identically (no fade, no open mark — P8)`; `bead click opens / switches (R3)`; `bead drag reorders memberOrder and the top group follows (R5)`; `drop outside the block snaps back`; `collapse line shows one light per member and expands on click`; `tick / blank click collapses`; `ghost row after the lead tab is closed shows its beads; lead click reopens the group; bead click opens that member`; `host icon follows the setting`; `top-only tab position renders no beads`.
Mutation gates: bead order from join time only → "team order" red; ghost row in every workspace → ghost test red.
Screenshot gate: dark, one and two bead rows, collapsed, ghost row.

---

## TI-4 — floating team panel

*Amended 2026-10-09:* built as WA-2a of `docs/plans/2026-10-09-session-workbook-plan.md` (the panel area shared with the workbook, TI spec §4.4 amended). The bullets and tests below remain the team-view part; "same width" means the area's current width, and a full row's click follows the amended §4.4.

Files: new ported `spa/src/components/team/{TeamPanel,ModelIcon,model-family}.tsx|ts`, the shell mount (where the pane area is laid out — next to the tab bar region in `App.tsx`), tests.

- Mounted once; shows `panelTeam(activeTabId)`'s team (spec §4.4) or nothing; anchored to the top of the content area, never over the tab bar; does not steal focus from the terminal.
- Full: header (accent, `panelName`, count, switch to one-line); rows per spec §4.4; values from `Seat.session.{title, model, effort, context}`, lights / subagents from the agent store; missing → "—".
- One-line: cells per person (bot + light, context ring around the model shape: ◆ Opus ● Sonnet ▲ Haiku ★ Fable by `model-family`), then the name; wraps; same width.
- Mode per team from `useTeamUiStore.panelMode` (absent = full); row / cell click → `openTeamSeat`; member drag → `setMemberOrder`.

Tests: `panel shows for a lead tab and a member tab, not for others`; `active row = the active tab's seat`; `lead row is first and not draggable`; `full rows carry host chip / title / model / effort / context / light / subagents`; `header shows panelName and its tooltip "<name> (<label>)"`; `missing model or context shows —`; `one-line cells + name, wraps`; `mode remembered per team and survives a reload`; `new team starts full`; `row click opens an unopened member`; `drag reorders and the sidebar / top bar follow`; `switch tabs away and back keeps the mode` (shell remount).
Mutation gates: mode in component state → "survives a reload" red; panel for any tab → "not for others" red.
Screenshot gate: dark, full and one-line, small and large team.

---

## TI-6 — round 2 (user 2026-10-10): no pills, no top-bar collapse, shadow trial

Lands right after WA-2a, before TI-5a. Spec: TI spec items marked *round 2* (R7, R8, P6, P11, §4.1–§4.3, §4.8, §5).

- **Top bar**: the group no longer renders `TeamGroupLabel`; the group starts with the lead's tab. No collapse control on the top bar; the bar still reads the shared `collapsed` state (set from the sidebar) — collapsed hides member tabs, stepping skips them, R9 / R10 unchanged. Separators: the one before the group's first tab follows today's rule.
- **Left list**: `TeamSidebarBlock` no longer renders the label capsule (live and ghost rows). Collapse stays on the collapse line, the tick and the blank bead area (P9).
- **Panel**: unchanged (its header keeps name / label).
- **Shadow trial**: `useTeamUiStore.groupShadow: 'v0' | 'v1' | 'v2' | 'v3'` (device-local, default `'v2'`); the group tabs take the variant's `box-shadow` from TI spec §4.2 (one function mapping variant + team colour + theme → the shadow string, shared by the tab and its tests). Setting row 設定 → 介面 → 分頁 「（試用）群組陰影」 with four options named 現行／加柔光／往左下延伸／整圈浮起 (zh-TW / en keys). Removed by a follow-up once the user picks.
Tests: no label on the top bar, in the left list or on a ghost row; the top bar has no collapse control; collapsing from the sidebar hides the members on the top bar and stepping skips them; expanding from the sidebar shows them; the shadow function returns each variant's string (dark and light); the setting switches the rendered shadow; default `v2`; locale completeness.
Mutation gates: render the label again → "no label" red; shadow function ignores the variant → variant test red.
Screenshot gate (zh-TW): the top bar with one group under each variant V0–V3 (dark), V2 in light; the left list expanded, collapsed and ghost without the capsule.

## TR-1 — daemon: edit a team's name, label and colour (user 2026-10-10)

Team module (A-line files; owner and wire agreed with purdex-1f before the PR). TI spec §4.12.
- Migration: `ensureColumn(teams, team_color, INTEGER NULL)` (NULL = automatic).
- `PUT /api/team/appearance` (admin; the team module's flat App-settings style, like max-members / relay-quota — purdex-1f): body **all of** `{team_id, team_name, team_label, team_color, client}` (the App sends the roster's current values with its changes, so Go never tells "absent" from `null`); `team_name` / `team_label` through `team.NormaliseTeamName` / `NormaliseTeamLabel`, `team_label: ""` = derived from the name by the creation rule (as `pdx lead` does), not cleared; `team_color` = `0–7` or `null` (automatic); `client.kind` recorded, not checked; only a live team (`ended_at = 0`) → else 409 `not_live`; unknown → 404; bad field → 400 naming it; 200 → `{team_id, team_name, team_label, team_color}`; the UPDATE sits next to the store's existing transaction helpers (no new connection), then `rosterChanged()`; one log line (team id, old → new, client.kind). `Roster` / `TeamRoster` gain `team_color` (omitted when NULL). Capability `team.edit.v1`. Built by the interface line's second seat, reviewed by purdex-1f; it shares `module.go` routes and `migrate.go` with A-line X3b-1b (the later one rebases).
- Cross-host (purdex-1f, later, issue): the M side keeps the join-time name / colour until a `team.appearance` command follows a rename (after X3b-1b).
Tests: name / label / colour changes; label `""` derives from the name; normalisation errors per field; colour out of range / non-integer → 400; `null` = automatic; ended → 409; unknown → 404; the roster frame after the PUT carries the new values; migration on an existing `team.db` copy.

## TI-7 — panel header: click toggles mode, double-click edits (user 2026-10-10)

After TR-1 is deployed (capability-gated otherwise). TI spec §4.4 header click, §4.12.
- `TeamRoster.team_color` parsed (0–7 or absent); `TeamView.colorIndex` = it when set, else today's hash. Save = `PUT /api/team/appearance` with all of name / label / colour (the roster's values plus the edits).
- Header: single click toggles full ⇄ one-line (not on the expand / switch controls or the resize edge); on the name, the toggle waits the double-click interval and is cancelled by a double-click.
- Double-click the name (when the lead's host has `team.edit.v1`) → popover: name, label (live display-width hint, `lib/textwidth.ts`), eight swatches + 自動; save sends all three values to the lead's host; close on 200, inline error on 400, toast + close on 409; the panel always renders the roster's values.
Tests: header click toggles, controls do not; a double-click on the name opens the popover and does not toggle; save sends all three values (roster values + edits); 400 shows the message on its field; 409 closes with a toast; no capability → no popover; a roster `team_color` recolours the panel and the group shadow; absent → hash colour.
Screenshot gate (zh-TW, dark): popover open (name, label with width hint, swatches), a recoloured team on the top bar and the panel.

## TI-5a — daemon: the mod's team read

Files: the mod socket handler (`internal/modevents`, U1-1a), a narrow interface the team module implements (`TeamRoleOf(sessionID) (role string, activeMembers int, ok bool)`), wiring where the modules are assembled, tests. **Agreed with purdex-1f first** (its module answers).

- **purdex-1f's conditions (agreed 2026-10-09):** the team module answers with indexed `team.db` queries only (the `LiveTeamByLead` / `ActiveMemberInLiveTeam` kind) — no fork, no table scan; the answer is for the **current** session id only (a relay changes the lead's session id and `teams.lead_session_id` with it); the response also carries `team_label` (from TL-1b on) so the footer can show the team later without another endpoint; the `TeamRoleOf` signature goes to 1f before the PR.
- `GET /mod/v1/team?session_id=<sid>` on the mod socket only (same peer-uid / socket rules as `POST /mod/v1/events`): `200 {"role":"lead"|"member"|"none","members":N,"team_label":"…"}`; `members` counts members with `state == "active"` (released / killed / gone excluded); `role:"none", members:0` for an unknown or team-less session; missing `session_id` → 400; any other method → 405. No token (the socket's own checks apply, U1 spec §6.1).
- U1 spec §6.1 gains this one read in the same PR.

Tests: `lead with 3 active + 1 released → members 3`; `member → role member`; `unknown session → none / 0`; `no session_id → 400`; `POST → 405`; `TCP listener does not serve it`.
Mutation gates: count all states → the released case red; serve on TCP → last test red.

## TI-5b — mod: lead mode in the terminal

Files: `cmd/pdx/plugin/purdex/hooks/events.js` (the interface line's file; `register.js` untouched), its tests, the Go guard test assertions.

- In `events.js` (top-level functions only — M-U1-2 / M-U1-5): `on('ui.render', {component: 'SessionMode'}, onSessionModeRender)` → `next({...e, modes: [...e.modes, label]})` while the last good read says lead, else `next(e)` unchanged. Label `lead mode · N members` / `lead mode · 1 member` / `lead mode · 0 members`.
- Read `GET /mod/v1/team?session_id=<sid>` with the same socket client `events.js` uses, on `session.start` and every 15 s (`$.clock.every`, started in the `session.start` hook, cleared on `session.end`) — never from the render hook, which only reads the cached answer (1f's condition); always with the **current** session id, and the cached answer is dropped on `session.switch` (`/clear`, relay) and re-read; a failed read keeps the last good value; no label before the first good read. Drawing re-reads the value; a change requests a redraw the way the SDK allows for `ui.render` hooks (an `atom` / `update` if the footer only redraws on state change — check the 2.1.294 types at implementation time).
- Guard (Go `embed_test.go` or the existing guard): assert `ui.render` is registered only with matchers across the mod's files (ToolUse, SessionMode), never unmatched; `$` is not passed to an imported function.
- Live check (the coordinator deploys with `pdx setup --agent cc`): a lead session shows the label; a member and a plain session do not; N follows a spawn / release within 15 s. Screenshot to the lead — the user may prefer a separate row (`PromptHint.tail` / `AbovePrompt`, spec §4.10).

Tests (`claude plugin test`): `not lead → modes unchanged`; `lead with 3 → "lead mode · 3 members" appended`; `1 → singular`; `0 → "0 members"`; `failed read keeps the last value`; `no label before the first good read`; `session.end clears the timer`; `session.switch drops the cache and re-reads with the new id`; `the render hook never calls fetch`; plus `claude plugin validate --strict`, `go test ./cmd/pdx/plugin/`.
Mutation gates: drop the `SessionMode` matcher → the guard red (and the module fails to load, M-U1-3); move the hook into a new imported file taking `$` → validate red (M-U1-5); count from the label instead of the read → the "0 members" test red.

---

## Acceptance (after TI-4; TI-5a/5b after their deploy)

Real App (Mac App on the user's machine via the dev server, or `playwright cli` against the worktree dev server with a real daemon roster): a lead with three members (one without a tab): top group label / shadow / wash / no separators; collapse and R9; ⌘-step skips hidden members; bead click opens the unopened member after the group; session list opens a member into the group; pin disabled; close the lead → group closes, ghost row, reopen; release a member with a tab → tab stays, leaves the group; panel full / one-line, survives reload; setting toggles host icons. Screenshots to the lead; tmux rules as in the U1-3 acceptance (named `acc-ti-<n>` sessions on the real server, `kill-session -t` by name only).

## Plan review fold-in (codex `task-mv02o348-5bq0sj`, 2026-10-09)

| # | Sev / conf | Finding | Disposition |
|---|---|---|---|
| 1 | critical 0.99 | the mod socket only takes `POST /mod/v1/events`; TCP `/api/team` needs a token and `origin_inbox` | Accepted: new read `GET /mod/v1/team` on the mod socket (TI-5a); spec §3 / §4.10 |
| 2 | critical 0.99 | closing the lead only in `handleCloseTab` misses ⌘W, TerminatedPane, WorkerEndedPane, workspace removal | Accepted: TI-1c subscriber on the tab store; a named test per path |
| 3 | important 0.98 | `closeTabInWorkspace` skips locked tabs | Accepted as behaviour: the lock wins, the tab rejoins when the lead is back (spec §4.5); named test |
| 4 | important 0.98 | `useTabStore` / workspace store missing from TI-1b files | Fixed |
| 5 | important 0.97 | `forgetHost` also runs on repoint / unmount | Accepted: pruning only on a roster frame (ended teams) and on host deletion; tests for the three non-prune cases |
| 6 | important 0.99 | `useTeamViews` re-runs on any host's frame; "other host does not re-render" false | Accepted: structural context with a signature + live readings selected per seat; the test now asserts "context-only frame does not re-render" |
| 7 | important 0.98 | per-array identity does not help the record dependency | Accepted: the record changes only via `setMemberOrder`; test adjusted |
| 8 | important 0.94 | `teamOfTab` per tab per render | Accepted: `buildTeamIndex` once per input (TI-1a) |
| 9 | important 0.97 | R6 "right after the group" had no task | Accepted: order normalisation in TI-1c makes it hold; named test |
| 10 | important 0.98 | TI-5 role / hello wiring undefined, `s` private to `register.js` | Accepted: role comes from the new read; the hook lives in `events.js`; `register.js` untouched |
| 11 | important 0.98 | live member count not filtered | Accepted: `state == "active"` only, `0 members` shown; tests |
| 12 | important 0.96 | no gates for M-U1-3 / M-U1-5 regressions | Accepted: guard assertions + mutation gates (TI-5b) |
| 13 | important 0.93 | ghost beads, P8, panel lead fixed, host chip / effort, tooltips untested | Accepted: named tests added (TI-2 / TI-3 / TI-4) |
| 14 | minor 1.00 | "Seven PRs" vs six | Fixed (now eight) |
