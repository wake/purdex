// spa/src/stores/usePendingRevocationsStore.ts — the (host, pairing) revocations that still have to reach a host
// (QR pairing spec §4.3, QP-3): the device tokens of a paired phone could not be revoked on a host that was unreachable.
// Device-local (purdex-pending-revocations): never registered with syncManager, never part of Profile Sync — a host being
// unreachable from THIS Mac is this Mac's business. Retried by lib/pending-revocation-retry.ts.
import { create } from 'zustand'
import { persist } from 'zustand/middleware'
import { purdexStorage, STORAGE_KEYS } from '../lib/storage'

export interface PendingRevocation {
  hostId: string
  pairingId: string
}

interface State {
  items: PendingRevocation[]
  add: (hostId: string, pairingId: string) => void
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
    if (!out.some((o) => same(o, hostId, pairingId))) out.push({ hostId, pairingId })
  }
  return out
}

export const usePendingRevocationsStore = create<State>()(
  persist(
    (set, get) => ({
      items: [],
      add: (hostId, pairingId) => {
        if (get().items.some((i) => same(i, hostId, pairingId))) return
        set((s) => ({ items: [...s.items, { hostId, pairingId }] }))
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
