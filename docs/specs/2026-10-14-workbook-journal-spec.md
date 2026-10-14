# Workbook journal — spec (v3.1)

Supersedes the **display** half of `docs/specs/2026-10-09-session-workbook-spec.md` (v1 + v2, §10) — see §9 for each clause it replaces. The summariser (§4–§8 of v2) stays as it is and feeds the journal.
Prototype the user reviewed (2026-10-13): `docs/prototypes/workbook-journal/` (source, fixture of the A line's real events on 2026-10-13, five screenshots). Data inventory behind §4: lead notes `journal-sources-2026-10-14.md` (88).
Owner: 88 (interface line). Daemon pieces that read the team module go past 1f (A line) for review.

## 1. Problem

The workbook view stacks 「目前狀況」, up to ~30 todos and several lists; a manager cannot tell what happened (user, 2026-10-13). The user wants a **journal**: what happened, in order, written like a short report to a manager.

## 2. User decisions (2026-10-13, do not reopen)

1. A **journal-style report plus an event stream** replaces the workbook view.
2. Three event families: **執行** (an execution report, an execution finished), **任務** (created, completed), **溝通或請求** (to the user, between peers).
3. Opening it **scrolls to the bottom** (newest last), and new events keep **loading in by themselves**.
4. The journal and the team panel have **separate entries and separate panels that never overlap**. Both are **floating cards**; opening one pushes the other to the left; **the team card is on the left**.
5. The reader is **a manager**; the writing reads like **a brief report to a superior**.
6. *(2026-10-14, badge)* an ask (a tool-permission prompt, an agent question) is a kind of unread whose light is yellow; a hidden tab knocking is its own unread. The journal's 「等我回覆」 follows the same notion (§5.3).

## 3. Open — the user decides (88's proposals)

- **Q1 — who writes the 「報給主管」 lines.** Proposal: **by event type, no new model call** (§4.2): execution uses the workbook entry the summariser already writes in report tone (only turns that made progress); tasks use the `pdx task` subject and the `pdx report` summary as written; communication and requests use the first sentence of the original text. The alternative — the daemon batches events and has a model rewrite them — adds a call per batch, a delay, and a second voice for the same fact.
- **Q2 — a number on the journal button.** Proposal: **only the count of 「等我回覆」** (§5.3), nothing for ordinary new events (the badge already counts unread).

The rest of this spec is written on the two proposals; changing either only touches §4.2 and §6.2.

## 4. Events

### 4.1 Scope — whose journal

- A journal belongs to **a conversation** (`conv_key`, the relay lineage root, as the workbook's §4.1): relays continue it, a manual `/clear` starts a new one.
- **A lead's journal includes its team**: its own events, every event of a seat while it was in that team (`team_id` on the record), the team's tasks and reports, and the messages between the team's seats.
- A member's journal: its own events, the tasks it owns (only a lead creates tasks), the reports it wrote, and the messages it received or sent (a sent one when its sender is resolved, §4.4).
- A solo session: its own events.
- The Mac journal card shows **the active tab's conversation** (§6.1); a team row's notebook button narrows it to one seat (§6.1).

### 4.2 Kinds, sources and text

| kind | family | label | source (daemon) | title | body |
|---|---|---|---|---|---|
| `exec` | 執行 | 執行 | workbook entry, `state = ok`, `kind = turn`, `thing_done = false` | entry `push` | entry `entry` |
| `done` | 執行 | 完成 | workbook entry, `state = ok`, `thing_done = true` | 「完成：」+ `thing` | entry `entry` |
| `task` | 任務 | 任務 | team task created | task `subject` | task description, first 2 lines |
| `taskdone` | 任務 | 完成任務 | task status → `completed`; **or a `merged` report** (§8 gap 2) | task `subject` | the report `summary` |
| `comm` | 溝通 | 溝通 | a peer message (§4.4); **the user's own words** to a session (§4.4) | first sentence, ≤ 80 chars | the rest, ≤ 400 chars |
| `req` | 請求 | 請求 | an approval opened for a seat in scope (all six kinds); a report with `needs = user` (`question`, `blocked`) | the card's question / command / relay line; the report `summary` | the card's detail; the report body, first 2 lines |

- Workbook entries in `pending`, `failed` or `skipped` produce nothing; `refresh` entries produce nothing.
- `ready`, `progress`, `ack` reports produce nothing (they are noise for a manager; the roster already shows the current task).
- **actor**: `<seat title>` for a seat's own event; `A → B` for a message or request (`→ 使用者` for a request to the person). Seat titles are the ones known when the event is recorded (live events: at the event; backfilled rows: the stored title, else the ref); no title history exists.
- **refs**: PR numbers, issue numbers and task ids found in the record's fields (`metadata.prs`, report `pr`, `#NNNN` in the text) — each a link in the App.

### 4.3 Record

```json
{"id":"…","conv_key":"…","other_conv_key":"","team_id":"…","seat_session_id":"…","at":1760400000000,
 "kind":"exec|done|task|taskdone|comm|req","actor":"…","title":"…","body":"…","refs":["PR #2765"],
 "pending":false,"source":{"type":"workbook|task|report|approval|peer|user","id":"…"}}
```

`id` is stable per source record (`<source type>:<source id>`), so re-ingesting is idempotent. `pending` is §5.3. `other_conv_key` is a second conversation that sees the event: the resolved sender of a `comm`, the task owner of a report event written by someone else.

### 4.4 The conversation-derived events (gaps 1 and 4)

- `pdx msg` stores no text (`peer_messages` is metadata only), and the user's words are not stored either. Both are in the **recipient's transcript**: a user item with `source = peer` (and its `from`), or `source = user`.
- On each **turn end** of a session in scope (the trigger the workbook already uses), the daemon reads that turn's user items through the conversation model and records one `comm` per item with `source` `peer` or `user`. Other sources (`queued`, `slash`, `bash`, `task`, `scheduled`) are skipped.
- The text is scrubbed with the workbook's §8 rules before it is stored, then cut to the first sentence + 400 chars. Images and attachments are never stored.
- A message is recorded once, from the recipient's side, so a lead→member message appears in the lead's journal through the member's transcript (the member is in scope).
- Not covered: a message whose recipient never ends a turn (it shows when it does), and a remote seat on another host (its transcript lives there; §10).

## 5. Daemon

### 5.1 Module `journal` (`journal.db`, 0600)

- One table `journal_events` (§4.3 columns + `created_at`), indexed on `(conv_key, at, id)` and `(team_id, at, id)`; `pending` rows indexed on `(pending)`.
- **Ingest** (all idempotent on `id`):
  - workbook: subscribe to the module's entry insert/state change (the `workbook.entry` event);
  - approvals: subscribe to `approval.request` opened / closed (in process);
  - tasks and reports: tail by cursor — reports by `rowid`, tasks by `(updated_at, team_id, seq)` — every 5 s, through a **read-only feed the team module exports** (`team.JournalFeed`; the only team change, reviewed by 1f);
  - conversations: §4.4.
- **Backfill** at first start: workbook entries, tasks, reports and open approvals already stored (bounded to the last 30 days). Conversation-derived events start at deploy (no transcript backfill).
- Retention: as the workbook — no automatic deletion (rows are short; measured size goes in the plan).

### 5.2 API and events (capability `journal.v1`)

- `GET /api/journal/conversations/claude/{session_id}?before=<at>:<id>&limit=50[&seat=<session_id>]` → `{conv_key, team_ids:[…], events:[… oldest first], more: bool, pending: N}` (`team_ids`: every team this conversation has led). Without `before`: the newest page. `seat` narrows to one seat (§6.1).
- Host event `journal.event` `{conv_key, other_conv_key, team_ids:[…], op:"add"|"update", event}` — `update` when `pending` flips or an empty body is filled (a `taskdone`'s summary). A client keeps the events of the journals it shows.
- Device tokens (iOS) may read both.
- `404 not_found` for a session the daemon has never seen; an empty journal is `events: []`.

### 5.3 `pending` — 「等我回覆」

- A `req` from an approval is pending while the approval is `open`; its close flips it (op `update`).
- A `req` from a `needs = user` report is pending until the next report on that task, or the task leaves `in_progress` / `pending`.
- The journal answer carries `pending` (the count in scope) — the button number of Q2.

## 6. Mac App

### 6.1 Entries and cards (decisions 3–4)

- The title bar gets **two buttons**, 「團隊」 (Phosphor `UsersThree`) and 「日誌」 (`Notebook`), where today's single notebook button sits. 「團隊」 shows only on a team tab; 「日誌」 shows on any Claude Code tab whose host has `journal.v1`.
- Each opens its own **floating card** under the title bar (today's `TeamPanelArea` layer, right-aligned). Both open: **team card on the left, journal card on its right**; opening one pushes the other left (animated). The team card keeps its modes and width (`useTeamUiStore`); the journal card has its own width (same bounds) and open state.
- A window too narrow for both: the journal card stays, the team card hides until there is room (prototype rule: width < 2 × card + gaps).
- A team row's notebook button opens the journal card **narrowed to that seat** (`seat=`), with a chip 「只看 <seat>」 that clears it.

### 6.2 The journal card

- Header: the conversation (title, role line 「lead · <team> · N 位 member」), live dot, close.
- Filters: 全部 / 執行 / 任務 / 溝通與請求 / 等我回覆 (the prototype's), with counts on 等我回覆.
- Body: events **oldest at the top, newest at the bottom**; a date divider per day; each row: time, kind chip (the prototype's six colours — distinct hue and lightness, readable in greyscale), actor, title; refs as links; body collapsed to two lines, a click expands.
- Opening scrolls to the bottom. A new event appends; if the reader is at the bottom it stays pinned, else a 「↓ N 則新事件」 button appears.
- Scrolling to the top loads the previous page (`before=`), keeping the reading position.
- A pending `req` row has the yellow light and a 「去處理」 action that opens the card or the session where it is answered.
- **Button number (Q2):** the 「日誌」 button shows the `pending` count of the active tab's journal; nothing when 0.
- **Tab-hosted rule (CLAUDE.md):** open state, width, filter, seat narrowing and scroll position live outside the component (per conversation) and survive a tab switch; a regression test with the real `TabContent`.

### 6.3 What leaves the Mac UI

The workbook view (`OwnWorkbookPanel`, `TeamSeatWorkbookView`, `WorkbookStatus`, `WorkbookTodos`, `WorkbookToolbar`, the 紀錄｜待辦 switch, 重整) is removed on hosts with `journal.v1`; on older hosts it stays as it is. The team row line 「正在做的任務」 stays as it is.

## 7. iOS

Later phase (J-3), designed by 88: the session detail page's workbook segments become one 「日誌」 segment with the same events, filters and paging; the Tab-info content stays as it is. Until then iOS keeps v2's page.

## 8. Data gaps (handoff 2026-10-13) and how each is handled

1. **`pdx msg` text not stored** → read from the recipient's transcript at turn end (§4.4); no text is added to the peers store.
2. **Tasks are not closed on merge** → the journal treats a `merged` report as `taskdone` (§4.2). Closing the task itself on merge stays an A-line question (issue for 1f, not needed by the journal).
3. **Summaries leaking the summariser's thinking** → the code path does not send thinking and rejects non-JSON output (`input.go:82-92`, `validate.go:55`), so the leak is in the model's written `entry`. The plan collects the examples from `workbook.db` and fixes the prompt (v2 prompt doc) if they are real; the journal titles use `push` (short) to lessen it.
4. **The user's words not stored** → user items with `source = user` (§4.4).

## 9. What v3 replaces (原文 → 新文)

| v2 clause | becomes |
|---|---|
| §10.1 "The workbook and the team panel share one panel area" with four states (title bar, line, full, max) behind one notebook button | two entries, two floating cards (§6.1); the team card keeps its modes |
| §10.1 the workbook view: 目前狀況, 待辦, entries grouped by thing, 紀錄｜待辦 switch, 重整 | the journal card (§6.2); status, todos and refresh are not shown |
| §10.1 a team row's workbook button drills into that seat's workbook | it opens the journal card narrowed to that seat (§6.1) |
| §2 items 9, 11, 12 (todos readable mixed or alone; read-only; 重整 from the Mac view) | not shown on the Mac; the summariser still keeps them (data unchanged); `/workbook refresh` in the session stays |
| §10.3 iOS segments 目前狀況 / 待辦 / 已完成 / 紀錄 | one 日誌 segment, later (§7) |

The summariser, the push, storage and the workbook API (v2 §4–§9) are unchanged.

## 10. Out of scope

- Remote seats' conversation-derived events (another host's transcript); their tasks, reports and approvals on the lead's host are in.
- Editing or deleting journal events; the employee workbook (v2 §11).
- Rewriting events by a model (Q1's alternative), unless the user picks it.

## 11. Phases (each ≤ 800 lines / 20 files)

| phase | side | content |
|---|---|---|
| J-0a | daemon | team read feed `team.JournalFeed` (1f reviews) |
| J-0b | daemon | workbook entry feed |
| J-1a | daemon | module `journal`: store, ingest from workbook / approvals, backfill, API, `journal.event`, `journal.v1` |
| J-1b | daemon | tasks / reports ingest |
| J-1c | daemon | conversation-derived `comm` (§4.4), scrub, turn-end trigger |
| J-2a | SPA | title-bar split (團隊 / 日誌), journal card shell and placement, store, API + events, tab-switch memory |
| J-2b | SPA | rows, filters, paging, live pinning, pending count on the button, seat narrowing, removal of the workbook view on `journal.v1` hosts |
| J-3 | purdex-ios | the 日誌 segment |
| — | docs | gap 3 prompt check (with J-1a) |
