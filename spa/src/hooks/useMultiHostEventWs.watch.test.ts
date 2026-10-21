// W5e: the host-events socket routes `watch.changed` and `watch.events` to the watch handler — only while the host's connection key
// is the one the socket was made for (a re-pointed host's old frames are rejected).
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
vi.mock('../lib/watch/watch-ws', async (orig) => ({ ...(await orig<typeof import('../lib/watch/watch-ws')>()), handleWatchEvent: vi.fn() }))

const { useMultiHostEventWs } = await import('./useMultiHostEventWs')
const { handleWatchEvent } = await import('../lib/watch/watch-ws')

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
  vi.mocked(handleWatchEvent).mockReset()
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

const frame = (type: string) => JSON.stringify({ type, session: '', value: JSON.stringify({ host_id: 'd1', unhandled: 2, cause: 'recorded' }) })

describe('useMultiHostEventWs watch pushes (W5e)', () => {
  it('a watch.changed frame goes to the watch handler with the host id and the type', async () => {
    const view = await connect()
    act(() => { sockets[0].emit(frame('watch.changed')) })
    expect(handleWatchEvent).toHaveBeenCalledWith(HOST, 'watch.changed', expect.any(String))
    view.unmount()
  })

  it('a watch.events frame (the retired push) is not handed to the watch handler', async () => {
    const view = await connect()
    act(() => { sockets[0].emit(frame('watch.events')) })
    expect(handleWatchEvent).not.toHaveBeenCalled()
    view.unmount()
  })

  it('a frame from a connection the host has since left is dropped, for both types', async () => {
    const view = await connect()
    // The host is re-pointed at another daemon. The old socket is still open in the moment before the hook has torn it down
    // (the store is written outside act, so React has not committed yet); a frame that arrives then is the guard's case.
    useHostStore.setState({ hosts: { [HOST]: { id: HOST, name: 'Host', ip: '9.9.9.9', port: 7860, order: 0 } } })
    sockets[0].emit(frame('watch.changed'))
    sockets[0].emit(frame('watch.events'))
    expect(handleWatchEvent).not.toHaveBeenCalled()
    await act(async () => {})
    view.unmount()
  })

  it('a frame of a host that was removed is dropped', async () => {
    const view = await connect()
    useHostStore.setState({ hosts: {}, hostOrder: [] })
    sockets[0].emit(frame('watch.events'))
    expect(handleWatchEvent).not.toHaveBeenCalled()
    await act(async () => {})
    view.unmount()
  })
})
