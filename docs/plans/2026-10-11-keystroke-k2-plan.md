# K-2 — the Mac deck sends by typing, 1.5 s send buffer, take back — plan (rev 2, after the plan review)

Spec: `docs/specs/2026-10-11-keystroke-submit-spec.md` §3 (the daemon route) and §4 (what the deck shows, user decisions
2026-10-11). Daemon side is merged: K-1a-2 `/api/sessions/{code}/submit` (#2636, #2639), K-1b `/api/sessions/{code}/recall` (#2644).
PRs: K-2a-1 (daemon capability), K-2a-2 (send client + bound terminal adapter), K-2a-3 (queue), K-2b (take back) — each ≤ 800 lines / 20 files incl. tests (budgets below). iOS follows in K-3 (purdex-ios).

## Facts (code at 17fc48a76)
- One queue for the deck, chat and the worker deck: `spa/src/lib/conversations/send-queue.ts` (`SendQueue`, `sendQueueFor(key, make)`),
  keyed by `draftKey(paneKey, hostId, sessionId)`. It holds a message `UNDO_MS = 3000` (`undo` state), then sends; a `busy` answer
  puts it in `waiting` and `setIdle()` resends on the next idle edge (the #2617 "wait for idle" ledger, quota + 1/2/4 s backoff);
  `unknown` / `not_owner` / `network` → `maybe` (`mayHaveRun`), settled by the transcript echo (`reconcile`, by `client_msg_id`,
  else text within a window). `resend` = new id + old entry `superseded`.
- The terminal adapter (`lib/conversations/terminal-adapter.ts:9-17`) is built from `(hostId, sessionId, sessionCode)`; its port is
  `hostSendPort` (`send.ts:82`) → mod route `POST /api/conversations/claude/{sid}/submit` → `{status, reason}` parsed by `fromStatus`.
  The worker adapter (`worker-adapter.ts`) never answers `busy`.
- `sendQueueFor` memoizes the port when the queue is created (`adapterOf(key)?.port ?? make`); `useSendQueueDriver` and
  `usePending` pass `hostSendPort` as the fallback `make`.
- `tmuxInstance` is on the pane content (`types/tab.ts:123`, `''` = unknown for legacy records, stamped later by
  `useTabStore.adoptTmuxInstance`); `Session.tmux_instance` is in the session list. Neither reaches the deck input today
  (`SessionPaneContent.tsx:30-33,111`; `DeckFooterContext` has no code / instance).
- Pending rendering: `components/deck/QueuedMessages.tsx` — `undo`/`waiting` use `PENDING_USER_BOX` (accent frame) + an Undo button
  (the block itself has no click); `sending`/`sent` keep the frame only for workers (`frameUntilEchoed`), terminal entries turn into
  a grey card. `HeaderQueuedLine` (`PendingTail.tsx:16-33`) draws Claude Code's own queue (`header.queued`, user rows framed, peer
  rows teal), not clickable, shown only while running / waiting (`shownHeaderQueue`, `usePending.ts:22`).
- Up in the deck input (`SessionInput.tsx:262-267`): empty draft, nothing uploading, `queue.heldCount() > 0` → `recallHeld`
  (`draft-recall.ts:24-37`: takes every held message, oldest first, prepends `back + '\n' + draft`, re-adds chips, notifies the
  input). No daemon call; nothing for Claude Code's queue.
- Daemon answers (`internal/module/conversation/typed_answers.go`): one shape `{outcome?, mode?, reason?, error?, text?}`;
  `200 handed {mode: fill|keys}`, `200 unknown`, `409/429 refused {reason}`, `400/404/500/503` without outcome. A refusal from the
  fill step is recorded under the id as `cleared` and **replays the same refusal** for that id (`replayTyped`); refusals before the
  job (lock `busy`, `generation`, `waiting`, …) are not recorded. `/recall` requires `client_msg_id`, answers `200 {text}`,
  `409 refused {reason}` or `200 {outcome: unknown, reason}`.
- None of #2636 / #2639 / #2644 is in a released daemon yet (CHANGELOG stops at alpha.709): **no host serves `/submit` or
  `/recall` until 710 is deployed.**
- `/submit` refusals, classified (`typed_submit.go`, `typed_fill.go`, `typed_keys.go`): **every refusal means nothing was sent**
  (before the fill; a mod refusal / clean fill; or a fill that was cleaned up best effort before any Enter — a failed clean-up
  leaves our text in the composer unsent, and a retry then meets `input_not_empty`), **except `text_left_in_terminal`** (keys mode,
  tmux failed mid-write: the text may be in the terminal and the id stays recorded as unknown). `needs_terminal` (a leading `/` or
  `!`) comes back as `400 needs_terminal` from the shared sanitiser (`submit.go:103`, `typed_submit.go:46`).
- Capabilities (`submit.go` `capabilitiesFor`): a terminal conversation declares `send: "prompt"` only while a mod owner is live;
  otherwise `send` stays `not_wired`, so the terminal adapter's `canSend` (`caps.send === 'prompt'`) disables the input and the
  daemon's keys fallback (spec §3.5, plan K-1 M4) is unreachable from the deck.
- `PendingTail` / `HeaderQueuedLine` sit in the stream tail (`DeckView.tsx:154`, `ChatView.tsx:146`) and get only pane, host,
  conversation sid, entries and queued — no session code or tmux instance.

## Decisions
- **D1 Bound terminal adapter with a live target.** `SessionPaneContent` binds a terminal adapter for its draft key
  (`bindAdapter(draftKey, …)`, as `WorkerDeckPane` does for workers) built with a **getter** `target(): {code, instance} | null`
  that reads, at each call, the pane content's `sessionCode` / `tmuxInstance`, falling back to the session list's `tmux_instance`
  for that code. Every consumer goes through `adapterOf(key)`: the port, `SessionInput`, `PendingTail` (D7) — no prop drilling
  through Deck/Chat views, and the memoized port never freezes a stale instance. The fallback `make` in `useSendQueueDriver` /
  `usePending` / `PendingTail` must never build the old mod submit port for a terminal pane (bind before the first queue access,
  or make the fallback the same bound adapter). Port `submit(text, id)` → `POST /api/sessions/{code}/submit {text, client_msg_id,
  expected_tmux_instance}`; no target → local outcome `no_instance` (nothing sent). `interrupt` stays on the mod route (spec §4).
- **D1b Keys capability (daemon).** `capabilitiesFor` declares `send: "keys"` for a terminal conversation that is live (the same
  `live` the header reports) and has no mod owner; `prompt` wins when the mod is there; ended / not live stays `not_wired`. The
  terminal adapter's `canSend` accepts `prompt` and `keys`. `interrupt` unchanged. The daemon still decides per request (keys only
  after 10 s without an owner and an idle light → else `no_owner_yet`).
