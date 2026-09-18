// spa/src/hooks/useMultiHostEventWs.legacy-events.test.ts — the daemon no
// longer emits `handoff` / `relay` events (P-D.2 tore down Stream mode), and
// the SPA no longer has a store for them (P-D.3). A daemon that still sends
// one — an older build on a peer host — must be ignored: no throw, no session
// refetch, and the attach gate left exactly where it was.
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { renderHook, act, waitFor } from '@testing-library/react'
import { useHostStore } from '../stores/useHostStore'
import { useSessionStore } from '../stores/useSessionStore'

vi.mock('../lib/host-connection', () => ({
  checkHealth: vi.fn(async () => ({ daemon: 'connected', latency: 3, ticket: 'tk' })),
}))

const { useMultiHostEventWs } = await import('./useMultiHostEventWs')

const HOST = 'h1'

class FakeSocket {
  static OPEN = 1
  readyState = 0
  binaryType = ''
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
let fetchHost: ReturnType<typeof vi.fn>

beforeEach(() => {
  sockets = []
  fetchHost = vi.fn(async () => {})
  vi.stubGlobal('WebSocket', FakeSocket)
  useHostStore.setState({
    hosts: { [HOST]: { id: HOST, name: 'Host', ip: '1.2.3.4', port: 7860, order: 0 } },
    hostOrder: [HOST],
    runtime: {},
    activeHostId: HOST,
  })
  useSessionStore.setState({
    fetchHost,
    replaceHost: vi.fn(),
  } as never)
})

afterEach(() => {
  vi.unstubAllGlobals()
  useHostStore.getState().reset()
})

describe('useMultiHostEventWs legacy stream events', () => {
  it.each([
    ['handoff connected', { type: 'handoff', session: 'abc123', value: 'connected' }],
    ['handoff failed', { type: 'handoff', session: 'abc123', value: 'failed: boom' }],
    ['handoff progress', { type: 'handoff', session: 'abc123', value: 'starting' }],
    ['relay connected', { type: 'relay', session: 'abc123', value: 'connected' }],
    ['relay disconnected', { type: 'relay', session: 'abc123', value: 'disconnected' }],
  ])('ignores a %s event: no throw, no session refetch, gate untouched', async (_label, event) => {
    const view = renderHook(() => useMultiHostEventWs())
    await waitFor(() => expect(sockets).toHaveLength(1))
    act(() => { sockets[0].emit(JSON.stringify({ type: 'sessions', session: '', value: '[]' })) })
    expect(useHostStore.getState().runtime[HOST]?.attachReady).toBe(true)
    fetchHost.mockClear()

    expect(() => {
      act(() => { sockets[0].emit(JSON.stringify(event)) })
    }).not.toThrow()

    expect(fetchHost).not.toHaveBeenCalled()
    expect(useHostStore.getState().runtime[HOST]?.attachReady).toBe(true)

    view.unmount()
  })
})
