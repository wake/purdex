# Workbook journal — spec (v4)

> v4 (2026-10-14) rewrites v3 after the user's redesign the same afternoon (§2, items 7–20). v3's daemon module that pulled events from the workbook, approvals, tasks, reports and transcripts is **dropped**: the journal is written by the workbook's own Haiku call. History: v3 / v3.1 and the daemon plan rev 2.1 are in the git history of this branch; J-0a (#2835) is paused.
> Supersedes the **display** half of `docs/specs/2026-10-09-session-workbook-spec.md` (v2, §10) and extends its summariser (§5) — §12 lists each clause.
> Owner: 88 (interface line). The summariser prompt, the workbook module and anything that reads the team module go past 1f (A line) for review.

## 1. Problem

The workbook view stacks 「目前狀況」, up to ~30 todos and several lists; a manager cannot tell what happened (user, 2026-10-13). The user wants **the workbook's content to be a journal report for a manager**: what happened, in order, one voice.

## 2. User decisions (do not reopen)

2026-10-13:

1. A journal-style report plus an event stream replaces the workbook view.
2. *(v4 refines into item 9)* Event families: execution, tasks, communication / requests.
3. Opening it **scrolls to the bottom** (newest last), and new entries keep **loading in by themselves**.
4. The journal and the team panel have **separate entries and separate panels that never overlap**; both are floating cards; **the team card is on the left**.
5. The reader is **a manager**; the writing reads like **a brief report to a superior**.

2026-10-14:

6. An ask (tool permission, agent question) is a kind of unread whose light is yellow; a hidden tab knocking is its own unread (badge rule; unchanged here).
7. **One report, written as now**: the journal is the workbook entry text, written by **the same per-turn Haiku call** (「出法維持現在，同一個事件中拋給 Haiku 去寫」). One call per turn writes all of that turn's journal lines (user: 「可以」).
8. **The journal is only a journal**: nothing about cards (approval, permission, ask cards), no handling or management from it.
9. **Kinds**: 請求 (by 使用者 | leader | member); 任務建立 / 任務完成 (with the task number); 執行 / 執行完成; 溝通; **待確認** (special, item 10).
10. **待確認**: when an agent needs the person's reply. One entry per message, **one checkbox per question** (「像現在你一個一個詢問我，就算是 6 項（同一筆內，6 個 checkbox）」); it opens to the left with the full detail, or the original question copied as is. Not only option-button prompts.
11. **Pin**: any entry can be pinned; a 待確認 is pinned automatically and floats at the top; it unpins when every item is answered, or by hand. **The checkboxes are ticked by Haiku** (「基本上是 Haiku」).
12. **Scope**: one journal per team, or per session when it is not in a team; **with a team, the whole team has one journal, recorded from the leader** — **only the leader's turns** (「乙，我只需要 leader 的日誌匯報」).
13. **Storage by project directory**: files live **in the project folder**; all worktrees of a project are the same project.
14. **In git**: the files are project-level assets and are committed (how: §3, open).
15. **Tasks tab** on the journal's right, openable; hovering a task entry highlights its task.
16. **Tasks** come from `pdx task` in a team; work tied to issues is dispatched with it too, so the tasks tab records both.
17. **A session without a team**: its tasks are the summariser's todos (88's proposal, 「照你的先試試看」).
18. **Roadmap tab** on the journal's left: project-level phases, three columns **階段 / 說明 / 狀態**, kept up to date; with a team it shows under the team card.
19. **Roadmap ownership**: the stages and descriptions are written by the lead agent (「目前所有 roadmap 或 phases 拆分都是我和你討論完後由你做的，所以統一由你進行」); **the 狀態 column is updated by Haiku** from the journal.
20. **Journal, tasks and roadmap are markdown files.**

The name pool (session names instead of the ref suffix, user 2026-10-14) is a separate spec (§13).

## 3. Decided after the prototype (2026-10-14)

