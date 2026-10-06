import { describe, it, expect, vi, beforeEach } from 'vitest'
import { renderHook, waitFor } from '@testing-library/react'

vi.mock('../lib/nex/nex-api', () => ({ listExecutions: vi.fn(), fetchExecutionPrelude: vi.fn() }))
import * as api from '../lib/nex/nex-api'
import { useEntityStints } from './useEntityStints'
import { useNexHostStore } from '../stores/useNexHostStore'
import { LIST_MAX_PAGES } from '../lib/nex/list-all-executions'

const S = '0a1b2c3d-1111-4222-8333-444455556666'
const row = (id: string, created_at: number, resume = S) => ({
  id, state: 'idle', provider: 'claude', principal_id: 'p', cwd: '/w', mount_kind: 'dir', brief: '', labels: {},
  created_at, updated_at: created_at, duration_ms: null, event_count: 0, observers: 0, archived: false,
  ...(resume ? { resume_session_id: resume } : {}),
}) as never
const cur = (extra = {}) => ({ ...(row('cur', 100) as object), ...extra }) as never
const prelude = (totalBytes: number | null, state = 'ok') => ({ state, items: [], prevCursor: null, totalBytes }) as never

function caps(sessionFilter: boolean) {
  useNexHostStore.setState({ byHost: { h: { phase: 'ready', capabilities: sessionFilter ? { list: { session_filter: true } } : {} } } } as never)
}

