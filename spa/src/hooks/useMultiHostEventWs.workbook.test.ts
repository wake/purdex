// spa/src/hooks/useMultiHostEventWs.workbook.test.ts — the workbook host events reach the store through the real hook,
// bound to their connection, and never cause a request (plan WA-1.3 / WA-1.4). Harness as the roster test.
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { renderHook, act, waitFor } from '@testing-library/react'
import { useHostStore } from '../stores/useHostStore'
import { useSessionStore } from '../stores/useSessionStore'
import { useWorkbookStore } from '../stores/useWorkbookStore'
import { wireEntry, wireTodo } from '../lib/workbook/fixtures'

vi.mock('../lib/host-connection', () => ({
  checkHealth: vi.fn(async () => ({ daemon: 'connected', latency: 3, ticket: 'tk' })),
}))
const fetchConversation = vi.fn()
const fetchTodos = vi.fn()
const postRefresh = vi.fn()
vi.mock('../lib/workbook/api', () => ({
  fetchConversation: (...a: unknown[]) => fetchConversation(...a),
  fetchTodos: (...a: unknown[]) => fetchTodos(...a),
  postRefresh: (...a: unknown[]) => postRefresh(...a),
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

const frame = (type: string, value: unknown) => JSON.stringify({ type, session: '', value: JSON.stringify(value) })
const entryFrame = (id: number) => frame('workbook.entry', { conv_key: 'c1', session_id: 's1', entry: wireEntry({ id }) })
const statusFrame = frame('workbook.status', { conv_key: 'c1', session_id: 's1', status: 'hello', updated_at: 5 })

beforeEach(() => {
  sockets = []
  fetchConversation.mockReset()
  vi.stubGlobal('WebSocket', FakeSocket)
  useHostStore.setState({
    hosts: { [HOST]: { id: HOST, name: 'mlab', ip: '1.2.3.4', port: 7860, order: 0 } },
    hostOrder: [HOST], runtime: {}, activeHostId: HOST,
  })
  useSessionStore.setState({ fetchHost: vi.fn(async () => {}), replaceHost: vi.fn() } as never)
  useWorkbookStore.getState().reset()
})
afterEach(() => {
  vi.unstubAllGlobals()
  useHostStore.getState().reset()
})

describe('useMultiHostEventWs workbook events', () => {
  it('an entry and a status land in the store, on a v1 host, with no request', async () => {
    useWorkbookStore.getState().setSupport(HOST, { v1: true, v2: false })
    const view = renderHook(() => useMultiHostEventWs())
    await waitFor(() => expect(sockets).toHaveLength(1))
    act(() => { for (let i = 1; i <= 50; i++) sockets[0].emit(entryFrame(i)); sockets[0].emit(statusFrame) })
    const c = useWorkbookStore.getState().byHost[HOST].byConv.c1
    expect(c.entries).toHaveLength(50)
    expect(c.status).toBe('hello')
    expect(useWorkbookStore.getState().convOfSession[HOST].s1).toBe('c1')
    expect(fetchConversation).not.toHaveBeenCalled()
    view.unmount()
  })

  it('v2 todos and refresh_available frames land in the store, 50 of each, with no request', async () => {
    useWorkbookStore.getState().setSupport(HOST, { v1: true, v2: true })
    const view = renderHook(() => useMultiHostEventWs())
    await waitFor(() => expect(sockets).toHaveLength(1))
    act(() => {
      for (let i = 1; i <= 50; i++) {
        sockets[0].emit(frame('workbook.todos', { conv_key: 'c1', session_id: 's1', todos: [wireTodo({ id: i, state: i % 2 ? 'open' : 'done' })] }))
        sockets[0].emit(frame('workbook.refresh_available', { conv_key: 'c1', available: i % 2 === 0 }))
      }
    })
    const c = useWorkbookStore.getState().byHost[HOST].byConv.c1
    expect(c.todos.open).toHaveLength(25)
    expect(c.todos.done).toHaveLength(25)
    expect(c.refreshAvailable).toBe(true)
    expect(fetchConversation).not.toHaveBeenCalled()
    expect(fetchTodos).not.toHaveBeenCalled()
    expect(postRefresh).not.toHaveBeenCalled()
    view.unmount()
  })

  it('a malformed todos frame is dropped and does not throw', async () => {
    const warn = vi.spyOn(console, 'warn').mockImplementation(() => {})
    const view = renderHook(() => useMultiHostEventWs())
    await waitFor(() => expect(sockets).toHaveLength(1))
    act(() => {
      sockets[0].emit(frame('workbook.todos', { conv_key: 'c1', session_id: 's1', todos: 'nope' }))
      sockets[0].emit(frame('workbook.refresh_available', { conv_key: 'c1', available: 'yes' }))
    })
    expect(useWorkbookStore.getState().byHost[HOST]).toBeUndefined()
    warn.mockRestore()
    view.unmount()
  })

  it('a malformed entry frame is dropped and does not throw', async () => {
    const warn = vi.spyOn(console, 'warn').mockImplementation(() => {})
    const view = renderHook(() => useMultiHostEventWs())
    await waitFor(() => expect(sockets).toHaveLength(1))
    act(() => { sockets[0].emit(frame('workbook.entry', { conv_key: 'c1', session_id: 's1', entry: wireEntry({ state: 'bogus' }) })) })
    expect(useWorkbookStore.getState().byHost[HOST]).toBeUndefined()
    warn.mockRestore()
    view.unmount()
  })

  it('a frame queued on the socket of a re-pointed host is dropped', async () => {
    const view = renderHook(() => useMultiHostEventWs())
    await waitFor(() => expect(sockets).toHaveLength(1))
    const old = sockets[0]
    act(() => {
      useHostStore.setState((s) => ({ hosts: { ...s.hosts, [HOST]: { ...s.hosts[HOST], ip: '5.6.7.8' } } }))
      useWorkbookStore.getState().forgetHost(HOST) // what roster-forget.ts does on the same store change
      old.emit(entryFrame(1))
    })
    expect(useWorkbookStore.getState().byHost[HOST]).toBeUndefined()
    await waitFor(() => expect(sockets).toHaveLength(2))
    act(() => { sockets[1].emit(entryFrame(1)) })
    expect(useWorkbookStore.getState().byHost[HOST].byConv.c1.entries).toHaveLength(1)
    view.unmount()
  })
})
