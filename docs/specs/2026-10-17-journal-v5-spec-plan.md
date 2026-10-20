# Workbook journal v5 + the team panel back + team settings + watcher-area polish (spec + plan)

User, 2026-10-17, after using J-2b…J-2d (#2980, #3054, #3059, #3064, #3066) and the host cells (#3065). B line (88).
Amends `docs/specs/2026-10-14-workbook-journal-spec.md` (v4), `docs/plans/2026-10-14-workbook-journal-mac-plan.md`,
`docs/specs/2026-10-09-team-interface-spec.md`, `docs/specs/2026-10-14-fleet-status-spec.md`,
`docs/specs/2026-10-16-watcher-area-spec.md` / `docs/plans/2026-10-16-watcher-area-plan.md`,
`docs/specs/2026-10-13-relay-team-v2-spec.md` / its plan (App contract 8) and
`docs/specs/2026-10-09-relay-quota-spec-plan.md`; §2 lists every sentence. Rev 2 after the codex plan review
(task-mvbzmc9p-hkvpqc): §2 rows added, code locations corrected, the members feed contract, dependencies and tests.

## 1. User decisions (2026-10-17, do not reopen)

Team panel
- **U1** The team panel goes back to the previous interface (before J-2b), on every host: three states — **full**,
  **one line**, **collapsed to the title bar** (the title-bar strip shows the whole team name plus the lead's and every
  member's status). **No max state.** It is shown by default and is not closed by an outside click; one line and the
  title bar are its minimum states.
- **U2** The toolbar keeps the two buttons it has now: 「團隊」 and 「工作簿」. *(Amended 2026-10-20, TG-5: on a tab that is no team seat, 「團隊」 is 「個人」; 「工作簿」 is unchanged.)*
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

Unattended panel and team settings (relay-team v2 U-0 of 10-13 and its App contract item 8, never scheduled; re-asked
and amended by the user 10-17)
- **U14** The unattended panel and the team's settings are split. The team's settings open from a **gear on the team
  panel's title** (a small panel, per team, leads only): **member cap**, **folders** (team roots, incl. other hosts'),
  and the **relay quotas** (the lead's own auto-relay quota and its member pool). The unattended panel keeps: the paused
  (被擋下) sessions, every session's **relay quotas** (own auto-relay; on a lead also the member pool), the auto-approved
  list. **The relay quotas are shown and editable in both places** (one value: a change in either shows in the other);
  the cap and the folders are in the team settings only.
- **U15** In the unattended panel a team's row is named by the **team name**, not an id or address
  (「purdex-88-b8」-like names are not recognisable).

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
| watcher spec :56-61; plan :67 | leaving = selecting another workspace or tab; "The return workspace is the last active workspace other than `watch`, kept in memory." | Plus a back button at the far left of the area's tab row, and Cmd+W, both returning to the return workspace **and its tab** (U11); the return target (workspace + tab) is taken when the area is entered. |
| journal spec :145, :151, :198 | the daemon writes pins; a device token may pin; the pinned band is delivered | **Removed** (U7); the band is the 待確認 band of §3.4. |
| journal Mac plan :24, :56-58 | a journal host hides `TeamTitleStrip`; the team is open / closed; a narrow window hides the team card | **Withdrawn** (U1): every host has titlebar / line / full and the strip; the panel cannot be closed. |
| journal Mac plan :55; journal spec :159 | the title-bar button is named 「日誌」 | 「工作簿」 (U2). |
| team-interface spec :103-104, :119-120, :124 | max / expanded, the expand control, the button returns to the previous line / full / max | Max and the expand control are removed (U1); the 「團隊」 button follows §5 Q1. |
| fleet spec :262 | narrow bar: host cells 「2 px apart」 | Narrower (U9). |
| relay-team v2 spec :12 (U-0), :171; plan :555 (App contract 8) | cap and folders move from the unattended panel to the team panel; the member pool stays in the unattended panel | Done here (U14): cap + folders in the team settings only; **both** relay quotas (own, member pool) in the team settings **and** the unattended panel. |
| relay-quota spec :10 (R1), :15 (R5) | the quota is set **only** in the unattended panel; the team panel shows it | Set in the unattended panel **or** the team settings (U14); still App-only (no agent command). |

## 3. Behaviour

### 3.1 Team panel (U1-U3)
- `PanelAreas` mounts the previous `TeamPanelArea` (titlebar / line / full) for the team on every host — today a
  journal host does not mount it at all (`components/journal/CardStack.tsx:60-64`) and pins the team card to full with an
  empty `onSetMode` (`:108-117`); both go, and the card stack keeps only the journal cards. `ToTitleBarButton` and the
  header click (full ⇄ line) work as before; the max state and `ExpandButton` are removed everywhere. The title-bar strip
  (`TeamTitleStrip`, `components/team/TeamTitleBar.tsx`) shows the team name plus each seat's light / model icon /
  context, as before J-2b.
- State memory per team on this device, as team-interface R17 (titlebar / line / full).
- 「團隊」 toolbar button (§5 Q1): full or line → title bar; title bar → full. 「工作簿」 opens the journal card as
  today.
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
- The guardian tab's label is always 「守門員」, whatever the session's title. While the watcher lives the pane is an
  ordinary `tmux-session` (`features/workspace/lib/watch-area.ts:121-130`), so `pane-labels` returns the session name
  (`lib/pane-labels.ts:25-34`); the label is overridden in `useTabDisplay` (`hooks/useTabDisplay.ts:126-137`) by the
  guardian tab's id, not by the pane content.
- The watcher area's tabs never get the team group look: the area's `fixed` row skips `groupSegments` and the
  `TeamTabGroupFrame` wrapper (`components/TabBar.tsx:11-13, 28-29, 42-46, 243-251`; `fixed` is wired at
  `App.tsx:236-240`).
- A back button (← `ArrowLeft`, tooltip 「離開守門員專區」) is the first item of the area's tab row.
- Cmd+W while the area is shown closes the area (not a tab; the area's tabs stay locked) and returns to the return
  target. Today only the return workspace id is kept (`features/workspace/store.ts:27-38, 412-416`) and `openWatchArea`
  takes no snapshot (`lib/open-watch-area.ts:10-15, 29-31`): the store gains a return target `{workspaceId, tabId}`
  taken by `openWatchArea` when the area is entered; if that tab is gone on return, the workspace's active tab.
  Back button = Cmd+W.

### 3.8 Queue lanes: the running command (U13)
- Today a holder card already has a faint accent tint and outline (`components/watch/QueueLane.tsx:17-28`), which the
  user found too weak. It gets the **running** colour three ways: a tint of it as background (about 12 %), a 1 px
  outline in it, and a small dot at its left that breathes with the sidebar's running light (the same keyframes and
  period, reused, not copied). Under `prefers-reduced-motion` the dot is still. Waiting cards, the 空名額 slot and the
  paused look (`:32-45, 80-113`) are unchanged. Theme tokens only; readable in light and dark (contrast of the text on
  the tint ≥ 4.5:1).

### 3.9 Team settings and the unattended panel (U14-U15)
- **Team settings**: a gear (`GearSix`) at the right of the team panel's title, leads only (the strip in the title bar
  has no gear). It opens a `FloatingPanel` titled 「團隊設定・{team name}」 with three sections:
  - 「成員上限」: the cap stepper of today (`UnattendedQuotaSection.tsx` `CapStepper`, moved, same rules and texts);
  - 「資料夾」: `TeamRootsEditor` and the other hosts' roots (`HostRootsSection`), moved;
  - 「接力額度」: the lead's own 自動接力 and the member pool, the same steppers as the unattended panel, writing through
    the same `relay-quota-writer` (one value per chain root, so both places show the same number at once).
  - Reading: today the quota rows (`SessionQuota`: `session_id`, `root_session_id`, the values) reach the store only
    when the unattended panel reads `GET /api/team/unattended` (`UnattendedPanel.tsx:158-183`); the roster carries none
    (`lib/team/roster.ts:78-99`). Opening the team settings therefore reads that same route for the lead's host and
    fills the same store (one reader, shared by both panels, single-flight per host); while it loads the section shows
    「讀取中…」; on an error the section shows the error line and no steppers; a host without `team.relay_quota.v1`
    shows no 接力額度 section. The cap and folders read as they do today (the roster's `max_members` / `in_use`, the roots
    store).
- **Unattended panel**: the cap column and the folders leave it; the relay-quota section keeps both steppers (own; pool
  on a lead). The paused list and the auto-approved list are unchanged.
- **Team names (U15)**: wherever the unattended panel names a lead's row (the quota rows, the held rows, the paused
  rows), it shows the **team name** from the roster (`TeamRoster.team_name`, `lib/team/roster.ts:104`; `""` from a
  daemon that predates names → the lead's title), with the lead's title after it in the muted
  colour when it differs; a solo session keeps its title, else its address name. No id or ref is shown when a name
  exists.
- iOS: the same split is asked of the iOS member separately (its unattended panel shows the quotas read-only today).

## 4. Daemon contract (B line, reviewed by 1f where it reads the team module)

Where the code is: the journal routes and their orchestration live in the **workbook module**
(`internal/module/workbook/api.go:157-161`, `journal_api.go`); `internal/journal` is the file model only
(`entry.go:41-60`, `markdown.go:145-182`). There is no `internal/module/journal`.

Capabilities: `/api/info` declares only `journal.v1` today (`internal/core/info_handler.go:117-122`, whose comment still
lists the pin route). Each task below adds its own flag there, with the real-module test (the workbook module registered
into core → `/api/info` declares it), and updates that comment.

### 4.1 Hand checks — `journal.confirm.v1`
- `PUT /api/journal/confirm {session_id, entry_id, idx, done}` → 204; 404 unknown entry or item index out of range;
  400 the entry is not a confirm. `journal.Tick` today checks only the index and `done`, not the kind
  (`internal/journal/markdown.go:372-383`), and the handler maps not-found → 404 and everything else → 500
  (`journal_api.go:353-378`): `internal/journal` gains distinguishable errors (`ErrNotConfirm`, `ErrItemRange`) and an
  untick; the handler maps them. Writes `items[idx] = {text, done, manual: true}`, then `journal.changed {what:
  entries}`. Haiku's open-item list (`ReadOpenItems`) and its tick (`tick`) skip `manual` items, so a hand check or a
  hand uncheck is never overridden.
- Every journal page response gains `confirms[]`: the open 待確認 entries (any `!done` item) of the whole unit, newest
  first (replaces `pinned[]`). This changes the page shape of `journalPageJSON` / `handleJournal`
  (`journal_api.go:59-67, 243-280`), which §4.2 then reshapes again: JV-1b builds on JV-1a.

### 4.2 Session pages — `journal.sessions.v1`
- New entries carry the leader session id (`sid` in the meta JSON). Old entries are attributed at read time: `turn:` /
  `ev:` through `wb_entries.session_id`; others by `at` within a session's span.
- `GET /api/journal/sessions?session_id=<reader>` → `{sessions: [{session_id, started_at, ended_at, via: "root" |
  "relay" | "clear", current}]}`, oldest first: the leader's chain over relay and /clear. The two links come from two
  readers: relay from `team.PredecessorReader` (`internal/module/team/module.go:558-568`, `seat_reader.go:95-106`),
  /clear from `agent.PredecessorReader` (`internal/module/agent/predecessor.go:8-26`); `conversation/predecessor.go` is
  that module's private merge and is not reused. The workbook module gets both readers injected and merges them the
  same way (relay before clear at the same instant), with a test that pins the order.
- `GET /api/journal?session_id=<reader>&page=<leader session id>` → that session's entries (oldest first, capped at
  1000 with `truncated`), plus `confirms[]` and `open_confirms` of the whole unit. Without `page`: the current session.
  Without `journal.sessions.v1` on the host, the App keeps today's `before` + `limit` paging with `more`
  (`journal_api.go:59-67`); a host that declares it answers `page` and still answers `before` for older Apps.

