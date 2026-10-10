// spa/src/stores/useWorkbookStore.v2.test.ts — WA-1b: the v2 data layer (todos, refresh, loadUntil) on top of WA-1a's rules.
import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest'
import type { ConversationResult, RefreshResult, TodosResult } from '../lib/workbook/api'
import type { TodoLists, WorkbookEntry, WorkbookTodo } from '../lib/workbook/types'

const fetchConversation = vi.fn<(hostId: string, provider: string, sessionId: string, q?: { limit?: number; before?: number }) => Promise<ConversationResult>>()
const fetchTodos = vi.fn<(hostId: string, sessionId: string, q: { state: string; limit?: number; before?: number }) => Promise<TodosResult>>()
const postRefresh = vi.fn<(hostId: string, sessionId: string) => Promise<RefreshResult>>()
vi.mock('../lib/workbook/api', () => ({
  fetchConversation: (...a: Parameters<typeof fetchConversation>) => fetchConversation(...a),
  fetchTodos: (...a: Parameters<typeof fetchTodos>) => fetchTodos(...a),
  postRefresh: (...a: Parameters<typeof postRefresh>) => postRefresh(...a),
}))

import { MAX_DONE_TODOS, MAX_OPEN_TODOS, MAX_RESNAPS, MAX_TODO_TOUCHES, MAX_UNTIL_PAGES, RESNAP_BACKOFF_MS, selectConv, selectRefreshPending, selectTodoCaps, useWorkbookStore } from './useWorkbookStore'

const entry = (id: number, over: Partial<WorkbookEntry> = {}): WorkbookEntry => ({
  id, convKey: 'c1', sessionId: 's1', turnId: `t${id}`, turnAt: id * 1000, state: 'ok', reason: '', thing: `thing ${id}`, push: '', entry: '',
  thingDone: false, createdAt: id * 1000, updatedAt: id * 1000,
  kind: 'turn', usage: { in: 0, out: 0, cacheRead: 0 }, todoChanges: { added: [], done: [], dropped: [] }, ...over,
})
const todo = (id: number, state: WorkbookTodo['state'] = 'open', over: Partial<WorkbookTodo> = {}): WorkbookTodo => ({
  id, title: `todo ${id}`, detail: '', state, closedBy: '', createdAt: id, closedAt: state === 'open' ? 0 : id + 1, addedEntryId: 1, closedEntryId: 0, ...over,
})
const page = (entries: WorkbookEntry[], over: { todos?: TodoLists | null; refreshAvailable?: boolean | null } = {}): ConversationResult =>
  ({ kind: 'ok', page: { convKey: 'c1', status: 'doing', statusAt: 5, entries, todos: over.todos ?? null, refreshAvailable: over.refreshAvailable ?? null } })
const st = () => useWorkbookStore.getState()
const conv = () => selectConv(st(), 'h1', 'c1')
const ids = (ts: WorkbookTodo[] | undefined) => ts?.map((t) => t.id)
const V1 = { v1: true, v2: false }
const V2 = { v1: true, v2: true }
const refreshEntry = (id: number, state: WorkbookEntry['state'], updatedAt = 10_000 + id) => entry(id, { kind: 'refresh', state, updatedAt })

beforeEach(() => {
  st().reset()
  fetchConversation.mockReset()
  fetchTodos.mockReset()
  postRefresh.mockReset()
  fetchConversation.mockResolvedValue(page([entry(3), entry(2)]))
})

