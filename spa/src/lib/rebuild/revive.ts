// spa/src/lib/rebuild/revive.ts — reviving a pane by tmux session name: the
// pure decision, the per-pane gate, and the pass that applies them
// (spec §3.1 / §3.2).
//
// A pane terminated `tmux-restarted` lost its binding, not its identity: tmux
// session names are unique per server, so a live session that reappears under
// the same name is the pane's session again, whatever new generation it now
// carries. `decideRevive` looks for exactly that — by name, never by code,
// since a code is a reversible encoding of `$N` and the restarted server can
// mint the same code for an unrelated session. It requires the live session's
// `code` and `tmux_instance` to be non-empty strings ("no evidence, no
// action", spec §4.6) — not merely truthy, since the payload is `JSON.parse`d
// with no schema and a number or object there is not a binding a pane may be
// put on — but does NOT compare the generation against the candidate's own: a same-generation
// live session is a legitimate recovery target, not a contradiction (a pane
// can be marked `tmux-restarted` merely because its lookup failed once).
// `mode` is not consulted: it is not part of the binding, and since P-D.3
// every live session is attached as a terminal whatever a pre-P-D.2 daemon
// reports there (codex F1 on P-D.3a).
//
// `reviveAllowed` is the last check before a decision is applied: it refuses
// a pane whose current-binding operation is still running (unreachable under
// the pass's own lock guard, kept so the rule stands alone) or already holds
// a `createdSession` — the engine deliberately left that pane un-re-pointed,
// and its panel is showing the report.
//
// `decideRevive` and `reviveAllowed` are pure: no store access, no side
// effects. `runRevivePass` is the one place they meet the stores, and it has
// two triggers with the same evidence: the host's reconciled `sessions`
// payload, and the release of the operation lock — a rebuild in flight owns
// the outcome for every pane it may re-point, and the pass may not act on any
// of them until it is done.
//
// That evidence is a per-host snapshot the handler hands over, NOT
// `useSessionStore.sessions`: `fetchHost` overwrites the store unconditionally
// whenever an HTTP list response lands, and the hook's `onOpen` starts one on
// every connection, so between a payload and the lock release the store can
// come to hold a list OLDER than the payload the gate was opened for. Nothing
// but the handler writes the snapshot, and the attach gate still ties it to
// the current connection — the gate reopens only on that connection's own
// payload, which is also when the snapshot is overwritten.
import { bindingEquals } from './binding'
import { canAttachTerminal } from './attach-gate'
import { repointPane } from './engine'
import { scanPaneTree } from '../pane-tree'
import { useHostStore } from '../../stores/useHostStore'
import { useRebuildStore, type RebuildBinding, type RebuildOperation } from '../../stores/useRebuildStore'
import { useTabStore } from '../../stores/useTabStore'
import type { Session } from '../host-api'

const reconciledSessions = new Map<string, Session[]>()

/** The `sessions` handler's payload for `hostId`, exactly as it was reconciled. */
export function noteReconciledSessions(hostId: string, sessions: Session[]): void {
  reconciledSessions.set(hostId, sessions)
}

/** A terminated pane eligible for revive-by-name — the caller has already
 * filtered for `kind === 'tmux-session'` and
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
    // The payload is `JSON.parse`d with no schema, and a binding needs a real
    // code and a real generation — `null` or a number would clear `terminated`
    // and point the terminal at nothing. `name` needs no check: it only ever
    // matched a string the pane already held.
    if (typeof session.code !== 'string' || session.code.length === 0) continue
    if (typeof session.tmux_instance !== 'string' || session.tmux_instance.length === 0) continue

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

/** Every `tmux-restarted` terminal pane on `hostId`, as the pass sees it now. */
export function collectCandidates(hostId: string): ReviveCandidate[] {
  const candidates: ReviveCandidate[] = []
  for (const tab of Object.values(useTabStore.getState().tabs)) {
    scanPaneTree(tab.layout, (pane) => {
      const c = pane.content
      if (c.kind !== 'tmux-session' || c.hostId !== hostId) return
      if (c.terminated !== 'tmux-restarted') return
      candidates.push({
        hostId, tabId: tab.id, paneId: pane.id,
        sessionCode: c.sessionCode, tmuxInstance: c.tmuxInstance, cachedName: c.cachedName,
      })
    })
  }
  return candidates
}

/**
 * Revive every eligible pane on `hostId` against the host's last reconciled
 * payload (spec §3.2). Synchronous: nothing runs between the candidate scan
 * and the write, so the binding a decision was made from is the binding the
 * write lands on.
 *
 * The attach gate ties the evidence to the CURRENT connection — it reopens
 * only after that connection's own payload has been reconciled, so a pass can
 * never act on a list left over from a dropped one. The lock guard is global:
 * a batch records an operation entry only for each group's source pane, so
 * the lock is the one thing every pane a rebuild will re-point is under.
 *
 * Each write is isolated. On the release trigger this runs synchronously
 * inside `releaseOperationLock`'s `set`, i.e. inside the `finally` of the
 * rebuild that is releasing: a throw from the persisted tab store (quota on
 * `localStorage.setItem`) would become THAT rebuild's rejection and stop the
 * remaining panes. A failed revive just leaves its pane for the next trigger.
 */
export function runRevivePass(hostId: string): void {
  if (!canAttachTerminal(hostId)) return
  if (useRebuildStore.getState().lockedBy !== null) return
  const sessions = reconciledSessions.get(hostId) ?? []
  for (const d of decideRevive(hostId, sessions, collectCandidates(hostId))) {
    if (!reviveAllowed(d.paneId, d.binding, useRebuildStore.getState().operations)) continue
    try {
      repointPane(d.tabId, d.paneId, d.session)
    } catch { /* ignore */ }
  }
}

/** The lock-release trigger: the lock is global, so every host gets a pass —
 * and one host's failure may not cost the others theirs, for the same reason
 * a pane's may not. */
export function runRevivePassAll(): void {
  for (const hostId of useHostStore.getState().hostOrder) {
    try {
      runRevivePass(hostId)
    } catch { /* ignore */ }
  }
}