### 4.3 Member status — `journal.members.v1`
- `GET /api/journal/members?session_id=<reader>` → `{members: [{ref, session_id, title, task: {number, title, state} |
  null, last: {at, kind, text} | null}]}` for the team's **current** members (released / killed members are not listed).
- The team read contract does not have this yet: `JournalFeed` (`internal/team/journalfeed.go:49-64`) has incremental
  tasks, reports and one `TeamInfo`, no member snapshot. It gains `Members(teamID) ([]MemberInfo, error)`:
  `{member_key, ref, session_id, title, state}` from `team_members` (`internal/module/team/team_store.go:47-67`), current
  members only, plus `CurrentTask(teamID, memberKey)` that exposes the module's own rule (`currentTaskOf`, in progress
  before pending, newest of each: `task_store.go:360-385`) so the journal and `/api/team` agree. Interface in
  `internal/team`, implementation in `internal/module/team` → reviewed by 1f.
- `task`: `CurrentTask`'s answer. `last`: the member's newest **report** to the leader (`reports` by `member_key`,
  `report_store.go:47-59`), ties broken by insertion order (rowid), as ReportsAfter; report ids are UUIDs and carry no order (A line 1f, 10-17); `kind` = the report kind, `text` = its first line. Journal
  request / comm entries carry no member identity (`internal/journal/entry.go:47-60`), so they are **not** a source.

