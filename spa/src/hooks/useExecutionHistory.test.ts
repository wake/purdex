import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { renderHook, waitFor, act } from '@testing-library/react'

const listAll = vi.fn()
vi.mock('../lib/nex/list-all-executions', () => ({ listAllExecutions: (...a: unknown[]) => listAll(...a), LIST_PAGE_LIMIT: 500, LIST_MAX_PAGES: 20 }))
import { useExecutionHistory, HISTORY_MIN_INTERVAL_MS } from './useExecutionHistory'
import { useExecutionListStore } from '../stores/useExecutionListStore'

const row = (id: string) => ({ id }) as never
const res = (items: unknown[], extra = {}) => ({ items, dropped: 0, truncated: false, stuck: false, stuckPage: null, ...extra })

describe('useExecutionHistory', () => {
  beforeEach(() => {
    listAll.mockReset()
    useExecutionListStore.setState({ byHost: {} })
  })

  it('fetches with includeArchived and reports ready', async () => {
    listAll.mockResolvedValue(res([row('a')], { truncated: true }))
    const { result } = renderHook(() => useExecutionHistory('h1'))
    expect(result.current.phase).toBe('loading')
    await waitFor(() => expect(result.current.phase).toBe('ready'))
    expect(listAll.mock.calls[0][0]).toBe('h1')
    expect(listAll.mock.calls[0][1]).toEqual({ includeArchived: true })
    expect(result.current.items).toHaveLength(1)
    expect(result.current.truncated).toBe(true)
  })

  const bump = (n: number) => act(() => {
    useExecutionListStore.setState({ byHost: { h1: { items: [], phase: 'ready', error: null, lastSeq: null, refreshRevision: n, truncated: false } as never } })
  })

  describe('walk scheduling (fake timers)', () => {
    beforeEach(() => { vi.useFakeTimers() })
    afterEach(() => { vi.useRealTimers() })
    const settle = () => act(async () => { await vi.advanceTimersByTimeAsync(0) })

    it('refetches when refreshRevision bumps, after the minimum gap', async () => {
      listAll.mockResolvedValue(res([]))
      const { result } = renderHook(() => useExecutionHistory('h1'))
      await settle()
      expect(result.current.phase).toBe('ready')
      expect(listAll).toHaveBeenCalledTimes(1)
      bump(1)
      await settle()
      expect(listAll).toHaveBeenCalledTimes(1)
      await act(async () => { await vi.advanceTimersByTimeAsync(HISTORY_MIN_INTERVAL_MS) })
      expect(listAll).toHaveBeenCalledTimes(2)
    })

    it('bumps inside the window coalesce into one walk after it', async () => {
      listAll.mockResolvedValue(res([]))
      renderHook(() => useExecutionHistory('h1'))
      await settle()
      bump(1); bump(2); bump(3)
      await act(async () => { await vi.advanceTimersByTimeAsync(HISTORY_MIN_INTERVAL_MS - 1) })
      expect(listAll).toHaveBeenCalledTimes(1)
      await act(async () => { await vi.advanceTimersByTimeAsync(1) })
      expect(listAll).toHaveBeenCalledTimes(2)
      await act(async () => { await vi.advanceTimersByTimeAsync(HISTORY_MIN_INTERVAL_MS * 3) })
      expect(listAll).toHaveBeenCalledTimes(2)
    })

    it('three bumps during one walk give exactly one trailing walk', async () => {
      let resolve1!: (v: unknown) => void
      listAll.mockImplementationOnce(() => new Promise((r) => { resolve1 = r }))
      listAll.mockResolvedValue(res([]))
      renderHook(() => useExecutionHistory('h1'))
      await settle()
      bump(1); bump(2); bump(3)
      await act(async () => { await vi.advanceTimersByTimeAsync(HISTORY_MIN_INTERVAL_MS * 2) })
      expect(listAll).toHaveBeenCalledTimes(1) // still in flight: never a second concurrent walk
      await act(async () => { resolve1(res([])) })
      await settle()
      expect(listAll).toHaveBeenCalledTimes(2)
      await act(async () => { await vi.advanceTimersByTimeAsync(HISTORY_MIN_INTERVAL_MS * 3) })
      expect(listAll).toHaveBeenCalledTimes(2)
    })

    it('a host change starts at once, ignoring the window', async () => {
      listAll.mockResolvedValue(res([]))
      const { rerender } = renderHook(({ h }) => useExecutionHistory(h), { initialProps: { h: 'a' } })
      await settle()
      rerender({ h: 'b' })
      await settle()
      expect(listAll).toHaveBeenCalledTimes(2)
      expect(listAll.mock.calls[1][0]).toBe('b')
    })
  })

  it('drops a stale response after the host changed, and passes isCurrent', async () => {
    let resolveA!: (v: unknown) => void
    listAll.mockImplementationOnce(() => new Promise((r) => { resolveA = r }))
    listAll.mockResolvedValueOnce(res([row('b')]))
    const { result, rerender } = renderHook(({ h }) => useExecutionHistory(h), { initialProps: { h: 'a' } })
    const isCurrentA = listAll.mock.calls[0][2] as () => boolean
    expect(isCurrentA()).toBe(true)
    rerender({ h: 'b' })
    await waitFor(() => expect(result.current.items.map((x) => x.id)).toEqual(['b']))
    expect(isCurrentA()).toBe(false)
    await act(async () => { resolveA(res([row('a')])) })
    expect(result.current.items.map((x) => x.id)).toEqual(['b'])
  })

  it('a stuck walk logs the warning and is not truncation', async () => {
    const warn = vi.spyOn(console, 'warn').mockImplementation(() => {})
    listAll.mockResolvedValue(res([row('a')], { stuck: true, stuckPage: 3 }))
    const { result } = renderHook(() => useExecutionHistory('h1'))
    await waitFor(() => expect(result.current.phase).toBe('ready'))
    expect(warn).toHaveBeenCalledWith('nex: execution history cursor repeated', { hostId: 'h1', page: 3 })
    expect(result.current.truncated).toBe(false)
    warn.mockRestore()
  })

  it('error keeps previous items', async () => {
    listAll.mockResolvedValueOnce(res([row('a')]))
    const { result } = renderHook(() => useExecutionHistory('h1'))
    await waitFor(() => expect(result.current.phase).toBe('ready'))
    listAll.mockRejectedValueOnce(new Error('malformed'))
    act(() => { result.current.refetch() })
    await waitFor(() => expect(result.current.phase).toBe('error'))
    expect(result.current.error).toContain('malformed')
    expect(result.current.items).toHaveLength(1)
  })
})
