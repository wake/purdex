// spa/src/stores/usePoolHostsStore.test.ts — pool host order + looks (monitoring tabs spec A3, W1; MON-2a task 1).
import { beforeEach, describe, expect, it, vi } from 'vitest'

const registerSpy = vi.hoisted(() => vi.fn())

vi.mock('../lib/storage/sync', () => ({
  syncManager: { register: registerSpy, notify: vi.fn(), destroy: vi.fn() },
  createSyncManager: vi.fn(),
}))

import { MAX_POOL_HOSTS, sanitizePoolHostLook, usePoolHostsStore } from './usePoolHostsStore'
import { STORAGE_KEYS } from '../lib/storage/keys'

const A = '82b11c521df16c12'
const B = 'a0b1c2d3e4f50617'
const hex = (n: number) => n.toString(16).padStart(16, '0')

async function rehydrateFrom(state: unknown): Promise<void> {
  localStorage.setItem(STORAGE_KEYS.POOL_HOSTS, JSON.stringify({ state, version: 1 }))
  await usePoolHostsStore.persist.rehydrate()
}

const st = () => usePoolHostsStore.getState()
const persisted = () => (JSON.parse(localStorage.getItem(STORAGE_KEYS.POOL_HOSTS) ?? '{}') as { state?: Record<string, unknown> }).state

beforeEach(() => {
  localStorage.clear()
  usePoolHostsStore.setState({ order: [], looks: {} })
})

