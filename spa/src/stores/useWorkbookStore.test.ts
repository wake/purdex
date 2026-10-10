import { describe, it, expect, beforeEach, vi } from 'vitest'
import type { ConversationResult } from '../lib/workbook/api'
import type { WorkbookEntry } from '../lib/workbook/types'

const fetchConversation = vi.fn<(hostId: string, provider: string, sessionId: string, q?: { limit?: number; before?: number }) => Promise<ConversationResult>>()
vi.mock('../lib/workbook/api', () => ({ fetchConversation: (...a: Parameters<typeof fetchConversation>) => fetchConversation(...a) }))

import { MAX_ENTRIES, MAX_UNPINNED, selectConv, selectWorkbookSupport, useWorkbookStore } from './useWorkbookStore'

const entry = (id: number, over: Partial<WorkbookEntry> = {}): WorkbookEntry => ({
  id, convKey: 'c1', sessionId: 's1', turnId: `t${id}`, turnAt: id * 1000, state: 'ok', reason: '', thing: `thing ${id}`, push: '', entry: '',
  thingDone: false, createdAt: id * 1000, updatedAt: id * 1000, ...over,
})
const page = (entries: WorkbookEntry[], over: { status?: string; statusAt?: number; convKey?: string } = {}): ConversationResult =>
  ({ kind: 'ok', page: { convKey: over.convKey ?? 'c1', status: over.status ?? 'doing', statusAt: over.statusAt ?? 5, entries } })
const st = () => useWorkbookStore.getState()
const conv = (key = 'c1') => selectConv(st(), 'h1', key)
const V1 = { v1: true, v2: false }
const flush = () => new Promise((r) => setTimeout(r, 0))
function deferred<T>() {
  let resolve!: (v: T) => void
  const promise = new Promise<T>((res) => { resolve = res })
  return { promise, resolve }
}

beforeEach(() => {
  st().reset()
  fetchConversation.mockReset()
  fetchConversation.mockResolvedValue(page([entry(3), entry(2)]))
})

describe('support', () => {
  it('is false until the probe answered, then follows it', () => {
    expect(selectWorkbookSupport('h1')).toMatchObject({ v1: false, v2: false })
    st().setSupport('h1', { v1: true, v2: true })
    expect(selectWorkbookSupport('h1')).toMatchObject({ v1: true, v2: true })
  })
})

describe('loadSeat', () => {
  it('capability off (or unknown): no fetch at all', async () => {
    await st().loadSeat('h1', 's1')
    st().setSupport('h1', { v1: false, v2: false })
    await st().loadSeat('h1', 's1')
    await st().openWorkbook('h1', 's1')
    expect(fetchConversation).not.toHaveBeenCalled()
  })

  it('fetches limit 1 once per seat in a generation, however often it appears', async () => {
    st().setSupport('h1', V1)
    await Promise.all([st().loadSeat('h1', 's1'), st().loadSeat('h1', 's1')])
    await st().loadSeat('h1', 's1')
    await st().loadSeat('h1', 's2')
    expect(fetchConversation).toHaveBeenCalledTimes(2)
    expect(fetchConversation).toHaveBeenNthCalledWith(1, 'h1', 'claude', 's1', { limit: 1 })
    expect(st().convOfSession.h1.s1).toBe('c1')
  })

  it('a reconnect (fence, then a new support answer) fetches each seat once more', async () => {
    st().setSupport('h1', V1)
    await st().loadSeat('h1', 's1')
    await st().loadSeat('h1', 's2')
    st().fence('h1')
    st().setSupport('h1', V1)
    await st().loadSeat('h1', 's1')
    await st().loadSeat('h1', 's1')
    await st().loadSeat('h1', 's2')
    expect(fetchConversation).toHaveBeenCalledTimes(4)
  })

  it('404 marks the session missing; an entry event for it clears that and does not fetch', async () => {
    st().setSupport('h1', V1)
    fetchConversation.mockResolvedValue({ kind: 'not_found' })
    await st().loadSeat('h1', 's1')
    expect(st().missingSessions.h1.s1).toBe(true)
    fetchConversation.mockClear()
    for (let i = 1; i <= 50; i++) st().applyEntry('h1', { convKey: 'c1', sessionId: 's1', entry: entry(i) })
    expect(st().missingSessions.h1.s1).toBeUndefined()
    expect(st().convOfSession.h1.s1).toBe('c1')
    expect(conv()?.entries).toHaveLength(50)
    expect(fetchConversation).not.toHaveBeenCalled()
  })

  it('a new generation forgets the 404', async () => {
    st().setSupport('h1', V1)
    fetchConversation.mockResolvedValue({ kind: 'not_found' })
    await st().loadSeat('h1', 's1')
    st().fence('h1')
    st().setSupport('h1', V1)
    expect(st().missingSessions.h1).toEqual({})
  })

  it('a failed fetch leaves the store alone and does not throw', async () => {
    st().setSupport('h1', V1)
    fetchConversation.mockRejectedValue(new Error('network'))
    await expect(st().loadSeat('h1', 's1')).resolves.toBeUndefined()
    expect(st().byHost.h1).toBeUndefined()
  })
})

