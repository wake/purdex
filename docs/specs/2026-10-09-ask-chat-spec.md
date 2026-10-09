# Answering an AskUserQuestion with a reply ("chat about this") — spec + plan

Date: 2026-10-09 · Owner: interface line lead (`mlab/purdex-88-b8`) · Status: draft for plan review

The phone's ask card (purdex-ios, Deck) answers a remote AskUserQuestion (`hook_ask`). The user asked for the two things
the terminal dialog has and the card lacks: a free-text answer per question ("Other") and, for the whole ask, replying
in words instead of picking answers ("Chat about this"). The first needs nothing new (below); this spec adds the second.

## 1. Facts (main @ 16e95fed, measured)

- `decideHook` takes a `hook_ask` only as `decision: "approve"` with non-empty `hook.answers`; anything else → 400
  (`internal/module/team/ask_handler.go:406`, the message at `:418`). `hook.answers` is question text → answer string,
  any string (`internal/team/wire_ask.go`), so a free-text "Other" answer is already just a string: **the per-question
  free text is iOS work only** (done in purdex-ios #44).
- `askWaitOf` reports `answered_remote` (with the row's `hook`) for an approved row of either kind and for a denied
  `hook_permission`; every other closed row is `closed` with its state as the reason (`ask_handler.go:266`).
- `pdx ask wait` decodes the daemon's `AskWaitResponse` and prints it again; `HookDecision` already has `Message`
  (`cmd/pdx/ask.go`, `internal/team/wire_ask.go`), so a message passes through the CLI untouched.
- The mod (`cmd/pdx/plugin/purdex/hooks/ask.js`) races the native dialog against `pdx ask wait`: `answered_remote` whose
  answers fit the questions → it returns `{ result: { questions, answers } }` (`:211`, `:231`); `answered_remote` that
  does not fit → `remote-error`, the native dialog runs on alone and the terminal's outcome is reported; `closed` →
  `remote-closed`, the dialog runs on alone, nothing reported (`:214`, `:238`).
- Plugin API (this build's `claude-code.d.ts`, `tool.call` and `ToolCallResult`): a `tool.call` hook may answer
  `{ deny: reason }` — "the model receives the text as an error result" — and "returning with `next` pending aborts
  what runs beneath", i.e. the native dialog closes, exactly as `{ result }` does today.
- The Mac App has no hook_ask card (`spa/src` only parses the kind), and push is untouched by how a row closes.

## 2. Contract

### 2.1 Decide (daemon)

`POST /api/team/approvals/{id}/decide` on a `hook_ask`:

- `decision: "approve"` + `hook.answers` — unchanged.
- **New:** `decision: "deny"` + `hook.message` — the person's reply. The message is trimmed; 1–4000 runes after
  trimming, printable (newlines and tabs allowed); missing, empty or longer → 400 `bad_request`. `hook.answers` with a
  deny → 400 (one or the other, never both). The row closes `denied` with `Hook{Message}` (nothing else kept).
- A `terminal_only` row still answers 409 `terminal_only`; the CAS, `already_decided`, `answered_local` /
  `terminal_override` and the push dismissal are unchanged: whoever closes first wins, the terminal's answer stands
  over a remote one exactly as today.

### 2.2 Wait (daemon)

`askWaitOf`: a denied `hook_ask` → `{"state":"answered_remote","hook":{"message":"…"}}`. (Only a hook_ask denied through
§2.1 has a message; a denied hook_ask without one cannot exist after this change, and if one did it would read
`answered_remote` with no answers and no message, which the mod treats as `remote-error`.)

### 2.3 Mod

In the wait loop, `answered_remote`:

1. answers that fit → `{ result: { questions, answers } }` (unchanged);
2. else, a non-empty string `hook.message` (and no `hook.answers`) → `{ deny: CHAT_PREFIX + "\n\n" + message }`, with
   `CHAT_PREFIX = "The user did not pick an answer and replied instead:"` — the dialog closes and the model reads the
   reply as the tool's error result, the same channel the native "Chat about this" ends in (the question is not
   answered; the conversation goes on with the person's words);
3. else → `remote-error` (unchanged).

No report follows a remote deny (as for a remote answer): the daemon already holds the outcome.

### 2.4 Capability and clients

- `/api/info` capability **`team.ask_chat.v1`** (`internal/core/info_handler.go` `capabilities`), shipped in the same
  change. purdex-ios shows its 「改成跟 agent 聊聊」 entry (a secondary button under the card's send button that opens a
  text field) only on a host that announces it, and sends §2.1.
- Mixed versions: the daemon and the mod ship in one binary, so a new daemon with an old mod only happens mid-deploy
  (a session that has not reloaded the mod yet). Then the row closes denied, the old mod sees `answered_remote` with no
  fitting answers → `remote-error` → the native dialog stays up for the terminal: the reply is lost to the model but
  nothing hangs and the terminal can still answer. An old daemon never announces the capability, so the phone never
  sends a deny to it (it would answer 400 anyway).
- The Mac App: nothing (no hook_ask card). Push: nothing.

## 3. Plan — one PR (daemon wire + wait + capability + mod)

Rules: TDD (failing test first), one commit per task, mutation check before release, only the affected packages
(`internal/module/team`, `internal/core`, the mod's vitest); the full suites only before merge with a slot from 1f.
**Touching `ask.js` (the A line's 分流 mod): the lead tells η (`mlab/purdex-47-mc`) and 1f before the work starts.**
Review: codex R1 + R2 (attack → critic).

1. **Decide.** `decideHook`, `hook_ask` branch: accept `StateDenied` with a valid message (trim; 1–4000 runes;
   printable except `\n` `\t`), reject answers alongside it; close with `Hook{Message}`. Tests: deny + message →
   denied, row's hook carries exactly the trimmed message; deny with missing / empty / whitespace-only / 4001-rune /
   control-character message → 400; deny with answers → 400; approve without answers → 400 (unchanged); approve with
   answers → approved (unchanged); terminal_only → 409; a second decide → 409 `already_decided`; an `answered_local`
   report after a remote deny → `terminal_override` as for an approve.
2. **Wait.** `askWaitOf`: denied hook_ask → `answered_remote` with its hook. Tests: the body for denied hook_ask; the
   bodies for approved hook_ask, denied / approved hook_permission and the other closed states unchanged; `pdx ask
   wait` prints the message (CLI test through a fake daemon).
3. **Capability.** `team.ask_chat.v1` in `capabilities`. Test: listed by `GET /api/info`.
4. **Mod.** `ask.js` wait loop per §2.3; `CHAT_PREFIX` a module constant. Vitest (`ask.test.ts` rig): answered_remote
   with a message → the hook returns `{ deny }` equal to prefix + blank line + message and the native dialog's `next`
   is abandoned; with fitting answers → `{ result }` (unchanged); with answers that do not fit and no message →
   remote-error, native dialog alone (unchanged); with both answers and a message → the answers win (defensive; the
   daemon never sends both); with an empty message → remote-error.
5. **Comments.** The doc comments that say a hook_ask cannot be denied (`askWaitOf`, `decideHook`, `HookDecision` in
   `wire_ask.go`, `DecideRequest.Hook` in `wire.go`) updated to this contract. (The lead-team plan v2 says the same at
   its `TestAskWait_RemoteDenyIsAnsweredRemoteWithHook` note; it is history and stays as written.)

Acceptance (real session, after deploy): a throwaway session asks an AskUserQuestion with the phone connected; the phone
sends a reply → the terminal dialog closes, the transcript shows the tool's error result starting with `CHAT_PREFIX`,
and the agent answers the reply. The terminal answering first still wins.
