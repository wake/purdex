// spa/src/stores/usePendingRevocationsStore.ts — the (host, pairing) revocations that still have to reach a host
// (QR pairing spec §4.3, QP-3): the device tokens of a paired phone could not be revoked on a host that was unreachable.
// Device-local (purdex-pending-revocations): never registered with syncManager, never part of Profile Sync — a host being
// unreachable from THIS Mac is this Mac's business. Retried by lib/pending-revocation-retry.ts.
import { create } from 'zustand'
import { persist } from 'zustand/middleware'
import { purdexStorage, STORAGE_KEYS } from '../lib/storage'
import { canonicalEndpoint, useHostStore } from './useHostStore'
import { hostLookOf } from '../lib/host-look'

/** An entry is bound to the daemon it was made on: `endpoint` ("ip:port") and `daemonId` are the host's identity at the
 *  moment of creation, so a retry never goes to whatever the same hostId points at later (lib/pending-revocation-retry.ts).
 *  Entries written by the first shape (hostId + pairingId only) have none of the optional fields: unknown, never retried. */
export interface PendingRevocation {
  hostId: string
  pairingId: string
  endpoint?: string
  daemonId?: string
  /** Display only. */
  hostName?: string
  label?: string
  createdAt?: number
}

interface State {
  items: PendingRevocation[]
  add: (hostId: string, pairingId: string, meta?: { label?: string }) => void
  remove: (hostId: string, pairingId: string) => void
  has: (hostId: string, pairingId: string) => boolean
}

const same = (a: PendingRevocation, hostId: string, pairingId: string) => a.hostId === hostId && a.pairingId === pairingId

function heal(persisted: unknown): PendingRevocation[] {
  const raw = typeof persisted === 'object' && persisted !== null ? (persisted as { items?: unknown }).items : undefined
  if (!Array.isArray(raw)) return []
  const out: PendingRevocation[] = []
  for (const v of raw) {
    if (typeof v !== 'object' || v === null) continue
    const { hostId, pairingId } = v as Record<string, unknown>
    if (typeof hostId !== 'string' || hostId === '' || typeof pairingId !== 'string' || pairingId === '') continue
    if (out.some((o) => same(o, hostId, pairingId))) continue
    const r = v as Record<string, unknown>
    const item: PendingRevocation = { hostId, pairingId }
    for (const k of ['endpoint', 'daemonId', 'hostName', 'label'] as const) {
      if (typeof r[k] === 'string' && r[k] !== '') item[k] = r[k] as string
    }
    if (typeof r.createdAt === 'number' && Number.isFinite(r.createdAt)) item.createdAt = r.createdAt
    out.push(item)
  }
  return out
}

export const usePendingRevocationsStore = create<State>()(
  persist(
    (set, get) => ({
      items: [],
      add: (hostId, pairingId, meta) => {
        if (get().items.some((i) => same(i, hostId, pairingId))) return
        const host = useHostStore.getState().hosts[hostId]
        const item: PendingRevocation = { hostId, pairingId, createdAt: Date.now() }
        if (host) {
          item.endpoint = canonicalEndpoint(host)
          if (host.daemonId) item.daemonId = host.daemonId
          item.hostName = hostLookOf(hostId).name
        }
        if (meta?.label) item.label = meta.label
        set((s) => ({ items: [...s.items, item] }))
      },
      remove: (hostId, pairingId) => {
        if (!get().items.some((i) => same(i, hostId, pairingId))) return
        set((s) => ({ items: s.items.filter((i) => !same(i, hostId, pairingId)) }))
      },
      has: (hostId, pairingId) => get().items.some((i) => same(i, hostId, pairingId)),
    }),
    {
      name: STORAGE_KEYS.PENDING_REVOCATIONS,
      storage: purdexStorage,
      partialize: (s) => ({ items: s.items }),
      merge: (persisted, current) => ({ ...current, items: heal(persisted) }),
    },
  ),
)
