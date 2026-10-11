# U4-1 — deck view for worker tabs — plan

Spec: `docs/specs/2026-10-11-interface-u4-spec.md` (§3.1–3.3, §4 U4-1). One PR ≤ 800 lines incl. tests, daemon + SPA; if the
daemon half grows past ~350 lines, split D1–D3 and S1–S4 into two PRs (daemon first; the SPA tolerates the old header).

## Goal
A worker tab's view menu offers 「指揮台」 next to room / chat. Picking it shows the deck on the execution's Claude Code
conversation (the transcript the probe already opens), with the worker's light as the deck's status and the existing worker input as
the footer. Room stays the default; nothing else about worker tabs changes.

## Daemon
**D1 Execution ownership seen by the conversation module.** The nex module exposes, through the registry (new key
`nex.conversation-owner`, soft dependency like `nex.worker-notify-feed`), `LiveWorker(ctx, sid) (ExecOwner, bool, error)` built on
the existing `liveWorkersFor` (newest live execution for that session id) and returning `{ExecID, State, Light}` where `Light` is the
worker light projected by the same rule the push classifier uses (`projectWorker`, worker_classify.go: running / waiting / idle /
error). Cache the answer per sid for a short time (e.g. 2 s) so the follow tick does not scan Nexen on every tick.
**D2 Header.** When the resolver finds no live tmux owner for the sid and D1 reports a live worker: `header.backend = "execution"`,
`header.status = Light`, `header.live = true`. A terminated / archived execution → today's behaviour (`ended`). Refresh on the
existing follow tick (≤ 0.5 s latency is fine) and bump the revision only when backend / status / live changed (same pattern as
`queued`). The nex module absent → today's behaviour.
**D3 Capabilities.** `capabilitiesFor(sid)` for an execution-owned sid: `send: "execution"`, `interrupt: "execution"`,
`answer_permission: "execution"`, `background_tasks: "execution"`; reasons for the rest as today. The terminal path (`prompt`) is
unchanged. (U4-1's SPA does not send through the conversation API; these values tell clients which adapter to use from U4-2 on.)

## SPA
**S1 View mode.** `ExecutionViewMode` gains `'deck'`; the worker tab's view menu (ViewModeMenu / status-bar mode buttons, whichever
the worker tab uses) lists 「指揮台」; the mode stays on the pane content (persisted, no remount on switch — existing rule).
**S2 Render.** In `ExecutionPaneWrapper`, `mode === 'deck'` renders a `WorkerDeckPane` instead of ExecutionView:
- the session id = the execution row's `session_id` (from the store the worker tab already fills; `resume_session_id` while the
  first turn has not reported one); none yet → a placeholder with the lifecycle state (queued / launching / rejected + reason),
  reusing ExecutionView's existing strings;
- the deck = the existing `DeckPane` bound to `(hostId, sid)` through the conversation store (`acquire` / release like
  `useConversationOfPane`), with its panel split, scroll memory and fold memory;
- the footer = the existing `WorkerInput` with `useExecutionActions` (lease, send, interrupt, uploads, worker draft memory),
  mounted under the deck like `SessionInput` is for terminals; the session status row shows model / effort from the header and
  the execution's `cost_usd` in the cost slot.
**S3 Lights / tabs.** Unchanged (row-based `exec-<id>` light).
**S4 Tab-hosted rule.** Switching away and back keeps the view mode, the deck scroll position (deck's own memory, keyed by pane)
and the worker draft (worker-draft-memory). Regression test with the real `TabContent` (CLAUDE.md checklist).

## Tests (TDD)
- Daemon: D1 newest-live selection, terminated/archived ignored, cache; D2 header backend/status/live for an execution-owned sid,
  revision bump only on change, nex absent → unchanged, terminal-owned sid unchanged; D3 capabilities per backend. `go test -race`
  on the affected packages only.
- SPA: the menu lists 指揮台; deck mode renders the deck for the row's session id; placeholder before a session id; WorkerInput in
  the footer sends through the worker path (mocked); the status row shows cost; tab switch keeps mode / scroll / draft; room / chat
  unchanged. Full vitest (--maxWorkers=3) + `tsc -b` before merge.

## Out of scope (later phases)
SessionInput for workers (U4-2), permission dock card (U4-3), live line from Nexen events / background tasks / lifecycle rows /
chips / handoff control (U4-4), deck as default and removal of ExecutionView (U4-5).

## Acceptance
On mlab: open an existing worker tab → view menu → 指揮台 → the conversation shows (turns, steps, status dots), the status row
shows model and cost, sending from the footer reaches the worker and the reply appears in the deck; switch tabs and back → same
place; switch to room → the old view as before. Screenshots to air26:~/Downloads/ (prefix u4-1-).