describe('usePoolHostsStore', () => {
  it('defaults to empty and persists exactly { order, looks }', () => {
    expect(STORAGE_KEYS.POOL_HOSTS).toBe('purdex-pool-hosts')
    expect(usePoolHostsStore.getInitialState()).toMatchObject({ order: [], looks: {} })
    st().setOrder([A])
    expect(persisted()).toEqual({ order: [A], looks: {} })
  })

  it('is registered with syncManager under its storage key', () => {
    expect(registerSpy).toHaveBeenCalledWith(STORAGE_KEYS.POOL_HOSTS, usePoolHostsStore)
  })

  describe('key sanitising (merge on every arrival)', () => {
    it('accepts a wwd host_id and drops everything else, in order and looks alike', async () => {
      const bad = ['mlab', 'mserv', 'abc:def', '82B11C521DF16C12', '82b11c521df16c1', '82b11c521df16c123', '__proto__']
      const looks: Record<string, unknown> = { [A]: { icon: 'Cpu' } }
      for (const k of bad) Object.defineProperty(looks, k, { value: { icon: 'Cpu' }, enumerable: true })
      await rehydrateFrom({ order: [...bad, A], looks })
      expect(st().order).toEqual([A])
      expect(Object.keys(st().looks)).toEqual([A])
      expect(Object.getPrototypeOf(st().looks)).toBe(Object.prototype)
    })

    it('de-duplicates order and caps both at MAX_POOL_HOSTS', async () => {
      expect(MAX_POOL_HOSTS).toBe(32)
      const many = Array.from({ length: 40 }, (_, i) => hex(i))
      const looks = Object.fromEntries(many.map((k) => [k, { icon: 'Cpu' }]))
      await rehydrateFrom({ order: [A, A, ...many], looks })
      expect(st().order).toHaveLength(32)
      expect(st().order.filter((k) => k === A)).toHaveLength(1)
      expect(Object.keys(st().looks)).toHaveLength(32)
    })

    it('treats a non-record payload as nothing stored', async () => {
      st().setOrder([A])
      await rehydrateFrom('garbage')
      expect(st().order).toEqual([A])
    })

    it('sanitises a write from another window the same way', async () => {
      await rehydrateFrom({ order: ['mlab', B], looks: { mlab: { icon: 'Cpu' } } })
      expect(st().order).toEqual([B])
      expect(st().looks).toEqual({})
    })
  })

  describe('look sanitising', () => {
    it('drops name and unknown fields, keeps the HostLookEntry fields', () => {
      expect(sanitizePoolHostLook({ name: 'x', icon: 'Cpu', iconWeight: 'bold', extra: 1 })).toEqual({ icon: 'Cpu', iconWeight: 'bold' })
    })

    it('returns null for a non-record', () => {
      expect(sanitizePoolHostLook(null)).toBeNull()
      expect(sanitizePoolHostLook([])).toBeNull()
    })

    it('keeps indicator shapes with 1–2 known items and fills a missing second bar with pressure', () => {
      expect(sanitizePoolHostLook({ indicator: { shape: 'ring', data: ['load', 'cpu'] } })).toEqual({ indicator: { shape: 'ring', data: ['load'] } })
      expect(sanitizePoolHostLook({ indicator: { shape: 'bar', data: ['mem', 'cpu'] } })).toEqual({ indicator: { shape: 'bar', data: ['mem'] } })
      expect(sanitizePoolHostLook({ indicator: { shape: 'bars', data: ['cpu', 'mem'] } })).toEqual({ indicator: { shape: 'bars', data: ['cpu', 'mem'] } })
      expect(sanitizePoolHostLook({ indicator: { shape: 'bars', data: ['cpu'] } })).toEqual({ indicator: { shape: 'bars', data: ['cpu', 'pressure'] } })
    })

    it('drops an indicator with an unknown shape, no data, or an unknown first item; ignores unknown extras', () => {
      expect(sanitizePoolHostLook({ indicator: { shape: 'pie', data: ['cpu'] } })).toEqual({})
      expect(sanitizePoolHostLook({ indicator: { shape: 'ring', data: [] } })).toEqual({})
      expect(sanitizePoolHostLook({ indicator: { shape: 'ring', data: ['bogus'] } })).toEqual({})
      // a malformed list is dropped whole, not rewritten into a different metric (codex R1)
      expect(sanitizePoolHostLook({ indicator: { shape: 'bars', data: ['cpu', 'bogus'] } })).toEqual({})
      expect(sanitizePoolHostLook({ indicator: { shape: 'ring', data: ['load', 'bogus'] } })).toEqual({})
      expect(sanitizePoolHostLook({ indicator: { shape: 'bars', data: ['cpu', 'mem', 'disk'] } })).toEqual({})
    })
  })

  describe('actions', () => {
    it('setOrder sanitises and replaces', () => {
      st().setOrder([B, 'mlab', A, B])
      expect(st().order).toEqual([B, A])
    })

    it('patchLook writes, removes on undefined, and writes nothing for the same object', () => {
      st().patchLook(A, () => ({ icon: 'Cpu' }))
      expect(st().looks[A]).toEqual({ icon: 'Cpu' })
      const before = st().looks
      st().patchLook(A, (cur) => cur)
      expect(st().looks).toBe(before)
      st().patchLook(A, () => undefined)
      expect(st().looks).toEqual({})
    })

    it('patchLook ignores a key that is not a host_id', () => {
      st().patchLook('mlab', () => ({ icon: 'Cpu' }))
      expect(st().looks).toEqual({})
    })

    it('putLooksIfAbsent skips existing keys and invalid ones', () => {
      st().patchLook(A, () => ({ icon: 'Cpu' }))
      const other = { icon: 'Cpu', iconWeight: 'bold' } as const
      st().putLooksIfAbsent({ [A]: other, [B]: other, mlab: other })
      expect(st().looks).toEqual({ [A]: { icon: 'Cpu' }, [B]: other })
    })

    it('setIndicatorForAll sets the indicator on each host, keeping its other fields', () => {
      st().patchLook(A, () => ({ icon: 'Cpu' }))
      st().setIndicatorForAll([A, B], { shape: 'bars', data: ['cpu', 'mem'] })
      expect(st().looks[A]).toEqual({ icon: 'Cpu', indicator: { shape: 'bars', data: ['cpu', 'mem'] } })
      expect(st().looks[B]).toEqual({ indicator: { shape: 'bars', data: ['cpu', 'mem'] } })
    })
  })
})
