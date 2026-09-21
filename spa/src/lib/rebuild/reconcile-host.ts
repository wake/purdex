// spa/src/lib/rebuild/reconcile-host.ts — applying one host's `sessions` payload
// to the panes on screen: the verdict (reconcile.ts), the store writes it asks
// for, the attach gate, the revive pass and the two probe sweeps, in the one
// order they are safe in.
//
// This was an inline block of the WS `sessions` handler in
// `useMultiHostEventWs`. It is a function so that the handler is not the only
// thing that can run it; it reads nothing of the hook (no ref, no previous
// payload) — only `hostId`, the payload, and the stores.
import { useAgentStore } from '../../stores/useAgentStore'
import { usePathCacheStore } from '../../stores/path-cache/usePathCacheStore'
import { useSessionStore } from '../../stores/useSessionStore'
import { useTabStore } from '../../stores/useTabStore'
import type { Session } from '../host-api'
import { scanPaneTree } from '../pane-tree'
import { openAttachGate } from './attach-gate'
import { probeMissingCwds } from './cwd-probe'
import { probeSessionProvenance } from './provenance-probe'
import { reconcileSessionsPayload, type ReconcilePane } from './reconcile'
import { noteReconciledSessions, runRevivePass } from './revive'

/**
 * The distinct live terminal-pane bindings on `hostId`, optionally narrowed to
 * one session code.
 *
 * Both provenance triggers — the sweep below and the hook-event one in
 * `useMultiHostEventWs` — go through here so they ask with the
 * generation the PANE RECORDED — never one read off the event that woke them.
 * The probe keys its in-flight set, its cooldown and its disowned set by
 * `(hostId, sessionCode, tmuxInstance)`, so a trigger that invented a different
 * generation would open a second binding and slip past all three.
 *
 * Eligibility itself is not decided here: `probeSessionProvenance` owns the
 * "does this pane still want an answer" rule (spec §5.4), and duplicating it at
 * the call site is how the cwd probe's two rules once drifted apart.
 */
export function provenanceBindings(
  hostId: string,
  sessionCode?: string,
): Array<{ sessionCode: string; tmuxInstance: string }> {
  // NUL separates the parts of the dedup key: it cannot occur in a session code
  // or an instance stamp, so no two distinct pairs collide. Written as the
  // `\u0000` escape rather than as a raw byte — a literal NUL makes the file
  // count as binary, which puts it out of grep's reach.
  const bindings = new Map<string, { sessionCode: string; tmuxInstance: string }>()
  for (const tab of Object.values(useTabStore.getState().tabs)) {
    scanPaneTree(tab.layout, (pane) => {
      const c = pane.content
      if (c.kind !== 'tmux-session' || c.terminated) return
      if (c.hostId !== hostId) return
      if (sessionCode !== undefined && c.sessionCode !== sessionCode) return
      bindings.set(`${c.sessionCode}\u0000${c.tmuxInstance}`, {
        sessionCode: c.sessionCode, tmuxInstance: c.tmuxInstance,
      })
    })
  }
  return [...bindings.values()]
}

/**
 * Reconcile the panes on screen against `hostId`'s `sessions` payload. Throws
 * what the stores throw; the caller decides what a failure costs.
 */
export function reconcileHostSessions(hostId: string, data: Session[]): void {
  // Decide BEFORE mutating anything (spec §4.5): the verdict must
  // read the panes as they were when the payload arrived, not
  // after a name refresh has already touched them.
  const panes: ReconcilePane[] = []
  for (const tab of Object.values(useTabStore.getState().tabs)) {
    scanPaneTree(tab.layout, (pane) => {
      const c = pane.content
      if (c.kind === 'tmux-session' && c.hostId === hostId && !c.terminated) {
        panes.push({ hostId: c.hostId, sessionCode: c.sessionCode, tmuxInstance: c.tmuxInstance })
      }
    })
  }
  const outcome = reconcileSessionsPayload({ hostId, sessions: data, panes })

  useSessionStore.getState().replaceHost(hostId, data)
  noteReconciledSessions(hostId, data)

  // Adoption first — a pane that takes the live generation here is
  // no longer matched by a sibling's tmux-restarted decision.
  for (const { sessionCode, tmuxInstance } of outcome.adoptInstance) {
    useTabStore.getState().adoptTmuxInstance(hostId, sessionCode, tmuxInstance)
  }
  for (const s of data) {
    useTabStore.getState().updateSessionCache(hostId, s.code, s.name, s.tmux_instance ?? '')
  }

  const clearedCodes = new Set<string>()
  for (const { sessionCode, expectedTmuxInstance, reason } of outcome.terminate) {
    useTabStore.getState().markTerminatedForGeneration(hostId, sessionCode, expectedTmuxInstance, reason)
    if (reason !== 'session-closed' || clearedCodes.has(sessionCode)) continue
    clearedCodes.add(sessionCode)
    // Clear agent state (subagents, status, etc.) so indicators
    // don't linger after the tmux session disappears.
    useAgentStore.getState().clearSession(hostId, sessionCode)
    // Path-cache entries tagged with this sessionCode are now
    // dead (their owning agent session is gone); other sessions
    // sharing the same cwd keep their entries. SessionCode is
    // host-local so we must scope the clear by hostId — sibling
    // hosts can mint identical codes (R3 P2).
    usePathCacheStore.getState().clearBySession(hostId, sessionCode)
  }

  // This connection has now told us which generation is live and
  // the panes have been reconciled against it: terminals bound to
  // this host may attach (spec §4.6). A payload we could not parse
  // is no evidence — the caller parses, and does not get here.
  openAttachGate(hostId)

  // Before the probes, so a revived pane is probed on its final binding.
  runRevivePass(hostId)

  // First of the two cwd-probe triggers (spec §4.4). Runs after
  // reconciliation so a pane about to be marked dead, or one that
  // just adopted this generation, is judged on its final binding.
  probeMissingCwds(hostId)

  // First of the three provenance triggers (spec §5.4). Same
  // placement and same ordering as the cwd sweep above: a sweep
  // before reconciliation would ask with a generation the SPA has
  // not adopted.
  for (const { sessionCode, tmuxInstance } of provenanceBindings(hostId)) {
    probeSessionProvenance(hostId, sessionCode, tmuxInstance)
  }
}
