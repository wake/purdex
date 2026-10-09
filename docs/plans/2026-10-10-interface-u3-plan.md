# Interface U3 — implementation plan

> For agentic workers: implement task by task with TDD (failing test first), one commit per task, PR per phase
> (≤ 800 lines / 20 files, else split into stacked PRs). Review: codex R1 + attack + critic. Full test suites only before
> merge, with a slot from the coordinator (1f). Every Bash command `cd <worktree> && …`. Never cat/grep files under
> `~/.config` (secrets).

**Spec:** `docs/specs/2026-10-10-interface-u3-spec.md` (read it first; §2 decisions are final).
**Goal:** a Claude Code tab on the Mac switches 終端機 / 指揮台 / 聊天 per tab; the deck and chat read the daemon's
conversation API, can send, and answer questions in a dock.

## 0. Facts (measured on main 1bc55471, 2026-10-10)

Daemon (`internal/module/conversation/`, U1 spec §8.2):
- `GET /api/conversations/claude/{session_id}` — `turns` (default 20, 1–200), `before=<turn index>`, `around=<item id>`;
  `after=<cursor>` for increments (`after`+`turns` → `turns_and_after`, `after`+`before|around` → `bad_query`,
  `before`+`around` → `before_and_around`). Snapshot `{reset?, conversation, header, window{first_index, last_index,
  total_turns, has_more_before}, cursor}`; increment `{changes:[{turn, items}], header, cursor}`; stale cursor or a
  catch-up over 4 MiB → `reset:true` + a full snapshot. Items carry an API-only `index`; a turn may carry
  `omitted_items` (skip an item whose `index` is below the lowest held for that turn). Errors `{error: code}`.
- `GET /ws/conversations/claude/{session_id}?after=&turns=` — frames `{type, seq, value}`, `seq` contiguous from 1:
  `conversation.snapshot | changes | reset (then a snapshot) | header`, `approvals.snapshot {approvals}` (replaces the
  set), `approval {op: opened|closed, approval}`. A `seq` gap → reconnect with the last cursor. Auth: Bearer, or
  `?ticket=` from `POST /api/ws-ticket`.
- **Transcript-only**: no streaming (`streaming` never set; U1-5 not wired), header usage is `{model, effort}` only
  (U1-7 not wired), capabilities declare `send / interrupt / answer_question` as `not_wired`. `/api/info` has
  `conversations.v1` and `team.ask_chat.v1`.
- Pane → conversation: `GET /api/sessions/{code}/provenance` → `{found, session_id, tmux_instance, agent_type, …}`
  (`found:false` is normal). Re-read when the pane's agent events report a session change (`/clear`, relay).
- `POST /api/sessions/{code}/send-keys` `{keys, expected_tmux_instance?}` → 204; 409 instance mismatch; 404 unknown;
  errors are plain text. iOS sends `[body, "\r"]` as two requests; ESC = `"\u001b"`.
- Questions: a `hook_ask` approval, payload `{tool_use_id, questions (AskUserQuestion input verbatim), terminal_only?}`;
  on the host `approval.request` event and on the conversation WS. Decide `POST /api/team/approvals/{id}/decide`:
  answer `{decision:"approve", hook:{answers:{<question text>: <answer>}}}` (multi-select comma-joined, "Other" =
  free text); chat reply `{decision:"deny", hook:{message}}` (1–4000 runes, needs `team.ask_chat.v1`); a
  `terminal_only` row → 409.

SPA (`spa/src`):
- `SessionPaneContent.tsx:97-107` renders `<TerminalView key={pane.id} …>`; the xterm lives in `useTerminal` (created on
  mount) — unmounting it drops the terminal and its WS. Inactive tabs stay mounted (`TabContent.tsx:43-69`,
  `visibility:hidden` + `inert`); `tmux-session` is a heavy pane (`lib/pane-weight.ts`) so state must live outside React.
- `PaneModeButtons.tsx` (`terminal | room | chat`; on a tmux pane room / chat open the handoff dialog via
  `useHandoffGate` + `useHandoffDialogStore`; on an execution pane terminal = take-to-terminal).
- No terminal registry (only `useTerminal.ts:56` creates xterms). No SPA client of `/api/conversations`.
- `sendKeys` exists only in `lib/rebuild/transport.ts:130-142` (pinned transport, `{keys: cmd+'\n', …}`).
- `lib/team/approval-ws.ts:77-78` **drops** `hook_ask` / `hook_permission` at the host WS boundary; `DecideRequest`
  has no `hook` field; `AskUserQuestion.tsx` handles `questions[0]` only.
