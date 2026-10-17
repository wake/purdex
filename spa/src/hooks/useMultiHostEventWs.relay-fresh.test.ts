// spa/src/hooks/useMultiHostEventWs.relay-fresh.test.ts — the `relay.fresh` host event reaches the per-host list store through the
// real hook, bound to the connection it arrived on (relay v2 contract 5): a frame from the socket of a re-pointed (endpoint or token)
// or removed host is dropped, a frame on the new connection applies. Harness as the roster test; the fresh sync driver is NOT
// started, so nothing but the hook's own connection-key check stands between the old socket and the store.
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { renderHook, act, waitFor } from '@testing-library/react'
import { useHostStore } from '../stores/useHostStore'
import { useSessionStore } from '../stores/useSessionStore'
import { useNexHostStore } from '../stores/useNexHostStore'
import { useRelayFreshStore } from '../stores/useRelayFreshStore'
import type { RelayFresh } from '../lib/relay/fresh'

vi.mock('../lib/host-connection', () => ({
  checkHealth: vi.fn(async () => ({ daemon: 'connected', latency: 3, ticket: 'tk' })),
}))

const { useMultiHostEventWs } = await import('./useMultiHostEventWs')

const HOST = 'h1'

class FakeSocket {
  static OPEN = 1
  readyState = 0
  url: string
  onopen: (() => void) | null = null
  onclose: (() => void) | null = null
  onmessage: ((e: { data: unknown }) => void) | null = null
  onerror: (() => void) | null = null
  send = vi.fn()
  close = vi.fn(() => { this.readyState = 3 })
  constructor(url: string) { this.url = url; sockets.push(this) }
  emit(data: string) { this.onmessage?.({ data }) }
}

let sockets: FakeSocket[] = []

const fresh = (id: string): RelayFresh => ({ session_id: id, ref: '_aaaaaa', op_id: `op-${id}`, path: `/p/${id}.md`, written_at: 1000, code: `c-${id}` })
const frame = (op: string, sessions: RelayFresh[]) => JSON.stringify({ type: 'relay.fresh', session: '', value: JSON.stringify({ op, sessions }) })
const ids = () => (useRelayFreshStore.getState().byHost[HOST] ?? []).map((s) => s.session_id)

beforeEach(() => {
  sockets = []
  vi.stubGlobal('WebSocket', FakeSocket)
  useHostStore.setState({
    hosts: { [HOST]: { id: HOST, name: 'mlab', ip: '1.2.3.4', port: 7860, token: 't', order: 0 } },
    hostOrder: [HOST],
    runtime: {},
    activeHostId: HOST,
  })
  useSessionStore.setState({ fetchHost: vi.fn(async () => {}), replaceHost: vi.fn() } as never)
  useNexHostStore.setState({
    byHost: { [HOST]: { info: null, capabilities: null, phase: 'unavailable', error: null, fetchedAt: 1, generation: 1, fingerprint: 'x', daemonCapabilities: ['relay.half.v1'] } },
  } as never)
  useRelayFreshStore.getState().reset()
})

afterEach(() => {
  vi.unstubAllGlobals()
  useHostStore.getState().reset()
  useNexHostStore.setState({ byHost: {} })
  useRelayFreshStore.getState().reset()
})

describe('useMultiHostEventWs relay.fresh', () => {
  it('the snapshot and a changed each replace this host\'s list in the store', async () => {
    const view = renderHook(() => useMultiHostEventWs())
    await waitFor(() => expect(sockets).toHaveLength(1))
    act(() => { sockets[0].emit(frame('snapshot', [fresh('a'), fresh('b')])) })
    expect(ids()).toEqual(['a', 'b'])
    act(() => { sockets[0].emit(frame('changed', [fresh('c')])) })
    expect(ids()).toEqual(['c'])
    view.unmount()
  })

  it.each([
    { name: 'endpoint', over: { ip: '5.6.7.8' } },
    { name: 'token', over: { token: 'other' } },
  ])('a frame from the socket of a host re-pointed ($name) is dropped; the new connection\'s frames apply', async ({ over }) => {
    const view = renderHook(() => useMultiHostEventWs())
    await waitFor(() => expect(sockets).toHaveLength(1))
    const old = sockets[0]
    act(() => { old.emit(frame('snapshot', [fresh('a')])) })
    expect(ids()).toEqual(['a'])
    act(() => {
      useHostStore.setState((s) => ({ hosts: { ...s.hosts, [HOST]: { ...s.hosts[HOST], ...over } } }))
      useRelayFreshStore.getState().forgetHost(HOST) // what fresh-sync.ts does on the same store change
      old.emit(frame('changed', [fresh('old-daemon')])) // still queued on the old socket, before the effect closes it
    })
    expect(useRelayFreshStore.getState().byHost[HOST]).toBeUndefined()
    await waitFor(() => expect(sockets).toHaveLength(2))
    act(() => { old.emit(frame('changed', [fresh('old-daemon')])) })
    expect(useRelayFreshStore.getState().byHost[HOST]).toBeUndefined()
    act(() => { sockets[1].emit(frame('snapshot', [fresh('new-daemon')])) })
    expect(ids()).toEqual(['new-daemon'])
    view.unmount()
  })

  it('a frame from the socket of a removed host is dropped', async () => {
    const view = renderHook(() => useMultiHostEventWs())
    await waitFor(() => expect(sockets).toHaveLength(1))
    const old = sockets[0]
    act(() => { old.emit(frame('snapshot', [fresh('a')])) })
    act(() => {
      useHostStore.setState({ hosts: {}, hostOrder: [] })
      useRelayFreshStore.getState().forgetHost(HOST)
      old.emit(frame('changed', [fresh('late')]))
    })
    expect(useRelayFreshStore.getState().byHost[HOST]).toBeUndefined()
    view.unmount()
  })
})
