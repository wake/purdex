// spa/src/lib/relay/fresh-sync.test.ts — the `relay.fresh` event and the connect-time GET (relay v2 contract 5): the flag gates
// both, an event and an answer meet in the store's generation, a removed or re-pointed host forgets its list.
import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest'
import { startRelayFresh } from './fresh-sync'
import { handleRelayFreshEvent } from './fresh-ws'
import type { RelayFresh } from './fresh'
import { useRelayFreshStore } from '../../stores/useRelayFreshStore'
import { useHostStore } from '../../stores/useHostStore'
import { useNexHostStore } from '../../stores/useNexHostStore'

vi.mock('./fresh', async (orig) => ({ ...(await orig<typeof import('./fresh')>()), fetchRelayFresh: vi.fn() }))
import { fetchRelayFresh } from './fresh'

const A = 'h-a'
const B = 'h-b'
const cfg = (id: string, over: Record<string, unknown> = {}) => ({ id, name: id, ip: '100.64.0.2', port: 7860, token: 't', order: 0, ...over })
const fresh = (id: string, over: Partial<RelayFresh> = {}): RelayFresh => ({ session_id: id, ref: '_aaaaaa', op_id: `op-${id}`, path: `/p/${id}.md`, written_at: 1000, code: `c-${id}`, ...over })
const ids = (host: string) => (useRelayFreshStore.getState().byHost[host] ?? []).map((s) => s.session_id)
const setStatus = (id: string, status: 'connected' | 'disconnected') => useHostStore.setState((s) => ({ runtime: { ...s.runtime, [id]: { status } } }) as never)
const setCaps = (id: string, caps: string[] | undefined) => useNexHostStore.setState((s) => ({
  byHost: { ...s.byHost, [id]: { info: null, capabilities: null, phase: 'unavailable', error: null, fetchedAt: 1, generation: 1, fingerprint: 'x', ...(caps ? { daemonCapabilities: caps } : {}) } },
}) as never)
const wire = (op: string, sessions: unknown[]) => JSON.stringify({ op, sessions })
const flush = async () => { await Promise.resolve(); await Promise.resolve(); await Promise.resolve() }
function deferred<T>() {
  let resolve!: (v: T) => void
  let reject!: (e: unknown) => void
  const promise = new Promise<T>((a, b) => { resolve = a; reject = b })
  return { promise, resolve, reject }
}

const realEnsure = useNexHostStore.getState().ensure
const ensure = vi.fn(() => Promise.resolve())
let stop: (() => void) | undefined

beforeEach(() => {
  vi.mocked(fetchRelayFresh).mockReset()
  ensure.mockClear()
  useRelayFreshStore.getState().reset()
  useNexHostStore.setState({ byHost: {}, ensure } as never)
  useHostStore.setState({ hosts: { [A]: cfg(A), [B]: cfg(B) }, hostOrder: [A, B], activeHostId: A, runtime: { [A]: { status: 'disconnected' }, [B]: { status: 'disconnected' } } } as never)
})
afterEach(() => {
  stop?.()
  stop = undefined
  useNexHostStore.setState({ byHost: {}, ensure: realEnsure } as never)
  vi.restoreAllMocks()
})

describe('the relay.fresh event', () => {
  it('is ignored on a host that does not list relay.half.v1 (and on one whose info is not read yet)', () => {
    handleRelayFreshEvent(A, wire('snapshot', [fresh('a')]))
    setCaps(A, ['fleet.v1'])
    handleRelayFreshEvent(A, wire('snapshot', [fresh('a')]))
    expect(useRelayFreshStore.getState().byHost).toEqual({})
  })

  it('a snapshot and a changed each replace the host\'s whole list (not a delta)', () => {
    setCaps(A, ['relay.half.v1'])
    handleRelayFreshEvent(A, wire('snapshot', [fresh('a'), fresh('b')]))
    expect(ids(A)).toEqual(['a', 'b'])
    handleRelayFreshEvent(A, wire('changed', [fresh('c')]))
    expect(ids(A)).toEqual(['c'])
    handleRelayFreshEvent(A, wire('changed', []))
    expect(ids(A)).toEqual([])
  })

  it('a bad frame is dropped whole: the list stays, one warning', () => {
    const warn = vi.spyOn(console, 'warn').mockImplementation(() => {})
    setCaps(A, ['relay.half.v1'])
    handleRelayFreshEvent(A, wire('snapshot', [fresh('a')]))
    handleRelayFreshEvent(A, '{not json')
    handleRelayFreshEvent(A, wire('delta', [fresh('z')]))
    handleRelayFreshEvent(A, JSON.stringify({ op: 'changed', sessions: 'x' }))
    expect(ids(A)).toEqual(['a'])
    expect(warn).toHaveBeenCalledTimes(3)
  })

  it('a bad entry is dropped and the others in the frame still land', () => {
    vi.spyOn(console, 'warn').mockImplementation(() => {})
    setCaps(A, ['relay.half.v1'])
    handleRelayFreshEvent(A, wire('snapshot', [fresh('a'), { session_id: 'x' }, fresh('b')]))
    expect(ids(A)).toEqual(['a', 'b'])
  })
})