- Reusable: `components/room/ToolDiffView.tsx`, `FoldContext` (`fold-context.tsx`), `useTranscriptScroll`
  (`{paneId, view}`), `lib/nex/transcript-scroll-memory.ts`, `lib/nex/worker-draft-memory.ts`. Not reusable as is:
  `OperationBlock`, `TranscriptSearch` (Nex `StreamMessage` shapes).
- Capability selectors: `useNexHostStore` (`selectConversationsScope` pattern, `ensure(hostId)` fetches `/api/info`).

iOS reference (purdex-ios main): `Chat/SendQueue.swift` (`SendPlan`: strip control chars but `\n`, tabs → 4 spaces, trim
blank head / tail lines and trailing spaces, reject empty, > 4000 UTF-8 bytes, a leading `!` or `/`; append a space
after a trailing `@token`; multi-line as a literal LF in the body — **no bracketed paste: Claude Code wraps pasted text
in `<pasted_content>`, measured 2026-10-07**; a 3 s undo buffer; one serial chain, a failure returns the later ones to
the input; ESC only while running and not twice). `Deck/DeckGrouping.swift` + `Deck/DialogueRows.swift` (the turn row
rules, D9). Fixtures: `testdata/conversation/v1/` (MANIFEST + sha256; iOS pins a commit).

Collie 1.17.2 (`~/Library/Caches/purdex-research/collie-1.17.2/`): parser `bridge/journal/claude.ts`,
`bridge/journal/tool-call.ts`, `bridge/journal/text.ts`; cards `web/src/components/chat-cards.tsx`.

## 1. Decisions (technical, 88)

- **D1 View state** — `TmuxSessionContent.view?: 'terminal' | 'deck' | 'chat'` (absent = terminal), written with a
  guarded `setPaneContent` like `lib/nex/view-mode.ts`; persisted with the tab, so per tab / per pane and surviving a
  reload. Check every equality / rewrite of `TmuxSessionContent` (rebuild, terminated) keeps the field.
- **D2 Swap without unmounting** — `SessionPaneContent` keeps `<TerminalView>` mounted in a relative wrapper; when the
  view is not terminal the terminal wrapper gets `visibility:hidden` (not `display:none`: the fit observer skips
  zero-size boxes) and `inert`, `visible={isActive && view === 'terminal'}`; the deck / chat render as an absolute
  sibling. Focus: switching to deck / chat focuses its input; back to terminal focuses the terminal.
- **D3 Conversation of a pane** — provenance (`found`, `session_id`, `agent_type === 'cc'`); re-read on the agent
  events' session change. No session id → the unreadable state (D11).
- **D4 Client and store** — `lib/conversations/` (types mirroring U1 §8.1 + `index`; REST; WS with a ticket;
  reconnect with the WS cursor; `reset`; `seq` gap → reconnect) and `stores/useConversationStore.ts` keyed by
  `${hostId}\0${sessionId}`: turns by index, items upserted by id and ordered by `index`, the `omitted_items` rule,
  `has_more_before` + `loadBefore()`, `around` for jumps, subagent children loaded on demand. One WS per conversation
  shown, ref-counted by the panes showing it; closed 30 s after the last pane leaves.
