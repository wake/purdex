import { describe, it, expect, vi, beforeEach } from 'vitest'
import { renderHook, waitFor, act } from '@testing-library/react'

const listAll = vi.fn()
vi.mock('../lib/nex/list-all-executions', () => ({ listAllExecutions: (...a: unknown[]) => listAll(...a), LIST_PAGE_LIMIT: 500, LIST_MAX_PAGES: 20 }))
import { useExecutionHistory } from './useExecutionHistory'
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

  it('refetches when refreshRevision bumps', async () => {
    listAll.mockResolvedValue(res([]))
    const { result } = renderHook(() => useExecutionHistory('h1'))
    await waitFor(() => expect(result.current.phase).toBe('ready'))
    expect(listAll).toHaveBeenCalledTimes(1)
    act(() => {
      useExecutionListStore.setState({ byHost: { h1: { items: [], phase: 'ready', error: null, lastSeq: null, refreshRevision: 1, truncated: false } as never } })
    })
    await waitFor(() => expect(listAll).toHaveBeenCalledTimes(2))
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
