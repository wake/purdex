# Message stages: never lose a message, show where it is, send now and take back (spec)

> B line, 2026-10-16. iOS first (step 1), then daemon + Mac + iOS (step 2). Builds on the send queue of the K line
> (`docs/specs/2026-10-07-chat-send.md` on iOS, Mac K-2), the daemon's `POST /api/sessions/{code}/submit` and `/recall`
> (`internal/module/conversation/typed_submit.go`, `typed_recall.go`, `internal/promptq`).

## 1. The problem (user, 10-16)

- A message sent from the iPhone's Deck stayed 「待送出」 for good; the desktop never got it, and after the App was quit
  and opened again it was gone — not in the conversation, not in the input box: the message was lost.
- While it was stuck, its text could not be selected or copied, so it could not be sent again by hand.
- Nothing on screen says why: still on the phone, at the daemon, or in Claude Code's queue.
- When a message changes state (local → Claude Code's queue → the conversation) two copies show for a moment, or it
  flickers. It must always be one bubble.

Why it can happen today (iOS `App/Sources/Chat/SendQueue.swift`, `Submit.swift`, `Composer.swift`): the queue lives in
memory only; a send that ends `unknown` (network lost, the App suspended mid-request) is kept as 「待送出」 until a new
turn starts or the transcript matches, which never happens when it was not delivered; quitting the App drops it. Three
sources draw the same message (the local entry, the header's queued row, the transcript item) and hand over by text
matching, so a hand-over can show two or none.

## 2. User decisions (10-16, do not reopen)

| # | Decision |
|---|---|
| D1 | Each pending message shows where it is, bottom-right of its bubble: 在 iPhone → 已到主機 → 在佇列 → (gone: Claude read it, it is a normal message); or 送不出 with the reason. |
| D2 | One identity per message (`client_msg_id`, already sent with `/submit`) from the tap to the transcript: one bubble that changes its mark in place, never two, never a gap. |
| D3 | A message not confirmed delivered survives an App restart: it comes back as 「上次沒確認送出」 with 重送 / 放回輸入框 / 複製 / 丟棄. Nothing is dropped silently. |
| D4 | Tap a pending bubble (after its 1.5 s buffer) → its detail: the stages with times, the reason when stuck, and 複製 / 重送 / 放回輸入框. During the buffer a tap still cancels (as today). |
| D5 | **Send now** (iOS): hold the bubble of a message in Claude Code's queue; after 0.5 s its colour starts to fill, full in 1 s more; releasing at ≥ 90 % sends now, earlier does nothing; a haptic at the start and when it is armed. Timings tuned by feel later. |
| D6 | **Take back** (iOS): double-tap a queued bubble → it changes colour (armed); double-tap again within 3 s → the queued messages come back into the input box. No count prompt (Claude Code takes back the whole queue at once; that is accepted). |
| D7 | Send now when the desktop input box has text → refused (「桌機輸入框有未送出的文字」): Claude Code's send-now key also sends the draft. |
| D8 | Mac: the same, by the terminal's keys, not by mouse: Ctrl+Enter or Ctrl+X Ctrl+S in the Deck's input = send now; Up on the first line of the Deck's input = take back the queue into it. |
| D9 | Two steps: step 1 iOS only (never lose, show why, detail and copy); step 2 the full stages, send now and take back (daemon + Mac + iOS). |

Claude Code facts (official docs, 2.1.301 on mlab): `chat:sendNow` = Ctrl+Enter / Ctrl+X Ctrl+S (≥ 2.1.275) sends the
queued messages and the draft right away — background-able work moves to the background and Claude reads them in the
same turn; a turn that is only writing is interrupted and they go next (≥ 2.1.281). Take back = Up from the first line
of the input box: the whole queue comes back, one message per line, ahead of the draft. Esc interrupts the turn and
sends the queue; it does not take it back.

## 3. Step 1 — iOS only

- **S1.1 Durable pending messages.** Every entry that is not confirmed (buffered, sending, sent, partial/unknown) is
  written to the App's storage per tab and session (text, `client_msg_id`, state, times, attempts, last reason) on every
  change, and removed only when it is confirmed (matched in the transcript, claimed by the header's queued row and then
  read, or recalled) or when the person drops it. On launch, stored entries come back on their tab as 「上次沒確認送出」
  (D3). An entry that was `buffered` at quit is never sent by itself after a relaunch.
- **S1.2 The reason, always.** Each entry records each state change with its time and, when a send ends other than
  handed, the verdict and its text (unreachable, refused + code, busy after retries, unknown + why). An entry that is
  still 「在 iPhone」 after 10 s (buffered or sending) shows why in its mark (「連不上主機」, 「重試中（第 2 次）」, …).
  No entry stays 「待送出」 with no reason: `sending` beyond its request budget (25 s × attempts + retry gaps) becomes
  「送不出（逾時）」; an `unknown` entry says 「不確定有沒有送到」.
- **S1.3 The detail (D4).** A tap on a pending bubble after the buffer opens a sheet: the text (selectable), the stages
  with times, the reason, `client_msg_id` (small, for support), and 複製 / 重送 / 放回輸入框 / 丟棄. 重送 sends it again
  with a new `client_msg_id` only after the person confirms when the old one is `unknown` (it may have arrived).
- **S1.4 Find and fix the stuck path.** Reproduce today's case (no desktop delivery, kept 「待送出」, gone after quit):
  network off mid-send, Tailscale paused, the App suspended during the buffer and during the request, the session's tmux
  instance changing, two messages in a row. Log every state change with `client_msg_id` (os.Logger, privacy public for
  ids and states). Fix the cause found, with a test per case.
- **S1.5 Marks for step 1.** With what the App knows today: 在 iPhone (buffered / sending / waiting to retry), 已交出
  (handed: the daemon answered `handed`), 送不出 (with the reason), 「上次沒確認送出」. The full 已到主機 / 在佇列 split comes in
  step 2 with the daemon's events.
- Tests: persistence round trip and relaunch (a stored `sending` entry comes back as 「上次沒確認送出」, not re-sent); each
  verdict → its mark and reason; the 10 s rule; the sheet's actions; copy puts the exact text on the pasteboard;
  mutation per rule. Swift builds and tests run on air26.
- Release: an iOS build + OTA (`~/.local/share/purdex-ios/` on air26: `build.sh`, `ota.sh <ver> <build>`), then the
  user tries it.

## 4. Step 2 — daemon + Mac + iOS (outline; its plan follows step 1)

- **Daemon**: `/submit` answers receipt at once and reports the stages per `client_msg_id` on the conversation stream
  (`received` → `filled` → `queued` in Claude Code's queue → `read` when it enters the transcript; `failed` with a reason),
  so every client draws one bubble keyed by the id (D1, D2); `POST /api/sessions/{code}/send-now` (mod required: refuse
  when the composer has text, D7; otherwise Ctrl+X Ctrl+S into the pane under the same pane and generation guards as
  `/submit`); `/recall` unchanged. Capabilities for each, with the real-module `/api/info` test.
- **iOS**: the stage marks from the events; hold to send now (D5); double-tap twice to take back (D6).
- **Mac**: the same marks; Ctrl+Enter / Ctrl+X Ctrl+S and Up-on-the-first-line in the Deck input (D8).
- Who: daemon by 53-pe (B line, `internal/module/conversation` past 1f), Mac by a subagent, iOS by the iOS member.

### 4.1 MS2-0 measurements (2026-10-19)

Claude Code 2.1.302 (`claude --model haiku`), tmux 3.6a, an isolated `tmux -L ms2` server. Transcript rows are the
`queue-operation` / `user` / `attachment` rows of the session's `.jsonl`. Without send now, a queued message is read at the next
tool boundary of the running turn (`queue-operation remove` + `attachment queued_command`) or, during plain text generation,
when the turn ends.

| # | Situation | Bytes sent | Rows added to the transcript | Behaviour | Conclusion |
|---|---|---|---|---|---|
| 0 | Baseline: two queued during a busy turn, no keys | none | `enqueue` x2, then after the next `tool_result`: `remove` x2 + two `attachment queued_command` | read at the next tool boundary, same turn | the ordinary shape |
| 1 | Two queued during text generation, send now | `18 13` | `enqueue` x2, `dequeue` x2 (no text), two `user` rows | the generation is **interrupted** (the half answer is cut; the model says the earlier text broke off); both messages are read in one new request; the screen hint "ctrl+x ctrl+s to send now" goes away with the queue | works; the shape is `dequeue` + `user` |
| 1b | Same, but a short tool finishes first | `18 13` | the baseline shape (`remove` + `queued_command`) | no visible difference: the tool boundary came first | send now differs only for long generations / tools |
| 2 | Draft in Claude Code's input + two queued | `18 13` | `enqueue` A, B, **`enqueue` draft**, `dequeue` x3, three `user` rows | the **draft is sent too** (queued, then flushed with the rest) | the daemon must refuse on a non-empty input (D7) |
| 3 | Permission dialog open + two queued | `18 13` | none | swallowed by the dialog: the dialog stays, the command does not run, the queue is untouched; on Esc the queue flushes (`dequeue` x2, interrupted-for-tool-use marker, two `user` rows) | harmless but sends nothing; refuse as `waiting` |
| 4 | Empty queue, idle, empty input | `18 13` | none | no change | no-op |
| 4b | Empty queue, busy | `18 13` | none | the turn is not interrupted | no-op |
| 5 | Ctrl+Enter as CSI-u, two queued | `1b 5b 31 33 3b 35 75` | same as 1 | tmux delivers the bytes unchanged; same effect as 1 | usable fallback |
| 6 | Lone Ctrl+S | `13` | none | not eaten as XOFF (the tty is `-ixon` while Claude Code runs; `ixon` with stop `^S` only at the shell prompt); the screen keeps updating; nothing happens | no XOFF risk; both bytes must travel in one `send-keys` |

**Chosen bytes for send now: `\x18\x13` (Ctrl+X Ctrl+S)**, Claude Code's default `chat:sendNow` binding, which its own screen hints.
Fallback: `\x1b[13;5u`.

**Mod runtime (rev 2 question).** The type declarations of 2.1.302 offer no way to trigger send now from a mod: `$.command.run` runs
slash commands only, `chat:sendNow` is only a keybinding action name, a UI Button's `action` is a chord the person presses, and
`$.prompt.submit` waits for the session to be idle. D7 therefore uses the plan's fallback: read Claude Code's input under the pane
and session locks, then send the keys with nothing in between.

**Matching shapes.** Both must be recognised: an ordinary queue read (`remove` + `attachment queued_command`) and a send now
(`dequeue` + real `user` rows).
