import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest'
import { fleetHostsView, useFleetStore } from './store'
import { fleetHistory } from './history'
import { useHostStore } from '../../stores/useHostStore'
import { fast, host, primaryAnswer, standbyAnswer } from './fixtures'
import type { FleetPush } from './types'

const A = 'cfg-a' // the App's HostConfig ids; the daemons' own host ids are 'd-a' / 'd-b'
const B = 'cfg-b'
const st = () => useFleetStore.getState()

const hostCfg = (id: string, daemonId: string) => ({ id, name: id, ip: '100.64.0.2', port: 7860, token: 't', order: 0, daemonId })

/** Start a host's generation, mark it supporting and apply its answer, as fleet-support does. */
function answer(hostId: string, a: ReturnType<typeof primaryAnswer> | ReturnType<typeof standbyAnswer>) {
  const gen = st().begin(hostId)
  st().setSupport(hostId, gen, 'yes')
  st().applyFetched(hostId, gen, a)
  return gen
}
const push = (term: number, hosts: FleetPush['hosts']): FleetPush => ({ term, primary: 'air26', hosts })

beforeEach(() => {
  vi.useFakeTimers()
  vi.setSystemTime(1_000_000)
  st().reset()
  fleetHistory.clear()
  useHostStore.setState({
    hosts: { [A]: hostCfg(A, 'd-a'), [B]: hostCfg(B, 'd-b') }, hostOrder: [A, B], activeHostId: A, runtime: {},
  } as never)
})
afterEach(() => { vi.useRealTimers() })

describe('primary choice', () => {
  it('a primary answer becomes the fleet', () => {
    answer(A, primaryAnswer(3, [host('d-a'), host('d-b')]))
    expect(st().primaryHostId).toBe(A)
    expect(st().snapshot?.order).toEqual(['d-a', 'd-b'])
    expect(st().waitingPrimary).toBe(false)
  })

  it('two primaries: the higher term wins whichever answered first', () => {
    answer(A, primaryAnswer(2, [host('d-a', { name: 'from-A' })]))
    answer(B, primaryAnswer(5, [host('d-a', { name: 'from-B' })]))
    expect(st().primaryHostId).toBe(B)
    expect(st().snapshot?.hosts['d-a'].name).toBe('from-B')
    answer(A, primaryAnswer(2, [host('d-a', { name: 'from-A2' })]))
    expect(st().primaryHostId).toBe(B)
    expect(st().snapshot?.hosts['d-a'].name).toBe('from-B')
  })

  it('equal terms: the earlier host in the App order wins', () => {
    answer(B, primaryAnswer(4, [host('d-a', { name: 'from-B' })]))
    answer(A, primaryAnswer(4, [host('d-a', { name: 'from-A' })]))
    expect(st().primaryHostId).toBe(A)
    expect(st().snapshot?.hosts['d-a'].name).toBe('from-A')
  })

  it('a standby answer is not the fleet; only a standby known means primaryElsewhere', () => {
    answer(A, standbyAnswer(2, 'air26'))
    expect(st().primaryHostId).toBeNull()
    expect(st().snapshot).toBeNull()
    expect(st().primaryElsewhere).toBe(true)
    expect(st().waitingPrimary).toBe(false)
  })

  it('a primary that turns standby on a re-read leaves the last values waiting, not cleared', () => {
    answer(A, primaryAnswer(2, [host('d-a', { fast: fast({ cpu: 77 }) })]))
    answer(A, standbyAnswer(3, 'air26'))
    expect(st().primaryHostId).toBeNull()
    expect(st().snapshot?.hosts['d-a'].fast?.cpu).toBe(77)
    expect(st().waitingPrimary).toBe(true)
    expect(st().primaryElsewhere).toBe(false)
  })
})

