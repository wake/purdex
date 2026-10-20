// spa/src/hooks/useSessionRoots.test.ts — the thin reader of a session's lineage root (TG-2r). The roots come from the device-local cache (an offline host still answers); "known"/live roots come from the host's last
// quota read; the reading itself is the lifecycle's (lib/task-groups/session-roots-lifecycle.test.ts) — this only tells it what the view wants.
import { describe, it, expect, vi, beforeEach } from 'vitest'
import { act, renderHook } from '@testing-library/react'

const setRootsInterest = vi.fn()
vi.mock('../lib/task-groups/session-roots-lifecycle', () => ({ setRootsInterest: (...a: unknown[]) => setRootsInterest(...a), ROOT_REREAD_DEBOUNCE_MS: 800 }))

import { useSessionRoots } from './useSessionRoots'
import { useRelayQuotaStore } from '../lib/team/relay-quota'
import { useRootCacheStore } from '../lib/task-groups/root-cache'
import type { SessionQuota } from '../lib/team/types'

const q = (session_id: string, root_session_id: string): SessionQuota => ({ session_id, root_session_id, title: '', address: `mlab/${session_id}`, is_lead: false, self_left: 0, member_pool_left: 0, rev: 1 })
const setRead = (hostId: string, rows: SessionQuota[]) => act(() => { useRelayQuotaStore.setState((s) => ({ reads: { ...s.reads, [hostId]: { ok: true, quotas: rows } } })) })
const want = (hostId: string, agentSessionId: string) => ({ hostId, agentSessionId })

beforeEach(() => {
  setRootsInterest.mockReset()
  useRelayQuotaStore.getState().reset()
  useRootCacheStore.setState({ roots: {} })
})

describe('useSessionRoots', () => {
  it('tells the lifecycle what the view wants, and withdraws it when the view goes', () => {
    const wanted = [want('h1', 'S1')]
    const { unmount } = renderHook(() => useSessionRoots(true, wanted))
    expect(setRootsInterest).toHaveBeenLastCalledWith(true, wanted)
    unmount()
    expect(setRootsInterest).toHaveBeenLastCalledWith(false, [])
  })

  it('answers the root of a cached session; anything else is null, never its own id — and an offline host still answers', () => {
    act(() => { useRootCacheStore.getState().ingest('h1', [q('S1', 'ROOT'), q('S2', 'S2')]) })
    const { result } = renderHook(() => useSessionRoots(true, [want('h1', 'S1')]))
    expect(result.current.rootOf('h1', 'S1')).toBe('ROOT')
    expect(result.current.rootOf('h1', 'S2')).toBe('S2')
    expect(result.current.rootOf('h1', 'S9')).toBeNull()
    expect(result.current.rootOf('h2', 'S1')).toBeNull()
    expect(result.current.rootsKnown('h1')).toBe(false) // no read this epoch: the cache answers, but the host is not "read"
  })

  it('lists the roots of every live session of a host (null until the host is read) — what a group may not be cleared of', () => {
    const { result } = renderHook(() => useSessionRoots(true, [want('h1', 'S1')]))
    expect(result.current.liveRoots('h1')).toBeNull()
    setRead('h1', [q('S1', 'ROOT'), q('S2', 'ROOT'), q('S3', 'S3')])
    expect([...(result.current.liveRoots('h1') ?? [])].sort()).toEqual(['ROOT', 'S3'])
    expect(result.current.liveRoots('h2')).toBeNull()
    expect(result.current.rootsKnown('h1')).toBe(true)
  })

  it('a read that failed, or came without quotas, is not "known"', () => {
    act(() => { useRelayQuotaStore.setState({ reads: { h1: { ok: false, code: 'network' } } }) })
    const { result } = renderHook(() => useSessionRoots(true, [want('h1', 'S1')]))
    expect(result.current.rootsKnown('h1')).toBe(false)
    act(() => { useRelayQuotaStore.setState({ reads: { h1: { ok: true } } }) })
    expect(result.current.rootsKnown('h1')).toBe(false)
  })
})
