# Interface U3 — the Mac App's deck and chat for Claude Code terminal sessions

Status: draft for the user's review (2026-10-10). Line: interface (88). Builds on U1 (`docs/specs/2026-10-08-interface-u1-spec.md`, the daemon conversation model and API) and the design document `docs/pages/interface-language.html` (§5 deck, §8 dock, §9 fallback, §10 chat, §11 desktop, §7 status bar).

## 1. Goal

A Claude Code tab on the Mac can show its conversation three ways — **終端機** (the terminal, as today), **指揮台** (deck: the terminal drawn as a structured record) and **聊天** (chat: a briefer exchange) — and switch between them without touching the session. The deck and chat read the daemon's conversation API (`conversations.v1`), can send, and answer the agent's questions above the input. Done = the five phases of §8 merged and the acceptance of §9 passed on the real Mac App.

Out of scope: approval cards in the dock (P8b — user 2026-10-08 and 2026-10-10: not brought forward; approvals stay with today's app-wide approval dialog); a Files view; Codex and Nexen workers in this view (U4 / U5); several people in one chat.

## 2. User decisions (2026-10-08 … 10-10, do not reopen)

1. **Three view buttons, view only.** 終端機／指揮台／聊天 switch what the pane shows; tmux and the session do not move. Handing the session to an execution (and taking it back) is **a separate control** (design doc §15, user 2026-10-08).
2. **Default view: 終端機** ("我用看看 deck 習慣不習慣", 2026-10-10).
3. **Each tab remembers its own view**, like iOS (2026-10-10).
4. **The deck is fully open — the terminal, drawn.** Every step is shown; nothing folds by default; there is no density or "show tool calls" switch. To read less, switch to chat (2026-10-10).
5. **Reference order: Collie first, then our iOS app** — for the **interface** (how things look: cards, icons, the user block, tool cards, the status slot) and **how the transcript is broken into items** ("拆資料"), not Collie's behaviour rules (2026-10-10). Collie = ColliePWA 1.17.2, source kept at `~/Library/Caches/purdex-research/collie-1.17.2/` (mlab).
6. **Unreadable conversation: no automatic switch.** The deck / chat says it cannot read the conversation correctly and offers a button to the terminal (2026-10-10).
7. **Enter sends, Shift+Enter breaks a line** (2026-10-10).
8. **Approval cards are not in U3** (P8b; 2026-10-10 kept the 10-08 decision).
9. Chat wording and behaviour already decided with iOS: **one row per chain of work (一串工作一列** — consecutive steps; *corrected 2026-10-10 from "per turn", which misquoted the decision*), 「處理了 N 分 N 秒」; a click on that row shows the turn's detail (2026-10-09/10).

## 3. Views and the pane

- The three buttons sit where `PaneModeButtons` is today (status bar, right side), for a `tmux-session` pane whose agent is Claude Code on a host with `conversations.v1`. Other agents, hosts without the capability, and Nexen execution panes are unchanged (an execution pane keeps its own room / chat; the terminal button is not shown there).
- **The view is per tab** (the tab's pane that shows the session; a split tab keeps one value per pane), device-local, persisted, default 終端機. A new tab starts at 終端機. Switching tabs never changes it.
- **Switching is view-only**: the terminal stays connected in the background while the deck or chat is shown, so switching back is immediate and nothing is replayed.
- **Hand to execution / take back** is one control beside the three, visibly separate (an arrow glyph, its own tooltip: 「交給執行體」 on a tmux pane, 「拿回終端機」 on an execution pane), and runs today's flows unchanged (`useHandoffDialogStore`, the take-to-terminal registry). The three buttons no longer open the handoff dialog.
- **Tab-hosted rule** (CLAUDE.md): the view, the deck / chat scroll position, which long outputs are expanded, the input draft and the right panel's state live outside the components and survive a tab switch; a regression test switches away and back with the real `TabContent`.

## 4. The deck (指揮台)

Data: `GET /api/conversations/claude/{session_id}` (last N turns, `before=` for older pages, `around=` to land on an item) and the live WebSocket (`conversation.snapshot` / `.changes` / `.reset` / `.header`, `approvals.snapshot` / `approval`), U1 spec §8.2. A `reset` replaces the view's data with the fresh snapshot, keeping the reader's place by item id when it is still there. Subagent steps load on demand (`.../subagents/{agent_id}`), never inlined.

Rendering — **Collie's look**, our words (zh-TW), every item shown:

| Item | Drawn as |
|---|---|
| user | the only tinted block: a framed well, caption 「你 · HH:mm」; other sources get their caption (「你 · 排隊中」, 「來自 <name>」, 「背景任務回報」, 「排程喚醒」); bash-mode input and `command_output` sit under it, output folded to 「輸出 · N 行」 that opens to the last 10 lines (「…已截斷」 when cut) |
| agent_text | unframed Markdown; streaming text grows in place with a cursor (the mod's live source; deltas merged every 150 ms). *Amended (plan, 2026-10-10): the API is transcript-only today (U1-5 not wired), so a message appears whole; the renderer honours `streaming: true` so U1-5 lights it up without a client change.* |
| thinking | a collapsed 「思考」 disclosure (duration when known) |
| step · edit | a card: icon (pencil / new file), 「編輯」／「新增」, path, green `+n` / red `−n`, the hunks up to 16 lines, 「顯示全部 N 行」 |
| step · execute | a 「執行」 card: `$ command` in a dark box clamped to 6 lines; its output folded to 「輸出 · N 行」, opening to the last 10 lines, 「顯示全部」 |
| step · read | one line: icon, 「讀取」, path, 「第 a–b 行」 |
| step · search / fetch / other | one line (icon, verb, subject); a click shows its output |
| step · task (subagent) | one line 「子 agent · <type>」 + its description; a click opens its steps in the right panel (§5) — not nested in the stream |
| question (AskUserQuestion step) | a card with its options; the chosen one marked ✓ once answered; while open it reads 「請在下方回答」 (the answering card is in the dock, §7) |
| system | short notices as small centred grey text; long machine notes folded under 「系統」; compaction as 「Context 已壓縮 · HH:mm」; interrupted as 「已中斷」 |

- **The status slot**: every step line / card has a fixed-width slot at its right — a running dot, a red 「失敗」 chip, a grey 「已拒絕」 / 「已中斷」 chip (`denial: interrupted` → 已中斷, any other or unknown denial → 已拒絕), or 「exit N」 for a non-zero exit — so nothing shifts when a step finishes.
- **No folding of runs** (decision 4). The daemon's `omitted_items: N` (a turn too long to send whole) is a grey, non-clickable row 「前面還有 N 步未顯示」.
- Long outputs and diffs open in the right panel on 「顯示全部」 (desktop, design doc §11).
- Timestamps only on user blocks and the compaction line (Collie).
- **How items are broken out** follows Collie's transcript parsing (§6).

## 5. Chat (聊天) and the right panel

- Same conversation, less detail. A header with the agent's icon, the tab's title and one line of what it is doing (the live status). The user on the right in accent bubbles, the agent on the left.
- **One row per chain of work** (consecutive steps; thinking does not break a chain, user / agent text / system items do — as iOS), above the agent's text that follows it: 「處理了 2 分 13 秒 · 3 個指令、2 個編輯 · 1 失敗 ›」 — the categories in the fixed order 指令、編輯、讀取、搜尋、網頁、子 agent、其他, then failed (red) and 已拒絕／已中斷 (grey) counts. While a turn runs, one progress message updates in place; a turn that changed files adds a chip 「3 個檔案 +29 −12」.
- **A click on a work row** opens the **right panel** with **that chain's steps** drawn as the deck draws them (the user / agent text around it stays in the chat; the panel's header names the turn and the chain's position in it). The right panel is one component inside the pane, shared with the deck's 「顯示全部」 and subagent steps; it closes with Esc or its close button and keeps its open state per tab (tab-hosted rule).
- The counting of categories is the same in iOS and the Mac: one shared fixture (input items → expected turn row), produced from the iOS implementation and checked by both apps' tests.

## 6. Breaking the transcript into items (拆資料)

The daemon's normalizer (`internal/convmodel/ccnorm`, U1-4) is what both apps read. U3-0 compares it with Collie's parser on the same transcripts and aligns ours where Collie splits finer or summarises better — at least: the AskUserQuestion tool as a question item (questions, options, answers), the read range, a created file, the search scope, the compaction summary, and each kind's one-line summary text (a side-by-side table against Collie). *Amended (plan, 2026-10-10): Collie's `delete` / `move` kinds come from tool names Claude Code does not have (`rm` / `mv` inside Bash stay commands in Collie too), so they are dropped; image attachments are already on the items and the apps draw them as a notice.* New kinds and fields are additive (U1 §8.1 evolution rule): older clients show them as `other`. The golden fixtures are updated and shared with iOS.

## 7. Input and the dock

**Input** (deck and chat, below the stream):
- Enter sends, Shift+Enter breaks a line.
- Sending goes through the daemon's `POST /api/sessions/{code}/send-keys` with the expected tmux instance (as iOS does), with Collie's safeguards: the text is typed **without** Enter, the pane's screen is read until the input box visibly holds it and only then is Enter pressed; if it never appears, Enter is withheld and the draft is kept. *Amended (plan, 2026-10-10): **no bracketed paste** — Claude Code wraps pasted text in `<pasted_content>` (iOS measured, 2026-10-07), so the text is sent as iOS sends it (a literal LF for new lines, ≤ 4000 UTF-8 bytes, iOS `SendPlan` rules), and Collie's paste-placeholder and 800-character rules do not apply.* A draft that looks destructive (e.g. `rm -rf`) needs a second press (「真的要送出？」). *Amended (plan review, 2026-10-10): the check and the keys happen in **one daemon route** (`POST /api/sessions/{code}/submit`, U3-0b), not in the App — text seen anywhere on the screen proves nothing (an agent's output can contain it), so the daemon checks, at the moment it types and again before Enter, that the pane still runs that Claude Code session (instance, owner, session id), is not in copy mode or the alternate screen, and that the text sits in the input region at the cursor; any check failing stops before Enter (fail closed) and says why. iOS can use the same route.*
- While the agent works, a sent message is shown as 「你 · 排隊中」 until its turn starts (*plan review 2026-10-10: it waits in the App and is typed when the agent turns idle — the daemon never types into a busy Claude Code, so a dialog cannot catch the Enter*); a 「中斷」 button (Esc to the pane) sits by the send button.
- Without the mod, a draft starting with `/` or `!` is blocked with 「這個指令要在終端機輸入」; interactive menus (`/model`, Rewind) always say so.
- The draft survives a tab switch (tab-hosted rule).

**The dock** (above the input, shared by deck and chat; questions only — P8a):
- An open question (AskUserQuestion, a `hook_ask` approval) shows as one card: the question, its options, 「其他」 with free text, and 「改成跟 agent 聊聊」 (the ask-chat reply, `docs/specs/2026-10-09-ask-chat-spec.md`). The stream keeps only its non-clickable card; the input's placeholder reads 「agent 正在等你回應，請在上方回答」.
- **Bound to its question**: a card whose request has closed or changed locks at once and says 「題目已變更」 (no queued taps, no retry). Answered anywhere (terminal, phone, another window) → every card closes; if the terminal answered first, the card briefly reads 「已在終端機回答：X」.
- A question only the terminal can answer (`terminal_only`, with its reason) says so with a 「開終端機」 button. Every card has a 「終端機」 button that switches this tab's view to the terminal (not remembered as a new choice).
- The terminal view draws no dock; it shows a thin strip 「● 等你回答」 at its top while a question is open.

**Unreadable** (decision 6): no transcript, an empty first turn, or data the App cannot read → the deck / chat shows 「無法正確讀取這個對話」 with the reason and a 「切到終端機」 button. Nothing switches by itself.

## 8. Phases (each one PR ≤ 800 lines / 20 files, else split)

| Phase | Where | What |
|---|---|---|
| U3-0 | daemon | §6: align `ccnorm` with Collie's breakdown (side-by-side on golden transcripts; additive kinds / summaries; fixtures to iOS) |
| U3-1 | SPA | §3 view buttons per tab + the separate handoff control; the conversation client (REST + WS, reset, paging, subagents on demand); the deck read-only (§4) with the unreadable state |
| U3-2 | SPA | §7 input: send with the safeguards, queued messages, interrupt, `/` `!` blocking, draft memory |
| U3-3 | SPA | §5 chat + the right panel (also used by the deck's 「顯示全部」 and subagents); the shared turn-row fixture with iOS |
| U3-4 | SPA | §7 dock (questions, P8a) + the terminal view's 「● 等你回答」 strip |
| U3-5 | SPA | the status row of design doc §7 (state · model · effort · context / 5 h / 7 d rings by today's ring rules · cost; a click opens the right panel) — **a real SPA prototype shown to the user first**, then built |

U3-1 and U3-2 together make the deck usable day to day; the user tries it from there.

## 9. Acceptance (real Mac App, a live Claude Code session on mlab)

1. A Claude Code tab: 終端機 → 指揮台 → 聊天 → 終端機; the terminal never reconnects; another tab keeps its own view; a reload keeps both.
2. The deck shows a turn with an edit, a command with long output, a read, a search, a subagent, a failed and a denied step, a compaction — each as §4, every step visible, the status slots aligned.
3. Send from the deck: a short line, a multi-line text, an 1 200-character text (one paste), `rm -rf /tmp/x` (asks twice); a message sent while the agent works shows 排隊中 and runs next; 中斷 stops a turn.
4. The agent asks (AskUserQuestion): the dock card answers it; answering in the terminal instead closes the card with 「已在終端機回答」; a changed question locks the card.
5. Chat: the turn rows count as iOS counts (shared fixture), a click opens the turn in the right panel.
6. A session with no transcript yet: 「無法正確讀取」 + the button; nothing switches by itself.
7. Tab switch away and back in the middle of each of the above keeps scroll, expanded outputs, the draft and the right panel.

## 10. Open for the plan (technical, 88 decides)

- ~~Where the send verification reads the screen~~ — decided: the daemon's `/submit` route (§7 amendment, plan D7); the App never verifies from its own terminal buffer.
- The deck's virtualisation for long conversations (paging by turns is given; whether rows are virtualised).
- Which existing execution-view pieces are reused (diff view, output folding, scroll memory, transcript search) and how they take `convmodel` items.
