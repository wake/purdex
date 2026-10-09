// spa/src/lib/pending-revocation-retry.ts — retries the revocations in usePendingRevocationsStore (QR pairing spec §4.3,
// QP-3). Lives outside any component so it works with the paired-phones page closed: `startPendingRevocationRetry` is one
// module-level subscription started from main.tsx; the page also calls `retryPendingRevocations` when it mounts.
//
// One attempt per trigger: a host's runtime status turning 'connected' (not "being connected": a heartbeat that keeps it
// connected is no trigger) and the page mounting. No timers, no polling — a failure stays in the store until the next
// trigger. An entry leaves the store on 204 or `unsupported` (a daemon without devices.v1 has nothing to revoke), and
// when its host is no longer in the host store.
import { revokePairing } from './devices-api'
import { useHostStore } from '../stores/useHostStore'
import { usePendingRevocationsStore } from '../stores/usePendingRevocationsStore'

const inFlight = new Set<string>()
const keyOf = (hostId: string, pairingId: string) => JSON.stringify([hostId, pairingId])

/** Try every pending revocation (of `hostId` only, when given). Never rejects. */
export async function retryPendingRevocations(hostId?: string): Promise<void> {
  const store = usePendingRevocationsStore.getState()
  const jobs: Promise<void>[] = []
  for (const item of store.items) {
    if (hostId !== undefined && item.hostId !== hostId) continue
    if (!useHostStore.getState().hosts[item.hostId]) {
      store.remove(item.hostId, item.pairingId)
      continue
    }
    const key = keyOf(item.hostId, item.pairingId)
    if (inFlight.has(key)) continue
    inFlight.add(key)
    jobs.push(
      (async () => {
        try {
          const r = await revokePairing(item.hostId, item.pairingId)
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
    for (const hostId of new Set(items.map((i) => i.hostId))) {
      if (next.runtime[hostId]?.status === 'connected' && prev.runtime[hostId]?.status !== 'connected') {
        void retryPendingRevocations(hostId)
      }
    }
  })
}