describe('todos', () => {
  it('a workbook.todos event upserts by id and moves an item open → done', () => {
    st().applyTodos('h1', { convKey: 'c1', sessionId: 's1', todos: [todo(1), todo(2), todo(3)] })
    expect(ids(conv()?.todos.open)).toEqual([1, 2, 3])
    st().applyTodos('h1', { convKey: 'c1', sessionId: 's1', todos: [todo(2, 'done'), todo(4)] })
    expect(ids(conv()?.todos.open)).toEqual([1, 3, 4])
    expect(ids(conv()?.todos.done)).toEqual([2])
    st().applyTodos('h1', { convKey: 'c1', sessionId: 's1', todos: [todo(1, 'done'), todo(3, 'dropped')] })
    expect(ids(conv()?.todos.open)).toEqual([4])
    expect(ids(conv()?.todos.done)).toEqual([2, 1].sort((a, b) => b - a)) // newest (highest id) first
    expect(conv()?.todos.doneOldestId).toBe(1)
    expect(st().convOfSession.h1.s1).toBe('c1')
  })

  it('openCapped is reset by a reconciled full snapshot; doneCapped is not (it records dropped older records, which a snapshot does not bring back)', async () => {
    st().setSupport('h1', V2)
    st().applyTodos('h1', { convKey: 'c1', sessionId: 's1', todos: Array.from({ length: MAX_OPEN_TODOS + 1 }, (_, i) => todo(i + 1)) })
    st().applyTodos('h1', { convKey: 'c1', sessionId: 's1', todos: Array.from({ length: MAX_DONE_TODOS + 1 }, (_, i) => todo(9000 + i, 'done')) })
    expect(selectTodoCaps(conv())).toEqual({ openCapped: true, doneCapped: true })
    fetchConversation.mockResolvedValue(page([entry(3)], { todos: { open: [todo(2)], done: [todo(9100, 'done')] } }))
    await st().openWorkbook('h1', 's1')
    expect(ids(conv()?.todos.open)).toEqual([2])
    expect(selectTodoCaps(conv())).toEqual({ openCapped: false, doneCapped: true })
  })

  it('keeps every open todo the daemon sends (well past the old 50), and marks openCapped only past MAX_OPEN_TODOS', () => {
    st().applyTodos('h1', { convKey: 'c1', sessionId: 's1', todos: Array.from({ length: 60 }, (_, i) => todo(i + 1)) })
    expect(conv()?.todos.open).toHaveLength(60)
    expect(conv()?.todos.openCapped).toBe(false)
    st().applyTodos('h1', { convKey: 'c1', sessionId: 's1', todos: Array.from({ length: MAX_OPEN_TODOS }, (_, i) => todo(1000 + i)) })
    expect(conv()?.todos.open).toHaveLength(MAX_OPEN_TODOS)
    expect(conv()?.todos.openCapped).toBe(true)
  })

  it('a done todo is never reopened by an open copy', () => {
    st().applyTodos('h1', { convKey: 'c1', sessionId: 's1', todos: [todo(1, 'done')] })
    st().applyTodos('h1', { convKey: 'c1', sessionId: 's1', todos: [todo(1)] })
    expect(conv()?.todos.open).toEqual([])
    expect(ids(conv()?.todos.done)).toEqual([1])
  })

  describe('a late answer (snapshot) never revives what events closed since its request started', () => {
    const lateSnapshot = async (during: () => void, open: WorkbookTodo[]) => {
      st().setSupport('h1', V2)
      let resolve!: (v: ConversationResult) => void
      fetchConversation.mockReturnValueOnce(new Promise<ConversationResult>((r) => { resolve = r }))
      const loading = st().openWorkbook('h1', 's1')
      during()
      resolve(page([entry(3)], { todos: { open, done: [] } }))
      await loading
    }

    it('the touched ids are refused, the rest of the snapshot applies', async () => {
      await lateSnapshot(
        () => st().applyTodos('h1', { convKey: 'c1', sessionId: 's1', todos: [todo(1, 'dropped'), todo(2, 'done')] }),
        [todo(1), todo(2), todo(3), todo(500)],
      )
      expect(ids(conv()?.todos.open)).toEqual([3, 500])
      expect(ids(conv()?.todos.done)).toEqual([2])
    })

    it('with more events than MAX_TODO_TOUCHES the history is gone, so the stale snapshot\'s open list is not trusted at all', async () => {
      st().applyTodos('h1', { convKey: 'c1', sessionId: 's1', todos: [todo(1), todo(7)] })
      await lateSnapshot(() => {
        st().applyTodos('h1', { convKey: 'c1', sessionId: 's1', todos: [todo(1, 'dropped')] }) // the first touch, soon evicted
        for (let i = 0; i < MAX_TODO_TOUCHES + 5; i++) st().applyTodos('h1', { convKey: 'c1', sessionId: 's1', todos: [todo(2000 + i, 'dropped')] })
      }, [todo(1), todo(7), todo(500)])
      expect(ids(conv()?.todos.open)).toEqual([7]) // 1 stays closed; 500 is not added from an answer that cannot be vouched for
    })

    it('an answer that started after the events is current: it applies in full', async () => {
      st().setSupport('h1', V2)
      st().applyTodos('h1', { convKey: 'c1', sessionId: 's1', todos: [todo(1, 'dropped')] })
      fetchConversation.mockResolvedValue(page([entry(3)], { todos: { open: [todo(5)], done: [] } }))
      await st().openWorkbook('h1', 's1')
      expect(ids(conv()?.todos.open)).toEqual([5])
    })
  })

  it('the conversation answer is the whole open list: it replaces what was there before its request and merges done', async () => {
    st().setSupport('h1', V2)
    st().applyTodos('h1', { convKey: 'c1', sessionId: 's1', todos: [todo(1), todo(2), todo(9)] }) // 1 and 9 are gone server-side
    fetchConversation.mockResolvedValue(page([entry(3)], { todos: { open: [todo(2), todo(5)], done: [todo(4, 'done'), todo(3, 'done')] } }))
    await st().openWorkbook('h1', 's1')
    expect(ids(conv()?.todos.open)).toEqual([2, 5])
    expect(ids(conv()?.todos.done)).toEqual([4, 3])
  })

  it('a high-id open todo the daemon no longer lists does not linger, whether the answer is empty or its largest id is lower', async () => {
    st().setSupport('h1', V2)
    st().applyTodos('h1', { convKey: 'c1', sessionId: 's1', todos: [todo(500)] })
    fetchConversation.mockResolvedValueOnce(page([entry(3)], { todos: { open: [], done: [] } }))
    await st().openWorkbook('h1', 's1')
    expect(conv()?.todos.open).toEqual([])
    st().applyTodos('h1', { convKey: 'c1', sessionId: 's1', todos: [todo(500)] })
    fetchConversation.mockResolvedValueOnce(page([entry(3)], { todos: { open: [todo(100)], done: [] } }))
    await st().openWorkbook('h1', 's1')
    expect(ids(conv()?.todos.open)).toEqual([100])
  })

  describe('an answer older than the touch history is not trusted, and re-asked (bounded, never event-driven)', () => {
    beforeEach(() => { vi.useFakeTimers() })
    afterEach(() => { vi.useRealTimers() })
    const flood = () => { for (let i = 0; i < MAX_TODO_TOUCHES + 5; i++) st().applyTodos('h1', { convKey: 'c1', sessionId: 's1', todos: [todo(3000 + i, 'dropped')] }) }
    const defer = () => { let r!: (v: ConversationResult) => void; const p = new Promise<ConversationResult>((res) => { r = res }); return { p, r } }
    /** One stale answer: the request starts, an event flood outruns the touch history, then the answer lands. */
    const staleAnswer = async (open: WorkbookTodo[]) => {
      const d = defer()
      fetchConversation.mockReturnValueOnce(d.p)
      const call = fetchConversation.mock.calls.length === 0 ? st().openWorkbook('h1', 's1') : st().loadSeat('h1', 's1')
      flood()
      d.r(page([entry(3)], { todos: { open, done: [] } }))
      await call
    }
    beforeEach(() => { st().setSupport('h1', V2) })

    it('keeps the local open list as it was, then re-asks once with limit 1 after the backoff, and the trusted answer replaces it', async () => {
      st().applyTodos('h1', { convKey: 'c1', sessionId: 's1', todos: [todo(7)] })
      await staleAnswer([todo(500)])
      expect(ids(conv()?.todos.open)).toEqual([7]) // not 500: the stale open list was not applied
      expect(fetchConversation).toHaveBeenCalledTimes(1)
      fetchConversation.mockResolvedValueOnce(page([entry(3)], { todos: { open: [todo(8)], done: [] } }))
      await vi.advanceTimersByTimeAsync(RESNAP_BACKOFF_MS)
      expect(fetchConversation).toHaveBeenCalledTimes(2)
      expect(fetchConversation.mock.calls[1][3]).toEqual({ limit: 1 })
      expect(ids(conv()?.todos.open)).toEqual([8])
      await vi.advanceTimersByTimeAsync(60_000)
      expect(fetchConversation).toHaveBeenCalledTimes(2) // reconciled: nothing pending
    })

    it('is bounded: at most MAX_RESNAPS re-asks in a row, one pending at a time', async () => {
      await staleAnswer([todo(500)])
      for (let n = 0; n < MAX_RESNAPS; n++) {
        const d = defer()
        fetchConversation.mockReturnValueOnce(d.p)
        await vi.advanceTimersByTimeAsync(RESNAP_BACKOFF_MS * 2 ** n)
        flood() // the re-ask is outrun again
        d.r(page([entry(3)], { todos: { open: [todo(501 + n)], done: [] } }))
        await vi.advanceTimersByTimeAsync(0)
      }
      expect(fetchConversation).toHaveBeenCalledTimes(1 + MAX_RESNAPS)
      await vi.advanceTimersByTimeAsync(600_000)
      expect(fetchConversation).toHaveBeenCalledTimes(1 + MAX_RESNAPS) // gave up; no loop
    })

    it('an older answer landing late does not clear the re-ask a newer request scheduled', async () => {
      const older = defer(); const newer = defer()
      fetchConversation.mockReturnValueOnce(older.p).mockReturnValueOnce(newer.p)
      const a = st().loadSeat('h1', 's1') // started first
      const b = st().openWorkbook('h1', 's1') // started second
      flood()
      newer.r(page([entry(3)], { todos: { open: [todo(500)], done: [] } })) // outrun by the flood: not trusted, schedules a re-ask
      await b
      older.r(page([entry(3)], { todos: { open: [], done: [] } })) // lands late: stale against the newer one
      await a
      fetchConversation.mockResolvedValueOnce(page([entry(3)], { todos: { open: [todo(8)], done: [] } }))
      await vi.advanceTimersByTimeAsync(RESNAP_BACKOFF_MS)
      expect(fetchConversation).toHaveBeenCalledTimes(3) // the re-ask still happened
      expect(ids(conv()?.todos.open)).toEqual([8])
    })

    it('a connection change cancels the pending re-ask', async () => {
      await staleAnswer([todo(500)])
      st().fence('h1')
      await vi.advanceTimersByTimeAsync(60_000)
      expect(fetchConversation).toHaveBeenCalledTimes(1)
    })

    it('the events themselves fetch nothing: a flood with no stale answer schedules no re-ask', async () => {
      flood()
      await vi.advanceTimersByTimeAsync(600_000)
      expect(fetchConversation).not.toHaveBeenCalled()
    })
  })

  it('a v1 answer (no todos) leaves the list alone', async () => {
    st().setSupport('h1', V1)
    st().applyTodos('h1', { convKey: 'c1', sessionId: 's1', todos: [todo(1)] })
    await st().openWorkbook('h1', 's1')
    expect(ids(conv()?.todos.open)).toEqual([1])
  })

  it('is bounded: the newest MAX_OPEN_TODOS open, the newest MAX_DONE_TODOS done, MAX_TODO_TOUCHES touches', () => {
    const open = Array.from({ length: MAX_OPEN_TODOS + 5 }, (_, i) => todo(i + 1))
    st().applyTodos('h1', { convKey: 'c1', sessionId: 's1', todos: open })
    expect(conv()?.todos.open).toHaveLength(MAX_OPEN_TODOS)
    expect(conv()?.todos.open[0].id).toBe(6)
    const done = Array.from({ length: MAX_DONE_TODOS + 5 }, (_, i) => todo(1000 + i, 'done'))
    st().applyTodos('h1', { convKey: 'c1', sessionId: 's1', todos: done })
    expect(conv()?.todos.done).toHaveLength(MAX_DONE_TODOS)
    expect(conv()?.todos.done[0].id).toBe(1000 + MAX_DONE_TODOS + 4)
    expect(conv()?.todos.doneOldestId).toBe(conv()?.todos.done[MAX_DONE_TODOS - 1].id)
    for (let i = 0; i < MAX_TODO_TOUCHES + 7; i++) st().applyTodos('h1', { convKey: 'c1', sessionId: 's1', todos: [todo(5000 + i, 'dropped')] })
    expect(conv()?.todos.touches).toHaveLength(MAX_TODO_TOUCHES)
  })
})

