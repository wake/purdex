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
  mount) — unmounting it drops the terminal and its WS. `tmux-session` is a **heavy** pane (`lib/pane-weight.ts:10`):
  with `keepAliveCount: 0` only the active heavy tab stays mounted (`hooks/useTabAlivePool.ts:61`), so a tab switch
  unmounts and remounts it — every view state must live outside React (CLAUDE.md tab-hosted rule).
- `PaneModeButtons.tsx` (`terminal | room | chat`; on a tmux pane room / chat open the handoff dialog via
  `useHandoffGate` + `useHandoffDialogStore`; on an execution pane terminal = take-to-terminal).
- No terminal registry (only `useTerminal.ts:56` creates xterms). No SPA client of `/api/conversations`.
- `sendKeys` exists only in `lib/rebuild/transport.ts:130-142` (pinned transport, `{keys: cmd+'\n', …}`). The daemon's
  send-keys checks only the tmux generation (`internal/module/session/handler.go:313,335`) — not the pane's owner,
  its Claude Code session or its UI state.
- `lib/team/approval-ws.ts:77-78` **drops** `hook_ask` / `hook_permission` at the host WS boundary. The daemon decide
  already takes `hook` (`internal/team/wire.go:136` `Hook *HookDecision`; `ask_handler.go:435` accepts approve +
  `hook.answers` and deny + `hook.message`) — only the SPA `DecideRequest` type lacks it (`spa/src/lib/team/types.ts:180`); `AskUserQuestion.tsx` handles `questions[0]` only.
- Reusable: `components/room/ToolDiffView.tsx`, `FoldContext` (`fold-context.tsx` — its expanded set is component `useState`, `:42,54`, so it does not survive a
  remount), `useTranscriptScroll`
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

