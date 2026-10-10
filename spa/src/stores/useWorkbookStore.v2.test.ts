// spa/src/stores/useWorkbookStore.v2.test.ts — WA-1b: the v2 data layer (todos, refresh, loadUntil) on top of WA-1a's rules.
import { describe, it, expect, beforeEach, vi } from 'vitest'
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

import { MAX_DONE_TODOS, MAX_OPEN_TODOS, MAX_TODO_TOUCHES, MAX_UNTIL_PAGES, selectConv, selectRefreshPending, useWorkbookStore } from './useWorkbookStore'

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

  it('the conversation answer is a snapshot: it replaces open, merges done, keeps events newer than itself', async () => {
    st().setSupport('h1', V2)
    st().applyTodos('h1', { convKey: 'c1', sessionId: 's1', todos: [todo(1), todo(2), todo(9)] }) // 1 closed server-side meanwhile; 9 is newer than the answer
    fetchConversation.mockResolvedValue(page([entry(3)], { todos: { open: [todo(2), todo(5)], done: [todo(4, 'done'), todo(3, 'done')] } }))
    await st().openWorkbook('h1', 's1')
    expect(ids(conv()?.todos.open)).toEqual([2, 5, 9])
    expect(ids(conv()?.todos.done)).toEqual([4, 3])
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

  it('at MAX_DONE_TODOS the cursor still advances past an evicted page, never refetches the same page, and paging stops (doneCapped)', async () => {
    st().setSupport('h1', V2)
    st().applyTodos('h1', { convKey: 'c1', sessionId: 's1', todos: Array.from({ length: MAX_DONE_TODOS }, (_, i) => todo(1201 - i, 'done')) }) // 1201..1002
    expect(conv()?.todos.doneCapped).toBe(false)
    const oldest = conv()?.todos.doneOldestId
    fetchTodos.mockResolvedValueOnce({ kind: 'ok', todos: Array.from({ length: 20 }, (_, i) => todo(1001 - i, 'done')) }) // a full page, all older: retention drops it
    await st().loadMoreDone('h1', 'c1')
    expect(fetchTodos).toHaveBeenLastCalledWith('h1', 's1', { state: 'done', limit: 20, before: oldest })
    expect(conv()?.todos.done).toHaveLength(MAX_DONE_TODOS)
    expect(conv()?.todos.doneCursor).toBe(982) // advanced to the page's oldest even though the page was not kept
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
