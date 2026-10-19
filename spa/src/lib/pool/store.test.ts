import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest'
import { classify } from './errors'
import type { PoolHost } from './types'
import { parseFacts, parseHostsAnswer } from './parse'
import darwinJson from './__fixtures__/facts_darwin.json'
import hostsJson from './__fixtures__/pool_hosts_response.json'
import { useHostStore } from '../../stores/useHostStore'
import { useNexHostStore } from '../../stores/useNexHostStore'

const fetchPoolHosts = vi.fn()
vi.mock('./api', () => ({
  fetchPoolHosts: (...a: unknown[]) => fetchPoolHosts(...a),
  PoolReadError: class PoolReadError extends Error {
    poolError: unknown
    constructor(poolError: unknown) { super('pool read'); this.poolError = poolError }
  },
}))

import { PoolReadError } from './api'
import { usePoolStore, POLL_MS } from './store'

const A = 'cfg-a'
const B = 'cfg-b'
const st = () => usePoolStore.getState()
const hostCfg = (id: string) => ({ id, name: id, ip: '100.64.0.2', port: 7860, token: 't', order: 0 })
const entry = (caps: string[]) => ({ phase: 'ready', daemonCapabilities: caps })

const facts = parseFacts(darwinJson)!
const mlab = (over: Partial<PoolHost> = {}): PoolHost => ({ ...parseHostsAnswer(hostsJson)![0], alias: 'mlab', facts, reachable: true, ...over })
const fail = (status: number | null, error?: string) => new (PoolReadError as never as new (e: unknown) => Error)(classify(status, error ? { error } : null))

let visibility: DocumentVisibilityState = 'visible'
const setVisibility = (v: DocumentVisibilityState) => { visibility = v; document.dispatchEvent(new Event('visibilitychange')) }

beforeEach(() => {
  vi.useFakeTimers()
  vi.setSystemTime(1_000_000)
  fetchPoolHosts.mockReset()
  usePoolStore.getState().stop()
  usePoolStore.getState().reset()
  visibility = 'visible'
  Object.defineProperty(document, 'visibilityState', { configurable: true, get: () => visibility })
  useHostStore.setState({ hosts: { [A]: hostCfg(A), [B]: hostCfg(B) }, hostOrder: [A, B], activeHostId: A, runtime: {} } as never)
  useNexHostStore.setState({ byHost: { [A]: entry(['pool.v1']), [B]: entry(['pool.v1']) } } as never)
})
afterEach(() => { usePoolStore.getState().stop(); vi.useRealTimers() })

describe('source and reads', () => {
  it('reads from the first daemon in host order that announces pool.v1', async () => {
    useNexHostStore.setState({ byHost: { [A]: entry([]), [B]: entry(['pool.v1']) } } as never)
    fetchPoolHosts.mockResolvedValue([mlab()])
    await st().readOnce()
    expect(fetchPoolHosts).toHaveBeenCalledWith(B)
    expect(st().sourceHostId).toBe(B)
    expect(st().hosts.map((h) => h.alias)).toEqual(['mlab'])
  })

  it('no daemon announcing pool.v1 is the old-daemon state, and nothing is read', async () => {
    useNexHostStore.setState({ byHost: { [A]: entry([]), [B]: entry([]) } } as never)
    await st().readOnce()
    expect(fetchPoolHosts).not.toHaveBeenCalled()
    expect(st().oldDaemon).toBe(true)
    expect(st().sourceHostId).toBeNull()
  })

  it('remembers when each host was last read live', async () => {
    fetchPoolHosts.mockResolvedValue([mlab()])
    await st().readOnce()
    expect(st().lastLiveAt.mlab).toBe(facts.at)
    fetchPoolHosts.mockResolvedValue([mlab({ reachable: false })])
    await st().readOnce()
    expect(st().lastLiveAt.mlab).toBe(facts.at)
  })
})