describe('loadMoreDone', () => {
  it('pages the done record before = doneOldestId, newest first, until a short page', async () => {
    st().setSupport('h1', V2)
    fetchConversation.mockResolvedValue(page([entry(3)], { todos: { open: [], done: [todo(30, 'done'), todo(29, 'done')] } }))
    await st().openWorkbook('h1', 's1')
    fetchTodos.mockResolvedValueOnce({ kind: 'ok', todos: Array.from({ length: 20 }, (_, i) => todo(28 - i, 'done')) }) // 28..9
    await st().loadMoreDone('h1', 'c1')
    expect(fetchTodos).toHaveBeenLastCalledWith('h1', 's1', { state: 'done', limit: 20, before: 29 })
    expect(conv()?.todos.doneOldestId).toBe(9)
    expect(conv()?.todos.doneExhausted).toBe(false)
    fetchTodos.mockResolvedValueOnce({ kind: 'ok', todos: [todo(8, 'done')] })
    await st().loadMoreDone('h1', 'c1')
    expect(fetchTodos).toHaveBeenLastCalledWith('h1', 's1', { state: 'done', limit: 20, before: 9 })
    expect(conv()?.todos.doneExhausted).toBe(true)
    await st().loadMoreDone('h1', 'c1')
    expect(fetchTodos).toHaveBeenCalledTimes(2)
  })

  it('the done record pages on well past 200 (retention is no 200-entry cut-off; spec §6 keeps it)', async () => {
    st().setSupport('h1', V2)
    st().applyTodos('h1', { convKey: 'c1', sessionId: 's1', todos: Array.from({ length: 250 }, (_, i) => todo(900 - i, 'done')) }) // 900..651
    fetchTodos.mockResolvedValueOnce({ kind: 'ok', todos: Array.from({ length: 20 }, (_, i) => todo(650 - i, 'done')) })
    await st().loadMoreDone('h1', 'c1')
    fetchTodos.mockResolvedValueOnce({ kind: 'ok', todos: Array.from({ length: 20 }, (_, i) => todo(630 - i, 'done')) })
    await st().loadMoreDone('h1', 'c1')
    expect(fetchTodos).toHaveBeenCalledTimes(2)
    expect(fetchTodos.mock.calls[1][2]).toMatchObject({ before: 631 })
    expect(conv()?.todos.done).toHaveLength(290)
    expect(conv()?.todos.doneOldestId).toBe(611)
    expect(conv()?.todos.doneCapped).toBe(false)
  })

  it('at the safety ceiling MAX_DONE_TODOS the cursor still advances past an evicted page, never refetches the same page, and paging stops (doneCapped)', async () => {
    st().setSupport('h1', V2)
    st().applyTodos('h1', { convKey: 'c1', sessionId: 's1', todos: Array.from({ length: MAX_DONE_TODOS }, (_, i) => todo(5000 - i, 'done')) }) // 5000 down, MAX_DONE_TODOS of them
    expect(conv()?.todos.doneCapped).toBe(false)
    const oldest = conv()?.todos.doneOldestId
    fetchTodos.mockResolvedValueOnce({ kind: 'ok', todos: Array.from({ length: 20 }, (_, i) => todo(5000 - MAX_DONE_TODOS - i, 'done')) }) // a full page, all older: the ceiling drops it
    await st().loadMoreDone('h1', 'c1')
    expect(fetchTodos).toHaveBeenLastCalledWith('h1', 's1', { state: 'done', limit: 20, before: oldest })
    expect(conv()?.todos.done).toHaveLength(MAX_DONE_TODOS)
    expect(conv()?.todos.doneCursor).toBe(5000 - MAX_DONE_TODOS - 19) // advanced to the page's oldest even though the page was not kept
    expect(conv()?.todos.doneCapped).toBe(true)
    await st().loadMoreDone('h1', 'c1')
    await st().loadMoreDone('h1', 'c1')
    expect(fetchTodos).toHaveBeenCalledTimes(1) // stopped, not refetching
  })

  it('a page that does not move the cursor ends the paging (exhausted)', async () => {
    st().setSupport('h1', V2)
    st().applyTodos('h1', { convKey: 'c1', sessionId: 's1', todos: [todo(50, 'done')] })
    fetchTodos.mockResolvedValue({ kind: 'ok', todos: Array.from({ length: 20 }, (_, i) => todo(50 + i, 'done')) }) // nothing below 50 (a bad server)
    await st().loadMoreDone('h1', 'c1')
    expect(conv()?.todos.doneExhausted).toBe(true)
    await st().loadMoreDone('h1', 'c1')
    expect(fetchTodos).toHaveBeenCalledTimes(1)
  })

  it('one load at a time; a failure releases it', async () => {
    st().setSupport('h1', V2)
    await st().openWorkbook('h1', 's1')
    fetchTodos.mockRejectedValueOnce(new Error('network'))
    await expect(st().loadMoreDone('h1', 'c1')).resolves.toBeUndefined()
    expect(conv()?.todos.loading).toBe(false)
  })

  it('no workbook.v2: no fetch', async () => {
    st().setSupport('h1', V1)
    await st().openWorkbook('h1', 's1')
    await st().loadMoreDone('h1', 'c1')
    expect(fetchTodos).not.toHaveBeenCalled()
  })
})

