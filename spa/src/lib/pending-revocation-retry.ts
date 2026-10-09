// spa/src/lib/pending-revocation-retry.ts — retries the revocations in usePendingRevocationsStore (QR pairing spec §4.3,
// QP-3). Lives outside any component so it works with the paired-phones page closed: `startPendingRevocationRetry` is one
// module-level subscription started from main.tsx; the page also calls `retryPendingRevocations` when it mounts.
//
// One attempt per trigger: a host's runtime status turning 'connected' (not "being connected": a heartbeat that keeps it
// connected is no trigger) and the page mounting. No timers, no polling — a failure stays in the store until the next
// trigger. An entry leaves the store on 204 or `unsupported` (a daemon without devices.v1 has nothing to revoke), or when the
// user explicitly gives it up in the section (放棄追蹤). It never leaves any other way: an entry that cannot be matched to
// the daemon it was made on (host removed, re-pointed to another ip:port or daemon, or a legacy entry without identity)
// sends NOTHING and stays, flagged as needing attention. A phone token that stays valid is a security hole.
import { revokePairing } from './devices-api'
import { useHostStore } from '../stores/useHostStore'
import { usePendingRevocationsStore, type PendingRevocation } from '../stores/usePendingRevocationsStore'

const inFlight = new Set<string>()
const keyOf = (hostId: string, pairingId: string) => JSON.stringify([hostId, pairingId])

/** What a retry may do with an entry right now. `retry` names the host id to send to (the stored one, or — when the stored
 *  host was removed — the configured host that has the same daemonId). Everything else sends nothing. */
export type PendingTarget =
  | { kind: 'retry'; hostId: string }
  | { kind: 'removed' }
  | { kind: 'repointed' }
  | { kind: 'unverifiable' }

export function resolvePendingTarget(item: PendingRevocation): PendingTarget {
  const hosts = useHostStore.getState().hosts
  const host = hosts[item.hostId]
  if (item.endpoint === undefined) return host ? { kind: 'unverifiable' } : { kind: 'removed' }
  if (host) {
    const sameEndpoint = `${host.ip}:${host.port}` === item.endpoint
    const sameDaemon = item.daemonId === undefined || host.daemonId === item.daemonId
    return sameEndpoint && sameDaemon ? { kind: 'retry', hostId: item.hostId } : { kind: 'repointed' }
  }
  if (item.daemonId !== undefined) {
    const moved = Object.values(hosts).find((h) => h.daemonId === item.daemonId)
    if (moved) return { kind: 'retry', hostId: moved.id }
  }
  return { kind: 'removed' }
}

/** Try every pending revocation that is safe to send (of `hostId` only, when given: the host it would be sent to). Never rejects. */
export async function retryPendingRevocations(hostId?: string): Promise<void> {
  const jobs: Promise<void>[] = []
  for (const item of usePendingRevocationsStore.getState().items) {
    const target = resolvePendingTarget(item)
    if (target.kind !== 'retry') continue
    if (hostId !== undefined && target.hostId !== hostId) continue
    const key = keyOf(item.hostId, item.pairingId)
    if (inFlight.has(key)) continue
    inFlight.add(key)
    jobs.push(
      (async () => {
        try {
          const r = await revokePairing(target.hostId, item.pairingId)
          if (r.kind === 'ok' || r.kind === 'unsupported') usePendingRevocationsStore.getState().remove(item.hostId, item.pairingId)
        } catch {
          // revokePairing never rejects; if it ever does, the entry simply stays for the next trigger.
        } finally {
          inFlight.delete(key)
        }
      })(),
    )
  }
  await Promise.all(jobs)
}

/** Retry a host's pending revocations each time it turns connected. Returns the unsubscribe. */
export function startPendingRevocationRetry(): () => void {
  return useHostStore.subscribe((next, prev) => {
    if (next.runtime === prev.runtime) return
    const items = usePendingRevocationsStore.getState().items
    if (items.length === 0) return
    const targets = new Set<string>()
    for (const item of items) {
      const t = resolvePendingTarget(item)
      if (t.kind === 'retry') targets.add(t.hostId)
    }
    for (const hostId of targets) {
      if (next.runtime[hostId]?.status === 'connected' && prev.runtime[hostId]?.status !== 'connected') {
        void retryPendingRevocations(hostId)
      }
    }
  })
}
