# Spec — the pane's last agent state in the rebuild record

Status: approved by the user 2026-09-23 (design items 1–4; the Rebuild-all preview table is parked as #1379).

## Why

A tab's rebuild record (`PaneRebuildRecord`, `spa/src/types/tab.ts`) learns the agent on its root SessionStart and
never forgets it. A user who quit claude and kept using the pane as a shell still gets `claude --resume` on Rebuild.
The user wants to SEE the pane's last state at Rebuild time — an agent was running, or it had exited back to a
shell — and decide how far to rebuild.

## Facts this rests on (measured on alpha.437)

- Exit signals exist: cc `SessionEnd`, codex `SessionEnd` (+ its process-dead probe), opencode has NO quit hook
  (`session.deleted` only) — its quit is seen by the daemon's 2 s pid sweep (`sweep:pid_dead`). The sweep also
  catches crashes / kills of every agent.
- What reaches the SPA today is a SESSION-level projection `status: 'clear'` with no identity; another pane's agent in
  the same tmux session masks it; `useAgentStore` returns on `clear` before any record write
  (`useAgentStore.ts:198-200`). So nothing is recorded now.
- Host crash / daemon down / pane gone: no end signal at all — the record's last state stays "agent running".

## Design

1. **Daemon — an exit envelope.** When a ROOT frame ends — SessionEnd deleting a frame with `ParentFrameID == ""`
   (not a proxy detach) in `frame_ops.go`, or the sweep clearing a root frame for `pid_dead` / `pid_reused` — the
   broadcast carries `detail.pdx_exit = { agent_type, session_id, tmux_pane_id, tmux_instance, reason:
   'session-end' | 'process-dead', at }`, built from the frame BEFORE it is deleted. Mirrors `pdx_provenance`
   (SessionStart's root gate). When the sweep cannot resolve the pane's session (pane gone), nothing is sent — as today.
2. **SPA — record field.** `PaneRebuildRecord.agentExited?: { at: number; reason: 'session-end' | 'process-dead' }`.
   Absent = "the agent was running when last seen". `useAgentStore` parses `pdx_exit` BEFORE the `clear` early
   return and applies a new patch `{ kind: 'agent-exit', … }` through `setPaneRebuild`. `applyRebuildPatch` sets
   the field only when the record's `agent` matches type, `sessionId` and `tmuxPaneId` (an exit of another pane's
   agent, or of an older agent, changes nothing). The agent identity is KEPT ("resume anyway" stays possible;
   the pane does not become backfill-eligible again). Every writer that establishes a live agent clears the field:
   `agent-group` (by construction) and every backfill mode. Terminated panes stay frozen (session-scoped writes
   already skip them). cc `/clear` = SessionEnd then SessionStart → set then cleared, in order.
3. **Rebuild UI.** Per pane (`RebuildActionSet`): show the last state — "‹Agent› running when last seen (‹time›)" /
   "‹Agent› exited ‹time› (normal exit | process gone)" / nothing for a shell-only record — and default the
   resume checkbox from it: `override.runResume ?? (!record.unverified && !record.agentExited)`; the user can still
   tick it. Rebuild all (`batch.ts` `planForRecord`): `&& !record.agentExited` — no other change (#1379 parked).
   Times in the UI language.
4. **Sync.** The field travels inside `tabs.*.layout` with no projection change. It is additive and optional; an old
   client ignores it and drops it on its next SessionStart write (which means "running" anyway) — no ordinal bump.

## Acceptance (real machine)

A pane runs claude → Rebuild panel (after killing the session) shows "running when last seen", resume ticked.
Quit claude with `/exit`, kill the session → "exited (normal exit)", resume unticked, ticking it still resumes.
Kill the claude process (`kill -9`) → within ~2 s "exited (process gone)". Two panes in one tmux session, quit
the agent in one → only that pane's record changes. codex and opencode (quit → process gone) once each.

## Review decisions (codex `task-mue3j3jb-8rzorx`, 8 findings, all taken) — these override the text above

1. **Sync: bump the tabs ordinal and add a tabs wire marker** (a new value domain inside `layout`; the rule in
   `projections.ts` asks for it, and `compareShape` ignores ordinals on equal fingerprints). Another PR (purdex-d4,
   local-only tabs) is bumping tabs 2→3 concurrently — whichever merges second takes the next number; test old/new
   coexistence (old locks `schema`, new does not ping-pong).
2. **An exit applies to exactly one agent run: match by FRAME ID, not by type + sessionId + paneId.** The SessionStart
   provenance envelope and the exit envelope both carry the daemon's frame id (add it where missing); the record
   stores it (`agent.frameId`); an exit applies only when `exit.frameId === record.agent.frameId`. A late exit of an
   old run with the same session id (`/resume`, restart) can then never mark the new run exited — no clock ordering
   needed. Records written before this change have no frameId → exits never apply to them (they stay "running when
   last seen") until their next SessionStart.
3. **Exit while the SPA is disconnected: accepted limitation, stated in the UI copy.** The envelope is broadcast once;
   the snapshot replays only live frames. The record keeps "running when last seen" — which is literally true. No
   negative inference from the snapshot.
4. **An exit may update a TERMINATED pane's record** (only `agentExited`, only on frame-id match): termination can
   arrive before the SessionEnd / sweep broadcast. Every other session-scoped write still skips terminated panes.
5. **`at` is Unix milliseconds on the daemon's clock**, used for display only (never for ordering — 2. makes ordering
   unnecessary). Formatted in the UI language.
6. **A live agent answer clears it:** backfill must treat an `agentExited` record as probe-eligible, and every backfill
   mode that confirms a live frame (incl. the same identity) clears `agentExited` and adopts the answer's frameId.
7. **Sweep: build the envelope BEFORE the frame is deleted** (`clearFrame` snapshots the frame, passes the envelope to
   `afterFrameCleared`); tests prove it survives the delete; root only (child / proxy frames send none), `pid_dead`
   and `pid_reused`.
8. **Batch:** the `agent-exit` patch re-stamps `capturedAt`, so the batch's newest-record winner sees the exit.

## Plan (two PRs, one branch; daemon first)

PR A — daemon (Go): (a) frame id in the SessionStart provenance envelope (if absent); (b) `pdx_exit` on the root
SessionEnd path in `frame_ops.go` (`ParentFrameID == ""`, not a proxy detach); (c) the same from the sweep for
`pid_dead` / `pid_reused`, built before `Delete`. Tests per agent (cc / codex / opencode sweep path) × root / proxy /
native subagent; old SPA ignores the new detail (additive).
PR B — SPA: (a) `PaneRebuildRecord.agent.frameId`, `agentExited?: {at, reason}`, patch `agent-exit` with frame-id
match, allowed on terminated panes, re-stamps `capturedAt`; `agent-group` and backfill clear it; backfill eligibility;
(b) `useAgentStore` parses `pdx_exit` before the `clear` early return; (c) `RebuildActionSet` state line + default,
`batch.ts` default; i18n en + zh-TW; (d) tabs WIRE_MARKER + ordinal bump + coexistence test. Tests: frame-id match
vs another pane / older run / missing frameId; exit→terminate and terminate→exit; `/clear` sequence; UI default and
manual override; batch mixed running/exited.
Real machine: the acceptance above, after PR A is deployed on mlab.