### 4.4 Pin removal
- Remove `PUT /api/journal/pin`, `SetPinned`, `pin_in.go`, auto-pin in `journal_drain.go`; keep parsing old `pinned`
  meta. `info_handler.go` capability text updated.
- Transition (JV-1a review, **ended by #3155**): until the SPA's JV-3 was out, pages carried `pinned[]` (the same entries as
  `confirms[]`) and every entry a `pinned` flag, and `PUT /api/journal/pin` answered 410 `pin_removed`. JV-3 is out (750), so
  #3155 removed all three: pages carry `confirms[]` only, no entry has `pinned`, and the pin route is not registered (404).
  Old files with `"pinned"` in an entry's meta still parse; the field is ignored. A guard test pins the daemon side
  (`journal_nopin_test.go`) and the SPA side (`no-pin-ui.guard.test.ts`).

## 5. Settled with the user (2026-10-17)
- **Q1** The 「團隊」 button, now that the panel cannot be closed: it switches between **full** and **collapsed to the
  title bar** (a one-click minimise / restore); the header click still switches full ⇄ one line. From line, the
  button goes to the title bar; from the title bar, back to full.
- **§2** The original → new rows above are confirmed as written, including the withdrawal of journal plan :23.
- **U13** The running holder look of §3.8 (tint + outline + breathing dot, all three) is confirmed.

