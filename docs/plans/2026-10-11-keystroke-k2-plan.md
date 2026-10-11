# K-2 — the Mac deck sends by typing, 1.5 s send buffer, take back — plan (rev 1)

Spec: `docs/specs/2026-10-11-keystroke-submit-spec.md` §3 (the daemon route) and §4 (what the deck shows, user decisions
2026-10-11). Daemon side is merged: K-1a-2 `/api/sessions/{code}/submit` (#2636, #2639), K-1b `/api/sessions/{code}/recall` (#2644).
PRs: **K-2a** (send path) and **K-2b** (take back), each ≤ 800 lines / 20 files incl. tests. iOS follows in K-3 (purdex-ios).

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

## Decisions
- **D1 Terminal port → the daemon route.** `submit(text, id)` → `POST /api/sessions/{code}/submit {text, client_msg_id,
  expected_tmux_instance}`. The port reads code and instance **at each call** through a getter (the pane content's `sessionCode` /
  `tmuxInstance`, falling back to the session list's `tmux_instance` for that code) — the memoized port must not freeze a stale
  instance. No instance known → the entry fails locally with `no_instance` (nothing sent). `interrupt` stays on the mod route
  (spec §4 "Interrupt: unchanged"). The fallback `make` in `useSendQueueDriver` / `usePending` / `PendingTail` must build the same
  terminal port (or never be reached for a terminal pane) — no path may still post to the old submit route.
- **D2 Outcomes.** Add `{kind: 'handed'; mode: 'fill' | 'keys'}` and `{kind: 'refused'; reason: string; status: number}` to
  `SendOutcome`. Mapping: `200 handed` → `handed`; `200 unknown` → `unknown` (may have run); `409/429` with `outcome: refused` →
  `refused`; `400` sanitiser / validation codes → `invalid` (existing keys: `empty_text`, `too_long`, `bad_text`, plus
  `bad_client_msg_id` / `bad_expected_tmux_instance` → `rejected`); other statuses → `rejected`; fetch failure / abort → `network`
  (may have run). The kinds only the old route produced (`dropped`, `busy`, `timeout`, `no_mod`, `not_owner`, `needs_terminal`)
  are removed when nothing else produces them (check the worker adapter and tests); `accepted` stays for the worker.
- **D3 Retries never reuse a refused id.** `refused` with reason `busy` (lock or promptq busy) → automatic retry after 1 s, 2 s,
  4 s, each attempt with a **new** `client_msg_id` (a refused id may replay its refusal forever); the entry keeps its identity and
  remembers every id it used, so the echo pairs with any of them. Any other refusal → `failed` with the reason's message and a
  Retry (new id; nothing was typed). `unknown` / `network` → `maybe`, never resent automatically; Resend (existing confirm step)
  uses a new id.
- **D4 The App no longer waits for idle.** Remove the `waiting` state, `setIdle`, the busy-resend quota and the idle input of
  `useSendQueueDriver` (keep `reconcile`). A busy session's message is queued by Claude Code itself (spec §3).
- **D5 Send buffer 1.5 s** for every adapter (`UNDO_MS = 1500`; the worker deck too — the decision is the deck's).
- **D6 Pending until accepted, no duplicate.** Every entry, terminal included, keeps `PENDING_USER_BOX` from send until accepted
  (`frameUntilEchoed` for every adapter, or the flag removed). After `handed`, when a `user` row of `header.queued` has the same
  text (both trailing-whitespace trimmed) the App entry is hidden — the header line now draws it; it reappears if the row leaves
  the header without an echo. Accepted = the transcript echo (`reconcile`, unchanged) → the entry settles; the transcript draws the
  accepted user block (current style, slightly dimmer — already U3). `handed` with neither a header match nor an echo for 10 s
  → `maybe` (「可能已送出，請看紀錄」, `deck.send.unknown`). `handed` with `mode: keys` → caption 「未經 mod 確認」 while pending.
- **D7 Take back (K-2b).**
  - A held entry (`undo` state): the **whole block** is clickable (role=button, keyboard Enter/Space, pointer cursor, title
    「點一下拉回」) and takes back **that entry only** (existing `recallHeld(key, queue, id)`); the Undo button may stay.
  - A Claude Code-queued message — a `user` row of `HeaderQueuedLine`, or an App entry already `handed` but not yet matched —
    is clickable: `POST /api/sessions/{code}/recall {client_msg_id (new per action), expected_tmux_instance}`. `200 {text}` →
    prepend to the draft (`text + '\n' + draft`, or `text` when empty), focus the input, and hide every App entry whose text is
    one of the recalled lines (they are back in the draft). All queued user messages come back at once (Claude Code pops all;
    user decision 2026-10-11). Peer rows are not clickable.
  - Up on an empty input takes back everything pending, oldest first: Claude Code's queue via `/recall` (when `header.queued` has
    user rows) and the held entries (`recallHeld`). Draft = recalled text, then held text. A failed `/recall` still returns the
    held entries and shows the failure.
  - One recall in flight per pane (Up / clicks ignored meanwhile, the row shows a spinner); a network failure retries once with
    the **same** id (the daemon replays it), then shows the network message.
  - The draft write is shared with `recallHeld`: extract `prependToDraft(key, text)` (writeDraft + reconcileAttachments +
    discardCleared + notify the recall listeners). A result arriving while the tab is switched away is written to draft memory
    and shows on return (tab-hosted checklist).
  - Messages (`deck.recall.*`, zh-TW + en): `input_not_empty` 「終端機輸入框有字，沒有拉回」; `recall_mismatch` 「終端機輸入框
    出現別的內容，沒有拉回，請到終端機確認」; `nothing_queued` 「已經送出，拉不回來」; `recall_unsupported` 「佇列裡有不是你送的
    訊息，請到終端機處理」; `recall_too_long` 「訊息太長，請到終端機處理」; `input_not_cleared` 「沒有拉回：終端機輸入框沒清掉，請
    到終端機處理」; `unknown` (any reason) 「可能已送出，或還在終端機輸入框，請看終端機」; `busy` 「忙碌中，稍後再試」; any other
    refusal 「終端機狀態變了，請到終端機處理（{code}）」; network 「連線中斷，結果不明，請看終端機」. Shown as the pending tail's
    notice (same place and tone rules as send outcomes), dismissable.
- **D8 Chat** shares the footer, queue and `PendingTail` (`SessionPaneContent.tsx:108-169`), so it gets D1–D7 with no extra code;
  tests cover one chat path.
- **D9 Not in K-2:** the daemon's old mod submit route stays (removal is a follow-up issue once K-3 ships); interrupt unchanged.

## K-2a — send path (D1–D6)
1. `send.ts`: daemon submit client + D2 mapping + `outcomeMessage` for `handed` / `refused` reasons (`input_not_empty`,
   `dialog_open`, `pane_busy`, `waiting`, `generation`, `owner_changed`, `stale`, `conflict`, `light_unavailable`, `no_owner_yet`,
   `text_left_in_terminal`, `mod_timeout`, `client_msg_id_reused`, `not_claude`, `ambiguous_pane`, `pane_mismatch`, `busy`; one
   generic message with the code for the rest). Tests per mapping.
2. Terminal adapter + port with the code / instance getter; `SessionPaneContent` passes `tmuxInstance` down (footer context or
   adapter args); the fallback `make` paths fixed. Tests: the URL and body (`SessionPaneContent.send.test.tsx`), an instance
   stamped after the queue was created is used, `''` → `no_instance`.
3. `send-queue.ts`: remove waiting / setIdle / quota (D4); D3 retry with new ids + id list for echo pairing; `UNDO_MS = 1500`
   (D5); `handed` → `sent`-like state with the 10 s → `maybe` timer; hide-when-in-header (D6) exposed for `PendingTail`.
   Update `send-queue.test.ts`, `send-queue.recall.test.ts`, `useSendQueueDriver`.
4. `QueuedMessages` / `PendingTail`: frame until accepted for terminal entries, header de-duplication, keys caption. Tests incl.
   the header match / un-match and the 10 s timer (fake timers).
5. Tab switch (real `TabContent`): a held entry still sends after a switch (existing test, 1.5 s); a `handed` entry survives a
   switch and still de-duplicates against the header on return.

## K-2b — take back (D7)
1. `recall` client in `send.ts` (or `recall.ts`): request, answer mapping, one same-id retry on network failure. Tests.
2. `prependToDraft` extracted from `draft-recall.ts`; `recallHeld` uses it. Tests unchanged in behaviour.
3. Clickable held block; clickable header `user` rows and handed entries → `/recall`; Up path extended (Claude Code queue +
   held). Tests: click held → only that entry; click a header row with 3 queued → all three lines, one per line, draft
   prepended; draft non-empty → recalled first; Up with both kinds → Claude Code text then held; each `deck.recall.*` reason; in
   flight → second click ignored; peer row not clickable; chat view path once.
4. Tab switch: a recall answered while switched away → the draft holds the text on return (real `TabContent`).

## Merge gates
- Both PRs: codex R1 + attack in parallel, then critic (gpt-5.6-sol, effort low); full `vitest --maxWorkers=2` + `pnpm exec tsc -b`
  on a freshly fetched origin/main.
- **K-2a and K-2b merge only after alpha.710 (or later, with #2636 / #2639 / #2644) is deployed on mlab and air26** — the SPA
  reaches the Mac App through the dev server at once, and a daemon without `/submit` would fail every send.
- Real-machine acceptance (spec §4, plan K-1 "Acceptance") on mlab after 710, before K-2b merges: busy session — a send queues
  (header shows it, framed, no duplicate) and is read mid-turn; idle → runs at once; terminal draft → `input_not_empty`, terminal
  unchanged; permission dialog open → `dialog_open`; copy-mode → `pane_busy`; click a queued message → it comes back, TUI input
  empty; Up with an empty queue in the terminal input is never pressed (deck refuses with `nothing_queued`); keys mode (no mod):
  text and Enter in one write is not taken as a paste, Up sequence `\x1b[A` vs `\x1bOA`, settle delay.
