# U4-1 — deck view for worker tabs — plan (rev 2, after the spec+plan review)

Spec: `docs/specs/2026-10-11-interface-u4-spec.md` (§3.1–3.3, §4 U4-1). **Two PRs**: U4-1a daemon, then U4-1b SPA (the SPA
tolerates an old daemon: no `backend: "execution"` → the deck simply shows status `ended`).

## Goal
A worker tab's view menu offers 「指揮台」 next to room / chat. Picking it shows the deck on the execution's Claude Code
conversation, with the worker's light as the deck's status and the existing worker input as the footer. Room stays the default.

## Review decisions (rev 2)
- C1 The light needs `pending_permission`, which `store.Execution` (owner scan) lacks → the light comes from the nex projector's
  **latest row digest for that execution** (in memory, the same digest the push classifier uses) through `projectWorker`; only when
  the projector has no digest yet does it fall back to the row state (running / idle / error, never waiting).
- I1 Header status must not wait for the 10 s re-resolve → an execution counterpart of `cheapLight`: on every follow tick, an
  execution-owned entry reads the light from the D1 cache and feeds `Refresh` with `StatusAt` = the query time (monotonic).
- I2 convfeed must not import nex/module: `convfeed.Owner` gains `Backend` and `Status`; module/conversation defines
  `ExecOwnerSource`, fetched from the Registry **at call time** (soft, no Dependencies change, like `nex.worker-notify-feed`);
  nex registers `nex.conversation-owner` and imports nothing from conversation. Resolve order: terminal owner first; a terminal
  lookup error is not turned into a live execution; an execution owner supplies its row's `transcript_path`.
- I3 One 2 s cache per sid serves header, cheap light and capabilities; `liveWorkersFor` is an in-process store query (no HTTP).
  New capability values (`execution`) are an added enum: clients treat unknown values as "not supported" (fail closed); tests pin it.
- I4 SPA wiring: extract `useHeldConversation(hostId, sid)` from `useConversationOfPane` (acquire / release / select) and use it
  from a new `useExecutionConversation`; `DeckPane` / `DeckView` / `ChatView` / `DeckUnreadable` take `onSwitchToTerminal` as
  optional and hide 「切到終端機」 when absent (a worker has no terminal view); the status row takes `model` / `cost` overrides
  (worker: model from the header usage, cost from the row's `cost_usd`); `retireStaleSessions` runs when the sid changes (the
  first turn reports it).
- I5 One worker view mounted at a time, sharing one hook: extract **`useWorkerSession(hostId, executionId, isActive)`** from
  ExecutionView (pure move of `useExecutionSubscription`, `useExecutionLease` with its permission hold, `useWorkerUploads`,
  `useNexHostStore.ensure`, `usePermissionAnswer`, `registerTakeToTerminal`), called by ExecutionView and by `WorkerDeckPane`.
  Switching room ↔ chat stays remount-free as today; switching to / from 指揮台 remounts and re-subscribes (documented change of
  the invariant comment; no double lease, no double SSE).
- M1 Both mode entries learn `deck` (`PaneModeButtons` group and `ViewModeMenu` `VIEW_MODES`); `WorkerDeckPane` carries its own
  small view menu in its status row so a narrow window can go back; `viewModeOf` knows `'deck'` (an older build reading `'deck'`
  falls back to room — tested). Known limit until U4-4: take-to-terminal is not offered from the deck view.
- M3/M4 iOS regression item in Acceptance; a test that a live execution-owned conversation's last turn still closes (the `-p`
  transcript's turn end converges with `SetLive`); `-p` fixtures (sidechain, peer-mailbox rows) rendered by ccnorm as a first task.

## U4-1a daemon
1. Fixtures: a synthetic `-p` worker transcript (SDK entrypoint user rows, a sidechain subagent, a peer-mailbox row, a result
   tail) normalises as expected (golden).
2. nex: `nex.conversation-owner` → `LiveWorker(ctx, sid) (ExecOwner{ExecID, Light, TranscriptPath, Live}, ok, err)` — newest live
   execution via `liveWorkersFor`, light via the projector digest (C1), 2 s cache per sid.
3. convfeed: `Owner{…, Backend, Status}`; Resolve keeps terminal first, then execution (I2); `Header.Backend` from the owner.
4. conversation: `ExecOwnerSource` from the Registry at call time; execution `cheapLight` per tick (I1); `capabilitiesFor` per
   backend (I3): `send`/`interrupt`/`answer_permission`/`background_tasks` = `execution`.
5. Tests: newest-live selection, terminated / archived ignored, cache hit / expiry, light with and without a digest (cases from
   `testdata/worker-status-cases.json`), header backend / status / live, revision bump only on change, terminal first, terminal
   error not overridden, nex absent → unchanged, caps per backend, last turn closes for a live execution-owned conversation.
   `go test -race` on affected packages; `go vet ./...`.

## U4-1b SPA
1. `useWorkerSession` extraction from ExecutionView (pure move, byte-compare the moved declarations; ExecutionView behaviour
   unchanged; its tests stay green).
2. `useHeldConversation` extraction + `useExecutionConversation(hostId, executionId)` (sid = row `session_id`, else
   `resume_session_id`; none → placeholder state).
3. `'deck'` view mode: `viewModeOf`, `PaneModeButtons`, `ViewModeMenu`; `ExecutionPaneWrapper` renders `WorkerDeckPane` for it.
4. `WorkerDeckPane`: `useWorkerSession` + `useExecutionConversation`; placeholder with the lifecycle state before a sid (existing
   strings); `DeckPane` with optional `onSwitchToTerminal` absent; footer = `WorkerInput` (worker send path, worker draft memory)
   + status row with model / cost overrides and the small view menu; `retireStaleSessions` on sid change.
5. Tests: menus list 指揮台; deck mode renders the deck for the row's sid; placeholder → deck when the sid appears; no 「切到終端機」;
   WorkerInput sends through the worker path (mocked); status row cost; tab switch keeps mode / scroll / draft (real `TabContent`);
   room ↔ chat still remount-free; an old build's `'deck'` → room. Full vitest (--maxWorkers=3) + `tsc -b`.

## Out of scope
SessionInput for workers (U4-2), permission dock card (U4-3), live line from Nexen events / background tasks / lifecycle rows /
chips / take-to-terminal from the deck (U4-4), deck as default and removal of ExecutionView (U4-5). Lease when the same worker is
open on Mac and phone: unchanged (whoever opens holds it).

## Acceptance
On mlab (after U4-1a is deployed): an existing worker tab → 指揮台 → the conversation (turns, steps, status dots), status follows
the worker within ~1 s (running / waiting / idle), the status row shows model and cost, sending from the footer reaches the worker
and the reply appears; tab switch and back → same place; back to room (also from a narrow window) → the old view; a queued worker
→ placeholder, then the deck once the session id exists; daemon restart → status recovers. iOS: an execution-owned conversation
(new `backend` / capability values) does not break the iOS deck. Screenshots to air26:~/Downloads/ (prefix u4-1-).