describe('openWorkbook and loadMore', () => {
  it('loads 20, then pages with before = oldestId until a short page', async () => {
    st().setSupport('h1', V1)
    fetchConversation.mockResolvedValueOnce(page([entry(9), entry(8), entry(7)]))
    await st().openWorkbook('h1', 's1')
    expect(fetchConversation).toHaveBeenLastCalledWith('h1', 'claude', 's1', { limit: 20 })
    expect(conv()?.oldestId).toBe(7)
    expect(conv()?.exhausted).toBe(true) // 3 < 20
  })

  it('「更多」 asks before=oldestId (the conversation key names the session) and merges', async () => {
    st().setSupport('h1', V1)
    fetchConversation.mockResolvedValueOnce(page(Array.from({ length: 20 }, (_, i) => entry(40 - i)))) // 40..21
    await st().openWorkbook('h1', 's1')
    expect(conv()?.exhausted).toBe(false)
    fetchConversation.mockResolvedValueOnce(page([entry(21), entry(20), entry(19)]))
    await st().loadMore('h1', 'c1')
    expect(fetchConversation).toHaveBeenLastCalledWith('h1', 'claude', 'c1', { limit: 20, before: 21 })
    expect(conv()?.entries.map((e) => e.id)).toEqual([40, 39, 38, 37, 36, 35, 34, 33, 32, 31, 30, 29, 28, 27, 26, 25, 24, 23, 22, 21, 20, 19])
    expect(conv()?.oldestId).toBe(19)
    expect(conv()?.exhausted).toBe(true)
    await st().loadMore('h1', 'c1') // nothing older: no request
    expect(fetchConversation).toHaveBeenCalledTimes(2)
  })

  it('does not start a second load while one is out', async () => {
    st().setSupport('h1', V1)
    await st().openWorkbook('h1', 's1')
    const d = deferred<ConversationResult>()
    fetchConversation.mockClear()
    fetchConversation.mockReturnValueOnce(d.promise)
    const first = st().openWorkbook('h1', 's1')
    await st().openWorkbook('h1', 's1')
    expect(fetchConversation).toHaveBeenCalledTimes(1)
    d.resolve(page([entry(3)]))
    await first
    expect(conv()?.loading).toBe(false)
  })

  it('opening a view fetches every time (one per view opened)', async () => {
    st().setSupport('h1', V1)
    await st().openWorkbook('h1', 's1')
    await st().openWorkbook('h1', 's1')
    expect(fetchConversation).toHaveBeenCalledTimes(2)
  })
})