describe('requestRefresh', () => {
  beforeEach(() => { st().setSupport('h1', V2) })

  it('returns the three daemon answers as typed results, never throwing', async () => {
    await st().openWorkbook('h1', 's1')
    postRefresh.mockResolvedValueOnce({ kind: 'accepted', entryId: 12 })
    expect(await st().requestRefresh('h1', 'c1')).toEqual({ kind: 'accepted', entryId: 12 })
    expect(postRefresh).toHaveBeenLastCalledWith('h1', 's1') // a real session of the conversation, not the key
    postRefresh.mockResolvedValueOnce({ kind: 'not_live' })
    expect(await st().requestRefresh('h1', 'c1')).toEqual({ kind: 'not_live' })
    postRefresh.mockResolvedValueOnce({ kind: 'refresh_pending' })
    expect(await st().requestRefresh('h1', 'c1')).toEqual({ kind: 'refresh_pending' })
    postRefresh.mockRejectedValueOnce(Object.assign(new Error('x'), { code: 'http_500' }))
    expect(await st().requestRefresh('h1', 'c1')).toEqual({ kind: 'error', code: 'http_500' })
    postRefresh.mockRejectedValueOnce(new Error('boom'))
    expect(await st().requestRefresh('h1', 'c1')).toEqual({ kind: 'error', code: 'network' })
  })

  it('without workbook.v2 it posts nothing and says unsupported', async () => {
    st().setSupport('h1', V1)
    expect(await st().requestRefresh('h1', 'c1')).toEqual({ kind: 'unsupported' })
    st().fence('h1') // support unknown
    expect(await st().requestRefresh('h1', 'c1')).toEqual({ kind: 'unsupported' })
    expect(postRefresh).not.toHaveBeenCalled()
  })

  it('a 409 answer leaves refresh pending as it was', async () => {
    await st().openWorkbook('h1', 's1')
    postRefresh.mockResolvedValue({ kind: 'not_live' })
    await st().requestRefresh('h1', 'c1')
    expect(selectRefreshPending(conv())).toBe(false)
  })
})

