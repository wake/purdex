# Workbook journal v5 + the team panel back + watcher-area polish (spec + plan)

User, 2026-10-17, after using J-2b…J-2d (#2980, #3054, #3059, #3064, #3066) and the host cells (#3065). B line (88).
Amends `docs/specs/2026-10-14-workbook-journal-spec.md` (v4), `docs/plans/2026-10-14-workbook-journal-mac-plan.md`,
`docs/specs/2026-10-09-team-interface-spec.md`, `docs/specs/2026-10-14-fleet-status-spec.md` and
`docs/specs/2026-10-16-watcher-area-spec.md` / `docs/plans/2026-10-16-watcher-area-plan.md`; §2 lists every sentence.

## 1. User decisions (2026-10-17, do not reopen)

Team panel
- **U1** The team panel goes back to the previous interface (before J-2b), on every host: three states — **full**,
  **one line**, **collapsed to the title bar** (the title-bar strip shows the whole team name plus the lead's and every
  member's status). **No max state.** It is shown by default and is not closed by an outside click; one line and the
  title bar are its minimum states.
- **U2** The toolbar keeps the two buttons it has now: 「團隊」 and 「工作簿」.
- **U3** Each member row shows two lines taken from the **leader's journal** (members run no journal): the member's
  current task and its state; its last exchange with the leader (time + one line).

Journal cards (日誌 / 路線圖 / 任務)
- **U4** They float by default: a click outside closes them. Each has a **固定** (pin-to-screen) control; a fixed card
  stays open and is not closed by an outside click.
- **U5** The stream shows the leader's **current session** only (since its last relay or /clear); each earlier session
  is the previous page; **上一頁 / 下一頁** buttons at the bottom.
- **U6** 待確認 is shown across sessions (every unanswered one, whatever page is shown).
- **U7** **Entry pinning is removed entirely** (the pinned band, pin / unpin, auto-pin); the filters are enough.
- **U8** A 待確認 item can be **checked and unchecked by hand**. The check is stored on the daemon (every device sees
  it). A hand-set item is never changed by Haiku afterwards. When every item of an entry is checked it leaves 待確認 and
  the count drops.

Host cells and the watcher area
- **U9** The queue count badge on a host cell is too big: smaller, moved further to the upper right; the ring after it
  moves a little to the right; the gap **between hosts** narrows.
- **U10** In the watcher area the guardian tab is labelled **「守門員」** (not the session's title) and loses the
  group-style (team) look.
- **U11** A **back button at the far left of the watcher area's tab row**. It and **Cmd+W** (anywhere in the area)
  close the area and return to the workspace **and the tab** the user was on before entering.
- **U12** (open, user) how a guardian member is shown is not decided; nothing in this spec.
- **U13** In the queue section (當下), a **running** command (the slot holder: e.g. 「npx vitest rel… 24:02」 in 重指令,
  「make lint 20:11」 in 輕指令) must stand out from the waiting cards: a tint, a bright outline and / or a small
  animation.

## 2. Original text → new text

| Where | Original | New |
|---|---|---|
| journal plan :22 | "A card stack replaces the single panel area on `journal.v1` hosts only." | The team panel is the previous panel area (U1) on every host; the card stack holds the journal / roadmap / tasks cards only. |
| journal plan :23 | "The team card on a `journal.v1` host is open or closed, nothing else … the `titlebar` / `line` / `max` modes and the `TeamTitleStrip` do not exist there." | **Withdrawn** (user: no recollection of agreeing). The team panel has full / line / titlebar (no max) on every host; `TeamTitleStrip` is back. |
| journal spec :18 (item 4) | "both are floating cards; the team card is on the left" | The team panel is the docked panel of U1; the journal cards float (U4). |
| team-interface spec :118 | "**Four states, no hidden one**: title bar … line … full … max" | **Three states**: title bar, line, full. Max is removed. |
| team-interface spec :53 (P3) | "The panel cannot be closed (one line is the minimum)." (amended by J-2b: closable card) | Back to: it cannot be closed; one line and the title bar are the minimum states. |
| team-interface spec :48 (R19), :110 | "「正在做的任務」: the first sentence of the conversation's latest workbook `status`" | Two lines from the leader's journal (U3): current task + state; last exchange with the leader. |
| journal spec :180 | "§10.1 a team row's workbook button … removed: members have no journal" | Unchanged (members still have no journal); U3 reads the leader's journal. |
| journal spec :167, plan :28, :57 | Roadmap / tasks open from the journal header; open states remembered | Unchanged, plus: each card floats unless **固定** (U4); an outside click closes the unfixed ones; 固定 is remembered per unit. |
| journal spec :17 (item 3), :169; plan :49, :62, :65 | "scrolling up loads older months"; month pages (`before`) | One page = one leader session (U5); 上一頁 / 下一頁 at the bottom; on the current page new entries still append and it opens scrolled to the bottom. |
| journal spec :28 (item 11), :87, :89-92 (§5.3), :126, :149, :168, :190; plan :29, :47-49, :63, :65 | Pin: any entry can be pinned; 待確認 auto-pinned; `PUT /api/journal/pin`; the pinned band | **Removed** (U7). The band at the top shows the open 待確認 entries (any unchecked item), across sessions (U6). |
| journal spec :28 (item 11), :86, :168; plan :64 | "The checkboxes are ticked by Haiku"; "read-only — Haiku ticks them" | Haiku ticks; the person can also check and uncheck by hand (U8); a hand-set item is never changed by Haiku. |
| fleet spec :243-245, :263 | queue badge "12 px, the count in 8 px … on the host badge's top-right corner" | Smaller and further up-right (U9); exact size tuned on screenshots (§3.6). |
| fleet spec :233, :274, :237 | cells `gap-0.5`; in-cell `gap-1` | Narrower gap between cells; more room between the badge and the ring (U9). |
| watcher spec :12 (U1), :66 | the guardian tab is "an ordinary session tab" labelled 守門員 | Its label is always 「守門員」, also while it shows the watcher's session; no team group look (U10). |
| watcher spec :56-61; plan :67 | leaving = selecting another workspace or tab; "The return workspace is the last active workspace other than `watch`, kept in memory." | Plus a back button at the far left of the area's tab row, and Cmd+W, both returning to the return workspace **and its tab** (U11); the return tab id is kept with the workspace. |

## 3. Behaviour

### 3.1 Team panel (U1-U3)
- `PanelAreas` mounts the previous `TeamPanelArea` (titlebar / line / full) for the team on every host; `TeamPanel`'s
  `card` mode and the empty `onSetMode` go; `ToTitleBarButton` and the header click (full ⇄ line) work as before; the
  max state and `ExpandButton` are removed everywhere. The title-bar strip (`TeamTitleBar` / `TeamTitleStrip`) shows the
  team name plus each seat's light / model icon / context, as before J-2b.
- State memory per team on this device, as team-interface R17 (titlebar / line / full).
- 「團隊」 toolbar button: see §5 Q1. 「工作簿」 opens the journal card as today.
- Member rows: line 2 = current task (number, title, state 進行中 / 待辦); line 3 = last exchange (relative time + one
  line, e.g. 「3 分前 回報：#3088 檢查跑完」). A missing part is left out; with neither the row is one line. Source:
  `GET /api/journal/members` (§4.3); without `journal.members.v1` both lines are left out.

### 3.2 Journal cards float (U4)
- The journal, roadmap and tasks cards are floating by default: a pointer-down outside every open card (and outside
  the 工作簿 button and the 待確認 detail) closes the unfixed ones. Each card header has a 固定 toggle (a pin icon,
  pressed = fixed); a fixed card is not closed by an outside click and stays until its X or the 工作簿 button.
- 固定 is remembered per unit with the other view state (`view-memory.ts`), default off.

### 3.3 Session pages (U5)
- The stream shows one leader session; on open, the current one, scrolled to the bottom, live as today. 上一頁 shows the
  session before (relay or /clear), 下一頁 the next; at the current page 下一頁 is disabled, at the first 上一頁 is.
  A page header line names the session span (start time; 「目前」 on the current page).
- Filters apply within the page. The scroll memory is per page.
- Without `journal.sessions.v1` the stream keeps today's paging.

### 3.4 待確認 band and hand checks (U6-U8)
- The band at the top lists the open 待確認 entries (any unchecked item) of **all** sessions, newest first; its count
  is the number of open entries' unchecked items (`open_confirms`, as today). It no longer depends on pinning.
- In the band row and in the 待確認 detail each item has a checkbox the person can toggle (with
  `journal.confirm.v1`; read-only without it). Optimistic, rolled back on a failed write; `journal.changed` refreshes.
- All items checked → the entry leaves the band and the 待確認 filter count drops.

### 3.5 Pin removal (U7)
- SPA: the pinned band's pin source, the row pin action and every 「釘選」 label go; `pinned` on the wire is ignored.
- Daemon: `PUT /api/journal/pin` and auto-pin are removed; old files' `pinned` meta still parses and is ignored.

### 3.6 Host cells (U9)
- `CountBadge` on the host cell: smaller than today's 11-12 px (start at 9 px, digits 7 px) and offset further out of
  the block's corner (start at -5 px / -5 px); the indicator (ring / bars) gets 2 px more left margin; the gap between
  cells shrinks (start: from 2 px to 0-1 px). The guardian shield's badge keeps its size. Final values are set on
  screenshots of the real sidebar (light and dark) against the user's description; the PR shows before / after.

### 3.7 Watcher area (U10-U11)
- The guardian tab's label is always 「守門員」 (`useTabDisplay` / `pane-labels`), whatever the session's title; the
  watcher area's tabs never get the team group look (`TeamTabShadow`, `groupSegments`).
- A back button (← `ArrowLeft`, tooltip 「離開守門員專區」) is the first item of the area's tab row.
- Cmd+W while the area is shown closes the area (not a tab; the area's tabs stay locked) and returns to the return
  workspace and its tab. The return target is recorded when the area is entered: the workspace and its active tab id;
  if that tab is gone, the workspace's active tab. Back button = Cmd+W.

### 3.8 Queue lanes: the running command (U13)
- A holder card (running) gets the running colour three ways: a light tint of it as background (about 12 %), a 1 px
  outline in it, and a small dot at its left that breathes like the sidebar's running light (the same animation and
  period). Under `prefers-reduced-motion` the dot is still. Waiting cards, the 空名額 slot and the paused look are
  unchanged. Theme tokens only; readable in light and dark (contrast of the text on the tint ≥ 4.5:1).

## 4. Daemon contract (journal module; B line, reviewed by 1f where it reads the team module)

### 4.1 Hand checks — `journal.confirm.v1`
- `PUT /api/journal/confirm {session_id, entry_id, idx, done}` → 204; 404 unknown entry / item; 400 not a confirm.
  Writes `items[idx] = {text, done, manual: true}` (`journal.Tick` and a new untick), then `journal.changed {what:
  entries}`. Haiku's open-item list (`ReadOpenItems`) and its tick (`tick`) skip `manual` items, so a hand check or a
  hand uncheck is never overridden.
- Every journal page response gains `confirms[]`: the open 待確認 entries (any `!done` item) of the whole unit, newest
  first (replaces `pinned[]`).

### 4.2 Session pages — `journal.sessions.v1`
- New entries carry the leader session id (`sid` in the meta JSON). Old entries are attributed at read time: `turn:` /
  `ev:` through `wb_entries.session_id`; others by `at` within a session's span.
- `GET /api/journal/sessions?session_id=<reader>` → `{sessions: [{session_id, started_at, ended_at, via: "root" |
  "relay" | "clear", current}]}`, oldest first: the leader's chain over relay (`session_lineage`) and /clear
  (`conversation/predecessor.go`).
- `GET /api/journal?session_id=<reader>&page=<leader session id>` → that session's entries (oldest first, capped at
  1000 with `truncated`), plus `confirms[]` and `open_confirms`. Without `page`: the current session.

### 4.3 Member status — `journal.members.v1`
- `GET /api/journal/members?session_id=<reader>` → `{members: [{ref, session_id, title, task: {number, title, state} |
  null, last: {at, kind, text} | null}]}` for the team's members. `task`: the member's newest open (doing / todo) task
  from the team's tasks (`owner_key` joined to `team_members`). `last`: the newest exchange between that member and the
  leader (the team's `reports` by `member_key`, and journal request / comm entries the daemon can tie to the member).
  Reads the team module → reviewed by 1f.

### 4.4 Pin removal
- Remove `PUT /api/journal/pin`, `SetPinned`, `pin_in.go`, auto-pin in `journal_drain.go`; keep parsing old `pinned`
  meta. `info_handler.go` capability text updated.

## 5. Open questions for the user
- **Q1** The 「團隊」 button, now that the panel cannot be closed: proposal — it switches between **full** and
  **collapsed to the title bar** (a one-click minimise / restore); the header click still switches full ⇄ one line.

## 6. Plan (one task = one PR; TDD; full flow: this doc → codex once; PR → codex R1 + R2)

| Task | Where | What | Depends |
|---|---|---|---|
| JV-1a | daemon journal | §4.1 hand checks + `confirms[]`; §4.4 pin removal | — |
| JV-1b | daemon journal | §4.2 `sid` stamping, sessions API, page by session | — |
| JV-1c | daemon journal + team read | §4.3 members API (1f reviews) | — |
| JV-2 | SPA team | §3.1 panel back (titlebar / line / full, strip), member lines (members API) | JV-1c for the lines (the rest independent) |
| JV-3 | SPA journal | §3.2 float + 固定; §3.4 band from `confirms[]` + checkboxes; §3.5 pin UI removal | JV-1a for checks (gated) |
| JV-4 | SPA journal | §3.3 session pages | JV-1b (gated) |
| WA-1 | SPA fleet | §3.6 host cell spacing | — |
| WA-2 | SPA watch | §3.7 guardian label, no group look, back button, Cmd+W | — |
| WA-3 | SPA watch | §3.8 running holder look in the queue lanes | — |

Tests (each with a mutation that turns it red):
- JV-1a: check / uncheck an item → file and `journal.changed`; a manual item is not in Haiku's open list and `tick`
  skips it (M: drop the `manual` skip); all checked → not in `confirms[]`, `open_confirms` drops; old file with
  `pinned=1` parses; pin route gone.
- JV-1b: new entries carry `sid`; sessions list over relay and /clear in order; old entries attributed (turn / ev by
  `wb_entries`, task by span); `page` returns only that session (M: ignore `page`).
- JV-1c: task by owner, last exchange newest first, a member with neither → nulls; a released member not listed.
- JV-2: three states and the strip content; no max; the header click; state memory; member lines present / absent by
  capability; tab-switch (the panel is not tab-hosted — n/a).
- JV-3: outside click closes unfixed cards only, 固定 survives it and is remembered (M: close fixed too); band from
  `confirms[]` across sessions; checkbox optimistic + rollback; no pin UI left (a guard test greps for the pin API).
- JV-4: open on the current page at the bottom; 上一頁 / 下一頁 bounds; filters within the page; fallback without the
  capability.
- WA-1: badge size / offset classes; screenshots before / after (light, dark).
- WA-2: label 「守門員」 while the session runs; no group look in the area; back button and Cmd+W return to workspace +
  tab (M: return to the workspace only); Cmd+W outside the area unchanged.

- WA-3: a holder card has the running tint / outline / dot, a waiting card does not (M: style every card); reduced
  motion stops the dot; screenshots light / dark.

Test tiers as CLAUDE.md (2026-10-15): changed tests while developing; PR tier on the tree with the latest main —
Go: the changed packages + `make lint`; SPA: `vitest related` + the 5 guard tests + `tsc` (app through the heavy slot)
+ eslint. Heavy runs go to air26 when mlab's queue is long (Assist runs tests only): Go through
`~/.local/share/purdex-lead/deploy/assist-verify.sh <sha> "<go test args>" --lint`, SPA through
`~/.local/share/purdex-lead/deploy/assist-run.sh <sha> --dir spa --pnpm -- "npx vitest related --run <files>" "npx tsc
--noEmit -p tsconfig.app.json" "npx tsc --noEmit -p tsconfig.node.json"` (commit first; no single quotes in commands).
