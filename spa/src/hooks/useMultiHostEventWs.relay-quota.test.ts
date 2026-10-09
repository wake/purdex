// spa/src/hooks/useMultiHostEventWs.relay-quota.test.ts — the `team.relay_quota` host event reaches the quota store through
// the real hook (plan RQ-A Task 5). Harness as useMultiHostEventWs.unattended.test.ts.
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { renderHook, act, waitFor } from '@testing-library/react'
import { useHostStore } from '../stores/useHostStore'
import { useSessionStore } from '../stores/useSessionStore'
import { useRelayQuotaStore, quotaKey } from '../lib/team/relay-quota'

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

const ev = (over: Record<string, unknown> = {}) => ({ op: 'changed', root_session_id: 'r1', self_left: 3, member_pool_left: 2, rev: 4, ...over })
const frame = (value: unknown) => JSON.stringify({ type: 'team.relay_quota', session: '', value: JSON.stringify(value) })
const confirmed = (host = HOST, root = 'r1') => useRelayQuotaStore.getState().confirmed[quotaKey(host, root)]

beforeEach(() => {
  sockets = []
  vi.stubGlobal('WebSocket', FakeSocket)
  useHostStore.setState({
    hosts: { [HOST]: { id: HOST, name: 'mlab', ip: '1.2.3.4', port: 7860, order: 0 } },
    hostOrder: [HOST],
    runtime: {},
    activeHostId: HOST,
  })
  useSessionStore.setState({ fetchHost: vi.fn(async () => {}), replaceHost: vi.fn() } as never)
  useRelayQuotaStore.getState().reset()
})

afterEach(() => {
  vi.unstubAllGlobals()
  useHostStore.getState().reset()
})

describe('useMultiHostEventWs team.relay_quota', () => {
  it('a frame is applied to this host\'s root, by the rev rule', async () => {
    const view = renderHook(() => useMultiHostEventWs())
    await waitFor(() => expect(sockets).toHaveLength(1))
    const ws = sockets[0]
    act(() => { ws.emit(frame(ev())) })
    expect(confirmed()).toEqual({ self_left: 3, member_pool_left: 2, rev: 4 })
    act(() => { ws.emit(frame(ev({ rev: 3, self_left: 9 }))) }) // older: ignored
    expect(confirmed()?.self_left).toBe(3)
    act(() => { ws.emit(frame(ev({ rev: 5, self_left: 1 }))) })
    expect(confirmed()).toEqual({ self_left: 1, member_pool_left: 2, rev: 5 })
    view.unmount()
  })

  it('a malformed frame is dropped whole', async () => {
    const warn = vi.spyOn(console, 'warn').mockImplementation(() => {})
    const view = renderHook(() => useMultiHostEventWs())
    await waitFor(() => expect(sockets).toHaveLength(1))
    act(() => { sockets[0].emit(frame(ev({ self_left: 100 }))) })
    act(() => { sockets[0].emit(frame(ev({ rev: undefined }))) })
    expect(useRelayQuotaStore.getState().confirmed).toEqual({})
    expect(warn).toHaveBeenCalled()
    warn.mockRestore()
    view.unmount()
  })

  it('after a removal, a frame queued on the old socket does not write', async () => {
    const view = renderHook(() => useMultiHostEventWs())
    await waitFor(() => expect(sockets).toHaveLength(1))
    const old = sockets[0]
    act(() => { old.emit(frame(ev())) })
    expect(confirmed()).toBeDefined()
    act(() => {
      useHostStore.setState({ hosts: {}, hostOrder: [] })
      useRelayQuotaStore.getState().forgetHost(HOST) // what unattended-support.ts does on the same store change
      old.emit(frame(ev({ rev: 9 }))) // before the effect closes the old socket
    })
    expect(confirmed()).toBeUndefined()
    act(() => { old.emit(frame(ev({ rev: 10 }))) }) // and after it closed
    expect(confirmed()).toBeUndefined()
    view.unmount()
  })

  it('after a re-point, a frame queued on the old socket does not write; the new connection\'s do', async () => {
    const view = renderHook(() => useMultiHostEventWs())
    await waitFor(() => expect(sockets).toHaveLength(1))
    const old = sockets[0]
    act(() => {
      useHostStore.setState((s) => ({ hosts: { ...s.hosts, [HOST]: { ...s.hosts[HOST], ip: '5.6.7.8' } } }))
      useRelayQuotaStore.getState().forgetHost(HOST)
      old.emit(frame(ev({ rev: 9 })))
    })
    expect(confirmed()).toBeUndefined()
    await waitFor(() => expect(sockets).toHaveLength(2))
    act(() => { sockets[1].emit(frame(ev({ rev: 1, self_left: 6 }))) })
    expect(confirmed()?.self_left).toBe(6)
    view.unmount()
  })
})