- **D1 View state** — **device-local**, not in pane content (pane content travels with profile sync,
  `lib/profile/sections.ts:184-189`): `stores/useSessionViewStore.ts`, zustand `persist` (localStorage), key
  `${tabId}\0${paneId}`, value `{view: 'terminal'|'deck'|'chat', sessionCode}` (the binding: a pane rebound to another
  session starts at terminal); pruned when the tab / pane closes (the tab store's close path). Absent = terminal.
- **D2 Swap without unmounting** — `SessionPaneContent` keeps `<TerminalView>` mounted in a relative wrapper; when the
  view is not terminal the terminal wrapper gets `visibility:hidden` (not `display:none`: the fit observer skips
  zero-size boxes) and `inert`, `visible={isActive && view === 'terminal'}`; the deck / chat render as an absolute
  sibling. Focus: switching to deck / chat focuses its input; back to terminal focuses the terminal.
- **D3 Conversation of a pane** — provenance (`found`, `session_id`, `agent_type === 'cc'`); re-read on the agent
  events' session change. No session id → the unreadable state (D11).
- **D4 Client and store** — `lib/conversations/` (types mirroring U1 §8.1 + `index`; REST; WS with a **fresh
  one-time ticket on every (re)connect**; `seq` restarts at 1 per connection and a gap → reconnect; the cursor moves
  only on `conversation.*` frames, never on paging answers; `reset` replaces) and `stores/useConversationStore.ts` keyed
  by `${hostId}\0${sessionId}`: turns by index, items upserted by id and ordered by `index`, the `omitted_items` rule,
  `has_more_before` + `loadBefore()`, `around` for jumps, subagent children loaded on demand. **Subscription owner:
  every mounted `tmux-session` pane whose agent is Claude Code holds one, in all three views** (the terminal view needs
  the approvals for its 「● 等你回答」 strip); one WS per conversation, ref-counted by those panes, closed 30 s after
  the last one unmounts.
- **D5 Rendering** — new light components under `components/deck/` (one per item kind, Collie's look, spec §4);
  reuse `ToolDiffView` through an adapter from `diff{path, added, removed, hunks[]}`; expansion (「顯示全部」,
  outputs, thinking) in a **pane-keyed external fold memory** (`lib/conversations/fold-memory.ts`, a module map like
  `transcript-scroll-memory.ts`; `FoldContext` gets an optional external backing store rather than its `useState`);
  `useTranscriptScroll` with new `'deck'` and `'chat'` view keys. No `OperationBlock` / `TranscriptSearch`.
- **D6 Streaming** — the API is transcript-only today: agent text appears per message. The renderer honours
  `streaming: true` (cursor ▍) so U1-5 lights it up without a client change. (Spec §4 amended.)
- **D7 Send** — **one daemon route does the checking and the typing (U3-0b)**; the App never decides "the text
  arrived" from what it sees (text anywhere on the screen proves nothing — an agent's output can contain it; plan
  review 2026-10-10). `POST /api/sessions/{code}/submit {text, expected_tmux_instance, expected_session_id}`, under a
  per-session mutex:
  1. **validate** `text` with iOS `SendPlan` rules (control chars but `\n` stripped, tabs → 4 spaces, blank head / tail
     lines and trailing spaces trimmed, 1 … 4000 UTF-8 bytes; a leading `!` or `/` → 400 `needs_terminal`, U3 has no
     mod path);
  2. **pre-check** — all must hold, else nothing is typed: the tmux instance; the pane's owner in the agent registry is
     a live Claude Code process whose session id is `expected_session_id` (process identity pid + start); the pane's
     UI state equals the measured prompt state (`#{pane_in_mode}` = 0, `#{alternate_on}` as measured for Claude Code
     at its prompt — U3-0b task 1 measures it before coding); **the agent is idle** — its status (the owner's
     `Status`, `internal/module/agent/pane_owner.go`) is `idle` and no `hook_ask` / `hook_permission` approval is open
     for it (`OpenApprovals()`); while the agent is running the App keeps the message in its own queue
     (「你 · 排隊中」) and submits when the status turns idle — the daemon never types into a busy Claude Code; **the input box is recognised and empty** — the rows from the box's top rule down to the cursor
     match the measured empty-prompt layout (prompt glyph, nothing typed), else `input_not_empty`
     (「終端機輸入框裡已經有文字，請先在終端機清掉或送出」 — this also stops a retry from appending to text a failed
     attempt left; the App never retypes into a non-empty box);
  3. **type** the text (no Enter; new lines as a literal LF; no bracketed paste);
  4. **verify the box holds exactly the text**: capture the box rows (top rule → cursor row, ≤ 12 rows, the measured
     layout) and wait ≤ 2 s (every 100 ms) until their content — prompt glyph and padding removed, wrapped rows joined —
     equals the whitespace-normalised text, or, when the text is longer than the box shows, the box holds nothing but a
     contiguous tail of the text of at least min(len, 200) characters;
  5. **re-check** step 2 (owner, session, idle, no open approval), then send Enter as **one guarded tmux command**
     within **50 ms** of that re-check (else abort with `not_seen`) — `if-shell -F` (the pattern of
     `internal/tmux/send_keys_conditional.go`) on the tmux instance, `#{pane_in_mode}`, `#{alternate_on}` and
     `#{pane_current_command}` equal to the measured Claude Code foreground command, with `send-keys Enter` as its only
     branch. Why this closes the races codex named: Claude Code leaving the foreground (exit, crash, a shell) fails the
     tmux condition atomically; a dialog needs a running turn and a model round-trip (hundreds of ms at least) after an
     idle re-check, so it cannot appear within the 50 ms; a session change needs `/clear` or a relay typed or triggered
     in that pane. **The residual, accepted and written in the PR: a person (or another client of this daemon) acting
     on the same pane within those 50 ms.** U3-0b task 1 also measures the re-check → Enter time (p99 must be well
     under 50 ms on mlab).

  Any check failing → no Enter, `409 {reason: instance | owner_changed | session_mismatch | in_mode | alternate_screen |
  busy | dialog_open | input_not_empty | not_seen}` (JSON); when the text was already typed the App says so
  (「文字已在終端機輸入框，沒有按 Enter：<原因>」). Capability `sessions.submit.v1` in `/api/info`; iOS may adopt it
  later. The App side, `lib/conversations/send.ts`: the destructive guard (a line matching `rm -rf`, a forced push, a
  hard reset, `DROP TABLE`, `mkfs`, `dd if=` → a second press within 5 s, 「真的要送出？」), iOS's `SendQueue` (3 s
  undo, one serial chain; **a message waits in this queue while the agent is running** and is submitted when the
  header status turns idle — 「你 · 排隊中」 until then; local echo matched to the transcript's user item within 30 s), interrupt = ESC through the existing send-keys with the instance, only while the header status is
  `running`, never twice in a row.
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
  "run" = consecutive steps (thinking does not break it; user / agent_text / system do). Spec §2.9 / §5 say the same (corrected:
  the decision was 一串工作一列, per chain, not per turn). Parity: iOS generates
  `testdata/conversation/v1/render/turn-rows.json` (per golden case: runs → expected row text) from its own code and
  tests it in purdex-ios; **that committed file is a gate for U3-3** — the Mac test reads only it, never a local
  replacement.
- **D10 Right panel** — inside the pane, right side, width 42 % (min 320 px, max 640 px), Esc / ✕ closes; content =
  a chain's steps (a chat work-row click — the chain, not the whole turn; the header names the turn and the chain's
  position), a full output / diff (deck 「顯示全部」), or a subagent's steps; state per pane in a memory
  module (`lib/conversations/panel-memory.ts`, tab-hosted rule).
