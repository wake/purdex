// WWD-7b1-1b: the host-events socket routes `jobs.changed` to the jobs driver — only while the host's connection key is the one the socket was made for — and tells the driver when a connection opened.
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
vi.mock('../lib/pool/jobs-driver-live', () => ({ jobsDriver: { jobChanged: vi.fn(async () => {}), hostOpened: vi.fn() } }))

const { useMultiHostEventWs } = await import('./useMultiHostEventWs')
const { jobsDriver } = await import('../lib/pool/jobs-driver-live')

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
  vi.mocked(jobsDriver.jobChanged).mockClear()
  vi.mocked(jobsDriver.hostOpened).mockClear()
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

const value = JSON.stringify({ host: 'mserv', job_id: 'j1', state: 'running' })

describe('useMultiHostEventWs jobs.changed (WWD-7b1-1b)', () => {
  it('a jobs.changed frame makes the driver read that job of that host', async () => {
    const view = await connect()
    act(() => { sockets[0].emit(JSON.stringify({ type: 'jobs.changed', session: '', value })) })
    expect(jobsDriver.jobChanged).toHaveBeenCalledWith('mserv', 'j1')
    view.unmount()
  })

  it('a jobs.changed frame from a connection the host has since left is dropped', async () => {
    const view = await connect()
    // the host is re-pointed at another daemon: this socket's connection key no longer matches
    act(() => { useHostStore.setState({ hosts: { [HOST]: { id: HOST, name: 'Host', ip: '9.9.9.9', port: 7860, order: 0 } } }) })
    act(() => { sockets[0].emit(JSON.stringify({ type: 'jobs.changed', session: '', value })) })
    expect(jobsDriver.jobChanged).not.toHaveBeenCalled()
    view.unmount()
  })

  it('a connection that opened tells the driver which daemon it is', async () => {
    const view = await connect()
    expect(jobsDriver.hostOpened).toHaveBeenCalledWith(HOST)
    view.unmount()
  })
})