describe('upsert, dedupe, order', () => {
  it('keeps entries newest first, one per id, and a stale copy never replaces a newer one', () => {
    st().applyEntry('h1', { convKey: 'c1', sessionId: 's1', entry: entry(2, { state: 'pending', updatedAt: 100 }) })
    st().applyEntry('h1', { convKey: 'c1', sessionId: 's1', entry: entry(5) })
    st().applyEntry('h1', { convKey: 'c1', sessionId: 's1', entry: entry(2, { state: 'ok', updatedAt: 200 }) })
    st().applyEntry('h1', { convKey: 'c1', sessionId: 's1', entry: entry(2, { state: 'pending', updatedAt: 100 }) })
    expect(conv()?.entries.map((e) => [e.id, e.state])).toEqual([[5, 'ok'], [2, 'ok']])
  })

  it('a page merges with what events brought in, without duplicating', async () => {
    st().setSupport('h1', V1)
    st().applyEntry('h1', { convKey: 'c1', sessionId: 's1', entry: entry(4) })
    await st().loadSeat('h1', 's1') // page: 3, 2
    expect(conv()?.entries.map((e) => e.id)).toEqual([4, 3, 2])
    expect(conv()?.oldestId).toBe(4) // events do not move the paging cursor
  })
})

describe('status', () => {
  it('after a reload a status event lands through session_id, and an older one is ignored', () => {
    st().applyStatus('h1', { convKey: 'c1', sessionId: 's9', status: 'new', updatedAt: 50 })
    expect(st().convOfSession.h1.s9).toBe('c1')
    expect(conv()).toMatchObject({ status: 'new', statusAt: 50 })
    st().applyStatus('h1', { convKey: 'c1', sessionId: 's9', status: 'older', updatedAt: 10 })
    expect(conv()?.status).toBe('new')
  })

  it('a status for a conversation no seat maps to yet is kept', () => {
    st().applyStatus('h1', { convKey: 'zzz', sessionId: 'sx', status: 'kept', updatedAt: 1 })
    expect(conv('zzz')?.status).toBe('kept')
  })
})

describe('forget and fences', () => {
  it('forgetHost drops everything of the host and keeps the others', () => {
    st().setSupport('h1', V1)
    st().setSupport('h2', V1)
    st().applyEntry('h1', { convKey: 'c1', sessionId: 's1', entry: entry(1) })
    st().applyEntry('h2', { convKey: 'c1', sessionId: 's1', entry: entry(1) })
    st().forgetHost('h1')
    expect(st().byHost.h1).toBeUndefined()
    expect(st().convOfSession.h1).toBeUndefined()
    expect(selectWorkbookSupport('h1').v1).toBe(false)
    expect(st().byHost.h2).toBeDefined()
  })

  it('an answer that lands after the host was forgotten, or after a newer generation, is dropped', async () => {
    st().setSupport('h1', V1)
    const d = deferred<ConversationResult>()
    fetchConversation.mockReturnValueOnce(d.promise)
    const p = st().loadSeat('h1', 's1')
    st().forgetHost('h1')
    d.resolve(page([entry(3)]))
    await p
    expect(st().byHost.h1).toBeUndefined()

    st().setSupport('h1', V1)
    const d2 = deferred<ConversationResult>()
    fetchConversation.mockReturnValueOnce(d2.promise)
    const p2 = st().loadSeat('h1', 's1')
    st().fence('h1') // the old connection's answer is not this one's
    d2.resolve(page([entry(3)]))
    await p2
    await flush()
    expect(st().byHost.h1).toBeUndefined()
  })
})

describe('generation fence', () => {
  it('the same answer again on an unbroken connection (a periodic probe) is not a new generation: no refetch', async () => {
    st().setSupport('h1', V1)
    await st().loadSeat('h1', 's1')
    for (let i = 0; i < 5; i++) { st().setSupport('h1', V1); await st().loadSeat('h1', 's1') }
    expect(fetchConversation).toHaveBeenCalledTimes(1)
  })

  it('a fence makes support unknown at once (v1 false), keeps the loaded entries, and clears loading', async () => {
    st().setSupport('h1', { v1: true, v2: true })
    await st().loadSeat('h1', 's1')
    st().fence('h1')
    expect(selectWorkbookSupport('h1')).toMatchObject({ v1: false, v2: false })
    expect(conv()?.entries).toHaveLength(2)
    await st().loadSeat('h1', 's1') // support unknown: no request
    await st().openWorkbook('h1', 's1')
    expect(fetchConversation).toHaveBeenCalledTimes(1)
  })

  it('a request out when the daemon restarts writes nothing, even though the new probe has not answered (or fails)', async () => {
    st().setSupport('h1', V1)
    const d = deferred<ConversationResult>()
    fetchConversation.mockReturnValueOnce(d.promise)
    const p = st().loadSeat('h1', 's1')
    st().fence('h1') // the socket closed / a new probe started; no answer yet
    d.resolve(page([entry(3)]))
    await p
    expect(st().byHost.h1).toBeUndefined()
    expect(st().convOfSession.h1?.s1).toBeUndefined()
  })

  it('a request out for the old generation does not swallow the new generation\'s request for the same seat', async () => {
    st().setSupport('h1', V1)
    const d = deferred<ConversationResult>()
    fetchConversation.mockReturnValueOnce(d.promise)
    const old = st().loadSeat('h1', 's1')
    st().fence('h1')
    st().setSupport('h1', V1)
    await st().loadSeat('h1', 's1')
    expect(fetchConversation).toHaveBeenCalledTimes(2)
    expect(conv()?.entries).toHaveLength(2)
    d.resolve(page([entry(99)]))
    await old
    expect(conv()?.entries.map((e) => e.id)).toEqual([3, 2])
  })
})

