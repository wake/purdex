// spa/src/hooks/useMultiHostEventWs.gate.test.ts — the gate's open/close wiring
// (spec §4.6). The gate closes whenever a host-events connection is (re)started
// or drops, and reopens only when that connection's own `sessions` payload has
// been reconciled.
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { renderHook, act, waitFor } from '@testing-library/react'
import { useHostStore } from '../stores/useHostStore'
import { useSessionStore } from '../stores/useSessionStore'
import { __resetForTests, currentConn, heldVersion } from '../lib/rebuild/session-version'

vi.mock('../lib/host-connection', () => ({
  checkHealth: vi.fn(async () => ({ daemon: 'connected', latency: 3, ticket: 'tk' })),
}))

const { cancelSessionRefresh } = vi.hoisted(() => ({ cancelSessionRefresh: vi.fn() }))
vi.mock('../lib/rebuild/refresh-after-switch', async (importOriginal) => ({
  ...(await importOriginal<typeof import('../lib/rebuild/refresh-after-switch')>()),
  cancelSessionRefresh,
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

const attachReady = () => useHostStore.getState().runtime[HOST]?.attachReady

beforeEach(() => {
  __resetForTests()
  sockets = []
  vi.stubGlobal('WebSocket', FakeSocket)
  useHostStore.setState({
    hosts: { [HOST]: { id: HOST, name: 'Host', ip: '1.2.3.4', port: 7860, order: 0 } },
    hostOrder: [HOST],
    runtime: {},
    activeHostId: HOST,
  })
  useSessionStore.setState({
    fetchHost: vi.fn(async () => {}),
    replaceHost: vi.fn(),
  } as never)
})

afterEach(() => {
  vi.unstubAllGlobals()
  useHostStore.getState().reset()
})

describe('useMultiHostEventWs attach gate', () => {
  it('keeps the gate closed until this connection delivers a sessions payload', async () => {
    const view = renderHook(() => useMultiHostEventWs())

    expect(attachReady()).toBe(false)
    await waitFor(() => expect(sockets).toHaveLength(1))
    expect(attachReady()).toBe(false)

    act(() => { sockets[0].onopen?.() })
    expect(attachReady()).toBe(false) // an open socket is not yet a fresh list

    act(() => { sockets[0].emit(JSON.stringify({ type: 'sessions', session: '', value: '[]' })) })
    expect(attachReady()).toBe(true)

    view.unmount()
  })

  it('closes the gate again as soon as the socket drops', async () => {
    const view = renderHook(() => useMultiHostEventWs())
    await waitFor(() => expect(sockets).toHaveLength(1))
    act(() => { sockets[0].emit(JSON.stringify({ type: 'sessions', session: '', value: '[]' })) })
    expect(attachReady()).toBe(true)

    act(() => { sockets[0].onclose?.() })
    expect(attachReady()).toBe(false)

    view.unmount()
  })

  // #1255 SPA spec §3.3 (codex #5): a post-switch refresh reads the gate and
  // captures `conn` in one step, so by the time anything can see the gate
  // closed, `conn` must already name a different connection.
  it('onClose moves conn BEFORE the gate closes', async () => {
    const view = renderHook(() => useMultiHostEventWs())
    await waitFor(() => expect(sockets).toHaveLength(1))
    act(() => { sockets[0].onopen?.() })
    act(() => { sockets[0].emit(JSON.stringify({ type: 'sessions', session: '', value: '[]' })) })
    const before = currentConn(HOST)
    let connWhenGateClosed: number | undefined
    const unsub = useHostStore.subscribe((s, prev) => {
      if (prev.runtime[HOST]?.attachReady === true && s.runtime[HOST]?.attachReady === false) {
        connWhenGateClosed = currentConn(HOST)
      }
    })

    act(() => { sockets[0].onclose?.() })
    unsub()
    expect(connWhenGateClosed).toBeDefined()
    expect(connWhenGateClosed).not.toBe(before)
    expect(attachReady()).toBe(false)

    view.unmount()
  })

  it('onOpen moves conn', async () => {
    const view = renderHook(() => useMultiHostEventWs())
    await waitFor(() => expect(sockets).toHaveLength(1))
    const before = currentConn(HOST)
    act(() => { sockets[0].onopen?.() })
    expect(currentConn(HOST)).not.toBe(before)
    view.unmount()
  })

  it('a stale versioned frame reaching the hook is not reconciled', async () => {
    const view = renderHook(() => useMultiHostEventWs())
    await waitFor(() => expect(sockets).toHaveLength(1))
    const epoch = '9f3c1a0b7d2e4c61'
    act(() => { sockets[0].emit(JSON.stringify({ type: 'sessions', session: '', value: '[]', epoch, seq: 7 })) })
    expect(heldVersion(HOST)).toEqual({ epoch, seq: 7 })
    const replaceHost = useSessionStore.getState().replaceHost as ReturnType<typeof vi.fn>
    replaceHost.mockClear()

    act(() => { sockets[0].emit(JSON.stringify({ type: 'sessions', session: '', value: '[]', epoch, seq: 6 })) })
    expect(replaceHost).not.toHaveBeenCalled()
    view.unmount()
  })

  it('tearing an entry down (host removed, unmount) forgets its held version and moves conn', async () => {
    const view = renderHook(() => useMultiHostEventWs())
    await waitFor(() => expect(sockets).toHaveLength(1))
    act(() => { sockets[0].emit(JSON.stringify({ type: 'sessions', session: '', value: '[]', epoch: '9f3c1a0b7d2e4c61', seq: 7 })) })
    const before = currentConn(HOST)

    act(() => { useHostStore.setState({ hostOrder: [] }) })
    expect(heldVersion(HOST)).toBeNull()
    expect(currentConn(HOST)).not.toBe(before)

    const afterRemove = currentConn(HOST)
    view.unmount()
    expect(currentConn(HOST)).toBe(afterRemove) // entry already gone: nothing left to tear down
  })

  it('tearing an entry down cancels its pending session refresh (removed, endpoint changed, unmount)', async () => {
    cancelSessionRefresh.mockClear()
    const view = renderHook(() => useMultiHostEventWs())
    await waitFor(() => expect(sockets).toHaveLength(1))

    act(() => {
      const h = useHostStore.getState().hosts[HOST]
      useHostStore.setState({ hosts: { [HOST]: { ...h, port: 7861 } } })
    })
    expect(cancelSessionRefresh).toHaveBeenCalledTimes(1)
    await waitFor(() => expect(sockets).toHaveLength(2))

    act(() => { useHostStore.setState({ hostOrder: [] }) })
    expect(cancelSessionRefresh).toHaveBeenCalledTimes(2)

    act(() => { useHostStore.setState({ hostOrder: [HOST] }) })
    await waitFor(() => expect(sockets).toHaveLength(3))
    view.unmount()
    expect(cancelSessionRefresh).toHaveBeenCalledTimes(3)
    expect(cancelSessionRefresh).toHaveBeenCalledWith(HOST)
  })

  it('unmount tears down every live entry', async () => {
    const view = renderHook(() => useMultiHostEventWs())
    await waitFor(() => expect(sockets).toHaveLength(1))
    act(() => { sockets[0].emit(JSON.stringify({ type: 'sessions', session: '', value: '[]', epoch: '9f3c1a0b7d2e4c61', seq: 7 })) })
    const before = currentConn(HOST)
    view.unmount()
    expect(heldVersion(HOST)).toBeNull()
    expect(currentConn(HOST)).not.toBe(before)
  })

  it('leaves the gate closed when the payload cannot be parsed', async () => {
    const view = renderHook(() => useMultiHostEventWs())
    await waitFor(() => expect(sockets).toHaveLength(1))

    act(() => { sockets[0].emit(JSON.stringify({ type: 'sessions', session: '', value: 'not-json' })) })
    expect(attachReady()).toBe(false)

    view.unmount()
  })
})
