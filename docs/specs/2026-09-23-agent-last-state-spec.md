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
