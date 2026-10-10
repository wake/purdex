// spa/src/components/team/seat-workbook.viewing.test.tsx — #2417: a reconnect with a workbook view open refills a full page
// (limit 20) even though the roster loader's limit-1 `loadSeat` already has the conversation loading.
import { describe, it, expect, beforeEach, vi } from 'vitest'
import { renderHook, act } from '@testing-library/react'
import type { ConversationResult } from '../../lib/workbook/api'

const fetchConversation = vi.fn<(hostId: string, provider: string, sessionId: string, q?: { limit?: number; before?: number }) => Promise<ConversationResult>>()
vi.mock('../../lib/workbook/api', () => ({ fetchConversation: (...a: Parameters<typeof fetchConversation>) => fetchConversation(...a) }))

import { useWorkbookViewing } from './seat-workbook'
import { selectConv, useWorkbookStore } from '../../stores/useWorkbookStore'
import { entry, seedWorkbook } from '../../lib/team/__tests__/workbook-fixture'

const st = () => useWorkbookStore.getState()
const page = (ids: number[]): ConversationResult =>
  ({ kind: 'ok', page: { convKey: 'c-S', status: 'x', statusAt: 5, entries: ids.map((i) => entry(i)), todos: null, refreshAvailable: null } })
function deferred<T>() {
  let resolve!: (v: T) => void
  const promise = new Promise<T>((res) => { resolve = res })
  return { promise, resolve }
}
const limits = () => fetchConversation.mock.calls.map((c) => c[3]?.limit)
const flush = async () => { await act(async () => { await new Promise((r) => setTimeout(r, 0)) }) }

beforeEach(() => {
  st().reset()
  fetchConversation.mockReset()
  fetchConversation.mockResolvedValue(page([3, 2, 1]))
})

for (const [label, support] of [['v1', { v1: true, v2: false }], ['v2', { v1: true, v2: true }]] as const) {
  describe(`reconnect with a view open (${label} daemon)`, () => {
    it('refills with limit 20 although loadSeat (limit 1) has the conversation loading', async () => {
      seedWorkbook('h1', 'S', {}, support)
      renderHook(() => useWorkbookViewing('h1', 'S'))
      await flush()
      fetchConversation.mockClear()
      const seat = deferred<ConversationResult>()
      fetchConversation.mockReturnValueOnce(seat.promise) // the roster loader's limit-1 ask, still out
      await act(async () => {
        st().fence('h1')
        st().setSupport('h1', support)
        void st().loadSeat('h1', 'S')
      })
      await flush()
      expect(limits()).toEqual([1, 20])
      seat.resolve(page([3]))
      await flush()
      expect(selectConv(st(), 'h1', 'c-S')?.entries.map((e) => e.id)).toEqual([3, 2, 1])
    })
  })
}

describe('reconnect without a view', () => {
  it('only the roster loader asks (limit 1)', async () => {
    seedWorkbook('h1', 'S')
    st().fence('h1')
    st().setSupport('h1', { v1: true, v2: false })
    await act(async () => { await st().loadSeat('h1', 'S') })
    expect(limits()).toEqual([1])
  })
})

describe('an answer of an older generation', () => {
  it('does not overwrite the newer data', async () => {
    seedWorkbook('h1', 'S')
    const old = deferred<ConversationResult>()
    fetchConversation.mockReturnValueOnce(old.promise)
    renderHook(() => useWorkbookViewing('h1', 'S')) // generation 0: a limit-20 ask stays out
    await flush()
    await act(async () => { st().fence('h1'); st().setSupport('h1', { v1: true, v2: false }) }) // generation 1 asks again
    await flush()
    expect(selectConv(st(), 'h1', 'c-S')?.entries.map((e) => e.id)).toEqual([3, 2, 1])
    old.resolve(page([99]))
    await flush()
    expect(selectConv(st(), 'h1', 'c-S')?.entries.map((e) => e.id)).toEqual([3, 2, 1])
  })
})