- **D2 Outcomes.** Add `{kind: 'handed'; mode: 'fill' | 'keys'}` and `{kind: 'refused'; reason: string; status: number}` to
  `SendOutcome`. Mapping: `200 handed` → `handed`; `200 unknown` → `unknown`; `409/429` with `outcome: refused` → `refused`;
  `400 needs_terminal` → `needs_terminal` (kept: 「這種指令請到終端機操作」); `400 empty_text|too_long|bad_text` → `invalid`;
  other `400` (`bad_client_msg_id`, `bad_expected_tmux_instance`) / `404` / `500` / `503` → `rejected` (no outcome: not run);
  fetch failure / abort → `network`. The old route's `dropped`, `busy`, `timeout`, `no_mod`, `not_owner` are removed when nothing
  else produces them (the worker adapter produces `accepted`, `rejected`, `network` only — verify); `accepted` stays for workers.
- **D3 What may have run, and retries.** `mayHaveRun` = `unknown`, `network`, and `refused` with reason **`text_left_in_terminal`**:
  → `maybe` (「可能已送出，請看紀錄」, for `text_left_in_terminal` 「文字可能留在終端機，請看終端機」), never resent automatically;
  Resend keeps its confirm step and uses a new id. `refused` `busy` (either 429: the pane/session lock, or promptq busy) → automatic
  retry after 1 s, 2 s, 4 s, **each attempt with a new `client_msg_id`** (a fill refusal is recorded and replays itself for that id);
  the entry keeps its identity and the list of every id it used, and `reconcile` pairs an echo with any of them. Every other
  refusal → `failed` with its reason's message and a Retry (new id). After the retries are spent → `failed` (busy message).
