// spa/src/lib/relay/fresh-sync.repoint.test.ts — a connected host re-pointed (endpoint or token) with the REAL Nexen cache: its own
// `ensure` and its own host-store watcher (`startNexHostInvalidation`), started in main.tsx's order (relay fresh first) and in the
// reverse. Only the two network calls are faked: the new daemon's `/api/info` (what flag it lists) and `GET /api/relay/fresh`.
import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest'
import { startRelayFresh } from './fresh-sync'
import type { RelayFresh } from './fresh'
import { useRelayFreshStore } from '../../stores/useRelayFreshStore'
import { useHostStore } from '../../stores/useHostStore'
import { startNexHostInvalidation, useNexHostStore } from '../../stores/useNexHostStore'

vi.mock('./fresh', async (orig) => ({ ...(await orig<typeof import('./fresh')>()), fetchRelayFresh: vi.fn() }))
vi.mock('../host-api', async (orig) => ({ ...(await orig<typeof import('../host-api')>()), fetchInfo: vi.fn() }))
import { fetchRelayFresh } from './fresh'
import { fetchInfo } from '../host-api'

const A = 'h-a'
const cfg = (over: Record<string, unknown> = {}) => ({ id: A, name: A, ip: '1.1.1.1', port: 7860, token: 't', order: 0, ...over })
const fresh = (id: string): RelayFresh => ({ session_id: id, ref: '_aaaaaa', op_id: `op-${id}`, path: `/p/${id}.md`, written_at: 1000, code: `c-${id}` })
const ids = () => (useRelayFreshStore.getState().byHost[A] ?? []).map((s) => s.session_id)
const setStatus = (status: 'connected' | 'disconnected') => useHostStore.setState((s) => ({ runtime: { ...s.runtime, [A]: { status } } }) as never)
const repoint = (over: Record<string, unknown>) => useHostStore.setState((s) => ({ hosts: { ...s.hosts, [A]: { ...s.hosts[A], ...over } } }) as never)
const settle = async () => { for (let i = 0; i < 6; i++) await new Promise((r) => setTimeout(r, 0)) }
function deferred<T>() {
  let resolve!: (v: T) => void
  const promise = new Promise<T>((a) => { resolve = a })
  return { promise, resolve }
}

/** Which daemon a host config names right now, and what it says. */
const daemonKey = () => { const h = useHostStore.getState().hosts[A]; return `${h.ip}|${h.token}` }
const OLD = '1.1.1.1|t'
const NEW_ENDPOINT = '9.9.9.9|t'
const NEW_TOKEN = '1.1.1.1|other'
let caps: Record<string, string[]>
let infoCalls: string[]

const REPOINTS = [
  { name: 'endpoint', over: { ip: '9.9.9.9' }, key: NEW_ENDPOINT },
  { name: 'token', over: { token: 'other' }, key: NEW_TOKEN },
]
const ORDERS = [
  { name: 'relay fresh first (main.tsx)', nexFirst: false },
  { name: 'Nexen cache first', nexFirst: true },
]

let stops: Array<() => void> = []
function boot(nexFirst: boolean) {
  if (nexFirst) stops = [startNexHostInvalidation(), startRelayFresh()]
  else stops = [startRelayFresh(), startNexHostInvalidation()]
}

beforeEach(() => {
  caps = { [OLD]: ['relay.half.v1'], [NEW_ENDPOINT]: ['relay.half.v1'], [NEW_TOKEN]: ['relay.half.v1'] }
  infoCalls = []
  vi.mocked(fetchInfo).mockReset().mockImplementation(async () => {
    const key = daemonKey()
    infoCalls.push(key)
    return { ok: true, json: async () => ({ capabilities: caps[key] }) } as Response
  })
  vi.mocked(fetchRelayFresh).mockReset().mockImplementation(async () => [fresh(daemonKey() === OLD ? 'old' : 'new')])
  useRelayFreshStore.getState().reset()
  useNexHostStore.setState({ byHost: {} })
  useHostStore.setState({ hosts: { [A]: cfg() }, hostOrder: [A], activeHostId: A, runtime: { [A]: { status: 'disconnected' } } } as never)
})
afterEach(() => {
  stops.forEach((s) => s())
  stops = []
  useNexHostStore.setState({ byHost: {} })
})

describe.each(ORDERS)('a connected host re-pointed, $name', ({ nexFirst }) => {
  describe.each(REPOINTS)('$name', ({ over, key }) => {
    async function connectedWithOldList() {
      boot(nexFirst)
      setStatus('connected')
      await settle()
      expect(ids()).toEqual(['old'])
      infoCalls = []
      vi.mocked(fetchRelayFresh).mockClear()
    }

    it('the new daemon\'s /api/info is asked, and when it lists the flag the list is read from it again', async () => {
      await connectedWithOldList()
      repoint(over)
      await settle()
      expect(infoCalls).toContain(key)
      expect(vi.mocked(fetchRelayFresh)).toHaveBeenCalledTimes(1)
      expect(ids()).toEqual(['new'])
    })

    it('a new daemon without the flag is not read, and the list is empty', async () => {
      await connectedWithOldList()
      caps[key] = ['fleet.v1']
      repoint(over)
      await settle()
      expect(infoCalls).toContain(key)
      expect(vi.mocked(fetchRelayFresh)).not.toHaveBeenCalled()
      expect(useRelayFreshStore.getState().byHost).toEqual({})
    })

    it('the old daemon\'s late answer never reaches the store, before or after the new daemon\'s', async () => {
      boot(nexFirst)
      const oldAnswer = deferred<RelayFresh[]>()
      vi.mocked(fetchRelayFresh).mockImplementationOnce(() => oldAnswer.promise)
      setStatus('connected')
      await settle()
      expect(vi.mocked(fetchRelayFresh)).toHaveBeenCalledTimes(1) // the old daemon's GET is out
      repoint(over)
      await settle()
      expect(ids()).toEqual(['new'])
      oldAnswer.resolve([fresh('old')])
      await settle()
      expect(ids()).toEqual(['new'])
    })
  })

  it('a host the cache never asked about (nothing for it to clear) is asked all the same', async () => {
    boot(nexFirst)
    expect(useNexHostStore.getState().byHost).toEqual({})
    useHostStore.setState((s) => ({ hosts: { ...s.hosts, [A]: cfg({ ip: '9.9.9.9' }) }, runtime: { [A]: { status: 'connected' } } }) as never)
    await settle()
    expect([...new Set(infoCalls)]).toEqual([NEW_ENDPOINT]) // only ever the new daemon (the cache's own connect refresh may add more)
    expect(ids()).toEqual(['new'])
  })
})
