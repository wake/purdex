# Message stages step 2 — plan (daemon + Mac + iOS)

> B line, 2026-10-19. Implements step 2 of `docs/specs/2026-10-16-message-stages-spec.md` (decisions D1–D9 in §2,
> outline in §4). Step 1 (iOS only) shipped in purdex-ios #144 / #147 (0.6.82–0.6.83). This plan fixes the wire
> contract first so the daemon, Mac and iOS tracks run in parallel. Code facts are from main `c937b388f`.

## 0. Decisions taken here (lead; the user's are D1–D9)

| # | Decision | Why |
|---|---|---|
| L1 | A message is keyed by its **first** `client_msg_id` (`entry.ids[0]`, `spa/src/lib/conversations/send-queue.ts:51`). Busy retries keep minting new ids (`:359-361`, needed because refused fills are replayed by id, `internal/module/conversation/typed_answers.go:56-70`); the client folds every stage of any of its ids into the one bubble. | D2 without changing the retry rule |
| L2 | Stages and order: `received` < `filled` < `entered` < `queued` < `read`; terminal: `read`, `failed`, `recalled`; `unknown` may still advance to `queued` / `read` by matching. A stage never moves back. | D1 |
| L3 | Send now is refused while the light is `waiting` (a dialog is open): the keys would act on the dialog. Reason `waiting` (same as `/submit`, `typed_submit.go:100-103`). | safety |
| L4 | Send now is refused when nothing is queued (`nothing_queued`, as `/recall`). A draft alone is an ordinary send. | D7 spirit |
| L5 | Mac **Up on the first line** of the Deck input takes back the queue when anything can be taken back (D8); otherwise Up keeps today's history stepping (#2666) and caret movement. Today's rule only fires on an empty box (`spa/src/lib/conversations/input-arrows.ts:84-86`). Taken-back text goes before the draft, one message per line (`draft-recall.ts:38`). | D8 |
| L6 | Mac binds both Ctrl+Enter and Ctrl+X Ctrl+S in the Deck input. Ctrl+X arms for 1.5 s; Ctrl+S inside that window sends now; anything else disarms (Ctrl+X alone keeps its normal cut). Ctrl+Enter no longer falls into the plain-Enter send (`SessionInput.tsx:268`). | D8 |
| L7 | The stage ledger lives in daemon memory, like promptq's (`internal/promptq/queue.go:68-74`). After a daemon restart in-flight ids lose their stages and the clients fall back to today's text-and-time matching. Persistence of unconfirmed messages is the App's job (D3). | scope |
| L8 | Ask the user: should the Mac also keep unconfirmed messages across an App reload (D3 on Mac)? Today the Mac queue is memory only (`send-queue.ts:389`). Not in this plan until answered. | open |

## 1. Contract (fixed before any track starts)

### 1.1 Stages

| Stage | Meaning | Daemon source |
|---|---|---|
| `received` | `/submit` accepted the id (validated, before the ledger replay) | `typed_submit.go:27-57` |
| `filled` | the mod filled Claude Code's input (fill mode) | promptq phase `filled` (`internal/promptq/typed_submit.go:13`), `Result.FilledAt` |
| `entered` | Enter reached the pane (fill: `typed_fill.go:78-86`; keys: `typed_keys.go`) | `MarkEntered` / keys ledger |
| `queued` | the text is in Claude Code's own queue | a `header.queued` entry (`internal/ccnorm/queue.go:131-145`) matched by text + time |
| `read` | the text entered the transcript (user item, `source` user **or queued**) | `promptq.Match` (`echo.go:34-104`), extended to `SourceQueued` |
| `failed` | refused; `reason` is the `/submit` reason | `typed_answers.go` refusals |
| `unknown` | the outcome is not known; `reason` says why | `typed_fill.go:54`, `:111`; `typed_keys.go` |
| `recalled` | taken back by `/recall` | `typed_recall.go:132-215` |

