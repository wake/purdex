// MON-10a: the host-events socket routes `alerts.changed {host}` to the alerts store — only while the host's connection key is the one the socket was made for — and tells it when a connection opened.
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
vi.mock('../lib/pool/jobs-driver-live', () => ({ jobsDriver: { jobChanged: vi.fn(async () => {}), hostOpened: vi.fn() }, startJobsCatchUp: vi.fn(() => () => {}) }))
vi.mock('../lib/pool/alerts-store', () => ({ alertsEvent: vi.fn(), alertsConnectionOpened: vi.fn(), retainPoolAlerts: vi.fn(() => () => {}) }))

const { useMultiHostEventWs } = await import('./useMultiHostEventWs')
const { alertsEvent, alertsConnectionOpened } = await import('../lib/pool/alerts-store')

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

beforeEach(() => {
  sockets = []
  vi.mocked(alertsEvent).mockClear()
  vi.mocked(alertsConnectionOpened).mockClear()
  vi.spyOn(console, 'warn').mockImplementation(() => {})
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

const frame = (value: unknown) => JSON.stringify({ type: 'alerts.changed', session: '', value: typeof value === 'string' ? value : JSON.stringify(value) })

describe('useMultiHostEventWs alerts.changed (MON-10a)', () => {
  it('an alerts.changed frame tells the alerts store which daemon and which host', async () => {
    const view = await connect()
    act(() => { sockets[0].emit(frame({ host: 'mserv' })) })
    expect(alertsEvent).toHaveBeenCalledTimes(1)
    expect(alertsEvent).toHaveBeenCalledWith(HOST, 'mserv')
    view.unmount()
  })

  it('a frame from a connection the host has since left is dropped', async () => {
    const view = await connect()
    act(() => { useHostStore.setState({ hosts: { [HOST]: { id: HOST, name: 'Host', ip: '9.9.9.9', port: 7860, order: 0 } } }) })
    act(() => { sockets[0].emit(frame({ host: 'mserv' })) })
    expect(alertsEvent).not.toHaveBeenCalled()
    view.unmount()
  })

  it.each([['no host', {}], ['an empty host', { host: '' }], ['a number', { host: 3 }], ['not json', 'nope'], ['an array', []]])('a frame with %s is dropped', async (_n, value) => {
    const view = await connect()
    act(() => { sockets[0].emit(frame(value as never)) })
    expect(alertsEvent).not.toHaveBeenCalled()
    view.unmount()
  })

  it('a connection that opened tells the alerts store, since events missed while it was down are not sent again', async () => {
    const view = await connect()
    expect(alertsConnectionOpened).toHaveBeenCalledWith(HOST)
    view.unmount()
  })
})
