# K-1 — daemon submit by typing (shared by Mac and iOS) — plan (rev 2, after the spec+plan review)

Spec: `docs/specs/2026-10-11-keystroke-submit-spec.md`. PRs: **K-1a-1** (measurement + promptq kinds + mod), **K-1a-2** (route,
lock, guards, keys fallback), **K-1b** (recall). The mod change ships with the daemon (deploy runs `pdx setup`).

## Step 0 — measure first (gates the plan)
On a throwaway Claude Code session (Haiku, tmux, a throwaway mod loaded with `--plugin-dir` that calls the APIs from inside a
running turn and logs the results to a file): (a) do `$.prompt.read()` / `$.prompt.fill({mode:'replace'})` return at once while a
turn runs, or block until idle like `$.prompt.submit`? (b) the full set of `refusal` values (dialog, no_composer, others); (c) does
`fill` keep newlines / trailing spaces as given, and what does `read` return right after; (d) Enter after a mid-turn fill queues
the message (`queue-operation enqueue`) and it is read at the next tool boundary; (e) `#{alternate_on}` value of a Claude Code pane
(fullscreen mode?); (f) Up on an empty input with a queue containing user + peer + task-notification items: which items pop
(`popAllEditable`?), and the separator between popped items; (g) Up with an empty queue (history recall). Write the results into the
spec §2 before any code. **If (a) blocks, stop and report — the fill route does not work.**

## Review decisions (rev 2)
- C1 No stale Enter: the mod's fill result carries `filled_at`; the daemon presses Enter only if `now − filled_at ≤ 1.5 s` **and** the
  agent light is not `waiting` (`LightStatus`) at that moment; otherwise it clears (see I2) and answers `unknown`/refused. A `fill`
  result is posted once (no result retries for this kind — retries only widen the window).
- I1 Enter / Up use a new `SendKeysIfInstanceAndMode` guard: generation **and** `#{pane_in_mode}==0` (copy-mode would eat Enter)
  → `409 pane_busy`; `#{alternate_on}==0` only if step 0 (e) shows Claude Code runs on the main screen.