describe('fleet.changed', () => {
  beforeEach(() => { answer(A, primaryAnswer(2, [host('d-a'), host('d-b', { slow: { at: 1, top_cpu: [], top_mem: [] } })])) })

  it('merges fast into the primary snapshot and keeps the slow part until a new one comes', () => {
    const r = st().applyPush(A, push(2, [{ id: 'd-a', fast: fast({ at: 2_000_000, cpu: 88 }) }, { id: 'd-b', fast: fast({ at: 2_000_000 }) }]))
    expect(r).toBe('applied')
    expect(st().snapshot?.hosts['d-a'].fast?.cpu).toBe(88)
    expect(st().snapshot?.hosts['d-b'].slow?.at).toBe(1)
    st().applyPush(A, push(2, [{ id: 'd-b', fast: fast({ at: 3_000_000 }), slow: { at: 9, top_cpu: [], top_mem: [] } }]))
    expect(st().snapshot?.hosts['d-b'].slow?.at).toBe(9)
  })

  it('a status change arrives in a later push', () => {
    st().applyPush(A, push(2, [{ id: 'd-b', fast: fast({ at: 2_000_000 }), status: 'offline', error: 'timeout' }]))
    expect(st().snapshot?.hosts['d-b']).toMatchObject({ status: 'offline', error: 'timeout' })
  })

  it('a push from a daemon that is not the primary at the same or a lower term is ignored', () => {
    answer(B, standbyAnswer(2, 'air26'))
    expect(st().applyPush(B, push(2, [{ id: 'd-a', fast: fast({ at: 2_000_000, cpu: 5 }) }]))).toBe('ignored')
    expect(st().snapshot?.hosts['d-a'].fast?.cpu).toBe(20)
  })

  it('a push with a higher term from another daemon asks for a re-read and does not switch by itself', () => {
    answer(B, standbyAnswer(2, 'air26'))
    expect(st().applyPush(B, push(3, [{ id: 'd-a', fast: fast({ at: 2_000_000, cpu: 5 }) }]))).toBe('reread')
    expect(st().primaryHostId).toBe(A)
    expect(st().snapshot?.hosts['d-a'].fast?.cpu).toBe(20)
    answer(B, primaryAnswer(3, [host('d-a', { fast: fast({ cpu: 5 }) })])) // the re-read says primary
    expect(st().primaryHostId).toBe(B)
  })

  it('a push below the primary term is ignored', () => {
    expect(st().applyPush(A, push(1, [{ id: 'd-a', fast: fast({ at: 2_000_000, cpu: 5 }) }]))).toBe('ignored')
    expect(st().snapshot?.hosts['d-a'].fast?.cpu).toBe(20)
  })

  it('a host the snapshot has not seen asks for a re-read unless the push carries the whole row', () => {
    expect(st().applyPush(A, push(2, [{ id: 'd-new', fast: fast({ at: 2_000_000 }) }]))).toBe('reread')
    expect(st().snapshot?.hosts['d-new']).toBeUndefined()
    expect(st().applyPush(A, push(2, [{ id: 'p1', fast: fast({ at: 3_000_000 }), full: host('p1', { kind: 'probe' }) }]))).toBe('applied')
    expect(st().snapshot?.order).toEqual(['d-a', 'd-b', 'p1'])
  })

  it('a push for a host with no supporting answer yet is ignored', () => {
    useFleetStore.getState().reset()
    expect(st().applyPush(A, push(2, [{ id: 'd-a', fast: fast() }]))).toBe('ignored')
  })
})

describe('staleness and waitingPrimary', () => {
  beforeEach(() => { answer(A, primaryAnswer(2, [host('d-a', { fast: fast({ cpu: 61 }) })])) })

  it('no push for 10 s: waiting, with the last values kept', () => {
    st().checkStale(1_009_999)
    expect(st().waitingPrimary).toBe(false)
    st().checkStale(1_010_000)
    expect(st().waitingPrimary).toBe(true)
    expect(st().snapshot?.hosts['d-a'].fast?.cpu).toBe(61)
  })

  it('a push clears the waiting', () => {
    st().checkStale(1_011_000)
    vi.setSystemTime(1_012_000)
    st().applyPush(A, push(2, [{ id: 'd-a', fast: fast({ at: 1_012_000 }) }]))
    expect(st().waitingPrimary).toBe(false)
  })

  it('the primary disconnecting (fence) waits and keeps the values', () => {
    st().fence(A)
    expect(st().primaryHostId).toBeNull()
    expect(st().waitingPrimary).toBe(true)
    expect(st().snapshot?.hosts['d-a'].fast?.cpu).toBe(61)
  })

  it('removing the primary host drops the values with it', () => {
    st().forgetHost(A)
    expect(st().snapshot).toBeNull()
    expect(st().waitingPrimary).toBe(false)
  })
})

describe('generation', () => {
  it('a reply from before a fence does not write', () => {
    const gen = st().begin(A)
    st().setSupport(A, gen, 'yes')
    st().fence(A)
    st().applyFetched(A, gen, primaryAnswer(2, [host('d-a')]))
    expect(st().snapshot).toBeNull()
    expect(st().byHost[A].role).toBeUndefined()
  })

  it('a reply from before a re-point (forget + begin) does not write', () => {
    const gen = st().begin(A)
    st().forgetHost(A)
    st().begin(A)
    st().applyFetched(A, gen, primaryAnswer(2, [host('d-a')]))
    expect(st().snapshot).toBeNull()
  })

  it('a support answer of an old generation does not apply', () => {
    const old = st().begin(A)
    st().begin(A)
    st().setSupport(A, old, 'yes')
    expect(st().byHost[A].support).toBe('unknown')
  })

  it('a GET reply is not applied to a host whose support is not yes', () => {
    const gen = st().begin(A)
    st().applyFetched(A, gen, primaryAnswer(2, [host('d-a')]))
    expect(st().snapshot).toBeNull()
  })
})

describe('history feed and hosts view', () => {
  it('every accepted reading reaches the in-memory history; an ignored push does not', () => {
    answer(A, primaryAnswer(2, [host('d-a', { fast: fast({ at: 1_000, cpu: 10 }) })]))
    st().applyPush(A, push(2, [{ id: 'd-a', fast: fast({ at: 2_000, cpu: 20 }) }]))
    st().applyPush(A, push(1, [{ id: 'd-a', fast: fast({ at: 3_000, cpu: 30 }) }]))
    expect(fleetHistory.series('d-a', 'cpu', '5m', 3_000).map((p) => p.avg)).toEqual([10, 20])
  })

  it('maps daemon hosts to their HostConfig by daemon id, in the App order, then probe hosts', () => {
    answer(A, primaryAnswer(2, [host('p1', { kind: 'probe', name: 'mserv' }), host('d-b'), host('d-a'), host('zz-unknown')]))
    const view = fleetHostsView(st().snapshot, useHostStore.getState())
    expect(view.map((h) => [h.id, h.hostConfigId])).toEqual([['d-a', A], ['d-b', B], ['zz-unknown', undefined], ['p1', undefined]])
  })
})
