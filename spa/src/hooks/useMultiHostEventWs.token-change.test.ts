// spa/src/hooks/useMultiHostEventWs.token-change.test.ts — #1360: a host whose
// token changes in-app (Add Host's same-endpoint branch, the Overview token
// field) must renegotiate with the NEW token at once, the way an ip/port change
// does — not sit in the auth-error the tokenless attempt ended in until reload.
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { renderHook, act, waitFor } from '@testing-library/react'
import { useHostStore } from '../stores/useHostStore'
import { useSessionStore } from '../stores/useSessionStore'
import { __resetForTests } from '../lib/rebuild/session-version'

// The real checkHealth's auth contract, minus the network: no token → auth-error
// (the daemon is not in pairing mode); a token the daemon knows → a ticket.
const { checkHealth } = vi.hoisted(() => {
  const VALID = new Set(['good', 'good-2'])
  const checkHealth = vi.fn(async (_base: string, getToken?: () => string | undefined) => {
    const token = getToken?.()
    if (!token || !VALID.has(token)) {
      return { daemon: 'auth-error', tmux: 'unavailable', latency: 1, mode: 'normal' }
    }
    return { daemon: 'connected', tmux: 'unavailable', latency: 1, mode: 'normal', ticket: `tk-${token}` }
  })
  return { checkHealth }
})
vi.mock('../lib/host-connection', () => ({ checkHealth }))

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
}

let sockets: FakeSocket[] = []

const status = () => useHostStore.getState().runtime[HOST]?.status
const ticketOf = (s: FakeSocket) => new URL(s.url).searchParams.get('ticket')
const liveSockets = () => sockets.filter((s) => s.readyState !== 3)
/** The token each health check was run with, in order. */
const tokensChecked = () => checkHealth.mock.calls.map(([, getToken]) => getToken?.())

function openLatest() {
  const s = sockets[sockets.length - 1]
  act(() => { s.readyState = 1; s.onopen?.() })
}

beforeEach(() => {
  __resetForTests()
  sockets = []
  checkHealth.mockClear()
  vi.stubGlobal('WebSocket', FakeSocket)
  useHostStore.setState({
    hosts: { [HOST]: { id: HOST, name: 'mlab', ip: '100.64.0.2', port: 7860, order: 0 } },
    hostOrder: [HOST],
    runtime: {},
    activeHostId: HOST,
  })
  useSessionStore.setState({ fetchHost: vi.fn(async () => {}), replaceHost: vi.fn() } as never)
})

afterEach(() => {
  vi.unstubAllGlobals()
  useHostStore.getState().reset()
})

describe('useMultiHostEventWs — token change (#1360)', () => {
  it('a tokenless host that is given a token renegotiates with it and connects, without a remount', async () => {
    const view = renderHook(() => useMultiHostEventWs())
    await waitFor(() => expect(status()).toBe('auth-error'))
    expect(sockets).toHaveLength(0) // lazy connection: never dialed without a ticket

    act(() => { useHostStore.getState().updateHost(HOST, { token: 'good' }) })

    await waitFor(() => expect(sockets).toHaveLength(1))
    expect(ticketOf(sockets[0])).toBe('tk-good')
    openLatest()
    expect(status()).toBe('connected')
    view.unmount()
  })

  it('a token changed while connected reconnects with the new token and retires the old socket', async () => {
    useHostStore.getState().updateHost(HOST, { token: 'good' })
    const view = renderHook(() => useMultiHostEventWs())
    await waitFor(() => expect(sockets).toHaveLength(1))
    openLatest()
    expect(status()).toBe('connected')

    act(() => { useHostStore.getState().updateHost(HOST, { token: 'good-2' }) })

    await waitFor(() => expect(sockets).toHaveLength(2))
    expect(ticketOf(sockets[1])).toBe('tk-good-2')
    expect(sockets[0].close).toHaveBeenCalled()
    expect(liveSockets()).toEqual([sockets[1]])
    expect(tokensChecked().at(-1)).toBe('good-2')
    view.unmount()
  })

  it('a token cleared while connected drops the socket and settles on auth-error', async () => {
    useHostStore.getState().updateHost(HOST, { token: 'good' })
    const view = renderHook(() => useMultiHostEventWs())
    await waitFor(() => expect(sockets).toHaveLength(1))
    openLatest()

    act(() => { useHostStore.getState().updateHost(HOST, { token: undefined }) })

    await waitFor(() => expect(status()).toBe('auth-error'))
    expect(sockets[0].close).toHaveBeenCalled()
    expect(liveSockets()).toEqual([])
    expect(sockets).toHaveLength(1) // nothing dialed without a token
    view.unmount()
  })

  it('tokens flipped back and forth quickly leave exactly one live connection, on the last token', async () => {
    const view = renderHook(() => useMultiHostEventWs())
    await waitFor(() => expect(status()).toBe('auth-error'))

    act(() => {
      const { updateHost } = useHostStore.getState()
      updateHost(HOST, { token: 'good' })
      updateHost(HOST, { token: 'good-2' })
      updateHost(HOST, { token: 'good' })
    })
    act(() => { useHostStore.getState().updateHost(HOST, { token: 'good-2' }) })
    act(() => { useHostStore.getState().updateHost(HOST, { token: 'good' }) })

    await waitFor(() => expect(liveSockets()).toHaveLength(1))
    // Let any straggling negotiation from a superseded attempt settle.
    await act(async () => { await new Promise((r) => setTimeout(r, 20)) })
    expect(liveSockets()).toHaveLength(1)
    expect(ticketOf(liveSockets()[0])).toBe('tk-good')
    openLatest()
    expect(status()).toBe('connected')
    view.unmount()
  })

  it('an unrelated host-store change (name, color, runtime flags) does not recreate the connection', async () => {
    useHostStore.getState().updateHost(HOST, { token: 'good' })
    const view = renderHook(() => useMultiHostEventWs())
    await waitFor(() => expect(sockets).toHaveLength(1))
    openLatest()
    const checks = checkHealth.mock.calls.length

    act(() => {
      const s = useHostStore.getState()
      s.updateHost(HOST, { name: 'renamed' })
      s.setHostColor(HOST, '#ff0000')
      s.setRuntime(HOST, { daemonIdMismatch: { stored: 'a', observed: 'b', endpoint: '100.64.0.2:7860' } })
    })
    await act(async () => { await new Promise((r) => setTimeout(r, 20)) })

    expect(sockets).toHaveLength(1)
    expect(sockets[0].close).not.toHaveBeenCalled()
    expect(checkHealth.mock.calls.length).toBe(checks)
    expect(status()).toBe('connected')
    view.unmount()
  })
})
