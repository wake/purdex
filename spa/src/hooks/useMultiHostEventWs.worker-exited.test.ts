// A `nex-worker-exited` host event (the daemon exited a worker because its
// conversation was resumed in a terminal) toasts and refetches the host list.
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { renderHook, act, waitFor } from '@testing-library/react'
import { useHostStore } from '../stores/useHostStore'
import { useSessionStore } from '../stores/useSessionStore'
import { useExecutionListStore } from '../stores/useExecutionListStore'
import { useUndoToast } from '../stores/useUndoToast'

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
const refetch = vi.fn()
const originalRefetch = useExecutionListStore.getState().refetch

beforeEach(() => {
  sockets = []
  refetch.mockClear()
  vi.stubGlobal('WebSocket', FakeSocket)
  useHostStore.setState({
    hosts: { [HOST]: { id: HOST, name: 'Host', ip: '1.2.3.4', port: 7860, order: 0 } },
    hostOrder: [HOST],
    runtime: {},
    activeHostId: HOST,
  })
  useSessionStore.setState({ fetchHost: vi.fn(async () => {}), replaceHost: vi.fn() } as never)
  useExecutionListStore.setState({ byHost: {}, refetch })
  useUndoToast.setState({ toast: null, notice: null })
})

afterEach(() => {
  vi.unstubAllGlobals()
  useHostStore.getState().reset()
  useExecutionListStore.setState({ refetch: originalRefetch })
})

describe('useMultiHostEventWs nex-worker-exited', () => {
  it('toasts and refetches, even when the event carries no session', async () => {
    const view = renderHook(() => useMultiHostEventWs())
    await waitFor(() => expect(sockets).toHaveLength(1))
    const value = JSON.stringify({ execution_id: 'E9', session_id: 'S', reason: 'manual_resume', tmux_session: 'proj-2' })
    act(() => { sockets[0].emit(JSON.stringify({ type: 'nex-worker-exited', session: '', value })) })
    expect(refetch).toHaveBeenCalledWith(HOST)
    expect(useUndoToast.getState().toast?.message).toContain('proj-2')
    view.unmount()
  })
})
