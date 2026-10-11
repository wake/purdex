# Deck / chat send by typing into Claude Code (Mac and iOS) — spec (draft 2026-10-11)

## 1. User decision (2026-10-11, do not reopen)
Both apps send a deck / chat message **by putting it into the Claude Code TUI and pressing Enter**, so Claude Code's own queue
handles a busy session (the message is queued and read mid-turn, as when typing in the terminal). Reason given: with the mod route a
message can be stuck in our pipeline when the daemon is interrupted; Claude Code's queue should own it, and mid-turn insertion comes
with it. This **reverses U3 plan rev 4** (which removed the keystroke route because external checks + Enter cannot be atomic with
Claude Code's state); the residual risks of §5 are accepted by the user.

## 2. Facts (measured 2026-10-11)
- iOS already types: `POST /api/sessions/{code}/send-keys {keys, expected_tmux_instance}` (body, then `\r`, 150 ms apart), with
  client-side sanitising (tabs→spaces, control chars stripped, 4000 bytes, leading `/` `!` refused, trailing `@token` padded, LF via
  Ctrl-J, no bracketed paste because Claude Code wraps pastes as `<pasted_content>`), blocked while the agent is `waiting`; no
  dialog / input-empty / text-landed checks. Daemon side: generation guard only (`SendKeysIfInstance`), no owner / mode / mutex.
- Mac sends through the mod (`POST /api/conversations/claude/{sid}/submit` → `$.prompt.submit`), which refuses mid-turn (`busy`) and
  makes the App hold the message until idle.
- The mod API offers `$.prompt.read() → {text, cursor}` (the unsent draft in the TUI input) and
  `$.prompt.fill({text, mode: replace|append|insert}) → {isFilled, refusal?: 'no_composer'|'dialog', text, cursor}`; **fill refuses
  while a dialog is open** and puts text in without going through key parsing (no `/` `!` `@` completion, no paste wrapping).
- Claude Code keys: Enter = submit (queues while busy), `ctrl+x enter` = queueSubmit, `ctrl+enter` / `ctrl+x ctrl+s` = sendNow,
  `ctrl+j` = newline; Up on an empty input pops the queued items into the input (`popAll`, written to the transcript).
- B-read (#2619) puts Claude Code's queued prompts in `header.queued`; the transcript shows the delivered prompt
  (`promptSource: "queued"` / `queued_command`).

## 3. Design — one daemon endpoint, used by both apps
`POST /api/sessions/{code}/submit {text, client_msg_id, expected_tmux_instance}` (device-allowed like send-keys). In order:
1. Per-session lock (one submit / recall / interrupt at a time per pane).
2. `client_msg_id` ledger (at-most-once): a seen id returns its recorded outcome, never types again.
3. Sanitise (iOS rules, moved to the daemon; the apps keep a copy for instant feedback).
4. Generation guard (`expected_tmux_instance`), pane exists, agent is Claude Code; the agent is not `waiting` (a question /
   permission is open → `409 waiting`).
5. **With a mod owner (normal case):** `$.prompt.read` must be empty (else `409 input_not_empty` — never mix with a draft someone is
   typing in the terminal); `$.prompt.fill({text, mode:'replace'})` must return `isFilled` with `text` equal to ours (`refusal:
   dialog` → `409 dialog_open`); a second zero-length check (`read` still equals ours) immediately before Enter; then Enter through
   tmux (`send-keys Enter` under the generation `if-shell` guard). **Without a mod:** fall back to today's iOS literal typing (status
   not waiting, generation guard), marked `mode: "keys"` in the answer.
6. Answer `{outcome: handed | refused(reason) | unknown, mode: fill|keys}`. After Enter the message belongs to Claude Code: a lost
   answer is `unknown` and is **never** retyped automatically.

## 4. What the deck shows
- Sent → **pending** (the previous user-block style, user 2026-10-11) until accepted. Pending sources: our in-flight request, then
  `header.queued` (Claude Code queued it while busy). **Accepted** = the user item appears in the transcript (current user-block
  style, slightly dimmer). Idle sessions go straight to the transcript.
- `unknown` / nothing seen 10 s after `handed` → 「可能已送出，請看紀錄」, no automatic retry.
- The App-side "wait for idle" queue (#2617 ledger) is removed; messages go out immediately. The iOS 3-second undo buffer stays
  (iOS only) unless decided otherwise.
- **Recall (Up on an empty deck input) / undo of a pending message:** `POST /api/sessions/{code}/recall` under the same lock:
  requires `$.prompt.read` empty and `header.queued` non-empty; sends Up; reads the input; it must equal the queued texts joined
  as Claude Code joins them, else put nothing back and report `recall_mismatch`; clears the TUI input (`fill('')`); returns the text
  to the deck input. Claude Code pops **all** queued items at once, so recall returns all of them (same as the terminal). A turn
  ending between Up and read → `already_sent`.
- Interrupt: unchanged (mod `$.turn.abort` where present, else Esc).

## 5. Accepted residual risks (from U3 review rounds 1–4)
- A dialog opening in the moment between the last check and Enter (Enter then acts on the dialog). Minimised by fill's dialog
  refusal and the immediate re-check; cannot be made atomic.
- A background task / scheduled wake-up starting a turn: the message simply queues (now the intended behaviour).
- Someone typing in the terminal at the same moment: `input_not_empty` refuses instead of mixing.
- Sessions without the mod: blind typing as iOS does today.

## 6. Phases
- K-1 daemon: `/submit` with lock, ledger, sanitise, guards, mod fill path, keys fallback; `/recall`. Tests with a fake mod and a
  tmux test server (`-L` label).
- K-2 Mac SPA: SessionInput sends via `/submit` (immediately), pending/accepted rendering from request + `header.queued` +
  transcript, Up → `/recall`, remove the wait-for-idle ledger; mod `/conversations/.../submit` no longer used by the deck.
- K-3 iOS: Composer uses `/submit` instead of raw send-keys (keeps its buffer), pending/accepted, recall.
- Real-machine check before K-2/K-3 merge: busy session — message queues and is read mid-turn; Up recall returns it; a permission
  dialog open → `dialog_open`, nothing typed.
