import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { requestAtOf, useHostStore } from './useHostStore'
import { STORAGE_KEYS } from '../lib/storage/keys'

const rev = () => useHostStore.getState().hostsRev

describe('useHostStore hostsRev (PA-2 D8)', () => {
  beforeEach(() => { useHostStore.getState().reset() })
  afterEach(() => { vi.useRealTimers() })

  it('starts at 0', () => { expect(rev()).toBe(0) })

  it('addHost bumps it to max(prev + 1, now)', () => {
    vi.useFakeTimers(); vi.setSystemTime(5_000_000)
    useHostStore.getState().addHost({ name: 'a', ip: '1.1.1.1', port: 1 })
    expect(rev()).toBe(5_000_000)
    vi.setSystemTime(4_000_000) // clock went back: still strictly increasing
    useHostStore.getState().addHost({ name: 'b', ip: '1.1.1.2', port: 1 })
    expect(rev()).toBe(5_000_001)
  })

  it('addHost of an existing id does not bump', () => {
    const id = useHostStore.getState().addHost({ name: 'a', ip: '1.1.1.1', port: 1 })
    const r = rev()
    useHostStore.getState().addHost({ id, name: 'a', ip: '1.1.1.1', port: 1 })
    expect(rev()).toBe(r)
  })

  it('updateHost bumps for ip / port / token, not for a name or a no-op', () => {
    const id = useHostStore.getState().addHost({ name: 'a', ip: '1.1.1.1', port: 1, token: 't' })
    let r = rev()
    useHostStore.getState().updateHost(id, { name: 'renamed' })
    expect(rev()).toBe(r)
    useHostStore.getState().updateHost(id, { token: 't' })
    expect(rev()).toBe(r)
    for (const patch of [{ ip: '2.2.2.2' }, { port: 2 }, { token: 'u' }]) {
      useHostStore.getState().updateHost(id, patch)
      expect(rev()).toBeGreaterThan(r)
      r = rev()
    }
    useHostStore.getState().updateHost('missing', { port: 9 })
    expect(rev()).toBe(r)
  })

  it('removeHost bumps; a refused removal (last host) does not', () => {
    const only = useHostStore.getState().hostOrder[0]
    useHostStore.getState().removeHost(only)
    expect(rev()).toBe(0)
    const id = useHostStore.getState().addHost({ name: 'a', ip: '1.1.1.1', port: 1 })
    const r = rev()
    useHostStore.getState().removeHost(id)
    expect(rev()).toBeGreaterThan(r)
  })

  it('reorderHosts does not bump', () => {
    const id = useHostStore.getState().addHost({ name: 'a', ip: '1.1.1.1', port: 1 })
    const r = rev()
    useHostStore.getState().reorderHosts([id, ...useHostStore.getState().hostOrder.filter((h) => h !== id)])
    expect(rev()).toBe(r)
  })

  it('applyHostTransfer bumps for a created row and for an overwrite of the address', () => {
    const m = useHostStore.getState().addHost({ name: 'm', ip: '1.1.1.1', port: 1, token: 'old' })
    const s = useHostStore.getState()
    s.observeDaemonId(m, 'd1_m', requestAtOf(s.hosts[m]))
    let r = rev()
    const created = useHostStore.getState().applyHostTransfer({
      create: [{ name: 'air', ip: '100.64.0.4', port: 7860, token: 't', daemonId: 'd1_air', look: {} }], overwrite: [],
    })
    expect(created.kind).toBe('applied')
    expect(rev()).toBeGreaterThan(r)
    r = rev()
    const over = useHostStore.getState().applyHostTransfer({
      create: [], overwrite: [{ hostId: m, expect: { endpoint: '1.1.1.1:1', token: 'old', daemonId: 'd1_m' }, name: 'mm', ip: '2.2.2.2', port: 1, token: 'new', look: {} }],
    })
    expect(over.kind).toBe('applied')
    expect(rev()).toBeGreaterThan(r)
  })

  it('applyHostTransfer that only renames does not bump; a stale one does not', () => {
    const m = useHostStore.getState().addHost({ name: 'm', ip: '1.1.1.1', port: 1, token: 'old' })
    const s = useHostStore.getState()
    s.observeDaemonId(m, 'd1_m', requestAtOf(s.hosts[m]))
    const r = rev()
    useHostStore.getState().applyHostTransfer({
      create: [], overwrite: [{ hostId: m, expect: { endpoint: '1.1.1.1:1', token: 'old', daemonId: 'd1_m' }, name: 'renamed', ip: '1.1.1.1', port: 1, token: 'old', look: {} }],
    })
    expect(rev()).toBe(r)
    useHostStore.getState().applyHostTransfer({
      create: [], overwrite: [{ hostId: m, expect: { endpoint: 'x:1', token: 'old', daemonId: 'd1_m' }, name: 'q', ip: '9.9.9.9', port: 1, token: 'old', look: {} }],
    })
    expect(rev()).toBe(r)
  })

  it('registerLocalHost bumps for a new host, not for an existing one that keeps its token', () => {
    let r = rev()
    useHostStore.getState().registerLocalHost({ url: 'http://100.64.0.9:7860', token: 'a', hostname: 'x' })
    expect(rev()).toBeGreaterThan(r)
    r = rev()
    useHostStore.getState().registerLocalHost({ url: 'http://100.64.0.9:7860', token: 'b', hostname: 'x' })
    expect(rev()).toBe(r)
  })

  it('registerLocalHost filling an empty token bumps', () => {
    useHostStore.getState().addHost({ name: 'x', ip: '100.64.0.9', port: 7860, token: null })
    const r = rev()
    useHostStore.getState().registerLocalHost({ url: 'http://100.64.0.9:7860', token: 'b', hostname: 'x' })
    expect(rev()).toBeGreaterThan(r)
  })

  it('is persisted, and a rehydrate adopts the stored value without bumping', async () => {
    const id = useHostStore.getState().addHost({ name: 'a', ip: '1.1.1.1', port: 1 })
    const r = rev()
    const raw = JSON.parse(localStorage.getItem(STORAGE_KEYS.HOSTS) ?? 'null')
    expect(raw.state.hostsRev).toBe(r)
    await useHostStore.persist.rehydrate()
    expect(rev()).toBe(r)
    expect(useHostStore.getState().hosts[id]).toBeDefined()
  })

  it('a persisted state without hostsRev rehydrates as 0', async () => {
    localStorage.setItem(STORAGE_KEYS.HOSTS, JSON.stringify({ state: { hosts: {}, hostOrder: [], activeHostId: null, devHostId: null }, version: 1 }))
    await useHostStore.persist.rehydrate()
    expect(rev()).toBe(0)
  })
})