## 6. Plan (one task = one PR; TDD; full flow: this doc → codex once; PR → codex R1 + R2)

| Task | Where | What | Depends |
|---|---|---|---|
| JV-1a | daemon: `internal/journal`, `internal/module/workbook`, `internal/core/info_handler.go` | §4.1 hand checks (errors, untick, handler mapping) + `confirms[]`; §4.4 pin removal; `journal.confirm.v1` | — |
| JV-1b | same | §4.2 `sid` stamping, the two predecessor readers injected, sessions API, page by session; `journal.sessions.v1` | JV-1a (same page shape and handler) |
| JV-1c | daemon: `internal/team` (JournalFeed `Members`, `CurrentTask`), `internal/module/team` (impl), `internal/module/workbook` (route) | §4.3 members API; `journal.members.v1` (1f reviews the team part) | — |
| JV-2 | SPA team + journal | §3.1 panel back on every host (CardStack stops owning the team card), strip, Q1 button, member lines | JV-1c for the lines only (gated; the rest independent) |
| JV-3 | SPA journal | §3.2 float + 固定; §3.4 band from `confirms[]` + checkboxes; §3.5 pin UI removal | JV-1a for checks (gated) |
| JV-4 | SPA journal | §3.3 session pages | JV-1b (gated) |
| JV-5a | SPA unattended | §3.9 team names in the unattended panel (U15) | — |
| JV-5b | SPA team + unattended | §3.9 the team settings panel (gear), cap + folders moved, relay quotas in both places (U14) | JV-2 (the title the gear sits on) |
| WA-1 | SPA fleet | §3.6 host cell spacing | — |
| WA-2 | SPA watch + workspace | §3.7 guardian label, no group look, back button, Cmd+W, return target snapshot | — |
| WA-3 | SPA watch | §3.8 running holder look in the queue lanes | — |

Order: JV-1a → JV-1b; JV-1c in parallel. JV-5a, WA-1, WA-2, WA-3 any time. JV-2 → JV-5b. JV-3 / JV-4 after their
daemon tasks (each gated by its capability, so they may merge first and light up on deploy). WA-1 / WA-2 / WA-3 and the
look of JV-5b's panel are visual: Opus (the 10-16 rule); the rest Sonnet.

Tests (each with a mutation that turns it red):
- JV-1a: check / uncheck an item → file, `manual: true` and `journal.changed {what: entries}`; HTTP contract: 204 on
  success, 404 unknown entry and index out of range, 400 not a confirm (M: map every error to 500); a manual item is not
  in Haiku's open list and `tick` skips it (M: drop the `manual` skip); all checked → not in `confirms[]`,
  `open_confirms` drops; old file with `pinned=1` parses; pin route gone; the capability with the real module.
