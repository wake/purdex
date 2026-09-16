import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest'
import { act, renderHook } from '@testing-library/react'
import { PEER_STALE_AFTER_MS, usePeerInfo } from './usePeerInfo'
import { useHostStore, type HostRuntime } from '../stores/useHostStore'
import { emptyPeerHostEntry, usePeerStore, type PeerHostEntry, type PeerRow } from '../stores/usePeerStore'
import { useSessionCwdStore } from '../stores/useSessionCwdStore'

const H = 'h1'
const CODE = 'z141yl'
const NOW = 1_700_000_000_000

const peerRefresh = vi.fn(async (_hostId: string) => {})
const cwdRefresh = vi.fn(async (_hostId: string, _code: string) => {})

const ROW: PeerRow = {
  address: 'mini-lab/ai-chat4:ai-chat4-ai-chat-story-3a',
  label: 'ai-chat4',
  labelSource: 'default',
  deliverable: true,
  reason: '',
  agent: { type: 'cc', peerName: 'ai-chat-story-3a', status: 'idle' },
}

function setRuntime(runtime: HostRuntime | undefined) {
  useHostStore.setState({
    hosts: {
      [H]: { id: H, name: 'mlab', ip: '100.64.0.2', port: 7860, order: 0 },
      h2: { id: 'h2', name: 'air', ip: '100.64.0.4', port: 7860, order: 1 },
    },
    hostOrder: [H, 'h2'],
    runtime: runtime ? { [H]: runtime } : {},
  })
}

/** Seed a host as already fetched, so the policy has nothing left to do. */
function seedFetched(entry: Partial<PeerHostEntry> = {}) {
  usePeerStore.setState({
    byHost: { [H]: { ...emptyPeerHostEntry(), fetchedAt: NOW, rows: { [CODE]: ROW }, ...entry } },
  })
}

beforeEach(() => {
  vi.useFakeTimers()
  vi.setSystemTime(NOW)
  peerRefresh.mockClear()
  cwdRefresh.mockClear()
  usePeerStore.setState({ byHost: {}, refresh: peerRefresh })
  useSessionCwdStore.setState({ byHost: {}, refresh: cwdRefresh })
  setRuntime({ status: 'connected' })
})

afterEach(() => {
  vi.useRealTimers()
})

describe('the fetch policy', () => {
  it('fetches once on the first render that needs a connected, unfetched host', () => {
    renderHook(() => usePeerInfo(H, CODE))
    expect(peerRefresh).toHaveBeenCalledTimes(1)
    expect(peerRefresh).toHaveBeenCalledWith(H)
  })

  it('does not fetch peers again when the tab switches between sessions of one host', () => {
    seedFetched()
    const { rerender } = renderHook(({ code }) => usePeerInfo(H, code), { initialProps: { code: CODE } })
    peerRefresh.mockClear()
    for (let i = 0; i < 10; i++) rerender({ code: `code-${i}` })
    expect(peerRefresh).toHaveBeenCalledTimes(0)
  })

  it('does not fetch a host it has already fetched, even after an error', () => {
    seedFetched({ error: 'boom' })
    renderHook(() => usePeerInfo(H, CODE))
    expect(peerRefresh).not.toHaveBeenCalled()
  })

  it.each(['disconnected', 'reconnecting', 'auth-error'] as const)(
    'never fetches a host that is %s',
    (status) => {
      setRuntime({ status })
      renderHook(() => usePeerInfo(H, CODE))
      expect(peerRefresh).not.toHaveBeenCalled()
      expect(cwdRefresh).not.toHaveBeenCalled()
    },
  )

  it('never fetches a host with no runtime entry at all', () => {
    setRuntime(undefined)
    renderHook(() => usePeerInfo(H, CODE))
    expect(peerRefresh).not.toHaveBeenCalled()
  })

  it('fetches a connected host whose tmux is unavailable — "no rows" is a real answer', () => {
    setRuntime({ status: 'connected', tmuxState: 'unavailable' })
    renderHook(() => usePeerInfo(H, CODE))
    expect(peerRefresh).toHaveBeenCalledTimes(1)
  })

  it('fetches a connected host whose tmuxState is still undefined — the normal opening moment', () => {
    // useMultiHostEventWs sets status:'connected' on WS open and only sets
    // tmuxState when a tmux event arrives, so every host passes through this
    // state. Treating undefined as "not ok" would mean the first render after
    // connecting never fetches.
    setRuntime({ status: 'connected', tmuxState: undefined })
    renderHook(() => usePeerInfo(H, CODE))
    expect(peerRefresh).toHaveBeenCalledTimes(1)
  })

  it('fetches once the host becomes connected, not before', () => {
    setRuntime({ status: 'disconnected' })
    renderHook(() => usePeerInfo(H, CODE))
    expect(peerRefresh).not.toHaveBeenCalled()
    act(() => { useHostStore.getState().setRuntime(H, { status: 'connected' }) })
    expect(peerRefresh).toHaveBeenCalledTimes(1)
  })

  it('does nothing at all without a host or a session code', () => {
    renderHook(() => usePeerInfo(null, null))
    renderHook(() => usePeerInfo(null, CODE))
    expect(peerRefresh).not.toHaveBeenCalled()
    expect(cwdRefresh).not.toHaveBeenCalled()
  })

  it('reads the cwd on the first render and again whenever the session changes', () => {
    seedFetched()
    const { rerender } = renderHook(({ code }) => usePeerInfo(H, code), { initialProps: { code: CODE } })
    expect(cwdRefresh).toHaveBeenCalledTimes(1)
    expect(cwdRefresh).toHaveBeenCalledWith(H, CODE)
    rerender({ code: 'other1' })
    expect(cwdRefresh).toHaveBeenCalledTimes(2)
    expect(cwdRefresh).toHaveBeenLastCalledWith(H, 'other1')
  })
})