Matching rules for `queued` and `read` follow `promptq.Match`: exact text (after the same normalisation), closest
first, one to one. The `read` window grows from 30 s after completion (`echo.go:19`) to **30 minutes** for an id that
reached `queued` (a busy session reads its queue late; the SPA already allows 10 min, `terminal-adapter.ts:21`).
The keys ledger (`typed_keys_ledger.go`, today `{handed, sid, at}`) also keeps the text (bounded like promptq's, 16 KiB)
so keys-mode messages can reach `queued` / `read`.

### 1.2 Wire

- New WS frame on `/ws/conversations/{provider}/{session_id}`: `{type: "conversation.stages", seq, value: {stages: Stage[]}}`.
  - `Stage = {client_msg_id: string, stage: string, at: number /* unix ms */, reason?: string, mode?: "fill" | "keys"}`.
  - The frame carries only the stages that changed since the last frame of this connection.
  - It is pushed from `follow()` beside `pushCaps`, **before** the quiet gate (`internal/module/conversation/ws.go:349`, gate at `:356`), so a stage change shows without a transcript change.
- `conversation.snapshot` gains `stages: Stage[]`: the latest stage of every id of this session still in the ledger. Ids drop 10 minutes after a terminal stage (promptq `LedgerTTL`). A reconnect therefore sees where its messages are.
- Clients ignore unknown frame types (the SPA does, `useConversationStore.ts:156`). An old daemon simply sends no stages.
- Host capability `conversations.stages.v1` (`internal/core/info_handler.go:49-71`; update the exact-list test `info_handler_test.go:419`). Add a real-module test: register the conversation module in a core and assert `/api/info` lists it.

### 1.3 Send now

- **Route**: `POST /api/sessions/{code}/send-now`, body `{client_msg_id, expected_tmux_instance}`, beside `/recall` (`module.go:211-212`).
  - Allowed for the phone in `cmd/pdx/device_scope.go:37-38`.
  - Host capability `sessions.send_now.v1`.
  - Conversation capability `send_now: "keys" | "not_wired"` in `convmodel/capabilities.go`, added to `capsKey` (`ws.go:87-91`) so a change is pushed.
- **Order of checks** (copied from `handleSessionRecall`, `typed_recall.go:33-97`):
  1. id and instance validation; generation;
  2. ledger replay (its own ledger keyed like `recallKey`, `typed_recall_ledger.go:23-27`);
  3. owner, locks, owner re-check, `paneIsUnique`;
  4. a mod owner, else `no_owner`;
  5. light `waiting` → `waiting` (L3);
  6. `queuedOf` empty → `nothing_queued` (L4);
  7. the mod's `Read` of Claude Code's input not empty → `input_not_empty` (D7, text 「桌機輸入框有未送出的文字」).
- **Keys**: `SendKeysIfInstanceAndMode` (`internal/tmux/send_keys_mode.go:57`) with the send-now bytes measured in MS2-0. The expected bytes are Ctrl+X Ctrl+S `\x18\x13`, the fallback is Ctrl+Enter. There are no new guards: generation, session and `pane_in_mode` are checked in the one `if-shell` (`:40-50`).
- **Answer**: `200 {outcome: "sent_now"}`, or `refused` + reason, or `unknown`, in `/submit`'s shape (`typed_answers.go:14-23`). Messages already queued then advance to `read` through the normal matching; send now adds no stage.

### 1.4 Marks (Mac and iOS, D1)

| Stage | Mark (zh-TW) | Mark (en) |
|---|---|---|
| buffer, sending (client side) | 在本機（iOS 在 iPhone） | On this device |
| `received`, `filled`, `entered` | 已到主機 | At the host |
| `queued` | 在佇列 | Queued |
| `read` | no mark; it is a normal message | — |
| `failed` | 送不出：<reason> | Not sent: <reason> |
| `unknown` | 不確定有沒有送到 | Not sure it arrived |

Without `conversations.stages.v1` both Apps keep today's behaviour.

## 2. Phases (one PR each, ≤ 800 lines / 20 files with tests; TDD; codex R1 + R2)

| Phase | Who | Content | Depends on |
|---|---|---|---|
| MS2-0 | daemon member | Measurement only, no code PR; results go into spec §4 through a docs PR | — |
| MS2-1 | daemon member | Stage ledger, transitions, `queued` / `read` matching, keys-ledger text, WS frame, snapshot field, capability | MS2-0 (none for the wire) |
| MS2-2 | daemon member | `/send-now` route and capabilities | MS2-0, MS2-1 (shares the ledger package) |
| MS2-3 | Mac member (olga) | One bubble driven by stages, marks, fallback | the §1.1–1.2 contract (fixtures; may merge before MS2-1, gated by the capability) |
| MS2-4 | Mac member (olga) | Ctrl+Enter, Ctrl+X Ctrl+S, Up on the first line | the §1.3 contract (gated) |
| MS2-5 | iOS member (air26) | Stage marks, hold to send now (D5), double-tap twice to take back (D6), iOS build and OTA | §1 contract (gated by both capabilities) |

### MS2-0 measurement (≈ 1 h)

- In an isolated tmux server: `unset TMUX; tmux -L ms2 …`. Never touch the user's server; at the end `tmux -L ms2 kill-server` only.
- Run a throwaway `claude --model haiku` in a scratch directory. Record the Claude Code version.
- While a turn runs, queue two messages, then measure:
  - **Send-now bytes**: send `\x18\x13`. Record the transcript `queue-operation` rows that follow, and whether the queued messages enter the same turn.
  - **Non-empty input**: repeat with text in Claude Code's input.
  - **Dialog open**: repeat with a permission dialog open.
  - **Empty queue**: repeat with nothing queued.
  - **Ctrl+Enter**: the CSI-u bytes `\x1b[13;5u` and what tmux passes on.
  - **Ctrl+S**: whether Ctrl+S reaches Claude Code, or is eaten as XOFF.
- Output: a table in spec §4 (docs PR), plus the bytes chosen for §1.3.

### MS2-1 tests (each with a mutation that turns it red)

- **Transitions**: `received` → `filled` → `entered` → `read` for fill mode (idle session), and keys mode with the text kept.
- **Busy session**: `entered` → `queued` (header match) → `read` with a `source=queued` item 5 min later (M: keep the 30 s window).
- **Terminal stages**: `failed` carries the reason; `unknown` → `read` by a later match; `recalled` after `/recall`; no stage moves back (M: allow it).
- **Frame**: a stage change pushes `conversation.stages` with no transcript change (M: put it behind the quiet gate). Snapshot `stages` after a reconnect.
- **Capability**: the exact-list test and the real-module `/api/info` test.
- **Ledger expiry**: ids drop 10 min after a terminal stage.

### MS2-2 tests

- **Refusals**: each one in the §1.3 order — `no_owner`, `waiting`, `nothing_queued`, `input_not_empty`, generation, pane uniqueness (M: drop each check).
- **Success**: send-now sends the measured bytes through `SendKeysIfInstanceAndMode`.
- **Replay**: a second call with the same id is answered from the ledger.
- **Routing and capabilities**: the phone may call the route (device-scope test); `send_now` is pushed in capabilities when it changes.

### MS2-3 tests (SPA)

- **One bubble**: a `conversation.stages` frame for `ids[1]` (a busy retry) moves the entry of `ids[0]` (M: key by the latest id).
- **Marks**: each mark text per stage.
- **Handover**: `read` removes the entry with no second copy from `header.queued` (M: keep the header line).
- **Fallback**: without the capability, today's matching still runs (M: require stages).
- **Tab switch**: switching tabs keeps the stages (real `TabContent` test).

### MS2-4 tests (SPA)

- **Send now**: Ctrl+Enter calls send-now and not submit (M: let it fall into Enter). Ctrl+X then Ctrl+S within 1.5 s sends now, after 1.5 s it does not; Ctrl+X alone keeps its cut.
- **Up on the first line**: with a queue it takes back before history (M: history first); with no queue, history as today.
- **Refusals**: each refusal shows its text (`input_not_empty` → 「桌機輸入框有未送出的文字」).

### MS2-5 (iOS)

- Marks from stages.
- D5: a 0.5 s hold starts the fill, which completes 1 s later; release at ≥ 90 % sends now; haptics.
- D6: a double tap arms; a second double tap within 3 s takes back.
- Refusal texts as on the Mac.
- Swift tests on air26.
- Release: `WT=<worktree> zsh build.sh`, `zsh ota.sh <ver> <build>` (`~/.local/share/purdex-ios/` on air26). The OTA swap on mlab is done by the lead.

## 3. Review, release, tests

- Test tiers as CLAUDE.md:
  - Go and SPA PR tiers go to air26 through `~/.local/share/purdex-lead/deploy/assist-verify.sh` and `assist-run.sh`.
  - Swift runs on air26.
- **Daemon PRs** (MS2-1, MS2-2) touch `internal/module/conversation`, `internal/promptq` and `internal/tmux`. They are released by 1f, who deploys them. The ledger stays in memory, so there is no schema change and no migration.
- **SPA PRs** (MS2-3, MS2-4) are released by the B-line lead.
- **iOS** (MS2-5) is released by the B-line lead after the user's on-device try.
- **Deploy together**: MS2-1 and MS2-2 ship in one version on both Macs; `pdx setup` is not needed unless the mod changes. This plan changes no mod file.

## 4. Out of scope

- Mac persistence (L8, to be asked).
- Sends to workers (Nexen executions).
- Stage persistence across daemon restarts (L7).
- Changes to `/submit`'s answer shape.
