// spa/src/hooks/useExecutionPrelude.test.ts
import { describe, it, expect, vi, beforeEach } from 'vitest'
import { renderHook, act, waitFor } from '@testing-library/react'
import { useExecutionStore, executionKey } from '../stores/useExecutionStore'
import { useNexHostStore } from '../stores/useNexHostStore'
import type { PreludePage } from '../lib/nex/prelude-wire'
import { NexApiError } from '../lib/nex/types'

const fetchExecutionPrelude = vi.fn<(h: string, e: string, o?: { before?: string; limit?: number }) => Promise<PreludePage>>()
vi.mock('../lib/nex/nex-api', () => ({ fetchExecutionPrelude: (...a: Parameters<typeof fetchExecutionPrelude>) => fetchExecutionPrelude(...a) }))
import { useExecutionPrelude, PRELUDE_PAGE_LIMIT } from './useExecutionPrelude'

const CAP = { route: { method: 'GET', path: '/x' }, page_max_items: 500, page_max_bytes: 1, max_block_bytes: 1 }
const ok = (pos: string, prevCursor: string | null): PreludePage => ({
  state: 'ok', prevCursor, totalBytes: null,
  items: [{ pos, at: 1, kind: 'prelude.segment', entrypoint: 'cli' }],
})

/** A ready host with (or without) the capability, and an execution whose history is loaded. */
function seed({ cap = true, resume = 'sid' }: { cap?: boolean; resume?: string | null } = {}) {
  useNexHostStore.setState({ byHost: { h: { phase: 'ready', capabilities: { transcript_prelude: cap ? CAP : undefined } } } } as never)
  useExecutionStore.setState({ executions: {} })
  const s = useExecutionStore.getState()
  s.setSummary('h', 'e', { id: 'e', state: 'idle', resume_session_id: resume ?? undefined } as never)
  s.setHistoryLoaded('h', 'e', true)
}
const prelude = () => useExecutionStore.getState().executions[executionKey('h', 'e')].prelude

