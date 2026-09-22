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
// connection instead (`recoverHostSessions`, refresh-after-switch.ts): with no
// further session change, no further frame would come.
//
// An unversioned frame (old daemon) is reconciled exactly as before, and clears
// what is held: nothing versioned may be compared against a list from before it.
import type { HostEvent } from '../host-events'
import type { Session } from '../host-api'
import { reconcileHostSessions } from './reconcile-host'
import { recoverHostSessions } from './refresh-after-switch'
import { clearHeld, decide, note, parseVersion } from './session-version'

export function handleSessionsFrame(hostId: string, event: HostEvent): void {
  let data: Session[]
  try {
    data = JSON.parse(event.value)
  } catch {
    return // no list, no evidence
  }
  const v = parseVersion(event)
  if (v === null) {
    clearHeld(hostId)
    try {
      reconcileHostSessions(hostId, data)
    } catch { /* ignore */ }
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
