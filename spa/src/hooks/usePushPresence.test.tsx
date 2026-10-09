import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { renderHook } from '@testing-library/react'
import type { HostInfo } from '../stores/useHostStore'

// The real wiring: the real stores, the real activity tracker, the real client and window ids; only the network is faked.
const fetchHostInfo = vi.fn<(hostId: string) => Promise<HostInfo>>()
const pinnedHostFetch = vi.fn<(hostId: string, path: string, init?: RequestInit) => Promise<Response>>()
vi.mock('../lib/host-api', () => ({
  fetchHostInfo: (id: string) => fetchHostInfo(id),
  pinnedHostFetch: (id: string, path: string, init?: RequestInit) => pinnedHostFetch(id, path, init),
}))

import { usePushPresence } from './usePushPresence'
import { useHostStore } from '../stores/useHostStore'
import { useTabStore } from '../stores/useTabStore'
import { useSessionStore } from '../stores/useSessionStore'

const info = (capabilities: string[]): HostInfo =>
  ({ host_id: 'mini-lab:abc', tmux_instance: '', purdex_version: '', tmux_version: '', os: '', arch: '', capabilities })
const host = (id: string) => ({ id, name: id, ip: '100.64.0.2', port: 7860, token: 't', order: 0 })

beforeEach(() => {
  vi.useFakeTimers()
  vi.spyOn(document, 'hasFocus').mockReturnValue(true)
  fetchHostInfo.mockReset()
  pinnedHostFetch.mockReset()
  pinnedHostFetch.mockResolvedValue(new Response(null, { status: 204 }))
  useHostStore.getState().reset()
  useHostStore.setState({ hosts: { h1: host('h1'), h2: host('h2') }, hostOrder: ['h1', 'h2'], runtime: { h1: { status: 'connected' }, h2: { status: 'connected' } } })
  useSessionStore.setState({ sessions: { h1: [{ code: 'c1', name: 'dev' } as never] } })
  useTabStore.setState({
    tabs: { t1: { id: 't1', pinned: false, locked: false, createdAt: 0, layout: { type: 'leaf', pane: { id: 'p1', content: { kind: 'tmux-session', hostId: 'h1', sessionCode: 'c1', mode: 'terminal', cachedName: '', tmuxInstance: '' } } } } },
    activeTabId: 't1', tabOrder: ['t1'],
  })
})
afterEach(() => { vi.useRealTimers(); vi.restoreAllMocks() })

describe('usePushPresence', () => {
  it('reports the shown session to the push-capable host with this device and window id, and nothing to the other', async () => {
    fetchHostInfo.mockImplementation(async (id) => info(id === 'h1' ? ['push.v1'] : ['conversations.scope.v1']))
    const { unmount } = renderHook(() => usePushPresence())
    window.dispatchEvent(new KeyboardEvent('keydown', { key: 'a' })) // the user is here
    await vi.advanceTimersByTimeAsync(400)
    expect(pinnedHostFetch).toHaveBeenCalledTimes(1)
    const [hostId, path, init] = pinnedHostFetch.mock.calls[0]
    expect(hostId).toBe('h1')
    expect(path).toBe('/api/push/presence')
    expect(init?.method).toBe('PUT')
    const body = JSON.parse(String(init?.body))
    expect(body.client_id).toMatch(/^c_[0-9a-f]{12}:[0-9a-f]{32}$/)
    expect(body.client_id.length).toBeLessThanOrEqual(64)
    expect(body).toMatchObject({ active: true, ttl_ms: 45000, sessions: [{ code: 'c1', name: 'dev' }] })
    unmount()
  })

  it('stops reporting when unmounted', async () => {
    fetchHostInfo.mockResolvedValue(info(['push.v1']))
    const { unmount } = renderHook(() => usePushPresence())
    window.dispatchEvent(new KeyboardEvent('keydown', { key: 'a' }))
    await vi.advanceTimersByTimeAsync(400)
    unmount()
    pinnedHostFetch.mockClear()
    await vi.advanceTimersByTimeAsync(60_000)
    expect(pinnedHostFetch).not.toHaveBeenCalled()
  })

  it('a daemon that answers the PUT with an error is not retried in a loop', async () => {
    fetchHostInfo.mockResolvedValue(info(['push.v1']))
    pinnedHostFetch.mockResolvedValue(new Response('no', { status: 500 }))
    const { unmount } = renderHook(() => usePushPresence())
    window.dispatchEvent(new KeyboardEvent('keydown', { key: 'a' }))
    await vi.advanceTimersByTimeAsync(400)
    const first = pinnedHostFetch.mock.calls.length
    await vi.advanceTimersByTimeAsync(5_000)
    expect(pinnedHostFetch.mock.calls.length).toBe(first) // the next try is the 20 s tick, not a spin
    unmount()
  })
})
