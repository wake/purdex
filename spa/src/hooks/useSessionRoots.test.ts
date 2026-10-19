// spa/src/hooks/useSessionRoots.test.ts — a session's lineage root, for the sidebar's task-group view (name pool v2 spec §7, L10, NA-6b). The one source is the quota rows of `GET /api/team/unattended` (the daemon lists
// every live session with its chain root), read through the shared reader and only while the view is open: once when it opens, again after a reconnect, and once more (debounced) when a live session shows up
// that the last read did not list. A host whose daemon lacks `task_groups.v1` is not asked and reports no roots. The hook only reads; nothing is guessed.
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { act, renderHook } from '@testing-library/react'

const readFirstPage = vi.fn()
vi.mock('../lib/team/unattended-reader', () => ({ readFirstPage: (...a: unknown[]) => readFirstPage(...a), refreshHost: (...a: unknown[]) => readFirstPage(...a) }))
const supported = new Set<string>()
vi.mock('../lib/task-groups/loader', () => ({ taskGroupsSupported: (h: string) => supported.has(h) }))

import { useSessionRoots, ROOT_REREAD_DEBOUNCE_MS } from './useSessionRoots'
import { useRelayQuotaStore } from '../lib/team/relay-quota'
import type { SessionQuota } from '../lib/team/types'

const q = (session_id: string, root_session_id: string): SessionQuota => ({ session_id, root_session_id, title: '', address: `mlab/${session_id}`, is_lead: false, self_left: 0, member_pool_left: 0, rev: 1 })
const setRead = (hostId: string, rows: SessionQuota[]) => act(() => { useRelayQuotaStore.setState((s) => ({ reads: { ...s.reads, [hostId]: { ok: true, quotas: rows } } })) })
const want = (hostId: string, agentSessionId: string) => ({ hostId, agentSessionId })

beforeEach(() => {
  vi.useFakeTimers()
  readFirstPage.mockReset()
  readFirstPage.mockResolvedValue({})
  supported.clear()
  supported.add('h1')
  useRelayQuotaStore.getState().reset()
})
afterEach(() => { vi.useRealTimers() })

describe('useSessionRoots', () => {
  it('reads nothing while the view is closed', () => {
    renderHook(() => useSessionRoots(false, [want('h1', 'S1')]))
    vi.advanceTimersByTime(60_000)
    expect(readFirstPage).not.toHaveBeenCalled()
  })

  it('reads a supporting host once when the view opens, and never a host without task_groups.v1', () => {
    renderHook(() => useSessionRoots(true, [want('h1', 'S1'), want('h2', 'S2')]))
    expect(readFirstPage.mock.calls.map((c) => c[0])).toEqual(['h1'])
  })

  it('answers the root of a session the last read listed; anything else is null, never its own id', () => {
    setRead('h1', [q('S1', 'ROOT'), q('S2', 'S2')])
    const { result } = renderHook(() => useSessionRoots(true, [want('h1', 'S1')]))
    expect(result.current.rootOf('h1', 'S1')).toBe('ROOT')
    expect(result.current.rootOf('h1', 'S2')).toBe('S2')
    expect(result.current.rootOf('h1', 'S9')).toBeNull()
    expect(result.current.rootOf('h2', 'S1')).toBeNull()
  })

  it('knows when a host has been read ("reading" until then)', () => {
    const { result } = renderHook(() => useSessionRoots(true, [want('h1', 'S1')]))
    expect(result.current.rootsKnown('h1')).toBe(false)
    setRead('h1', [q('S1', 'ROOT')])
    expect(result.current.rootsKnown('h1')).toBe(true)
  })

  it('a read that failed, or came without quotas, is not "known"', () => {
    act(() => { useRelayQuotaStore.setState({ reads: { h1: { ok: false, code: 'network' } } }) })
    const { result } = renderHook(() => useSessionRoots(true, [want('h1', 'S1')]))
    expect(result.current.rootsKnown('h1')).toBe(false)
    act(() => { useRelayQuotaStore.setState({ reads: { h1: { ok: true } } }) })
    expect(result.current.rootsKnown('h1')).toBe(false)
  })

  it('a live session the last read did not list triggers one more read after the debounce — once per session', () => {
    setRead('h1', [q('S1', 'ROOT')])
    const { rerender } = renderHook(({ wants }) => useSessionRoots(true, wants), { initialProps: { wants: [want('h1', 'S1')] } })
    expect(readFirstPage).not.toHaveBeenCalled() // everything wanted is listed
    rerender({ wants: [want('h1', 'S1'), want('h1', 'NEW')] })
    expect(readFirstPage).not.toHaveBeenCalled()
    act(() => { vi.advanceTimersByTime(ROOT_REREAD_DEBOUNCE_MS - 1) })
    expect(readFirstPage).not.toHaveBeenCalled()
    act(() => { vi.advanceTimersByTime(1) })
    expect(readFirstPage).toHaveBeenCalledTimes(1)
    rerender({ wants: [want('h1', 'S1'), want('h1', 'NEW')] })
    act(() => { vi.advanceTimersByTime(ROOT_REREAD_DEBOUNCE_MS * 3) })
    expect(readFirstPage).toHaveBeenCalledTimes(1) // asked about NEW already: no loop when the daemon does not list it
  })

  it('several new sessions inside the debounce are one read', () => {
    setRead('h1', [q('S1', 'ROOT')])
    const { rerender } = renderHook(({ wants }) => useSessionRoots(true, wants), { initialProps: { wants: [want('h1', 'S1')] } })
    rerender({ wants: [want('h1', 'S1'), want('h1', 'A')] })
    act(() => { vi.advanceTimersByTime(ROOT_REREAD_DEBOUNCE_MS / 2) })
    rerender({ wants: [want('h1', 'S1'), want('h1', 'A'), want('h1', 'B')] })
    act(() => { vi.advanceTimersByTime(ROOT_REREAD_DEBOUNCE_MS * 2) })
    expect(readFirstPage).toHaveBeenCalledTimes(1)
  })

  it('a reconnect (the quota store fenced the host: its read is gone) reads again', () => {
    setRead('h1', [q('S1', 'ROOT')])
    renderHook(() => useSessionRoots(true, [want('h1', 'S1')]))
    expect(readFirstPage).not.toHaveBeenCalled()
    act(() => { useRelayQuotaStore.getState().forgetHost('h1') }) // what a disconnect/reconnect does: read gone, epoch moved
    expect(readFirstPage).toHaveBeenCalledTimes(1)
  })

  it('closing the view cancels a pending debounced read', () => {
    setRead('h1', [q('S1', 'ROOT')])
    const { rerender } = renderHook(({ on, wants }) => useSessionRoots(on, wants), { initialProps: { on: true, wants: [want('h1', 'S1')] } })
    rerender({ on: true, wants: [want('h1', 'S1'), want('h1', 'NEW')] })
    rerender({ on: false, wants: [want('h1', 'S1'), want('h1', 'NEW')] })
    act(() => { vi.advanceTimersByTime(ROOT_REREAD_DEBOUNCE_MS * 3) })
    expect(readFirstPage).not.toHaveBeenCalled()
  })
})