- **D4 The App no longer waits for idle.** Remove `waiting`, `setIdle`, the busy-resend quota and the idle input of
  `useSendQueueDriver` (keep `reconcile`). Claude Code queues a busy session's message itself (spec §3).
- **D5 Send buffer 1.5 s** for every adapter (`UNDO_MS = 1500`; the worker deck too — the decision is the deck's).
- **D6 Pending until accepted, no duplicate.** Every entry, terminal included, keeps `PENDING_USER_BOX` from send until accepted.
  After `handed`, while the header line **actually renders** a `user` row with the same text (both trailing-whitespace trimmed;
  i.e. `shownHeaderQueue` returns it), the App entry is hidden; it shows again when that row is no longer rendered and no echo
  came. Accepted = the transcript echo (`reconcile`) → settled; the transcript draws the accepted block (U3 style). `handed` with
  neither a header match nor an echo for 10 s → `maybe`. `mode: keys` → caption 「未經 mod 確認」 while pending.
- **D7 Take back.**
  - The bound adapter gains `takeBack(): Promise<RecallOutcome>` (terminal: `POST /api/sessions/{code}/recall {client_msg_id,
    expected_tmux_instance}` from the D1 getter; one new id per user action; a `network` failure retries once with the **same** id).
    The worker adapter has none (its App-held messages still come back through `recallHeld`).
  - `RecallOutcome` classes, mirroring `typed_recall.go`: **back** `200 {text}`; **not run** — `400`, `404`, `409/429 refused`
    (`nothing_queued`, `recall_unsupported`, `recall_too_long`, `input_not_empty`, `recall_mismatch`, `input_not_cleared`, guards,
    `busy`, …), `500`, `503` other than `keys_failed`; **may have popped** — `200 {outcome: unknown, reason}` (`recall_empty`,
    `read_failed`, `clear_unknown`), `503 keys_failed`, `network` after the retry.
  - A held entry (`undo`): the **whole block** is clickable (role=button, Enter/Space, pointer, title 「點一下拉回」) and takes back
    **that entry only** (`recallHeld(key, queue, id)`).
  - A Claude Code-queued message — a `user` row of `HeaderQueuedLine`, or an App entry `handed` and not yet matched — is clickable
    → `takeBack()`. `back` → prepend to the draft (`text + '\n' + draft`, or `text`), focus the input, hide every App entry whose
    text is one of the recalled lines. All queued user messages come back at once (user decision 2026-10-11). Peer rows are not
    clickable.
  - Up on an empty input takes back everything pending, oldest first: `takeBack()` when the header renders user rows, then the held
    entries. Draft = recalled text, then held text. A failed `takeBack()` still returns the held entries and shows the failure.
  - One take-back in flight per pane (Up / clicks ignored meanwhile, the row shows a spinner).
  - Draft writes share `prependToDraft(key, text)` extracted from `draft-recall.ts` (writeDraft + reconcileAttachments +
    discardCleared + notify the recall listeners); a result that lands while the tab is switched away stays in draft memory and
    shows on return.
  - Messages (`deck.recall.*`, zh-TW + en), shown as the pending tail's notice (dismissable): `input_not_empty` 「終端機輸入框有字，
    沒有拉回」; `recall_mismatch` 「終端機輸入框出現別的內容，沒有拉回，請到終端機確認」; `nothing_queued` 「已經送出，拉不回來」;
    `recall_unsupported` 「佇列裡有不是你送的訊息，請到終端機處理」; `recall_too_long` 「訊息太長，請到終端機處理」;
    `input_not_cleared` 「沒有拉回：終端機輸入框沒清掉，請到終端機處理」; `busy` 「忙碌中，稍後再試」; other not-run 「終端機狀態變了，
    請到終端機處理（{code}）」; may-have-popped 「可能已送出，或還在終端機輸入框，請看終端機」.
- **D8 Chat** shares the footer, queue and `PendingTail` (`SessionPaneContent.tsx:108-169`): it gets D1–D7 with no extra code; one
  chat-path test per PR that touches the tail.
- **D9 Not in K-2:** the daemon's old mod submit route stays (follow-up issue after K-3); interrupt unchanged.

## PRs (budgets are changed lines incl. tests; if a PR's diff passes 800 or 20 files, split at the numbered task boundary)
**K-2a-1 — daemon keys capability (D1b), ~120.** `capabilitiesFor` + tests (live terminal without owner → `keys`; with owner →
`prompt`; not live → `not_wired`; execution unchanged). go test -race on the conversation package.

**K-2a-2 — send client + bound terminal adapter (D1, D2), ~550.**
1. `send.ts`: daemon submit client, D2 mapping, `outcomeMessage` for `handed` (keys caption), `needs_terminal`, every `refused`
   reason of the classification (one message each for `input_not_empty`, `dialog_open`, `pane_busy`, `waiting`, `generation`,
   `no_owner_yet`, `text_left_in_terminal`, `busy`; a generic message with the code for the rest), `no_instance`. One test per status
   / reason class incl. both 429 bodies and a 400 without outcome.
2. Terminal adapter with the target getter, bound by `SessionPaneContent`; `canSend` for `prompt` / `keys`; the fallback `make`
   paths can no longer reach the old submit route. Tests: URL + body (`SessionPaneContent.send.test.tsx`); an instance stamped after
   the queue exists is the one sent; `''` everywhere → `no_instance`, no request; `keys` capability enables the input.

**K-2a-3 — the queue (D3–D6), ~650.**
1. `send-queue.ts`: remove waiting / setIdle / quota; `mayHaveRun` per D3; busy retries with a new id each (fake timers: 1 s / 2 s /
   4 s, three different ids posted, then `failed`); an echo carrying an earlier id settles the entry; a retry answered `unknown` /
   `network` is not resent; `UNDO_MS = 1500`; `handed` + 10 s → `maybe`. Update `send-queue.test.ts`, `send-queue.recall.test.ts`,
   `useSendQueueDriver`.
2. `QueuedMessages` / `PendingTail`: frame until accepted for terminal entries; hidden while the header renders the same text,
   shown again when it stops; keys caption. Tests incl. header match / un-match, status idle (header not rendered → entry shown).
3. Tab switch (real `TabContent`): a held entry still sends after a switch (1.5 s); a `handed` entry survives and still
   de-duplicates on return.

**K-2b — take back (D7), ~700.**
1. `takeBack()` on the terminal adapter + `RecallOutcome` mapping: one test per class and status (200 text; 200 unknown × 3 reasons;
   400 / 404 / 409 reasons / 429 / 500 / 503 / 503 `keys_failed`; network → same-id retry → then may-have-popped).
2. `prependToDraft` extracted; `recallHeld` uses it (behaviour tests unchanged).
3. Clickable held block, clickable header user rows and handed entries, Up merge, in-flight guard, `deck.recall.*` messages. Tests:
   click held → only that entry; click a header row with 3 queued → three lines, draft prepended; draft non-empty → recalled first;
   Up with both kinds → Claude Code text then held; failed takeBack + held → held back, notice shown; second click while in flight
   ignored; peer row not clickable; one chat-path test.
4. Tab switch (real `TabContent`): a take-back answered while switched away → the draft holds the text on return.

## Merge gates
- Every PR: codex R1 + attack in parallel, then critic (gpt-5.6-sol, effort low); SPA PRs run full `vitest --maxWorkers=2` +
  `pnpm exec tsc -b` on a freshly fetched origin/main; the daemon PR runs `go test -race` on the touched packages.
- **K-2a-2, K-2a-3 and K-2b merge only after alpha.710 (or later, with #2636 / #2639 / #2644) is deployed on mlab and air26** — the
  SPA reaches the Mac App through the dev server at once, and a daemon without `/submit` fails every send. K-2a-1 (daemon only,
  additive capability value) can merge any time; the SPA ignores `keys` until K-2a-2.
- Real-machine acceptance (spec §4, plan K-1 "Acceptance") on mlab after 710, before K-2b merges: busy session — a send queues
  (header shows it, framed, no duplicate) and is read mid-turn; idle → runs at once; terminal draft → `input_not_empty`, terminal
  unchanged; permission dialog open → `dialog_open`; copy-mode → `pane_busy`; click a queued message → it comes back and the TUI
  input is empty; nothing queued → `nothing_queued`, Up never pressed; keys mode (no mod): text and Enter in one write is not taken
  as a paste, Up sequence `\x1b[A` vs `\x1bOA`, settle delay.