describe('refresh pending (derived, not stored)', () => {
  beforeEach(async () => { st().setSupport('h1', V2); await st().openWorkbook('h1', 's1') })
  const ask = async (id = 12) => { postRefresh.mockResolvedValueOnce({ kind: 'accepted', entryId: id }); await st().requestRefresh('h1', 'c1') }

  it('is false with no refresh entry, true right after the 202, false once the entry is ok', async () => {
    expect(selectRefreshPending(conv())).toBe(false)
    await ask()
    expect(selectRefreshPending(conv())).toBe(true)
    expect(conv()?.entries[0]).toMatchObject({ id: 12, kind: 'refresh', state: 'pending' })
    st().applyEntry('h1', { convKey: 'c1', sessionId: 's1', entry: refreshEntry(12, 'ok') })
    expect(selectRefreshPending(conv())).toBe(false)
  })

  it('and false once the entry failed', async () => {
    await ask()
    st().applyEntry('h1', { convKey: 'c1', sessionId: 's1', entry: refreshEntry(12, 'failed') })
    expect(selectRefreshPending(conv())).toBe(false)
  })

  it('the entry\'s terminal event that outruns the 202 is not undone by it', async () => {
    const d = (() => { let r!: (v: RefreshResult) => void; const p = new Promise<RefreshResult>((res) => { r = res }); return { p, r } })()
    postRefresh.mockReturnValueOnce(d.p)
    const asked = st().requestRefresh('h1', 'c1')
    st().applyEntry('h1', { convKey: 'c1', sessionId: 's1', entry: refreshEntry(12, 'ok') })
    d.r({ kind: 'accepted', entryId: 12 })
    await asked
    expect(selectRefreshPending(conv())).toBe(false)
  })

  it('a reconnect cannot leave it stuck: the refetch that sees the entry terminal ends it', async () => {
    await ask()
    expect(selectRefreshPending(conv())).toBe(true)
    st().fence('h1') // the connection dropped while pending; its terminal event was missed
    expect(selectRefreshPending(conv())).toBe(true) // unknown until the new generation answers…
    st().setSupport('h1', V2)
    fetchConversation.mockResolvedValue(page([refreshEntry(12, 'ok'), entry(3)]))
    await st().loadSeat('h1', 's1')
    expect(selectRefreshPending(conv())).toBe(false) // …and the refetch ended it
  })

  it('a pending refresh entry seen by a fetch (started elsewhere) is pending too', async () => {
    fetchConversation.mockResolvedValue(page([refreshEntry(13, 'pending'), entry(3)]))
    await st().openWorkbook('h1', 's1')
    expect(selectRefreshPending(conv())).toBe(true)
  })

  it('a turn entry that is pending does not count', () => {
    st().applyEntry('h1', { convKey: 'c1', sessionId: 's1', entry: entry(20, { state: 'pending' }) })
    expect(selectRefreshPending(conv())).toBe(false)
  })
})