describe('error actions', () => {
  it('wwd_unavailable goes to the next daemon announcing the capability', async () => {
    fetchPoolHosts.mockImplementation(async (id: string) => { if (id === A) throw fail(503, 'wwd_unavailable'); return [mlab()] })
    await st().readOnce()
    expect(fetchPoolHosts.mock.calls.map((c) => c[0])).toEqual([A, B])
    expect(st().sourceHostId).toBe(B)
    expect(st().error?.code).toBeUndefined()
  })

  it('every daemon failing empties the pool: the whole fleet falls back, and it recovers when one answers', async () => {
    fetchPoolHosts.mockImplementation(async () => { throw fail(503, 'wwd_unavailable') })
    await st().readOnce()
    expect(st().hosts).toEqual([])
    expect(st().sourceHostId).toBeNull()
    expect(st().error).toMatchObject({ code: 'wwd_unavailable' })
    fetchPoolHosts.mockResolvedValue([mlab()])
    await st().readOnce()
    expect(st().hosts).toHaveLength(1)
    expect(st().error).toBeNull()
    expect(st().sourceHostId).toBe(A)
  })

  it('busy retries the same source after 1–2 s and never tries the next daemon', async () => {
    fetchPoolHosts.mockImplementation(async () => { throw fail(429, 'busy') })
    const delay = await st().readOnce()
    expect(fetchPoolHosts).toHaveBeenCalledTimes(1)
    expect(delay).toBeGreaterThanOrEqual(1000)
    expect(delay).toBeLessThanOrEqual(2000)
  })

  it('busy shows its text only after three in a row', async () => {
    fetchPoolHosts.mockImplementation(async () => { throw fail(429, 'busy') })
    await st().readOnce(); await st().readOnce()
    expect(st().error).toBeNull()
    await st().readOnce()
    expect(st().error).toMatchObject({ code: 'busy' })
  })

  it('wwd_error backs off 2, 4, 8 s on the same source and keeps the last hosts', async () => {
    fetchPoolHosts.mockResolvedValueOnce([mlab()])
    await st().readOnce()
    fetchPoolHosts.mockImplementation(async () => { throw fail(502, 'wwd_error') })
    expect(await st().readOnce()).toBe(2000)
    expect(await st().readOnce()).toBe(4000)
    expect(await st().readOnce()).toBe(8000)
    expect(fetchPoolHosts.mock.calls.map((c) => c[0])).toEqual([A, A, A, A])
    expect(st().hosts).toHaveLength(1)
  })

  it('a success resets the back-off', async () => {
    fetchPoolHosts.mockImplementationOnce(async () => { throw fail(502, 'wwd_error') })
    await st().readOnce()
    fetchPoolHosts.mockResolvedValue([mlab()])
    expect(await st().readOnce()).toBe(POLL_MS)
    fetchPoolHosts.mockImplementationOnce(async () => { throw fail(502, 'wwd_error') })
    expect(await st().readOnce()).toBe(2000)
  })

  it('a code the table sends elsewhere (bad_request, forbidden) stays on the source and polls normally', async () => {
    fetchPoolHosts.mockImplementation(async () => { throw fail(400, 'bad_request') })
    expect(await st().readOnce()).toBe(POLL_MS)
    expect(fetchPoolHosts).toHaveBeenCalledTimes(1)
  })
})

describe('polling', () => {
  it('reads now, then every 5 s', async () => {
    fetchPoolHosts.mockResolvedValue([mlab()])
    st().start()
    await vi.advanceTimersByTimeAsync(0)
    expect(fetchPoolHosts).toHaveBeenCalledTimes(1)
    await vi.advanceTimersByTimeAsync(POLL_MS)
    expect(fetchPoolHosts).toHaveBeenCalledTimes(2)
    await vi.advanceTimersByTimeAsync(POLL_MS)
    expect(fetchPoolHosts).toHaveBeenCalledTimes(3)
  })

  it('a hidden window reads nothing; turning visible reads at once and then polls again', async () => {
    fetchPoolHosts.mockResolvedValue([mlab()])
    st().start()
    await vi.advanceTimersByTimeAsync(0)
    setVisibility('hidden')
    await vi.advanceTimersByTimeAsync(POLL_MS * 5)
    expect(fetchPoolHosts).toHaveBeenCalledTimes(1)
    setVisibility('visible')
    await vi.advanceTimersByTimeAsync(0)
    expect(fetchPoolHosts).toHaveBeenCalledTimes(2)
    await vi.advanceTimersByTimeAsync(POLL_MS)
    expect(fetchPoolHosts).toHaveBeenCalledTimes(3)
  })

  it('starting while hidden reads nothing until visible', async () => {
    visibility = 'hidden'
    fetchPoolHosts.mockResolvedValue([mlab()])
    st().start()
    await vi.advanceTimersByTimeAsync(POLL_MS * 2)
    expect(fetchPoolHosts).not.toHaveBeenCalled()
    setVisibility('visible')
    await vi.advanceTimersByTimeAsync(0)
    expect(fetchPoolHosts).toHaveBeenCalledTimes(1)
  })

  it('stop clears the timer and the listener', async () => {
    fetchPoolHosts.mockResolvedValue([mlab()])
    st().start()
    await vi.advanceTimersByTimeAsync(0)
    st().stop()
    await vi.advanceTimersByTimeAsync(POLL_MS * 3)
    setVisibility('hidden'); setVisibility('visible')
    await vi.advanceTimersByTimeAsync(POLL_MS)
    expect(fetchPoolHosts).toHaveBeenCalledTimes(1)
  })

  it('start twice does not double the poll', async () => {
    fetchPoolHosts.mockResolvedValue([mlab()])
    st().start(); st().start()
    await vi.advanceTimersByTimeAsync(POLL_MS)
    expect(fetchPoolHosts).toHaveBeenCalledTimes(2)
  })
})
