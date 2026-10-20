// spa/src/lib/pool/pool-look-migration.test.ts — the one-time copy of the fleet's probe-host looks and order into usePoolHostsStore, ahead of Profile Sync (monitoring tabs MON-2b, A3 / W2).
import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest'
import { bootPoolLooks } from './pool-look-migration'
import { usePoolStore } from './store'
import { useFleetStore } from '../fleet/store'
import { usePoolHostsStore } from '../../stores/usePoolHostsStore'
import { STORAGE_KEYS } from '../storage'

const WWD_MLAB = '82b11c521df16c12'
const WWD_AIR = '0123456789abcdef'
const WWD_MSERV = 'aaaaaaaaaaaaaaaa'

const start = vi.fn()
const stop = vi.fn()
const marker = () => JSON.parse(localStorage.getItem(STORAGE_KEYS.POOL_HOST_LOOKS_MIGRATED) ?? 'null')
const poolHost = (alias: string, host_id: string) => ({ alias, host_id }) as never
const fhost = (id: string, alias: string, kind: 'daemon' | 'probe', extra: object = {}) => ({ id, alias, kind, name: alias, icon: '', os: '', ...extra })

function setFleet(order: string[], hosts: Record<string, object>, stale = false) {
  useFleetStore.setState({ snapshot: { hostId: 'x', role: 'primary', term: 1, at: 1, order, hosts } as never, stale } as never)
}
const answerPool = () => usePoolStore.setState({ hosts: [poolHost('mlab', WWD_MLAB), poolHost('air26', WWD_AIR), poolHost('mserv', WWD_MSERV)], sourceHostId: 'cfg-a' })

beforeEach(() => {
  vi.useFakeTimers()
  localStorage.removeItem(STORAGE_KEYS.POOL_HOST_LOOKS_MIGRATED)
  start.mockClear(); stop.mockClear()
  usePoolStore.setState({ start, stop, hosts: [], sourceHostId: null } as never)
  useFleetStore.setState({ snapshot: null, stale: false } as never)
  usePoolHostsStore.setState({ order: [], looks: {} })
})
afterEach(() => vi.useRealTimers())

const fleetBody = () => setFleet(['p1', 'd1', 'p2', 'p3'], {
  p1: fhost('p1', 'air26', 'probe', { icon: 'Laptop', icon_weight: 'bold', colors: { terminal: { bg: '#112233' } } }),
  d1: fhost('d1', 'mlab', 'daemon', { icon: 'Cube' }),
  p2: fhost('p2', 'mserv', 'probe', { icon: 'HardDrives' }),
  p3: fhost('p3', 'ghost', 'probe', { icon: 'Cube' }), // pool does not list it
})

describe('bootPoolLooks', () => {
  it('waits for both the pool\'s first answer and a fresh fleet snapshot, then copies once and writes the marker', async () => {
    const p = bootPoolLooks()
    let done = false
    void p.then(() => { done = true })
    answerPool()
    await vi.advanceTimersByTimeAsync(0)
    expect(done).toBe(false) // no fleet yet
    fleetBody()
    await p
    expect(usePoolHostsStore.getState().looks[WWD_AIR]).toMatchObject({ icon: 'Laptop', iconWeight: 'bold' })
    expect(marker()).toMatchObject({ done: true })
    expect(start).toHaveBeenCalledTimes(1)
    expect(stop).toHaveBeenCalledTimes(1) // retainPool let go
  })

  it('copies only probe hosts (a daemon host\'s look is already in the host look store), and only those the pool lists', async () => {
    answerPool(); fleetBody()
    await bootPoolLooks()
    const { looks } = usePoolHostsStore.getState()
    expect(Object.keys(looks).sort()).toEqual([WWD_AIR, WWD_MSERV].sort())
    expect(looks[WWD_MLAB]).toBeUndefined()
  })

  it('never overwrites a look that is already there', async () => {
    usePoolHostsStore.setState({ looks: { [WWD_AIR]: { icon: 'Cube' } } })
    answerPool(); fleetBody()
    await bootPoolLooks()
    expect(usePoolHostsStore.getState().looks[WWD_AIR]).toEqual({ icon: 'Cube' })
  })

  it('writes the order from the fleet\'s order (alias to host_id, unlisted skipped) only when the order is empty', async () => {
    answerPool(); fleetBody()
    await bootPoolLooks()
    expect(usePoolHostsStore.getState().order).toEqual([WWD_AIR, WWD_MLAB, WWD_MSERV])
    localStorage.removeItem(STORAGE_KEYS.POOL_HOST_LOOKS_MIGRATED)
    usePoolHostsStore.setState({ order: [WWD_MSERV] })
    await bootPoolLooks()
    expect(usePoolHostsStore.getState().order).toEqual([WWD_MSERV])
  })

  it('a stale snapshot does not count', async () => {
    answerPool(); setFleet(['p1'], { p1: fhost('p1', 'air26', 'probe', { icon: 'Laptop' }) }, true)
    const p = bootPoolLooks(1000)
    await vi.advanceTimersByTimeAsync(1000)
    await p
    expect(usePoolHostsStore.getState().looks).toEqual({})
    expect(marker()).toEqual({ done: false, tries: 1 })
  })

  it('after done it resolves at once, touches nothing, and a look the user reset is not copied back', async () => {
    localStorage.setItem(STORAGE_KEYS.POOL_HOST_LOOKS_MIGRATED, JSON.stringify({ done: true, tries: 0 }))
    answerPool(); fleetBody()
    await bootPoolLooks()
    expect(usePoolHostsStore.getState().looks).toEqual({})
    expect(start).not.toHaveBeenCalled()
  })

  it('on timeout it lets Profile Sync go without writing; a snapshot that arrives later writes nothing either; tries goes up', async () => {
    const p = bootPoolLooks(10_000)
    await vi.advanceTimersByTimeAsync(10_000)
    await p
    expect(marker()).toEqual({ done: false, tries: 1 })
    answerPool(); fleetBody()
    await vi.advanceTimersByTimeAsync(0)
    expect(usePoolHostsStore.getState().looks).toEqual({})
    expect(stop).toHaveBeenCalledTimes(1)
  })

  it('the third timeout in a row gives up with done', async () => {
    localStorage.setItem(STORAGE_KEYS.POOL_HOST_LOOKS_MIGRATED, JSON.stringify({ done: false, tries: 2 }))
    const p = bootPoolLooks(1000)
    await vi.advanceTimersByTimeAsync(1000)
    await p
    expect(marker()).toEqual({ done: true, tries: 3 })
  })

  it('a broken marker reads as not done and the run goes on', async () => {
    localStorage.setItem(STORAGE_KEYS.POOL_HOST_LOOKS_MIGRATED, '{nope')
    answerPool(); fleetBody()
    await bootPoolLooks()
    expect(marker()).toMatchObject({ done: true })
  })

  it('the marker key is device-local: not a synced store key', () => {
    expect(STORAGE_KEYS.POOL_HOST_LOOKS_MIGRATED).toBe('purdex-pool-host-looks-migrated')
  })
})
