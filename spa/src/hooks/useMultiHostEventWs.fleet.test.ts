// MON-12b-1: the fleet is retired. The host-events socket no longer opts in to `fleet.changed` (no `fleet=v1`), and a `fleet.changed`
// frame from a daemon that still sends one is not a frame this hook knows: it does nothing and throws nothing.
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { renderHook, waitFor, act } from '@testing-library/react'
import { useHostStore } from '../stores/useHostStore'
import { useSessionStore } from '../stores/useSessionStore'
import { useExecutionListStore } from '../stores/useExecutionListStore'

vi.mock('../lib/host-connection', () => ({
  checkHealth: vi.fn(async () => ({ daemon: 'connected', latency: 3, ticket: 'tk' })),
}))
vi.mock('../lib/host-api', async (orig) => ({
  ...(await orig<typeof import('../lib/host-api')>()),
  fetchWsTicket: vi.fn(async () => 'tk2'),
  pinnedHostFetch: vi.fn(),
}))
vi.mock('../lib/nex/nex-api', () => ({ listExecutions: vi.fn() }))
vi.mock('../lib/nex/nex-sse', () => ({ openNexSse: vi.fn(() => ({ close: vi.fn() })) }))
vi.mock('../lib/team/approval-ws', () => ({ handleApprovalEvent: vi.fn() }))

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
let warn: ReturnType<typeof vi.spyOn>

beforeEach(() => {
  sockets = []
  warn = vi.spyOn(console, 'warn').mockImplementation(() => {})
  vi.stubGlobal('WebSocket', FakeSocket)
  useHostStore.setState({
    hosts: { [HOST]: { id: HOST, name: 'Host', ip: '1.2.3.4', port: 7860, order: 0 } },
    hostOrder: [HOST], runtime: {}, activeHostId: HOST,
  })
  useSessionStore.setState({ fetchHost: vi.fn(async () => {}), replaceHost: vi.fn() } as never)
  useExecutionListStore.setState({ onHello: vi.fn(), applyDelta: vi.fn() })
})
afterEach(() => {
  vi.unstubAllGlobals()
  vi.restoreAllMocks()
  useHostStore.getState().reset()
})

const connect = async () => {
  const view = renderHook(() => useMultiHostEventWs())
  await waitFor(() => expect(sockets).toHaveLength(1))
  act(() => { sockets[0].onopen?.() })
  return view
}

describe('useMultiHostEventWs without the fleet (MON-12b-1)', () => {
  it('the URL carries no fleet opt-in; the other opt-ins stay', async () => {
    const view = await connect()
    const u = new URL(sockets[0].url)
    expect(u.searchParams.getAll('fleet')).toEqual([])
    expect(u.searchParams.getAll('agent')).toEqual(['v2'])
    expect(u.searchParams.getAll('reads')).toEqual(['v1'])
    expect(u.searchParams.get('ticket')).toBe('tk')
    view.unmount()
  })

  it('a fleet.changed frame does nothing and throws nothing', async () => {
    const view = await connect()
    const value = JSON.stringify({ term: 2, primary: 'air26', hosts: [] })
    warn.mockClear()
    expect(() => act(() => { sockets[0].emit(JSON.stringify({ type: 'fleet.changed', session: '', value })) })).not.toThrow()
    expect(warn).not.toHaveBeenCalled()
    view.unmount()
  })
})
