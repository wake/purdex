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
  vi.setSystemTime(facts.at + 1_000) // the sample's facts are fresh at this time
  fetchPoolHosts.mockReset()
  usePoolStore.getState().stop()
  usePoolStore.getState().reset()
  visibility = 'visible'
  Object.defineProperty(document, 'visibilityState', { configurable: true, get: () => visibility })
  useHostStore.setState({
    hosts: { [A]: hostCfg(A), [B]: hostCfg(B) }, hostOrder: [A, B], activeHostId: A,
    runtime: { [A]: { status: 'connected' }, [B]: { status: 'connected' } },
  } as never)
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

  it('a daemon that is not connected is skipped even though its capability is cached', async () => {
    useHostStore.setState({ runtime: { [A]: { status: 'disconnected' }, [B]: { status: 'connected' } } } as never)
    fetchPoolHosts.mockResolvedValue([mlab()])
    await st().readOnce()
    expect(fetchPoolHosts.mock.calls.map((c) => c[0])).toEqual([B])
  })

  // `oldDaemon` is a verdict about the version, so it needs evidence: a connected daemon whose capabilities were read and that does not
  // announce pool.v1, with none announcing it anywhere. Disconnected or not yet probed is "not known", never "old".
  it('every announcing daemon being disconnected is not the old-daemon state, and nothing is read', async () => {
    useHostStore.setState({ runtime: { [A]: { status: 'disconnected' }, [B]: { status: 'reconnecting' } } } as never)
    await st().readOnce()
    expect(fetchPoolHosts).not.toHaveBeenCalled()
    expect(st().oldDaemon).toBe(false)
  })

  it('an announcing daemon that is disconnected while another, connected one does not announce: not old either', async () => {
    useNexHostStore.setState({ byHost: { [A]: entry(['pool.v1']), [B]: entry([]) } } as never)
    useHostStore.setState({ runtime: { [A]: { status: 'disconnected' }, [B]: { status: 'connected' } } } as never)
    await st().readOnce()
    expect(st().oldDaemon).toBe(false)
  })

  it('capabilities not read yet (the startup window) is not the old-daemon state', async () => {
    useNexHostStore.setState({ byHost: {} } as never)
    await st().readOnce()
    expect(st().oldDaemon).toBe(false)
    useNexHostStore.setState({ byHost: { [A]: { phase: 'loading' }, [B]: { phase: 'loading' } } } as never)
    await st().readOnce()
    expect(st().oldDaemon).toBe(false)
  })

  it('a daemon without Nexen (phase unavailable) whose info answered still counts as read', async () => {
    useNexHostStore.setState({ byHost: { [A]: { phase: 'unavailable', daemonCapabilities: [] }, [B]: { phase: 'unavailable', daemonCapabilities: [] } } } as never)
    await st().readOnce()
    expect(st().oldDaemon).toBe(true)
  })

  it('no host connected at all is not the old-daemon state', async () => {
    useHostStore.setState({ runtime: {} } as never)
    useNexHostStore.setState({ byHost: { [A]: entry([]), [B]: entry([]) } } as never)
    await st().readOnce()
    expect(st().oldDaemon).toBe(false)
  })

  it('connected daemons read and none announcing pool.v1 is the old-daemon state, and nothing is read', async () => {
    useNexHostStore.setState({ byHost: { [A]: entry([]), [B]: entry([]) } } as never)
    await st().readOnce()
    expect(fetchPoolHosts).not.toHaveBeenCalled()
    expect(st().oldDaemon).toBe(true)
    expect(st().sourceHostId).toBeNull()
  })

  // MON-1b (spec M21): the time of the last facts is kept by the wwd host_id, which an alias change does not move.
  it('keeps the last facts time by host_id, and keeps it when the host cannot be read', async () => {
    const id = mlab().host_id
    fetchPoolHosts.mockResolvedValue([mlab()])
    await st().readOnce()
    expect(st().lastFactsAt[id]).toBe(facts.at)
    fetchPoolHosts.mockResolvedValue([mlab({ reachable: false, facts: null, liveness: 'host_down' })])
    await st().readOnce()
    expect(st().lastFactsAt[id]).toBe(facts.at)
    fetchPoolHosts.mockResolvedValue([mlab({ alias: 'renamed' })])
    await st().readOnce()
    expect(Object.keys(st().lastFactsAt)).toEqual([id])
  })

  it('a host that is not reachable never had a facts time recorded', async () => {
    fetchPoolHosts.mockResolvedValue([mlab({ reachable: false })])
    await st().readOnce()
    expect(st().lastFactsAt).toEqual({})
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

  it('a code the table sends elsewhere (bad_request, forbidden) stays on the source and polls normally, and is published', async () => {
    fetchPoolHosts.mockImplementation(async () => { throw fail(400, 'bad_request') })
    expect(await st().readOnce()).toBe(POLL_MS)
    expect(fetchPoolHosts).toHaveBeenCalledTimes(1)
    expect(st().error).toMatchObject({ code: 'bad_request' })
    fetchPoolHosts.mockImplementation(async () => { throw fail(403, 'forbidden') })
    await st().readOnce()
    expect(st().error).toMatchObject({ code: 'forbidden' })
  })

  it('a different error replaces the old one and ends the busy count', async () => {
    fetchPoolHosts.mockImplementation(async () => { throw fail(429, 'busy') })
    await st().readOnce(); await st().readOnce(); await st().readOnce()
    expect(st().error).toMatchObject({ code: 'busy' })
    fetchPoolHosts.mockImplementation(async () => { throw fail(400, 'bad_request') })
    await st().readOnce()
    expect(st().error).toMatchObject({ code: 'bad_request' })
    fetchPoolHosts.mockImplementation(async () => { throw fail(429, 'busy') })
    await st().readOnce()
    expect(st().error).toMatchObject({ code: 'bad_request' }) // one busy is not three in a row
  })

  describe('a source that answered busy or failed is kept until it recovers', () => {
    const aDown = (id: string, then: () => Promise<PoolHost[]>) => (id === A ? Promise.reject(fail(503, 'wwd_unavailable')) : then())

    it('busy on the second daemon after the first was unavailable: the next read goes to the second only', async () => {
      fetchPoolHosts.mockImplementation((id: string) => aDown(id, () => Promise.reject(fail(429, 'busy'))))
      await st().readOnce()
      fetchPoolHosts.mockClear()
      await st().readOnce()
      expect(fetchPoolHosts.mock.calls.map((c) => c[0])).toEqual([B])
    })

    it('wwd_error on the second daemon is retried on it alone, with the back-off growing', async () => {
      fetchPoolHosts.mockImplementation((id: string) => aDown(id, () => Promise.reject(fail(502, 'wwd_error'))))
      expect(await st().readOnce()).toBe(2000)
      fetchPoolHosts.mockClear()
      expect(await st().readOnce()).toBe(4000)
      expect(fetchPoolHosts.mock.calls.map((c) => c[0])).toEqual([B])
    })

    it('once the kept source answers, reading starts from the first daemon again', async () => {
      fetchPoolHosts.mockImplementation((id: string) => aDown(id, () => Promise.reject(fail(429, 'busy'))))
      await st().readOnce()
      fetchPoolHosts.mockImplementation(async () => [mlab()])
      await st().readOnce()
      fetchPoolHosts.mockClear()
      await st().readOnce()
      expect(fetchPoolHosts.mock.calls.map((c) => c[0])).toEqual([A])
    })

    it('if the kept source then says next_source, the others are tried', async () => {
      fetchPoolHosts.mockImplementation((id: string) => aDown(id, () => Promise.reject(fail(429, 'busy'))))
      await st().readOnce()
      fetchPoolHosts.mockImplementation(async () => { throw fail(503, 'wwd_unavailable') })
      fetchPoolHosts.mockClear()
      await st().readOnce()
      expect(fetchPoolHosts.mock.calls.map((c) => c[0])).toEqual([B, A])
    })

    it('the busy count belongs to its source: A busy twice, then A unavailable and B busy once, shows nothing', async () => {
      fetchPoolHosts.mockImplementation(async () => { throw fail(429, 'busy') })
      await st().readOnce(); await st().readOnce()
      fetchPoolHosts.mockImplementation((id: string) => aDown(id, () => Promise.reject(fail(429, 'busy'))))
      await st().readOnce()
      expect(st().error).toBeNull()
    })

    it('the back-off belongs to its source: A failing twice, then B failing once waits 2 s, not 8', async () => {
      fetchPoolHosts.mockImplementation(async () => { throw fail(502, 'wwd_error') })
      await st().readOnce(); await st().readOnce()
      fetchPoolHosts.mockImplementation((id: string) => aDown(id, () => Promise.reject(fail(502, 'wwd_error'))))
      expect(await st().readOnce()).toBe(2000)
    })

    it('every daemon disconnecting forgets the kept source: when both return, A is read first', async () => {
      fetchPoolHosts.mockImplementation((id: string) => aDown(id, () => Promise.reject(fail(429, 'busy'))))
      await st().readOnce()
      useHostStore.setState({ runtime: {} } as never)
      await st().readOnce()
      useHostStore.setState({ runtime: { [A]: { status: 'connected' }, [B]: { status: 'connected' } } } as never)
      fetchPoolHosts.mockClear()
      fetchPoolHosts.mockImplementation(async () => [mlab()])
      await st().readOnce()
      expect(fetchPoolHosts.mock.calls.map((c) => c[0])).toEqual([A])
    })

    it('a kept source that is no longer connected is forgotten', async () => {
      fetchPoolHosts.mockImplementation((id: string) => aDown(id, () => Promise.reject(fail(429, 'busy'))))
      await st().readOnce()
      useHostStore.setState({ runtime: { [A]: { status: 'connected' }, [B]: { status: 'disconnected' } } } as never)
      fetchPoolHosts.mockClear()
      fetchPoolHosts.mockImplementation(async () => [mlab()])
      await st().readOnce()
      expect(fetchPoolHosts.mock.calls.map((c) => c[0])).toEqual([A])
    })
  })
})