- JV-1b: new entries carry `sid`; the sessions wire (`started_at`, `ended_at`, `via`, `current`), oldest first, over
  relay and /clear merged in order (M: drop the /clear reader); old entries attributed (turn / ev by `wb_entries`, task
  by span); `page` returns only that session (M: ignore `page`); no `page` → the current session; the 1000 cap with
  `truncated`; an older page still carries the whole unit's `confirms[]` / `open_confirms`; `before` still answered; the
  capability with the real module.
- JV-1c: `Members` lists current members only (a released / killed member not listed); `CurrentTask` matches
  `currentTaskOf` (in progress before pending); `last` = newest report, ties by rowid; a member with neither → nulls; the
  capability with the real module.
- JV-2: three states and the strip content on a journal host **and** a non-journal host (M: keep the CardStack team
  card); no max; the header click (full ⇄ line); the 「團隊」 button: full → title bar, line → title bar, title bar → full
  (M: toggle line ⇄ full); 「工作簿」 opens only the journal card; state memory per team; member lines present / absent
  by capability; switching between a journal and a non-journal host keeps each team's own state.
- JV-3: outside click closes unfixed cards only (M: close fixed too); the 工作簿 button and the 待確認 detail are not
  outside; a fixed card still closes by its X and by 工作簿; each of the three cards on its own; 固定 off by default and
  remembered per unit (unit A's 固定 does not fix unit B's); band from `confirms[]` across sessions; checkbox optimistic
  + rollback; no pin UI left (a guard test greps for the pin API).
- JV-4: open on the current page at the bottom; the page header (span; 「目前」); 上一頁 / 下一頁 bounds; filters within
  the page; scroll memory per page (M: one value per unit); new entries append live on the current page only; without
  the capability the old `before` / load-older paging works as today.
- JV-5a: a lead row shows the team name (M: show the address); a daemon without names → the lead's title; a solo row
  keeps its title; held and paused rows too.
- JV-5b: the gear on a lead's team panel only; the cap stepper and folders in the team settings and gone from the
  unattended panel (M: leave the cap in the unattended panel); a quota change in the team settings shows in the
  unattended panel at once and the reverse (one store; M: a second copy of the value); the rules of the moved cap
  stepper (min = members in use, max 8) unchanged; a cold start (the unattended panel never opened) → opening the team
  settings reads the quotas and shows them (M: rely on the unattended panel's read); loading, error and a host without
  `team.relay_quota.v1`; two quick opens make one request (single-flight).
- WA-1: the badge size / offset classes, the indicator's left margin and the gap between cells (class assertions, each
  with a mutation); the guardian shield's badge unchanged; screenshots before / after (light, dark).
- WA-2: label 「守門員」 while the session runs (M: label by pane content); no group look in the area (M: group the fixed
  row); back button and Cmd+W return to workspace + tab (M: return to the workspace only); the target is the one taken
  on entry (switching tabs inside the area does not change it); the tab gone → the workspace's active tab; Cmd+W
  outside the area unchanged.
- WA-3: a holder card has the running tint / outline / dot, a waiting card does not (M: style every card); the dot uses
  the sidebar running light's keyframes and period (same class / variable, asserted); reduced motion stops the dot; the
  free slot and the paused look unchanged; theme tokens only (a guard: no hex colour in the changed files); screenshots
  light / dark.

Test tiers as CLAUDE.md (2026-10-15): changed tests while developing; PR tier on the tree with the latest main —
Go: the changed packages + `make lint`; SPA: `vitest related` + the guard tests + `main.test.tsx` + locale-completeness
+ `tsc` + eslint. **All tests go to air26 while it is in assist mode** (user 10-17), and to mserv (A-class only: SPA
`tsc --noEmit`, related / small vitest) when air26's heavy queue holds 2 or more; mlab only when neither can: Go through
`~/.local/share/purdex-lead/deploy/assist-verify.sh <sha> "<go test args>" --lint`, SPA through
`~/.local/share/purdex-lead/deploy/assist-run.sh <sha> --dir spa --pnpm -- "<command>" …` (commit first; no single
quotes in commands); mserv as in the watcher's `watch/assist/mserv/README.md`.