describe('refreshAvailable', () => {
  it('comes from the conversation answer, then follows workbook.refresh_available, and clears on a new connection', async () => {
    st().setSupport('h1', V2)
    fetchConversation.mockResolvedValue(page([entry(3)], { refreshAvailable: true }))
    await st().openWorkbook('h1', 's1')
    expect(conv()?.refreshAvailable).toBe(true)
    st().applyRefreshAvailable('h1', { convKey: 'c1', available: false })
    expect(conv()?.refreshAvailable).toBe(false)
    st().applyRefreshAvailable('h1', { convKey: 'c1', available: true })
    expect(conv()?.refreshAvailable).toBe(true)
    st().fence('h1')
    expect(conv()?.refreshAvailable).toBe(false)
  })

  it('a late conversation answer does not overwrite a newer workbook.refresh_available event', async () => {
    st().setSupport('h1', V2)
    let resolve!: (v: ConversationResult) => void
    fetchConversation.mockReturnValueOnce(new Promise<ConversationResult>((r) => { resolve = r }))
    const loading = st().openWorkbook('h1', 's1')
    st().applyRefreshAvailable('h1', { convKey: 'c1', available: false }) // after the request started
    resolve(page([entry(3)], { refreshAvailable: true })) // the answer was computed before the event
    await loading
    expect(conv()?.refreshAvailable).toBe(false)
  })

  it('of two answers in flight (different queries), the one whose request started later wins whichever lands last', async () => {
    st().setSupport('h1', V2)
    const defer = () => { let r!: (v: ConversationResult) => void; const p = new Promise<ConversationResult>((res) => { r = res }); return { p, r } }
    const older = defer(); const newer = defer()
    fetchConversation.mockReturnValueOnce(older.p).mockReturnValueOnce(newer.p)
    const a = st().loadSeat('h1', 's1') // limit 1, started first
    const b = st().openWorkbook('h1', 's1') // limit 20, started second
    newer.r(page([entry(3)], { refreshAvailable: true, todos: { open: [todo(5)], done: [] } }))
    await b
    older.r(page([entry(3)], { refreshAvailable: false, todos: { open: [], done: [] } })) // computed earlier, lands later
    await a
    expect(conv()?.refreshAvailable).toBe(true)
    expect(ids(conv()?.todos.open)).toEqual([5])
  })

  it('an answer that started after the last event does apply', async () => {
    st().setSupport('h1', V2)
    st().applyRefreshAvailable('h1', { convKey: 'c1', available: false })
    fetchConversation.mockResolvedValue(page([entry(3)], { refreshAvailable: true }))
    await st().openWorkbook('h1', 's1')
    expect(conv()?.refreshAvailable).toBe(true)
  })

  it('an answer without the flag (v1) leaves it', async () => {
    st().setSupport('h1', V2)
    st().applyRefreshAvailable('h1', { convKey: 'c1', available: true })
    fetchConversation.mockResolvedValue(page([entry(3)], { refreshAvailable: null }))
    await st().openWorkbook('h1', 's1')
    expect(conv()?.refreshAvailable).toBe(true)
  })
})

