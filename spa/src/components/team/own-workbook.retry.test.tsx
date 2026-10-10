// spa/src/components/team/own-workbook.retry.test.tsx — a failed probe of the tab's own conversation is retried (bounded backoff);
// a 404 or a success is not asked again.
import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest'
import { renderHook, act } from '@testing-library/react'
import type { ConversationResult } from '../../lib/workbook/api'

const fetchConversation = vi.fn<(hostId: string, provider: string, sessionId: string, q?: { limit?: number }) => Promise<ConversationResult>>()
vi.mock('../../lib/workbook/api', () => ({ fetchConversation: (...a: Parameters<typeof fetchConversation>) => fetchConversation(...a) }))

import { useOwnWorkbook } from './own-workbook'
import { useTabStore } from '../../stores/useTabStore'
import { useWorkbookStore } from '../../stores/useWorkbookStore'
import type { Tab, PaneContent, PaneLayout } from '../../types/tab'

const layout = { type: 'leaf', pane: { id: 'p', content: {
  kind: 'tmux-session', hostId: 'h1', sessionCode: 'abc', mode: 'terminal', cachedName: 'n', tmuxInstance: 'i',
  rebuild: { sessionName: 'n', tmuxInstance: 'i', agent: { type: 'cc', sessionId: 'S1', updatedAt: 1 }, capturedAt: 1 }, } as PaneContent } } as PaneLayout
const ok: ConversationResult = { kind: 'ok', page: { convKey: 'c-S1', status: 'Doing x.', statusAt: 5, entries: [], todos: null, refreshAvailable: null } }

beforeEach(() => {
  vi.useFakeTimers()
  useWorkbookStore.getState().reset()
  fetchConversation.mockReset()
  useTabStore.setState({ tabs: { t1: { id: 't1', layout } as unknown as Tab }, activeTabId: 't1' } as never)
  useWorkbookStore.getState().setSupport('h1', { v1: true, v2: false })
})
afterEach(() => { vi.useRealTimers() })

describe('useOwnWorkbook retry', () => {
  it('first probe fails, a later one succeeds in the same generation: the workbook appears', async () => {
    fetchConversation.mockRejectedValueOnce(new Error('offline')).mockResolvedValue(ok)
    const { result } = renderHook(() => useOwnWorkbook())
    await act(async () => { await vi.advanceTimersByTimeAsync(0) })
    expect(result.current).toBeNull()
    for (let i = 0; i < 5; i++) await act(async () => { await vi.advanceTimersByTimeAsync(1000) })
    expect(result.current).toEqual({ hostId: 'h1', sessionId: 'S1' })
    expect(fetchConversation).toHaveBeenCalledTimes(2)
    await act(async () => { await vi.advanceTimersByTimeAsync(60000) })
    expect(fetchConversation).toHaveBeenCalledTimes(2) // success: no repeat
  })

  it('retries are bounded', async () => {
    fetchConversation.mockRejectedValue(new Error('offline'))
    renderHook(() => useOwnWorkbook())
    for (let i = 0; i < 120; i++) await act(async () => { await vi.advanceTimersByTimeAsync(1000) }) // a render per step
    expect(fetchConversation).toHaveBeenCalledTimes(4) // the first ask + 3 retries
  })

  it('a 404 is an answer: asked once', async () => {
    fetchConversation.mockResolvedValue({ kind: 'not_found' })
    const { result } = renderHook(() => useOwnWorkbook())
    await act(async () => { await vi.advanceTimersByTimeAsync(60000) })
    expect(fetchConversation).toHaveBeenCalledTimes(1)
    expect(result.current).toBeNull()
  })
})