describe('startRelayFresh: the connect-time GET', () => {
  it('a connected host with the flag gets one GET and its answer is the list', async () => {
    setCaps(A, ['relay.half.v1'])
    vi.mocked(fetchRelayFresh).mockResolvedValue([fresh('a')])
    stop = startRelayFresh()
    setStatus(A, 'connected')
    await flush()
    expect(fetchRelayFresh).toHaveBeenCalledTimes(1)
    expect(fetchRelayFresh).toHaveBeenCalledWith(A)
    expect(ids(A)).toEqual(['a'])
  })

  it('a host without the flag is never asked: no GET, the info is looked at instead', async () => {
    setCaps(A, ['fleet.v1'])
    stop = startRelayFresh()
    setStatus(A, 'connected')
    await flush()
    expect(fetchRelayFresh).not.toHaveBeenCalled()
    expect(ensure).toHaveBeenCalledWith(A)
    expect(useRelayFreshStore.getState().byHost).toEqual({})
  })

  it('a host that is not connected is not asked', async () => {
    setCaps(A, ['relay.half.v1'])
    stop = startRelayFresh()
    await flush()
    expect(fetchRelayFresh).not.toHaveBeenCalled()
  })

  it('a host already connected when the driver starts is asked', async () => {
    setCaps(A, ['relay.half.v1'])
    setStatus(A, 'connected')
    vi.mocked(fetchRelayFresh).mockResolvedValue([fresh('a')])
    stop = startRelayFresh()
    await flush()
    expect(ids(A)).toEqual(['a'])
  })

  it('the flag turning true after the connect reads the list then (the first snapshot was ignored without it)', async () => {
    stop = startRelayFresh()
    setStatus(A, 'connected')
    await flush()
    expect(ensure).toHaveBeenCalledWith(A)
    handleRelayFreshEvent(A, wire('snapshot', [fresh('lost')])) // flag unknown: ignored
    vi.mocked(fetchRelayFresh).mockResolvedValue([fresh('a')])
    setCaps(A, ['relay.half.v1'])
    await flush()
    expect(fetchRelayFresh).toHaveBeenCalledTimes(1)
    expect(ids(A)).toEqual(['a'])
  })

  it('every reconnect reads again', async () => {
    setCaps(A, ['relay.half.v1'])
    vi.mocked(fetchRelayFresh).mockResolvedValue([fresh('a')])
    stop = startRelayFresh()
    setStatus(A, 'connected')
    await flush()
    setStatus(A, 'disconnected')
    await flush()
    expect(fetchRelayFresh).toHaveBeenCalledTimes(1)
    vi.mocked(fetchRelayFresh).mockResolvedValue([fresh('b')])
    setStatus(A, 'connected')
    await flush()
    expect(fetchRelayFresh).toHaveBeenCalledTimes(2)
    expect(ids(A)).toEqual(['b'])
  })

  it('a failed GET throws nothing and keeps the list shown', async () => {
    setCaps(A, ['relay.half.v1'])
    useRelayFreshStore.getState().apply(A, [fresh('keep')])
    vi.mocked(fetchRelayFresh).mockRejectedValue(new Error('boom'))
    stop = startRelayFresh()
    setStatus(A, 'connected')
    await flush()
    expect(ids(A)).toEqual(['keep'])
  })

  it('an event that comes while the GET is out wins: the older answer does not overwrite it', async () => {
    setCaps(A, ['relay.half.v1'])
    const d = deferred<RelayFresh[]>()
    vi.mocked(fetchRelayFresh).mockReturnValue(d.promise)
    stop = startRelayFresh()
    setStatus(A, 'connected')
    await flush()
    handleRelayFreshEvent(A, wire('changed', [fresh('event')]))
    d.resolve([fresh('older-get')])
    await flush()
    expect(ids(A)).toEqual(['event'])
  })

  it('an answer that lands after the flag was lost is dropped', async () => {
    setCaps(A, ['relay.half.v1'])
    const d = deferred<RelayFresh[]>()
    vi.mocked(fetchRelayFresh).mockReturnValue(d.promise)
    stop = startRelayFresh()
    setStatus(A, 'connected')
    await flush()
    setCaps(A, ['fleet.v1'])
    d.resolve([fresh('a')])
    await flush()
    expect(useRelayFreshStore.getState().byHost).toEqual({})
  })

  it('an answer for a host that was re-pointed while it was out is dropped; the new daemon\'s flag is asked, not the old one\'s trusted', async () => {
    setCaps(A, ['relay.half.v1'])
    const d = deferred<RelayFresh[]>()
    vi.mocked(fetchRelayFresh).mockReturnValueOnce(d.promise).mockResolvedValue([fresh('new-daemon')])
    stop = startRelayFresh()
    setStatus(A, 'connected')
    await flush()
    ensure.mockClear()
    useHostStore.setState((s) => ({ hosts: { ...s.hosts, [A]: cfg(A, { ip: '9.9.9.9' }) } }) as never)
    await flush()
    d.resolve([fresh('old-daemon')])
    await flush()
    // the cached flag may be the old daemon's: nothing is read on it, the new daemon's /api/info is asked (re-point.test.ts
    // follows the answer), and the old daemon's late answer lands nowhere
    expect(ensure).toHaveBeenCalledWith(A)
    expect(fetchRelayFresh).toHaveBeenCalledTimes(1)
    expect(useRelayFreshStore.getState().byHost).toEqual({})
  })
})