- M7 Keys go to the **owner pane** (`-t <pane id>` of the mod's frame), never "the session's active pane"; a mismatch → `409
  pane_mismatch`.
- I2 Cleanup: whenever a fill was accepted but Enter is not sent (guard refused, stale, waiting, daemon timeout, late execution of a
  timed-out fill), queue a best-effort `clear_if` job: the mod clears the input only if it still equals our text.
- I3 One state machine per `client_msg_id`, in the existing promptq ledger (same key, so `Match` keeps pairing echoes):
  `queued → filled → entering → entered | cleared | unknown`; the state is written as `entering` **before** Enter (the at-most-once
  line); a replayed id returns the recorded state and never types.
- I4 The route lives in the **conversation module** (it already has agent, promptq, convfeed, `sanitizePrompt`, `decodeBody`); URL
  stays `/api/sessions/{code}/submit`; tmux comes in through an interface; export a `code → (sid, frame/pane)` resolver from where
  `resolveSessionOwner` lives; "waiting" = `LightStatus(sid, frameID)`.
- M1 One per-code lock shared with the old `/api/conversations/claude/{sid}/submit` route (still used until K-2/K-3 switch); bounded
  wait 2 s → `429 busy` (Mac + phone at once must not hang 8 s).
- M2/M3 Reuse the existing `sanitizePrompt` (one copy); `@token` padding only in keys mode; compare fill's returned text with the
  same normalised string; leading `/` and `!` stay refused in both modes (Enter would still run them as a command / bash).
- M4 Keys fallback only when no mod owner has been seen for ≥ 10 s and the light is `idle` or `running`; answer `mode:"keys"`; the
  deck labels such sends 「未經 mod 確認」. **K-1c (measured 2026-10-11, Claude Code 2.1.296):** one `send-keys` with text+Enter
  swallows the Enter for ≥ 90 chars or multi-line, so it is text, 150 ms settle (`keysSettle`), light re-read (not idle → no Enter,
  `text_left_in_terminal`), then a separate guarded Enter (any refusal there → `text_left_in_terminal`); LF stays LF; any trailing
  `\` gets a space. Spec §5 has the table.
- I5/I6 Recall: until step 0 (f) is known, a queue holding non-user items → `409 recall_unsupported`; the input must be empty
  first; the join over `promptq.MaxTextBytes` or an item at the feed's 2000-rune cap → `409 recall_too_long` (nothing pressed);
  Up under the same guard and pane target; reads on a detached deadline (poll every `recallSettle` up to a 1 s budget). Only a read
  EQUAL to the join is cleared (`clear_if(exactly that text)`) and answered `200 {text}`; refused clear → one more read (empty →
  200, equal → `409 input_not_cleared`, else `unknown clear_unknown`); any other non-empty or truncated read → `409 recall_mismatch`
  with no clear and no text; nothing read → `unknown recall_empty`; read error → `unknown read_failed`. All answers after Up are
  recorded in a ledger keyed by session code + tmux instance + client_msg_id (`typed_recall_ledger.go`), replayed before the owner is resolved; client_msg_id is required. Residual risk: a mismatch leaves the text in the TUI input.

## K-1a-1 — promptq kinds + mod
promptq job kinds `fill {text}`, `clear_if {text}`, `read` (K-1b); outcomes with `filled_at`; ledger state machine (I3). mod
`pqRun`: `fill` (session check; `read` non-empty → `input_not_empty`; `fill replace` → refusal → `dialog_open` / `no_composer` /
other; returned text ≠ ours → clean up + `fill_mismatch`; else `accepted` + `filled_at`), `clear_if`, `read`. events.test.ts with a
fake `$` for every branch; promptq tests for the state machine and the one-shot result.

## K-1a-2 — route, lock, guards, keys fallback
`POST /api/sessions/{code}/submit {text, client_msg_id, expected_tmux_instance}` in module/conversation, device-allowed
(cmd/pdx/device_scope.go). Order: lock (2 s) → ledger → sanitise → session / generation / owner pane → not waiting →
owner: fill → C1 checks → `entering` → Enter (I1/M7 guard) → `entered` / on refusal `clear_if` → answer
`{outcome: handed|refused(reason)|unknown, mode: fill|keys}`; no owner ≥ 10 s: keys mode (M4). Tests: sanitiser reuse, ledger replay
(each state), lock + 429, waiting, generation / pane_busy / pane_mismatch, stale fill (>1.5 s) → no Enter + clear_if, light turns
waiting → no Enter, fill dropped reasons → 409 no Enter, timeout → unknown + clear_if, keys fallback gating; tmux test server with
`-L <label>` and `unset TMUX` (never a bare kill-server).

Contracts K-1a-1 hands to this PR (review fixes):
- **Freshness uses `Result.ReceivedAt`** (the daemon's clock when the mod's accepted fill result arrived), never the mod's
  `filled_at` (clamped to [handed out, ReceivedAt] and informational): the 1.5 s check is `now - ReceivedAt`.
- **Phase `unknown`** (reasons `no_result` / `not_owner` / `client_gone`, or the mod's own `unknown`/`fill_error`) means our
  text may be in the composer. The route sends one best-effort `ClearFilled(id)` (= `ClearIf(row text)`, which marks the row
  `cleared` on accept; `unknown -> cleared` is allowed only if `MarkEntering` never ran). A caller that went away
  (`client_gone`) leaves a **background finaliser** behind: it waits for the entry to settle (`Lookup` until the phase leaves
  the in-flight state or HandTimeout passes) and, for a row in `filled` whose Enter was never sent, calls `ClearFilled`.
- `SubmitFill` / `ClearIf` answer `dropped/bad_text` at once for blank text or text over `promptq.MaxTextBytes` (16384 BYTES, the
  mod's parse limit is the same); more than 256 open typed rows (filled / entering / unknown, never evicted) answers `busy`.
- The mod may report `unknown` (could not tell what its fill left behind): treat like a lost result.
- A row whose cleanup does not succeed (`ClearIf` dropped `not_ours`, or no answer) must still reach an end state: after one
  retry the route marks it settled (e.g. `cleared` with reason `cleanup_gave_up`, or a dedicated terminal phase) so it stops
  counting against the 256 open typed rows. Test the finaliser and this end state in K-1a-2.

## K-1b — recall
`POST /api/sessions/{code}/recall {client_msg_id (required), expected_tmux_instance}` per I5/I6 and step 0 (f)/(g) results.

## Acceptance (real machine, before K-2 / K-3 merge)
Busy session: a submit queues (header.queued shows it, pending style) and is read mid-turn (`queued_command`, accepted style);
idle: runs at once; a terminal draft → `input_not_empty`, unchanged; a permission dialog open → `dialog_open`, nothing typed;
copy-mode → `pane_busy`; daemon restarted between fill and Enter → the input is cleaned; recall returns the queued text and leaves
the TUI input empty; 10 s with neither queued nor transcript → 「可能已送出，請看紀錄」 (K-2).
