// spa/src/lib/rebuild/refresh-after-switch.ts — after a switch, reconcile the
// world that just came on screen against a list read AFTER the switch (#1255 SPA
// spec §3.2; daemon contract docs/specs/2026-09-23-session-list-fresh-spec.md).
//
// Session reconciliation only ever looks at the tabs on screen, so a world that
// was parked while one of its sessions closed comes back un-reconciled, and with
// no further session change its host never pushes again. The switching window
// therefore asks every host with a live, reconciled connection for a fresh,
// versioned list and reconciles it — the same `reconcileHostSessions` a WS frame
// runs (session store, revive snapshot, revive pass, probes).
//
// A list is evidence only when the daemon vouches for it: an unversioned answer
// (old daemon) is dropped, and a versioned one only applies when it is newer
// than anything already reconciled for the host (`decide`, session-version.ts).
// It is also dropped when, while it was on the way, the world changed again (the
// world-epoch fence moved — a switch or promote in ANY window), the attach gate
// closed, or the host left `hostOrder` / changed its endpoint.
//
// Only the switching window fetches (spec §3.6): the tab tree it writes reaches
// the other windows through the same rehydrate that brought them the new world.
import { useHostStore } from '../../stores/useHostStore'
import { listSessionsFresh, type FreshSessions } from '../host-api'
import { readWorldEpochFence } from '../storage/world-fence'
import { canAttachTerminal } from './attach-gate'
import { reconcileHostSessions } from './reconcile-host'
import { currentConn, decide, note } from './session-version'

/** `ip:port` of a host still in `hostOrder`; null otherwise. */
function endpointOf(hostId: string): string | null {
  const { hosts, hostOrder } = useHostStore.getState()
  const host = hosts[hostId]
  if (!host || !hostOrder.includes(hostId)) return null
  return `${host.ip}:${host.port}`
}

async function refreshHost(hostId: string): Promise<void> {
  // ONE synchronous step (spec §3.3, last paragraph): the gate and `conn` are
  // read together, so the fetch is sent only while the gate is open AND `conn`
  // names the connection that opened it — the hook moves `conn` before it
  // closes the gate.
  if (!canAttachTerminal(hostId)) return
  const conn = currentConn(hostId)
  const world = readWorldEpochFence()
  const endpoint = endpointOf(hostId)
  if (endpoint === null) return

  let fresh: FreshSessions
  try {
    fresh = await listSessionsFresh(hostId)
  } catch {
    return // no retry: the host's next WS frame still reconciles, as ever
  }
  if (fresh.kind !== 'versioned') return

  if (readWorldEpochFence() !== world) return
  if (!canAttachTerminal(hostId)) return
  if (endpointOf(hostId) !== endpoint) return

  const v = { epoch: fresh.epoch, seq: fresh.seq }
  if (decide(hostId, v, { kind: 'fetch', conn }) === 'stale') return
  try {
    reconcileHostSessions(hostId, fresh.sessions)
  } catch {
    return // nothing held moves on a failed reconciliation
  }
  note(hostId, v)
}

/**
 * Fire-and-forget for the caller (`switchActiveProfile`, after both locks are
 * released); the promise only exists for tests. Each host on its own: one
 * host's failure costs no other its refresh.
 */
export function refreshSessionsAfterSwitch(): Promise<void> {
  const hosts = useHostStore.getState().hostOrder
  return Promise.all(hosts.map((hostId) => refreshHost(hostId).catch(() => {}))).then(() => {})
}