describe('useEntityStints', () => {
  beforeEach(() => {
    vi.mocked(api.listExecutions).mockReset()
    vi.mocked(api.fetchExecutionPrelude).mockReset()
    caps(true)
  })

  it('walks every page (include_archived on each) and fetches the newest stint on the last page', async () => {
    vi.mocked(api.listExecutions)
      .mockResolvedValueOnce({ items: [row('a', 1)], next_cursor: 'a' } as never)
      .mockResolvedValueOnce({ items: [row('b', 2)], next_cursor: 'b' } as never)
      .mockResolvedValueOnce({ items: [row('c', 3), cur()], next_cursor: '' } as never)
    vi.mocked(api.fetchExecutionPrelude).mockImplementation(async (_h, id) => prelude({ a: 10, b: 20, c: 30 }[id as string]!))
    const { result } = renderHook(() => useEntityStints('h', cur(), true))
    await waitFor(() => expect(result.current.status).toBe('ok'))
    expect(api.listExecutions).toHaveBeenCalledTimes(3)
    for (const c of vi.mocked(api.listExecutions).mock.calls) expect(c[1]).toMatchObject({ includeArchived: true })
    expect(vi.mocked(api.fetchExecutionPrelude).mock.calls.map((c) => c[1]).sort()).toEqual(['a', 'b', 'c'])
    expect(vi.mocked(api.fetchExecutionPrelude).mock.calls[0][2]).toEqual({ limit: 1 })
    expect(result.current.stints.map((s) => s.id)).toEqual(['a', 'b', 'c'])
  })

  it('lists by session_id when the capability is present', async () => {
    vi.mocked(api.listExecutions).mockResolvedValue({ items: [], next_cursor: '' } as never)
    const { result } = renderHook(() => useEntityStints('h', cur(), true))
    await waitFor(() => expect(result.current.status).toBe('ok'))
    const o = vi.mocked(api.listExecutions).mock.calls[0][1]!
    expect(o.sessionId).toBe(S)
    expect(o.labels).toBeUndefined()
  })

  it('lists by the purdex.session_id label otherwise, S falling back to session_id', async () => {
    caps(false)
    vi.mocked(api.listExecutions).mockResolvedValue({ items: [], next_cursor: '' } as never)
    const { result } = renderHook(() => useEntityStints('h', cur({ resume_session_id: undefined, session_id: S }), true))
    await waitFor(() => expect(result.current.status).toBe('ok'))
    const o = vi.mocked(api.listExecutions).mock.calls[0][1]!
    expect(o.labels).toEqual({ 'purdex.session_id': S })
    expect(o.sessionId).toBeUndefined()
  })

  it('a list failure is unavailable', async () => {
    vi.mocked(api.listExecutions).mockRejectedValue(new Error('boom'))
    const { result } = renderHook(() => useEntityStints('h', cur(), true))
    await waitFor(() => expect(result.current.status).toBe('unavailable'))
    expect(result.current.stints).toEqual([])
  })

  it('a truncated walk is unavailable', async () => {
    let n = 0
    vi.mocked(api.listExecutions).mockImplementation(async () => ({ items: [row(`r${n}`, n)], next_cursor: `c${n++}` }) as never)
    const { result } = renderHook(() => useEntityStints('h', cur(), true))
    await waitFor(() => expect(result.current.status).toBe('unavailable'))
    expect(api.listExecutions).toHaveBeenCalledTimes(LIST_MAX_PAGES)
    expect(api.fetchExecutionPrelude).not.toHaveBeenCalled()
  })

  it('a stuck walk is unavailable', async () => {
    vi.mocked(api.listExecutions).mockResolvedValue({ items: [row('a', 1)], next_cursor: 'same' } as never)
    const { result } = renderHook(() => useEntityStints('h', cur(), true))
    await waitFor(() => expect(result.current.status).toBe('unavailable'))
  })

  it('a page with a dropped (malformed) row is unavailable and fetches no boundary', async () => {
    vi.mocked(api.listExecutions).mockResolvedValue({ items: [row('a', 1), { state: 'idle' }], next_cursor: '' } as never)
    const { result } = renderHook(() => useEntityStints('h', cur(), true))
    await waitFor(() => expect(result.current.status).toBe('unavailable'))
    expect(result.current.stints).toEqual([])
    expect(api.fetchExecutionPrelude).not.toHaveBeenCalled()
  })

  it('a fresh-start row has boundary 0 with no prelude fetch; a failed boundary fetch drops only that stint', async () => {
    vi.mocked(api.listExecutions).mockResolvedValue({ items: [row('fresh', 1, ''), row('bad', 2), row('none', 3), row('ok', 4)], next_cursor: '' } as never)
    vi.mocked(api.fetchExecutionPrelude).mockImplementation(async (_h, id) => {
      if (id === 'bad') throw new Error('x')
      if (id === 'none') return prelude(null, 'none')
      return prelude(50)
    })
    const { result } = renderHook(() => useEntityStints('h', cur(), true))
    await waitFor(() => expect(result.current.status).toBe('ok'))
    expect(vi.mocked(api.fetchExecutionPrelude).mock.calls.map((c) => c[1]).sort()).toEqual(['bad', 'none', 'ok'])
    expect(result.current.stints).toEqual([{ id: 'fresh', boundary: 0, createdAt: 1 }, { id: 'ok', boundary: 50, createdAt: 4 }])
  })

  it('lists and fetches once across re-renders, even with a new summary object; stints stay referentially stable', async () => {
    vi.mocked(api.listExecutions).mockResolvedValue({ items: [row('a', 1)], next_cursor: '' } as never)
    vi.mocked(api.fetchExecutionPrelude).mockResolvedValue(prelude(7))
    const { result, rerender } = renderHook(({ s }) => useEntityStints('h', s, true), { initialProps: { s: cur() } })
    await waitFor(() => expect(result.current.status).toBe('ok'))
    const first = result.current.stints
    rerender({ s: cur({ updated_at: 999 }) })
    rerender({ s: cur({ updated_at: 1000 }) })
    expect(result.current.stints).toBe(first)
    expect(api.listExecutions).toHaveBeenCalledTimes(1)
    expect(api.fetchExecutionPrelude).toHaveBeenCalledTimes(1)
  })

  it('a stale host change drops the late answer', async () => {
    let release!: (v: unknown) => void
    const withCap = { phase: 'ready', capabilities: { list: { session_filter: true } } }
    useNexHostStore.setState({ byHost: { h: withCap, h2: withCap } } as never)
    vi.mocked(api.listExecutions).mockImplementationOnce(() => new Promise((r) => { release = r }) as never)
    vi.mocked(api.listExecutions).mockResolvedValueOnce({ items: [row('new', 1)], next_cursor: '' } as never)
    vi.mocked(api.fetchExecutionPrelude).mockResolvedValue(prelude(5))
    const { result, rerender } = renderHook(({ h }) => useEntityStints(h, cur(), true), { initialProps: { h: 'h' } })
    rerender({ h: 'h2' })
    await waitFor(() => expect(result.current.status).toBe('ok'))
    release({ items: [row('old', 1)], next_cursor: '' })
    await new Promise((r) => setTimeout(r, 0))
    expect(result.current.stints.map((s) => s.id)).toEqual(['new'])
  })

  it('disabled, or without a session id, makes no request and stays idle', async () => {
    const a = renderHook(() => useEntityStints('h', cur(), false))
    const b = renderHook(() => useEntityStints('h', cur({ resume_session_id: undefined }), true))
    const c = renderHook(() => useEntityStints('h', null, true))
    await new Promise((r) => setTimeout(r, 0))
    for (const r of [a, b, c]) expect(r.result.current.status).toBe('idle')
    expect(api.listExecutions).not.toHaveBeenCalled()
  })
})