- **D11 Unreadable** — reasons: provenance `found:false` 「找不到這個分頁的 Claude Code 對話」; `not_found`
  「對話紀錄還沒出現」; `provider_unsupported` 「這個 agent 不支援」; a first turn with no items 「還沒有內容」;
  WS / fetch failure 「連不上主機」 (with retry). Always 「切到終端機」. Never auto-switch.
- **D12 U3-0 (daemon)** — additive only (U1 §8.1 rule): `step.question{questions[{question, header?, multiple,
  options[{label, description?}]}], answers?[[]string]}` for AskUserQuestion (answers read from
  `toolUseResult.answers` keyed by question text, multi-select matched to labels, free text kept whole, scrubbed like
  user text; `dismissed the question` → `denied` / `user-rejected`); `step.read{offset, limit}`; `diff.created`;
  `step.search{where}`; `system compacted.detail.summary` (capped like user text); **each kind's `summary` checked
  against Collie's `summarizeToolInput` in a side-by-side table test** (differences adopted unless they lose
  information; the table is the record). Collie's `delete` / `move` are dropped (tool names Claude Code lacks — spec §6
  amended); images stay as they are (the apps draw a notice). Fixtures regenerated (`go test ./internal/convmodel/ccnorm
  -update`), new cases `ask-question` and `read-range`; U1 spec §8.1 gets the fields; iOS re-pins.
- **D13 U3-5** — prototype first (a real SPA page with fixtures, shown to the user); usage keeps today's statusline
  source (agent store) since U1-7 is not wired.

## 2. Phases and tasks

### U3-0 daemon — align the breakdown with Collie (~450 lines)
1. `internal/convmodel/item.go`: `StepQuestion`, `StepRead`, `StepSearch`, `Diff.Created`; JSON tags; `Validate`.
   Tests: round-trip, omitted when empty.
2. `internal/convmodel/ccnorm`: AskUserQuestion → `question` (+ answers, multi-select matching, free text, dismissed →
   user-rejected); Read offset / limit; Write creating a file → `created`; Grep / Glob `where`; `compacted` summary;
   the summary side-by-side table test (D12). Tests per rule, Collie's examples as cases.
3. Fixtures: new `cc-transcript/ask-question` and `read-range` cases (scrubbed, `fixtureguard` passes), `-update`,
   `facts.json`; U1 spec §8.1 additive paragraph; README note for iOS re-pin.
Review focus: an AskUserQuestion with no result (still running), with "Other" text equal to an option label, a
multi-select answer whose label contains a comma.