- **D5 Rendering** — new light components under `components/deck/` (one per item kind, Collie's look, spec §4);
  reuse `ToolDiffView` through an adapter from `diff{path, added, removed, hunks[]}`, `FoldContext` for 「顯示全部」 /
  outputs, `useTranscriptScroll` with a new `'deck'` and `'chat'` view key. No `OperationBlock` / `TranscriptSearch`.
- **D6 Streaming** — the API is transcript-only today: agent text appears per message. The renderer honours
  `streaming: true` (cursor ▍) so U1-5 lights it up without a client change. (Spec §4 amended.)
- **D7 Send** — `lib/conversations/send.ts`: iOS `SendPlan` rules exactly (fact list above; no bracketed paste — spec
  §7 amended), then **verify before Enter**: send the body, read the pane's screen through a new
  `lib/terminal-registry.ts` (registered by `TerminalView` with its pane id; `readVisibleLines(paneId)`), poll every
  150 ms up to 2 s for the body's last non-empty line (whitespace-normalised, a trailing ≤ 40-char slice for long
  lines); seen → send `"\r"`; not seen → no Enter, keep the draft, message 「沒有確認到終端機收到文字，未送出」.
  The pane being in the deck / chat keeps its xterm alive (D2), so the screen is readable. Destructive guard: a draft
  with a line matching `rm -rf`, `git push --force|-f`, `git reset --hard`, `DROP TABLE`, `mkfs`, `dd if=` → a second
  press within 5 s (「真的要送出？」). Queue: iOS `SendQueue` (3 s undo, serial chain, local echo matched to the
  transcript's user item within 30 s, 「你 · 排隊中」 while the agent is running). Interrupt: ESC only while the
  header status is `running`, never twice in a row.
- **D8 Questions** — the dock reads the **conversation WS approvals** (not the host store, which drops hook kinds — keep
  that drop, so the app-wide dialog never shows them). `decideApproval` gains `hook?: {answers?: Record<string,string>,
  message?: string}`. A card is bound to its approval id: `approval op: closed` or a snapshot without it → the card
  locks (「題目已變更」) and closes after 1.5 s; if the question step then has `question.answers` (U3-0), the card reads
  「已在終端機回答：X」 for 3 s. `terminal_only` → read-only + 「開終端機」. 「改成跟 agent 聊聊」 only with
  `team.ask_chat.v1`. `AskUserQuestion.tsx` grows to several questions (one answer per question).
- **D9 Turn row** — iOS rules exactly: kind → 指令 (execute) 編輯 (edit) 讀取 (read) 搜尋 (search) 網頁 (fetch)
  子 agent (task) 其他 (else); 「N 個<類別>」 joined by 「、」, zero counts left out, every step counts; prefix
  「處理了 <span>」 where span = max(started_at + duration_ms) − min(started_at) over the run's steps, rounded to
  seconds, rendered 「N 秒」 / 「N 分」 / 「N 分 M 秒」 / 「N 時」 / 「N 時 M 分」; then 「· N 失敗」 (failed),
  「· N 已拒絕」 (denied, denial ≠ interrupted), 「· N 已中斷」 (denied, interrupted), each only when > 0. A chat
  "run" = consecutive steps (thinking does not break it; user / agent_text / system do). Parity: iOS generates
  `testdata/conversation/v1/render/turn-rows.json` (per golden case: runs → expected row text) from its code; the
  Mac test reads it from the repo.
- **D10 Right panel** — inside the pane, right side, width 42 % (min 320 px, max 640 px), Esc / ✕ closes; content =
  a turn (chat row click), a full output / diff (deck 「顯示全部」), or a subagent's steps; state per pane in a memory
  module (`lib/conversations/panel-memory.ts`, tab-hosted rule).
- **D11 Unreadable** — reasons: provenance `found:false` 「找不到這個分頁的 Claude Code 對話」; `not_found`
  「對話紀錄還沒出現」; `provider_unsupported` 「這個 agent 不支援」; a first turn with no items 「還沒有內容」;
  WS / fetch failure 「連不上主機」 (with retry). Always 「切到終端機」. Never auto-switch.
- **D12 U3-0 (daemon)** — additive only (U1 §8.1 rule): `step.question{questions[{question, header?, multiple,
  options[{label, description?}]}], answers?[[]string]}` for AskUserQuestion (answers from `toolUseResult.answers`,
  multi-select matched to labels, free text kept whole; `dismissed the question` → `denied` / `user-rejected`);
  `step.read{offset, limit}`; `diff.created`; `step.search{where}`; `system compacted.detail.summary` (capped like
  user text). Fixtures regenerated (`go test ./internal/convmodel/ccnorm -update`), new cases for question and
  read-range; U1 spec §8.1 gets the fields; iOS re-pins.
- **D13 U3-5** — prototype first (a real SPA page with fixtures, shown to the user); usage keeps today's statusline
  source (agent store) since U1-7 is not wired.

## 2. Phases and tasks

### U3-0 daemon — align the breakdown with Collie (~450 lines)
1. `internal/convmodel/item.go`: `StepQuestion`, `StepRead`, `StepSearch`, `Diff.Created`; JSON tags; `Validate`.
   Tests: round-trip, omitted when empty.
2. `internal/convmodel/ccnorm`: AskUserQuestion → `question` (+ answers, multi-select matching, free text, dismissed →
   user-rejected); Read offset / limit; Write creating a file → `created`; Grep / Glob `where`; `compacted` summary.
   Tests per rule, Collie's examples as cases.
3. Fixtures: new `cc-transcript/ask-question` and `read-range` cases (scrubbed, `fixtureguard` passes), `-update`,
   `facts.json`; U1 spec §8.1 additive paragraph; README note for iOS re-pin.
Review focus: an AskUserQuestion with no result (still running), with "Other" text equal to an option label, a
multi-select answer whose label contains a comma.

### U3-1a SPA — view buttons, handoff control, the swap (~400)
1. `types/tab.ts` `TmuxSessionContent.view`; `lib/session-view.ts` (`viewOf`, `setSessionView`); keep it across
   rebuild / terminated rewrites. Tests.
2. `PaneModeButtons.tsx`: tmux pane → 終端機／指揮台／聊天 view-only (deck / chat disabled with a reason without
   `conversations.v1` or for a non-cc agent); a separate handoff button (arrow icon, 「交給執行體」, the old gate and
   dialog); execution pane unchanged + its take-back as that separate control (「拿回終端機」). `useNexHostStore`
   `selectConversationsV1`. Tests incl. the old handoff path.
3. `SessionPaneContent.tsx`: D2 swap with a placeholder deck / chat; focus rules; real `TabContent` switch test
   (view kept, terminal not re-created — assert the xterm instance is the same object).

### U3-1b SPA — the conversation client and store (~600)
1. `lib/conversations/types.ts`, `api.ts` (snapshot, before, around, subagent; error codes), tests with fixtures from
   `testdata/conversation/v1` expected files.
2. `lib/conversations/ws.ts` (ticket, frames, seq gap, reset, reconnect with backoff) + `stores/useConversationStore.ts`
   (D4) + provenance resolution (D3). Tests: upsert / index order, omitted rule, reset keeps place by id, ref-count
   close after 30 s, session change re-resolves.

### U3-1c SPA — the deck (~700, split a/b if needed)
1. `components/deck/` item components (spec §4 table; status slot; `diff` adapter to `ToolDiffView`; output fold to
   the last 10 lines; 「顯示全部」 → right panel placeholder until U3-3), `DeckView` with paging (load older on top
   reach), scroll memory (`'deck'`), the unreadable state (D11). Tests per kind with golden expected items;
   screenshot gate (zh-TW, dark) of a real-session turn with every kind next to Collie's card shapes.

### U3-2 SPA — input (~600)
1. `lib/terminal-registry.ts` + `TerminalView` registration (`paneId` prop); `readVisibleLines`. Tests.
2. `lib/conversations/send.ts` (SendPlan rules, verify-before-Enter, destructive guard, 409 → 「終端機已重建，請重送」)
   and `SendQueue` port (`stores` or module) + `components/deck/SessionInput.tsx` (Enter / Shift+Enter, 中斷, draft
   memory per pane, `/` `!` block message). Tests: each SendPlan rule, verify seen / not seen, 409, queue order and
   undo, ESC rules, draft survives a real tab switch.
Review focus: the text appears in the terminal but wrapped (long line) — the slice match still finds it; a draft
that is only whitespace; two quick sends while the first is verifying.

### U3-3 SPA — chat and the right panel (~700)
1. `lib/conversations/turn-row.ts` (D9) tested against `render/turn-rows.json` (iOS produces it first; until it lands
   the test reads a local copy generated from iOS's rules and is switched to the repo file in the same PR).
2. `components/deck/ChatView.tsx` (bubbles, header with the live status line, one progress message in place, the
   file chip from edit steps), `SessionRightPanel.tsx` (D10) used by chat rows, deck 「顯示全部」 and subagents.
   Tests + screenshot gate.

### U3-4 SPA — the dock (~600)
1. `lib/conversations/asks.ts` (approvals from the conversation WS; bound cards; decide with `hook`), `api` extension
   of `decideApproval`. Tests: snapshot replace, closed → lock → close, answered in the terminal text, terminal_only,
   ask-chat gating and validation (mirror the daemon's 1–4000 runes / no bidi rules).
2. `components/deck/QuestionDock.tsx` + multi-question `AskUserQuestion`; the input's placeholder while a card is open;
   the terminal view's 「● 等你回答」 strip; per-card 「終端機」 button. Tests + screenshot gate.

### U3-5 SPA — the status row (prototype first)
1. Prototype page (fixtures, real components) → user decision → its own short plan addendum.

## 3. Order and owners

U3-0 (daemon) and U3-1a … U3-1c can run in parallel. Then U3-2 → U3-3 → U3-4 → U3-5. iOS: `render/turn-rows.json`
before U3-3, and re-pin after U3-0. Owner: a U3 member (Sonnet) under 88 — needs the 介面線 cap raised to 4 (user), or
U3 waits for the solo seat after WA-2b-2.

## 4. Review focus (whole plan)

1. A conversation that is `/clear`ed while the deck shows it (session change → re-resolve, old one `live:false`).
2. A very long conversation (hundreds of turns): paging and memory stay bounded; the WS `turns` window does not grow.
3. The host restarts mid-view (WS closes, reconnect with cursor, `reset`).
4. Sending while the terminal is in copy mode / a dialog (the text never appears → no Enter, draft kept).
5. Two tabs showing the same session in different views (one WS, two views, independent scroll / draft).