describe('in-flight requests', () => {
  it('two opens of a session whose conversation is not known yet send one request', async () => {
    st().setSupport('h1', V1)
    const d = deferred<ConversationResult>()
    fetchConversation.mockReturnValueOnce(d.promise)
    const a = st().openWorkbook('h1', 'plain')
    const b = st().openWorkbook('h1', 'plain')
    expect(fetchConversation).toHaveBeenCalledTimes(1)
    d.resolve(page([entry(3)]))
    await Promise.all([a, b])
    await st().openWorkbook('h1', 'plain') // answered: a later open asks again
    expect(fetchConversation).toHaveBeenCalledTimes(2)
  })
})

describe('retention', () => {
  const keys = () => Object.keys(st().byHost.h1?.byConv ?? {})

  it('a flood of status and entry frames for other conversations stays within bounds, with no fetch', async () => {
    st().setSupport('h1', V1)
    await st().loadSeat('h1', 's1') // seat s1 → c1
    fetchConversation.mockClear()
    st().setViewing('h1', 'viewed', true)
    st().applyStatus('h1', { convKey: 'viewed', sessionId: 'sv', status: 'v', updatedAt: 1 })
    for (let i = 0; i < 400; i++) {
      st().applyStatus('h1', { convKey: `x${i}`, sessionId: `sx${i}`, status: 's', updatedAt: 1 })
      st().applyEntry('h1', { convKey: `y${i}`, sessionId: `sy${i}`, entry: entry(i + 1, { convKey: `y${i}`, sessionId: `sy${i}` }) })
    }
    expect(keys().length).toBeLessThanOrEqual(MAX_UNPINNED + 2)
    expect(keys()).toEqual(expect.arrayContaining(['c1', 'viewed']))
    expect(Object.keys(st().convOfSession.h1).length).toBeLessThanOrEqual(MAX_UNPINNED + 3) // mappings go with their conversation
    expect(keys()).toContain('y399') // the most recent survive
    expect(keys()).not.toContain('x0')
    expect(fetchConversation).not.toHaveBeenCalled()
  })

  it('a closed view no longer holds its conversation', () => {
    st().setViewing('h1', 'v', true)
    st().applyStatus('h1', { convKey: 'v', sessionId: 'sv', status: 'v', updatedAt: 1 })
    st().setViewing('h1', 'v', false)
    for (let i = 0; i < MAX_UNPINNED + 5; i++) st().applyStatus('h1', { convKey: `x${i}`, sessionId: `sx${i}`, status: 's', updatedAt: 1 })
    expect(keys()).not.toContain('v')
  })

  it('keeps the newest MAX_ENTRIES entries; the cursor points at the oldest kept and 「更多」 is open again', () => {
    for (let i = 1; i <= MAX_ENTRIES + 30; i++) st().applyEntry('h1', { convKey: 'c1', sessionId: 's1', entry: entry(i) })
    const c = conv()!
    expect(c.entries).toHaveLength(MAX_ENTRIES)
    expect(c.entries[0].id).toBe(MAX_ENTRIES + 30)
    expect(c.oldestId).toBe(31)
    expect(c.exhausted).toBe(false)
  })
})
