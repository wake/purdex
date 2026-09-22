// spa/src/lib/rebuild/ws-sessions.ts — one WS `sessions` frame of a host's
// CURRENT host-events socket (#1255 SPA spec §3.3). Frames of a closed or
// superseded socket never get here (lib/host-events.ts drops them).
//
// A versioned frame (new daemon) is reconciled only when it is newer than the
// list last reconciled for the host — a stale one is NEVER reconciled, gate open
// or closed (codex #2): under the daemon contract a new connection cannot
// legitimately deliver one while its gate is closed, so dropping it leaves the
// gate closed until the connection's next frame.
//
// The version is CLAIMED before the reconciliation runs (codex adversarial F2,
// replacing "held only after it returned", codex #4): `reconcileHostSessions`
// is not transactional — it may have written part of the list (a
// `session-closed`, a re-pointed pane) before it throws — so an older list
// must never be let in after it. A throw asks for a fresh refresh on this
// connection instead (`recoverHostSessions`, refresh-sessions.ts): with no
// further session change, no further frame would come. That refresh is fenced
// by the operation lock; one the lock stops is re-sent by the release.
//
// An unversioned frame (old daemon) is reconciled exactly as before, and — once
// that reconciliation returned — clears what is held: nothing versioned may be
// compared against a list from before it. A value that is not a JSON array is
// no list at all: the whole frame is ignored, versioned or not, and `held` is
// left alone (codex adversarial F4). An unversioned reconcile that throws keeps
// `held` and asks for the same recovery refresh as a versioned one.
//
// THE BARRIER (#1309 + #1310 spec §3.1.1, codex plan review #2). The version
// orders lists against lists, not against this window's own writes: a frame
// read by the daemon BEFORE a write under the operation lock but delivered
// after it would judge the panes the write just put on screen — a session
// created just before the write, absent from that frame, would be marked
// `session-closed`, irreversibly. So from the lock's acquire until the
// release's refresh settles, a versioned, live host is in barrier
// (refresh-sessions.ts raises it, `endSessionsBarrier` lowers it): a versioned
// frame on its open gate is not reconciled but held back, newest only. When the
// barrier ends — however the refresh ended — the frame goes through the normal
// path below, i.e. through `decide`: older than the refreshed list → dropped;
// newer, or no list was applied → reconciled, as it would have been without
// the barrier. Unversioned frames (nothing better is coming) and frames on a
// closed gate (a new connection: the gate must open) are handled at once.
import type { HostEvent } from '../host-events'
import type { Session } from '../host-api'
import { reconcileHostSessions } from './reconcile-host'
import { canAttachTerminal } from './attach-gate'
import { currentLockGen, recoverHostSessions } from './refresh-sessions'
import { clearHeld, decide, dropStash, inBarrier, lowerBarrier, note, parseVersion, stashFrame } from './session-version'

export function handleSessionsFrame(hostId: string, event: HostEvent): void {
  let parsed: unknown
  try {
    parsed = JSON.parse(event.value)
  } catch {
    return // no list, no evidence
  }
  if (!Array.isArray(parsed)) return // not a list either
  const data = parsed as Session[]
  const v = parseVersion(event)
  if (v === null) {
    dropStash(hostId) // this list came later on the same socket
    try {
      reconcileHostSessions(hostId, data)
    } catch {
      void recoverHostSessions(hostId)
      return // held stays: this list was not applied, so nothing is known to be older than it
    }
    clearHeld(hostId)
    return
  }
  if (inBarrier(hostId) && canAttachTerminal(hostId)) {
    stashFrame(hostId, event, v)
    return
  }
  if (decide(hostId, v, { kind: 'ws' }) === 'stale') return
  // Claim before apply: see the header.
  note(hostId, v)
  try {
    reconcileHostSessions(hostId, data)
  } catch {
    void recoverHostSessions(hostId)
  }
}

/**
 * The refresh started by the lock release of lock generation `lockGen` is over
 * for `hostId` (whether it applied a list or not): end the host's barrier and
 * hand the frame it held back to the normal path. Nothing happens when the lock
 * was taken again since — the barrier is the next holder's, and ITS release's
 * refresh ends it.
 */
export function endSessionsBarrier(hostId: string, lockGen: number): void {
  if (currentLockGen() !== lockGen) return
  const stashed = lowerBarrier(hostId)
  if (stashed !== null) handleSessionsFrame(hostId, stashed)
}