### U3-0b daemon — the submit route (~400 lines)
1. **Measure first** (write the numbers into the PR and this plan's §0): on mlab, a live Claude Code 2.1.29x pane at its
   prompt, in a dialog (AskUserQuestion, a permission prompt), in `/model`, and after exiting to the shell — record
   `#{pane_in_mode}`, `#{alternate_on}`, `#{cursor_y}`, `#{pane_current_command}`, and the capture of the rows around
   the cursor (the input box's rules / prompt glyph); and the time from an idle re-check to the guarded Enter. The verify region and the UI-state check are built from these numbers. Use a throwaway
   session on an isolated tmux socket (`-L <label>`, `unset TMUX`), never the user's sessions.
2. `internal/module/session`: `POST /api/sessions/{code}/submit` (D7) with the per-session mutex, the owner / session
   check through the agent registry, the UI-state check, typing, the input-region verify, the re-check, JSON errors;
   `sessions.submit.v1` in `/api/info`; not in the phones' `deviceAllowed` for now. Tests with a fake tmux: each check
   failing before typing and before Enter; hostile output above the input box containing the text → `not_seen`;
   a permission / question dialog open → `dialog_open`, nothing typed; a pre-filled box → `input_not_empty`, nothing
   typed; a running agent → nothing typed (`busy`, the App queues); the re-check → Enter deadline exceeded → no Enter;
   Claude Code exiting to the shell between typing and Enter → the guarded Enter does not fire (the
   `pane_current_command` condition); wrapped
   multi-line text and a text longer than the box found; concurrent submits serialised; a retry after `not_seen` hits
   `input_not_empty` (no duplicate text).
Review focus: the owner changing between the re-check and Enter (keep that window to one tmux call); a text whose last
line is also the prompt's placeholder; CJK width in the captured rows.

### U3-1a SPA — view buttons, handoff control, the swap (~400)
1. `stores/useSessionViewStore.ts` (D1: device-local persisted, key tab + pane, binding = session code, pruned on
   close; never in pane content, so profile sync never carries it). Tests: persist / restore, rebind → terminal, prune.
2. `PaneModeButtons.tsx`: tmux pane → 終端機／指揮台／聊天 view-only (deck / chat disabled with a reason without
   `conversations.v1` or for a non-cc agent); a separate handoff button (arrow icon, 「交給執行體」, the old gate and
   dialog); execution pane unchanged + its take-back as that separate control (「拿回終端機」). `useNexHostStore`
   `selectConversationsV1`. Tests incl. the old handoff path.
3. `SessionPaneContent.tsx`: D2 swap with a placeholder deck / chat; focus rules. Tests: switching views **within the
   pane** keeps the same xterm instance (no reconnect); a real `TabContent` switch away and back (the heavy tab
   unmounts and remounts) restores the view from `useSessionViewStore`.

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

### U3-2 SPA — input (~500; needs U3-0b for real use, tests mock the route)
1. `lib/conversations/send.ts` (calls `/submit`; maps each 409 reason to its message; the destructive guard; the
   `SendQueue` port; interrupt) + `components/deck/SessionInput.tsx` (Enter / Shift+Enter, 中斷, draft memory per pane,
   the `/` `!` message, disabled with a reason without `sessions.submit.v1`). Tests: each 409 reason, queue order and
   undo, ESC rules, destructive guard, draft survives a real `TabContent` remount.
Review focus: a draft that is only whitespace; two quick sends while the first is in flight; the host restarting
mid-submit.

### U3-3 SPA — chat and the right panel (~700)
1. **Gate: iOS has committed `testdata/conversation/v1/render/turn-rows.json`** (generated and tested in purdex-ios).
   `lib/conversations/turn-row.ts` (D9) tested against that file only.
2. `components/deck/ChatView.tsx` (bubbles, header with the live status line, one progress message in place, the
   file chip from edit steps), `SessionRightPanel.tsx` (D10) used by chat rows, deck 「顯示全部」 and subagents.
   Tests + screenshot gate.

### U3-4 SPA — the dock (~600)
1. `lib/conversations/asks.ts` (approvals from the conversation WS; bound cards; decide with `hook`), `api` extension
   of `decideApproval`. Tests: snapshot replace, closed → lock → close, answered in the terminal text, terminal_only,
   ask-chat gating and validation (mirror the daemon's 1–4000 runes / no bidi rules).
2. `components/deck/QuestionDock.tsx` + multi-question `AskUserQuestion`; the input's placeholder while a card is open;
   the terminal view's 「● 等你回答」 strip (the pane's subscription, D4); per-card 「終端機」 button. Tests: opened /
   closed / snapshot replacement seen from the terminal view; + screenshot gate.
3. **The tab-hosted integration test** (CLAUDE.md): one real `TabContent` test that switches away and back (heavy-tab
   unmount / remount) in the middle of each state — deck scrolled and outputs expanded, a draft, the right panel open,
   chat, an open dock card — and finds each restored.

### U3-5 SPA — the status row (prototype first)
1. Prototype page (fixtures, real components) → user decision → its own short plan addendum.

## 3. Order and owners

Daemon U3-0 → U3-0b and SPA U3-1a … U3-1c run in parallel. Then U3-2 (U3-0b deployed for real use) → U3-3 (gate:
iOS `render/turn-rows.json`) → U3-4 → U3-5. iOS: the fixture before U3-3, the re-pin after U3-0, `/submit` adoption
optional. Owner: a U3 member (Sonnet) under 88 — needs the 介面線 cap raised to 4 (user), or
U3 waits for the solo seat after WA-2b-2.

## 4. Review focus (whole plan)

1. A conversation that is `/clear`ed while the deck shows it (session change → re-resolve, old one `live:false`).
2. A very long conversation (hundreds of turns): paging and memory stay bounded; the WS `turns` window does not grow.
3. The host restarts mid-view (WS closes, reconnect with cursor, `reset`).
4. Sending while the terminal is in copy mode / a dialog (the text never appears → no Enter, draft kept).
5. Two tabs showing the same session in different views (one WS, two views, independent scroll / draft).

## 5. Review map (codex plan review `task-mv1gmhpu-9o374w`, 9 findings)

1 (critical, App-side screen check spoofable) → D7 / U3-0b: the daemon submit route, input-region verify, owner /
session / UI-state checks before typing and before Enter, fail closed. 2 (view in synced pane content) → D1
device-local store. 3 (heavy tabs unmount) → §0 fact and U3-1a tests. 4 (FoldContext local state) → D5 external fold
memory. 5 (per turn vs per run) → spec corrected to the user's 一串工作一列 (the plan was right). 6 (parity fixture
replaceable) → D9 / U3-3 gate. 7 (U3-0 gaps) → D12 summary table, question answers; delete / move dropped and images
left to the apps (spec §6 amended with the evidence). 8 (terminal view without a subscription) → D4 owner = every CC
pane in all views, ticket per reconnect, cursor only from conversation frames; U3-4 tests. 9 (stale DecideRequest fact)
→ §0 corrected; D8 extends only the SPA type. Plus the tab-hosted integration test (U3-4 task 3).

Round 2 (incremental re-review `task-mv1gvzr8-oh74e8`): 7 of 9 resolved; new — (critical) no "safe to submit" state →
D7 step 2 adds no-open-dialog (hook approvals + agent status) and an empty, recognised input box before typing, step 4
an exact box-content match; (critical) re-check → Enter not atomic → Enter is one `if-shell -F` tmux command guarded by
mode, alternate screen and owner pid; the remaining window (a person typing into the same pane) is accepted and
documented; (important) spec §10 still open → closed; (important) retry duplication → `input_not_empty`; (5, partly)
chat row click scope → the chain (spec §5, D10).

Round 3 (`task-mv1h112l-igerxq`): spec §10, retry duplication and the chat row scope resolved; still critical — the
Enter guard checked the pane's pid (the shell), not Claude Code, and owner / session / dialog could change after the
re-check. → D7: submit only while the agent is **idle** (the App queues while it runs), the guarded Enter checks
`#{pane_current_command}` (Claude Code still in front) with the instance / mode / alternate screen, and the Enter must
follow the re-check within 50 ms (measured). A dialog needs a model round-trip after idle and a session change needs a
`/clear` or relay in that pane, so neither fits the window; the residual is a person or another client acting on that
pane within the 50 ms, written in the PR.