describe('events never fetch', () => {
  it('50 todos events and 50 refresh_available events leave the fetch counts alone', () => {
    st().setSupport('h1', V2)
    for (let i = 1; i <= 50; i++) {
      st().applyTodos('h1', { convKey: 'c1', sessionId: 's1', todos: [todo(i), todo(i, 'done')] })
      st().applyRefreshAvailable('h1', { convKey: 'c1', available: i % 2 === 0 })
    }
    expect(fetchConversation).not.toHaveBeenCalled()
    expect(fetchTodos).not.toHaveBeenCalled()
    expect(postRefresh).not.toHaveBeenCalled()
    expect(conv()?.todos.done).toHaveLength(50)
  })
})

describe('loadUntil', () => {
  // The daemon's pages: 20 per page, newest first, ids counting down from 200.
  const serve = () => fetchConversation.mockImplementation(async (_h, _p, _s, q = {}) => {
    const top = (q.before ?? 201) - 1
    return page(Array.from({ length: Math.min(q.limit ?? 20, top) }, (_, i) => entry(top - i)))
  })
  beforeEach(() => { st().setSupport('h1', V1); serve() })

  it('is true at once when the entry is loaded', async () => {
    await st().openWorkbook('h1', 's1') // 200..181
    fetchConversation.mockClear()
    expect(await st().loadUntil('h1', 'c1', 190)).toBe(true)
    expect(fetchConversation).not.toHaveBeenCalled()
  })

  it('pages before = oldest loaded and finds an entry on the third page', async () => {
    await st().openWorkbook('h1', 's1') // page 1: 200..181
    fetchConversation.mockClear()
    expect(await st().loadUntil('h1', 'c1', 145)).toBe(true) // 180..161, 160..141: two more pages (the third overall)
    expect(fetchConversation).toHaveBeenCalledTimes(2)
    expect(fetchConversation.mock.calls[0][3]).toEqual({ limit: 20, before: 181 })
    expect(fetchConversation.mock.calls[1][3]).toEqual({ limit: 20, before: 161 })
  })

  it('gives up after MAX_UNTIL_PAGES pages and answers false', async () => {
    await st().openWorkbook('h1', 's1')
    fetchConversation.mockClear()
    expect(await st().loadUntil('h1', 'c1', 1)).toBe(false)
    expect(fetchConversation).toHaveBeenCalledTimes(MAX_UNTIL_PAGES)
    expect(conv()?.entries).toHaveLength(20 + MAX_UNTIL_PAGES * 20)
  })

  it('with nothing loaded the first fetch is the first page', async () => {
    expect(await st().loadUntil('h1', 'c1', 150)).toBe(true) // 200..181, then 180..161, 160..141
    expect(fetchConversation).toHaveBeenCalledTimes(3)
    expect(fetchConversation.mock.calls[0][3]).toEqual({ limit: 20 })
  })

  it('false when the entry is not there: older than the oldest page, or past the cursor', async () => {
    fetchConversation.mockImplementation(async (_h, _p, _s, q = {}) => {
      const top = (q.before ?? 11) - 1
      return page(Array.from({ length: Math.min(q.limit ?? 20, top) }, (_, i) => entry(top - i)))
    })
    await st().openWorkbook('h1', 's1') // 10..1, short page: exhausted
    fetchConversation.mockClear()
    expect(await st().loadUntil('h1', 'c1', 99)).toBe(false)
    expect(await st().loadUntil('h1', 'c1', 0)).toBe(false)
    expect(fetchConversation).not.toHaveBeenCalled()
  })

  it('a failing page stops the walk instead of spinning', async () => {
    await st().openWorkbook('h1', 's1')
    fetchConversation.mockClear()
    fetchConversation.mockRejectedValue(new Error('network'))
    expect(await st().loadUntil('h1', 'c1', 1)).toBe(false)
    expect(fetchConversation).toHaveBeenCalledTimes(1)
  })

  it('without workbook.v1 it fetches nothing', async () => {
    st().fence('h1')
    expect(await st().loadUntil('h1', 'c1', 5)).toBe(false)
    expect(fetchConversation).not.toHaveBeenCalled()
  })
})
