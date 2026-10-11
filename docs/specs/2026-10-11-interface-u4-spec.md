# U4 — worker (Nexen execution) tabs use the deck / chat interface — spec (draft 2026-10-11)

## 1. User decisions (do not reopen)
- The interface layer is **deck / chat only**; terminal and worker (執行體, a Nexen execution) are back-end implementations under
  it. A capability a back-end lacks is hidden or replaced by that back-end's own version (e.g. a worker's draft memory is its own).
  (2026-10-11)
- The deck shows what the terminal shows (2026-10-11); Mac and iOS share the rules (`docs/specs/2026-10-11-deck-shared-presentation-rules.md`).
- The view buttons only switch views; a worker has no terminal view button; handing off / taking back is a separate control
  (2026-10-08, kept by U3).
- The old worker UI branches (`worktree-merge-3-worker-ui`) are not revived; their ideas map to deck parts (2026-10-11).

## 2. Facts (measured 2026-10-11)
- A worker runs `claude -p`; its transcript is a standard Claude Code JSONL under `~/.claude/projects/<slug>/<sid>.jsonl`
  (execution row `session_id`, `transcript_path`). **Probe:** `GET /api/conversations/claude/<worker sid>` on mlab returns 200 with
  the turns (status `ended`, capabilities = transcript-only, `send`/`interrupt` not wired) — the conversation pipeline (convfeed
  Resolver by sid → ccnorm) already reads worker transcripts. ccnorm already knows SDK-entrypoint users and the terminal↔execution
  `handoff` system item.
- Worker profiles run with `--setting-sources ""`, so the Purdex mod does not load: no mod stream (no step-level live streaming, no
  `promptq` owner). Live per-message updates come from the transcript (`text_streaming: "message"`).
- Today's worker tab: `kind:'execution'` → ExecutionView (room / chat), Nexen data via `/api/nex/v1/*` (row, SSE events, tasks,
  prelude, permission channel), send = `POST /v1/executions/{id}/messages` with a control lease, interrupt `POST …/interrupt`,
  uploads `/api/nex/executions/{id}/uploads`, light from the execution list row (`exec-<id>` key).
- The deck: SessionPaneContent → useConversationOfPane (tmux code → provenance → sid) → DeckPane / ChatPane; send =
  `POST /api/conversations/claude/{sid}/submit` (mod only); `capabilities.send !== 'prompt'` is the only capability the SPA reads.

## 3. Design
**3.1 Data: transcript first, Nexen fills the worker-only parts.** A worker tab opens the conversation of its execution's
`session_id` through the existing conversation API and WS. Before the execution has a session id (queued / launching) the tab shows
the lifecycle state (3.5) instead of the deck. Worker-only facts come from the Nexen side the App already has (row, events, tasks,
permission channel) and are drawn as deck parts, never as a second transcript.

**3.2 Backend in the header.** The conversation module learns that a sid is owned by a live execution (the nex module's
`latest_execution_id` / `last_in` data): `header.backend = "execution"`, `header.status` = the worker light projected from the row
(running / waiting / idle / error — the same projection the lights use), `live` = the execution is not terminal.

**3.3 Capabilities per backend.** `capabilitiesFor(sid)` returns, for an execution-owned sid: `send: "execution"`,
`interrupt: "execution"`, `answer_permission: "execution"`, `background_tasks: "execution"`, `usage: "execution"`; the SPA picks the
send / interrupt / dock adapter by these values (today it only checks `send === 'prompt'`). Terminal keeps `prompt`.

**3.4 Send, interrupt, uploads, drafts.** Worker adapter: `ensureLease` → `POST /v1/executions/{id}/messages` (busy → Nexen
queues; the deck shows it as the App-held grey message until it appears in the transcript), interrupt via Nexen, uploads via the
worker upload route (inserted as paths like the terminal's), draft memory = `worker-draft-memory` (own, per host+execution).
The control lease is held while the tab is open (today's behaviour).

**3.5 Worker-only rows (deck parts).**
- Permission requests → a dock card (same place as asks), answered through the permission channel; aligned with P8b's future
  approval cards.
- Live line: running tool from Nexen events when present (`✻ 執行中 · {tool}`), else `✻ 思考中…`; background tasks
  (`task_start`/`task_end`) as `· {n} 個背景工作` (same wording the terminal will use when #2592 lands).
- Lifecycle: queued / launching / rejected (reason) / failed / terminated (reason) as system rows and an ended footer (today's
  WorkerEndedPane content).
- Cost: in the session status row's cost slot (row `cost_usd`).
- Header chips (stream state / observers / lease): in the session status row, right side, small.
- Handoff controls (take to terminal / exit): the separate control U3 defined.

**3.6 Removal.** When the deck covers the above, ExecutionView (room / chat), prelude / stints and their stores are removed (the
transcript already spans terminal and worker stints, with `handoff` items).

## 4. Phases (each ≤ 800 lines incl. tests)
The worker tab keeps working at every step: the deck arrives as an **extra view** first and becomes the default only when it
covers what ExecutionView does.
- U4-0 probe & contract: this spec; the probe (§2); confirm sidechain / peer-mailbox rows of `-p` transcripts render (fixtures).
- U4-1 deck view for worker tabs: the worker tab's view menu gains 「指揮台」 (ExecutionView's room / chat stay, room stays the
  default); it renders the deck on the execution's `session_id` (lifecycle placeholder until there is one); daemon: header
  backend / status / live for an execution-owned sid, capabilities per backend (3.2, 3.3); the footer is the existing
  `WorkerInput` (worker send path, own draft memory), so the view is usable from day one.
- U4-2 the deck footer becomes `SessionInput` with the worker adapter (send / interrupt / uploads / drafts / App-held grey messages,
  3.4), replacing `WorkerInput` in that view.
- U4-3 permission dock card.
- U4-4 worker-only rows: live line from events, background tasks, lifecycle rows, cost, chips, handoff control.
- U4-5 the deck becomes the worker tab's default and its chat view is the conversation chat; ExecutionView (room / chat) and the
  prelude / stints path are removed.
- iOS follows the same rules per phase (it already has a worker view; aligns to the deck rules after U4-1).

## 5. Open questions
- Q1 (Nexen): load the Purdex mod in workers (`--plugin-dir`, a controlled exception to `--setting-sources ""`) for step-level live
  streaming? Not needed for U4-1…4; decide with the Nexen side when live step streaming is wanted.
- Q2: who holds the control lease when the same worker is open on Mac and phone (today: whoever opens; keep, show the holder chip).