- **Q-A, how the files reach git — 甲** (user: 「可以」): a dedicated branch `purdex/journal` in the project, written in its own worktree by the daemon, committed and pushed every ~10 minutes when something changed. Main is untouched (no direct push, no conflict with fast-forwards or PRs).
- **Q-B, the number on the 日誌 button**: the count of unresolved 待確認 **items** of the active tab's journal; nothing for ordinary entries, nothing when 0.
- **Q-C, filters**: 全部 / 執行 / 任務 / 請求與溝通 / 待確認 (a count of open items on 待確認). **Clicking the active filter again clears it** (back to 全部).
- **Prototype** (`docs/prototypes/workbook-journal-v4/`): layout approved; **a 待確認 row has no coloured left edge** (the yellow is on its chip and checkboxes only).
- **Q-D, §12 原文 → 新文**: all six rows confirmed (user 2026-10-14, 「1–6 ok」).
- **Go-ahead** for the journal and the name pool (user 2026-10-14, 「1, 2 可以」): prompt rounds → plan → codex → dispatch.

## 4. Journal units and scope

- **Project** = the repository the session's cwd belongs to, keyed by the main worktree's root (`git rev-parse --git-common-dir`, its parent). A cwd outside a git repository: the cwd itself.
- **Unit** = a team (while it lives) or a conversation (`conv_key`, the relay lineage root) that is not in a team.
- A **leader**'s turns write to its team's journal while it leads; a **solo** session's turns write to its own journal; a **member**'s turns write nothing to any journal (item 12) — the member's tab shows its team's journal.
- A session that becomes a leader starts writing to the team journal from that turn; its solo journal keeps what came before. A team that ends closes its journal (the file stays).
- A relay continues the unit (same lineage); a manual `/clear` starts a new solo unit.

## 5. Entries

### 5.1 Kinds and who writes them

| kind | label | written by | text |
|---|---|---|---|
| `request` | 請求 | Haiku | who asked whom for what — `by`: `user` / `leader` / `member` (e.g. the person asks the leader; a member asks the leader for a decision; the leader asks a member) |
| `task_new` | 任務建立 | daemon, from the task source (§6) | 「#<n> <title>」 |
| `task_done` | 任務完成 | daemon, from the task source | 「#<n> <title>」 + the closing line (the report summary, or Haiku's) |
| `exec` | 執行 | Haiku | what this turn did, report tone (today's `entry`) |
| `exec_done` | 執行完成 | Haiku | a thing finished (today's `thing_done`) |
| `comm` | 溝通 | Haiku | an exchange between seats or with the person that is not a request |
| `confirm` | 待確認 | Haiku | the questions the agent left for the person, one item each, plus the original text |

- Haiku never invents task numbers: task kinds come from the task source; Haiku only writes the closing line when it has one.
- A turn yields 0–6 entries (a quiet turn: none). Entries of one turn share its time and keep the order Haiku gave.
- **Actor**: the seat's name (its pdx name; the name pool's later) for `exec`, `exec_done`; `A → B` for `request`, `comm`, `confirm` (`→ 使用者` when it is the person).
- **Refs**: `#NNNN` (issue / PR), `PR #NNNN`, task numbers — links in the App.

### 5.2 待確認

- Written when the turn's final reply asks the person to answer or decide something and the question is still open at the turn's end. A question answered inside the same turn (an option-button prompt the person already answered) is not one.
- `items`: one per question, ≤ 60 chars each, in the order asked; `detail`: the reply's question part copied as is (scrubbed, ≤ 4 000 chars).
- Pinned when written. On every later turn of the unit, Haiku receives the open 待確認 items and returns the ones the person's message answered (`confirm_done`); the daemon ticks them. All ticked → unpinned (the entry stays in the stream).
- The person can unpin by hand (App → daemon → file); the items stay as they are.

### 5.3 Pin

- Any entry can be pinned or unpinned by the person from the App; 待確認 are pinned by the daemon.
- Pinned entries show in a band at the top of the journal card (newest first), and in the stream at their place.

## 6. Tasks

- **Team**: the team's `pdx task` rows — number = the task's seq (shown with its id, e.g. `7bcf08-12`), title = subject, state, owner, issue / PR refs from the subject and metadata. A task created → `task_new`; completed (or a `merged` report) → `task_done`.
- **Solo**: the summariser's todos (workbook v2 §5.5) are the session's tasks — number = the todo's stable number within the conversation, title, detail, open / done / dropped. An add → `task_new`; a done → `task_done`; dropped → no entry (shown struck through in the tasks tab).
- Written to `tasks.md` of the unit on every change (§8).

## 7. Roadmap

- One `roadmap.md` per project: a section per feature (`## <feature>`), each a table **階段 | 說明 | 狀態**.
- The lead agent writes and edits stages and descriptions (item 19) — in practice 88 for the interface line, 1f for the A line — as part of its spec / plan work.
- **狀態** is one of 未開始 / 進行中 / 審查中 / 完成 / 暫停. On a leader's (or solo's) turn, Haiku receives the rows of the features this unit works on and returns status changes (`roadmap`: `[{feature, stage, status}]`) when the turn shows one (e.g. 「J-0a merged」 → 完成); the daemon applies them to the file. Haiku never adds or renames stages.
- Shown in the roadmap tab; with a team, under the team card (item 18).

