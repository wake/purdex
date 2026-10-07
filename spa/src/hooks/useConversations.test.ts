import { StrictMode } from 'react'
import { describe, it, expect, beforeEach, vi } from 'vitest'
import { renderHook, act, waitFor } from '@testing-library/react'
import { HandoffApiError } from '../lib/nex/handoff-api'
import type { ConversationsPage } from '../lib/nex/conversations-api'

vi.mock('../lib/nex/conversations-api', () => ({ listConversations: vi.fn() }))
import { listConversations } from '../lib/nex/conversations-api'
import { useConversations } from './useConversations'

const mock = vi.mocked(listConversations)
const mk = (tag: string, state: 'ended' | 'gone' = 'ended'): ConversationsPage => ({
  state, scanned_at: 1, home: tag, total: 0, truncated: false, unknown_owner: 0, conversations: [],
})
function deferred<T>() {
  let resolve!: (v: T) => void
  let reject!: (e: unknown) => void
  const promise = new Promise<T>((a, b) => { resolve = a; reject = b })
  return { promise, resolve, reject }
}

describe('useConversations', () => {
  beforeEach(() => { mock.mockReset() })

  it('fetches on mount', async () => {
    mock.mockResolvedValueOnce(mk('a'))
    const { result } = renderHook(() => useConversations('h1', 'ended'))
    expect(result.current.phase).toBe('loading')
    expect(result.current.page).toBeNull()
    await waitFor(() => expect(result.current.phase).toBe('ready'))
    expect(result.current.page?.home).toBe('a')
    expect(mock).toHaveBeenCalledWith('h1', 'ended', undefined)
  })

  it('passes scope to listConversations; scopes do not share a pending request', async () => {
    const d1 = deferred<ConversationsPage>()
    const d2 = deferred<ConversationsPage>()
    mock.mockReturnValueOnce(d1.promise).mockReturnValueOnce(d2.promise)
    const a = renderHook(() => useConversations('hs', 'ended', 'normal'))
    const b = renderHook(() => useConversations('hs', 'ended', 'test'))
    expect(mock).toHaveBeenCalledTimes(2)
    expect(mock).toHaveBeenCalledWith('hs', 'ended', 'normal')
    expect(mock).toHaveBeenCalledWith('hs', 'ended', 'test')
    await act(async () => { d1.resolve(mk('n')); d2.resolve(mk('t')) })
    expect(a.result.current.page?.home).toBe('n')
    expect(b.result.current.page?.home).toBe('t')
  })

  it('the same scope shares one request', async () => {
    mock.mockResolvedValue(mk('x'))
    renderHook(() => useConversations('hq', 'gone', 'test'))
    renderHook(() => useConversations('hq', 'gone', 'test'))
    expect(mock).toHaveBeenCalledTimes(1)
  })

  it('drops a stale host response and fetches the new host', async () => {
    const d1 = deferred<ConversationsPage>()
    mock.mockReturnValueOnce(d1.promise).mockResolvedValueOnce(mk('two'))
    const { result, rerender } = renderHook(({ h }) => useConversations(h, 'ended'), { initialProps: { h: 'h1' } })
    rerender({ h: 'h2' })
    await waitFor(() => expect(result.current.page?.home).toBe('two'))
    await act(async () => { d1.resolve(mk('one')) })
    expect(result.current.page?.home).toBe('two')
    expect(mock).toHaveBeenCalledTimes(2)
  })

  it('drops a stale state response', async () => {
    const d1 = deferred<ConversationsPage>()
    mock.mockReturnValueOnce(d1.promise).mockResolvedValueOnce(mk('gone', 'gone'))
    const { result, rerender } = renderHook(({ s }) => useConversations('h', s), { initialProps: { s: 'ended' as 'ended' | 'gone' } })
    rerender({ s: 'gone' })
    await waitFor(() => expect(result.current.page?.state).toBe('gone'))
    await act(async () => { d1.resolve(mk('x')) })
    expect(result.current.page?.state).toBe('gone')
  })

  it('does not show the old host page for the new host', async () => {
    const d2 = deferred<ConversationsPage>()
    mock.mockResolvedValueOnce(mk('one')).mockReturnValueOnce(d2.promise)
    const { result, rerender } = renderHook(({ h }) => useConversations(h, 'ended'), { initialProps: { h: 'h1' } })
    await waitFor(() => expect(result.current.page?.home).toBe('one'))
    rerender({ h: 'h2' })
    expect(result.current.page).toBeNull()
    expect(result.current.phase).toBe('loading')
  })

  it('refetch while a request is in flight issues no second request', async () => {
    const d = deferred<ConversationsPage>()
    mock.mockReturnValueOnce(d.promise)
    const { result } = renderHook(() => useConversations('h', 'ended'))
    act(() => { result.current.refetch(); result.current.refetch() })
    expect(mock).toHaveBeenCalledTimes(1)
    await act(async () => { d.resolve(mk('a')) })
    expect(result.current.phase).toBe('ready')
  })

  it('refetch after settle fetches again, keeping the page visible and flipping to loading', async () => {
    const d = deferred<ConversationsPage>()
    mock.mockResolvedValueOnce(mk('a')).mockReturnValueOnce(d.promise)
    const { result } = renderHook(() => useConversations('h', 'ended'))
    await waitFor(() => expect(result.current.phase).toBe('ready'))
    act(() => result.current.refetch())
    expect(mock).toHaveBeenCalledTimes(2)
    expect(result.current.phase).toBe('loading')
    expect(result.current.page?.home).toBe('a')
    await act(async () => { d.resolve(mk('b')) })
    expect(result.current.page?.home).toBe('b')
    expect(result.current.phase).toBe('ready')
  })

  it('retries after an error', async () => {
    mock.mockRejectedValueOnce(new HandoffApiError(503, 'conversations_unavailable', {}, 'down')).mockResolvedValueOnce(mk('a'))
    const { result } = renderHook(() => useConversations('h', 'ended'))
    await waitFor(() => expect(result.current.phase).toBe('error'))
    expect(result.current.error).toBe('conversations_unavailable')
    expect(result.current.unavailable).toBe(false)
    act(() => result.current.refetch())
    expect(result.current.phase).toBe('loading')
    expect(result.current.error).toBeNull()
    await waitFor(() => expect(result.current.phase).toBe('ready'))
    expect(result.current.page?.home).toBe('a')
  })

  it('a failed refetch keeps the last good page and sets error', async () => {
    mock.mockResolvedValueOnce(mk('a')).mockRejectedValueOnce(new HandoffApiError(0, 'network', {}, 'offline'))
    const { result } = renderHook(() => useConversations('h', 'ended'))
    await waitFor(() => expect(result.current.phase).toBe('ready'))
    act(() => result.current.refetch())
    await waitFor(() => expect(result.current.phase).toBe('error'))
    expect(result.current.page?.home).toBe('a')
    expect(result.current.error).toBe('network')
  })

  it('404 sets unavailable', async () => {
    mock.mockRejectedValueOnce(new HandoffApiError(404, 'http_404', {}))
    const { result } = renderHook(() => useConversations('h', 'ended'))
    await waitFor(() => expect(result.current.phase).toBe('error'))
    expect(result.current.unavailable).toBe(true)
  })

  describe('shared pending request', () => {
    it('StrictMode double mount issues one call and shows the page', async () => {
      const d = deferred<ConversationsPage>()
      mock.mockReturnValueOnce(d.promise)
      const { result } = renderHook(() => useConversations('sm', 'ended'), { wrapper: StrictMode })
      expect(mock).toHaveBeenCalledTimes(1)
      await act(async () => { d.resolve(mk('a')) })
      expect(result.current.phase).toBe('ready')
      expect(result.current.page?.home).toBe('a')
    })

    it('two hooks for the same host and state share one call', async () => {
      const d = deferred<ConversationsPage>()
      mock.mockReturnValueOnce(d.promise)
      const a = renderHook(() => useConversations('two', 'ended'))
      const b = renderHook(() => useConversations('two', 'ended'))
      expect(mock).toHaveBeenCalledTimes(1)
      await act(async () => { d.resolve(mk('x')) })
      expect(a.result.current.page?.home).toBe('x')
      expect(b.result.current.page?.home).toBe('x')
    })

    it('a different state has its own call', async () => {
      const d1 = deferred<ConversationsPage>()
      const d2 = deferred<ConversationsPage>()
      mock.mockReturnValueOnce(d1.promise).mockReturnValueOnce(d2.promise)
      renderHook(() => useConversations('ds', 'ended'))
      renderHook(() => useConversations('ds', 'gone'))
      expect(mock).toHaveBeenCalledTimes(2)
      await act(async () => { d1.resolve(mk('a')); d2.resolve(mk('b', 'gone')) })
    })

    it('refetch after settle starts a new call', async () => {
      mock.mockResolvedValueOnce(mk('a')).mockResolvedValueOnce(mk('b'))
      const { result } = renderHook(() => useConversations('rs', 'ended'), { wrapper: StrictMode })
      await waitFor(() => expect(result.current.phase).toBe('ready'))
      expect(mock).toHaveBeenCalledTimes(1)
      act(() => result.current.refetch())
      expect(mock).toHaveBeenCalledTimes(2)
      await waitFor(() => expect(result.current.page?.home).toBe('b'))
    })
  })
})
