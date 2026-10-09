// A device-local list of (host, pairing) revocations that still have to reach a host: persisted under
// purdex-pending-revocations, never registered with syncManager, never projected into Profile Sync.
import { beforeEach, describe, expect, it } from 'vitest'
import { PROJECTIONS } from '../lib/profile/projections'
import { usePendingRevocationsStore } from './usePendingRevocationsStore'

beforeEach(() => {
  localStorage.clear()
  usePendingRevocationsStore.setState({ items: [] })
})

describe('usePendingRevocationsStore', () => {
  it('add is idempotent; has and remove address one (host, pairing)', () => {
    const s = usePendingRevocationsStore.getState()
    s.add('h1', 'P1')
    s.add('h1', 'P1')
    s.add('h2', 'P1')
    expect(usePendingRevocationsStore.getState().items).toEqual([
      { hostId: 'h1', pairingId: 'P1' },
      { hostId: 'h2', pairingId: 'P1' },
    ])
    expect(usePendingRevocationsStore.getState().has('h1', 'P1')).toBe(true)
    expect(usePendingRevocationsStore.getState().has('h1', 'P2')).toBe(false)
    usePendingRevocationsStore.getState().remove('h1', 'P1')
    expect(usePendingRevocationsStore.getState().items).toEqual([{ hostId: 'h2', pairingId: 'P1' }])
    usePendingRevocationsStore.getState().remove('h9', 'P9') // unknown: a no-op
    expect(usePendingRevocationsStore.getState().items).toHaveLength(1)
  })

  it('persists under purdex-pending-revocations and survives a rehydrate', () => {
    expect(usePendingRevocationsStore.persist.getOptions().name).toBe('purdex-pending-revocations')
    usePendingRevocationsStore.getState().add('h1', 'P1')
    const saved = localStorage.getItem('purdex-pending-revocations')!
    expect(JSON.parse(saved).state).toEqual({ items: [{ hostId: 'h1', pairingId: 'P1' }] })
    usePendingRevocationsStore.setState({ items: [] })
    localStorage.setItem('purdex-pending-revocations', saved)
    usePendingRevocationsStore.persist.rehydrate()
    expect(usePendingRevocationsStore.getState().has('h1', 'P1')).toBe(true)
  })

  it('heals a malformed persisted value', () => {
    localStorage.setItem(
      'purdex-pending-revocations',
      JSON.stringify({ state: { items: [{ hostId: 'h', pairingId: 'p' }, { hostId: 1 }, 'x', { hostId: 'h', pairingId: 'p' }] }, version: 0 }),
    )
    usePendingRevocationsStore.persist.rehydrate()
    expect(usePendingRevocationsStore.getState().items).toEqual([{ hostId: 'h', pairingId: 'p' }])
  })

  it('is device-local: no Profile Sync projection mentions it', () => {
    for (const list of Object.values(PROJECTIONS)) {
      for (const path of list as readonly string[]) expect(path.startsWith('purdex-pending-revocations')).toBe(false)
    }
  })
})
