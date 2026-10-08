# Team interface — plan

Spec: `docs/specs/2026-10-09-team-interface-spec.md` (rulings R1–R19, P1–P12; behaviour §4; values §5). Data shipped: `spa/src/lib/team/*` (#1996 / #1997), team name (TN-2). Depends on the team-label line's **TL-2** (`team_label` parse, `spa/src/lib/textwidth.ts`) and on **U1-3** (lights over all panes) for the files both touch.

Format as the U1-3 plan: contracts, rules, named tests and mutation gates; implementers write the code (TDD: each named test is written red first). Line numbers are as of `ff90f472`; re-check before editing.

**Porting rule.** The prototype on `worktree-team-ui` at `f61748aa` already draws every piece with real components (`spa/src/components/team/*`, edits in `TabBar.tsx`, `SortableTab.tsx`, `InlineTabList.tsx`). Port those components, **keeping only the final variant** (spec §5) and deleting every comparison option (`TeamGroupStyle` beyond the shadow, corner / badge / bookmark / edge / companion code, `sidebarStyle`, `hookStyle` other than the rail, `openMark`, `shadowStrength` / `shadowDepth` / `shadowScope` / `labelTint` as options — they become constants). The prototype entry (`spa/proto-team.html`, `spa/src/proto/team/*`) is **not** merged; the branch is kept for later design rounds.

Seven PRs, each ≤ 800 lines diff / ≤ 20 files:

| PR | Content | Depends on | Est. lines |
|---|---|---|---|
| **TI-1a** | foundation: `TeamView.name` / `.label`, label / name rules, `useTeamUiStore` (device-local order / collapse / panel mode / ghost workspace), bead setting, the real `TeamDisplayProvider` (no surface uses it yet) | TL-2 | ~550 |
| **TI-1b** | actions: open a seat (R3, R10, R11, §4.5), close the lead (P2), collapse (R8, R9), tab stepping and ⌘1–8 skip hidden members, pin disabled (R12), session-list placement | a | ~600 |
| **TI-2** | top tab bar group: label capsule, shadow + wash, separators, collapse `+N`, drag within the group | 1b, U1-3 | ~600 |
| **TI-3** | left list: beads, tick, sidebar collapse, ghost lead row, bead drag, the setting row | 1b, U1-3 | ~650 |
| **TI-4** | floating team panel: full / one-line, model icons, context ring, row click / drag | 1b | ~550 |
| **TI-5** | mod: `lead mode · N members` in the `SessionMode` footer | — (agree with 1f) | ~300 |

TI-2, TI-3 and TI-4 are independent of each other once TI-1b is in. TI-5 is independent of the SPA. With one member the order is TI-1a → 1b → 2 → 3 → 4 → 5.

Common rules:
- SPA: `cd spa && npx vitest run <affected files>` during development; **the full vitest once before merge, `--maxWorkers=3`, after asking purdex-1f for the slot**; `pnpm run lint`; `npx tsc -p tsconfig.app.json --noEmit`; `pnpm run build`.
- Tab-hosted checklist (CLAUDE.md): none of these is a tab-hosted component; the panel is mounted in the shell above the pane area, so its state is in stores, not `useState` (mode, order) — tested with a switch-away / switch-back.
- **Screenshot gate** for TI-2 / TI-3 / TI-4 / TI-5: real App screens (dev server on the worktree's own port, `playwright cli -s=<worktree>`) matching the prototype at `f61748aa` side by side, dark theme; the lead reviews them before merge.
- Each task one commit; parallel subagents in one worktree commit with `git commit --only <files>`.

---

## TI-1a — foundation

Files: `spa/src/lib/team/team-views.ts` (+ test), new `spa/src/lib/team/team-names.ts` (+ test), new `spa/src/stores/useTeamUiStore.ts` (+ test), `spa/src/stores/useUISettingsStore.ts`, new `spa/src/components/team/team-display.ts` (ported, final variant only), new `spa/src/components/team/TeamDisplayProvider.tsx` (+ test), `spa/src/App.tsx` (mount the provider; no surface reads it yet).

- `TeamView` gains `name: string` (`team_name`) and `label: string` (`team_label`, `""` when absent — parsed by TL-2).
- `team-names.ts`: `groupLabel(view): {text, full, truncated}` — `label` if not `""`; else the lead's `Seat.label` cut to ≤ 10 display width with `textWidth` (TL-2's `spa/src/lib/textwidth.ts`) — the cut keeps whole grapheme clusters and the "…" (width 1) counts inside the 10 (spec §4.8, P12); `panelName(view): {text, unnamed}` — `name`, else the lead's label uncut; `tooltipOf(view)` = `"<name> (<label>)"` when both exist.
- `useTeamUiStore` (zustand `persist`, key `purdex-team-ui`, device-local, not in profile sync): `memberOrder: Record<teamKey, string[]>`, `collapsed: Record<teamKey, boolean>`, `panelMode: Record<teamKey, 'full' | 'line'>` (absent = `full`, R17), `ghostWorkspace: Record<teamKey, string>`; actions `setMemberOrder`, `setCollapsed`, `setPanelMode`, `setGhostWorkspace`, `forgetTeams(hostId, liveTeamKeys)` — called from the roster apply path so keys of teams that ended on that host are pruned (a host that is merely offline keeps its keys; `roster-forget` on host removal prunes that host's keys). `memberOrder[k]` is the **same array reference** until it changes (it is the `useTeamViews` input).
- `useUISettingsStore.teamBeadHost: boolean` (default `true`, P7) + setter.
- `TeamDisplayProvider`: builds the `TeamDisplay` context from `useTeamViews(memberOrder)`, `teamOfTab`, the tab / workspace stores and `useTeamUiStore`: `tabMark(tabId)`, `sidebarHidden(tabId)`, `sidebarBeads(tabId)`, `ghostLeads(workspaceId)`, `panelTeam(activeTabId)`, and the action callbacks (wired in TI-1b; no-ops here). Memoised per input; a roster frame for another host does not change the context of this one (selector by host).

Tests: `team-names.test.ts` — label shown whole; empty label → lead title cut (ASCII 12 → 9 + "…"; CJK 6 chars → 4 + "…"; exactly 10 → whole; emoji / ZWJ cluster not split, using `testdata/textwidth/cases.json` cases); panel name fallback uncut; tooltip both. `useTeamUiStore.test.ts` — persist round trip; `forgetTeams` prunes ended teams only; order reference stable when unrelated keys change. `team-views.test.ts` — `name` / `label` mapped, `""` when the roster lacks them. `TeamDisplayProvider.test.tsx` — `tabMark` for lead / member / non-team tabs; `sidebarHidden` true for member tabs only; `panelTeam` for a member tab; a frame for another host does not re-render a consumer (render counter).
Mutation gates: cut by UTF-16 length → the CJK / emoji cases red; `"…"` outside the 10 → "exactly 10" red; prune on missing host → "offline host keeps keys" red.

---

## TI-1b — actions

Files: new `spa/src/lib/team/team-actions.ts` (+ test), `spa/src/lib/open-session-tab.ts` (+ test), `spa/src/features/workspace/hooks.ts` (`handleCloseTab` ~:119), `spa/src/hooks/useShortcuts.ts` (~:42–60 and `switch-tab-N`), `spa/src/components/TabContextMenu.tsx` (~:83), `TeamDisplayProvider.tsx` (wire callbacks), tests.

- `openTeamSeat(teamKey, sessionId)` (spec §4.5): seat has a tab → activate (`activateTab`); else open a tab for its tmux session (`createTab` tmux-session content as `openSessionTab` builds it) inserted with `insertTab(id, leadWs, lastGroupTabId)`; lead has no tab → open the lead first in `ghostWorkspace[teamKey]` (else the active workspace) and clear the ghost entry; collapsed → `setCollapsed(false)` first (R10). A seat whose tmux session is not in the host's session list (not yet listed) → no-op with a toast "session 尚未出現在清單" (never a tab to nowhere).
- `openSessionTab(hostId, session)`: if `teamOfSession(hostId, session.name)` finds a **member** seat → `openTeamSeat` (R11); a lead or a non-team session → today's behaviour. `teamOfSession` is a small selector over the views (same name match as `shownSessions`).
- Close (P2): `handleCloseTab(tabId)` — if `teamOfTab` says lead → close the group's member tabs with `closeTabInWorkspace(id, {skipHistory: true})`, then the lead tab, then `setGhostWorkspace(teamKey, wsId)`; member or non-team → today's path.
- `toggleTeamCollapse(teamKey)`: expand, or collapse and — when the active tab is that team's member — activate the lead (R9).
- Stepping (R8): `useShortcuts` `prev-tab` / `next-tab` and `switch-tab-N` / `switch-tab-last` work on the **visible** tab list: member tabs of a collapsed team are skipped (a pure `visibleTabIds(tabs, collapsedTeams, teamOfTab)` used by both and by TabBar later).
- Pin (R12): `TabContextMenu` disables pin for a tab in a team (title explains); `togglePin` refuses one too (store-level guard, so a shortcut cannot pin it either).
- Release / end (R6): nothing to do beyond the roster — a tab whose session left the roster is no longer in `teamOfTab`; tested.

Tests (`team-actions.test.ts`, `open-session-tab.test.ts`, `useShortcuts` tests, `TabContextMenu` test):
`opens an unopened member after the group's last tab in the lead's workspace`; `switches to a member's existing tab (never a second)`; `reopens a closed lead first, in the ghost workspace`; `expands a collapsed group before opening`; `session list opens a member into the group`; `session list opens a lead / non-team session as before`; `closing the lead closes the group's tabs, records the ghost workspace, adds no history for members`; `closing a member tab leaves the others`; `collapsing on a member activates the lead`; `next-tab skips hidden members`; `switch-tab-3 counts visible tabs only`; `pin disabled and refused for a team tab`; `a released member's tab is a normal tab again`; `unlisted session → toast, no tab`.
Mutation gates: insert after the lead instead of the group's last tab → first test red; drop the collapse expand → "expands a collapsed group" red; step over all tabs → "next-tab skips" red; pin guard only in the menu → "refused" red.

---

## TI-2 — top tab bar group

Files: `spa/src/components/TabBar.tsx`, `spa/src/components/SortableTab.tsx`, new `spa/src/components/team/TeamTabGroup.tsx` (ported: `TeamTabGroupFrame`, `TeamGroupLabel`, shadow + wash overlay), tests.

- Segments: the normal zone is split into runs; a team run = label + lead + visible members (spec §4.2), built by a pure `groupSegments(tabs, tabMark, collapsed)` (from the prototype's TabBar segment code).
- Label capsule: text / tooltip from `groupLabel`; `+N` when collapsed; click → `toggleTeamCollapse`.
- Each group tab: shadow `1px -1px 0 color-mix(in oklab, <team> 70%, transparent)` (light theme darkens first, spec §5) and wash 6 % / 8 %, drawn by a pointer-transparent overlay so the tab's own background / active highlight stay; separators inside the group and after its last tab hidden (prototype commit `f61748aa`).
- Drag: within the group (members only; the lead stays first) → `setMemberOrder`; out of / into a group → snap back (refused in `handleDragEnd`); pinned zone untouched.
- `SortableTab` gains `group?: TeamTabMark`; its existing indicators are unchanged.

Tests (`TabBar.team.test.tsx`, `groupSegments.test.ts`): `group renders label → lead → members in team order`; `collapsed shows label +N and the lead only`; `label click collapses / expands`; `every group tab has the shadow and wash; a non-group tab has neither`; `no separator inside the group or after it`; `member drag reorders memberOrder`; `drag across the group boundary snaps back`; `lead cannot be dragged behind a member`; `pinned tab never grouped`; `member tab of a team whose lead is in another workspace stays ungrouped`.
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

Tests: `member tabs are not listed as rows`; `beads in team order, wrap to rows`; `bead light follows the agent store`; `bead click opens / switches (R3)`; `bead drag reorders memberOrder and the top group follows (R5)`; `drop outside the block snaps back`; `collapse line shows one light per member and expands on click`; `tick / blank click collapses`; `ghost row after the lead tab is closed, click reopens the group`; `host icon follows the setting`; `top-only tab position renders no beads`.
Mutation gates: bead order from join time only → "team order" red; ghost row in every workspace → ghost test red.
Screenshot gate: dark, one and two bead rows, collapsed, ghost row.

---

## TI-4 — floating team panel

Files: new ported `spa/src/components/team/{TeamPanel,ModelIcon,model-family}.tsx|ts`, the shell mount (where the pane area is laid out — next to the tab bar region in `App.tsx`), tests.

- Mounted once; shows `panelTeam(activeTabId)`'s team (spec §4.4) or nothing; anchored to the top of the content area, never over the tab bar; does not steal focus from the terminal.
- Full: header (accent, `panelName`, count, switch to one-line); rows per spec §4.4; values from `Seat.session.{title, model, effort, context}`, lights / subagents from the agent store; missing → "—".
- One-line: cells per person (bot + light, context ring around the model shape: ◆ Opus ● Sonnet ▲ Haiku ★ Fable by `model-family`), then the name; wraps; same width.
- Mode per team from `useTeamUiStore.panelMode` (absent = full); row / cell click → `openTeamSeat`; member drag → `setMemberOrder`.

Tests: `panel shows for a lead tab and a member tab, not for others`; `active row = the active tab's seat`; `full rows carry title / model / context / light / subagents`; `missing model or context shows —`; `one-line cells + name, wraps`; `mode remembered per team and survives a reload`; `new team starts full`; `row click opens an unopened member`; `drag reorders and the sidebar / top bar follow`; `switch tabs away and back keeps the mode` (shell remount).
Mutation gates: mode in component state → "survives a reload" red; panel for any tab → "not for others" red.
Screenshot gate: dark, full and one-line, small and large team.

---

## TI-5 — lead mode in the terminal (mod)

Files: new `cmd/pdx/plugin/purdex/hooks/leadmode.js`, `register.js` (import + one call, **agreed with purdex-1f first**), `hooks` tests, the Go embed / guard test if it lists hook files.

- `registerLeadMode(on)`: `on('ui.render', {component: 'SessionMode'}, hook)` → `next({...e, modes: [...e.modes, label]})` while lead, else `next(e)` unchanged (spec §4.10). The label text `lead mode · N members` / `· 1 member`.
- State: role from the relay's hello (`s.role`; the hook reads it via the same module-level holder `register.js` exposes, or the mod's `$.state` — pick whichever M-U1-5 allows without passing `$` across files); N from `GET /api/team` over the mod socket (`$.http.fetch`, as `events.js` does), on `session.start`, on each hello and every 15 s while lead (`$.clock.every`), cancelled when the role leaves lead; a failed read keeps the last N.
- M-U1-2 / M-U1-3 / M-U1-5 checked by `claude plugin validate cmd/pdx/plugin/purdex --strict`, `claude plugin test cmd/pdx/plugin/purdex`, `go test ./cmd/pdx/plugin/`.
- Live check (coordinator deploys with `pdx setup --agent cc`): a lead session shows the label in the footer; a member and a plain session do not; N follows a spawn / release within 15 s. Screenshot to the lead — the user may prefer a separate row (`PromptHint.tail` / `AbovePrompt`, spec §4.10).

Tests (`leadmode.test.js` under `claude plugin test`): `not lead → modes unchanged`; `lead with 3 → appended "lead mode · 3 members"`; `1 → singular`; `failed fetch keeps the last N`; `role leaves lead → label gone, timer cancelled`.

---

## Acceptance (after TI-4; TI-5 after its own deploy)

Real App (Mac App on the user's machine via the dev server, or `playwright cli` against the worktree dev server with a real daemon roster): a lead with three members (one without a tab): top group label / shadow / wash / no separators; collapse and R9; ⌘-step skips hidden members; bead click opens the unopened member after the group; session list opens a member into the group; pin disabled; close the lead → group closes, ghost row, reopen; release a member with a tab → tab stays, leaves the group; panel full / one-line, survives reload; setting toggles host icons. Screenshots to the lead; tmux rules as in the U1-3 acceptance (named `acc-ti-<n>` sessions on the real server, `kill-session -t` by name only).