describe('a read that was overtaken', () => {
  const deferred = () => { let resolve!: (h: PoolHost[]) => void; const p = new Promise<PoolHost[]>((r) => { resolve = r }); return { p, resolve } }

  it('stop() while a read is in flight: its answer is dropped', async () => {
    const d = deferred()
    fetchPoolHosts.mockReturnValue(d.p)
    st().start()
    await vi.advanceTimersByTimeAsync(0)
    st().stop()
    d.resolve([mlab()])
    await vi.advanceTimersByTimeAsync(0)
    expect(st().hosts).toEqual([])
  })

  it('hidden then visible: the old read finishing last does not overwrite the newer one', async () => {
    const old = deferred()
    const fresh = deferred()
    fetchPoolHosts.mockReturnValueOnce(old.p).mockReturnValueOnce(fresh.p)
    st().start()
    await vi.advanceTimersByTimeAsync(0)
    setVisibility('hidden')
    setVisibility('visible')
    await vi.advanceTimersByTimeAsync(0)
    fresh.resolve([mlab({ alias: 'new' })])
    await vi.advanceTimersByTimeAsync(0)
    old.resolve([mlab({ alias: 'old' })])
    await vi.advanceTimersByTimeAsync(0)
    expect(st().hosts.map((h) => h.alias)).toEqual(['new'])
  })

  it('a failure of an overtaken read does not set an error either', async () => {
    let reject!: (e: unknown) => void
    fetchPoolHosts.mockReturnValue(new Promise((_, r) => { reject = r }))
    st().start()
    await vi.advanceTimersByTimeAsync(0)
    st().stop()
    reject(fail(503, 'wwd_unavailable'))
    await vi.advanceTimersByTimeAsync(0)
    expect(st().error).toBeNull()
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

// WWD-7a-1c: a source announcing pool.events.v1 stops the poll; reads follow pool.changed, a reconnect of the source and the window turning visible.
describe('pool.events.v1', () => {
  const EV = 'pool.events.v1'
  const announce = (a: string[], b: string[] = ['pool.v1']) => useNexHostStore.setState({ byHost: { [A]: entry(a), [B]: entry(b) } } as never)
  const settle = () => vi.advanceTimersByTimeAsync(0)
  // The sample's facts count as fresh from MAX_SKEW_MS before their time, so their expiry (the one timer event mode still arms) is 40 s away:
  // these tests are about events versus the poll, not about freshness (the M20 cases below set their own clock).
  beforeEach(() => { vi.setSystemTime(facts.at - 10_000) })

  it('a source with the capability is read once and then not on a timer', async () => {
    announce(['pool.v1', EV])
    fetchPoolHosts.mockResolvedValue([mlab()])
    st().start()
    await settle()
    await vi.advanceTimersByTimeAsync(POLL_MS * 6)
    expect(fetchPoolHosts).toHaveBeenCalledTimes(1)
  })

  // MON-1b (spec M20): `liveness_at` moves without a pool.changed, so while any host is not ok the 5 s poll stays even in event mode.
  describe('a host that is not ok keeps the 5 s read (M20)', () => {
    const down = () => mlab({ host_id: 'a0b1c2d3e4f50617', alias: 'mserv', facts: null, liveness: 'wwd_down' })

    it('all hosts ok: one read, no timer', async () => {
      announce(['pool.v1', EV])
      fetchPoolHosts.mockResolvedValue([mlab({ liveness: 'ok' })])
      st().start()
      await settle()
      await vi.advanceTimersByTimeAsync(POLL_MS * 4)
      expect(fetchPoolHosts).toHaveBeenCalledTimes(1)
    })

    // codex R1 (P2): a peer can keep saying `ok` while its facts stop advancing, and nothing emits pool.changed for that.
    it('facts that stop advancing are read again when they stop being fresh, then every 5 s (stalled is not ok)', async () => {
      vi.setSystemTime(facts.at + 5_000)
      announce(['pool.v1', EV])
      fetchPoolHosts.mockResolvedValue([mlab({ liveness: 'ok' })])
      st().start()
      await settle()
      expect(fetchPoolHosts).toHaveBeenCalledTimes(1)
      await vi.advanceTimersByTimeAsync(24_000)
      expect(fetchPoolHosts).toHaveBeenCalledTimes(1)
      await vi.advanceTimersByTimeAsync(2_000) // past facts.at + 30 s
      expect(fetchPoolHosts).toHaveBeenCalledTimes(2)
      await vi.advanceTimersByTimeAsync(POLL_MS)
      expect(fetchPoolHosts).toHaveBeenCalledTimes(3)
    })

    // codex attack (high): facts dated far ahead of this clock must not count as fresh, or the host stays ok and nothing is read again.
    it('facts dated far in the future are not ok: the 5 s read stays', async () => {
      vi.setSystemTime(1_000_000)
      announce(['pool.v1', EV])
      fetchPoolHosts.mockResolvedValue([mlab({ liveness: 'ok' })])
      st().start()
      await settle()
      await vi.advanceTimersByTimeAsync(POLL_MS * 2)
      expect(fetchPoolHosts.mock.calls.length).toBeGreaterThanOrEqual(3)
    })

    it('an event while the expiry timer waits reads at once and re-arms it', async () => {
      vi.setSystemTime(facts.at + 5_000)
      announce(['pool.v1', EV])
      fetchPoolHosts.mockResolvedValue([mlab({ liveness: 'ok' })])
      st().start()
      await settle()
      st().poolEvent(A)
      await settle()
      expect(fetchPoolHosts).toHaveBeenCalledTimes(2)
      await vi.advanceTimersByTimeAsync(26_000)
      expect(fetchPoolHosts).toHaveBeenCalledTimes(3)
    })

    it('one host not ok: read again every 5 s; once it is ok again the next read stops the timer', async () => {
      announce(['pool.v1', EV])
      fetchPoolHosts.mockResolvedValue([mlab(), down()])
      st().start()
      await settle()
      await vi.advanceTimersByTimeAsync(POLL_MS)
      await vi.advanceTimersByTimeAsync(POLL_MS)
      expect(fetchPoolHosts).toHaveBeenCalledTimes(3)
      fetchPoolHosts.mockResolvedValue([mlab(), { ...down(), liveness: 'ok', facts }])
      await vi.advanceTimersByTimeAsync(POLL_MS)
      expect(fetchPoolHosts).toHaveBeenCalledTimes(4)
      await vi.advanceTimersByTimeAsync(POLL_MS * 4)
      expect(fetchPoolHosts).toHaveBeenCalledTimes(4)
    })
  })

  it('without the capability it still polls', async () => {
    announce(['pool.v1'])
    fetchPoolHosts.mockResolvedValue([mlab()])
    st().start()
    await vi.advanceTimersByTimeAsync(POLL_MS * 2)
    expect(fetchPoolHosts.mock.calls.length).toBeGreaterThanOrEqual(3)
  })

  it('pool.changed from the source reads again; from another daemon it does not', async () => {
    announce(['pool.v1', EV])
    fetchPoolHosts.mockResolvedValue([mlab()])
    st().start()
    await settle()
    st().poolEvent(B)
    await settle()
    expect(fetchPoolHosts).toHaveBeenCalledTimes(1)
    st().poolEvent(A)
    await settle()
    expect(fetchPoolHosts).toHaveBeenCalledTimes(2)
    expect(fetchPoolHosts).toHaveBeenLastCalledWith(A)
  })

  it('an event while a read is under way is read once more after it, not in parallel', async () => {
    announce(['pool.v1', EV])
    let release: (h: PoolHost[]) => void = () => {}
    fetchPoolHosts.mockReturnValueOnce(Promise.resolve([mlab()]))
    st().start()
    await settle()
    fetchPoolHosts.mockImplementationOnce(() => new Promise<PoolHost[]>((r) => { release = r }))
    st().poolEvent(A)
    await settle()
    st().poolEvent(A); st().poolEvent(A) // two more while the second read is open
    await settle()
    expect(fetchPoolHosts).toHaveBeenCalledTimes(2)
    fetchPoolHosts.mockResolvedValue([mlab()])
    release([mlab()])
    await settle(); await settle()
    expect(fetchPoolHosts).toHaveBeenCalledTimes(3)
  })

  it('a hidden window reads nothing on an event, and reads at once when it turns visible', async () => {
    announce(['pool.v1', EV])
    fetchPoolHosts.mockResolvedValue([mlab()])
    st().start()
    await settle()
    setVisibility('hidden')
    st().poolEvent(A)
    await vi.advanceTimersByTimeAsync(POLL_MS * 2)
    expect(fetchPoolHosts).toHaveBeenCalledTimes(1)
    setVisibility('visible')
    await settle()
    expect(fetchPoolHosts).toHaveBeenCalledTimes(2)
    await vi.advanceTimersByTimeAsync(POLL_MS * 3) // and no timer after it
    expect(fetchPoolHosts).toHaveBeenCalledTimes(2)
  })

  it('a failing read retries on its own timer even with the capability, and events drive it again once it recovers', async () => {
    announce(['pool.v1', EV])
    fetchPoolHosts.mockResolvedValueOnce([mlab()])
    st().start()
    await settle()
    fetchPoolHosts.mockRejectedValue(fail(502, 'wwd_error'))
    st().poolEvent(A)
    await settle()
    expect(st().error?.code).toBe('wwd_error')
    const before = fetchPoolHosts.mock.calls.length
    await vi.advanceTimersByTimeAsync(40_000)
    expect(fetchPoolHosts.mock.calls.length).toBeGreaterThan(before)
    fetchPoolHosts.mockReset(); fetchPoolHosts.mockImplementation(async () => [mlab({ facts: { ...facts, at: Date.now() } })]) // facts of the moment of the read
    await vi.advanceTimersByTimeAsync(40_000)
    expect(st().error).toBeNull()
    const settled = fetchPoolHosts.mock.calls.length
    await vi.advanceTimersByTimeAsync(POLL_MS * 5)
    // no 5 s poll (that would be 5 more reads); at most the one read when the fresh facts expire (they last 30 s from their read)
    expect(fetchPoolHosts.mock.calls.length).toBeLessThanOrEqual(settled + 1)
  })

  // codex R1 (P1): `busy` publishes no error until its third time in a row, but it is a failure: its 1–2 s retry must still run
  it('a busy answer from an events source is retried on its timer, though no error is published yet', async () => {
    announce(['pool.v1', EV])
    fetchPoolHosts.mockResolvedValueOnce([mlab()])
    st().start()
    await settle()
    fetchPoolHosts.mockRejectedValueOnce(fail(429, 'busy'))
    fetchPoolHosts.mockResolvedValue([mlab()])
    st().poolEvent(A)
    await settle()
    expect(st().error).toBeNull() // the first busy shows nothing
    expect(fetchPoolHosts).toHaveBeenCalledTimes(2)
    await vi.advanceTimersByTimeAsync(2500)
    expect(fetchPoolHosts).toHaveBeenCalledTimes(3) // the retry came without any event
    await vi.advanceTimersByTimeAsync(POLL_MS * 4)
    expect(fetchPoolHosts).toHaveBeenCalledTimes(3) // and once it is answered the events drive it again
  })

  // codex R2 (high): a timer that fired is no timer
  it('after a retry timer fired and the source recovered, losing the capability brings the poll back at once', async () => {
    announce(['pool.v1', EV])
    fetchPoolHosts.mockResolvedValueOnce([mlab()])
    st().start()
    await settle()
    fetchPoolHosts.mockRejectedValueOnce(fail(502, 'wwd_error'))
    fetchPoolHosts.mockResolvedValue([mlab()])
    st().poolEvent(A)
    await settle()
    await vi.advanceTimersByTimeAsync(3000) // the back-off retry fires and is answered
    const calls = fetchPoolHosts.mock.calls.length
    await vi.advanceTimersByTimeAsync(POLL_MS * 2)
    expect(fetchPoolHosts.mock.calls.length).toBe(calls) // events drive it: no timer
    announce(['pool.v1'])
    await settle()
    expect(fetchPoolHosts.mock.calls.length).toBe(calls + 1) // read at once
    await vi.advanceTimersByTimeAsync(POLL_MS)
    expect(fetchPoolHosts.mock.calls.length).toBe(calls + 2) // and polling
  })

  // codex R2 (high): the source is the FIRST connected pool daemon, as a read picks it
  it('when an earlier daemon in host order comes back, the store moves to it even though both announce events', async () => {
    announce(['pool.v1', EV], ['pool.v1', EV])
    useHostStore.setState({ runtime: { [A]: { status: 'disconnected' }, [B]: { status: 'connected' } } } as never)
    fetchPoolHosts.mockResolvedValue([mlab()])
    st().start()
    await settle()
    expect(fetchPoolHosts).toHaveBeenLastCalledWith(B)
    useHostStore.setState({ runtime: { [A]: { status: 'connected' }, [B]: { status: 'connected' } } } as never)
    await settle()
    expect(fetchPoolHosts).toHaveBeenLastCalledWith(A)
    st().poolEvent(B) // B's events are no longer the source's
    await settle()
    expect(fetchPoolHosts.mock.calls.filter((c) => c[0] === B)).toHaveLength(1)
  })

  it('the same switch during a read is not lost', async () => {
    announce(['pool.v1', EV], ['pool.v1', EV])
    useHostStore.setState({ runtime: { [A]: { status: 'disconnected' }, [B]: { status: 'connected' } } } as never)
    let release: (h: PoolHost[]) => void = () => {}
    fetchPoolHosts.mockImplementationOnce(() => new Promise<PoolHost[]>((r) => { release = r }))
    fetchPoolHosts.mockResolvedValue([mlab()])
    st().start()
    await settle()
    useHostStore.setState({ runtime: { [A]: { status: 'connected' }, [B]: { status: 'connected' } } } as never)
    await settle()
    release([mlab()])
    await settle(); await settle()
    expect(fetchPoolHosts).toHaveBeenLastCalledWith(A)
  })

  it('a change that does not move the first source during a read costs no extra read', async () => {
    announce(['pool.v1', EV], ['pool.v1', EV])
    let release: (h: PoolHost[]) => void = () => {}
    fetchPoolHosts.mockImplementationOnce(() => new Promise<PoolHost[]>((r) => { release = r }))
    st().start()
    await settle()
    announce(['pool.v1', EV], ['pool.v1', EV, 'other']) // the Nex cache moves; A is still the first source
    await settle()
    release([mlab()])
    await settle(); await settle()
    expect(fetchPoolHosts).toHaveBeenCalledTimes(1)
  })

  it('the source losing the capability brings the poll back', async () => {
    announce(['pool.v1', EV])
    fetchPoolHosts.mockResolvedValue([mlab()])
    st().start()
    await settle()
    await vi.advanceTimersByTimeAsync(POLL_MS * 2)
    expect(fetchPoolHosts).toHaveBeenCalledTimes(1)
    announce(['pool.v1'])
    await vi.advanceTimersByTimeAsync(POLL_MS * 2)
    expect(fetchPoolHosts.mock.calls.length).toBeGreaterThanOrEqual(3)
  })

  it('a switch to a source without the capability polls again', async () => {
    announce(['pool.v1', EV], ['pool.v1'])
    fetchPoolHosts.mockResolvedValue([mlab()])
    st().start()
    await settle()
    useHostStore.setState({ runtime: { [A]: { status: 'disconnected' }, [B]: { status: 'connected' } } } as never)
    await vi.advanceTimersByTimeAsync(POLL_MS * 3)
    expect(fetchPoolHosts.mock.calls.length).toBeGreaterThanOrEqual(2)
    expect(fetchPoolHosts).toHaveBeenLastCalledWith(B)
  })

  it('stop() leaves nothing subscribed: a later capability change reads nothing', async () => {
    announce(['pool.v1', EV])
    fetchPoolHosts.mockResolvedValue([mlab()])
    st().start()
    await settle()
    st().stop()
    announce(['pool.v1'])
    await vi.advanceTimersByTimeAsync(POLL_MS * 3)
    expect(fetchPoolHosts).toHaveBeenCalledTimes(1)
  })
})