describe('startRelayFresh: when a list goes', () => {
  beforeEach(() => {
    setCaps(A, ['relay.half.v1'])
    setCaps(B, ['relay.half.v1'])
    useRelayFreshStore.getState().apply(A, [fresh('a')])
    useRelayFreshStore.getState().apply(B, [fresh('b')])
  })

  it('a host that loses the flag has its list dropped; the others keep theirs', () => {
    stop = startRelayFresh()
    setCaps(A, ['fleet.v1'])
    expect(Object.keys(useRelayFreshStore.getState().byHost)).toEqual([B])
  })

  it('a host whose info could not be read (no capabilities) has its list dropped', () => {
    stop = startRelayFresh()
    setCaps(B, undefined)
    expect(Object.keys(useRelayFreshStore.getState().byHost)).toEqual([A])
  })

  it('a removed host forgets its list', () => {
    stop = startRelayFresh()
    useHostStore.setState({ hosts: { [B]: cfg(B) }, hostOrder: [B] } as never)
    expect(Object.keys(useRelayFreshStore.getState().byHost)).toEqual([B])
  })

  it('a re-pointed host (endpoint or token) forgets its list; a rename does not', () => {
    stop = startRelayFresh()
    useHostStore.setState((s) => ({ hosts: { ...s.hosts, [A]: { ...s.hosts[A], name: 'renamed' } } }) as never)
    expect(ids(A)).toEqual(['a'])
    useHostStore.setState((s) => ({ hosts: { ...s.hosts, [A]: cfg(A, { token: 'other' }) } }) as never)
    expect(Object.keys(useRelayFreshStore.getState().byHost)).toEqual([B])
  })

  it('stops when the returned function is called', () => {
    stop = startRelayFresh()
    stop()
    stop = undefined
    useHostStore.setState({ hosts: {}, hostOrder: [] } as never)
    expect(Object.keys(useRelayFreshStore.getState().byHost).sort()).toEqual([A, B])
  })
})