describe('refresh()', () => {
  it('always issues both refreshes, however fresh the cache is', () => {
    seedFetched()
    const { result } = renderHook(() => usePeerInfo(H, CODE))
    peerRefresh.mockClear()
    cwdRefresh.mockClear()
    act(() => { result.current.refresh() })
    expect(peerRefresh).toHaveBeenCalledTimes(1)
    expect(cwdRefresh).toHaveBeenCalledTimes(1)
  })

  it('does nothing when the host is not connected', () => {
    seedFetched()
    setRuntime({ status: 'disconnected' })
    const { result } = renderHook(() => usePeerInfo(H, CODE))
    act(() => { result.current.refresh() })
    expect(peerRefresh).not.toHaveBeenCalled()
    expect(cwdRefresh).not.toHaveBeenCalled()
  })
})

describe('what it returns', () => {
  it('returns the row for this session, the cwd, and the envelope flags', () => {
    seedFetched({ envelope: { partial: true, labelsUnavailable: false, unknownRegistryFiles: ['/x.json'] } })
    useSessionCwdStore.setState({
      byHost: { [H]: { [CODE]: { cwd: '/somewhere/else', fetchedAt: NOW, loading: false, error: null } } },
    })
    const { result } = renderHook(() => usePeerInfo(H, CODE))
    expect(result.current.row).toEqual(ROW)
    expect(result.current.cwd).toBe('/somewhere/else')
    expect(result.current.envelope).toEqual({ partial: true, labelsUnavailable: false, unknownRegistryFiles: ['/x.json'] })
    expect(result.current.connected).toBe(true)
    expect(result.current.fetched).toBe(true)
  })

  it('a host with an answer but no row for this session returns null, not undefined', () => {
    seedFetched({ rows: {} })
    const { result } = renderHook(() => usePeerInfo(H, CODE))
    expect(result.current.row).toBeNull()
    expect(result.current.fetched).toBe(true)
  })

  it('surfaces the two errors separately', () => {
    seedFetched({ error: 'peers boom' })
    useSessionCwdStore.setState({
      byHost: { [H]: { [CODE]: { cwd: '', fetchedAt: 0, loading: false, error: 'cwd boom' } } },
    })
    const { result } = renderHook(() => usePeerInfo(H, CODE))
    expect(result.current.error).toBe('peers boom')
    expect(result.current.cwdError).toBe('cwd boom')
  })

  it('is loading while either store is', () => {
    seedFetched({ loading: true })
    const { result } = renderHook(() => usePeerInfo(H, CODE))
    expect(result.current.loading).toBe(true)
    expect(result.current.row).toEqual(ROW)  // the previous value stays visible
  })
})

describe('staleness', () => {
  it('is fresh 30 s after the rows arrived', () => {
    seedFetched({ fetchedAt: NOW - 30_000 })
    const { result } = renderHook(() => usePeerInfo(H, CODE))
    expect(result.current.stale).toBe(false)
    expect(result.current.fetchedAt).toBe(NOW - 30_000)
  })

  it('is stale 90 s after the rows arrived', () => {
    seedFetched({ fetchedAt: NOW - 90_000 })
    const { result } = renderHook(() => usePeerInfo(H, CODE))
    expect(result.current.stale).toBe(true)
  })

  it('is never stale before the first answer', () => {
    usePeerStore.setState({ byHost: {} })
    const { result } = renderHook(() => usePeerInfo(H, CODE))
    expect(result.current.stale).toBe(false)
    expect(result.current.fetched).toBe(false)
  })

  it('goes stale on its own once the boundary passes, without another render', () => {
    seedFetched({ fetchedAt: NOW })
    const { result } = renderHook(() => usePeerInfo(H, CODE))
    expect(result.current.stale).toBe(false)
    act(() => { vi.advanceTimersByTime(PEER_STALE_AFTER_MS + 1) })
    expect(result.current.stale).toBe(true)
  })
})
