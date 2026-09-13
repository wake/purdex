// spa/src/lib/rebuild/revive.ts — pure decision for reviving a pane by tmux
// session name, and the per-pane gate that guards it (spec §3.1 / §3.2).
//
// A pane terminated `tmux-restarted` lost its binding, not its identity: tmux
// session names are unique per server, so a live session that reappears under
// the same name is the pane's session again, whatever new generation it now
// carries. `decideRevive` looks for exactly that — by name, never by code,
// since a code is a reversible encoding of `$N` and the restarted server can
// mint the same code for an unrelated session. It requires the live session's
// `tmux_instance` to be non-empty ("no evidence, no action", spec §4.6) but
// does NOT compare it against the candidate's own generation: a same-generation
// live session is a legitimate recovery target, not a contradiction (a pane
// can be marked `tmux-restarted` merely because its lookup failed once). A
// `mode` key that is present but not `'terminal'` is rejected — and `null`
// counts as present, because the handler `JSON.parse`s the payload with no
// schema, so an unexpected shape must fail closed.
//
// `reviveAllowed` is the last check before a decision is applied: it refuses
// a pane whose current-binding operation is still running (unreachable under
// the pass's own lock guard, kept so the rule stands alone) or already holds
// a `createdSession` — the engine deliberately left that pane un-re-pointed,
// and its panel is showing the report.
//
// Both functions are pure: no store access, no side effects.
import { bindingEquals } from './binding'
import type { Session } from '../host-api'
import type { RebuildBinding, RebuildOperation } from '../../stores/useRebuildStore'

/** A terminated pane eligible for revive-by-name — the caller has already
 * filtered for `kind === 'tmux-session'`, `mode === 'terminal'`, and
 * `terminated === 'tmux-restarted'`. */
export interface ReviveCandidate {
  hostId: string
  tabId: string
  paneId: string
  /** The dead binding — what the pane must still hold when the write lands. */
  sessionCode: string
  tmuxInstance: string
  /** The name the pane last saw for its session. */
  cachedName: string
}

export interface ReviveDecision {
  tabId: string
  paneId: string
  binding: RebuildBinding
  session: Session
}

/**
 * Decide which candidates should revive against which live session, from one
 * `sessions` payload. First entry wins on a duplicate name.
 */
export function decideRevive(hostId: string, sessions: Session[], candidates: ReviveCandidate[]): ReviveDecision[] {
  const byName = new Map<string, Session>()
  for (const s of sessions) {
    if (!byName.has(s.name)) byName.set(s.name, s)
  }

  const decisions: ReviveDecision[] = []
  for (const candidate of candidates) {
    if (candidate.hostId !== hostId) continue

    const session = byName.get(candidate.cachedName)
    if (!session) continue
    if (!session.tmux_instance) continue
    if (session.mode !== undefined && session.mode !== 'terminal') continue

    decisions.push({
      tabId: candidate.tabId,
      paneId: candidate.paneId,
      binding: { hostId, sessionCode: candidate.sessionCode, tmuxInstance: candidate.tmuxInstance },
      session,
    })
  }
  return decisions
}

/**
 * The per-pane gate applied right before a decision is written (spec §3.2).
 */
export function reviveAllowed(paneId: string, binding: RebuildBinding, operations: Record<string, RebuildOperation>): boolean {
  const op = operations[paneId]
  if (!op) return true
  if (!bindingEquals(op.binding, binding)) return true // op describes an earlier cycle of this pane
  if (op.status === 'running') return false // unreachable under the lock guard; kept so the rule stands alone
  if (op.createdSession) return false
  return true
}