## 8. Files (item 20)

- On the branch `purdex/journal` (§3 Q-A), in the daemon's worktree of it: `.purdex/journal/<unit>/<YYYY-MM>.md`, `.purdex/tasks/<unit>.md`, `.purdex/roadmap.md`. The lead agent edits `roadmap.md` in that worktree; the daemon commits it with the rest. `<unit>` = the team's label slug + id suffix, or the solo session's name + ref.
- **Markdown that reads as a document and parses back** — each entry a block:

```markdown
## 2026-10-14

### 14:32 · 執行完成 · purdex-88-b8
RA-2 已 merge：拿掉「不再詢問」、補上終端機指令核准的提示，額度說明改成暫停的說法。
<!-- j id=wb:4812:1 kind=exec_done refs=#2829 -->

### 14:40 · 待確認 · purdex-88-b8 → 使用者 📌
- [x] 日誌檔要不要進 git
- [ ] 單一 session 的任務從哪來
<details><summary>原始提問</summary>

…the question part, as written…
</details>
<!-- j id=wb:4813:1 kind=confirm pinned=1 -->
```

- The daemon is the only writer of journal and tasks files (append, tick, pin); the roadmap file has two writers (the lead agent for stages, the daemon for 狀態) — the daemon rewrites only the 狀態 cell of a row it can match, and leaves a file it cannot parse untouched (one log line).
- The daemon keeps no second copy of the entries: the App reads them through the daemon, which parses the files (cached by mtime).

## 9. Summariser changes (workbook v2 §5, prompt v3)