describe('useExecutionPrelude', () => {
  beforeEach(() => { fetchExecutionPrelude.mockReset() })

  it('loads the first page at once, with no before', async () => {
    seed()
    fetchExecutionPrelude.mockResolvedValueOnce(ok('20', 'c1'))
    renderHook(() => useExecutionPrelude('h', 'e'))
    await waitFor(() => expect(prelude().status).toBe('ok'))
    expect(fetchExecutionPrelude).toHaveBeenCalledWith('h', 'e', { limit: PRELUDE_PAGE_LIMIT })
  })

  it('makes no request without the capability', async () => {
    seed({ cap: false })
    renderHook(() => useExecutionPrelude('h', 'e'))
    await new Promise((r) => setTimeout(r, 0))
    expect(fetchExecutionPrelude).not.toHaveBeenCalled()
  })

  it('makes no request without resume_session_id', async () => {
    seed({ resume: null })
    renderHook(() => useExecutionPrelude('h', 'e'))
    await new Promise((r) => setTimeout(r, 0))
    expect(fetchExecutionPrelude).not.toHaveBeenCalled()
  })

  it('two hooks, one request (the store status is the lock)', async () => {
    seed()
    let resolve!: (p: PreludePage) => void
    fetchExecutionPrelude.mockReturnValueOnce(new Promise((r) => { resolve = r }))
    // Both panes mount in one commit, before any effect can resolve.
    renderHook(() => { useExecutionPrelude('h', 'e'); useExecutionPrelude('h', 'e') })
    await act(async () => { resolve(ok('20', null)) })
    expect(fetchExecutionPrelude).toHaveBeenCalledTimes(1)
  })

  it('loadOlder sends the cursor; it is ignored while loading, done or in error', async () => {
    seed()
    fetchExecutionPrelude.mockResolvedValueOnce(ok('20', 'c1'))
    const { result } = renderHook(() => useExecutionPrelude('h', 'e'))
    await waitFor(() => expect(prelude().status).toBe('ok'))
    fetchExecutionPrelude.mockResolvedValueOnce(ok('20', 'c1')) // stuck cursor → error
    await act(async () => { result.current.loadOlder() })
    expect(fetchExecutionPrelude).toHaveBeenLastCalledWith('h', 'e', { before: 'c1', limit: PRELUDE_PAGE_LIMIT })
    await waitFor(() => expect(prelude().status).toBe('error'))
    await act(async () => { result.current.loadOlder() })
    expect(fetchExecutionPrelude).toHaveBeenCalledTimes(2)
  })

  it('retry re-asks after an error', async () => {
    seed()
    fetchExecutionPrelude.mockRejectedValueOnce(new Error('net'))
    const { result } = renderHook(() => useExecutionPrelude('h', 'e'))
    await waitFor(() => expect(prelude()).toMatchObject({ status: 'error', error: 'net' }))
    fetchExecutionPrelude.mockResolvedValueOnce(ok('20', null))
    await act(async () => { result.current.retry() })
    await waitFor(() => expect(prelude().status).toBe('ok'))
  })

  it('late page after clearExecution is dropped', async () => {
    seed()
    let resolve!: (p: PreludePage) => void
    fetchExecutionPrelude.mockReturnValueOnce(new Promise((r) => { resolve = r }))
    renderHook(() => useExecutionPrelude('h', 'e'))
    await waitFor(() => expect(fetchExecutionPrelude).toHaveBeenCalled())
    act(() => useExecutionStore.getState().clearExecution('h', 'e'))
    await act(async () => { resolve(ok('20', null)) })
    expect(useExecutionStore.getState().executions[executionKey('h', 'e')]).toBeUndefined()
  })

  it('clear → recreate → new request: the old answer is dropped, the new one lands (Review Focus 3)', async () => {
    seed()
    let resolveOld!: (p: PreludePage) => void
    let resolveNew!: (p: PreludePage) => void
    fetchExecutionPrelude
      .mockReturnValueOnce(new Promise((r) => { resolveOld = r }))
      .mockReturnValueOnce(new Promise((r) => { resolveNew = r }))
    const first = renderHook(() => useExecutionPrelude('h', 'e'))
    await waitFor(() => expect(fetchExecutionPrelude).toHaveBeenCalledTimes(1))
    first.unmount()
    act(() => useExecutionStore.getState().clearExecution('h', 'e'))
    seed()                                          // the entry comes back (undo / a new pane)
    renderHook(() => useExecutionPrelude('h', 'e'))
    await waitFor(() => expect(fetchExecutionPrelude).toHaveBeenCalledTimes(2))
    await act(async () => { resolveOld(ok('OLD', null)) })
    expect(prelude().status).toBe('loading')        // still waiting for its own request
    await act(async () => { resolveNew(ok('NEW', null)) })
    expect(prelude().items.map((i) => i.pos)).toEqual(['NEW'])
  })

  it('a 400 malformed_parameter on an older page restarts from the first page', async () => {
    seed()
    fetchExecutionPrelude.mockResolvedValueOnce(ok('20', 'c1'))
    const { result } = renderHook(() => useExecutionPrelude('h', 'e'))
    await waitFor(() => expect(prelude().status).toBe('ok'))
    fetchExecutionPrelude
      .mockRejectedValueOnce(new NexApiError(400, 'malformed_parameter', 'bad cursor'))
      .mockResolvedValueOnce(ok('20', null))
    await act(async () => { result.current.loadOlder() })
    await waitFor(() => expect(prelude()).toMatchObject({ status: 'ok', done: true }))
    expect(fetchExecutionPrelude).toHaveBeenLastCalledWith('h', 'e', { limit: PRELUDE_PAGE_LIMIT })
  })

  it('a page with no items but a cursor is not the end (spec §4.3)', async () => {
    seed()
    fetchExecutionPrelude.mockResolvedValueOnce({ state: 'ok', items: [], prevCursor: 'c1', totalBytes: null })
    renderHook(() => useExecutionPrelude('h', 'e'))
    await waitFor(() => expect(prelude()).toMatchObject({ status: 'ok', done: false, cursor: 'c1', pages: 1 }))
  })

  it('loadAll pages until done', async () => {
    seed()
    fetchExecutionPrelude
      .mockResolvedValueOnce(ok('30', 'c2'))
      .mockResolvedValueOnce(ok('20', 'c1'))
      .mockResolvedValueOnce(ok('10', null))
    const { result } = renderHook(() => useExecutionPrelude('h', 'e'))
    await waitFor(() => expect(prelude().status).toBe('ok'))
    await act(async () => { await result.current.loadAll() })
    expect(prelude()).toMatchObject({ done: true })
    expect(prelude().items.map((i) => i.pos)).toEqual(['10', '20', '30'])
  })

  it('loadAll stopped by its page cap while pages remain ends in an error, not silently', async () => {
    seed()
    let n = 0
    fetchExecutionPrelude.mockImplementation(async () => { n++; return ok(String(1000 - n), `c${n}`) })
    const { result } = renderHook(() => useExecutionPrelude('h', 'e'))
    await waitFor(() => expect(prelude().status).toBe('ok'))
    await act(async () => { await result.current.loadAll(2) })
    expect(fetchExecutionPrelude).toHaveBeenCalledTimes(3)
    expect(prelude()).toMatchObject({ status: 'error', error: 'prelude: too many pages', done: false })
  })

  it('loadAll(2) started while the first page is in flight: the wait does not use up the quota', async () => {
    seed()
    let resolveFirst!: (p: PreludePage) => void
    fetchExecutionPrelude
      .mockReturnValueOnce(new Promise((r) => { resolveFirst = r }))
      .mockResolvedValueOnce(ok('20', 'c1'))
      .mockResolvedValueOnce(ok('10', null))
    const { result } = renderHook(() => useExecutionPrelude('h', 'e'))
    await waitFor(() => expect(fetchExecutionPrelude).toHaveBeenCalledTimes(1))
    let all!: Promise<void>
    act(() => { all = result.current.loadAll(2) })
    await act(async () => { resolveFirst(ok('30', 'c2')) })
    await act(async () => { await all })
    expect(prelude()).toMatchObject({ status: 'ok', done: true, error: null })
  })

  it('loadAll started while the first page is in flight waits for it, then pages on without re-requesting it', async () => {
    seed()
    let resolveFirst!: (p: PreludePage) => void
    fetchExecutionPrelude
      .mockReturnValueOnce(new Promise((r) => { resolveFirst = r }))
      .mockResolvedValueOnce(ok('20', 'c1'))
      .mockResolvedValueOnce(ok('10', null))
    const { result } = renderHook(() => useExecutionPrelude('h', 'e'))
    await waitFor(() => expect(fetchExecutionPrelude).toHaveBeenCalledTimes(1))
    expect(prelude().status).toBe('loading')
    let all!: Promise<void>
    act(() => { all = result.current.loadAll() })
    await act(async () => { resolveFirst(ok('30', 'c2')) })
    await act(async () => { await all })
    expect(prelude()).toMatchObject({ done: true, pages: 3 })
    expect(prelude().items.map((i) => i.pos)).toEqual(['10', '20', '30'])
    expect(fetchExecutionPrelude.mock.calls.map((c) => c[2]?.before)).toEqual([undefined, 'c2', 'c1'])
  })

  it('loadAll waiting on an in-flight page returns when the execution is cleared', async () => {
    seed()
    let resolveFirst!: (p: PreludePage) => void
    fetchExecutionPrelude.mockReturnValueOnce(new Promise((r) => { resolveFirst = r }))
    const { result } = renderHook(() => useExecutionPrelude('h', 'e'))
    await waitFor(() => expect(fetchExecutionPrelude).toHaveBeenCalledTimes(1))
    let all!: Promise<void>
    act(() => { all = result.current.loadAll() })
    act(() => useExecutionStore.getState().clearExecution('h', 'e'))
    await act(async () => { await all })
    await act(async () => { resolveFirst(ok('20', null)) })
    expect(fetchExecutionPrelude).toHaveBeenCalledTimes(1)
    expect(useExecutionStore.getState().executions[executionKey('h', 'e')]).toBeUndefined()
  })
})