- **Who**: unchanged trigger and path (the session's mod, `$.model.complete`, Haiku, `effort: low`). The journal part of the job is asked **only for a leader's or a solo session's turn**; a member's job stays v2 (push, thing, status, todos — the push line still serves its notifications).
- **Input adds** (journal jobs): `unit_role` (`leader` | `solo`); `open_confirms` (numbered items still open); `roadmap_rows` (the unit's features, stage + status); for a leader, the turn's `user_prompt` keeps the peer and report prefixes that tell who sent what (as today).
- **Output adds**: `events: [{kind: request|comm|confirm, by?, from?, to?, text, items?, detail?}]`, `confirm_done: [n…]`, `roadmap: [{feature, stage, status}]`. `push`, `thing`, `entry`, `status`, `thing_done`, `todos` stay as v2: `entry` becomes one `exec` entry, or `exec_done` when `thing_done` (so v2's validation and background re-write of a long entry keep working); `skip: true` drops that entry only. Prompt: `docs/specs/2026-10-14-workbook-journal-prompt-v3.md`.
- **Validation** (code): unknown kinds dropped; `request` / `comm` text ≤ 80 chars (cut at the last punctuation); `confirm` `items` ≤ 10 × ≤ 30, `detail` ≤ 4 000; unknown `confirm_done` numbers ignored; roadmap changes to unknown rows or with an unknown status ignored; entry as v2.
- **Measured before it ships**: prompt rounds on ≥ 30 real leader and solo turns (the v2 method, 1f's `workbook-exp/` set plus the 10-13 A-line turns): kinds right, 待確認 caught and ticked right, no invented task numbers, entry length as v2. The prompt document is `docs/specs/2026-10-14-workbook-journal-prompt-v3.md`.

## 10. Daemon

- **Module**: the workbook module grows the journal writer (no new module): applying a journal job's output writes the entries, ticks, pins, roadmap statuses and task mirror to the files; the team's task changes reach it through a read-only team feed (reviewed by 1f).
- **API** (capability `journal.v1`):
  - `GET /api/journal?session_id=<sid>&before=<cursor>&limit=50` → `{unit: {kind: team|solo, id, label}, project, entries: [… oldest first], pinned: [...], more, open_confirms: N}` — the unit is resolved from the session (a member → its team).
  - `GET /api/journal/tasks?session_id=<sid>` → the unit's tasks; `GET /api/journal/roadmap?session_id=<sid>` → the project's roadmap.
  - `PUT /api/journal/pin` `{session_id, entry_id, pinned}`.
  - Host event `journal.changed` `{unit, project, what: entries|tasks|roadmap}` → the App re-reads what it shows.
  - Device tokens (iOS) may read and pin.
- **Git** (§3 Q-A): one worktree of `purdex/journal` per project under the daemon's data folder (created on first write from the remote branch, or as an orphan branch when none exists); a commit every ~10 minutes when files changed (author `Purdex journal`), then a push; a failed push is retried next round and never blocks writing; the branch is never merged into main.

## 11. Mac App

- Title bar: 「團隊」 (team tabs) and 「日誌」 (any Claude Code tab whose host has `journal.v1`) buttons, as v3.
- **Layout** (left → right): team card (team tabs) with the **roadmap tab** under it — or the roadmap tab alone without a team; the **journal card**; the **tasks tab**. Roadmap and tasks open and close from the journal card's header. Narrow windows drop, in order: the tasks tab, the roadmap, the team card; the journal stays.
- **Journal card**: header (unit name, role line, live dot, close); the pinned band; filters (Q-C; the active one clicked again clears it); the stream oldest at the top, newest at the bottom, a date divider per day; row = time, kind chip, actor, text, refs; a 待確認 row (no coloured left edge) shows its checkboxes (read-only — Haiku ticks them) and opens its detail **to the left** over the roadmap side; a row's menu pins / unpins.
- Opens scrolled to the bottom; new entries append; pinned to the bottom while there, else 「↓ N 則新事件」; scrolling up loads older months.
- **Tasks tab**: open tasks first, then done; hovering a `task_new` / `task_done` row highlights its task, and hovering a task highlights its rows.
- **Roadmap tab**: the project's features, each a 階段 / 說明 / 狀態 table, live.
- Tab-hosted rule: open states, widths, filter and scroll position live outside the components, per unit; regression test with the real `TabContent`.

## 12. What v4 replaces (原文 → 新文; Q-D, confirmed)

| workbook v2 clause | becomes |
|---|---|
| §10.1 one shared panel (title bar / line / full / max) behind one notebook button | two buttons, separate cards (§11) |
| §10.1 the workbook view: 目前狀況, 待辦, entries grouped by thing, 紀錄｜待辦, 重整 | the journal card; 目前狀況 is not shown; 待辦 becomes the solo session's tasks tab; 重整 is not shown (`/workbook refresh` in the session stays) |
| §10.1 a team row's workbook button opens that seat's workbook | removed: members have no journal; the team's journal is the leader's |
| §2 items 9, 11 (todos mixed or alone, read-only) | todos are the solo tasks tab, read-only |
| §10.3 iOS 目前狀況 / 待辦 / 已完成 / 紀錄 | one 日誌 segment later; until then iOS keeps v2 |
| v3 (this file's previous version) — journal module, `等我回覆` from cards, member journals, transcript ingest | dropped (items 7, 8, 12) |

The summariser trigger, push, storage of `workbook.db` and secrets handling (v2 §4, §7, §8) are unchanged.

## 13. Out of scope

- **Name pool** — separate spec: a pre-built list of 30 short names (mixed), a session takes a random unused one at start (one scan of all hosts' addresses), the relay keeps it, it frees at the end; it replaces the ref suffix (`purdex-3f-adam`). Touches the peer address shared by all lines (agreed with 1f first).
- Members' own journals; remote seats' turns (they are members); editing entries by hand (pin only); iOS (later, J-3).

## 14. Phases (rough; the plan splits to ≤ 800 lines / 20 files)

| phase | side | content |
|---|---|---|
| J-P | docs | prompt v3 rounds on real turns (§9), prompt document |
| J-1 | daemon | project / unit resolution, file writer and parser, journal job output applied, task mirror (team feed, 1f), roadmap status, API, `journal.changed`, `journal.v1`, the `purdex/journal` branch worktree and its commit / push loop |
| J-2 | SPA | buttons, journal card, pinned band, 待確認, filters, tasks tab, roadmap tab, layout, tab memory, workbook view removed on `journal.v1` hosts |
| J-3 | purdex-ios | the 日誌 segment |
